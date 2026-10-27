package pos

// returns.go, the W7 POS return: idempotent, over-return-guarded, one atomic tx across the money rail
// (CASH drawer pay-out / CARD processor reversal / ACCOUNT_CREDIT), the optional restock (RETURN moves
// at the current weighted average through the pos-declared seam) and the two GL events. The till stays
// the SOLE writer of payments + drawer movements; inventory stays the sole stock writer.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/jackc/pgx/v5"
)

// qtyEpsilon absorbs DECIMAL(19,4) float round-trip noise when comparing quantities.
const qtyEpsilon = 1e-6

// returnableQtyOpen is the ONE quantity-open derivation, called by the commit guard (returnInTx's
// over-return comparison) and by the customer lines read (ListCustomerReturnableOrders' scan): the
// line's sold quantity less every quantity already returned on it, both sides of the subtraction
// read from the same shared prior-return sums. One function, so the screen's open quantity and the
// guard's remaining quantity are the same number.
func returnableQtyOpen(qtySold, qtyReturned float64) float64 {
	return qtySold - qtyReturned
}

// The customer lines read's bounds: the orders answered are newest first and bounded, the counter's
// own list discipline (the sale lookup's numbers).
const (
	defaultCustomerLinesLimit = 25
	maxCustomerLinesLimit     = 100
)

// errWalkInNotReturnable refuses the customer lines read for the house WALK-IN row (sub item 10.1):
// its sales are everybody's walk-in sales, not one customer's history; the words send the clerk
// to the customer on the sale, or to a card or cash refund of the walk-in sale itself.
var errWalkInNotReturnable = clientErr(http.StatusUnprocessableEntity,
	"pos: a swap is rung for a customer's own account, not the WALK-IN house account; return the walk-in sale to the card it was paid on or in cash, and ring the new order apart")

// errWalkInNeedsNumber answers the WALK-IN house account's lines read without a number: its sales are
// everybody's walk-in sales, so they are never listed, only found by the number on the receipt.
var errWalkInNeedsNumber = clientErr(http.StatusUnprocessableEntity,
	"pos: walk-in sales are never listed; find the sale by the receipt or order number, and it goes back to the card it was paid on or in cash")

// errCustomerOrNumber answers a read or a write that names neither a customer nor a walk-in sale.
var errCustomerOrNumber = clientErr(http.StatusBadRequest,
	"pos: name the customer, or give the receipt or order number of a walk-in sale")

// customerOrWalkIn is how every returns door reads its customer: a named customer is that customer;
// an empty customer_id together with a receipt or order number is the house WALK-IN row, as the ring
// treats an empty customer, so no app has to carry the row's id; an empty customer_id with no
// number is refused in words.
func (s *Service) customerOrWalkIn(ctx context.Context, customerID, number string) (string, error) {
	customerID = strings.TrimSpace(customerID)
	if customerID != "" {
		return customerID, nil
	}
	if normalizeSaleRef(number) == "" {
		return "", errCustomerOrNumber
	}
	walkIn, err := s.store.ResolveWalkIn(ctx)
	if err != nil {
		return "", err
	}
	if walkIn == "" {
		return "", errCustomerOrNumber
	}
	return walkIn, nil
}

// CustomerLines is the customer lines read (sub item 10.1): every sales order the customer holds in
// HH, till rings and quote conversions alike (the widened Q1 ruling), newest first, each line with
// the quantity the commit guard would accept and the money and tax already back. The branch wall is
// the counter's own: a scoped caller sees only the orders their resolved authority reaches (the
// others are simply absent), and naming a branch the caller does not reach is the wall's 403 naming
// the branch.
func (s *Service) CustomerLines(ctx context.Context, in CustomerLinesInput) (CustomerLinesView, error) {
	var cerr error
	if in.CustomerID, cerr = s.customerOrWalkIn(ctx, in.CustomerID, in.Number); cerr != nil {
		return CustomerLinesView{}, cerr
	}
	caller, err := s.caller(ctx)
	if err != nil {
		return CustomerLinesView{}, err
	}
	// The house WALK-IN row (ruling A): its sales are found only through a receipt or order number
	// filter, never listed, and the filter never leaves the customer's own orders; its refunds go to
	// cash or card only (D2).
	in.Number = normalizeSaleRef(in.Number)
	if walkIn, werr := s.store.ResolveWalkIn(ctx); werr == nil && walkIn != "" && walkIn == in.CustomerID && in.Number == "" {
		return CustomerLinesView{}, errWalkInNeedsNumber
	}
	// The branch wall: naming a branch the caller's resolved authority does not reach is the same 403
	// the return itself answers; naming none answers every branch the caller reaches (a global caller
	// every branch), the absent orders simply left out.
	branchIDs := reachableBranches(caller)
	if in.BranchID = strings.TrimSpace(in.BranchID); in.BranchID != "" {
		if err := requireBranchReach(caller, in.BranchID); err != nil {
			return CustomerLinesView{}, err
		}
		branchIDs = []string{in.BranchID}
	}
	if in.Limit <= 0 {
		in.Limit = defaultCustomerLinesLimit
	}
	if in.Limit > maxCustomerLinesLimit {
		in.Limit = maxCustomerLinesLimit
	}
	items, err := s.store.ListCustomerReturnableOrders(ctx, in.CustomerID, branchIDs, in.Limit, in.Number)
	if err != nil {
		return CustomerLinesView{}, err
	}
	if items == nil {
		items = []CustomerOrderView{}
	}
	// Returnable: a till ring always; an order a quote became only with a live finalised invoice, read
	// through billing's seam. The invoice the return would refund against rides invoice_id.
	for i := range items {
		items[i].Returnable = true
		if items[i].Source != "QUOTE" {
			continue
		}
		if s.orderInvoices == nil {
			items[i].Returnable, items[i].NotReturnableReason = false, "orders a quote became cannot be returned here yet"
			continue
		}
		id, status, found, ierr := s.orderInvoices.LiveOrderInvoice(ctx, items[i].OrderID)
		if ierr != nil {
			return CustomerLinesView{}, ierr
		}
		if why := convertedNotReturnable(found, status); why != "" {
			items[i].Returnable, items[i].NotReturnableReason = false, why
			continue
		}
		items[i].InvoiceID = id
	}
	// Each order names its branch, so the one branch refusal can name the branches. The names come
	// through the branches module's seam; an unwired seam leaves them empty, never invented.
	if s.branchNames != nil && len(items) > 0 {
		names, nerr := s.branchNames.BranchNames(ctx)
		if nerr != nil {
			return CustomerLinesView{}, nerr
		}
		for i := range items {
			items[i].BranchName = names[items[i].BranchID]
		}
	}
	return CustomerLinesView{CustomerID: in.CustomerID, Items: items}, nil
}

// reservedRequestKeyPrefix is the idempotency-key namespace ApproveReturnRequest derives its rail key
// from ("rreq:" + request id). It is RESERVED: a public return may never use it (a colliding key would
// hijack a PENDING request's replay slot and stamp the wrong money onto its decision).
const reservedRequestKeyPrefix = "rreq:"

// auditActionRefundApprovalRefused is the audit action of one return the
// till refused for want of an approval: the row names the clerk, the sale
// and the amount (D49).
const auditActionRefundApprovalRefused = "REFUND_APPROVAL_REFUSED"

// refundApprovalRequiredError is the error string the contract pins as the
// discriminator of the approval refusal (the 409 body's Error envelope;
// the approval key rides beside it, present exactly when this refusal is
// the one the till's screen shows the manager dialog for).
const refundApprovalRequiredError = "refund_approval_required"

// ErrRefundApprovalRequired refuses one return above the configured
// amount from a role that holds no approval: AmountCents is the
// configured amount without approval, Approvers the role keys that hold
// it, both as the configuration sets them (X7), and RefundCents is the
// refused refund's own total, the fact the audit row keeps. The handler
// maps it to the contract's 409.
type ErrRefundApprovalRequired struct {
	AmountCents int64
	Approvers   []string
	RefundCents int64
}

func (e ErrRefundApprovalRequired) Error() string {
	return "pos: the refund is above the approval amount and the caller's role holds no approval"
}

// restocker returns the injected restock seam, defaulting to the behavior-preserving no-op.
func (s *Service) restocker() StockRestocker {
	if s.restock != nil {
		return s.restock
	}
	return NoopRestock{}
}

// Return commits one atomic POS return against ONE original committed sale. Idempotent on the client
// key (ux_pos_returns_idem); the over-return guard runs per order line under FOR UPDATE inside the tx.
// Money: per-line refund = qty × original unit price (capped so Σ refunds never exceed the line total);
// tax refunds proportionally from the line's ORIGINAL tax_cents and Σ per line NEVER exceeds it.
//
// The transactional body is returnInTx, over a CALLER-SUPPLIED transaction: Return resolves everything
// that reads the pool first (prepareReturn, while no connection is held), then runs the body inside its
// own RunInTx. A caller already inside a transaction (ApproveReturnRequest) runs the same body on ITS
// transaction, so the money and the decision commit atomically, and no second acquisition ever asks the
// pool for a connection while one is held.
func (s *Service) Return(ctx context.Context, in ReturnInput) (Return, error) {
	plan, err := s.prepareReturn(ctx, in)
	if err != nil {
		return Return{}, err
	}
	// The pre-transaction replay check answered: the key already carries its committed return, judged
	// against the caller's reach of the replayed return's own sale by the same wall. No transaction,
	// no rail, no completion audit row.
	if plan.preReplayed {
		return plan.preReplay, nil
	}
	var out Return
	var replayedHere, approvedHere bool
	// The two-transaction card refund (HH-690): short tx 1 locks the sale, guards the lines, runs
	// the approval gate and stages the attempt IN_FLIGHT, then COMMITS and releases every DB lock;
	// the processor call runs outside any DB transaction; short tx 2 (returnInTx below) books the
	// return against the staged reversal ref. No restock row lock, invoice lock or payments counter
	// is ever held across the processor call.
	if in.RefundMethod == RefundCard && !in.railOptional {
		err = s.store.RunInTx(ctx, func(tx pgx.Tx) error {
			var txErr error
			out, replayedHere, _, txErr = s.stageCardReturnInTx(ctx, tx, plan)
			return txErr
		})
		if err != nil {
			return s.returnFailed(ctx, plan, err)
		}
		if replayedHere {
			return out, nil
		}
		if derr := s.dispatchStagedCardRefunds(ctx, []*returnPlan{plan}); derr != nil {
			return s.returnFailed(ctx, plan, derr)
		}
		out = Return{}
		replayedHere = false
	}
	err = s.store.RunInTx(ctx, func(tx pgx.Tx) error {
		var txErr error
		out, replayedHere, approvedHere, txErr = s.returnInTx(ctx, tx, plan)
		return txErr
	})
	if err != nil {
		return s.returnFailed(ctx, plan, err)
	}
	s.settleCardRefunds(ctx, []*returnPlan{plan}, nil)
	// The in-transaction duplicate answered its twin's replay: the twin's
	// own completion row already exists, this answer writes none.
	if replayedHere {
		return out, nil
	}

	completed := audit.SensitiveAccess{
		Action: audit.ActReturnCompleted, EntityType: "pos.return", EntityID: out.ReturnID,
		ActorID: plan.actor, BranchID: plan.oc.BranchID, Purpose: "POS_RETURN", Reason: in.RefundMethod,
		Extra: map[string]any{"return_no": out.ReturnNo, "total_cents": out.TotalCents},
	}
	if approvedHere {
		// The manager's decision, beside the clerk who rang it (row 482).
		// Both people are named in one identifier space, the linked staff
		// users id (the lead's P2-4 ruling: the platform subject is a
		// directory string the audit chokepoint may redact), and the
		// platform subject rides subject_ref as the directory reference.
		// approver_id, amount_cents and subject_ref are allowlisted audit
		// keys, so these are the facts the row keeps; amount_cents is the
		// audit vocabulary's own name for an integer money fact, here the
		// refused refund's total.
		if plan.approverStaff != "" {
			completed.Extra["approver_id"] = plan.approverStaff
		}
		completed.Extra["subject_ref"] = plan.decision.approver.PlatformUserID
		completed.Extra["amount_cents"] = out.TotalCents
		completed.Reason = refundApprovalRequiredError
	}
	s.record(ctx, completed)
	return out, nil
}

// returnFailed is the one failure path of the single return, shared by both transactions of the
// two-transaction card refund and the plain single-transaction body: the approval refusal writes
// its own audit row (D49), a concurrent duplicate surfaces the winner's replay, and the card
// refund facts the dead attempt leaves behind are settled on record.
func (s *Service) returnFailed(ctx context.Context, plan *returnPlan, err error) (Return, error) {
	// The approval refusal writes its own audit row naming the clerk,
	// the sale and the amount (D49): the money asked for, refused at
	// the counter, is exactly the fact the trail exists to keep.
	var refusal ErrRefundApprovalRequired
	if errors.As(err, &refusal) {
		s.record(ctx, audit.SensitiveAccess{
			Action: auditActionRefundApprovalRefused, EntityType: "pos.return", EntityID: plan.oc.OrderID,
			ActorID: plan.actor, BranchID: plan.oc.BranchID, Purpose: "POS_RETURN",
			Reason: refundApprovalRequiredError,
			Extra:  map[string]any{"amount_cents": refusal.RefundCents},
		})
		return Return{}, refusal
	}
	// A concurrent duplicate hit ux_pos_returns_idem and rolled back, surface the winner (replay),
	// judged against the winner's own sale by the same wall.
	if isUniqueViolationErr(err) {
		if cached, found, ferr := s.replayIfAllowed(ctx, plan.in.IdempotencyKey, plan.caller); ferr == nil && found {
			s.settleCardRefunds(ctx, []*returnPlan{plan}, nil)
			return cached, nil
		}
	}
	s.settleCardRefunds(ctx, []*returnPlan{plan}, err)
	return Return{}, err
}

// stageCardReturnInTx is short tx 1 of the two-transaction card refund (HH-690): the per-sale lock,
// the in-transaction replay re-check, the line guard, the approval gate and the capture ceiling all
// run HERE, then the attempt is staged IN_FLIGHT in pos_card_refund_attempts and the transaction
// commits, releasing every DB lock before the processor is called. It answers the replay when the
// key's twin already committed, and whether a card amount was staged.
func (s *Service) stageCardReturnInTx(ctx context.Context, tx pgx.Tx, plan *returnPlan) (Return, bool, bool, error) {
	in, oc, caller := plan.in, plan.oc, plan.caller
	if err := s.store.LockSaleForReturn(ctx, tx, oc.OrderID); err != nil {
		return Return{}, false, false, err
	}
	cached, found, rerr := s.replayIfAllowedTx(ctx, tx, in.IdempotencyKey, caller)
	if rerr != nil {
		return Return{}, false, false, rerr
	}
	if found {
		return cached, true, false, nil
	}
	g, gerr := s.guardReturnLines(ctx, tx, oc, in.Lines, in.Restock)
	if gerr != nil {
		return Return{}, false, false, gerr
	}
	total := g.subtotal + g.taxTotal
	if _, aerr := s.returnApprovalGateTx(ctx, tx, plan, total); aerr != nil {
		return Return{}, false, false, aerr
	}
	// The rail amount tx 2 will book: the split of an order that is not fully paid decides it the
	// same way commitReturnTx does, so the staged amount and the booked amount can be compared.
	railTotal := total - plan.applied
	if plan.applied == 0 {
		sp, serr := s.railSplitFor(ctx, tx, oc, in.RefundMethod, total)
		if serr != nil {
			return Return{}, false, false, serr
		}
		if sp.rail == 0 && sp.offset > 0 {
			return Return{}, false, false, clientErr(http.StatusUnprocessableEntity, "%s", sp.note)
		}
		railTotal = total - sp.offset
	}
	plan.stagedRailTotal = railTotal
	if railTotal > 0 {
		if err := s.checkCardCapTx(ctx, tx, oc, "", "refund:"+in.IdempotencyKey, railTotal); err != nil {
			return Return{}, false, false, err
		}
		existing, stored, err := s.store.StageCardRefundAttemptTx(ctx, tx, cardRefundFact{
			RefundKey: "refund:" + in.IdempotencyKey, OrderID: oc.OrderID, BranchID: oc.BranchID,
			CustomerID: oc.CustomerID, GatewayRef: oc.GatewayRef, AmountCents: railTotal,
			InitiatedBy: plan.actor,
		})
		if err != nil {
			return Return{}, false, false, err
		}
		plan.stagedRefundRef = existing
		plan.stagedRefundCents = stored
	}
	return Return{}, false, true, nil
}

// returnApprovalGateTx is the single return's refund approval gate (X7, the CUMULATIVE rule) on the
// open transaction after the per-sale lock: the exact block returnInTx has always run, shared with
// short tx 1 of the two-transaction card refund so the refusal lands before any processor call. It
// answers whether a manager's decision stands behind the return (the caller proceeds), or the
// refusal.
func (s *Service) returnApprovalGateTx(ctx context.Context, tx pgx.Tx, plan *returnPlan, total int64) (bool, error) {
	if plan.in.internalRail {
		return false, nil
	}
	prior, err := s.store.SumCommittedReturnTotalsTx(ctx, tx, plan.oc.OrderID)
	if err != nil {
		return false, err
	}
	cumulative := prior + total
	refusal, refused := s.refundApprovalRefusal(cumulative, total, plan.caller)
	// Above the amount, a presented credential must qualify, whoever carried it (the lead's P2-B
	// ruling): a failed judgment refuses on its own family, a credential that does not qualify
	// refuses with the contract's body, the configured amount and the configured approvers.
	if plan.credentialJudged && s.roles.RefundRequiresApproval(cumulative) {
		if plan.judgeErr != nil {
			return false, plan.judgeErr
		}
		if !plan.decision.pending {
			refusal = s.standingRefusalFor(total)
			refused = true
		}
	}
	if refused {
		if !plan.decision.pending {
			return false, refusal
		}
		return true, nil
	}
	return false, nil
}

// dispatchStagedCardRefunds is the BETWEEN-transactions step of the two-transaction card refund
// (HH-690): NO DB transaction is open and NO DB lock is held. Each staged plan calls the processor
// with tx nil (the pool-backed entry point), records the reversal's ref UNBOOKED on the pool the
// moment the processor answers, and keeps the fact for tx 2's settle. A plan whose staging already
// found an earlier UNBOOKED attempt's ref reuses it and never calls the processor a second time. A
// processor refusal closes its own and every later still-IN_FLIGHT attempt FAILED (no money moved),
// leaves the earlier dispatched children UNBOOKED on record, and answers the 502.
func (s *Service) dispatchStagedCardRefunds(ctx context.Context, plans []*returnPlan) error {
	for i, p := range plans {
		if p.in.RefundMethod != RefundCard || p.in.railOptional || p.stagedRailTotal <= 0 {
			continue
		}
		refundKey := "refund:" + p.in.IdempotencyKey
		fact := cardRefundFact{
			RefundKey: refundKey, OrderID: p.oc.OrderID, BranchID: p.oc.BranchID,
			CustomerID: p.oc.CustomerID, GatewayRef: p.oc.GatewayRef, AmountCents: p.stagedRailTotal,
			InitiatedBy: p.actor,
		}
		// An earlier attempt under the same key already refunded at the processor: reuse its ref
		// and ITS amount (the staging compared the bodies, so the two agree here).
		if p.stagedRefundRef != "" {
			fact.RefundRef = p.stagedRefundRef
			fact.AmountCents = p.stagedRefundCents
			p.cardRefunds = append(p.cardRefunds, fact)
			continue
		}
		// The live request fence measures from the start of THIS child's call (PR #501 round 2
		// P2-1): the start is stamped on its own row immediately before the call, so a later
		// child that starts after its staging window is fenced for its own window. A row a
		// manager already settled is never sent to the processor.
		var refundRef string
		stampKeys := []string{refundKey}
		for _, later := range plans[i+1:] {
			if later.in.RefundMethod == RefundCard && !later.in.railOptional && later.stagedRailTotal > 0 && later.stagedRefundRef == "" {
				stampKeys = append(stampKeys, "refund:"+later.in.IdempotencyKey)
			}
		}
		started, serr := s.store.StampCardRefundCallStart(context.WithoutCancel(ctx), stampKeys)
		var err error
		switch {
		case serr != nil:
			err = fmt.Errorf("could not stamp the card refund call start: %w", serr)
		case !started:
			err = errCardRefundAlreadyResolved
		default:
			refundRef, err = s.cardRefund.RefundCard(ctx, nil, p.oc.GatewayRef, p.stagedRailTotal, p.oc.OrderNo, refundKey)
		}
		if err != nil {
			reason := trimReason(err.Error())
			// The failed child: only a decline or a not-dispatched failure is KNOWN to have
			// moved no money, so only those close FAILED. Anything else (an unknown outcome, a
			// transport timeout, an unclassified error) holds the attempt IN_FLIGHT with its
			// reason: the amount stays reserved against the capture ceiling and the same-key
			// retry refuses instead of refunding again (HH-690 round 2, P1-2).
			if errors.Is(err, errCardRefundAlreadyResolved) {
				// The manager settled this row before its call began: nothing was sent and
				// the row is theirs; only the later children close below.
				slog.Default().Error("pos: a card refund attempt was settled by a manager before its processor call began; the call was not sent",
					"refund_key", refundKey)
				if merr := s.store.MarkCardRefundCallNotSent(context.WithoutCancel(ctx), refundKey); merr != nil {
					slog.Default().Error("pos: the never sent card refund call could not be written on its row", "refund_key", refundKey, "err", merr)
				}
			} else if isCardRefundNoMoneyMoved(err) {
				if ferr := s.store.FailCardRefundAttempt(context.WithoutCancel(ctx), refundKey, reason); ferr != nil {
					slog.Default().Warn("pos: the refused card refund attempt could not be closed", "err", ferr)
				}
			} else if herr := s.store.HoldCardRefundAttemptInFlight(context.WithoutCancel(ctx), refundKey, reason); herr != nil {
				slog.Default().Warn("pos: the unknown-outcome card refund attempt could not be held", "err", herr)
			}
			// Every LATER child was never sent over the wire: no money can have moved for any
			// of them, so they close FAILED whatever the failed child's own classification.
			for _, late := range plans[i+1:] {
				if late.in.RefundMethod != RefundCard || late.in.railOptional || late.stagedRailTotal <= 0 || late.stagedRefundRef != "" {
					continue
				}
				if ferr := s.store.FailCardRefundAttempt(context.WithoutCancel(ctx), "refund:"+late.in.IdempotencyKey, reason); ferr != nil {
					slog.Default().Warn("pos: the undispatched card refund attempt could not be closed", "err", ferr)
				}
			}
			// The children that DID refund keep their own record: the failed child's reason
			// describes that child, not them.
			s.settleCardRefundsWithReason(ctx, plans, "a later card refund of this return did not go through; this refund was made at the processor and is not booked yet")
			return clientErr(http.StatusBadGateway, "pos: card refund failed, %s", err.Error())
		}
		p.stagedRefundRef = refundRef
		p.stagedRefundCents = p.stagedRailTotal
		fact.RefundRef = refundRef
		p.cardRefunds = append(p.cardRefunds, fact)
		if derr := s.store.RecordCardRefundDispatched(context.WithoutCancel(ctx), fact); derr != nil {
			// Best effort by the same discipline as settleCardRefunds: the fact rides the plan,
			// so tx 2's settle closes the row BOOKED on commit or UNBOOKED with the ref on rollback.
			slog.Default().Warn("pos: the dispatched card refund record could not be written", "err", derr)
		}
	}
	return nil
}

// isCardRefundNoMoneyMoved answers whether a card refund failure is KNOWN to have moved no money:
// the processor's own decline, or a failure that happened before anything reached it (HH-690
// round 2, P1-2). Every other failure (an unknown outcome, a transport timeout, an unclassified
// error) may have refunded, so its attempt is held open, never closed FAILED.
func isCardRefundNoMoneyMoved(err error) bool {
	var decline *payment.DeclineError
	return errors.As(err, &decline) ||
		errors.Is(err, payment.ErrProcessorDeclined) ||
		errors.Is(err, payment.ErrProcessorNotDispatched)
}

// returnPlan is everything the return resolves BEFORE any transaction
// opens: the validated input, the original sale's context, the caller and
// the approver facts. Every read here runs on the pool while no connection
// is held; the transactional body (returnInTx) touches only the transaction
// it is handed. A caller already inside its own transaction
// (ApproveReturnRequest) can therefore run the rail on ITS transaction:
// asking the pool for a second connection while the first is held is the
// starvation a pool of pool-size concurrent rails dies of, every holder
// waiting for a connection the pool can never give.
type returnPlan struct {
	// cardRefunds are the processor refunds this return made inside its transaction (D4): read by
	// the caller after the transaction ended to record the ones a rollback left unbooked.
	cardRefunds []cardRefundFact
	// stagedRailTotal and stagedRefundRef are the two-transaction card refund's staging facts
	// (HH-690): short tx 1 computed the rail total and staged the attempt, and the dispatch
	// outside any DB transaction either holds the reversal's processor ref or an earlier
	// UNBOOKED attempt's ref it reused. stagedRefundCents is the amount the processor actually
	// refunded under that ref (the stored amount of the reused attempt, or the fresh dispatch's
	// own amount): tx 2 compares against IT, never against the new body's figure alone
	// (HH-690 round 2, P1-1). Tx 2 (commitReturnTx) reuses the ref instead of calling the
	// processor again, and refuses in words if the sale's balance moved under it.
	stagedRailTotal   int64
	stagedRefundRef   string
	stagedRefundCents int64
	// applied is the part of this return\'s total spent on a swap\'s new order (10.5): the rail moves
	// total less applied. Set inside the swap\'s transaction once the guard has priced the note.
	applied int64
	// offset and paymentNote are the ruling\'s split of an order that is not fully paid: the part that
	// comes off the open invoice, and the split in words.
	offset         int64
	paymentNote    string
	in             ReturnInput
	oc             returnOrderCtx
	caller         identity.StaffContext
	actor          string
	pickupRequired bool
	pickupStatus   string
	pickupSite     string
	pickupDate     string
	decision       approverDecision
	judgeErr       error
	// credentialJudged records that a presented credential went through the
	// resolver, whatever it answered: the in-transaction gate consults the
	// verdict only when the CUMULATIVE actually crosses the amount.
	credentialJudged bool
	approverStaff    string
	refusalRowID     string
	// preReplay carries the committed return the pre-transaction replay
	// check answered: set means no transaction and no rail body at all, the
	// caller receives this return (Return) or stamps it as the decision
	// (ApproveReturnRequest).
	preReplay   Return
	preReplayed bool
	// restockProducts are the products whose balance rows the return's restock will touch, resolved
	// before the transaction opens (a pool read of the sale's lines, immutable once rung): the body
	// takes them, sorted through inventory's own seam, before its first counter draw, so the guard's
	// own restock set inside the transaction is always a subset of rows already held.
	restockProducts []string
}

// prepareReturn is the return's pre-transaction section, run while no
// connection is held: validation, the sale's context, the branch wall, the
// replay pre-check, the approver resolution and the decision-record names.
// It returns the plan the transactional body consumes.
func (s *Service) prepareReturn(ctx context.Context, in ReturnInput) (*returnPlan, error) {
	plan := &returnPlan{in: in}
	if in.IdempotencyKey == "" {
		return nil, errMissingKey
	}
	// The "rreq:" namespace belongs to the return-request approval rail alone (see the const above)
	// and the "note:" namespace to the across-orders note's derived child keys: a public key in
	// either would squat a reserved replay slot. A note's own children carry theirs derived.
	if !in.internalRail && strings.HasPrefix(in.IdempotencyKey, reservedRequestKeyPrefix) {
		return nil, errReservedKey
	}
	if !in.noteChild && strings.HasPrefix(in.IdempotencyKey, reservedNoteKeyPrefix) {
		return nil, errReservedKey
	}
	if len(in.Lines) == 0 {
		return nil, errNoReturnLines
	}
	switch in.RefundMethod {
	case RefundCash, RefundCard, RefundAccountCredit:
	default:
		return nil, errBadRefundMethod
	}
	if in.OrderID == "" && in.OrderNo == "" {
		return nil, clientErr(http.StatusBadRequest, "pos: order_id or order_no is required")
	}
	for _, ln := range in.Lines {
		if ln.OrderLineID == "" || ln.QtyBase <= 0 {
			return nil, clientErr(http.StatusUnprocessableEntity, "pos: every return line needs an order_line_id and a positive qty_base")
		}
	}
	// A-06 restocking fee: bounded bps, ACCOUNT_CREDIT only (the fee reduces the credited amount, a
	// cash/card refund never nets a fee here).
	if in.FeeBps < 0 || in.FeeBps > 10000 {
		return nil, errBadFeeBps
	}
	if in.FeeBps > 0 && in.RefundMethod != RefundAccountCredit {
		return nil, errFeeNeedsCredit
	}

	// The original sale: must be a committed POS sale; the return runs at ITS branch.
	callerForRef, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	ref := normalizeSaleRef(in.OrderNo)
	oc, found, err := s.store.ReturnOrderContextFor(ctx, in.OrderID, ref, reachableBranches(callerForRef))
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errReturnNotFound
	}
	// An order_id and an order_no that name different sales are refused outright: the id would win
	// and the number would be silently ignored, so a clerk's typo could ring a return on a sale they
	// did not mean.
	if in.OrderID != "" && ref != "" {
		named, namedFound, nerr := s.store.ReturnOrderContextFor(ctx, "", ref, reachableBranches(callerForRef))
		if nerr != nil {
			return nil, nerr
		}
		if !namedFound || named.OrderID != oc.OrderID {
			return nil, clientErr(http.StatusConflict, "pos: order_id and order_no name different sales")
		}
	}
	plan.oc = oc
	// The branch wall runs BEFORE any rail, before any write and before the
	// idempotent replay (X2, fixing HH-218; the lead's P2-2 ruling): no
	// processor reversal, no drawer payout and no account credit may move
	// for a sale the caller's resolved authority does not reach, and the
	// replay of a foreign sale refuses exactly like the first attempt, so
	// another branch's return number, sale, invoice, payment id and money
	// are not a thing a key alone buys. The return runs at the sale's
	// branch; the caller brings their own grants, resolved per request by
	// the guards, never claimed.
	caller := callerForRef
	plan.caller = caller
	if err := requireBranchReach(caller, plan.oc.BranchID); err != nil {
		return nil, err
	}
	if cached, found, err := s.replayIfAllowed(ctx, in.IdempotencyKey, caller); err != nil {
		return nil, err
	} else if found {
		plan.preReplay = cached
		plan.preReplayed = true
		return plan, nil
	}
	// A converted order's invoice comes from billing, after the wall and the replay (a replay answers
	// from its stored return and needs no live invoice).
	if plan.oc, err = s.resolveConverted(ctx, plan.oc); err != nil {
		return nil, err
	}
	// The stock rows the restock will touch, read here while no connection is held: the body's stock
	// pre-lock (returnInTx) consumes them ahead of its first counter draw.
	if plan.restockProducts, err = s.restockProductsFor(ctx, plan.oc.OrderID, in); err != nil {
		return nil, err
	}
	if in.RefundMethod == RefundCash && in.TillSessionID == "" && !in.railOptional {
		return nil, ErrNoOpenTill{}
	}
	if in.RefundMethod == RefundAccountCredit && !in.railOptional {
		if err := s.accountCreditAllowed(ctx, plan.oc.CustomerID, in.internalRail); err != nil {
			return nil, err
		}
	}
	if in.RefundMethod == RefundCard && !in.railOptional {
		if plan.oc.SaleTender != TenderCard || plan.oc.GatewayRef == "" {
			return nil, errCardRefundNoRef
		}
		if s.cardRefund == nil {
			return nil, errNoCardRefunder
		}
	}

	// The gated paths fail closed (the lead's P1-1 ruling): a counter
	// return on a service built with no role configuration refuses, it
	// never skips the refund rule. The A-06 portal rail keeps its
	// exemption: the queue's own decision already approved that request.
	//
	// The approver credential resolves BEFORE the transaction opens (the
	// lead's P3-6 ruling): no verifier call, membership read or authority
	// read ever runs while the sale's lines are held for update; only the
	// comparison against the computed total stays inside. A presented
	// credential is judged wherever it rides: a return that ends up under
	// the amount commits ordinarily, a credential that cannot be judged
	// refuses on its own family with its audit row.
	if !in.internalRail {
		if s.roles == nil {
			return nil, errRoleConfigUnwired()
		}
		if in.ApproverCredential != "" && s.refundMayNeedApproval(ctx, plan.oc, in) {
			resolved, pending, rerr := s.resolveRefundApprover(ctx, in.ApproverCredential, caller, plan.oc)
			plan.decision = resolved
			plan.decision.pending = pending
			plan.judgeErr = rerr
			plan.credentialJudged = true
		}
	}
	// The decision record's names, read before the transaction opens (the
	// lead's P3-D ruling: no trail read under the sale's row locks).
	if plan.decision.pending {
		plan.approverStaff = s.approverStaffID(ctx, plan.decision.approver.PlatformUserID)
		plan.refusalRowID = s.latestRefusalRowID(ctx, plan.oc.OrderID)
	}

	if in.Pickup != nil && in.Pickup.Required {
		plan.pickupRequired = true
		plan.pickupStatus = PickupRequested
		plan.pickupSite = in.Pickup.Site
		plan.pickupDate = in.Pickup.RequestedDate
	} else {
		plan.pickupStatus = PickupNone
	}

	plan.actor = identity.StaffUserIDFromContext(ctx)
	return plan, nil
}

// returnInTx is the return's transactional body over a caller-supplied
// transaction: the per-sale lock, the in-transaction replay re-check, the
// line locks and guards, the money rail, the restock and the two GL events,
// every database touch on tx, none on the pool. Return runs it inside its
// own RunInTx; ApproveReturnRequest runs it on its decision transaction, so
// the money and the decision commit atomically. It reports the two facts
// the post-transaction audit reads: whether the answer is a same-key twin's
// replay (no completion row) and whether the rule fired and a presented
// credential was spent (the completion row names the decision).
func (s *Service) returnInTx(ctx context.Context, tx pgx.Tx, plan *returnPlan) (out Return, replayedHere, approvedHere bool, err error) {
	// The plan's facts, aliased to the names the body has always read.
	in := plan.in
	oc := plan.oc
	caller := plan.caller

	// Serialize every return of the sale before any line lock: the line
	// locks below serialize only returns of the SAME line, two concurrent
	// returns of DIFFERENT lines of one sale would each read the prior
	// sum before the other commits and both slip under the refund
	// approval amount. Every Return takes it, the A-06 portal rail
	// included: its committed rows must count in later sums too. The
	// lock is transaction-scoped, released at commit or rollback.
	if err := s.store.LockSaleForReturn(ctx, tx, oc.OrderID); err != nil {
		return Return{}, false, false, err
	}
	// The return's restock writes stock rows, and every writer that touches those rows before a
	// counter takes them stock first (the ring does, and the cancel's settlement now does too, round
	// 2): take the restock's rows, sorted through inventory's own seam, BEFORE the return's first
	// counter draw (the invoice locks below draw the payments one on the cash and card paths) and
	// before any processor refund call, so against either writer the two only queue (the Opus read's
	// P2-1). The set is the plan's own candidate set, read before the transaction opened; the guard's
	// restock set inside is a subset of it (the sale's lines are immutable once rung).
	if err := s.lockRestockRowsTx(ctx, tx, oc.BranchID, plan.restockProducts); err != nil {
		return Return{}, false, false, err
	}
	if !in.railOptional {
		// The account rail offsets invoices; a cash or card return offsets them too when the order is
		// not fully paid (the ruling's split): take them, sorted, before the first offset (P2-1).
		// lockReturnInvoicesTx draws the payments counter itself, and only when it will take invoice
		// rows: the payments module's one money lock order is counter first, invoices second (every
		// settle, received payment and reversal takes them that way), so a return that takes an
		// invoice row must hold the counter before it (the Opus review's P2-4), while a return on a
		// fully paid sale with no deposit document locks no invoice and must not hold the one counter
		// row every payments writer serialises on at all (the delta reviews' P2).
		if err := s.lockReturnInvoicesTx(ctx, tx, []returnOrderCtx{oc}, in.RefundMethod != RefundAccountCredit); err != nil {
			return Return{}, false, false, err
		}
	}
	// A same-key duplicate that arrived while its twin held the lock
	// passed the pre-transaction replay check before either row existed.
	// The lock has now serialized them, so whatever is committed under
	// this key is final: the duplicate answers it here as the replay,
	// the same shape as the pre-transaction check (the cached return,
	// judged against its own sale by the branch wall), committing this
	// now-empty transaction with no gate, no rail and no audit row.
	// Without the re-check the duplicate's own gate would read its
	// twin's committed row into the prior sum and answer a false
	// refusal (or trip the over-return guard) for a return that
	// committed. The re-check reads ON the transaction (replayIfAllowedTx):
	// a pool read while this transaction holds a connection would
	// deadlock a full pool of Returns waiting on the sale's lock.
	cached, found, rerr := s.replayIfAllowedTx(ctx, tx, in.IdempotencyKey, caller)
	if rerr != nil {
		return Return{}, false, false, rerr
	}
	if found {
		out = cached
		replayedHere = true
		return out, replayedHere, approvedHere, nil
	}

	// 1. Lock + guard every requested line, derive its refund money/tax (the shared guard half).
	g, gerr := s.guardReturnLines(ctx, tx, oc, in.Lines, in.Restock)
	if gerr != nil {
		return Return{}, false, false, gerr
	}
	total := g.subtotal + g.taxTotal

	// The refund rule reads the dealer's configuration (X7) and counts
	// every return against the same sale together: the sale's earlier
	// committed returns, read inside this transaction after the per-sale
	// lock above so the prior total is exact, ride the comparison, and
	// the gate judges the CUMULATIVE. A return above the configured
	// amount from a role not on the approvers list is refused HERE,
	// after every line is guarded and before any write and any rail, so
	// the refusal commits nothing and touches nobody; returns each under
	// the amount can no longer walk a sale out of the drawer one
	// approved-sized slice at a time. At or under the amount no approver
	// is ever asked. The A-06 portal rail skips this gate: the queue's
	// own decision already approved that request, and the counter rule
	// is not a second veto over it.
	//
	// The manager's credential at the clerk's till (row 482): the same
	// return carried again with the approver's credential in its header
	// commits as the manager's decision when the credential qualified
	// in the pre-transaction resolution (approver.go); anything short of
	// that leaves the standing refusal in place. Short tx 1 of the
	// two-transaction card refund runs the SAME gate (returnApprovalGateTx)
	// so a refusal lands before any processor call, with zero rows in
	// pos_card_refund_attempts.
	var gerr2 error
	approvedHere, gerr2 = s.returnApprovalGateTx(ctx, tx, plan, total)
	if gerr2 != nil {
		return Return{}, false, false, gerr2
	}

	// 2 through 5: the header, the rail, the restock and the events (the shared commit half).
	out, cerr := s.commitReturnTx(ctx, tx, plan, g, "", approvedHere)
	if cerr != nil {
		return Return{}, false, false, cerr
	}
	return out, replayedHere, approvedHere, nil
}

// lockRestockRowsTx takes the branch's balance rows of the products a return's restock will touch,
// sorted through inventory's own seam, ahead of the return's first counter draw: the same pre-lock
// the swap takes for its two halves (swap.go), on the seam the deploy wires for it. A nil seam or an
// empty set locks nothing (a return that restocks nothing holds no stock row).
func (s *Service) lockRestockRowsTx(ctx context.Context, tx pgx.Tx, branchID string, productIDs []string) error {
	if s.stockLocks == nil || len(productIDs) == 0 {
		return nil
	}
	return s.stockLocks.LockStockTx(ctx, tx, branchID, productIDs)
}

// restockProductsFor answers the products whose balance rows the return's restock will touch: every
// requested line whose disposition puts the goods back on the shelf, explicitly RESTOCK or by the
// return's own restock flag (the same rule applyDisposition resolves per line inside the
// transaction). A line that turns out credit only (goods that never left the shelf) writes no stock,
// but its row is locked anyway: one sorted pass over a stable superset, never a row the guard's own
// set could miss. Read on the pool before the transaction opens.
func (s *Service) restockProductsFor(ctx context.Context, orderID string, in ReturnInput) ([]string, error) {
	lineRestocks := func(req ReturnLineInput) bool {
		switch req.Disposition {
		case DispositionRestock:
			return true
		case "":
			return in.Restock
		}
		return false
	}
	requested := make(map[string]bool, len(in.Lines))
	for _, ln := range in.Lines {
		if lineRestocks(ln) {
			requested[ln.OrderLineID] = true
		}
	}
	if len(requested) == 0 {
		return nil, nil
	}
	lines, err := s.store.ListReturnableLines(ctx, orderID)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(lines))
	var out []string
	for _, ln := range lines {
		if !requested[ln.OrderLineID] || ln.ProductID == "" || seen[ln.ProductID] {
			continue
		}
		seen[ln.ProductID] = true
		out = append(out, ln.ProductID)
	}
	sort.Strings(out)
	return out, nil
}

// guardedReturn is the per-line guard result every return body consumes: the derived refund lines
// with their money and tax, the stock and event projections of the same lines, and the sums. One
// code path for the single-order return and for every child of an across-orders note, so a note
// child's line guard is exactly the single return's own.
type guardedReturn struct {
	lines      []returnLineRow
	stockLines []StockLine
	eventLines []map[string]any
	// writeOffLines are the damaged goods written off (pos.return.written_off), product and quantity.
	writeOffLines []map[string]any
	subtotal      int64
	taxTotal      int64
}

// guardReturnLines locks and guards every requested line of ONE order under FOR UPDATE and derives
// its refund money and tax: the shared step 1 of the single return's transactional body
// (returnInTx) and of every child of a note (ReturnAcross). The over-return refusal and the money
// invariants are the single return's own words, unchanged.
func (s *Service) guardReturnLines(ctx context.Context, tx pgx.Tx, oc returnOrderCtx, reqs []ReturnLineInput, defaultRestock bool) (guardedReturn, error) {
	return guardLines(oc, reqs, defaultRestock, func(orderLineID string) (returnableLine, bool, error) {
		return s.store.LockReturnableLine(ctx, tx, oc.OrderID, orderLineID)
	})
}

// previewReturnLines is the credit preview's guard: the SAME guardLines over the same prior-return
// sums, read without the lock (a preview takes none and writes nothing).
func (s *Service) previewReturnLines(ctx context.Context, oc returnOrderCtx, reqs []ReturnLineInput) (guardedReturn, error) {
	return guardLines(oc, reqs, false, func(orderLineID string) (returnableLine, bool, error) {
		return s.store.ReadReturnableLine(ctx, oc.OrderID, orderLineID)
	})
}

// guardLines is the ONE guard and pricing walk over a return's requested lines, the single order
// return's, every child of a note's and the credit preview's: read is the line source (locked in a
// transaction, or a plain read for the preview). A line named twice in one request is refused in
// words: each entry is guarded against what was committed BEFORE the request, so two entries
// could together return more than is open.
func guardLines(oc returnOrderCtx, reqs []ReturnLineInput, defaultRestock bool, read func(orderLineID string) (returnableLine, bool, error)) (guardedReturn, error) {
	lines := make([]returnLineRow, 0, len(reqs))
	var subtotal, taxTotal int64
	stockLines := make([]StockLine, 0, len(reqs))
	eventLines := make([]map[string]any, 0, len(reqs))
	writeOffLines := make([]map[string]any, 0, len(reqs))
	seen := make(map[string]bool, len(reqs))
	for _, req := range reqs {
		if seen[req.OrderLineID] {
			return guardedReturn{}, clientErr(http.StatusUnprocessableEntity,
				"pos: line %s appears twice on order %s, send it once with the whole quantity", req.OrderLineID, oc.OrderNo)
		}
		seen[req.OrderLineID] = true
		rl, ok, lerr := read(req.OrderLineID)
		if lerr != nil {
			return guardedReturn{}, lerr
		}
		if !ok {
			return guardedReturn{}, clientErr(http.StatusUnprocessableEntity, "pos: line %s does not belong to order %s", req.OrderLineID, oc.OrderNo)
		}
		row, perr := priceReturnLine(rl, req)
		if perr != nil {
			return guardedReturn{}, perr
		}
		if derr := applyDisposition(&row, rl, req, defaultRestock); derr != nil {
			return guardedReturn{}, derr
		}
		lines = append(lines, row)
		subtotal += row.LineTotalCents
		taxTotal += row.TaxCents
		// Only goods that physically went back on the shelf move stock and reverse COGS, and only
		// goods that physically came back damaged are written off: the rest is credit only.
		if row.RestockedBase > 0 {
			stockLines = append(stockLines, StockLine{ProductID: rl.ProductID, QtyBase: row.RestockedBase})
			eventLines = append(eventLines, map[string]any{"product_id": rl.ProductID, "qty_base": row.RestockedBase})
		}
		if row.WrittenOffBase > 0 {
			writeOffLines = append(writeOffLines, map[string]any{"product_id": rl.ProductID, "qty_base": row.WrittenOffBase})
		}
	}
	// Sorted by product: a restock takes its balance rows in the one order every move does.
	sort.SliceStable(stockLines, func(i, j int) bool { return stockLines[i].ProductID < stockLines[j].ProductID })
	return guardedReturn{lines: lines, stockLines: stockLines, eventLines: eventLines, writeOffLines: writeOffLines,
		subtotal: subtotal, taxTotal: taxTotal}, nil
}

// applyDisposition resolves what became of one returned line's goods and how much of it physically
// went where (sub item 10.3, the D1 fix and the Q4 and Q5 rulings). A line without a disposition
// takes the whole return's restock flag (true RESTOCK, false NO_RETURN). The physical part of the
// return is what had left the shelf and not yet come back: min(returned qty, left the shelf less
// every earlier return of the line). A RESTOCK puts the physical part back on the shelf, a DAMAGED
// writes the physical part off; the rest of the quantity (goods that never left the shelf) is credit
// only, the money reversal whole and no stock or cost entry at all.
func applyDisposition(row *returnLineRow, rl returnableLine, req ReturnLineInput, defaultRestock bool) error {
	disp := req.Disposition
	switch disp {
	case "":
		disp = DispositionNoReturn
		if defaultRestock {
			disp = DispositionRestock
		}
	case DispositionRestock, DispositionDamaged, DispositionNoReturn:
	default:
		return clientErr(http.StatusUnprocessableEntity,
			"pos: disposition on line %s must be RESTOCK, DAMAGED or NO_RETURN", req.OrderLineID)
	}
	// The goods that came back so far are the earlier returns less their credit only parts: a credit
	// only return brought nothing back, so goods that ship after it are still returnable in full.
	physical := math.Min(req.QtyBase, math.Max(0, rl.LeftShelfBase-(rl.PriorQty-rl.PriorCreditOnly)))
	if physical < qtyEpsilon {
		physical = 0
	}
	row.Disposition = disp
	row.CreditOnlyBase = req.QtyBase - physical
	switch disp {
	case DispositionRestock:
		row.RestockedBase = physical
	case DispositionDamaged:
		row.WrittenOffBase = physical
	}
	return nil
}

// priceReturnLine is the ONE pricing function of a return line: the over-return guard and the
// refund money and tax of the requested quantity against the line's sold quantity and its prior
// returns. The commit (single order and note children) and the credit preview both call it, so the
// preview and the commit can never differ.
func priceReturnLine(rl returnableLine, req ReturnLineInput) (returnLineRow, error) {
	remainingQty := returnableQtyOpen(rl.QtySold, rl.PriorQty)
	if req.QtyBase > remainingQty+qtyEpsilon {
		return returnLineRow{}, clientErr(http.StatusUnprocessableEntity,
			"pos: over-return on line %s, %.4f requested but only %.4f returnable", req.OrderLineID, req.QtyBase, remainingQty)
	}
	var money, tax int64
	if math.Abs(req.QtyBase-remainingQty) <= qtyEpsilon {
		// Returning everything still out: pay out exactly what remains so the line zeroes.
		money = rl.LineTotalCents - rl.PriorMoney
		tax = rl.TaxCents - rl.PriorTax
	} else {
		// Partial return: PROPORTIONAL by base qty, mirroring the tax proration below.
		// qty_base is base-UOM while unit_price_cents is per PRICE-UOM, so qty × unit price
		// mixes units whenever they differ (e.g. 1000 SF entered / 4100 PC base / 300¢ per SF)
		// and over-refunds up to the cap. line_total / qty_sold is unit-consistent by
		// construction; round half-up like the tax line, capped at the remaining un-refunded
		// line money so cumulative partials can never exceed line_total.
		if rl.QtySold > 0 {
			money = int64(math.Round(float64(rl.LineTotalCents) * req.QtyBase / rl.QtySold))
		}
		if rem := rl.LineTotalCents - rl.PriorMoney; money > rem {
			money = rem
		}
		if rl.QtySold > 0 {
			tax = int64(math.Round(float64(rl.TaxCents) * req.QtyBase / rl.QtySold))
		}
		if rem := rl.TaxCents - rl.PriorTax; tax > rem {
			tax = rem
		}
	}
	if money < 0 || tax < 0 {
		return returnLineRow{}, clientErr(http.StatusUnprocessableEntity, "pos: line %s has nothing left to refund", req.OrderLineID)
	}
	// Money invariant (never weakened): Σ refunded tax per order line ≤ the original line tax.
	if rl.PriorTax+tax > rl.TaxCents {
		return returnLineRow{}, clientErr(http.StatusUnprocessableEntity, "pos: tax refund would exceed the original line tax on %s", req.OrderLineID)
	}
	return returnLineRow{
		OrderLineID: req.OrderLineID, ProductID: rl.ProductID, QtyBase: req.QtyBase,
		UnitPriceCents: rl.UnitPriceCents, LineTotalCents: money, TaxCents: tax, ReasonCode: req.ReasonCode,
	}, nil
}

// commitReturnTx is the write half of one return's transactional body: the shared steps 2 through
// 5 of the single return (returnInTx) and of every child of an across-orders note, the header and
// lines, the deposit release read, the refund rail, the restock and the GL events. noteID is empty
// on the single-order return; a note child carries its note's id. approvedHere rides in from
// whichever gate judged the return (the single path's per-sale cumulative, the note path's note
// total): the committed event names the decision it was given.
func (s *Service) commitReturnTx(ctx context.Context, tx pgx.Tx, plan *returnPlan, g guardedReturn, noteID string, approvedHere bool) (Return, error) {
	// The plan's facts the write half reads.
	in := plan.in
	oc := plan.oc
	actor := plan.actor
	pickupRequired, pickupStatus, pickupSite, pickupDate := plan.pickupRequired, plan.pickupStatus, plan.pickupSite, plan.pickupDate
	decision := plan.decision
	approverStaff := plan.approverStaff
	refusalRowID := plan.refusalRowID
	lines, stockLines, eventLines := g.lines, g.stockLines, g.eventLines
	subtotal, taxTotal := g.subtotal, g.taxTotal
	total := subtotal + taxTotal

	// FW1-01b: the return's own date, the instant the return was rung. It rides the three GL events
	// this return emits (the stock reversal, the damage write off and the money-side refund), so the
	// ledger books them in the day the return happened even when the consumer reads the outbox after
	// midnight.
	returnAt := s.nowOrClock()

	// A-06 restocking fee, derived from the ACTUAL committed subtotal (round half-up) so the fee is
	// exact under partial/capped refunds. fee ≤ subtotal ≤ total by construction (bps ≤ 10000), so the
	// credited amount (total − fee) can never go negative.
	var feeCents int64
	if in.FeeBps > 0 {
		feeCents = (subtotal*int64(in.FeeBps) + 5000) / 10000
	}

	// The ruling on an order that is not fully paid: cash or card go back only up to what was paid on
	// the invoice, the rest comes off the open invoice. A swap child's sources are paid in full, so it
	// has nothing to split. An order with nothing paid at all gets no cash or card, in words.
	if plan.applied == 0 && (in.RefundMethod == RefundCash || in.RefundMethod == RefundCard) {
		sp, serr := s.railSplitFor(ctx, tx, oc, in.RefundMethod, total-feeCents)
		if serr != nil {
			return Return{}, serr
		}
		if sp.rail == 0 && sp.offset > 0 {
			return Return{}, clientErr(http.StatusUnprocessableEntity, "%s", sp.note)
		}
		plan.offset, plan.paymentNote = sp.offset, sp.note
	}

	// 2. Header first, ux_pos_returns_idem makes a concurrent duplicate abort the whole tx before
	//    any rail write.
	returnID, returnNo, ierr := s.store.InsertReturn(ctx, tx, returnRow{
		OrderID: oc.OrderID, InvoiceID: oc.InvoiceID, CustomerID: oc.CustomerID, BranchID: oc.BranchID,
		RefundMethod: in.RefundMethod, SubtotalCents: subtotal, TaxCents: taxTotal, TotalCents: total,
		FeeCents: feeCents, Reason: in.Reason, Restock: anyRestock(lines), TillSessionID: in.TillSessionID,
		PickupRequired: pickupRequired, PickupSite: pickupSite, PickupDate: pickupDate, PickupStatus: pickupStatus,
		IdempotencyKey: in.IdempotencyKey, CreatedBy: actor, NoteID: noteID, AppliedCents: plan.applied, OffsetCents: plan.offset,
	})
	if ierr != nil {
		return Return{}, ierr
	}
	if err := s.store.InsertReturnLines(ctx, tx, returnID, lines); err != nil {
		return Return{}, err
	}
	// Q5: goods credited that never left the shelf are released from the order's open lines, through
	// the order module's seam, so the yard cannot pick them.
	if s.openQty != nil {
		for _, ln := range lines {
			if ln.CreditOnlyBase > qtyEpsilon {
				if err := s.openQty.ReleaseOpenQuantityTx(ctx, tx, ln.OrderLineID, ln.CreditOnlyBase); err != nil {
					return Return{}, err
				}
			}
		}
	}

	// 2b. A supplier deposit order releases its deposit on ANY return, partial or whole (the
	//     supervisor's default, pending the principal's ruling): the goods were dealt with, so the
	//     deposit is recognised once. The documents are read here, inside the transaction, and the
	//     release events are emitted below ahead of the revenue reversal (step 5). The documents
	//     are billing's: they are read through its seam (DepositDocumentLister), never from the
	//     invoices table here.
	var depositDocs []string
	if s.deposits != nil {
		var derr error
		if depositDocs, derr = s.deposits.DepositDocumentIDsTx(ctx, tx, oc.OrderID); derr != nil {
			return Return{}, derr
		}
	}

	// 3. The refund rail (all writes behind the till/payment seams; nothing here touches their tables).
	var refundPaymentID string
	// The rail moves the total less what a swap spent on its new order; with nothing left over no rail
	// runs at all (no drawer payout, no processor call, no credit row).
	railTotal := total - plan.applied - plan.offset
	switch {
	case railTotal <= 0:
	case in.RefundMethod == RefundCash:
		res, err := s.tender.RefundTender(ctx, tx, till.RefundInput{
			TillSessionID: in.TillSessionID, Method: till.MethodCash, AmountCents: railTotal,
			BranchID: oc.BranchID, CustomerID: oc.CustomerID, Reference: returnNo,
			IdempotencyKey: "refund:" + in.IdempotencyKey,
		})
		if err != nil {
			return Return{}, clientErr(http.StatusConflict, "%s", err.Error())
		}
		refundPaymentID = res.PaymentID
	case in.RefundMethod == RefundCard:
		// The staged path (HH-690): the processor already refunded OUTSIDE any DB transaction
		// (dispatchStagedCardRefunds), so tx 2 only re-checks the ceiling and books against the
		// staged reversal ref. The sale's returnable balance moved under the in-flight refund:
		// refuse in words and leave the attempt UNBOOKED for reconciliation, never book a return
		// whose money no longer matches what the processor holds.
		if plan.stagedRefundRef != "" {
			if (plan.stagedRailTotal > 0 && railTotal != plan.stagedRailTotal) ||
				(plan.stagedRefundCents > 0 && railTotal != plan.stagedRefundCents) {
				return Return{}, clientErr(http.StatusConflict,
					"pos: the sale's returnable balance changed while the card refund was in flight; review the sale")
			}
			if err := s.checkCardCapTx(ctx, tx, oc, returnID, "refund:"+in.IdempotencyKey, railTotal); err != nil {
				return Return{}, err
			}
		} else {
			// The reversal rides THIS transaction (the CardRefunder seam's
			// discipline): a pool read while the Return transaction holds a
			// connection has the per-sale lock's holder wait for a second
			// connection every other connection is a Return waiter that needs
			// the holder's lock, deadlocking a full pool.
			if err := s.checkCardCapTx(ctx, tx, oc, returnID, "refund:"+in.IdempotencyKey, railTotal); err != nil {
				return Return{}, err
			}
			var err error
			plan.stagedRefundRef, err = s.cardRefund.RefundCard(ctx, tx, oc.GatewayRef, railTotal, returnNo, "refund:"+in.IdempotencyKey)
			if err != nil {
				return Return{}, clientErr(http.StatusBadGateway, "pos: card refund failed, %s", err.Error())
			}
			// The processor now holds this refund whatever becomes of the transaction: keep the fact so a
			// rollback leaves a record of it (D4).
			plan.cardRefunds = append(plan.cardRefunds, cardRefundFact{
				RefundKey: "refund:" + in.IdempotencyKey, OrderID: oc.OrderID, BranchID: oc.BranchID, CustomerID: oc.CustomerID,
				GatewayRef: oc.GatewayRef, RefundRef: plan.stagedRefundRef, AmountCents: railTotal,
				InitiatedBy: plan.actor,
			})
		}
		refundRef := plan.stagedRefundRef
		res, err := s.tender.RefundTender(ctx, tx, till.RefundInput{
			Method: till.MethodCard, AmountCents: railTotal, BranchID: oc.BranchID, CustomerID: oc.CustomerID,
			GatewayRef: refundRef, Reference: returnNo, IdempotencyKey: "refund:" + in.IdempotencyKey,
		})
		if err != nil {
			return Return{}, clientErr(http.StatusConflict, "%s", err.Error())
		}
		refundPaymentID = res.PaymentID
	case in.RefundMethod == RefundAccountCredit:
		// The restocking fee reduces what the customer's account is credited (total − fee); the fee
		// itself books through the GL event's fee_cents, never a subledger row.
		offsetMade, err := s.applyAccountCredit(ctx, tx, oc, railTotal-feeCents, depositDocs)
		if err != nil {
			return Return{}, err
		}
		if offsetMade > 0 {
			if err := s.store.SetReturnOffset(ctx, tx, returnID, offsetMade); err != nil {
				return Return{}, err
			}
		}
	}
	if refundPaymentID != "" {
		if err := s.store.SetReturnRefundPayment(ctx, tx, returnID, refundPaymentID); err != nil {
			return Return{}, err
		}
	}
	// The split's account part comes off the order's open documents: the sale's invoice first, then the
	// supplier deposit documents (as the account rail does), each a plain offset of the document's
	// balance with its own CREDIT_MEMO row on the customer's account, never a credit memo document.
	if plan.offset > 0 {
		remaining := plan.offset
		targets := append([]string{oc.InvoiceID}, depositDocs...)
		for _, docID := range targets {
			if remaining <= 0 {
				break
			}
			done, oerr := s.store.ApplyCreditToInvoice(ctx, tx, docID, remaining)
			if oerr != nil {
				return Return{}, oerr
			}
			if done == 0 {
				continue
			}
			cust, cerr := s.store.ReadCustomerTx(ctx, tx, oc.CustomerID)
			if cerr != nil {
				return Return{}, cerr
			}
			if terr := s.store.InsertCustomerTxn(ctx, tx, customerTxnRow{
				CustomerID: oc.CustomerID, TxnType: "CREDIT_MEMO", AmountCents: -done,
				BalanceAfter: cust.OpenBalanceCents - done, SourceType: "invoice", SourceRefID: docID, BranchID: oc.BranchID,
			}); terr != nil {
				return Return{}, terr
			}
			remaining -= done
		}
		if remaining > 0 {
			return Return{}, clientErr(http.StatusConflict, "pos: the order's invoice changed while the return was being rung; review the return and ring it again")
		}
	}

	// 4. Restock, RETURN moves at the current weighted average through the seam, then the stock-side
	//    GL event (mirrors the Sale's decrement + order.fulfilled pairing; skips quietly when no cost).
	//    Only the lines whose goods physically went back on the shelf are in stockLines (the D1 fix:
	//    goods that never left the shelf move no stock and have no COGS to reverse).
	if len(stockLines) > 0 {
		if err := s.restocker().RestockForReturnTx(ctx, tx, RestockInput{
			ReturnID: returnID, OrderID: oc.OrderID, BranchID: oc.BranchID, ReturnNo: returnNo, Lines: stockLines,
		}); err != nil {
			return Return{}, err
		}
		if err := s.store.EmitEvent(ctx, tx, "order.returned", map[string]any{
			"order_id": oc.OrderID, "return_id": returnID, "branch_id": oc.BranchID, "lines": eventLines,
			"occurred_at": returnAt,
		}); err != nil {
			return Return{}, err
		}
	}
	//    Damaged goods that came back are written off (Q4): the cost leaves COGS for 5200 at the
	//    average at posting time.
	if len(g.writeOffLines) > 0 {
		if err := s.store.EmitEvent(ctx, tx, "pos.return.written_off", map[string]any{
			"order_id": oc.OrderID, "return_id": returnID, "branch_id": oc.BranchID, "lines": g.writeOffLines,
			"occurred_at": returnAt,
		}); err != nil {
			return Return{}, err
		}
	}

	// 5. The money-side GL event (DR revenue + DR HST / CR the refund rail). A committed
	//    manager decision names the decision in the record: the approver who decided and the
	//    clerk who rang it in one identifier space (the linked staff users ids, the lead's
	//    P3-3 ruling), the platform subject beside them as the directory reference, and the
	//    refusal the decision answers, cited by its own audit row id where the trail can
	//    name one (row 482, D49).
	// The deposit release rides ahead of the reversal: same topic and journal key the fulfilment
	// road uses (deposit.released:<invoice id>), so whichever comes second dedupes in the ledger.
	for _, depID := range depositDocs {
		if err := s.store.EmitEvent(ctx, tx, "deposit.released", map[string]any{
			"invoice_id": depID, "order_id": oc.OrderID,
		}); err != nil {
			return Return{}, err
		}
	}
	committed := map[string]any{
		"return_id": returnID, "order_id": oc.OrderID, "invoice_id": oc.InvoiceID,
		"refund_method": in.RefundMethod, "subtotal_cents": subtotal, "tax_cents": taxTotal,
		"total_cents": total, "fee_cents": feeCents, "branch_id": oc.BranchID,
		"applied_cents": plan.applied, "invoice_offset_cents": plan.offset,
		"occurred_at": returnAt,
	}
	if approvedHere {
		committed["decided_by"] = approverStaff
		committed["decided_by_subject"] = decision.approver.PlatformUserID
		committed["decided_by_role"] = decision.approver.RoleID
		committed["rang_by"] = actor
		committed["approval"] = map[string]any{
			"refusal":      refundApprovalRequiredError,
			"refund_cents": total,
		}
		if refusalRowID != "" {
			committed["approval"].(map[string]any)["refusal_id"] = refusalRowID
		}
	}
	if err := s.store.EmitEvent(ctx, tx, "pos.return.committed", committed); err != nil {
		return Return{}, err
	}

	out := Return{
		ReturnID: returnID, ReturnNo: returnNo, OrderID: oc.OrderID, InvoiceID: oc.InvoiceID,
		RefundMethod: in.RefundMethod, SubtotalCents: subtotal, TaxCents: taxTotal, TotalCents: total,
		FeeCents: feeCents, AppliedCents: plan.applied, OffsetCents: plan.offset, PaymentNote: plan.paymentNote, RefundPaymentID: refundPaymentID, PickupStatus: pickupStatus, Status: "COMMITTED",
	}
	return out, nil
}

// errWalkInNoAccountCredit refuses an account credit on the house WALK-IN row (D2): the one shared
// row's credit is nobody's to spend and any walk-in could in principle draw it.
var errWalkInNoAccountCredit = clientErr(http.StatusUnprocessableEntity,
	"pos: the WALK-IN house account holds no credit; refund this walk-in sale to the card it was paid on or in cash")

// errAccountCannotSpendCredit is the Q6 ruling in words: there is no store credit wallet, so a
// credit on an account that cannot spend it (D3) would be a dead end.
var errAccountCannotSpendCredit = clientErr(http.StatusUnprocessableEntity,
	"pos: this account cannot spend a credit (it has no credit terms or no credit line); refund to the card it was paid on or in cash, or set the account up with terms and a credit line first")

// accountCreditAllowed judges the ACCOUNT_CREDIT rail for a customer before any transaction opens:
// never the house WALK-IN row (D2, every caller), and, outside the portal request rail whose
// customers the queue's own decision already vouched for, only an account that can spend a credit,
// the same shape the ring offers on account (terms above zero and a credit line, D3 by Q6).
func (s *Service) accountCreditAllowed(ctx context.Context, customerID string, internalRail bool) error {
	if walkIn, werr := s.store.ResolveWalkIn(ctx); werr == nil && walkIn != "" && walkIn == customerID {
		return errWalkInNoAccountCredit
	}
	if internalRail {
		return nil
	}
	// Read directly, on purpose (customerFor's ruling): a return of the branch's own sale is held by the sale's
	// branch, not by the account's home.
	cust, err := s.store.ReadCustomer(ctx, customerID)
	if err != nil {
		return err
	}
	if cust.PaymentTermsDays <= 0 || cust.CreditLimitCents <= 0 {
		return errAccountCannotSpendCredit
	}
	return nil
}

// checkCardCapTx is D5: a card refund never takes the card past what the original capture holds
// less what earlier card refunds of the order already took. Read on the open transaction after the
// per-sale lock, so the earlier refunds are exact. A capture of unknown size (0) sets no ceiling.
func (s *Service) checkCardCapTx(ctx context.Context, tx pgx.Tx, oc returnOrderCtx, exceptReturnID, refundKey string, refundCents int64) error {
	if oc.CapturedCents <= 0 {
		return nil
	}
	refunded, err := s.store.SumCardRefundedTx(ctx, tx, oc.OrderID, exceptReturnID)
	if err != nil {
		return err
	}
	unbooked, err := s.store.SumUnbookedCardRefundsTx(ctx, tx, oc.OrderID, refundKey)
	if err != nil {
		return err
	}
	if unbooked > 0 && refunded+unbooked+refundCents > oc.CapturedCents {
		return clientErr(http.StatusUnprocessableEntity,
			"pos: the processor already refunded %s of this card sale that is not booked yet, so another card refund of %s would pass what the capture holds; ring the return that made it again, with its own number, to book it",
			centsWords(unbooked), centsWords(refundCents))
	}
	if refunded+refundCents > oc.CapturedCents {
		return clientErr(http.StatusUnprocessableEntity,
			"pos: a card refund of %s would take the card past what the original capture holds (%s captured, %s already refunded); refund the rest in cash or to the account",
			centsWords(refundCents), centsWords(oc.CapturedCents), centsWords(refunded))
	}
	return nil
}

// centsWords writes integer cents as dollars and cents for a refusal's words.
func centsWords(cents int64) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	return fmt.Sprintf("%s$%d.%02d", sign, cents/100, cents%100)
}

// trimReason bounds a processor or transaction refusal kept on an attempt row: the record a manager
// reads, never a log line, so it stops at 300 runes.
func trimReason(reason string) string {
	if r := []rune(reason); len(r) > 300 {
		return string(r[:300])
	}
	return reason
}

// settleCardRefunds records, on the pool after the transaction ended, what the processor refunded
// inside it: a rolled back transaction leaves each refund on record as UNBOOKED for a manager to
// see, a committed one closes any earlier such record as BOOKED (D4). Best effort by design: the
// return's own answer must not change because the record could not be written, so a failure is
// logged, never returned.
func (s *Service) settleCardRefunds(ctx context.Context, plans []*returnPlan, txErr error) {
	reason := ""
	if txErr != nil {
		reason = trimReason(txErr.Error())
	}
	s.settleCardRefundsOutcome(ctx, plans, txErr != nil, reason)
}

// settleCardRefundsWithReason files the plans' dispatched refunds UNBOOKED with a reason of the
// caller's choosing (the dispatch's failure path, where the batch error describes one child only).
func (s *Service) settleCardRefundsWithReason(ctx context.Context, plans []*returnPlan, reason string) {
	s.settleCardRefundsOutcome(ctx, plans, true, reason)
}

func (s *Service) settleCardRefundsOutcome(ctx context.Context, plans []*returnPlan, failed bool, reason string) {
	var facts []cardRefundFact
	for _, p := range plans {
		facts = append(facts, p.cardRefunds...)
		p.cardRefunds = nil
	}
	if len(facts) == 0 {
		return
	}
	var err error
	if failed {
		err = s.store.RecordUnbookedCardRefunds(context.WithoutCancel(ctx), facts, reason)
	} else {
		keys := make([]string, len(facts))
		for i, f := range facts {
			keys[i] = f.RefundKey
		}
		err = s.store.MarkCardRefundsBooked(context.WithoutCancel(ctx), keys)
	}
	if err != nil {
		slog.Default().Warn("pos: the card refund record could not be written", "err", err, "refunds", len(facts))
	}
}

// railSplit is the ruling's split of one return of an order that is not fully paid.
type railSplit struct {
	rail   int64  // what goes back in cash or card
	offset int64  // what comes off the order's open invoice
	note   string // the split in words, empty when nothing is split
}

// railSplitFor decides the split for a CASH or CARD return of total cents (the principal's ruling,
// FROM-PRINCIPAL 47): cash or card only up to what was actually paid on the order's invoice, less what
// earlier returns already paid out that way; the rest comes off the open invoice. A fully paid invoice
// splits nothing. tx nil reads the pool (the preview).
func (s *Service) railSplitFor(ctx context.Context, tx pgx.Tx, oc returnOrderCtx, method string, total int64) (railSplit, error) {
	if (method != RefundCash && method != RefundCard) || oc.InvoiceID == "" {
		return railSplit{rail: total}, nil
	}
	// The order's money sits on the sale's invoice and on any live supplier deposit documents: what
	// was paid and what is still owed are counted across all of them (rulings 16 and 17: the deposit
	// is released once, the customer gets back the returned goods' value, never a share of the deposit).
	docs := []string{oc.InvoiceID}
	if s.deposits != nil {
		deps, err := s.deposits.DepositDocumentIDsTx(ctx, tx, oc.OrderID)
		if err != nil {
			return railSplit{}, err
		}
		docs = append(docs, deps...)
	}
	var invTotal, balance int64
	for _, id := range docs {
		t, b, err := s.store.InvoiceMoney(ctx, tx, id)
		if err != nil {
			return railSplit{}, err
		}
		invTotal += t
		balance += b
	}
	if balance == 0 { // every document is paid: unchanged
		return railSplit{rail: total}, nil
	}
	offsets, payouts, err := s.store.SumReturnRails(ctx, tx, oc.OrderID)
	if err != nil {
		return railSplit{}, err
	}
	avail := invTotal - balance - offsets - payouts
	if avail < 0 {
		avail = 0
	}
	sp := railSplit{rail: total}
	if avail < total {
		sp.rail = avail
		sp.offset = total - avail
		if sp.offset > balance { // never more than the documents still ask for; the rest is paid out
			sp.rail += sp.offset - balance
			sp.offset = balance
		}
	}
	switch {
	case sp.offset == 0:
	case sp.rail == 0:
		sp.note = fmt.Sprintf("This order is not paid yet: nothing can go back in cash or card; the whole %s comes off the open invoice", centsWords(sp.offset))
	default:
		sp.note = fmt.Sprintf("This order is not fully paid: up to %s can go back in cash or card; the rest, %s, comes off the open invoice", centsWords(sp.rail), centsWords(sp.offset))
	}
	return sp, nil
}

// convertedNotReturnable words why a converted order cannot be returned yet.
func convertedNotReturnable(found bool, status string) string {
	switch {
	case !found:
		return "no invoice has been raised for this order yet, so there is nothing to refund against"
	case status == "DRAFT":
		return "this order's invoice is still a draft, so there is nothing to refund against"
	}
	return ""
}

// resolveConverted fills a converted order's invoice from the billing seam, or refuses in words when
// the order has no live finalised invoice. Orders rung at the till pass through untouched.
func (s *Service) resolveConverted(ctx context.Context, oc returnOrderCtx) (returnOrderCtx, error) {
	if !oc.Converted {
		return oc, nil
	}
	if s.orderInvoices == nil {
		return oc, clientErr(http.StatusUnprocessableEntity, "pos: orders a quote became cannot be returned here yet")
	}
	id, status, found, err := s.orderInvoices.LiveOrderInvoice(ctx, oc.OrderID)
	if err != nil {
		return oc, err
	}
	if why := convertedNotReturnable(found, status); why != "" {
		return oc, clientErr(http.StatusUnprocessableEntity, "pos: order %s cannot be returned: %s", oc.OrderNo, why)
	}
	oc.InvoiceID, oc.InvoiceStatus = id, status
	return oc, nil
}

// lockReturnInvoicesTx locks, sorted and before any credit offset, every invoice the returns of these
// orders may touch: each order's own invoice and its live deposit documents (P2-1). Only the account
// rail offsets invoices, so only it takes these locks: a cash or card return touches an invoice by its
// foreign key share alone, and a stronger lock there would queue it against a multi invoice reversal
// that draws the payments counter first (lockorder_pos_return_test.go).
//
// A cash or card return (owedOnly) that will take ANY of these rows draws the payments counter first,
// inside this helper and before the locks: its rail writes a refund payment, which draws the counter,
// and the payments module's one money lock order is counter first, invoices second, so holding an
// invoice row while waiting on the counter closed the cycle the Opus review's P2-4 and the delta
// reviews' P1 found on the two return paths. The draw is gated on the rows existing because the one
// counter row serialises every payments writer in the company: a return on a fully paid sale with no
// deposit document locks no invoice and never needs the counter, so it must not hold one for its
// transaction's length (the delta reviews' P2), and the account credit rail (not owedOnly) writes no
// payment row and draws no counter at all.
func (s *Service) lockReturnInvoicesTx(ctx context.Context, tx pgx.Tx, ocs []returnOrderCtx, owedOnly bool) error {
	seen := map[string]bool{}
	var ids []string
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, oc := range ocs {
		var docs []string
		if s.deposits != nil {
			var err error
			if docs, err = s.deposits.DepositDocumentIDsTx(ctx, tx, oc.OrderID); err != nil {
				return err
			}
		}
		// A cash or card return offsets documents only on an order that is not fully paid: a paid
		// order with no deposit document keeps the plain path (and its lock window).
		if owedOnly && oc.InvoiceStatus == "PAID" && len(docs) == 0 {
			continue
		}
		add(oc.InvoiceID)
		for _, d := range docs {
			add(d)
		}
	}
	if owedOnly && len(ids) > 0 {
		if err := s.store.LockPaymentsSequenceTx(ctx, tx); err != nil {
			return err
		}
	}
	sort.Strings(ids)
	return s.store.LockInvoicesTx(ctx, tx, ids)
}

// anyRestock answers whether any line of the return asked for its goods to go back on the shelf:
// the pos_returns.restock header column, kept for the readers that predate dispositions.
func anyRestock(lines []returnLineRow) bool {
	for _, l := range lines {
		if l.Disposition == DispositionRestock {
			return true
		}
	}
	return false
}

// replayIfAllowed answers an idempotent replay only to a caller the wall
// admits to the REPLAYED return's own sale (the lead's P2-2R ruling): the
// key is not a bearer of the answer, so the branch reach is judged against
// the cached return's sale, and a foreign clerk refuses identically to a
// first attempt whatever sale their own request body names.
func (s *Service) replayIfAllowed(ctx context.Context, key string, caller identity.StaffContext) (Return, bool, error) {
	cached, found, err := s.store.FindReturnByKey(ctx, key)
	if err != nil || !found {
		return Return{}, false, err
	}
	oc, found, err := s.store.ReturnOrderContext(ctx, cached.OrderID, "")
	if err != nil {
		return Return{}, false, err
	}
	if !found {
		return Return{}, false, errReturnNotFound
	}
	if err := requireBranchReach(caller, oc.BranchID); err != nil {
		return Return{}, false, err
	}
	cached.Replayed = true
	return cached, true, nil
}

// replayIfAllowedTx is replayIfAllowed on the open Return transaction: the
// in-transaction same-key re-check reads through the transaction's own
// connection, never through the pool. A Return transaction holds one
// connection while it waits on the per-sale advisory lock; a pool read
// there would have the lock holder wait for a second connection every
// other connection is a waiter that needs the holder's lock, and a full
// pool deadlocks until the context ends.
func (s *Service) replayIfAllowedTx(ctx context.Context, tx pgx.Tx, key string, caller identity.StaffContext) (Return, bool, error) {
	cached, found, err := s.store.FindReturnByKeyTx(ctx, tx, key)
	if err != nil || !found {
		return Return{}, false, err
	}
	oc, found, err := s.store.ReturnOrderContextTx(ctx, tx, cached.OrderID, "")
	if err != nil {
		return Return{}, false, err
	}
	if !found {
		return Return{}, false, errReturnNotFound
	}
	if err := requireBranchReach(caller, oc.BranchID); err != nil {
		return Return{}, false, err
	}
	cached.Replayed = true
	return cached, true, nil
}

// standingRefusalFor builds the contract's refusal for one computed
// return: the configured threshold and the configured approver roles,
// never null and never zero, whatever the refund's own total (the lead's
// P2-B ruling).
func (s *Service) standingRefusalFor(totalCents int64) ErrRefundApprovalRequired {
	return ErrRefundApprovalRequired{
		AmountCents: s.roles.RefundAmountWithoutApprovalCents(),
		Approvers:   s.roles.RefundApprovers(),
		RefundCents: totalCents,
	}
}

// refundMayNeedApproval answers, from the sale's lines without locks,
// whether the requested return COULD land the sale's CUMULATIVE refunds
// where the dealer's configuration asks for an approver: the sale's earlier
// committed returns (the pool read) plus the sum over the requested lines
// of their original totals and tax, which over-approximates every remaining
// refund, against RefundRequiresApproval, the one rule that folds the limit
// switch in. A false answer makes the credential header inert (nothing
// needs authorization, nothing is consulted) and stays trustworthy: if the
// prior committed total plus the requested lines' original totals sits at
// or under the amount, the cumulative cannot cross it, and with no limit
// configured nothing ever crosses it. A true one is only a prospect, the
// transaction's own cumulative decides. An unreadable read judges the
// credential: fail closed.
func (s *Service) refundMayNeedApproval(ctx context.Context, oc returnOrderCtx, in ReturnInput) bool {
	lines, err := s.store.ListReturnableLines(ctx, oc.OrderID)
	if err != nil {
		return true
	}
	prior, err := s.store.SumCommittedReturnTotals(ctx, oc.OrderID)
	if err != nil {
		return true
	}
	requested := make(map[string]bool, len(in.Lines))
	for _, ln := range in.Lines {
		requested[ln.OrderLineID] = true
	}
	var prospective int64
	for _, v := range lines {
		if requested[v.OrderLineID] {
			prospective += v.LineTotalCents + v.TaxCents
		}
	}
	return s.roles.RefundRequiresApproval(prior + prospective)
}

// refundApprovalRefusal answers the configured approval rule (X7) for one
// return against its sale's running total: ok false when the CUMULATIVE of
// the sale's earlier committed returns and this return sits at or under the
// amount, when the caller's role holds the approval, or when the dealer
// configured no limit at all (the Dibbits ruling: RefundRequiresApproval
// folds the switch in, so no refund ever asks). The refusal names the
// configured amount and the configured approver roles, and RefundCents
// is the NEW return's own total, the money asked for at the counter, never
// the cumulative: the audit row's amount_cents keeps meaning the money
// asked for, refused at the counter. The caller has already refused a
// service built with no configuration (the nil gate fails closed); the
// configured amount is integer cents and the comparison is integer against
// integer; a role outside the configuration's vocabulary is on no approvers
// list, fail closed.
func (s *Service) refundApprovalRefusal(cumulativeCents, newTotalCents int64, caller identity.StaffContext) (ErrRefundApprovalRequired, bool) {
	amount := s.roles.RefundAmountWithoutApprovalCents()
	if !s.roles.RefundRequiresApproval(cumulativeCents) {
		return ErrRefundApprovalRequired{}, false
	}
	approved := false
	if role, ok := canonicalRole(caller); ok {
		if holds, err := s.roles.ApprovesRefundAboveAmount(role); err == nil {
			approved = holds
		}
	}
	if approved {
		return ErrRefundApprovalRequired{}, false
	}
	return ErrRefundApprovalRequired{
		AmountCents: amount,
		Approvers:   s.roles.RefundApprovers(),
		RefundCents: newTotalCents,
	}, true
}

// applyAccountCredit books the ACCOUNT_CREDIT rail: offset the original invoice first (floor 0; PAID
// only when fully offset), then carry any remainder as a store credit. The customer subledger drops by
// the FULL total either way (balance_after = open − total) and the AR view stays exact: the offset part
// rides the invoice balance (its memo row is source_type 'invoice', excluded from the non-invoice
// union), the remainder is a counted non-invoice CREDIT_MEMO. txn_type CREDIT_MEMO is the 0009 CHECK's
// vocabulary for the design's "CREDIT" row.
//
// A supplier deposit order carries a third place for the remainder: the live deposit documents
// (depositDocs). What the sale's own invoice cannot absorb is applied to them in turn, so the
// deposit invoice does not stay open and owed beside a store credit for the same money.
func (s *Service) applyAccountCredit(ctx context.Context, tx pgx.Tx, oc returnOrderCtx, total int64, depositDocs []string) (int64, error) {
	// The customer read rides THIS transaction (ReadCustomerTx, the
	// FindReturnByKeyTx discipline): a pool read while the Return
	// transaction holds its connection has the per-sale lock's holder wait
	// on a second connection every other connection is a Return waiter
	// that needs the holder's lock, deadlocking a full pool.
	cust, err := s.store.ReadCustomerTx(ctx, tx, oc.CustomerID)
	if err != nil {
		return 0, err
	}
	var applied, invoiceOffset int64
	if oc.InvoiceStatus == "FINALIZED" || oc.InvoiceStatus == "PARTIALLY_PAID" {
		applied, err = s.store.ApplyCreditToInvoice(ctx, tx, oc.InvoiceID, total)
		if err != nil {
			return 0, err
		}
		invoiceOffset = applied
	}
	open := cust.OpenBalanceCents
	if applied > 0 {
		if err := s.store.InsertCustomerTxn(ctx, tx, customerTxnRow{
			CustomerID: oc.CustomerID, TxnType: "CREDIT_MEMO", AmountCents: -applied,
			BalanceAfter: open - applied, SourceType: "invoice", SourceRefID: oc.InvoiceID, BranchID: oc.BranchID,
		}); err != nil {
			return 0, err
		}
	}
	remainder := total - applied
	for _, depID := range depositDocs {
		if remainder <= 0 {
			break
		}
		depApplied, err := s.store.ApplyCreditToInvoice(ctx, tx, depID, remainder)
		if err != nil {
			return 0, err
		}
		if depApplied > 0 {
			if err := s.store.InsertCustomerTxn(ctx, tx, customerTxnRow{
				CustomerID: oc.CustomerID, TxnType: "CREDIT_MEMO", AmountCents: -depApplied,
				BalanceAfter: open - applied - depApplied, SourceType: "invoice", SourceRefID: depID, BranchID: oc.BranchID,
			}); err != nil {
				return 0, err
			}
			applied += depApplied
			invoiceOffset += depApplied // the documents' offsets all count toward what the invoices took
			remainder -= depApplied
		}
	}
	if remainder > 0 {
		if err := s.store.InsertCustomerTxn(ctx, tx, customerTxnRow{
			CustomerID: oc.CustomerID, TxnType: "CREDIT_MEMO", AmountCents: -remainder,
			BalanceAfter: open - total, SourceType: "pos_return", SourceRefID: oc.OrderID, BranchID: oc.BranchID,
		}); err != nil {
			return 0, err
		}
	}
	return invoiceOffset, nil
}

// DepositDocumentLister is the seam the return reads the order's live supplier deposit documents
// through, on the return's own transaction. Billing owns those documents and satisfies it at the
// composition root; unwired, a return releases nothing.
type DepositDocumentLister interface {
	DepositDocumentIDsTx(ctx context.Context, tx pgx.Tx, orderID string) ([]string, error)
}

// WithDepositDocuments binds the deposit document read the return releases deposits from.
func (s *Service) WithDepositDocuments(l DepositDocumentLister) *Service { s.deposits = l; return s }

// reachableBranches is the branch ids a bare number may be searched in: nil for a caller who reaches
// every branch (global scope), otherwise the granted ids, a non-nil slice even when there are none.
func reachableBranches(caller identity.StaffContext) []string {
	if caller.Scope == identity.ScopeGlobal {
		return nil
	}
	ids := make([]string, 0, len(caller.Branches))
	for id, ok := range caller.Branches {
		if ok {
			ids = append(ids, id)
		}
	}
	return ids
}

// normalizeSaleRef is the typed sale reference as the lookup compares it: trimmed, upper cased, and
// without a leading hash, so "#SO-TRT-21", "so-trt-21", " SO-TRT-21 " and "INV-17" all reach the
// same comparison (the stored numbers carry the hash; the query adds it back).
func normalizeSaleRef(raw string) string {
	return strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(raw)), "#")
}

// LookupReturn is the pre-return read: the original committed sale + its lines with prior-return
// sums netted, so the counter can build the per-line qty entry. Read-only, the commit re-guards.
// The branch wall answers before the lines are read (X2): another branch's sale is refused, its
// returnable quantities are not a thing this counter gets to browse.
func (s *Service) LookupReturn(ctx context.Context, orderID, orderNo string) (ReturnLookup, error) {
	if orderID == "" && orderNo == "" {
		return ReturnLookup{}, clientErr(http.StatusBadRequest, "pos: order_id or order_no is required")
	}
	caller, err := s.caller(ctx)
	if err != nil {
		return ReturnLookup{}, err
	}
	oc, found, err := s.store.ReturnOrderContextFor(ctx, orderID, normalizeSaleRef(orderNo), reachableBranches(caller))
	if err != nil {
		return ReturnLookup{}, err
	}
	if !found {
		return ReturnLookup{}, errReturnNotFound
	}
	if err := requireBranchReach(caller, oc.BranchID); err != nil {
		return ReturnLookup{}, err
	}
	lines, err := s.store.ListReturnableLines(ctx, oc.OrderID)
	if err != nil {
		return ReturnLookup{}, err
	}
	return ReturnLookup{
		OrderID: oc.OrderID, OrderNo: oc.OrderNo, BranchID: oc.BranchID, CustomerID: oc.CustomerID,
		InvoiceID: oc.InvoiceID, SaleTender: oc.SaleTender,
		CardRefundable: oc.SaleTender == TenderCard && oc.GatewayRef != "",
		Lines:          lines,
	}, nil
}

// ListReturnPickups is the logistics hand-off surface: REQUESTED/SCHEDULED pickups for a branch.
func (s *Service) ListReturnPickups(ctx context.Context, branchID string) ([]ReturnPickup, error) {
	if branchID == "" {
		return nil, clientErr(http.StatusBadRequest, "pos: branch_id is required")
	}
	caller, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	if !caller.CanAccessBranch(branchID) {
		return nil, clientErr(http.StatusForbidden, "pos: caller cannot access branch %s", branchID)
	}
	return s.store.ListReturnPickups(ctx, branchID)
}

// AdvancePickup moves a return's pickup along REQUESTED → SCHEDULED → DONE (manager route). Any other
// transition is a 409, the queue only ever walks forward.
func (s *Service) AdvancePickup(ctx context.Context, returnID, to string) (ReturnPickup, error) {
	if returnID == "" {
		return ReturnPickup{}, errBadRequest
	}
	cur, found, err := s.store.GetReturnPickup(ctx, returnID)
	if err != nil {
		return ReturnPickup{}, err
	}
	if !found {
		return ReturnPickup{}, clientErr(http.StatusNotFound, "pos: return not found")
	}
	// Behind the wall answers what a missing return answers.
	caller, err := s.caller(ctx)
	if err != nil {
		return ReturnPickup{}, err
	}
	if !caller.CanAccessBranch(cur.BranchID) {
		return ReturnPickup{}, clientErr(http.StatusNotFound, "pos: return not found")
	}
	valid := (cur.PickupStatus == PickupRequested && to == PickupScheduled) ||
		(cur.PickupStatus == PickupScheduled && to == PickupDone)
	if !valid {
		return ReturnPickup{}, clientErr(http.StatusConflict, "pos: pickup cannot move %s → %s", cur.PickupStatus, to)
	}
	if err := s.store.SetPickupStatus(ctx, returnID, cur.PickupStatus, to); err != nil {
		return ReturnPickup{}, err
	}
	cur.PickupStatus = to
	return cur, nil
}
