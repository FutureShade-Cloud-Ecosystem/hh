//go:build integration

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	staffctx "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/landedcost"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"
)

// TestIntegration_W3P0A_LateFiledBillApplicationWindow proves S6 on 704's P3-2: an application
// counts only when its bill counts too. A bill dated inside a CLOSED month, posted today, files
// late into the open month and counts in neither book until that month ends. A credit posted
// today counts now. When the credit is applied to that bill today, the application must count
// NOWHERE until the bill counts: as of now the aging carries the credit's whole open as its
// negative item (the application cannot have eaten it), and only from the filed month's end does
// the application net the bill's open against the credit's. If the application counted at its own
// applied_at, the credit's open would drop by 800 while neither book counts the bill, and the
// aging would sit under GL 2100 by the application's cents through the whole rest of the month.
func TestIntegration_W3P0A_LateFiledBillApplicationWindow(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "w3p0alate")
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
		WithGLPoster(landedGLPoster{svc: glSvc}).
		WithJournalClock(w4cLandedJournalClock{svc: glSvc})
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

	sku := fmt.Sprintf("W3P0A-%d", time.Now().UnixNano())
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

	// The story's own rows leave with the test: the credit's applications, its journal, the bill's
	// instalments, lines and journals, then the rows themselves.
	var billID, creditID string
	t.Cleanup(func() {
		bg := context.Background()
		if creditID != "" {
			_, _ = pool.Exec(bg, `DELETE FROM purchasing_vendor_credit_applications WHERE credit_id = $1`, creditID)
			_, _ = pool.Exec(bg, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE source_ref_id = $1 AND source = 'PO_RECEIPT' AND memo LIKE 'Vendor credit%')`, creditID)
			_, _ = pool.Exec(bg, `DELETE FROM gl_journals WHERE source_ref_id = $1 AND source = 'PO_RECEIPT' AND memo LIKE 'Vendor credit%'`, creditID)
			_, _ = pool.Exec(bg, `UPDATE purchasing_vendor_credits SET gl_journal_id = NULL WHERE id = $1`, creditID)
			_, _ = pool.Exec(bg, `DELETE FROM purchasing_vendor_credits WHERE id = $1`, creditID)
		}
		if billID != "" {
			_, _ = pool.Exec(bg, `DELETE FROM purchasing_vendor_credit_applications WHERE bill_id = $1`, billID)
			_, _ = pool.Exec(bg, `DELETE FROM purchasing_bill_instalments WHERE bill_id = $1`, billID)
			_, _ = pool.Exec(bg, `DELETE FROM purchasing_bill_lines WHERE bill_id = $1`, billID)
			_, _ = pool.Exec(bg, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT gl_journal_id FROM purchasing_bills WHERE id = $1)`, billID)
			_, _ = pool.Exec(bg, `DELETE FROM gl_journals WHERE id IN (SELECT gl_journal_id FROM purchasing_bills WHERE id = $1)`, billID)
			_, _ = pool.Exec(bg, `UPDATE purchasing_bills SET gl_journal_id = NULL WHERE id = $1`, billID)
			_, _ = pool.Exec(bg, `DELETE FROM purchasing_bills WHERE id = $1`, billID)
		}
	})

	// The baseline at every as of instant, before the story's documents exist, so each assertion
	// reads the story's own contribution and never the seed's ambient AP.
	lastMonthNoon := time.Date(lastMonth.Year(), lastMonth.Month(), 15, 12, 0, 0, 0, zone)
	billDate := lastMonthNoon.Format("2006-01-02")
	dueDate := lastMonthNoon.AddDate(0, 0, 5).Format("2006-01-02")
	todayStr := time.Now().In(zone).Format("2006-01-02")
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

	// A 2000 cent bill dated the 15th of the CLOSED month, posted today: its journal keeps the
	// document date on posted_at and files late into the open month, so it counts in neither book
	// until that month ends.
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
		BillNo:   fmt.Sprintf("W3P0A-B-%d", time.Now().UnixNano()),
		POID:     po.ID,
		BillDate: billDate,
		DueDate:  dueDate,
		Lines:    []purchasing.BillLineParams{{POLineID: po.Lines[0].ID, QtyBilledBase: 1, UnitCostCents: 2000}},
	})
	if err != nil {
		t.Fatalf("create bill: %v", err)
	}
	billID = bill.ID
	if _, err := purchasingSvc.PostBill(sCtx, bill.ID, purchasing.PostBillRequest{AcceptVariance: true, Note: "w3p0a late filed"}); err != nil {
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

	// A 1200 cent credit dated and posted today: it counts now, and 800 of it is applied to the
	// late filed bill.
	credit, err := purchasingSvc.CreateVendorCredit(sCtx, purchasing.CreateVendorCreditRequest{
		VendorID:   vendorID,
		BranchID:   branchTRT,
		CreditNo:   fmt.Sprintf("W3P0A-C-%d", time.Now().UnixNano()),
		CreditDate: todayStr,
		Lines:      []purchasing.VendorCreditLineParams{{Description: "w3p0a late filed application", AmountCents: 1200}},
	})
	if err != nil {
		t.Fatalf("create credit: %v", err)
	}
	creditID = credit.ID
	if _, err := purchasingSvc.PostVendorCredit(sCtx, credit.ID); err != nil {
		t.Fatalf("post credit: %v", err)
	}
	if _, err := purchasingSvc.ApplyVendorCredit(sCtx, credit.ID, purchasing.ApplyVendorCreditRequest{
		BillID: bill.ID, AmountCents: 800, IdempotencyKey: fmt.Sprintf("w3p0a-apply-%d", time.Now().UnixNano()),
	}); err != nil {
		t.Fatalf("apply the credit to the late filed bill: %v", err)
	}

	assertTie := func(step string, asOf time.Time, wantCents int64) {
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
		if agingDelta != wantCents || glDelta != wantCents {
			t.Fatalf("W3-P0A red (%s): the story's aging delta = %d and GL 2100 delta = %d, want both %d",
				step, agingDelta, glDelta, wantCents)
		}
	}

	// As of the bill's own date (inside the closed month) nothing counts: the credit was not yet
	// given and the bill's journal counts from the filed month's end.
	assertTie("as of the bill's own date", asOfBillDate, 0)
	// As of now the credit counts and the bill does not: the aging carries the credit's whole
	// 1200 as its negative item, the application netting nothing until the bill counts.
	assertTie("as of now, inside the filed month", asOfNow, -1200)
	// From the filed month's end the bill counts with the application netted against it: the
	// bill's 1200 open less the credit's remaining 400 of on account, 800 in both books.
	assertTie("as of the filed month's end", asOfAfterFiledMonth, 800)
}
