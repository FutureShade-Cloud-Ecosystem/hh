package pos

// approver.go, the manager's credential at the clerk's till (row 482), as
// the return rules consume it: a return above the amount may carry the
// approver's own credential in its header beside the clerk's bearer, and
// the return commits as the manager's decision when that credential
// verifies through the same machinery any caller passes, belongs to the
// request org, names a person other than the clerk, resolves to a role on
// the configured approvers list whose grants reach the sale's branch, and
// was issued within the window the configuration sets. The credential's
// resolution is the guards' (ResolveApprover, injected here as the narrow
// seam); this file holds the RULE: who qualifies, the window read from the
// configuration, and what the committed decision names. Nothing here logs
// the credential.
//
// The resolution runs BEFORE the return's transaction opens (the lead's
// P3-6 ruling): no verifier call, membership read or authority read ever
// runs while the sale's lines are held for update. The in-transaction
// half is only the comparison against the computed total.

import (
	"context"
	"errors"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

// approverAuthorizationHeader is the contract's approver header; its value
// carries the same Bearer scheme prefix as Authorization. It is read once
// per request by the handler and never logged anywhere on this path.
const approverAuthorizationHeader = "X-Approver-Authorization"

// auditActionRefundCredentialRefused is the audit action of one return
// request refused because the approver credential it carried could not be
// judged (it does not verify, holds no standing in the org, or the
// machinery could not run): the row names the clerk and the sale (the
// lead's P3-5 ruling: a forged or foreign credential at the counter is in
// the trail).
const auditActionRefundCredentialRefused = "REFUND_APPROVAL_CREDENTIAL_REFUSED"

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

// approverDecision is one presented credential's judgment, carried out of
// the pre-transaction resolution into the return. pending true means a
// qualifying credential stands ready to make the decision; pending false
// with a nil error means no credential, or one that does not qualify, so
// the standing refusal answers if the rule fires. ownCredential true means
// the credential names the caller: the clerk of this return, whoever their
// role is, and the return refuses even when the rule would not have fired
// (the lead's P3-2 ruling: a listed manager attaching their own credential
// is refusing themselves, and the request does not slip through on the
// rule's silence).
type approverDecision struct {
	approver      guards.Approver
	pending       bool
	ownCredential bool
}

// resolveRefundApprover runs the rule over the presented credential. An
// error is a credential that could not be JUDGED (verification failed, no
// standing in the org, the machinery unwired or unreadable): the audit row
// is written here, so a forged or foreign credential at the counter is in
// the trail, and the caller refuses the request on the credential's own
// family, never reading as a manager who declined. ok false with a nil
// error is a credential that does not qualify (absent, stale, the clerk's
// own, an unlisted role, a grant short of the sale's branch): the calling
// rule answers the standing refusal, and the request is never committed on
// a half answer.
func (s *Service) resolveRefundApprover(ctx context.Context, credential string, caller identity.StaffContext, oc returnOrderCtx) (approverDecision, bool, error) {
	if credential == "" {
		return approverDecision{}, false, nil
	}
	// refuseJudgment writes the credential refusal's audit row and answers
	// the mapped family: the row names the clerk and the sale, the status
	// names why the credential could not be judged.
	refuseJudgment := func(status int, msg, reason string) (approverDecision, bool, error) {
		s.record(ctx, audit.SensitiveAccess{
			Action: auditActionRefundCredentialRefused, EntityType: "pos.return", EntityID: oc.OrderID,
			ActorID: identity.StaffUserIDFromContext(ctx), BranchID: oc.BranchID, Purpose: "POS_RETURN",
			Reason: refundApprovalRequiredError,
			Extra:  map[string]any{"status": reason},
		})
		return approverDecision{}, false, clientErr(status, "%s", msg)
	}
	org, ok := requestOrgFrom(ctx)
	if !ok || org.OrgSlug == "" {
		// The credential cannot be judged in-org without the request's
		// resolved org facts: fail closed, never commit on a half answer.
		return refuseJudgment(http.StatusServiceUnavailable,
			"pos: the approver credential cannot be resolved in the request org", "unjudgeable_no_request_org")
	}
	if s.approver == nil {
		return refuseJudgment(http.StatusServiceUnavailable,
			"pos: the approver credential resolver is not wired", "unjudgeable_resolver_unwired")
	}
	resolved, err := s.approver.ResolveApprover(ctx, credential, org.OrgSlug)
	if err != nil {
		switch {
		case errors.Is(err, authz.ErrSecondCredentialUnavailable):
			return refuseJudgment(http.StatusServiceUnavailable,
				"pos: the approver credential could not be checked", "unjudgeable_machinery_unavailable")
		case errors.Is(err, authz.ErrSecondCredentialForeign):
			return refuseJudgment(http.StatusForbidden,
				"pos: the approver credential holds no standing in this org", "no_standing_in_the_request_org")
		default:
			return refuseJudgment(http.StatusUnauthorized,
				"pos: the approver credential did not verify", "did_not_verify")
		}
	}
	// The window: the credential must have been issued within the window
	// the configuration sets (X7, plan 3.2) of the commit, an old bearer
	// passed forward from some earlier moment does not stand in for a
	// manager at the counter.
	if resolved.IssuedAt.Before(s.now().Add(-s.roles.RefundApprovalWindow())) {
		return approverDecision{}, false, nil
	}
	// A person other than the clerk: the same credential presented twice is
	// not a second pair of eyes, and the clerk cannot approve their own
	// refund. The refusal sticks even when the caller's role would not have
	// needed an approval: a presented credential naming the caller is the
	// one answer the not-the-clerk check exists for.
	if resolved.PlatformUserID == org.PlatformUserID {
		return approverDecision{ownCredential: true}, false, nil
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
	return approverDecision{approver: resolved, pending: true}, true, nil
}

// approverStaffID resolves the approver's linked staff users id, so the
// committed record and the audit row name both people in one identifier
// space (the lead's P2-4 ruling: the platform subject is a directory
// string the audit chokepoint may redact, the users id is a stable UUID).
func (s *Service) approverStaffID(ctx context.Context, platformUserID string) string {
	if s.userLink == nil {
		return ""
	}
	id, ok := s.userLink(ctx, platformUserID)
	if !ok {
		return ""
	}
	return id
}

// refusalTrail is the read capability on the audit recorder the committed
// decision uses to cite the refusal's own audit row: the rows filed under
// one sale, newest first, carry their ids. *audit.Service satisfies it
// structurally; a recorder without the read leaves the citation at the
// refusal's kind.
type refusalTrail interface {
	ReopenTransaction(ctx context.Context, entityType, entityID string) ([]audit.Entry, error)
}

// latestRefusalRowID returns the id of the most recent
// REFUND_APPROVAL_REFUSED row filed under the sale, empty when the trail
// cannot say: a first attempt that arrives already carrying the credential
// commits with no prior refusal to cite, and the kind alone is the
// citation then.
func (s *Service) latestRefusalRowID(ctx context.Context, orderID string) string {
	trail, ok := s.audit.(refusalTrail)
	if !ok {
		return ""
	}
	entries, err := trail.ReopenTransaction(ctx, "pos.return", orderID)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.Action == auditActionRefundApprovalRefused {
			return e.ID
		}
	}
	return ""
}
