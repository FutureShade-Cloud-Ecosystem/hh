//go:build integration

package pos

// sale_supplier_deposit_integration_test.go, sub item 9.5: the supplier's
// demanded deposit on a special order, billed to a credit customer's account
// against the real database. The ring books TWO documents (the deposit's
// invoice and the rest's), the subledger carries both INVOICE rows, and both
// documents tell the GL consumer they finalized; the journal legs themselves
// are proven in gl's own suite (TestSupplierDepositInvoicePostsToCustomerDeposits).

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestIntegration_SupplierDeposit_BilledToTheCreditAccount rings a real
// on-account sale with the supplier's deposit marked on it and proves the
// booking end to end: the two invoices and their facts, the subledger rows,
// the events, the metadata.
func TestIntegration_SupplierDeposit_BilledToTheCreditAccount(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	productID, branchID := aPaver(t, pool)
	suffix := fmt.Sprintf("sd-%d", time.Now().UnixNano()%1_000_000_000)
	var custID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
		VALUES ('SD-'||$1, 'Supplier deposit proof', 'CONTRACTOR', 30, 5000000) RETURNING id::text`,
		suffix).Scan(&custID); err != nil {
		t.Fatalf("plant the customer: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1`, custID)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE customer_id = $1`, custID)
	})
	svc := realPosSvc(t, pool, stubCharger{})
	out, err := svc.Sale(ctx, SaleInput{
		IdempotencyKey: key(), BranchID: branchID, CustomerID: custID,
		SaleType: SaleCollectLater, Tender: TenderAccount,
		SupplierDepositCents: 4000,
		Lines:                []LineInput{{ProductID: productID, QtyEntered: 5, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("the ring: %v", err)
	}
	if out.SupplierDepositCents != 4000 || out.SupplierDepositInvoiceID == "" || out.SupplierDepositInvoiceNo == "" {
		t.Fatalf("the answer's supplier deposit facts: %+v", out)
	}
	// THE TWO DOCUMENTS: the deposit's (FINALIZED, its own total, the source
	// kind marking it) and the rest's (FINALIZED, the total less the deposit,
	// its balance the account carries on terms).
	var depTotal, depBalance, restTotal, restBalance int64
	var depStatus, depKind, restStatus, restKind string
	if err := pool.QueryRow(ctx,
		`SELECT total_cents, balance_cents, status::text, source_kind FROM invoices WHERE id = $1`,
		out.SupplierDepositInvoiceID).Scan(&depTotal, &depBalance, &depStatus, &depKind); err != nil {
		t.Fatalf("read the deposit invoice: %v", err)
	}
	if depTotal != 4000 || depBalance != 4000 || depStatus != "FINALIZED" || depKind != "SUPPLIER_DEPOSIT" {
		t.Fatalf("the deposit invoice reads %d/%d %s %s", depTotal, depBalance, depStatus, depKind)
	}
	if err := pool.QueryRow(ctx,
		`SELECT total_cents, balance_cents, status::text, source_kind FROM invoices WHERE id = $1`,
		out.InvoiceID).Scan(&restTotal, &restBalance, &restStatus, &restKind); err != nil {
		t.Fatalf("read the rest's invoice: %v", err)
	}
	if restTotal != out.TotalCents-4000 || restBalance != out.TotalCents-4000 || restStatus != "FINALIZED" || restKind != "" {
		t.Fatalf("the rest's invoice reads %d/%d %s %q, want the total less the deposit at FINALIZED",
			restTotal, restBalance, restStatus, restKind)
	}
	// The documents' lines: the deposit's one line carries the deposit, and
	// the rest's are the goods lines at their real prices plus the zero line
	// naming the deposit document (the schema forbids a negative line, so the
	// deposit is never struck off a goods line; the header carries the rest).
	var depLines int64
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(line_total_cents),0) FROM invoice_lines WHERE invoice_id = $1`,
		out.SupplierDepositInvoiceID).Scan(&depLines); err != nil || depLines != 4000 {
		t.Fatalf("the deposit invoice's lines sum to %d (%v), want 4000", depLines, err)
	}
	var pointerCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM invoice_lines WHERE invoice_id = $1
		  AND line_total_cents = 0 AND description LIKE 'Supplier deposit of %, billed on its own invoice %, is not part of this invoice''s total'`,
		out.InvoiceID).Scan(&pointerCount); err != nil || pointerCount != 1 {
		t.Fatalf("the rest's invoice carries %d deposit pointer lines (%v), want the one", pointerCount, err)
	}
	// The subledger: two INVOICE rows summing to the sale's whole total.
	var invoiced int64
	var invoiceRows int
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_cents),0), count(*) FROM customer_transactions
		 WHERE customer_id = $1::uuid AND txn_type = 'INVOICE'`, custID).Scan(&invoiced, &invoiceRows); err != nil ||
		invoiceRows != 2 || invoiced != out.TotalCents {
		t.Fatalf("the subledger carries %d INVOICE rows summing %d, want two summing %d",
			invoiceRows, invoiced, out.TotalCents)
	}
	// Both documents finalized for the GL consumer: two outbox rows.
	var finalized int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM events_outbox e
		  JOIN (SELECT id FROM invoices WHERE customer_id = $1::uuid) i
		    ON (e.payload->>'invoice_id')::uuid = i.id`, custID).Scan(&finalized); err != nil || finalized < 2 {
		t.Fatalf("the outbox carries %d finalized events for the two documents (%v)", finalized, err)
	}
	// The order carries the deposit fact.
	var meta string
	if err := pool.QueryRow(ctx, `SELECT metadata::text FROM orders WHERE id = $1`, out.OrderID).Scan(&meta); err != nil ||
		!containsAll(meta, `"supplier_deposit_cents": 4000`) {
		t.Fatalf("the order's metadata: %s (%v)", meta, err)
	}
	// And the Pay balance door reads the same money: the whole balance is the
	// two open documents, one number.
	pre, found, err := svc.store.ReadOwingOrder(ctx, out.OrderID)
	if err != nil || !found {
		t.Fatalf("the owing read: (%v, %v)", found, err)
	}
	if want := out.TotalCents; pre.balanceCents() != want {
		t.Fatalf("the owing read answers %d, want the whole %d across the two documents", pre.balanceCents(), want)
	}
}

func containsAll(haystack string, needles ...string) bool {
	for _, n := range needles {
		if !stringsContains(haystack, n) {
			return false
		}
	}
	return true
}

func stringsContains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
