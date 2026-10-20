-- 0425: slice 6b of the assistant's database boundary (TanyAI plan r3 slice 6, quotes and purchasing).
--
-- 1. The signed claim grows the branches where the asker may see purchase cost (po_cost_branch_ids), decided in
--    Go by the per branch purchasing policy, the second cost vocabulary. ai.claims() reads fields by name, so the
--    new attribute needs no change to the function: an old claim verifies and reads NULL, which every purchase
--    cost mask treats as no purchase cost. Made under SET LOCAL ROLE the claims owner and reset.
-- 2. The quote views, the purchasing views, their row security and their masks.
--
-- Role names carry the database suffix exactly as in 0418.

DO $s6b$
DECLARE
  db          text := current_database();
  norm        text;
  suffix      text;
  v_view      text;
  v_claims    text;
  v_reader    text;
  t           text;
  owner_name  text;
BEGIN
  norm := translate(db, 'ABCDEFGHIJKLMNOPQRSTUVWXYZ', 'abcdefghijklmnopqrstuvwxyz');
  norm := regexp_replace(norm, '[^a-z0-9]', '_', 'g');
  IF norm = db AND length(norm) <= 30 THEN
    suffix := norm;
  ELSE
    suffix := left(norm, 21) || '_' || left(md5(db), 8);
  END IF;
  v_view   := 'assist_view_owner_'   || suffix;
  v_claims := 'assist_claims_owner_' || suffix;
  v_reader := 'assist_reader_'       || suffix;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = v_claims) THEN
    RAISE EXCEPTION 'migration 0418 has not created the assist roles for this database';
  END IF;

  -- 1. The claim's purchase cost branches.
  EXECUTE format('SET LOCAL ROLE %I', v_claims);
  ALTER TYPE ai.claim_set ADD ATTRIBUTE po_cost_branch_ids uuid[];
  RESET ROLE;
  -- 2. The quote family: ai.quotes and ai.quote_lines.
  --
  -- Row security on quotes and quote_lines for the view owner alone (ENABLE, never FORCE, ownership assert
  -- first), policies on audience, the tier floor and branch reach, as the order family has them. quotes is a
  -- served table: this enables row security on it and adds no column and no json column to it.
  FOREACH t IN ARRAY ARRAY['quotes', 'quote_lines'] LOOP
    SELECT tableowner INTO owner_name FROM pg_tables WHERE schemaname = 'public' AND tablename = t;
    IF owner_name IS DISTINCT FROM current_user THEN
      RAISE EXCEPTION 'table % is owned by %, not by the migrating role %: refusing to enable row security on it',
        t, coalesce(owner_name, '(missing)'), current_user;
    END IF;
  END LOOP;

  -- Exact column grants. qty_base and product_id are read only to compute the margin cells the quote screen's
  -- margin glance computes; they are not columns of the view.
  EXECUTE format('GRANT SELECT (id, quote_no, customer_id, branch_id, status, valid_until, subtotal_cents, tax_cents, total_cents, created_at, deposit_pct, customer_notes, contact_name, fulfillment, delivery_address, requested_date, delivery_fee_cents) ON public.quotes TO %I', v_view);
  EXECUTE format('GRANT SELECT (id, quote_id, line_no, product_id, description, qty_entered, uom_entered, qty_base, unit_price_cents, disc_pct, line_total_cents) ON public.quote_lines TO %I', v_view);

  ALTER TABLE public.quotes ENABLE ROW LEVEL SECURITY;
  ALTER TABLE public.quote_lines ENABLE ROW LEVEL SECURITY;
  EXECUTE format($p$
    CREATE POLICY assist_staff_quotes ON public.quotes TO %I
    USING (
      (SELECT ai.claims()).audience = 'staff'
      AND (SELECT ai.claims()).tier BETWEEN 1 AND 2
      AND ((SELECT ai.claims()).global OR branch_id = ANY ((SELECT ai.claims()).branch_ids))
    )$p$, v_view);
  EXECUTE format($p$
    CREATE POLICY assist_staff_quote_lines ON public.quote_lines TO %I
    USING (
      (SELECT ai.claims()).audience = 'staff'
      AND (SELECT ai.claims()).tier BETWEEN 1 AND 2
      AND EXISTS (
        SELECT 1 FROM public.quotes q
        WHERE q.id = quote_lines.quote_id
          AND ((SELECT ai.claims()).global OR q.branch_id = ANY ((SELECT ai.claims()).branch_ids))
      )
    )$p$, v_view);

  -- The views, created under the view owner. ai.quote_lines carries the three margin cells the quote screen's
  -- margin glance carries (quote.StaffService.MarginGlance), by the same arithmetic: the cost is the branch
  -- average cost on file times the base quantity, rounded half up; the margin is in basis points of the line
  -- total with integer division; the profit is the line total less the cost. A line contributes cells only when
  -- it has a product, a positive quantity and total, and a cost ROW at the quote's branch (a cost of zero on
  -- file is a real cost of zero, a missing row is no cost, never a 100 percent margin). The screen shows them
  -- only to a role on the dealer's cost and margin list; the claim's margin flag is that answer, decided in Go,
  -- and the cells are NULL for every other asker.
  EXECUTE format('SET LOCAL ROLE %I', v_view);
  CREATE VIEW ai.quotes AS
    SELECT q.id,
           q.quote_no::text AS quote_no,
           q.status,
           q.valid_until,
           q.branch_id,
           COALESCE(l.name, '') AS branch_label,
           q.customer_id,
           COALESCE(c.name, '') AS customer_name,
           q.contact_name,
           q.delivery_address,
           q.customer_notes,
           q.fulfillment,
           q.requested_date,
           q.deposit_pct,
           q.subtotal_cents,
           q.tax_cents,
           q.delivery_fee_cents,
           q.total_cents,
           q.created_at
      FROM public.quotes q
      LEFT JOIN public.customers c ON c.id = q.customer_id
      LEFT JOIN public.locations l ON l.id = q.branch_id;
  CREATE VIEW ai.quote_lines AS
    SELECT ql.id,
           ql.quote_id,
           q.quote_no::text AS quote_no,
           q.branch_id,
           ql.line_no,
           ql.description,
           ql.qty_entered,
           ql.uom_entered::text AS uom,
           ql.unit_price_cents,
           ql.disc_pct,
           ql.line_total_cents,
           m.cost AS line_cost_cents,
           CASE WHEN m.cost IS NOT NULL THEN ((ql.line_total_cents - m.cost) * 10000) / ql.line_total_cents END AS margin_bps,
           CASE WHEN m.cost IS NOT NULL THEN ql.line_total_cents - m.cost END AS est_profit_cents
      FROM public.quote_lines ql
      JOIN public.quotes q ON q.id = ql.quote_id
      LEFT JOIN public.product_branch_costs pbc
        ON pbc.product_id = ql.product_id AND pbc.branch_id = q.branch_id
      CROSS JOIN LATERAL (
        SELECT CASE WHEN (SELECT ai.claims()).margin IS TRUE
                     AND ql.product_id IS NOT NULL AND ql.qty_base > 0 AND ql.line_total_cents > 0
                     AND pbc.avg_unit_cost_cents IS NOT NULL
                    THEN floor(pbc.avg_unit_cost_cents::numeric * ql.qty_base + 0.5)::bigint
               END AS cost
      ) m;
  EXECUTE format('GRANT SELECT ON ai.quotes, ai.quote_lines TO %I', v_reader);
  RESET ROLE;
  -- 3. The purchasing family: ai.purchase_orders and ai.purchase_order_lines.
  --
  -- Row security on purchasing_pos and purchasing_po_lines for the view owner alone, policies as above.
  FOREACH t IN ARRAY ARRAY['purchasing_pos', 'purchasing_po_lines', 'vendors'] LOOP
    SELECT tableowner INTO owner_name FROM pg_tables WHERE schemaname = 'public' AND tablename = t;
    IF owner_name IS DISTINCT FROM current_user THEN
      RAISE EXCEPTION 'table % is owned by %, not by the migrating role %: refusing to grant on it',
        t, coalesce(owner_name, '(missing)'), current_user;
    END IF;
  END LOOP;

  EXECUTE format('GRANT SELECT (id, po_no, vendor_id, branch_id, status, expected_at, ordered_at, notes, currency, delivery_method) ON public.purchasing_pos TO %I', v_view);
  EXECUTE format('GRANT SELECT (id, po_id, line_no, product_id, description, qty_ordered_base, qty_received_base, qty_billed_base, uom, unit_cost_cents, billed_cents) ON public.purchasing_po_lines TO %I', v_view);
  EXECUTE format('GRANT SELECT (id, name) ON public.vendors TO %I', v_view);

  ALTER TABLE public.purchasing_pos ENABLE ROW LEVEL SECURITY;
  ALTER TABLE public.purchasing_po_lines ENABLE ROW LEVEL SECURITY;
  EXECUTE format($p$
    CREATE POLICY assist_staff_purchasing_pos ON public.purchasing_pos TO %I
    USING (
      (SELECT ai.claims()).audience = 'staff'
      AND (SELECT ai.claims()).tier BETWEEN 1 AND 2
      AND ((SELECT ai.claims()).global OR branch_id = ANY ((SELECT ai.claims()).branch_ids))
    )$p$, v_view);
  EXECUTE format($p$
    CREATE POLICY assist_staff_purchasing_po_lines ON public.purchasing_po_lines TO %I
    USING (
      (SELECT ai.claims()).audience = 'staff'
      AND (SELECT ai.claims()).tier BETWEEN 1 AND 2
      AND EXISTS (
        SELECT 1 FROM public.purchasing_pos po
        WHERE po.id = purchasing_po_lines.po_id
          AND ((SELECT ai.claims()).global OR po.branch_id = ANY ((SELECT ai.claims()).branch_ids))
      )
    )$p$, v_view);

  -- The cost cells (the header subtotal, the line unit cost, line total and billed amount) are NULL unless the
  -- order's branch is one of the claim's purchase cost branches: the branches where the dealer's per branch
  -- purchasing policy lets this asker see what the dealer pays, decided in Go (ALL_STAFF allows, HIDDEN refuses,
  -- and the default and every unknown allow a manager and up). A line total is the quantity ordered times the
  -- unit cost, rounded half away from zero, as the purchasing module computes it, and the header subtotal is the
  -- sum of the line totals.
  EXECUTE format('SET LOCAL ROLE %I', v_view);
  CREATE VIEW ai.purchase_orders AS
    SELECT po.id,
           po.po_no::text AS po_no,
           po.status,
           po.branch_id,
           COALESCE(l.name, '') AS branch_label,
           po.vendor_id,
           COALESCE(v.name, '') AS vendor_name,
           po.expected_at,
           po.ordered_at,
           po.currency::text AS document_currency,
           po.delivery_method,
           po.notes,
           COALESCE(s.line_count, 0) AS line_count,
           CASE WHEN po.branch_id = ANY ((SELECT ai.claims()).po_cost_branch_ids)
                THEN COALESCE(s.subtotal, 0)
           END AS subtotal_cents
      FROM public.purchasing_pos po
      LEFT JOIN public.vendors v ON v.id = po.vendor_id
      LEFT JOIN public.locations l ON l.id = po.branch_id
      LEFT JOIN LATERAL (
        SELECT count(*)::int AS line_count,
               SUM(round(pl.qty_ordered_base * pl.unit_cost_cents))::bigint AS subtotal
          FROM public.purchasing_po_lines pl
         WHERE pl.po_id = po.id
      ) s ON TRUE;
  CREATE VIEW ai.purchase_order_lines AS
    SELECT pl.id,
           pl.po_id,
           po.po_no::text AS po_no,
           po.branch_id,
           pl.line_no,
           COALESCE(pr.sku::text, '') AS sku,
           pl.description,
           pl.qty_ordered_base,
           pl.qty_received_base,
           pl.qty_billed_base,
           pl.qty_ordered_base - pl.qty_received_base AS qty_outstanding_base,
           pl.uom::text AS uom,
           CASE WHEN po.branch_id = ANY ((SELECT ai.claims()).po_cost_branch_ids) THEN pl.unit_cost_cents END AS unit_cost_cents,
           CASE WHEN po.branch_id = ANY ((SELECT ai.claims()).po_cost_branch_ids)
                THEN round(pl.qty_ordered_base * pl.unit_cost_cents)::bigint END AS line_total_cents,
           CASE WHEN po.branch_id = ANY ((SELECT ai.claims()).po_cost_branch_ids) THEN pl.billed_cents END AS billed_cents
      FROM public.purchasing_po_lines pl
      JOIN public.purchasing_pos po ON po.id = pl.po_id
      LEFT JOIN public.products pr ON pr.id = pl.product_id;
  EXECUTE format('GRANT SELECT ON ai.purchase_orders, ai.purchase_order_lines TO %I', v_reader);
  RESET ROLE;
END
$s6b$;

RESET ROLE;
