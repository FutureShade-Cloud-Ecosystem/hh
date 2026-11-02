//go:build integration

// export_sage50_integration_test.go, W3 AX-3 item 3: the tie on the real core. The rendered file
// is parsed the way Sage 50's importer reads it and every package account's debit and credit
// totals are checked against the GL's own for the period, with another month's amounts proven to
// stay out; the quarter and whole year picks work as the QBO body's do. The figures are proven
// through the real write doors (W3-COMMON's split figure rule): every journal seeds through
// PostExternal, now and at a past date.

package gl

import (
	"context"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// sageFileDebitsCredits sums the file's amounts per package account the way the importer reads
// them: a positive amount is a debit, a negative one a credit.
func sageFileDebitsCredits(t *testing.T, txs []sageTx) map[string][2]int64 {
	t.Helper()
	file := map[string][2]int64{}
	for _, tx := range txs {
		for _, line := range tx.Lines {
			amt := sageAmountCents(t, line[1])
			s := file[line[0]]
			if amt >= 0 {
				s[0] += amt
			} else {
				s[1] += -amt
			}
			file[line[0]] = s
		}
	}
	return file
}

// sageAssertTiesToGL checks the rendered file against the GL's own per account figures for the
// period: every mapped package account's debit and credit totals in the file equal the GL's, the
// file carries no account outside the mapping, and every transaction is dated inside the period.
func sageAssertTiesToGL(t *testing.T, pool *pgxpool.Pool, content string, year, month int, packageFor map[string]string) {
	t.Helper()
	txs := sageFile(t, content)
	sageAssertInPeriod(t, txs, year, month)
	file := sageFileDebitsCredits(t, txs)

	glRows, err := pool.Query(context.Background(),
		`SELECT account_code::text, SUM(COALESCE(debit_cents,0)), SUM(COALESCE(credit_cents,0))
		   FROM v_gl_detail WHERE period_year = $1 AND period_month = $2 GROUP BY account_code`, year, month)
	if err != nil {
		t.Fatalf("read the GL's period totals: %v", err)
	}
	defer glRows.Close()
	gl := map[string][2]int64{}
	for glRows.Next() {
		var code string
		var d [2]int64
		if err := glRows.Scan(&code, &d[0], &d[1]); err != nil {
			t.Fatalf("scan the GL's period totals: %v", err)
		}
		gl[code] = d
	}
	if err := glRows.Err(); err != nil {
		t.Fatalf("read the GL's period totals: %v", err)
	}
	for acct := range file {
		known := false
		for _, pkg := range packageFor {
			if pkg == acct {
				known = true
				break
			}
		}
		if !known {
			t.Fatalf("the file carries account %q outside this test's mapping:\n%s", acct, content)
		}
	}
	for hhCode, pkg := range packageFor {
		want, ok := gl[hhCode]
		if !ok {
			t.Fatalf("the GL has no movement for %s, so the fixture is wrong", hhCode)
		}
		got, ok := file[pkg]
		if !ok {
			t.Fatalf("the file carries no rows for the mapped account %q (HH %s):\n%s", pkg, hhCode, content)
		}
		if got != want {
			t.Fatalf("account %s (HH %s): the file says %d debit and %d credit cents, the GL says %d and %d",
				pkg, hhCode, got[0], got[1], want[0], want[1])
		}
	}
}

// sageLegDrops collects the rows one Sage leg creates, so its cleanup deletes exactly its own, in
// dependency order, and fails the test loudly naming the table when a delete fails (W3-COMMON's
// cleanup rule, the AX-3 read's P3-3): never `_, _ = pool.Exec(...)`. The legs' years are past
// years a later package's as of now read would otherwise carry, and the export batches the legs
// record are rows nothing else deletes.
type sageLegDrops struct {
	journalIDs []string
	batchIDs   []string
}

func (d *sageLegDrops) journal(id string) {
	if id != "" {
		d.journalIDs = append(d.journalIDs, id)
	}
}

func (d *sageLegDrops) batch(id string) {
	if id != "" {
		d.batchIDs = append(d.batchIDs, id)
	}
}

// cleanup deletes the leg's export batches, then its journals' lines and the journals (lines
// before headers), then its mappings. Every delete fails the test naming its table.
func (d *sageLegDrops) cleanup(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, id := range d.batchIDs {
		if _, err := pool.Exec(ctx, `DELETE FROM gl_export_batches WHERE id = $1::uuid`, id); err != nil {
			t.Fatalf("cleanup gl_export_batches (%s): %v", id, err)
		}
	}
	for _, id := range d.journalIDs {
		if _, err := pool.Exec(ctx, `DELETE FROM gl_journal_lines WHERE journal_id = $1::uuid`, id); err != nil {
			t.Fatalf("cleanup gl_journal_lines (%s): %v", id, err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM gl_journals WHERE id = $1::uuid`, id); err != nil {
			t.Fatalf("cleanup gl_journals (%s): %v", id, err)
		}
	}
	if _, err := pool.Exec(ctx, `DELETE FROM gl_account_mapping WHERE target = $1`, TargetSage50); err != nil {
		t.Fatalf("cleanup gl_account_mapping: %v", err)
	}
}

// TestIntegration_Export_Sage50FileTiesToTheGLPerAccount is the tie to the books: May's file
// equals the GL for May per account, June's journal stays out of it, and the same tie holds at a
// past date (May 2022) through the same real write door.
func TestIntegration_Export_Sage50FileTiesToTheGLPerAccount(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	ensurePeriods(t, pool, 2054)
	ensurePeriods(t, pool, 2022)
	svc := exportSvc(t, pool, time.Date(2054, 5, 10, 12, 0, 0, 0, time.UTC))
	drops := &sageLegDrops{}
	t.Cleanup(func() { drops.cleanup(t, pool) })

	packageFor := map[string]string{"1000": "1010", "4000": "4010", "1300": "1310"}
	for hh, pkg := range packageFor {
		if _, err := svc.SetAccountMapping(ctx, TargetSage50, hh, pkg); err != nil {
			t.Fatalf("map %s: %v", hh, err)
		}
	}
	drops.journal(seedExportJournal(t, pool, 2054, 5, "ax3-tie-a", "1000", "4000", 30000))
	drops.journal(seedExportJournal(t, pool, 2054, 5, "ax3-tie-b", "1000", "1300", 5000))
	drops.journal(seedExportJournal(t, pool, 2054, 6, "ax3-tie-june", "1000", "4000", 7000))

	res, err := svc.Export(ctx, ExportQuery{Year: 2054, Month: 5, Target: TargetSage50})
	if err != nil {
		t.Fatalf("the fully mapped month must export: %v", err)
	}
	drops.batch(res.BatchID)
	sageAssertTiesToGL(t, pool, res.Content, 2054, 5, packageFor)
	// June's journal is outside the pick: the cash account's file total must not carry it.
	file := sageFileDebitsCredits(t, sageFile(t, res.Content))
	if file["1010"][0] != 35000 {
		t.Fatalf("May's file carries %d debit cents of cash, want only May's 35000 (June's 7000 must stay out)",
			file["1010"][0])
	}
	// The batch is the Sage 50 one.
	var format string
	if err := pool.QueryRow(ctx, `SELECT format FROM gl_export_batches WHERE id = $1::uuid`, res.BatchID).Scan(&format); err != nil {
		t.Fatalf("read the recorded batch: %v", err)
	}
	if format != TargetSage50 || res.Format != TargetSage50 || !res.IsDraft {
		t.Fatalf("the Sage 50 export must record its own batch, got format %q res %+v", format, res)
	}

	// The same tie at a past date, through the same real write door.
	drops.journal(seedExportJournal(t, pool, 2022, 5, "ax3-tie-past", "1000", "4000", 12000))
	past, err := svc.Export(ctx, ExportQuery{Year: 2022, Month: 5, Target: TargetSage50})
	if err != nil {
		t.Fatalf("the past month must export: %v", err)
	}
	drops.batch(past.BatchID)
	sageAssertTiesToGL(t, pool, past.Content, 2022, 5, map[string]string{"1000": "1010", "4000": "4010"})
}

// TestIntegration_Export_Sage50QuarterAndWholeYearPicks is the pick on the Sage 50 file: a
// quarter reads exactly its three house months' journals, and the whole year reads the year's,
// both recording their wider-than-a-month batch, as the QBO body's picks do.
func TestIntegration_Export_Sage50QuarterAndWholeYearPicks(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	ensurePeriods(t, pool, 2055)
	svc := exportSvc(t, pool, time.Date(2055, 4, 10, 12, 0, 0, 0, time.UTC))
	drops := &sageLegDrops{}
	t.Cleanup(func() { drops.cleanup(t, pool) })

	for _, m := range [][2]string{{"1000", "1010"}, {"4000", "4010"}} {
		if _, err := svc.SetAccountMapping(ctx, TargetSage50, m[0], m[1]); err != nil {
			t.Fatalf("map %s: %v", m[0], err)
		}
	}
	drops.journal(seedExportJournal(t, pool, 2055, 4, "ax3-q2-apr", "1000", "4000", 11100))
	drops.journal(seedExportJournal(t, pool, 2055, 6, "ax3-q2-jun", "1000", "4000", 22200))
	drops.journal(seedExportJournal(t, pool, 2055, 8, "ax3-q2-aug", "1000", "4000", 33300)) // outside Q2

	q2, err := svc.Export(ctx, ExportQuery{Year: 2055, Quarter: 2, Target: TargetSage50})
	if err != nil {
		t.Fatalf("the quarter export must run: %v", err)
	}
	drops.batch(q2.BatchID)
	qtxs := sageFile(t, q2.Content)
	if len(qtxs) != 2 {
		t.Fatalf("Q2's two journals export as two transactions, got %d:\n%s", len(qtxs), q2.Content)
	}
	sawApril, sawJune := false, false
	for _, tx := range qtxs {
		d := sageDay(t, tx.Date)
		if d.Month() == time.August {
			t.Fatalf("August's journal is outside Q2 but rode the quarter's file:\n%s", q2.Content)
		}
		sawApril = sawApril || d.Month() == time.April
		sawJune = sawJune || d.Month() == time.June
	}
	if !sawApril || !sawJune {
		t.Fatalf("Q2's file must carry April's and June's journals:\n%s", q2.Content)
	}

	year, err := svc.Export(ctx, ExportQuery{Year: 2055, Month: 0, Target: TargetSage50})
	if err != nil {
		t.Fatalf("the whole year export must run: %v", err)
	}
	drops.batch(year.BatchID)
	ytxs := sageFile(t, year.Content)
	if len(ytxs) != 3 {
		t.Fatalf("the year's three journals export as three transactions, got %d:\n%s", len(ytxs), year.Content)
	}
	sawAug := false
	for _, tx := range ytxs {
		sawAug = sawAug || sageDay(t, tx.Date).Month() == time.August
	}
	if !sawAug {
		t.Fatalf("the whole year pick must carry August's journal too:\n%s", year.Content)
	}

	// Both wider-than-a-month picks record their batch the way the QBO body's do.
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

// TestIntegration_Export_Sage50CommentIsTheJournalsMemoAndLinesKeepTheirOwn is the proof that the
// Sage body reads the JOURNAL's memo through the real query (the AX-3 delta read's P3-a): one
// journal posted through the real door with its own memo and lines whose memos differ. The
// transaction's comment is the journal's memo; each detail line carries its own line memo.
func TestIntegration_Export_Sage50CommentIsTheJournalsMemoAndLinesKeepTheirOwn(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	ensurePeriods(t, pool, 2053)
	svc := exportSvc(t, pool, time.Date(2053, 5, 10, 12, 0, 0, 0, time.UTC))
	drops := &sageLegDrops{}
	t.Cleanup(func() { drops.cleanup(t, pool) })
	for _, m := range [][2]string{{"1000", "1010"}, {"4000", "4010"}} {
		if _, err := svc.SetAccountMapping(ctx, TargetSage50, m[0], m[1]); err != nil {
			t.Fatalf("map %s: %v", m[0], err)
		}
	}

	poster := svcAt(pool, identity.StaffContext{UserID: aUser(t, pool), RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal},
		time.Date(2053, 5, 10, 12, 0, 0, 0, time.UTC))
	id, err := poster.PostExternal(ctx, ExternalJournal{
		Source: SourceAdjustment, IdempotencyKey: "ax3-fix2-memo", Memo: "THE JOURNAL MEMO",
		Lines: []ExternalLine{
			{AccountCode: "1000", DebitCents: 4100, Memo: "line one"},
			{AccountCode: "4000", CreditCents: 4100, Memo: "line two"},
		},
	})
	if err != nil {
		t.Fatalf("post the journal: %v", err)
	}
	drops.journal(id)

	res, err := svc.Export(ctx, ExportQuery{Year: 2053, Month: 5, Target: TargetSage50})
	if err != nil {
		t.Fatalf("the month must export: %v", err)
	}
	drops.batch(res.BatchID)
	txs := sageFile(t, res.Content)
	if len(txs) != 1 || len(txs[0].Lines) != 2 {
		t.Fatalf("the one journal exports as one transaction of two lines, got %+v:\n%s", txs, res.Content)
	}
	if txs[0].Comment != "THE JOURNAL MEMO" {
		t.Fatalf("the transaction comment is %q, want the JOURNAL's memo, not a line's:\n%s", txs[0].Comment, res.Content)
	}
	got := map[string]string{}
	for _, line := range txs[0].Lines {
		got[line[0]] = line[2]
	}
	if got["1010"] != "line one" || got["4010"] != "line two" {
		t.Fatalf("each detail line must carry its own memo (1010 line one, 4010 line two), got %v:\n%s", got, res.Content)
	}
}
