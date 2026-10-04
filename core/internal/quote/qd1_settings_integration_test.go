//go:build integration

// qd1_settings_integration_test.go, the settings' role rule (defect D9,
// CP0-5 answer A): the Sales manager changes the Quote Desk's settings for
// its own branch, the General Manager sets the dealer wide default. Also the
// wrong role ledger's row 15: a role CP0-5 does not name is refused.
package quote

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// - ledger row 15: save the quote policy as a role CP0-5 does not name -

func TestQD1_Row15_PolicySaveRefusedForUnnamedRoles(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

	for _, role := range []string{"INSIDE_SALES", "OUTSIDE_SALES", "YARD_MANAGER", "BRANCH_MANAGER", "FINANCIAL_CONTROLLER"} {
		who := qd1User(t, pool, "r15"+role)
		svc := qd1Svc(t, pool, now)
		ctx := qCtxAs(intStaff(intTrenton))
		ctx = qd1WithRole(ctx, role, intTrenton, who)

		_, err := svc.UpdateQuotePolicy(ctx, QuotePolicy{BranchID: intTrenton, CostVisibility: "MANAGER_UP"})
		if err == nil {
			t.Fatalf("row 15 (%s): saved the quote policy, want the 403 refusal", role)
		}
		if !errors.Is(err, ErrForbidden) {
			var nsa *NotSettingsAdminError
			if !errors.As(err, &nsa) {
				t.Fatalf("row 15 (%s): want the typed settings refusal or a 403, got %T: %v", role, err, err)
			}
		}
	}
}

// The Sales manager writes its own branch's row.
func TestQD1_PolicyBranchRowBySalesManager(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	mgr := qd1User(t, pool, "mgr")
	t.Cleanup(qd1ClearPolicyRow(t, pool, intTrenton))

	svc := qd1Svc(t, pool, now)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "SALES_MANAGER", intTrenton, mgr)

	p, err := svc.UpdateQuotePolicy(ctx, QuotePolicy{BranchID: intTrenton, CostVisibility: "MANAGER_UP", DefaultValidityDays: 30})
	if err != nil {
		t.Fatalf("a Sales manager must write its own branch's policy row: %v", err)
	}
	if p.BranchID != intTrenton {
		t.Errorf("the branch row must hold its branch, got %q", p.BranchID)
	}

	// Another branch's row is refused: the manager reaches Trenton alone.
	other := qCtxAs(intStaff(intTrenton))
	other = qd1WithRole(other, "SALES_MANAGER", intTrenton, mgr)
	if _, err := svc.UpdateQuotePolicy(other, QuotePolicy{BranchID: intKingston, CostVisibility: "MANAGER_UP"}); err == nil {
		t.Fatal("a Sales manager must not write another branch's policy row")
	}
	t.Cleanup(qd1ClearPolicyRow(t, pool, intKingston))
}

// The General Manager writes the NULL dealer default row, and only it.
func TestQD1_PolicyDefaultRowByGeneralManager(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	gm := qd1User(t, pool, "gm")
	t.Cleanup(qd1ClearPolicyRow(t, pool, ""))

	// The NULL dealer default row is the 0173 seed's; the test seeds it if
	// the world lacks it, and restores its absence afterwards.
	var haveDefault bool
	if err := pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM quote_policy WHERE branch_id IS NULL)`).Scan(&haveDefault); err != nil {
		t.Fatalf("read default policy row: %v", err)
	}
	if !haveDefault {
		if _, err := pool.Exec(context.Background(), `
			INSERT INTO quote_policy (branch_id, cost_visibility, default_validity_days) VALUES (NULL, 'MANAGER_UP', 30)`); err != nil {
			t.Fatalf("seed default policy row: %v", err)
		}
	}
	svc := qd1Svc(t, pool, now)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "GENERAL_MANAGER", intTrenton, gm)

	if _, err := svc.UpdateQuotePolicy(ctx, QuotePolicy{CostVisibility: "MANAGER_UP", DefaultValidityDays: 45}); err != nil {
		t.Fatalf("the General Manager must write the dealer default row: %v", err)
	}
	if _, err := svc.UpdateQuotePolicy(ctx, QuotePolicy{BranchID: intTrenton, CostVisibility: "MANAGER_UP"}); err == nil {
		t.Fatal("the General Manager must not write a branch row (CP0-5 A gives branch rows to the Sales manager)")
	}
	t.Cleanup(qd1ClearPolicyRow(t, pool, intTrenton))

	// The Sales manager may not write the dealer default row either.
	mgr := qd1User(t, pool, "mgr2")
	smCtx := qCtxAs(intStaff(intTrenton))
	smCtx = qd1WithRole(smCtx, "SALES_MANAGER", intTrenton, mgr)
	if _, err := svc.UpdateQuotePolicy(smCtx, QuotePolicy{CostVisibility: "MANAGER_UP"}); err == nil {
		t.Fatal("a Sales manager must not write the dealer default row")
	}
}

// The acceptance settings: reading opens to both, saving per the same rule.
func TestQD1_AcceptanceSettingsReadAndSave(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	mgr := qd1User(t, pool, "mgr")
	gm := qd1User(t, pool, "gm")
	clerk := qd1User(t, pool, "clerk")

	svc := qd1Svc(t, pool, now)

	mgrCtx := qCtxAs(intStaff(intTrenton))
	mgrCtx = qd1WithRole(mgrCtx, "SALES_MANAGER", intTrenton, mgr)
	gmCtx := qCtxAs(intStaff(intTrenton))
	gmCtx = qd1WithRole(gmCtx, "GENERAL_MANAGER", intTrenton, gm)
	clerkCtx := qCtxAs(intStaff(intTrenton))
	clerkCtx = qd1WithRole(clerkCtx, "INSIDE_SALES", intTrenton, clerk)

	if _, err := svc.AcceptanceSettings(mgrCtx); err != nil {
		t.Fatalf("reading the acceptance settings must open to the Sales manager: %v", err)
	}
	if _, err := svc.AcceptanceSettings(gmCtx); err != nil {
		t.Fatalf("reading the acceptance settings must open to the General Manager: %v", err)
	}
	if _, err := svc.AcceptanceSettings(clerkCtx); err == nil {
		t.Fatal("reading the acceptance settings must refuse a role CP0-5 does not name")
	}

	// Save returns the switch to its shipped off state whoever flips it.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM system_settings WHERE key = 'allow_quote_multi_accept'`)
	})
	if _, err := svc.SetAllowMultiAccept(mgrCtx, true, "qd1"); err != nil {
		t.Fatalf("a Sales manager must save the acceptance settings: %v", err)
	}
	if _, err := svc.SetAllowMultiAccept(gmCtx, false, "qd1"); err != nil {
		t.Fatalf("the General Manager must save the acceptance settings: %v", err)
	}
	if _, err := svc.SetAllowMultiAccept(clerkCtx, true, "qd1"); err == nil {
		t.Fatal("a role CP0-5 does not name must not save the acceptance settings")
	}
}

// qd1ClearPolicyRow removes a quote_policy row the settings tests wrote.
func qd1ClearPolicyRow(t *testing.T, pool *pgxpool.Pool, branchID string) func() {
	return func() {
		if branchID == "" {
			_, _ = pool.Exec(context.Background(),
				`DELETE FROM quote_policy WHERE branch_id IS NULL`)
			return
		}
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM quote_policy WHERE branch_id = $1`, branchID)
	}
}
