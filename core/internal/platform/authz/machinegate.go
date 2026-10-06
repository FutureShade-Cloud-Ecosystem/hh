// The machine gate (ADR-0024, HH-LOAD-1 LD-2, CP0-2 answer A): the leg
// FB Move's load doors serve behind. One caller exists, a machine
// credential per dealer whose private key stays in FB Move's vault; this
// core verifies it against the deployment's configured machine key set
// (the identity verifier's machine class) and admits it only when the
// record's principal is enabled, the X-Org selection equals the
// principal's own org, and the door named is on the principal's own door
// list. Every org refusal carries the staff leg's one 403 body, byte for
// byte, so the doors answer no question about which orgs, which
// principals or which doors exist: a missing header, a malformed slug,
// an unknown slug, another org, a disabled principal, an unknown
// principal and an unheld door are one answer. A check that cannot run
// (the principal source, the org anchor) answers the 503 family, never
// an admission. No staff role, no roleconfig cell and no dealer setting
// reaches this leg: the machine principal's own record is the whole
// authority, and the doors themselves register only where the
// deployment's configuration switches them on (the composition root's
// switch, off by default).
package authz

import (
	"context"
	"errors"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

// MachinePrincipalSource resolves the machine principal a verified
// machine token's subject names. The concrete reader is
// identity.MachinePrincipalResolver over the machine_principal table
// (migration 0321); the narrow seam keeps the gate honest about the one
// fact it reads and lets tests inject doubles, the same shape
// StaffRolesSource carries. ok false with a nil error is no such
// principal.
type MachinePrincipalSource interface {
	MachinePrincipal(ctx context.Context, principalID string) (identity.MachinePrincipal, bool, error)
}

// MachineGate is the gate over the machine surface. It holds the
// middleware core (whose verifier carries the machine audience and its
// key set) and the principal source, all injected.
type MachineGate struct {
	gate       *Gate
	principals MachinePrincipalSource
}

// NewMachineGate validates the collaborators and returns the gate. nil is
// a construction error, never a runtime surprise.
func NewMachineGate(gate *Gate, principals MachinePrincipalSource) (*MachineGate, error) {
	if gate == nil {
		return nil, errors.New("authz: machine gate requires the middleware core")
	}
	if principals == nil {
		return nil, errors.New("authz: machine gate requires a machine principal source")
	}
	return &MachineGate{gate: gate, principals: principals}, nil
}

// Middleware attaches the gate to one door. The door is the contract
// operation id the census carries for the route; it is validated at
// attach time so a wiring that cannot name its door refuses the boot
// instead of serving a door no principal can hold. The registrar calls
// this for every machine worded registration.
func (g *MachineGate) Middleware(door string) (func(http.Handler) http.Handler, error) {
	if door == "" {
		return nil, errors.New("authz: machine route names no door")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := g.gate.verifyBearer(w, r)
			if !ok {
				// The bearer leg already wrote its refusal: 401 with the
				// one body for every who-you-are case, 503 when the
				// verifier could not run.
				return
			}
			if token.Class != identity.ClassMachine {
				// The staff and tenant classes never pass a door, whatever
				// role a staff caller holds: the wrong caller ledger's
				// rows 2 and 3, the same body the staff leg gives a
				// non-staff class.
				refuse(w, http.StatusForbidden, BodyForbidden)
				return
			}
			scope, ok := g.machineScopeFor(w, r, token, door)
			if !ok {
				// The org, principal and door checks already wrote their
				// refusal: one 403 body for every one of them.
				return
			}
			next.ServeHTTP(w, r.WithContext(WithMachineScope(r.Context(), scope)))
		})
	}, nil
}

// machineScopeFor runs the org, principal and door checks and returns the
// scope a machine handler would receive. Every refusal but the
// operational ones is the one 403 body: the caller learns only that this
// credential may not call this door, never which org, principal or door
// exists (the same anti-oracle rule the staff leg's 403 carries, one
// level down).
func (g *MachineGate) machineScopeFor(w http.ResponseWriter, r *http.Request, token identity.Token, door string) (MachineScope, bool) {
	// X-Org is required and must equal the principal's org. The staff
	// leg answers the header's shape failures in the 400 family; this
	// leg answers every deviation, shape included, with the one 403
	// body: the doors are one credential's surface and no answer they
	// give may split the refusal families into a directory.
	slug, ok := g.machineOrgSelection(w, r)
	if !ok {
		return MachineScope{}, false
	}
	principal, ok, err := g.principals.MachinePrincipal(r.Context(), token.Subject)
	if err != nil {
		refuse(w, http.StatusServiceUnavailable, BodyUnavailable)
		return MachineScope{}, false
	}
	if !ok || !principal.Enabled {
		refuse(w, http.StatusForbidden, BodyForbidden)
		return MachineScope{}, false
	}
	orgID, ok, err := g.gate.orgs.ResolveSlug(r.Context(), slug)
	if err != nil {
		refuse(w, http.StatusServiceUnavailable, BodyUnavailable)
		return MachineScope{}, false
	}
	if !ok || orgID != principal.OrgID {
		refuse(w, http.StatusForbidden, BodyForbidden)
		return MachineScope{}, false
	}
	if !holdsDoor(principal.Doors, door) {
		refuse(w, http.StatusForbidden, BodyForbidden)
		return MachineScope{}, false
	}
	return MachineScope{
		PrincipalID: principal.ID,
		Name:        principal.Name,
		OrgSlug:     slug,
		OrgID:       orgID,
		IssuedAt:    token.IssuedAt,
		ExpiresAt:   token.ExpiresAt,
	}, true
}

// machineOrgSelection reads the machine caller's org selection with the
// one-body rule: absent, repeated, malformed or any other deviation
// refuses here with the shared 403 body, and the value itself never
// enters any response.
func (g *MachineGate) machineOrgSelection(w http.ResponseWriter, r *http.Request) (string, bool) {
	values := r.Header.Values(HeaderXOrg)
	if len(values) != 1 {
		refuse(w, http.StatusForbidden, BodyForbidden)
		return "", false
	}
	slug := values[0]
	if !orgSlugPattern.MatchString(slug) {
		refuse(w, http.StatusForbidden, BodyForbidden)
		return "", false
	}
	return slug, true
}

// holdsDoor reports whether the door is on the principal's list.
func holdsDoor(doors []string, door string) bool {
	for _, d := range doors {
		if d == door {
			return true
		}
	}
	return false
}
