package branches

import (
	"context"
	"sort"
)

// branchStore is the persistence the Service needs. Real impl: Repository (pgx); tests
// use a fake. Kept narrow (list + default resolve) so the seam stays a read-only reader.
type branchStore interface {
	List(ctx context.Context) ([]Branch, error)
	// ListInOrg is List held to the caller's dealer org; an empty org answers nothing.
	ListInOrg(ctx context.Context, orgID string) ([]Branch, error)
	DefaultBranchID(ctx context.Context) (string, error)
	// Exists asks whether a branch id names a row this dealer actually has. It is on the SAME
	// interface as the other two rather than a second seam because it is the same read of the same
	// table by the same reader, a third method here costs one fake method, and a second interface
	// would cost a second thing to forget to wire.
	Exists(ctx context.Context, id string) (bool, error)
	// Names resolves the display names of the branch ids a consumer already holds (the stock
	// lookup's out-at line today, change 5 slice S4). Same table, same reader, same reasoning as
	// Exists: a fourth method costs one fake method, a second interface costs a second wire.
	Names(ctx context.Context, ids []string) (map[string]string, error)
}

// Service is the branch read seam. It holds no cross-module state; it just resolves the
// seeded branch list and the system default branch.
type Service struct {
	store branchStore
}

func NewService(store branchStore) *Service { return &Service{store: store} }

// List returns both seeded branches with their addresses, each flagged is_default per the
// system default_branch_id (US-M7-06: "the branch list API returns both branches, 200"). A
// nil slice is normalized to [] by the handler, never null.
func (s *Service) List(ctx context.Context) ([]Branch, error) {
	return s.store.List(ctx)
}

// DefaultBranchID resolves the system default branch UUID (system_settings, 0014). ""
// when no default is configured, the caller decides whether that is acceptable.
func (s *Service) DefaultBranchID(ctx context.Context) (string, error) {
	return s.store.DefaultBranchID(ctx)
}

// Exists reports whether a branch id names a real branch. It is the consumer-facing half of the
// existence seam: dashboard declares its own one-method interface (dashboard.BranchResolver) and
// main.go satisfies it with a thin adapter over this, so no module outside this one learns that
// `locations` is where the answer lives.
//
// It is deliberately a BOOLEAN AND AN ERROR rather than a refusal. Whether an unknown branch is a
// 404, a 422 or a shrug is the CONSUMER's decision, the widget gate turns false into the same 422
// its four sibling refusals use, so the answer cannot be read back as "that branch is real"; a
// different consumer may reasonably want different words. A reader that decided the status here
// would be deciding it for callers it has never met.
func (s *Service) Exists(ctx context.Context, id string) (bool, error) {
	return s.store.Exists(ctx, id)
}

// BranchNames is the owning module's answer for the consumers that hold branch ids and need the
// names a screen shows. See Repository.Names for the reply's shape; what an unnamed id reads as
// stays the caller's decision.
func (s *Service) BranchNames(ctx context.Context, ids []string) (map[string]string, error) {
	return s.store.Names(ctx, ids)
}

// ListFor is List, fenced to the caller and defaulted for the caller.
//
// -- THE DEFECT THIS CLOSES ----------------------------------------------------------------------
//
// handler.list called List(ctx) and never read the StaffContext, so GET /api/v1/branches answered
// EVERY loc_type='BRANCH' row to every staff caller, a Kingston yard lead's branch picker offered
// Trenton, and the is_default flag it carried was the DEALER's default, which for that person is a
// yard they cannot reach. The precedent is one file over in the cockpit, and its header is the
// ruling: "The branch list comes from the caller's GRANTS, never from a query parameter. A dashboard
// that let you ask for another branch's numbers by editing a URL would be a nicer-looking version of
// the thing branch scoping exists to prevent" (dashboard/handler.go:65-69). A list is the same
// question one step earlier: the names you are offered are the names you may ask about.
//
// THE FENCE IS A FILTER AND ONLY EVER A FILTER. It cannot widen anything, a global caller gets what
// List returned, and a scoped caller gets a subset of it. The three cases are the ones
// internal/assist already writes down for the same table (adapters/catalogue.go:296-298): global sees
// all; granted sees those; NO GRANTS SEES NOTHING.
//
// THE DEFAULT IS RECOMPUTED AFTER THE FENCE, not before, because a flag on a row that was filtered
// out is not a flag anybody receives. See the Branch header for what the two default fields now mean.
// The recompute is deterministic, the rows arrive ORDER BY code (repository.go:95) and are not
// reordered here, for the reason dashboard/handler.go:82 gives about its own sort: "so a provider's
// SQL plan and any test assertion are stable". A default that depended on map iteration order would
// land two logins of the same person on two different yards.
//
// REVERT COST: delete this method and point handler.list back at List. Nothing else consumes it -
// internal/assist reaches List directly through its own BranchLister interface and applies its own
// fence, which is why List keeps its signature untouched.
func (s *Service) ListFor(ctx context.Context, who CallerScope) ([]Branch, error) {
	all, err := s.store.ListInOrg(ctx, who.OrgID)
	if err != nil {
		return nil, err
	}
	// The dealer-wide fact is captured from the UNFENCED rows, because the row carrying it is
	// exactly the row a scoped caller is most likely to have filtered away.
	for i := range all {
		all[i].IsSystemDefault = all[i].IsDefault
	}
	out := fenceBranches(who, all)
	markCallerDefault(out)
	return out, nil
}

// fenceBranches narrows the dealer's list to what this caller was granted.
//
// A retired branch is NOT dropped here. The three shipping clients each filter is_active themselves
// (the quote builder's picker, the widget branch field, the staff-roles grant control), and a reader
// that silently removed a row would make "this person holds a yard that has closed" invisible to the
// one screen, the grant editor, whose whole job is to show exactly that.
func fenceBranches(who CallerScope, all []Branch) []Branch {
	if who.AllBranches {
		return all
	}
	granted := make(map[string]bool, len(who.BranchIDs))
	for _, id := range who.BranchIDs {
		granted[id] = true
	}
	out := make([]Branch, 0, len(granted))
	for _, b := range all {
		if granted[b.ID] {
			out = append(out, b)
		}
	}
	return out
}

// markCallerDefault sets is_default on exactly one row, the one a picker should open on.
//
// THE ORDER OF THE THREE CASES IS THE RULE. The dealer's default wins when the caller can actually
// reach it, because that is what the dealer configured and a caller who holds it should not be moved
// off it. Otherwise the caller's first LIVE grant in code order, which is the only answer that is
// both theirs and stable. And if every yard they hold is retired, NOTHING is flagged: landing a
// person on a closed yard is worse than landing them nowhere, because nowhere is visible and a
// closed yard looks like it worked.
func markCallerDefault(rows []Branch) {
	for i := range rows {
		rows[i].IsDefault = false
	}
	for i := range rows {
		if rows[i].IsSystemDefault && rows[i].IsActive {
			rows[i].IsDefault = true
			return
		}
	}
	for i := range rows {
		if rows[i].IsActive {
			rows[i].IsDefault = true
			return
		}
	}
}

// SortedGrants is the deterministic grant list the handler hands ListFor. It is here rather than in
// the handler so the ordering rule and the code that depends on it sit together.
func SortedGrants(ids []string) []string {
	out := append([]string(nil), ids...)
	sort.Strings(out)
	return out
}
