//go:build integration

package billing

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// ROUND 10, M3-P3-4: the desk's offered anchor excludes WRITE_OFF exactly as the door's named row
// rule does, and a group whose only negative row is a write off is not offered at all.
//
// The desk's ACCOUNT offer names the group's first NEGATIVE row (round 9, M3-P3-3), read with the
// AGING's kinds, which include WRITE_OFF. The row a caller may NAME as the credit must carry a
// kind in creditRowTxnTypes, which does not: a write off is never value the customer holds, so it
// can never be the credit a door applies (credit_apply.go's own rule). So a group whose first
// negative row is a write off was offered with an id the door refuses, the offer a promise the
// door does not keep; and a group whose ONLY negative row is a write off was offered at all, its
// anchor the write off's id. The desk must carry the door's kind rule in: the anchor is the
// group's first negative row OF A KIND THE DOOR WILL NAME, and a group holding none is not
// offered, never offered with a null id. No production writer writes a WRITE_OFF row into
// customer_transactions today, so the reach is dormant; the shape is one writer away.

func TestIntegration_FW1_12b_TheDeskNeverOffersAWriteOffRowAsTheCredit(t *testing.T) {
	pool := intPool(t)
	applySvc := NewCreditApplyService(NewRepository(pool))
	ctx := context.Background()
	n := ns(t)
	applyOrg := plantApplyOrg(t, pool, n)

	custID := seedCustomer(t, pool, n+"-CUST")

	var invID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', CURRENT_DATE, CURRENT_DATE, 10000, 0, 10000, 10000, now(), 'INVOICE')
		RETURNING id::text`, n+"-INV", custID, trentonBranch).Scan(&invID); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}

	seedRow := func(group, tag, txn string, cents int64, minutesAgo int) string {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO customer_transactions
			  (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo, occurred_at)
			VALUES ($1::uuid, $2, $3, 0, 'deposit_cancel', $4::uuid, $5::uuid, $6, clock_timestamp() - ($7 * interval '1 minute'))
			RETURNING id::text`,
			custID, txn, cents, group, trentonBranch, n+" "+tag, minutesAgo).Scan(&id); err != nil {
			t.Fatalf("seed the %s row: %v", tag, err)
		}
		return id
	}

	// Group one: its ONLY negative row is a write off. Not a credit the door can take, so not a
	// credit the desk may offer, whatever the group's sum reads.
	writeOffOnlyID := seedRow(uuid.NewString(), "only-write-off", "WRITE_OFF", -8000, 90)

	// Group two: a write off is its first negative row by time, and a CREDIT_MEMO row (a kind the
	// door names) sits after it. Offered with the CREDIT_MEMO row's id, never the write off's.
	groupTwo := uuid.NewString()
	writeOffID := seedRow(groupTwo, "first-write-off", "WRITE_OFF", -8000, 120)
	creditID := seedRow(groupTwo, "credit", "CREDIT_MEMO", -20000, 60)

	offers, err := NewRepository(pool).OpenCreditsFor(ctx, custID, trentonBranch)
	if err != nil {
		t.Fatalf("the desk's open credits read: %v", err)
	}
	for _, c := range offers {
		if c.CreditKind == CreditKindAccount && c.CreditID == writeOffOnlyID {
			t.Fatalf("the desk offered a group whose only negative row is a write off (%s), want the group not offered at all", writeOffOnlyID)
		}
	}
	offeredTwo := ""
	for _, c := range offers {
		if c.CreditKind == CreditKindAccount && (c.CreditID == writeOffID || c.CreditID == creditID) {
			offeredTwo = c.CreditID
		}
	}
	if offeredTwo == "" {
		t.Fatal("the desk offered none of group two's rows, want the group offered with the row the door will name")
	}
	if offeredTwo != creditID {
		t.Fatalf("the desk offered the write off row %s as the credit, want the row the door will name %s", offeredTwo, creditID)
	}
	if offeredTwo == "" {
		t.Fatal("the desk offered the group with a null id")
	}

	// The id the desk hands over is one the door takes: the apply goes through and moves the
	// invoice.
	if _, err := applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID: custID, CreditKind: CreditKindAccount, CreditID: offeredTwo, InvoiceID: invID,
		OrgID:       applyOrg,
		AmountCents: 10000, Reason: "round 10: the anchor the desk offered", IdempotencyKey: n + "-key-1",
	}, true); err != nil {
		t.Fatalf("the door refused the id the desk offered (%s, the write off is %s): %v", offeredTwo, writeOffID, err)
	}
}
