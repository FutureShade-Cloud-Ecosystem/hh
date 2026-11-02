//go:build integration

// export_period_integration_test.go, W3 AX-1 item 2: the export's period pick (a month, a quarter
// or a year, on the house days the ledger already files by) and the mapping precondition: an
// account with movement in the period and no mapping for the target refuses the export, in words,
// naming the account. The door is POST /api/v1/gl/export; without a target it stays the GP-parity
// draft it was.

package gl

import (
	"context"
	"strings"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// exportSvc builds a Controller service pinned to the named instant, the caller the export door's
// RequireAdmin gate admits and its service work runs as.
func exportSvc(t *testing.T, pool *pgxpool.Pool, at time.Time) *Service {
	t.Helper()
	user := aUser(t, pool)
	return svcAt(pool, identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, at)
}

// seedExportJournal posts one balanced external journal into the named house month, the movement
// the export period then carries. It posts through its own service pinned to the month, the same
// house-day filing the real posting path takes, and answers the journal's id so a leg's cleanup
// can delete exactly what it created (a caller that needs no id ignores the answer).
func seedExportJournal(t *testing.T, pool *pgxpool.Pool, year, month int, key, debitCode, creditCode string, cents int64) string {
	t.Helper()
	user := aUser(t, pool)
	at := time.Date(year, time.Month(month), 10, 12, 0, 0, 0, time.UTC)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, at)
	id, err := svc.PostExternal(context.Background(), ExternalJournal{
		Source: SourceAdjustment, IdempotencyKey: key, Memo: "period pick",
		Lines: []ExternalLine{{AccountCode: debitCode, DebitCents: cents}, {AccountCode: creditCode, CreditCents: cents}},
	})
	if err != nil {
		t.Fatalf("seed the period journal: %v", err)
	}
	return id
}

// TestIntegration_Export_AnUnmappedAccountRefusesNamingIt is red leg 1: one account with movement
// and no mapping refuses the target's export, in words, naming the account.
func TestIntegration_Export_AnUnmappedAccountRefusesNamingIt(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	ensurePeriods(t, pool, 2094)
	svc := exportSvc(t, pool, time.Date(2094, 7, 10, 12, 0, 0, 0, time.UTC))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM gl_account_mapping WHERE target = $1`, TargetQuickBooksOnline)
	})

	seedExportJournal(t, pool, 2094, 7, "ax1-unmapped", "1300", "2300", 50000)

	_, err := svc.Export(ctx, ExportQuery{Year: 2094, Month: 7, Target: TargetQuickBooksOnline})
	if err == nil {
		t.Fatal("an unmapped account with movement exported anyway")
	}
	if !strings.Contains(err.Error(), "no mapping") {
		t.Fatalf("the refusal must say the mapping is missing, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "1300") || !strings.Contains(err.Error(), "2300") {
		t.Fatalf("the refusal must name every unmapped account, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "QuickBooks Online") {
		t.Fatalf("the refusal must name the target package, got %q", err.Error())
	}
}

// TestIntegration_Export_AFullyMappedMonthExportsAndBalances is red leg 2: a fully mapped period
// exports, and its rendered debits equal its rendered credits. The QuickBooks Online target's body
// is QBO's journal entry import CSV (AX-2), so the leg reads it the way its importer does.
func TestIntegration_Export_AFullyMappedMonthExportsAndBalances(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	ensurePeriods(t, pool, 2094)
	svc := exportSvc(t, pool, time.Date(2094, 5, 10, 12, 0, 0, 0, time.UTC))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM gl_account_mapping WHERE target = $1`, TargetQuickBooksOnline)
	})

	if _, err := svc.SetAccountMapping(ctx, TargetQuickBooksOnline, "1000", "1010"); err != nil {
		t.Fatalf("map the cash account: %v", err)
	}
	if _, err := svc.SetAccountMapping(ctx, TargetQuickBooksOnline, "4000", "4010"); err != nil {
		t.Fatalf("map the revenue account: %v", err)
	}
	seedExportJournal(t, pool, 2094, 5, "ax1-mapped", "1000", "4000", 250000)

	res, err := svc.Export(ctx, ExportQuery{Year: 2094, Month: 5, Target: TargetQuickBooksOnline})
	if err != nil {
		t.Fatalf("the fully mapped period must export: %v", err)
	}
	if res.Format != TargetQuickBooksOnline {
		t.Fatalf("format = %q, want the target's word", res.Format)
	}
	if res.BatchID == "" {
		t.Fatal("the target export must record its batch")
	}
	// The rendered rows carry the PACKAGE's account codes, read as the importer reads them.
	recs := qboCSV(t, res.Content)
	if len(recs) != 3 {
		t.Fatalf("the month's one journal exports as one entry of two rows, got %d lines:\n%s", len(recs), res.Content)
	}
	if recs[1][2] != "1010" || recs[2][2] != "4010" {
		t.Fatalf("the export must carry the mapped package codes, got:\n%s", res.Content)
	}
	// And the file's own two sides tie: the red leg's assertion, read off the rendered rows.
	debit, credit := qboSum(t, recs, recs[1][0])
	if debit == 0 || debit != credit {
		t.Fatalf("the export's debits (%d cents) must equal its credits (%d cents), content:\n%s", debit, credit, res.Content)
	}
}

// TestIntegration_Export_TheQuarterPickReadsItsThreeMonths pins the quarter pick at the row read:
// the quarter's rows are exactly its three house months', no more, no fewer. The batch write of a
// wider than month pick is item 3's leg.
func TestIntegration_Export_TheQuarterPickReadsItsThreeMonths(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	ensurePeriods(t, pool, 2094)

	seedExportJournal(t, pool, 2094, 4, "ax1-q2a", "1000", "4000", 11100)
	seedExportJournal(t, pool, 2094, 5, "ax1-q2b", "1000", "4000", 22200)
	seedExportJournal(t, pool, 2094, 6, "ax1-q2c", "1000", "4000", 33300)

	repo := NewRepository(pool)
	q2, err := repo.ExportRows(ctx, 2094, []int{4, 5, 6}, "")
	if err != nil {
		t.Fatalf("read the quarter's rows: %v", err)
	}
	may, err := repo.ExportRows(ctx, 2094, []int{5}, "")
	if err != nil {
		t.Fatalf("read the month's rows: %v", err)
	}
	year, err := repo.ExportRows(ctx, 2094, nil, "")
	if err != nil {
		t.Fatalf("read the whole year's rows: %v", err)
	}
	if len(q2) != 6 {
		t.Fatalf("the quarter pick reads %d rows, want the quarter's six (three balanced journals, two legs each)", len(q2))
	}
	if len(may) != 2 {
		t.Fatalf("the month pick reads %d rows, want May's two", len(may))
	}
	if len(year) < len(q2) {
		t.Fatalf("the year pick (%d rows) cannot be narrower than its quarter (%d)", len(year), len(q2))
	}
}

// TestIntegration_Export_ThePickRefusesImpossiblePeriods words the pick's own bounds.
func TestIntegration_Export_ThePickRefusesImpossiblePeriods(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := exportSvc(t, pool, time.Date(2094, 3, 10, 12, 0, 0, 0, time.UTC))

	if _, err := svc.Export(ctx, ExportQuery{Year: 0, Month: 5}); err == nil || !strings.Contains(err.Error(), "fiscal_year") {
		t.Fatalf("a yearless export must be refused in words, got %v", err)
	}
	if _, err := svc.Export(ctx, ExportQuery{Year: 2094, Month: 13}); err == nil {
		t.Fatal("month 13 took an export")
	}
	if _, err := svc.Export(ctx, ExportQuery{Year: 2094, Quarter: 5}); err == nil {
		t.Fatal("quarter 5 took an export")
	}
	if _, err := svc.Export(ctx, ExportQuery{Year: 2094, Month: 3, Quarter: 1}); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("a month and a quarter together must be refused in words, got %v", err)
	}
	if _, err := svc.Export(ctx, ExportQuery{Year: 2094, Month: 3, Target: "XERO"}); err == nil || !strings.Contains(err.Error(), "XERO") {
		t.Fatalf("an unknown target must be refused in words, got %v", err)
	}
}
