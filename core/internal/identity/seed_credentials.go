package identity

// seed_credentials.go, the finance seed's password leg (the deploy spec
// FINANCE-BOX-SPEC-r1, section 3): leave the fresh finance world with no
// usable staff password, through this module, never SQL from cmd/core.
//
// HOW PASSWORDS ARE STORED HERE: bcrypt hashes. Staff hashes live in
// users.password_hash, where NULL is the no-password state (the INVITED
// staff spelling). The portal half of the seed's password leg lives in the
// portal module, which owns portal_users: its portal_users.password_hash
// takes the literal marker "seeded-no-password" (the convention
// populate.go's portal accounts are planted with), and this module's seam
// clears the staff half beside it.
//
// The statements are deliberately blanket, not a list of ids: the dibbits
// demo seed is not the only planter of demonstration credentials. The
// migrations plant demonstration staff logins of their own, and a fresh
// finance world has no real user whose password could survive a blanket
// clear. So after finance-seed, no row on either credential table answers
// any password: every staff hash is NULL, and every portal hash is the
// marker the portal module's own seam writes.

import (
	"context"
	"errors"
	"fmt"
)

// DisableSeedCredentials leaves the database with no usable staff
// password, in the caller's transaction: every users.password_hash is
// NULLed. It is idempotent: a re-run against a cleared world counts zero
// and changes nothing. Identity owns the users table, so the statement
// lives here; cmd/core calls the seam beside the portal module's own.
func DisableSeedCredentials(ctx context.Context, db DBTX) (cleared int64, err error) {
	if db == nil {
		return 0, errors.New("identity: disabling seed credentials requires a database handle")
	}
	tag, err := db.Exec(ctx, `
		UPDATE users SET password_hash = NULL, updated_at = now()
		 WHERE password_hash IS NOT NULL`)
	if err != nil {
		return 0, fmt.Errorf("identity: clearing the staff passwords: %w", err)
	}
	return tag.RowsAffected(), nil
}
