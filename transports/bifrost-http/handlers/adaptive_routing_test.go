package handlers

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/plugins/governance"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
	"gorm.io/gorm"
)

type mockAdaptiveRoutingConfigStore struct {
	configstore.ConfigStore
	plugin        *configstoreTables.TablePlugin
	createdPlugin *configstoreTables.TablePlugin
	updatedPlugin *configstoreTables.TablePlugin
}

func (m *mockAdaptiveRoutingConfigStore) GetPlugin(_ context.Context, name string) (*configstoreTables.TablePlugin, error) {
	if m.plugin == nil || m.plugin.Name != name {
		return nil, configstore.ErrNotFound
	}
	pluginCopy := *m.plugin
	if configMap, ok := m.plugin.Config.(map[string]any); ok {
		cloned := make(map[string]any, len(configMap))
		for key, value := range configMap {
			cloned[key] = value
		}
		pluginCopy.Config = cloned
	}
	return &pluginCopy, nil
}

func (m *mockAdaptiveRoutingConfigStore) CreatePlugin(_ context.Context, plugin *configstoreTables.TablePlugin, _ ...*gorm.DB) error {
	pluginCopy := *plugin
	if configMap, ok := plugin.Config.(map[string]any); ok {
		cloned := make(map[string]any, len(configMap))
		for key, value := range configMap {
			cloned[key] = value
		}
		pluginCopy.Config = cloned
	}
	m.createdPlugin = &pluginCopy
	m.plugin = &pluginCopy
	return nil
}

func (m *mockAdaptiveRoutingConfigStore) UpdatePlugin(_ context.Context, plugin *configstoreTables.TablePlugin, _ ...*gorm.DB) error {
	pluginCopy := *plugin
	if configMap, ok := plugin.Config.(map[string]any); ok {
		cloned := make(map[string]any, len(configMap))
		for key, value := range configMap {
			cloned[key] = value
		}
		pluginCopy.Config = cloned
	}
	m.updatedPlugin = &pluginCopy
	m.plugin = &pluginCopy
	return nil
}

type mockAdaptiveRoutingRuntime struct {
	governance.BaseGovernancePlugin
	config          governance.ActiveHealthProbeConfig
	store           governance.GovernanceStore
	healthTracker   *governance.HealthTracker
	reloadName      string
	reloadPath      *string
	reloadConfig    any
	reloadPlacement *schemas.PluginPlacement
	reloadOrder     *int
}

func (m *mockAdaptiveRoutingRuntime) GetGovernancePlugin() governance.BaseGovernancePlugin {
	return m
}

func (m *mockAdaptiveRoutingRuntime) GetActiveHealthProbeConfig() governance.ActiveHealthProbeConfig {
	return m.config
}

func (m *mockAdaptiveRoutingRuntime) GetGovernanceStore() governance.GovernanceStore {
	return m.store
}

func (m *mockAdaptiveRoutingRuntime) GetHealthTracker() *governance.HealthTracker {
	return m.healthTracker
}

func (m *mockAdaptiveRoutingRuntime) ReloadPlugin(_ context.Context, name string, path *string, pluginConfig any, placement *schemas.PluginPlacement, order *int) error {
	m.reloadName = name
	m.reloadPath = path
	m.reloadConfig = pluginConfig
	m.reloadPlacement = placement
	m.reloadOrder = order
	return nil
}

type mockAdaptiveRoutingStore struct {
	governance.GovernanceStore
	rules []*configstoreTables.TableRoutingRule
}

func (m *mockAdaptiveRoutingStore) GetAllRoutingRules() []*configstoreTables.TableRoutingRule {
	return m.rules
}

func TestGetHealthDetectionConfigReturnsRunningValues(t *testing.T) {
	SetLogger(&mockLogger{})

	handler, err := NewAdaptiveRoutingHandler(
		&mockAdaptiveRoutingRuntime{
			config: governance.ActiveHealthProbeConfig{
				Enabled:          true,
				Interval:         21 * time.Second,
				PassiveFreshness: 44 * time.Second,
				Timeout:          6 * time.Second,
				MaxConcurrency:   5,
			},
		},
		&mockAdaptiveRoutingConfigStore{},
	)
	require.NoError(t, err)

	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("GET")
	ctx.Request.SetRequestURI("/api/governance/health-detection-config")

	handler.getHealthDetectionConfig(ctx)

	require.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode(), string(ctx.Response.Body()))

	var resp struct {
		Mode                                     string `json:"mode"`
		ActiveHealthProbeIntervalSeconds         int    `json:"active_health_probe_interval_seconds"`
		ActiveHealthProbePassiveFreshnessSeconds int    `json:"active_health_probe_passive_freshness_seconds"`
		ActiveHealthProbeTimeoutSeconds          int    `json:"active_health_probe_timeout_seconds"`
		ActiveHealthProbeMaxConcurrency          int    `json:"active_health_probe_max_concurrency"`
		Editable                                 bool   `json:"editable"`
	}
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &resp))
	require.Equal(t, "hybrid", resp.Mode)
	require.Equal(t, 21, resp.ActiveHealthProbeIntervalSeconds)
	require.Equal(t, 44, resp.ActiveHealthProbePassiveFreshnessSeconds)
	require.Equal(t, 6, resp.ActiveHealthProbeTimeoutSeconds)
	require.Equal(t, 5, resp.ActiveHealthProbeMaxConcurrency)
	require.True(t, resp.Editable)
}

func TestGetHealthDetectionConfigReturnsReadOnlyWhenConfigStoreDisabled(t *testing.T) {
	SetLogger(&mockLogger{})

	handler, err := NewAdaptiveRoutingHandler(
		&mockAdaptiveRoutingRuntime{
			config: governance.ActiveHealthProbeConfig{
				Enabled:          false,
				Interval:         15 * time.Second,
				PassiveFreshness: 30 * time.Second,
				Timeout:          5 * time.Second,
				MaxConcurrency:   4,
			},
		},
		nil,
	)
	require.NoError(t, err)

	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("GET")
	ctx.Request.SetRequestURI("/api/governance/health-detection-config")

	handler.getHealthDetectionConfig(ctx)

	require.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode(), string(ctx.Response.Body()))

	var resp struct {
		Mode           string `json:"mode"`
		Editable       bool   `json:"editable"`
		ReadOnlyReason string `json:"read_only_reason"`
	}
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &resp))
	require.Equal(t, "passive", resp.Mode)
	require.False(t, resp.Editable)
	require.NotEmpty(t, resp.ReadOnlyReason)
}

func TestUpdateHealthDetectionConfigMergesExistingPluginConfig(t *testing.T) {
	SetLogger(&mockLogger{})

	store := &mockAdaptiveRoutingConfigStore{
		plugin: &configstoreTables.TablePlugin{
			Name:    governance.PluginName,
			Enabled: false,
			Config: map[string]any{
				"is_vk_mandatory":                     true,
				"required_headers":                    []any{"x-team-id"},
				"active_health_probe_enabled":         true,
				"active_health_probe_timeout_seconds": 9,
			},
		},
	}
	runtime := &mockAdaptiveRoutingRuntime{
		config: governance.ActiveHealthProbeConfig{
			Enabled:          true,
			Interval:         20 * time.Second,
			PassiveFreshness: 40 * time.Second,
			Timeout:          9 * time.Second,
			MaxConcurrency:   3,
		},
	}
	handler, err := NewAdaptiveRoutingHandler(runtime, store)
	require.NoError(t, err)

	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("PUT")
	ctx.Request.SetRequestURI("/api/governance/health-detection-config")
	ctx.Request.SetBodyString(`{
		"mode":"passive",
		"active_health_probe_interval_seconds":18,
		"active_health_probe_passive_freshness_seconds":33,
		"active_health_probe_timeout_seconds":4,
		"active_health_probe_max_concurrency":7
	}`)

	handler.updateHealthDetectionConfig(ctx)

	require.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode(), string(ctx.Response.Body()))
	require.NotNil(t, store.updatedPlugin)
	require.True(t, store.updatedPlugin.Enabled)

	updatedConfig, ok := store.updatedPlugin.Config.(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, updatedConfig["is_vk_mandatory"])
	require.Equal(t, false, updatedConfig["active_health_probe_enabled"])
	require.Equal(t, 18, updatedConfig["active_health_probe_interval_seconds"])
	require.Equal(t, 33, updatedConfig["active_health_probe_passive_freshness_seconds"])
	require.Equal(t, 4, updatedConfig["active_health_probe_timeout_seconds"])
	require.Equal(t, 7, updatedConfig["active_health_probe_max_concurrency"])
	require.Equal(t, governance.PluginName, runtime.reloadName)

	var resp struct {
		Mode     string `json:"mode"`
		Editable bool   `json:"editable"`
	}
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &resp))
	require.Equal(t, "passive", resp.Mode)
	require.True(t, resp.Editable)
}

func TestUpdateHealthDetectionConfigCreatesPluginWhenMissing(t *testing.T) {
	SetLogger(&mockLogger{})

	store := &mockAdaptiveRoutingConfigStore{}
	runtime := &mockAdaptiveRoutingRuntime{
		config: governance.ActiveHealthProbeConfig{
			Enabled:          false,
			Interval:         15 * time.Second,
			PassiveFreshness: 30 * time.Second,
			Timeout:          5 * time.Second,
			MaxConcurrency:   4,
		},
	}
	handler, err := NewAdaptiveRoutingHandler(runtime, store)
	require.NoError(t, err)

	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("PUT")
	ctx.Request.SetRequestURI("/api/governance/health-detection-config")
	ctx.Request.SetBodyString(`{
		"mode":"hybrid",
		"active_health_probe_interval_seconds":19,
		"active_health_probe_passive_freshness_seconds":38,
		"active_health_probe_timeout_seconds":6,
		"active_health_probe_max_concurrency":8
	}`)

	handler.updateHealthDetectionConfig(ctx)

	require.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode(), string(ctx.Response.Body()))
	require.NotNil(t, store.createdPlugin)
	require.True(t, store.createdPlugin.Enabled)
	createdConfig, ok := store.createdPlugin.Config.(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, createdConfig["active_health_probe_enabled"])
	require.Equal(t, 19, createdConfig["active_health_probe_interval_seconds"])
	require.Equal(t, 38, createdConfig["active_health_probe_passive_freshness_seconds"])
	require.Equal(t, 6, createdConfig["active_health_probe_timeout_seconds"])
	require.Equal(t, 8, createdConfig["active_health_probe_max_concurrency"])
	require.Equal(t, governance.PluginName, runtime.reloadName)
}

func TestUpdateHealthDetectionConfigReturnsConflictWhenReadOnly(t *testing.T) {
	SetLogger(&mockLogger{})

	handler, err := NewAdaptiveRoutingHandler(
		&mockAdaptiveRoutingRuntime{
			config: governance.ActiveHealthProbeConfig{
				Enabled:          false,
				Interval:         15 * time.Second,
				PassiveFreshness: 30 * time.Second,
				Timeout:          5 * time.Second,
				MaxConcurrency:   4,
			},
		},
		nil,
	)
	require.NoError(t, err)

	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("PUT")
	ctx.Request.SetRequestURI("/api/governance/health-detection-config")
	ctx.Request.SetBodyString(`{
		"mode":"hybrid",
		"active_health_probe_interval_seconds":19,
		"active_health_probe_passive_freshness_seconds":38,
		"active_health_probe_timeout_seconds":6,
		"active_health_probe_max_concurrency":8
	}`)

	handler.updateHealthDetectionConfig(ctx)

	require.Equal(t, fasthttp.StatusConflict, ctx.Response.StatusCode(), string(ctx.Response.Body()))
}

func TestGetHealthStatusUsesInMemoryRoutingRulesWithoutConfigStore(t *testing.T) {
	SetLogger(&mockLogger{})

	provider := "openai"
	model := "gpt-4o"
	keyID := "key-a"
	targetKey := governance.TargetKey(provider, model, keyID)
	now := time.Now().UTC()

	healthTracker := governance.NewHealthTracker()
	healthTracker.RecordObservation(targetKey, schemas.ChatCompletionRequest, governance.HealthObservationSourceActive, now)

	rule := &configstoreTables.TableRoutingRule{
		ID:                    "rule-1",
		Name:                  "Primary Rule",
		GroupedRoutingEnabled: true,
		ParsedHealthPolicy: &configstoreTables.HealthPolicy{
			FailureThreshold:     3,
			FailureWindowSeconds: 45,
			CooldownSeconds:      60,
		},
		ParsedRouteGroups: []configstoreTables.RouteGroup{
			{
				Name: "group-a",
				Targets: []configstoreTables.RouteGroupTarget{
					{
						Provider: &provider,
						Model:    &model,
						KeyID:    &keyID,
						Weight:   1,
					},
				},
			},
		},
	}

	handler, err := NewAdaptiveRoutingHandler(
		&mockAdaptiveRoutingRuntime{
			config:        governance.ActiveHealthProbeConfig{},
			store:         &mockAdaptiveRoutingStore{rules: []*configstoreTables.TableRoutingRule{rule}},
			healthTracker: healthTracker,
		},
		nil,
	)
	require.NoError(t, err)

	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("GET")
	ctx.Request.SetRequestURI("/api/governance/health-status")

	handler.getHealthStatus(ctx)

	require.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode(), string(ctx.Response.Body()))

	var resp HealthStatusResponse
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &resp))
	require.Len(t, resp.Rules, 1)
	require.Equal(t, 1, resp.Count)
	require.Equal(t, "rule-1", resp.Rules[0].RuleID)
	require.Equal(t, "Primary Rule", resp.Rules[0].RuleName)
	require.Len(t, resp.Rules[0].Targets, 1)
	require.Equal(t, targetKey, resp.Rules[0].Targets[0].Key)
	require.Equal(t, "active", resp.Rules[0].Targets[0].LastObservationSource)
}
