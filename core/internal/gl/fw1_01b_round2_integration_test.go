//go:build integration

// fw1_01b_round2_integration_test.go, FW1-01b round 2's red first legs (S3's review,
// FROM-PRINCIPAL 112): the deposit cancel is dated by the CANCEL, never by the deposit's
// finalization (P1), and the till session's house zone leg lands: a drawer opened at 23:30
// house time on the month's last day and counted at 00:30 books in the month it was OPENED
// in, not the month the count happened in.
//
// The two halves of the house zone leg live in their own packages on purpose: the GL's
// posting consumer is package-private (the till imports the GL, so the GL cannot import the
// till), and a journal posted from another package's test would land in a shared period and
// move balances other tests read. The till's half, which proves the drawer close stamps its
// own opening day on the event, is internal/till/fw1_01b_emitter_integration_test.go.
package gl

import (
	"context"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fw1b2Year is the fiscal year band these legs post in: outside the seeded 2026 periods and
// outside the bands the package's other integration tests use.
const fw1b2Year = 2089

// fw1b2Branch is one seeded location: the branch the journal is stamped with.
func fw1b2Branch(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM locations LIMIT 1`).Scan(&id); err != nil {
		t.Skipf("no seeded branch: %v", err)
	}
	return id
}

// fw1b2Journal reads a journal's filed period (year, month) and its posted_at.
func fw1b2Journal(t *testing.T, pool *pgxpool.Pool, key string) (year, month int, postedAt *time.Time) {
	t.Helper()
	var y int
	var m int
	var posted *time.Time
	if err := pool.QueryRow(context.Background(), `
		SELECT p.year, p.month, j.posted_at
		  FROM gl_journals j JOIN fiscal_periods p ON p.id = j.period_id
		 WHERE j.idempotency_key = $1`, key).Scan(&y, &m, &posted); err != nil {
		t.Fatalf("read the journal %q: %v", key, err)
	}
	return y, m, posted
}

// fw1b2DropJournal removes a journal this leg posted, so the books the leg touches are the
// books it found.
func fw1b2DropJournal(t *testing.T, pool *pgxpool.Pool, key string) {
	t.Helper()
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id=j.id AND j.idempotency_key=$1`, key)
	_, _ = pool.Exec(ctx, `DELETE FROM gl_journals WHERE idempotency_key=$1`, key)
}

// TestIntegration_FW1_01B_R2_DepositCancelPostsInTheCancelsMonth is P1's red: a
// deposit finalized in January and cancelled in March posts in January today, dated by the
// deposit's finalization, an earlier document than the cancel itself. After the fix the
// cancel's own instant rides the event and the journal posts in March.
func TestIntegration_FW1_01B_R2_DepositCancelPostsInTheCancelsMonth(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, fw1b2Year)

	// The deposit: taken and finalized in January, the month the money was taken.
	dep := fw1PlantDeposit(t, pool, time.Date(fw1b2Year, 1, 15, 12, 0, 0, 0, time.UTC))
	// The cancel: two months later, the month the liability actually left the books.
	cancelAt := time.Date(fw1b2Year, 3, 10, 9, 0, 0, 0, time.UTC)
	t.Cleanup(func() { fw1b2DropJournal(t, pool, "deposit.cancelled:"+dep) })

	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager}, cancelAt).
		WithInvoiceReader(seedInvoiceReader{pool: pool})

	if err := svc.consume(ctx, ev("deposit.cancelled", map[string]any{
		"invoice_id":   dep,
		"disposition":  "CREDIT_BACK",
		"paid_cents":   0,
		"closed_cents": 0,
		"occurred_at":  cancelAt,
	})); err != nil {
		t.Fatalf("the cancel consume: %v", err)
	}
	year, month, posted := fw1b2Journal(t, pool, "deposit.cancelled:"+dep)
	if year != fw1b2Year || month != 3 {
		t.Fatalf("the cancel journal posts in %d-%02d, want %d-03 (the cancel's own month, never the deposit's finalization month)",
			year, month, fw1b2Year)
	}
	if posted == nil || !posted.UTC().Equal(cancelAt) {
		t.Fatalf("the cancel journal carries posted_at %v, want the cancel's own instant %v", posted, cancelAt)
	}
}

// TestIntegration_FW1_01B_R2_TillSessionBooksInItsOpeningDay is the house zone leg: a
// drawer opened at 23:30 house time on the month's last day, counted at 00:30 the next
// morning, books its over/short in the month it was OPENED in. The month is the house
// zone's, not the instant's own and not the count's.
func TestIntegration_FW1_01B_R2_TillSessionBooksInItsOpeningDay(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, fw1b2Year)

	house := houseday.DefaultZone().Location()
	openedAt := time.Date(fw1b2Year, 1, 31, 23, 30, 0, 0, house) // the drawer's own evening
	countAt := time.Date(fw1b2Year, 2, 1, 0, 30, 0, 0, house)    // the count, after midnight
	// The journal's source_ref_id is a uuid column, so the session id is one.
	var sessionID string
	if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&sessionID); err != nil {
		t.Fatalf("mint the session id: %v", err)
	}
	t.Cleanup(func() { fw1b2DropJournal(t, pool, "till.session.completed:"+sessionID) })

	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager}, countAt)

	if err := svc.consume(ctx, ev("till.session.completed", map[string]any{
		"session_id":       sessionID,
		"branch_id":        fw1b2Branch(t, pool),
		"over_short_cents": -500,
		"occurred_at":      openedAt,
	})); err != nil {
		t.Fatalf("the till session consume: %v", err)
	}
	year, month, posted := fw1b2Journal(t, pool, "till.session.completed:"+sessionID)
	if year != fw1b2Year || month != 1 {
		t.Fatalf("the till session journal posts in %d-%02d, want %d-01 (the month the drawer was opened in, never the count's month)",
			year, month, fw1b2Year)
	}
	if posted == nil || !posted.UTC().Equal(openedAt) {
		t.Fatalf("the till session journal carries posted_at %v, want the drawer's opening %v", posted, openedAt)
	}
}
