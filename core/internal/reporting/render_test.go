package reporting

// render_test.go, the export/print parity properties, plus the totals and coercion rules. These are
// what make "the screen, the spreadsheet and the printout say the same numbers" checkable.

import (
	"encoding/csv"
	"strings"
	"testing"
	"time"
)

func sampleResult() Result {
	cols := []Column{
		col("day", "Day", KindDate, false),
		col("invoice_count", "Invoices", KindInt, true),
		col("qty_base", "Quantity", KindQty, true),
		col("total_cents", "Total", KindCents, true),
		col("auto", "Automatic", KindBool, false),
	}
	rows := [][]any{
		{"2026-01-05", int64(2), 12.5, int64(123_456), true},
		{"2026-01-06", int64(3), 0.25, int64(-1_050), false},
	}
	return Result{
		Code: "sample", Name: "Sample Report", Category: "SALES",
		Columns: cols, Rows: rows, RowCount: len(rows),
		Totals: totalsFor(cols, rows),
		Params: map[string]string{"to_date": "2026-01-31", "from_date": "2026-01-01"},
		RunAt:  time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC),
	}
}

// TestTotals_SumOnlyFlaggedNumericColumns.
func TestTotals_SumOnlyFlaggedNumericColumns(t *testing.T) {
	t.Parallel()
	res := sampleResult()
	if got := res.Totals["invoice_count"]; got != int64(5) {
		t.Errorf("invoice_count total = %v, want 5", got)
	}
	if got := res.Totals["total_cents"]; got != int64(122_406) {
		t.Errorf("total_cents total = %v, want 122406 (a negative row must SUBTRACT)", got)
	}
	if got, ok := res.Totals["qty_base"].(float64); !ok || got != 12.75 {
		t.Errorf("qty_base total = %v, want 12.75", res.Totals["qty_base"])
	}
	if _, ok := res.Totals["day"]; ok {
		t.Error("a text column appeared in the totals, a totals row must not carry nonsense")
	}
	if _, ok := res.Totals["auto"]; ok {
		t.Error("a bool column appeared in the totals")
	}
}

// TestCSV_HasTitleParamsHeadersRowsAndTotal, an exported file that does not say what it is a report OF
// is how the wrong quarter reaches a board pack.
func TestCSV_HasTitleParamsHeadersRowsAndTotal(t *testing.T) {
	t.Parallel()
	res := sampleResult()
	recs, err := csv.NewReader(strings.NewReader(string(renderCSV(res)))).ReadAll()
	if err != nil {
		t.Fatalf("the CSV this module produces is not parseable CSV: %v", err)
	}
	if len(recs) != 3+1+2+1 { // title + params + generated, headers, 2 rows, totals
		t.Fatalf("csv has %d records, want 7 (3 title-block + header + 2 rows + total): %v", len(recs), recs)
	}
	if recs[0][0] != "Sample Report" {
		t.Errorf("title row = %q", recs[0][0])
	}
	if recs[1][1] != "from_date=2026-01-01; to_date=2026-01-31" {
		t.Errorf("parameter row = %q, parameters must be listed in a stable order", recs[1][1])
	}
	if recs[3][0] != "Day" || recs[3][3] != "Total" {
		t.Errorf("header row = %v, want the column LABELS", recs[3])
	}
	if recs[4][3] != "1234.56" {
		t.Errorf("money cell = %q, want a plain decimal with no symbol or grouping so Excel sums it", recs[4][3])
	}
	if recs[5][3] != "-10.50" {
		t.Errorf("negative money cell = %q, want -10.50", recs[5][3])
	}
	if recs[6][0] != "TOTAL" || recs[6][3] != "1224.06" {
		t.Errorf("totals row = %v, want TOTAL … 1224.06", recs[6])
	}
}

// TestHTMLAndCSV_RenderIdenticalCellValues is the parity AC, checked rather than asserted: every cell
// in the printed artefact appears verbatim in the exported CSV. Both go through formatCell, and this
// test is what stops a future "nicer" HTML formatter from quietly diverging.
func TestHTMLAndCSV_RenderIdenticalCellValues(t *testing.T) {
	t.Parallel()
	res := sampleResult()
	htmlDoc := renderHTML(res)
	recs, err := csv.NewReader(strings.NewReader(string(renderCSV(res)))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range recs[4:6] { // the two data rows
		for _, cell := range rec {
			if cell == "" {
				continue
			}
			if !strings.Contains(htmlDoc, ">"+cell+"<") {
				t.Errorf("cell %q is in the CSV but not in the printed HTML, the two renderings have diverged", cell)
			}
		}
	}
	if !strings.Contains(htmlDoc, "TOTAL") {
		t.Error("the printed artefact has no totals row")
	}
}

// TestHTML_EscapesCellText. Report cells carry customer names, product descriptions and bill lines -
// free text a person typed. The printed artefact is stored and re-served months later, so an unescaped
// cell would be stored XSS with a long fuse.
func TestHTML_EscapesCellText(t *testing.T) {
	t.Parallel()
	res := Result{
		Name:    `Sample <script>alert(1)</script>`,
		Columns: []Column{col("description", "Description", KindText, false)},
		Rows:    [][]any{{`<img src=x onerror="alert(1)">`}},
		Params:  map[string]string{},
		RunAt:   time.Now(),
	}
	out := renderHTML(res)
	if strings.Contains(out, "<script>") {
		t.Error("the report TITLE was not escaped")
	}
	if strings.Contains(out, "<img src=x") {
		t.Error("a report CELL was not escaped, a stored artefact is re-served later, so this is stored XSS")
	}
	if !strings.Contains(out, "&lt;img src=x") {
		t.Error("the cell is missing entirely, escaping must not drop content")
	}
}

// TestFormatCell_QuantityTrimsTrailingZeros, "12" must not print as "12.0000" in a column a person
// reads down.
func TestFormatCell_QuantityTrimsTrailingZeros(t *testing.T) {
	t.Parallel()
	cases := map[float64]string{12: "12", 12.5: "12.5", 0.25: "0.25", 0: "0", 1.2345: "1.2345"}
	for in, want := range cases {
		if got := formatCell(KindQty, in); got != want {
			t.Errorf("formatCell(qty, %v) = %q, want %q", in, got, want)
		}
	}
}

// TestCoerce_NullsBecomeKindZeros. A NULL in a money column must read as 0, not as an empty string that
// breaks a spreadsheet's SUM.
func TestCoerce_NullsBecomeKindZeros(t *testing.T) {
	t.Parallel()
	if got := coerce(KindCents, nil); got != int64(0) {
		t.Errorf("NULL cents coerced to %#v, want int64(0)", got)
	}
	if got := coerce(KindQty, nil); got != float64(0) {
		t.Errorf("NULL qty coerced to %#v, want float64(0)", got)
	}
	if got := coerce(KindText, nil); got != "" {
		t.Errorf("NULL text coerced to %#v, want \"\"", got)
	}
	if got := coerce(KindBool, nil); got != false {
		t.Errorf("NULL bool coerced to %#v, want false", got)
	}
}

// TestCentsToDecimal covers the edges a money formatter gets wrong: sub-dollar, exact dollar, negative
// sub-dollar (where a naive implementation prints "-0.05" as "0.-5" or drops the sign).
func TestCentsToDecimal(t *testing.T) {
	t.Parallel()
	cases := map[int64]string{0: "0.00", 5: "0.05", 100: "1.00", -5: "-0.05", -123456: "-1234.56", 99: "0.99"}
	for in, want := range cases {
		if got := centsToDecimal(in); got != want {
			t.Errorf("centsToDecimal(%d) = %q, want %q", in, got, want)
		}
	}
}

// TestCommissionResult_ProjectsIntoTheSameShape, the print/export parity requirement for commissions:
// a period renders through the SAME Result, columns and formatter as a template report.
func TestCommissionResult_ProjectsIntoTheSameShape(t *testing.T) {
	t.Parallel()
	p := CommissionPeriod{
		RepUserID: "rep", RepName: "Alex Morgan", PeriodStart: "2026-01-01", PeriodEnd: "2026-01-31",
		Lines: []CommissionLine{{
			AttributedLine: AttributedLine{InvoiceNo: "INV-1", InvoiceDate: "2026-01-05",
				CustomerName: "Kingston Hardscape", ProductSKU: "PAV-01", RevenueCents: 100_000, CostCents: 75_000},
			MarginCentsV: 25_000, Basis: BasisMargin, BasisCents: 25_000, RateBps: 500, CommissionCents: 1_250,
		}},
	}
	res := commissionResult(p, time.Now())
	if res.Category != "COMMISSION" {
		t.Errorf("category = %q", res.Category)
	}
	if got := res.Totals["commission_cents"]; got != int64(1_250) {
		t.Errorf("commission total = %v, want 1250", got)
	}
	body := string(renderCSV(res))
	if !strings.Contains(body, "12.50") {
		t.Error("the commission CSV does not carry the commission amount as a plain decimal")
	}
	if !strings.Contains(body, "Alex Morgan") {
		t.Error("the commission CSV does not name the rep it is for")
	}
}

// TestSanitizeFilename keeps a download name free of path and header characters. No user string reaches
// it today; the guard is here so the first one that does cannot smuggle a quote into a
// Content-Disposition header.
func TestSanitizeFilename(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"sales_by_day_2026-01-01": "sales_by_day_2026-01-01",
		`../../etc/passwd`:        "______etc_passwd",
		`a"; rm -rf /`:            "a___rm_-rf__",
	}
	for in, want := range cases {
		if got := sanitizeFilename(in); got != want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}
