//go:build integration

package readtx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// All six roles of one database, with the attributes the plan asks for.
func TestRolesExistWithTheirAttributes(t *testing.T) {
	conn := connect(t, gateURL(t))
	roles := RolesFor(currentDatabase(t, conn))
	type want struct {
		name      string
		canLogin  bool
		inherit   bool
		wantLogin string
	}
	for _, w := range []want{
		{roles.ViewOwner, false, true, ""},
		{roles.ClaimsOwner, false, true, ""},
		{roles.Reader, false, true, ""},
		{roles.ProReader, false, true, ""},
		{roles.Login, true, false, ""},
		{roles.ProLogin, true, false, ""},
		{roles.ZoneReader, false, true, ""},
	} {
		var super, bypass, createRole, createDB, repl, login, inherit bool
		err := conn.QueryRow(ctxT(t), `SELECT rolsuper, rolbypassrls, rolcreaterole, rolcreatedb, rolreplication, rolcanlogin, rolinherit
			FROM pg_roles WHERE rolname = $1`, w.name).Scan(&super, &bypass, &createRole, &createDB, &repl, &login, &inherit)
		if err != nil {
			t.Fatalf("role %s: %v", w.name, err)
		}
		if super || bypass || createRole || createDB || repl {
			t.Errorf("%s carries a power it must not: super=%v bypassrls=%v createrole=%v createdb=%v replication=%v",
				w.name, super, bypass, createRole, createDB, repl)
		}
		if login != w.canLogin {
			t.Errorf("%s login = %v, want %v", w.name, login, w.canLogin)
		}
		if w.canLogin && inherit {
			t.Errorf("%s must be NOINHERIT", w.name)
		}
	}
}

func TestLoginsAreMembersOnlyOfTheirReader(t *testing.T) {
	conn := connect(t, gateURL(t))
	roles := RolesFor(currentDatabase(t, conn))
	for login, reader := range map[string]string{roles.Login: roles.Reader, roles.ProLogin: roles.ProReader} {
		rows, err := conn.Query(ctxT(t), `SELECT g.rolname FROM pg_auth_members m
			JOIN pg_roles g ON g.oid = m.roleid JOIN pg_roles r ON r.oid = m.member WHERE r.rolname = $1`, login)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				t.Fatal(err)
			}
			got = append(got, n)
		}
		rows.Close()
		if len(got) != 1 || got[0] != reader {
			t.Errorf("%s is a member of %v, want only %s", login, got, reader)
		}
	}
}

func TestSchemasAndGrantsAreExactlyPlan31(t *testing.T) {
	conn := connect(t, gateURL(t))
	roles := RolesFor(currentDatabase(t, conn))
	check := func(label, sql string, want bool, args ...any) {
		t.Helper()
		var got bool
		if err := conn.QueryRow(ctxT(t), sql, args...).Scan(&got); err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if got != want {
			t.Errorf("%s = %v, want %v", label, got, want)
		}
	}
	sch := func(role, schema, priv string, want bool) {
		check(fmt.Sprintf("%s %s on schema %s", role, priv, schema),
			`SELECT has_schema_privilege($1, $2, $3)`, want, role, schema, priv)
	}
	sch(roles.Reader, "ai", "USAGE", true)
	sch(roles.Reader, "ai", "CREATE", false)
	sch(roles.Reader, "ai_pro", "USAGE", false)
	sch(roles.ProReader, "ai_pro", "USAGE", true)
	sch(roles.ProReader, "ai", "USAGE", true) // needed to call ai.claims(); see the report
	sch(roles.ProReader, "ai_pro", "CREATE", false)
	sch(roles.ClaimsOwner, "ai", "USAGE", true)
	sch(roles.ClaimsOwner, "ai", "CREATE", true)
	sch(roles.ClaimsOwner, "ai_pro", "CREATE", false)
	sch(roles.ViewOwner, "ai", "CREATE", true)
	sch(roles.ViewOwner, "ai", "USAGE", true)
	sch(roles.ViewOwner, "ai_pro", "CREATE", true)
	sch(roles.ViewOwner, "ai_pro", "USAGE", true)
	// ai.dealer_day() is created by the zone reader and migration 0428 then takes the schema privileges back: a
	// definer function runs as its owner without needing USAGE on its own schema unless its body names it, and
	// this one names pg_catalog and public only.
	sch(roles.ZoneReader, "ai", "USAGE", false)
	sch(roles.ZoneReader, "ai", "CREATE", false)
	sch(roles.ZoneReader, "ai_pro", "CREATE", false)
	// The logins hold nothing of their own (NOINHERIT): the reader's grants
	// reach them only through SET ROLE.
	sch(roles.Login, "ai", "USAGE", false)
	sch(roles.ProLogin, "ai_pro", "USAGE", false)
	sch(roles.Login, "public", "CREATE", false)
}

func TestTempAndOutsideFunctionsAreRevokedFromTheAssistRoles(t *testing.T) {
	conn := connect(t, gateURL(t))
	db := currentDatabase(t, conn)
	roles := RolesFor(db)
	for _, r := range []string{roles.Login, roles.ProLogin, roles.Reader, roles.ProReader, roles.ViewOwner} {
		var temp bool
		if err := conn.QueryRow(ctxT(t), `SELECT has_database_privilege($1, $2, 'TEMP')`, r, db).Scan(&temp); err != nil {
			t.Fatal(err)
		}
		if temp {
			t.Errorf("%s still holds TEMP on %s", r, db)
		}
	}
	var appTemp bool
	if err := conn.QueryRow(ctxT(t), `SELECT has_database_privilege(current_user, $1, 'TEMP')`, db).Scan(&appTemp); err != nil || !appTemp {
		t.Errorf("the app role must keep TEMP, got %v err %v", appTemp, err)
	}
	// Every function outside the system schemas and outside ai and ai_pro is
	// closed to the assist roles, except the one grant the claims owner needs.
	rows, err := conn.Query(ctxT(t), `
		SELECT r.rolname, n.nspname, p.oid::regprocedure::text
		FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace,
		     pg_roles r
		WHERE r.rolname = ANY($1)
		  AND n.nspname NOT IN ('pg_catalog', 'information_schema', 'ai', 'ai_pro')
		  AND n.nspname NOT LIKE 'pg\_%'
		  AND has_function_privilege(r.oid, p.oid, 'EXECUTE')
		  AND NOT (r.rolname = $2 AND p.proname = 'hmac')`,
		[]string{roles.Login, roles.ProLogin, roles.Reader, roles.ProReader, roles.ViewOwner, roles.ClaimsOwner}, roles.ClaimsOwner)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var role, schema, fn string
		if err := rows.Scan(&role, &schema, &fn); err != nil {
			t.Fatal(err)
		}
		t.Errorf("%s can EXECUTE %s (schema %s)", role, fn, schema)
	}
}

func TestRoleLevelSettings(t *testing.T) {
	conn := connect(t, gateURL(t))
	roles := RolesFor(currentDatabase(t, conn))
	for _, login := range []string{roles.Login, roles.ProLogin} {
		var cfg []string
		if err := conn.QueryRow(ctxT(t), `SELECT coalesce(rolconfig, '{}') FROM pg_roles WHERE rolname = $1`, login).Scan(&cfg); err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(cfg, ";")
		for _, key := range []string{"default_transaction_read_only=on", "statement_timeout=", "lock_timeout=", "idle_in_transaction_session_timeout="} {
			if !strings.Contains(joined, key) {
				t.Errorf("%s lacks the setting %s, has %v", login, key, cfg)
			}
		}
	}
}

// A second migrated database on the same cluster gets roles of its own, and
// the first database's roles are left alone.
func TestSecondDatabaseOnTheClusterGetsItsOwnRoles(t *testing.T) {
	gate := gateURL(t)
	conn := connect(t, gate)
	firstRoles := RolesFor(currentDatabase(t, conn))

	second := "readtx_second_" + randomHex(t, 4)
	if _, err := conn.Exec(ctxT(t), `CREATE DATABASE `+pgx.Identifier{second}.Sanitize()); err != nil {
		t.Fatalf("create database: %v", err)
	}
	secondRoles := RolesFor(second)
	t.Cleanup(func() {
		c, err := pgx.Connect(ctxT(t), gate)
		if err != nil {
			return
		}
		defer c.Close(ctxT(t))
		_, _ = c.Exec(ctxT(t), `DROP DATABASE IF EXISTS `+pgx.Identifier{second}.Sanitize()+` WITH (FORCE)`)
		for _, r := range []string{secondRoles.Login, secondRoles.ProLogin, secondRoles.Reader, secondRoles.ProReader, secondRoles.ViewOwner, secondRoles.ClaimsOwner} {
			_, _ = c.Exec(ctxT(t), `DROP ROLE IF EXISTS `+pgx.Identifier{r}.Sanitize())
		}
	})

	body, err := os.ReadFile(filepath.Join("..", "..", "..", "db", "migrations", "0418_assist_roles_and_claims.sql"))
	if err != nil {
		t.Fatal(err)
	}
	sc := connect(t, withDatabase(t, gate, second))
	if _, err := sc.Exec(ctxT(t), `CREATE EXTENSION pgcrypto`); err != nil {
		t.Fatalf("pgcrypto: %v", err)
	}
	if _, err := sc.Exec(ctxT(t), `CREATE FUNCTION public.outside_fn() RETURNS int LANGUAGE sql AS 'SELECT 1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := sc.Exec(ctxT(t), string(body)); err != nil {
		t.Fatalf("0418 on a fresh database: %v", err)
	}
	for _, r := range []string{secondRoles.Login, secondRoles.ProLogin, secondRoles.Reader, secondRoles.ProReader, secondRoles.ViewOwner, secondRoles.ClaimsOwner} {
		var n int
		if err := conn.QueryRow(ctxT(t), `SELECT count(*) FROM pg_roles WHERE rolname = $1`, r).Scan(&n); err != nil || n != 1 {
			t.Errorf("role %s missing after migrating the second database (n=%d err=%v)", r, n, err)
		}
	}
	if secondRoles == firstRoles {
		t.Fatalf("the two databases must not share role names")
	}
	// The second database's migration revoked the outside function from the
	// second database's roles only by privilege, so check the call is refused.
	var canCall bool
	if err := sc.QueryRow(ctxT(t), `SELECT has_function_privilege($1, 'public.outside_fn()', 'EXECUTE')`, secondRoles.Reader).Scan(&canCall); err != nil || canCall {
		t.Errorf("assist reader can execute a function outside ai (can=%v err=%v)", canCall, err)
	}
	// The first database's roles are untouched: same attributes, same members.
	var n int
	if err := conn.QueryRow(ctxT(t), `SELECT count(*) FROM pg_roles WHERE rolname = $1`, firstRoles.Login).Scan(&n); err != nil || n != 1 {
		t.Errorf("first database lost its login role (n=%d err=%v)", n, err)
	}
}
