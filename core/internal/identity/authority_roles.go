package identity

import (
	"context"
	"errors"
	"fmt"
)

// ResolveRoles reads every role the caller holds in the org, for the role
// gate's whole-row walk (D48's X10: a person holding two roles gets both;
// the review ruling on the platform roles): every active grant's role id,
// lowest tier first, the role id as the deterministic tie-break, the same
// order ResolveStaff resolves its winner by. The linked users row's status
// gates the read exactly as ResolveStaff's does: a linked row that is not
// ACTIVE is ErrStaffUserInactive (the 401 family upstream), a caller with
// no linked users row keeps roles from grants alone. An empty list with a
// nil error is no active grant, the RBAC-403 upstream. The grant-shape
// checks (the branch-id rule, the tier range) stay ResolveStaff's: the
// gate that reads this list decides admissions from role names alone, and
// the tier guard on the same request still runs ResolveStaff's checks, so
// a malformed row is refused there regardless of what the name walk sees.
func (r *StaffAuthorityResolver) ResolveRoles(ctx context.Context, platformUserID, orgID string) ([]string, error) {
	if platformUserID == "" || orgID == "" {
		return nil, errors.New("identity: staff role resolution requires the platform user id and the org id")
	}
	status, linked, err := r.linkedUserStatus(ctx, platformUserID)
	if err != nil {
		return nil, err
	}
	if linked && status != StaffStatusActive {
		return nil, fmt.Errorf("%w: users row status %q", ErrStaffUserInactive, status)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT sr.id
		FROM staff_role_grant g
		JOIN staff_role sr ON sr.id = g.role_id
		WHERE g.platform_user_id = $1
		  AND g.org_id = $2
		  AND g.deleted_at IS NULL
		  AND sr.deleted_at IS NULL
		GROUP BY sr.id
		ORDER BY min(sr.tier), sr.id`, platformUserID, orgID)
	if err != nil {
		return nil, fmt.Errorf("identity: staff role read failed: %w", err)
	}
	defer rows.Close()
	roles := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("identity: staff role read failed: %w", err)
		}
		roles = append(roles, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("identity: staff role read failed: %w", err)
	}
	return roles, nil
}
