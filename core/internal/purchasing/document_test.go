package purchasing

// document_test.go, "a PO renders identically in PDF and CSV from one source", held to.
//
// THE TEST IS FIELD FOR FIELD, BY NAME. It parses the rendered print page's data-field attributes
// and the CSV's own header row, and compares the two maps cell by cell. Comparing by POSITION would
// pass a document whose columns had been reordered in one encoder; comparing by name will not.
//
// It also asserts the two never diverge STRUCTURALLY: both encoders read PODocument.table() and
// neither one formats a cell, proved by a source scan with comments stripped. Two encoders that
// agree today because somebody kept them in step by hand is exactly the arrangement the acceptance
// clause forbids, and it is exactly the arrangement that drifts.

import (
	"encoding/csv"
	"html"
	"regexp"
	"strings"
	"testing"
	"time"
)

// - the fixture -

// docFixture is one order that exercises every mapping arm at once: a fully mapped line, a
// code-only line, a unit-only line, an unmapped line, and a line whose quantity does not divide into
// the vendor's unit. Anything less would let a whole arm rot behind a passing test.
func docFixture(showMoney bool) PODocument {
	return PODocument{
		PONo: "#PO-TRT-4", StatusWord: "Ordered",
		IssuedOn: "2026-08-03", ExpectedOn: "2026-08-21", Currency: "CAD",
		BuyerName: "Dibbits Landscape Supply", BuyerBranch: "Trenton", BuyerCode: "TRT",
		BuyerAddress: "132 County Road 40, Trenton, ON, K8V 5P7",
		VendorName:   "Unilock", VendorNo: "V-UNILOCK",
		ContactTagged: true, ContactName: "Orders desk", ContactEmail: "orders@unilock.example",
		Notes:     "Deliver to the north gate & call ahead",
		ShowMoney: showMoney, SubtotalCents: 778811,
		PrintedAt: time.Date(2026, 8, 3, 9, 15, 0, 0, time.UTC),
		Lines: []PODocumentLine{
			{LineNo: 1, ItemCode: "UNI-9911", CodeFromVendor: true, Description: "Beacon Hill Flagstone",
				OrderQty: 2, OrderUOM: "SKID", UnitFromVendor: true, BaseQty: 120, BaseUOM: "PC",
				UnitCostCents: 5511, LineTotalCents: 661320},
			{LineNo: 2, ItemCode: "TEC-7742", CodeFromVendor: true, Description: "Blu 60 Smooth",
				OrderQty: 40, OrderUOM: "PC", BaseQty: 40, BaseUOM: "PC",
				UnitCostCents: 2937, LineTotalCents: 117480},
			{LineNo: 3, ItemCode: "BAN-LEDGE-3", Description: "Ledgestone corner",
				OrderQty: 6, OrderUOM: "PLT", UnitFromVendor: true, BaseQty: 360, BaseUOM: "PC",
				UnitCostCents: 1, LineTotalCents: 360},
			{LineNo: 4, ItemCode: "OAK-STEP-72", Description: "Step 72\" & riser",
				OrderQty: 3.5, OrderUOM: "EA", BaseQty: 3.5, BaseUOM: "EA",
				UnitCostCents: 21900, LineTotalCents: 76650},
		},
	}
}

// - parsing what each encoder actually produced -

var htmlCellRe = regexp.MustCompile(`<t[dd]\s+data-field="([a-z_]+)"[^>]*>(.*?)</td>`)
var htmlSummaryRe = regexp.MustCompile(`<dd data-field="([a-z_]+)">(.*?)</dd>`)
var htmlTotalRe = regexp.MustCompile(`<span data-field="([a-z_]+)">(.*?)</span>`)

// htmlRows returns the print page's line cells as one map per row, keyed by field name.
func htmlRows(t *testing.T, page string) []map[string]string {
	t.Helper()
	body := page
	if i := strings.Index(body, "<tbody>"); i >= 0 {
		body = body[i:]
	}
	if j := strings.Index(body, "</tbody>"); j >= 0 {
		body = body[:j]
	}
	var out []map[string]string
	for _, tr := range strings.Split(body, "<tr>")[1:] {
		row := map[string]string{}
		for _, m := range htmlCellRe.FindAllStringSubmatch(tr, -1) {
			row[m[1]] = html.UnescapeString(m[2])
		}
		if len(row) > 0 {
			out = append(out, row)
		}
	}
	return out
}

// csvRows returns the attachment's line rows as one map per row, keyed by the field NAME the spec
// declares, matched to the CSV through the shared header row, which is the only thing the CSV
// carries. That is the point: if the two encoders disagreed about which heading a column has, this
// mapping would fail before any cell was compared.
func csvRows(t *testing.T, blob []byte, names, headers []string) []map[string]string {
	t.Helper()
	r := csv.NewReader(strings.NewReader(string(blob)))
	r.FieldsPerRecord = -1
	recs, err := r.ReadAll()
	if err != nil {
		t.Fatalf("the CSV does not parse: %v", err)
	}
	headerAt := -1
	for i, rec := range recs {
		if len(rec) == len(headers) && rec[0] == headers[0] && rec[len(rec)-1] == headers[len(headers)-1] {
			headerAt = i
			break
		}
	}
	if headerAt < 0 {
		t.Fatalf("the CSV carries no header row matching the field spec %v:\n%s", headers, blob)
	}
	var out []map[string]string
	for _, rec := range recs[headerAt+1:] {
		if len(rec) != len(names) {
			break // the totals block, which is a name/value pair and not a line
		}
		row := map[string]string{}
		for i, n := range names {
			row[n] = rec[i]
		}
		out = append(out, row)
	}
	return out
}

// - 1 · the acceptance clause -

func TestPODocument_ThePrintPageAndTheCSVAgreeFieldForField(t *testing.T) {
	doc := docFixture(true)
	tbl := doc.table()
	page, err := RenderPODocumentHTML(doc)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	blob, err := EncodePODocumentCSV(doc)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	hRows := htmlRows(t, page)
	cRows := csvRows(t, blob, tbl.Names, tbl.Headers)

	// GUARD THE GUARD, and it is the whole test's foundation: an extraction that silently found
	// nothing would make every comparison below pass. Both sides must have produced every line, and
	// every field of the spec must have been compared.
	if len(tbl.Names) < 8 {
		t.Fatalf("the field spec has shrunk to %d columns, this is comparing almost nothing", len(tbl.Names))
	}
	if len(hRows) != len(doc.Lines) {
		t.Fatalf("the print page rendered %d line rows, the order has %d", len(hRows), len(doc.Lines))
	}
	if len(cRows) != len(doc.Lines) {
		t.Fatalf("the CSV rendered %d line rows, the order has %d", len(cRows), len(doc.Lines))
	}

	compared := 0
	for i := range doc.Lines {
		for _, name := range tbl.Names {
			h, okH := hRows[i][name]
			c, okC := cRows[i][name]
			if !okH {
				t.Fatalf("line %d: the print page has no %q cell", i+1, name)
			}
			if !okC {
				t.Fatalf("line %d: the CSV has no %q cell", i+1, name)
			}
			if h != c {
				t.Errorf("line %d field %q: print page %q, CSV %q, the two renderings must be the same document",
					i+1, name, h, c)
			}
			compared++
		}
	}
	if compared != len(doc.Lines)*len(tbl.Names) {
		t.Fatalf("compared %d cells, expected %d", compared, len(doc.Lines)*len(tbl.Names))
	}

	// The document HEAD agrees too, the vendor, the dates, the desk it is aimed at and the notes.
	hSum := map[string]string{}
	for _, m := range htmlSummaryRe.FindAllStringSubmatch(page, -1) {
		hSum[m[1]] = html.UnescapeString(m[2])
	}
	csvPairs := csvSummary(t, blob)
	if len(tbl.Summary) < 6 {
		t.Fatalf("the summary has shrunk to %d facts", len(tbl.Summary))
	}
	for _, p := range tbl.Summary {
		if hSum[p.Name] != p.Value {
			t.Errorf("summary %q: print page %q, source %q", p.Name, hSum[p.Name], p.Value)
		}
		if csvPairs[p.Label] != p.Value {
			t.Errorf("summary %q: CSV %q, source %q", p.Name, csvPairs[p.Label], p.Value)
		}
	}

	// The TOTAL is one fact and both encoders carry it.
	hTot := map[string]string{}
	for _, m := range htmlTotalRe.FindAllStringSubmatch(page, -1) {
		hTot[m[1]] = html.UnescapeString(m[2])
	}
	if len(tbl.Totals) != 1 {
		t.Fatalf("the order total is no longer one fact: %v", tbl.Totals)
	}
	if hTot["subtotal"] != tbl.Totals[0].Value || csvPairs[tbl.Totals[0].Label] != tbl.Totals[0].Value {
		t.Errorf("the order total disagrees: print %q, CSV %q, source %q",
			hTot["subtotal"], csvPairs[tbl.Totals[0].Label], tbl.Totals[0].Value)
	}
}

// csvSummary reads the name/value block at the head of the attachment.
func csvSummary(t *testing.T, blob []byte) map[string]string {
	t.Helper()
	r := csv.NewReader(strings.NewReader(string(blob)))
	r.FieldsPerRecord = -1
	recs, err := r.ReadAll()
	if err != nil {
		t.Fatalf("the CSV does not parse: %v", err)
	}
	out := map[string]string{}
	for _, rec := range recs {
		if len(rec) == 2 {
			out[rec[0]] = rec[1]
		}
	}
	return out
}

// - 2 · the gate reaches the document -

func TestPODocument_ARefusedReaderGetsADocumentWithNoMoneyInIt(t *testing.T) {
	// The positive control first, so the absence below cannot be an encoder that renders nothing.
	withMoney, err := RenderPODocumentHTML(docFixture(true))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	withMoneyCSV, err := EncodePODocumentCSV(docFixture(true))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	for _, want := range []string{"55.11", "6613.20", "7788.11", "Unit cost (CAD)"} {
		if !strings.Contains(withMoney, want) || !strings.Contains(string(withMoneyCSV), want) {
			t.Fatalf("the ALLOWED document is missing %q, every absence below would be vacuous", want)
		}
	}

	page, err := RenderPODocumentHTML(docFixture(false))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	blob, err := EncodePODocumentCSV(docFixture(false))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	for _, forbidden := range []string{"55.11", "6613.20", "1174.80", "219.00", "7788.11",
		"Unit cost", "Line total", "Order total", "unit_cost", "line_total", "subtotal"} {
		if strings.Contains(page, forbidden) {
			t.Errorf("the refused reader's print page carries %q", forbidden)
		}
		if strings.Contains(string(blob), forbidden) {
			t.Errorf("the refused reader's CSV carries %q", forbidden)
		}
	}
	// THE COLUMN IS GONE, NOT BLANK. A blank money column still tells the reader there is a number
	// they are not being shown, and a CSV column of empties gets summed to zero by somebody.
	if strings.Contains(page, `data-field="unit_cost"`) {
		t.Error("the money column is present and empty rather than absent")
	}
	// The ORDER still renders in full. Removing money must not remove the purchase order.
	for _, want := range []string{"#PO-TRT-4", "Beacon Hill Flagstone", "UNI-9911", "Unilock"} {
		if !strings.Contains(page, want) || !strings.Contains(string(blob), want) {
			t.Errorf("the refused reader lost %q, the gate removes the money and nothing else", want)
		}
	}
	// And the two still agree with each other with the shorter spec.
	doc := docFixture(false)
	tbl := doc.table()
	hRows, cRows := htmlRows(t, page), csvRows(t, blob, tbl.Names, tbl.Headers)
	if len(hRows) != len(doc.Lines) || len(cRows) != len(doc.Lines) {
		t.Fatalf("gated: print %d rows, CSV %d rows, order %d lines", len(hRows), len(cRows), len(doc.Lines))
	}
	for i := range doc.Lines {
		for _, name := range tbl.Names {
			if hRows[i][name] != cRows[i][name] {
				t.Errorf("gated line %d field %q: print %q, CSV %q", i+1, name, hRows[i][name], cRows[i][name])
			}
		}
	}
}

// - 3 · the vendor's vocabulary, and the honest degrade -

func TestPODocument_TheMappingColumnNamesWhoseVocabularyEachLineSpeaks(t *testing.T) {
	doc := docFixture(true)
	rows := doc.table().Rows
	names := doc.table().Names
	at := indexOfStr(names, "mapping")
	if at < 0 {
		t.Fatal("the document no longer says whose codes it is using")
	}
	want := []string{"Vendor code and unit", "Vendor code", "Vendor unit", "Our catalogue"}
	for i, w := range want {
		if rows[i][at] != w {
			t.Errorf("line %d mapping = %q, want %q", i+1, rows[i][at], w)
		}
	}
}

func TestDocumentLine_DegradesToOurCatalogueInTheOpen(t *testing.T) {
	line := POLine{LineNo: 1, ProductID: "p-1", SKU: "UNI-BEA-GRA", Description: "Beacon Hill",
		QtyOrderedBase: 120, UOM: "PC", UnitCostCents: 5511, LineTotalCents: 661320}

	unmapped := documentLine(line, VendorSKU{})
	if unmapped.ItemCode != "UNI-BEA-GRA" || unmapped.CodeFromVendor {
		t.Fatalf("an unmapped line must carry OUR code and say so: %+v", unmapped)
	}
	if unmapped.OrderQty != 120 || unmapped.OrderUOM != "PC" || unmapped.UnitFromVendor {
		t.Fatalf("an unmapped line must be ordered in our base unit and say so: %+v", unmapped)
	}

	mapped := documentLine(line, VendorSKU{VendorSKU: "UNI-9911", VendorUOM: "SKID", QtyPerVendorUOM: 60})
	if mapped.ItemCode != "UNI-9911" || !mapped.CodeFromVendor {
		t.Fatalf("a mapped line must carry the vendor's code: %+v", mapped)
	}
	if mapped.OrderQty != 2 || mapped.OrderUOM != "SKID" || !mapped.UnitFromVendor {
		t.Fatalf("120 PC at 60 per skid is 2 skids: %+v", mapped)
	}
	// THE BASE PAIR IS ALWAYS OURS. It is what the receiving clerk counts off the truck.
	if mapped.BaseQty != 120 || mapped.BaseUOM != "PC" {
		t.Fatalf("the base pair was translated away: %+v", mapped)
	}
}

// THE REFUSAL THAT MATTERS. A quantity that does not land on a whole vendor unit is NOT expressed in
// that unit: "1.6667 skids" is a purchase order the vendor guesses at, and they will send one or two.
func TestVendorUnitFor_RefusesAQuantityThatDoesNotDivide(t *testing.T) {
	m := VendorSKU{VendorUOM: "SKID", QtyPerVendorUOM: 60}
	if q, uom, ok := vendorUnitFor(120, m); !ok || q != 2 || uom != "SKID" {
		t.Fatalf("120/60 = %v %q (ok=%v), want 2 SKID", q, uom, ok)
	}
	if _, _, ok := vendorUnitFor(100, m); ok {
		t.Fatal("100 PC at 60 per skid was expressed as 1.6667 skids, the vendor would guess")
	}
	if _, _, ok := vendorUnitFor(30, m); ok {
		t.Fatal("half a skid was expressed as a whole one")
	}
	// A DECIMAL(19,4) round trip must not read as an inexact division.
	if _, _, ok := vendorUnitFor(119.99999999, m); !ok {
		t.Fatal("float noise defeated an exact two skids")
	}
	if _, _, ok := vendorUnitFor(120, VendorSKU{VendorUOM: "SKID"}); ok {
		t.Fatal("a unit with no factor converted something")
	}
	if _, _, ok := vendorUnitFor(120, VendorSKU{QtyPerVendorUOM: 60}); ok {
		t.Fatal("a factor with no unit named a unit")
	}
}

func TestStatusWord_HasADefaultArmThatLowercasesRatherThanInventing(t *testing.T) {
	if got := StatusWord(StatusPartial); got != "Part received" {
		t.Fatalf("PARTIAL = %q", got)
	}
	if got := StatusWord("SOMETHING_NEW"); got != "something new" {
		t.Fatalf("an unknown state = %q, want the lowercased enum and never an invented phrase", got)
	}
}

func TestPODocument_FileNameIsTheOrderNumber(t *testing.T) {
	if got := (PODocument{PONo: "#PO-TRT-4"}).FileName(); got != "PO-TRT-4.csv" {
		t.Fatalf("file name = %q", got)
	}
	if got := (PODocument{}).FileName(); got != "purchase-order.csv" {
		t.Fatalf("an unnumbered order = %q, want a name and never an empty one", got)
	}
}

// - 4 · the two encoders are structurally one source -

func TestPODocument_NeitherEncoderFormatsACell(t *testing.T) {
	src := shippedSource(t, "document.go")

	// GUARD THE GUARD: the file is being read and the functions are still called what this asserts.
	for _, fn := range []string{"func RenderPODocumentHTML", "func EncodePODocumentCSV", "func (d PODocument) table()"} {
		if !strings.Contains(src, fn) {
			t.Fatalf("%s is gone, this gate is asleep", fn)
		}
	}

	// The field spec is consulted in EXACTLY ONE place: table(). Two consultations is two column
	// lists, which is the drift the acceptance clause forbids.
	if n := strings.Count(src, "poDocumentFields("); n != 2 {
		t.Fatalf("poDocumentFields appears %d times (its declaration plus one call, expected 2), a second caller is a second column list", n)
	}

	// Both encoders read the same table, and neither one calls a cell formatter itself.
	for _, fn := range []string{"RenderPODocumentHTML", "EncodePODocumentCSV"} {
		body := funcBody(t, src, fn)
		if !strings.Contains(body, "table()") {
			t.Errorf("%s does not read PODocument.table(), it is formatting its own document", fn)
		}
		for _, formatter := range []string{"docMoney(", "docQty(", "mappingWord("} {
			if strings.Contains(body, formatter) {
				t.Errorf("%s calls %s itself, a cell must be formatted once, in the shared spec", fn, formatter)
			}
		}
	}
	// The template is html/template and never text/template: every value on this page is text
	// somebody typed, landing inside markup.
	if !strings.Contains(src, `"html/template"`) || strings.Contains(src, `"text/template"`) {
		t.Error("the print page is no longer rendered through html/template")
	}
}

// funcBody returns the source of one top-level function, from its `func` line to the closing brace
// at column zero. Crude on purpose: it needs to be obvious what it selected when a test fails.
func funcBody(t *testing.T, src, name string) string {
	t.Helper()
	start := strings.Index(src, "func "+name+"(")
	if start < 0 {
		t.Fatalf("no function %s", name)
	}
	rest := src[start:]
	if end := strings.Index(rest, "\n}\n"); end >= 0 {
		return rest[:end]
	}
	return rest
}

func indexOfStr(xs []string, want string) int {
	for i, x := range xs {
		if x == want {
			return i
		}
	}
	return -1
}
