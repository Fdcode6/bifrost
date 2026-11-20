package logstore

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrInvalidExternalProfitEvent indicates a client-side validation failure when ingesting external profit events.
var ErrInvalidExternalProfitEvent = errors.New("invalid external profit event")

const (
	defaultProfitSettingsID           = "default"
	routingRuleProfitSettingsIDPrefix = "routing_rule:"
	defaultProfitTimezone             = "Asia/Shanghai"
	defaultSellInputPer1MUSD          = 2
	defaultSellOutputPer1MUSD         = 12
	defaultProfitBackfillPageSize     = 500
	ExternalProfitLogIDPrefix         = "ext:"
	maxExternalProfitEventsBatch      = 1000
	ProfitSourceAll                   = "all"
	ProfitSourceBifrost               = "bifrost"
	ProfitSourceExternal              = "external"
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

// RoutingRuleProfitSettings stores one routing-rule-specific selling price.
type RoutingRuleProfitSettings struct {
	RoutingRuleID      string    `json:"routing_rule_id"`
	ID                 string    `json:"id"`
	SellInputPer1MUSD  float64   `json:"sell_input_per_1m_usd"`
	SellOutputPer1MUSD float64   `json:"sell_output_per_1m_usd"`
	Timezone           string    `json:"timezone"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
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

// ProfitQuery defines an inclusive business-day range and optional source filter.
type ProfitQuery struct {
	StartDay string `json:"start_day,omitempty"`
	EndDay   string `json:"end_day,omitempty"`
	Source   string `json:"source,omitempty"`
}

// ExternalProfitEventInput represents one externally reported profit ledger entry.
// EventID must start with ExternalProfitLogIDPrefix ("ext:") to remain strictly isolated from native Bifrost logs.
type ExternalProfitEventInput struct {
	EventID            string    `json:"event_id"`
	Timestamp          time.Time `json:"timestamp"`
	Provider           string    `json:"provider"`
	Model              string    `json:"model"`
	SelectedKeyID      string    `json:"selected_key_id,omitempty"`
	SelectedKeyName    string    `json:"selected_key_name,omitempty"`
	VirtualKeyID       *string   `json:"virtual_key_id,omitempty"`
	VirtualKeyName     *string   `json:"virtual_key_name,omitempty"`
	RoutingRuleID      *string   `json:"routing_rule_id,omitempty"`
	RoutingRuleName    *string   `json:"routing_rule_name,omitempty"`
	Status             string    `json:"status,omitempty"`
	PromptTokens       int       `json:"prompt_tokens"`
	CompletionTokens   int       `json:"completion_tokens"`
	TotalTokens        int       `json:"total_tokens,omitempty"`
	CachedReadTokens   int       `json:"cached_read_tokens,omitempty"`
	CostUSD            float64   `json:"cost_usd"`
	RevenueUSD         float64   `json:"revenue_usd"`
	SellInputPer1MUSD  float64   `json:"sell_input_per_1m_usd,omitempty"`
	SellOutputPer1MUSD float64   `json:"sell_output_per_1m_usd,omitempty"`
}

// ExternalProfitIngestResult reports the outcome of one external profit batch upsert.
type ExternalProfitIngestResult struct {
	Received int `json:"received"`
	Upserted int `json:"upserted"`
}

// NormalizeProfitSource validates and normalizes a profit source filter.
func NormalizeProfitSource(source string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "", ProfitSourceAll:
		return ProfitSourceAll, nil
	case ProfitSourceBifrost:
		return ProfitSourceBifrost, nil
	case ProfitSourceExternal:
		return ProfitSourceExternal, nil
	default:
		return "", fmt.Errorf("invalid profit source %q: must be all, bifrost, or external", source)
	}
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

// ProfitBreakdownRow is one routing-rule/provider/model aggregate row for profit analysis.
type ProfitBreakdownRow struct {
	RoutingRuleID      *string  `json:"routing_rule_id,omitempty"`
	RoutingRuleName    *string  `json:"routing_rule_name,omitempty"`
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

func profitSettingsIDForRoutingRule(routingRuleID string) (string, error) {
	routingRuleID = strings.TrimSpace(routingRuleID)
	if routingRuleID == "" {
		return "", fmt.Errorf("routing rule id cannot be empty")
	}
	return routingRuleProfitSettingsIDPrefix + routingRuleID, nil
}

func routingRuleIDFromProfitSettingsID(id string) (string, bool) {
	if !strings.HasPrefix(id, routingRuleProfitSettingsIDPrefix) {
		return "", false
	}
	routingRuleID := strings.TrimPrefix(id, routingRuleProfitSettingsIDPrefix)
	return routingRuleID, routingRuleID != ""
}

func routingRuleProfitSettingsFromSettings(settings ProfitSettings) (RoutingRuleProfitSettings, bool) {
	routingRuleID, ok := routingRuleIDFromProfitSettingsID(settings.ID)
	if !ok {
		return RoutingRuleProfitSettings{}, false
	}
	return RoutingRuleProfitSettings{
		RoutingRuleID:      routingRuleID,
		ID:                 settings.ID,
		SellInputPer1MUSD:  settings.SellInputPer1MUSD,
		SellOutputPer1MUSD: settings.SellOutputPer1MUSD,
		Timezone:           settings.Timezone,
		CreatedAt:          settings.CreatedAt,
		UpdatedAt:          settings.UpdatedAt,
	}, true
}

func (s *RDBLogStore) getProfitSettingsByID(ctx context.Context, id string) (*ProfitSettings, error) {
	var settings ProfitSettings
	if err := s.db.WithContext(ctx).Where("id = ?", id).First(&settings).Error; err != nil {
		return nil, err
	}
	return &settings, nil
}

func (s *RDBLogStore) GetProfitSettings(ctx context.Context) (*ProfitSettings, error) {
	settings, err := s.getProfitSettingsByID(ctx, defaultProfitSettingsID)
	if err == nil {
		return settings, nil
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

func (s *RDBLogStore) GetRoutingRuleProfitSettings(ctx context.Context, routingRuleID string) (*ProfitSettings, bool, error) {
	id, err := profitSettingsIDForRoutingRule(routingRuleID)
	if err != nil {
		return nil, false, err
	}
	settings, err := s.getProfitSettingsByID(ctx, id)
	if err == nil {
		return settings, true, nil
	}
	if err == gorm.ErrRecordNotFound {
		return nil, false, nil
	}
	return nil, false, err
}

func (s *RDBLogStore) ListRoutingRuleProfitSettings(ctx context.Context) ([]RoutingRuleProfitSettings, error) {
	var settings []ProfitSettings
	if err := s.db.WithContext(ctx).
		Where("id <> ?", defaultProfitSettingsID).
		Order("id ASC").
		Find(&settings).Error; err != nil {
		return nil, err
	}
	result := make([]RoutingRuleProfitSettings, 0, len(settings))
	for _, item := range settings {
		if routingRuleSettings, ok := routingRuleProfitSettingsFromSettings(item); ok {
			result = append(result, routingRuleSettings)
		}
	}
	return result, nil
}

func (s *RDBLogStore) SaveRoutingRuleProfitSettings(ctx context.Context, routingRuleID string, settings *ProfitSettings) (*ProfitSettings, error) {
	id, err := profitSettingsIDForRoutingRule(routingRuleID)
	if err != nil {
		return nil, err
	}
	if settings == nil {
		return nil, fmt.Errorf("profit settings cannot be nil")
	}
	if settings.SellInputPer1MUSD < 0 || settings.SellOutputPer1MUSD < 0 {
		return nil, fmt.Errorf("profit sell prices cannot be negative")
	}
	if settings.SellInputPer1MUSD == 0 && settings.SellOutputPer1MUSD == 0 {
		return nil, fmt.Errorf("at least one profit sell price must be greater than zero")
	}
	timezone := settings.Timezone
	if timezone == "" {
		defaultSettings, err := s.GetProfitSettings(ctx)
		if err != nil {
			return nil, err
		}
		timezone = defaultSettings.Timezone
	}
	normalized, err := normalizeProfitSettings(&ProfitSettings{
		ID:                 id,
		SellInputPer1MUSD:  settings.SellInputPer1MUSD,
		SellOutputPer1MUSD: settings.SellOutputPer1MUSD,
		Timezone:           timezone,
	})
	if err != nil {
		return nil, err
	}
	normalized.ID = id
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"sell_input_per_1m_usd", "sell_output_per_1m_usd", "timezone", "updated_at"}),
	}).Create(normalized).Error; err != nil {
		return nil, err
	}
	return s.getProfitSettingsByID(ctx, id)
}

func (s *RDBLogStore) DeleteRoutingRuleProfitSettings(ctx context.Context, routingRuleID string) error {
	id, err := profitSettingsIDForRoutingRule(routingRuleID)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Where("id = ?", id).Delete(&ProfitSettings{}).Error
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

	settings, err := s.profitSettingsForLog(ctx, entry)
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

func (s *RDBLogStore) profitSettingsForLog(ctx context.Context, entry *Log) (*ProfitSettings, error) {
	if entry != nil && entry.RoutingRuleID != nil && strings.TrimSpace(*entry.RoutingRuleID) != "" {
		settings, found, err := s.GetRoutingRuleProfitSettings(ctx, *entry.RoutingRuleID)
		if err != nil {
			return nil, err
		}
		if found {
			return settings, nil
		}
	}
	return s.GetProfitSettings(ctx)
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
	missingCost := entry.Status == "success" && entry.Cost == nil
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

func trimOptionalStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func validateNonNegativeFiniteFloat(name string, value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return fmt.Errorf("%w: %s must be a non-negative finite number", ErrInvalidExternalProfitEvent, name)
	}
	return nil
}

// UpsertExternalProfitEvents validates and idempotently upserts externally reported profit events.
func (s *RDBLogStore) UpsertExternalProfitEvents(ctx context.Context, inputs []ExternalProfitEventInput) (*ExternalProfitIngestResult, error) {
	if len(inputs) == 0 {
		return &ExternalProfitIngestResult{Received: 0, Upserted: 0}, nil
	}
	if len(inputs) > maxExternalProfitEventsBatch {
		return nil, fmt.Errorf("%w: external profit batch size %d exceeds maximum %d", ErrInvalidExternalProfitEvent, len(inputs), maxExternalProfitEventsBatch)
	}

	timezone := defaultProfitTimezone
	if settings, err := s.GetProfitSettings(ctx); err == nil && settings != nil && strings.TrimSpace(settings.Timezone) != "" {
		timezone = settings.Timezone
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("invalid profit timezone %q: %w", timezone, err)
	}

	dedupIndex := make(map[string]int, len(inputs))
	events := make([]ProfitEvent, 0, len(inputs))

	for i, input := range inputs {
		eventID := strings.TrimSpace(input.EventID)
		if !strings.HasPrefix(eventID, ExternalProfitLogIDPrefix) || len(eventID) <= len(ExternalProfitLogIDPrefix) {
			return nil, fmt.Errorf("%w: events[%d].event_id must start with %q and include a non-empty suffix", ErrInvalidExternalProfitEvent, i, ExternalProfitLogIDPrefix)
		}
		if len(eventID) > 255 || strings.ContainsAny(eventID, " \t\r\n") {
			return nil, fmt.Errorf("%w: events[%d].event_id must be at most 255 characters with no whitespace", ErrInvalidExternalProfitEvent, i)
		}
		if input.Timestamp.IsZero() {
			return nil, fmt.Errorf("%w: events[%d].timestamp is required", ErrInvalidExternalProfitEvent, i)
		}
		provider := strings.TrimSpace(input.Provider)
		if provider == "" {
			return nil, fmt.Errorf("%w: events[%d].provider is required", ErrInvalidExternalProfitEvent, i)
		}
		model := strings.TrimSpace(input.Model)
		if model == "" {
			return nil, fmt.Errorf("%w: events[%d].model is required", ErrInvalidExternalProfitEvent, i)
		}
		status := strings.ToLower(strings.TrimSpace(input.Status))
		if status == "" {
			status = "success"
		}
		if status != "success" && status != "error" {
			return nil, fmt.Errorf("%w: events[%d].status must be success or error", ErrInvalidExternalProfitEvent, i)
		}
		if input.PromptTokens < 0 || input.CompletionTokens < 0 || input.TotalTokens < 0 || input.CachedReadTokens < 0 {
			return nil, fmt.Errorf("%w: events[%d] token counts cannot be negative", ErrInvalidExternalProfitEvent, i)
		}
		if err := validateNonNegativeFiniteFloat(fmt.Sprintf("events[%d].cost_usd", i), input.CostUSD); err != nil {
			return nil, err
		}
		if err := validateNonNegativeFiniteFloat(fmt.Sprintf("events[%d].revenue_usd", i), input.RevenueUSD); err != nil {
			return nil, err
		}
		if err := validateNonNegativeFiniteFloat(fmt.Sprintf("events[%d].sell_input_per_1m_usd", i), input.SellInputPer1MUSD); err != nil {
			return nil, err
		}
		if err := validateNonNegativeFiniteFloat(fmt.Sprintf("events[%d].sell_output_per_1m_usd", i), input.SellOutputPer1MUSD); err != nil {
			return nil, err
		}

		totalTokens := input.TotalTokens
		if totalTokens == 0 {
			totalTokens = input.PromptTokens + input.CompletionTokens
		}
		missingTokens := status == "success" && input.PromptTokens == 0 && input.CompletionTokens == 0 && totalTokens == 0

		event := ProfitEvent{
			LogID:              eventID,
			Timestamp:          input.Timestamp,
			BusinessDay:        input.Timestamp.In(loc).Format("2006-01-02"),
			Provider:           provider,
			Model:              model,
			SelectedKeyID:      strings.TrimSpace(input.SelectedKeyID),
			SelectedKeyName:    strings.TrimSpace(input.SelectedKeyName),
			VirtualKeyID:       trimOptionalStringPtr(input.VirtualKeyID),
			VirtualKeyName:     trimOptionalStringPtr(input.VirtualKeyName),
			RoutingRuleID:      trimOptionalStringPtr(input.RoutingRuleID),
			RoutingRuleName:    trimOptionalStringPtr(input.RoutingRuleName),
			Status:             status,
			PromptTokens:       input.PromptTokens,
			CompletionTokens:   input.CompletionTokens,
			TotalTokens:        totalTokens,
			CachedReadTokens:   input.CachedReadTokens,
			CostUSD:            input.CostUSD,
			RevenueUSD:         input.RevenueUSD,
			ProfitUSD:          input.RevenueUSD - input.CostUSD,
			SellInputPer1MUSD:  input.SellInputPer1MUSD,
			SellOutputPer1MUSD: input.SellOutputPer1MUSD,
			MissingCost:        false,
			MissingTokens:      missingTokens,
		}

		if idx, exists := dedupIndex[eventID]; exists {
			events[idx] = event
		} else {
			dedupIndex[eventID] = len(events)
			events = append(events, event)
		}
	}

	err = s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "log_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"timestamp",
			"business_day",
			"provider",
			"model",
			"selected_key_id",
			"selected_key_name",
			"virtual_key_id",
			"virtual_key_name",
			"routing_rule_id",
			"routing_rule_name",
			"status",
			"prompt_tokens",
			"completion_tokens",
			"total_tokens",
			"cached_read_tokens",
			"cost_usd",
			"revenue_usd",
			"profit_usd",
			"sell_input_per_1m_usd",
			"sell_output_per_1m_usd",
			"missing_cost",
			"missing_tokens",
			"updated_at",
		}),
	}).CreateInBatches(&events, 200).Error
	if err != nil {
		return nil, err
	}

	return &ExternalProfitIngestResult{
		Received: len(inputs),
		Upserted: len(events),
	}, nil
}

func applyProfitQuery(db *gorm.DB, query ProfitQuery) *gorm.DB {
	if query.StartDay != "" {
		db = db.Where("business_day >= ?", query.StartDay)
	}
	if query.EndDay != "" {
		db = db.Where("business_day <= ?", query.EndDay)
	}
	switch strings.ToLower(strings.TrimSpace(query.Source)) {
	case ProfitSourceExternal:
		db = db.Where("log_id LIKE ?", ExternalProfitLogIDPrefix+"%")
	case ProfitSourceBifrost:
		db = db.Where("log_id NOT LIKE ?", ExternalProfitLogIDPrefix+"%")
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
			COALESCE(SUM(CASE WHEN status = 'success' AND missing_cost THEN 1 ELSE 0 END), 0) AS missing_cost_count,
			COALESCE(SUM(CASE WHEN status = 'success' AND missing_tokens THEN 1 ELSE 0 END), 0) AS missing_tokens_count`)
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
			COALESCE(SUM(CASE WHEN status = 'success' AND missing_cost THEN 1 ELSE 0 END), 0) AS missing_cost_count,
			COALESCE(SUM(CASE WHEN status = 'success' AND missing_tokens THEN 1 ELSE 0 END), 0) AS missing_tokens_count`).
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
			routing_rule_id,
			routing_rule_name,
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
			COALESCE(SUM(CASE WHEN status = 'success' AND missing_cost THEN 1 ELSE 0 END), 0) AS missing_cost_count,
			COALESCE(SUM(CASE WHEN status = 'success' AND missing_tokens THEN 1 ELSE 0 END), 0) AS missing_tokens_count`).
		Group("routing_rule_id, routing_rule_name, provider, model").
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

func (s *RDBLogStore) staleOrMissingProfitLogsQuery(ctx context.Context) *gorm.DB {
	return s.db.WithContext(ctx).Model(&Log{}).
		Joins("LEFT JOIN profit_events ON profit_events.log_id = logs.id").
		Where(
			`logs.status <> ? AND (
				profit_events.log_id IS NULL
				OR profit_events.status <> logs.status
				OR (logs.status = ? AND logs.cost IS NOT NULL AND profit_events.missing_cost = ?)
				OR (logs.status = ? AND (logs.prompt_tokens > 0 OR logs.completion_tokens > 0) AND profit_events.missing_tokens = ?)
			)`,
			"processing", "success", true, "success", true,
		)
}

func (s *RDBLogStore) BackfillProfitEvents(ctx context.Context, limit int) (*ProfitBackfillResult, error) {
	if limit <= 0 {
		limit = defaultProfitBackfillPageSize
	}
	var logs []Log
	if err := s.staleOrMissingProfitLogsQuery(ctx).
		Select("logs.*").
		Order("CASE WHEN profit_events.log_id IS NULL THEN 0 ELSE 1 END ASC, logs.timestamp ASC").
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
	if err := s.staleOrMissingProfitLogsQuery(ctx).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}
