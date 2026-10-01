package pos

// approver.go, the manager's credential at the clerk's till (row 482), as
// the return rules consume it: a return above the amount may carry the
// approver's own credential in its header beside the clerk's bearer, and
// the return commits as the manager's decision when that credential
// verifies through the same machinery any caller passes, belongs to the
// request org, names a person other than the clerk, resolves to a role on
// the configured approvers list whose grants reach the sale's branch, and
// was issued within the product's window. The credential's resolution is
// the guards' (ResolveApprover, injected here as the narrow seam); this
// file holds the RULE: who qualifies, the window, and what the committed
// decision names. Nothing here logs the credential.

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

// approverAuthorizationHeader is the contract's approver header; its value
// carries the same Bearer scheme prefix as Authorization. It is read once
// per request by the handler and never logged anywhere on this path.
const approverAuthorizationHeader = "X-Approver-Authorization"

// approverCredentialWindow is how old an approver's credential may be when
// the return carrying it commits, read from the credential's issued-at
// claim against the service clock. The window is a product constant held
// here in the return rules (the lead's decision for RT-B): the decided
// record's nine dealer settings carry no window, so the product holds it,
// and a tenth dealer setting later is a small additive change with a way
// back. Stated as code, not prose, per the repository rule.
const approverCredentialWindow = 5 * time.Minute

// ApproverSource resolves the credential carried beside the clerk's bearer
// through the same pieces the gate resolves any caller: verification,
// membership of the request org, authority from the app-role tables, the
// role's session ceiling. The concrete resolver is the guards'
// ResolveApprover; the seam is the narrow interface the rule needs, so the
// unit tests fake it and no return rule ever touches a token.
type ApproverSource interface {
	ResolveApprover(ctx context.Context, credential, orgSlug string) (guards.Approver, error)
}

// WithApproverSource injects the second-credential resolver. The
// composition wires the same Guards the routes registered behind, so the
// approver's credential rides exactly the machinery the clerk's did.
func (s *Service) WithApproverSource(a ApproverSource) *Service { s.approver = a; return s }

// approverDecision is one resolved, qualifying credential: the approver as
// the guards resolved them, carried out of the transaction so the
// committed record and the audit row can name the decision.
type approverDecision struct {
	approver guards.Approver
}

// resolveRefundApprover runs the rule over the presented credential.
// ok false is a credential that does not qualify (absent, stale, the
// clerk's own, an unlisted role, a grant short of the sale's branch): the
// calling rule answers the standing refusal, and the request is never
// committed on a half answer. An error is a credential that could not be
// JUDGED (verification failed, no standing in the org, the machinery
// unwired or unreadable): those refuse the request on their own families
// rather than passing for the standing refusal, so a broken credential
// never reads as a manager who declined.
func (s *Service) resolveRefundApprover(ctx context.Context, credential string, caller identity.StaffContext, oc returnOrderCtx) (approverDecision, bool, error) {
	if credential == "" {
		return approverDecision{}, false, nil
	}
	org, ok := requestOrgFrom(ctx)
	if !ok || org.OrgSlug == "" {
		// The credential cannot be judged in-org without the request's
		// resolved org facts: fail closed, never commit on a half answer.
		return approverDecision{}, false, clientErr(http.StatusServiceUnavailable,
			"pos: the approver credential cannot be resolved in the request org")
	}
	if s.approver == nil {
		return approverDecision{}, false, clientErr(http.StatusServiceUnavailable,
			"pos: the approver credential resolver is not wired")
	}
	resolved, err := s.approver.ResolveApprover(ctx, credential, org.OrgSlug)
	if err != nil {
		switch {
		case errors.Is(err, authz.ErrSecondCredentialUnavailable):
			return approverDecision{}, false, clientErr(http.StatusServiceUnavailable,
				"pos: the approver credential could not be checked")
		case errors.Is(err, authz.ErrSecondCredentialForeign):
			return approverDecision{}, false, clientErr(http.StatusForbidden,
				"pos: the approver credential holds no standing in this org")
		default:
			return approverDecision{}, false, clientErr(http.StatusUnauthorized,
				"pos: the approver credential did not verify")
		}
	}
	// The window: the credential must have been issued within the product's
	// window of the commit, an old bearer passed forward from some earlier
	// moment does not stand in for a manager at the counter.
	if resolved.IssuedAt.Before(s.now().Add(-approverCredentialWindow)) {
		return approverDecision{}, false, nil
	}
	// A person other than the clerk: the same credential presented twice is
	// not a second pair of eyes, and the clerk cannot approve their own
	// refund.
	if resolved.PlatformUserID == org.PlatformUserID {
		return approverDecision{}, false, nil
	}
	// The role must be on the configured approvers list; an unknown role is
	// on no list (fail closed).
	holds, err := s.roles.ApprovesRefundAboveAmount(resolved.RoleID)
	if err != nil || !holds {
		return approverDecision{}, false, nil
	}
	// The approval belongs to the sale's branch (X7): the approver's grants
	// must reach it, by the same rule the guards apply to any caller.
	if !resolved.CanAccessBranch(oc.BranchID) {
		return approverDecision{}, false, nil
	}
	return approverDecision{approver: resolved}, true, nil
}
