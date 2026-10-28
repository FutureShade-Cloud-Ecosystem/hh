//go:build integration

package billing

// ROUND 9, M3-P3-3: the desk's ACCOUNT offer must name the group's first
// NEGATIVE row, not the group's first row by time.
//
// OpenCreditsFor's ACCOUNT arm offered (ARRAY_AGG(id ORDER BY occurred_at, id))[1]:
// the first row of the group by time, whatever kind it carries. The door's own
// anchor rule (lockCreditGroupAnchor) names the group's FIRST NEGATIVE row, and
// the row a caller may name as the credit must carry a kind in
// creditRowTxnTypes, where a write off is not one. So a group whose first row
// is a write off was offered with the write off's id, and the door refused the
// very id the desk had just handed over (billing: no such credit). The offer
// must name the anchor: the first NEGATIVE row is a row the door can always
// take. No production writer puts a write off inside one of the door's two
// credit families today, so the reach is dormant; the shape is one writer
// away, which is the same shape M3-P2-1's reach was in round 6.

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestIntegration_FW1_12b_TheDeskOffersTheGroupsFirstNegativeRow pins the
// desk's offer to the door's own anchor rule: a group whose first row by time
// is a write off, holding a credit row after it, is offered with the CREDIT
// row's id, and the door takes that id.
func TestIntegration_FW1_12b_TheDeskOffersTheGroupsFirstNegativeRow(t *testing.T) {
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

	// One accepted family group ('deposit_cancel', the producer's own spelling)
	// whose FIRST row by time is a write off and whose SECOND row is the credit.
	// The group sums to -15000, so the aging reads it open and the desk offers it.
	groupRef := uuid.NewString()
	seedRow := func(tag, txn string, cents int64, balanceAfter int64, minutesAgo int) string {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO customer_transactions
			  (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo, occurred_at)
			VALUES ($1::uuid, $2, $3, $4, 'deposit_cancel', $5::uuid, $6::uuid, $7, clock_timestamp() - ($8 * interval '1 minute'))
			RETURNING id::text`,
			custID, txn, cents, balanceAfter, groupRef, trentonBranch, n+" "+tag, minutesAgo).Scan(&id); err != nil {
			t.Fatalf("seed the %s row: %v", tag, err)
		}
		return id
	}
	writeOffID := seedRow("write-off", "WRITE_OFF", 25000, 25000, 120)
	creditID := seedRow("credit", "ADJUSTMENT", -40000, -15000, 60)

	offers, err := NewRepository(pool).OpenCreditsFor(ctx, custID, trentonBranch)
	if err != nil {
		t.Fatalf("the desk's open credits read: %v", err)
	}
	offeredID := ""
	for _, c := range offers {
		if c.CreditKind == CreditKindAccount {
			offeredID = c.CreditID
		}
	}
	if offeredID == "" {
		t.Fatal("the desk offered no account credit for the group, want the group offered at all")
	}
	if offeredID != creditID {
		t.Fatalf("the desk offered the group's first row by time (%s), want the group's first NEGATIVE row %s, the door's own anchor", offeredID, creditID)
	}

	// The offered id is one the door can take: the apply on the anchor the desk
	// just named goes through and moves the invoice.
	if _, err := applySvc.Apply(ctx, ApplyCreditInput{
		CustomerID: custID, CreditKind: CreditKindAccount, CreditID: offeredID, InvoiceID: invID,
		OrgID:       applyOrg,
		AmountCents: 10000, Reason: "round 9: the anchor the desk offered", IdempotencyKey: n + "-key-1",
	}, true); err != nil {
		t.Fatalf("the door refused the id the desk offered (%s, the write off is %s): %v", offeredID, writeOffID, err)
	}
}
