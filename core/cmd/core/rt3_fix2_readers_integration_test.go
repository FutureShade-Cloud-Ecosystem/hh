//go:build integration

package main

// rt3_fix2_readers_integration_test.go, the RT-3 fix round 2, item 4 (P3-1): the AR landing, the register
// (chips and rows) and the customer workspace header split the released part of a holdback the same way
// the aging does. On release day the released money is not yet due, so none of them calls it overdue.

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
)

// releasedToday sets up the reader's case: a 1,000.00 invoice due long ago, the net paid, 100.00 held, and
// the holdback released now (the release day).
func (l *rt2Leg) releasedToday(key string) billing.InvoiceDoc {
	l.t.Helper()
	l.newJob(true)
	doc := l.rt3OldPaidInvoice()
	l.rt3ReleaseAged(doc.InvoiceID, key, 0)
	return doc
}

func TestRT3Fix2_TheRegisterChipsAndRowsDoNotCallAReleasedHoldbackOverdueOnReleaseDay(t *testing.T) {
	l := rt2Setup(t, "f2g")
	doc := l.releasedToday("f2g-key")
	repo := billing.NewRepository(l.pool)
	ctx := context.Background()
	f := billing.RegisterFilter{CustomerID: l.cust, Reach: billing.AllBranches, ShowCredits: true}

	counts, err := repo.RegisterCountsFor(ctx, time.Now(), 7, f)
	if err != nil {
		t.Fatalf("register counts: %v", err)
	}
	eq(t, "the overdue chip count", counts.Overdue, 0)
	f.Chip = billing.ChipPastDue
	rows, _, err := repo.RegisterRows(ctx, time.Now(), 7, f)
	if err != nil {
		t.Fatalf("register rows: %v", err)
	}
	eq(t, "the past due chip lists nothing", len(rows), 0)
	f.Chip = ""
	rows, _, err = repo.RegisterRows(ctx, time.Now(), 7, f)
	if err != nil || len(rows) != 1 || rows[0].ID != doc.InvoiceID {
		t.Fatalf("register row: %v %+v", err, rows)
	}
	if rows[0].Status == billing.StatusOverdue || rows[0].DaysOverdue != 0 {
		t.Errorf("on release day the row is not overdue, got %s with %d days", rows[0].Status, rows[0].DaysOverdue)
	}
}

func TestRT3Fix2_TheARLandingAgesAReleasedHoldbackFromItsReleaseDay(t *testing.T) {
	l := rt2Setup(t, "f2h")
	doc := l.releasedToday("f2h-key")
	invs, err := billing.NewARLandingRepository(l.pool).OpenInvoices(context.Background(), time.Now(), l.branch, billing.AllBranches)
	if err != nil {
		t.Fatalf("open invoices: %v", err)
	}
	found := false
	for _, iv := range invs {
		if iv.InvoiceID != doc.InvoiceID {
			continue
		}
		found = true
		want := rt3Today().AddDate(0, 0, 30).Format("2006-01-02")
		if got := iv.DueDate.Format("2006-01-02"); got != want {
			t.Errorf("the released 100.00 falls due its release day plus the invoice's terms: got %s, want %s", got, want)
		}
	}
	if !found {
		t.Fatalf("the invoice is open and must be listed")
	}
}

func TestRT3Fix2_TheWorkspaceHeaderDoesNotCountAReleasedHoldbackOverdueOnReleaseDay(t *testing.T) {
	l := rt2Setup(t, "f2i")
	l.releasedToday("f2i-key")
	h, err := billing.NewRepository(l.pool).WorkspaceHeader(context.Background(), l.cust)
	if err != nil {
		t.Fatalf("workspace header: %v", err)
	}
	eq(t, "open", h.OpenCents, int64(10_000))
	eq(t, "overdue", h.OverdueCents, int64(0))
}
