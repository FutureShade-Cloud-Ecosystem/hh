//go:build integration

package readtx

// Red team cases 6 and 16 of the TanyAI plan (section 7), proven at the database:
// raw SQL on a connection that logs in as the real assist login (or its contractor
// twin) with a test password, bypassing every Go helper in this package. Each
// attack must be refused by privilege (SQLSTATE 42501) or verify to NULL, and a
// correctly signed claim for this backend must work, so the suite cannot pass by
// everything being broken.

import (
	"errors"
	"fmt"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/redact"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type audienceCase struct {
	name     string
	login    string
	reader   string
	other    string // the other audience's reader role
	audience string
}

func setupRedTeam(t *testing.T) (admin *pgx.Conn, roles Roles, appRole string, key string, cases []audienceCase, urls map[string]string) {
	t.Helper()
	admin = connect(t, gateURL(t))
	db := currentDatabase(t, admin)
	roles = RolesFor(db)
	if err := admin.QueryRow(ctxT(t), `SELECT current_user`).Scan(&appRole); err != nil {
		t.Fatal(err)
	}
	_, key = testKey(t, admin)
	staffURL, proURL := loginPasswords(t, admin)
	urls = map[string]string{roles.Login: staffURL, roles.ProLogin: proURL}
	cases = []audienceCase{
		{"staff", roles.Login, roles.Reader, roles.ProReader, AudienceStaff},
		{"pro", roles.ProLogin, roles.ProReader, roles.Reader, AudiencePro},
	}
	return
}

// refused runs sql on conn and requires insufficient_privilege. Any other error
// (a missing function, a syntax slip) fails the test, so a refusal cannot be
// vacuous.
func refused(t *testing.T, conn *pgx.Conn, label, sql string, args ...any) {
	t.Helper()
	_, err := conn.Exec(ctxT(t), sql, args...)
	var pg *pgconn.PgError
	if err == nil {
		t.Errorf("%s: ran, it must be refused", label)
		return
	}
	if !errors.As(err, &pg) || pg.Code != "42501" {
		t.Errorf("%s: want 42501 insufficient_privilege, got %v", label, err)
	}
}

func TestRawSQLAsTheAssistLoginsReachesNothing(t *testing.T) {
	admin, roles, appRole, key, cases, urls := setupRedTeam(t)
	_ = key
	var nextOrderNo string
	if err := admin.QueryRow(ctxT(t), `
		SELECT 'public.next_order_no(' || coalesce((SELECT string_agg('NULL::' || t::regtype::text, ', ' ORDER BY o)
		   FROM unnest(p.proargtypes::oid[]) WITH ORDINALITY AS u(t, o)), '') || ')'
		FROM pg_proc p WHERE p.proname = 'next_order_no' LIMIT 1`).Scan(&nextOrderNo); err != nil {
		t.Skipf("next_order_no not found: %v", err)
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			conn := connect(t, urls[c.login])
			var user string
			if err := conn.QueryRow(ctxT(t), `SELECT session_user`).Scan(&user); err != nil || user != c.login {
				t.Fatalf("connected as %q (err %v), want %s", user, err, c.login)
			}

			// Straight from the login, before any SET ROLE: nothing.
			refused(t, conn, "login reads orders", `SELECT count(*) FROM public.orders`)
			refused(t, conn, "login reads the claims key", `SELECT * FROM ai.claims_key`)
			refused(t, conn, "login calls ai.claims()", `SELECT ai.claims()`)

			// RESET ROLE, then a base table.
			if _, err := conn.Exec(ctxT(t), `RESET ROLE`); err != nil {
				t.Fatal(err)
			}
			refused(t, conn, "RESET ROLE then orders", `SELECT count(*) FROM public.orders`)

			// SET ROLE to everything it must not become.
			for label, role := range map[string]string{
				"view owner": roles.ViewOwner, "claims owner": roles.ClaimsOwner, "app role": appRole,
				"the other audience's reader": c.other, "the other login": map[string]string{roles.Login: roles.ProLogin, roles.ProLogin: roles.Login}[c.login],
			} {
				refused(t, conn, "SET ROLE "+label, `SET ROLE `+pgx.Identifier{role}.Sanitize())
			}
			refused(t, conn, "SET SESSION AUTHORIZATION the app role", `SET SESSION AUTHORIZATION `+pgx.Identifier{appRole}.Sanitize())

			// The one role it can become, and what that role cannot do.
			if _, err := conn.Exec(ctxT(t), `SET ROLE `+pgx.Identifier{c.reader}.Sanitize()); err != nil {
				t.Fatalf("the login must be able to become its reader: %v", err)
			}
			refused(t, conn, "reader reads orders", `SELECT count(*) FROM public.orders`)
			refused(t, conn, "reader reads the claims key", `SELECT * FROM ai.claims_key`)
			for label, role := range map[string]string{"view owner": roles.ViewOwner, "claims owner": roles.ClaimsOwner, "app role": appRole} {
				refused(t, conn, "reader SET ROLE "+label, `SET ROLE `+pgx.Identifier{role}.Sanitize())
			}
			refused(t, conn, "reader RESET ROLE then SET ROLE view owner", `SET ROLE `+pgx.Identifier{roles.ViewOwner}.Sanitize())

			// Functions outside ai, pgcrypto's included, and the server file reads.
			refused(t, conn, "next_order_no", `SELECT `+nextOrderNo)
			refused(t, conn, "pgcrypto digest", `SELECT public.digest('a', 'sha256')`)
			refused(t, conn, "pgcrypto hmac", `SELECT public.hmac('a', 'k', 'sha256')`)
			refused(t, conn, "pgcrypto crypt", `SELECT public.crypt('a', public.gen_salt('bf'))`)
			refused(t, conn, "pg_read_file", `SELECT pg_read_file('/etc/hostname')`)
			refused(t, conn, "pg_ls_dir", `SELECT pg_ls_dir('.')`)
			refused(t, conn, "COPY PROGRAM", `COPY (SELECT 1) TO PROGRAM 'true'`)

			// With the role default of read only in place the writes below stop earlier, at
			// 25006; check that, then switch the session to read write, as an attacker
			// would, so that what refuses them is privilege alone.
			_, err := conn.Exec(ctxT(t), `INSERT INTO public.orders DEFAULT VALUES`)
			var pg *pgconn.PgError
			if !errors.As(err, &pg) || pg.Code != "25006" {
				t.Errorf("the role default must make a write fail read only (25006), got %v", err)
			}
			if _, err := conn.Exec(ctxT(t), `SET default_transaction_read_only = off`); err != nil {
				t.Fatal(err)
			}

			// No temp tables, no objects, no writes.
			refused(t, conn, "CREATE TEMP TABLE", `CREATE TEMP TABLE leak (x int)`)
			refused(t, conn, "CREATE TABLE in public", `CREATE TABLE public.leak (x int)`)
			refused(t, conn, "CREATE TABLE in ai", `CREATE TABLE ai.leak (x int)`)
			refused(t, conn, "CREATE FUNCTION in ai", `CREATE FUNCTION ai.evil() RETURNS int LANGUAGE sql AS 'SELECT 1'`)
			refused(t, conn, "CREATE SCHEMA", `CREATE SCHEMA leak`)
			refused(t, conn, "INSERT into orders", `INSERT INTO public.orders DEFAULT VALUES`)
			refused(t, conn, "UPDATE orders", `UPDATE public.orders SET branch_id = branch_id`)
			refused(t, conn, "DELETE orders", `DELETE FROM public.orders`)
			refused(t, conn, "INSERT into the claims key", `INSERT INTO ai.claims_key (key_id, key) VALUES (1, 'x')`)
			refused(t, conn, "TRUNCATE orders", `TRUNCATE public.orders`)
		})
	}
}

func TestRawSQLForgedClaimsVerifyToNull(t *testing.T) {
	admin, roles, _, key, cases, urls := setupRedTeam(t)
	kid := int(KeyID(redact.NewSecret(key)))
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			conn := connect(t, urls[c.login])
			pid := backendPID(t, conn)
			if _, err := conn.Exec(ctxT(t), `SET ROLE `+pgx.Identifier{c.reader}.Sanitize()); err != nil {
				t.Fatal(err)
			}
			good := func() map[string]any {
				p := goodPayload(pid)
				p["audience"] = c.audience
				return p
			}
			// The positive control: a correctly signed claim for this backend verifies.
			ok, asker := verified(t, conn, rawSigned(kid, key, good()))
			if !ok || asker == "" {
				t.Fatalf("the positive control failed: a correctly signed claim for this pid returned NULL (ok=%v)", ok)
			}
			goodTok := rawSigned(kid, key, good())
			parts := strings.Split(goodTok, ".")
			editedBody := good()
			editedBody["global"] = true
			editedBody["branch_ids"] = []string{"99999999-9999-9999-9999-999999999999"}
			eb, _ := jsonB64(editedBody)
			attacks := map[string]string{
				"forged mac":                strings.Join([]string{parts[0], parts[1], parts[2], strings.Repeat("a", 64)}, "."),
				"edited payload":            strings.Join([]string{parts[0], parts[1], eb, parts[3]}, "."),
				"expired":                   rawSigned(kid, key, func() map[string]any { p := good(); p["exp"] = time.Now().Add(-time.Second).Unix(); return p }()),
				"another pid":               rawSigned(kid, key, func() map[string]any { p := good(); p["pid"] = pid + 1; return p }()),
				"signed with a guessed key": rawSigned(kid, "guess-"+key, good()),
				"unknown key id":            rawSigned(kid+1, key, good()),
				"empty":                     "",
			}
			for name, v := range attacks {
				if ok, _ := verified(t, conn, v); ok {
					t.Errorf("%s verified, it must return NULL", name)
				}
			}
			// set_config of a forged value, in a transaction, the way the compiler would.
			tx, err := conn.Begin(ctxT(t))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(ctxT(t), `SELECT set_config('ai.claims', $1, true)`, attacks["forged mac"]); err != nil {
				t.Fatal(err)
			}
			var asker2 *string
			if err := tx.QueryRow(ctxT(t), `SELECT (ai.claims()).asker_id`).Scan(&asker2); err != nil || asker2 != nil {
				t.Errorf("forged set_config verified: %v %v", asker2, err)
			}
			_ = tx.Rollback(ctxT(t))
			// The control still works after every attack.
			if ok, _ := verified(t, conn, goodTok); !ok {
				t.Error("the good claim stopped verifying")
			}
			_ = admin
			_ = roles
		})
	}
}

// The verifier runs as the claims owner and nothing else can: the definer is not
// the app role, and the claims owner is not a superuser (red team 16).
func TestVerifierRunsAsTheClaimsOwner(t *testing.T) {
	admin, roles, appRole, _, _, _ := setupRedTeam(t)
	var owner string
	var super bool
	if err := admin.QueryRow(ctxT(t), `SELECT r.rolname, r.rolsuper FROM pg_proc p JOIN pg_roles r ON r.oid = p.proowner
		WHERE p.oid = 'ai.claims()'::regprocedure`).Scan(&owner, &super); err != nil {
		t.Fatal(err)
	}
	if owner != roles.ClaimsOwner || super || owner == appRole {
		t.Errorf("ai.claims() owner = %s (super=%v), want the non superuser %s", owner, super, roles.ClaimsOwner)
	}
}

func jsonB64(m map[string]any) (string, error) {
	tok := rawSigned(1, "x", m)
	parts := strings.Split(tok, ".")
	if len(parts) != 4 {
		return "", fmt.Errorf("unexpected token shape")
	}
	return parts[2], nil
}
