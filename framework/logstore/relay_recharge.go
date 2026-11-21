package logstore

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	RelayRechargeSourceAPISync      = "api_sync"
	RelayRechargeSourceBalanceDelta = "balance_delta"
	RelayRechargeSourceManual       = "manual"

	RelayRechargeStatusSuccess        = "success"
	RelayRechargeStatusPendingConfirm = "pending_confirm"
)

// RelayRechargeRecord stores one upstream relay top-up / recharge record for reconciliation.
type RelayRechargeRecord struct {
	ID            string    `gorm:"column:id;primaryKey;type:varchar(128)" json:"id"`
	SiteID        string    `gorm:"column:site_id;type:varchar(64);not null;index:idx_relay_recharges_site_time" json:"site_id"`
	SiteName      string    `gorm:"column:site_name;type:varchar(128);not null" json:"site_name"`
	Source        string    `gorm:"column:source;type:varchar(32);not null" json:"source"`
	ExternalID    string    `gorm:"column:external_id;type:varchar(128)" json:"external_id,omitempty"`
	RechargedAt   time.Time `gorm:"column:recharged_at;not null;index:idx_relay_recharges_site_time;index:idx_relay_recharges_time" json:"recharged_at"`
	SiteAmountUSD float64   `gorm:"column:site_amount_usd;not null" json:"site_amount_usd"`
	PaidAmount    float64   `gorm:"column:paid_amount;not null" json:"paid_amount"`
	PaidCurrency  string    `gorm:"column:paid_currency;type:varchar(16);not null" json:"paid_currency"`
	PaidCNY       float64   `gorm:"column:paid_cny;not null" json:"paid_cny"`
	PaymentMethod string    `gorm:"column:payment_method;type:varchar(64)" json:"payment_method,omitempty"`
	Status        string    `gorm:"column:status;type:varchar(32);not null" json:"status"`
	Note          string    `gorm:"column:note;type:text" json:"note,omitempty"`
	CreatedAt     time.Time `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt     time.Time `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (RelayRechargeRecord) TableName() string {
	return "relay_recharge_records"
}

// RelayRechargeQuery filters recharge history queries.
type RelayRechargeQuery struct {
	SiteID    string
	StartTime *time.Time
	EndTime   *time.Time
	Limit     int
}

// RelayRechargeSiteSummary aggregates recharge totals for one site.
type RelayRechargeSiteSummary struct {
	SiteID          string     `json:"site_id"`
	SiteName        string     `json:"site_name"`
	RecordCount     int        `json:"record_count"`
	PendingCount    int        `json:"pending_count"`
	TotalSiteUSD    float64    `json:"total_site_usd"`
	TotalPaidCNY    float64    `json:"total_paid_cny"`
	LastRechargedAt *time.Time `json:"last_recharged_at,omitempty"`
}

// RelayRechargeSummary aggregates totals across matched recharge records.
type RelayRechargeSummary struct {
	TotalCount   int                        `json:"total_count"`
	PendingCount int                        `json:"pending_count"`
	TotalSiteUSD float64                    `json:"total_site_usd"`
	TotalPaidCNY float64                    `json:"total_paid_cny"`
	BySite       []RelayRechargeSiteSummary `json:"by_site"`
}

func normalizeRelayRechargeRecord(rec *RelayRechargeRecord) error {
	if rec == nil {
		return fmt.Errorf("recharge record is nil")
	}
	rec.ID = strings.TrimSpace(rec.ID)
	if rec.ID == "" {
		return fmt.Errorf("recharge record id is required")
	}
	rec.SiteID = strings.ToLower(strings.TrimSpace(rec.SiteID))
	if rec.SiteID == "" {
		return fmt.Errorf("recharge record site_id is required")
	}
	rec.SiteName = strings.TrimSpace(rec.SiteName)
	if rec.SiteName == "" {
		rec.SiteName = rec.SiteID
	}
	rec.Source = strings.TrimSpace(rec.Source)
	switch rec.Source {
	case RelayRechargeSourceAPISync, RelayRechargeSourceBalanceDelta, RelayRechargeSourceManual:
	default:
		rec.Source = RelayRechargeSourceManual
	}
	rec.ExternalID = strings.TrimSpace(rec.ExternalID)
	if rec.RechargedAt.IsZero() {
		rec.RechargedAt = time.Now().UTC()
	}
	if rec.SiteAmountUSD < 0 {
		return fmt.Errorf("site_amount_usd cannot be negative")
	}
	if rec.PaidAmount < 0 {
		return fmt.Errorf("paid_amount cannot be negative")
	}
	if rec.PaidCNY < 0 {
		return fmt.Errorf("paid_cny cannot be negative")
	}
	rec.PaidCurrency = strings.ToUpper(strings.TrimSpace(rec.PaidCurrency))
	if rec.PaidCurrency == "" {
		rec.PaidCurrency = "CNY"
	}
	rec.PaymentMethod = strings.TrimSpace(rec.PaymentMethod)
	rec.Status = strings.TrimSpace(rec.Status)
	if rec.Status == "" {
		rec.Status = RelayRechargeStatusSuccess
	}
	rec.Note = strings.TrimSpace(rec.Note)
	return nil
}

// UpsertRelayRechargeRecords inserts new synced or auto-detected recharge rows idempotently
// without overwriting existing records (preserving any user edits or notes).
func (s *RDBLogStore) UpsertRelayRechargeRecords(ctx context.Context, records []RelayRechargeRecord) (int, error) {
	if len(records) == 0 {
		return 0, nil
	}
	normalized := make([]RelayRechargeRecord, 0, len(records))
	for i := range records {
		rec := records[i]
		if err := normalizeRelayRechargeRecord(&rec); err != nil {
			return 0, fmt.Errorf("records[%d]: %w", i, err)
		}
		normalized = append(normalized, rec)
	}
	tx := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoNothing: true,
	}).Create(&normalized)
	if tx.Error != nil {
		return 0, tx.Error
	}
	return int(tx.RowsAffected), nil
}

func (s *RDBLogStore) GetRelayRechargeRecord(ctx context.Context, id string) (*RelayRechargeRecord, error) {
	var rec RelayRechargeRecord
	err := s.db.WithContext(ctx).Where("id = ?", strings.TrimSpace(id)).First(&rec).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

func (s *RDBLogStore) SaveRelayRechargeRecord(ctx context.Context, record *RelayRechargeRecord) error {
	if err := normalizeRelayRechargeRecord(record); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"site_id",
			"site_name",
			"source",
			"external_id",
			"recharged_at",
			"site_amount_usd",
			"paid_amount",
			"paid_currency",
			"paid_cny",
			"payment_method",
			"status",
			"note",
			"updated_at",
		}),
	}).Create(record).Error
}

func (s *RDBLogStore) DeleteRelayRechargeRecord(ctx context.Context, id string) error {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return fmt.Errorf("recharge record id is required")
	}
	return s.db.WithContext(ctx).Where("id = ?", trimmed).Delete(&RelayRechargeRecord{}).Error
}

func (s *RDBLogStore) ListRelayRechargeRecords(ctx context.Context, query RelayRechargeQuery) ([]RelayRechargeRecord, *RelayRechargeSummary, error) {
	baseQ := s.db.WithContext(ctx).Model(&RelayRechargeRecord{})
	siteFilter := strings.ToLower(strings.TrimSpace(query.SiteID))
	if siteFilter != "" && siteFilter != "all" {
		baseQ = baseQ.Where("site_id = ?", siteFilter)
	}
	if query.StartTime != nil {
		baseQ = baseQ.Where("recharged_at >= ?", *query.StartTime)
	}
	if query.EndTime != nil {
		baseQ = baseQ.Where("recharged_at <= ?", *query.EndTime)
	}

	var allRows []RelayRechargeRecord
	if err := baseQ.Order("recharged_at DESC, id DESC").Find(&allRows).Error; err != nil {
		return nil, nil, err
	}

	summary := &RelayRechargeSummary{}
	bySiteMap := make(map[string]*RelayRechargeSiteSummary)
	for i := range allRows {
		r := &allRows[i]
		summary.TotalCount++
		summary.TotalSiteUSD += r.SiteAmountUSD
		summary.TotalPaidCNY += r.PaidCNY
		if r.Status == RelayRechargeStatusPendingConfirm {
			summary.PendingCount++
		}
		agg, ok := bySiteMap[r.SiteID]
		if !ok {
			agg = &RelayRechargeSiteSummary{
				SiteID:   r.SiteID,
				SiteName: r.SiteName,
			}
			bySiteMap[r.SiteID] = agg
		}
		agg.RecordCount++
		agg.TotalSiteUSD += r.SiteAmountUSD
		agg.TotalPaidCNY += r.PaidCNY
		if r.Status == RelayRechargeStatusPendingConfirm {
			agg.PendingCount++
		}
		if agg.LastRechargedAt == nil || r.RechargedAt.After(*agg.LastRechargedAt) {
			t := r.RechargedAt
			agg.LastRechargedAt = &t
		}
	}

	summary.BySite = make([]RelayRechargeSiteSummary, 0, len(bySiteMap))
	for _, agg := range bySiteMap {
		summary.BySite = append(summary.BySite, *agg)
	}
	sort.Slice(summary.BySite, func(i, j int) bool {
		return summary.BySite[i].TotalPaidCNY > summary.BySite[j].TotalPaidCNY
	})

	limit := query.Limit
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	if len(allRows) > limit {
		allRows = allRows[:limit]
	}
	return allRows, summary, nil
}
