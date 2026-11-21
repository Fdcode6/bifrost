package handlers

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/logstore"
	"github.com/valyala/fasthttp"
)

func TestRelayRecharge_SyncTopupAndLogAndBAIDeltaCapture(t *testing.T) {
	logsStore := newTestRelayLogStore(t)
	ctx := context.Background()

	storeAPISrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota":50000000}}`))
		case "/api/user/topup/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"total":2,"items":[
				{"id":1,"amount":100,"money":100,"trade_no":"ST-OK-1","payment_method":"alipay","create_time":1795242181,"complete_time":1795242218,"status":"success"},
				{"id":2,"amount":50,"money":50,"trade_no":"ST-PEND-2","payment_method":"alipay","create_time":1795242000,"complete_time":0,"status":"pending"}
			]}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer storeAPISrv.Close()

	poloSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/usage/token":
			_, _ = w.Write([]byte(`{"data":{"user_quota":{"total_available":50000000}}}`))
		case "/api/user/topup/self":
			if r.Header.Get("Authorization") != "Bearer polo-pat" || r.Header.Get("New-Api-User") != "24723" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"total":1,"items":[
				{"id":62498,"amount":500,"money":500,"trade_no":"POLO-OK-1","payment_method":"alipay","create_time":1795256790,"complete_time":1795256839,"status":"success"}
			]}}`))
		case "/api/log/self":
			// First row duplicates POLO-OK-1 (must be deduped); second row is an older pre-migration recharge (must be merged!).
			_, _ = w.Write([]byte(`{"success":true,"data":{"total":2,"items":[
				{"id":1,"created_at":1795256839,"type":1,"content":"Online top-up succeeded, amount added: ＄500.000000, payment amount: 500.000000","quota":0},
				{"id":2,"created_at":1791540355,"type":1,"content":"使用在线充值成功，充值金额: ＄200.000000，支付金额：200","quota":0}
			]}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer poloSrv.Close()

	openLuxSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota":6440000}}`))
		case "/api/user/topuprecords":
			_, _ = w.Write([]byte(`{"success":true,"data":{"total":2,"records":[
				{"id":829018,"amount":50,"money":49.25,"trade_no":"OL-OK-1","payment_method":"antom","create_time":1795138846,"complete_time":1795138865,"status":"success"},
				{"id":829019,"amount":50,"money":49.25,"trade_no":"OL-EXP-2","payment_method":"antom","create_time":1795138000,"complete_time":0,"status":"expired"}
			]}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer openLuxSrv.Close()

	var baiMu sync.Mutex
	baiCredits := 20_000_000.0 // $20 initially
	baiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		baiMu.Lock()
		bal := baiCredits
		baiMu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data":    map[string]any{"personal_balance": bal},
		})
	}))
	defer baiSrv.Close()

	cfg := logstore.DefaultRelayBalanceConfig()
	cfg.BarkEnabled = false
	cfg.Sites[0].BaseURL = storeAPISrv.URL
	cfg.Sites[0].UserID = "10001"
	cfg.Sites[0].AccessToken = "st-tok"
	cfg.Sites[1].BaseURL = openLuxSrv.URL
	cfg.Sites[1].UserID = "10002"
	cfg.Sites[1].AccessToken = "ol-tok"
	cfg.Sites[2].BaseURL = poloSrv.URL
	cfg.Sites[2].UserID = "24723"
	cfg.Sites[2].AccessToken = "polo-pat"
	cfg.Sites[3].BaseURL = baiSrv.URL
	if _, err := logsStore.SaveRelayBalanceConfig(ctx, cfg); err != nil {
		t.Fatalf("SaveRelayBalanceConfig() error = %v", err)
	}

	enabledTrue := true
	cfgStore := &mockRelayConfigStore{
		providers: map[schemas.ModelProvider]configstore.ProviderConfig{
			"polo-pool-3x": {Keys: []schemas.Key{{Value: *schemas.NewEnvVar("sk-polo"), Enabled: &enabledTrue}}},
			"bai-70pct":    {Keys: []schemas.Key{{Value: *schemas.NewEnvVar("sk-bai"), Enabled: &enabledTrue}}},
		},
	}

	loc := time.FixedZone("CST", 8*3600)
	h := NewRelayBalanceHandler(cfgStore, logsStore)
	h.nowFn = func() time.Time { return time.Date(2026, 11, 21, 19, 0, 0, 0, loc) }

	// 1. Sync official recharges: StoreAPI (1 success), OpenLux (1 success, ignores expired),
	// PoloAPI (1 topup order + 1 older log merged, duplicate log deduped) => total 4 inserted!
	syncRes, err := h.SyncAllRecharges(ctx)
	if err != nil {
		t.Fatalf("SyncAllRecharges() error = %v", err)
	}
	if syncRes.TotalInserted != 4 {
		t.Fatalf("expected 4 synced recharge records, got %d (results=%+v)", syncRes.TotalInserted, syncRes.Results)
	}
	// Check OpenLux paid_cny = 49.25 * 6.8 = 334.90
	var olRec *logstore.RelayRechargeRecord
	for i := range syncRes.Data.Items {
		if syncRes.Data.Items[i].SiteID == "openlux" {
			olRec = &syncRes.Data.Items[i]
		}
	}
	if olRec == nil || math.Abs(olRec.PaidCNY-49.25*6.8) > 0.01 || math.Abs(olRec.SiteAmountUSD-50) > 0.01 {
		t.Fatalf("unexpected OpenLux parsed record: %+v", olRec)
	}

	// 2. Poll B.AI twice: first poll sets baseline $20, second poll sees jump to $40 -> auto-captures +$20 delta!
	if _, err := h.PollAll(ctx, false); err != nil {
		t.Fatalf("first PollAll() error = %v", err)
	}
	baiMu.Lock()
	baiCredits = 40_000_000.0 // +$20 topup
	baiMu.Unlock()
	h.nowFn = func() time.Time { return time.Date(2026, 11, 21, 19, 10, 0, 0, loc) }
	if _, err := h.PollAll(ctx, false); err != nil {
		t.Fatalf("second PollAll() error = %v", err)
	}

	items, summary, err := logsStore.ListRelayRechargeRecords(ctx, logstore.RelayRechargeQuery{})
	if err != nil {
		t.Fatalf("ListRelayRechargeRecords() error = %v", err)
	}
	if summary.TotalCount != 5 || summary.PendingCount != 1 {
		t.Fatalf("expected 5 total records with 1 pending B.AI delta, got count=%d pending=%d", summary.TotalCount, summary.PendingCount)
	}

	var baiRec *logstore.RelayRechargeRecord
	for i := range items {
		if items[i].SiteID == "bai" {
			baiRec = &items[i]
		}
	}
	if baiRec == nil || baiRec.Source != logstore.RelayRechargeSourceBalanceDelta || math.Abs(baiRec.SiteAmountUSD-20) > 0.01 {
		t.Fatalf("unexpected B.AI auto-detected record: %+v", baiRec)
	}

	// 3. Edit & confirm the B.AI auto-detected record via HTTP PUT (URL-escaped ID with colons).
	var putCtx fasthttp.RequestCtx
	putCtx.SetUserValue("id", url.PathEscape(baiRec.ID))
	putCtx.Request.SetBodyString(`{"paid_cny":141.80,"payment_method":"card","note":"含网关手续费已核对"}`)
	h.updateRelayRecharge(&putCtx)
	if putCtx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("updateRelayRecharge() status=%d body=%s", putCtx.Response.StatusCode(), string(putCtx.Response.Body()))
	}
	updated, err := logsStore.GetRelayRechargeRecord(ctx, baiRec.ID)
	if err != nil || updated == nil {
		t.Fatalf("GetRelayRechargeRecord() err=%v", err)
	}
	if updated.Status != logstore.RelayRechargeStatusSuccess || math.Abs(updated.PaidCNY-141.80) > 0.01 {
		t.Fatalf("expected confirmed updated B.AI record, got %+v", updated)
	}

	// 4. Manual create + delete via HTTP endpoints.
	var postCtx fasthttp.RequestCtx
	postCtx.Request.SetBodyString(`{"site_id":"bai","site_amount_usd":10,"paid_cny":70.5,"payment_method":"card","recharged_at":"2026-11-21 18:30"}`)
	h.createRelayRecharge(&postCtx)
	if postCtx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("createRelayRecharge() status=%d body=%s", postCtx.Response.StatusCode(), string(postCtx.Response.Body()))
	}
	var created logstore.RelayRechargeRecord
	if err := json.Unmarshal(postCtx.Response.Body(), &created); err != nil {
		t.Fatalf("unmarshal created error = %v", err)
	}
	var delCtx fasthttp.RequestCtx
	delCtx.SetUserValue("id", url.PathEscape(created.ID))
	h.deleteRelayRecharge(&delCtx)
	if delCtx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("deleteRelayRecharge() status=%d", delCtx.Response.StatusCode())
	}
}
