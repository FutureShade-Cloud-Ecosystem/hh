package reporting

// export.go, the two renderings of a Result: CSV (the export) and HTML (the printed artefact).
//
// Both walk the SAME Result and the SAME Column allowlist, in the same order, and format each cell
// through the same formatCell. That is not tidiness, it is the AC: what a person sees on screen, what
// lands in the spreadsheet, and what comes out of the printer must be the same numbers, and the only
// way to be sure of that is for there to be one function that turns a value into text.
//
// Money is rendered as a plain decimal with no currency symbol and no thousands separator ("1234.56"),
// exactly as internal/finstmt's export does, so Excel treats the cell as a NUMBER. A grouped "1,234.56"
// imports as text and the column will not sum, which is the single most common complaint about exported
// reports and the reason the office keeps retyping them.

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"html"
	"strconv"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// errBadRequest is the module's 400 constructor (httpx.BadRequestError is the shared errors.As-able type).
func errBadRequest(format string, a ...any) error { return httpx.BadRequestf(format, a...) }

// centsToDecimal renders integer cents as a plain decimal-dollar string. -12345 → "-123.45".
func centsToDecimal(cents int64) string {
	neg := cents < 0
	if neg {
		cents = -cents
	}
	s := fmt.Sprintf("%d.%02d", cents/100, cents%100)
	if neg {
		return "-" + s
	}
	return s
}

// formatCell turns ONE value into its text form, per the column's declared kind.
func formatCell(kind string, v any) string {
	switch kind {
	case KindCents:
		n, _ := v.(int64)
		return centsToDecimal(n)
	case KindInt:
		n, _ := v.(int64)
		return strconv.FormatInt(n, 10)
	case KindQty:
		f, _ := v.(float64)
		// Four decimals matches the NUMERIC(18,4) the quantities come from, then trailing zeros are
		// trimmed so "12" does not print as "12.0000" in a column a person reads.
		s := strconv.FormatFloat(f, 'f', 4, 64)
		s = strings.TrimRight(s, "0")
		return strings.TrimSuffix(s, ".")
	case KindBool:
		b, _ := v.(bool)
		if b {
			return "yes"
		}
		return "no"
	default:
		s, _ := v.(string)
		return s
	}
}

// totalCell formats a totals-map entry for the column.
func totalCell(c Column, totals map[string]any) string {
	v, ok := totals[c.Name]
	if !ok {
		return ""
	}
	return formatCell(c.Kind, v)
}

// renderCSV serialises a Result to RFC-4180 CSV: a title row, a parameter row, the headers, the rows,
// then a TOTAL row. The title and parameter rows are there because an exported file gets emailed, and a
// spreadsheet of numbers with no statement of what it is a report OF is how the wrong quarter ends up
// in a board pack.
//
// The title-block rows are PADDED to the full column width. internal/finstmt's export writes a
// single-cell title row, which Excel opens fine but which makes the file RAGGED, encoding/csv and
// every other strict RFC-4180 reader reject it with "wrong number of fields". Padding costs three
// commas and makes the file parseable by anything, which matters because the most common thing done
// with an exported report after Excel is a script.
func renderCSV(res Result) []byte {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	width := len(res.Columns)
	pad := func(cells ...string) []string {
		row := make([]string, width)
		copy(row, cells)
		return row
	}
	_ = w.Write(pad(res.Name))
	_ = w.Write(pad("Parameters", paramSummary(res.Params)))
	_ = w.Write(pad("Generated", res.RunAt.Format("2006-01-02 15:04:05 MST")))

	headers := make([]string, len(res.Columns))
	for i, c := range res.Columns {
		headers[i] = c.Label
	}
	_ = w.Write(headers)

	for _, row := range res.Rows {
		cells := make([]string, len(res.Columns))
		for i, c := range res.Columns {
			cells[i] = formatCell(c.Kind, row[i])
		}
		_ = w.Write(cells)
	}

	if len(res.Totals) > 0 {
		cells := make([]string, len(res.Columns))
		cells[0] = "TOTAL"
		for i, c := range res.Columns {
			if i == 0 {
				continue
			}
			cells[i] = totalCell(c, res.Totals)
		}
		_ = w.Write(cells)
	}
	w.Flush()
	return buf.Bytes()
}

// paramSummary renders the bound parameters in a stable, human order for the title block.
func paramSummary(params map[string]string) string {
	if len(params) == 0 {
		return "(none)"
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	// Stable order without importing sort twice: the parameter set is small and fixed per template.
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+params[k])
	}
	return strings.Join(parts, "; ")
}

// renderHTML builds the PRINTED artefact, a self-contained document stored on the report_runs row.
//
// Self-contained is the requirement, not a preference: this HTML is read back months later out of a
// database column, by a browser that may be pointed at a different deployment, and anything it links to
// (a stylesheet, a font, a logo URL) is a thing that can be gone or different by then. So the styling is
// inline, plain, and printer-first. It is deliberately not the on-screen component: a screen has
// affordances and a printed page has a header, a parameter statement and a totals row.
func renderHTML(res Result) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8">`)
	b.WriteString(`<title>` + html.EscapeString(res.Name) + `</title>`)
	b.WriteString(`<style>
body{font:12px/1.45 -apple-system,Segoe UI,Roboto,sans-serif;color:#1b1b1b;margin:24px}
h1{font-size:16px;margin:0 0 2px}
.meta{color:#666;font-size:11px;margin:0 0 14px}
table{border-collapse:collapse;width:100%}
th,td{border-bottom:1px solid #ddd;padding:4px 8px;text-align:left;vertical-align:top}
th{border-bottom:1px solid #888;font-size:10px;text-transform:uppercase;letter-spacing:.06em;color:#444}
td.n,th.n{text-align:right;font-variant-numeric:tabular-nums}
tr.total td{border-top:2px solid #444;border-bottom:none;font-weight:600}
@media print{body{margin:0}thead{display:table-header-group}}
</style></head><body>`)
	b.WriteString(`<h1>` + html.EscapeString(res.Name) + `</h1>`)
	b.WriteString(`<p class="meta">` + html.EscapeString(paramSummary(res.Params)) +
		` &middot; generated ` + html.EscapeString(res.RunAt.Format("2006-01-02 15:04:05 MST")) +
		` &middot; ` + strconv.Itoa(res.RowCount) + ` rows</p>`)

	b.WriteString(`<table><thead><tr>`)
	for _, c := range res.Columns {
		b.WriteString(`<th` + numClass(c) + `>` + html.EscapeString(c.Label) + `</th>`)
	}
	b.WriteString(`</tr></thead><tbody>`)
	for _, row := range res.Rows {
		b.WriteString(`<tr>`)
		for i, c := range res.Columns {
			b.WriteString(`<td` + numClass(c) + `>` + html.EscapeString(formatCell(c.Kind, row[i])) + `</td>`)
		}
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</tbody>`)
	if len(res.Totals) > 0 {
		b.WriteString(`<tfoot><tr class="total">`)
		for i, c := range res.Columns {
			cell := totalCell(c, res.Totals)
			if i == 0 {
				cell = "TOTAL"
			}
			b.WriteString(`<td` + numClass(c) + `>` + html.EscapeString(cell) + `</td>`)
		}
		b.WriteString(`</tr></tfoot>`)
	}
	b.WriteString(`</table></body></html>`)
	return b.String()
}

// numClass right-aligns numeric columns. Money and counts read wrong left-aligned, the eye compares
// magnitudes by digit position, and a left-aligned money column makes 9.99 look bigger than 1000.00.
func numClass(c Column) string {
	switch c.Kind {
	case KindCents, KindInt, KindQty:
		return ` class="n"`
	}
	return ""
}
