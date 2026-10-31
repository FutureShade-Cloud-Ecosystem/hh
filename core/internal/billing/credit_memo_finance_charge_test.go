package billing

import "testing"

// A memo against a finance charge document debits the finance charge role, not revenue; any other
// source kind keeps the revenue leg.
func TestReversingLegsForAFinanceChargeDebitsFinanceChargeIncome(t *testing.T) {
	legs := CreditMemoJournal{ARCents: 15_000, RevenueCents: 15_000, ReversesSourceKind: SourceFinanceCharge}.ReversingLegsFor("HST")
	if len(legs) != 2 || legs[0].Role != RoleFinanceCharge || legs[0].DebitCents != 15_000 || legs[1].Role != RoleAR || legs[1].CreditCents != 15_000 {
		t.Fatalf("a finance charge memo's legs are %+v, want DR finance_charge / CR ar", legs)
	}
	for _, kind := range []string{"", SourceOrder} {
		legs := CreditMemoJournal{ARCents: 15_000, RevenueCents: 15_000, ReversesSourceKind: kind}.ReversingLegsFor("HST")
		if legs[0].Role != RoleRevenue {
			t.Fatalf("source kind %q reverses %q, want revenue", kind, legs[0].Role)
		}
	}
}
