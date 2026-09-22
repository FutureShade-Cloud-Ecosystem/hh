package export

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// xlsx.go, the OOXML package writer: six parts, one zip, standard library only.
//
// THE MINIMUM VIABLE .xlsx, and why each part is here rather than one fewer:
//
//	[Content_Types].xml       every part's MIME type. Without it the package is not an OPC package
//	                          at all and Excel reports it as corrupt.
//	_rels/.rels               the package-level relationship pointing at the workbook part.
//	xl/workbook.xml           the sheet list.
//	xl/_rels/workbook.xml.rels the workbook's relationships: one per worksheet, plus styles.
//	xl/styles.xml             fonts/fills/number formats. Not optional in practice: the money and
//	                          date cells reference style indices, and a missing styles part means
//	                          those references dangle.
//	xl/worksheets/sheetN.xml  the cells.
//
// docProps/{app,core}.xml and xl/theme/theme1.xml are DELIBERATELY ABSENT. They are optional in the
// spec, Excel and LibreOffice both open a package without them, and every part not written is a part
// that cannot be malformed. Nothing in this writer references a theme colour for that reason -
// styles use explicit RGB, never <color theme="1"/>, which would dangle without theme1.xml.

// Style indices into the cellXfs table written by stylesXML. The numbers are positions in that
// table; changing one without changing the other silently restyles every cell.
const (
	styleDefault = 0
	styleHeader  = 1
	styleMoney   = 2
	styleDate    = 3
	styleTitle   = 4
	styleSubtle  = 5
)

// zipModified is a fixed timestamp for every entry so the same workbook produces byte-identical
// bytes on two runs. Tests can compare files; a diff of two exports shows data changes, not clock
// changes. 1980-01-01 is the earliest timestamp the zip format can represent.
var zipModified = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

// Bytes renders the workbook as a complete .xlsx package.
//
// It builds into memory rather than streaming to the response for one reason worth stating: an error
// discovered halfway through writing a streamed 200 leaves the caller holding a truncated file that
// still looks successful. An export is at most a few hundred kilobytes; buying atomicity with that
// much memory is the right trade.
func (w *Workbook) Bytes() ([]byte, error) {
	if len(w.Sheets) == 0 {
		// A workbook with no sheets is not a valid package. Rather than emit one Excel will refuse,
		// give the caller an empty sheet, an export of nothing is still a file, and the catalogue's
		// "no rows matched" case must download a sheet with its headers.
		w.AddSheet("Sheet1")
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name, body string) error {
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: zipModified}
		f, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		_, err = io.WriteString(f, body)
		return err
	}
	// [Content_Types].xml first: not required by the spec, but it is where every reader looks first
	// and some tolerate nothing else.
	parts := []struct{ name, body string }{
		{"[Content_Types].xml", w.contentTypesXML()},
		{"_rels/.rels", relsXML()},
		{"xl/workbook.xml", w.workbookXML()},
		{"xl/_rels/workbook.xml.rels", w.workbookRelsXML()},
		{"xl/styles.xml", stylesXML()},
	}
	for _, p := range parts {
		if err := add(p.name, p.body); err != nil {
			return nil, err
		}
	}
	for i, s := range w.Sheets {
		if err := add(fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1), sheetXML(s)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

const xmlDecl = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"

func (w *Workbook) contentTypesXML() string {
	var b strings.Builder
	b.WriteString(xmlDecl)
	b.WriteString(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	b.WriteString(`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`)
	b.WriteString(`<Default Extension="xml" ContentType="application/xml"/>`)
	b.WriteString(`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>`)
	for i := range w.Sheets {
		fmt.Fprintf(&b, `<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, i+1)
	}
	b.WriteString(`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>`)
	b.WriteString(`</Types>`)
	return b.String()
}

func relsXML() string {
	return xmlDecl +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>` +
		`</Relationships>`
}

func (w *Workbook) workbookXML() string {
	var b strings.Builder
	b.WriteString(xmlDecl)
	b.WriteString(`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">`)
	b.WriteString(`<sheets>`)
	for i, s := range w.Sheets {
		fmt.Fprintf(&b, `<sheet name="%s" sheetId="%d" r:id="rId%d"/>`,
			escapeXML(sanitiseSheetName(s.Name)), i+1, i+1)
	}
	b.WriteString(`</sheets>`)
	// Print titles: the header row (and the title above it) repeat on every printed page. Without
	// this, page 2 of a printed export is a wall of numbers with no column names, which is how an
	// exported price list gets read off the wrong column. Defined names live after <sheets> and
	// before <calcPr>; the schema fixes that order.
	var names []string
	for i, s := range w.Sheets {
		if len(s.Columns) == 0 {
			continue
		}
		names = append(names, fmt.Sprintf(
			`<definedName name="_xlnm.Print_Titles" localSheetId="%d">'%s'!$1:$%d</definedName>`,
			i, escapeXML(strings.ReplaceAll(sanitiseSheetName(s.Name), "'", "''")), s.headerRowIndex()))
	}
	if len(names) > 0 {
		fmt.Fprintf(&b, `<definedNames>%s</definedNames>`, strings.Join(names, ""))
	}
	b.WriteString(`</workbook>`)
	return b.String()
}

func (w *Workbook) workbookRelsXML() string {
	var b strings.Builder
	b.WriteString(xmlDecl)
	b.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for i := range w.Sheets {
		fmt.Fprintf(&b, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, i+1, i+1)
	}
	// The styles relationship id follows the sheets so adding a sheet never renumbers it.
	fmt.Fprintf(&b, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>`, len(w.Sheets)+1)
	b.WriteString(`</Relationships>`)
	return b.String()
}

// Custom number format ids. Anything below 164 is reserved for the built-ins; 164 is the first id an
// application may define.
const (
	numFmtMoney = 164
	numFmtDate  = 165
)

// stylesXML is fixed: this package offers a small, deliberate set of cell looks rather than an
// arbitrary style builder. Every index here is referenced by a style* constant above.
//
// The money format is "$"#,##0.00 with a red-in-parentheses negative, an export that shows a credit
// as an ordinary number is how a credit gets read as a charge. The date format is ISO yyyy-mm-dd
// because this is a Canadian shop reading files that also get opened in US locales, and 05-08-2026
// means two different days on the two sides of that border.
func stylesXML() string {
	var b strings.Builder
	b.WriteString(xmlDecl)
	b.WriteString(`<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	fmt.Fprintf(&b, `<numFmts count="2"><numFmt numFmtId="%d" formatCode="&quot;$&quot;#,##0.00;[Red]&quot;$&quot;(#,##0.00)"/><numFmt numFmtId="%d" formatCode="yyyy\-mm\-dd"/></numFmts>`, numFmtMoney, numFmtDate)
	b.WriteString(`<fonts count="4">` +
		`<font><sz val="11"/><color rgb="FF1A1A1A"/><name val="Calibri"/><family val="2"/></font>` +
		`<font><b/><sz val="11"/><color rgb="FF1A1A1A"/><name val="Calibri"/><family val="2"/></font>` +
		`<font><b/><sz val="14"/><color rgb="FF1A1A1A"/><name val="Calibri"/><family val="2"/></font>` +
		`<font><sz val="10"/><color rgb="FF6B6B6B"/><name val="Calibri"/><family val="2"/></font>` +
		`</fonts>`)
	// Fill 0 (none) and fill 1 (gray125) are required by the spec to occupy the first two slots -
	// Excel indexes them implicitly. The header fill is therefore index 2.
	b.WriteString(`<fills count="3">` +
		`<fill><patternFill patternType="none"/></fill>` +
		`<fill><patternFill patternType="gray125"/></fill>` +
		`<fill><patternFill patternType="solid"><fgColor rgb="FFE7E2D9"/><bgColor indexed="64"/></patternFill></fill>` +
		`</fills>`)
	b.WriteString(`<borders count="2">` +
		`<border><left/><right/><top/><bottom/><diagonal/></border>` +
		`<border><left/><right/><top/><bottom style="thin"><color rgb="FF9A9287"/></bottom><diagonal/></border>` +
		`</borders>`)
	b.WriteString(`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>`)
	b.WriteString(`<cellXfs count="6">`)
	b.WriteString(`<xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>`)                                                                                                  // 0 default
	b.WriteString(`<xf numFmtId="0" fontId="1" fillId="2" borderId="1" xfId="0" applyFont="1" applyFill="1" applyBorder="1" applyAlignment="1"><alignment vertical="center"/></xf>`) // 1 header
	fmt.Fprintf(&b, `<xf numFmtId="%d" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/>`, numFmtMoney)                                                            // 2 money
	fmt.Fprintf(&b, `<xf numFmtId="%d" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/>`, numFmtDate)                                                             // 3 date
	b.WriteString(`<xf numFmtId="0" fontId="2" fillId="0" borderId="0" xfId="0" applyFont="1"/>`)                                                                                    // 4 title
	b.WriteString(`<xf numFmtId="0" fontId="3" fillId="0" borderId="0" xfId="0" applyFont="1"/>`)                                                                                    // 5 subtitle
	b.WriteString(`</cellXfs>`)
	b.WriteString(`<cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles>`)
	b.WriteString(`<dxfs count="0"/>`)
	b.WriteString(`<tableStyles count="0" defaultTableStyle="TableStyleMedium2" defaultPivotStyle="PivotStyleLight16"/>`)
	b.WriteString(`</styleSheet>`)
	return b.String()
}

// sheetXML renders one worksheet. Element ORDER inside <worksheet> is fixed by the schema
// (sheetViews → sheetFormatPr → cols → sheetData → mergeCells); a correct element in the wrong
// place is a file Excel offers to repair.
func sheetXML(s *Sheet) string {
	cols := len(s.Columns)
	for _, r := range s.Rows {
		if len(r) > cols {
			cols = len(r)
		}
	}
	headerRow := s.headerRowIndex()
	lastRow := headerRow + len(s.Rows)

	var b strings.Builder
	b.WriteString(xmlDecl)
	b.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	// fitToPage belongs to sheetPr, which must be the FIRST child of <worksheet>. It is what makes a
	// ten-column export print as one wide page instead of three narrow ones with the money columns
	// orphaned on the last.
	b.WriteString(`<sheetPr><pageSetUpPr fitToPage="1"/></sheetPr>`)
	if cols > 0 {
		fmt.Fprintf(&b, `<dimension ref="A1:%s"/>`, cellRef(cols-1, max(lastRow, 1)))
	}
	// The frozen pane splits BELOW the header row, so the title, the subtitle and the headers all
	// stay on screen while the data scrolls under them.
	b.WriteString(`<sheetViews><sheetView workbookViewId="0">`)
	if s.FreezeHeader && len(s.Columns) > 0 {
		fmt.Fprintf(&b, `<pane ySplit="%d" topLeftCell="A%d" activePane="bottomLeft" state="frozen"/><selection pane="bottomLeft" activeCell="A%d" sqref="A%d"/>`,
			headerRow, headerRow+1, headerRow+1, headerRow+1)
	}
	b.WriteString(`</sheetView></sheetViews>`)
	b.WriteString(`<sheetFormatPr defaultRowHeight="15"/>`)
	if hasWidths(s.Columns) {
		b.WriteString(`<cols>`)
		for i, c := range s.Columns {
			if c.Width <= 0 {
				continue
			}
			fmt.Fprintf(&b, `<col min="%d" max="%d" width="%s" customWidth="1"/>`,
				i+1, i+1, strconv.FormatFloat(c.Width, 'f', -1, 64))
		}
		b.WriteString(`</cols>`)
	}
	b.WriteString(`<sheetData>`)
	row := 1
	if s.Title != "" {
		writeRow(&b, row, []Cell{Str(s.Title)}, styleTitle)
		row++
	}
	if s.Subtitle != "" {
		writeRow(&b, row, []Cell{Str(s.Subtitle)}, styleSubtle)
		row++
	}
	if len(s.Columns) > 0 {
		head := make([]Cell, len(s.Columns))
		for i, c := range s.Columns {
			head[i] = Str(c.Header)
		}
		writeRow(&b, row, head, styleHeader)
		row++
	}
	for _, r := range s.Rows {
		writeRow(&b, row, r, styleDefault)
		row++
	}
	b.WriteString(`</sheetData>`)
	// Merging the title across the columns is what makes a long title readable instead of clipped at
	// the first column boundary, the statement header (DIB-207) is the consumer that needs it.
	if cols > 1 {
		var merges []string
		if s.Title != "" {
			merges = append(merges, fmt.Sprintf(`<mergeCell ref="A1:%s1"/>`, colName(cols-1)))
		}
		if s.Subtitle != "" {
			r := 1
			if s.Title != "" {
				r = 2
			}
			merges = append(merges, fmt.Sprintf(`<mergeCell ref="A%d:%s%d"/>`, r, colName(cols-1), r))
		}
		if len(merges) > 0 {
			fmt.Fprintf(&b, `<mergeCells count="%d">%s</mergeCells>`, len(merges), strings.Join(merges, ""))
		}
	}
	// Landscape, scaled to one page WIDE and as many pages tall as it takes. A catalogue extract gets
	// printed and walked out to the yard; splitting its columns across sheets of paper is how the
	// price ends up next to the wrong product. fitToHeight="0" means "don't squeeze the rows", only
	// the width is constrained.
	b.WriteString(`<pageMargins left="0.5" right="0.5" top="0.6" bottom="0.6" header="0.3" footer="0.3"/>`)
	b.WriteString(`<pageSetup orientation="landscape" fitToWidth="1" fitToHeight="0" paperSize="9"/>`)
	b.WriteString(`</worksheet>`)
	return b.String()
}

func hasWidths(cols []Column) bool {
	for _, c := range cols {
		if c.Width > 0 {
			return true
		}
	}
	return false
}

// writeRow emits one <row>. `base` is the style applied to non-blank cells that do not carry their
// own (money and date always carry their own, because the format IS the type's rendering).
//
// THE EMPTY-CELL RULE LIVES HERE: a blank cell emits NO <c> element. Not <c/>, not <c><v></v></c>,
// and above all not <c t="inlineStr"><is><t></t></is></c>, an empty string in a numeric column is
// what makes a spreadsheet re-type the whole column as text, and it is why "available" for a product
// with no ledger row must be nothing at all rather than "".
func writeRow(b *strings.Builder, rowIdx int, cells []Cell, base int) {
	fmt.Fprintf(b, `<row r="%d">`, rowIdx)
	for i, c := range cells {
		ref := cellRef(i, rowIdx)
		switch c.Kind {
		case KindBlank:
			// Nothing. See the comment above, this is the feature.
		case KindString:
			text := escapeXML(c.Text)
			space := ""
			if needsSpacePreserve(c.Text) {
				space = ` xml:space="preserve"`
			}
			fmt.Fprintf(b, `<c r="%s" s="%d" t="inlineStr"><is><t%s>%s</t></is></c>`, ref, base, space, text)
		case KindNumber:
			fmt.Fprintf(b, `<c r="%s" s="%d"><v>%s</v></c>`, ref, base, formatNumber(c.Number))
		case KindMoney:
			style := styleMoney
			if base == styleHeader {
				style = styleHeader
			}
			fmt.Fprintf(b, `<c r="%s" s="%d"><v>%s</v></c>`, ref, style, formatCents(c.Cents))
		case KindDate:
			style := styleDate
			if base == styleHeader {
				style = styleHeader
			}
			fmt.Fprintf(b, `<c r="%s" s="%d"><v>%d</v></c>`, ref, style, dateSerial(c.When))
		}
	}
	b.WriteString(`</row>`)
}

// -- HTTP -----------------------------------------------------------------------------------------

// ContentType is the .xlsx MIME type. Getting it wrong is how a download lands as
// "catalogue-2026-08-11.xlsx.zip" in Safari or opens in the browser instead of the spreadsheet.
const ContentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

// Download writes a rendered workbook as a file download.
//
// Content-Length is set explicitly and the body written in one shot: a browser download with no
// length shows an indeterminate progress bar, and a partially-written body with a 200 status is a
// corrupt file the user will open and blame the data for.
func Download(w http.ResponseWriter, filename string, data []byte) {
	w.Header().Set("Content-Type", ContentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+sanitiseFilename(filename)+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// sanitiseFilename keeps a header-safe filename: no quotes, no control characters, no path
// separators. A filename is data (it can carry a customer's name in a statement export), and data in
// a header is a header-injection question.
func sanitiseFilename(name string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case '"', '\\', '/', '\r', '\n', ';':
			return -1
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	if cleaned == "" {
		return "export.xlsx"
	}
	return cleaned
}
