package logstore

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestSearchLogs_AssignsRequestGroupingMetadataAndDeserializesFields(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()
	groupID := "req-group-1"

	userText := "hello grouped routing"
	requireCreateLog(t, store, &Log{
		ID:              groupID,
		Timestamp:       time.Date(2026, 4, 15, 8, 0, 0, 0, time.UTC),
		Object:          "chat_completion",
		Provider:        "openai",
		Model:           "gpt-4.1",
		FallbackIndex:   0,
		RouteLayerIndex: intPtr(0),
		Status:          "error",
		InputHistoryParsed: []schemas.ChatMessage{{
			Role: schemas.ChatMessageRoleUser,
			Content: &schemas.ChatMessageContent{
				ContentStr: &userText,
			},
		}},
	})
	requireCreateLog(t, store, &Log{
		ID:              "req-group-1-fb1",
		ParentRequestID: stringPtr(groupID),
		Timestamp:       time.Date(2026, 4, 15, 8, 0, 1, 0, time.UTC),
		Object:          "chat_completion",
		Provider:        "openai",
		Model:           "gpt-4.1-mini",
		FallbackIndex:   1,
		RouteLayerIndex: intPtr(0),
		Status:          "error",
	})
	requireCreateLog(t, store, &Log{
		ID:              "req-group-1-fb2",
		ParentRequestID: stringPtr(groupID),
		Timestamp:       time.Date(2026, 4, 15, 8, 0, 2, 0, time.UTC),
		Object:          "chat_completion",
		Provider:        "azure",
		Model:           "gpt-4.1",
		FallbackIndex:   2,
		RouteLayerIndex: intPtr(1),
		Status:          "success",
	})

	result, err := store.SearchLogs(ctx, SearchFilters{}, PaginationOptions{
		Limit:  20,
		SortBy: string(SortByTimestamp),
		Order:  string(SortDesc),
	})
	if err != nil {
		t.Fatalf("SearchLogs() error = %v", err)
	}

	if result.Stats.TotalRequests != 3 {
		t.Fatalf("expected total_requests=3, got %d", result.Stats.TotalRequests)
	}
	if result.Pagination.TotalCount != 3 {
		t.Fatalf("expected pagination.total_count=3, got %d", result.Pagination.TotalCount)
	}

	byID := make(map[string]Log, len(result.Logs))
	for _, log := range result.Logs {
		byID[log.ID] = log
	}

	first := byID[groupID]
	if first.GroupID != groupID || first.AttemptSequence != 1 || first.IsFinalAttempt {
		t.Fatalf("unexpected primary grouping metadata: %+v", first)
	}
	if len(first.InputHistoryParsed) != 1 {
		t.Fatalf("expected input history to be deserialized, got %+v", first.InputHistoryParsed)
	}
	if first.RouteLayerIndex == nil || *first.RouteLayerIndex != 0 {
		t.Fatalf("expected primary route layer 0, got %+v", first.RouteLayerIndex)
	}

	second := byID["req-group-1-fb1"]
	if second.GroupID != groupID || second.AttemptSequence != 2 || second.IsFinalAttempt {
		t.Fatalf("unexpected first fallback grouping metadata: %+v", second)
	}

	final := byID["req-group-1-fb2"]
	if final.GroupID != groupID || final.AttemptSequence != 3 || !final.IsFinalAttempt {
		t.Fatalf("unexpected final attempt grouping metadata: %+v", final)
	}
	if final.RouteLayerIndex == nil || *final.RouteLayerIndex != 1 {
		t.Fatalf("expected final route layer 1, got %+v", final.RouteLayerIndex)
	}
}

func TestFindByID_ReturnsRequestGroupingMetadata(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	requireCreateLog(t, store, &Log{
		ID:            "req-detail",
		Timestamp:     time.Date(2026, 4, 15, 8, 10, 0, 0, time.UTC),
		Object:        "chat_completion",
		Provider:      "openai",
		Model:         "gpt-4.1",
		FallbackIndex: 0,
		Status:        "error",
	})
	requireCreateLog(t, store, &Log{
		ID:              "req-detail-fb1",
		ParentRequestID: stringPtr("req-detail"),
		Timestamp:       time.Date(2026, 4, 15, 8, 10, 1, 0, time.UTC),
		Object:          "chat_completion",
		Provider:        "azure",
		Model:           "gpt-4.1",
		FallbackIndex:   1,
		Status:          "success",
	})

	logEntry, err := store.FindByID(ctx, "req-detail-fb1")
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}

	if logEntry.GroupID != "req-detail" || logEntry.AttemptSequence != 2 || !logEntry.IsFinalAttempt {
		t.Fatalf("unexpected detail grouping metadata: %+v", logEntry)
	}
}

func TestGetStats_SeparatesAttemptAndRequestSemantics(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()
	seedStatsFixture(t, store)

	stats, err := store.GetStats(ctx, SearchFilters{})
	if err != nil {
		t.Fatalf("GetStats() error = %v", err)
	}

	if stats.TotalRequests != 5 {
		t.Fatalf("expected total_requests=5, got %d", stats.TotalRequests)
	}
	if stats.CompletedAttempts != 4 || stats.SuccessfulAttempts != 2 {
		t.Fatalf("unexpected attempt counts: %+v", stats)
	}
	if !almostEqual(stats.SuccessRate, 50) {
		t.Fatalf("expected attempt success rate 50, got %.4f", stats.SuccessRate)
	}
	if stats.CompletedRequestGroups != 3 || stats.SuccessfulRequestGroups != 2 {
		t.Fatalf("unexpected request counts: %+v", stats)
	}
	if !almostEqual(stats.RequestSuccessRate, 66.6666667) {
		t.Fatalf("expected request success rate 66.67, got %.4f", stats.RequestSuccessRate)
	}
	if !almostEqual(stats.AverageLatency, 87.5) {
		t.Fatalf("expected average latency 87.5, got %.4f", stats.AverageLatency)
	}
	if !almostEqual(stats.AverageFinalLatency, 83.3333333) {
		t.Fatalf("expected average final latency 83.33, got %.4f", stats.AverageFinalLatency)
	}
	if stats.TotalTokens != 50 {
		t.Fatalf("expected total_tokens=50, got %d", stats.TotalTokens)
	}
	if !almostEqual(stats.TotalCost, 0.5) {
		t.Fatalf("expected total_cost=0.5, got %.4f", stats.TotalCost)
	}
}

func TestGetStats_AppliesFiltersToFinalAttemptsAfterSelectingThem(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()
	seedStatsFixture(t, store)

	stats, err := store.GetStats(ctx, SearchFilters{
		Status: []string{"error"},
	})
	if err != nil {
		t.Fatalf("GetStats() error = %v", err)
	}

	if stats.TotalRequests != 2 {
		t.Fatalf("expected filtered total_requests=2, got %d", stats.TotalRequests)
	}
	if stats.CompletedAttempts != 2 || stats.SuccessfulAttempts != 0 {
		t.Fatalf("unexpected filtered attempt counts: %+v", stats)
	}
	if stats.CompletedRequestGroups != 1 || stats.SuccessfulRequestGroups != 0 {
		t.Fatalf("unexpected filtered request counts: %+v", stats)
	}
	if !almostEqual(stats.RequestSuccessRate, 0) {
		t.Fatalf("expected filtered request success rate 0, got %.4f", stats.RequestSuccessRate)
	}
	if !almostEqual(stats.AverageFinalLatency, 120) {
		t.Fatalf("expected filtered average final latency 120, got %.4f", stats.AverageFinalLatency)
	}
}

func TestGetFinalSuccessDistribution_UsesSuccessfulFinalAttemptsOnly(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()
	seedStatsFixture(t, store)

	distribution, err := store.GetFinalSuccessDistribution(ctx, SearchFilters{}, FinalSuccessDistributionByLayer)
	if err != nil {
		t.Fatalf("GetFinalSuccessDistribution() error = %v", err)
	}

	if distribution.Dimension != FinalSuccessDistributionByLayer {
		t.Fatalf("expected layer dimension, got %s", distribution.Dimension)
	}
	if distribution.TotalSuccessCount != 2 {
		t.Fatalf("expected total_success_count=2, got %d", distribution.TotalSuccessCount)
	}

	itemsByValue := make(map[string]FinalSuccessDistributionItem, len(distribution.Items))
	for _, item := range distribution.Items {
		itemsByValue[item.Value] = item
	}

	layerTwo := itemsByValue["layer:1"]
	if layerTwo.Label != "Layer 2" || layerTwo.SuccessCount != 1 || !almostEqual(layerTwo.SuccessRatio, 50) {
		t.Fatalf("unexpected layer 2 distribution item: %+v", layerTwo)
	}

	unlayered := itemsByValue["unlayered"]
	if unlayered.Label != "Unlayered" || unlayered.SuccessCount != 1 || !almostEqual(unlayered.SuccessRatio, 50) {
		t.Fatalf("unexpected unlayered distribution item: %+v", unlayered)
	}
}

func seedStatsFixture(t *testing.T, store *RDBLogStore) {
	t.Helper()

	entries := []*Log{
		{
			ID:            "req-1",
			Timestamp:     time.Date(2026, 4, 15, 9, 0, 0, 0, time.UTC),
			Object:        "chat_completion",
			Provider:      "openai",
			Model:         "gpt-4.1",
			FallbackIndex: 0,
			Latency:       floatPtr(100),
			TotalTokens:   10,
			Cost:          floatPtr(0.1),
			Status:        "error",
		},
		{
			ID:              "req-1-fb1",
			ParentRequestID: stringPtr("req-1"),
			Timestamp:       time.Date(2026, 4, 15, 9, 0, 1, 0, time.UTC),
			Object:          "chat_completion",
			Provider:        "azure",
			Model:           "gpt-4.1",
			FallbackIndex:   1,
			RouteLayerIndex: intPtr(1),
			Latency:         floatPtr(50),
			TotalTokens:     15,
			Cost:            floatPtr(0.15),
			Status:          "success",
		},
		{
			ID:            "req-2",
			Timestamp:     time.Date(2026, 4, 15, 9, 5, 0, 0, time.UTC),
			Object:        "chat_completion",
			Provider:      "anthropic",
			Model:         "claude-sonnet",
			FallbackIndex: 0,
			Latency:       floatPtr(80),
			TotalTokens:   20,
			Cost:          floatPtr(0.2),
			Status:        "success",
		},
		{
			ID:            "req-3",
			Timestamp:     time.Date(2026, 4, 15, 9, 10, 0, 0, time.UTC),
			Object:        "chat_completion",
			Provider:      "openrouter",
			Model:         "openai/gpt-4.1",
			FallbackIndex: 0,
			Latency:       floatPtr(120),
			TotalTokens:   5,
			Cost:          floatPtr(0.05),
			Status:        "error",
		},
		{
			ID:            "req-4",
			Timestamp:     time.Date(2026, 4, 15, 9, 15, 0, 0, time.UTC),
			Object:        "chat_completion",
			Provider:      "openai",
			Model:         "gpt-4.1-mini",
			FallbackIndex: 0,
			Latency:       floatPtr(40),
			Status:        "processing",
		},
	}

	for _, entry := range entries {
		requireCreateLog(t, store, entry)
	}
}

func requireCreateLog(t *testing.T, store *RDBLogStore, entry *Log) {
	t.Helper()
	if err := store.Create(context.Background(), entry); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
}

func stringPtr(value string) *string {
	return &value
}

func intPtr(value int) *int {
	return &value
}

func floatPtr(value float64) *float64 {
	return &value
}

func almostEqual(left, right float64) bool {
	return math.Abs(left-right) < 0.0001
}
