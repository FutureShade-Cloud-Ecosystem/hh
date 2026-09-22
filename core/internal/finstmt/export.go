package finstmt

// export.go, the CSV export of the statements, and the format-neutral table every report renders
// into.
//
// DIB-454 took the SECOND renderer off this file. It used to hold renderExcelXML, which wrote
// SpreadsheetML 2003 (`application/vnd.ms-excel`, `.xls`) and was offered to the user as "Excel" -
// a mislabelled older format whose every number was a string that happened to look numeric. The
// spreadsheet export now goes through the shared native-.xlsx exporter in xlsx.go, so this file has
// one renderer again.
//
// CSV IS UNCHANGED, deliberately. It is a shipped format with its own consumers, and the .xlsx
// rules that DIB-454 brings, money as a typed number, an empty side of an entry as a BLANK cell -
// are not free to apply here: a CSV cell has no type to carry, and silently emptying a column that
// has always read "0.00" would change a file other people's spreadsheets already parse. The two
// formats therefore diverge on purpose, and the parity test names exactly where and why.

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strconv"
)

// exportTable is the format-neutral tabular projection every report renders into.
type exportTable struct {
	Title   string
	Headers []string
	Rows    [][]string // cell values already stringified (money as plain decimal, e.g. "1234.56")
}

// centsToDecimal renders integer cents as a plain decimal-dollar string (no currency symbol, no grouping)
// so Excel/CSV treat the cell as a number. -12345 → "-123.45".
func centsToDecimal(cents int64) string {
	neg := cents < 0
	if neg {
		cents = -cents
	}
	s := fmt.Sprintf("%d.%02d", cents/100, cents%100)
	if neg {
		return "-" + s
	}
	return s
}

// renderCSV serialises an exportTable to RFC-4180 CSV (with the title as a leading single-cell row).
func renderCSV(t exportTable) []byte {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if t.Title != "" {
		_ = w.Write([]string{t.Title})
	}
	_ = w.Write(t.Headers)
	for _, row := range t.Rows {
		_ = w.Write(row)
	}
	w.Flush()
	return buf.Bytes()
}

// -- report → exportTable projections -----------------------------------------

func incomeStatementTable(rep IncomeStatementReport) exportTable {
	t := exportTable{Title: "Income Statement", Headers: []string{"Section", "Account", "Name", "Amount"}}
	for _, r := range rep.Revenue {
		t.Rows = append(t.Rows, []string{"REVENUE", r.AccountCode, r.AccountName, centsToDecimal(r.AmountCents)})
	}
	t.Rows = append(t.Rows, []string{"", "", "Total Revenue", centsToDecimal(rep.RevenueCents)})
	for _, r := range rep.Expenses {
		t.Rows = append(t.Rows, []string{"EXPENSE", r.AccountCode, r.AccountName, centsToDecimal(r.AmountCents)})
	}
	t.Rows = append(t.Rows, []string{"", "", "Total Expenses", centsToDecimal(rep.ExpenseCents)})
	t.Rows = append(t.Rows, []string{"", "", "Net Income", centsToDecimal(rep.NetIncomeCents)})
	return t
}

func balanceSheetTable(rep BalanceSheetReport) exportTable {
	t := exportTable{Title: "Balance Sheet", Headers: []string{"Section", "Account", "Name", "Amount"}}
	add := func(section string, rows []StatementRow) {
		for _, r := range rows {
			t.Rows = append(t.Rows, []string{section, r.AccountCode, r.AccountName, centsToDecimal(r.AmountCents)})
		}
	}
	add("ASSET", rep.Assets)
	t.Rows = append(t.Rows, []string{"", "", "Total Assets", centsToDecimal(rep.TotalAssetsCents)})
	add("LIABILITY", rep.Liabilities)
	t.Rows = append(t.Rows, []string{"", "", "Total Liabilities", centsToDecimal(rep.TotalLiabilitiesCents)})
	add("EQUITY", rep.Equity)
	t.Rows = append(t.Rows, []string{"", "", "Total Equity", centsToDecimal(rep.TotalEquityCents)})
	t.Rows = append(t.Rows, []string{"", "", "Net Income", centsToDecimal(rep.NetIncomeCents)})
	return t
}

func trialBalanceTable(rep TrialBalanceReport) exportTable {
	t := exportTable{Title: "Trial Balance", Headers: []string{"Account", "Name", "Type", "Debit", "Credit"}}
	for _, r := range rep.Rows {
		t.Rows = append(t.Rows, []string{
			r.AccountCode, r.AccountName, r.AccountType,
			centsToDecimal(r.TotalDebitCents), centsToDecimal(r.TotalCreditCents),
		})
	}
	t.Rows = append(t.Rows, []string{"", "TOTAL", "", centsToDecimal(rep.TotalDebitCents), centsToDecimal(rep.TotalCreditCents)})
	return t
}

func glDetailTable(rows []GLDetailRow) exportTable {
	t := exportTable{Title: "GL Detail", Headers: []string{
		"Entry", "Posted", "Account", "Name", "Branch", "Debit", "Credit", "Memo"}}
	for _, r := range rows {
		t.Rows = append(t.Rows, []string{
			strconv.Itoa(r.EntryNo), r.PostedAt, r.AccountCode, r.AccountName, r.BranchID,
			centsToDecimal(r.DebitCents), centsToDecimal(r.CreditCents), r.Memo,
		})
	}
	return t
}

func agingTable(sum AgingSummary) exportTable {
	t := exportTable{Title: "AR Aging", Headers: []string{
		"Customer", "Source", "Ref", "Branch", "Bucket", "AgeDays", "Amount"}}
	for _, r := range sum.Rows {
		t.Rows = append(t.Rows, []string{
			r.CustomerID, r.SourceType, r.SourceRefID, r.BranchID, r.Bucket,
			strconv.Itoa(r.AgeDays), centsToDecimal(r.AmountCents),
		})
	}
	t.Rows = append(t.Rows, []string{"", "", "", "", "GRAND TOTAL", "", centsToDecimal(sum.GrandTotalCents)})
	return t
}
