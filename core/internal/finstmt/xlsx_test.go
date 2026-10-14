package finstmt

import (
	"encoding/csv"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/export"
)

// xlsx_test.go, DIB-454: the statements and transaction exports on the shared native-.xlsx exporter.

// exportClock is a fixed instant for the "generated" line. It is compared against nothing but itself
// (the subtitle string these tests assert), so no real clock can arm it, the DIB-427 rule.
var exportClock = func() time.Time { return time.Date(2026, 3, 31, 17, 5, 0, 0, time.UTC) }

func xlsxSvc() *Service {
	return NewService(NewRepository(nil)).WithClock(exportClock)
}

// cellText renders one typed cell the way a reader sees it, so a test can compare a workbook against
// a CSV without unzipping anything. Blank is "", which is precisely the distinction under test.
func cellText(c export.Cell) string {
	switch c.Kind {
	case export.KindBlank:
		return ""
	case export.KindString:
		return c.Text
	case export.KindNumber:
		return strings.TrimSuffix(strings.TrimRight(formatFloat(c.Number), "0"), ".")
	case export.KindMoney:
		return centsToDecimal(c.Cents)
	case export.KindDate:
		return c.When.Format("2006-01-02")
	}
	return ""
}

func formatFloat(v float64) string {
	s := make([]byte, 0, 24)
	return string(appendFloat(s, v))
}

func appendFloat(dst []byte, v float64) []byte {
	// strconv without the import churn in a test file that already has plenty.
	return append(dst, []byte(trimFloat(v))...)
}

func trimFloat(v float64) string {
	if v == float64(int64(v)) {
		return itoa64(int64(v))
	}
	return "" // no fractional numbers are produced by these projections
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [24]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func sheetByName(t *testing.T, wb *export.Workbook, name string) *export.Sheet {
	t.Helper()
	for _, s := range wb.Sheets {
		if s.Name == name {
			return s
		}
	}
	names := make([]string, 0, len(wb.Sheets))
	for _, s := range wb.Sheets {
		names = append(names, s.Name)
	}
	t.Fatalf("no sheet %q in the workbook; it has %v", name, names)
	return nil
}

func headers(sh *export.Sheet) []string {
	out := make([]string, 0, len(sh.Columns))
	for _, c := range sh.Columns {
		out = append(out, c.Header)
	}
	return out
}

// -- DIB-207: title block, typed money, blank-not-zero --------------------------------------------

// The two things DIB-207 asked for by name: a real title block, and money that reads as money.
func TestStatementXLSX_CarriesATitleBlockAndTypedMoney(t *testing.T) {
	rep := composeIncome(2026, 3, "", []StatementRow{
		{AccountCode: "4000", AccountName: "Sales, Net", AccountType: "REVENUE", AmountCents: 1_234_56},
		{AccountCode: "5000", AccountName: "COGS", AccountType: "EXPENSE", AmountCents: 400_00},
	})
	wb := xlsxSvc().incomeStatementWorkbook(rep, export.StatementQuery{Report: export.ReportIncomeStatement, Year: 2026, Month: 3})
	sh := sheetByName(t, wb, "Income Statement")

	if sh.Title != "Dealer, Income Statement" {
		t.Fatalf("sheet title = %q, want the entity and the report", sh.Title)
	}
	if !strings.Contains(sh.Subtitle, "March 2026") {
		t.Fatalf("subtitle %q carries no period; a printed statement with no period is unattributable", sh.Subtitle)
	}
	if !strings.Contains(sh.Subtitle, "All branches") {
		t.Fatalf("subtitle %q does not state the scope; an unscoped export must not read as if it were scoped", sh.Subtitle)
	}
	if !strings.Contains(sh.Subtitle, "generated 2026-03-31 17:05") {
		t.Fatalf("subtitle %q carries no generated-at stamp", sh.Subtitle)
	}
	if !sh.FreezeHeader {
		t.Fatal("the header row is not frozen; row 400 must still know what column it is in")
	}

	// Every amount is a MONEY cell holding CENTS, never a string that looks like money. This is the
	// rule the whole package exists for, and the one the old SpreadsheetML path broke.
	var moneyCells int
	for _, row := range sh.Rows {
		amount := row[3]
		if amount.Kind != export.KindMoney {
			t.Fatalf("amount cell %+v is kind %d, want KindMoney, a formatted string neither sums nor sorts", amount, amount.Kind)
		}
		moneyCells++
	}
	if moneyCells != 5 { // 2 accounts + 2 subtotals + net income
		t.Fatalf("%d money cells, want 5", moneyCells)
	}
	if got := sh.Rows[0][3].Cents; got != 1_234_56 {
		t.Fatalf("revenue cell carries %d cents, want 123456, money must cross as an integer", got)
	}
}

// The DIB-207 case the exporter's blank rule exists for: a line that is a debit has NOTHING in its
// credit cell. Not 0.00, that is a number in a ledger where there is none, and it sums.
func TestTransactionXLSX_ACreditLineLeavesTheDebitCellBLANKNotZero(t *testing.T) {
	rows := []GLDetailRow{
		{EntryNo: 1, PostedAt: "2026-03-04T00:00:00Z", Source: "INVOICE", AccountCode: "1100",
			AccountName: "Accounts Receivable", DebitCents: 113_00, CreditCents: 0, Memo: "INV-1001"},
		{EntryNo: 1, PostedAt: "2026-03-04T00:00:00Z", Source: "INVOICE", AccountCode: "4000",
			AccountName: "Sales, Net", DebitCents: 0, CreditCents: 100_00, Memo: "INV-1001"},
	}
	wb := xlsxSvc().transactionWorkbook(rows, export.TransactionQuery{Year: 2026, Month: 3})
	tx := sheetByName(t, wb, "Transactions")

	const debitCol, creditCol = 6, 7 // Entry, Posted, Source, Account, Name, Branch, Debit, Credit
	debitLine, creditLine := tx.Rows[0], tx.Rows[1]

	if debitLine[debitCol].Kind != export.KindMoney || debitLine[debitCol].Cents != 113_00 {
		t.Fatalf("the debit line's debit cell is %+v, want money 11300", debitLine[debitCol])
	}
	if debitLine[creditCol].Kind != export.KindBlank {
		t.Fatalf("the debit line's CREDIT cell is %+v, want BLANK, $0.00 in the other column is a number that is not there, and it sums",
			debitLine[creditCol])
	}
	if creditLine[creditCol].Kind != export.KindMoney || creditLine[creditCol].Cents != 100_00 {
		t.Fatalf("the credit line's credit cell is %+v, want money 10000", creditLine[creditCol])
	}
	if creditLine[debitCol].Kind != export.KindBlank {
		t.Fatalf("the credit line's CHARGE cell is %+v, want BLANK", creditLine[debitCol])
	}
}

// A statement AMOUNT of zero is a real, reportable balance and stays a $0.00 money cell. The blank
// rule is about the empty side of a debit/credit PAIR, not about zero in general, blanking a
// zero-balance account would hide the row someone opened the report to look for.
func TestStatementXLSX_AZeroBalanceAccountIsStillReportedAsZero(t *testing.T) {
	rep := composeIncome(2026, 0, "", []StatementRow{
		{AccountCode: "4900", AccountName: "Misc Revenue", AccountType: "REVENUE", AmountCents: 0},
	})
	wb := xlsxSvc().incomeStatementWorkbook(rep, export.StatementQuery{Report: export.ReportIncomeStatement, Year: 2026})
	sh := sheetByName(t, wb, "Income Statement")
	amount := sh.Rows[0][3]
	if amount.Kind != export.KindMoney || amount.Cents != 0 {
		t.Fatalf("a zero-balance account exported as %+v, want a real $0.00 money cell", amount)
	}
}

// -- DIB-207: the AR aging gets its own second sheet ----------------------------------------------

// The aging strip is a SHEET, not columns beside the ledger: a bucket column repeated down 400 detail
// rows sums to nonsense, while five numbers on their own sheet add up to one.
func TestAgingXLSX_TheBucketsAreASecondSheetThatTiesToTheDetail(t *testing.T) {
	sum := AgingSummary{
		BranchID: "b-1",
		Rows: []AgingRow{
			{CustomerID: "c-1", CustomerName: "Kane Landscaping", SourceType: "INVOICE", SourceRefID: "INV-1", Bucket: "CURRENT", AgeDays: 5, AmountCents: 500_00},
			{CustomerID: "c-2", CustomerName: "Bay Masonry", SourceType: "INVOICE", SourceRefID: "INV-2", Bucket: "31_60", AgeDays: 40, AmountCents: 250_00},
		},
		Buckets:         []AgingBucket{{Bucket: "CURRENT", Cents: 500_00}, {Bucket: "31_60", Cents: 250_00}},
		GrandTotalCents: 750_00,
	}
	wb := xlsxSvc().agingWorkbook(sum, export.StatementQuery{Report: export.ReportARAging, BranchID: "b-1", BranchName: "Trenton"})

	if len(wb.Sheets) != 2 {
		t.Fatalf("the aging export has %d sheets, want 2 (detail + Aging)", len(wb.Sheets))
	}
	detail := sheetByName(t, wb, "AR Aging")
	ag := sheetByName(t, wb, "Aging")

	if !strings.Contains(detail.Subtitle, "Trenton") {
		t.Fatalf("detail subtitle %q does not name the branch", detail.Subtitle)
	}
	if !strings.Contains(detail.Subtitle, "as at 2026-03-31") {
		t.Fatalf("detail subtitle %q carries no as-at date; aging is an as-at report", detail.Subtitle)
	}

	// The two sheets must agree: Σ buckets == the detail's grand total. A file that contradicts
	// itself is worse than one number being wrong, because neither can then be trusted.
	var bucketSum int64
	for _, row := range ag.Rows[:len(ag.Rows)-1] { // last row is the grand total
		bucketSum += row[1].Cents
	}
	grand := ag.Rows[len(ag.Rows)-1][1]
	if bucketSum != grand.Cents {
		t.Fatalf("the Aging sheet's buckets sum to %d but its grand total says %d", bucketSum, grand.Cents)
	}
	detailTotal := detail.Rows[len(detail.Rows)-1][7]
	if detailTotal.Cents != grand.Cents {
		t.Fatalf("the detail sheet's grand total is %d and the Aging sheet's is %d, one file, two answers",
			detailTotal.Cents, grand.Cents)
	}
}

// -- DIB-203: two sheets, and a summary derived from the SAME rows --------------------------------

func TestTransactionXLSX_IsTwoSheetsWithASummaryDerivedFromTheDetail(t *testing.T) {
	rows := []GLDetailRow{
		{EntryNo: 1, AccountCode: "4000", AccountName: "Sales, Net", CreditCents: 100_00, PostedAt: "2026-03-04T00:00:00Z"},
		{EntryNo: 2, AccountCode: "4000", AccountName: "Sales, Net", CreditCents: 50_00, PostedAt: "2026-03-05T00:00:00Z"},
		{EntryNo: 1, AccountCode: "1100", AccountName: "Accounts Receivable", DebitCents: 150_00, PostedAt: "2026-03-04T00:00:00Z"},
	}
	wb := xlsxSvc().transactionWorkbook(rows, export.TransactionQuery{Year: 2026, Month: 3})

	if len(wb.Sheets) != 2 || wb.Sheets[0].Name != "Summary" || wb.Sheets[1].Name != "Transactions" {
		t.Fatalf("transaction export has %d sheets starting with %q, want [Summary Transactions]", len(wb.Sheets), wb.Sheets[0].Name)
	}
	sm := sheetByName(t, wb, "Summary")
	tx := sheetByName(t, wb, "Transactions")

	if len(tx.Rows) != 3 {
		t.Fatalf("the Transactions sheet has %d rows, want every posted line (3)", len(tx.Rows))
	}
	// Accounts in CODE order (1100 before 4000), then the TOTAL row.
	if got := sm.Rows[0][0].Text; got != "1100" {
		t.Fatalf("the summary opens with account %q; it must read in chart-of-accounts order", got)
	}
	if got := sm.Rows[1][3].Number; got != 2 {
		t.Fatalf("account 4000 shows %v lines, want 2", got)
	}
	if got := sm.Rows[1][5].Cents; got != 150_00 {
		t.Fatalf("account 4000's credit total is %d, want 15000", got)
	}
	// The summary is derived from the detail, so the totals row must tie to it exactly.
	total := sm.Rows[len(sm.Rows)-1]
	if total[4].Cents != 150_00 || total[5].Cents != 150_00 {
		t.Fatalf("summary totals debit=%d credit=%d, want 15000/15000, the summary must be the detail, rolled up",
			total[4].Cents, total[5].Cents)
	}
	if total[6].Cents != 0 {
		t.Fatalf("Σdebit − Σcredit over a complete extract is %d, want 0", total[6].Cents)
	}
}

// The running Balance column, carried over from the client-side exporter DIB-454 deleted.
//
// Two rules, and both were in the shipped client behaviour: the balance is NATURAL (a revenue
// account's credits count POSITIVE, so a revenue line does not print as a negative number that makes
// the company look like it is losing money), and it RESETS per account (one running total across a
// mixed extract adds a revenue credit to an asset debit and reconciles to nothing).
func TestTransactionXLSX_TheBalanceColumnIsNaturalAndResetsPerAccount(t *testing.T) {
	rows := []GLDetailRow{
		{EntryNo: 11, AccountCode: "1000", AccountName: "Cash", AccountType: "ASSET", DebitCents: 250_00, Source: "SALE"},
		{EntryNo: 12, AccountCode: "1000", AccountName: "Cash", AccountType: "ASSET", CreditCents: 40_00, Source: "PAYMENT"},
		{EntryNo: 13, AccountCode: "1000", AccountName: "Cash", AccountType: "ASSET", DebitCents: 90_00, Source: "SALE"},
		{EntryNo: 20, AccountCode: "4000", AccountName: "Sales", AccountType: "REVENUE", CreditCents: 250_00, Source: "SALE"},
	}
	tx := sheetByName(t, xlsxSvc().transactionWorkbook(rows, export.TransactionQuery{}), "Transactions")
	const balCol = 8

	for i, want := range []int64{250_00, 210_00, 300_00} {
		if got := tx.Rows[i][balCol].Cents; got != want {
			t.Fatalf("row %d running balance = %d, want %d", i, got, want)
		}
	}
	// The REVENUE account starts a FRESH series, and its credit is POSITIVE.
	if got := tx.Rows[3][balCol].Cents; got != 250_00 {
		t.Fatalf("the revenue line's balance is %d, want +25000, a credit-normal account's credits are positive, and the series resets per account",
			got)
	}

	// And the summary's Balance for each account equals that account's final running balance, so the
	// two sheets cannot tell different stories about the same account.
	sm := sheetByName(t, xlsxSvc().transactionWorkbook(rows, export.TransactionQuery{}), "Summary")
	if got := sm.Rows[0][6].Cents; got != 300_00 {
		t.Fatalf("summary balance for 1000 = %d, want 30000 (its final running balance)", got)
	}
	if got := sm.Rows[1][6].Cents; got != 250_00 {
		t.Fatalf("summary balance for 4000 = %d, want +25000", got)
	}
}

// A posting date is a DATE cell, not a string. "2026-03-04" typed as text does not sort or filter,
// and "1-2" typed as text is the case a CSV gets wrong in the other direction.
func TestTransactionXLSX_ThePostedColumnIsADateAndAnUnparseableOneIsBlank(t *testing.T) {
	rows := []GLDetailRow{
		{EntryNo: 1, AccountCode: "4000", PostedAt: "2026-03-04T00:00:00Z", CreditCents: 100},
		{EntryNo: 2, AccountCode: "4000", PostedAt: "not a date", CreditCents: 100},
	}
	tx := sheetByName(t, xlsxSvc().transactionWorkbook(rows, export.TransactionQuery{}), "Transactions")
	if tx.Rows[0][1].Kind != export.KindDate {
		t.Fatalf("posted cell is kind %d, want KindDate", tx.Rows[0][1].Kind)
	}
	if got := tx.Rows[0][1].When.Format("2006-01-02"); got != "2026-03-04" {
		t.Fatalf("posted date = %s, want 2026-03-04", got)
	}
	if tx.Rows[1][1].Kind != export.KindBlank {
		t.Fatalf("an unparseable posted_at exported as %+v, want BLANK, never today's date, which would be a fabricated posting date in a ledger extract",
			tx.Rows[1][1])
	}
}

// -- AC7: CSV / .xlsx parity, and the two places they diverge on purpose --------------------------

// The replacement for the old SpreadsheetML parity test. Same intent, the two exports of one report
// carry the same facts, but now it also PINS the deliberate divergences, so a third one cannot
// appear without someone deciding to add it here.
func TestExportParity_TheXlsxAgreesWithTheCsvExceptTheDeliberateBlanks(t *testing.T) {
	rep := composeIncome(2026, 0, "", []StatementRow{
		{AccountCode: "4000", AccountName: "Sales, Net", AccountType: "REVENUE", AmountCents: 1_234_56},
		{AccountCode: "5000", AccountName: "COGS", AccountType: "EXPENSE", AmountCents: 400_00},
	})
	table := incomeStatementTable(rep)
	sh := sheetByName(t, xlsxSvc().incomeStatementWorkbook(rep, export.StatementQuery{Year: 2026}), "Income Statement")

	// Headers first: the two exports must describe the same columns.
	csvRecords := parseCSVRecords(t, renderCSV(table))
	if len(csvRecords) < 2 {
		t.Fatal("csv produced no rows")
	}
	csvHeader := csvRecords[1] // record 0 is the CSV's single-cell title row
	if strings.Join(csvHeader, "|") != strings.Join(headers(sh), "|") {
		t.Fatalf("column headers differ:\n  csv:  %v\n  xlsx: %v", csvHeader, headers(sh))
	}

	csvBody := csvRecords[2:]
	if len(csvBody) != len(sh.Rows) {
		t.Fatalf("row count differs: csv=%d xlsx=%d", len(csvBody), len(sh.Rows))
	}
	for i := range csvBody {
		for j := range csvHeader {
			want := csvBody[i][j]
			got := cellText(sh.Rows[i][j])
			if want == got {
				continue
			}
			t.Errorf("row %d column %q differs: csv=%q xlsx=%q", i, csvHeader[j], want, got)
		}
	}

	// And the money is still a bare decimal in the CSV, never "$".
	joined := strings.Join(csvHeader, "|") + "|" + strings.Join(csvBody[0], "|")
	if strings.Contains(joined, "$") {
		t.Errorf("csv money must be a bare decimal for spreadsheets, got: %s", joined)
	}
}

// The divergence, stated as its own test so it is a DECISION on the record rather than a difference
// somebody notices later: the trial balance's credit column is blank in the .xlsx where the CSV
// writes 0.00.
func TestExportParity_TheTrialBalanceBlanksTheEmptySideOnlyInTheXlsx(t *testing.T) {
	rep := TrialBalanceReport{
		Year: 2026,
		Rows: []TrialBalanceRow{
			{AccountCode: "1100", AccountName: "Accounts Receivable", AccountType: "ASSET", TotalDebitCents: 150_00, TotalCreditCents: 0},
		},
		TotalDebitCents: 150_00, TotalCreditCents: 150_00, InBalance: true,
	}
	csvRows := parseCSVRecords(t, renderCSV(trialBalanceTable(rep)))
	if got := csvRows[2][4]; got != "0.00" {
		t.Fatalf("csv credit cell = %q, want the unchanged \"0.00\", the CSV is a shipped format and DIB-454 did not touch it", got)
	}
	sh := sheetByName(t, xlsxSvc().trialBalanceWorkbook(rep, export.StatementQuery{Year: 2026}), "Trial Balance")
	if sh.Rows[0][4].Kind != export.KindBlank {
		t.Fatalf("xlsx credit cell = %+v, want BLANK", sh.Rows[0][4])
	}
	// The TOTALS row is the exception and carries both sides: Σdebit == Σcredit is the assertion the
	// report exists to make, and two blanks would not make it.
	totals := sh.Rows[len(sh.Rows)-1]
	if totals[3].Kind != export.KindMoney || totals[4].Kind != export.KindMoney {
		t.Fatalf("the totals row must state both sides, got debit=%+v credit=%+v", totals[3], totals[4])
	}
}

func parseCSVRecords(t *testing.T, raw []byte) [][]string {
	t.Helper()
	rd := csv.NewReader(strings.NewReader(string(raw)))
	rd.FieldsPerRecord = -1 // the title row is a single cell; data rows are wider
	recs, err := rd.ReadAll()
	if err != nil {
		t.Fatalf("csv parse: %v", err)
	}
	return recs
}
