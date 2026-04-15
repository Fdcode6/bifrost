package handlers

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/logstore"
	"github.com/maximhq/bifrost/plugins/logging"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

type stubLogManager struct {
	logging.LogManager
	searchResult      *logstore.SearchResult
	searchErr         error
	logEntry          *logstore.Log
	logErr            error
	stats             *logstore.SearchStats
	statsErr          error
	finalDistribution *logstore.FinalSuccessDistributionResult
	finalErr          error
	lastFilters       *logstore.SearchFilters
	lastPagination    *logstore.PaginationOptions
	lastGroupBy       logstore.FinalSuccessDistributionDimension
}

func (s *stubLogManager) Search(_ context.Context, filters *logstore.SearchFilters, pagination *logstore.PaginationOptions) (*logstore.SearchResult, error) {
	s.lastFilters = filters
	s.lastPagination = pagination
	return s.searchResult, s.searchErr
}

func (s *stubLogManager) GetLog(_ context.Context, _ string) (*logstore.Log, error) {
	return s.logEntry, s.logErr
}

func (s *stubLogManager) GetStats(_ context.Context, filters *logstore.SearchFilters) (*logstore.SearchStats, error) {
	s.lastFilters = filters
	return s.stats, s.statsErr
}

func (s *stubLogManager) GetFinalSuccessDistribution(_ context.Context, filters *logstore.SearchFilters, groupBy logstore.FinalSuccessDistributionDimension) (*logstore.FinalSuccessDistributionResult, error) {
	s.lastFilters = filters
	s.lastGroupBy = groupBy
	return s.finalDistribution, s.finalErr
}

type stubRedactedKeysManager struct{}

func (stubRedactedKeysManager) GetAllRedactedKeys(context.Context, []string) []schemas.Key {
	return nil
}

func (stubRedactedKeysManager) GetAllRedactedVirtualKeys(context.Context, []string) []tables.TableVirtualKey {
	return nil
}

func (stubRedactedKeysManager) GetAllRedactedRoutingRules(context.Context, []string) []tables.TableRoutingRule {
	return nil
}

func TestGetLogsIncludesRequestGroupingMetadata(t *testing.T) {
	SetLogger(&mockLogger{})

	handler := NewLoggingHandler(&stubLogManager{
		searchResult: &logstore.SearchResult{
			Logs: []logstore.Log{{
				ID:              "req-1-fb1",
				ParentRequestID: stringPtr("req-1"),
				GroupID:         "req-1",
				AttemptSequence: 2,
				IsFinalAttempt:  true,
				RouteLayerIndex: intPtr(1),
				Timestamp:       time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC),
				Object:          "chat_completion",
				Provider:        "azure",
				Model:           "gpt-4.1",
				FallbackIndex:   1,
				Status:          "success",
				CreatedAt:       time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC),
			}},
			Pagination: logstore.PaginationOptions{Limit: 25, Offset: 0, SortBy: "timestamp", Order: "desc"},
			Stats:      logstore.SearchStats{TotalRequests: 1},
			HasLogs:    true,
		},
	}, stubRedactedKeysManager{}, nil)

	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("GET")
	ctx.Request.SetRequestURI("/api/logs?limit=25&offset=0")

	handler.getLogs(ctx)

	require.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode(), string(ctx.Response.Body()))

	var resp struct {
		Logs []struct {
			ID              string  `json:"id"`
			ParentRequestID *string `json:"parent_request_id"`
			GroupID         string  `json:"group_id"`
			AttemptSequence int     `json:"attempt_sequence"`
			IsFinalAttempt  bool    `json:"is_final_attempt"`
			RouteLayerIndex *int    `json:"route_layer_index"`
		} `json:"logs"`
	}
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &resp))
	require.Len(t, resp.Logs, 1)
	require.Equal(t, "req-1", resp.Logs[0].GroupID)
	require.Equal(t, 2, resp.Logs[0].AttemptSequence)
	require.True(t, resp.Logs[0].IsFinalAttempt)
	require.NotNil(t, resp.Logs[0].RouteLayerIndex)
	require.Equal(t, 1, *resp.Logs[0].RouteLayerIndex)
	require.NotNil(t, resp.Logs[0].ParentRequestID)
	require.Equal(t, "req-1", *resp.Logs[0].ParentRequestID)
}

func TestGetLogByIDIncludesRequestGroupingMetadata(t *testing.T) {
	SetLogger(&mockLogger{})

	handler := NewLoggingHandler(&stubLogManager{
		logEntry: &logstore.Log{
			ID:              "req-1-fb1",
			ParentRequestID: stringPtr("req-1"),
			GroupID:         "req-1",
			AttemptSequence: 2,
			IsFinalAttempt:  true,
			RouteLayerIndex: intPtr(1),
			Timestamp:       time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC),
			Object:          "chat_completion",
			Provider:        "azure",
			Model:           "gpt-4.1",
			FallbackIndex:   1,
			Status:          "success",
			CreatedAt:       time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC),
		},
	}, stubRedactedKeysManager{}, nil)

	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("GET")
	ctx.SetUserValue("id", "req-1-fb1")

	handler.getLogByID(ctx)

	require.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode(), string(ctx.Response.Body()))

	var resp struct {
		GroupID         string `json:"group_id"`
		AttemptSequence int    `json:"attempt_sequence"`
		IsFinalAttempt  bool   `json:"is_final_attempt"`
	}
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &resp))
	require.Equal(t, "req-1", resp.GroupID)
	require.Equal(t, 2, resp.AttemptSequence)
	require.True(t, resp.IsFinalAttempt)
}

func TestGetLogsStatsIncludesRequestSuccessFields(t *testing.T) {
	SetLogger(&mockLogger{})

	handler := NewLoggingHandler(&stubLogManager{
		stats: &logstore.SearchStats{
			TotalRequests:           5,
			CompletedAttempts:       4,
			SuccessfulAttempts:      2,
			SuccessRate:             50,
			CompletedRequestGroups:  3,
			SuccessfulRequestGroups: 2,
			RequestSuccessRate:      66.67,
			AverageFinalLatency:     83.33,
		},
	}, stubRedactedKeysManager{}, nil)

	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("GET")
	ctx.Request.SetRequestURI("/api/logs/stats")

	handler.getLogsStats(ctx)

	require.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode(), string(ctx.Response.Body()))

	var resp struct {
		CompletedAttempts      int64   `json:"completed_attempts"`
		SuccessfulAttempts     int64   `json:"successful_attempts"`
		CompletedRequestGroups int64   `json:"completed_request_groups"`
		SuccessfulRequestGroups int64  `json:"successful_request_groups"`
		RequestSuccessRate     float64 `json:"request_success_rate"`
		AverageFinalLatency    float64 `json:"average_final_latency"`
	}
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &resp))
	require.Equal(t, int64(4), resp.CompletedAttempts)
	require.Equal(t, int64(2), resp.SuccessfulAttempts)
	require.Equal(t, int64(3), resp.CompletedRequestGroups)
	require.Equal(t, int64(2), resp.SuccessfulRequestGroups)
	require.Equal(t, 66.67, resp.RequestSuccessRate)
	require.Equal(t, 83.33, resp.AverageFinalLatency)
}

func TestGetLogsFinalSuccessDistributionUsesGroupByAndFilters(t *testing.T) {
	SetLogger(&mockLogger{})

	manager := &stubLogManager{
		finalDistribution: &logstore.FinalSuccessDistributionResult{
			Dimension:         logstore.FinalSuccessDistributionByLayer,
			TotalSuccessCount: 2,
			Items: []logstore.FinalSuccessDistributionItem{
				{Value: "layer:1", Label: "Layer 2", SuccessCount: 1, SuccessRatio: 50},
				{Value: "unlayered", Label: "Unlayered", SuccessCount: 1, SuccessRatio: 50},
			},
		},
	}
	handler := NewLoggingHandler(manager, stubRedactedKeysManager{}, nil)

	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("GET")
	ctx.Request.SetRequestURI("/api/logs/final-distribution?group_by=layer&status=success&providers=azure")

	handler.getLogsFinalSuccessDistribution(ctx)

	require.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode(), string(ctx.Response.Body()))
	require.Equal(t, logstore.FinalSuccessDistributionByLayer, manager.lastGroupBy)
	require.NotNil(t, manager.lastFilters)
	require.Equal(t, []string{"success"}, manager.lastFilters.Status)
	require.Equal(t, []string{"azure"}, manager.lastFilters.Providers)

	var resp struct {
		Dimension         string `json:"dimension"`
		TotalSuccessCount int64  `json:"total_success_count"`
	}
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &resp))
	require.Equal(t, "layer", resp.Dimension)
	require.Equal(t, int64(2), resp.TotalSuccessCount)
}

func TestGetLogsFinalSuccessDistributionRejectsInvalidGroupBy(t *testing.T) {
	SetLogger(&mockLogger{})

	handler := NewLoggingHandler(&stubLogManager{}, stubRedactedKeysManager{}, nil)

	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("GET")
	ctx.Request.SetRequestURI("/api/logs/final-distribution?group_by=region")

	handler.getLogsFinalSuccessDistribution(ctx)

	require.Equal(t, fasthttp.StatusBadRequest, ctx.Response.StatusCode(), string(ctx.Response.Body()))
}

func stringPtr(value string) *string {
	return &value
}

func intPtr(value int) *int {
	return &value
}
