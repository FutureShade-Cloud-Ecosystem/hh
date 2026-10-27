package identity

// seed_credentials.go, the finance seed's password leg (the deploy spec
// FINANCE-BOX-SPEC-r1, section 3): leave the fresh finance world with no
// usable password, through this module, never SQL from cmd/core.
//
// HOW PASSWORDS ARE STORED HERE: bcrypt hashes. Staff hashes live in
// users.password_hash, where NULL is the no-password state (the INVITED
// staff spelling). Portal hashes live in portal_users.password_hash, which
// is NOT NULL and spells no password with the literal marker
// "seeded-no-password" (populate.go's convention for a provisioned account).
//
// The statements are deliberately blanket, not a list of ids: the dibbits
// demo seed is not the only planter of demonstration credentials. The
// migrations plant demonstration staff and portal logins of their own, and
// a fresh finance world has no real user whose password could survive a
// blanket clear. So after finance-seed, no row on either credential table
// answers any password: every staff hash is NULL, every portal hash is the
// marker.

import (
	"context"
	"errors"
	"fmt"
)

// SeedCredentialReport counts the rows each table had cleared, so the caller
// can say plainly what the seed left behind.
type SeedCredentialReport struct {
	StaffCleared  int64
	PortalCleared int64
}

// DisableSeedCredentials leaves the database with no usable password on
// either credential table, in the caller's transaction: every
// users.password_hash is NULLed and every portal_users.password_hash takes
// the portalNoPassword marker, the convention populate.go already plants.
// It is idempotent: a re-run against a cleared world counts zero and changes
// nothing. Identity owns both tables, so the statements live here; cmd/core
// calls the seam.
func DisableSeedCredentials(ctx context.Context, db DBTX) (SeedCredentialReport, error) {
	var report SeedCredentialReport
	if db == nil {
		return report, errors.New("identity: disabling seed credentials requires a database handle")
	}
	tag, err := db.Exec(ctx, `
		UPDATE users SET password_hash = NULL, updated_at = now()
		 WHERE password_hash IS NOT NULL`)
	if err != nil {
		return report, fmt.Errorf("identity: clearing the staff passwords: %w", err)
	}
	report.StaffCleared = tag.RowsAffected()
	tag, err = db.Exec(ctx, `
		UPDATE portal_users SET password_hash = $1, updated_at = now()
		 WHERE password_hash <> $1`, portalNoPassword)
	if err != nil {
		return report, fmt.Errorf("identity: marking the portal passwords unusable: %w", err)
	}
	report.PortalCleared = tag.RowsAffected()
	return report, nil
}
