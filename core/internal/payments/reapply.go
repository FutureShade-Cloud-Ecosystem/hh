package payments

// reapply.go, SF-2l: re-applying the money a reversal holds in 2310 to an invoice.
//
// A reversal (reversal.go) posts Dr AR 1100 / Cr 2310 "Customer Refunds and Unapplied Cash": the money stays with the
// dealer, held for the customer. Re-application is the act that clears it by applying that money to invoices of the
// same customer: Dr 2310 / Cr AR, the invoice balance and the AR subledger moving through the same settle slice the
// payment settle runs. It is also the right fix for a payment rung on the wrong account: a new tender would count the
// cash twice in the drawer, because the till's expected tenders count every RECEIVED payment of the session and the
// reversed original is still one.
//
// THE RULES, written once:
//
//  1. Only a REVERSED payment holds money. The held figure is its amount less what has already been re-applied
//     (payment_reapplications, migration 0431); a request above it is refused with nothing written, and the table's
//     own guard trigger refuses the same thing at the insert.
//  2. The invoices must be the payment's own customer's, open (FINALIZED or PARTIALLY_PAID), inside the caller's branch
//     reach and in the payment's own branch. The branch rule is the settle's: the 2310 liability was booked to the
//     payment's branch at the reversal, and the journal that clears it carries ONE branch, so an invoice of another
//     branch would move that branch's AR with the liability left in this one. Another customer's invoice is never
//     allowed: the source does not say the money may follow the customer's group, so it is not built.
//  3. Each invoice takes at most its payable (balance less holdback), as the settle does.
//  4. Re-application is not a payment. It writes no payments row, no till movement and no tender: the cash is already
//     in the drawer's count through the reversed original, and a payments row would count it again. The original stays
//     reversed, so it cannot be reversed again (ErrAlreadyReversed) or bounced (MarkNSF refuses a reversed payment),
//     and the applied slices are rows of payment_reapplications, which no reversal or bounce reads. An applied slice is
//     therefore final here: taking money back out of an invoice into 2310 is a separate act, not built by this item.
//  5. The write takes its rows in the one money lock order (payments/LOCK-ORDER.md): the payment row FOR UPDATE (the
//     lock a reversal and a bounce take first), then the named invoice rows FOR NO KEY UPDATE in ascending id order,
//     then the invoices counter the balance moves draw. It draws no payments counter: it stamps no payments row.
//  6. A request carries an idempotency key. The same key with the same body answers the first result and writes
//     nothing; a spent key with a different body is refused (409).
//
// The GL books one journal per request on the key payment.reapplied:<batch id>: Dr 2310 (refund_payable) / Cr AR for
// the total, through the outbox row written in the same transaction.

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// ActPaymentReapplied is the audit verb for a re-application.
const ActPaymentReapplied = "PAYMENT_REAPPLIED"

var (
	// ErrNotReapplicable: the payment holds no money to re-apply (it was never reversed, it is itself a reversing
	// entry, or it has no account to hold the money for).
	ErrNotReapplicable = errors.New("payments: no money is held for this payment")
	// ErrReapplyOverHeld: the request asks for more than the payment still holds.
	ErrReapplyOverHeld = errors.New("payments: that is more than the payment still holds")
	// ErrReapplyReasonRequired is the reason rule, as its own error so a UI can prompt for it.
	ErrReapplyReasonRequired = errors.New("payments: a re-application reason is required")
)

// ReapplyInput is one re-application request.
type ReapplyInput struct {
	PaymentID      string
	Reason         string
	ActorID        string
	IdempotencyKey string
	// Allocations names the invoices and the cents each takes. Their order does not matter: the walk sorts them.
	Allocations []Allocation
	// IsManager is resolved by the caller from the identity context; the service refuses without it.
	IsManager bool
	// CanAccessBranch is the caller's branch reach: a payment or an invoice in a branch it does not reach is not
	// found, the same answer as a missing id. Nil means an internal caller with no branch to be fenced to.
	CanAccessBranch func(branchID string) bool
}

// ReapplyLine is one invoice's share of a re-application.
type ReapplyLine struct {
	InvoiceID         string `json:"invoice_id"`
	InvoiceNo         string `json:"invoice_no"`
	AppliedCents      int64  `json:"applied_cents"`
	Outcome           string `json:"outcome"`
	BalanceAfterCents int64  `json:"balance_after_cents"`
}

// Reapplication is the answer: what was applied, and what the payment still holds after it.
type Reapplication struct {
	PaymentID       string        `json:"payment_id"`
	BatchID         string        `json:"batch_id"`
	AppliedCents    int64         `json:"applied_cents"`
	HeldBeforeCents int64         `json:"held_before_cents"`
	HeldAfterCents  int64         `json:"held_after_cents"`
	Lines           []ReapplyLine `json:"lines"`
	Replayed        bool          `json:"replayed"`
}

// HeldFigure is what one payment holds in 2310 right now: its amount less what has been re-applied, and zero for a
// payment that was never reversed (or is itself a reversing entry), which holds nothing.
type HeldFigure struct {
	PaymentID    string `json:"payment_id"`
	PaymentNo    string `json:"payment_no"`
	AmountCents  int64  `json:"amount_cents"`
	Reversed     bool   `json:"reversed"`
	AppliedCents int64  `json:"applied_cents"`
	HeldCents    int64  `json:"held_cents"`
}

// reapplyStore is the persistence the re-application service needs.
type reapplyStore interface {
	ReapplyHeld(ctx context.Context, in ReapplyInput, now time.Time) (Reapplication, error)
	HeldFor(ctx context.Context, paymentID string, canAccessBranch func(branchID string) bool) (HeldFigure, error)
}

// validateReapply is the request check that runs before any database is consulted: the ids are normalised to one
// canonical spelling (uuid.Parse accepts upper case, braces and the undashed form, and Postgres folds them onto one
// row), the list is sorted ascending by invoice id (the lock order), and the money must be positive. It answers the
// request with its normalised fields.
func validateReapply(in ReapplyInput) (ReapplyInput, error) {
	if strings.TrimSpace(in.Reason) == "" {
		return ReapplyInput{}, ErrReapplyReasonRequired
	}
	if strings.TrimSpace(in.IdempotencyKey) == "" {
		return ReapplyInput{}, httpx.BadRequestf("re-applying held money needs an idempotency key")
	}
	pid, err := uuid.Parse(strings.TrimSpace(in.PaymentID))
	if err != nil {
		return ReapplyInput{}, httpx.BadRequestf("payment id must be a uuid")
	}
	in.PaymentID = pid.String()
	if len(in.Allocations) == 0 {
		return ReapplyInput{}, httpx.BadRequestf("name at least one invoice to apply the held money to")
	}
	if len(in.Allocations) > MaxApplyInvoices {
		return ReapplyInput{}, httpx.BadRequestf("a re-application may name at most %d invoices", MaxApplyInvoices)
	}
	seen := map[string]bool{}
	allocs := make([]Allocation, 0, len(in.Allocations))
	for _, a := range in.Allocations {
		u, err := uuid.Parse(strings.TrimSpace(a.InvoiceID))
		if err != nil {
			return ReapplyInput{}, httpx.BadRequestf("invoice ids must be uuids")
		}
		id := u.String()
		if seen[id] {
			return ReapplyInput{}, httpx.BadRequestf("the same invoice is named twice: %s", id)
		}
		seen[id] = true
		if a.AmountCents <= 0 {
			return ReapplyInput{}, httpx.BadRequestf("each amount applied must be greater than zero")
		}
		allocs = append(allocs, Allocation{InvoiceID: id, AmountCents: a.AmountCents})
	}
	sort.Slice(allocs, func(i, j int) bool { return allocs[i].InvoiceID < allocs[j].InvoiceID })
	in.Allocations = allocs
	in.Reason = strings.TrimSpace(in.Reason)
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	return in, nil
}

// Reapply re-applies held money: manager only, reason required, audited. The store does the write.
func (s *ReverseService) Reapply(ctx context.Context, in ReapplyInput) (Reapplication, error) {
	if !in.IsManager {
		return Reapplication{}, errors.New("payments: re-applying held money is a manager action")
	}
	if s.reapply == nil {
		return Reapplication{}, errors.New("payments: re-application is not wired")
	}
	in, err := validateReapply(in)
	if err != nil {
		return Reapplication{}, err
	}
	res, err := s.reapply.ReapplyHeld(ctx, in, s.clock())
	if err != nil {
		return Reapplication{}, err
	}
	if s.audit != nil && !res.Replayed {
		lines := make([]map[string]any, 0, len(res.Lines))
		for _, l := range res.Lines {
			lines = append(lines, map[string]any{"invoice_id": l.InvoiceID, "applied_cents": l.AppliedCents})
		}
		_ = s.audit.Record(ctx, audit.SensitiveAccess{
			Action: ActPaymentReapplied, EntityType: "payment", EntityID: in.PaymentID,
			ActorID: in.ActorID, Purpose: "REAPPLY", Reason: in.Reason,
			Extra: map[string]any{
				"batch_id":          res.BatchID,
				"applied_cents":     res.AppliedCents,
				"held_before_cents": res.HeldBeforeCents,
				"held_after_cents":  res.HeldAfterCents,
				"lines":             lines,
			},
		})
	}
	return res, nil
}

// sameReapplyBody answers whether the rows a key already wrote are THIS request's own replay: the same payment and
// the same invoice to cents map. The reason is a note, not part of the body: a retry with a reworded reason is still
// the same act.
func sameReapplyBody(paymentID string, stored map[string]int64, storedPayment string, in ReapplyInput) bool {
	if storedPayment != paymentID || len(stored) != len(in.Allocations) {
		return false
	}
	for _, a := range in.Allocations {
		if stored[a.InvoiceID] != a.AmountCents {
			return false
		}
	}
	return true
}

func replayConflict() error {
	return ReplayConflictError{Msg: "that idempotency key already re-applied held money differently; use a new key"}
}

// Held answers what a payment holds, for the desk's re-application form. A plain read, no lock: the figure the write
// re-judges under the payment row. Manager only, like the write.
func (s *ReverseService) Held(ctx context.Context, paymentID string, isManager bool, canAccessBranch func(branchID string) bool) (HeldFigure, error) {
	if !isManager {
		return HeldFigure{}, errors.New("payments: reading held money is a manager action")
	}
	if s.reapply == nil {
		return HeldFigure{}, errors.New("payments: re-application is not wired")
	}
	return s.reapply.HeldFor(ctx, paymentID, canAccessBranch)
}

// InvoiceReapplication is one slice of a reversed payment's held money applied to an invoice, as the register reads it
// (SF-2r): the invoice's own applied entry for it.
type InvoiceReapplication struct {
	ID          string    `json:"id"`
	PaymentID   string    `json:"payment_id"`
	PaymentNo   string    `json:"payment_no"`
	Method      string    `json:"method"`
	AmountCents int64     `json:"amount_cents"`
	AppliedAt   time.Time `json:"applied_at"`
	AppliedBy   string    `json:"applied_by"` // users.id, "" when none
	AppliedName string    `json:"applied_name"`
}

// OrderReapplication is one slice applied to an invoice of an order, as the order's activity reads it (SF-2r round 2).
type OrderReapplication struct {
	ID          string    `json:"id"`
	PaymentID   string    `json:"payment_id"`
	PaymentNo   string    `json:"payment_no"`
	Method      string    `json:"method"`
	InvoiceNo   string    `json:"invoice_no"`
	AmountCents int64     `json:"amount_cents"`
	AppliedAt   time.Time `json:"applied_at"`
	AppliedBy   string    `json:"applied_by"`
	AppliedName string    `json:"applied_name"`
}
