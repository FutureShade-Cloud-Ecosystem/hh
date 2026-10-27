//go:build integration

// policy_history_integration_test.go, the counter settings history, against a LIVE audit_log.
//
// WHY, WHEN policy_history_test.go ALREADY PASSES: because the failure being guarded is a DATABASE
// behaviour and a fake cannot have it. The unit tests drive a fake Recorder that collects
// SensitiveAccess values, so they never apply audit's deny-by-default Extra allowlist, never run
// `NULLIF($3,”)::uuid`, and never issue the `entity_id = $2::uuid` predicate the read depends on.
// billing/train9_autohold_audit_integration_test.go exists in this repo because exactly that gap hid
// a shipped defect, an entity id the uuid column rejected, an INSERT that raised 22P02, a
// best-effort record() that swallowed it, and a screen reporting success over an empty trail.
//
// The counter's ORG-DEFAULT policy row is branch_id NULL, which is the "" case, which is the case
// that writes an unreadable NULL entity id. The only way to catch "the write landed somewhere
// unreadable" is to go and look.
//
// Run: DATABASE_URL=… go test -tags=integration ./internal/pos/... -run PosPolicyHistory -count=1
package pos

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// TestIntegrationPosPolicyHistory_DealerDefaultSaveIsReadable drives the REAL service over the REAL
// repository with the REAL audit service, saves at the org-default scope, and reads the history back
// the way the settings screen does.
func TestIntegrationPosPolicyHistory_DealerDefaultSaveIsReadable(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	auditSvc := audit.NewService(audit.NewRepository(pool), nil)
	svc := NewService(repo).WithAudit(auditSvc).WithAuditTrail(auditSvc)
	ctx := context.Background()

	before, err := repo.ReadPosPolicy(ctx, "")
	if err != nil {
		t.Fatalf("read the org-default counter policy: %v", err)
	}
	t.Cleanup(func() { _ = repo.UpsertPosPolicy(context.Background(), before) })

	// The SUPER_ADMIN seeded by 0043, a real users row, so actor_id takes a uuid the way production
	// does and the read is made by somebody the service will actually admit.
	const adminID = "d7000000-0000-4000-9000-000000000010"
	callerCtx := identity.ContextWithStaff(ctx, identity.StaffContext{
		UserID: adminID, RoleCode: "SUPER_ADMIN",
		Tier: identity.TierLeadership, Scope: identity.ScopeGlobal,
	})

	rowsBefore, err := svc.PolicyHistory(callerCtx, "", 0)
	if err != nil {
		t.Fatalf("read the history: %v", err)
	}

	// Move ONE knob, at the org-default scope, the branch_id NULL row 0043 seeds.
	// balance_visibility was the knob here until UX-97 item 208 retired it; margin_visibility is
	// the same kind of move.
	next := before
	next.BranchID = ""
	if next.MarginVisibility == "HIDDEN" {
		next.MarginVisibility = "MANAGER_UP"
	} else {
		next.MarginVisibility = "HIDDEN"
	}
	if _, err := svc.UpdatePolicy(callerCtx, next); err != nil {
		t.Fatalf("save the policy: %v", err)
	}

	rowsAfter, err := svc.PolicyHistory(callerCtx, "", 0)
	if err != nil {
		t.Fatalf("re-read the history: %v", err)
	}
	if len(rowsAfter) != len(rowsBefore)+1 {
		t.Fatalf("an org-default save produced %d new READABLE history rows, want 1.\n"+
			"The row is written with NULLIF(entity_id,'')::uuid and read back with entity_id = $2::uuid, "+
			"so an empty scope writes SQL NULL and is never matched again, the write succeeds, the "+
			"screen stays empty, and nothing anywhere reports a failure.", len(rowsAfter)-len(rowsBefore))
	}

	got := rowsAfter[0] // newest first
	// balance_visibility was the knob this leg moved until UX-97 item 208 retired it into the
	// account position policy; margin_visibility is the same kind of move now.
	if got.Field != "margin_visibility" {
		t.Fatalf("newest row is %q, want margin_visibility", got.Field)
	}
	if got.Before == "" || got.After == "" {
		t.Fatalf("the persisted row lost a value: before=%q after=%q, audit's Extra allowlist is "+
			"deny-by-default and drops any key it does not name", got.Before, got.After)
	}
	if got.Before == "<nil>" || got.After == "<nil>" {
		t.Fatalf(`the history rendered the literal "<nil>": before=%q after=%q`, got.Before, got.After)
	}
	if got.Who != "an administrator" {
		t.Fatalf("who = %q, with no name resolver wired the row must degrade to the role word", got.Who)
	}
	if got.When == "" {
		t.Fatal("the row carries no timestamp")
	}
}

// TestIntegrationPosPolicyHistory_NoRowIsWrittenWithANullScope looks at the table directly. The unit
// tests prove the SERVICE asks for the sentinel; only SQL proves nothing has quietly landed in the
// unreadable NULL scope beside it.
func TestIntegrationPosPolicyHistory_NoRowIsWrittenWithANullScope(t *testing.T) {
	pool := intPool(t)
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action = $1 AND entity_id IS NULL`,
		ActPosPolicyChanged).Scan(&n); err != nil {
		t.Fatalf("count null-scope rows: %v", err)
	}
	if n != 0 {
		t.Fatalf("%d counter policy-change rows carry a NULL entity id, every one of them is "+
			"written, permanent and unreadable by any history this program can run", n)
	}
}
