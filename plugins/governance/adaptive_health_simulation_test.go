package governance

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type simulatedRelay struct {
	server *httptest.Server

	mu             sync.Mutex
	delay          time.Duration
	status         int
	hits           int
	statusSequence []int
}

func newSimulatedRelay(t *testing.T, delay time.Duration, status int) *simulatedRelay {
	t.Helper()
	relay := &simulatedRelay{
		delay:  delay,
		status: status,
	}
	relay.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relay.mu.Lock()
		relay.hits++
		delay := relay.delay
		status := relay.status
		if len(relay.statusSequence) > 0 {
			status = relay.statusSequence[0]
			if len(relay.statusSequence) > 1 {
				relay.statusSequence = relay.statusSequence[1:]
			}
		}
		relay.mu.Unlock()

		if delay > 0 {
			time.Sleep(delay)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status >= 400 {
			_, _ = w.Write([]byte(`{"error":{"message":"current group is saturated"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"chatcmpl-sim","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(relay.server.Close)
	return relay
}

func (r *simulatedRelay) set(delay time.Duration, status int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.delay = delay
	r.status = status
	r.statusSequence = nil
}

func (r *simulatedRelay) setStatusSequence(delay time.Duration, statuses ...int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.delay = delay
	r.statusSequence = append([]int(nil), statuses...)
	if len(statuses) > 0 {
		r.status = statuses[len(statuses)-1]
	}
}

func (r *simulatedRelay) hitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hits
}

type simulatedRequestResult struct {
	decision      *RoutingDecision
	success       bool
	providersHit  []string
	lastErrorCode int
}

func TestAdaptiveHealthRoutingSimulation_LocalRelayEndpoints(t *testing.T) {
	recovery := 0
	policy := ApplyHealthPolicyDefaults(&configstoreTables.HealthPolicy{
		FailureThreshold:     1,
		FailureWindowSeconds: 30,
		CooldownSeconds:      30,
		SlowThresholdMs:      40,
		SlowWindowSize:       4,
		SlowRatioThreshold:   0.5,
		SlowRecoverySeconds:  &recovery,
	})
	rule := &configstoreTables.TableRoutingRule{
		ID:                 "rule-local-relay-simulation",
		Name:               "local relay simulation",
		ParsedHealthPolicy: policy,
		ParsedRouteGroups: []configstoreTables.RouteGroup{
			{Name: "cheap", Targets: []configstoreTables.RouteGroupTarget{{
				Provider: bifrost.Ptr("cheap-relay"),
				Model:    bifrost.Ptr("gemini-auto"),
				KeyID:    bifrost.Ptr("cheap-key"),
				Weight:   1,
			}}},
			{Name: "fast", Targets: []configstoreTables.RouteGroupTarget{{
				Provider: bifrost.Ptr("fast-relay"),
				Model:    bifrost.Ptr("gemini-auto"),
				KeyID:    bifrost.Ptr("fast-key"),
				Weight:   1,
			}}},
			{Name: "quality-fallback", RetryLimit: 2, FallbackOnly: true, Targets: []configstoreTables.RouteGroupTarget{{
				Provider: bifrost.Ptr("quality-relay"),
				Model:    bifrost.Ptr("gemini-auto"),
				KeyID:    bifrost.Ptr("quality-key"),
				Weight:   1,
			}}},
		},
	}
	routingCtx := &RoutingContext{
		Provider:    schemas.ModelProvider("mock-entrypoint"),
		Model:       "gemini-auto",
		RequestType: string(schemas.ChatCompletionRequest),
		Headers:     map[string]string{},
		QueryParams: map[string]string{},
	}
	tracker := NewHealthTracker()
	relays := map[string]*simulatedRelay{
		"cheap-relay":   newSimulatedRelay(t, 70*time.Millisecond, http.StatusOK),
		"fast-relay":    newSimulatedRelay(t, 1*time.Millisecond, http.StatusOK),
		"quality-relay": newSimulatedRelay(t, 1*time.Millisecond, http.StatusOK),
	}

	first := runSimulatedGroupedRoutingRequest(t, rule, routingCtx, tracker, relays)
	second := runSimulatedGroupedRoutingRequest(t, rule, routingCtx, tracker, relays)
	require.True(t, first.success)
	require.True(t, second.success)
	assert.Equal(t, "cheap-relay", first.decision.Provider)
	assert.Equal(t, "cheap-relay", second.decision.Provider)

	cheapKey := TargetKey("cheap-relay", "gemini-auto", "cheap-key")
	cheapStatus := tracker.GetTargetStatusForRule(rule.ID, cheapKey, policy, time.Now())
	assert.Equal(t, string(HealthDegraded), cheapStatus.HealthLevel)
	assert.GreaterOrEqual(t, cheapStatus.P95LatencyMsValue(), int64(40))

	third := runSimulatedGroupedRoutingRequest(t, rule, routingCtx, tracker, relays)
	require.True(t, third.success)
	assert.Equal(t, "fast-relay", third.decision.Provider)
	assert.Equal(t, []string{"cheap-relay/gemini-auto", "quality-relay/gemini-auto", "quality-relay/gemini-auto", "quality-relay/gemini-auto"}, third.decision.Fallbacks)
	assert.Equal(t, 0, relays["quality-relay"].hitCount(), "fallback_only relay should not be used while any regular target is routable")

	relays["cheap-relay"].set(1*time.Millisecond, http.StatusServiceUnavailable)
	relays["fast-relay"].set(1*time.Millisecond, http.StatusServiceUnavailable)
	relays["quality-relay"].setStatusSequence(1*time.Millisecond, http.StatusServiceUnavailable, http.StatusServiceUnavailable, http.StatusOK)

	fourth := runSimulatedGroupedRoutingRequest(t, rule, routingCtx, tracker, relays)
	require.True(t, fourth.success)
	assert.Equal(t, []string{"fast-relay", "cheap-relay", "quality-relay", "quality-relay", "quality-relay"}, fourth.providersHit)
	assert.Equal(t, http.StatusServiceUnavailable, fourth.lastErrorCode)

	fifth := runSimulatedGroupedRoutingRequest(t, rule, routingCtx, tracker, relays)
	require.True(t, fifth.success)
	assert.Equal(t, "quality-relay", fifth.decision.Provider)
	assert.Equal(t, "quality-key", fifth.decision.KeyID)
	assert.Equal(t, 4, relays["quality-relay"].hitCount())

	cheapCooldown := tracker.GetTargetStatusForRule(rule.ID, cheapKey, policy, time.Now())
	fastCooldown := tracker.GetTargetStatusForRule(rule.ID, TargetKey("fast-relay", "gemini-auto", "fast-key"), policy, time.Now())
	assert.Equal(t, "cooldown", cheapCooldown.Status)
	assert.Equal(t, "cooldown", fastCooldown.Status)
}

func runSimulatedGroupedRoutingRequest(
	t *testing.T,
	rule *configstoreTables.TableRoutingRule,
	routingCtx *RoutingContext,
	tracker *HealthTracker,
	relays map[string]*simulatedRelay,
) simulatedRequestResult {
	t.Helper()

	ctx := schemas.NewBifrostContext(context.Background(), time.Now())
	decision := buildGroupedRoutingDecision(ctx, rule, routingCtx, tracker, NewMockLogger())
	require.NotNil(t, decision)

	client := &http.Client{Timeout: 2 * time.Second}
	plans := append([]RoutingLayerPlan{decision.PrimaryLayer}, decision.FallbackLayerPlan...)
	result := simulatedRequestResult{decision: decision}
	for _, plan := range plans {
		relay, ok := relays[plan.Provider]
		require.Truef(t, ok, "missing simulated relay for provider %s", plan.Provider)

		result.providersHit = append(result.providersHit, plan.Provider)
		start := time.Now()
		resp, err := client.Post(
			relay.server.URL+"/v1/chat/completions",
			"application/json",
			strings.NewReader(`{"model":"`+plan.Model+`","messages":[{"role":"user","content":"ping"}]}`),
		)
		latency := time.Since(start)
		now := time.Now()
		targetKey := TargetKey(plan.Provider, plan.Model, plan.KeyID)
		tracker.RecordRealAccess(targetKey, schemas.ChatCompletionRequest, now)

		var bfErr *schemas.BifrostError
		if err != nil {
			bfErr = &schemas.BifrostError{Error: &schemas.ErrorField{Message: err.Error()}}
		} else {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode >= 400 {
				result.lastErrorCode = resp.StatusCode
				bfErr = testBifrostError(resp.StatusCode, http.StatusText(resp.StatusCode))
			}
		}

		kind := ClassifyOutcome(bfErr, latency, time.Duration(rule.ParsedHealthPolicy.SlowThresholdMs)*time.Millisecond)
		tracker.RecordOutcome(rule.ID, targetKey, kind, latency, errorMessage(bfErr), rule.ParsedHealthPolicy, now)
		if kind == OutcomeSuccess || kind == OutcomeSlow {
			result.success = true
			return result
		}
	}
	return result
}

func (snap TargetHealthSnapshot) P95LatencyMsValue() int64 {
	if snap.P95LatencyMs == nil {
		return 0
	}
	return *snap.P95LatencyMs
}
