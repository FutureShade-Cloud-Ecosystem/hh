package dispatch

// service_freightraise.go, what happens when the gate fires, and what happens when the queue answers.
//
// THREE ACTS, AND EACH ONE IS SOMEBODY ELSE'S:
//
//	PARK    the dispatcher asked. Nothing is written to the loads, a proposal is stored, an approval
//	        row is filed on the ONE queue this system has, and the dispatcher is told.
//	APPLY   an approver said yes. The split is written INSIDE the transaction that stamps the
//	        decision, so the money and the APPROVED stamp commit or roll back together.
//	REFUSE  an approver said no. The proposal is marked refused, in the same transaction, and not one
//	        cent of freight moves.
//
// NOTHING IN THIS FILE DECIDES ANYTHING. The verdict belongs to permission_approval_requests and to
// identity.CanApprove; a delivery_freight_raises row is the state that verdict applies. That is the
// gl_journals shape, and 0195's header argues at length why it is the right one here, the short
// version is that a dispatcher who has already typed four numbers should not have to type them again
// after somebody agrees to them.

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
	"github.com/jackc/pgx/v5"
)

// freightAsk carries the four figures and the shape of one proposal from the gate to the park, so
// SplitFreight's own signature does not grow six parameters that only ever travel together.
type freightAsk struct {
	quoted   int64
	current  int64
	proposed int64
	loads    int
	// amounts is the caller's own per-load override, or nil for "divide it evenly". nil is stored as
	// SQL NULL and re-divided at apply time; see 0195's column comment for why that is not the same
	// as materialising the even split now.
	amounts []int64
}

// quotedFreightCents reads the frozen baseline through the value seam.
//
// AN UNWIRED SEAM IS AN ERROR HERE AND NOT A "NO BASELINE". The two answers look alike and are
// opposites: "this order was never quoted" opens the gate (correctly, there is nothing to be above),
// while "I could not ask" must close it. Returning quoted=false for an unwired seam would make every
// order in the deployment ungatable the moment somebody forgot a wiring line, and the failure would
// be invisible: freight would simply rise, silently, exactly as it did before this feature existed.
func (s *Service) quotedFreightCents(ctx context.Context, orderID string) (int64, bool, error) {
	if s.values == nil {
		return 0, false, errors.New("dispatch: the order value seam is not wired")
	}
	return s.values.OrderQuotedFreightCents(ctx, orderID)
}

// parkFreightRaise stores the proposal, files the approval, and hands the dispatcher back a result
// that says, by the shape of it, that nothing was applied.
func (s *Service) parkFreightRaise(ctx context.Context, caller identity.StaffContext, brief OrderBrief, existing []Stop, ask freightAsk) (SplitResult, error) {
	// NIL IS REFUSE. If the queue is not wired there is nobody to ask, and applying a raise because
	// nobody was available to object is the failure this whole feature exists to prevent.
	if s.approvals == nil {
		return SplitResult{}, conflictf("dispatch: this freight is above the quoted delivery and the approvals queue is not reachable, nothing was changed")
	}

	// One parked raise per order. The unique index makes this true under a race; this read makes the
	// refusal a sentence instead of a constraint name, and lets an identical re-ask (a double click,
	// a retried request whose reply was lost) converge on the raise that is already waiting.
	if parked, found, err := s.repo.ParkedRaiseForOrder(ctx, brief.ID); err == nil && found {
		if sameAsk(parked, ask.proposed, ask.loads, ask.amounts) {
			return s.parkedResult(ctx, brief, existing, ask, parked), nil
		}
		return SplitResult{}, conflictf(
			"dispatch: a freight raise on this order is already waiting for approval, that one has to be decided before another can be asked for")
	} else if err != nil {
		return SplitResult{}, err
	}

	name, no := "", ""
	if len(existing) > 0 {
		name, no = existing[0].CustomerName, existing[0].OrderNo
	}
	raise, err := s.repo.InsertFreightRaise(ctx, FreightRaise{
		OrderID: brief.ID, BranchID: brief.BranchID, OrderNo: no, CustomerName: name,
		QuotedCents: ask.quoted, CurrentCents: ask.current, ProposedCents: ask.proposed,
		Loads: ask.loads, Amounts: ask.amounts,
		RequestedBy: caller.UserID, RequestedByRole: caller.RoleCode,
	})
	if err != nil {
		return SplitResult{}, asConflict(err, "dispatch: a freight raise on this order is already waiting for approval")
	}

	approvalID, err := s.approvals.FileFreightApproval(ctx, raise.ID, brief.BranchID, caller.UserID,
		raiseDetail(no, name))
	if err != nil {
		// The proposal exists and nothing will ever decide it, so it is removed rather than left to
		// occupy the order's one parked slot forever. Best-effort: a delete that also fails leaves an
		// inert row, which is strictly better than an applied raise nobody approved, the direction
		// every failure on this path leans.
		if derr := s.repo.DeleteParkedRaise(ctx, raise.ID); derr != nil {
			s.log.Error("dispatch: could not unwind a freight raise whose approval failed to file",
				"raise_id", raise.ID, "err", derr)
		}
		return SplitResult{}, err
	}
	if err := s.repo.AttachRaiseApproval(ctx, raise.ID, approvalID); err != nil {
		// The approval is filed and decidable; only the back-reference is missing, and the back-
		// reference is display. Log it and carry on rather than unwinding a request an approver may
		// already be looking at.
		s.log.Error("dispatch: freight raise filed but not linked to its approval",
			"raise_id", raise.ID, "approval_id", approvalID, "err", err)
	}
	raise.ApprovalID = approvalID

	// THE EXTRA KEYS ARE NOT ARBITRARY. audit.buildEntry is deny-by-default over an allowlist that
	// module owns (allowedExtraKeys), and a key outside it is dropped with a warning nobody reads -
	// which is how a trail ends up carrying an action and no facts. Every key below is on that list
	// and means here what it means everywhere else: `policy` names the rule, `subject_ref` is what it
	// applied to, `amount_cents` is the figure and `delta_cents` the new money in it.
	s.record(ctx, audit.SensitiveAccess{
		Action: "DELIVERY_FREIGHT_RAISE_PARKED", EntityType: "order", EntityID: brief.ID,
		BranchID: brief.BranchID, ActorID: caller.UserID, Purpose: "FREIGHT_ABOVE_QUOTE",
		Extra: map[string]any{
			"policy": string(identity.PolicyFreightAboveQuote), "subject_ref": raise.ID,
			"amount_cents": ask.proposed, "delta_cents": ask.proposed - ask.quoted,
			"status": RaiseParked,
		},
	})
	return s.parkedResult(ctx, brief, existing, ask, raise), nil
}

// parkedResult is the answer a parked split gives back: the loads AS THEY STILL ARE, the freight the
// order STILL charges, and the proposal that is waiting. Every number in it is the world before the
// ask, which is the honest thing to render on a screen where nothing happened.
func (s *Service) parkedResult(ctx context.Context, brief OrderBrief, existing []Stop, ask freightAsk, raise FreightRaise) SplitResult {
	out := SplitResult{OrderID: brief.ID, FreightCents: ask.current, Loads: []LoadStop{}}
	for _, st := range existing {
		out.Loads = append(out.Loads, s.loadStop(st, true, nil))
	}
	view := s.raiseView(ctx, raise)
	out.Parked = &view
	return out
}

// - the gated read -

// FreightRaiseByID is the card read behind an approval: what am I being asked to agree to?
//
// It is branch-fenced like every other read in this module, and its money is gated separately -
// reaching the row and being allowed to see the figures on it are two different permissions, and a
// caller who has the first and not the second gets the subject with no numbers rather than a 403 for
// a record they are entitled to know exists.
func (s *Service) FreightRaiseByID(ctx context.Context, id string) (FreightRaiseView, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return FreightRaiseView{}, err
	}
	raise, err := s.repo.GetFreightRaise(ctx, id)
	if errors.Is(err, errNoRaise) {
		return FreightRaiseView{}, ErrNotFound
	}
	if err != nil {
		return FreightRaiseView{}, err
	}
	if !caller.CanAccessBranch(raise.BranchID) {
		return FreightRaiseView{}, ErrForbidden
	}
	return s.raiseView(ctx, raise), nil
}

// raiseView is THE ONLY PLACE A FREIGHT RAISE'S MONEY IS PUT ON A PAYLOAD, and it is the only place
// that asks whether the caller may have it.
//
// That is the same shape loadStop has twenty lines from here and the same shape the quote pilot
// settled on: the gated figures live on their own type, exactly one method constructs it, and it has
// already asked the policy before it does. A caller the dealer's load-value policy refuses gets a
// FreightRaiseView with a nil Money pointer, which marshals with NO `money` key at all, not zero,
// not null, ABSENT. `null` is not absence; three trains on this tree have learned that, and the byte
// scan that guards this behaviour looks for the literal word.
func (s *Service) raiseView(ctx context.Context, r FreightRaise) FreightRaiseView {
	out := FreightRaiseView{
		ID: r.ID, OrderID: r.OrderID, OrderNo: r.OrderNo, CustomerName: r.CustomerName,
		BranchID: r.BranchID, Loads: r.Loads,
		AskedBy: actorWord(r.RequestedByRole), AskedAt: r.RequestedAt,
		State: r.State, ApprovalID: r.ApprovalID,
	}
	if s.loadValueAllowed(ctx, r.BranchID) {
		out.Money = &FreightRaiseMoney{
			QuotedCents: r.QuotedCents, CurrentCents: r.CurrentCents, ProposedCents: r.ProposedCents,
			DeltaCents: r.ProposedCents - r.QuotedCents,
		}
	}
	return out
}

// - the decide bridge -

// ApplyFreightRaiseTx applies a parked raise as the decider, on the approval decision's OWN
// transaction. It satisfies identity.FreightDecider.
//
// EVERYTHING IS RE-CHECKED HERE, against the rows as they are now and not as they were when the ask
// was filed. An approval can sit in a queue for a day, and in that day the loads can be cancelled,
// re-scheduled, or split by somebody else. The proposal is a request, not a promise about the world;
// applying it blindly would write a four-way division across two remaining trucks.
//
// Every refusal below returns a status-bearing error, which the composition root's adapter translates
// through identity.NewClientError so the approver reads a sentence instead of a 500, and which rolls
// the decision's own stamp back, leaving the request PENDING and decidable once the world is fixed.
func (s *Service) ApplyFreightRaiseTx(ctx context.Context, tx pgx.Tx, raiseID, deciderID string) error {
	raise, err := s.repo.LockFreightRaise(ctx, tx, raiseID)
	if errors.Is(err, errNoRaise) {
		return settings.Errf(http.StatusNotFound, "dispatch: the freight raise behind this approval is gone")
	}
	if err != nil {
		return err
	}
	if raise.State != RaiseParked {
		return conflictf("dispatch: this freight raise was already %s", strings.ToLower(raise.State))
	}

	loads, err := s.repo.LoadsForOrderTx(ctx, tx, raise.OrderID)
	if err != nil {
		return err
	}
	if len(loads) == 0 {
		return conflictf("dispatch: this order has no live loads any more, so there is nothing to charge the freight to")
	}
	if len(loads) > raise.Loads {
		return conflictf("dispatch: this order is now on %d loads and the raise was asked for %d, ask for it again against the loads as they stand",
			len(loads), raise.Loads)
	}
	// Mint the trucks the raise was asked for, on the decide transaction. THIS is where the fourth
	// truck appears, not at park time. Minting it when the ask was filed would have put a load on
	// the board that nobody had agreed to, which is precisely the "the split is not applied" clause.
	for seq := len(loads) + 1; seq <= raise.Loads; seq++ {
		if _, err := s.repo.InsertSplitLoadTx(ctx, tx, loads[0].ID, deciderID, seq, raise.Loads); err != nil {
			return asConflict(err, "dispatch: this order already has a load in that position")
		}
	}
	loads, err = s.repo.LoadsForOrderTx(ctx, tx, raise.OrderID)
	if err != nil {
		return err
	}

	amounts := raise.Amounts
	override := len(amounts) > 0
	if !override {
		amounts = EvenSplit(raise.ProposedCents, len(loads))
	}
	if len(amounts) != len(loads) {
		return conflictf("dispatch: this raise divides freight %d ways and the order is on %d loads", len(amounts), len(loads))
	}
	// The same validation the ask passed, run again against the figure being written. It cannot fail
	// for a proposal this service parked; it can fail for one edited underneath us, and the whole
	// point of a park-and-apply is that the apply is not a leap of faith.
	if err := ValidateOverride(amounts, raise.ProposedCents); err != nil {
		return err
	}

	ids := make([]string, len(loads))
	for i, st := range loads {
		ids[i] = st.ID
	}
	if err := s.repo.WriteFreightSplitTx(ctx, tx, ids, amounts, override, len(loads)); err != nil {
		return err
	}
	if s.freightWriter == nil {
		return conflictf("dispatch: the order freight seam is not wired, nothing was applied")
	}
	if err := s.freightWriter.SetOrderFreightTx(ctx, tx, raise.OrderID, raise.ProposedCents); err != nil {
		return err
	}
	applied, err := s.repo.MarkFreightRaiseDecided(ctx, tx, raise.ID, RaiseApplied, deciderID, s.now())
	if err != nil {
		return err
	}
	if !applied {
		return conflictf("dispatch: this freight raise was decided by somebody else")
	}

	s.recordRaiseDecision(ctx, raise, deciderID, RaiseApplied)
	return nil
}

// RefuseFreightRaiseTx marks a parked raise refused, on the decision's own transaction. Nothing is
// applied: no truck is minted, no share is written, and the order charges exactly what it charged
// before anybody asked. Satisfies identity.FreightDecider.
func (s *Service) RefuseFreightRaiseTx(ctx context.Context, tx pgx.Tx, raiseID, deciderID string) error {
	raise, err := s.repo.LockFreightRaise(ctx, tx, raiseID)
	if errors.Is(err, errNoRaise) {
		// A refusal with nothing to refuse is CONVERGENT, not an error. The approver said no and the
		// world already agrees with them; failing here would roll back a REJECTED stamp and leave the
		// request pending forever over a row that is not coming back.
		s.log.Warn("dispatch: freight raise refused but the proposal is gone", "raise_id", raiseID)
		return nil
	}
	if err != nil {
		return err
	}
	if raise.State != RaiseParked {
		return conflictf("dispatch: this freight raise was already %s", strings.ToLower(raise.State))
	}
	refused, err := s.repo.MarkFreightRaiseDecided(ctx, tx, raise.ID, RaiseRefused, deciderID, s.now())
	if err != nil {
		return err
	}
	if !refused {
		return conflictf("dispatch: this freight raise was decided by somebody else")
	}
	s.recordRaiseDecision(ctx, raise, deciderID, RaiseRefused)
	return nil
}

// recordRaiseDecision writes the trail for both outcomes, with the figures, keyed to the order the
// charge lands on and to the raise that carried it.
//
// IT RUNS INSIDE THE DECIDE TRANSACTION BUT NOT ON IT, and that is a trade worth stating rather than
// hiding. audit.Recorder has no transactional door (its Record takes a context and nothing else), so
// a decision that is rolled back AFTER this point would leave a trail row for money that never moved.
// The alternative, recording nothing, or recording only after a commit this module does not own -
// loses the trail for every decision, which is the direction that actually costs an auditor
// something. A trail is an index of who did what, never a precondition (service.go says so at
// record()); an extra row saying somebody TRIED is not the harmful error.
//
// THE ACTOR IS THE DECIDER, not the person who asked. The dispatcher is already the actor on the
// PARKED row this one names through subject_ref, so between the two rows both people are recorded
// exactly once, and an audit row whose actor did not perform the act it describes is the kind of
// trail that gets read wrong in the one meeting it matters in.
func (s *Service) recordRaiseDecision(ctx context.Context, r FreightRaise, deciderID, outcome string) {
	s.record(ctx, audit.SensitiveAccess{
		Action:     "DELIVERY_FREIGHT_RAISE_" + outcome,
		EntityType: "order", EntityID: r.OrderID, BranchID: r.BranchID, ActorID: deciderID,
		Purpose: "FREIGHT_ABOVE_QUOTE",
		Extra: map[string]any{
			"policy": string(identity.PolicyFreightAboveQuote), "subject_ref": r.ID,
			"outcome": outcome, "status": outcome,
			"amount_cents": r.ProposedCents, "delta_cents": r.ProposedCents - r.QuotedCents,
		},
	})
}
