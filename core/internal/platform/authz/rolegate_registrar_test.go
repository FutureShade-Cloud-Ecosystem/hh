package authz

// The registrar's own suite (the PR 44 review's P2-5): the attach-time
// refusals and the pass-throughs, over a synthetic census index, so the
// defect paths are exercised without mutating the generated table. The
// gate beneath the wrapper is real: the same middleware fixture, so the
// attachment is observed behaviorally (the wrapped handler answers the
// gate's refusal, not the handler's own).

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// recordingInner records every pattern handed through and serves a bare
// 200 from a handler the test can replace per pattern.
type recordingInner struct {
	patterns []string
	handlers map[string]http.Handler
}

func (r *recordingInner) Handle(pattern string, h http.Handler) {
	r.patterns = append(r.patterns, pattern)
	r.handlers[pattern] = h
}

func censusFor(routes ...apigen.Route) map[string]apigen.Route {
	m := make(map[string]apigen.Route, len(routes))
	for _, r := range routes {
		m[r.Method+" "+r.Path] = r
	}
	return m
}

func newRegistrarFixture(t *testing.T) (*fixture, *RoleGate, *roleconfig.Config) {
	t.Helper()
	f := newFixture(t)
	cfg, err := roleconfig.Default()
	if err != nil {
		t.Fatalf("roleconfig.Default: %v", err)
	}
	g, err := NewRoleGate(f.gate, &fakeRolesSource{roles: []string{"inside_sales"}}, cfg)
	if err != nil {
		t.Fatalf("NewRoleGate: %v", err)
	}
	return f, g, cfg
}

func annotatedBoard() apigen.Route {
	for _, r := range apigen.Routes {
		if r.OperationID == "dispatchGetBoard" {
			return r
		}
	}
	panic("dispatchGetBoard left the census")
}

// TestRegistrarAttachesTheGate proves the wrapper actually wraps: a staff
// registration served through the wrapper answers the role gate's refusal
// for a caller the matrix refuses, where the bare handler would answer
// its own. This is the attachment the composition completeness walk
// cannot see (P3-3's note, at unit scale).
func TestRegistrarAttachesTheGate(t *testing.T) {
	f, g, _ := newRegistrarFixture(t)
	inner := &recordingInner{handlers: map[string]http.Handler{}}
	reg, err := newRoleGateRegistrarWithCensus(inner, g, censusFor(annotatedBoard()))
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	bare := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	reg.Handle("GET /api/v1/dispatch/board", bare)
	if len(inner.patterns) != 1 {
		t.Fatalf("registration did not pass through: %v", inner.patterns)
	}
	// A caller the matrix refuses (the fixture's role source holds
	// inside_sales, whose dispatch-board cell is none) must meet the
	// gate's RBAC-403, not the bare handler's teapot.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dispatch/board", nil)
	req.Header.Set("Authorization", "Bearer "+f.staffToken(t, fixtureSubject))
	req.Header.Set(HeaderXOrg, fixtureOrgSlug)
	rec := httptest.NewRecorder()
	inner.handlers["GET /api/v1/dispatch/board"].ServeHTTP(rec, req)
	if rec.Code == http.StatusTeapot {
		t.Fatal("the gate is not in the chain: the bare handler answered")
	}
	if rec.Code != http.StatusForbidden || rec.Body.String() != string(BodyForbidden) {
		t.Fatalf("status %d body %s, want the role gate's RBAC-403", rec.Code, rec.Body.String())
	}
}

// TestRegistrarPanicsOnAnUnannotatedStaffRoute is the attach-time wall: a
// staff route the census annotates with no surface stops the boot here,
// which is what the composition completeness walk's census-side
// assertion would name a moment later.
func TestRegistrarPanicsOnAnUnannotatedStaffRoute(t *testing.T) {
	_, g, _ := newRegistrarFixture(t)
	inner := &recordingInner{handlers: map[string]http.Handler{}}
	reg, err := newRoleGateRegistrarWithCensus(inner, g, censusFor(apigen.Route{
		Method: "GET", Path: "/api/v1/dispatch/board", OperationID: "dispatchGetBoard", XRole: "requireStaff",
	}))
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("an unannotated staff route registered without a panic")
		}
		if len(inner.patterns) != 0 {
			t.Fatal("the unannotated route reached the mux")
		}
	}()
	reg.Handle("GET /api/v1/dispatch/board", http.NotFoundHandler())
}

// TestRegistrarPanicsOnAnUnknownAPIRoute is the boot-time half of the
// parity direction: a registration under /api/v1/ the document does not
// know would serve with no gate, so the wrapper refuses it outright.
func TestRegistrarPanicsOnAnUnknownAPIRoute(t *testing.T) {
	_, g, _ := newRegistrarFixture(t)
	inner := &recordingInner{handlers: map[string]http.Handler{}}
	reg, err := newRoleGateRegistrarWithCensus(inner, g, censusFor())
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("an unknown /api/v1/ registration passed without a panic")
		}
	}()
	reg.Handle("POST /api/v1/not-in-the-document", http.NotFoundHandler())
}

// TestRegistrarPassesThroughTheRest holds the two pass-throughs: a
// non-staff route the census knows (public, tenant, sync) registers
// ungated, and a registration outside /api/v1/ (the healthz leg) passes
// even though no census row names it.
func TestRegistrarPassesThroughTheRest(t *testing.T) {
	_, g, _ := newRegistrarFixture(t)
	inner := &recordingInner{handlers: map[string]http.Handler{}}
	reg, err := newRoleGateRegistrarWithCensus(inner, g, censusFor(apigen.Route{
		Method: "GET", Path: "/api/v1/tenant/config", OperationID: "tenantGetConfig", XRole: "public",
	}))
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	mark := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	reg.Handle("GET /api/v1/tenant/config", mark)
	reg.Handle("GET /healthz", mark)
	if len(inner.patterns) != 2 {
		t.Fatalf("pass-through registrations lost: %v", inner.patterns)
	}
	for _, pattern := range inner.patterns {
		req := httptest.NewRequest(http.MethodGet, "/anything", nil)
		rec := httptest.NewRecorder()
		inner.handlers[pattern].ServeHTTP(rec, req)
		if rec.Code != http.StatusTeapot {
			t.Fatalf("%s: the pass-through handler was wrapped", pattern)
		}
	}
}

// TestRegistrarConstructorRefusals holds the nil collaborators.
func TestRegistrarConstructorRefusals(t *testing.T) {
	_, g, _ := newRegistrarFixture(t)
	inner := &recordingInner{handlers: map[string]http.Handler{}}
	if _, err := newRoleGateRegistrarWithCensus(nil, g, censusFor()); err == nil {
		t.Fatal("nil inner registrar constructed")
	}
	if _, err := newRoleGateRegistrarWithCensus(inner, nil, censusFor()); err == nil {
		t.Fatal("nil gate constructed")
	}
	if _, err := newRoleGateRegistrarWithCensus(inner, g, nil); err == nil {
		t.Fatal("nil census constructed")
	}
}
