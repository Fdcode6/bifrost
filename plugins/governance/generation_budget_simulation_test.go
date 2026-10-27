package governance

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	tables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/require"
)

type budgetSimulationAccount struct {
	baseURL string
	timeout int
}

func (a budgetSimulationAccount) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	return []schemas.ModelProvider{"a", "b", "c", "vip"}, nil
}
func (a budgetSimulationAccount) GetKeysForProvider(_ context.Context, p schemas.ModelProvider) ([]schemas.Key, error) {
	return []schemas.Key{{ID: string(p) + "-key", Value: *schemas.NewEnvVar("local-fixture"), Weight: 1}}, nil
}
func (a budgetSimulationAccount) GetConfigForProvider(p schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	return &schemas.ProviderConfig{
		NetworkConfig:            schemas.NetworkConfig{BaseURL: a.baseURL + "/" + string(p), DefaultRequestTimeoutInSeconds: a.timeout, MaxRetries: 3},
		ConcurrencyAndBufferSize: schemas.ConcurrencyAndBufferSize{Concurrency: 1, BufferSize: 4},
		CustomProviderConfig:     &schemas.CustomProviderConfig{CustomProviderKey: string(p), BaseProviderType: schemas.OpenAI},
	}, nil
}

func TestAutoRouteGatewaySimulation(t *testing.T) {
	cases := []struct {
		name, model string
		failures    int
		cooldown    bool
		timeout     int
	}{
		{"first-success", "gemini-auto", 0, false, 0},
		{"second-success", "gemini-auto", 1, false, 0},
		{"third-success", "gemini-auto-bif", 2, false, 0},
		{"stop-after-three", "gemini-auto", 3, false, 0},
		{"alias-stop-after-three", "gemini-auto-bif", 3, false, 0},
		{"cooled-first-vip-third", "gemini-auto", 2, true, 0},
		{"other-model-still-four", "other-model", 3, false, 0},
		{"timeouts-no-inner-retry", "gemini-auto", 3, false, 1},
	}
	if os.Getenv("RUN_GENERATION_BUDGET_CLOCK") == "1" {
		cases = append(cases, struct {
			name, model string
			failures    int
			cooldown    bool
			timeout     int
		}{"three-real-60s-timeouts", "gemini-auto", 3, false, 60})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var calls []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				calls = append(calls, strings.Split(strings.Trim(r.URL.Path, "/"), "/")[0])
				attempt := len(calls)
				mu.Unlock()
				if attempt <= tc.failures {
					if tc.timeout > 0 {
						select {
						case <-time.After(time.Duration(tc.timeout+1) * time.Second):
						case <-r.Context().Done():
						}
					}
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = io.WriteString(w, `{"error":{"message":"local channel failure"}}`)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"local","object":"chat.completion","model":"upstream","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
			}))
			defer server.Close()
			timeout := tc.timeout
			if timeout == 0 {
				timeout = 60
			}
			client, err := bifrost.Init(context.Background(), schemas.BifrostConfig{Account: budgetSimulationAccount{server.URL, timeout}, Logger: bifrost.NewDefaultLogger(schemas.LogLevelError)})
			require.NoError(t, err)
			defer client.Shutdown()
			tracker := NewHealthTracker()
			rule := &tables.TableRoutingRule{ID: "local-budget"}
			for _, name := range []string{"a", "b", "c", "vip"} {
				rule.ParsedRouteGroups = append(rule.ParsedRouteGroups, tables.RouteGroup{Name: name, Targets: []tables.RouteGroupTarget{{Provider: bifrost.Ptr(name), Model: bifrost.Ptr("upstream"), KeyID: bifrost.Ptr(name + "-key"), Weight: 1}}})
			}
			policy := ApplyHealthPolicyDefaults(&tables.HealthPolicy{FailureThreshold: 1, CooldownSeconds: 120})
			rule.ParsedHealthPolicy = policy
			if tc.cooldown {
				tracker.RecordOutcome(rule.ID, TargetKey("a", "upstream", "a-key"), OutcomeHardFail, 0, "503", policy, time.Now())
			}
			parent, cancel := context.WithTimeout(context.Background(), 190*time.Second)
			defer cancel()
			ctx := schemas.NewBifrostContext(parent, schemas.NoDeadline)
			decision := buildGroupedRoutingDecision(ctx, rule, &RoutingContext{Provider: schemas.OpenAI, Model: tc.model}, tracker, NewMockLogger())
			require.NotNil(t, decision)
			// Same grouped-route context contract as the governance pre-hook.
			ctx.SetValue(schemas.BifrostContextKeyDisableProviderRetries, true)
			ctx.SetValue(schemas.BifrostContextKeyAPIKeyID, decision.KeyID)
			ctx.SetValue(schemas.BifrostContextKeyFallbackKeyIDs, decision.FallbackKeyIDs)
			req := &schemas.BifrostChatRequest{Provider: schemas.ModelProvider(decision.Provider), Model: decision.Model, Input: []schemas.ChatMessage{{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: bifrost.Ptr("local fixture")}}}}
			for _, fallback := range decision.Fallbacks {
				parts := strings.SplitN(fallback, "/", 2)
				req.Fallbacks = append(req.Fallbacks, schemas.Fallback{Provider: schemas.ModelProvider(parts[0]), Model: parts[1]})
			}
			started := time.Now()
			result, failure := client.ChatCompletionRequest(ctx, req)
			elapsed := time.Since(started)
			want := []string{"a", "b", "c", "vip"}
			if tc.cooldown {
				want = want[1:]
			}
			limit := 3
			if tc.model == "other-model" {
				limit = 4
			}
			count := min(tc.failures+1, limit)
			mu.Lock()
			got := append([]string(nil), calls...)
			mu.Unlock()
			require.Equal(t, want[:count], got)
			if tc.failures >= limit {
				require.NotNil(t, failure)
			} else {
				require.Nil(t, failure, fmt.Sprint(failure))
				require.NotNil(t, result)
			}
			if tc.timeout == 60 {
				require.GreaterOrEqual(t, elapsed, 179*time.Second)
				require.Less(t, elapsed, 190*time.Second)
			}
			t.Logf("channels=%v elapsed=%v success=%t", got, elapsed, failure == nil)
		})
	}
}
