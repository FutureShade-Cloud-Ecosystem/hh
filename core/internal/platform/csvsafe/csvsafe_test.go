package csvsafe

import "testing"

// A cell led by =, +, -, @, a tab or a carriage return is a formula to a spreadsheet, so it gets one
// leading single quote. Anything else is left as it came.
func TestCell_QuotesFormulaLeaders(t *testing.T) {
	for _, lead := range []string{"=", "+", "-", "@", "\t", "\r"} {
		in := lead + "SUM(A1)"
		if got, want := Cell(in), "'"+in; got != want {
			t.Fatalf("Cell(%q) = %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{"", "plain", "a=b", "1.00", " =x"} {
		if got := Cell(in); got != in {
			t.Fatalf("Cell(%q) = %q, want it unchanged", in, got)
		}
	}
}
