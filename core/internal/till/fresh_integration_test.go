//go:build integration

// fresh_integration_test.go: the sync freshness probe (harness.Fresh) over the
// till's payment status writers. A payer whose device has already synced the
// payments feed must see a bounced or deposited payment on the next pull from
// its own cursor. Each test drives the REAL write path against the REAL
// payments scope store.
package till

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync/harness"
)

// freshPaymentsCase wires the payments scope for the payer and stamps the
// seeded payment into the feed (a payment a device has already synced).
func freshPaymentsCase(t *testing.T, pool *pgxpool.Pool, paymentID, customerID string) harness.FreshCase {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`WITH s AS (
			INSERT INTO scope_sequences (scope) VALUES ('payments')
			ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
			RETURNING next_value)
		 UPDATE payments SET seq = (SELECT next_value FROM s) WHERE id = $1::uuid`, paymentID); err != nil {
		t.Fatalf("stamp the seeded payment into the feed: %v", err)
	}
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
	}
}

func freshWantStatus(want string) func(map[string]any) error {
	return func(d map[string]any) error {
		if d["status"] != want {
			return fmt.Errorf("status is %v, want %s", d["status"], want)
		}
		return nil
	}
}

// TestIntegration_Fresh_PaymentBouncedReachesThePayersFeed: MarkNSF flips the
// payment to BOUNCED (till/repository.go MarkNSF).
func TestIntegration_Fresh_PaymentBouncedReachesThePayersFeed(t *testing.T) {
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	svc := realSvc(pool)
	pid, _, custID, cleanup := seedNSFCase(t, pool, branchID, 50000)
	t.Cleanup(cleanup)

	c := freshPaymentsCase(t, pool, pid, custID)
	c.Write = func(context.Context) (string, error) {
		_, err := svc.MarkNSF(globalCtx(userID), NSFInput{PaymentID: pid, Reason: "cheque returned NSF"})
		return pid, err
	}
	c.Want = freshWantStatus("BOUNCED")
	harness.Fresh(t, c)
}

// TestIntegration_Fresh_PaymentDepositedReachesThePayersFeed: MarkDeposited
// (recon_seam.go) flips a RECEIVED payment to DEPOSITED.
func TestIntegration_Fresh_PaymentDepositedReachesThePayersFeed(t *testing.T) {
	pool := intPool(t)
	branchID, _ := seedBranchUser(t, pool)
	pid, _, custID, cleanup := seedNSFCase(t, pool, branchID, 50000)
	t.Cleanup(cleanup)

	c := freshPaymentsCase(t, pool, pid, custID)
	c.Write = func(ctx context.Context) (string, error) {
		return pid, NewRepository(pool).MarkDeposited(ctx, []string{pid}, "fresh-line")
	}
	c.Want = freshWantStatus("DEPOSITED")
	harness.Fresh(t, c)
}
