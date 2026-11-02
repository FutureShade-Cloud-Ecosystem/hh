package billing

import (
	"strings"
	"testing"
)

// Each allow listed shape takes only its minted forms: a uuid or a clerk's token in a slot reads fixed words.
func TestStatementDescription_AllowListedSlotsTakeOnlyMintedForms(t *testing.T) {
	shapes := []struct{ name, tmpl, fixed string }{
		{"early discount", "early payment discount (%s)", "Adjustment"},
		{"overpaid receipt", "overpaid receipt left on account (%s)", "Adjustment"},
		{"holdback withheld", "Holdback withheld on %s", "Holdback withheld"},
		{"parked memo", "Credit memo %s", "Adjustment"},
	}
	for _, s := range shapes {
		for _, bad := range []string{swUUID, swToken, "INVSECRET-9"} {
			memo := strings.Replace(s.tmpl, "%s", bad, 1)
			got := statementDescription("ADJUSTMENT", "invoice", memo, "", "", "")
			if s.name == "holdback withheld" {
				got = statementDescription("HOLDBACK", "", memo, "", "", "")
			}
			if got == memo {
				t.Errorf("%s: the slot took %q", s.name, bad)
			}
		}
	}
	for _, bad := range []string{swUUID, swToken} {
		for _, memo := range []string{
			bad + " (holdback released 2000-01-02)",
		} {
			if got := statementDescription("INVOICE", "invoice", memo, "", "", ""); got == memo {
				t.Errorf("the slot took %q", bad)
			}
		}
	}
	// the minted forms pass as written
	for _, ok := range []string{
		"early payment discount (12)", "overpaid receipt left on account (12)", "Holdback withheld on #INV-12",
		"#INV-12 (holdback released 2000-01-02)", "Credit memo #CM-4", "NSF fee",
	} {
		if got := statementDescription("ADJUSTMENT", "invoice", ok, "", "", ""); got != ok {
			t.Errorf("%q reads %q, want it as written", ok, got)
		}
	}
}

// The clerk's own invoice memo shape (#INV-n, <memo>) never passes.
func TestStatementDescription_AClerksInvoiceMemoIsFixedWords(t *testing.T) {
	if got := statementDescription("INVOICE", "invoice", "#INV-12, "+swToken+" keyed 77777", "#INV-12", "", ""); got != "Invoice" {
		t.Errorf("a clerk's invoice memo reads %q, want Invoice", got)
	}
}
