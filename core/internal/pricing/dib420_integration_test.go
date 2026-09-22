//go:build integration

// dib420_integration_test.go, DIB-420 against the live schema. The unit arms prove the DECISION;
// these prove the two things a fake cannot:
//
//  1. The 0133 CHECK and the Go constant agree. AppendAudit's error is discarded at every call site
//     (by design, a failed audit write must not fail a price), so a reason the CHECK rejects is a
//     SILENTLY LOST audit row. The only way to know is to go and look for the row.
//  2. A refused request leaves NOTHING behind. A refusal that still wrote a bypass row would make
//     the month-end "how many below-floor prices went out" count wrong in the safe-looking direction.
package pricing

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// dib420Actor returns a real users.id, pricing_audit_log.user_id carries an FK, so an invented uuid
// would make the INSERT fail and hide the row we are trying to assert on.
func dib420Actor(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users LIMIT 1`).Scan(&id); err != nil {
		t.Skipf("no user seeded: %v", err)
	}
	return id
}

// TestIntegration_DIB420_HonouredOverrideWritesTheAuthorityReason is the named integration proof for
// the second acceptance arm: an override honoured on MANAGER_SCOPE authority lands a BYPASS row
// carrying MANAGER_SCOPE_OVERRIDE, which only migration 0133 makes legal.
func TestIntegration_DIB420_HonouredOverrideWritesTheAuthorityReason(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	f := newW4Fixture(t, pool)
	withAnchor(t, pool, f.productID, 10000, f.baseUOM)
	actor := dib420Actor(t, ctx, pool)

	svc := realSvc(pool).WithOverrideAuthority(&grantingAuthority{source: AuthorityManagerScope})
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM pricing_audit_log WHERE entity_type='margin_enforcement' AND user_id=$1 AND bypass_reason=$2`,
			actor, BypassManagerScope)
	})

	// cost 9900 against a 10000 anchor → 100 bps, far below any floor in the system.
	bd, err := svc.ResolvePrice(ctx, ResolveInput{
		ProductID: f.productID, CustomerType: "RETAIL", QtyEntered: 1, UOMEntered: f.baseUOM,
		BranchID: f.branchID, CostCents: 9900,
		OverrideRequested: true, UserID: actor,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if bd.Blocked {
		t.Fatalf("an override on manager-scope authority must be honoured, got %+v", bd)
	}
	if bd.OverrideAuthority != AuthorityManagerScope || bd.BypassReason != BypassManagerScope {
		t.Fatalf("the breakdown must name the authority and the reason, got authority=%q reason=%q",
			bd.OverrideAuthority, bd.BypassReason)
	}

	// The row landed. If 0133's widened CHECK were missing, the INSERT would violate
	// pricing_audit_bypass_reason_known, AppendAudit would swallow the error, and this count is 0.
	var n int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM pricing_audit_log
		 WHERE entity_type='margin_enforcement' AND action='BYPASS'
		   AND bypass_reason=$1 AND user_id=$2`, BypassManagerScope, actor).Scan(&n); err != nil {
		t.Fatalf("count bypass rows: %v", err)
	}
	if n != 1 {
		t.Fatalf("an honoured override must leave exactly one MANAGER_SCOPE_OVERRIDE row (0133 CHECK), got %d", n)
	}

	// And the viewer's read projects the new reason back out, the half a UI consumes.
	rows, err := svc.ListAudit(ctx, AuditFilter{EntityType: "margin_enforcement", Action: "BYPASS", Limit: 100})
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	seen := false
	for _, r := range rows {
		if r.BypassReason == BypassManagerScope {
			seen = true
		}
	}
	if !seen {
		t.Fatal("the MANAGER_SCOPE_OVERRIDE row must come back out of ListAudit")
	}
}

// TestIntegration_DIB420_RefusedRequestBlocksAndWritesNothing is the named integration proof for the
// first arm, against the real repository: the client flag alone is refused AND leaves no trace, so
// the bypass register counts authorised overrides and nothing else.
func TestIntegration_DIB420_RefusedRequestBlocksAndWritesNothing(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	f := newW4Fixture(t, pool)
	withAnchor(t, pool, f.productID, 10000, f.baseUOM)
	actor := dib420Actor(t, ctx, pool)

	var before int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM pricing_audit_log WHERE entity_type='margin_enforcement'`).Scan(&before); err != nil {
		t.Fatalf("count before: %v", err)
	}

	// realSvc wires NO override authority, this is exactly what cmd/server ships to a deployment
	// whose approvals substrate is not reachable, and it is the request W4-D described.
	bd, err := realSvc(pool).ResolvePrice(ctx, ResolveInput{
		ProductID: f.productID, CustomerType: "RETAIL", QtyEntered: 1, UOMEntered: f.baseUOM,
		BranchID: f.branchID, CostCents: 9900,
		OverrideRequested: true, UserID: actor,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !bd.Blocked || bd.BlockPolicy != BlockMarginBelowMinimum {
		t.Fatalf("the client flag alone must be refused against the real repository, got %+v", bd)
	}
	if bd.OverrideAuthority != "" || bd.BypassReason != "" {
		t.Fatalf("a refused request must claim no authority and no bypass reason, got %+v", bd)
	}

	var after int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM pricing_audit_log WHERE entity_type='margin_enforcement'`).Scan(&after); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if after != before {
		t.Fatalf("a REFUSED override must write no margin_enforcement rows, got %d new", after-before)
	}
}
