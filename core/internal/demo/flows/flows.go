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
// The table is closed: three flows, the pair the Counter Till cycle ships
// (D49, DECISIONS.md). A new flow is an edit here, reviewed, never a
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
	// Role is the staff role the account's grant must hold.
	Role string
}

// accounts is the closed table. The platform user ids, the emails and
// the display names are the populate's rows, reconciled against the
// populate's pushed branch: the mint's token sub and the membership
// source's answers must name the users rows the populate creates. The
// agreement is by copy today (the populate holds its own copies of these
// values); it becomes by construction when the populate imports this
// table, a named stitch for the demo image's lane.
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
}

// Get returns the account one flow name stands for. The second return is
// false for any name outside the table, so a caller cannot mint for a
// flow this file does not list.
func Get(flow string) (Account, bool) {
	a, ok := accounts[flow]
	return a, ok
}

// All returns every account in the table, ordered by flow name so the
// entry page and the populate see one stable order.
func All() []Account {
	return []Account{
		accounts["sales"],
		accounts["sales-manager"],
		accounts["yard-manager"],
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
