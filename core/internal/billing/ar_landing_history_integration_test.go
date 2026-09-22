//go:build integration

// ar_landing_history_integration_test.go, the receivables settings history, against a LIVE
// audit_log.
//
// WHY A SECOND TEST WHEN ar_landing_history_test.go ALREADY PASSES.
//
// Because the failure this guards is a DATABASE behaviour, and a fake cannot have it. The unit test
// drives a fake Recorder that collects SensitiveAccess values, so it never applies audit's
// deny-by-default Extra allowlist, never runs `NULLIF($3,”)::uuid`, and never issues the
// `entity_id = $2::uuid` predicate the read side depends on. train9_autohold_audit_integration_test
// in this same package exists because exactly that gap hid a shipped defect: an EntityID the uuid
// column rejected, an INSERT that raised 22P02, a best-effort record() that swallowed it, and a
// screen reporting success over an empty trail.
//
// THE AR LANDING IS THE SAME SHAPE, AND WORSE. It mounts with NO branch id and keeps branchId = ”,
// so every policy save this product makes targets the dealer scope. Written as "" that row lands as
// SQL NULL, and no equality predicate ever matches NULL again, the write would succeed, the read
// would return nothing, and the disclosure would be empty BY CONSTRUCTION with no error anywhere.
// The only way to catch "the write landed somewhere unreadable" is to go and look.
//
// Run: DATABASE_URL=… go test -tags=integration ./internal/billing/... -run ARPolicyHistory -count=1
package billing

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// TestIntegrationARPolicyHistory_DealerDefaultSaveIsReadable drives the REAL service over the REAL
// repository with the REAL audit service, saves at the scope the shipped panel actually uses, and
// then reads the history back the way the screen does.
func TestIntegrationARPolicyHistory_DealerDefaultSaveIsReadable(t *testing.T) {
	pool := intPool(t)
	repo := NewARLandingRepository(pool)
	auditSvc := audit.NewService(audit.NewRepository(pool), nil)
	svc := NewARLandingService(repo).WithAudit(auditSvc).WithAuditTrail(auditSvc)
	ctx := context.Background()

	before, err := repo.ReadARLandingPolicy(ctx, "")
	if err != nil {
		t.Fatalf("read the dealer policy: %v", err)
	}
	t.Cleanup(func() { _ = repo.UpsertARLandingPolicy(context.Background(), before) })

	// The SUPER_ADMIN seeded by 0043, a real users row, so the name resolution below has something
	// to resolve and the actor_id column takes a uuid the way production does.
	const adminID = "d7000000-0000-4000-9000-000000000010"
	callerCtx := identity.ContextWithStaff(ctx, identity.StaffContext{
		UserID: adminID, RoleCode: "SUPER_ADMIN",
		Tier: identity.TierLeadership, Scope: identity.ScopeGlobal,
	})

	rowsBefore, err := svc.PolicyHistory(callerCtx, "", 0)
	if err != nil {
		t.Fatalf("read the history: %v", err)
	}

	// Move ONE knob, at the DEALER scope, branch_id "", exactly what the settings panel sends.
	next := before
	if next.AgingEscalateDays == 75 {
		next.AgingEscalateDays = 80
	} else {
		next.AgingEscalateDays = 75
	}
	next.BranchID = ""
	if _, err := svc.SetPolicy(callerCtx, next); err != nil {
		t.Fatalf("save the policy: %v", err)
	}

	rowsAfter, err := svc.PolicyHistory(callerCtx, "", 0)
	if err != nil {
		t.Fatalf("re-read the history: %v", err)
	}
	if len(rowsAfter) != len(rowsBefore)+1 {
		t.Fatalf("a dealer-default save produced %d new READABLE history rows, want 1.\n"+
			"The row is written with NULLIF(entity_id,'')::uuid and read back with entity_id = $2::uuid, "+
			"so an empty scope writes SQL NULL and is never matched again, the write succeeds, the "+
			"screen stays empty, and nothing anywhere reports a failure.", len(rowsAfter)-len(rowsBefore))
	}

	got := rowsAfter[0] // newest first
	if got.Field != "aging_escalate_days" {
		t.Fatalf("newest row is %q, want aging_escalate_days", got.Field)
	}
	if got.Before == "" || got.After == "" {
		t.Fatalf("the persisted row lost a value: before=%q after=%q, audit's Extra allowlist is "+
			"deny-by-default and drops any key it does not name", got.Before, got.After)
	}
	if got.Before == "<nil>" || got.After == "<nil>" {
		t.Fatalf(`the history rendered the literal "<nil>": before=%q after=%q`, got.Before, got.After)
	}
	if got.Who == "" || got.Who == "somebody" {
		t.Fatalf("who = %q, the actor id did not survive the write, or the name did not resolve", got.Who)
	}
	if got.When == "" {
		t.Fatal("the row carries no timestamp")
	}
}

// TestIntegrationARPolicyHistory_NoRowIsWrittenWithANullScope looks at the table directly. The unit
// tests can prove the SERVICE asks for the sentinel; only SQL can prove nothing has quietly landed
// in the unreadable NULL scope beside it.
func TestIntegrationARPolicyHistory_NoRowIsWrittenWithANullScope(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = $1 AND entity_id IS NULL`,
		ActARPolicyChanged).Scan(&n); err != nil {
		t.Fatalf("count null-scope rows: %v", err)
	}
	if n != 0 {
		t.Fatalf("%d receivables policy-change rows carry a NULL entity id, every one of them is "+
			"written, permanent and unreadable by any history this program can run", n)
	}
}
