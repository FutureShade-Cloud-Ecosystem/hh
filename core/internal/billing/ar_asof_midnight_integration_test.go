//go:build integration

package billing

// ar_asof_midnight_integration_test.go, W3 0.57 (FROM-PRINCIPAL 234, a bug): the 1100 tie figure at
// TODAY'S OWN MIDNIGHT is the books at that instant, never today's balances. The close preflight reads
// both sides at the first instant of the month after the one closing; on the 1st that instant is
// today's house midnight, and a day based current state shortcut made the AR side read every
// invoice, payment and NSF of the 1st so far while 1100 was read at midnight.

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// houseMidnight is the first instant of the dealer's current house day.
func houseMidnight(t *testing.T, repo *Repository) time.Time {
	t.Helper()
	zone, err := repo.zoneFor(context.Background())
	if err != nil {
		t.Fatalf("read the dealer's zone: %v", err)
	}
	n := houseClockIn(zone, time.Now())
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, zone.Location())
}

// midnightWorld plants the two acts that tell today's midnight from now: an invoice finalized
// yesterday and paid after midnight (owed 10000 at midnight, settled now) and one finalized after
// midnight (open 7000 now, not on the books at midnight). It answers the customer, the house
// midnight and the branch baselines read before the plant at midnight and at now.
func midnightWorld(t *testing.T, pool *pgxpool.Pool, repo *Repository) (cust string, midnight time.Time, base, baseNow int64) {
	t.Helper()
	ctx := context.Background()
	midnight = houseMidnight(t, repo)
	yesterday := midnight.Add(-12 * time.Hour).Format("2006-01-02")
	cust = seedCustomer(t, pool, ns(t))

	var err error
	base, err = repo.ARBalanceCents(ctx, midnight, trentonBranch)
	if err != nil {
		t.Fatalf("baseline at midnight: %v", err)
	}
	baseNow, err = repo.ARBalanceCents(ctx, time.Now().Add(time.Minute), trentonBranch)
	if err != nil {
		t.Fatalf("baseline at now: %v", err)
	}

	// Finalized yesterday, paid after midnight: still owed at midnight, 10000.
	paid := seedInvoice(t, pool, cust, ns(t), invSpec{no: "OWED-AT-MIDNIGHT", status: "PAID", issue: yesterday,
		subtotal: 10_000, total: 10_000, balance: 0, finalized: true})
	var payID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, branch_id)
		 VALUES (nextval('payment_no_seq')::text, $1::uuid, 'CHEQUE', 10000, 'RECEIVED', $2::uuid)
		 RETURNING id::text`, cust, trentonBranch).Scan(&payID); err != nil {
		t.Fatalf("seed the after midnight payment: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO payment_allocations (payment_id, invoice_id, amount_cents, created_at)
		 VALUES ($1::uuid, $2::uuid, 10000, now())`, payID, paid); err != nil {
		t.Fatalf("seed the after midnight allocation: %v", err)
	}
	// Registered after the invoices so it runs first (cleanups run last in, first out).
	t.Cleanup(func() {
		for _, q := range []struct{ table, sql string }{
			{"payment_allocations", `DELETE FROM payment_allocations WHERE payment_id = $1::uuid`},
			{"payments", `DELETE FROM payments WHERE id = $1::uuid`},
		} {
			if _, err := pool.Exec(context.Background(), q.sql, payID); err != nil {
				t.Errorf("remove the %s of the midnight leg: %v", q.table, err)
			}
		}
	})
	// Finalized after midnight: not on the books at midnight, 7000.
	late := seedInvoice(t, pool, cust, ns(t), invSpec{no: "FINALIZED-AFTER-MIDNIGHT", status: "FINALIZED", issue: yesterday,
		subtotal: 7_000, total: 7_000, balance: 7_000, finalized: true})
	if _, err := pool.Exec(ctx, `UPDATE invoices SET finalized_at = now() WHERE id = $1::uuid`, late); err != nil {
		t.Fatalf("stamp the late finalize: %v", err)
	}

	return cust, midnight, base, baseNow
}

func TestIntegration_ARBalanceAtTodaysMidnightIsTheBooksAtMidnight(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	repo := NewRepository(pool)
	_, midnight, base, baseNow := midnightWorld(t, pool, repo)

	gotMidnight, err := repo.ARBalanceCents(ctx, midnight, trentonBranch)
	if err != nil {
		t.Fatalf("read at midnight: %v", err)
	}
	if d := gotMidnight - base; d != 10_000 {
		t.Fatalf("the tie figure at today's midnight moved %d, want 10000 (the invoice paid after midnight was still owed at it, "+
			"the one finalized after it is not on the books at it)", d)
	}
	// And the current state is still the balance sheet's: only the 7000 is open now.
	gotNow, err := repo.ARBalanceCents(ctx, time.Now(), trentonBranch)
	if err != nil {
		t.Fatalf("read at now: %v", err)
	}
	if d := gotNow - baseNow; d != 7_000 {
		t.Fatalf("the tie figure at now moved %d, want 7000 (the invoice paid is settled, the late one is open)", d)
	}
}

// The aging is a calendar date surface: its as of is a date, and today's date reads the current
// state (the statement run, the worklist and the sweep all ask for today's date and mean now). It
// does not share the tie figure's fault because nothing reads it at the month's end instant: the
// close preflight asks only ARBalanceCents. This leg pins both readings side by side so a change
// to either shows here.
func TestIntegration_AgingAtTodaysDateReadsNowWhileTheTieAtMidnightRebuilds(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	repo := NewRepository(pool)
	cust, midnight, base, _ := midnightWorld(t, pool, repo)

	if got := agingTotalForCustomer(t, pool, midnight, cust); got != 7_000 {
		t.Fatalf("the aging at today's date reads %d, want 7000 (a calendar date: today's date is the current state)", got)
	}
	tie, err := repo.ARBalanceCents(ctx, midnight, trentonBranch)
	if err != nil {
		t.Fatalf("read the tie figure at midnight: %v", err)
	}
	if d := tie - base; d != 10_000 {
		t.Fatalf("the tie figure at midnight moved %d, want 10000", d)
	}
}
