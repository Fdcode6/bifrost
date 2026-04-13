package governance

import (
	"fmt"
	"sync"
	"time"

	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
)

// TargetKey uniquely identifies a routing target for health tracking: provider:model[:key_id]
func TargetKey(provider, model, keyID string) string {
	if keyID != "" {
		return fmt.Sprintf("%s:%s:%s", provider, model, keyID)
	}
	return fmt.Sprintf("%s:%s", provider, model)
}

// RouteGroupTargetKey returns the TargetKey for a RouteGroupTarget
func RouteGroupTargetKey(t configstoreTables.RouteGroupTarget) string {
	p, m, k := "", "", ""
	if t.Provider != nil {
		p = *t.Provider
	}
	if t.Model != nil {
		m = *t.Model
	}
	if t.KeyID != nil {
		k = *t.KeyID
	}
	return TargetKey(p, m, k)
}

func scopedTargetKey(ruleID, targetKey string) string {
	if ruleID == "" {
		return targetKey
	}
	return fmt.Sprintf("%s::%s", ruleID, targetKey)
}

// TargetHealthState holds the health state for a single target
type TargetHealthState struct {
	mu                  sync.Mutex
	failures            []time.Time // timestamps of recent failures
	consecutiveFailures int         // consecutive failures without any success
	cooldownUntil       time.Time   // zero value means not in cooldown
	lastFailureTime     time.Time
	lastFailureMsg      string
}

// HealthTracker tracks health state for routing targets (in-process, not shared across instances).
//
// Failure recording is decoupled from cooldown triggering:
// - RecordFailure just appends a timestamp (cheap, called from PostLLMHook for every failure)
// - IsInCooldown evaluates the policy lazily during chain building
type HealthTracker struct {
	mu      sync.RWMutex
	targets map[string]*TargetHealthState // key = TargetKey
}

// NewHealthTracker creates a new in-process HealthTracker
func NewHealthTracker() *HealthTracker {
	return &HealthTracker{
		targets: make(map[string]*TargetHealthState),
	}
}

// getOrCreate returns existing state or lazily creates one
func (ht *HealthTracker) getOrCreate(key string) *TargetHealthState {
	ht.mu.RLock()
	s, ok := ht.targets[key]
	ht.mu.RUnlock()
	if ok {
		return s
	}
	ht.mu.Lock()
	defer ht.mu.Unlock()
	// double check after write lock
	if s, ok = ht.targets[key]; ok {
		return s
	}
	s = &TargetHealthState{}
	ht.targets[key] = s
	return s
}

// RecordFailure records a failure timestamp for the given target.
// This is a lightweight operation — it does NOT evaluate any policy or trigger cooldown.
// Cooldown evaluation happens lazily in IsInCooldown when a grouped routing decision is built.
func (ht *HealthTracker) RecordFailure(key string, failureMsg string, now time.Time) {
	s := ht.getOrCreate(key)
	s.mu.Lock()
	defer s.mu.Unlock()

	s.lastFailureTime = now
	s.lastFailureMsg = failureMsg
	s.failures = append(s.failures, now)
	s.consecutiveFailures++
}

// RecordFailureForRule records a failure for a target scoped to a specific routing rule.
func (ht *HealthTracker) RecordFailureForRule(ruleID, targetKey, failureMsg string, now time.Time) {
	ht.RecordFailure(scopedTargetKey(ruleID, targetKey), failureMsg, now)
}

// RecordSuccess records a successful request, resetting the consecutive failure counter.
// This enables the consecutive-failure trigger to distinguish persistent outages from transient errors.
func (ht *HealthTracker) RecordSuccess(key string) {
	ht.mu.RLock()
	s, ok := ht.targets[key]
	ht.mu.RUnlock()
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.consecutiveFailures = 0
}

// RecordSuccessForRule records a success for a target scoped to a specific routing rule.
func (ht *HealthTracker) RecordSuccessForRule(ruleID, targetKey string) {
	ht.RecordSuccess(scopedTargetKey(ruleID, targetKey))
}

// IsInCooldown checks if the target should be considered in cooldown based on the given policy.
// It prunes old failures, evaluates the threshold, and triggers/expires cooldown as needed.
// This is the main evaluation point, called during grouped routing chain building.
func (ht *HealthTracker) IsInCooldown(key string, policy *configstoreTables.HealthPolicy, now time.Time) bool {
	ht.mu.RLock()
	s, ok := ht.targets[key]
	ht.mu.RUnlock()
	if !ok {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// If explicitly in cooldown, check if expired
	if !s.cooldownUntil.IsZero() {
		if now.After(s.cooldownUntil) {
			// cooldown expired — auto-recover
			s.cooldownUntil = time.Time{}
			s.failures = s.failures[:0]
			s.consecutiveFailures = 0
			return false
		}
		return true
	}

	// Prune failures outside window
	windowStart := now.Add(-time.Duration(policy.FailureWindowSeconds) * time.Second)
	pruned := s.failures[:0]
	for _, t := range s.failures {
		if t.After(windowStart) {
			pruned = append(pruned, t)
		}
	}
	s.failures = pruned

	// Dual-trigger check:
	// 1. Window trigger: N failures within the sliding window (handles burst failures)
	// 2. Consecutive trigger: N consecutive failures regardless of time (handles slow-drip failures)
	windowTriggered := len(s.failures) >= policy.FailureThreshold

	consecThreshold := policy.ConsecutiveFailures
	if consecThreshold <= 0 {
		consecThreshold = policy.FailureThreshold // default: same as window threshold
	}
	consecutiveTriggered := s.consecutiveFailures >= consecThreshold

	if windowTriggered || consecutiveTriggered {
		s.cooldownUntil = now.Add(time.Duration(policy.CooldownSeconds) * time.Second)
		return true
	}
	return false
}

// IsInCooldownForRule evaluates cooldown for a target scoped to a specific routing rule.
func (ht *HealthTracker) IsInCooldownForRule(ruleID, targetKey string, policy *configstoreTables.HealthPolicy, now time.Time) bool {
	return ht.IsInCooldown(scopedTargetKey(ruleID, targetKey), policy, now)
}

// TargetHealthSnapshot is a point-in-time view of a target's health state
type TargetHealthSnapshot struct {
	Key                 string  `json:"key"`
	Status              string  `json:"status"` // "available" | "cooldown"
	FailureCount        int     `json:"failure_count"`
	ConsecutiveFailures int     `json:"consecutive_failures"`
	CooldownUntil       *string `json:"cooldown_until,omitempty"`
	LastFailureTime     *string `json:"last_failure_time,omitempty"`
	LastFailureMsg      string  `json:"last_failure_msg,omitempty"`
}

// GetTargetStatus returns a snapshot of the health state for the given target.
// It evaluates thresholds consistently with IsInCooldown so the snapshot reflects
// the actual routing state (a target that has crossed thresholds is reported as "cooldown"
// even if it hasn't been formally evaluated by the routing engine yet).
func (ht *HealthTracker) GetTargetStatus(key string, policy *configstoreTables.HealthPolicy, now time.Time) TargetHealthSnapshot {
	return ht.getTargetStatus(key, key, policy, now)
}

// GetTargetStatusForRule returns a snapshot for a target scoped to a specific routing rule,
// while keeping the user-facing snapshot key readable as the original target identity.
func (ht *HealthTracker) GetTargetStatusForRule(ruleID, targetKey string, policy *configstoreTables.HealthPolicy, now time.Time) TargetHealthSnapshot {
	return ht.getTargetStatus(scopedTargetKey(ruleID, targetKey), targetKey, policy, now)
}

func (ht *HealthTracker) getTargetStatus(stateKey, displayKey string, policy *configstoreTables.HealthPolicy, now time.Time) TargetHealthSnapshot {
	ht.mu.RLock()
	s, ok := ht.targets[stateKey]
	ht.mu.RUnlock()

	if !ok {
		return TargetHealthSnapshot{Key: displayKey, Status: "available"}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Check cooldown expiry
	if !s.cooldownUntil.IsZero() && now.After(s.cooldownUntil) {
		// cooldown expired — auto-recover
		s.cooldownUntil = time.Time{}
		s.failures = s.failures[:0]
		s.consecutiveFailures = 0
	}

	// Prune old failures outside window
	windowStart := now.Add(-time.Duration(policy.FailureWindowSeconds) * time.Second)
	pruned := s.failures[:0]
	for _, t := range s.failures {
		if t.After(windowStart) {
			pruned = append(pruned, t)
		}
	}
	s.failures = pruned

	snap := TargetHealthSnapshot{
		Key:                 displayKey,
		FailureCount:        len(s.failures),
		ConsecutiveFailures: s.consecutiveFailures,
	}

	if !s.cooldownUntil.IsZero() && now.Before(s.cooldownUntil) {
		// Already in cooldown
		snap.Status = "cooldown"
		cu := s.cooldownUntil.UTC().Format(time.RFC3339)
		snap.CooldownUntil = &cu
	} else {
		// Evaluate thresholds — same logic as IsInCooldown
		windowTriggered := len(s.failures) >= policy.FailureThreshold
		consecThreshold := policy.ConsecutiveFailures
		if consecThreshold <= 0 {
			consecThreshold = policy.FailureThreshold
		}
		consecutiveTriggered := s.consecutiveFailures >= consecThreshold

		if windowTriggered || consecutiveTriggered {
			// Threshold met — trigger cooldown (consistent with routing behavior)
			s.cooldownUntil = now.Add(time.Duration(policy.CooldownSeconds) * time.Second)
			snap.Status = "cooldown"
			cu := s.cooldownUntil.UTC().Format(time.RFC3339)
			snap.CooldownUntil = &cu
		} else {
			snap.Status = "available"
		}
	}

	if !s.lastFailureTime.IsZero() {
		lft := s.lastFailureTime.UTC().Format(time.RFC3339)
		snap.LastFailureTime = &lft
		snap.LastFailureMsg = s.lastFailureMsg
	}

	return snap
}

// GetAllStatuses returns snapshots for all tracked targets
func (ht *HealthTracker) GetAllStatuses(policy *configstoreTables.HealthPolicy, now time.Time) []TargetHealthSnapshot {
	ht.mu.RLock()
	keys := make([]string, 0, len(ht.targets))
	for k := range ht.targets {
		keys = append(keys, k)
	}
	ht.mu.RUnlock()

	result := make([]TargetHealthSnapshot, 0, len(keys))
	for _, k := range keys {
		result = append(result, ht.GetTargetStatus(k, policy, now))
	}
	return result
}
