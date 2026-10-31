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

	var out, cashOut Sale
	t.Cleanup(func() {
		c := context.Background()
		for _, s := range []Sale{out, cashOut} {
			if s.InvoiceID != "" {
				_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE payload->>'invoice_id' = $1 OR payload->>'order_id' = $2`, s.InvoiceID, s.OrderID)
				_, _ = pool.Exec(c, `DELETE FROM payment_allocations WHERE invoice_id = $1::uuid`, s.InvoiceID)
			}
			if s.PaymentID != "" {
				_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE payload->>'payment_id' = $1`, s.PaymentID)
			}
			if s.OrderID != "" {
				_, _ = pool.Exec(c, `DELETE FROM pos_sale_tenders WHERE pos_sale_id IN (SELECT id FROM pos_sale WHERE order_id = $1::uuid)`, s.OrderID)
				_, _ = pool.Exec(c, `DELETE FROM pos_sale WHERE order_id = $1::uuid`, s.OrderID)
			}
			if s.PaymentID != "" {
				_, _ = pool.Exec(c, `DELETE FROM payments WHERE id = $1::uuid`, s.PaymentID)
			}
		}
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, custID)
		_, _ = pool.Exec(c, `DELETE FROM invoice_lines WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`, custID)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1::uuid`, custID)
		_, _ = pool.Exec(c, `DELETE FROM order_lines WHERE order_id IN (SELECT id FROM orders WHERE customer_id = $1::uuid)`, custID)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE customer_id = $1::uuid`, custID)
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
		t.Fatalf("Sale (account): %v", err)
	}

	// Cash leg beside the account leg (Ruling 128 P1-3): a fully paid counter sale to a named
	// customer posts ar.invoice.finalized (Dr 1100) and payment.received (Cr 1100), so the
	// subledger must write the INVOICE (+total) and PAYMENT (-total) pair, keeping SUM(amount_cents)
	// equal to the customer's open balance and every row chaining by balance_after = prev + amount_cents.
	cashOut, err = svc.Sale(ctx, SaleInput{
		IdempotencyKey: key(),
		BranchID:       branchID,
		TillSessionID:  sessionID,
		CustomerID:     custID,
		SaleType:       SaleCollectNow,
		Tender:         TenderCash,
		Lines:          []LineInput{{ProductID: productID, QtyEntered: 5, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("Sale (cash): %v", err)
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
	if len(got) != 4 {
		t.Fatalf("customer_transactions len = %d, want 4 (account INVOICE+PAYMENT then cash INVOICE+PAYMENT): %+v", len(got), got)
	}
	if got[0].txnType != "INVOICE" || got[0].amountCents != out.TotalCents || got[0].balanceAfter != out.TotalCents {
		t.Fatalf("row 0 = %+v, want INVOICE amount=%d balance_after=%d", got[0], out.TotalCents, out.TotalCents)
	}
	if got[1].txnType != "PAYMENT" || got[1].amountCents != -depositCents {
		t.Fatalf("PAYMENT row has wrong sign: amount_cents = %d (balance_after = %d), want %d",
			got[1].amountCents, got[1].balanceAfter, -depositCents)
	}
	if got[2].txnType != "INVOICE" || got[2].amountCents != cashOut.TotalCents {
		t.Fatalf("row 2 = %+v, want cash sale INVOICE amount=%d", got[2], cashOut.TotalCents)
	}
	if got[3].txnType != "PAYMENT" || got[3].amountCents != -cashOut.TotalCents {
		t.Fatalf("row 3 = %+v, want cash sale PAYMENT amount=%d", got[3], -cashOut.TotalCents)
	}
	var running int64
	for i, r := range got {
		running += r.amountCents
		if r.balanceAfter != running {
			t.Fatalf("row %d (%s): balance_after = %d, want prev + amount_cents = %d", i, r.txnType, r.balanceAfter, running)
		}
	}
	wantOpen := out.TotalCents - depositCents
	if running != wantOpen {
		t.Fatalf("SUM(amount_cents) = %d, want open balance %d", running, wantOpen)
	}
}
