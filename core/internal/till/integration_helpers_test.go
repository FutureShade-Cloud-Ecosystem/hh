//go:build integration

// integration_helpers_test.go carries the pool-only helpers the surviving
// integration-tagged till tests share (the A4 suite-delta precedent for a
// left-behind file: till/integration_test.go stays behind on its payment
// import, the RUN-PAY card rail, and these helpers come over without it).
// intPool and realSvc are the pin's, with the one recorded difference the
// left-behind charger forces: realSvc constructs without a CardCharger,
// which is legal and fail-closed (a card tender answers not-configured
// rather than charging through a rail that is not wired in this tree);
// the tests that survive here exercise the NSF and history paths, never
// the card path.
package till

import (
	"context"
	"os"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

func intPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	fail := func(format string, a ...any) {
		if set {
			t.Fatalf(format, a...)
		}
		t.Skipf(format, a...)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		fail("integration DB unavailable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		fail("integration DB not reachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func realSvc(pool *pgxpool.Pool) *Service {
	return NewService(NewRepository(pool)).
		withCaller(testCallerFrom)
}

// seedBranchUser returns a seeded BRANCH location id + an ACTIVE user id
// (skips if the DB is unseeded), the pin's helper carried verbatim.
func seedBranchUser(t *testing.T, pool *pgxpool.Pool) (branchID, userID string) {
	t.Helper()
	ctx := context.Background()
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type = 'BRANCH' LIMIT 1`).Scan(&branchID); err != nil {
		t.Skipf("no seeded BRANCH location: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Skipf("no seeded user: %v", err)
	}
	return branchID, userID
}

// globalCtx is the pin's stamped-context helper, carried verbatim over
// the package's own ctxWith (the services read the bridge stamp on the
// caller seam).
func globalCtx(userID string) context.Context {
	return ctxWith(context.Background(), identity.StaffContext{
		UserID: userID, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal, Status: "ACTIVE",
	})
}

// cleanupSession removes a till session and everything hanging off it,
// the pin's helper carried verbatim.
func cleanupSession(pool *pgxpool.Pool, sessionID string) {
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `DELETE FROM payment_allocations WHERE payment_id IN (SELECT id FROM payments WHERE till_session_id = $1)`, sessionID)
	_, _ = pool.Exec(ctx, `DELETE FROM payments WHERE till_session_id = $1`, sessionID)
	_, _ = pool.Exec(ctx, `DELETE FROM till_movements WHERE till_session_id = $1`, sessionID)
	_, _ = pool.Exec(ctx, `DELETE FROM till_tender_totals WHERE till_session_id = $1`, sessionID)
	_, _ = pool.Exec(ctx, `DELETE FROM till_denomination_counts WHERE till_session_id = $1`, sessionID)
	_, _ = pool.Exec(ctx, `DELETE FROM till_sessions WHERE id = $1`, sessionID)
}
