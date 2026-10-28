//go:build integration

package billing

// FW1-12c, K3's P3: the apply org helper must clean up after itself.
//
// plantApplyOrg plants one real org row for the apply door's key scope and says the row is
// removed at test end. It was not: the application's key row references that org (0448 gives
// the key table a real org foreign key), so the org delete was refused by the database and
// the row, and the key row under it, stayed in the gate database for every later run to read.
//
// The leg applies a credit under a planted org inside a SUBTEST, because a subtest's
// cleanups run when that subtest ends: after the subtest returns, the database is asked
// what the helper left behind. Before the fix the org count is 1 and the key count is 1;
// after it, both are 0.

import (
	"context"
	"testing"
)

// TestIntegration_FW1_12c_TheApplyOrgHelperLeavesNothingBehind plants an org, applies a credit
// under it, and then asks the database whether the helper's cleanup removed what it planted.
func TestIntegration_FW1_12c_TheApplyOrgHelperLeavesNothingBehind(t *testing.T) {
	pool := intPool(t)
	applySvc := NewCreditApplyService(NewRepository(pool))
	ctx := context.Background()
	n := ns(t)
	slug := "apply-org-" + n
	var orgID string

	t.Run("the apply writes a key row under the planted org", func(t *testing.T) {
		orgID = plantApplyOrg(t, pool, n)

		custID := seedCustomer(t, pool, n+"-CUST")
		var invID string
		if err := pool.QueryRow(ctx, `
			INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
			                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
			VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', CURRENT_DATE, CURRENT_DATE, 10000, 0, 10000, 10000, now(), 'INVOICE')
			RETURNING id::text`, n+"-INV", custID, trentonBranch).Scan(&invID); err != nil {
			t.Fatalf("seed the invoice: %v", err)
		}
		var creditID string
		if err := pool.QueryRow(ctx, `
			INSERT INTO customer_transactions
			  (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
			VALUES ($1::uuid, 'ADJUSTMENT', -10000, 0, 'deposit_cancel', gen_random_uuid(), $2::uuid, 'the credit')
			RETURNING id::text`, custID, trentonBranch).Scan(&creditID); err != nil {
			t.Fatalf("seed the credit: %v", err)
		}

		res, err := applySvc.Apply(ctx, ApplyCreditInput{
			CustomerID: custID, CreditKind: CreditKindAccount, CreditID: creditID, InvoiceID: invID,
			AmountCents: 5000, Reason: "the cleanup leg's application", IdempotencyKey: n + "-key-1", OrgID: orgID,
		}, true)
		if err != nil {
			t.Fatalf("apply the credit under the planted org: %v", err)
		}
		if res.Replayed {
			t.Fatalf("the first apply answered Replayed=true, want the first result")
		}
	})

	// The subtest's cleanups have run by now, so the database answers what the helper left.
	var orgs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM org WHERE slug = $1`, slug).Scan(&orgs); err != nil {
		t.Fatalf("count the planted org: %v", err)
	}
	if orgs != 0 {
		t.Fatalf("the apply org %q is still there after the helper's cleanup: %d row(s) left, want 0", slug, orgs)
	}
	var keys int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM credit_application_keys WHERE org_id = $1::uuid`, orgID).Scan(&keys); err != nil {
		t.Fatalf("count the org's credit application keys: %v", err)
	}
	if keys != 0 {
		t.Fatalf("the apply org %q still carries %d credit application key row(s) after the helper's cleanup, want 0", slug, keys)
	}
}
