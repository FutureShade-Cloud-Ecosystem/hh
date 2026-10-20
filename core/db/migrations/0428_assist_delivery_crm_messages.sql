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
END
$s7b$;
