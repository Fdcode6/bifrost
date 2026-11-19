package governance

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
)

const (
	halfOpenProbeLeaseDuration   = 65 * time.Second
	adaptiveColdStartLeaseWindow = 45 * time.Second
	adaptivePriorSuccess         = 3.4
	adaptivePriorFail            = 0.6
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
	rescueWindows            [2][10]rescueMinute
	mu                       sync.Mutex
	failures                 []time.Time // timestamps of recent failures
	consecutiveFailures      int         // consecutive failures without any success
	fatalConsecutiveFailures int         // consecutive fatal channel failures (401/402/403/404/insufficient balance/no channel)
	cooldownUntil            time.Time   // zero value means not in cooldown
	lastFailureTime          time.Time
	lastFailureMsg           string
	recentSamples            []latencySample
	slowCount                int
	softFailCount            int
	hardFailCount            int
	fatalFailCount           int
	lastSlowAt               time.Time
	lastSlowLatency          time.Duration
	cooldownStreak           int
	cooldownReason           OutcomeKind
	halfOpenInFlight         bool
	halfOpenStartedAt        time.Time
	activeProbeWakeup        bool
	lastOutcomeKind          OutcomeKind
	lastOutcomeAt            time.Time

	// V2 adaptive decaying reputation & probation state
	repWeightedSuccess      float64
	repWeightedFail         float64
	repUpdatedAt            time.Time
	totalSuccesses          int
	inFlightCount           int
	inFlightStartedAt       time.Time
	consecutiveSlowFail     int
	shortTripwireUntil      time.Time
	fatalUntil              time.Time
	probationNextProbeAt    time.Time
	probationFailStreak     int
	probationConsecSuccess  int
	probationProbeInFlight  bool
	probationProbeStartedAt time.Time
	successLatencies        []latencySample
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

// TargetRoutingState captures a read-only snapshot of routing health for decision building
// without prematurely claiming a half-open probe slot.
type TargetRoutingState struct {
	Level            HealthLevel
	HalfOpenEligible bool
	CooldownUntil    time.Time
	CooldownReason   OutcomeKind
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
// Failure recording is decoupled from cooldown evaluation for transient failures, while fatal
// channel errors (e.g. banned/insufficient balance/invalid token) trigger rapid cooldown once
// fatal_consecutive_failures is reached.
type HealthTracker struct {
	mu         sync.RWMutex
	targets    map[string]*TargetHealthState   // key = TargetKey or ruleID::TargetKey
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
	} else {
		// Rules may be shared with in-flight requests during a hot reload.
		copy := *policy
		policy = &copy
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
	if policy.FatalConsecutiveFailures <= 0 {
		policy.FatalConsecutiveFailures = 2
	}
	if policy.FatalCooldownSeconds <= 0 {
		policy.FatalCooldownSeconds = 120
	}
	if policy.ErrorRateWindowSize > 0 && policy.ErrorRateMinSamples <= 0 {
		policy.ErrorRateMinSamples = 5
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
	if policy.EnableUpstreamDiversity == nil {
		diversify := true
		policy.EnableUpstreamDiversity = &diversify
	}
	if policy.ActiveProbeWakeup == nil {
		wakeup := false
		policy.ActiveProbeWakeup = &wakeup
	}
	if policy.ReputationWindowSeconds <= 0 {
		policy.ReputationWindowSeconds = 5400
	}
	if policy.MinQualifiedSuccessRate <= 0 {
		policy.MinQualifiedSuccessRate = 0.70
	}
	if policy.MinActiveTargets <= 0 {
		policy.MinActiveTargets = 3
	}
	if policy.PrimaryAttemptTimeoutMs <= 0 {
		policy.PrimaryAttemptTimeoutMs = 38000
	}
	if policy.FastRescueElapsedMs <= 0 {
		policy.FastRescueElapsedMs = 15000
	}
	if policy.FastChannelThresholdMs <= 0 {
		policy.FastChannelThresholdMs = 16000
	}
	if policy.RescueAttemptTimeoutMs <= 0 {
		policy.RescueAttemptTimeoutMs = 20000
	}
	if policy.ProbationProbeBaseSeconds <= 0 {
		policy.ProbationProbeBaseSeconds = 300
	}
	if policy.ProbationProbeMaxSeconds <= 0 {
		policy.ProbationProbeMaxSeconds = 900
	}
	if policy.ProbationProbeMaxSeconds < policy.ProbationProbeBaseSeconds {
		policy.ProbationProbeMaxSeconds = policy.ProbationProbeBaseSeconds
	}
	if policy.ProbationRecoverSuccesses <= 0 {
		policy.ProbationRecoverSuccesses = 2
	}
	return policy
}

func isAdaptivePolicy(policy *configstoreTables.HealthPolicy) bool {
	return policy != nil && policy.AdaptiveRankingEnabled != nil && *policy.AdaptiveRankingEnabled
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

	releaseAdaptiveInFlightLocked(s)

	if kind == OutcomeIgnore {
		// Client cancellations / pure client 4xx must neither penalize nor clear health state.
		// If this request held the half-open probe lease, release it so another request can probe.
		if s.halfOpenInFlight {
			s.halfOpenInFlight = false
			s.halfOpenStartedAt = time.Time{}
		}
		return
	}

	s.lastOutcomeKind = kind
	s.lastOutcomeAt = now
	updateAdaptiveReputationLocked(s, kind, latency, failureMsg, policy, now)

	switch kind {
	case OutcomeSuccess:
		wasInCooldown := !s.cooldownUntil.IsZero() || s.halfOpenInFlight
		expiredReason := s.cooldownReason
		resetCooldownLocked(s)
		if wasInCooldown {
			resetFailureSamplesLocked(s)
		}
		if expiredReason == OutcomeSlow {
			resetSlowSamplesLocked(s)
		}
		s.cooldownStreak = 0
		appendLatencySampleLocked(s, latencySample{at: now, latency: latency, kind: kind}, policy)
	case OutcomeSlow:
		wasInCooldown := !s.cooldownUntil.IsZero() || s.halfOpenInFlight
		s.consecutiveFailures = 0
		s.fatalConsecutiveFailures = 0
		s.lastSlowAt = now
		s.lastSlowLatency = latency
		if wasInCooldown && (s.halfOpenInFlight || !now.Before(s.cooldownUntil)) {
			// A slow success during half-open proves the channel is alive again; exit cooldown
			// and let ratio-based degradation rank it behind faster healthy channels.
			resetCooldownLocked(s)
			resetFailureSamplesLocked(s)
			s.cooldownStreak = 0
		}
		appendLatencySampleLocked(s, latencySample{at: now, latency: latency, kind: kind}, policy)
	case OutcomeFatalChannel:
		appendLatencySampleLocked(s, latencySample{at: now, latency: latency, kind: kind}, policy)
		s.lastFailureTime = now
		s.lastFailureMsg = failureMsg
		s.failures = append(s.failures, now)
		s.consecutiveFailures++
		s.fatalConsecutiveFailures++
		if s.halfOpenInFlight || (!s.cooldownUntil.IsZero() && !now.Before(s.cooldownUntil)) {
			startCooldownLocked(s, policy, now, OutcomeFatalChannel)
			return
		}
		if s.fatalConsecutiveFailures >= policy.FatalConsecutiveFailures {
			startCooldownLocked(s, policy, now, OutcomeFatalChannel)
			return
		}
	case OutcomeSoftFail, OutcomeHardFail:
		appendLatencySampleLocked(s, latencySample{at: now, latency: latency, kind: kind}, policy)
		s.lastFailureTime = now
		s.lastFailureMsg = failureMsg
		s.failures = append(s.failures, now)
		s.consecutiveFailures++
		s.fatalConsecutiveFailures = 0
		if s.halfOpenInFlight || (!s.cooldownUntil.IsZero() && !now.Before(s.cooldownUntil)) {
			startCooldownLocked(s, policy, now, kind)
			return
		}
	}
	s.halfOpenInFlight = false
	s.halfOpenStartedAt = time.Time{}
}

// WakeTargetForHalfOpenProbeForRule accelerates a cooled target so the next real request
// may immediately send a single in-place half-open verification probe without clearing
// cooldown outright.
func (ht *HealthTracker) WakeTargetForHalfOpenProbeForRule(ruleID, targetKey string, now time.Time) {
	stateKey := scopedTargetKey(ruleID, targetKey)
	if stateKey == "" {
		return
	}
	ht.mu.RLock()
	s, ok := ht.targets[stateKey]
	ht.mu.RUnlock()
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.cooldownUntil.IsZero() && now.Before(s.cooldownUntil) && !s.halfOpenInFlight {
		s.activeProbeWakeup = true
	}
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

// InspectTargetRoutingStateForRule evaluates routing health without mutating halfOpenInFlight.
func (ht *HealthTracker) InspectTargetRoutingStateForRule(ruleID, targetKey string, policy *configstoreTables.HealthPolicy, now time.Time) TargetRoutingState {
	return ht.inspectTargetRoutingState(scopedTargetKey(ruleID, targetKey), policy, now)
}

// TryClaimHalfOpenProbeForRule atomically claims the single in-flight half-open probe lease
// for a cooled target whose cooldown has expired (or was woken by an active probe).
func (ht *HealthTracker) TryClaimHalfOpenProbeForRule(ruleID, targetKey string, policy *configstoreTables.HealthPolicy, now time.Time) bool {
	return ht.tryClaimHalfOpenProbe(scopedTargetKey(ruleID, targetKey), policy, now)
}

// GetTargetRoutingHealthForRule inspects routing health and claims the half-open slot if eligible.
func (ht *HealthTracker) GetTargetRoutingHealthForRule(ruleID, targetKey string, policy *configstoreTables.HealthPolicy, now time.Time) (HealthLevel, bool) {
	stateKey := scopedTargetKey(ruleID, targetKey)
	state := ht.inspectTargetRoutingState(stateKey, policy, now)
	if state.Level == HealthCooldown && state.HalfOpenEligible {
		if ht.tryClaimHalfOpenProbe(stateKey, policy, now) {
			return HealthCooldown, true
		}
		return HealthCooldown, false
	}
	return state.Level, false
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

	if isAdaptivePolicy(policy) {
		return evaluateAdaptiveHealthLevelLocked(s, policy, now)
	}
	if evaluateCooldownLocked(s, policy, now) {
		return HealthCooldown
	}
	if isDegradedLocked(s, policy, now) {
		return HealthDegraded
	}
	return HealthHealthy
}

func (ht *HealthTracker) inspectTargetRoutingState(stateKey string, policy *configstoreTables.HealthPolicy, now time.Time) TargetRoutingState {
	policy = ApplyHealthPolicyDefaults(policy)
	ht.mu.RLock()
	s, ok := ht.targets[stateKey]
	ht.mu.RUnlock()
	if !ok {
		return TargetRoutingState{Level: HealthHealthy}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.cooldownUntil.IsZero() {
		if now.Before(s.cooldownUntil) && !s.activeProbeWakeup {
			return TargetRoutingState{
				Level:            HealthCooldown,
				HalfOpenEligible: false,
				CooldownUntil:    s.cooldownUntil,
				CooldownReason:   s.cooldownReason,
			}
		}
		// Cooldown expired or active-probe woken.
		if policy.HalfOpenProbe != nil && !*policy.HalfOpenProbe {
			expiredReason := s.cooldownReason
			resetCooldownLocked(s)
			resetFailureSamplesLocked(s)
			if expiredReason == OutcomeSlow {
				resetSlowSamplesLocked(s)
			}
			if isDegradedLocked(s, policy, now) {
				return TargetRoutingState{Level: HealthDegraded}
			}
			return TargetRoutingState{Level: HealthHealthy}
		}
		eligible := !s.halfOpenInFlight || now.Sub(s.halfOpenStartedAt) >= halfOpenProbeLeaseDuration
		return TargetRoutingState{
			Level:            HealthCooldown,
			HalfOpenEligible: eligible,
			CooldownUntil:    s.cooldownUntil,
			CooldownReason:   s.cooldownReason,
		}
	}

	if evaluateCooldownLocked(s, policy, now) {
		return TargetRoutingState{
			Level:            HealthCooldown,
			HalfOpenEligible: false,
			CooldownUntil:    s.cooldownUntil,
			CooldownReason:   s.cooldownReason,
		}
	}
	if isDegradedLocked(s, policy, now) {
		return TargetRoutingState{Level: HealthDegraded}
	}
	return TargetRoutingState{Level: HealthHealthy}
}

func (ht *HealthTracker) tryClaimHalfOpenProbe(stateKey string, policy *configstoreTables.HealthPolicy, now time.Time) bool {
	policy = ApplyHealthPolicyDefaults(policy)
	if policy.HalfOpenProbe != nil && !*policy.HalfOpenProbe {
		return false
	}
	ht.mu.RLock()
	s, ok := ht.targets[stateKey]
	ht.mu.RUnlock()
	if !ok {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	return shouldHalfOpenProbeLocked(s, policy, now)
}

// TargetHealthSnapshot is a point-in-time view of a target's health state
type TargetHealthSnapshot struct {
	Key                      string  `json:"key"`
	Status                   string  `json:"status"` // "available" | "cooldown"
	HealthLevel              string  `json:"health_level"`
	FailureCount             int     `json:"failure_count"`
	ConsecutiveFailures      int     `json:"consecutive_failures"`
	FatalConsecutiveFailures int     `json:"fatal_consecutive_failures,omitempty"`
	ErrorRate                float64 `json:"error_rate,omitempty"`
	SlowCount                int     `json:"slow_count"`
	SampleCount              int     `json:"sample_count"`
	SlowRatio                float64 `json:"slow_ratio"`
	P95LatencyMs             *int64  `json:"p95_latency_ms,omitempty"`
	CooldownStreak           int     `json:"cooldown_streak"`
	CooldownReason           string  `json:"cooldown_reason,omitempty"`
	LastSlowAt               *string `json:"last_slow_at,omitempty"`
	LastSlowLatencyMs        *int64  `json:"last_slow_latency_ms,omitempty"`
	LastOutcomeKind          string  `json:"last_outcome_kind,omitempty"`
	HalfOpenInFlight         bool    `json:"half_open_in_flight,omitempty"`
	CooldownUntil            *string `json:"cooldown_until,omitempty"`
	LastFailureTime          *string `json:"last_failure_time,omitempty"`
	LastFailureMsg           string  `json:"last_failure_msg,omitempty"`
	LastObservedAt           *string `json:"last_observed_at,omitempty"`
	LastObservedRequestType  string  `json:"last_observed_request_type,omitempty"`
	LastObservationSource    string  `json:"last_observation_source,omitempty"`
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
	var effectiveCooldownUntil time.Time
	if isAdaptivePolicy(policy) {
		healthLevel = evaluateAdaptiveHealthLevelLocked(s, policy, now)
		if !s.fatalUntil.IsZero() && now.Before(s.fatalUntil) {
			effectiveCooldownUntil = s.fatalUntil
		} else if !s.probationNextProbeAt.IsZero() && now.Before(s.probationNextProbeAt) {
			effectiveCooldownUntil = s.probationNextProbeAt
		} else if !s.shortTripwireUntil.IsZero() && now.Before(s.shortTripwireUntil) {
			effectiveCooldownUntil = s.shortTripwireUntil
		}
	} else {
		if evaluateCooldownLocked(s, policy, now) {
			healthLevel = HealthCooldown
			effectiveCooldownUntil = s.cooldownUntil
		} else if isDegradedLocked(s, policy, now) {
			healthLevel = HealthDegraded
		}
	}

	slowCount, sampleCount, slowRatio := sampleStatsLocked(s, policy)
	_, _, errorRate := errorRateStatsLocked(s, policy, now)
	if isAdaptivePolicy(policy) {
		errorRate = math.Max(0, 1.0-adaptiveEffectiveSuccessRateLocked(s, policy, now))
	}
	p95LatencyMs := p95LatencyMsLocked(s)

	snap := TargetHealthSnapshot{
		Key:                      displayKey,
		HealthLevel:              string(healthLevel),
		FailureCount:             len(s.failures),
		ConsecutiveFailures:      s.consecutiveFailures,
		FatalConsecutiveFailures: s.fatalConsecutiveFailures,
		ErrorRate:                errorRate,
		SlowCount:                slowCount,
		SampleCount:              sampleCount,
		SlowRatio:                slowRatio,
		P95LatencyMs:             p95LatencyMs,
		CooldownStreak:           s.cooldownStreak,
		CooldownReason:           string(s.cooldownReason),
		LastOutcomeKind:          string(s.lastOutcomeKind),
		HalfOpenInFlight:         (s.halfOpenInFlight && now.Sub(s.halfOpenStartedAt) < halfOpenProbeLeaseDuration) || (s.probationProbeInFlight && now.Sub(s.probationProbeStartedAt) < adaptiveColdStartLeaseWindow),
	}

	if healthLevel == HealthCooldown && !effectiveCooldownUntil.IsZero() {
		snap.Status = "cooldown"
		cu := effectiveCooldownUntil.UTC().Format(time.RFC3339)
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
	maxSamples := 20
	if policy != nil {
		if policy.SlowWindowSize > maxSamples {
			maxSamples = policy.SlowWindowSize
		}
		if policy.ErrorRateWindowSize > maxSamples {
			maxSamples = policy.ErrorRateWindowSize
		}
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
	case OutcomeFatalChannel:
		s.fatalFailCount++
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
	case OutcomeFatalChannel:
		if s.fatalFailCount > 0 {
			s.fatalFailCount--
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

func isFailureOutcome(kind OutcomeKind) bool {
	return kind == OutcomeSoftFail || kind == OutcomeHardFail || kind == OutcomeFatalChannel
}

func errorRateStatsLocked(s *TargetHealthState, policy *configstoreTables.HealthPolicy, now time.Time) (int, int, float64) {
	windowSize := policy.ErrorRateWindowSize
	if windowSize <= 0 {
		windowSize = len(s.recentSamples)
	}
	if windowSize <= 0 {
		return 0, 0, 0
	}
	var cutoff time.Time
	if policy.FailureWindowSeconds > 0 {
		cutoff = now.Add(-time.Duration(policy.FailureWindowSeconds) * time.Second)
	}
	considered := 0
	failed := 0
	for i := len(s.recentSamples) - 1; i >= 0 && considered < windowSize; i-- {
		sample := s.recentSamples[i]
		if !cutoff.IsZero() && sample.at.Before(cutoff) {
			break
		}
		considered++
		if isFailureOutcome(sample.kind) {
			failed++
		}
	}
	if considered == 0 {
		return 0, 0, 0
	}
	return failed, considered, float64(failed) / float64(considered)
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

// isDegradedLocked reports whether a target is degraded due to sustained slow responses.
// Slow responses never trigger hard cooldown; they only demote the target behind fast healthy channels
// and auto-expire once SlowRecoverySeconds elapse without new slow responses.
func isDegradedLocked(s *TargetHealthState, policy *configstoreTables.HealthPolicy, now time.Time) bool {
	if policy.SlowRatioThreshold > 1 {
		return false
	}
	recoverySeconds := 60
	if policy.SlowRecoverySeconds != nil {
		recoverySeconds = *policy.SlowRecoverySeconds
	}
	if recoverySeconds > 0 && !s.lastSlowAt.IsZero() && now.Sub(s.lastSlowAt) >= time.Duration(recoverySeconds)*time.Second {
		resetSlowSamplesLocked(s)
		return false
	}

	windowSize := policy.SlowWindowSize
	if windowSize <= 0 {
		windowSize = 10
	}
	minSamples := windowSize / 2
	if minSamples < 1 {
		minSamples = 1
	}
	slow, considered, ratio := sampleStatsLocked(s, policy)
	if considered >= minSamples && slow > 0 && policy.SlowRatioThreshold > 0 && ratio >= policy.SlowRatioThreshold {
		return true
	}
	return false
}

func evaluateCooldownLocked(s *TargetHealthState, policy *configstoreTables.HealthPolicy, now time.Time) bool {
	policy = ApplyHealthPolicyDefaults(policy)
	if !s.cooldownUntil.IsZero() {
		if now.Before(s.cooldownUntil) && !s.activeProbeWakeup {
			return true
		}
		if policy.HalfOpenProbe != nil && !*policy.HalfOpenProbe {
			expiredReason := s.cooldownReason
			resetCooldownLocked(s)
			resetFailureSamplesLocked(s)
			if expiredReason == OutcomeSlow {
				resetSlowSamplesLocked(s)
			}
			return false
		}
		// Under HalfOpenProbe=true, preserve cooldownUntil so routing enforces a single
		// in-place verification request, while reporting active in-flight probes as cooled.
		if s.halfOpenInFlight && now.Sub(s.halfOpenStartedAt) < halfOpenProbeLeaseDuration {
			return true
		}
		return false
	}

	windowStart := now.Add(-time.Duration(policy.FailureWindowSeconds) * time.Second)
	pruned := s.failures[:0]
	for _, t := range s.failures {
		if t.After(windowStart) {
			pruned = append(pruned, t)
		}
	}
	s.failures = pruned

	fatalTriggered := s.fatalConsecutiveFailures >= policy.FatalConsecutiveFailures
	consecThreshold := policy.ConsecutiveFailures
	if consecThreshold <= 0 {
		consecThreshold = policy.FailureThreshold
	}
	consecutiveTriggered := s.consecutiveFailures >= consecThreshold

	windowTriggered := false
	if policy.ErrorRateWindowSize > 0 && policy.ErrorRateThreshold > 0 {
		minSamples := policy.ErrorRateMinSamples
		if minSamples <= 0 {
			minSamples = 5
		}
		_, considered, ratio := errorRateStatsLocked(s, policy, now)
		windowTriggered = considered >= minSamples && ratio >= policy.ErrorRateThreshold
	} else {
		windowTriggered = len(s.failures) >= policy.FailureThreshold
	}

	if fatalTriggered || windowTriggered || consecutiveTriggered {
		cooldownStart := s.lastFailureTime
		if cooldownStart.IsZero() {
			cooldownStart = now
		}
		reason := s.lastOutcomeKind
		if fatalTriggered {
			reason = OutcomeFatalChannel
		}
		if !startCooldownLocked(s, policy, cooldownStart, reason) || !now.Before(s.cooldownUntil) {
			resetCooldownLocked(s)
			return false
		}
		return true
	}

	return false
}

func shouldHalfOpenProbeLocked(s *TargetHealthState, policy *configstoreTables.HealthPolicy, now time.Time) bool {
	if policy.HalfOpenProbe != nil && !*policy.HalfOpenProbe {
		return false
	}
	if s.cooldownUntil.IsZero() {
		return false
	}
	if now.Before(s.cooldownUntil) && !s.activeProbeWakeup {
		return false
	}
	if s.halfOpenInFlight && now.Sub(s.halfOpenStartedAt) < halfOpenProbeLeaseDuration {
		return false
	}
	s.halfOpenInFlight = true
	s.halfOpenStartedAt = now
	s.activeProbeWakeup = false
	return true
}

func startCooldownLocked(s *TargetHealthState, policy *configstoreTables.HealthPolicy, cooldownStart time.Time, kind OutcomeKind) bool {
	if cooldownStart.IsZero() {
		return false
	}
	nextStreak := s.cooldownStreak + 1
	duration := cooldownDuration(policy, kind, nextStreak)
	if duration <= 0 {
		return false
	}
	s.cooldownStreak = nextStreak
	s.cooldownUntil = cooldownStart.Add(duration)
	s.cooldownReason = kind
	s.halfOpenInFlight = false
	s.halfOpenStartedAt = time.Time{}
	s.activeProbeWakeup = false
	return true
}

func cooldownDuration(policy *configstoreTables.HealthPolicy, kind OutcomeKind, nextStreak int) time.Duration {
	baseSeconds := float64(policy.CooldownSeconds)
	multiplier := 1.0
	if kind == OutcomeFatalChannel {
		if policy.FatalCooldownSeconds > 0 {
			baseSeconds = float64(policy.FatalCooldownSeconds)
		}
	} else if kind == OutcomeSoftFail {
		multiplier = policy.SoftCooldownMultiplier
	}
	if multiplier < 1 {
		multiplier = 1
	}
	backoff := 1.0
	if policy.CooldownBackoffFactor > 1 && nextStreak > 1 {
		backoff = math.Pow(policy.CooldownBackoffFactor, float64(nextStreak-1))
	}
	durationSeconds := baseSeconds * multiplier * backoff
	if maxSeconds := float64(policy.CooldownMaxSeconds); maxSeconds > 0 && durationSeconds > maxSeconds {
		durationSeconds = maxSeconds
	}
	return time.Duration(durationSeconds * float64(time.Second))
}

func resetCooldownLocked(s *TargetHealthState) {
	s.cooldownUntil = time.Time{}
	s.cooldownReason = ""
	s.failures = s.failures[:0]
	s.consecutiveFailures = 0
	s.fatalConsecutiveFailures = 0
	s.lastFailureTime = time.Time{}
	s.lastFailureMsg = ""
	s.halfOpenInFlight = false
	s.halfOpenStartedAt = time.Time{}
	s.activeProbeWakeup = false
}

func resetSlowSamplesLocked(s *TargetHealthState) {
	filtered := s.recentSamples[:0]
	for _, sample := range s.recentSamples {
		if sample.kind == OutcomeSlow {
			continue
		}
		filtered = append(filtered, sample)
	}
	s.recentSamples = filtered
	s.slowCount = 0
	s.lastSlowAt = time.Time{}
	s.lastSlowLatency = 0
}

func resetFailureSamplesLocked(s *TargetHealthState) {
	filtered := s.recentSamples[:0]
	for _, sample := range s.recentSamples {
		if isFailureOutcome(sample.kind) {
			continue
		}
		filtered = append(filtered, sample)
	}
	s.recentSamples = filtered
	s.softFailCount = 0
	s.hardFailCount = 0
	s.fatalFailCount = 0
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

// AdaptiveTargetSnapshot exposes read-only reputation and latency metrics for V2 adaptive routing.
type AdaptiveTargetSnapshot struct {
	EffectiveSuccessRate       float64
	TotalSuccesses             int
	P80LatencyMs               int64
	HasLatencySample           bool
	InShortTripwire            bool
	ShortTripwireUntil         time.Time
	InFatal                    bool
	FatalUntil                 time.Time
	InProbation                bool
	ProbationProbeDue          bool
	ProbationNextProbeAt       time.Time
	ProbationConsecSuccess     int
	ProbationProbeInFlight     bool
	ColdStartBusy              bool
	Rescue10mNonStreamAttempts int
	Rescue10mNonStreamFast     int
	Rescue10mStreamAttempts    int
	Rescue10mStreamFast        int
	LastOutcomeKind            OutcomeKind
	LastOutcomeAt              time.Time
	LastFailureMsg             string
	LastFailureTime            time.Time
}

// MarkAttemptInFlightForRule marks an in-flight real request on a target so cold-start
// channels (<2 successes) only take 1 concurrent verification call at a time.
func (ht *HealthTracker) MarkAttemptInFlightForRule(ruleID, targetKey string, now time.Time) {
	stateKey := scopedTargetKey(ruleID, targetKey)
	if stateKey == "" {
		return
	}
	s := ht.getOrCreate(stateKey)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inFlightCount > 0 && !s.inFlightStartedAt.IsZero() && now.Sub(s.inFlightStartedAt) >= adaptiveColdStartLeaseWindow {
		s.inFlightCount = 0
	}
	s.inFlightCount++
	s.inFlightStartedAt = now
}

// InspectAdaptiveTargetForRule returns the current V2 reputation snapshot without mutating probe leases.
func (ht *HealthTracker) InspectAdaptiveTargetForRule(ruleID, targetKey string, policy *configstoreTables.HealthPolicy, now time.Time) AdaptiveTargetSnapshot {
	policy = ApplyHealthPolicyDefaults(policy)
	stateKey := scopedTargetKey(ruleID, targetKey)
	ht.mu.RLock()
	s, ok := ht.targets[stateKey]
	ht.mu.RUnlock()
	if !ok {
		return AdaptiveTargetSnapshot{
			EffectiveSuccessRate: adaptivePriorSuccess / (adaptivePriorSuccess + adaptivePriorFail),
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	effRate := adaptiveEffectiveSuccessRateLocked(s, policy, now)
	p80Ms, hasLat := adaptiveP80LatencyMsLocked(s, policy, now)
	inFatal := !s.fatalUntil.IsZero() && now.Before(s.fatalUntil)
	inTripwire := !s.shortTripwireUntil.IsZero() && now.Before(s.shortTripwireUntil)
	inProbation := inFatal || effRate <= policy.MinQualifiedSuccessRate || !s.probationNextProbeAt.IsZero()

	probeLeaseBusy := s.probationProbeInFlight && !s.probationProbeStartedAt.IsZero() && now.Sub(s.probationProbeStartedAt) < adaptiveColdStartLeaseWindow
	probeDue := false
	if inProbation && !inFatal && !probeLeaseBusy {
		if s.probationNextProbeAt.IsZero() || !now.Before(s.probationNextProbeAt) {
			probeDue = true
		}
	}

	coldStartBusy := s.totalSuccesses < 2 && s.inFlightCount > 0 && !s.inFlightStartedAt.IsZero() && now.Sub(s.inFlightStartedAt) < adaptiveColdStartLeaseWindow

	minute := now.Unix() / 60
	var nonStreamAttempts, nonStreamFast, streamAttempts, streamFast int
	for _, b := range s.rescueWindows[0] {
		if b.minute <= minute && b.minute > minute-10 {
			nonStreamAttempts += b.attempts
			nonStreamFast += b.fast
		}
	}
	for _, b := range s.rescueWindows[1] {
		if b.minute <= minute && b.minute > minute-10 {
			streamAttempts += b.attempts
			streamFast += b.fast
		}
	}

	var shortTripwireUntil time.Time
	if inTripwire {
		shortTripwireUntil = s.shortTripwireUntil
	}
	var fatalUntil time.Time
	if inFatal {
		fatalUntil = s.fatalUntil
	}

	return AdaptiveTargetSnapshot{
		EffectiveSuccessRate:       effRate,
		TotalSuccesses:             s.totalSuccesses,
		P80LatencyMs:               p80Ms,
		HasLatencySample:           hasLat,
		InShortTripwire:            inTripwire,
		ShortTripwireUntil:         shortTripwireUntil,
		InFatal:                    inFatal,
		FatalUntil:                 fatalUntil,
		InProbation:                inProbation,
		ProbationProbeDue:          probeDue,
		ProbationNextProbeAt:       s.probationNextProbeAt,
		ProbationConsecSuccess:     s.probationConsecSuccess,
		ProbationProbeInFlight:     probeLeaseBusy,
		ColdStartBusy:              coldStartBusy,
		Rescue10mNonStreamAttempts: nonStreamAttempts,
		Rescue10mNonStreamFast:     nonStreamFast,
		Rescue10mStreamAttempts:    streamAttempts,
		Rescue10mStreamFast:        streamFast,
		LastOutcomeKind:            s.lastOutcomeKind,
		LastOutcomeAt:              s.lastOutcomeAt,
		LastFailureMsg:             s.lastFailureMsg,
		LastFailureTime:            s.lastFailureTime,
	}
}

// TryClaimProbationProbeForRule atomically claims the single-concurrency real-request probe slot
// for a probation target whose periodic check interval has elapsed.
func (ht *HealthTracker) TryClaimProbationProbeForRule(ruleID, targetKey string, policy *configstoreTables.HealthPolicy, now time.Time) bool {
	policy = ApplyHealthPolicyDefaults(policy)
	stateKey := scopedTargetKey(ruleID, targetKey)
	if stateKey == "" {
		return false
	}
	s := ht.getOrCreate(stateKey)
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.fatalUntil.IsZero() && now.Before(s.fatalUntil) {
		return false
	}
	if s.probationProbeInFlight && !s.probationProbeStartedAt.IsZero() && now.Sub(s.probationProbeStartedAt) < adaptiveColdStartLeaseWindow {
		return false
	}
	if !s.probationNextProbeAt.IsZero() && now.Before(s.probationNextProbeAt) {
		return false
	}
	s.probationProbeInFlight = true
	s.probationProbeStartedAt = now
	return true
}

func releaseAdaptiveInFlightLocked(s *TargetHealthState) {
	if s.inFlightCount > 0 {
		s.inFlightCount--
		if s.inFlightCount == 0 {
			s.inFlightStartedAt = time.Time{}
		}
	}
	s.probationProbeInFlight = false
	s.probationProbeStartedAt = time.Time{}
}

func decayAdaptiveWeightsLocked(s *TargetHealthState, policy *configstoreTables.HealthPolicy, now time.Time) {
	if s.repUpdatedAt.IsZero() {
		s.repUpdatedAt = now
		return
	}
	dt := now.Sub(s.repUpdatedAt).Seconds()
	if dt <= 0 {
		return
	}
	tau := float64(policy.ReputationWindowSeconds)
	if tau <= 0 {
		tau = 5400
	}
	factor := math.Exp(-dt / tau)
	s.repWeightedSuccess *= factor
	s.repWeightedFail *= factor
	s.repUpdatedAt = now
}

func adaptiveEffectiveSuccessRateLocked(s *TargetHealthState, policy *configstoreTables.HealthPolicy, now time.Time) float64 {
	decayAdaptiveWeightsLocked(s, policy, now)
	num := s.repWeightedSuccess + adaptivePriorSuccess
	den := s.repWeightedSuccess + s.repWeightedFail + adaptivePriorSuccess + adaptivePriorFail
	if den <= 0 {
		return 0.85
	}
	return num / den
}

func isTimeoutFailureMsg(msg string) bool {
	if msg == "" {
		return false
	}
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "timeout") ||
		strings.Contains(lower, "timed out") ||
		strings.Contains(lower, "deadline")
}

func scheduleProbationProbeLocked(s *TargetHealthState, policy *configstoreTables.HealthPolicy, now time.Time) {
	s.probationFailStreak++
	base := float64(policy.ProbationProbeBaseSeconds)
	if base <= 0 {
		base = 300
	}
	maxSec := float64(policy.ProbationProbeMaxSeconds)
	if maxSec < base {
		maxSec = base
	}
	sec := base * math.Pow(1.5, float64(max(0, s.probationFailStreak-1)))
	if sec > maxSec {
		sec = maxSec
	}
	s.probationNextProbeAt = now.Add(time.Duration(sec * float64(time.Second)))
}

func appendSuccessLatencyLocked(s *TargetHealthState, sample latencySample, policy *configstoreTables.HealthPolicy, now time.Time) {
	if sample.latency <= 0 {
		return
	}
	cutoff := now.Add(-time.Duration(policy.ReputationWindowSeconds) * time.Second)
	kept := s.successLatencies[:0]
	for _, item := range s.successLatencies {
		if item.at.After(cutoff) {
			kept = append(kept, item)
		}
	}
	kept = append(kept, sample)
	if len(kept) > 24 {
		kept = kept[len(kept)-24:]
	}
	s.successLatencies = kept
}

func adaptiveP80LatencyMsLocked(s *TargetHealthState, policy *configstoreTables.HealthPolicy, now time.Time) (int64, bool) {
	if len(s.successLatencies) == 0 {
		return 0, false
	}
	cutoff := now.Add(-time.Duration(policy.ReputationWindowSeconds) * time.Second)
	vals := make([]int64, 0, len(s.successLatencies))
	for _, item := range s.successLatencies {
		if item.latency > 0 && (cutoff.IsZero() || item.at.After(cutoff)) {
			vals = append(vals, item.latency.Milliseconds())
		}
	}
	if len(vals) == 0 {
		return 0, false
	}
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
	idx := int(math.Ceil(float64(len(vals))*0.80)) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(vals) {
		idx = len(vals) - 1
	}
	return vals[idx], true
}

func updateAdaptiveReputationLocked(s *TargetHealthState, kind OutcomeKind, latency time.Duration, failureMsg string, policy *configstoreTables.HealthPolicy, now time.Time) {
	prevRate := adaptiveEffectiveSuccessRateLocked(s, policy, now)
	wasProbation := (!s.fatalUntil.IsZero() && now.Before(s.fatalUntil)) ||
		prevRate <= policy.MinQualifiedSuccessRate ||
		!s.probationNextProbeAt.IsZero()

	switch kind {
	case OutcomeSuccess:
		s.totalSuccesses++
		s.repWeightedSuccess += 1.0
		s.consecutiveSlowFail = 0
		s.shortTripwireUntil = time.Time{}
		s.fatalUntil = time.Time{}
		appendSuccessLatencyLocked(s, latencySample{at: now, latency: latency, kind: kind}, policy, now)

		if wasProbation {
			s.probationConsecSuccess++
			s.probationFailStreak = 0
			if s.probationConsecSuccess >= policy.ProbationRecoverSuccesses {
				s.probationNextProbeAt = time.Time{}
				s.probationConsecSuccess = 0
				targetRate := math.Max(policy.MinQualifiedSuccessRate+0.12, 0.82)
				if targetRate > 0.92 {
					targetRate = 0.92
				}
				// Cap historical weighted failures so the recovered channel cleanly re-enters active pool.
				maxAllowedFail := ((s.repWeightedSuccess + adaptivePriorSuccess) * (1.0 - targetRate) / targetRate) - adaptivePriorFail
				if maxAllowedFail < 0 {
					maxAllowedFail = 0
				}
				if s.repWeightedFail > maxAllowedFail {
					s.repWeightedFail = maxAllowedFail
				}
			} else {
				// First verification probe succeeded — schedule quick follow-up check in 60s.
				s.probationNextProbeAt = now.Add(60 * time.Second)
			}
		} else {
			s.probationNextProbeAt = time.Time{}
			s.probationConsecSuccess = 0
			s.probationFailStreak = 0
		}

	case OutcomeSlow:
		s.totalSuccesses++
		s.repWeightedSuccess += 0.5
		s.repWeightedFail += 1.0
		s.probationConsecSuccess = 0
		appendSuccessLatencyLocked(s, latencySample{at: now, latency: latency, kind: kind}, policy, now)
		if adaptiveEffectiveSuccessRateLocked(s, policy, now) <= policy.MinQualifiedSuccessRate {
			scheduleProbationProbeLocked(s, policy, now)
		}

	case OutcomeFatalChannel:
		s.repWeightedFail += 6.0
		s.probationConsecSuccess = 0
		s.probationFailStreak++
		fatalSec := max(policy.FatalCooldownSeconds, policy.ProbationProbeMaxSeconds)
		s.fatalUntil = now.Add(time.Duration(fatalSec) * time.Second)
		s.probationNextProbeAt = s.fatalUntil

	case OutcomeSoftFail, OutcomeHardFail:
		nextConsec := s.consecutiveFailures + 1
		isFastTransient := kind == OutcomeSoftFail && latency < 3*time.Second && !isTimeoutFailureMsg(failureMsg)
		if isFastTransient {
			s.repWeightedFail += 0.35
			if nextConsec >= 5 {
				s.shortTripwireUntil = now.Add(60 * time.Second)
			}
		} else {
			s.repWeightedFail += 2.0
			s.consecutiveSlowFail++
			if s.consecutiveSlowFail >= 2 || nextConsec >= 4 {
				s.shortTripwireUntil = now.Add(60 * time.Second)
			}
		}
		s.probationConsecSuccess = 0
		if wasProbation || adaptiveEffectiveSuccessRateLocked(s, policy, now) <= policy.MinQualifiedSuccessRate {
			scheduleProbationProbeLocked(s, policy, now)
		}
	}
}

func evaluateAdaptiveHealthLevelLocked(s *TargetHealthState, policy *configstoreTables.HealthPolicy, now time.Time) HealthLevel {
	if !s.fatalUntil.IsZero() && now.Before(s.fatalUntil) {
		return HealthCooldown
	}
	effRate := adaptiveEffectiveSuccessRateLocked(s, policy, now)
	if effRate <= policy.MinQualifiedSuccessRate || !s.probationNextProbeAt.IsZero() {
		return HealthCooldown
	}
	if !s.shortTripwireUntil.IsZero() && now.Before(s.shortTripwireUntil) {
		return HealthDegraded
	}
	return HealthHealthy
}
