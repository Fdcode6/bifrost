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

func TestProfitEventFromLog_UsesRoutingRuleSellPrices(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	if _, err := store.SaveProfitSettings(ctx, &ProfitSettings{
		SellInputPer1MUSD:  2,
		SellOutputPer1MUSD: 12,
		Timezone:           "Asia/Shanghai",
	}); err != nil {
		t.Fatalf("SaveProfitSettings(default) error = %v", err)
	}
	if _, err := store.SaveRoutingRuleProfitSettings(ctx, "rule-review", &ProfitSettings{
		SellInputPer1MUSD:  8,
		SellOutputPer1MUSD: 40,
	}); err != nil {
		t.Fatalf("SaveRoutingRuleProfitSettings() error = %v", err)
	}

	ruleID := "rule-review"
	ruleName := "会话审查"
	cost := 4.5
	event, err := store.UpsertProfitEventFromLog(ctx, &Log{
		ID:               "profit-rule-price",
		Timestamp:        time.Date(2026, 5, 4, 8, 0, 0, 0, time.UTC),
		Object:           "chat_completion",
		Provider:         "deepinfra",
		Model:            "google/gemma-4-26B-A4B-it",
		RoutingRuleID:    &ruleID,
		RoutingRuleName:  &ruleName,
		Status:           "success",
		PromptTokens:     1_000_000,
		CompletionTokens: 500_000,
		TotalTokens:      1_500_000,
		Cost:             &cost,
	})
	if err != nil {
		t.Fatalf("UpsertProfitEventFromLog() error = %v", err)
	}
	if !almostEqualFloat(event.SellInputPer1MUSD, 8) || !almostEqualFloat(event.SellOutputPer1MUSD, 40) {
		t.Fatalf("expected routing rule prices, got %.2f / %.2f", event.SellInputPer1MUSD, event.SellOutputPer1MUSD)
	}
	if !almostEqualFloat(event.RevenueUSD, 28) || !almostEqualFloat(event.ProfitUSD, 23.5) {
		t.Fatalf("routing rule money mismatch: %#v", event)
	}
}

func TestProfitEventFromLog_FallsBackToDefaultSellPricesWhenRoutingRuleHasNoPrice(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	if _, err := store.SaveProfitSettings(ctx, &ProfitSettings{
		SellInputPer1MUSD:  3,
		SellOutputPer1MUSD: 9,
		Timezone:           "Asia/Shanghai",
	}); err != nil {
		t.Fatalf("SaveProfitSettings(default) error = %v", err)
	}

	ruleID := "rule-without-price"
	event, err := store.UpsertProfitEventFromLog(ctx, &Log{
		ID:               "profit-rule-price-fallback",
		Timestamp:        time.Date(2026, 5, 4, 8, 30, 0, 0, time.UTC),
		Object:           "chat_completion",
		Provider:         "openrouter",
		Model:            "gemma-4-31b-it",
		RoutingRuleID:    &ruleID,
		Status:           "success",
		PromptTokens:     1_000_000,
		CompletionTokens: 1_000_000,
		TotalTokens:      2_000_000,
	})
	if err != nil {
		t.Fatalf("UpsertProfitEventFromLog() error = %v", err)
	}
	if !almostEqualFloat(event.SellInputPer1MUSD, 3) || !almostEqualFloat(event.SellOutputPer1MUSD, 9) {
		t.Fatalf("expected default prices, got %.2f / %.2f", event.SellInputPer1MUSD, event.SellOutputPer1MUSD)
	}
	if !almostEqualFloat(event.RevenueUSD, 12) {
		t.Fatalf("expected default-price revenue 12, got %.6f", event.RevenueUSD)
	}
}

func TestRoutingRuleProfitSettingsListAndDelete(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	if _, err := store.SaveProfitSettings(ctx, &ProfitSettings{
		SellInputPer1MUSD:  2,
		SellOutputPer1MUSD: 12,
		Timezone:           "Asia/Shanghai",
	}); err != nil {
		t.Fatalf("SaveProfitSettings(default) error = %v", err)
	}
	if _, err := store.SaveRoutingRuleProfitSettings(ctx, "rule-auto", &ProfitSettings{
		SellInputPer1MUSD:  4,
		SellOutputPer1MUSD: 20,
	}); err != nil {
		t.Fatalf("SaveRoutingRuleProfitSettings(rule-auto) error = %v", err)
	}
	if _, err := store.SaveRoutingRuleProfitSettings(ctx, "rule-review", &ProfitSettings{
		SellInputPer1MUSD:  6,
		SellOutputPer1MUSD: 30,
	}); err != nil {
		t.Fatalf("SaveRoutingRuleProfitSettings(rule-review) error = %v", err)
	}

	settings, err := store.ListRoutingRuleProfitSettings(ctx)
	if err != nil {
		t.Fatalf("ListRoutingRuleProfitSettings() error = %v", err)
	}
	if len(settings) != 2 {
		t.Fatalf("expected two routing rule settings, got %d: %#v", len(settings), settings)
	}
	for _, item := range settings {
		if item.RoutingRuleID == "" || item.ID == defaultProfitSettingsID {
			t.Fatalf("unexpected routing rule settings item: %#v", item)
		}
	}

	if err := store.DeleteRoutingRuleProfitSettings(ctx, "rule-auto"); err != nil {
		t.Fatalf("DeleteRoutingRuleProfitSettings() error = %v", err)
	}
	if _, found, err := store.GetRoutingRuleProfitSettings(ctx, "rule-auto"); err != nil || found {
		t.Fatalf("expected deleted routing rule settings to be absent, found=%v err=%v", found, err)
	}
}

func TestRoutingRuleProfitSettingsChangeDoesNotRewriteExistingEventPrice(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	if _, err := store.SaveProfitSettings(ctx, &ProfitSettings{
		SellInputPer1MUSD:  2,
		SellOutputPer1MUSD: 12,
		Timezone:           "Asia/Shanghai",
	}); err != nil {
		t.Fatalf("SaveProfitSettings(default) error = %v", err)
	}
	if _, err := store.SaveRoutingRuleProfitSettings(ctx, "rule-review", &ProfitSettings{
		SellInputPer1MUSD:  1,
		SellOutputPer1MUSD: 5,
	}); err != nil {
		t.Fatalf("SaveRoutingRuleProfitSettings(initial) error = %v", err)
	}

	ruleID := "rule-review"
	entry := &Log{
		ID:               "profit-rule-frozen-price",
		Timestamp:        time.Date(2026, 5, 4, 9, 0, 0, 0, time.UTC),
		Object:           "chat_completion",
		Provider:         "deepinfra",
		Model:            "google/gemma-4-26B-A4B-it",
		RoutingRuleID:    &ruleID,
		Status:           "success",
		PromptTokens:     1_000_000,
		CompletionTokens: 1_000_000,
		TotalTokens:      2_000_000,
	}
	if _, err := store.UpsertProfitEventFromLog(ctx, entry); err != nil {
		t.Fatalf("UpsertProfitEventFromLog(initial) error = %v", err)
	}

	if _, err := store.SaveRoutingRuleProfitSettings(ctx, "rule-review", &ProfitSettings{
		SellInputPer1MUSD:  10,
		SellOutputPer1MUSD: 50,
	}); err != nil {
		t.Fatalf("SaveRoutingRuleProfitSettings(updated) error = %v", err)
	}
	entry.CompletionTokens = 2_000_000
	entry.TotalTokens = 3_000_000
	event, err := store.UpsertProfitEventFromLog(ctx, entry)
	if err != nil {
		t.Fatalf("UpsertProfitEventFromLog(updated) error = %v", err)
	}
	if !almostEqualFloat(event.SellInputPer1MUSD, 1) || !almostEqualFloat(event.SellOutputPer1MUSD, 5) {
		t.Fatalf("expected existing rule event prices to stay frozen, got %.2f / %.2f", event.SellInputPer1MUSD, event.SellOutputPer1MUSD)
	}
	if !almostEqualFloat(event.RevenueUSD, 11) {
		t.Fatalf("expected revenue to use frozen rule prices after token update, got %.6f want 11.000000", event.RevenueUSD)
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

func TestBackfillProfitEventsPrioritizesMissingProfitEvents(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	cost := 0.1
	entries := []*Log{
		{
			ID:               "profit-existing",
			Timestamp:        time.Date(2026, 5, 4, 1, 0, 0, 0, time.UTC),
			Object:           "chat_completion",
			Provider:         "yunwu",
			Model:            "gemini-pro",
			Status:           "success",
			PromptTokens:     1000,
			CompletionTokens: 1000,
			TotalTokens:      2000,
			Cost:             &cost,
		},
		{
			ID:               "profit-missing-a",
			Timestamp:        time.Date(2026, 5, 4, 2, 0, 0, 0, time.UTC),
			Object:           "chat_completion",
			Provider:         "yunwu",
			Model:            "gemini-pro",
			Status:           "success",
			PromptTokens:     2000,
			CompletionTokens: 2000,
			TotalTokens:      4000,
			Cost:             &cost,
		},
		{
			ID:               "profit-missing-b",
			Timestamp:        time.Date(2026, 5, 4, 3, 0, 0, 0, time.UTC),
			Object:           "chat_completion",
			Provider:         "yunwu",
			Model:            "gemini-pro",
			Status:           "success",
			PromptTokens:     3000,
			CompletionTokens: 3000,
			TotalTokens:      6000,
			Cost:             &cost,
		},
	}
	for _, entry := range entries {
		if err := store.Create(ctx, entry); err != nil {
			t.Fatalf("Create(%s) error = %v", entry.ID, err)
		}
	}
	if _, err := store.UpsertProfitEventFromLog(ctx, entries[0]); err != nil {
		t.Fatalf("UpsertProfitEventFromLog(existing) error = %v", err)
	}

	first, err := store.BackfillProfitEvents(ctx, 1)
	if err != nil {
		t.Fatalf("BackfillProfitEvents(first) error = %v", err)
	}
	if first.Processed != 1 || first.Created != 1 || first.Updated != 0 {
		t.Fatalf("first backfill should create the first missing event, got %#v", first)
	}
	if _, err := store.FindProfitEvent(ctx, "profit-missing-a"); err != nil {
		t.Fatalf("expected first missing profit event to be created: %v", err)
	}

	second, err := store.BackfillProfitEvents(ctx, 1)
	if err != nil {
		t.Fatalf("BackfillProfitEvents(second) error = %v", err)
	}
	if second.Processed != 1 || second.Created != 1 || second.Updated != 0 {
		t.Fatalf("second backfill should advance to the next missing event, got %#v", second)
	}
	if _, err := store.FindProfitEvent(ctx, "profit-missing-b"); err != nil {
		t.Fatalf("expected second missing profit event to be created: %v", err)
	}

	third, err := store.BackfillProfitEvents(ctx, 1)
	if err != nil {
		t.Fatalf("BackfillProfitEvents(third) error = %v", err)
	}
	if third.Processed != 0 || third.Created != 0 || third.Updated != 0 {
		t.Fatalf("third backfill should be a no-op when no profit events are missing, got %#v", third)
	}
}

func TestCountMissingProfitEvents(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	cost := 0.1
	entries := []*Log{
		{
			ID:               "profit-count-existing",
			Timestamp:        time.Date(2026, 5, 4, 1, 0, 0, 0, time.UTC),
			Object:           "chat_completion",
			Provider:         "yunwu",
			Model:            "gemini-pro",
			Status:           "success",
			PromptTokens:     1000,
			CompletionTokens: 1000,
			TotalTokens:      2000,
			Cost:             &cost,
		},
		{
			ID:               "profit-count-missing",
			Timestamp:        time.Date(2026, 5, 4, 2, 0, 0, 0, time.UTC),
			Object:           "chat_completion",
			Provider:         "yunwu",
			Model:            "gemini-pro",
			Status:           "success",
			PromptTokens:     2000,
			CompletionTokens: 2000,
			TotalTokens:      4000,
			Cost:             &cost,
		},
		{
			ID:        "profit-count-processing",
			Timestamp: time.Date(2026, 5, 4, 3, 0, 0, 0, time.UTC),
			Object:    "chat_completion",
			Provider:  "yunwu",
			Model:     "gemini-pro",
			Status:    "processing",
		},
	}
	for _, entry := range entries {
		if err := store.Create(ctx, entry); err != nil {
			t.Fatalf("Create(%s) error = %v", entry.ID, err)
		}
	}
	if _, err := store.UpsertProfitEventFromLog(ctx, entries[0]); err != nil {
		t.Fatalf("UpsertProfitEventFromLog(existing) error = %v", err)
	}

	missing, err := store.CountMissingProfitEvents(ctx)
	if err != nil {
		t.Fatalf("CountMissingProfitEvents() error = %v", err)
	}
	if missing != 1 {
		t.Fatalf("missing count mismatch: got %d want 1", missing)
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

func TestGetProfitBreakdownSeparatesRoutingRulesForSameProviderModel(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()
	if _, err := store.SaveProfitSettings(ctx, &ProfitSettings{
		SellInputPer1MUSD:  2,
		SellOutputPer1MUSD: 12,
		Timezone:           "Asia/Shanghai",
	}); err != nil {
		t.Fatalf("SaveProfitSettings() error = %v", err)
	}
	if _, err := store.SaveRoutingRuleProfitSettings(ctx, "rule-auto", &ProfitSettings{
		SellInputPer1MUSD:  2,
		SellOutputPer1MUSD: 12,
	}); err != nil {
		t.Fatalf("SaveRoutingRuleProfitSettings(rule-auto) error = %v", err)
	}
	if _, err := store.SaveRoutingRuleProfitSettings(ctx, "rule-review", &ProfitSettings{
		SellInputPer1MUSD:  5,
		SellOutputPer1MUSD: 30,
	}); err != nil {
		t.Fatalf("SaveRoutingRuleProfitSettings(rule-review) error = %v", err)
	}

	autoRuleID := "rule-auto"
	autoRuleName := "gemini-auto"
	reviewRuleID := "rule-review"
	reviewRuleName := "会话审查"
	entries := []*Log{
		{
			ID:               "breakdown-openrouter-auto",
			Timestamp:        time.Date(2026, 5, 4, 1, 0, 0, 0, time.UTC),
			Object:           "chat_completion",
			Provider:         "openrouter",
			Model:            "gemma-4-31b-it",
			RoutingRuleID:    &autoRuleID,
			RoutingRuleName:  &autoRuleName,
			Status:           "success",
			PromptTokens:     1_000_000,
			CompletionTokens: 1_000_000,
			TotalTokens:      2_000_000,
		},
		{
			ID:               "breakdown-openrouter-review",
			Timestamp:        time.Date(2026, 5, 4, 2, 0, 0, 0, time.UTC),
			Object:           "chat_completion",
			Provider:         "openrouter",
			Model:            "gemma-4-31b-it",
			RoutingRuleID:    &reviewRuleID,
			RoutingRuleName:  &reviewRuleName,
			Status:           "success",
			PromptTokens:     1_000_000,
			CompletionTokens: 1_000_000,
			TotalTokens:      2_000_000,
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
		t.Fatalf("expected two routing-rule breakdown rows, got %d: %#v", len(rows), rows)
	}
	if rows[0].RoutingRuleID == nil || *rows[0].RoutingRuleID != reviewRuleID || rows[0].RoutingRuleName == nil || *rows[0].RoutingRuleName != reviewRuleName {
		t.Fatalf("expected highest revenue row to be review rule, got %#v", rows[0])
	}
	if !almostEqualFloat(rows[0].RevenueUSD, 35) {
		t.Fatalf("review rule revenue mismatch: got %.6f want 35.000000", rows[0].RevenueUSD)
	}
	if rows[1].RoutingRuleID == nil || *rows[1].RoutingRuleID != autoRuleID || rows[1].RoutingRuleName == nil || *rows[1].RoutingRuleName != autoRuleName {
		t.Fatalf("expected second row to be auto rule, got %#v", rows[1])
	}
	if !almostEqualFloat(rows[1].RevenueUSD, 14) {
		t.Fatalf("auto rule revenue mismatch: got %.6f want 14.000000", rows[1].RevenueUSD)
	}
}
