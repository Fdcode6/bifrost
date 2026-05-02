package governance

import (
	"fmt"
	"math"
	"sort"
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
	recentSamples       []latencySample
	slowCount           int
	softFailCount       int
	hardFailCount       int
	lastSlowAt          time.Time
	lastSlowLatency     time.Duration
	cooldownStreak      int
	halfOpenInFlight    bool
	halfOpenStartedAt   time.Time
	lastOutcomeKind     OutcomeKind
}

type latencySample struct {
	at      time.Time
	latency time.Duration
	kind    OutcomeKind
}

type HealthLevel string

const (
	HealthHealthy  HealthLevel = "healthy"
	HealthDegraded HealthLevel = "degraded"
	HealthCooldown HealthLevel = "cooldown"
)

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

func ApplyHealthPolicyDefaults(policy *configstoreTables.HealthPolicy) *configstoreTables.HealthPolicy {
	if policy == nil {
		policy = &configstoreTables.HealthPolicy{}
	}
	if policy.FailureThreshold <= 0 {
		policy.FailureThreshold = 2
	}
	if policy.FailureWindowSeconds <= 0 {
		policy.FailureWindowSeconds = 30
	}
	if policy.CooldownSeconds <= 0 {
		policy.CooldownSeconds = 30
	}
	if policy.SlowThresholdMs <= 0 {
		policy.SlowThresholdMs = 45000
	}
	if policy.SlowWindowSize <= 0 {
		policy.SlowWindowSize = 10
	}
	if policy.SlowRatioThreshold == 0 {
		policy.SlowRatioThreshold = 0.5
	}
	if policy.SlowRecoverySeconds == nil {
		recovery := 60
		policy.SlowRecoverySeconds = &recovery
	}
	if policy.SoftCooldownMultiplier == 0 {
		policy.SoftCooldownMultiplier = 2
	}
	if policy.CooldownBackoffFactor == 0 {
		policy.CooldownBackoffFactor = 2
	}
	if policy.CooldownMaxSeconds <= 0 {
		policy.CooldownMaxSeconds = 600
	}
	if policy.CooldownMaxSeconds < policy.CooldownSeconds {
		policy.CooldownMaxSeconds = policy.CooldownSeconds
	}
	if policy.HalfOpenProbe == nil {
		halfOpen := true
		policy.HalfOpenProbe = &halfOpen
	}
	return policy
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
	ht.recordOutcomeForStateKey(key, OutcomeHardFail, 0, failureMsg, nil, now)
}

// RecordFailureForRule records a failure for a target scoped to a specific routing rule.
func (ht *HealthTracker) RecordFailureForRule(ruleID, targetKey, failureMsg string, now time.Time) {
	ht.recordOutcomeForStateKey(scopedTargetKey(ruleID, targetKey), OutcomeHardFail, 0, failureMsg, nil, now)
}

// RecordSuccess records a successful request, resetting the consecutive failure counter.
// This enables the consecutive-failure trigger to distinguish persistent outages from transient errors.
func (ht *HealthTracker) RecordSuccess(key string) {
	ht.recordOutcomeForStateKey(key, OutcomeSuccess, 0, "", nil, time.Now())
}

// RecordSuccessForRule records a success for a target scoped to a specific routing rule.
func (ht *HealthTracker) RecordSuccessForRule(ruleID, targetKey string) {
	ht.recordOutcomeForStateKey(scopedTargetKey(ruleID, targetKey), OutcomeSuccess, 0, "", nil, time.Now())
}

func (ht *HealthTracker) RecordOutcome(ruleID, targetKey string, kind OutcomeKind, latency time.Duration, failureMsg string, policy *configstoreTables.HealthPolicy, now time.Time) {
	ht.recordOutcomeForStateKey(scopedTargetKey(ruleID, targetKey), kind, latency, failureMsg, policy, now)
}

func (ht *HealthTracker) recordOutcomeForStateKey(stateKey string, kind OutcomeKind, latency time.Duration, failureMsg string, policy *configstoreTables.HealthPolicy, now time.Time) {
	if stateKey == "" {
		return
	}
	policy = ApplyHealthPolicyDefaults(policy)
	s := ht.getOrCreate(stateKey)
	s.mu.Lock()
	defer s.mu.Unlock()

	s.lastOutcomeKind = kind
	appendLatencySampleLocked(s, latencySample{at: now, latency: latency, kind: kind}, policy)

	switch kind {
	case OutcomeSuccess:
		resetCooldownLocked(s)
		s.cooldownStreak = 0
	case OutcomeSlow:
		s.consecutiveFailures = 0
		s.lastSlowAt = now
		s.lastSlowLatency = latency
		if s.halfOpenInFlight {
			s.lastFailureTime = now
			if failureMsg == "" {
				failureMsg = "half-open probe was slow"
			}
			s.lastFailureMsg = failureMsg
			s.failures = append(s.failures, now)
			s.consecutiveFailures = 1
			startCooldownLocked(s, policy, now, kind)
			return
		}
	case OutcomeSoftFail, OutcomeHardFail:
		s.lastFailureTime = now
		s.lastFailureMsg = failureMsg
		s.failures = append(s.failures, now)
		s.consecutiveFailures++
		if s.halfOpenInFlight {
			startCooldownLocked(s, policy, now, kind)
		}
	}
	s.halfOpenInFlight = false
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
	policy = ApplyHealthPolicyDefaults(policy)
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

func (ht *HealthTracker) GetTargetHealth(key string, policy *configstoreTables.HealthPolicy, now time.Time) HealthLevel {
	return ht.getTargetHealth(key, policy, now)
}

func (ht *HealthTracker) GetTargetHealthForRule(ruleID, targetKey string, policy *configstoreTables.HealthPolicy, now time.Time) HealthLevel {
	return ht.getTargetHealth(scopedTargetKey(ruleID, targetKey), policy, now)
}

func (ht *HealthTracker) GetTargetRoutingHealthForRule(ruleID, targetKey string, policy *configstoreTables.HealthPolicy, now time.Time) (HealthLevel, bool) {
	return ht.getTargetRoutingHealth(scopedTargetKey(ruleID, targetKey), policy, now)
}

func (ht *HealthTracker) getTargetHealth(stateKey string, policy *configstoreTables.HealthPolicy, now time.Time) HealthLevel {
	policy = ApplyHealthPolicyDefaults(policy)
	ht.mu.RLock()
	s, ok := ht.targets[stateKey]
	ht.mu.RUnlock()
	if !ok {
		return HealthHealthy
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if evaluateCooldownLocked(s, policy, now) {
		return HealthCooldown
	}
	if isDegradedLocked(s, policy, now) {
		return HealthDegraded
	}
	return HealthHealthy
}

func (ht *HealthTracker) getTargetRoutingHealth(stateKey string, policy *configstoreTables.HealthPolicy, now time.Time) (HealthLevel, bool) {
	policy = ApplyHealthPolicyDefaults(policy)
	ht.mu.RLock()
	s, ok := ht.targets[stateKey]
	ht.mu.RUnlock()
	if !ok {
		return HealthHealthy, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.cooldownUntil.IsZero() {
		if now.Before(s.cooldownUntil) {
			return HealthCooldown, false
		}
		if shouldHalfOpenProbeLocked(s, policy, now) {
			return HealthCooldown, true
		}
		if s.halfOpenInFlight {
			return HealthCooldown, false
		}
		resetCooldownLocked(s)
		if isDegradedLocked(s, policy, now) {
			return HealthDegraded, false
		}
		return HealthHealthy, false
	}

	if evaluateCooldownLocked(s, policy, now) {
		return HealthCooldown, false
	}
	if isDegradedLocked(s, policy, now) {
		return HealthDegraded, false
	}
	return HealthHealthy, false
}

// TargetHealthSnapshot is a point-in-time view of a target's health state
type TargetHealthSnapshot struct {
	Key                     string  `json:"key"`
	Status                  string  `json:"status"` // "available" | "cooldown"
	HealthLevel             string  `json:"health_level"`
	FailureCount            int     `json:"failure_count"`
	ConsecutiveFailures     int     `json:"consecutive_failures"`
	SlowCount               int     `json:"slow_count"`
	SampleCount             int     `json:"sample_count"`
	SlowRatio               float64 `json:"slow_ratio"`
	P95LatencyMs            *int64  `json:"p95_latency_ms,omitempty"`
	CooldownStreak          int     `json:"cooldown_streak"`
	LastSlowAt              *string `json:"last_slow_at,omitempty"`
	LastSlowLatencyMs       *int64  `json:"last_slow_latency_ms,omitempty"`
	LastOutcomeKind         string  `json:"last_outcome_kind,omitempty"`
	HalfOpenInFlight        bool    `json:"half_open_in_flight,omitempty"`
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
	policy = ApplyHealthPolicyDefaults(policy)
	observation := ht.GetObservation(displayKey)

	ht.mu.RLock()
	s, ok := ht.targets[stateKey]
	ht.mu.RUnlock()

	if !ok {
		snap := TargetHealthSnapshot{Key: displayKey, Status: "available", HealthLevel: string(HealthHealthy)}
		applyObservationSnapshot(&snap, observation)
		return snap
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	healthLevel := HealthHealthy
	if evaluateCooldownLocked(s, policy, now) {
		healthLevel = HealthCooldown
	} else if isDegradedLocked(s, policy, now) {
		healthLevel = HealthDegraded
	}

	slowCount, sampleCount, slowRatio := sampleStatsLocked(s, policy)
	p95LatencyMs := p95LatencyMsLocked(s)

	snap := TargetHealthSnapshot{
		Key:                 displayKey,
		HealthLevel:         string(healthLevel),
		FailureCount:        len(s.failures),
		ConsecutiveFailures: s.consecutiveFailures,
		SlowCount:           slowCount,
		SampleCount:         sampleCount,
		SlowRatio:           slowRatio,
		P95LatencyMs:        p95LatencyMs,
		CooldownStreak:      s.cooldownStreak,
		LastOutcomeKind:     string(s.lastOutcomeKind),
		HalfOpenInFlight:    s.halfOpenInFlight,
	}

	if healthLevel == HealthCooldown && !s.cooldownUntil.IsZero() {
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
	if !s.lastSlowAt.IsZero() {
		lsa := s.lastSlowAt.UTC().Format(time.RFC3339)
		snap.LastSlowAt = &lsa
		latencyMs := s.lastSlowLatency.Milliseconds()
		snap.LastSlowLatencyMs = &latencyMs
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

func appendLatencySampleLocked(s *TargetHealthState, sample latencySample, policy *configstoreTables.HealthPolicy) {
	maxSamples := policy.SlowWindowSize
	if maxSamples < 20 {
		maxSamples = 20
	}
	s.recentSamples = append(s.recentSamples, sample)
	for len(s.recentSamples) > maxSamples {
		removed := s.recentSamples[0]
		s.recentSamples = s.recentSamples[1:]
		decrementSampleCountLocked(s, removed.kind)
	}
	incrementSampleCountLocked(s, sample.kind)
}

func incrementSampleCountLocked(s *TargetHealthState, kind OutcomeKind) {
	switch kind {
	case OutcomeSlow:
		s.slowCount++
	case OutcomeSoftFail:
		s.softFailCount++
	case OutcomeHardFail:
		s.hardFailCount++
	}
}

func decrementSampleCountLocked(s *TargetHealthState, kind OutcomeKind) {
	switch kind {
	case OutcomeSlow:
		if s.slowCount > 0 {
			s.slowCount--
		}
	case OutcomeSoftFail:
		if s.softFailCount > 0 {
			s.softFailCount--
		}
	case OutcomeHardFail:
		if s.hardFailCount > 0 {
			s.hardFailCount--
		}
	}
}

func sampleStatsLocked(s *TargetHealthState, policy *configstoreTables.HealthPolicy) (int, int, float64) {
	windowSize := policy.SlowWindowSize
	if windowSize <= 0 {
		windowSize = 10
	}
	considered := 0
	slow := 0
	for i := len(s.recentSamples) - 1; i >= 0 && considered < windowSize; i-- {
		considered++
		if s.recentSamples[i].kind == OutcomeSlow {
			slow++
		}
	}
	if considered == 0 {
		return 0, 0, 0
	}
	return slow, considered, float64(slow) / float64(considered)
}

func p95LatencyMsLocked(s *TargetHealthState) *int64 {
	if len(s.recentSamples) == 0 {
		return nil
	}
	values := make([]int64, 0, len(s.recentSamples))
	for _, sample := range s.recentSamples {
		if sample.latency > 0 {
			values = append(values, sample.latency.Milliseconds())
		}
	}
	if len(values) == 0 {
		return nil
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	idx := int(math.Ceil(float64(len(values))*0.95)) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(values) {
		idx = len(values) - 1
	}
	v := values[idx]
	return &v
}

func isDegradedLocked(s *TargetHealthState, policy *configstoreTables.HealthPolicy, now time.Time) bool {
	windowSize := policy.SlowWindowSize
	if windowSize <= 0 {
		windowSize = 10
	}
	if len(s.recentSamples) >= windowSize/2 {
		_, considered, ratio := sampleStatsLocked(s, policy)
		if considered > 0 && policy.SlowRatioThreshold > 0 && ratio >= policy.SlowRatioThreshold {
			return true
		}
	}
	recoverySeconds := 60
	if policy.SlowRecoverySeconds != nil {
		recoverySeconds = *policy.SlowRecoverySeconds
	}
	if recoverySeconds > 0 && !s.lastSlowAt.IsZero() && now.Sub(s.lastSlowAt) < time.Duration(recoverySeconds)*time.Second {
		return true
	}
	return false
}

func evaluateCooldownLocked(s *TargetHealthState, policy *configstoreTables.HealthPolicy, now time.Time) bool {
	policy = ApplyHealthPolicyDefaults(policy)
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
	if !startCooldownLocked(s, policy, cooldownStart, s.lastOutcomeKind) || !now.Before(s.cooldownUntil) {
		resetCooldownLocked(s)
		return false
	}
	return true
}

func shouldHalfOpenProbeLocked(s *TargetHealthState, policy *configstoreTables.HealthPolicy, now time.Time) bool {
	if policy.HalfOpenProbe != nil && !*policy.HalfOpenProbe {
		return false
	}
	if s.cooldownUntil.IsZero() || now.Before(s.cooldownUntil) {
		return false
	}
	if s.halfOpenInFlight && now.Sub(s.halfOpenStartedAt) < 30*time.Second {
		return false
	}
	s.halfOpenInFlight = true
	s.halfOpenStartedAt = now
	return true
}

func startCooldownLocked(s *TargetHealthState, policy *configstoreTables.HealthPolicy, cooldownStart time.Time, kind OutcomeKind) bool {
	if cooldownStart.IsZero() {
		return false
	}
	nextStreak := s.cooldownStreak + 1
	multiplier := 1.0
	if kind == OutcomeSoftFail {
		multiplier = policy.SoftCooldownMultiplier
	}
	if multiplier < 1 {
		multiplier = 1
	}
	backoff := 1.0
	if policy.CooldownBackoffFactor > 1 && nextStreak > 1 {
		backoff = math.Pow(policy.CooldownBackoffFactor, float64(nextStreak-1))
	}
	durationSeconds := float64(policy.CooldownSeconds) * multiplier * backoff
	if maxSeconds := float64(policy.CooldownMaxSeconds); maxSeconds > 0 && durationSeconds > maxSeconds {
		durationSeconds = maxSeconds
	}
	s.cooldownStreak = nextStreak
	s.cooldownUntil = cooldownStart.Add(time.Duration(durationSeconds * float64(time.Second)))
	s.halfOpenInFlight = false
	s.halfOpenStartedAt = time.Time{}
	return true
}

func resetCooldownLocked(s *TargetHealthState) {
	s.cooldownUntil = time.Time{}
	s.failures = s.failures[:0]
	s.consecutiveFailures = 0
	s.lastFailureTime = time.Time{}
	s.lastFailureMsg = ""
	s.halfOpenInFlight = false
	s.halfOpenStartedAt = time.Time{}
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
