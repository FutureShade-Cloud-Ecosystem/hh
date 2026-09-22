// Package portal owns contractor portal auth: per-person logins, three roles, and
// the invite/accept flow (CP-07, W2). It serves the contractor portal (Lit, app/src/portal/)
// (dibbits/portal/app) at /api/portal/v1/*. Module boundary: portal owns
// portal_users + portal_invites; it writes the crm.NewPortalContact signal to the
// shared events outbox (the sanctioned cross-module write seam).
package portal

// Role is a contractor portal role. These strings are also the DB CHECK set
// (portal_users.role + portal_invites.role, migration 0019, widened by 0160).
type Role string

const (
	RoleAccountAdmin Role = "account_admin" // manages the company's users + sees billing
	RoleBuyer        Role = "buyer"         // transacts, sees billing, no team management
	RoleFieldCrew    Role = "field_crew"    // orders/deliveries only, no financials, no team

	// -- DIB-479 / OR-2: the two SPLIT-BUYER personas --------------------------------------------
	//
	// `buyer` holds BOTH PayInvoices and CreateOrders, and a six-person contractor outfit does not
	// work that way: a project manager builds the order and the bookkeeper pays the bill. Until
	// these two roles existed, the ERP could not express that division and the HH Pro portal would
	// have had to render a role picker the server cannot enforce, the "permission gate silently
	// switched off" bug class (spec §1.3, risk R-4).
	//
	// They are DELTAS OF `buyer`, one subtracting the pay pair and the other the buy trio, which is
	// exactly the additive split this file's header promised: "an AP/Finance user who pays but
	// can't order, a Buyer who orders but can't pay".
	RoleBuyerNoPay Role = "buyer_no_pay" // HH Pro `pm`: orders + RFQ + deliveries, CANNOT pay
	RoleAPFinance  Role = "ap_finance"   // HH Pro `ap`: pays + payment methods, CANNOT order
)

// Capabilities is the single authority every portal endpoint gates on, never the
// raw role string (US-CP07-2 keystone). The coarse three-flag model (billing/transact/
// admin) is split into feature-aligned capabilities so the named personas the portal
// must express, an AP/Finance user who pays but can't order, a Buyer who orders but
// can't pay, a Viewer, become representable additively, without revisiting every
// guard after invoice-pay and procurement ship (plan §4.6).
type Capabilities struct {
	ManageUsers              bool `json:"manage_users"`               // add/remove the company's portal users
	ViewBilling              bool `json:"view_billing"`               // dashboard, invoices, AR balance
	PayInvoices              bool `json:"pay_invoices"`               // move money against open invoices
	ManagePaymentMethods     bool `json:"manage_payment_methods"`     // add/remove stored (vaulted) methods
	SubmitRFQ                bool `json:"submit_rfq"`                 // turn a planning list into a priced quote
	CreateOrders             bool `json:"create_orders"`              // convert/commit an order against credit
	ViewOrdersDeliveries     bool `json:"view_orders_deliveries"`     // see orders + delivery status
	EditDeliveryInstructions bool `json:"edit_delivery_instructions"` // annotate a scheduled delivery
	ManageFiles              bool `json:"manage_files"`               // upload/share account documents
}

// CapabilitiesFor maps a role to its capabilities. An unknown role grants nothing. The
// DB role values (account_admin/buyer/field_crew) are unchanged, "Owner" is a display
// label for account_admin, so adding personas later needs no data migration (§4.6).
func CapabilitiesFor(role string) Capabilities {
	switch Role(role) {
	case RoleAccountAdmin:
		// Owner: full control of the account, its crew, money, procurement, and files.
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
	case RoleBuyer:
		// Transacts and sees billing across the whole order-to-cash chain, but cannot
		// manage the company's user roster (that stays an Owner power).
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
	case RoleBuyerNoPay:
		// HH Pro's `pm` (DIB-479 / OR-2): Buyer MINUS the pay pair. Builds the plan, submits the
		// RFQ, commits the order against the account's credit, and annotates the delivery, but
		// cannot move money and cannot touch a stored payment method. ViewBilling stays ON: a
		// person who commits against a credit limit has to be able to see the headroom, or every
		// order becomes a phone call.
		return Capabilities{
			ViewBilling:              true,
			SubmitRFQ:                true,
			CreateOrders:             true,
			ViewOrdersDeliveries:     true,
			EditDeliveryInstructions: true,
			ManageFiles:              true,
		}
	case RoleAPFinance:
		// HH Pro's `ap` (DIB-479 / OR-2): Buyer MINUS the buy trio. Sees the AR, pays invoices, and
		// manages stored methods, but cannot start an RFQ, cannot commit an order, and cannot
		// change a delivery. ViewOrdersDeliveries stays ON because reconciling an invoice against
		// what actually arrived IS the A/P job; it is a read and commits nothing.
		return Capabilities{
			ViewBilling:          true,
			PayInvoices:          true,
			ManagePaymentMethods: true,
			ViewOrdersDeliveries: true,
			ManageFiles:          true,
		}
	case RoleFieldCrew:
		// Field crew: orders and deliveries only, no financials, no team, no commits.
		return Capabilities{
			ViewOrdersDeliveries:     true,
			EditDeliveryInstructions: true,
		}
	default:
		return Capabilities{}
	}
}

// validRole reports whether a string is one of the canonical roles. It is the single gate the
// invite and role-change paths run every caller-supplied role string through, so widening the
// role set here (and in the DB CHECK, 0160) is all it takes for invites/role updates to accept
// a new persona, no per-route change, nothing to forget.
func validRole(role string) bool {
	switch Role(role) {
	case RoleAccountAdmin, RoleBuyer, RoleBuyerNoPay, RoleAPFinance, RoleFieldCrew:
		return true
	default:
		return false
	}
}
