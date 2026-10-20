//go:build integration

package readtx

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// The Go signer and the SQL verifier agree on the wire format, end to end: a
// claim signed here, carried as a bind parameter through Run, is read back by
// ai.claims() inside the same read only transaction as the reader role.
func TestRunCarriesASignedClaimToTheVerifier(t *testing.T) {
	admin := connect(t, gateURL(t))
	_, secret := testKey(t, admin)
	staffURL, proURL := loginPasswords(t, admin)
	rs, err := Open(ctxT(t), Config{
		ReaderURL: NewSecret(staffURL), ProReaderURL: NewSecret(proURL), ClaimsKey: NewSecret(secret),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(rs.Close)
	if rs.Pro == nil {
		t.Fatal("the contractor pool did not open")
	}

	read := func(r *Reader, c Claims) (asker, user, role string, readOnly string, err error) {
		err = r.Run(ctxT(t), c, `SELECT coalesce((ai.claims()).asker_id, 'NULL'), session_user::text, current_user::text, current_setting('transaction_read_only')`, nil,
			func(rows pgx.Rows) error {
				if !rows.Next() {
					return context.Canceled
				}
				return rows.Scan(&asker, &user, &role, &readOnly)
			})
		return
	}
	roles := RolesFor(currentDatabase(t, admin))

	asker, user, role, ro, err := read(rs.Staff, Claims{Audience: AudienceStaff, AskerID: "asker-1", BranchIDs: []string{"22222222-2222-2222-2222-222222222222"}})
	if err != nil {
		t.Fatalf("staff run: %v", err)
	}
	if asker != "asker-1" || user != roles.Login || role != roles.Reader || ro != "on" {
		t.Errorf("staff: asker=%s session_user=%s current_user=%s read_only=%s", asker, user, role, ro)
	}
	asker, user, role, _, err = read(rs.Pro, Claims{Audience: AudiencePro, AskerID: "portal-1", CustomerID: "33333333-3333-3333-3333-333333333333"})
	if err != nil {
		t.Fatalf("pro run: %v", err)
	}
	if asker != "portal-1" || user != roles.ProLogin || role != roles.ProReader {
		t.Errorf("pro: asker=%s session_user=%s current_user=%s", asker, user, role)
	}
	// The wrong audience never reaches the database.
	if _, _, _, _, err := read(rs.Pro, Claims{Audience: AudienceStaff, AskerID: "x"}); err == nil {
		t.Error("a staff claim ran on the contractor reader")
	}
}

func TestRunWritesNothingAndLeavesNoClaimBehind(t *testing.T) {
	admin := connect(t, gateURL(t))
	_, secret := testKey(t, admin)
	staffURL, _ := loginPasswords(t, admin)
	rs, err := Open(ctxT(t), Config{ReaderURL: NewSecret(staffURL), ClaimsKey: NewSecret(secret)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(rs.Close)
	c := Claims{Audience: AudienceStaff, AskerID: "asker-2"}
	err = rs.Staff.Run(ctxT(t), c, `CREATE TEMP TABLE leak (x int)`, nil, func(pgx.Rows) error { return nil })
	if err == nil {
		t.Fatal("a write statement ran")
	}
	if strings.Contains(err.Error(), "leak") {
		t.Errorf("error echoes the statement: %v", err)
	}
	// A later call on the pool sees no claim from the earlier one (set_config was local).
	var seen string
	err = rs.Staff.Run(ctxT(t), Claims{Audience: AudienceStaff, AskerID: "asker-3"}, `SELECT (ai.claims()).asker_id`, nil,
		func(rows pgx.Rows) error { rows.Next(); return rows.Scan(&seen) })
	if err != nil || seen != "asker-3" {
		t.Errorf("second call saw %q (err %v)", seen, err)
	}
}

func TestOpenErrorsNeverCarryTheURLOrTheKey(t *testing.T) {
	admin := connect(t, gateURL(t))
	_, secret := testKey(t, admin)
	roles := RolesFor(currentDatabase(t, admin))
	bad := withUser(t, gateURL(t), roles.Login, "CANARYPASSWORD")
	_, err := Open(ctxT(t), Config{ReaderURL: NewSecret(bad), ClaimsKey: NewSecret(secret)})
	if err == nil {
		t.Fatal("a wrong password opened")
	}
	for _, s := range []string{err.Error(), secret, "CANARYPASSWORD"} {
		if s != err.Error() && strings.Contains(err.Error(), s) {
			t.Errorf("open error carries %q", s[:6])
		}
	}
	_, err = Open(ctxT(t), Config{ReaderURL: NewSecret("postgres://u:CANARYPASSWORD@:::bad"), ClaimsKey: NewSecret(secret)})
	if err == nil || strings.Contains(err.Error(), "CANARYPASSWORD") {
		t.Errorf("unparseable URL error: %v", err)
	}
}
