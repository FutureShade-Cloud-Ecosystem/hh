// staffguards.go gives module tests the Guards-for-tests shape: the real
// Guards constructor over the kit's DB-free harness (minter, key server,
// membership and resolver doubles) with a settable staff authority stub,
// so a carried module's route-binding tests can prove which constructor
// each route sits behind exactly as the ERP's positional-guard tests did
// (a tier 3 caller is refused the manager routes, a tier 2 caller is not)
// without a database. Authority always resolves through the stub, never
// from the token (D3), which is the same property the real source has.
package testkit

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
)

// StaffSubAny is the subject the StaffGuards double admits; its authority
// is whatever SetAuthority last set, so the subject itself carries no
// meaning (never a role fact, D3).
const StaffSubAny = "fxstaff-any"

// staffAuthorityStub is the settable StaffAuthoritySource.
type staffAuthorityStub struct {
	authority identity.StaffAuthority
	has       bool
}

func (s *staffAuthorityStub) ResolveStaff(_ context.Context, _, _ string) (identity.StaffAuthority, bool, error) {
	return s.authority, s.has, nil
}

// portalRoleStub is the always-absent PortalRoleSource; staff-only route
// tests never consult it.
type portalRoleStub struct{}

func (portalRoleStub) PortalRole(_ context.Context, _, _ string) (string, bool, error) {
	return "", false, nil
}

// StaffGuards is the Guards-for-tests bundle: the real Guards, the harness
// its gate is built on (for bearers, membership and org doubles), and the
// SetAuthority swap the route-binding halves use.
type StaffGuards struct {
	*guards.Guards
	Harness *Harness
	stub    *staffAuthorityStub
}

// NewStaffGuards wires the double and points the harness doubles at one
// member org, so a staff bearer plus X-Org reaches the guard's scoped leg.
func NewStaffGuards(t *testing.T) *StaffGuards {
	t.Helper()
	h := NewHarness(t)
	h.Membership.SetMember(StaffSubAny, OrgNorthSlug, true)
	h.Resolver.Set(OrgNorthSlug, OrgNorthID)
	stub := &staffAuthorityStub{has: true}
	g, err := guards.NewGuards(h.Gate, stub, portalRoleStub{}, nil)
	if err != nil {
		t.Fatalf("testkit: NewGuards: %v", err)
	}
	return &StaffGuards{Guards: g, Harness: h, stub: stub}
}

// SetAuthority swaps the authority every staff guard resolves for any
// bearer: the tier 3 half and the tier 2 half of a binding test.
func (sg *StaffGuards) SetAuthority(a identity.StaffAuthority) {
	sg.stub.authority = a
	sg.stub.has = true
}

// SetNoAuthority makes the resolved authority absent (has false), the
// authenticated-without-grant leg every staff guard refuses.
func (sg *StaffGuards) SetNoAuthority() {
	sg.stub.has = false
}

// Bearer mints the staff bearer the double admits.
func (sg *StaffGuards) Bearer(t *testing.T) string {
	t.Helper()
	return sg.Harness.Minter.StaffToken(t, StaffSubAny)
}
