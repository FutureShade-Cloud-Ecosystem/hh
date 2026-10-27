package invcount

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var errNoDB = errors.New("invcount: database not configured")

// nullStr maps an empty string to a SQL NULL (used for optional uuid/text columns).
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// isInvalidUUID reports whether err is a Postgres invalid-text-representation (22P02), raised when a
// malformed non-UUID sheet id reaches `WHERE id = $1` on a uuid column. Folding it into the not-found
// branch keeps a garbage id indistinguishable from a missing one (404, IDOR-safe) instead of 500.
func isInvalidUUID(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "22P02"
}

// sheetCols is the canonical count_sheets projection (kept in one place so pool + tx reads agree).
const sheetCols = `id::text, sheet_no, branch_id::text, scope, status,
	COALESCE(freeze_at::text,''), COALESCE(counted_by::text,''), COALESCE(approved_by::text,''),
	COALESCE(approved_at::text,''), COALESCE(posted_at::text,''), notes, COALESCE(created_at::text,'')`

func scanSheet(row pgx.Row) (Sheet, error) {
	var s Sheet
	err := row.Scan(&s.ID, &s.SheetNo, &s.BranchID, &s.Scope, &s.Status,
		&s.FreezeAt, &s.CountedBy, &s.ApprovedBy, &s.ApprovedAt, &s.PostedAt, &s.Notes, &s.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
		return Sheet{}, errNotFound
	}
	if err != nil {
		return Sheet{}, fmt.Errorf("invcount: scan sheet: %w", err)
	}
	return s, nil
}

// - sheet reads -

func (r *Repository) GetSheet(ctx context.Context, sheetID string) (Sheet, error) {
	if r.pool == nil {
		return Sheet{}, errNoDB
	}
	return scanSheet(r.pool.QueryRow(ctx, `SELECT `+sheetCols+` FROM count_sheets WHERE id = $1`, sheetID))
}

func (r *Repository) ListSheets(ctx context.Context, branchID, status string) ([]Sheet, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `SELECT `+sheetCols+` FROM count_sheets
		WHERE ($1 = '' OR branch_id = $1::uuid) AND ($2 = '' OR status = $2)
		ORDER BY created_at DESC`, branchID, status)
	if err != nil {
		return nil, fmt.Errorf("invcount: list sheets: %w", err)
	}
	defer rows.Close()
	var out []Sheet
	for rows.Next() {
		s, err := scanSheet(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

const lineCols = `cl.id::text, cl.sheet_id::text, cl.product_id::text, COALESCE(p.sku,''), COALESCE(p.name,''),
	COALESCE(cl.location_id::text,''), cl.snapshot_qty_base::float8, cl.counted_qty_base::float8,
	cl.variance_base::float8, cl.unit_cost_cents, cl.variance_value_cents,
	COALESCE(cl.reason_code::text,''), COALESCE(cl.move_id::text,'')`

func scanLine(row pgx.Row) (Line, error) {
	var l Line
	var counted *float64
	err := row.Scan(&l.ID, &l.SheetID, &l.ProductID, &l.SKU, &l.ProductName,
		&l.LocationID, &l.SnapshotQtyBase, &counted, &l.VarianceBase,
		&l.UnitCostCents, &l.VarianceValueCents, &l.ReasonCode, &l.MoveID)
	if err != nil {
		return Line{}, fmt.Errorf("invcount: scan line: %w", err)
	}
	l.CountedQtyBase = counted
	return l, nil
}

func (r *Repository) ListLines(ctx context.Context, sheetID string) ([]Line, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `SELECT `+lineCols+` FROM count_lines cl
		LEFT JOIN products p ON p.id = cl.product_id
		WHERE cl.sheet_id = $1 ORDER BY p.sku`, sheetID)
	if err != nil {
		return nil, fmt.Errorf("invcount: list lines: %w", err)
	}
	defer rows.Close()
	var out []Line
	for rows.Next() {
		l, err := scanLine(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (r *Repository) ReasonCodes(ctx context.Context) ([]ReasonCode, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `SELECT code::text, description, account_code::text, is_active
		FROM count_reason_codes ORDER BY code`)
	if err != nil {
		return nil, fmt.Errorf("invcount: reason codes: %w", err)
	}
	defer rows.Close()
	var out []ReasonCode
	for rows.Next() {
		var rc ReasonCode
		if err := rows.Scan(&rc.Code, &rc.Description, &rc.AccountCode, &rc.IsActive); err != nil {
			return nil, fmt.Errorf("invcount: scan reason: %w", err)
		}
		out = append(out, rc)
	}
	return out, rows.Err()
}

// - sheet lifecycle writes -

func (r *Repository) CreateSheet(ctx context.Context, branchID, scope, notes string) (Sheet, error) {
	if r.pool == nil {
		return Sheet{}, errNoDB
	}
	// The cycle_counts scope's seq stamps beside the row (0213,
	// syncscope.go), so a sheet the desk opens is visible to a
	// device's pull the moment it lands.
	return scanSheet(r.pool.QueryRow(ctx, cycleSeqCTE+`INSERT INTO count_sheets (sheet_no, branch_id, scope, notes, seq)
		VALUES ('CS-' || to_char(now(),'YYYYMMDD') || '-' || substr(gen_random_uuid()::text,1,8), $1, $2, $3,
		        (SELECT next_value FROM cseq))
		RETURNING `+sheetCols, branchID, scope, notes))
}

// FreezeSheet captures the snapshot on-hand basis onto count_lines and flips the sheet FROZEN, in one tx.
func (r *Repository) FreezeSheet(ctx context.Context, sheetID string, snaps []Snapshot) error {
	if r.pool == nil {
		return errNoDB
	}
	return r.InTx(ctx, func(tx pgx.Tx) error {
		for _, sn := range snaps {
			if _, err := tx.Exec(ctx, `INSERT INTO count_lines
				(sheet_id, product_id, location_id, snapshot_qty_base, unit_cost_cents)
				VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT (sheet_id, product_id, location_id) DO NOTHING`,
				sheetID, sn.ProductID, nullStr(sn.LocationID), sn.OnHandBase, sn.AvgCostCents); err != nil {
				return fmt.Errorf("invcount: insert snapshot line: %w", err)
			}
		}
		ct, err := tx.Exec(ctx, `UPDATE count_sheets SET status = 'FROZEN', freeze_at = now(), updated_at = now()
			WHERE id = $1 AND status = 'OPEN'`, sheetID)
		if err != nil {
			return fmt.Errorf("invcount: freeze sheet: %w", err)
		}
		if ct.RowsAffected() == 0 {
			return errNotFound
		}
		return nil
	})
}

// SaveCounts upserts the maker's counted quantities + reasons, derives each variance, and flips the sheet
// COUNTED with the maker stamped as counted_by, in one tx.
func (r *Repository) SaveCounts(ctx context.Context, sheetID, countedBy string, lines []CountLineInput) error {
	if r.pool == nil {
		return errNoDB
	}
	return r.InTx(ctx, func(tx pgx.Tx) error {
		for _, ln := range lines {
			if _, err := tx.Exec(ctx, `INSERT INTO count_lines
				(sheet_id, product_id, location_id, snapshot_qty_base, counted_qty_base, variance_base, reason_code)
				VALUES ($1, $2, $3, 0, $4, $4, $5)
				ON CONFLICT (sheet_id, product_id, location_id) DO UPDATE
				SET counted_qty_base = EXCLUDED.counted_qty_base,
				    variance_base = EXCLUDED.counted_qty_base - count_lines.snapshot_qty_base,
				    reason_code = EXCLUDED.reason_code`,
				sheetID, ln.ProductID, nullStr(ln.LocationID), ln.CountedQtyBase, nullStr(ln.ReasonCode)); err != nil {
				return fmt.Errorf("invcount: save count line: %w", err)
			}
		}
		// Re-saving counts INVALIDATES any prior approval: a sheet that was already approved (still COUNTED,
		// approved_by stamped) must not carry that stamp onto edited numbers, or a maker could revise quantities
		// after the checker signed off and post under a stale approval (S3 W6 B5, maker-checker bypass). Null the
		// approval on every save so the checker must re-approve the numbers that will actually post.
		ct, err := tx.Exec(ctx, `UPDATE count_sheets
			SET status = 'COUNTED', counted_by = $2, approved_by = NULL, approved_at = NULL, updated_at = now()
			WHERE id = $1 AND status IN ('FROZEN','COUNTED')`, sheetID, countedBy)
		if err != nil {
			return fmt.Errorf("invcount: mark counted: %w", err)
		}
		if ct.RowsAffected() == 0 {
			return errNotFrozen
		}
		return nil
	})
}

func (r *Repository) ApproveSheet(ctx context.Context, sheetID, approvedBy string) error {
	if r.pool == nil {
		return errNoDB
	}
	ct, err := r.pool.Exec(ctx, `UPDATE count_sheets SET approved_by = $2, approved_at = now(), updated_at = now()
		WHERE id = $1 AND status = 'COUNTED'`, sheetID, approvedBy)
	if err != nil {
		return fmt.Errorf("invcount: approve sheet: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return errSheetNotCounted
	}
	return nil
}

func (r *Repository) CancelSheet(ctx context.Context, sheetID string) error {
	if r.pool == nil {
		return errNoDB
	}
	ct, err := r.pool.Exec(ctx, `UPDATE count_sheets SET status = 'CANCELLED', updated_at = now()
		WHERE id = $1 AND status <> 'POSTED'`, sheetID)
	if err != nil {
		return fmt.Errorf("invcount: cancel sheet: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return errNotFound
	}
	return nil
}

// - post (in caller's tx) -

func (r *Repository) InTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	if r.pool == nil {
		return errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("invcount: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) LockSheetTx(ctx context.Context, tx pgx.Tx, sheetID string) (Sheet, error) {
	return scanSheet(tx.QueryRow(ctx, `SELECT `+sheetCols+` FROM count_sheets WHERE id = $1 FOR UPDATE`, sheetID))
}

func (r *Repository) LinesForPostTx(ctx context.Context, tx pgx.Tx, sheetID string) ([]Line, error) {
	rows, err := tx.Query(ctx, `SELECT cl.id::text, cl.sheet_id::text, cl.product_id::text, '', '',
		COALESCE(cl.location_id::text,''), cl.snapshot_qty_base::float8, cl.counted_qty_base::float8,
		cl.variance_base::float8, cl.unit_cost_cents, cl.variance_value_cents,
		COALESCE(cl.reason_code::text,''), COALESCE(cl.move_id::text,'')
		FROM count_lines cl WHERE cl.sheet_id = $1`, sheetID)
	if err != nil {
		return nil, fmt.Errorf("invcount: lines for post: %w", err)
	}
	defer rows.Close()
	var out []Line
	for rows.Next() {
		l, err := scanLine(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (r *Repository) UpdateLinePostedTx(ctx context.Context, tx pgx.Tx, lineID string, unitCostCents, varianceValueCents int64, moveID string) error {
	_, err := tx.Exec(ctx, `UPDATE count_lines
		SET unit_cost_cents = $2, variance_value_cents = $3, move_id = $4 WHERE id = $1`,
		lineID, unitCostCents, varianceValueCents, nullStr(moveID))
	if err != nil {
		return fmt.Errorf("invcount: update posted line: %w", err)
	}
	return nil
}

func (r *Repository) MarkSheetPostedTx(ctx context.Context, tx pgx.Tx, sheetID string) error {
	_, err := tx.Exec(ctx, `UPDATE count_sheets SET status = 'POSTED', posted_at = now(), updated_at = now()
		WHERE id = $1`, sheetID)
	if err != nil {
		return fmt.Errorf("invcount: mark posted: %w", err)
	}
	return nil
}

// - recon -

const reconCols = `id::text, COALESCE(branch_id::text,''), fiscal_year, month,
	subledger_cents, gl_1200_cents, delta_cents, status, COALESCE(run_by::text,''), COALESCE(created_at::text,'')`

func scanRecon(row pgx.Row) (ReconRun, error) {
	var rr ReconRun
	err := row.Scan(&rr.ID, &rr.BranchID, &rr.FiscalYear, &rr.Month,
		&rr.SubledgerCents, &rr.GL1200Cents, &rr.DeltaCents, &rr.Status, &rr.RunBy, &rr.CreatedAt)
	return rr, err
}

func (r *Repository) GetReconRun(ctx context.Context, year, month int, branchID string) (ReconRun, bool, error) {
	if r.pool == nil {
		return ReconRun{}, false, errNoDB
	}
	q := `SELECT ` + reconCols + ` FROM inventory_recon_runs
		WHERE fiscal_year = $1 AND month = $2 AND branch_id IS NULL`
	args := []any{year, month}
	if branchID != "" {
		q = `SELECT ` + reconCols + ` FROM inventory_recon_runs
			WHERE fiscal_year = $1 AND month = $2 AND branch_id = $3::uuid`
		args = append(args, branchID)
	}
	rr, err := scanRecon(r.pool.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return ReconRun{}, false, nil
	}
	if err != nil {
		return ReconRun{}, false, fmt.Errorf("invcount: get recon: %w", err)
	}
	return rr, true, nil
}

// InsertReconRun inserts the period run; on a unique-index collision (a concurrent run-once winner) it returns
// the existing row instead, the lazy run-once convergence.
func (r *Repository) InsertReconRun(ctx context.Context, in ReconRun) (ReconRun, error) {
	if r.pool == nil {
		return ReconRun{}, errNoDB
	}
	// FW1-11: a period re-runs. The row is UPSERTED, so a correction in the month is never hidden
	// behind the first run's frozen figures: the latest run stands, one row per period and branch.
	// The table's unique rules are two PARTIAL indexes (a NULL branch row and one row per named
	// branch), so the conflict target carries the index's own predicate, one arm per shape.
	const upsert = `INSERT INTO inventory_recon_runs
		(branch_id, fiscal_year, month, subledger_cents, gl_1200_cents, delta_cents, status, run_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT %s DO UPDATE
		SET subledger_cents = EXCLUDED.subledger_cents, gl_1200_cents = EXCLUDED.gl_1200_cents,
		    delta_cents = EXCLUDED.delta_cents, status = EXCLUDED.status, run_by = EXCLUDED.run_by,
		    created_at = now()
		RETURNING ` + reconCols
	conflictTarget := `(fiscal_year, month, branch_id) WHERE branch_id IS NOT NULL`
	if in.BranchID == "" {
		conflictTarget = `(fiscal_year, month) WHERE branch_id IS NULL`
	}
	rr, err := scanRecon(r.pool.QueryRow(ctx,
		fmt.Sprintf(upsert, conflictTarget),
		nullStr(in.BranchID), in.FiscalYear, in.Month, in.SubledgerCents, in.GL1200Cents, in.DeltaCents, in.Status, nullStr(in.RunBy)))
	if err != nil {
		if isUniqueViolation(err) {
			existing, _, gerr := r.GetReconRun(ctx, in.FiscalYear, in.Month, in.BranchID)
			if gerr != nil {
				return ReconRun{}, gerr
			}
			return existing, nil
		}
		return ReconRun{}, fmt.Errorf("invcount: insert recon: %w", err)
	}
	return rr, nil
}

func (r *Repository) ListReconRuns(ctx context.Context, branchID string) ([]ReconRun, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	q := `SELECT ` + reconCols + ` FROM inventory_recon_runs WHERE branch_id IS NULL
		ORDER BY fiscal_year DESC, month DESC`
	args := []any{}
	if branchID != "" {
		q = `SELECT ` + reconCols + ` FROM inventory_recon_runs WHERE branch_id = $1::uuid
			ORDER BY fiscal_year DESC, month DESC`
		args = append(args, branchID)
	}
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("invcount: list recon: %w", err)
	}
	defer rows.Close()
	var out []ReconRun
	for rows.Next() {
		rr, err := scanRecon(rows)
		if err != nil {
			return nil, fmt.Errorf("invcount: scan recon: %w", err)
		}
		out = append(out, rr)
	}
	return out, rows.Err()
}
