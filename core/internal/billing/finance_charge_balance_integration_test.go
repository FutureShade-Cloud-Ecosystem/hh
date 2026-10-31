//go:build integration

// finance_charge_balance_integration_test.go, wave 3 FC-1 fix round (the Opus read of PR 748).
package billing

import (
	"context"
	"testing"
	"time"
)

// fcApply plants the three things that reduce an invoice's open amount, each at an instant of the
// test's choosing: a payment with its allocation, a credit applied to the invoice, a write off.
func (r *fcRig) pay(cust, invoice string, cents int64, at time.Time, label string) {
	r.t.Helper()
	var pid string
	if err := r.pool.QueryRow(r.ctx, `
		INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, received_at, branch_id, created_at)
		VALUES ($1, $2::uuid, 'CASH', $3, 'RECEIVED', $4, $5::uuid, $4) RETURNING id::text`,
		r.tag+"-PAY-"+label, cust, cents, at, r.branch).Scan(&pid); err != nil {
		r.t.Fatalf("plant the payment %s: %v", label, err)
	}
	if _, err := r.pool.Exec(r.ctx, `
		INSERT INTO payment_allocations (payment_id, invoice_id, amount_cents, created_at) VALUES ($1::uuid, $2::uuid, $3, $4)`,
		pid, invoice, cents, at); err != nil {
		r.t.Fatalf("plant the allocation %s: %v", label, err)
	}
	r.t.Cleanup(func() {
		c := context.Background()
		_, _ = r.pool.Exec(c, `DELETE FROM payment_allocations WHERE payment_id = $1::uuid`, pid)
		_, _ = r.pool.Exec(c, `DELETE FROM payments WHERE id = $1::uuid`, pid)
	})
}

// offset plants a ledger row tagged to the invoice that reduces it: a credit memo applied or a
// write off, dated at the instant given.
func (r *fcRig) offset(cust, invoice, txnType string, cents int64, at time.Time) {
	r.t.Helper()
	if _, err := r.pool.Exec(r.ctx, `
		INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, occurred_at, created_at)
		VALUES ($1::uuid, $2, $3, 0, 'invoice', $4::uuid, $5::uuid, $6, $6)`,
		cust, txnType, -cents, invoice, r.branch, at); err != nil {
		r.t.Fatalf("plant the %s: %v", txnType, err)
	}
}

// P3-4: the charged balance moves exactly as the aging does when a payment, a credit and a write off
// land on a past due invoice, before and after the month's last instant.
func TestIntegration_W3FC1Fix_TheChargedBalanceMovesAsTheAgingDoes(t *testing.T) {
	r := fcRigNew(t)
	before := fc1At(20, 10, 0)                             // inside the month
	after := time.Date(2026, 10, 5, 10, 0, 0, 0, fc1Loc()) // after the month's last instant

	type leg struct {
		label string
		apply func(cust, inv string)
		bal   int64 // the expected charged balance
	}
	const total = 1_000_000
	legs := []leg{
		{"PAYBEFORE", func(c, i string) { r.pay(c, i, 400_000, before, "B") }, 600_000},
		{"PAYAFTER", func(c, i string) { r.pay(c, i, total, after, "A") }, total},
		{"CREDBEFORE", func(c, i string) { r.offset(c, i, "CREDIT_MEMO", 200_000, before) }, 800_000},
		{"CREDAFTER", func(c, i string) { r.offset(c, i, "CREDIT_MEMO", 200_000, after) }, total},
		{"WOBEFORE", func(c, i string) { r.offset(c, i, "WRITE_OFF", total, before) }, 0},
		{"WOAFTER", func(c, i string) { r.offset(c, i, "WRITE_OFF", total, after) }, total},
	}
	custs := map[string]string{}
	for _, l := range legs {
		c := r.customer(r.branch, l.label)
		custs[l.label] = c
		inv := r.invoice(c, r.branch, fcInv{no: l.label, status: "FINALIZED", sourceKind: "ORDER",
			issue: "2026-08-01", due: fc1Due31, total: total, finalized: fc1At(10, 9, 0)})
		l.apply(c, inv)
	}

	// The aging as of the month's last instant: the balance more than 30 days past due is what the
	// run must charge on.
	monthEnd := time.Date(2026, 9, 30, 23, 59, 59, 999_999_000, fc1Loc())
	agingRows, err := NewCreditService(NewRepository(r.pool)).Aging(r.ctx, monthEnd, r.branch)
	if err != nil {
		t.Fatalf("read the aging: %v", err)
	}
	agingPast := map[string]int64{}
	for _, row := range agingRows {
		agingPast[row.CustomerID] = row.Days60Cents + row.Days60To90Cents + row.Days90PlusCents
	}

	if _, err := r.writer(nil).RunFinanceCharges(r.ctx, FinanceChargeRequest{BranchID: r.branch, Month: fc1Month}); err != nil {
		t.Fatalf("the run: %v", err)
	}
	rows := r.chargeRows(r.branch)
	for _, l := range legs {
		c := custs[l.label]
		if agingPast[c] != l.bal {
			t.Fatalf("%s: the aging's balance past 30 days is %d, the leg expects %d", l.label, agingPast[c], l.bal)
		}
		want := int64(0)
		if l.bal > 0 {
			want = roundHalfEvenDiv(l.bal*15, 1000)
			if want < 200 {
				want = 200
			}
		}
		got, charged := rows[c]
		if want == 0 && charged {
			t.Fatalf("%s: charged %d on a settled invoice", l.label, got)
		}
		if want > 0 && got != want {
			t.Fatalf("%s: charged %d, want %d (1.5 percent of the aging's %d)", l.label, got, want, l.bal)
		}
	}
}
