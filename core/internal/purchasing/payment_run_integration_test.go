//go:build integration

package purchasing

// payment_run_integration_test.go, the W3 payment runs legs against the real seams (the same rig the
// W2-9a aging legs run on). The five red legs of FIN-WAVE3-PLAN item 0.9:
//
//  1. a run pays exactly the selected bills once (with the review step taken: a removed bill is not
//     paid and a lowered amount is the amount paid);
//  2. re-paying the run writes nothing;
//  3. the aging and GL 2100 tie after;
//  4. a bill partly paid elsewhere after review is paid only its rest, and a bill paid in full
//     elsewhere is skipped with a reason;
//  5. a clerk is refused on every route, and a branch outside the caller's reach is not found.
//
// The three legs of the PR 702 fix round ride here too, the review of S2 (Opus) on that PR:
//
//  6. an interrupted pay (the bill's payment landed, the run's record of it did not) leaves the run
//     recording what its own key already moved, never the bill's open amount;
//  7. a review naming another branch's bill is refused as not found, with no figures;
//  8. a pay date the books would refuse is refused at propose and at update, in words.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	erpidentity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"github.com/jackc/pgx/v5/pgxpool"
)

// cleanupRun removes a payment run and its lines. Registered after the bills' cleanups, so (LIFO)
// the run's FK rows go before the bills they reference.
func cleanupRun(t *testing.T, pool *pgxpool.Pool, runID string) {
	t.Helper()
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM purchasing_payment_run_lines WHERE run_id = $1`, runID)
		_, _ = pool.Exec(bg, `DELETE FROM purchasing_payment_runs WHERE id = $1`, runID)
	})
}

// postBillForVendor drives one product, PO, receipt, bill and post for an arbitrary vendor, the
// shape every payment run leg needs a posted bill in. The bill total is exactly unitCostCents (one
// unit, no tax), dated billDate and due dueDate, in branchTRT.
func postBillForVendor(t *testing.T, h *harness, vendorID, tag string, unitCostCents int64, billDate, dueDate string) BillDetail {
	t.Helper()
	ctx := context.Background()
	if d, err := time.Parse("2006-01-02", billDate); err == nil {
		_, _ = h.pool.Exec(ctx,
			`INSERT INTO fiscal_periods (year, month) VALUES ($1, $2) ON CONFLICT (year, month) DO NOTHING`,
			d.Year(), int(d.Month()))
	}
	nowLocal := time.Now()
	_, _ = h.pool.Exec(ctx,
		`INSERT INTO fiscal_periods (year, month) VALUES ($1, $2) ON CONFLICT (year, month) DO NOTHING`,
		nowLocal.Year(), int(nowLocal.Month()))

	prod := h.newProduct(t, tag)
	po, err := h.svc.CreatePO(h.ctx, CreatePORequest{
		VendorID: vendorID, BranchID: branchTRT, ExpectedAt: billDate,
		Lines: []AddLineParams{{ProductID: prod, QtyOrderedBase: 1, UOM: "EA", UnitCostCents: unitCostCents}},
	})
	if err != nil {
		t.Fatalf("create PO (%s): %v", tag, err)
	}
	h.cleanupPO(t, po.ID)
	if _, err := h.svc.Transition(h.ctx, po.ID, StatusOrdered); err != nil {
		t.Fatalf("order PO (%s): %v", tag, err)
	}
	if _, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: po.Lines[0].ID, QtyBase: 1}},
	}); err != nil {
		t.Fatalf("receive PO (%s): %v", tag, err)
	}
	bill, err := h.svc.CreateBill(h.ctx, CreateBillRequest{
		BillNo: billNo(tag), POID: po.ID, BillDate: billDate, DueDate: dueDate,
		Lines: []BillLineParams{{POLineID: po.Lines[0].ID, QtyBilledBase: 1, UnitCostCents: unitCostCents}},
	})
	if err != nil {
		t.Fatalf("create bill (%s): %v", tag, err)
	}
	posted, err := h.svc.PostBill(h.ctx, bill.ID, PostBillRequest{AcceptVariance: true, Note: "w3 prun"})
	if err != nil {
		t.Fatalf("post bill (%s): %v", tag, err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = h.pool.Exec(bg, `
			DELETE FROM gl_journal_lines
			 WHERE journal_id IN (SELECT id FROM gl_journals WHERE source_ref_id = $1)`, posted.ID)
		_, _ = h.pool.Exec(bg, `DELETE FROM gl_journals WHERE source_ref_id = $1`, posted.ID)
	})
	return posted
}

// postKGNBill posts one bill in Kingston, the branch a Trenton caller must never reach.
func postKGNBill(t *testing.T, h *harness, tag string, unitCostCents int64, todayStr string) BillDetail {
	t.Helper()
	prod := h.newProduct(t, tag)
	po, err := h.svc.CreatePO(h.ctx, CreatePORequest{
		VendorID: h.vendor, BranchID: branchKGN, ExpectedAt: todayStr,
		Lines: []AddLineParams{{ProductID: prod, QtyOrderedBase: 1, UOM: "EA", UnitCostCents: unitCostCents}},
	})
	if err != nil {
		t.Fatalf("create KGN PO (%s): %v", tag, err)
	}
	h.cleanupPO(t, po.ID)
	if _, err := h.svc.Transition(h.ctx, po.ID, StatusOrdered); err != nil {
		t.Fatalf("order KGN PO (%s): %v", tag, err)
	}
	if _, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: po.Lines[0].ID, QtyBase: 1}},
	}); err != nil {
		t.Fatalf("receive KGN PO (%s): %v", tag, err)
	}
	bill, err := h.svc.CreateBill(h.ctx, CreateBillRequest{
		BillNo: billNo(tag), POID: po.ID, BillDate: todayStr, DueDate: todayStr,
		Lines: []BillLineParams{{POLineID: po.Lines[0].ID, QtyBilledBase: 1, UnitCostCents: unitCostCents}},
	})
	if err != nil {
		t.Fatalf("create KGN bill (%s): %v", tag, err)
	}
	posted, err := h.svc.PostBill(h.ctx, bill.ID, PostBillRequest{AcceptVariance: true, Note: "w3 prun kgn"})
	if err != nil {
		t.Fatalf("post KGN bill (%s): %v", tag, err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = h.pool.Exec(bg, `
			DELETE FROM gl_journal_lines
			 WHERE journal_id IN (SELECT id FROM gl_journals WHERE source_ref_id = $1)`, posted.ID)
		_, _ = h.pool.Exec(bg, `DELETE FROM gl_journals WHERE source_ref_id = $1`, posted.ID)
	})
	return posted
}

// countRunPaymentJournals counts the payment journals the run's own keys wrote (one per paid bill).
func countRunPaymentJournals(t *testing.T, h *harness, runID string) int {
	t.Helper()
	var n int
	if err := h.pool.QueryRow(h.ctx,
		`SELECT count(*) FROM gl_journals WHERE idempotency_key LIKE 'purchasing.bill.payment:run:' || $1 || ':%'`,
		runID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// assertAP2100Tie fails unless the AP aging total for branchTRT equals the net credit balance of
// GL 2100 at the same instant (the W2-9a Sub2 pairing), and equals want.
func assertAP2100Tie(t *testing.T, h *harness, step string, asOf time.Time, want int64) {
	t.Helper()
	page, err := h.svc.APAging(h.ctx, asOf, BranchReach{BranchIDs: []string{branchTRT}})
	if err != nil {
		t.Fatalf("%s: APAging: %v", step, err)
	}
	glRepo := gl.NewRepository(h.pool)
	glBal, _, err := glRepo.AccountBalanceAsOf(h.ctx, "2100", asOf, branchTRT)
	if err != nil {
		t.Fatalf("%s: AccountBalanceAsOf(2100): %v", step, err)
	}
	glCredit := -glBal
	if page.TotalCents != want || glCredit != want {
		t.Fatalf("W3 payment runs red (%s): ap_aging.total_cents=%d gl_2100_credit_cents=%d, want both %d",
			step, page.TotalCents, glCredit, want)
	}
}

// TestIntegration_W3_PaymentRun_PaysSelectedOnce_TiesToGL2100 covers red legs 1 to 3:
// propose selects exactly the open bills due on or before the house day in the run's branch, the
// review removes one and lowers another, the pay pays exactly what remains selected once, a
// re-pay writes nothing, and the aging ties to GL 2100 after.
func TestIntegration_W3_PaymentRun_PaysSelectedOnce_TiesToGL2100(t *testing.T) {
	h := newHarness(t)
	zone, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatalf("load dealer zone: %v", err)
	}
	todayStr := time.Now().In(zone).Format("2006-01-02")
	futureStr := time.Now().In(zone).AddDate(0, 0, 30).Format("2006-01-02")

	// Bill A (50000, due today) and bill B (30000, due today) are due; bill C (20000) is not due
	// for 30 more days; bill D sits in Kingston, another branch's payable.
	billA := postBillForVendor(t, h, h.vendor, "prun-leg1-a", 50000, todayStr, todayStr)
	billB := postBillForVendor(t, h, h.vendor, "prun-leg1-b", 30000, todayStr, todayStr)
	billC := postBillForVendor(t, h, h.vendor, "prun-leg1-c", 20000, todayStr, futureStr)
	billKGN := postKGNBill(t, h, "prun-leg1-kgn", 40000, todayStr)
	_ = billKGN

	// 1a. Propose selects exactly A and B at their open amounts.
	run, err := h.svc.ProposePaymentRun(h.ctx, ProposePaymentRunRequest{
		BranchID: branchTRT, DueOn: todayStr, PayDate: todayStr,
	})
	if err != nil {
		t.Fatalf("W3 payment runs red: ProposePaymentRun: %v", err)
	}
	cleanupRun(t, h.pool, run.ID)
	if run.Status != RunDraft {
		t.Fatalf("W3 payment runs red: proposed run status = %q, want DRAFT", run.Status)
	}
	if len(run.Lines) != 2 {
		t.Fatalf("W3 payment runs red: proposed run has %d lines (%+v), want exactly bills A and B", len(run.Lines), run.Lines)
	}
	for _, ln := range run.Lines {
		switch ln.BillID {
		case billA.ID:
			if ln.AmountCents != 50000 {
				t.Fatalf("W3 payment runs red: bill A proposed at %d, want its open 50000", ln.AmountCents)
			}
			if ln.Status != RunLineSelected {
				t.Fatalf("W3 payment runs red: bill A line status = %q, want SELECTED", ln.Status)
			}
		case billB.ID:
			if ln.AmountCents != 30000 {
				t.Fatalf("W3 payment runs red: bill B proposed at %d, want its open 30000", ln.AmountCents)
			}
		case billC.ID:
			t.Fatalf("W3 payment runs red: bill C (due %s) was proposed, it is not due on or before %s", billC.DueDate, todayStr)
		default:
			t.Fatalf("W3 payment runs red: unexpected bill %s on the proposed run", ln.BillID)
		}
	}

	// 1b. Review: remove bill A, lower bill B to 25000. A lowering above the open amount is refused.
	run, err = h.svc.UpdatePaymentRun(h.ctx, run.ID, UpdatePaymentRunRequest{
		Remove: []string{billA.ID},
		Lower:  []PaymentRunLineAmount{{BillID: billB.ID, AmountCents: 25000}},
	})
	if err != nil {
		t.Fatalf("W3 payment runs red: UpdatePaymentRun: %v", err)
	}
	if len(run.Lines) != 1 || run.Lines[0].BillID != billB.ID || run.Lines[0].AmountCents != 25000 {
		t.Fatalf("W3 payment runs red: reviewed run lines = %+v, want only bill B at 25000", run.Lines)
	}
	if _, err := h.svc.UpdatePaymentRun(h.ctx, run.ID, UpdatePaymentRunRequest{
		Lower: []PaymentRunLineAmount{{BillID: billB.ID, AmountCents: 30001}},
	}); err == nil {
		t.Fatalf("W3 payment runs red: lowering bill B to 30001 above its open 30000 was accepted")
	}
	if _, err := h.svc.UpdatePaymentRun(h.ctx, run.ID, UpdatePaymentRunRequest{
		Remove: []string{billC.ID},
	}); err == nil {
		t.Fatalf("W3 payment runs red: removing bill C, which is not on the run, was accepted")
	}

	// 1c. Pay: exactly bill B, exactly 25000, exactly once.
	paid, err := h.svc.PayPaymentRun(h.ctx, run.ID)
	if err != nil {
		t.Fatalf("W3 payment runs red: PayPaymentRun: %v", err)
	}
	if paid.Status != RunPaid {
		t.Fatalf("W3 payment runs red: paid run status = %q, want PAID", paid.Status)
	}
	if paid.PaidCents != 25000 {
		t.Fatalf("W3 payment runs red: paid run paid_cents = %d, want 25000", paid.PaidCents)
	}
	if len(paid.Lines) != 1 || paid.Lines[0].BillID != billB.ID ||
		paid.Lines[0].Status != RunLinePaid || paid.Lines[0].PaidCents != 25000 {
		t.Fatalf("W3 payment runs red: paid run lines = %+v, want bill B PAID at 25000", paid.Lines)
	}
	var instB int
	if err := h.pool.QueryRow(h.ctx,
		`SELECT count(*) FROM purchasing_bill_instalments WHERE bill_id = $1 AND status = 'PAID'`, billB.ID).Scan(&instB); err != nil {
		t.Fatal(err)
	}
	var instA, instC int
	_ = h.pool.QueryRow(h.ctx, `SELECT count(*) FROM purchasing_bill_instalments WHERE bill_id = $1 AND status = 'PAID'`, billA.ID).Scan(&instA)
	_ = h.pool.QueryRow(h.ctx, `SELECT count(*) FROM purchasing_bill_instalments WHERE bill_id = $1 AND status = 'PAID'`, billC.ID).Scan(&instC)
	if instA != 0 || instC != 0 {
		t.Fatalf("W3 payment runs red: bills outside the reviewed selection were paid (A instalments %d, C instalments %d), want 0 and 0", instA, instC)
	}
	if instB != 1 {
		t.Fatalf("W3 payment runs red: bill B carries %d paid instalments, want exactly 1", instB)
	}
	if n := countRunPaymentJournals(t, h, run.ID); n != 1 {
		t.Fatalf("W3 payment runs red: the run wrote %d payment journals, want exactly 1 (bill B)", n)
	}
	var keyN int
	if err := h.pool.QueryRow(h.ctx,
		`SELECT count(*) FROM gl_journals WHERE idempotency_key = $1`,
		glPaymentKeyPrefix+runKey(run.ID, billB.ID)).Scan(&keyN); err != nil {
		t.Fatal(err)
	}
	if keyN != 1 {
		t.Fatalf("W3 payment runs red: bill B's payment key %s sits on %d journals, want 1",
			runKey(run.ID, billB.ID), keyN)
	}

	// 2. Re-paying the run writes nothing.
	again, err := h.svc.PayPaymentRun(h.ctx, run.ID)
	if err != nil {
		t.Fatalf("W3 payment runs red: re-pay: %v", err)
	}
	if again.Status != RunPaid || again.PaidCents != 25000 || len(again.Lines) != 1 || again.Lines[0].PaidCents != 25000 {
		t.Fatalf("W3 payment runs red: re-pay answered %+v, want the first pay unchanged", again)
	}
	if instB = countPaidInstalments(t, h, billB.ID); instB != 1 {
		t.Fatalf("W3 payment runs red: bill B carries %d paid instalments after the re-pay, want 1", instB)
	}
	if n := countRunPaymentJournals(t, h, run.ID); n != 1 {
		t.Fatalf("W3 payment runs red: the run wrote %d payment journals after the re-pay, want 1", n)
	}
	// A paid run refuses further review.
	if _, err := h.svc.UpdatePaymentRun(h.ctx, run.ID, UpdatePaymentRunRequest{
		Lower: []PaymentRunLineAmount{{BillID: billB.ID, AmountCents: 10000}},
	}); err == nil {
		t.Fatalf("W3 payment runs red: reviewing a PAID run was accepted")
	}

	// 3. The aging and GL 2100 tie after: open AP in Trenton is bill A 50000 (never paid) plus
	// bill B's 5000 rest plus bill C 20000 (not due but still open), exactly 75000.
	assertAP2100Tie(t, h, "after the run paid", time.Now().UTC(), 75000)

	// The run reads back the same through the get door.
	got, err := h.svc.GetPaymentRun(h.ctx, run.ID)
	if err != nil {
		t.Fatalf("W3 payment runs red: GetPaymentRun: %v", err)
	}
	if got.ID != run.ID || got.Status != RunPaid || got.PaidCents != 25000 || len(got.Lines) != 1 {
		t.Fatalf("W3 payment runs red: GetPaymentRun answered %+v, want the paid run", got)
	}
}

func countPaidInstalments(t *testing.T, h *harness, billID string) int {
	t.Helper()
	var n int
	if err := h.pool.QueryRow(h.ctx,
		`SELECT count(*) FROM purchasing_bill_instalments WHERE bill_id = $1 AND status = 'PAID'`, billID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestIntegration_W3_PaymentRun_VendorFilterAndEmptyRefusals covers the proposal's optional vendor
// filter: a run proposed for one vendor never carries another vendor's bill, and a proposal that
// would select nothing is refused in words rather than written as an empty run.
func TestIntegration_W3_PaymentRun_VendorFilterAndEmptyRefusals(t *testing.T) {
	h := newHarness(t)
	zone, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatalf("load dealer zone: %v", err)
	}
	todayStr := time.Now().In(zone).Format("2006-01-02")

	// Another active vendor, with no bills yet.
	var vendor2 string
	if err := h.pool.QueryRow(h.ctx,
		`SELECT id::text FROM vendors WHERE is_active AND id <> $1 ORDER BY name LIMIT 1`, h.vendor).Scan(&vendor2); err != nil {
		t.Skipf("no second seeded vendor: %v", err)
	}

	// Bills of the first vendor are open and due, so a vendor filtered proposal that names vendor2
	// must select nothing and be refused.
	postBillForVendor(t, h, h.vendor, "prun-legvf-a", 12000, todayStr, todayStr)
	if _, err := h.svc.ProposePaymentRun(h.ctx, ProposePaymentRunRequest{
		BranchID: branchTRT, DueOn: todayStr, PayDate: todayStr, VendorID: vendor2,
	}); err == nil {
		t.Fatalf("W3 payment runs red: a proposal selecting no bills was accepted as an empty run")
	}

	// Now give vendor2 a due bill: the filtered proposal selects exactly it.
	billE := postBillForVendor(t, h, vendor2, "prun-legvf-e", 9000, todayStr, todayStr)
	run, err := h.svc.ProposePaymentRun(h.ctx, ProposePaymentRunRequest{
		BranchID: branchTRT, DueOn: todayStr, PayDate: todayStr, VendorID: vendor2,
	})
	if err != nil {
		t.Fatalf("W3 payment runs red: vendor filtered ProposePaymentRun: %v", err)
	}
	cleanupRun(t, h.pool, run.ID)
	if len(run.Lines) != 1 || run.Lines[0].BillID != billE.ID || run.Lines[0].AmountCents != 9000 {
		t.Fatalf("W3 payment runs red: vendor filtered run lines = %+v, want exactly bill E at 9000", run.Lines)
	}
	if run.VendorID != vendor2 {
		t.Fatalf("W3 payment runs red: run.VendorID = %q, want the filter %q", run.VendorID, vendor2)
	}

	// The list answers for the branch and filters by status.
	runs, err := h.svc.ListPaymentRuns(h.ctx, branchTRT, "")
	if err != nil {
		t.Fatalf("W3 payment runs red: ListPaymentRuns: %v", err)
	}
	found := false
	for _, r := range runs {
		if r.ID == run.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("W3 payment runs red: ListPaymentRuns did not answer the proposed run %s", run.ID)
	}
	drafts, err := h.svc.ListPaymentRuns(h.ctx, branchTRT, RunPaid)
	if err != nil {
		t.Fatalf("W3 payment runs red: ListPaymentRuns(PAID): %v", err)
	}
	for _, r := range drafts {
		if r.ID == run.ID {
			t.Fatalf("W3 payment runs red: ListPaymentRuns(PAID) answered DRAFT run %s", run.ID)
		}
	}
}

// TestIntegration_W3_PaymentRun_BillPaidElsewhereAfterReview covers red leg 4: a bill partly paid
// elsewhere between the review and the pay is paid only its rest, and a bill paid in full
// elsewhere is skipped with a reason, never paid twice.
func TestIntegration_W3_PaymentRun_BillPaidElsewhereAfterReview(t *testing.T) {
	h := newHarness(t)
	zone, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatalf("load dealer zone: %v", err)
	}
	todayStr := time.Now().In(zone).Format("2006-01-02")

	billA := postBillForVendor(t, h, h.vendor, "prun-leg4-a", 40000, todayStr, todayStr)
	billB := postBillForVendor(t, h, h.vendor, "prun-leg4-b", 25000, todayStr, todayStr)

	run, err := h.svc.ProposePaymentRun(h.ctx, ProposePaymentRunRequest{
		BranchID: branchTRT, DueOn: todayStr, PayDate: todayStr,
	})
	if err != nil {
		t.Fatalf("W3 payment runs red: ProposePaymentRun: %v", err)
	}
	cleanupRun(t, h.pool, run.ID)
	if len(run.Lines) != 2 {
		t.Fatalf("W3 payment runs red: proposed run lines = %+v, want bills A and B", run.Lines)
	}

	// Between the review and the pay: bill A is partly paid 10000 elsewhere, bill B is paid in
	// full elsewhere.
	if _, err := h.svc.PayBill(h.ctx, billA.ID, PayBillRequest{
		AmountCents: 10000, Note: "elsewhere part",
		IdempotencyKey: "w3-prun-leg4-a-" + billA.ID,
	}); err != nil {
		t.Fatalf("elsewhere part payment on A: %v", err)
	}
	if _, err := h.svc.PayBill(h.ctx, billB.ID, PayBillRequest{
		AmountCents: 25000, Note: "elsewhere in full",
		IdempotencyKey: "w3-prun-leg4-b-" + billB.ID,
	}); err != nil {
		t.Fatalf("elsewhere full payment on B: %v", err)
	}

	paid, err := h.svc.PayPaymentRun(h.ctx, run.ID)
	if err != nil {
		t.Fatalf("W3 payment runs red: PayPaymentRun after elsewhere payments: %v", err)
	}
	if paid.Status != RunPaid || paid.PaidCents != 30000 {
		t.Fatalf("W3 payment runs red: paid run = (%s, %d), want (PAID, 30000)", paid.Status, paid.PaidCents)
	}
	var aLine, bLine *PaymentRunLine
	for i := range paid.Lines {
		switch paid.Lines[i].BillID {
		case billA.ID:
			aLine = &paid.Lines[i]
		case billB.ID:
			bLine = &paid.Lines[i]
		}
	}
	if aLine == nil || aLine.Status != RunLinePaid || aLine.PaidCents != 30000 {
		t.Fatalf("W3 payment runs red: bill A's run line = %+v, want PAID at only its 30000 rest", aLine)
	}
	if bLine == nil || bLine.Status != RunLineSkipped || strings.TrimSpace(bLine.SkipReason) == "" {
		t.Fatalf("W3 payment runs red: bill B's run line = %+v, want SKIPPED with a reason", bLine)
	}
	// Bill A moved exactly twice in total: the 10000 elsewhere and the 30000 rest, and bill B
	// exactly once.
	if got := countPaidInstalments(t, h, billA.ID); got != 2 {
		t.Fatalf("W3 payment runs red: bill A carries %d paid instalments, want 2 (10000 elsewhere + 30000 rest)", got)
	}
	if got := countPaidInstalments(t, h, billB.ID); got != 1 {
		t.Fatalf("W3 payment runs red: bill B carries %d paid instalments, want 1 (the elsewhere payment only)", got)
	}
	var aSum int64
	if err := h.pool.QueryRow(h.ctx,
		`SELECT COALESCE(SUM(amount_cents),0) FROM purchasing_bill_instalments WHERE bill_id = $1 AND status = 'PAID'`,
		billA.ID).Scan(&aSum); err != nil {
		t.Fatal(err)
	}
	if aSum != 40000 {
		t.Fatalf("W3 payment runs red: bill A was paid %d cents in total, want exactly its 40000 total", aSum)
	}
}

// TestIntegration_W3_PaymentRunRoutes_RequireManagerAndFence covers red leg 5 at the doors: a
// frontline clerk is refused on every payment run route, a run in a branch outside the caller's
// reach answers the same 404 as a missing one, and a manager's presses answer.
func TestIntegration_W3_PaymentRunRoutes_RequireManagerAndFence(t *testing.T) {
	h := newHarness(t)
	zone, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatalf("load dealer zone: %v", err)
	}
	todayStr := time.Now().In(zone).Format("2006-01-02")

	// One due bill in Trenton and one in Kingston, both on the books as the global caller.
	billTRT := postBillForVendor(t, h, h.vendor, "prun-leg5-trt", 33000, todayStr, todayStr)
	postKGNBill(t, h, "prun-leg5-kgn", 21000, todayStr)

	// A Kingston run only a Kingston caller may see.
	kgnRun, err := h.svc.ProposePaymentRun(h.ctx, ProposePaymentRunRequest{
		BranchID: branchKGN, DueOn: todayStr, PayDate: todayStr,
	})
	if err != nil {
		t.Fatalf("W3 payment runs red: KGN ProposePaymentRun: %v", err)
	}
	cleanupRun(t, h.pool, kgnRun.ID)

	// Restore HTTP request-context caller resolution, the way the routes serve.
	h.svc.withCaller(identity.StaffContextFromContext)

	sg := testkit.NewStaffGuards(t)
	mux := http.NewServeMux()
	NewHandler(h.svc, slog.Default()).WithUserLinker(func(context.Context, string) (string, bool) {
		return h.user, true
	}).RegisterRoutes(mux, sg.Guards)

	call := func(auth erpidentity.StaffAuthority, method, path, body string) *httptest.ResponseRecorder {
		sg.SetAuthority(auth)
		var rd *strings.Reader
		if body == "" {
			rd = strings.NewReader("")
		} else {
			rd = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, path, rd)
		req.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
		req.Header.Set("X-Org", testkit.OrgNorthSlug)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	clerk := erpidentity.StaffAuthority{
		RoleID:   "COUNTER_SALES",
		Tier:     erpidentity.TierFrontline,
		Scope:    erpidentity.ScopeBranch,
		Branches: []string{branchTRT},
	}
	trtMgr := erpidentity.StaffAuthority{
		RoleID:   "BRANCH_MANAGER",
		Tier:     erpidentity.TierManager,
		Scope:    erpidentity.ScopeBranch,
		Branches: []string{branchTRT},
	}

	proposeBody := fmt.Sprintf(`{"branch_id":%q,"due_on":%q,"pay_date":%q}`, branchTRT, todayStr, todayStr)
	kgnProposeBody := fmt.Sprintf(`{"branch_id":%q,"due_on":%q,"pay_date":%q}`, branchKGN, todayStr, todayStr)

	// 5a. The clerk is refused on all five doors, and a refused propose writes nothing.
	if rec := call(clerk, http.MethodPost, "/api/v1/purchasing/payment-runs", proposeBody); rec.Code != http.StatusForbidden {
		t.Fatalf("W3 payment runs red: clerk propose status = %d (%s), want 403 Forbidden", rec.Code, rec.Body.String())
	}
	if rec := call(clerk, http.MethodGet, "/api/v1/purchasing/payment-runs", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("W3 payment runs red: clerk list status = %d (%s), want 403 Forbidden", rec.Code, rec.Body.String())
	}
	if rec := call(clerk, http.MethodGet, "/api/v1/purchasing/payment-runs/"+kgnRun.ID, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("W3 payment runs red: clerk get status = %d (%s), want 403 Forbidden", rec.Code, rec.Body.String())
	}
	if rec := call(clerk, http.MethodPut, "/api/v1/purchasing/payment-runs/"+kgnRun.ID, `{"remove":[]}`); rec.Code != http.StatusForbidden {
		t.Fatalf("W3 payment runs red: clerk update status = %d (%s), want 403 Forbidden", rec.Code, rec.Body.String())
	}
	if rec := call(clerk, http.MethodPost, "/api/v1/purchasing/payment-runs/"+kgnRun.ID+"/pay", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("W3 payment runs red: clerk pay status = %d (%s), want 403 Forbidden", rec.Code, rec.Body.String())
	}
	var nRuns int
	if err := h.pool.QueryRow(h.ctx, `SELECT count(*) FROM purchasing_payment_runs`).Scan(&nRuns); err != nil {
		t.Fatal(err)
	}
	if nRuns != 1 {
		t.Fatalf("W3 payment runs red: %d runs exist after the clerk presses, want only the KGN run", nRuns)
	}

	// 5b. A Trenton manager cannot reach the Kingston run: the branch named outright is a 403, and
	// the run behind the wall answers the same 404 as a missing one on every id door.
	if rec := call(trtMgr, http.MethodPost, "/api/v1/purchasing/payment-runs", kgnProposeBody); rec.Code != http.StatusForbidden {
		t.Fatalf("W3 payment runs red: TRT manager proposing for KGN status = %d (%s), want 403 Forbidden", rec.Code, rec.Body.String())
	}
	if rec := call(trtMgr, http.MethodGet, "/api/v1/purchasing/payment-runs/"+kgnRun.ID, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("W3 payment runs red: TRT manager get KGN run status = %d (%s), want 404 Not Found", rec.Code, rec.Body.String())
	}
	if rec := call(trtMgr, http.MethodPut, "/api/v1/purchasing/payment-runs/"+kgnRun.ID, `{"remove":["00000000-0000-4000-9000-0000000000aa"]}`); rec.Code != http.StatusNotFound {
		t.Fatalf("W3 payment runs red: TRT manager update KGN run status = %d (%s), want 404 Not Found", rec.Code, rec.Body.String())
	}
	if rec := call(trtMgr, http.MethodPost, "/api/v1/purchasing/payment-runs/"+kgnRun.ID+"/pay", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("W3 payment runs red: TRT manager pay KGN run status = %d (%s), want 404 Not Found", rec.Code, rec.Body.String())
	}

	// 5c. The manager's presses answer: propose 201, get 200, list 200 (and the KGN run is not in
	// a Trenton list), update 200, pay 200 and re-pay 200.
	rec := call(trtMgr, http.MethodPost, "/api/v1/purchasing/payment-runs", proposeBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("W3 payment runs red: manager propose status = %d (%s), want 201 Created", rec.Code, rec.Body.String())
	}
	var proposed PaymentRun
	if err := json.Unmarshal(rec.Body.Bytes(), &proposed); err != nil {
		t.Fatalf("decode propose: %v", err)
	}
	cleanupRun(t, h.pool, proposed.ID)
	if len(proposed.Lines) != 1 || proposed.Lines[0].BillID != billTRT.ID || proposed.Lines[0].AmountCents != 33000 {
		t.Fatalf("W3 payment runs red: manager proposed run lines = %+v, want bill TRT at 33000", proposed.Lines)
	}

	rec = call(trtMgr, http.MethodGet, "/api/v1/purchasing/payment-runs/"+proposed.ID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("W3 payment runs red: manager get status = %d (%s), want 200 OK", rec.Code, rec.Body.String())
	}
	rec = call(trtMgr, http.MethodGet, "/api/v1/purchasing/payment-runs?branch_id="+branchTRT, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("W3 payment runs red: manager list status = %d (%s), want 200 OK", rec.Code, rec.Body.String())
	}
	var listed struct {
		Items []PaymentRunSummary `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	for _, r := range listed.Items {
		if r.ID == kgnRun.ID {
			t.Fatalf("W3 payment runs red: a Trenton list answered the Kingston run %s", kgnRun.ID)
		}
	}
	foundTRT := false
	for _, r := range listed.Items {
		if r.ID == proposed.ID {
			foundTRT = true
		}
	}
	if !foundTRT {
		t.Fatalf("W3 payment runs red: the Trenton list did not answer run %s", proposed.ID)
	}

	updateBody := fmt.Sprintf(`{"lower":[{"bill_id":%q,"amount_cents":30000}]}`, billTRT.ID)
	rec = call(trtMgr, http.MethodPut, "/api/v1/purchasing/payment-runs/"+proposed.ID, updateBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("W3 payment runs red: manager update status = %d (%s), want 200 OK", rec.Code, rec.Body.String())
	}

	rec = call(trtMgr, http.MethodPost, "/api/v1/purchasing/payment-runs/"+proposed.ID+"/pay", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("W3 payment runs red: manager pay status = %d (%s), want 200 OK", rec.Code, rec.Body.String())
	}
	rec = call(trtMgr, http.MethodPost, "/api/v1/purchasing/payment-runs/"+proposed.ID+"/pay", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("W3 payment runs red: manager re-pay status = %d (%s), want 200 OK", rec.Code, rec.Body.String())
	}
	var paidRun PaymentRun
	if err := json.Unmarshal(rec.Body.Bytes(), &paidRun); err != nil {
		t.Fatalf("decode re-pay: %v", err)
	}
	if paidRun.Status != RunPaid || paidRun.PaidCents != 30000 {
		t.Fatalf("W3 payment runs red: re-paid run = (%s, %d), want (PAID, 30000)", paidRun.Status, paidRun.PaidCents)
	}
	if n := countRunPaymentJournals(t, h, proposed.ID); n != 1 {
		t.Fatalf("W3 payment runs red: the run wrote %d payment journals after the re-pay, want 1", n)
	}
}

// TestIntegration_W3_PaymentRun_InterruptedPay_RecordsWhatTheRunPaid is S2's P2 on PR 702, both of
// its probes. A pay that stopped between a bill's payment and the run's record of it left the
// line undecided while the run's own key had already moved the money. The retry read the bill's
// open amount first and took the run's own payment for someone else's: a 10000 the run paid was
// recorded SKIPPED, and a 7000 it paid was recorded PAID 3000.
//
// THE LINE'S OWN KEY DECIDES THE LINE. The journal under run:<run id>:<bill id> is the truth
// about what that line moved, so it is read before any open amount.
func TestIntegration_W3_PaymentRun_InterruptedPay_RecordsWhatTheRunPaid(t *testing.T) {
	h := newHarness(t)
	zone, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatalf("load dealer zone: %v", err)
	}
	todayStr := time.Now().In(zone).Format("2006-01-02")
	paidAt, err := time.ParseInLocation("2006-01-02", todayStr, zone)
	if err != nil {
		t.Fatalf("parse the house day: %v", err)
	}

	// Probe A, the full amount: the run's key paid the whole 10000 bill. Probe B, the part
	// amount: the bill is 10000 and the review lowers the line to 7000.
	billA := postBillForVendor(t, h, h.vendor, "prun-intr-a", 10000, todayStr, todayStr)
	billB := postBillForVendor(t, h, h.vendor, "prun-intr-b", 10000, todayStr, todayStr)

	run, err := h.svc.ProposePaymentRun(h.ctx, ProposePaymentRunRequest{
		BranchID: branchTRT, DueOn: todayStr, PayDate: todayStr,
	})
	if err != nil {
		t.Fatalf("W3 payment runs red: ProposePaymentRun: %v", err)
	}
	cleanupRun(t, h.pool, run.ID)
	if len(run.Lines) != 2 {
		t.Fatalf("W3 payment runs red: proposed run has %d lines, want bills A and B", len(run.Lines))
	}

	// The review lowers bill B to 7000, the part amount probe.
	run, err = h.svc.UpdatePaymentRun(h.ctx, run.ID, UpdatePaymentRunRequest{
		Lower: []PaymentRunLineAmount{{BillID: billB.ID, AmountCents: 7000}},
	})
	if err != nil {
		t.Fatalf("W3 payment runs red: UpdatePaymentRun: %v", err)
	}

	// THE INTERRUPTED PAY: each bill's payment lands under the run's own key, and the run's
	// record of the line never lands.
	if _, err := h.svc.PayBill(h.ctx, billA.ID, PayBillRequest{
		AmountCents: 10000, PaidAt: paidAt, Note: "Payment run " + run.ID,
		IdempotencyKey: runKey(run.ID, billA.ID),
	}); err != nil {
		t.Fatalf("W3 payment runs red: the interrupted payment on bill A: %v", err)
	}
	if _, err := h.svc.PayBill(h.ctx, billB.ID, PayBillRequest{
		AmountCents: 7000, PaidAt: paidAt, Note: "Payment run " + run.ID,
		IdempotencyKey: runKey(run.ID, billB.ID),
	}); err != nil {
		t.Fatalf("W3 payment runs red: the interrupted payment on bill B: %v", err)
	}

	// The pay is pressed again. The run records what its own keys already moved.
	paid, err := h.svc.PayPaymentRun(h.ctx, run.ID)
	if err != nil {
		t.Fatalf("W3 payment runs red: PayPaymentRun after the interrupted pay: %v", err)
	}
	var aLine, bLine *PaymentRunLine
	for i := range paid.Lines {
		switch paid.Lines[i].BillID {
		case billA.ID:
			aLine = &paid.Lines[i]
		case billB.ID:
			bLine = &paid.Lines[i]
		}
	}
	if aLine == nil || aLine.Status != RunLinePaid || aLine.PaidCents != 10000 {
		t.Fatalf("W3 payment runs red: bill A's run line = %+v, want PAID at the 10000 its own key moved", aLine)
	}
	if bLine == nil || bLine.Status != RunLinePaid || bLine.PaidCents != 7000 {
		t.Fatalf("W3 payment runs red: bill B's run line = %+v, want PAID at the 7000 its own key moved", bLine)
	}
	if paid.Status != RunPaid || paid.PaidCents != 17000 {
		t.Fatalf("W3 payment runs red: paid run = (%s, %d), want (PAID, 17000)", paid.Status, paid.PaidCents)
	}

	// The books moved once each: one instalment per bill, one journal per key, and the money
	// the run recorded is the money that moved.
	for _, tc := range []struct {
		billID string
		want   int64
	}{{billA.ID, 10000}, {billB.ID, 7000}} {
		if got := countPaidInstalments(t, h, tc.billID); got != 1 {
			t.Fatalf("W3 payment runs red: bill %s carries %d paid instalments, want exactly 1", tc.billID, got)
		}
		var sum int64
		if err := h.pool.QueryRow(h.ctx,
			`SELECT COALESCE(SUM(amount_cents),0) FROM purchasing_bill_instalments WHERE bill_id = $1 AND status = 'PAID'`,
			tc.billID).Scan(&sum); err != nil {
			t.Fatal(err)
		}
		if sum != tc.want {
			t.Fatalf("W3 payment runs red: bill %s was paid %d cents, want exactly %d", tc.billID, sum, tc.want)
		}
	}
	if n := countRunPaymentJournals(t, h, run.ID); n != 2 {
		t.Fatalf("W3 payment runs red: the run's keys sit on %d journals, want 2 (bill A and bill B, one each)", n)
	}
}
