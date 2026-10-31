//go:build integration

// discount_integration_test.go, TM-2: the journal the early payment discount posts.
//
// The discount a cash application takes is its own document: DR the discounts allowed account the
// posting rules name, CR 1100, dated by the application's own instant, idempotent per payment and
// invoice so a redelivered outbox row posts once. These legs consume the event the payments module
// emits beside the application and witness the journal the books keep.
//
// Run: go test -tags integration ./internal/gl/ -run Discount
package gl

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// discountLinesOf reads one journal's lines keyed by account code, each as (debit, credit).
func discountLinesOf(t *testing.T, pool *pgxpool.Pool, journalID string) (map[string][2]int64, error) {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT a.code, l.debit_cents, l.credit_cents
		  FROM gl_journal_lines l JOIN gl_accounts a ON a.id = l.account_id
		 WHERE l.journal_id = $1::uuid`,
		journalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][2]int64{}
	for rows.Next() {
		var code string
		var dr, cr *int64
		if err := rows.Scan(&code, &dr, &cr); err != nil {
			return nil, err
		}
		var d, c int64
		if dr != nil {
			d = *dr
		}
		if cr != nil {
			c = *cr
		}
		out[code] = [2]int64{d, c}
	}
	return out, rows.Err()
}

// TestIntegration_PaymentDiscountTaken_PostsDiscountAllowedAgainstAR is the posting itself: a
// consumed payment.discount.taken event posts one balanced journal, DR the discounts allowed rule's
// account and CR the AR rule's account, and a redelivered event posts nothing more.
func TestIntegration_PaymentDiscountTaken_PostsDiscountAllowedAgainstAR(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager}, time.Now()).
		WithPaymentReader(seedPaymentReader{pool: pool})
	paymentID, branchID := fw1bPlantPayment(t, pool, time.Now())
	at := time.Now().Add(-time.Minute)

	payload := map[string]any{
		"payment_id": paymentID, "customer_id": "", "invoice_id": "11111111-1111-4111-8111-111111111111",
		"invoice_no": "TM2-INV-1", "branch_id": branchID, "discount_cents": 2002, "occurred_at": at,
	}
	if err := svc.consume(ctx, ev("payment.discount.taken", payload)); err != nil {
		t.Fatalf("the discount consume: %v", err)
	}
	key := "payment.discount.taken:" + paymentID + ":11111111-1111-4111-8111-111111111111"
	var journalID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM gl_journals WHERE idempotency_key = $1`, key).
		Scan(&journalID); err != nil {
		t.Fatalf("the discount journal exists on its own key: %v", err)
	}
	lines, err := discountLinesOf(t, pool, journalID)
	if err != nil {
		t.Fatalf("read the journal lines: %v", err)
	}
	if len(lines) != 2 || lines["5450"] != [2]int64{2002, 0} || lines["1100"] != [2]int64{0, 2002} {
		t.Fatalf("the journal is DR 5450 2002 / CR 1100 2002, got %v", lines)
	}
	// The redelivery: the outbox hands the same row twice, the journal exists once.
	if err := svc.consume(ctx, ev("payment.discount.taken", payload)); err != nil {
		t.Fatalf("the redelivered discount consume: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM gl_journals WHERE idempotency_key = $1`, key).Scan(&n); err != nil {
		t.Fatalf("count the journals: %v", err)
	}
	if n != 1 {
		t.Fatalf("a redelivered discount event posts nothing more, found %d journals", n)
	}
}
