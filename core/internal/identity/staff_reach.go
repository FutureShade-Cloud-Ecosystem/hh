package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// StaffReach is what a consumer module needs to decide whether a staffer may be shown something that is
// fenced by the role gate and the branch reach: the platform identity the gate answers for, the orgs the
// staffer holds a live grant in, and the branches they reach.
type StaffReach struct {
	PlatformUserID string
	OrgIDs         []string
	// Global is true when the staffer's winning grant in any org is a global role: every branch.
	Global bool
	// BranchIDs is the union of the branch grants of the orgs where the winning grant is branch scoped.
	BranchIDs []string
}

// ReachOfUser reads the reach of the staffer whose ERP users row is userID, through the platform identity
// link and the same ResolveStaff walk the request path runs per org, so the answer is the guard's and not a
// second definition. An unknown, tombstoned, unlinked or not ACTIVE staffer is the zero value and a nil
// error: the gate cannot answer for them, which the consumer reads as no. A grant data defect or a
// database failure is returned.
func (r *StaffAuthorityResolver) ReachOfUser(ctx context.Context, userID string) (StaffReach, error) {
	var platformID, status string
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(platform_user_id, ''), status FROM users
		WHERE id = $1::uuid AND deleted_at IS NULL`, userID).Scan(&platformID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return StaffReach{}, nil
	}
	if err != nil {
		return StaffReach{}, fmt.Errorf("identity: staff reach read failed: %w", err)
	}
	if platformID == "" || status != StaffStatusActive {
		return StaffReach{}, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT org_id::text FROM staff_role_grant
		WHERE platform_user_id = $1 AND deleted_at IS NULL
		ORDER BY 1`, platformID)
	if err != nil {
		return StaffReach{}, fmt.Errorf("identity: staff reach orgs read failed: %w", err)
	}
	var orgs []string
	for rows.Next() {
		var o string
		if err := rows.Scan(&o); err != nil {
			rows.Close()
			return StaffReach{}, fmt.Errorf("identity: staff reach orgs read failed: %w", err)
		}
		orgs = append(orgs, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return StaffReach{}, fmt.Errorf("identity: staff reach orgs read failed: %w", err)
	}
	reach := StaffReach{PlatformUserID: platformID}
	seen := map[string]bool{}
	for _, org := range orgs {
		authority, ok, err := r.ResolveStaff(ctx, platformID, org)
		if err != nil {
			return StaffReach{}, err
		}
		if !ok {
			continue
		}
		reach.OrgIDs = append(reach.OrgIDs, org)
		if authority.Scope == ScopeGlobal {
			reach.Global = true
			continue
		}
		for _, b := range authority.Branches {
			if !seen[b] {
				seen[b] = true
				reach.BranchIDs = append(reach.BranchIDs, b)
			}
		}
	}
	return reach, nil
}
