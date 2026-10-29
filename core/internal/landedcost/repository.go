package landedcost

// repository.go, the pgx access layer over the 0041 landed_costs + landed_cost_allocations tables. It
// reads/writes ONLY landedcost-owned tables (no cross-module table access, the received PO lines + the
// weighted-average capitalization come through the frozen Capitalizer seam). A nil pool is tolerated
// (scaffold/offline mode → errNoDB). The apply/retro/void writes ride inside a single tx (WithinTx) so the
// allocation rows, the inventory capitalization seam call, and the status flip commit atomically.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// isInvalidUUID reports whether err is a Postgres invalid-text-representation (22P02), raised when a
// malformed non-UUID id reaches `WHERE id = $1` on a uuid column. Folding it into scanLanded's
// not-found branch keeps a garbage id indistinguishable from a missing one (404, IDOR-safe, matching
// guardBranch's no-existence-leak contract) instead of an opaque HTTP 500.
func isInvalidUUID(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "22P02"
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

const landedCols = `id::text, COALESCE(po_id::text, ''), po_ref, COALESCE(payee_vendor_id::text, ''), branch_id::text, currency, fx_rate::float8,
	freight_cents, duty_cents, handling_cents, freight_cad_cents, duty_cad_cents, handling_cad_cents,
	allocation_basis, exclude_special_buys, status, settled_cad_cents, COALESCE(settled_at, '0001-01-01'::timestamptz),
	COALESCE(retro_of_id::text, ''),
	COALESCE(gl_journal_id::text, ''), created_at`

func scanLanded(row pgx.Row) (LandedCost, error) {
	var lc LandedCost
	err := row.Scan(&lc.ID, &lc.POID, &lc.PORef, &lc.PayeeVendorID, &lc.BranchID, &lc.Currency, &lc.FXRate,
		&lc.FreightCents, &lc.DutyCents, &lc.HandlingCents, &lc.FreightCadCents, &lc.DutyCadCents,
		&lc.HandlingCadCents, &lc.AllocationBasis, &lc.ExcludeSpecialBuys, &lc.Status, &lc.SettledCadCents, &lc.SettledAt,
		&lc.RetroOfID,
		&lc.GLJournalID, &lc.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
		return LandedCost{}, errNotFound
	}
	if err != nil {
		return LandedCost{}, fmt.Errorf("landedcost: scan landed cost: %w", err)
	}
	return lc, nil
}

// Insert writes the landed_costs header (CAD amounts precomputed) and returns its id. A payee_vendor_id
// that names no vendor is refused as the caller's clean client error: a malformed id is a bad request
// and an unknown vendor is the FK's own refusal, never an opaque 500.
func (r *Repository) Insert(ctx context.Context, lc LandedCost) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	const q = `
		INSERT INTO landed_costs
		  (po_id, po_ref, payee_vendor_id, branch_id, currency, fx_rate, freight_cents, duty_cents, handling_cents,
		   freight_cad_cents, duty_cad_cents, handling_cad_cents, allocation_basis, exclude_special_buys,
		   status, retro_of_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		RETURNING id::text`
	var id string
	err := r.pool.QueryRow(ctx, q,
		nullStr(lc.POID), lc.PORef, nullStr(lc.PayeeVendorID), lc.BranchID, lc.Currency, lc.FXRate,
		lc.FreightCents, lc.DutyCents, lc.HandlingCents,
		lc.FreightCadCents, lc.DutyCadCents, lc.HandlingCadCents,
		lc.AllocationBasis, lc.ExcludeSpecialBuys, lc.Status, nullStr(lc.RetroOfID),
	).Scan(&id)
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) {
			switch pg.Code {
			case "22P02":
				return "", clientErr(http.StatusBadRequest, "landedcost: payee_vendor_id must be a vendor id")
			case "23503":
				return "", clientErr(http.StatusBadRequest, "landedcost: payee_vendor_id does not name a vendor")
			}
		}
		return "", fmt.Errorf("landedcost: insert: %w", err)
	}
	return id, nil
}

// Get returns one landed_costs header by id.
func (r *Repository) Get(ctx context.Context, id string) (LandedCost, error) {
	if r.pool == nil {
		return LandedCost{}, errNoDB
	}
	return scanLanded(r.pool.QueryRow(ctx, `SELECT `+landedCols+` FROM landed_costs WHERE id = $1`, id))
}

// List returns landed_costs filtered by branch ("" = all) and status ("" = all), newest first.
func (r *Repository) List(ctx context.Context, branchID, status string) ([]LandedCost, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	q := `SELECT ` + landedCols + ` FROM landed_costs WHERE 1=1`
	var args []any
	if branchID != "" {
		args = append(args, branchID)
		q += fmt.Sprintf(" AND branch_id = $%d", len(args))
	}
	if status != "" {
		args = append(args, status)
		q += fmt.Sprintf(" AND status = $%d", len(args))
	}
	q += ` ORDER BY created_at DESC`
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("landedcost: list: %w", err)
	}
	defer rows.Close()
	var out []LandedCost
	for rows.Next() {
		lc, err := scanLanded(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, lc)
	}
	return out, rows.Err()
}

// Allocations returns the persisted allocation rows for a landed cost, in insertion order.
func (r *Repository) Allocations(ctx context.Context, landedCostID string) ([]Allocation, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT COALESCE(po_line_id::text, ''), COALESCE(product_id::text, ''), basis_weight::float8,
		        allocated_cad_cents, sold_through_cad_cents, pre_apply_avg_unit_cost
		 FROM landed_cost_allocations WHERE landed_cost_id = $1 ORDER BY created_at, id`, landedCostID)
	if err != nil {
		return nil, fmt.Errorf("landedcost: allocations: %w", err)
	}
	defer rows.Close()
	return scanAllocations(rows)
}

// scanAllocations reads allocation rows, decoding the nullable pre_apply_avg_unit_cost into (PreApplyAvgUnitCost,
// HasPreApplyAvg) so Void can restore the exact snapshot when present and fall back to the legacy negate path when
// NULL (rows written before 0058 / seed / retro rows).
func scanAllocations(rows pgx.Rows) ([]Allocation, error) {
	var out []Allocation
	for rows.Next() {
		var a Allocation
		var pre *int64
		if err := rows.Scan(&a.POLineID, &a.ProductID, &a.BasisWeight, &a.AllocatedCadCents, &a.SoldThroughCadCents, &pre); err != nil {
			return nil, fmt.Errorf("landedcost: scan allocation: %w", err)
		}
		if pre != nil {
			a.PreApplyAvgUnitCost = *pre
			a.HasPreApplyAvg = true
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AllocationsTx reads the allocation rows inside the caller's tx (the FOR UPDATE lock already held on the
// header serializes it). apply/retro use it as the capitalize-once marker: a crashed attempt that already
// capitalized leaves committed rows, so a replay recomputes the split totals from them instead of
// double-capitalizing (the header status is only advanced after the GL journal posts).
func (r *Repository) AllocationsTx(ctx context.Context, tx pgx.Tx, landedCostID string) ([]Allocation, error) {
	rows, err := tx.Query(ctx,
		`SELECT COALESCE(po_line_id::text, ''), COALESCE(product_id::text, ''), basis_weight::float8,
		        allocated_cad_cents, sold_through_cad_cents, pre_apply_avg_unit_cost
		 FROM landed_cost_allocations WHERE landed_cost_id = $1 ORDER BY created_at, id`, landedCostID)
	if err != nil {
		return nil, fmt.Errorf("landedcost: allocations (tx): %w", err)
	}
	defer rows.Close()
	return scanAllocations(rows)
}

// RetroOnHand reads the still-on-hand base captured at retro-doc create time, keyed by product id. The seed
// rows carry the on-hand base in basis_weight with allocated_cad_cents = 0 (RetroAdjust overwrites them with
// the real split). An empty map means "capture nothing on hand" → the whole amount capitalizes.
func (r *Repository) RetroOnHand(ctx context.Context, landedCostID string) (map[string]float64, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT COALESCE(product_id::text, ''), basis_weight::float8
		 FROM landed_cost_allocations WHERE landed_cost_id = $1`, landedCostID)
	if err != nil {
		return nil, fmt.Errorf("landedcost: retro on-hand: %w", err)
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var pid string
		var onHand float64
		if err := rows.Scan(&pid, &onHand); err != nil {
			return nil, fmt.Errorf("landedcost: scan retro on-hand: %w", err)
		}
		out[pid] = onHand
	}
	return out, rows.Err()
}

// SeedRetroOnHand pre-inserts the per-product on-hand base rows for a retro doc (basis_weight carries the
// on-hand base; allocated = 0 until RetroAdjust). Called at Create when retro_of_id + on_hand are supplied.
func (r *Repository) SeedRetroOnHand(ctx context.Context, landedCostID string, lines []OnHandLine) error {
	if r.pool == nil {
		return errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("landedcost: begin seed: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, l := range lines {
		if _, err := tx.Exec(ctx,
			`INSERT INTO landed_cost_allocations
			   (landed_cost_id, po_line_id, product_id, basis_weight, allocated_cad_cents, sold_through_cad_cents)
			 VALUES ($1, $2, $3, $4, 0, 0)`,
			landedCostID, nullStr(l.POLineID), nullStr(l.ProductID), l.OnHandBase); err != nil {
			return fmt.Errorf("landedcost: seed retro on-hand: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// - tx-scoped writes (apply / retro / void ride one tx with the capitalization seam) -

// WithinTx runs fn inside a single transaction, committing on success and rolling back on error.
func (r *Repository) WithinTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	if r.pool == nil {
		return errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("landedcost: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// LockForUpdateTx re-reads the header FOR UPDATE inside the tx (the status guard that makes apply/retro/void
// idempotent under concurrency).
func (r *Repository) LockForUpdateTx(ctx context.Context, tx pgx.Tx, id string) (LandedCost, error) {
	return scanLanded(tx.QueryRow(ctx, `SELECT `+landedCols+` FROM landed_costs WHERE id = $1 FOR UPDATE`, id))
}

// ClearAllocationsTx removes any existing allocation rows (the retro seed rows) before writing the real split.
func (r *Repository) ClearAllocationsTx(ctx context.Context, tx pgx.Tx, landedCostID string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM landed_cost_allocations WHERE landed_cost_id = $1`, landedCostID); err != nil {
		return fmt.Errorf("landedcost: clear allocations: %w", err)
	}
	return nil
}

// InsertAllocationTx writes one allocation row inside the tx.
func (r *Repository) InsertAllocationTx(ctx context.Context, tx pgx.Tx, landedCostID string, a Allocation) error {
	// pre_apply_avg_unit_cost is written only when Apply captured a snapshot (HasPreApplyAvg); otherwise NULL, so a
	// Void of this row falls back to the legacy negate path and stays voidable.
	var pre any
	if a.HasPreApplyAvg {
		pre = a.PreApplyAvgUnitCost
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO landed_cost_allocations
		   (landed_cost_id, po_line_id, product_id, basis_weight, allocated_cad_cents, sold_through_cad_cents,
		    pre_apply_avg_unit_cost)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		landedCostID, nullStr(a.POLineID), nullStr(a.ProductID), a.BasisWeight, a.AllocatedCadCents, a.SoldThroughCadCents, pre); err != nil {
		return fmt.Errorf("landedcost: insert allocation: %w", err)
	}
	return nil
}

// SettleTx stamps the payable's settlement inside the tx: the CAD amount paid
// and the instant it was paid (0455). The caller has already re-read the row
// FOR UPDATE and seen it unsettled.
func (r *Repository) SettleTx(ctx context.Context, tx pgx.Tx, id string, settledCadCents int64, at time.Time) error {
	if _, err := tx.Exec(ctx,
		`UPDATE landed_costs SET settled_cad_cents = $2, settled_at = $3, updated_at = now()
		 WHERE id = $1`, id, settledCadCents, at); err != nil {
		return fmt.Errorf("landedcost: settle: %w", err)
	}
	return nil
}

// SetStatusTx flips the header status (and stamps the gl journal id when supplied) inside the tx.
func (r *Repository) SetStatusTx(ctx context.Context, tx pgx.Tx, id, status, glJournalID string) error {
	if _, err := tx.Exec(ctx,
		`UPDATE landed_costs SET status = $2, gl_journal_id = COALESCE($3, gl_journal_id), updated_at = now()
		 WHERE id = $1`, id, status, nullStr(glJournalID)); err != nil {
		return fmt.Errorf("landedcost: set status: %w", err)
	}
	return nil
}
