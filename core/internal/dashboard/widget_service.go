package dashboard

// widget_service.go, resolving, saving and RESETTING a person's widget layout, and assembling the
// Needs-you queue.
//
// WHY A SEPARATE STORE SEAM. WidgetStore is its own interface rather than five more methods on
// Store, and the reason is not tidiness: every fake in this package's existing tests implements
// Store, and widening it would stop them compiling for a feature they do not exercise. The hotkey
// and metric-curation reads made the same call for the same reason, and a store nobody injected must
// DEGRADE rather than error, a build ahead of 0201 serves an empty widget layout and a working
// Home, not a 500.

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Widget-layer sentinels. The handler maps these to 4xx; anything else is a 500, so a new internal
// failure can never be reported as the caller's fault.
var (
	// ErrMeasureNotOffered, a submitted widget names a measure that is not in this caller's
	// catalog. ONE ERROR FOR TWO CASES on purpose (see ValidateWidgets): "no such measure" and "not
	// yours" are indistinguishable to the caller, so the refusal cannot be used to enumerate what
	// exists.
	ErrMeasureNotOffered = errors.New("a widget names a measure that is not in your catalog")
	// ErrShapeNotOffered, a shape that measure does not declare (a gauge on a measure with no
	// ceiling, a board on a measure with no states).
	ErrShapeNotOffered = errors.New("a widget names a shape that measure does not offer")
	// ErrSplitNotOffered, a split that measure does not declare.
	ErrSplitNotOffered = errors.New("a widget names a split that measure does not offer")
	// ErrRangeNotOffered, a window that measure cannot be asked for (a queue "last 30 days", a
	// single-day figure "this quarter"). Every measure declares exactly the windows its provider can
	// actually honour; asking for another one produced a true-looking number under a false caption.
	ErrRangeNotOffered = errors.New("a widget names a range that measure does not offer")
	// ErrSpanNotOffered, a free span (0207) naming a grid line that does not exist: a column count
	// off the snap set (3,4,6,8,9,12) or a row count outside 1-3. It is a REFUSAL rather than a
	// silent repair because there is nothing honest to repair it to, rounding five columns down to
	// four and up to six are both a card the person did not drag, applied behind their back on a
	// request that answered 200. NormalizeWidgets owns the other half of this (a half-pair from a
	// client that predates the columns) and clears it to the preset instead; the two cases are an
	// invention and an omission and they must not be treated alike.
	ErrSpanNotOffered = errors.New("a widget names a span the grid does not offer")
	// ErrBranchNotOffered, a branch narrowing the caller was not granted, OR a branch that does not
	// exist. ONE ERROR FOR TWO CASES, the third time this file makes that call and for the third time
	// the same reason: two different answers would let a caller ask the server which yards are real
	// (see refuseUnknownBranches). Same wording posture as its siblings: it does not name the branch,
	// so the refusal cannot be used to ask the server which branches exist.
	ErrBranchNotOffered = errors.New("a widget names a branch this caller was not granted")
	// ErrEmptyWidgets, a submitted layout survived normalisation to nothing. Refused for exactly
	// the reason SaveMyLayout refuses an empty tile layout: stored, it is indistinguishable from NO
	// override, so the person's next login would silently hand them the role default back and their
	// deliberate act would look like it had been undone.
	ErrEmptyWidgets = errors.New("a widget layout must name at least one well-formed widget")
	// ErrNoWidgetStore, the substrate is not wired (a build ahead of 0201). Only ever returned from
	// a WRITE: the reads degrade to empty.
	ErrNoWidgetStore = errors.New("widget substrate not configured")
)

// WidgetStore is the persistence seam for widget layouts (0201).
type WidgetStore interface {
	ProfileWidgets(ctx context.Context, profile string) ([]Widget, error)
	UserWidgets(ctx context.Context, userID string) ([]Widget, error)
	SaveUserWidgets(ctx context.Context, userID string, rows []Widget) error
	ClearUserWidgets(ctx context.Context, userID string) (int, error)
	SaveProfileWidgets(ctx context.Context, profile string, rows []Widget, by string) error
}

// NeedsPolicyStore reads the dealer's Needs-you source policy (0201 dashboard_needs_sources).
// Optional: a nil store, or an unreadable one, resolves to DefaultNeedsSources.
type NeedsPolicyStore interface {
	NeedsSources(ctx context.Context, profile string) ([]NeedsPolicy, error)
}

// GalleryStore is the persistence seam for the "Add widget" gallery (0208).
//
// A THIRD INTERFACE RATHER THAN TWO MORE METHODS ON WidgetStore, and it is the argument this file
// opens with, applied again: every fake in this package's tests that implements WidgetStore would
// stop compiling for a feature it does not exercise, and, the part that actually matters in
// production, a build sitting between 0201 and 0208 must serve a working Home with no gallery
// rather than a Service whose widget layer will not wire. NeedsPolicyStore was split off for exactly
// this reason one wave ago; WidgetRepository satisfies all three, so the composition root still binds
// one object.
//
// TWO VERBS, NOT FIVE. A gallery has no per-person layer at all, it is dealer curation, full stop -
// so there is no user read, no user write and no reset. That absence is the whole design: "reset my
// gallery" would be a sentence with no meaning, because nobody ever personalised one.
type GalleryStore interface {
	ProfileGallery(ctx context.Context, profile string) ([]GalleryEntry, error)
	SaveProfileGallery(ctx context.Context, profile string, rows []GalleryEntry, by string) error
}

// BranchResolver is the FIFTH GATE on a widget's branch narrowing: does this branch exist at all.
//
// -- THE DEFECT, AS IT REACHED A BROWSER ---------------------------------------------------------
//
// PUT /api/v1/dashboard/widgets, as a manager-up GLOBAL caller, with a widget carrying a
// syntactically perfect but invented branch uuid, answered 500 carrying the generic internal-error
// envelope. (That body is DESCRIBED here and never pasted, deliberately: the hand-rolled-envelope
// sweep at platform/httpx/counter_vocabulary_test.go:104 matches the envelope's error key and its
// value over RAW SOURCE, comments included, so a header quoting the JSON it is arguing ABOUT is
// indistinguishable to it from a handler writing one, and this file would land on a ratchet whose
// entire job is catching bypasses of httpx.Error. The rule for every header under this roof: name
// the body, do not paste it. The same trap catches a header that pastes the sweep's own pattern.)
// Nothing was written, so the security property held, but the 500 was an EXISTENCE ORACLE: a real
// branch id saved and a fake one 500'd, so the endpoint would answer "is this a real yard" for any
// uuid you cared to try. That is precisely what ValidateWidgets' one-message rule (widget.go:529-532) exists
// to prevent, arrived at from the other direction. The route there: BranchGate.Allows returns true
// for every well-formed uuid when the caller is global (widget.go:290-297); IsBranchID checks SHAPE
// only and says so in its own header (widget.go:225); so the id reached
// widget_repository.go:99-106, violated `branch_id uuid REFERENCES locations (id)` (migration 0201),
// and the raw pgx error fell through widget_handler.go:159's `default:` arm to a 500.
//
// COMMIT 7027df5 CLOSED FOUR GATES AND THIS IS THE FIFTH. Shape (IsBranchID), grant
// (BranchGate.Allows), narrowing (narrowToBranch) and the substrate's own sets are all answerable
// from what the caller sent plus what the caller holds. "Does `locations` hold this row" is not, and
// this package may not read `locations`, so it declares the question and the composition root
// answers it, exactly the way purchasing declares BranchDirectory (purchasing/module.go:66-79) and
// main.go:3040-3081 satisfies it over the same table.
//
// NIL DEGRADES TO TODAY'S BEHAVIOUR, which is the house rule for every consumer-declared seam here
// and is stated most plainly by the seam this one is modelled on: "Nil is honest and degrades
// DOWNWARD" (purchasing/module.go:74). Unwired, the four gates still run and an invented id still
// reaches Postgres, no worse than before, and a build that has not wired it does not lose the
// ability to save a Home.
type BranchResolver interface {
	Exists(ctx context.Context, branchID string) (bool, error)
}

// WithWidgetStore binds the widget substrate. Chainable at wiring time.
func (s *Service) WithWidgetStore(st WidgetStore) *Service { s.widgetStore = st; return s }

// WithNeedsStore binds the Needs-you policy read. Chainable at wiring time.
func (s *Service) WithNeedsStore(st NeedsPolicyStore) *Service { s.needsStore = st; return s }

// WithGalleryStore binds the "Add widget" gallery (0208). Chainable at wiring time, and leaving it
// unbound is a supported state: the read serves an empty gallery, so "Add widget" falls straight
// through to "Build your own", which is the whole builder, unchanged, and is exactly what every
// person had before this wave.
func (s *Service) WithGalleryStore(st GalleryStore) *Service { s.galleryStore = st; return s }

// ErrNoGalleryStore, the gallery substrate is not wired (a build ahead of 0208). Only ever returned
// from the WRITE; the read degrades to an empty gallery, for the reason WidgetsFor gives: a Home with
// nothing in the Add panel is a recoverable afternoon and a Home that 500s is not.
var ErrNoGalleryStore = errors.New("widget gallery not configured")

// GalleryFor resolves the caller's gallery: their role's curated entries, minus every entry whose
// measure is not in their catalog.
//
// A READ NEVER FAILS OVER CONFIGURATION, the same posture and the same words as WidgetsFor. No store,
// or a store that will not answer, gives an empty gallery and a logged warning.
func (s *Service) GalleryFor(ctx context.Context, scope Scope) Gallery {
	if scope.Profile == "" {
		scope.Profile = s.ProfileFor(ctx, scope.RoleCode)
	}
	var rows []GalleryEntry
	if s.galleryStore != nil {
		var err error
		if rows, err = s.galleryStore.ProfileGallery(ctx, scope.Profile); err != nil {
			s.warn("dashboard: widget gallery unreadable; Add widget opens the builder",
				"profile", scope.Profile, "err", err)
			rows = nil
		}
	}
	cards := GalleryCardsFor(rows, s.allowedMeasures(ctx, scope))
	if cards == nil {
		cards = []GalleryCard{}
	}
	return Gallery{Profile: scope.Profile, Entries: cards}
}

// ProfileGalleryFor returns a role's gallery as STORED (admin), every entry, including ones this
// admin's own catalog would hide, because that is what they are about to edit. The staff read is
// where the caller's catalog narrows it.
func (s *Service) ProfileGalleryFor(ctx context.Context, profile string) ([]GalleryEntry, error) {
	if !IsProfile(profile) {
		return nil, ErrUnknownProfile
	}
	if s.galleryStore == nil {
		return []GalleryEntry{}, nil
	}
	rows, err := s.galleryStore.ProfileGallery(ctx, profile)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []GalleryEntry{}
	}
	return rows, nil
}

// SaveProfileGallery stores a role's gallery (admin). One document, replaced whole, the same shape
// as every other curation write in this package, and for the reason widget_handler.go:33-37 gives
// about widgets: a per-entry POST would need somewhere to append to, and there is no code-side
// default gallery to materialise.
//
// THE MEASURE GATE IS THE FULL CATALOG, NOT THE ADMIN'S OWN, and this is SaveProfileWidgets' sentence
// unchanged: an admin may curate a cost-bearing entry onto the manager shape, and a caller who may
// not see it receives a gallery without it on every read. Curation hides things; it never grants
// anything. Naming is not seeing.
//
// AN EMPTY GALLERY IS ALLOWED, and this is where it parts company with SaveMyWidgets. An empty
// personal LAYOUT is refused because stored it is indistinguishable from no override, so the person's
// next login would silently hand them the role default back. A gallery has no second layer to be
// confused with: "this shape is offered nothing ready-made, everybody builds their own" is a real
// curation decision, it is what the driver ships with, and refusing it would leave an admin unable to
// clear a gallery they had decided against.
func (s *Service) SaveProfileGallery(ctx context.Context, profile string, rows []GalleryEntry, by string) ([]GalleryEntry, error) {
	if !IsProfile(profile) {
		return nil, ErrUnknownProfile
	}
	if s.galleryStore == nil {
		return nil, ErrNoGalleryStore
	}
	clean := NormalizeGallery(rows)
	if err := ValidateGallery(clean, MetricByID); err != nil {
		return nil, err
	}
	if err := s.galleryStore.SaveProfileGallery(ctx, profile, clean, by); err != nil {
		return nil, err
	}
	return clean, nil
}

// WithBranchResolver binds the branch existence read. Chainable at wiring time, optional, and
// leaving it unbound is a supported state, see BranchResolver.
func (s *Service) WithBranchResolver(r BranchResolver) *Service { s.branchResolver = r; return s }

// RegisterNeedsSource binds a source adapter to a catalog source key. Called ONLY from the
// composition root.
//
// An unknown key is REFUSED rather than ignored, for the third time in this package and the same
// reason: a typo'd key is a silently dead adapter whose symptom (a queue row that never appears)
// is indistinguishable from "nothing is waiting", which is the one lie this widget must not tell.
func (s *Service) RegisterNeedsSource(key string, src NeedsSource) error {
	if _, ok := NeedsSourceByKey[key]; !ok {
		return errUnknownNeedsSource(key)
	}
	if src == nil {
		return errNilNeedsSource(key)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.needs == nil {
		s.needs = map[string]NeedsSource{}
	}
	s.needs[key] = src
	return nil
}

// WiredNeedsSources lists the source keys that currently have an adapter, in catalog order, for the
// boot log, so an unwired source is visible at start-up rather than as a missing row at 7am.
func (s *Service) WiredNeedsSources() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.needs))
	for _, m := range NeedsSourceCatalog {
		if _, ok := s.needs[m.Key]; ok {
			out = append(out, m.Key)
		}
	}
	return out
}

func (s *Service) needsSource(key string) (NeedsSource, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src, ok := s.needs[key]
	return src, ok
}

// allowedMeasures is the caller's catalog as a map, the SAME two layers as the chart composer's
// (MetricsFor: curation decides the menu, the cost gate decides what may exist), so a widget and a
// composed chart can never disagree about what this person may see.
func (s *Service) allowedMeasures(ctx context.Context, scope Scope) map[string]MetricMeta {
	out := map[string]MetricMeta{}
	for _, m := range MetricsFor(s.curation(ctx, scope.Profile), scope.CostAllowed) {
		out[m.ID] = m
	}
	return out
}

// WidgetCatalogFor is the builder's one read: the measures this caller may draw, each carrying the
// splits and widget shapes it supports, plus the substrate's closed sets.
func (s *Service) WidgetCatalogFor(ctx context.Context, scope Scope) []MetricMeta {
	if scope.Profile == "" {
		scope.Profile = s.ProfileFor(ctx, scope.RoleCode)
	}
	return MetricsFor(s.curation(ctx, scope.Profile), scope.CostAllowed)
}

// WidgetsFor resolves the caller's Home: their layout if they have one, else their role's default,
// else nothing, then drops every widget whose measure is not in their catalog.
//
// A READ NEVER FAILS OVER CONFIGURATION. No widget store, or a store that will not answer, gives an
// empty layout and a logged warning rather than an error: a Home with no widgets is a recoverable
// afternoon, and a Home that 500s is not.
func (s *Service) WidgetsFor(ctx context.Context, scope Scope) WidgetLayout {
	if scope.Profile == "" {
		scope.Profile = s.ProfileFor(ctx, scope.RoleCode)
	}
	var profileRows, userRows []Widget
	if s.widgetStore != nil {
		var err error
		if profileRows, err = s.widgetStore.ProfileWidgets(ctx, scope.Profile); err != nil {
			s.warn("dashboard: widget role default unreadable", "profile", scope.Profile, "err", err)
			profileRows = nil
		}
		if scope.UserID != "" {
			if userRows, err = s.widgetStore.UserWidgets(ctx, scope.UserID); err != nil {
				s.warn("dashboard: personal widget layout unreadable; falling back to the role default",
					"err", err)
				userRows = nil
			}
		}
	}
	rows, source := ResolveWidgets(profileRows, userRows)
	views := WidgetViewsFor(rows, s.allowedMeasures(ctx, scope))
	if views == nil {
		views = []WidgetView{}
	}
	return WidgetLayout{Profile: scope.Profile, Source: source, Widgets: views}
}

// SaveMyWidgets stores a person's whole layout.
//
// THE BODY IS THE WHOLE SET, because the override is whole-or-absent, which is also why "add a
// widget" is not a route. The builder places a widget by submitting the layout it is already showing
// with the new one at the top, so the first personal save materialises what the person had; there is
// no server-side path that writes a role default into a person's rows, which is the door 0191 had to
// close and 0192/0193 restate.
//
// VALIDATION ORDER IS THE POINT OF THIS FUNCTION. Normalise (drop substrate-malformed rows), then
// GATE (refuse a forbidden measure outright), then store. The gate runs before the store is touched,
// so a forged submission leaves ZERO rows behind, byte-absence gates the display, and this gates
// the act. The twin test asserts the store is never called.
func (s *Service) SaveMyWidgets(ctx context.Context, scope Scope, rows []Widget) ([]Widget, error) {
	if scope.UserID == "" {
		return nil, ErrNoCaller
	}
	if s.widgetStore == nil {
		return nil, ErrNoWidgetStore
	}
	if scope.Profile == "" {
		scope.Profile = s.ProfileFor(ctx, scope.RoleCode)
	}
	clean := NormalizeWidgets(rows)
	if len(clean) == 0 {
		return nil, ErrEmptyWidgets
	}
	if err := ValidateWidgets(clean, s.allowedMeasures(ctx, scope), s.myBranchGate(ctx, scope)); err != nil {
		return nil, err
	}
	// AFTER THE GRANT GATE, NEVER BEFORE IT, and the order is the whole security argument, see
	// refuseUnknownBranches.
	if err := s.refuseUnknownBranches(ctx, clean); err != nil {
		return nil, err
	}
	if err := s.widgetStore.SaveUserWidgets(ctx, scope.UserID, clean); err != nil {
		return nil, err
	}
	return clean, nil
}

// ResetMyWidgets is RESET TO ROLE DEFAULT, and it is one DELETE.
//
// It writes nothing. It does not copy the role default into the person's rows, does not "restore"
// anything, and does not need to know what the default currently is, it removes the person's own
// layer and the layer beneath answers again, which is what the word reset has meant everywhere else
// in this product since 0191 had to make it mean that. The one-way door is a reset that MATERIALISES:
// afterwards nobody can tell a default from a choice, and the next change to the role default reaches
// nobody who ever pressed the button.
func (s *Service) ResetMyWidgets(ctx context.Context, userID string) (int, error) {
	if userID == "" {
		return 0, ErrNoCaller
	}
	if s.widgetStore == nil {
		return 0, ErrNoWidgetStore
	}
	return s.widgetStore.ClearUserWidgets(ctx, userID)
}

// ProfileWidgetsFor returns a role default as stored (admin).
func (s *Service) ProfileWidgetsFor(ctx context.Context, profile string) ([]Widget, error) {
	if !IsProfile(profile) {
		return nil, ErrUnknownProfile
	}
	if s.widgetStore == nil {
		return []Widget{}, nil
	}
	rows, err := s.widgetStore.ProfileWidgets(ctx, profile)
	if err != nil {
		return nil, err
	}
	out, _ := ResolveWidgets(rows, nil)
	return out, nil
}

// myBranchGate is the caller's own grants PLUS any branch already stored on their own rows.
//
// -- THE SECOND HALF IS NOT GENEROSITY, IT IS THE ONLY THING THAT MAKES A REVOCATION SURVIVABLE --
//
// A layout is one document: the client re-PUTs everything it was served on every drag. So the day a
// person's grant for a branch is revoked, a widget THEY saved while they held it would make their
// whole Home unsaveable, every drag a 422, naming no widget, with no way back except deleting the
// card. That is a worse outcome than the thing the gate is for.
//
// Adding what is already stored grants nothing. They chose that branch themselves while they held
// it, the string reveals nothing they did not already know, and the MEASUREMENT still intersects
// against live grants, so the widget draws nothing rather than another branch's numbers. The rule
// is: you may not ADD a branch you do not hold; you may keep one you already have.
//
// It reads the store, and a store that will not answer falls back to grants alone, the same posture
// the layout read takes, and the strict direction to fail in.
func (s *Service) myBranchGate(ctx context.Context, scope Scope) BranchGate {
	g := BranchGateFor(scope)
	if s.widgetStore == nil || scope.UserID == "" {
		return g
	}
	held, err := s.widgetStore.UserWidgets(ctx, scope.UserID)
	if err != nil {
		s.warn("dashboard: existing widget rows unreadable; a revoked branch will refuse the save", "err", err)
		return g
	}
	for _, w := range held {
		if w.BranchID != "" && !g.Allows(w.BranchID) {
			g.IDs = append(g.IDs, w.BranchID)
		}
	}
	return g
}

// refuseUnknownBranches is the FIFTH GATE, and everything about it is decided by two rules already
// written down in this package.
//
// -- RULE ONE: THE SAME REFUSAL, OR IT IS AN ORACLE AGAIN ----------------------------------------
//
// It returns ErrBranchNotOffered, the SAME sentinel, the same 422, the same words as a branch the
// caller was not granted. A 404 was the obvious alternative (widget_handler.go:153-154 already maps
// ErrUnknownProfile that way, and "no such branch" is literally a missing thing), and it is wrong
// here for the reason ValidateWidgets gives about measures at widget.go:529-532: "ONE MESSAGE FOR
// TWO CASES, on purpose". Two statuses would mean 422 = "that yard is real and is not yours" and
// 404 = "no such yard", a perfect enumeration oracle for a branch-scoped caller, which is a WORSE
// version of the 500 this function exists to remove, because it is a supported answer rather than a
// crash. 422 is also what widget_handler.go:140-144 already argues for a well-formed submission
// whose CONTENT is wrong, which is exactly what this is.
//
// The caller loses nothing by the shared wording. ErrBranchNotOffered reads "a widget names a branch
// this caller was not granted", which is a sentence a person can act on, pick a branch from the
// picker, or leave the field on "every branch I can see", and the branch they typed is not the
// thing they need to be told about.
//
// -- RULE TWO: THE ORDER, WHICH IS WHY THE ORACLE CANNOT BE REBUILT FROM TIMING EITHER -----------
//
// This runs AFTER ValidateWidgets. A scoped caller's invented id is refused by the GRANT gate and
// never reaches this function, so no database read ever happens on their behalf and nothing about
// existence is observable to them, by status, wording or latency. The only caller who reaches the
// resolver is one whose gate said yes: a GLOBAL caller (BranchGate.All), or a caller re-saving a
// branch already stored on their own row, and a global caller can already read the whole branch
// list from GET /api/v1/branches, so telling them "that one is not real" reveals nothing they were
// not entitled to ask for directly.
//
// -- AND IT DEGRADES DOWNWARD --------------------------------------------------------------------
//
// No resolver, or a resolver that will not answer, is TODAY'S BEHAVIOUR: the id goes on to Postgres
// exactly as it did before this existed. That is the same call myBranchGate makes one function up
// ("a store that will not answer falls back to grants alone") and the same one WidgetsFor makes for
// the read path. Refusing a save because a lookup blinked would break every branch-narrowed Home in
// the building over a transient, to protect against a mistake that at worst produces the 500 we
// already had.
//
// It asks once per DISTINCT id, not once per widget: a bento with nine cards on the Kingston yard is
// one question, and nine identical reads inside a save would be a cost with no answer attached.
//
// SaveProfileWidgets does not call this, and that is not an omission, it validates under NoBranch,
// which refuses every named branch outright (widget.go:283-286), so there is no surviving branch id
// for this to ask about.
func (s *Service) refuseUnknownBranches(ctx context.Context, rows []Widget) error {
	if s.branchResolver == nil {
		return nil
	}
	asked := make(map[string]bool, len(rows))
	for _, w := range rows {
		if w.BranchID == "" || asked[w.BranchID] {
			continue
		}
		asked[w.BranchID] = true
		ok, err := s.branchResolver.Exists(ctx, w.BranchID)
		if err != nil {
			s.warn("dashboard: branch existence unreadable; the save proceeds on the grant gate alone",
				"err", err)
			continue
		}
		if !ok {
			return ErrBranchNotOffered
		}
	}
	return nil
}

// SaveProfileWidgets stores a role default (admin).
//
// THE MEASURE GATE HERE IS THE FULL CATALOG, NOT THE ADMIN'S OWN. A role default is CURATION: an
// admin may put a cost-bearing measure on the manager shape's Home, and a caller who may not see it
// still receives a payload without it (WidgetViewsFor drops it on every read). Curation hides
// things; it never grants anything, 0182's sentence, and the reason this write is allowed to name
// more than the writer could personally see is that naming is not seeing.
func (s *Service) SaveProfileWidgets(ctx context.Context, profile string, rows []Widget, by string) ([]Widget, error) {
	if !IsProfile(profile) {
		return nil, ErrUnknownProfile
	}
	if s.widgetStore == nil {
		return nil, ErrNoWidgetStore
	}
	clean := NormalizeWidgets(rows)
	if len(clean) == 0 {
		return nil, ErrEmptyWidgets
	}
	if err := ValidateWidgets(clean, MetricByID, NoBranch); err != nil {
		return nil, err
	}
	if err := s.widgetStore.SaveProfileWidgets(ctx, profile, clean, by); err != nil {
		return nil, err
	}
	return clean, nil
}

// -- the Needs-you queue -------------------------------------------------------------------------

// needsPolicy reads the dealer's source policy for a job shape, degrading TO THE SHIPPED DEFAULTS
// rather than to nothing (the hotkey posture). An EMPTY answer is respected as the answer: a dealer
// who curated every source off a shape decided that, and it is not the same fact as a table that
// would not read.
func (s *Service) needsPolicy(ctx context.Context, profile string) []NeedsPolicy {
	if s.needsStore == nil {
		return defaultNeedsFor(profile)
	}
	rows, err := s.needsStore.NeedsSources(ctx, profile)
	if err != nil {
		s.warn("dashboard: needs-source policy unreadable; serving the shipped defaults",
			"profile", profile, "err", err)
		return defaultNeedsFor(profile)
	}
	return rows
}

// NeedsFor assembles the caller's Needs-you queue.
//
// EVERY ROW IS OPTIONAL AND THE QUEUE ALWAYS ANSWERS. A source with no adapter, a failing adapter, a
// panicking adapter, an unknown key, a zero count: the row is absent and the rest of the queue is
// returned. There is no error return on this function at all, which is the strongest way to say that
// a read path degrades downward, a caller cannot accidentally propagate a failure that does not
// exist.
func (s *Service) NeedsFor(ctx context.Context, scope Scope) NeedsQueue {
	if scope.Now.IsZero() {
		scope.Now = s.now()
	}
	if scope.Profile == "" {
		scope.Profile = s.ProfileFor(ctx, scope.RoleCode)
	}
	policy := append([]NeedsPolicy(nil), s.needsPolicy(ctx, scope.Profile)...)
	sortNeedsPolicy(policy)

	out := NeedsQueue{Profile: scope.Profile, Rows: []NeedsRow{}, Worst: SeverityGood}
	for _, p := range policy {
		if !p.Visible {
			continue
		}
		meta, known := NeedsSourceByKey[p.SourceKey]
		if !known {
			continue // a key this build does not have renders nothing, silently (the 0172 posture)
		}
		if meta.ManagerOnly && !scope.CostAllowed {
			continue // BYTE-ABSENT, and curation cannot grant it: the catalog decides, not the row
		}
		src, wired := s.needsSource(p.SourceKey)
		if !wired {
			continue // an unwired seam is an ABSENT row, and the rest of the queue still answers
		}
		m, err := s.measureNeeds(ctx, src, scope)
		if err != nil {
			s.warn("dashboard: needs source failed; the row is omitted",
				"source", p.SourceKey, "module", meta.Module, "err", err)
			continue
		}
		if m.Count <= 0 {
			continue // nothing is waiting: no row, and certainly not a zero in a big font
		}
		age := 0
		if !m.Oldest.IsZero() && m.Oldest.Before(scope.Now) {
			age = int(scope.Now.Sub(m.Oldest) / time.Minute)
		}
		out.Rows = append(out.Rows, NeedsRow{
			SourceKey: meta.Key, What: meta.What, Count: m.Count,
			Age: HumanAge(age), AgeMinutes: age, Severity: SeverityFor(m.Count, age, p),
			Module: meta.Module, Unit: meta.Unit, Screen: meta.Screen,
			View: meta.View, Action: meta.Action,
		})
		out.Total += m.Count
	}
	out.Worst = worstOf(out.Rows)
	return out
}

// measureNeeds runs one source under its own budget and survives a panic, the same contract a tile
// provider and a series provider get, because a queue row is written by the same kind of adapter
// over the same kind of module.
func (s *Service) measureNeeds(ctx context.Context, src NeedsSource, scope Scope) (m NeedsMeasure, err error) {
	defer func() {
		if r := recover(); r != nil {
			m, err = NeedsMeasure{}, errNeedsPanic(r)
		}
	}()
	cctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	return src.Needs(cctx, scope)
}

func (s *Service) warn(msg string, args ...any) {
	if s.log != nil {
		s.log.Warn(msg, args...)
	}
}

func errUnknownNeedsSource(k string) error {
	return fmt.Errorf("dashboard: no needs source %q in the catalog", k)
}
func errNilNeedsSource(k string) error {
	return fmt.Errorf("dashboard: nil needs source for %q", k)
}
func errNeedsPanic(r any) error {
	return fmt.Errorf("dashboard: needs source panicked: %v", r)
}
