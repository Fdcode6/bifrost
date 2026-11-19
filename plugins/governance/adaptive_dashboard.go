package governance

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
)

const (
	adaptiveV22FinalHopReserveMs = 14000
)

// TargetPricingInfo captures effective per-1M-token USD pricing for a routing target.
type TargetPricingInfo struct {
	InputCostPer1MUSD  float64
	OutputCostPer1MUSD float64
	RawCost            float64
	HasPricing         bool
}

// TargetPricingResolver resolves effective catalog pricing for one target.
type TargetPricingResolver func(provider schemas.ModelProvider, model string, requestType schemas.RequestType) TargetPricingInfo

// TargetAvailabilityChecker reports whether a target is currently enabled and eligible for traffic.
type TargetAvailabilityChecker func(provider, model, keyID string) bool

// RescueWindowStats reports 10-minute rolling <=16s timely rescue observations.
type RescueWindowStats struct {
	Attempts int `json:"attempts"`
	Fast     int `json:"fast"`
}

// AdaptivePoolCounts summarizes live channel states across L1, L2, and L3 tiers.
type AdaptivePoolCounts struct {
	Total        int `json:"total"`
	L1Total      int `json:"l1_total"`
	L1Active     int `json:"l1_active"`
	L1Tripwire   int `json:"l1_tripwire"`
	L1Probation  int `json:"l1_probation"`
	L1Fatal      int `json:"l1_fatal"`
	L2Total      int `json:"l2_total"`
	L2Ready      int `json:"l2_ready"`
	L2Restricted int `json:"l2_restricted"`
	L3Total      int `json:"l3_total"`
	L3Ready      int `json:"l3_ready"`
	L3Restricted int `json:"l3_restricted"`
}

// AdaptiveRouteHopPreview describes one hop in the live next-request routing simulation.
type AdaptiveRouteHopPreview struct {
	TargetKey        string   `json:"target_key"`
	Provider         string   `json:"provider"`
	Model            string   `json:"model"`
	KeyID            string   `json:"key_id,omitempty"`
	Tier             string   `json:"tier"`
	OfficialCostPct  *float64 `json:"official_cost_pct,omitempty"`
	TimeoutMs        int      `json:"timeout_ms,omitempty"`
	IsFastRescue     bool     `json:"is_fast_rescue,omitempty"`
	IsLastResort     bool     `json:"is_last_resort,omitempty"`
	IsProbationProbe bool     `json:"is_probation_probe,omitempty"`
}

// AdaptiveRoutePreview simulates the next request's routing trajectory from live in-memory state.
type AdaptiveRoutePreview struct {
	Primary               *AdaptiveRouteHopPreview  `json:"primary,omitempty"`
	FastFailContinuation  *AdaptiveRouteHopPreview  `json:"fast_fail_continuation,omitempty"`
	TimeoutRescue         []AdaptiveRouteHopPreview `json:"timeout_rescue,omitempty"`
	FinalHopFallback      []AdaptiveRouteHopPreview `json:"final_hop_fallback,omitempty"`
	ProbationProbePending *AdaptiveRouteHopPreview  `json:"probation_probe_pending,omitempty"`
	FinalHopReserveMs     int                       `json:"final_hop_reserve_ms,omitempty"`
}

// AdaptiveDashboardTargetRuntime holds live in-memory health, ranking, pricing, and rescue telemetry
// for a single configured target in a grouped/adaptive routing rule.
type AdaptiveDashboardTargetRuntime struct {
	TargetKey                 string            `json:"target_key"`
	Provider                  string            `json:"provider"`
	Model                     string            `json:"model"`
	KeyID                     string            `json:"key_id,omitempty"`
	GroupName                 string            `json:"group_name"`
	GroupIndex                int               `json:"group_index"`
	Tier                      string            `json:"tier"` // "L1" | "L2" | "L3"
	FallbackOnly              bool              `json:"fallback_only"`
	LastResort                bool              `json:"last_resort"`
	Weight                    float64           `json:"weight"`
	AttemptTimeoutMs          int               `json:"attempt_timeout_ms,omitempty"`
	PrimaryAttemptTimeoutMs   int               `json:"primary_attempt_timeout_ms,omitempty"`
	InputCostPer1MUSD         *float64          `json:"input_cost_per_1m_usd,omitempty"`
	OutputCostPer1MUSD        *float64          `json:"output_cost_per_1m_usd,omitempty"`
	OfficialCostPct           *float64          `json:"official_cost_pct,omitempty"`
	Score                     float64           `json:"score"`
	RankLabel                 string            `json:"rank_label"`
	RankOrder                 int               `json:"rank_order"`
	IsPrimary                 bool              `json:"is_primary"`
	State                     string            `json:"state"` // "active" | "tripwire" | "probation" | "fatal" | "unavailable"
	IsFastRescue              bool              `json:"is_fast_rescue"`
	MinActivePromoted         bool              `json:"min_active_promoted,omitempty"`
	EffectiveSuccessRate      float64           `json:"effective_success_rate"`
	TotalSuccesses            int               `json:"total_successes"`
	P80LatencyMs              *int64            `json:"p80_latency_ms,omitempty"`
	ShortTripwireUntil        *time.Time        `json:"short_tripwire_until,omitempty"`
	FatalUntil                *time.Time        `json:"fatal_until,omitempty"`
	ProbationNextProbeAt      *time.Time        `json:"probation_next_probe_at,omitempty"`
	ProbationConsecSuccess    int               `json:"probation_consec_success"`
	ProbationRecoverSuccesses int               `json:"probation_recover_successes"`
	ProbationProbeInFlight    bool              `json:"probation_probe_in_flight,omitempty"`
	ProbationProbeDue         bool              `json:"probation_probe_due,omitempty"`
	Rescue10mNonStream        RescueWindowStats `json:"rescue_10m_non_stream"`
	Rescue10mStream           RescueWindowStats `json:"rescue_10m_stream"`
	Rescue10mTotal            RescueWindowStats `json:"rescue_10m_total"`
	LastOutcomeKind           string            `json:"last_outcome_kind,omitempty"`
	LastOutcomeAt             *time.Time        `json:"last_outcome_at,omitempty"`
	LastFailureMsg            string            `json:"last_failure_msg,omitempty"`
	LastFailureTime           *time.Time        `json:"last_failure_time,omitempty"`
	LastRealAccessAt          *time.Time        `json:"last_real_access_at,omitempty"`
	LegacyHealthLevel         string            `json:"legacy_health_level,omitempty"`
	LegacyCooldownUntil       *string           `json:"legacy_cooldown_until,omitempty"`
	LegacyConsecutiveFailures int               `json:"legacy_consecutive_failures,omitempty"`
}

// AdaptiveDashboardRuleRuntime captures the complete live runtime snapshot of one routing rule.
type AdaptiveDashboardRuleRuntime struct {
	RuleID                         string                           `json:"rule_id"`
	RuleName                       string                           `json:"rule_name"`
	Description                    string                           `json:"description,omitempty"`
	Enabled                        bool                             `json:"enabled"`
	EngineMode                     string                           `json:"engine_mode"` // "adaptive_v22" | "adaptive_v21" | "grouped"
	Policy                         *configstoreTables.HealthPolicy  `json:"policy"`
	EffectiveBudgetMs              int                              `json:"effective_budget_ms"`
	EffectiveMaxAttempts           int                              `json:"effective_max_attempts"`
	FinalHopReserveMs              int                              `json:"final_hop_reserve_ms,omitempty"`
	OfficialBaselineInputPer1MUSD  *float64                         `json:"official_baseline_input_per_1m_usd,omitempty"`
	OfficialBaselineOutputPer1MUSD *float64                         `json:"official_baseline_output_per_1m_usd,omitempty"`
	PoolCounts                     AdaptivePoolCounts               `json:"pool_counts"`
	RoutePreview                   *AdaptiveRoutePreview            `json:"route_preview,omitempty"`
	Targets                        []AdaptiveDashboardTargetRuntime `json:"targets"`
}

type rawDashboardEntry struct {
	target       configstoreTables.RouteGroupTarget
	resolved     resolvedTarget
	pricing      TargetPricingInfo
	fallbackOnly bool
	lastResort   bool
	tier         string
	available    bool
	order        int
}

type dashboardEvalEntry struct {
	raw       rawDashboardEntry
	snap      AdaptiveTargetSnapshot
	legacy    TargetHealthSnapshot
	activity  TargetActivitySnapshot
	cand      adaptiveCandidate
	officialP *float64
}

// InspectAdaptiveRuleDashboard computes a read-only live runtime view (ranking, health states,
// 90m reputation, 10m rescue window, pricing, and next-request route preview) without mutating
// any health tracker probe leases or in-flight counters.
func InspectAdaptiveRuleDashboard(
	rule *configstoreTables.TableRoutingRule,
	healthTracker *HealthTracker,
	pricingResolver TargetPricingResolver,
	upstreamResolver RoutingTargetUpstreamResolver,
	targetAvailable TargetAvailabilityChecker,
	now time.Time,
) *AdaptiveDashboardRuleRuntime {
	if rule == nil {
		return nil
	}
	policy := ApplyHealthPolicyDefaults(rule.ParsedHealthPolicy)
	adaptiveEnabled := isAdaptivePolicy(policy)

	engineMode := "grouped"
	if adaptiveEnabled {
		if policy.AdaptiveV22Enabled {
			engineMode = "adaptive_v22"
		} else {
			engineMode = "adaptive_v21"
		}
	}

	budgetMs := policy.RequestDeadlineMs
	if budgetMs <= 0 && adaptiveEnabled && strings.Contains(strings.ToLower(rule.Name), "gemini-auto") {
		budgetMs = defaultAdaptiveFallbackBudgetMs
	}
	maxAttempts := effectiveAdaptiveMaxAttempts(policy)
	finalReserveMs := 0
	if engineMode == "adaptive_v22" {
		finalReserveMs = adaptiveV22FinalHopReserveMs
	}

	var rawEntries []rawDashboardEntry
	seenKeys := make(map[string]struct{})
	maxCost := 0.0
	var officialBasePricing *TargetPricingInfo
	orderCounter := 0

	for gi, group := range rule.ParsedRouteGroups {
		isLastResort := group.LastResort
		isFallbackOnly := group.FallbackOnly || isLastResort
		tier := "L1"
		if isLastResort {
			tier = "L3"
		} else if isFallbackOnly {
			tier = "L2"
		}
		for _, t := range group.Targets {
			resolved := resolveRouteGroupTarget(t, &RoutingContext{}, gi, group.Name)
			if resolved.provider == "" || resolved.model == "" {
				continue
			}
			if _, dup := seenKeys[resolved.key]; dup {
				continue
			}
			seenKeys[resolved.key] = struct{}{}
			resolved.fallbackOnly = isFallbackOnly
			resolved.order = orderCounter
			orderCounter++

			avail := true
			if targetAvailable != nil && !targetAvailable(resolved.provider, resolved.model, resolved.keyID) {
				avail = false
			}

			var pricing TargetPricingInfo
			if pricingResolver != nil {
				pricing = pricingResolver(schemas.ModelProvider(resolved.provider), resolved.model, schemas.ChatCompletionRequest)
				if pricing.HasPricing && pricing.RawCost > maxCost {
					maxCost = pricing.RawCost
				}
				if pricing.HasPricing && isLastResort {
					if officialBasePricing == nil || pricing.RawCost > officialBasePricing.RawCost {
						pCopy := pricing
						officialBasePricing = &pCopy
					}
				}
			}

			rawEntries = append(rawEntries, rawDashboardEntry{
				target:       t,
				resolved:     resolved,
				pricing:      pricing,
				fallbackOnly: isFallbackOnly,
				lastResort:   isLastResort,
				tier:         tier,
				available:    avail,
				order:        resolved.order,
			})
		}
	}

	if officialBasePricing == nil && maxCost > 0 {
		for i := range rawEntries {
			if rawEntries[i].pricing.HasPricing && rawEntries[i].pricing.RawCost == maxCost {
				pCopy := rawEntries[i].pricing
				officialBasePricing = &pCopy
				break
			}
		}
	}

	out := &AdaptiveDashboardRuleRuntime{
		RuleID:               rule.ID,
		RuleName:             rule.Name,
		Description:          rule.Description,
		Enabled:              rule.Enabled,
		EngineMode:           engineMode,
		Policy:               policy,
		EffectiveBudgetMs:    budgetMs,
		EffectiveMaxAttempts: maxAttempts,
		FinalHopReserveMs:    finalReserveMs,
		Targets:              make([]AdaptiveDashboardTargetRuntime, 0, len(rawEntries)),
	}
	if officialBasePricing != nil {
		inCost := officialBasePricing.InputCostPer1MUSD
		outCost := officialBasePricing.OutputCostPer1MUSD
		out.OfficialBaselineInputPer1MUSD = &inCost
		out.OfficialBaselineOutputPer1MUSD = &outCost
	}

	if len(rawEntries) == 0 {
		return out
	}

	primaryTimeoutSec := float64(policy.PrimaryAttemptTimeoutMs) / 1000.0
	if primaryTimeoutSec <= 0 {
		primaryTimeoutSec = 38.0
	}

	// Build snapshots & candidates for all targets (even unavailable/tripwire/fatal so UI shows everything!)
	evalByKey := make(map[string]*dashboardEvalEntry, len(rawEntries))
	var regularEligible []adaptiveCandidate
	var backupEligible []adaptiveCandidate
	var lastResortEligible []adaptiveCandidate

	for _, re := range rawEntries {
		snap := AdaptiveTargetSnapshot{
			EffectiveSuccessRate: adaptivePriorSuccess / (adaptivePriorSuccess + adaptivePriorFail),
		}
		var legacy TargetHealthSnapshot
		var activity TargetActivitySnapshot
		if healthTracker != nil {
			snap = healthTracker.InspectAdaptiveTargetForRule(rule.ID, re.resolved.key, policy, now)
			legacy = healthTracker.GetTargetStatusForRule(rule.ID, re.resolved.key, policy, now)
			activity = healthTracker.GetTargetActivity(re.resolved.key)
		}

		normCost := 0.50
		var officialPct *float64
		if re.pricing.HasPricing && maxCost > 0 {
			normCost = re.pricing.RawCost / maxCost
			pct := normCost * 100.0
			officialPct = &pct
		}

		latencyPenalty := 0.0
		if snap.HasLatencySample {
			p80Sec := float64(snap.P80LatencyMs) / 1000.0
			if p80Sec > primaryTimeoutSec {
				latencyPenalty = 0.04 * (p80Sec - primaryTimeoutSec)
			}
		}
		failRate := math.Max(0, 1.0-snap.EffectiveSuccessRate)
		failPenalty := 3.5 * failRate * failRate
		score := normCost + latencyPenalty + failPenalty

		isFast := false
		if re.available {
			if !snap.InFatal && !snap.InProbation && !snap.InShortTripwire {
				if snap.TotalSuccesses >= 6 && snap.HasLatencySample {
					isFast = snap.EffectiveSuccessRate >= 0.88 && snap.P80LatencyMs <= int64(policy.FastChannelThresholdMs)
				} else if snap.EffectiveSuccessRate >= 0.82 {
					isFast = (normCost >= 0.40 || re.fallbackOnly || re.lastResort) &&
						(!snap.HasLatencySample || snap.P80LatencyMs <= int64(policy.FastChannelThresholdMs))
				}
			} else if re.lastResort && !snap.InFatal {
				if !snap.HasLatencySample || snap.P80LatencyMs <= int64(policy.RescueAttemptTimeoutMs) {
					isFast = true
				}
			}
		}

		// Pick representative 10m rescue counts for read-only V2.2 rescue simulation
		recent := rescueCounts{
			attempts: snap.Rescue10mNonStreamAttempts,
			fast:     snap.Rescue10mNonStreamFast,
		}
		if recent.attempts < 6 && snap.Rescue10mStreamAttempts >= 6 {
			recent = rescueCounts{
				attempts: snap.Rescue10mStreamAttempts,
				fast:     snap.Rescue10mStreamFast,
			}
		} else if recent.attempts < 6 {
			recent = rescueCounts{
				attempts: snap.Rescue10mNonStreamAttempts + snap.Rescue10mStreamAttempts,
				fast:     snap.Rescue10mNonStreamFast + snap.Rescue10mStreamFast,
			}
		}

		cand := adaptiveCandidate{
			primaryLimit:  re.target.PrimaryAttemptTimeoutMs,
			recent:        recent,
			resolved:      re.resolved,
			snap:          snap,
			rawCost:       re.pricing.RawCost,
			hasCost:       re.pricing.HasPricing,
			normCost:      normCost,
			score:         score,
			isFastRescue:  isFast,
			isBackup:      re.fallbackOnly && !re.lastResort,
			isLastResort:  re.lastResort,
			originalOrder: re.order,
		}

		evalByKey[re.resolved.key] = &dashboardEvalEntry{
			raw:       re,
			snap:      snap,
			legacy:    legacy,
			activity:  activity,
			cand:      cand,
			officialP: officialPct,
		}

		// Match adaptive_router.go eligibility (V2.2 filters out unavailable, fatal, and short tripwire)
		eligible := true
		if policy.AdaptiveV22Enabled {
			if !re.available || snap.InFatal || snap.InShortTripwire {
				eligible = false
			}
		}
		if eligible {
			switch {
			case re.lastResort:
				lastResortEligible = append(lastResortEligible, cand)
			case re.fallbackOnly:
				backupEligible = append(backupEligible, cand)
			default:
				regularEligible = append(regularEligible, cand)
			}
		}
	}

	// Non-adaptive grouped rules degrade cleanly with legacy health order
	if !adaptiveEnabled {
		out.Targets = make([]AdaptiveDashboardTargetRuntime, 0, len(rawEntries))
		for idx, re := range rawEntries {
			ev := evalByKey[re.resolved.key]
			state := "active"
			if !re.available {
				state = "unavailable"
			} else if ev.legacy.HealthLevel == string(HealthCooldown) {
				state = "fatal"
			} else if ev.legacy.HealthLevel == string(HealthDegraded) {
				state = "probation"
			}
			rankLabel := fmt.Sprintf("#%d", idx+1)
			if re.tier == "L2" {
				rankLabel = fmt.Sprintf("备%d", idx+1)
			} else if re.tier == "L3" {
				rankLabel = fmt.Sprintf("底%d", idx+1)
			}
			item := buildDashboardTargetRuntime(ev, policy, state, rankLabel, idx, idx == 0 && state == "active", false)
			out.Targets = append(out.Targets, item)
			accumulatePoolCounts(&out.PoolCounts, re.tier, state)
		}
		return out
	}

	// Mirror adaptive_router.go ranking logic without mutating tracker state
	sortByScore := func(slice []adaptiveCandidate) {
		sort.SliceStable(slice, func(i, j int) bool {
			a, b := slice[i], slice[j]
			if a.score != b.score {
				return a.score < b.score
			}
			if a.hasCost && b.hasCost && a.rawCost != b.rawCost {
				return a.rawCost < b.rawCost
			}
			return a.originalOrder < b.originalOrder
		})
	}

	var qualifiedReady []adaptiveCandidate
	var qualifiedColdBusy []adaptiveCandidate
	var demotedRegular []adaptiveCandidate

	for _, c := range regularEligible {
		if !c.snap.InProbation && !c.snap.InShortTripwire {
			if c.snap.ColdStartBusy {
				qualifiedColdBusy = append(qualifiedColdBusy, c)
			} else {
				qualifiedReady = append(qualifiedReady, c)
			}
		} else {
			demotedRegular = append(demotedRegular, c)
		}
	}

	sortByScore(qualifiedReady)
	sortByScore(qualifiedColdBusy)
	activeRegular := make([]adaptiveCandidate, 0, len(regularEligible))
	activeRegular = append(activeRegular, qualifiedReady...)
	activeRegular = append(activeRegular, qualifiedColdBusy...)

	promotedKeys := make(map[string]struct{})
	minActive := policy.MinActiveTargets
	if minActive > len(regularEligible) {
		minActive = len(regularEligible)
	}
	if len(activeRegular) < minActive && len(demotedRegular) > 0 {
		sort.SliceStable(demotedRegular, func(i, j int) bool {
			a, b := demotedRegular[i], demotedRegular[j]
			if a.snap.InFatal != b.snap.InFatal {
				return !a.snap.InFatal
			}
			if a.snap.InProbation != b.snap.InProbation {
				return !a.snap.InProbation
			}
			if a.score != b.score {
				return a.score < b.score
			}
			return a.originalOrder < b.originalOrder
		})
		for _, d := range demotedRegular {
			if len(activeRegular) >= minActive {
				break
			}
			activeRegular = append(activeRegular, d)
			promotedKeys[d.resolved.key] = struct{}{}
		}
	}

	var probationPool []adaptiveCandidate
	for _, d := range demotedRegular {
		if _, promoted := promotedKeys[d.resolved.key]; promoted {
			continue
		}
		if d.snap.InProbation {
			probationPool = append(probationPool, d)
		}
	}

	sortTierPool := func(slice []adaptiveCandidate) {
		sort.SliceStable(slice, func(i, j int) bool {
			a, b := slice[i], slice[j]
			if a.snap.InFatal != b.snap.InFatal {
				return !a.snap.InFatal
			}
			if a.snap.InProbation != b.snap.InProbation {
				return !a.snap.InProbation
			}
			if a.snap.InShortTripwire != b.snap.InShortTripwire {
				return !a.snap.InShortTripwire
			}
			if a.score != b.score {
				return a.score < b.score
			}
			return a.originalOrder < b.originalOrder
		})
	}
	sortTierPool(backupEligible)
	sortTierPool(lastResortEligible)

	var effectiveUpstream RoutingTargetUpstreamResolver
	if policy.EnableUpstreamDiversity == nil || *policy.EnableUpstreamDiversity {
		effectiveUpstream = upstreamResolver
	}

	diversifyAdaptive := func(items []adaptiveCandidate) []adaptiveCandidate {
		if len(items) <= 1 || effectiveUpstream == nil {
			return items
		}
		byKey := make(map[string]adaptiveCandidate, len(items))
		rawTargets := make([]resolvedTarget, 0, len(items))
		for _, item := range items {
			byKey[item.resolved.key] = item
			rawTargets = append(rawTargets, item.resolved)
		}
		diversified := diversifyByUpstream(rawTargets, effectiveUpstream)
		res := make([]adaptiveCandidate, 0, len(diversified))
		for _, rt := range diversified {
			if item, ok := byKey[rt.key]; ok {
				res = append(res, item)
			}
		}
		return res
	}

	if policy.AdaptiveV22Enabled {
		for i := range activeRegular {
			if !activeRegular[i].snap.InProbation {
				activeRegular[i].continuationRank = i + 1
			}
		}
	}
	if len(activeRegular) > 2 {
		tail := diversifyAdaptive(activeRegular[1:])
		activeRegular = append([]adaptiveCandidate{activeRegular[0]}, tail...)
	}
	backupEligible = diversifyAdaptive(backupEligible)
	lastResortEligible = diversifyAdaptive(lastResortEligible)

	// Find best fast rescue for read-only probation probe preview
	var bestFastRescue *adaptiveCandidate
	for i := range activeRegular {
		if activeRegular[i].isFastRescue {
			c := activeRegular[i]
			bestFastRescue = &c
			break
		}
	}
	if bestFastRescue == nil {
		for i := range backupEligible {
			if backupEligible[i].isFastRescue {
				c := backupEligible[i]
				bestFastRescue = &c
				break
			}
		}
	}

	var dueProbe *adaptiveCandidate
	if bestFastRescue != nil && len(probationPool) > 0 {
		var dueProbes []adaptiveCandidate
		for _, p := range probationPool {
			if p.snap.ProbationProbeDue {
				dueProbes = append(dueProbes, p)
			}
		}
		sort.SliceStable(dueProbes, func(i, j int) bool {
			a, b := dueProbes[i], dueProbes[j]
			if !a.snap.ProbationNextProbeAt.Equal(b.snap.ProbationNextProbeAt) {
				return a.snap.ProbationNextProbeAt.Before(b.snap.ProbationNextProbeAt)
			}
			if a.normCost != b.normCost {
				return a.normCost < b.normCost
			}
			return a.originalOrder < b.originalOrder
		})
		if len(dueProbes) > 0 {
			c := dueProbes[0]
			dueProbe = &c
		}
	}

	// Build candidate chain (normal traffic path, without claiming probation lease)
	ordered := make([]adaptiveCandidate, 0, len(rawEntries))
	seenOrdered := make(map[string]struct{}, len(rawEntries))
	appendOrdered := func(c adaptiveCandidate) {
		if len(ordered) >= maxAdaptiveCandidateChainLen {
			return
		}
		if _, exists := seenOrdered[c.resolved.key]; exists {
			return
		}
		seenOrdered[c.resolved.key] = struct{}{}
		ordered = append(ordered, c)
	}
	for _, c := range activeRegular {
		appendOrdered(c)
	}
	for _, d := range demotedRegular {
		if !d.snap.InProbation && !d.snap.InFatal {
			appendOrdered(d)
		}
	}
	for _, c := range backupEligible {
		appendOrdered(c)
	}
	for _, c := range lastResortEligible {
		appendOrdered(c)
	}
	if len(ordered) == 0 {
		for _, c := range demotedRegular {
			appendOrdered(c)
		}
	}

	// Compute rescue-ordered tail identically to adaptive_router.go V2.2
	rescueTail := make([]adaptiveCandidate, 0)
	if len(ordered) > 1 {
		rescueTail = append(rescueTail, ordered[1:]...)
		if policy.AdaptiveV22Enabled && len(rescueTail) > 1 {
			higherRescuePriority := func(a, b adaptiveCandidate) bool {
				if a.snap.InProbation != b.snap.InProbation {
					return !a.snap.InProbation
				}
				if a.isLastResort != b.isLastResort {
					return !a.isLastResort
				}
				ak, bk := a.recent.attempts >= 6, b.recent.attempts >= 6
				if ak && bk {
					left, right := int64(a.recent.fast)*int64(b.recent.attempts), int64(b.recent.fast)*int64(a.recent.attempts)
					if left != right {
						return left > right
					}
				} else if ak != bk {
					if ak {
						return a.recent.fast > 0 || !b.isFastRescue
					}
					return b.recent.fast == 0 && a.isFastRescue
				}
				if a.isFastRescue != b.isFastRescue {
					return a.isFastRescue
				}
				if a.isBackup != b.isBackup {
					return !a.isBackup
				}
				if a.hasCost != b.hasCost {
					return a.hasCost
				}
				return a.hasCost && a.rawCost < b.rawCost
			}
			sort.SliceStable(rescueTail, func(i, j int) bool { return higherRescuePriority(rescueTail[i], rescueTail[j]) })
			if effectiveUpstream != nil {
				last := effectiveUpstream(schemas.ModelProvider(ordered[0].resolved.provider))
				for i := range rescueTail {
					for j := i; j < len(rescueTail) && !higherRescuePriority(rescueTail[i], rescueTail[j]); j++ {
						host := effectiveUpstream(schemas.ModelProvider(rescueTail[j].resolved.provider))
						if host != "" && host != last {
							chosen := rescueTail[j]
							copy(rescueTail[i+1:j+1], rescueTail[i:j])
							rescueTail[i] = chosen
							break
						}
					}
					last = effectiveUpstream(schemas.ModelProvider(rescueTail[i].resolved.provider))
				}
			}
		}
	}

	// Build Next-Request Route Preview
	makeHopPreview := func(c adaptiveCandidate, timeoutMs int, isProbe bool) AdaptiveRouteHopPreview {
		ev := evalByKey[c.resolved.key]
		tier := "L1"
		if c.isLastResort {
			tier = "L3"
		} else if c.isBackup {
			tier = "L2"
		}
		return AdaptiveRouteHopPreview{
			TargetKey:        c.resolved.key,
			Provider:         c.resolved.provider,
			Model:            c.resolved.model,
			KeyID:            c.resolved.keyID,
			Tier:             tier,
			OfficialCostPct:  ev.officialP,
			TimeoutMs:        timeoutMs,
			IsFastRescue:     c.isFastRescue,
			IsLastResort:     c.isLastResort,
			IsProbationProbe: isProbe,
		}
	}

	preview := &AdaptiveRoutePreview{
		FinalHopReserveMs: finalReserveMs,
	}
	if len(ordered) > 0 {
		primary := ordered[0]
		primaryTimeout := effectiveCandidateAttemptTimeoutMs(primary, policy, true)
		hop := makeHopPreview(primary, primaryTimeout, false)
		preview.Primary = &hop

		// Fast-fail (<15s) continuation: next candidate with lowest continuationRank > primary
		var bestCont *adaptiveCandidate
		for i := range activeRegular {
			c := activeRegular[i]
			if c.resolved.key == primary.resolved.key {
				continue
			}
			if c.continuationRank > 0 {
				if bestCont == nil || c.continuationRank < bestCont.continuationRank {
					copyC := c
					bestCont = &copyC
				}
			}
		}
		if bestCont == nil && len(activeRegular) > 1 {
			for i := range activeRegular {
				if activeRegular[i].resolved.key != primary.resolved.key {
					copyC := activeRegular[i]
					bestCont = &copyC
					break
				}
			}
		}
		if bestCont != nil {
			contTimeout := effectiveCandidateAttemptTimeoutMs(*bestCont, policy, false)
			contHop := makeHopPreview(*bestCont, contTimeout, false)
			preview.FastFailContinuation = &contHop
		}

		// Timeout (>=15s) fast-rescue candidates: top 2 non-last-resort rescue-ordered hops
		for _, c := range rescueTail {
			if c.isLastResort {
				continue
			}
			preview.TimeoutRescue = append(preview.TimeoutRescue, makeHopPreview(c, policy.RescueAttemptTimeoutMs, false))
			if len(preview.TimeoutRescue) >= 2 {
				break
			}
		}

		// Final-hop safety net: top ready L2 backup + top ready L3 last-resort
		for _, c := range backupEligible {
			if c.resolved.key == primary.resolved.key || c.snap.InProbation || c.snap.InFatal || c.snap.InShortTripwire {
				continue
			}
			preview.FinalHopFallback = append(preview.FinalHopFallback, makeHopPreview(c, policy.RescueAttemptTimeoutMs, false))
			break
		}
		for _, c := range lastResortEligible {
			if c.snap.InFatal {
				continue
			}
			preview.FinalHopFallback = append(preview.FinalHopFallback, makeHopPreview(c, finalReserveMs, false))
			break
		}
	}
	if dueProbe != nil {
		probeTimeout := min(policy.PrimaryAttemptTimeoutMs, 22000)
		if dueProbe.primaryLimit > 0 {
			probeTimeout = min(dueProbe.primaryLimit, probeTimeout)
		}
		hop := makeHopPreview(*dueProbe, probeTimeout, true)
		preview.ProbationProbePending = &hop
	}
	out.RoutePreview = preview

	// Assign live rank order & labels per tier (matching activeRegular, backupEligible, lastResortEligible)
	l1ActiveRank := make(map[string]int, len(activeRegular))
	for i, c := range activeRegular {
		l1ActiveRank[c.resolved.key] = i + 1
	}
	l2ReadyRank := make(map[string]int, len(backupEligible))
	l2RankIdx := 0
	for _, c := range backupEligible {
		if !c.snap.InProbation && !c.snap.InFatal && !c.snap.InShortTripwire {
			l2RankIdx++
			l2ReadyRank[c.resolved.key] = l2RankIdx
		}
	}
	l3ReadyRank := make(map[string]int, len(lastResortEligible))
	l3RankIdx := 0
	for _, c := range lastResortEligible {
		if !c.snap.InFatal && !c.snap.InShortTripwire {
			l3RankIdx++
			l3ReadyRank[c.resolved.key] = l3RankIdx
		}
	}

	primaryKey := ""
	if len(ordered) > 0 {
		primaryKey = ordered[0].resolved.key
	}

	for _, re := range rawEntries {
		ev := evalByKey[re.resolved.key]
		_, promoted := promotedKeys[re.resolved.key]

		state := "active"
		switch {
		case !re.available:
			state = "unavailable"
		case ev.snap.InFatal:
			state = "fatal"
		case ev.snap.InShortTripwire:
			state = "tripwire"
		case ev.snap.InProbation && !promoted:
			if re.tier == "L3" {
				// L3 last-resort channels stay ready unless fatal/tripwire
				state = "active"
			} else {
				state = "probation"
			}
		default:
			state = "active"
		}

		rankLabel := "—"
		rankOrder := 10000 + re.order
		switch re.tier {
		case "L1":
			if r, ok := l1ActiveRank[re.resolved.key]; ok && state == "active" {
				rankLabel = fmt.Sprintf("#%d", r)
				rankOrder = r
			} else if state == "tripwire" {
				rankOrder = 1000 + re.order
			} else if state == "probation" {
				// Order probation channels by effective success rate descending
				rankOrder = 2000 + int((1.0-ev.snap.EffectiveSuccessRate)*1000)
			} else if state == "fatal" {
				rankOrder = 5000 + re.order
			} else {
				rankOrder = 6000 + re.order
			}
		case "L2":
			if r, ok := l2ReadyRank[re.resolved.key]; ok && state == "active" {
				rankLabel = fmt.Sprintf("备%d", r)
				rankOrder = r
			} else if state == "tripwire" {
				rankOrder = 1000 + re.order
			} else if state == "probation" {
				rankOrder = 2000 + int((1.0-ev.snap.EffectiveSuccessRate)*1000)
			} else {
				rankOrder = 5000 + re.order
			}
		case "L3":
			if r, ok := l3ReadyRank[re.resolved.key]; ok && state == "active" {
				rankLabel = fmt.Sprintf("底%d", r)
				rankOrder = r
			} else {
				rankOrder = 5000 + re.order
			}
		}

		isPrimary := re.resolved.key == primaryKey && state == "active"
		item := buildDashboardTargetRuntime(ev, policy, state, rankLabel, rankOrder, isPrimary, promoted)
		out.Targets = append(out.Targets, item)
		accumulatePoolCounts(&out.PoolCounts, re.tier, state)
	}

	// Deterministic tier-then-rank ordering
	tierOrd := map[string]int{"L1": 1, "L2": 2, "L3": 3}
	sort.SliceStable(out.Targets, func(i, j int) bool {
		ti, tj := tierOrd[out.Targets[i].Tier], tierOrd[out.Targets[j].Tier]
		if ti != tj {
			return ti < tj
		}
		if out.Targets[i].RankOrder != out.Targets[j].RankOrder {
			return out.Targets[i].RankOrder < out.Targets[j].RankOrder
		}
		if out.Targets[i].Score != out.Targets[j].Score {
			return out.Targets[i].Score < out.Targets[j].Score
		}
		return out.Targets[i].TargetKey < out.Targets[j].TargetKey
	})

	return out
}

func effectiveCandidateAttemptTimeoutMs(c adaptiveCandidate, policy *configstoreTables.HealthPolicy, isPrimary bool) int {
	timeoutMs := policy.PrimaryAttemptTimeoutMs
	if c.isLastResort && policy.RescueAttemptTimeoutMs > 0 {
		timeoutMs = policy.RescueAttemptTimeoutMs
	} else if c.isBackup && !isPrimary && policy.RescueAttemptTimeoutMs > 0 {
		timeoutMs = policy.RescueAttemptTimeoutMs
	}
	if policy.AdaptiveV22Enabled && (isPrimary || c.continuationRank > 0) && c.primaryLimit > 0 {
		timeoutMs = c.primaryLimit
	}
	return timeoutMs
}

func accumulatePoolCounts(counts *AdaptivePoolCounts, tier, state string) {
	counts.Total++
	switch tier {
	case "L1":
		counts.L1Total++
		switch state {
		case "active":
			counts.L1Active++
		case "tripwire":
			counts.L1Tripwire++
		case "probation":
			counts.L1Probation++
		default:
			counts.L1Fatal++
		}
	case "L2":
		counts.L2Total++
		if state == "active" {
			counts.L2Ready++
		} else {
			counts.L2Restricted++
		}
	case "L3":
		counts.L3Total++
		if state == "active" {
			counts.L3Ready++
		} else {
			counts.L3Restricted++
		}
	}
}

func buildDashboardTargetRuntime(
	ev *dashboardEvalEntry,
	policy *configstoreTables.HealthPolicy,
	state string,
	rankLabel string,
	rankOrder int,
	isPrimary bool,
	promoted bool,
) AdaptiveDashboardTargetRuntime {
	timeoutMs := policy.PrimaryAttemptTimeoutMs
	if ev.raw.tier == "L3" {
		timeoutMs = 0 // uses final-hop remaining budget / rescue window
	} else if ev.raw.tier == "L2" {
		timeoutMs = policy.RescueAttemptTimeoutMs
	} else if ev.raw.target.PrimaryAttemptTimeoutMs > 0 {
		timeoutMs = ev.raw.target.PrimaryAttemptTimeoutMs
	}

	item := AdaptiveDashboardTargetRuntime{
		TargetKey:                 ev.raw.resolved.key,
		Provider:                  ev.raw.resolved.provider,
		Model:                     ev.raw.resolved.model,
		KeyID:                     ev.raw.resolved.keyID,
		GroupName:                 ev.raw.resolved.layerName,
		GroupIndex:                ev.raw.resolved.layerIndex,
		Tier:                      ev.raw.tier,
		FallbackOnly:              ev.raw.fallbackOnly,
		LastResort:                ev.raw.lastResort,
		Weight:                    ev.raw.target.Weight,
		AttemptTimeoutMs:          timeoutMs,
		PrimaryAttemptTimeoutMs:   ev.raw.target.PrimaryAttemptTimeoutMs,
		OfficialCostPct:           ev.officialP,
		Score:                     ev.cand.score,
		RankLabel:                 rankLabel,
		RankOrder:                 rankOrder,
		IsPrimary:                 isPrimary,
		State:                     state,
		IsFastRescue:              ev.cand.isFastRescue && state == "active",
		MinActivePromoted:         promoted,
		EffectiveSuccessRate:      ev.snap.EffectiveSuccessRate,
		TotalSuccesses:            ev.snap.TotalSuccesses,
		ProbationConsecSuccess:    ev.snap.ProbationConsecSuccess,
		ProbationRecoverSuccesses: policy.ProbationRecoverSuccesses,
		ProbationProbeInFlight:    ev.snap.ProbationProbeInFlight,
		ProbationProbeDue:         ev.snap.ProbationProbeDue,
		Rescue10mNonStream: RescueWindowStats{
			Attempts: ev.snap.Rescue10mNonStreamAttempts,
			Fast:     ev.snap.Rescue10mNonStreamFast,
		},
		Rescue10mStream: RescueWindowStats{
			Attempts: ev.snap.Rescue10mStreamAttempts,
			Fast:     ev.snap.Rescue10mStreamFast,
		},
		Rescue10mTotal: RescueWindowStats{
			Attempts: ev.snap.Rescue10mNonStreamAttempts + ev.snap.Rescue10mStreamAttempts,
			Fast:     ev.snap.Rescue10mNonStreamFast + ev.snap.Rescue10mStreamFast,
		},
		LastOutcomeKind:           string(ev.snap.LastOutcomeKind),
		LastFailureMsg:            ev.snap.LastFailureMsg,
		LegacyHealthLevel:         ev.legacy.HealthLevel,
		LegacyConsecutiveFailures: ev.legacy.ConsecutiveFailures,
	}

	if ev.raw.pricing.HasPricing {
		inC := ev.raw.pricing.InputCostPer1MUSD
		outC := ev.raw.pricing.OutputCostPer1MUSD
		item.InputCostPer1MUSD = &inC
		item.OutputCostPer1MUSD = &outC
	}
	if ev.snap.HasLatencySample {
		p80 := ev.snap.P80LatencyMs
		item.P80LatencyMs = &p80
	}
	if !ev.snap.ShortTripwireUntil.IsZero() {
		t := ev.snap.ShortTripwireUntil.UTC()
		item.ShortTripwireUntil = &t
	}
	if !ev.snap.FatalUntil.IsZero() {
		t := ev.snap.FatalUntil.UTC()
		item.FatalUntil = &t
	}
	if !ev.snap.ProbationNextProbeAt.IsZero() {
		t := ev.snap.ProbationNextProbeAt.UTC()
		item.ProbationNextProbeAt = &t
	}
	if !ev.snap.LastOutcomeAt.IsZero() {
		t := ev.snap.LastOutcomeAt.UTC()
		item.LastOutcomeAt = &t
	}
	if !ev.snap.LastFailureTime.IsZero() {
		t := ev.snap.LastFailureTime.UTC()
		item.LastFailureTime = &t
	}
	if !ev.activity.LastRealAccessAt.IsZero() {
		t := ev.activity.LastRealAccessAt.UTC()
		item.LastRealAccessAt = &t
	}
	if ev.legacy.CooldownUntil != nil && *ev.legacy.CooldownUntil != "" {
		c := *ev.legacy.CooldownUntil
		item.LegacyCooldownUntil = &c
	}

	return item
}
