package pricing

// w4_repository.go, the pgx half of the three W4-A seams (promoStore, rebateStore, convStore).
//
// Same house rules as repository.go: r.pool nil → errNoDB, pgx.ErrNoRows → errNotFound, ::text
// casts on uuids so the models stay string-typed, unique violations mapped to a named error rather
// than leaking a 23505 as a 500.
//
// ZERO-TRUST, RESTATED FOR THE NEW TABLES. Every SELECT below reads a pricing-owned table
// (pricing_promotions, pricing_clearance_items, rebate_programs, rebate_accruals,
// pricing_uom_conversions), with two deliberate, read-only JOIN exceptions that exist purely to
// put a human-readable label on an admin list: products.sku on the clearance/conversion lists and
// vendors.name on the rebate ledger. Without them the manager's screens render columns of uuids.
// They are LEFT JOINs on a list read, never on the resolve path, and they never write.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// - promoStore -

const promoCols = `id::text, code, name, COALESCE(product_id::text,''), COALESCE(category_id::text,''),
	COALESCE(branch_id::text,''), starts_at, ends_at, calc_type, discount_bps, discount_cents,
	price_cents, COALESCE(price_uom,''), priority, detail_text, is_active, version`

func scanPromo(row pgx.Row) (Promotion, error) {
	var p Promotion
	err := row.Scan(&p.ID, &p.Code, &p.Name, &p.ProductID, &p.CategoryID, &p.BranchID,
		&p.StartsAt, &p.EndsAt, &p.CalcType, &p.DiscountBps, &p.DiscountCents,
		&p.PriceCents, &p.PriceUOM, &p.Priority, &p.DetailText, &p.IsActive, &p.Version)
	return p, err
}

// ActivePromotions is the RESOLVE-PATH read, and it is deliberately narrow.
//
// The window predicate is in SQL (indexed, and it discards the overwhelming majority of rows -
// most promos in a mature system are historical). The SCOPE predicate is only a coarse filter here:
// it keeps rows that COULD apply (this product, this category, or store-wide) and leaves the
// specificity ordering to selectPromotion in Go. That split is on purpose, the precedence ruling
// is the contentious part of this feature, and it belongs in one readable, unit-testable function
// rather than smeared across an ORDER BY that nobody can test without a database.
func (r *Repository) ActivePromotions(ctx context.Context, productID, categoryID, branchID string, at time.Time) ([]Promotion, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	q := `SELECT ` + promoCols + `
	        FROM pricing_promotions
	       WHERE is_active
	         AND starts_at <= $1 AND ends_at > $1
	         AND (product_id  IS NULL OR product_id  = NULLIF($2, '')::uuid)
	         AND (category_id IS NULL OR category_id = NULLIF($3, '')::uuid)
	         AND (branch_id   IS NULL OR branch_id   = NULLIF($4, '')::uuid)`
	rows, err := r.pool.Query(ctx, q, at, productID, categoryID, branchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Promotion
	for rows.Next() {
		p, err := scanPromo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

const clearanceCols = `product_id::text, reason, clearance_price_cents, COALESCE(price_uom,''),
	started_at, ends_at, is_active, version`

func (r *Repository) ClearanceFor(ctx context.Context, productID string) (*ClearanceItem, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if productID == "" {
		return nil, nil
	}
	var c ClearanceItem
	err := r.pool.QueryRow(ctx, `SELECT `+clearanceCols+` FROM pricing_clearance_items WHERE product_id=$1`, productID).
		Scan(&c.ProductID, &c.Reason, &c.ClearancePriceCents, &c.PriceUOM, &c.StartedAt, &c.EndsAt, &c.IsActive, &c.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		// Absent row = not on clearance. That is the normal answer for almost every product, so it
		// is nil/nil rather than errNotFound, a resolve must not have to distinguish "no clearance"
		// from "lookup failed" at every call site.
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *Repository) ListPromotions(ctx context.Context, includeInactive bool) ([]Promotion, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	q := `SELECT ` + promoCols + ` FROM pricing_promotions
	       WHERE ($1 OR is_active)
	       ORDER BY starts_at DESC, code ASC`
	rows, err := r.pool.Query(ctx, q, includeInactive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Promotion
	for rows.Next() {
		p, err := scanPromo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repository) GetPromotion(ctx context.Context, id string) (Promotion, error) {
	if r.pool == nil {
		return Promotion{}, errNoDB
	}
	p, err := scanPromo(r.pool.QueryRow(ctx, `SELECT `+promoCols+` FROM pricing_promotions WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Promotion{}, errNotFound
	}
	return p, err
}

func (r *Repository) CreatePromotion(ctx context.Context, p Promotion) (Promotion, error) {
	if r.pool == nil {
		return Promotion{}, errNoDB
	}
	q := `INSERT INTO pricing_promotions
	        (code, name, product_id, category_id, branch_id, starts_at, ends_at, calc_type,
	         discount_bps, discount_cents, price_cents, price_uom, priority, detail_text, is_active)
	      VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,TRUE)
	      RETURNING id::text, version`
	err := r.pool.QueryRow(ctx, q, p.Code, p.Name, nullStr(p.ProductID), nullStr(p.CategoryID),
		nullStr(p.BranchID), p.StartsAt, p.EndsAt, p.CalcType, p.DiscountBps, p.DiscountCents,
		p.PriceCents, nullStr(p.PriceUOM), p.Priority, p.DetailText).Scan(&p.ID, &p.Version)
	if err != nil {
		if isUniqueViolation(err) {
			return Promotion{}, errPromoCodeTaken
		}
		if isFKViolation(err) {
			return Promotion{}, errFKViolation
		}
		return Promotion{}, fmt.Errorf("create promotion: %w", err)
	}
	p.IsActive = true
	return p, nil
}

func (r *Repository) SavePromotion(ctx context.Context, p Promotion, expectedVersion int64) (Promotion, error) {
	if r.pool == nil {
		return Promotion{}, errNoDB
	}
	q := `UPDATE pricing_promotions SET
	        name=$2, product_id=$3, category_id=$4, branch_id=$5, starts_at=$6, ends_at=$7,
	        calc_type=$8, discount_bps=$9, discount_cents=$10, price_cents=$11, price_uom=$12,
	        priority=$13, detail_text=$14, is_active=$15, version=version+1, updated_at=now()
	      WHERE id=$1 AND version=$16
	      RETURNING ` + promoCols
	out, err := scanPromo(r.pool.QueryRow(ctx, q, p.ID, p.Name, nullStr(p.ProductID), nullStr(p.CategoryID),
		nullStr(p.BranchID), p.StartsAt, p.EndsAt, p.CalcType, p.DiscountBps, p.DiscountCents,
		p.PriceCents, nullStr(p.PriceUOM), p.Priority, p.DetailText, p.IsActive, expectedVersion))
	if errors.Is(err, pgx.ErrNoRows) {
		return Promotion{}, r.rowConflictOrMissing(ctx, "pricing_promotions", p.ID)
	}
	if err != nil {
		if isFKViolation(err) {
			return Promotion{}, errFKViolation
		}
		return Promotion{}, fmt.Errorf("save promotion: %w", err)
	}
	return out, nil
}

// DeletePromotion is a RETIREMENT, not a DELETE. A promotion that priced real lines is part of the
// explanation for those prices; hard-deleting it makes a historical quote unexplainable. Setting
// is_active=false takes it out of every resolve immediately (the ActivePromotions predicate) while
// leaving it readable in the admin list with includeInactive.
func (r *Repository) DeletePromotion(ctx context.Context, id string, expectedVersion int64) error {
	if r.pool == nil {
		return errNoDB
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE pricing_promotions SET is_active=FALSE, version=version+1, updated_at=now()
		  WHERE id=$1 AND version=$2`, id, expectedVersion)
	if err != nil {
		return fmt.Errorf("retire promotion: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return r.rowConflictOrMissing(ctx, "pricing_promotions", id)
	}
	return nil
}

func (r *Repository) ListClearance(ctx context.Context, includeInactive bool) ([]ClearanceItem, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	// The products LEFT JOIN is label-only (see the zero-trust note at the top of this file): an
	// admin list of bare product uuids is unusable.
	q := `SELECT c.product_id::text, COALESCE(p.sku::text,''), COALESCE(p.name,''), c.reason,
	             c.clearance_price_cents, COALESCE(c.price_uom,''), c.started_at, c.ends_at,
	             c.is_active, c.version
	        FROM pricing_clearance_items c
	        LEFT JOIN products p ON p.id = c.product_id
	       WHERE ($1 OR c.is_active)
	       ORDER BY c.started_at DESC`
	rows, err := r.pool.Query(ctx, q, includeInactive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ClearanceItem
	for rows.Next() {
		var c ClearanceItem
		if err := rows.Scan(&c.ProductID, &c.SKU, &c.Name, &c.Reason, &c.ClearancePriceCents,
			&c.PriceUOM, &c.StartedAt, &c.EndsAt, &c.IsActive, &c.Version); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *Repository) UpsertClearance(ctx context.Context, c ClearanceItem) (ClearanceItem, error) {
	if r.pool == nil {
		return ClearanceItem{}, errNoDB
	}
	started := c.StartedAt
	if started.IsZero() {
		started = time.Now()
	}
	q := `INSERT INTO pricing_clearance_items
	        (product_id, reason, clearance_price_cents, price_uom, started_at, ends_at, is_active)
	      VALUES ($1,$2,$3,$4,$5,$6,$7)
	      ON CONFLICT (product_id) DO UPDATE SET
	        reason=EXCLUDED.reason, clearance_price_cents=EXCLUDED.clearance_price_cents,
	        price_uom=EXCLUDED.price_uom, ends_at=EXCLUDED.ends_at, is_active=EXCLUDED.is_active,
	        version=pricing_clearance_items.version+1, updated_at=now()
	      RETURNING ` + clearanceCols
	var out ClearanceItem
	err := r.pool.QueryRow(ctx, q, c.ProductID, c.Reason, c.ClearancePriceCents, nullStr(c.PriceUOM),
		started, c.EndsAt, c.IsActive).
		Scan(&out.ProductID, &out.Reason, &out.ClearancePriceCents, &out.PriceUOM,
			&out.StartedAt, &out.EndsAt, &out.IsActive, &out.Version)
	if err != nil {
		if isFKViolation(err) {
			return ClearanceItem{}, errFKViolation
		}
		return ClearanceItem{}, fmt.Errorf("upsert clearance: %w", err)
	}
	return out, nil
}

func (r *Repository) DeleteClearance(ctx context.Context, productID string) error {
	if r.pool == nil {
		return errNoDB
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE pricing_clearance_items SET is_active=FALSE, version=version+1, updated_at=now()
		  WHERE product_id=$1`, productID)
	if err != nil {
		return fmt.Errorf("clear clearance: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errNotFound
	}
	return nil
}

// - rebateStore -

const rebateCols = `id::text, code, name, vendor_id::text, COALESCE(product_id::text,''),
	COALESCE(category_id::text,''), basis, rate_bps, rate_cents_per_unit, starts_at, ends_at,
	notes, is_active, version`

func scanRebate(row pgx.Row) (RebateProgram, error) {
	var p RebateProgram
	err := row.Scan(&p.ID, &p.Code, &p.Name, &p.VendorID, &p.ProductID, &p.CategoryID,
		&p.Basis, &p.RateBps, &p.RateCentsPerUnit, &p.StartsAt, &p.EndsAt, &p.Notes, &p.IsActive, &p.Version)
	return p, err
}

func (r *Repository) ListRebatePrograms(ctx context.Context, vendorID string, includeInactive bool) ([]RebateProgram, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	q := `SELECT p.id::text, p.code, p.name, p.vendor_id::text, COALESCE(v.name,''),
	             COALESCE(p.product_id::text,''), COALESCE(p.category_id::text,''), p.basis,
	             p.rate_bps, p.rate_cents_per_unit, p.starts_at, p.ends_at, p.notes, p.is_active, p.version
	        FROM rebate_programs p
	        LEFT JOIN vendors v ON v.id = p.vendor_id
	       WHERE ($1 = '' OR p.vendor_id = $1::uuid)
	         AND ($2 OR p.is_active)
	       ORDER BY COALESCE(v.name,''), p.code`
	rows, err := r.pool.Query(ctx, q, vendorID, includeInactive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RebateProgram
	for rows.Next() {
		var p RebateProgram
		if err := rows.Scan(&p.ID, &p.Code, &p.Name, &p.VendorID, &p.VendorName, &p.ProductID,
			&p.CategoryID, &p.Basis, &p.RateBps, &p.RateCentsPerUnit, &p.StartsAt, &p.EndsAt,
			&p.Notes, &p.IsActive, &p.Version); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repository) GetRebateProgram(ctx context.Context, id string) (RebateProgram, error) {
	if r.pool == nil {
		return RebateProgram{}, errNoDB
	}
	p, err := scanRebate(r.pool.QueryRow(ctx, `SELECT `+rebateCols+` FROM rebate_programs WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return RebateProgram{}, errNotFound
	}
	return p, err
}

func (r *Repository) CreateRebateProgram(ctx context.Context, p RebateProgram) (RebateProgram, error) {
	if r.pool == nil {
		return RebateProgram{}, errNoDB
	}
	q := `INSERT INTO rebate_programs
	        (code, name, vendor_id, product_id, category_id, basis, rate_bps, rate_cents_per_unit,
	         starts_at, ends_at, notes, is_active)
	      VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,TRUE)
	      RETURNING id::text, version`
	err := r.pool.QueryRow(ctx, q, p.Code, p.Name, p.VendorID, nullStr(p.ProductID), nullStr(p.CategoryID),
		p.Basis, p.RateBps, p.RateCentsPerUnit, p.StartsAt, p.EndsAt, p.Notes).Scan(&p.ID, &p.Version)
	if err != nil {
		if isUniqueViolation(err) {
			return RebateProgram{}, errRebateCodeTaken
		}
		if isFKViolation(err) {
			return RebateProgram{}, errFKViolation
		}
		return RebateProgram{}, fmt.Errorf("create rebate program: %w", err)
	}
	p.IsActive = true
	return p, nil
}

func (r *Repository) SaveRebateProgram(ctx context.Context, p RebateProgram, expectedVersion int64) (RebateProgram, error) {
	if r.pool == nil {
		return RebateProgram{}, errNoDB
	}
	q := `UPDATE rebate_programs SET
	        name=$2, product_id=$3, category_id=$4, basis=$5, rate_bps=$6, rate_cents_per_unit=$7,
	        starts_at=$8, ends_at=$9, notes=$10, is_active=$11, version=version+1, updated_at=now()
	      WHERE id=$1 AND version=$12
	      RETURNING ` + rebateCols
	out, err := scanRebate(r.pool.QueryRow(ctx, q, p.ID, p.Name, nullStr(p.ProductID), nullStr(p.CategoryID),
		p.Basis, p.RateBps, p.RateCentsPerUnit, p.StartsAt, p.EndsAt, p.Notes, p.IsActive, expectedVersion))
	if errors.Is(err, pgx.ErrNoRows) {
		return RebateProgram{}, r.rowConflictOrMissing(ctx, "rebate_programs", p.ID)
	}
	if err != nil {
		return RebateProgram{}, fmt.Errorf("save rebate program: %w", err)
	}
	return out, nil
}

// DeleteRebateProgram retires rather than deletes, for the same reason DeletePromotion does, and
// more sharply here: rebate_accruals CASCADEs from this row, so a hard delete would silently
// destroy the claim ledger the program produced.
func (r *Repository) DeleteRebateProgram(ctx context.Context, id string, expectedVersion int64) error {
	if r.pool == nil {
		return errNoDB
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE rebate_programs SET is_active=FALSE, version=version+1, updated_at=now()
		  WHERE id=$1 AND version=$2`, id, expectedVersion)
	if err != nil {
		return fmt.Errorf("retire rebate program: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return r.rowConflictOrMissing(ctx, "rebate_programs", id)
	}
	return nil
}

func (r *Repository) ProgramsForLine(ctx context.Context, productID, categoryID, vendorID string) ([]RebateProgram, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	q := `SELECT ` + rebateCols + `
	        FROM rebate_programs
	       WHERE is_active
	         AND (product_id  IS NULL OR product_id  = NULLIF($1, '')::uuid)
	         AND (category_id IS NULL OR category_id = NULLIF($2, '')::uuid)
	         AND ($3 = '' OR vendor_id = $3::uuid)`
	rows, err := r.pool.Query(ctx, q, productID, categoryID, vendorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RebateProgram
	for rows.Next() {
		p, err := scanRebate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repository) InsertAccrual(ctx context.Context, a RebateAccrual, userID string) (RebateAccrual, error) {
	if r.pool == nil {
		return RebateAccrual{}, errNoDB
	}
	q := `INSERT INTO rebate_accruals
	        (program_id, vendor_id, product_id, branch_id, source_type, source_id,
	         qty_base, basis_cents, accrual_cents, rate_note, accrued_at, created_by)
	      VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
	      RETURNING id::text, accrued_at`
	err := r.pool.QueryRow(ctx, q, a.ProgramID, a.VendorID, a.ProductID, nullStr(a.BranchID),
		a.SourceType, nullStr(a.SourceID), a.QtyBase, a.BasisCents, a.AccrualCents,
		a.RateNote, a.AccruedAt, nullStr(userID)).Scan(&a.ID, &a.AccruedAt)
	if err != nil {
		// The 0112 unique index firing is the AT-MOST-ONCE guard doing its job on a retried
		// delivery. It is mapped to a sentinel the service treats as "already done", never to a
		// 500, see the invariant note at the top of rebate.go.
		if isUniqueViolation(err) {
			return RebateAccrual{}, errAccrualExists
		}
		if isFKViolation(err) {
			return RebateAccrual{}, errFKViolation
		}
		return RebateAccrual{}, fmt.Errorf("insert accrual: %w", err)
	}
	return a, nil
}

func (r *Repository) VendorLedger(ctx context.Context, vendorID string, from, to *time.Time) (VendorRebateLedger, error) {
	if r.pool == nil {
		return VendorRebateLedger{}, errNoDB
	}
	out := VendorRebateLedger{VendorID: vendorID, From: from, To: to}
	if err := r.pool.QueryRow(ctx, `SELECT name FROM vendors WHERE id=$1`, vendorID).Scan(&out.VendorName); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return VendorRebateLedger{}, errNotFound
		}
		return VendorRebateLedger{}, err
	}
	q := `SELECT a.id::text, a.program_id::text, p.code, a.vendor_id::text, a.product_id::text,
	             COALESCE(pr.sku::text,''), COALESCE(a.branch_id::text,''), a.source_type,
	             COALESCE(a.source_id::text,''), a.qty_base, a.basis_cents, a.accrual_cents,
	             a.rate_note, a.accrued_at, p.basis
	        FROM rebate_accruals a
	        JOIN rebate_programs p ON p.id = a.program_id
	        LEFT JOIN products pr ON pr.id = a.product_id
	       WHERE a.vendor_id = $1
	         AND ($2::timestamptz IS NULL OR a.accrued_at >= $2)
	         AND ($3::timestamptz IS NULL OR a.accrued_at <  $3)
	       ORDER BY a.accrued_at DESC, a.id DESC`
	rows, err := r.pool.Query(ctx, q, vendorID, from, to)
	if err != nil {
		return VendorRebateLedger{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var a RebateAccrual
		var qty float64
		if err := rows.Scan(&a.ID, &a.ProgramID, &a.ProgramCode, &a.VendorID, &a.ProductID,
			&a.ProductSKU, &a.BranchID, &a.SourceType, &a.SourceID, &qty, &a.BasisCents,
			&a.AccrualCents, &a.RateNote, &a.AccruedAt, &a.Basis); err != nil {
			return VendorRebateLedger{}, err
		}
		a.QtyBase = qty
		out.Rows = append(out.Rows, a)
		out.TotalCents += a.AccrualCents
		out.AccrualCount++
	}
	return out, rows.Err()
}

// - convStore -

const convCols = `id::text, COALESCE(product_id::text,''), COALESCE(category_id::text,''),
	from_uom::text, to_uom::text, factor::float8, round_up_at_pick, source, source_note, is_active, version, updated_at`

func scanConv(row pgx.Row) (UOMConversion, error) {
	var c UOMConversion
	err := row.Scan(&c.ID, &c.ProductID, &c.CategoryID, &c.FromUOM, &c.ToUOM, &c.Factor,
		&c.RoundUpAtPick, &c.Source, &c.SourceNote, &c.IsActive, &c.Version, &c.UpdatedAt)
	return c, err
}

// ConversionsFor pulls the product's rows, its category's rows and the global rows in ONE query.
// The Go-side buildGraph resolves per-edge precedence, see the note in uomplan.go about why that
// is not an ORDER BY + DISTINCT ON here: the reverse edges have to be minted before precedence can
// mean anything, and minting them in SQL would double the row count for no gain.
func (r *Repository) ConversionsFor(ctx context.Context, productID, categoryID string) ([]UOMConversion, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	q := `SELECT ` + convCols + `
	        FROM pricing_uom_conversions
	       WHERE is_active
	         AND ( (product_id IS NULL AND category_id IS NULL)
	            OR product_id  = NULLIF($1, '')::uuid
	            OR category_id = NULLIF($2, '')::uuid )`
	rows, err := r.pool.Query(ctx, q, productID, categoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UOMConversion
	for rows.Next() {
		c, err := scanConv(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *Repository) ListConversions(ctx context.Context, productID string, includeInactive bool) ([]UOMConversion, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	q := `SELECT c.id::text, COALESCE(c.product_id::text,''), COALESCE(p.sku::text,''),
	             COALESCE(c.category_id::text,''), c.from_uom::text, c.to_uom::text, c.factor::float8,
	             c.round_up_at_pick, c.source, c.source_note, c.is_active, c.version, c.updated_at
	        FROM pricing_uom_conversions c
	        LEFT JOIN products p ON p.id = c.product_id
	       WHERE ($1 = '' OR c.product_id = $1::uuid)
	         AND ($2 OR c.is_active)
	       ORDER BY COALESCE(p.sku::text,''), c.from_uom, c.to_uom
	       LIMIT 500`
	rows, err := r.pool.Query(ctx, q, productID, includeInactive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UOMConversion
	for rows.Next() {
		var c UOMConversion
		if err := rows.Scan(&c.ID, &c.ProductID, &c.ProductSKU, &c.CategoryID, &c.FromUOM, &c.ToUOM,
			&c.Factor, &c.RoundUpAtPick, &c.Source, &c.SourceNote, &c.IsActive, &c.Version, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpsertConversion writes an edge, keyed on the SCOPE + the edge itself. The ON CONFLICT target is
// the ux_pricing_uom_conv_edge expression index, so an admin re-saving "SF→PLT for this product"
// updates the row instead of colliding, which is the difference between an editable grid and one
// that 409s on every second save.
func (r *Repository) UpsertConversion(ctx context.Context, c UOMConversion) (UOMConversion, error) {
	if r.pool == nil {
		return UOMConversion{}, errNoDB
	}
	q := `INSERT INTO pricing_uom_conversions
	        (product_id, category_id, from_uom, to_uom, factor, round_up_at_pick, source, source_note, is_active)
	      VALUES ($1,$2,$3,$4,$5,$6,$7,$8,TRUE)
	      ON CONFLICT (COALESCE(product_id,  '00000000-0000-0000-0000-000000000000'::uuid),
	                   COALESCE(category_id, '00000000-0000-0000-0000-000000000000'::uuid),
	                   from_uom, to_uom) WHERE is_active
	      DO UPDATE SET factor=EXCLUDED.factor, round_up_at_pick=EXCLUDED.round_up_at_pick,
	                    source=EXCLUDED.source, source_note=EXCLUDED.source_note,
	                    version=pricing_uom_conversions.version+1, updated_at=now()
	      RETURNING ` + convCols
	out, err := scanConv(r.pool.QueryRow(ctx, q, nullStr(c.ProductID), nullStr(c.CategoryID),
		c.FromUOM, c.ToUOM, c.Factor, c.RoundUpAtPick, c.Source, c.SourceNote))
	if err != nil {
		if isFKViolation(err) {
			return UOMConversion{}, errFKViolation
		}
		return UOMConversion{}, fmt.Errorf("upsert conversion: %w", err)
	}
	return out, nil
}

func (r *Repository) DeleteConversion(ctx context.Context, id string) error {
	if r.pool == nil {
		return errNoDB
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE pricing_uom_conversions SET is_active=FALSE, version=version+1, updated_at=now() WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("retire conversion: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errNotFound
	}
	return nil
}

// rowConflictOrMissing is the generic form of ruleConflictOrMissing: after a version-guarded write
// matched 0 rows, the id still existing means the version moved on (409) and the id being gone
// means the row is (404). The table name is a package-internal literal at every call site, never
// user input, the three callers pass constants, so the interpolation cannot carry a payload.
func (r *Repository) rowConflictOrMissing(ctx context.Context, table, id string) error {
	var exists bool
	q := fmt.Sprintf(`SELECT EXISTS(SELECT 1 FROM %s WHERE id=$1)`, table)
	if err := r.pool.QueryRow(ctx, q, id).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return errConflict
	}
	return errNotFound
}
