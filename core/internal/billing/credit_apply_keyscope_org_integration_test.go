//go:build integration

package billing

import (
	"context"
	"testing"
)

// ROUND 10, M3-P3-6: the key table's scope is the caller's org, not the bare key string.
//
// 0447 made the idempotency key the table's whole PRIMARY KEY, so one key string is one row for
// the whole route, every org's: a key org A spent on its own invoice answers the spent key 409 to
// org B, whose FIRST use under the same string it is. The route is multi tenant (the handler passes
// the caller's org in, and the door refuses another org's invoice with it), and AGENTS.md says a
// scoped table is never keyed or queried without its scope. The desk mints a fresh key per
// attempt, so the reach needs a caller that picks its own key strings; the route's contract is
// still that one org's key cannot be spent by another.
//
// The leg: two orgs, one key string. Org A applies under the key; org B's FIRST use of the same
// string, against org B's own invoice and credit, must APPLY, not answer the 409. Org B's retry of
// its own request replays org B's stored result. The table carries one row per org under the one
// string, each naming its own invoice.

func TestIntegration_FW1_12b_ASecondOrgsFirstUseOfAKeyStringAppliesNotConflicts(t *testing.T) {
	pool := intPool(t)
	applySvc := NewCreditApplyService(NewRepository(pool))
	ctx := context.Background()
	n := ns(t)

	orgA := "11111111-1111-4111-8111-111111111111"
	orgB := "22222222-2222-4222-8222-222222222222"
	seedOrgWorld := func(org, tag string) (custID, invID, creditID string) {
		custID = seedCustomer(t, pool, n+"-"+tag+"-CUST")
		if err := pool.QueryRow(ctx, `
			INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
			                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type, org_id)
			VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', CURRENT_DATE, CURRENT_DATE, 10000, 0, 10000, 10000, now(), 'INVOICE', $4::uuid)
			RETURNING id::text`, n+"-"+tag+"-INV", custID, trentonBranch, org).Scan(&invID); err != nil {
			t.Fatalf("seed %s's invoice: %v", tag, err)
		}
		if err := pool.QueryRow(ctx, `
			INSERT INTO customer_transactions
			  (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo, org_id)
			VALUES ($1::uuid, 'ADJUSTMENT', -5000, 0, 'deposit_cancel', gen_random_uuid(), $2::uuid, 'the credit', $3::uuid)
			RETURNING id::text`, custID, trentonBranch, org).Scan(&creditID); err != nil {
			t.Fatalf("seed %s's credit: %v", tag, err)
		}
		return custID, invID, creditID
	}
	custA, invA, creditA := seedOrgWorld(orgA, "a")
	custB, invB, creditB := seedOrgWorld(orgB, "b")

	key := n + "-key-1"

	first, err := applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID: custA, CreditKind: CreditKindAccount, CreditID: creditA, InvoiceID: invA,
		AmountCents: 5000, Reason: "org a's only use of the key", IdempotencyKey: key, OrgID: orgA,
	}, true)
	if err != nil {
		t.Fatalf("org a's first apply under the key: %v", err)
	}
	if first.Replayed {
		t.Fatalf("org a's first apply answered Replayed=true, want the first result")
	}

	// Org B's FIRST use of the same key string, on org B's own invoice and credit: an
	// application, not the spent key 409 org A's row answers today.
	second, err := applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID: custB, CreditKind: CreditKindAccount, CreditID: creditB, InvoiceID: invB,
		AmountCents: 5000, Reason: "org b's first use of the string", IdempotencyKey: key, OrgID: orgB,
	}, true)
	if err != nil {
		t.Fatalf("org b's first use of the key string answered %v, want org b's own application", err)
	}
	if second.Replayed {
		t.Fatalf("org b's first use answered Replayed=true, want a fresh application")
	}
	if second.ApplicationID == first.ApplicationID {
		t.Fatalf("org b's use answered org a's application %s, want org b's own", first.ApplicationID)
	}

	// Org b's retry of its own request replays org b's stored result.
	retry, err := applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID: custB, CreditKind: CreditKindAccount, CreditID: creditB, InvoiceID: invB,
		AmountCents: 5000, Reason: "org b's first use of the string", IdempotencyKey: key, OrgID: orgB,
	}, true)
	if err != nil {
		t.Fatalf("org b's retry answered %v, want its own stored result", err)
	}
	if !retry.Replayed || retry.ApplicationID != second.ApplicationID {
		t.Fatalf("org b's retry answered Replayed=%v application %s, want replayed %s", retry.Replayed, retry.ApplicationID, second.ApplicationID)
	}

	// Both orgs' invoices moved: org b's first use really applied.
	var invABalance, invBBalance int64
	if err := pool.QueryRow(ctx, `SELECT balance_cents FROM invoices WHERE id = $1::uuid`, invA).Scan(&invABalance); err != nil {
		t.Fatalf("read org a's invoice: %v", err)
	}
	if invABalance != 5000 {
		t.Fatalf("org a's invoice stands at %d, want 5000", invABalance)
	}
	if err := pool.QueryRow(ctx, `SELECT balance_cents FROM invoices WHERE id = $1::uuid`, invB).Scan(&invBBalance); err != nil {
		t.Fatalf("read org b's invoice: %v", err)
	}
	if invBBalance != 5000 {
		t.Fatalf("org b's invoice stands at %d, want 5000: its first use under the string must apply", invBBalance)
	}

	// The table carries one row per org under the one string, each naming its own invoice.
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM credit_application_keys WHERE idempotency_key = $1`, key).Scan(&rows); err != nil {
		t.Fatalf("count the key's rows: %v", err)
	}
	if rows != 2 {
		t.Fatalf("the key string carries %d rows, want 2, one per org", rows)
	}
	var rowA, rowB string
	if err := pool.QueryRow(ctx, `SELECT invoice_id::text FROM credit_application_keys WHERE idempotency_key = $1 AND org_id = $2`, key, orgA).Scan(&rowA); err != nil {
		t.Fatalf("read org a's key row: %v", err)
	}
	if rowA != invA {
		t.Fatalf("org a's key row names invoice %s, want %s", rowA, invA)
	}
	if err := pool.QueryRow(ctx, `SELECT invoice_id::text FROM credit_application_keys WHERE idempotency_key = $1 AND org_id = $2`, key, orgB).Scan(&rowB); err != nil {
		t.Fatalf("read org b's key row: %v", err)
	}
	if rowB != invB {
		t.Fatalf("org b's key row names invoice %s, want %s", rowB, invB)
	}
}
