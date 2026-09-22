package pricing

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// xlsx_reader_test.go, the OOXML reader, against workbooks BUILT BY HAND rather than by our own
// writer.
//
// WHY NOT internal/export. It would be the convenient fixture source and it would test the wrong
// thing: internal/export writes inline strings and no sharedStrings table (its header says so and
// says why), which is precisely the dialect a real manufacturer file does NOT use. A reader tested
// only against our own writer is a reader tested against half the format. Both dialects are built
// below, by hand, from the bytes up, which also means every edge this reader claims to handle is
// visible in the fixture rather than asserted about somebody else's output.

// zipOf builds a .xlsx package from part name → content.
func zipOf(t *testing.T, parts map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range parts {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// sharedStringWorkbook is the Excel/LibreOffice dialect, with four things that have each broken a
// naive reader:
//
//	· the FIRST TAB is sheet2.xml (a deleted or reordered tab), so a reader that grabs sheet1.xml
//	  reads the wrong data, and sheet1.xml here holds obviously wrong data, so it would be caught.
//	· a SPARSE ROW: row 3 has no B cell, so a positional reader shifts the retail into the cost column.
//	· a RUN-SPLIT shared string (<r><t>DBS-</t></r><r><t>TB-SPLIT</t></r>) plus an <rPh> phonetic
//	  guide, which also contains a <t> and is not part of the value.
//	· a formula string (t="str") and an error cell (t="e").
func sharedStringWorkbook(t *testing.T) []byte {
	t.Helper()
	return zipOf(t, map[string]string{
		"[Content_Types].xml": `<?xml version="1.0"?><Types/>`,
		"xl/workbook.xml": `<?xml version="1.0"?>
<workbook xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <sheets><sheet name="Prices" sheetId="7" r:id="rId9"/><sheet name="Notes" sheetId="1" r:id="rId1"/></sheets>
</workbook>`,
		"xl/_rels/workbook.xml.rels": `<?xml version="1.0"?>
<Relationships>
  <Relationship Id="rId1" Target="worksheets/sheet1.xml"/>
  <Relationship Id="rId9" Target="worksheets/sheet2.xml"/>
</Relationships>`,
		"xl/sharedStrings.xml": `<?xml version="1.0"?>
<sst count="6">
  <si><t>sku</t></si>
  <si><t>cost</t></si>
  <si><t>retail</t></si>
  <si><t>DBS-TB-BLU60</t></si>
  <si><r><t>DBS-</t></r><r><t>TB-SPLIT</t></r><rPh sb="0" eb="3"><t>IGNORED</t></rPh></si>
  <si><t>DBS-TB-SPARSE</t></si>
</sst>`,
		// The wrong tab, with values that would be unmistakable if they leaked into the assertions.
		"xl/worksheets/sheet1.xml": `<?xml version="1.0"?>
<worksheet><sheetData>
  <row r="1"><c r="A1" t="inlineStr"><is><t>WRONG-TAB</t></is></c></row>
</sheetData></worksheet>`,
		"xl/worksheets/sheet2.xml": `<?xml version="1.0"?>
<worksheet><sheetData>
  <row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c><c r="C1" t="s"><v>2</v></c></row>
  <row r="2"><c r="A2" t="s"><v>3</v></c><c r="B2"><v>10.5</v></c><c r="C2"><v>24</v></c></row>
  <row r="3"><c r="A3" t="s"><v>5</v></c><c r="C3"><v>19.5</v></c></row>
  <row r="4"><c r="A4" t="s"><v>4</v></c><c r="B4" t="str"><v>8.25</v></c><c r="C4" t="e"><v>#N/A</v></c></row>
</sheetData></worksheet>`,
	})
}

func TestReadXLSXSharedStringDialect(t *testing.T) {
	rows, err := readXLSX(sharedStringWorkbook(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("read %d rows, want 4: %+v", len(rows), rows)
	}
	if rows[0][0] != "sku" || rows[0][1] != "cost" || rows[0][2] != "retail" {
		t.Fatalf("header row = %+v, the wrong tab, or the shared strings did not resolve", rows[0])
	}
	if rows[1][0] != "DBS-TB-BLU60" || rows[1][1] != "10.5" || rows[1][2] != "24" {
		t.Fatalf("row 2 = %+v", rows[1])
	}
	// THE SPARSE ROW. B is missing, so C must still land in index 2, a shifted value here would
	// import the retail price as the cost.
	if len(rows[2]) != 3 || rows[2][0] != "DBS-TB-SPARSE" || rows[2][1] != "" || rows[2][2] != "19.5" {
		t.Fatalf("sparse row = %+v, want the C value to stay in column C", rows[2])
	}
	// The run-split string is CONCATENATED and the phonetic guide is not part of it.
	if rows[3][0] != "DBS-TB-SPLIT" {
		t.Fatalf("run-split shared string = %q, want the runs joined and <rPh> dropped", rows[3][0])
	}
	if rows[3][1] != "8.25" {
		t.Fatalf("formula string cell = %q", rows[3][1])
	}
	// A formula ERROR is not a value. Importing the text "#N/A" as a price is worse than a blank.
	if rows[3][2] != "" {
		t.Fatalf("error cell = %q, want empty", rows[3][2])
	}
	// And the wrong tab really was there to be read, or "we read the right tab" proves nothing.
	if strings.Contains(strings.Join(rows[0], "|"), "WRONG-TAB") {
		t.Fatal("the reader took sheet1.xml, the workbook's first tab is sheet2.xml")
	}
}

// The dialect internal/export writes: inline strings, no shared table, no workbook rels to follow.
func TestReadXLSXInlineStringDialectAndTheSheetFallback(t *testing.T) {
	data := zipOf(t, map[string]string{
		"xl/worksheets/sheet1.xml": `<?xml version="1.0"?>
<worksheet><sheetData>
  <row r="1"><c r="A1" t="inlineStr"><is><t>sku</t></is></c><c r="B1" t="inlineStr"><is><t>cost</t></is></c></row>
  <row r="2"><c r="A2" t="inlineStr"><is><t>DBS-UL-BRUSSELS</t></is></c><c r="B2"><v>7.25</v></c></row>
</sheetData></worksheet>`,
	})
	rows, err := readXLSX(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[1][0] != "DBS-UL-BRUSSELS" || rows[1][1] != "7.25" {
		t.Fatalf("inline-string workbook = %+v", rows)
	}
}

func TestReadXLSXRefusesWhatIsNotAWorkbook(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"a CSV", "sku,cost\nA,1\n"},
		{"plain text", "this is not a spreadsheet"},
		{"an empty file", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := readXLSX([]byte(tc.body)); !errors.Is(err, errNotXLSX) {
				t.Fatalf("readXLSX(%s) = %v, want errNotXLSX", tc.name, err)
			}
		})
	}
	// A zip that is a zip and carries no sheet is also not a workbook.
	if _, err := readXLSX(zipOf(t, map[string]string{"hello.txt": "hi"})); !errors.Is(err, errNotXLSX) {
		t.Fatal("a zip with no worksheet must be refused")
	}
	// GUARD THE GUARD: a real workbook must NOT be refused, or the four assertions above are just a
	// function that always errors.
	if _, err := readXLSX(sharedStringWorkbook(t)); err != nil {
		t.Fatalf("a valid workbook was refused: %v", err)
	}
}

func TestColIndexOf(t *testing.T) {
	cases := map[string]int{"A1": 0, "B7": 1, "Z100": 25, "AA12": 26, "AB1": 27, "XFD1": 16383, "a3": 0}
	for ref, want := range cases {
		if got := colIndexOf(ref); got != want {
			t.Fatalf("colIndexOf(%q) = %d, want %d", ref, got, want)
		}
	}
	for _, bad := range []string{"", "7", "ABCD1"} {
		if got := colIndexOf(bad); got != -1 {
			t.Fatalf("colIndexOf(%q) = %d, want -1", bad, got)
		}
	}
}

// - the whole way through: an .xlsx uploaded to the import -

func TestPreviewImportsAnXLSXEndToEnd(t *testing.T) {
	h := newPFHarness(t)
	anchor := int64(1000)
	h.stock("DBS-TB-BLU60", "Blu 60 Smooth", &anchor)
	h.stock("DBS-TB-SPARSE", "Sparse Slab", nil)
	h.stock("DBS-TB-SPLIT", "Split Name", nil)

	pv, err := h.svc.PreviewPriceFile(context.Background(), PriceFileInput{
		VendorID: vendorTecho, UserID: "mgr", FileName: "techo.xlsx", Data: sharedStringWorkbook(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	if pv.Format != FormatXLSX {
		t.Fatalf("format = %q, want XLSX", pv.Format)
	}
	if want := []string{"sku", "cost", "retail"}; strings.Join(pv.Headers, ",") != strings.Join(want, ",") {
		t.Fatalf("headers = %v, want %v", pv.Headers, want)
	}
	if pv.Counts.Rows != 3 {
		t.Fatalf("counts = %+v, want 3 rows", pv.Counts)
	}
	byS := map[string]PriceFileLine{}
	for _, l := range pv.Lines {
		byS[l.SKU] = l
	}
	// 10.5 → 1050 cents → ×1.85 → 1943 (round half up), the same arithmetic the CSV path uses.
	if got := byS["DBS-TB-BLU60"].NewRetailCents; got == nil || *got != 1943 {
		t.Fatalf("BLU60 derived retail = %v, want 1943", got)
	}
	// The sparse row has NO cost, so it derives from the manufacturer retail column, which is only
	// true if the missing B cell did not shift C into B.
	sparse := byS["DBS-TB-SPARSE"]
	if sparse.DerivedFrom != DeriveMfrRetail || sparse.NewRetailCents == nil || *sparse.NewRetailCents != 1950 {
		t.Fatalf("sparse row = %+v, want the retail column read from column C", sparse)
	}
}

// A workbook whose extension lies. The format is decided by the bytes, so a CSV named .xlsx imports
// rather than producing a parse error about a format nobody chose.
func TestAFileWhoseExtensionLiesStillImports(t *testing.T) {
	h := newPFHarness(t)
	h.stock("DBS-TB-BLU60", "Blu 60 Smooth", nil)
	pv, err := h.svc.PreviewPriceFile(context.Background(), PriceFileInput{
		VendorID: vendorTecho, UserID: "mgr", FileName: "definitely.xlsx",
		Data: []byte("sku,cost\nDBS-TB-BLU60,10.00\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if pv.Format != FormatCSV || pv.Counts.Rows != 1 {
		t.Fatalf("format=%q counts=%+v, a CSV named .xlsx must still import", pv.Format, pv.Counts)
	}
}

// A UTF-8 BOM is what Excel writes on every CSV export, and left in place it turns the first header
// into something no mapping matches, a file where every row is unpriced for an invisible reason.
func TestACSVWithAByteOrderMarkStillMapsItsFirstColumn(t *testing.T) {
	h := newPFHarness(t)
	anchor := int64(1000)
	h.stock("DBS-TB-BLU60", "Blu 60 Smooth", &anchor)
	data := append([]byte{0xEF, 0xBB, 0xBF}, []byte("sku,cost\nDBS-TB-BLU60,10.00\n")...)
	pv, err := h.svc.PreviewPriceFile(context.Background(), PriceFileInput{
		VendorID: vendorTecho, UserID: "mgr", FileName: "excel.csv", Data: data,
	})
	if err != nil {
		t.Fatal(err)
	}
	if pv.Headers[0] != "sku" {
		t.Fatalf("first header = %q, the byte-order mark survived", pv.Headers[0])
	}
	if pv.Counts.Changed != 1 || pv.Counts.Unpriced != 0 {
		t.Fatalf("counts = %+v, want the row priced", pv.Counts)
	}
}
