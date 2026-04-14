package handlers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bytedance/sonic"
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/plugins/governance"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

const (
	healthDetectionModePassive = "passive"
	healthDetectionModeHybrid  = "hybrid"
)

const healthDetectionReadOnlyReason = "Health detection is currently managed by config.json. Enable a config store to edit settings here."

type AdaptiveRoutingManager interface {
	ReloadPlugin(ctx context.Context, name string, path *string, pluginConfig any, placement *schemas.PluginPlacement, order *int) error
	GetGovernancePlugin() governance.BaseGovernancePlugin
}

type AdaptiveRoutingHandler struct {
	manager     AdaptiveRoutingManager
	configStore configstore.ConfigStore
}

type HealthDetectionConfigResponse struct {
	Mode                                     string `json:"mode"`
	ActiveHealthProbeIntervalSeconds         int    `json:"active_health_probe_interval_seconds"`
	ActiveHealthProbePassiveFreshnessSeconds int    `json:"active_health_probe_passive_freshness_seconds"`
	ActiveHealthProbeTimeoutSeconds          int    `json:"active_health_probe_timeout_seconds"`
	ActiveHealthProbeMaxConcurrency          int    `json:"active_health_probe_max_concurrency"`
	Editable                                 bool   `json:"editable"`
	ReadOnlyReason                           string `json:"read_only_reason,omitempty"`
}

type UpdateHealthDetectionConfigRequest struct {
	Mode                                     string `json:"mode"`
	ActiveHealthProbeIntervalSeconds         int    `json:"active_health_probe_interval_seconds"`
	ActiveHealthProbePassiveFreshnessSeconds int    `json:"active_health_probe_passive_freshness_seconds"`
	ActiveHealthProbeTimeoutSeconds          int    `json:"active_health_probe_timeout_seconds"`
	ActiveHealthProbeMaxConcurrency          int    `json:"active_health_probe_max_concurrency"`
}

func NewAdaptiveRoutingHandler(manager AdaptiveRoutingManager, configStore configstore.ConfigStore) (*AdaptiveRoutingHandler, error) {
	if manager == nil {
		return nil, fmt.Errorf("adaptive routing manager is required")
	}
	if manager.GetGovernancePlugin() == nil {
		return nil, fmt.Errorf("governance plugin is required")
	}
	return &AdaptiveRoutingHandler{
		manager:     manager,
		configStore: configStore,
	}, nil
}

func (h *AdaptiveRoutingHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	r.GET("/api/governance/health-status", lib.ChainMiddlewares(h.getHealthStatus, middlewares...))
	r.GET("/api/governance/health-detection-config", lib.ChainMiddlewares(h.getHealthDetectionConfig, middlewares...))
	r.PUT("/api/governance/health-detection-config", lib.ChainMiddlewares(h.updateHealthDetectionConfig, middlewares...))
}

type RuleHealthStatusResponse struct {
	RuleID   string                            `json:"rule_id"`
	RuleName string                            `json:"rule_name"`
	Policy   *configstoreTables.HealthPolicy   `json:"policy"`
	Targets  []governance.TargetHealthSnapshot `json:"targets"`
}

type HealthStatusResponse struct {
	Rules []RuleHealthStatusResponse `json:"rules"`
	Count int                        `json:"count"`
}

func (h *AdaptiveRoutingHandler) getHealthStatus(ctx *fasthttp.RequestCtx) {
	governancePlugin := h.manager.GetGovernancePlugin()
	if governancePlugin == nil {
		SendError(ctx, fasthttp.StatusServiceUnavailable, "Governance plugin is not available")
		return
	}

	healthTracker := governancePlugin.GetHealthTracker()
	if healthTracker == nil {
		SendJSON(ctx, HealthStatusResponse{
			Rules: []RuleHealthStatusResponse{},
			Count: 0,
		})
		return
	}

	rules := governancePlugin.GetGovernanceStore().GetAllRoutingRules()
	now := time.Now()
	result := make([]RuleHealthStatusResponse, 0, len(rules))

	for _, rule := range rules {
		if rule == nil || !rule.GroupedRoutingEnabled || len(rule.ParsedRouteGroups) == 0 {
			continue
		}

		policy := rule.ParsedHealthPolicy
		if policy == nil {
			policy = &configstoreTables.HealthPolicy{
				FailureThreshold:     2,
				FailureWindowSeconds: 30,
				CooldownSeconds:      30,
			}
		}

		targets := make([]governance.TargetHealthSnapshot, 0)
		seen := make(map[string]struct{})
		for _, group := range rule.ParsedRouteGroups {
			for _, target := range group.Targets {
				key := governance.RouteGroupTargetKey(target)
				if _, exists := seen[key]; exists {
					continue
				}
				seen[key] = struct{}{}
				targets = append(targets, healthTracker.GetTargetStatusForRule(rule.ID, key, policy, now))
			}
		}

		result = append(result, RuleHealthStatusResponse{
			RuleID:   rule.ID,
			RuleName: rule.Name,
			Policy:   policy,
			Targets:  targets,
		})
	}

	SendJSON(ctx, HealthStatusResponse{
		Rules: result,
		Count: len(result),
	})
}

func (h *AdaptiveRoutingHandler) getHealthDetectionConfig(ctx *fasthttp.RequestCtx) {
	governancePlugin := h.manager.GetGovernancePlugin()
	if governancePlugin == nil {
		SendError(ctx, fasthttp.StatusServiceUnavailable, "Governance plugin is not available")
		return
	}

	resp := responseFromProbeConfig(governancePlugin.GetActiveHealthProbeConfig(), h.configStore != nil)
	if h.configStore == nil {
		resp.ReadOnlyReason = healthDetectionReadOnlyReason
	}
	SendJSON(ctx, resp)
}

func (h *AdaptiveRoutingHandler) updateHealthDetectionConfig(ctx *fasthttp.RequestCtx) {
	if h.configStore == nil {
		SendError(ctx, fasthttp.StatusConflict, healthDetectionReadOnlyReason)
		return
	}

	var request UpdateHealthDetectionConfigRequest
	if err := sonic.Unmarshal(ctx.PostBody(), &request); err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, "Invalid request body")
		return
	}
	if err := validateHealthDetectionUpdateRequest(request); err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}

	mergedConfig := map[string]any{}
	var existingPlugin *configstoreTables.TablePlugin
	var err error
	existingPlugin, err = h.configStore.GetPlugin(ctx, governance.PluginName)
	if err != nil && !errors.Is(err, configstore.ErrNotFound) {
		logger.Error("failed to get governance plugin config: %v", err)
		SendError(ctx, fasthttp.StatusInternalServerError, "Failed to load governance plugin config")
		return
	}
	if existingPlugin != nil {
		mergedConfig, err = cloneConfigMap(existingPlugin.Config)
		if err != nil {
			logger.Error("failed to clone governance plugin config: %v", err)
			SendError(ctx, fasthttp.StatusInternalServerError, "Failed to load governance plugin config")
			return
		}
	}

	applyHealthDetectionConfigUpdate(mergedConfig, request)

	pluginRecord := &configstoreTables.TablePlugin{
		Name:    governance.PluginName,
		Enabled: true,
		Config:  mergedConfig,
	}
	if existingPlugin != nil {
		pluginRecord.Path = existingPlugin.Path
		pluginRecord.Placement = existingPlugin.Placement
		pluginRecord.Order = existingPlugin.Order
	}

	if errors.Is(err, configstore.ErrNotFound) {
		if err := h.configStore.CreatePlugin(ctx, pluginRecord); err != nil {
			logger.Error("failed to create governance plugin config: %v", err)
			SendError(ctx, fasthttp.StatusInternalServerError, "Failed to save health detection config")
			return
		}
	} else {
		if err := h.configStore.UpdatePlugin(ctx, pluginRecord); err != nil {
			logger.Error("failed to update governance plugin config: %v", err)
			SendError(ctx, fasthttp.StatusInternalServerError, "Failed to save health detection config")
			return
		}
	}

	if err := h.manager.ReloadPlugin(ctx, governance.PluginName, pluginRecord.Path, pluginRecord.Config, pluginRecord.Placement, pluginRecord.Order); err != nil {
		logger.Error("failed to reload governance plugin: %v", err)
		SendError(ctx, fasthttp.StatusInternalServerError, fmt.Sprintf("Health detection settings saved but reload failed: %v", err))
		return
	}

	SendJSON(ctx, responseFromProbeConfig(probeConfigFromRequest(request), true))
}

func validateHealthDetectionUpdateRequest(request UpdateHealthDetectionConfigRequest) error {
	if request.Mode != healthDetectionModePassive && request.Mode != healthDetectionModeHybrid {
		return fmt.Errorf("mode must be either %q or %q", healthDetectionModePassive, healthDetectionModeHybrid)
	}
	if request.ActiveHealthProbeIntervalSeconds < 1 {
		return fmt.Errorf("active_health_probe_interval_seconds must be at least 1")
	}
	if request.ActiveHealthProbePassiveFreshnessSeconds < 1 {
		return fmt.Errorf("active_health_probe_passive_freshness_seconds must be at least 1")
	}
	if request.ActiveHealthProbeTimeoutSeconds < 1 {
		return fmt.Errorf("active_health_probe_timeout_seconds must be at least 1")
	}
	if request.ActiveHealthProbeMaxConcurrency < 1 {
		return fmt.Errorf("active_health_probe_max_concurrency must be at least 1")
	}
	return nil
}

func cloneConfigMap(source any) (map[string]any, error) {
	if source == nil {
		return map[string]any{}, nil
	}
	if configMap, ok := source.(map[string]any); ok {
		cloned := make(map[string]any, len(configMap))
		for key, value := range configMap {
			cloned[key] = value
		}
		return cloned, nil
	}
	raw, err := sonic.Marshal(source)
	if err != nil {
		return nil, err
	}
	var configMap map[string]any
	if err := sonic.Unmarshal(raw, &configMap); err != nil {
		return nil, err
	}
	if configMap == nil {
		configMap = map[string]any{}
	}
	return configMap, nil
}

func applyHealthDetectionConfigUpdate(configMap map[string]any, request UpdateHealthDetectionConfigRequest) {
	configMap["active_health_probe_enabled"] = request.Mode == healthDetectionModeHybrid
	configMap["active_health_probe_interval_seconds"] = request.ActiveHealthProbeIntervalSeconds
	configMap["active_health_probe_passive_freshness_seconds"] = request.ActiveHealthProbePassiveFreshnessSeconds
	configMap["active_health_probe_timeout_seconds"] = request.ActiveHealthProbeTimeoutSeconds
	configMap["active_health_probe_max_concurrency"] = request.ActiveHealthProbeMaxConcurrency
}

func probeConfigFromRequest(request UpdateHealthDetectionConfigRequest) governance.ActiveHealthProbeConfig {
	return governance.ActiveHealthProbeConfig{
		Enabled:          request.Mode == healthDetectionModeHybrid,
		Interval:         time.Duration(request.ActiveHealthProbeIntervalSeconds) * time.Second,
		PassiveFreshness: time.Duration(request.ActiveHealthProbePassiveFreshnessSeconds) * time.Second,
		Timeout:          time.Duration(request.ActiveHealthProbeTimeoutSeconds) * time.Second,
		MaxConcurrency:   request.ActiveHealthProbeMaxConcurrency,
	}
}

func responseFromProbeConfig(cfg governance.ActiveHealthProbeConfig, editable bool) HealthDetectionConfigResponse {
	mode := healthDetectionModePassive
	if cfg.Enabled {
		mode = healthDetectionModeHybrid
	}
	return HealthDetectionConfigResponse{
		Mode:                                     mode,
		ActiveHealthProbeIntervalSeconds:         int(cfg.Interval / time.Second),
		ActiveHealthProbePassiveFreshnessSeconds: int(cfg.PassiveFreshness / time.Second),
		ActiveHealthProbeTimeoutSeconds:          int(cfg.Timeout / time.Second),
		ActiveHealthProbeMaxConcurrency:          cfg.MaxConcurrency,
		Editable:                                 editable,
	}
}
