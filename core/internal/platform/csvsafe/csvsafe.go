// Package csvsafe holds the one rule that makes a text cell safe to open in a spreadsheet. A cell led
// by =, +, -, @, a tab or a carriage return is a formula to Excel and Sheets, so it gets a leading
// single quote (the OWASP rule). It was written for the GL journal exports (W3 AX-2) and is shared
// from here so the report writer's CSV applies the same rule instead of a copy of it.
//
// Quoting a field for the CSV format itself (commas, quotes, newlines) is the writer's job, either
// encoding/csv or the caller's own escape; this package only neutralises formula leaders. Money cells
// never come through here: a negative amount starts with a minus sign and must stay a number.
package csvsafe

import "strings"

// Cell returns s with a single quote in front when its first character would make a spreadsheet read
// it as a formula, and s unchanged otherwise.
func Cell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}
