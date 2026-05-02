package governance

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
)

const routingGroupEngine = "routing-group"

// resolvedTarget holds provider/model/keyID after resolving optional fields against defaults
type resolvedTarget struct {
	provider   string
	model      string
	keyID      string
	key        string // TargetKey for dedup
	layerIndex int
	layerName  string
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

// buildGroupedRoutingDecision builds a RoutingDecision by selecting healthy targets
// from route groups and constructing a primary + fallback chain.
//
// For each group (in priority order), available (non-cooled-down) targets are selected
// via weighted random (without replacement) up to (1 + retry_limit) slots. The first
// selected target across all groups becomes the primary; subsequent ones fill the
// fallback chain. Targets already selected in an earlier group are skipped (dedup).
// Fallback-only groups never outrank regular healthy/degraded/half-open targets,
// but they are appended to the end of the same request fallback chain. Fallback-only
// groups may repeat their selected targets to consume their retry_limit budget.
//
// Returns nil if no targets are available across all groups.
func buildGroupedRoutingDecision(
	ctx *schemas.BifrostContext,
	rule *configstoreTables.TableRoutingRule,
	routingCtx *RoutingContext,
	healthTracker *HealthTracker,
	logger schemas.Logger,
) *RoutingDecision {
	policy := rule.ParsedHealthPolicy
	policy = ApplyHealthPolicyDefaults(policy)

	now := time.Now()
	ctx.AppendRoutingEngineLog(routingGroupEngine,
		fmt.Sprintf("Building grouped routing chain for rule '%s', %d groups", rule.Name, len(rule.ParsedRouteGroups)))

	var healthy []resolvedTarget
	var degraded []resolvedTarget
	var halfOpen []resolvedTarget
	var fallbackOnly []resolvedTarget

	for gi, group := range rule.ParsedRouteGroups {
		groupHealthy := make([]configstoreTables.RouteGroupTarget, 0, len(group.Targets))
		groupDegraded := make([]configstoreTables.RouteGroupTarget, 0, len(group.Targets))
		groupHalfOpen := make([]configstoreTables.RouteGroupTarget, 0, len(group.Targets))
		groupFallbackOnly := make([]configstoreTables.RouteGroupTarget, 0, len(group.Targets))
		cooldownCount := 0
		for _, t := range group.Targets {
			resolved := resolveRouteGroupTarget(t, routingCtx, gi, group.Name)
			level := HealthHealthy
			allowHalfOpen := false
			if healthTracker != nil {
				level, allowHalfOpen = healthTracker.GetTargetRoutingHealthForRule(rule.ID, resolved.key, policy, now)
			}
			if level == HealthCooldown {
				if allowHalfOpen && !group.FallbackOnly {
					groupHalfOpen = append(groupHalfOpen, t)
					ctx.AppendRoutingEngineLog(routingGroupEngine,
						fmt.Sprintf("Half-open tail candidate: %s", resolved.key))
					continue
				}
				cooldownCount++
				ctx.AppendRoutingEngineLog(routingGroupEngine,
					fmt.Sprintf("Filtered: %s (cooldown)", resolved.key))
				continue
			}
			if group.FallbackOnly {
				groupFallbackOnly = append(groupFallbackOnly, t)
				continue
			}
			if level == HealthDegraded {
				groupDegraded = append(groupDegraded, t)
				continue
			}
			groupHealthy = append(groupHealthy, t)
		}

		ctx.AppendRoutingEngineLog(routingGroupEngine,
			fmt.Sprintf("Group[%d] %s: targets=%d, healthy=%d, degraded=%d, half_open_tail=%d, cooldown=%d, fallback_only=%t",
				gi, group.Name, len(group.Targets), len(groupHealthy), len(groupDegraded), len(groupHalfOpen), cooldownCount, group.FallbackOnly))

		slots := 1 + group.RetryLimit
		healthy = append(healthy, selectResolvedTargets(groupHealthy, routingCtx, gi, group.Name, slots)...)
		degraded = append(degraded, selectResolvedTargets(groupDegraded, routingCtx, gi, group.Name, slots)...)
		halfOpen = append(halfOpen, selectResolvedTargets(groupHalfOpen, routingCtx, gi, group.Name, slots)...)
		fallbackOnly = append(fallbackOnly, selectResolvedTargetsWithRepeats(groupFallbackOnly, routingCtx, gi, group.Name, slots)...)
	}

	chain := make([]resolvedTarget, 0, len(healthy)+len(degraded)+len(halfOpen)+len(fallbackOnly))
	seen := make(map[string]struct{})
	appendUnique := func(targets []resolvedTarget) {
		for _, rt := range targets {
			if _, ok := seen[rt.key]; ok {
				continue
			}
			chain = append(chain, rt)
			seen[rt.key] = struct{}{}
			ctx.AppendRoutingEngineLog(routingGroupEngine,
				fmt.Sprintf("Selected slot %d from group %s (layer=%d): provider=%s model=%s", len(chain), rt.layerName, rt.layerIndex, rt.provider, rt.model))
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
				fmt.Sprintf("Selected slot %d from fallback-only group %s (layer=%d): provider=%s model=%s", len(chain), rt.layerName, rt.layerIndex, rt.provider, rt.model))
		}
	}
	appendUnique(healthy)
	appendUnique(degraded)
	appendUnique(halfOpen)
	appendFallbackOnly(fallbackOnly)

	if len(chain) == 0 {
		ctx.AppendRoutingEngineLog(routingGroupEngine, "No available targets across all groups")
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
		fmt.Sprintf("Decision: primary=%s/%s (keyID=%s, layer=%d), fallbacks=%v, fallbackKeyIDs=%v", primary.provider, primary.model, primary.keyID, primary.layerIndex, fallbacks, fallbackKeyIDs))

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
