package handlers

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"
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

	healthDetectionSupportSupported   = "supported"
	healthDetectionSupportUnsupported = "unsupported"

	healthDetectionProbeStateUnsupported       = "unsupported"
	healthDetectionProbeStateOff               = "off"
	healthDetectionProbeStatePendingFirstProbe = "pending_first_probe"
	healthDetectionProbeStateEligible          = "eligible"
	healthDetectionProbeStatePausedIdle        = "paused_idle"

	healthDetectionRuntimeScopeNodeLocal = "node_local"
)

const healthDetectionReadOnlyReason = "Health detection is currently managed by config.json. Enable a config store to edit settings here."

type AdaptiveRoutingManager interface {
	ReloadPlugin(ctx context.Context, name string, path *string, pluginConfig any, placement *schemas.PluginPlacement, order *int) error
	GetGovernancePlugin() governance.BaseGovernancePlugin
}

type adaptiveRoutingTargetPreferenceStore interface {
	GetHealthDetectionTargetPreferences(ctx context.Context) ([]configstoreTables.TableHealthDetectionTargetPreference, error)
	UpsertHealthDetectionTargetPreference(ctx context.Context, pref *configstoreTables.TableHealthDetectionTargetPreference) error
}

type AdaptiveRoutingHandler struct {
	manager     AdaptiveRoutingManager
	configStore configstore.ConfigStore
}

type HealthDetectionConfigResponse struct {
	Mode                             string `json:"mode"`
	IdlePauseMinutes                 int    `json:"idle_pause_minutes"`
	ActiveHealthProbeIntervalSeconds int    `json:"active_health_probe_interval_seconds"`
	ActiveHealthProbeTimeoutSeconds  int    `json:"active_health_probe_timeout_seconds"`
	ActiveHealthProbeMaxConcurrency  int    `json:"active_health_probe_max_concurrency"`
	Editable                         bool   `json:"editable"`
	ReadOnlyReason                   string `json:"read_only_reason,omitempty"`
}

type UpdateHealthDetectionConfigRequest struct {
	Mode                             string `json:"mode"`
	IdlePauseMinutes                 int    `json:"idle_pause_minutes"`
	ActiveHealthProbeIntervalSeconds int    `json:"active_health_probe_interval_seconds"`
	ActiveHealthProbeTimeoutSeconds  int    `json:"active_health_probe_timeout_seconds"`
	ActiveHealthProbeMaxConcurrency  int    `json:"active_health_probe_max_concurrency"`
}

type UpdateHealthDetectionTargetRequest struct {
	DetectionEnabled *bool `json:"detection_enabled"`
}

type HealthDetectionRuleHealthSummary struct {
	TotalRuleCount    int `json:"total_rule_count"`
	CooldownRuleCount int `json:"cooldown_rule_count"`
}

type HealthDetectionTargetResponse struct {
	TargetID            string                           `json:"target_id"`
	Provider            string                           `json:"provider"`
	Model               string                           `json:"model"`
	KeyID               *string                          `json:"key_id,omitempty"`
	ReferencedRuleIDs   []string                         `json:"referenced_rule_ids"`
	ReferencedRuleNames []string                         `json:"referenced_rule_names"`
	SupportStatus       string                           `json:"support_status"`
	SupportReason       string                           `json:"support_reason,omitempty"`
	DetectionEnabled    bool                             `json:"detection_enabled"`
	ProbeState          string                           `json:"probe_state"`
	RuleHealthSummary   HealthDetectionRuleHealthSummary `json:"rule_health_summary"`
	LastRealAccessAt    *string                          `json:"last_real_access_at,omitempty"`
	LastProbeAt         *string                          `json:"last_probe_at,omitempty"`
	LastProbeResult     string                           `json:"last_probe_result,omitempty"`
	LastProbeError      string                           `json:"last_probe_error,omitempty"`
	RuntimeScope        string                           `json:"runtime_scope"`
}

type HealthDetectionTargetsResponse struct {
	Targets []HealthDetectionTargetResponse `json:"targets"`
	Count   int                             `json:"count"`
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

type healthDetectionTargetRecord struct {
	CanonicalKey        string
	TargetID            string
	RuntimeTargetKey    string
	Provider            string
	Model               string
	KeyID               *string
	ReferencedRuleIDs   []string
	ReferencedRuleNames []string
	CooldownRuleCount   int
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
	r.GET("/api/governance/health-detection-targets", lib.ChainMiddlewares(h.getHealthDetectionTargets, middlewares...))
	r.PUT("/api/governance/health-detection-targets/{target_id}", lib.ChainMiddlewares(h.updateHealthDetectionTarget, middlewares...))
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

		policy := healthPolicyOrDefault(rule)
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

func (h *AdaptiveRoutingHandler) getHealthDetectionTargets(ctx *fasthttp.RequestCtx) {
	targets, err := h.buildHealthDetectionTargets(ctx, time.Now())
	if err != nil {
		logger.Error("failed to build health detection targets: %v", err)
		SendError(ctx, fasthttp.StatusInternalServerError, "Failed to load health detection targets")
		return
	}

	SendJSON(ctx, HealthDetectionTargetsResponse{
		Targets: targets,
		Count:   len(targets),
	})
}

func (h *AdaptiveRoutingHandler) updateHealthDetectionTarget(ctx *fasthttp.RequestCtx) {
	prefStore := h.targetPreferenceStore()
	if prefStore == nil {
		SendError(ctx, fasthttp.StatusConflict, healthDetectionReadOnlyReason)
		return
	}
	now := time.Now()

	targetID, _ := ctx.UserValue("target_id").(string)
	if targetID == "" {
		SendError(ctx, fasthttp.StatusBadRequest, "Missing target_id")
		return
	}

	var request UpdateHealthDetectionTargetRequest
	if err := sonic.Unmarshal(ctx.PostBody(), &request); err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, "Invalid request body")
		return
	}
	if request.DetectionEnabled == nil {
		SendError(ctx, fasthttp.StatusBadRequest, "detection_enabled is required")
		return
	}

	targets, err := h.buildHealthDetectionTargets(ctx, now)
	if err != nil {
		logger.Error("failed to load health detection targets before update: %v", err)
		SendError(ctx, fasthttp.StatusInternalServerError, "Failed to load health detection targets")
		return
	}

	var selected *HealthDetectionTargetResponse
	for idx := range targets {
		if targets[idx].TargetID == targetID {
			selected = &targets[idx]
			break
		}
	}
	if selected == nil {
		SendError(ctx, fasthttp.StatusNotFound, "Health detection target not found")
		return
	}
	if selected.SupportStatus != healthDetectionSupportSupported {
		reason := selected.SupportReason
		if reason == "" {
			reason = "This target is visible but cannot be enrolled in active probing."
		}
		SendError(ctx, fasthttp.StatusConflict, reason)
		return
	}

	provider, model, keyID, err := decodeHealthDetectionTargetID(targetID)
	if err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, "Invalid target_id")
		return
	}

	if err := prefStore.UpsertHealthDetectionTargetPreference(ctx, &configstoreTables.TableHealthDetectionTargetPreference{
		TargetKey:        canonicalHealthDetectionTargetKey(provider, model, keyID),
		Provider:         provider,
		Model:            model,
		KeyID:            cloneStringPtr(keyID),
		DetectionEnabled: *request.DetectionEnabled,
	}); err != nil {
		logger.Error("failed to save health detection target preference: %v", err)
		SendError(ctx, fasthttp.StatusInternalServerError, "Failed to save health detection target")
		return
	}

	governancePlugin := h.manager.GetGovernancePlugin()
	runtimeTargetKey := governance.TargetKey(provider, model, derefString(keyID))
	probeConfig := governance.ActiveHealthProbeConfig{}
	activity := governance.TargetActivitySnapshot{}
	if governancePlugin != nil {
		probeConfig = governancePlugin.GetActiveHealthProbeConfig()
		if tracker := governancePlugin.GetHealthTracker(); tracker != nil {
			if !*request.DetectionEnabled {
				tracker.SetPendingFirstProbe(runtimeTargetKey, false)
			} else {
				activity = tracker.GetTargetActivity(runtimeTargetKey)
				tracker.SetPendingFirstProbe(runtimeTargetKey, activity.LastRealAccessAt.IsZero())
			}
			activity = tracker.GetTargetActivity(runtimeTargetKey)
		}
	}

	updatedTarget := *selected
	updatedTarget.DetectionEnabled = *request.DetectionEnabled
	updatedTarget.ProbeState = resolveHealthDetectionProbeState(updatedTarget.SupportStatus, updatedTarget.DetectionEnabled, activity, probeConfig.IdlePause, now)
	updatedTarget.LastRealAccessAt = formatTimePtr(activity.LastRealAccessAt)
	updatedTarget.LastProbeAt = formatTimePtr(activity.LastProbeAt)
	updatedTarget.LastProbeResult = activity.LastProbeResult
	updatedTarget.LastProbeError = activity.LastProbeError

	SendJSON(ctx, updatedTarget)
}

func (h *AdaptiveRoutingHandler) buildHealthDetectionTargets(ctx context.Context, now time.Time) ([]HealthDetectionTargetResponse, error) {
	governancePlugin := h.manager.GetGovernancePlugin()
	if governancePlugin == nil {
		return nil, fmt.Errorf("governance plugin is not available")
	}

	rules := governancePlugin.GetGovernanceStore().GetAllRoutingRules()
	tracker := governancePlugin.GetHealthTracker()
	probeConfig := governancePlugin.GetActiveHealthProbeConfig()
	prefsByKey, err := h.loadTargetPreferenceMap(ctx)
	if err != nil {
		return nil, err
	}

	records := make(map[string]*healthDetectionTargetRecord)
	for _, rule := range rules {
		if rule == nil || !rule.Enabled || !rule.GroupedRoutingEnabled || len(rule.ParsedRouteGroups) == 0 {
			continue
		}

		policy := healthPolicyOrDefault(rule)
		seenInRule := make(map[string]struct{})

		for _, group := range rule.ParsedRouteGroups {
			for _, target := range group.Targets {
				if target.Provider == nil || strings.TrimSpace(*target.Provider) == "" {
					continue
				}
				if target.Model == nil || strings.TrimSpace(*target.Model) == "" {
					continue
				}

				provider := strings.TrimSpace(*target.Provider)
				model := strings.TrimSpace(*target.Model)
				keyID := cloneStringPtr(target.KeyID)
				canonicalKey := canonicalHealthDetectionTargetKey(provider, model, keyID)
				if _, exists := seenInRule[canonicalKey]; exists {
					continue
				}
				seenInRule[canonicalKey] = struct{}{}

				record, ok := records[canonicalKey]
				if !ok {
					record = &healthDetectionTargetRecord{
						CanonicalKey:        canonicalKey,
						TargetID:            encodeHealthDetectionTargetID(provider, model, keyID),
						RuntimeTargetKey:    governance.TargetKey(provider, model, derefString(keyID)),
						Provider:            provider,
						Model:               model,
						KeyID:               keyID,
						ReferencedRuleIDs:   make([]string, 0, 1),
						ReferencedRuleNames: make([]string, 0, 1),
					}
					records[canonicalKey] = record
				}

				record.ReferencedRuleIDs = append(record.ReferencedRuleIDs, rule.ID)
				record.ReferencedRuleNames = append(record.ReferencedRuleNames, rule.Name)

				if tracker != nil {
					snap := tracker.GetTargetStatusForRule(rule.ID, record.RuntimeTargetKey, policy, now)
					if snap.Status == "cooldown" {
						record.CooldownRuleCount++
					}
				}
			}
		}
	}

	targets := make([]HealthDetectionTargetResponse, 0, len(records))
	for _, record := range records {
		activity := governance.TargetActivitySnapshot{}
		if tracker != nil {
			activity = tracker.GetTargetActivity(record.RuntimeTargetKey)
		}

		detectionEnabled := prefsByKey[record.CanonicalKey]
		supportStatus, supportReason := resolveHealthDetectionSupport(record.KeyID, activity)
		targets = append(targets, HealthDetectionTargetResponse{
			TargetID:            record.TargetID,
			Provider:            record.Provider,
			Model:               record.Model,
			KeyID:               cloneStringPtr(record.KeyID),
			ReferencedRuleIDs:   append([]string(nil), record.ReferencedRuleIDs...),
			ReferencedRuleNames: append([]string(nil), record.ReferencedRuleNames...),
			SupportStatus:       supportStatus,
			SupportReason:       supportReason,
			DetectionEnabled:    detectionEnabled,
			ProbeState:          resolveHealthDetectionProbeState(supportStatus, detectionEnabled, activity, probeConfig.IdlePause, now),
			RuleHealthSummary: HealthDetectionRuleHealthSummary{
				TotalRuleCount:    len(record.ReferencedRuleIDs),
				CooldownRuleCount: record.CooldownRuleCount,
			},
			LastRealAccessAt: formatTimePtr(activity.LastRealAccessAt),
			LastProbeAt:      formatTimePtr(activity.LastProbeAt),
			LastProbeResult:  activity.LastProbeResult,
			LastProbeError:   activity.LastProbeError,
			RuntimeScope:     healthDetectionRuntimeScopeNodeLocal,
		})
	}

	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Provider != targets[j].Provider {
			return targets[i].Provider < targets[j].Provider
		}
		if targets[i].Model != targets[j].Model {
			return targets[i].Model < targets[j].Model
		}
		return derefString(targets[i].KeyID) < derefString(targets[j].KeyID)
	})

	return targets, nil
}

func validateHealthDetectionUpdateRequest(request UpdateHealthDetectionConfigRequest) error {
	if request.Mode != healthDetectionModePassive && request.Mode != healthDetectionModeHybrid {
		return fmt.Errorf("mode must be either %q or %q", healthDetectionModePassive, healthDetectionModeHybrid)
	}
	if request.IdlePauseMinutes < 1 {
		return fmt.Errorf("idle_pause_minutes must be at least 1")
	}
	if request.ActiveHealthProbeIntervalSeconds < 1 {
		return fmt.Errorf("active_health_probe_interval_seconds must be at least 1")
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
	configMap["active_health_probe_idle_pause_minutes"] = request.IdlePauseMinutes
	configMap["active_health_probe_interval_seconds"] = request.ActiveHealthProbeIntervalSeconds
	configMap["active_health_probe_timeout_seconds"] = request.ActiveHealthProbeTimeoutSeconds
	configMap["active_health_probe_max_concurrency"] = request.ActiveHealthProbeMaxConcurrency
}

func probeConfigFromRequest(request UpdateHealthDetectionConfigRequest) governance.ActiveHealthProbeConfig {
	return governance.ActiveHealthProbeConfig{
		Enabled:        request.Mode == healthDetectionModeHybrid,
		Interval:       time.Duration(request.ActiveHealthProbeIntervalSeconds) * time.Second,
		IdlePause:      time.Duration(request.IdlePauseMinutes) * time.Minute,
		Timeout:        time.Duration(request.ActiveHealthProbeTimeoutSeconds) * time.Second,
		MaxConcurrency: request.ActiveHealthProbeMaxConcurrency,
	}
}

func responseFromProbeConfig(cfg governance.ActiveHealthProbeConfig, editable bool) HealthDetectionConfigResponse {
	mode := healthDetectionModePassive
	if cfg.Enabled {
		mode = healthDetectionModeHybrid
	}
	return HealthDetectionConfigResponse{
		Mode:                             mode,
		IdlePauseMinutes:                 durationToPositiveMinutes(cfg.IdlePause),
		ActiveHealthProbeIntervalSeconds: int(cfg.Interval / time.Second),
		ActiveHealthProbeTimeoutSeconds:  int(cfg.Timeout / time.Second),
		ActiveHealthProbeMaxConcurrency:  cfg.MaxConcurrency,
		Editable:                         editable,
	}
}

func (h *AdaptiveRoutingHandler) targetPreferenceStore() adaptiveRoutingTargetPreferenceStore {
	if h.configStore == nil {
		return nil
	}
	store, _ := h.configStore.(adaptiveRoutingTargetPreferenceStore)
	return store
}

func (h *AdaptiveRoutingHandler) loadTargetPreferenceMap(ctx context.Context) (map[string]bool, error) {
	result := make(map[string]bool)
	store := h.targetPreferenceStore()
	if store == nil {
		return result, nil
	}

	prefs, err := store.GetHealthDetectionTargetPreferences(ctx)
	if err != nil {
		return nil, err
	}
	for _, pref := range prefs {
		result[pref.TargetKey] = pref.DetectionEnabled
	}
	return result, nil
}

func resolveHealthDetectionSupport(keyID *string, activity governance.TargetActivitySnapshot) (string, string) {
	if keyID == nil || strings.TrimSpace(*keyID) == "" {
		return healthDetectionSupportUnsupported, "Missing Key ID"
	}
	if _, ok := resolveHealthDetectionRequestType(activity); !ok {
		return healthDetectionSupportUnsupported, "Unsupported request type for active probing"
	}
	return healthDetectionSupportSupported, ""
}

func resolveHealthDetectionRequestType(activity governance.TargetActivitySnapshot) (schemas.RequestType, bool) {
	if supportsHealthDetectionRequestType(activity.LastRealAccessRequestType) {
		return activity.LastRealAccessRequestType, true
	}
	if supportsHealthDetectionRequestType(activity.LastProbeRequestType) {
		return activity.LastProbeRequestType, true
	}
	if activity.LastRealAccessRequestType != "" || activity.LastProbeRequestType != "" {
		return "", false
	}
	return schemas.ChatCompletionRequest, true
}

func supportsHealthDetectionRequestType(requestType schemas.RequestType) bool {
	switch requestType {
	case schemas.ChatCompletionRequest, schemas.ResponsesRequest, schemas.TextCompletionRequest:
		return true
	default:
		return false
	}
}

func resolveHealthDetectionProbeState(
	supportStatus string,
	detectionEnabled bool,
	activity governance.TargetActivitySnapshot,
	idlePause time.Duration,
	now time.Time,
) string {
	if supportStatus != healthDetectionSupportSupported {
		return healthDetectionProbeStateUnsupported
	}
	if !detectionEnabled {
		return healthDetectionProbeStateOff
	}
	if activity.PendingFirstProbe || (activity.LastRealAccessAt.IsZero() && activity.LastProbeAt.IsZero()) {
		return healthDetectionProbeStatePendingFirstProbe
	}
	if activity.LastRealAccessAt.IsZero() {
		return healthDetectionProbeStatePausedIdle
	}
	if !activity.LastRealAccessAt.IsZero() && idlePause > 0 && now.Sub(activity.LastRealAccessAt) > idlePause {
		return healthDetectionProbeStatePausedIdle
	}
	return healthDetectionProbeStateEligible
}

func healthPolicyOrDefault(rule *configstoreTables.TableRoutingRule) *configstoreTables.HealthPolicy {
	if rule != nil && rule.ParsedHealthPolicy != nil {
		return rule.ParsedHealthPolicy
	}
	return &configstoreTables.HealthPolicy{
		FailureThreshold:     2,
		FailureWindowSeconds: 30,
		CooldownSeconds:      30,
	}
}

func formatTimePtr(value time.Time) *string {
	if value.IsZero() {
		return nil
	}
	formatted := value.UTC().Format(time.RFC3339)
	return &formatted
}

func durationToPositiveMinutes(value time.Duration) int {
	if value <= 0 {
		return 0
	}
	minutes := int(value / time.Minute)
	if value%time.Minute != 0 {
		minutes++
	}
	if minutes < 1 {
		return 1
	}
	return minutes
}

func canonicalHealthDetectionTargetKey(provider, model string, keyID *string) string {
	raw, err := sonic.Marshal([]string{provider, model, derefString(keyID)})
	if err != nil {
		return fmt.Sprintf(`["%s","%s","%s"]`, provider, model, derefString(keyID))
	}
	return string(raw)
}

func encodeHealthDetectionTargetID(provider, model string, keyID *string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(canonicalHealthDetectionTargetKey(provider, model, keyID)))
}

func decodeHealthDetectionTargetID(targetID string) (string, string, *string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(targetID)
	if err != nil {
		return "", "", nil, err
	}
	var parts []string
	if err := sonic.Unmarshal(decoded, &parts); err != nil {
		return "", "", nil, err
	}
	if len(parts) != 3 {
		return "", "", nil, fmt.Errorf("target id must contain provider, model, key_id")
	}
	var keyID *string
	if parts[2] != "" {
		keyID = &parts[2]
	}
	return parts[0], parts[1], keyID, nil
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
