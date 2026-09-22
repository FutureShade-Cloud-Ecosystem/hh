package export

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"
	"time"
)

// xlsx_test.go, the OOXML package's own tests.
//
// These assert the SHAPE OF THE FILE, not just that the writer ran: a .xlsx that is silently missing
// a relationship or an Override still writes 4KB of plausible bytes and then fails in Excel with
// "we found a problem with some content", which is the least debuggable error message in office
// software. Every part this writer emits is opened, parsed and checked here.

// -- helpers --------------------------------------------------------------------------------------

// parts unzips a rendered workbook into name → body.
func parts(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("the export is not a readable zip: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		out[f.Name] = string(body)
	}
	return out
}

func render(t *testing.T, wb *Workbook) map[string]string {
	t.Helper()
	data, err := wb.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	return parts(t, data)
}

// xlCell is one parsed <c> from a worksheet: its reference, style index, type and value.
type xlCell struct {
	Ref    string `xml:"r,attr"`
	Style  string `xml:"s,attr"`
	Type   string `xml:"t,attr"`
	Value  string `xml:"v"`
	Inline struct {
		Text string `xml:"t"`
	} `xml:"is"`
}

type xlRow struct {
	Ref   string   `xml:"r,attr"`
	Cells []xlCell `xml:"c"`
}

type xlSheet struct {
	Rows []xlRow `xml:"sheetData>row"`
}

func parseSheet(t *testing.T, body string) xlSheet {
	t.Helper()
	var s xlSheet
	if err := xml.Unmarshal([]byte(body), &s); err != nil {
		t.Fatalf("worksheet is not well-formed XML: %v", err)
	}
	return s
}

// cellAt finds a cell by A1 reference, or reports that no <c> element exists for it, which is the
// blank-cell case and a thing several tests assert deliberately.
func cellAt(sheet xlSheet, ref string) (xlCell, bool) {
	for _, r := range sheet.Rows {
		for _, c := range r.Cells {
			if c.Ref == ref {
				return c, true
			}
		}
	}
	return xlCell{}, false
}

func sampleWorkbook() *Workbook {
	wb := New()
	sh := wb.AddSheet("Catalogue")
	sh.Title = "Product catalogue"
	sh.Subtitle = "2 products"
	sh.Columns = []Column{
		{Header: "SKU", Width: 20},
		{Header: "Product", Width: 38},
		{Header: "On hand", Width: 12},
		{Header: "List price", Width: 14},
		{Header: "Added", Width: 14},
	}
	sh.AddRow(Str("DBS-AGG-34CLEAR"), Str("Blu 60 Slab"), Num(800), Money(2200), Date(time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)))
	sh.AddRow(Str("00123"), Str("Border Stone"), Blank(), MoneyOr(nil), Blank())
	return wb
}

// -- the package ----------------------------------------------------------------------------------

func TestPackage_HasExactlyTheSixPartsAnXlsxNeeds(t *testing.T) {
	got := render(t, sampleWorkbook())
	want := []string{
		"[Content_Types].xml",
		"_rels/.rels",
		"xl/workbook.xml",
		"xl/_rels/workbook.xml.rels",
		"xl/styles.xml",
		"xl/worksheets/sheet1.xml",
	}
	for _, name := range want {
		if _, ok := got[name]; !ok {
			t.Errorf("the package is missing %s, Excel opens this as corrupt", name)
		}
	}
	if len(got) != len(want) {
		t.Errorf("unexpected parts: got %d entries, want %d (%v)", len(got), len(want), keys(got))
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestPackage_EveryPartIsWellFormedXML(t *testing.T) {
	for name, body := range render(t, sampleWorkbook()) {
		var v any
		if err := xml.Unmarshal([]byte(body), &v); err != nil {
			t.Errorf("%s is not well-formed XML: %v", name, err)
		}
		if !strings.HasPrefix(body, `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`) {
			t.Errorf("%s is missing the XML declaration", name)
		}
	}
}

// The Content_Types part is the one a hand-written package gets wrong: an Override missing for a
// worksheet produces a file that unzips fine and opens as corrupt.
func TestContentTypes_DeclaresEveryPartIncludingEverySheet(t *testing.T) {
	wb := New()
	a := wb.AddSheet("One")
	a.Columns = []Column{{Header: "A"}}
	b := wb.AddSheet("Two")
	b.Columns = []Column{{Header: "B"}}
	got := render(t, wb)

	ct := got["[Content_Types].xml"]
	for _, want := range []string{
		`PartName="/xl/workbook.xml"`,
		`PartName="/xl/styles.xml"`,
		`PartName="/xl/worksheets/sheet1.xml"`,
		`PartName="/xl/worksheets/sheet2.xml"`,
		`Extension="rels"`,
	} {
		if !strings.Contains(ct, want) {
			t.Errorf("[Content_Types].xml is missing %s", want)
		}
	}
	if _, ok := got["xl/worksheets/sheet2.xml"]; !ok {
		t.Fatal("the second sheet part was not written")
	}
	// The workbook relationships must name each sheet AND the styles part, with ids that match.
	rels := got["xl/_rels/workbook.xml.rels"]
	for _, want := range []string{
		`Id="rId1"`, `Target="worksheets/sheet1.xml"`,
		`Id="rId2"`, `Target="worksheets/sheet2.xml"`,
		`Id="rId3"`, `Target="styles.xml"`,
	} {
		if !strings.Contains(rels, want) {
			t.Errorf("xl/_rels/workbook.xml.rels is missing %s", want)
		}
	}
	if !strings.Contains(got["xl/workbook.xml"], `<sheet name="One" sheetId="1" r:id="rId1"/>`) {
		t.Errorf("workbook.xml does not name the first sheet: %s", got["xl/workbook.xml"])
	}
}

func TestPackage_ARelationshipExistsForEveryPartItPointsAt(t *testing.T) {
	got := render(t, sampleWorkbook())
	if !strings.Contains(got["_rels/.rels"], `Target="xl/workbook.xml"`) {
		t.Error("the package relationship does not point at the workbook part")
	}
	// Nothing may reference a theme part: this writer deliberately does not emit one, and a dangling
	// theme reference is exactly the kind of thing that opens with a repair prompt.
	for name, body := range got {
		if strings.Contains(body, "theme") {
			t.Errorf("%s references a theme part that this package does not ship", name)
		}
	}
}

// -- typing ---------------------------------------------------------------------------------------

func TestCells_CarryTheirTypeSoASKUStaysASKUAndAQuantitySums(t *testing.T) {
	got := render(t, sampleWorkbook())
	sheet := parseSheet(t, got["xl/worksheets/sheet1.xml"])

	// Title (row 1), subtitle (row 2), header (row 3), data from row 4.
	sku, ok := cellAt(sheet, "A4")
	if !ok {
		t.Fatal("no cell at A4")
	}
	if sku.Type != "inlineStr" || sku.Inline.Text != "DBS-AGG-34CLEAR" {
		t.Errorf("SKU cell is not an inline string: %+v", sku)
	}
	qty, ok := cellAt(sheet, "C4")
	if !ok {
		t.Fatal("no cell at C4")
	}
	if qty.Type != "" {
		t.Errorf("a quantity must have no t attribute (numeric is the default); got t=%q", qty.Type)
	}
	if qty.Value != "800" {
		t.Errorf("quantity value = %q, want 800", qty.Value)
	}
}

func TestMoney_IsANumberInCentsWithACurrencyFormatNeverAString(t *testing.T) {
	got := render(t, sampleWorkbook())
	sheet := parseSheet(t, got["xl/worksheets/sheet1.xml"])
	price, ok := cellAt(sheet, "D4")
	if !ok {
		t.Fatal("no cell at D4")
	}
	if price.Type != "" {
		t.Errorf("money must be numeric, not %q, a string price neither sums nor sorts", price.Type)
	}
	if price.Value != "22.00" {
		t.Errorf("money value = %q, want 22.00 (2200 cents rendered exactly, not via a float)", price.Value)
	}
	if price.Style != "2" {
		t.Errorf("money cell style = %q, want the currency style index 2", price.Style)
	}
	styles := got["xl/styles.xml"]
	if !strings.Contains(styles, `numFmtId="164"`) || !strings.Contains(styles, `#,##0.00`) {
		t.Errorf("styles.xml does not define the currency number format: %s", styles)
	}
}

func TestMoney_RendersCentsExactlyWithoutFloatDrift(t *testing.T) {
	cases := []struct {
		cents int64
		want  string
	}{
		{0, "0.00"},
		{5, "0.05"},
		{99, "0.99"},
		{100, "1.00"},
		{2200, "22.00"},
		{542400, "5424.00"},
		{1234567, "12345.67"},
		{-12345, "-123.45"},
		{-5, "-0.05"},
	}
	for _, c := range cases {
		if got := formatCents(c.cents); got != c.want {
			t.Errorf("formatCents(%d) = %q, want %q", c.cents, got, c.want)
		}
	}
}

func TestDate_IsASerialNumberWithADateFormat(t *testing.T) {
	got := render(t, sampleWorkbook())
	sheet := parseSheet(t, got["xl/worksheets/sheet1.xml"])
	d, ok := cellAt(sheet, "E4")
	if !ok {
		t.Fatal("no cell at E4")
	}
	if d.Type != "" {
		t.Errorf("a date must be numeric with a date format, got t=%q", d.Type)
	}
	// 2026-08-11 is 46245 days after the 1899-12-30 origin. Hard-coded rather than recomputed with
	// the same expression the code uses: a test that repeats the implementation proves nothing.
	if d.Value != "46245" {
		t.Errorf("date serial = %q, want 46245 for 2026-08-11", d.Value)
	}
	if d.Style != "3" {
		t.Errorf("date cell style = %q, want the date style index 3", d.Style)
	}
	if !strings.Contains(got["xl/styles.xml"], `yyyy\-mm\-dd`) {
		t.Error("styles.xml does not define the ISO date format")
	}
}

func TestDate_UsesTheDatesOwnLocationSoAnEveningTimestampIsNotYesterday(t *testing.T) {
	east := time.FixedZone("EDT", -4*3600)
	// 2026-08-11 00:30 in Toronto is 2026-08-11 04:30 UTC. Both must export as the 11th.
	local := time.Date(2026, 8, 11, 0, 30, 0, 0, east)
	if got, want := dateSerial(local), int64(46245); got != want {
		t.Errorf("dateSerial(%s) = %d, want %d", local, got, want)
	}
}

// -- the empty-cell rule --------------------------------------------------------------------------

func TestBlank_IsATrulyEmptyCellNotAZeroAndNotAnEmptyString(t *testing.T) {
	got := render(t, sampleWorkbook())
	body := got["xl/worksheets/sheet1.xml"]
	sheet := parseSheet(t, body)

	// Row 5 is the product with no ledger row and no price anchor: C5 (quantity) and D5 (money) and
	// E5 (date) must not exist AT ALL.
	for _, ref := range []string{"C5", "D5", "E5"} {
		if c, ok := cellAt(sheet, ref); ok {
			t.Errorf("%s exists (%+v), a value nobody has must be an EMPTY cell, not 0 and not \"\"", ref, c)
		}
	}
	// And specifically: no empty inline string anywhere. That is the construct that re-types a
	// numeric column as text on re-import.
	if strings.Contains(body, "<t></t>") || strings.Contains(body, "<t/>") {
		t.Error("the sheet contains an empty inline string, a zero-length string in a numeric column is what turns the column into text")
	}
	if strings.Contains(body, "<v></v>") || strings.Contains(body, "<v/>") {
		t.Error("the sheet contains an empty <v>, an empty numeric value is not a number")
	}
	// The row itself still exists, so the row after it is not pulled up.
	if _, ok := cellAt(sheet, "A5"); !ok {
		t.Error("the blank row lost its SKU, blanks must not collapse the row")
	}
}

func TestBlank_AnEmptyStringIsBlankAndNaNIsBlank(t *testing.T) {
	if Str("").Kind != KindBlank {
		t.Error(`Str("") must be a blank cell, not an empty inline string`)
	}
	if Num(nan()).Kind != KindBlank {
		t.Error("NaN must be blank, writing it produces a file Excel offers to repair")
	}
	if NumOr(nil).Kind != KindBlank {
		t.Error("NumOr(nil) must be blank")
	}
	if MoneyOr(nil).Kind != KindBlank {
		t.Error("MoneyOr(nil) must be blank, 'no price on file' is not $0.00")
	}
	if got := Money(0); got.Kind != KindMoney || got.Cents != 0 {
		t.Error("Money(0) is a REAL price of $0.00 and must be written")
	}
	if Date(time.Time{}).Kind != KindBlank {
		t.Error("the zero time must be blank, not 1899-12-30")
	}
}

func nan() float64 {
	zero := 0.0
	return zero / zero
}

// -- escaping -------------------------------------------------------------------------------------

func TestEscaping_AProductNameWithMarkupSurvivesIntactAndDoesNotBreakTheFile(t *testing.T) {
	const name = `Blu 60 <b>"Slab" & Co.</b>, 3'x2' <script>alert(1)</script>`
	wb := New()
	sh := wb.AddSheet("Catalogue")
	sh.Columns = []Column{{Header: "Product"}}
	sh.AddRow(Str(name))

	got := render(t, wb)
	body := got["xl/worksheets/sheet1.xml"]
	// The raw markup must NOT appear literally, if it does, the worksheet is no longer valid XML
	// and the whole workbook is unreadable.
	if strings.Contains(body, "<b>") || strings.Contains(body, "<script>") {
		t.Fatalf("unescaped markup leaked into the worksheet: %s", body)
	}
	if !strings.Contains(body, "&amp;") || !strings.Contains(body, "&lt;b&gt;") {
		t.Errorf("the markup was not escaped: %s", body)
	}
	// Round trip: what a reader parses back must be exactly what went in.
	sheet := parseSheet(t, body)
	c, ok := cellAt(sheet, "A2")
	if !ok {
		t.Fatal("no cell at A2")
	}
	if c.Inline.Text != name {
		t.Errorf("round trip changed the name:\n got %q\nwant %q", c.Inline.Text, name)
	}
}

func TestEscaping_DoesNotDoubleEscapeAnAmpersand(t *testing.T) {
	if got := escapeXML("Blu & Grey"); got != "Blu &amp; Grey" {
		t.Errorf("escapeXML = %q, want %q", got, "Blu &amp; Grey")
	}
	if got := escapeXML("a &amp; b"); got != "a &amp;amp; b" {
		// Escaping is applied ONCE, to raw text. Text that already contains an entity is text that
		// contains an ampersand, and must be escaped as one.
		t.Errorf("escapeXML = %q, want %q", got, "a &amp;amp; b")
	}
}

func TestEscaping_DropsCharactersThatAreIllegalInXML(t *testing.T) {
	// \x0b and \x1a arrive in product descriptions pasted out of supplier PDFs. They cannot be
	// represented in XML 1.0 at all, not even as &#11;, so they are dropped, not escaped.
	got := escapeXML("Blu\x0b60\x1aSlab")
	if strings.ContainsAny(got, "\x0b\x1a") || strings.Contains(got, "&#") {
		t.Errorf("illegal control characters survived: %q", got)
	}
	if got != "Blu60Slab" {
		t.Errorf("escapeXML = %q, want %q", got, "Blu60Slab")
	}
	// Tab/newline/carriage return are legal and kept.
	if got := escapeXML("a\tb\nc"); got != "a\tb\nc" {
		t.Errorf("legal whitespace was dropped: %q", got)
	}
}

func TestEscaping_PreservesSignificantWhitespaceInAString(t *testing.T) {
	wb := New()
	sh := wb.AddSheet("S")
	sh.Columns = []Column{{Header: "SKU"}}
	sh.AddRow(Str("Blu 60 "))
	body := render(t, wb)["xl/worksheets/sheet1.xml"]
	if !strings.Contains(body, `xml:space="preserve"`) {
		t.Errorf(`a trailing space needs xml:space="preserve" or "Blu 60 " and "Blu 60" become the same SKU: %s`, body)
	}
}

// -- sheet furniture ------------------------------------------------------------------------------

func TestHeaderRow_IsStyledAndFrozenBelowTheTitle(t *testing.T) {
	got := render(t, sampleWorkbook())
	body := got["xl/worksheets/sheet1.xml"]
	sheet := parseSheet(t, body)

	head, ok := cellAt(sheet, "A3")
	if !ok {
		t.Fatal("no header cell at A3 (title row 1, subtitle row 2, header row 3)")
	}
	if head.Inline.Text != "SKU" || head.Style != "1" {
		t.Errorf("header cell = %+v, want the bold/filled header style index 1", head)
	}
	// The freeze must split BELOW the header, so the title, the subtitle and the headers all stay
	// put while the rows scroll.
	if !strings.Contains(body, `<pane ySplit="3" topLeftCell="A4" activePane="bottomLeft" state="frozen"/>`) {
		t.Errorf("the header row is not frozen: %s", body)
	}
	if !strings.Contains(got["xl/styles.xml"], "<b/>") {
		t.Error("styles.xml defines no bold font for the header")
	}
	if !strings.Contains(got["xl/styles.xml"], `patternType="solid"`) {
		t.Error("styles.xml defines no header fill")
	}
}

func TestColumnWidths_AreWrittenWhenSet(t *testing.T) {
	body := render(t, sampleWorkbook())["xl/worksheets/sheet1.xml"]
	if !strings.Contains(body, `<col min="1" max="1" width="20" customWidth="1"/>`) {
		t.Errorf("column widths were not written: %s", body)
	}
	// A sheet with no widths must not emit an empty <cols/>, which is invalid.
	wb := New()
	sh := wb.AddSheet("S")
	sh.Columns = []Column{{Header: "A"}}
	if b := render(t, wb)["xl/worksheets/sheet1.xml"]; strings.Contains(b, "<cols>") {
		t.Errorf("an empty <cols> element was written: %s", b)
	}
}

func TestTitle_IsMergedAcrossTheColumnsSoItIsNotClippedAtColumnOne(t *testing.T) {
	body := render(t, sampleWorkbook())["xl/worksheets/sheet1.xml"]
	if !strings.Contains(body, `<mergeCell ref="A1:E1"/>`) {
		t.Errorf("the title is not merged across the five columns: %s", body)
	}
	if !strings.Contains(body, `<mergeCell ref="A2:E2"/>`) {
		t.Errorf("the subtitle is not merged: %s", body)
	}
	// mergeCells must follow sheetData, the schema fixes the element order and Excel repairs a file
	// that gets it wrong.
	if strings.Index(body, "<mergeCells") < strings.Index(body, "</sheetData>") {
		t.Error("mergeCells appears before sheetData ends; the schema requires it after")
	}
}

func TestSheetName_IsSanitisedToExcelsRules(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Catalogue", "Catalogue"},
		{"", "Sheet1"},
		{"A/B:C*D?E[F]G", "ABCDEFG"},
		{strings.Repeat("x", 40), strings.Repeat("x", 31)},
	}
	for _, c := range cases {
		if got := sanitiseSheetName(c.in); got != c.want {
			t.Errorf("sanitiseSheetName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestColName_CountsPastZ(t *testing.T) {
	cases := map[int]string{0: "A", 25: "Z", 26: "AA", 27: "AB", 51: "AZ", 52: "BA", 701: "ZZ", 702: "AAA"}
	for idx, want := range cases {
		if got := colName(idx); got != want {
			t.Errorf("colName(%d) = %q, want %q", idx, got, want)
		}
	}
}

func TestEmptyWorkbook_StillProducesAnOpenableFile(t *testing.T) {
	// The catalogue with no matching rows must still download a sheet with its headers: a button
	// that silently does nothing is indistinguishable from a broken one.
	wb := New()
	sh := wb.AddSheet("Catalogue")
	sh.Columns = catalogueColumns
	got := render(t, wb)
	sheet := parseSheet(t, got["xl/worksheets/sheet1.xml"])
	if len(sheet.Rows) != 1 {
		t.Fatalf("want exactly the header row, got %d rows", len(sheet.Rows))
	}
	if c, _ := cellAt(sheet, "A1"); c.Inline.Text != "SKU" {
		t.Errorf("the header row is missing from an empty export: %+v", c)
	}
}

func TestWorkbookWithNoSheetsGetsOneRatherThanEmittingAnInvalidPackage(t *testing.T) {
	got := render(t, New())
	if _, ok := got["xl/worksheets/sheet1.xml"]; !ok {
		t.Error("a workbook with no sheets must still write one, a sheetless package is invalid")
	}
}

func TestBytes_AreDeterministicAcrossRuns(t *testing.T) {
	a, err := sampleWorkbook().Bytes()
	if err != nil {
		t.Fatal(err)
	}
	b, err := sampleWorkbook().Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Error("two renders of the same workbook differ, a diff of two exports should show data changes, not clock changes")
	}
}

func TestSanitiseFilename_CannotInjectAHeader(t *testing.T) {
	got := sanitiseFilename("catalogue\r\nX-Evil: 1\".xlsx")
	if strings.ContainsAny(got, "\r\n\"") {
		t.Errorf("filename %q can break out of the Content-Disposition header", got)
	}
}

func TestPrint_TheSheetPrintsAsOneWidePageWithRepeatingHeaders(t *testing.T) {
	// A ten-column export printed portrait comes out as three narrow pages with the money columns
	// orphaned on the last one, and page 2 has no column names at all. Both are settings, not luck.
	got := render(t, sampleWorkbook())
	sheet := got["xl/worksheets/sheet1.xml"]
	if !strings.Contains(sheet, `<pageSetup orientation="landscape" fitToWidth="1" fitToHeight="0"`) {
		t.Errorf("the sheet is not set to print landscape, one page wide: %s", sheet)
	}
	if !strings.Contains(sheet, `<sheetPr><pageSetUpPr fitToPage="1"/></sheetPr>`) {
		t.Error("fitToPage is not enabled, so the fitToWidth setting is ignored")
	}
	// sheetPr must be the FIRST child of <worksheet>, and pageSetup among the last, the schema
	// fixes the order and Excel repairs a file that breaks it.
	if !strings.Contains(sheet, `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetPr>`) {
		t.Error("sheetPr is not the first child of worksheet")
	}
	if strings.Index(sheet, "<pageMargins") < strings.Index(sheet, "</sheetData>") {
		t.Error("pageMargins appears before sheetData ends; the schema requires it after")
	}
	// The header rows repeat on every printed page.
	wb := got["xl/workbook.xml"]
	if !strings.Contains(wb, `<definedName name="_xlnm.Print_Titles" localSheetId="0">'Catalogue'!$1:$3</definedName>`) {
		t.Errorf("no print titles: page 2 of a printed export would be a wall of numbers with no column names: %s", wb)
	}
	if strings.Index(wb, "<definedNames") < strings.Index(wb, "</sheets>") {
		t.Error("definedNames must follow <sheets>, the schema fixes that order")
	}
}
