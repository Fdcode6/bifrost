package logstore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

type rankedLogProjection struct {
	Log
	GroupID         string `gorm:"column:group_id"`
	AttemptSequence int    `gorm:"column:attempt_sequence"`
	IsFinalAttempt  bool   `gorm:"column:is_final_attempt"`
}

type completedAttemptStats struct {
	CompletedCount sql.NullInt64   `gorm:"column:completed_count"`
	SuccessCount   sql.NullInt64   `gorm:"column:success_count"`
	AvgLatency     sql.NullFloat64 `gorm:"column:avg_latency"`
	TotalTokens    sql.NullInt64   `gorm:"column:total_tokens"`
	TotalCost      sql.NullFloat64 `gorm:"column:total_cost"`
}

type completedRequestStats struct {
	CompletedCount sql.NullInt64   `gorm:"column:completed_count"`
	SuccessCount   sql.NullInt64   `gorm:"column:success_count"`
	AvgLatency     sql.NullFloat64 `gorm:"column:avg_latency"`
}

func (s *RDBLogStore) rankedAttemptsBaseQuery(ctx context.Context) *gorm.DB {
	return s.db.WithContext(ctx).
		Table("logs").
		Select(`
			logs.*,
			COALESCE(parent_request_id, id) AS group_id,
			ROW_NUMBER() OVER (
				PARTITION BY COALESCE(parent_request_id, id)
				ORDER BY fallback_index ASC, timestamp ASC, id ASC
			) AS attempt_sequence,
			CASE WHEN ROW_NUMBER() OVER (
				PARTITION BY COALESCE(parent_request_id, id)
				ORDER BY fallback_index DESC, timestamp DESC, id DESC
			) = 1 THEN TRUE ELSE FALSE END AS is_final_attempt
		`)
}

func (s *RDBLogStore) filteredAttemptRowsQuery(ctx context.Context, filters SearchFilters) *gorm.DB {
	query := s.db.WithContext(ctx).Table("(?) AS ranked", s.rankedAttemptsBaseQuery(ctx))
	return s.applyFilters(query, filters)
}

func (s *RDBLogStore) filteredFinalAttemptsQuery(ctx context.Context, filters SearchFilters) *gorm.DB {
	query := s.db.WithContext(ctx).
		Table("(?) AS ranked", s.rankedAttemptsBaseQuery(ctx)).
		Where("is_final_attempt = ?", true)
	return s.applyFilters(query, filters)
}

func mapRankedLogProjection(row rankedLogProjection) (Log, error) {
	logEntry := row.Log
	logEntry.GroupID = row.GroupID
	logEntry.AttemptSequence = row.AttemptSequence
	logEntry.IsFinalAttempt = row.IsFinalAttempt
	if err := logEntry.DeserializeFields(); err != nil {
		return Log{}, err
	}
	return logEntry, nil
}

func mapRankedLogProjections(rows []rankedLogProjection) ([]Log, error) {
	logs := make([]Log, 0, len(rows))
	for _, row := range rows {
		logEntry, err := mapRankedLogProjection(row)
		if err != nil {
			return nil, err
		}
		logs = append(logs, logEntry)
	}
	return logs, nil
}

func distributionValueAndLabelExpressions(groupBy FinalSuccessDistributionDimension) (string, string, error) {
	switch groupBy {
	case FinalSuccessDistributionByModel:
		return "COALESCE(model, '')", "COALESCE(model, '')", nil
	case FinalSuccessDistributionByProvider:
		return "COALESCE(provider, '')", "COALESCE(provider, '')", nil
	case FinalSuccessDistributionByKey:
		return `
			CASE
				WHEN COALESCE(selected_key_id, '') != '' THEN selected_key_id
				ELSE 'unassigned'
			END
		`, `
			CASE
				WHEN COALESCE(selected_key_name, '') != '' THEN selected_key_name
				WHEN COALESCE(selected_key_id, '') != '' THEN selected_key_id
				ELSE 'Unassigned'
			END
		`, nil
	case FinalSuccessDistributionByLayer:
		return `
			CASE
				WHEN route_layer_index IS NULL THEN 'unlayered'
				ELSE 'layer:' || CAST(route_layer_index AS TEXT)
			END
		`, `
			CASE
				WHEN route_layer_index IS NULL THEN 'Unlayered'
				ELSE 'Layer ' || CAST(route_layer_index + 1 AS TEXT)
			END
		`, nil
	default:
		return "", "", fmt.Errorf("unsupported final success distribution dimension: %s", groupBy)
	}
}

func normalizeDistributionExpression(expr string) string {
	return strings.Join(strings.Fields(expr), " ")
}
