package sync

import (
	"context"
	"slices"
)

// The write fences (plan B.2, FP 51, row 1259): the registry carries, per
// scope, the roles that may write it and the operations they may push, and
// the handler refuses everything else with the shared RBAC-403 before any
// store call. Deny by default: a scope with no writer entry admits no
// client write, and the registry's own test refuses a scope that is neither
// pull-only, money, nor carries an entry, so a new scope cannot be added
// without a decision.

// StaffRoles resolves every role a staff caller holds in the org, the whole
// row the fence walks (D48 X10: a person holding two roles gets both). The
// concrete reader is identity.StaffAuthorityResolver.ResolveRoles; the
// narrow seam lets tests inject doubles. An empty list with a nil error is
// no active grant.
type StaffRoles interface {
	ResolveRoles(ctx context.Context, platformUserID, orgID string) ([]string, error)
}

// PortalRoles resolves the canonical contractor role of a tenant caller,
// the same shape as the guards' PortalRoleSource (the concrete reader is
// identity.PortalRoleResolver over portal_users). has false is no portal
// row.
type PortalRoles interface {
	PortalRole(ctx context.Context, platformUserID, tenantID string) (role string, has bool, err error)
}

// The staff role ids (staff_role rows, migration 0215) the writer tables
// name. A role id outside the staff_role table fails the registry's test.
const (
	roleGeneralManager      = "general_manager"
	roleFinancialController = "financial_controller"
	roleHR                  = "hr"
	roleBranchManager       = "branch_manager"
	roleSalesManager        = "sales_manager"
	roleYardManager         = "yard_manager"
	roleLogisticsManager    = "logistics_manager"
	roleProcurementManager  = "procurement_manager"
	rolePayablesReceivables = "payables_receivables"
	roleInsideSales         = "inside_sales"
	roleOutsideSales        = "outside_sales"
	roleYardTeam            = "yard_team"
	roleDriver              = "driver"
	roleSuperAdmin          = "super_admin"
)

// The canonical contractor role ids (identity.PortalRole*), spelled here so
// the registry stays a plain table.
const (
	roleAccountAdmin = "account_admin"
	roleBuyer        = "buyer"
	roleFieldCrew    = "field_crew"
)

// The writer groups, one per declaring micro app, from the role catalog's
// outbox declarations (docs/analysis/role-catalog.yaml) cut to the roles
// the decided matrix (internal/roleconfig/default.json) holds at work or
// above on that app's surface.
var (
	writersYardPick      = []string{roleYardTeam, roleYardManager}
	writersDriveRun      = []string{roleDriver}
	writersAttachments   = []string{roleYardTeam, roleYardManager, roleDriver}
	writersDispatchBoard = []string{roleYardManager, roleLogisticsManager, roleBranchManager}
	writersBuyingStock   = []string{roleYardManager, roleProcurementManager}
	writersQuoteDesk     = []string{roleInsideSales, roleOutsideSales, roleSalesManager, roleBranchManager}
	// writersApprovals is the ten roles the matrix holds at work or above on
	// counter-till, quote-desk or manager-home, the surfaces a filing needs
	// (the approvals store's approvalFileSurfaces and REST identityCreateApproval
	// agree); a decision needs manage on a subset. The catalog declares no
	// outbox for the scope, so the REST twin's list is the source.
	writersApprovals = []string{
		roleBranchManager, roleFinancialController, roleGeneralManager, roleInsideSales,
		roleLogisticsManager, roleOutsideSales, rolePayablesReceivables,
		roleProcurementManager, roleSalesManager, roleYardManager,
	}
	// writersAllStaff is the thirteen staff roles (the matrix holds each at
	// work or above on house-messages) plus the catalog's super_admin. It
	// fences messages, acks and notify_prefs, and approvals too: the sync
	// door census (cmd/core qd1g, decision row 745) rules the requester's own
	// edit and withdrawal of an approval request whatever the role, and the
	// approvals store holds the finer rules per role and per document, so
	// the fence admits the whole row there and the store decides.
	writersAllStaff = []string{
		roleInsideSales, roleOutsideSales, roleYardTeam, roleDriver,
		roleYardManager, roleLogisticsManager, roleBranchManager, roleSalesManager,
		roleProcurementManager, rolePayablesReceivables, roleGeneralManager,
		roleFinancialController, roleHR, roleSuperAdmin,
	}

	// The contractor groups: the catalog's pro-procurement and pro-field
	// role lists cut to the capability the portal's own route guards demand
	// for the same write (identity.CapabilitiesFor).
	writersPortalBuy     = []string{roleAccountAdmin, roleBuyer}
	writersPortalAdmin   = []string{roleAccountAdmin}
	writersPortalField   = []string{roleAccountAdmin, roleBuyer, roleFieldCrew}
	writersPortalTracked = []string{roleAccountAdmin, roleBuyer, roleFieldCrew}
)

// The operation sets a scope's writers may push, from the op vocabulary the
// engine's validOp accepts.
var (
	opsCreateUpdate       = []string{opCreate, opUpdate}
	opsCreateUpdateDelete = []string{opCreate, opUpdate, opDelete}
	opsCreateOnly         = []string{opCreate}
)

// AllowsRole reports whether the role is on the scope's writer list. A
// scope with no writers (pull-only, money, or unfenced) admits no role.
func (i ScopeInfo) AllowsRole(role string) bool {
	return slices.Contains(i.Writers, role)
}

// AllowsOp reports whether the operation kind is on the scope's allowed
// list.
func (i ScopeInfo) AllowsOp(op string) bool {
	return slices.Contains(i.Ops, op)
}
