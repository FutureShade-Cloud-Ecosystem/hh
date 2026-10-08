//go:build integration

package pos

// return_supplier_deposit_integration_test.go, the return of a supplier
// deposit sale (review P3-6): a special order rung with the supplier's deposit
// is returned, before the goods leave and after. Any return releases the whole
// deposit once (the supervisor's default; the principal may rule otherwise),
// and the return's credit lands on the deposit invoice rather than leaving it
// open beside a store credit. The books go through the REAL gl consumer.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

func drainAll(t *testing.T, d *notify.Dispatcher) {
	t.Helper()
	for i := 0; i < 16; i++ {
		if d.DrainOnce(context.Background()) == 0 {
			return
		}
	}
	t.Fatalf("outbox did not quiesce after 16 drain passes")
}

// depositSaleReturn rings the deposit sale, finalises its documents through
// the real consumer, optionally releases it as a fulfilment would, returns the
// whole sale to the account, drains, and reads the books.
func depositSaleReturn(t *testing.T, fulfilFirst bool) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	productID, branchID := aPaver(t, pool)
	suffix := fmt.Sprintf("sdr-%d", time.Now().UnixNano()%1_000_000_000)
	var custID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
		VALUES ('SDR-'||$1, 'Supplier deposit return proof', 'CONTRACTOR', 30, 5000000) RETURNING id::text`,
		suffix).Scan(&custID); err != nil {
		t.Fatalf("plant the customer: %v", err)
	}
	svc := realPosSvc(t, pool, stubCharger{}).WithRoleConfig(defaultConfig(t))
	out, err := svc.Sale(ctx, SaleInput{
		IdempotencyKey: key(), BranchID: branchID, CustomerID: custID,
		SaleType: SaleCollectLater, Tender: TenderAccount,
		SupplierDepositCents: 4000,
		Lines:                []LineInput{{ProductID: productID, QtyEntered: 5, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("the ring: %v", err)
	}
	dep := out.SupplierDepositInvoiceID
	var returnID string
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE (payload->>'invoice_id')::uuid = ANY($1::uuid[]) OR payload->>'order_id' = $2`, []string{dep, out.InvoiceID}, out.OrderID)
		for _, k := range []string{"deposit.released:" + dep, "ar.invoice.finalized:" + dep, "ar.invoice.finalized:" + out.InvoiceID, "pos.return.committed:" + returnID} {
			_, _ = pool.Exec(c, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id = j.id AND j.idempotency_key = $1`, k)
			_, _ = pool.Exec(c, `DELETE FROM gl_journals WHERE idempotency_key = $1`, k)
		}
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1`, custID)
		_, _ = pool.Exec(c, `DELETE FROM pos_return_lines WHERE return_id IN (SELECT id FROM pos_returns WHERE order_id = $1)`, out.OrderID)
		_, _ = pool.Exec(c, `DELETE FROM pos_returns WHERE order_id = $1`, out.OrderID)
		_, _ = pool.Exec(c, `DELETE FROM invoice_lines WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1)`, custID)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1`, custID)
		_, _ = pool.Exec(c, `DELETE FROM pos_sale WHERE order_id = $1`, out.OrderID)
		_, _ = pool.Exec(c, `DELETE FROM order_lines WHERE order_id = $1`, out.OrderID)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1`, out.OrderID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, custID)
	})

	billingRepo := billing.NewRepository(pool)
	glSvc := gl.NewService(gl.NewRepository(pool)).WithInvoiceReader(billingRepo)
	dispatcher := notify.NewDispatcher(notify.NewPgOutboxStore(pool), nil)
	glSvc.RegisterPostingHandlers(dispatcher)
	drainAll(t, dispatcher)

	if fulfilFirst {
		// The fulfilment's release, exactly the event billing's seam writes.
		if err := billing.NewInvoiceWriter(pool).ReleaseDeposits(ctx, out.OrderID); err != nil {
			t.Fatalf("the fulfilment release: %v", err)
		}
		drainAll(t, dispatcher)
	}

	var lineID string
	var qty float64
	if err := pool.QueryRow(ctx, `SELECT id::text, qty_base FROM order_lines WHERE order_id = $1 ORDER BY line_no LIMIT 1`, out.OrderID).Scan(&lineID, &qty); err != nil {
		t.Fatalf("read the sale's line: %v", err)
	}
	ret, err := svc.Return(ctx, ReturnInput{
		IdempotencyKey: key(), OrderID: out.OrderID,
		Lines:        []ReturnLineInput{{OrderLineID: lineID, QtyBase: qty, ReasonCode: "OTHER"}},
		RefundMethod: RefundAccountCredit, Reason: "special order cancelled",
	})
	if err != nil {
		t.Fatalf("the return: %v", err)
	}
	returnID = ret.ReturnID
	drainAll(t, dispatcher)

	net := func(code string) int64 {
		var n int64
		if err := pool.QueryRow(ctx, `
			SELECT COALESCE(SUM(COALESCE(l.debit_cents,0) - COALESCE(l.credit_cents,0)), 0)
			  FROM gl_journal_lines l
			  JOIN gl_journals j ON j.id = l.journal_id
			  JOIN gl_accounts a ON a.id = l.account_id
			 WHERE j.idempotency_key IN ($1, $2, $3, $4) AND a.code = $5`,
			"ar.invoice.finalized:"+dep, "ar.invoice.finalized:"+out.InvoiceID, "deposit.released:"+dep,
			"pos.return.committed:"+ret.ReturnID, code).Scan(&n); err != nil {
			t.Fatalf("net %s: %v", code, err)
		}
		return n
	}
	if got := net("4000"); got != 0 {
		t.Fatalf("revenue nets %d after the return, want zero", got)
	}
	if got := net("2300"); got != 0 {
		t.Fatalf("2300 nets %d after the return, want zero", got)
	}
	var open int64
	if err := pool.QueryRow(ctx, `SELECT COALESCE(SUM(balance_cents),0) FROM invoices WHERE customer_id = $1`, custID).Scan(&open); err != nil {
		t.Fatal(err)
	}
	if open != 0 {
		t.Fatalf("%d cents still open on the customer's invoices (the deposit invoice must be credited), want none", open)
	}
	var stray int64
	if err := pool.QueryRow(ctx, `SELECT COALESCE(SUM(amount_cents),0) FROM customer_transactions
		 WHERE customer_id = $1 AND source_type = 'pos_return'`, custID).Scan(&stray); err != nil {
		t.Fatal(err)
	}
	if stray != 0 {
		t.Fatalf("a stray store credit of %d stands beside the credited deposit invoice, want none", stray)
	}
	var jn int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gl_journals WHERE idempotency_key = $1`, "deposit.released:"+dep).Scan(&jn); err != nil || jn != 1 {
		t.Fatalf("the release stands at %d journals (%v), want exactly the one", jn, err)
	}
	var balance int64
	if err := pool.QueryRow(ctx, `SELECT COALESCE(SUM(amount_cents),0) FROM customer_transactions WHERE customer_id = $1`, custID).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if balance != 0 {
		t.Fatalf("the customer's subledger nets %d, want zero", balance)
	}
}

func TestIntegration_SupplierDeposit_ReturnBeforeFulfilment(t *testing.T) {
	depositSaleReturn(t, false)
}

func TestIntegration_SupplierDeposit_ReturnAfterFulfilment(t *testing.T) { depositSaleReturn(t, true) }
