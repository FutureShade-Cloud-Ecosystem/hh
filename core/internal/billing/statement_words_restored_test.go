package billing

import "testing"

// Three writers' fixed words are restored as exact shapes: the NSF fee payment, the supplier deposit's close
// and credit back, and the system invoice shapes. The clerk's own invoice memo shape is not among them.
func TestStatementDescription_RestoredWritersReadTheirWords(t *testing.T) {
	for _, ok := range []string{
		"NSF fee payment",
		"Supplier deposit #INV-12 closed on the order's cancel",
		"Supplier deposit #INV-12 credited back on the order's cancel",
		"#INV-12, order #SO-3",
		"#INV-12, order #SO-TRT-1041",
		"#INV-12, progress invoice for order #SO-3 (2 shipment group(s))",
	} {
		if got := statementDescription("ADJUSTMENT", "invoice", ok, "", ""); got != ok {
			t.Errorf("%q reads %q, want it as written", ok, got)
		}
	}
	for _, bad := range []string{
		"Supplier deposit " + swUUID + " closed on the order's cancel",
		"Supplier deposit " + swToken + " credited back on the order's cancel",
		"#INV-12, order " + swUUID,
		swToken + ", order #SO-3",
		"#INV-12, progress invoice for order " + swToken + " (2 shipment group(s))",
		"NSF fee payment " + swToken,
	} {
		if got := statementDescription("ADJUSTMENT", "invoice", bad, "", ""); got == bad {
			t.Errorf("%q passed as written", bad)
		}
	}
}
