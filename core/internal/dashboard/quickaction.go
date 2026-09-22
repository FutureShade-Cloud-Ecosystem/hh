package dashboard

// quickaction.go, DIB-434: the QUICK-ACTION REGISTRY, and the admin config that decides whether an
// entry in it renders at all.
//
// The ticket asked for one button. This is a registry with one entry, and the difference is the
// whole reason it lives here instead of in a Lit template:
//
//   * a hardcoded button knows where it goes. A registered action DECLARES where it goes (Unit,
//     Screen) and what it asks the destination to do (Intent), which means the client that draws it
//     never needs a switch statement, and the command palette this is obviously the seed of is a
//     second consumer of the same list, not a rewrite of it;
//   * a hardcoded button cannot be turned off. Colton's caveat is that a tech admin must be able to
//     hide it, globally, or for one job shape, without a release. Visibility is therefore CONFIG
//     (0151) resolved against the catalog below, exactly the way the cockpit resolves a role's tile
//     layout against Catalog (0140/0141). Same substrate, same idea, no second mechanism.
//
// THE SEAM THIS PACKAGE KEEPS. internal/dashboard imports no peer module, and a quick action does
// not break that: an action is a NAME, a destination expressed in the SHELL's own keys, and an
// intent string. This package has no idea what "start a fresh POS ticket" means and must not, the
// POS surface owns that, and owns the guard that protects a live till from it.

// Action ids. Stable strings: they are the primary key of quick_action_settings and
// quick_action_profile_visibility (0151), so renaming one orphans an admin's saved configuration
// and silently restores the default. Add, never rename.
const (
	// ActionNewSale, jump to the till and start a fresh ticket, from anywhere in the OS.
	ActionNewSale = "sale.new"
)

// Intent strings. What the action ASKS its destination surface to do on arrival. The destination
// consumes it; this package never interprets it. Kept as data next to the action so a new action's
// contract with its screen is one catalog line rather than a client-side special case.
const (
	// IntentPosNewTicket, "start a fresh ticket". The POS terminal is the only consumer, and it is
	// the POS terminal, not this string, that decides what happens when a ticket is already live.
	IntentPosNewTicket = "pos.new_ticket"
)

// ActionMeta is one registered quick action: what it is called, where it goes, and what it asks for
// when it gets there.
type ActionMeta struct {
	ID    string `json:"action_id"`
	Label string `json:"label"` // the button's own words
	Glyph string `json:"glyph"`
	// Title is the sentence the button carries as its tooltip AND its accessible name. One string,
	// so what a mouse user reads on hover and what a screen reader announces cannot drift apart.
	Title string `json:"title"`
	// Unit/Screen name a shell surface using the SHELL's own keys, so an action resolves to an
	// ordinary #unit/module hash, the frozen deep-link contract (dashboard-nav.ts), not a second
	// router. Same decision, and same wording, as TileMeta.
	Unit   string `json:"unit"`
	Screen string `json:"screen"`
	// Intent is the instruction handed to the destination surface on arrival.
	Intent string `json:"intent"`
	// Lead is the one-line explanation an admin reads next to the toggle.
	Lead string `json:"lead"`
	// DefaultEnabled / DefaultProfiles are the answer when 0151 holds no row, a build with an
	// un-migrated database, or an action shipped after an admin last saved. Code and migration
	// agree deliberately: the seed makes the default visible to an admin, this makes it TRUE even
	// if somebody deletes the row.
	DefaultEnabled  bool            `json:"default_enabled"`
	DefaultProfiles map[string]bool `json:"default_profiles"`
}

// ActionCatalog is the registry. ONE entry today, on purpose: the palette is not built, but the
// list it would read is, so building it later adds no new concept.
var ActionCatalog = []ActionMeta{
	{
		ID:    ActionNewSale,
		Label: "+ Sale",
		Glyph: "＋",
		// UNION TRAIN 12 · registry content standard: a TITLE is a tooltip, and a tooltip is a
		// label, not a paragraph. This was two sentences, and the second one pre-explained a
		// dialog. That guarantee, a ticket on the till is never discarded, is real and is kept:
		// it is spoken by the guard itself, at the moment it applies, where a person can act on
		// it, instead of in a hover a mouse user has to find and a keyboard user never sees.
		Title:  "Start a new counter sale at the till.",
		Unit:   "sales",
		Screen: "pos",
		Intent: IntentPosNewTicket,
		// The LEAD is the one-line description on the admin's own settings surface. It named the
		// control, its location and its scope, three facts the admin can see for themselves, next
		// to the toggle that turns it on. What is left is what the row does not already show.
		Lead: "Jumps to Point of Sale with a fresh ticket.",
		// The ruling: selling shapes see it. counter and manager sell; admin configures it and
		// therefore has to be able to see what they configured (0140's own reasoning for its admin
		// row); yard and driver do not sell, so they do not get chrome that implies they do.
		DefaultEnabled: true,
		DefaultProfiles: map[string]bool{
			ProfileCounter: true,
			ProfileManager: true,
			ProfileAdmin:   true,
			ProfileYard:    false,
			ProfileDriver:  false,
		},
	},
}

// ActionByID indexes ActionCatalog. Built once at init; never mutated afterwards.
var ActionByID = func() map[string]ActionMeta {
	m := make(map[string]ActionMeta, len(ActionCatalog))
	for _, a := range ActionCatalog {
		m[a.ID] = a
	}
	return m
}()

// IsAction reports whether id names a registered quick action.
func IsAction(id string) bool {
	_, ok := ActionByID[id]
	return ok
}

// ActionConfig is one action's stored configuration: the global switch plus the per-profile matrix.
// A nil/absent entry in Profiles means "not configured" and falls back to the catalog default -
// which is why this is a map of pointers-by-presence rather than a plain map[string]bool with a
// zero value that would be indistinguishable from a deliberate false.
type ActionConfig struct {
	ActionID string          `json:"action_id"`
	Enabled  bool            `json:"enabled"`
	Profiles map[string]bool `json:"profiles"`
	// EnabledSet / stored-ness is tracked by the store, not here: the repository only returns rows
	// that exist, and ResolveVisible treats an absent action id as "use the catalog default".
}

// VisibleFor decides whether ONE action renders for ONE job shape.
//
// TWO GATES, AND THE ORDER MATTERS. The global switch is checked first and its "off" is final: a
// tech admin who turns the action off expects it gone from every screen in the building, not gone
// from every screen except the ones whose per-profile row still says true. The per-profile row is
// the second gate and only ever narrows.
//
// An action with no stored config at all resolves to the catalog default, so a database that has
// not been migrated yet behaves like the shipped default rather than like "hidden everywhere".
func VisibleFor(meta ActionMeta, cfg *ActionConfig, profile string) bool {
	enabled := meta.DefaultEnabled
	if cfg != nil {
		enabled = cfg.Enabled
	}
	if !enabled {
		return false
	}
	if cfg != nil {
		if v, ok := cfg.Profiles[profile]; ok {
			return v
		}
	}
	return meta.DefaultProfiles[profile]
}

// ResolveVisible returns, in catalog order, the actions a caller with this job shape may see.
//
// The client renders exactly what this returns and nothing else, there is no "here is the list,
// now hide some of it" step on the client, because a gate the client applies is a gate the client
// can be made to skip.
func ResolveVisible(catalog []ActionMeta, cfgs map[string]ActionConfig, profile string) []ActionMeta {
	out := make([]ActionMeta, 0, len(catalog))
	for _, meta := range catalog {
		var cfg *ActionConfig
		if c, ok := cfgs[meta.ID]; ok {
			c := c
			cfg = &c
		}
		if VisibleFor(meta, cfg, profile) {
			out = append(out, meta)
		}
	}
	return out
}

// EffectiveConfig is one action as the ADMIN surface sees it: the catalog entry, the switch, and a
// per-profile answer for EVERY job shape, resolved, so a profile with no stored row shows the
// default it is actually getting rather than an empty cell the admin has to interpret.
type EffectiveConfig struct {
	ActionMeta
	Enabled bool `json:"enabled"`
	// Profiles is keyed by every entry in Profiles (the five job shapes), always fully populated.
	Profiles map[string]bool `json:"profiles"`
	// Stored is false when nothing has ever been saved for this action, the admin surface says
	// "default" instead of implying somebody chose this.
	Stored bool `json:"stored"`
}

// EffectiveFor resolves one action's full admin view.
func EffectiveFor(meta ActionMeta, cfg *ActionConfig) EffectiveConfig {
	eff := EffectiveConfig{
		ActionMeta: meta,
		Enabled:    meta.DefaultEnabled,
		Profiles:   make(map[string]bool, len(Profiles)),
		Stored:     cfg != nil,
	}
	if cfg != nil {
		eff.Enabled = cfg.Enabled
	}
	for _, p := range Profiles {
		v := meta.DefaultProfiles[p]
		if cfg != nil {
			if stored, ok := cfg.Profiles[p]; ok {
				v = stored
			}
		}
		eff.Profiles[p] = v
	}
	return eff
}

// NormalizeProfiles drops keys that are not job shapes from a submitted matrix. An admin surface
// that posts a typo'd profile name gets it ignored rather than stored, because a stored row for a
// profile nobody resolves to is a setting that can never be seen again but still shows up in the
// next admin's export.
func NormalizeProfiles(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(Profiles))
	for _, p := range Profiles {
		if v, ok := in[p]; ok {
			out[p] = v
		}
	}
	return out
}
