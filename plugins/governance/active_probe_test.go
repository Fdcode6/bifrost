package governance

import (
	"sort"
	"testing"
	"time"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildActiveProbePlans_IncludesPendingFirstProbeWithoutRealAccess(t *testing.T) {
	ht := NewHealthTracker()
	now := time.Now()
	targetKey := TargetKey("openai", "gpt-4.1", "relay-a")
	ht.SetPendingFirstProbe(targetKey, true)

	plans := buildActiveProbePlans([]*configstoreTables.TableRoutingRule{
		testGroupedProbeRule("rule-a", "openai", "gpt-4.1", "relay-a"),
	}, ht, map[string]bool{targetKey: true}, now, 10*time.Minute)

	require.Len(t, plans, 1)
	assert.Equal(t, schemas.ChatCompletionRequest, plans[0].RequestType)
}

func TestDefaultActiveHealthProbeConfig_IsDisabledWithRecommendedCadence(t *testing.T) {
	cfg := defaultActiveHealthProbeConfig(nil)

	assert.False(t, cfg.Enabled)
	assert.Equal(t, 60*time.Second, cfg.Interval)
	assert.Equal(t, 5*time.Minute, cfg.IdlePause)
	assert.Equal(t, 5*time.Second, cfg.Timeout)
}

func TestBuildActiveProbePlans_DeduplicatesTargetsAcrossRules(t *testing.T) {
	ht := NewHealthTracker()
	now := time.Now()
	targetKey := TargetKey("openai", "gpt-4.1", "relay-a")

	plans := buildActiveProbePlans([]*configstoreTables.TableRoutingRule{
		testGroupedProbeRule("rule-a", "openai", "gpt-4.1", "relay-a"),
		testGroupedProbeRule("rule-b", "openai", "gpt-4.1", "relay-a"),
	}, ht, map[string]bool{targetKey: true}, now, 10*time.Minute)

	require.Len(t, plans, 1)
	assert.Equal(t, targetKey, plans[0].TargetKey)
	assert.Equal(t, schemas.OpenAI, plans[0].Provider)
	assert.Equal(t, "gpt-4.1", plans[0].Model)
	assert.Equal(t, "relay-a", plans[0].KeyID)
	assert.Equal(t, schemas.ChatCompletionRequest, plans[0].RequestType)

	ruleIDs := append([]string(nil), plans[0].RuleIDs...)
	sort.Strings(ruleIDs)
	assert.Equal(t, []string{"rule-a", "rule-b"}, ruleIDs)
}

func TestBuildActiveProbePlans_DeduplicatesRuleIDsWithinSameTarget(t *testing.T) {
	ht := NewHealthTracker()
	now := time.Now()
	targetKey := TargetKey("openai", "gpt-4.1", "relay-a")

	rule := testGroupedProbeRule("rule-a", "openai", "gpt-4.1", "relay-a")
	rule.ParsedRouteGroups = append(rule.ParsedRouteGroups, configstoreTables.RouteGroup{
		Name:       "secondary",
		RetryLimit: 0,
		Targets: []configstoreTables.RouteGroupTarget{
			{
				Provider: bifrost.Ptr("openai"),
				Model:    bifrost.Ptr("gpt-4.1"),
				KeyID:    bifrost.Ptr("relay-a"),
				Weight:   1,
			},
		},
	})

	plans := buildActiveProbePlans([]*configstoreTables.TableRoutingRule{rule}, ht, map[string]bool{targetKey: true}, now, 10*time.Minute)

	require.Len(t, plans, 1)
	assert.Equal(t, []string{"rule-a"}, plans[0].RuleIDs)
}

func TestBuildActiveProbePlans_SkipsTargetsPastIdlePause(t *testing.T) {
	ht := NewHealthTracker()
	now := time.Now()
	targetKey := TargetKey("openai", "gpt-4.1", "relay-a")
	ht.RecordRealAccess(targetKey, schemas.ChatCompletionRequest, now.Add(-6*time.Minute))

	plans := buildActiveProbePlans([]*configstoreTables.TableRoutingRule{
		testGroupedProbeRule("rule-a", "openai", "gpt-4.1", "relay-a"),
	}, ht, map[string]bool{targetKey: true}, now, 5*time.Minute)

	require.Len(t, plans, 0)
}

func TestBuildActiveProbePlans_UsesBootstrapChatRequestTypeForFirstProbe(t *testing.T) {
	ht := NewHealthTracker()
	now := time.Now()
	targetKey := TargetKey("openai", "gpt-4.1", "relay-a")

	plans := buildActiveProbePlans([]*configstoreTables.TableRoutingRule{
		testGroupedProbeRule("rule-a", "openai", "gpt-4.1", "relay-a"),
	}, ht, map[string]bool{targetKey: true}, now, 5*time.Minute)

	require.Len(t, plans, 1)
	assert.Equal(t, schemas.ChatCompletionRequest, plans[0].RequestType)
}

func TestBuildActiveProbePlans_UsesChatProbeForStreamOnlyHistory(t *testing.T) {
	ht := NewHealthTracker()
	now := time.Now()
	targetKey := TargetKey("openai", "gpt-4.1", "relay-a")
	ht.RecordRealAccess(targetKey, schemas.ChatCompletionStreamRequest, now)

	plans := buildActiveProbePlans([]*configstoreTables.TableRoutingRule{
		testGroupedProbeRule("rule-a", "openai", "gpt-4.1", "relay-a"),
	}, ht, map[string]bool{targetKey: true}, now, 5*time.Minute)

	require.Len(t, plans, 1)
	assert.Equal(t, schemas.ChatCompletionRequest, plans[0].RequestType)
}

func TestBuildActiveProbePlans_SkipsFallbackOnlyGroups(t *testing.T) {
	ht := NewHealthTracker()
	now := time.Now()
	targetKey := TargetKey("openrouter", "gemma", "fallback-key")

	rule := testGroupedProbeRule("rule-fallback", "openrouter", "gemma", "fallback-key")
	rule.ParsedRouteGroups[0].FallbackOnly = true

	plans := buildActiveProbePlans([]*configstoreTables.TableRoutingRule{rule}, ht, map[string]bool{targetKey: true}, now, 5*time.Minute)

	require.Empty(t, plans)
}

func TestBuildActiveProbePlans_PausesAfterInitialProbeUntilRealTraffic(t *testing.T) {
	ht := NewHealthTracker()
	now := time.Now()
	targetKey := TargetKey("openai", "gpt-4.1", "relay-a")
	ht.RecordProbeResult(targetKey, schemas.ChatCompletionRequest, true, "", now.Add(-time.Minute))

	plans := buildActiveProbePlans([]*configstoreTables.TableRoutingRule{
		testGroupedProbeRule("rule-a", "openai", "gpt-4.1", "relay-a"),
	}, ht, map[string]bool{targetKey: true}, now, 5*time.Minute)

	require.Len(t, plans, 0)
}

func TestBuildActiveProbePlans_SkipsTargetsWithoutSupportedProbeShape(t *testing.T) {
	ht := NewHealthTracker()
	now := time.Now()

	unsupportedKey := TargetKey("openai", "gpt-4.1", "relay-b")
	ht.RecordRealAccess(unsupportedKey, schemas.EmbeddingRequest, now)

	plans := buildActiveProbePlans([]*configstoreTables.TableRoutingRule{
		testGroupedProbeRule("rule-no-key", "openai", "gpt-4.1", ""),
		testGroupedProbeRule("rule-unsupported", "openai", "gpt-4.1", "relay-b"),
	}, ht, map[string]bool{unsupportedKey: true}, now, 10*time.Minute)

	require.Len(t, plans, 0)
}

func TestApplyActiveProbeResult_RecordsProbeFailureWithoutMutatingRuleHealth(t *testing.T) {
	ht := NewHealthTracker()
	now := time.Now()
	targetKey := TargetKey("openai", "gpt-4.1", "relay-a")
	policy := &configstoreTables.HealthPolicy{
		FailureThreshold:     1,
		FailureWindowSeconds: 30,
		CooldownSeconds:      30,
	}
	ht.SetPendingFirstProbe(targetKey, true)

	applyActiveProbeResult(ht, activeProbePlan{
		TargetKey:   targetKey,
		RequestType: schemas.ChatCompletionRequest,
		RuleIDs:     []string{"rule-a", "rule-b"},
	}, activeProbeResult{
		Success:    false,
		FailureMsg: "timeout",
	}, now)

	snapA := ht.GetTargetStatusForRule("rule-a", targetKey, policy, now)
	snapB := ht.GetTargetStatusForRule("rule-b", targetKey, policy, now)
	require.Equal(t, "available", snapA.Status)
	require.Equal(t, "available", snapB.Status)
	assert.Equal(t, 0, snapA.FailureCount)
	assert.Equal(t, 0, snapB.FailureCount)
	assert.Empty(t, snapA.LastFailureMsg)
	assert.Empty(t, snapB.LastFailureMsg)
	assert.Equal(t, string(HealthObservationSourceActive), snapA.LastObservationSource)
	assert.Equal(t, string(HealthObservationSourceActive), snapB.LastObservationSource)
	activity := ht.GetTargetActivity(targetKey)
	assert.False(t, activity.PendingFirstProbe)
	assert.Equal(t, "failure", activity.LastProbeResult)
	assert.Equal(t, "timeout", activity.LastProbeError)
	assert.True(t, activity.LastRealAccessAt.IsZero())
}

func TestApplyActiveProbeResult_RecordsProbeSuccessWithoutClearingRuleCooldown(t *testing.T) {
	ht := NewHealthTracker()
	now := time.Now()
	targetKey := TargetKey("openai", "gpt-4.1", "relay-a")
	policy := &configstoreTables.HealthPolicy{
		FailureThreshold:     1,
		FailureWindowSeconds: 30,
		CooldownSeconds:      30,
	}

	ht.RecordFailureForRule("rule-a", targetKey, "timeout", now)
	ht.RecordFailureForRule("rule-b", targetKey, "timeout", now)
	ht.SetPendingFirstProbe(targetKey, true)
	require.True(t, ht.IsInCooldownForRule("rule-a", targetKey, policy, now))
	require.True(t, ht.IsInCooldownForRule("rule-b", targetKey, policy, now))

	applyActiveProbeResult(ht, activeProbePlan{
		TargetKey:   targetKey,
		RequestType: schemas.ChatCompletionRequest,
		RuleIDs:     []string{"rule-a", "rule-b"},
	}, activeProbeResult{
		Success: true,
	}, now.Add(5*time.Second))

	snapA := ht.GetTargetStatusForRule("rule-a", targetKey, policy, now.Add(5*time.Second))
	snapB := ht.GetTargetStatusForRule("rule-b", targetKey, policy, now.Add(5*time.Second))
	assert.Equal(t, "cooldown", snapA.Status)
	assert.Equal(t, "cooldown", snapB.Status)
	assert.Equal(t, 1, snapA.FailureCount)
	assert.Equal(t, 1, snapB.FailureCount)
	assert.Equal(t, string(HealthObservationSourceActive), snapA.LastObservationSource)
	assert.Equal(t, string(HealthObservationSourceActive), snapB.LastObservationSource)
	activity := ht.GetTargetActivity(targetKey)
	assert.False(t, activity.PendingFirstProbe)
	assert.Equal(t, "success", activity.LastProbeResult)
	assert.True(t, activity.LastRealAccessAt.IsZero())
}

func TestApplyActiveProbeResult_OptInActiveProbeWakeupUnlocksInPlaceHalfOpen(t *testing.T) {
	ht := NewHealthTracker()
	now := time.Now()
	targetKey := TargetKey("yunwu", "gemini-3.1-pro-preview", "relay-a")
	wakeup := true
	policy := ApplyHealthPolicyDefaults(&configstoreTables.HealthPolicy{
		FailureThreshold:     1,
		FailureWindowSeconds: 30,
		CooldownSeconds:      120,
		ActiveProbeWakeup:    &wakeup,
	})

	ht.RecordFailureForRule("rule-a", targetKey, "timeout", now)
	require.True(t, ht.IsInCooldownForRule("rule-a", targetKey, policy, now.Add(5*time.Second)))

	applyActiveProbeResult(ht, activeProbePlan{
		TargetKey:     targetKey,
		RequestType:   schemas.ChatCompletionRequest,
		RuleIDs:       []string{"rule-a"},
		WakeupRuleIDs: []string{"rule-a"},
	}, activeProbeResult{
		Success: true,
	}, now.Add(10*time.Second))

	// Active probe wakeup does not blindly wipe cooldown, but immediately unlocks one in-place half-open probe.
	state := ht.InspectTargetRoutingStateForRule("rule-a", targetKey, policy, now.Add(11*time.Second))
	assert.Equal(t, HealthCooldown, state.Level)
	assert.True(t, state.HalfOpenEligible)
	assert.True(t, ht.TryClaimHalfOpenProbeForRule("rule-a", targetKey, policy, now.Add(11*time.Second)))
}

func TestBuildActiveProbePlans_KeepsProbingCooledTargetWhileRuleHasRecentTraffic(t *testing.T) {
	ht := NewHealthTracker()
	now := time.Now()
	wakeup := true
	policy := ApplyHealthPolicyDefaults(&configstoreTables.HealthPolicy{
		FailureThreshold:         2,
		FailureWindowSeconds:     60,
		CooldownSeconds:          300,
		FatalConsecutiveFailures: 2,
		FatalCooldownSeconds:     300,
		ActiveProbeWakeup:        &wakeup,
	})

	cheapKey := TargetKey("yunwu", "gemini-3.1-pro-preview", "k-cheap")
	backupKey := TargetKey("B.AI", "gemini-3.1-pro", "k-backup")
	idleBackupKey := TargetKey("poloapi_vip", "gemini-3.1-pro-preview", "k-vip")

	rule := &configstoreTables.TableRoutingRule{
		ID:                    "gemini-auto",
		Name:                  "gemini-auto",
		Enabled:               true,
		GroupedRoutingEnabled: true,
		ParsedHealthPolicy:    policy,
		ParsedRouteGroups: []configstoreTables.RouteGroup{
			{Name: "cheap", Targets: []configstoreTables.RouteGroupTarget{{Provider: bifrost.Ptr("yunwu"), Model: bifrost.Ptr("gemini-3.1-pro-preview"), KeyID: bifrost.Ptr("k-cheap"), Weight: 1}}},
			{Name: "backup", Targets: []configstoreTables.RouteGroupTarget{{Provider: bifrost.Ptr("B.AI"), Model: bifrost.Ptr("gemini-3.1-pro"), KeyID: bifrost.Ptr("k-backup"), Weight: 1}}},
			{Name: "vip", Targets: []configstoreTables.RouteGroupTarget{{Provider: bifrost.Ptr("poloapi_vip"), Model: bifrost.Ptr("gemini-3.1-pro-preview"), KeyID: bifrost.Ptr("k-vip"), Weight: 1}}},
		},
	}

	// cheap entered 300s fatal cooldown 3 minutes ago (past idlePause=2m!), while backup served traffic 10s ago.
	// idleBackup is healthy and hasn't had traffic in 10 minutes (should stay idle-paused to save tokens).
	ht.RecordRealAccess(cheapKey, schemas.ChatCompletionRequest, now.Add(-3*time.Minute))
	ht.RecordOutcome(rule.ID, cheapKey, OutcomeFatalChannel, 0, "insufficient balance", policy, now.Add(-3*time.Minute))
	ht.RecordOutcome(rule.ID, cheapKey, OutcomeFatalChannel, 0, "insufficient balance", policy, now.Add(-3*time.Minute))
	ht.RecordRealAccess(backupKey, schemas.ChatCompletionRequest, now.Add(-10*time.Second))
	ht.RecordRealAccess(idleBackupKey, schemas.ChatCompletionRequest, now.Add(-10*time.Minute))

	enabled := map[string]bool{
		cheapKey:      true,
		backupKey:     true,
		idleBackupKey: true,
	}

	plans := buildActiveProbePlans([]*configstoreTables.TableRoutingRule{rule}, ht, enabled, now, 2*time.Minute)
	planByKey := make(map[string]activeProbePlan, len(plans))
	for _, p := range plans {
		planByKey[p.TargetKey] = p
	}

	cheapPlan, hasCheap := planByKey[cheapKey]
	require.True(t, hasCheap, "cooled cheap primary must keep being probed while rule is actively serving traffic on backup")
	assert.Equal(t, []string{"gemini-auto"}, cheapPlan.WakeupRuleIDs)
	_, hasBackup := planByKey[backupKey]
	assert.True(t, hasBackup, "active backup should remain eligible for probing")
	_, hasIdleVIP := planByKey[idleBackupKey]
	assert.False(t, hasIdleVIP, "healthy idle backup past idlePause must stay paused")

	// When the ENTIRE rule goes idle (overnight: backup also past idlePause=2m), cooled cheap target also pauses!
	overnight := now.Add(4 * time.Minute)
	plansOvernight := buildActiveProbePlans([]*configstoreTables.TableRoutingRule{rule}, ht, enabled, overnight, 2*time.Minute)
	assert.Empty(t, plansOvernight, "when entire rule is idle overnight, cooled target should also pause probing")
}

func TestApplyActiveProbeResult_DoesNotInventRealAccess(t *testing.T) {
	ht := NewHealthTracker()
	now := time.Now()
	targetKey := TargetKey("openai", "gpt-4.1", "relay-a")

	applyActiveProbeResult(ht, activeProbePlan{
		TargetKey:   targetKey,
		RequestType: schemas.ChatCompletionRequest,
		RuleIDs:     []string{"rule-a"},
	}, activeProbeResult{
		Success: true,
	}, now)

	activity := ht.GetTargetActivity(targetKey)
	assert.True(t, activity.LastRealAccessAt.IsZero())
	assert.Equal(t, "success", activity.LastProbeResult)
}

func testGroupedProbeRule(ruleID, provider, model, keyID string) *configstoreTables.TableRoutingRule {
	target := configstoreTables.RouteGroupTarget{
		Provider: bifrost.Ptr(provider),
		Model:    bifrost.Ptr(model),
		Weight:   1,
	}
	if keyID != "" {
		target.KeyID = bifrost.Ptr(keyID)
	}

	return &configstoreTables.TableRoutingRule{
		ID:                    ruleID,
		Name:                  ruleID,
		Enabled:               true,
		GroupedRoutingEnabled: true,
		ParsedRouteGroups: []configstoreTables.RouteGroup{
			{
				Name:       "primary",
				RetryLimit: 0,
				Targets:    []configstoreTables.RouteGroupTarget{target},
			},
		},
	}
}
