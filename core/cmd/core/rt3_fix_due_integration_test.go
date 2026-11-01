//go:build integration

package main

// rt3_fix_due_integration_test.go, the RT-3 fix round, item 2: a released holdback falls due on the release
// house day plus the invoice's own terms, not on the invoice's original due date. The aging, the finance
// charge and the statement read the released part by that date, through the ledger's RELEASE row.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
)

// rt3ReleaseAged releases the leg's job now, then dates the RELEASE row (its house day and its instant) to
// the house day `daysAgo` days back at noon, as if the Controller had pressed the button then.
func (l *rt2Leg) rt3ReleaseAged(invoiceID, key string, daysAgo int) {
	l.t.Helper()
	if _, err := l.rt3Service(time.Now()).Release(context.Background(), l.rt3Input(key)); err != nil {
		l.t.Fatalf("release: %v", err)
	}
	day := rt3Today().AddDate(0, 0, -daysAgo)
	mustExec(l.t, context.Background(), l.pool, `
		UPDATE holdback_ledger SET released_at = $2::date, created_at = $3
		 WHERE invoice_id = $1::uuid AND entry_type = 'RELEASE'`, invoiceID, day.Format("2006-01-02"), rt3InstantOn(day))
	l.drain()
}

// rt3InstantOn is an instant inside the house day `day` that has already passed: noon, or now when the leg runs
// before noon of that day. The readers count a RELEASE row from its created_at up to now(), so an instant later
// than now (noon of today, before noon) reads as not yet released.
func rt3InstantOn(day time.Time) time.Time {
	noon := day.Add(12 * time.Hour)
	if now := time.Now(); noon.After(now) {
		return now
	}
	return noon
}

// rt3PriorMonth is the house month before this one, and its last house day.
func rt3PriorMonth() (month string, lastDay time.Time) {
	n := rt3Today()
	first := time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, rt3Zone)
	last := first.AddDate(0, 0, -1)
	return last.Format("2006-01"), last
}

func (l *rt2Leg) rt3Charge(month string) (int64, bool) {
	l.t.Helper()
	res, err := l.writer(l.job, 100_000, 0).RunFinanceCharges(context.Background(), billing.FinanceChargeRequest{BranchID: l.branch, Month: month})
	if err != nil {
		l.t.Fatalf("the finance charge run: %v", err)
	}
	for _, c := range res.Charges {
		if c.CustomerID == l.cust {
			return c.PastDueCents, true
		}
	}
	return 0, false
}

func (l *rt2Leg) rt3AgingRow() billing.AgingRow {
	l.t.Helper()
	rows, err := billing.NewCreditService(billing.NewRepository(l.pool)).Aging(fw1StaffCtx(context.Background()), time.Now(), "")
	if err != nil {
		l.t.Fatalf("aging: %v", err)
	}
	for _, r := range rows {
		if r.CustomerID == l.cust {
			return r
		}
	}
	return billing.AgingRow{}
}

// rt3OldPaidInvoice is the reader's case: 1,000.00 with 100.00 held, written long ago, the net paid.
func (l *rt2Leg) rt3OldPaidInvoice() billing.InvoiceDoc {
	l.t.Helper()
	doc := l.invoice(100_000, 0)
	l.ageInvoice(doc.InvoiceID, 150)
	l.plantPayment(doc.InvoiceID, 90_000, time.Now().AddDate(0, 0, -60))
	l.rt3PerfOn(300)
	return doc
}

// The reader's case: on the release day the finance charge base is still 0.
func TestRT3Fix_TheFinanceChargeBaseOnReleaseDayStaysZero(t *testing.T) {
	l := rt2Setup(t, "fxa")
	l.newJob(true)
	doc := l.rt3OldPaidInvoice()
	month, lastDay := rt3PriorMonth()
	// Before the release the held part is never charged.
	if base, charged := l.rt3Charge(month); charged {
		t.Fatalf("before the release the customer is charged on %d", base)
	}
	// Released on the month's last day: due that day plus 30, so nothing is past due at that month end.
	l.rt3ReleaseAged(doc.InvoiceID, "fxa-key", int(rt3Today().Sub(lastDay).Hours()/24))
	if base, charged := l.rt3Charge(month); charged {
		t.Errorf("a holdback released on the month's last day is charged at that month end: past due %d", base)
	}
}

// It ages from the release day: released 45 days ago on 30 day terms it is 15 days past due, so it sits in
// the 1 to 30 day bucket and not the oldest one.
func TestRT3Fix_TheReleasedPartAgesFromTheReleaseDay(t *testing.T) {
	l := rt2Setup(t, "fxb")
	l.newJob(true)
	doc := l.rt3OldPaidInvoice()
	l.rt3ReleaseAged(doc.InvoiceID, "fxb-key", 45)
	a := l.rt3AgingRow()
	eq(t, "the customer owes the released part", a.TotalCents, int64(10_000))
	eq(t, "it is 15 days past its new due date: the 1 to 30 bucket", a.Days30Cents, int64(10_000))
	eq(t, "not the oldest bucket", a.Days90PlusCents, int64(0))
	// Released today it is current.
	l2 := rt2Setup(t, "fxc")
	l2.newJob(true)
	d2 := l2.rt3OldPaidInvoice()
	l2.rt3ReleaseAged(d2.InvoiceID, "fxc-key", 0)
	b := l2.rt3AgingRow()
	eq(t, "released today: current", b.CurrentCents, int64(10_000))
	eq(t, "released today: nothing past due", b.PastBucketCents, int64(0))
}

// Past its new due date by more than 30 days it is charged like any past due amount.
func TestRT3Fix_PastItsNewDueDateTheReleasedPartIsCharged(t *testing.T) {
	l := rt2Setup(t, "fxd")
	l.newJob(true)
	doc := l.rt3OldPaidInvoice()
	month, lastDay := rt3PriorMonth()
	// Released 70 days before the month end: due 40 days before it, so 40 days past due at that month end.
	l.rt3ReleaseAged(doc.InvoiceID, "fxd-key", int(rt3Today().Sub(lastDay).Hours()/24)+70)
	base, charged := l.rt3Charge(month)
	if !charged || base != 10_000 {
		t.Errorf("the released part, 40 days past its new due date, is charged like any past due amount: charged=%v base=%d", charged, base)
	}
}

// The statement lists the released part as released on its day, due 30 days after it.
func TestRT3Fix_TheStatementShowsTheReleasedPartOnItsDay(t *testing.T) {
	l := rt2Setup(t, "fxe")
	l.newJob(true)
	doc := l.rt3OldPaidInvoice()
	l.rt3ReleaseAged(doc.InvoiceID, "fxe-key", 10)
	lines, err := billing.NewRepository(l.pool).PreviewLines(context.Background(), l.cust, time.Now())
	if err != nil {
		t.Fatalf("statement lines: %v", err)
	}
	wantDue := rt3Today().AddDate(0, 0, -10+30).Format("2006-01-02")
	found := false
	for _, ln := range lines {
		if ln.BalanceCents == 10_000 {
			found = true
			if !strings.Contains(ln.InvoiceNo, "holdback released "+rt3Today().AddDate(0, 0, -10).Format("2006-01-02")) {
				t.Errorf("the line must say it was released on its day, got %q", ln.InvoiceNo)
			}
			if ln.DueDate.Format("2006-01-02") != wantDue {
				t.Errorf("the released part falls due %s, got %s", wantDue, ln.DueDate.Format("2006-01-02"))
			}
		}
	}
	if !found {
		t.Fatalf("no statement line for the released 100.00: %+v", lines)
	}
}

// Not yet 30 days past its new due date: released 55 days before the month end, due 25 days before it, so
// the released part is not charged (the original due date, long gone, would have charged it).
func TestRT3Fix_NotYetThirtyDaysPastTheNewDueDateIsNotCharged(t *testing.T) {
	l := rt2Setup(t, "fxf")
	l.newJob(true)
	doc := l.rt3OldPaidInvoice()
	month, lastDay := rt3PriorMonth()
	l.rt3ReleaseAged(doc.InvoiceID, "fxf-key", int(rt3Today().Sub(lastDay).Hours()/24)+55)
	if base, charged := l.rt3Charge(month); charged {
		t.Errorf("25 days past the new due date is not charged: past due %d", base)
	}
}

// rt3SetDates sets the linked lien job's three starting dates, each as days back from the house day (nil
// leaves it empty).
func (l *rt2Leg) rt3SetDates(pub, perf, supply *int) {
	l.t.Helper()
	day := func(n *int) any {
		if n == nil {
			return nil
		}
		return rt3Today().AddDate(0, 0, -*n).Format("2006-01-02")
	}
	mustExec(l.t, context.Background(), l.pool,
		`UPDATE lien_jobs SET csp_published_on = $2::date, substantial_perf_date = $3::date, last_supply_date = $4::date WHERE id = $1::uuid`,
		l.lien, day(pub), day(perf), day(supply))
}

func rt3N(n int) *int { return &n }

// Item 3: the preservation period runs from the LATEST of the dates the job carries that can start it
// (publication of the certificate, substantial performance, last supply), so a release is never early.
func TestRT3Fix_ThePeriodRunsFromTheLatestStartingDate(t *testing.T) {
	l := rt2Setup(t, "fxg")
	l.newJob(true)
	doc := l.invoice(100_000, 0)

	// Performed 100 days ago, last supply 90 days ago, but the certificate was published 30 days ago.
	l.rt3SetDates(rt3N(30), rt3N(100), rt3N(90))
	_, err := l.rt3Service(time.Now()).Release(context.Background(), l.rt3Input("fxg-1"))
	if rt3Refusal(err) != billing.RefusedNotDue {
		t.Fatalf("publication 30 days ago: want a not due refusal, got %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), rt3Today().AddDate(0, 0, -30).Format("2006-01-02")) || !strings.Contains(err.Error(), "publication") {
		t.Errorf("the refusal must name the publication date it counts from, got %v", err)
	}

	// A last supply later than the other two counts too.
	l.rt3SetDates(rt3N(100), rt3N(100), rt3N(20))
	if _, err := l.rt3Service(time.Now()).Release(context.Background(), l.rt3Input("fxg-2")); rt3Refusal(err) != billing.RefusedNotDue {
		t.Fatalf("last supply 20 days ago: want a not due refusal, got %v", err)
	}
	eq(t, "nothing moved by the refusals", l.holdbackOf(doc.InvoiceID), int64(10_000))
	eq(t, "no release row", l.rt3Releases(doc.InvoiceID), 0)

	// The latest date is 61 days back: its period ended yesterday, so the holdback is due today.
	l.rt3SetDates(rt3N(61), rt3N(100), rt3N(90))
	if _, err := l.rt3Service(rt3Today()).Release(context.Background(), l.rt3Input("fxg-3")); err != nil {
		t.Fatalf("every date is past its period: %v", err)
	}
	eq(t, "released", l.holdbackOf(doc.InvoiceID), int64(0))
}

// A job with no starting date at all is refused in words, and so is one the dates have not caught up with.
func TestRT3Fix_ANoStartingDateIsRefused(t *testing.T) {
	l := rt2Setup(t, "fxh")
	l.newJob(true)
	l.invoice(100_000, 0)
	l.rt3SetDates(nil, nil, nil)
	_, err := l.rt3Service(time.Now()).Release(context.Background(), l.rt3Input("fxh-1"))
	if rt3Refusal(err) != billing.RefusedNoDate {
		t.Fatalf("no starting date: want a no date refusal, got %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "publication") {
		t.Errorf("the words must name the dates it looks for, got %v", err)
	}
}
