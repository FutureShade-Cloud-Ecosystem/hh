package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// EnsureStaffGrant writes one staff_role_grant for a platform user in an
// org, idempotently: the caller names the role and, for a branch scoped
// role, the branch. It is the narrow seam a populate uses for a grant the
// carried user_roles catalog cannot derive (the demo's persona rows, keyed
// by a namespaced subject, and the roles the legacy catalog has no code
// for). The role's scope is read from staff_role and the 0215 header's
// branch id rule is checked before any write, here and again at the insert
// boundary, so a populate cannot write a grant the guards would refuse as a
// data defect. inserted is false when the live grant already existed.
func EnsureStaffGrant(ctx context.Context, db DBTX, orgID, platformUserID, roleID string, branchID *string) (inserted bool, err error) {
	if db == nil {
		return false, errors.New("identity: a staff grant requires a database handle")
	}
	if orgID == "" || platformUserID == "" || roleID == "" {
		return false, errors.New("identity: a staff grant requires the org, the platform user and the role")
	}
	scope, err := staffRoleScope(ctx, db, roleID)
	if err != nil {
		return false, err
	}
	return insertGrant(ctx, db, orgID, grantCandidate{platformUserID: platformUserID, roleID: roleID, scope: scope, branchID: branchID})
}

// EnsureStaffGrantAt is EnsureStaffGrant for a caller that holds the one
// branch a person works and not the role's scope: a branch scoped role is
// granted at that branch, a global role with none, by the role's own scope
// read from staff_role. The branch rule is then true by construction.
func EnsureStaffGrantAt(ctx context.Context, db DBTX, orgID, platformUserID, roleID, branchID string) (inserted bool, err error) {
	if db == nil {
		return false, errors.New("identity: a staff grant requires a database handle")
	}
	if orgID == "" || platformUserID == "" || roleID == "" || branchID == "" {
		return false, errors.New("identity: a staff grant requires the org, the platform user, the role and the branch")
	}
	scope, err := staffRoleScope(ctx, db, roleID)
	if err != nil {
		return false, err
	}
	var branch *string
	if scope == ScopeBranch {
		branch = &branchID
	}
	return insertGrant(ctx, db, orgID, grantCandidate{platformUserID: platformUserID, roleID: roleID, scope: scope, branchID: branch})
}

func staffRoleScope(ctx context.Context, db DBTX, roleID string) (string, error) {
	var scope string
	err := db.QueryRow(ctx, `SELECT scope FROM staff_role WHERE id = $1 AND deleted_at IS NULL`, roleID).Scan(&scope)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("identity: staff grant for an unknown role %q", roleID)
	}
	if err != nil {
		return "", fmt.Errorf("identity: staff role read failed: %w", err)
	}
	return scope, nil
}
