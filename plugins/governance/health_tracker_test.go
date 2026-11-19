package governance

import (
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testPolicy() *configstoreTables.HealthPolicy {
	return &configstoreTables.HealthPolicy{
		FailureThreshold:     2,
		FailureWindowSeconds: 30,
		CooldownSeconds:      30,
	}
}

func TestHealthTracker_NoCooldownBelowThreshold(t *testing.T) {
	ht := NewHealthTracker()
	policy := testPolicy()
	now := time.Now()

	ht.RecordFailure("openai:gpt-4.1", "502", now)
	assert.False(t, ht.IsInCooldown("openai:gpt-4.1", policy, now), "should not be in cooldown with 1 failure below threshold of 2")
}

func TestHealthTracker_CooldownTriggeredAtThreshold(t *testing.T) {
	ht := NewHealthTracker()
	policy := testPolicy()
	now := time.Now()

	ht.RecordFailure("openai:gpt-4.1", "502", now)
	ht.RecordFailure("openai:gpt-4.1", "503", now.Add(5*time.Second))

	assert.True(t, ht.IsInCooldown("openai:gpt-4.1", policy, now.Add(6*time.Second)), "should trigger cooldown at threshold")
}

func TestHealthTracker_CooldownAutoRecovers(t *testing.T) {
	ht := NewHealthTracker()
	policy := testPolicy()
	now := time.Now()

	ht.RecordFailure("openai:gpt-4.1", "502", now)
	ht.RecordFailure("openai:gpt-4.1", "502", now.Add(1*time.Second))

	// Still in cooldown at now+10s
	assert.True(t, ht.IsInCooldown("openai:gpt-4.1", policy, now.Add(10*time.Second)))

	// Cooldown expired: IsInCooldown at threshold triggers cooldown at that moment.
	// The cooldown lasts 30s from the evaluation time. After that, it auto-recovers.
	assert.False(t, ht.IsInCooldown("openai:gpt-4.1", policy, now.Add(42*time.Second)), "should auto-recover after cooldown")
}

func TestHealthTracker_WindowPruning(t *testing.T) {
	ht := NewHealthTracker()
	policy := testPolicy()
	now := time.Now()

	// First failure at now
	ht.RecordFailure("openai:gpt-4.1", "502", now)
	// Success in between resets consecutive counter
	ht.RecordSuccess("openai:gpt-4.1")
	// Second failure at now+35s — outside 30s window from first
	ht.RecordFailure("openai:gpt-4.1", "502", now.Add(35*time.Second))

	// Window pruning: only the second failure is inside window (consecutive=1 after reset)
	assert.False(t, ht.IsInCooldown("openai:gpt-4.1", policy, now.Add(35*time.Second)),
		"first failure should have been pruned outside window, consecutive reset by success")
}

func TestHealthTracker_SeparateTargetsIndependent(t *testing.T) {
	ht := NewHealthTracker()
	policy := testPolicy()
	now := time.Now()

	ht.RecordFailure("openai:gpt-4.1", "502", now)
	ht.RecordFailure("openai:gpt-4.1", "502", now.Add(1*time.Second))

	assert.True(t, ht.IsInCooldown("openai:gpt-4.1", policy, now.Add(2*time.Second)))
	assert.False(t, ht.IsInCooldown("anthropic:claude-sonnet-4", policy, now.Add(2*time.Second)), "different target should not be affected")
}

func TestHealthTracker_GetTargetStatus(t *testing.T) {
	ht := NewHealthTracker()
	policy := testPolicy()
	now := time.Now()

	// Unknown target should be available
	snap := ht.GetTargetStatus("unknown:model", policy, now)
	assert.Equal(t, "available", snap.Status)
	assert.Equal(t, 0, snap.FailureCount)

	// Record failures
	ht.RecordFailure("openai:gpt-4.1", "502 Bad Gateway", now)
	ht.RecordFailure("openai:gpt-4.1", "503 Service Unavailable", now.Add(1*time.Second))

	// First call to IsInCooldown triggers cooldown (2 failures >= threshold 2)
	assert.True(t, ht.IsInCooldown("openai:gpt-4.1", policy, now.Add(2*time.Second)))

	snap = ht.GetTargetStatus("openai:gpt-4.1", policy, now.Add(2*time.Second))
	assert.Equal(t, "cooldown", snap.Status)
	assert.Equal(t, 2, snap.FailureCount)
	assert.NotNil(t, snap.CooldownUntil)
	assert.NotNil(t, snap.LastFailureTime)
	assert.Equal(t, "503 Service Unavailable", snap.LastFailureMsg)
}

func TestHealthTracker_KeyWithKeyID(t *testing.T) {
	ht := NewHealthTracker()
	policy := testPolicy()
	now := time.Now()

	key1 := TargetKey("openai", "gpt-4.1", "key-us-east")
	key2 := TargetKey("openai", "gpt-4.1", "key-eu-west")

	ht.RecordFailure(key1, "502", now)
	ht.RecordFailure(key1, "502", now.Add(1*time.Second))

	assert.True(t, ht.IsInCooldown(key1, policy, now.Add(2*time.Second)))
	assert.False(t, ht.IsInCooldown(key2, policy, now.Add(2*time.Second)), "different key_id should not be affected")
}

func TestHealthTracker_ConsecutiveFailures_LowFrequency(t *testing.T) {
	ht := NewHealthTracker()
	// Window=30s, but consecutive_failures=2 catches slow-drip failures
	policy := &configstoreTables.HealthPolicy{
		FailureThreshold:     2,
		FailureWindowSeconds: 30,
		CooldownSeconds:      30,
		ConsecutiveFailures:  2,
	}
	now := time.Now()

	// First failure at t=0
	ht.RecordFailure("openai:gpt-4.1", "timeout", now)
	assert.False(t, ht.IsInCooldown("openai:gpt-4.1", policy, now))

	// Second failure at t=60s — outside window, but consecutive count = 2
	ht.RecordFailure("openai:gpt-4.1", "timeout", now.Add(60*time.Second))
	assert.True(t, ht.IsInCooldown("openai:gpt-4.1", policy, now.Add(60*time.Second)),
		"should trigger cooldown via consecutive failures even though window failures < threshold")
}

func TestHealthTracker_RecordSuccess_ResetsConsecutive(t *testing.T) {
	ht := NewHealthTracker()
	policy := &configstoreTables.HealthPolicy{
		FailureThreshold:     3,
		FailureWindowSeconds: 30,
		CooldownSeconds:      30,
		ConsecutiveFailures:  3,
	}
	now := time.Now()

	// 2 consecutive failures
	ht.RecordFailure("openai:gpt-4.1", "502", now)
	ht.RecordFailure("openai:gpt-4.1", "502", now.Add(60*time.Second))

	// Success resets consecutive counter
	ht.RecordSuccess("openai:gpt-4.1")

	// Third failure — consecutive count is now 1 (not 3)
	ht.RecordFailure("openai:gpt-4.1", "502", now.Add(120*time.Second))
	assert.False(t, ht.IsInCooldown("openai:gpt-4.1", policy, now.Add(120*time.Second)),
		"success should have reset consecutive counter, so cooldown should not trigger")
}

func TestHealthTracker_RecordSuccess_ClearsCooldownAndWindowFailures(t *testing.T) {
	ht := NewHealthTracker()
	policy := testPolicy()
	now := time.Now()
	key := "openai:gpt-4.1"

	ht.RecordFailure(key, "502", now)
	ht.RecordFailure(key, "503", now.Add(time.Second))
	assert.True(t, ht.IsInCooldown(key, policy, now.Add(2*time.Second)))

	ht.RecordSuccess(key)

	snap := ht.GetTargetStatus(key, policy, now.Add(2*time.Second))
	assert.Equal(t, "available", snap.Status)
	assert.Equal(t, 0, snap.FailureCount)
	assert.Equal(t, 0, snap.ConsecutiveFailures)
	assert.Nil(t, snap.CooldownUntil)
	assert.Nil(t, snap.LastFailureTime)
	assert.Empty(t, snap.LastFailureMsg)
}

func TestHealthTracker_RecordObservation_IncludedInSnapshot(t *testing.T) {
	ht := NewHealthTracker()
	now := time.Now()
	key := TargetKey("openai", "gpt-4.1", "relay-a")

	ht.RecordObservation(key, schemas.ChatCompletionRequest, HealthObservationSourcePassive, now)

	snap := ht.GetTargetStatus(key, testPolicy(), now)
	require.NotNil(t, snap.LastObservedAt)
	assert.Equal(t, now.UTC().Format(time.RFC3339), *snap.LastObservedAt)
	assert.Equal(t, string(HealthObservationSourcePassive), snap.LastObservationSource)
	assert.Equal(t, string(schemas.ChatCompletionRequest), snap.LastObservedRequestType)
}

func TestHealthTracker_GetTargetStatusForRule_UsesGlobalObservationMetadata(t *testing.T) {
	ht := NewHealthTracker()
	now := time.Now()
	key := TargetKey("openai", "gpt-4.1", "relay-a")

	ht.RecordObservation(key, schemas.ResponsesRequest, HealthObservationSourceActive, now)

	snap := ht.GetTargetStatusForRule("rule-a", key, testPolicy(), now)
	require.NotNil(t, snap.LastObservedAt)
	assert.Equal(t, now.UTC().Format(time.RFC3339), *snap.LastObservedAt)
	assert.Equal(t, string(HealthObservationSourceActive), snap.LastObservationSource)
	assert.Equal(t, string(schemas.ResponsesRequest), snap.LastObservedRequestType)
}

func TestHealthTracker_RecordRealAccess_DoesNotOverwriteLastProbe(t *testing.T) {
	ht := NewHealthTracker()
	key := TargetKey("openai", "gpt-4.1", "relay-a")
	probeAt := time.Now().Add(-time.Minute)
	realAt := time.Now()

	ht.RecordProbeResult(key, schemas.ResponsesRequest, false, "timeout", probeAt)
	ht.RecordRealAccess(key, schemas.ChatCompletionRequest, realAt)

	activity := ht.GetTargetActivity(key)
	assert.Equal(t, probeAt.UTC().Format(time.RFC3339), activity.LastProbeAt.UTC().Format(time.RFC3339))
	assert.Equal(t, string(schemas.ResponsesRequest), string(activity.LastProbeRequestType))
	assert.Equal(t, "failure", activity.LastProbeResult)
	assert.Equal(t, "timeout", activity.LastProbeError)
	assert.Equal(t, realAt.UTC().Format(time.RFC3339), activity.LastRealAccessAt.UTC().Format(time.RFC3339))
	assert.Equal(t, string(schemas.ChatCompletionRequest), string(activity.LastRealAccessRequestType))
}

func TestHealthTracker_RecordProbeResult_DoesNotOverwriteLastRealAccess(t *testing.T) {
	ht := NewHealthTracker()
	key := TargetKey("openai", "gpt-4.1", "relay-a")
	realAt := time.Now().Add(-time.Minute)
	probeAt := time.Now()

	ht.RecordRealAccess(key, schemas.ChatCompletionRequest, realAt)
	ht.RecordProbeResult(key, schemas.ResponsesRequest, true, "", probeAt)

	activity := ht.GetTargetActivity(key)
	assert.Equal(t, realAt.UTC().Format(time.RFC3339), activity.LastRealAccessAt.UTC().Format(time.RFC3339))
	assert.Equal(t, string(schemas.ChatCompletionRequest), string(activity.LastRealAccessRequestType))
	assert.Equal(t, probeAt.UTC().Format(time.RFC3339), activity.LastProbeAt.UTC().Format(time.RFC3339))
	assert.Equal(t, string(schemas.ResponsesRequest), string(activity.LastProbeRequestType))
	assert.Equal(t, "success", activity.LastProbeResult)
	assert.Empty(t, activity.LastProbeError)
}

func TestHealthTracker_SetPendingFirstProbe_VisibleInSnapshot(t *testing.T) {
	ht := NewHealthTracker()
	key := TargetKey("openai", "gpt-4.1", "relay-a")

	ht.SetPendingFirstProbe(key, true)
	activity := ht.GetTargetActivity(key)
	assert.True(t, activity.PendingFirstProbe)

	ht.SetPendingFirstProbe(key, false)
	activity = ht.GetTargetActivity(key)
	assert.False(t, activity.PendingFirstProbe)
}

func TestHealthTracker_GetTargetStatusForRule_PreservesLegacyObservedFields(t *testing.T) {
	ht := NewHealthTracker()
	now := time.Now()
	key := TargetKey("openai", "gpt-4.1", "relay-a")

	ht.RecordRealAccess(key, schemas.ChatCompletionRequest, now.Add(-time.Minute))
	ht.RecordProbeResult(key, schemas.ResponsesRequest, true, "", now)

	snap := ht.GetTargetStatusForRule("rule-a", key, testPolicy(), now)
	require.NotNil(t, snap.LastObservedAt)
	assert.Equal(t, now.UTC().Format(time.RFC3339), *snap.LastObservedAt)
	assert.Equal(t, string(HealthObservationSourceActive), snap.LastObservationSource)
	assert.Equal(t, string(schemas.ResponsesRequest), snap.LastObservedRequestType)
}

func TestHealthTracker_ConsecutiveDefault_FallsBackToThreshold(t *testing.T) {
	ht := NewHealthTracker()
	// ConsecutiveFailures=0 means use FailureThreshold as default
	policy := &configstoreTables.HealthPolicy{
		FailureThreshold:     2,
		FailureWindowSeconds: 10,
		CooldownSeconds:      30,
		ConsecutiveFailures:  0,
	}
	now := time.Now()

	// Two failures far apart (outside 10s window), but consecutive=2
	ht.RecordFailure("openai:gpt-4.1", "502", now)
	ht.RecordFailure("openai:gpt-4.1", "502", now.Add(60*time.Second))

	assert.True(t, ht.IsInCooldown("openai:gpt-4.1", policy, now.Add(60*time.Second)),
		"with ConsecutiveFailures=0, should fall back to FailureThreshold=2 as consecutive threshold")
}

func TestHealthTracker_GetTargetStatus_EvaluatesThreshold(t *testing.T) {
	ht := NewHealthTracker()
	policy := testPolicy()
	now := time.Now()

	// Record 2 failures (meets threshold=2) but do NOT call IsInCooldown
	ht.RecordFailure("openai:gpt-4.1", "502", now)
	ht.RecordFailure("openai:gpt-4.1", "503", now.Add(1*time.Second))

	// GetTargetStatus should evaluate thresholds and report "cooldown"
	// even though IsInCooldown was never called during routing
	snap := ht.GetTargetStatus("openai:gpt-4.1", policy, now.Add(2*time.Second))
	assert.Equal(t, "cooldown", snap.Status,
		"GetTargetStatus should evaluate thresholds, not just report pre-existing cooldown")
	assert.Equal(t, 2, snap.FailureCount)
	assert.NotNil(t, snap.CooldownUntil)
}

func TestHealthTracker_GetTargetStatus_ConsecutiveFailures(t *testing.T) {
	ht := NewHealthTracker()
	policy := &configstoreTables.HealthPolicy{
		FailureThreshold:     3,
		FailureWindowSeconds: 10,
		CooldownSeconds:      30,
		ConsecutiveFailures:  2,
	}
	now := time.Now()

	// Failures outside window but consecutive count=2
	ht.RecordFailure("relay:gpt-4.1:key-a", "timeout", now)
	ht.RecordFailure("relay:gpt-4.1:key-a", "timeout", now.Add(30*time.Second))

	// GetTargetStatus should detect consecutive trigger
	snap := ht.GetTargetStatus("relay:gpt-4.1:key-a", policy, now.Add(30*time.Second))
	assert.Equal(t, "cooldown", snap.Status, "should trigger via consecutive failures")
	assert.Equal(t, 2, snap.ConsecutiveFailures)
}

func TestHealthTracker_GetTargetStatus_AvailableBelowThreshold(t *testing.T) {
	ht := NewHealthTracker()
	policy := testPolicy()
	now := time.Now()

	ht.RecordFailure("openai:gpt-4.1", "502", now)

	snap := ht.GetTargetStatus("openai:gpt-4.1", policy, now.Add(1*time.Second))
	assert.Equal(t, "available", snap.Status, "below threshold should be available")
	assert.Equal(t, 1, snap.FailureCount)
}

func TestHealthTracker_LazyCooldownUsesLastFailureTime(t *testing.T) {
	ht := NewHealthTracker()
	policy := &configstoreTables.HealthPolicy{
		FailureThreshold:     2,
		FailureWindowSeconds: 30,
		CooldownSeconds:      30,
		ConsecutiveFailures:  2,
	}
	now := time.Now()

	ht.RecordFailure("relay:gpt-4.1:key-a", "502", now)
	ht.RecordFailure("relay:gpt-4.1:key-a", "502", now.Add(1*time.Second))

	// No evaluation happened when the threshold was crossed. A late evaluation after the
	// cooldown window should not start a brand-new cooldown from "now".
	assert.False(t, ht.IsInCooldown("relay:gpt-4.1:key-a", policy, now.Add(32*time.Second)))

	snap := ht.GetTargetStatus("relay:gpt-4.1:key-a", policy, now.Add(32*time.Second))
	assert.Equal(t, "available", snap.Status)
	assert.Equal(t, 0, snap.ConsecutiveFailures)
}

func TestApplyHealthPolicyDefaults_PreservesExplicitHalfOpenFalse(t *testing.T) {
	halfOpen := false
	policy := ApplyHealthPolicyDefaults(&configstoreTables.HealthPolicy{
		HalfOpenProbe: &halfOpen,
	})

	require.NotNil(t, policy.HalfOpenProbe)
	assert.False(t, *policy.HalfOpenProbe)
}

func TestApplyHealthPolicyDefaults_PreservesExplicitSlowRecoveryZero(t *testing.T) {
	recovery := 0
	policy := ApplyHealthPolicyDefaults(&configstoreTables.HealthPolicy{
		SlowRecoverySeconds: &recovery,
	})

	require.NotNil(t, policy.SlowRecoverySeconds)
	assert.Equal(t, 0, *policy.SlowRecoverySeconds)
}

func TestGetTargetHealth_DegradedBySlowRatio(t *testing.T) {
	ht := NewHealthTracker()
	recovery := 60
	policy := ApplyHealthPolicyDefaults(&configstoreTables.HealthPolicy{
		SlowWindowSize:       10,
		SlowRatioThreshold:   0.5,
		SlowRecoverySeconds:  &recovery,
		FailureThreshold:     2,
		FailureWindowSeconds: 30,
		CooldownSeconds:      30,
	})
	now := time.Now()
	key := "openai:gpt-4.1"

	for i := 0; i < 6; i++ {
		ht.RecordOutcome("rule-a", key, OutcomeSlow, 60*time.Second, "", policy, now.Add(time.Duration(i)*time.Second))
	}
	for i := 6; i < 10; i++ {
		ht.RecordOutcome("rule-a", key, OutcomeSuccess, 2*time.Second, "", policy, now.Add(time.Duration(i)*time.Second))
	}

	snap := ht.GetTargetStatusForRule("rule-a", key, policy, now.Add(10*time.Second))
	assert.Equal(t, HealthDegraded, ht.GetTargetHealthForRule("rule-a", key, policy, now.Add(10*time.Second)))
	assert.Equal(t, "available", snap.Status)
	assert.Equal(t, "degraded", snap.HealthLevel)
	assert.Equal(t, 6, snap.SlowCount)
	assert.Equal(t, 10, snap.SampleCount)
	assert.InDelta(t, 0.6, snap.SlowRatio, 0.001)
	require.NotNil(t, snap.P95LatencyMs)
	assert.GreaterOrEqual(t, *snap.P95LatencyMs, int64(60000))
	assert.Nil(t, snap.CooldownUntil, "slow responses should demote to degraded without locking target into hard cooldown")
}

func TestGetTargetHealth_SingleSlowRequestDoesNotDegradeOrCooldown(t *testing.T) {
	ht := NewHealthTracker()
	recovery := 60
	policy := ApplyHealthPolicyDefaults(&configstoreTables.HealthPolicy{
		SlowWindowSize:      10,
		SlowRatioThreshold:  0.5,
		SlowRecoverySeconds: &recovery,
	})
	now := time.Now()
	key := "openai:gpt-4.1"

	for i := 0; i < 9; i++ {
		ht.RecordOutcome("rule-a", key, OutcomeSuccess, 2*time.Second, "", policy, now.Add(time.Duration(i)*time.Second))
	}
	ht.RecordOutcome("rule-a", key, OutcomeSlow, 65*time.Second, "", policy, now.Add(10*time.Second))

	assert.Equal(t, HealthHealthy, ht.GetTargetHealthForRule("rule-a", key, policy, now.Add(11*time.Second)))
}

func TestGetTargetHealth_SlowDegradedAutoExpiresAfterSlowRecoverySeconds(t *testing.T) {
	ht := NewHealthTracker()
	recovery := 30
	policy := ApplyHealthPolicyDefaults(&configstoreTables.HealthPolicy{
		SlowWindowSize:       10,
		SlowRatioThreshold:   0.5,
		SlowRecoverySeconds:  &recovery,
		FailureThreshold:     2,
		FailureWindowSeconds: 30,
		CooldownSeconds:      30,
	})
	now := time.Now()
	key := "openai:gpt-4.1"

	for i := 0; i < 6; i++ {
		ht.RecordOutcome("rule-a", key, OutcomeSlow, 60*time.Second, "", policy, now.Add(time.Duration(i)*time.Second))
	}
	for i := 6; i < 10; i++ {
		ht.RecordOutcome("rule-a", key, OutcomeSuccess, 2*time.Second, "", policy, now.Add(time.Duration(i)*time.Second))
	}

	assert.Equal(t, HealthDegraded, ht.GetTargetHealthForRule("rule-a", key, policy, now.Add(10*time.Second)))
	// Last slow was at now+5s; after 30s recovery window (now+36s) slow samples auto-clear.
	snap := ht.GetTargetStatusForRule("rule-a", key, policy, now.Add(36*time.Second))
	assert.Equal(t, "available", snap.Status)
	assert.Equal(t, "healthy", snap.HealthLevel)
	assert.Equal(t, 0, snap.SlowCount)
	assert.Equal(t, 4, snap.SampleCount)
}

func TestRecordOutcome_OutcomeIgnoreDoesNotResetFailuresOrPolluteHealth(t *testing.T) {
	ht := NewHealthTracker()
	policy := ApplyHealthPolicyDefaults(&configstoreTables.HealthPolicy{
		FailureThreshold:         3,
		FailureWindowSeconds:     60,
		CooldownSeconds:          30,
		FatalConsecutiveFailures: 2,
		FatalCooldownSeconds:     120,
	})
	now := time.Now()
	key := "B.AI:gemini-3.1-pro:key-1"

	// Record 1 fatal error, then an ignored 499 / client 400 — must NOT reset fatalConsecutiveFailures!
	ht.RecordOutcome("rule-a", key, OutcomeFatalChannel, 0, "credit insufficient balance", policy, now)
	ht.RecordOutcome("rule-a", key, OutcomeIgnore, 0, "context canceled", policy, now.Add(time.Second))
	assert.Equal(t, HealthHealthy, ht.GetTargetHealthForRule("rule-a", key, policy, now.Add(2*time.Second)))

	// Second fatal error hits FatalConsecutiveFailures=2 and immediately enters 120s fatal cooldown.
	ht.RecordOutcome("rule-a", key, OutcomeFatalChannel, 0, "credit insufficient balance", policy, now.Add(3*time.Second))
	snap := ht.GetTargetStatusForRule("rule-a", key, policy, now.Add(4*time.Second))
	assert.Equal(t, "cooldown", snap.Status)
	assert.Equal(t, string(OutcomeFatalChannel), snap.CooldownReason)
	require.NotNil(t, snap.CooldownUntil)
	until, err := time.Parse(time.RFC3339, *snap.CooldownUntil)
	require.NoError(t, err)
	assert.Equal(t, now.Add(123*time.Second).UTC().Format(time.RFC3339), until.UTC().Format(time.RFC3339))

	// Another ignored error while in cooldown must NOT clear cooldown!
	ht.RecordOutcome("rule-a", key, OutcomeIgnore, 0, "invalid request", policy, now.Add(5*time.Second))
	assert.Equal(t, HealthCooldown, ht.GetTargetHealthForRule("rule-a", key, policy, now.Add(6*time.Second)))
}

func TestRecordOutcome_OutcomeIgnoreReleasesInFlightHalfOpenLease(t *testing.T) {
	ht := NewHealthTracker()
	policy := ApplyHealthPolicyDefaults(&configstoreTables.HealthPolicy{
		FailureThreshold:     1,
		FailureWindowSeconds: 30,
		CooldownSeconds:      10,
	})
	now := time.Now()
	key := "yunwu:gemini:key-1"

	ht.RecordOutcome("rule-a", key, OutcomeSoftFail, 0, "timeout", policy, now)
	require.Equal(t, HealthCooldown, ht.GetTargetHealthForRule("rule-a", key, policy, now.Add(1*time.Second)))

	// Cooldown expires (20s soft cooldown); first caller claims half-open.
	probeAt := now.Add(25 * time.Second)
	require.True(t, ht.TryClaimHalfOpenProbeForRule("rule-a", key, policy, probeAt))
	require.False(t, ht.TryClaimHalfOpenProbeForRule("rule-a", key, policy, probeAt.Add(time.Second)))

	// Client cancels mid-probe (499 -> OutcomeIgnore): lease is released, cooldown remains armed for next probe.
	ht.RecordOutcome("rule-a", key, OutcomeIgnore, 0, "context canceled", policy, probeAt.Add(2*time.Second))
	state := ht.InspectTargetRoutingStateForRule("rule-a", key, policy, probeAt.Add(3*time.Second))
	assert.Equal(t, HealthCooldown, state.Level)
	assert.True(t, state.HalfOpenEligible)
}

func TestEvaluateCooldown_ErrorRateWindowAvoidsFalseCooldownUnderHighConcurrency(t *testing.T) {
	ht := NewHealthTracker()
	policy := ApplyHealthPolicyDefaults(&configstoreTables.HealthPolicy{
		FailureThreshold:       3,
		FailureWindowSeconds:   120,
		CooldownSeconds:        45,
		ConsecutiveFailures:    4,
		SoftCooldownMultiplier: 1,
		ErrorRateWindowSize:    20,
		ErrorRateMinSamples:    5,
		ErrorRateThreshold:     0.4,
	})
	now := time.Now()
	key := "yunwu:gemini-3.1-pro-preview:k1"

	// 17 fast successes interspersed with 3 timeouts (15% error rate < 40%, max consecutive = 1 < 4)
	for i := 0; i < 20; i++ {
		kind := OutcomeSuccess
		if i == 3 || i == 9 || i == 15 {
			kind = OutcomeSoftFail
		}
		ht.RecordOutcome("rule-a", key, kind, 10*time.Second, "", policy, now.Add(time.Duration(i)*time.Second))
	}
	assert.Equal(t, HealthHealthy, ht.GetTargetHealthForRule("rule-a", key, policy, now.Add(21*time.Second)),
		"15% transient error rate must not trigger cooldown when error_rate_threshold is 40%")

	// Push error rate to >= 40% (add 6 non-consecutive failures interleaved with 2 successes)
	t0 := now.Add(22 * time.Second)
	for i := 0; i < 6; i++ {
		ht.RecordOutcome("rule-a", key, OutcomeSoftFail, 60*time.Second, "timeout", policy, t0.Add(time.Duration(i*2)*time.Second))
		if i%2 == 0 {
			ht.RecordOutcome("rule-a", key, OutcomeSuccess, 10*time.Second, "", policy, t0.Add(time.Duration(i*2+1)*time.Second))
		}
	}
	snap := ht.GetTargetStatusForRule("rule-a", key, policy, t0.Add(15*time.Second))
	assert.Equal(t, "cooldown", snap.Status)
	assert.GreaterOrEqual(t, snap.ErrorRate, 0.4)
}

func TestRecordOutcome_SporadicFatalErrorResetsOnSuccess(t *testing.T) {
	ht := NewHealthTracker()
	policy := ApplyHealthPolicyDefaults(&configstoreTables.HealthPolicy{
		FailureThreshold:         5,
		FailureWindowSeconds:     120,
		CooldownSeconds:          45,
		ConsecutiveFailures:      4,
		FatalConsecutiveFailures: 2,
		FatalCooldownSeconds:     120,
		ErrorRateWindowSize:      20,
		ErrorRateMinSamples:      5,
		ErrorRateThreshold:       0.4,
	})
	now := time.Now()
	key := "NightRouter:gemini-3.1-pro-preview:k1"

	// Relay pool has 1 bad sub-account out of 5 (20% sporadic 403s < 40% error-rate threshold):
	// consecutive fatal count resets on every success so cheap relay stays healthy!
	ht.RecordOutcome("rule-a", key, OutcomeFatalChannel, 0, "no active subscription", policy, now)
	for i := 1; i <= 4; i++ {
		ht.RecordOutcome("rule-a", key, OutcomeSuccess, 8*time.Second, "", policy, now.Add(time.Duration(i)*time.Second))
	}
	ht.RecordOutcome("rule-a", key, OutcomeFatalChannel, 0, "no active subscription", policy, now.Add(5*time.Second))
	for i := 6; i <= 9; i++ {
		ht.RecordOutcome("rule-a", key, OutcomeSuccess, 8*time.Second, "", policy, now.Add(time.Duration(i)*time.Second))
	}

	assert.Equal(t, HealthHealthy, ht.GetTargetHealthForRule("rule-a", key, policy, now.Add(10*time.Second)))
}

func TestGetTargetHealth_SlowRatioThresholdAboveOneDisablesDegraded(t *testing.T) {
	ht := NewHealthTracker()
	recovery := 0
	policy := ApplyHealthPolicyDefaults(&configstoreTables.HealthPolicy{
		SlowWindowSize:       10,
		SlowRatioThreshold:   999,
		SlowRecoverySeconds:  &recovery,
		FailureThreshold:     2,
		FailureWindowSeconds: 30,
		CooldownSeconds:      30,
	})
	now := time.Now()
	key := "openai:gpt-4.1"

	for i := 0; i < 10; i++ {
		ht.RecordOutcome("rule-a", key, OutcomeSlow, 60*time.Second, "", policy, now.Add(time.Duration(i)*time.Second))
	}

	assert.Equal(t, HealthHealthy, ht.GetTargetHealthForRule("rule-a", key, policy, now.Add(10*time.Second)))
}

func TestRecordOutcome_SoftFailUsesMultiplier(t *testing.T) {
	ht := NewHealthTracker()
	policy := ApplyHealthPolicyDefaults(&configstoreTables.HealthPolicy{
		FailureThreshold:       1,
		FailureWindowSeconds:   30,
		CooldownSeconds:        30,
		SoftCooldownMultiplier: 2,
		CooldownBackoffFactor:  1,
		CooldownMaxSeconds:     300,
	})
	now := time.Now()
	key := "openai:gpt-4.1"

	ht.RecordOutcome("rule-a", key, OutcomeSoftFail, 0, "rate limited", policy, now)
	snap := ht.GetTargetStatusForRule("rule-a", key, policy, now)

	require.NotNil(t, snap.CooldownUntil)
	cooldownUntil, err := time.Parse(time.RFC3339, *snap.CooldownUntil)
	require.NoError(t, err)
	assert.Equal(t, now.Add(60*time.Second).UTC().Format(time.RFC3339), cooldownUntil.UTC().Format(time.RFC3339))
	assert.Equal(t, "soft_fail", snap.LastOutcomeKind)
}

func TestRecordOutcome_SlowSuccess_DoesNotResetFailures(t *testing.T) {
	ht := NewHealthTracker()
	recovery := 0
	policy := ApplyHealthPolicyDefaults(&configstoreTables.HealthPolicy{
		FailureThreshold:     3,
		FailureWindowSeconds: 30,
		CooldownSeconds:      30,
		SlowRecoverySeconds:  &recovery,
	})
	now := time.Now()
	key := "openai:gpt-4.1"

	ht.RecordOutcome("rule-a", key, OutcomeHardFail, 0, "502", policy, now)
	ht.RecordOutcome("rule-a", key, OutcomeHardFail, 0, "503", policy, now.Add(time.Second))
	ht.RecordOutcome("rule-a", key, OutcomeSlow, 60*time.Second, "", policy, now.Add(2*time.Second))

	snap := ht.GetTargetStatusForRule("rule-a", key, policy, now.Add(3*time.Second))
	assert.Equal(t, "available", snap.Status)
	assert.Equal(t, 2, snap.FailureCount)
	assert.Equal(t, 0, snap.ConsecutiveFailures)
	assert.Equal(t, "slow", snap.LastOutcomeKind)
}

func TestGetTargetHealth_RecoversToHealthy(t *testing.T) {
	ht := NewHealthTracker()
	recovery := 0
	policy := ApplyHealthPolicyDefaults(&configstoreTables.HealthPolicy{
		SlowWindowSize:       10,
		SlowRatioThreshold:   0.5,
		SlowRecoverySeconds:  &recovery,
		FailureThreshold:     2,
		FailureWindowSeconds: 30,
		CooldownSeconds:      30,
	})
	now := time.Now()
	key := "openai:gpt-4.1"

	for i := 0; i < 6; i++ {
		ht.RecordOutcome("rule-a", key, OutcomeSlow, 60*time.Second, "", policy, now.Add(time.Duration(i)*time.Second))
	}
	for i := 6; i < 16; i++ {
		ht.RecordOutcome("rule-a", key, OutcomeSuccess, time.Second, "", policy, now.Add(time.Duration(i)*time.Second))
	}

	assert.Equal(t, HealthHealthy, ht.GetTargetHealthForRule("rule-a", key, policy, now.Add(16*time.Second)))
}

func TestHalfOpenProbe_OnlyOneInFlight(t *testing.T) {
	ht := NewHealthTracker()
	policy := ApplyHealthPolicyDefaults(&configstoreTables.HealthPolicy{
		FailureThreshold:     1,
		FailureWindowSeconds: 30,
		CooldownSeconds:      1,
	})
	now := time.Now()
	key := "openai:gpt-4.1"

	ht.RecordOutcome("rule-a", key, OutcomeHardFail, 0, "502", policy, now)
	assert.Equal(t, HealthCooldown, ht.GetTargetHealthForRule("rule-a", key, policy, now))

	level, halfOpen := ht.GetTargetRoutingHealthForRule("rule-a", key, policy, now.Add(2*time.Second))
	assert.Equal(t, HealthCooldown, level)
	assert.True(t, halfOpen)

	for i := 0; i < 5; i++ {
		level, halfOpen = ht.GetTargetRoutingHealthForRule("rule-a", key, policy, now.Add(2*time.Second))
		assert.Equal(t, HealthCooldown, level)
		assert.False(t, halfOpen)
	}

	ht.RecordOutcome("rule-a", key, OutcomeSuccess, time.Second, "", policy, now.Add(3*time.Second))
	assert.Equal(t, HealthHealthy, ht.GetTargetHealthForRule("rule-a", key, policy, now.Add(3*time.Second)))
}

func TestExponentialBackoff_StreakIncrement(t *testing.T) {
	ht := NewHealthTracker()
	policy := ApplyHealthPolicyDefaults(&configstoreTables.HealthPolicy{
		FailureThreshold:      1,
		FailureWindowSeconds:  30,
		CooldownSeconds:       10,
		CooldownBackoffFactor: 2,
		CooldownMaxSeconds:    60,
	})
	now := time.Now()
	key := "openai:gpt-4.1"

	ht.RecordOutcome("rule-a", key, OutcomeHardFail, 0, "502", policy, now)
	snap := ht.GetTargetStatusForRule("rule-a", key, policy, now)
	require.NotNil(t, snap.CooldownUntil)
	firstUntil, err := time.Parse(time.RFC3339, *snap.CooldownUntil)
	require.NoError(t, err)
	assert.Equal(t, now.Add(10*time.Second).UTC().Format(time.RFC3339), firstUntil.UTC().Format(time.RFC3339))

	ht.GetTargetHealthForRule("rule-a", key, policy, now.Add(11*time.Second))
	ht.RecordOutcome("rule-a", key, OutcomeHardFail, 0, "502", policy, now.Add(12*time.Second))
	snap = ht.GetTargetStatusForRule("rule-a", key, policy, now.Add(12*time.Second))
	require.NotNil(t, snap.CooldownUntil)
	secondUntil, err := time.Parse(time.RFC3339, *snap.CooldownUntil)
	require.NoError(t, err)
	assert.Equal(t, now.Add(32*time.Second).UTC().Format(time.RFC3339), secondUntil.UTC().Format(time.RFC3339))
	assert.Equal(t, 2, snap.CooldownStreak)
}

func TestRecordOutcome_HardFailUsesBaseCooldown(t *testing.T) {
	ht := NewHealthTracker()
	policy := ApplyHealthPolicyDefaults(&configstoreTables.HealthPolicy{
		FailureThreshold:       1,
		FailureWindowSeconds:   30,
		CooldownSeconds:        30,
		SoftCooldownMultiplier: 2,
		CooldownBackoffFactor:  1,
		CooldownMaxSeconds:     300,
	})
	now := time.Now()
	key := "openai:gpt-4.1"

	ht.RecordOutcome("rule-a", key, OutcomeHardFail, 0, "500", policy, now)
	snap := ht.GetTargetStatusForRule("rule-a", key, policy, now)

	require.NotNil(t, snap.CooldownUntil)
	cooldownUntil, err := time.Parse(time.RFC3339, *snap.CooldownUntil)
	require.NoError(t, err)
	assert.Equal(t, now.Add(30*time.Second).UTC().Format(time.RFC3339), cooldownUntil.UTC().Format(time.RFC3339))
	assert.Equal(t, "hard_fail", snap.LastOutcomeKind)
}
