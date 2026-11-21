package logstore

import (
	"context"
	"testing"
	"time"
)

func TestRelayRechargeRecords_CRUDIdempotencyAndSummary(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	t1 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

	batch := []RelayRechargeRecord{
		{
			ID: "storeapi:topup:ORD1", SiteID: "storeapi", SiteName: "StoreAPI",
			Source: RelayRechargeSourceAPISync, ExternalID: "ORD1",
			RechargedAt: t1, SiteAmountUSD: 100, PaidAmount: 100, PaidCurrency: "CNY", PaidCNY: 100,
			PaymentMethod: "alipay", Status: RelayRechargeStatusSuccess,
		},
		{
			ID: "polo:topup:ORD2", SiteID: "polo", SiteName: "PoloAPI",
			Source: RelayRechargeSourceAPISync, ExternalID: "ORD2",
			RechargedAt: t2, SiteAmountUSD: 500, PaidAmount: 500, PaidCurrency: "CNY", PaidCNY: 500,
			PaymentMethod: "alipay", Status: RelayRechargeStatusSuccess,
		},
		{
			ID: "bai:delta:1795250000", SiteID: "bai", SiteName: "B.AI",
			Source:      RelayRechargeSourceBalanceDelta,
			RechargedAt: t3, SiteAmountUSD: 20, PaidAmount: 20, PaidCurrency: "USD", PaidCNY: 136,
			PaymentMethod: "auto_detected", Status: RelayRechargeStatusPendingConfirm,
			Note: "auto detected",
		},
	}

	inserted, err := store.UpsertRelayRechargeRecords(ctx, batch)
	if err != nil {
		t.Fatalf("UpsertRelayRechargeRecords() error = %v", err)
	}
	if inserted != 3 {
		t.Fatalf("expected 3 inserted rows, got %d", inserted)
	}

	// Re-inserting same IDs must be idempotent (0 new rows, preserves user edits).
	edited := batch[2]
	edited.PaidCNY = 142.50
	edited.Status = RelayRechargeStatusSuccess
	edited.Note = "confirmed with bank fee"
	if err := store.SaveRelayRechargeRecord(ctx, &edited); err != nil {
		t.Fatalf("SaveRelayRechargeRecord() error = %v", err)
	}

	insertedAgain, err := store.UpsertRelayRechargeRecords(ctx, batch)
	if err != nil {
		t.Fatalf("second UpsertRelayRechargeRecords() error = %v", err)
	}
	if insertedAgain != 0 {
		t.Fatalf("expected 0 inserted rows on duplicate sync, got %d", insertedAgain)
	}

	got, err := store.GetRelayRechargeRecord(ctx, edited.ID)
	if err != nil || got == nil {
		t.Fatalf("GetRelayRechargeRecord() got=%v err=%v", got, err)
	}
	if !almostEqualFloat(got.PaidCNY, 142.50) || got.Status != RelayRechargeStatusSuccess {
		t.Fatalf("expected user edit preserved, got %+v", got)
	}

	items, summary, err := store.ListRelayRechargeRecords(ctx, RelayRechargeQuery{Limit: 2})
	if err != nil {
		t.Fatalf("ListRelayRechargeRecords() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected limited 2 items, got %d", len(items))
	}
	if summary.TotalCount != 3 || !almostEqualFloat(summary.TotalPaidCNY, 742.50) {
		t.Fatalf("unexpected summary across all rows: %+v", summary)
	}
	if len(summary.BySite) != 3 {
		t.Fatalf("expected 3 sites in summary, got %d", len(summary.BySite))
	}

	// Filter by site and date range.
	poloItems, poloSummary, err := store.ListRelayRechargeRecords(ctx, RelayRechargeQuery{SiteID: "polo"})
	if err != nil {
		t.Fatalf("ListRelayRechargeRecords(polo) error = %v", err)
	}
	if len(poloItems) != 1 || poloSummary.TotalCount != 1 || !almostEqualFloat(poloSummary.TotalPaidCNY, 500) {
		t.Fatalf("unexpected polo filter result: len=%d summary=%+v", len(poloItems), poloSummary)
	}

	if err := store.DeleteRelayRechargeRecord(ctx, edited.ID); err != nil {
		t.Fatalf("DeleteRelayRechargeRecord() error = %v", err)
	}
	afterDel, err := store.GetRelayRechargeRecord(ctx, edited.ID)
	if err != nil || afterDel != nil {
		t.Fatalf("expected nil after delete, got %+v err=%v", afterDel, err)
	}
}
