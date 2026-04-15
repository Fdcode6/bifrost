package governance

import (
	"fmt"
	"math/rand/v2"
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
	return &configstoreTables.HealthPolicy{
		FailureThreshold:     2,
		FailureWindowSeconds: 30,
		CooldownSeconds:      30,
		ConsecutiveFailures:  0, // 0 means fall back to FailureThreshold
	}
}

// buildGroupedRoutingDecision builds a RoutingDecision by selecting healthy targets
// from route groups and constructing a primary + fallback chain.
//
// For each group (in priority order), available (non-cooled-down) targets are selected
// via weighted random (without replacement) up to (1 + retry_limit) slots. The first
// selected target across all groups becomes the primary; subsequent ones fill the
// fallback chain. Targets already selected in an earlier group are skipped (dedup).
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
	if policy == nil {
		policy = defaultHealthPolicy()
	}

	now := time.Now()
	ctx.AppendRoutingEngineLog(routingGroupEngine,
		fmt.Sprintf("Building grouped routing chain for rule '%s', %d groups", rule.Name, len(rule.ParsedRouteGroups)))

	// Collect an ordered list of resolved targets across all groups
	var chain []resolvedTarget
	seen := make(map[string]struct{}) // dedup by TargetKey

	for gi, group := range rule.ParsedRouteGroups {
		// Filter available targets in this group
		available := make([]configstoreTables.RouteGroupTarget, 0, len(group.Targets))
		cooldownCount := 0
		for _, t := range group.Targets {
			resolved := resolveRouteGroupTarget(t, routingCtx, gi, group.Name)
			if _, dup := seen[resolved.key]; dup {
				continue
			}
			if healthTracker.IsInCooldownForRule(rule.ID, resolved.key, policy, now) {
				cooldownCount++
				ctx.AppendRoutingEngineLog(routingGroupEngine,
					fmt.Sprintf("Filtered: %s (cooldown)", resolved.key))
				continue
			}
			available = append(available, t)
		}

		ctx.AppendRoutingEngineLog(routingGroupEngine,
			fmt.Sprintf("Group[%d] %s: targets=%d, available=%d, cooldown=%d",
				gi, group.Name, len(group.Targets), len(available), cooldownCount))

		if len(available) == 0 {
			continue
		}

		// Pick up to (1 + retry_limit) targets from this group via weighted selection without replacement
		slots := 1 + group.RetryLimit
		for i := 0; i < slots && len(available) > 0; i++ {
			target, ok := selectWeightedGroupTarget(available)
			if !ok {
				break
			}
			rt := resolveRouteGroupTarget(target, routingCtx, gi, group.Name)
			chain = append(chain, rt)
			seen[rt.key] = struct{}{}

			ctx.AppendRoutingEngineLog(routingGroupEngine,
				fmt.Sprintf("Selected slot %d from group %s (layer=%d): provider=%s model=%s", len(chain), group.Name, rt.layerIndex, rt.provider, rt.model))

			// Remove selected target for next pick (without replacement)
			remaining := available[:0]
			for _, a := range available {
				if resolveRouteGroupTarget(a, routingCtx, gi, group.Name).key != rt.key {
					remaining = append(remaining, a)
				}
			}
			available = remaining
		}
	}

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
	}
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
