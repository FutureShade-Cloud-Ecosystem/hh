// Package sync is the offline sync core (context 4.5, work item #165): the
// scope registry, the pull and push engines, the conflict engine and the two
// dual-bearer routes the contract declares (core/api/openapi.yaml, the sync
// surface). The package owns no business table: every scope's rows belong to
// the module that owns them, and each of those modules implements the Store
// seam (store.go) for its scope and is wired into the Engine at the
// composition root. The fence rules hold the same way they hold everywhere
// else: cross-module reads go through the owning module's seam, never a
// shared table grab.
//
// The routes are the G4 route half (ADR-0020): X-Org required under
// StaffBearer and rejected under TenantBearer, enforced by the guards
// package's RequireSync (the middleware half this package consumes), org and
// tenant stamped typed on the caller and forced into every Store call as the
// ScopeFilter predicate, never read from a request parameter.
package sync

import "sort"

// Policy is one scope's per-entity conflict policy, the vocabulary the
// contract's SyncConflict.policy enum pins (core/api/openapi.yaml):
// server_wins, last_write_wins_by_field, manual. PolicyPullOnly is the
// registry's record of Colton's ruling on undeclared scopes (the confirmed
// table, HH-3.5 Phase D pack): a scope the client never writes needs no
// conflict policy and is pull-only, its POST answered with the contract's
// own refusal shape. PolicyPullOnly never appears in a conflict envelope
// because a pull-only scope never reaches one.
type Policy string

const (
	// PolicyServerWins returns the server row as the outcome and applies
	// nothing of the client's stale write.
	PolicyServerWins Policy = "server_wins"
	// PolicyLWWByField merges per field, the server row authoritative on
	// fields the client's payload does not declare.
	PolicyLWWByField Policy = "last_write_wins_by_field"
	// PolicyManual never overwrites: the server row is returned with the
	// review marker (applied false, the entity waits on a review card).
	PolicyManual Policy = "manual"
	// PolicyPullOnly records the pull-only ruling: GET implemented, POST
	// refused in the contract's refusal shape.
	PolicyPullOnly Policy = "pull_only"
)

// Tenancy is which caller classes a scope serves, derived from the role
// catalog's audience declarations (docs/analysis/role-catalog.yaml): scopes
// declared only by internal frontends are org scopes (staff callers, org
// from X-Org), scopes declared by contractor frontends are tenant scopes
// (tenant from the token, never from X-Org, per G4), and the two scopes
// declared on both sides serve both classes.
type Tenancy string

const (
	// TenancyOrg serves staff callers only.
	TenancyOrg Tenancy = "org"
	// TenancyTenant serves tenant callers only.
	TenancyTenant Tenancy = "tenant"
	// TenancyBoth serves staff and tenant callers.
	TenancyBoth Tenancy = "both"
)

// ScopeInfo is one registry row: the scope's name (the contract's SyncScope
// enum vocabulary), its confirmed policy, and its tenancy class.
type ScopeInfo struct {
	Name    string
	Policy  Policy
	Tenancy Tenancy
}

// scopes is the 43-row registry, table-driven from the two documents the
// pack locks: the per-entity conflict policy CONFIRMED at the START
// checkpoint (including the runs exception, written at end-of-run close, and
// invoices, whose counter-till declaration carries no outbox while the
// pro-procurement declaration sets server_wins), and the catalog's audience
// declarations for tenancy. The SyncScope enum gate
// (scripts/sync-scope-catalog.test.ts) holds the catalog and the contract
// enum to the same 43; this table is the Go-side spine and its test pins it
// to the confirmed policy table entry by entry, so a policy edit here
// without the pack (or the reverse) fails the suite.
var scopes = map[string]ScopeInfo{
	// server_wins, per the confirmed table.
	"pick_tasks":      {Name: "pick_tasks", Policy: PolicyServerWins, Tenancy: TenancyOrg},
	"weights":         {Name: "weights", Policy: PolicyServerWins, Tenancy: TenancyOrg},
	"stops":           {Name: "stops", Policy: PolicyServerWins, Tenancy: TenancyOrg},
	"runs":            {Name: "runs", Policy: PolicyServerWins, Tenancy: TenancyOrg},
	"loads":           {Name: "loads", Policy: PolicyServerWins, Tenancy: TenancyOrg},
	"delivery_stops":  {Name: "delivery_stops", Policy: PolicyServerWins, Tenancy: TenancyOrg},
	"will_call":       {Name: "will_call", Policy: PolicyServerWins, Tenancy: TenancyOrg},
	"purchase_orders": {Name: "purchase_orders", Policy: PolicyServerWins, Tenancy: TenancyOrg},
	"approvals":       {Name: "approvals", Policy: PolicyServerWins, Tenancy: TenancyOrg},
	"messages":        {Name: "messages", Policy: PolicyServerWins, Tenancy: TenancyOrg},
	"acks":            {Name: "acks", Policy: PolicyServerWins, Tenancy: TenancyOrg},
	"orders":          {Name: "orders", Policy: PolicyServerWins, Tenancy: TenancyTenant},
	"tracking_events": {Name: "tracking_events", Policy: PolicyServerWins, Tenancy: TenancyTenant},
	"invoices":        {Name: "invoices", Policy: PolicyServerWins, Tenancy: TenancyBoth},
	"payments":        {Name: "payments", Policy: PolicyServerWins, Tenancy: TenancyTenant},
	"price_matrix":    {Name: "price_matrix", Policy: PolicyServerWins, Tenancy: TenancyBoth},

	// last_write_wins_by_field, per the confirmed table.
	"attachments":           {Name: "attachments", Policy: PolicyLWWByField, Tenancy: TenancyOrg},
	"quotes":                {Name: "quotes", Policy: PolicyLWWByField, Tenancy: TenancyOrg},
	"notify_prefs":          {Name: "notify_prefs", Policy: PolicyLWWByField, Tenancy: TenancyOrg},
	"scope_items":           {Name: "scope_items", Policy: PolicyLWWByField, Tenancy: TenancyTenant},
	"delivery_instructions": {Name: "delivery_instructions", Policy: PolicyLWWByField, Tenancy: TenancyTenant},

	// manual, per the confirmed table.
	"shortages":       {Name: "shortages", Policy: PolicyManual, Tenancy: TenancyOrg},
	"receiving":       {Name: "receiving", Policy: PolicyManual, Tenancy: TenancyOrg},
	"pod":             {Name: "pod", Policy: PolicyManual, Tenancy: TenancyOrg},
	"cycle_counts":    {Name: "cycle_counts", Policy: PolicyManual, Tenancy: TenancyOrg},
	"customer_quotes": {Name: "customer_quotes", Policy: PolicyManual, Tenancy: TenancyTenant},
	"team":            {Name: "team", Policy: PolicyManual, Tenancy: TenancyTenant},
	"pickups":         {Name: "pickups", Policy: PolicyManual, Tenancy: TenancyTenant},

	// pull-only, Colton's ruling on undeclared scopes. inventory_lookup is
	// the yard's read seam, products and customers the reference cache, the
	// rest the read-only records; orders stays server_wins above (the
	// pro-procurement declaration), it is named here only in this comment.
	"products":         {Name: "products", Policy: PolicyPullOnly, Tenancy: TenancyOrg},
	"customers":        {Name: "customers", Policy: PolicyPullOnly, Tenancy: TenancyOrg},
	"inventory_lookup": {Name: "inventory_lookup", Policy: PolicyPullOnly, Tenancy: TenancyOrg},
	"trucks":           {Name: "trucks", Policy: PolicyPullOnly, Tenancy: TenancyOrg},
	"drivers":          {Name: "drivers", Policy: PolicyPullOnly, Tenancy: TenancyOrg},
	"vendors":          {Name: "vendors", Policy: PolicyPullOnly, Tenancy: TenancyOrg},
	"channels":         {Name: "channels", Policy: PolicyPullOnly, Tenancy: TenancyOrg},
	"projects":         {Name: "projects", Policy: PolicyPullOnly, Tenancy: TenancyTenant},
	"catalog":          {Name: "catalog", Policy: PolicyPullOnly, Tenancy: TenancyTenant},
	"parked_tickets":   {Name: "parked_tickets", Policy: PolicyPullOnly, Tenancy: TenancyOrg},
	"warranty_claims":  {Name: "warranty_claims", Policy: PolicyPullOnly, Tenancy: TenancyOrg},
	"widget_measures":  {Name: "widget_measures", Policy: PolicyPullOnly, Tenancy: TenancyOrg},
	"warranty_board":   {Name: "warranty_board", Policy: PolicyPullOnly, Tenancy: TenancyOrg},
	"payment_methods":  {Name: "payment_methods", Policy: PolicyPullOnly, Tenancy: TenancyTenant},
	"aging":            {Name: "aging", Policy: PolicyPullOnly, Tenancy: TenancyTenant},
}

// Lookup resolves one scope name to its registry row. ok is false for any
// name the contract's SyncScope enum does not carry; the routes answer such
// a name with 400, the contract's unknown-scope refusal.
func Lookup(name string) (ScopeInfo, bool) {
	info, ok := scopes[name]
	return info, ok
}

// Scopes returns the registry rows sorted by name, the stable order the
// registry's own test and any listing consumer uses.
func Scopes() []ScopeInfo {
	out := make([]ScopeInfo, 0, len(scopes))
	for _, info := range scopes {
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Admits reports whether a caller class fits the scope's tenancy. The
// handler consults this before any store call: an org scope under a tenant
// bearer (or the mirror) is the RBAC-403, the same family every scope-member
// refusal uses, byte-identical body, so the refusal leaks nothing about the
// scope's rows.
func (i ScopeInfo) Admits(tenantCaller bool) bool {
	switch i.Tenancy {
	case TenancyOrg:
		return !tenantCaller
	case TenancyTenant:
		return tenantCaller
	case TenancyBoth:
		return true
	}
	return false
}
