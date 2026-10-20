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

func (r Roles) all() []string {
	return []string{r.ViewOwner, r.ClaimsOwner, r.Reader, r.ProReader, r.Login, r.ProLogin}
}

func catalogueViolations(ctx context.Context, conn *pgx.Conn, roles Roles) ([]string, error) {
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
	for _, reader := range []string{roles.Reader, roles.ProReader} {
		rows, err := collect(`SELECT r.rolname FROM pg_auth_members m JOIN pg_roles g ON g.oid = m.roleid
			JOIN pg_roles r ON r.oid = m.member WHERE g.rolname = $1 ORDER BY 1`, reader)
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
		readerOK := kind == "v" && ((role == roles.Reader && schema == "ai") || (role == roles.ProReader && schema == "ai_pro"))
		if (sel || anyCol) && !readerOK {
			add("role %s holds a SELECT privilege on %s outside the plan's list", role, fq)
		}
	}

	// 4. Function privileges: ai.claims() for the readers, hmac for the claims owner.
	rows, err = collect(`
		SELECT r.rolname, n.nspname, p.proname, pg_get_function_identity_arguments(p.oid), p.proowner = r.oid
		FROM pg_roles r, pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE r.rolname = ANY($1) AND n.nspname NOT IN ('pg_catalog', 'information_schema') AND n.nspname NOT LIKE 'pg\_%'
		  AND has_function_privilege(r.oid, p.oid, 'EXECUTE')`, roles.all())
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		role, schema, fn, args, owner := row[0].(string), row[1].(string), row[2].(string), row[3].(string), row[4].(bool)
		ok := owner ||
			((role == roles.Reader || role == roles.ProReader) && schema == "ai" && fn == "claims" && args == "") ||
			(role == roles.ClaimsOwner && fn == "hmac" && args == "text, text, text")
		if !ok {
			add("role %s can EXECUTE %s.%s(%s) outside the plan's list", role, schema, fn, args)
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
	got, err := catalogueViolations(ctxT(t), conn, RolesFor(currentDatabase(t, conn)))
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
	if got, err := catalogueViolations(ctxT(t), conn, roles); err != nil || len(got) != 0 {
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
		got, err := catalogueViolations(ctxT(t), conn, roles)
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
		if got, err := catalogueViolations(ctxT(t), conn, roles); err != nil || len(got) != 0 {
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
	if got, err := catalogueViolations(ctxT(t), conn, roles); err != nil || len(got) != 0 {
		t.Errorf("objects created under the owner role must pass: %v %v", got, err)
	}
}
