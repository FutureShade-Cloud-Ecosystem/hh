-- 0418: the assistant's database boundary (TanyAI slice 4, plan r3 sections 3.1, 3.1a and 3.2).
--
-- Six roles per database and two schemas. The assistant's tool calls log in as
-- assist_login (or assist_pro_login), a NOINHERIT member of one reader role that
-- holds nothing but the ai or ai_pro objects, so a RESET ROLE or SET ROLE from that
-- session reaches nothing: not the app role, not an owner. No view, row policy or
-- base table grant is made here; the data slices add them on top.
--
-- Role names are cluster wide and dealer databases may share a cluster, so every
-- name ends in a suffix derived from current_database(): the name in ASCII lower
-- case, every other character an underscore; a name that derivation changed, or
-- one longer than 30 characters, is cut to 21 characters and joined to the first
-- eight hex characters of md5(name). core/internal/assist/readtx.Suffix derives
-- the same string in Go, and an integration test compares them.
--
-- Objects in schema ai are created under SET LOCAL ROLE so their owner is right by
-- construction (plan 3.1a), and the role is RESET before this file ends: the
-- migration runner writes schema_migrations in the same transaction, and the
-- switched role cannot (the runner also refuses a file that leaves a role set).
-- On a database where the migrate role is not a superuser it must be a member of
-- the two owner roles with SET; a CREATEROLE migrate role is granted that below.

CREATE SCHEMA IF NOT EXISTS ai;
CREATE SCHEMA IF NOT EXISTS ai_pro;

DO $assist$
DECLARE
  db          text := current_database();
  norm        text;
  suffix      text;
  v_view      text;
  v_claims    text;
  v_reader    text;
  v_pro       text;
  v_login     text;
  v_pro_login text;
  app         text := current_user;
  is_super    boolean;
  crypto_schema text;
  r           text;
BEGIN
  -- Suffix derivation (mirrors readtx.Suffix).
  norm := translate(db, 'ABCDEFGHIJKLMNOPQRSTUVWXYZ', 'abcdefghijklmnopqrstuvwxyz');
  norm := regexp_replace(norm, '[^a-z0-9]', '_', 'g');
  IF norm = db AND length(norm) <= 30 THEN
    suffix := norm;
  ELSE
    suffix := left(norm, 21) || '_' || left(md5(db), 8);
  END IF;
  v_view      := 'assist_view_owner_'   || suffix;
  v_claims    := 'assist_claims_owner_' || suffix;
  v_reader    := 'assist_reader_'       || suffix;
  v_pro       := 'assist_pro_reader_'   || suffix;
  v_login     := 'assist_login_'        || suffix;
  v_pro_login := 'assist_pro_login_'    || suffix;

  SELECT rolsuper INTO is_super FROM pg_roles WHERE rolname = current_user;

  -- The roles. Created once, never altered on a rerun beyond the fixed attributes.
  FOREACH r IN ARRAY ARRAY[v_view, v_claims, v_reader, v_pro] LOOP
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = r) THEN
      EXECUTE format('CREATE ROLE %I NOLOGIN NOSUPERUSER NOBYPASSRLS NOCREATEROLE NOCREATEDB NOREPLICATION', r);
    END IF;
  END LOOP;
  FOREACH r IN ARRAY ARRAY[v_login, v_pro_login] LOOP
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = r) THEN
      -- No password here: the provisioning oneshot sets it from the deployment's own value.
      EXECUTE format('CREATE ROLE %I LOGIN NOINHERIT NOSUPERUSER NOBYPASSRLS NOCREATEROLE NOCREATEDB NOREPLICATION', r);
    END IF;
  END LOOP;

  -- Each login is a member of its reader role only, and can become nothing else.
  EXECUTE format('GRANT %I TO %I', v_reader, v_login);
  EXECUTE format('GRANT %I TO %I', v_pro, v_pro_login);

  -- A migrate role that is not a superuser must be able to SET ROLE to the owners.
  IF NOT is_super THEN
    EXECUTE format('GRANT %I TO %I WITH SET TRUE, INHERIT FALSE', v_view, app);
    EXECUTE format('GRANT %I TO %I WITH SET TRUE, INHERIT FALSE', v_claims, app);
  END IF;

  -- Role level settings: read only by default, short timeouts.
  FOREACH r IN ARRAY ARRAY[v_login, v_pro_login] LOOP
    EXECUTE format('ALTER ROLE %I SET default_transaction_read_only = on', r);
    EXECUTE format('ALTER ROLE %I SET statement_timeout = ''5s''', r);
    EXECUTE format('ALTER ROLE %I SET lock_timeout = ''2s''', r);
    EXECUTE format('ALTER ROLE %I SET idle_in_transaction_session_timeout = ''10s''', r);
  END LOOP;

  -- Schema grants (plan 3.1). The pro reader also needs USAGE on ai to call ai.claims().
  EXECUTE format('GRANT USAGE ON SCHEMA ai TO %I, %I', v_reader, v_pro);
  EXECUTE format('GRANT USAGE ON SCHEMA ai_pro TO %I', v_pro);
  EXECUTE format('GRANT USAGE, CREATE ON SCHEMA ai TO %I, %I', v_claims, v_view);
  EXECUTE format('GRANT USAGE, CREATE ON SCHEMA ai_pro TO %I', v_view);

  -- Functions: nothing outside ai is executable by the assist roles. Postgres grants
  -- EXECUTE on every new function to PUBLIC, so revoke it from PUBLIC on every
  -- existing function outside the system schemas and give it back to the app role,
  -- and do the same by default for functions the migrate role creates from now on.
  FOR r IN
    SELECT p.oid::regprocedure::text
    FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
    WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
      AND n.nspname NOT LIKE 'pg\_%' AND n.nspname NOT IN ('ai', 'ai_pro')
  LOOP
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', r);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO %I', r, app);
  END LOOP;
  EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE %I REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC', app);
  EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA ai GRANT EXECUTE ON FUNCTIONS TO %I', app, app);
  EXECUTE 'REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA ai FROM PUBLIC';
  EXECUTE 'REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA ai_pro FROM PUBLIC';
  EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA ai REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC', app);
  EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA ai_pro REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC', app);

  -- The claims owner alone runs pgcrypto's hmac (the one function outside ai it needs).
  SELECT n.nspname INTO crypto_schema FROM pg_extension e JOIN pg_namespace n ON n.oid = e.extnamespace
   WHERE e.extname = 'pgcrypto';
  IF crypto_schema IS NULL THEN
    RAISE EXCEPTION 'pgcrypto is not installed (migration 0001 creates it)';
  END IF;
  EXECUTE format('GRANT EXECUTE ON FUNCTION %I.hmac(text, text, text) TO %I', crypto_schema, v_claims);

  -- TEMP is granted to PUBLIC on every database by default; keep it for the app role only.
  EXECUTE format('REVOKE TEMP ON DATABASE %I FROM PUBLIC', db);
  EXECUTE format('GRANT TEMP ON DATABASE %I TO %I', db, app);
END
$assist$;

RESET ROLE;
