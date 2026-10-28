//go:build finworld && integration

package main

// The AP tie's read goes through the purchasing module's own seam: the open AP in cents as of an
// instant, posted bills less the supplier payments settled against them, with the settlement side
// injected by the composition over the ledger. No purchasing SQL lives in cmd/core. This test plants
// an unpaid bill through the real routes and holds the read honest against it and against a payment.

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/db"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

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
	// The bill's date is a payload column but the payment journal posts at the process clock, so the
	// as-of instant has to sit past both.
	asOf := time.Now().Add(time.Minute)
	openBefore, err := svc.OpenAPCentsAsOf(ctx, asOf)
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
	openAfterBill, err := svc.OpenAPCentsAsOf(ctx, asOf)
	if err != nil {
		t.Fatalf("open AP read after the bill: %v", err)
	}
	if openAfterBill-openBefore != 25000 {
		t.Fatalf("the planted unpaid bill moved the open AP by %d, want exactly 25000", openAfterBill-openBefore)
	}

	// A supplier payment settled against trade payables drops the read by exactly its amount.
	if _, err := h.money.glSvc.PostExternal(ctx, gl.ExternalJournal{
		Source:         "PAYMENT",
		IdempotencyKey: "finworld:apread:0001",
		Memo:           "ap read probe supplier cheque",
		BranchID:       branchID,
		Lines: []gl.ExternalLine{
			{AccountCode: "2100", DebitCents: 10000, Memo: "settlement"},
			{AccountCode: "1000", CreditCents: 10000, Memo: "cheque"},
		},
	}); err != nil {
		t.Fatalf("payment journal: %v", err)
	}
	openAfterPay, err := svc.OpenAPCentsAsOf(ctx, asOf)
	if err != nil {
		t.Fatalf("open AP read after the payment: %v", err)
	}
	if openAfterPay != openAfterBill-10000 {
		t.Fatalf("the payment moved the open AP by %d, want exactly 10000", openAfterBill-openAfterPay)
	}
}
