-- 0428: slice 7b of the assistant's database boundary (TanyAI plan r4 slice 7, delivery, CRM and messages metadata).
--
-- 1. The zone reader (0427) needed CREATE and USAGE on schema ai only to create ai.dealer_day(). A definer function
--    runs as its owner and resolves the names in its body through its own search path (pg_catalog, public,
--    pg_temp), none of them in ai, so the owner needs no privilege on ai at call time (the caller needs USAGE, and
--    the reader has it). Both are taken back here, so the role owns one function in a schema it cannot use or
--    create in.
-- 2. The delivery views, the customer views, the message channel view, their row security and their masks.
--
-- Role names carry the database suffix exactly as in 0418.

DO $s7b$
DECLARE
  db          text := current_database();
  norm        text;
  suffix      text;
  v_view      text;
  v_zone      text;
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
  v_zone   := 'assist_zone_reader_'  || suffix;
  v_reader := 'assist_reader_'       || suffix;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = v_view) THEN
    RAISE EXCEPTION 'migration 0418 has not created the assist roles for this database';
  END IF;
  IF to_regprocedure('ai.dealer_day()') IS NULL THEN
    RAISE EXCEPTION 'migration 0427 has not created ai.dealer_day()';
  END IF;

  -- 1. The zone reader keeps nothing on schema ai.
  EXECUTE format('REVOKE CREATE, USAGE ON SCHEMA ai FROM %I', v_zone);
  -- 2. The delivery family: ai.delivery_runs and ai.delivery_stops.
  --
  -- Rows follow the dispatch screens (board, run, stop, manifest): the branch fence of the run or the stop, the
  -- assistant's tier floor on top. delivery_runs and delivery_stops are served tables (sync): row security is
  -- enabled on them and no column and no json column is added. No latitude, longitude, instructions, gate code,
  -- signature reference, signer name, freight or load-building column is granted or exposed.
  FOREACH t IN ARRAY ARRAY['delivery_runs', 'delivery_stops'] LOOP
    SELECT tableowner INTO owner_name FROM pg_tables WHERE schemaname = 'public' AND tablename = t;
    IF owner_name IS DISTINCT FROM current_user THEN
      RAISE EXCEPTION 'table % is owned by %, not by the migrating role %: refusing to enable row security on it',
        t, coalesce(owner_name, '(missing)'), current_user;
    END IF;
  END LOOP;
  EXECUTE format('GRANT SELECT (id, run_no, branch_id, scheduled_date, truck_id, driver_id, status, dispatched_at, completed_at) ON public.delivery_runs TO %I', v_view);
  EXECUTE format('GRANT SELECT (id, branch_id, order_id, run_id, stop_no, scheduled_date, status, time_window, contact_name, contact_phone, address_line, city, postal_code, failure_reason, delivered_at) ON public.delivery_stops TO %I', v_view);
  EXECUTE format('GRANT SELECT (id, code) ON public.dispatch_trucks TO %I', v_view);
  EXECUTE format('GRANT SELECT (id, name) ON public.dispatch_drivers TO %I', v_view);

  ALTER TABLE public.delivery_runs ENABLE ROW LEVEL SECURITY;
  ALTER TABLE public.delivery_stops ENABLE ROW LEVEL SECURITY;
  EXECUTE format($p$
    CREATE POLICY assist_staff_delivery_runs ON public.delivery_runs TO %I
    USING (
      (SELECT ai.claims()).audience = 'staff'
      AND (SELECT ai.claims()).tier BETWEEN 1 AND 2
      AND ((SELECT ai.claims()).global OR branch_id = ANY ((SELECT ai.claims()).branch_ids))
    )$p$, v_view);
  EXECUTE format($p$
    CREATE POLICY assist_staff_delivery_stops ON public.delivery_stops TO %I
    USING (
      (SELECT ai.claims()).audience = 'staff'
      AND (SELECT ai.claims()).tier BETWEEN 1 AND 2
      AND ((SELECT ai.claims()).global OR branch_id = ANY ((SELECT ai.claims()).branch_ids))
    )$p$, v_view);

  EXECUTE format('SET LOCAL ROLE %I', v_view);
  CREATE VIEW ai.delivery_runs AS
    SELECT r.id,
           r.run_no,
           r.scheduled_date,
           r.branch_id,
           COALESCE(l.name, '') AS branch_label,
           COALESCE(t.code::text, '') AS truck_code,
           COALESCE(d.name, '') AS driver_label,
           r.status,
           (SELECT count(*) FROM public.delivery_stops s WHERE s.run_id = r.id)::int AS stop_count,
           r.dispatched_at,
           r.completed_at
      FROM public.delivery_runs r
      LEFT JOIN public.locations l ON l.id = r.branch_id
      LEFT JOIN public.dispatch_trucks t ON t.id = r.truck_id
      LEFT JOIN public.dispatch_drivers d ON d.id = r.driver_id;
  CREATE VIEW ai.delivery_stops AS
    SELECT s.id,
           s.run_id,
           COALESCE(r.run_no, '') AS run_no,
           s.stop_no,
           s.scheduled_date,
           s.branch_id,
           COALESCE(l.name, '') AS branch_label,
           s.order_id,
           COALESCE(o.order_no::text, '') AS order_no,
           COALESCE(c.name, '') AS customer_name,
           s.status,
           s.time_window,
           s.contact_name,
           s.contact_phone,
           s.address_line,
           s.city,
           s.postal_code,
           s.failure_reason,
           s.delivered_at
      FROM public.delivery_stops s
      LEFT JOIN public.delivery_runs r ON r.id = s.run_id
      LEFT JOIN public.locations l ON l.id = s.branch_id
      LEFT JOIN public.orders o ON o.id = s.order_id
      LEFT JOIN public.customers c ON c.id = o.customer_id;
  EXECUTE format('GRANT SELECT ON ai.delivery_runs, ai.delivery_stops TO %I', v_reader);
  RESET ROLE;
END
$s7b$;
