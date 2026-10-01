package authz

// second_credential.go, the resolution half of the manager's credential at
// the clerk's till (row 482): a return above the dealer's approval amount
// may carry a second credential beside the caller's bearer, and this file
// resolves that credential through the same legs the gate runs for any
// caller, verification and bearer class, membership of the request org,
// the org anchor, without writing HTTP: the calling rule decides the
// response. The refusal sentinels name the three families the gate writes
// so the caller maps the same statuses this package already pins. Nothing
// here logs the credential: every error is static text and the token never
// enters one, the same discipline verifyBearer holds for the first bearer.

import (
	"context"
	"errors"
	"fmt"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

// The second credential's refusal families, the gate's own vocabulary:
// ErrSecondCredentialRefused is the 401 family (the credential did not
// verify, or is not a staff bearer), ErrSecondCredentialForeign is the 403
// family (it verified but holds no standing in the request org: no
// membership, or an org with no anchor, or, at the guard layer, no grant),
// and ErrSecondCredentialUnavailable is the 503 family (a check could not
// run, never a verdict on the credential). The caller wraps them with
// static detail; the sentinels stay errors.Is-able across the wrap.
var (
	ErrSecondCredentialRefused     = errors.New("authz: the second credential did not verify")
	ErrSecondCredentialForeign     = errors.New("authz: the second credential holds no standing in the request org")
	ErrSecondCredentialUnavailable = errors.New("authz: the second credential could not be checked")
)

// ResolveSecondCredential verifies one second credential within one
// request's org and returns the staff scope it would have stamped, the
// same scope shape the staff leg stamps for any caller. The credential is
// the raw header value (the same Bearer scheme prefix as Authorization);
// orgSlug is the request's own org selection. An empty credential or org
// is refused, never guessed.
func (g *Gate) ResolveSecondCredential(ctx context.Context, credential, orgSlug string) (StaffScope, error) {
	if credential == "" || orgSlug == "" {
		return StaffScope{}, fmt.Errorf("%w: the credential and the org selection are required", ErrSecondCredentialRefused)
	}
	raw, ok := bearerToken(credential)
	if !ok {
		return StaffScope{}, fmt.Errorf("%w: not a bearer credential", ErrSecondCredentialRefused)
	}
	token, err := g.verify.Verify(ctx, raw)
	if err != nil {
		var verr *identity.VerifyError
		if errors.As(err, &verr) && verr.Class() == identity.ClassOperational {
			return StaffScope{}, fmt.Errorf("%w: the verifier could not run", ErrSecondCredentialUnavailable)
		}
		return StaffScope{}, fmt.Errorf("%w: verification refused it", ErrSecondCredentialRefused)
	}
	if token.Class != identity.ClassStaff {
		return StaffScope{}, fmt.Errorf("%w: not a staff bearer", ErrSecondCredentialRefused)
	}
	member, err := g.members.OrgMember(ctx, token.Subject, orgSlug)
	if err != nil {
		return StaffScope{}, fmt.Errorf("%w: the membership check failed", ErrSecondCredentialUnavailable)
	}
	if !member {
		return StaffScope{}, fmt.Errorf("%w: no membership of the request org", ErrSecondCredentialForeign)
	}
	orgID, ok, err := g.orgs.ResolveSlug(ctx, orgSlug)
	if err != nil {
		return StaffScope{}, fmt.Errorf("%w: the org anchor read failed", ErrSecondCredentialUnavailable)
	}
	if !ok {
		return StaffScope{}, fmt.Errorf("%w: the request org has no anchor", ErrSecondCredentialForeign)
	}
	return StaffScope{
		PlatformUserID: token.Subject,
		OrgSlug:        orgSlug,
		OrgID:          orgID,
		IssuedAt:       token.IssuedAt,
		ExpiresAt:      token.ExpiresAt,
	}, nil
}
