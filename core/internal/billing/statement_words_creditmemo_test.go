package billing

import (
	"testing"
)

// memo's text is never read for either.
func TestStatementDescription_CreditMemoLineIsTheRowsOwnNumbers(t *testing.T) {
	for _, c := range []struct{ memo, doc, reverses, want string }{
		{"#CM-5, reverses #INV-3 (" + swToken + ")", "#CM-5", "#INV-3", "#CM-5, reverses #INV-3"},
		{"#CM-5 (" + swToken + ")", "#CM-5", "", "#CM-5"},
		{swUUID, "#CM-5", "", "#CM-5"},
		{swToken + " (whatever)", "#CM-5", "#INV-3", "#CM-5, reverses #INV-3"},
		{swUUID, "", "", "Credit memo"},
		{"", "", "", "Credit memo"},
	} {
		if got := statementDescription("CREDIT_MEMO", "invoice", c.memo, c.doc, c.reverses); got != c.want {
			t.Errorf("memo %q doc %q reverses %q reads %q, want %q", c.memo, c.doc, c.reverses, got, c.want)
		}
	}
}
