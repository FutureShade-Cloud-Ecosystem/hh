// Package export builds NATIVE .xlsx workbooks, the real OOXML/SpreadsheetML package Excel and
// LibreOffice open without a "the file format and the extension don't match" warning, using the Go
// standard library ONLY (archive/zip plus hand-written XML parts). No third-party dependency, by
// client ruling (D-076, DIB-441).
//
// WHY A PACKAGE AND NOT A FUNCTION. Four asks want the same file (DIB-441 catalogue, DIB-207
// statements with a title and formatting, DIB-405 audit export, DIB-203 transaction exports with a
// second sheet). Written once as a typed workbook model, each of those is a column list and a row
// loop; written four times inline, they are four subtly different XML dialects and four places to
// get the money format wrong.
//
// WHAT "TYPED" BUYS. A CSV hands a spreadsheet strings and lets its import heuristics decide what
// they were: "DBS-AGG-34CLEAR" becomes text (fine), "1-2" becomes a date (not fine), "00123" loses
// its leading zeros, and a quantity column that came back as text does not sum. This package writes
// the cell TYPE into the file, so what the exporter meant is what the reader gets.
//
// THREE RULES THIS PACKAGE ENFORCES, each of which was a bug somewhere before:
//
//  1. A number nobody has is a BLANK CELL, not 0, and not a zero-length string sitting in a numeric
//     column. A fabricated zero sums; an empty string in a number column makes the whole column
//     text on re-import. Blank() emits NO <c> element at all (see writeRow).
//  2. Money crosses the wire as CENTS (int64) and lands as a NUMBER with a currency number format.
//     Never "$22.00" as text, that is a string that neither sums nor sorts.
//  3. Everything is XML-escaped, once, in one place, and characters that are illegal in XML 1.0 are
//     dropped rather than written out to corrupt the package.
//
// DESIGN DECISION, INLINE STRINGS, NO sharedStrings.xml. Strings are written as
// <c t="inlineStr"><is><t>…</t></is></c> instead of an index into a shared string table. It is one
// fewer part, one fewer relationship, and, the real reason, there is no index to drift: a shared
// table is a second data structure that has to stay in lockstep with every cell that points into it,
// and the failure mode when it doesn't is a workbook full of the wrong words. The cost is file size
// on a workbook with heavy repetition, which a catalogue extract of at most a few hundred rows does
// not have. If a consumer ever needs 100k rows, the table can be added behind this same API.
package export

import (
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// CellKind is what a cell IS, which decides both how it is written and how a spreadsheet reads it.
type CellKind uint8

const (
	// KindBlank is a truly empty cell: no value, no type, no element. It is the honest rendering of
	// "we do not have this number", and it is deliberately the zero value so a Cell{} is blank.
	KindBlank CellKind = iota
	// KindString is text. Written as an inline string so a SKU stays a SKU.
	KindString
	// KindNumber is a plain number (quantities, counts).
	KindNumber
	// KindMoney is a number carrying a currency number format. Held as CENTS.
	KindMoney
	// KindDate is a date, written as an Excel serial number with a yyyy-mm-dd format.
	KindDate
)

// Cell is one typed value. Build them with the constructors below rather than by hand, the
// constructors are where "not a number" collapses to blank instead of writing NaN into the file.
type Cell struct {
	Kind CellKind
	// Text carries KindString.
	Text string
	// Number carries KindNumber.
	Number float64
	// Cents carries KindMoney. Cents, not dollars: money is integer arithmetic everywhere in this
	// codebase, and the only float in a money path should be the one the spreadsheet renders.
	Cents int64
	// When carries KindDate.
	When time.Time
}

// Blank is an empty cell. See rule 1 in the package doc: this is NOT zero and NOT "".
func Blank() Cell { return Cell{Kind: KindBlank} }

// Str is a text cell. An empty string is a BLANK cell, not an empty inline string, an empty <t/>
// is a value ("this cell contains the empty text") where none was meant, and in a column that is
// otherwise numeric it is the thing that turns the column into text.
func Str(s string) Cell {
	if s == "" {
		return Blank()
	}
	return Cell{Kind: KindString, Text: s}
}

// Num is a numeric cell. NaN/±Inf are blank: they are not numbers, and writing them produces a file
// Excel repairs on open.
func Num(v float64) Cell {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return Blank()
	}
	return Cell{Kind: KindNumber, Number: v}
}

// NumOr is the nil-safe Num: a nil pointer is the "no ledger row for this product" case and must
// stay blank rather than become 0.
func NumOr(v *float64) Cell {
	if v == nil {
		return Blank()
	}
	return Num(*v)
}

// Money is a currency cell from CENTS. Zero cents is a REAL price ($0.00) and is written; "no price
// on file" is MoneyOr(nil).
func Money(cents int64) Cell { return Cell{Kind: KindMoney, Cents: cents} }

// MoneyOr is the nil-safe Money. The distinction it preserves is load-bearing: "$0.00" is a price
// someone may try to sell at, and "no anchor on file" is not.
func MoneyOr(cents *int64) Cell {
	if cents == nil {
		return Blank()
	}
	return Money(*cents)
}

// Date is a date cell (date only, the time of day is dropped). The zero time is blank.
func Date(t time.Time) Cell {
	if t.IsZero() {
		return Blank()
	}
	return Cell{Kind: KindDate, When: t}
}

// Column is one column's header text and its width in Excel's character units. A width of 0 means
// "leave it to the reader's default".
type Column struct {
	Header string
	Width  float64
}

// Sheet is one worksheet: an optional title row, a styled header row, and the data.
type Sheet struct {
	// Name is the tab name. Sanitised on write (Excel forbids []:*?/\ and 31+ characters).
	Name string
	// Title, when set, occupies row 1 in a larger bold style, merged across the columns. Statements
	// (DIB-207) need it; the catalogue uses it to stamp the file with what it is and what filter
	// produced it, so a printed extract is never mistaken for "the catalogue".
	Title string
	// Subtitle, when set, follows the title on its own row (the filter summary / generated-at line).
	Subtitle string
	Columns  []Column
	Rows     [][]Cell
	// FreezeHeader pins the header row (and anything above it) so row 400 still knows what column
	// it is in.
	FreezeHeader bool
}

// AddRow appends one row. Short rows are fine: a missing trailing cell is a blank cell.
func (s *Sheet) AddRow(cells ...Cell) { s.Rows = append(s.Rows, cells) }

// headerRowIndex is the 1-based worksheet row the header lands on, given the title/subtitle above it.
func (s *Sheet) headerRowIndex() int {
	n := 1
	if s.Title != "" {
		n++
	}
	if s.Subtitle != "" {
		n++
	}
	return n
}

// Workbook is an ordered set of sheets. The zero value is usable; New() reads better at call sites.
type Workbook struct {
	Sheets []*Sheet
}

// New returns an empty workbook.
func New() *Workbook { return &Workbook{} }

// AddSheet appends a sheet and returns it for further building.
func (w *Workbook) AddSheet(name string) *Sheet {
	s := &Sheet{Name: name, FreezeHeader: true}
	w.Sheets = append(w.Sheets, s)
	return s
}

// -- value rendering ------------------------------------------------------------------------------

// excelEpoch is the origin of the 1900 date system. It is 1899-12-30, not 1899-12-31, because Excel
// deliberately reproduces Lotus 1-2-3's belief that 1900 was a leap year; anchoring two days early
// makes every date from 1900-03-01 onward land on the serial Excel expects.
var excelEpoch = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)

// dateSerial converts a date to its Excel serial number, in the date's OWN location, so a timestamp
// stored as 2026-08-11T00:00:00-04:00 exports as 2026-08-11 and not the previous day.
func dateSerial(t time.Time) int64 {
	y, m, d := t.Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return int64(day.Sub(excelEpoch) / (24 * time.Hour))
}

// formatNumber renders a float without an exponent, 1e6 must arrive in the file as 1000000, because
// "1e+06" is not a number to a spreadsheet parser.
func formatNumber(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// formatCents renders cents as a decimal string EXACTLY, without going through a float. 1234567
// cents is "12345.67" and not "12345.669999999999", the whole point of holding money as an integer
// is thrown away if the last step divides it.
func formatCents(c int64) string {
	neg := c < 0
	if neg {
		c = -c
	}
	whole := c / 100
	frac := c % 100
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	b.WriteString(strconv.FormatInt(whole, 10))
	b.WriteByte('.')
	if frac < 10 {
		b.WriteByte('0')
	}
	b.WriteString(strconv.FormatInt(frac, 10))
	return b.String()
}

// -- XML escaping ---------------------------------------------------------------------------------

// escapeXML escapes the five XML entities and DROPS characters that are illegal in XML 1.0.
//
// The escaping order matters: & must be replaced first or every replacement it writes gets
// re-escaped ("Blu & Grey" → "Blu &amp;amp; Grey"). Writing to a builder rune by rune sidesteps the
// question entirely, which is why it is done that way rather than with five chained replacements.
//
// The control-character drop is not decoration. A product description pasted out of a supplier PDF
// carries \x0b and \x1a often enough to matter, those bytes are not representable in XML 1.0 at all,
// and a workbook containing one opens as "we found a problem with some content".
func escapeXML(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&apos;")
		case '\t', '\n', '\r':
			b.WriteRune(r)
		default:
			switch {
			case r < 0x20:
				// Illegal in XML 1.0, dropped, not escaped: &#11; is equally illegal.
			case r == utf8.RuneError:
				// Invalid UTF-8 in the source. Dropping it beats writing a byte sequence that makes
				// the whole package unreadable.
			case r >= 0xD800 && r <= 0xDFFF, r == 0xFFFE, r == 0xFFFF:
				// Surrogates and the two non-characters: illegal in XML.
			default:
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// needsSpacePreserve reports whether an inline string would lose meaning to XML whitespace
// collapsing. "Blu 60 " and "Blu 60" are two different SKUs' worth of confusion.
func needsSpacePreserve(s string) bool {
	return s != strings.TrimSpace(s)
}

// -- cell references ------------------------------------------------------------------------------

// colName converts a 0-based column index to its spreadsheet letters (0→A, 25→Z, 26→AA).
func colName(idx int) string {
	if idx < 0 {
		idx = 0
	}
	name := ""
	for {
		name = string(rune('A'+idx%26)) + name
		idx = idx/26 - 1
		if idx < 0 {
			break
		}
	}
	return name
}

// cellRef is the A1-style reference for a 0-based column and a 1-based row.
func cellRef(col int, row int) string { return colName(col) + strconv.Itoa(row) }

// sanitiseSheetName applies Excel's tab-name rules: at most 31 characters, none of []:*?/\, never
// empty. A name that breaks them is not a warning, it is a workbook Excel refuses to open.
func sanitiseSheetName(name string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case '[', ']', ':', '*', '?', '/', '\\':
			return -1
		}
		if r < 0x20 {
			return -1
		}
		return r
	}, name)
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return "Sheet1"
	}
	// Trim by RUNES, not bytes: Excel counts characters, and slicing a multi-byte name at byte 31
	// produces invalid UTF-8.
	if utf8.RuneCountInString(cleaned) > 31 {
		runes := []rune(cleaned)
		cleaned = strings.TrimSpace(string(runes[:31]))
	}
	return cleaned
}
