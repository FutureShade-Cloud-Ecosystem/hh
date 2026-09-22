package export

import (
	"context"
	"errors"
	"strings"
	"time"
)

// statement.go, DIB-454 / DIB-207 / DIB-203: the seams the financial-statement and transaction
// exports are reached through, and the filenames they land under.
//
// WHY THE MAPPING IS NOT IN THIS FILE. The catalogue consumer (catalogue.go) reads product and
// inventory through interfaces and builds its sheets here, because this package can import those two
// modules. The statements cannot work that way: `internal/finstmt` must import THIS package to build
// a workbook at all, so a `finstmt` import here would be a cycle. The split follows the dependency:
//
//	internal/export   owns the workbook, the cell types, the routes and the filenames  (imports nothing of finstmt)
//	internal/finstmt  owns the reports, and therefore owns the report → sheets mapping (imports export)
//
// The interfaces below are the join. They are declared HERE, in the consumer's own terms, so the
// export handler can be built and tested against a fake with no finance module in the picture at all
//, and *finstmt.Service satisfies them as written, so the wiring is one line and no adapter type
// exists to drift out of date.
//
// WHAT DIB-454 IS ACTUALLY FIXING. The statements screen offered "Export Excel" and produced
// SpreadsheetML 2003 served as `application/vnd.ms-excel` under a `.xls` filename, an XML file with
// a spreadsheet's name on it, which is the mislabelling the ticket opens with. Those bytes are gone;
// `?format=xlsx` now answers with the real package this module writes.

// StatementReport names one of the five finance reports an export can be asked for. Kept as a small
// closed set of strings rather than an int enum because it arrives from a query parameter and lands
// in a filename, the wire spelling IS the value, and a mapping between the two is a place to drift.
type StatementReport string

const (
	ReportIncomeStatement StatementReport = "income-statement"
	ReportBalanceSheet    StatementReport = "balance-sheet"
	ReportTrialBalance    StatementReport = "trial-balance"
	ReportARAging         StatementReport = "ar-aging"
	ReportGLDetail        StatementReport = "gl-detail"
)

// statementReports is the allowlist. A report name that is not on it is a 400, never a guess: an
// export route that silently substituted a different report would hand someone a file with the right
// filename and the wrong numbers in it.
var statementReports = map[StatementReport]bool{
	ReportIncomeStatement: true,
	ReportBalanceSheet:    true,
	ReportTrialBalance:    true,
	ReportARAging:         true,
}

// ParseStatementReport reads the ?report= parameter. The empty string is the income statement, which
// is the screen's landing report; anything unrecognised is refused (ok=false).
func ParseStatementReport(raw string) (StatementReport, bool) {
	r := StatementReport(strings.ToLower(strings.TrimSpace(raw)))
	if r == "" {
		return ReportIncomeStatement, true
	}
	return r, statementReports[r]
}

// StatementQuery is what a statement export is asked for: which report, over which period, for which
// branch. Month 0 means the whole fiscal year (the finance module's own convention, kept rather than
// re-invented so the export cannot mean something different from the screen).
type StatementQuery struct {
	Report   StatementReport
	Year     int
	Month    int
	BranchID string
	// BranchName is the human label for the subtitle. Blank renders as "All branches", an export
	// with no branch line reads as if it were scoped when it is not.
	BranchName string
}

// TransactionQuery is the GL-detail (transaction) export's filter, DIB-203.
type TransactionQuery struct {
	Year        int
	Month       int
	AccountCode string
	BranchID    string
	BranchName  string
}

// StatementWorkbookReader builds a statement workbook. Implemented by *finstmt.Service.
//
// It returns a *Workbook rather than bytes so the handler owns encoding and the download headers in
// ONE place for every export, and so a test can read the cells instead of unzipping a package.
type StatementWorkbookReader interface {
	StatementWorkbook(ctx context.Context, q StatementQuery) (*Workbook, error)
}

// TransactionWorkbookReader builds the two-sheet transaction workbook. Implemented by
// *finstmt.Service.
type TransactionWorkbookReader interface {
	TransactionWorkbook(ctx context.Context, q TransactionQuery) (*Workbook, error)
}

// StatementFilename names a statement download: the report, the period it covers, and nothing about
// the moment it was generated.
//
// The catalogue export is dated with TODAY because a catalogue extract is a snapshot of a moving
// thing. A statement is not: "income-statement-2026-03.xlsx" is the same file whenever it is pulled,
// and two people comparing March would otherwise be looking at two differently-named copies of one
// report and wondering which is current.
func StatementFilename(report StatementReport, year, month int) string {
	base := string(report)
	if base == "" {
		base = "statement"
	}
	switch {
	case year > 0 && month > 0:
		return base + "-" + isoYearMonth(year, month) + ".xlsx"
	case year > 0:
		return base + "-" + itoa4(year) + ".xlsx"
	default:
		return base + ".xlsx"
	}
}

// TransactionsFilename names the GL-detail download.
func TransactionsFilename(year, month int) string {
	return StatementFilename("transactions", year, month)
}

// AgingFilename names the AR-aging download. Aging is an "as at" report, it describes the state of
// the ledger at the moment it is asked for, not a closed period, so this one IS dated.
func AgingFilename(now time.Time) string {
	return "ar-aging-" + now.Format("2006-01-02") + ".xlsx"
}

func isoYearMonth(year, month int) string {
	m := "0" + itoa(month)
	return itoa4(year) + "-" + m[len(m)-2:]
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
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

// itoa4 renders a year without padding surprises: a year is four digits, and a two-digit one in a
// filename sorts wrong beside its neighbours.
func itoa4(year int) string {
	s := itoa(year)
	for len(s) < 4 {
		s = "0" + s
	}
	return s
}

// -- client-input errors --------------------------------------------------------------------------

// badRequestError marks a client input error so the handler can answer 400 rather than 500. The
// module had no error taxonomy before DIB-454 because its only route took no rejectable input; the
// statement/audit routes take a report name, a period and a UUID, all of which a caller can get
// wrong, and "export failed" with a 500 is the wrong answer to a typo.
type badRequestError struct{ msg string }

func (e badRequestError) Error() string { return e.msg }

func errBadRequest(msg string) error { return badRequestError{msg: msg} }

// IsBadRequest reports whether an error came from client input.
func IsBadRequest(err error) bool {
	var bre badRequestError
	return errors.As(err, &bre)
}
