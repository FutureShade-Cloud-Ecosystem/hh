package portal

// seed_credentials.go: the finance seed's portal password leg. The portal
// module owns portal_users, so the statement that leaves the world with no
// usable portal password lives here, and the finance seed calls this seam in
// its own transaction beside identity's staff half. No other module's SQL
// names this table.
//
// HOW PORTAL PASSWORDS ARE STORED HERE: bcrypt hashes in
// portal_users.password_hash, which is NOT NULL and spells no password with
// the literal marker "seeded-no-password", the convention the seed's own
// provisioned accounts are planted with. The marker is identity's exported
// constant, because identity plants those accounts and this seam clears
// them: one value, so the marker that clears the passwords is the marker
// the accounts were planted with.
//
// The statement is deliberately blanket, not a list of ids: the migrations
// plant demonstration portal logins of their own, and a fresh finance world
// has no real user whose password could survive a blanket clear.

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

// Execer is the handle this seam takes: the caller's transaction or the
// pool. It is the one method the seam needs, so a caller can hand it
// either.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// DisableSeedCredentials leaves the database with no usable password on
// the portal accounts, in the caller's transaction: every
// portal_users.password_hash takes the no password marker. It is
// idempotent: a re-run against a cleared world counts zero and changes
// nothing. cleared is the number of rows the statement changed, so the
// caller can say plainly what the seed left behind.
func DisableSeedCredentials(ctx context.Context, db Execer) (cleared int64, err error) {
	if db == nil {
		return 0, errors.New("portal: disabling the seed credentials requires a database handle")
	}
	tag, err := db.Exec(ctx, `
		UPDATE portal_users SET password_hash = $1, updated_at = now()
		 WHERE password_hash <> $1`, identity.PortalNoPasswordMarker)
	if err != nil {
		return 0, fmt.Errorf("portal: marking the portal passwords unusable: %w", err)
	}
	return tag.RowsAffected(), nil
}
