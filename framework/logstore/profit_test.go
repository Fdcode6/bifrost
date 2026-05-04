package logstore

import (
	"context"
	"math"
	"testing"
	"time"
)

func almostEqualFloat(got, want float64) bool {
	return math.Abs(got-want) < 0.000001
}

func TestProfitEventFromLog_SuccessUsesCurrentSellPrices(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	if _, err := store.SaveProfitSettings(ctx, &ProfitSettings{
		SellInputPer1MUSD:  2,
		SellOutputPer1MUSD: 12,
		Timezone:           "Asia/Shanghai",
	}); err != nil {
		t.Fatalf("SaveProfitSettings() error = %v", err)
	}

	cost := 3.25
	event, err := store.UpsertProfitEventFromLog(ctx, &Log{
		ID:               "profit-success",
		Timestamp:        time.Date(2026, 5, 4, 3, 30, 0, 0, time.UTC),
		Object:           "chat_completion",
		Provider:         "yunwu",
		Model:            "gemini-3.1-pro-preview",
		SelectedKeyID:    "key-1",
		SelectedKeyName:  "Yunwu",
		Status:           "success",
		PromptTokens:     1_500_000,
		CompletionTokens: 250_000,
		TotalTokens:      1_750_000,
		Cost:             &cost,
	})
	if err != nil {
		t.Fatalf("UpsertProfitEventFromLog() error = %v", err)
	}
	if event == nil {
		t.Fatalf("expected profit event")
	}
	if event.BusinessDay != "2026-05-04" {
		t.Fatalf("business day mismatch: got %q", event.BusinessDay)
	}
	if !almostEqualFloat(event.RevenueUSD, 6.0) {
		t.Fatalf("revenue mismatch: got %.6f want 6.000000", event.RevenueUSD)
	}
	if !almostEqualFloat(event.CostUSD, 3.25) {
		t.Fatalf("cost mismatch: got %.6f want 3.250000", event.CostUSD)
	}
	if !almostEqualFloat(event.ProfitUSD, 2.75) {
		t.Fatalf("profit mismatch: got %.6f want 2.750000", event.ProfitUSD)
	}
	if event.MissingCost {
		t.Fatalf("did not expect missing cost")
	}
	if event.MissingTokens {
		t.Fatalf("did not expect missing tokens")
	}
}

func TestProfitEventFromLog_ErrorCountsCostOnly(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	cost := 0.42
	event, err := store.UpsertProfitEventFromLog(ctx, &Log{
		ID:               "profit-error",
		Timestamp:        time.Date(2026, 5, 4, 4, 0, 0, 0, time.UTC),
		Object:           "chat_completion",
		Provider:         "poloapi",
		Model:            "gemini-3.1-pro-preview",
		SelectedKeyID:    "key-2",
		SelectedKeyName:  "Polo",
		Status:           "error",
		PromptTokens:     1_000_000,
		CompletionTokens: 1_000_000,
		TotalTokens:      2_000_000,
		Cost:             &cost,
	})
	if err != nil {
		t.Fatalf("UpsertProfitEventFromLog() error = %v", err)
	}
	if event == nil {
		t.Fatalf("expected profit event")
	}
	if event.RevenueUSD != 0 {
		t.Fatalf("error revenue mismatch: got %.6f want 0", event.RevenueUSD)
	}
	if !almostEqualFloat(event.ProfitUSD, -0.42) {
		t.Fatalf("error profit mismatch: got %.6f want -0.420000", event.ProfitUSD)
	}
}

func TestProfitSettingsChangeDoesNotRewriteExistingEventPrice(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	if _, err := store.SaveProfitSettings(ctx, &ProfitSettings{
		SellInputPer1MUSD:  2,
		SellOutputPer1MUSD: 12,
		Timezone:           "Asia/Shanghai",
	}); err != nil {
		t.Fatalf("SaveProfitSettings(initial) error = %v", err)
	}

	entry := &Log{
		ID:               "profit-frozen-price",
		Timestamp:        time.Date(2026, 5, 4, 5, 0, 0, 0, time.UTC),
		Object:           "chat_completion",
		Provider:         "yunwu",
		Model:            "gemini-3.1-pro-preview",
		SelectedKeyID:    "key-1",
		SelectedKeyName:  "Yunwu",
		Status:           "success",
		PromptTokens:     1_000_000,
		CompletionTokens: 1_000_000,
		TotalTokens:      2_000_000,
	}
	if _, err := store.UpsertProfitEventFromLog(ctx, entry); err != nil {
		t.Fatalf("UpsertProfitEventFromLog(initial) error = %v", err)
	}

	if _, err := store.SaveProfitSettings(ctx, &ProfitSettings{
		SellInputPer1MUSD:  20,
		SellOutputPer1MUSD: 120,
		Timezone:           "Asia/Shanghai",
	}); err != nil {
		t.Fatalf("SaveProfitSettings(updated) error = %v", err)
	}
	entry.CompletionTokens = 2_000_000
	entry.TotalTokens = 3_000_000
	event, err := store.UpsertProfitEventFromLog(ctx, entry)
	if err != nil {
		t.Fatalf("UpsertProfitEventFromLog(updated) error = %v", err)
	}

	if !almostEqualFloat(event.SellInputPer1MUSD, 2) || !almostEqualFloat(event.SellOutputPer1MUSD, 12) {
		t.Fatalf("expected existing event prices to stay frozen, got %.2f / %.2f", event.SellInputPer1MUSD, event.SellOutputPer1MUSD)
	}
	if !almostEqualFloat(event.RevenueUSD, 26) {
		t.Fatalf("expected revenue to use frozen prices after token update, got %.6f want 26.000000", event.RevenueUSD)
	}
}

func TestClearAllLogsDoesNotClearProfitData(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	if _, err := store.SaveProfitSettings(ctx, &ProfitSettings{
		SellInputPer1MUSD:  2,
		SellOutputPer1MUSD: 12,
		Timezone:           "Asia/Shanghai",
	}); err != nil {
		t.Fatalf("SaveProfitSettings() error = %v", err)
	}

	entry := &Log{
		ID:               "profit-survives-clear",
		Timestamp:        time.Now().UTC(),
		Object:           "chat_completion",
		Provider:         "openrouter",
		Model:            "gemma-4-31b-it",
		SelectedKeyID:    "key-3",
		SelectedKeyName:  "OpenRouter",
		Status:           "success",
		PromptTokens:     1000,
		CompletionTokens: 2000,
		TotalTokens:      3000,
	}
	if err := store.Create(ctx, entry); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := store.UpsertProfitEventFromLog(ctx, entry); err != nil {
		t.Fatalf("UpsertProfitEventFromLog() error = %v", err)
	}

	if err := store.ClearAllLogs(ctx); err != nil {
		t.Fatalf("ClearAllLogs() error = %v", err)
	}

	event, err := store.FindProfitEvent(ctx, entry.ID)
	if err != nil {
		t.Fatalf("FindProfitEvent() error = %v", err)
	}
	if event.LogID != entry.ID {
		t.Fatalf("event log id mismatch: got %q want %q", event.LogID, entry.ID)
	}
	settings, err := store.GetProfitSettings(ctx)
	if err != nil {
		t.Fatalf("GetProfitSettings() error = %v", err)
	}
	if settings.SellInputPer1MUSD != 2 || settings.SellOutputPer1MUSD != 12 {
		t.Fatalf("profit settings were unexpectedly changed: %#v", settings)
	}
}

func TestGetProfitSummaryAndDaily(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()
	if _, err := store.SaveProfitSettings(ctx, &ProfitSettings{
		SellInputPer1MUSD:  2,
		SellOutputPer1MUSD: 12,
		Timezone:           "Asia/Shanghai",
	}); err != nil {
		t.Fatalf("SaveProfitSettings() error = %v", err)
	}

	costA := 1.0
	costB := 0.5
	entries := []*Log{
		{
			ID:               "profit-day-a",
			Timestamp:        time.Date(2026, 5, 3, 16, 10, 0, 0, time.UTC), // 2026-05-04 in Asia/Shanghai
			Object:           "chat_completion",
			Provider:         "yunwu",
			Model:            "gemini-3.1-pro-preview",
			SelectedKeyID:    "key-1",
			SelectedKeyName:  "Yunwu",
			Status:           "success",
			PromptTokens:     1_000_000,
			CompletionTokens: 1_000_000,
			TotalTokens:      2_000_000,
			Cost:             &costA,
		},
		{
			ID:               "profit-day-b",
			Timestamp:        time.Date(2026, 5, 4, 2, 0, 0, 0, time.UTC),
			Object:           "chat_completion",
			Provider:         "poloapi",
			Model:            "gemini-3.1-pro-preview",
			SelectedKeyID:    "key-2",
			SelectedKeyName:  "Polo",
			Status:           "error",
			PromptTokens:     1_000_000,
			CompletionTokens: 1_000_000,
			TotalTokens:      2_000_000,
			Cost:             &costB,
		},
	}
	for _, entry := range entries {
		if _, err := store.UpsertProfitEventFromLog(ctx, entry); err != nil {
			t.Fatalf("UpsertProfitEventFromLog(%s) error = %v", entry.ID, err)
		}
	}

	summary, err := store.GetProfitSummary(ctx, ProfitQuery{
		StartDay: "2026-05-04",
		EndDay:   "2026-05-04",
	})
	if err != nil {
		t.Fatalf("GetProfitSummary() error = %v", err)
	}
	if summary.RequestCount != 2 || summary.SuccessCount != 1 || summary.ErrorCount != 1 {
		t.Fatalf("summary counts mismatch: %#v", summary)
	}
	if !almostEqualFloat(summary.RevenueUSD, 14) || !almostEqualFloat(summary.CostUSD, 1.5) || !almostEqualFloat(summary.ProfitUSD, 12.5) {
		t.Fatalf("summary money mismatch: %#v", summary)
	}

	daily, err := store.GetProfitDaily(ctx, ProfitQuery{
		StartDay: "2026-05-04",
		EndDay:   "2026-05-04",
	})
	if err != nil {
		t.Fatalf("GetProfitDaily() error = %v", err)
	}
	if len(daily) != 1 {
		t.Fatalf("expected one daily bucket, got %d", len(daily))
	}
	if daily[0].BusinessDay != "2026-05-04" || !almostEqualFloat(daily[0].ProfitUSD, 12.5) {
		t.Fatalf("daily bucket mismatch: %#v", daily[0])
	}
}

func TestGetProfitBreakdownByProviderModel(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()
	if _, err := store.SaveProfitSettings(ctx, &ProfitSettings{
		SellInputPer1MUSD:  2,
		SellOutputPer1MUSD: 12,
		Timezone:           "Asia/Shanghai",
	}); err != nil {
		t.Fatalf("SaveProfitSettings() error = %v", err)
	}

	yunwuCostA := 1.0
	yunwuCostB := 2.0
	vapiCost := 0.5
	entries := []*Log{
		{
			ID:               "breakdown-yunwu-a",
			Timestamp:        time.Date(2026, 5, 4, 1, 0, 0, 0, time.UTC),
			Object:           "chat_completion",
			Provider:         "yunwu",
			Model:            "gemini-pro",
			Status:           "success",
			PromptTokens:     1_000_000,
			CompletionTokens: 1_000_000,
			TotalTokens:      2_000_000,
			Cost:             &yunwuCostA,
		},
		{
			ID:               "breakdown-yunwu-b",
			Timestamp:        time.Date(2026, 5, 4, 2, 0, 0, 0, time.UTC),
			Object:           "chat_completion",
			Provider:         "yunwu",
			Model:            "gemini-pro",
			Status:           "error",
			PromptTokens:     200_000,
			CompletionTokens: 100_000,
			TotalTokens:      300_000,
			Cost:             &yunwuCostB,
		},
		{
			ID:               "breakdown-vapi",
			Timestamp:        time.Date(2026, 5, 4, 3, 0, 0, 0, time.UTC),
			Object:           "chat_completion",
			Provider:         "v-api",
			Model:            "gemini-low",
			Status:           "success",
			PromptTokens:     500_000,
			CompletionTokens: 500_000,
			TotalTokens:      1_000_000,
			Cost:             &vapiCost,
		},
	}
	for _, entry := range entries {
		if _, err := store.UpsertProfitEventFromLog(ctx, entry); err != nil {
			t.Fatalf("UpsertProfitEventFromLog(%s) error = %v", entry.ID, err)
		}
	}

	rows, err := store.GetProfitBreakdown(ctx, ProfitQuery{
		StartDay: "2026-05-04",
		EndDay:   "2026-05-04",
	})
	if err != nil {
		t.Fatalf("GetProfitBreakdown() error = %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected two breakdown rows, got %d: %#v", len(rows), rows)
	}
	if rows[0].Provider != "yunwu" || rows[0].Model != "gemini-pro" {
		t.Fatalf("expected highest revenue row first, got %#v", rows[0])
	}
	if rows[0].RequestCount != 2 || rows[0].SuccessCount != 1 || rows[0].ErrorCount != 1 {
		t.Fatalf("yunwu counts mismatch: %#v", rows[0])
	}
	if !almostEqualFloat(rows[0].RevenueUSD, 14) || !almostEqualFloat(rows[0].CostUSD, 3) || !almostEqualFloat(rows[0].ProfitUSD, 11) {
		t.Fatalf("yunwu money mismatch: %#v", rows[0])
	}
	if rows[1].Provider != "v-api" || rows[1].Model != "gemini-low" {
		t.Fatalf("v-api row mismatch: %#v", rows[1])
	}
	if !almostEqualFloat(rows[1].RevenueUSD, 7) || !almostEqualFloat(rows[1].CostUSD, 0.5) || !almostEqualFloat(rows[1].ProfitUSD, 6.5) {
		t.Fatalf("v-api money mismatch: %#v", rows[1])
	}
}
