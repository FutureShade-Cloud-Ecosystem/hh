//go:build integration

// export_qbo_legs_integration_test.go, W3 AX-2 item 2: the legs. The file's debits equal its
// credits per entry and in total; the file's totals equal the GL for the period, per account; an
// unmapped account refuses in words and writes nothing; a quarter and a whole year pick work as
// AX-1's do; and the house day bounds the period, both which months the pick reads and which day
// the file dates an entry by. S7's P3s on 719 (item 0.18) are fixed where they touch this path:
// their legs live here and in export_period_p3_test.go.

package gl

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedQBOJournalAt posts one balanced external journal at a chosen instant. The service's clock is
// the journal's occurred instant, so the instant names the house day the journal files into: the
// boundary legs post at instants whose UTC day and house day differ.
func seedQBOJournalAt(t *testing.T, pool *pgxpool.Pool, at time.Time, key, debitCode, creditCode string, cents int64) {
	t.Helper()
	user := aUser(t, pool)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, at)
	if _, err := svc.PostExternal(context.Background(), ExternalJournal{
		Source: SourceAdjustment, IdempotencyKey: key, Memo: "qbo leg",
		Lines: []ExternalLine{{AccountCode: debitCode, DebitCents: cents}, {AccountCode: creditCode, CreditCents: cents}},
	}); err != nil {
		t.Fatalf("seed the journal at %s: %v", at.Format(time.RFC3339), err)
	}
}

// TestIntegration_SetAccountMapping_AnUnknownHHAccountRefusesInWords is P3-1 on the real core: the
// mapping set is a Controller's act on the chart's own codes, so a code the chart does not hold is
// refused in words at the seam (the 400 shape), never the storage layer's foreign-key 500. The
// code is derived from the chart itself, because the chart is the seed's: a hardcoded "unknown"
// code is only unknown until the seed grows it (9999 is the seeded Suspense).
func TestIntegration_SetAccountMapping_AnUnknownHHAccountRefusesInWords(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := exportSvc(t, pool, time.Date(2096, 5, 10, 12, 0, 0, 0, time.UTC))

	var missing string
	if err := pool.QueryRow(ctx,
		`SELECT gs.code FROM generate_series(8000, 8999) AS gs(code)
		  WHERE NOT EXISTS (SELECT 1 FROM gl_accounts a WHERE a.code::text = gs.code::text)
		  LIMIT 1`).Scan(&missing); err != nil {
		t.Fatalf("the seeded chart occupies every code from 8000 up; pick another range: %v", err)
	}

	_, err := svc.SetAccountMapping(ctx, TargetQuickBooksOnline, missing, "8880")
	if err == nil {
		t.Fatalf("an unknown account code took a mapping")
	}
	if !strings.Contains(err.Error(), "no HH account "+missing) {
		t.Fatalf("the refusal must name the account in words, got %q", err.Error())
	}
	var br badRequestError
	if !errors.As(err, &br) {
		t.Fatalf("the refusal must be the 400 shape, got %T %q", err, err.Error())
	}
	// And the refusal wrote no mapping row.
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gl_account_mapping WHERE account_code::text = $1`, missing).Scan(&n); err != nil {
		t.Fatalf("count the refused mapping: %v", err)
	}
	if n != 0 {
		t.Fatalf("the refused set wrote %d mapping rows, want none", n)
	}
}

// TestIntegration_Export_QBOFileTiesToTheGLPerAccount is the tie to the books: every package
// account's debit and credit totals in the file equal the GL's own for the period, read from the
// GL's detail view, and the pick bounds what the file carries (June's movement stays out of May's
// file).
func TestIntegration_Export_QBOFileTiesToTheGLPerAccount(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	ensurePeriods(t, pool, 2096)
	svc := exportSvc(t, pool, time.Date(2096, 5, 10, 12, 0, 0, 0, time.UTC))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM gl_account_mapping WHERE target = $1`, TargetQuickBooksOnline)
	})

	for _, m := range [][2]string{{"1000", "10100 Checking"}, {"4000", "40100 Sales"}, {"1300", "13000 Prepaid Expenses"}} {
		if _, err := svc.SetAccountMapping(ctx, TargetQuickBooksOnline, m[0], m[1]); err != nil {
			t.Fatalf("map %s: %v", m[0], err)
		}
	}
	seedExportJournal(t, pool, 2096, 5, "ax2-tie-a", "1000", "4000", 30000)
	seedExportJournal(t, pool, 2096, 5, "ax2-tie-b", "1000", "1300", 5000)
	seedExportJournal(t, pool, 2096, 6, "ax2-tie-june", "1000", "4000", 7000)

	res, err := svc.Export(ctx, ExportQuery{Year: 2096, Month: 5, Target: TargetQuickBooksOnline})
	if err != nil {
		t.Fatalf("the fully mapped month must export: %v", err)
	}
	recs := qboCSV(t, res.Content)

	// The GL's own per-account totals for the same period, read from the GL's detail view.
	glRows, err := pool.Query(ctx,
		`SELECT account_code::text, SUM(COALESCE(debit_cents,0)), SUM(COALESCE(credit_cents,0))
		   FROM v_gl_detail WHERE period_year = 2096 AND period_month = 5 GROUP BY account_code`)
	if err != nil {
		t.Fatalf("read the GL's period totals: %v", err)
	}
	defer glRows.Close()
	type twoSides struct{ debit, credit int64 }
	gl := map[string]twoSides{}
	packageFor := map[string]string{"1000": "10100 Checking", "4000": "40100 Sales", "1300": "13000 Prepaid Expenses"}
	for glRows.Next() {
		var code string
		var d twoSides
		if err := glRows.Scan(&code, &d.debit, &d.credit); err != nil {
			t.Fatalf("scan the GL's period totals: %v", err)
		}
		gl[code] = d
	}
	if len(gl) != 3 {
		t.Fatalf("the May fixture must move three accounts, the GL says %d (%v)", len(gl), gl)
	}

	// The file's per-package-account totals, summed off the rendered rows.
	file := map[string]twoSides{}
	for _, r := range recs[1:] {
		s := file[r[2]]
		s.debit += qboCellDollars(t, r[3])
		s.credit += qboCellDollars(t, r[4])
		file[r[2]] = s
	}
	for hhCode, pkg := range packageFor {
		want, ok := gl[hhCode]
		if !ok {
			t.Fatalf("the GL has no movement for %s, so the fixture is wrong", hhCode)
		}
		got, ok := file[pkg]
		if !ok {
			t.Fatalf("the file carries no rows for the mapped account %q (HH %s):\n%s", pkg, hhCode, res.Content)
		}
		if got.debit != want.debit || got.credit != want.credit {
			t.Fatalf("account %s (HH %s): the file says %d debit %d credit cents, the GL says %d and %d",
				pkg, hhCode, got.debit, got.credit, want.debit, want.credit)
		}
	}
	// June's journal is outside the pick: the cash account's file total must not carry it.
	if file["10100 Checking"].debit != 35000 {
		t.Fatalf("May's file carries %d debit cents of cash, want only May's 35000 (June's 7000 must stay out)",
			file["10100 Checking"].debit)
	}
}

// TestIntegration_Export_QBOQuarterAndWholeYearPicks is the pick on the QBO file: a quarter reads
// exactly its three house months' journals, and the whole year reads the year's, both recording
// their wider-than-a-month batch, as AX-1's picks do.
func TestIntegration_Export_QBOQuarterAndWholeYearPicks(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	ensurePeriods(t, pool, 2097)
	svc := exportSvc(t, pool, time.Date(2097, 4, 10, 12, 0, 0, 0, time.UTC))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM gl_account_mapping WHERE target = $1`, TargetQuickBooksOnline)
	})

	for _, m := range [][2]string{{"1000", "10100 Checking"}, {"4000", "40100 Sales"}} {
		if _, err := svc.SetAccountMapping(ctx, TargetQuickBooksOnline, m[0], m[1]); err != nil {
			t.Fatalf("map %s: %v", m[0], err)
		}
	}
	seedExportJournal(t, pool, 2097, 4, "ax2-q2-apr", "1000", "4000", 11100)
	seedExportJournal(t, pool, 2097, 6, "ax2-q2-jun", "1000", "4000", 22200)
	seedExportJournal(t, pool, 2097, 8, "ax2-q2-aug", "1000", "4000", 33300) // outside Q2

	q2, err := svc.Export(ctx, ExportQuery{Year: 2097, Quarter: 2, Target: TargetQuickBooksOnline})
	if err != nil {
		t.Fatalf("the quarter export must run: %v", err)
	}
	recs := qboCSV(t, q2.Content)
	if len(recs) != 5 {
		t.Fatalf("Q2's two journals export as two entries of two rows, got %d lines:\n%s", len(recs), q2.Content)
	}
	days := map[string]bool{}
	for _, r := range recs[1:] {
		days[r[1]] = true
		if r[1] == "2097-08-10" {
			t.Fatalf("August's journal is outside Q2 but rode the quarter's file:\n%s", q2.Content)
		}
	}
	if !days["2097-04-10"] || !days["2097-06-10"] {
		t.Fatalf("Q2's file must carry April's and June's house days, got %v:\n%s", days, q2.Content)
	}

	year, err := svc.Export(ctx, ExportQuery{Year: 2097, Month: 0, Target: TargetQuickBooksOnline})
	if err != nil {
		t.Fatalf("the whole year export must run: %v", err)
	}
	yrecs := qboCSV(t, year.Content)
	if len(yrecs) != 7 {
		t.Fatalf("the year's three journals export as three entries of two rows, got %d lines:\n%s", len(yrecs), year.Content)
	}
	sawAug := false
	for _, r := range yrecs[1:] {
		if r[1] == "2097-08-10" {
			sawAug = true
		}
	}
	if !sawAug {
		t.Fatalf("the whole year pick must carry August's journal too:\n%s", year.Content)
	}

	// Both wider-than-a-month picks record their batch the way AX-1's do.
	for name, res := range map[string]ExportResult{"quarter": q2, "year": year} {
		var month int
		if err := pool.QueryRow(ctx, `SELECT month FROM gl_export_batches WHERE id = $1::uuid`, res.BatchID).Scan(&month); err != nil {
			t.Fatalf("read the %s pick's batch: %v", name, err)
		}
		if month != 0 {
			t.Fatalf("the %s pick's batch records month %d, want the wider-than-a-month word 0", name, month)
		}
	}
}

// TestIntegration_Export_TheHouseDayBoundsThePeriod is the house day's leg: a journal posted at
// 01:00 UTC on July the first is 21:00 on June the thirtieth in the dealer's zone, so it files
// into June's books, June's export carries it, July's does not, and the file dates its entry June's
// house day, never the UTC day.
func TestIntegration_Export_TheHouseDayBoundsThePeriod(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	ensurePeriods(t, pool, 2096)
	svc := exportSvc(t, pool, time.Date(2096, 7, 1, 2, 0, 0, 0, time.UTC))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM gl_account_mapping WHERE target = $1`, TargetQuickBooksOnline)
	})

	for _, m := range [][2]string{{"1000", "10100 Checking"}, {"4000", "40100 Sales"}} {
		if _, err := svc.SetAccountMapping(ctx, TargetQuickBooksOnline, m[0], m[1]); err != nil {
			t.Fatalf("map %s: %v", m[0], err)
		}
	}
	seedQBOJournalAt(t, pool, time.Date(2096, 7, 1, 1, 0, 0, 0, time.UTC), "ax2-houseday", "1000", "4000", 61000)

	june, err := svc.Export(ctx, ExportQuery{Year: 2096, Month: 6, Target: TargetQuickBooksOnline})
	if err != nil {
		t.Fatalf("June's export must carry the boundary journal: %v", err)
	}
	jrecs := qboCSV(t, june.Content)
	dated := 0
	for _, r := range jrecs[1:] {
		if r[1] == "2096-06-30" {
			dated++
		}
		if r[1] == "2096-07-01" {
			t.Fatalf("the entry posted 21:00 June 30 house time is dated the UTC day %q:\n%s", r[1], june.Content)
		}
	}
	if dated != 2 {
		t.Fatalf("the boundary journal's two rows must be dated its house day 2096-06-30, %d were:\n%s", dated, june.Content)
	}

	july, err := svc.Export(ctx, ExportQuery{Year: 2096, Month: 7, Target: TargetQuickBooksOnline})
	if err != nil {
		t.Fatalf("July's export must run: %v", err)
	}
	for _, r := range qboCSV(t, july.Content)[1:] {
		if r[1] == "2096-06-30" {
			t.Fatalf("the journal filed into June rode July's file:\n%s", july.Content)
		}
	}
}
