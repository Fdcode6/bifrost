package logstore

import (
	"encoding/json"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestListSelectColumns_UsesLightweightListPayload(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	store := &RDBLogStore{db: db}
	columns := store.listSelectColumns()
	if !strings.Contains(columns, "routing_engine_logs") {
		t.Fatalf("expected list select columns to include routing_engine_logs, got %q", columns)
	}
	if !strings.Contains(columns, "content_summary") {
		t.Fatalf("expected list select columns to include content_summary, got %q", columns)
	}
	for _, omitted := range []string{"input_history", "responses_input_history"} {
		if strings.Contains(columns, omitted) {
			t.Fatalf("expected list select columns to omit %s, got %q", omitted, columns)
		}
	}
}

func TestLogJSONIncludesContentSummaryForListPreview(t *testing.T) {
	logEntry := Log{
		ID:             "log-preview",
		ContentSummary: "preview text",
	}

	data, err := json.Marshal(logEntry)
	if err != nil {
		t.Fatalf("marshal log: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["content_summary"] != "preview text" {
		t.Fatalf("expected content_summary in JSON payload, got %v", payload["content_summary"])
	}
}
