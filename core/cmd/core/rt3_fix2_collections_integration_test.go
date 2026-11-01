//go:build integration

package main

// rt3_fix2_collections_integration_test.go, the RT-3 fix round 2, item 2: the collections sweep ages the
// released part of a holdback from its release day, as the aging does, so a customer is not dunned on
// release day for money that became payable that day.

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/messagingprovider"
)

func (l *rt2Leg) sweepListing(invoiceID string) (messagingprovider.AgingInvoice, bool) {
	l.t.Helper()
	rows, err := messagingprovider.NewPgARAging(l.pool).OverdueInvoices(context.Background(), time.Now(), 1, 1000)
	if err != nil {
		l.t.Fatalf("the collections sweep read: %v", err)
	}
	for _, r := range rows {
		if r.InvoiceID == invoiceID {
			return r, true
		}
	}
	return messagingprovider.AgingInvoice{}, false
}

// The net is paid and 100.00 is held on an invoice due long ago: the sweep does not list it before the
// release, and on release day it still does not (the released part is due the release day plus the invoice's
// own terms). Once the released part is past its own due day, the sweep lists it by that day.
func TestRT3Fix2_TheCollectionsSweepDoesNotDunAReleasedHoldbackOnReleaseDay(t *testing.T) {
	l := rt2Setup(t, "f2d")
	l.newJob(true)
	doc := l.rt3OldPaidInvoice()
	if r, listed := l.sweepListing(doc.InvoiceID); listed {
		t.Fatalf("before the release the held part is never listed: %+v", r)
	}
	l.rt3ReleaseAged(doc.InvoiceID, "f2d-key", 0)
	if r, listed := l.sweepListing(doc.InvoiceID); listed {
		t.Errorf("on release day the released 100.00 is not yet due, yet the sweep lists %d cents %d days past due", r.BalanceCents, r.DaysPastDue)
	}

	// Released 45 days ago, due 30 days after: 15 days past its own due day.
	mustExecRelease := func(daysAgo int) {
		day := rt3Today().AddDate(0, 0, -daysAgo)
		mustExec(t, context.Background(), l.pool, `
			UPDATE holdback_ledger SET released_at = $2::date, created_at = $3
			 WHERE invoice_id = $1::uuid AND entry_type = 'RELEASE'`, doc.InvoiceID, day.Format("2006-01-02"), day.Add(12*time.Hour))
	}
	mustExecRelease(45)
	r, listed := l.sweepListing(doc.InvoiceID)
	if !listed {
		t.Fatalf("a released part 15 days past its own due day must be listed")
	}
	eq(t, "the listed balance is the released part", r.BalanceCents, int64(10_000))
	eq(t, "aged from the released part's due day", r.DaysPastDue, 15)
}
