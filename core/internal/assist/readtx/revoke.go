package readtx

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// revokeExtensionFunctionsSQL closes EXECUTE to PUBLIC on every function that belongs to an extension, gives
// it to the function users role, gives pgcrypto's hmac(text, text, text) to the claims owner, and citext's
// citext_eq(citext, citext) to the zone reader (when that role exists). The three
// role names arrive as transaction local settings; nothing else is read from outside. Idempotent.
//
// It reads the system catalogue, which is no module's data, and says so to the table gate in the one way the
// gate has for it: every catalogue table is written pg_catalog qualified, the ownership map carries
// pg_catalog as the system owner, and the allowlist has one entry for this file with its reason. (An
// earlier version kept this SQL in an embedded file the census could not read; this is the other fix.)
const revokeExtensionFunctionsSQL = `DO $do$
DECLARE
  fn_users     text := current_setting('readtx.fn_users');
  claims_owner text := current_setting('readtx.claims_owner');
  zone_reader  text := current_setting('readtx.zone_reader');
  r            record;
  crypto       text;
  citext_sch   text;
BEGIN
  FOR r IN
    SELECT p.oid::regprocedure::text AS sig
    FROM pg_catalog.pg_proc p
    JOIN pg_catalog.pg_depend d ON d.classid = 'pg_catalog.pg_proc'::regclass AND d.objid = p.oid AND d.deptype = 'e'
    JOIN pg_catalog.pg_namespace n ON n.oid = p.pronamespace
    WHERE n.nspname NOT IN ('pg_catalog', 'information_schema') AND n.nspname NOT LIKE 'pg\_%'
  LOOP
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', r.sig);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO %I', r.sig, fn_users);
  END LOOP;
  SELECT n.nspname INTO crypto FROM pg_catalog.pg_extension e JOIN pg_catalog.pg_namespace n ON n.oid = e.extnamespace WHERE e.extname = 'pgcrypto';
  IF crypto IS NOT NULL THEN
    EXECUTE format('GRANT EXECUTE ON FUNCTION %I.hmac(text, text, text) TO %I', crypto, claims_owner);
  END IF;
  SELECT n.nspname INTO citext_sch FROM pg_catalog.pg_extension e JOIN pg_catalog.pg_namespace n ON n.oid = e.extnamespace WHERE e.extname = 'citext';
  IF citext_sch IS NOT NULL AND EXISTS (SELECT 1 FROM pg_catalog.pg_roles WHERE rolname = zone_reader) THEN
    EXECUTE format('GRANT EXECUTE ON FUNCTION %I.citext_eq(%I.citext, %I.citext) TO %I', citext_sch, citext_sch, citext_sch, zone_reader);
  END IF;
END
$do$`

// ProvisionRevokeExtensionFunctions closes the functions of extensions (pgcrypto,
// citext, pg_trgm) to PUBLIC. Migration 0418 cannot do it on a box where the
// migrating role is not a superuser, because those functions belong to the
// bootstrap superuser. Run it as the extensions' owner (core
// assist-revoke-extension-functions); it gives the functions to the function users
// role and hmac to the claims owner, and is safe to repeat.
func ProvisionRevokeExtensionFunctions(ctx context.Context, conn *pgx.Conn, roles Roles) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return errors.New("readtx: cannot begin")
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('readtx.fn_users', $1, true), set_config('readtx.claims_owner', $2, true), set_config('readtx.zone_reader', $3, true)`, roles.FunctionUsers, roles.ClaimsOwner, roles.ZoneReader); err != nil {
		return fmt.Errorf("readtx: cannot stage the role names%s", sqlState(err))
	}
	if _, err := tx.Exec(ctx, revokeExtensionFunctionsSQL); err != nil {
		return fmt.Errorf("readtx: cannot revoke the extension functions%s", sqlState(err))
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("readtx: commit failed")
	}
	return nil
}
