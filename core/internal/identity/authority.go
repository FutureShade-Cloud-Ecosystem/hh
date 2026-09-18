package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The staff role vocabulary of staff_role (migration
// core/db/migrations/0215_identity_audit.sql): three tiers, the catalog's
// thirteen business roles plus the platform role and the two internal
// roles, all in one table. Tiers follow the ERP's authority model at the
// pinned commit (rbac.go: TierLeadership, TierManager, TierFrontline),
// which the guards re-express over the tier column.
const (
	TierLeadership = 1
	TierManager    = 2
	TierFrontline  = 3
)

// Scope values of staff_role.scope.
const (
	ScopeGlobal = "global"
	ScopeBranch = "branch"
)

// RoleDriver names the one role whose session length is the dealer knob
// (0209): it is a constant, not a literal, for the same reason the ERP
// gives (a typo here would silently apply the wrong ceiling and sign a
// driver out mid-shift, or keep them in past the dealer's number).
const RoleDriver = "driver"

// StaffAuthority is one staff member's resolved authority for one org, read
// from the app-role tables per request and never from the token (D3). It
// carries the ERP's StaffContext semantics (rbac.go at the pinned commit)
// re-keyed onto the platform user id.
type StaffAuthority struct {
	// RoleID is the winning staff_role id.
	RoleID string
	// Tier is the winning role's tier (1 to 3).
	Tier int
	// Scope is the winning role's scope (global or branch).
	Scope string
	// Branches holds the granted branch ids, resolved through
	// users.platform_user_id. Empty for global scope, where grants are
	// ignored.
	Branches []string
}

// IsManager reports whether the caller holds a tier 1 or tier 2 role: an
// assigned manager or leadership member, not front-line and not unassigned
// (ERP rbac.go IsManager, carried).
func (a StaffAuthority) IsManager() bool {
	return a.Tier >= TierLeadership && a.Tier <= TierManager
}

// IsAdmin reports whether the caller is a tier 1 global role, which
// bypasses branch scoping entirely (ERP rbac.go IsAdmin, carried).
func (a StaffAuthority) IsAdmin() bool {
	return a.Tier == TierLeadership && a.Scope == ScopeGlobal
}

// CanAccessBranch reports whether the authority reaches one branch:
// global scope passes everywhere, branch scope only through a grant (ERP
// rbac.go CanAccessBranch, carried over user_branch_grants).
func (a StaffAuthority) CanAccessBranch(branchID string) bool {
	if a.Scope == ScopeGlobal {
		return true
	}
	for _, b := range a.Branches {
		if b == branchID {
			return true
		}
	}
	return false
}

// ErrGrantShape names the 0215 header's branch-id rule violation: a
// branch-scoped role grant requires a non-NULL branch_id, a global grant
// carries NULL, anything else is a data defect. The guard answers the 503
// family on it: an authority row that cannot be interpreted is an
// operational defect, never an admission and never a silent skip.
var ErrGrantShape = errors.New("identity: staff role grant violates the branch-id rule")

// StaffStatusActive is the one users.status value that may hold authority
// (the carried CHECK set is INVITED, ACTIVE, SUSPENDED, DEACTIVATED,
// migration 0004).
const StaffStatusActive = "ACTIVE"

// ErrStaffUserInactive names a linked users row whose status is not
// ACTIVE. The ERP's per-request re-resolution refuses a non-ACTIVE
// account with the credential error (identity/service.go Authenticate at
// the pinned commit: suspended or deactivated since issue, H-4 parity),
// so the guard maps this sentinel to the 401 family: an account-lifetime
// fact, not a gate decision.
var ErrStaffUserInactive = errors.New("identity: linked staff account is not active")

// StaffAuthorityResolver resolves staff authority from the identity tables
// through a pgx pool, per request with no caching (D28 family: authority
// resolves against its source of truth every time, like membership).
type StaffAuthorityResolver struct {
	pool *pgxpool.Pool
}

// NewStaffAuthorityResolver validates the collaborator and returns the
// resolver.
func NewStaffAuthorityResolver(pool *pgxpool.Pool) (*StaffAuthorityResolver, error) {
	if pool == nil {
		return nil, errors.New("identity: staff authority resolver requires a pool")
	}
	return &StaffAuthorityResolver{pool: pool}, nil
}

// ResolveStaff resolves the highest-priority active grant of the platform
// user in the org, with tier and scope from the role, honoring the 0215
// header's branch-id rule on every active grant row read: a violation is
// ErrGrantShape (a data defect), any database failure is returned as an
// error, and both are operational refusals upstream. Before any grant is
// read, the linked users row's status gates the whole resolution: a linked
// row that is not ACTIVE is ErrStaffUserInactive (the 401 family
// upstream), a caller with no linked users row keeps authority from grants
// alone until B4's population ruling pins the linkage requirement. ok
// false with a nil error is no authority: no active grant, which the guard
// chain answers with the RBAC-403 (authenticated, the gate refused), never
// an admission.
//
// Priority is the lowest tier first (leadership outranks management
// outranks front-line), with the role id as the deterministic tie-break:
// two grants of one tier resolve to the lexicographically first role id,
// a stable rule so the same rows always resolve the same way.
func (r *StaffAuthorityResolver) ResolveStaff(ctx context.Context, platformUserID, orgID string) (StaffAuthority, bool, error) {
	if platformUserID == "" || orgID == "" {
		return StaffAuthority{}, false, errors.New("identity: staff authority resolution requires the platform user id and the org id")
	}
	status, linked, err := r.linkedUserStatus(ctx, platformUserID)
	if err != nil {
		return StaffAuthority{}, false, err
	}
	if linked && status != StaffStatusActive {
		return StaffAuthority{}, false, fmt.Errorf("%w: users row status %q", ErrStaffUserInactive, status)
	}
	type grantRow struct {
		roleID   string
		tier     int
		scope    string
		branchID *string
	}
	rows, err := r.pool.Query(ctx, `
		SELECT sr.id, sr.tier::int, sr.scope, g.branch_id::text
		FROM staff_role_grant g
		JOIN staff_role sr ON sr.id = g.role_id
		WHERE g.platform_user_id = $1
		  AND g.org_id = $2
		  AND g.deleted_at IS NULL
		  AND sr.deleted_at IS NULL
		ORDER BY sr.tier, sr.id`, platformUserID, orgID)
	if err != nil {
		return StaffAuthority{}, false, fmt.Errorf("identity: staff grant read failed: %w", err)
	}
	defer rows.Close()
	var grants []grantRow
	for rows.Next() {
		var g grantRow
		if err := rows.Scan(&g.roleID, &g.tier, &g.scope, &g.branchID); err != nil {
			return StaffAuthority{}, false, fmt.Errorf("identity: staff grant read failed: %w", err)
		}
		grants = append(grants, g)
	}
	if err := rows.Err(); err != nil {
		return StaffAuthority{}, false, fmt.Errorf("identity: staff grant read failed: %w", err)
	}
	for _, g := range grants {
		// The branch-id rule is checked on every active grant row read,
		// not only the winner: a malformed row is a provisioning defect
		// and must surface loudly rather than hide behind a higher-tier
		// grant.
		switch {
		case g.scope == ScopeBranch && g.branchID == nil:
			return StaffAuthority{}, false, fmt.Errorf("%w: branch-scoped role %s granted with a NULL branch_id", ErrGrantShape, g.roleID)
		case g.scope == ScopeGlobal && g.branchID != nil:
			return StaffAuthority{}, false, fmt.Errorf("%w: global role %s granted with a branch_id", ErrGrantShape, g.roleID)
		case g.scope != ScopeBranch && g.scope != ScopeGlobal:
			return StaffAuthority{}, false, fmt.Errorf("%w: role %s carries the unknown scope %q", ErrGrantShape, g.roleID, g.scope)
		case g.tier < TierLeadership || g.tier > TierFrontline:
			return StaffAuthority{}, false, fmt.Errorf("%w: role %s carries the out-of-range tier %d", ErrGrantShape, g.roleID, g.tier)
		}
	}
	if len(grants) == 0 {
		return StaffAuthority{}, false, nil
	}
	winner := grants[0]
	authority := StaffAuthority{RoleID: winner.roleID, Tier: winner.tier, Scope: winner.scope}
	if winner.scope == ScopeBranch {
		branches, err := r.branchGrants(ctx, platformUserID, orgID)
		if err != nil {
			return StaffAuthority{}, false, err
		}
		authority.Branches = branches
	}
	return authority, true, nil
}

// linkedUserStatus reads the status of the users row linked by platform
// user id. A tombstoned row is not a linked row (C35), which keeps this
// read consistent with the branch-grant join. The partial unique index on
// platform_user_id (migration 0215) makes the single row deterministic.
// No core table outside the identity fence is touched.
func (r *StaffAuthorityResolver) linkedUserStatus(ctx context.Context, platformUserID string) (string, bool, error) {
	var status string
	err := r.pool.QueryRow(ctx, `
		SELECT status FROM users
		WHERE platform_user_id = $1
		  AND deleted_at IS NULL
		LIMIT 1`, platformUserID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("identity: staff account read failed: %w", err)
	}
	return status, true, nil
}

// branchGrants reads the caller's branch grants through the platform
// identity link: user_branch_grants keyed by the users row whose
// platform_user_id matches, within the request org. The org predicate is
// required by the module fence (a scoped table is never queried without
// its scope predicate), which also means carried rows whose additive
// org_id is still NULL resolve to no grant until population fills it: a
// tombstoned users row (C35) confers no grant either. Fail closed, and
// loudly at the boundary, not silently here.
func (r *StaffAuthorityResolver) branchGrants(ctx context.Context, platformUserID, orgID string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT ubg.branch_id::text
		FROM user_branch_grants ubg
		JOIN users u ON u.id = ubg.user_id
		WHERE u.platform_user_id = $1
		  AND ubg.org_id = $2
		  AND ubg.deleted_at IS NULL
		  AND u.deleted_at IS NULL
		ORDER BY ubg.branch_id`, platformUserID, orgID)
	if err != nil {
		return nil, fmt.Errorf("identity: branch grant read failed: %w", err)
	}
	defer rows.Close()
	var branches []string
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			return nil, fmt.Errorf("identity: branch grant read failed: %w", err)
		}
		branches = append(branches, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("identity: branch grant read failed: %w", err)
	}
	return branches, nil
}
