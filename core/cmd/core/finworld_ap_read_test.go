//go:build finworld && integration

package main

// The AP tie's read goes through the purchasing module's own seam: the AP aging, the open AP in
// cents as of an instant, grown only by posted vendor bills and open landed cost payables. No
// purchasing SQL lives in cmd/core. This test plants an unpaid bill through the real routes and
// holds the aging honest against it and against a supplier payment settled through the payment
// door.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/db"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"
)

// finworldCallPayBill pays a bill through the production payment door with its replay key. The
// key rides the Idempotency-Key header, so this helper is h.call with one header added.
func finworldCallPayBill(h *finworldHarness, step, billID string, amountCents int64, key string) ([]byte, error) {
	body, err := json.Marshal(map[string]any{
		"amount_cents": amountCents,
		"note":         "ap read probe settlement",
	})
	if err != nil {
		return nil, fmt.Errorf("%s: marshal body: %w", step, err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/purchasing/bills/"+billID+"/payments", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+h.token)
	req.Header.Set(authz.HeaderXOrg, finworldOrgSlug)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	if rec.Code >= 300 {
		return nil, fmt.Errorf("%s: POST bill payment refused with HTTP %d: %s", step, rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	return rec.Body.Bytes(), nil
}

func TestFinWorldOpenAPReadPlantedBill(t *testing.T) {
	ctx := context.Background()
	dsn := finworldTestDSN(t)
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect finworld db: %v", err)
	}
	defer pool.Close()

	finworldResetSchema(t, ctx, pool)
	binPath := finworldBuildBinary(t)
	day0 := time.Date(2025, time.October, 1, 12, 0, 0, 0, houseday.DefaultZone().Location())
	shift0 := finworldSetDayClock(t, finworldTestFaketimeFile(t), day0)
	finworldRunSubprocess(t, binPath, dsn, shift0, nil, "migrate")
	finworldRunSubprocess(t, binPath, dsn, shift0, nil, "seed")
	// Day 0 links the staff and manager subjects to users; without it every route call is a 401.
	finworldRunSubprocess(t, binPath, dsn, shift0, nil, "finworld", "day", "--day", "0")

	h, err := newFinWorldHarness(ctx, pool)
	if err != nil {
		t.Fatalf("harness: %v", err)
	}

	svc := finworldPurchasingComposition(pool, slog.Default(), nil)
	// The aging is the tie's read. This composition wires no landed cost payables and the fresh
	// world plants none, so the total is the open vendor bills alone.
	// The bill's date is a payload column but the payment settles at the process clock, so the
	// as-of instant has to sit past both.
	asOf := time.Now().Add(time.Minute)
	openBefore, err := svc.APAging(ctx, asOf, purchasing.BranchReach{Global: true})
	if err != nil {
		t.Fatalf("open AP read before the plant: %v", err)
	}

	// One vendor, one product, one PO received in full, one bill posted against it: open, unpaid.
	var vendorID, productID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM vendors WHERE is_active ORDER BY vendor_no LIMIT 1`).Scan(&vendorID); err != nil {
		t.Fatalf("vendor: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM products ORDER BY sku LIMIT 1`).Scan(&productID); err != nil {
		t.Fatalf("product: %v", err)
	}
	branchID := finworldBranchTRT

	out, err := h.call("plant PO", http.MethodPost, "/api/v1/purchasing/pos", map[string]any{
		"vendor_id":   vendorID,
		"branch_id":   branchID,
		"expected_at": "2025-10-01",
		"notes":       "ap-read-probe",
		"lines": []map[string]any{{
			"product_id":       productID,
			"description":      "ap read probe",
			"qty_ordered_base": 10.0,
			"uom":              "EA",
			"unit_cost_cents":  int64(2500),
		}},
	})
	if err != nil {
		t.Fatalf("create PO: %v", err)
	}
	var poRes struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &poRes); err != nil {
		t.Fatalf("po response: %v", err)
	}
	if _, err := h.call("place PO", http.MethodPost, "/api/v1/purchasing/pos/"+poRes.ID+"/transition", map[string]any{"to": "ORDERED"}); err != nil {
		t.Fatalf("place PO: %v", err)
	}
	var lineID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM purchasing_po_lines WHERE po_id = $1::uuid ORDER BY line_no LIMIT 1`, poRes.ID).Scan(&lineID); err != nil {
		t.Fatalf("po line: %v", err)
	}
	if _, err := h.call("receive PO", http.MethodPost, "/api/v1/purchasing/pos/"+poRes.ID+"/receive", map[string]any{
		"lines": []map[string]any{{"po_line_id": lineID, "qty_base": 10.0}},
	}); err != nil {
		t.Fatalf("receive PO: %v", err)
	}
	billOut, err := h.call("plant bill", http.MethodPost, "/api/v1/purchasing/bills", map[string]any{
		"bill_no":   "VINV-APREAD-0001",
		"po_id":     poRes.ID,
		"bill_date": "2025-10-01",
		"due_date":  "2025-10-31",
		"tax_cents": int64(0),
		"lines": []map[string]any{{
			"po_line_id":      lineID,
			"description":     "ap read probe",
			"qty_billed_base": 10.0,
			"unit_cost_cents": int64(2500),
		}},
	})
	if err != nil {
		t.Fatalf("create bill: %v", err)
	}
	var billRes struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(billOut, &billRes); err != nil {
		t.Fatalf("bill response: %v", err)
	}
	if _, err := h.call("post bill", http.MethodPost, "/api/v1/purchasing/bills/"+billRes.ID+"/post", map[string]any{"accept_variance": true}); err != nil {
		t.Fatalf("post bill: %v", err)
	}

	// The planted unpaid bill shows in the read: the open AP rose by exactly its 25000.
	openAfterBill, err := svc.APAging(ctx, asOf, purchasing.BranchReach{Global: true})
	if err != nil {
		t.Fatalf("open AP read after the bill: %v", err)
	}
	if openAfterBill.TotalCents-openBefore.TotalCents != 25000 {
		t.Fatalf("the planted unpaid bill moved the open AP by %d, want exactly 25000", openAfterBill.TotalCents-openBefore.TotalCents)
	}

	// A supplier payment settled against the bill drops the read by exactly its amount: the
	// payment door records the paid instalment the aging subtracts. A raw journal on 2100 moves
	// the tie, never the aging, so the settlement rides the real door.
	if _, err := finworldCallPayBill(h, "pay bill", billRes.ID, 10000, "finworld:apread:0001"); err != nil {
		t.Fatalf("pay bill: %v", err)
	}
	openAfterPay, err := svc.APAging(ctx, asOf, purchasing.BranchReach{Global: true})
	if err != nil {
		t.Fatalf("open AP read after the payment: %v", err)
	}
	if openAfterPay.TotalCents != openAfterBill.TotalCents-10000 {
		t.Fatalf("the payment moved the open AP by %d, want exactly 10000", openAfterBill.TotalCents-openAfterPay.TotalCents)
	}
}
