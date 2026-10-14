// Package dashboard is the cockpit substrate: the tile CATALOG, the per-role/per-user LAYOUT,
// and the MetricProvider seam every module's number arrives through.
//
// THE ONE RULE THIS PACKAGE EXISTS TO ENFORCE: it imports NO peer module. Not pos, not order,
// not dispatch, not tasks, not billing, not inventory, not warranty, not messaging. A tile is a
// NAME plus a promise that somebody, somewhere, can measure it; the composition root (cmd/server)
// is the only place that knows who. That is why MetricProvider is declared HERE, by the consumer,
// and satisfied THERE, by adapters, the same seam shape the rest of this codebase uses
// (order.PickStateReader, pricing.OverrideAuthority, billing.StatementNoticeQueue).
//
// The consequence people actually feel: an unwired tile renders a labelled em-dash, not a zero.
// "0 open orders" and "we cannot see open orders" are different sentences and only one of them is
// ever true when nothing is connected. See Service.Resolve.
package dashboard

import (
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// Kind classifies a tile's number so the client formats it without guessing. The client must not
// infer "this looks like cents" from magnitude.
type Kind string

const (
	KindCount      Kind = "count"       // a whole number of things
	KindMoneyCents Kind = "money_cents" // an amount in cents, CAD
	KindDays       Kind = "days"        // an age/interval in whole days
	// KindBasisPoints (the composed-metric catalog, viz.go) is a RATE in hundredths of a percent.
	// Integer, like every other figure that crosses this wire: a margin the browser reconstructs
	// from a float is a margin two screens can disagree about, and bps is how the rest of the ERP
	// already speaks rates (identity.MarginBps, quoteMarginBps).
	KindBasisPoints Kind = "basis_points"
)

// Tile IDs. Stable strings: they are the primary key of every stored layout row (0140), so
// renaming one orphans saved layouts. Add, never rename.
const (
	TileSalesToday      = "sales.today"          // pos
	TileOrdersOpen      = "orders.open"          // order
	TileDeliveriesToday = "deliveries.today"     // dispatch
	TilePickQueue       = "picks.queue"          // tasks
	TileARAging         = "ar.aging"             // billing
	TileLowStock        = "inventory.low_stock"  // inventory
	TileWarrantyOpen    = "warranty.open_claims" // warranty
	TileMessagingUnread = "messaging.unread"     // messaging
	// -- DIB-305 fulfilment/task tiles, same substrate, no special case --
	TileStalePicks    = "picks.stale"    // tasks
	TileWeightPending = "weight.pending" // tasks
	// -- DIB-463, the payments-today card. Same substrate, no special case (again) --
	//
	// It is a MESSAGING tile, not a billing or POS one, and that is the ticket's whole point: the
	// number it shows is the #payments channel's day line (DIB-449's rollup), read, not recomputed.
	// Naming its module 'messaging' is therefore not a filing convenience, it is what the unwired
	// label will say, and "not connected (messaging)" is the true sentence when the rollup is not
	// reachable. Calling it 'billing' would send someone to the wrong module to fix it.
	TilePaymentsToday = "payments.today" // messaging
)

// Layout profiles. Five job shapes, not fourteen role codes: the mapping from a role code to a
// profile is DATA (dashboard_role_profiles, 0140) so adding a role does not mean shipping code.
const (
	ProfileCounter = "counter"
	ProfileYard    = "yard"
	ProfileDriver  = "driver"
	ProfileManager = "manager"
	ProfileAdmin   = "admin"
)

// Manager profiles (0381, decision 5: one Manager Home, one profile row per manager role). A
// manager.<role> value is a REFINEMENT of a job shape, not a sixth shape: it is born from its base
// shape's rows (tiles, tabs, widgets, Needs-you policy) and diverges from there by editing data.
// The dashboard read answers the caller's own role's profile, so six manager roles stop sharing the
// controller's Home and each manager's landing becomes curatable on its own.
//
// ADD-ONLY, LIKE EVERY ID UNDER THIS ROOF: a stored layout row keys these strings, so a rename
// orphans it. The closed set travels with the CHECK constraint 0381 widened (the same thirteen
// values in every profile-keyed table) and with the OpenAPI DashboardProfile enum.
const (
	ProfileManagerBranch      = "manager.branch"      // born from "manager"
	ProfileManagerSales       = "manager.sales"       // born from "manager"
	ProfileManagerYard        = "manager.yard"        // born from "yard"; the first per-role landing (0381 §4)
	ProfileManagerLogistics   = "manager.logistics"   // born from "yard", 0203's dock ruling kept
	ProfileManagerProcurement = "manager.procurement" // born from "manager"
	ProfileManagerPayables    = "manager.payables"    // born from "manager"
	ProfileManagerController  = "manager.controller"  // born from "manager"
	ProfileManagerGeneral     = "manager.general"     // born from "admin"
)

// ManagerBaseShape names the job shape each manager profile was born from (0381 §3). The rows are
// copies, not a live inheritance, so this map is read only where a copy may be missing: the
// fallback policy for an unreadable table and the quick action defaults.
var ManagerBaseShape = map[string]string{
	ProfileManagerBranch:      ProfileManager,
	ProfileManagerSales:       ProfileManager,
	ProfileManagerYard:        ProfileYard,
	ProfileManagerLogistics:   ProfileYard,
	ProfileManagerProcurement: ProfileManager,
	ProfileManagerPayables:    ProfileManager,
	ProfileManagerController:  ProfileManager,
	ProfileManagerGeneral:     ProfileAdmin,
}

// Profiles is the closed set the migration's CHECK constraint also encodes.
var Profiles = []string{
	ProfileCounter, ProfileYard, ProfileDriver, ProfileManager, ProfileAdmin,
	ProfileManagerBranch, ProfileManagerSales, ProfileManagerYard, ProfileManagerLogistics,
	ProfileManagerProcurement, ProfileManagerPayables, ProfileManagerController, ProfileManagerGeneral,
}

// IsProfile reports whether p names a known layout profile.
func IsProfile(p string) bool {
	for _, v := range Profiles {
		if v == p {
			return true
		}
	}
	return false
}

// Tile states. The client renders an em-dash for anything that is not StateOK, and the label the
// state carries explains WHICH kind of nothing it is.
const (
	StateOK      = "ok"      // a provider measured it
	StateUnwired = "unwired" // no provider is registered for this tile
	StateError   = "error"   // a provider is registered and it failed (or timed out, or panicked)
	// StateHidden is a tile the layout switched OFF. It is returned (the editor needs the full
	// catalog with its current visibility) but deliberately carries no measurement and no
	// diagnosis: a hidden tile is not evidence that its module is up or down.
	StateHidden = "hidden"
)

// Part is one component of a compound metric, the AR aging buckets, or the escalation count that
// rides along with unread messages. The tile's own Number stays the headline.
type Part struct {
	Label  string `json:"label"`
	Number int64  `json:"number"`
	Kind   Kind   `json:"kind,omitempty"` // defaults to the tile's Kind when empty
}

// Value is what a MetricProvider returns: one headline number, an optional human sub-line, and an
// optional breakdown.
type Value struct {
	Number int64
	Hint   string
	Parts  []Part
	// AsOf (DIB-463) names the PERIOD a measurement is for, as the provider's own calendar day
	// (YYYY-MM-DD). Optional, and empty for the tiles that measure a live state rather than a period.
	//
	// It exists because "today" is not a fact a browser may decide about a number it did not compute.
	// The payments rollup files a day in the HOUSE zone (America/Toronto); a client that labelled the
	// card from its own clock would relabel yesterday's takings as today's for anyone in another
	// timezone, and for everyone with a tab left open across midnight. That is the precise failure
	// DIB-449 caught in its own first draft, and it is no less wrong on a dashboard than it was in a
	// channel. So the measurement carries its own day, and the view humanises it honestly.
	AsOf string
}

// Scope is everything a provider is allowed to know about who is asking. Deliberately NOT an
// http.Request or an identity.StaffContext: providers live in the composition root and must not
// need this package to import identity either.
type Scope struct {
	UserID   string
	RoleCode string
	Profile  string
	// BranchIDs are the branches the caller may see. Empty WITH AllBranches false means the caller
	// has no branch grants at all, a provider should return an empty measurement, not everything.
	BranchIDs []string
	// AllBranches is set for a GLOBAL-scope caller (Tier-1 global). Providers must treat it as
	// "no branch filter", never as "branch list of length zero".
	AllBranches bool
	// CostAllowed is the MARSHAL-LAYER gate for the composed-metric catalog (viz.go): resolved by
	// the handler from the caller's tier, never from a query parameter, and never from anything a
	// dealer can curate. False means every ClassCost metric is absent from what this caller
	// receives, absent, not hidden and not zeroed.
	CostAllowed bool
	Now         time.Time
	// Zone is the dealer's business day zone the window and the day literals below are read in,
	// stamped by the service beside Now (the zero Zone is the Ontario dealer's, America/Toronto).
	Zone houseday.Zone
	// Window is the resolved [Since, Until) the caller's widget named, in the HOUSE zone, never in
	// the process's. Resolved by the handler from the STORED row's `time_range`, so it is a fact
	// about the widget rather than a query parameter, and a caller cannot widen it by editing a URL.
	//
	// THE ZERO VALUE IS THE COMMON CASE AND IT MEANS "DO NOT NARROW". Every provider must work
	// without it: the composed-chart route has no widget and therefore no window, `open`/`all_time`/
	// `now` resolve to nothing on purpose, and a provider that has not learned a window yet ignores
	// the field entirely. That is why this rides on Scope rather than widening SeriesProvider -
	// putting it in the signature would compel eight adapters to acknowledge a value seven of them
	// cannot use.
	Window Window
}

// TileMeta is the catalog entry for one tile: everything the client needs to draw it, plus the
// deep link it navigates to. Screen/Unit are the SHELL's own keys, so a tile click reuses the
// frozen #unit/module hash contract rather than inventing a second navigation mechanism.
type TileMeta struct {
	ID     string `json:"tile_id"`
	Label  string `json:"label"`
	Module string `json:"module"` // the owning module, named in the unwired label
	Kind   Kind   `json:"kind"`
	Glyph  string `json:"glyph"`
	Accent string `json:"accent"`
	Unit   string `json:"unit,omitempty"`   // shell UnitKey  → #<unit>
	Screen string `json:"screen,omitempty"` // shell Tab      → #<unit>/<screen>
	// DefaultVisible decides what happens to a tile that ships AFTER a layout was saved: it is
	// appended to that layout at this visibility. A tile nobody asked for defaults to hidden.
	DefaultVisible bool `json:"default_visible"`
	// DefaultOnProfiles (DIB-500) names the job shapes for which this tile is DEFAULT-PRESENT: a
	// layout that does not mention it gets it appended VISIBLE for those profiles, instead of the
	// appended-hidden every other unnamed tile gets (see ApplyDefaultOn in layout.go). A stored row
	// naming the tile, visible OR hidden, always wins, so a person who switched it off stays
	// switched off. Nil for every tile that has not asked for this, and omitted from the wire so
	// their catalog entries serialize byte-identically.
	DefaultOnProfiles map[string]bool `json:"default_on_profiles,omitempty"`
	// Lead is the one-line explanation shown in the admin layout editor.
	Lead string `json:"lead"`
}

// Catalog is the ordered tile catalog. ORDER IS THE FALLBACK LAYOUT: a role with no stored
// profile rows gets exactly this, filtered to DefaultVisible.
var Catalog = []TileMeta{
	{
		ID: TileSalesToday, Label: "Today's sales", Module: "pos", Kind: KindMoneyCents,
		Glyph: "🧾", Accent: "#e0a54a", Unit: "sales", Screen: "pos", DefaultVisible: true,
		Lead: "Committed counter sales for today, at the branches you can see.",
	},
	{
		ID: TileOrdersOpen, Label: "Open orders", Module: "order", Kind: KindCount,
		Glyph: "📄", Accent: "#e0a54a", Unit: "sales", Screen: "pos", DefaultVisible: true,
		Lead: "Orders that are confirmed but not yet closed.",
	},
	{
		ID: TileDeliveriesToday, Label: "Deliveries today", Module: "dispatch", Kind: KindCount,
		Glyph: "🚚", Accent: "#4a86c4", Unit: "dispatch", Screen: "dispatch", DefaultVisible: true,
		Lead: "Delivery stops scheduled for today that are not yet delivered.",
	},
	{
		ID: TilePickQueue, Label: "Pick queue", Module: "tasks", Kind: KindCount,
		Glyph: "▦", Accent: "#7a915c", Unit: "yard", Screen: "yard", DefaultVisible: true,
		Lead: "Pick tasks waiting to be claimed or still in progress.",
	},
	{
		ID: TileARAging, Label: "AR aging", Module: "billing", Kind: KindMoneyCents,
		Glyph: "💳", Accent: "#4a9e6f", Unit: "finance", Screen: "ar", DefaultVisible: true,
		Lead: "Outstanding receivable balance, split into aging buckets.",
	},
	{
		ID: TileLowStock, Label: "Low stock", Module: "inventory", Kind: KindCount,
		Glyph: "📦", Accent: "#4a86c4", Unit: "inventory", Screen: "inventory", DefaultVisible: true,
		Lead: "Products at or below their reorder point.",
	},
	{
		ID: TileWarrantyOpen, Label: "Open claims", Module: "warranty", Kind: KindCount,
		Glyph: "🛡️", Accent: "#c4864a", Unit: "warranty", Screen: "warranty", DefaultVisible: true,
		Lead: "Warranty claims that have not reached a closed phase.",
	},
	{
		ID: TileMessagingUnread, Label: "Unread messages", Module: "messaging", Kind: KindCount,
		Glyph: "💬", Accent: "#c48ad0", Unit: "messaging", Screen: "messaging", DefaultVisible: true,
		Lead: "Unread messages across your channels, with escalations called out.",
	},
	// -- DIB-305 --
	{
		ID: TileStalePicks, Label: "Stale picks", Module: "tasks", Kind: KindCount,
		Glyph: "⏳", Accent: "#c99a3a", Unit: "yard", Screen: "yard", DefaultVisible: false,
		Lead: "Pick tasks that have sat unclaimed past their accept deadline.",
	},
	{
		ID: TileWeightPending, Label: "Weights pending", Module: "tasks", Kind: KindCount,
		Glyph: "⚖", Accent: "#7a915c", Unit: "yard", Screen: "yard", DefaultVisible: false,
		Lead: "NEED_WEIGHT tasks the yard has not returned a weight for.",
	},
	// -- DIB-463 --
	//
	// DefaultVisible is FALSE and that is the role gate, working exactly as ResolveLayout documents:
	// a curated layout (every shipped profile has one) appends an unnamed tile HIDDEN, and the catalog
	// fallback shows only DefaultVisible tiles. So this tile appears for nobody until a stored profile
	// row names it, and 0157 stores exactly two, 'manager' and 'admin', the profiles 0140 maps the
	// owner/finance roles onto. No role check in tile code, no second visibility mechanism.
	//
	// Kind is money_cents because the money is the headline a finance role reads first; the COUNT rides
	// as a Part, so the card reads "Payments today · $12,340.00 · 14 payments" without either number
	// having to be parsed out of the other.
	{
		ID: TilePaymentsToday, Label: "Payments today", Module: "messaging", Kind: KindMoneyCents,
		Glyph: "💵", Accent: "#4a9e6f", Unit: "messaging", Screen: "messaging", DefaultVisible: false,
		// DIB-500: the owner wants this card every day WITHOUT setup, and a layout saved before the
		// card shipped must not keep hiding it from them. DefaultOnProfiles is the mechanism (see
		// ApplyDefaultOn): for the two leadership shapes 0157 already gates it to, a layout that
		// does not NAME the card gets it appended visible, while any layout that names it (which is
		// every save the editor makes, hides included) is honoured exactly as stored. DefaultVisible
		// stays false: counter/yard/driver still never see it, on any path.
		//
		// 0381's manager refinements join the set because a refinement must not LOSE a guarantee its
		// base shape carried: a general manager filed under manager.general is the same owner the
		// card was defaulted on for, and the manager.* profiles born from "manager" inherit the same
		// promise. The two born from the dock (manager.yard, manager.logistics) stay OUT, exactly as
		// their base shape did: the payments card was never a yard-screen card.
		DefaultOnProfiles: map[string]bool{
			ProfileManager: true, ProfileAdmin: true,
			ProfileManagerBranch: true, ProfileManagerSales: true, ProfileManagerProcurement: true,
			ProfileManagerPayables: true, ProfileManagerController: true, ProfileManagerGeneral: true,
		},
		Lead: "Today's payments, the same count and total as the #payments channel's day line.",
	},
}

// CatalogByID indexes Catalog. Built once at init; never mutated afterwards.
var CatalogByID = func() map[string]TileMeta {
	m := make(map[string]TileMeta, len(Catalog))
	for _, t := range Catalog {
		m[t.ID] = t
	}
	return m
}()

// LayoutRow is one stored (or submitted) layout entry, the shape of both
// dashboard_profile_tiles and dashboard_user_tiles.
type LayoutRow struct {
	TileID   string `json:"tile_id"`
	Position int    `json:"position"`
	Visible  bool   `json:"visible"`
}

// Slot is a resolved layout entry: a catalog tile at a decided position and visibility, before
// any measurement happens.
type Slot struct {
	Meta     TileMeta
	Position int
	Visible  bool
}

// Tile is one resolved, measured tile as it goes over the wire.
//
// Number is a POINTER and it is nil unless State == StateOK. That is not a style choice: an int64
// zero value is indistinguishable from "we measured zero", and shipping a zero for a tile nobody
// is measuring is the exact failure this package was written to prevent. The client renders an
// em-dash whenever Number is absent.
type Tile struct {
	TileMeta
	Position int    `json:"position"`
	Visible  bool   `json:"visible"`
	State    string `json:"state"`
	Number   *int64 `json:"number,omitempty"`
	Hint     string `json:"hint,omitempty"`
	Parts    []Part `json:"parts,omitempty"`
	// AsOf (DIB-463) is Value.AsOf on the wire: the day this measurement is FOR, decided by whoever
	// computed it. omitempty, so every tile that shipped before this serializes byte-identically.
	AsOf string `json:"as_of,omitempty"`
	// Note explains a non-OK state in the words the tile shows under its em-dash.
	Note string `json:"note,omitempty"`
}

// HomeConfig is DIB-354's role-based screen takeover, as CONFIG (0141) rather than a hardcoded
// switch on role code. A driver's phone opens on the run sheet; a yard tablet opens on the pick
// queue; everyone else opens on the dashboard.
type HomeConfig struct {
	Profile string `json:"profile"`
	// LandingUnit/LandingModule name a shell surface using the shell's OWN keys, so the takeover
	// resolves to an ordinary #unit/module hash, the frozen contract, not a parallel router.
	LandingUnit   string `json:"landing_unit,omitempty"`
	LandingModule string `json:"landing_module,omitempty"`
	// Takeover: land straight on that surface instead of the dashboard home.
	Takeover bool `json:"takeover"`
	// MobileOnly: the takeover applies only below the mobile breakpoint. A yard manager at a desk
	// still gets the cockpit; the same person on the tablet in the yard gets the queue.
	MobileOnly bool `json:"mobile_only"`
}

// TabPin is one stored (or submitted) pinned-tab row, the shape of both dashboard_profile_tabs
// and dashboard_user_tabs (0149).
//
// TabKey is the BODY OF A FROZEN DEEP LINK ('sales/pos', 'finance'), so a pinned tab activates
// through the same route a bookmark does. Visible=false is a role default the person UNPINNED: it
// has to be stored rather than omitted, because an override is whole-or-absent and an empty one
// would read as "no override" (see ResolveTabs).
type TabPin struct {
	TabKey   string `json:"tab_key"`
	Position int    `json:"position"`
	Visible  bool   `json:"visible"`
}

// TabStrip is the pinned-tab half of a caller's home: DIB-433's answer to "each App as a tab at
// the top". Source carries the same three values as Layout.Source and drives the same
// "Reset to role default" affordance.
type TabStrip struct {
	Source string   `json:"source"`
	Pins   []TabPin `json:"pins"`
}

// Layout is the GET /api/v1/dashboard payload.
type Layout struct {
	Profile string `json:"profile"`
	// Source is "user" when a per-user override is in force, "role" when the profile default is,
	// and "catalog" when neither exists. It is what the "Reset to role default" button keys off.
	Source string     `json:"source"`
	Tiles  []Tile     `json:"tiles"`
	Home   HomeConfig `json:"home"`
	// Tabs rides along on the SAME request the shell already makes once per session. The strip has
	// to be right in the first paint, a pinned tab that arrives on a second round trip is a strip
	// that visibly rearranges itself under the cursor at login, which is worse than no pinning.
	Tabs TabStrip `json:"tabs"`
	// Hotkeys (DIB-496) rides the same request for the same reason: the dealer's key map has to be
	// in the shell before the first keypress, and a second round trip is a window where F1 silently
	// does nothing. Always an array, never null (see NormalizeHotkeys).
	Hotkeys []HotkeyBinding `json:"hotkeys"`
}

const (
	SourceUser    = "user"
	SourceRole    = "role"
	SourceCatalog = "catalog"
)
