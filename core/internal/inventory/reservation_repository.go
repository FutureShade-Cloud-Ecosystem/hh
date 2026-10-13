package inventory

// reservation_repository.go, the pgx half of DIB-60: stock_reservations rows plus the
// stock_balances.qty_reserved projection they maintain, and the branch_allocation_policy read/write.
//
// PROJECTION DISCIPLINE. Every write that changes a hold changes the projection in the SAME
// statement or the same transaction, never later and never in a background reconcile. The reason is
// that available is read on the hot path (a counter sale, a quote line) and a read model that is
// "eventually" right is a read model that oversells during the eventually.
//
// The RELEASE direction clamps at zero (GREATEST(... , 0)). That clamp is belt to the 0115 CHECK's
// braces: the CHECK turns a negative into a failed transaction, and the clamp means the only way to
// reach it is a genuine double-release rather than float noise on a DECIMAL(19,4) round trip.
//
// Timestamps cross the wire as to_char'd strings, matching the RecentMoves convention already in
// repository.go, one format for the whole module, chosen in SQL, so no Go-side formatting drift.

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// tsFmt is the module's wire timestamp format (matches repository.go RecentMoves).
const tsFmt = `'YYYY-MM-DD"T"HH24:MI:SSOF'`

// AllocationPolicy reads a branch's configured policy. found=false → caller uses the system default.
// tx joins the caller's transaction (nil tx = the pool, the seam's convention): the reserve path
// passes its tx so the read runs on the connection the caller already holds and never asks the
// pool for a second one mid transaction.
func (r *Repository) AllocationPolicy(ctx context.Context, tx pgx.Tx, branchID string) (AllocationPolicy, bool, error) {
	if r.pool == nil && tx == nil {
		return AllocationPolicy{}, false, errNoDB
	}
	var p AllocationPolicy
	err := r.q(tx).QueryRow(ctx, `
		SELECT branch_id::text, order_mode, quote_mode, order_ttl_hours, quote_ttl_hours
		  FROM branch_allocation_policy WHERE branch_id = $1`, branchID).
		Scan(&p.BranchID, &p.OrderMode, &p.QuoteMode, &p.OrderTTLHours, &p.QuoteTTLHours)
	if errors.Is(err, pgx.ErrNoRows) {
		return AllocationPolicy{}, false, nil
	}
	if err != nil {
		return AllocationPolicy{}, false, err
	}
	return p, true, nil
}

// UpsertAllocationPolicy writes a branch's policy and returns what is now stored.
func (r *Repository) UpsertAllocationPolicy(ctx context.Context, p AllocationPolicy, actorID string) (AllocationPolicy, error) {
	if r.pool == nil {
		return AllocationPolicy{}, errNoDB
	}
	var out AllocationPolicy
	err := r.pool.QueryRow(ctx, `
		INSERT INTO branch_allocation_policy
			(branch_id, order_mode, quote_mode, order_ttl_hours, quote_ttl_hours, updated_by, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())
		ON CONFLICT (branch_id) DO UPDATE SET
			order_mode      = EXCLUDED.order_mode,
			quote_mode      = EXCLUDED.quote_mode,
			order_ttl_hours = EXCLUDED.order_ttl_hours,
			quote_ttl_hours = EXCLUDED.quote_ttl_hours,
			updated_by      = EXCLUDED.updated_by,
			updated_at      = now()
		RETURNING branch_id::text, order_mode, quote_mode, order_ttl_hours, quote_ttl_hours`,
		p.BranchID, p.OrderMode, p.QuoteMode, p.OrderTTLHours, p.QuoteTTLHours, nullStr(actorID)).
		Scan(&out.BranchID, &out.OrderMode, &out.QuoteMode, &out.OrderTTLHours, &out.QuoteTTLHours)
	return out, err
}

// InsertReservation writes one hold row inside the caller's tx.
func (r *Repository) InsertReservation(ctx context.Context, tx pgx.Tx, in ReserveInput, holdMode string, expires *time.Time) (Reservation, error) {
	var res Reservation
	err := r.q(tx).QueryRow(ctx, `
		INSERT INTO stock_reservations
			(product_id, branch_id, location_id, res_type, hold_mode, qty_base,
			 source_type, source_ref_id, reason, expires_at, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id::text, product_id::text, branch_id::text, location_id::text, res_type, hold_mode,
		          qty_base, status, source_type, COALESCE(source_ref_id::text, ''), reason,
		          COALESCE(to_char(expires_at, `+tsFmt+`), ''),
		          to_char(created_at, `+tsFmt+`),
		          COALESCE(to_char(closed_at, `+tsFmt+`), '')`,
		in.ProductID, in.BranchID, in.LocationID, in.ResType, holdMode, in.QtyBase,
		in.SourceType, nullStr(in.SourceRefID), in.Reason, expires, nullStr(in.CreatedBy)).
		Scan(&res.ID, &res.ProductID, &res.BranchID, &res.LocationID, &res.ResType, &res.HoldMode,
			&res.QtyBase, &res.Status, &res.SourceType, &res.SourceRefID, &res.Reason,
			&res.ExpiresAt, &res.CreatedAt, &res.ClosedAt)
	if err != nil {
		return Reservation{}, err
	}
	return res, nil
}

// AdjustReserved moves the qty_reserved projection by delta for a (product, location). A positive
// delta upserts the balance row (a hold can be the first thing that ever happened to a product at a
// location, an order confirmed before any stock arrived); a negative delta clamps at zero.
func (r *Repository) AdjustReserved(ctx context.Context, tx pgx.Tx, productID, locationID, branchID string, delta float64) error {
	if delta >= 0 {
		_, err := r.q(tx).Exec(ctx, `
			INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base, qty_reserved, updated_at)
			VALUES ($1, $2, $3, 0, $4, now())
			ON CONFLICT (product_id, location_id) DO UPDATE
			   SET qty_reserved = stock_balances.qty_reserved + EXCLUDED.qty_reserved, updated_at = now()`,
			productID, locationID, branchID, delta)
		return err
	}
	_, err := r.q(tx).Exec(ctx, `
		UPDATE stock_balances
		   SET qty_reserved = GREATEST(qty_reserved - $3, 0), updated_at = now()
		 WHERE product_id = $1 AND location_id = $2`,
		productID, locationID, -delta)
	return err
}

// BalanceFactsTx reads (on_hand, reserved) for a product at a branch inside the caller's tx, summed
// across the branch's locations. Used by the overdraft guard, so it MUST run in the tx: a guard that
// read on the pool would not see the caller's own earlier holds in the same transfer.
func (r *Repository) BalanceFactsTx(ctx context.Context, tx pgx.Tx, productID, branchID string) (float64, float64, error) {
	var onHand, reserved float64
	err := r.q(tx).QueryRow(ctx, `
		SELECT COALESCE(SUM(on_hand_base), 0), COALESCE(SUM(qty_reserved), 0)
		  FROM stock_balances WHERE product_id = $1 AND branch_id = $2`, productID, branchID).
		Scan(&onHand, &reserved)
	return onHand, reserved, err
}

// closeReservations is the shared close path: flip the matched ACTIVE rows to a terminal status and
// unwind the projection for the HARD ones.
//
// The projection unwind is a SET-BASED update joined to the rows just closed, not a Go loop issuing
// one UPDATE per reservation. Two reasons: it is one round trip for an order with forty lines, and
// it makes the close and the unwind provably operate on the SAME row set, a loop can drift if the
// caller's filter and the loop's filter are ever edited apart.
func (r *Repository) closeReservations(ctx context.Context, tx pgx.Tx, where string, status string, args ...any) (int, error) {
	statusArg := len(args) + 1
	sql := `
		WITH closed AS (
			UPDATE stock_reservations
			   SET status = $` + strconv.Itoa(statusArg) + `, closed_at = now(), updated_at = now()
			 WHERE status = 'ACTIVE' AND ` + where + `
			RETURNING id, product_id, location_id, hold_mode, qty_base
		), unwound AS (
			UPDATE stock_balances sb
			   SET qty_reserved = GREATEST(sb.qty_reserved - agg.held, 0), updated_at = now()
			  FROM (SELECT product_id, location_id, SUM(qty_base) AS held
			          FROM closed WHERE hold_mode = 'HARD'
			         GROUP BY product_id, location_id) agg
			 WHERE sb.product_id = agg.product_id AND sb.location_id = agg.location_id
			RETURNING 1
		)
		SELECT (SELECT count(*) FROM closed)::int`
	all := append(append([]any{}, args...), status)
	var n int
	if err := r.q(tx).QueryRow(ctx, sql, all...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// CloseReservationsBySource closes every ACTIVE hold raised by one source document. An empty
// sourceType matches any type for that ref id.
func (r *Repository) CloseReservationsBySource(ctx context.Context, tx pgx.Tx, sourceType, sourceRefID, status string) (int, error) {
	return r.closeReservations(ctx, tx,
		`($1 = '' OR source_type = $1) AND source_ref_id = $2::uuid`, status, sourceType, sourceRefID)
}

// LockHardHoldBalancesTx takes the stock_balances rows one source document's ACTIVE HARD holds
// will unwind, FOR UPDATE, sorted by product then location, on the caller's transaction. The order
// cancel calls it ahead of the deposit settlement that draws the invoices feed counter, because the
// release hook writes these same rows later in that one transaction and the POS ring takes a stock
// row first and that counter later: a cancel that drew the counter first could meet the ring head
// on. The sort and the strength are LockBalances' own, the one order every balance locking caller
// in this module uses, so two stock taking writers queue instead of crossing.
func (r *Repository) LockHardHoldBalancesTx(ctx context.Context, tx pgx.Tx, sourceType, sourceRefID string) error {
	rows, err := r.q(tx).Query(ctx, `
		SELECT 1
		  FROM stock_reservations sr
		  JOIN stock_balances sb ON sb.product_id = sr.product_id AND sb.location_id = sr.location_id
		 WHERE sr.status = 'ACTIVE' AND sr.hold_mode = 'HARD'
		   AND ($1 = '' OR sr.source_type = $1) AND sr.source_ref_id = $2::uuid
		 ORDER BY sb.product_id, sb.location_id
		   FOR UPDATE OF sb`, sourceType, sourceRefID)
	if err != nil {
		return err
	}
	rows.Close()
	return rows.Err()
}

// CloseReservationByID closes one hold. Returns 0 when the id is unknown or already closed.
func (r *Repository) CloseReservationByID(ctx context.Context, tx pgx.Tx, reservationID, status string) (int, error) {
	return r.closeReservations(ctx, tx, `id = $1::uuid`, status, reservationID)
}

// ExpireDueReservations flips every ACTIVE hold past its expires_at to EXPIRED.
func (r *Repository) ExpireDueReservations(ctx context.Context, tx pgx.Tx) (int, error) {
	return r.closeReservations(ctx, tx, `expires_at IS NOT NULL AND expires_at <= now()`, ResExpired)
}

// ListReservations returns the ACTIVE holds at a branch (optionally one product), newest first.
func (r *Repository) ListReservations(ctx context.Context, branchID, productID string, limit int) ([]Reservation, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, product_id::text, branch_id::text, location_id::text, res_type, hold_mode,
		       qty_base, status, source_type, COALESCE(source_ref_id::text, ''), reason,
		       COALESCE(to_char(expires_at, `+tsFmt+`), ''),
		       to_char(created_at, `+tsFmt+`),
		       COALESCE(to_char(closed_at, `+tsFmt+`), '')
		  FROM stock_reservations
		 WHERE status = 'ACTIVE'
		   AND ($1 = '' OR branch_id = $1::uuid)
		   AND ($2 = '' OR product_id = $2::uuid)
		 ORDER BY created_at DESC
		 LIMIT $3`, branchID, productID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Reservation
	for rows.Next() {
		var res Reservation
		if err := rows.Scan(&res.ID, &res.ProductID, &res.BranchID, &res.LocationID, &res.ResType,
			&res.HoldMode, &res.QtyBase, &res.Status, &res.SourceType, &res.SourceRefID, &res.Reason,
			&res.ExpiresAt, &res.CreatedAt, &res.ClosedAt); err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, rows.Err()
}
