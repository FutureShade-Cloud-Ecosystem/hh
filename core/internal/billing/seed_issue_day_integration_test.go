//go:build integration

package billing

// seed_issue_day_integration_test.go, the seed's own leg for the fixture rule (FROM-PRINCIPAL 261):
// the seed's opening balance invoices are dated from the DEALER's day, the day the seed instant
// reads in the house zone, not the session's CURRENT_DATE, which is the UTC day and runs one day
// ahead of the dealer's day in the evening window. The seed's opening balances carry no finalize,
// so the ledger's COALESCE(finalized_at, created_at) dates them by the seed instant; their issue
// and due days are that day less or more the offsets the seed's own comments name. The leg reads
// them by invoice_no from the shared seeded gate database and holds each day to the dealer
// arithmetic: green all day, biting under the evening guard's pin.

import (
	"context"
	"testing"
	"time"
)

func TestIntegration_TheSeedIssuesOnTheDealerDay(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()

	cases := []struct {
		no               string
		issueBack, ahead int
	}{
		{"INV-2026-0001", 30, 0},  // the PAID opening balance, due on the seed's own day
		{"INV-2026-0002", 10, 35}, // the FINALIZED opening balance
	}
	for _, w := range cases {
		var issue, due, seedDay string
		if err := pool.QueryRow(ctx,
			`SELECT issue_date::text, due_date::text, (created_at AT TIME ZONE 'America/Toronto')::date::text
			   FROM invoices WHERE invoice_no = $1`, w.no).Scan(&issue, &due, &seedDay); err != nil {
			t.Fatalf("read the seed's %s (is this database seeded?): %v", w.no, err)
		}
		day, err := time.Parse("2006-01-02", seedDay)
		if err != nil {
			t.Fatalf("parse the seed instant's dealer day %s: %v", seedDay, err)
		}
		wantIssue := day.AddDate(0, 0, -w.issueBack).Format("2006-01-02")
		wantDue := day.AddDate(0, 0, w.ahead).Format("2006-01-02")
		if issue != wantIssue || due != wantDue {
			t.Fatalf("the seed's %s reads issue %s due %s off the seed instant's dealer day %s, want issue %s due %s: the seed dates its invoices from the session's CURRENT_DATE, the UTC day, which runs one day ahead of the dealer's day in the evening window", w.no, issue, due, seedDay, wantIssue, wantDue)
		}
	}
}
