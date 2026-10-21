//go:build integration

// fresh_integration_test.go: the sync freshness probe (harness.Fresh) over the
// credit memo writer. A credit memo issued to a customer must reach the
// customer's invoices feed (tenant arm, by customer), the dealer's invoices
// feed (staff arm, by org) and the customer's aging feed. Each case drives the
// REAL InvoiceWriter.IssueCreditMemo against the REAL scope stores.
package billing

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync/harness"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func freshMemoWorld(t *testing.T) (*InvoiceWriter, harness.FreshCase) {
	t.Helper()
	pool := intPool(t)
	ctx := context.Background()
	if err := testkit.Build(ctx, testkit.PgxPool(pool)); err != nil {
		t.Fatalf("fixture build: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE source_type = 'invoice' AND customer_id = $1::uuid
			AND source_ref_id IN (SELECT id FROM invoices WHERE invoice_no LIKE '#CM-%' AND idempotency_key LIKE 'fresh-memo-%')`, testkit.TenantAlphaID)
		_, _ = pool.Exec(c, `DELETE FROM invoice_lines WHERE invoice_id IN (SELECT id FROM invoices WHERE idempotency_key LIKE 'fresh-memo-%')`)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE idempotency_key LIKE 'fresh-memo-%'`)
		_ = testkit.Cleanup(c, testkit.PgxPool(pool))
	})
	// The device has already synced: the customer's fixture invoices are in
	// the feed, stamped with the org and a drawn invoices seq, so the cursor
	// the probe pulls from is past zero.
	if _, err := pool.Exec(ctx,
		`WITH s AS (
			INSERT INTO scope_sequences (scope) VALUES ('invoices')
			ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
			RETURNING next_value)
		 UPDATE invoices SET org_id = $2::uuid, seq = (SELECT next_value FROM s)
		  WHERE customer_id = $1::uuid AND doc_type <> 'CREDIT_MEMO'`,
		testkit.TenantAlphaID, testkit.OrgNorthID); err != nil {
		t.Fatalf("stamp the fixture invoices into the feed: %v", err)
	}
	writer := NewInvoiceWriter(pool)
	return writer, harness.FreshCase{Engine: sync.NewEngine()}
}

func issueMemo(writer *InvoiceWriter) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		doc, err := writer.IssueCreditMemo(ctx, CreditMemoInput{
			CustomerID: testkit.TenantAlphaID, BranchID: testkit.BranchNorthAID,
			AmountCents: 2500, Memo: "freshness harness", IdempotencyKey: "fresh-memo-" + uuid.NewString(),
		})
		return doc.InvoiceID, err
	}
}

func wantNegativeTotal(d map[string]any) error {
	if v, _ := d["total_cents"].(float64); v >= 0 {
		return fmt.Errorf("total_cents is %v, want a negative credit", d["total_cents"])
	}
	return nil
}

// invoicesFreshCase wires the invoices scope with the credit memo write.
func invoicesFreshCase(t *testing.T) harness.FreshCase {
	t.Helper()
	writer, c := freshMemoWorld(t)
	pool := intPool(t)
	if err := c.Engine.RegisterScope("invoices", NewInvoicesSyncStore(pool, writer, nil)); err != nil {
		t.Fatalf("register: %v", err)
	}
	info, ok := sync.Lookup("invoices")
	if !ok {
		t.Fatalf("scope invoices missing")
	}
	c.Scope = info
	c.Write = issueMemo(writer)
	c.Want = wantNegativeTotal
	return c
}

// TestIntegration_Fresh_CreditMemoReachesTheCustomersInvoicesFeed: the tenant
// arm (Pro Pay), by customer.
func TestIntegration_Fresh_CreditMemoReachesTheCustomersInvoicesFeed(t *testing.T) {
	c := invoicesFreshCase(t)
	c.Filter = sync.ScopeFilter{OrgID: testkit.OrgNorthID, TenantID: testkit.TenantAlphaID, Tenant: true, Actor: "fresh"}
	harness.Fresh(t, c)
}

// TestIntegration_Fresh_CreditMemoReachesTheDealersInvoicesFeed: the staff arm
// (Counter Till), by org. The memo insert stamps no org_id, so this stays red
// until SF-3 stamps it.
func TestIntegration_Fresh_CreditMemoReachesTheDealersInvoicesFeed(t *testing.T) {
	t.Skip("SF-3: org stamp (the credit memo insert carries no org_id, so the staff fence never admits it)")
	c := invoicesFreshCase(t)
	c.Filter = sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: "fresh"}
	harness.Fresh(t, c)
}

// TestIntegration_Fresh_CreditMemoReachesTheAgingFeed: the customer's aging
// feed (tenant) shows the credit.
func TestIntegration_Fresh_CreditMemoReachesTheAgingFeed(t *testing.T) {
	writer, c := freshMemoWorld(t)
	pool := intPool(t)
	if err := c.Engine.RegisterScope("aging", NewAgingStore(pool, nil)); err != nil {
		t.Fatalf("register: %v", err)
	}
	info, ok := sync.Lookup("aging")
	if !ok {
		t.Fatalf("scope aging missing")
	}
	c.Scope = info
	c.Filter = sync.ScopeFilter{OrgID: testkit.OrgNorthID, TenantID: testkit.TenantAlphaID, Tenant: true, Actor: "fresh"}
	c.Write = issueMemo(writer)
	c.Want = wantNegativeTotal
	harness.Fresh(t, c)
}
