package reporting

// catalog.go, the compiled half of a report definition: ONE fixed SQL string and ONE explicit column
// allowlist per template code. This file is the security boundary of the whole module, so it is worth
// being precise about what it guarantees and what it does not.
//
// -- WHAT IS GUARANTEED HERE --------------------------------------------------------------------------
//  1. Every query below is a Go string CONSTANT in the sense that matters: it contains no format verb,
//     no concatenation, and no interpolation point. The only way a caller influences it is by binding
//     $1..$n. TestCatalog_SQLHasNoFormatVerbs and TestCatalog_SQLPlaceholdersMatchParams pin this.
//  2. Optional filters are expressed IN the SQL as null/empty checks, ($3::uuid IS NULL OR ...),
//     ($4::text = '' OR ...), so a request that omits a filter runs the SAME query text as one that
//     supplies it. There is no branch in Go that assembles a different statement, which means there is
//     no place for a branch to be assembled wrongly.
//  3. The column list projected by each query is declared alongside it and is compared against the
//     database's own field descriptions at run time (runner.go). A drifted query fails; it does not
//     return a column nobody declared.
//
// -- WHAT IS NOT GUARANTEED, STATED SO NOBODY ASSUMES IT ----------------------------------------------
// These queries read PEER TABLES. They are read-only and they take no locks beyond a snapshot, but they
// are coupled to peer schemas: a column rename in orders/invoices/purchasing breaks a report, loudly,
// at its named integration test. That coupling is deliberate and is the price of a reporting module
// that reports on the business rather than on a private copy of it. The alternative, a warehouse the
// modules push into, is a second source of truth, and a second source of truth for "what did we sell"
// is how a business ends up with two numbers and no answer.
//
// -- MONEY, AND THE ONE PLACE COST COMES FROM ---------------------------------------------------------
// Cost is v_product_weighted_avg_cost (the 0034-era view over stock_moves), per product per branch. It
// is NOT the invoice's cost at the time of sale, the system does not carry one, and every margin
// report here therefore states a CURRENT-cost margin. That is the honest description of the number
// these queries can produce today, and it is written on the report's own description text so a manager
// reading it knows which question it answers.

// catalogEntry is the compiled half: the query and its allowlist.
type catalogEntry struct {
	sql     string
	columns []Column
}

// col is a small constructor so the tables below read as tables.
func col(name, label, kind string, total bool) Column {
	return Column{Name: name, Label: label, Kind: kind, Total: total}
}

// catalog maps a report_templates.code to its compiled query. A code present in the database but
// absent here CANNOT be run (the service 404s on it); a code present here but absent from the database
// is invisible to the surface. TestCatalog_MatchesSeededTemplates pins both directions against 0143.
var catalog = map[string]catalogEntry{

	// -- SALES ----------------------------------------------------------------------------------------
	// "Sales" throughout means INVOICED sales, the revenue-recognition event, not the order. Orders are
	// intent; invoices are what was billed, and the AR ledger and the GL both agree with the invoice.
	// DRAFT and VOID are excluded everywhere for the same reason.

	"sales_by_day": {
		sql: `
SELECT i.issue_date::text                 AS day,
       count(*)::bigint                   AS invoice_count,
       sum(i.subtotal_cents)::bigint      AS subtotal_cents,
       sum(i.tax_cents)::bigint           AS tax_cents,
       sum(i.total_cents)::bigint         AS total_cents
  FROM invoices i
 WHERE i.status NOT IN ('DRAFT','VOID')
   AND i.issue_date >= $1::date
   AND i.issue_date <= $2::date
   AND ($3::uuid IS NULL OR i.branch_id = $3::uuid)
 GROUP BY i.issue_date
 ORDER BY i.issue_date`,
		columns: []Column{
			col("day", "Day", KindDate, false),
			col("invoice_count", "Invoices", KindInt, true),
			col("subtotal_cents", "Subtotal", KindCents, true),
			col("tax_cents", "Tax", KindCents, true),
			col("total_cents", "Total", KindCents, true),
		},
	},

	"sales_by_branch": {
		sql: `
SELECT l.code::text                       AS branch_code,
       l.name                             AS branch_name,
       count(*)::bigint                   AS invoice_count,
       sum(i.subtotal_cents)::bigint      AS subtotal_cents,
       sum(i.tax_cents)::bigint           AS tax_cents,
       sum(i.total_cents)::bigint         AS total_cents
  FROM invoices i
  JOIN locations l ON l.id = i.branch_id
 WHERE i.status NOT IN ('DRAFT','VOID')
   AND i.issue_date >= $1::date
   AND i.issue_date <= $2::date
 GROUP BY l.code, l.name
 ORDER BY l.code`,
		columns: []Column{
			col("branch_code", "Branch", KindText, false),
			col("branch_name", "Name", KindText, false),
			col("invoice_count", "Invoices", KindInt, true),
			col("subtotal_cents", "Subtotal", KindCents, true),
			col("tax_cents", "Tax", KindCents, true),
			col("total_cents", "Total", KindCents, true),
		},
	},

	// Rep attribution follows the W5-D ruling EXACTLY (0136): the per-order override wins, then the
	// customer-level default, then nothing, which is the house queue, shown here as an explicit
	// "unassigned" row rather than dropped. Dropping it would make the report's total disagree with
	// sales_by_day, and a reporting suite whose totals disagree with each other is worse than no suite.
	// TestIntegration_SalesByRep_AgreesWithRepAssignmentSeam pins this SQL against the owner module's
	// own resolver.
	"sales_by_rep": {
		sql: `
WITH scoped AS (
  SELECT i.id, i.order_id, i.customer_id, i.subtotal_cents, i.total_cents
    FROM invoices i
   WHERE i.status NOT IN ('DRAFT','VOID')
     AND i.issue_date >= $1::date
     AND i.issue_date <= $2::date
     AND ($3::uuid IS NULL OR i.branch_id = $3::uuid)
), attributed AS (
  SELECT s.subtotal_cents, s.total_cents,
         COALESCE(ovr.rep_user_id, dflt.rep_user_id) AS rep_user_id
    FROM scoped s
    LEFT JOIN rep_assignments ovr  ON ovr.order_id = s.order_id
    LEFT JOIN rep_assignments dflt ON dflt.customer_id = s.customer_id AND dflt.order_id IS NULL
)
SELECT COALESCE(u.display_name, 'Unassigned (house queue)') AS rep_name,
       COALESCE(a.rep_user_id::text, '')                    AS rep_user_id,
       count(*)::bigint                                     AS invoice_count,
       sum(a.subtotal_cents)::bigint                        AS subtotal_cents,
       sum(a.total_cents)::bigint                           AS total_cents
  FROM attributed a
  LEFT JOIN users u ON u.id = a.rep_user_id
 GROUP BY 1, 2
 ORDER BY 5 DESC, 1`,
		columns: []Column{
			col("rep_name", "Rep", KindText, false),
			col("rep_user_id", "Rep ID", KindText, false),
			col("invoice_count", "Invoices", KindInt, true),
			col("subtotal_cents", "Subtotal", KindCents, true),
			col("total_cents", "Total", KindCents, true),
		},
	},

	"sales_by_category": {
		sql: `
SELECT COALESCE(pc.code::text, '(uncategorised)') AS category_code,
       COALESCE(pc.name, '(uncategorised)')       AS category_name,
       count(DISTINCT i.id)::bigint               AS invoice_count,
       sum(il.qty_base)::float8                   AS qty_base,
       sum(il.line_total_cents)::bigint           AS revenue_cents
  FROM invoice_lines il
  JOIN invoices i ON i.id = il.invoice_id
  LEFT JOIN products p ON p.id = il.product_id
  LEFT JOIN product_categories pc ON pc.id = p.category_id
 WHERE i.status NOT IN ('DRAFT','VOID')
   AND i.issue_date >= $1::date
   AND i.issue_date <= $2::date
   AND ($3::uuid IS NULL OR i.branch_id = $3::uuid)
 GROUP BY 1, 2
 ORDER BY 5 DESC, 1`,
		columns: []Column{
			col("category_code", "Category", KindText, false),
			col("category_name", "Name", KindText, false),
			col("invoice_count", "Invoices", KindInt, true),
			col("qty_base", "Quantity", KindQty, true),
			col("revenue_cents", "Revenue", KindCents, true),
		},
	},

	// -- MARGIN ---------------------------------------------------------------------------------------
	// margin_bps is computed in SQL from the SUMMED revenue and cost, not averaged from per-line
	// percentages. Averaging percentages weights a $40 line the same as a $40,000 one and produces a
	// number that is wrong in the direction that flatters small sales.

	"margin_by_product": {
		sql: `
SELECT p.sku::text                                                            AS sku,
       p.name                                                                 AS product_name,
       sum(il.qty_base)::float8                                               AS qty_base,
       sum(il.line_total_cents)::bigint                                       AS revenue_cents,
       sum(round(COALESCE(w.avg_unit_cost_cents, 0) * il.qty_base))::bigint   AS cost_cents,
       (sum(il.line_total_cents)
        - sum(round(COALESCE(w.avg_unit_cost_cents, 0) * il.qty_base)))::bigint AS margin_cents,
       CASE WHEN sum(il.line_total_cents) > 0
            THEN (((sum(il.line_total_cents)
                    - sum(round(COALESCE(w.avg_unit_cost_cents, 0) * il.qty_base))) * 10000)
                  / sum(il.line_total_cents))::bigint
            ELSE 0::bigint END                                                AS margin_bps
  FROM invoice_lines il
  JOIN invoices i ON i.id = il.invoice_id
  JOIN products p ON p.id = il.product_id
  LEFT JOIN v_product_weighted_avg_cost w
         ON w.product_id = il.product_id AND w.branch_id = i.branch_id
 WHERE i.status NOT IN ('DRAFT','VOID')
   AND i.issue_date >= $1::date
   AND i.issue_date <= $2::date
   AND ($3::uuid IS NULL OR i.branch_id = $3::uuid)
 GROUP BY p.sku, p.name
 ORDER BY 6 DESC`,
		columns: []Column{
			col("sku", "SKU", KindText, false),
			col("product_name", "Product", KindText, false),
			col("qty_base", "Quantity", KindQty, true),
			col("revenue_cents", "Revenue", KindCents, true),
			col("cost_cents", "Cost", KindCents, true),
			col("margin_cents", "Margin", KindCents, true),
			col("margin_bps", "Margin bps", KindInt, false),
		},
	},

	"margin_by_category": {
		sql: `
SELECT COALESCE(pc.code::text, '(uncategorised)')                             AS category_code,
       COALESCE(pc.name, '(uncategorised)')                                   AS category_name,
       sum(il.qty_base)::float8                                               AS qty_base,
       sum(il.line_total_cents)::bigint                                       AS revenue_cents,
       sum(round(COALESCE(w.avg_unit_cost_cents, 0) * il.qty_base))::bigint   AS cost_cents,
       (sum(il.line_total_cents)
        - sum(round(COALESCE(w.avg_unit_cost_cents, 0) * il.qty_base)))::bigint AS margin_cents,
       CASE WHEN sum(il.line_total_cents) > 0
            THEN (((sum(il.line_total_cents)
                    - sum(round(COALESCE(w.avg_unit_cost_cents, 0) * il.qty_base))) * 10000)
                  / sum(il.line_total_cents))::bigint
            ELSE 0::bigint END                                                AS margin_bps
  FROM invoice_lines il
  JOIN invoices i ON i.id = il.invoice_id
  LEFT JOIN products p ON p.id = il.product_id
  LEFT JOIN product_categories pc ON pc.id = p.category_id
  LEFT JOIN v_product_weighted_avg_cost w
         ON w.product_id = il.product_id AND w.branch_id = i.branch_id
 WHERE i.status NOT IN ('DRAFT','VOID')
   AND i.issue_date >= $1::date
   AND i.issue_date <= $2::date
   AND ($3::uuid IS NULL OR i.branch_id = $3::uuid)
 GROUP BY 1, 2
 ORDER BY 6 DESC`,
		columns: []Column{
			col("category_code", "Category", KindText, false),
			col("category_name", "Name", KindText, false),
			col("qty_base", "Quantity", KindQty, true),
			col("revenue_cents", "Revenue", KindCents, true),
			col("cost_cents", "Cost", KindCents, true),
			col("margin_cents", "Margin", KindCents, true),
			col("margin_bps", "Margin bps", KindInt, false),
		},
	},

	// -- AR -------------------------------------------------------------------------------------------
	// Both aging reports read v_ar_aging, the SAME view internal/finstmt's AR aging reads. That is the
	// point: two surfaces that disagree about who owes what is the failure mode this replaces, so there
	// is one definition of a bucket and both read it.

	"ar_aging_detail": {
		sql: `
SELECT COALESCE(a.customer_name, '(unknown)') AS customer_name,
       a.source_type                          AS source_type,
       COALESCE(l.code::text, '')             AS branch_code,
       a.bucket                               AS bucket,
       a.age_days::bigint                     AS age_days,
       a.amount_cents::bigint                 AS amount_cents
  FROM v_ar_aging a
  LEFT JOIN locations l ON l.id = a.branch_id
 WHERE ($1::uuid IS NULL OR a.branch_id = $1::uuid)
   AND ($2::uuid IS NULL OR a.customer_id = $2::uuid)
 ORDER BY 1, 5 DESC`,
		columns: []Column{
			col("customer_name", "Customer", KindText, false),
			col("source_type", "Source", KindText, false),
			col("branch_code", "Branch", KindText, false),
			col("bucket", "Bucket", KindText, false),
			col("age_days", "Age (days)", KindInt, false),
			col("amount_cents", "Amount", KindCents, true),
		},
	},

	"ar_aging_summary": {
		sql: `
SELECT COALESCE(a.customer_name, '(unknown)')                                        AS customer_name,
       sum(CASE WHEN a.bucket = 'CURRENT' THEN a.amount_cents ELSE 0 END)::bigint    AS current_cents,
       sum(CASE WHEN a.bucket = '1_30'    THEN a.amount_cents ELSE 0 END)::bigint    AS days_1_30_cents,
       sum(CASE WHEN a.bucket = '31_60'   THEN a.amount_cents ELSE 0 END)::bigint    AS days_31_60_cents,
       sum(CASE WHEN a.bucket = '61_90'   THEN a.amount_cents ELSE 0 END)::bigint    AS days_61_90_cents,
       sum(CASE WHEN a.bucket = '90_PLUS' THEN a.amount_cents ELSE 0 END)::bigint    AS days_90_plus_cents,
       sum(a.amount_cents)::bigint                                                   AS total_cents
  FROM v_ar_aging a
 WHERE ($1::uuid IS NULL OR a.branch_id = $1::uuid)
   AND ($2::uuid IS NULL OR a.customer_id = $2::uuid)
 GROUP BY 1
 ORDER BY 7 DESC, 1`,
		columns: []Column{
			col("customer_name", "Customer", KindText, false),
			col("current_cents", "Current", KindCents, true),
			col("days_1_30_cents", "1-30", KindCents, true),
			col("days_31_60_cents", "31-60", KindCents, true),
			col("days_61_90_cents", "61-90", KindCents, true),
			col("days_90_plus_cents", "90+", KindCents, true),
			col("total_cents", "Total", KindCents, true),
		},
	},

	"statement_run_history": {
		sql: `
SELECT r.created_at::date::text            AS run_date,
       r.mode                              AS mode,
       COALESCE(l.code::text, '(all)')     AS branch_code,
       r.as_of_date::text                  AS as_of_date,
       r.status                            AS status,
       r.generated_count::bigint           AS generated_count,
       r.queued_count::bigint              AS queued_count,
       COALESCE(u.display_name, '')        AS requested_by
  FROM ar_statement_runs r
  LEFT JOIN locations l ON l.id = r.branch_id
  LEFT JOIN users u ON u.id = r.requested_by
 WHERE r.created_at >= $1::date
   AND r.created_at <  ($2::date + 1)
 ORDER BY r.created_at DESC`,
		columns: []Column{
			col("run_date", "Run date", KindDate, false),
			col("mode", "Mode", KindText, false),
			col("branch_code", "Branch", KindText, false),
			col("as_of_date", "As of", KindDate, false),
			col("status", "Status", KindText, false),
			col("generated_count", "Generated", KindInt, true),
			col("queued_count", "Queued", KindInt, true),
			col("requested_by", "Requested by", KindText, false),
		},
	},

	// -- INVENTORY ------------------------------------------------------------------------------------
	// stock_balances is per LOCATION; a branch has several (yard bays, the shop). Every inventory report
	// here therefore aggregates to product × branch in a subquery FIRST. Joining locations directly to
	// stock_balances would multiply a SKU by its bin count, which is the classic way an inventory
	// valuation comes out three times too high and nobody notices until the auditor does.

	"inventory_valuation": {
		sql: `
SELECT p.sku::text                                                    AS sku,
       p.name                                                         AS product_name,
       l.code::text                                                   AS branch_code,
       b.on_hand_base::float8                                         AS on_hand_base,
       COALESCE(w.avg_unit_cost_cents, 0)::bigint                     AS unit_cost_cents,
       round(b.on_hand_base * COALESCE(w.avg_unit_cost_cents, 0))::bigint AS value_cents
  FROM (SELECT product_id, branch_id, sum(on_hand_base) AS on_hand_base
          FROM stock_balances
         GROUP BY product_id, branch_id) b
  JOIN products p  ON p.id = b.product_id
  JOIN locations l ON l.id = b.branch_id
  LEFT JOIN v_product_weighted_avg_cost w
         ON w.product_id = b.product_id AND w.branch_id = b.branch_id
 WHERE ($1::uuid IS NULL OR b.branch_id = $1::uuid)
   AND b.on_hand_base <> 0
 ORDER BY p.sku, l.code`,
		columns: []Column{
			col("sku", "SKU", KindText, false),
			col("product_name", "Product", KindText, false),
			col("branch_code", "Branch", KindText, false),
			col("on_hand_base", "On hand", KindQty, true),
			col("unit_cost_cents", "Unit cost", KindCents, false),
			col("value_cents", "Value", KindCents, true),
		},
	},

	// QUANTITY HERE IS "QUANTITY MOVED", NOT A NET CHANGE. stock_moves stores a positive magnitude and
	// expresses direction through which location column is set (from_location_id for stock leaving,
	// to_location_id for stock arriving). Summing qty_base therefore totals movement VOLUME, which is
	// what a movement log is for; the net position is inventory_valuation's job. Grouping by move_type
	// keeps the two directions on separate rows, so a reader is never adding a receipt to a pick.
	"inventory_movement": {
		sql: `
SELECT sm.occurred_at::date::text            AS day,
       sm.move_type                          AS move_type,
       p.sku::text                           AS sku,
       p.name                                AS product_name,
       sum(sm.qty_base)::float8              AS qty_base,
       sum(COALESCE(sm.value_cents, 0))::bigint AS value_cents,
       count(*)::bigint                      AS move_count
  FROM stock_moves sm
  JOIN products p ON p.id = sm.product_id
 WHERE sm.occurred_at >= $1::date
   AND sm.occurred_at <  ($2::date + 1)
   AND ($3::uuid IS NULL OR sm.branch_id = $3::uuid)
   AND ($4::text = '' OR sm.move_type = $4::text)
 GROUP BY 1, 2, 3, 4
 ORDER BY 1, 3, 2`,
		columns: []Column{
			col("day", "Day", KindDate, false),
			col("move_type", "Type", KindText, false),
			col("sku", "SKU", KindText, false),
			col("product_name", "Product", KindText, false),
			col("qty_base", "Quantity", KindQty, true),
			col("value_cents", "Value", KindCents, true),
			col("move_count", "Moves", KindInt, true),
		},
	},

	"below_reorder": {
		sql: `
SELECT p.sku::text                                                  AS sku,
       p.name                                                       AS product_name,
       l.code::text                                                 AS branch_code,
       COALESCE(b.on_hand_base, 0)::float8                          AS on_hand_base,
       rp.reorder_point_base::float8                                AS reorder_point_base,
       rp.reorder_qty_base::float8                                  AS reorder_qty_base,
       (rp.reorder_point_base - COALESCE(b.on_hand_base, 0))::float8 AS shortfall_base,
       COALESCE(v.name, '')                                         AS preferred_vendor
  FROM product_reorder_points rp
  JOIN products p  ON p.id = rp.product_id
  JOIN locations l ON l.id = rp.branch_id
  LEFT JOIN (SELECT product_id, branch_id, sum(on_hand_base) AS on_hand_base
               FROM stock_balances
              GROUP BY product_id, branch_id) b
         ON b.product_id = rp.product_id AND b.branch_id = rp.branch_id
  LEFT JOIN vendors v ON v.id = rp.preferred_vendor_id
 WHERE rp.is_active
   AND ($1::uuid IS NULL OR rp.branch_id = $1::uuid)
   AND COALESCE(b.on_hand_base, 0) < rp.reorder_point_base
 ORDER BY 7 DESC, 1`,
		columns: []Column{
			col("sku", "SKU", KindText, false),
			col("product_name", "Product", KindText, false),
			col("branch_code", "Branch", KindText, false),
			col("on_hand_base", "On hand", KindQty, true),
			col("reorder_point_base", "Reorder point", KindQty, false),
			col("reorder_qty_base", "Reorder qty", KindQty, true),
			col("shortfall_base", "Shortfall", KindQty, true),
			col("preferred_vendor", "Preferred vendor", KindText, false),
		},
	},

	// -- PURCHASING -----------------------------------------------------------------------------------

	"po_status": {
		sql: `
SELECT po.po_no::text                                                          AS po_no,
       v.name                                                                  AS vendor_name,
       l.code::text                                                            AS branch_code,
       po.status                                                               AS status,
       COALESCE(po.expected_at::text, '')                                      AS expected_at,
       COALESCE(sum(pl.qty_ordered_base), 0)::float8                           AS qty_ordered_base,
       COALESCE(sum(pl.qty_received_base), 0)::float8                          AS qty_received_base,
       COALESCE(sum(round(pl.qty_ordered_base * pl.unit_cost_cents)), 0)::bigint AS ordered_cents
  FROM purchasing_pos po
  JOIN vendors v   ON v.id = po.vendor_id
  JOIN locations l ON l.id = po.branch_id
  LEFT JOIN purchasing_po_lines pl ON pl.po_id = po.id
 WHERE ($1::text = '' OR po.status = $1::text)
   AND ($2::uuid IS NULL OR po.branch_id = $2::uuid)
   AND ($3::uuid IS NULL OR po.vendor_id = $3::uuid)
 GROUP BY po.po_no, v.name, l.code, po.status, po.expected_at
 ORDER BY po.po_no`,
		columns: []Column{
			col("po_no", "PO", KindText, false),
			col("vendor_name", "Vendor", KindText, false),
			col("branch_code", "Branch", KindText, false),
			col("status", "Status", KindText, false),
			col("expected_at", "Expected", KindDate, false),
			col("qty_ordered_base", "Ordered", KindQty, true),
			col("qty_received_base", "Received", KindQty, true),
			col("ordered_cents", "Committed", KindCents, true),
		},
	},

	"receiving_history": {
		sql: `
SELECT r.received_at::date::text                     AS received_date,
       r.grn_no::text                                AS grn_no,
       po.po_no::text                                AS po_no,
       v.name                                        AS vendor_name,
       COALESCE(u.display_name, '')                  AS received_by,
       COALESCE(sum(rl.qty_base), 0)::float8         AS qty_base,
       COALESCE(sum(rl.value_cents), 0)::bigint      AS value_cents
  FROM purchasing_receipts r
  JOIN purchasing_pos po ON po.id = r.po_id
  JOIN vendors v         ON v.id = po.vendor_id
  LEFT JOIN users u      ON u.id = r.received_by
  LEFT JOIN purchasing_receipt_lines rl ON rl.receipt_id = r.id
 WHERE r.received_at >= $1::date
   AND r.received_at <  ($2::date + 1)
   AND ($3::uuid IS NULL OR po.branch_id = $3::uuid)
 GROUP BY r.id, r.received_at, r.grn_no, po.po_no, v.name, u.display_name
 ORDER BY r.received_at DESC, r.grn_no`,
		columns: []Column{
			col("received_date", "Received", KindDate, false),
			col("grn_no", "GRN", KindText, false),
			col("po_no", "PO", KindText, false),
			col("vendor_name", "Vendor", KindText, false),
			col("received_by", "Received by", KindText, false),
			col("qty_base", "Quantity", KindQty, true),
			col("value_cents", "Value", KindCents, true),
		},
	},

	// MATCHED lines are excluded on purpose: this is the exception report the AP clerk works from, and a
	// variance report that also lists everything that matched is a report nobody opens twice.
	"three_way_variance": {
		sql: `
SELECT b.bill_no::text                       AS bill_no,
       v.name                                AS vendor_name,
       COALESCE(po.po_no::text, '')          AS po_no,
       bl.line_no::bigint                    AS line_no,
       bl.description                        AS description,
       bl.match_state                        AS match_state,
       bl.qty_variance_base::float8          AS qty_variance_base,
       bl.price_variance_cents::bigint       AS price_variance_cents,
       bl.line_total_cents::bigint           AS line_total_cents
  FROM purchasing_bill_lines bl
  JOIN purchasing_bills b ON b.id = bl.bill_id
  JOIN vendors v          ON v.id = b.vendor_id
  LEFT JOIN purchasing_pos po ON po.id = b.po_id
 WHERE b.bill_date >= $1::date
   AND b.bill_date <= $2::date
   AND ($3::uuid IS NULL OR b.branch_id = $3::uuid)
   AND bl.match_state <> 'MATCHED'
 ORDER BY b.bill_no, bl.line_no`,
		columns: []Column{
			col("bill_no", "Bill", KindText, false),
			col("vendor_name", "Vendor", KindText, false),
			col("po_no", "PO", KindText, false),
			col("line_no", "Line", KindInt, false),
			col("description", "Description", KindText, false),
			col("match_state", "Match", KindText, false),
			col("qty_variance_base", "Qty variance", KindQty, true),
			col("price_variance_cents", "Price variance", KindCents, true),
			col("line_total_cents", "Line total", KindCents, true),
		},
	},

	// -- TAX ------------------------------------------------------------------------------------------
	// Tax collected reads the INVOICE's own tax_cents, not a recomputation from tax_rates. The invoice
	// is what the customer was charged and what was remitted against; recomputing would produce a
	// number that is arguably more correct and is definitely not what happened.
	"tax_collected": {
		sql: `
SELECT i.issue_date::text                AS day,
       l.code::text                      AS branch_code,
       count(*)::bigint                  AS invoice_count,
       sum(i.subtotal_cents)::bigint     AS taxable_base_cents,
       sum(i.tax_cents)::bigint          AS tax_cents
  FROM invoices i
  JOIN locations l ON l.id = i.branch_id
 WHERE i.status NOT IN ('DRAFT','VOID')
   AND i.issue_date >= $1::date
   AND i.issue_date <= $2::date
   AND ($3::uuid IS NULL OR i.branch_id = $3::uuid)
 GROUP BY 1, 2
 ORDER BY 1, 2`,
		columns: []Column{
			col("day", "Day", KindDate, false),
			col("branch_code", "Branch", KindText, false),
			col("invoice_count", "Invoices", KindInt, true),
			col("taxable_base_cents", "Taxable base", KindCents, true),
			col("tax_cents", "Tax collected", KindCents, true),
		},
	},

	// -- PAYMENTS -------------------------------------------------------------------------------------
	// RECEIVED and DEPOSITED only. A BOUNCED, REFUNDED or VOID payment is money that did not stay, and a
	// cash-receipts report that counts it is the report that makes a bank reconciliation impossible.

	"payments_by_method": {
		sql: `
SELECT p.method                                       AS method,
       count(*)::bigint                               AS payment_count,
       sum(p.amount_cents)::bigint                    AS amount_cents,
       sum(COALESCE(p.surcharge_cents, 0))::bigint    AS surcharge_cents
  FROM payments p
 WHERE p.received_at >= $1::date
   AND p.received_at <  ($2::date + 1)
   AND ($3::uuid IS NULL OR p.branch_id = $3::uuid)
   AND p.status IN ('RECEIVED','DEPOSITED')
 GROUP BY 1
 ORDER BY 3 DESC`,
		columns: []Column{
			col("method", "Method", KindText, false),
			col("payment_count", "Payments", KindInt, true),
			col("amount_cents", "Amount", KindCents, true),
			col("surcharge_cents", "Surcharge", KindCents, true),
		},
	},

	"payments_by_day": {
		sql: `
SELECT p.received_at::date::text                      AS day,
       count(*)::bigint                               AS payment_count,
       sum(p.amount_cents)::bigint                    AS amount_cents,
       sum(COALESCE(p.surcharge_cents, 0))::bigint    AS surcharge_cents
  FROM payments p
 WHERE p.received_at >= $1::date
   AND p.received_at <  ($2::date + 1)
   AND ($3::uuid IS NULL OR p.branch_id = $3::uuid)
   AND p.status IN ('RECEIVED','DEPOSITED')
 GROUP BY 1
 ORDER BY 1`,
		columns: []Column{
			col("day", "Day", KindDate, false),
			col("payment_count", "Payments", KindInt, true),
			col("amount_cents", "Amount", KindCents, true),
			col("surcharge_cents", "Surcharge", KindCents, true),
		},
	},

	// -- CREDIT ---------------------------------------------------------------------------------------
	"credit_hold_history": {
		sql: `
SELECT c.name                                        AS customer_name,
       h.hold_kind                                   AS hold_kind,
       h.status                                      AS status,
       h.reason                                      AS reason,
       h.created_at::date::text                      AS placed_date,
       COALESCE(h.released_at::date::text, '')       AS released_date,
       COALESCE(pb.display_name, '')                 AS placed_by,
       COALESCE(rb.display_name, '')                 AS released_by,
       h.auto_placed                                 AS auto_placed
  FROM credit_holds h
  JOIN customers c ON c.id = h.customer_id
  LEFT JOIN users pb ON pb.id = h.placed_by
  LEFT JOIN users rb ON rb.id = h.released_by
 WHERE h.created_at >= $1::date
   AND h.created_at <  ($2::date + 1)
   AND ($3::text = '' OR h.status = $3::text)
 ORDER BY h.created_at DESC`,
		columns: []Column{
			col("customer_name", "Customer", KindText, false),
			col("hold_kind", "Kind", KindText, false),
			col("status", "Status", KindText, false),
			col("reason", "Reason", KindText, false),
			col("placed_date", "Placed", KindDate, false),
			col("released_date", "Released", KindDate, false),
			col("placed_by", "Placed by", KindText, false),
			col("released_by", "Released by", KindText, false),
			col("auto_placed", "Automatic", KindBool, false),
		},
	},

	// -- DELIVERY -------------------------------------------------------------------------------------
	// "On time" is delivered_at's DATE on or before the scheduled date. The substrate carries a
	// time_window as free text, not as a bounded interval, so a tighter definition would be inventing
	// precision the data does not have.
	"delivery_performance": {
		sql: `
SELECT s.scheduled_date::text                                                          AS scheduled_date,
       l.code::text                                                                    AS branch_code,
       count(*)::bigint                                                                AS stop_count,
       sum(CASE WHEN s.status = 'DELIVERED' THEN 1 ELSE 0 END)::bigint                 AS delivered_count,
       sum(CASE WHEN s.status = 'FAILED'    THEN 1 ELSE 0 END)::bigint                 AS failed_count,
       sum(CASE WHEN s.status = 'DELIVERED' AND s.delivered_at::date <= s.scheduled_date
                THEN 1 ELSE 0 END)::bigint                                             AS on_time_count
  FROM delivery_stops s
  JOIN locations l ON l.id = s.branch_id
 WHERE s.scheduled_date >= $1::date
   AND s.scheduled_date <= $2::date
   AND ($3::uuid IS NULL OR s.branch_id = $3::uuid)
 GROUP BY 1, 2
 ORDER BY 1, 2`,
		columns: []Column{
			col("scheduled_date", "Scheduled", KindDate, false),
			col("branch_code", "Branch", KindText, false),
			col("stop_count", "Stops", KindInt, true),
			col("delivered_count", "Delivered", KindInt, true),
			col("failed_count", "Failed", KindInt, true),
			col("on_time_count", "On time", KindInt, true),
		},
	},

	// -- WARRANTY -------------------------------------------------------------------------------------
	"warranty_by_vendor": {
		sql: `
SELECT COALESCE(v.name, '(no vendor)')                                       AS vendor_name,
       count(*)::bigint                                                      AS claim_count,
       sum(COALESCE(w.expected_cents, 0))::bigint                            AS expected_cents,
       sum(COALESCE(w.realized_cents, 0))::bigint                            AS realized_cents,
       sum(CASE WHEN w.phase = 'CLOSED'   THEN 1 ELSE 0 END)::bigint         AS closed_count,
       sum(CASE WHEN w.phase = 'REJECTED' THEN 1 ELSE 0 END)::bigint         AS rejected_count
  FROM warranty_claims w
  LEFT JOIN vendors v ON v.id = w.vendor_id
 WHERE w.opened_at >= $1::date
   AND w.opened_at <  ($2::date + 1)
   AND ($3::uuid IS NULL OR w.branch_id = $3::uuid)
 GROUP BY 1
 ORDER BY 2 DESC, 1`,
		columns: []Column{
			col("vendor_name", "Vendor", KindText, false),
			col("claim_count", "Claims", KindInt, true),
			col("expected_cents", "Expected", KindCents, true),
			col("realized_cents", "Realized", KindCents, true),
			col("closed_count", "Closed", KindInt, true),
			col("rejected_count", "Rejected", KindInt, true),
		},
	},

	"warranty_by_status": {
		sql: `
SELECT w.phase                                       AS phase,
       COALESCE(w.outcome, '')                       AS outcome,
       count(*)::bigint                              AS claim_count,
       sum(COALESCE(w.expected_cents, 0))::bigint    AS expected_cents,
       sum(COALESCE(w.realized_cents, 0))::bigint    AS realized_cents
  FROM warranty_claims w
 WHERE w.opened_at >= $1::date
   AND w.opened_at <  ($2::date + 1)
   AND ($3::uuid IS NULL OR w.branch_id = $3::uuid)
 GROUP BY 1, 2
 ORDER BY 1, 2`,
		columns: []Column{
			col("phase", "Phase", KindText, false),
			col("outcome", "Outcome", KindText, false),
			col("claim_count", "Claims", KindInt, true),
			col("expected_cents", "Expected", KindCents, true),
			col("realized_cents", "Realized", KindCents, true),
		},
	},
}

// CatalogCodes returns every compiled template code (sorted by the caller when order matters).
func CatalogCodes() []string {
	out := make([]string, 0, len(catalog))
	for code := range catalog {
		out = append(out, code)
	}
	return out
}
