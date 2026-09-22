//go:build integration

// DIB-463 integration tests. The tile's whole promise is "same underlying data as the #payments
// channel", and that promise can only be broken in SQL, so it is only worth proving in SQL:
//
//	the tile's numbers come from the rollup ROOT rows, not from the payments ledger;
//	a payment the rollup never saw does not appear on the card;
//	the branch predicate really scopes, and the GLOBAL channel really is included;
//	yesterday's day line is not readable as today's total.
//
// Run: cd backend && DATABASE_URL=... go test -tags integration ./internal/messaging/... -v
package messaging

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The 0152 Kingston payments channel (its Trenton/global siblings are declared in the DIB-449 file).
const chPaymentsKingston = "a5000000-0000-4000-9000-000000000450"

// tileTotals is the tile read for one caller, on the fixture's own day.
func tileTotals(t *testing.T, pool *pgxpool.Pool, branchIDs []string, all bool) PaymentsToday {
	t.Helper()
	got, err := NewService(NewRepository(pool)).PaymentsTodayFor(context.Background(), branchIDs, all, testDay)
	if err != nil {
		t.Fatalf("payments today: %v", err)
	}
	return got
}

// - THE ONE-SOURCE PROPERTY, IN SQL -

// MUTATION TARGET: point RollupTotalsForDay at the `payments` table (the obvious one-line
// implementation) and this test dies.
//
// The fixture deliberately creates a payment row that NEVER reached the rollup, which is not a
// contrived state: it is a receipt that arrived while the outbox was behind, one filed under a
// different day boundary, or one that was later voided. A recomputing tile would count it and the
// card would then disagree with the channel line, which is how a person stops believing both numbers.
func TestIntegration_PaymentsToday_IgnoresPaymentsTheRollupNeverSaw(t *testing.T) {
	pool := intPool(t)
	rollupCleanup(t, pool, uAlexMorgan, chPaymentsTrenton, chPaymentsGlobal)
	svc := NewService(NewRepository(pool))

	// Two payments the rollup DID see.
	ringPayments(t, svc, "payments-trenton", map[string]int64{"itest-tile-1": 124000, "itest-tile-2": 388050})
	// And one it did not, a real ledger row, invisible to the day line.
	seedPayment(t, pool, branchTrenton, 999999, true, false)

	got := tileTotals(t, pool, nil, true)
	if got.Count != 2 || got.Cents != 512050 {
		t.Fatalf("the tile must report the ROLLUP (2 · $5,120.50), got %d · %d, it is recomputing", got.Count, got.Cents)
	}

	// And it must equal the day line character for character, which is the actual requirement.
	_, line, _, _, _ := rollupRow(t, pool, chPaymentsTrenton)
	if want := renderRollupBody(got.Count, got.Cents, got.Day); want != line {
		t.Fatalf("card and channel must render the same day:\n  card → %q\n  line → %q", want, line)
	}
}

// A day with no rollup measures ZERO, not "unavailable". The card says $0.00 because that is the true
// answer at 9am, and an em-dash would claim the module is unreachable.
func TestIntegration_PaymentsToday_AnEmptyDayIsAMeasuredZero(t *testing.T) {
	pool := intPool(t)
	rollupCleanup(t, pool, uAlexMorgan, chPaymentsTrenton, chPaymentsGlobal)

	got := tileTotals(t, pool, nil, true)
	if got.Count != 0 || got.Cents != 0 || got.Channels != 0 {
		t.Fatalf("a day with no rollup is a measured zero, got %+v", got)
	}
}

// Two branches, two day lines, one total for a GLOBAL caller, and only their own for a scoped one.
func TestIntegration_PaymentsToday_SumsTheBranchesTheCallerCanSee(t *testing.T) {
	pool := intPool(t)
	rollupCleanup(t, pool, uAlexMorgan, chPaymentsTrenton, chPaymentsKingston, chPaymentsGlobal)
	svc := NewService(NewRepository(pool))

	ringPayments(t, svc, "payments-trenton", map[string]int64{"itest-scope-t1": 100000})
	ringPayments(t, svc, "payments-kingston", map[string]int64{"itest-scope-k1": 250000})

	global := tileTotals(t, pool, nil, true)
	if global.Count != 2 || global.Cents != 350000 || global.Channels != 2 {
		t.Fatalf("a GLOBAL caller sees both branches, got %+v", global)
	}

	trenton := tileTotals(t, pool, []string{branchTrenton}, false)
	if trenton.Count != 1 || trenton.Cents != 100000 {
		t.Fatalf("a Trenton-only caller sees Trenton's day, got %+v", trenton)
	}

	kingston := tileTotals(t, pool, []string{branchKingston}, false)
	if kingston.Count != 1 || kingston.Cents != 250000 {
		t.Fatalf("a Kingston-only caller sees Kingston's day, got %+v", kingston)
	}
}

// The GLOBAL #payments channel (a branchless payment's home, 0152) counts for a branch-scoped caller.
// Excluding it would silently drop money out of the only total that claims to be the day's.
func TestIntegration_PaymentsToday_IncludesTheGlobalChannelForAScopedCaller(t *testing.T) {
	pool := intPool(t)
	rollupCleanup(t, pool, uAlexMorgan, chPaymentsTrenton, chPaymentsGlobal)
	svc := NewService(NewRepository(pool))

	ringPayments(t, svc, "payments", map[string]int64{"itest-global-1": 75000})

	scoped := tileTotals(t, pool, []string{branchTrenton}, false)
	if scoped.Count != 1 || scoped.Cents != 75000 {
		t.Fatalf("a branchless payment must reach a scoped caller's card, got %+v", scoped)
	}
}

// Yesterday's line is not today's number: the day's identity is the rollup's own event key.
func TestIntegration_PaymentsToday_ADifferentDayIsADifferentNumber(t *testing.T) {
	pool := intPool(t)
	rollupCleanup(t, pool, uAlexMorgan, chPaymentsTrenton, chPaymentsGlobal)
	svc := NewService(NewRepository(pool))
	ringPayments(t, svc, "payments-trenton", map[string]int64{"itest-day-1": 100000})

	tomorrow := testDay.AddDate(0, 0, 1)
	got, err := svc.PaymentsTodayFor(context.Background(), nil, true, tomorrow)
	if err != nil {
		t.Fatalf("payments today: %v", err)
	}
	if got.Count != 0 || got.Cents != 0 {
		t.Fatalf("the next day must not inherit this day's total, got %+v", got)
	}
	if got.Day == rollupDay(testDay) {
		t.Fatalf("the day must advance with the clock, got %q", got.Day)
	}
}

// A day line that was DELETED (moderation, retention) stops counting, the tile is a read of the
// channel, so what is not in the channel is not on the card.
func TestIntegration_PaymentsToday_ADeletedDayLineStopsCounting(t *testing.T) {
	pool := intPool(t)
	rollupCleanup(t, pool, uAlexMorgan, chPaymentsTrenton, chPaymentsGlobal)
	svc := NewService(NewRepository(pool))
	ringPayments(t, svc, "payments-trenton", map[string]int64{"itest-del-1": 100000})

	if got := tileTotals(t, pool, nil, true); got.Count != 1 {
		t.Fatalf("precondition: the day line should count, got %+v", got)
	}
	if _, err := pool.Exec(context.Background(),
		`UPDATE msg_messages SET deleted_at = now() WHERE channel_id = $1::uuid AND event_key = $2`,
		chPaymentsTrenton, rollupKey(ClassRoutine, rollupDay(testDay))); err != nil {
		t.Fatalf("soft-delete the day line: %v", err)
	}
	if got := tileTotals(t, pool, nil, true); got.Count != 0 {
		t.Fatalf("a removed day line must not keep asserting a total, got %+v", got)
	}
}
