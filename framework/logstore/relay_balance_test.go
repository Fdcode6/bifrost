package logstore

import (
	"context"
	"testing"
	"time"
)

func TestRelayBalanceConfig_DefaultsAndRoundTrip(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	cfg, err := store.GetRelayBalanceConfig(ctx)
	if err != nil {
		t.Fatalf("GetRelayBalanceConfig() error = %v", err)
	}
	if !cfg.Enabled || !cfg.BarkEnabled {
		t.Fatalf("expected default enabled=true, bark_enabled=true, got enabled=%v bark=%v", cfg.Enabled, cfg.BarkEnabled)
	}
	if len(cfg.Sites) != 4 {
		t.Fatalf("expected 4 default sites, got %d", len(cfg.Sites))
	}
	wantRatios := map[string]float64{
		"storeapi": 1.0,
		"openlux":  6.664,
		"polo":     1.0,
		"bai":      6.8,
	}
	wantRechargeURLs := map[string]string{
		"storeapi": "https://www.storeapi.one/wallet",
		"openlux":  "https://www.openlux.ai/console/topup",
		"polo":     "https://poloai.top/wallet",
		"bai":      "https://chat.b.ai/purchase",
	}
	for _, s := range cfg.Sites {
		if want, ok := wantRatios[s.ID]; !ok || !almostEqualFloat(s.CNYPerSiteUnit, want) {
			t.Fatalf("unexpected ratio for site %q: got %.4f want %.4f", s.ID, s.CNYPerSiteUnit, want)
		}
		if want := wantRechargeURLs[s.ID]; s.RechargeURL != want {
			t.Fatalf("unexpected default recharge_url for site %q: got %q want %q", s.ID, s.RechargeURL, want)
		}
		if !almostEqualFloat(s.ThresholdCNY, 50) || !almostEqualFloat(s.UrgentThresholdCNY, 15) {
			t.Fatalf("unexpected thresholds for %q: got %.2f / %.2f", s.ID, s.ThresholdCNY, s.UrgentThresholdCNY)
		}
	}

	// Unsafe non-http(s) recharge_url must be rejected.
	badURL := *cfg
	badSites := append([]RelayBalanceSiteConfig(nil), cfg.Sites...)
	badSites[0].RechargeURL = "javascript:alert(1)"
	badURL.Sites = badSites
	if _, err := store.SaveRelayBalanceConfig(ctx, &badURL); err == nil {
		t.Fatalf("expected error for unsafe recharge_url, got nil")
	}

	// Empty recharge_url on a known default site must auto-backfill from defaults.
	cfg.Sites[0].RechargeURL = ""
	cfg.Sites[1].RechargeURL = "https://custom.openlux.example/topup"

	// Invalid timezone must be rejected explicitly.
	badTZ := *cfg
	badTZ.Timezone = "Invalid/Zone"
	if _, err := store.SaveRelayBalanceConfig(ctx, &badTZ); err == nil {
		t.Fatalf("expected error for invalid timezone, got nil")
	}

	// Modify and save config with out-of-range numeric values to verify normalization.
	cfg.PollIntervalSeconds = 10 // below minimum -> clamped to 60
	cfg.Timezone = ""            // blank -> defaults to Asia/Shanghai
	cfg.CooldownHours = 0
	cfg.UrgentCooldownHours = 99
	cfg.BarkURL = "https://api.day.app/testkey/"
	cfg.Sites[0].ThresholdCNY = 40
	cfg.Sites[0].UrgentThresholdCNY = 60 // > threshold -> clamped to threshold
	cfg.Sites[0].UserID = "10001"
	cfg.Sites[0].AccessToken = "secret-token-123"

	saved, err := store.SaveRelayBalanceConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("SaveRelayBalanceConfig() error = %v", err)
	}
	if saved.PollIntervalSeconds != 60 {
		t.Fatalf("expected PollIntervalSeconds clamped to 60, got %d", saved.PollIntervalSeconds)
	}
	if saved.Timezone != "Asia/Shanghai" {
		t.Fatalf("expected empty timezone default to Asia/Shanghai, got %q", saved.Timezone)
	}
	if saved.CooldownHours != 6 {
		t.Fatalf("expected CooldownHours default 6, got %d", saved.CooldownHours)
	}
	if saved.UrgentCooldownHours > saved.CooldownHours {
		t.Fatalf("expected UrgentCooldownHours <= CooldownHours, got %d > %d", saved.UrgentCooldownHours, saved.CooldownHours)
	}
	if !almostEqualFloat(saved.Sites[0].UrgentThresholdCNY, 40) {
		t.Fatalf("expected urgent threshold clamped to 40, got %.2f", saved.Sites[0].UrgentThresholdCNY)
	}

	reloaded, err := store.GetRelayBalanceConfig(ctx)
	if err != nil {
		t.Fatalf("reloaded GetRelayBalanceConfig() error = %v", err)
	}
	if reloaded.BarkURL != "https://api.day.app/testkey" {
		t.Fatalf("expected trimmed bark url, got %q", reloaded.BarkURL)
	}
	if reloaded.Sites[0].UserID != "10001" || reloaded.Sites[0].AccessToken != "secret-token-123" {
		t.Fatalf("expected site credentials persisted, got uid=%q tok=%q", reloaded.Sites[0].UserID, reloaded.Sites[0].AccessToken)
	}
	if reloaded.Sites[0].RechargeURL != "https://www.storeapi.one/wallet" {
		t.Fatalf("expected empty recharge_url backfilled to default, got %q", reloaded.Sites[0].RechargeURL)
	}
	if reloaded.Sites[1].RechargeURL != "https://custom.openlux.example/topup" {
		t.Fatalf("expected custom recharge_url preserved, got %q", reloaded.Sites[1].RechargeURL)
	}
}

func TestRelayBalanceState_UpsertAndList(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	balUSD := 12.88
	balCNY := 85.83232
	days := 4.5
	st := &RelayBalanceState{
		SiteID:            "openlux",
		Status:            RelayBalanceStatusOK,
		SiteBalanceUSD:    &balUSD,
		BalanceCNY:        &balCNY,
		Spend24hCNY:       19.07,
		EstimatedDaysLeft: &days,
		KeySource:         "access_token:uid=10002",
		LastCheckedAt:     &now,
		LastSuccessAt:     &now,
	}
	if err := store.SaveRelayBalanceState(ctx, st); err != nil {
		t.Fatalf("SaveRelayBalanceState() insert error = %v", err)
	}

	// Update existing state to low + pending after quiet.
	lowCNY := 32.0
	st.Status = RelayBalanceStatusLow
	st.BalanceCNY = &lowCNY
	st.AlertActive = true
	st.PendingAfterQuiet = true
	if err := store.SaveRelayBalanceState(ctx, st); err != nil {
		t.Fatalf("SaveRelayBalanceState() update error = %v", err)
	}

	rows, err := store.ListRelayBalanceStates(ctx)
	if err != nil {
		t.Fatalf("ListRelayBalanceStates() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 state row, got %d", len(rows))
	}
	if rows[0].Status != RelayBalanceStatusLow || !rows[0].PendingAfterQuiet || !rows[0].AlertActive {
		t.Fatalf("unexpected updated state: %+v", rows[0])
	}
	if rows[0].BalanceCNY == nil || !almostEqualFloat(*rows[0].BalanceCNY, 32.0) {
		t.Fatalf("expected BalanceCNY=32.0, got %v", rows[0].BalanceCNY)
	}
}

func TestGetProviderCost24hUSD(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	recent1 := 1.25
	recent2 := 0.75
	oldCost := 9.99
	extCost := 5.55

	logs := []*Log{
		{
			ID: "r1", Timestamp: now.Add(-2 * time.Hour), Object: "chat_completion",
			Provider: "openlux-aistudio-3", Model: "gemini-3.1-pro-preview", Status: "success", Cost: &recent1,
		},
		{
			ID: "r2", Timestamp: now.Add(-6 * time.Hour), Object: "chat_completion",
			Provider: "openlux-aistudio-3", Model: "gemini-3.1-pro-preview", Status: "success", Cost: &recent2,
		},
		{
			ID: "old1", Timestamp: now.Add(-30 * time.Hour), Object: "chat_completion",
			Provider: "openlux-aistudio-3", Model: "gemini-3.1-pro-preview", Status: "success", Cost: &oldCost,
		},
		{
			ID: "ext:doubao:1", Timestamp: now.Add(-1 * time.Hour), Object: "chat_completion",
			Provider: "openlux-aistudio-3", Model: "doubao-seed", Status: "success", Cost: &extCost,
		},
	}
	for _, l := range logs {
		if _, err := store.UpsertProfitEventFromLog(ctx, l); err != nil {
			t.Fatalf("UpsertProfitEventFromLog(%s) error = %v", l.ID, err)
		}
	}

	costs, err := store.GetProviderCost24hUSD(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("GetProviderCost24hUSD() error = %v", err)
	}
	got := costs["openlux-aistudio-3"]
	if !almostEqualFloat(got, 2.0) {
		t.Fatalf("expected 24h Bifrost cost 2.00 USD (excluding old and ext: rows), got %.6f", got)
	}
}
