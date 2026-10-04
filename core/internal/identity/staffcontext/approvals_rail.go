package staffcontext

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// approvals_rail.go: the identity side of the generic approvals rail (0075). Identity stays the source of
// truth for approval STATE (permission_approval_requests) and AUTHORITY (CanApprove); this file adds two
// nil-able seams so a rail can render + notify without identity importing messaging or gl:
//
//   - ApprovalNotifier: fan APPROVAL cards out to eligible approvers + echo decisions (a messaging adapter).
//   - JEDecider:        bridge a JE_OVER_THRESHOLD approve to the SHIPPED GL maker-checker (a gl adapter).
//
// Both are wired in the composition root; nil = shipped behavior (the kill switch). The interfaces are declared HERE
// (the consuming package) so the adapters live in the wire functions and there is no import cycle.
//
// CARRY NOTE: at the pin these methods hung off the ERP identity Service; the
// session-issuing half of that type stays behind with the HS256 world, so the
// rail hangs off Approvals (approvals.go), the rail's own service type inside
// this package. The signatures, the seams and the behaviour are the carried
// ones; only the receiver moved, and the consumers (gl in a later batch,
// dispatch and messaging after it) take the rail through constructors, never
// through the superseded Service.

// ApprovalNotifier is identity's fan-out seam. Best-effort: identity calls it log-and-continue, so a
// messaging outage never fails CreateApproval or a decision.
type ApprovalNotifier interface {
	// ApprovalRequested fans one APPROVAL card to each eligible approver's per-person channel.
	ApprovalRequested(ctx context.Context, n ApprovalNotice)
	// ApprovalDecided echoes a decision across the rail (stamp cards + EVENT into approver channels + SSE
	// the requester's own-request status).
	ApprovalDecided(ctx context.Context, n ApprovalDecidedNotice)
}

// ApprovalNotice is one requested-approval fan-out: the card fields + the resolved eligible approver ids.
type ApprovalNotice struct {
	ApprovalID  string
	Policy      PolicyType
	SubjectRef  string
	BranchID    string
	AmountCents int64
	RequestedBy string
	Detail      string
	DeepLink    string
	ApproverIDs []string
}

// ApprovalDecidedNotice is one decision echo.
type ApprovalDecidedNotice struct {
	ApprovalID  string
	Status      string // APPROVED | REJECTED
	DecidedBy   string
	Note        string
	RequestedBy string
}

// JEDecider bridges a JE_OVER_THRESHOLD rail decision to the SHIPPED GL maker-checker. ApproveJournalTx
// posts the parked DRAFT journal as the decider WITHIN the approval-decision's transaction (0075), so the
// ledger POST and the request-row APPROVED stamp commit or roll back atomically: a GL failure rolls the
// stamp back too, and a concurrent reject can never leave the ledger POSTED against a REJECTED request.
// deciderID is the resolved rail approver; GL enforces four-eyes (not the maker) + DRAFT-state and returns a
// typed 403/409 the wire adapter translates via NewClientError so identity surfaces the right status.
type JEDecider interface {
	ApproveJournalTx(ctx context.Context, tx pgx.Tx, journalID, deciderID string) error
}

// FreightDecider bridges a FREIGHT_ABOVE_QUOTE rail decision to the PARKED freight raise dispatch
// owns (0195). It is the JE bridge's sibling and shares its atomicity contract: both methods run
// WITHIN the approval-decision's transaction, so the split and the APPROVED stamp, or the refusal
// and the REJECTED stamp, commit or roll back together.
//
// IT HAS TWO METHODS BECAUSE A FREIGHT REFUSAL HAS SOMEWHERE TO GO. The JE bridge has only an
// approve arm: a declined journal simply stays DRAFT, and voiding it is the maker's own act. A
// declined freight raise cannot stay parked: the row holds the order's one-parked-raise slot, and a
// dispatcher reading it would be told a decision is still coming after one was made. So the decline
// is a write, and it is on this transaction rather than after it for the reason every part of this
// bridge is: a stamp whose mirror failed to move is a lie the next read believes.
//
// Neither method takes an amount. identity never learns what the raise is worth, which is the whole
// posture of 0195: the money lives behind dispatch's own visibility policy and this module has no
// business holding a copy of it in order to route a decision.
type FreightDecider interface {
	// ApplyFreightRaiseTx applies the parked split as the decider. dispatch re-reads the proposal
	// under the transaction's own lock and refuses anything it can no longer honour, returning a
	// status-bearing error the wire adapter translates through NewClientError.
	ApplyFreightRaiseTx(ctx context.Context, tx pgx.Tx, raiseID, deciderID string) error
	// RefuseFreightRaiseTx marks the parked proposal refused. Nothing is applied: no load is minted,
	// no freight moves, and the order is charged exactly what it was charged before.
	RefuseFreightRaiseTx(ctx context.Context, tx pgx.Tx, raiseID, deciderID string) error
}

// WithApprovalNotifier attaches the rail fan-out seam (nil = no cards/echoes; approvals still work via the
// pending-approvals surface). Chainable.
func (s *Approvals) WithApprovalNotifier(n ApprovalNotifier) *Approvals { s.notifier = n; return s }

// WithJEDecider attaches the JE decide-to-GL bridge (nil = the shipped GL maker-checker continues untouched,
// a JE rail-approve simply stamps the request without posting). Chainable.
func (s *Approvals) WithJEDecider(d JEDecider) *Approvals { s.jeDecider = d; return s }

// WithFreightDecider attaches the freight decide-to-dispatch bridge (nil = a decision on a freight card
// stamps the request and applies nothing, which is the safe half of the kill switch: money never
// moves without the bridge, and re-wiring it makes the parked proposals decidable again). Chainable.
func (s *Approvals) WithFreightDecider(d FreightDecider) *Approvals { s.freightDecider = d; return s }

// NewClientError builds a status-bearing error in identity's taxonomy. It is the boundary constructor a
// wire adapter uses to translate a sibling module's typed error (e.g. GL's 409 on the JE decide-to-GL
// bridge) into an identity error the handler maps faithfully, instead of a generic 500.
func NewClientError(status int, msg string) error { return clientError{status: status, msg: msg} }

// ApprovalErrorStatus is NewClientError's read half: the HTTP status a
// rail client error carries, so a caller outside this package (a handler,
// an integration harness) maps the taxonomy faithfully instead of
// pattern-matching on messages. ok false is everything else (the 500
// family and pass-throughs).
func ApprovalErrorStatus(err error) (status int, ok bool) {
	var ce clientError
	if errors.As(err, &ce) {
		return ce.status, true
	}
	return 0, false
}

// FileJEApproval files a JE_OVER_THRESHOLD request for a parked DRAFT journal and fans out cards. The GL
// park-bridge calls it best-effort (the park never depends on it). requestedBy is the JE creator, so the
// four-eyes guard blocks them from approving their own entry (belt-and-suspenders with GL's own check).
func (s *Approvals) FileJEApproval(ctx context.Context, journalID, branchID string, amountCents int64, requestedBy string) (ApprovalRequest, error) {
	return s.fileApproval(ctx, ApprovalRequest{
		RequestedBy: requestedBy,
		BranchID:    branchID,
		Policy:      PolicyJEThreshold,
		Detail:      fmt.Sprintf("Journal entry at/over the maker-checker threshold (%d cents) awaiting approval", amountCents),
		SubjectRef:  journalID,
		AmountCents: amountCents,
	})
}

// FileFreightApproval files a FREIGHT_ABOVE_QUOTE request for a parked freight raise and fans out
// cards (0195). dispatch's gate calls it after parking the proposal; requestedBy is the dispatcher who
// asked, so four-eyes stops them approving their own raise.
//
// NOTE THE MISSING ARGUMENT. FileJEApproval takes an amountCents and rides it in the request's `data`
// JSONB so the card can show a magnitude. This one does not, and the omission is the design: the
// figures on a freight raise are a customer's delivery charge, GET /api/v1/staff/approvals is open to
// every authenticated staff member, and there is no money gate on that list. `detail` is a sentence
// with no numbers in it; the quoted-versus-proposed pair reaches a card only through dispatch's own
// gated read, which has asked the dealer's load-value policy first.
func (s *Approvals) FileFreightApproval(ctx context.Context, raiseID, branchID, requestedBy, detail string) (ApprovalRequest, error) {
	return s.fileApproval(ctx, ApprovalRequest{
		RequestedBy: requestedBy,
		BranchID:    branchID,
		Policy:      PolicyFreightAboveQuote,
		Detail:      detail,
		SubjectRef:  raiseID,
	})
}

// notifyApprovalRequested resolves the eligible approvers for a freshly-filed request and fans out cards
// (best-effort). The zero-eligible-approver edge is logged (not silently dropped): the request stays
// decidable via the pending-approvals surface and re-fans on the reconcile sweep once an approver exists.
func (s *Approvals) notifyApprovalRequested(ctx context.Context, req ApprovalRequest) {
	if s.notifier == nil {
		return
	}
	approvers, err := s.resolveApproverIDs(ctx, req.Policy, req.BranchID, req.RequesterRole)
	if err != nil {
		s.log.Error("identity: approvals fan-out could not resolve approvers", "approval_id", req.ID, "err", err)
		return
	}
	if len(approvers) == 0 {
		s.log.Error("identity: approval has no eligible approver to fan to",
			"approval_id", req.ID, "policy", req.Policy, "branch", req.BranchID)
	}
	s.notifier.ApprovalRequested(ctx, ApprovalNotice{
		ApprovalID:  req.ID,
		Policy:      req.Policy,
		SubjectRef:  req.SubjectRef,
		BranchID:    req.BranchID,
		AmountCents: req.AmountCents,
		RequestedBy: req.RequestedBy,
		Detail:      req.Detail,
		DeepLink:    approvalDeepLink(req),
		ApproverIDs: approvers,
	})
}

// notifyApprovalDecided echoes a won decision across the rail (best-effort).
func (s *Approvals) notifyApprovalDecided(ctx context.Context, req ApprovalRequest, decidedBy, note string) {
	if s.notifier == nil {
		return
	}
	s.notifier.ApprovalDecided(ctx, ApprovalDecidedNotice{
		ApprovalID:  req.ID,
		Status:      string(req.Status),
		DecidedBy:   decidedBy,
		Note:        note,
		RequestedBy: req.RequestedBy,
	})
}

// resolveApproverIDs resolves the eligible approver user ids for a (policy, branch): the candidate pool
// filtered through CanApprove. The margin arm reads the CONFIGURED floor set the service was wired with
// (D11, CP0-1 answer B): the fan-out notifies exactly the authority the decide path holds, so a branch
// floor request reaches the branch's Sales manager, never only the carried default set.
func (s *Approvals) resolveApproverIDs(ctx context.Context, policy PolicyType, branchID, requesterRole string) ([]string, error) {
	candidates, err := s.store.ListApproverCandidates(ctx)
	if err != nil {
		return nil, err
	}
	return ResolveApproversFloor(candidates, policy, branchID, requesterRole, s.floorApprovers), nil
}

// CanDecideRequest answers whether the caller could decide one loaded
// request under the CONFIGURED floor set the service was wired with (D11),
// the same rule the Decide path runs (four eyes and PENDING are the
// caller's own to check): the caller's authority decides the request's
// policy in its branch, a margin request answers to the branch scoped arm
// only when a salesperson role filed it (row 743), AND the decide path's
// line-bound rule holds (P2-C): a margin request with no line binding, a
// legacy card filed before the quote desk's door, is decidable by the
// dealer wide floor set alone. The queue's count and list read this, so
// they show exactly what the caller could decide, nothing it would only
// appear to hold. There is no policy only form: the rule needs the request.
func (s *Approvals) CanDecideRequest(caller StaffContext, r ApprovalRequest) bool {
	return canDecideRequestWith(s.floorApprovers, caller, r)
}

// canDecideRequestWith is CanDecideRequest over an explicit floor set, the
// one rule the rail and the sync door both hold.
func canDecideRequestWith(fa FloorApprovers, caller StaffContext, r ApprovalRequest) bool {
	if !CanApproveFloor(caller, r.Policy, r.BranchID, r.RequesterRole, fa) {
		return false
	}
	return !(r.Policy == PolicyMargin && r.LineID == "" && !fa.mayDecideDealerWide(caller.RoleCode))
}

// MayReadRequest answers whether the caller may read one request's card.
// A floor request's detail carries the resulting margin and the floor in
// basis points, which with the discount and the product name the cost, and
// the approvals reads have no money gate of their own (P2-3, round 3): the
// caller reads a floor request only when it could decide it or filed it
// itself. Every other policy reads as before.
func (s *Approvals) MayReadRequest(caller StaffContext, r ApprovalRequest) bool {
	return mayReadRequestWith(s.floorApprovers, caller, r)
}

// mayReadRequestWith is MayReadRequest over an explicit floor set.
func mayReadRequestWith(fa FloorApprovers, caller StaffContext, r ApprovalRequest) bool {
	if r.Policy != PolicyMargin {
		return true
	}
	if caller.UserID != "" && r.RequestedBy == caller.UserID {
		return true
	}
	return canDecideRequestWith(fa, caller, r)
}

// ApprovalReconcilePending is one still-PENDING request the messaging sweep must ensure has a card in each
// currently-eligible approver's channel (identity-owned type; the wire adapter maps it into messaging).
type ApprovalReconcilePending struct {
	ApprovalID  string
	Policy      PolicyType
	SubjectRef  string
	BranchID    string
	AmountCents int64
	RequestedBy string
	Detail      string
	DeepLink    string
	ApproverIDs []string
}

// ApprovalReconcileDecided is one recently-decided request whose rail copies must reflect the decision.
type ApprovalReconcileDecided struct {
	ApprovalID  string
	Status      string
	DecidedBy   string
	RequestedBy string
}

// ApprovalReconcile returns the current desired rail state for the messaging reconcile sweep: still-PENDING
// requests (with freshly-resolved eligible approvers) and requests decided since `decidedSince`. It is the
// identity side of the reconcile seam; the wire adapter maps it into messaging.ReconcilePlan.
func (s *Approvals) ApprovalReconcile(ctx context.Context, decidedSince time.Time) ([]ApprovalReconcilePending, []ApprovalReconcileDecided, error) {
	rows, err := s.store.ApprovalsForReconcile(ctx, decidedSince)
	if err != nil {
		return nil, nil, err
	}
	candidates, err := s.store.ListApproverCandidates(ctx)
	if err != nil {
		return nil, nil, err
	}
	var pending []ApprovalReconcilePending
	var decided []ApprovalReconcileDecided
	for _, r := range rows {
		switch r.Status {
		case ApprovalPending:
			pending = append(pending, ApprovalReconcilePending{
				ApprovalID:  r.ID,
				Policy:      r.Policy,
				SubjectRef:  r.SubjectRef,
				BranchID:    r.BranchID,
				AmountCents: r.AmountCents,
				RequestedBy: r.RequestedBy,
				Detail:      r.Detail,
				DeepLink:    approvalDeepLink(r),
				// The same configured authority the filed fan-out and the
				// decide path hold (D11): a sweep re-fans to exactly who may
				// decide, so a newly configured branch approver's card
				// arrives and a deconfigured one's stops.
				ApproverIDs: ResolveApproversFloor(candidates, r.Policy, r.BranchID, r.RequesterRole, s.floorApprovers),
			})
		case ApprovalApproved, ApprovalRejected:
			decided = append(decided, ApprovalReconcileDecided{
				ApprovalID:  r.ID,
				Status:      string(r.Status),
				DecidedBy:   r.DecidedBy,
				RequestedBy: r.RequestedBy,
			})
		}
	}
	return pending, decided, nil
}

// approvalDeepLink builds a best-effort deep link back into the app for a card (JE to the journal; else the
// subject, else the request). Presentation-only; the frontend refines it.
func approvalDeepLink(r ApprovalRequest) string {
	if r.Policy == PolicyJEThreshold && r.SubjectRef != "" {
		return "/gl/journals/" + r.SubjectRef
	}
	if r.SubjectRef != "" {
		return "/approvals?subject=" + r.SubjectRef
	}
	return "/approvals?id=" + r.ID
}
