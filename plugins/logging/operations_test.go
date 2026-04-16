package logging

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/logstore"
	"github.com/maximhq/bifrost/framework/streaming"
)

type testLogger struct{}

func (testLogger) Debug(string, ...any)                   {}
func (testLogger) Info(string, ...any)                    {}
func (testLogger) Warn(string, ...any)                    {}
func (testLogger) Error(string, ...any)                   {}
func (testLogger) Fatal(string, ...any)                   {}
func (testLogger) SetLevel(schemas.LogLevel)              {}
func (testLogger) SetOutputType(schemas.LoggerOutputType) {}
func (testLogger) LogHTTPRequest(schemas.LogLevel, string) schemas.LogEventBuilder {
	return schemas.NoopLogEvent
}

func newTestStore(t *testing.T) logstore.LogStore {
	t.Helper()

	store, err := logstore.NewLogStore(context.Background(), &logstore.Config{
		Enabled: true,
		Type:    logstore.LogStoreTypeSQLite,
		Config: &logstore.SQLiteConfig{
			Path: filepath.Join(t.TempDir(), "logging.db"),
		},
	}, testLogger{})
	if err != nil {
		t.Fatalf("NewLogStore() error = %v", err)
	}
	return store
}

func newAsyncTestPlugin(t *testing.T, store logstore.LogStore) *LoggerPlugin {
	t.Helper()

	plugin, err := Init(context.Background(), &Config{}, testLogger{}, store, nil, nil)
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	t.Cleanup(func() {
		if err := plugin.Cleanup(); err != nil {
			t.Fatalf("Cleanup() error = %v", err)
		}
	})

	return plugin
}

func waitForLog(t *testing.T, store logstore.LogStore, id string) *logstore.Log {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		logEntry, err := store.FindByID(context.Background(), id)
		if err == nil {
			return logEntry
		}
		time.Sleep(25 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for log %s", id)
	return nil
}

func TestUpdateLogEntryPreservesResponsesInputContentSummary(t *testing.T) {
	store := newTestStore(t)
	plugin := &LoggerPlugin{
		store:  store,
		logger: testLogger{},
	}

	requestID := "req-1"
	now := time.Now().UTC()
	inputText := "request-side text"
	initial := &InitialLogData{
		Object:   "responses",
		Provider: "openai",
		Model:    "gpt-4o-mini",
		ResponsesInputHistory: []schemas.ResponsesMessage{{
			Content: &schemas.ResponsesMessageContent{
				ContentStr: &inputText,
			},
		}},
	}

	if err := plugin.insertInitialLogEntry(context.Background(), requestID, "", now, 0, nil, nil, initial); err != nil {
		t.Fatalf("insertInitialLogEntry() error = %v", err)
	}

	responsesText := "responses output"
	update := &UpdateLogData{
		Status: "success",
		ResponsesOutput: []schemas.ResponsesMessage{{
			Content: &schemas.ResponsesMessageContent{
				ContentStr: &responsesText,
			},
		}},
	}

	if err := plugin.updateLogEntry(context.Background(), requestID, "", "", 10, "", "", "", "", 0, nil, nil, "", update); err != nil {
		t.Fatalf("updateLogEntry() error = %v", err)
	}

	logEntry, err := store.FindByID(context.Background(), requestID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if !strings.Contains(logEntry.ContentSummary, inputText) {
		t.Fatalf("expected content summary to preserve responses input, got %q", logEntry.ContentSummary)
	}
	if strings.Contains(logEntry.ContentSummary, responsesText) {
		t.Fatalf("expected content summary to avoid overwriting with responses output-only data, got %q", logEntry.ContentSummary)
	}
}

func TestUpdateLogEntryUpdatesContentSummaryForChatOutput(t *testing.T) {
	store := newTestStore(t)
	plugin := &LoggerPlugin{
		store:  store,
		logger: testLogger{},
	}

	requestID := "req-chat"
	now := time.Now().UTC()
	initial := &InitialLogData{
		Object:   "chat_completion",
		Provider: "openai",
		Model:    "gpt-4o-mini",
	}

	if err := plugin.insertInitialLogEntry(context.Background(), requestID, "", now, 0, nil, nil, initial); err != nil {
		t.Fatalf("insertInitialLogEntry() error = %v", err)
	}

	chatText := "assistant output"
	update := &UpdateLogData{
		Status: "success",
		ChatOutput: &schemas.ChatMessage{
			Role: schemas.ChatMessageRoleAssistant,
			Content: &schemas.ChatMessageContent{
				ContentStr: &chatText,
			},
		},
	}

	if err := plugin.updateLogEntry(context.Background(), requestID, "", "", 10, "", "", "", "", 0, nil, nil, "", update); err != nil {
		t.Fatalf("updateLogEntry() error = %v", err)
	}

	logEntry, err := store.FindByID(context.Background(), requestID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if !strings.Contains(logEntry.ContentSummary, chatText) {
		t.Fatalf("expected content summary to include chat output, got %q", logEntry.ContentSummary)
	}
}

func TestUpdateLogEntrySuppressesChatOutputWhenContentLoggingDisabled(t *testing.T) {
	store := newTestStore(t)
	disableContentLogging := true
	plugin := &LoggerPlugin{
		store:                 store,
		logger:                testLogger{},
		disableContentLogging: &disableContentLogging,
	}

	requestID := "req-chat-disabled"
	now := time.Now().UTC()
	initial := &InitialLogData{
		Object:   "chat_completion",
		Provider: "openai",
		Model:    "gpt-4o-mini",
	}

	if err := plugin.insertInitialLogEntry(context.Background(), requestID, "", now, 0, nil, nil, initial); err != nil {
		t.Fatalf("insertInitialLogEntry() error = %v", err)
	}

	chatText := "assistant output should not be logged"
	update := &UpdateLogData{
		Status: "success",
		ChatOutput: &schemas.ChatMessage{
			Role: schemas.ChatMessageRoleAssistant,
			Content: &schemas.ChatMessageContent{
				ContentStr: &chatText,
			},
		},
	}

	if err := plugin.updateLogEntry(context.Background(), requestID, "", "", 10, "", "", "", "", 0, nil, nil, "", update); err != nil {
		t.Fatalf("updateLogEntry() error = %v", err)
	}

	logEntry, err := store.FindByID(context.Background(), requestID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if logEntry.OutputMessage != "" {
		t.Fatalf("expected output_message to be suppressed, got %q", logEntry.OutputMessage)
	}
	if strings.Contains(logEntry.ContentSummary, chatText) {
		t.Fatalf("expected content summary to suppress chat output, got %q", logEntry.ContentSummary)
	}
}

func TestUpdateStreamingLogEntryPreservesResponsesInputContentSummary(t *testing.T) {
	store := newTestStore(t)
	plugin := &LoggerPlugin{
		store:  store,
		logger: testLogger{},
	}

	requestID := "req-stream"
	now := time.Now().UTC()
	inputText := "stream request-side text"
	initial := &InitialLogData{
		Object:   "responses_stream",
		Provider: "openai",
		Model:    "gpt-4o-mini",
		ResponsesInputHistory: []schemas.ResponsesMessage{{
			Content: &schemas.ResponsesMessageContent{
				ContentStr: &inputText,
			},
		}},
	}

	if err := plugin.insertInitialLogEntry(context.Background(), requestID, "", now, 0, nil, nil, initial); err != nil {
		t.Fatalf("insertInitialLogEntry() error = %v", err)
	}

	responsesText := "streamed response text"
	streamResponse := &streaming.ProcessedStreamResponse{
		Data: &streaming.AccumulatedData{
			Latency: 25,
			TokenUsage: &schemas.BifrostLLMUsage{
				PromptTokens:     10,
				CompletionTokens: 5,
				TotalTokens:      15,
			},
			OutputMessages: []schemas.ResponsesMessage{{
				Content: &schemas.ResponsesMessageContent{
					ContentStr: &responsesText,
				},
			}},
		},
	}

	if err := plugin.updateStreamingLogEntry(context.Background(), requestID, "", "", "", "", "", "", 0, nil, nil, "", streamResponse, true, false, false); err != nil {
		t.Fatalf("updateStreamingLogEntry() error = %v", err)
	}

	logEntry, err := store.FindByID(context.Background(), requestID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if logEntry.TokenUsageParsed == nil || logEntry.TokenUsageParsed.TotalTokens != 15 {
		t.Fatalf("expected token usage to be updated, got %+v", logEntry.TokenUsageParsed)
	}
	if !strings.Contains(logEntry.ContentSummary, inputText) {
		t.Fatalf("expected content summary to preserve responses input, got %q", logEntry.ContentSummary)
	}
	if strings.Contains(logEntry.ContentSummary, responsesText) {
		t.Fatalf("expected content summary to avoid overwriting with streamed responses output-only data, got %q", logEntry.ContentSummary)
	}
}

func TestBuildInitialLogEntryCarriesRouteLayerIndex(t *testing.T) {
	entry := buildInitialLogEntry(&PendingLogData{
		RequestID:       "req-layer-initial",
		Timestamp:       time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC),
		FallbackIndex:   1,
		RouteLayerIndex: intPtr(2),
		InitialData: &InitialLogData{
			Object:   "chat_completion",
			Provider: "openai",
			Model:    "gpt-4.1",
		},
	})

	if entry.RouteLayerIndex == nil || *entry.RouteLayerIndex != 2 {
		t.Fatalf("expected route_layer_index=2, got %+v", entry.RouteLayerIndex)
	}
}

func TestBuildCompleteLogEntryFromPendingCarriesRouteLayerIndex(t *testing.T) {
	entry := buildCompleteLogEntryFromPending(&PendingLogData{
		RequestID:       "req-layer-complete",
		Timestamp:       time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC),
		FallbackIndex:   2,
		RouteLayerIndex: intPtr(1),
		InitialData: &InitialLogData{
			Object:   "chat_completion",
			Provider: "azure",
			Model:    "gpt-4.1",
		},
	})

	if entry.RouteLayerIndex == nil || *entry.RouteLayerIndex != 1 {
		t.Fatalf("expected route_layer_index=1, got %+v", entry.RouteLayerIndex)
	}
}

func TestPostLLMHook_MinimalErrorEntryCarriesRouteLayerIndex(t *testing.T) {
	store := newTestStore(t)
	plugin := newAsyncTestPlugin(t, store)

	ctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(time.Minute))
	ctx.SetValue(schemas.BifrostContextKeyRequestID, "req-layer-error")
	ctx.SetValue(schemas.BifrostContextKeyRouteLayerIndex, 1)

	_, _, err := plugin.PostLLMHook(ctx, nil, &schemas.BifrostError{
		Error: &schemas.ErrorField{
			Message: "upstream failed",
		},
		ExtraFields: schemas.BifrostErrorExtraFields{
			Provider:       schemas.OpenAI,
			ModelRequested: "gpt-4.1",
		},
	})
	if err != nil {
		t.Fatalf("PostLLMHook() error = %v", err)
	}

	logEntry := waitForLog(t, store, "req-layer-error")
	if logEntry.RouteLayerIndex == nil || *logEntry.RouteLayerIndex != 1 {
		t.Fatalf("expected minimal error route_layer_index=1, got %+v", logEntry.RouteLayerIndex)
	}
}

func TestPostLLMHook_UsesCurrentRouteLayerIndexForFallbackAttempt(t *testing.T) {
	store := newTestStore(t)
	plugin := newAsyncTestPlugin(t, store)

	requestID := "req-layer-fallback"
	plugin.pendingLogs.Store(requestID, &PendingLogData{
		RequestID:       requestID,
		ParentRequestID: "req-layer-primary",
		Timestamp:       time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC),
		FallbackIndex:   1,
		RouteLayerIndex: intPtr(0),
		InitialData: &InitialLogData{
			Object:   "chat_completion",
			Provider: "openrouter",
			Model:    "gemma-4-31b-it",
		},
		CreatedAt: time.Now(),
		Status:    "processing",
	})

	ctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(time.Minute))
	ctx.SetValue(schemas.BifrostContextKeyRequestID, requestID)
	ctx.SetValue(schemas.BifrostContextKeyRouteLayerIndex, 1)

	result := &schemas.BifrostResponse{
		ChatResponse: &schemas.BifrostChatResponse{
			Model: "gemma-4-31b-it",
			Choices: []schemas.BifrostResponseChoice{{
				ChatNonStreamResponseChoice: &schemas.ChatNonStreamResponseChoice{
					Message: &schemas.ChatMessage{
						Role: schemas.ChatMessageRoleAssistant,
					},
				},
			}},
			ExtraFields: schemas.BifrostResponseExtraFields{
				Latency: 42,
			},
		},
	}

	_, _, err := plugin.PostLLMHook(ctx, result, nil)
	if err != nil {
		t.Fatalf("PostLLMHook() error = %v", err)
	}

	logEntry := waitForLog(t, store, requestID)
	if logEntry.RouteLayerIndex == nil || *logEntry.RouteLayerIndex != 1 {
		t.Fatalf("expected fallback route_layer_index=1, got %+v", logEntry.RouteLayerIndex)
	}
}

func TestPostLLMHook_PreludeRetryPreservesPendingForFinalSuccess(t *testing.T) {
	store := newTestStore(t)
	plugin := newAsyncTestPlugin(t, store)

	requestID := "req-stream-retry"
	plugin.pendingLogs.Store(requestID, &PendingLogData{
		RequestID: requestID,
		Timestamp: time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC),
		InitialData: &InitialLogData{
			Object:   "responses",
			Provider: "openai",
			Model:    "gpt-4.1",
		},
		CreatedAt: time.Now(),
		Status:    "processing",
	})

	ctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(time.Minute))
	ctx.SetValue(schemas.BifrostContextKeyRequestID, requestID)
	ctx.SetValue(schemas.BifrostContextKeyNumberOfRetries, 0)
	ctx.SetValue(schemas.BifrostContextKeyProviderMaxRetries, 1)
	ctx.SetValue(schemas.BifrostContextKeyStreamPreludeError, true)
	ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)

	_, _, err := plugin.PostLLMHook(ctx, nil, &schemas.BifrostError{
		StatusCode: schemas.Ptr(429),
		Error: &schemas.ErrorField{
			Message: "rate limit exceeded",
		},
		ExtraFields: schemas.BifrostErrorExtraFields{
			RequestType:    schemas.ResponsesStreamRequest,
			Provider:       schemas.OpenAI,
			ModelRequested: "gpt-4.1",
		},
	})
	if err != nil {
		t.Fatalf("PostLLMHook() prelude error = %v", err)
	}

	if _, ok := plugin.pendingLogs.Load(requestID); !ok {
		t.Fatal("expected pending log to survive retryable prelude error")
	}

	ctx.ClearValue(schemas.BifrostContextKeyStreamPreludeError)
	ctx.SetValue(schemas.BifrostContextKeyNumberOfRetries, 1)
	ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)

	result := &schemas.BifrostResponse{
		ResponsesStreamResponse: &schemas.BifrostResponsesStreamResponse{
			Type: schemas.ResponsesStreamResponseTypeCompleted,
			ExtraFields: schemas.BifrostResponseExtraFields{
				RequestType:    schemas.ResponsesStreamRequest,
				Provider:       schemas.OpenAI,
				ModelRequested: "gpt-4.1",
				Latency:        42,
			},
		},
	}

	_, _, err = plugin.PostLLMHook(ctx, result, nil)
	if err != nil {
		t.Fatalf("PostLLMHook() final success = %v", err)
	}

	logEntry := waitForLog(t, store, requestID)
	if logEntry.Status != "success" {
		t.Fatalf("expected final log status=success, got %q", logEntry.Status)
	}
	if logEntry.NumberOfRetries != 1 {
		t.Fatalf("expected number_of_retries=1, got %d", logEntry.NumberOfRetries)
	}
}

func intPtr(value int) *int {
	return &value
}
