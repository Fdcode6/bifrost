package logstore

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	defaultProfitSettingsID       = "default"
	defaultProfitTimezone         = "Asia/Shanghai"
	defaultSellInputPer1MUSD      = 2
	defaultSellOutputPer1MUSD     = 12
	defaultProfitBackfillPageSize = 500
)

// ProfitSettings stores the current selling price used for future profit events.
type ProfitSettings struct {
	ID                 string    `gorm:"primaryKey;type:varchar(64)" json:"id"`
	SellInputPer1MUSD  float64   `gorm:"column:sell_input_per_1m_usd;not null" json:"sell_input_per_1m_usd"`
	SellOutputPer1MUSD float64   `gorm:"column:sell_output_per_1m_usd;not null" json:"sell_output_per_1m_usd"`
	Timezone           string    `gorm:"type:varchar(64);not null" json:"timezone"`
	CreatedAt          time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt          time.Time `gorm:"not null" json:"updated_at"`
}

// ProfitEvent stores one immutable-price profit ledger entry for one LLM log.
type ProfitEvent struct {
	LogID              string    `gorm:"primaryKey;type:varchar(255)" json:"log_id"`
	Timestamp          time.Time `gorm:"index;not null" json:"timestamp"`
	BusinessDay        string    `gorm:"type:varchar(10);index;not null" json:"business_day"`
	Provider           string    `gorm:"type:varchar(255);index:idx_profit_provider_model,priority:1;not null" json:"provider"`
	Model              string    `gorm:"type:varchar(255);index:idx_profit_provider_model,priority:2;not null" json:"model"`
	SelectedKeyID      string    `gorm:"type:varchar(255);index" json:"selected_key_id"`
	SelectedKeyName    string    `gorm:"type:varchar(255)" json:"selected_key_name"`
	VirtualKeyID       *string   `gorm:"type:varchar(255);index" json:"virtual_key_id,omitempty"`
	VirtualKeyName     *string   `gorm:"type:varchar(255)" json:"virtual_key_name,omitempty"`
	RoutingRuleID      *string   `gorm:"type:varchar(255);index" json:"routing_rule_id,omitempty"`
	RoutingRuleName    *string   `gorm:"type:varchar(255)" json:"routing_rule_name,omitempty"`
	Status             string    `gorm:"type:varchar(50);index;not null" json:"status"`
	PromptTokens       int       `gorm:"default:0" json:"prompt_tokens"`
	CompletionTokens   int       `gorm:"default:0" json:"completion_tokens"`
	TotalTokens        int       `gorm:"default:0" json:"total_tokens"`
	CachedReadTokens   int       `gorm:"default:0" json:"cached_read_tokens"`
	CostUSD            float64   `gorm:"default:0" json:"cost_usd"`
	RevenueUSD         float64   `gorm:"default:0" json:"revenue_usd"`
	ProfitUSD          float64   `gorm:"default:0" json:"profit_usd"`
	SellInputPer1MUSD  float64   `gorm:"column:sell_input_per_1m_usd;not null" json:"sell_input_per_1m_usd"`
	SellOutputPer1MUSD float64   `gorm:"column:sell_output_per_1m_usd;not null" json:"sell_output_per_1m_usd"`
	MissingCost        bool      `gorm:"default:false" json:"missing_cost"`
	MissingTokens      bool      `gorm:"default:false" json:"missing_tokens"`
	CreatedAt          time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt          time.Time `gorm:"not null" json:"updated_at"`
}

// ProfitQuery defines an inclusive business-day range.
type ProfitQuery struct {
	StartDay string `json:"start_day,omitempty"`
	EndDay   string `json:"end_day,omitempty"`
}

// ProfitSummary is the aggregate result for a profit query.
type ProfitSummary struct {
	RevenueUSD         float64  `json:"revenue_usd"`
	CostUSD            float64  `json:"cost_usd"`
	ProfitUSD          float64  `json:"profit_usd"`
	GrossMargin        *float64 `json:"gross_margin,omitempty"`
	RequestCount       int64    `json:"request_count"`
	SuccessCount       int64    `json:"success_count"`
	ErrorCount         int64    `json:"error_count"`
	PromptTokens       int64    `json:"prompt_tokens"`
	CompletionTokens   int64    `json:"completion_tokens"`
	TotalTokens        int64    `json:"total_tokens"`
	MissingCostCount   int64    `json:"missing_cost_count"`
	MissingTokensCount int64    `json:"missing_tokens_count"`
}

// ProfitDailyBucket is one business-day aggregate for charts and tables.
type ProfitDailyBucket struct {
	BusinessDay        string   `json:"business_day"`
	RevenueUSD         float64  `json:"revenue_usd"`
	CostUSD            float64  `json:"cost_usd"`
	ProfitUSD          float64  `json:"profit_usd"`
	GrossMargin        *float64 `json:"gross_margin,omitempty"`
	RequestCount       int64    `json:"request_count"`
	SuccessCount       int64    `json:"success_count"`
	ErrorCount         int64    `json:"error_count"`
	PromptTokens       int64    `json:"prompt_tokens"`
	CompletionTokens   int64    `json:"completion_tokens"`
	TotalTokens        int64    `json:"total_tokens"`
	MissingCostCount   int64    `json:"missing_cost_count"`
	MissingTokensCount int64    `json:"missing_tokens_count"`
}

// ProfitBreakdownRow is one provider/model aggregate row for profit analysis.
type ProfitBreakdownRow struct {
	Provider           string   `json:"provider"`
	Model              string   `json:"model"`
	RevenueUSD         float64  `json:"revenue_usd"`
	CostUSD            float64  `json:"cost_usd"`
	ProfitUSD          float64  `json:"profit_usd"`
	GrossMargin        *float64 `json:"gross_margin,omitempty"`
	RequestCount       int64    `json:"request_count"`
	SuccessCount       int64    `json:"success_count"`
	ErrorCount         int64    `json:"error_count"`
	PromptTokens       int64    `json:"prompt_tokens"`
	CompletionTokens   int64    `json:"completion_tokens"`
	TotalTokens        int64    `json:"total_tokens"`
	MissingCostCount   int64    `json:"missing_cost_count"`
	MissingTokensCount int64    `json:"missing_tokens_count"`
}

// ProfitBackfillResult reports a bounded backfill run.
type ProfitBackfillResult struct {
	Processed int `json:"processed"`
	Created   int `json:"created"`
	Updated   int `json:"updated"`
	Skipped   int `json:"skipped"`
}

// ProfitReconciliationStatus reports profit ledger completeness and background repair state.
type ProfitReconciliationStatus struct {
	MissingEventCount int64                 `json:"missing_event_count"`
	LastRunAt         *time.Time            `json:"last_run_at,omitempty"`
	NextRunAt         *time.Time            `json:"next_run_at,omitempty"`
	IntervalSeconds   int64                 `json:"interval_seconds"`
	BatchLimit        int                   `json:"batch_limit"`
	LastResult        *ProfitBackfillResult `json:"last_result,omitempty"`
	LastError         string                `json:"last_error,omitempty"`
}

func normalizeProfitSettings(settings *ProfitSettings) (*ProfitSettings, error) {
	if settings == nil {
		settings = &ProfitSettings{}
	}
	if settings.ID == "" {
		settings.ID = defaultProfitSettingsID
	}
	if settings.Timezone == "" {
		settings.Timezone = defaultProfitTimezone
	}
	if settings.SellInputPer1MUSD == 0 && settings.SellOutputPer1MUSD == 0 {
		settings.SellInputPer1MUSD = defaultSellInputPer1MUSD
		settings.SellOutputPer1MUSD = defaultSellOutputPer1MUSD
	}
	if settings.SellInputPer1MUSD < 0 || settings.SellOutputPer1MUSD < 0 {
		return nil, fmt.Errorf("profit sell prices cannot be negative")
	}
	if settings.SellInputPer1MUSD == 0 && settings.SellOutputPer1MUSD == 0 {
		return nil, fmt.Errorf("at least one profit sell price must be greater than zero")
	}
	if _, err := time.LoadLocation(settings.Timezone); err != nil {
		return nil, fmt.Errorf("invalid profit timezone %q: %w", settings.Timezone, err)
	}
	return settings, nil
}

func (s *RDBLogStore) GetProfitSettings(ctx context.Context) (*ProfitSettings, error) {
	var settings ProfitSettings
	err := s.db.WithContext(ctx).Where("id = ?", defaultProfitSettingsID).First(&settings).Error
	if err == nil {
		return &settings, nil
	}
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}
	return s.SaveProfitSettings(ctx, &ProfitSettings{})
}

func (s *RDBLogStore) SaveProfitSettings(ctx context.Context, settings *ProfitSettings) (*ProfitSettings, error) {
	normalized, err := normalizeProfitSettings(settings)
	if err != nil {
		return nil, err
	}
	normalized.ID = defaultProfitSettingsID
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"sell_input_per_1m_usd", "sell_output_per_1m_usd", "timezone", "updated_at"}),
	}).Create(normalized).Error; err != nil {
		return nil, err
	}
	return s.GetProfitSettings(ctx)
}

func (s *RDBLogStore) FindProfitEvent(ctx context.Context, logID string) (*ProfitEvent, error) {
	var event ProfitEvent
	if err := s.db.WithContext(ctx).Where("log_id = ?", logID).First(&event).Error; err != nil {
		return nil, err
	}
	return &event, nil
}

func (s *RDBLogStore) UpsertProfitEventFromLog(ctx context.Context, entry *Log) (*ProfitEvent, error) {
	if entry == nil {
		return nil, fmt.Errorf("log entry cannot be nil")
	}
	if entry.Status == "processing" {
		return nil, nil
	}

	settings, err := s.GetProfitSettings(ctx)
	if err != nil {
		return nil, err
	}

	var existing ProfitEvent
	existingErr := s.db.WithContext(ctx).Where("log_id = ?", entry.ID).First(&existing).Error
	if existingErr != nil && existingErr != gorm.ErrRecordNotFound {
		return nil, existingErr
	}
	if existingErr == nil {
		settings = &ProfitSettings{
			ID:                 defaultProfitSettingsID,
			SellInputPer1MUSD:  existing.SellInputPer1MUSD,
			SellOutputPer1MUSD: existing.SellOutputPer1MUSD,
			Timezone:           settings.Timezone,
		}
	}

	event, err := profitEventFromLog(entry, settings)
	if err != nil {
		return nil, err
	}
	if event == nil {
		return nil, nil
	}
	if existingErr == nil {
		event.CreatedAt = existing.CreatedAt
	}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "log_id"}},
		UpdateAll: true,
	}).Create(event).Error; err != nil {
		return nil, err
	}
	return s.FindProfitEvent(ctx, entry.ID)
}

func profitEventFromLog(entry *Log, settings *ProfitSettings) (*ProfitEvent, error) {
	settings, err := normalizeProfitSettings(settings)
	if err != nil {
		return nil, err
	}
	if entry.Status == "processing" {
		return nil, nil
	}
	loc, err := time.LoadLocation(settings.Timezone)
	if err != nil {
		return nil, err
	}
	businessDay := entry.Timestamp.In(loc).Format("2006-01-02")
	cost := 0.0
	missingCost := entry.Cost == nil
	if entry.Cost != nil {
		cost = *entry.Cost
	}
	revenue := 0.0
	missingTokens := false
	if entry.Status == "success" {
		missingTokens = entry.PromptTokens == 0 && entry.CompletionTokens == 0
		revenue = float64(entry.PromptTokens)*settings.SellInputPer1MUSD/1_000_000 +
			float64(entry.CompletionTokens)*settings.SellOutputPer1MUSD/1_000_000
	}
	return &ProfitEvent{
		LogID:              entry.ID,
		Timestamp:          entry.Timestamp,
		BusinessDay:        businessDay,
		Provider:           entry.Provider,
		Model:              entry.Model,
		SelectedKeyID:      entry.SelectedKeyID,
		SelectedKeyName:    entry.SelectedKeyName,
		VirtualKeyID:       entry.VirtualKeyID,
		VirtualKeyName:     entry.VirtualKeyName,
		RoutingRuleID:      entry.RoutingRuleID,
		RoutingRuleName:    entry.RoutingRuleName,
		Status:             entry.Status,
		PromptTokens:       entry.PromptTokens,
		CompletionTokens:   entry.CompletionTokens,
		TotalTokens:        entry.TotalTokens,
		CachedReadTokens:   entry.CachedReadTokens,
		CostUSD:            cost,
		RevenueUSD:         revenue,
		ProfitUSD:          revenue - cost,
		SellInputPer1MUSD:  settings.SellInputPer1MUSD,
		SellOutputPer1MUSD: settings.SellOutputPer1MUSD,
		MissingCost:        missingCost,
		MissingTokens:      missingTokens,
	}, nil
}

func applyProfitQuery(db *gorm.DB, query ProfitQuery) *gorm.DB {
	if query.StartDay != "" {
		db = db.Where("business_day >= ?", query.StartDay)
	}
	if query.EndDay != "" {
		db = db.Where("business_day <= ?", query.EndDay)
	}
	return db
}

func (s *RDBLogStore) GetProfitSummary(ctx context.Context, query ProfitQuery) (*ProfitSummary, error) {
	var row struct {
		RevenueUSD         float64
		CostUSD            float64
		ProfitUSD          float64
		RequestCount       int64
		SuccessCount       int64
		ErrorCount         int64
		PromptTokens       int64
		CompletionTokens   int64
		TotalTokens        int64
		MissingCostCount   int64
		MissingTokensCount int64
	}
	db := s.db.WithContext(ctx).Model(&ProfitEvent{}).
		Select(`
			COALESCE(SUM(revenue_usd), 0) AS revenue_usd,
			COALESCE(SUM(cost_usd), 0) AS cost_usd,
			COALESCE(SUM(profit_usd), 0) AS profit_usd,
			COUNT(*) AS request_count,
			COALESCE(SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END), 0) AS success_count,
			COALESCE(SUM(CASE WHEN status = 'error' THEN 1 ELSE 0 END), 0) AS error_count,
			COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
			COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
			COALESCE(SUM(total_tokens), 0) AS total_tokens,
			COALESCE(SUM(CASE WHEN missing_cost THEN 1 ELSE 0 END), 0) AS missing_cost_count,
			COALESCE(SUM(CASE WHEN missing_tokens THEN 1 ELSE 0 END), 0) AS missing_tokens_count`)
	if err := applyProfitQuery(db, query).Scan(&row).Error; err != nil {
		return nil, err
	}
	summary := &ProfitSummary{
		RevenueUSD:         row.RevenueUSD,
		CostUSD:            row.CostUSD,
		ProfitUSD:          row.ProfitUSD,
		RequestCount:       row.RequestCount,
		SuccessCount:       row.SuccessCount,
		ErrorCount:         row.ErrorCount,
		PromptTokens:       row.PromptTokens,
		CompletionTokens:   row.CompletionTokens,
		TotalTokens:        row.TotalTokens,
		MissingCostCount:   row.MissingCostCount,
		MissingTokensCount: row.MissingTokensCount,
	}
	if summary.RevenueUSD > 0 {
		margin := summary.ProfitUSD / summary.RevenueUSD
		summary.GrossMargin = &margin
	}
	return summary, nil
}

func (s *RDBLogStore) GetProfitDaily(ctx context.Context, query ProfitQuery) ([]ProfitDailyBucket, error) {
	var rows []ProfitDailyBucket
	db := s.db.WithContext(ctx).Model(&ProfitEvent{}).
		Select(`
			business_day,
			COALESCE(SUM(revenue_usd), 0) AS revenue_usd,
			COALESCE(SUM(cost_usd), 0) AS cost_usd,
			COALESCE(SUM(profit_usd), 0) AS profit_usd,
			COUNT(*) AS request_count,
			COALESCE(SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END), 0) AS success_count,
			COALESCE(SUM(CASE WHEN status = 'error' THEN 1 ELSE 0 END), 0) AS error_count,
			COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
			COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
			COALESCE(SUM(total_tokens), 0) AS total_tokens,
			COALESCE(SUM(CASE WHEN missing_cost THEN 1 ELSE 0 END), 0) AS missing_cost_count,
			COALESCE(SUM(CASE WHEN missing_tokens THEN 1 ELSE 0 END), 0) AS missing_tokens_count`).
		Group("business_day").
		Order("business_day DESC")
	if err := applyProfitQuery(db, query).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for i := range rows {
		if rows[i].RevenueUSD > 0 {
			margin := rows[i].ProfitUSD / rows[i].RevenueUSD
			rows[i].GrossMargin = &margin
		}
	}
	return rows, nil
}

func (s *RDBLogStore) GetProfitBreakdown(ctx context.Context, query ProfitQuery) ([]ProfitBreakdownRow, error) {
	var rows []ProfitBreakdownRow
	db := s.db.WithContext(ctx).Model(&ProfitEvent{}).
		Select(`
			provider,
			model,
			COALESCE(SUM(revenue_usd), 0) AS revenue_usd,
			COALESCE(SUM(cost_usd), 0) AS cost_usd,
			COALESCE(SUM(profit_usd), 0) AS profit_usd,
			COUNT(*) AS request_count,
			COALESCE(SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END), 0) AS success_count,
			COALESCE(SUM(CASE WHEN status = 'error' THEN 1 ELSE 0 END), 0) AS error_count,
			COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
			COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
			COALESCE(SUM(total_tokens), 0) AS total_tokens,
			COALESCE(SUM(CASE WHEN missing_cost THEN 1 ELSE 0 END), 0) AS missing_cost_count,
			COALESCE(SUM(CASE WHEN missing_tokens THEN 1 ELSE 0 END), 0) AS missing_tokens_count`).
		Group("provider, model").
		Order("revenue_usd DESC")
	if err := applyProfitQuery(db, query).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for i := range rows {
		if rows[i].RevenueUSD > 0 {
			margin := rows[i].ProfitUSD / rows[i].RevenueUSD
			rows[i].GrossMargin = &margin
		}
	}
	return rows, nil
}

func (s *RDBLogStore) BackfillProfitEvents(ctx context.Context, limit int) (*ProfitBackfillResult, error) {
	if limit <= 0 {
		limit = defaultProfitBackfillPageSize
	}
	var logs []Log
	if err := s.db.WithContext(ctx).Model(&Log{}).
		Select("logs.*").
		Joins("LEFT JOIN profit_events ON profit_events.log_id = logs.id").
		Where("logs.status <> ? AND profit_events.log_id IS NULL", "processing").
		Order("logs.timestamp ASC").
		Limit(limit).
		Find(&logs).Error; err != nil {
		return nil, err
	}
	result := &ProfitBackfillResult{}
	for i := range logs {
		result.Processed++
		existed := true
		if err := s.db.WithContext(ctx).Select("log_id").Where("log_id = ?", logs[i].ID).First(&ProfitEvent{}).Error; err != nil {
			if err != gorm.ErrRecordNotFound {
				return nil, err
			}
			existed = false
		}
		event, err := s.UpsertProfitEventFromLog(ctx, &logs[i])
		if err != nil {
			return nil, err
		}
		if event == nil {
			result.Skipped++
			continue
		}
		if existed {
			result.Updated++
		} else {
			result.Created++
		}
	}
	return result, nil
}

func (s *RDBLogStore) CountMissingProfitEvents(ctx context.Context) (int64, error) {
	var count int64
	if err := s.db.WithContext(ctx).Model(&Log{}).
		Joins("LEFT JOIN profit_events ON profit_events.log_id = logs.id").
		Where("logs.status <> ? AND profit_events.log_id IS NULL", "processing").
		Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}
