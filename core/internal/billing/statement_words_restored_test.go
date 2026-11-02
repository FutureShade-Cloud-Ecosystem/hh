package billing

import "testing"

// Three writers' fixed words are restored as exact shapes: the NSF fee payment, the supplier deposit's close
// and credit back, and the system invoice shapes. The clerk's own invoice memo shape is not among them.
// The order memos pass only with the invoice's own order's number (statementDescription's ownOrderNo).
func TestStatementDescription_RestoredWritersReadTheirWords(t *testing.T) {
	for _, ok := range []string{
		"NSF fee payment",
		"Supplier deposit #INV-12 closed on the order's cancel",
		"Supplier deposit #INV-12 credited back on the order's cancel",
	} {
		if got := statementDescription("ADJUSTMENT", "invoice", ok, "", "", ""); got != ok {
			t.Errorf("%q reads %q, want it as written", ok, got)
		}
	}
	for _, ok := range []struct{ memo, own string }{
		{"#INV-12, order #SO-3", "#SO-3"},
		{"#INV-12, order #SO-TRT-1041", "#SO-TRT-1041"},
		{"#INV-12, progress invoice for order #SO-TRT-1041 (2 shipment group(s))", "#SO-TRT-1041"},
	} {
		if got := statementDescription("ADJUSTMENT", "invoice", ok.memo, "", "", ok.own); got != ok.memo {
			t.Errorf("%q with its own order %q reads %q, want it as written", ok.memo, ok.own, got)
		}
	}
	for _, bad := range []string{
		"Supplier deposit " + swUUID + " closed on the order's cancel",
		"Supplier deposit " + swToken + " credited back on the order's cancel",
		"NSF fee payment " + swToken,
	} {
		if got := statementDescription("ADJUSTMENT", "invoice", bad, "", "", ""); got == bad {
			t.Errorf("%q passed as written", bad)
		}
	}
}
