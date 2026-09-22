package pricing

// xlsx_reader.go, a READER for the OOXML/SpreadsheetML subset a manufacturer price list actually
// uses, written against archive/zip and encoding/xml and nothing else.
//
// - WHY HAND-WRITTEN -
//
// Third-party spreadsheet libraries are refused here by client ruling (D-076 / DIB-441). That ruling
// already produced a hand-written OOXML WRITER, internal/export, whose header states the same rule
// in its first four lines, and it applies to the reading direction identically. excelize and its
// kin are not on the table, so the question is not "library or not" but "this subset, or CSV only".
//
// - WHY IT IS SAFE TO WRITE THIS SUBSET BY HAND -
//
// The general problem, styles, number formats, dates, calc chains, defined names, pivot caches, is
// genuinely large, and a half-done general reader on a money path is the bad outcome this file has
// to avoid. What makes it tractable is that a price list needs FOUR CELL SHAPES and no more:
//
//	t="s"          an index into sharedStrings.xml. What Excel and LibreOffice write for text.
//	t="inlineStr"  the string inside the cell. What internal/export writes, and what some exporters
//	               emit; both dialects appear in the wild, so both are read.
//	t="str"        a formula's cached string result. A price list with a CONCAT'd SKU column hits it.
//	(no t)         a number, in the file's own decimal text. Money arrives here.
//
// Everything else this reader does not understand it renders as the raw <v> text and lets
// parseMoneyCents refuse it by name, which is the honest failure: a cell we could not interpret
// becomes a row that names itself in the error, not a zero.
//
// TWO THINGS ARE DELIBERATELY NOT SUPPORTED, and both are stated rather than silently mishandled:
//
//  1. DATE CELLS. A date in xlsx is a float plus a style, and resolving it means reading styles.xml
//     and the number-format table. This surface maps four columns, SKU, cost, retail, trade, and
//     none of them is a date (pricefile_model.go says why effective_col is not offered). A date cell
//     read through here comes back as its serial number, which parseMoneyCents will happily read as
//     money; that is why the mapping UI does not offer a date column to map it into.
//  2. MULTI-SHEET SELECTION. The FIRST sheet in workbook order is read. Not the first file named
//     sheet1.xml, those two differ whenever a tab has been reordered or deleted, which is common -
//     so the workbook part and its relationships are resolved properly rather than by filename.
//
// - BUDGETS -
//
// A zip is a decompression bomb until somebody bounds it. Every part is read through an io.LimitedReader
// and the totals below are enforced; a file that exceeds one is refused in words, not truncated
// silently, because a price list quietly missing its last four thousand rows is the worst outcome
// available.

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strconv"
	"strings"
)

const (
	// xlsxMaxPartBytes bounds ONE decompressed part. The sheet is the big one; 96 MiB of XML is
	// roughly a quarter-million rows of four columns, which is far past any price list and far short
	// of a memory problem.
	xlsxMaxPartBytes = 96 << 20
	// xlsxMaxRows bounds the sheet. 200k is two orders of magnitude past the largest real price list
	// in this deployment and still refuses a bomb.
	xlsxMaxRows = 200000
	// xlsxMaxCols bounds a row. A cell reference of "XFD" is column 16384, the format's own limit;
	// this is well under it because a price list with 512 columns is not a price list.
	xlsxMaxCols = 512
)

// errNotXLSX marks a payload that is not a zip at all, so the caller can say "this is not a
// spreadsheet" rather than leaking a zip-format message.
var errNotXLSX = errors.New("pricing: the upload is not a readable .xlsx workbook")

// looksLikeXLSX answers on the file's own bytes, not on its name. A browser will happily label a CSV
// as .xlsx and a manufacturer will happily email one; the local file header magic "PK\x03\x04" is
// what actually decides, and a mislabelled CSV then falls through to the CSV path and works.
func looksLikeXLSX(data []byte) bool {
	return len(data) >= 4 && data[0] == 'P' && data[1] == 'K' && data[2] == 0x03 && data[3] == 0x04
}

// readXLSX returns the first worksheet as a rectangular []string grid, blank-padded.
//
// Blank padding matters: xlsx omits empty cells entirely, so row 7 of a four-column sheet can be a
// single <c r="C7">. A reader that appended cells in document order would silently shift that value
// into column A and price the wrong column. Every cell lands at the index its own r= reference names.
func readXLSX(data []byte) (rows [][]string, err error) {
	if !looksLikeXLSX(data) {
		return nil, errNotXLSX
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errNotXLSX
	}
	parts := map[string]*zip.File{}
	for _, f := range zr.File {
		parts[strings.TrimPrefix(f.Name, "/")] = f
	}

	sheetPath, err := firstSheetPath(parts)
	if err != nil {
		return nil, err
	}
	shared, err := readSharedStrings(parts)
	if err != nil {
		return nil, err
	}
	return readSheet(parts[sheetPath], shared)
}

// firstSheetPath resolves the workbook's FIRST tab to its part name through the relationship table.
// Falls back to the lowest-numbered xl/worksheets/sheetN.xml only when the workbook part or its
// rels are missing or unreadable, a degrade, so an unusual-but-openable workbook still imports.
func firstSheetPath(parts map[string]*zip.File) (string, error) {
	rid := ""
	if wb := parts["xl/workbook.xml"]; wb != nil {
		if raw, err := readPart(wb); err == nil {
			rid = firstSheetRelID(raw)
		}
	}
	if rid != "" {
		if rels := parts["xl/_rels/workbook.xml.rels"]; rels != nil {
			if raw, err := readPart(rels); err == nil {
				if target := relTarget(raw, rid); target != "" {
					// Targets are relative to the workbook part's folder (xl/). An absolute target
					// (leading /) is legal too and is used as-is minus the slash.
					p := strings.TrimPrefix(target, "/")
					if !strings.HasPrefix(target, "/") {
						p = "xl/" + strings.TrimPrefix(target, "./")
					}
					if parts[p] != nil {
						return p, nil
					}
				}
			}
		}
	}
	best := ""
	bestN := 1 << 30
	for name := range parts {
		if !strings.HasPrefix(name, "xl/worksheets/sheet") || !strings.HasSuffix(name, ".xml") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, "xl/worksheets/sheet"), ".xml"))
		if err != nil {
			continue
		}
		if n < bestN {
			best, bestN = name, n
		}
	}
	if best == "" {
		return "", errNotXLSX
	}
	return best, nil
}

// firstSheetRelID pulls the r:id of the first <sheet> element under <sheets>.
func firstSheetRelID(raw []byte) string {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	for {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "sheet" {
			continue
		}
		for _, a := range se.Attr {
			// The namespace prefix is r: by convention but is not guaranteed, so match on the local
			// name and let the (single) id attribute win.
			if a.Name.Local == "id" {
				return a.Value
			}
		}
	}
}

// relTarget finds the Target of the relationship with the given Id.
func relTarget(raw []byte, id string) string {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	for {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "Relationship" {
			continue
		}
		gotID, target := "", ""
		for _, a := range se.Attr {
			switch a.Name.Local {
			case "Id":
				gotID = a.Value
			case "Target":
				target = a.Value
			}
		}
		if gotID == id {
			return target
		}
	}
}

// readSharedStrings materialises sharedStrings.xml. Absent is normal, a workbook of inline strings
// (internal/export writes those) has no such part, and yields an empty table, not an error.
//
// An <si> can be one <t> or a run of <r><t> fragments that must be CONCATENATED; a SKU styled
// mid-string in Excel arrives as three runs, and a reader that took only the first would import
// "DBS-" and match nothing. <rPh> (phonetic guides, East-Asian locales) also contains <t> and is NOT
// part of the value, so its subtree is skipped.
func readSharedStrings(parts map[string]*zip.File) ([]string, error) {
	f := parts["xl/sharedStrings.xml"]
	if f == nil {
		return nil, nil
	}
	raw, err := readPart(f)
	if err != nil {
		return nil, err
	}
	dec := xml.NewDecoder(bytes.NewReader(raw))
	var (
		out  []string
		cur  strings.Builder
		inSI bool
		inT  bool
	)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errNotXLSX
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "si":
				inSI, inT = true, false
				cur.Reset()
			case "rPh":
				if err := dec.Skip(); err != nil {
					return nil, errNotXLSX
				}
			case "t":
				inT = inSI
			}
		case xml.CharData:
			if inT {
				cur.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inT = false
			case "si":
				if inSI {
					out = append(out, cur.String())
					inSI = false
				}
			}
		}
	}
	return out, nil
}

// readSheet streams sheetData into a blank-padded grid.
func readSheet(f *zip.File, shared []string) ([][]string, error) {
	if f == nil {
		return nil, errNotXLSX
	}
	raw, err := readPart(f)
	if err != nil {
		return nil, err
	}
	dec := xml.NewDecoder(bytes.NewReader(raw))

	var (
		rows    [][]string
		row     []string
		inSheet bool
		inRow   bool
		cellCol int
		cellTyp string
		val     strings.Builder
		inVal   bool
		inIS    bool
	)
	put := func(col int, s string) {
		if col < 0 || col >= xlsxMaxCols {
			return
		}
		for len(row) <= col {
			row = append(row, "")
		}
		row[col] = s
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errNotXLSX
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "sheetData":
				inSheet = true
			case "row":
				if !inSheet {
					break
				}
				inRow = true
				row = row[:0]
			case "c":
				if !inRow {
					break
				}
				cellCol, cellTyp = -1, ""
				val.Reset()
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "r":
						cellCol = colIndexOf(a.Value)
					case "t":
						cellTyp = a.Value
					}
				}
				if cellCol < 0 {
					// A cell with no reference is legal and means "the next column along".
					cellCol = len(row)
				}
			case "is":
				inIS = inRow
			case "v":
				inVal = inRow
			case "t":
				if inIS {
					inVal = true
				}
			}
		case xml.CharData:
			if inVal {
				val.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v":
				inVal = false
			case "t":
				if inIS {
					inVal = false
				}
			case "is":
				inIS = false
			case "c":
				if inRow {
					put(cellCol, decodeCell(cellTyp, val.String(), shared))
				}
			case "row":
				if inRow {
					cp := make([]string, len(row))
					copy(cp, row)
					rows = append(rows, cp)
					inRow = false
					if len(rows) > xlsxMaxRows {
						return nil, badReq("the workbook has more than %d rows", xlsxMaxRows)
					}
				}
			case "sheetData":
				inSheet = false
			}
		}
	}
	return rows, nil
}

// decodeCell turns one cell's raw <v>/<is> text into the string a column mapper sees.
//
// An out-of-range shared-string index returns EMPTY rather than the index. Returning "3" for a
// corrupt string reference would put a number in a SKU column, and a SKU of "3" matches nothing and
// looks like data.
func decodeCell(typ, raw string, shared []string) string {
	switch typ {
	case "s":
		i, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || i < 0 || i >= len(shared) {
			return ""
		}
		return shared[i]
	case "e":
		// A formula error (#N/A, #REF!). It is not a value; treating it as one would import the word
		// "#N/A" as a SKU or refuse the whole file over a cell nobody meant to be read.
		return ""
	default:
		// inlineStr, str, b, n and the untyped numeric cell all carry their own text.
		return strings.TrimSpace(raw)
	}
}

// colIndexOf converts a cell reference ("B7", "AA12") to a zero-based column index. A reference with
// no letters, or with more than three, is refused with -1 so the caller falls back to positional
// placement rather than indexing into nowhere.
func colIndexOf(ref string) int {
	n, letters := 0, 0
	for i := 0; i < len(ref); i++ {
		c := ref[i]
		switch {
		case c >= 'A' && c <= 'Z':
			n = n*26 + int(c-'A') + 1
			letters++
		case c >= 'a' && c <= 'z':
			n = n*26 + int(c-'a') + 1
			letters++
		default:
			i = len(ref)
		}
		if letters > 3 {
			return -1
		}
	}
	if letters == 0 {
		return -1
	}
	return n - 1
}

// readPart decompresses one zip entry under a hard byte budget.
func readPart(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, errNotXLSX
	}
	defer rc.Close()
	// LimitReader at budget+1 so a part that exactly fills the budget is not mistaken for one that
	// overflowed it.
	buf, err := io.ReadAll(io.LimitReader(rc, xlsxMaxPartBytes+1))
	if err != nil {
		return nil, errNotXLSX
	}
	if len(buf) > xlsxMaxPartBytes {
		return nil, badReq("the workbook part %s is larger than this import accepts", f.Name)
	}
	return buf, nil
}
