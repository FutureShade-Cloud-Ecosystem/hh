package gl

import (
	"context"
	"errors"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

// round2BranchFacts is the branch facts seam with the LIVE distinction the head office candidate
// check needs (FW1-08b part B round 2, P3-1): a soft deleted branch is still a BRANCH row, so the
// plain existence read answers true for it, but it is not a live branch and a soft deleted branch
// is never a head office.
//
// The seam's live read is what the candidate check must run on. This fake carries it from the
// red leg on, so the red leg and the fix agree on the shape of the answer.
type round2BranchFacts struct {
	fakeBranchFacts
	deleted map[string]bool
}

// LiveBranchExists answers whether the id names a live branch: a BRANCH row that is not soft
// deleted.
func (f round2BranchFacts) LiveBranchExists(_ context.Context, id string) (bool, error) {
	if f.deleted[id] {
		return false, nil
	}
	return f.isBranch[id], nil
}

// TestFW1_08b_PartB_Round2_TheHeadOfficeSetRefusesAnUnscopedContext is the P2-1 red
// (FROM-PRINCIPAL 309): the set's org half used to hold only when the context carried a staff
// scope, so a call with no scope skipped the tenancy wall entirely and accepted a branch of any
// org. The rule is stated without a condition, so the set must refuse in words when the scope
// is absent, the way it already refuses when the branch facts seam is unwired.
func TestFW1_08b_PartB_Round2_TheHeadOfficeSetRefusesAnUnscopedContext(t *testing.T) {
	f := newFakeStore()
	ctrl := identity.StaffContext{UserID: "u-ctrl", RoleCode: controllerRole, Scope: identity.ScopeGlobal}
	svc := newTestServiceAs(f, ctrl).
		WithAudit(&fakeAuditRecorder{}).
		WithBranchFacts(round2BranchFacts{
			fakeBranchFacts: fakeBranchFacts{
				isBranch: map[string]bool{"branch-kgn": true},
				orgOf:    map[string]string{"branch-kgn": "org-a"},
				codeIDs:  map[string]string{"KGN": "branch-kgn"},
			},
		})

	err := svc.SetHeadOfficeBranch(context.Background(), "branch-kgn", "round 2: no staff scope on the context")
	if err == nil {
		t.Fatal("want a refusal in words when the context carries no staff scope, got <nil>")
	}
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 403 {
		t.Fatalf("want a 403 client refusal for an unscoped context, got %v", err)
	}
}

// TestFW1_08b_PartB_Round2_ASoftDeletedBranchIsNeverTheHeadOffice is the P3-1 red: the candidate
// check asked whether the id is a BRANCH row and ignored deleted_at, so a soft deleted branch
// passed and could be set as the head office, while the code read the same wave relies on
// (BranchRowByCodeTx) filters deleted_at. The two lookups must agree on what a live branch is.
func TestFW1_08b_PartB_Round2_ASoftDeletedBranchIsNeverTheHeadOffice(t *testing.T) {
	f := newFakeStore()
	ctrl := identity.StaffContext{UserID: "u-ctrl", RoleCode: controllerRole, Scope: identity.ScopeGlobal}
	svc := newTestServiceAs(f, ctrl).
		WithAudit(&fakeAuditRecorder{}).
		WithBranchFacts(round2BranchFacts{
			fakeBranchFacts: fakeBranchFacts{
				isBranch: map[string]bool{"branch-retired": true},
				orgOf:    map[string]string{"branch-retired": "org-a"},
				codeIDs:  map[string]string{},
			},
			deleted: map[string]bool{"branch-retired": true},
		})
	scoped := authz.WithStaffScope(context.Background(), authz.StaffScope{OrgID: "org-a"})

	err := svc.SetHeadOfficeBranch(scoped, "branch-retired", "round 2: a soft deleted branch is never a head office")
	if err == nil {
		t.Fatal("want a refusal in words for a soft deleted branch, got <nil>")
	}
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 422 {
		t.Fatalf("want a 422 client refusal for a soft deleted branch, got %v", err)
	}
}
