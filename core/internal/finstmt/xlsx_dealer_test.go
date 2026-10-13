package finstmt

import (
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/export"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/dealer"
)

func incomeSheetTitle(t *testing.T) string {
	t.Helper()
	rep := composeIncome(2026, 3, "", []StatementRow{
		{AccountCode: "4000", AccountName: "Sales, Net", AccountType: "REVENUE", AmountCents: 100_00},
	})
	wb := xlsxSvc().incomeStatementWorkbook(rep, export.StatementQuery{Report: export.ReportIncomeStatement, Year: 2026, Month: 3})
	return sheetByName(t, wb, "Income Statement").Title
}

// The workbook title names the dealer's own configured name.
func TestXLSXEntity_ReadsTheConfiguredDealerName(t *testing.T) {
	t.Cleanup(func() { dealer.Set("") })
	dealer.Set("Gable Landscape Supply")
	if got := incomeSheetTitle(t); got != "Gable Landscape Supply, Income Statement" {
		t.Fatalf("title = %q, want the configured dealer name and the report", got)
	}
}

// With no dealer name set the title uses a neutral word, never another dealer's name.
func TestXLSXEntity_NeutralWhenNoDealerName(t *testing.T) {
	dealer.Set("")
	got := incomeSheetTitle(t)
	if strings.Contains(strings.ToLower(got), "dibbits") || got != "Dealer, Income Statement" {
		t.Fatalf("title = %q, want the neutral entity and the report", got)
	}
}
