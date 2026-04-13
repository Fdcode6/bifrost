package logstore

import (
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestListSelectColumns_IncludesRoutingEngineLogs(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	store := &RDBLogStore{db: db}
	columns := store.listSelectColumns()
	if !strings.Contains(columns, "routing_engine_logs") {
		t.Fatalf("expected list select columns to include routing_engine_logs, got %q", columns)
	}
}
