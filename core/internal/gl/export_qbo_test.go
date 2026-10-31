package gl

// export_qbo_test.go, W3 AX-2 item 1's unit half: the QuickBooks Online journal entry import file's
// shape and its own refusals, against the fake store, no database. The database halves live in
// export_qbo_integration_test.go.

import (
	"context"
	"encoding/csv"
	"math"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// qboCSV parses the QBO file the way its importer does: one record per line, ten columns, the
// header first (QBO's own column vocabulary, verbatim and in its order). A file the importer
// cannot parse is a file that cannot be imported, so the parse itself is part of every leg.
func qboCSV(t *testing.T, content string) [][]string {
	t.Helper()
	const header = "JournalNo,JournalDate,Account,Debits,Credits,Description,Name,Location,Class,Currency"
	recs, err := csv.NewReader(strings.NewReader(content)).ReadAll()
	if err != nil {
		t.Fatalf("the QBO file must parse as CSV: %v\n%s", err, content)
	}
	if len(recs) < 1 {
		t.Fatalf("the QBO file must carry its header line\n%s", content)
	}
	if got := strings.Join(recs[0], ","); got != header {
		t.Fatalf("the QBO header must be %q, got %q\n%s", header, got, content)
	}
	for i, r := range recs[1:] {
		if len(r) != 10 {
			t.Fatalf("QBO row %d carries %d columns, want its ten: %q", i+2, len(r), r)
		}
	}
	return recs
}

// qboCellDollars reads one money cell back into integer cents, the exact inverse of the renderer's
// dollars: two decimals, empty is the other side's zero. Money never travels as a float, not even
// in a test's arithmetic.
func qboCellDollars(t *testing.T, cell string) int64 {
	t.Helper()
	cell = strings.TrimSpace(cell)
	if cell == "" {
		return 0
	}
	neg := false
	if strings.HasPrefix(cell, "-") {
		neg = true
		cell = cell[1:]
	}
	parts := strings.Split(cell, ".")
	if len(parts) != 2 || len(parts[1]) != 2 {
		t.Fatalf("the money cell %q must be dollars with two decimals", cell)
	}
	var cents int64
	for _, ch := range parts[0] + parts[1] {
		if ch < '0' || ch > '9' {
			t.Fatalf("the money cell %q must be digits", cell)
		}
		cents = cents*10 + int64(ch-'0')
	}
	if neg {
		return -cents
	}
	return cents
}

func TestExport_QBORendersItsJournalEntryImportFormat(t *testing.T) {
	f := newFakeStore()
	f.exportRows = []ExportRow{
		{EntryNo: 41, PostedAt: "2096-05-10", AccountCode: "1000", AccountName: "Cash Bank", DebitCents: 250000, Memo: "the cash leg"},
		{EntryNo: 41, PostedAt: "2096-05-10", AccountCode: "4000", AccountName: "Sales", CreditCents: 250000, Memo: "the sales leg"},
		{EntryNo: 42, PostedAt: "2096-05-17", AccountCode: "1000", AccountName: "Cash Bank", DebitCents: 12599, Memo: "the second cash leg"},
		{EntryNo: 42, PostedAt: "2096-05-17", AccountCode: "4000", AccountName: "Sales", CreditCents: 12599, Memo: "the second sales leg"},
	}
	f.mappings = []AccountMapping{
		{Target: TargetQuickBooksOnline, AccountCode: "1000", AccountName: "Cash Bank", PackageAccount: "10100 Checking"},
		{Target: TargetQuickBooksOnline, AccountCode: "4000", AccountName: "Sales", PackageAccount: "40100 Sales"},
	}
	svc := newTestServiceAs(f, identity.StaffContext{UserID: "u-ctrl", RoleCode: controllerRole, Tier: 1})

	res, err := svc.Export(context.Background(), ExportQuery{Year: 2096, Month: 5, Target: TargetQuickBooksOnline})
	if err != nil {
		t.Fatalf("a fully mapped period must export: %v", err)
	}
	if res.Format != TargetQuickBooksOnline || !res.IsDraft || res.BatchID == "" {
		t.Fatalf("the QBO export must record its own batch, got %+v", res)
	}
	if res.RowCount != 4 {
		t.Fatalf("the QBO export reads %d rows, want the period's four", res.RowCount)
	}

	recs := qboCSV(t, res.Content)
	if len(recs) != 5 {
		t.Fatalf("the QBO file carries %d lines, want the header and the period's four rows:\n%s", len(recs), res.Content)
	}
	// One journal entry per GL journal: the entry's lines share its JournalNo and its house day,
	// and nothing but the entry's own lines carries them.
	row41a, row41b, row42a, row42b := recs[1], recs[2], recs[3], recs[4]
	for i, row := range [][]string{row41a, row41b, row42a, row42b} {
		want := "41"
		if i >= 2 {
			want = "42"
		}
		if row[0] != want {
			t.Fatalf("row %d names journal %q, want the GL journal %q", i+2, row[0], want)
		}
	}
	if row41a[1] != "2096-05-10" || row41b[1] != "2096-05-10" {
		t.Fatalf("entry 41's rows must carry its house day 2096-05-10, got %q and %q", row41a[1], row41b[1])
	}
	if row42a[1] != "2096-05-17" || row42b[1] != "2096-05-17" {
		t.Fatalf("entry 42's rows must carry its house day 2096-05-17, got %q and %q", row42a[1], row42b[1])
	}
	// Every account comes through the mapping: the Account column speaks the package's codes, in
	// the package's own words.
	if row41a[2] != "10100 Checking" || row41b[2] != "40100 Sales" {
		t.Fatalf("the Account cells must be the mapped package accounts, got %q and %q", row41a[2], row41b[2])
	}
	// Money is the integer cents of the books rendered as dollars with two decimals, one side per
	// row, the other side empty the way the import takes it.
	if row41a[3] != "2500.00" || row41a[4] != "" {
		t.Fatalf("the debit row renders %q and %q, want 2500.00 and the empty credit side", row41a[3], row41a[4])
	}
	if row41b[3] != "" || row41b[4] != "2500.00" {
		t.Fatalf("the credit row renders %q and %q, want the empty debit side and 2500.00", row41b[3], row41b[4])
	}
	if row42a[3] != "125.99" {
		t.Fatalf("the second entry's debit renders %q, want 125.99", row42a[3])
	}
	// The line's memo is the row's Description.
	if row41a[5] != "the cash leg" {
		t.Fatalf("the Description cell carries %q, want the line's memo", row41a[5])
	}
	// The tie, per entry and in total, is the file's own shape: every leg re-reads it.
	if d, c := qboSum(t, recs, "41"); d != 250000 || c != 250000 {
		t.Fatalf("entry 41 reads %d debit and %d credit cents, want its balanced 250000", d, c)
	}
	var dAll, cAll int64
	for _, no := range []string{"41", "42"} {
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
}

// qboSum sums one journal's two sides off the parsed file.
func qboSum(t *testing.T, recs [][]string, journalNo string) (debit, credit int64) {
	t.Helper()
	for _, r := range recs[1:] {
		if r[0] != journalNo {
			continue
		}
		debit += qboCellDollars(t, r[3])
		credit += qboCellDollars(t, r[4])
	}
	return debit, credit
}

func TestExport_QBORefusesAnUnbalancedEntryInWords(t *testing.T) {
	f := newFakeStore()
	// Entry 41 as a partial read would hand it: one cash leg and a sales leg that does not match.
	// The GL cannot post this, so meeting it here means the read and the books disagree, and the
	// file must refuse rather than ship an unbalanced entry into another package's books.
	f.exportRows = []ExportRow{
		{EntryNo: 41, PostedAt: "2096-05-10", AccountCode: "1000", AccountName: "Cash Bank", DebitCents: 250000, Memo: "the cash leg"},
		{EntryNo: 41, PostedAt: "2096-05-10", AccountCode: "4000", AccountName: "Sales", CreditCents: 240000, Memo: "the sales leg"},
	}
	f.mappings = []AccountMapping{
		{Target: TargetQuickBooksOnline, AccountCode: "1000", PackageAccount: "10100 Checking"},
		{Target: TargetQuickBooksOnline, AccountCode: "4000", PackageAccount: "40100 Sales"},
	}
	svc := newTestServiceAs(f, identity.StaffContext{UserID: "u-ctrl", RoleCode: controllerRole, Tier: 1})

	_, err := svc.Export(context.Background(), ExportQuery{Year: 2096, Month: 5, Target: TargetQuickBooksOnline})
	if err == nil {
		t.Fatal("an unbalanced entry exported anyway")
	}
	for _, word := range []string{"QuickBooks Online", "41", "does not balance", "2500.00", "2400.00"} {
		if !strings.Contains(err.Error(), word) {
			t.Fatalf("the refusal must carry %q, got %q", word, err.Error())
		}
	}
	if f.batchInserts != 0 {
		t.Fatalf("a refused export must write nothing, but %d batches were recorded", f.batchInserts)
	}
}

func TestExport_QBORefusesAnAccountWithNoMappingInWords(t *testing.T) {
	f := newFakeStore()
	f.exportRows = []ExportRow{
		{EntryNo: 41, PostedAt: "2096-05-10", AccountCode: "1000", AccountName: "Cash Bank", DebitCents: 250000, Memo: "the cash leg"},
		{EntryNo: 41, PostedAt: "2096-05-10", AccountCode: "1300", AccountName: "Prepaid Expenses", CreditCents: 250000, Memo: "the prepaid leg"},
	}
	// The unmapped read answered nothing (the seam's own refusal is AX-1's leg); the render itself
	// still refuses an account whose mapping it does not hold, so the two reads cannot drift apart
	// and hand the file a hole.
	f.mappings = []AccountMapping{
		{Target: TargetQuickBooksOnline, AccountCode: "1000", PackageAccount: "10100 Checking"},
	}
	svc := newTestServiceAs(f, identity.StaffContext{UserID: "u-ctrl", RoleCode: controllerRole, Tier: 1})

	_, err := svc.Export(context.Background(), ExportQuery{Year: 2096, Month: 5, Target: TargetQuickBooksOnline})
	if err == nil {
		t.Fatal("an account with no mapping exported anyway")
	}
	for _, word := range []string{"no mapping", "QuickBooks Online", "1300 Prepaid Expenses"} {
		if !strings.Contains(err.Error(), word) {
			t.Fatalf("the refusal must carry %q, got %q", word, err.Error())
		}
	}
	if f.batchInserts != 0 {
		t.Fatalf("a refused export must write nothing, but %d batches were recorded", f.batchInserts)
	}
}

// dollarsExact on the smallest int64: negating it overflows back to itself, so a recursive
// implementation never ends. It renders the true value instead.
func TestDollarsExact_TheSmallestInt64Renders(t *testing.T) {
	if got := dollarsExact(math.MinInt64); got != "-92233720368547758.08" {
		t.Fatalf("dollarsExact(MinInt64) = %q, want -92233720368547758.08", got)
	}
	if got := dollarsExact(-5); got != "-0.05" || dollarsExact(0) != "0.00" || dollarsExact(12345) != "123.45" {
		t.Fatalf("dollarsExact must keep its ordinary words, got %q %q %q", dollarsExact(-5), dollarsExact(0), dollarsExact(12345))
	}
}
