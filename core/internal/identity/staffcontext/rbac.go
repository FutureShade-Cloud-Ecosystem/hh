package staffcontext

import (
	"fmt"
	"net/http"
	"sort"
	"time"
)

// -- A-20 RBAC domain (W1) --------------------------------------------
// 13 roles across 3 tiers (migration 0015). Scope is GLOBAL (operates across
// both branches) or BRANCH (limited to granted branches). The authority model
// below is a documented best-assumption pending the dealer principals'
// confirmation (Q-029, decision D-016) and is intentionally isolated so it can
// be tuned post-call.
//
// CARRY NOTE (the identity bridge). This file is the ERP staff authority
// vocabulary carried from backend/internal/identity/rbac.go at the pinned
// commit, under its own package so every declaration keeps the name the
// ported modules compile against. It is vocabulary only: the HS256 session
// world that once resolved these values is superseded by the Phase B
// verifier and the staff_role_grant resolver, and resolution arrives through
// the bridge in bridge.go (FromAuthority, CallerFrom), never through a
// re-point of the predicates below.

type Tier int

const (
	TierLeadership Tier = 1 // General Manager, Financial Controller, HR, org-wide
	TierManager    Tier = 2 // branch managers + central Payables/Receivables
	TierFrontline  Tier = 3 // inside/outside sales, yard team, drivers
)

type Scope string

const (
	ScopeGlobal Scope = "GLOBAL" // reaches both branches
	ScopeBranch Scope = "BRANCH" // limited to granted branches
)

// RoleDef is the canonical definition of one of the 13 roles (mirrors migration 0015).
type RoleDef struct {
	Code  string `json:"code"`
	Name  string `json:"name"`
	Tier  Tier   `json:"tier"`
	Scope Scope  `json:"scope"`
}

// RoleSuperAdmin is the platform-owner role (the platform owner's own login) -- a 14th role
// that sits ABOVE the 13 Dibbits business roles (migration 0043). It shares the
// leadership tier (1) + GLOBAL scope so it satisfies every existing authority gate,
// but it is the only role permitted to mint or assign another platform admin (the
// escalation guard in the service). It is intentionally kept OUT of Roles() so the
// business role catalog stays the 13-role/3-tier model; AllRoles() adds it back for
// validation and the create-user surface.
const RoleSuperAdmin = "SUPER_ADMIN"

// RoleDrivers names the one role whose SESSION LENGTH is a dealer knob (0209).
//
// It is a constant rather than a literal for a narrow reason: auth.go's maximum-lifetime ceiling now
// asks "is this token asserting the driver role" on every verification, and a typo in that string
// would not fail -- it would silently apply the twelve-hour ceiling to a driver's legitimately longer
// token and sign them out mid-shift, which is exactly the failure the knob exists to prevent. A
// constant shared with Roles() below cannot drift from the role code the login path stamps.
//
// CARRY NOTE: the session-length ceiling the comment describes lives in the target
// as identity.SessionCeiling over the canonical role id (staff_role 'driver');
// this constant remains the vocabulary name for the role code, and bridge.go
// maps 'driver' to DRIVERS so the name keeps meaning one role.
const RoleDrivers = "DRIVERS"

// superAdminDef is the platform-owner role definition (mirrors migration 0043).
func superAdminDef() RoleDef {
	return RoleDef{RoleSuperAdmin, "Platform Administrator", TierLeadership, ScopeGlobal}
}

// AllRoles is the 13 business roles plus the SUPER_ADMIN platform role -- the full
// assignable set a platform admin may grant. The platform role is listed first so it
// reads as the top of the hierarchy.
func AllRoles() []RoleDef {
	return append([]RoleDef{superAdminDef()}, Roles()...)
}

// Roles returns the 13 Dibbits staff roles. Source of truth is the DB (0015);
// this in-code catalog backs the admin UI dropdown and keeps tests DB-free.
func Roles() []RoleDef {
	return []RoleDef{
		{"GENERAL_MANAGER", "General Manager", TierLeadership, ScopeGlobal},
		{"FINANCIAL_CONTROLLER", "Financial Controller", TierLeadership, ScopeGlobal},
		{"HR", "HR", TierLeadership, ScopeGlobal},
		{"BRANCH_MANAGER", "Branch Manager", TierManager, ScopeBranch},
		{"PROCUREMENT_MANAGER", "Procurement Manager", TierManager, ScopeBranch},
		{"SALES_MANAGER", "Sales Manager", TierManager, ScopeBranch},
		{"YARD_MANAGER", "Yard Manager", TierManager, ScopeBranch},
		{"LOGISTICS_MANAGER", "Logistics Manager", TierManager, ScopeBranch},
		{"PAYABLES_RECEIVABLES", "Payables/Receivables", TierManager, ScopeGlobal},
		{"INSIDE_SALES", "Inside Sales", TierFrontline, ScopeBranch},
		{"OUTSIDE_SALES", "Outside Sales", TierFrontline, ScopeBranch},
		{"YARD_TEAM", "Yard Team", TierFrontline, ScopeBranch},
		{RoleDrivers, "Drivers", TierFrontline, ScopeBranch},
	}
}

// StaffContext is a user's resolved authority for a given request. It is rebuilt
// from the DB on every authenticated request (never trusted from the token), so a
// role or status change takes effect immediately.
type StaffContext struct {
	UserID   string
	RoleCode string
	Tier     Tier
	Scope    Scope
	Status   string          // users.status, only ACTIVE may authenticate
	Branches map[string]bool // granted branch IDs (ignored when Scope == GLOBAL)
}

// IsManager reports whether the caller is a Tier-1 or Tier-2 role (an assigned
// manager or leadership member), i.e. not front-line and not unassigned (Tier 0).
func (s StaffContext) IsManager() bool { return s.Tier >= TierLeadership && s.Tier <= TierManager }

// IsAdmin: a Tier-1 GLOBAL user bypasses branch scoping entirely.
func (s StaffContext) IsAdmin() bool { return s.Tier == TierLeadership && s.Scope == ScopeGlobal }

// IsSuperAdmin reports whether the caller holds the platform-owner role. The super
// admin is a Tier-1 GLOBAL role, so it already passes IsAdmin/IsManager; this narrower
// predicate gates the platform-only actions (minting or assigning another SUPER_ADMIN).
func (s StaffContext) IsSuperAdmin() bool { return s.RoleCode == RoleSuperAdmin }

// CanAccessBranch: GLOBAL users reach any branch; scoped users only their grants.
func (s StaffContext) CanAccessBranch(branchID string) bool {
	if s.Scope == ScopeGlobal {
		return true
	}
	return s.Branches[branchID]
}

// CanReachAccountHome is the customer module's account rule (customer/account_lookup.go) as a predicate on an
// account's home branch: an account homed at a branch is held to callers who reach that branch, and an account
// with no home ("") is shared company wide. A global caller reaches every home. The SQL of the account read
// (home_branch_id = ANY(reach) OR home_branch_id IS NULL) says the same thing.
func (s StaffContext) CanReachAccountHome(homeBranchID string) bool {
	return homeBranchID == "" || s.CanAccessBranch(homeBranchID)
}

// -- Policy enforcement --------------------------------------------------

type PolicyType string

const (
	PolicyMargin PolicyType = "MARGIN_BELOW_MINIMUM"
	PolicyCredit PolicyType = "CREDIT_LIMIT_EXCEEDED"
	PolicyCOD    PolicyType = "COD_CONSTRAINT"
	// PolicyJEThreshold is the approvals-rail trigger for a manual journal entry at/above the
	// gl_policy maker-checker threshold (0075). It reuses the SHIPPED GL maker-checker: the request's
	// subject_ref is the parked DRAFT journal id, and a rail approve bridges to gl.ApproveJournal.
	PolicyJEThreshold PolicyType = "JE_OVER_THRESHOLD"
	// PolicyFreightAboveQuote is the second-truck gate (0195). It fires when a delivery's freight
	// would be raised ABOVE what the customer was quoted, which is the only part of splitting an
	// order across more trucks that is anybody's business but the yard's.
	//
	// It rides the SAME queue and the SAME authority as the two above, and its subject_ref is a
	// PARKED delivery_freight_raises row -- the JE shape exactly: the decision lives here, the state
	// the decision applies lives in the module that owns it, and approving is what applies it.
	//
	// The request carries NO money. Not in a column, not in the `data` JSONB that PolicyJEThreshold
	// uses for its debit total. A JE total is finance information shown to a finance approver; this
	// is a figure on a customer's invoice, and the approvals list is open to every authenticated
	// staff member. The figures stay behind dispatch's own visibility policy and reach a card only
	// through the one method that has asked it.
	PolicyFreightAboveQuote PolicyType = "FREIGHT_ABOVE_QUOTE"
)

// MarginFloorBps is the minimum gross margin in basis points. Q-029 assumption: 12.00%.
const MarginFloorBps int64 = 1200

// ActionBlocked is returned when a policy blocks an action; serialized as a 403 body.
type ActionBlocked struct {
	Policy   PolicyType        `json:"policy_type"`
	BranchID string            `json:"branch_id,omitempty"`
	Detail   string            `json:"detail"`
	Data     map[string]string `json:"data,omitempty"`
}

func (e *ActionBlocked) Error() string {
	return fmt.Sprintf("action_blocked: %s, %s", e.Policy, e.Detail)
}

// MarginBps computes gross margin in basis points from integer cents.
func MarginBps(priceCents, costCents int64) int64 {
	if priceCents <= 0 {
		return 0
	}
	return ((priceCents - costCents) * 10000) / priceCents
}

// CheckMargin blocks a quote whose margin is below the legacy floor (MarginFloorBps),
// unless an approved override exists. Preserved as the 4-arg entry point: it now
// delegates to CheckMarginAt with the const floor, so every existing caller and test
// behaves exactly as before (regression-pinned by TestCheckMargin_LegacyPathUnchanged).
func CheckMargin(priceCents, costCents int64, branchID string, hasApprovedOverride bool) *ActionBlocked {
	return CheckMarginAt(priceCents, costCents, branchID, hasApprovedOverride, MarginFloorBps)
}

// CheckMarginAt is the additive, parameterized margin gate (A-07 / D-A07-4). It is
// identical to CheckMargin except the floor is supplied by the caller, so the pricing
// MarginGate can pass an effective per-category floor (base plus the category adjuster from
// pricing_category_policy) instead of the single legacy const. Go has no default
// params, so this is a genuinely new function, NOT a mutated signature; identity (A-20)
// keeps ownership of the floor decision and the 0016 approval queue.
func CheckMarginAt(priceCents, costCents int64, branchID string, hasApprovedOverride bool, floorBps int64) *ActionBlocked {
	if hasApprovedOverride {
		return nil
	}
	m := MarginBps(priceCents, costCents)
	if m >= floorBps {
		return nil
	}
	return &ActionBlocked{
		Policy:   PolicyMargin,
		BranchID: branchID,
		Detail:   fmt.Sprintf("margin %d bps is below the %d bps floor", m, floorBps),
		Data:     map[string]string{"margin_bps": fmt.Sprintf("%d", m), "floor_bps": fmt.Sprintf("%d", floorBps)},
	}
}

// CheckCredit blocks an order that exceeds the credit limit, or that is on credit
// terms with no credit available (COD constraint), unless an approved override exists.
func CheckCredit(orderTotalCents, openBalanceCents, creditLimitCents int64, termsAreCredit bool, branchID string, hasApprovedOverride bool) *ActionBlocked {
	if hasApprovedOverride || !termsAreCredit {
		return nil // approved, or a cash/card sale that doesn't draw on credit
	}
	if creditLimitCents <= 0 {
		return &ActionBlocked{Policy: PolicyCOD, BranchID: branchID, Detail: "account has no credit; cash or card only (COD)"}
	}
	if openBalanceCents+orderTotalCents > creditLimitCents {
		return &ActionBlocked{
			Policy:   PolicyCredit,
			BranchID: branchID,
			Detail:   fmt.Sprintf("order would put the balance %d cents over the %d cents limit", (openBalanceCents+orderTotalCents)-creditLimitCents, creditLimitCents),
		}
	}
	return nil
}

// railApproverRoles is the commander-ruled authority set for the approvals-rail policies
// (DIB-216 decision #1): MARGIN_BELOW_MINIMUM, JE_OVER_THRESHOLD and (since 0195)
// FREIGHT_ABOVE_QUOTE are decidable ONLY by these three
// Tier-1 GLOBAL roles: General Manager, Financial Controller, and the platform admin. Note this is
// narrower than a plain IsAdmin() check, which would also admit HR (a Tier-1 GLOBAL role that is
// deliberately NOT an approval authority here). It is a role-code allowlist, not a tier test, precisely
// so HR is excluded.
//
// D11: this constant set is the CARRIED DEFAULT the rail falls back to when no
// configuration is wired, and it equals r3's ruling. The live margin authority is
// the dealer's role configuration, handed to the rail as FloorApprovers by the
// composition root (approvals.go): the floor list's dealer wide approvers plus the
// branch scoped quote floor approvers (CP0-1 answer B, the branch's Sales manager).
// TODO(approver-assignment): a downstream by-role to assigned-user config will let an
// org route a policy to specific people; until then, authority is this fixed role set.
var railApproverRoles = map[string]bool{
	"GENERAL_MANAGER":      true,
	"FINANCIAL_CONTROLLER": true,
	RoleSuperAdmin:         true,
}

// isRailApprover reports whether a role code is in the ruled rail-approver set.
func isRailApprover(roleCode string) bool { return railApproverRoles[roleCode] }

// FloorApprovers is the margin floor's decide authority as data (D11): which
// ERP role codes may decide a MARGIN_BELOW_MINIMUM request dealer wide, and
// which may decide one inside a branch they reach (CP0-1 answer B: the
// branch's Sales manager). Built by the composition root from the dealer's
// role configuration, never from a code constant; the zero value carries no
// authority, so an unwired set falls back to the carried default set above,
// which equals r3's ruling.
type FloorApprovers struct {
	DealerWide []string
	OwnBranch  []string
}

// defaultFloorApprovers is the carried default: the rail's ruled Tier-1 set,
// no branch scoped arm (r3 as ruled before CP0-1 B widened it).
func defaultFloorApprovers() FloorApprovers {
	return FloorApprovers{
		DealerWide: []string{"GENERAL_MANAGER", "FINANCIAL_CONTROLLER", RoleSuperAdmin},
	}
}

// mayDecideDealerWide reports whether the role is on the dealer wide list.
func (f FloorApprovers) mayDecideDealerWide(roleCode string) bool {
	for _, r := range f.DealerWide {
		if r == roleCode {
			return true
		}
	}
	return false
}

// floorBranchFilerRoles are the filer roles whose below floor requests the
// branch scoped arm may answer (decision log row 743, plan 3.3 and CP0-1 B):
// the branch Sales manager decides what Inside Sales and Outside Sales file in
// its own branch, and nothing else. A request filed by a Branch manager, a
// Sales manager (a peer's included) or the Yard manager, or by any role this
// vocabulary does not name, goes to the dealer wide approvers alone, whether
// manager_own_branch_floor_clearing is on or off (that setting only decides
// whether a manager may clear its own price itself). A role code allowlist,
// so an empty or unknown filer role fails closed to leadership.
var floorBranchFilerRoles = map[string]bool{
	"INSIDE_SALES":  true,
	"OUTSIDE_SALES": true,
}

// FloorBranchFiler reports whether a request filed by this ERP role code may
// be answered by the branch scoped arm. It is the ONE definition of the filer
// allowlist: the decide rule (MayDecideInBranchFor) and the approvers the
// quote refusal names both read it, so the named set cannot drift from the
// deciding set.
func FloorBranchFiler(requesterRole string) bool { return floorBranchFilerRoles[requesterRole] }

// MayDecideInBranch reports whether the role is on the branch scoped list.
// The branch REACH itself is the caller's to prove (CanApprove checks it
// before the arm is consulted), a listed role with no grant over the
// request's branch decides nothing. The filer's role is the caller's to
// prove as well: MayDecideInBranchFor.
func (f FloorApprovers) MayDecideInBranch(roleCode string) bool {
	for _, r := range f.OwnBranch {
		if r == roleCode {
			return true
		}
	}
	return false
}

// MayDecideInBranchFor is MayDecideInBranch for one request: the role is on
// the branch scoped list AND the request was filed by a salesperson role
// (floorBranchFilerRoles). The branch scoped arm decides nothing a leader or
// a peer manager filed.
func (f FloorApprovers) MayDecideInBranchFor(roleCode, requesterRole string) bool {
	return f.MayDecideInBranch(roleCode) && FloorBranchFiler(requesterRole)
}

// sellingRoles is the authority set for COMMITTING AN ORDER AGAINST A CUSTOMER'S CREDIT from a staff
// surface: today, the DIB-483 counter door onto quote to order conversion (quote/convert_staff.go).
//
// It is a role-code allowlist rather than a tier test, and the exclusions are the point. A tier test
// would admit the whole frontline tier (YARD_TEAM, DRIVERS) and the whole of leadership (HR); none of
// those four people sell. Conversely INSIDE_SALES and OUTSIDE_SALES are Tier-3 frontline roles who
// absolutely do sell: a manager-tier gate would lock out the two roles that stand at the counter all
// day, which is how a "safe" permission model gets worked around with a shared manager login.
//
// Note the asymmetry with quote EDITING, which is open to any active in-branch staff member
// (quote/edit_handler.go). That is deliberate and stated there: a quote moves no money until Convert.
// Convert is the money, so Convert is where the narrower gate lives.
var sellingRoles = map[string]bool{
	"INSIDE_SALES":         true,
	"OUTSIDE_SALES":        true,
	"SALES_MANAGER":        true,
	"BRANCH_MANAGER":       true,
	"GENERAL_MANAGER":      true,
	"FINANCIAL_CONTROLLER": true,
	RoleSuperAdmin:         true,
}

// CanSellOnCredit reports whether this caller may commit an order against a customer's credit.
//
// Status is checked here as well as at the call site on purpose: an INACTIVE user holding a selling
// role is not a seller, and a predicate named "can" that ignores whether the account is live is a trap
// for the next caller who reaches for it.
func CanSellOnCredit(c StaffContext) bool {
	if c.Status != StatusActive {
		return false
	}
	return sellingRoles[c.RoleCode]
}

// CanConvertAndPay reports whether this caller may take the order payment that converts an accepted
// cash quote in the same action: a selling role, alone. It is the ONE predicate behind that rule.
// The order-payment door calls it, and the payment grant read folds it into its answer, so the flag
// a client reads can never say yes where the door answers not-selling-role. Status is checked
// inside, as CanSellOnCredit does. The platform roles pass every gate level and are in neither
// set, so they are refused here, and the grant must say so. The yard manager sat here once (the
// admission row 370's cells carried); decision log row 1156 removed its Counter Till and Quote
// Desk, and this predicate names no role for a payment the matrix refuses it.
func CanConvertAndPay(c StaffContext) bool {
	return CanSellOnCredit(c)
}

// SellingRoles returns the ruled selling-role codes, sorted, for surfaces that need to EXPLAIN the
// gate (an admin screen, a test table) rather than merely enforce it. Returning a copy keeps the
// authority set unmodifiable from outside this package.
func SellingRoles() []string {
	out := make([]string, 0, len(sellingRoles))
	for code := range sellingRoles {
		out = append(out, code)
	}
	sort.Strings(out)
	return out
}

// dispatchCommittingRoles is the authority set for COMMITTING A DELIVERY DATE: putting a load on a
// truck and standing behind the date the customer was given.
//
// It is a role-code allowlist and the EXCLUSIONS are the point. The dispatch ruling on the
// record: salespeople have no business committing, because dispatchers do not want them
// overpromising or overcommitting to something they know nothing about, and dispatch owns
// that date. So INSIDE_SALES, OUTSIDE_SALES and SALES_MANAGER are all out: a
// sales manager is one of those people, and a tier test would have let them through.
//
// YARD_TEAM is IN, and that is not an oversight: the person building tomorrow's truck at 06:45 is
// frontline, and a manager-tier gate would stop the work this whole surface exists to do. DRIVERS is
// out: a driver executes a committed load, they do not decide what is on it.
//
// Note the asymmetry with REQUESTING a date, which is open to any active in-branch staff member: an
// ask is not a promise, and the whole point of the two states is that asking must be easy.
var dispatchCommittingRoles = map[string]bool{
	"LOGISTICS_MANAGER": true,
	"YARD_MANAGER":      true,
	"YARD_TEAM":         true,
	"BRANCH_MANAGER":    true,
	"GENERAL_MANAGER":   true,
	RoleSuperAdmin:      true,
}

// CanCommitDelivery reports whether this caller may turn a requested delivery date into a committed
// one. Status is checked here as well as at the call site for the CanSellOnCredit reason: a predicate
// named "can" that ignores whether the account is live is a trap for the next caller.
func CanCommitDelivery(c StaffContext) bool {
	if c.Status != StatusActive {
		return false
	}
	return dispatchCommittingRoles[c.RoleCode]
}

// DispatchCommittingRoles returns the ruled codes, sorted, for a surface that needs to EXPLAIN the
// gate rather than merely enforce it. A copy, so the authority set is unmodifiable from outside.
func DispatchCommittingRoles() []string {
	out := make([]string, 0, len(dispatchCommittingRoles))
	for code := range dispatchCommittingRoles {
		out = append(out, code)
	}
	sort.Strings(out)
	return out
}

// CanApprove encodes the decide-authority model:
//   - MARGIN + JE_OVER_THRESHOLD + FREIGHT_ABOVE_QUOTE (the approvals rail): the three ruled Tier-1
//     GLOBAL roles only (DIB-216 decision #1, GM / Financial Controller / platform admin; NOT HR,
//     NOT branch managers). The margin arm consults the carried default set; a caller holding the
//     dealer's configured set passes it through CanApproveFloor (D11, CP0-1 answer B).
//   - CREDIT/COD override: Tier-1 leadership only (a finance/GM decision), unchanged.
//
// The branch-reachability guard still applies (the ruled roles are all GLOBAL, so it is always satisfied
// for them, but a non-reaching caller is rejected up front regardless of role).
//
// THE DEFAULT ARM IS THE SAFETY PROPERTY OF THIS FUNCTION AND IT MUST STAY FALSE. A policy value that
// the CHECK constraint admits but this switch does not name is UNDECIDABLE BY EVERYONE: the request
// sits in the queue and nothing happens to it. That is the correct failure: a widened enum with a
// missing arm costs a stuck approval, whereas a permissive default would let an unnamed kind of money
// be waved through by whoever happened to be looking. 0195 added FREIGHT_ABOVE_QUOTE to the CHECK and
// to the arm below in the same change, for exactly this reason.
func CanApprove(approver StaffContext, policy PolicyType, requestBranchID string) bool {
	return CanApproveFloor(approver, policy, requestBranchID, "", defaultFloorApprovers())
}

// CanApproveFloor is CanApprove with the margin arm's authority supplied
// from the dealer's role configuration (D11): a dealer wide approver decides
// anywhere it reaches; a branch scoped approver (CP0-1 answer B) decides
// only inside a branch it reaches, and only a request a salesperson role
// filed (requesterRole, row 743). Four eyes is NOT checked here, it is the
// Decide path's own rule and holds whichever set answers.
func CanApproveFloor(approver StaffContext, policy PolicyType, requestBranchID, requesterRole string, floor FloorApprovers) bool {
	if !approver.CanAccessBranch(requestBranchID) {
		return false
	}
	switch policy {
	case PolicyMargin:
		if floor.mayDecideDealerWide(approver.RoleCode) {
			return true
		}
		return floor.MayDecideInBranchFor(approver.RoleCode, requesterRole)
	case PolicyJEThreshold, PolicyFreightAboveQuote:
		return isRailApprover(approver.RoleCode)
	case PolicyCredit, PolicyCOD:
		return approver.Tier == TierLeadership
	default:
		return false
	}
}

// ResolveApprovers filters a candidate set of staff contexts down to the user ids that CanApprove the
// given policy in the given branch: the fan-out target resolution the approvals rail uses to decide
// whose per-person channel gets a card. It is a pure function over CanApprove (single source of authority
// truth), so the target set is unit-tested against the same matrix decide enforces. An empty result is the
// zero-eligible-approver edge (the caller logs it; the request stays decidable and re-fans on the sweep).
func ResolveApprovers(candidates []StaffContext, policy PolicyType, branchID string) []string {
	return ResolveApproversFloor(candidates, policy, branchID, "", defaultFloorApprovers())
}

// ResolveApproversFloor is ResolveApprovers over a configured FloorApprovers
// set (D11): the fan-out resolves the same authority the decide path holds,
// the requester's role included (row 743).
func ResolveApproversFloor(candidates []StaffContext, policy PolicyType, branchID, requesterRole string, floor FloorApprovers) []string {
	var out []string
	for _, c := range candidates {
		if c.UserID == "" {
			continue
		}
		if CanApproveFloor(c, policy, branchID, requesterRole, floor) {
			out = append(out, c.UserID)
		}
	}
	return out
}

// -- Approval queue domain -------------------------------------------------

type ApprovalStatus string

const (
	ApprovalPending  ApprovalStatus = "PENDING"
	ApprovalApproved ApprovalStatus = "APPROVED"
	ApprovalRejected ApprovalStatus = "REJECTED"
)

type ApprovalRequest struct {
	ID          string     `json:"id"`
	RequestedBy string     `json:"requested_by"`
	BranchID    string     `json:"branch_id"`
	Policy      PolicyType `json:"policy_type"`
	Detail      string     `json:"detail"`
	SubjectRef  string     `json:"subject_ref,omitempty"` // optional binding to a quote/order/journal (H-2)
	// AmountCents is the money magnitude an approvals-rail card shows (a JE debit total; 0/omitted for a
	// margin override). It has NO column in permission_approval_requests: it is persisted in the existing
	// `data` JSONB (data->>'amount_cents') and read back for the card + the reconcile re-fan (0075).
	AmountCents int64 `json:"amount_cents,omitempty"`
	// LineNo and DiscPct bind a quote floor request to exactly one line of
	// the subject quote at exactly the discount that was asked (D3, D7).
	// They ride the `data` JSONB like AmountCents; an approval covers one
	// line at no deeper a discount than was asked, and the first write that
	// uses it uses it up.
	LineNo  int `json:"line_no,omitempty"`
	DiscPct int `json:"disc_pct,omitempty"`
	// LineID and ProductID bind a quote floor request to the line's OWN row
	// (quote_lines.id) and its product. A line number is not an identity: a
	// new line takes MAX(line_no)+1, so deleting a line frees its number
	// for the next line added, and an approval that matched on the number
	// alone could clear a line that did not exist when it was filed. They
	// ride the `data` JSONB like the figures above.
	LineID    string `json:"line_id,omitempty"`
	ProductID string `json:"product_id,omitempty"`
	// Note is the requester's bounded free text on a quote floor request.
	// It rides `data`; it is never the detail the approver reads as the
	// server's own figures (D7).
	Note      string         `json:"note,omitempty"`
	Status    ApprovalStatus `json:"status"`
	DecidedBy string         `json:"decided_by,omitempty"`
	// RequesterRole is the ERP role code of the user who filed the request,
	// read by the store from the requester's role grant (never from the
	// request body). It decides which floor arm may answer a margin request:
	// the branch Sales manager answers a salesperson's alone. It is empty
	// when the requester holds no single ERP named role, and an empty or
	// unknown filer role reaches leadership only. It stays off the wire.
	RequesterRole string `json:"-"`
	// RequestedAt is the filing time the list is ordered by (newest first, id as the tie break) and the
	// page cursor is positioned on. It is read by the list only and stays off the wire.
	RequestedAt time.Time `json:"-"`
}

// Decide applies an approver's decision, enforcing the authority model, the
// pending-state, and separation of duties (no self-approval), over the
// carried default margin authority set. A caller holding the dealer's
// configured set passes it through DecideWithFloor (D11).
func (r *ApprovalRequest) Decide(approver StaffContext, approve bool) error {
	return r.DecideWithFloor(approver, approve, defaultFloorApprovers())
}

// DecideWithFloor is Decide with the margin arm's authority supplied from
// the dealer's role configuration. Four eyes holds whichever set answers:
// the requester never decides its own request, even when it holds an
// approver role. The branch scoped arm answers LINE-BOUND floor requests
// alone: a margin request with no line binding is decidable by the dealer
// wide set alone (P2-C).
func (r *ApprovalRequest) DecideWithFloor(approver StaffContext, approve bool, floor FloorApprovers) error {
	if r.Status != ApprovalPending {
		return clientErr(http.StatusConflict, "request already %s", r.Status)
	}
	// Four-eyes: the requester may never decide their own override, regardless of tier.
	if approver.UserID != "" && approver.UserID == r.RequestedBy {
		return clientErr(http.StatusForbidden, "separation of duties: you cannot approve your own request")
	}
	if !CanApproveFloor(approver, r.Policy, r.BranchID, r.RequesterRole, floor) {
		return clientErr(http.StatusForbidden, "approver %s (tier %d) not authorized for %s in branch %s", approver.RoleCode, approver.Tier, r.Policy, r.BranchID)
	}
	// The branch scoped floor arm decides LINE-BOUND requests alone (P2-C,
	// grant rows 739 and 740): a margin card with no line binding is a
	// legacy filing from before the quote desk's door, a subject and detail
	// of the filer's choosing, and it stays decidable by the dealer wide
	// floor set alone, never by the branch Sales manager the widening
	// reached. GetApproval and the sync projection load the binding for
	// exactly this check.
	if r.Policy == PolicyMargin && r.LineID == "" && !floor.mayDecideDealerWide(approver.RoleCode) {
		return clientErr(http.StatusForbidden,
			"approver %s holds only the branch scoped floor authority, which decides the quote desk's line-bound floor requests; this margin request carries no line binding", approver.RoleCode)
	}
	if approve {
		r.Status = ApprovalApproved
	} else {
		r.Status = ApprovalRejected
	}
	r.DecidedBy = approver.UserID
	return nil
}

// StaffMember is a user plus their assigned role + branch grants: the admin list view.
type StaffMember struct {
	UserID      string   `json:"user_id"`
	Email       string   `json:"email"`
	DisplayName string   `json:"display_name"`
	RoleCode    string   `json:"role_code"` // "" when unassigned
	RoleName    string   `json:"role_name"`
	Tier        int      `json:"tier"`
	Scope       string   `json:"scope"`
	Branches    []string `json:"branch_ids"`
}
