package governance

import (
	"fmt"
	"sync"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
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

type HealthObservationSource string

const (
	HealthObservationSourcePassive HealthObservationSource = "passive"
	HealthObservationSourceActive  HealthObservationSource = "active"
)

type TargetActivityState struct {
	mu                        sync.Mutex
	lastRealAccessAt          time.Time
	lastRealAccessRequestType schemas.RequestType
	lastProbeAt               time.Time
	lastProbeRequestType      schemas.RequestType
	lastProbeResult           string
	lastProbeError            string
	pendingFirstProbe         bool
}

type TargetActivitySnapshot struct {
	LastRealAccessAt          time.Time
	LastRealAccessRequestType schemas.RequestType
	LastProbeAt               time.Time
	LastProbeRequestType      schemas.RequestType
	LastProbeResult           string
	LastProbeError            string
	PendingFirstProbe         bool
}

type TargetObservationSnapshot struct {
	LastObservedAt          time.Time
	LastObservedRequestType schemas.RequestType
	LastObservationSource   HealthObservationSource
}

// HealthTracker tracks health state for routing targets (in-process, not shared across instances).
//
// Failure recording is decoupled from cooldown triggering:
// - RecordFailure just appends a timestamp (cheap, called from PostLLMHook for every failure)
// - IsInCooldown evaluates the policy lazily during chain building
type HealthTracker struct {
	mu         sync.RWMutex
	targets    map[string]*TargetHealthState   // key = TargetKey
	activities map[string]*TargetActivityState // key = concrete TargetKey (provider:model[:key_id])
}

// NewHealthTracker creates a new in-process HealthTracker
func NewHealthTracker() *HealthTracker {
	return &HealthTracker{
		targets:    make(map[string]*TargetHealthState),
		activities: make(map[string]*TargetActivityState),
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

func (ht *HealthTracker) getOrCreateActivity(key string) *TargetActivityState {
	ht.mu.RLock()
	s, ok := ht.activities[key]
	ht.mu.RUnlock()
	if ok {
		return s
	}
	ht.mu.Lock()
	defer ht.mu.Unlock()
	if s, ok = ht.activities[key]; ok {
		return s
	}
	s = &TargetActivityState{}
	ht.activities[key] = s
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
	resetCooldownLocked(s)
}

// RecordSuccessForRule records a success for a target scoped to a specific routing rule.
func (ht *HealthTracker) RecordSuccessForRule(ruleID, targetKey string) {
	ht.RecordSuccess(scopedTargetKey(ruleID, targetKey))
}

func (ht *HealthTracker) RecordRealAccess(targetKey string, requestType schemas.RequestType, now time.Time) {
	if targetKey == "" {
		return
	}
	s := ht.getOrCreateActivity(targetKey)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastRealAccessAt = now
	s.lastRealAccessRequestType = requestType
}

func (ht *HealthTracker) RecordProbeResult(targetKey string, requestType schemas.RequestType, success bool, failureMsg string, now time.Time) {
	if targetKey == "" {
		return
	}
	s := ht.getOrCreateActivity(targetKey)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastProbeAt = now
	s.lastProbeRequestType = requestType
	if success {
		s.lastProbeResult = "success"
		s.lastProbeError = ""
	} else {
		s.lastProbeResult = "failure"
		s.lastProbeError = failureMsg
	}
	s.pendingFirstProbe = false
}

func (ht *HealthTracker) SetPendingFirstProbe(targetKey string, pending bool) {
	if targetKey == "" {
		return
	}
	s := ht.getOrCreateActivity(targetKey)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingFirstProbe = pending
}

func (ht *HealthTracker) GetTargetActivity(targetKey string) TargetActivitySnapshot {
	ht.mu.RLock()
	s, ok := ht.activities[targetKey]
	ht.mu.RUnlock()
	if !ok {
		return TargetActivitySnapshot{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return TargetActivitySnapshot{
		LastRealAccessAt:          s.lastRealAccessAt,
		LastRealAccessRequestType: s.lastRealAccessRequestType,
		LastProbeAt:               s.lastProbeAt,
		LastProbeRequestType:      s.lastProbeRequestType,
		LastProbeResult:           s.lastProbeResult,
		LastProbeError:            s.lastProbeError,
		PendingFirstProbe:         s.pendingFirstProbe,
	}
}

func (ht *HealthTracker) RecordObservation(targetKey string, requestType schemas.RequestType, source HealthObservationSource, now time.Time) {
	if targetKey == "" {
		return
	}
	s := ht.getOrCreateActivity(targetKey)
	s.mu.Lock()
	defer s.mu.Unlock()
	if source == HealthObservationSourceActive {
		s.lastProbeAt = now
		s.lastProbeRequestType = requestType
		return
	}
	s.lastRealAccessAt = now
	s.lastRealAccessRequestType = requestType
}

func (ht *HealthTracker) GetObservation(targetKey string) TargetObservationSnapshot {
	activity := ht.GetTargetActivity(targetKey)
	if activity.LastRealAccessAt.IsZero() && activity.LastProbeAt.IsZero() {
		return TargetObservationSnapshot{}
	}

	if activity.LastProbeAt.After(activity.LastRealAccessAt) {
		return TargetObservationSnapshot{
			LastObservedAt:          activity.LastProbeAt,
			LastObservedRequestType: activity.LastProbeRequestType,
			LastObservationSource:   HealthObservationSourceActive,
		}
	}

	if !activity.LastRealAccessAt.IsZero() {
		return TargetObservationSnapshot{
			LastObservedAt:          activity.LastRealAccessAt,
			LastObservedRequestType: activity.LastRealAccessRequestType,
			LastObservationSource:   HealthObservationSourcePassive,
		}
	}

	return TargetObservationSnapshot{
		LastObservedAt:          activity.LastProbeAt,
		LastObservedRequestType: activity.LastProbeRequestType,
		LastObservationSource:   HealthObservationSourceActive,
	}
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

	return evaluateCooldownLocked(s, policy, now)
}

// IsInCooldownForRule evaluates cooldown for a target scoped to a specific routing rule.
func (ht *HealthTracker) IsInCooldownForRule(ruleID, targetKey string, policy *configstoreTables.HealthPolicy, now time.Time) bool {
	return ht.IsInCooldown(scopedTargetKey(ruleID, targetKey), policy, now)
}

// TargetHealthSnapshot is a point-in-time view of a target's health state
type TargetHealthSnapshot struct {
	Key                     string  `json:"key"`
	Status                  string  `json:"status"` // "available" | "cooldown"
	FailureCount            int     `json:"failure_count"`
	ConsecutiveFailures     int     `json:"consecutive_failures"`
	CooldownUntil           *string `json:"cooldown_until,omitempty"`
	LastFailureTime         *string `json:"last_failure_time,omitempty"`
	LastFailureMsg          string  `json:"last_failure_msg,omitempty"`
	LastObservedAt          *string `json:"last_observed_at,omitempty"`
	LastObservedRequestType string  `json:"last_observed_request_type,omitempty"`
	LastObservationSource   string  `json:"last_observation_source,omitempty"`
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
	observation := ht.GetObservation(displayKey)

	ht.mu.RLock()
	s, ok := ht.targets[stateKey]
	ht.mu.RUnlock()

	if !ok {
		snap := TargetHealthSnapshot{Key: displayKey, Status: "available"}
		applyObservationSnapshot(&snap, observation)
		return snap
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	inCooldown := evaluateCooldownLocked(s, policy, now)

	snap := TargetHealthSnapshot{
		Key:                 displayKey,
		FailureCount:        len(s.failures),
		ConsecutiveFailures: s.consecutiveFailures,
	}

	if inCooldown && !s.cooldownUntil.IsZero() {
		snap.Status = "cooldown"
		cu := s.cooldownUntil.UTC().Format(time.RFC3339)
		snap.CooldownUntil = &cu
	} else {
		snap.Status = "available"
	}

	if !s.lastFailureTime.IsZero() {
		lft := s.lastFailureTime.UTC().Format(time.RFC3339)
		snap.LastFailureTime = &lft
		snap.LastFailureMsg = s.lastFailureMsg
	}

	applyObservationSnapshot(&snap, observation)

	return snap
}

func applyObservationSnapshot(snap *TargetHealthSnapshot, observation TargetObservationSnapshot) {
	if snap == nil {
		return
	}
	if !observation.LastObservedAt.IsZero() {
		loa := observation.LastObservedAt.UTC().Format(time.RFC3339)
		snap.LastObservedAt = &loa
	}
	if observation.LastObservedRequestType != "" {
		snap.LastObservedRequestType = string(observation.LastObservedRequestType)
	}
	if observation.LastObservationSource != "" {
		snap.LastObservationSource = string(observation.LastObservationSource)
	}
}

func evaluateCooldownLocked(s *TargetHealthState, policy *configstoreTables.HealthPolicy, now time.Time) bool {
	if !s.cooldownUntil.IsZero() {
		if !now.Before(s.cooldownUntil) {
			resetCooldownLocked(s)
			return false
		}
		return true
	}

	windowStart := now.Add(-time.Duration(policy.FailureWindowSeconds) * time.Second)
	pruned := s.failures[:0]
	for _, t := range s.failures {
		if t.After(windowStart) {
			pruned = append(pruned, t)
		}
	}
	s.failures = pruned

	windowTriggered := len(s.failures) >= policy.FailureThreshold
	consecThreshold := policy.ConsecutiveFailures
	if consecThreshold <= 0 {
		consecThreshold = policy.FailureThreshold
	}
	consecutiveTriggered := s.consecutiveFailures >= consecThreshold
	if !windowTriggered && !consecutiveTriggered {
		return false
	}

	cooldownStart := s.lastFailureTime
	if cooldownStart.IsZero() {
		cooldownStart = now
	}
	s.cooldownUntil = cooldownStart.Add(time.Duration(policy.CooldownSeconds) * time.Second)
	if !now.Before(s.cooldownUntil) {
		resetCooldownLocked(s)
		return false
	}
	return true
}

func resetCooldownLocked(s *TargetHealthState) {
	s.cooldownUntil = time.Time{}
	s.failures = s.failures[:0]
	s.consecutiveFailures = 0
	s.lastFailureTime = time.Time{}
	s.lastFailureMsg = ""
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
