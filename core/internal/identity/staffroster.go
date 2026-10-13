// StaffRosterReader is the read seam behind the staff roster route
// (identityListStaff). Read only: no user is created, changed or
// deactivated here (D38: FB Console issues users). The users table stays
// this package's to read.
package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrRosterNoDatabase is the reader's state when it was built without a database; the HTTP layer answers
// it 503, as the approvals rail answers its own unconfigured pool.
var ErrRosterNoDatabase = errors.New("identity: staff roster has no database")

// RosterEntry is one staff member of one org: the users row linked by
// platform_user_id, the winning role of that person's grants in the org
// (the same priority the authority resolver applies), and the branch
// grants for a branch scoped role. Role and scope are in the canonical
// vocabulary (staff_role ids, "global"/"branch"); the HTTP layer maps
// them onto the contract's words.
type RosterEntry struct {
	UserID      string
	Email       string
	DisplayName string
	RoleID      string
	RoleLabel   string
	Tier        int
	Scope       string
	BranchIDs   []string
}

// StaffRosterReader reads the roster over the pool.
type StaffRosterReader struct {
	pool *pgxpool.Pool
}

// NewStaffRosterReader returns the reader. A nil pool is legal at construction (the wire functions
// run poolless in composition tests) and every read then fails closed.
func NewStaffRosterReader(pool *pgxpool.Pool) *StaffRosterReader {
	return &StaffRosterReader{pool: pool}
}

// Roster answers every live users row that holds an active grant in the
// org, ordered by tier, display name and id. A person with no grant in the
// org is not in its roster (the org is the scope), and so is a user with no
// platform link, which has no org at all. The org predicate sits on both
// scoped tables read (staff_role_grant and user_branch_grants).
func (r *StaffRosterReader) Roster(ctx context.Context, orgID string) ([]RosterEntry, error) {
	if r.pool == nil {
		return nil, ErrRosterNoDatabase
	}
	if orgID == "" {
		return nil, errors.New("identity: staff roster requires the org id")
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, email, display_name, role_id, label, tier, scope, branch_ids
		FROM (
			SELECT u.id::text AS id, u.email::text AS email, u.display_name,
			       sr.id AS role_id, sr.label, sr.tier::int AS tier, sr.scope,
			       CASE WHEN sr.scope = 'branch' THEN COALESCE((
			           SELECT array_agg(ubg.branch_id::text ORDER BY ubg.branch_id)
			           FROM user_branch_grants ubg
			           WHERE ubg.user_id = u.id AND ubg.org_id = $1 AND ubg.deleted_at IS NULL
			       ), ARRAY[]::text[]) ELSE ARRAY[]::text[] END AS branch_ids,
			       row_number() OVER (PARTITION BY u.id ORDER BY sr.tier, sr.id) AS rn
			FROM users u
			JOIN staff_role_grant g ON g.platform_user_id = u.platform_user_id
			                       AND g.org_id = $1 AND g.deleted_at IS NULL
			JOIN staff_role sr ON sr.id = g.role_id AND sr.deleted_at IS NULL
			WHERE u.deleted_at IS NULL
		) ranked
		WHERE rn = 1
		ORDER BY tier, display_name, id`, orgID)
	if err != nil {
		return nil, fmt.Errorf("identity: staff roster read failed: %w", err)
	}
	defer rows.Close()
	var out []RosterEntry
	for rows.Next() {
		var e RosterEntry
		if err := rows.Scan(&e.UserID, &e.Email, &e.DisplayName, &e.RoleID, &e.RoleLabel, &e.Tier, &e.Scope, &e.BranchIDs); err != nil {
			return nil, fmt.Errorf("identity: staff roster read failed: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("identity: staff roster read failed: %w", err)
	}
	return out, nil
}
