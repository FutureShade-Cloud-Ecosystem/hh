//go:build integration

// policy_integration_test.go, the 0396 warranty_policy table against a LIVE Postgres.
//
// WHY, WHEN policy_test.go ALREADY PASSES: because the failures being guarded are DATABASE
// behaviours a fake cannot have. The CHECK constraint refusing a negative threshold, the NULLS LAST
// branch-over-org resolution, UNIQUE not covering NULLs (the org row updates in place), and the audit
// row surviving audit's deny-by-default Extra allowlist and the NULLIF(”)::uuid entity write are all
// SQL facts. The pos policy_history integration test exists in this repo because exactly that last
// gap hid a shipped defect: a row written successfully into a scope no read could ever match.
//
// Run: DATABASE_URL=… go test -tags=integration ./internal/warranty/... -run WarrantyPolicy -count=1
package warranty

import (
	"context"
	"strconv"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// TestIntegrationWarrantyPolicy_OrgRowSeededAt5000 proves 0396 seeds the dealer default.
func TestIntegrationWarrantyPolicy_OrgRowSeededAt5000(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	p, err := repo.WarrantyPolicy(context.Background(), "")
	if err != nil {
		t.Fatalf("read the org policy: %v", err)
	}
	if p.ClaimThresholdCents != 5000 {
		t.Fatalf("org claim_threshold_cents = %d, want the 0396 seed 5000", p.ClaimThresholdCents)
	}
	if p.BranchID != "" {
		t.Fatalf("org row answered branch %q, want empty", p.BranchID)
	}
}

// TestIntegrationWarrantyPolicy_BranchOverrideRoundTrip drives the REAL SQL: a branch row overrides
// the org row, the org scope is untouched, and a second write updates the branch row in place (one
// row, not a second).
func TestIntegrationWarrantyPolicy_BranchOverrideRoundTrip(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()

	orgBefore, err := repo.WarrantyPolicy(ctx, "")
	if err != nil {
		t.Fatalf("read the org policy: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM warranty_policy WHERE branch_id::text = $1`, trentonBranch)
		_, _ = repo.UpsertWarrantyPolicy(context.Background(), PolicyInput{ClaimThresholdCents: orgBefore.ClaimThresholdCents}, "")
	})

	const adminID = "d7000000-0000-4000-9000-000000000010"
	saved, err := repo.UpsertWarrantyPolicy(ctx, PolicyInput{BranchID: trentonBranch, ClaimThresholdCents: 2500}, adminID)
	if err != nil {
		t.Fatalf("upsert the branch override: %v", err)
	}
	if saved.ClaimThresholdCents != 2500 || saved.BranchID != trentonBranch {
		t.Fatalf("read-back after upsert = branch %q threshold %d, want %q 2500", saved.BranchID, saved.ClaimThresholdCents, trentonBranch)
	}
	if saved.UpdatedBy != adminID {
		t.Fatalf("updated_by = %q, want the acting admin %q", saved.UpdatedBy, adminID)
	}

	over, err := repo.WarrantyPolicy(ctx, trentonBranch)
	if err != nil {
		t.Fatalf("read the branch policy: %v", err)
	}
	if over.ClaimThresholdCents != 2500 {
		t.Fatalf("branch resolution answered %d, want the override 2500", over.ClaimThresholdCents)
	}
	org, err := repo.WarrantyPolicy(ctx, "")
	if err != nil {
		t.Fatalf("read the org policy: %v", err)
	}
	if org.ClaimThresholdCents != orgBefore.ClaimThresholdCents {
		t.Fatalf("org threshold = %d, want %d untouched by the branch override", org.ClaimThresholdCents, orgBefore.ClaimThresholdCents)
	}

	// The second write is an UPDATE of the one branch row: UNIQUE (branch_id) holds exactly one.
	if _, err := repo.UpsertWarrantyPolicy(ctx, PolicyInput{BranchID: trentonBranch, ClaimThresholdCents: 1000}, adminID); err != nil {
		t.Fatalf("re-upsert the branch override: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM warranty_policy WHERE branch_id::text = $1`, trentonBranch).Scan(&n); err != nil {
		t.Fatalf("count branch rows: %v", err)
	}
	if n != 1 {
		t.Fatalf("%d policy rows for the branch after a re-upsert, want exactly 1 (UNIQUE branch_id)", n)
	}
	again, err := repo.WarrantyPolicy(ctx, trentonBranch)
	if err != nil {
		t.Fatalf("re-read the branch policy: %v", err)
	}
	if again.ClaimThresholdCents != 1000 {
		t.Fatalf("branch threshold = %d after the re-upsert, want 1000", again.ClaimThresholdCents)
	}
}

// TestIntegrationWarrantyPolicy_CheckRefusesANegativeThreshold looks at the constraint itself: the
// service's 422 is the first line, the CHECK is the backstop and this proves it holds.
func TestIntegrationWarrantyPolicy_CheckRefusesANegativeThreshold(t *testing.T) {
	pool := intPool(t)
	_, err := pool.Exec(context.Background(),
		`INSERT INTO warranty_policy (branch_id, claim_threshold_cents) VALUES (NULL, -1)`)
	if err == nil {
		t.Fatal("a negative claim_threshold_cents was stored; the CHECK (claim_threshold_cents >= 0) did not fire")
	}
}

// TestIntegrationWarrantyPolicy_AuditRowCarriesOldAndNew drives the REAL service over the REAL
// repository with the REAL audit service at the ORG-DEFAULT scope, the branch_id NULL row: the case
// that writes an unreadable NULL entity id if the sentinel is missing.
func TestIntegrationWarrantyPolicy_AuditRowCarriesOldAndNew(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	auditSvc := audit.NewService(audit.NewRepository(pool), nil)
	svc := NewService(repo).WithAudit(auditSvc)
	ctx := context.Background()

	before, err := repo.WarrantyPolicy(ctx, "")
	if err != nil {
		t.Fatalf("read the org policy: %v", err)
	}
	t.Cleanup(func() {
		_, _ = repo.UpsertWarrantyPolicy(context.Background(), PolicyInput{ClaimThresholdCents: before.ClaimThresholdCents}, "")
	})

	// The SUPER_ADMIN seeded by 0043, a real users row, so actor_id takes a uuid the way production does.
	const adminID = "d7000000-0000-4000-9000-000000000010"
	callerCtx := identity.ContextWithStaff(ctx, identity.StaffContext{
		UserID: adminID, RoleCode: "SUPER_ADMIN",
		Tier: identity.TierLeadership, Scope: identity.ScopeGlobal,
	})

	next := 2500
	if before.ClaimThresholdCents == 2500 {
		next = 5000
	}
	if _, err := svc.UpdatePolicy(callerCtx, PolicyInput{ClaimThresholdCents: int64(next)}); err != nil {
		t.Fatalf("save the threshold: %v", err)
	}

	const q = `
SELECT changes->>'field', changes->>'before', changes->>'after', entity_id::text
  FROM audit_log
 WHERE action = $1 AND entity_type = $2 AND entity_id = $3::uuid
 ORDER BY seq DESC
 LIMIT 1`
	var field, beforeV, afterV, entityID string
	if err := pool.QueryRow(ctx, q, actWarrantyPolicyChanged, policyEntity, policyOrgScopeID).Scan(&field, &beforeV, &afterV, &entityID); err != nil {
		t.Fatalf("the org-default threshold change left no READABLE audit row: %v. The row is written with "+
			"NULLIF(entity_id,'')::uuid and read back with entity_id = $3::uuid, so an empty scope writes SQL NULL, "+
			"the write succeeds, and no history can ever see it (the pos dealerScopeID lesson).", err)
	}
	if field != "claim_threshold_cents" {
		t.Fatalf("audit field = %q, want claim_threshold_cents", field)
	}
	if beforeV != strconv.FormatInt(before.ClaimThresholdCents, 10) || afterV != strconv.FormatInt(int64(next), 10) {
		t.Fatalf("audit before/after = %q/%q, want the old and new value %s/%s (audit's Extra allowlist is "+
			"deny-by-default and drops any key it does not name)", beforeV, afterV, strconv.FormatInt(before.ClaimThresholdCents, 10), strconv.FormatInt(int64(next), 10))
	}
}

// TestIntegrationWarrantyPolicy_NoRowIsWrittenWithANullScope looks at the table directly, the pos
// test's second leg: nothing has quietly landed in the unreadable NULL scope beside the sentinel.
func TestIntegrationWarrantyPolicy_NoRowIsWrittenWithANullScope(t *testing.T) {
	pool := intPool(t)
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action = $1 AND entity_id IS NULL`,
		actWarrantyPolicyChanged).Scan(&n); err != nil {
		t.Fatalf("count null-scope rows: %v", err)
	}
	if n != 0 {
		t.Fatalf("%d warranty policy-change rows carry a NULL entity id, every one of them is "+
			"written, permanent and unreadable by any read this program can run", n)
	}
}
