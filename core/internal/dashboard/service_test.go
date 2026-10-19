package dashboard

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeStore is an in-memory Store. Every layout rule can therefore be exercised without a
// database; the SQL itself is proved in integration_test.go.
type fakeStore struct {
	roleProfile map[string]string
	profile     map[string][]LayoutRow
	user        map[string][]LayoutRow
	home        map[string]HomeConfig
	// DIB-433 pinned tabs, the same two layers over a different key.
	profileTabs map[string][]TabPin
	userTabs    map[string][]TabPin
	// DIB-496 dealer hotkeys, one dealer-wide list, plus its own error knob so a hotkey-store
	// failure can be staged WITHOUT failing the tile reads (the degradations differ: tiles fail the
	// request, hotkeys fall back to the shipped defaults).
	hotkeys    []HotkeyBinding
	hotkeysErr error
	err        error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		roleProfile: map[string]string{},
		profile:     map[string][]LayoutRow{},
		user:        map[string][]LayoutRow{},
		home:        map[string]HomeConfig{},
		profileTabs: map[string][]TabPin{},
		userTabs:    map[string][]TabPin{},
	}
}

func (f *fakeStore) ProfileForRole(_ context.Context, role string) (string, error) {
	return f.roleProfile[role], f.err
}
func (f *fakeStore) ProfileTiles(_ context.Context, p string) ([]LayoutRow, error) {
	return f.profile[p], f.err
}
func (f *fakeStore) UserTiles(_ context.Context, u string) ([]LayoutRow, error) {
	return f.user[u], f.err
}
func (f *fakeStore) SaveUserTiles(_ context.Context, u string, rows []LayoutRow) error {
	f.user[u] = rows
	return f.err
}
func (f *fakeStore) ClearUserTiles(_ context.Context, u string) (int, error) {
	n := len(f.user[u])
	delete(f.user, u)
	return n, f.err
}
func (f *fakeStore) SaveProfileTiles(_ context.Context, p string, rows []LayoutRow, _ string) error {
	f.profile[p] = rows
	return f.err
}
func (f *fakeStore) Home(_ context.Context, p string) (HomeConfig, error) {
	return f.home[p], f.err
}
func (f *fakeStore) SaveHome(_ context.Context, p string, h HomeConfig, _ string) error {
	f.home[p] = h
	return f.err
}
func (f *fakeStore) ProfileTabs(_ context.Context, p string) ([]TabPin, error) {
	return f.profileTabs[p], f.err
}
func (f *fakeStore) UserTabs(_ context.Context, u string) ([]TabPin, error) {
	return f.userTabs[u], f.err
}
func (f *fakeStore) SaveUserTabs(_ context.Context, u string, rows []TabPin) error {
	f.userTabs[u] = rows
	return f.err
}
func (f *fakeStore) ClearUserTabs(_ context.Context, u string) (int, error) {
	n := len(f.userTabs[u])
	delete(f.userTabs, u)
	return n, f.err
}
func (f *fakeStore) SaveProfileTabs(_ context.Context, p string, rows []TabPin, _ string) error {
	f.profileTabs[p] = rows
	return f.err
}
func (f *fakeStore) Hotkeys(_ context.Context) ([]HotkeyBinding, error) {
	return f.hotkeys, f.hotkeysErr
}

// allVisible makes every catalog tile visible for a profile, so a degradation test is not silently
// passing because the tile it names happens to be hidden.
func allVisible(profile string, f *fakeStore) {
	rows := make([]LayoutRow, len(Catalog))
	for i, t := range Catalog {
		rows[i] = LayoutRow{TileID: t.ID, Position: i, Visible: true}
	}
	f.profile[profile] = rows
}

func tileByID(l Layout, id string) (Tile, bool) {
	for _, t := range l.Tiles {
		if t.ID == id {
			return t, true
		}
	}
	return Tile{}, false
}

// -- DIB-299: the degradation contract, one named subtest PER TILE ------------------------------
//
// The rule the whole substrate exists for: an unwired tile shows a labelled em-dash, never a zero.
// On the wire that is `state: "unwired"` and NO number field at all, a *int64 that is nil, so a
// client cannot accidentally read a Go zero value as a measurement.
func TestTileDegradesToLabelledEmDashWhenUnwired(t *testing.T) {
	for _, meta := range Catalog {
		t.Run(meta.ID, func(t *testing.T) {
			f := newFakeStore()
			allVisible(ProfileAdmin, f)
			svc := NewService(f, nil)
			// Wire EVERY tile except this one, so the test isolates one missing provider rather
			// than asserting against an empty registry.
			for _, other := range Catalog {
				if other.ID == meta.ID {
					continue
				}
				svc.MustRegister(other.ID, MetricFunc(func(context.Context, Scope) (Value, error) {
					return Value{Number: 7}, nil
				}))
			}
			l, err := svc.Resolve(context.Background(), Scope{UserID: "u1", Profile: ProfileAdmin})
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			got, ok := tileByID(l, meta.ID)
			if !ok {
				t.Fatalf("tile %q missing from the layout", meta.ID)
			}
			if got.State != StateUnwired {
				t.Fatalf("state = %q, want %q", got.State, StateUnwired)
			}
			if got.Number != nil {
				t.Fatalf("an unwired tile carried a number (%d), this is the fake zero the contract forbids", *got.Number)
			}
			if got.Note == "" {
				t.Fatal("an unwired tile must be LABELLED; the em-dash alone says nothing")
			}
			if !contains(got.Note, meta.Module) {
				t.Fatalf("note %q must name the owning module %q", got.Note, meta.Module)
			}
			// The rest of the cockpit still works.
			for _, other := range Catalog {
				if other.ID == meta.ID {
					continue
				}
				o, _ := tileByID(l, other.ID)
				if o.State != StateOK || o.Number == nil || *o.Number != 7 {
					t.Fatalf("one unwired tile degraded its neighbour %q (state %q)", other.ID, o.State)
				}
			}
		})
	}
}

// A provider that FAILS is a different sentence from a provider that is missing, and neither is a
// zero. Both draw an em-dash; only one of them is somebody's fault.
func TestFailingProviderDegradesWithoutNumberAndWithoutTakingTheRequestDown(t *testing.T) {
	f := newFakeStore()
	allVisible(ProfileAdmin, f)
	svc := NewService(f, nil)
	svc.MustRegister(TileSalesToday, MetricFunc(func(context.Context, Scope) (Value, error) {
		return Value{}, errors.New("pos is having an afternoon")
	}))
	svc.MustRegister(TileOrdersOpen, MetricFunc(func(context.Context, Scope) (Value, error) {
		return Value{Number: 3}, nil
	}))
	l, err := svc.Resolve(context.Background(), Scope{UserID: "u1", Profile: ProfileAdmin})
	if err != nil {
		t.Fatalf("a failing provider must not fail the request: %v", err)
	}
	sales, _ := tileByID(l, TileSalesToday)
	if sales.State != StateError || sales.Number != nil {
		t.Fatalf("failing tile = %+v, want state=error with no number", sales)
	}
	orders, _ := tileByID(l, TileOrdersOpen)
	if orders.State != StateOK || orders.Number == nil || *orders.Number != 3 {
		t.Fatalf("healthy neighbour = %+v, want 3", orders)
	}
}

// Providers are foreign code written in the composition root over nine other modules. One of them
// panicking must cost its own tile and nothing else.
func TestPanickingProviderIsContainedToItsOwnTile(t *testing.T) {
	f := newFakeStore()
	allVisible(ProfileAdmin, f)
	svc := NewService(f, nil)
	svc.MustRegister(TilePickQueue, MetricFunc(func(context.Context, Scope) (Value, error) {
		var m map[string]int
		m["boom"] = 1 // nil map write
		return Value{}, nil
	}))
	svc.MustRegister(TileOrdersOpen, MetricFunc(func(context.Context, Scope) (Value, error) {
		return Value{Number: 11}, nil
	}))
	l, err := svc.Resolve(context.Background(), Scope{UserID: "u1", Profile: ProfileAdmin})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	pick, _ := tileByID(l, TilePickQueue)
	if pick.State != StateError || pick.Number != nil {
		t.Fatalf("panicking tile = %+v, want state=error with no number", pick)
	}
	orders, _ := tileByID(l, TileOrdersOpen)
	if orders.Number == nil || *orders.Number != 11 {
		t.Fatal("a panicking provider took its neighbour down with it")
	}
}

// A provider that never returns must not hold the home screen open.
func TestSlowProviderTimesOutIntoStateError(t *testing.T) {
	f := newFakeStore()
	allVisible(ProfileAdmin, f)
	svc := NewService(f, nil).WithTimeout(30 * time.Millisecond)
	svc.MustRegister(TileARAging, MetricFunc(func(ctx context.Context, _ Scope) (Value, error) {
		<-ctx.Done()
		return Value{}, ctx.Err()
	}))
	start := time.Now()
	l, err := svc.Resolve(context.Background(), Scope{UserID: "u1", Profile: ProfileAdmin})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Resolve waited %v on a 30ms budget", elapsed)
	}
	ar, _ := tileByID(l, TileARAging)
	if ar.State != StateError || ar.Number != nil {
		t.Fatalf("timed-out tile = %+v, want state=error with no number", ar)
	}
}

// A hidden tile is not measured and makes no claim about its module.
func TestHiddenTileIsNeitherMeasuredNorDiagnosed(t *testing.T) {
	f := newFakeStore()
	f.profile[ProfileCounter] = []LayoutRow{
		{TileID: TileSalesToday, Position: 0, Visible: true},
		{TileID: TileARAging, Position: 1, Visible: false},
	}
	svc := NewService(f, nil)
	called := 0
	svc.MustRegister(TileARAging, MetricFunc(func(context.Context, Scope) (Value, error) {
		called++
		return Value{Number: 99}, nil
	}))
	l, err := svc.Resolve(context.Background(), Scope{UserID: "u1", Profile: ProfileCounter})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if called != 0 {
		t.Fatalf("a hidden tile cost %d provider call(s)", called)
	}
	ar, _ := tileByID(l, TileARAging)
	if ar.State != StateHidden || ar.Number != nil {
		t.Fatalf("hidden tile = %+v, want state=hidden with no number", ar)
	}
}

// Register refuses an id that is not in the catalog, a typo'd adapter must be loud at boot, not a
// permanent em-dash that looks like an outage.
func TestRegisterRefusesAnUnknownTileID(t *testing.T) {
	svc := NewService(newFakeStore(), nil)
	if err := svc.Register("orders.opne", MetricFunc(func(context.Context, Scope) (Value, error) {
		return Value{}, nil
	})); err == nil {
		t.Fatal("Register accepted a tile id that is not in the catalog")
	}
	if err := svc.Register(TileOrdersOpen, nil); err == nil {
		t.Fatal("Register accepted a nil provider")
	}
}

// -- DIB-353: role layout + reset-to-role, through the service ----------------------------------

func TestSaveThenResetMyLayoutReturnsToTheRoleDefault(t *testing.T) {
	f := newFakeStore()
	f.profile[ProfileCounter] = []LayoutRow{
		{TileID: TileSalesToday, Position: 0, Visible: true},
		{TileID: TileOrdersOpen, Position: 1, Visible: true},
	}
	svc := NewService(f, nil)
	ctx := context.Background()

	role, err := svc.Resolve(ctx, Scope{UserID: "u1", Profile: ProfileCounter})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if role.Source != SourceRole {
		t.Fatalf("source = %q, want %q", role.Source, SourceRole)
	}

	if _, err := svc.SaveMyLayout(ctx, Scope{UserID: "u1"}, []LayoutRow{
		{TileID: TileOrdersOpen, Position: 0, Visible: true},
		{TileID: TileSalesToday, Position: 1, Visible: false},
	}); err != nil {
		t.Fatalf("SaveMyLayout: %v", err)
	}
	mine, _ := svc.Resolve(ctx, Scope{UserID: "u1", Profile: ProfileCounter})
	if mine.Source != SourceUser {
		t.Fatalf("source = %q, want %q", mine.Source, SourceUser)
	}
	if mine.Tiles[0].ID != TileOrdersOpen {
		t.Fatalf("override did not take: first tile is %q", mine.Tiles[0].ID)
	}

	// Another user is untouched by u1's override, the override is per-user, not per-role.
	other, _ := svc.Resolve(ctx, Scope{UserID: "u2", Profile: ProfileCounter})
	if other.Source != SourceRole || other.Tiles[0].ID != TileSalesToday {
		t.Fatalf("u1's override leaked onto u2: %+v", other.Tiles[0])
	}

	n, err := svc.ResetMyLayout(ctx, "u1")
	if err != nil || n != 2 {
		t.Fatalf("ResetMyLayout = %d, %v; want 2, nil", n, err)
	}
	back, _ := svc.Resolve(ctx, Scope{UserID: "u1", Profile: ProfileCounter})
	if back.Source != SourceRole {
		t.Fatalf("after reset source = %q, want %q", back.Source, SourceRole)
	}
	for i := range role.Tiles {
		if back.Tiles[i].ID != role.Tiles[i].ID || back.Tiles[i].Visible != role.Tiles[i].Visible {
			t.Fatalf("reset landed on a different layout at %d: %+v vs %+v", i, back.Tiles[i], role.Tiles[i])
		}
	}
}

func TestSaveMyLayoutRefusesAnEmptyOrAllUnknownLayout(t *testing.T) {
	svc := NewService(newFakeStore(), nil)
	if _, err := svc.SaveMyLayout(context.Background(), Scope{UserID: "u1"}, nil); !errors.Is(err, ErrEmptyLayout) {
		t.Fatalf("empty layout error = %v, want ErrEmptyLayout", err)
	}
	if _, err := svc.SaveMyLayout(context.Background(), Scope{UserID: "u1"}, []LayoutRow{{TileID: "nope"}}); !errors.Is(err, ErrEmptyLayout) {
		t.Fatalf("all-unknown layout error = %v, want ErrEmptyLayout", err)
	}
	// Storing an empty override would be indistinguishable from having reset, so nothing was stored.
	if _, err := svc.ResetMyLayout(context.Background(), "u1"); err != nil {
		t.Fatalf("ResetMyLayout: %v", err)
	}
}

func TestProfileForUnmappedRoleFallsBackToCounter(t *testing.T) {
	f := newFakeStore()
	f.roleProfile["YARD_TEAM"] = ProfileYard
	svc := NewService(f, nil)
	if got := svc.ProfileFor(context.Background(), "YARD_TEAM"); got != ProfileYard {
		t.Fatalf("mapped role = %q, want %q", got, ProfileYard)
	}
	if got := svc.ProfileFor(context.Background(), "A_ROLE_ADDED_NEXT_WAVE"); got != ProfileCounter {
		t.Fatalf("unmapped role = %q, want %q", got, ProfileCounter)
	}
}

func TestSaveProfileLayoutRejectsAnUnknownProfile(t *testing.T) {
	svc := NewService(newFakeStore(), nil)
	_, err := svc.SaveProfileLayout(context.Background(), "warehouse-ninja",
		[]LayoutRow{{TileID: TileSalesToday}}, "admin")
	if !errors.Is(err, ErrUnknownProfile) {
		t.Fatalf("err = %v, want ErrUnknownProfile", err)
	}
}

// -- DIB-354: the takeover config ---------------------------------------------------------------

func TestSaveProfileHomeRefusesATakeoverWithNowhereToLand(t *testing.T) {
	svc := NewService(newFakeStore(), nil)
	err := svc.SaveProfileHome(context.Background(), ProfileDriver,
		HomeConfig{Takeover: true}, "admin")
	if !errors.Is(err, ErrNoLanding) {
		t.Fatalf("err = %v, want ErrNoLanding", err)
	}
	if err := svc.SaveProfileHome(context.Background(), ProfileDriver,
		HomeConfig{Takeover: true, LandingUnit: "dispatch", LandingModule: "dispatch", MobileOnly: true}, "admin"); err != nil {
		t.Fatalf("valid takeover rejected: %v", err)
	}
}

func TestResolveCarriesTheHomeConfig(t *testing.T) {
	f := newFakeStore()
	f.home[ProfileDriver] = HomeConfig{LandingUnit: "dispatch", LandingModule: "dispatch", Takeover: true, MobileOnly: true}
	svc := NewService(f, nil)
	l, err := svc.Resolve(context.Background(), Scope{UserID: "d1", Profile: ProfileDriver})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !l.Home.Takeover || l.Home.LandingUnit != "dispatch" || l.Home.Profile != ProfileDriver {
		t.Fatalf("home = %+v", l.Home)
	}
}

// The scope a provider receives must be the one the caller was resolved with, a provider that
// honours BranchIDs can only do so if it actually gets them.
func TestProvidersReceiveTheCallerScope(t *testing.T) {
	f := newFakeStore()
	f.profile[ProfileCounter] = []LayoutRow{{TileID: TileSalesToday, Position: 0, Visible: true}}
	svc := NewService(f, nil)
	var seen Scope
	svc.MustRegister(TileSalesToday, MetricFunc(func(_ context.Context, sc Scope) (Value, error) {
		seen = sc
		return Value{Number: 1}, nil
	}))
	want := Scope{UserID: "u9", RoleCode: "INSIDE_SALES", Profile: ProfileCounter, BranchIDs: []string{"b1", "b2"}}
	if _, err := svc.Resolve(context.Background(), want); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if seen.UserID != "u9" || len(seen.BranchIDs) != 2 || seen.AllBranches {
		t.Fatalf("provider scope = %+v", seen)
	}
	if seen.Now.IsZero() {
		t.Fatal("Scope.Now must be filled in for providers that need 'today'")
	}
}

func contains(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
