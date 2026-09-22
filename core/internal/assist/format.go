package assist

// format.go, the assistant's one formatter.
//
// Every number a command puts in front of a person passes through here. Not because formatting is
// hard, but because there are now three consumers of the same answer, the structured panel, the
// chat sentence, and whatever reads the JSON next, and a number that reads "$1,240.00" in the
// panel and "1240 cents" in the sentence is a number nobody trusts twice. One formatter, called by
// the commands, is the cheapest way to make that class of bug impossible rather than unlikely.

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// money renders integer cents as CAD with thousands separators: 124000 → "$1,240.00".
//
// Negative amounts get a leading minus rather than parentheses. Parentheses are the accounting
// convention and this is not an accounting statement, a counter clerk reading "(1,240.00)" on a
// chat line has to know the convention to read a credit, and the ones who do not will read it as a
// footnote.
func money(cents int64) string {
	neg := cents < 0
	if neg {
		cents = -cents
	}
	whole := cents / 100
	frac := cents % 100
	s := strconv.FormatInt(whole, 10)
	// Group from the right in threes.
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	b.WriteByte('$')
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	fmt.Fprintf(&b, ".%02d", frac)
	return b.String()
}

// qty renders a base-unit quantity without lying about precision: whole numbers print whole, and
// fractions keep at most three decimals with trailing zeros trimmed.
//
// "12" and "12.000" carry different implied precision to someone counting pallets, and printing
// 18.5 tonnes as "18.500" invites a reader to believe the yard measured to the kilogram.
func qty(v float64) string {
	s := strconv.FormatFloat(v, 'f', 3, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	if s == "" || s == "-" {
		return "0"
	}
	return s
}

// qtyUOM appends the unit of measure when there is one. An empty UOM prints the bare number rather
// than "12 " with a trailing space, because that space shows up in a copied cell.
func qtyUOM(v float64, uom string) string {
	if strings.TrimSpace(uom) == "" {
		return qty(v)
	}
	return qty(v) + " " + strings.TrimSpace(uom)
}

// day renders a timestamp as a plain date in the branch's reading order: "11 Aug 2026".
//
// No time of day: none of the five commands answers a question where the hour matters, and a
// timestamp printed to the second is a promise about clock accuracy across a NATS-driven system
// that this package cannot keep. A zero time prints "-", never "1 Jan 0001".
func day(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("2 Jan 2006")
}

// plural picks the singular or plural noun for n. Used so headlines read "1 payment" and
// "3 payments" rather than the "3 payment(s)" that tells a reader a programmer was here.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// thousands renders a token count with separators: 1240000 → "1,240,000". Token budgets are large
// numbers that people have to compare at a glance, and an ungrouped seven-digit number is read as
// a different order of magnitude about a third of the time.
func thousands(n int) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.Itoa(n)
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}
