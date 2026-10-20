package pos

// card_refund_attempts.go, HH-767 (HH-690b) Leg A: the manager surface over
// the two-transaction card refund's attempt ledger. A crash between the two
// transactions, or an unknown processor outcome, leaves a
// pos_card_refund_attempts row open (IN_FLIGHT or UNBOOKED) with its amount
// reserved against the sale's card capture ceiling and same-key retries
// refusing 409. A manager at the sale's branch, never the cashier who
// started the refund (separation of duties, held by initiated_by), lists the
// open rows and resolves one as REFUNDED (the row stands UNBOOKED with the
// processor's refund_ref, so the same-key retry books without a second
// processor call) or NOT_REFUNDED (the row closes FAILED, releasing the
// ceiling so a retry or a fresh return can proceed).

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The two resolutions a manager can record after checking the processor.
const (
	CardRefundOutcomeRefunded    = "REFUNDED"
	CardRefundOutcomeNotRefunded = "NOT_REFUNDED"
)

// cardRefundLiveRequestMargin is the crash margin added to the payment
// module's own processor client timeout (payment.ProcessorClientTimeout, the
// one place that bound lives): past attempted_at plus the two, nothing the
// client started can still be running, so an IN_FLIGHT row with no recorded
// outcome is the crash case and settles.
const cardRefundLiveRequestMargin = 30 * time.Second

// CardRefundAttempt is one row of the attempt ledger as the manager's list
// and resolve answer it: the ledger's own facts joined with the order's
// number and the customer's name, plus who started the refund and who (if
// anybody yet) resolved its processor outcome.
type CardRefundAttempt struct {
	ID             string  `json:"id"`
	RefundKey      string  `json:"refund_key"`
	OrderID        string  `json:"order_id"`
	OrderNo        string  `json:"order_no"`
	BranchID       string  `json:"branch_id"`
	CustomerID     string  `json:"customer_id"`
	CustomerName   string  `json:"customer_name"`
	GatewayRef     string  `json:"gateway_ref"`
	RefundRef      string  `json:"refund_ref"`
	AmountCents    int64   `json:"amount_cents"`
	Status         string  `json:"status"`
	LastError      string  `json:"last_error"`
	InitiatedBy    string  `json:"initiated_by"`
	ResolvedBy     string  `json:"resolved_by,omitempty"`
	ResolvedAt     *string `json:"resolved_at,omitempty"`
	ResolutionNote string  `json:"resolution_note,omitempty"`
	AttemptedAt    string  `json:"attempted_at"`
	ProcessorAt    *string `json:"processor_at,omitempty"`
	BookedAt       *string `json:"booked_at,omitempty"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
	// CallNotSent is internal: the dispatch never sent this row's processor call because a manager
	// settled it first, so a typed ref on it is not a processor ref (PR #501 round 3).
	CallNotSent bool `json:"-"`
}

// ResolveCardRefundAttemptInput is the manager's judgment of one open
// attempt: the outcome, the processor's refund_ref when the money moved, and
// the note that lands on the sensitive-access audit row.
type ResolveCardRefundAttemptInput struct {
	Outcome   string `json:"outcome"`
	RefundRef string `json:"refund_ref"`
	Note      string `json:"note"`
}

// ListOpenCardRefundAttempts answers the open card refund attempts the caller
// can see: manager or admin tier only, fenced to the caller's branch reach,
// both open statuses by default or the one named status, newest first.
func (s *Service) ListOpenCardRefundAttempts(ctx context.Context, statusFilter string) ([]CardRefundAttempt, error) {
	caller, ok := identity.StaffContextFromContext(ctx)
	if !ok || !caller.IsManager() {
		return nil, clientErr(http.StatusForbidden, "pos: a manager must list the open card refund attempts")
	}
	switch f := strings.ToUpper(strings.TrimSpace(statusFilter)); f {
	case "", "IN_FLIGHT", "UNBOOKED":
		items, err := s.store.ListOpenCardRefundAttempts(ctx, f, reachableBranches(caller))
		if err != nil {
			return nil, err
		}
		if items == nil {
			items = []CardRefundAttempt{} // an empty queue answers [], never null
		}
		return items, nil
	default:
		return nil, clientErr(http.StatusUnprocessableEntity, "pos: status filter must be IN_FLIGHT or UNBOOKED")
	}
}

// ResolveCardRefundAttempt records a manager's judgment of one open attempt
// after checking the processor. REFUNDED moves the row to UNBOOKED with the
// processor's refund_ref (an empty one refuses: the manager must name what
// the processor holds), so the return's same-key retry books without a
// second processor call; NOT_REFUNDED closes the row FAILED, releasing the
// capture ceiling. The initiator is refused (separation of duties), another
// branch's attempt is a plain 404, and every resolution writes its
// sensitive-access audit row inside the settle's own transaction.
func (s *Service) ResolveCardRefundAttempt(ctx context.Context, attemptID string, in ResolveCardRefundAttemptInput) (CardRefundAttempt, error) {
	caller, ok := identity.StaffContextFromContext(ctx)
	if !ok || !caller.IsManager() {
		return CardRefundAttempt{}, clientErr(http.StatusForbidden, "pos: a manager must resolve a card refund attempt")
	}
	actor := identity.StaffUserIDFromContext(ctx)
	if _, err := uuid.Parse(attemptID); err != nil {
		return CardRefundAttempt{}, clientErr(http.StatusUnprocessableEntity, "pos: card refund attempt id must be a uuid")
	}
	outcome := strings.ToUpper(strings.TrimSpace(in.Outcome))
	if outcome != CardRefundOutcomeRefunded && outcome != CardRefundOutcomeNotRefunded {
		return CardRefundAttempt{}, clientErr(http.StatusUnprocessableEntity, "pos: outcome must be REFUNDED or NOT_REFUNDED")
	}
	note := trimReason(strings.TrimSpace(in.Note))

	var out CardRefundAttempt
	if err := s.store.RunInTx(ctx, func(tx pgx.Tx) error {
		row, found, err := s.store.CardRefundAttemptForUpdateTx(ctx, tx, attemptID)
		if err != nil {
			return err
		}
		if !found || !caller.CanAccessBranch(row.BranchID) {
			return clientErr(http.StatusNotFound, "pos: card refund attempt not found")
		}
		// Separation of duties: the person who started the refund never judges
		// its processor outcome, whoever they are (HH-767).
		if actor == "" || (row.InitiatedBy != "" && actor == row.InitiatedBy) {
			return clientErr(http.StatusForbidden, "pos: the person who started this card refund cannot resolve its processor outcome; another manager must resolve it")
		}
		switch row.Status {
		case "IN_FLIGHT", "UNBOOKED":
		default:
			return clientErr(http.StatusConflict, "pos: card refund attempt is already %s", row.Status)
		}
		// The live-request fence (PR #501 round 1 P1-1): an IN_FLIGHT row with
		// no recorded outcome may still be at the processor. It settles only
		// once the unknown outcome is recorded (processor_at stamped by the
		// hold) or the window of the processor client's own timeout plus the
		// crash margin is past; an UNBOOKED row (the reversal already answered
		// or a rolled-back booking) is always settleable.
		if row.Status == "IN_FLIGHT" && row.ProcessorAt == nil {
			if attemptedAt, perr := time.Parse(time.RFC3339, row.AttemptedAt); perr == nil &&
				time.Now().UTC().Before(attemptedAt.Add(payment.ProcessorClientTimeout+cardRefundLiveRequestMargin)) {
				return clientErr(http.StatusConflict,
					"pos: this card refund may still be running at the processor; resolve it after the live request ends")
			}
		}
		if outcome == CardRefundOutcomeRefunded {
			// The never sent flag is sticky against REFUNDED (PR #501 round 4 P2-1): the dispatch
			// knows no call was made under this row, so no manager's word can make its ref a
			// processor reversal. Only NOT_REFUNDED (so the cashier's retry sends the call) or a
			// restage clears it.
			if row.CallNotSent {
				return clientErr(http.StatusConflict,
					"pos: this refund was never sent to the processor; settle it as not refunded so the cashier's retry sends it")
			}
			ref := strings.TrimSpace(in.RefundRef)
			if row.RefundRef != "" && ref != "" && ref != row.RefundRef {
				return clientErr(http.StatusConflict, "pos: card refund attempt already carries processor ref %s", row.RefundRef)
			}
			if row.RefundRef != "" {
				ref = row.RefundRef
			}
			if ref == "" {
				return clientErr(http.StatusUnprocessableEntity, "pos: resolving a card refund attempt as REFUNDED requires the processor's refund_ref")
			}
			// The duplicate-ref fence (PR #501 round 1 P1-2): one processor
			// reversal ref may never ride two attempts or double-book against a
			// payment that already landed under it.
			onAttempt, onPayment, derr := s.store.CardRefundRefUsedElsewhereTx(ctx, tx, ref, row.RefundKey)
			if derr != nil {
				return derr
			}
			if onAttempt || onPayment {
				return clientErr(http.StatusConflict,
					"pos: processor ref %s is already recorded against another card refund or a booked payment; check the processor console", ref)
			}
			if err := s.store.ApplyCardRefundResolutionTx(ctx, tx, attemptID, "UNBOOKED", ref, actor, note); err != nil {
				return err
			}
			row.Status, row.RefundRef = "UNBOOKED", ref
		} else {
			// A row that already recorded the processor's reversal ref moved
			// money: it must be booked or settled REFUNDED, never closed FAILED.
			if row.Status == "UNBOOKED" && row.RefundRef != "" && !row.CallNotSent {
				return clientErr(http.StatusConflict, "pos: card refund attempt already has processor reversal ref %s; settle it as REFUNDED or book the return", row.RefundRef)
			}
			if err := s.store.ApplyCardRefundResolutionTx(ctx, tx, attemptID, "FAILED", "", actor, note); err != nil {
				return err
			}
			row.Status = "FAILED"
		}
		row.ResolvedBy = actor
		row.ResolutionNote = note
		now := time.Now().UTC().Format(time.RFC3339)
		row.ResolvedAt = &now
		out = row
		// The sensitive-access row rides this transaction (PR #501 round 1
		// P2-2): the outcome, the resulting status, the amount and the
		// approver, so a CASL-style question about who closed money is
		// answered from the action alone. A failed write aborts the settle;
		// money is never closed without its row.
		if s.audit == nil {
			return nil
		}
		ride, ok := s.audit.(txRidingAudit)
		if !ok {
			return clientErr(http.StatusInternalServerError, "pos: the wired audit recorder cannot write the resolution row on the settle's transaction")
		}
		return ride.RecordTx(ctx, tx, audit.SensitiveAccess{
			Action:     audit.ActCardRefundAttemptResolved,
			EntityType: "pos.card_refund_attempt",
			EntityID:   out.ID,
			ActorID:    actor,
			BranchID:   out.BranchID,
			Purpose:    "POS_CARD_REFUND_RESOLUTION",
			Reason:     note,
			Extra: map[string]any{
				"outcome":      outcome,
				"status":       out.Status,
				"amount_cents": out.AmountCents,
				"approver_id":  actor,
			},
		})
	}); err != nil {
		return CardRefundAttempt{}, err
	}
	return out, nil
}

// errCardRefundAlreadyResolved is the typed signal of the dispatch-side writes
// (FailCardRefundAttempt, HoldCardRefundAttemptInFlight) meeting a row a manager
// already resolved: the processor's answer contradicts the manager's word.
var errCardRefundAlreadyResolved = errors.New("pos: the card refund attempt was already resolved by a manager")
