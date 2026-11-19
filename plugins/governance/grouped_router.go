package governance

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
)

const (
	routingGroupEngine               = "routing-group"
	defaultAutoRouteFallbackBudgetMs = 135000
	defaultAutoRouteMaxAttemptBudget = 6
)

// resolvedTarget holds provider/model/keyID after resolving optional fields against defaults
type resolvedTarget struct {
	provider          string
	model             string
	keyID             string
	key               string // TargetKey for dedup
	layerIndex        int
	layerName         string
	halfOpenCandidate bool
	cooldownUntil     time.Time
	cooldownReason    OutcomeKind
	fallbackOnly      bool
	order             int
}

func resolveRouteGroupTarget(target configstoreTables.RouteGroupTarget, routingCtx *RoutingContext, layerIndex int, layerName string) resolvedTarget {
	provider := derefOr(target.Provider, string(routingCtx.Provider))
	model := derefOr(target.Model, routingCtx.Model)
	keyID := derefOr(target.KeyID, "")
	return resolvedTarget{
		provider:   provider,
		model:      model,
		keyID:      keyID,
		key:        TargetKey(provider, model, keyID),
		layerIndex: layerIndex,
		layerName:  layerName,
	}
}

func (target resolvedTarget) toLayerPlan() RoutingLayerPlan {
	return RoutingLayerPlan{
		Provider:   target.provider,
		Model:      target.model,
		KeyID:      target.keyID,
		LayerIndex: target.layerIndex,
		LayerName:  target.layerName,
	}
}

// defaultHealthPolicy returns the default health policy when none is configured
func defaultHealthPolicy() *configstoreTables.HealthPolicy {
	return ApplyHealthPolicyDefaults(nil)
}

func isGeminiAutoModel(routingCtx *RoutingContext) bool {
	if routingCtx == nil {
		return false
	}
	return routingCtx.Model == "gemini-auto" || routingCtx.Model == "gemini-auto-bif"
}

func effectiveMaxFallbackAttempts(policy *configstoreTables.HealthPolicy, routingCtx *RoutingContext) int {
	if policy != nil && policy.MaxFallbackAttempts > 0 {
		return policy.MaxFallbackAttempts
	}
	if isGeminiAutoModel(routingCtx) {
		return defaultAutoRouteMaxAttemptBudget
	}
	return 0
}

func effectiveFallbackBudgetMs(policy *configstoreTables.HealthPolicy, routingCtx *RoutingContext) int {
	if isAdaptivePolicy(policy) {
		return effectiveAdaptiveBudgetMs(policy, routingCtx)
	}
	if policy != nil && policy.RequestDeadlineMs > 0 {
		return policy.RequestDeadlineMs
	}
	if isGeminiAutoModel(routingCtx) {
		return defaultAutoRouteFallbackBudgetMs
	}
	return 0
}

// buildGroupedRoutingDecision builds a RoutingDecision by selecting healthy targets
// from route groups and constructing a primary + fallback chain.
//
// Key properties:
//   - Price-first / group-priority ordering: healthy targets are ordered by group priority,
//     and within multi-target groups by effective cost ascending when TargetCostResolver is set.
//   - In-place half-open recovery: an expired cooled target competes at its natural group/price
//     priority (not buried at the tail where backups starve it), capped to at most 1 half-open
//     probe per request and claimed atomically only if it actually enters the final chain.
//   - Upstream diversity: when EnableUpstreamDiversity is true and TargetUpstreamResolver is set,
//     fallbacks are arranged in distinct-upstream rounds so a single request does not burn
//     multiple attempts against the same failing upstream host when independent upstreams exist.
//   - All-cooldown graceful escape: if all targets are actively cooled, constructs an emergency
//     escape chain preferring non-fatal cooldowns and earliest recovery instead of returning nil.
func buildGroupedRoutingDecision(
	ctx *schemas.BifrostContext,
	rule *configstoreTables.TableRoutingRule,
	routingCtx *RoutingContext,
	healthTracker *HealthTracker,
	logger schemas.Logger,
) *RoutingDecision {
	policy := ApplyHealthPolicyDefaults(rule.ParsedHealthPolicy)

	now := time.Now()
	if isAdaptivePolicy(policy) {
		return buildAdaptiveGroupedRoutingDecision(ctx, rule, routingCtx, healthTracker, policy, now)
	}
	ctx.AppendRoutingEngineLog(routingGroupEngine,
		fmt.Sprintf("Building grouped routing chain for rule '%s', %d groups", rule.Name, len(rule.ParsedRouteGroups)))

	var primaryPool []resolvedTarget
	var degradedPool []resolvedTarget
	var fallbackOnlyPool []resolvedTarget
	var cooledCandidates []resolvedTarget
	nextOrder := 0

	for gi, group := range rule.ParsedRouteGroups {
		groupPrimaryTargets := make([]configstoreTables.RouteGroupTarget, 0, len(group.Targets))
		groupHalfOpenKeys := make(map[string]struct{})
		groupDegradedTargets := make([]configstoreTables.RouteGroupTarget, 0, len(group.Targets))
		groupFallbackOnlyTargets := make([]configstoreTables.RouteGroupTarget, 0, len(group.Targets))
		healthyCount := 0
		halfOpenCount := 0
		cooldownCount := 0

		for _, t := range group.Targets {
			resolved := resolveRouteGroupTarget(t, routingCtx, gi, group.Name)
			resolved.fallbackOnly = group.FallbackOnly
			resolved.order = nextOrder
			nextOrder++

			state := TargetRoutingState{Level: HealthHealthy}
			if healthTracker != nil {
				state = healthTracker.InspectTargetRoutingStateForRule(rule.ID, resolved.key, policy, now)
			}

			if state.Level == HealthCooldown {
				resolved.cooldownUntil = state.CooldownUntil
				resolved.cooldownReason = state.CooldownReason
				cooledCandidates = append(cooledCandidates, resolved)

				if state.HalfOpenEligible && !group.FallbackOnly {
					groupPrimaryTargets = append(groupPrimaryTargets, t)
					groupHalfOpenKeys[resolved.key] = struct{}{}
					halfOpenCount++
					ctx.AppendRoutingEngineLog(routingGroupEngine,
						fmt.Sprintf("In-place half-open candidate: %s", resolved.key))
					continue
				}
				cooldownCount++
				ctx.AppendRoutingEngineLog(routingGroupEngine,
					fmt.Sprintf("Filtered: %s (cooldown)", resolved.key))
				continue
			}

			if group.FallbackOnly {
				groupFallbackOnlyTargets = append(groupFallbackOnlyTargets, t)
				continue
			}
			if state.Level == HealthDegraded {
				groupDegradedTargets = append(groupDegradedTargets, t)
				continue
			}
			healthyCount++
			groupPrimaryTargets = append(groupPrimaryTargets, t)
		}

		ctx.AppendRoutingEngineLog(routingGroupEngine,
			fmt.Sprintf("Group[%d] %s: targets=%d, healthy=%d, degraded=%d, half_open=%d, cooldown=%d, fallback_only=%t",
				gi, group.Name, len(group.Targets), healthyCount, len(groupDegradedTargets), halfOpenCount, cooldownCount, group.FallbackOnly))

		slots := resolveGroupSlots(group, routingCtx, len(groupPrimaryTargets))
		// When half-open candidates share a group with healthy targets, allow extra room during
		// per-group selection so dropping excess half-open candidates globally never starves
		// healthy targets in the same group.
		primarySelectSlots := slots
		if halfOpenCount > 0 && len(groupPrimaryTargets) > primarySelectSlots {
			primarySelectSlots = min(len(groupPrimaryTargets), slots+halfOpenCount)
		}
		selectedPrimary := selectResolvedTargets(groupPrimaryTargets, routingCtx, gi, group.Name, primarySelectSlots)
		for idx := range selectedPrimary {
			if _, ok := groupHalfOpenKeys[selectedPrimary[idx].key]; ok {
				selectedPrimary[idx].halfOpenCandidate = true
			}
		}
		primaryPool = append(primaryPool, selectedPrimary...)

		degradedSlots := resolveGroupSlots(group, routingCtx, len(groupDegradedTargets))
		degradedPool = append(degradedPool, selectResolvedTargets(groupDegradedTargets, routingCtx, gi, group.Name, degradedSlots)...)

		fallbackSlots := 1 + group.RetryLimit
		fallbackOnlyPool = append(fallbackOnlyPool, selectResolvedTargetsWithRepeats(groupFallbackOnlyTargets, routingCtx, gi, group.Name, fallbackSlots)...)
	}

	// Cap to at most 1 half-open candidate per request (the highest-priority / cheapest one),
	// so no single request carries compounded recovery risk when multiple channels thaw together.
	filteredPrimary := make([]resolvedTarget, 0, len(primaryPool))
	keptHalfOpen := false
	for _, rt := range primaryPool {
		if rt.halfOpenCandidate {
			if keptHalfOpen {
				continue
			}
			keptHalfOpen = true
		}
		filteredPrimary = append(filteredPrimary, rt)
	}

	var upstreamResolver RoutingTargetUpstreamResolver
	if (policy.EnableUpstreamDiversity == nil || *policy.EnableUpstreamDiversity) &&
		routingCtx != nil && routingCtx.TargetUpstreamResolver != nil {
		upstreamResolver = routingCtx.TargetUpstreamResolver
	}

	if upstreamResolver != nil {
		filteredPrimary = diversifyByUpstream(filteredPrimary, upstreamResolver)
		degradedPool = diversifyByUpstream(degradedPool, upstreamResolver)
	}

	chain := make([]resolvedTarget, 0, len(filteredPrimary)+len(degradedPool)+len(fallbackOnlyPool))
	seen := make(map[string]struct{})
	appendUnique := func(targets []resolvedTarget, tag string) {
		for _, rt := range targets {
			if _, ok := seen[rt.key]; ok {
				continue
			}
			chain = append(chain, rt)
			seen[rt.key] = struct{}{}
			ctx.AppendRoutingEngineLog(routingGroupEngine,
				fmt.Sprintf("Selected slot %d (%s) from group %s (layer=%d): provider=%s model=%s",
					len(chain), tag, rt.layerName, rt.layerIndex, rt.provider, rt.model))
		}
	}
	fallbackOnlySeen := make(map[string]struct{})
	appendFallbackOnly := func(targets []resolvedTarget) {
		for _, rt := range targets {
			if _, ok := seen[rt.key]; ok {
				if _, alreadyFallbackOnly := fallbackOnlySeen[rt.key]; !alreadyFallbackOnly {
					continue
				}
			}
			chain = append(chain, rt)
			seen[rt.key] = struct{}{}
			fallbackOnlySeen[rt.key] = struct{}{}
			ctx.AppendRoutingEngineLog(routingGroupEngine,
				fmt.Sprintf("Selected slot %d from fallback-only group %s (layer=%d): provider=%s model=%s",
					len(chain), rt.layerName, rt.layerIndex, rt.provider, rt.model))
		}
	}

	appendUnique(filteredPrimary, "primary")
	appendUnique(degradedPool, "degraded")
	appendFallbackOnly(fallbackOnlyPool)

	maxAttempts := effectiveMaxFallbackAttempts(policy, routingCtx)
	if maxAttempts > 0 && len(chain) > maxAttempts {
		chain = chain[:maxAttempts]
	}

	// Atomically claim the single half-open candidate ONLY if it actually survived into chain.
	if healthTracker != nil {
		claimedChain := chain[:0]
		for _, rt := range chain {
			if rt.halfOpenCandidate {
				if !healthTracker.TryClaimHalfOpenProbeForRule(rule.ID, rt.key, policy, now) {
					ctx.AppendRoutingEngineLog(routingGroupEngine,
						fmt.Sprintf("Half-open probe already claimed concurrently: %s, skipping", rt.key))
					continue
				}
				ctx.AppendRoutingEngineLog(routingGroupEngine,
					fmt.Sprintf("Claimed in-place half-open probe: %s", rt.key))
			}
			claimedChain = append(claimedChain, rt)
		}
		chain = claimedChain
	}

	// All-cooldown graceful degradation: never return nil when targets exist; build an escape
	// chain preferring non-fatal cooldowns and earliest recovery across distinct upstreams.
	if len(chain) == 0 && len(cooledCandidates) > 0 {
		escape := buildAllCooldownEscapeChain(cooledCandidates, upstreamResolver, maxAttempts)
		for _, rt := range escape {
			chain = append(chain, rt)
			ctx.AppendRoutingEngineLog(routingGroupEngine,
				fmt.Sprintf("All targets in cooldown; escape slot %d from group %s (layer=%d): provider=%s model=%s",
					len(chain), rt.layerName, rt.layerIndex, rt.provider, rt.model))
		}
	}

	if len(chain) == 0 {
		ctx.AppendRoutingEngineLog(routingGroupEngine, "No targets configured across all groups")
		return nil
	}

	// First target = primary; rest = fallback chain
	primary := chain[0]
	fallbacks := make([]string, 0, len(chain)-1)
	fallbackKeyIDs := make([]string, 0, len(chain)-1)
	fallbackLayerPlan := make([]RoutingLayerPlan, 0, len(chain)-1)
	for _, rt := range chain[1:] {
		fb := rt.provider + "/" + rt.model
		fallbacks = append(fallbacks, fb)
		fallbackKeyIDs = append(fallbackKeyIDs, rt.keyID)
		fallbackLayerPlan = append(fallbackLayerPlan, rt.toLayerPlan())
	}

	ctx.AppendRoutingEngineLog(routingGroupEngine,
		fmt.Sprintf("Decision: primary=%s/%s (keyID=%s, layer=%d), fallbacks=%v, fallbackKeyIDs=%v",
			primary.provider, primary.model, primary.keyID, primary.layerIndex, fallbacks, fallbackKeyIDs))

	return &RoutingDecision{
		Provider:          primary.provider,
		Model:             primary.model,
		KeyID:             primary.keyID,
		Fallbacks:         fallbacks,
		FallbackKeyIDs:    fallbackKeyIDs,
		PrimaryLayer:      primary.toLayerPlan(),
		FallbackLayerPlan: fallbackLayerPlan,
		MatchedRuleID:     rule.ID,
		MatchedRuleName:   rule.Name,
		IsGroupedRouting:  true,
		HealthPolicy:      policy,
	}
}

// resolveGroupSlots determines how many targets to pick from a group.
// When RetryLimit <= 0 on a non-fallback-only multi-target pool with cost resolution enabled,
// emit all eligible targets in ascending cost order so operators can drop 15-30+ priced tiers
// into one pool without manually maintaining retry_limit.
func resolveGroupSlots(group configstoreTables.RouteGroup, routingCtx *RoutingContext, candidateCount int) int {
	if candidateCount <= 0 {
		return 0
	}
	if group.RetryLimit > 0 {
		return 1 + group.RetryLimit
	}
	if !group.FallbackOnly && routingCtx != nil && routingCtx.TargetCostResolver != nil && candidateCount > 1 {
		return candidateCount
	}
	return 1
}

// diversifyByUpstream reorders targets into distinct-upstream rounds while strictly preserving
// the original (price/priority) order within each round.
func diversifyByUpstream(targets []resolvedTarget, resolver RoutingTargetUpstreamResolver) []resolvedTarget {
	if len(targets) <= 1 || resolver == nil {
		return targets
	}
	remaining := append([]resolvedTarget(nil), targets...)
	result := make([]resolvedTarget, 0, len(targets))
	for len(remaining) > 0 {
		seenHost := make(map[string]struct{}, len(remaining))
		nextRound := make([]resolvedTarget, 0)
		for _, rt := range remaining {
			host := strings.ToLower(strings.TrimSpace(resolver(schemas.ModelProvider(rt.provider))))
			if host == "" {
				host = strings.ToLower(rt.provider)
			}
			if _, exists := seenHost[host]; exists {
				nextRound = append(nextRound, rt)
				continue
			}
			seenHost[host] = struct{}{}
			result = append(result, rt)
		}
		remaining = nextRound
	}
	return result
}

func buildAllCooldownEscapeChain(
	cooled []resolvedTarget,
	upstreamResolver RoutingTargetUpstreamResolver,
	maxAttempts int,
) []resolvedTarget {
	candidates := append([]resolvedTarget(nil), cooled...)
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.fallbackOnly != b.fallbackOnly {
			return !a.fallbackOnly
		}
		aFatal := a.cooldownReason == OutcomeFatalChannel
		bFatal := b.cooldownReason == OutcomeFatalChannel
		if aFatal != bFatal {
			return !aFatal
		}
		if !a.cooldownUntil.Equal(b.cooldownUntil) {
			if a.cooldownUntil.IsZero() {
				return true
			}
			if b.cooldownUntil.IsZero() {
				return false
			}
			return a.cooldownUntil.Before(b.cooldownUntil)
		}
		return a.order < b.order
	})

	if upstreamResolver != nil {
		candidates = diversifyByUpstream(candidates, upstreamResolver)
	}

	escape := make([]resolvedTarget, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, rt := range candidates {
		if _, ok := seen[rt.key]; ok {
			continue
		}
		seen[rt.key] = struct{}{}
		escape = append(escape, rt)
		if maxAttempts > 0 && len(escape) >= maxAttempts {
			break
		}
	}
	return escape
}

func selectResolvedTargets(targets []configstoreTables.RouteGroupTarget, routingCtx *RoutingContext, layerIndex int, layerName string, slots int) []resolvedTarget {
	if slots <= 0 || len(targets) == 0 {
		return nil
	}
	if selected, ok := selectCostAwareResolvedTargets(targets, routingCtx, layerIndex, layerName, slots); ok {
		return selected
	}
	available := append([]configstoreTables.RouteGroupTarget(nil), targets...)
	selected := make([]resolvedTarget, 0, slots)
	for i := 0; i < slots && len(available) > 0; i++ {
		target, ok := selectWeightedGroupTarget(available)
		if !ok {
			break
		}
		rt := resolveRouteGroupTarget(target, routingCtx, layerIndex, layerName)
		selected = append(selected, rt)

		remaining := available[:0]
		for _, candidate := range available {
			if resolveRouteGroupTarget(candidate, routingCtx, layerIndex, layerName).key != rt.key {
				remaining = append(remaining, candidate)
			}
		}
		available = remaining
	}
	return selected
}

type costAwareRouteGroupTarget struct {
	resolved resolvedTarget
	cost     float64
	score    float64
	hasCost  bool
	index    int
}

func selectCostAwareResolvedTargets(targets []configstoreTables.RouteGroupTarget, routingCtx *RoutingContext, layerIndex int, layerName string, slots int) ([]resolvedTarget, bool) {
	if routingCtx == nil || routingCtx.TargetCostResolver == nil {
		return nil, false
	}

	requestType := schemas.RequestType(routingCtx.RequestType)
	if requestType == "" {
		requestType = schemas.ChatCompletionRequest
	}

	entries := make([]costAwareRouteGroupTarget, 0, len(targets))
	hasAnyCost := false
	for i, target := range targets {
		resolved := resolveRouteGroupTarget(target, routingCtx, layerIndex, layerName)
		cost, ok := routingCtx.TargetCostResolver(schemas.ModelProvider(resolved.provider), resolved.model, requestType)
		score := math.Inf(1)
		if ok {
			hasAnyCost = true
			if target.Weight > 0 {
				score = cost / target.Weight
			} else {
				score = cost + 1e12
			}
		}
		entries = append(entries, costAwareRouteGroupTarget{
			resolved: resolved,
			cost:     cost,
			score:    score,
			hasCost:  ok,
			index:    i,
		})
	}
	if !hasAnyCost {
		return nil, false
	}

	sort.SliceStable(entries, func(i, j int) bool {
		left := entries[i]
		right := entries[j]
		if left.hasCost != right.hasCost {
			return left.hasCost
		}
		if left.hasCost {
			if left.score != right.score {
				return left.score < right.score
			}
			if left.cost != right.cost {
				return left.cost < right.cost
			}
		}
		return left.index < right.index
	})

	selected := make([]resolvedTarget, 0, slots)
	seen := make(map[string]struct{}, slots)
	for _, entry := range entries {
		if len(selected) >= slots {
			break
		}
		if _, ok := seen[entry.resolved.key]; ok {
			continue
		}
		selected = append(selected, entry.resolved)
		seen[entry.resolved.key] = struct{}{}
	}
	return selected, true
}

func selectResolvedTargetsWithRepeats(targets []configstoreTables.RouteGroupTarget, routingCtx *RoutingContext, layerIndex int, layerName string, slots int) []resolvedTarget {
	selected := selectResolvedTargets(targets, routingCtx, layerIndex, layerName, slots)
	distinctCount := len(selected)
	if slots <= 0 || distinctCount == 0 {
		return selected
	}
	for len(selected) < slots {
		selected = append(selected, selected[(len(selected)-distinctCount)%distinctCount])
	}
	return selected
}

// selectWeightedGroupTarget picks one target from the slice using weighted random selection
func selectWeightedGroupTarget(targets []configstoreTables.RouteGroupTarget) (configstoreTables.RouteGroupTarget, bool) {
	if len(targets) == 0 {
		return configstoreTables.RouteGroupTarget{}, false
	}
	if len(targets) == 1 {
		return targets[0], true
	}

	total := 0.0
	for _, t := range targets {
		if t.Weight > 0 {
			total += t.Weight
		}
	}
	if total == 0 {
		return targets[rand.IntN(len(targets))], true
	}

	r := rand.Float64() * total
	cumulative := 0.0
	for _, t := range targets {
		if t.Weight > 0 {
			cumulative += t.Weight
			if r < cumulative {
				return t, true
			}
		}
	}
	return targets[len(targets)-1], true
}

func derefOr(p *string, fallback string) string {
	if p != nil && *p != "" {
		return *p
	}
	return fallback
}
