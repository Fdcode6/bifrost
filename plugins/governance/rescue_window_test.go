package governance

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	tables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/require"
)

func TestV22RescueWindow(t *testing.T) {
	h := NewHealthTracker()
	now := time.Unix(1800000, 0)
	for _, v := range []struct {
		k OutcomeKind
		d time.Duration
	}{{OutcomeSuccess, 16 * time.Second}, {OutcomeSlow, 17 * time.Second}, {OutcomeSoftFail, 3 * time.Second}, {OutcomeIgnore, time.Second}} {
		h.observeRescue("r", "k", false, v.k, v.d, now)
	}
	h.observeRescue("r", "k", true, OutcomeSuccess, time.Second, now)
	require.Equal(t, rescueCounts{3, 1}, h.recentRescue("r", "k", false, now))
	require.Equal(t, rescueCounts{1, 1}, h.recentRescue("r", "k", true, now.Add(9*time.Minute)))
	require.Zero(t, h.recentRescue("r", "k", false, now.Add(10*time.Minute)))
	require.Zero(t, h.recentRescue("other", "k", true, now))
}

func TestV22CostOrderAndQueueAttribution(t *testing.T) {
	now := time.Now()
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	p := makeAdaptiveTestPolicy()
	p.AdaptiveV22Enabled = true
	target := func(n string, ms int) tables.RouteGroupTarget {
		return tables.RouteGroupTarget{Provider: &n, Model: schemas.Ptr("pro"), KeyID: &n, Weight: 1, PrimaryAttemptTimeoutMs: ms}
	}
	rule := &tables.TableRoutingRule{ID: "r", ParsedRouteGroups: []tables.RouteGroup{
		{Name: "regular", Targets: []tables.RouteGroupTarget{target("cheap", 22000), target("main", 28000)}},
		{Name: "backup", FallbackOnly: true, Targets: []tables.RouteGroupTarget{target("backup", 0)}},
		{Name: "last", LastResort: true, Targets: []tables.RouteGroupTarget{target("retail", 0)}},
	}}
	prices := map[schemas.ModelProvider]float64{"cheap": 0.0221, "main": 0.4324, "backup": 0.70, "retail": 1}
	rc := &RoutingContext{Model: "gemini-auto", RequestType: string(schemas.ChatCompletionRequest), TargetCostResolver: func(n schemas.ModelProvider, _ string, _ schemas.RequestType) (float64, bool) { return prices[n], true }}
	h := NewHealthTracker()
	for n := range prices {
		key := TargetKey(string(n), "pro", string(n))
		for i := 0; i < 10; i++ {
			h.RecordOutcome(rule.ID, key, OutcomeSuccess, time.Second, "", p, now)
			d := time.Second
			if n == "main" && i >= 8 {
				d = 18 * time.Second
			}
			h.observeRescue(rule.ID, key, false, OutcomeSuccess, d, now)
		}
	}
	decision := buildAdaptiveGroupedRoutingDecision(ctx, rule, rc, h, p, now)
	require.Equal(t, "cheap", decision.Provider)
	require.Equal(t, []string{"backup", "main", "retail"}, decision.FallbackKeyIDs)
	hints := decision.AdaptivePolicy.FallbackHints
	require.Zero(t, hints[0].ContinuationRank)
	require.Positive(t, hints[1].ContinuationRank)
	require.Equal(t, 28000, hints[1].AttemptTimeoutMs)
	require.Zero(t, hints[2].ContinuationRank)

	hint := hints[1]
	key := TargetKey("main", "pro", "main")
	err := &schemas.BifrostError{LocalQueueTimeout: true, StatusCode: schemas.Ptr(504), Error: &schemas.ErrorField{Message: "deadline exceeded"}}
	for _, stream := range []bool{false, true} {
		before := h.recentRescue(rule.ID, key, stream, now)
		for range 2 {
			hint.Observe(ctx, stream, 20*time.Second, err)
		}
		require.Equal(t, before, h.recentRescue(rule.ID, key, stream, time.Now()))
		require.True(t, hint.Available())
	}
	err.LocalQueueTimeout = false
	for range 2 {
		hint.Observe(ctx, true, 20*time.Second, err)
	}
	require.False(t, hint.Available())
	require.Equal(t, 2, h.recentRescue(rule.ID, key, true, time.Now()).attempts)
}

func TestV22RuleAliasesRankAndReload(t *testing.T) {
	store, err := NewLocalGovernanceStore(context.Background(), NewMockLogger(), nil, &configstore.GovernanceConfig{}, nil)
	require.NoError(t, err)
	h := NewHealthTracker()
	engine, err := NewRoutingEngine(store, NewMockLogger(), h)
	require.NoError(t, err)
	now := time.Now()
	policy := makeAdaptiveTestPolicy()
	policy.PrimaryAttemptTimeoutMs = 35000
	policy.RequestDeadlineMs = 58000
	policy.AdaptiveV22Enabled = true
	target := func(p string, limit int) tables.RouteGroupTarget {
		return tables.RouteGroupTarget{Provider: schemas.Ptr(p), Model: schemas.Ptr("pro"), KeyID: schemas.Ptr(p + "-key"), PrimaryAttemptTimeoutMs: limit, Weight: 1}
	}
	rule := &tables.TableRoutingRule{ID: "rule", Scope: "global", Name: "gemini-auto", Enabled: true, GroupedRoutingEnabled: true, CelExpression: "model in ['gemini-auto', 'gemini-auto-bif']", ParsedHealthPolicy: policy, ParsedRouteGroups: []tables.RouteGroup{
		{Name: "primary", Targets: []tables.RouteGroupTarget{target("cheap", 22000)}},
		{Name: "backup", FallbackOnly: true, Targets: []tables.RouteGroupTarget{target("busy", 28000), target("fast", 0)}},
		{Name: "last", FallbackOnly: true, LastResort: true, Targets: []tables.RouteGroupTarget{target("retail", 0)}},
	}}
	require.NoError(t, store.UpdateRoutingRuleInMemory(rule))
	plugin, httpStore := newGovernancePluginForRoutingTests(t)
	require.NoError(t, httpStore.UpdateRoutingRuleInMemory(rule))
	for _, alias := range []string{"gemini-auto", "gemini-auto-bif"} {
		for _, stream := range []bool{false, true} {
			req := &schemas.HTTPRequest{Path: "/v1/chat/completions", Method: "POST", Headers: map[string]string{"Content-Type": "application/json"}, Body: []byte(fmt.Sprintf(`{"model":%q,"stream":%t,"messages":[{"role":"user","content":"ok"}]}`, alias, stream))}
			requestCtx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
			reply, err := plugin.HTTPTransportPreHook(requestCtx, req)
			require.NoError(t, err)
			require.Nil(t, reply)
			var payload struct {
				Model     string
				Fallbacks []string
			}
			require.NoError(t, json.Unmarshal(req.Body, &payload))
			require.Equal(t, "cheap/pro", payload.Model)
			require.Len(t, payload.Fallbacks, 3)
			p := requestCtx.Value(schemas.BifrostContextKeyAdaptiveFallbackPolicy).(*schemas.AdaptiveFallbackRuntimePolicy)
			require.True(t, p.V22)
			require.Equal(t, 22000, p.PrimaryHint.AttemptTimeoutMs)
			require.Equal(t, "cheap-key", requestCtx.Value(schemas.BifrostContextKeyAPIKeyID))
			require.Equal(t, 58*time.Second, requestCtx.Value(schemas.BifrostContextKeyFallbackDeadline).(time.Time).Sub(p.RequestStartTime))
		}
	}
	for i := 0; i < 8; i++ {
		for _, p := range []string{"fast", "busy"} {
			k := TargetKey(p, "pro", p+"-key")
			h.RecordOutcome("rule", k, OutcomeSuccess, 5*time.Second, "", policy, now)
			kind := OutcomeSuccess
			if p == "busy" && i > 1 {
				kind = OutcomeSoftFail
			}
			h.observeRescue("rule", k, false, kind, 5*time.Second, now)
		}
	}
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	denied := ""
	rc := &RoutingContext{TargetAvailable: func(p, m, k string) bool { return denied != "*" && p != denied }, RequestType: string(schemas.ChatCompletionRequest)}
	rc.TargetUpstreamResolver = func(p schemas.ModelProvider) string {
		if p == "cheap" || p == "fast" {
			return "shared"
		}
		return string(p)
	}
	var started *RoutingDecision
	for _, alias := range []string{"gemini-auto", "gemini-auto-bif"} {
		rc.Model = alias
		started, err = engine.EvaluateRoutingRules(ctx, rc)
		require.NoError(t, err)
		require.NotNil(t, started)
		require.True(t, started.AdaptivePolicy.V22)
		require.Equal(t, 22000, started.AdaptivePolicy.PrimaryHint.AttemptTimeoutMs)
		require.Equal(t, []string{"fast/pro", "busy/pro", "retail/pro"}, started.Fallbacks)
		require.Equal(t, []string{"fast-key", "busy-key", "retail-key"}, started.FallbackKeyIDs)
	}
	denied = "fast"
	require.False(t, started.AdaptivePolicy.FallbackHints[0].Available())
	denied = ""
	for i := 0; i < 2; i++ {
		h.RecordOutcome("rule", TargetKey("fast", "pro", "fast-key"), OutcomeSoftFail, 20*time.Second, "504 timeout", policy, now)
	}
	require.False(t, started.AdaptivePolicy.FallbackHints[0].Available())
	rc.Stream = true
	streamDecision, err := engine.EvaluateRoutingRules(ctx, rc)
	require.NoError(t, err)
	require.NotNil(t, streamDecision)
	require.NotContains(t, streamDecision.Fallbacks, "fast/pro")
	denied = "*"
	_, err = engine.EvaluateRoutingRules(ctx, rc)
	require.ErrorIs(t, err, errNoAdaptiveTargets)
	denied = ""
	disabled := *policy
	disabled.AdaptiveV22Enabled = false
	rolled := *rule
	rolled.ParsedHealthPolicy = &disabled
	require.NoError(t, store.UpdateRoutingRuleInMemory(&rolled))
	after, err := engine.EvaluateRoutingRules(ctx, rc)
	require.NoError(t, err)
	require.NotNil(t, after)
	require.False(t, after.AdaptivePolicy.V22)
	require.Equal(t, 35000, after.AdaptivePolicy.PrimaryHint.AttemptTimeoutMs)
	require.True(t, started.AdaptivePolicy.V22)
}

func TestV22PinnedKeyPermissions(t *testing.T) {
	p := &GovernancePlugin{resolver: &BudgetResolver{}}
	vk := &tables.TableVirtualKey{ProviderConfigs: []tables.TableVirtualKeyProviderConfig{{Provider: "relay", AllowedModels: []string{"pro"}, Keys: []tables.TableKey{{KeyID: "allowed"}}}}}
	require.True(t, p.routeTargetAvailable(vk, "relay", "pro", "allowed"))
	require.False(t, p.routeTargetAvailable(vk, "relay", "other", "allowed"))
	require.False(t, p.routeTargetAvailable(vk, "relay", "pro", "denied"))
	require.False(t, p.routeTargetAvailable(vk, "other", "pro", "allowed"))
	plugin, store := newGovernancePluginForRoutingTests(t)
	vk.ID, vk.Name, vk.Value, vk.IsActive = "test", "test", "test-key", true
	store.UpdateVirtualKeyInMemory(vk, nil, nil, nil)
	require.True(t, plugin.routeTargetAvailable(vk, "relay", "pro", "allowed"))
	revoked := *vk
	revoked.IsActive = false
	store.UpdateVirtualKeyInMemory(&revoked, nil, nil, nil)
	require.False(t, plugin.routeTargetAvailable(vk, "relay", "pro", "allowed"))
}
