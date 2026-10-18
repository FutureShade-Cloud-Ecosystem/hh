package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The canonical contractor role set (D3): the ERP portal's CHECK set on
// portal_users.role, widened by 0160. HH Pro's set maps onto it once by
// hhpro_contractor_role_map; nothing else grants a role.
const (
	PortalRoleAccountAdmin = "account_admin"
	PortalRoleBuyer        = "buyer"
	PortalRoleBuyerNoPay   = "buyer_no_pay"
	PortalRoleAPFinance    = "ap_finance"
	PortalRoleFieldCrew    = "field_crew"
)

// PortalStatusActive is the one portal_users.status value that may hold
// authority (the carried CHECK set is active, inactive, pending, migration
// 0019).
const PortalStatusActive = "active"

// ErrPortalUserInactive names a resolved portal user whose status is not
// active. The ERP's authenticate re-loads the user on every request and
// refuses a non-active one with the credential error (portal/service.go at
// the pinned commit, H-4: the caller's current role and status decide,
// never the possibly stale claims), and the contract pins the same plain
// 401 for a deactivated portal user (core/api/openapi.yaml, the refresh
// route's refusal description). The guard maps this sentinel to the 401
// family: an account-lifetime fact, not a gate decision.
var ErrPortalUserInactive = errors.New("identity: portal user is not active")

// Capability names the guardable capabilities: the eight the ERP wraps in
// route guards at the pinned commit (portal/handler.go admin, billing,
// payInvoices, managePaymentMethods, submitRFQ, createOrders,
// viewOrdersDeliveries, editDeliveryInstructions). The string values match
// the contract's x-role capability:* vocabulary. ManageFiles is model
// surface only: it has no enforcing route at the pinned commit
// (Integrations B1 finding 2), so it has no guard constant here and no
// guard may be invented for it without a contract change first.
type Capability string

const (
	CapManageUsers              Capability = "manage_users"
	CapViewBilling              Capability = "view_billing"
	CapPayInvoices              Capability = "pay_invoices"
	CapManagePaymentMethods     Capability = "manage_payment_methods"
	CapSubmitRFQ                Capability = "submit_rfq"
	CapCreateOrders             Capability = "create_orders"
	CapViewOrdersDeliveries     Capability = "view_orders_deliveries"
	CapEditDeliveryInstructions Capability = "edit_delivery_instructions"
)

// Capabilities is the capability model carried verbatim from the ERP
// portal rbac.go at the pinned commit, so the role to capability mapping
// cannot drift between the products. The struct keeps all nine model
// fields including ManageFiles (reserved surface, unguarded); Has refuses
// everything that is not one of the eight guardable names.
type Capabilities struct {
	ManageUsers              bool
	ViewBilling              bool
	PayInvoices              bool
	ManagePaymentMethods     bool
	SubmitRFQ                bool
	CreateOrders             bool
	ViewOrdersDeliveries     bool
	EditDeliveryInstructions bool
	ManageFiles              bool
}

// Has reports whether the set carries one guardable capability. The
// reserved field is deliberately unreachable through this method.
func (c Capabilities) Has(cap Capability) bool {
	switch cap {
	case CapManageUsers:
		return c.ManageUsers
	case CapViewBilling:
		return c.ViewBilling
	case CapPayInvoices:
		return c.PayInvoices
	case CapManagePaymentMethods:
		return c.ManagePaymentMethods
	case CapSubmitRFQ:
		return c.SubmitRFQ
	case CapCreateOrders:
		return c.CreateOrders
	case CapViewOrdersDeliveries:
		return c.ViewOrdersDeliveries
	case CapEditDeliveryInstructions:
		return c.EditDeliveryInstructions
	default:
		return false
	}
}

// CapabilitiesFor maps a canonical contractor role to its capabilities,
// carried verbatim from the ERP (portal/rbac.go at the pinned commit,
// every arm and its exact boolean set). An unknown role grants nothing
// (C31: no fail-open default).
func CapabilitiesFor(role string) Capabilities {
	switch role {
	case PortalRoleAccountAdmin:
		return Capabilities{
			ManageUsers:              true,
			ViewBilling:              true,
			PayInvoices:              true,
			ManagePaymentMethods:     true,
			SubmitRFQ:                true,
			CreateOrders:             true,
			ViewOrdersDeliveries:     true,
			EditDeliveryInstructions: true,
			ManageFiles:              true,
		}
	case PortalRoleBuyer:
		return Capabilities{
			ViewBilling:              true,
			PayInvoices:              true,
			ManagePaymentMethods:     true,
			SubmitRFQ:                true,
			CreateOrders:             true,
			ViewOrdersDeliveries:     true,
			EditDeliveryInstructions: true,
			ManageFiles:              true,
		}
	case PortalRoleBuyerNoPay:
		return Capabilities{
			ViewBilling:              true,
			SubmitRFQ:                true,
			CreateOrders:             true,
			ViewOrdersDeliveries:     true,
			EditDeliveryInstructions: true,
			ManageFiles:              true,
		}
	case PortalRoleAPFinance:
		return Capabilities{
			ViewBilling:          true,
			PayInvoices:          true,
			ManagePaymentMethods: true,
			ViewOrdersDeliveries: true,
			ManageFiles:          true,
		}
	case PortalRoleFieldCrew:
		return Capabilities{
			ViewOrdersDeliveries:     true,
			EditDeliveryInstructions: true,
		}
	default:
		return Capabilities{}
	}
}

// PortalRoleResolver resolves the canonical contractor role from
// portal_users through a pgx pool, per request with no caching (D28
// family).
type PortalRoleResolver struct {
	pool *pgxpool.Pool
}

// NewPortalRoleResolver validates the collaborator and returns the
// resolver.
func NewPortalRoleResolver(pool *pgxpool.Pool) (*PortalRoleResolver, error) {
	if pool == nil {
		return nil, errors.New("identity: portal role resolver requires a pool")
	}
	return &PortalRoleResolver{pool: pool}, nil
}

// PortalRole resolves the contractor role of the platform user within the
// token's tenant, keyed by platform_user_id (migration 0215), together
// with the row's status: a resolved row that is not active is
// ErrPortalUserInactive (the 401 family upstream), never a role. The
// tenant predicate is the scope predicate the module fence requires on a
// scoped table: the token's contractor account is the only account whose
// role may ever be read for this caller. ok false with a nil error is no
// linked portal user in this tenant, which is no authority and answers 403
// upstream; carried rows whose additive tenant_id is still NULL do not
// resolve until population fills it (fail closed).
func (r *PortalRoleResolver) PortalRole(ctx context.Context, platformUserID, tenantID string) (string, bool, error) {
	if platformUserID == "" || tenantID == "" {
		return "", false, errors.New("identity: portal role resolution requires the platform user id and the tenant id")
	}
	var role, status string
	err := r.pool.QueryRow(ctx, `
		SELECT role, status FROM portal_users
		WHERE platform_user_id = $1
		  AND tenant_id = $2
		  AND deleted_at IS NULL
		LIMIT 1`, platformUserID, tenantID).Scan(&role, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("identity: portal role read failed: %w", err)
	}
	if status != PortalStatusActive {
		return "", false, fmt.Errorf("%w: portal_users row status %q", ErrPortalUserInactive, status)
	}
	return role, true, nil
}

// PortalLink is the live contractor row a platform user is linked to: the
// row id (the audit row's actor), the contractor account the `tenant` claim
// carries, the row's email (the exchange's account match) and its role.
type PortalLink struct {
	PortalUserID string
	TenantID     string
	Email        string
	Role         string
}

// LinkedPortalUser is the tenant exchange's linkage check (the staff gate
// 6 counterpart): the live portal_users row for the platform user, read by
// platform_user_id alone, because the exchange is where the tenant claim is
// decided and so has no claim to predicate on yet. A row that is not active
// is ErrPortalUserInactive; no row, a tombstoned row and a row whose
// tenant_id is not yet stamped all answer ok false with a nil error (the
// same fail closed reading PortalRole gives an unstamped row).
func (r *PortalRoleResolver) LinkedPortalUser(ctx context.Context, platformUserID string) (PortalLink, bool, error) {
	if platformUserID == "" {
		return PortalLink{}, false, errors.New("identity: portal linkage check requires the platform user id")
	}
	var l PortalLink
	var status string
	err := r.pool.QueryRow(ctx, `
		SELECT id::text, tenant_id::text, email::text, role, status FROM portal_users
		WHERE platform_user_id = $1
		  AND tenant_id IS NOT NULL
		  AND deleted_at IS NULL
		LIMIT 1`, platformUserID).Scan(&l.PortalUserID, &l.TenantID, &l.Email, &l.Role, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return PortalLink{}, false, nil
	}
	if err != nil {
		return PortalLink{}, false, fmt.Errorf("identity: portal linkage read failed: %w", err)
	}
	if status != PortalStatusActive {
		return PortalLink{}, false, fmt.Errorf("%w: portal_users row status %q", ErrPortalUserInactive, status)
	}
	return l, true, nil
}
