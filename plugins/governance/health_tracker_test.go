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
