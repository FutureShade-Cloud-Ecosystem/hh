//go:build integration

// fw1_09b_round3_integration_test.go, FW1-09b round 3's red first legs
// (SECURITY-SOD gap 19, S2's PR 615 P2-2, the lead's brief): the ledger's
// WRITE routes are held to the caller's branch reach the way the reads are,
// and each write reads its answer back through the store, so an answer never
// contradicts a committed write.
package gl

import (
	"context"
	"strings"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fw1Scoped answers a manager whose reach is one branch only.
func fw1Scoped(userID, branch string) identity.StaffContext {
	return identity.StaffContext{UserID: userID, RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership, Scope: identity.ScopeBranch, Branches: map[string]bool{branch: true}}
}

// fw1JournalsByMemo counts the journals carrying a memo (the probe's committed
// row count).
func fw1JournalsByMemo(t *testing.T, pool *pgxpool.Pool, memo string) int64 {
	t.Helper()
	var n int64
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM gl_journals WHERE memo=$1`, memo).Scan(&n); err != nil {
		t.Fatalf("count journals by memo: %v", err)
	}
	return n
}

// TestIntegration_FW1_09bR3_JournalWritesHeldToTheCallersBranch is the create,
// reverse and approve red: a Kingston only manager writing Trenton's books
// commits rows today (S2's probe: the create landed and answered not found,
// the reversal wrote and answered not found). After the fix each write is
// refused before it lands, in plain words, with nothing committed, and a
// consolidated (no branch) manual journal needs a dealer wide caller.
func TestIntegration_FW1_09bR3_JournalWritesHeldToTheCallersBranch(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	trenton, kingston := fw1TwoBranches(t, pool)
	clockAt := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	// The red run of this leg may have committed behind the old open fence;
	// the memos start clean whatever the database holds.
	for _, memo := range []string{"R3 out of reach create", "R3 consolidated create", "R3 reversal target", "R3 my own branch create", "R3 out of reach park"} {
		if _, err := pool.Exec(ctx, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id=j.id AND j.memo=$1`, memo); err != nil {
			t.Fatalf("scrub %s: %v", memo, err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM gl_journals WHERE memo=$1`, memo); err != nil {
			t.Fatalf("scrub %s: %v", memo, err)
		}
	}
	scoped := svcAt(pool, fw1Scoped(user, kingston), clockAt)
	global := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, clockAt)

	// CREATE at a branch out of reach: refused before it lands.
	if _, err := scoped.CreateJournal(ctx, CreateJournalInput{
		BranchID: trenton, Memo: "R3 out of reach create",
		Lines: []JournalLineInput{
			{AccountCode: "1000", DebitCents: 100, Memo: "bank"},
			{AccountCode: "4000", CreditCents: 100, Memo: "revenue"},
		},
	}); err == nil {
		t.Fatalf("a branch scoped manager created a journal in another branch's books; the write fence must refuse it")
	} else if got := err.Error(); !strings.Contains(got, "does not cover") {
		t.Fatalf("the refusal = %q, want plain words about the books the sign in covers", got)
	}
	if n := fw1JournalsByMemo(t, pool, "R3 out of reach create"); n != 0 {
		t.Fatalf("%d journals committed behind the refused create; an answer must never contradict a write", n)
	}

	// CREATE with no branch: the consolidated books, a dealer wide write.
	if _, err := scoped.CreateJournal(ctx, CreateJournalInput{
		Memo: "R3 consolidated create",
		Lines: []JournalLineInput{
			{AccountCode: "1000", DebitCents: 100, Memo: "bank"},
			{AccountCode: "4000", CreditCents: 100, Memo: "revenue"},
		},
	}); err == nil {
		t.Fatalf("a branch scoped manager wrote the consolidated books; a no branch journal needs a dealer wide caller")
	}
	if n := fw1JournalsByMemo(t, pool, "R3 consolidated create"); n != 0 {
		t.Fatalf("%d consolidated journals committed behind the refused create", n)
	}

	// REVERSE a Trenton journal: refused, no reversal written.
	if _, err := global.CreateJournal(ctx, CreateJournalInput{
		BranchID: trenton, Memo: "R3 reversal target",
		Lines: []JournalLineInput{
			{AccountCode: "1000", DebitCents: 300, Memo: "bank"},
			{AccountCode: "4000", CreditCents: 300, Memo: "revenue"},
		},
	}); err != nil {
		t.Fatalf("the global create: %v", err)
	}
	var targetID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM gl_journals WHERE memo='R3 reversal target'`).Scan(&targetID); err != nil {
		t.Fatalf("read the target: %v", err)
	}
	if _, err := scoped.ReverseJournal(ctx, targetID); err == nil {
		t.Fatalf("a branch scoped manager reversed another branch's journal; the write fence must refuse it")
	} else if got := err.Error(); !strings.Contains(got, "does not cover") {
		t.Fatalf("the reversal refusal = %q, want plain words about the books the sign in covers", got)
	}
	var reversals int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gl_journals WHERE reverses_journal_id=$1::uuid`, targetID).Scan(&reversals); err != nil {
		t.Fatal(err)
	}
	if reversals != 0 {
		t.Fatalf("%d reversals committed behind the refused reverse", reversals)
	}

	// APPROVE a Trenton park: refused, the journal stays parked.
	maker := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "SALES_MANAGER", Tier: identity.TierManager, Scope: identity.ScopeGlobal}, clockAt)
	parked, err := maker.CreateJournal(ctx, CreateJournalInput{
		BranchID: trenton, Memo: "R3 out of reach park",
		Lines: []JournalLineInput{
			{AccountCode: "1000", DebitCents: 900000, Memo: "bank"},
			{AccountCode: "4000", CreditCents: 900000, Memo: "revenue"},
		},
	})
	if err != nil {
		t.Fatalf("park the trenton journal: %v", err)
	}
	if parked.Status != StatusDraft {
		t.Fatalf("the park did not park, status %s", parked.Status)
	}
	if _, err := scoped.ApproveJournal(ctx, parked.ID); err == nil {
		t.Fatalf("a branch scoped manager approved another branch's parked journal; the write fence must refuse it")
	} else if got := err.Error(); !strings.Contains(got, "does not cover") {
		t.Fatalf("the approve refusal = %q, want plain words about the books the sign in covers", got)
	}
	var st string
	if err := pool.QueryRow(ctx, `SELECT status FROM gl_journals WHERE id=$1::uuid`, parked.ID).Scan(&st); err != nil {
		t.Fatal(err)
	}
	if st != StatusDraft {
		t.Fatalf("the refused approve left the journal %s, want it still parked", st)
	}

	// The allowed path answers fully: a Kingston manager creates and reads
	// back their own branch's journal, legs and status intact.
	mine, err := scoped.CreateJournal(ctx, CreateJournalInput{
		BranchID: kingston, Memo: "R3 my own branch create",
		Lines: []JournalLineInput{
			{AccountCode: "1000", DebitCents: 200, Memo: "bank"},
			{AccountCode: "4000", CreditCents: 200, Memo: "revenue"},
		},
	})
	if err != nil {
		t.Fatalf("the create inside the caller's own reach: %v", err)
	}
	if mine.ID == "" || mine.Status != StatusPosted || len(mine.Lines) != 2 {
		t.Fatalf("the allowed create answered %+v, want the journal with its legs read back through the store", mine)
	}
	t.Cleanup(func() {
		for _, memo := range []string{"R3 reversal target", "R3 my own branch create", "R3 out of reach park"} {
			_, _ = pool.Exec(ctx, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id=j.id AND j.memo=$1`, memo)
			_, _ = pool.Exec(ctx, `DELETE FROM gl_journals WHERE memo=$1`, memo)
		}
	})
}

// TestIntegration_FW1_09bR3_PeriodAndAccountWritesNeedDealerWide is the
// consolidated writes' red: period close and reopen, the year end close, and
// the account deactivate are dealer wide acts, and a branch scoped caller
// runs them today. After the fix each is refused in plain words.
func TestIntegration_FW1_09bR3_PeriodAndAccountWritesNeedDealerWide(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, 2094)
	trenton, _ := fw1TwoBranches(t, pool)
	scopedManager := svcAt(pool, fw1Scoped(user, trenton), time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC))
	scopedController := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeBranch, Branches: map[string]bool{trenton: true}}, time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC))

	if _, err := scopedManager.ClosePeriod(ctx, periodID(t, pool, 2094, 6), "R3 scoped close"); err == nil {
		t.Fatalf("a branch scoped manager closed a company wide period; the close is a dealer wide act")
	} else if got := err.Error(); !strings.Contains(got, "dealer wide") {
		t.Fatalf("the close refusal = %q, want plain words naming the dealer wide act", got)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM fiscal_periods WHERE year=2094 AND month=6`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "OPEN" {
		t.Fatalf("the refused close left the period %s, want OPEN", status)
	}

	if _, err := scopedController.CloseYear(ctx, 2094, "R3 scoped year end"); err == nil {
		t.Fatalf("a branch scoped controller ran the year end close; it is a dealer wide act")
	}

	global := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC))
	if _, err := global.ClosePeriod(ctx, periodID(t, pool, 2094, 6), "R3 global close"); err != nil {
		t.Fatalf("the dealer wide close: %v", err)
	}
	// The year end runs after 2094 has ended (the FW1-09b year end refusal).
	yearEnder := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, time.Date(2095, 1, 5, 12, 0, 0, 0, time.UTC))
	if _, err := yearEnder.CloseYear(ctx, 2094, "R3 global year end"); err != nil {
		t.Fatalf("the dealer wide year end: %v", err)
	}
	if _, err := scopedController.ReopenPeriod(ctx, periodID(t, pool, 2094, 6), "R3 scoped reopen"); err == nil {
		t.Fatalf("a branch scoped controller reopened a period; reopen is a dealer wide act")
	}

	// ACCOUNT deactivate: the chart of accounts is dealer wide. The leg plants
	// an account of its own, so no fixture's drafts can stand in the way.
	code := "9999"
	if _, err := pool.Exec(ctx, `DELETE FROM gl_accounts WHERE code=$1`, code); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM gl_accounts WHERE code=$1`, code)
	})
	if _, err := NewRepository(pool).InsertAccount(ctx, CreateAccountInput{
		Code: code, Name: "FW1-09b round 3 account", AccountType: "ASSET", NormalBalance: "DEBIT",
	}); err != nil {
		t.Fatalf("plant the account: %v", err)
	}
	if _, err := scopedManager.DeactivateAccount(ctx, code); err == nil {
		t.Fatalf("a branch scoped manager deactivated a chart of accounts account; it is a dealer wide act")
	}
	var active bool
	if err := pool.QueryRow(ctx, `SELECT is_active FROM gl_accounts WHERE code=$1`, code).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if !active {
		t.Fatalf("the refused deactivate left %s inactive", code)
	}
	if _, err := global.DeactivateAccount(ctx, code); err != nil {
		t.Fatalf("the dealer wide deactivate: %v", err)
	}
}
