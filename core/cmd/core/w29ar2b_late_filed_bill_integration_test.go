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

// TestIntegration_W29AR2B_LateFiledBillTies proves S2's P2 1 red (FROM-PRINCIPAL 124):
// a document's as of instant is its posting journal's effective instant, the GL's own
// late filing rule. A bill dated inside a CLOSED month, posted today, files into the open
// month, and the GL counts it only from the end of that filed period. The aging must
// count it at exactly the same instants, or the tie breaks: as of the bill's own date
// (inside the closed month) and as of now the bill counts in neither book, and from the
// filed month's end it counts in both. The aging still AGES the bill by its due date
// once it counts; only the inclusion instant moved.
func TestIntegration_W29AR2B_LateFiledBillTies(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "w29ar2late")
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
	thisMonth := time.Date(nowLocal.Year(), nowLocal.Month(), 1, 0, 0, 0, 0, time.UTC)
	lastMonth := thisMonth.AddDate(0, -1, 0)
	// The dealer closed last month. The current month stays open.
	mustExec(t, ctx, pool, `INSERT INTO fiscal_periods (year, month, status, closed_at) VALUES ($1, $2, 'CLOSED', now()) ON CONFLICT (year, month) DO UPDATE SET status = 'CLOSED', closed_at = now()`,
		lastMonth.Year(), int(lastMonth.Month()))
	mustExec(t, ctx, pool, `INSERT INTO fiscal_periods (year, month) VALUES ($1, $2) ON CONFLICT (year, month) DO NOTHING`,
		thisMonth.Year(), int(thisMonth.Month()))

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
		WithGLJournalClock(w4cGLJournalClock{svc: glSvc}).
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

	sCtx := staffctx.ContextWithStaff(ctx, staffctx.StaffContext{
		UserID:   userID,
		RoleCode: "GENERAL_MANAGER",
		Tier:     staffctx.TierLeadership,
		Scope:    staffctx.ScopeGlobal,
		Status:   "ACTIVE",
	})

	sku := fmt.Sprintf("W29AR2B-%d", time.Now().UnixNano())
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
		_, _ = pool.Exec(bg, `DELETE FROM po_lines WHERE product_id = $1`, productID)
		_, _ = pool.Exec(bg, `DELETE FROM purchasing_po_lines WHERE product_id = $1`, productID)
		_, _ = pool.Exec(bg, `DELETE FROM products WHERE id = $1`, productID)
	})
	var billID string
	t.Cleanup(func() {
		bg := context.Background()
		if billID != "" {
			_, _ = pool.Exec(bg, `DELETE FROM purchasing_bill_instalments WHERE bill_id = $1`, billID)
			_, _ = pool.Exec(bg, `DELETE FROM purchasing_bill_lines WHERE bill_id = $1`, billID)
			_, _ = pool.Exec(bg, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT gl_journal_id FROM purchasing_bills WHERE id = $1)`, billID)
			_, _ = pool.Exec(bg, `DELETE FROM gl_journals WHERE id IN (SELECT gl_journal_id FROM purchasing_bills WHERE id = $1)`, billID)
			_, _ = pool.Exec(bg, `UPDATE purchasing_bills SET gl_journal_id = NULL WHERE id = $1`, billID)
			_, _ = pool.Exec(bg, `DELETE FROM purchasing_bills WHERE id = $1`, billID)
		}
	})

	// The baseline at every as of instant, before the bill exists, so the assertions
	// read the bill's own contribution and never the seed's ambient AP.
	lastMonthNoon := time.Date(lastMonth.Year(), lastMonth.Month(), 15, 12, 0, 0, 0, zone)
	billDate := lastMonthNoon.Format("2006-01-02")
	dueDate := lastMonthNoon.AddDate(0, 0, 5).Format("2006-01-02")
	asOfBillDate := lastMonthNoon
	asOfNow := time.Now().UTC().Add(time.Minute)
	asOfAfterFiledMonth := time.Date(thisMonth.Year(), thisMonth.Month(), 1, 0, 0, 0, 1, zone).AddDate(0, 1, 0)
	instants := []time.Time{asOfBillDate, asOfNow, asOfAfterFiledMonth}

	agingBefore := make(map[time.Time]int64)
	glBefore := make(map[time.Time]int64)
	for _, asOf := range instants {
		page, err := purchasingSvc.APAging(sCtx, asOf, purchasing.BranchReach{BranchIDs: []string{branchTRT}})
		if err != nil {
			t.Fatalf("baseline APAging: %v", err)
		}
		agingBefore[asOf] = page.TotalCents
		bal, _, err := glRepo.AccountBalanceAsOf(sCtx, "2100", asOf, branchTRT)
		if err != nil {
			t.Fatalf("baseline AccountBalanceAsOf(2100): %v", err)
		}
		glBefore[asOf] = -bal
	}

	// A 2000 cent bill dated the 15th of the CLOSED month, posted today. The bill
	// journal carries the bill's document date, so the GL files it late into the
	// open month and counts it from that month's end.
	po, err := purchasingSvc.CreatePO(sCtx, purchasing.CreatePORequest{
		VendorID:   vendorID,
		BranchID:   branchTRT,
		ExpectedAt: time.Now().In(zone).Format("2006-01-02"),
		Lines:      []purchasing.AddLineParams{{ProductID: productID, QtyOrderedBase: 1, UOM: "EA", UnitCostCents: 2000}},
	})
	if err != nil {
		t.Fatalf("create PO: %v", err)
	}
	po, err = purchasingSvc.Transition(sCtx, po.ID, purchasing.StatusOrdered)
	if err != nil {
		t.Fatalf("order PO: %v", err)
	}
	if _, err := purchasingSvc.Receive(sCtx, po.ID, purchasing.ReceiveRequest{
		Lines: []purchasing.ReceiveLineParams{{POLineID: po.Lines[0].ID, QtyBase: 1}},
	}); err != nil {
		t.Fatalf("receive PO: %v", err)
	}
	bill, err := purchasingSvc.CreateBill(sCtx, purchasing.CreateBillRequest{
		BillNo:   fmt.Sprintf("W29AR2B-%d", time.Now().UnixNano()),
		POID:     po.ID,
		BillDate: billDate,
		DueDate:  dueDate,
		Lines:    []purchasing.BillLineParams{{POLineID: po.Lines[0].ID, QtyBilledBase: 1, UnitCostCents: 2000}},
	})
	if err != nil {
		t.Fatalf("create bill: %v", err)
	}
	billID = bill.ID
	if _, err := purchasingSvc.PostBill(sCtx, bill.ID, purchasing.PostBillRequest{AcceptVariance: true, Note: "w29ar2b late filed"}); err != nil {
		t.Fatalf("post bill: %v", err)
	}

	var latePosted bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM fiscal_periods lp WHERE lp.id = gl_journals.period_id
			AND date_trunc('month', gl_journals.posted_at AT TIME ZONE 'America/Toronto') != make_date(lp.year, lp.month, 1))
		 FROM gl_journals WHERE id = (SELECT gl_journal_id FROM purchasing_bills WHERE id = $1)`, bill.ID).Scan(&latePosted); err != nil {
		t.Fatalf("read late posted marker: %v", err)
	}
	if !latePosted {
		t.Fatalf("the bill's journal did not file late into the open month; the scenario is not the one under test")
	}

	assertTie := func(step string, asOf time.Time, wantBillCents int64) {
		t.Helper()
		page, err := purchasingSvc.APAging(sCtx, asOf, purchasing.BranchReach{BranchIDs: []string{branchTRT}})
		if err != nil {
			t.Fatalf("%s: APAging: %v", step, err)
		}
		bal, _, err := glRepo.AccountBalanceAsOf(sCtx, "2100", asOf, branchTRT)
		if err != nil {
			t.Fatalf("%s: AccountBalanceAsOf(2100): %v", step, err)
		}
		agingDelta := page.TotalCents - agingBefore[asOf]
		glDelta := -bal - glBefore[asOf]
		if agingDelta != wantBillCents || glDelta != wantBillCents {
			t.Fatalf("W2-9a r2b red (%s): the bill's aging delta = %d and GL 2100 delta = %d, want both %d",
				step, agingDelta, glDelta, wantBillCents)
		}
	}

	// As of the bill's own date (inside the closed month) the journal counts nowhere:
	// it was filed late into the open month, and a closed month's figures are final.
	assertTie("as of the bill's own date", asOfBillDate, 0)
	// As of now (the open month not yet ended) the GL still counts nothing.
	assertTie("as of now", asOfNow, 0)
	// From the end of the filed month the bill counts in both books, and it ages by
	// its due date (the 20th of the closed month, inside the CURRENT bucket there).
	assertTie("as of the filed month's end", asOfAfterFiledMonth, 2000)

	// The route's answer agrees with the service's at the after instant.
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
	req := httptest.NewRequest(http.MethodGet, "/api/v1/purchasing/ap-aging?as_of="+asOfAfterFiledMonth.Format(time.RFC3339Nano), nil)
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
	if page.TotalCents-agingBefore[asOfAfterFiledMonth] != 2000 {
		t.Fatalf("W2-9a r2b red (route at the filed month's end): the bill's aging delta = %d, want 2000",
			page.TotalCents-agingBefore[asOfAfterFiledMonth])
	}
}
