package quote

// builder_repository.go, DIB-506 pgx persistence: quote_policy (0173, branch-over-NULL), the
// terms guarded write (with the flat-HST stamp), the per-line discount guarded write, and the
// margin-glance raw read (per-branch WAC join, THE engine's cost source, the row pos/rev8 reads).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// splitQuoteChips/joinQuoteChips, the 0170 comma-joined column convention.
func splitQuoteChips(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func joinQuoteChips(set []string) string { return strings.Join(set, ",") }

// ReadQuotePolicy resolves the effective quote policy: the branch row when present, else the
// dealer NULL row (0043 idiom; 0173 seeds the NULL row).
func (r *Repository) ReadQuotePolicy(ctx context.Context, branchID string) (QuotePolicy, error) {
	if r.pool == nil {
		return QuotePolicy{}, errNoDB
	}
	const q = `
SELECT COALESCE(branch_id::text,''), cost_visibility, default_validity_days, default_deposit_pct,
       line_discounts_enabled, send_email_enabled, send_sms_enabled, include_product_images,
       quick_chip_set, workspace_split_pct, workspace_arrangement
  FROM quote_policy
 WHERE ($1 <> '' AND branch_id::text = $1) OR branch_id IS NULL
 ORDER BY branch_id::text NULLS LAST
 LIMIT 1`
	var p QuotePolicy
	var chips string
	err := r.pool.QueryRow(ctx, q, branchID).Scan(&p.BranchID, &p.CostVisibility,
		&p.DefaultValidityDays, &p.DefaultDepositPct, &p.LineDiscountsEnabled,
		&p.SendEmailEnabled, &p.SendSMSEnabled, &p.IncludeProductImages,
		&chips, &p.WorkspaceSplitPct, &p.WorkspaceArrangement)
	if errors.Is(err, pgx.ErrNoRows) {
		return QuotePolicy{}, fmt.Errorf("quote: quote_policy default row missing (0173 seed)")
	}
	p.QuickChipSet = splitQuoteChips(chips)
	return p, err
}

// UpsertQuotePolicy writes the dealer NULL row (BranchID "") or a branch override row.
func (r *Repository) UpsertQuotePolicy(ctx context.Context, p QuotePolicy) error {
	if r.pool == nil {
		return errNoDB
	}
	if p.BranchID == "" {
		_, err := r.pool.Exec(ctx, `
			UPDATE quote_policy SET cost_visibility = $1, default_validity_days = $2,
			       default_deposit_pct = $3, line_discounts_enabled = $4, send_email_enabled = $5,
			       send_sms_enabled = $6, include_product_images = $7, quick_chip_set = $8,
			       workspace_split_pct = $9, workspace_arrangement = $10, updated_at = now()
			 WHERE branch_id IS NULL`,
			p.CostVisibility, p.DefaultValidityDays, p.DefaultDepositPct, p.LineDiscountsEnabled,
			p.SendEmailEnabled, p.SendSMSEnabled, p.IncludeProductImages, joinQuoteChips(p.QuickChipSet),
			p.WorkspaceSplitPct, p.WorkspaceArrangement)
		return err
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO quote_policy (branch_id, cost_visibility, default_validity_days, default_deposit_pct,
		        line_discounts_enabled, send_email_enabled, send_sms_enabled, include_product_images,
		        quick_chip_set, workspace_split_pct, workspace_arrangement)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (branch_id) DO UPDATE SET
		    cost_visibility = EXCLUDED.cost_visibility,
		    default_validity_days = EXCLUDED.default_validity_days,
		    default_deposit_pct = EXCLUDED.default_deposit_pct,
		    line_discounts_enabled = EXCLUDED.line_discounts_enabled,
		    send_email_enabled = EXCLUDED.send_email_enabled,
		    send_sms_enabled = EXCLUDED.send_sms_enabled,
		    include_product_images = EXCLUDED.include_product_images,
		    quick_chip_set = EXCLUDED.quick_chip_set,
		    workspace_split_pct = EXCLUDED.workspace_split_pct,
		    workspace_arrangement = EXCLUDED.workspace_arrangement,
		    updated_at = now()`,
		p.BranchID, p.CostVisibility, p.DefaultValidityDays, p.DefaultDepositPct,
		p.LineDiscountsEnabled, p.SendEmailEnabled, p.SendSMSEnabled, p.IncludeProductImages,
		joinQuoteChips(p.QuickChipSet), p.WorkspaceSplitPct, p.WorkspaceArrangement)
	return err
}

// ReadHSTRateBps reads the dealer's flat HST rate from system_settings (fallback 1300, the
// billing/pos FlatHSTAssessor constant).
func (r *Repository) ReadHSTRateBps(ctx context.Context) int64 {
	if r.pool == nil {
		return 1300
	}
	var raw string
	if err := r.pool.QueryRow(ctx,
		`SELECT value::text FROM system_settings WHERE key = 'hst_rate_bps'`).Scan(&raw); err != nil {
		return 1300
	}
	n, err := strconv.ParseInt(strings.Trim(raw, `"`), 10, 64)
	if err != nil || n < 0 || n > 10000 {
		return 1300
	}
	return n
}

// TopCatalogueApplications derives the un-curated chip default: the most common product
// applications in the active catalogue (honest data-driven default until the dealer curates).
func (r *Repository) TopCatalogueApplications(ctx context.Context, limit int) ([]string, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if limit <= 0 || limit > 12 {
		limit = 6
	}
	rows, err := r.pool.Query(ctx, `
		SELECT application FROM products
		 WHERE is_active AND COALESCE(application,'') <> ''
		 GROUP BY application ORDER BY COUNT(*) DESC, application LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// readTermsTx reads the terms view inside any queryable (pool or tx).
func (r *Repository) readTermsTx(ctx context.Context, q pgxQuerier, quoteID string) (QuoteTerms, error) {
	var t QuoteTerms
	var reqDate *string
	var spID, spName *string
	err := q.QueryRow(ctx, `
		SELECT q.id::text, q.deposit_required, q.deposit_pct, q.include_images, q.customer_notes,
		       q.contact_name, q.project_job, q.customer_po,
		       q.salesperson_id::text, u.display_name,
		       q.fulfillment, q.delivery_address, q.requested_date::text, q.delivery_fee_cents,
		       q.subtotal_cents, q.tax_cents, q.total_cents
		  FROM quotes q LEFT JOIN users u ON u.id = q.salesperson_id
		 WHERE q.id = $1`, quoteID).
		Scan(&t.QuoteID, &t.DepositRequired, &t.DepositPct, &t.IncludeImages, &t.CustomerNotes,
			&t.ContactName, &t.ProjectJob, &t.CustomerPO, &spID, &spName,
			&t.Fulfillment, &t.DeliveryAddress, &reqDate, &t.DeliveryFeeCents,
			&t.SubtotalCents, &t.TaxCents, &t.TotalCents)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	if err != nil {
		return t, fmt.Errorf("read quote terms: %w", err)
	}
	if reqDate != nil {
		t.RequestedDate = *reqDate
	}
	if spID != nil {
		t.SalespersonID = *spID
	}
	if spName != nil {
		t.SalespersonName = *spName
	}
	if t.DepositRequired && t.DepositPct > 0 {
		t.DepositCents = (t.TotalCents*int64(t.DepositPct) + 50) / 100
	}
	rows, err := q.Query(ctx, `
		SELECT ql.line_no, ql.disc_pct, ql.pre_discount_total_cents,
		       COALESCE(p.sku::text, ''),
		       EXISTS (SELECT 1 FROM product_images pi WHERE pi.product_id = ql.product_id AND pi.sort_order = 0)
		  FROM quote_lines ql LEFT JOIN products p ON p.id = ql.product_id
		 WHERE ql.quote_id = $1 ORDER BY ql.line_no`, quoteID)
	if err != nil {
		return t, fmt.Errorf("read quote term lines: %w", err)
	}
	defer rows.Close()
	t.Lines = []TermsLine{}
	for rows.Next() {
		var l TermsLine
		if err := rows.Scan(&l.LineNo, &l.DiscPct, &l.PreDiscountTotalCents, &l.SKU, &l.HasImage); err != nil {
			return t, fmt.Errorf("scan quote term line: %w", err)
		}
		t.Lines = append(t.Lines, l)
	}
	return t, rows.Err()
}

// pgxQuerier is the querying subset pool and tx share.
type pgxQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// ReadQuoteTerms reads the terms view for one quote.
func (r *Repository) ReadQuoteTerms(ctx context.Context, quoteID string) (QuoteTerms, error) {
	if r.pool == nil {
		return QuoteTerms{}, errNoDB
	}
	if !looksLikeUUID(quoteID) {
		return QuoteTerms{}, ErrNotFound
	}
	return r.readTermsTx(ctx, r.pool, quoteID)
}

// UpdateTermsGuarded writes the terms strip under the quote row lock (status + expiry re-checked
// inside the transaction, exactly like every line write), stamps the flat-HST tax over
// (subtotal + delivery fee), half-up ONCE at the header, the billing/pos FlatHSTAssessor
// arithmetic, and folds the money up.
func (r *Repository) UpdateTermsGuarded(ctx context.Context, quoteID string, in TermsInput, allowedFrom []string, actorID string, hstRateBps int64) (QuoteTerms, error) {
	if r.pool == nil {
		return QuoteTerms{}, errNoDB
	}
	if !looksLikeUUID(quoteID) {
		return QuoteTerms{}, ErrNotFound
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return QuoteTerms{}, fmt.Errorf("begin quote terms write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := lockQuoteForEdit(ctx, tx, quoteID, allowedFrom, r.now()); err != nil {
		return QuoteTerms{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE quotes SET
		    deposit_required = $2, deposit_pct = $3, include_images = $4, customer_notes = $5,
		    contact_name = $6, project_job = $7, customer_po = $8,
		    salesperson_id = NULLIF($9,'')::uuid,
		    fulfillment = $10, delivery_address = $11,
		    requested_date = NULLIF($12,'')::date, delivery_fee_cents = $13,
		    updated_by = NULLIF($14,'')::uuid, updated_at = now()
		 WHERE id = $1`,
		quoteID, in.DepositRequired, in.DepositPct, in.IncludeImages, in.CustomerNotes,
		in.ContactName, in.ProjectJob, in.CustomerPO, in.SalespersonID,
		in.Fulfillment, in.DeliveryAddress, in.RequestedDate, in.DeliveryFeeCents, actorID); err != nil {
		if isForeignKeyViolation(err) {
			return QuoteTerms{}, &BadTermsRefError{}
		}
		return QuoteTerms{}, fmt.Errorf("update quote terms: %w", err)
	}
	// The money fold-up: subtotal from the lines (unchanged semantics), tax stamped flat-HST over
	// subtotal + delivery, total = subtotal + delivery + tax. The tax base's SUM is cast ::bigint
	// (HH-204): SUM(bigint) returns numeric, and uncast the + 5000 half-up and the / 10000 become a
	// NUMERIC division whose fraction the bigint column rounds a second time, one cent high.
	if _, err := tx.Exec(ctx, `
		UPDATE quotes SET
		    subtotal_cents = COALESCE((SELECT SUM(line_total_cents) FROM quote_lines WHERE quote_id = $1), 0),
		    tax_cents      = ((COALESCE((SELECT SUM(line_total_cents) FROM quote_lines WHERE quote_id = $1), 0)::bigint
		                       + delivery_fee_cents) * $2 + 5000) / 10000,
		    total_cents    = COALESCE((SELECT SUM(line_total_cents) FROM quote_lines WHERE quote_id = $1), 0)
		                     + delivery_fee_cents
		                     + ((COALESCE((SELECT SUM(line_total_cents) FROM quote_lines WHERE quote_id = $1), 0)::bigint
		                         + delivery_fee_cents) * $2 + 5000) / 10000
		 WHERE id = $1`, quoteID, hstRateBps); err != nil {
		return QuoteTerms{}, fmt.Errorf("recompute quote terms money: %w", err)
	}
	out, err := r.readTermsTx(ctx, tx, quoteID)
	if err != nil {
		return QuoteTerms{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return QuoteTerms{}, fmt.Errorf("commit quote terms write: %w", err)
	}
	return out, nil
}

// isForeignKeyViolation reports a Postgres 23503 (the salesperson FK arm of the terms write).
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// BadTermsRefError, the salesperson id did not name a staff member. 422 in words at the handler.
type BadTermsRefError struct{}

func (e *BadTermsRefError) Error() string {
	return "quote: salesperson_id must name a staff member, pick one from the list"
}

// UpdateLineDiscountGuarded writes one line's discount under the quote row lock: the stored gross
// (pre_discount_total_cents, falling back to the line total for never-discounted legacy lines) is
// re-discounted to the new percent, half-up once, and the totals fold up. A floor plan carries the
// decision's verdict (floor.go): the gross it priced (a mismatch under the lock refuses the write),
// the approval consumed on this transaction, and the clearing stamped into the line's metadata.
func (r *Repository) UpdateLineDiscountGuarded(ctx context.Context, quoteID string, lineNo, discPct int, allowedFrom []string, actorID string, plan floorWritePlan) (QuoteEditResult, error) {
	if r.pool == nil {
		return QuoteEditResult{}, errNoDB
	}
	if !looksLikeUUID(quoteID) {
		return QuoteEditResult{}, ErrNotFound
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return QuoteEditResult{}, fmt.Errorf("begin quote line discount: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	status, err := lockQuoteForEdit(ctx, tx, quoteID, allowedFrom, r.now())
	if err != nil {
		return QuoteEditResult{}, err
	}
	var gross, before int64
	var beforePct int
	err = tx.QueryRow(ctx, `
		SELECT CASE WHEN pre_discount_total_cents > 0 THEN pre_discount_total_cents ELSE line_total_cents END,
		       line_total_cents, disc_pct
		  FROM quote_lines WHERE quote_id = $1 AND line_no = $2 FOR UPDATE`, quoteID, lineNo).
		Scan(&gross, &before, &beforePct)
	if errors.Is(err, pgx.ErrNoRows) {
		return QuoteEditResult{}, ErrLineNotFound
	}
	if err != nil {
		return QuoteEditResult{}, fmt.Errorf("read quote line for discount: %w", err)
	}
	if plan.expectedGross != 0 && plan.expectedGross != gross {
		// The floor decision priced a gross the lock no longer sees; refuse
		// rather than discount figures nobody decided on.
		return QuoteEditResult{}, errFloorStateChanged
	}
	if err := runFloorPlan(ctx, tx, &plan); err != nil {
		return QuoteEditResult{}, err
	}
	net := (gross*int64(100-discPct) + 50) / 100
	var after QuoteLineSnapshot
	err = tx.QueryRow(ctx, fmt.Sprintf(`
		UPDATE quote_lines SET disc_pct = $3, pre_discount_total_cents = $4, line_total_cents = $5,
		       metadata = CASE WHEN $6::jsonb IS NULL THEN metadata
		                       ELSE metadata || jsonb_build_object('floor_clear', $6::jsonb) END,
		       updated_at = now()
		 WHERE quote_id = $1 AND line_no = $2
		 RETURNING %s`, editLineColumns), quoteID, lineNo, discPct, gross, net, stampClearingJSON(plan.clearing)).
		Scan(&after.LineNo, &after.ProductID, &after.QtyEntered, &after.QtyBase,
			&after.UnitPriceCents, &after.LineTotalCents)
	if err != nil {
		return QuoteEditResult{}, fmt.Errorf("write quote line discount: %w", err)
	}
	totals, err := recomputeTotals(ctx, tx, quoteID, actorID)
	if err != nil {
		return QuoteEditResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return QuoteEditResult{}, fmt.Errorf("commit quote line discount: %w", err)
	}
	_ = beforePct
	_ = before
	return QuoteEditResult{Line: &after, Totals: totals, Status: status}, nil
}

// ReadQuoteMarginRows reads the glance's raw inputs: each line's stored revenue + base qty joined
// to the per-branch WAC row (product_branch_costs.avg_unit_cost_cents, the engine's cost source).
// A line with no WAC row answers HasCost=false, never cost 0.
func (r *Repository) ReadQuoteMarginRows(ctx context.Context, quoteID string) (string, []quoteMarginRow, error) {
	if r.pool == nil {
		return "", nil, errNoDB
	}
	if !looksLikeUUID(quoteID) {
		return "", nil, ErrNotFound
	}
	var branchID string
	if err := r.pool.QueryRow(ctx,
		`SELECT branch_id::text FROM quotes WHERE id = $1`, quoteID).Scan(&branchID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil, ErrNotFound
		}
		return "", nil, fmt.Errorf("read quote branch: %w", err)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT ql.line_no, COALESCE(ql.product_id::text,''), ql.qty_base::float8, ql.line_total_cents,
		       COALESCE(pbc.avg_unit_cost_cents, 0), pbc.avg_unit_cost_cents IS NOT NULL
		  FROM quote_lines ql
		  LEFT JOIN product_branch_costs pbc
		         ON pbc.product_id = ql.product_id AND pbc.branch_id = (SELECT branch_id FROM quotes WHERE id = $1)
		 WHERE ql.quote_id = $1 ORDER BY ql.line_no`, quoteID)
	if err != nil {
		return "", nil, fmt.Errorf("read quote margin rows: %w", err)
	}
	defer rows.Close()
	out := []quoteMarginRow{}
	for rows.Next() {
		var m quoteMarginRow
		if err := rows.Scan(&m.LineNo, &m.ProductID, &m.QtyBase, &m.LineTotalCents, &m.WACCents, &m.HasCost); err != nil {
			return "", nil, fmt.Errorf("scan quote margin row: %w", err)
		}
		out = append(out, m)
	}
	return branchID, out, rows.Err()
}

// runFloorPlan runs the plan's approval consumption on the write's
// transaction, before the mutation: an approval that cannot be consumed
// refuses the write, so a below floor price never lands on a request that
// is not standing.
func runFloorPlan(ctx context.Context, tx pgx.Tx, plan *floorWritePlan) error {
	if plan == nil || plan.consume == nil {
		return nil
	}
	_, found, err := plan.consume(ctx, tx)
	if err != nil {
		return err
	}
	if !found {
		return &FloorRefusalError{Policy: string(identity.PolicyMargin)}
	}
	return nil
}

// LineFloorState reads one line's floor facts (floor.go's seam): the line's
// OWN row id (the approval's binding; a line number is reused by the next
// line added after a delete, the row id never is), the stored discount, the
// rule unit price, the pre-discount gross, the price uom, and the line's
// stamped clearing (nil when none).
func (r *Repository) LineFloorState(ctx context.Context, quoteID string, lineNo int) (string, int, int64, int64, string, *floorClearingStamp, error) {
	if r.pool == nil {
		return "", 0, 0, 0, "", nil, errNoDB
	}
	if !looksLikeUUID(quoteID) {
		return "", 0, 0, 0, "", nil, ErrNotFound
	}
	var lineID string
	var discPct int
	var unitPrice, gross int64
	var priceUOM string
	var metadata []byte
	err := r.pool.QueryRow(ctx, `
		SELECT id::text, disc_pct, unit_price_cents, price_uom::text,
		       CASE WHEN pre_discount_total_cents > 0 THEN pre_discount_total_cents ELSE line_total_cents END,
		       metadata
		  FROM quote_lines WHERE quote_id = $1 AND line_no = $2`, quoteID, lineNo).
		Scan(&lineID, &discPct, &unitPrice, &priceUOM, &gross, &metadata)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, 0, 0, "", nil, ErrLineNotFound
	}
	if err != nil {
		return "", 0, 0, 0, "", nil, fmt.Errorf("read quote line floor state: %w", err)
	}
	var clearing *floorClearingStamp
	if len(metadata) > 0 {
		var m map[string]json.RawMessage
		if json.Unmarshal(metadata, &m) == nil {
			if raw, ok := m["floor_clear"]; ok {
				var c floorClearingStamp
				if json.Unmarshal(raw, &c) == nil {
					clearing = &c
				}
			}
		}
	}
	return lineID, discPct, unitPrice, gross, priceUOM, clearing, nil
}
