-- 0419: the first data slice of the assistant's database boundary (TanyAI slice 5, plan r3 sections 3.3, 3.4).
--
-- Row level security on orders and order_lines for the view owner role only, then (below) the two views the
-- assistant reads, created under SET LOCAL ROLE so the view owner is right by construction, and the append only
-- query log. The app role is unaffected: it owns these tables (asserted first) and an owner is exempt from row
-- security. FORCE is never used: it does nothing against a superuser and would blank both tables for a
-- non superuser owning app.
--
-- Role names carry the database suffix exactly as in 0418 (current_database(), ASCII lower case, other
-- characters an underscore; a changed or over long name is cut to 21 characters plus _ plus eight hex of md5).
--
-- orders and order_lines are served (synced) tables. This file enables row security on them and adds no column to
-- any table, so the sync freshness trigger (NEW IS DISTINCT FROM OLD) is untouched.

DO $s5$
DECLARE
  db          text := current_database();
  norm        text;
  suffix      text;
  v_view      text;
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
  v_view   := 'assist_view_owner_' || suffix;
  v_reader := 'assist_reader_'     || suffix;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = v_view) THEN
    RAISE EXCEPTION 'migration 0418 has not created the assist roles for this database';
  END IF;

  -- The ownership assert (review r1 P2-3): row security is enabled only on a table the migrating role owns, so
  -- the app that owns it is exempt. Where migrate and serve are different roles the serve startup check holds
  -- the other half (row_security_active is false for the serving role on each of these tables).
  FOREACH t IN ARRAY ARRAY['orders', 'order_lines'] LOOP
    SELECT tableowner INTO owner_name FROM pg_tables WHERE schemaname = 'public' AND tablename = t;
    IF owner_name IS DISTINCT FROM current_user THEN
      RAISE EXCEPTION 'table % is owned by %, not by the migrating role %: refusing to enable row security on it',
        t, coalesce(owner_name, '(missing)'), current_user;
    END IF;
  END LOOP;

  -- Base columns the views use, and no others. Column grants, never table grants.
  EXECUTE format('GRANT SELECT (id, order_no, customer_id, branch_id, status, fulfillment, requested_date, created_at, subtotal_cents, tax_cents, total_cents) ON public.orders TO %I', v_view);
  EXECUTE format('GRANT SELECT (id, order_id, line_no, description, qty_entered, uom_entered, unit_price_cents, line_total_cents) ON public.order_lines TO %I', v_view);
  EXECUTE format('GRANT SELECT (id, name) ON public.customers TO %I', v_view);
  EXECUTE format('GRANT SELECT (id, name) ON public.locations TO %I', v_view);

  ALTER TABLE public.orders ENABLE ROW LEVEL SECURITY;
  ALTER TABLE public.order_lines ENABLE ROW LEVEL SECURITY;

  -- One permissive policy each, for the view owner alone (a view applies its owner's policies). Every
  -- predicate checks the claim's audience as well as its branches, and fails closed on a NULL claim.
  EXECUTE format($p$
    CREATE POLICY assist_staff_orders ON public.orders TO %I
    USING (
      (SELECT ai.claims()).audience = 'staff'
      AND ((SELECT ai.claims()).global OR branch_id = ANY ((SELECT ai.claims()).branch_ids))
    )$p$, v_view);
  EXECUTE format($p$
    CREATE POLICY assist_staff_order_lines ON public.order_lines TO %I
    USING (
      (SELECT ai.claims()).audience = 'staff'
      AND EXISTS (
        SELECT 1 FROM public.orders o
        WHERE o.id = order_lines.order_id
          AND ((SELECT ai.claims()).global OR o.branch_id = ANY ((SELECT ai.claims()).branch_ids))
      )
    )$p$, v_view);
END
$s5$;

RESET ROLE;
