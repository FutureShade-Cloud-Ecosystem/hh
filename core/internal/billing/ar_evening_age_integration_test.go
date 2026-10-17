//go:build integration

// ar_evening_age_integration_test.go, the SQL reads of the AR family in the house evening window
// (the unit twin is ar_evening_age_test.go). Pinned at houseday.JustAfterUTCDayChange, so each leg
// is red on a read that follows the UTC day at any wall clock hour.
//
// Run: DATABASE_URL=... go test -tags integration ./internal/billing/... -run EveningAge -count=1
package billing

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// An invoice due on the house day is CURRENT on the house day, whatever the UTC day says: the aging
// the credit desk and the month end statements read cuts its date parameter from the clock's house day.
func TestIntegrationEveningAge_AgingRowsReadTheClockOnTheHouseDay(t *testing.T) {
	rig := newHH195Rig(t)
	cust := rig.customer(t, "AG")
	rig.doc(t, cust, "INV-AG", "INVOICE", "FINALIZED", 5000, 0)

	rows, err := NewRepository(rig.pool).AgingRows(context.Background(), rig.clock, 60, "")
	if err != nil {
		t.Fatalf("aging rows: %v", err)
	}
	got, ok := findRow(rows, cust)
	if !ok {
		t.Fatalf("customer missing from the aging")
	}
	if got.CurrentCents != 5000 || got.Days30Cents != 0 {
		t.Errorf("invoice due on the house day: current %d, 1-30 %d; want 5000, 0 (it is not late yet)",
			got.CurrentCents, got.Days30Cents)
	}
}

// The register's overdue test reads the same date parameter.
func TestIntegrationEveningAge_RegisterCountsReadTheClockOnTheHouseDay(t *testing.T) {
	rig := newHH195Rig(t)
	cust := rig.customer(t, "RG")
	rig.doc(t, cust, "INV-RG", "INVOICE", "FINALIZED", 5000, 0)

	counts, err := NewRepository(rig.pool).RegisterCountsFor(context.Background(), rig.clock, 0, RegisterFilter{CustomerID: cust})
	if err != nil {
		t.Fatalf("register counts: %v", err)
	}
	if counts.Overdue != 0 {
		t.Errorf("overdue = %d, want 0: the invoice is due on the house day", counts.Overdue)
	}
}

// A statement cut for the house day carries a transaction rung that house evening, though its
// instant is already the next UTC day.
func TestIntegrationEveningAge_StatementLinesFileAnEveningUnderTheHouseDay(t *testing.T) {
	rig := newHH195Rig(t)
	cust := rig.customer(t, "ST")
	evening := rig.clock // the house evening of rig's day, already the next UTC day
	if _, err := rig.pool.Exec(context.Background(),
		`INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after,
		                                    source_type, source_ref_id, branch_id, occurred_at, created_at)
		 VALUES ($1::uuid,'INVOICE',700,700,'invoice',gen_random_uuid(),$2::uuid,$3,$3)`,
		cust, rig.branchID, evening); err != nil {
		t.Fatalf("seed transaction: %v", err)
	}
	t.Cleanup(func() {
		_, _ = rig.pool.Exec(context.Background(), `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, cust)
	})
	houseDay := houseClock(evening).Format("2006-01-02")
	asOf, err := parseAsOf(houseDay)
	if err != nil {
		t.Fatalf("as_of: %v", err)
	}
	lines, err := NewRepository(rig.pool).StatementLines(context.Background(), cust, asOf, AllBranches)
	if err != nil {
		t.Fatalf("statement lines: %v", err)
	}
	if len(lines) != 1 {
		t.Errorf("%d lines on the statement for %s, want the evening transaction", len(lines), houseDay)
	}
}

// The tie-out balance counts an invoice only from its issue day on the dealer's calendar: an invoice
// issue-dated the UTC day of the pinned instant is one day ahead of the house day at that hour, so
// the 1100 balance must not carry it yet. The non-invoice arm stays on the raw instant (a payment or
// NSF is an instant, not a day), so only the date arm moves.
func TestIntegrationEveningAge_TieOutBalanceCutsTheIssueDateOnTheHouseDay(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	n := ns(t)
	cust := seedCustomer(t, pool, n)

	at := houseday.JustAfterUTCDayChange(time.Now())
	utcDay := at.UTC().Format("2006-01-02")

	base, err := repo.ARBalanceCents(ctx, at, "")
	if err != nil {
		t.Fatalf("baseline AR: %v", err)
	}
	seedInvoice(t, pool, cust, n, invSpec{no: "AR-EVE", status: "FINALIZED", issue: utcDay, total: 8000, balance: 8000, finalized: true})
	got, err := repo.ARBalanceCents(ctx, at, "")
	if err != nil {
		t.Fatalf("AR after seeding: %v", err)
	}
	if delta := got - base; delta != 0 {
		t.Fatalf("an invoice issue-dated %s, one UTC day ahead of the dealer's day at %s, moved the tie-out balance by %d; the issue date arm must cut on the house day",
			utcDay, at.UTC().Format(time.RFC3339), delta)
	}
}

// An explicit day asked of the tie-out is the dealer's day, taken at its last instant (the cut the
// GL route hands both arms). An invoice issued on that house day is counted, and one issued the next
// house day is not, at any wall hour.
func TestIntegrationEveningAge_TieOutBalanceCountsAnInvoiceIssuedOnTheExplicitDay(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	n := ns(t)
	cust := seedCustomer(t, pool, n)

	const day, nextDay = "2031-03-10", "2031-03-11"
	asOf, err := houseday.EndOfDay(day)
	if err != nil {
		t.Fatal(err)
	}
	base, err := repo.ARBalanceCents(ctx, asOf, "")
	if err != nil {
		t.Fatalf("baseline AR: %v", err)
	}
	seedInvoice(t, pool, cust, n, invSpec{no: "AR-EXP", status: "FINALIZED", issue: day, total: 8000, balance: 8000, finalized: true})
	seedInvoice(t, pool, cust, n, invSpec{no: "AR-EXP-NEXT", status: "FINALIZED", issue: nextDay, total: 500, balance: 500, finalized: true})
	got, err := repo.ARBalanceCents(ctx, asOf, "")
	if err != nil {
		t.Fatalf("AR after seeding: %v", err)
	}
	if delta := got - base; delta != 8000 {
		t.Fatalf("as of %s the tie-out moved by %d for an invoice issued that day (8000) and one issued the day after (500 must not count)", day, delta)
	}
}
