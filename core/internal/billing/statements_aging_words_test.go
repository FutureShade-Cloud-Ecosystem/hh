package billing

import (
	"strings"
	"testing"
	"time"
)

// The statement's aging heads, the over band notice and the footer date read in the screen's one
// vocabulary. The four stored bands are fixed at 30 day steps whatever the dealer's aging window is,
// so the words name the bands the figures really are.
func TestRenderStatementHTML_AgingWordsAndFooterDate(t *testing.T) {
	asOf := time.Date(2026, time.March, 1, 9, 0, 0, 0, time.FixedZone("dealer", -5*3600))
	r := AgingRow{CustomerName: "Customer one", AccountNo: "A1", CurrentCents: 100, Days30Cents: 200,
		Days60Cents: 300, Days60To90Cents: 400, Days90PlusCents: 500, TotalCents: 1500}
	out := RenderStatementHTML(StatementHeader{BranchName: "Branch"}, r, nil, asOf)

	for _, want := range []string{">Current<", ">1 to 30 days<", ">31 to 60 days<", ">Over 60 days<", ">Total due<",
		"is over 60 days past due", "As of Mar 1, 2026<", "Statement generated Mar 1, 2026."} {
		if !strings.Contains(out, want) {
			t.Errorf("statement lacks %q", want)
		}
	}
	for _, bad := range []string{"1-30 days", "31-60 days", "60+ days", "more than 60 days", "generated 2026-03-01", "1 March 2026"} {
		if strings.Contains(out, bad) {
			t.Errorf("statement still carries %q", bad)
		}
	}
}
