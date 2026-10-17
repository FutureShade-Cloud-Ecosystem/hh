package billing

// branch_reach.go, the receivables reads' branch fence.
//
// A branch scoped caller holds grants to some branches and the book is cut by invoices.branch_id. The
// register and every read beside it answer for the caller's reach: the grants of a scoped caller,
// every branch for a global one. Payables and Receivables is global, so its reach is the whole book;
// a branch manager's is the branch it is granted.
//
// The reach is read from the caller the guard stamped on the request. A context with no stamp is an
// internal caller (the nightly sweep, the assistant's adapters, a test), and the HTTP doors always
// stamp: the fence is for requests, and an internal caller has no branch to be fenced to.
//
// Two answers, deliberately different:
//   - a branch NAMED outside the reach is refused (ErrBranchForbidden, 403): asking about a branch
//     you cannot see is a forbidden question, not an empty answer;
//   - an invoice BY ID outside the reach is not found (ErrNotFound, 404): a distinguishable refusal
//     there would be an existence oracle for another branch's documents.

import (
	"context"
	"errors"
	"sort"
	"strings"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// BranchForbiddenWords is what a person sees when they name a branch that is not theirs.
const BranchForbiddenWords = "that branch is not one of yours, the receivables book answers for your own branches only"

// ErrBranchForbidden is the refusal for a caller who names a branch outside its reach.
var ErrBranchForbidden = errors.New("billing: that branch is not one of the caller's branches")

// BranchReach is the branches a receivables read may answer for.
//
// Global true means every branch and IDs is ignored. Global false with an EMPTY IDs reaches nothing:
// a scoped caller with no grant sees an empty book, which is the truthful answer for it.
type BranchReach struct {
	Global bool
	IDs    []string
}

// AllBranches is the reach of an internal caller and of a global role.
var AllBranches = BranchReach{Global: true}

// reachOfCaller projects a caller onto the book.
func reachOfCaller(c identity.StaffContext) BranchReach {
	if c.Scope == identity.ScopeGlobal {
		return AllBranches
	}
	ids := make([]string, 0, len(c.Branches))
	for b, granted := range c.Branches {
		if granted {
			ids = append(ids, b)
		}
	}
	// Sorted so the SQL parameter does not depend on Go's randomised map order.
	sort.Strings(ids)
	return BranchReach{IDs: ids}
}

// reachFromContext reads the stamped caller. See the file header for the unstamped case.
func reachFromContext(ctx context.Context) BranchReach {
	c, ok := identity.StaffContextFromContext(ctx)
	if !ok {
		return AllBranches
	}
	return reachOfCaller(c)
}

// Allows reports whether one branch is inside the reach.
func (r BranchReach) Allows(branchID string) bool {
	if r.Global {
		return true
	}
	for _, id := range r.IDs {
		if id == branchID {
			return true
		}
	}
	return false
}

// SQLArg is the reach as a query argument for `($n::uuid[] IS NULL OR <col> = ANY($n::uuid[]))`:
// nil (SQL NULL) for every branch, a non nil slice (possibly empty, which matches nothing) otherwise.
func (r BranchReach) SQLArg() any {
	if r.Global {
		return nil
	}
	if r.IDs == nil {
		return []string{}
	}
	return r.IDs
}

// narrow applies an explicit branch to the reach: "" keeps the reach, a branch inside it narrows to
// that branch, a branch outside it is refused.
func (r BranchReach) narrow(branchID string) (BranchReach, error) {
	branchID = strings.TrimSpace(branchID)
	if branchID == "" {
		return r, nil
	}
	if !r.Allows(branchID) {
		return BranchReach{}, ErrBranchForbidden
	}
	return BranchReach{IDs: []string{branchID}}, nil
}
