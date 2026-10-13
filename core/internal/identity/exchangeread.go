package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// LinkedActiveUser answers the sign in exchange's explicit linkage check
// (state note gate 6): whether the platform user has a live users row that
// is ACTIVE, and that row's id (the audit row's actor). ResolveStaff passes
// a caller with no linked row on grants alone; the exchange does not. No
// linked row and a row that is not ACTIVE answer the same: ok false with a
// nil error.
func (r *StaffAuthorityResolver) LinkedActiveUser(ctx context.Context, platformUserID string) (userID string, ok bool, err error) {
	if platformUserID == "" {
		return "", false, errors.New("identity: linkage check requires the platform user id")
	}
	var status string
	err = r.pool.QueryRow(ctx, `
		SELECT id::text, status FROM users
		WHERE platform_user_id = $1
		  AND deleted_at IS NULL
		LIMIT 1`, platformUserID).Scan(&userID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("identity: staff linkage read failed: %w", err)
	}
	if status != StaffStatusActive {
		return "", false, nil
	}
	return userID, true, nil
}

// StaffRoleLabel reads staff_role.label, the role name the exchange answer
// carries. ok false is an unknown or tombstoned role.
func (r *StaffAuthorityResolver) StaffRoleLabel(ctx context.Context, roleID string) (label string, ok bool, err error) {
	err = r.pool.QueryRow(ctx,
		`SELECT label FROM staff_role WHERE id = $1 AND deleted_at IS NULL`, roleID).Scan(&label)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("identity: staff role label read failed: %w", err)
	}
	return label, true, nil
}
