package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/logstore"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

const (
	relayBalanceUSDToCNYRate     = 6.8
	relayBalanceRecoveryRatio    = 1.10
	relayBalanceManualDebounce   = 15 * time.Second
	relayBalanceHTTPTimeout      = 10 * time.Second
	relayBalanceBarkEnvVar       = "BIFROST_BARK_URL"
	relayBalanceAlertTierLow     = "low"
	relayBalanceAlertTierUrgent  = "urgent"
	relayBalanceAlertTierError   = "error"
	relayBalanceConsecErrAlertAt = 3
)

// RelayBalanceHandler periodically checks upstream relay site balances, caches snapshots in DB,
// exposes balance/settings HTTP endpoints, and dispatches rate-limited Bark alerts on low balance.
type RelayBalanceHandler struct {
	configStore configstore.ConfigStore
	logsStore   logstore.LogStore
	httpClient  *http.Client
	nowFn       func() time.Time

	mu                 sync.RWMutex
	pollMu             sync.Mutex
	rechargeSyncMu     sync.Mutex
	lastManualPoll     time.Time
	lastPollAt         *time.Time
	nextPollAt         *time.Time
	lastRechargeSyncAt *time.Time
	started            bool
	baseCtx            context.Context
}

func NewRelayBalanceHandler(configStore configstore.ConfigStore, logsStore logstore.LogStore) *RelayBalanceHandler {
	return &RelayBalanceHandler{
		configStore: configStore,
		logsStore:   logsStore,
		httpClient: &http.Client{
			Timeout: relayBalanceHTTPTimeout,
		},
		nowFn: time.Now,
	}
}

func (h *RelayBalanceHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	r.GET("/api/relay-balances", lib.ChainMiddlewares(h.getRelayBalances, middlewares...))
	r.POST("/api/relay-balances/refresh", lib.ChainMiddlewares(h.refreshRelayBalances, middlewares...))
	r.GET("/api/relay-balances/settings", lib.ChainMiddlewares(h.getRelayBalanceSettings, middlewares...))
	r.PUT("/api/relay-balances/settings", lib.ChainMiddlewares(h.updateRelayBalanceSettings, middlewares...))
	r.POST("/api/relay-balances/test-bark", lib.ChainMiddlewares(h.testBarkNotification, middlewares...))
	r.GET("/api/relay-recharges", lib.ChainMiddlewares(h.listRelayRecharges, middlewares...))
	r.POST("/api/relay-recharges/sync", lib.ChainMiddlewares(h.syncRelayRecharges, middlewares...))
	r.POST("/api/relay-recharges", lib.ChainMiddlewares(h.createRelayRecharge, middlewares...))
	r.PUT("/api/relay-recharges/{id}", lib.ChainMiddlewares(h.updateRelayRecharge, middlewares...))
	r.DELETE("/api/relay-recharges/{id}", lib.ChainMiddlewares(h.deleteRelayRecharge, middlewares...))
}

// Start launches the background polling goroutine tied to serverCtx.
func (h *RelayBalanceHandler) Start(serverCtx context.Context) {
	if h == nil || h.logsStore == nil {
		return
	}
	h.mu.Lock()
	if h.started {
		h.mu.Unlock()
		return
	}
	h.started = true
	h.baseCtx = serverCtx
	h.mu.Unlock()

	go func() {
		// Brief delay after boot so provider catalog finishes initializing cleanly.
		select {
		case <-serverCtx.Done():
			return
		case <-time.After(5 * time.Second):
		}

		for {
			cfg, err := h.loadConfig(serverCtx)
			if err != nil {
				logger.Warn("relay-balance: failed to load config: %v", err)
				cfg = logstore.DefaultRelayBalanceConfig()
			}
			interval := time.Duration(cfg.PollIntervalSeconds) * time.Second
			if interval < time.Minute {
				interval = 10 * time.Minute
			}

			if cfg.Enabled {
				if _, err := h.PollAll(serverCtx, true); err != nil {
					logger.Warn("relay-balance: poll cycle completed with error: %v", err)
				}
				h.maybeSyncRecharges(serverCtx)
			}

			next := h.nowFn().Add(interval)
			h.mu.Lock()
			h.nextPollAt = &next
			h.mu.Unlock()

			timer := time.NewTimer(interval)
			select {
			case <-serverCtx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
}

// --- API DTOs ---

type relayBalanceSiteSnapshot struct {
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	Kind               string     `json:"kind"`
	BaseURL            string     `json:"base_url"`
	RechargeURL        string     `json:"recharge_url,omitempty"`
	ProviderPrefix     string     `json:"provider_prefix"`
	CNYPerSiteUnit     float64    `json:"cny_per_site_unit"`
	ThresholdCNY       float64    `json:"threshold_cny"`
	UrgentThresholdCNY float64    `json:"urgent_threshold_cny"`
	Enabled            bool       `json:"enabled"`
	Status             string     `json:"status"`
	SiteBalanceUSD     *float64   `json:"site_balance_usd,omitempty"`
	BalanceCNY         *float64   `json:"balance_cny,omitempty"`
	SiteUsedUSD        *float64   `json:"site_used_usd,omitempty"`
	Spend24hCNY        float64    `json:"spend_24h_cny"`
	EstimatedDaysLeft  *float64   `json:"estimated_days_left,omitempty"`
	KeySource          string     `json:"key_source,omitempty"`
	LastCheckedAt      *time.Time `json:"last_checked_at,omitempty"`
	LastSuccessAt      *time.Time `json:"last_success_at,omitempty"`
	LastError          string     `json:"last_error,omitempty"`
	ConsecutiveErrors  int        `json:"consecutive_errors"`
	AlertActive        bool       `json:"alert_active"`
	LastAlertTier      string     `json:"last_alert_tier,omitempty"`
	LastAlertAt        *time.Time `json:"last_alert_at,omitempty"`
	PendingAfterQuiet  bool       `json:"pending_after_quiet"`
	NeedsAccessToken   bool       `json:"needs_access_token"`
	HasAccessToken     bool       `json:"has_access_token"`
	UserID             string     `json:"user_id,omitempty"`
}

type relayBalancesResponse struct {
	Enabled             bool                       `json:"enabled"`
	BarkEnabled         bool                       `json:"bark_enabled"`
	BarkConfigured      bool                       `json:"bark_configured"`
	PollIntervalSeconds int                        `json:"poll_interval_seconds"`
	TotalBalanceCNY     float64                    `json:"total_balance_cny"`
	LowCount            int                        `json:"low_count"`
	UrgentCount         int                        `json:"urgent_count"`
	ErrorCount          int                        `json:"error_count"`
	LastPollAt          *time.Time                 `json:"last_poll_at,omitempty"`
	NextPollAt          *time.Time                 `json:"next_poll_at,omitempty"`
	Sites               []relayBalanceSiteSnapshot `json:"sites"`
}

type relayBalanceSiteSettingsView struct {
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
	HasAccessToken     bool    `json:"has_access_token"`
	AccessTokenSource  string  `json:"access_token_source,omitempty"`
	AccessTokenMasked  string  `json:"access_token_masked,omitempty"`
}

type relayBalanceSettingsView struct {
	Enabled             bool                           `json:"enabled"`
	PollIntervalSeconds int                            `json:"poll_interval_seconds"`
	Timezone            string                         `json:"timezone"`
	BarkEnabled         bool                           `json:"bark_enabled"`
	BarkGroup           string                         `json:"bark_group"`
	BarkConfigured      bool                           `json:"bark_configured"`
	BarkSource          string                         `json:"bark_source,omitempty"`
	BarkURLMasked       string                         `json:"bark_url_masked,omitempty"`
	CooldownHours       int                            `json:"cooldown_hours"`
	UrgentCooldownHours int                            `json:"urgent_cooldown_hours"`
	QuietHoursEnabled   bool                           `json:"quiet_hours_enabled"`
	QuietStartHour      int                            `json:"quiet_start_hour"`
	QuietEndHour        int                            `json:"quiet_end_hour"`
	Sites               []relayBalanceSiteSettingsView `json:"sites"`
}

type updateRelayBalanceSiteInput struct {
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
	ClearAccessToken   bool    `json:"clear_access_token,omitempty"`
}

type updateRelayBalanceSettingsRequest struct {
	Enabled             bool                          `json:"enabled"`
	PollIntervalSeconds int                           `json:"poll_interval_seconds"`
	Timezone            string                        `json:"timezone"`
	BarkEnabled         bool                          `json:"bark_enabled"`
	BarkGroup           string                        `json:"bark_group"`
	BarkURL             string                        `json:"bark_url,omitempty"`
	ClearBarkURL        bool                          `json:"clear_bark_url,omitempty"`
	CooldownHours       int                           `json:"cooldown_hours"`
	UrgentCooldownHours int                           `json:"urgent_cooldown_hours"`
	QuietHoursEnabled   bool                          `json:"quiet_hours_enabled"`
	QuietStartHour      int                           `json:"quiet_start_hour"`
	QuietEndHour        int                           `json:"quiet_end_hour"`
	Sites               []updateRelayBalanceSiteInput `json:"sites"`
}

// --- Secret / Env Resolution Helpers ---

func envKeyForSite(siteID, suffix string) string {
	clean := strings.ToUpper(strings.TrimSpace(siteID))
	clean = strings.ReplaceAll(clean, "-", "_")
	return fmt.Sprintf("BIFROST_BALANCE_%s_%s", clean, suffix)
}

func resolveBarkURL(cfg *logstore.RelayBalanceConfig) (url string, source string) {
	if cfg != nil && strings.TrimSpace(cfg.BarkURL) != "" {
		return strings.TrimRight(strings.TrimSpace(cfg.BarkURL), "/"), "db"
	}
	if env := strings.TrimRight(strings.TrimSpace(os.Getenv(relayBalanceBarkEnvVar)), "/"); env != "" {
		return env, "env"
	}
	return "", ""
}

func resolveSiteNewAPICredentials(site logstore.RelayBalanceSiteConfig) (userID, token, tokenSource string) {
	userID = strings.TrimSpace(site.UserID)
	if userID == "" {
		userID = strings.TrimSpace(os.Getenv(envKeyForSite(site.ID, "USER_ID")))
	}
	if t := strings.TrimSpace(site.AccessToken); t != "" {
		return userID, t, "db"
	}
	if t := strings.TrimSpace(os.Getenv(envKeyForSite(site.ID, "ACCESS_TOKEN"))); t != "" {
		return userID, t, "env"
	}
	return userID, "", ""
}

func maskSecret(secret string) string {
	s := strings.TrimSpace(secret)
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "****" + s[len(s)-4:]
}

func maskBarkURL(raw string) string {
	u := strings.TrimRight(strings.TrimSpace(raw), "/")
	if u == "" {
		return ""
	}
	idx := strings.LastIndex(u, "/")
	if idx < 0 || idx >= len(u)-1 {
		return maskSecret(u)
	}
	return u[:idx+1] + maskSecret(u[idx+1:])
}

// --- HTTP Handlers ---

func (h *RelayBalanceHandler) loadConfig(ctx context.Context) (*logstore.RelayBalanceConfig, error) {
	if h.logsStore == nil {
		return logstore.DefaultRelayBalanceConfig(), nil
	}
	return h.logsStore.GetRelayBalanceConfig(ctx)
}

func (h *RelayBalanceHandler) buildBalancesResponse(ctx context.Context) (*relayBalancesResponse, error) {
	cfg, err := h.loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	var states []logstore.RelayBalanceState
	if h.logsStore != nil {
		states, err = h.logsStore.ListRelayBalanceStates(ctx)
		if err != nil {
			return nil, err
		}
	}
	stateByID := make(map[string]logstore.RelayBalanceState, len(states))
	for _, st := range states {
		stateByID[st.SiteID] = st
	}

	barkURL, _ := resolveBarkURL(cfg)
	h.mu.RLock()
	lastPollAt := h.lastPollAt
	nextPollAt := h.nextPollAt
	h.mu.RUnlock()

	resp := &relayBalancesResponse{
		Enabled:             cfg.Enabled,
		BarkEnabled:         cfg.BarkEnabled,
		BarkConfigured:      barkURL != "",
		PollIntervalSeconds: cfg.PollIntervalSeconds,
		LastPollAt:          lastPollAt,
		NextPollAt:          nextPollAt,
		Sites:               make([]relayBalanceSiteSnapshot, 0, len(cfg.Sites)),
	}

	for _, site := range cfg.Sites {
		st, hasState := stateByID[site.ID]
		uid, tok, _ := resolveSiteNewAPICredentials(site)
		snap := relayBalanceSiteSnapshot{
			ID:                 site.ID,
			Name:               site.Name,
			Kind:               site.Kind,
			BaseURL:            site.BaseURL,
			RechargeURL:        site.RechargeURL,
			ProviderPrefix:     site.ProviderPrefix,
			CNYPerSiteUnit:     site.CNYPerSiteUnit,
			ThresholdCNY:       site.ThresholdCNY,
			UrgentThresholdCNY: site.UrgentThresholdCNY,
			Enabled:            site.Enabled,
			NeedsAccessToken:   site.Kind == logstore.RelayBalanceKindNewAPIUserSelf,
			HasAccessToken:     tok != "",
			UserID:             uid,
		}
		if hasState {
			snap.Status = st.Status
			snap.SiteBalanceUSD = st.SiteBalanceUSD
			snap.BalanceCNY = st.BalanceCNY
			snap.SiteUsedUSD = st.SiteUsedUSD
			snap.Spend24hCNY = st.Spend24hCNY
			snap.EstimatedDaysLeft = st.EstimatedDaysLeft
			snap.KeySource = st.KeySource
			snap.LastCheckedAt = st.LastCheckedAt
			snap.LastSuccessAt = st.LastSuccessAt
			snap.LastError = st.LastError
			snap.ConsecutiveErrors = st.ConsecutiveErrors
			snap.AlertActive = st.AlertActive
			snap.LastAlertTier = st.LastAlertTier
			snap.LastAlertAt = st.LastAlertAt
			snap.PendingAfterQuiet = st.PendingAfterQuiet
		} else {
			if site.Kind == logstore.RelayBalanceKindNewAPIUserSelf && tok == "" {
				snap.Status = logstore.RelayBalanceStatusUnconfigured
			} else {
				snap.Status = "pending"
			}
		}

		if snap.BalanceCNY != nil && site.Enabled {
			resp.TotalBalanceCNY += *snap.BalanceCNY
		}
		if site.Enabled {
			switch snap.Status {
			case logstore.RelayBalanceStatusUrgent:
				resp.UrgentCount++
				resp.LowCount++
			case logstore.RelayBalanceStatusLow:
				resp.LowCount++
			case logstore.RelayBalanceStatusError:
				resp.ErrorCount++
			}
		}
		resp.Sites = append(resp.Sites, snap)
	}
	return resp, nil
}

func (h *RelayBalanceHandler) getRelayBalances(ctx *fasthttp.RequestCtx) {
	reqCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	resp, err := h.buildBalancesResponse(reqCtx)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, fmt.Sprintf("Failed to load relay balances: %v", err))
		return
	}
	SendJSON(ctx, resp)
}

func (h *RelayBalanceHandler) refreshRelayBalances(ctx *fasthttp.RequestCtx) {
	reqCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	force := string(ctx.QueryArgs().Peek("force")) == "true"
	now := h.nowFn()
	h.mu.RLock()
	tooSoon := !force && !h.lastManualPoll.IsZero() && now.Sub(h.lastManualPoll) < relayBalanceManualDebounce
	h.mu.RUnlock()

	if !tooSoon {
		if _, err := h.PollAll(reqCtx, true); err != nil {
			logger.Warn("relay-balance: manual refresh warning: %v", err)
		}
		h.mu.Lock()
		h.lastManualPoll = h.nowFn()
		h.mu.Unlock()
	}

	resp, err := h.buildBalancesResponse(reqCtx)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, fmt.Sprintf("Failed to refresh relay balances: %v", err))
		return
	}
	SendJSON(ctx, resp)
}

func (h *RelayBalanceHandler) buildSettingsView(cfg *logstore.RelayBalanceConfig) *relayBalanceSettingsView {
	barkURL, barkSource := resolveBarkURL(cfg)
	view := &relayBalanceSettingsView{
		Enabled:             cfg.Enabled,
		PollIntervalSeconds: cfg.PollIntervalSeconds,
		Timezone:            cfg.Timezone,
		BarkEnabled:         cfg.BarkEnabled,
		BarkGroup:           cfg.BarkGroup,
		BarkConfigured:      barkURL != "",
		BarkSource:          barkSource,
		BarkURLMasked:       maskBarkURL(barkURL),
		CooldownHours:       cfg.CooldownHours,
		UrgentCooldownHours: cfg.UrgentCooldownHours,
		QuietHoursEnabled:   cfg.QuietHoursEnabled,
		QuietStartHour:      cfg.QuietStartHour,
		QuietEndHour:        cfg.QuietEndHour,
		Sites:               make([]relayBalanceSiteSettingsView, 0, len(cfg.Sites)),
	}
	for _, s := range cfg.Sites {
		uid, tok, tokSource := resolveSiteNewAPICredentials(s)
		view.Sites = append(view.Sites, relayBalanceSiteSettingsView{
			ID:                 s.ID,
			Name:               s.Name,
			Kind:               s.Kind,
			BaseURL:            s.BaseURL,
			RechargeURL:        s.RechargeURL,
			ProviderPrefix:     s.ProviderPrefix,
			QuotaPerUnit:       s.QuotaPerUnit,
			CNYPerSiteUnit:     s.CNYPerSiteUnit,
			ThresholdCNY:       s.ThresholdCNY,
			UrgentThresholdCNY: s.UrgentThresholdCNY,
			Enabled:            s.Enabled,
			UserID:             uid,
			HasAccessToken:     tok != "",
			AccessTokenSource:  tokSource,
			AccessTokenMasked:  maskSecret(tok),
		})
	}
	return view
}

func (h *RelayBalanceHandler) getRelayBalanceSettings(ctx *fasthttp.RequestCtx) {
	reqCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cfg, err := h.loadConfig(reqCtx)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, fmt.Sprintf("Failed to get relay balance settings: %v", err))
		return
	}
	SendJSON(ctx, h.buildSettingsView(cfg))
}

func (h *RelayBalanceHandler) updateRelayBalanceSettings(ctx *fasthttp.RequestCtx) {
	reqCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if h.logsStore == nil {
		SendError(ctx, fasthttp.StatusServiceUnavailable, "Log store is not configured")
		return
	}
	var payload updateRelayBalanceSettingsRequest
	if err := sonic.Unmarshal(ctx.PostBody(), &payload); err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err))
		return
	}
	existing, err := h.loadConfig(reqCtx)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, fmt.Sprintf("Failed to load existing settings: %v", err))
		return
	}
	existingSiteByID := make(map[string]logstore.RelayBalanceSiteConfig, len(existing.Sites))
	for _, s := range existing.Sites {
		existingSiteByID[s.ID] = s
	}

	next := &logstore.RelayBalanceConfig{
		Enabled:             payload.Enabled,
		PollIntervalSeconds: payload.PollIntervalSeconds,
		Timezone:            payload.Timezone,
		BarkEnabled:         payload.BarkEnabled,
		BarkGroup:           payload.BarkGroup,
		BarkURL:             existing.BarkURL,
		CooldownHours:       payload.CooldownHours,
		UrgentCooldownHours: payload.UrgentCooldownHours,
		QuietHoursEnabled:   payload.QuietHoursEnabled,
		QuietStartHour:      payload.QuietStartHour,
		QuietEndHour:        payload.QuietEndHour,
	}
	if payload.ClearBarkURL {
		next.BarkURL = ""
	} else if trimmed := strings.TrimSpace(payload.BarkURL); trimmed != "" {
		next.BarkURL = trimmed
	}

	if len(payload.Sites) > 0 {
		next.Sites = make([]logstore.RelayBalanceSiteConfig, 0, len(payload.Sites))
		for _, in := range payload.Sites {
			prev := existingSiteByID[strings.ToLower(strings.TrimSpace(in.ID))]
			tok := prev.AccessToken
			if in.ClearAccessToken {
				tok = ""
			} else if trimmed := strings.TrimSpace(in.AccessToken); trimmed != "" {
				tok = trimmed
			}
			next.Sites = append(next.Sites, logstore.RelayBalanceSiteConfig{
				ID:                 in.ID,
				Name:               in.Name,
				Kind:               in.Kind,
				BaseURL:            in.BaseURL,
				RechargeURL:        in.RechargeURL,
				ProviderPrefix:     in.ProviderPrefix,
				QuotaPerUnit:       in.QuotaPerUnit,
				CNYPerSiteUnit:     in.CNYPerSiteUnit,
				ThresholdCNY:       in.ThresholdCNY,
				UrgentThresholdCNY: in.UrgentThresholdCNY,
				Enabled:            in.Enabled,
				UserID:             in.UserID,
				AccessToken:        tok,
			})
		}
	} else {
		next.Sites = existing.Sites
	}

	saved, err := h.logsStore.SaveRelayBalanceConfig(reqCtx, next)
	if err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, fmt.Sprintf("Failed to save relay balance settings: %v", err))
		return
	}
	// Trigger an immediate refresh in the background when the background worker is running,
	// so newly pasted tokens or adjusted thresholds reflect right away without leaking goroutines in tests.
	h.mu.RLock()
	shouldTrigger := h.started
	parentCtx := h.baseCtx
	h.mu.RUnlock()
	if shouldTrigger {
		if parentCtx == nil {
			parentCtx = context.Background()
		}
		go func() {
			bgCtx, cancel := context.WithTimeout(parentCtx, 30*time.Second)
			defer cancel()
			_, _ = h.PollAll(bgCtx, true)
		}()
	}

	SendJSON(ctx, h.buildSettingsView(saved))
}

func (h *RelayBalanceHandler) testBarkNotification(ctx *fasthttp.RequestCtx) {
	reqCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cfg, err := h.loadConfig(reqCtx)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, fmt.Sprintf("Failed to load relay balance settings: %v", err))
		return
	}
	barkURL, _ := resolveBarkURL(cfg)
	if barkURL == "" {
		SendError(ctx, fasthttp.StatusBadRequest, "Bark 推送地址未配置")
		return
	}
	balances, err := h.buildBalancesResponse(reqCtx)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, fmt.Sprintf("Failed to read balances: %v", err))
		return
	}

	lines := make([]string, 0, len(balances.Sites)+1)
	clickURL := ""
	for _, s := range balances.Sites {
		if !s.Enabled {
			continue
		}
		if s.BalanceCNY != nil {
			tag := "正常"
			if s.Status == logstore.RelayBalanceStatusUrgent {
				tag = "告急"
				if clickURL == "" && s.RechargeURL != "" {
					clickURL = s.RechargeURL
				}
			} else if s.Status == logstore.RelayBalanceStatusLow {
				tag = "偏低"
				if clickURL == "" && s.RechargeURL != "" {
					clickURL = s.RechargeURL
				}
			}
			lines = append(lines, fmt.Sprintf("%s：¥%.2f（站内 $%.2f · %s）", s.Name, *s.BalanceCNY, ptrFloat(s.SiteBalanceUSD), tag))
		} else {
			lines = append(lines, fmt.Sprintf("%s：%s", s.Name, s.Status))
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "Bifrost 余额预警通道连通正常")
	}

	title := "Bifrost 余额监控测试通知"
	body := strings.Join(lines, "\n")
	if err := h.sendBark(reqCtx, barkURL, cfg.BarkGroup, title, body, "active", clickURL); err != nil {
		SendError(ctx, fasthttp.StatusBadGateway, fmt.Sprintf("Bark 推送失败: %v", err))
		return
	}
	SendJSON(ctx, map[string]any{
		"success": true,
		"message": "测试通知已发送至 Bark",
		"title":   title,
		"body":    body,
		"url":     clickURL,
	})
}

// --- Core Polling & Provider Key Reuse ---

type relayKeyCandidate struct {
	providerName string
	apiKey       string
}

type relayQueryOutcome struct {
	siteBalanceUSD float64
	siteUsedUSD    *float64
	keySource      string
}

func (h *RelayBalanceHandler) collectProviderKeys(ctx context.Context) map[string][]relayKeyCandidate {
	out := make(map[string][]relayKeyCandidate)
	if h.configStore == nil {
		return out
	}
	providers, err := h.configStore.GetProvidersConfig(ctx)
	if err != nil {
		logger.Warn("relay-balance: failed to read providers from config store: %v", err)
		return out
	}
	names := make([]string, 0, len(providers))
	for p := range providers {
		names = append(names, string(p))
	}
	// Sort descending so higher-tier/newer keys (e.g. bai-70pct, bai-60pct) are tried before older/expired ones (e.g. bai-40pct).
	sort.Sort(sort.Reverse(sort.StringSlice(names)))

	for _, name := range names {
		pCfg := providers[schemas.ModelProvider(name)]
		for _, k := range pCfg.Keys {
			if k.Enabled != nil && !*k.Enabled {
				continue
			}
			val := strings.TrimSpace(k.Value.GetValue())
			if val == "" {
				continue
			}
			out[strings.ToLower(name)] = append(out[strings.ToLower(name)], relayKeyCandidate{
				providerName: name,
				apiKey:       val,
			})
		}
	}
	return out
}

func candidatesForPrefix(all map[string][]relayKeyCandidate, prefix string) []relayKeyCandidate {
	pref := strings.ToLower(strings.TrimSpace(prefix))
	if pref == "" {
		return nil
	}
	names := make([]string, 0)
	for name := range all {
		if strings.HasPrefix(name, pref) {
			names = append(names, name)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	var list []relayKeyCandidate
	seenKey := make(map[string]struct{})
	for _, name := range names {
		for _, c := range all[name] {
			if _, ok := seenKey[c.apiKey]; ok {
				continue
			}
			seenKey[c.apiKey] = struct{}{}
			list = append(list, c)
		}
	}
	return list
}

func (h *RelayBalanceHandler) PollAll(ctx context.Context, evaluateAlerts bool) (*relayBalancesResponse, error) {
	h.pollMu.Lock()
	defer h.pollMu.Unlock()

	cfg, err := h.loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	now := h.nowFn()
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}

	existingStates := make(map[string]logstore.RelayBalanceState)
	if h.logsStore != nil {
		if rows, err := h.logsStore.ListRelayBalanceStates(ctx); err == nil {
			for _, row := range rows {
				existingStates[row.SiteID] = row
			}
		}
	}

	var spendByProvider map[string]float64
	if h.logsStore != nil {
		spendByProvider, _ = h.logsStore.GetProviderCost24hUSD(ctx, now.Add(-24*time.Hour))
	}
	allKeys := h.collectProviderKeys(ctx)
	barkURL, _ := resolveBarkURL(cfg)

	for _, site := range cfg.Sites {
		if !site.Enabled {
			continue
		}
		state := existingStates[site.ID]
		state.SiteID = site.ID
		checkedAt := now
		state.LastCheckedAt = &checkedAt

		// Compute 24h spend (CNY) across all Bifrost providers belonging to this relay site.
		spendUSD := 0.0
		prefLower := strings.ToLower(strings.TrimSpace(site.ProviderPrefix))
		if prefLower != "" {
			for pName, costUSD := range spendByProvider {
				if strings.HasPrefix(strings.ToLower(pName), prefLower) {
					spendUSD += costUSD
				}
			}
		}
		state.Spend24hCNY = spendUSD * relayBalanceUSDToCNYRate

		outcome, unconfigured, queryErr := h.querySiteBalance(ctx, site, allKeys)
		if unconfigured {
			state.Status = logstore.RelayBalanceStatusUnconfigured
			state.LastError = "待配置系统访问令牌与 User ID"
			state.ConsecutiveErrors = 0
			if h.logsStore != nil {
				_ = h.logsStore.SaveRelayBalanceState(ctx, &state)
			}
			continue
		}
		if queryErr != nil {
			state.ConsecutiveErrors++
			state.LastError = truncateStr(queryErr.Error(), 300)
			// Keep previous valid balance so transient 502s never falsely trigger "0 balance".
			if state.BalanceCNY == nil {
				state.Status = logstore.RelayBalanceStatusError
			} else if state.ConsecutiveErrors >= relayBalanceConsecErrAlertAt {
				state.Status = logstore.RelayBalanceStatusError
			}
			if h.logsStore != nil {
				_ = h.logsStore.SaveRelayBalanceState(ctx, &state)
			}
			continue
		}

		// Successful balance query!
		var prevSiteUSD *float64
		if state.SiteBalanceUSD != nil {
			v := *state.SiteBalanceUSD
			prevSiteUSD = &v
		}
		siteUSD := outcome.siteBalanceUSD
		balCNY := siteUSD * site.CNYPerSiteUnit
		successAt := now
		state.SiteBalanceUSD = &siteUSD
		state.BalanceCNY = &balCNY
		state.SiteUsedUSD = outcome.siteUsedUSD
		state.KeySource = outcome.keySource
		state.LastSuccessAt = &successAt
		state.LastError = ""
		state.ConsecutiveErrors = 0

		if site.Kind == logstore.RelayBalanceKindBAIV1Balance && prevSiteUSD != nil && siteUSD-*prevSiteUSD >= relayRechargeDeltaMinUSD {
			h.recordBalanceDeltaRecharge(ctx, cfg, site, *prevSiteUSD, siteUSD, now, barkURL)
		}

		if state.Spend24hCNY > 0.01 && balCNY >= 0 {
			days := balCNY / state.Spend24hCNY
			state.EstimatedDaysLeft = &days
		} else {
			state.EstimatedDaysLeft = nil
		}

		switch {
		case site.UrgentThresholdCNY > 0 && balCNY < site.UrgentThresholdCNY:
			state.Status = logstore.RelayBalanceStatusUrgent
		case site.ThresholdCNY > 0 && balCNY < site.ThresholdCNY:
			state.Status = logstore.RelayBalanceStatusLow
		default:
			state.Status = logstore.RelayBalanceStatusOK
		}

		if evaluateAlerts && cfg.BarkEnabled && barkURL != "" {
			decision := evaluateRelayBalanceAlert(cfg, site, &state, balCNY, now.In(loc))
			if decision.reset {
				state.AlertActive = false
				state.LastAlertTier = ""
				state.LastAlertAt = nil
				state.LastAlertBalanceCNY = nil
				state.PendingAfterQuiet = false
			} else if decision.deferredQuiet {
				state.AlertActive = true
				state.PendingAfterQuiet = true
			} else if decision.shouldSend {
				title, body, level := formatBarkBalanceAlert(site, &state, decision.tier)
				if err := h.sendBark(ctx, barkURL, cfg.BarkGroup, title, body, level, site.RechargeURL); err != nil {
					logger.Warn("relay-balance: failed to send Bark alert for %s: %v", site.ID, err)
				} else {
					sentAt := now
					sentBal := balCNY
					state.AlertActive = true
					state.LastAlertTier = decision.tier
					state.LastAlertAt = &sentAt
					state.LastAlertBalanceCNY = &sentBal
					state.PendingAfterQuiet = false
				}
			}
		}

		if h.logsStore != nil {
			_ = h.logsStore.SaveRelayBalanceState(ctx, &state)
		}
	}

	h.mu.Lock()
	polledAt := now
	h.lastPollAt = &polledAt
	h.mu.Unlock()

	return h.buildBalancesResponse(ctx)
}

func (h *RelayBalanceHandler) querySiteBalance(
	ctx context.Context,
	site logstore.RelayBalanceSiteConfig,
	allKeys map[string][]relayKeyCandidate,
) (*relayQueryOutcome, bool, error) {
	switch site.Kind {
	case logstore.RelayBalanceKindBAIV1Balance:
		candidates := candidatesForPrefix(allKeys, site.ProviderPrefix)
		if len(candidates) == 0 {
			return nil, true, nil
		}
		var lastErr error
		for _, c := range candidates {
			out, err := h.fetchBAIBalance(ctx, site, c)
			if err == nil {
				return out, false, nil
			}
			lastErr = err
		}
		return nil, false, lastErr

	case logstore.RelayBalanceKindPoloUsageToken:
		candidates := candidatesForPrefix(allKeys, site.ProviderPrefix)
		if len(candidates) == 0 {
			return nil, true, nil
		}
		var lastErr error
		for _, c := range candidates {
			out, err := h.fetchPoloBalance(ctx, site, c)
			if err == nil {
				return out, false, nil
			}
			lastErr = err
		}
		return nil, false, lastErr

	case logstore.RelayBalanceKindNewAPIUserSelf:
		uid, tok, _ := resolveSiteNewAPICredentials(site)
		if tok == "" {
			return nil, true, nil
		}
		out, err := h.fetchNewAPIUserSelfBalance(ctx, site, uid, tok)
		if err != nil {
			return nil, false, err
		}
		return out, false, nil

	default:
		return nil, false, fmt.Errorf("unsupported relay balance kind: %s", site.Kind)
	}
}

func (h *RelayBalanceHandler) doJSONGet(ctx context.Context, url string, headers map[string]string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "Bifrost-BalanceMonitor/1.0")
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

func (h *RelayBalanceHandler) fetchBAIBalance(ctx context.Context, site logstore.RelayBalanceSiteConfig, cand relayKeyCandidate) (*relayQueryOutcome, error) {
	url := strings.TrimRight(site.BaseURL, "/") + "/v1/balance"
	body, status, err := h.doJSONGet(ctx, url, map[string]string{
		"Authorization": "Bearer " + cand.apiKey,
	})
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from B.AI /v1/balance (%s)", status, cand.providerName)
	}
	var parsed struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			PersonalBalance *float64 `json:"personal_balance"`
			Team            *struct {
				TeamBalance *float64 `json:"team_balance"`
			} `json:"team"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("invalid B.AI response: %w", err)
	}
	if !parsed.Success {
		return nil, fmt.Errorf("B.AI balance lookup failed: %s", parsed.Message)
	}
	var credits float64
	switch {
	case parsed.Data.PersonalBalance != nil:
		credits = *parsed.Data.PersonalBalance
	case parsed.Data.Team != nil && parsed.Data.Team.TeamBalance != nil:
		credits = *parsed.Data.Team.TeamBalance
	default:
		return nil, fmt.Errorf("B.AI balance response missing personal_balance/team_balance")
	}
	qpu := site.QuotaPerUnit
	if qpu <= 0 {
		qpu = 1000000
	}
	return &relayQueryOutcome{
		siteBalanceUSD: credits / qpu,
		keySource:      "auto:" + cand.providerName,
	}, nil
}

func (h *RelayBalanceHandler) fetchPoloBalance(ctx context.Context, site logstore.RelayBalanceSiteConfig, cand relayKeyCandidate) (*relayQueryOutcome, error) {
	url := strings.TrimRight(site.BaseURL, "/") + "/api/usage/token"
	body, status, err := h.doJSONGet(ctx, url, map[string]string{
		"Authorization": "Bearer " + cand.apiKey,
	})
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from Polo /api/usage/token (%s)", status, cand.providerName)
	}
	var parsed struct {
		Data struct {
			UserQuota *struct {
				TotalAvailable *float64 `json:"total_available"`
				TotalUsed      *float64 `json:"total_used"`
			} `json:"user_quota"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("invalid Polo response: %w", err)
	}
	if parsed.Data.UserQuota == nil || parsed.Data.UserQuota.TotalAvailable == nil {
		return nil, fmt.Errorf("Polo response missing data.user_quota.total_available")
	}
	qpu := site.QuotaPerUnit
	if qpu <= 0 {
		qpu = 500000
	}
	var usedUSD *float64
	if parsed.Data.UserQuota.TotalUsed != nil {
		u := *parsed.Data.UserQuota.TotalUsed / qpu
		usedUSD = &u
	}
	return &relayQueryOutcome{
		siteBalanceUSD: *parsed.Data.UserQuota.TotalAvailable / qpu,
		siteUsedUSD:    usedUSD,
		keySource:      "auto:" + cand.providerName,
	}, nil
}

func (h *RelayBalanceHandler) fetchNewAPIUserSelfBalance(ctx context.Context, site logstore.RelayBalanceSiteConfig, userID, accessToken string) (*relayQueryOutcome, error) {
	url := strings.TrimRight(site.BaseURL, "/") + "/api/user/self"
	headers := map[string]string{
		"Authorization": "Bearer " + accessToken,
	}
	if userID != "" {
		headers["New-Api-User"] = userID
	}
	body, status, err := h.doJSONGet(ctx, url, headers)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s /api/user/self", status, site.Name)
	}
	var parsed struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			Quota     *float64 `json:"quota"`
			UsedQuota *float64 `json:"used_quota"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("invalid %s /api/user/self response: %w", site.Name, err)
	}
	if !parsed.Success || parsed.Data.Quota == nil {
		msg := parsed.Message
		if msg == "" {
			msg = "missing quota field"
		}
		return nil, fmt.Errorf("%s balance lookup failed: %s", site.Name, msg)
	}
	qpu := site.QuotaPerUnit
	if qpu <= 0 {
		qpu = 500000
	}
	var usedUSD *float64
	if parsed.Data.UsedQuota != nil {
		u := *parsed.Data.UsedQuota / qpu
		usedUSD = &u
	}
	src := "access_token"
	if userID != "" {
		src = "access_token:uid=" + userID
	}
	return &relayQueryOutcome{
		siteBalanceUSD: *parsed.Data.Quota / qpu,
		siteUsedUSD:    usedUSD,
		keySource:      src,
	}, nil
}

// --- Anti-Spam Alert Decision Engine (Pure & Deterministic) ---

type balanceAlertDecision struct {
	shouldSend    bool
	tier          string
	deferredQuiet bool
	reset         bool
}

func isInQuietHours(localNow time.Time, startHour, endHour int) bool {
	if startHour == endHour {
		return false
	}
	hour := localNow.Hour()
	if startHour < endHour {
		return hour >= startHour && hour < endHour
	}
	// Overnight wrap (e.g. 23 -> 8)
	return hour >= startHour || hour < endHour
}

func evaluateRelayBalanceAlert(
	cfg *logstore.RelayBalanceConfig,
	site logstore.RelayBalanceSiteConfig,
	state *logstore.RelayBalanceState,
	balanceCNY float64,
	localNow time.Time,
) balanceAlertDecision {
	if site.ThresholdCNY <= 0 {
		return balanceAlertDecision{}
	}
	// Recovery with 10% hysteresis prevents flap spam around the threshold.
	if balanceCNY >= site.ThresholdCNY*relayBalanceRecoveryRatio {
		if state.AlertActive || state.LastAlertAt != nil || state.PendingAfterQuiet {
			return balanceAlertDecision{reset: true}
		}
		return balanceAlertDecision{}
	}
	// Still inside the [threshold, threshold*1.10) hysteresis band: stay quiet.
	if balanceCNY >= site.ThresholdCNY {
		return balanceAlertDecision{}
	}

	tier := relayBalanceAlertTierLow
	if site.UrgentThresholdCNY > 0 && balanceCNY < site.UrgentThresholdCNY {
		tier = relayBalanceAlertTierUrgent
	}

	due := false
	// First alert or deferred from overnight quiet hours.
	if !state.AlertActive || state.LastAlertAt == nil || state.PendingAfterQuiet {
		due = true
	} else {
		elapsed := localNow.Sub(*state.LastAlertAt)
		normalCooldown := time.Duration(cfg.CooldownHours) * time.Hour
		urgentCooldown := time.Duration(cfg.UrgentCooldownHours) * time.Hour

		if tier == relayBalanceAlertTierUrgent {
			// Upgrading from normal low -> urgent breaks the normal cooldown immediately.
			if state.LastAlertTier != relayBalanceAlertTierUrgent {
				due = true
			} else if elapsed >= urgentCooldown {
				due = true
			} else if state.LastAlertBalanceCNY != nil && *state.LastAlertBalanceCNY > 0 &&
				balanceCNY <= *state.LastAlertBalanceCNY*0.5 && elapsed >= 30*time.Minute {
				// Sharp additional drop (>=50%) during urgent cooldown still respects a 30m floor.
				due = true
			}
		} else {
			if elapsed >= normalCooldown {
				due = true
			} else if state.LastAlertBalanceCNY != nil && *state.LastAlertBalanceCNY > 0 &&
				balanceCNY <= *state.LastAlertBalanceCNY*0.5 && elapsed >= time.Hour {
				due = true
			}
		}
	}

	if !due {
		return balanceAlertDecision{}
	}

	// Honor configured quiet hours (e.g. 23:00 - 08:00): only defer alerts that are actually due.
	if cfg.QuietHoursEnabled && isInQuietHours(localNow, cfg.QuietStartHour, cfg.QuietEndHour) {
		return balanceAlertDecision{deferredQuiet: true, tier: tier}
	}

	return balanceAlertDecision{shouldSend: true, tier: tier}
}

func formatBarkBalanceAlert(site logstore.RelayBalanceSiteConfig, state *logstore.RelayBalanceState, tier string) (title, body, level string) {
	balCNY := ptrFloat(state.BalanceCNY)
	siteUSD := ptrFloat(state.SiteBalanceUSD)
	prefix := "⚠️ 余额预警"
	level = "active"
	if tier == relayBalanceAlertTierUrgent {
		prefix = "🚨 余额告急"
		level = "timeSensitive"
	}
	title = fmt.Sprintf("%s：%s 仅剩 ¥%.2f", prefix, site.Name, balCNY)

	lines := []string{
		fmt.Sprintf("当前余额：¥%.2f（站内 $%.2f）", balCNY, siteUSD),
		fmt.Sprintf("预警阈值：¥%.2f（紧急线 ¥%.2f）", site.ThresholdCNY, site.UrgentThresholdCNY),
	}
	if state.Spend24hCNY > 0.01 && state.EstimatedDaysLeft != nil {
		lines = append(lines, fmt.Sprintf("近24h消耗：¥%.2f（预计约可撑 %.1f 天）", state.Spend24hCNY, *state.EstimatedDaysLeft))
	} else {
		lines = append(lines, fmt.Sprintf("近24h消耗：¥%.2f", state.Spend24hCNY))
	}
	return title, strings.Join(lines, "\n"), level
}

func (h *RelayBalanceHandler) sendBark(ctx context.Context, barkURL, group, title, body, level, clickURL string) error {
	endpoint := strings.TrimSpace(barkURL)
	if qIdx := strings.Index(endpoint, "?"); qIdx >= 0 {
		endpoint = endpoint[:qIdx]
	}
	endpoint = strings.TrimRight(endpoint, "/")
	payload := map[string]any{
		"title": title,
		"body":  body,
		"group": group,
		"level": level,
		"ttl":   600,
	}
	if u := strings.TrimSpace(clickURL); strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") {
		payload["url"] = u
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func ptrFloat(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

func truncateStr(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
