package export

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// statement_test.go, DIB-454: the statement and transaction routes on the export handler.
//
// These test the ROUTE, not the mapping: the report → sheets projection lives in internal/finstmt
// (which must import this package, so the reverse would be a cycle) and is tested there against real
// reports. What is asserted here is everything the handler owns, the parameter grading, the
// filename, the headers, and the fact that a nil seam registers nothing at all.

type fakeStatementReader struct {
	wb  *Workbook
	err error
	got StatementQuery
}

func (f *fakeStatementReader) StatementWorkbook(_ context.Context, q StatementQuery) (*Workbook, error) {
	f.got = q
	if f.err != nil {
		return nil, f.err
	}
	return f.wb, nil
}

type fakeTransactionReader struct {
	wb  *Workbook
	err error
	got TransactionQuery
}

func (f *fakeTransactionReader) TransactionWorkbook(_ context.Context, q TransactionQuery) (*Workbook, error) {
	f.got = q
	if f.err != nil {
		return nil, f.err
	}
	return f.wb, nil
}

func oneSheetWorkbook() *Workbook {
	wb := New()
	sh := wb.AddSheet("Income Statement")
	sh.Title = "Dibbits, Income Statement"
	sh.Columns = []Column{{Header: "Account", Width: 12}, {Header: "Amount", Width: 16}}
	sh.AddRow(Str("4000"), Money(1_234_56))
	return wb
}

func statementHandler(r StatementWorkbookReader) *Handler {
	return NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithStatements(r).WithClock(auditClock)
}

func getExport(t *testing.T, h *Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	exportMux(t, h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestStatementRoute_AnswersARealXlsxNamedForItsPeriod(t *testing.T) {
	f := &fakeStatementReader{wb: oneSheetWorkbook()}
	rec := getExport(t, statementHandler(f), "/api/v1/exports/statement.xlsx?report=income-statement&year=2026&month=3&branch_id=b-1&branch_name=Trenton")

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != ContentType {
		t.Errorf("Content-Type=%q, want %q", ct, ContentType)
	}
	// Named for the PERIOD, not for today: March's income statement is the same file whenever it is
	// pulled, and two people comparing March must not end up with two differently-named copies.
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="income-statement-2026-03.xlsx"`) {
		t.Errorf("Content-Disposition=%q, want income-statement-2026-03.xlsx", cd)
	}
	if body := rec.Body.Bytes(); len(body) < 2 || body[0] != 'P' || body[1] != 'K' {
		t.Errorf("body is not an OOXML (ZIP) package")
	}
	want := StatementQuery{Report: ReportIncomeStatement, Year: 2026, Month: 3, BranchID: "b-1", BranchName: "Trenton"}
	if f.got != want {
		t.Errorf("the finance module was asked for %+v, want %+v", f.got, want)
	}
}

// AR aging is an "as at" report, it describes the ledger at the moment it is pulled, not a closed
// period, so it is the one statement download that IS dated with today.
func TestStatementRoute_TheAgingDownloadIsDatedWithToday(t *testing.T) {
	f := &fakeStatementReader{wb: oneSheetWorkbook()}
	rec := getExport(t, statementHandler(f), "/api/v1/exports/statement.xlsx?report=ar-aging")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="ar-aging-2026-08-12.xlsx"`) {
		t.Errorf("Content-Disposition=%q, want ar-aging-2026-08-12.xlsx", cd)
	}
}

// A report name nobody recognises is a 400. Substituting a different report would hand someone a
// file with the right name and the wrong numbers in it.
func TestStatementRoute_AnUnknownReportIsRefused(t *testing.T) {
	f := &fakeStatementReader{wb: oneSheetWorkbook()}
	rec := getExport(t, statementHandler(f), "/api/v1/exports/statement.xlsx?report=cashflow")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
	if f.got.Report != "" {
		t.Error("the finance module was asked for a report the route had already refused")
	}
}

// A malformed period is refused rather than silently read as 0, which would answer a DIFFERENT
// period than the one asked for and look like success.
func TestStatementRoute_AMalformedPeriodIsRefusedNotReadAsZero(t *testing.T) {
	for _, q := range []string{"year=twentytwentysix", "year=2026&month=-1", "year=2026&month=13", "year=2026&month=March"} {
		f := &fakeStatementReader{wb: oneSheetWorkbook()}
		rec := getExport(t, statementHandler(f), "/api/v1/exports/statement.xlsx?report=income-statement&"+q)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("?%s → %d, want 400 (%s)", q, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
}

// An absent period is NOT an error: no year means the finance module's own "not narrowed", which is
// what the screen's landing state asks for.
func TestStatementRoute_AnAbsentPeriodIsNotAnError(t *testing.T) {
	f := &fakeStatementReader{wb: oneSheetWorkbook()}
	rec := getExport(t, statementHandler(f), "/api/v1/exports/statement.xlsx")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	if f.got.Report != ReportIncomeStatement {
		t.Errorf("no ?report= resolved to %q, want the screen's landing report", f.got.Report)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="income-statement.xlsx"`) {
		t.Errorf("Content-Disposition=%q, want an undated, unperiodised name", cd)
	}
}

// A failed READ is a 500 in JSON, never a spreadsheet MIME type over an error body, which is a file
// the user opens and blames the data for.
func TestStatementRoute_AFailedReadIsAJSONErrorNotAHalfWrittenFile(t *testing.T) {
	f := &fakeStatementReader{err: errors.New("view unavailable")}
	rec := getExport(t, statementHandler(f), "/api/v1/exports/statement.xlsx?report=trial-balance&year=2026")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); strings.Contains(ct, "spreadsheet") {
		t.Fatalf("a failed export answered with %q, a spreadsheet content type over an error body", ct)
	}
}

// -- transactions (DIB-203) -----------------------------------------------------------------------

func TestTransactionsRoute_AnswersARealXlsxAndPassesTheFilterThrough(t *testing.T) {
	wb := New()
	wb.AddSheet("Summary").AddRow(Str("4000"), Money(100_00))
	wb.AddSheet("Transactions").AddRow(Str("4000"), Money(100_00))
	f := &fakeTransactionReader{wb: wb}
	h := NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil))).WithTransactions(f).WithClock(auditClock)

	rec := getExport(t, h, "/api/v1/exports/transactions.xlsx?year=2026&month=3&account_code=4000&branch_id=b-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", rec.Code, rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="transactions-2026-03.xlsx"`) {
		t.Errorf("Content-Disposition=%q", cd)
	}
	want := TransactionQuery{Year: 2026, Month: 3, AccountCode: "4000", BranchID: "b-1"}
	if f.got != want {
		t.Errorf("the finance module was asked for %+v, want %+v", f.got, want)
	}
}

// -- filenames ------------------------------------------------------------------------------------

func TestStatementFilename_IsNamedForThePeriodItCovers(t *testing.T) {
	cases := []struct {
		report      StatementReport
		year, month int
		want        string
	}{
		{ReportIncomeStatement, 2026, 3, "income-statement-2026-03.xlsx"},
		{ReportIncomeStatement, 2026, 12, "income-statement-2026-12.xlsx"},
		{ReportBalanceSheet, 2026, 0, "balance-sheet-2026.xlsx"},
		{ReportTrialBalance, 0, 0, "trial-balance.xlsx"},
		{ReportIncomeStatement, 999, 1, "income-statement-0999-01.xlsx"},
	}
	for _, c := range cases {
		if got := StatementFilename(c.report, c.year, c.month); got != c.want {
			t.Errorf("StatementFilename(%q, %d, %d) = %q, want %q", c.report, c.year, c.month, got, c.want)
		}
	}
}

func TestParseStatementReport_DefaultsToTheLandingReportAndRefusesTheRest(t *testing.T) {
	if r, ok := ParseStatementReport(""); !ok || r != ReportIncomeStatement {
		t.Fatalf("empty report → %q/%v, want the income statement", r, ok)
	}
	if r, ok := ParseStatementReport("  AR-AGING "); !ok || r != ReportARAging {
		t.Fatalf("case/space-insensitive parse failed: %q/%v", r, ok)
	}
	// gl-detail has its OWN route (transactions.xlsx, two sheets) and is deliberately NOT a
	// statement report, routing it here would produce a one-sheet file under a name that promises
	// the two-sheet one.
	if _, ok := ParseStatementReport("gl-detail"); ok {
		t.Fatal("gl-detail was accepted as a statement report; it belongs to the transactions route")
	}
	if _, ok := ParseStatementReport("cashflow"); ok {
		t.Fatal("an unknown report was accepted")
	}
}
