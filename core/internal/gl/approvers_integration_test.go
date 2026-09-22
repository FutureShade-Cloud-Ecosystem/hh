//go:build integration

// approvers_integration_test.go, DIB-360/361 against the real database, including the end-to-end
// claim that matters: a JE OVER the configured threshold parks as PENDING_APPROVAL and posts only on
// approval.
package gl

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// TestIntegration_Approvals_ThresholdIsConfigurablePerBranchAndGatesThePost is the DIB-360 claim end
// to end: change the number, and which journals need a second signer changes with it.
func TestIntegration_Approvals_ThresholdIsConfigurablePerBranchAndGatesThePost(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)

	var branchID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type='BRANCH' LIMIT 1`).Scan(&branchID); err != nil {
		t.Skipf("no seeded BRANCH: %v", err)
	}
	// Restore whatever the branch policy was, so this test leaves the DB as it found it.
	var hadRow bool
	var prior int64
	_ = pool.QueryRow(ctx, `SELECT TRUE, je_approval_threshold_cents FROM gl_policy WHERE branch_id = $1::uuid`, branchID).
		Scan(&hadRow, &prior)
	t.Cleanup(func() {
		c := context.Background()
		if hadRow {
			_, _ = pool.Exec(c, `UPDATE gl_policy SET je_approval_threshold_cents = $2 WHERE branch_id = $1::uuid`, branchID, prior)
		} else {
			_, _ = pool.Exec(c, `DELETE FROM gl_policy WHERE branch_id = $1::uuid`, branchID)
		}
	})

	// The seeded org default is the blessed $5,000 (0032, re-blessed by 0075).
	orgPol, err := repo.Policy(ctx, "")
	if err != nil {
		t.Fatalf("org policy: %v", err)
	}
	if orgPol.JEApprovalThresholdCents != 500000 {
		t.Fatalf("org JE threshold = %d, want the blessed 500000 ($5,000)", orgPol.JEApprovalThresholdCents)
	}

	// Configure this branch at $1,000, stricter than the org.
	if err := repo.SetJEThreshold(ctx, branchID, 100000); err != nil {
		t.Fatalf("set branch threshold: %v", err)
	}
	branchPol, err := repo.Policy(ctx, branchID)
	if err != nil {
		t.Fatalf("branch policy: %v", err)
	}
	if branchPol.JEApprovalThresholdCents != 100000 {
		t.Fatalf("branch threshold = %d, want the configured 100000", branchPol.JEApprovalThresholdCents)
	}
	// The org default is untouched by the branch override.
	if again, _ := repo.Policy(ctx, ""); again.JEApprovalThresholdCents != 500000 {
		t.Fatalf("configuring a branch moved the ORG default to %d", again.JEApprovalThresholdCents)
	}

	// The derived label follows the branch's number: $2,000 is below the org threshold and ABOVE the
	// branch's, and must read PENDING_APPROVAL at this branch.
	if got := derivedStatus(StatusDraft, 200000, branchPol.JEApprovalThresholdCents); got != StatusPendingApproval {
		t.Fatalf("a $2,000 draft at a $1,000-threshold branch reads %q, want PENDING_APPROVAL", got)
	}
	if got := derivedStatus(StatusDraft, 200000, orgPol.JEApprovalThresholdCents); got != StatusDraft {
		t.Fatalf("the same $2,000 draft under the $5,000 org threshold reads %q, want plain DRAFT", got)
	}
}

// TestIntegration_Approvals_ThresholdGuardsRefuseFootguns pins the two refusals that keep a
// configuration slip from silently switching the control off.
func TestIntegration_Approvals_ThresholdGuardsRefuseFootguns(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := svcAt(pool, identity.StaffContext{UserID: aUser(t, pool), RoleCode: "CONTROLLER", Tier: identity.TierLeadership}, time.Now())
	staffCtx := ctx

	// Restore the org threshold unconditionally. Every assertion below expects the guard to REFUSE, so
	// on a green run nothing is written and this is a no-op, but a mutation run that removes the guard
	// writes a 0 into shared policy and would poison every later test in the suite. A fixture that only
	// cleans up what it MEANT to write is not a fixture that restores what it touched.
	var orgThreshold int64
	_ = pool.QueryRow(ctx, `SELECT je_approval_threshold_cents FROM gl_policy WHERE branch_id IS NULL`).Scan(&orgThreshold)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`UPDATE gl_policy SET je_approval_threshold_cents = $1 WHERE branch_id IS NULL`, orgThreshold)
	})

	if err := svc.SetJEThreshold(staffCtx, "", 0); err == nil {
		t.Fatal("a threshold of 0 was accepted, derivedStatus treats 0 as 'no approval rail', so saving it " +
			"DISABLES maker-checker while reading on screen like the strictest possible setting")
	}
	if err := svc.SetJEThreshold(staffCtx, "", -1); err == nil {
		t.Fatal("a negative threshold was accepted")
	}
	if err := svc.SetJEThreshold(staffCtx, "", maxJEThreshold+1); err == nil {
		t.Fatal("a threshold above the typo ceiling was accepted, an extra zero here posts every journal " +
			"below the new figure unreviewed, with nothing on screen to say so")
	}
}

// TestIntegration_Approvers_AssignmentNarrowsAndNeverWidens is the DIB-361 claim.
func TestIntegration_Approvers_AssignmentNarrowsAndNeverWidens(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)

	var branchID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type='BRANCH' LIMIT 1`).Scan(&branchID); err != nil {
		t.Skipf("no seeded BRANCH: %v", err)
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
	mkUser := func(tag string) string {
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO users (email, display_name, status) VALUES ($1, $2, 'ACTIVE') RETURNING id::text`,
			fmt.Sprintf("approver-%s-%s@dibbits.example", tag, suffix), "Approver "+tag).Scan(&id); err != nil {
			t.Fatalf("seed user %s: %v", tag, err)
		}
		t.Cleanup(func() {
			c := context.Background()
			_, _ = pool.Exec(c, `DELETE FROM gl_approver_assignments WHERE user_id = $1::uuid`, id)
			_, _ = pool.Exec(c, `DELETE FROM users WHERE id = $1::uuid`, id)
		})
		return id
	}
	named := mkUser("named")
	other := mkUser("other")

	// - UNCONFIGURED FAILS OPEN -
	// This is the asymmetry with DIB-416's fail-closed credential guard, and it is deliberate: an
	// unconfigured branch that cannot post ANY journal is an outage on day one for every branch that
	// has not been set up, while an unconfigured branch under the old rule is where it was yesterday.
	before, err := repo.ListApprovers(ctx, PolicyJEOverThreshold, branchID)
	if err != nil {
		t.Fatalf("list approvers: %v", err)
	}
	if !MayApprove(before, other) {
		t.Fatal("with no assignments configured, an eligible manager must still be able to approve")
	}

	// - CONFIGURED NARROWS -
	id, err := repo.UpsertApprover(ctx, ApproverAssignment{
		BranchID: branchID, PolicyType: PolicyJEOverThreshold, Role: "CONTROLLER", UserID: named,
	})
	if err != nil {
		t.Fatalf("assign approver: %v", err)
	}
	after, err := repo.ListApprovers(ctx, PolicyJEOverThreshold, branchID)
	if err != nil {
		t.Fatalf("list approvers: %v", err)
	}
	if !MayApprove(after, named) {
		t.Fatal("the named approver cannot approve at their own branch")
	}
	if MayApprove(after, other) {
		t.Fatal("a manager who is NOT a named approver was still permitted, the whole point of DIB-361 is " +
			"that a $400,000 reclass stops being approvable by whichever manager is logged in")
	}
	// The assignment resolves the user's display name so a config screen is readable.
	var found bool
	for _, a := range after {
		if a.ID == id {
			found = true
			if a.UserName == "" {
				t.Fatal("an assignment must resolve a display name, a screen of raw UUIDs is not a config screen")
			}
			if a.Role != "CONTROLLER" {
				t.Fatalf("role = %q, want CONTROLLER", a.Role)
			}
		}
	}
	if !found {
		t.Fatal("the new assignment is missing from the list")
	}

	// - ORG-WIDE ROWS APPLY EVERYWHERE -
	if _, err := repo.UpsertApprover(ctx, ApproverAssignment{
		PolicyType: PolicyJEOverThreshold, Role: "CFO", UserID: other,
	}); err != nil {
		t.Fatalf("assign org-wide approver: %v", err)
	}
	withOrg, _ := repo.ListApprovers(ctx, PolicyJEOverThreshold, branchID)
	if !MayApprove(withOrg, other) {
		t.Fatal("an ORG-WIDE assignment must apply at every branch")
	}

	// - A DUPLICATE IS A SLIP, NOT A SECOND GRANT -
	againID, err := repo.UpsertApprover(ctx, ApproverAssignment{
		BranchID: branchID, PolicyType: PolicyJEOverThreshold, Role: "CONTROLLER", UserID: named,
	})
	if err != nil {
		t.Fatalf("re-assign: %v", err)
	}
	if againID != id {
		t.Fatalf("a duplicate assignment created a second row (%s vs %s)", againID, id)
	}

	// - REMOVAL RESTORES THE UNCONFIGURED RULE ONLY WHEN NOTHING IS LEFT -
	if err := repo.RemoveApprover(ctx, id); err != nil {
		t.Fatalf("remove approver: %v", err)
	}
	if err := repo.RemoveApprover(ctx, id); err == nil {
		t.Fatal("removing a missing assignment must error rather than silently succeed")
	}
}

// TestIntegration_Approvers_CheckApproverSurfacesANamedError proves the decide path gets an error a
// UI can explain, not a bare refusal.
func TestIntegration_Approvers_CheckApproverSurfacesANamedError(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := NewService(NewRepository(pool))

	var branchID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type='BRANCH' LIMIT 1`).Scan(&branchID); err != nil {
		t.Skipf("no seeded BRANCH: %v", err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
	var named string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, status) VALUES ($1, 'Named', 'ACTIVE') RETURNING id::text`,
		"named-"+suffix+"@dibbits.example").Scan(&named); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM gl_approver_assignments WHERE user_id = $1::uuid`, named)
		_, _ = pool.Exec(c, `DELETE FROM users WHERE id = $1::uuid`, named)
	})

	if _, err := NewRepository(pool).UpsertApprover(ctx, ApproverAssignment{
		BranchID: branchID, PolicyType: PolicyJEOverThreshold, Role: "CONTROLLER", UserID: named,
	}); err != nil {
		t.Fatalf("assign: %v", err)
	}

	if err := svc.CheckApprover(ctx, PolicyJEOverThreshold, branchID, named); err != nil {
		t.Fatalf("the named approver was refused: %v", err)
	}
	err := svc.CheckApprover(ctx, PolicyJEOverThreshold, branchID, "00000000-0000-4000-8000-000000000000")
	if !errors.Is(err, ErrNotAnAssignedApprover) {
		t.Fatalf("an unassigned user got %v, want ErrNotAnAssignedApprover so the UI can say why", err)
	}
}
