// Package branches is a small read-only reader over the seeded branch locations
// (migration 0003 roots + 0017 real addresses) plus the system default branch
// (system_settings.default_branch_id, migration 0014). It exists to close the
// US-M7-06 acceptance gap (scope reconciliation 2026-07-14, A-1): the branch DATA
// was seeded but GET /api/v1/branches had no route and 404'd live.
//
// Module boundary: it is a *reader only* of the locations BRANCH rows and the
// default_branch_id setting, it never writes either, and it does not reach into
// any other module's tables. It is intentionally tiny (one list read + a default
// resolve), not a heavyweight module.
package branches

// Address is the structured branch address projected from locations.address (JSONB,
// 0003/0017). Fields map to the seeded keys; any absent key serializes as "".
type Address struct {
	Line1    string `json:"line1"`
	City     string `json:"city"`
	Province string `json:"province"`
	Postal   string `json:"postal"`
	Country  string `json:"country"`
}

// Branch is the read view of a locations BRANCH row (loc_type='BRANCH'): the fixed
// UUID, the yard code (TRT/KGN), the display name, its address, and the two senses of
// "default" the list has to keep apart.
//
// -- IsDefault IS NOW THE CALLER'S DEFAULT, NOT THE DEALER'S, AND THAT IS A MEANING CHANGE --
//
// It used to be one fact: system_settings.default_branch_id, the same answer for everybody. That
// was serviceable only while GET /api/v1/branches returned everybody the same list. It no longer
// does, handler.list fences the list to the caller's own grants, and a flag that survives its
// own row being filtered out is a flag that is false on every row that remains. The person it is
// false for is exactly the branch-scoped person the fence exists for: a Kingston yard lead, whose
// dealer defaults to Trenton, opens a branch picker with nothing preselected and picks whichever
// yard the list happened to sort first.
//
// So IsDefault answers the question the picker is actually asking, WHICH ONE OF THESE SHOULD BE
// SELECTED WHEN I OPEN, and it is computed per caller in Service.ListFor: the dealer's default
// when this caller holds it, otherwise their first live grant in code order. Exactly one row in a
// non-empty list carries it, which is the invariant every consumer was already assuming and none
// of them could rely on.
//
// IsSystemDefault is the raw dealer-wide fact, unfenced and unchanged, kept so nothing is lost:
// an admin screen that needs to show which yard the DEALER defaults to reads this one, and a
// picker that needs to know where to land reads IsDefault. Two questions, two fields, neither
// standing in for the other.
//
// THE REVERT COST IS ONE LINE in ListFor (drop the recompute, and IsDefault is the repository's
// system flag again). The field order and the JSON keys do not move, so nothing downstream has to
// be touched to undo it.
type Branch struct {
	ID      string  `json:"id"`
	Code    string  `json:"code"`
	Name    string  `json:"name"`
	Address Address `json:"address"`
	// IsDefault: the branch THIS CALLER should land on. Caller-relative, see the header above.
	IsDefault bool `json:"is_default"`
	// IsSystemDefault: system_settings.default_branch_id (0014), the dealer-wide fact.
	IsSystemDefault bool `json:"is_system_default"`
	IsActive        bool `json:"is_active"`
}

// CallerScope is a caller's branch authority expressed in STRINGS, and the reason it is not simply
// identity.StaffContext is the seam dashboard/handler.go:20-22 states for itself: "internal/identity
// is the ONE peer this package imports, and only in this file: the handler has to know who is
// asking. The service and the resolver below it never see an identity type."
//
// This package now has to know who is asking too (handler.list reads the StaffContext), so it takes
// the same shape rather than inventing a second one: the HANDLER translates identity into these two
// fields, and the service below it gates on strings it cannot mistake for an authority object.
type CallerScope struct {
	// OrgID is the dealer org the middleware resolved for the request (UX-88). Even a global-scope
	// caller is global inside this org only: the list never names a branch of another org or of no org.
	OrgID string
	// AllBranches is a GLOBAL-scope caller (identity.ScopeGlobal). The list is not fenced for them,
	// which is identity.StaffContext.CanAccessBranch's own rule (rbac.go:103-109) said once more.
	AllBranches bool
	// BranchIDs are the caller's OWN grants. EMPTY WITH AllBranches FALSE MEANS THEY SEE NOTHING -
	// it never falls through to "everything". That is the middle case internal/assist's fenceBranches
	// (adapters/catalogue.go:296-298) calls "the point", and it is the point here for the same
	// reason: an empty grant list is a fact about the person, not a missing filter.
	BranchIDs []string
}
