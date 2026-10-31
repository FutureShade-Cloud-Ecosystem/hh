package reporting

// saved_reports_test.go, W3-RW2: a saved report is a stored definition over one curated data set, and
// everything it can say is checked against that set's catalog. These legs run DB-free: every refusal
// happens before the repository is touched, and the renderers are pure.

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

func validSavedReport() SavedReportDefinition {
	return SavedReportDefinition{
		Name:    "Aging by customer",
		Dataset: "ar",
		Columns: []string{"customer_name", "total_cents"},
		Filters: []DatasetFilter{{Column: "branch_id", Op: "eq", Values: []any{"b-1"}}},
		Sort:    []SavedReportSort{{Column: "total_cents", Desc: true}},
	}
}

func mustRefuse(t *testing.T, def SavedReportDefinition, wants ...string) {
	t.Helper()
	_, err := datasetTestService(&fakeARSource{}).CreateSavedReport(context.Background(), def)
	if err == nil {
		t.Fatalf("the definition must be refused: %+v", def)
	}
	var bad httpx.BadRequestError
	if !errors.As(err, &bad) {
		t.Fatalf("a refusal answers 400 in words, got %T: %v", err, err)
	}
	for _, w := range wants {
		if !strings.Contains(err.Error(), w) {
			t.Fatalf("the refusal must name %q, got: %s", w, err.Error())
		}
	}
}

// -- item 2: no free query ----------------------------------------------------

func TestSavedReport_RefusesAColumnOutsideTheCatalog(t *testing.T) {
	def := validSavedReport()
	def.Columns = []string{"customer_name", "sum(total_cents)"}
	mustRefuse(t, def, "sum(total_cents)", "ar")
}

func TestSavedReport_RefusesAFilterFieldOutsideTheCatalog(t *testing.T) {
	def := validSavedReport()
	def.Filters = []DatasetFilter{{Column: "1=1; DROP TABLE users", Op: "eq", Values: []any{"x"}}}
	mustRefuse(t, def, "DROP TABLE users")
}

func TestSavedReport_RefusesAFilterOnAColumnThatCannotFilter(t *testing.T) {
	def := validSavedReport()
	def.Filters = []DatasetFilter{{Column: "customer_name", Op: "eq", Values: []any{"x"}}}
	mustRefuse(t, def, "customer_name")
}

func TestSavedReport_RefusesAnOperatorOutsideTheCatalog(t *testing.T) {
	def := validSavedReport()
	def.Filters = []DatasetFilter{{Column: "branch_id", Op: "like", Values: []any{"%"}}}
	mustRefuse(t, def, "like", "branch_id")
}

func TestSavedReport_RefusesAGroupingOutsideTheCatalog(t *testing.T) {
	def := validSavedReport()
	def.GroupBy = []string{"total_cents"}
	mustRefuse(t, def, "total_cents")
	def.GroupBy = []string{"customer_name; --"}
	mustRefuse(t, def, "customer_name; --")
}

func TestSavedReport_RefusesASortOutsideTheRead(t *testing.T) {
	def := validSavedReport()
	def.Sort = []SavedReportSort{{Column: "credit_limit_cents"}}
	mustRefuse(t, def, "credit_limit_cents", "sort")
	def.Sort = []SavedReportSort{{Column: "total_cents"}, {Column: "total_cents", Desc: true}}
	mustRefuse(t, def, "total_cents", "twice")
}

func TestSavedReport_RefusesAValueThatIsNotTheColumnsKind(t *testing.T) {
	def := validSavedReport()
	def.Filters = []DatasetFilter{{Column: "terms_days", Op: "eq", Values: []any{"thirty"}}}
	mustRefuse(t, def, "terms_days")
}

func TestSavedReport_RefusesAReadTheSetCouldNeverRun(t *testing.T) {
	def := SavedReportDefinition{Name: "GL", Dataset: "gl", Columns: []string{"account_code", "debit_cents"}}
	mustRefuse(t, def, "fiscal_year")
}

func TestSavedReport_RefusesAnUnknownSetAsNotFound(t *testing.T) {
	def := validSavedReport()
	def.Dataset = "free_queries"
	_, err := datasetTestService(&fakeARSource{}).CreateSavedReport(context.Background(), def)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown set is not found, got %v", err)
	}
}

func TestSavedReport_RefusesABlankOrOverlongName(t *testing.T) {
	def := validSavedReport()
	def.Name = "   "
	mustRefuse(t, def, "name")
	def.Name = strings.Repeat("x", 121)
	mustRefuse(t, def, "name")
}

func TestSavedReport_AValidDefinitionPassesEveryRefusalAndReachesTheStore(t *testing.T) {
	// No database in this leg: the only thing left to refuse the valid definition is the missing store,
	// which proves the refusals above are not refusing everything.
	_, err := datasetTestService(&fakeARSource{}).CreateSavedReport(context.Background(), validSavedReport())
	if !errors.Is(err, ErrNoDatabase) {
		t.Fatalf("a valid definition reaches the repository, got %v", err)
	}
}

// -- item 4: the CSV ----------------------------------------------------------

func TestRenderDatasetCSV_IsTheRunRowForRowWithMoneyAsTwoDecimals(t *testing.T) {
	svc := datasetTestService(&fakeARSource{lines: []ARAgingLine{
		{CustomerID: "c1", CustomerName: "Alpha", BranchID: "b-1", TermsDays: 30, TotalCents: 123456, CurrentCents: 5},
		{CustomerID: "c2", CustomerName: "Beta", BranchID: "b-1", TermsDays: 15, TotalCents: -250, CurrentCents: 100},
	}})
	res, err := svc.RunDataset(context.Background(), "ar", DatasetQuery{
		Columns: []string{"customer_name", "terms_days", "current_cents", "total_cents"},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	recs := parseCSV(t, renderDatasetCSV(res))
	want := [][]string{
		{"Customer", "Terms (days)", "Current", "Balance"},
		{"Alpha", "30", "0.05", "1234.56"},
		{"Beta", "15", "1.00", "-2.50"},
		{"TOTAL", "", "1.05", "1232.06"},
	}
	if len(recs) != len(want) {
		t.Fatalf("csv has %d records, want %d: %v", len(recs), len(want), recs)
	}
	for i := range want {
		if strings.Join(recs[i], "|") != strings.Join(want[i], "|") {
			t.Fatalf("csv record %d = %q, want %q", i, recs[i], want[i])
		}
	}
}

func TestRenderDatasetCSV_NeutralisesEveryFormulaLeader(t *testing.T) {
	res := DatasetResult{
		Columns: []DatasetColumn{
			datasetColumn("customer_name", "=Customer", KindText, false, false, false),
			datasetColumn("total_cents", "Balance", KindCents, false, false, true),
		},
		Rows: [][]any{
			{"=HYPERLINK(\"http://x\")", int64(-100)},
			{"+1+1", int64(1)},
			{"-2", int64(2)},
			{"@SUM(A1)", int64(3)},
			{"\tcmd", int64(4)},
			{"\rcmd", int64(5)},
			{"ok, with \"quotes\"", int64(6)},
		},
		RowCount: 7,
	}
	recs := parseCSV(t, renderDatasetCSV(res))
	if recs[0][0] != "'=Customer" {
		t.Fatalf("a header led by = must be quoted, got %q", recs[0][0])
	}
	for i, row := range recs[1:] {
		cell := row[0]
		if strings.ContainsRune("=+-@\t\r", rune(cell[0])) {
			t.Fatalf("row %d leads with a live formula character: %q", i, cell)
		}
	}
	if recs[1][0] != "'=HYPERLINK(\"http://x\")" {
		t.Fatalf("got %q", recs[1][0])
	}
	if recs[1][1] != "-1.00" {
		t.Fatalf("a negative money cell stays a number, got %q", recs[1][1])
	}
	if recs[7][0] != "ok, with \"quotes\"" {
		t.Fatalf("commas and quotes round trip through the CSV format, got %q", recs[7][0])
	}
}

func parseCSV(t *testing.T, body []byte) [][]string {
	t.Helper()
	r := csv.NewReader(bytes.NewReader(body))
	r.FieldsPerRecord = -1
	recs, err := r.ReadAll()
	if err != nil {
		t.Fatalf("the CSV must parse: %v\n%s", err, body)
	}
	return recs
}

// -- item 4: the same rows every time -----------------------------------------

func TestSortDatasetRows_IsTotalSoAReRunIsTheSameRows(t *testing.T) {
	cols := []DatasetColumn{
		datasetColumn("customer_name", "Customer", KindText, false, true, false),
		datasetColumn("total_cents", "Balance", KindCents, false, false, true),
	}
	a := [][]any{{"B", int64(5)}, {"A", int64(5)}, {"C", int64(9)}, {"A", int64(1)}}
	b := [][]any{{"A", int64(1)}, {"C", int64(9)}, {"A", int64(5)}, {"B", int64(5)}}
	sortDatasetRows(cols, a, []SavedReportSort{{Column: "total_cents", Desc: true}})
	sortDatasetRows(cols, b, []SavedReportSort{{Column: "total_cents", Desc: true}})
	for i := range a {
		if a[i][0] != b[i][0] || a[i][1] != b[i][1] {
			t.Fatalf("source order leaked into the sorted rows: %v vs %v", a, b)
		}
	}
	if a[0][1] != int64(9) || a[3][1] != int64(1) {
		t.Fatalf("descending by total: %v", a)
	}
}
