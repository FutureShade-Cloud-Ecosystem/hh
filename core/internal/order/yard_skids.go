package order

// yard_skids.go, the skid count on an order, changeable by the yard with a reason (migration 0366).
//
// WHAT EXISTED. No order recorded how many skids it left the yard on, and no skid deposit charge
// existed anywhere in the core. This holds the count, the deposit per skid (a rate, zero until
// the office sets one, since none exists today), and the deposit that follows from them. The deposit
// is RECORDED, not billed: no invoice line type carries it, and a notice reports what is owed back or
// due so the office can act on it.
//
// A change is any count that differs from the recorded one, the first record included (it has no
// before). The same count again is not a change and is not told to anyone.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// YardSkidInput is one skid count for one order.
type YardSkidInput struct {
	OrderID   string
	ActorID   string
	SkidCount int
	Reason    string
	Note      string
}

// YardSkidOutcome is the count before and after, the deposit before and after, and the order's
// facts. CountBefore is nil when no count had been recorded. On the facts, OwedBackCents and DueCents
// are the skid deposit's: set only when the order's invoice has taken money.
type YardSkidOutcome struct {
	Order              YardOrderFacts
	CountBefore        *int
	CountAfter         int
	DepositBeforeCents int64
	DepositAfterCents  int64
	Changed            bool
	// ChangeID is the history row of this change, empty when nothing changed. A notice keys on it.
	ChangeID string
}

type yardSkidStore interface {
	RecordYardSkidsTx(ctx context.Context, in YardSkidInput) (YardSkidOutcome, error)
}

// RecordYardSkids records or changes the order's skid count. See the file header.
func (s *Service) RecordYardSkids(ctx context.Context, in YardSkidInput) (YardSkidOutcome, error) {
	if !looksLikeUUID(in.OrderID) {
		return YardSkidOutcome{}, ErrNotFound
	}
	if in.SkidCount < 0 {
		return YardSkidOutcome{}, httpx.BadRequestf("a skid count cannot be negative")
	}
	if strings.TrimSpace(in.Reason) == "" {
		return YardSkidOutcome{}, httpx.BadRequestf("a skid count change needs a reason")
	}
	st, ok := s.store.(yardSkidStore)
	if !ok {
		return YardSkidOutcome{}, ErrEditNotConfigured
	}
	out, err := st.RecordYardSkidsTx(ctx, in)
	if err != nil {
		return YardSkidOutcome{}, err
	}
	if s.standing != nil {
		stand, err := s.standing.InvoiceStanding(ctx, in.OrderID)
		if err != nil {
			return YardSkidOutcome{}, fmt.Errorf("order: invoice standing for %s: %w", in.OrderID, err)
		}
		if stand.Found {
			out.Order.InvoiceNo, out.Order.InvoicedCents, out.Order.PaidCents = stand.InvoiceNo, stand.TotalCents, stand.PaidCents
			if stand.PaidCents > 0 {
				delta := out.DepositAfterCents - out.DepositBeforeCents
				if delta < 0 {
					out.Order.OwedBackCents = -delta
				} else {
					out.Order.DueCents = delta
				}
			}
		}
	}
	return out, nil
}

// RecordYardSkidsTx is the transaction: the count row, the deposit it implies and the history row
// commit together under the order lock.
func (r *Repository) RecordYardSkidsTx(ctx context.Context, in YardSkidInput) (YardSkidOutcome, error) {
	if r.pool == nil {
		return YardSkidOutcome{}, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return YardSkidOutcome{}, fmt.Errorf("yard skids: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		out    YardSkidOutcome
		status string
		total  int64
	)
	f := &out.Order
	err = tx.QueryRow(ctx, `
SELECT status, total_cents, order_no::text, customer_id::text, branch_id::text, COALESCE(salesperson_id::text, '')
  FROM orders WHERE id = $1 FOR UPDATE`, in.OrderID).
		Scan(&status, &total, &f.OrderNo, &f.CustomerID, &f.BranchID, &f.SalespersonID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return YardSkidOutcome{}, ErrNotFound
		}
		return YardSkidOutcome{}, fmt.Errorf("yard skids: lock order: %w", err)
	}
	if !slices.Contains(yardPickStatuses, status) {
		return YardSkidOutcome{}, &NotEditableError{Field: FieldLineQty, Status: status}
	}
	f.TotalBeforeCents, f.TotalAfterCents = total, total

	var (
		have        bool
		beforeCount int
		rate        int64
	)
	err = tx.QueryRow(ctx, `
SELECT skid_count, deposit_per_skid_cents, deposit_cents FROM order_skids WHERE order_id = $1 FOR UPDATE`,
		in.OrderID).Scan(&beforeCount, &rate, &out.DepositBeforeCents)
	switch {
	case err == nil:
		have = true
		out.CountBefore = &beforeCount
	case errors.Is(err, pgx.ErrNoRows):
	default:
		return YardSkidOutcome{}, fmt.Errorf("yard skids: read: %w", err)
	}
	out.CountAfter = in.SkidCount
	out.DepositAfterCents = int64(in.SkidCount) * rate
	if have && beforeCount == in.SkidCount {
		return out, nil
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO order_skids (order_id, skid_count, deposit_per_skid_cents, deposit_cents, updated_by)
VALUES ($1, $2, 0, 0, NULLIF($3, '')::uuid)
ON CONFLICT (order_id) DO UPDATE SET skid_count = EXCLUDED.skid_count,
  deposit_cents = EXCLUDED.skid_count * order_skids.deposit_per_skid_cents,
  updated_by = EXCLUDED.updated_by, updated_at = now()`, in.OrderID, in.SkidCount, in.ActorID); err != nil {
		return YardSkidOutcome{}, fmt.Errorf("yard skids: write: %w", err)
	}
	if err := tx.QueryRow(ctx, `
INSERT INTO order_skid_changes (order_id, count_before, count_after, deposit_before_cents,
                                deposit_after_cents, reason, note, changed_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, '')::uuid)
RETURNING id::text`,
		in.OrderID, out.CountBefore, in.SkidCount, out.DepositBeforeCents, out.DepositAfterCents,
		in.Reason, in.Note, in.ActorID).Scan(&out.ChangeID); err != nil {
		return YardSkidOutcome{}, fmt.Errorf("yard skids: history: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return YardSkidOutcome{}, fmt.Errorf("yard skids: commit: %w", err)
	}
	out.Changed = true
	return out, nil
}
