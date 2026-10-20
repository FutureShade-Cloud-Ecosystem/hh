//go:build integration

package readtx

import (
	"errors"
	"testing"
)

// The assist path starts only when it logs in as the assist login (never the app
// role) and the key it signs with is the one the database holds.
func TestOpenRefusesTheAppRoleAndAnUnprovisionedKey(t *testing.T) {
	admin := connect(t, gateURL(t))
	_, secret := testKey(t, admin)
	staffURL, _ := loginPasswords(t, admin)

	// The app role's own URL: a superuser, not the assist login.
	if rs, err := Open(ctxT(t), Config{ReaderURL: NewSecret(gateURL(t)), ClaimsKey: NewSecret(secret)}); !errors.Is(err, ErrWrongLogin) {
		if rs != nil {
			rs.Close()
		}
		t.Errorf("Open with the app role's URL: err = %v, want ErrWrongLogin", err)
	}
	// A key the database does not hold.
	if rs, err := Open(ctxT(t), Config{ReaderURL: NewSecret(staffURL), ClaimsKey: NewSecret("not-provisioned-" + randomHex(t, 24))}); !errors.Is(err, ErrClaimsNotProvisioned) {
		if rs != nil {
			rs.Close()
		}
		t.Errorf("Open with an unprovisioned key: err = %v, want ErrClaimsNotProvisioned", err)
	}
	// And the good configuration still opens.
	rs, err := Open(ctxT(t), Config{ReaderURL: NewSecret(staffURL), ClaimsKey: NewSecret(secret)})
	if err != nil {
		t.Fatalf("good config: %v", err)
	}
	rs.Close()
}

// On a direct connection two statements in one transaction see one backend.
func TestRealSessionIsStable(t *testing.T) {
	admin := connect(t, gateURL(t))
	_, secret := testKey(t, admin)
	staffURL, _ := loginPasswords(t, admin)
	rs, err := Open(ctxT(t), Config{ReaderURL: NewSecret(staffURL), ClaimsKey: NewSecret(secret)})
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	if err := rs.Staff.checkSessionStable(ctxT(t)); err != nil {
		t.Fatal(err)
	}
}
