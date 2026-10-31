//go:build integration

package main

// rt2_fix2_integration_test.go, the RT-2 fix round 2: the retention ledger is the one dated source of an
// invoice's holdback. A write off, a credit memo or (later) a release taken this month never rewrites a
// closed month, whatever reader asks (the aging, ARBalanceCents, the finance charge, the statement).

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
)

// lastMonthEnd is the last instant of last month in the dealer's zone.
func rt2LastMonthEnd() time.Time {
	zone, _ := time.LoadLocation("America/Toronto")
	n := time.Now().In(zone)
	return time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, zone).Add(-time.Second)
}

// dated is what every reader says about last month's end: ARBalanceCents, the aging's grand total.
type rt2Dated struct{ ar, aging int64 }

func (l *rt2Leg) datedAt(end time.Time) rt2Dated {
	l.t.Helper()
	ctx := context.Background()
	repo := billing.NewRepository(l.pool)
	ar, err := repo.ARBalanceCents(ctx, end, l.branch)
	if err != nil {
		l.t.Fatalf("ARBalanceCents: %v", err)
	}
	rows, err := billing.NewCreditService(repo).Aging(fw1StaffCtx(ctx), end, l.branch)
	if err != nil {
		l.t.Fatalf("aging: %v", err)
	}
	var aging int64
	for _, r := range rows {
		aging += r.TotalCents
	}
	return rt2Dated{ar: ar, aging: aging}
}

// columnIsLedger asserts the invoice's holdback_cents equals the sum of its ledger rows as of now.
func (l *rt2Leg) columnIsLedger(invoiceID, step string) {
	l.t.Helper()
	var ledger int64
	if err := l.pool.QueryRow(context.Background(), `
		SELECT COALESCE(SUM(CASE entry_type WHEN 'RETENTION' THEN amount_cents ELSE -amount_cents END), 0)
		  FROM holdback_ledger WHERE invoice_id = $1::uuid`, invoiceID).Scan(&ledger); err != nil {
		l.t.Fatalf("read the invoice's ledger: %v", err)
	}
	eq(t0(l), step+": the invoice's holdback column equals its ledger sum", l.holdbackOf(invoiceID), ledger)
}

func t0(l *rt2Leg) *testing.T { return l.t }

func (l *rt2Leg) writeOffNow(invoiceID, key string) billing.WriteOffResult {
	l.t.Helper()
	res, err := l.writeOffRepo().WriteOffInvoice(context.Background(), billing.WriteOffInput{
		InvoiceID: invoiceID, Reason: "rt2 fix 2", IdempotencyKey: key, OrgID: rt2FixOrg, IsController: true,
	}, time.Now())
	if err != nil {
		l.t.Fatalf("write off: %v", err)
	}
	l.drain()
	return res
}

// Probe A: a write off this month leaves last month's aging, AR figure, statement and tie alone.
func TestRT2Fix2_AWriteOffThisMonthLeavesLastMonthAlone(t *testing.T) {
	l := rt2Setup(t, "f2wo")
	lastEnd := rt2LastMonthEnd()
	l.newJob(true)
	doc := l.invoice(100_000, 13_000) // 113,000 with 10,000 held
	l.ageInvoice(doc.InvoiceID, 45)
	l.columnIsLedger(doc.InvoiceID, "written")
	before := l.datedAt(lastEnd)

	res := l.writeOffNow(doc.InvoiceID, "rt2fix2-wo")
	l.columnIsLedger(doc.InvoiceID, "after the write off")
	after := l.datedAt(lastEnd)
	eq(t, "ARBalanceCents at last month's end", after.ar-before.ar, int64(0))
	eq(t, "the aging at last month's end", after.aging-before.aging, int64(0))

	if _, err := l.writeOffRepo().ReverseWriteOff(context.Background(), billing.ReverseWriteOffInput{
		InvoiceID: doc.InvoiceID, WriteOffID: res.WriteOffID, Reason: "undo", OrgID: rt2FixOrg,
	}, time.Now()); err != nil {
		t.Fatalf("reverse: %v", err)
	}
	l.drain()
	l.columnIsLedger(doc.InvoiceID, "after the reversal")
	back := l.datedAt(lastEnd)
	eq(t, "ARBalanceCents at last month's end after the reversal", back.ar-before.ar, int64(0))
	eq(t, "the aging at last month's end after the reversal", back.aging-before.aging, int64(0))
}

// Probe C: the customer paid the net; the holdback written off today is not charged by last month's run.
func TestRT2Fix2_AWriteOffTodayIsNotChargedByLastMonthsFinanceCharge(t *testing.T) {
	l := rt2Setup(t, "f2fc")
	l.newJob(true)
	doc := l.invoice(100_000, 13_000)
	l.ageInvoice(doc.InvoiceID, 150)
	l.plantPayment(doc.InvoiceID, 103_000, time.Now().AddDate(0, 0, -60))
	l.writeOffNow(doc.InvoiceID, "rt2fix2-fc")
	l.columnIsLedger(doc.InvoiceID, "after the write off")
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
			t.Errorf("charged after the write off: past due %d, charge %d", c.PastDueCents, c.ChargeCents)
		}
	}
}

// The one function every dated reader calls, across a write off and its reversal straddling a month end.
func TestRT2Fix2_TheOneFunctionReadsTheLedgerAsOfADateAcrossAStraddle(t *testing.T) {
	l := rt2Setup(t, "f2fn")
	l.newJob(true)
	doc := l.invoice(100_000, 0) // 10,000 held
	l.ageInvoice(doc.InvoiceID, 30)
	res := l.writeOffNow(doc.InvoiceID, "rt2fix2-fn")
	if _, err := l.writeOffRepo().ReverseWriteOff(context.Background(), billing.ReverseWriteOffInput{
		InvoiceID: doc.InvoiceID, WriteOffID: res.WriteOffID, Reason: "undo", OrgID: rt2FixOrg,
	}, time.Now()); err != nil {
		t.Fatalf("reverse: %v", err)
	}
	// The write off row sits 10 days back, the reversal now.
	mustExec(t, context.Background(), l.pool, `
		UPDATE holdback_ledger SET created_at = now() - interval '10 days'
		 WHERE invoice_id = $1::uuid AND amount_cents < 0`, doc.InvoiceID)
	at := func(daysBack int) int64 {
		var h int64
		if err := l.pool.QueryRow(context.Background(),
			`SELECT invoice_holdback_as_of($1::uuid, now() - make_interval(days => $2::int))`, doc.InvoiceID, daysBack).Scan(&h); err != nil {
			t.Fatalf("invoice_holdback_as_of: %v", err)
		}
		return h
	}
	eq(t, "before the invoice", at(40), int64(0))
	eq(t, "written, not yet written off", at(20), int64(10_000))
	eq(t, "written off, not yet reversed", at(5), int64(0))
	eq(t, "reversed", at(-1), int64(10_000))
}
