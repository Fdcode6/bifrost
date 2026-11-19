package governance

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	tables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockTierEndpoint struct {
	provider   string
	model      string
	keyID      string
	upstream   string // logical upstream host (shared by split tiers on same relay)
	costPer1M  float64
	timeoutSec int

	mu       sync.Mutex
	handler  func(hit int) (status int, delay time.Duration, body string)
	hitCount int
}

func (m *mockTierEndpoint) reset(fn func(hit int) (status int, delay time.Duration, body string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handler = fn
}

func (m *mockTierEndpoint) hits() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hitCount
}

func (m *mockTierEndpoint) resetHits() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hitCount = 0
}

type smartRoutingSimAccount struct {
	baseURL   string
	endpoints map[string]*mockTierEndpoint
	order     []string
}

func (a *smartRoutingSimAccount) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	out := make([]schemas.ModelProvider, 0, len(a.order))
	for _, name := range a.order {
		out = append(out, schemas.ModelProvider(name))
	}
	return out, nil
}

func (a *smartRoutingSimAccount) GetKeysForProvider(_ context.Context, p schemas.ModelProvider) ([]schemas.Key, error) {
	ep := a.endpoints[string(p)]
	return []schemas.Key{{ID: ep.keyID, Value: *schemas.NewEnvVar("sim-key-" + ep.provider), Weight: 1}}, nil
}

func (a *smartRoutingSimAccount) GetConfigForProvider(p schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	ep := a.endpoints[string(p)]
	timeout := ep.timeoutSec
	if timeout <= 0 {
		timeout = 5
	}
	return &schemas.ProviderConfig{
		NetworkConfig: schemas.NetworkConfig{
			BaseURL:                        a.baseURL + "/" + ep.provider,
			DefaultRequestTimeoutInSeconds: timeout,
			MaxRetries:                     0,
		},
		ConcurrencyAndBufferSize: schemas.ConcurrencyAndBufferSize{Concurrency: 64, BufferSize: 128},
		CustomProviderConfig: &schemas.CustomProviderConfig{
			CustomProviderKey: ep.provider,
			BaseProviderType:  schemas.OpenAI,
		},
	}, nil
}

type smartRoutingHarness struct {
	t         *testing.T
	server    *httptest.Server
	client    *bifrost.Bifrost
	tracker   *HealthTracker
	endpoints map[string]*mockTierEndpoint
	order     []string
	rule      *tables.TableRoutingRule
	policy    *tables.HealthPolicy
}

func newSmartRoutingHarness(t *testing.T, policy *tables.HealthPolicy, singlePool bool) *smartRoutingHarness {
	t.Helper()

	okBody := func(model string) string {
		return fmt.Sprintf(`{"id":"chatcmpl-sim","object":"chat.completion","model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"{\"rhythm\":\"ONE_BREATH\",\"text\":\"ok\"}"},"finish_reason":"stop"}]}`, model)
	}

	// Mirrors production split-tier topology: 8 channels across 5 independent upstream hosts!
	defs := []*mockTierEndpoint{
		{provider: "StoreAPI", model: "gemini-3.1-pro-preview", keyID: "k-store", upstream: "www.storeapi.one", costPer1M: 0.044 + 0.265},
		{provider: "NightRouter", model: "gemini-3.1-pro-preview", keyID: "k-nr", upstream: "api.nightrouter.ai", costPer1M: 0.054 + 0.324},
		{provider: "云雾API", model: "gemini-3.1-pro-preview", keyID: "k-yw", upstream: "api.openlux.ai", costPer1M: 0.217 + 1.304},
		{provider: "poloapi", model: "gemini-3.1-pro-preview", keyID: "k-polo", upstream: "polo.zdfan.com", costPer1M: 0.588 + 3.529},
		{provider: "B.AI", model: "gemini-3.1-pro", keyID: "k-bai", upstream: "bai.zdfan.com", costPer1M: 0.800 + 4.800},
		{provider: "B.AI-6fold", model: "gemini-3.1-pro", keyID: "k-bai6", upstream: "bai.zdfan.com", costPer1M: 1.200 + 7.200},
		{provider: "B.AI-7fold", model: "gemini-3.1-pro", keyID: "k-bai7", upstream: "bai.zdfan.com", costPer1M: 1.400 + 8.400},
		{provider: "poloapi_vip", model: "gemini-3.1-pro-preview", keyID: "k-pvip", upstream: "polo.zdfan.com", costPer1M: 1.618 + 9.706},
	}

	endpoints := make(map[string]*mockTierEndpoint, len(defs))
	order := make([]string, 0, len(defs))
	for _, ep := range defs {
		ep := ep
		ep.handler = func(hit int) (int, time.Duration, string) {
			return http.StatusOK, 2 * time.Millisecond, okBody(ep.model)
		}
		endpoints[ep.provider] = ep
		order = append(order, ep.provider)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trimmed := strings.TrimPrefix(r.URL.Path, "/")
		parts := strings.SplitN(trimmed, "/", 2)
		providerName := parts[0]
		ep, ok := endpoints[providerName]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":{"message":"unknown sim provider"}}`)
			return
		}
		ep.mu.Lock()
		ep.hitCount++
		hit := ep.hitCount
		fn := ep.handler
		ep.mu.Unlock()

		status, delay, body := fn(hit)
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)

	account := &smartRoutingSimAccount{
		baseURL:   server.URL,
		endpoints: endpoints,
		order:     order,
	}
	client, err := bifrost.Init(context.Background(), schemas.BifrostConfig{
		Account: account,
		Logger:  bifrost.NewDefaultLogger(schemas.LogLevelError),
	})
	require.NoError(t, err)
	t.Cleanup(client.Shutdown)

	policy = ApplyHealthPolicyDefaults(policy)
	rule := &tables.TableRoutingRule{
		ID:                 "rule-gemini-auto-sim",
		Name:               "gemini-auto",
		Enabled:            true,
		ParsedHealthPolicy: policy,
	}
	if singlePool {
		// Deliberately insert in reverse (most expensive first) to prove automatic cost sorting!
		targets := make([]tables.RouteGroupTarget, 0, len(defs))
		for i := len(defs) - 1; i >= 0; i-- {
			ep := defs[i]
			targets = append(targets, tables.RouteGroupTarget{
				Provider: bifrost.Ptr(ep.provider),
				Model:    bifrost.Ptr(ep.model),
				KeyID:    bifrost.Ptr(ep.keyID),
				Weight:   1,
			})
		}
		rule.ParsedRouteGroups = []tables.RouteGroup{{
			Name:       "Price-First Auto Pool",
			RetryLimit: 0,
			Targets:    targets,
		}}
	} else {
		for _, ep := range defs {
			rule.ParsedRouteGroups = append(rule.ParsedRouteGroups, tables.RouteGroup{
				Name:       ep.provider,
				RetryLimit: 0,
				Targets: []tables.RouteGroupTarget{{
					Provider: bifrost.Ptr(ep.provider),
					Model:    bifrost.Ptr(ep.model),
					KeyID:    bifrost.Ptr(ep.keyID),
					Weight:   1,
				}},
			})
		}
	}

	return &smartRoutingHarness{
		t:         t,
		server:    server,
		client:    client,
		tracker:   NewHealthTracker(),
		endpoints: endpoints,
		order:     order,
		rule:      rule,
		policy:    policy,
	}
}

type simCallResult struct {
	decision      *RoutingDecision
	resp          *schemas.BifrostChatResponse
	err           *schemas.BifrostError
	attempts      []string
	winner        string
	totalDuration time.Duration
}

// execute runs one full virtual-model request through grouped decision building,
// real Bifrost core fallback execution, and per-attempt PostLLM outcome tracking.
func (h *smartRoutingHarness) execute(model string) simCallResult {
	h.t.Helper()
	routingCtx := &RoutingContext{
		Provider:    schemas.OpenAI,
		Model:       model,
		RequestType: string(schemas.ChatCompletionRequest),
		TargetCostResolver: func(provider schemas.ModelProvider, _ string, _ schemas.RequestType) (float64, bool) {
			ep, ok := h.endpoints[string(provider)]
			if !ok {
				return 0, false
			}
			return ep.costPer1M / 1e6, true
		},
		TargetUpstreamResolver: func(provider schemas.ModelProvider) string {
			if ep, ok := h.endpoints[string(provider)]; ok {
				return ep.upstream
			}
			return string(provider)
		},
	}

	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	decision := buildGroupedRoutingDecision(ctx, h.rule, routingCtx, h.tracker, NewMockLogger())
	require.NotNil(h.t, decision)

	plans := append([]RoutingLayerPlan{decision.PrimaryLayer}, decision.FallbackLayerPlan...)
	budgetMs := effectiveFallbackBudgetMs(h.policy, routingCtx)
	var deadline time.Time
	if budgetMs > 0 {
		deadline = time.Now().Add(time.Duration(budgetMs) * time.Millisecond)
	}

	started := time.Now()
	var attempts []string
	var lastErr *schemas.BifrostError
	for idx, plan := range plans {
		if idx > 0 && !deadline.IsZero() && !time.Now().Before(deadline) {
			break
		}
		attempts = append(attempts, plan.Provider)

		attemptCtx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
		attemptCtx.SetValue(schemas.BifrostContextKeyDisableProviderRetries, true)
		attemptCtx.SetValue(schemas.BifrostContextKeyAPIKeyID, plan.KeyID)

		req := &schemas.BifrostChatRequest{
			Provider: schemas.ModelProvider(plan.Provider),
			Model:    plan.Model,
			Input: []schemas.ChatMessage{{
				Role:    schemas.ChatMessageRoleUser,
				Content: &schemas.ChatMessageContent{ContentStr: bifrost.Ptr("hello")},
			}},
		}
		attemptStart := time.Now()
		resp, bfErr := h.client.ChatCompletionRequest(attemptCtx, req)
		latency := time.Since(attemptStart)
		now := time.Now()

		targetKey := TargetKey(plan.Provider, plan.Model, plan.KeyID)
		h.tracker.RecordRealAccess(targetKey, schemas.ChatCompletionRequest, now)
		kind := ClassifyOutcome(bfErr, latency, time.Duration(h.policy.SlowThresholdMs)*time.Millisecond)
		h.tracker.RecordOutcome(h.rule.ID, targetKey, kind, latency, errorMessage(bfErr), h.policy, now)

		if bfErr == nil && resp != nil {
			return simCallResult{
				decision:      decision,
				resp:          resp,
				attempts:      attempts,
				winner:        string(resp.ExtraFields.Provider),
				totalDuration: time.Since(started),
			}
		}
		lastErr = bfErr
	}
	return simCallResult{
		decision:      decision,
		err:           lastErr,
		attempts:      attempts,
		totalDuration: time.Since(started),
	}
}

func recommendedProductionPolicy() *tables.HealthPolicy {
	return &tables.HealthPolicy{
		FailureThreshold:         5,
		FailureWindowSeconds:     120,
		CooldownSeconds:          1, // short in local simulation for fast deterministic execution
		ConsecutiveFailures:      4,
		FatalConsecutiveFailures: 2,
		FatalCooldownSeconds:     30,
		ErrorRateWindowSize:      20,
		ErrorRateMinSamples:      5,
		ErrorRateThreshold:       0.4,
		SlowThresholdMs:          500,
		SlowWindowSize:           10,
		SlowRatioThreshold:       0.5,
		SoftCooldownMultiplier:   1,
		CooldownBackoffFactor:    1.5,
		CooldownMaxSeconds:       120,
		RequestDeadlineMs:        135000,
		MaxFallbackAttempts:      6,
	}
}

func TestSmartRoutingSimulation_SinglePoolPriceFirstAndFatalCircuitBreak(t *testing.T) {
	h := newSmartRoutingHarness(t, recommendedProductionPolicy(), true)

	// Scenario 1: All healthy -> 100% traffic goes to cheapest StoreAPI ($0.044+$0.265)
	for i := 0; i < 5; i++ {
		res := h.execute("gemini-auto")
		require.Nil(t, res.err)
		assert.Equal(t, "StoreAPI", res.winner)
		assert.Equal(t, []string{"StoreAPI"}, res.attempts)
	}
	assert.Equal(t, 5, h.endpoints["StoreAPI"].hits())
	assert.Equal(t, 0, h.endpoints["NightRouter"].hits())

	// Scenario 2: StoreAPI fails with 503 fallback_skip_json AND NightRouter runs out of subscription (403)
	// AND B.AI returns 400 credit insufficient balance!
	h.endpoints["StoreAPI"].reset(func(int) (int, time.Duration, string) {
		return http.StatusServiceUnavailable, 1 * time.Millisecond,
			`{"error":{"type":"invalid_request_error","code":"fallback_skip_json","message":"this route does not support JSON mode"}}`
	})
	h.endpoints["NightRouter"].reset(func(int) (int, time.Duration, string) {
		return http.StatusForbidden, 1 * time.Millisecond,
			`{"error":{"type":"new_api_error","code":"insufficient_user_quota","message":"You have no active subscription"}}`
	})

	// First 2 requests immediately fallback in milliseconds to 云雾API ($0.217) with 100% client success!
	for i := 0; i < 2; i++ {
		res := h.execute("gemini-auto")
		require.Nil(t, res.err)
		assert.Equal(t, "云雾API", res.winner)
		assert.Equal(t, []string{"StoreAPI", "NightRouter", "云雾API"}, res.attempts)
	}

	// Request 3+ must completely skip StoreAPI and NightRouter (both hit FatalConsecutiveFailures=2)!
	storeHitsBefore := h.endpoints["StoreAPI"].hits()
	nrHitsBefore := h.endpoints["NightRouter"].hits()
	for i := 0; i < 10; i++ {
		res := h.execute("gemini-auto")
		require.Nil(t, res.err)
		assert.Equal(t, "云雾API", res.winner)
		assert.Equal(t, []string{"云雾API"}, res.attempts)
	}
	assert.Equal(t, storeHitsBefore, h.endpoints["StoreAPI"].hits(), "fatal-cooled StoreAPI must receive 0 requests")
	assert.Equal(t, nrHitsBefore, h.endpoints["NightRouter"].hits(), "fatal-cooled NightRouter must receive 0 requests")
}

func TestSmartRoutingSimulation_SporadicRelaySubAccount403DoesNotKillCheapChannel(t *testing.T) {
	h := newSmartRoutingHarness(t, recommendedProductionPolicy(), true)
	// Disable StoreAPI so NightRouter is #1
	h.endpoints["StoreAPI"].reset(func(int) (int, time.Duration, string) {
		return http.StatusForbidden, 1 * time.Millisecond, `{"error":{"message":"User has been banned"}}`
	})
	// NightRouter fails ONLY on hit 3 and hit 8 (mimicking 1 bad sub-account in relay pool)
	h.endpoints["NightRouter"].reset(func(hit int) (int, time.Duration, string) {
		if hit == 3 || hit == 8 {
			return http.StatusForbidden, 1 * time.Millisecond,
				`{"error":{"type":"new_api_error","code":"insufficient_user_quota","message":"You have no active subscription"}}`
		}
		return http.StatusOK, 2 * time.Millisecond,
			`{"id":"ok","object":"chat.completion","model":"gemini-3.1-pro-preview","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`
	})

	// Warm up past StoreAPI fatal cooldown (2 hits)
	for i := 0; i < 2; i++ {
		res := h.execute("gemini-auto")
		require.Nil(t, res.err)
		assert.Equal(t, "NightRouter", res.winner)
	}

	// Run 10 more requests: hits 3 and 8 seamlessly fallback to 云雾API, while all others stay on NightRouter!
	nrWins := 0
	ywWins := 0
	for i := 0; i < 10; i++ {
		res := h.execute("gemini-auto")
		require.Nil(t, res.err, "every single client request must succeed")
		if res.winner == "NightRouter" {
			nrWins++
		} else if res.winner == "云雾API" {
			ywWins++
		}
	}
	assert.Equal(t, 8, nrWins, "sporadic 403 must not falsely kill 80%+ healthy cheap relay")
	assert.Equal(t, 2, ywWins, "the 2 sporadic 403s must be silently recovered by 云雾API")
}

func TestSmartRoutingSimulation_CheapPrimaryCooldownAndInPlaceRevivalNoStarvation(t *testing.T) {
	policy := recommendedProductionPolicy()
	policy.CooldownSeconds = 1 // 1s cooldown so we can test real revival quickly
	h := newSmartRoutingHarness(t, policy, false)

	// StoreAPI suffers transient 503 overload (4 consecutive -> enters 1s short cooldown)
	h.endpoints["StoreAPI"].reset(func(int) (int, time.Duration, string) {
		return http.StatusServiceUnavailable, 1 * time.Millisecond,
			`{"error":{"type":"api_error","code":"model_temporarily_unavailable","message":"The model service is temporarily unavailable."}}`
	})

	for i := 0; i < 4; i++ {
		res := h.execute("gemini-auto")
		require.Nil(t, res.err)
		assert.Equal(t, "NightRouter", res.winner)
	}

	// StoreAPI is now in cooldown: traffic shifts cleanly to NightRouter while 7 other channels are healthy!
	resDuringCooldown := h.execute("gemini-auto")
	require.Nil(t, resDuringCooldown.err)
	assert.Equal(t, "NightRouter", resDuringCooldown.winner)
	assert.Equal(t, []string{"NightRouter"}, resDuringCooldown.attempts)

	// StoreAPI recovers upstream; wait for 1s cooldown to expire
	h.endpoints["StoreAPI"].reset(func(int) (int, time.Duration, string) {
		return http.StatusOK, 2 * time.Millisecond,
			`{"id":"revived","object":"chat.completion","model":"gemini-3.1-pro-preview","choices":[{"index":0,"message":{"role":"assistant","content":"back"},"finish_reason":"stop"}]}`
	})
	time.Sleep(1100 * time.Millisecond)

	// Next request MUST probe StoreAPI IN-PLACE at slot 1 (despite 7 healthy backups!), succeed,
	// and immediately return 100% of subsequent traffic to StoreAPI!
	revival := h.execute("gemini-auto")
	require.Nil(t, revival.err)
	assert.Equal(t, "StoreAPI", revival.winner, "cheapest primary must revive in-place and never starve behind healthy backups")
	assert.Equal(t, []string{"StoreAPI"}, revival.attempts)

	// Follow-up requests all stay on revived StoreAPI
	for i := 0; i < 5; i++ {
		res := h.execute("gemini-auto")
		require.Nil(t, res.err)
		assert.Equal(t, "StoreAPI", res.winner)
	}
}

func TestSmartRoutingSimulation_UpstreamDiversityUnderWholePlatformOutage(t *testing.T) {
	h := newSmartRoutingHarness(t, recommendedProductionPolicy(), true)

	// Cool down StoreAPI, NightRouter, 云雾API, poloapi so B.AI ($0.80), B.AI-6fold ($1.20),
	// B.AI-7fold ($1.40) [all bai.zdfan.com] and poloapi_vip ($1.62) [polo.zdfan.com] remain.
	now := time.Now()
	for _, p := range []string{"StoreAPI", "NightRouter", "云雾API", "poloapi"} {
		ep := h.endpoints[p]
		k := TargetKey(ep.provider, ep.model, ep.keyID)
		h.tracker.RecordOutcome(h.rule.ID, k, OutcomeFatalChannel, 0, "banned", h.policy, now)
		h.tracker.RecordOutcome(h.rule.ID, k, OutcomeFatalChannel, 0, "banned", h.policy, now)
	}

	// Simulate whole api.b.ai outage across ALL three B.AI accounts!
	for _, p := range []string{"B.AI", "B.AI-6fold", "B.AI-7fold"} {
		h.endpoints[p].reset(func(int) (int, time.Duration, string) {
			return http.StatusServiceUnavailable, 1 * time.Millisecond,
				`{"error":{"type":"api_error","code":"model_temporarily_unavailable","message":"The model service is temporarily unavailable."}}`
		})
	}

	// Request tries B.AI (cheapest bai.zdfan.com) -> immediately jumps to independent poloapi_vip!
	// Does NOT waste attempts on B.AI-6fold and B.AI-7fold on the same failing host!
	res := h.execute("gemini-auto")
	require.Nil(t, res.err)
	assert.Equal(t, "poloapi_vip", res.winner)
	assert.Equal(t, []string{"B.AI", "poloapi_vip"}, res.attempts)
	assert.Equal(t, 0, h.endpoints["B.AI-6fold"].hits())
	assert.Equal(t, 0, h.endpoints["B.AI-7fold"].hits())
}

func TestSmartRoutingSimulation_HighConcurrencyMildTransientErrorsAvoidFalseCooldown(t *testing.T) {
	policy := recommendedProductionPolicy()
	h := newSmartRoutingHarness(t, policy, true)

	// Primary StoreAPI fails 10% of requests (every 10th request times out/503) under 50 concurrent callers.
	// ErrorRateWindowSize=20, ErrorRateThreshold=0.4 -> 10% must NEVER falsely cool down StoreAPI,
	// while fallback catches every single failed attempt for 100% client success!
	var counter atomic.Int64
	h.endpoints["StoreAPI"].reset(func(int) (int, time.Duration, string) {
		n := counter.Add(1)
		if n%10 == 0 {
			return http.StatusServiceUnavailable, 5 * time.Millisecond,
				`{"error":{"message":"The model service is temporarily unavailable."}}`
		}
		return http.StatusOK, 5 * time.Millisecond,
			`{"id":"ok","object":"chat.completion","model":"gemini-3.1-pro-preview","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`
	})

	const total = 50
	var wg sync.WaitGroup
	var succeeded atomic.Int64
	var storeWins atomic.Int64
	var nrWins atomic.Int64

	for i := 0; i < total; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := h.execute("gemini-auto")
			if res.err == nil {
				succeeded.Add(1)
				if res.winner == "StoreAPI" {
					storeWins.Add(1)
				} else if res.winner == "NightRouter" {
					nrWins.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int64(total), succeeded.Load(), "100% of concurrent requests must succeed via instant fallback")
	assert.GreaterOrEqual(t, storeWins.Load(), int64(40), "primary should retain ~90% of traffic without false cooldown")
	assert.GreaterOrEqual(t, nrWins.Load(), int64(1), "fallback should smoothly absorb the 10% transient errors")
	storeKey := TargetKey("StoreAPI", "gemini-3.1-pro-preview", "k-store")
	assert.Equal(t, HealthHealthy, h.tracker.GetTargetHealthForRule(h.rule.ID, storeKey, policy, time.Now()))
}

func TestSmartRoutingSimulation_AllCooldownGracefulEscapeRecovers(t *testing.T) {
	policy := recommendedProductionPolicy()
	h := newSmartRoutingHarness(t, policy, false)
	now := time.Now()

	// Put ALL 8 channels into active cooldown:
	// First 7 are fatal-banned (30s cooldown), last 1 (云雾API) had a transient timeout (10s remaining)
	for _, ep := range h.endpoints {
		k := TargetKey(ep.provider, ep.model, ep.keyID)
		if ep.provider == "云雾API" {
			h.tracker.RecordOutcome(h.rule.ID, k, OutcomeSoftFail, 0, "timeout", policy, now)
			h.tracker.RecordOutcome(h.rule.ID, k, OutcomeSoftFail, 0, "timeout", policy, now)
			h.tracker.RecordOutcome(h.rule.ID, k, OutcomeSoftFail, 0, "timeout", policy, now)
			h.tracker.RecordOutcome(h.rule.ID, k, OutcomeSoftFail, 0, "timeout", policy, now)
			// trigger cooldown via inspection
			h.tracker.IsInCooldownForRule(h.rule.ID, k, policy, now)
		} else {
			h.tracker.RecordOutcome(h.rule.ID, k, OutcomeFatalChannel, 0, "User has been banned", policy, now)
			h.tracker.RecordOutcome(h.rule.ID, k, OutcomeFatalChannel, 0, "User has been banned", policy, now)
		}
	}

	// Execute request while ALL 8 channels are in cooldown:
	// Must NOT return nil / provider-model error; must prioritize transient-cooled 云雾API and succeed!
	res := h.execute("gemini-auto")
	require.Nil(t, res.err)
	assert.Equal(t, "云雾API", res.winner)
	assert.Equal(t, []string{"云雾API"}, res.attempts)
}
