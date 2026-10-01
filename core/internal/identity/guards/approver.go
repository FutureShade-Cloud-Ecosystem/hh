package guards

// approver.go, the manager's credential at the clerk's till (row 482),
// resolved: verification and org through the gate's second-credential legs,
// authority through the same staff source any caller resolves through, and
// the same session ceiling any staff caller is held to (resolveCaller's
// chain, run for a credential instead of a request). No HTTP is written:
// the refusals return as the gate's sentinel errors wrapped, and the
// calling rule decides the response and its status. Nothing here logs the
// credential; every wrapped message is static text.

import (
	"context"
	"errors"
	"fmt"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

// Approver is the resolved second credential: the scope the staff leg
// stamps for any caller plus the database-resolved authority, carried
// beside the clerk's own caller for the one request that names the header.
type Approver struct {
	authz.StaffScope
	identity.StaffAuthority
}

// ResolveApprover runs the second credential through the same chain a
// staff caller passes: the gate verifies it and resolves its membership of
// orgSlug (the request's own org selection) to a scope; the staff source
// resolves its authority from the app-role tables; the role's session
// ceiling holds it exactly as it holds any caller. A credential that is
// not active (a linked users row that is not ACTIVE) or that holds no
// grant in the org is refused on the same families resolveCaller answers:
// ErrSecondCredentialRefused for the account-lifetime and ceiling facts,
// ErrSecondCredentialForeign for no grant, ErrSecondCredentialUnavailable
// for every check that could not run.
func (g *Guards) ResolveApprover(ctx context.Context, credential, orgSlug string) (Approver, error) {
	scope, err := g.gate.ResolveSecondCredential(ctx, credential, orgSlug)
	if err != nil {
		return Approver{}, err
	}
	authority, has, err := g.staff.ResolveStaff(ctx, scope.PlatformUserID, scope.OrgID)
	if err != nil {
		if errors.Is(err, identity.ErrStaffUserInactive) {
			return Approver{}, fmt.Errorf("%w: the credential names an inactive account", authz.ErrSecondCredentialRefused)
		}
		return Approver{}, fmt.Errorf("%w: the authority read failed", authz.ErrSecondCredentialUnavailable)
	}
	if !has {
		return Approver{}, fmt.Errorf("%w: the credential holds no grant in the org", authz.ErrSecondCredentialForeign)
	}
	ceiling, err := identity.SessionCeiling(ctx, authority.RoleID, scope.OrgID, g.knob)
	if err != nil {
		return Approver{}, fmt.Errorf("%w: the session ceiling is not computable", authz.ErrSecondCredentialUnavailable)
	}
	if !identity.SessionWithinCeiling(scope.IssuedAt, scope.ExpiresAt, ceiling) {
		return Approver{}, fmt.Errorf("%w: the credential window exceeds the role's ceiling", authz.ErrSecondCredentialRefused)
	}
	return Approver{StaffScope: scope, StaffAuthority: authority}, nil
}
