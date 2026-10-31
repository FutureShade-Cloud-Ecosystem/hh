package gl

// export_csv_test.go, the AX-2 fix round's P2-1: a CSV text cell is safe to open in a spreadsheet.
// csvEscape serves three export bodies (the GP parity body's account name and memo, the mapped
// draft body's package account, account name and memo, and the QBO body's account and
// description), so every leg here is for all of them.

import (
	"encoding/csv"
	"strings"
	"testing"
)

func TestCSVEscape_AFormulaLeadCharacterGetsALeadingQuote(t *testing.T) {
	for _, lead := range []string{"=", "+", "-", "@", "\t", "\r"} {
		in := lead + `HYPERLINK("http://x","refund")`
		got := csvEscape(in)
		recs, err := csv.NewReader(strings.NewReader(got)).ReadAll()
		if err != nil || len(recs) != 1 || len(recs[0]) != 1 {
			t.Fatalf("lead %q: the cell must parse as one field, got %q (%v)", lead, got, err)
		}
		if want := "'" + in; recs[0][0] != want {
			t.Fatalf("lead %q: the cell reads %q, want %q", lead, recs[0][0], want)
		}
	}
	if got := csvEscape("plain memo"); got != "plain memo" {
		t.Fatalf("a plain cell must be untouched, got %q", got)
	}
	if got := csvEscape("a=b+c-d@e"); got != "a=b+c-d@e" {
		t.Fatalf("only a LEADING character is dangerous, got %q", got)
	}
}

func TestCSVEscape_ACarriageReturnIsQuoted(t *testing.T) {
	got := csvEscape("line one\rline two")
	if !strings.HasPrefix(got, `"`) || !strings.HasSuffix(got, `"`) {
		t.Fatalf("a cell holding a carriage return must be quoted, got %q", got)
	}
}

func TestCSVEscape_ACommaAndAQuoteTogetherRoundTrip(t *testing.T) {
	in := `refund "VIP", per Smith, 2nd try`
	got := csvEscape(in)
	recs, err := csv.NewReader(strings.NewReader(got)).ReadAll()
	if err != nil || len(recs) != 1 || len(recs[0]) != 1 || recs[0][0] != in {
		t.Fatalf("a memo with a comma and a quote must round trip, got %q -> %q (%v)", got, recs, err)
	}
}
