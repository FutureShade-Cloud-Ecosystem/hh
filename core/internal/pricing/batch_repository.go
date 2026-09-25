package pricing

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// A-08 pgx implementation of batchStore. Follows the house patterns in repository.go:
// r.pool nil → errNoDB, pgx.ErrNoRows → errNotFound / nil, ::text casts on uuids.

func derefTimePtr(p *time.Time) any {
	if p == nil {
		return nil
	}
	return *p
}

func (r *Repository) AnchorPolicy(ctx context.Context, branchID string) (AnchorPolicy, error) {
	if r.pool == nil {
		return AnchorPolicy{}, errNoDB
	}
	// branchID "" → the org default (branch_id IS NULL). A per-location row wins when present.
	var (
		q    string
		args []any
	)
	if branchID == "" {
		q = `SELECT COALESCE(branch_id::text,''), markup_bps, fob_low_cents, fob_high_cents, variance_alert_bps
		       FROM pricing_anchor_policy WHERE branch_id IS NULL LIMIT 1`
	} else {
		q = `SELECT COALESCE(branch_id::text,''), markup_bps, fob_low_cents, fob_high_cents, variance_alert_bps
		       FROM pricing_anchor_policy
		      WHERE branch_id = $1 OR branch_id IS NULL
		      ORDER BY branch_id NULLS LAST LIMIT 1`
		args = append(args, branchID)
	}
	var p AnchorPolicy
	err := r.pool.QueryRow(ctx, q, args...).Scan(&p.BranchID, &p.MarkupBps, &p.FOBLowCents, &p.FOBHighCents, &p.VarianceAlertBps)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// A sane placeholder default so import never hard-fails on a missing config row.
			return AnchorPolicy{MarkupBps: 18500, FOBLowCents: 40, FOBHighCents: 50, VarianceAlertBps: 1500}, nil
		}
		return AnchorPolicy{}, err
	}
	return p, nil
}

func (r *Repository) ListImportProfiles(ctx context.Context) ([]ImportProfile, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT p.id::text, p.vendor_id::text, v.name, p.name, p.mapping, p.markup_bps, p.is_active
		   FROM price_import_profiles p JOIN vendors v ON v.id = p.vendor_id
		  ORDER BY v.name, p.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ImportProfile
	for rows.Next() {
		var (
			p       ImportProfile
			mapping []byte
		)
		if err := rows.Scan(&p.ID, &p.VendorID, &p.VendorName, &p.Name, &mapping, &p.MarkupBps, &p.IsActive); err != nil {
			return nil, err
		}
		p.Mapping = map[string]string{}
		if len(mapping) > 0 {
			_ = json.Unmarshal(mapping, &p.Mapping)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repository) UpdateImportProfileMapping(ctx context.Context, id string, mapping map[string]string) (ImportProfile, error) {
	if r.pool == nil {
		return ImportProfile{}, errNoDB
	}
	raw, err := json.Marshal(mapping)
	if err != nil {
		return ImportProfile{}, err
	}
	if _, err := r.pool.Exec(ctx,
		`UPDATE price_import_profiles SET mapping = $2, updated_at = now() WHERE id = $1`, id, raw); err != nil {
		return ImportProfile{}, err
	}
	return r.GetImportProfile(ctx, id)
}

func (r *Repository) GetImportProfile(ctx context.Context, id string) (ImportProfile, error) {
	if r.pool == nil {
		return ImportProfile{}, errNoDB
	}
	var (
		p       ImportProfile
		mapping []byte
	)
	err := r.pool.QueryRow(ctx,
		`SELECT p.id::text, p.vendor_id::text, v.name, p.name, p.mapping, p.markup_bps, p.is_active
		   FROM price_import_profiles p JOIN vendors v ON v.id = p.vendor_id
		  WHERE p.id = $1`, id).
		Scan(&p.ID, &p.VendorID, &p.VendorName, &p.Name, &mapping, &p.MarkupBps, &p.IsActive)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ImportProfile{}, errNoProfile
		}
		return ImportProfile{}, err
	}
	p.Mapping = map[string]string{}
	if len(mapping) > 0 {
		_ = json.Unmarshal(mapping, &p.Mapping) // seed values are all strings; a bad map degrades to defaults
	}
	return p, nil
}

func (r *Repository) ProductBySKU(ctx context.Context, sku string) (ProductRef, error) {
	if r.pool == nil {
		return ProductRef{}, errNoDB
	}
	var ref ProductRef
	err := r.pool.QueryRow(ctx,
		`SELECT id::text, sku::text, price_uom::text FROM products WHERE sku = $1`, sku).
		Scan(&ref.ID, &ref.SKU, &ref.PriceUOM)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProductRef{}, errNotFound
		}
		return ProductRef{}, err
	}
	return ref, nil
}

func (r *Repository) CurrentListPrice(ctx context.Context, productID string) (*int64, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	var cents int64
	err := r.pool.QueryRow(ctx,
		`SELECT list_price_cents FROM pricing_list_prices WHERE product_id = $1`, productID).Scan(&cents)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &cents, nil
}

const batchCols = `b.id::text, b.batch_no::text, COALESCE(b.vendor_id::text,''), COALESCE(v.name,''),
	COALESCE(b.profile_id::text,''), b.status, b.effective_date, b.source_ref, b.line_count, b.outlier_count,
	COALESCE(b.created_by::text,''), COALESCE(b.approved_by::text,''), b.approved_at, b.applied_at, b.created_at`

func scanBatch(row pgx.Row) (PriceBatch, error) {
	var (
		b   PriceBatch
		eff *time.Time
		apv *time.Time
		apl *time.Time
	)
	err := row.Scan(&b.ID, &b.BatchNo, &b.VendorID, &b.VendorName, &b.ProfileID, &b.Status, &eff,
		&b.SourceRef, &b.LineCount, &b.OutlierCount, &b.CreatedBy, &b.ApprovedBy, &apv, &apl, &b.CreatedAt)
	if err != nil {
		return PriceBatch{}, err
	}
	b.EffectiveDate, b.ApprovedAt, b.AppliedAt = eff, apv, apl
	return b, nil
}

func (r *Repository) CreateBatch(ctx context.Context, b PriceBatch) (PriceBatch, error) {
	if r.pool == nil {
		return PriceBatch{}, errNoDB
	}
	row := r.pool.QueryRow(ctx,
		`INSERT INTO price_batches (batch_no, vendor_id, profile_id, status, effective_date, source_ref, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING `+returningBatchCols(),
		b.BatchNo, nullStr(b.VendorID), nullStr(b.ProfileID), b.Status, derefTimePtr(b.EffectiveDate),
		b.SourceRef, nullStr(b.CreatedBy))
	out, err := scanBatchReturning(row)
	if err != nil {
		if isUniqueViolation(err) {
			return PriceBatch{}, badReq("batch_no %q already exists", b.BatchNo)
		}
		if isFKViolation(err) {
			return PriceBatch{}, errFKViolation
		}
		return PriceBatch{}, err
	}
	return out, nil
}

// returningBatchCols / scanBatchReturning avoid the vendors JOIN on INSERT/UPDATE RETURNING
// (no alias available), then vendor_name is filled lazily on read paths.
func returningBatchCols() string {
	return `id::text, batch_no::text, COALESCE(vendor_id::text,''), COALESCE(profile_id::text,''), status,
	 effective_date, source_ref, line_count, outlier_count, COALESCE(created_by::text,''),
	 COALESCE(approved_by::text,''), approved_at, applied_at, created_at`
}

func scanBatchReturning(row pgx.Row) (PriceBatch, error) {
	var (
		b   PriceBatch
		eff *time.Time
		apv *time.Time
		apl *time.Time
	)
	err := row.Scan(&b.ID, &b.BatchNo, &b.VendorID, &b.ProfileID, &b.Status, &eff, &b.SourceRef,
		&b.LineCount, &b.OutlierCount, &b.CreatedBy, &b.ApprovedBy, &apv, &apl, &b.CreatedAt)
	if err != nil {
		return PriceBatch{}, err
	}
	b.EffectiveDate, b.ApprovedAt, b.AppliedAt = eff, apv, apl
	return b, nil
}

func (r *Repository) GetBatch(ctx context.Context, id string) (PriceBatch, error) {
	if r.pool == nil {
		return PriceBatch{}, errNoDB
	}
	row := r.pool.QueryRow(ctx,
		`SELECT `+batchCols+` FROM price_batches b LEFT JOIN vendors v ON v.id = b.vendor_id WHERE b.id = $1`, id)
	b, err := scanBatch(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PriceBatch{}, errNotFound
		}
		return PriceBatch{}, err
	}
	return b, nil
}

func (r *Repository) ListBatches(ctx context.Context, f BatchFilter) ([]PriceBatch, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+batchCols+`
		   FROM price_batches b LEFT JOIN vendors v ON v.id = b.vendor_id
		  WHERE ($1 = '' OR b.status = $1) AND ($2 = '' OR b.vendor_id = NULLIF($2,'')::uuid)
		  ORDER BY b.created_at DESC LIMIT $3`,
		f.Status, f.VendorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PriceBatch
	for rows.Next() {
		b, err := scanBatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (r *Repository) UpdateBatch(ctx context.Context, b PriceBatch) (PriceBatch, error) {
	if r.pool == nil {
		return PriceBatch{}, errNoDB
	}
	row := r.pool.QueryRow(ctx,
		`UPDATE price_batches
		    SET status = $2, effective_date = $3, source_ref = $4, line_count = $5, outlier_count = $6,
		        approved_by = $7, approved_at = $8, applied_at = $9, updated_at = now()
		  WHERE id = $1
		  RETURNING `+returningBatchCols(),
		b.ID, b.Status, derefTimePtr(b.EffectiveDate), b.SourceRef, b.LineCount, b.OutlierCount,
		nullStr(b.ApprovedBy), derefTimePtr(b.ApprovedAt), derefTimePtr(b.AppliedAt))
	out, err := scanBatchReturning(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PriceBatch{}, errNotFound
		}
		return PriceBatch{}, err
	}
	return out, nil
}

func (r *Repository) ReplaceBatchLines(ctx context.Context, batchID string, lines []PriceChangeLine) error {
	if r.pool == nil {
		return errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM price_change_lines WHERE batch_id = $1`, batchID); err != nil {
		return err
	}
	for _, l := range lines {
		if _, err := tx.Exec(ctx,
			`INSERT INTO price_change_lines
			   (batch_id, product_id, sku, before_cost_cents, after_cost_cents,
			    before_retail_cents, after_retail_cents, variance_bps, is_outlier)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			batchID, nullStr(l.ProductID), l.SKU, l.BeforeCostCents, l.AfterCostCents,
			l.BeforeRetailCents, l.AfterRetailCents, l.VarianceBps, l.IsOutlier); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *Repository) ListBatchLines(ctx context.Context, batchID string) ([]PriceChangeLine, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id::text, batch_id::text, COALESCE(product_id::text,''), sku::text,
		        before_cost_cents, after_cost_cents, before_retail_cents, after_retail_cents,
		        variance_bps, is_outlier
		   FROM price_change_lines WHERE batch_id = $1 ORDER BY sku`, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PriceChangeLine
	for rows.Next() {
		var l PriceChangeLine
		if err := rows.Scan(&l.ID, &l.BatchID, &l.ProductID, &l.SKU, &l.BeforeCostCents, &l.AfterCostCents,
			&l.BeforeRetailCents, &l.AfterRetailCents, &l.VarianceBps, &l.IsOutlier); err != nil {
			return nil, err
		}
		l.Unmatched = l.ProductID == ""
		out = append(out, l)
	}
	return out, rows.Err()
}

func (r *Repository) DueBatches(ctx context.Context, asOf time.Time) ([]PriceBatch, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+batchCols+`
		   FROM price_batches b LEFT JOIN vendors v ON v.id = b.vendor_id
		  WHERE b.status = 'APPROVED' AND b.effective_date IS NOT NULL AND b.effective_date <= $1::date
		  ORDER BY b.effective_date`, asOf)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PriceBatch
	for rows.Next() {
		b, err := scanBatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ApplyBatch is the atomic apply: for every matched line it upserts the unified retail anchor
// (one pricing_list_prices row per product) and appends the append-only change-log rows, then
// flips the batch to APPLIED. Idempotent: a batch already APPLIED returns its recorded applied
// count with zero further writes (the status guard under FOR UPDATE makes concurrent re-apply
// safe). Cost changes are logged for audit even though pricing owns no product-cost table.
func (r *Repository) ApplyBatch(ctx context.Context, batchID, userID string) (int, error) {
	if r.pool == nil {
		return 0, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var (
		status string
		eff    *time.Time
	)
	if err := tx.QueryRow(ctx,
		`SELECT status, effective_date FROM price_batches WHERE id = $1 FOR UPDATE`, batchID).
		Scan(&status, &eff); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, errNotFound
		}
		return 0, err
	}
	if status == BatchApplied {
		var n int
		_ = tx.QueryRow(ctx,
			`SELECT count(*) FROM price_change_log WHERE batch_id = $1 AND field = 'retail_cents'`, batchID).Scan(&n)
		return n, tx.Commit(ctx) // idempotent no-op
	}
	if status != BatchApproved {
		return 0, errBadBatchState
	}

	rows, err := tx.Query(ctx,
		`SELECT product_id::text, before_cost_cents, after_cost_cents, before_retail_cents, after_retail_cents
		   FROM price_change_lines
		  WHERE batch_id = $1 AND product_id IS NOT NULL AND after_retail_cents IS NOT NULL`, batchID)
	if err != nil {
		return 0, err
	}
	type applyLine struct {
		productID                                        string
		beforeCost, afterCost, beforeRetail, afterRetail *int64
	}
	var lines []applyLine
	for rows.Next() {
		var l applyLine
		if err := rows.Scan(&l.productID, &l.beforeCost, &l.afterCost, &l.beforeRetail, &l.afterRetail); err != nil {
			rows.Close()
			return 0, err
		}
		lines = append(lines, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	applied := 0
	for _, l := range lines {
		// The retail anchor lands through the module's one list-price
		// write path (applyMatrixPrice, shared with the sync store),
		// trail and upsert in the same transaction.
		batch := batchID
		if err := applyMatrixPrice(ctx, tx, matrixPriceWrite{
			ProductID: l.productID, Cents: *l.afterRetail, Before: l.beforeRetail,
			BatchID: &batch, Effective: eff, Actor: userID,
		}); err != nil {
			return 0, err
		}
		if l.afterCost != nil {
			if _, err := tx.Exec(ctx,
				`INSERT INTO price_change_log (product_id, batch_id, field, before_cents, after_cents, effective_date, changed_by)
				 VALUES ($1, $2, 'cost_cents', $3, $4, $5, $6)`,
				l.productID, batchID, l.beforeCost, *l.afterCost, derefTimePtr(eff), nullStr(userID)); err != nil {
				return 0, err
			}
		}
		applied++
	}

	if _, err := tx.Exec(ctx,
		`UPDATE price_batches SET status = 'APPLIED', applied_at = now(), updated_at = now() WHERE id = $1`,
		batchID); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return applied, nil
}

func (r *Repository) ListChangeLog(ctx context.Context, f ChangeLogFilter) ([]ChangeLogRow, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id::text, COALESCE(product_id::text,''), COALESCE(batch_id::text,''), field,
		        before_cents, after_cents, effective_date, COALESCE(changed_by::text,''), created_at
		   FROM price_change_log
		  WHERE ($1 = '' OR product_id = NULLIF($1,'')::uuid)
		    AND ($2 = '' OR batch_id = NULLIF($2,'')::uuid)
		  ORDER BY created_at DESC LIMIT $3`,
		f.ProductID, f.BatchID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChangeLogRow
	for rows.Next() {
		var (
			c   ChangeLogRow
			eff *time.Time
		)
		if err := rows.Scan(&c.ID, &c.ProductID, &c.BatchID, &c.Field, &c.BeforeCents, &c.AfterCents,
			&eff, &c.ChangedBy, &c.CreatedAt); err != nil {
			return nil, err
		}
		c.EffectiveDate = eff
		out = append(out, c)
	}
	return out, rows.Err()
}
