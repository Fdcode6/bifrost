package logstore

import (
	"context"
	"database/sql"
	"sort"
	"time"

	"golang.org/x/sync/errgroup"
)

// AdaptiveDashboardMetricsQuery scopes windowed routing and profit telemetry to one routing rule.
type AdaptiveDashboardMetricsQuery struct {
	RoutingRuleID string
	StartTime     time.Time
	EndTime       time.Time
}

// AdaptiveDashboardRootSummary holds customer-facing root request success and end-to-end latency metrics.
type AdaptiveDashboardRootSummary struct {
	RootRequestCount     int64    `json:"root_request_count"`
	RootSuccessCount     int64    `json:"root_success_count"`
	FirstHopSuccessCount int64    `json:"first_hop_success_count"`
	RescuedCount         int64    `json:"rescued_count"`
	SuccessRate          *float64 `json:"success_rate,omitempty"`
	FirstHopSuccessRate  *float64 `json:"first_hop_success_rate,omitempty"`
	AvgLatencyMs         *float64 `json:"avg_latency_ms,omitempty"`
	P50LatencyMs         *float64 `json:"p50_latency_ms,omitempty"`
	P80LatencyMs         *float64 `json:"p80_latency_ms,omitempty"`
	P95LatencyMs         *float64 `json:"p95_latency_ms,omitempty"`
}

// AdaptiveDashboardTargetStats holds single-hop request counts, success latency percentiles,
// and Profit Ledger revenue/cost/profit metrics for one (provider, model, selected_key_id) tuple.
type AdaptiveDashboardTargetStats struct {
	Provider           string   `json:"provider"`
	Model              string   `json:"model"`
	SelectedKeyID      string   `json:"selected_key_id"`
	RequestCount       int64    `json:"request_count"`
	SuccessCount       int64    `json:"success_count"`
	ErrorCount         int64    `json:"error_count"`
	AvgLatencyMs       *float64 `json:"avg_latency_ms,omitempty"`
	P50LatencyMs       *float64 `json:"p50_latency_ms,omitempty"`
	P80LatencyMs       *float64 `json:"p80_latency_ms,omitempty"`
	P95LatencyMs       *float64 `json:"p95_latency_ms,omitempty"`
	RevenueUSD         float64  `json:"revenue_usd"`
	CostUSD            float64  `json:"cost_usd"`
	ProfitUSD          float64  `json:"profit_usd"`
	GrossMargin        *float64 `json:"gross_margin,omitempty"`
	PromptTokens       int64    `json:"prompt_tokens"`
	CompletionTokens   int64    `json:"completion_tokens"`
	TotalTokens        int64    `json:"total_tokens"`
	MissingCostCount   int64    `json:"missing_cost_count"`
	MissingTokensCount int64    `json:"missing_tokens_count"`
}

// AdaptiveDashboardMetricsResult aggregates root-level and per-target business telemetry for a rule window.
type AdaptiveDashboardMetricsResult struct {
	RootSummary           AdaptiveDashboardRootSummary   `json:"root_summary"`
	Targets               []AdaptiveDashboardTargetStats `json:"targets"`
	TotalRevenueUSD       float64                        `json:"total_revenue_usd"`
	TotalCostUSD          float64                        `json:"total_cost_usd"`
	TotalProfitUSD        float64                        `json:"total_profit_usd"`
	GrossMargin           *float64                       `json:"gross_margin,omitempty"`
	TotalPromptTokens     int64                          `json:"total_prompt_tokens"`
	TotalCompletionTokens int64                          `json:"total_completion_tokens"`
	TotalTokens           int64                          `json:"total_tokens"`
	MissingCostCount      int64                          `json:"missing_cost_count"`
	MissingTokensCount    int64                          `json:"missing_tokens_count"`
}

type adaptiveTargetKey struct {
	provider string
	model    string
	keyID    string
}

type adaptiveTargetHopAgg struct {
	Provider      string
	Model         string
	SelectedKeyID string
	RequestCount  int64
	SuccessCount  int64
	ErrorCount    int64
	AvgLatencyMs  *float64
	P50LatencyMs  *float64
	P80LatencyMs  *float64
	P95LatencyMs  *float64
}

type adaptiveTargetProfitAgg struct {
	Provider           string  `gorm:"column:provider"`
	Model              string  `gorm:"column:model"`
	SelectedKeyID      string  `gorm:"column:selected_key_id"`
	RevenueUSD         float64 `gorm:"column:revenue_usd"`
	CostUSD            float64 `gorm:"column:cost_usd"`
	ProfitUSD          float64 `gorm:"column:profit_usd"`
	PromptTokens       int64   `gorm:"column:prompt_tokens"`
	CompletionTokens   int64   `gorm:"column:completion_tokens"`
	TotalTokens        int64   `gorm:"column:total_tokens"`
	MissingCostCount   int64   `gorm:"column:missing_cost_count"`
	MissingTokensCount int64   `gorm:"column:missing_tokens_count"`
}

// GetAdaptiveDashboardMetrics aggregates customer-facing root request success & latency,
// per-target single-hop performance, and Profit Ledger financials for one routing rule and time window.
func (s *RDBLogStore) GetAdaptiveDashboardMetrics(ctx context.Context, query AdaptiveDashboardMetricsQuery) (*AdaptiveDashboardMetricsResult, error) {
	result := &AdaptiveDashboardMetricsResult{
		Targets: []AdaptiveDashboardTargetStats{},
	}
	if query.RoutingRuleID == "" {
		return result, nil
	}

	startUTC := query.StartTime.UTC()
	endUTC := query.EndTime.UTC()
	isPostgres := s.db.Dialector.Name() == "postgres"

	var (
		rootSummary AdaptiveDashboardRootSummary
		hopRows     []adaptiveTargetHopAgg
		profitRows  []adaptiveTargetProfitAgg
	)

	g, gCtx := errgroup.WithContext(ctx)

	g.Go(func() error {
		var err error
		if isPostgres {
			rootSummary, err = s.getAdaptiveRootSummaryPostgres(gCtx, query.RoutingRuleID, startUTC, endUTC)
		} else {
			rootSummary, err = s.getAdaptiveRootSummaryGeneric(gCtx, query.RoutingRuleID, startUTC, endUTC)
		}
		return err
	})

	g.Go(func() error {
		var err error
		if isPostgres {
			hopRows, err = s.getAdaptiveTargetHopsPostgres(gCtx, query.RoutingRuleID, startUTC, endUTC)
		} else {
			hopRows, err = s.getAdaptiveTargetHopsGeneric(gCtx, query.RoutingRuleID, startUTC, endUTC)
		}
		return err
	})

	g.Go(func() error {
		var err error
		profitRows, err = s.getAdaptiveTargetProfits(gCtx, query.RoutingRuleID, startUTC, endUTC)
		return err
	})

	if err := g.Wait(); err != nil {
		return nil, err
	}

	merged := make(map[adaptiveTargetKey]*AdaptiveDashboardTargetStats, len(hopRows)+len(profitRows))
	getOrCreate := func(provider, model, keyID string) *AdaptiveDashboardTargetStats {
		k := adaptiveTargetKey{provider: provider, model: model, keyID: keyID}
		if existing, ok := merged[k]; ok {
			return existing
		}
		entry := &AdaptiveDashboardTargetStats{
			Provider:      provider,
			Model:         model,
			SelectedKeyID: keyID,
		}
		merged[k] = entry
		return entry
	}

	for _, hop := range hopRows {
		entry := getOrCreate(hop.Provider, hop.Model, hop.SelectedKeyID)
		entry.RequestCount = hop.RequestCount
		entry.SuccessCount = hop.SuccessCount
		entry.ErrorCount = hop.ErrorCount
		entry.AvgLatencyMs = hop.AvgLatencyMs
		entry.P50LatencyMs = hop.P50LatencyMs
		entry.P80LatencyMs = hop.P80LatencyMs
		entry.P95LatencyMs = hop.P95LatencyMs
	}

	for _, p := range profitRows {
		entry := getOrCreate(p.Provider, p.Model, p.SelectedKeyID)
		entry.RevenueUSD = p.RevenueUSD
		entry.CostUSD = p.CostUSD
		entry.ProfitUSD = p.ProfitUSD
		entry.PromptTokens = p.PromptTokens
		entry.CompletionTokens = p.CompletionTokens
		entry.TotalTokens = p.TotalTokens
		entry.MissingCostCount = p.MissingCostCount
		entry.MissingTokensCount = p.MissingTokensCount
		if p.RevenueUSD > 0 {
			m := p.ProfitUSD / p.RevenueUSD
			entry.GrossMargin = &m
		}
		result.TotalRevenueUSD += p.RevenueUSD
		result.TotalCostUSD += p.CostUSD
		result.TotalProfitUSD += p.ProfitUSD
		result.TotalPromptTokens += p.PromptTokens
		result.TotalCompletionTokens += p.CompletionTokens
		result.TotalTokens += p.TotalTokens
		result.MissingCostCount += p.MissingCostCount
		result.MissingTokensCount += p.MissingTokensCount
	}

	if result.TotalRevenueUSD > 0 {
		m := result.TotalProfitUSD / result.TotalRevenueUSD
		result.GrossMargin = &m
	}

	targets := make([]AdaptiveDashboardTargetStats, 0, len(merged))
	for _, entry := range merged {
		targets = append(targets, *entry)
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Provider != targets[j].Provider {
			return targets[i].Provider < targets[j].Provider
		}
		if targets[i].Model != targets[j].Model {
			return targets[i].Model < targets[j].Model
		}
		return targets[i].SelectedKeyID < targets[j].SelectedKeyID
	})

	result.RootSummary = rootSummary
	result.Targets = targets
	return result, nil
}

func (s *RDBLogStore) getAdaptiveRootSummaryPostgres(ctx context.Context, ruleID string, startUTC, endUTC time.Time) (AdaptiveDashboardRootSummary, error) {
	var row struct {
		RootRequestCount     int64           `gorm:"column:root_request_count"`
		RootSuccessCount     int64           `gorm:"column:root_success_count"`
		FirstHopSuccessCount int64           `gorm:"column:first_hop_success_count"`
		RescuedCount         int64           `gorm:"column:rescued_count"`
		AvgLatencyMs         sql.NullFloat64 `gorm:"column:avg_latency_ms"`
		P50LatencyMs         sql.NullFloat64 `gorm:"column:p50_latency_ms"`
		P80LatencyMs         sql.NullFloat64 `gorm:"column:p80_latency_ms"`
		P95LatencyMs         sql.NullFloat64 `gorm:"column:p95_latency_ms"`
	}

	rawSQL := `
		WITH root_groups AS (
			SELECT
				COALESCE(NULLIF(parent_request_id, ''), id) AS group_id,
				MAX(CASE WHEN status = 'success' THEN 1 ELSE 0 END) AS succeeded,
				MIN(CASE WHEN status = 'success' THEN fallback_index END) AS first_success_hop,
				SUM(CASE WHEN latency IS NOT NULL AND latency > 0 THEN latency ELSE 0 END) AS total_latency_ms
			FROM logs
			WHERE routing_rule_id = ?
				AND timestamp >= ?
				AND timestamp <= ?
				AND status IN ('success', 'error')
			GROUP BY COALESCE(NULLIF(parent_request_id, ''), id)
		)
		SELECT
			COUNT(*) AS root_request_count,
			COALESCE(SUM(succeeded), 0) AS root_success_count,
			COALESCE(SUM(CASE WHEN succeeded = 1 AND first_success_hop = 0 THEN 1 ELSE 0 END), 0) AS first_hop_success_count,
			COALESCE(SUM(CASE WHEN succeeded = 1 AND first_success_hop > 0 THEN 1 ELSE 0 END), 0) AS rescued_count,
			AVG(total_latency_ms) FILTER (WHERE succeeded = 1 AND total_latency_ms > 0) AS avg_latency_ms,
			percentile_cont(0.50) WITHIN GROUP (ORDER BY total_latency_ms) FILTER (WHERE succeeded = 1 AND total_latency_ms > 0) AS p50_latency_ms,
			percentile_cont(0.80) WITHIN GROUP (ORDER BY total_latency_ms) FILTER (WHERE succeeded = 1 AND total_latency_ms > 0) AS p80_latency_ms,
			percentile_cont(0.95) WITHIN GROUP (ORDER BY total_latency_ms) FILTER (WHERE succeeded = 1 AND total_latency_ms > 0) AS p95_latency_ms
		FROM root_groups
	`

	if err := s.db.WithContext(ctx).Raw(rawSQL, ruleID, startUTC, endUTC).Scan(&row).Error; err != nil {
		return AdaptiveDashboardRootSummary{}, err
	}

	summary := AdaptiveDashboardRootSummary{
		RootRequestCount:     row.RootRequestCount,
		RootSuccessCount:     row.RootSuccessCount,
		FirstHopSuccessCount: row.FirstHopSuccessCount,
		RescuedCount:         row.RescuedCount,
	}
	if row.RootRequestCount > 0 {
		sr := float64(row.RootSuccessCount) / float64(row.RootRequestCount) * 100
		fr := float64(row.FirstHopSuccessCount) / float64(row.RootRequestCount) * 100
		summary.SuccessRate = &sr
		summary.FirstHopSuccessRate = &fr
	}
	summary.AvgLatencyMs = nullFloat64Ptr(row.AvgLatencyMs)
	summary.P50LatencyMs = nullFloat64Ptr(row.P50LatencyMs)
	summary.P80LatencyMs = nullFloat64Ptr(row.P80LatencyMs)
	summary.P95LatencyMs = nullFloat64Ptr(row.P95LatencyMs)
	return summary, nil
}

func (s *RDBLogStore) getAdaptiveRootSummaryGeneric(ctx context.Context, ruleID string, startUTC, endUTC time.Time) (AdaptiveDashboardRootSummary, error) {
	var rows []struct {
		Succeeded       int           `gorm:"column:succeeded"`
		FirstSuccessHop sql.NullInt64 `gorm:"column:first_success_hop"`
		TotalLatencyMs  float64       `gorm:"column:total_latency_ms"`
	}

	rawSQL := `
		SELECT
			MAX(CASE WHEN status = 'success' THEN 1 ELSE 0 END) AS succeeded,
			MIN(CASE WHEN status = 'success' THEN fallback_index END) AS first_success_hop,
			SUM(CASE WHEN latency IS NOT NULL AND latency > 0 THEN latency ELSE 0 END) AS total_latency_ms
		FROM logs
		WHERE routing_rule_id = ?
			AND timestamp >= ?
			AND timestamp <= ?
			AND status IN ('success', 'error')
		GROUP BY COALESCE(NULLIF(parent_request_id, ''), id)
	`

	if err := s.db.WithContext(ctx).Raw(rawSQL, ruleID, startUTC, endUTC).Scan(&rows).Error; err != nil {
		return AdaptiveDashboardRootSummary{}, err
	}

	var summary AdaptiveDashboardRootSummary
	latencies := make([]float64, 0, len(rows))
	var latencySum float64
	for _, r := range rows {
		summary.RootRequestCount++
		if r.Succeeded == 1 {
			summary.RootSuccessCount++
			if r.FirstSuccessHop.Valid && r.FirstSuccessHop.Int64 == 0 {
				summary.FirstHopSuccessCount++
			} else {
				summary.RescuedCount++
			}
			if r.TotalLatencyMs > 0 {
				latencies = append(latencies, r.TotalLatencyMs)
				latencySum += r.TotalLatencyMs
			}
		}
	}

	if summary.RootRequestCount > 0 {
		sr := float64(summary.RootSuccessCount) / float64(summary.RootRequestCount) * 100
		fr := float64(summary.FirstHopSuccessCount) / float64(summary.RootRequestCount) * 100
		summary.SuccessRate = &sr
		summary.FirstHopSuccessRate = &fr
	}
	if len(latencies) > 0 {
		sort.Float64s(latencies)
		avg := latencySum / float64(len(latencies))
		p50 := computePercentile(latencies, 0.50)
		p80 := computePercentile(latencies, 0.80)
		p95 := computePercentile(latencies, 0.95)
		summary.AvgLatencyMs = &avg
		summary.P50LatencyMs = &p50
		summary.P80LatencyMs = &p80
		summary.P95LatencyMs = &p95
	}
	return summary, nil
}

func (s *RDBLogStore) getAdaptiveTargetHopsPostgres(ctx context.Context, ruleID string, startUTC, endUTC time.Time) ([]adaptiveTargetHopAgg, error) {
	var rows []struct {
		Provider      string          `gorm:"column:provider"`
		Model         string          `gorm:"column:model"`
		SelectedKeyID string          `gorm:"column:selected_key_id"`
		RequestCount  int64           `gorm:"column:request_count"`
		SuccessCount  int64           `gorm:"column:success_count"`
		ErrorCount    int64           `gorm:"column:error_count"`
		AvgLatencyMs  sql.NullFloat64 `gorm:"column:avg_latency_ms"`
		P50LatencyMs  sql.NullFloat64 `gorm:"column:p50_latency_ms"`
		P80LatencyMs  sql.NullFloat64 `gorm:"column:p80_latency_ms"`
		P95LatencyMs  sql.NullFloat64 `gorm:"column:p95_latency_ms"`
	}

	rawSQL := `
		SELECT
			provider,
			model,
			COALESCE(selected_key_id, '') AS selected_key_id,
			COUNT(*) AS request_count,
			COALESCE(SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END), 0) AS success_count,
			COALESCE(SUM(CASE WHEN status = 'error' THEN 1 ELSE 0 END), 0) AS error_count,
			AVG(latency) FILTER (WHERE status = 'success' AND latency IS NOT NULL AND latency > 0) AS avg_latency_ms,
			percentile_cont(0.50) WITHIN GROUP (ORDER BY latency) FILTER (WHERE status = 'success' AND latency IS NOT NULL AND latency > 0) AS p50_latency_ms,
			percentile_cont(0.80) WITHIN GROUP (ORDER BY latency) FILTER (WHERE status = 'success' AND latency IS NOT NULL AND latency > 0) AS p80_latency_ms,
			percentile_cont(0.95) WITHIN GROUP (ORDER BY latency) FILTER (WHERE status = 'success' AND latency IS NOT NULL AND latency > 0) AS p95_latency_ms
		FROM logs
		WHERE routing_rule_id = ?
			AND timestamp >= ?
			AND timestamp <= ?
			AND status IN ('success', 'error')
		GROUP BY provider, model, COALESCE(selected_key_id, '')
	`

	if err := s.db.WithContext(ctx).Raw(rawSQL, ruleID, startUTC, endUTC).Scan(&rows).Error; err != nil {
		return nil, err
	}

	out := make([]adaptiveTargetHopAgg, 0, len(rows))
	for _, r := range rows {
		out = append(out, adaptiveTargetHopAgg{
			Provider:      r.Provider,
			Model:         r.Model,
			SelectedKeyID: r.SelectedKeyID,
			RequestCount:  r.RequestCount,
			SuccessCount:  r.SuccessCount,
			ErrorCount:    r.ErrorCount,
			AvgLatencyMs:  nullFloat64Ptr(r.AvgLatencyMs),
			P50LatencyMs:  nullFloat64Ptr(r.P50LatencyMs),
			P80LatencyMs:  nullFloat64Ptr(r.P80LatencyMs),
			P95LatencyMs:  nullFloat64Ptr(r.P95LatencyMs),
		})
	}
	return out, nil
}

func (s *RDBLogStore) getAdaptiveTargetHopsGeneric(ctx context.Context, ruleID string, startUTC, endUTC time.Time) ([]adaptiveTargetHopAgg, error) {
	var countRows []struct {
		Provider      string `gorm:"column:provider"`
		Model         string `gorm:"column:model"`
		SelectedKeyID string `gorm:"column:selected_key_id"`
		RequestCount  int64  `gorm:"column:request_count"`
		SuccessCount  int64  `gorm:"column:success_count"`
		ErrorCount    int64  `gorm:"column:error_count"`
	}

	countSQL := `
		SELECT
			provider,
			model,
			COALESCE(selected_key_id, '') AS selected_key_id,
			COUNT(*) AS request_count,
			COALESCE(SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END), 0) AS success_count,
			COALESCE(SUM(CASE WHEN status = 'error' THEN 1 ELSE 0 END), 0) AS error_count
		FROM logs
		WHERE routing_rule_id = ?
			AND timestamp >= ?
			AND timestamp <= ?
			AND status IN ('success', 'error')
		GROUP BY provider, model, COALESCE(selected_key_id, '')
	`
	if err := s.db.WithContext(ctx).Raw(countSQL, ruleID, startUTC, endUTC).Scan(&countRows).Error; err != nil {
		return nil, err
	}

	var latencyRows []struct {
		Provider      string  `gorm:"column:provider"`
		Model         string  `gorm:"column:model"`
		SelectedKeyID string  `gorm:"column:selected_key_id"`
		Latency       float64 `gorm:"column:latency"`
	}

	latSQL := `
		SELECT
			provider,
			model,
			COALESCE(selected_key_id, '') AS selected_key_id,
			latency
		FROM logs
		WHERE routing_rule_id = ?
			AND timestamp >= ?
			AND timestamp <= ?
			AND status = 'success'
			AND latency IS NOT NULL
			AND latency > 0
	`
	if err := s.db.WithContext(ctx).Raw(latSQL, ruleID, startUTC, endUTC).Scan(&latencyRows).Error; err != nil {
		return nil, err
	}

	latMap := make(map[adaptiveTargetKey][]float64, len(countRows))
	for _, r := range latencyRows {
		k := adaptiveTargetKey{provider: r.Provider, model: r.Model, keyID: r.SelectedKeyID}
		latMap[k] = append(latMap[k], r.Latency)
	}

	out := make([]adaptiveTargetHopAgg, 0, len(countRows))
	for _, r := range countRows {
		item := adaptiveTargetHopAgg{
			Provider:      r.Provider,
			Model:         r.Model,
			SelectedKeyID: r.SelectedKeyID,
			RequestCount:  r.RequestCount,
			SuccessCount:  r.SuccessCount,
			ErrorCount:    r.ErrorCount,
		}
		k := adaptiveTargetKey{provider: r.Provider, model: r.Model, keyID: r.SelectedKeyID}
		if vals := latMap[k]; len(vals) > 0 {
			sort.Float64s(vals)
			var sum float64
			for _, v := range vals {
				sum += v
			}
			avg := sum / float64(len(vals))
			p50 := computePercentile(vals, 0.50)
			p80 := computePercentile(vals, 0.80)
			p95 := computePercentile(vals, 0.95)
			item.AvgLatencyMs = &avg
			item.P50LatencyMs = &p50
			item.P80LatencyMs = &p80
			item.P95LatencyMs = &p95
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *RDBLogStore) getAdaptiveTargetProfits(ctx context.Context, ruleID string, startUTC, endUTC time.Time) ([]adaptiveTargetProfitAgg, error) {
	var rows []adaptiveTargetProfitAgg
	err := s.db.WithContext(ctx).Model(&ProfitEvent{}).
		Select(`
			provider,
			model,
			COALESCE(selected_key_id, '') AS selected_key_id,
			COALESCE(SUM(revenue_usd), 0) AS revenue_usd,
			COALESCE(SUM(cost_usd), 0) AS cost_usd,
			COALESCE(SUM(profit_usd), 0) AS profit_usd,
			COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
			COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
			COALESCE(SUM(total_tokens), 0) AS total_tokens,
			COALESCE(SUM(CASE WHEN status = 'success' AND missing_cost THEN 1 ELSE 0 END), 0) AS missing_cost_count,
			COALESCE(SUM(CASE WHEN status = 'success' AND missing_tokens THEN 1 ELSE 0 END), 0) AS missing_tokens_count
		`).
		Where("routing_rule_id = ? AND timestamp >= ? AND timestamp <= ?", ruleID, startUTC, endUTC).
		Group("provider, model, COALESCE(selected_key_id, '')").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func nullFloat64Ptr(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	f := v.Float64
	return &f
}
