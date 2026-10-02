package reporting

// commission_repo.go, the SQL half of DIB-96. Same discipline as catalog.go: fixed statements, bound
// parameters, no interpolation. The attribution query is the interesting one and is commented where it
// makes a decision.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

// -- rules --------------------------------------------------------------------

// ListCommissionRules reads rules, optionally for one rep, newest scope first, held to the caller's
// reach. The branch predicate keeps a rule with NO branch visible to a scoped caller on purpose: an
// empty branch is chooseRule's wildcard, in force at every branch including the caller's, so it is
// part of the arithmetic their own fenced preview performs. A rule naming a branch outside the reach
// is that branch's pay decision and does not surface.
func (r *Repository) ListCommissionRules(ctx context.Context, repUserID string, reach branchReach) ([]CommissionRule, error) {
	if r.pool == nil {
		return nil, ErrNoDatabase
	}
	rows, err := r.pool.Query(ctx, `
SELECT cr.id::text, cr.rep_user_id::text, COALESCE(u.display_name,''), cr.basis, cr.rate_bps,
       COALESCE(cr.category_id::text,''), COALESCE(pc.code::text,''),
       COALESCE(cr.branch_id::text,''),
       cr.effective_from, cr.effective_to, cr.is_active, cr.note, cr.created_at
  FROM commission_rules cr
  LEFT JOIN users u ON u.id = cr.rep_user_id
  LEFT JOIN product_categories pc ON pc.id = cr.category_id
 WHERE ($1::uuid IS NULL OR cr.rep_user_id = $1::uuid)
   AND ($2::uuid[] IS NULL OR cr.branch_id IS NULL OR cr.branch_id = ANY($2::uuid[]))
 ORDER BY u.display_name, cr.effective_from DESC`, nullUUID(repUserID), reach.filter())
	if err != nil {
		return nil, fmt.Errorf("list commission rules: %w", err)
	}
	defer rows.Close()
	var out []CommissionRule
	for rows.Next() {
		var c CommissionRule
		var from time.Time
		var to *time.Time
		if err := rows.Scan(&c.ID, &c.RepUserID, &c.RepName, &c.Basis, &c.RateBps,
			&c.CategoryID, &c.CategoryCode, &c.BranchID, &from, &to,
			&c.IsActive, &c.Note, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan commission rule: %w", err)
		}
		c.effFrom = from
		c.effTo = to
		c.EffectiveFrom = from.Format(DateLayout)
		if to != nil {
			c.EffectiveTo = to.Format(DateLayout)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// InsertCommissionRule creates a rule.
func (r *Repository) InsertCommissionRule(ctx context.Context, c CommissionRule, createdBy string) (CommissionRule, error) {
	if r.pool == nil {
		return CommissionRule{}, ErrNoDatabase
	}
	out := c
	err := r.pool.QueryRow(ctx, `
INSERT INTO commission_rules
  (rep_user_id, basis, rate_bps, category_id, branch_id, effective_from, effective_to, note, created_by)
VALUES ($1::uuid, $2, $3, NULLIF($4,'')::uuid, NULLIF($5,'')::uuid, $6::date,
        NULLIF($7,'')::date, $8, NULLIF($9,'')::uuid)
RETURNING id::text, is_active, created_at`,
		c.RepUserID, c.Basis, c.RateBps, c.CategoryID, c.BranchID,
		c.EffectiveFrom, c.EffectiveTo, c.Note, createdBy).
		Scan(&out.ID, &out.IsActive, &out.CreatedAt)
	if err != nil {
		return CommissionRule{}, fmt.Errorf("insert commission rule: %w", err)
	}
	return out, nil
}

// RetireCommissionRule deactivates a rule. It is NOT deleted: a run computed under it carries its id,
// and the drill-through has to keep resolving. Deactivation also releases the partial unique index so a
// replacement rule with the same scope and start date can be written.
func (r *Repository) RetireCommissionRule(ctx context.Context, id string) error {
	if r.pool == nil {
		return ErrNoDatabase
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE commission_rules SET is_active = FALSE, updated_at = now() WHERE id = $1::uuid AND is_active`, id)
	if err != nil {
		return fmt.Errorf("retire commission rule: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// -- attribution --------------------------------------------------------------

// attributionSQL reads every invoice LINE in the window with its resolved rep and its cost.
//
// THE RESOLUTION ORDER IS THE W5-D RULING, IN SQL. COALESCE(order-override, customer-default) is
// exactly "order override wins, then customer-level, then nothing" from 0136. It is joined rather than
// called per row because a commission run touches thousands of lines; the seam-conformance test
// (TestIntegration_SalesByRep_AgreesWithRepAssignmentSeam) is what keeps this honest.
//
// COST is the branch weighted-average from v_product_weighted_avg_cost, the same source every margin
// template uses, so a rep's margin-basis commission and the margin report agree. A line for a product
// with no receipts has no weighted average and costs 0, which makes it 100% margin; that is visible in
// the drill-through rather than hidden, and it is the honest consequence of a system that does not
// carry cost-at-time-of-sale.
const attributionSQL = `
SELECT il.id::text                                                    AS invoice_line_id,
       i.id::text                                                     AS invoice_id,
       i.invoice_no::text                                             AS invoice_no,
       i.issue_date::text                                             AS invoice_date,
       COALESCE(cu.name,'')                                           AS customer_name,
       COALESCE(p.sku::text,'')                                       AS product_sku,
       COALESCE(p.category_id::text,'')                               AS category_id,
       COALESCE(pc.code::text,'')                                     AS category_code,
       i.branch_id::text                                              AS branch_id,
       il.qty_base::float8                                            AS qty_base,
       il.line_total_cents                                            AS revenue_cents,
       round(COALESCE(w.avg_unit_cost_cents,0) * il.qty_base)::bigint AS cost_cents,
       COALESCE(ovr.rep_user_id, dflt.rep_user_id)::text               AS rep_user_id
  FROM invoice_lines il
  JOIN invoices i        ON i.id = il.invoice_id
  LEFT JOIN customers cu ON cu.id = i.customer_id
  LEFT JOIN products p   ON p.id = il.product_id
  LEFT JOIN product_categories pc ON pc.id = p.category_id
  LEFT JOIN v_product_weighted_avg_cost w
         ON w.product_id = il.product_id AND w.branch_id = i.branch_id
  LEFT JOIN rep_assignments ovr  ON ovr.order_id = i.order_id
  LEFT JOIN rep_assignments dflt ON dflt.customer_id = i.customer_id AND dflt.order_id IS NULL
 WHERE i.status NOT IN ('DRAFT','VOID')
   AND i.issue_date >= $1::date
   AND i.issue_date <= $2::date
   AND ($3::uuid IS NULL OR i.branch_id = $3::uuid)
 ORDER BY i.issue_date, i.invoice_no, il.line_no`

// AttributedLines reads the window.
func (r *Repository) AttributedLines(ctx context.Context, from, to time.Time, branchID string) ([]AttributedLine, error) {
	if r.pool == nil {
		return nil, ErrNoDatabase
	}
	rows, err := r.pool.Query(ctx, attributionSQL, from, to, nullUUID(branchID))
	if err != nil {
		return nil, fmt.Errorf("read attributed lines: %w", err)
	}
	defer rows.Close()
	var out []AttributedLine
	for rows.Next() {
		var l AttributedLine
		var rep *string
		if err := rows.Scan(&l.InvoiceLineID, &l.InvoiceID, &l.InvoiceNo, &l.InvoiceDate, &l.CustomerName,
			&l.ProductSKU, &l.CategoryID, &l.CategoryCode, &l.BranchID, &l.QtyBase,
			&l.RevenueCents, &l.CostCents, &rep); err != nil {
			return nil, fmt.Errorf("scan attributed line: %w", err)
		}
		l.RepUserID = deref(rep)
		out = append(out, l)
	}
	return out, rows.Err()
}

// RepsWithSales lists (id, display name) for every rep attributed in the window, plus every rep who has
// an active rule. A rep with a rule and no sales must still appear, with a zero, because "did I sell
// nothing, or did the report lose me?" is the question a zero row answers and a missing row does not.
func (r *Repository) RepsWithSales(ctx context.Context, from, to time.Time, branchID string) (map[string]string, error) {
	if r.pool == nil {
		return nil, ErrNoDatabase
	}
	rows, err := r.pool.Query(ctx, `
SELECT u.id::text, u.display_name
  FROM users u
 WHERE u.id IN (
        SELECT COALESCE(ovr.rep_user_id, dflt.rep_user_id)
          FROM invoices i
          LEFT JOIN rep_assignments ovr  ON ovr.order_id = i.order_id
          LEFT JOIN rep_assignments dflt ON dflt.customer_id = i.customer_id AND dflt.order_id IS NULL
         WHERE i.status NOT IN ('DRAFT','VOID')
           AND i.issue_date >= $1::date AND i.issue_date <= $2::date
           AND ($3::uuid IS NULL OR i.branch_id = $3::uuid)
       )
    OR u.id IN (SELECT rep_user_id FROM commission_rules WHERE is_active)
 ORDER BY u.display_name`, from, to, nullUUID(branchID))
	if err != nil {
		return nil, fmt.Errorf("list commission reps: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("scan commission rep: %w", err)
		}
		out[id] = name
	}
	return out, rows.Err()
}

// -- runs ---------------------------------------------------------------------

// InsertCommissionRun freezes one rep's period and its drill-through in ONE transaction. A header with
// no lines, or lines with no header, would each be a report that cannot be defended.
func (r *Repository) InsertCommissionRun(ctx context.Context, p CommissionPeriod, computedBy string) (CommissionPeriod, error) {
	if r.pool == nil {
		return CommissionPeriod{}, ErrNoDatabase
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return CommissionPeriod{}, fmt.Errorf("begin commission run: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	out := p
	if err := tx.QueryRow(ctx, `
INSERT INTO commission_runs
  (rep_user_id, period_start, period_end, branch_id, sale_count, revenue_cents, cost_cents,
   margin_cents, commission_cents, status, computed_by)
VALUES ($1::uuid, $2::date, $3::date, NULLIF($4,'')::uuid, $5, $6, $7, $8, $9, 'ACCRUED', NULLIF($10,'')::uuid)
RETURNING id::text, computed_at`,
		p.RepUserID, p.PeriodStart, p.PeriodEnd, p.BranchID, p.SaleCount, p.RevenueCents,
		p.CostCents, p.MarginCents, p.CommissionCents, computedBy).
		Scan(&out.RunID, &out.ComputedAt); err != nil {
		return CommissionPeriod{}, fmt.Errorf("insert commission run: %w", err)
	}

	for _, l := range p.Lines {
		basis := l.Basis
		if basis == "" {
			// A line with no covering rule still records, at the basis it would have used had one
			// existed is unknowable, so REVENUE with a zero rate, and the reason carried in the header's
			// drill-through. The CHECK requires a value; zero rate makes the money right.
			basis = BasisRevenue
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO commission_run_lines
  (run_id, invoice_id, invoice_line_id, invoice_no, invoice_date, customer_name, product_sku,
   category_code, qty_base, revenue_cents, cost_cents, margin_cents, basis, basis_cents, rate_bps,
   commission_cents, rule_id)
VALUES ($1::uuid, NULLIF($2,'')::uuid, NULLIF($3,'')::uuid, $4, NULLIF($5,'')::date, $6, $7,
        $8, $9, $10, $11, $12, $13, $14, $15, $16, NULLIF($17,'')::uuid)`,
			out.RunID, l.InvoiceID, l.InvoiceLineID, l.InvoiceNo, l.InvoiceDate, l.CustomerName,
			l.ProductSKU, l.CategoryCode, l.QtyBase, l.RevenueCents, l.CostCents, l.MarginCentsV,
			basis, l.BasisCents, l.RateBps, l.CommissionCents, l.RuleID); err != nil {
			return CommissionPeriod{}, fmt.Errorf("insert commission run line: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return CommissionPeriod{}, fmt.Errorf("commit commission run: %w", err)
	}
	return out, nil
}

// ListCommissionRuns reads frozen periods in a window, fenced to the caller's branch reach with
// ($4::uuid[] IS NULL OR cr.branch_id = ANY($4::uuid[])). A consolidated period (branch NULL) never
// matches ANY(), the same global-only ruling the single read makes.
func (r *Repository) ListCommissionRuns(ctx context.Context, from, to time.Time, repUserID string, reach branchReach) ([]CommissionPeriod, error) {
	if r.pool == nil {
		return nil, ErrNoDatabase
	}
	rows, err := r.pool.Query(ctx, `
SELECT cr.id::text, cr.rep_user_id::text, COALESCE(u.display_name,''), cr.period_start, cr.period_end,
       COALESCE(cr.branch_id::text,''), cr.sale_count, cr.revenue_cents, cr.cost_cents,
       cr.margin_cents, cr.commission_cents, cr.status, cr.computed_at
  FROM commission_runs cr
  LEFT JOIN users u ON u.id = cr.rep_user_id
 WHERE cr.period_start >= $1::date AND cr.period_end <= $2::date
   AND ($3::uuid IS NULL OR cr.rep_user_id = $3::uuid)
   AND ($4::uuid[] IS NULL OR cr.branch_id = ANY($4::uuid[]))
 ORDER BY cr.period_start DESC, u.display_name`, from, to, nullUUID(repUserID), reach.filter())
	if err != nil {
		return nil, fmt.Errorf("list commission runs: %w", err)
	}
	defer rows.Close()
	var out []CommissionPeriod
	for rows.Next() {
		var p CommissionPeriod
		var start, end time.Time
		if err := rows.Scan(&p.RunID, &p.RepUserID, &p.RepName, &start, &end, &p.BranchID,
			&p.SaleCount, &p.RevenueCents, &p.CostCents, &p.MarginCents, &p.CommissionCents,
			&p.Status, &p.ComputedAt); err != nil {
			return nil, fmt.Errorf("scan commission run: %w", err)
		}
		p.PeriodStart = start.Format(DateLayout)
		p.PeriodEnd = end.Format(DateLayout)
		p.PostsToGL = false
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetCommissionRun reads ONE frozen period with its drill-through lines.
func (r *Repository) GetCommissionRun(ctx context.Context, id string) (CommissionPeriod, error) {
	if r.pool == nil {
		return CommissionPeriod{}, ErrNoDatabase
	}
	var p CommissionPeriod
	var start, end time.Time
	err := r.pool.QueryRow(ctx, `
SELECT cr.id::text, cr.rep_user_id::text, COALESCE(u.display_name,''), cr.period_start, cr.period_end,
       COALESCE(cr.branch_id::text,''), cr.sale_count, cr.revenue_cents, cr.cost_cents,
       cr.margin_cents, cr.commission_cents, cr.status, cr.computed_at
  FROM commission_runs cr
  LEFT JOIN users u ON u.id = cr.rep_user_id
 WHERE cr.id = $1::uuid`, id).
		Scan(&p.RunID, &p.RepUserID, &p.RepName, &start, &end, &p.BranchID, &p.SaleCount,
			&p.RevenueCents, &p.CostCents, &p.MarginCents, &p.CommissionCents, &p.Status, &p.ComputedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return CommissionPeriod{}, ErrNotFound
	}
	if err != nil {
		return CommissionPeriod{}, fmt.Errorf("get commission run: %w", err)
	}
	p.PeriodStart = start.Format(DateLayout)
	p.PeriodEnd = end.Format(DateLayout)
	p.PostsToGL = false

	rows, err := r.pool.Query(ctx, `
SELECT COALESCE(invoice_id::text,''), COALESCE(invoice_line_id::text,''), invoice_no,
       COALESCE(invoice_date::text,''), customer_name, product_sku, category_code,
       qty_base::float8, revenue_cents, cost_cents, margin_cents, basis, basis_cents, rate_bps,
       commission_cents, COALESCE(rule_id::text,'')
  FROM commission_run_lines
 WHERE run_id = $1::uuid
 ORDER BY invoice_date, invoice_no`, id)
	if err != nil {
		return CommissionPeriod{}, fmt.Errorf("get commission run lines: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var l CommissionLine
		if err := rows.Scan(&l.InvoiceID, &l.InvoiceLineID, &l.InvoiceNo, &l.InvoiceDate, &l.CustomerName,
			&l.ProductSKU, &l.CategoryCode, &l.QtyBase, &l.RevenueCents, &l.CostCents, &l.MarginCentsV,
			&l.Basis, &l.BasisCents, &l.RateBps, &l.CommissionCents, &l.RuleID); err != nil {
			return CommissionPeriod{}, fmt.Errorf("scan commission run line: %w", err)
		}
		l.CommissionRunID = id
		p.Lines = append(p.Lines, l)
	}
	return p, rows.Err()
}

// AccrualRegister sums frozen, unposted runs per rep in a window, fenced to the caller's branch
// reach: a scoped caller's register owes their branches' periods alone, and a consolidated period
// (branch NULL) never matches ANY(), it is the whole company's pay and answers to a global caller.
func (r *Repository) AccrualRegister(ctx context.Context, from, to time.Time, reach branchReach) ([]AccrualLine, error) {
	if r.pool == nil {
		return nil, ErrNoDatabase
	}
	rows, err := r.pool.Query(ctx, `
SELECT cr.rep_user_id::text, COALESCE(u.display_name,''), count(*)::int, sum(cr.commission_cents)::bigint
  FROM commission_runs cr
  LEFT JOIN users u ON u.id = cr.rep_user_id
 WHERE cr.period_start >= $1::date AND cr.period_end <= $2::date
   AND cr.gl_journal_id IS NULL
   AND ($3::uuid[] IS NULL OR cr.branch_id = ANY($3::uuid[]))
 GROUP BY 1, 2
 ORDER BY 4 DESC, 2`, from, to, reach.filter())
	if err != nil {
		return nil, fmt.Errorf("commission accrual register: %w", err)
	}
	defer rows.Close()
	var out []AccrualLine
	for rows.Next() {
		var a AccrualLine
		if err := rows.Scan(&a.RepUserID, &a.RepName, &a.PeriodCount, &a.CommissionCents); err != nil {
			return nil, fmt.Errorf("scan accrual line: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CommissionArtefactScopes reads the branch scope of a COMMISSION artefact's data: the branches of
// every frozen period matching the (rep, window) linkage the artefact row carries in params. A
// period with no branch comes back as "" and reads as consolidated. The artefact row names no period
// id (params carries rep_user_id, period_start and period_end, nothing else), so when the same
// window was frozen more than once the artefact could mean any of the matches and the caller must
// treat the set as a whole; no match at all returns the empty set, which the caller also reads as
// consolidated, global only, fail closed.
func (r *Repository) CommissionArtefactScopes(ctx context.Context, repUserID, periodStart, periodEnd string) ([]string, error) {
	if r.pool == nil {
		return nil, ErrNoDatabase
	}
	rows, err := r.pool.Query(ctx, `
SELECT DISTINCT COALESCE(cr.branch_id::text, '')
  FROM commission_runs cr
 WHERE cr.rep_user_id = NULLIF($1, '')::uuid
   AND cr.period_start = $2::date
   AND cr.period_end = $3::date`, repUserID, periodStart, periodEnd)
	if err != nil {
		return nil, fmt.Errorf("commission artefact scope: %w", err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	var out []string
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			return nil, fmt.Errorf("scan commission artefact scope: %w", err)
		}
		if !seen[b] {
			seen[b] = true
			out = append(out, b)
		}
	}
	// Sorted so the scope set, and any assertion on it, does not depend on row order.
	sort.Strings(out)
	return out, rows.Err()
}

// nullUUID turns "" into a typed SQL NULL so an optional uuid filter binds without the query text
// changing. The same idiom the catalog uses; it is here so commission's ad-hoc reads share it.
func nullUUID(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// InvoiceParties reads the customer and order an invoice hangs off. Used ONLY by AttributionCheck, so
// the seam is asked the same question the report's join answered, with the same inputs.
func (r *Repository) InvoiceParties(ctx context.Context, invoiceID string) (customerID, orderID string, err error) {
	if r.pool == nil {
		return "", "", ErrNoDatabase
	}
	var order *string
	err = r.pool.QueryRow(ctx,
		`SELECT customer_id::text, order_id::text FROM invoices WHERE id = $1::uuid`, invoiceID).
		Scan(&customerID, &order)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("read invoice parties: %w", err)
	}
	return customerID, deref(order), nil
}
