package handlers

import (
	"context"
	"sort"
	"strings"
	"time"

	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/logstore"
	"github.com/maximhq/bifrost/plugins/governance"
	"github.com/valyala/fasthttp"
)

const (
	defaultAdaptiveDashboardWindow   = "today"
	defaultAdaptiveDashboardTimezone = "Asia/Shanghai"
	defaultAdaptiveDashboardCacheTTL = 15 * time.Second
	maxAdaptiveDashboardCacheEntries = 64
)

type adaptiveDashboardMetricsStore interface {
	GetAdaptiveDashboardMetrics(ctx context.Context, query logstore.AdaptiveDashboardMetricsQuery) (*logstore.AdaptiveDashboardMetricsResult, error)
}

type adaptiveDashboardRuleInspector interface {
	InspectAdaptiveDashboardRule(rule *configstoreTables.TableRoutingRule, now time.Time) *governance.AdaptiveDashboardRuleRuntime
}

type adaptiveMetricsCacheEntry struct {
	result      *logstore.AdaptiveDashboardMetricsResult
	cachedAt    time.Time
	windowStart time.Time
	windowEnd   time.Time
}

// AdaptiveDashboardRuleOption summarizes one grouped/adaptive rule for the selector dropdown.
type AdaptiveDashboardRuleOption struct {
	RuleID      string `json:"rule_id"`
	RuleName    string `json:"rule_name"`
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
	EngineMode  string `json:"engine_mode"`
	TargetCount int    `json:"target_count"`
}

// AdaptiveDashboardTargetMetrics holds windowed single-hop and Profit Ledger metrics for one target.
type AdaptiveDashboardTargetMetrics struct {
	RequestCount         int64    `json:"request_count"`
	SuccessCount         int64    `json:"success_count"`
	ErrorCount           int64    `json:"error_count"`
	SingleHopSuccessRate *float64 `json:"single_hop_success_rate,omitempty"`
	AvgLatencyMs         *float64 `json:"avg_latency_ms,omitempty"`
	P50LatencyMs         *float64 `json:"p50_latency_ms,omitempty"`
	P80LatencyMs         *float64 `json:"p80_latency_ms,omitempty"`
	P95LatencyMs         *float64 `json:"p95_latency_ms,omitempty"`
	RevenueUSD           float64  `json:"revenue_usd"`
	CostUSD              float64  `json:"cost_usd"`
	ProfitUSD            float64  `json:"profit_usd"`
	GrossMargin          *float64 `json:"gross_margin,omitempty"`
	PromptTokens         int64    `json:"prompt_tokens"`
	CompletionTokens     int64    `json:"completion_tokens"`
	TotalTokens          int64    `json:"total_tokens"`
	MissingCostCount     int64    `json:"missing_cost_count"`
	MissingTokensCount   int64    `json:"missing_tokens_count"`
}

// AdaptiveDashboardTargetRow merges live runtime state with windowed business metrics.
type AdaptiveDashboardTargetRow struct {
	governance.AdaptiveDashboardTargetRuntime
	Metrics AdaptiveDashboardTargetMetrics `json:"metrics"`
}

// AdaptiveDashboardTierSummary groups targets into L1/L2/L3 with per-tier subtotals.
type AdaptiveDashboardTierSummary struct {
	Tier         string                       `json:"tier"`
	SuccessCount int64                        `json:"success_count"`
	RequestCount int64                        `json:"request_count"`
	SuccessShare float64                      `json:"success_share"`
	RevenueUSD   float64                      `json:"revenue_usd"`
	CostUSD      float64                      `json:"cost_usd"`
	ProfitUSD    float64                      `json:"profit_usd"`
	GrossMargin  *float64                     `json:"gross_margin,omitempty"`
	Targets      []AdaptiveDashboardTargetRow `json:"targets"`
}

// AdaptiveDashboardKPI holds the top-bar customer-facing and financial KPI metrics.
type AdaptiveDashboardKPI struct {
	RootRequestCount      int64    `json:"root_request_count"`
	RootSuccessCount      int64    `json:"root_success_count"`
	FirstHopSuccessCount  int64    `json:"first_hop_success_count"`
	RescuedCount          int64    `json:"rescued_count"`
	RootSuccessRate       *float64 `json:"root_success_rate,omitempty"`
	FirstHopSuccessRate   *float64 `json:"first_hop_success_rate,omitempty"`
	AvgLatencyMs          *float64 `json:"avg_latency_ms,omitempty"`
	P50LatencyMs          *float64 `json:"p50_latency_ms,omitempty"`
	P80LatencyMs          *float64 `json:"p80_latency_ms,omitempty"`
	P95LatencyMs          *float64 `json:"p95_latency_ms,omitempty"`
	TotalRevenueUSD       float64  `json:"total_revenue_usd"`
	TotalCostUSD          float64  `json:"total_cost_usd"`
	TotalProfitUSD        float64  `json:"total_profit_usd"`
	GrossMargin           *float64 `json:"gross_margin,omitempty"`
	EffectiveOfficialPct  *float64 `json:"effective_official_cost_pct,omitempty"`
	TotalPromptTokens     int64    `json:"total_prompt_tokens"`
	TotalCompletionTokens int64    `json:"total_completion_tokens"`
	TotalTokens           int64    `json:"total_tokens"`
	MissingCostCount      int64    `json:"missing_cost_count"`
	MissingTokensCount    int64    `json:"missing_tokens_count"`
	TotalTargetSuccesses  int64    `json:"total_target_successes"`
}

// AdaptiveDashboardResponse is the payload for GET /api/governance/adaptive-dashboard.
type AdaptiveDashboardResponse struct {
	GeneratedAt     time.Time                                `json:"generated_at"`
	Window          string                                   `json:"window"`
	WindowStart     time.Time                                `json:"window_start"`
	WindowEnd       time.Time                                `json:"window_end"`
	Timezone        string                                   `json:"timezone"`
	LogsConnected   bool                                     `json:"logs_connected"`
	MetricsCachedAt *time.Time                               `json:"metrics_cached_at,omitempty"`
	Rules           []AdaptiveDashboardRuleOption            `json:"rules"`
	SelectedRule    *governance.AdaptiveDashboardRuleRuntime `json:"selected_rule,omitempty"`
	KPI             AdaptiveDashboardKPI                     `json:"kpi"`
	Tiers           []AdaptiveDashboardTierSummary           `json:"tiers"`
}

// SetLogStore attaches an optional LogStore for windowed business & profit telemetry.
func (h *AdaptiveRoutingHandler) SetLogStore(store logstore.LogStore) {
	if store == nil {
		h.logStore = nil
		return
	}
	if metricsStore, ok := store.(adaptiveDashboardMetricsStore); ok {
		h.logStore = metricsStore
	}
}

func (h *AdaptiveRoutingHandler) setMetricsStore(store adaptiveDashboardMetricsStore) {
	h.logStore = store
}

func (h *AdaptiveRoutingHandler) getAdaptiveDashboard(ctx *fasthttp.RequestCtx) {
	governancePlugin := h.manager.GetGovernancePlugin()
	if governancePlugin == nil {
		SendError(ctx, fasthttp.StatusServiceUnavailable, "Governance plugin is not available")
		return
	}
	store := governancePlugin.GetGovernanceStore()
	if store == nil {
		SendError(ctx, fasthttp.StatusServiceUnavailable, "Governance store is not available")
		return
	}

	windowParam := strings.TrimSpace(string(ctx.QueryArgs().Peek("window")))
	window, ok := normalizeAdaptiveDashboardWindow(windowParam)
	if !ok {
		SendError(ctx, fasthttp.StatusBadRequest, "Invalid window parameter. Supported values: 1h, today, 24h, 7d")
		return
	}
	requestedRuleID := strings.TrimSpace(string(ctx.QueryArgs().Peek("rule_id")))
	refreshRaw := strings.ToLower(strings.TrimSpace(string(ctx.QueryArgs().Peek("refresh"))))
	forceRefresh := refreshRaw == "true" || refreshRaw == "1"

	now := time.Now()
	windowStart, windowEnd := resolveAdaptiveDashboardWindowRange(window, now)

	allRules := store.GetAllRoutingRules()
	type ruleWithMode struct {
		rule        int
		ruleID      string
		ruleName    string
		description string
		enabled     bool
		priority    int
		engineMode  string
		targetCount int
	}

	groupedIndices := make([]ruleWithMode, 0, len(allRules))
	for i, r := range allRules {
		if r == nil || !r.GroupedRoutingEnabled || len(r.ParsedRouteGroups) == 0 {
			continue
		}
		policy := governance.ApplyHealthPolicyDefaults(r.ParsedHealthPolicy)
		mode := "grouped"
		if policy.AdaptiveRankingEnabled != nil && *policy.AdaptiveRankingEnabled {
			if policy.AdaptiveV22Enabled {
				mode = "adaptive_v22"
			} else {
				mode = "adaptive_v21"
			}
		}
		targetCount := 0
		seen := make(map[string]struct{})
		for _, g := range r.ParsedRouteGroups {
			for _, t := range g.Targets {
				if t.Provider == nil || strings.TrimSpace(*t.Provider) == "" {
					continue
				}
				k := governance.RouteGroupTargetKey(t)
				if _, dup := seen[k]; !dup {
					seen[k] = struct{}{}
					targetCount++
				}
			}
		}
		groupedIndices = append(groupedIndices, ruleWithMode{
			rule:        i,
			ruleID:      r.ID,
			ruleName:    r.Name,
			description: r.Description,
			enabled:     r.Enabled,
			priority:    r.Priority,
			engineMode:  mode,
			targetCount: targetCount,
		})
	}

	modeRank := func(m string) int {
		switch m {
		case "adaptive_v22":
			return 0
		case "adaptive_v21":
			return 1
		default:
			return 2
		}
	}
	sort.SliceStable(groupedIndices, func(i, j int) bool {
		a, b := groupedIndices[i], groupedIndices[j]
		if a.enabled != b.enabled {
			return a.enabled
		}
		if modeRank(a.engineMode) != modeRank(b.engineMode) {
			return modeRank(a.engineMode) < modeRank(b.engineMode)
		}
		if a.priority != b.priority {
			return a.priority < b.priority
		}
		return a.ruleName < b.ruleName
	})

	options := make([]AdaptiveDashboardRuleOption, 0, len(groupedIndices))
	selectedRuleIdx := -1
	for idx, item := range groupedIndices {
		options = append(options, AdaptiveDashboardRuleOption{
			RuleID:      item.ruleID,
			RuleName:    item.ruleName,
			Description: item.description,
			Enabled:     item.enabled,
			EngineMode:  item.engineMode,
			TargetCount: item.targetCount,
		})
		if requestedRuleID != "" && item.ruleID == requestedRuleID {
			selectedRuleIdx = item.rule
		} else if requestedRuleID == "" && idx == 0 {
			selectedRuleIdx = item.rule
		}
	}

	resp := AdaptiveDashboardResponse{
		GeneratedAt:   now.UTC(),
		Window:        window,
		WindowStart:   windowStart.UTC(),
		WindowEnd:     windowEnd.UTC(),
		Timezone:      defaultAdaptiveDashboardTimezone,
		LogsConnected: h.logStore != nil,
		Rules:         options,
		Tiers:         []AdaptiveDashboardTierSummary{},
	}

	if selectedRuleIdx < 0 {
		if requestedRuleID != "" && len(groupedIndices) > 0 {
			SendError(ctx, fasthttp.StatusNotFound, "Routing rule not found")
			return
		}
		SendJSON(ctx, resp)
		return
	}

	selectedTableRule := allRules[selectedRuleIdx]

	// 1. Live in-memory runtime snapshot (never cached)
	var runtimeView *governance.AdaptiveDashboardRuleRuntime
	if inspector, ok := governancePlugin.(adaptiveDashboardRuleInspector); ok {
		runtimeView = inspector.InspectAdaptiveDashboardRule(selectedTableRule, now)
	} else {
		runtimeView = governance.InspectAdaptiveRuleDashboard(
			selectedTableRule,
			governancePlugin.GetHealthTracker(),
			nil,
			nil,
			nil,
			now,
		)
	}
	resp.SelectedRule = runtimeView

	// 2. Windowed business & profit telemetry from LogStore (15s TTL cache)
	var metricsRes *logstore.AdaptiveDashboardMetricsResult
	if h.logStore != nil {
		var cachedAt time.Time
		var err error
		metricsRes, cachedAt, err = h.loadAdaptiveDashboardMetrics(ctx, selectedTableRule.ID, window, windowStart, windowEnd, now, forceRefresh)
		if err != nil {
			logger.Error("failed to load adaptive dashboard metrics for rule %s: %v", selectedTableRule.ID, err)
			SendError(ctx, fasthttp.StatusInternalServerError, "Failed to load adaptive dashboard metrics")
			return
		}
		if !cachedAt.IsZero() {
			t := cachedAt.UTC()
			resp.MetricsCachedAt = &t
		}
	}

	// 3. Merge live runtime targets with windowed DB metrics and group into L1 / L2 / L3 tiers
	resp.KPI, resp.Tiers = mergeAdaptiveDashboardView(runtimeView, metricsRes)
	SendJSON(ctx, resp)
}

func (h *AdaptiveRoutingHandler) loadAdaptiveDashboardMetrics(
	ctx context.Context,
	ruleID, window string,
	windowStart, windowEnd, now time.Time,
	forceRefresh bool,
) (*logstore.AdaptiveDashboardMetricsResult, time.Time, error) {
	ttl := h.metricsCacheTTL
	if ttl <= 0 {
		ttl = defaultAdaptiveDashboardCacheTTL
	}
	cacheKey := ruleID + "|" + window

	if !forceRefresh {
		h.metricsCacheMu.RLock()
		entry, ok := h.metricsCache[cacheKey]
		h.metricsCacheMu.RUnlock()
		if ok && now.Sub(entry.cachedAt) < ttl {
			return entry.result, entry.cachedAt, nil
		}
	}

	res, err := h.logStore.GetAdaptiveDashboardMetrics(ctx, logstore.AdaptiveDashboardMetricsQuery{
		RoutingRuleID: ruleID,
		StartTime:     windowStart,
		EndTime:       windowEnd,
	})
	if err != nil {
		return nil, time.Time{}, err
	}

	h.metricsCacheMu.Lock()
	if h.metricsCache == nil {
		h.metricsCache = make(map[string]adaptiveMetricsCacheEntry)
	}
	if len(h.metricsCache) >= maxAdaptiveDashboardCacheEntries {
		for k, v := range h.metricsCache {
			if now.Sub(v.cachedAt) >= ttl {
				delete(h.metricsCache, k)
			}
		}
		if len(h.metricsCache) >= maxAdaptiveDashboardCacheEntries {
			h.metricsCache = make(map[string]adaptiveMetricsCacheEntry)
		}
	}
	h.metricsCache[cacheKey] = adaptiveMetricsCacheEntry{
		result:      res,
		cachedAt:    now,
		windowStart: windowStart,
		windowEnd:   windowEnd,
	}
	h.metricsCacheMu.Unlock()

	return res, now, nil
}

func normalizeAdaptiveDashboardWindow(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "today":
		return "today", true
	case "1h":
		return "1h", true
	case "24h":
		return "24h", true
	case "7d":
		return "7d", true
	default:
		return "", false
	}
}

func resolveAdaptiveDashboardWindowRange(window string, now time.Time) (time.Time, time.Time) {
	loc, err := time.LoadLocation(defaultAdaptiveDashboardTimezone)
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	switch window {
	case "1h":
		return now.Add(-1 * time.Hour).UTC(), now.UTC()
	case "24h":
		return now.Add(-24 * time.Hour).UTC(), now.UTC()
	case "7d":
		return now.Add(-7 * 24 * time.Hour).UTC(), now.UTC()
	default: // "today"
		localNow := now.In(loc)
		startLocal := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)
		return startLocal.UTC(), now.UTC()
	}
}

func mergeAdaptiveDashboardView(
	runtimeView *governance.AdaptiveDashboardRuleRuntime,
	metrics *logstore.AdaptiveDashboardMetricsResult,
) (AdaptiveDashboardKPI, []AdaptiveDashboardTierSummary) {
	var kpi AdaptiveDashboardKPI
	if metrics != nil {
		kpi = AdaptiveDashboardKPI{
			RootRequestCount:      metrics.RootSummary.RootRequestCount,
			RootSuccessCount:      metrics.RootSummary.RootSuccessCount,
			FirstHopSuccessCount:  metrics.RootSummary.FirstHopSuccessCount,
			RescuedCount:          metrics.RootSummary.RescuedCount,
			RootSuccessRate:       metrics.RootSummary.SuccessRate,
			FirstHopSuccessRate:   metrics.RootSummary.FirstHopSuccessRate,
			AvgLatencyMs:          metrics.RootSummary.AvgLatencyMs,
			P50LatencyMs:          metrics.RootSummary.P50LatencyMs,
			P80LatencyMs:          metrics.RootSummary.P80LatencyMs,
			P95LatencyMs:          metrics.RootSummary.P95LatencyMs,
			TotalRevenueUSD:       metrics.TotalRevenueUSD,
			TotalCostUSD:          metrics.TotalCostUSD,
			TotalProfitUSD:        metrics.TotalProfitUSD,
			GrossMargin:           metrics.GrossMargin,
			TotalPromptTokens:     metrics.TotalPromptTokens,
			TotalCompletionTokens: metrics.TotalCompletionTokens,
			TotalTokens:           metrics.TotalTokens,
			MissingCostCount:      metrics.MissingCostCount,
			MissingTokensCount:    metrics.MissingTokensCount,
		}
	}

	if runtimeView == nil {
		return kpi, []AdaptiveDashboardTierSummary{}
	}

	// Calculate effective cost % relative to official 100% baseline using actual window tokens
	if runtimeView.OfficialBaselineInputPer1MUSD != nil && runtimeView.OfficialBaselineOutputPer1MUSD != nil && kpi.TotalTokens > 0 {
		officialRefUSD := float64(kpi.TotalPromptTokens)*(*runtimeView.OfficialBaselineInputPer1MUSD)/1_000_000 +
			float64(kpi.TotalCompletionTokens)*(*runtimeView.OfficialBaselineOutputPer1MUSD)/1_000_000
		if officialRefUSD > 0 {
			pct := kpi.TotalCostUSD / officialRefUSD * 100.0
			kpi.EffectiveOfficialPct = &pct
		}
	}

	// Build target metric lookup (exact key-pinned first, then wildcard key fallback)
	type exactKey struct {
		provider string
		model    string
		keyID    string
	}
	type wildcardKey struct {
		provider string
		model    string
	}

	exactIndex := make(map[exactKey]int, len(runtimeView.Targets))
	wildcardIndex := make(map[wildcardKey]int, len(runtimeView.Targets))
	for i, tgt := range runtimeView.Targets {
		if tgt.KeyID != "" {
			exactIndex[exactKey{provider: tgt.Provider, model: tgt.Model, keyID: tgt.KeyID}] = i
		} else {
			if _, exists := wildcardIndex[wildcardKey{provider: tgt.Provider, model: tgt.Model}]; !exists {
				wildcardIndex[wildcardKey{provider: tgt.Provider, model: tgt.Model}] = i
			}
		}
	}

	type metricAccum struct {
		m             AdaptiveDashboardTargetMetrics
		latWeighted   float64
		latWeight     int64
		p50Weighted   float64
		p80Weighted   float64
		p95Weighted   float64
		pctWeight     int64
		matchedGroups int
	}

	accums := make([]metricAccum, len(runtimeView.Targets))
	if metrics != nil {
		for _, stat := range metrics.Targets {
			targetIdx := -1
			if idx, ok := exactIndex[exactKey{provider: stat.Provider, model: stat.Model, keyID: stat.SelectedKeyID}]; ok {
				targetIdx = idx
			} else if idx, ok := wildcardIndex[wildcardKey{provider: stat.Provider, model: stat.Model}]; ok {
				targetIdx = idx
			}
			if targetIdx < 0 {
				continue
			}

			acc := &accums[targetIdx]
			acc.matchedGroups++
			acc.m.RequestCount += stat.RequestCount
			acc.m.SuccessCount += stat.SuccessCount
			acc.m.ErrorCount += stat.ErrorCount
			acc.m.RevenueUSD += stat.RevenueUSD
			acc.m.CostUSD += stat.CostUSD
			acc.m.ProfitUSD += stat.ProfitUSD
			acc.m.PromptTokens += stat.PromptTokens
			acc.m.CompletionTokens += stat.CompletionTokens
			acc.m.TotalTokens += stat.TotalTokens
			acc.m.MissingCostCount += stat.MissingCostCount
			acc.m.MissingTokensCount += stat.MissingTokensCount

			weight := stat.SuccessCount
			if weight <= 0 {
				weight = 1
			}
			if stat.AvgLatencyMs != nil {
				acc.latWeighted += (*stat.AvgLatencyMs) * float64(weight)
				acc.latWeight += weight
			}
			if stat.P50LatencyMs != nil && stat.P80LatencyMs != nil && stat.P95LatencyMs != nil {
				if acc.matchedGroups == 1 {
					v50, v80, v95 := *stat.P50LatencyMs, *stat.P80LatencyMs, *stat.P95LatencyMs
					acc.m.P50LatencyMs = &v50
					acc.m.P80LatencyMs = &v80
					acc.m.P95LatencyMs = &v95
				}
				acc.p50Weighted += (*stat.P50LatencyMs) * float64(weight)
				acc.p80Weighted += (*stat.P80LatencyMs) * float64(weight)
				acc.p95Weighted += (*stat.P95LatencyMs) * float64(weight)
				acc.pctWeight += weight
			}
		}
	}

	tierOrder := []string{"L1", "L2", "L3"}
	tiersByID := make(map[string]*AdaptiveDashboardTierSummary, 3)
	var totalTargetSuccesses int64

	for i, tgt := range runtimeView.Targets {
		acc := &accums[i]
		m := acc.m
		if m.RequestCount > 0 {
			rate := float64(m.SuccessCount) / float64(m.RequestCount) * 100.0
			m.SingleHopSuccessRate = &rate
		}
		if m.RevenueUSD > 0 {
			gm := m.ProfitUSD / m.RevenueUSD
			m.GrossMargin = &gm
		}
		if acc.latWeight > 0 {
			avg := acc.latWeighted / float64(acc.latWeight)
			m.AvgLatencyMs = &avg
		}
		if acc.matchedGroups > 1 && acc.pctWeight > 0 {
			p50 := acc.p50Weighted / float64(acc.pctWeight)
			p80 := acc.p80Weighted / float64(acc.pctWeight)
			p95 := acc.p95Weighted / float64(acc.pctWeight)
			m.P50LatencyMs = &p50
			m.P80LatencyMs = &p80
			m.P95LatencyMs = &p95
		}

		tierID := tgt.Tier
		if tierID == "" {
			tierID = "L1"
		}
		ts, exists := tiersByID[tierID]
		if !exists {
			ts = &AdaptiveDashboardTierSummary{
				Tier:    tierID,
				Targets: []AdaptiveDashboardTargetRow{},
			}
			tiersByID[tierID] = ts
		}
		ts.Targets = append(ts.Targets, AdaptiveDashboardTargetRow{
			AdaptiveDashboardTargetRuntime: tgt,
			Metrics:                        m,
		})
		ts.SuccessCount += m.SuccessCount
		ts.RequestCount += m.RequestCount
		ts.RevenueUSD += m.RevenueUSD
		ts.CostUSD += m.CostUSD
		ts.ProfitUSD += m.ProfitUSD
		totalTargetSuccesses += m.SuccessCount
	}

	kpi.TotalTargetSuccesses = totalTargetSuccesses

	tiers := make([]AdaptiveDashboardTierSummary, 0, len(tiersByID))
	for _, id := range tierOrder {
		ts, ok := tiersByID[id]
		if !ok || len(ts.Targets) == 0 {
			continue
		}
		if totalTargetSuccesses > 0 {
			ts.SuccessShare = float64(ts.SuccessCount) / float64(totalTargetSuccesses) * 100.0
		}
		if ts.RevenueUSD > 0 {
			gm := ts.ProfitUSD / ts.RevenueUSD
			ts.GrossMargin = &gm
		}
		tiers = append(tiers, *ts)
	}

	return kpi, tiers
}
