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
	prefs         []configstoreTables.TableHealthDetectionTargetPreference
	prefReadCount int
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

func (m *mockAdaptiveRoutingConfigStore) GetHealthDetectionTargetPreferences(_ context.Context) ([]configstoreTables.TableHealthDetectionTargetPreference, error) {
	m.prefReadCount++
	result := make([]configstoreTables.TableHealthDetectionTargetPreference, len(m.prefs))
	copy(result, m.prefs)
	return result, nil
}

func (m *mockAdaptiveRoutingConfigStore) UpsertHealthDetectionTargetPreference(_ context.Context, pref *configstoreTables.TableHealthDetectionTargetPreference) error {
	prefCopy := *pref
	for idx := range m.prefs {
		if m.prefs[idx].TargetKey == pref.TargetKey {
			m.prefs[idx] = prefCopy
			return nil
		}
	}
	m.prefs = append(m.prefs, prefCopy)
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
				Enabled:        true,
				Interval:       21 * time.Second,
				IdlePause:      44 * time.Minute,
				Timeout:        6 * time.Second,
				MaxConcurrency: 5,
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
		Mode                             string `json:"mode"`
		IdlePauseMinutes                 int    `json:"idle_pause_minutes"`
		ActiveHealthProbeIntervalSeconds int    `json:"active_health_probe_interval_seconds"`
		ActiveHealthProbeTimeoutSeconds  int    `json:"active_health_probe_timeout_seconds"`
		ActiveHealthProbeMaxConcurrency  int    `json:"active_health_probe_max_concurrency"`
		Editable                         bool   `json:"editable"`
	}
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &resp))
	require.Equal(t, "hybrid", resp.Mode)
	require.Equal(t, 44, resp.IdlePauseMinutes)
	require.Equal(t, 21, resp.ActiveHealthProbeIntervalSeconds)
	require.Equal(t, 6, resp.ActiveHealthProbeTimeoutSeconds)
	require.Equal(t, 5, resp.ActiveHealthProbeMaxConcurrency)
	require.True(t, resp.Editable)
}

func TestGetHealthDetectionConfigReturnsReadOnlyWhenConfigStoreDisabled(t *testing.T) {
	SetLogger(&mockLogger{})

	handler, err := NewAdaptiveRoutingHandler(
		&mockAdaptiveRoutingRuntime{
			config: governance.ActiveHealthProbeConfig{
				Enabled:        false,
				Interval:       15 * time.Second,
				IdlePause:      30 * time.Minute,
				Timeout:        5 * time.Second,
				MaxConcurrency: 4,
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
				"is_vk_mandatory":                        true,
				"required_headers":                       []any{"x-team-id"},
				"active_health_probe_enabled":            true,
				"active_health_probe_timeout_seconds":    9,
				"active_health_probe_idle_pause_minutes": 25,
			},
		},
	}
	runtime := &mockAdaptiveRoutingRuntime{
		config: governance.ActiveHealthProbeConfig{
			Enabled:        true,
			Interval:       20 * time.Second,
			IdlePause:      40 * time.Minute,
			Timeout:        9 * time.Second,
			MaxConcurrency: 3,
		},
	}
	handler, err := NewAdaptiveRoutingHandler(runtime, store)
	require.NoError(t, err)

	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("PUT")
	ctx.Request.SetRequestURI("/api/governance/health-detection-config")
	ctx.Request.SetBodyString(`{
		"mode":"passive",
		"idle_pause_minutes":12,
		"active_health_probe_interval_seconds":18,
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
	require.Equal(t, 12, updatedConfig["active_health_probe_idle_pause_minutes"])
	require.Equal(t, 18, updatedConfig["active_health_probe_interval_seconds"])
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
			Enabled:        false,
			Interval:       15 * time.Second,
			IdlePause:      30 * time.Minute,
			Timeout:        5 * time.Second,
			MaxConcurrency: 4,
		},
	}
	handler, err := NewAdaptiveRoutingHandler(runtime, store)
	require.NoError(t, err)

	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("PUT")
	ctx.Request.SetRequestURI("/api/governance/health-detection-config")
	ctx.Request.SetBodyString(`{
		"mode":"hybrid",
		"idle_pause_minutes":18,
		"active_health_probe_interval_seconds":19,
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
	require.Equal(t, 18, createdConfig["active_health_probe_idle_pause_minutes"])
	require.Equal(t, 19, createdConfig["active_health_probe_interval_seconds"])
	require.Equal(t, 6, createdConfig["active_health_probe_timeout_seconds"])
	require.Equal(t, 8, createdConfig["active_health_probe_max_concurrency"])
	require.Equal(t, governance.PluginName, runtime.reloadName)
}

func TestUpdateHealthDetectionConfigReturnsConflictWhenReadOnly(t *testing.T) {
	SetLogger(&mockLogger{})

	handler, err := NewAdaptiveRoutingHandler(
		&mockAdaptiveRoutingRuntime{
			config: governance.ActiveHealthProbeConfig{
				Enabled:        false,
				Interval:       15 * time.Second,
				IdlePause:      30 * time.Minute,
				Timeout:        5 * time.Second,
				MaxConcurrency: 4,
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
		"idle_pause_minutes":18,
		"active_health_probe_interval_seconds":19,
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

func TestGetHealthDetectionTargets_DeduplicatesAcrossRules(t *testing.T) {
	SetLogger(&mockLogger{})

	provider := "openai"
	model := "gpt-4.1"
	keyID := "relay-a"
	targetKey := governance.TargetKey(provider, model, keyID)

	healthTracker := governance.NewHealthTracker()
	healthTracker.SetPendingFirstProbe(targetKey, true)

	handler, err := NewAdaptiveRoutingHandler(
		&mockAdaptiveRoutingRuntime{
			config: governance.ActiveHealthProbeConfig{
				Enabled:        true,
				Interval:       15 * time.Second,
				IdlePause:      30 * time.Minute,
				Timeout:        5 * time.Second,
				MaxConcurrency: 4,
			},
			store: &mockAdaptiveRoutingStore{rules: []*configstoreTables.TableRoutingRule{
				newAdaptiveRoutingRule("rule-a", "Rule A", provider, model, &keyID, 2, 30, 30),
				newAdaptiveRoutingRule("rule-b", "Rule B", provider, model, &keyID, 2, 30, 30),
			}},
			healthTracker: healthTracker,
		},
		&mockAdaptiveRoutingConfigStore{},
	)
	require.NoError(t, err)

	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("GET")
	ctx.Request.SetRequestURI("/api/governance/health-detection-targets")

	handler.getHealthDetectionTargets(ctx)

	require.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode(), string(ctx.Response.Body()))

	var resp struct {
		Targets []struct {
			Provider            string   `json:"provider"`
			Model               string   `json:"model"`
			KeyID               string   `json:"key_id"`
			ReferencedRuleIDs   []string `json:"referenced_rule_ids"`
			ReferencedRuleNames []string `json:"referenced_rule_names"`
		} `json:"targets"`
		Count int `json:"count"`
	}
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &resp))
	require.Equal(t, 1, resp.Count)
	require.Len(t, resp.Targets, 1)
	require.Equal(t, provider, resp.Targets[0].Provider)
	require.Equal(t, model, resp.Targets[0].Model)
	require.Equal(t, keyID, resp.Targets[0].KeyID)
	require.Len(t, resp.Targets[0].ReferencedRuleIDs, 2)
	require.Len(t, resp.Targets[0].ReferencedRuleNames, 2)
}

func TestGetHealthDetectionTargets_IncludesUnsupportedTargetWithoutKeyID(t *testing.T) {
	SetLogger(&mockLogger{})

	provider := "openai"
	model := "gpt-4.1"

	handler, err := NewAdaptiveRoutingHandler(
		&mockAdaptiveRoutingRuntime{
			config: governance.ActiveHealthProbeConfig{},
			store: &mockAdaptiveRoutingStore{rules: []*configstoreTables.TableRoutingRule{
				newAdaptiveRoutingRule("rule-a", "Rule A", provider, model, nil, 2, 30, 30),
			}},
			healthTracker: governance.NewHealthTracker(),
		},
		&mockAdaptiveRoutingConfigStore{},
	)
	require.NoError(t, err)

	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("GET")
	ctx.Request.SetRequestURI("/api/governance/health-detection-targets")

	handler.getHealthDetectionTargets(ctx)

	require.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode(), string(ctx.Response.Body()))

	var resp struct {
		Targets []struct {
			SupportStatus    string `json:"support_status"`
			SupportReason    string `json:"support_reason"`
			DetectionEnabled bool   `json:"detection_enabled"`
			ProbeState       string `json:"probe_state"`
		} `json:"targets"`
	}
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &resp))
	require.Len(t, resp.Targets, 1)
	require.Equal(t, "unsupported", resp.Targets[0].SupportStatus)
	require.NotEmpty(t, resp.Targets[0].SupportReason)
	require.False(t, resp.Targets[0].DetectionEnabled)
	require.Equal(t, "unsupported", resp.Targets[0].ProbeState)
}

func TestGetHealthDetectionTargets_DefaultsSupportedTargetsToOff(t *testing.T) {
	SetLogger(&mockLogger{})

	provider := "openai"
	model := "gpt-4.1"
	keyID := "relay-a"

	handler, err := NewAdaptiveRoutingHandler(
		&mockAdaptiveRoutingRuntime{
			config: governance.ActiveHealthProbeConfig{},
			store: &mockAdaptiveRoutingStore{rules: []*configstoreTables.TableRoutingRule{
				newAdaptiveRoutingRule("rule-a", "Rule A", provider, model, &keyID, 2, 30, 30),
			}},
			healthTracker: governance.NewHealthTracker(),
		},
		&mockAdaptiveRoutingConfigStore{},
	)
	require.NoError(t, err)

	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("GET")
	ctx.Request.SetRequestURI("/api/governance/health-detection-targets")

	handler.getHealthDetectionTargets(ctx)

	require.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode(), string(ctx.Response.Body()))

	var resp struct {
		Targets []struct {
			SupportStatus    string `json:"support_status"`
			DetectionEnabled bool   `json:"detection_enabled"`
			ProbeState       string `json:"probe_state"`
		} `json:"targets"`
	}
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &resp))
	require.Len(t, resp.Targets, 1)
	require.Equal(t, "supported", resp.Targets[0].SupportStatus)
	require.False(t, resp.Targets[0].DetectionEnabled)
	require.Equal(t, "off", resp.Targets[0].ProbeState)
}

func TestGetHealthDetectionTargets_ComputesCooldownRuleSummary(t *testing.T) {
	SetLogger(&mockLogger{})

	provider := "openai"
	model := "gpt-4.1"
	keyID := "relay-a"
	targetKey := governance.TargetKey(provider, model, keyID)
	now := time.Now()

	healthTracker := governance.NewHealthTracker()
	healthTracker.RecordFailureForRule("rule-a", targetKey, "timeout", now)

	handler, err := NewAdaptiveRoutingHandler(
		&mockAdaptiveRoutingRuntime{
			config: governance.ActiveHealthProbeConfig{},
			store: &mockAdaptiveRoutingStore{rules: []*configstoreTables.TableRoutingRule{
				newAdaptiveRoutingRule("rule-a", "Rule A", provider, model, &keyID, 1, 30, 30),
				newAdaptiveRoutingRule("rule-b", "Rule B", provider, model, &keyID, 2, 30, 30),
			}},
			healthTracker: healthTracker,
		},
		&mockAdaptiveRoutingConfigStore{},
	)
	require.NoError(t, err)

	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("GET")
	ctx.Request.SetRequestURI("/api/governance/health-detection-targets")

	handler.getHealthDetectionTargets(ctx)

	require.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode(), string(ctx.Response.Body()))

	var resp struct {
		Targets []struct {
			RuleHealthSummary struct {
				TotalRuleCount    int `json:"total_rule_count"`
				CooldownRuleCount int `json:"cooldown_rule_count"`
			} `json:"rule_health_summary"`
		} `json:"targets"`
	}
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &resp))
	require.Len(t, resp.Targets, 1)
	require.Equal(t, 2, resp.Targets[0].RuleHealthSummary.TotalRuleCount)
	require.Equal(t, 1, resp.Targets[0].RuleHealthSummary.CooldownRuleCount)
}

func TestUpdateHealthDetectionTarget_EnablesPendingFirstProbe(t *testing.T) {
	SetLogger(&mockLogger{})

	provider := "openai"
	model := "gpt-4.1"
	keyID := "relay-a"
	targetKey := governance.TargetKey(provider, model, keyID)
	targetID := encodeHealthDetectionTargetID(provider, model, &keyID)
	store := &mockAdaptiveRoutingConfigStore{}
	healthTracker := governance.NewHealthTracker()

	handler, err := NewAdaptiveRoutingHandler(
		&mockAdaptiveRoutingRuntime{
			config: governance.ActiveHealthProbeConfig{
				Enabled:        true,
				Interval:       15 * time.Second,
				IdlePause:      30 * time.Minute,
				Timeout:        5 * time.Second,
				MaxConcurrency: 4,
			},
			store: &mockAdaptiveRoutingStore{rules: []*configstoreTables.TableRoutingRule{
				newAdaptiveRoutingRule("rule-a", "Rule A", provider, model, &keyID, 2, 30, 30),
			}},
			healthTracker: healthTracker,
		},
		store,
	)
	require.NoError(t, err)

	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("PUT")
	ctx.Request.SetRequestURI("/api/governance/health-detection-targets/" + targetID)
	ctx.SetUserValue("target_id", targetID)
	ctx.Request.SetBodyString(`{"detection_enabled":true}`)

	handler.updateHealthDetectionTarget(ctx)

	require.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode(), string(ctx.Response.Body()))
	require.Len(t, store.prefs, 1)
	require.True(t, store.prefs[0].DetectionEnabled)
	require.Equal(t, 1, store.prefReadCount)
	require.True(t, healthTracker.GetTargetActivity(targetKey).PendingFirstProbe)

	var resp struct {
		DetectionEnabled bool   `json:"detection_enabled"`
		ProbeState       string `json:"probe_state"`
	}
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &resp))
	require.True(t, resp.DetectionEnabled)
	require.Equal(t, "pending_first_probe", resp.ProbeState)
}

func TestUpdateHealthDetectionTarget_RejectsUnsupportedTarget(t *testing.T) {
	SetLogger(&mockLogger{})

	provider := "openai"
	model := "gpt-4.1"
	targetID := encodeHealthDetectionTargetID(provider, model, nil)

	handler, err := NewAdaptiveRoutingHandler(
		&mockAdaptiveRoutingRuntime{
			config: governance.ActiveHealthProbeConfig{},
			store: &mockAdaptiveRoutingStore{rules: []*configstoreTables.TableRoutingRule{
				newAdaptiveRoutingRule("rule-a", "Rule A", provider, model, nil, 2, 30, 30),
			}},
			healthTracker: governance.NewHealthTracker(),
		},
		&mockAdaptiveRoutingConfigStore{},
	)
	require.NoError(t, err)

	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("PUT")
	ctx.Request.SetRequestURI("/api/governance/health-detection-targets/" + targetID)
	ctx.SetUserValue("target_id", targetID)
	ctx.Request.SetBodyString(`{"detection_enabled":true}`)

	handler.updateHealthDetectionTarget(ctx)

	require.Equal(t, fasthttp.StatusConflict, ctx.Response.StatusCode(), string(ctx.Response.Body()))
}

func TestResolveHealthDetectionProbeState_PausesWithoutRealTrafficAfterInitialProbe(t *testing.T) {
	now := time.Now()

	state := resolveHealthDetectionProbeState(
		healthDetectionSupportSupported,
		true,
		governance.TargetActivitySnapshot{
			LastProbeAt:          now.Add(-time.Minute),
			LastProbeRequestType: schemas.ChatCompletionRequest,
			LastProbeResult:      "success",
		},
		2*time.Minute,
		now,
	)

	require.Equal(t, healthDetectionProbeStatePausedIdle, state)
}

func newAdaptiveRoutingRule(ruleID, ruleName, provider, model string, keyID *string, failureThreshold, failureWindowSeconds, cooldownSeconds int) *configstoreTables.TableRoutingRule {
	target := configstoreTables.RouteGroupTarget{
		Provider: &provider,
		Model:    &model,
		Weight:   1,
	}
	if keyID != nil {
		target.KeyID = keyID
	}

	return &configstoreTables.TableRoutingRule{
		ID:                    ruleID,
		Name:                  ruleName,
		Enabled:               true,
		GroupedRoutingEnabled: true,
		ParsedHealthPolicy: &configstoreTables.HealthPolicy{
			FailureThreshold:     failureThreshold,
			FailureWindowSeconds: failureWindowSeconds,
			CooldownSeconds:      cooldownSeconds,
		},
		ParsedRouteGroups: []configstoreTables.RouteGroup{
			{
				Name:       ruleName + "-group",
				RetryLimit: 0,
				Targets:    []configstoreTables.RouteGroupTarget{target},
			},
		},
	}
}
