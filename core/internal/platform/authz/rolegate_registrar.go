// The role gate's attach point: a Registrar wrapper that reads each
// registration's annotation from the generated census and hands the role
// gate the routes it guards. The composition root hands its module wiring
// the wrapped registrar, so every production registration is gated the
// moment it lands: a staff route (the census x-role naming a tier gate)
// is served only through its role gate, and a staff route the census
// annotates with no surface and level is a registration defect that
// stops the boot with a panic, the mux's own idiom for a registration
// contract broken (the ServeMux duplicate-registration panic is the same
// shape). Non-staff registrations, and patterns the census does not know
// (the healthz leg registers outside registerRoutes), pass through
// unchanged: the census direction of the parity test owns those.
package authz

import (
	"fmt"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// RouteRegistrar is the one-method registration seam the wrapper wraps.
// *http.ServeMux and the test recording registrars satisfy it directly.
type RouteRegistrar interface {
	Handle(pattern string, h http.Handler)
}

// staffXRole reports whether a census x-role spelling names a tier gate
// the role gate rides behind.
func staffXRole(xrole string) bool {
	switch xrole {
	case "requireStaff", "requireManager", "requireAdmin":
		return true
	}
	return false
}

// RoleGateRegistrar wraps a Registrar and attaches the role gate to every
// staff route registered through it.
type RoleGateRegistrar struct {
	inner RouteRegistrar
	gate  *RoleGate
}

// NewRoleGateRegistrar validates the collaborators and returns the
// wrapper. The census is read lazily per registration from the generated
// table, so a regenerated census is this wrapper's regenerated facts.
func NewRoleGateRegistrar(inner RouteRegistrar, gate *RoleGate) (*RoleGateRegistrar, error) {
	if inner == nil {
		return nil, fmt.Errorf("authz: role gate registrar requires a registrar")
	}
	if gate == nil {
		return nil, fmt.Errorf("authz: role gate registrar requires a role gate")
	}
	return &RoleGateRegistrar{inner: inner, gate: gate}, nil
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

// Handle attaches the role gate to a staff registration and passes
// everything else through. The census facts are read at attach time, so
// an unannotated staff route cannot reach the mux: the completeness test
// walks the same census the wrapper reads, and the mutation that removes
// an annotation fails both. A registration pattern that matches no
// census row attaches no gate, and is owned by the parity test, which
// fails a registered route the document does not know; the admission and
// refusal probes are the behavioral backstop that the gate is truly in
// the chain.
func (r *RoleGateRegistrar) Handle(pattern string, h http.Handler) {
	route, ok := censusIndex()[pattern]
	if ok && staffXRole(route.XRole) {
		access := RouteAccess{
			Surfaces: route.Surfaces,
			Level:    roleconfig.Level(route.Level),
		}
		if err := access.valid(); err != nil {
			panic(fmt.Sprintf("authz: registered staff route %s carries no usable x-access: %v", pattern, err))
		}
		mw, err := r.gate.Middleware(access)
		if err != nil {
			panic(fmt.Sprintf("authz: registered staff route %s refused its role gate: %v", pattern, err))
		}
		h = mw(h)
	}
	r.inner.Handle(pattern, h)
}
