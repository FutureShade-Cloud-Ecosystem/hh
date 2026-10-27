//go:build integration

package billing

// ar_aging_as_of_integration_test.go, FW1-06b: an aging "as of" a past date must age what was owed
// ON that date.
//
// AR-08: the as_of parameter only moved the day the buckets were cut against. The balances were
// today's: an invoice paid after the as of date dropped out of the past aging (the payment moved
// balance_cents and flipped the status to PAID), and an invoice issued after the as of date counted.
// An aged trial balance as of month end, run after month end, could not tie to GL 1100 at that date.
//
// The window is the suite's back dated epoch (repository_integration_test.go): no other row in the
// database lives there, so the figures are assertable by absolute bucket and by delta.

import (
	"context"
	"testing"
)

func TestIntegrationARAgingAsOf_AgesWhatWasOwedOnTheDate(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	n := ns(t)
	cust := seedCustomer(t, pool, n)
	// This test's payments and allocations are its own; they go before the customer cleanup runs.
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM payment_allocations WHERE payment_id IN
			(SELECT id FROM payments WHERE customer_id = $1::uuid)`, cust)
		_, _ = pool.Exec(c, `DELETE FROM payments WHERE customer_id = $1::uuid`, cust)
	})

	// The tie out's baseline in the window, before any of this test's rows exist, so whatever else
	// lives there cancels in the delta.
	repo := NewRepository(pool)
	base, err := repo.ARBalanceCents(ctx, asOfEpoch, "")
	if err != nil {
		t.Fatalf("AR balance baseline: %v", err)
	}

	// The invoice the account owed on the as of day: issued and finalized inside the window, due
	// more than 90 days before it, wholly unpaid AT it. The receipt below arrives long after the as
	// of instant.
	var paidID string
	if err := pool.QueryRow(ctx, invoiceWriterSeqCTE+`
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, seq)
		VALUES ($1, $2::uuid, $3::uuid, 'PAID', $4, '1990-02-14',
		        9000, 0, 9000, 0, $4::date::timestamptz, (SELECT next_value FROM iseq))
		RETURNING id::text`,
		"#INV-ASOF-PAID-"+n, cust, trentonBranch, epochIssue).Scan(&paidID); err != nil {
		t.Fatalf("seed the invoice paid after the as of date: %v", err)
	}

	// An invoice issued AFTER the as of date must not be on the books at it at all.
	var lateID string
	if err := pool.QueryRow(ctx, invoiceWriterSeqCTE+`
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, seq)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', '1991-01-01', '1991-01-31',
		        7777, 0, 7777, 7777, now(), (SELECT next_value FROM iseq))
		RETURNING id::text`,
		"#INV-ASOF-LATE-"+n, cust, trentonBranch).Scan(&lateID); err != nil {
		t.Fatalf("seed the invoice issued after the as of date: %v", err)
	}

	// S6's P3: a document drafted inside the window (issue_date before the as of day) but finalized
	// AFTER the as of instant joins the books when its journal does, at finalized_at, never in the
	// earlier period's past aging or 1100 tie out.
	if _, err := pool.Exec(ctx, invoiceWriterSeqCTE+`
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, seq)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', $4, '1990-02-14',
		        5555, 0, 5555, 5555, now(), (SELECT next_value FROM iseq))`,
		"#INV-ASOF-FINALIZED-LATE-"+n, cust, trentonBranch, epochIssue); err != nil {
		t.Fatalf("seed the invoice finalized after the as of date: %v", err)
	}

	// The receipt that came after the as of date, as the settle left the row: a desk cheque received
	// now, allocated to the paid invoice now, the balance at zero and the status PAID.
	var payID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, branch_id)
		 VALUES (nextval('payment_no_seq')::text, $1::uuid, 'CHEQUE', 9000, 'RECEIVED', $2::uuid)
		 RETURNING id::text`, cust, trentonBranch).Scan(&payID); err != nil {
		t.Fatalf("seed the after payment: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO payment_allocations (payment_id, invoice_id, amount_cents, created_at)
		 VALUES ($1::uuid, $2::uuid, 9000, now())`, payID, paidID); err != nil {
		t.Fatalf("seed the after allocation: %v", err)
	}

	// The as of aging: the account carries exactly what it owed on that day, the 9000, in the bucket
	// the due date falls in AT that date. The late issued invoice is not on the books at it.
	rows, err := repo.AgingRows(ctx, asOfEpoch, 60, "")
	if err != nil {
		t.Fatalf("aging as of: %v", err)
	}
	var row *AgingRow
	for i := range rows {
		if rows[i].CustomerID == cust {
			row = &rows[i]
		}
	}
	if row == nil {
		t.Fatalf("an invoice wholly unpaid at the as of date must age at that date; the account is missing from the as of aging entirely")
	}
	if row.TotalCents != 9000 {
		t.Fatalf("the as of aging total = %d, want 9000 (the 7777 issued after the date is not on the books at it, "+
			"and the invoice paid after it was still owed at it)", row.TotalCents)
	}
	if row.Days90PlusCents != 9000 {
		t.Fatalf("the as of 90+ bucket = %d, want 9000: the due date was more than 90 days before the as of day", row.Days90PlusCents)
	}
	if row.CurrentCents != 0 {
		t.Fatalf("the as of current bucket = %d, want 0: the whole balance was already past due at that date", row.CurrentCents)
	}

	// The tie out's figure at the same date must carry the same invoice, or the aged trial balance
	// cannot tie to GL 1100 at that date.
	after, err := repo.ARBalanceCents(ctx, asOfEpoch, "")
	if err != nil {
		t.Fatalf("AR balance as of: %v", err)
	}
	if delta := after - base; delta != 9000 {
		t.Fatalf("ARBalanceCents as of delta = %d, want 9000: the invoice paid after the date was still owed at it, "+
			"and the one issued after it is not counted at all", delta)
	}
}
