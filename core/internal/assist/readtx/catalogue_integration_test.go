//go:build integration

package readtx

// The catalogue test (plan 3.1a, red team 16). It reads the system catalogue of
// the migrated database and fails unless the assist boundary is exactly what
// the plan says, so a later migration that creates an object in ai or ai_pro as
// the superuser, adds a grant, or widens a role fails CI instead of silently
// opening the data to every asker.
//
// Written exceptions, and nothing else:
//   - ai.claims_key, its primary key index and the composite return type
//     ai.claim_set are owned by the claims owner (a composite type has a pg_class
//     row; review r3 P3 note 2).
//   - ai.claims() is owned by the claims owner.
//   - the claims owner may run pgcrypto's hmac(text, text, text).
//   - the readers may run ai.claims() and, in later slices, SELECT the views of
//     their own schema; slice 5 adds the view owner's column grants on base
//     tables to the table rules below.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// assistReaderViews is the exact list of views the staff reader may SELECT. Each data slice adds its
// views here, with the reason, so a view cannot be granted by accident.
var assistReaderViews = map[string]bool{
	"ai.orders":         true, // slice 5
	"ai.order_lines":    true, // slice 5
	"ai.stock_balances": true, // slice 6a
}

// viewOwnerColumns is the exact set of base columns the view owner may read, and nothing else: column level
// SELECT only, never a table grant. Each data slice adds the columns its views use.
var viewOwnerColumns = map[string][]string{
	"public.orders":               {"branch_id", "created_at", "customer_id", "fulfillment", "id", "order_no", "requested_date", "status", "subtotal_cents", "tax_cents", "total_cents"}, // slice 5
	"public.order_lines":          {"description", "id", "line_no", "order_id", "qty_entered", "unit_price_cents", "line_total_cents", "uom_entered"},                                    // slice 5
	"public.customers":            {"id", "name"},                                                                                                                                        // slice 5
	"public.locations":            {"id", "loc_type", "name"},                                                                                                                            // loc_type: slice 6a
	"public.stock_balances":       {"branch_id", "on_hand_base", "product_id", "qty_reserved"},                                                                                           // slice 6a
	"public.product_branch_costs": {"avg_unit_cost_cents", "branch_id", "product_id"},                                                                                                    // slice 6a
	"public.products":             {"base_uom", "id", "name", "sku"},                                                                                                                     // slice 6a                                                                                                                                        // slice 5
}

func (r Roles) all() []string {
	return []string{r.ViewOwner, r.ClaimsOwner, r.Reader, r.ProReader, r.Login, r.ProLogin}
}

// catalogueViolations reads the catalogue. extensionsOpen describes a box where the
// migrating role is not a superuser and the extensions' own owner has not yet run
// the revoke oneshot (core assist-revoke-extension-functions): there, and only
// there, the functions of extensions owned by another role keep EXECUTE for PUBLIC
// (the migration's REVOKE on them is only a warning for a role that does not own
// them), and the test asserts exactly that set, none outside it.
func catalogueViolations(ctx context.Context, conn *pgx.Conn, roles Roles, extensionsOpen bool) ([]string, error) {
	var migrating string
	if err := conn.QueryRow(ctx, `SELECT current_user::text`).Scan(&migrating); err != nil {
		return nil, err
	}
	var out []string
	add := func(format string, a ...any) { out = append(out, fmt.Sprintf(format, a...)) }
	collect := func(sql string, args ...any) ([][]any, error) {
		rows, err := conn.Query(ctx, sql, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var res [][]any
		for rows.Next() {
			v, err := rows.Values()
			if err != nil {
				return nil, err
			}
			res = append(res, v)
		}
		return res, rows.Err()
	}

	// 1. Ownership of every relation, function and type in ai and ai_pro.
	claimsOwned := map[string]bool{"ai.claims_key": true, "ai.claims_key_pkey": true, "ai.claim_set": true, "ai.claims": true}
	for _, q := range []struct{ kind, sql string }{
		{"relation", `SELECT n.nspname, c.relname, r.rolname, r.rolsuper, r.rolbypassrls
			FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace JOIN pg_roles r ON r.oid = c.relowner
			WHERE n.nspname IN ('ai', 'ai_pro')`},
		{"function", `SELECT n.nspname, p.proname, r.rolname, r.rolsuper, r.rolbypassrls
			FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace JOIN pg_roles r ON r.oid = p.proowner
			WHERE n.nspname IN ('ai', 'ai_pro')`},
		{"type", `SELECT n.nspname, t.typname, r.rolname, r.rolsuper, r.rolbypassrls
			FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace JOIN pg_roles r ON r.oid = t.typowner
			WHERE n.nspname IN ('ai', 'ai_pro') AND t.typtype IN ('e', 'd', 'r', 'm', 'b') AND t.typcategory <> 'A'`},
	} {
		rows, err := collect(q.sql)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			schema, name, owner := row[0].(string), row[1].(string), row[2].(string)
			super, bypass := row[3].(bool), row[4].(bool)
			want := roles.ViewOwner
			if claimsOwned[schema+"."+name] {
				want = roles.ClaimsOwner
			}
			if owner != want {
				add("%s %s.%s is owned by %s, want %s", q.kind, schema, name, owner, want)
			}
			if super || bypass {
				add("%s %s.%s is owned by %s, which holds superuser or BYPASSRLS", q.kind, schema, name, owner)
			}
		}
	}

	// 2. Role attributes and memberships.
	rows, err := collect(`SELECT rolname, rolsuper, rolbypassrls, rolcreaterole, rolcreatedb, rolreplication, rolcanlogin, rolinherit
		FROM pg_roles WHERE rolname = ANY($1)`, roles.all())
	if err != nil {
		return nil, err
	}
	if len(rows) != 6 {
		add("expected 6 assist roles, found %d", len(rows))
	}
	for _, row := range rows {
		name := row[0].(string)
		for i, attr := range []string{"rolsuper", "rolbypassrls", "rolcreaterole", "rolcreatedb", "rolreplication"} {
			if row[1+i].(bool) {
				add("role %s has %s", name, attr)
			}
		}
		isLogin := name == roles.Login || name == roles.ProLogin
		if row[6].(bool) != isLogin {
			add("role %s rolcanlogin = %v", name, row[6])
		}
		if isLogin && row[7].(bool) {
			add("login %s is INHERIT, want NOINHERIT", name)
		}
	}
	wantMember := map[string][]string{roles.Login: {roles.Reader}, roles.ProLogin: {roles.ProReader}}
	for _, name := range roles.all() {
		rows, err := collect(`SELECT g.rolname FROM pg_auth_members m JOIN pg_roles g ON g.oid = m.roleid
			JOIN pg_roles r ON r.oid = m.member WHERE r.rolname = $1 ORDER BY 1`, name)
		if err != nil {
			return nil, err
		}
		var got []string
		for _, row := range rows {
			got = append(got, row[0].(string))
		}
		if strings.Join(got, ",") != strings.Join(wantMember[name], ",") {
			add("role %s is a member of [%s], want [%s]", name, strings.Join(got, ","), strings.Join(wantMember[name], ","))
		}
	}
	// The migrating role is exempt: a non superuser role that creates these roles holds
	// ADMIN (and SET) on them, which is how it can switch to the owners.
	for _, reader := range []string{roles.Reader, roles.ProReader} {
		rows, err := collect(`SELECT r.rolname FROM pg_auth_members m JOIN pg_roles g ON g.oid = m.roleid
			JOIN pg_roles r ON r.oid = m.member WHERE g.rolname = $1 AND r.rolname <> $2 ORDER BY 1`, reader, migrating)
		if err != nil {
			return nil, err
		}
		var got []string
		for _, row := range rows {
			got = append(got, row[0].(string))
		}
		login := roles.Login
		if reader == roles.ProReader {
			login = roles.ProLogin
		}
		if strings.Join(got, ",") != login {
			add("reader %s has members [%s], want only %s", reader, strings.Join(got, ","), login)
		}
	}
	rows, err = collect(`SELECT r.rolname, p.rolname FROM pg_roles r, pg_roles p
		WHERE r.rolname = ANY($1) AND p.rolname LIKE 'pg\_%' AND pg_has_role(r.oid, p.oid, 'MEMBER')`, roles.all())
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		add("role %s is a member of the predefined role %s", row[0], row[1])
	}

	// The function users role is no assist role: no assist role may be a member, and it
	// carries no power and no login.
	rows, err = collect(`SELECT r.rolname FROM pg_roles r WHERE r.rolname = ANY($1) AND pg_has_role(r.oid, $2, 'MEMBER')`, roles.all(), roles.FunctionUsers)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		add("assist role %s is a member of the function users role %s", row[0], roles.FunctionUsers)
	}
	rows, err = collect(`SELECT rolname, rolsuper OR rolbypassrls OR rolcreaterole OR rolcreatedb OR rolreplication OR rolcanlogin
		FROM pg_roles WHERE rolname = $1`, roles.FunctionUsers)
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		add("the function users role %s is missing", roles.FunctionUsers)
	} else if rows[0][1].(bool) {
		add("the function users role %s carries a power or a login", roles.FunctionUsers)
	}

	// 3. Table privileges outside the plan's list. Writes: never, on anything the
	// role does not own. SELECT: only a reader on a view of its own schema.
	rows, err = collect(`
		SELECT r.rolname, n.nspname, c.relname, c.relkind::text, c.relowner = r.oid,
		       has_table_privilege(r.oid, c.oid, 'SELECT'),
		       has_table_privilege(r.oid, c.oid, 'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER'),
		       has_any_column_privilege(r.oid, c.oid, 'SELECT,INSERT,UPDATE,REFERENCES')
		FROM pg_roles r, pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE r.rolname = ANY($1) AND c.relkind IN ('r', 'p', 'v', 'm', 'f', 'S')
		  AND n.nspname NOT IN ('pg_catalog', 'information_schema') AND n.nspname NOT LIKE 'pg\_%'`, roles.all())
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		role, schema, rel, kind := row[0].(string), row[1].(string), row[2].(string), row[3].(string)
		owner, sel, write, anyCol := row[4].(bool), row[5].(bool), row[6].(bool), row[7].(bool)
		if owner {
			continue
		}
		fq := schema + "." + rel
		if write {
			add("role %s holds a write privilege on %s", role, fq)
		}
		// The view owner's column grants are checked as an exact set below.
		if role == roles.ViewOwner {
			continue
		}
		readerOK := kind == "v" && role == roles.Reader && assistReaderViews[fq]
		if (sel || anyCol) && !readerOK {
			add("role %s holds a SELECT privilege on %s outside the plan's list", role, fq)
		}
	}

	// 3b. The view owner's column grants, as an exact set.
	rows, err = collect(`
		SELECT n.nspname || '.' || c.relname || '.' || a.attname
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
		WHERE c.relkind IN ('r', 'p', 'v', 'm', 'f') AND n.nspname NOT IN ('pg_catalog', 'information_schema', 'ai', 'ai_pro')
		  AND n.nspname NOT LIKE 'pg\_%' AND c.relowner <> (SELECT oid FROM pg_roles WHERE rolname = $1)
		  AND has_column_privilege($1, c.oid, a.attnum, 'SELECT,INSERT,UPDATE,REFERENCES')`, roles.ViewOwner)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, row := range rows {
		have[row[0].(string)] = true
	}
	want := map[string]bool{}
	for table, cols := range viewOwnerColumns {
		for _, col := range cols {
			want[table+"."+col] = true
		}
	}
	for col := range have {
		if !want[col] {
			add("the view owner can read %s outside the plan's list", col)
		}
	}
	// The planned columns are required only once the data slice's views exist (a database
	// that has run 0418 alone, as the planted mistake tests build, has none yet).
	var viewsExist bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass('ai.orders') IS NOT NULL`).Scan(&viewsExist); err != nil {
		return nil, err
	}
	for col := range want {
		if viewsExist && !have[col] {
			add("the view owner lacks the planned column %s", col)
		}
	}

	// 4. Function privileges: ai.claims() for the readers, hmac for the claims owner.
	rows, err = collect(`
		SELECT r.rolname, n.nspname, p.proname, pg_get_function_identity_arguments(p.oid), p.proowner = r.oid,
		       EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_proc'::regclass AND d.objid = p.oid AND d.deptype = 'e')
		FROM pg_roles r, pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE r.rolname = ANY($1) AND n.nspname NOT IN ('pg_catalog', 'information_schema') AND n.nspname NOT LIKE 'pg\_%'
		  AND has_function_privilege(r.oid, p.oid, 'EXECUTE')`, roles.all())
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		role, schema, fn, args, owner := row[0].(string), row[1].(string), row[2].(string), row[3].(string), row[4].(bool)
		ext := row[5].(bool)
		ok := owner || (extensionsOpen && ext) ||
			((role == roles.Reader || role == roles.ProReader) && schema == "ai" && fn == "claims" && args == "") ||
			(role == roles.ClaimsOwner && fn == "hmac" && args == "text, text, text")
		if !ok {
			add("role %s can EXECUTE %s.%s(%s) outside the plan's list", role, schema, fn, args)
		}
	}

	// 4b. PUBLIC and functions outside the system schemas, ai and ai_pro: none, except in
	// the open state the extension functions the migrating role does not own, exactly.
	rows, err = collect(`
		SELECT n.nspname, p.proname, pg_get_function_identity_arguments(p.oid), pg_get_userbyid(p.proowner)::text,
		       EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_proc'::regclass AND d.objid = p.oid AND d.deptype = 'e'),
		       has_function_privilege('public', p.oid, 'EXECUTE')
		FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname NOT IN ('pg_catalog', 'information_schema', 'ai', 'ai_pro') AND n.nspname NOT LIKE 'pg\_%'`)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		schema, fn, args, owner := row[0].(string), row[1].(string), row[2].(string), row[3].(string)
		ext, public := row[4].(bool), row[5].(bool)
		sig := fmt.Sprintf("%s.%s(%s)", schema, fn, args)
		switch {
		case public && !ext:
			add("PUBLIC can EXECUTE %s, which is not an extension function", sig)
		case public && ext && !extensionsOpen:
			add("PUBLIC can EXECUTE the extension function %s", sig)
		case public && ext && owner == migrating:
			add("PUBLIC can EXECUTE the extension function %s although the migrating role owns it", sig)
		case !public && ext && extensionsOpen && owner != migrating:
			add("the extension function %s (owned by %s) is closed to PUBLIC, so this is not the open state", sig, owner)
		}
	}

	// 5. Schema and database privileges.
	rows, err = collect(`
		SELECT r.rolname, n.nspname FROM pg_roles r, pg_namespace n
		WHERE r.rolname = ANY($1) AND n.nspname NOT LIKE 'pg\_%' AND n.nspname <> 'information_schema'
		  AND has_schema_privilege(r.oid, n.oid, 'CREATE')`, roles.all())
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		role, schema := row[0].(string), row[1].(string)
		ok := (schema == "ai" && (role == roles.ClaimsOwner || role == roles.ViewOwner)) || (schema == "ai_pro" && role == roles.ViewOwner)
		if !ok {
			add("role %s holds CREATE on schema %s", role, schema)
		}
	}
	rows, err = collect(`SELECT r.rolname, p FROM pg_roles r, unnest(ARRAY['CREATE', 'TEMP']) p
		WHERE r.rolname = ANY($1) AND has_database_privilege(r.oid, current_database(), p)`, roles.all())
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		add("role %s holds %s on the database", row[0], row[1])
	}
	return out, nil
}

// The migrated and seeded gate database passes.
func TestCatalogueOfTheMigratedDatabase(t *testing.T) {
	conn := connect(t, gateURL(t))
	got, err := catalogueViolations(ctxT(t), conn, RolesFor(currentDatabase(t, conn)), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range got {
		t.Errorf("catalogue: %s", v)
	}
}

// Red team 16: the catalogue test bites. Each planted mistake, made as the
// superuser the way a careless migration would, must be reported, and an object
// created the right way (under SET LOCAL ROLE) must not be.
func TestCatalogueTestBitesOnEveryPlantedMistake(t *testing.T) {
	conn, db := freshMigratedDB(t, "readtx_cat")
	roles := RolesFor(db)
	if got, err := catalogueViolations(ctxT(t), conn, roles, false); err != nil || len(got) != 0 {
		t.Fatalf("a freshly migrated database must be clean: %v %v", got, err)
	}
	if _, err := conn.Exec(ctxT(t), `CREATE FUNCTION public.outside_fn() RETURNS int LANGUAGE sql AS 'SELECT 1'`); err != nil {
		t.Fatal(err)
	}
	q := pgx.Identifier.Sanitize
	var appRole string
	if err := conn.QueryRow(ctxT(t), `SELECT current_user`).Scan(&appRole); err != nil {
		t.Fatal(err)
	}
	plants := []struct{ name, plant, revert, mention string }{
		{"a view created by the superuser", `CREATE VIEW ai.bad_view AS SELECT 1 AS x`, `DROP VIEW ai.bad_view`, "ai.bad_view"},
		{"a function created by the superuser", `CREATE FUNCTION ai.bad_fn() RETURNS int LANGUAGE sql AS 'SELECT 1'`, `DROP FUNCTION ai.bad_fn()`, "bad_fn"},
		{"the verifier owned by the view owner", `ALTER FUNCTION ai.claims() OWNER TO ` + q(pgx.Identifier{roles.ViewOwner}), `ALTER FUNCTION ai.claims() OWNER TO ` + q(pgx.Identifier{roles.ClaimsOwner}), "ai.claims"},
		{"the verifier owned by the superuser", `ALTER FUNCTION ai.claims() OWNER TO ` + q(pgx.Identifier{appRole}), `ALTER FUNCTION ai.claims() OWNER TO ` + q(pgx.Identifier{roles.ClaimsOwner}), "holds superuser"},
		{"a login in the function users role", `GRANT ` + q(pgx.Identifier{roles.FunctionUsers}) + ` TO ` + q(pgx.Identifier{roles.ProLogin}), `REVOKE ` + q(pgx.Identifier{roles.FunctionUsers}) + ` FROM ` + q(pgx.Identifier{roles.ProLogin}), "function users role"},
		{"a login in pg_monitor", `GRANT pg_monitor TO ` + q(pgx.Identifier{roles.Login}), `REVOKE pg_monitor FROM ` + q(pgx.Identifier{roles.Login}), "pg_monitor"},
		{"a reader with BYPASSRLS", `ALTER ROLE ` + q(pgx.Identifier{roles.Reader}) + ` BYPASSRLS`, `ALTER ROLE ` + q(pgx.Identifier{roles.Reader}) + ` NOBYPASSRLS`, "rolbypassrls"},
		{"a login with CREATEROLE", `ALTER ROLE ` + q(pgx.Identifier{roles.ProLogin}) + ` CREATEROLE`, `ALTER ROLE ` + q(pgx.Identifier{roles.ProLogin}) + ` NOCREATEROLE`, "rolcreaterole"},
		{"a login that can SET ROLE to the view owner", `GRANT ` + q(pgx.Identifier{roles.ViewOwner}) + ` TO ` + q(pgx.Identifier{roles.Login}), `REVOKE ` + q(pgx.Identifier{roles.ViewOwner}) + ` FROM ` + q(pgx.Identifier{roles.Login}), "is a member of"},
		{"the claims key readable by a reader", `GRANT SELECT ON ai.claims_key TO ` + q(pgx.Identifier{roles.Reader}), `REVOKE SELECT ON ai.claims_key FROM ` + q(pgx.Identifier{roles.Reader}), "ai.claims_key"},
		{"an outside function executable by a reader", `GRANT EXECUTE ON FUNCTION public.outside_fn() TO ` + q(pgx.Identifier{roles.Reader}), `REVOKE EXECUTE ON FUNCTION public.outside_fn() FROM ` + q(pgx.Identifier{roles.Reader}), "outside_fn"},
		{"TEMP granted to PUBLIC", `GRANT TEMP ON DATABASE ` + q(pgx.Identifier{db}) + ` TO PUBLIC`, `REVOKE TEMP ON DATABASE ` + q(pgx.Identifier{db}) + ` FROM PUBLIC`, "TEMP"},
		{"CREATE on public for a login", `GRANT CREATE ON SCHEMA public TO ` + q(pgx.Identifier{roles.Login}), `REVOKE CREATE ON SCHEMA public FROM ` + q(pgx.Identifier{roles.Login}), "schema public"},
	}
	for _, p := range plants {
		if _, err := conn.Exec(ctxT(t), p.plant); err != nil {
			t.Fatalf("%s: plant: %v", p.name, err)
		}
		got, err := catalogueViolations(ctxT(t), conn, roles, false)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, v := range got {
			if strings.Contains(v, p.mention) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: the catalogue test did not report it (got %v)", p.name, got)
		}
		if _, err := conn.Exec(ctxT(t), p.revert); err != nil {
			t.Fatalf("%s: revert: %v", p.name, err)
		}
		if got, err := catalogueViolations(ctxT(t), conn, roles, false); err != nil || len(got) != 0 {
			t.Fatalf("%s: not clean after the revert: %v %v", p.name, got, err)
		}
	}
	// The right way: created under SET LOCAL ROLE, owned by the view owner, passes.
	tx, err := conn.Begin(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		`SET LOCAL ROLE ` + q(pgx.Identifier{roles.ViewOwner}),
		`CREATE VIEW ai.good_view AS SELECT 1 AS x`,
		`CREATE FUNCTION ai.good_fn() RETURNS int LANGUAGE sql AS 'SELECT 1'`,
		`RESET ROLE`,
	} {
		if _, err := tx.Exec(ctxT(t), s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	if err := tx.Commit(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	if got, err := catalogueViolations(ctxT(t), conn, roles, false); err != nil || len(got) != 0 {
		t.Errorf("objects created under the owner role must pass: %v %v", got, err)
	}
}

// The slice 5 additions bite: one column beyond the view owner's exact list, and a
// reader grant on a base table, are each reported (and put back).
func TestCatalogueBitesOnAnExtraColumnAndAReaderTableGrant(t *testing.T) {
	conn := connect(t, gateURL(t))
	roles := RolesFor(currentDatabase(t, conn))
	q := pgx.Identifier.Sanitize
	for _, p := range []struct{ name, plant, revert, mention string }{
		{"a contested column for the view owner", `GRANT SELECT (job_address) ON public.orders TO ` + q(pgx.Identifier{roles.ViewOwner}), `REVOKE SELECT (job_address) ON public.orders FROM ` + q(pgx.Identifier{roles.ViewOwner}), "public.orders.job_address"},
		{"a whole table for the view owner", `GRANT SELECT ON public.customers TO ` + q(pgx.Identifier{roles.ViewOwner}), `REVOKE SELECT ON public.customers FROM ` + q(pgx.Identifier{roles.ViewOwner}) + `; GRANT SELECT (id, name) ON public.customers TO ` + q(pgx.Identifier{roles.ViewOwner}), "public.customers.account_no"},
		{"a base table for the reader", `GRANT SELECT ON public.orders TO ` + q(pgx.Identifier{roles.Reader}), `REVOKE SELECT ON public.orders FROM ` + q(pgx.Identifier{roles.Reader}), "public.orders"},
	} {
		if _, err := conn.Exec(ctxT(t), p.plant); err != nil {
			t.Fatalf("%s: %v", p.name, err)
		}
		got, err := catalogueViolations(ctxT(t), conn, roles, false)
		_, _ = conn.Exec(ctxT(t), p.revert)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, v := range got {
			found = found || strings.Contains(v, p.mention)
		}
		if !found {
			t.Errorf("%s was not reported: %v", p.name, got)
		}
	}
	if got, err := catalogueViolations(ctxT(t), conn, roles, false); err != nil || len(got) != 0 {
		t.Fatalf("not clean after the reverts: %v %v", got, err)
	}
}
