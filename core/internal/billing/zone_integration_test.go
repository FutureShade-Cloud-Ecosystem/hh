//go:build integration

package billing

// The invoice writers' dates in the dealer's own zone (item 3b slice 3). The same instant is the 27th
// in Toronto and still the 26th in Los Angeles; each writer files its documents in its own dealer's
// calendar, and two writers in one process do not leak into each other.

import (
	"context"
	"testing"
	"time"
)

func TestIntegrationInvoiceWriter_DatesFollowTheDealersZone(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	at := zoneInstant
	for _, tc := range []struct {
		name       string
		src        zoneSource
		issue, due string
	}{
		{"Los Angeles", zoneOf(t, "America/Los_Angeles"), "2026-08-26", "2026-09-25"},
		{"Toronto", zoneOf(t, "America/Toronto"), "2026-08-27", "2026-09-26"},
	} {
		cust := seedCustomer(t, pool, ns(t)+tc.name) // a customer per case: one order key must not replay the other
		w := NewInvoiceWriter(pool).
			WithOrderSource(houseDaySource{customer: cust, branch: trentonBranch}).
			WithClock(func() time.Time { return at }).
			WithZone(tc.src)
		doc, err := w.InvoiceOrder(ctx, "zone-"+tc.name+"-"+ns(t))
		if err != nil {
			t.Fatalf("%s: raise the order invoice: %v", tc.name, err)
		}
		var issue, due string
		if err := pool.QueryRow(ctx,
			`SELECT issue_date::text, due_date::text FROM invoices WHERE id = $1::uuid`,
			doc.InvoiceID).Scan(&issue, &due); err != nil {
			t.Fatalf("%s: read the raised invoice: %v", tc.name, err)
		}
		if issue != tc.issue || due != tc.due {
			t.Fatalf("%s: issue/due = %s/%s, want %s/%s", tc.name, issue, due, tc.issue, tc.due)
		}
	}
}

func TestIntegrationCreditMemo_DatesFollowTheDealersZone(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	cust := seedCustomer(t, pool, ns(t))
	at := zoneInstant
	for _, tc := range []struct {
		name string
		src  zoneSource
		day  string
	}{
		{"Los Angeles", zoneOf(t, "America/Los_Angeles"), "2026-08-26"},
		{"Toronto", zoneOf(t, "America/Toronto"), "2026-08-27"},
	} {
		w := NewInvoiceWriter(pool).WithClock(func() time.Time { return at }).WithZone(tc.src)
		doc, err := w.IssueCreditMemo(ctx, CreditMemoInput{
			CustomerID: cust, BranchID: trentonBranch,
			AmountCents: 2_500, Memo: "zone leg", IdempotencyKey: "zone-cm-" + tc.name + ns(t),
		})
		if err != nil {
			t.Fatalf("%s: raise the credit memo: %v", tc.name, err)
		}
		var issue, due string
		if err := pool.QueryRow(ctx,
			`SELECT issue_date::text, due_date::text FROM invoices WHERE id = $1::uuid`,
			doc.InvoiceID).Scan(&issue, &due); err != nil {
			t.Fatalf("%s: read the raised memo: %v", tc.name, err)
		}
		if issue != tc.day || due != tc.day {
			t.Fatalf("%s: issue/due = %s/%s, want %s/%s", tc.name, issue, due, tc.day, tc.day)
		}
	}
}
