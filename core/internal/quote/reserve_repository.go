package quote

// reserve_repository.go, the pgx half of the quote hold lifecycle (reserve.go).

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// compile-time proof the real repository satisfies the hold store.
var _ holdStore = (*Repository)(nil)

// ReservableLinesTx reads a quote's (product, qty_base) projection on the caller's transaction.
// A NULL product_id comes back as "" rather than being filtered in SQL, so the "which lines are
// skipped and why" decision lives in one Go place (syncQuoteHoldsTx) and is unit-testable.
func (r *Repository) ReservableLinesTx(ctx context.Context, tx pgx.Tx, quoteID string) ([]reservableLine, error) {
	rows, err := tx.Query(ctx, `
		SELECT line_no, COALESCE(product_id::text,''), qty_base
		  FROM quote_lines WHERE quote_id = $1 ORDER BY line_no`, quoteID)
	if err != nil {
		return nil, fmt.Errorf("quote reservable lines: %w", err)
	}
	defer rows.Close()
	var out []reservableLine
	for rows.Next() {
		var l reservableLine
		if err := rows.Scan(&l.LineNo, &l.ProductID, &l.QtyBase); err != nil {
			return nil, fmt.Errorf("scan quote reservable line: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// QuoteHoldContextTx reads the branch the holds belong to and the expiry they should carry.
//
// valid_until is a DATE. It is projected to the END of that day in UTC, not midnight: a quote valid
// "until the 20th" is valid ON the 20th, and expiring the hold at 00:00 would kill it a full day
// before the quote it belongs to, the same date-grained ruling quoteExpired() makes for the quote
// itself, applied to the hold so the two cannot disagree.
func (r *Repository) QuoteHoldContextTx(ctx context.Context, tx pgx.Tx, quoteID string) (string, *time.Time, error) {
	var branchID string
	var validUntil *time.Time
	err := tx.QueryRow(ctx,
		`SELECT COALESCE(branch_id::text,''), valid_until FROM quotes WHERE id = $1`, quoteID).
		Scan(&branchID, &validUntil)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil, ErrNotFound
		}
		return "", nil, fmt.Errorf("quote hold context: %w", err)
	}
	if validUntil != nil {
		end := time.Date(validUntil.Year(), validUntil.Month(), validUntil.Day(),
			23, 59, 59, 0, time.UTC)
		validUntil = &end
	}
	return branchID, validUntil, nil
}

// LockQuoteTx runs fn under the quote row's write lock, so an explicit hold resync serializes against
// a concurrent line write (which takes the same lock in lockQuoteForEdit).
func (r *Repository) LockQuoteTx(ctx context.Context, quoteID string, fn func(ctx context.Context, tx pgx.Tx) error) error {
	if r.pool == nil {
		return errNoDB
	}
	if !looksLikeUUID(quoteID) {
		return ErrNotFound
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("lock quote: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tx.QueryRow(ctx, `SELECT 1 FROM quotes WHERE id = $1 FOR UPDATE`, quoteID).
		Scan(new(int)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("lock quote %q: %w", quoteID, err)
	}
	if fn != nil {
		if err := fn(ctx, tx); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("lock quote: commit: %w", err)
	}
	return nil
}

// QuoteHolds reads this quote's ACTIVE holds for the surface.
//
// Like order.OrderHolds, this SELECTs stock_reservations, inventory's table, and the same narrow
// justification applies: it is a read of rows THIS MODULE WROTE, keyed by (source_type='quote',
// source_ref_id=this quote). It writes nothing and computes no availability; every number that
// gates anything still comes through the AvailabilityReader seam.
func (r *Repository) QuoteHolds(ctx context.Context, quoteID string) ([]QuoteHold, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if !looksLikeUUID(quoteID) {
		return nil, ErrNotFound
	}
	rows, err := r.pool.Query(ctx, `
		SELECT sr.id::text, sr.product_id::text, COALESCE(p.name,''), sr.branch_id::text,
		       sr.hold_mode, sr.qty_base,
		       COALESCE(to_char(sr.expires_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),'')
		  FROM stock_reservations sr
		  LEFT JOIN products p ON p.id = sr.product_id
		 WHERE sr.source_type = 'quote' AND sr.source_ref_id = $1 AND sr.status = 'ACTIVE'
		 ORDER BY sr.created_at, sr.id`, quoteID)
	if err != nil {
		return nil, fmt.Errorf("quote holds: %w", err)
	}
	defer rows.Close()
	out := []QuoteHold{}
	for rows.Next() {
		var h QuoteHold
		if err := rows.Scan(&h.ReservationID, &h.ProductID, &h.ProductName, &h.BranchID,
			&h.HoldMode, &h.QtyBase, &h.ExpiresAt); err != nil {
			return nil, fmt.Errorf("scan quote hold: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
