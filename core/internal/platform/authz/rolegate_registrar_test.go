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
	"strings"
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

// teapot is the bare handler whose own answer (418) marks an unwrapped
// pass-through: a handler wearing the role gate cannot answer it.
func teapot() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
}

// TestRegistrarPanicsOnAnUnknownCensusWord is the fail-closed wall: a
// census row carrying a word the registrar does not know (an operator
// route the platform launcher's own binary owns, or a word some future
// contract invents) must refuse the registration at attach time, because
// handed through it would serve with no gate at all. The refusal names
// the pattern and the word, the file's existing panic idiom.
func TestRegistrarPanicsOnAnUnknownCensusWord(t *testing.T) {
	_, g, _ := newRegistrarFixture(t)
	for _, word := range []string{"operator", "invented"} {
		inner := &recordingInner{handlers: map[string]http.Handler{}}
		reg, err := newRoleGateRegistrarWithCensus(inner, g, censusFor(apigen.Route{
			Method: "PUT", Path: "/api/v1/operator/dealer-credential", OperationID: "putDealerCredential", XRole: word,
		}))
		if err != nil {
			t.Fatalf("constructor: %v", err)
		}
		stopped := func() (stopped bool) {
			defer func() {
				r := recover()
				if r == nil {
					return
				}
				stopped = true
				msg, ok := r.(string)
				if !ok {
					t.Fatalf("x-role %s: the refusal is not the panic idiom: %v", word, r)
				}
				if !strings.Contains(msg, "PUT /api/v1/operator/dealer-credential") || !strings.Contains(msg, word) {
					t.Fatalf("x-role %s: the refusal must name the pattern and the word: %q", word, msg)
				}
			}()
			reg.Handle("PUT /api/v1/operator/dealer-credential", http.NotFoundHandler())
			return false
		}()
		if !stopped {
			t.Fatalf("x-role %s: the registration passed through ungated; an unknown census word must fail closed", word)
		}
		if len(inner.patterns) != 0 {
			t.Fatalf("x-role %s: the unknown word reached the mux: %v", word, inner.patterns)
		}
	}
}

// censusWalkExpected is the census walk's expected-classes table, today's
// census words only. The three tier words are wrapped in the role gate;
// public, tenant, scope-member and the capability words pass through
// unwrapped, their gates being the identity guards the module wiring
// applies downstream; operator is refused outright, the word being
// declared for the platform launcher's own binary and never this core's
// to serve. A census word this table does not name fails the walk: a word
// invented tomorrow must be classified here before the suite passes.
var censusWalkExpected = map[string]string{
	"requireStaff":                          "wrapped",
	"requireManager":                        "wrapped",
	"requireAdmin":                          "wrapped",
	"public":                                "passed",
	"tenant":                                "passed",
	"scope-member":                          "passed",
	"capability:create_orders":              "passed",
	"capability:edit_delivery_instructions": "passed",
	"capability:manage_payment_methods":     "passed",
	"capability:manage_users":               "passed",
	"capability:pay_invoices":               "passed",
	"capability:submit_rfq":                 "passed",
	"capability:view_billing":               "passed",
	"capability:view_orders_deliveries":     "passed",
	"operator":                              "refused",
}

// TestRegistrarCensusWalkClassifiesEveryWord registers every route in the
// real census through the wrapper over the recording inner and classifies
// each word by what happened: wrapped means the stored handler answers
// the gate's staff refusal for a request with no credential at all (the
// role gate is in the chain), passed means the stored handler answers its
// own teapot (nothing wrapped it), refused means the attach panicked
// naming the pattern and the word. Each census word must land in exactly
// the class the expected table names, on the real table the parity and
// x-role suites walk beside this.
func TestRegistrarCensusWalkClassifiesEveryWord(t *testing.T) {
	_, g, _ := newRegistrarFixture(t)
	index := censusIndex()
	inner := &recordingInner{handlers: map[string]http.Handler{}}
	reg, err := newRoleGateRegistrarWithCensus(inner, g, index)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	observed := map[string]map[string]bool{}
	for _, route := range apigen.Routes {
		pattern := route.Method + " " + route.Path
		class := classifyCensusRegistration(t, reg, inner, pattern, route.XRole)
		if observed[route.XRole] == nil {
			observed[route.XRole] = map[string]bool{}
		}
		observed[route.XRole][class] = true
	}
	for word, classes := range observed {
		want, classified := censusWalkExpected[word]
		if !classified {
			t.Fatalf("census word %q must be classified: the walk's expected table does not name it, so no gate contract covers the word", word)
		}
		for class := range classes {
			if class != want {
				t.Fatalf("census word %q: observed %s, want %s", word, class, want)
			}
		}
	}
	for word := range censusWalkExpected {
		if observed[word] == nil {
			t.Fatalf("expected word %q appears in no census row: the expected table and the document have drifted", word)
		}
	}
}

// classifyCensusRegistration registers one census pattern through the
// wrapper and reports what happened. The classes are the whole contract:
// refused (the attach panicked naming the pattern and the word), passed
// (the inner registrar holds the bare handler and it answers its own
// teapot), wrapped (the inner registrar holds a handler that answers the
// gate's unauthenticated refusal). Any other outcome fails the walk.
func classifyCensusRegistration(t *testing.T, reg *RoleGateRegistrar, inner *recordingInner, pattern, word string) string {
	t.Helper()
	refused := func() (refused bool) {
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			msg, ok := r.(string)
			if !ok {
				t.Fatalf("%s: the attach refused with a non-string panic: %v", pattern, r)
			}
			if !strings.Contains(msg, pattern) || !strings.Contains(msg, word) {
				t.Fatalf("%s: the unknown-word refusal must name the pattern and the word: %q", pattern, msg)
			}
			refused = true
		}()
		reg.Handle(pattern, teapot())
		return false
	}()
	if refused {
		return "refused"
	}
	h, ok := inner.handlers[pattern]
	if !ok {
		t.Fatalf("%s: the attach neither registered nor refused the route", pattern)
	}
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	switch {
	case rec.Code == http.StatusTeapot:
		return "passed"
	case rec.Code == http.StatusUnauthorized && rec.Body.String() == string(BodyUnauthorized):
		return "wrapped"
	}
	t.Fatalf("%s: the served answer is none of the walk's classes: %d %q", pattern, rec.Code, rec.Body.String())
	return ""
}

// TestRegistrarPassThroughWordsStayOpen holds the pass-through families
// open: a census row carrying public, tenant, scope-member or a
// capability word registers with no panic and the bare handler is reached
// unwrapped, their gates being the identity guards the module wiring
// applies downstream (RequireTenant, RequireSync, RequireCapability),
// never this wrapper's business.
func TestRegistrarPassThroughWordsStayOpen(t *testing.T) {
	_, g, _ := newRegistrarFixture(t)
	rows := []apigen.Route{
		{Method: "GET", Path: "/api/v1/tenant/config", OperationID: "tenantGetConfig", XRole: "public"},
		{Method: "GET", Path: "/api/v1/tenant/me", OperationID: "tenantGetMe", XRole: "tenant"},
		{Method: "GET", Path: "/api/v1/sync/{scope}", OperationID: "syncPull", XRole: "scope-member"},
		{Method: "GET", Path: "/api/v1/tenant/invoices", OperationID: "tenantListInvoices", XRole: "capability:view_billing"},
	}
	inner := &recordingInner{handlers: map[string]http.Handler{}}
	reg, err := newRoleGateRegistrarWithCensus(inner, g, censusFor(rows...))
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	for _, row := range rows {
		reg.Handle(row.Method+" "+row.Path, teapot())
	}
	if len(inner.patterns) != len(rows) {
		t.Fatalf("pass-through registrations lost: %v", inner.patterns)
	}
	for _, row := range rows {
		pattern := row.Method + " " + row.Path
		req := httptest.NewRequest(http.MethodGet, "/probe", nil)
		rec := httptest.NewRecorder()
		inner.handlers[pattern].ServeHTTP(rec, req)
		if rec.Code != http.StatusTeapot {
			t.Fatalf("%s (x-role %s): the pass-through handler was wrapped", pattern, row.XRole)
		}
	}
}
