//go:build integration

package till_test

// fw113b_round2_bounce_ledger_integration_test.go, FW1-13b round 2: the bounce's subledger rows carry
// the bounce's own instant.
//
// Round 1 gave the bounce its own clock: the payment_nsf row and the journal the payment.bounced
// payload carries date from the service clock. The bounce's three customer_transactions rows (the
// NSF reversal row, the fee reversal rows and the NSF fee ADJUSTMENT) still took the column default,
// so the subledger and GL 1100 dated one bounce in different months, and an AR aging or a statement
// as of the bounce's month end could not tie with GL 1100.
//
// The leg here reads the real rows on a migrated and seeded gate database and cleans up every row it
// writes, so the shared gate database stays as it found it.

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fw113bRound2BouncePinnedDay is the instant this leg pins: 15 April of the books' FIRST seeded
// year, noon in the house zone. April is a month of that year no leg reads (the tie-out legs read
// June, the walk reads the month the run happens in), and a day that ended long before the run.
func fw113bRound2BouncePinnedDay(t *testing.T, pool *pgxpool.Pool) time.Time {
	t.Helper()
	var minYear int
	if err := pool.QueryRow(context.Background(), `SELECT COALESCE(MIN(year), 0) FROM fiscal_periods`).Scan(&minYear); err != nil {
		t.Skipf("no fiscal periods to read: %v", err)
	}
	if minYear == 0 {
		t.Skipf("no fiscal periods seeded")
	}
	return time.Date(minYear, time.April, 15, 12, 0, 0, 0, houseday.Location())
}

// TestFW113BTheBouncesSubledgerRowsCarryTheBounceDay is the red leg. Before the fix the bounce's
// subledger rows took the column default (the database's now()), so the fee row the customer owes
// sat in the database's month while the bounce row and its journal sat in the pinned one.
func TestFW113BTheBouncesSubledgerRowsCarryTheBounceDay(t *testing.T) {
	w := newMJWorld(t)
	ctx := context.Background()
	zone := houseday.Location()

	pinned := fw113bRound2BouncePinnedDay(t, w.pool)
	wantDay := pinned.In(zone).Format("2006-01-02")

	// A customer with a payment of its own: the bounce writes its subledger rows for the customer
	// the money was taken from.
	var customerID string
	if err := w.pool.QueryRow(ctx, `SELECT id::text FROM customers LIMIT 1`).Scan(&customerID); err != nil {
		t.Skipf("no seeded customer: %v", err)
	}
	var paymentID string
	if err := w.pool.QueryRow(ctx, `
		INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, branch_id, till_session_id)
		VALUES ('FW113B-R2-' || md5(random()::text), $1::uuid, 'CHEQUE', 30_00, 'RECEIVED', $2::uuid, $3::uuid)
		RETURNING id::text`, customerID, w.branchID, w.sessionID).Scan(&paymentID); err != nil {
		t.Fatalf("seed the bounced payment: %v", err)
	}
	// The cleanup is SCOPED to this leg's own rows: the shared gate database holds other packages'
	// movements, holds and journals, and a leg that deleted every credit hold or every journal would
	// take their markers with it.
	t.Cleanup(func() {
		c := context.Background()
		_, _ = w.pool.Exec(c, `DELETE FROM customer_transactions WHERE source_ref_id::text IN ($1, $2)`,
			paymentID, fw113bRound2NSFID(t, w.pool, paymentID))
		_, _ = w.pool.Exec(c, `DELETE FROM credit_holds WHERE source_payment_id = $1::uuid`, paymentID)
		_, _ = w.pool.Exec(c, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE idempotency_key = $1)`,
			"payment.bounced:"+paymentID)
		_, _ = w.pool.Exec(c, `DELETE FROM gl_journals WHERE idempotency_key = $1`, "payment.bounced:"+paymentID)
		_, _ = w.pool.Exec(c, `DELETE FROM events_outbox WHERE topic = 'payment.bounced' AND payload->>'payment_id' = $1`, paymentID)
		_, _ = w.pool.Exec(c, `DELETE FROM payment_nsf WHERE payment_id = $1::uuid`, paymentID)
		_, _ = w.pool.Exec(c, `DELETE FROM payment_allocations WHERE payment_id = $1::uuid`, paymentID)
		_, _ = w.pool.Exec(c, `DELETE FROM payments WHERE id = $1::uuid`, paymentID)
	})

	svc := till.NewService(till.NewRepository(w.pool)).WithClock(func() time.Time { return pinned })
	if _, err := svc.MarkNSF(staffCtx(w.userID, w.branchID), till.NSFInput{
		PaymentID: paymentID, Reason: "FW1-13b round 2 bounce ledger",
	}); err != nil {
		t.Fatalf("the bounce: %v", err)
	}
	nsfID := fw113bRound2NSFID(t, w.pool, paymentID)
	if nsfID == "" {
		t.Fatal("the bounce wrote no payment_nsf row")
	}

	// The fee row the customer now owes, and every other subledger row the bounce wrote, carry the
	// bounce's own instant.
	rows, err := w.pool.Query(ctx, `
		SELECT txn_type, source_type, to_char(occurred_at AT TIME ZONE $3, 'YYYY-MM-DD')
		  FROM customer_transactions
		 WHERE source_ref_id::text IN ($1, $2)
		 ORDER BY txn_type`, paymentID, nsfID, zone.String())
	if err != nil {
		t.Fatalf("read the bounce's subledger rows: %v", err)
	}
	defer rows.Close()
	seen := 0
	feeSeen := false
	for rows.Next() {
		var txnType, sourceType, day string
		if err := rows.Scan(&txnType, &sourceType, &day); err != nil {
			t.Fatalf("scan a subledger row: %v", err)
		}
		seen++
		if txnType == "ADJUSTMENT" && sourceType == "payment_nsf" {
			feeSeen = true
		}
		if day != wantDay {
			t.Fatalf("the bounce's %s subledger row is dated %s, want the bounce's own day %s", txnType, day, wantDay)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read the bounce's subledger rows: %v", err)
	}
	if seen == 0 {
		t.Fatal("the bounce wrote no subledger row for the customer, so this leg proved nothing")
	}
	if !feeSeen {
		t.Fatal("the bounce wrote no NSF fee row, so this leg proved nothing")
	}
}

// fw113bRound2NSFID reads the bounce row's id, "" when there is none.
func fw113bRound2NSFID(t *testing.T, pool *pgxpool.Pool, paymentID string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`SELECT COALESCE((SELECT id::text FROM payment_nsf WHERE payment_id = $1::uuid LIMIT 1), '')`,
		paymentID).Scan(&id); err != nil {
		t.Fatalf("read the bounce row: %v", err)
	}
	return id
}
