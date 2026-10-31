//go:build integration

// finance_charge_deposit_integration_test.go, wave 3 FC-1 fix round (the Opus read of PR 748).
package billing

import (
	"testing"
)

// P3-2: a past due supplier deposit is not charged.
func TestIntegration_W3FC1Fix_ASupplierDepositIsNotCharged(t *testing.T) {
	r := fcRigNew(t)
	depOnly := r.customer(r.branch, "DEPONLY")
	depPlus := r.customer(r.branch, "DEPPLUS")
	r.invoice(depOnly, r.branch, fcInv{no: "DEPONLY", status: "FINALIZED", sourceKind: "SUPPLIER_DEPOSIT",
		issue: "2026-08-01", due: fc1Due31, total: 1_000_000, finalized: fc1At(10, 9, 0)})
	r.invoice(depPlus, r.branch, fcInv{no: "DEPPLUS-D", status: "FINALIZED", sourceKind: "SUPPLIER_DEPOSIT",
		issue: "2026-08-01", due: fc1Due31, total: 1_000_000, finalized: fc1At(10, 9, 0)})
	r.tradeInvoice(depPlus, r.branch, fc1Due31, 100_000, "DEPPLUS-T")

	res, err := r.writer(nil).RunFinanceCharges(r.ctx, FinanceChargeRequest{BranchID: r.branch, Month: fc1Month})
	if err != nil {
		t.Fatalf("the run: %v", err)
	}
	rows := r.chargeRows(r.branch)
	if _, charged := rows[depOnly]; charged {
		t.Fatalf("a customer with only a past due deposit was charged: %+v", res)
	}
	// 1.5 percent of the trade invoice alone ($1,000.00): $15.00, never of the deposit.
	if got := rows[depPlus]; got != 1_500 {
		t.Fatalf("the customer with a deposit and a trade invoice is charged %d, want 1500 on the trade invoice alone", got)
	}
}
