package payments

// payout.go, SF-2m and SF-2n: money a reversal holds in 2310 leaves it only by the act that moves the money.
//
// SF-2l (reapply.go) is the first such act, applying the held money to an invoice. This file is the others:
//
//   - DRAWER: the dealer hands the customer cash from the till, Dr 2310 / Cr 1020, with a PAID_OUT movement on an OPEN
//     till session of the payment's branch, written by the till through its own seam (the till is the sole writer of
//     till_movements) in the same transaction, so the drawer's expected cash falls with the books.
//   - BANK: a cheque or transfer written to the customer, Dr 2310 / Cr 1000, with its reference recorded.
//   - WRITE_OFF (SF-2n): the cash left the drawer unrecorded and the count has posted the shortage, Dr 2310 / Cr 6350.
//     Refused until the payment's till session is CLOSED with a shortage that covers it, and never more than the
//     shortage not already written off by the session's other payments.
//
// THE HELD FIGURE is the reversed amount less every act that took money out of 2310: re-applications and every kind of
// payout row. Each act is a manager action with a reason and an idempotency key, judged under the payment row lock
// (the payment row first, LOCK-ORDER.md), refused above the held figure with nothing written, and booked by one
// payment.paid_out outbox row for the GL (one journal per act). The table's guard trigger refuses an over take whoever
// writes it.

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// ActPaymentPaidOut is the audit verb for a payout of held money.
const ActPaymentPaidOut = "PAYMENT_HELD_PAID_OUT"

// The kinds of act that take money out of 2310 other than a re-application.
const (
	PayoutDrawer   = "DRAWER"
	PayoutBank     = "BANK"
	PayoutWriteOff = "WRITE_OFF"
)

// creditRole is the posting role of the account a kind credits against 2310.
func creditRole(kind string) string {
	switch kind {
	case PayoutDrawer:
		return "drawer"
	case PayoutBank:
		return "bank"
	case PayoutWriteOff:
		return "write_off"
	}
	return ""
}

var (
	// ErrPayoutNotAllowed: the act cannot be done now (a write off before the count, no open drawer, no shortage left).
	ErrPayoutNotAllowed = errors.New("payments: that cannot be done with the money held for this payment")
	// ErrPayoutReasonRequired is the reason rule.
	ErrPayoutReasonRequired = errors.New("payments: a reason is required")
)

// PayoutRefusedError is a refusal the till's seam says in words with the status the route answers (an session that is
// not open, another branch's drawer): the till returns it so the route needs no till type.
type PayoutRefusedError struct {
	Status int
	Msg    string
}

func (e PayoutRefusedError) Error() string { return e.Msg }

// PayoutInput is one pay out or write off request.
type PayoutInput struct {
	PaymentID      string
	Kind           string
	AmountCents    int64
	Reason         string
	Reference      string // BANK: the cheque number or transfer reference, required
	TillSessionID  string // DRAWER: the OPEN session the cash leaves; empty lets the till find the branch's one open drawer
	ActorID        string
	IdempotencyKey string
	IsManager      bool
	// CanAccessBranch is the caller's branch reach: a payment outside it is not found.
	CanAccessBranch func(branchID string) bool
}

// Payout is the answer: what left 2310, and what the payment still holds.
type Payout struct {
	PaymentID       string `json:"payment_id"`
	PayoutID        string `json:"payout_id"`
	Kind            string `json:"kind"`
	AmountCents     int64  `json:"amount_cents"`
	HeldBeforeCents int64  `json:"held_before_cents"`
	HeldAfterCents  int64  `json:"held_after_cents"`
	Replayed        bool   `json:"replayed"`
}

// DrawerPaidOut is the till's part of a drawer pay out: the PAID_OUT movement on the named session.
type DrawerPaidOut struct {
	SessionID   string // empty: the till takes the branch's one OPEN drawer, and refuses when there is none or more than one
	BranchID    string // the payment's branch: the session must be this branch's
	AmountCents int64
	Reason      string
	Reference   string
}

// SessionCount is what the till holds about a counted session, for the write off.
type SessionCount struct {
	Status         string // OPEN, COUNTED or CLOSED
	BranchID       string
	OverShortCents int64 // counted less expected, negative when short; meaningful once CLOSED
}

// TillSeam is the till's side of the drawer pay out and the write off, implemented by the till (which imports this
// package, so the interface lives here). Both run on the caller's transaction.
type TillSeam interface {
	// PayOutHeldMoney writes the PAID_OUT movement and answers the session it landed on.
	PayOutHeldMoney(ctx context.Context, tx pgx.Tx, in DrawerPaidOut) (sessionID string, err error)
	SessionCountTx(ctx context.Context, tx pgx.Tx, sessionID string) (SessionCount, error)
}

// payoutStore is the persistence the pay out service needs.
type payoutStore interface {
	PayOutHeld(ctx context.Context, in PayoutInput, now time.Time) (Payout, error)
}

// validatePayout is the request check before any database is consulted.
func validatePayout(in PayoutInput) (PayoutInput, error) {
	in.Reason = strings.TrimSpace(in.Reason)
	in.Reference = strings.TrimSpace(in.Reference)
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	if in.Reason == "" {
		return PayoutInput{}, ErrPayoutReasonRequired
	}
	if in.IdempotencyKey == "" {
		return PayoutInput{}, httpx.BadRequestf("paying out held money needs an idempotency key")
	}
	if creditRole(in.Kind) == "" {
		return PayoutInput{}, httpx.BadRequestf("the kind must be DRAWER, BANK or WRITE_OFF")
	}
	if in.AmountCents <= 0 {
		return PayoutInput{}, httpx.BadRequestf("the amount must be greater than zero")
	}
	switch in.Kind {
	case PayoutBank:
		if in.Reference == "" {
			return PayoutInput{}, httpx.BadRequestf("a bank pay out needs the cheque number or transfer reference")
		}
	}
	return in, nil
}

// PayOut takes held money out of 2310: manager only, reason required, audited. The store does the write.
func (s *ReverseService) PayOut(ctx context.Context, in PayoutInput) (Payout, error) {
	if !in.IsManager {
		return Payout{}, errors.New("payments: paying out held money is a manager action")
	}
	if s.payout == nil {
		return Payout{}, errors.New("payments: pay out is not wired")
	}
	in, err := validatePayout(in)
	if err != nil {
		return Payout{}, err
	}
	res, err := s.payout.PayOutHeld(ctx, in, s.clock())
	if err != nil {
		return Payout{}, err
	}
	if s.audit != nil && !res.Replayed {
		_ = s.audit.Record(ctx, audit.SensitiveAccess{
			Action: ActPaymentPaidOut, EntityType: "payment", EntityID: in.PaymentID,
			ActorID: in.ActorID, Purpose: in.Kind, Reason: in.Reason,
			Extra: map[string]any{
				"payout_id":         res.PayoutID,
				"kind":              in.Kind,
				"amount_cents":      res.AmountCents,
				"reference":         in.Reference,
				"held_before_cents": res.HeldBeforeCents,
				"held_after_cents":  res.HeldAfterCents,
			},
		})
	}
	return res, nil
}
