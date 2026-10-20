//go:build integration

package readtx

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// 0418 closes EXECUTE on every function outside ai to PUBLIC. A serving role that
// is separate from the migrating role and not a superuser must keep working once
// it is a member of the per database function users role, for the functions that
// exist at migration time and the ones created after it.
func TestSeparateServingRoleKeepsItsFunctionsThroughTheFunctionUsersRole(t *testing.T) {
	conn, db := freshMigratedDBBefore(t, "readtx_fn", "citext", "pg_trgm")
	roles := RolesFor(db)
	serve, stranger := "readtx_serve_"+randomHex(t, 4), "readtx_stranger_"+randomHex(t, 4)
	for _, r := range []string{serve, stranger} {
		r := r
		if _, err := conn.Exec(ctxT(t), `CREATE ROLE `+pgx.Identifier{r}.Sanitize()+` NOLOGIN NOSUPERUSER`); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			c, err := pgx.Connect(ctxT(t), gateURL(t))
			if err == nil {
				defer c.Close(ctxT(t))
				_, _ = c.Exec(ctxT(t), `DROP ROLE IF EXISTS `+pgx.Identifier{r}.Sanitize())
			}
		})
	}
	if _, err := conn.Exec(ctxT(t), `GRANT `+pgx.Identifier{roles.FunctionUsers}.Sanitize()+` TO `+pgx.Identifier{serve}.Sanitize()); err != nil {
		t.Fatalf("the function users role must exist and be grantable: %v", err)
	}
	if _, err := conn.Exec(ctxT(t), `CREATE FUNCTION public.created_after() RETURNS int LANGUAGE sql AS 'SELECT 7'`); err != nil {
		t.Fatal(err)
	}
	calls := map[string]string{
		"citext comparison": `SELECT 'a'::citext = 'A'::citext`,
		"pg_trgm":           `SELECT similarity('abc', 'abd')`,
		"pgcrypto":          `SELECT encode(digest('a', 'sha256'), 'hex')`,
		"created after":     `SELECT public.created_after()`,
	}
	as := func(role, sql string) error {
		tx, err := conn.Begin(ctxT(t))
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctxT(t))
		if _, err := tx.Exec(ctxT(t), `SET LOCAL ROLE `+pgx.Identifier{role}.Sanitize()); err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctxT(t), sql)
		return err
	}
	for name, sql := range calls {
		if err := as(serve, sql); err != nil {
			t.Errorf("a serving role in the function users role cannot run %s: %v", name, err)
		}
		if err := as(stranger, sql); err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("a role outside the function users role ran %s (err %v), the revoke must hold", name, err)
		}
		for _, r := range []string{roles.Reader, roles.ProReader, roles.Login, roles.ViewOwner} {
			if err := as(r, sql); err == nil {
				t.Errorf("assist role %s ran %s", r, name)
			}
		}
	}
	// The migrating role is a member too.
	var member bool
	if err := conn.QueryRow(ctxT(t), `SELECT pg_has_role(current_user, $1, 'MEMBER')`, roles.FunctionUsers).Scan(&member); err != nil || !member {
		t.Errorf("the migrating role must be a member of the function users role (member=%v err=%v)", member, err)
	}
}
