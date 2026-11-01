package billing

import "testing"

// 0.64 N1: the invoice writer's two system order memos carry an order number in a slot a clerk's
// typed memo could imitate, so each passes only when that number is the invoiced order's OWN number
// (ownOrderNo, resolved by the caller through the order module's seam). Any other token in the slot,
// a crafted clerk memo or another real order's number included, reads fixed words; an invoice with no
// order, or an unwired seam, fails the same way.
func TestStatementDescription_AnOrderMemoTakesOnlyTheInvoicesOwnOrder(t *testing.T) {
	const own = "#SO-TRT-1041"
	for _, bad := range []string{
		"#INV-1, order #SO-P063LEAK-1", // the clerk's crafted token in the slot
		"#INV-12, order #SO-KGN-7",     // another real order's number
		"#INV-12, order " + swUUID,     // (already shape rejected; kept for the pair)
		"#INV-12, progress invoice for order #SO-P063LEAK-1 (1 shipment group(s))",
		"#INV-12, progress invoice for order #SO-KGN-7 (3 shipment group(s))",
	} {
		if got := statementDescription("INVOICE", "invoice", bad, "#INV-12", "", own); got == bad {
			t.Errorf("the order slot took %q", bad)
		}
		if got := statementDescription("INVOICE", "invoice", bad, "#INV-12", "", own); got != "Invoice" {
			t.Errorf("%q reads %q, want Invoice", bad, got)
		}
	}
	// An invoice with no order (or an unwired seam) leaves ownOrderNo empty: even the true shape reads
	// fixed words, the fail safe side.
	for _, memo := range []string{
		"#INV-12, order #SO-TRT-1041",
		"#INV-12, progress invoice for order #SO-TRT-1041 (2 shipment group(s))",
	} {
		if got := statementDescription("INVOICE", "invoice", memo, "#INV-12", "", ""); got != "Invoice" {
			t.Errorf("%q with no own order reads %q, want Invoice", memo, got)
		}
	}
	// A clerk memo of the invoice's own number and words around it still never passes.
	if got := statementDescription("INVOICE", "invoice", "#INV-12, keyed 77777", "#INV-12", "", own); got != "Invoice" {
		t.Errorf("a clerk's invoice memo reads %q, want Invoice", got)
	}
}
