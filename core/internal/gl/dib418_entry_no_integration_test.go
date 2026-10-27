//go:build integration

// dib418_entry_no_integration_test.go, SWARM-W4-C DIB-418.
//
// The defect: InsertJournal allocated entry_no with COALESCE(MAX(entry_no),0)+1 and inserted it under
// UNIQUE (fiscal_year, entry_no), a read-modify-write with no lock between read and write. Two concurrent
// posting transactions read the same MAX (an uncommitted neighbour's header is invisible under READ
// COMMITTED), computed the same number, and the loser died on the unique constraint. Migration 0119 replaces
// the expression with next_entry_no(fiscal_year), which locks the year's counter row FOR UPDATE before
// reading it.
//
// These tests are the proof, and they are written so they FAIL on the pre-fix code: run the burst below
// against MAX()+1 and a meaningful fraction of the 50 posts return `duplicate key value violates unique
// constraint "gl_journals_fiscal_year_entry_no_key"`.
//
// ISOLATION. Both tests use fiscal year 2089, outside the seeded 2026 periods and outside the 2091-2099
// band the rest of this package's integration tests use, so the "gapless" assertion is a per-test
// invariant over rows only this test writes, NOT a whole-DB invariant. That is deliberate: a gaplessness
// claim over a year other packages post into would be a DIB-396 shared-DB flake, and it would need an
// exclusive lease. Scoping the year instead needs none, and it survives `go test ./... -p N` unchanged.
package gl

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// dib418Year is this test's private fiscal year (see ISOLATION above).
const dib418Year = 2089

// dib418Cleanup drops the counter row for the test year. ensurePeriods already removes the journals + lines +
// periods; the counter row is 0119's addition and is this file's to clean.
func dib418Cleanup(t *testing.T, pool *pgxpool.Pool, year int) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM gl_entry_no_sequences WHERE fiscal_year = $1`, year)
	})
}

// TestIntegration_DIB418_ConcurrentPostsMintDistinctEntryNos is the 50-goroutine burst. Every post must
// SUCCEED (the pre-fix code loses a race and returns a unique-violation), every entry_no must be distinct
// (the constraint would have caught a duplicate anyway, this asserts we never got that far), and the 50
// numbers must form one contiguous run with no gap (the property a Postgres sequence could not give,
// because nextval() survives a rollback).
func TestIntegration_DIB418_ConcurrentPostsMintDistinctEntryNos(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, dib418Year)
	dib418Cleanup(t, pool, dib418Year)

	at := time.Date(dib418Year, 6, 15, 12, 0, 0, 0, time.UTC)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager, Scope: identity.ScopeGlobal}, at)

	const n = 50
	run := time.Now().UnixNano()
	errs := make([]error, n)
	ids := make([]string, n)

	// A start barrier so all 50 hit the mint at once rather than trickling in as goroutines are scheduled -
	// without it the race window is easy to miss and the test would pass on the broken code.
	var start sync.WaitGroup
	start.Add(1)
	var done sync.WaitGroup
	done.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer done.Done()
			start.Wait()
			id, err := svc.PostExternal(ctx, ExternalJournal{
				Source:         SourceAdjustment,
				IdempotencyKey: fmt.Sprintf("dib418-%d-%d", run, i),
				Memo:           "DIB-418 concurrent mint",
				Lines: []ExternalLine{
					{AccountCode: "1000", DebitCents: 100, Memo: "bank"},
					{AccountCode: "1020", CreditCents: 100, Memo: "undeposited"},
				},
			})
			ids[i], errs[i] = id, err
		}(i)
	}
	start.Done()
	done.Wait()

	var failed int
	for i, err := range errs {
		if err != nil {
			failed++
			if failed <= 3 {
				t.Errorf("concurrent post %d failed: %v", i, err)
			}
		}
	}
	if failed > 0 {
		t.Fatalf("%d/%d concurrent posts failed, entry_no is still a read-modify-write race", failed, n)
	}
	for i, id := range ids {
		if id == "" {
			t.Fatalf("post %d returned an empty journal id", i)
		}
	}

	rows, err := pool.Query(ctx,
		`SELECT entry_no FROM gl_journals WHERE fiscal_year = $1 ORDER BY entry_no`, dib418Year)
	if err != nil {
		t.Fatalf("read entry_nos: %v", err)
	}
	defer rows.Close()
	var got []int
	for rows.Next() {
		var e int
		if err := rows.Scan(&e); err != nil {
			t.Fatal(err)
		}
		got = append(got, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != n {
		t.Fatalf("fiscal year %d holds %d journals, want %d", dib418Year, len(got), n)
	}
	seen := make(map[int]bool, n)
	for _, e := range got {
		if seen[e] {
			t.Fatalf("entry_no %d minted twice", e)
		}
		seen[e] = true
	}
	// Gapless: sorted ascending, each number is exactly one more than the last.
	for i := 1; i < len(got); i++ {
		if got[i] != got[i-1]+1 {
			t.Fatalf("gap in entry_no run: %d follows %d (all: %v)", got[i], got[i-1], got)
		}
	}

	// The counter row and the register agree, the sequence row is the register's mirror, not a parallel truth.
	var last int64
	if err := pool.QueryRow(ctx,
		`SELECT last_value FROM gl_entry_no_sequences WHERE fiscal_year = $1`, dib418Year).Scan(&last); err != nil {
		t.Fatalf("read counter row: %v", err)
	}
	if int(last) != got[len(got)-1] {
		t.Fatalf("counter row last_value = %d but the highest posted entry_no is %d", last, got[len(got)-1])
	}
}

// TestIntegration_DIB418_HealsAfterADirectEntryNoWrite is the regression the parallel suite found. entry_no
// is a plain column, and three shipped fixtures plus migration 0056 write it directly, pushing MAX(entry_no)
// above the counter row. 0119 trusted the counter blindly, so the next real post handed back a number that
// already existed, the very unique violation DIB-418 exists to remove, now caused by the fix for it.
//
// 0123 floors the counter at the register under the lock it already holds. This test reproduces the exact
// shape: write a journal directly with a number well above the counter, then post normally and require it to
// succeed with a number ABOVE the direct write.
func TestIntegration_DIB418_HealsAfterADirectEntryNoWrite(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, dib418Year)
	dib418Cleanup(t, pool, dib418Year)

	at := time.Date(dib418Year, 6, 15, 12, 0, 0, 0, time.UTC)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager, Scope: identity.ScopeGlobal}, at)

	// One normal post, so the counter row exists and is in step.
	if _, err := svc.PostExternal(ctx, ExternalJournal{
		Source: SourceAdjustment, IdempotencyKey: fmt.Sprintf("dib418-heal-a-%d", time.Now().UnixNano()),
		Memo: "before the direct write",
		Lines: []ExternalLine{
			{AccountCode: "1000", DebitCents: 100, Memo: "bank"},
			{AccountCode: "1020", CreditCents: 100, Memo: "undeposited"},
		},
	}); err != nil {
		t.Fatalf("baseline post: %v", err)
	}

	// A fixture-shaped direct write: it picks its own number and never touches the counter.
	const direct = 5000
	pid := periodID(t, pool, dib418Year, 6)
	if _, err := pool.Exec(ctx, `
		INSERT INTO gl_journals (fiscal_year, entry_no, period_id, source, memo, status)
		VALUES ($1, $2, $3, 'MANUAL', 'direct fixture write', 'DRAFT')`, dib418Year, direct, pid); err != nil {
		t.Fatalf("direct journal write: %v", err)
	}

	// The next mint must step OVER the direct write, not collide with it.
	if _, err := svc.PostExternal(ctx, ExternalJournal{
		Source: SourceAdjustment, IdempotencyKey: fmt.Sprintf("dib418-heal-b-%d", time.Now().UnixNano()),
		Memo: "after the direct write",
		Lines: []ExternalLine{
			{AccountCode: "1000", DebitCents: 100, Memo: "bank"},
			{AccountCode: "1020", CreditCents: 100, Memo: "undeposited"},
		},
	}); err != nil {
		t.Fatalf("post after a direct entry_no write: %v, the counter did not reconcile with the register", err)
	}

	var got int
	if err := pool.QueryRow(ctx,
		`SELECT max(entry_no) FROM gl_journals WHERE fiscal_year = $1`, dib418Year).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != direct+1 {
		t.Fatalf("highest entry_no = %d, want %d (one past the direct write)", got, direct+1)
	}
}

// TestIntegration_DIB418_RolledBackMintReturnsTheNumber is the reason 0119 is a TABLE ROW and not a Postgres
// sequence: the bump lives in the caller's transaction, so a posting transaction that fails gives its number
// back instead of burning it. A journal register with holes is a finding in an ASPE/CRA audit trail; a
// sequence would produce one on every failed post.
func TestIntegration_DIB418_RolledBackMintReturnsTheNumber(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, dib418Year)
	dib418Cleanup(t, pool, dib418Year)

	at := time.Date(dib418Year, 6, 15, 12, 0, 0, 0, time.UTC)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager, Scope: identity.ScopeGlobal}, at)

	// Mint inside a transaction and abandon it, exactly what InsertJournal does when its header insert or
	// its deferred balance check fails.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	var burned int
	if err := tx.QueryRow(ctx, `SELECT next_entry_no($1)`, dib418Year).Scan(&burned); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("mint: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	if _, err := svc.PostExternal(ctx, ExternalJournal{
		Source:         SourceAdjustment,
		IdempotencyKey: fmt.Sprintf("dib418-rollback-%d", time.Now().UnixNano()),
		Memo:           "DIB-418 rollback reclaim",
		Lines: []ExternalLine{
			{AccountCode: "1000", DebitCents: 100, Memo: "bank"},
			{AccountCode: "1020", CreditCents: 100, Memo: "undeposited"},
		},
	}); err != nil {
		t.Fatalf("post after rolled-back mint: %v", err)
	}

	var got int
	if err := pool.QueryRow(ctx,
		`SELECT entry_no FROM gl_journals WHERE fiscal_year = $1 ORDER BY entry_no DESC LIMIT 1`,
		dib418Year).Scan(&got); err != nil {
		t.Fatalf("read entry_no: %v", err)
	}
	if got != burned {
		t.Fatalf("rolled-back mint burned entry_no %d, the next post took %d, leaving a hole in the register", burned, got)
	}
}
