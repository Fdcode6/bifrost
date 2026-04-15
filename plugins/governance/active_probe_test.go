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

func TestApplyActiveProbeResult_FansOutFailureAndObservation(t *testing.T) {
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
	require.Equal(t, "cooldown", snapA.Status)
	require.Equal(t, "cooldown", snapB.Status)
	assert.Equal(t, "timeout", snapA.LastFailureMsg)
	assert.Equal(t, "timeout", snapB.LastFailureMsg)
	assert.Equal(t, string(HealthObservationSourceActive), snapA.LastObservationSource)
	assert.Equal(t, string(HealthObservationSourceActive), snapB.LastObservationSource)
	activity := ht.GetTargetActivity(targetKey)
	assert.False(t, activity.PendingFirstProbe)
	assert.Equal(t, "failure", activity.LastProbeResult)
	assert.Equal(t, "timeout", activity.LastProbeError)
	assert.True(t, activity.LastRealAccessAt.IsZero())
}

func TestApplyActiveProbeResult_ClearsPendingFirstProbe(t *testing.T) {
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
	assert.Equal(t, "available", snapA.Status)
	assert.Equal(t, "available", snapB.Status)
	assert.Equal(t, 0, snapA.FailureCount)
	assert.Equal(t, 0, snapB.FailureCount)
	assert.Equal(t, string(HealthObservationSourceActive), snapA.LastObservationSource)
	assert.Equal(t, string(HealthObservationSourceActive), snapB.LastObservationSource)
	activity := ht.GetTargetActivity(targetKey)
	assert.False(t, activity.PendingFirstProbe)
	assert.Equal(t, "success", activity.LastProbeResult)
	assert.True(t, activity.LastRealAccessAt.IsZero())
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
