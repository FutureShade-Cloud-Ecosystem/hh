package pos

// branchfence.go, the X2 wall as this module's reads and return rules ask
// it: one branch is a hard wall for every branch role on every read (D49).
// The wall is in the product and cannot be switched off; what the wall
// checks is the caller's RESOLVED authority, never a request claim: the
// bridged caller carries the grants the guards resolved per request, and a
// global role reaches everywhere by the same rule the guards apply.

import (
	"net/http"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// errBranchUnreachable is the refusal for a branch the caller's resolved
// authority does not reach. The caller named the branch, so the message may
// name it back; the refusal carries no other fact about the branch.
func errBranchUnreachable(branchID string) error {
	return clientErr(http.StatusForbidden, "pos: branch %s is not reachable with this credential", branchID)
}

// requireBranchReach refuses with the 403 wall when the caller's resolved
// authority does not reach the branch. Every read that shows a number about
// a branch, and every return that moves money for a sale, asks this first.
func requireBranchReach(caller identity.StaffContext, branchID string) error {
	if branchID == "" {
		return clientErr(http.StatusBadRequest, "pos: branch is required")
	}
	if !caller.CanAccessBranch(branchID) {
		return errBranchUnreachable(branchID)
	}
	return nil
}

// canonicalRole maps the bridged caller's role code onto the configuration
// vocabulary (staff_role.id). ok false is a role outside the vocabulary:
// the platform role and the two internal roles sit on no configuration
// list, so every list question about them answers no, never a guess.
func canonicalRole(caller identity.StaffContext) (string, bool) {
	return identity.CanonicalForERPCode(caller.RoleCode)
}
