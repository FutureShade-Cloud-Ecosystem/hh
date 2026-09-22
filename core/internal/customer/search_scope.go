package customer

import (
	"context"
	"errors"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// -- U-park E4: server-side branch scoping of customer search -------------------------------------
//
// GET /api/v1/customers/search was staff-gated but not branch-fenced. The POS customer picker has
// always SENT a branch id; the server ignored it, so a branch-scoped rep at Trenton could type two
// letters and page through Kingston's account list, names, account numbers, credit limits and open
// balances, for accounts they have no business seeing. The picker filtering client-side would not
// have helped: the data was already on the wire.
//
// WHAT "THEIR BRANCH'S ACCOUNTS" MEANS. customers.home_branch_id is the account's owning branch. A
// scoped caller sees accounts homed to a branch it can reach, plus accounts with NO home branch,
// which are company-wide by construction (the WALK-IN house account is the canonical one, and an
// unassigned account belongs to everyone rather than to nobody). Hiding null-homed accounts would
// silently break the picker for exactly the accounts every branch needs.
//
// POSTURE (the landedcost List pattern, because it is the same question about the same kind of data):
//   - scoped caller, no branch named  → 403. Not an unfiltered sweep, and not a silent narrowing to
//     an arbitrary "first" branch: the caller must say which branch it is acting for.
//   - scoped caller, unreachable branch → 403.
//   - GLOBAL caller                     → unfiltered, or narrowed if it names a branch.
//
// There is no 404 arm here because a search returns a LIST: "no results" and "not allowed" are
// different answers and the second must not be disguised as the first.

// ErrForbidden is the out-of-scope answer. Exported so the handler maps it to 403 via errors.Is
// rather than letting it fall through to a 500 (the same shape ErrNotFound / ErrDuplicateAccountNo
// already use in this module).
var ErrForbidden = errors.New("customer: caller cannot search accounts for that branch")

// ErrUnauthenticated is returned when no staff caller is stamped on the context. The route is
// guard-gated, so this is a wiring failure rather than a user error, but it must not read as 500.
var ErrUnauthenticated = errors.New("customer: unauthenticated")

// SearchScope is the resolved fence a search runs under. BranchID == "" with Global == true means
// "no filter"; any other combination has already been validated by resolveSearchScope.
type SearchScope struct {
	BranchID string
	Global   bool
}

// nullUUID renders an empty branch id as a SQL NULL, so the fence predicate short-circuits to the
// pre-fence query rather than comparing against the empty string (which is not a uuid and would
// error). Kept next to the scope it serves.
func nullUUID(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// resolveSearchScope reads the guard-stamped staff caller and validates the requested branch.
func resolveSearchScope(ctx context.Context, branchID string) (SearchScope, error) {
	caller, ok := identity.StaffContextFromContext(ctx)
	if !ok || caller.UserID == "" {
		return SearchScope{}, ErrUnauthenticated
	}
	global := caller.Scope == identity.ScopeGlobal
	if branchID == "" {
		if !global {
			return SearchScope{}, ErrForbidden
		}
		return SearchScope{Global: true}, nil
	}
	if !caller.CanAccessBranch(branchID) {
		return SearchScope{}, ErrForbidden
	}
	return SearchScope{BranchID: branchID, Global: global}, nil
}
