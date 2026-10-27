//go:build integration

package billing

// FW1-12c: the credit application key's org becomes a real reference (S6's PR 630 round 10 P3).
//
// 0447 carried the key's org as TEXT with no foreign key, so the empty string could stand for "no
// org" and org-less callers shared one key scope. Production never passes an empty org: the staff
// gate resolves an org uuid or refuses, and the handler is the door's only caller. 0448 therefore
// makes credit_application_keys.org_id UUID NOT NULL REFERENCES org (id), the scoped siblings' own
// shape (0214, 0388), and the door refuses an empty org in words before any write. A non uuid org
// is NOT refused in words: the door passes it through and the column refuses it, so a programming
// error is loud at the database instead of silently sharing a scope.
//
// The legs, on the real migrated and seeded database:
//   - an empty org is refused in words, and nothing is written: no key row, no money moved;
//   - a non uuid org (a slug where the id belongs) fails at the column, and nothing is written.

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// seedOrgLessWorld plants one customer, one open invoice of 10000 and one account credit of -5000,
// all with NO org: the org-less shape the door used to accept. seedCustomer restores the rows, and
// the leg's own key rows are removed at test end.
func seedOrgLessWorld(t *testing.T, pool *pgxpool.Pool, n, tag string) (custID, invID, creditID string) {
	t.Helper()
	ctx := context.Background()
	custID = seedCustomer(t, pool, n+"-"+tag+"-CUST")
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', CURRENT_DATE, CURRENT_DATE, 10000, 0, 10000, 10000, now(), 'INVOICE')
		RETURNING id::text`, n+"-"+tag+"-INV", custID, trentonBranch).Scan(&invID); err != nil {
		t.Fatalf("seed %s's invoice: %v", tag, err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO customer_transactions
		  (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
		VALUES ($1::uuid, 'ADJUSTMENT', -5000, 0, 'deposit_cancel', gen_random_uuid(), $2::uuid, 'the credit')
		RETURNING id::text`, custID, trentonBranch).Scan(&creditID); err != nil {
		t.Fatalf("seed %s's credit: %v", tag, err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM credit_application_keys WHERE idempotency_key = $1`, n+"-"+tag+"-key-1")
	})
	return custID, invID, creditID
}

// keyRowsFor answers how many key rows the named key string carries.
func keyRowsFor(t *testing.T, pool *pgxpool.Pool, key string) int {
	t.Helper()
	ctx := context.Background()
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM credit_application_keys WHERE idempotency_key = $1`, key).Scan(&rows); err != nil {
		t.Fatalf("count the key's rows: %v", err)
	}
	return rows
}

func TestIntegration_FW1_12c_AnEmptyOrgIsRefusedInWordsWithNothingWritten(t *testing.T) {
	pool := intPool(t)
	applySvc := NewCreditApplyService(NewRepository(pool))
	ctx := context.Background()
	n := ns(t)

	custID, invID, creditID := seedOrgLessWorld(t, pool, n, "empty")
	key := n + "-empty-key-1"

	res, err := applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID: custID, CreditKind: CreditKindAccount, CreditID: creditID, InvoiceID: invID,
		AmountCents: 5000, Reason: "an org less caller's application", IdempotencyKey: key, OrgID: "",
	}, true)
	if err == nil {
		t.Fatalf("the empty org applied (application %s): the door must refuse it in words before any write", res.ApplicationID)
	}
	if !strings.Contains(err.Error(), "needs the caller's org") {
		t.Fatalf("the empty org was refused with %q, want the door's own words naming the caller's org", err)
	}

	// Nothing was written: the key has no row and the invoice's balance stands.
	if rows := keyRowsFor(t, pool, key); rows != 0 {
		t.Fatalf("the refused empty org left %d key row(s), want 0", rows)
	}
	var balance int64
	if err := pool.QueryRow(ctx, `SELECT balance_cents FROM invoices WHERE id = $1::uuid`, invID).Scan(&balance); err != nil {
		t.Fatalf("read the invoice: %v", err)
	}
	if balance != 10000 {
		t.Fatalf("the invoice stands at %d, want 10000: a refused org writes nothing", balance)
	}
}

func TestIntegration_FW1_12c_ANonUuidOrgFailsAtTheColumn(t *testing.T) {
	pool := intPool(t)
	applySvc := NewCreditApplyService(NewRepository(pool))
	ctx := context.Background()
	n := ns(t)

	custID, invID, creditID := seedOrgLessWorld(t, pool, n, "slug")
	key := n + "-slug-key-1"

	// A caller that carries the org's SLUG where the id belongs: the door does not judge the org's
	// spelling, it passes the org and the key table's uuid column refuses it.
	res, err := applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID: custID, CreditKind: CreditKindAccount, CreditID: creditID, InvoiceID: invID,
		AmountCents: 5000, Reason: "a slug where the id belongs", IdempotencyKey: key, OrgID: "north-yards",
	}, true)
	if err == nil {
		t.Fatalf("a non uuid org applied (application %s): the key table's org column must refuse it", res.ApplicationID)
	}
	if !strings.Contains(err.Error(), "uuid") {
		t.Fatalf("a non uuid org was refused with %q, want the column's uuid refusal", err)
	}

	// Nothing was written: the reservation's own insert failed, so the transaction wrote nothing.
	if rows := keyRowsFor(t, pool, key); rows != 0 {
		t.Fatalf("the column refusal left %d key row(s), want 0", rows)
	}
	var balance int64
	if err := pool.QueryRow(ctx, `SELECT balance_cents FROM invoices WHERE id = $1::uuid`, invID).Scan(&balance); err != nil {
		t.Fatalf("read the invoice: %v", err)
	}
	if balance != 10000 {
		t.Fatalf("the invoice stands at %d, want 10000: a column refusal writes nothing", balance)
	}
}
