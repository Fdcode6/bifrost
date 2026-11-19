package governance

import (
	"math"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
)

func TestInspectAdaptiveRuleDashboard_V22StatesAndRoutePreview(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	ht := NewHealthTracker()

	enabled := true
	policy := &configstoreTables.HealthPolicy{
		AdaptiveRankingEnabled:  &enabled,
		AdaptiveV22Enabled:      true,
		RequestDeadlineMs:       58000,
		MaxFallbackAttempts:     5,
		PrimaryAttemptTimeoutMs: 35000,
		FastRescueElapsedMs:     15000,
		RescueAttemptTimeoutMs:  20000,
		MinQualifiedSuccessRate: 0.70,
		MinActiveTargets:        2,
	}

	strPtr := func(s string) *string { return &s }
	rule := &configstoreTables.TableRoutingRule{
		ID:                    "rule-gemini",
		Name:                  "gemini-auto",
		Enabled:               true,
		GroupedRoutingEnabled: true,
		ParsedHealthPolicy:    policy,
		ParsedRouteGroups: []configstoreTables.RouteGroup{
			{
				Name: "L1-profit",
				Targets: []configstoreTables.RouteGroupTarget{
					{Provider: strPtr("storeapi-aistudio"), Model: strPtr("gemini-3.1-pro-preview"), PrimaryAttemptTimeoutMs: 22000, Weight: 1},
					{Provider: strPtr("openlux-aistudio-3"), Model: strPtr("gemini-3.1-pro-preview"), PrimaryAttemptTimeoutMs: 28000, Weight: 1},
					{Provider: strPtr("bai-40pct"), Model: strPtr("gemini-3.1-pro"), Weight: 1},
					{Provider: strPtr("polo-flaky"), Model: strPtr("gemini-3.1-pro-preview"), Weight: 1},
					{Provider: strPtr("polo-tripwire"), Model: strPtr("gemini-3.1-pro-preview"), Weight: 1},
				},
			},
			{
				Name:         "L2-backup",
				FallbackOnly: true,
				Targets: []configstoreTables.RouteGroupTarget{
					{Provider: strPtr("bai-70pct"), Model: strPtr("gemini-3.1-pro"), Weight: 1},
				},
			},
			{
				Name:       "L3-last-resort",
				LastResort: true,
				Targets: []configstoreTables.RouteGroupTarget{
					{Provider: strPtr("bai-100pct"), Model: strPtr("gemini-3.1-pro"), Weight: 1},
				},
			},
		},
	}

	// Seed realistic health outcomes:
	// 1. storeapi: fast, cheap, healthy
	for i := 0; i < 8; i++ {
		ht.RecordOutcome(rule.ID, "storeapi-aistudio:gemini-3.1-pro-preview", OutcomeSuccess, 10*time.Second, "", policy, now.Add(-time.Duration(i+1)*time.Minute))
		ht.observeRescue(rule.ID, "storeapi-aistudio:gemini-3.1-pro-preview", false, OutcomeSuccess, 10*time.Second, now.Add(-time.Duration(i+1)*time.Minute))
	}
	// 2. openlux-3: proven fast rescue (10m 8/8 timely)
	for i := 0; i < 8; i++ {
		ht.RecordOutcome(rule.ID, "openlux-aistudio-3:gemini-3.1-pro-preview", OutcomeSuccess, 12*time.Second, "", policy, now.Add(-time.Duration(i+1)*time.Minute))
		ht.observeRescue(rule.ID, "openlux-aistudio-3:gemini-3.1-pro-preview", false, OutcomeSuccess, 12*time.Second, now.Add(-time.Duration(i+1)*time.Minute))
	}
	// 3. bai-40pct: healthy, 6/8 timely in 10m
	for i := 0; i < 8; i++ {
		lat := 11 * time.Second
		if i < 2 {
			lat = 19 * time.Second
		}
		ht.RecordOutcome(rule.ID, "bai-40pct:gemini-3.1-pro", OutcomeSuccess, lat, "", policy, now.Add(-time.Duration(i+1)*time.Minute))
		ht.observeRescue(rule.ID, "bai-40pct:gemini-3.1-pro", false, OutcomeSuccess, lat, now.Add(-time.Duration(i+1)*time.Minute))
	}
	// 4. polo-flaky: degraded into probation with probe due
	for i := 0; i < 6; i++ {
		ht.RecordOutcome(rule.ID, "polo-flaky:gemini-3.1-pro-preview", OutcomeHardFail, 2*time.Second, "451 empty response", policy, now.Add(-20*time.Minute+time.Duration(i)*time.Second))
	}
	// 5. polo-tripwire: 2 consecutive timeouts -> 60s short tripwire
	ht.RecordOutcome(rule.ID, "polo-tripwire:gemini-3.1-pro-preview", OutcomeHardFail, 25*time.Second, "504 request timed out", policy, now.Add(-20*time.Second))
	ht.RecordOutcome(rule.ID, "polo-tripwire:gemini-3.1-pro-preview", OutcomeHardFail, 25*time.Second, "504 request timed out", policy, now.Add(-10*time.Second))

	pricingMap := map[string]TargetPricingInfo{
		"storeapi-aistudio":  {InputCostPer1MUSD: 0.0442, OutputCostPer1MUSD: 0.2652, RawCost: (0.0442 + 0.2652) / 1e6, HasPricing: true},
		"openlux-aistudio-3": {InputCostPer1MUSD: 0.8648, OutputCostPer1MUSD: 5.1888, RawCost: (0.8648 + 5.1888) / 1e6, HasPricing: true},
		"bai-40pct":          {InputCostPer1MUSD: 0.80, OutputCostPer1MUSD: 4.80, RawCost: 5.60 / 1e6, HasPricing: true},
		"polo-flaky":         {InputCostPer1MUSD: 0.294, OutputCostPer1MUSD: 1.765, RawCost: 2.059 / 1e6, HasPricing: true},
		"polo-tripwire":      {InputCostPer1MUSD: 0.588, OutputCostPer1MUSD: 3.529, RawCost: 4.117 / 1e6, HasPricing: true},
		"bai-70pct":          {InputCostPer1MUSD: 1.40, OutputCostPer1MUSD: 8.40, RawCost: 9.80 / 1e6, HasPricing: true},
		"bai-100pct":         {InputCostPer1MUSD: 2.00, OutputCostPer1MUSD: 12.00, RawCost: 14.00 / 1e6, HasPricing: true},
	}

	res := InspectAdaptiveRuleDashboard(
		rule,
		ht,
		func(provider schemas.ModelProvider, _ string, _ schemas.RequestType) TargetPricingInfo {
			return pricingMap[string(provider)]
		},
		func(provider schemas.ModelProvider) string {
			return string(provider) + ".example.com"
		},
		nil,
		now,
	)

	if res.EngineMode != "adaptive_v22" {
		t.Fatalf("expected engine_mode=adaptive_v22, got %s", res.EngineMode)
	}
	if res.EffectiveBudgetMs != 58000 || res.EffectiveMaxAttempts != 5 || res.FinalHopReserveMs != 14000 {
		t.Fatalf("unexpected policy summary: budget=%d maxAttempts=%d reserve=%d", res.EffectiveBudgetMs, res.EffectiveMaxAttempts, res.FinalHopReserveMs)
	}
	if res.PoolCounts.L1Active != 3 || res.PoolCounts.L1Tripwire != 1 || res.PoolCounts.L1Probation != 1 || res.PoolCounts.L2Ready != 1 || res.PoolCounts.L3Ready != 1 {
		t.Fatalf("unexpected pool counts: %+v", res.PoolCounts)
	}

	// Verify official baseline pricing captured from L3 (100%)
	if res.OfficialBaselineInputPer1MUSD == nil || math.Abs(*res.OfficialBaselineInputPer1MUSD-2.0) > 0.001 {
		t.Fatalf("expected official baseline input=2.0, got %v", res.OfficialBaselineInputPer1MUSD)
	}

	// Verify #1 primary is storeapi with 2.21% official cost and 22s timeout
	if res.RoutePreview == nil || res.RoutePreview.Primary == nil {
		t.Fatalf("expected route preview primary")
	}
	if res.RoutePreview.Primary.Provider != "storeapi-aistudio" || res.RoutePreview.Primary.TimeoutMs != 22000 {
		t.Fatalf("unexpected primary preview: %+v", res.RoutePreview.Primary)
	}
	if res.RoutePreview.Primary.OfficialCostPct == nil || math.Abs(*res.RoutePreview.Primary.OfficialCostPct-2.21) > 0.05 {
		t.Fatalf("expected ~2.21%% official cost for storeapi, got %v", res.RoutePreview.Primary.OfficialCostPct)
	}

	// Timeout rescue should put openlux-aistudio-3 (8/8 timely) ahead of bai-40pct (6/8 timely)
	if len(res.RoutePreview.TimeoutRescue) < 2 || res.RoutePreview.TimeoutRescue[0].Provider != "openlux-aistudio-3" {
		t.Fatalf("expected openlux-aistudio-3 as #1 timeout rescue, got %+v", res.RoutePreview.TimeoutRescue)
	}

	// Final hop fallback should include L2 bai-70pct and L3 bai-100pct
	if len(res.RoutePreview.FinalHopFallback) != 2 || res.RoutePreview.FinalHopFallback[0].Provider != "bai-70pct" || res.RoutePreview.FinalHopFallback[1].Provider != "bai-100pct" {
		t.Fatalf("unexpected final hop fallback preview: %+v", res.RoutePreview.FinalHopFallback)
	}

	// Inspection MUST be strictly read-only: probation probe must remain unclaimed!
	snapAfter := ht.InspectAdaptiveTargetForRule(rule.ID, "polo-flaky:gemini-3.1-pro-preview", policy, now)
	if snapAfter.ProbationProbeInFlight {
		t.Fatalf("InspectAdaptiveRuleDashboard must not claim probation probe lease")
	}
}
