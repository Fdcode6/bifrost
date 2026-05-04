package handlers

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/maximhq/bifrost/framework/logstore"
	"github.com/maximhq/bifrost/plugins/logging"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

type mockProfitLogManager struct {
	logging.LogManager
	settings       *logstore.ProfitSettings
	savedSettings  *logstore.ProfitSettings
	summary        *logstore.ProfitSummary
	daily          []logstore.ProfitDailyBucket
	breakdown      []logstore.ProfitBreakdownRow
	backfillResult *logstore.ProfitBackfillResult
	status         *logstore.ProfitReconciliationStatus
	backfillLimit  int
	recalcCalled   bool
	recalcLimit    int
	recalcCalls    int
	recalcResults  []*logging.RecalculateCostResult
}

func (m *mockProfitLogManager) GetProfitSettings(_ context.Context) (*logstore.ProfitSettings, error) {
	return m.settings, nil
}

func (m *mockProfitLogManager) SaveProfitSettings(_ context.Context, settings *logstore.ProfitSettings) (*logstore.ProfitSettings, error) {
	m.savedSettings = settings
	m.settings = settings
	return settings, nil
}

func (m *mockProfitLogManager) GetProfitSummary(_ context.Context, query logstore.ProfitQuery) (*logstore.ProfitSummary, error) {
	return m.summary, nil
}

func (m *mockProfitLogManager) GetProfitDaily(_ context.Context, query logstore.ProfitQuery) ([]logstore.ProfitDailyBucket, error) {
	return m.daily, nil
}

func (m *mockProfitLogManager) GetProfitBreakdown(_ context.Context, query logstore.ProfitQuery) ([]logstore.ProfitBreakdownRow, error) {
	return m.breakdown, nil
}

func (m *mockProfitLogManager) BackfillProfitEvents(_ context.Context, limit int) (*logstore.ProfitBackfillResult, error) {
	m.backfillLimit = limit
	return m.backfillResult, nil
}

func (m *mockProfitLogManager) GetProfitReconciliationStatus(_ context.Context) (*logstore.ProfitReconciliationStatus, error) {
	return m.status, nil
}

func (m *mockProfitLogManager) RecalculateCosts(_ context.Context, _ *logstore.SearchFilters, limit int) (*logging.RecalculateCostResult, error) {
	m.recalcCalled = true
	m.recalcCalls++
	m.recalcLimit = limit
	if len(m.recalcResults) > 0 {
		index := m.recalcCalls - 1
		if index >= len(m.recalcResults) {
			index = len(m.recalcResults) - 1
		}
		return m.recalcResults[index], nil
	}
	return &logging.RecalculateCostResult{Updated: 3, Remaining: 0}, nil
}

func TestProfitSettingsHandlers(t *testing.T) {
	manager := &mockProfitLogManager{
		settings: &logstore.ProfitSettings{
			SellInputPer1MUSD:  2,
			SellOutputPer1MUSD: 12,
			Timezone:           "Asia/Shanghai",
		},
	}
	handler := NewLoggingHandler(manager, nil, nil)

	getCtx := &fasthttp.RequestCtx{}
	getCtx.Request.Header.SetMethod("GET")
	getCtx.Request.SetRequestURI("/api/profit/settings")
	handler.getProfitSettings(getCtx)
	require.Equal(t, fasthttp.StatusOK, getCtx.Response.StatusCode(), string(getCtx.Response.Body()))
	var getResp logstore.ProfitSettings
	require.NoError(t, json.Unmarshal(getCtx.Response.Body(), &getResp))
	require.Equal(t, 2.0, getResp.SellInputPer1MUSD)
	require.Equal(t, 12.0, getResp.SellOutputPer1MUSD)

	putCtx := &fasthttp.RequestCtx{}
	putCtx.Request.Header.SetMethod("PUT")
	putCtx.Request.SetRequestURI("/api/profit/settings")
	putCtx.Request.SetBodyString(`{"sell_input_per_1m_usd":2.5,"sell_output_per_1m_usd":15}`)
	handler.updateProfitSettings(putCtx)
	require.Equal(t, fasthttp.StatusOK, putCtx.Response.StatusCode(), string(putCtx.Response.Body()))
	require.NotNil(t, manager.savedSettings)
	require.Equal(t, 2.5, manager.savedSettings.SellInputPer1MUSD)
	require.Equal(t, 15.0, manager.savedSettings.SellOutputPer1MUSD)
	require.Equal(t, "Asia/Shanghai", manager.savedSettings.Timezone)
}

func TestProfitSummaryDailyAndBackfillHandlers(t *testing.T) {
	margin := 0.75
	manager := &mockProfitLogManager{
		summary: &logstore.ProfitSummary{
			RevenueUSD:   20,
			CostUSD:      5,
			ProfitUSD:    15,
			GrossMargin:  &margin,
			RequestCount: 4,
			SuccessCount: 3,
			ErrorCount:   1,
		},
		daily: []logstore.ProfitDailyBucket{{
			BusinessDay:  "2026-05-04",
			RevenueUSD:   20,
			CostUSD:      5,
			ProfitUSD:    15,
			GrossMargin:  &margin,
			RequestCount: 4,
		}},
		breakdown: []logstore.ProfitBreakdownRow{{
			Provider:     "yunwu",
			Model:        "gemini-3.1-pro-preview",
			RevenueUSD:   20,
			CostUSD:      5,
			ProfitUSD:    15,
			GrossMargin:  &margin,
			RequestCount: 4,
			SuccessCount: 3,
			ErrorCount:   1,
		}},
		backfillResult: &logstore.ProfitBackfillResult{Processed: 10, Created: 8, Updated: 2},
	}
	handler := NewLoggingHandler(manager, nil, nil)

	summaryCtx := &fasthttp.RequestCtx{}
	summaryCtx.Request.Header.SetMethod("GET")
	summaryCtx.Request.SetRequestURI("/api/profit/summary?preset=today")
	handler.getProfitSummary(summaryCtx)
	require.Equal(t, fasthttp.StatusOK, summaryCtx.Response.StatusCode(), string(summaryCtx.Response.Body()))
	require.Contains(t, string(summaryCtx.Response.Body()), `"profit_usd":15`)

	dailyCtx := &fasthttp.RequestCtx{}
	dailyCtx.Request.Header.SetMethod("GET")
	dailyCtx.Request.SetRequestURI("/api/profit/daily?days=30")
	handler.getProfitDaily(dailyCtx)
	require.Equal(t, fasthttp.StatusOK, dailyCtx.Response.StatusCode(), string(dailyCtx.Response.Body()))
	require.Contains(t, string(dailyCtx.Response.Body()), `"business_day":"2026-05-04"`)

	breakdownCtx := &fasthttp.RequestCtx{}
	breakdownCtx.Request.Header.SetMethod("GET")
	breakdownCtx.Request.SetRequestURI("/api/profit/breakdown?preset=all")
	handler.getProfitBreakdown(breakdownCtx)
	require.Equal(t, fasthttp.StatusOK, breakdownCtx.Response.StatusCode(), string(breakdownCtx.Response.Body()))
	require.Contains(t, string(breakdownCtx.Response.Body()), `"provider":"yunwu"`)
	require.Contains(t, string(breakdownCtx.Response.Body()), `"model":"gemini-3.1-pro-preview"`)

	backfillCtx := &fasthttp.RequestCtx{}
	backfillCtx.Request.Header.SetMethod("POST")
	backfillCtx.Request.SetRequestURI("/api/profit/backfill")
	backfillCtx.Request.SetBodyString(`{"limit":10}`)
	handler.backfillProfitEvents(backfillCtx)
	require.Equal(t, fasthttp.StatusOK, backfillCtx.Response.StatusCode(), string(backfillCtx.Response.Body()))
	require.True(t, manager.recalcCalled)
	require.Equal(t, 10, manager.recalcLimit)
	require.Equal(t, 10, manager.backfillLimit)
	require.Contains(t, string(backfillCtx.Response.Body()), `"created":8`)
}

func TestProfitReconciliationStatusHandler(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	next := now.Add(10 * time.Minute)
	manager := &mockProfitLogManager{
		status: &logstore.ProfitReconciliationStatus{
			MissingEventCount: 42,
			LastRunAt:         &now,
			NextRunAt:         &next,
			IntervalSeconds:   600,
			BatchLimit:        1000,
			LastResult:        &logstore.ProfitBackfillResult{Processed: 42, Created: 42},
		},
	}
	handler := NewLoggingHandler(manager, nil, nil)

	statusCtx := &fasthttp.RequestCtx{}
	statusCtx.Request.Header.SetMethod("GET")
	statusCtx.Request.SetRequestURI("/api/profit/reconciliation-status")
	handler.getProfitReconciliationStatus(statusCtx)

	require.Equal(t, fasthttp.StatusOK, statusCtx.Response.StatusCode(), string(statusCtx.Response.Body()))
	require.Contains(t, string(statusCtx.Response.Body()), `"missing_event_count":42`)
	require.Contains(t, string(statusCtx.Response.Body()), `"interval_seconds":600`)
	require.Contains(t, string(statusCtx.Response.Body()), `"created":42`)
}

func TestProfitBackfillStopsCostRecalculationWhenRemainingDoesNotShrink(t *testing.T) {
	manager := &mockProfitLogManager{
		backfillResult: &logstore.ProfitBackfillResult{Processed: 10},
		recalcResults: []*logging.RecalculateCostResult{
			{Updated: 1, Remaining: 5},
			{Updated: 1, Remaining: 5},
			{Updated: 1, Remaining: 5},
		},
	}
	handler := NewLoggingHandler(manager, nil, nil)

	backfillCtx := &fasthttp.RequestCtx{}
	backfillCtx.Request.Header.SetMethod("POST")
	backfillCtx.Request.SetRequestURI("/api/profit/backfill")
	backfillCtx.Request.SetBodyString(`{"limit":10}`)
	handler.backfillProfitEvents(backfillCtx)

	require.Equal(t, fasthttp.StatusOK, backfillCtx.Response.StatusCode(), string(backfillCtx.Response.Body()))
	require.Equal(t, 2, manager.recalcCalls)
}
