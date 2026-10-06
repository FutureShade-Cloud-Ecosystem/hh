// The role gate's attach point: a Registrar wrapper that reads each
// registration's annotation from the generated census and hands the role
// gate the routes it guards. The composition root hands its module wiring
// the wrapped registrar, so every production registration is gated the
// moment it lands: a staff route (the census x-role naming a tier gate)
// is served only through its role gate, and a staff route the census
// annotates with no surface and level is a registration defect that
// stops the boot with a panic, the mux's own idiom for a registration
// contract broken (the ServeMux duplicate-registration panic is the same
// shape). A registration under /api/v1/ the census does not know is the
// same kind of defect and panics too: it would serve ungated otherwise,
// and the tests that own that direction (parity, the completeness walk)
// are database gated, while this refusal costs one lookup at boot.
//
// The census word set is closed and the registrar fails closed on it.
// The words, and which middleware gates each family: requireStaff,
// requireManager and requireAdmin are wrapped in the role gate here;
// public passes open by design (sign-in, public quote views, the product
// list, will-call); tenant is gated downstream by Guards.RequireTenant,
// applied by the module wiring that registers the tenant routes;
// scope-member is gated by Guards.RequireSync, applied where the sync
// routes register (the identity shims); a word carrying the capability:
// prefix is gated by Guards.RequireCapability, applied by the module
// that registers the route. Those pass through and nothing else does: a
// census row carrying any other word panics at registration, because
// handed through it would serve ungated. The word that matters today is
// operator (three routes declared for the platform launcher's own
// binary, never this core's to register) and the rule holds for every
// word a future contract invents: an unknown word is a boot refusal, an
// ungated door is what it replaces. Patterns outside /api/v1/ (the
// healthz leg registers outside registerRoutes) and non-API
// registrations pass through unchanged.
package authz

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// RouteRegistrar is the one-method registration seam the wrapper wraps.
// *http.ServeMux and the test recording registrars satisfy it directly.
type RouteRegistrar interface {
	Handle(pattern string, h http.Handler)
}

// staffXRole reports whether a census x-role spelling names a tier gate
// the role gate rides behind. It is one half of the registrar's closed
// word set; the other half is passThroughXRole. A word in neither half
// is a word the registrar does not know, and Handle refuses the
// registration for it: the wrapper fails closed on any census word
// outside the set, so an invented word can never serve ungated.
func staffXRole(xrole string) bool {
	switch xrole {
	case "requireStaff", "requireManager", "requireAdmin":
		return true
	}
	return false
}

// passThroughXRole reports whether a census x-role word is one the
// registrar hands to the mux unwrapped: the word families whose gates
// are the identity guards applied where the routes register. public is
// open by design; tenant rides Guards.RequireTenant; scope-member rides
// Guards.RequireSync; a word with the capability: prefix rides
// Guards.RequireCapability. Anything else is unknown here and fails
// closed in Handle.
func passThroughXRole(xrole string) bool {
	switch xrole {
	case "public", "tenant", "scope-member":
		return true
	}
	return strings.HasPrefix(xrole, "capability:")
}

// censusIndex indexes the census by method-prefixed pattern, the same
// index the parity and x-role suites use.
func censusIndex() map[string]apigen.Route {
	byPattern := make(map[string]apigen.Route, len(apigen.Routes))
	for _, route := range apigen.Routes {
		byPattern[route.Method+" "+route.Path] = route
	}
	return byPattern
}

// RoleGateRegistrar wraps a Registrar and attaches the role gate to every
// staff route registered through it.
type RoleGateRegistrar struct {
	inner  RouteRegistrar
	gate   *RoleGate
	census map[string]apigen.Route
}

// NewRoleGateRegistrar validates the collaborators, builds the census
// index once, and returns the wrapper.
func NewRoleGateRegistrar(inner RouteRegistrar, gate *RoleGate) (*RoleGateRegistrar, error) {
	return newRoleGateRegistrarWithCensus(inner, gate, censusIndex())
}

// newRoleGateRegistrarWithCensus is the constructor over a caller-supplied
// census index, the seam the unit tests use to exercise the defect paths
// without mutating the generated table.
func newRoleGateRegistrarWithCensus(inner RouteRegistrar, gate *RoleGate, census map[string]apigen.Route) (*RoleGateRegistrar, error) {
	if inner == nil {
		return nil, fmt.Errorf("authz: role gate registrar requires a registrar")
	}
	if gate == nil {
		return nil, fmt.Errorf("authz: role gate registrar requires a role gate")
	}
	if census == nil {
		return nil, fmt.Errorf("authz: role gate registrar requires a census index")
	}
	return &RoleGateRegistrar{inner: inner, gate: gate, census: census}, nil
}

// apiV1Path strips the method prefix from a registration pattern and
// reports whether the path is under /api/v1/.
func apiV1Path(pattern string) (string, bool) {
	for _, method := range []string{"GET ", "POST ", "PUT ", "PATCH ", "DELETE "} {
		if path, found := strings.CutPrefix(pattern, method); found {
			return path, strings.HasPrefix(path, "/api/v1/")
		}
	}
	return "", false
}

// Handle attaches the role gate to a staff registration, hands the known
// pass-through families (public, tenant, scope-member, capability:) to
// the mux unwrapped, and refuses any other census word outright: a word
// the registrar does not know has no gate anywhere in this wiring, so
// the fail-closed answer is the boot refusal, the file's own panic
// idiom. The census facts are read at attach time, so an unannotated
// staff route cannot reach the mux: the completeness test walks the same
// census the wrapper reads, and the mutation that removes an annotation
// fails both. A registration under /api/v1/ matching no census row is
// refused here too: parity owns the same direction at test time, and
// this owns it at boot time, where the route would otherwise serve
// ungated.
func (r *RoleGateRegistrar) Handle(pattern string, h http.Handler) {
	route, ok := r.census[pattern]
	if !ok {
		if _, isAPI := apiV1Path(pattern); isAPI {
			panic(fmt.Sprintf("authz: registered route %s is not in the document; it would serve with no gate", pattern))
		}
		r.inner.Handle(pattern, h)
		return
	}
	if staffXRole(route.XRole) {
		access := RouteAccess{
			Surfaces: route.Surfaces,
			Level:    roleconfig.Level(route.Level),
			List:     route.List,
		}
		if err := access.valid(); err != nil {
			panic(fmt.Sprintf("authz: registered staff route %s carries no usable x-access: %v", pattern, err))
		}
		mw, err := r.gate.Middleware(access)
		if err != nil {
			panic(fmt.Sprintf("authz: registered staff route %s refused its role gate: %v", pattern, err))
		}
		h = mw(h)
	} else if !passThroughXRole(route.XRole) {
		panic(fmt.Sprintf("authz: registered route %s carries census x-role %q, which this registrar does not know; it would serve with no gate", pattern, route.XRole))
	}
	r.inner.Handle(pattern, h)
}
