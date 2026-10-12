package till

import (
	coreid "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

// nsfPolicyWriterRoles are the staff_role ids that write the org default NSF policy. The policy is a dealer
// wide money setting (fee, taxability, income account, auto hold), so a tier 2 manager, a branch role and the
// tier 1 hr role never write it. The decision matrix (D48, the money-desk surface) names no cell for this
// single setting, so the narrowest reading of "tier 1 global finance and general" applies: the general manager
// and the financial controller, plus the three platform roles, which stand outside the matrix and pass every
// staff role gate by the role gate's own ruling. The read route stays manager wide.
var nsfPolicyWriterRoles = map[string]bool{
	"general_manager":      true,
	"financial_controller": true,
	"super_admin":          true,
	"dealer_admin":         true,
	"fb_operator":          true,
}

// nsfPolicyWriter is the route's staff predicate over the caller's winning role. A person who holds a writer
// role beside a tier 1 role that outranks it on the id tie break (hr beside super_admin) resolves to the
// other role and is refused: fail closed, the grant is the fix.
func nsfPolicyWriter(a coreid.StaffAuthority) bool {
	return a.Tier == coreid.TierLeadership && a.Scope == coreid.ScopeGlobal && nsfPolicyWriterRoles[a.RoleID]
}
