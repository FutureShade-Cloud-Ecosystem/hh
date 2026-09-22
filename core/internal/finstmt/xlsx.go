package finstmt

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/export"
)

// xlsx.go, DIB-454 / DIB-207 / DIB-203: the statements and transaction exports as REAL .xlsx.
//
// WHAT THIS REPLACES. `?format=xlsx` used to answer with SpreadsheetML 2003, a single XML file
// served as `application/vnd.ms-excel` under a `.xls` filename. Excel opens it, which is why it
// survived, but it is not the format the button offered, every number in it was a STRING that
// happened to look numeric, and the extension did not match what the caller asked for. That is the
// mislabelling DIB-454 opens with.
//
// WHY THE MAPPING LIVES HERE AND NOT IN internal/export. This module must import `export` to build a
// workbook at all, so the reverse import would be a cycle. The split follows the dependency:
// `export` owns the workbook, the cell types and the download headers; `finstmt` owns the reports,
// and therefore owns the report → sheets mapping. The interfaces the export handler reaches this
// through (export.StatementWorkbookReader / export.TransactionWorkbookReader) are declared over
// export-owned query types, and *Service satisfies them as written.
//
// THREE THINGS THE FOUNDATION GIVES THESE REPORTS THAT THE OLD PATH COULD NOT:
//
//  1. Money is a NUMBER with a currency format, held as cents to the last step. The old exporter
//     wrote `centsToDecimal` output and let Excel's import heuristics decide what it was.
//  2. A zero that means "nothing on this side of the entry" is a BLANK CELL. A GL line is a debit OR
//     a credit; writing 0.00 in the other column puts a number in a ledger where there is none, and
//     it sums. This is the DIB-207 case the exporter's MoneyOr exists for, and it is why the
//     package doc forbids ever adding a cell kind for "money already formatted as text", which is
//     exactly the shortcut that would bring the old behaviour back.
//  3. A title block. DIB-207 asked for "formatting and titles" by name: Sheet.Title carries the
//     report, Sheet.Subtitle the period, branch, filter and generated-at line, so a printed page is
//     never mistaken for a different period's.
//
// CSV IS UNTOUCHED. It renders from the same exportTable it always has, byte for byte. The two
// formats no longer share a projection, and the parity test now says exactly where they diverge and
// why (see TestExportParity_TheXlsxAgreesWithTheCsvExceptTheDeliberateBlanks) rather than asserting
// a sameness that would forbid the blank-instead-of-zero rule.

// entity is the legal entity these statements belong to. It matches the frontend's ENTITY constant,
// which the brand test pins.
const entity = "Dibbits"

// monthNames renders the period label. Month 0 is a full fiscal year.
var monthNames = [...]string{
	"January", "February", "March", "April", "May", "June",
	"July", "August", "September", "October", "November", "December",
}

// periodLabel renders "March 2026" or "Fiscal year 2026".
func periodLabel(year, month int) string {
	if year == 0 {
		return ""
	}
	if month >= 1 && month <= 12 {
		return fmt.Sprintf("%s %d", monthNames[month-1], year)
	}
	return fmt.Sprintf("Fiscal year %d", year)
}

// branchLabel renders the branch line. An unnamed branch id is shown as the id: a truncated or
// omitted scope reads as "all branches", which is the one thing a branch-scoped statement must not
// be mistaken for.
func branchLabel(id, name string) string {
	switch {
	case name != "":
		return name
	case id != "":
		return id
	default:
		return "All branches"
	}
}

// subtitle assembles the provenance line every exported sheet carries.
func subtitle(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

// nowFn returns the service clock, which the export subtitle and filenames read. Tests pin it.
func (s *Service) nowFn() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// moneyOrBlank is the rule that DIB-207 is really about: a side of an entry that carries nothing is
// BLANK, not $0.00.
//
// It applies to the debit/credit PAIR columns only, GL detail's Debit/Credit and the trial
// balance's. It does NOT apply to a statement line's amount, where zero is a real, reportable
// balance: an account that nets to zero this period is a fact, and blanking it would hide a row that
// someone is looking for precisely because they expect it to be non-zero.
func moneyOrBlank(cents int64) export.Cell {
	if cents == 0 {
		return export.Blank()
	}
	return export.Money(cents)
}

// StatementWorkbook builds the .xlsx for one finance report, the export.StatementWorkbookReader
// seam. The report is fetched through the SAME service methods the JSON and CSV responses use, so
// the three renderings of a statement cannot disagree.
func (s *Service) StatementWorkbook(ctx context.Context, q export.StatementQuery) (*export.Workbook, error) {
	switch q.Report {
	case export.ReportIncomeStatement:
		rep, err := s.IncomeStatementReportFor(ctx, q.Year, q.Month, q.BranchID)
		if err != nil {
			return nil, err
		}
		return s.incomeStatementWorkbook(rep, q), nil
	case export.ReportBalanceSheet:
		rep, err := s.BalanceSheetReportFor(ctx, q.Year, q.BranchID)
		if err != nil {
			return nil, err
		}
		return s.balanceSheetWorkbook(rep, q), nil
	case export.ReportTrialBalance:
		rep, err := s.TrialBalanceReportFor(ctx, q.Year)
		if err != nil {
			return nil, err
		}
		return s.trialBalanceWorkbook(rep, q), nil
	case export.ReportARAging:
		sum, err := s.AgingSummaryFor(ctx, q.BranchID)
		if err != nil {
			return nil, err
		}
		return s.agingWorkbook(sum, q), nil
	default:
		return nil, validationError{msg: "unknown report " + string(q.Report)}
	}
}

// incomeStatementWorkbook, one sheet, revenue then expenses, each section subtotalled and net
// income last. Amount is a real money column: a zero-balance account is reported, not blanked.
func (s *Service) incomeStatementWorkbook(rep IncomeStatementReport, q export.StatementQuery) *export.Workbook {
	wb := export.New()
	sh := wb.AddSheet("Income Statement")
	sh.Title = entity + ", Income Statement"
	sh.Subtitle = subtitle(periodLabel(rep.Year, rep.Month), branchLabel(rep.BranchID, q.BranchName),
		"generated "+s.nowFn().Format("2006-01-02 15:04"))
	sh.Columns = []export.Column{
		{Header: "Section", Width: 12},
		{Header: "Account", Width: 12},
		{Header: "Name", Width: 38},
		{Header: "Amount", Width: 16},
	}
	for _, r := range rep.Revenue {
		sh.AddRow(export.Str("REVENUE"), export.Str(r.AccountCode), export.Str(r.AccountName), export.Money(r.AmountCents))
	}
	sh.AddRow(export.Blank(), export.Blank(), export.Str("Total Revenue"), export.Money(rep.RevenueCents))
	for _, r := range rep.Expenses {
		sh.AddRow(export.Str("EXPENSE"), export.Str(r.AccountCode), export.Str(r.AccountName), export.Money(r.AmountCents))
	}
	sh.AddRow(export.Blank(), export.Blank(), export.Str("Total Expenses"), export.Money(rep.ExpenseCents))
	sh.AddRow(export.Blank(), export.Blank(), export.Str("Net Income"), export.Money(rep.NetIncomeCents))
	return wb
}

// balanceSheetWorkbook, assets, liabilities, equity, each subtotalled, with the balance invariant
// stated on the sheet.
//
// The "Balanced" line is exported because the report carries it and a reader cannot recompute it:
// assets == liabilities + equity + net income is the check the finance module already performs, and
// a balance sheet that failed it must say so in the file rather than only on the screen it was
// downloaded from.
func (s *Service) balanceSheetWorkbook(rep BalanceSheetReport, q export.StatementQuery) *export.Workbook {
	wb := export.New()
	sh := wb.AddSheet("Balance Sheet")
	sh.Title = entity + ", Balance Sheet"
	sh.Subtitle = subtitle(periodLabel(rep.Year, 0), branchLabel(rep.BranchID, q.BranchName),
		"generated "+s.nowFn().Format("2006-01-02 15:04"))
	sh.Columns = []export.Column{
		{Header: "Section", Width: 14},
		{Header: "Account", Width: 12},
		{Header: "Name", Width: 38},
		{Header: "Amount", Width: 16},
	}
	add := func(section string, rows []StatementRow) {
		for _, r := range rows {
			sh.AddRow(export.Str(section), export.Str(r.AccountCode), export.Str(r.AccountName), export.Money(r.AmountCents))
		}
	}
	total := func(label string, cents int64) {
		sh.AddRow(export.Blank(), export.Blank(), export.Str(label), export.Money(cents))
	}
	add("ASSET", rep.Assets)
	total("Total Assets", rep.TotalAssetsCents)
	add("LIABILITY", rep.Liabilities)
	total("Total Liabilities", rep.TotalLiabilitiesCents)
	add("EQUITY", rep.Equity)
	total("Total Equity", rep.TotalEquityCents)
	total("Net Income", rep.NetIncomeCents)
	sh.AddRow(export.Blank(), export.Blank(), export.Str("Balanced"), export.Str(yesNo(rep.Balanced)))
	return wb
}

// trialBalanceWorkbook, the debit/credit columns, with the double-entry tie stated.
//
// Debit and Credit are the blank-not-zero pair: an account with no credit activity has an EMPTY
// credit cell. The totals row is the exception and carries both, because Σdebit == Σcredit is the
// assertion the report exists to make and two blanks would not make it.
func (s *Service) trialBalanceWorkbook(rep TrialBalanceReport, q export.StatementQuery) *export.Workbook {
	wb := export.New()
	sh := wb.AddSheet("Trial Balance")
	sh.Title = entity + ", Trial Balance"
	sh.Subtitle = subtitle(periodLabel(rep.Year, 0), branchLabel(q.BranchID, q.BranchName),
		"generated "+s.nowFn().Format("2006-01-02 15:04"),
		"in balance: "+yesNo(rep.InBalance))
	sh.Columns = []export.Column{
		{Header: "Account", Width: 12},
		{Header: "Name", Width: 38},
		{Header: "Type", Width: 14},
		{Header: "Debit", Width: 16},
		{Header: "Credit", Width: 16},
	}
	for _, r := range rep.Rows {
		sh.AddRow(
			export.Str(r.AccountCode), export.Str(r.AccountName), export.Str(r.AccountType),
			moneyOrBlank(r.TotalDebitCents), moneyOrBlank(r.TotalCreditCents),
		)
	}
	sh.AddRow(export.Blank(), export.Str("TOTAL"), export.Blank(),
		export.Money(rep.TotalDebitCents), export.Money(rep.TotalCreditCents))
	return wb
}

// agingWorkbook, the AR aging, in TWO sheets.
//
// This is the DIB-207 plan's "aging strip as its own sheet rather than columns nobody can sum next
// to the ledger". Sheet 1 is the per-invoice detail; sheet 2 is the bucket roll-up with the grand
// total. Putting the buckets beside the detail rows would mean a column whose value repeats down 400
// rows and sums to nonsense; on their own sheet they are five numbers that add up to one.
func (s *Service) agingWorkbook(sum AgingSummary, q export.StatementQuery) *export.Workbook {
	wb := export.New()
	sh := wb.AddSheet("AR Aging")
	sh.Title = entity + ", AR Aging"
	sh.Subtitle = subtitle("as at "+s.nowFn().Format("2006-01-02"), branchLabel(sum.BranchID, q.BranchName))
	sh.Columns = []export.Column{
		{Header: "Customer", Width: 32},
		{Header: "Customer ID", Width: 38},
		{Header: "Source", Width: 14},
		{Header: "Reference", Width: 24},
		{Header: "Branch ID", Width: 38},
		{Header: "Bucket", Width: 12},
		{Header: "Age (days)", Width: 12},
		{Header: "Amount", Width: 16},
	}
	for _, r := range sum.Rows {
		sh.AddRow(
			export.Str(r.CustomerName), export.Str(r.CustomerID), export.Str(r.SourceType),
			export.Str(r.SourceRefID), export.Str(r.BranchID), export.Str(r.Bucket),
			export.Num(float64(r.AgeDays)), export.Money(r.AmountCents),
		)
	}
	sh.AddRow(export.Blank(), export.Blank(), export.Blank(), export.Blank(), export.Blank(),
		export.Str("GRAND TOTAL"), export.Blank(), export.Money(sum.GrandTotalCents))

	ag := wb.AddSheet("Aging")
	ag.Title = "Aging summary"
	ag.Subtitle = sh.Subtitle
	ag.Columns = []export.Column{{Header: "Bucket", Width: 16}, {Header: "Amount", Width: 16}}
	for _, b := range sum.Buckets {
		ag.AddRow(export.Str(b.Bucket), export.Money(b.Cents))
	}
	ag.AddRow(export.Str("GRAND TOTAL"), export.Money(sum.GrandTotalCents))
	return wb
}

// TransactionWorkbook builds the DIB-203 transaction export, the export.TransactionWorkbookReader
// seam. TWO sheets, which is the whole point of the ticket:
//
//	Summary       one row per account: entry count, Σdebit, Σcredit, net. What a person opens first.
//	Transactions  every posted line, in the order the ledger holds them.
//
// The summary is derived HERE from the same rows the detail sheet carries, not from a second query.
// A summary read separately is a second definition of the total, and the two would eventually
// disagree inside one file, which is worse than either being wrong on its own, because the file
// contradicts itself and neither number can be trusted.
func (s *Service) TransactionWorkbook(ctx context.Context, q export.TransactionQuery) (*export.Workbook, error) {
	rows, err := s.GLDetail(ctx, GLDetailFilter{
		Year: q.Year, Month: q.Month, AccountCode: q.AccountCode, BranchID: q.BranchID,
	})
	if err != nil {
		return nil, err
	}
	return s.transactionWorkbook(rows, q), nil
}

// transactionWorkbook is the mapping half of TransactionWorkbook, split out so the finstmt handler
// can render the rows it has ALREADY read for the JSON/CSV response instead of asking the database
// for them a second time.
func (s *Service) transactionWorkbook(rows []GLDetailRow, q export.TransactionQuery) *export.Workbook {
	sub := subtitle(periodLabel(q.Year, q.Month), branchLabel(q.BranchID, q.BranchName),
		accountLabel(q.AccountCode), plural(len(rows), "posted line", "posted lines"),
		"generated "+s.nowFn().Format("2006-01-02 15:04"))

	wb := export.New()

	// Sheet 1, the summary.
	sm := wb.AddSheet("Summary")
	sm.Title = entity + ", Transactions"
	sm.Subtitle = sub
	sm.Columns = []export.Column{
		{Header: "Account", Width: 12},
		{Header: "Name", Width: 38},
		{Header: "Type", Width: 14},
		{Header: "Lines", Width: 10},
		{Header: "Debit", Width: 16},
		{Header: "Credit", Width: 16},
		{Header: "Balance", Width: 16},
	}
	var totalDebit, totalCredit int64
	for _, a := range summariseByAccount(rows) {
		sm.AddRow(
			export.Str(a.code), export.Str(a.name), export.Str(a.kind), export.Num(float64(a.lines)),
			moneyOrBlank(a.debit), moneyOrBlank(a.credit), export.Money(naturalBalance(a.kind, a.debit, a.credit)),
		)
		totalDebit += a.debit
		totalCredit += a.credit
	}
	// The TOTAL row states both sides and their difference, NOT a sum of the natural balances: adding
	// a credit-normal account's balance to a debit-normal one's produces a number that means nothing.
	// Σdebit − Σcredit over a complete extract is zero, and that IS the assertion worth printing.
	sm.AddRow(export.Blank(), export.Str("TOTAL"), export.Blank(), export.Num(float64(len(rows))),
		export.Money(totalDebit), export.Money(totalCredit), export.Money(totalDebit-totalCredit))

	// Sheet 2, every posted line.
	tx := wb.AddSheet("Transactions")
	tx.Title = "Posted lines"
	tx.Subtitle = sub
	tx.Columns = []export.Column{
		{Header: "Entry", Width: 10},
		{Header: "Posted", Width: 12},
		{Header: "Source", Width: 16},
		{Header: "Account", Width: 12},
		{Header: "Name", Width: 32},
		{Header: "Branch ID", Width: 38},
		{Header: "Debit", Width: 16},
		{Header: "Credit", Width: 16},
		{Header: "Balance", Width: 16},
		{Header: "Memo", Width: 44},
	}
	// running carries the per-ACCOUNT natural balance down the sheet. Per account, not per file: a
	// single running total across a mixed extract adds a revenue credit to an asset debit and prints
	// a number nobody can reconcile to anything.
	running := map[string]int64{}
	for _, r := range rows {
		bal := running[r.AccountCode] + naturalBalance(r.AccountType, r.DebitCents, r.CreditCents)
		running[r.AccountCode] = bal
		tx.AddRow(
			export.Num(float64(r.EntryNo)),
			export.Date(parsePostedAt(r.PostedAt)),
			export.Str(r.Source),
			export.Str(r.AccountCode),
			export.Str(r.AccountName),
			export.Str(r.BranchID),
			moneyOrBlank(r.DebitCents),
			moneyOrBlank(r.CreditCents),
			export.Money(bal),
			export.Str(r.Memo),
		)
	}
	return wb
}

// naturalBalance orients an amount the way the account's own type reads it.
//
// A revenue account with $100 of credits has a balance of +$100, not −$100. Exporting the raw
// debit−credit would print every revenue and liability line as a negative number, and the first
// person to sum the column would conclude the company was losing money. This mirrors
// sumDetailNatural, which is what the GL-detail screen ties to, the file and the screen must agree
// about which way is up.
func naturalBalance(accountType string, debit, credit int64) int64 {
	switch accountType {
	case "REVENUE", "LIABILITY", "EQUITY":
		return credit - debit
	default: // ASSET, EXPENSE, and anything unclassified
		return debit - credit
	}
}

// accountSummary is one account's roll-up inside the transaction export.
type accountSummary struct {
	code, name, kind string
	lines            int
	debit, credit    int64
}

// summariseByAccount rolls the posted lines up per account, in ACCOUNT CODE order.
//
// Sorted by code rather than by amount because a chart of accounts is read in code order and a
// summary that reorders itself between two exports of the same period is not comparable with itself.
func summariseByAccount(rows []GLDetailRow) []accountSummary {
	idx := map[string]*accountSummary{}
	order := make([]string, 0, 16)
	for _, r := range rows {
		a := idx[r.AccountCode]
		if a == nil {
			a = &accountSummary{code: r.AccountCode, name: r.AccountName, kind: r.AccountType}
			idx[r.AccountCode] = a
			order = append(order, r.AccountCode)
		}
		a.lines++
		a.debit += r.DebitCents
		a.credit += r.CreditCents
	}
	sortStrings(order)
	out := make([]accountSummary, 0, len(order))
	for _, code := range order {
		out = append(out, *idx[code])
	}
	return out
}

// sortStrings is a tiny insertion sort, the account list on one period's GL detail is tens of
// entries, and pulling in sort for it is not worth the import.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func accountLabel(code string) string {
	if code == "" {
		return ""
	}
	return "account " + code
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "NO"
}

// parsePostedAt reads the posted_at string the GL detail view returns. An unparseable value yields
// the zero time, which export.Date writes as a BLANK cell, never today's date, which in a ledger
// extract would be a fabricated posting date.
func parsePostedAt(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05Z07:00", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
