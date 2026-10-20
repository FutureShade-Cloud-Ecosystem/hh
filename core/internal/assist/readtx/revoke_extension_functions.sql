-- Run as the extensions' owner by core assist-revoke-extension-functions. Closes
-- EXECUTE to PUBLIC on every function that belongs to an extension, gives it to the
-- function users role, and gives pgcrypto's hmac(text, text, text) to the claims
-- owner. The two role names arrive as transaction local settings; nothing else is
-- read from outside. Idempotent.
DO $do$
DECLARE
  fn_users     text := current_setting('readtx.fn_users');
  claims_owner text := current_setting('readtx.claims_owner');
  r            record;
  crypto       text;
BEGIN
  FOR r IN
    SELECT p.oid::regprocedure::text AS sig
    FROM pg_proc p
    JOIN pg_depend d ON d.classid = 'pg_proc'::regclass AND d.objid = p.oid AND d.deptype = 'e'
    JOIN pg_namespace n ON n.oid = p.pronamespace
    WHERE n.nspname NOT IN ('pg_catalog', 'information_schema') AND n.nspname NOT LIKE 'pg\_%'
  LOOP
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', r.sig);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO %I', r.sig, fn_users);
  END LOOP;
  SELECT n.nspname INTO crypto FROM pg_extension e JOIN pg_namespace n ON n.oid = e.extnamespace WHERE e.extname = 'pgcrypto';
  IF crypto IS NOT NULL THEN
    EXECUTE format('GRANT EXECUTE ON FUNCTION %I.hmac(text, text, text) TO %I', crypto, claims_owner);
  END IF;
END
$do$
