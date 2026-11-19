package handlers

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/logstore"
	"github.com/maximhq/bifrost/plugins/governance"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

type mockAdaptiveMetricsStore struct {
	calls  int
	result *logstore.AdaptiveDashboardMetricsResult
}

func (m *mockAdaptiveMetricsStore) GetAdaptiveDashboardMetrics(_ context.Context, _ logstore.AdaptiveDashboardMetricsQuery) (*logstore.AdaptiveDashboardMetricsResult, error) {
	m.calls++
	return m.result, nil
}

type mockAdaptiveDashboardRuntime struct {
	*mockAdaptiveRoutingRuntime
	pricing map[string]governance.TargetPricingInfo
}

func (m *mockAdaptiveDashboardRuntime) GetGovernancePlugin() governance.BaseGovernancePlugin {
	return m
}

func (m *mockAdaptiveDashboardRuntime) InspectAdaptiveDashboardRule(rule *configstoreTables.TableRoutingRule, now time.Time) *governance.AdaptiveDashboardRuleRuntime {
	return governance.InspectAdaptiveRuleDashboard(
		rule,
		m.healthTracker,
		func(provider schemas.ModelProvider, _ string, _ schemas.RequestType) governance.TargetPricingInfo {
			return m.pricing[string(provider)]
		},
		func(provider schemas.ModelProvider) string {
			return string(provider) + ".host"
		},
		nil,
		now,
	)
}

func TestGetAdaptiveDashboard_MergesLiveStateAndWindowMetricsWithCache(t *testing.T) {
	SetLogger(&mockLogger{})
	ht := governance.NewHealthTracker()
	now := time.Now()
	enabled := true
	strPtr := func(s string) *string { return &s }

	v22Policy := &configstoreTables.HealthPolicy{
		AdaptiveRankingEnabled:  &enabled,
		AdaptiveV22Enabled:      true,
		RequestDeadlineMs:       58000,
		MaxFallbackAttempts:     5,
		PrimaryAttemptTimeoutMs: 35000,
		FastRescueElapsedMs:     15000,
		RescueAttemptTimeoutMs:  20000,
	}

	geminiRule := &configstoreTables.TableRoutingRule{
		ID:                    "rule-gemini",
		Name:                  "gemini-auto",
		Enabled:               true,
		Priority:              10,
		GroupedRoutingEnabled: true,
		ParsedHealthPolicy:    v22Policy,
		ParsedRouteGroups: []configstoreTables.RouteGroup{
			{
				Name: "L1",
				Targets: []configstoreTables.RouteGroupTarget{
					{Provider: strPtr("storeapi-aistudio"), Model: strPtr("gemini-3.1-pro-preview"), PrimaryAttemptTimeoutMs: 22000, Weight: 1},
					{Provider: strPtr("openlux-aistudio-3"), Model: strPtr("gemini-3.1-pro-preview"), PrimaryAttemptTimeoutMs: 28000, Weight: 1},
				},
			},
			{
				Name:         "L2",
				FallbackOnly: true,
				Targets: []configstoreTables.RouteGroupTarget{
					{Provider: strPtr("bai-70pct"), Model: strPtr("gemini-3.1-pro"), Weight: 1},
				},
			},
			{
				Name:       "L3",
				LastResort: true,
				Targets: []configstoreTables.RouteGroupTarget{
					{Provider: strPtr("bai-100pct"), Model: strPtr("gemini-3.1-pro"), Weight: 1},
				},
			},
		},
	}

	groupedRule := &configstoreTables.TableRoutingRule{
		ID:                    "rule-audit",
		Name:                  "会话审查",
		Enabled:               true,
		Priority:              1,
		GroupedRoutingEnabled: true,
		ParsedRouteGroups: []configstoreTables.RouteGroup{
			{
				Name: "default",
				Targets: []configstoreTables.RouteGroupTarget{
					{Provider: strPtr("doubao-main"), Model: strPtr("doubao-mini"), Weight: 1},
				},
			},
		},
	}

	ht.RecordOutcome(geminiRule.ID, "storeapi-aistudio:gemini-3.1-pro-preview", governance.OutcomeSuccess, 10*time.Second, "", v22Policy, now.Add(-1*time.Minute))
	ht.RecordOutcome(geminiRule.ID, "openlux-aistudio-3:gemini-3.1-pro-preview", governance.OutcomeSuccess, 12*time.Second, "", v22Policy, now.Add(-1*time.Minute))

	rt := &mockAdaptiveDashboardRuntime{
		mockAdaptiveRoutingRuntime: &mockAdaptiveRoutingRuntime{
			store:         &mockAdaptiveRoutingStore{rules: []*configstoreTables.TableRoutingRule{groupedRule, geminiRule}},
			healthTracker: ht,
		},
		pricing: map[string]governance.TargetPricingInfo{
			"storeapi-aistudio":  {InputCostPer1MUSD: 0.0442, OutputCostPer1MUSD: 0.2652, RawCost: (0.0442 + 0.2652) / 1e6, HasPricing: true},
			"openlux-aistudio-3": {InputCostPer1MUSD: 0.8648, OutputCostPer1MUSD: 5.1888, RawCost: (0.8648 + 5.1888) / 1e6, HasPricing: true},
			"bai-70pct":          {InputCostPer1MUSD: 1.40, OutputCostPer1MUSD: 8.40, RawCost: 9.80 / 1e6, HasPricing: true},
			"bai-100pct":         {InputCostPer1MUSD: 2.00, OutputCostPer1MUSD: 12.00, RawCost: 14.00 / 1e6, HasPricing: true},
		},
	}

	handler, err := NewAdaptiveRoutingHandler(rt, nil)
	require.NoError(t, err)

	fPtr := func(v float64) *float64 { return &v }
	mockMetrics := &mockAdaptiveMetricsStore{
		result: &logstore.AdaptiveDashboardMetricsResult{
			RootSummary: logstore.AdaptiveDashboardRootSummary{
				RootRequestCount:     100,
				RootSuccessCount:     99,
				FirstHopSuccessCount: 88,
				RescuedCount:         11,
				SuccessRate:          fPtr(99.0),
				FirstHopSuccessRate:  fPtr(88.0),
				AvgLatencyMs:         fPtr(11200),
				P50LatencyMs:         fPtr(10000),
				P80LatencyMs:         fPtr(14500),
				P95LatencyMs:         fPtr(25000),
			},
			Targets: []logstore.AdaptiveDashboardTargetStats{
				{
					Provider:         "storeapi-aistudio",
					Model:            "gemini-3.1-pro-preview",
					SelectedKeyID:    "any-key-id",
					RequestCount:     95,
					SuccessCount:     85,
					ErrorCount:       10,
					AvgLatencyMs:     fPtr(10200),
					P50LatencyMs:     fPtr(9600),
					P80LatencyMs:     fPtr(12000),
					P95LatencyMs:     fPtr(16000),
					RevenueUSD:       20.0,
					CostUSD:          0.4,
					ProfitUSD:        19.6,
					PromptTokens:     5_000_000,
					CompletionTokens: 500_000,
					TotalTokens:      5_500_000,
				},
				{
					Provider:         "bai-70pct",
					Model:            "gemini-3.1-pro",
					SelectedKeyID:    "key-bai70",
					RequestCount:     15,
					SuccessCount:     14,
					ErrorCount:       1,
					AvgLatencyMs:     fPtr(11800),
					P50LatencyMs:     fPtr(11000),
					P80LatencyMs:     fPtr(13500),
					P95LatencyMs:     fPtr(17000),
					RevenueUSD:       4.0,
					CostUSD:          2.4,
					ProfitUSD:        1.6,
					PromptTokens:     1_000_000,
					CompletionTokens: 100_000,
					TotalTokens:      1_100_000,
				},
			},
			TotalRevenueUSD:       24.0,
			TotalCostUSD:          2.8,
			TotalProfitUSD:        21.2,
			GrossMargin:           fPtr(21.2 / 24.0),
			TotalPromptTokens:     6_000_000,
			TotalCompletionTokens: 600_000,
			TotalTokens:           6_600_000,
		},
	}
	handler.setMetricsStore(mockMetrics)

	// 1. Default request auto-selects adaptive_v22 rule ahead of legacy grouped rule
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("GET")
	ctx.Request.SetRequestURI("/api/governance/adaptive-dashboard?window=today")
	handler.getAdaptiveDashboard(ctx)
	require.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode(), string(ctx.Response.Body()))

	var resp AdaptiveDashboardResponse
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &resp))
	require.Equal(t, "today", resp.Window)
	require.True(t, resp.LogsConnected)
	require.Len(t, resp.Rules, 2)
	require.Equal(t, "rule-gemini", resp.Rules[0].RuleID)
	require.NotNil(t, resp.SelectedRule)
	require.Equal(t, "rule-gemini", resp.SelectedRule.RuleID)
	require.Equal(t, "adaptive_v22", resp.SelectedRule.EngineMode)
	require.Equal(t, int64(99), resp.KPI.RootSuccessCount)
	require.Equal(t, int64(11), resp.KPI.RescuedCount)
	require.NotNil(t, resp.KPI.EffectiveOfficialPct)
	// officialRef = 6M * 2/1M + 0.6M * 12/1M = 12 + 7.2 = 19.2 USD; 2.8 / 19.2 * 100 = 14.583%
	require.InDelta(t, 14.583, *resp.KPI.EffectiveOfficialPct, 0.05)

	require.Len(t, resp.Tiers, 3)
	require.Equal(t, "L1", resp.Tiers[0].Tier)
	require.Equal(t, int64(85), resp.Tiers[0].SuccessCount)
	require.InDelta(t, 85.0/99.0*100.0, resp.Tiers[0].SuccessShare, 0.05)
	require.Equal(t, "L2", resp.Tiers[1].Tier)
	require.Equal(t, int64(14), resp.Tiers[1].SuccessCount)
	require.Equal(t, "L3", resp.Tiers[2].Tier)
	require.Equal(t, 1, mockMetrics.calls)

	// 2. Second request within 15s hits cache (mockMetrics.calls stays 1), yet picks up live health updates!
	ht.RecordOutcome(geminiRule.ID, "openlux-aistudio-3:gemini-3.1-pro-preview", governance.OutcomeHardFail, 25*time.Second, "504 timeout", v22Policy, time.Now())
	ht.RecordOutcome(geminiRule.ID, "openlux-aistudio-3:gemini-3.1-pro-preview", governance.OutcomeHardFail, 25*time.Second, "504 timeout", v22Policy, time.Now())

	ctx2 := &fasthttp.RequestCtx{}
	ctx2.Request.Header.SetMethod("GET")
	ctx2.Request.SetRequestURI("/api/governance/adaptive-dashboard?window=today")
	handler.getAdaptiveDashboard(ctx2)
	require.Equal(t, fasthttp.StatusOK, ctx2.Response.StatusCode())
	require.Equal(t, 1, mockMetrics.calls, "second call within TTL should reuse cached DB metrics")

	var resp2 AdaptiveDashboardResponse
	require.NoError(t, json.Unmarshal(ctx2.Response.Body(), &resp2))
	require.Equal(t, 1, resp2.SelectedRule.PoolCounts.L1Tripwire, "live health update must bypass DB cache")

	// 3. refresh=true forces fresh DB metrics fetch
	ctx3 := &fasthttp.RequestCtx{}
	ctx3.Request.Header.SetMethod("GET")
	ctx3.Request.SetRequestURI("/api/governance/adaptive-dashboard?window=today&refresh=true")
	handler.getAdaptiveDashboard(ctx3)
	require.Equal(t, fasthttp.StatusOK, ctx3.Response.StatusCode())
	require.Equal(t, 2, mockMetrics.calls)

	// 4. Invalid window returns 400
	ctx4 := &fasthttp.RequestCtx{}
	ctx4.Request.Header.SetMethod("GET")
	ctx4.Request.SetRequestURI("/api/governance/adaptive-dashboard?window=invalid")
	handler.getAdaptiveDashboard(ctx4)
	require.Equal(t, fasthttp.StatusBadRequest, ctx4.Response.StatusCode())

	// Ensure math import used
	require.False(t, math.IsNaN(resp.KPI.TotalProfitUSD))
}
