//go:build integration

// postbill_document_date_integration_test.go, FW1-11 round 2b (S2 P1-3). A vendor bill is dated by
// its bill_date, and the tax reconcile reads its input tax credit in the bill's month through
// VendorBillITCCents; the bill's journal must therefore land in the bill's month too, dated by the
// bill itself (the document date rule the GL already keeps), not by the posting clock. A bill dated
// last month and posted today used to debit 2200 on today's month while the documents side counted
// the credit last month, so both months showed a variance.
//
// The leg drives the real PostBill (harness, real gl.Service poster) and, in the same leg, confirms
// the sales side's grain: an invoice issued and finalized on the same house day has its finalize
// journal (the real ar.invoice.finalized consumer) in that day's month, the same month the
// documents read counts it in.
//
//	Run: DATABASE_URL='postgres://.../db?sslmode=disable' go test -tags=integration -count=1 \
//	     -run 'PostBillDocumentDate' ./internal/purchasing/

package purchasing

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

// fw11pdInvoiceBridge adapts billing.Repository.InvoiceForGL to gl.InvoiceReader so gl's real
// ar.invoice.finalized consumer can read the leg's planted invoice.
type fw11pdInvoiceBridge struct{ repo *billing.Repository }

func (b fw11pdInvoiceBridge) InvoiceForGL(ctx context.Context, invoiceID string) (gl.InvoiceAmounts, error) {
	a, err := b.repo.InvoiceForGL(ctx, invoiceID)
	if err != nil {
		return gl.InvoiceAmounts{}, err
	}
	return gl.InvoiceAmounts{
		SubtotalCents: a.SubtotalCents,
		TaxCents:      a.TaxCents,
		TotalCents:    a.TotalCents,
		HoldbackCents: a.HoldbackCents,
		BranchID:      a.BranchID,
		CustomerID:    a.CustomerID,
		InvoiceNo:     a.InvoiceNo,
		FinalizedAt:   a.FinalizedAt,
		SourceKind:    a.SourceKind,
	}, nil
}

// fw11pdSingleEvent settles exactly one event through the given consumer, the way the worker's
// drain settles an outbox row.
type fw11pdSingleEvent struct{ ev notify.Event }

func (o fw11pdSingleEvent) ProcessBatch(ctx context.Context, _ int, settle func(context.Context, notify.Event) notify.Settlement) (int, error) {
	s := settle(ctx, o.ev)
	if s.LastError != "" {
		return 0, fmt.Errorf("outbox settle %s: %s", o.ev.Topic, s.LastError)
	}
	return 1, nil
}

// fw11pdJournalPeriod reads the fiscal year and month a journal filed into.
func fw11pdJournalPeriod(t *testing.T, h *harness, journalID string) (int, int) {
	t.Helper()
	var y, m int
	if err := h.pool.QueryRow(h.ctx, `
		SELECT fp.year, fp.month FROM gl_journals j JOIN fiscal_periods fp ON fp.id = j.period_id
		 WHERE j.id = $1::uuid`, journalID).Scan(&y, &m); err != nil {
		t.Fatalf("read the journal's fiscal period: %v", err)
	}
	return y, m
}

// fw11pdEnsurePeriod opens the month's fiscal period when the seed does not hold it, so a leg run
// near a year edge still files where its dates say.
func fw11pdEnsurePeriod(t *testing.T, h *harness, y, m int) {
	t.Helper()
	if _, err := h.pool.Exec(h.ctx,
		`INSERT INTO fiscal_periods (year, month) VALUES ($1, $2) ON CONFLICT (year, month) DO NOTHING`, y, m); err != nil {
		t.Fatalf("ensure fiscal period %d-%02d: %v", y, m, err)
	}
}

func TestIntegration_PostBillDocumentDateAgreesWithTheITCLine(t *testing.T) {
	h := newHarness(t)
	repo := NewRepository(h.pool)

	// The bill is dated the 15th of last month and posted today, the ordinary vendor bill case.
	zone, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatalf("load the dealer zone: %v", err)
	}
	now := time.Now().In(zone)
	// Last month from the first of this month, never from today's day of month, which on the 31st after a
	// 30 day month normalizes into the current month.
	lastMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, zone).AddDate(0, -1, 0)
	billY, billM := lastMonth.Year(), int(lastMonth.Month())
	billDate := fmt.Sprintf("%04d-%02d-15", billY, billM)
	fw11pdEnsurePeriod(t, h, billY, billM)
	fw11pdEnsurePeriod(t, h, now.Year(), int(now.Month()))

	itcBefore, err := repo.VendorBillITCCents(h.ctx, billY, billM, branchTRT)
	if err != nil {
		t.Fatalf("baseline ITC read: %v", err)
	}
	glSvc := gl.NewService(gl.NewRepository(h.pool))
	glBefore, err := glSvc.GLBalanceCents(h.ctx, "2200", billY, billM, branchTRT)
	if err != nil {
		t.Fatalf("baseline GL 2200 read: %v", err)
	}

	product := h.newProduct(t, "docdate")
	po := h.orderedPO(t, product, 100, 2500)
	lineID := po.Lines[0].ID
	if _, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: lineID, QtyBase: 100}},
	}); err != nil {
		t.Fatalf("receive: %v", err)
	}
	bill, err := h.svc.CreateBill(h.ctx, CreateBillRequest{
		BillNo: billNo("docdate"), POID: po.ID, BillDate: billDate,
		TaxCents: 650,
		Lines:    []BillLineParams{{POLineID: lineID, QtyBilledBase: 100, UnitCostCents: 2500}},
	})
	if err != nil {
		t.Fatalf("create bill: %v", err)
	}
	posted, err := h.svc.PostBill(h.ctx, bill.ID, PostBillRequest{AcceptVariance: true, Note: "fw1-11 document date"})
	if err != nil {
		t.Fatalf("post bill: %v", err)
	}
	var journalID string
	if err := h.pool.QueryRow(h.ctx,
		`SELECT gl_journal_id::text FROM purchasing_bills WHERE id = $1::uuid`, posted.ID).Scan(&journalID); err != nil {
		t.Fatalf("read the bill's journal: %v", err)
	}
	if journalID == "" {
		t.Fatalf("PostBill left no journal on the bill")
	}

	// The documents side: the bill's input tax credit counts in the bill's month, by bill_date.
	itcAfter, err := repo.VendorBillITCCents(h.ctx, billY, billM, branchTRT)
	if err != nil {
		t.Fatalf("ITC read after the post: %v", err)
	}
	itcDelta := itcAfter - itcBefore
	if itcDelta != 650 {
		t.Fatalf("FW1-11 P1-3: the ITC line moved %d for %04d-%02d, want the bill's 650", itcDelta, billY, billM)
	}

	// The ledger side: the bill's journal must file in the bill's month too, dated by the bill
	// (the document date rule), so both sides of the tie-out read the same month.
	jy, jm := fw11pdJournalPeriod(t, h, journalID)
	glAfter, err := glSvc.GLBalanceCents(h.ctx, "2200", billY, billM, branchTRT)
	if err != nil {
		t.Fatalf("GL 2200 read after the post: %v", err)
	}
	glDelta := glAfter - glBefore
	if jy != billY || jm != billM {
		t.Errorf("FW1-11 P1-3 red: the bill dated %s posted its journal into %04d-%02d, want the bill's month %04d-%02d (the journal must carry bill_date as its document date)",
			billDate, jy, jm, billY, billM)
	}
	if glDelta != -650 {
		t.Errorf("FW1-11 P1-3 red: GL 2200 moved %d in the bill's month %04d-%02d, want -650, so the ITC line and the journal agree on the bill's month (variance delta %d)",
			glDelta, billY, billM, glDelta+itcDelta)
	}

	// The sales side, the same grain question: an invoice issued and finalized on the same house
	// day must have its finalize journal in that day's month, the month the documents read counts
	// it in. The journal comes from the real ar.invoice.finalized consumer, not a hand post.
	var customerID string
	if err := h.pool.QueryRow(h.ctx, `SELECT id::text FROM customers ORDER BY id LIMIT 1`).Scan(&customerID); err != nil {
		t.Skipf("no seeded customer: %v", err)
	}
	invNo := fmt.Sprintf("FW11PD-%d", time.Now().UnixNano())
	// One instant anchors both sides of the confirmation: the invoice is issued and finalized on
	// the same house day by construction, so the leg asserts the JOURNAL keeps that day, never a
	// clock drift of its own.
	finalized := time.Now().In(zone)
	houseDay := finalized.Format("2006-01-02")
	var invID string
	if err := h.pool.QueryRow(h.ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', $4, $4,
		        100000, 13000, 113000, 113000, $5, 'INVOICE')
		RETURNING id::text`, invNo, customerID, branchTRT, houseDay, finalized).Scan(&invID); err != nil {
		t.Fatalf("plant the same day invoice: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = h.pool.Exec(bg, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE idempotency_key = $1)`, "ar.invoice.finalized:"+invID)
		_, _ = h.pool.Exec(bg, `DELETE FROM gl_journals WHERE idempotency_key = $1`, "ar.invoice.finalized:"+invID)
		_, _ = h.pool.Exec(bg, `DELETE FROM invoice_lines WHERE invoice_id = $1::uuid`, invID)
		_, _ = h.pool.Exec(bg, `DELETE FROM invoices WHERE id = $1::uuid`, invID)
	})
	if _, err := h.pool.Exec(h.ctx, `
		INSERT INTO invoice_lines (invoice_id, line_no, description, qty_entered, uom_entered, qty_base,
		                           unit_price_cents, price_uom, line_total_cents, tax_cents)
		VALUES ($1::uuid, 1, 'FW11 document date sale line', 1, 'EA', 1, 100000, 'EA', 100000, 13000)`, invID); err != nil {
		t.Fatalf("plant the same day invoice line: %v", err)
	}

	finalizeSvc := gl.NewService(gl.NewRepository(h.pool)).WithInvoiceReader(fw11pdInvoiceBridge{repo: billing.NewRepository(h.pool)})
	disp := notify.NewDispatcher(fw11pdSingleEvent{ev: notify.Event{
		EventID: "fw11pd-1", Topic: "ar.invoice.finalized",
		Payload: mustJSON(t, map[string]string{"invoice_id": invID}),
	}}, slog.New(slog.DiscardHandler))
	finalizeSvc.RegisterPostingHandlers(disp)
	if n := disp.DrainOnce(h.ctx); n != 1 {
		t.Fatalf("drain ar.invoice.finalized: settled %d rows, want 1", n)
	}
	var fjid string
	if err := h.pool.QueryRow(h.ctx,
		`SELECT id::text FROM gl_journals WHERE idempotency_key = $1`, "ar.invoice.finalized:"+invID).Scan(&fjid); err != nil {
		t.Fatalf("the finalize journal: %v", err)
	}
	var issueDay, postedDay string
	if err := h.pool.QueryRow(h.ctx, `
		SELECT to_char(i.issue_date, 'YYYY-MM-DD'), to_char(j.posted_at AT TIME ZONE 'America/Toronto', 'YYYY-MM-DD')
		  FROM invoices i JOIN gl_journals j ON j.idempotency_key = $1 WHERE i.id = $2::uuid`,
		"ar.invoice.finalized:"+invID, invID).Scan(&issueDay, &postedDay); err != nil {
		t.Fatalf("read the invoice and journal days: %v", err)
	}
	fy, fm := fw11pdJournalPeriod(t, h, fjid)
	if issueDay != postedDay {
		t.Errorf("FW1-11 P1-3 sales side: the invoice issued %s but its finalize journal posts %s, want the same house day", issueDay, postedDay)
	}
	if iy, im := finalized.Year(), int(finalized.Month()); fy != iy || fm != im {
		t.Errorf("FW1-11 P1-3 sales side: the finalize journal filed into %04d-%02d, want the issue month %04d-%02d (issue_date and finalized_at fall on the same house day)", fy, fm, iy, im)
	}
}
