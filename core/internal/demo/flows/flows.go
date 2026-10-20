//go:build demo

// Package flows holds the demo's closed flow table: the role logins the
// tap-a-role entry page offers, and the one org they belong to. The table
// is the seam three lanes share, so the ids, the emails and the labels of
// the demo accounts are written exactly once:
//
//   - the demo module (its own lane) mints a session for the account a
//     flow names, and takes the mint response's label from here, never
//     from the roles table;
//   - the demo populate (its own lane) creates the users rows from here,
//     at the reserved domain, with these platform user ids and roles;
//   - the demo membership source answers membership for these accounts
//     and no others, and the demo database guard holds every ACTIVE user
//     to the reserved domain.
//
// The table also holds Summit Ridge Builders' five contractor personas (the
// Pro apps' sign in), told apart by Account.Class.
//
// The table is closed: the Counter Till's pair (D49, DECISIONS.md), the
// Manager Home app's yard manager, and the live role logins the roles
// roadmap adds one reviewed row at a time (the yard personas, Payables and
// Receivables and the Procurement Manager and the Financial Controller
// after PRs 300 and 303, and the Logistics Manager of the dispatch board, then
// the coming roles cycle's four: General Manager, HR, Branch Manager and
// Outside Sales). A new flow is an edit here, reviewed, never a
// runtime input: the entry page may only ever offer what this file lists.
package flows

// OrgSlug is the one org a demo box holds and the only org the demo
// membership source answers membership in.
const OrgSlug = "hh-demo"

// ReservedDomain is the email domain every demo account carries. It ends
// in the reserved .invalid top level domain, so no real address can sit
// inside it and no mail can be delivered to it. The database guard
// refuses a demo box whose ACTIVE users reach outside this domain and
// the two reserved example top level domains.
const ReservedDomain = "hh-demo.invalid"

// DemoMerchantID is the merchant id of the demo's invented payment
// configuration. The demo populate writes its one active configuration
// with this id, and the database guard refuses a demo box whose active
// payment configuration names any other, or whose row carries a host.
// The values here agree with the populate's pushed rows by copy today;
// the by-construction claim lands when the populate imports this table,
// a named stitch for the demo image's lane.
const DemoMerchantID = "DEMO-MERCHANT-0001"

// PopulateMarker is the marker the demo populate writes into its demo_box
// row, and the value the database guard requires there: the table's
// existence and row count alone would accept any one-row table of the
// name, so the guard reads the marker. Shared here so the populate and
// the guard cannot drift apart silently; the populate carries its own
// copy until the stitch makes it import this table.
const PopulateMarker = "hh-demo-populate-v1"

// The two bearer classes a flow can mint. A staff flow's account holds a
// staff grant in the demo org; a tenant flow's account is a portal user of
// one contractor account (Summit Ridge Builders) and mints a tenant class
// token whose tenant claim is that account's id (ADR-0016 C33).
const (
	ClassStaff  = "staff"
	ClassTenant = "tenant"
)

// TenantAccountID is the one contractor account the demo holds, Summit
// Ridge Builders (GLS-TENANT-r3): the customers row the populate creates,
// the tenant claim of every contractor token and the tenant_id of every
// contractor portal user. A pinned literal, like the staff ids, so the
// table, the populate and the tests cannot drift apart silently.
const TenantAccountID = "de2c0000-0000-4000-9000-000000000001"

// TenantAccountNo and TenantAccountName are the customers row's account
// number and trading name, invented.
const (
	TenantAccountNo   = "CUST-SUMMIT-RIDGE"
	TenantAccountName = "Summit Ridge Builders"
)

// Account is one demo role login: the flow name the entry page taps, the
// account it stands for, and the role that account must hold in the demo
// org. PlatformUserID is the token subject; the identity layer resolves
// authority from the database per request and never from the token.
type Account struct {
	// Flow is the closed flow name ("sales", "sales-manager").
	Flow string
	// PlatformUserID is the account's platform user id and the sub claim
	// of every token minted for it.
	PlatformUserID string
	// Email is the account's address at the reserved domain.
	Email string
	// DisplayName is the users display name the populate creates.
	DisplayName string
	// Label is the role label the mint response carries.
	Label string
	// Role is the role the account's live grant must hold: a staff role
	// code for a staff flow, a portal role (account_admin, buyer,
	// buyer_no_pay, ap_finance, field_crew) for a contractor flow.
	Role string
	// Tenant is the contractor account id (the customers row) a contractor
	// flow's portal user belongs to, and the tenant claim of its tokens.
	// Empty for a staff flow. A contractor row has no branch reach: the
	// tenant leg scopes by org and by contractor account only.
	Tenant string
}

// Class is the bearer class the flow mints: tenant when the row names a
// contractor account, staff otherwise. The entry page and the front door
// route a contractor to the Pro apps by this answer.
func (a Account) Class() string {
	if a.Tenant != "" {
		return ClassTenant
	}
	return ClassStaff
}

// TenantJob is one job site of the contractor account (a projects row),
// invented, in the three areas the tenant sheet names.
type TenantJob struct {
	ID      string
	Name    string
	Address string
	Area    string
}

// TenantJobs returns the contractor account's jobs in a stable order:
// La Jolla, Santa Fe and Point Loma (GLS-TENANT-r3).
func TenantJobs() []TenantJob {
	return []TenantJob{
		{ID: "de2c0000-0000-4000-9000-0000000000b1", Name: "Coastal terrace", Address: "4100 Example Bluff Road, La Jolla, CA", Area: "La Jolla"},
		{ID: "de2c0000-0000-4000-9000-0000000000b2", Name: "Hillside retaining walls", Address: "2250 Example Ridge Lane, Santa Fe, CA", Area: "Santa Fe"},
		{ID: "de2c0000-0000-4000-9000-0000000000b3", Name: "Courtyard and driveway", Address: "980 Example Cove Street, Point Loma, CA", Area: "Point Loma"},
	}
}

// accounts is the closed table. The Counter Till pair's platform user
// ids, emails and display names are the populate's rows, reconciled
// against the populate's pushed branch: the mint's token sub and the
// membership source's answers must name the users rows the populate
// creates. The persona rows name the logins migration 0200 plants: the
// same ids the demoseed resolves by those emails, and the display names
// the populate's renames leave on them. The agreement is by copy today
// (the populate holds its own copies of these values); it becomes by
// construction when the populate imports this table, a named stitch for
// the demo image's lane.
var accounts = map[string]Account{
	"sales": {
		Flow:           "sales",
		PlatformUserID: "de110000-0000-4000-9000-000000000001",
		Email:          "sales@" + ReservedDomain,
		DisplayName:    "Sales (demo)",
		Label:          "Sales",
		Role:           "inside_sales",
	},
	"sales-manager": {
		Flow:           "sales-manager",
		PlatformUserID: "de110000-0000-4000-9000-000000000002",
		Email:          "sales.manager@" + ReservedDomain,
		DisplayName:    "Sales manager (demo)",
		Label:          "Sales manager",
		Role:           "sales_manager",
	},
	// The yard manager, Manager Home's first per-role landing (roadmap 2.3):
	// the demo persona whose card opens the manager.yard profile.
	"yard-manager": {
		Flow:           "yard-manager",
		PlatformUserID: "de110000-0000-4000-9000-000000000003",
		Email:          "yard.manager@" + ReservedDomain,
		DisplayName:    "Yard manager (demo)",
		Label:          "Yard manager",
		Role:           "yard_manager",
	},
	"yard-team": {
		Flow:           "yard-team",
		PlatformUserID: "d0200000-0000-4000-9000-000000000002",
		Email:          "yard.demo@" + ReservedDomain,
		DisplayName:    "Yard persona (demo)",
		Label:          "Yard Team",
		Role:           "yard_team",
	},
	"driver": {
		Flow:           "driver",
		PlatformUserID: "d0200000-0000-4000-9000-000000000003",
		Email:          "driver.demo@" + ReservedDomain,
		DisplayName:    "Driver persona (demo)",
		Label:          "Driver",
		Role:           "driver",
	},
	// Payables and Receivables, the roles roadmap's first receivables
	// increment, PR 300. The id block is the lane's own, so the role
	// lanes' rows cannot collide as they land one after another.
	"payables-receivables": {
		Flow:           "payables-receivables",
		PlatformUserID: "de110000-0000-4000-9000-0000000000a1",
		Email:          "payables.receivables@" + ReservedDomain,
		DisplayName:    "Payables and Receivables (demo)",
		Label:          "Payables and Receivables",
		Role:           "payables_receivables",
	},
	// The procurement manager's login, the roles roadmap's Buying and
	// Stock slice, PR 303. The id block is this lane's own, so the role
	// lanes' rows cannot collide as they land one after another.
	"procurement-manager": {
		Flow:           "procurement-manager",
		PlatformUserID: "de64b000-0000-4000-9000-000000000001",
		Email:          "procurement.manager@" + ReservedDomain,
		DisplayName:    "Procurement manager (demo)",
		Label:          "Procurement manager",
		Role:           "procurement_manager",
	},
	// The Logistics Manager, the dispatch board's first role: the dispatch
	// persona migration 0200 plants, whose id and display name the populate
	// keeps. The label is the roles catalog's own spelling, the front door
	// card's word.
	"logistics-manager": {
		Flow:           "logistics-manager",
		PlatformUserID: "d0200000-0000-4000-9000-000000000001",
		Email:          "dispatch.demo@" + ReservedDomain,
		DisplayName:    "Dispatch persona (demo)",
		Label:          "Logistics Manager",
		Role:           "logistics_manager",
	},
	"financial-controller": {
		Flow:           "financial-controller",
		PlatformUserID: "de110000-0000-4000-9000-0000000000c1",
		Email:          "financial.controller@" + ReservedDomain,
		DisplayName:    "Financial controller (demo)",
		Label:          "Financial Controller",
		Role:           "financial_controller",
	},
	// The General Manager, the first of the coming roles cycle's five
	// logins (global, tier one). The id block is the cycle's own, so the
	// five rows cannot collide with the role lanes' blocks.
	"general-manager": {
		Flow:           "general-manager",
		PlatformUserID: "de5b2000-0000-4000-9000-000000000001",
		Email:          "general.manager@" + ReservedDomain,
		DisplayName:    "General Manager (demo)",
		Label:          "General Manager",
		Role:           "general_manager",
	},
	// HR, the second of the coming roles cycle's logins (global, tier one):
	// no Manager Home, no selling surface; its cells are House Messages and
	// Dealer Admin.
	"hr": {
		Flow:           "hr",
		PlatformUserID: "de5b2000-0000-4000-9000-000000000002",
		Email:          "hr@" + ReservedDomain,
		DisplayName:    "HR (demo)",
		Label:          "HR",
		Role:           "hr",
	},
	// The Branch Manager, the third of the coming roles cycle's logins
	// (branch scoped, tier two): the branch's own floor, quotes, dispatch
	// and Manager Home.
	"branch-manager": {
		Flow:           "branch-manager",
		PlatformUserID: "de5b2000-0000-4000-9000-000000000003",
		Email:          "branch.manager@" + ReservedDomain,
		DisplayName:    "Branch manager (demo)",
		Label:          "Branch Manager",
		Role:           "branch_manager",
	},
	// Outside Sales, the last of the coming roles cycle's logins (branch
	// scoped, tier three): the field seller who works quotes at the Quote
	// Desk and holds no till.
	"outside-sales": {
		Flow:           "outside-sales",
		PlatformUserID: "de5b2000-0000-4000-9000-000000000005",
		Email:          "outside.sales@" + ReservedDomain,
		DisplayName:    "Outside sales (demo)",
		Label:          "Outside Sales",
		Role:           "outside_sales",
	},
	// Summit Ridge Builders' five contractor personas, one per portal role
	// the core stores (identity/portal.go), role labels only on screens.
	// The id block is this lane's own. The platform user id is the portal
	// user's platform_user_id and the token subject; the account id is
	// TenantAccountID. Authority is the live portal row, never this table.
	"contractor-account-admin": {
		Flow:           "contractor-account-admin",
		PlatformUserID: "de2c0000-0000-4000-9000-0000000000a1",
		Email:          "account_admin@" + ReservedDomain,
		DisplayName:    "Contractor owner (demo)",
		Label:          "Contractor owner",
		Role:           "account_admin",
		Tenant:         TenantAccountID,
	},
	"contractor-buyer": {
		Flow:           "contractor-buyer",
		PlatformUserID: "de2c0000-0000-4000-9000-0000000000a2",
		Email:          "buyer@" + ReservedDomain,
		DisplayName:    "Contractor buyer (demo)",
		Label:          "Contractor buyer",
		Role:           "buyer",
		Tenant:         TenantAccountID,
	},
	"contractor-buyer-no-pay": {
		Flow:           "contractor-buyer-no-pay",
		PlatformUserID: "de2c0000-0000-4000-9000-0000000000a3",
		Email:          "buyer_no_pay@" + ReservedDomain,
		DisplayName:    "Contractor buyer (no pay) (demo)",
		Label:          "Contractor buyer (no pay)",
		Role:           "buyer_no_pay",
		Tenant:         TenantAccountID,
	},
	"contractor-ap-finance": {
		Flow:           "contractor-ap-finance",
		PlatformUserID: "de2c0000-0000-4000-9000-0000000000a4",
		Email:          "ap_finance@" + ReservedDomain,
		DisplayName:    "Contractor AP (demo)",
		Label:          "Contractor AP",
		Role:           "ap_finance",
		Tenant:         TenantAccountID,
	},
	"contractor-field-crew": {
		Flow:           "contractor-field-crew",
		PlatformUserID: "de2c0000-0000-4000-9000-0000000000a5",
		Email:          "field_crew@" + ReservedDomain,
		DisplayName:    "Contractor field (demo)",
		Label:          "Contractor field",
		Role:           "field_crew",
		Tenant:         TenantAccountID,
	},
}

// Get returns the account one flow name stands for. The second return is
// false for any name outside the table, so a caller cannot mint for a
// flow this file does not list.
func Get(flow string) (Account, bool) {
	a, ok := accounts[flow]
	return a, ok
}

// All returns every account in the table, ordered by flow name so the
// entry pages and the populate see one stable order.
func All() []Account {
	return []Account{
		accounts["sales"],
		accounts["sales-manager"],
		accounts["yard-manager"],
		accounts["yard-team"],
		accounts["driver"],
		accounts["payables-receivables"],
		accounts["procurement-manager"],
		accounts["financial-controller"],
		accounts["logistics-manager"],
		accounts["general-manager"],
		accounts["hr"],
		accounts["branch-manager"],
		accounts["outside-sales"],
		accounts["contractor-account-admin"],
		accounts["contractor-buyer"],
		accounts["contractor-buyer-no-pay"],
		accounts["contractor-ap-finance"],
		accounts["contractor-field-crew"],
	}
}

// ByPlatformUserID returns the account whose platform user id is given,
// for callers that hold a token subject and need to know which flow it
// belongs to.
func ByPlatformUserID(id string) (Account, bool) {
	for _, a := range accounts {
		if a.PlatformUserID == id {
			return a, true
		}
	}
	return Account{}, false
}
