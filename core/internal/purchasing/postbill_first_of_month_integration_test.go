//go:build integration

// postbill_first_of_month_integration_test.go, FW1-11 round 4: a document date is a CALENDAR DAY in
// the house zone, not midnight at Greenwich. A bill dated the FIRST of a month is the case a
// Greenwich midnight gets wrong: midnight at Greenwich on the first is the previous evening in the
// house zone, so the ledger filed the bill's journal into the month BEFORE the bill's own, and the
// tax tie-out showed a variance in both months (the documents side counted the input tax credit in
// the bill's month, the ledger side debited 2200 in the previous one).
//
// The leg drives the real PostBill (the harness's real gl.Service poster) with a bill dated
// 2026-03-01 and asserts the three answers at once: the journal files into 2026-03, the input tax
// credit the reconcile reads by bill_date and the 2200 debit land in that same month, and the
// journal's own posted_at carries the bill's own house day.
//
// EVERY DATE IN THIS FILE IS FIXED and no real clock is read here, so the leg cannot age into a
// dated time bomb (internal/testaudit's tripwire).
//
//	Run: DATABASE_URL='postgres://.../db?sslmode=disable' go test -tags=integration -count=1 \
//	     -run 'PostBillDatedTheFirstOfAMonth' ./internal/purchasing/

package purchasing

import (
	"context"
	"testing"
)

// TestIntegration_PostBillDatedTheFirstOfAMonthFilesIntoThatMonth is the house zone document date,
// at the boundary that shows it: a bill dated the first of a month files into THAT month.
func TestIntegration_PostBillDatedTheFirstOfAMonthFilesIntoThatMonth(t *testing.T) {
	h := newHarness(t)
	repo := NewRepository(h.pool)

	// The bill's own day, and the month it belongs to. The predecessor month is open as well, so a
	// journal that filed into the wrong month would be visible there instead of quietly landing
	// late in this one and hiding the defect.
	const (
		billY   = 2026
		billM   = 3
		billDay = "2026-03-01"
		billTax = int64(650)
	)
	fw11pdEnsurePeriod(t, h, billY, billM)
	fw11pdEnsurePeriod(t, h, billY, billM-1)

	itcBefore, err := repo.VendorBillITCCents(h.ctx, billY, billM, branchTRT)
	if err != nil {
		t.Fatalf("baseline ITC read: %v", err)
	}
	glBefore, err := h.gl.GLBalanceCents(h.ctx, "2200", billY, billM, branchTRT)
	if err != nil {
		t.Fatalf("baseline GL 2200 read: %v", err)
	}
	// The predecessor month, read before the post: a journal filed into the month before the bill's
	// own shows here, and this is where the defect used to land.
	prevGLBefore, err := h.gl.GLBalanceCents(h.ctx, "2200", billY, billM-1, branchTRT)
	if err != nil {
		t.Fatalf("baseline predecessor GL 2200 read: %v", err)
	}
	prevITCBefore, err := repo.VendorBillITCCents(h.ctx, billY, billM-1, branchTRT)
	if err != nil {
		t.Fatalf("baseline predecessor ITC read: %v", err)
	}

	product := h.newProduct(t, "firstofmonth")
	po := h.orderedPO(t, product, 100, 2500)
	lineID := po.Lines[0].ID
	if _, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: lineID, QtyBase: 100}},
	}); err != nil {
		t.Fatalf("receive: %v", err)
	}
	bill, err := h.svc.CreateBill(h.ctx, CreateBillRequest{
		BillNo: billNo("firstofmonth"), POID: po.ID, BillDate: billDay,
		TaxCents: billTax,
		Lines:    []BillLineParams{{POLineID: lineID, QtyBilledBase: 100, UnitCostCents: 2500}},
	})
	if err != nil {
		t.Fatalf("create bill: %v", err)
	}
	posted, err := h.svc.PostBill(h.ctx, bill.ID, PostBillRequest{AcceptVariance: true, Note: "fw1-11 first of the month"})
	if err != nil {
		t.Fatalf("post bill: %v", err)
	}
	var journalID string
	if err := h.pool.QueryRow(h.ctx,
		`SELECT gl_journal_id::text FROM purchasing_bills WHERE id = $1::uuid`, posted.ID).Scan(&journalID); err != nil {
		t.Fatalf("read the bill's journal: %v", err)
	}
	if journalID == "" {
		t.Fatal("PostBill left no journal on the bill")
	}

	// The ledger side: the journal files into the BILL'S month, not the month before it.
	jy, jm := fw11pdJournalPeriod(t, h, journalID)
	if jy != billY || jm != billM {
		t.Errorf("FW1-11 house zone document date: the bill dated %s filed its journal into %04d-%02d, want the bill's own month %04d-%02d (a document date is a calendar day in the house zone, and midnight at Greenwich on the first is the previous evening there)",
			billDay, jy, jm, billY, billM)
	}

	// The journal carries the bill's own house day on posted_at, which is the document date the
	// ledger files by. The bill's day is the first, so the instant is that day's house midnight and
	// nothing before it.
	var postedDay string
	if err := h.pool.QueryRow(h.ctx,
		`SELECT to_char(posted_at AT TIME ZONE 'America/Toronto', 'YYYY-MM-DD') FROM gl_journals WHERE id = $1::uuid`,
		journalID).Scan(&postedDay); err != nil {
		t.Fatalf("read the journal's posted day: %v", err)
	}
	if postedDay != billDay {
		t.Errorf("FW1-11 house zone document date: the journal's posted day reads %s, want the bill's own day %s", postedDay, billDay)
	}

	// The two sides of the tie-out, in the bill's month: the input tax credit the reconcile reads
	// by bill_date and the 2200 debit the ledger took. They move together or the month shows a
	// variance.
	itcAfter, err := repo.VendorBillITCCents(h.ctx, billY, billM, branchTRT)
	if err != nil {
		t.Fatalf("ITC read after the post: %v", err)
	}
	itcDelta := itcAfter - itcBefore
	if itcDelta != billTax {
		t.Errorf("FW1-11 house zone document date: the ITC line moved %d for %04d-%02d, want the bill's %d", itcDelta, billY, billM, billTax)
	}
	glAfter, err := h.gl.GLBalanceCents(h.ctx, "2200", billY, billM, branchTRT)
	if err != nil {
		t.Fatalf("GL 2200 read after the post: %v", err)
	}
	glDelta := glAfter - glBefore
	if glDelta != -billTax {
		t.Errorf("FW1-11 house zone document date: GL 2200 moved %d in the bill's month %04d-%02d, want %d, so the ITC line and the journal agree on the bill's month (variance delta %d)",
			glDelta, billY, billM, -billTax, glDelta+itcDelta)
	}

	// And nothing landed in the month before: the predecessor's 2200 movement and its input tax
	// credits are untouched by this bill, so the defect cannot hide in a late posting either.
	prevGL, err := h.gl.GLBalanceCents(context.Background(), "2200", billY, billM-1, branchTRT)
	if err != nil {
		t.Fatalf("the predecessor month's GL 2200 read: %v", err)
	}
	prevITC, err := repo.VendorBillITCCents(context.Background(), billY, billM-1, branchTRT)
	if err != nil {
		t.Fatalf("the predecessor month's ITC read: %v", err)
	}
	if prevGL != prevGLBefore || prevITC != prevITCBefore {
		t.Errorf("FW1-11 house zone document date: the bill dated %s moved the month BEFORE it (2200 %d, ITC %d), want it untouched (2200 %d, ITC %d)",
			billDay, prevGL-prevGLBefore, prevITC-prevITCBefore, prevGLBefore, prevITCBefore)
	}
}
