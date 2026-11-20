package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/logstore"
	"github.com/maximhq/bifrost/plugins/logging"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

type mockProfitLogManager struct {
	logging.LogManager
	settings                 *logstore.ProfitSettings
	savedSettings            *logstore.ProfitSettings
	routingRulePrices        []logstore.RoutingRuleProfitSettings
	savedRoutingRuleID       string
	savedRoutingRuleSettings *logstore.ProfitSettings
	deletedRoutingRuleID     string
	summary                  *logstore.ProfitSummary
	daily                    []logstore.ProfitDailyBucket
	breakdown                []logstore.ProfitBreakdownRow
	lastSummaryQuery         logstore.ProfitQuery
	lastDailyQuery           logstore.ProfitQuery
	lastBreakdownQuery       logstore.ProfitQuery
	ingestedEvents           []logstore.ExternalProfitEventInput
	ingestResult             *logstore.ExternalProfitIngestResult
	ingestErr                error
	backfillResult           *logstore.ProfitBackfillResult
	status                   *logstore.ProfitReconciliationStatus
	backfillLimit            int
	recalcCalled             bool
	recalcLimit              int
	recalcCalls              int
	recalcResults            []*logging.RecalculateCostResult
}

func (m *mockProfitLogManager) GetProfitSettings(_ context.Context) (*logstore.ProfitSettings, error) {
	return m.settings, nil
}

func (m *mockProfitLogManager) SaveProfitSettings(_ context.Context, settings *logstore.ProfitSettings) (*logstore.ProfitSettings, error) {
	m.savedSettings = settings
	m.settings = settings
	return settings, nil
}

func (m *mockProfitLogManager) ListRoutingRuleProfitSettings(_ context.Context) ([]logstore.RoutingRuleProfitSettings, error) {
	return m.routingRulePrices, nil
}

func (m *mockProfitLogManager) SaveRoutingRuleProfitSettings(_ context.Context, routingRuleID string, settings *logstore.ProfitSettings) (*logstore.ProfitSettings, error) {
	m.savedRoutingRuleID = routingRuleID
	m.savedRoutingRuleSettings = settings
	return settings, nil
}

func (m *mockProfitLogManager) DeleteRoutingRuleProfitSettings(_ context.Context, routingRuleID string) error {
	m.deletedRoutingRuleID = routingRuleID
	return nil
}

func (m *mockProfitLogManager) GetProfitSummary(_ context.Context, query logstore.ProfitQuery) (*logstore.ProfitSummary, error) {
	m.lastSummaryQuery = query
	return m.summary, nil
}

func (m *mockProfitLogManager) GetProfitDaily(_ context.Context, query logstore.ProfitQuery) ([]logstore.ProfitDailyBucket, error) {
	m.lastDailyQuery = query
	return m.daily, nil
}

func (m *mockProfitLogManager) GetProfitBreakdown(_ context.Context, query logstore.ProfitQuery) ([]logstore.ProfitBreakdownRow, error) {
	m.lastBreakdownQuery = query
	return m.breakdown, nil
}

func (m *mockProfitLogManager) UpsertExternalProfitEvents(_ context.Context, events []logstore.ExternalProfitEventInput) (*logstore.ExternalProfitIngestResult, error) {
	m.ingestedEvents = append([]logstore.ExternalProfitEventInput(nil), events...)
	if m.ingestErr != nil {
		return nil, m.ingestErr
	}
	if m.ingestResult != nil {
		return m.ingestResult, nil
	}
	return &logstore.ExternalProfitIngestResult{Received: len(events), Upserted: len(events)}, nil
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

func TestProfitRoutingRulePriceHandlers(t *testing.T) {
	manager := &mockProfitLogManager{
		routingRulePrices: []logstore.RoutingRuleProfitSettings{{
			RoutingRuleID:      "rule-review",
			ID:                 "routing_rule:rule-review",
			SellInputPer1MUSD:  5,
			SellOutputPer1MUSD: 30,
			Timezone:           "Asia/Shanghai",
		}},
	}
	handler := NewLoggingHandler(manager, nil, nil)

	getCtx := &fasthttp.RequestCtx{}
	getCtx.Request.Header.SetMethod("GET")
	getCtx.Request.SetRequestURI("/api/profit/routing-rule-prices")
	handler.getRoutingRuleProfitSettings(getCtx)
	require.Equal(t, fasthttp.StatusOK, getCtx.Response.StatusCode(), string(getCtx.Response.Body()))
	require.Contains(t, string(getCtx.Response.Body()), `"routing_rule_id":"rule-review"`)
	require.Contains(t, string(getCtx.Response.Body()), `"sell_output_per_1m_usd":30`)

	putCtx := &fasthttp.RequestCtx{}
	putCtx.Request.Header.SetMethod("PUT")
	putCtx.Request.SetRequestURI("/api/profit/routing-rule-prices/rule-review")
	putCtx.SetUserValue("rule_id", "rule-review")
	putCtx.Request.SetBodyString(`{"sell_input_per_1m_usd":6,"sell_output_per_1m_usd":36}`)
	handler.updateRoutingRuleProfitSettings(putCtx)
	require.Equal(t, fasthttp.StatusOK, putCtx.Response.StatusCode(), string(putCtx.Response.Body()))
	require.Equal(t, "rule-review", manager.savedRoutingRuleID)
	require.NotNil(t, manager.savedRoutingRuleSettings)
	require.Equal(t, 6.0, manager.savedRoutingRuleSettings.SellInputPer1MUSD)
	require.Equal(t, 36.0, manager.savedRoutingRuleSettings.SellOutputPer1MUSD)
	require.Equal(t, "Asia/Shanghai", manager.savedRoutingRuleSettings.Timezone)

	deleteCtx := &fasthttp.RequestCtx{}
	deleteCtx.Request.Header.SetMethod("DELETE")
	deleteCtx.Request.SetRequestURI("/api/profit/routing-rule-prices/rule-review")
	deleteCtx.SetUserValue("rule_id", "rule-review")
	handler.deleteRoutingRuleProfitSettings(deleteCtx)
	require.Equal(t, fasthttp.StatusOK, deleteCtx.Response.StatusCode(), string(deleteCtx.Response.Body()))
	require.Equal(t, "rule-review", manager.deletedRoutingRuleID)
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

func TestProfitSourceQueryAndExternalIngestHandlers(t *testing.T) {
	t.Setenv(profitIngestTokenEnvVar, "secret-ingest-token")

	manager := &mockProfitLogManager{
		summary:   &logstore.ProfitSummary{RevenueUSD: 10, CostUSD: 1, ProfitUSD: 9},
		daily:     []logstore.ProfitDailyBucket{{BusinessDay: "2026-11-20", RevenueUSD: 10, CostUSD: 1, ProfitUSD: 9}},
		breakdown: []logstore.ProfitBreakdownRow{{Provider: "volces-ark-direct", Model: "doubao-seed-2-0-mini-260428", RevenueUSD: 10}},
	}
	handler := NewLoggingHandler(manager, nil, nil)

	// Valid source filtering query params
	summaryCtx := &fasthttp.RequestCtx{}
	summaryCtx.Request.Header.SetMethod("GET")
	summaryCtx.Request.SetRequestURI("/api/profit/summary?preset=today&source=external")
	handler.getProfitSummary(summaryCtx)
	require.Equal(t, fasthttp.StatusOK, summaryCtx.Response.StatusCode(), string(summaryCtx.Response.Body()))
	require.Equal(t, logstore.ProfitSourceExternal, manager.lastSummaryQuery.Source)

	dailyCtx := &fasthttp.RequestCtx{}
	dailyCtx.Request.Header.SetMethod("GET")
	dailyCtx.Request.SetRequestURI("/api/profit/daily?days=30&source=bifrost")
	handler.getProfitDaily(dailyCtx)
	require.Equal(t, fasthttp.StatusOK, dailyCtx.Response.StatusCode(), string(dailyCtx.Response.Body()))
	require.Equal(t, logstore.ProfitSourceBifrost, manager.lastDailyQuery.Source)

	badSourceCtx := &fasthttp.RequestCtx{}
	badSourceCtx.Request.Header.SetMethod("GET")
	badSourceCtx.Request.SetRequestURI("/api/profit/breakdown?preset=today&source=unknown")
	handler.getProfitBreakdown(badSourceCtx)
	require.Equal(t, fasthttp.StatusBadRequest, badSourceCtx.Response.StatusCode())

	// Direct ingest handler enforces token when BIFROST_PROFIT_INGEST_TOKEN is configured
	unauthCtx := &fasthttp.RequestCtx{}
	unauthCtx.Request.Header.SetMethod("POST")
	unauthCtx.Request.SetRequestURI(profitExternalEventsRoute)
	unauthCtx.Request.SetBodyString(`{"events":[]}`)
	handler.ingestExternalProfitEvents(unauthCtx)
	require.Equal(t, fasthttp.StatusUnauthorized, unauthCtx.Response.StatusCode())

	okCtx := &fasthttp.RequestCtx{}
	okCtx.Request.Header.SetMethod("POST")
	okCtx.Request.SetRequestURI(profitExternalEventsRoute)
	okCtx.Request.Header.Set(profitIngestTokenHeader, "secret-ingest-token")
	okCtx.Request.SetBodyString(`{"events":[{"event_id":"ext:qiwei:reply:1:0","timestamp":"2026-11-20T10:00:00+08:00","provider":"volces-ark-direct","model":"doubao-seed-2-0-mini-260428","prompt_tokens":1000,"completion_tokens":200,"cost_usd":0.001,"revenue_usd":0.02}]}`)
	handler.ingestExternalProfitEvents(okCtx)
	require.Equal(t, fasthttp.StatusOK, okCtx.Response.StatusCode(), string(okCtx.Response.Body()))
	require.Len(t, manager.ingestedEvents, 1)
	require.Equal(t, "ext:qiwei:reply:1:0", manager.ingestedEvents[0].EventID)

	// Validation errors map to 400 Bad Request
	manager.ingestErr = fmt.Errorf("%w: missing ext prefix", logstore.ErrInvalidExternalProfitEvent)
	badEventCtx := &fasthttp.RequestCtx{}
	badEventCtx.Request.Header.SetMethod("POST")
	badEventCtx.Request.SetRequestURI(profitExternalEventsRoute)
	badEventCtx.Request.Header.Set(profitIngestTokenHeader, "secret-ingest-token")
	badEventCtx.Request.SetBodyString(`{"events":[{"event_id":"bad-id"}]}`)
	handler.ingestExternalProfitEvents(badEventCtx)
	require.Equal(t, fasthttp.StatusBadRequest, badEventCtx.Response.StatusCode())
}

func TestAuthMiddlewareAllowsProfitIngestTokenOnlyOnExternalEventsRoute(t *testing.T) {
	t.Setenv(profitIngestTokenEnvVar, "secret-ingest-token")

	am := &AuthMiddleware{}
	am.authConfig.Store(&configstore.AuthConfig{
		IsEnabled:     true,
		AdminUserName: schemas.NewEnvVar("admin"),
		AdminPassword: schemas.NewEnvVar("hash"),
	})
	mw := am.APIMiddleware()

	called := false
	next := mw(func(ctx *fasthttp.RequestCtx) {
		called = true
		ctx.SetStatusCode(fasthttp.StatusOK)
	})

	// Valid token succeeds on /api/profit/external-events
	ingestCtx := &fasthttp.RequestCtx{}
	ingestCtx.Request.Header.SetMethod("POST")
	ingestCtx.Request.SetRequestURI(profitExternalEventsRoute)
	ingestCtx.Request.Header.Set(profitIngestTokenHeader, "secret-ingest-token")
	next(ingestCtx)
	require.True(t, called)
	require.Equal(t, fasthttp.StatusOK, ingestCtx.Response.StatusCode())

	// Wrong token is rejected on /api/profit/external-events
	called = false
	wrongCtx := &fasthttp.RequestCtx{}
	wrongCtx.Request.Header.SetMethod("POST")
	wrongCtx.Request.SetRequestURI(profitExternalEventsRoute)
	wrongCtx.Request.Header.Set(profitIngestTokenHeader, "wrong-token")
	next(wrongCtx)
	require.False(t, called)
	require.Equal(t, fasthttp.StatusUnauthorized, wrongCtx.Response.StatusCode())

	// Valid ingest token MUST NOT authorize other admin routes
	called = false
	otherCtx := &fasthttp.RequestCtx{}
	otherCtx.Request.Header.SetMethod("GET")
	otherCtx.Request.SetRequestURI("/api/profit/settings")
	otherCtx.Request.Header.Set(profitIngestTokenHeader, "secret-ingest-token")
	next(otherCtx)
	require.False(t, called)
	require.Equal(t, fasthttp.StatusUnauthorized, otherCtx.Response.StatusCode())
}
