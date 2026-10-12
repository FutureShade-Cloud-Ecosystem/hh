//go:build integration

// payment_lookup_like_integration_test.go, the staff payment lookup's two pattern arms against real
// Postgres.
//
// Both arms bind the caller's text and a bound pattern is still a pattern: on main a '%' in a
// customer reference matched any run and an '_' any one character, in a gateway reference the same,
// and a '\' was consumed as the pattern's own escape character (Postgres's default). Each leg
// plants a literal row and a decoy whose text differs only in the character a wildcard would have
// stood in for, both at one amount no other row carries so the exact-amount arm isolates the pair,
// and asks for the term. The payment_no arm stays an exact CITEXT comparison on the RAW reference
// and is asserted untouched: only the patterns change, never the amount match, the ordering or the
// reach.
//
//	Run: DATABASE_URL=... go test -tags integration ./internal/payments/ -run TestIntegration_FindPayments_PatternsAreLiteral -count=1
package payments

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// likePayFixture plants a customer with the given name and a payment for it at the given amount,
// with the given gateway ref, returning the payment and customer ids. Everything is removed at
// teardown.
func likePayFixture(t *testing.T, pool *pgxpool.Pool, branchID, name, gatewayRef string, amount int64) (paymentID, customerID string) {
	t.Helper()
	ctx := context.Background()
	nonce := time.Now().UnixNano()
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		 VALUES ($1, $2, 'CONTRACTOR', $3::uuid, 0) RETURNING id::text`,
		fmt.Sprintf("PLK%d", nonce), name, branchID).Scan(&customerID); err != nil {
		t.Fatalf("seed customer %q: %v", name, err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, branch_id, gateway_ref)
		 VALUES ($1, $2::uuid, 'CHEQUE', $3, 'RECEIVED', $4::uuid, $5) RETURNING id::text`,
		fmt.Sprintf("PLK-PAY-%d-%d", nonce, amount), customerID, amount, branchID, gatewayRef).Scan(&paymentID); err != nil {
		t.Fatalf("seed payment for %q: %v", name, err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM payments WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, customerID)
	})
	return paymentID, customerID
}

// paymentCustomerTermIsLiteral holds the customer arm to the literal-only contract.
func paymentCustomerTermIsLiteral(t *testing.T, svc *Service, ctx context.Context, term string, amount int64, litID, decID string) {
	t.Helper()
	got, err := svc.FindPayments(ctx, BranchReach{Global: true}, LookupQuery{CustomerRef: term, AmountCents: amount, Limit: 50})
	if err != nil {
		t.Fatalf("FindPayments %q: %v", term, err)
	}
	var haveLit, haveDec bool
	for _, m := range got {
		if m.ID == litID {
			haveLit = true
		}
		if m.ID == decID {
			haveDec = true
		}
		if !strings.Contains(m.CustomerName, term) {
			t.Fatalf("customer search %q returned a row whose name contains none of it: %q", term, m.CustomerName)
		}
	}
	if !haveLit {
		t.Fatalf("customer search %q did not find the payment whose customer name contains it literally", term)
	}
	if haveDec {
		t.Fatalf("customer search %q matched a row the term fits only as a wildcard, not as text", term)
	}
}

// paymentReferenceTermIsLiteral holds the gateway-ref arm to the same contract.
func paymentReferenceTermIsLiteral(t *testing.T, svc *Service, ctx context.Context, term string, amount int64, litID, decID string) {
	t.Helper()
	got, err := svc.FindPayments(ctx, BranchReach{Global: true}, LookupQuery{Reference: term, AmountCents: amount, Limit: 50})
	if err != nil {
		t.Fatalf("FindPayments %q: %v", term, err)
	}
	var haveLit, haveDec bool
	for _, m := range got {
		if m.ID == litID {
			haveLit = true
		}
		if m.ID == decID {
			haveDec = true
		}
		if !strings.Contains(m.GatewayRef, term) {
			t.Fatalf("reference search %q returned a row whose gateway ref contains none of it: %q", term, m.GatewayRef)
		}
	}
	if !haveLit {
		t.Fatalf("reference search %q did not find the payment whose gateway ref contains it literally", term)
	}
	if haveDec {
		t.Fatalf("reference search %q matched a row the term fits only as a wildcard, not as text", term)
	}
}

// TestIntegration_FindPayments_PatternsAreLiteral, the three metacharacter classes a bound pattern
// can misread, across both pattern arms of the lookup.
func TestIntegration_FindPayments_PatternsAreLiteral(t *testing.T) {
	pool := intPool(t)
	svc := NewService(NewRepository(pool), NewStubGateway())
	ctx := context.Background()
	var branchID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type='BRANCH' LIMIT 1`).Scan(&branchID); err != nil {
		t.Skipf("no seeded BRANCH: %v", err)
	}

	t.Run("customer arm, a percent sign is text not any-run", func(t *testing.T) {
		lit, _ := likePayFixture(t, pool, branchID, "Half 50% Co", "gw-plain-1", 1234571)
		dec, _ := likePayFixture(t, pool, branchID, "Half 501 Co", "gw-plain-2", 1234571)
		paymentCustomerTermIsLiteral(t, svc, ctx, "50%", 1234571, lit, dec)
	})
	t.Run("customer arm, an underscore is text not any-one", func(t *testing.T) {
		lit, _ := likePayFixture(t, pool, branchID, "Grade A_1 Co", "gw-plain-3", 1234573)
		dec, _ := likePayFixture(t, pool, branchID, "Grade AB1 Co", "gw-plain-4", 1234573)
		paymentCustomerTermIsLiteral(t, svc, ctx, "A_1", 1234573, lit, dec)
	})
	t.Run("customer arm, the escape character is text too", func(t *testing.T) {
		lit, _ := likePayFixture(t, pool, branchID, `Slab\2 Co`, "gw-plain-5", 1234577)
		dec, _ := likePayFixture(t, pool, branchID, "Slab2 Co", "gw-plain-6", 1234577)
		paymentCustomerTermIsLiteral(t, svc, ctx, `Slab\2`, 1234577, lit, dec)
	})
	t.Run("reference arm, a percent sign is text not any-run", func(t *testing.T) {
		lit, _ := likePayFixture(t, pool, branchID, "Ref Arm Co", "GWR 50% X", 1234581)
		dec, _ := likePayFixture(t, pool, branchID, "Ref Arm Dec Co", "GWR 501 X", 1234581)
		paymentReferenceTermIsLiteral(t, svc, ctx, "GWR 50% X", 1234581, lit, dec)
	})
	t.Run("reference arm, an underscore is text not any-one", func(t *testing.T) {
		lit, _ := likePayFixture(t, pool, branchID, "Ref Arm Co", "GWR A_1 X", 1234583)
		dec, _ := likePayFixture(t, pool, branchID, "Ref Arm Dec Co", "GWR AB1 X", 1234583)
		paymentReferenceTermIsLiteral(t, svc, ctx, "GWR A_1 X", 1234583, lit, dec)
	})
	t.Run("reference arm, the escape character is text too", func(t *testing.T) {
		lit, _ := likePayFixture(t, pool, branchID, "Ref Arm Co", `GWR Slab\2 X`, 1234587)
		dec, _ := likePayFixture(t, pool, branchID, "Ref Arm Dec Co", "GWR Slab2 X", 1234587)
		paymentReferenceTermIsLiteral(t, svc, ctx, `GWR Slab\2 X`, 1234587, lit, dec)
	})
	t.Run("a full payment number still finds its payment exactly", func(t *testing.T) {
		// The payment_no arm is an exact CITEXT comparison on the RAW reference and must stay that
		// way: escaping may move the pattern arms, never the equality.
		var paymentNo string
		if err := pool.QueryRow(ctx,
			`INSERT INTO payments (payment_no, method, amount_cents, status, branch_id)
			 VALUES ($1, 'CHEQUE', 1234591, 'RECEIVED', $2::uuid) RETURNING payment_no::text`,
			fmt.Sprintf("PLK-EXACT-%d", time.Now().UnixNano()), branchID).Scan(&paymentNo); err != nil {
			t.Fatalf("seed exact payment: %v", err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE payment_no = $1`, paymentNo)
		})
		got, err := svc.FindPayments(ctx, BranchReach{Global: true}, LookupQuery{Reference: paymentNo, Limit: 10})
		if err != nil {
			t.Fatalf("FindPayments by payment number: %v", err)
		}
		if len(got) != 1 || got[0].PaymentNo != paymentNo {
			t.Fatalf("an exact payment number must find exactly its payment, got %+v", got)
		}
	})
}
