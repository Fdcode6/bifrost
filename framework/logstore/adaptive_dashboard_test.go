package logstore

import (
	"context"
	"math"
	"testing"
	"time"
)

func TestGetAdaptiveDashboardMetrics_RootAndTargetAggregation(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	ruleID := "rule-gemini-auto"
	otherRuleID := "rule-other"
	base := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)

	_, err := store.SaveRoutingRuleProfitSettings(ctx, ruleID, &ProfitSettings{
		SellInputPer1MUSD:  2.3,
		SellOutputPer1MUSD: 13.8,
		Timezone:           "Asia/Shanghai",
	})
	if err != nil {
		t.Fatalf("SaveRoutingRuleProfitSettings error: %v", err)
	}

	logs := []*Log{
		// Root 1: first-hop success on storeapi (10s)
		{
			ID:               "root-1",
			Timestamp:        base.Add(1 * time.Minute),
			Object:           "chat.completion",
			Provider:         "storeapi-aistudio",
			Model:            "gemini-3.1-pro-preview",
			SelectedKeyID:    "key-storeapi",
			RoutingRuleID:    &ruleID,
			FallbackIndex:    0,
			Status:           "success",
			Latency:          floatPtr(10000),
			Cost:             floatPtr(0.001),
			PromptTokens:     1000,
			CompletionTokens: 500,
			TotalTokens:      1500,
		},
		// Root 2: Hop 0 fails on storeapi (22s), Hop 1 succeeds on openlux (12s) -> Rescued! Total e2e latency = 34s
		{
			ID:            "root-2",
			Timestamp:     base.Add(2 * time.Minute),
			Object:        "chat.completion",
			Provider:      "storeapi-aistudio",
			Model:         "gemini-3.1-pro-preview",
			SelectedKeyID: "key-storeapi",
			RoutingRuleID: &ruleID,
			FallbackIndex: 0,
			Status:        "error",
			Latency:       floatPtr(22000),
		},
		{
			ID:               "root-2-fb1",
			ParentRequestID:  stringPtr("root-2"),
			Timestamp:        base.Add(2*time.Minute + 22*time.Second),
			Object:           "chat.completion",
			Provider:         "openlux-aistudio-3",
			Model:            "gemini-3.1-pro-preview",
			SelectedKeyID:    "key-openlux3",
			RoutingRuleID:    &ruleID,
			FallbackIndex:    1,
			Status:           "success",
			Latency:          floatPtr(12000),
			Cost:             floatPtr(0.005),
			PromptTokens:     1000,
			CompletionTokens: 500,
			TotalTokens:      1500,
		},
		// Root 3: Hop 0 fails on storeapi (5s), Hop 1 fails on openlux (8s) -> Total failure
		{
			ID:            "root-3",
			Timestamp:     base.Add(3 * time.Minute),
			Object:        "chat.completion",
			Provider:      "storeapi-aistudio",
			Model:         "gemini-3.1-pro-preview",
			SelectedKeyID: "key-storeapi",
			RoutingRuleID: &ruleID,
			FallbackIndex: 0,
			Status:        "error",
			Latency:       floatPtr(5000),
		},
		{
			ID:              "root-3-fb1",
			ParentRequestID: stringPtr("root-3"),
			Timestamp:       base.Add(3*time.Minute + 5*time.Second),
			Object:          "chat.completion",
			Provider:        "openlux-aistudio-3",
			Model:           "gemini-3.1-pro-preview",
			SelectedKeyID:   "key-openlux3",
			RoutingRuleID:   &ruleID,
			FallbackIndex:   1,
			Status:          "error",
			Latency:         floatPtr(8000),
		},
		// Root 4: first-hop success on storeapi (8s)
		{
			ID:               "root-4",
			Timestamp:        base.Add(4 * time.Minute),
			Object:           "chat.completion",
			Provider:         "storeapi-aistudio",
			Model:            "gemini-3.1-pro-preview",
			SelectedKeyID:    "key-storeapi",
			RoutingRuleID:    &ruleID,
			FallbackIndex:    0,
			Status:           "success",
			Latency:          floatPtr(8000),
			Cost:             floatPtr(0.002),
			PromptTokens:     2000,
			CompletionTokens: 1000,
			TotalTokens:      3000,
		},
		// Other rule log (must be excluded)
		{
			ID:               "other-rule-1",
			Timestamp:        base.Add(5 * time.Minute),
			Object:           "chat.completion",
			Provider:         "storeapi-aistudio",
			Model:            "gemini-3.1-pro-preview",
			SelectedKeyID:    "key-storeapi",
			RoutingRuleID:    &otherRuleID,
			FallbackIndex:    0,
			Status:           "success",
			Latency:          floatPtr(1000),
			Cost:             floatPtr(0.5),
			PromptTokens:     5000,
			CompletionTokens: 5000,
			TotalTokens:      10000,
		},
		// Out of window log (must be excluded)
		{
			ID:               "old-log-1",
			Timestamp:        base.Add(-2 * time.Hour),
			Object:           "chat.completion",
			Provider:         "storeapi-aistudio",
			Model:            "gemini-3.1-pro-preview",
			SelectedKeyID:    "key-storeapi",
			RoutingRuleID:    &ruleID,
			FallbackIndex:    0,
			Status:           "success",
			Latency:          floatPtr(2000),
			Cost:             floatPtr(0.2),
			PromptTokens:     1000,
			CompletionTokens: 1000,
			TotalTokens:      2000,
		},
	}

	for _, l := range logs {
		requireCreateLog(t, store, l)
		if _, err := store.UpsertProfitEventFromLog(ctx, l); err != nil {
			t.Fatalf("UpsertProfitEventFromLog(%s) error: %v", l.ID, err)
		}
	}

	res, err := store.GetAdaptiveDashboardMetrics(ctx, AdaptiveDashboardMetricsQuery{
		RoutingRuleID: ruleID,
		StartTime:     base,
		EndTime:       base.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("GetAdaptiveDashboardMetrics error: %v", err)
	}

	// 4 root requests: 3 succeeded (root-1, root-2, root-4), 2 first-hop (root-1, root-4), 1 rescued (root-2)
	if res.RootSummary.RootRequestCount != 4 {
		t.Fatalf("expected root_request_count=4, got %d", res.RootSummary.RootRequestCount)
	}
	if res.RootSummary.RootSuccessCount != 3 {
		t.Fatalf("expected root_success_count=3, got %d", res.RootSummary.RootSuccessCount)
	}
	if res.RootSummary.FirstHopSuccessCount != 2 {
		t.Fatalf("expected first_hop_success_count=2, got %d", res.RootSummary.FirstHopSuccessCount)
	}
	if res.RootSummary.RescuedCount != 1 {
		t.Fatalf("expected rescued_count=1, got %d", res.RootSummary.RescuedCount)
	}
	if res.RootSummary.SuccessRate == nil || math.Abs(*res.RootSummary.SuccessRate-75.0) > 0.01 {
		t.Fatalf("expected root success_rate=75, got %v", res.RootSummary.SuccessRate)
	}
	if res.RootSummary.FirstHopSuccessRate == nil || math.Abs(*res.RootSummary.FirstHopSuccessRate-50.0) > 0.01 {
		t.Fatalf("expected first_hop_success_rate=50, got %v", res.RootSummary.FirstHopSuccessRate)
	}
	// Root success latencies sorted: [8000, 10000, 34000] -> P50 = 10000, Avg = 52000/3 = 17333.33
	if res.RootSummary.P50LatencyMs == nil || math.Abs(*res.RootSummary.P50LatencyMs-10000) > 0.01 {
		t.Fatalf("expected root p50=10000, got %v", res.RootSummary.P50LatencyMs)
	}

	// Verify per-target single-hop & profit metrics
	if len(res.Targets) != 2 {
		t.Fatalf("expected 2 target rows, got %d: %+v", len(res.Targets), res.Targets)
	}
	byProvider := map[string]AdaptiveDashboardTargetStats{}
	for _, tgt := range res.Targets {
		byProvider[tgt.Provider] = tgt
	}

	storeapi := byProvider["storeapi-aistudio"]
	if storeapi.RequestCount != 4 || storeapi.SuccessCount != 2 || storeapi.ErrorCount != 2 {
		t.Fatalf("unexpected storeapi hop counts: %+v", storeapi)
	}
	// storeapi success latencies: [8000, 10000] -> avg 9000, p50 9000 (errors 22000 & 5000 excluded!)
	if storeapi.AvgLatencyMs == nil || math.Abs(*storeapi.AvgLatencyMs-9000) > 0.01 {
		t.Fatalf("expected storeapi avg success latency 9000, got %v", storeapi.AvgLatencyMs)
	}
	// storeapi profit: 3000 prompt * 2.3/1M + 1500 completion * 13.8/1M = 0.0069 + 0.0207 = 0.0276 revenue; cost = 0.003
	if math.Abs(storeapi.RevenueUSD-0.0276) > 1e-6 || math.Abs(storeapi.CostUSD-0.003) > 1e-6 {
		t.Fatalf("unexpected storeapi profit: rev=%f cost=%f", storeapi.RevenueUSD, storeapi.CostUSD)
	}

	openlux := byProvider["openlux-aistudio-3"]
	if openlux.RequestCount != 2 || openlux.SuccessCount != 1 || openlux.ErrorCount != 1 {
		t.Fatalf("unexpected openlux hop counts: %+v", openlux)
	}
	if openlux.P50LatencyMs == nil || math.Abs(*openlux.P50LatencyMs-12000) > 0.01 {
		t.Fatalf("expected openlux p50=12000, got %v", openlux.P50LatencyMs)
	}

	// Totals across rule
	if math.Abs(res.TotalCostUSD-0.008) > 1e-6 {
		t.Fatalf("expected total cost 0.008, got %f", res.TotalCostUSD)
	}
	if res.TotalPromptTokens != 4000 || res.TotalCompletionTokens != 2000 {
		t.Fatalf("unexpected total tokens: prompt=%d completion=%d", res.TotalPromptTokens, res.TotalCompletionTokens)
	}
	if res.GrossMargin == nil || *res.GrossMargin <= 0 {
		t.Fatalf("expected positive gross margin, got %v", res.GrossMargin)
	}
}
