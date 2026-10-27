package identity

// The named testers' staff rows (W2-2): the one missing seam between the
// platform directory and the sign in exchange. Nothing else in identity
// inserts a users row: the carried ERP rows arrive through the seed and the
// demo, and the exchange's gate 6 requires a linked, ACTIVE row before any
// grant is read. These functions are that link for a person who exists in
// FB Console and on the box's allow list, and its removal on revoke. Every
// write is an upsert or a tombstone: a re-run converges, and nothing is
// ever deleted.

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ErrStaffEmailTaken names the refusal when the email already belongs to a
// different platform user (or to a row with no platform link at all): the
// users table's unique email index makes the collision a fact either way,
// so guessing a new owner would fail closed at the insert and is refused
// here instead, before any write.
var ErrStaffEmailTaken = errors.New("identity: the email already belongs to another platform user")

// EnsureStaffUser inserts, or reactivates, one ACTIVE users row for a
// platform user, idempotent on platform_user_id: a live row converges its
// email, display name and org and a tombstoned or non-ACTIVE row is
// restored to ACTIVE, so a re-run after a revoke simply brings the person
// back. The email is owned by exactly one platform subject: a row already
// holding it under another subject (or under none) refuses with
// ErrStaffEmailTaken. written reports whether this call changed the row.
// The row carries no password hash: the core has no password route and the
// sign in is the platform's.
func EnsureStaffUser(ctx context.Context, db DBTX, orgID, platformUserID, email, displayName string) (userID string, written bool, err error) {
	if db == nil {
		return "", false, errors.New("identity: a staff user requires a database handle")
	}
	if orgID == "" || platformUserID == "" || email == "" || displayName == "" {
		return "", false, errors.New("identity: a staff user requires the org, the platform user, an email and a display name")
	}

	var holder struct {
		id             string
		platformUserID *string
	}
	err = db.QueryRow(ctx, `SELECT id::text, platform_user_id FROM users WHERE email = $1`, email).Scan(&holder.id, &holder.platformUserID)
	switch {
	case err == nil && (holder.platformUserID == nil || *holder.platformUserID != platformUserID):
		return "", false, ErrStaffEmailTaken
	case err != nil && !errors.Is(err, pgx.ErrNoRows):
		return "", false, fmt.Errorf("identity: staff user email ownership read failed: %w", err)
	}

	var converged bool
	err = db.QueryRow(ctx, `
		SELECT id::text, deleted_at IS NULL AND status = 'ACTIVE'
		  AND email IS NOT DISTINCT FROM $3
		  AND display_name IS NOT DISTINCT FROM $4
		  AND org_id::text IS NOT DISTINCT FROM $1
		FROM users WHERE platform_user_id = $2`,
		orgID, platformUserID, email, displayName).Scan(&userID, &converged)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if err := db.QueryRow(ctx, `
			INSERT INTO users (email, display_name, status, platform_user_id, org_id)
			VALUES ($1, $2, 'ACTIVE', $3, $4) RETURNING id::text`,
			email, displayName, platformUserID, orgID).Scan(&userID); err != nil {
			return "", false, fmt.Errorf("identity: staff user write failed: %w", err)
		}
		return userID, true, nil
	case err != nil:
		return "", false, fmt.Errorf("identity: staff user read failed: %w", err)
	}
	if converged {
		return userID, false, nil
	}
	tag, err := db.Exec(ctx, `
		UPDATE users SET email = $2, display_name = $3, org_id = $4::uuid, status = 'ACTIVE',
			deleted_at = NULL, updated_at = now()
		WHERE id = $1::uuid`,
		userID, email, displayName, orgID)
	if err != nil {
		return "", false, fmt.Errorf("identity: staff user reactivation failed: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return "", false, errors.New("identity: staff user reactivation changed no row")
	}
	return userID, true, nil
}

// StaffUserByPlatformID reads the users row the platform subject holds: the
// row's id and the email it carries. The unique index on platform_user_id
// makes the row at most one, so the read is the subject a caller acts on:
// a revoke names its person by email and by platform id, and the two must
// agree before anything is written. The email comes back as the row holds
// it, so the caller folds the case itself. found is false when no row
// holds the platform user.
func StaffUserByPlatformID(ctx context.Context, db DBTX, platformUserID string) (userID, email string, found bool, err error) {
	if db == nil {
		return "", "", false, errors.New("identity: a staff user read requires a database handle")
	}
	if platformUserID == "" {
		return "", "", false, errors.New("identity: a staff user read requires the platform user")
	}
	err = db.QueryRow(ctx, `
		SELECT id::text, email::text FROM users WHERE platform_user_id = $1`, platformUserID).Scan(&userID, &email)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("identity: staff user read failed: %w", err)
	}
	return userID, email, true, nil
}

// DeactivateStaffUser ends the person's sign in: the linked users row, if
// live, becomes DEACTIVATED and the exchange's gate 6 refuses them from the
// next request. It is the revoke's user half; the grant half is
// RevokeStaffGrants. deactivated is false when no live row held the
// platform user. Nothing is deleted: the row is a provisioning fact.
func DeactivateStaffUser(ctx context.Context, db DBTX, platformUserID string) (userID string, deactivated bool, err error) {
	if db == nil {
		return "", false, errors.New("identity: a staff user deactivation requires a database handle")
	}
	if platformUserID == "" {
		return "", false, errors.New("identity: a staff user deactivation requires the platform user")
	}
	err = db.QueryRow(ctx, `
		UPDATE users SET status = 'DEACTIVATED', updated_at = now()
		WHERE platform_user_id = $1 AND deleted_at IS NULL AND status <> 'DEACTIVATED'
		RETURNING id::text`, platformUserID).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("identity: staff user deactivation failed: %w", err)
	}
	return userID, true, nil
}

// RevokeStaffGrants soft deletes every live grant the platform user holds
// in the org, the revoke's grant half beside DeactivateStaffUser. It is
// every grant and not one role: a grant left live comes back with the next
// EnsureStaffGrant, so a revoke that named only the role the caller had in
// mind would not be the last word. The partial unique index keeps the
// tombstones out of the live set, so a later EnsureStaffGrant writes fresh
// rows and the history keeps these ones. The ids come back in the order
// the database hands them out; revoked is false when no live grant matched.
func RevokeStaffGrants(ctx context.Context, db DBTX, orgID, platformUserID string) (grantIDs []string, revoked bool, err error) {
	if db == nil {
		return nil, false, errors.New("identity: a staff grant revocation requires a database handle")
	}
	if orgID == "" || platformUserID == "" {
		return nil, false, errors.New("identity: a staff grant revocation requires the org and the platform user")
	}
	rows, err := db.Query(ctx, `
		UPDATE staff_role_grant SET deleted_at = now()
		WHERE org_id = $1 AND platform_user_id = $2 AND deleted_at IS NULL
		RETURNING id::text`, orgID, platformUserID)
	if err != nil {
		return nil, false, fmt.Errorf("identity: staff grant revocation failed: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var grantID string
		if err := rows.Scan(&grantID); err != nil {
			return nil, false, fmt.Errorf("identity: staff grant revocation read failed: %w", err)
		}
		grantIDs = append(grantIDs, grantID)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("identity: staff grant revocation read failed: %w", err)
	}
	return grantIDs, len(grantIDs) > 0, nil
}

// StaffGrantID reads the id of the person's live grant of one role in the
// org, the identifier a provisioning caller prints after EnsureStaffGrant.
// ok is false when no live grant matches.
func StaffGrantID(ctx context.Context, db DBTX, orgID, platformUserID, roleID string) (grantID string, ok bool, err error) {
	if db == nil {
		return "", false, errors.New("identity: a staff grant read requires a database handle")
	}
	if orgID == "" || platformUserID == "" || roleID == "" {
		return "", false, errors.New("identity: a staff grant read requires the org, the platform user and the role")
	}
	err = db.QueryRow(ctx, `
		SELECT id::text FROM staff_role_grant
		WHERE org_id = $1 AND platform_user_id = $2 AND role_id = $3 AND deleted_at IS NULL`,
		orgID, platformUserID, roleID).Scan(&grantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("identity: staff grant read failed: %w", err)
	}
	return grantID, true, nil
}

// StaffSubject is one platform subject that holds a live grant: the platform
// user id and the email the linked users row carries.
type StaffSubject struct {
	PlatformUserID string
	Email          string
}

// StaffSubjectsWithLiveGrantsInRoles lists the platform subjects that hold a
// live grant of one of the named roles in the org. It is the read a caller
// acts on when the box's list of people changes: the subjects the list left
// out are the ones to revoke. The email comes back as the linked users row
// carries it, so the caller folds the case itself. Each subject comes back
// once, however many of the roles it holds.
func StaffSubjectsWithLiveGrantsInRoles(ctx context.Context, db DBTX, orgID string, roleIDs []string) ([]StaffSubject, error) {
	if db == nil {
		return nil, errors.New("identity: a staff subject read requires a database handle")
	}
	if orgID == "" || len(roleIDs) == 0 {
		return nil, errors.New("identity: a staff subject read requires the org and the roles")
	}
	rows, err := db.Query(ctx, `
		SELECT DISTINCT g.platform_user_id, COALESCE(u.email::text, '')
		  FROM staff_role_grant g
		  LEFT JOIN users u ON u.platform_user_id = g.platform_user_id
		 WHERE g.org_id = $1 AND g.deleted_at IS NULL AND g.role_id = ANY($2::text[])`,
		orgID, roleIDs)
	if err != nil {
		return nil, fmt.Errorf("identity: staff subject read failed: %w", err)
	}
	defer rows.Close()
	var out []StaffSubject
	for rows.Next() {
		var s StaffSubject
		if err := rows.Scan(&s.PlatformUserID, &s.Email); err != nil {
			return nil, fmt.Errorf("identity: staff subject read failed: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("identity: staff subject read failed: %w", err)
	}
	return out, nil
}
