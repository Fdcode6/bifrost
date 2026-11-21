package handlers

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/logstore"
	"github.com/valyala/fasthttp"
)

type mockRelayConfigStore struct {
	configstore.ConfigStore
	providers map[schemas.ModelProvider]configstore.ProviderConfig
}

func (m *mockRelayConfigStore) GetProvidersConfig(_ context.Context) (map[schemas.ModelProvider]configstore.ProviderConfig, error) {
	return m.providers, nil
}

func newTestRelayLogStore(t *testing.T) logstore.LogStore {
	t.Helper()
	testLogger := bifrost.NewDefaultLogger(schemas.LogLevelError)
	SetLogger(testLogger)
	dbPath := filepath.Join(t.TempDir(), "relay-balance-test.db")
	store, err := logstore.NewLogStore(context.Background(), &logstore.Config{
		Enabled: true,
		Type:    logstore.LogStoreTypeSQLite,
		Config: &logstore.SQLiteConfig{
			Path: dbPath,
		},
	}, testLogger)
	if err != nil {
		t.Fatalf("NewLogStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close(context.Background())
	})
	return store
}

func TestEvaluateRelayBalanceAlert_AntiSpamMatrix(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	cfg := logstore.DefaultRelayBalanceConfig()
	site := cfg.Sites[0] // threshold 50, urgent 15

	daytime := time.Date(2026, 11, 21, 14, 0, 0, 0, loc)
	quietNight := time.Date(2026, 11, 21, 23, 30, 0, 0, loc)
	morning := time.Date(2026, 11, 22, 8, 5, 0, 0, loc)

	// 1. Healthy balance -> silent.
	st := &logstore.RelayBalanceState{SiteID: site.ID}
	d := evaluateRelayBalanceAlert(cfg, site, st, 80, daytime)
	if d.shouldSend || d.deferredQuiet || d.reset {
		t.Fatalf("expected silent for healthy balance, got %+v", d)
	}

	// 2. First drop below 50 during daytime -> fires low.
	d = evaluateRelayBalanceAlert(cfg, site, st, 42, daytime)
	if !d.shouldSend || d.tier != relayBalanceAlertTierLow {
		t.Fatalf("expected immediate low alert, got %+v", d)
	}
	sentAt := daytime
	sentBal := 42.0
	st.AlertActive = true
	st.LastAlertTier = d.tier
	st.LastAlertAt = &sentAt
	st.LastAlertBalanceCNY = &sentBal

	// 3. Re-poll 1h later still low (40) -> suppressed by 6h cooldown.
	d = evaluateRelayBalanceAlert(cfg, site, st, 40, daytime.Add(time.Hour))
	if d.shouldSend || d.deferredQuiet || d.reset {
		t.Fatalf("expected cooldown suppression at +1h, got %+v", d)
	}

	// 4. Drops below urgent (12 < 15) at +1h -> immediately breaks normal cooldown!
	d = evaluateRelayBalanceAlert(cfg, site, st, 12, daytime.Add(time.Hour))
	if !d.shouldSend || d.tier != relayBalanceAlertTierUrgent {
		t.Fatalf("expected immediate upgrade to urgent alert, got %+v", d)
	}
	sentAt = daytime.Add(time.Hour)
	sentBal = 12.0
	st.LastAlertTier = d.tier
	st.LastAlertAt = &sentAt
	st.LastAlertBalanceCNY = &sentBal

	// 5. Re-poll 20m later still urgent (11) -> suppressed by 2h urgent cooldown.
	d = evaluateRelayBalanceAlert(cfg, site, st, 11, sentAt.Add(20*time.Minute))
	if d.shouldSend || d.deferredQuiet {
		t.Fatalf("expected urgent cooldown suppression at +20m, got %+v", d)
	}

	// 6. Sharp >=50% drop (5 <= 12*0.5) at +35m -> fires urgent respecting 30m floor.
	d = evaluateRelayBalanceAlert(cfg, site, st, 5, sentAt.Add(35*time.Minute))
	if !d.shouldSend || d.tier != relayBalanceAlertTierUrgent {
		t.Fatalf("expected >=50%% drop fast alert at +35m, got %+v", d)
	}

	// 7. Quiet hours when alert is NOT due (recent alert 30m before quiet) -> must NOT mark deferredQuiet!
	recentBeforeQuiet := time.Date(2026, 11, 21, 22, 45, 0, 0, loc)
	recentBal := 35.0
	stRecent := &logstore.RelayBalanceState{
		SiteID:              site.ID,
		AlertActive:         true,
		LastAlertTier:       relayBalanceAlertTierLow,
		LastAlertAt:         &recentBeforeQuiet,
		LastAlertBalanceCNY: &recentBal,
	}
	d = evaluateRelayBalanceAlert(cfg, site, stRecent, 34, quietNight)
	if d.shouldSend || d.deferredQuiet {
		t.Fatalf("expected quiet hours not to defer an alert still inside cooldown, got %+v", d)
	}

	// 8. First alert occurring during quiet hours -> deferredQuiet=true, shouldSend=false.
	stQuiet := &logstore.RelayBalanceState{SiteID: site.ID}
	d = evaluateRelayBalanceAlert(cfg, site, stQuiet, 30, quietNight)
	if d.shouldSend || !d.deferredQuiet {
		t.Fatalf("expected deferredQuiet=true during quiet hours, got %+v", d)
	}
	stQuiet.AlertActive = true
	stQuiet.PendingAfterQuiet = true

	// 9. Morning 08:05 after quiet hours with PendingAfterQuiet -> fires immediately.
	d = evaluateRelayBalanceAlert(cfg, site, stQuiet, 30, morning)
	if !d.shouldSend || d.tier != relayBalanceAlertTierLow {
		t.Fatalf("expected morning release of deferred alert, got %+v", d)
	}

	// 10. Balance inside hysteresis band [50, 55) -> neither fires nor resets.
	d = evaluateRelayBalanceAlert(cfg, site, st, 52, daytime.Add(10*time.Hour))
	if d.shouldSend || d.reset {
		t.Fatalf("expected hysteresis band [50,55) to stay silent without reset, got %+v", d)
	}

	// 11. Balance recovered >= 55 -> resets alert state cleanly.
	d = evaluateRelayBalanceAlert(cfg, site, st, 56, daytime.Add(10*time.Hour))
	if !d.reset || d.shouldSend {
		t.Fatalf("expected reset=true at >=55, got %+v", d)
	}
}

func TestRelayBalanceHandler_PollAll_ParsesAllKindsAndAlertsOnce(t *testing.T) {
	logsStore := newTestRelayLogStore(t)
	ctx := context.Background()

	var barkMu sync.Mutex
	var barkPayloads []map[string]any
	barkSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		barkMu.Lock()
		barkPayloads = append(barkPayloads, body)
		barkMu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":200,"message":"success"}`))
	}))
	defer barkSrv.Close()

	storeAPISrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/self" || r.Header.Get("New-Api-User") != "10001" || r.Header.Get("Authorization") != "Bearer store-tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"quota":1115000,"used_quota":5000000}}`)) // $2.23 -> ¥2.23
	}))
	defer storeAPISrv.Close()

	openLuxSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/self" || r.Header.Get("New-Api-User") != "10002" || r.Header.Get("Authorization") != "Bearer openlux-tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"quota":6440000,"used_quota":25000000}}`)) // $12.88 -> ¥85.83232
	}))
	defer openLuxSrv.Close()

	poloSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/usage/token" || r.Header.Get("Authorization") != "Bearer sk-polo-live" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"user_quota":{"total_available":61920000,"total_used":10000000}}}`)) // $123.84 -> ¥123.84
	}))
	defer poloSrv.Close()

	baiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/balance" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// Simulate bai-70pct returning 401 so fallback must skip to bai-60pct!
		if r.Header.Get("Authorization") == "Bearer sk-bai-expired" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"success":false,"message":"Invalid or expired api_key"}`))
			return
		}
		if r.Header.Get("Authorization") == "Bearer sk-bai-good" {
			_, _ = w.Write([]byte(`{"success":true,"data":{"personal_balance":30620000}}`)) // $30.62 -> ¥208.216
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer baiSrv.Close()

	cfg := logstore.DefaultRelayBalanceConfig()
	cfg.BarkURL = barkSrv.URL + "/devicekey?group=old&ttl=10"
	cfg.Sites[0].BaseURL = storeAPISrv.URL
	cfg.Sites[0].UserID = "10001"
	cfg.Sites[0].AccessToken = "store-tok"
	cfg.Sites[1].BaseURL = openLuxSrv.URL
	cfg.Sites[1].UserID = "10002"
	cfg.Sites[1].AccessToken = "openlux-tok"
	cfg.Sites[2].BaseURL = poloSrv.URL
	cfg.Sites[3].BaseURL = baiSrv.URL
	if _, err := logsStore.SaveRelayBalanceConfig(ctx, cfg); err != nil {
		t.Fatalf("SaveRelayBalanceConfig() error = %v", err)
	}

	enabledTrue := true
	cfgStore := &mockRelayConfigStore{
		providers: map[schemas.ModelProvider]configstore.ProviderConfig{
			"polo-pool-3x": {
				Keys: []schemas.Key{{Value: *schemas.NewEnvVar("sk-polo-live"), Enabled: &enabledTrue}},
			},
			"bai-70pct": {
				Keys: []schemas.Key{{Value: *schemas.NewEnvVar("sk-bai-expired"), Enabled: &enabledTrue}},
			},
			"bai-60pct": {
				Keys: []schemas.Key{{Value: *schemas.NewEnvVar("sk-bai-good"), Enabled: &enabledTrue}},
			},
		},
	}

	loc := time.FixedZone("CST", 8*3600)
	h := NewRelayBalanceHandler(cfgStore, logsStore)
	h.nowFn = func() time.Time { return time.Date(2026, 11, 21, 14, 15, 0, 0, loc) }

	resp, err := h.PollAll(ctx, true)
	if err != nil {
		t.Fatalf("PollAll() error = %v", err)
	}
	if resp.UrgentCount != 1 || resp.LowCount != 1 || resp.ErrorCount != 0 {
		t.Fatalf("unexpected counts: urgent=%d low=%d err=%d", resp.UrgentCount, resp.LowCount, resp.ErrorCount)
	}

	byID := make(map[string]relayBalanceSiteSnapshot, len(resp.Sites))
	for _, s := range resp.Sites {
		byID[s.ID] = s
	}
	if byID["storeapi"].Status != logstore.RelayBalanceStatusUrgent || byID["storeapi"].BalanceCNY == nil || math.Abs(*byID["storeapi"].BalanceCNY-2.23) > 0.01 {
		t.Fatalf("unexpected storeapi snapshot: %+v", byID["storeapi"])
	}
	if byID["openlux"].Status != logstore.RelayBalanceStatusOK || byID["openlux"].BalanceCNY == nil || math.Abs(*byID["openlux"].BalanceCNY-85.83232) > 0.01 {
		t.Fatalf("unexpected openlux snapshot: %+v", byID["openlux"])
	}
	if byID["polo"].Status != logstore.RelayBalanceStatusOK || byID["polo"].BalanceCNY == nil || math.Abs(*byID["polo"].BalanceCNY-123.84) > 0.01 {
		t.Fatalf("unexpected polo snapshot: %+v", byID["polo"])
	}
	if byID["bai"].Status != logstore.RelayBalanceStatusOK || byID["bai"].BalanceCNY == nil || math.Abs(*byID["bai"].BalanceCNY-208.216) > 0.01 {
		t.Fatalf("unexpected bai snapshot: %+v", byID["bai"])
	}
	if byID["bai"].KeySource != "auto:bai-60pct" {
		t.Fatalf("expected B.AI fallback to auto:bai-60pct, got %q", byID["bai"].KeySource)
	}

	barkMu.Lock()
	if len(barkPayloads) != 1 {
		t.Fatalf("expected 1 Bark alert (storeapi urgent), got %d", len(barkPayloads))
	}
	if barkPayloads[0]["level"] != "timeSensitive" || barkPayloads[0]["group"] != "bifrost-balance" {
		t.Fatalf("unexpected Bark payload: %+v", barkPayloads[0])
	}
	if barkPayloads[0]["url"] != "https://www.storeapi.one/wallet" {
		t.Fatalf("expected StoreAPI recharge url in Bark payload, got %v", barkPayloads[0]["url"])
	}
	if byID["storeapi"].RechargeURL != "https://www.storeapi.one/wallet" || byID["polo"].RechargeURL != "https://poloai.top/wallet" {
		t.Fatalf("expected recharge_url in balance snapshots, got storeapi=%q polo=%q", byID["storeapi"].RechargeURL, byID["polo"].RechargeURL)
	}
	if !strings.Contains(barkPayloads[0]["title"].(string), "StoreAPI") {
		t.Fatalf("expected StoreAPI in Bark title, got %q", barkPayloads[0]["title"])
	}
	barkMu.Unlock()

	// Poll again 10 minutes later -> cooldown must prevent duplicate Bark push!
	h.nowFn = func() time.Time { return time.Date(2026, 11, 21, 14, 25, 0, 0, loc) }
	if _, err := h.PollAll(ctx, true); err != nil {
		t.Fatalf("second PollAll() error = %v", err)
	}
	barkMu.Lock()
	if len(barkPayloads) != 1 {
		t.Fatalf("expected cooldown to suppress duplicate Bark alert, got %d payloads", len(barkPayloads))
	}
	barkMu.Unlock()

	// Test manual Bark endpoint returns HTTP 200 and sends summary body.
	var rctx fasthttp.RequestCtx
	h.testBarkNotification(&rctx)
	if rctx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("testBarkNotification() status=%d body=%s", rctx.Response.StatusCode(), string(rctx.Response.Body()))
	}
	barkMu.Lock()
	if len(barkPayloads) != 2 {
		t.Fatalf("expected test-bark to send 2nd notification, got %d", len(barkPayloads))
	}
	barkMu.Unlock()
}

func TestRelayBalanceHandler_SettingsMaskingAndPreservation(t *testing.T) {
	logsStore := newTestRelayLogStore(t)
	ctx := context.Background()

	cfg := logstore.DefaultRelayBalanceConfig()
	cfg.BarkURL = "https://api.day.app/ABCDEFGHIJKLMNOPQRSTUV"
	cfg.Sites[0].UserID = "10001"
	cfg.Sites[0].AccessToken = "super-secret-access-token-999"
	if _, err := logsStore.SaveRelayBalanceConfig(ctx, cfg); err != nil {
		t.Fatalf("SaveRelayBalanceConfig() error = %v", err)
	}

	h := NewRelayBalanceHandler(nil, logsStore)

	// GET must never leak raw secrets.
	var getCtx fasthttp.RequestCtx
	h.getRelayBalanceSettings(&getCtx)
	if getCtx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("GET settings status=%d", getCtx.Response.StatusCode())
	}
	rawJSON := string(getCtx.Response.Body())
	if strings.Contains(rawJSON, "ABCDEFGHIJKLMNOPQRSTUV") || strings.Contains(rawJSON, "super-secret-access-token-999") {
		t.Fatalf("raw secret leaked in settings response: %s", rawJSON)
	}

	// PUT with empty secret fields must preserve existing DB secrets.
	updateBody := `{
		"enabled": true,
		"poll_interval_seconds": 300,
		"timezone": "Asia/Shanghai",
		"bark_enabled": true,
		"bark_group": "bifrost-balance",
		"cooldown_hours": 4,
		"urgent_cooldown_hours": 1,
		"quiet_hours_enabled": true,
		"quiet_start_hour": 23,
		"quiet_end_hour": 8,
		"sites": [
			{
				"id": "storeapi",
				"name": "StoreAPI",
				"kind": "new_api_user_self",
				"base_url": "https://www.storeapi.one",
				"provider_prefix": "storeapi-",
				"quota_per_unit": 500000,
				"cny_per_site_unit": 1.0,
				"threshold_cny": 60,
				"urgent_threshold_cny": 20,
				"enabled": true,
				"user_id": "10001"
			}
		]
	}`
	var putCtx fasthttp.RequestCtx
	putCtx.Request.SetBodyString(updateBody)
	h.updateRelayBalanceSettings(&putCtx)
	if putCtx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("PUT settings status=%d body=%s", putCtx.Response.StatusCode(), string(putCtx.Response.Body()))
	}

	reloaded, err := logsStore.GetRelayBalanceConfig(ctx)
	if err != nil {
		t.Fatalf("GetRelayBalanceConfig() error = %v", err)
	}
	if reloaded.BarkURL != "https://api.day.app/ABCDEFGHIJKLMNOPQRSTUV" {
		t.Fatalf("expected BarkURL preserved on blank PUT, got %q", reloaded.BarkURL)
	}
	if reloaded.Sites[0].AccessToken != "super-secret-access-token-999" {
		t.Fatalf("expected AccessToken preserved on blank PUT, got %q", reloaded.Sites[0].AccessToken)
	}
	if reloaded.CooldownHours != 4 || reloaded.Sites[0].ThresholdCNY != 60 {
		t.Fatalf("expected updated cooldown/threshold saved, got cooldown=%d threshold=%.1f", reloaded.CooldownHours, reloaded.Sites[0].ThresholdCNY)
	}
}
