package logging

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/framework/logstore"
)

func TestPostWriteCallbackUpsertsProfitEvent(t *testing.T) {
	store := newTestStore(t)
	plugin := &LoggerPlugin{
		store:  store,
		ctx:    context.Background(),
		logger: testLogger{},
	}

	cost := 0.5
	entry := &logstore.Log{
		ID:               "profit-callback",
		Timestamp:        time.Now().UTC(),
		Object:           "chat_completion",
		Provider:         "yunwu",
		Model:            "gemini-3.1-pro-preview",
		SelectedKeyID:    "key-1",
		SelectedKeyName:  "Yunwu",
		Status:           "success",
		PromptTokens:     1_000_000,
		CompletionTokens: 1_000_000,
		TotalTokens:      2_000_000,
		Cost:             &cost,
	}

	if err := store.Create(context.Background(), entry); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	plugin.makePostWriteCallback(nil)(entry)

	event, err := store.FindProfitEvent(context.Background(), entry.ID)
	if err != nil {
		t.Fatalf("FindProfitEvent() error = %v", err)
	}
	if event.RevenueUSD != 14 || event.CostUSD != 0.5 || event.ProfitUSD != 13.5 {
		t.Fatalf("profit event mismatch: %#v", event)
	}
}

func TestSyncProfitEventAfterDeferredUsageUpdate(t *testing.T) {
	store := newTestStore(t)
	plugin := &LoggerPlugin{
		store:  store,
		ctx:    context.Background(),
		logger: testLogger{},
	}

	entry := &logstore.Log{
		ID:              "profit-deferred",
		Timestamp:       time.Now().UTC(),
		Object:          "chat_completion",
		Provider:        "yunwu",
		Model:           "gemini-3.1-pro-preview",
		SelectedKeyID:   "key-1",
		SelectedKeyName: "Yunwu",
		Status:          "success",
	}
	if err := store.Create(context.Background(), entry); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := store.UpsertProfitEventFromLog(context.Background(), entry); err != nil {
		t.Fatalf("UpsertProfitEventFromLog(initial) error = %v", err)
	}

	if err := store.Update(context.Background(), entry.ID, map[string]interface{}{
		"prompt_tokens":     1_000_000,
		"completion_tokens": 500_000,
		"total_tokens":      1_500_000,
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if err := plugin.syncProfitEventForLogID(context.Background(), entry.ID); err != nil {
		t.Fatalf("syncProfitEventForLogID() error = %v", err)
	}

	event, err := store.FindProfitEvent(context.Background(), entry.ID)
	if err != nil {
		t.Fatalf("FindProfitEvent() error = %v", err)
	}
	if event.MissingTokens {
		t.Fatalf("expected deferred usage sync to clear missing tokens")
	}
	if event.RevenueUSD != 8 {
		t.Fatalf("revenue mismatch after deferred usage sync: got %.6f want 8.000000", event.RevenueUSD)
	}
}
