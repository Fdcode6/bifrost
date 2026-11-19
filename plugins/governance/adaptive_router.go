package governance

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
)

const (
	defaultAdaptiveFallbackBudgetMs = 56000 // 56s: safely inside both 60s plugin and 70s main-engine HTTP timeouts
	defaultAdaptiveMaxAttempts      = 5     // up to 4 discount/backup attempts + 1 final 100% last-resort attempt
	maxAdaptiveCandidateChainLen    = 24
)

type adaptiveCandidate struct {
	resolved      resolvedTarget
	snap          AdaptiveTargetSnapshot
	rawCost       float64
	hasCost       bool
	normCost      float64
	score         float64
	isFastRescue  bool
	isBackup      bool
	isLastResort  bool
	isProbation   bool
	originalOrder int
}

func effectiveAdaptiveBudgetMs(policy *configstoreTables.HealthPolicy, routingCtx *RoutingContext) int {
	if policy != nil && policy.RequestDeadlineMs > 0 {
		return policy.RequestDeadlineMs
	}
	if isGeminiAutoModel(routingCtx) {
		return defaultAdaptiveFallbackBudgetMs
	}
	return 0
}

func effectiveAdaptiveMaxAttempts(policy *configstoreTables.HealthPolicy) int {
	if policy != nil && policy.MaxFallbackAttempts > 0 {
		return policy.MaxFallbackAttempts
	}
	return defaultAdaptiveMaxAttempts
}

// buildAdaptiveGroupedRoutingDecision implements V2 Reputation + 4-Zone Profit-Preserving +
// Dynamic Fast-Rescue routing:
//  1. Regular groups (<=60% profit pool): qualified channels (>70% decaying success rate, <=38s)
//     rank strictly by composite cost score; #1 eats 100% of traffic without artificial caps.
//  2. Minimum Active Pool Guarantee: even during upstream storms where all channels dip below 70%,
//     at least MinActiveTargets (default 3) best discount channels remain active.
//  3. Observation / Probation Pool: flaky cheap channels receive 0 normal traffic and are tested
//     periodically (every 5m..15m) with 1 real request at Hop 0 backed immediately by a proven
//     fast rescue channel at Hop 1; 2 consecutive real successes promote them back to #1.
//  4. Backup groups (fallback_only: 70%~95%) never jump ahead of regular groups; LastResort groups
//     (last_resort: 100% full price) are strictly locked to the final attempt slot.
func buildAdaptiveGroupedRoutingDecision(
	ctx *schemas.BifrostContext,
	rule *configstoreTables.TableRoutingRule,
	routingCtx *RoutingContext,
	healthTracker *HealthTracker,
	policy *configstoreTables.HealthPolicy,
	now time.Time,
) *RoutingDecision {
	policy = ApplyHealthPolicyDefaults(policy)

	requestType := schemas.ChatCompletionRequest
	if routingCtx != nil && routingCtx.RequestType != "" {
		requestType = schemas.RequestType(routingCtx.RequestType)
	}

	type rawEntry struct {
		target       configstoreTables.RouteGroupTarget
		resolved     resolvedTarget
		rawCost      float64
		hasCost      bool
		fallbackOnly bool
		lastResort   bool
		order        int
	}

	var rawEntries []rawEntry
	maxCost := 0.0
	orderCounter := 0

	for gi, group := range rule.ParsedRouteGroups {
		isLastResort := group.LastResort
		isFallbackOnly := group.FallbackOnly || isLastResort
		for _, t := range group.Targets {
			resolved := resolveRouteGroupTarget(t, routingCtx, gi, group.Name)
			resolved.fallbackOnly = isFallbackOnly
			resolved.order = orderCounter
			orderCounter++

			var c float64
			var ok bool
			if routingCtx != nil && routingCtx.TargetCostResolver != nil {
				c, ok = routingCtx.TargetCostResolver(schemas.ModelProvider(resolved.provider), resolved.model, requestType)
				if ok && c > maxCost {
					maxCost = c
				}
			}
			rawEntries = append(rawEntries, rawEntry{
				target:       t,
				resolved:     resolved,
				rawCost:      c,
				hasCost:      ok,
				fallbackOnly: isFallbackOnly,
				lastResort:   isLastResort,
				order:        resolved.order,
			})
		}
	}

	if len(rawEntries) == 0 {
		ctx.AppendRoutingEngineLog(routingGroupEngine, "Adaptive router: no targets configured")
		return nil
	}

	primaryTimeoutSec := float64(policy.PrimaryAttemptTimeoutMs) / 1000.0
	if primaryTimeoutSec <= 0 {
		primaryTimeoutSec = 38.0
	}

	var regularAll []adaptiveCandidate
	var backupAll []adaptiveCandidate
	var lastResortAll []adaptiveCandidate

	for _, re := range rawEntries {
		snap := AdaptiveTargetSnapshot{
			EffectiveSuccessRate: adaptivePriorSuccess / (adaptivePriorSuccess + adaptivePriorFail),
		}
		if healthTracker != nil {
			snap = healthTracker.InspectAdaptiveTargetForRule(rule.ID, re.resolved.key, policy, now)
		}

		normCost := 0.50
		if re.hasCost && maxCost > 0 {
			normCost = re.rawCost / maxCost
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
		if !snap.InFatal && !snap.InProbation && !snap.InShortTripwire {
			// A Fast-Rescue ("快枪手") channel runs after 15~35s have ALREADY been spent on Hop 0,
			// so it MUST have high reliability (>=88% effective success rate) — a 71% coin-flip
			// cheap channel must never be chosen as the rescue hop after a timeout.
			if snap.TotalSuccesses >= 6 && snap.HasLatencySample {
				isFast = snap.EffectiveSuccessRate >= 0.88 && snap.P80LatencyMs <= int64(policy.FastChannelThresholdMs)
			} else if snap.EffectiveSuccessRate >= 0.82 {
				// Cold-start bootstrap before 6 samples: only >=40% mainstream and backup/last-resort
				// tiers act as fast-rescue capable until sufficient samples prove speed & stability.
				isFast = (normCost >= 0.40 || re.fallbackOnly || re.lastResort) &&
					(!snap.HasLatencySample || snap.P80LatencyMs <= int64(policy.FastChannelThresholdMs))
			}
		} else if re.lastResort && !snap.InFatal {
			if !snap.HasLatencySample || snap.P80LatencyMs <= int64(policy.RescueAttemptTimeoutMs) {
				isFast = true
			}
		}

		cand := adaptiveCandidate{
			resolved:      re.resolved,
			snap:          snap,
			rawCost:       re.rawCost,
			hasCost:       re.hasCost,
			normCost:      normCost,
			score:         score,
			isFastRescue:  isFast,
			isBackup:      re.fallbackOnly && !re.lastResort,
			isLastResort:  re.lastResort,
			originalOrder: re.order,
		}

		switch {
		case re.lastResort:
			lastResortAll = append(lastResortAll, cand)
		case re.fallbackOnly:
			backupAll = append(backupAll, cand)
		default:
			regularAll = append(regularAll, cand)
		}
	}

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

	for _, c := range regularAll {
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
	activeRegular := make([]adaptiveCandidate, 0, len(regularAll))
	activeRegular = append(activeRegular, qualifiedReady...)
	activeRegular = append(activeRegular, qualifiedColdBusy...)

	// Ironclad Minimum Active Pool Guarantee: never allow activeRegular to drop below MinActiveTargets
	// when regular targets exist, preventing total starvation during global upstream turbulence.
	promotedKeys := make(map[string]struct{})
	minActive := policy.MinActiveTargets
	if minActive > len(regularAll) {
		minActive = len(regularAll)
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

	// Remaining probation pool eligible for periodic single-request verification
	var probationPool []adaptiveCandidate
	for _, d := range demotedRegular {
		if _, promoted := promotedKeys[d.resolved.key]; promoted {
			continue
		}
		if d.snap.InProbation {
			probationPool = append(probationPool, d)
		}
	}

	// Sort Backup Pool (70%~95%) and Last-Resort Pool (100%): healthy first, then composite score
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
	sortTierPool(backupAll)
	sortTierPool(lastResortAll)

	var upstreamResolver RoutingTargetUpstreamResolver
	if (policy.EnableUpstreamDiversity == nil || *policy.EnableUpstreamDiversity) &&
		routingCtx != nil && routingCtx.TargetUpstreamResolver != nil {
		upstreamResolver = routingCtx.TargetUpstreamResolver
	}

	diversifyAdaptive := func(items []adaptiveCandidate) []adaptiveCandidate {
		if len(items) <= 1 || upstreamResolver == nil {
			return items
		}
		byKey := make(map[string]adaptiveCandidate, len(items))
		rawTargets := make([]resolvedTarget, 0, len(items))
		for _, item := range items {
			byKey[item.resolved.key] = item
			rawTargets = append(rawTargets, item.resolved)
		}
		diversified := diversifyByUpstream(rawTargets, upstreamResolver)
		out := make([]adaptiveCandidate, 0, len(diversified))
		for _, rt := range diversified {
			if item, ok := byKey[rt.key]; ok {
				out = append(out, item)
			}
		}
		return out
	}

	// Preserve #1 lowest-score active channel at index 0; diversify fallbacks strictly within each tier!
	if len(activeRegular) > 2 {
		tail := diversifyAdaptive(activeRegular[1:])
		activeRegular = append([]adaptiveCandidate{activeRegular[0]}, tail...)
	}
	backupAll = diversifyAdaptive(backupAll)
	lastResortAll = diversifyAdaptive(lastResortAll)

	// Find cheapest available proven fast-rescue channel across activeRegular, then backupAll
	var bestFastRescue *adaptiveCandidate
	for i := range activeRegular {
		if activeRegular[i].isFastRescue {
			c := activeRegular[i]
			bestFastRescue = &c
			break
		}
	}
	if bestFastRescue == nil {
		for i := range backupAll {
			if backupAll[i].isFastRescue {
				c := backupAll[i]
				bestFastRescue = &c
				break
			}
		}
	}

	// Periodic real-request probation probe: only when a fast-rescue safety net exists!
	var claimedProbe *adaptiveCandidate
	if healthTracker != nil && bestFastRescue != nil && len(probationPool) > 0 {
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
		for i := range dueProbes {
			if healthTracker.TryClaimProbationProbeForRule(rule.ID, dueProbes[i].resolved.key, policy, now) {
				c := dueProbes[i]
				c.isProbation = true
				c.isFastRescue = false
				claimedProbe = &c
				ctx.AppendRoutingEngineLog(routingGroupEngine,
					fmt.Sprintf("Adaptive probation real-request probe claimed: %s (eff_rate=%.2f, backed by %s)",
						c.resolved.key, c.snap.EffectiveSuccessRate, bestFastRescue.resolved.key))
				break
			}
		}
	}

	ordered := make([]adaptiveCandidate, 0, len(rawEntries))
	seen := make(map[string]struct{}, len(rawEntries))
	appendCandidate := func(c adaptiveCandidate) {
		if len(ordered) >= maxAdaptiveCandidateChainLen {
			return
		}
		if _, exists := seen[c.resolved.key]; exists {
			return
		}
		seen[c.resolved.key] = struct{}{}
		ordered = append(ordered, c)
	}

	if claimedProbe != nil {
		appendCandidate(*claimedProbe)
		if bestFastRescue != nil {
			appendCandidate(*bestFastRescue)
		}
	}
	for _, c := range activeRegular {
		appendCandidate(c)
	}
	// Tripwired-only regular targets (not in probation) sit behind healthy regular targets before >70% backups
	for _, d := range demotedRegular {
		if !d.snap.InProbation && !d.snap.InFatal {
			appendCandidate(d)
		}
	}
	for _, c := range backupAll {
		appendCandidate(c)
	}
	for _, c := range lastResortAll {
		appendCandidate(c)
	}
	// Ultimate fallback if only probation targets were configured on the rule
	if len(ordered) == 0 {
		for _, c := range demotedRegular {
			appendCandidate(c)
		}
	}
	if len(ordered) == 0 {
		return nil
	}

	probeTimeoutMs := policy.PrimaryAttemptTimeoutMs - 3000
	if probeTimeoutMs > 35000 {
		probeTimeoutMs = 35000
	}
	if probeTimeoutMs < 10000 {
		probeTimeoutMs = policy.PrimaryAttemptTimeoutMs
	}

	buildHint := func(c adaptiveCandidate, isPrimary bool) schemas.FallbackAttemptHint {
		timeoutMs := policy.PrimaryAttemptTimeoutMs
		if isPrimary && c.isProbation {
			timeoutMs = probeTimeoutMs
		} else if c.isLastResort && policy.RescueAttemptTimeoutMs > 0 {
			timeoutMs = policy.RescueAttemptTimeoutMs
		}
		return schemas.FallbackAttemptHint{
			AttemptTimeoutMs: timeoutMs,
			IsFastRescue:     c.isFastRescue,
			IsLastResort:     c.isLastResort,
			IsProbationProbe: isPrimary && c.isProbation,
		}
	}

	primary := ordered[0]
	primaryHint := buildHint(primary, true)

	fallbacks := make([]string, 0, len(ordered)-1)
	fallbackKeyIDs := make([]string, 0, len(ordered)-1)
	fallbackLayerPlan := make([]RoutingLayerPlan, 0, len(ordered)-1)
	fallbackHints := make([]schemas.FallbackAttemptHint, 0, len(ordered)-1)

	for _, c := range ordered[1:] {
		fallbacks = append(fallbacks, c.resolved.provider+"/"+c.resolved.model)
		fallbackKeyIDs = append(fallbackKeyIDs, c.resolved.keyID)
		fallbackLayerPlan = append(fallbackLayerPlan, c.resolved.toLayerPlan())
		fallbackHints = append(fallbackHints, buildHint(c, false))
	}

	adaptiveRuntime := &schemas.AdaptiveFallbackRuntimePolicy{
		RequestStartTime:       now,
		FastRescueElapsedMs:    policy.FastRescueElapsedMs,
		RescueAttemptTimeoutMs: policy.RescueAttemptTimeoutMs,
		MaxTotalAttempts:       effectiveAdaptiveMaxAttempts(policy),
		PrimaryHint:            primaryHint,
		FallbackHints:          fallbackHints,
	}

	ctx.AppendRoutingEngineLog(routingGroupEngine,
		fmt.Sprintf("Adaptive decision: primary=%s/%s (score=%.3f, eff_rate=%.2f, fast=%t, probe=%t), candidates=%d",
			primary.resolved.provider, primary.resolved.model, primary.score,
			primary.snap.EffectiveSuccessRate, primary.isFastRescue, primaryHint.IsProbationProbe, len(ordered)))

	return &RoutingDecision{
		Provider:          primary.resolved.provider,
		Model:             primary.resolved.model,
		KeyID:             primary.resolved.keyID,
		Fallbacks:         fallbacks,
		FallbackKeyIDs:    fallbackKeyIDs,
		PrimaryLayer:      primary.resolved.toLayerPlan(),
		FallbackLayerPlan: fallbackLayerPlan,
		MatchedRuleID:     rule.ID,
		MatchedRuleName:   rule.Name,
		IsGroupedRouting:  true,
		HealthPolicy:      policy,
		AdaptivePolicy:    adaptiveRuntime,
	}
}
