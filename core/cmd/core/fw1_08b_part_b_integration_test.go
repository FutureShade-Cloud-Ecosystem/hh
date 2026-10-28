//go:build integration

package main

// FW1-08b part B (FROM-PRINCIPAL 162, 176) integration legs on real Postgres.
//
// TestFW1_08b_PartB_HeadOfficeSetChecksItsCandidate pins the head office set's candidate check:
// only a location with loc_type = 'BRANCH' in the caller's org is accepted, another org's branch
// and a non branch location are refused in words, and the non-controller refusal carries the
// set's own words, not the reopen ones (FROM-PRINCIPAL 206).

import (
	"context"
	"log/slog"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/branches"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/google/uuid"
)

func TestFW1_08b_PartB_HeadOfficeSetChecksItsCandidate(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "fw108bpbho")

	var trtID, kgnID, userID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE code = 'TRT'`).Scan(&trtID); err != nil {
		t.Fatalf("resolve TRT: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE code = 'KGN'`).Scan(&kgnID); err != nil {
		t.Fatalf("resolve KGN: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("resolve a user: %v", err)
	}

	// The caller's org and a second dealer org with a branch of its own, plus a zone row under
	// Trenton: the three candidates the check must tell apart.
	orgA, orgB := uuid.NewString(), uuid.NewString()
	foreignBranchID, zoneID := uuid.NewString(), uuid.NewString()
	mustExec(t, ctx, pool, `INSERT INTO org (id, slug, name) VALUES ($1, 'fw108bpba', 'FW1 08b Part B Org A')`, orgA)
	mustExec(t, ctx, pool, `INSERT INTO org (id, slug, name) VALUES ($1, 'fw108bpbb', 'FW1 08b Part B Org B')`, orgB)
	mustExec(t, ctx, pool, `UPDATE locations SET org_id = $1 WHERE loc_type = 'BRANCH'`, orgA)
	mustExec(t, ctx, pool, `
		INSERT INTO locations (id, branch_id, parent_id, loc_type, code, name, org_id)
		VALUES ($1::uuid, $1::uuid, NULL, 'BRANCH', 'FBD', 'Another org yard', $2::uuid)
	`, foreignBranchID, orgB)
	mustExec(t, ctx, pool, `
		INSERT INTO locations (id, branch_id, parent_id, loc_type, code, name, org_id)
		VALUES ($1::uuid, $2::uuid, $2::uuid, 'ZONE', 'ZONE-PB', 'Part B zone row', $3::uuid)
	`, zoneID, trtID, orgA)

	auditSvc := audit.NewService(audit.NewRepository(pool), slog.Default())
	glSvc := gl.NewService(gl.NewRepository(pool).WithZone(newDealerZone(pool))).
		WithAudit(auditSvc).
		WithAuditTxRecorder(auditSvc).
		WithBranchFacts(glBranchFacts{repo: branches.NewRepository(pool)})

	ctrl := identity.StaffContext{UserID: userID, RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}
	mgr := identity.StaffContext{UserID: userID, RoleCode: "SALES_MANAGER", Tier: identity.TierManager, Scope: identity.ScopeGlobal}
	scopedCtrl := identity.ContextWithStaff(authz.WithStaffScope(ctx, authz.StaffScope{OrgID: orgA}), ctrl)
	scopedMgr := identity.ContextWithStaff(authz.WithStaffScope(ctx, authz.StaffScope{OrgID: orgA}), mgr)

	t.Run("another_orgs_branch_refused_in_words", func(t *testing.T) {
		err := glSvc.SetHeadOfficeBranch(scopedCtrl, foreignBranchID, "part B: another org's branch")
		if err == nil {
			t.Fatal("want a refusal for another org's branch, got <nil>")
		}
		if status, ok := gl.ErrorHTTPStatus(err); !ok || status != 403 {
			t.Fatalf("another org's branch refusal status = (%d, %v), want (403, true); err=%v", status, ok, err)
		}
	})

	t.Run("non_branch_location_refused_in_words", func(t *testing.T) {
		err := glSvc.SetHeadOfficeBranch(scopedCtrl, zoneID, "part B: a zone is not a branch")
		if err == nil {
			t.Fatal("want a refusal for a non branch location, got <nil>")
		}
		if status, ok := gl.ErrorHTTPStatus(err); !ok || status != 422 {
			t.Fatalf("non branch location refusal status = (%d, %v), want (422, true); err=%v", status, ok, err)
		}
	})

	t.Run("the_non_controller_words_name_the_set_not_the_reopen", func(t *testing.T) {
		err := glSvc.SetHeadOfficeBranch(scopedMgr, kgnID, "part B: the set's own words")
		want := "gl: only the Financial Controller may set the head office branch"
		if err == nil || err.Error() != want {
			t.Fatalf("want the head office set's own refusal words %q, got %v", want, err)
		}
	})

	t.Run("a_branch_of_the_callers_org_is_still_accepted", func(t *testing.T) {
		if err := glSvc.SetHeadOfficeBranch(scopedCtrl, kgnID, "part B: relocate within the caller's org"); err != nil {
			t.Fatalf("SetHeadOfficeBranch(KGN): %v", err)
		}
		if ho, err := glSvc.HeadOfficeBranchID(scopedCtrl); err != nil || ho != kgnID {
			t.Fatalf("HeadOfficeBranchID after the set = %q (err=%v), want KGN (%q)", ho, err, kgnID)
		}
		if err := glSvc.SetHeadOfficeBranch(scopedCtrl, trtID, "part B: restore Trenton"); err != nil {
			t.Fatalf("SetHeadOfficeBranch(TRT) restore: %v", err)
		}
	})
}
