//go:build integration

package pos

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestIntegration_FW1_06a_CounterSaleSubledgerSign proves that a counter sale's PAYMENT row in
// customer_transactions carries a negative amount_cents (-c.AmountCents) so that every row in the
// subledger chains by balance_after = prev + amount_cents and SUM(amount_cents) equals the customer's
// open balance.
func TestIntegration_FW1_06a_CounterSaleSubledgerSign(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	productID, branchID := aPaver(t, pool)
	sessionID := openTill(t, pool, branchID)

	suffix := fmt.Sprintf("fw106a-%d", time.Now().UnixNano()%1_000_000_000)
	var custID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
		VALUES ('FW106A-'||$1, 'FW1-06a Subledger Sign Co', 'CONTRACTOR', 30, 5000000) RETURNING id::text`,
		suffix).Scan(&custID); err != nil {
		t.Fatalf("plant customer: %v", err)
	}

	var out Sale
	t.Cleanup(func() {
		c := context.Background()
		if out.InvoiceID != "" {
			_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE payload->>'invoice_id' = $1 OR payload->>'order_id' = $2`, out.InvoiceID, out.OrderID)
			_, _ = pool.Exec(c, `DELETE FROM payment_allocations WHERE invoice_id = $1::uuid`, out.InvoiceID)
		}
		if out.PaymentID != "" {
			_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE payload->>'payment_id' = $1`, out.PaymentID)
		}
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, custID)
		if out.OrderID != "" {
			_, _ = pool.Exec(c, `DELETE FROM pos_sale_tenders WHERE pos_sale_id IN (SELECT id FROM pos_sale WHERE order_id = $1::uuid)`, out.OrderID)
			_, _ = pool.Exec(c, `DELETE FROM pos_sale WHERE order_id = $1::uuid`, out.OrderID)
		}
		if out.PaymentID != "" {
			_, _ = pool.Exec(c, `DELETE FROM payments WHERE id = $1::uuid`, out.PaymentID)
		}
		_, _ = pool.Exec(c, `DELETE FROM invoice_lines WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`, custID)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1::uuid`, custID)
		_, _ = pool.Exec(c, `DELETE FROM order_lines WHERE order_id IN (SELECT id FROM orders WHERE customer_id = $1::uuid)`, custID)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE customer_id = $1::uuid`, custID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, custID)
	})

	const depositCents = int64(2000)
	svc := realPosSvc(t, pool, stubCharger{})
	var err error
	out, err = svc.Sale(ctx, SaleInput{
		IdempotencyKey: key(),
		BranchID:       branchID,
		TillSessionID:  sessionID,
		CustomerID:     custID,
		SaleType:       SaleCollectLater,
		Tender:         TenderAccount,
		DepositCents:   depositCents,
		DepositMethod:  TenderCash,
		Lines:          []LineInput{{ProductID: productID, QtyEntered: 5, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("Sale: %v", err)
	}

	rows, err := pool.Query(ctx, `
		SELECT txn_type, amount_cents, balance_after
		FROM customer_transactions
		WHERE customer_id = $1::uuid
		ORDER BY occurred_at, id`, custID)
	if err != nil {
		t.Fatalf("query customer_transactions: %v", err)
	}
	defer rows.Close()

	type row struct {
		txnType      string
		amountCents  int64
		balanceAfter int64
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.txnType, &r.amountCents, &r.balanceAfter); err != nil {
			t.Fatalf("scan customer_transactions: %v", err)
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("customer_transactions len = %d, want 2 (INVOICE + PAYMENT): %+v", len(got), got)
	}
	if got[0].txnType != "INVOICE" || got[0].amountCents != out.TotalCents || got[0].balanceAfter != out.TotalCents {
		t.Fatalf("row 0 = %+v, want INVOICE amount=%d balance_after=%d", got[0], out.TotalCents, out.TotalCents)
	}
	if got[1].txnType != "PAYMENT" || got[1].amountCents != -depositCents {
		t.Fatalf("PAYMENT row has wrong sign: amount_cents = %d (balance_after = %d), want %d",
			got[1].amountCents, got[1].balanceAfter, -depositCents)
	}
	var running int64
	for i, r := range got {
		running += r.amountCents
		if r.balanceAfter != running {
			t.Fatalf("row %d (%s): balance_after = %d, want prev + amount_cents = %d", i, r.txnType, r.balanceAfter, running)
		}
	}
}
