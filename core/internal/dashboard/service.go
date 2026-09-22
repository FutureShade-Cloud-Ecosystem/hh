package dashboard

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// providerTimeout bounds ONE provider. Ten tiles behind one home screen means ten chances for a
// slow query to hold the page; each gets its own budget and a blown budget is StateError, which
// draws an em-dash with "unavailable" under it. The cockpit renders in bounded time or it says so.
const providerTimeout = 3 * time.Second

// Request-level sentinels. The handler maps these to 4xx; anything else is a 500, so a new
// internal failure can never be mistaken for the caller's fault.
var (
	// ErrUnknownProfile, a caller named a layout profile outside the closed set.
	ErrUnknownProfile = errors.New("unknown dashboard profile")
	// ErrEmptyLayout, a submitted layout named no tile this build knows about.
	ErrEmptyLayout = errors.New("a layout must name at least one known tile")
	// ErrEmptyTabs, a submitted pin set named no well-formed surface. See SaveMyTabs for why an
	// empty personal override is refused while an empty ROLE default is allowed.
	ErrEmptyTabs = errors.New("a pinned-tab override must name at least one surface")
	// ErrNoLanding, a takeover was configured with nowhere to land.
	ErrNoLanding = errors.New("a takeover must name a landing unit")
	// ErrNoCaller, the guard resolved no user id (should be impossible behind requireStaff).
	ErrNoCaller = errors.New("no caller")
)

// Store is the persistence seam for layouts. Declared here and satisfied by *Repository (pgx) in
// this package and by a fake in the tests, so every layout rule above can be exercised without a
// database while the integration tests still prove the SQL.
type Store interface {
	ProfileForRole(ctx context.Context, roleCode string) (string, error)
	ProfileTiles(ctx context.Context, profile string) ([]LayoutRow, error)
	UserTiles(ctx context.Context, userID string) ([]LayoutRow, error)
	SaveUserTiles(ctx context.Context, userID string, rows []LayoutRow) error
	ClearUserTiles(ctx context.Context, userID string) (int, error)
	SaveProfileTiles(ctx context.Context, profile string, rows []LayoutRow, by string) error
	Home(ctx context.Context, profile string) (HomeConfig, error)
	SaveHome(ctx context.Context, profile string, h HomeConfig, by string) error
	// -- DIB-433 pinned tabs (0149). Same five verbs as the tile half, because it is the same
	// curation model over a different key. --
	ProfileTabs(ctx context.Context, profile string) ([]TabPin, error)
	UserTabs(ctx context.Context, userID string) ([]TabPin, error)
	SaveUserTabs(ctx context.Context, userID string, rows []TabPin) error
	ClearUserTabs(ctx context.Context, userID string) (int, error)
	SaveProfileTabs(ctx context.Context, profile string, rows []TabPin, by string) error
	// -- DIB-496 dealer hotkeys (0164). One read, dealer-wide, no per-user verbs, because the
	// ruling (D-095) is that the map is company-level configuration, not preference. --
	Hotkeys(ctx context.Context) ([]HotkeyBinding, error)
}

// Service resolves layouts and gathers measurements.
type Service struct {
	store Store
	log   *slog.Logger

	mu        sync.RWMutex
	providers map[string]MetricProvider
	// series holds the composed-metric catalog's providers (viz.go). A separate map from
	// `providers` because a metric and a tile are different things: a tile is one number, a metric
	// is a number that can be split by a dimension, and several tiles measure no metric at all.
	series map[string]SeriesProvider
	// needs holds the Needs-you queue's source adapters (needs.go). A third map rather than a third
	// use of `providers`, because a queue source answers a COUNT AND AN AGE for one person, which is
	// neither a tile's one number nor a metric's series.
	needs map[string]NeedsSource
	// metricStore reads the dealer's metric curation. Optional, nil means "not curated", which
	// resolves to the shipped catalog (see Service.curation).
	metricStore MetricCurationStore
	// widgetStore is the widget substrate (0201). Optional: a build ahead of the migration serves an
	// empty widget layout and a working Home rather than an error.
	widgetStore WidgetStore
	// needsStore reads the dealer's Needs-you source policy. Optional, nil, or unreadable, resolves
	// to DefaultNeedsSources.
	needsStore NeedsPolicyStore
	// galleryStore reads and writes the "Add widget" gallery (0208). Optional: a build between 0201
	// and 0208 serves an empty gallery, so "Add widget" falls through to the builder, which is
	// exactly what everybody had before the gallery existed.
	galleryStore GalleryStore
	// branchResolver answers "is this a real branch" for the widget write gate (widget_service.go,
	// BranchResolver). Optional, and nil is TODAY'S BEHAVIOUR EXACTLY: the four gates 7027df5 closed
	// still run, and an id that survives them reaches Postgres as it did before. It is a field on
	// Service rather than a fifth argument to ValidateWidgets because ValidateWidgets is a pure
	// function over a caller's grants, every test in this package calls it directly, and the
	// existence check needs a context and a table, which is a service's business and not a
	// validator's.
	branchResolver BranchResolver

	// now is injectable so a test can pin "today" without touching the machine clock.
	now func() time.Time
	// timeout is the per-provider budget; overridable in tests.
	timeout time.Duration
}

func NewService(store Store, log *slog.Logger) *Service {
	return &Service{
		store:     store,
		log:       log,
		providers: map[string]MetricProvider{},
		series:    map[string]SeriesProvider{},
		needs:     map[string]NeedsSource{},
		now:       time.Now,
		timeout:   providerTimeout,
	}
}

// WithClock pins the clock (tests).
func (s *Service) WithClock(now func() time.Time) *Service { s.now = now; return s }

// WithTimeout overrides the per-provider budget (tests).
func (s *Service) WithTimeout(d time.Duration) *Service { s.timeout = d; return s }

// Register binds a provider to a catalog tile. Called ONLY from the composition root.
//
// Registering an id that is not in the catalog is refused rather than ignored: a typo'd tile id
// would otherwise be a silently dead adapter, and the symptom (one tile permanently showing an
// em-dash) looks exactly like a module being down. Returning the error lets main.go log the
// difference at boot.
func (s *Service) Register(tileID string, p MetricProvider) error {
	if _, ok := CatalogByID[tileID]; !ok {
		return fmt.Errorf("dashboard: no catalog tile %q", tileID)
	}
	if p == nil {
		return fmt.Errorf("dashboard: nil provider for %q", tileID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.providers[tileID] = p
	return nil
}

// MustRegister is Register for a composition root that would rather fail loudly at boot.
func (s *Service) MustRegister(tileID string, p MetricProvider) {
	if err := s.Register(tileID, p); err != nil {
		panic(err)
	}
}

// WiredTiles lists the tile ids that currently have a provider, used by the boot log and by the
// admin editor, which marks unwired tiles so an admin does not put a permanent em-dash at the top
// of a role's home screen without knowing.
func (s *Service) WiredTiles() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.providers))
	for _, t := range Catalog { // catalog order, not map order
		if _, ok := s.providers[t.ID]; ok {
			out = append(out, t.ID)
		}
	}
	return out
}

func (s *Service) provider(tileID string) (MetricProvider, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.providers[tileID]
	return p, ok
}

// ProfileFor resolves a role code to a layout profile, falling back to ProfileCounter when the
// role is unmapped. The fallback is deliberate and it is the least-privileged useful home: a role
// nobody has classified yet gets the counter tiles rather than a blank page or a manager's view.
func (s *Service) ProfileFor(ctx context.Context, roleCode string) string {
	if s.store == nil || roleCode == "" {
		return ProfileCounter
	}
	p, err := s.store.ProfileForRole(ctx, roleCode)
	if err != nil {
		if s.log != nil {
			s.log.Warn("dashboard: role→profile lookup failed; defaulting to counter",
				"role", roleCode, "err", err)
		}
		return ProfileCounter
	}
	if !IsProfile(p) {
		return ProfileCounter
	}
	return p
}

// Resolve builds the caller's home: layout first, then measurements for the VISIBLE tiles only.
//
// Hidden tiles are still returned (the editor needs them) but are never measured, a tile nobody
// is looking at must not cost a query.
func (s *Service) Resolve(ctx context.Context, scope Scope) (Layout, error) {
	if scope.Now.IsZero() {
		scope.Now = s.now()
	}
	if scope.Profile == "" {
		scope.Profile = s.ProfileFor(ctx, scope.RoleCode)
	}

	var profileRows, userRows []LayoutRow
	if s.store != nil {
		var err error
		if profileRows, err = s.store.ProfileTiles(ctx, scope.Profile); err != nil {
			return Layout{}, err
		}
		if scope.UserID != "" {
			if userRows, err = s.store.UserTiles(ctx, scope.UserID); err != nil {
				return Layout{}, err
			}
		}
	}

	slots, source := ResolveLayout(Catalog, CatalogByID, profileRows, userRows)
	// DIB-500: a tile that is default-present for this job shape, and that the decisive layer is
	// SILENT about, flips visible. A layer that names it, the editor always names everything -
	// is honoured as stored, which is what keeps an explicit removal removed.
	slots = ApplyDefaultOn(slots, DecisiveLayer(profileRows, userRows), scope.Profile)
	tiles := make([]Tile, len(slots))
	for i, sl := range slots {
		tiles[i] = Tile{TileMeta: sl.Meta, Position: sl.Position, Visible: sl.Visible, State: StateUnwired}
	}

	// Measure the visible tiles concurrently. Each provider gets its own derived context so one
	// slow module cannot spend another's budget, and each writes only its own slot, so no lock is
	// needed on the slice.
	var wg sync.WaitGroup
	for i := range tiles {
		if !tiles[i].Visible {
			tiles[i].State, tiles[i].Note = StateHidden, ""
			continue
		}
		p, ok := s.provider(tiles[i].ID)
		if !ok {
			tiles[i].Note = unwiredNote(tiles[i].Module)
			continue
		}
		wg.Add(1)
		go func(idx int, p MetricProvider) {
			defer wg.Done()
			v, err := s.measure(ctx, p, scope)
			t := &tiles[idx]
			if err != nil {
				t.State = StateError
				t.Note = "Temporarily unavailable"
				if s.log != nil {
					s.log.Warn("dashboard: provider failed", "tile", t.ID, "module", t.Module, "err", err)
				}
				return
			}
			n := v.Number
			t.State, t.Number, t.Hint, t.Parts, t.AsOf = StateOK, &n, v.Hint, v.Parts, v.AsOf
		}(i, p)
	}
	wg.Wait()

	home, err := s.HomeFor(ctx, scope.Profile)
	if err != nil {
		return Layout{}, err
	}
	// DIB-433: the pinned strip rides the same response. Cheap, two indexed reads of a handful of
	// rows, and it is the only way the strip can be correct in the FIRST paint after login.
	strip, err := s.TabsFor(ctx, scope.Profile, scope.UserID)
	if err != nil {
		return Layout{}, err
	}
	return Layout{
		Profile: scope.Profile, Source: source, Tiles: tiles, Home: home, Tabs: strip,
		// DIB-496: the dealer's key map, on the one request the shell already makes.
		Hotkeys: s.hotkeys(ctx),
	}, nil
}

// hotkeys reads the shell key map (DIB-496), degrading exactly the way the quick-action config
// does and for the same reason: TO THE SHIPPED DEFAULT, NOT TO DEAD KEYS. An unreadable store (a
// build ahead of 0164, a store blip) must not take away the F1 the counter had yesterday, so the
// failure is logged and the seeded defaults are served. An EMPTY answer is the opposite case, a
// decision to have no hotkeys, and empty is respected as the answer. The layout itself never
// fails over this: hotkeys are optional chrome, and a home screen that 500s because a key map
// would not load has its priorities backwards.
//
// D-111 LEFT THIS FUNCTION ALONE, ON PURPOSE. The tier the ruling added, the platform roster a
// dealer with no override inherits, is resolved one layer down, in Repository.Hotkeys, so the two
// cases above keep meaning precisely what they meant: the store either could not answer, or
// answered that nobody has bound anything at any level. Putting the tier here instead would have
// changed the meaning of both, to buy nothing.
func (s *Service) hotkeys(ctx context.Context) []HotkeyBinding {
	if s.store == nil {
		return NormalizeHotkeys(DefaultHotkeys)
	}
	rows, err := s.store.Hotkeys(ctx)
	if err != nil {
		if s.log != nil {
			s.log.Warn("dashboard: hotkey lookup failed; falling back to shipped defaults", "err", err)
		}
		return NormalizeHotkeys(DefaultHotkeys)
	}
	return NormalizeHotkeys(rows)
}

// TabsFor resolves the caller's pinned strip: their override if they have one, else their role's
// default, else nothing. See ResolveTabs for why "nothing" is a real answer here and not a bug.
func (s *Service) TabsFor(ctx context.Context, profile, userID string) (TabStrip, error) {
	if s.store == nil {
		return TabStrip{Source: SourceCatalog, Pins: []TabPin{}}, nil
	}
	profileRows, err := s.store.ProfileTabs(ctx, profile)
	if err != nil {
		return TabStrip{}, err
	}
	var userRows []TabPin
	if userID != "" {
		if userRows, err = s.store.UserTabs(ctx, userID); err != nil {
			return TabStrip{}, err
		}
	}
	pins, source := ResolveTabs(profileRows, userRows)
	return TabStrip{Source: source, Pins: pins}, nil
}

// SaveMyTabs stores a per-user pin override. The body is the WHOLE set, an override is whole or
// absent, and a submission that survives normalisation to nothing is refused for exactly the
// reason SaveMyLayout refuses one: stored, it would be indistinguishable from no override, so the
// person's next login would silently hand them the role default back. A person who wants an EMPTY
// strip submits their role's pins with visible=false, which is a real override and stays one.
func (s *Service) SaveMyTabs(ctx context.Context, userID string, rows []TabPin) ([]TabPin, error) {
	if userID == "" {
		return nil, ErrNoCaller
	}
	clean := NormalizeTabs(rows)
	if len(clean) == 0 {
		return nil, ErrEmptyTabs
	}
	if err := s.store.SaveUserTabs(ctx, userID, clean); err != nil {
		return nil, err
	}
	return clean, nil
}

// ResetMyTabs drops the caller's pin override so the role default takes over again.
func (s *Service) ResetMyTabs(ctx context.Context, userID string) (int, error) {
	if userID == "" {
		return 0, ErrNoCaller
	}
	return s.store.ClearUserTabs(ctx, userID)
}

// ProfileTabsFor returns a role default's pins as stored (admin).
func (s *Service) ProfileTabsFor(ctx context.Context, profile string) ([]TabPin, error) {
	if !IsProfile(profile) {
		return nil, ErrUnknownProfile
	}
	rows, err := s.store.ProfileTabs(ctx, profile)
	if err != nil {
		return nil, err
	}
	pins, _ := ResolveTabs(rows, nil)
	return pins, nil
}

// SaveProfileTabs stores a role default's pins (admin). A role default MAY be empty, an admin
// deciding a job shape needs no permanent tabs is a decision, not an accident, and unlike a user
// override there is no lower layer for "empty" to be confused with.
func (s *Service) SaveProfileTabs(ctx context.Context, profile string, rows []TabPin, by string) ([]TabPin, error) {
	if !IsProfile(profile) {
		return nil, ErrUnknownProfile
	}
	clean := NormalizeTabs(rows)
	if err := s.store.SaveProfileTabs(ctx, profile, clean, by); err != nil {
		return nil, err
	}
	return clean, nil
}

// measure runs one provider under its own timeout, and survives a provider that panics.
//
// The recover is not defensive paranoia: providers are adapters written in the composition root
// over nine other modules, and a nil map or a short slice in one of them would otherwise take the
// whole home screen down with a 500. A panicking tile degrades exactly like a failing one.
func (s *Service) measure(ctx context.Context, p MetricProvider, scope Scope) (v Value, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("dashboard: provider panicked: %v", r)
		}
	}()
	cctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	return p.Measure(cctx, scope)
}

func unwiredNote(module string) string {
	if module == "" {
		return "Not connected"
	}
	return "Not connected (" + module + ")"
}

// HomeFor returns a profile's landing configuration (DIB-354). A profile with no stored row lands
// on the dashboard home, which is the correct default for every desk role.
func (s *Service) HomeFor(ctx context.Context, profile string) (HomeConfig, error) {
	if s.store == nil {
		return HomeConfig{Profile: profile}, nil
	}
	h, err := s.store.Home(ctx, profile)
	if err != nil {
		return HomeConfig{}, err
	}
	h.Profile = profile
	return h, nil
}

// SaveMyLayout stores a per-user override.
func (s *Service) SaveMyLayout(ctx context.Context, userID string, rows []LayoutRow) ([]LayoutRow, error) {
	if userID == "" {
		return nil, ErrNoCaller
	}
	clean := NormalizeRows(CatalogByID, rows)
	if len(clean) == 0 {
		// An empty override is indistinguishable from no override once stored, and storing it
		// would mean "reset to role" silently already happened. Refuse; the client that wants a
		// reset calls DELETE.
		return nil, ErrEmptyLayout
	}
	if err := s.store.SaveUserTiles(ctx, userID, clean); err != nil {
		return nil, err
	}
	return clean, nil
}

// ResetMyLayout drops the caller's override so the role default takes over again.
func (s *Service) ResetMyLayout(ctx context.Context, userID string) (int, error) {
	if userID == "" {
		return 0, ErrNoCaller
	}
	return s.store.ClearUserTiles(ctx, userID)
}

// SaveProfileLayout stores a role default (admin).
func (s *Service) SaveProfileLayout(ctx context.Context, profile string, rows []LayoutRow, by string) ([]LayoutRow, error) {
	if !IsProfile(profile) {
		return nil, ErrUnknownProfile
	}
	clean := NormalizeRows(CatalogByID, rows)
	if len(clean) == 0 {
		return nil, ErrEmptyLayout
	}
	if err := s.store.SaveProfileTiles(ctx, profile, clean, by); err != nil {
		return nil, err
	}
	return clean, nil
}

// ProfileLayout returns a role default as stored, resolved against the catalog so the editor sees
// newly-shipped tiles too.
func (s *Service) ProfileLayout(ctx context.Context, profile string) ([]Slot, HomeConfig, error) {
	if !IsProfile(profile) {
		return nil, HomeConfig{}, ErrUnknownProfile
	}
	rows, err := s.store.ProfileTiles(ctx, profile)
	if err != nil {
		return nil, HomeConfig{}, err
	}
	slots, _ := ResolveLayout(Catalog, CatalogByID, rows, nil)
	// DIB-500: the editor shows the layout this profile's people actually get, default-on applied -
	// otherwise an admin reads "hidden" for a card every manager can see, and "fixes" it into a
	// duplicate decision.
	slots = ApplyDefaultOn(slots, rows, profile)
	h, err := s.HomeFor(ctx, profile)
	if err != nil {
		return nil, HomeConfig{}, err
	}
	return slots, h, nil
}

// SaveProfileHome stores a profile's landing/takeover configuration (admin).
func (s *Service) SaveProfileHome(ctx context.Context, profile string, h HomeConfig, by string) error {
	if !IsProfile(profile) {
		return ErrUnknownProfile
	}
	// A takeover with nowhere to land is a blank screen. Refuse it here rather than discover it
	// on a driver's phone at 6am.
	if h.Takeover && h.LandingUnit == "" {
		return ErrNoLanding
	}
	return s.store.SaveHome(ctx, profile, h, by)
}
