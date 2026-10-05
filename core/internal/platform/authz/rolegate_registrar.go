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
// Non-staff registrations the census knows, and patterns outside /api/v1/
// (the healthz leg registers outside registerRoutes), pass through
// unchanged.
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
// the role gate rides behind. It is one half of the registrar's wrapped
// word set; machineXRole is the other half, and a machine worded
// registration carries no x-access at all.
func staffXRole(xrole string) bool {
	switch xrole {
	case "requireStaff", "requireManager", "requireAdmin":
		return true
	}
	return false
}

// machineXRole reports whether a census x-role spelling names the
// machine gate (ADR-0024): the load doors' word. A machine worded route
// registers only behind the machine gate, and Handle stops the boot when
// one arrives without it, the same idiom the unannotated staff route
// carries: handed through, it would serve with no gate at all. Machine
// routes carry no x-access (the machine principal's own door list is the
// wall), so the census fields the role gate reads stay empty for them.
func machineXRole(xrole string) bool {
	return xrole == "machine"
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
// staff route registered through it, and the machine gate to every
// machine worded route when the wiring carried one (the composition root
// attaches it only where the deployment's configuration switched the
// load doors on).
type RoleGateRegistrar struct {
	inner       RouteRegistrar
	gate        *RoleGate
	machineGate *MachineGate
	census      map[string]apigen.Route
}

// NewRoleGateRegistrar validates the collaborators, builds the census
// index once, and returns the wrapper with no machine gate: the doors
// ship switched off, so a registration the census words machine stops
// the boot here until the wiring that switched them on carries the gate.
func NewRoleGateRegistrar(inner RouteRegistrar, gate *RoleGate) (*RoleGateRegistrar, error) {
	return newRoleGateRegistrarWithCensus(inner, gate, censusIndex())
}

// NewRoleGateRegistrarWithMachineGate is NewRoleGateRegistrar with the
// machine gate attached: the shape the composition root builds when the
// deployment's configuration switched the load doors on, so the machine
// worded routes the load module registers land behind their gate.
func NewRoleGateRegistrarWithMachineGate(inner RouteRegistrar, gate *RoleGate, machineGate *MachineGate) (*RoleGateRegistrar, error) {
	if machineGate == nil {
		return nil, fmt.Errorf("authz: role gate registrar requires a machine gate when one is named")
	}
	r, err := newRoleGateRegistrarWithCensus(inner, gate, censusIndex())
	if err != nil {
		return nil, err
	}
	r.machineGate = machineGate
	return r, nil
}

// newRoleGateRegistrarWithCensus is the constructor over a caller-supplied
// census index, the seam the unit tests use to exercise the defect paths
// without mutating the generated table.
func newRoleGateRegistrarWithCensus(inner RouteRegistrar, gate *RoleGate, census map[string]apigen.Route) (*RoleGateRegistrar, error) {
	return newRoleGateRegistrarWithCensusAndMachineGate(inner, gate, nil, census)
}

// newRoleGateRegistrarWithCensusAndMachineGate is the test seam over both
// gates and a caller-supplied census index.
func newRoleGateRegistrarWithCensusAndMachineGate(inner RouteRegistrar, gate *RoleGate, machineGate *MachineGate, census map[string]apigen.Route) (*RoleGateRegistrar, error) {
	if inner == nil {
		return nil, fmt.Errorf("authz: role gate registrar requires a registrar")
	}
	if gate == nil {
		return nil, fmt.Errorf("authz: role gate registrar requires a role gate")
	}
	if census == nil {
		return nil, fmt.Errorf("authz: role gate registrar requires a census index")
	}
	return &RoleGateRegistrar{inner: inner, gate: gate, machineGate: machineGate, census: census}, nil
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

// Handle attaches the role gate to a staff registration, the machine
// gate to a machine worded registration, and passes everything else
// through. The census facts are read at attach time, so an unannotated
// staff route cannot reach the mux: the completeness test walks the same
// census the wrapper reads, and the mutation that removes an annotation
// fails both. A machine worded registration whose wiring carries no
// machine gate stops the boot the same way: the doors ship switched off,
// and a route arriving for them without the gate is a registration
// defect, never a door that serves ungated. A registration under
// /api/v1/ matching no census row is refused here outright: parity owns
// the same direction at test time, and this owns it at boot time, where
// the route would otherwise serve ungated.
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
	} else if machineXRole(route.XRole) {
		if r.machineGate == nil {
			panic(fmt.Sprintf("authz: registered route %s is worded machine but the wiring carries no machine gate; it would serve with no gate", pattern))
		}
		mw, err := r.machineGate.Middleware(route.OperationID)
		if err != nil {
			panic(fmt.Sprintf("authz: registered machine route %s refused its machine gate: %v", pattern, err))
		}
		h = mw(h)
	}
	r.inner.Handle(pattern, h)
}
