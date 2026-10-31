//go:build integration

package pos

// sale_replay_deposit_integration_test.go, item 9 delta review P3-a: a
// replayed ring (a lost acknowledgement retried) answers VERBATIM, and that
// includes the deposit facts the Summary prints. The counter deposit
// (deposit_cents) and the supplier's demanded deposit
// (supplier_deposit_cents and its deposit document's number) are read back
// from the order metadata and the SUPPLIER_DEPOSIT invoice, so the Summary
// keeps its lines after a lost answer instead of dropping them.

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestIntegration_SaleReplay_EchoesBothDeposits(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	productID, branchID := aPaver(t, pool)
	suffix := fmt.Sprintf("rp-%d", time.Now().UnixNano()%1_000_000_000)

	// The supplier's demanded deposit on a credit customer's on account ring.
	var creditID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
		VALUES ('RP-'||$1, 'Replay deposit proof', 'CONTRACTOR', 30, 5000000) RETURNING id::text`,
		suffix).Scan(&creditID); err != nil {
		t.Fatalf("plant the credit customer: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1`, creditID)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE customer_id = $1`, creditID)
	})
	svc := realPosSvc(t, pool, stubCharger{})
	supplierKey := key()
	in := SaleInput{
		IdempotencyKey: supplierKey, BranchID: branchID, CustomerID: creditID,
		SaleType: SaleCollectLater, Tender: TenderAccount,
		SupplierDepositCents: 4000,
		Lines:                []LineInput{{ProductID: productID, QtyEntered: 5, UOMEntered: "SF"}},
	}
	first, err := svc.Sale(ctx, in)
	if err != nil {
		t.Fatalf("the supplier deposit ring: %v", err)
	}
	again, err := svc.Sale(ctx, in)
	if err != nil {
		t.Fatalf("the supplier deposit replay: %v", err)
	}
	if !again.Replayed || again.OrderID != first.OrderID {
		t.Fatalf("the replay did not answer the first ring: %+v vs %+v", again, first)
	}
	if again.SupplierDepositCents != first.SupplierDepositCents || again.SupplierDepositCents != 4000 {
		t.Fatalf("the replay's supplier deposit is %d, want the rung %d",
			again.SupplierDepositCents, first.SupplierDepositCents)
	}
	if again.SupplierDepositInvoiceID != first.SupplierDepositInvoiceID ||
		again.SupplierDepositInvoiceNo != first.SupplierDepositInvoiceNo || again.SupplierDepositInvoiceNo == "" {
		t.Fatalf("the replay's deposit document is %q/%q, want the rung %q/%q",
			again.SupplierDepositInvoiceID, again.SupplierDepositInvoiceNo,
			first.SupplierDepositInvoiceID, first.SupplierDepositInvoiceNo)
	}

	// The counter deposit on a pay before release ring: a zero terms customer
	// with a credit line (the gate's own posture; the walk-in's zero line would
	// refuse the ring for other reasons).
	var codID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
		VALUES ('RPC-'||$1, 'Replay counter deposit proof', 'CONTRACTOR', 0, 5000000) RETURNING id::text`,
		suffix).Scan(&codID); err != nil {
		t.Fatalf("plant the zero terms customer: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1`, codID)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE customer_id = $1`, codID)
	})
	counterKey := key()
	codIn := SaleInput{
		IdempotencyKey: counterKey, BranchID: branchID, CustomerID: codID,
		SaleType: SaleCOD, Fulfilment: "WILL_CALL", Tender: TenderCash,
		DepositCents: 2000, DepositMethod: TenderCash, TillSessionID: openTill(t, pool, branchID),
		Lines: []LineInput{{ProductID: productID, QtyEntered: 3, UOMEntered: "SF"}},
	}
	codFirst, err := svc.Sale(ctx, codIn)
	if err != nil {
		t.Fatalf("the counter deposit ring: %v", err)
	}
	codAgain, err := svc.Sale(ctx, codIn)
	if err != nil {
		t.Fatalf("the counter deposit replay: %v", err)
	}
	if !codAgain.Replayed || codAgain.OrderID != codFirst.OrderID {
		t.Fatalf("the counter deposit replay did not answer the first ring: %+v vs %+v", codAgain, codFirst)
	}
	if codAgain.DepositCents != codFirst.DepositCents || codAgain.DepositCents != 2000 {
		t.Fatalf("the replay's counter deposit is %d, want the rung %d",
			codAgain.DepositCents, codFirst.DepositCents)
	}
}
