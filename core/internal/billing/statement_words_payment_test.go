package billing

import (
	"testing"
)

// With no memo the source decides the words of a payment row, so each arm of that switch is pinned.
func TestStatementDescription_AnEmptyPaymentMemoReadsBySource(t *testing.T) {
	for src, want := range map[string]string{
		"payment_void": "Payment voided", "payment_reversal": "Payment reversed", "payment_reapply": "Payment applied",
		"payment": "Payment received, thank you", "brand_new_writer": "Payment received, thank you",
	} {
		if got := statementDescription("PAYMENT", src, "", "", ""); got != want {
			t.Errorf("empty memo on %s reads %q, want %q", src, got, want)
		}
	}
}
