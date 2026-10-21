//go:build integration

package payments_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// SF-3's concurrency leg (SF-2p): a credit memo beside a register settle on the same customer's invoice. The memo
// takes the reversed invoice FOR NO KEY UPDATE before its insert and draws the invoices counter at the insert (the
// 0422 trigger); the settle takes the payments counter, then the invoice row, then the invoices counter
// (LOCK-ORDER.md steps 2 to 4). The settle is the parked session (testkit.DrawPark on payments, at its payments
// counter draw); the memo runs while it stands there. Neither deadlocks, both commit, and the memo carries the
// trigger's seq.
func TestIntegration_SF2p_ACreditMemoBesideARegisterSettleOnTheSameInvoice(t *testing.T) {
	r := newLORig(t, 4)
	ctx := context.Background()
	inv := r.invoice("SF2P", 1_000_000)
	park := testkit.NewDrawPark(t, r.pool, "payments", 55010901)
	settle := make(chan error, 1)
	go func() {
		reg := billing.NewRegisterService(billing.NewRepository(park.Pool)).
			WithMoney(billing.NewPaymentsReceiptWriter(payments.NewRepository(park.Pool)))
		_, err := reg.RecordPayment(ctx, billing.ReceiptInput{
			InvoiceID: inv, Method: "CHEQUE", AmountCents: 500, Reference: "chq sf2p",
			IdempotencyKey: fmt.Sprintf("sf2p-%d", time.Now().UnixNano()),
		})
		settle <- err
	}()
	park.WaitParked("the settle holds the payments counter at its draw")

	var memoDone atomic.Bool
	memo := make(chan error, 1)
	var memoID string
	go func() {
		defer memoDone.Store(true)
		doc, err := billing.NewInvoiceWriter(r.pool).IssueCreditMemo(ctx, billing.CreditMemoInput{
			CustomerID: r.custID, BranchID: r.branchID, ReversesInvoiceID: inv, AmountCents: 2000,
			Memo: "sf2p beside a settle", IdempotencyKey: fmt.Sprintf("sf2p-memo-%d", time.Now().UnixNano()),
		})
		memoID = doc.InvoiceID
		memo <- err
	}()
	park.WaitBlockedOr("the memo queues behind the settle, or has already committed", memoDone.Load)
	park.Answers(map[string]chan error{"settle": settle, "memo": memo})

	var seq *int64
	if err := r.obs.QueryRow(ctx, `SELECT seq FROM invoices WHERE id = $1::uuid`, memoID).Scan(&seq); err != nil || seq == nil {
		t.Errorf("the memo carries seq %v (%v), want the trigger's draw", seq, err)
	}
	var payments int
	_ = r.obs.QueryRow(ctx, `SELECT count(*) FROM payment_allocations WHERE invoice_id = $1::uuid`, inv).Scan(&payments)
	if payments != 1 {
		t.Errorf("the settle left %d allocation(s) on the invoice, want 1", payments)
	}
}
