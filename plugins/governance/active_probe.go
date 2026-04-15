package governance

import (
	"context"
	"strings"
	"sync"
	"time"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
)

type ActiveHealthProbeConfig struct {
	Enabled        bool
	Interval       time.Duration
	IdlePause      time.Duration
	Timeout        time.Duration
	MaxConcurrency int
}

type BifrostClientAwareGovernancePlugin interface {
	BaseGovernancePlugin
	SetBifrostClient(client *bifrost.Bifrost)
}

func (p *GovernancePlugin) GetActiveHealthProbeConfig() ActiveHealthProbeConfig {
	if p == nil {
		return ActiveHealthProbeConfig{}
	}
	p.cfgMutex.RLock()
	defer p.cfgMutex.RUnlock()
	return p.activeProbeConfig
}

type activeProbePlan struct {
	TargetKey   string
	Provider    schemas.ModelProvider
	Model       string
	KeyID       string
	RequestType schemas.RequestType
	RuleIDs     []string
}

type activeProbeResult struct {
	Success    bool
	FailureMsg string
}

type activeProbeTargetPreferenceReader interface {
	GetHealthDetectionTargetPreferences(ctx context.Context) ([]configstoreTables.TableHealthDetectionTargetPreference, error)
}

func defaultActiveHealthProbeConfig(cfg *Config) ActiveHealthProbeConfig {
	resolved := ActiveHealthProbeConfig{
		Enabled:        false,
		Interval:       15 * time.Second,
		IdlePause:      30 * time.Minute,
		Timeout:        5 * time.Second,
		MaxConcurrency: 4,
	}
	if cfg == nil {
		return resolved
	}
	if cfg.ActiveHealthProbeEnabled != nil {
		resolved.Enabled = *cfg.ActiveHealthProbeEnabled
	}
	if cfg.ActiveHealthProbeIntervalSeconds != nil && *cfg.ActiveHealthProbeIntervalSeconds > 0 {
		resolved.Interval = time.Duration(*cfg.ActiveHealthProbeIntervalSeconds) * time.Second
	}
	if cfg.ActiveHealthProbeIdlePauseMinutes != nil && *cfg.ActiveHealthProbeIdlePauseMinutes > 0 {
		resolved.IdlePause = time.Duration(*cfg.ActiveHealthProbeIdlePauseMinutes) * time.Minute
	} else if cfg.ActiveHealthProbePassiveFreshnessSeconds != nil && *cfg.ActiveHealthProbePassiveFreshnessSeconds > 0 {
		resolved.IdlePause = time.Duration(*cfg.ActiveHealthProbePassiveFreshnessSeconds) * time.Second
	}
	if cfg.ActiveHealthProbeTimeoutSeconds != nil && *cfg.ActiveHealthProbeTimeoutSeconds > 0 {
		resolved.Timeout = time.Duration(*cfg.ActiveHealthProbeTimeoutSeconds) * time.Second
	}
	if cfg.ActiveHealthProbeMaxConcurrency != nil && *cfg.ActiveHealthProbeMaxConcurrency > 0 {
		resolved.MaxConcurrency = *cfg.ActiveHealthProbeMaxConcurrency
	}
	return resolved
}

func supportsActiveProbeRequestType(requestType schemas.RequestType) bool {
	switch requestType {
	case schemas.ChatCompletionRequest, schemas.ResponsesRequest, schemas.TextCompletionRequest:
		return true
	default:
		return false
	}
}

func buildActiveProbePlans(
	rules []*configstoreTables.TableRoutingRule,
	tracker *HealthTracker,
	enabledTargets map[string]bool,
	now time.Time,
	idlePause time.Duration,
) []activeProbePlan {
	if tracker == nil {
		return nil
	}

	plansByTarget := make(map[string]*activeProbePlan)
	for _, rule := range rules {
		if rule == nil || !rule.Enabled || !rule.GroupedRoutingEnabled || len(rule.ParsedRouteGroups) == 0 {
			continue
		}
		for _, group := range rule.ParsedRouteGroups {
			for _, target := range group.Targets {
				if target.Provider == nil || strings.TrimSpace(*target.Provider) == "" {
					continue
				}
				if target.Model == nil || strings.TrimSpace(*target.Model) == "" {
					continue
				}
				if target.KeyID == nil || strings.TrimSpace(*target.KeyID) == "" {
					continue
				}

				targetKey := RouteGroupTargetKey(target)
				if !enabledTargets[targetKey] {
					continue
				}

				activity := tracker.GetTargetActivity(targetKey)
				requestType, ok := resolveActiveProbeRequestType(activity)
				if !ok {
					continue
				}

				shouldProbe := false
				switch {
				case activity.PendingFirstProbe:
					shouldProbe = true
				case activity.LastRealAccessAt.IsZero() && activity.LastProbeAt.IsZero():
					shouldProbe = true
				case activity.LastRealAccessAt.IsZero():
					shouldProbe = false
				case idlePause > 0 && now.Sub(activity.LastRealAccessAt) > idlePause:
					shouldProbe = false
				default:
					shouldProbe = true
				}
				if !shouldProbe {
					continue
				}

				plan, ok := plansByTarget[targetKey]
				if !ok {
					plan = &activeProbePlan{
						TargetKey:   targetKey,
						Provider:    schemas.ModelProvider(*target.Provider),
						Model:       *target.Model,
						KeyID:       *target.KeyID,
						RequestType: requestType,
						RuleIDs:     make([]string, 0, 1),
					}
					plansByTarget[targetKey] = plan
				}
				if !containsString(plan.RuleIDs, rule.ID) {
					plan.RuleIDs = append(plan.RuleIDs, rule.ID)
				}
			}
		}
	}

	plans := make([]activeProbePlan, 0, len(plansByTarget))
	for _, plan := range plansByTarget {
		plans = append(plans, *plan)
	}
	return plans
}

func resolveActiveProbeRequestType(activity TargetActivitySnapshot) (schemas.RequestType, bool) {
	if supportsActiveProbeRequestType(activity.LastRealAccessRequestType) {
		return activity.LastRealAccessRequestType, true
	}
	if supportsActiveProbeRequestType(activity.LastProbeRequestType) {
		return activity.LastProbeRequestType, true
	}
	if activity.LastRealAccessRequestType != "" || activity.LastProbeRequestType != "" {
		return "", false
	}
	return schemas.ChatCompletionRequest, true
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func applyActiveProbeResult(
	tracker *HealthTracker,
	plan activeProbePlan,
	result activeProbeResult,
	now time.Time,
) {
	if tracker == nil || plan.TargetKey == "" {
		return
	}
	tracker.RecordProbeResult(plan.TargetKey, plan.RequestType, result.Success, result.FailureMsg, now)
	for _, ruleID := range plan.RuleIDs {
		if result.Success {
			tracker.RecordSuccessForRule(ruleID, plan.TargetKey)
			continue
		}
		tracker.RecordFailureForRule(ruleID, plan.TargetKey, result.FailureMsg, now)
	}
}

func (p *GovernancePlugin) SetBifrostClient(client *bifrost.Bifrost) {
	if p == nil || client == nil {
		return
	}

	p.cfgMutex.Lock()
	p.bifrostClient = client
	p.cfgMutex.Unlock()

	if !p.activeProbeConfig.Enabled {
		return
	}
	p.activeProbeStartOnce.Do(func() {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			p.runActiveProbeLoop()
		}()
	})
}

func (p *GovernancePlugin) runActiveProbeLoop() {
	if p == nil {
		return
	}

	p.runActiveProbeCycle()

	ticker := time.NewTicker(p.activeProbeConfig.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.runActiveProbeCycle()
		}
	}
}

func (p *GovernancePlugin) runActiveProbeCycle() {
	if p == nil || p.healthTracker == nil || p.store == nil {
		return
	}

	p.cfgMutex.RLock()
	client := p.bifrostClient
	cfg := p.activeProbeConfig
	p.cfgMutex.RUnlock()
	if client == nil || !cfg.Enabled {
		return
	}

	enabledTargets, err := loadEnabledActiveProbeTargets(p.ctx, p.configStore)
	if err != nil && p.logger != nil {
		p.logger.Warn("failed to load active probe target preferences: %v", err)
	}

	plans := buildActiveProbePlans(p.store.GetAllRoutingRules(), p.healthTracker, enabledTargets, time.Now(), cfg.IdlePause)
	if len(plans) == 0 {
		return
	}

	semaphore := make(chan struct{}, cfg.MaxConcurrency)
	var wg sync.WaitGroup
	for _, plan := range plans {
		select {
		case <-p.ctx.Done():
			wg.Wait()
			return
		case semaphore <- struct{}{}:
		}

		wg.Add(1)
		go func(plan activeProbePlan) {
			defer wg.Done()
			defer func() { <-semaphore }()

			now := time.Now()
			result := p.executeActiveProbe(plan, now)
			applyActiveProbeResult(p.healthTracker, plan, result, now)
		}(plan)
	}
	wg.Wait()
}

func loadEnabledActiveProbeTargets(ctx context.Context, store any) (map[string]bool, error) {
	enabledTargets := make(map[string]bool)
	reader, ok := store.(activeProbeTargetPreferenceReader)
	if !ok || reader == nil {
		return enabledTargets, nil
	}

	prefs, err := reader.GetHealthDetectionTargetPreferences(ctx)
	if err != nil {
		return nil, err
	}

	for _, pref := range prefs {
		if !pref.DetectionEnabled {
			continue
		}
		keyID := ""
		if pref.KeyID != nil {
			keyID = *pref.KeyID
		}
		enabledTargets[TargetKey(pref.Provider, pref.Model, keyID)] = true
	}

	return enabledTargets, nil
}

func (p *GovernancePlugin) executeActiveProbe(plan activeProbePlan, now time.Time) activeProbeResult {
	p.cfgMutex.RLock()
	client := p.bifrostClient
	timeout := p.activeProbeConfig.Timeout
	p.cfgMutex.RUnlock()
	if client == nil {
		return activeProbeResult{Success: false, FailureMsg: "bifrost client not set"}
	}

	ctx := schemas.NewBifrostContext(p.ctx, now.Add(timeout))
	defer ctx.Cancel()
	ctx.SetValue(schemas.BifrostContextKeyAPIKeyID, plan.KeyID)
	ctx.SetValue(schemas.BifrostContextKeyDisableProviderRetries, true)
	ctx.SetValue(schemas.BifrostContextKeySkipPluginPipeline, true)

	switch plan.RequestType {
	case schemas.ChatCompletionRequest:
		req := buildChatActiveProbeRequest(plan)
		_, err := client.ChatCompletionRequest(ctx, req)
		return activeProbeResultFromError(err)
	case schemas.ResponsesRequest:
		req := buildResponsesActiveProbeRequest(plan)
		_, err := client.ResponsesRequest(ctx, req)
		return activeProbeResultFromError(err)
	case schemas.TextCompletionRequest:
		req := buildTextActiveProbeRequest(plan)
		_, err := client.TextCompletionRequest(ctx, req)
		return activeProbeResultFromError(err)
	default:
		return activeProbeResult{Success: false, FailureMsg: "unsupported request type for active probe"}
	}
}

func activeProbeResultFromError(err *schemas.BifrostError) activeProbeResult {
	if err == nil {
		return activeProbeResult{Success: true}
	}
	failureMsg := "active probe failed"
	if err.Error != nil && err.Error.Message != "" {
		failureMsg = err.Error.Message
	}
	return activeProbeResult{
		Success:    false,
		FailureMsg: failureMsg,
	}
}

func buildChatActiveProbeRequest(plan activeProbePlan) *schemas.BifrostChatRequest {
	probeText := "ping"
	maxTokens := 1
	return &schemas.BifrostChatRequest{
		Provider: plan.Provider,
		Model:    plan.Model,
		Input: []schemas.ChatMessage{
			{
				Role: schemas.ChatMessageRoleUser,
				Content: &schemas.ChatMessageContent{
					ContentStr: &probeText,
				},
			},
		},
		Params: &schemas.ChatParameters{
			MaxCompletionTokens: &maxTokens,
		},
	}
}

func buildResponsesActiveProbeRequest(plan activeProbePlan) *schemas.BifrostResponsesRequest {
	probeText := "ping"
	maxTokens := 1
	return &schemas.BifrostResponsesRequest{
		Provider: plan.Provider,
		Model:    plan.Model,
		Input: []schemas.ResponsesMessage{
			{
				Role: bifrost.Ptr(schemas.ResponsesInputMessageRoleUser),
				Content: &schemas.ResponsesMessageContent{
					ContentStr: &probeText,
				},
			},
		},
		Params: &schemas.ResponsesParameters{
			MaxOutputTokens: &maxTokens,
		},
	}
}

func buildTextActiveProbeRequest(plan activeProbePlan) *schemas.BifrostTextCompletionRequest {
	probeText := "ping"
	maxTokens := 1
	return &schemas.BifrostTextCompletionRequest{
		Provider: plan.Provider,
		Model:    plan.Model,
		Input: &schemas.TextCompletionInput{
			PromptStr: &probeText,
		},
		Params: &schemas.TextCompletionParameters{
			MaxTokens: &maxTokens,
		},
	}
}
