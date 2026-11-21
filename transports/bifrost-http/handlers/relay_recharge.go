package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/google/uuid"
	"github.com/maximhq/bifrost/framework/logstore"
	"github.com/valyala/fasthttp"
)

const (
	relayRechargeSyncInterval = time.Hour
	relayRechargeDeltaMinUSD  = 5.0
	relayRechargeMaxPages     = 20
	relayRechargePageSize     = 100
)

var (
	reTopupLogCredited = regexp.MustCompile(`(?:充值金额|amount added)[:：]\s*[＄$¥￥]?\s*([0-9]+(?:\.[0-9]+)?)`)
	reTopupLogPaid     = regexp.MustCompile(`(?:支付金额|payment amount)[:：]\s*[＄$¥￥]?\s*([0-9]+(?:\.[0-9]+)?)`)
)

type relayRechargesResponse struct {
	LastSyncAt *time.Time                     `json:"last_sync_at,omitempty"`
	Summary    *logstore.RelayRechargeSummary `json:"summary"`
	Sites      []relayRechargeSiteMeta        `json:"sites"`
	Items      []logstore.RelayRechargeRecord `json:"items"`
}

type relayRechargeSiteMeta struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Kind           string  `json:"kind"`
	RechargeURL    string  `json:"recharge_url,omitempty"`
	CNYPerSiteUnit float64 `json:"cny_per_site_unit"`
	SyncMode       string  `json:"sync_mode"` // "api_topup" | "api_log" | "balance_delta" | "unconfigured"
}

type relayRechargeSiteSyncResult struct {
	SiteID   string `json:"site_id"`
	SiteName string `json:"site_name"`
	SyncMode string `json:"sync_mode"`
	Fetched  int    `json:"fetched"`
	Inserted int    `json:"inserted"`
	Error    string `json:"error,omitempty"`
}

type relayRechargeSyncResponse struct {
	SyncedAt      time.Time                     `json:"synced_at"`
	TotalInserted int                           `json:"total_inserted"`
	Results       []relayRechargeSiteSyncResult `json:"results"`
	Data          *relayRechargesResponse       `json:"data"`
}

type upsertRelayRechargeRequest struct {
	SiteID        string  `json:"site_id"`
	RechargedAt   string  `json:"recharged_at,omitempty"`
	SiteAmountUSD float64 `json:"site_amount_usd"`
	PaidAmount    float64 `json:"paid_amount"`
	PaidCurrency  string  `json:"paid_currency,omitempty"`
	PaidCNY       float64 `json:"paid_cny"`
	PaymentMethod string  `json:"payment_method,omitempty"`
	Status        string  `json:"status,omitempty"`
	ExternalID    string  `json:"external_id,omitempty"`
	Note          string  `json:"note,omitempty"`
}

func (h *RelayBalanceHandler) maybeSyncRecharges(ctx context.Context) {
	if h == nil || h.logsStore == nil {
		return
	}
	now := h.nowFn()
	h.mu.RLock()
	last := h.lastRechargeSyncAt
	h.mu.RUnlock()
	if last != nil && now.Sub(*last) < relayRechargeSyncInterval {
		return
	}
	syncCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if _, err := h.SyncAllRecharges(syncCtx); err != nil {
		logger.Warn("relay-recharge: periodic sync finished with warning: %v", err)
	}
}

func (h *RelayBalanceHandler) determineSiteRechargeSyncMode(site logstore.RelayBalanceSiteConfig) string {
	if site.Kind == logstore.RelayBalanceKindBAIV1Balance {
		return "balance_delta"
	}
	_, tok, _ := resolveSiteNewAPICredentials(site)
	if tok == "" {
		return "unconfigured"
	}
	return "api_topup"
}

func (h *RelayBalanceHandler) buildRechargesResponse(ctx context.Context, query logstore.RelayRechargeQuery) (*relayRechargesResponse, error) {
	cfg, err := h.loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	items, summary, err := h.logsStore.ListRelayRechargeRecords(ctx, query)
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []logstore.RelayRechargeRecord{}
	}
	if summary == nil {
		summary = &logstore.RelayRechargeSummary{BySite: []logstore.RelayRechargeSiteSummary{}}
	}

	sitesMeta := make([]relayRechargeSiteMeta, 0, len(cfg.Sites))
	for _, s := range cfg.Sites {
		sitesMeta = append(sitesMeta, relayRechargeSiteMeta{
			ID:             s.ID,
			Name:           s.Name,
			Kind:           s.Kind,
			RechargeURL:    s.RechargeURL,
			CNYPerSiteUnit: s.CNYPerSiteUnit,
			SyncMode:       h.determineSiteRechargeSyncMode(s),
		})
	}

	h.mu.RLock()
	lastSync := h.lastRechargeSyncAt
	h.mu.RUnlock()

	return &relayRechargesResponse{
		LastSyncAt: lastSync,
		Summary:    summary,
		Sites:      sitesMeta,
		Items:      items,
	}, nil
}

func (h *RelayBalanceHandler) parseRechargeQuery(ctx *fasthttp.RequestCtx, timezone string) (logstore.RelayRechargeQuery, error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	q := logstore.RelayRechargeQuery{
		SiteID: strings.TrimSpace(string(ctx.QueryArgs().Peek("site_id"))),
		Limit:  200,
	}
	if rawLimit := strings.TrimSpace(string(ctx.QueryArgs().Peek("limit"))); rawLimit != "" {
		n, err := strconv.Atoi(rawLimit)
		if err != nil || n <= 0 {
			return q, fmt.Errorf("invalid limit")
		}
		q.Limit = n
	}
	if rawStart := strings.TrimSpace(string(ctx.QueryArgs().Peek("start_date"))); rawStart != "" {
		t, err := time.ParseInLocation("2006-01-02", rawStart, loc)
		if err != nil {
			return q, fmt.Errorf("invalid start_date (expected YYYY-MM-DD)")
		}
		q.StartTime = &t
	}
	if rawEnd := strings.TrimSpace(string(ctx.QueryArgs().Peek("end_date"))); rawEnd != "" {
		t, err := time.ParseInLocation("2006-01-02", rawEnd, loc)
		if err != nil {
			return q, fmt.Errorf("invalid end_date (expected YYYY-MM-DD)")
		}
		endOfDay := t.Add(24*time.Hour - time.Nanosecond)
		q.EndTime = &endOfDay
	}
	return q, nil
}

func (h *RelayBalanceHandler) listRelayRecharges(ctx *fasthttp.RequestCtx) {
	if h.logsStore == nil {
		SendError(ctx, fasthttp.StatusServiceUnavailable, "Log store is not configured")
		return
	}
	reqCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cfg, err := h.loadConfig(reqCtx)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, fmt.Sprintf("Failed to load config: %v", err))
		return
	}
	q, err := h.parseRechargeQuery(ctx, cfg.Timezone)
	if err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	resp, err := h.buildRechargesResponse(reqCtx, q)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, fmt.Sprintf("Failed to load relay recharge records: %v", err))
		return
	}
	SendJSON(ctx, resp)
}

func (h *RelayBalanceHandler) syncRelayRecharges(ctx *fasthttp.RequestCtx) {
	if h.logsStore == nil {
		SendError(ctx, fasthttp.StatusServiceUnavailable, "Log store is not configured")
		return
	}
	reqCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	res, err := h.SyncAllRecharges(reqCtx)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, fmt.Sprintf("Failed to sync relay recharge records: %v", err))
		return
	}
	SendJSON(ctx, res)
}

func (h *RelayBalanceHandler) createRelayRecharge(ctx *fasthttp.RequestCtx) {
	if h.logsStore == nil {
		SendError(ctx, fasthttp.StatusServiceUnavailable, "Log store is not configured")
		return
	}
	reqCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var req upsertRelayRechargeRequest
	if err := sonic.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err))
		return
	}
	cfg, err := h.loadConfig(reqCtx)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, fmt.Sprintf("Failed to load config: %v", err))
		return
	}
	siteID := strings.ToLower(strings.TrimSpace(req.SiteID))
	siteName := siteID
	var matchedSite *logstore.RelayBalanceSiteConfig
	for i := range cfg.Sites {
		if cfg.Sites[i].ID == siteID {
			matchedSite = &cfg.Sites[i]
			siteName = cfg.Sites[i].Name
			break
		}
	}
	if siteID == "" {
		SendError(ctx, fasthttp.StatusBadRequest, "site_id is required")
		return
	}

	rechargedAt := h.nowFn()
	if strings.TrimSpace(req.RechargedAt) != "" {
		parsed, err := parseFlexibleTime(req.RechargedAt, cfg.Timezone)
		if err != nil {
			SendError(ctx, fasthttp.StatusBadRequest, fmt.Sprintf("invalid recharged_at: %v", err))
			return
		}
		rechargedAt = parsed
	}

	paidCurrency := strings.ToUpper(strings.TrimSpace(req.PaidCurrency))
	if paidCurrency == "" {
		paidCurrency = "CNY"
	}
	paidCNY := req.PaidCNY
	paidAmount := req.PaidAmount
	if paidCNY <= 0 && paidAmount > 0 {
		if paidCurrency == "USD" {
			paidCNY = paidAmount * relayBalanceUSDToCNYRate
		} else {
			paidCNY = paidAmount
		}
	}
	if paidAmount <= 0 && paidCNY > 0 {
		if paidCurrency == "USD" {
			paidAmount = paidCNY / relayBalanceUSDToCNYRate
		} else {
			paidAmount = paidCNY
		}
	}
	siteUSD := req.SiteAmountUSD
	if siteUSD <= 0 && paidCNY > 0 && matchedSite != nil && matchedSite.CNYPerSiteUnit > 0 {
		siteUSD = paidCNY / matchedSite.CNYPerSiteUnit
	}
	if paidCNY <= 0 && siteUSD <= 0 {
		SendError(ctx, fasthttp.StatusBadRequest, "paid_cny or site_amount_usd must be positive")
		return
	}

	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = logstore.RelayRechargeStatusSuccess
	}
	method := strings.TrimSpace(req.PaymentMethod)
	if method == "" {
		method = "manual"
	}

	rec := &logstore.RelayRechargeRecord{
		ID:            "manual:" + uuid.NewString(),
		SiteID:        siteID,
		SiteName:      siteName,
		Source:        logstore.RelayRechargeSourceManual,
		ExternalID:    strings.TrimSpace(req.ExternalID),
		RechargedAt:   rechargedAt,
		SiteAmountUSD: siteUSD,
		PaidAmount:    paidAmount,
		PaidCurrency:  paidCurrency,
		PaidCNY:       paidCNY,
		PaymentMethod: method,
		Status:        status,
		Note:          strings.TrimSpace(req.Note),
	}
	if err := h.logsStore.SaveRelayRechargeRecord(reqCtx, rec); err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, fmt.Sprintf("Failed to save recharge record: %v", err))
		return
	}
	SendJSON(ctx, rec)
}

func (h *RelayBalanceHandler) updateRelayRecharge(ctx *fasthttp.RequestCtx) {
	if h.logsStore == nil {
		SendError(ctx, fasthttp.StatusServiceUnavailable, "Log store is not configured")
		return
	}
	reqCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	rawID, _ := ctx.UserValue("id").(string)
	id, err := url.PathUnescape(rawID)
	if err != nil || strings.TrimSpace(id) == "" {
		SendError(ctx, fasthttp.StatusBadRequest, "invalid recharge record id")
		return
	}
	existing, err := h.logsStore.GetRelayRechargeRecord(reqCtx, id)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, fmt.Sprintf("Failed to read recharge record: %v", err))
		return
	}
	if existing == nil {
		SendError(ctx, fasthttp.StatusNotFound, "recharge record not found")
		return
	}

	var req upsertRelayRechargeRequest
	if err := sonic.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err))
		return
	}
	cfg, err := h.loadConfig(reqCtx)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, fmt.Sprintf("Failed to load config: %v", err))
		return
	}
	if strings.TrimSpace(req.RechargedAt) != "" {
		parsed, err := parseFlexibleTime(req.RechargedAt, cfg.Timezone)
		if err != nil {
			SendError(ctx, fasthttp.StatusBadRequest, fmt.Sprintf("invalid recharged_at: %v", err))
			return
		}
		existing.RechargedAt = parsed
	}
	if req.SiteAmountUSD > 0 {
		existing.SiteAmountUSD = req.SiteAmountUSD
	}
	if strings.TrimSpace(req.PaidCurrency) != "" {
		existing.PaidCurrency = strings.ToUpper(strings.TrimSpace(req.PaidCurrency))
	}
	if req.PaidCNY > 0 {
		existing.PaidCNY = req.PaidCNY
		if req.PaidAmount > 0 {
			existing.PaidAmount = req.PaidAmount
		} else if existing.PaidCurrency == "CNY" {
			existing.PaidAmount = req.PaidCNY
		}
	} else if req.PaidAmount > 0 {
		existing.PaidAmount = req.PaidAmount
	}
	if strings.TrimSpace(req.PaymentMethod) != "" {
		existing.PaymentMethod = strings.TrimSpace(req.PaymentMethod)
	}
	if strings.TrimSpace(req.Status) != "" {
		existing.Status = strings.TrimSpace(req.Status)
	} else {
		// Saving an edit confirms any pending_confirm auto-detected record by default.
		existing.Status = logstore.RelayRechargeStatusSuccess
	}
	existing.Note = strings.TrimSpace(req.Note)
	if strings.TrimSpace(req.ExternalID) != "" {
		existing.ExternalID = strings.TrimSpace(req.ExternalID)
	}

	if err := h.logsStore.SaveRelayRechargeRecord(reqCtx, existing); err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, fmt.Sprintf("Failed to update recharge record: %v", err))
		return
	}
	SendJSON(ctx, existing)
}

func (h *RelayBalanceHandler) deleteRelayRecharge(ctx *fasthttp.RequestCtx) {
	if h.logsStore == nil {
		SendError(ctx, fasthttp.StatusServiceUnavailable, "Log store is not configured")
		return
	}
	reqCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	rawID, _ := ctx.UserValue("id").(string)
	id, err := url.PathUnescape(rawID)
	if err != nil || strings.TrimSpace(id) == "" {
		SendError(ctx, fasthttp.StatusBadRequest, "invalid recharge record id")
		return
	}
	if err := h.logsStore.DeleteRelayRechargeRecord(reqCtx, id); err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, fmt.Sprintf("Failed to delete recharge record: %v", err))
		return
	}
	SendJSON(ctx, map[string]any{"success": true, "id": id})
}

// SyncAllRecharges pulls official recharge history from StoreAPI, PoloAPI, and OpenLux.
func (h *RelayBalanceHandler) SyncAllRecharges(ctx context.Context) (*relayRechargeSyncResponse, error) {
	h.rechargeSyncMu.Lock()
	defer h.rechargeSyncMu.Unlock()

	cfg, err := h.loadConfig(ctx)
	if err != nil {
		return nil, err
	}

	now := h.nowFn()
	results := make([]relayRechargeSiteSyncResult, 0, len(cfg.Sites))
	totalInserted := 0

	for _, site := range cfg.Sites {
		if !site.Enabled {
			continue
		}
		mode := h.determineSiteRechargeSyncMode(site)
		res := relayRechargeSiteSyncResult{
			SiteID:   site.ID,
			SiteName: site.Name,
			SyncMode: mode,
		}
		switch mode {
		case "api_topup":
			uid, tok, _ := resolveSiteNewAPICredentials(site)
			var recs []logstore.RelayRechargeRecord
			var syncErr error
			if site.ID == "openlux" {
				recs, syncErr = h.fetchOpenLuxTopupRecords(ctx, site, uid, tok)
			} else {
				recs, syncErr = h.fetchNewAPITopupSelfRecords(ctx, site, uid, tok)
				if syncErr == nil {
					// Also merge any earlier /api/log/self?type=1 top-up logs not covered by the newer orders table
					// (e.g. PoloAPI pre-migration orders or redemption codes), deduplicating by timestamp+amount.
					if logRecs, logErr := h.fetchNewAPILogTopupRecords(ctx, site, uid, tok, now); logErr == nil && len(logRecs) > 0 {
						recs = mergeOrderAndLogRecharges(recs, logRecs)
					}
				}
			}
			if syncErr != nil {
				res.Error = truncateStr(syncErr.Error(), 240)
			} else {
				res.Fetched = len(recs)
				if inserted, err := h.logsStore.UpsertRelayRechargeRecords(ctx, recs); err != nil {
					res.Error = truncateStr(err.Error(), 240)
				} else {
					res.Inserted = inserted
					totalInserted += inserted
				}
			}
		case "balance_delta":
			// B.AI has no official fiat recharge history list API; captured automatically on positive balance jumps in PollAll.
		case "unconfigured":
			res.Error = "未配置只读系统访问令牌"
		}
		results = append(results, res)
	}

	syncedAt := now
	h.mu.Lock()
	h.lastRechargeSyncAt = &syncedAt
	h.mu.Unlock()

	data, err := h.buildRechargesResponse(ctx, logstore.RelayRechargeQuery{Limit: 200})
	if err != nil {
		return nil, err
	}
	return &relayRechargeSyncResponse{
		SyncedAt:      syncedAt,
		TotalInserted: totalInserted,
		Results:       results,
		Data:          data,
	}, nil
}

func (h *RelayBalanceHandler) fetchNewAPITopupSelfRecords(
	ctx context.Context,
	site logstore.RelayBalanceSiteConfig,
	userID, accessToken string,
) ([]logstore.RelayRechargeRecord, error) {
	headers := map[string]string{
		"Authorization": "Bearer " + accessToken,
	}
	if userID != "" {
		headers["New-Api-User"] = userID
	}

	var records []logstore.RelayRechargeRecord
	base := strings.TrimRight(site.BaseURL, "/")
	for page := 1; page <= relayRechargeMaxPages; page++ {
		endpoint := fmt.Sprintf("%s/api/user/topup/self?page=%d&page_size=%d", base, page, relayRechargePageSize)
		body, status, err := h.doJSONGet(ctx, endpoint, headers)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("HTTP %d from %s /api/user/topup/self", status, site.Name)
		}
		var parsed struct {
			Success bool   `json:"success"`
			Message string `json:"message"`
			Data    struct {
				Total int `json:"total"`
				Items []struct {
					ID              int64   `json:"id"`
					Amount          float64 `json:"amount"`
					Money           float64 `json:"money"`
					TradeNo         string  `json:"trade_no"`
					PaymentMethod   string  `json:"payment_method"`
					PaymentProvider string  `json:"payment_provider"`
					CreateTime      int64   `json:"create_time"`
					CompleteTime    int64   `json:"complete_time"`
					Status          string  `json:"status"`
				} `json:"items"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("invalid %s topup/self response: %w", site.Name, err)
		}
		if !parsed.Success {
			return nil, fmt.Errorf("%s topup/self failed: %s", site.Name, parsed.Message)
		}
		if len(parsed.Data.Items) == 0 {
			break
		}
		for _, it := range parsed.Data.Items {
			if !strings.EqualFold(strings.TrimSpace(it.Status), "success") {
				continue
			}
			ts := it.CompleteTime
			if ts <= 0 {
				ts = it.CreateTime
			}
			rechargedAt := time.Unix(ts, 0).UTC()
			extID := strings.TrimSpace(it.TradeNo)
			recID := fmt.Sprintf("%s:topup:%s", site.ID, extID)
			if extID == "" {
				recID = fmt.Sprintf("%s:topup:id-%d", site.ID, it.ID)
			}
			paidCNY := it.Money
			if paidCNY <= 0 {
				paidCNY = it.Amount * site.CNYPerSiteUnit
			}
			records = append(records, logstore.RelayRechargeRecord{
				ID:            recID,
				SiteID:        site.ID,
				SiteName:      site.Name,
				Source:        logstore.RelayRechargeSourceAPISync,
				ExternalID:    extID,
				RechargedAt:   rechargedAt,
				SiteAmountUSD: it.Amount,
				PaidAmount:    paidCNY,
				PaidCurrency:  "CNY",
				PaidCNY:       paidCNY,
				PaymentMethod: strings.TrimSpace(it.PaymentMethod),
				Status:        logstore.RelayRechargeStatusSuccess,
			})
		}
		if len(parsed.Data.Items) < relayRechargePageSize || page*relayRechargePageSize >= parsed.Data.Total {
			break
		}
	}
	return records, nil
}

func (h *RelayBalanceHandler) fetchOpenLuxTopupRecords(
	ctx context.Context,
	site logstore.RelayBalanceSiteConfig,
	userID, accessToken string,
) ([]logstore.RelayRechargeRecord, error) {
	headers := map[string]string{
		"Authorization": "Bearer " + accessToken,
	}
	if userID != "" {
		headers["New-Api-User"] = userID
	}

	var records []logstore.RelayRechargeRecord
	base := strings.TrimRight(site.BaseURL, "/")
	for page := 1; page <= relayRechargeMaxPages; page++ {
		endpoint := fmt.Sprintf("%s/api/user/topuprecords?page=%d&page_size=%d", base, page, relayRechargePageSize)
		body, status, err := h.doJSONGet(ctx, endpoint, headers)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("HTTP %d from %s /api/user/topuprecords", status, site.Name)
		}
		var parsed struct {
			Success bool   `json:"success"`
			Message string `json:"message"`
			Data    struct {
				Total   int `json:"total"`
				Records []struct {
					ID            int64   `json:"id"`
					Amount        float64 `json:"amount"`
					Money         float64 `json:"money"`
					TradeNo       string  `json:"trade_no"`
					PaymentMethod string  `json:"payment_method"`
					CreateTime    int64   `json:"create_time"`
					CompleteTime  int64   `json:"complete_time"`
					Status        string  `json:"status"`
				} `json:"records"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("invalid %s topuprecords response: %w", site.Name, err)
		}
		if !parsed.Success && parsed.Message != "" {
			return nil, fmt.Errorf("%s topuprecords failed: %s", site.Name, parsed.Message)
		}
		if len(parsed.Data.Records) == 0 {
			break
		}
		for _, it := range parsed.Data.Records {
			if !strings.EqualFold(strings.TrimSpace(it.Status), "success") {
				continue
			}
			ts := it.CompleteTime
			if ts <= 0 {
				ts = it.CreateTime
			}
			extID := strings.TrimSpace(it.TradeNo)
			recID := fmt.Sprintf("%s:topup:%s", site.ID, extID)
			if extID == "" {
				recID = fmt.Sprintf("%s:topup:id-%d", site.ID, it.ID)
			}
			paidUSD := it.Money
			paidCNY := 0.0
			if paidUSD > 0 {
				paidCNY = paidUSD * relayBalanceUSDToCNYRate
			} else {
				paidCNY = it.Amount * site.CNYPerSiteUnit
				paidUSD = paidCNY / relayBalanceUSDToCNYRate
			}
			records = append(records, logstore.RelayRechargeRecord{
				ID:            recID,
				SiteID:        site.ID,
				SiteName:      site.Name,
				Source:        logstore.RelayRechargeSourceAPISync,
				ExternalID:    extID,
				RechargedAt:   time.Unix(ts, 0).UTC(),
				SiteAmountUSD: it.Amount,
				PaidAmount:    paidUSD,
				PaidCurrency:  "USD",
				PaidCNY:       paidCNY,
				PaymentMethod: strings.TrimSpace(it.PaymentMethod),
				Status:        logstore.RelayRechargeStatusSuccess,
			})
		}
		if len(parsed.Data.Records) < relayRechargePageSize || page*relayRechargePageSize >= parsed.Data.Total {
			break
		}
	}
	return records, nil
}

// mergeOrderAndLogRecharges combines structured top-up orders with older log-only records,
// skipping log rows that correspond to an already-synced order (same site within ±120s and ±0.05 USD).
func mergeOrderAndLogRecharges(orders, logs []logstore.RelayRechargeRecord) []logstore.RelayRechargeRecord {
	merged := append([]logstore.RelayRechargeRecord(nil), orders...)
	for _, lr := range logs {
		matched := false
		for _, ord := range orders {
			diffSec := math.Abs(lr.RechargedAt.Sub(ord.RechargedAt).Seconds())
			diffAmt := math.Abs(lr.SiteAmountUSD - ord.SiteAmountUSD)
			if diffSec <= 120 && diffAmt <= 0.05 {
				matched = true
				break
			}
		}
		if !matched {
			merged = append(merged, lr)
		}
	}
	return merged
}

func (h *RelayBalanceHandler) fetchNewAPILogTopupRecords(
	ctx context.Context,
	site logstore.RelayBalanceSiteConfig,
	userID, accessToken string,
	now time.Time,
) ([]logstore.RelayRechargeRecord, error) {
	headers := map[string]string{
		"Authorization": "Bearer " + accessToken,
	}
	if userID != "" {
		headers["New-Api-User"] = userID
	}

	startTS := now.Add(-365 * 24 * time.Hour).Unix()
	endTS := now.Add(time.Hour).Unix()
	qpu := site.QuotaPerUnit
	if qpu <= 0 {
		qpu = 500000
	}

	var records []logstore.RelayRechargeRecord
	base := strings.TrimRight(site.BaseURL, "/")
	for page := 1; page <= relayRechargeMaxPages; page++ {
		endpoint := fmt.Sprintf(
			"%s/api/log/self?type=1&p=%d&page_size=%d&start_timestamp=%d&end_timestamp=%d",
			base, page, relayRechargePageSize, startTS, endTS,
		)
		body, status, err := h.doJSONGet(ctx, endpoint, headers)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("HTTP %d from %s /api/log/self", status, site.Name)
		}
		var parsed struct {
			Success bool   `json:"success"`
			Message string `json:"message"`
			Data    struct {
				Total int `json:"total"`
				Items []struct {
					ID        int64   `json:"id"`
					CreatedAt int64   `json:"created_at"`
					Type      int     `json:"type"`
					Content   string  `json:"content"`
					Quota     float64 `json:"quota"`
				} `json:"items"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("invalid %s log/self response: %w", site.Name, err)
		}
		if !parsed.Success {
			return nil, fmt.Errorf("%s log/self failed: %s", site.Name, parsed.Message)
		}
		if len(parsed.Data.Items) == 0 {
			break
		}
		for _, it := range parsed.Data.Items {
			creditedUSD, paidVal := parseOpenLuxTopupContent(it.Content)
			if creditedUSD <= 0 && it.Quota > 0 {
				creditedUSD = it.Quota / qpu
			}
			if creditedUSD <= 0 && paidVal <= 0 {
				continue
			}
			paidCurrency := "USD"
			paidAmount := paidVal
			paidCNY := 0.0
			if site.CNYPerSiteUnit <= 2.0 {
				paidCurrency = "CNY"
				if paidAmount <= 0 {
					paidAmount = creditedUSD * site.CNYPerSiteUnit
				}
				paidCNY = paidAmount
			} else {
				if paidAmount > 0 {
					paidCNY = paidAmount * relayBalanceUSDToCNYRate
				} else {
					paidAmount = creditedUSD * (site.CNYPerSiteUnit / relayBalanceUSDToCNYRate)
					paidCNY = creditedUSD * site.CNYPerSiteUnit
				}
			}
			records = append(records, logstore.RelayRechargeRecord{
				ID:            fmt.Sprintf("%s:log:%d-%d", site.ID, it.CreatedAt, int64(math.Round(creditedUSD*100))),
				SiteID:        site.ID,
				SiteName:      site.Name,
				Source:        logstore.RelayRechargeSourceAPISync,
				ExternalID:    "",
				RechargedAt:   time.Unix(it.CreatedAt, 0).UTC(),
				SiteAmountUSD: creditedUSD,
				PaidAmount:    paidAmount,
				PaidCurrency:  paidCurrency,
				PaidCNY:       paidCNY,
				PaymentMethod: "online",
				Status:        logstore.RelayRechargeStatusSuccess,
				Note:          truncateStr(it.Content, 240),
			})
		}
		if len(parsed.Data.Items) < relayRechargePageSize || page*relayRechargePageSize >= parsed.Data.Total {
			break
		}
	}
	return records, nil
}

func parseOpenLuxTopupContent(content string) (creditedUSD float64, paidAmount float64) {
	if m := reTopupLogCredited.FindStringSubmatch(content); len(m) == 2 {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			creditedUSD = v
		}
	}
	if m := reTopupLogPaid.FindStringSubmatch(content); len(m) == 2 {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			paidAmount = v
		}
	}
	return creditedUSD, paidAmount
}

func (h *RelayBalanceHandler) recordBalanceDeltaRecharge(
	ctx context.Context,
	cfg *logstore.RelayBalanceConfig,
	site logstore.RelayBalanceSiteConfig,
	prevUSD, currUSD float64,
	now time.Time,
	barkURL string,
) {
	if h.logsStore == nil {
		return
	}
	deltaUSD := currUSD - prevUSD
	if deltaUSD < relayRechargeDeltaMinUSD {
		return
	}
	paidCNY := deltaUSD * site.CNYPerSiteUnit
	rec := logstore.RelayRechargeRecord{
		ID:            fmt.Sprintf("%s:delta:%d", site.ID, now.Unix()),
		SiteID:        site.ID,
		SiteName:      site.Name,
		Source:        logstore.RelayRechargeSourceBalanceDelta,
		RechargedAt:   now,
		SiteAmountUSD: deltaUSD,
		PaidAmount:    deltaUSD,
		PaidCurrency:  "USD",
		PaidCNY:       paidCNY,
		PaymentMethod: "auto_detected",
		Status:        logstore.RelayRechargeStatusPendingConfirm,
		Note: fmt.Sprintf(
			"自动检测到余额增加 +$%.2f（$%.2f → $%.2f），请按实际账单核对含手续费的实付人民币",
			deltaUSD, prevUSD, currUSD,
		),
	}
	inserted, err := h.logsStore.UpsertRelayRechargeRecords(ctx, []logstore.RelayRechargeRecord{rec})
	if err != nil {
		logger.Warn("relay-recharge: failed to save balance delta recharge for %s: %v", site.ID, err)
		return
	}
	if inserted > 0 && cfg != nil && cfg.BarkEnabled && barkURL != "" {
		title := fmt.Sprintf("✅ 检测到 %s 充值到账 +$%.2f", site.Name, deltaUSD)
		body := fmt.Sprintf(
			"站内余额：$%.2f → $%.2f\n估算金额：约 ¥%.2f（未含支付手续费）\n已自动记入充值记录（待核对），可在后台修正实付金额。",
			prevUSD, currUSD, paidCNY,
		)
		_ = h.sendBark(ctx, barkURL, cfg.BarkGroup, title, body, "active", "")
	}
}

func parseFlexibleTime(raw, timezone string) (time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if layout == time.RFC3339 {
			if t, err := time.Parse(layout, trimmed); err == nil {
				return t, nil
			}
			continue
		}
		if t, err := time.ParseInLocation(layout, trimmed, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported datetime format %q", raw)
}
