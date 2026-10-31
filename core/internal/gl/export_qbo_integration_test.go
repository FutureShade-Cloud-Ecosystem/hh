//go:build integration

// export_qbo_integration_test.go, W3 AX-2 item 1's database half: the QuickBooks Online journal
// entry import file on the real core, one journal entry per GL journal, its accounts mapped
// through AX-1's seam, its money the integer cents of the books rendered as dollars with two
// decimals, and its two sides tied per entry and in total.

package gl

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestIntegration_Export_QBOJournalEntryImportFile is item 1 on the real core: a fully mapped month
// exports as QBO's journal entry import CSV, one entry per GL journal, dated the house day, under
// the package's account codes, balanced per entry and in total.
func TestIntegration_Export_QBOJournalEntryImportFile(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	ensurePeriods(t, pool, 2096)
	svc := exportSvc(t, pool, time.Date(2096, 5, 10, 12, 0, 0, 0, time.UTC))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM gl_account_mapping WHERE target = $1`, TargetQuickBooksOnline)
	})

	for _, m := range [][2]string{{"1000", "10100 Checking"}, {"4000", "40100 Sales"}} {
		if _, err := svc.SetAccountMapping(ctx, TargetQuickBooksOnline, m[0], m[1]); err != nil {
			t.Fatalf("map %s: %v", m[0], err)
		}
	}
	seedExportJournal(t, pool, 2096, 5, "ax2-qbo-a", "1000", "4000", 250000)
	seedExportJournal(t, pool, 2096, 5, "ax2-qbo-b", "1000", "4000", 12599)

	res, err := svc.Export(ctx, ExportQuery{Year: 2096, Month: 5, Target: TargetQuickBooksOnline})
	if err != nil {
		t.Fatalf("the fully mapped month must export: %v", err)
	}
	recs := qboCSV(t, res.Content)
	if len(recs) != 5 {
		t.Fatalf("the QBO file carries %d lines, want the header and the month's four rows:\n%s", len(recs), res.Content)
	}
	// One journal entry per GL journal: two GL journals posted, two JournalNos in the file, two
	// rows each, every row of an entry dated the house day its journal posted (day 10, 12:00 UTC
	// is 08:00 on the same Toronto day).
	seen := map[string]int{}
	for _, r := range recs[1:] {
		seen[r[0]]++
		if r[1] != "2096-05-10" {
			t.Fatalf("entry %s is dated %q, want the posted house day 2096-05-10", r[0], r[1])
		}
		if r[2] != "10100 Checking" && r[2] != "40100 Sales" {
			t.Fatalf("the Account cell %q must be one of the mapped package accounts", r[2])
		}
	}
	if len(seen) != 2 {
		t.Fatalf("two GL journals must export as two QBO journal entries, saw %d entries %v", len(seen), seen)
	}
	for no, n := range seen {
		if n != 2 {
			t.Fatalf("journal %s carries %d rows, want its two legs", no, n)
		}
	}
	// The two sides tie per entry and in total.
	var dAll, cAll int64
	for no := range seen {
		d, c := qboSum(t, recs, no)
		if d != c {
			t.Fatalf("journal %s does not balance in the file: %d vs %d cents", no, d, c)
		}
		dAll += d
		cAll += c
	}
	if dAll != 262599 || cAll != 262599 {
		t.Fatalf("the file totals %d debit and %d credit cents, want its balanced 262599", dAll, cAll)
	}
	// The batch is the QBO one.
	if res.Format != TargetQuickBooksOnline || res.BatchID == "" || !res.IsDraft {
		t.Fatalf("the QBO export must record its own batch, got %+v", res)
	}
	var format string
	if err := pool.QueryRow(ctx, `SELECT format FROM gl_export_batches WHERE id = $1::uuid`, res.BatchID).Scan(&format); err != nil {
		t.Fatalf("read the recorded batch: %v", err)
	}
	if format != TargetQuickBooksOnline {
		t.Fatalf("the recorded batch's format is %q, want %q", format, TargetQuickBooksOnline)
	}
}

// TestIntegration_Export_QBOUnmappedRefusalWritesNothing is the refusal's other half: an unmapped
// account with movement refuses the export in words AND leaves no batch behind, so a refused
// period never reads as an exported one anywhere in the books.
func TestIntegration_Export_QBOUnmappedRefusalWritesNothing(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	ensurePeriods(t, pool, 2096)
	svc := exportSvc(t, pool, time.Date(2096, 7, 10, 12, 0, 0, 0, time.UTC))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM gl_account_mapping WHERE target = $1`, TargetQuickBooksOnline)
	})

	if _, err := svc.SetAccountMapping(ctx, TargetQuickBooksOnline, "1000", "10100 Checking"); err != nil {
		t.Fatalf("map the cash account: %v", err)
	}
	seedExportJournal(t, pool, 2096, 7, "ax2-qbo-unmapped", "1000", "2300", 50000)

	var before int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gl_export_batches WHERE format = $1 AND fiscal_year = 2096`, TargetQuickBooksOnline).Scan(&before); err != nil {
		t.Fatalf("count the QBO batches: %v", err)
	}
	_, err := svc.Export(ctx, ExportQuery{Year: 2096, Month: 7, Target: TargetQuickBooksOnline})
	if err == nil {
		t.Fatal("an unmapped account with movement exported anyway")
	}
	for _, word := range []string{"no mapping", "QuickBooks Online", "2300"} {
		if !strings.Contains(err.Error(), word) {
			t.Fatalf("the refusal must carry %q, got %q", word, err.Error())
		}
	}
	var after int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gl_export_batches WHERE format = $1 AND fiscal_year = 2096`, TargetQuickBooksOnline).Scan(&after); err != nil {
		t.Fatalf("count the QBO batches after the refusal: %v", err)
	}
	if after != before {
		t.Fatalf("a refused export wrote %d batches, want none", after-before)
	}
}
