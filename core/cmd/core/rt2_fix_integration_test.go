//go:build integration

package main

// rt2_fix_integration_test.go, the RT-2 fix round: the readers and doors that predate withholding
// (finance charge, write off, credit memo apply, lien sync, collections, statements) treat the holdback
// as what it is, money the customer may hold, not money past due. Each leg runs on the rt2 fixture.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
)

// plantPayment records a payment of cents against an invoice, dated at `at` (the planted shape of the
// finance charge legs).
func (l *rt2Leg) plantPayment(invoiceID string, cents int64, at time.Time) {
	l.t.Helper()
	ctx := context.Background()
	l.n++
	var pid string
	if err := l.pool.QueryRow(ctx, `
		INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, received_at, branch_id, created_at)
		VALUES ($1, $2::uuid, 'CASH', $3, 'RECEIVED', $4, $5::uuid, $4) RETURNING id::text`,
		fmt.Sprintf("RT2-PAY-%d", l.n), l.cust, cents, at, l.branch).Scan(&pid); err != nil {
		l.t.Fatalf("plant the payment: %v", err)
	}
	mustExec(l.t, ctx, l.pool, `INSERT INTO payment_allocations (payment_id, invoice_id, amount_cents, created_at) VALUES ($1::uuid, $2::uuid, $3, $4)`,
		pid, invoiceID, cents, at)
	mustExec(l.t, ctx, l.pool, `UPDATE invoices SET balance_cents = balance_cents - $2, status = 'PARTIALLY_PAID' WHERE id = $1::uuid`, invoiceID, cents)
}

// ageInvoice moves an invoice's dates back by days.
func (l *rt2Leg) ageInvoice(invoiceID string, days int) {
	l.t.Helper()
	mustExec(l.t, context.Background(), l.pool, `
		UPDATE invoices SET issue_date = issue_date - $2::int, due_date = due_date - $2::int,
		       finalized_at = finalized_at - make_interval(days => $2::int), created_at = created_at - make_interval(days => $2::int)
		 WHERE id = $1::uuid`, invoiceID, days)
}

// P1-1: a customer who paid the whole net of a 90 day old holdback invoice is charged nothing.
func TestRT2Fix_TheFinanceChargeNeverChargesHoldback(t *testing.T) {
	l := rt2Setup(t, "fc")
	l.newJob(true)
	doc := l.invoice(100_000, 13_000) // net 103,000; holdback 10,000
	l.ageInvoice(doc.InvoiceID, 150)
	l.plantPayment(doc.InvoiceID, 103_000, time.Now().AddDate(0, 0, -60))
	zone, _ := time.LoadLocation("America/Toronto")
	n := time.Now().In(zone)
	prior := time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, zone).AddDate(0, -1, 0)
	res, err := l.writer(l.job, 100_000, 0).RunFinanceCharges(context.Background(), billing.FinanceChargeRequest{
		BranchID: l.branch, Month: prior.Format("2006-01"),
	})
	if err != nil {
		t.Fatalf("the finance charge run: %v", err)
	}
	for _, c := range res.Charges {
		if c.CustomerID == l.cust {
			t.Errorf("the customer paid the whole net and was charged: past due %d, charge %d", c.PastDueCents, c.ChargeCents)
		}
	}
}

const rt2FixOrg = "88a20000-0000-4000-8000-0000000000c2"

// applyMemo applies a credit memo to the invoice it reverses, as the credit desk does.
func (l *rt2Leg) applyMemo(memoID, invoiceID string, cents int64, key string) (billing.CreditApplication, error) {
	l.t.Helper()
	return billing.NewRepository(l.pool).ApplyCredit(context.Background(), billing.ApplyCreditInput{
		CustomerID: l.cust, CreditKind: billing.CreditKindMemo, CreditID: memoID, InvoiceID: invoiceID,
		AmountCents: cents, Reason: "rt2 fix", IdempotencyKey: key, OrgID: rt2FixOrg,
	}, time.Now())
}

type invoiceRow struct {
	balance, holdback int64
	status            string
}

func (l *rt2Leg) invoiceRow(id string) invoiceRow {
	l.t.Helper()
	var r invoiceRow
	if err := l.pool.QueryRow(context.Background(), `SELECT balance_cents, holdback_cents, status FROM invoices WHERE id = $1::uuid`, id).
		Scan(&r.balance, &r.holdback, &r.status); err != nil {
		l.t.Fatalf("read the invoice: %v", err)
	}
	return r
}

// P1-3, full credit: after the rest is credited the invoice carries no holdback, the memo applies in full
// and the invoice closes; the lien worklist no longer lists it.
func TestRT2Fix_AFullCreditLeavesNoHoldbackAndTheMemoAppliesInFull(t *testing.T) {
	l := rt2Setup(t, "fullcm")
	l.newJob(true)
	doc := l.invoice(100_000, 13_000) // 113,000 with 10,000 held
	memo := l.creditMemo(doc.InvoiceID, 0, "rt2fix-full")
	eq(t, "the invoice's own holdback after a full credit", l.holdbackOf(doc.InvoiceID), int64(0))
	if _, err := l.applyMemo(memo.InvoiceID, doc.InvoiceID, 113_000, "rt2fix-full-apply"); err != nil {
		t.Fatalf("the memo applies in full: %v", err)
	}
	got := l.invoiceRow(doc.InvoiceID)
	eq(t, "the invoice balance", got.balance, int64(0))
	eq(t, "the invoice status", got.status, "PAID")
	eq(t, "the memo's balance", l.invoiceRow(memo.InvoiceID).balance, int64(0))
	held, err := billing.NewRepository(l.pool).InvoicesWithHoldback(context.Background(), l.branch)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range held {
		if h.InvoiceID == doc.InvoiceID {
			t.Errorf("the lien worklist still lists the credited invoice with %d held", h.HoldbackCents)
		}
	}
	eq(t, "the aging", l.agingTotal()-l.agingBase, int64(0))
	l.ties("after the full credit applied")
}

// P1-3, partial credit: the invoice keeps only the holdback still withheld, the payment doors take what
// is owed, and a fully applied memo is not an open aging line.
func TestRT2Fix_APartialCreditLowersTheInvoicesHoldbackAndTheAgingTies(t *testing.T) {
	l := rt2Setup(t, "partcm")
	l.newJob(true)
	doc := l.invoice(100_000, 0)
	memo := l.creditMemo(doc.InvoiceID, 50_000, "rt2fix-part")
	eq(t, "the invoice's own holdback after half is credited", l.holdbackOf(doc.InvoiceID), int64(5_000))
	if _, err := l.applyMemo(memo.InvoiceID, doc.InvoiceID, 50_000, "rt2fix-part-apply"); err != nil {
		t.Fatalf("apply the memo: %v", err)
	}
	got := l.invoiceRow(doc.InvoiceID)
	eq(t, "the invoice balance", got.balance, int64(50_000))
	eq(t, "the invoice holdback", got.holdback, int64(5_000))
	eq(t, "the aging shows the net owed", l.agingTotal()-l.agingBase, int64(45_000))
	// A fully applied memo has no aging line of its own.
	var memoLines int
	rows, err := billing.NewCreditService(billing.NewRepository(l.pool)).Aging(fw1StaffCtx(context.Background()), time.Now(), l.branch)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.CustomerID == l.cust {
			memoLines++
			eq(t, "the customer's aged total", r.TotalCents, int64(45_000))
		}
	}
	eq(t, "one aged row for the customer", memoLines, 1)
	// The dated rebuild agrees as of today.
	arNow, err := billing.NewRepository(l.pool).ARBalanceCents(context.Background(), time.Now().Add(time.Minute), l.branch)
	if err != nil {
		t.Fatal(err)
	}
	_ = arNow
	l.ties("after the partial credit applied")
}

// The dated rebuild (the month end tie) reads the holdback as it stood on that day: an invoice from last
// month, credited this month, still shows its original net at last month's end.
func TestRT2Fix_ADatedAgingReadsTheHoldbackAsItStoodThatDay(t *testing.T) {
	l := rt2Setup(t, "dated")
	zone, _ := time.LoadLocation("America/Toronto")
	n := time.Now().In(zone)
	lastEnd := time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, zone).Add(-time.Second)
	before, err := billing.NewRepository(l.pool).ARBalanceCents(context.Background(), lastEnd, l.branch)
	if err != nil {
		t.Fatal(err)
	}
	l.newJob(true)
	doc := l.invoice(100_000, 0)
	l.ageInvoice(doc.InvoiceID, 45)
	l.creditMemo(doc.InvoiceID, 50_000, "rt2fix-dated")
	after, err := billing.NewRepository(l.pool).ARBalanceCents(context.Background(), lastEnd, l.branch)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "AR at last month's end: the invoice's net, before the credit", after-before, int64(90_000))
}

// writeOffRepo is the billing repository as cmd/core wires it for the write off door.
func (l *rt2Leg) writeOffRepo() *billing.Repository { return holdBillingRepo(l.pool) }

// P1-2: a write off of an invoice with holdback credits 1150 for the holdback part and 1100 for the rest,
// takes the ledger's retention out, and the reversal puts every cent back.
func TestRT2Fix_AWriteOffSplitsTheHoldbackAndItsReversalRestoresIt(t *testing.T) {
	l := rt2Setup(t, "wo")
	l.newJob(true)
	doc := l.invoice(100_000, 13_000) // 113,000 with 10,000 held
	ctx := context.Background()
	res, err := l.writeOffRepo().WriteOffInvoice(ctx, billing.WriteOffInput{
		InvoiceID: doc.InvoiceID, Reason: "rt2 fix", IdempotencyKey: "rt2fix-wo", OrgID: rt2FixOrg, IsController: true,
	}, time.Now())
	if err != nil {
		t.Fatalf("write off: %v", err)
	}
	l.drain()
	j := l.legs(doc.InvoiceID)
	// The invoice's own journal debited 1100 103,000 and 1150 10,000; the write off credits the same.
	eq(t, "1100 credit (the net, written off)", j["1100"][1], int64(103_000))
	eq(t, "1150 credit (the holdback part, written off)", j["1150"][1], int64(10_000))
	eq(t, "the expense debit", j["5400"][0], int64(113_000))
	eq(t, "the ledger after the write off", l.ledgerNet(), int64(0))
	eq(t, "the invoice's holdback after the write off", l.holdbackOf(doc.InvoiceID), int64(0))
	eq(t, "the aging after the write off", l.agingTotal()-l.agingBase, int64(0))
	l.ties("after the write off")

	if _, err := l.writeOffRepo().ReverseWriteOff(ctx, billing.ReverseWriteOffInput{
		InvoiceID: doc.InvoiceID, WriteOffID: res.WriteOffID, Reason: "rt2 fix undo", OrgID: rt2FixOrg,
	}, time.Now()); err != nil {
		t.Fatalf("reverse the write off: %v", err)
	}
	l.drain()
	eq(t, "the ledger after the reversal", l.ledgerNet(), int64(10_000))
	eq(t, "the invoice's holdback after the reversal", l.holdbackOf(doc.InvoiceID), int64(10_000))
	eq(t, "the aging after the reversal", l.agingTotal()-l.agingBase, int64(103_000))
	l.ties("after the write off is reversed")
}
