//go:build integration

package till

// seeded_issue_day_integration_test.go, the fixture rule's own leg (FROM-PRINCIPAL 261): an invoice
// a till fixture seeds, seeded the way this package's helpers seed it, carries the DEALER's day as
// its issue day, the day its finalize instant reads in the dealer's calendar. A helper that names
// no issue day leaves it to the column default, and the default is the session's CURRENT_DATE, the
// UTC day: in the evening window that reads one day ahead of the dealer's day, the shape no
// production writer makes (every production INSERT binds zone.Today at finalize). The house's own
// SQL spelling of the dealer's day is (now() AT TIME ZONE 'America/Toronto')::date. The leg runs
// green all day and bites while the database's clock sits in the evening window, which the evening
// guard pins.

import (
	"context"
	"testing"
)

func TestIntegration_SeededInvoice_IssueDayIsTheFinalizeDayInTheDealerCalendar(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, _ := seedBranchUser(t, pool)

	assertSeededOnTheDealerDay := func(invoiceID, label string) {
		t.Helper()
		var issue, finalizeDay string
		if err := pool.QueryRow(ctx,
			`SELECT issue_date::text, (COALESCE(finalized_at, created_at) AT TIME ZONE 'America/Toronto')::date::text
			   FROM invoices WHERE id = $1::uuid`, invoiceID).Scan(&issue, &finalizeDay); err != nil {
			t.Fatalf("read the %s seeded invoice: %v", label, err)
		}
		if issue != finalizeDay {
			t.Fatalf("the %s seeded invoice is issue-dated %s, but its finalize instant reads %s in the dealer's calendar: the fixture must name the dealer's day, the session's CURRENT_DATE default is the UTC day and runs one day ahead in the evening window", label, issue, finalizeDay)
		}
	}

	// The attribution fixture's invoice: FINALIZED, balance carried, no named issue day before the
	// fix.
	_, inv := seedCustomerInvoice(t, pool, branchID, 8000)
	assertSeededOnTheDealerDay(inv, "history")

	// The NSF fixture's invoice (M3's P1): PAID by its cheque, no named issue day before the fix.
	_, invNSF, _, cleanupNSF := seedNSFCase(t, pool, branchID, 50000)
	t.Cleanup(cleanupNSF)
	assertSeededOnTheDealerDay(invNSF, "nsf")
}
