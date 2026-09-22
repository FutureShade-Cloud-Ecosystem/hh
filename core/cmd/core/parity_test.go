package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/tenantconfig"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

// The registration parity test (B-parity, migrated at the Phase C
// integration per the A6 review's assembly steps): the core's registered
// surface and the contract are asserted equal in both directions, from the
// one composition point the binary itself wires (registerRoutes over the
// fold's environment-driven signature, so the census covers the FULL
// served surface: the shims, the public tenant config, the portal
// re-homing's tenant operations, the customer-quote trio, the money spine,
// the operations cluster and the tops), against the route table generated
// from the assembled document (core/internal/platform/apigen). The
// construction runs over the gate database and the identity environment
// map (compfixture_test.go); the census is DB-gated, and absent the gate
// variable this suite skips politely. The vendored ERP-side baseline is
// the document's own parity ledger (core/api/drop-list.yaml), checked by
// core/api/tools/parity.mjs inside check-all; this test is the CORE side
// of the same discipline: every registered route must be documented, and
// every documented route must be registered or explicitly accounted in
// the not-yet-registered ledger, which only ever shrinks.

// recordingRegistrar enumerates the registered surface: it records every
// pattern handed to Handle, in registration order, then delegates to a
// real ServeMux. The standard library exposes no enumeration of a mux, so
// the recording happens at the registration seam (httpx.Registrar) the
// wiring already uses.
type recordingRegistrar struct {
	inner    *http.ServeMux
	patterns []string
}

func (r *recordingRegistrar) Handle(pattern string, h http.Handler) {
	r.patterns = append(r.patterns, pattern)
	r.inner.Handle(pattern, h)
}

func TestRegistrationParityBothDirections(t *testing.T) {
	env := newCompEnv(t)
	rec, _ := registeredSurface(t, env)

	// The document side, indexed two ways, with uniqueness asserted so a
	// generator fault cannot paper over a duplicate.
	byPattern := make(map[string]apigen.Route, len(apigen.Routes))
	byOperation := make(map[string]apigen.Route, len(apigen.Routes))
	for _, route := range apigen.Routes {
		pattern := route.Method + " " + route.Path
		if _, dup := byPattern[pattern]; dup {
			t.Fatalf("document has two operations for %s", pattern)
		}
		if _, dup := byOperation[route.OperationID]; dup {
			t.Fatalf("document has two operations named %s", route.OperationID)
		}
		byPattern[pattern] = route
		byOperation[route.OperationID] = route
	}

	// Direction one: every route registered in the core is in the
	// document, method and path template exactly. A different path
	// parameter name is a mismatch on purpose: the fragments transcribe
	// the ERP's parameter names verbatim and the shims carry them, so an
	// exact-template comparison is the strict reading of "registered".
	registered := make(map[string]bool, len(rec.patterns))
	for _, pattern := range rec.patterns {
		method, path, ok := strings.Cut(pattern, " ")
		if !ok || path == "" {
			t.Fatalf("registered pattern %q is not method-prefixed", pattern)
		}
		if _, ok := byPattern[pattern]; !ok {
			t.Fatalf("registered route %s %s is not in the document (core/api/openapi.yaml)", method, path)
		}
		if registered[pattern] {
			t.Fatalf("route registered twice: %s", pattern)
		}
		registered[pattern] = true
	}

	// Direction two: every documented route is registered or explicitly
	// accounted in the not-yet-registered ledger, no ledger entry is
	// stale (a registered operation must have its entry removed), and no
	// entry names an operation the document does not carry.
	accounted := 0
	for op := range notYetRegistered {
		route, ok := byOperation[op]
		if !ok {
			t.Fatalf("ledger entry %q names no documented operation; prune it with the document", op)
		}
		if registered[route.Method+" "+route.Path] {
			t.Fatalf("stale ledger entry: %s is registered; remove its entry so the ledger only shrinks", op)
		}
		accounted++
	}
	missing := 0
	for _, route := range apigen.Routes {
		if registered[route.Method+" "+route.Path] {
			continue
		}
		if _, ok := notYetRegistered[route.OperationID]; !ok {
			t.Errorf("documented route %s %s (%s) is neither registered nor in the not-yet-registered ledger",
				route.Method, route.Path, route.OperationID)
			missing++
		}
	}
	if missing > 0 {
		t.Fatalf("%d documented operations lack both a registration and a ledger entry", missing)
	}
	t.Logf("parity: %d documented operations, %d registered, %d ledgered, both directions green",
		len(apigen.Routes), len(registered), accounted)
}

// The public config route rides the public leg through the real wiring
// (registerRoutes over a real ServeMux, the identity environment map and
// the gate database): no verification, no org resolution, and neither an
// Authorization header nor an X-Org header has any effect on the answer
// (ADR-0020 leg 2: X-Org on a public request is an undeclared header with
// zero effect). The body is the narrowed TenantPublicConfig: six dealer
// fields, no brand property.
func TestPublicConfigRoutePublicLeg(t *testing.T) {
	env := newCompEnv(t)
	rec, _ := registeredSurface(t, env)
	mux := rec.inner

	get := func(name string, header map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tenant/config", nil)
		for k, v := range header {
			req.Header.Set(k, v)
		}
		rsp := httptest.NewRecorder()
		mux.ServeHTTP(rsp, req)
		return rsp
	}

	bare := get("no credentials", nil)
	if bare.Code != http.StatusOK {
		t.Fatalf("no credentials: status %d, want 200 on the public leg", bare.Code)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(bare.Body.Bytes(), &raw); err != nil {
		t.Fatalf("body is not a JSON object: %v", err)
	}
	if _, ok := raw["brand"]; ok {
		t.Fatalf("public config serves a brand property: %v", raw)
	}
	if len(raw) != 6 {
		t.Fatalf("public config serves %d keys, want the six dealer fields: %v", len(raw), raw)
	}

	withOrg := get("X-Org header", map[string]string{authz.HeaderXOrg: "some-org"})
	if withOrg.Code != http.StatusOK || withOrg.Body.String() != bare.Body.String() {
		t.Fatalf("X-Org changed the public answer: status %d", withOrg.Code)
	}
	withBearer := get("bogus bearer", map[string]string{"Authorization": "Bearer not-a-token"})
	if withBearer.Code != http.StatusOK || withBearer.Body.String() != bare.Body.String() {
		t.Fatalf("a bearer header changed the public answer: status %d", withBearer.Code)
	}

	// The typed shape: the served body decodes into the generated
	// TenantPublicConfig and back, and the type has no brand field to
	// fill, so the narrowing is structural, not a handler promise.
	var served apigen.TenantPublicConfig
	if err := json.Unmarshal(bare.Body.Bytes(), &served); err != nil {
		t.Fatalf("decode into TenantPublicConfig: %v", err)
	}
	if served != tenantconfig.Default() {
		t.Fatalf("served config %+v, want the dealer stub", served)
	}
}

// The shim removal backstop (Security S-3): when a real module registers a
// route still held by a shim, the ServeMux panics on the duplicate at
// wiring time rather than serving two handlers for one route. The panic is
// the mechanism that makes a missed ledger removal loud.
func TestServeMuxDuplicateRegistrationPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("duplicate registration did not panic")
		}
		if !strings.Contains(fmt.Sprint(r), "conflicts with pattern") {
			t.Fatalf("panic does not name the duplicate: %v", r)
		}
	}()
	mux := http.NewServeMux()
	noop := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	mux.Handle("GET /api/v1/tenant/config", noop)
	mux.Handle("GET /api/v1/tenant/config", noop)
}
