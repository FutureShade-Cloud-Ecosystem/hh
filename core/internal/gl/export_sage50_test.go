package gl

// export_sage50_test.go, W3 AX-3 item 1's unit half: the Sage 50 general journal import file's
// shape and its own refusals, against the fake store, no database. Every leg parses the rendered
// file the way Sage 50's importer reads it (comma separated, text fields in straight double
// quotes, a transaction's first line of three fields and each detail line of four), so a leg can
// never pass on a file the importer would refuse. The database halves live in
// export_sage50_integration_test.go and export_sage50_dates_integration_test.go.

import (
	"context"
	"encoding/csv"
	"math"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// sageTx is one Sage 50 general journal transaction as its importer reads it: the first line's
// three fields (date, source, comment), then the transaction's detail lines of four fields each
// (account, amount, comment, project).
type sageTx struct {
	Date, Source, Comment string
	Lines                 [][]string
}

// sageFile parses a rendered Sage 50 file the way its importer does and groups the records into
// transactions. The parse itself is part of every leg: a file the importer cannot read is a file
// that cannot be imported. Sage 50 is a Windows importer, so the legs hold the file to CRLF.
func sageFile(t *testing.T, content string) []sageTx {
	t.Helper()
	if strings.Contains(strings.ReplaceAll(content, "\r\n", ""), "\n") {
		t.Fatalf("every line ends CRLF (a Windows importer), but a bare LF rides in the file:\n%q", content)
	}
	if content != "" && !strings.HasSuffix(content, "\r\n") {
		t.Fatalf("the file must end with CRLF:\n%q", content)
	}
	reader := csv.NewReader(strings.NewReader(content))
	reader.FieldsPerRecord = -1 // the transaction's first line carries 3 fields, a detail line 4
	recs, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("the Sage 50 file must parse as comma separated records with quoted text fields: %v\n%s", err, content)
	}
	var txs []sageTx
	for _, rec := range recs {
		switch len(rec) {
		case 3:
			txs = append(txs, sageTx{Date: rec[0], Source: rec[1], Comment: rec[2]})
		case 4:
			if len(txs) == 0 {
				t.Fatalf("a detail line opens the file; the importer reads a transaction's first line there: %q", rec)
			}
			txs[len(txs)-1].Lines = append(txs[len(txs)-1].Lines, rec)
		default:
			t.Fatalf("a general journal line carries 3 fields (the transaction's first line) or 4 (a detail line), got %d: %q", len(rec), rec)
		}
	}
	return txs
}

// sageDay reads one mm-dd-yy cell back into a day, the date shape Sage 50's importer takes.
func sageDay(t *testing.T, cell string) time.Time {
	t.Helper()
	d, err := time.Parse("01-02-06", cell)
	if err != nil {
		t.Fatalf("the transaction date %q must be Sage 50's mm-dd-yy", cell)
	}
	return d
}

// sageAssertInPeriod asserts every transaction of the file is dated inside the period. The
// importer books by the date cell, so a date outside the period books the entry into a month the
// GL already filed elsewhere.
func sageAssertInPeriod(t *testing.T, txs []sageTx, year, month int) {
	t.Helper()
	first := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	last := first.AddDate(0, 1, -1)
	for _, tx := range txs {
		d := sageDay(t, tx.Date)
		if d.Before(first) || d.After(last) {
			t.Fatalf("the transaction dated %s falls outside the file's period %s..%s",
				tx.Date, first.Format("2006-01-02"), last.Format("2006-01-02"))
		}
	}
}

// sageAmountCents reads one amount cell back into integer cents, the exact inverse of the
// renderer: signed dollars with two decimals, credits negative. Money never travels as a float,
// not even in a test's arithmetic.
func sageAmountCents(t *testing.T, cell string) int64 {
	t.Helper()
	neg := strings.HasPrefix(cell, "-")
	body := strings.TrimPrefix(cell, "-")
	parts := strings.Split(body, ".")
	if len(parts) != 2 || len(parts[1]) != 2 {
		t.Fatalf("the amount cell %q must be signed dollars with two decimals", cell)
	}
	var cents int64
	for _, ch := range parts[0] + parts[1] {
		if ch < '0' || ch > '9' {
			t.Fatalf("the amount cell %q must be digits after its sign", cell)
		}
		cents = cents*10 + int64(ch-'0')
	}
	if neg {
		return -cents
	}
	return cents
}

func sageFixture(rows []ExportRow) (*Service, *fakeStore) {
	f := newFakeStore()
	f.exportRows = rows
	f.mappings = []AccountMapping{
		{Target: TargetSage50, AccountCode: "1000", AccountName: "Cash Bank", PackageAccount: "1000"},
		{Target: TargetSage50, AccountCode: "4000", AccountName: "Sales", PackageAccount: "4000"},
	}
	return newTestServiceAs(f, identity.StaffContext{UserID: "u-ctrl", RoleCode: controllerRole, Tier: 1}), f
}

// TestExport_Sage50RendersItsGeneralJournalImportFormat is item 1's shape leg: two GL journals
// export as two transactions, each opening with the mm-dd-yy house day, the journal's own number
// as the source and the journal's memo as the comment, then one detail line per GL line (the
// mapped account, the signed amount, the line comment, 0), CRLF between every line and no file
// header.
func TestExport_Sage50RendersItsGeneralJournalImportFormat(t *testing.T) {
	svc, _ := sageFixture([]ExportRow{
		{EntryNo: 41, PostedAt: "2096-05-10", AccountCode: "1000", AccountName: "Cash Bank", DebitCents: 250000, Memo: "the cash leg"},
		{EntryNo: 41, PostedAt: "2096-05-10", AccountCode: "4000", AccountName: "Sales", CreditCents: 250000, Memo: "the sales leg"},
		{EntryNo: 42, PostedAt: "2096-05-17", AccountCode: "1000", AccountName: "Cash Bank", DebitCents: 12599, Memo: "the second cash leg"},
		{EntryNo: 42, PostedAt: "2096-05-17", AccountCode: "4000", AccountName: "Sales", CreditCents: 12599, Memo: "the second sales leg"},
	})

	res, err := svc.Export(context.Background(), ExportQuery{Year: 2096, Month: 5, Target: TargetSage50})
	if err != nil {
		t.Fatalf("a fully mapped period must export: %v", err)
	}
	if res.Format != TargetSage50 || !res.IsDraft || res.BatchID == "" {
		t.Fatalf("the Sage 50 export must record its own batch, got %+v", res)
	}
	txs := sageFile(t, res.Content)
	if len(txs) != 2 {
		t.Fatalf("two GL journals must export as two transactions, got %d:\n%s", len(txs), res.Content)
	}
	first, second := txs[0], txs[1]
	if first.Date != "05-10-96" {
		t.Fatalf("the first transaction is dated %q, want the posted house day 2096-05-10 as 05-10-96", first.Date)
	}
	if first.Source != "41" {
		t.Fatalf("the first transaction's source is %q, want the journal's own entry number 41", first.Source)
	}
	if first.Comment != "the cash leg" {
		t.Fatalf("the first transaction's comment is %q, want the journal's memo", first.Comment)
	}
	if second.Date != "05-17-96" || second.Source != "42" {
		t.Fatalf("the second transaction reads %q and %q, want 05-17-96 and the journal number 42", second.Date, second.Source)
	}
	if len(first.Lines) != 2 || len(second.Lines) != 2 {
		t.Fatalf("each transaction carries one detail line per GL line, got %d and %d", len(first.Lines), len(second.Lines))
	}
	if first.Lines[0][0] != "1000" || first.Lines[0][1] != "2500.00" || first.Lines[0][2] != "the cash leg" || first.Lines[0][3] != "0" {
		t.Fatalf("the first detail line reads %q, want the account, the positive debit, the line comment and 0", first.Lines[0])
	}
	if first.Lines[1][1] != "-2500.00" {
		t.Fatalf("the credit line renders %q, want the negative amount -2500.00", first.Lines[1][1])
	}
	if second.Lines[0][1] != "125.99" || second.Lines[1][1] != "-125.99" {
		t.Fatalf("the second transaction's amounts read %q and %q, want 125.99 and -125.99", second.Lines[0][1], second.Lines[1][1])
	}
	// The two sides tie per transaction and in total, read off the parsed file.
	var all int64
	for _, tx := range txs {
		var sum int64
		for _, line := range tx.Lines {
			sum += sageAmountCents(t, line[1])
		}
		if sum != 0 {
			t.Fatalf("transaction %s does not balance in the file: its amounts net %d cents", tx.Source, sum)
		}
		all += sum
	}
	if all != 0 {
		t.Fatalf("the file nets %d cents, want zero", all)
	}
}

// TestExport_Sage50RefusesAnUnbalancedEntryInWords is the tie's bite: a partial read hands the
// render one entry whose two sides differ, and the export refuses in words naming the entry
// before anything is written, the same assertion the QBO body carries (S7's P3-4 on 719).
func TestExport_Sage50RefusesAnUnbalancedEntryInWords(t *testing.T) {
	svc, f := sageFixture([]ExportRow{
		{EntryNo: 41, PostedAt: "2096-05-10", AccountCode: "1000", AccountName: "Cash Bank", DebitCents: 250000, Memo: "the cash leg"},
		{EntryNo: 41, PostedAt: "2096-05-10", AccountCode: "4000", AccountName: "Sales", CreditCents: 240000, Memo: "the short leg"},
	})

	_, err := svc.Export(context.Background(), ExportQuery{Year: 2096, Month: 5, Target: TargetSage50})
	if err == nil {
		t.Fatal("an unbalanced entry exported anyway")
	}
	for _, word := range []string{"Sage 50", "journal entry 41", "does not sum to zero", "100.00"} {
		if !strings.Contains(err.Error(), word) {
			t.Fatalf("the refusal must carry %q, got %q", word, err.Error())
		}
	}
	if f.batchInserts != 0 {
		t.Fatalf("a refused export must write nothing, but %d batches were recorded", f.batchInserts)
	}
}

// TestExport_Sage50RefusesAnAccountWithNoMappingInWords is AX-1's rule held at this body's render:
// a row whose account has no mapped Sage 50 code refuses the whole file in words, so the
// unmapped-movement read and the render cannot drift apart and hand the file a hole.
func TestExport_Sage50RefusesAnAccountWithNoMappingInWords(t *testing.T) {
	f := newFakeStore()
	f.exportRows = []ExportRow{
		{EntryNo: 41, PostedAt: "2096-05-10", AccountCode: "1000", AccountName: "Cash Bank", DebitCents: 250000, Memo: "the cash leg"},
		{EntryNo: 41, PostedAt: "2096-05-10", AccountCode: "1300", AccountName: "Prepaid Expenses", CreditCents: 250000, Memo: "the prepaid leg"},
	}
	// The unmapped read answered nothing (the seam's own refusal is AX-1's leg); the render itself
	// still refuses an account whose mapping it does not hold.
	f.mappings = []AccountMapping{
		{Target: TargetSage50, AccountCode: "1000", PackageAccount: "1000"},
	}
	svc := newTestServiceAs(f, identity.StaffContext{UserID: "u-ctrl", RoleCode: controllerRole, Tier: 1})

	_, err := svc.Export(context.Background(), ExportQuery{Year: 2096, Month: 5, Target: TargetSage50})
	if err == nil {
		t.Fatal("an account with no mapping exported anyway")
	}
	for _, word := range []string{"no mapping", "Sage 50", "1300 Prepaid Expenses"} {
		if !strings.Contains(err.Error(), word) {
			t.Fatalf("the refusal must carry %q, got %q", word, err.Error())
		}
	}
	if f.batchInserts != 0 {
		t.Fatalf("a refused export must write nothing, but %d batches were recorded", f.batchInserts)
	}
}

// TestExport_Sage50TheTwentyAndSeventyFiveCharacterLimitsCutOnRunes is item 4's limits leg:
// Sage 50 caps the source at twenty characters and a comment at 75, and the cut is on runes, so
// the books' accented and multibyte words are cut whole, never in the middle of a letter. An
// entry number can never reach twenty characters (an int64 is at most nineteen digits), so the
// source's limit is held at the helper, the one place it lives.
func TestExport_Sage50TheTwentyAndSeventyFiveCharacterLimitsCutOnRunes(t *testing.T) {
	wide := strings.Repeat("é", 80) // 80 runes, 160 bytes
	cjk := strings.Repeat("日", 80)  // 80 runes, 240 bytes
	svc, _ := sageFixture([]ExportRow{
		{EntryNo: 41, PostedAt: "2096-05-10", AccountCode: "1000", AccountName: "Cash Bank", DebitCents: 250000, Memo: wide},
		{EntryNo: 41, PostedAt: "2096-05-10", AccountCode: "4000", AccountName: "Sales", CreditCents: 250000, Memo: cjk},
	})
	res, err := svc.Export(context.Background(), ExportQuery{Year: 2096, Month: 5, Target: TargetSage50})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	txs := sageFile(t, res.Content)
	if txs[0].Comment != wide[:75*2] || utf8.RuneCountInString(txs[0].Comment) != 75 {
		t.Fatalf("the transaction comment must be the first 75 runes of the memo, got %d runes", utf8.RuneCountInString(txs[0].Comment))
	}
	if txs[0].Lines[1][2] != cjk[:75*3] || utf8.RuneCountInString(txs[0].Lines[1][2]) != 75 {
		t.Fatalf("the detail comment must be the first 75 runes of the memo, got %d runes", utf8.RuneCountInString(txs[0].Lines[1][2]))
	}
	if got := sageCut("é"+strings.Repeat("x", 30), 20); utf8.RuneCountInString(got) != 20 {
		t.Fatalf("the source cut must hold twenty runes, got %d (%q)", utf8.RuneCountInString(got), got)
	}
}

// TestExport_Sage50AFormulaLeaderInAMemoIsNeutralised is item 4's csvsafe leg: a memo led by a
// formula character rides the file with the single quote guard in front, inside the field's own
// double quotes, so a bookkeeper opening the file in a spreadsheet does not run it.
func TestExport_Sage50AFormulaLeaderInAMemoIsNeutralised(t *testing.T) {
	svc, _ := sageFixture([]ExportRow{
		{EntryNo: 41, PostedAt: "2096-05-10", AccountCode: "1000", AccountName: "Cash Bank", DebitCents: 250000, Memo: `=HYPERLINK("http://x","refund")`},
		{EntryNo: 41, PostedAt: "2096-05-10", AccountCode: "4000", AccountName: "Sales", CreditCents: 250000, Memo: "- reversal"},
	})
	res, err := svc.Export(context.Background(), ExportQuery{Year: 2096, Month: 5, Target: TargetSage50})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	txs := sageFile(t, res.Content)
	if txs[0].Comment != `'=HYPERLINK('http://x','refund')` {
		t.Fatalf("the transaction comment must carry the guard and the single quoted words, got %q", txs[0].Comment)
	}
	if txs[0].Lines[1][2] != "'- reversal" {
		t.Fatalf("the credit memo must carry the guard, got %q", txs[0].Lines[1][2])
	}
}

// TestExport_Sage50AQuoteACommaACarriageReturnAndALineFeedStayInsideTheQuotes is item 4's text
// leg: a memo carrying all four record breakers at once comes out one field, one record, the
// double quote become a single quote and the carriage return and line feed one space each, the
// comma kept by the field's own surrounding double quotes.
func TestExport_Sage50AQuoteACommaACarriageReturnAndALineFeedStayInsideTheQuotes(t *testing.T) {
	svc, _ := sageFixture([]ExportRow{
		{EntryNo: 41, PostedAt: "2096-05-10", AccountCode: "1000", AccountName: "Cash Bank", DebitCents: 250000, Memo: "say \"hi\", ok\r\nnow"},
		{EntryNo: 41, PostedAt: "2096-05-10", AccountCode: "4000", AccountName: "Sales", CreditCents: 250000, Memo: "the other side"},
	})
	res, err := svc.Export(context.Background(), ExportQuery{Year: 2096, Month: 5, Target: TargetSage50})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	txs := sageFile(t, res.Content)
	if len(txs) != 1 || len(txs[0].Lines) != 2 {
		t.Fatalf("the record breakers must not split the file: %d transactions, %d detail lines\n%s", len(txs), len(txs[0].Lines), res.Content)
	}
	if got := txs[0].Lines[0][2]; got != "say 'hi', ok  now" {
		t.Fatalf("the memo must read one field with the quote single and the breaks spaces, got %q", got)
	}
}

// TestExport_Sage50AnAccountOutsideTheShapeRefuses is item 4's shape leg: a mapped Sage account
// that is not four to eight digits refuses the export in words naming the HH account and the
// code, whatever way the code misses the shape.
func TestExport_Sage50AnAccountOutsideTheShapeRefuses(t *testing.T) {
	for _, bad := range []struct{ pkg, why string }{
		{"AB", "letters"},
		{"123", "three digits"},
		{"123456789", "nine digits"},
		{"12A4", "a letter inside"},
	} {
		f := newFakeStore()
		f.exportRows = []ExportRow{
			{EntryNo: 41, PostedAt: "2096-05-10", AccountCode: "1000", AccountName: "Cash Bank", DebitCents: 250000, Memo: "the cash leg"},
			{EntryNo: 41, PostedAt: "2096-05-10", AccountCode: "4000", AccountName: "Sales", CreditCents: 250000, Memo: "the other side"},
		}
		f.mappings = []AccountMapping{
			{Target: TargetSage50, AccountCode: "1000", AccountName: "Cash Bank", PackageAccount: bad.pkg},
			{Target: TargetSage50, AccountCode: "4000", PackageAccount: "4000"},
		}
		svc := newTestServiceAs(f, identity.StaffContext{UserID: "u-ctrl", RoleCode: controllerRole, Tier: 1})

		_, err := svc.Export(context.Background(), ExportQuery{Year: 2096, Month: 5, Target: TargetSage50})
		if err == nil {
			t.Fatalf("the %s code %q exported anyway", bad.why, bad.pkg)
		}
		for _, word := range []string{"1000 Cash Bank", bad.pkg, "four to eight digits"} {
			if !strings.Contains(err.Error(), word) {
				t.Fatalf("the %s refusal must carry %q, got %q", bad.why, word, err.Error())
			}
		}
		if f.batchInserts != 0 {
			t.Fatalf("a refused export must write nothing, but %d batches were recorded", f.batchInserts)
		}
	}
}

// TestExport_Sage50TheSmallestAndALargeInt64AmountsRenderExactly is item 4's money leg: the
// amount rides a uint64 magnitude, so the smallest and a large int64 render exactly instead of
// overflowing back to themselves on the negation, and a planted absurdity cannot turn into a
// wrong cent.
func TestExport_Sage50TheSmallestAndALargeInt64AmountsRenderExactly(t *testing.T) {
	if got := sageAmount(math.MinInt64); got != "-92233720368547758.08" {
		t.Fatalf("sageAmount(MinInt64) = %q, want -92233720368547758.08", got)
	}
	if got := sageAmount(math.MaxInt64); got != "92233720368547758.07" {
		t.Fatalf("sageAmount(MaxInt64) = %q, want 92233720368547758.07", got)
	}
	if sageAmount(0) != "0.00" || sageAmount(-5) != "-0.05" || sageAmount(12599) != "125.99" {
		t.Fatalf("sageAmount must keep its ordinary words, got %q %q %q", sageAmount(0), sageAmount(-5), sageAmount(12599))
	}
	svc, _ := sageFixture([]ExportRow{
		{EntryNo: 50, PostedAt: "2096-05-10", AccountCode: "1000", AccountName: "Cash Bank", DebitCents: math.MaxInt64, Memo: "the big debit"},
		{EntryNo: 50, PostedAt: "2096-05-10", AccountCode: "4000", AccountName: "Sales", CreditCents: math.MaxInt64, Memo: "the big credit"},
	})
	res, err := svc.Export(context.Background(), ExportQuery{Year: 2096, Month: 5, Target: TargetSage50})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	txs := sageFile(t, res.Content)
	if txs[0].Lines[0][1] != "92233720368547758.07" || txs[0].Lines[1][1] != "-92233720368547758.07" {
		t.Fatalf("the extremes must render exactly, got %q and %q", txs[0].Lines[0][1], txs[0].Lines[1][1])
	}
	var sum int64
	for _, line := range txs[0].Lines {
		sum += sageAmountCents(t, line[1])
	}
	if sum != 0 {
		t.Fatalf("the transaction must still balance in the file, nets %d cents", sum)
	}
}
