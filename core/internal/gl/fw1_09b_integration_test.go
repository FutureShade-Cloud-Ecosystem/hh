//go:build integration

// fw1_09b_integration_test.go, FW1-09b's red first legs on the real migrated
// and seeded database: a parked journal can never post into a closed month;
// period close runs a pre flight that refuses and names each failing check
// with its figures in dollars; and year end close keeps the closed year's
// books by closing EACH BRANCH's results with the entry dated in the closed
// year.
//
// ISOLATION. Fiscal years 2081, 2082 and 2084, outside the seeded 2026
// periods and every band the package's other integration tests use.
//
//	Run: DATABASE_URL='postgres://.../db?sslmode=disable' go test -tags=integration -count=1 -run FW1_09b ./internal/gl/
package gl

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fw1TwoBranches answers two seeded location ids.
func fw1TwoBranches(t *testing.T, pool *pgxpool.Pool) (string, string) {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT id::text FROM locations ORDER BY id LIMIT 2`)
	if err != nil {
		t.Fatalf("read branches: %v", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan branch: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("branches: %v", err)
	}
	if len(ids) < 2 {
		t.Skipf("need two seeded branches, found %d", len(ids))
	}
	return ids[0], ids[1]
}

// fw1ClosingJournals reads a year's CLOSING journals: branch (empty when
// unbranched), period month, posted date.
func fw1ClosingJournals(t *testing.T, pool *pgxpool.Pool, year int) []struct {
	branch   string
	pmonth   int
	postedAt *time.Time
} {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT COALESCE(j.branch_id::text,''), p.month, j.posted_at
		  FROM gl_journals j JOIN fiscal_periods p ON p.id = j.period_id
		 WHERE j.fiscal_year = $1 AND j.source = 'CLOSING'`, year)
	if err != nil {
		t.Fatalf("read the closing journals: %v", err)
	}
	defer rows.Close()
	var out []struct {
		branch   string
		pmonth   int
		postedAt *time.Time
	}
	for rows.Next() {
		var c struct {
			branch   string
			pmonth   int
			postedAt *time.Time
		}
		if err := rows.Scan(&c.branch, &c.pmonth, &c.postedAt); err != nil {
			t.Fatalf("scan closing journal: %v", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("closing journals: %v", err)
	}
	return out
}

// TestIntegration_FW1_09b_ParkedJournalCannotPostIntoAClosedMonth is item 1's
// red: a June manual journal parked over the maker-checker threshold, approved
// AFTER June closed, posts into the closed June today (both approve paths).
// After the fix both paths refuse in plain words and the journal stays parked.
func TestIntegration_FW1_09b_ParkedJournalCannotPostIntoAClosedMonth(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, 2081)
	branch := ""
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations LIMIT 1`).Scan(&branch); err != nil {
		t.Skipf("no seeded branch: %v", err)
	}
	at := time.Date(2081, 6, 20, 12, 0, 0, 0, time.UTC)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "SALES_MANAGER", Tier: identity.TierManager}, at)

	// Two June journals parked over the seeded $5,000.00 threshold (bare
	// DRAFTs, the shipped no-rail posture).
	mk := func(n int) string {
		t.Helper()
		j, err := svc.CreateJournal(ctx, CreateJournalInput{
			BranchID: branch,
			Memo:     fmt.Sprintf("FW1-09b parked draft %d", n),
			Lines: []JournalLineInput{
				{AccountCode: "1000", DebitCents: 900000, Memo: "bank"},
				{AccountCode: "4000", CreditCents: 900000, Memo: "revenue"},
			},
		})
		if err != nil {
			t.Fatalf("park draft %d: %v", n, err)
		}
		if j.Status != StatusDraft {
			t.Fatalf("draft %d did not park, status %s", n, j.Status)
		}
		return j.ID
	}
	direct, rail := mk(1), mk(2)

	repo := NewRepository(pool)
	if _, err := repo.ClosePeriod(ctx, periodID(t, pool, 2081, 6), user, "FW1-09b close first"); err != nil {
		t.Fatalf("close June 2081: %v", err)
	}

	// The direct route: refused in plain words, the journal stays parked.
	if _, err := repo.ApproveJournal(ctx, direct, user); err == nil {
		t.Fatalf("the parked journal approved into the closed month; a manual journal into a closed month must be refused")
	} else if got := err.Error(); !strings.Contains(got, "closed") || !strings.Contains(got, "manual") {
		t.Fatalf("the refusal = %q, want plain words naming the closed month and a manual journal", got)
	}
	var st string
	if err := pool.QueryRow(ctx, `SELECT status FROM gl_journals WHERE id=$1::uuid`, direct).Scan(&st); err != nil {
		t.Fatal(err)
	}
	if st != StatusDraft {
		t.Fatalf("the refused journal is %s, want it left parked as DRAFT", st)
	}

	// The approvals rail path: refused inside the caller's transaction, the
	// post rolls back with it.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.ApproveJournalTx(ctx, tx, rail, user); err == nil {
		t.Fatalf("the rail approved the parked journal into the closed month; it must be refused")
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM gl_journals WHERE id=$1::uuid`, rail).Scan(&st); err != nil {
		t.Fatal(err)
	}
	if st != StatusDraft {
		t.Fatalf("the rail-refused journal is %s, want it left parked as DRAFT", st)
	}
}

// fw1StockValue, fw1Drawers and fw1Queue are fixed-answer fakes for the pre
// flight's three new seams.
type fw1StockValue struct{ cents int64 }

func (f fw1StockValue) StockValueCents(_ context.Context, _ string) (int64, error) {
	return f.cents, nil
}

type fw1Drawers struct {
	year, month int
	n           int
}

func (f fw1Drawers) OpenDrawersInMonth(_ context.Context, year, month int) (int, error) {
	if year == f.year && month == f.month {
		return f.n, nil
	}
	return 0, nil
}

type fw1Queue struct {
	year, month int
	n           int
}

func (f fw1Queue) UnpostedForWindow(_ context.Context, _ []string, from, _ time.Time) (int, error) {
	if from.UTC().Year() == f.year && int(from.UTC().Month()) == f.month {
		return f.n, nil
	}
	return 0, nil
}

type fw1AR struct{ cents int64 }

func (f fw1AR) ARBalanceCents(_ context.Context, _ time.Time, _ string) (int64, error) {
	return f.cents, nil
}

// TestIntegration_FW1_09b_PeriodCloseRunsAPreFlight is item 2's red: closing
// a month with AR out of tie, stock out of tie, a posting event waiting and
// two drawers still open succeeds today. After the fix the close is refused
// and the refusal names each failing check with its figures in dollars. A
// month the wired seams see clean still closes.
func TestIntegration_FW1_09b_PeriodCloseRunsAPreFlight(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, 2084)
	at := time.Date(2084, 6, 15, 12, 0, 0, 0, time.UTC)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, at).
		WithARSubledger(fw1AR{cents: 250000}).
		WithStockReader(fw1StockValue{cents: 10000}).
		WithDrawerReader(fw1Drawers{year: 2084, month: 6, n: 2}).
		WithQueueReader(fw1Queue{year: 2084, month: 6, n: 1})

	_, err := svc.ClosePeriod(ctx, periodID(t, pool, 2084, 6), "FW1-09b preflight proof")
	if err == nil {
		t.Fatalf("the month closed with AR and stock out of tie, a posting event waiting and two drawers open; the pre flight must refuse")
	}
	msg := err.Error()
	for _, want := range []string{
		"pre flight",
		"$2,500.00",
		"$100.00",
		"1 posting event",
		"2 till drawers",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("the pre flight refusal %q does not name %q", msg, want)
		}
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM fiscal_periods WHERE year=2084 AND month=6`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "OPEN" {
		t.Fatalf("the refused close left the period %s, want OPEN", status)
	}

	// A month the wired seams see quiet still closes: the drawers and the
	// queue answer clean for July. The AR and stock fakes are fixed answer,
	// so July closes under a service whose tie seams are unwired (a nil
	// seam is an unwired check, it never blocks a close).
	quiet := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, at).
		WithDrawerReader(fw1Drawers{year: 2084, month: 6, n: 2}).
		WithQueueReader(fw1Queue{year: 2084, month: 6, n: 1})
	if _, err := quiet.ClosePeriod(ctx, periodID(t, pool, 2084, 7), "FW1-09b clean month"); err != nil {
		t.Fatalf("the clean month did not close: %v", err)
	}
}

// TestIntegration_FW1_09b_YearEndClosesEachBranchsResults is item 3's red
// (the part that needs no view change): two branches traded in 2082, so the
// year end close posts ONE consolidated closing journal with no branch today.
// After the fix each branch's income and expense moves to retained earnings
// in its own closing journal, every line stamped its branch, the entry dated
// in the closed year (calendar December 31; the fiscal year start is stored
// but unused, the FW1-01 report's finding).
func TestIntegration_FW1_09b_YearEndClosesEachBranchsResults(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, 2082)
	branchA, branchB := fw1TwoBranches(t, pool)
	at := time.Date(2082, 6, 15, 12, 0, 0, 0, time.UTC)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, at)

	sell := func(branch string, amount int64, n int) {
		t.Helper()
		_, err := svc.PostExternal(ctx, ExternalJournal{
			Source: SourceInvoice, BranchID: branch,
			IdempotencyKey: fmt.Sprintf("fw109b-%d", n),
			Lines: []ExternalLine{
				{AccountCode: "1000", DebitCents: amount, Memo: "bank"},
				{AccountCode: "4000", CreditCents: amount, Memo: "revenue"},
			},
		})
		if err != nil {
			t.Fatalf("post the sale for %s: %v", branch, err)
		}
	}
	sell(branchA, 10000, 1)
	sell(branchB, 5000, 2)

	res, err := svc.CloseYear(ctx, 2082, "FW1-09b branch close")
	if err != nil {
		t.Fatalf("close year 2082: %v", err)
	}
	if res.NetIncomeCents != 15000 {
		t.Fatalf("net income = %d, want 15000", res.NetIncomeCents)
	}
	closings := fw1ClosingJournals(t, pool, 2082)
	if len(closings) < 2 {
		t.Fatalf("%d closing journals, want one per trading branch (the yards' results must close, not only the company's)", len(closings))
	}
	seen := map[string]bool{}
	for _, c := range closings {
		if c.branch == "" {
			t.Fatalf("a closing journal carries no branch; each branch's results close under its own stamp")
		}
		seen[c.branch] = true
		if c.pmonth != 12 {
			t.Fatalf("a closing journal sits in month %d, want 12 (dated in the closed year)", c.pmonth)
		}
		if c.postedAt == nil || c.postedAt.UTC().Format("2006-01-02") != "2082-12-31" {
			t.Fatalf("a closing journal is dated %v, want 2082-12-31", c.postedAt)
		}
	}
	if !seen[branchA] || !seen[branchB] {
		t.Fatalf("the closing journals cover %v, want both branches", seen)
	}
	if len(res.ClosingJournalIDs) != len(closings) {
		t.Fatalf("the result names %d closing journals, want %d", len(res.ClosingJournalIDs), len(closings))
	}
}
