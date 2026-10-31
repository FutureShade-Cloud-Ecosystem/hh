//go:build integration

// register_discount_totals_integration_test.go, TM-2 fix round, P3-5: the register's totals name the
// early payment discount, so total, applied, discount and balance add up on a discounted invoice.
//
// Run: go test -tags integration ./internal/billing/ -run RegisterDiscountTotals
package billing

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

func TestRegisterDiscountTotals_TotalAppliedDiscountAndBalanceAddUp(t *testing.T) {
	w := newApplyRouteWorld(t)
	today := time.Now().In(houseday.DefaultZone().Location()).Format("2006-01-02")
	var inv string
	if err := w.pool.QueryRow(context.Background(),
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date, discount_date, terms_code,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		 VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', $4::date, $4::date, $4::date, '2-10-NET30',
		         100000, 0, 100000, 100000, now(), 'INVOICE') RETURNING id::text`,
		w.tag+"-DT", w.customerID, trentonBranch, today).Scan(&inv); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}
	code, body := decodeApply(t, w.post(t, applyGlobalCaller(), map[string]any{
		"customer_id": w.customerID, "method": "cheque", "amount_cents": 98000,
		"reference": "CHQ 940", "invoice_ids": []string{inv}, "idempotency_key": w.tag + "-dt",
	}))
	if code != http.StatusOK {
		t.Fatalf("apply: %d %v", code, body)
	}
	d, err := w.svc.Detail(context.Background(), inv, true)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	tot := d.Totals
	if tot.TotalCents != 100000 || tot.AppliedCents != 98000 || tot.DiscountCents != 2000 || tot.BalanceCents != 0 {
		t.Errorf("want total 100000, applied 98000, discount 2000, balance 0; got %+v", tot)
	}
	if tot.TotalCents-tot.AppliedCents-tot.DiscountCents != tot.BalanceCents {
		t.Errorf("total less applied less discount is %d, the balance reads %d", tot.TotalCents-tot.AppliedCents-tot.DiscountCents, tot.BalanceCents)
	}
}
