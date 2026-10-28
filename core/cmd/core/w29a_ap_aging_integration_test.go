//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	erpidentity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	staffctx "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/landedcost"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestIntegration_W29A_APAging_CompositionRootTieAndRoute(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "w29a")
	body, err := seedFS.ReadFile("dibbits_seed.sql")
	if err != nil {
		t.Fatalf("read embedded seed: %v", err)
	}
	if _, err := pool.Exec(ctx, string(body)); err != nil {
		t.Fatalf("apply seed: %v", err)
	}

	const branchTRT = "5d1b3f6e-0000-4000-9000-000000000001"

	zone, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatalf("load zone: %v", err)
	}
	nowLocal := time.Now().In(zone)
	mustExec(t, ctx, pool, `INSERT INTO fiscal_periods (year, month) VALUES ($1, $2) ON CONFLICT (year, month) DO NOTHING`,
		nowLocal.Year(), int(nowLocal.Month()))

	glRepo := gl.NewRepository(pool).WithZone(newDealerZone(pool))
	glSvc := gl.NewService(glRepo)
	inventorySvc := inventory.NewService(inventory.NewRepository(pool)).
		WithGLPoster(invGLPoster{svc: glSvc}).
		WithRuleResolver(glRuleAdapter{svc: glSvc})
	landedSvc := landedcost.NewService(landedcost.NewRepository(pool)).
		WithCapitalizer(landedAdapter{svc: inventorySvc}).
		WithGLPoster(landedGLPoster{svc: glSvc})
	purchasingSvc := purchasing.NewService(purchasing.NewRepository(pool)).
		WithZone(newDealerZone(pool)).
		WithStockReceiver(w4cStockReceiver{svc: inventorySvc}).
		WithVendors(w4cVendorDirectory{pool: pool}).
		WithLandedCoster(w4cLandedCoster{svc: landedSvc}).
		WithLandedCostPayables(w4cLandedCoster{svc: landedSvc}).
		WithSupplierPayments(w4cSupplierPaymentReader{pool: pool}).
		WithGLPoster(w4cGLPoster{svc: glSvc}).
		WithRuleResolver(glRuleAdapter{svc: glSvc}).
		WithBranches(item5BranchDirectory{pool: pool}).
		WithVendorAddresses(item5VendorAddressBook{pool: pool}).
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))

	var userID, vendorID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users ORDER BY id LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("no seeded user: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM vendors WHERE is_active ORDER BY id LIMIT 1`).Scan(&vendorID); err != nil {
		t.Fatalf("no seeded vendor: %v", err)
	}

	sku := fmt.Sprintf("W29ACMD-%d", time.Now().UnixNano())
	var productID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO products (sku, name, base_uom, price_uom, is_active) VALUES ($1, $2, 'EA', 'EA', TRUE) RETURNING id::text`,
		sku, sku).Scan(&productID); err != nil {
		t.Fatalf("create product: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM stock_moves WHERE product_id = $1`, productID)
		_, _ = pool.Exec(bg, `DELETE FROM stock_balances WHERE product_id = $1`, productID)
		_, _ = pool.Exec(bg, `DELETE FROM product_branch_costs WHERE product_id = $1`, productID)
		_, _ = pool.Exec(bg, `DELETE FROM landed_cost_allocations WHERE product_id = $1`, productID)
		_, _ = pool.Exec(bg, `DELETE FROM po_lines WHERE product_id = $1`, productID)
		_, _ = pool.Exec(bg, `DELETE FROM purchasing_po_lines WHERE product_id = $1`, productID)
		_, _ = pool.Exec(bg, `DELETE FROM products WHERE id = $1`, productID)
	})

	sCtx := staffctx.ContextWithStaff(ctx, staffctx.StaffContext{
		UserID:   userID,
		RoleCode: "GENERAL_MANAGER",
		Tier:     staffctx.TierLeadership,
		Scope:    staffctx.ScopeGlobal,
		Status:   "ACTIVE",
	})

	todayStr := nowLocal.Format("2006-01-02")
	po, err := purchasingSvc.CreatePO(sCtx, purchasing.CreatePORequest{
		VendorID:   vendorID,
		BranchID:   branchTRT,
		ExpectedAt: todayStr,
		Lines: []purchasing.AddLineParams{
			{ProductID: productID, QtyOrderedBase: 10, UOM: "EA", UnitCostCents: 4000},
		},
	})
	if err != nil {
		t.Fatalf("create PO: %v", err)
	}

	var stockDocID, lcID, billID, extPayKey string
	t.Cleanup(func() {
		bg := context.Background()
		if extPayKey != "" {
			_, _ = pool.Exec(bg, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE idempotency_key = $1)`, extPayKey)
			_, _ = pool.Exec(bg, `DELETE FROM gl_journals WHERE idempotency_key = $1`, extPayKey)
		}
		if lcID != "" {
			_, _ = pool.Exec(bg, `DELETE FROM landed_cost_allocations WHERE landed_cost_id = $1`, lcID)
			_, _ = pool.Exec(bg, `UPDATE purchasing_receipts SET landed_cost_id = NULL WHERE landed_cost_id = $1`, lcID)
			_, _ = pool.Exec(bg, `UPDATE landed_costs SET gl_journal_id = NULL WHERE id = $1`, lcID)
			_, _ = pool.Exec(bg, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE idempotency_key IN ('landedcost.apply:' || $1, 'landedcost.void:' || $1))`, lcID)
			_, _ = pool.Exec(bg, `DELETE FROM gl_journals WHERE idempotency_key IN ('landedcost.apply:' || $1, 'landedcost.void:' || $1)`, lcID)
			_, _ = pool.Exec(bg, `DELETE FROM landed_costs WHERE id = $1`, lcID)
		}
		if billID != "" {
			_, _ = pool.Exec(bg, `DELETE FROM purchasing_bill_instalments WHERE bill_id = $1`, billID)
			_, _ = pool.Exec(bg, `DELETE FROM purchasing_bill_lines WHERE bill_id = $1`, billID)
			_, _ = pool.Exec(bg, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT gl_journal_id FROM purchasing_bills WHERE id = $1)`, billID)
			_, _ = pool.Exec(bg, `DELETE FROM gl_journals WHERE id IN (SELECT gl_journal_id FROM purchasing_bills WHERE id = $1)`, billID)
			_, _ = pool.Exec(bg, `UPDATE purchasing_bills SET gl_journal_id = NULL WHERE id = $1`, billID)
			_, _ = pool.Exec(bg, `DELETE FROM purchasing_bills WHERE id = $1`, billID)
		}
		_, _ = pool.Exec(bg, `DELETE FROM purchasing_receipt_lines USING purchasing_receipts r WHERE purchasing_receipt_lines.receipt_id = r.id AND r.po_id = $1`, po.ID)
		_, _ = pool.Exec(bg, `DELETE FROM purchasing_receipts WHERE po_id = $1`, po.ID)
		if stockDocID != "" {
			_, _ = pool.Exec(bg, `DELETE FROM po_receipts WHERE po_id = $1`, stockDocID)
			_, _ = pool.Exec(bg, `DELETE FROM po_lines WHERE po_id = $1`, stockDocID)
			_, _ = pool.Exec(bg, `DELETE FROM purchase_orders WHERE id = $1`, stockDocID)
		}
		_, _ = pool.Exec(bg, `DELETE FROM purchasing_po_lines WHERE po_id = $1`, po.ID)
		_, _ = pool.Exec(bg, `DELETE FROM purchasing_pos WHERE id = $1`, po.ID)
	})

	po, err = purchasingSvc.Transition(sCtx, po.ID, purchasing.StatusOrdered)
	if err != nil {
		t.Fatalf("order PO: %v", err)
	}
	if _, err := purchasingSvc.Receive(sCtx, po.ID, purchasing.ReceiveRequest{
		Lines: []purchasing.ReceiveLineParams{{POLineID: po.Lines[0].ID, QtyBase: 10}},
	}); err != nil {
		t.Fatalf("receive PO: %v", err)
	}
	if err := pool.QueryRow(sCtx, `SELECT stock_doc_id::text FROM purchasing_receipts WHERE po_id = $1 LIMIT 1`, po.ID).Scan(&stockDocID); err != nil {
		t.Fatalf("lookup stock_doc_id: %v", err)
	}

	bill, err := purchasingSvc.CreateBill(sCtx, purchasing.CreateBillRequest{
		BillNo:   fmt.Sprintf("W29A-CMD-%d", time.Now().UnixNano()),
		POID:     po.ID,
		BillDate: todayStr,
		DueDate:  todayStr,
		Lines: []purchasing.BillLineParams{
			{POLineID: po.Lines[0].ID, QtyBilledBase: 10, UnitCostCents: 4000},
		},
	})
	if err != nil {
		t.Fatalf("create bill: %v", err)
	}
	billID = bill.ID
	posted, err := purchasingSvc.PostBill(sCtx, bill.ID, purchasing.PostBillRequest{AcceptVariance: true, Note: "w29a cmd"})
	if err != nil {
		t.Fatalf("post bill: %v", err)
	}

	// Post an external supplier payment journal (12500 cents debit to 2100, matching the month-walk pattern)
	// and an open landed cost (8500 cents freight) through the composition-root adapters.
	extPayKey = "w29a:ext:vpay:" + posted.ID
	if _, err := glSvc.PostExternal(sCtx, gl.ExternalJournal{
		Source:         "PAYMENT",
		SourceRefID:    posted.ID,
		IdempotencyKey: extPayKey,
		Memo:           "External supplier cheque " + posted.BillNo,
		BranchID:       branchTRT,
		DocumentDate:   time.Now().UTC(),
		Lines: []gl.ExternalLine{
			{AccountCode: "2100", DebitCents: 12500, Memo: "AP settlement"},
			{AccountCode: "1000", CreditCents: 12500, Memo: "Cheque written"},
		},
	}); err != nil {
		t.Fatalf("post external payment: %v", err)
	}

	lcID, err = (w4cLandedCoster{svc: landedSvc}).CreateAndApply(sCtx, purchasing.LandedCostDraft{
		POID:         stockDocID,
		PORef:        po.PONo,
		BranchID:     branchTRT,
		Currency:     "CAD",
		FXRate:       1.0,
		FreightCents: 8500,
		Basis:        landedcost.BasisValue,
	})
	if err != nil {
		t.Fatalf("CreateAndApply landed cost: %v", err)
	}

	// Verify GET /api/v1/purchasing/ap-aging ties to GL 2100:
	// Bill 40000 - external payment 12500 + open landed cost 8500 = 36000 cents.
	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(erpidentity.StaffAuthority{
		RoleID: "CONTROLLER",
		Tier:   erpidentity.TierLeadership,
		Scope:  erpidentity.ScopeGlobal,
	})
	mux := http.NewServeMux()
	purchasing.NewHandler(purchasingSvc, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithUserLinker(func(context.Context, string) (string, bool) { return userID, true }).
		RegisterRoutes(mux, sg.Guards)

	asOf := time.Now().UTC().Add(10 * time.Millisecond)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/purchasing/ap-aging?as_of="+asOf.Format(time.RFC3339Nano), nil)
	req.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
	req.Header.Set("X-Org", testkit.OrgNorthSlug)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/purchasing/ap-aging status = %d: %s", rec.Code, rec.Body.String())
	}
	var page purchasing.APAgingPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	glBal, _, err := glRepo.AccountBalanceAsOf(sCtx, "2100", asOf, branchTRT)
	if err != nil {
		t.Fatalf("AccountBalanceAsOf(2100): %v", err)
	}
	if page.TotalCents != -glBal || page.TotalCents != 36000 {
		t.Fatalf("W29A composition tie: page.TotalCents=%d -glBal=%d, want 36000", page.TotalCents, -glBal)
	}
}
