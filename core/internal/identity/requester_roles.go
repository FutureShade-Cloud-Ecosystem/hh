package identity

import (
	"context"
	"fmt"
)

// RoleLabelsForUsers answers the staff_role label each given users.id holds, for the surfaces that
// show who raised a thing by role and never by name (the change order queue). A user with several
// live grants gets the label of the lowest tier role, the role id as the tie-break, the same order
// ResolveRoles uses. A user with no live grant, or no id in the list, is simply absent from the map.
// Read only; the order module reaches these tables through this method and no other way.
func (r *StaffAuthorityResolver) RoleLabelsForUsers(ctx context.Context, userIDs []string) (map[string]string, error) {
	out := map[string]string{}
	if len(userIDs) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT ON (u.id) u.id::text, sr.label
		FROM users u
		JOIN staff_role_grant g ON g.platform_user_id = u.platform_user_id AND g.deleted_at IS NULL
		JOIN staff_role sr ON sr.id = g.role_id AND sr.deleted_at IS NULL
		WHERE u.id = ANY($1::uuid[])
		  AND u.deleted_at IS NULL
		ORDER BY u.id, sr.tier, sr.id`, userIDs)
	if err != nil {
		return nil, fmt.Errorf("identity: requester role label read failed: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, label string
		if err := rows.Scan(&id, &label); err != nil {
			return nil, fmt.Errorf("identity: requester role label read failed: %w", err)
		}
		out[id] = label
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("identity: requester role label read failed: %w", err)
	}
	return out, nil
}
