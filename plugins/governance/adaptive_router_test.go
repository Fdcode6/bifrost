package governance

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func adaptiveStrPtr(v string) *string { return &v }

func makeAdaptiveTestPolicy() *configstoreTables.HealthPolicy {
	return ApplyHealthPolicyDefaults(&configstoreTables.HealthPolicy{
		AdaptiveRankingEnabled:    boolPtr(true),
		ReputationWindowSeconds:   5400,
		MinQualifiedSuccessRate:   0.70,
		MinActiveTargets:          3,
		PrimaryAttemptTimeoutMs:   38000,
		FastRescueElapsedMs:       15000,
		FastChannelThresholdMs:    16000,
		RescueAttemptTimeoutMs:    20000,
		ProbationProbeBaseSeconds: 300,
		ProbationProbeMaxSeconds:  900,
		ProbationRecoverSuccesses: 2,
		MaxFallbackAttempts:       5,
		RequestDeadlineMs:         56000,
	})
}

func TestAdaptiveRouter_SlowSteadyCheapBeatsFastMidPrice(t *testing.T) {
	ht := NewHealthTracker()
	policy := makeAdaptiveTestPolicy()
	now := time.Now()
	ruleID := "rule-adaptive-1"

	cheapKey := TargetKey("cheap-25pct", "gemini-3.1-pro-preview", "k1")
	fastMidKey := TargetKey("openlux-43pct", "gemini-3.1-pro-preview", "k2")
	backup70Key := TargetKey("bai-70pct", "gemini-3.1-pro", "k3")
	fullPriceKey := TargetKey("bai-100pct", "gemini-3.1-pro", "k4")

	// Cheap channel takes 34s (<= 38s budget!) with 100% success over 8 calls
	for i := 0; i < 8; i++ {
		ht.RecordOutcome(ruleID, cheapKey, OutcomeSuccess, 34*time.Second, "", policy, now.Add(time.Duration(i)*time.Second))
	}
	// Fast mid-price channel takes 11s with 100% success over 20 calls
	for i := 0; i < 20; i++ {
		ht.RecordOutcome(ruleID, fastMidKey, OutcomeSuccess, 11*time.Second, "", policy, now.Add(time.Duration(i)*time.Second))
	}
	ht.RecordOutcome(ruleID, backup70Key, OutcomeSuccess, 11*time.Second, "", policy, now)
	ht.RecordOutcome(ruleID, fullPriceKey, OutcomeSuccess, 11*time.Second, "", policy, now)

	rule := &configstoreTables.TableRoutingRule{
		ID:                    ruleID,
		Name:                  "gemini-auto",
		Enabled:               true,
		GroupedRoutingEnabled: true,
		ParsedHealthPolicy:    policy,
		ParsedRouteGroups: []configstoreTables.RouteGroup{
			{
				Name: "profit-pool",
				Targets: []configstoreTables.RouteGroupTarget{
					{Provider: adaptiveStrPtr("cheap-25pct"), Model: adaptiveStrPtr("gemini-3.1-pro-preview"), KeyID: adaptiveStrPtr("k1"), Weight: 0.5},
					{Provider: adaptiveStrPtr("openlux-43pct"), Model: adaptiveStrPtr("gemini-3.1-pro-preview"), KeyID: adaptiveStrPtr("k2"), Weight: 0.5},
				},
			},
			{
				Name:         "backup-pool",
				FallbackOnly: true,
				Targets: []configstoreTables.RouteGroupTarget{
					{Provider: adaptiveStrPtr("bai-70pct"), Model: adaptiveStrPtr("gemini-3.1-pro"), KeyID: adaptiveStrPtr("k3"), Weight: 1.0},
				},
			},
			{
				Name:         "last-resort-100pct",
				FallbackOnly: true,
				LastResort:   true,
				Targets: []configstoreTables.RouteGroupTarget{
					{Provider: adaptiveStrPtr("bai-100pct"), Model: adaptiveStrPtr("gemini-3.1-pro"), KeyID: adaptiveStrPtr("k4"), Weight: 1.0},
				},
			},
		},
	}

	costs := map[schemas.ModelProvider]float64{
		"cheap-25pct":   0.25,
		"openlux-43pct": 0.432,
		"bai-70pct":     0.70,
		"bai-100pct":    1.00,
	}
	rctx := &RoutingContext{
		Model:       "gemini-auto",
		RequestType: string(schemas.ChatCompletionRequest),
		TargetCostResolver: func(p schemas.ModelProvider, _ string, _ schemas.RequestType) (float64, bool) {
			c, ok := costs[p]
			return c, ok
		},
		TargetUpstreamResolver: func(p schemas.ModelProvider) string {
			return string(p)
		},
	}

	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	dec := buildGroupedRoutingDecision(ctx, rule, rctx, ht, nil)
	require.NotNil(t, dec)
	assert.Equal(t, "cheap-25pct", dec.Provider, "34s steady cheap channel must rank #1 to maximize margin")
	require.Equal(t, []string{
		"openlux-43pct/gemini-3.1-pro-preview",
		"bai-70pct/gemini-3.1-pro",
		"bai-100pct/gemini-3.1-pro",
	}, dec.Fallbacks)
	require.NotNil(t, dec.AdaptivePolicy)
	assert.False(t, dec.AdaptivePolicy.PrimaryHint.IsFastRescue, "34s channel is not a fast-rescue channel")
	assert.True(t, dec.AdaptivePolicy.FallbackHints[0].IsFastRescue, "11s openlux-43pct must be marked IsFastRescue")
	assert.False(t, dec.AdaptivePolicy.FallbackHints[1].IsLastResort, "70% backup is not last resort")
	assert.True(t, dec.AdaptivePolicy.FallbackHints[2].IsLastResort, "100% channel must be marked IsLastResort")
}

func TestAdaptiveRouter_ProvenChannelImmuneToTwoTransient503s(t *testing.T) {
	ht := NewHealthTracker()
	policy := makeAdaptiveTestPolicy()
	now := time.Now()
	ruleID := "rule-adaptive-2"

	goldKey := TargetKey("openlux-aistudio-3", "gemini-3.1-pro-preview", "k1")
	altKey := TargetKey("bai-60pct", "gemini-3.1-pro", "k2")

	for i := 0; i < 40; i++ {
		ht.RecordOutcome(ruleID, goldKey, OutcomeSuccess, 11*time.Second, "", policy, now.Add(time.Duration(i)*time.Second))
		ht.RecordOutcome(ruleID, altKey, OutcomeSuccess, 12*time.Second, "", policy, now.Add(time.Duration(i)*time.Second))
	}
	// Two transient fast 503s (300ms) on openlux-aistudio-3
	tErr := now.Add(45 * time.Second)
	ht.RecordOutcome(ruleID, goldKey, OutcomeSoftFail, 300*time.Millisecond, "503 service overloaded", policy, tErr)
	ht.RecordOutcome(ruleID, goldKey, OutcomeSoftFail, 300*time.Millisecond, "503 service overloaded", policy, tErr.Add(time.Second))

	rule := &configstoreTables.TableRoutingRule{
		ID:                    ruleID,
		Name:                  "gemini-auto",
		Enabled:               true,
		GroupedRoutingEnabled: true,
		ParsedHealthPolicy:    policy,
		ParsedRouteGroups: []configstoreTables.RouteGroup{
			{
				Name: "profit-pool",
				Targets: []configstoreTables.RouteGroupTarget{
					{Provider: adaptiveStrPtr("openlux-aistudio-3"), Model: adaptiveStrPtr("gemini-3.1-pro-preview"), KeyID: adaptiveStrPtr("k1"), Weight: 0.5},
					{Provider: adaptiveStrPtr("bai-60pct"), Model: adaptiveStrPtr("gemini-3.1-pro"), KeyID: adaptiveStrPtr("k2"), Weight: 0.5},
				},
			},
		},
	}
	costs := map[schemas.ModelProvider]float64{
		"openlux-aistudio-3": 0.432,
		"bai-60pct":          0.600,
	}
	rctx := &RoutingContext{
		Model:       "gemini-auto",
		RequestType: string(schemas.ChatCompletionRequest),
		TargetCostResolver: func(p schemas.ModelProvider, _ string, _ schemas.RequestType) (float64, bool) {
			return costs[p], true
		},
	}

	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	dec := buildAdaptiveGroupedRoutingDecision(ctx, rule, rctx, ht, policy, tErr.Add(2*time.Second))
	require.NotNil(t, dec)
	assert.Equal(t, "openlux-aistudio-3", dec.Provider, "proven 95%+ channel must NOT be evicted on 2 fast 503s")

	// If upstream genuinely goes down (5 consecutive fast errors), short 60s tripwire activates,
	// then at +61s it immediately returns to #1 with its 90m reputation intact!
	for j := 0; j < 3; j++ {
		ht.RecordOutcome(ruleID, goldKey, OutcomeSoftFail, 300*time.Millisecond, "503 service overloaded", policy, tErr.Add(time.Duration(3+j)*time.Second))
	}
	decDuringOutage := buildAdaptiveGroupedRoutingDecision(ctx, rule, rctx, ht, policy, tErr.Add(10*time.Second))
	require.NotNil(t, decDuringOutage)
	assert.Equal(t, "bai-60pct", decDuringOutage.Provider, "5 consecutive failures must trigger 60s short breather")

	decAfter61s := buildAdaptiveGroupedRoutingDecision(ctx, rule, rctx, ht, policy, tErr.Add(70*time.Second))
	require.NotNil(t, decAfter61s)
	assert.Equal(t, "openlux-aistudio-3", decAfter61s.Provider, "after 60s breather, high 90m reputation channel must immediately reclaim #1")
}

func TestAdaptiveRouter_ProbationDemotionPeriodicRealProbeAndPromotion(t *testing.T) {
	ht := NewHealthTracker()
	policy := makeAdaptiveTestPolicy()
	policy.MinActiveTargets = 2 // allow 3rd channel to enter probation when 2 healthy remain
	now := time.Now()
	ruleID := "rule-adaptive-3"

	flakyCheapKey := TargetKey("polo-1x", "gemini-3.1-pro-preview", "k1")
	fastMainKey := TargetKey("openlux-3", "gemini-3.1-pro-preview", "k2")
	secondMainKey := TargetKey("bai-60pct", "gemini-3.1-pro", "k3")

	for i := 0; i < 10; i++ {
		ht.RecordOutcome(ruleID, fastMainKey, OutcomeSuccess, 11*time.Second, "", policy, now)
		ht.RecordOutcome(ruleID, secondMainKey, OutcomeSuccess, 12*time.Second, "", policy, now)
	}
	// Flaky 1x channel hits a 38s timeout -> drops below 70% into Observation Pool
	ht.RecordOutcome(ruleID, flakyCheapKey, OutcomeSoftFail, 38*time.Second, "request timed out after 38 seconds", policy, now)

	rule := &configstoreTables.TableRoutingRule{
		ID:                    ruleID,
		Name:                  "gemini-auto",
		Enabled:               true,
		GroupedRoutingEnabled: true,
		ParsedHealthPolicy:    policy,
		ParsedRouteGroups: []configstoreTables.RouteGroup{
			{
				Name: "profit-pool",
				Targets: []configstoreTables.RouteGroupTarget{
					{Provider: adaptiveStrPtr("polo-1x"), Model: adaptiveStrPtr("gemini-3.1-pro-preview"), KeyID: adaptiveStrPtr("k1"), Weight: 0.34},
					{Provider: adaptiveStrPtr("openlux-3"), Model: adaptiveStrPtr("gemini-3.1-pro-preview"), KeyID: adaptiveStrPtr("k2"), Weight: 0.33},
					{Provider: adaptiveStrPtr("bai-60pct"), Model: adaptiveStrPtr("gemini-3.1-pro"), KeyID: adaptiveStrPtr("k3"), Weight: 0.33},
				},
			},
		},
	}
	costs := map[schemas.ModelProvider]float64{
		"polo-1x":   0.147,
		"openlux-3": 0.432,
		"bai-60pct": 0.600,
	}
	rctx := &RoutingContext{
		Model:       "gemini-auto",
		RequestType: string(schemas.ChatCompletionRequest),
		TargetCostResolver: func(p schemas.ModelProvider, _ string, _ schemas.RequestType) (float64, bool) {
			return costs[p], true
		},
	}

	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)

	// 1 minute later: polo-1x is in probation (next probe at +5m), so normal traffic goes 100% to openlux-3
	dec1m := buildAdaptiveGroupedRoutingDecision(ctx, rule, rctx, ht, policy, now.Add(time.Minute))
	require.NotNil(t, dec1m)
	assert.Equal(t, "openlux-3", dec1m.Provider)
	assert.NotContains(t, dec1m.Fallbacks, "polo-1x/gemini-3.1-pro-preview")

	// 5m01s later: exactly 1 real request claims the probation probe with openlux-3 immediately at Hop 1,
	// while a concurrent request at 5m02s bypasses polo-1x!
	tProbe1 := now.Add(301 * time.Second)
	decProbe := buildAdaptiveGroupedRoutingDecision(ctx, rule, rctx, ht, policy, tProbe1)
	require.NotNil(t, decProbe)
	assert.Equal(t, "polo-1x", decProbe.Provider)
	assert.True(t, decProbe.AdaptivePolicy.PrimaryHint.IsProbationProbe)
	assert.Equal(t, 35000, decProbe.AdaptivePolicy.PrimaryHint.AttemptTimeoutMs)
	require.NotEmpty(t, decProbe.Fallbacks)
	assert.Equal(t, "openlux-3/gemini-3.1-pro-preview", decProbe.Fallbacks[0], "Hop 1 after probation probe MUST be cheapest proven fast rescue channel")

	decConcurrent := buildAdaptiveGroupedRoutingDecision(ctx, rule, rctx, ht, policy, tProbe1.Add(time.Second))
	require.NotNil(t, decConcurrent)
	assert.Equal(t, "openlux-3", decConcurrent.Provider, "concurrent traffic must not pile onto in-flight probation probe")

	// First real probe succeeds in 22s -> schedules quick 60s verification; second probe succeeds -> graduates back to #1!
	ht.RecordOutcome(ruleID, flakyCheapKey, OutcomeSuccess, 22*time.Second, "", policy, tProbe1.Add(22*time.Second))
	tProbe2 := tProbe1.Add(90 * time.Second)
	decProbe2 := buildAdaptiveGroupedRoutingDecision(ctx, rule, rctx, ht, policy, tProbe2)
	require.NotNil(t, decProbe2)
	assert.Equal(t, "polo-1x", decProbe2.Provider)
	ht.RecordOutcome(ruleID, flakyCheapKey, OutcomeSuccess, 20*time.Second, "", policy, tProbe2.Add(20*time.Second))

	// Now polo-1x is promoted back to Active Pool #1 (IsProbationProbe == false)!
	decPromoted := buildAdaptiveGroupedRoutingDecision(ctx, rule, rctx, ht, policy, tProbe2.Add(25*time.Second))
	require.NotNil(t, decPromoted)
	assert.Equal(t, "polo-1x", decPromoted.Provider)
	assert.False(t, decPromoted.AdaptivePolicy.PrimaryHint.IsProbationProbe)
}

func TestAdaptiveRouter_MinimumActiveTargetsNeverEmptiesPool(t *testing.T) {
	ht := NewHealthTracker()
	policy := makeAdaptiveTestPolicy()
	policy.MinActiveTargets = 3
	now := time.Now()
	ruleID := "rule-adaptive-min3"

	keys := []string{
		TargetKey("c1", "m", "k1"),
		TargetKey("c2", "m", "k2"),
		TargetKey("c3", "m", "k3"),
		TargetKey("c4", "m", "k4"),
	}
	// Simulate an upstream storm where ALL 4 discount channels drop below 70%
	for _, k := range keys {
		ht.RecordOutcome(ruleID, k, OutcomeSoftFail, 38*time.Second, "timeout", policy, now)
		ht.RecordOutcome(ruleID, k, OutcomeSoftFail, 38*time.Second, "timeout", policy, now.Add(time.Second))
	}

	rule := &configstoreTables.TableRoutingRule{
		ID:                    ruleID,
		Name:                  "gemini-auto",
		Enabled:               true,
		GroupedRoutingEnabled: true,
		ParsedHealthPolicy:    policy,
		ParsedRouteGroups: []configstoreTables.RouteGroup{
			{
				Name: "profit-pool",
				Targets: []configstoreTables.RouteGroupTarget{
					{Provider: adaptiveStrPtr("c1"), Model: adaptiveStrPtr("m"), KeyID: adaptiveStrPtr("k1"), Weight: 0.25},
					{Provider: adaptiveStrPtr("c2"), Model: adaptiveStrPtr("m"), KeyID: adaptiveStrPtr("k2"), Weight: 0.25},
					{Provider: adaptiveStrPtr("c3"), Model: adaptiveStrPtr("m"), KeyID: adaptiveStrPtr("k3"), Weight: 0.25},
					{Provider: adaptiveStrPtr("c4"), Model: adaptiveStrPtr("m"), KeyID: adaptiveStrPtr("k4"), Weight: 0.25},
				},
			},
			{
				Name:         "last-resort",
				FallbackOnly: true,
				LastResort:   true,
				Targets: []configstoreTables.RouteGroupTarget{
					{Provider: adaptiveStrPtr("full-100"), Model: adaptiveStrPtr("m"), KeyID: adaptiveStrPtr("k5"), Weight: 1.0},
				},
			},
		},
	}
	costs := map[schemas.ModelProvider]float64{"c1": 0.20, "c2": 0.30, "c3": 0.40, "c4": 0.50, "full-100": 1.00}
	rctx := &RoutingContext{
		Model: "gemini-auto",
		TargetCostResolver: func(p schemas.ModelProvider, _ string, _ schemas.RequestType) (float64, bool) {
			return costs[p], true
		},
	}

	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	dec := buildAdaptiveGroupedRoutingDecision(ctx, rule, rctx, ht, policy, now.Add(5*time.Second))
	require.NotNil(t, dec)
	assert.Equal(t, "c1", dec.Provider, "even if all channels dip below 70%, top 3 discount channels must stay active ahead of 100% last resort")
	require.GreaterOrEqual(t, len(dec.Fallbacks), 3)
	assert.Equal(t, "c2/m", dec.Fallbacks[0])
	assert.Equal(t, "c3/m", dec.Fallbacks[1])
}

func TestAdaptiveRouter_DisablesShortPingActiveProbes(t *testing.T) {
	ht := NewHealthTracker()
	policy := makeAdaptiveTestPolicy()
	now := time.Now()
	rule := &configstoreTables.TableRoutingRule{
		ID:                    "rule-no-ping",
		Name:                  "gemini-auto",
		Enabled:               true,
		GroupedRoutingEnabled: true,
		ParsedHealthPolicy:    policy,
		ParsedRouteGroups: []configstoreTables.RouteGroup{
			{
				Name: "profit-pool",
				Targets: []configstoreTables.RouteGroupTarget{
					{Provider: adaptiveStrPtr("openlux-aistudio-3"), Model: adaptiveStrPtr("gemini-3.1-pro-preview"), KeyID: adaptiveStrPtr("k1"), Weight: 1.0},
				},
			},
		},
	}
	enabled := map[string]bool{
		TargetKey("openlux-aistudio-3", "gemini-3.1-pro-preview", "k1"): true,
	}
	plans := buildActiveProbePlans([]*configstoreTables.TableRoutingRule{rule}, ht, enabled, now, 5*time.Minute)
	assert.Empty(t, plans, "adaptive rules must NEVER emit short 1-token ping probes")
}

func TestOutcomeClassifier_451ProtocolCrashIsHardFail(t *testing.T) {
	code := 451
	err := &schemas.BifrostError{
		StatusCode: &code,
		Error: &schemas.ErrorField{
			Message: "Protocol generation failed",
		},
	}
	assert.Equal(t, OutcomeHardFail, ClassifyOutcome(err, 2*time.Second, 38*time.Second))
}

func TestAdaptiveRouter_Flaky72PctCheapNeverUsedAsFastRescue(t *testing.T) {
	ht := NewHealthTracker()
	policy := makeAdaptiveTestPolicy()
	now := time.Now()
	ruleID := "rule-fast-rescue-guard"

	flakyCheap := TargetKey("storeapi-2pct", "gemini-3.1-pro-preview", "k1")
	rockSolid43 := TargetKey("openlux-43pct", "gemini-3.1-pro-preview", "k2")

	// storeapi-2pct has 8 fast 10s successes + 2 timeouts -> ~71% success rate (>70%, stays in active pool as primary #1,
	// but MUST NOT be marked IsFastRescue!)
	for i := 0; i < 8; i++ {
		ht.RecordOutcome(ruleID, flakyCheap, OutcomeSuccess, 10*time.Second, "", policy, now.Add(time.Duration(i)*time.Second))
	}
	ht.RecordOutcome(ruleID, flakyCheap, OutcomeSoftFail, 35*time.Second, "request timed out", policy, now.Add(10*time.Second))
	ht.RecordOutcome(ruleID, flakyCheap, OutcomeSoftFail, 35*time.Second, "request timed out", policy, now.Add(11*time.Second))

	for i := 0; i < 15; i++ {
		ht.RecordOutcome(ruleID, rockSolid43, OutcomeSuccess, 11*time.Second, "", policy, now.Add(time.Duration(i)*time.Second))
	}

	rule := &configstoreTables.TableRoutingRule{
		ID:                    ruleID,
		Name:                  "gemini-auto",
		Enabled:               true,
		GroupedRoutingEnabled: true,
		ParsedHealthPolicy:    policy,
		ParsedRouteGroups: []configstoreTables.RouteGroup{
			{
				Name: "profit-pool",
				Targets: []configstoreTables.RouteGroupTarget{
					{Provider: adaptiveStrPtr("storeapi-2pct"), Model: adaptiveStrPtr("gemini-3.1-pro-preview"), KeyID: adaptiveStrPtr("k1"), Weight: 0.5},
					{Provider: adaptiveStrPtr("openlux-43pct"), Model: adaptiveStrPtr("gemini-3.1-pro-preview"), KeyID: adaptiveStrPtr("k2"), Weight: 0.5},
				},
			},
		},
	}
	costs := map[schemas.ModelProvider]float64{"storeapi-2pct": 0.022, "openlux-43pct": 0.432}
	rctx := &RoutingContext{
		Model: "gemini-auto",
		TargetCostResolver: func(p schemas.ModelProvider, _ string, _ schemas.RequestType) (float64, bool) {
			return costs[p], true
		},
	}
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	// Evaluate after the 60s tripwire expires: storeapi-2pct (~71% eff rate) returns to primary #1,
	// while still disqualified from IsFastRescue (<88%).
	dec := buildAdaptiveGroupedRoutingDecision(ctx, rule, rctx, ht, policy, now.Add(90*time.Second))
	require.NotNil(t, dec)
	assert.Equal(t, "storeapi-2pct", dec.Provider)
	assert.False(t, dec.AdaptivePolicy.PrimaryHint.IsFastRescue, "71%-reliable cheap channel must NEVER be marked IsFastRescue")
	assert.True(t, dec.AdaptivePolicy.FallbackHints[0].IsFastRescue, "95%+-reliable 11s channel must be IsFastRescue")
}

func TestAdaptiveProbationProbeTimeoutMs_ScalesWithRuleBudget(t *testing.T) {
	// Short-budget V2.2 rule (e.g. gemini-auto: 24s primary / 22s rescue) stays tight at 21s.
	shortV22 := &configstoreTables.HealthPolicy{
		AdaptiveV22Enabled:      true,
		PrimaryAttemptTimeoutMs: 24000,
		RescueAttemptTimeoutMs:  22000,
	}
	assert.Equal(t, 21000, adaptiveProbationProbeTimeoutMs(shortV22, 0))
	assert.Equal(t, 20000, adaptiveProbationProbeTimeoutMs(shortV22, 20000))

	// Long-budget streaming V2.2 rule (e.g. gemini-auto-bif: 65s primary / 55s rescue)
	// must NOT be artificially clamped to 22s.
	longV22 := &configstoreTables.HealthPolicy{
		AdaptiveV22Enabled:      true,
		PrimaryAttemptTimeoutMs: 65000,
		RescueAttemptTimeoutMs:  55000,
	}
	assert.Equal(t, 55000, adaptiveProbationProbeTimeoutMs(longV22, 0))
	assert.Equal(t, 45000, adaptiveProbationProbeTimeoutMs(longV22, 45000))

	// Long-budget V2.2 rule without smaller rescue falls back to primary - 3s.
	longNoRescue := &configstoreTables.HealthPolicy{
		AdaptiveV22Enabled:      true,
		PrimaryAttemptTimeoutMs: 65000,
	}
	assert.Equal(t, 62000, adaptiveProbationProbeTimeoutMs(longNoRescue, 0))

	// Legacy non-V2.2 keeps 35s cap.
	legacy := &configstoreTables.HealthPolicy{
		AdaptiveV22Enabled:      false,
		PrimaryAttemptTimeoutMs: 65000,
	}
	assert.Equal(t, 35000, adaptiveProbationProbeTimeoutMs(legacy, 0))
}
