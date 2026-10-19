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
	"net/http"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The two resolutions a manager can record after checking the processor.
const (
	CardRefundOutcomeRefunded    = "REFUNDED"
	CardRefundOutcomeNotRefunded = "NOT_REFUNDED"
)

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
// sensitive-access audit row.
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
		if outcome == CardRefundOutcomeRefunded {
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
			if err := s.store.ApplyCardRefundResolutionTx(ctx, tx, attemptID, "UNBOOKED", ref, actor, note); err != nil {
				return err
			}
			row.Status, row.RefundRef = "UNBOOKED", ref
		} else {
			// A row that already recorded the processor's reversal ref moved
			// money: it must be booked or settled REFUNDED, never closed FAILED.
			if row.Status == "UNBOOKED" && row.RefundRef != "" {
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
		return nil
	}); err != nil {
		return CardRefundAttempt{}, err
	}

	// The sensitive-access row, best effort after the commit (the s.record
	// discipline): the outcome, the resulting status, the amount and the
	// approver, so a CASL-style question about who closed money is answered
	// from the action alone.
	s.record(ctx, audit.SensitiveAccess{
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
	return out, nil
}
