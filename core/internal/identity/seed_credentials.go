package identity

// seed_credentials.go, the finance seed's password leg (the deploy spec
// FINANCE-BOX-SPEC-r1, section 3): leave the seed's users with no usable
// password, through this module, never SQL from cmd/core.
//
// HOW PASSWORDS ARE STORED HERE: bcrypt hashes. Staff hashes live in
// users.password_hash, where NULL is the no-password state (the INVITED
// staff spelling). Portal hashes live in portal_users.password_hash, which
// is NOT NULL and spells no password with the literal marker
// "seeded-no-password" (populate.go's convention for a provisioned account).
// The dibbits demo seed plants both tables with one well known demo hash, so
// a database the finance seed applied must end with those rows unreadable by
// any password: staff rows NULL, the portal row marked.
//
// The id lists are the seed's fixed ids (cmd/core/dibbits_seed.sql), written
// out here so the statement cannot drift from the seed by way of a join.

import (
	"context"
	"errors"
	"fmt"
)

// seedStaffIDs is the dibbits seed's fixed staff id list: the demo operator,
// the sample managers, the invited new hire (already NULL) and the platform
// admin mirror row.
var seedStaffIDs = []string{
	"d7000000-0000-4000-9000-000000000001",
	"d7000000-0000-4000-9000-000000000002",
	"d7000000-0000-4000-9000-000000000003",
	"d7000000-0000-4000-9000-000000000005",
	"d7000000-0000-4000-9000-000000000006",
	"d7000000-0000-4000-9000-000000000007",
	"d7000000-0000-4000-9000-000000000010",
}

// seedPortalUserID is the seed's one contractor portal account (CP-07).
const seedPortalUserID = "e7000000-0000-4000-9000-000000000001"

// SeedCredentialReport counts the rows each table had cleared, so the caller
// can say plainly what the seed left behind.
type SeedCredentialReport struct {
	StaffCleared  int64
	PortalCleared int64
}

// DisableSeedCredentials leaves every user the dibbits seed plants with no
// usable password, on both credential tables, in the caller's transaction.
// The staff table spells no password as NULL (the INVITED state);
// portal_users.password_hash is NOT NULL, so the portal row takes the
// portalNoPassword marker, the convention populate.go already plants. It is
// idempotent: a re-run against a cleared world counts zero and changes
// nothing. Identity owns both tables, so the statement lives here; cmd/core
// calls the seam.
func DisableSeedCredentials(ctx context.Context, db DBTX) (SeedCredentialReport, error) {
	var report SeedCredentialReport
	if db == nil {
		return report, errors.New("identity: disabling seed credentials requires a database handle")
	}
	tag, err := db.Exec(ctx, `
		UPDATE users SET password_hash = NULL, updated_at = now()
		 WHERE id = ANY($1::uuid[]) AND password_hash IS NOT NULL`, seedStaffIDs)
	if err != nil {
		return report, fmt.Errorf("identity: clearing the seed staff passwords: %w", err)
	}
	report.StaffCleared = tag.RowsAffected()
	tag, err = db.Exec(ctx, `
		UPDATE portal_users SET password_hash = $2, updated_at = now()
		 WHERE id = $1::uuid AND password_hash <> $2`, seedPortalUserID, portalNoPassword)
	if err != nil {
		return report, fmt.Errorf("identity: marking the seed portal password unusable: %w", err)
	}
	report.PortalCleared = tag.RowsAffected()
	return report, nil
}
