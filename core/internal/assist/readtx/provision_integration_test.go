//go:build integration

package readtx

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// The oneshot sets a password that the login can then use, without the password
// ever being sent to the server.
func TestProvisionLoginsSetsWorkingPasswords(t *testing.T) {
	admin := connect(t, gateURL(t))
	roles := RolesFor(currentDatabase(t, admin))
	staffPW, proPW := "staff-"+randomHex(t, 12), "pro-"+randomHex(t, 12)
	for _, login := range []string{roles.Login, roles.ProLogin} {
		login := login
		t.Cleanup(func() {
			c, err := pgx.Connect(ctxT(t), gateURL(t))
			if err == nil {
				defer c.Close(ctxT(t))
				_, _ = c.Exec(ctxT(t), `ALTER ROLE `+pgx.Identifier{login}.Sanitize()+` PASSWORD NULL`)
			}
		})
	}
	// Before: no password, no sign in.
	if c, err := pgx.Connect(ctxT(t), withUser(t, gateURL(t), roles.Login, staffPW)); err == nil {
		c.Close(ctxT(t))
		t.Fatal("the login signed in before it had a password")
	}
	names, err := ProvisionLogins(ctxT(t), admin, NewSecret(staffPW), NewSecret(proPW), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != roles.Login || names[1] != roles.ProLogin {
		t.Errorf("names = %v", names)
	}
	for login, pw := range map[string]string{roles.Login: staffPW, roles.ProLogin: proPW} {
		c, err := pgx.Connect(ctxT(t), withUser(t, gateURL(t), login, pw))
		if err != nil {
			t.Fatalf("%s cannot sign in with the provisioned password: %v", login, err)
		}
		c.Close(ctxT(t))
		if c2, err := pgx.Connect(ctxT(t), withUser(t, gateURL(t), login, pw+"x")); err == nil {
			c2.Close(ctxT(t))
			t.Errorf("%s signed in with a wrong password", login)
		}
	}
	var stored string
	if err := admin.QueryRow(ctxT(t), `SELECT rolpassword FROM pg_authid WHERE rolname = $1`, roles.Login).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stored, "SCRAM-SHA-256$") || strings.Contains(stored, staffPW) {
		t.Errorf("stored form is not a SCRAM verifier: %.20s", stored)
	}
	if _, err := ProvisionLogins(ctxT(t), admin, NewSecret("short"), Secret{}, nil); err == nil {
		t.Error("a short password was accepted")
	}
}

func TestProvisionClaimsKeyIsIdempotentRotatesAndKeepsTwo(t *testing.T) {
	admin := connect(t, gateURL(t))
	// Start from an empty table (the gate database provisions none) and restore it after.
	if _, err := admin.Exec(ctxT(t), `DELETE FROM ai.claims_key`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(ctxT(t), gateURL(t))
		if err == nil {
			defer c.Close(ctxT(t))
			_, _ = c.Exec(ctxT(t), `DELETE FROM ai.claims_key`)
		}
	})
	keys := []string{"key-a-" + randomHex(t, 16), "key-b-" + randomHex(t, 16), "key-c-" + randomHex(t, 16)}
	count := func() int {
		var n int
		if err := admin.QueryRow(ctxT(t), `SELECT count(*) FROM ai.claims_key`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for i, k := range keys {
		id, err := ProvisionClaimsKey(ctxT(t), admin, NewSecret(k), nil)
		if err != nil {
			t.Fatal(err)
		}
		if id != KeyID(NewSecret(k)) {
			t.Errorf("returned id %d is not the derived id", id)
		}
		if i == 1 {
			// Rerunning the current key changes nothing.
			if _, err := ProvisionClaimsKey(ctxT(t), admin, NewSecret(k), nil); err != nil || count() != 2 {
				t.Fatalf("rerun: count=%d err=%v", count(), err)
			}
		}
	}
	if count() != 2 {
		t.Fatalf("after three keys the table holds %d rows, want the two most recent", count())
	}
	pid := backendPID(t, admin)
	for i, want := range []bool{false, true, true} {
		ok, _ := verified(t, admin, rawSigned(int(KeyID(NewSecret(keys[i]))), keys[i], goodPayload(pid)))
		if ok != want {
			t.Errorf("key %d verifies = %v, want %v", i, ok, want)
		}
	}
	// Rotating back to the middle key makes it current again and drops the oldest.
	if _, err := ProvisionClaimsKey(ctxT(t), admin, NewSecret(keys[1]), nil); err != nil {
		t.Fatal(err)
	}
	var newest int
	if err := admin.QueryRow(ctxT(t), `SELECT key_id FROM ai.claims_key ORDER BY created_at DESC, key_id DESC LIMIT 1`).Scan(&newest); err != nil || newest != int(KeyID(NewSecret(keys[1]))) {
		t.Errorf("newest = %d err %v", newest, err)
	}
	if _, err := ProvisionClaimsKey(ctxT(t), admin, NewSecret("short"), nil); err == nil {
		t.Error("a short key was accepted")
	}
}

// P3-2: before a statement that carries a value, the oneshots turn statement
// logging off for their transaction where the role may.
func TestQuietStatementLogging(t *testing.T) {
	conn := connect(t, gateURL(t))
	show := func(tx pgx.Tx, name string) string {
		var v string
		if err := tx.QueryRow(ctxT(t), `SELECT current_setting($1)`, name).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	// A superuser may: both settings change for the transaction only.
	tx, err := conn.Begin(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctxT(t), `SET LOCAL log_statement = 'all'`); err != nil {
		t.Fatal(err)
	}
	quiet, err := quietStatementLogging(ctxT(t), tx)
	if err != nil || !quiet {
		t.Fatalf("a superuser must be able to silence statement logging (quiet=%v err=%v)", quiet, err)
	}
	if show(tx, "log_statement") != "none" || show(tx, "log_min_duration_statement") != "-1" {
		t.Errorf("settings not applied: %s %s", show(tx, "log_statement"), show(tx, "log_min_duration_statement"))
	}
	_ = tx.Rollback(ctxT(t))

	// A role that may not: reported as false, and the transaction stays usable.
	role := "readtx_plain_" + randomHex(t, 4)
	if _, err := conn.Exec(ctxT(t), `CREATE ROLE `+pgx.Identifier{role}.Sanitize()+` NOLOGIN`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(ctxT(t), gateURL(t))
		if err == nil {
			defer c.Close(ctxT(t))
			_, _ = c.Exec(ctxT(t), `DROP ROLE IF EXISTS `+pgx.Identifier{role}.Sanitize())
		}
	})
	tx, err = conn.Begin(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctxT(t))
	if _, err := tx.Exec(ctxT(t), `SET LOCAL ROLE `+pgx.Identifier{role}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	quiet, err = quietStatementLogging(ctxT(t), tx)
	if err != nil || quiet {
		t.Fatalf("a plain role cannot set log_statement: quiet=%v err=%v", quiet, err)
	}
	if show(tx, "log_statement") == "" {
		t.Error("transaction unusable after the refused SET")
	}
}
