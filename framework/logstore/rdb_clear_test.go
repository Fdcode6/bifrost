package logstore

import (
	"context"
	"testing"
	"time"
)

func TestClearAllLogsRemovesRequestAndMCPToolLogs(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	if err := store.Create(ctx, &Log{
		ID:        "log-clear-1",
		Timestamp: time.Now().UTC(),
		Object:    "chat_completion",
		Provider:  "openai",
		Model:     "gpt-4.1-mini",
		Status:    "success",
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if err := store.CreateMCPToolLog(ctx, &MCPToolLog{
		ID:        "mcp-clear-1",
		Timestamp: time.Now().UTC(),
		ToolName:  "echo",
		Status:    "success",
	}); err != nil {
		t.Fatalf("CreateMCPToolLog() error = %v", err)
	}

	hasLogs, err := store.HasLogs(ctx)
	if err != nil {
		t.Fatalf("HasLogs() error = %v", err)
	}
	if !hasLogs {
		t.Fatalf("expected request logs to exist before clearing")
	}

	hasMCPLogs, err := store.HasMCPToolLogs(ctx)
	if err != nil {
		t.Fatalf("HasMCPToolLogs() error = %v", err)
	}
	if !hasMCPLogs {
		t.Fatalf("expected MCP tool logs to exist before clearing")
	}

	if err := store.ClearAllLogs(ctx); err != nil {
		t.Fatalf("ClearAllLogs() error = %v", err)
	}

	hasLogs, err = store.HasLogs(ctx)
	if err != nil {
		t.Fatalf("HasLogs() after clear error = %v", err)
	}
	if hasLogs {
		t.Fatalf("expected request logs to be cleared")
	}

	hasMCPLogs, err = store.HasMCPToolLogs(ctx)
	if err != nil {
		t.Fatalf("HasMCPToolLogs() after clear error = %v", err)
	}
	if hasMCPLogs {
		t.Fatalf("expected MCP tool logs to be cleared")
	}
}
