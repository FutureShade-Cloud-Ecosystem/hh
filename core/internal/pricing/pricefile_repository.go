package pricing

// pricefile_repository.go, the pgx half of priceFileStore, over PRICING'S OWN TABLES ONLY.
//
// The two reads this surface needs that are NOT pricing's, `products` for the SKU lookup and
// `vendors` for the manufacturer picker, are consumer-declared seams (pricefile_model.go) adapted
// in cmd/server. They are absent from this file on purpose: 0039's repository reaches into both with
// a JOIN, that is pre-existing, and new code does not get to inherit it.
//
// The one join below is to `users`, and it is the exception that proves the rule rather than a
// second helping: it resolves the display name of the person on an audit row, it is a LEFT JOIN so a
// deleted user degrades to a blank name rather than dropping the audit row, and it is the same join
// the module's other audit reader already makes. A seam for one nullable name on a list nobody
// paginates would be ceremony.

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func (r *Repository) ImportCostVisibility(ctx context.Context, branchID string) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var (
		q    string
		args []any
	)
	if branchID == "" {
		q = `SELECT import_cost_visibility FROM pricing_anchor_policy WHERE branch_id IS NULL LIMIT 1`
	} else {
		q = `SELECT import_cost_visibility FROM pricing_anchor_policy
		      WHERE branch_id = $1 OR branch_id IS NULL
		      ORDER BY branch_id NULLS LAST LIMIT 1`
		args = append(args, branchID)
	}
	var vis string
	if err := r.pool.QueryRow(ctx, q, args...).Scan(&vis); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No policy row at all. MANAGER_UP, not ALL_STAFF: a deployment missing its config row
			// must not be the deployment where the counter can read dealer net.
			return "MANAGER_UP", nil
		}
		return "", err
	}
	return vis, nil
}

// ActiveProfileForVendor is the LAZY RESOLVE. Oldest active profile wins, which keeps the seven
// profiles 0039 seeded answering for their vendors and stops a second profile from displacing one
// silently. errNoProfile means "this vendor has never been mapped", which is a state, not a fault.
func (r *Repository) ActiveProfileForVendor(ctx context.Context, vendorID string) (ImportProfile, string, error) {
	if r.pool == nil {
		return ImportProfile{}, "", errNoDB
	}
	var (
		p       ImportProfile
		origin  string
		mapping []byte
	)
	err := r.pool.QueryRow(ctx,
		`SELECT p.id::text, p.vendor_id::text, v.name, p.name, p.mapping, p.markup_bps, p.is_active, p.origin
		   FROM price_import_profiles p JOIN vendors v ON v.id = p.vendor_id
		  WHERE p.vendor_id = $1 AND p.is_active
		  ORDER BY p.created_at, p.id
		  LIMIT 1`, vendorID).
		Scan(&p.ID, &p.VendorID, &p.VendorName, &p.Name, &mapping, &p.MarkupBps, &p.IsActive, &origin)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ImportProfile{}, "", errNoProfile
		}
		return ImportProfile{}, "", err
	}
	p.Mapping = map[string]string{}
	if len(mapping) > 0 {
		_ = json.Unmarshal(mapping, &p.Mapping)
	}
	return p, origin, nil
}

func (r *Repository) ListPricesForProducts(ctx context.Context, productIDs []string) (map[string]int64, error) {
	out := map[string]int64{}
	if r.pool == nil {
		return nil, errNoDB
	}
	if len(productIDs) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT product_id::text, list_price_cents FROM pricing_list_prices WHERE product_id = ANY($1::uuid[])`,
		productIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id    string
			cents int64
		)
		if err := rows.Scan(&id, &cents); err != nil {
			return nil, err
		}
		out[id] = cents
	}
	return out, rows.Err()
}

func (r *Repository) ProfileVendorRows(ctx context.Context) (map[string]PriceFileVendor, error) {
	out := map[string]PriceFileVendor{}
	if r.pool == nil {
		return nil, errNoDB
	}
	// DISTINCT ON mirrors ActiveProfileForVendor's ordering exactly. If the picker showed a different
	// profile than the import would use, every mapping on the screen would be a lie about a different
	// row, so the two orderings are written next to each other in the same file for a reason.
	rows, err := r.pool.Query(ctx,
		`SELECT DISTINCT ON (p.vendor_id)
		        p.vendor_id::text, p.id::text, p.origin, p.mapping, p.last_import_at
		   FROM price_import_profiles p
		  WHERE p.is_active
		  ORDER BY p.vendor_id, p.created_at, p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			v       PriceFileVendor
			mapping []byte
			last    *time.Time
		)
		if err := rows.Scan(&v.VendorID, &v.ProfileID, &v.Origin, &mapping, &last); err != nil {
			return nil, err
		}
		v.Mapping = map[string]string{}
		if len(mapping) > 0 {
			_ = json.Unmarshal(mapping, &v.Mapping)
		}
		v.LastImportAt = last
		out[v.VendorID] = v
	}
	return out, rows.Err()
}

// StagePriceFile is the WHOLE write, in one transaction: the profile (created if this vendor has
// never been mapped), the batch, its change lines, and the append-only audit row.
//
// ONE TRANSACTION BECAUSE THE HALF-DONE STATES ARE ALL BAD. A batch with no audit row is a price
// change nobody imported; an audit row with no batch is an import claiming to have staged something
// that is not there; a profile written for an import that then failed is a mapping the next operator
// inherits without anybody having approved it.
func (r *Repository) StagePriceFile(ctx context.Context, in priceFileStage) (priceFileStaged, error) {
	var out priceFileStaged
	if r.pool == nil {
		return out, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rawMapping, err := json.Marshal(in.Mapping)
	if err != nil {
		return out, err
	}
	userID := nullableUUID(in.UserID)

	out.ProfileID = in.ProfileID
	if in.ProfileID == "" {
		// LAZY CREATION. origin FIRST_IMPORT is what later tells a reader this mapping was authored
		// against a real file rather than guessed by a seed, see migration 0198's first section.
		if err := tx.QueryRow(ctx,
			`INSERT INTO price_import_profiles (vendor_id, name, mapping, origin, created_by, last_import_at)
			 VALUES ($1, $2, $3, 'FIRST_IMPORT', $4, $5)
			 ON CONFLICT (vendor_id, name) DO UPDATE
			    SET mapping = EXCLUDED.mapping, last_import_at = EXCLUDED.last_import_at, updated_at = now()
			 RETURNING id::text`,
			in.VendorID, in.ProfileName, rawMapping, userID, in.Now).Scan(&out.ProfileID); err != nil {
			return out, err
		}
		out.ProfileCreated = true
	} else {
		// The mapping IS the profile, so an apply that used a different mapping saves it. The merge
		// happened in the service (mergedMapping) so a key this screen does not offer survives.
		if _, err := tx.Exec(ctx,
			`UPDATE price_import_profiles SET mapping = $2, last_import_at = $3, updated_at = now() WHERE id = $1`,
			in.ProfileID, rawMapping, in.Now); err != nil {
			return out, err
		}
	}

	if err := tx.QueryRow(ctx,
		`INSERT INTO price_batches (batch_no, vendor_id, profile_id, status, effective_date, source_ref,
		                            line_count, outlier_count, created_by)
		 VALUES ($1, $2, $3, 'STAGED', $4, $5, $6, $7, $8)
		 RETURNING id::text, batch_no::text`,
		in.BatchNo, in.VendorID, out.ProfileID, derefTimePtr(in.EffectiveDate), in.SourceRef,
		len(in.Lines), in.OutlierCount, userID).Scan(&out.BatchID, &out.BatchNo); err != nil {
		return out, mapPriceFileWriteErr(err)
	}

	for _, l := range in.Lines {
		if _, err := tx.Exec(ctx,
			`INSERT INTO price_change_lines (batch_id, product_id, sku, after_cost_cents,
			                                 before_retail_cents, after_retail_cents, variance_bps, is_outlier)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			out.BatchID, nullableUUID(l.ProductID), l.SKU, l.AfterCostCents,
			l.BeforeRetailCents, l.AfterRetailCents, l.VarianceBps, l.IsOutlier); err != nil {
			return out, err
		}
		out.Staged++
	}

	c := in.Run.Counts
	if err := tx.QueryRow(ctx,
		`INSERT INTO price_import_runs (vendor_id, profile_id, batch_id, source_name, source_format,
		                                source_bytes, content_digest, mapping_used, row_count,
		                                new_count, changed_count, unchanged_count, unknown_count,
		                                unpriced_count, imported_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		 RETURNING id::text`,
		in.VendorID, out.ProfileID, out.BatchID, in.Run.SourceName, in.Run.SourceFormat,
		in.Run.SourceBytes, in.Run.ContentDigest, rawMapping, c.Rows,
		c.New, c.Changed, c.Unchanged, c.Unknown, c.Unpriced, userID).Scan(&out.RunID); err != nil {
		return out, err
	}

	if err := tx.Commit(ctx); err != nil {
		return out, err
	}
	return out, nil
}

func (r *Repository) ListImportRuns(ctx context.Context, vendorID string, limit int) ([]PriceImportRun, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.pool.Query(ctx,
		`SELECT ru.id::text, COALESCE(ru.vendor_id::text,''), COALESCE(v.name,''),
		        COALESCE(ru.batch_id::text,''), COALESCE(b.batch_no::text,''),
		        ru.source_name, ru.source_format, ru.source_bytes, ru.content_digest, ru.mapping_used,
		        ru.row_count, ru.new_count, ru.changed_count, ru.unchanged_count, ru.unknown_count,
		        ru.unpriced_count, COALESCE(ru.imported_by::text,''), COALESCE(u.display_name,''), ru.created_at
		   FROM price_import_runs ru
		   LEFT JOIN vendors v       ON v.id = ru.vendor_id
		   LEFT JOIN price_batches b ON b.id = ru.batch_id
		   LEFT JOIN users u         ON u.id = ru.imported_by
		  WHERE ($1 = '' OR ru.vendor_id = NULLIF($1,'')::uuid)
		  ORDER BY ru.created_at DESC
		  LIMIT $2`, vendorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PriceImportRun
	for rows.Next() {
		var (
			run     PriceImportRun
			mapping []byte
		)
		if err := rows.Scan(&run.ID, &run.VendorID, &run.VendorName, &run.BatchID, &run.BatchNo,
			&run.SourceName, &run.SourceFormat, &run.SourceBytes, &run.ContentDigest, &mapping,
			&run.Counts.Rows, &run.Counts.New, &run.Counts.Changed, &run.Counts.Unchanged,
			&run.Counts.Unknown, &run.Counts.Unpriced, &run.ImportedBy, &run.ImportedByName,
			&run.CreatedAt); err != nil {
			return nil, err
		}
		run.MappingUsed = map[string]string{}
		if len(mapping) > 0 {
			_ = json.Unmarshal(mapping, &run.MappingUsed)
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

// nullableUUID turns "" into a real SQL NULL. An empty string cast to uuid is a 22P02 error, and a
// price batch created by a system path with no user is a legitimate row with a NULL creator.
func nullableUUID(id string) any {
	if id == "" {
		return nil
	}
	return id
}

// mapPriceFileWriteErr turns the one collision this write can produce into words. batch_no carries a
// CITEXT UNIQUE and is built from a timestamp plus a digest prefix, so a collision means the same
// file was applied twice inside the same second, which is a double-submit, and saying so beats a
// generic 500.
func mapPriceFileWriteErr(err error) error {
	if isUniqueViolation(err) {
		return errConflict
	}
	return err
}
