package pricing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var errNoDB = errors.New("pricing: database not configured")

// isUniqueViolation reports whether err is a Postgres unique_violation (23505), used to
// turn the uq_pricing_rules_active index + the (customer,category) override UNIQUE into
// friendly 409s instead of a bare 500.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// isFKViolation reports whether err is a Postgres foreign_key_violation (23503), used to turn a
// well-formed request that references a nonexistent customer/product/category/branch id into a
// friendly 422 (errFKViolation) instead of a bare 500 (finding #10).
func isFKViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// Repository owns the pricing-* / quantity_break_* / customer_category_overrides tables
// (pgx). Zero-trust: it never SELECTs product_categories / product_uoms / products -
// category + UOM factors come through the injected catalog seam (see service.go).
type Repository struct {
	pool *pgxpool.Pool // nil in scaffold/tests
}

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// nullStr maps "" → SQL NULL (so empty UUID/text scope dims don't break a cast/compare).
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// rule scan column order (kept in one place so MatchingRules/GetRule/ListRules agree).
const ruleCols = `id::text, scope, customer_id::text, customer_type, category_id::text,
	product_id::text, branch_id::text, calc_type, price_cents, discount_bps, markup_bps,
	price_uom::text, priority, valid_from, valid_to, detail_text, is_active, version`

func scanRule(row pgx.Row) (Rule, error) {
	var r Rule
	var cust, ctype, cat, prod, branch *string
	var vf, vt *time.Time
	if err := row.Scan(
		&r.ID, &r.Scope, &cust, &ctype, &cat, &prod, &branch, &r.CalcType,
		&r.PriceCents, &r.DiscountBps, &r.MarkupBps, &r.PriceUOM, &r.Priority,
		&vf, &vt, &r.DetailText, &r.IsActive, &r.Version,
	); err != nil {
		return Rule{}, err
	}
	r.CustomerID, r.CustomerType, r.CategoryID, r.ProductID, r.BranchID =
		derefStr(cust), derefStr(ctype), derefStr(cat), derefStr(prod), derefStr(branch)
	r.ValidFrom, r.ValidTo = vf, vt
	return r, nil
}

// MatchingRules returns the active candidate rules for the input in one query, with the
// total-order tie-break applied in SQL (the service re-applies it for determinism, and
// filters the validity window). NULL scope dims simply never match a non-NULL input.
func (r *Repository) MatchingRules(ctx context.Context, in ResolveInput) ([]Rule, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	q := `SELECT ` + ruleCols + ` FROM pricing_rules
	      WHERE is_active
	        AND ( (scope='CUSTOMER_PRODUCT'  AND customer_id = $1 AND product_id = $2)
	           OR (scope='CUSTOMER_CATEGORY' AND customer_id = $1 AND category_id = $4)
	           OR (scope='TIER_CATEGORY'     AND customer_type = $3 AND category_id = $4)
	           OR (scope='TIER_DEFAULT'      AND customer_type = $3) )
	        AND (branch_id IS NULL OR branch_id = $5)
	      ORDER BY priority DESC, valid_from DESC NULLS LAST, id ASC`
	rows, err := r.pool.Query(ctx, q, nullStr(in.CustomerID), nullStr(in.ProductID), in.CustomerType, nullStr(in.CategoryID), nullStr(in.BranchID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Rule
	for rows.Next() {
		rule, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, rows.Err()
}

// QuantityBreaksFor returns the active break items for a product or its category.
func (r *Repository) QuantityBreaksFor(ctx context.Context, productID, categoryID string) ([]QuantityBreak, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	q := `SELECT h.id::text, i.id::text, h.name, h.product_id::text, h.category_id::text,
	             h.threshold_uom::text, i.from_qty, i.to_qty, i.value_type,
	             i.discount_bps, i.discount_cents, i.surcharge_bps, i.surcharge_cents, i.price_uom::text
	        FROM quantity_break_headers h
	        JOIN quantity_break_items i ON i.header_id = h.id
	       WHERE h.is_active AND (h.product_id = $1 OR h.category_id = $2)
	       ORDER BY i.from_qty ASC`
	rows, err := r.pool.Query(ctx, q, nullStr(productID), nullStr(categoryID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []QuantityBreak
	for rows.Next() {
		var b QuantityBreak
		var prod, cat *string
		var fromQty, toQty *float64
		if err := rows.Scan(&b.HeaderID, &b.ItemID, &b.Name, &prod, &cat, &b.ThresholdUOM,
			&fromQty, &toQty, &b.ValueType, &b.DiscountBps, &b.DiscountCents, &b.SurchargeBps, &b.SurchargeCents, &b.PriceUOM); err != nil {
			return nil, err
		}
		b.ProductID, b.CategoryID = derefStr(prod), derefStr(cat)
		if fromQty != nil {
			b.FromQty = *fromQty
		}
		b.ToQty = toQty
		out = append(out, b)
	}
	return out, rows.Err()
}

func (r *Repository) OverrideFor(ctx context.Context, customerID, categoryID string) (*Override, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if customerID == "" || categoryID == "" {
		return nil, nil
	}
	q := `SELECT id::text, customer_id::text, category_id::text, calc_type, price_cents,
	             discount_bps, markup_bps, price_uom::text, notes, version
	        FROM customer_category_overrides WHERE customer_id = $1 AND category_id = $2`
	var o Override
	err := r.pool.QueryRow(ctx, q, customerID, categoryID).Scan(
		&o.ID, &o.CustomerID, &o.CategoryID, &o.CalcType, &o.PriceCents,
		&o.DiscountBps, &o.MarkupBps, &o.PriceUOM, &o.Notes, &o.Version)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &o, nil
}

func (r *Repository) ListPrice(ctx context.Context, productID string) (*ListPrice, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	var lp ListPrice
	err := r.pool.QueryRow(ctx,
		`SELECT product_id::text, list_price_cents, price_uom::text FROM pricing_list_prices WHERE product_id = $1`,
		productID).Scan(&lp.ProductID, &lp.ListPriceCents, &lp.PriceUOM)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &lp, nil
}

func (r *Repository) CategoryPolicy(ctx context.Context, categoryID string) (*CategoryPolicy, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if categoryID == "" {
		return nil, nil
	}
	var p CategoryPolicy
	err := r.pool.QueryRow(ctx,
		`SELECT category_id::text, margin_target_bps, margin_floor_bps, margin_enforcement_disabled
		   FROM pricing_category_policy WHERE category_id = $1`,
		categoryID).Scan(&p.CategoryID, &p.MarginTargetBps, &p.MarginFloorBps, &p.EnforcementDisabled)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

func (r *Repository) ProductPolicy(ctx context.Context, productID string) (*ProductPolicy, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	var p ProductPolicy
	err := r.pool.QueryRow(ctx,
		`SELECT product_id::text, margin_enforcement_disabled FROM pricing_product_policy WHERE product_id = $1`,
		productID).Scan(&p.ProductID, &p.EnforcementDisabled)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

func (r *Repository) AppendAudit(ctx context.Context, e AuditEntry) error {
	if r.pool == nil {
		return errNoDB
	}
	// DIB-402: normalise the drill-through reference before it reaches the DB. A half-set pair
	// (type without id, or id without type) would violate pricing_audit_ref_pair and turn an
	// audit APPEND, which every caller fires-and-forgets with `_ =`, into a silent data loss.
	// Dropping the half-pair here writes the row with no target, which the viewer renders as
	// plain unlinked text: the row survives, and nothing pretends to know where it points.
	refType, refID := normaliseAuditRef(e.RefEntityType, e.RefEntityID)
	// W4-A (0114): the same drop-don't-fail discipline for bypass_reason. Two CHECKs guard the
	// column, the value must be in the known set AND the row must be a BYPASS, and either one
	// firing would lose the whole audit row through the discarded `_ =`. A reason that fails either
	// test is dropped and the row is written without it.
	reason := e.BypassReason
	if !validBypassReasons[reason] || e.Action != "BYPASS" {
		reason = ""
	}
	_, err := r.pool.Exec(ctx,
		`INSERT INTO pricing_audit_log (entity_type, entity_id, action, field_name, old_value, new_value, user_id,
		                                ref_entity_type, ref_entity_id, bypass_reason)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		e.EntityType, nullStr(e.EntityID), e.Action, nullStr(e.FieldName), nullStr(e.OldValue), nullStr(e.NewValue), nullStr(e.UserID),
		nullStr(refType), nullStr(refID), nullStr(reason))
	return err
}

func (r *Repository) CreateRule(ctx context.Context, rule Rule) (Rule, error) {
	if r.pool == nil {
		return Rule{}, errNoDB
	}
	q := `INSERT INTO pricing_rules
	        (scope, customer_id, customer_type, category_id, product_id, branch_id,
	         calc_type, price_cents, discount_bps, markup_bps, price_uom, priority,
	         valid_from, valid_to, detail_text, is_active)
	      VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
	      RETURNING id::text, version`
	err := r.pool.QueryRow(ctx, q,
		rule.Scope, nullStr(rule.CustomerID), nullStr(rule.CustomerType), nullStr(rule.CategoryID),
		nullStr(rule.ProductID), nullStr(rule.BranchID), rule.CalcType, rule.PriceCents, rule.DiscountBps,
		rule.MarkupBps, rule.PriceUOM, rule.Priority, rule.ValidFrom, rule.ValidTo, rule.DetailText, rule.IsActive,
	).Scan(&rule.ID, &rule.Version)
	if err != nil {
		if isUniqueViolation(err) {
			return Rule{}, errRuleCollision
		}
		if isFKViolation(err) {
			return Rule{}, errFKViolation // nonexistent customer/product/category/branch id → 422
		}
		return Rule{}, fmt.Errorf("create rule: %w", err)
	}
	return rule, nil
}

func (r *Repository) GetRule(ctx context.Context, id string) (Rule, error) {
	if r.pool == nil {
		return Rule{}, errNoDB
	}
	rule, err := scanRule(r.pool.QueryRow(ctx, `SELECT `+ruleCols+` FROM pricing_rules WHERE id = $1`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Rule{}, errNotFound
		}
		return Rule{}, err
	}
	return rule, nil
}

// SaveRule persists a full rule row (the service computes the new version + diff) under an ATOMIC
// optimistic-lock guard: the UPDATE matches only the row whose version still equals expectedVersion
// (finding #3). On 0 rows affected it distinguishes a version mismatch (errConflict → 409) from a
// missing row (errNotFound → 404). It writes the scope dims too, so the editor can re-key a rule
// (R-3 extended patch); a uq_pricing_rules_active collision surfaces as errRuleCollision (→409) and
// a nonexistent FK target as errFKViolation (→422, finding #10).
func (r *Repository) SaveRule(ctx context.Context, rule Rule, expectedVersion int64) (Rule, error) {
	if r.pool == nil {
		return Rule{}, errNoDB
	}
	q := `UPDATE pricing_rules SET
	        scope=$2, customer_id=$3, customer_type=$4, category_id=$5, product_id=$6, branch_id=$7,
	        calc_type=$8, price_cents=$9, discount_bps=$10, markup_bps=$11, price_uom=$12,
	        priority=$13, valid_from=$14, valid_to=$15, detail_text=$16, is_active=$17,
	        version=$18, updated_at=now()
	      WHERE id=$1 AND version=$19`
	tag, err := r.pool.Exec(ctx, q, rule.ID, rule.Scope, nullStr(rule.CustomerID), nullStr(rule.CustomerType),
		nullStr(rule.CategoryID), nullStr(rule.ProductID), nullStr(rule.BranchID), rule.CalcType,
		rule.PriceCents, rule.DiscountBps, rule.MarkupBps, rule.PriceUOM, rule.Priority,
		rule.ValidFrom, rule.ValidTo, rule.DetailText, rule.IsActive, rule.Version, expectedVersion)
	if err != nil {
		if isUniqueViolation(err) {
			return Rule{}, errRuleCollision
		}
		if isFKViolation(err) {
			return Rule{}, errFKViolation
		}
		return Rule{}, fmt.Errorf("save rule: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return Rule{}, r.ruleConflictOrMissing(ctx, rule.ID)
	}
	return rule, nil
}

// ruleConflictOrMissing distinguishes a version mismatch (errConflict → 409) from a genuinely
// absent row (errNotFound → 404) after a guarded UPDATE/DELETE matched 0 rows. The id still
// existing means the version moved on under us; otherwise the row is gone.
func (r *Repository) ruleConflictOrMissing(ctx context.Context, id string) error {
	var exists bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pricing_rules WHERE id=$1)`, id).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return errConflict
	}
	return errNotFound
}

func (r *Repository) ListRules(ctx context.Context, f RuleFilter) ([]Rule, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	q := `SELECT ` + ruleCols + ` FROM pricing_rules
	       WHERE is_active
	         AND ($1 = '' OR customer_type = $1)
	         AND ($2 = '' OR category_id = $2::uuid)
	         AND ($3 = '' OR scope = $3)
	       ORDER BY priority DESC, id ASC`
	rows, err := r.pool.Query(ctx, q, f.CustomerType, f.CategoryID, f.Scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Rule
	for rows.Next() {
		rule, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, rows.Err()
}

// - category margin-policy CRUD (US-P01/02/03) -

func (r *Repository) ListCategoryPolicies(ctx context.Context) ([]CategoryPolicy, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT category_id::text, margin_target_bps, margin_floor_bps, margin_enforcement_disabled
		   FROM pricing_category_policy ORDER BY category_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CategoryPolicy
	for rows.Next() {
		var p CategoryPolicy
		if err := rows.Scan(&p.CategoryID, &p.MarginTargetBps, &p.MarginFloorBps, &p.EnforcementDisabled); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UpsertCategoryPolicy inserts or updates the per-category policy row. The category_id FK
// (→ product_categories) is a read-only constraint, pricing never writes the catalog.
func (r *Repository) UpsertCategoryPolicy(ctx context.Context, p CategoryPolicy) (CategoryPolicy, error) {
	if r.pool == nil {
		return CategoryPolicy{}, errNoDB
	}
	q := `INSERT INTO pricing_category_policy
	        (category_id, margin_target_bps, margin_floor_bps, margin_enforcement_disabled)
	      VALUES ($1::uuid, $2, $3, $4)
	      ON CONFLICT (category_id) DO UPDATE SET
	        margin_target_bps = EXCLUDED.margin_target_bps,
	        margin_floor_bps  = EXCLUDED.margin_floor_bps,
	        margin_enforcement_disabled = EXCLUDED.margin_enforcement_disabled,
	        updated_at = now()
	      RETURNING category_id::text, margin_target_bps, margin_floor_bps, margin_enforcement_disabled`
	var out CategoryPolicy
	if err := r.pool.QueryRow(ctx, q, p.CategoryID, p.MarginTargetBps, p.MarginFloorBps, p.EnforcementDisabled).
		Scan(&out.CategoryID, &out.MarginTargetBps, &out.MarginFloorBps, &out.EnforcementDisabled); err != nil {
		if isFKViolation(err) {
			return CategoryPolicy{}, errFKViolation // policy for a nonexistent category → 422 (finding #10)
		}
		return CategoryPolicy{}, fmt.Errorf("upsert category policy: %w", err)
	}
	return out, nil
}

// - customer override CRUD (US-P09) -

const overrideCols = `id::text, customer_id::text, category_id::text, calc_type, price_cents,
	discount_bps, markup_bps, price_uom::text, notes, version`

func scanOverride(row pgx.Row) (Override, error) {
	var o Override
	if err := row.Scan(&o.ID, &o.CustomerID, &o.CategoryID, &o.CalcType, &o.PriceCents,
		&o.DiscountBps, &o.MarkupBps, &o.PriceUOM, &o.Notes, &o.Version); err != nil {
		return Override{}, err
	}
	return o, nil
}

func (r *Repository) ListOverrides(ctx context.Context, customerID string) ([]Override, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+overrideCols+` FROM customer_category_overrides WHERE customer_id = $1::uuid ORDER BY category_id`,
		customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Override
	for rows.Next() {
		o, err := scanOverride(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// GetOverride is keyed by ROW id (the optimistic-version edit path), distinct from
// OverrideFor(customer,category) the resolver uses.
func (r *Repository) GetOverride(ctx context.Context, id string) (Override, error) {
	if r.pool == nil {
		return Override{}, errNoDB
	}
	o, err := scanOverride(r.pool.QueryRow(ctx, `SELECT `+overrideCols+` FROM customer_category_overrides WHERE id = $1`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Override{}, errNotFound
		}
		return Override{}, err
	}
	return o, nil
}

func (r *Repository) CreateOverride(ctx context.Context, o Override) (Override, error) {
	if r.pool == nil {
		return Override{}, errNoDB
	}
	q := `INSERT INTO customer_category_overrides
	        (customer_id, category_id, calc_type, price_cents, discount_bps, markup_bps, price_uom, notes)
	      VALUES ($1::uuid,$2::uuid,$3,$4,$5,$6,$7,$8)
	      RETURNING id::text, version`
	err := r.pool.QueryRow(ctx, q, o.CustomerID, o.CategoryID, o.CalcType, o.PriceCents,
		o.DiscountBps, o.MarkupBps, o.PriceUOM, o.Notes).Scan(&o.ID, &o.Version)
	if err != nil {
		if isUniqueViolation(err) {
			return Override{}, errOverrideTaken
		}
		if isFKViolation(err) {
			return Override{}, errFKViolation // nonexistent customer/category id → 422 (finding #10)
		}
		return Override{}, fmt.Errorf("create override: %w", err)
	}
	return o, nil
}

// SaveOverride persists an override under an ATOMIC version guard (finding #3): the UPDATE matches
// only the row whose version still equals expectedVersion; 0 rows affected → errConflict (409) on a
// version mismatch, errNotFound (404) when absent.
func (r *Repository) SaveOverride(ctx context.Context, o Override, expectedVersion int64) (Override, error) {
	if r.pool == nil {
		return Override{}, errNoDB
	}
	q := `UPDATE customer_category_overrides SET
	        calc_type=$2, price_cents=$3, discount_bps=$4, markup_bps=$5, price_uom=$6,
	        notes=$7, version=$8, updated_at=now()
	      WHERE id=$1 AND version=$9`
	tag, err := r.pool.Exec(ctx, q, o.ID, o.CalcType, o.PriceCents, o.DiscountBps, o.MarkupBps,
		o.PriceUOM, o.Notes, o.Version, expectedVersion)
	if err != nil {
		if isFKViolation(err) {
			return Override{}, errFKViolation
		}
		return Override{}, fmt.Errorf("save override: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return Override{}, r.overrideConflictOrMissing(ctx, o.ID)
	}
	return o, nil
}

// DeleteOverride removes an override under an ATOMIC version guard (finding #4): the DELETE matches
// only the row whose version still equals expectedVersion; 0 rows affected → errConflict (409) on a
// stale delete, errNotFound (404) when the row is already gone.
func (r *Repository) DeleteOverride(ctx context.Context, id string, expectedVersion int64) error {
	if r.pool == nil {
		return errNoDB
	}
	tag, err := r.pool.Exec(ctx, `DELETE FROM customer_category_overrides WHERE id=$1 AND version=$2`, id, expectedVersion)
	if err != nil {
		return fmt.Errorf("delete override: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return r.overrideConflictOrMissing(ctx, id)
	}
	return nil
}

// overrideConflictOrMissing mirrors ruleConflictOrMissing for the overrides table.
func (r *Repository) overrideConflictOrMissing(ctx context.Context, id string) error {
	var exists bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM customer_category_overrides WHERE id=$1)`, id).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return errConflict
	}
	return errNotFound
}

// - quantity-break admin CRUD (A-07 Tier-B editor) -

// mapBreakErr turns the pricing-owned constraint violations into friendly typed errors: a duplicate
// code (UNIQUE) → 409, a nonexistent product/category FK → 422.
func mapBreakErr(err error) error {
	switch {
	case err == nil:
		return nil
	case isUniqueViolation(err):
		return errBreakCodeTaken
	case isFKViolation(err):
		return errFKViolation
	default:
		return fmt.Errorf("quantity break: %w", err)
	}
}

const breakHeaderCols = `id::text, code, name, product_id::text, category_id::text, threshold_uom::text, is_active, version`

func scanBreakHeader(row pgx.Row) (QuantityBreakHeader, error) {
	var h QuantityBreakHeader
	var prod, cat *string
	if err := row.Scan(&h.ID, &h.Code, &h.Name, &prod, &cat, &h.ThresholdUOM, &h.IsActive, &h.Version); err != nil {
		return QuantityBreakHeader{}, err
	}
	h.ProductID, h.CategoryID = derefStr(prod), derefStr(cat)
	return h, nil
}

func (r *Repository) breakItems(ctx context.Context, headerID string) ([]QuantityBreakItem, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id::text, from_qty, to_qty, value_type, discount_bps, discount_cents, surcharge_bps, surcharge_cents, price_uom::text
		   FROM quantity_break_items WHERE header_id = $1 ORDER BY from_qty ASC`, headerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []QuantityBreakItem{}
	for rows.Next() {
		var it QuantityBreakItem
		var fromQty *float64
		if err := rows.Scan(&it.ID, &fromQty, &it.ToQty, &it.ValueType, &it.DiscountBps,
			&it.DiscountCents, &it.SurchargeBps, &it.SurchargeCents, &it.PriceUOM); err != nil {
			return nil, err
		}
		if fromQty != nil {
			it.FromQty = *fromQty
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

func (r *Repository) ListQuantityBreakHeaders(ctx context.Context) ([]QuantityBreakHeader, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `SELECT `+breakHeaderCols+` FROM quantity_break_headers ORDER BY code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hs []QuantityBreakHeader
	for rows.Next() {
		h, err := scanBreakHeader(rows)
		if err != nil {
			return nil, err
		}
		hs = append(hs, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range hs {
		items, err := r.breakItems(ctx, hs[i].ID)
		if err != nil {
			return nil, err
		}
		hs[i].Items = items
	}
	return hs, nil
}

func (r *Repository) GetQuantityBreakHeader(ctx context.Context, id string) (QuantityBreakHeader, error) {
	if r.pool == nil {
		return QuantityBreakHeader{}, errNoDB
	}
	h, err := scanBreakHeader(r.pool.QueryRow(ctx, `SELECT `+breakHeaderCols+` FROM quantity_break_headers WHERE id = $1`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return QuantityBreakHeader{}, errNotFound
		}
		return QuantityBreakHeader{}, err
	}
	items, err := r.breakItems(ctx, id)
	if err != nil {
		return QuantityBreakHeader{}, err
	}
	h.Items = items
	return h, nil
}

func insertBreakItems(ctx context.Context, tx pgx.Tx, headerID string, items []QuantityBreakItem) error {
	for i := range items {
		it := items[i]
		if _, err := tx.Exec(ctx,
			`INSERT INTO quantity_break_items
			   (header_id, from_qty, to_qty, value_type, discount_bps, discount_cents, surcharge_bps, surcharge_cents, price_uom)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			headerID, it.FromQty, it.ToQty, it.ValueType, it.DiscountBps, it.DiscountCents,
			it.SurchargeBps, it.SurchargeCents, it.PriceUOM); err != nil {
			return err
		}
	}
	return nil
}

// CreateQuantityBreakHeader writes the header + its tiers in ONE transaction (zero-trust: pricing
// tables only). A duplicate code → errBreakCodeTaken (409); a nonexistent product/category FK →
// errFKViolation (422).
func (r *Repository) CreateQuantityBreakHeader(ctx context.Context, h QuantityBreakHeader) (QuantityBreakHeader, error) {
	if r.pool == nil {
		return QuantityBreakHeader{}, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return QuantityBreakHeader{}, err
	}
	defer tx.Rollback(ctx)
	if err := tx.QueryRow(ctx,
		`INSERT INTO quantity_break_headers (code, name, product_id, category_id, threshold_uom, is_active, version)
		 VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id::text, version`,
		h.Code, h.Name, nullStr(h.ProductID), nullStr(h.CategoryID), h.ThresholdUOM, h.IsActive, h.Version,
	).Scan(&h.ID, &h.Version); err != nil {
		return QuantityBreakHeader{}, mapBreakErr(err)
	}
	if err := insertBreakItems(ctx, tx, h.ID, h.Items); err != nil {
		return QuantityBreakHeader{}, mapBreakErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return QuantityBreakHeader{}, err
	}
	return h, nil
}

// SaveQuantityBreakHeader replaces the header + its tiers under an ATOMIC optimistic-version guard:
// the header UPDATE matches only the row whose version still equals expectedVersion (0 rows →
// errConflict / errNotFound), then the tiers are re-inserted wholesale.
func (r *Repository) SaveQuantityBreakHeader(ctx context.Context, h QuantityBreakHeader, expectedVersion int64) (QuantityBreakHeader, error) {
	if r.pool == nil {
		return QuantityBreakHeader{}, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return QuantityBreakHeader{}, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx,
		`UPDATE quantity_break_headers SET
		   code=$2, name=$3, product_id=$4, category_id=$5, threshold_uom=$6, is_active=$7, version=$8, updated_at=now()
		 WHERE id=$1 AND version=$9`,
		h.ID, h.Code, h.Name, nullStr(h.ProductID), nullStr(h.CategoryID), h.ThresholdUOM, h.IsActive, h.Version, expectedVersion)
	if err != nil {
		return QuantityBreakHeader{}, mapBreakErr(err)
	}
	if tag.RowsAffected() == 0 {
		return QuantityBreakHeader{}, r.breakConflictOrMissing(ctx, h.ID)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM quantity_break_items WHERE header_id=$1`, h.ID); err != nil {
		return QuantityBreakHeader{}, err
	}
	if err := insertBreakItems(ctx, tx, h.ID, h.Items); err != nil {
		return QuantityBreakHeader{}, mapBreakErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return QuantityBreakHeader{}, err
	}
	return h, nil
}

// DeleteQuantityBreakHeader soft-retires the header (is_active=false) under an optimistic-version
// guard (flag-don't-delete; the resolver filters is_active). The items are kept for history.
func (r *Repository) DeleteQuantityBreakHeader(ctx context.Context, id string, expectedVersion int64) error {
	if r.pool == nil {
		return errNoDB
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE quantity_break_headers SET is_active=false, version=version+1, updated_at=now() WHERE id=$1 AND version=$2`,
		id, expectedVersion)
	if err != nil {
		return fmt.Errorf("retire quantity break: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return r.breakConflictOrMissing(ctx, id)
	}
	return nil
}

func (r *Repository) breakConflictOrMissing(ctx context.Context, id string) error {
	var exists bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM quantity_break_headers WHERE id=$1)`, id).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return errConflict
	}
	return errNotFound
}

// - audit-log read (US-P12) -

func (r *Repository) ListAudit(ctx context.Context, f AuditFilter) ([]AuditLogRow, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	// DIB-402: ref_entity_type/ref_entity_id come back COALESCEd to '' rather than as pointers -
	// the pair CHECK (migration 0098) guarantees they are both-or-neither, so one empty string is
	// enough for the viewer to decide "no link" without a nullable scan target.
	q := `SELECT id::text, entity_type, entity_id::text, action,
	             COALESCE(field_name,''), COALESCE(old_value,''), COALESCE(new_value,''),
	             COALESCE(user_id::text,''), created_at,
	             COALESCE(ref_entity_type,''), COALESCE(ref_entity_id::text,''),
	             COALESCE(bypass_reason,'')
	        FROM pricing_audit_log
	       WHERE ($1 = '' OR entity_type = $1)
	         AND ($2 = '' OR entity_id = $2::uuid)
	         AND ($3 = '' OR action = $3)
	       ORDER BY created_at DESC, id DESC
	       LIMIT $4`
	rows, err := r.pool.Query(ctx, q, f.EntityType, f.EntityID, f.Action, f.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditLogRow
	for rows.Next() {
		var a AuditLogRow
		if err := rows.Scan(&a.ID, &a.EntityType, &a.EntityID, &a.Action,
			&a.FieldName, &a.OldValue, &a.NewValue, &a.UserID, &a.CreatedAt,
			&a.RefEntityType, &a.RefEntityID, &a.BypassReason); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// - D-PA-3 daily low-margin scan (live-sales feed) -

// LowMarginSalesLines reads the COMMITTED sold lines in [from, to) (optionally scoped to one branch)
// for the principal's daily low-margin report (D-PA-3). This is the ONE deliberately cross-module reporting
// query in the pricing repo, it joins orders/customers/products (normally read only through the
// zero-trust catalog seam) because the report IS a live-sales feed; it never WRITES those tables.
//
// Committed-sale predicate (design #6): a line is included when its order is INVOICED/FULFILLED/CLOSED
// OR it has a COMMITTED pos_sale facet. The pos_sale branch is LOAD-BEARING for POS cash sales, the
// POS ring inserts the order at CONFIRMED and never advances orders.status (internal/pos/service.go),
// so a cash counter sale is caught ONLY by the facet, not the status set. product_branch_costs is a
// LEFT JOIN keyed on (product_id, branch_id), the SAME per-branch WAC row inventory.BranchCostCents
// reads, so avg_unit_cost_cents is NULL when the product has no cost on file at the sale's branch
// (surfaced as invalid, never coalesced to 0). Numeric qty is cast to float8 for a clean scan.
func (r *Repository) LowMarginSalesLines(ctx context.Context, from, to time.Time, branchID string) ([]LowMarginSaleLine, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	const q = `
	SELECT ol.id::text, o.id::text, o.order_no::text, o.created_at, o.branch_id::text,
	       o.customer_id::text, c.customer_type,
	       ol.product_id::text, COALESCE(p.category_id::text, ''), p.sku::text, p.name,
	       ol.qty_entered::float8, ol.uom_entered::text, ol.unit_price_cents, ol.price_uom::text,
	       pbc.avg_unit_cost_cents
	  FROM order_lines ol
	  JOIN orders o    ON o.id = ol.order_id
	  JOIN customers c ON c.id = o.customer_id
	  JOIN products p  ON p.id = ol.product_id
	  LEFT JOIN product_branch_costs pbc
	         ON pbc.product_id = ol.product_id AND pbc.branch_id = o.branch_id
	 WHERE ol.product_id IS NOT NULL
	   AND o.created_at >= $1 AND o.created_at < $2
	   AND ($3::uuid IS NULL OR o.branch_id = $3::uuid)
	   AND ( o.status IN ('INVOICED','FULFILLED','CLOSED')
	         OR EXISTS (SELECT 1 FROM pos_sale ps WHERE ps.order_id = o.id AND ps.status = 'COMMITTED') )
	 ORDER BY o.created_at DESC, ol.id`
	rows, err := r.pool.Query(ctx, q, from, to, nullStr(branchID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LowMarginSaleLine
	for rows.Next() {
		var ln LowMarginSaleLine
		if err := rows.Scan(
			&ln.OrderLineID, &ln.OrderID, &ln.OrderNo, &ln.SoldAt, &ln.BranchID,
			&ln.CustomerID, &ln.CustomerType, &ln.ProductID, &ln.CategoryID, &ln.SKU, &ln.ProductName,
			&ln.QtyEntered, &ln.UOMEntered, &ln.UnitPriceCents, &ln.PriceUOM, &ln.AvgUnitCostCents,
		); err != nil {
			return nil, err
		}
		out = append(out, ln)
	}
	return out, rows.Err()
}
