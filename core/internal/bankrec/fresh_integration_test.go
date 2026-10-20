//go:build integration

// fresh_integration_test.go: the sync freshness probe (harness.Fresh) over the
// bank reconciliation writers that move a payment's status. A payer whose
// device has already synced the payments feed must see the payment deposited
// when a statement line is matched to it, and received again when the match is
// undone. Each test drives the REAL repository write path against the REAL
// payments scope store.
package bankrec

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync/harness"
)

// freshPayment seeds a customer and a RECEIVED payment already in the payments
// feed (a row a device has synced), and returns the case wired to the payer.
func freshPayment(t *testing.T, pool *pgxpool.Pool) (harness.FreshCase, string) {
	t.Helper()
	ctx := context.Background()
	branch := aBranch(t, pool)
	suffix := uuid.NewString()[:8]
	var customerID, paymentID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		 VALUES ('BRFRESH-'||$1, 'Bankrec Fresh Co '||$1, 'CONTRACTOR', $2::uuid, 1000000) RETURNING id::text`,
		suffix, branch).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`WITH s AS (
			INSERT INTO scope_sequences (scope) VALUES ('payments')
			ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
			RETURNING next_value)
		 INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, branch_id, gateway_ref, seq)
		 VALUES ('PMT-BRF-'||$1, $2::uuid, 'CARD', 41100, 'RECEIVED', $3::uuid, 'GTW-BRFRESH-'||$1, (SELECT next_value FROM s))
		 RETURNING id::text`, suffix, customerID, branch).Scan(&paymentID); err != nil {
		t.Fatalf("seed payment: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM bank_matches WHERE target_ref_id = $1::uuid`, paymentID)
		_, _ = pool.Exec(c, `DELETE FROM payments WHERE id = $1::uuid`, paymentID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, customerID)
	})
	info, ok := sync.Lookup("payments")
	if !ok {
		t.Fatalf("scope payments missing")
	}
	engine := sync.NewEngine()
	if err := engine.RegisterScope("payments", payments.NewPaymentsSyncStore(pool, nil)); err != nil {
		t.Fatalf("register payments: %v", err)
	}
	return harness.FreshCase{
		Engine: engine,
		Scope:  info,
		Filter: sync.ScopeFilter{OrgID: "3ef10000-0000-4000-8000-000000000001", TenantID: customerID, Tenant: true, Actor: "fresh"},
	}, paymentID
}

func freshWantStatus(want string) func(map[string]any) error {
	return func(d map[string]any) error {
		if d["status"] != want {
			return fmt.Errorf("status is %v, want %s", d["status"], want)
		}
		return nil
	}
}

// TestIntegration_Fresh_PaymentMatchedReachesThePayersFeed: PersistMatch claims
// the payment RECEIVED to DEPOSITED (bankrec/repository.go PersistMatch).
func TestIntegration_Fresh_PaymentMatchedReachesThePayersFeed(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	c, pid := freshPayment(t, pool)
	lineID := insertLine(t, pool, acct, time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC), "FRESH MATCH", "GTW-FRESH-MATCH", 41100)

	c.Write = func(ctx context.Context) (string, error) {
		_, err := NewRepository(pool).PersistMatch(ctx, Match{
			StatementLineID: lineID, TargetType: TargetPayment, TargetRefID: pid, Method: MethodManual, AmountCents: 41100,
		}, []string{pid})
		return pid, err
	}
	c.Want = freshWantStatus("DEPOSITED")
	harness.Fresh(t, c)
}

// TestIntegration_Fresh_PaymentUnmatchedReachesThePayersFeed: Unmatch releases
// the payment DEPOSITED to RECEIVED (bankrec/repository.go Unmatch).
func TestIntegration_Fresh_PaymentUnmatchedReachesThePayersFeed(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	c, pid := freshPayment(t, pool)
	ctx := context.Background()
	lineID := insertLine(t, pool, acct, time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC), "FRESH UNMATCH", "GTW-FRESH-UNMATCH", 41100)
	repo := NewRepository(pool)
	matchID, err := repo.PersistMatch(ctx, Match{
		StatementLineID: lineID, TargetType: TargetPayment, TargetRefID: pid, Method: MethodManual, AmountCents: 41100,
	}, []string{pid})
	if err != nil {
		t.Fatalf("seed match: %v", err)
	}
	// The matched state is what the device has synced: stamp the deposit into the feed.
	if _, err := pool.Exec(ctx,
		`WITH s AS (
			INSERT INTO scope_sequences (scope) VALUES ('payments')
			ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
			RETURNING next_value)
		 UPDATE payments SET seq = (SELECT next_value FROM s) WHERE id = $1::uuid`, pid); err != nil {
		t.Fatalf("stamp the matched payment into the feed: %v", err)
	}

	c.Write = func(ctx context.Context) (string, error) {
		_, _, _, err := repo.Unmatch(ctx, matchID, "")
		return pid, err
	}
	c.Want = freshWantStatus("RECEIVED")
	harness.Fresh(t, c)
}
