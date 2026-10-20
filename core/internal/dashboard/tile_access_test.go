package dashboard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// - HH-777: A TILE'S ACCESS RULE IS CHECKED WHEN A LAYOUT IS SAVED AND WHEN IT IS RESOLVED -
//
// PUT /api/v1/dashboard/layout stores any catalog tile, and Resolve measures every visible one, so a
// counter staffer who named payments.today in their own layout read their branches' payment total.
// The layout is a wish; the money tile's rule (the payment trail) is the authority.

func denyAll(context.Context, Scope) (bool, error)  { return false, nil }
func allowAll(context.Context, Scope) (bool, error) { return true, nil }

func paymentsProvider(calls *atomic.Int32) MetricProvider {
	return MetricFunc(func(context.Context, Scope) (Value, error) {
		calls.Add(1)
		return Value{Number: 124000}, nil
	})
}

func TestTileAccess_PaymentsTodayNamesThePaymentTrailRule(t *testing.T) {
	if got := CatalogByID[TilePaymentsToday].Access; got != AccessPaymentTrail {
		t.Fatalf("payments.today must name the payment trail rule, got %q", got)
	}
	for _, m := range Catalog {
		if m.ID != TilePaymentsToday && m.Access != "" {
			t.Fatalf("%s names an access rule nobody asked for: %q", m.ID, m.Access)
		}
	}
}

// MUTATION TARGET: store the layout without asking the rule and this dies.
func TestTileAccess_SaveRefusesAGatedTileTheCallerFailsAndStoresNothing(t *testing.T) {
	f := newFakeStore()
	svc := NewService(f, nil).WithTileAccess(AccessPaymentTrail, denyAll)
	rows := []LayoutRow{{TileID: TileSalesToday, Position: 0, Visible: true}, {TileID: TilePaymentsToday, Position: 1, Visible: true}}

	_, err := svc.SaveMyLayout(context.Background(), Scope{UserID: "counter-1"}, rows)
	if !errors.Is(err, ErrTileForbidden) {
		t.Fatalf("saving payments.today visible without the trail must be refused, got %v", err)
	}
	if len(f.user["counter-1"]) != 0 {
		t.Fatalf("a refused layout stores nothing, got %+v", f.user["counter-1"])
	}
}

// Naming the tile hidden is not a measurement: the editor always names everything, hides included.
func TestTileAccess_SaveAllowsAGatedTileNamedHidden(t *testing.T) {
	f := newFakeStore()
	svc := NewService(f, nil).WithTileAccess(AccessPaymentTrail, denyAll)
	rows := []LayoutRow{{TileID: TileSalesToday, Position: 0, Visible: true}, {TileID: TilePaymentsToday, Position: 1, Visible: false}}
	if _, err := svc.SaveMyLayout(context.Background(), Scope{UserID: "counter-1"}, rows); err != nil {
		t.Fatalf("a hidden gated tile is fine: %v", err)
	}
}

func TestTileAccess_SaveAllowsTheTileToACallerThePassesTheRule(t *testing.T) {
	f := newFakeStore()
	svc := NewService(f, nil).WithTileAccess(AccessPaymentTrail, allowAll)
	rows := []LayoutRow{{TileID: TilePaymentsToday, Position: 0, Visible: true}}
	if _, err := svc.SaveMyLayout(context.Background(), Scope{UserID: "mgr-1"}, rows); err != nil {
		t.Fatalf("a trail holder saves it: %v", err)
	}
}

// MUTATION TARGET: measure every visible tile and this dies; so does a stale layout saved earlier.
func TestTileAccess_ResolveDoesNotMeasureAGatedTileTheCallerFails(t *testing.T) {
	f := newFakeStore()
	// A layout already stored (saved before the rule, or by an admin profile row) names the tile.
	f.user["counter-1"] = []LayoutRow{{TileID: TilePaymentsToday, Position: 0, Visible: true}}
	var calls atomic.Int32
	svc := NewService(f, nil).WithTileAccess(AccessPaymentTrail, denyAll)
	svc.MustRegister(TilePaymentsToday, paymentsProvider(&calls))

	l, err := svc.Resolve(context.Background(), Scope{UserID: "counter-1"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	tile, ok := tileByID(l, TilePaymentsToday)
	if !ok {
		t.Fatal("the tile stays in the catalog the editor sees")
	}
	if calls.Load() != 0 || tile.Number != nil || tile.State != StateHidden || tile.Visible {
		t.Fatalf("a caller without the trail must not have the tile measured: calls=%d tile=%+v", calls.Load(), tile)
	}
}

func TestTileAccess_ResolveMeasuresTheTileForACallerThePassesTheRule(t *testing.T) {
	f := newFakeStore()
	f.user["mgr-1"] = []LayoutRow{{TileID: TilePaymentsToday, Position: 0, Visible: true}}
	var calls atomic.Int32
	svc := NewService(f, nil).WithTileAccess(AccessPaymentTrail, allowAll)
	svc.MustRegister(TilePaymentsToday, paymentsProvider(&calls))
	l, _ := svc.Resolve(context.Background(), Scope{UserID: "mgr-1"})
	if tile, _ := tileByID(l, TilePaymentsToday); tile.Number == nil || *tile.Number != 124000 {
		t.Fatalf("a trail holder reads the tile: %+v", tile)
	}
}

// Fail closed: a rule that is not bound, and a rule that errors, refuse.
func TestTileAccess_UnboundAndErroringRulesRefuse(t *testing.T) {
	for name, svc := range map[string]*Service{
		"unbound": NewService(newFakeStore(), nil),
		"errors":  NewService(newFakeStore(), nil).WithTileAccess(AccessPaymentTrail, func(context.Context, Scope) (bool, error) { return true, errors.New("roles unreadable") }),
	} {
		f := svc.store.(*fakeStore)
		f.user["u"] = []LayoutRow{{TileID: TilePaymentsToday, Position: 0, Visible: true}}
		var calls atomic.Int32
		svc.MustRegister(TilePaymentsToday, paymentsProvider(&calls))
		l, _ := svc.Resolve(context.Background(), Scope{UserID: "u"})
		if tile, _ := tileByID(l, TilePaymentsToday); calls.Load() != 0 || tile.Number != nil {
			t.Fatalf("%s: the tile must not be measured: calls=%d %+v", name, calls.Load(), tile)
		}
		if _, err := svc.SaveMyLayout(context.Background(), Scope{UserID: "u"}, []LayoutRow{{TileID: TilePaymentsToday, Visible: true}}); !errors.Is(err, ErrTileForbidden) {
			t.Fatalf("%s: saving must be refused, got %v", name, err)
		}
	}
}

// An ungated tile is untouched by the machinery.
func TestTileAccess_UngatedTilesAreUnaffected(t *testing.T) {
	f := newFakeStore()
	f.user["u"] = []LayoutRow{{TileID: TileSalesToday, Position: 0, Visible: true}}
	svc := NewService(f, nil).WithTileAccess(AccessPaymentTrail, denyAll)
	svc.MustRegister(TileSalesToday, MetricFunc(func(context.Context, Scope) (Value, error) { return Value{Number: 5}, nil }))
	l, _ := svc.Resolve(context.Background(), Scope{UserID: "u"})
	if tile, _ := tileByID(l, TileSalesToday); tile.Number == nil || *tile.Number != 5 {
		t.Fatalf("sales.today needs no rule: %+v", tile)
	}
}

// The refusal is the caller's, a 403, never the 500 of an unclassified error.
func TestTileAccess_ForbiddenAnswers403(t *testing.T) {
	rec := httptest.NewRecorder()
	(&Handler{}).fail(rec, ErrTileForbidden)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", rec.Code)
	}
}
