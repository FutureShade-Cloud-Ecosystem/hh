package export

import (
	"encoding/csv"
	"encoding/xml"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// libreoffice_test.go, the round trip that proves the file is a REAL .xlsx.
//
// Every other test in this package checks the bytes this package writes. That is necessary and not
// sufficient: a hand-written OOXML package can satisfy every assertion about its own XML and still
// make Excel offer to repair it. The only honest proof is to hand the file to a spreadsheet and read
// back what it understood, so these tests run LibreOffice headless over the produced workbook and
// assert on ITS output.
//
// TWO CONVERSIONS, because they answer different questions:
//
//	→ .csv  with "save cell contents as shown": what a human SEES. $22.00 in the price column proves
//	        the cell is a NUMBER carrying a currency format, a text cell is not formatted, so a
//	        price written as a string comes back as bare text and this test fails.
//	→ .fods (flat ODF): what the spreadsheet THINKS each cell IS. office:value-type is LibreOffice's
//	        own verdict on the type, float, currency-formatted float, date or string. There is no
//	        way to fake it from the writing side.
//
// The tests SKIP when LibreOffice is absent (a developer laptop without it should not fail the
// suite) and under -short. Set HOS_XLSX_REQUIRE_LIBREOFFICE=1 to turn the skip into a failure, which
// is what CI should do the day it grows a LibreOffice image.

func libreOfficeBin(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"soffice", "libreoffice"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	if os.Getenv("HOS_XLSX_REQUIRE_LIBREOFFICE") == "1" {
		t.Fatal("LibreOffice is required here (HOS_XLSX_REQUIRE_LIBREOFFICE=1) and was not found")
	}
	t.Skip("LibreOffice not installed, skipping the round-trip proof")
	return ""
}

// convert runs a headless conversion and returns the produced file's contents. It fails on a
// non-zero exit AND on any warning in the output: "opens warning-free" is the acceptance bar, so a
// conversion that succeeds while complaining is not a pass.
func convert(t *testing.T, data []byte, filter, ext string) string {
	t.Helper()
	bin := libreOfficeBin(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "export.xlsx")
	if err := os.WriteFile(src, data, 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	// A private user profile per run: LibreOffice refuses to start twice against the same one, and
	// `go test` runs packages in parallel.
	profile := "-env:UserInstallation=file://" + filepath.Join(dir, "profile")
	cmd := exec.Command(bin, profile, "--headless", "--norestore", "--convert-to", filter, "--outdir", out, src)
	cmd.Env = append(os.Environ(), "HOME="+dir)
	combined, err := cmd.CombinedOutput()
	text := string(combined)
	if err != nil {
		t.Fatalf("LibreOffice could not open the workbook: %v\n%s", err, text)
	}
	lower := strings.ToLower(text)
	for _, bad := range []string{"error", "warning", "repair", "corrupt"} {
		if strings.Contains(lower, bad) {
			t.Fatalf("LibreOffice reported %q while opening the workbook:\n%s", bad, text)
		}
	}
	body, err := os.ReadFile(filepath.Join(out, "export."+ext))
	if err != nil {
		t.Fatalf("LibreOffice produced no output file: %v\n%s", err, text)
	}
	return string(body)
}

// roundTripCSV converts the workbook to CSV as a human sees it.
func roundTripCSV(t *testing.T, data []byte) [][]string {
	t.Helper()
	// Filter options: comma, double quote, UTF-8 (76), first line 1, no format codes, language 0,
	// quoted-field-as-text false, detect-special-numbers true, SAVE CELL CONTENTS AS SHOWN true.
	const filter = `csv:Text - txt - csv (StarCalc):44,34,76,1,,0,false,true,true`
	body := convert(t, data, filter, "csv")
	r := csv.NewReader(strings.NewReader(body))
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("the CSV LibreOffice produced is unreadable: %v\n%s", err, body)
	}
	return rows
}

// roundTripTypes converts to flat ODF and returns, per data row, the value-type LibreOffice assigned
// to each of the ten catalogue columns. "" means the cell is empty.
func roundTripTypes(t *testing.T, data []byte) [][]string {
	t.Helper()
	body := convert(t, data, "fods", "fods")
	dec := xml.NewDecoder(strings.NewReader(body))
	var rows [][]string
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "table-row" {
			continue
		}
		var row []string
		depth := 1
		for depth > 0 {
			tok, err := dec.Token()
			if err != nil {
				break
			}
			switch el := tok.(type) {
			case xml.StartElement:
				if el.Name.Local == "table-cell" {
					// The type attribute lives in the ODF office namespace; read it by local name so
					// the test does not depend on which of the two office namespaces LibreOffice used.
					kind, repeat := "", 1
					for _, a := range el.Attr {
						switch a.Name.Local {
						case "value-type":
							kind = a.Value
						case "number-columns-repeated":
							if n := atoiSafe(a.Value); n > 0 {
								repeat = n
							}
						}
					}
					if err := dec.Skip(); err != nil {
						break
					}
					if repeat > 64 {
						repeat = 0 // the trailing "rest of the sheet is empty" run
					}
					for i := 0; i < repeat; i++ {
						row = append(row, kind)
					}
					continue
				}
				depth++
			case xml.EndElement:
				depth--
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// roundTripFixture is the workbook both round-trip tests read. Every row in it is a case that a CSV
// export gets wrong.
func roundTripFixture(t *testing.T) []byte {
	t.Helper()
	wb := New()
	sh := wb.AddSheet("Catalogue")
	sh.Title = "Product catalogue"
	sh.Subtitle = "3 products · generated 2026-08-11 09:30"
	sh.Columns = catalogueColumns
	sh.AddRow(
		Str("DBS-AGG-34CLEAR"), Str(`Blu 60 <b>"Slab" & Co.</b>`), Str("Techo-Bloc"), Str("Charcoal"), Str("Patio"),
		Num(800), Num(795), Str("PC"), Money(2200), Str("PC"),
	)
	// Leading zeros and a date-shaped SKU: the two things a CSV import silently destroys.
	sh.AddRow(
		Str("00123"), Str("Border Stone"), Str("Unilock"), Str("Beige"), Str("Walkway"),
		Blank(), Blank(), Str("SF"), MoneyOr(nil), Blank(),
	)
	sh.AddRow(
		Str("1-2"), Str("Cap 3x12"), Str("Techo-Bloc"), Str("Grey"), Str("Wall"),
		Num(12.5), Num(10), Str("EA"), Money(542400), Str("PLT"),
	)
	data, err := wb.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRoundTrip_LibreOfficeOpensTheWorkbookAndReadsBackWhatWasWritten(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: skipping the LibreOffice round trip")
	}
	rows := roundTripCSV(t, roundTripFixture(t))
	if len(rows) < 6 {
		t.Fatalf("want title + subtitle + header + 3 data rows, got %d rows: %v", len(rows), rows)
	}
	header, first, second, third := rows[2], rows[3], rows[4], rows[5]

	if header[0] != "SKU" || header[8] != "List price" {
		t.Errorf("header row came back as %v", header)
	}
	// 1. A SKU with leading zeros is still a SKU. A CSV import turns "00123" into 123.
	if second[0] != "00123" {
		t.Errorf("SKU came back as %q, the leading zeros were eaten", second[0])
	}
	// 2. A date-shaped SKU is not a date. This is THE classic spreadsheet data loss.
	if third[0] != "1-2" {
		t.Errorf("SKU came back as %q, a date-shaped SKU was converted to a date", third[0])
	}
	// 3. Markup in a product name survives the XML escaping intact.
	if first[1] != `Blu 60 <b>"Slab" & Co.</b>` {
		t.Errorf("product name came back as %q", first[1])
	}
	// 4. Quantities are numbers, rendered without a stock ledger's four decimal places.
	if first[5] != "800" || first[6] != "795" || third[5] != "12.5" {
		t.Errorf("quantities came back as %q/%q/%q", first[5], first[6], third[5])
	}
	// 5. THE MONEY PROOF. A number carrying a currency format renders as $22.00; a cell whose value
	//    is the STRING "22.00" is not formatted and comes back bare. This assertion is what dies if
	//    money is ever written as text.
	if first[8] != "$22.00" {
		t.Errorf("list price came back as %q, want $22.00, a formatted currency NUMBER, not text", first[8])
	}
	if third[8] != "$5,424.00" {
		t.Errorf("list price came back as %q, want $5,424.00 (thousands separator from the number format)", third[8])
	}
	// 6. The empty-cell rule survives the trip: unknown on hand / available / price are EMPTY, not 0.
	if second[5] != "" || second[6] != "" || second[8] != "" {
		t.Errorf("a row with no ledger entry came back as %v, blanks became values", second)
	}
}

func TestRoundTrip_LibreOfficeAgreesWithEveryColumnsType(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: skipping the LibreOffice round trip")
	}
	types := roundTripTypes(t, roundTripFixture(t))
	if len(types) < 6 {
		t.Fatalf("got %d rows from the ODF conversion", len(types))
	}
	// Row 0 title, row 1 subtitle, row 2 header, rows 3.. the data.
	first, second := types[3], types[4]

	// Text columns must be strings, including the two SKUs a CSV would have coerced.
	for _, i := range []int{0, 1, 2, 3, 4, 7, 9} {
		if first[i] != "string" {
			t.Errorf("column %d re-typed as %q, want string", i, first[i])
		}
	}
	// Quantity, availability and money are floats, the money one carrying the currency format,
	// which the CSV test above proves separately.
	for _, i := range []int{5, 6, 8} {
		if first[i] != "float" {
			t.Errorf("column %d re-typed as %q, want float, a quantity or price that is text does not sum", i, first[i])
		}
	}
	// The row with no ledger entry: those cells have NO type at all, because they have no content.
	if len(second) > 5 && second[5] != "" {
		t.Errorf("an unknown quantity re-typed as %q, want an empty cell", second[5])
	}
}

func TestRoundTrip_ADateCellComesBackAsADate(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: skipping the LibreOffice round trip")
	}
	wb := New()
	sh := wb.AddSheet("Statement")
	sh.Columns = []Column{{Header: "Date", Width: 14}, {Header: "Amount", Width: 14}}
	sh.AddRow(Date(time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)), Money(-12345))
	data, err := wb.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	rows := roundTripCSV(t, data)
	if len(rows) < 2 {
		t.Fatalf("got %v", rows)
	}
	if rows[1][0] != "2026-08-11" {
		t.Errorf("the date came back as %q, want the ISO 2026-08-11", rows[1][0])
	}
	// A negative amount must be unmistakable: a credit shown as an ordinary number gets read as a
	// charge (the DIB-207 statement case this format was chosen for).
	if rows[1][1] != "$(123.45)" {
		t.Errorf("the negative amount came back as %q, want $(123.45)", rows[1][1])
	}
}
