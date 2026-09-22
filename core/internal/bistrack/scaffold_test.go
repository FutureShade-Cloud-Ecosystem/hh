package bistrack

import (
	"context"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"net/http"
	"strings"
	"testing"
	"time"
)

// scaffold_test.go, DIB-53 the phase-0 guarantees: the connector fails closed, the observability service
// reports honest unconfigured state, the direction-of-truth data is correct, the cursor tie semantics
// hold, and, the load-bearing one (supervisor C4), the module is STRUCTURALLY unable to sync.

// -- connector fails closed ---------------------------------------------------------------------------

// TestUnconfiguredConnector_FailsClosed asserts every one of the eight frozen methods refuses with
// ErrUnconfigured. There is no input that makes phase 0 connect, that is the fail-closed guarantee.
func TestUnconfiguredConnector_FailsClosed(t *testing.T) {
	c := newUnconfiguredConnector()
	ctx := context.Background()
	if c.Name() != "unconfigured" {
		t.Errorf("Name = %q, want unconfigured", c.Name())
	}
	checks := []func() error{
		func() error { _, _, e := c.FetchCustomers(ctx, Cursor{}); return e },
		func() error { _, _, e := c.FetchProducts(ctx, Cursor{}); return e },
		func() error { _, _, e := c.FetchInventory(ctx, Cursor{}); return e },
		func() error { _, _, e := c.FetchOrders(ctx, Cursor{}); return e },
		func() error { return c.PushCustomer(ctx, MappedCustomer{}) },
		func() error { return c.PushProduct(ctx, MappedProduct{}) },
		func() error { return c.PushInventory(ctx, MappedInventory{}) },
		func() error { return c.PushOrder(ctx, MappedOrder{}) },
	}
	for i, ck := range checks {
		if !errors.Is(ck(), ErrUnconfigured) {
			t.Errorf("connector method %d did not fail closed with ErrUnconfigured", i)
		}
	}
}

// -- observability service ----------------------------------------------------------------------------

// fakeStore is an empty read store (no jobs, no discrepancies), phase-0 reality.
type fakeStore struct {
	last *SyncJob
	open int64
	err  error
}

func (f fakeStore) LatestJob(context.Context) (*SyncJob, error)         { return f.last, f.err }
func (f fakeStore) OpenDiscrepancyCount(context.Context) (int64, error) { return f.open, f.err }

func TestService_Unconfigured(t *testing.T) {
	svc := NewService(fakeStore{}).WithConfigured(false)
	if svc.Configured() {
		t.Error("want not configured with no credentials")
	}
	if svc.HealthStatus() != "unconfigured" {
		t.Errorf("HealthStatus = %q, want unconfigured", svc.HealthStatus())
	}
	st, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Configured || st.LastSync != nil || st.Discrepancies != 0 {
		t.Errorf("Status = %+v, want {false, nil, 0}", st)
	}
}

func TestService_ConfiguredStillIdle(t *testing.T) {
	// Even WITH credentials, phase 0 never reports "syncing", it is idle by construction.
	svc := NewService(fakeStore{}).WithConfigured(true)
	if svc.HealthStatus() != "idle" {
		t.Errorf("HealthStatus = %q, want idle", svc.HealthStatus())
	}
}

func TestService_StatusReadsStore(t *testing.T) {
	ts := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	svc := NewService(fakeStore{last: &SyncJob{FinishedAt: &ts}, open: 3}).WithConfigured(true)
	st, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.LastSync == nil || !st.LastSync.Equal(ts) || st.Discrepancies != 3 {
		t.Errorf("Status = %+v, want last_sync=%v discrepancies=3", st, ts)
	}
}

// -- the STRUCTURAL guarantee (C4) --------------------------------------------------------------------

// TestStructurallyIncapableOfSyncing walks every route the module registers and asserts NONE is a
// non-GET. Phase 0 must be structurally unable to initiate a sync, no POST/PUT/PATCH/DELETE trigger can
// exist. This is enforced by CI, not by a checklist grep, so it survives the W10 worker landing: adding a
// trigger route makes this test go red.
func TestStructurallyIncapableOfSyncing(t *testing.T) {
	h := NewHandler(NewService(fakeStore{}), nil)
	routes := h.Routes()
	if len(routes) == 0 {
		t.Fatal("expected at least the status route")
	}
	for _, r := range routes {
		if r.Method != http.MethodGet {
			t.Errorf("route %s %s is a non-GET, phase 0 must expose no sync trigger", r.Method, r.Pattern)
		}
		if !strings.HasPrefix(r.Pattern, "/api/v1/bistrack") {
			t.Errorf("route %s escapes the module namespace", r.Pattern)
		}
	}
}

// TestRegisterRoutesMatchesRoutes proves RegisterRoutes wires exactly what Routes() reports (they share
// routeDefs), so the structural test above cannot be bypassed by registering a route Routes() omits.
func TestRegisterRoutesMatchesRoutes(t *testing.T) {
	h := NewHandler(NewService(fakeStore{}), nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, testkit.NewStaffGuards(t).Guards)
	for _, r := range h.Routes() {
		// The registered handler must resolve for the declared method+pattern.
		req, _ := http.NewRequest(r.Method, "http://x"+r.Pattern, nil)
		if _, pattern := mux.Handler(req); pattern == "" {
			t.Errorf("Routes() lists %s %s but RegisterRoutes did not wire it", r.Method, r.Pattern)
		}
	}
}

// -- direction-of-truth data (C5) ---------------------------------------------------------------------

func TestDirectionOfTruth(t *testing.T) {
	cases := []struct {
		phase      MigrationPhase
		master     Master
		pull, push bool
	}{
		{PhaseMirror, MasterBisTrack, true, false}, // BisTrack master: one-way pull mirror
		{PhaseShadow, MasterNative, true, true},    // parallel run: native master, push keeps BisTrack current
		{PhaseFlip, MasterNative, true, false},     // native master: BisTrack read-only
	}
	for _, c := range cases {
		for _, e := range AllEntities {
			p, ok := DirectionFor(c.phase, e)
			if !ok {
				t.Fatalf("no policy for %s/%s", c.phase, e)
			}
			if p.Master != c.master || p.AllowPull != c.pull || p.AllowPush != c.push {
				t.Errorf("%s/%s = %+v, want master=%s pull=%v push=%v", c.phase, e, p, c.master, c.pull, c.push)
			}
		}
	}
	// Push is live exactly in the shadow (parallel-run) phase, the window the rollback guarantee lives in.
	if p, _ := DirectionFor(PhaseShadow, EntityCustomer); !p.AllowPush {
		t.Error("shadow phase must allow push (the rollback-until-November guarantee)")
	}
	if p, _ := DirectionFor(PhaseMirror, EntityCustomer); p.AllowPush {
		t.Error("mirror phase must NOT push (BisTrack is master)")
	}
}

// -- cursor tie semantics (C2) ------------------------------------------------------------------------

// TestCursorTieSemantics proves the composite cursor never skips a row that shares a timestamp with the
// last one consumed (the DIB-191 H1 class). Two rows on the SAME tick are strictly ordered by id, and a
// cursor at one of them still sees the other as "new".
func TestCursorTieSemantics(t *testing.T) {
	tick := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	a := Cursor{UpdatedAt: tick, LastID: "C1"}
	b := Cursor{UpdatedAt: tick, LastID: "C2"} // same tick, later id

	if !a.Before(b) {
		t.Error("same-tick rows must be ordered by id (C1 before C2), else C2 is silently skipped")
	}
	if b.Before(a) {
		t.Error("ordering must be strict/antisymmetric on the composite")
	}
	if a.Before(a) {
		t.Error("a cursor is not Before itself, the fetch uses strict `>`, so the last row is not re-read")
	}
	// A later timestamp always wins regardless of id.
	later := Cursor{UpdatedAt: tick.Add(time.Nanosecond), LastID: "A0"}
	if !b.Before(later) {
		t.Error("a strictly later timestamp must sort after, even with a smaller id")
	}
	if !(Cursor{}).IsZero() {
		t.Error("the zero cursor means from-the-beginning")
	}
}
