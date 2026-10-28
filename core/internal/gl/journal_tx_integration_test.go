//go:build integration

// journal_tx_integration_test.go, InsertJournalTx and PostExternalTx against a real database: a journal posted
// through a caller's transaction commits or rolls back with that caller; a refusal (closed period) or an
// idempotency race rolls back only the journal's savepoint and leaves the caller's transaction usable.
//
// ISOLATION. Fiscal year 2087, outside the seeded 2026 periods and every band the package's other integration
// tests use, so the legs never touch rows another test reads.
//
//	Run: DATABASE_URL='postgres://.../db?sslmode=disable' go test -tags=integration -count=1 -run JournalTx ./internal/gl/
package gl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const journalTxYear = 2087

func journalTxSetup(t *testing.T) (*pgxpool.Pool, *Service) {
	t.Helper()
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set")
	}
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, journalTxYear)
	at := time.Date(journalTxYear, 6, 15, 12, 0, 0, 0, time.UTC)
	return pool, svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager}, at)
}

func journalTxExt(key string) ExternalJournal {
	return ExternalJournal{
		Source: SourceAdjustment, IdempotencyKey: key,
		Lines: []ExternalLine{{AccountCode: "1000", DebitCents: 100}, {AccountCode: "1020", CreditCents: 100}},
	}
}

func journalTxCount(t *testing.T, pool *pgxpool.Pool, key string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM gl_journals WHERE idempotency_key = $1`, key).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// journalTxIn runs body inside a transaction the leg owns, so the leg's failure path can be
// exercised on purpose: the failure path is a scope, and what that scope leaves behind is the
// transaction.
//
// FW1-08b part B round 3, item 2 (S4's P1): a leg that fails with the transaction still open holds
// a pooled connection, the next leg waits for that connection, and the suite hangs to its timeout
// instead of failing. S4 hit a 1h30m panic on this leg for exactly that reason.
//
// The rollback is a defer, so it runs on every way out of the scope: the leg's own explicit
// rollback, the leg's commit, and the leg's failure (a Fatalf leaves the scope through
// runtime.Goexit, which unwinds the defer). A rollback after a commit reports the transaction
// closed, and that answer is ignored.
func journalTxIn(t *testing.T, ctx context.Context, pool *pgxpool.Pool, body func(tx pgx.Tx)) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	body(tx)
}

// The journal commits and rolls back with the caller, and a replay through the transaction dedupes.
func TestIntegration_JournalTx_CommitsAndRollsBackWithTheCaller(t *testing.T) {
	ctx := context.Background()
	pool, svc := journalTxSetup(t)
	key := fmt.Sprintf("jtx-%d", time.Now().UnixNano())

	var id string
	journalTxIn(t, ctx, pool, func(tx pgx.Tx) {
		var err error
		id, err = svc.PostExternalTx(ctx, tx, journalTxExt(key))
		if err != nil || id == "" {
			t.Fatalf("post through tx: %q %v", id, err)
		}
		if n := journalTxCount(t, pool, key); n != 0 {
			t.Fatalf("an uncommitted tx journal is visible to another session (%d)", n)
		}
		if replay, err := svc.PostExternalTx(ctx, tx, journalTxExt(key)); err != nil || replay != id {
			t.Fatalf("replay through the same tx = %q %v, want %q", replay, err, id)
		}
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
	})
	if n := journalTxCount(t, pool, key); n != 0 {
		t.Fatalf("a rolled back caller left %d journal(s)", n)
	}

	journalTxIn(t, ctx, pool, func(tx pgx.Tx) {
		var err error
		id, err = svc.PostExternalTx(ctx, tx, journalTxExt(key))
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
	})
	if n := journalTxCount(t, pool, key); n != 1 {
		t.Fatalf("committed caller left %d journal(s), want 1", n)
	}
	// The pool variant dedupes onto the committed journal: same external behaviour.
	if again, err := svc.PostExternal(ctx, journalTxExt(key)); err != nil || again != id {
		t.Fatalf("pool replay = %q %v, want %q", again, err, id)
	}
}

// A refusal rolls back only the journal's savepoint: the caller's earlier work survives and the transaction
// stays usable.
func TestIntegration_JournalTx_RefusalLeavesTheCallerUsable(t *testing.T) {
	ctx := context.Background()
	pool, svc := journalTxSetup(t)
	user := aUser(t, pool)
	closed := periodID(t, pool, journalTxYear, 3)
	if _, err := NewRepository(pool).ClosePeriod(ctx, closed, user, "journal tx leg"); err != nil {
		t.Fatalf("close period: %v", err)
	}
	okKey := fmt.Sprintf("jtx-ok-%d", time.Now().UnixNano())

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := svc.PostExternalTx(ctx, tx, journalTxExt(okKey)); err != nil {
		t.Fatalf("first post: %v", err)
	}
	// Refused: an unknown account (a statement that does not abort on its own). A closed period no
	// longer refuses a system journal through the tx (FW1-01, ruling 82.2: it posts into the
	// earliest open month instead), so the savepoint rollback proof rides the account refusal.
	closedSvc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager},
		time.Date(journalTxYear, 3, 15, 12, 0, 0, 0, time.UTC))
	_ = closedSvc
	bad := journalTxExt(okKey + "-acct")
	bad.Lines[0].AccountCode = "no-such-account"
	if _, err := svc.PostExternalTx(ctx, tx, bad); err == nil {
		t.Fatal("unknown account through tx must be refused")
	}
	if _, err := tx.Exec(ctx, `SELECT 1`); err != nil {
		t.Fatalf("the caller's transaction is unusable after a refusal: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if n := journalTxCount(t, pool, okKey); n != 1 {
		t.Fatalf("earlier journal count = %d, want 1 (a refusal must not take it with it)", n)
	}
	for _, k := range []string{okKey + "-closed", okKey + "-acct"} {
		if n := journalTxCount(t, pool, k); n != 0 {
			t.Fatalf("refused journal %s left %d row(s)", k, n)
		}
	}
}

// Losing the idempotency race: the other session holds the same key uncommitted, this caller blocks on the
// unique index, the other commits, and this caller gets the winner back with its transaction intact.
func TestIntegration_JournalTx_LosesTheKeyRaceCleanly(t *testing.T) {
	ctx := context.Background()
	pool, svc := journalTxSetup(t)
	key := fmt.Sprintf("jtx-race-%d", time.Now().UnixNano())

	winner, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = winner.Rollback(ctx) }()
	winnerID, err := svc.PostExternalTx(ctx, winner, journalTxExt(key))
	if err != nil {
		t.Fatalf("winner post: %v", err)
	}

	type result struct {
		id  string
		err error
	}
	done := make(chan result, 1)
	loser, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = loser.Rollback(ctx) }()
	go func() {
		id, err := svc.PostExternalTx(ctx, loser, journalTxExt(key))
		done <- result{id, err}
	}()

	// Commit the winner only once the loser is parked behind it, so the loser really loses at the insert.
	for until := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		var parked int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND state = 'active'`).Scan(&parked); err != nil {
			t.Fatal(err)
		}
		if parked > 0 {
			break
		}
		if time.Now().After(until) {
			t.Fatal("the loser never parked behind the winner")
		}
	}
	if err := winner.Commit(ctx); err != nil {
		t.Fatalf("winner commit: %v", err)
	}
	select {
	case r := <-done:
		if r.err != nil || r.id != winnerID {
			t.Fatalf("loser = %q %v, want the winner %q", r.id, r.err, winnerID)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the loser never returned")
	}
	if _, err := loser.Exec(ctx, `SELECT 1`); err != nil {
		t.Fatalf("the loser's transaction is unusable after losing the race: %v", err)
	}
	if err := loser.Commit(ctx); err != nil {
		t.Fatalf("loser commit: %v", err)
	}
	if n := journalTxCount(t, pool, key); n != 1 {
		t.Fatalf("%d journals under the key, want 1", n)
	}
}

// A nil transaction is refused, never silently served from the pool.
func TestIntegration_JournalTx_NilTxRefused(t *testing.T) {
	_, svc := journalTxSetup(t)
	if _, err := svc.PostExternalTx(context.Background(), nil, journalTxExt("jtx-nil")); !errors.Is(err, errNilTx) {
		t.Fatalf("nil tx = %v, want errNilTx", err)
	}
	if _, err := svc.ResolveRuleTx(context.Background(), nil, "vendor.bill.posted", "ap", ""); !errors.Is(err, errNilTx) {
		t.Fatalf("nil tx rule read = %v, want errNilTx", err)
	}
}

// The leg's failure path leaves no open transaction: an open transaction holds a pooled connection,
// so the next leg waits for it and a red in the leg hangs the suite to its timeout instead of
// failing it (FW1-08b part B round 3, item 2).
func TestIntegration_JournalTx_TheFailurePathLeavesNoOpenTransaction(t *testing.T) {
	ctx := context.Background()
	pool, svc := journalTxSetup(t)
	key := fmt.Sprintf("jtx-failpath-%d", time.Now().UnixNano())

	var tx pgx.Tx
	journalTxIn(t, ctx, pool, func(outer pgx.Tx) {
		tx = outer
		bad := journalTxExt(key)
		bad.Lines[0].AccountCode = "no-such-account"
		if _, err := svc.PostExternalTx(ctx, outer, bad); err == nil {
			t.Error("the unknown account post must be refused")
		}
		// The failure path: the leg stops here the way a red leg stops, and it does not roll
		// the transaction back by hand.
	})
	// The failure path must have closed the transaction. Rolling it back again must report it
	// closed.
	if err := tx.Rollback(ctx); !errors.Is(err, pgx.ErrTxClosed) {
		t.Fatalf("the failure path left the transaction open (the rollback said %v): a red in this leg hangs the suite to its timeout instead of failing it", err)
	}
}
