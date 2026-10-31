//go:build integration

// account_mapping_integration_test.go, W3 AX-1 item 1: the accounting package export's account
// mapping. Each HH GL account maps to a target package's account code, per target; the list is a
// manager read and the set is the Financial Controller's act (the brief's "RequireManager plus the
// Controller to set"). The export's unmapped refusal rides on the export door itself and is pinned
// in export_period_integration_test.go; this file pins the mapping.

package gl

import (
	"context"
	"strings"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// mappingCaller builds a service whose caller carries the named role. The mapping reads run as a
// plain branch manager, the set as the Financial Controller, so each test states who acts.
func mappingCaller(userID, roleCode string, tier identity.Tier) identity.StaffContext {
	return identity.StaffContext{UserID: userID, RoleCode: roleCode, Tier: tier, Scope: identity.ScopeGlobal}
}

func TestIntegration_AccountMapping_ListStartsEmpty(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	svc := svcAt(pool, mappingCaller(user, controllerRole, identity.TierLeadership), time.Now())
	rows, err := svc.ListAccountMapping(ctx, TargetQuickBooksOnline)
	if err != nil {
		t.Fatalf("list the mapping: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("a chart with no mapping set carries %d rows, want none: %+v", len(rows), rows)
	}
}

func TestIntegration_AccountMapping_TheControllerSetsAndTheListAnswers(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	svc := svcAt(pool, mappingCaller(user, controllerRole, identity.TierLeadership), time.Now())
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM gl_account_mapping WHERE target = $1`, TargetQuickBooksOnline)
	})

	if _, err := svc.SetAccountMapping(ctx, TargetQuickBooksOnline, "1000", "1010"); err != nil {
		t.Fatalf("the controller sets the cash mapping: %v", err)
	}
	rows, err := svc.ListAccountMapping(ctx, TargetQuickBooksOnline)
	if err != nil {
		t.Fatalf("list the mapping: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want the one mapping set", len(rows))
	}
	r := rows[0]
	if r.Target != TargetQuickBooksOnline || r.AccountCode != "1000" || r.PackageAccount != "1010" {
		t.Fatalf("row = %+v, want the mapping that was set", r)
	}
	if !strings.Contains(r.AccountName, "Cash") {
		t.Fatalf("the row must carry the HH account's own name beside the code, got %q", r.AccountName)
	}
}

func TestIntegration_AccountMapping_AManagerReadsButCannotSet(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	controller := svcAt(pool, mappingCaller(user, controllerRole, identity.TierLeadership), time.Now())
	manager := svcAt(pool, mappingCaller(user, "BRANCH_MANAGER", identity.TierManager), time.Now())
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM gl_account_mapping WHERE target = $1`, TargetQuickBooksOnline)
	})

	if _, err := controller.SetAccountMapping(ctx, TargetQuickBooksOnline, "6100", "6105"); err != nil {
		t.Fatalf("seed the controller's mapping: %v", err)
	}
	// The read is a manager read: the desk can see what the books export under.
	rows, err := manager.ListAccountMapping(ctx, TargetQuickBooksOnline)
	if err != nil {
		t.Fatalf("a manager reads the mapping: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("manager rows = %d, want the one mapping", len(rows))
	}
	// The set is the Controller's act alone, refused in words that name who may act.
	_, err = manager.SetAccountMapping(ctx, TargetQuickBooksOnline, "1000", "1010")
	if err == nil {
		t.Fatal("a branch manager set the export mapping; only the Financial Controller may")
	}
	if !strings.Contains(err.Error(), "Financial Controller") {
		t.Fatalf("the refusal must name the Financial Controller, got %q", err.Error())
	}
	rows, _ = controller.ListAccountMapping(ctx, TargetQuickBooksOnline)
	if len(rows) != 1 || rows[0].AccountCode != "6100" {
		t.Fatalf("the refused set must have written nothing, got %+v", rows)
	}
}

func TestIntegration_AccountMapping_AnUnknownTargetRefusesInWords(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	svc := svcAt(pool, mappingCaller(user, controllerRole, identity.TierLeadership), time.Now())

	_, err := svc.SetAccountMapping(ctx, "XERO", "1000", "9010")
	if err == nil {
		t.Fatal("an unknown target took a mapping row")
	}
	if !strings.Contains(err.Error(), "XERO") {
		t.Fatalf("the refusal must name the unknown target, got %q", err.Error())
	}
	if _, err := svc.ListAccountMapping(ctx, "XERO"); err == nil {
		t.Fatal("an unknown target listed a mapping")
	}
}

func TestIntegration_AccountMapping_ReSettingMovesTheRowNotAddsOne(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	svc := svcAt(pool, mappingCaller(user, controllerRole, identity.TierLeadership), time.Now())
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM gl_account_mapping WHERE target = $1`, TargetSage50) })

	if _, err := svc.SetAccountMapping(ctx, TargetSage50, "1000", "1110"); err != nil {
		t.Fatalf("first set: %v", err)
	}
	if _, err := svc.SetAccountMapping(ctx, TargetSage50, "1000", "1120"); err != nil {
		t.Fatalf("re-set: %v", err)
	}
	rows, err := svc.ListAccountMapping(ctx, TargetSage50)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 || rows[0].PackageAccount != "1120" {
		t.Fatalf("a re-set must move the one row, got %+v", rows)
	}
}

func TestIntegration_AccountMapping_TheTargetsAreIndependent(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	svc := svcAt(pool, mappingCaller(user, controllerRole, identity.TierLeadership), time.Now())
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM gl_account_mapping WHERE target = ANY($1::text[])`,
			[]string{TargetQuickBooksOnline, TargetSage50})
	})

	if _, err := svc.SetAccountMapping(ctx, TargetQuickBooksOnline, "1000", "1010"); err != nil {
		t.Fatalf("set the QuickBooks Online row: %v", err)
	}
	sage, err := svc.ListAccountMapping(ctx, TargetSage50)
	if err != nil {
		t.Fatalf("list the Sage 50 side: %v", err)
	}
	if len(sage) != 0 {
		t.Fatalf("the QuickBooks Online row leaked into Sage 50: %+v", sage)
	}
	if _, err := svc.SetAccountMapping(ctx, TargetSage50, "1000", "1110"); err != nil {
		t.Fatalf("set the Sage 50 row: %v", err)
	}
	both, err := svc.ListAccountMapping(ctx, "")
	if err != nil {
		t.Fatalf("list every target: %v", err)
	}
	if len(both) != 2 {
		t.Fatalf("the unmapped-target list answers %d rows, want both targets' rows", len(both))
	}
	if both[0].Target != TargetQuickBooksOnline || both[1].Target != TargetSage50 {
		t.Fatalf("the every-target list orders by target then code, got %+v", both)
	}
}
