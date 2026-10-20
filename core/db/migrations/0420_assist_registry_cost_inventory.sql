-- 0420: slice 6a of the assistant's database boundary (TanyAI plan r3 slice 6, the money gate).
--
-- 1. The signed claim grows a tier. Its cost branch ids and margin flag were already fields of the composite
--    (0418); the Go side now fills them from the dealer's cost rule. ai.claims() reads every field by name
--    through jsonb_populate_record, so a new attribute needs no change to the function: a claim signed with
--    a tier reads it back, and a claim signed without one (an old payload) verifies and reads NULL, which
--    every tier floor below treats as no tier. The type is owned by the claims owner, so the change is made
--    under SET LOCAL ROLE and reset before the file ends.
-- 2. The tier floor in the policies of 0419 (and of every table this file adds).
-- 3. The inventory views, their row security and the cost mask.
--
-- Role names carry the database suffix exactly as in 0418.

DO $s6a$
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

  -- 1. The claim's tier.
  EXECUTE format('SET LOCAL ROLE %I', v_claims);
  ALTER TYPE ai.claim_set ADD ATTRIBUTE tier integer;
  RESET ROLE;

  -- 2. The tier floor, in the policies of 0419, as branch reach and audience are: the claim's tier must be
  -- 1 or 2 (manager or better) for the order family. A tier 3 claim, or a claim with no tier, reads nothing
  -- even if the Go side ever signed one. ALTER POLICY keeps the policy's role list (the view owner alone).
  ALTER POLICY assist_staff_orders ON public.orders USING (
    (SELECT ai.claims()).audience = 'staff'
    AND (SELECT ai.claims()).tier BETWEEN 1 AND 2
    AND ((SELECT ai.claims()).global OR branch_id = ANY ((SELECT ai.claims()).branch_ids))
  );
  ALTER POLICY assist_staff_order_lines ON public.order_lines USING (
    (SELECT ai.claims()).audience = 'staff'
    AND (SELECT ai.claims()).tier BETWEEN 1 AND 2
    AND EXISTS (
      SELECT 1 FROM public.orders o
      WHERE o.id = order_lines.order_id
        AND ((SELECT ai.claims()).global OR o.branch_id = ANY ((SELECT ai.claims()).branch_ids))
    )
  );

  -- 3. The inventory family: ai.stock_balances, one row per product and branch (the sum of the branch's
  -- locations, as the inventory module's own stock lookup sums them), with the average cost as the one cost
  -- class column.
  --
  -- Row security on the two base tables, for the view owner alone, ENABLE and never FORCE, the ownership assert
  -- first (the serve startup check holds the other half: row security must not be active for the serving role
  -- on either table). Both policies check audience, the tier floor and branch reach, as the order policies do.
  -- stock_balances is a served (synced) table: this enables row security on it and adds no column to it.
  FOREACH t IN ARRAY ARRAY['stock_balances', 'product_branch_costs'] LOOP
    SELECT tableowner INTO owner_name FROM pg_tables WHERE schemaname = 'public' AND tablename = t;
    IF owner_name IS DISTINCT FROM current_user THEN
      RAISE EXCEPTION 'table % is owned by %, not by the migrating role %: refusing to enable row security on it',
        t, coalesce(owner_name, '(missing)'), current_user;
    END IF;
  END LOOP;

  -- Column grants, exact. loc_type joins the locations grant: the stock lookup counts a location as a branch
  -- only when it is one.
  EXECUTE format('GRANT SELECT (product_id, branch_id, on_hand_base, qty_reserved) ON public.stock_balances TO %I', v_view);
  EXECUTE format('GRANT SELECT (product_id, branch_id, avg_unit_cost_cents) ON public.product_branch_costs TO %I', v_view);
  EXECUTE format('GRANT SELECT (id, sku, name, base_uom) ON public.products TO %I', v_view);
  EXECUTE format('GRANT SELECT (loc_type) ON public.locations TO %I', v_view);

  ALTER TABLE public.stock_balances ENABLE ROW LEVEL SECURITY;
  ALTER TABLE public.product_branch_costs ENABLE ROW LEVEL SECURITY;
  EXECUTE format($p$
    CREATE POLICY assist_staff_stock_balances ON public.stock_balances TO %I
    USING (
      (SELECT ai.claims()).audience = 'staff'
      AND (SELECT ai.claims()).tier BETWEEN 1 AND 2
      AND ((SELECT ai.claims()).global OR branch_id = ANY ((SELECT ai.claims()).branch_ids))
    )$p$, v_view);
  EXECUTE format($p$
    CREATE POLICY assist_staff_product_branch_costs ON public.product_branch_costs TO %I
    USING (
      (SELECT ai.claims()).audience = 'staff'
      AND (SELECT ai.claims()).tier BETWEEN 1 AND 2
      AND ((SELECT ai.claims()).global OR branch_id = ANY ((SELECT ai.claims()).branch_ids))
    )$p$, v_view);

  -- The view, created under the view owner. Quantities are the module's own arithmetic (available is on hand
  -- minus the hard holds in qty_reserved). The average cost is masked per row: it is present only where the row's
  -- branch is one of the claim's cost branches, NULL otherwise, and a cost of zero on file is NULL for everyone
  -- (the module reads it as absent). citext columns are cast to text so no function grant is needed.
  EXECUTE format('SET LOCAL ROLE %I', v_view);
  CREATE VIEW ai.stock_balances AS
    SELECT sb.product_id,
           p.sku::text AS sku,
           p.name AS product_name,
           sb.branch_id,
           COALESCE(l.name, '') AS branch_label,
           sb.on_hand_base,
           sb.reserved_base,
           sb.on_hand_base - sb.reserved_base AS available_base,
           p.base_uom::text AS uom,
           CASE WHEN sb.branch_id = ANY ((SELECT ai.claims()).cost_branch_ids)
                THEN NULLIF(pbc.avg_unit_cost_cents, 0)
           END AS avg_cost_cents
      FROM (SELECT product_id, branch_id,
                   SUM(on_hand_base) AS on_hand_base,
                   SUM(qty_reserved) AS reserved_base
              FROM public.stock_balances
             GROUP BY product_id, branch_id) sb
      JOIN public.products p ON p.id = sb.product_id
      JOIN public.locations l ON l.id = sb.branch_id AND l.loc_type = 'BRANCH'
      LEFT JOIN public.product_branch_costs pbc
        ON pbc.product_id = sb.product_id AND pbc.branch_id = sb.branch_id;
  EXECUTE format('GRANT SELECT ON ai.stock_balances TO %I', v_reader);
  RESET ROLE;
END
$s6a$;

RESET ROLE;
