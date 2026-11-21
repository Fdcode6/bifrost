package logstore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	defaultRelayBalanceSettingsID = "default"

	RelayBalanceKindPoloUsageToken = "polo_usage_token"
	RelayBalanceKindBAIV1Balance   = "bai_v1_balance"
	RelayBalanceKindNewAPIUserSelf = "new_api_user_self"

	RelayBalanceStatusOK           = "ok"
	RelayBalanceStatusLow          = "low"
	RelayBalanceStatusUrgent       = "urgent"
	RelayBalanceStatusUnconfigured = "unconfigured"
	RelayBalanceStatusError        = "error"
)

// RelayBalanceSettingsRow persists the JSON-serialized relay balance configuration.
type RelayBalanceSettingsRow struct {
	ID         string    `gorm:"primaryKey;type:varchar(64)" json:"id"`
	ConfigJSON string    `gorm:"type:text;not null" json:"config_json"`
	CreatedAt  time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt  time.Time `gorm:"not null" json:"updated_at"`
}

func (RelayBalanceSettingsRow) TableName() string {
	return "relay_balance_settings"
}

// RelayBalanceSiteConfig describes how one upstream relay site's balance is queried and converted to CNY.
type RelayBalanceSiteConfig struct {
	ID                 string  `json:"id"`
	Name               string  `json:"name"`
	Kind               string  `json:"kind"`
	BaseURL            string  `json:"base_url"`
	RechargeURL        string  `json:"recharge_url,omitempty"`
	ProviderPrefix     string  `json:"provider_prefix"`
	QuotaPerUnit       float64 `json:"quota_per_unit"`
	CNYPerSiteUnit     float64 `json:"cny_per_site_unit"`
	ThresholdCNY       float64 `json:"threshold_cny"`
	UrgentThresholdCNY float64 `json:"urgent_threshold_cny"`
	Enabled            bool    `json:"enabled"`
	UserID             string  `json:"user_id,omitempty"`
	AccessToken        string  `json:"access_token,omitempty"`
}

// RelayBalanceConfig controls background polling, thresholds, and Bark low-balance alerts.
type RelayBalanceConfig struct {
	Enabled             bool                     `json:"enabled"`
	PollIntervalSeconds int                      `json:"poll_interval_seconds"`
	Timezone            string                   `json:"timezone"`
	BarkURL             string                   `json:"bark_url,omitempty"`
	BarkGroup           string                   `json:"bark_group"`
	BarkEnabled         bool                     `json:"bark_enabled"`
	CooldownHours       int                      `json:"cooldown_hours"`
	UrgentCooldownHours int                      `json:"urgent_cooldown_hours"`
	QuietHoursEnabled   bool                     `json:"quiet_hours_enabled"`
	QuietStartHour      int                      `json:"quiet_start_hour"`
	QuietEndHour        int                      `json:"quiet_end_hour"`
	Sites               []RelayBalanceSiteConfig `json:"sites"`
}

// RelayBalanceState persists the latest balance snapshot and anti-spam alert state per site.
type RelayBalanceState struct {
	SiteID              string     `gorm:"column:site_id;primaryKey;type:varchar(64)" json:"site_id"`
	Status              string     `gorm:"column:status;type:varchar(32);not null" json:"status"`
	SiteBalanceUSD      *float64   `gorm:"column:site_balance_usd" json:"site_balance_usd,omitempty"`
	BalanceCNY          *float64   `gorm:"column:balance_cny" json:"balance_cny,omitempty"`
	SiteUsedUSD         *float64   `gorm:"column:site_used_usd" json:"site_used_usd,omitempty"`
	Spend24hCNY         float64    `gorm:"column:spend_24h_cny;default:0" json:"spend_24h_cny"`
	EstimatedDaysLeft   *float64   `gorm:"column:estimated_days_left" json:"estimated_days_left,omitempty"`
	KeySource           string     `gorm:"column:key_source;type:varchar(255)" json:"key_source"`
	LastCheckedAt       *time.Time `gorm:"column:last_checked_at" json:"last_checked_at,omitempty"`
	LastSuccessAt       *time.Time `gorm:"column:last_success_at" json:"last_success_at,omitempty"`
	LastError           string     `gorm:"column:last_error;type:text" json:"last_error,omitempty"`
	ConsecutiveErrors   int        `gorm:"column:consecutive_errors;default:0" json:"consecutive_errors"`
	AlertActive         bool       `gorm:"column:alert_active;default:false" json:"alert_active"`
	LastAlertTier       string     `gorm:"column:last_alert_tier;type:varchar(32)" json:"last_alert_tier,omitempty"`
	LastAlertAt         *time.Time `gorm:"column:last_alert_at" json:"last_alert_at,omitempty"`
	LastAlertBalanceCNY *float64   `gorm:"column:last_alert_balance_cny" json:"last_alert_balance_cny,omitempty"`
	PendingAfterQuiet   bool       `gorm:"column:pending_after_quiet;default:false" json:"pending_after_quiet"`
	CreatedAt           time.Time  `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt           time.Time  `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (RelayBalanceState) TableName() string {
	return "relay_balance_states"
}

// DefaultRelayBalanceConfig returns the pre-configured 4-site defaults with real recharge CNY ratios.
func DefaultRelayBalanceConfig() *RelayBalanceConfig {
	return &RelayBalanceConfig{
		Enabled:             true,
		PollIntervalSeconds: 600,
		Timezone:            defaultProfitTimezone,
		BarkGroup:           "bifrost-balance",
		BarkEnabled:         true,
		CooldownHours:       6,
		UrgentCooldownHours: 2,
		QuietHoursEnabled:   true,
		QuietStartHour:      23,
		QuietEndHour:        8,
		Sites: []RelayBalanceSiteConfig{
			{
				ID:                 "storeapi",
				Name:               "StoreAPI",
				Kind:               RelayBalanceKindNewAPIUserSelf,
				BaseURL:            "https://www.storeapi.one",
				RechargeURL:        "https://www.storeapi.one/wallet",
				ProviderPrefix:     "storeapi-",
				QuotaPerUnit:       500000,
				CNYPerSiteUnit:     1.0,
				ThresholdCNY:       50,
				UrgentThresholdCNY: 15,
				Enabled:            true,
			},
			{
				ID:                 "openlux",
				Name:               "OpenLux（云雾）",
				Kind:               RelayBalanceKindNewAPIUserSelf,
				BaseURL:            "https://api.openlux.ai",
				RechargeURL:        "https://www.openlux.ai/console/topup",
				ProviderPrefix:     "openlux-",
				QuotaPerUnit:       500000,
				CNYPerSiteUnit:     6.664,
				ThresholdCNY:       50,
				UrgentThresholdCNY: 15,
				Enabled:            true,
			},
			{
				ID:                 "polo",
				Name:               "PoloAPI",
				Kind:               RelayBalanceKindPoloUsageToken,
				BaseURL:            "https://polo.zdfan.com",
				RechargeURL:        "https://poloai.top/wallet",
				ProviderPrefix:     "polo-",
				QuotaPerUnit:       500000,
				CNYPerSiteUnit:     1.0,
				ThresholdCNY:       50,
				UrgentThresholdCNY: 15,
				Enabled:            true,
			},
			{
				ID:                 "bai",
				Name:               "B.AI",
				Kind:               RelayBalanceKindBAIV1Balance,
				BaseURL:            "https://bai.zdfan.com",
				RechargeURL:        "https://chat.b.ai/purchase",
				ProviderPrefix:     "bai-",
				QuotaPerUnit:       1000000,
				CNYPerSiteUnit:     6.8,
				ThresholdCNY:       50,
				UrgentThresholdCNY: 15,
				Enabled:            true,
			},
		},
	}
}

func NormalizeRelayBalanceConfig(cfg *RelayBalanceConfig) (*RelayBalanceConfig, error) {
	defaults := DefaultRelayBalanceConfig()
	if cfg == nil {
		return defaults, nil
	}
	out := *cfg
	if out.PollIntervalSeconds <= 0 {
		out.PollIntervalSeconds = defaults.PollIntervalSeconds
	}
	if out.PollIntervalSeconds < 60 {
		out.PollIntervalSeconds = 60
	}
	if out.PollIntervalSeconds > 86400 {
		out.PollIntervalSeconds = 86400
	}
	out.Timezone = strings.TrimSpace(out.Timezone)
	if out.Timezone == "" {
		out.Timezone = defaultProfitTimezone
	}
	if _, err := time.LoadLocation(out.Timezone); err != nil {
		return nil, fmt.Errorf("invalid timezone %q: %w", out.Timezone, err)
	}
	out.BarkURL = strings.TrimRight(strings.TrimSpace(out.BarkURL), "/")
	out.BarkGroup = strings.TrimSpace(out.BarkGroup)
	if out.BarkGroup == "" {
		out.BarkGroup = defaults.BarkGroup
	}
	if out.CooldownHours <= 0 {
		out.CooldownHours = defaults.CooldownHours
	}
	if out.CooldownHours > 168 {
		out.CooldownHours = 168
	}
	if out.UrgentCooldownHours <= 0 {
		out.UrgentCooldownHours = defaults.UrgentCooldownHours
	}
	if out.UrgentCooldownHours > out.CooldownHours {
		out.UrgentCooldownHours = out.CooldownHours
	}
	if out.QuietStartHour < 0 || out.QuietStartHour > 23 {
		out.QuietStartHour = defaults.QuietStartHour
	}
	if out.QuietEndHour < 0 || out.QuietEndHour > 23 {
		out.QuietEndHour = defaults.QuietEndHour
	}

	if len(out.Sites) == 0 {
		out.Sites = defaults.Sites
	} else {
		defaultByID := make(map[string]RelayBalanceSiteConfig, len(defaults.Sites))
		for _, s := range defaults.Sites {
			defaultByID[s.ID] = s
		}
		seen := make(map[string]struct{}, len(out.Sites))
		normalizedSites := make([]RelayBalanceSiteConfig, 0, len(out.Sites))
		for i, site := range out.Sites {
			site.ID = strings.ToLower(strings.TrimSpace(site.ID))
			if site.ID == "" {
				return nil, fmt.Errorf("sites[%d].id is required", i)
			}
			if _, dup := seen[site.ID]; dup {
				return nil, fmt.Errorf("duplicate site id %q", site.ID)
			}
			seen[site.ID] = struct{}{}
			def := defaultByID[site.ID]
			site.Name = strings.TrimSpace(site.Name)
			if site.Name == "" {
				site.Name = def.Name
			}
			if site.Name == "" {
				site.Name = site.ID
			}
			site.Kind = strings.TrimSpace(site.Kind)
			if site.Kind == "" {
				site.Kind = def.Kind
			}
			switch site.Kind {
			case RelayBalanceKindPoloUsageToken, RelayBalanceKindBAIV1Balance, RelayBalanceKindNewAPIUserSelf:
			default:
				return nil, fmt.Errorf("sites[%d].kind %q is unsupported", i, site.Kind)
			}
			site.BaseURL = strings.TrimRight(strings.TrimSpace(site.BaseURL), "/")
			if site.BaseURL == "" {
				site.BaseURL = def.BaseURL
			}
			if site.BaseURL == "" {
				return nil, fmt.Errorf("sites[%d].base_url is required", i)
			}
			site.RechargeURL = strings.TrimSpace(site.RechargeURL)
			if site.RechargeURL == "" {
				site.RechargeURL = def.RechargeURL
			}
			if site.RechargeURL != "" &&
				!strings.HasPrefix(site.RechargeURL, "https://") &&
				!strings.HasPrefix(site.RechargeURL, "http://") {
				return nil, fmt.Errorf("sites[%d].recharge_url must start with http:// or https://", i)
			}
			site.ProviderPrefix = strings.TrimSpace(site.ProviderPrefix)
			if site.ProviderPrefix == "" {
				site.ProviderPrefix = def.ProviderPrefix
			}
			if site.QuotaPerUnit <= 0 {
				if def.QuotaPerUnit > 0 {
					site.QuotaPerUnit = def.QuotaPerUnit
				} else if site.Kind == RelayBalanceKindBAIV1Balance {
					site.QuotaPerUnit = 1000000
				} else {
					site.QuotaPerUnit = 500000
				}
			}
			if site.CNYPerSiteUnit <= 0 {
				if def.CNYPerSiteUnit > 0 {
					site.CNYPerSiteUnit = def.CNYPerSiteUnit
				} else {
					site.CNYPerSiteUnit = 1.0
				}
			}
			if site.ThresholdCNY < 0 {
				return nil, fmt.Errorf("sites[%d].threshold_cny cannot be negative", i)
			}
			if site.UrgentThresholdCNY < 0 {
				return nil, fmt.Errorf("sites[%d].urgent_threshold_cny cannot be negative", i)
			}
			if site.UrgentThresholdCNY > site.ThresholdCNY {
				site.UrgentThresholdCNY = site.ThresholdCNY
			}
			site.UserID = strings.TrimSpace(site.UserID)
			site.AccessToken = strings.TrimSpace(site.AccessToken)
			normalizedSites = append(normalizedSites, site)
		}
		out.Sites = normalizedSites
	}
	return &out, nil
}

func (s *RDBLogStore) GetRelayBalanceConfig(ctx context.Context) (*RelayBalanceConfig, error) {
	var row RelayBalanceSettingsRow
	err := s.db.WithContext(ctx).Where("id = ?", defaultRelayBalanceSettingsID).First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return DefaultRelayBalanceConfig(), nil
	}
	if err != nil {
		return nil, err
	}
	var cfg RelayBalanceConfig
	if err := json.Unmarshal([]byte(row.ConfigJSON), &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse relay balance config: %w", err)
	}
	return NormalizeRelayBalanceConfig(&cfg)
}

func (s *RDBLogStore) SaveRelayBalanceConfig(ctx context.Context, cfg *RelayBalanceConfig) (*RelayBalanceConfig, error) {
	normalized, err := NormalizeRelayBalanceConfig(cfg)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return nil, err
	}
	row := &RelayBalanceSettingsRow{
		ID:         defaultRelayBalanceSettingsID,
		ConfigJSON: string(raw),
	}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"config_json", "updated_at"}),
	}).Create(row).Error; err != nil {
		return nil, err
	}
	return normalized, nil
}

func (s *RDBLogStore) ListRelayBalanceStates(ctx context.Context) ([]RelayBalanceState, error) {
	var rows []RelayBalanceState
	if err := s.db.WithContext(ctx).Order("site_id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *RDBLogStore) SaveRelayBalanceState(ctx context.Context, state *RelayBalanceState) error {
	if state == nil || strings.TrimSpace(state.SiteID) == "" {
		return fmt.Errorf("relay balance state site_id is required")
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "site_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"status",
			"site_balance_usd",
			"balance_cny",
			"site_used_usd",
			"spend_24h_cny",
			"estimated_days_left",
			"key_source",
			"last_checked_at",
			"last_success_at",
			"last_error",
			"consecutive_errors",
			"alert_active",
			"last_alert_tier",
			"last_alert_at",
			"last_alert_balance_cny",
			"pending_after_quiet",
			"updated_at",
		}),
	}).Create(state).Error
}

func (s *RDBLogStore) GetProviderCost24hUSD(ctx context.Context, since time.Time) (map[string]float64, error) {
	var rows []struct {
		Provider string
		CostUSD  float64
	}
	if err := s.db.WithContext(ctx).Model(&ProfitEvent{}).
		Select("provider, COALESCE(SUM(cost_usd), 0) AS cost_usd").
		Where("timestamp >= ? AND log_id NOT LIKE ?", since, ExternalProfitLogIDPrefix+"%").
		Group("provider").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[string]float64, len(rows))
	for _, r := range rows {
		result[r.Provider] = r.CostUSD
	}
	return result, nil
}
