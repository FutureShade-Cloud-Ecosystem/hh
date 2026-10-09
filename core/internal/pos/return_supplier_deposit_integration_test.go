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
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/jackc/pgx/v5/pgxpool"
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
	depositSaleReturnLeg(t, depositLeg{fulfilFirst: fulfilFirst})
}

// depositLeg shapes one return leg: partial returns the first of two lines
// only, refund picks the rail (zero is ACCOUNT_CREDIT).
type depositLeg struct {
	fulfilFirst, partial bool
	refund               string
}

func depositSaleReturnLeg(t *testing.T, leg depositLeg) {
	fulfilFirst := leg.fulfilFirst
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
	lines := []LineInput{{ProductID: productID, QtyEntered: 5, UOMEntered: "SF"}}
	if leg.partial {
		lines = append(lines, LineInput{ProductID: productID, QtyEntered: 3, UOMEntered: "SF"})
	}
	out, err := svc.Sale(ctx, SaleInput{
		IdempotencyKey: key(), BranchID: branchID, CustomerID: custID,
		SaleType: SaleCollectLater, Tender: TenderAccount,
		SupplierDepositCents: 4000,
		Lines:                lines,
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
	refund := leg.refund
	if refund == "" {
		refund = RefundAccountCredit
	}
	in := ReturnInput{
		IdempotencyKey: key(), OrderID: out.OrderID,
		Lines:        []ReturnLineInput{{OrderLineID: lineID, QtyBase: qty, ReasonCode: "OTHER"}},
		RefundMethod: refund, Reason: "special order cancelled",
	}
	if refund == RefundCash {
		in.TillSessionID = openTill(t, pool, branchID)
	}
	ret, err := svc.Return(ctx, in)
	if refund == RefundCash {
		// The principal's ruling (FROM-PRINCIPAL 47): nothing was paid on either document of this
		// unpaid account sale, so no cash or card goes back at all, in the ruling's words. This leg
		// used to pin the gap (the cash paid out and both documents stayed open); it now pins the
		// refusal, and that nothing was booked.
		if err == nil || !strings.Contains(err.Error(), "This order is not paid yet") {
			t.Fatalf("a cash refund of an unpaid account sale must be refused in words, got %v", err)
		}
		var n int
		if qerr := pool.QueryRow(ctx, `SELECT count(*) FROM pos_returns WHERE order_id = $1`, out.OrderID).Scan(&n); qerr != nil || n != 0 {
			t.Fatalf("the refused cash refund booked %d returns (%v)", n, qerr)
		}
		return
	}
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
	if leg.partial {
		assertPartialDepositReturn(t, pool, ctx, out, ret, lineID, custID, dep, refund, net)
		return
	}
	if refund != RefundAccountCredit {
		t.Fatalf("the whole return legs run on the account credit rail only")
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

func TestIntegration_SupplierDeposit_PartialReturnAfterFulfilment(t *testing.T) {
	depositSaleReturnLeg(t, depositLeg{fulfilFirst: true, partial: true})
}

func TestIntegration_SupplierDeposit_PartialCashRefundAfterFulfilment(t *testing.T) {
	depositSaleReturnLeg(t, depositLeg{fulfilFirst: true, partial: true, refund: RefundCash})
}

// assertPartialDepositReturn pins the principal's ruling 17: a partial return
// releases the whole deposit once, and what the customer gets back is the
// returned goods' value through the normal return path, never a share of the
// deposit. Revenue nets to the sale's pre tax total minus exactly the returned
// goods' pre tax value, 2300 is zero, the trial balance is zero, one release
// journal stands, and no store credit rides the account rail while the
// deposit invoice can absorb what the rest invoice could not: the return's
// credit offsets the sale's own rest invoice first, and only the part that
// rest invoice could not absorb is applied to the deposit document, so the
// deposit invoice's balance equals its amount minus exactly that unabsorbed
// part (zero when the rest invoice took the whole credit). The figures are
// computed from the database's own rows, never from the code under test, so
// the pin holds on any seeded line the ring prices, dear or cheap: which
// product the fixture lands on is the database's choice, not the test's.
func assertPartialDepositReturn(t *testing.T, pool *pgxpool.Pool, ctx context.Context, out Sale, ret Return, lineID, custID, dep string, refund string, net func(string) int64) {
	t.Helper()
	var saleSub int64
	if err := pool.QueryRow(ctx, `SELECT subtotal_cents FROM orders WHERE id = $1`, out.OrderID).Scan(&saleSub); err != nil {
		t.Fatalf("read the sale's pre tax total: %v", err)
	}
	var returnedGoods int64
	if err := pool.QueryRow(ctx, `SELECT COALESCE(SUM(line_total_cents),0) FROM pos_return_lines WHERE return_id = $1`, ret.ReturnID).Scan(&returnedGoods); err != nil {
		t.Fatalf("read the returned goods' value: %v", err)
	}
	if returnedGoods <= 0 || returnedGoods >= saleSub {
		t.Fatalf("the leg must return one line of several: returned %d of %d", returnedGoods, saleSub)
	}
	// Revenue is a credit balance, so its net is negative.
	if got := -net("4000"); got != saleSub-returnedGoods {
		t.Fatalf("revenue nets %d, want the sale's %d minus the returned goods' %d", got, saleSub, returnedGoods)
	}
	if got := net("2300"); got != 0 {
		t.Fatalf("2300 nets %d after the partial return, want zero", got)
	}
	var tb int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(COALESCE(l.debit_cents,0) - COALESCE(l.credit_cents,0)), 0)
		  FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id
		 WHERE j.idempotency_key IN ($1, $2, $3, $4)`,
		"ar.invoice.finalized:"+dep, "ar.invoice.finalized:"+out.InvoiceID, "deposit.released:"+dep,
		"pos.return.committed:"+ret.ReturnID).Scan(&tb); err != nil {
		t.Fatal(err)
	}
	if tb != 0 {
		t.Fatalf("the trial balance of the sale's journals is %d, want zero", tb)
	}
	var jn int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gl_journals WHERE idempotency_key = $1`, "deposit.released:"+dep).Scan(&jn); err != nil || jn != 1 {
		t.Fatalf("the release stands at %d journals (%v), want exactly the one", jn, err)
	}
	var restBalance, restTotal int64
	if err := pool.QueryRow(ctx, `SELECT balance_cents, total_cents FROM invoices WHERE id = $1`, out.InvoiceID).Scan(&restBalance, &restTotal); err != nil {
		t.Fatal(err)
	}
	var depBalance, depTotal int64
	if err := pool.QueryRow(ctx, `SELECT balance_cents, total_cents FROM invoices WHERE id = $1`, dep).Scan(&depBalance, &depTotal); err != nil {
		t.Fatal(err)
	}
	if refund == RefundCash {
		// The cash rail pays the credit out of the drawer: neither document
		// moves, whatever the seeded line priced.
		if restBalance != restTotal || depBalance != depTotal {
			t.Fatalf("after the cash refund the invoices stand at %d of %d and %d of %d, want both open at their amounts", restBalance, restTotal, depBalance, depTotal)
		}
	} else {
		var credit int64
		if err := pool.QueryRow(ctx, `SELECT COALESCE(SUM(line_total_cents + tax_cents),0) FROM pos_return_lines WHERE return_id = $1`, ret.ReturnID).Scan(&credit); err != nil {
			t.Fatalf("read the return's credit: %v", err)
		}
		// The account sale pays nothing at the ring, so the rest invoice opens
		// at its total: the balance it held before this return's credit
		// landed. What it could not absorb is applied to the deposit.
		wantRest := restTotal - credit
		if wantRest < 0 {
			wantRest = 0
		}
		if restBalance != wantRest {
			t.Fatalf("the rest invoice stands at %d, want %d, its total %d minus the return's credit %d floored at zero", restBalance, wantRest, restTotal, credit)
		}
		unabsorbed := credit - restTotal
		if unabsorbed < 0 {
			unabsorbed = 0
		}
		if depBalance != depTotal-unabsorbed {
			t.Fatalf("the deposit invoice stands at %d of %d, want %d: the return's credit %d held %d beyond the rest invoice's %d and the excess is applied to the deposit", depBalance, depTotal, depTotal-unabsorbed, credit, unabsorbed, restTotal)
		}
	}
	var stray int64
	if err := pool.QueryRow(ctx, `SELECT COALESCE(SUM(amount_cents),0) FROM customer_transactions
		 WHERE customer_id = $1 AND source_type = 'pos_return'`, custID).Scan(&stray); err != nil {
		t.Fatal(err)
	}
	if stray != 0 {
		t.Fatalf("a stray store credit of %d stands after the %s return, want none", stray, refund)
	}
}

// The return itself releases the deposit when no fulfilment did (ruling 17: any return, partial or
// whole, releases the whole deposit once).
func TestIntegration_SupplierDeposit_PartialReturnBeforeFulfilment(t *testing.T) {
	depositSaleReturnLeg(t, depositLeg{partial: true})
}
