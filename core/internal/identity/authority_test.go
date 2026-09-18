package identity

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// identityTestPool opens the scratch database for the resolver legs.
// Absent the variable the legs skip; the pure legs (capabilities, ceiling)
// still run everywhere.
func identityTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HH_B3_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("HH_B3_TEST_DATABASE_URL is not set; resolver legs need the scratch database")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// The two seeded branch ids (migration 0003 fixes them so tests can
// reference them deterministically).
const (
	b3BranchOne = "5d1b3f6e-0000-4000-9000-000000000001"
	b3BranchTwo = "5d1b3f6e-0000-4000-9000-000000000002"
)

// b3Fixture owns one synthetic org and user per test, with cleanup
// registered so nothing survives a failed assert.
type b3Fixture struct {
	pool          *pgxpool.Pool
	orgID         string
	otherOrgID    string
	userID        string
	platformUser  string
	platformUser2 string
}

func newB3Fixture(t *testing.T) *b3Fixture {
	t.Helper()
	pool := identityTestPool(t)
	ctx := context.Background()
	f := &b3Fixture{pool: pool}
	suffix := time.Now().UnixNano()
	f.platformUser = fmt.Sprintf("b3-platform-%d-a", suffix)
	f.platformUser2 = fmt.Sprintf("b3-platform-%d-b", suffix)
	var err error
	f.orgID, err = insertB3Org(ctx, pool, fmt.Sprintf("b3ident%d", suffix))
	if err != nil {
		t.Fatalf("insert org: %v", err)
	}
	f.otherOrgID, err = insertB3Org(ctx, pool, fmt.Sprintf("b3other%d", suffix))
	if err != nil {
		t.Fatalf("insert other org: %v", err)
	}
	t.Cleanup(func() { deleteB3Org(t, pool, f.otherOrgID) })
	t.Cleanup(func() { deleteB3Org(t, pool, f.orgID) })
	f.userID, err = insertB3User(ctx, pool, f.platformUser, fmt.Sprintf("b3staff%d@example.invalid", suffix))
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	t.Cleanup(func() { deleteB3User(t, pool, f.userID) })
	return f
}

func insertB3Org(ctx context.Context, pool *pgxpool.Pool, slug string) (string, error) {
	var id string
	err := pool.QueryRow(ctx,
		`INSERT INTO org (slug, name) VALUES ($1, 'B3 Identity Proof') RETURNING id::text`,
		slug).Scan(&id)
	return id, err
}

func deleteB3Org(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`DELETE FROM staff_role_grant WHERE org_id = $1`, id)
	if err != nil {
		t.Fatalf("clean grants: %v", err)
	}
	_, err = pool.Exec(context.Background(), `DELETE FROM org WHERE id = $1`, id)
	if err != nil {
		t.Fatalf("clean org: %v", err)
	}
}

func insertB3User(ctx context.Context, pool *pgxpool.Pool, platformUserID, email string) (string, error) {
	var id string
	err := pool.QueryRow(ctx, `
		INSERT INTO users (email, display_name, status, platform_user_id)
		VALUES ($1, 'B3 Proof Staff', 'ACTIVE', $2)
		RETURNING id::text`, email, platformUserID).Scan(&id)
	return id, err
}

func deleteB3User(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `DELETE FROM user_branch_grants WHERE user_id = $1`, id); err != nil {
		t.Fatalf("clean branch grants: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id); err != nil {
		t.Fatalf("clean user: %v", err)
	}
}

// grant inserts one staff_role_grant row; branchID may be empty for NULL.
func (f *b3Fixture) grant(t *testing.T, roleID, branchID string) {
	t.Helper()
	var branch any
	if branchID != "" {
		branch = branchID
	}
	_, err := f.pool.Exec(context.Background(), `
		INSERT INTO staff_role_grant (platform_user_id, role_id, org_id, branch_id)
		VALUES ($1, $2, $3, $4)`, f.platformUser, roleID, f.orgID, branch)
	if err != nil {
		t.Fatalf("insert grant: %v", err)
	}
}

func (f *b3Fixture) otherOrgGrant(t *testing.T, roleID string) {
	t.Helper()
	_, err := f.pool.Exec(context.Background(), `
		INSERT INTO staff_role_grant (platform_user_id, role_id, org_id)
		VALUES ($1, $2, $3)`, f.platformUser, roleID, f.otherOrgID)
	if err != nil {
		t.Fatalf("insert other-org grant: %v", err)
	}
}

func (f *b3Fixture) branchGrant(t *testing.T, branchID string) {
	t.Helper()
	_, err := f.pool.Exec(context.Background(), `
		INSERT INTO user_branch_grants (user_id, branch_id, org_id)
		VALUES ($1, $2, $3)`, f.userID, branchID, f.orgID)
	if err != nil {
		t.Fatalf("insert branch grant: %v", err)
	}
}

// The tier legs against real grant rows: a tier 3 branch role resolves
// with its branch grants and fails IsManager; a tier 2 role passes
// IsManager and fails IsAdmin; a tier 1 global role passes IsAdmin.
func TestResolveStaffTiers(t *testing.T) {
	f := newB3Fixture(t)
	ctx := context.Background()
	resolver, err := NewStaffAuthorityResolver(f.pool)
	if err != nil {
		t.Fatalf("NewStaffAuthorityResolver: %v", err)
	}

	f.grant(t, "inside_sales", b3BranchOne)
	f.branchGrant(t, b3BranchOne)

	frontline, ok, err := resolver.ResolveStaff(ctx, f.platformUser, f.orgID)
	if err != nil || !ok {
		t.Fatalf("frontline resolve: ok=%v err=%v", ok, err)
	}
	if frontline.RoleID != "inside_sales" || frontline.Tier != TierFrontline || frontline.Scope != ScopeBranch {
		t.Fatalf("frontline authority = %+v", frontline)
	}
	if frontline.IsManager() || frontline.IsAdmin() {
		t.Fatalf("frontline passes a manager gate")
	}
	if len(frontline.Branches) != 1 || frontline.Branches[0] != b3BranchOne {
		t.Fatalf("frontline branches = %v", frontline.Branches)
	}

	// Replace the grant with a tier 2 role: the manager gate opens, the
	// admin gate does not.
	if _, err := f.pool.Exec(ctx, `DELETE FROM staff_role_grant WHERE platform_user_id = $1`, f.platformUser); err != nil {
		t.Fatalf("clear grants: %v", err)
	}
	f.grant(t, "branch_manager", b3BranchOne)
	manager, ok, err := resolver.ResolveStaff(ctx, f.platformUser, f.orgID)
	if err != nil || !ok {
		t.Fatalf("manager resolve: ok=%v err=%v", ok, err)
	}
	if !manager.IsManager() || manager.IsAdmin() {
		t.Fatalf("manager tier legs: %+v", manager)
	}

	if _, err := f.pool.Exec(ctx, `DELETE FROM staff_role_grant WHERE platform_user_id = $1`, f.platformUser); err != nil {
		t.Fatalf("clear grants: %v", err)
	}
	f.grant(t, "general_manager", "")
	admin, ok, err := resolver.ResolveStaff(ctx, f.platformUser, f.orgID)
	if err != nil || !ok {
		t.Fatalf("admin resolve: ok=%v err=%v", ok, err)
	}
	if !admin.IsManager() || !admin.IsAdmin() {
		t.Fatalf("admin tier legs: %+v", admin)
	}
	if admin.Branches != nil {
		t.Fatalf("global authority carries branch grants: %v", admin.Branches)
	}
	if !admin.CanAccessBranch(b3BranchTwo) {
		t.Fatalf("global scope cannot reach an ungranted branch")
	}
}

// No active grant is no authority (ok false), and a grant in another org
// never resolves in this org: both legs are the RBAC-403 upstream.
func TestResolveStaffNoGrantAndOrgFence(t *testing.T) {
	f := newB3Fixture(t)
	ctx := context.Background()
	resolver, err := NewStaffAuthorityResolver(f.pool)
	if err != nil {
		t.Fatalf("NewStaffAuthorityResolver: %v", err)
	}

	if _, ok, err := resolver.ResolveStaff(ctx, f.platformUser, f.orgID); err != nil || ok {
		t.Fatalf("no grant: ok=%v err=%v", ok, err)
	}
	f.otherOrgGrant(t, "general_manager")
	if _, ok, err := resolver.ResolveStaff(ctx, f.platformUser, f.orgID); err != nil || ok {
		t.Fatalf("other-org grant resolved in this org: ok=%v err=%v", ok, err)
	}

	// A tombstoned grant is not an active grant (C35).
	f.grant(t, "inside_sales", b3BranchOne)
	if _, err := f.pool.Exec(ctx,
		`UPDATE staff_role_grant SET deleted_at = now() WHERE platform_user_id = $1`, f.platformUser); err != nil {
		t.Fatalf("tombstone grant: %v", err)
	}
	if _, ok, err := resolver.ResolveStaff(ctx, f.platformUser, f.orgID); err != nil || ok {
		t.Fatalf("tombstoned grant resolved: ok=%v err=%v", ok, err)
	}
}

// Highest priority wins: the lowest tier resolves regardless of insertion
// order, with the role id as the deterministic tie-break.
func TestResolveStaffPriority(t *testing.T) {
	f := newB3Fixture(t)
	ctx := context.Background()
	resolver, err := NewStaffAuthorityResolver(f.pool)
	if err != nil {
		t.Fatalf("NewStaffAuthorityResolver: %v", err)
	}
	f.grant(t, "inside_sales", b3BranchOne)
	f.grant(t, "branch_manager", b3BranchOne)
	f.grant(t, "driver", b3BranchTwo)
	authority, ok, err := resolver.ResolveStaff(ctx, f.platformUser, f.orgID)
	if err != nil || !ok {
		t.Fatalf("resolve: ok=%v err=%v", ok, err)
	}
	if authority.RoleID != "branch_manager" {
		t.Fatalf("priority resolve = %+v", authority)
	}

	// Same tier twice: lexicographically first role id, both directions.
	if _, err := f.pool.Exec(ctx, `DELETE FROM staff_role_grant WHERE platform_user_id = $1`, f.platformUser); err != nil {
		t.Fatalf("clear grants: %v", err)
	}
	f.grant(t, "yard_team", b3BranchOne)
	f.grant(t, "driver", b3BranchTwo)
	authority, ok, err = resolver.ResolveStaff(ctx, f.platformUser, f.orgID)
	if err != nil || !ok {
		t.Fatalf("resolve: ok=%v err=%v", ok, err)
	}
	if authority.RoleID != "driver" {
		t.Fatalf("tie-break resolve = %+v", authority)
	}
}

// The 0215 header's branch-id rule, both violation directions: a
// branch-scoped role granted with a NULL branch_id and a global role
// granted with a branch_id are data defects (ErrGrantShape), and the
// violation in a losing-tier grant poisons resolution just the same:
// data defects surface loudly, never hide behind a higher grant.
func TestResolveStaffBranchIDRuleViolations(t *testing.T) {
	f := newB3Fixture(t)
	ctx := context.Background()
	resolver, err := NewStaffAuthorityResolver(f.pool)
	if err != nil {
		t.Fatalf("NewStaffAuthorityResolver: %v", err)
	}

	f.grant(t, "driver", "")
	_, _, err = resolver.ResolveStaff(ctx, f.platformUser, f.orgID)
	if !errors.Is(err, ErrGrantShape) {
		t.Fatalf("branch role with NULL branch_id: err=%v", err)
	}

	if _, err := f.pool.Exec(ctx, `DELETE FROM staff_role_grant WHERE platform_user_id = $1`, f.platformUser); err != nil {
		t.Fatalf("clear grants: %v", err)
	}
	f.grant(t, "general_manager", b3BranchOne)
	_, _, err = resolver.ResolveStaff(ctx, f.platformUser, f.orgID)
	if !errors.Is(err, ErrGrantShape) {
		t.Fatalf("global role with branch_id: err=%v", err)
	}

	if _, err := f.pool.Exec(ctx, `DELETE FROM staff_role_grant WHERE platform_user_id = $1`, f.platformUser); err != nil {
		t.Fatalf("clear grants: %v", err)
	}
	f.grant(t, "general_manager", "")
	f.grant(t, "yard_team", "")
	_, _, err = resolver.ResolveStaff(ctx, f.platformUser, f.orgID)
	if !errors.Is(err, ErrGrantShape) {
		t.Fatalf("losing-tier violation did not poison resolution: err=%v", err)
	}
}

// The linked users row's status gates resolution (review finding F-1),
// both directions and both non-ACTIVE values: an ACTIVE linked row
// resolves authority, a SUSPENDED and a DEACTIVATED linked row each
// return ErrStaffUserInactive, and restoring ACTIVE resolves again. A
// caller with no linked users row keeps authority from grants alone
// (asserted by TestResolveStaffBranchGrants' unlinked second principal).
func TestResolveStaffLinkedUserStatus(t *testing.T) {
	f := newB3Fixture(t)
	ctx := context.Background()
	resolver, err := NewStaffAuthorityResolver(f.pool)
	if err != nil {
		t.Fatalf("NewStaffAuthorityResolver: %v", err)
	}
	f.grant(t, "general_manager", "")

	if _, ok, err := resolver.ResolveStaff(ctx, f.platformUser, f.orgID); err != nil || !ok {
		t.Fatalf("active resolve: ok=%v err=%v", ok, err)
	}
	for _, status := range []string{"SUSPENDED", "DEACTIVATED", "INVITED"} {
		if _, err := f.pool.Exec(ctx, `UPDATE users SET status = $1 WHERE id = $2`, status, f.userID); err != nil {
			t.Fatalf("set status %s: %v", status, err)
		}
		_, _, err = resolver.ResolveStaff(ctx, f.platformUser, f.orgID)
		if !errors.Is(err, ErrStaffUserInactive) {
			t.Fatalf("status %s: err=%v, want ErrStaffUserInactive", status, err)
		}
	}
	if _, err := f.pool.Exec(ctx, `UPDATE users SET status = 'ACTIVE' WHERE id = $1`, f.userID); err != nil {
		t.Fatalf("restore status: %v", err)
	}
	authority, ok, err := resolver.ResolveStaff(ctx, f.platformUser, f.orgID)
	if err != nil || !ok || authority.RoleID != "general_manager" {
		t.Fatalf("restored resolve: authority=%+v ok=%v err=%v", authority, ok, err)
	}
}

// A tombstoned users row confers no branch grant (C35, review required
// fix 4): the grant row still exists, the authority still resolves, and
// the branch set is empty because the platform link joined through a
// tombstone reads nothing.
func TestTombstonedUsersRowConfersNoBranchGrant(t *testing.T) {
	f := newB3Fixture(t)
	ctx := context.Background()
	resolver, err := NewStaffAuthorityResolver(f.pool)
	if err != nil {
		t.Fatalf("NewStaffAuthorityResolver: %v", err)
	}
	f.grant(t, "inside_sales", b3BranchOne)
	f.branchGrant(t, b3BranchOne)

	authority, ok, err := resolver.ResolveStaff(ctx, f.platformUser, f.orgID)
	if err != nil || !ok {
		t.Fatalf("linked resolve: ok=%v err=%v", ok, err)
	}
	if len(authority.Branches) != 1 || authority.Branches[0] != b3BranchOne {
		t.Fatalf("linked branches = %v, want the granted branch", authority.Branches)
	}

	if _, err := f.pool.Exec(ctx, `UPDATE users SET deleted_at = now() WHERE id = $1`, f.userID); err != nil {
		t.Fatalf("tombstone users row: %v", err)
	}
	authority, ok, err = resolver.ResolveStaff(ctx, f.platformUser, f.orgID)
	if err != nil || !ok {
		t.Fatalf("unlinked-by-tombstone resolve: ok=%v err=%v", ok, err)
	}
	if len(authority.Branches) != 0 {
		t.Fatalf("tombstoned link conferred branches: %v", authority.Branches)
	}
	if authority.CanAccessBranch(b3BranchOne) {
		t.Fatalf("tombstoned link conferred branch access")
	}
}

// A tombstoned staff_role row confers no authority (review required fix
// 4): the grant is active, the role row's tombstone removes it from the
// join, and the caller resolves to no authority rather than inheriting
// anything. The role row is synthetic (the seeded catalog is never
// mutated), tier 3 branch scope, deleted at insert.
func TestTombstonedStaffRoleConfersNoAuthority(t *testing.T) {
	f := newB3Fixture(t)
	ctx := context.Background()
	resolver, err := NewStaffAuthorityResolver(f.pool)
	if err != nil {
		t.Fatalf("NewStaffAuthorityResolver: %v", err)
	}
	const deadRole = "b3_deadrole"
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO staff_role (id, label, tier, scope, origin, is_platform_role, deleted_at)
		VALUES ($1, 'B3 Proof Dead Role', 3, 'branch', 'new', FALSE, now())`, deadRole); err != nil {
		t.Fatalf("insert dead role: %v", err)
	}
	t.Cleanup(func() {
		// The grant references the role with ON DELETE RESTRICT, so the
		// grant rows go first; the fixture's own org cleanup runs later
		// and finds nothing left to conflict with.
		if _, err := f.pool.Exec(context.Background(), `DELETE FROM staff_role_grant WHERE role_id = $1`, deadRole); err != nil {
			t.Fatalf("clean dead role grants: %v", err)
		}
		if _, err := f.pool.Exec(context.Background(), `DELETE FROM staff_role WHERE id = $1`, deadRole); err != nil {
			t.Fatalf("clean dead role: %v", err)
		}
	})
	f.grant(t, deadRole, b3BranchOne)

	if _, ok, err := resolver.ResolveStaff(ctx, f.platformUser, f.orgID); err != nil || ok {
		t.Fatalf("tombstoned role resolved authority: ok=%v err=%v", ok, err)
	}

	// The control half: the same grant against the live role resolves.
	if _, err := f.pool.Exec(ctx, `UPDATE staff_role SET deleted_at = NULL WHERE id = $1`, deadRole); err != nil {
		t.Fatalf("revive role: %v", err)
	}
	if _, ok, err := resolver.ResolveStaff(ctx, f.platformUser, f.orgID); err != nil || !ok {
		t.Fatalf("revived role resolved no authority: ok=%v err=%v", ok, err)
	}
}

// A user_branch_grants row whose additive org_id is still NULL confers
// nothing (review required fix 4): the fence's scope predicate excludes
// it, so a branch-scoped authority resolves with an empty branch set
// until population fills the column.
func TestNullOrgBranchGrantConfersNothing(t *testing.T) {
	f := newB3Fixture(t)
	ctx := context.Background()
	resolver, err := NewStaffAuthorityResolver(f.pool)
	if err != nil {
		t.Fatalf("NewStaffAuthorityResolver: %v", err)
	}
	f.grant(t, "inside_sales", b3BranchOne)
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO user_branch_grants (user_id, branch_id)
		VALUES ($1, $2)`, f.userID, b3BranchOne); err != nil {
		t.Fatalf("insert NULL-org branch grant: %v", err)
	}

	authority, ok, err := resolver.ResolveStaff(ctx, f.platformUser, f.orgID)
	if err != nil || !ok {
		t.Fatalf("resolve: ok=%v err=%v", ok, err)
	}
	if len(authority.Branches) != 0 {
		t.Fatalf("NULL-org row conferred branches: %v", authority.Branches)
	}
	if authority.CanAccessBranch(b3BranchOne) {
		t.Fatalf("NULL-org row conferred branch access")
	}
}

// Branch grants through the platform link, both halves: the granted
// branch passes CanAccessBranch, the ungranted branch does not.
func TestResolveStaffBranchGrants(t *testing.T) {
	f := newB3Fixture(t)
	ctx := context.Background()
	resolver, err := NewStaffAuthorityResolver(f.pool)
	if err != nil {
		t.Fatalf("NewStaffAuthorityResolver: %v", err)
	}
	f.grant(t, "inside_sales", b3BranchOne)
	f.branchGrant(t, b3BranchOne)

	authority, ok, err := resolver.ResolveStaff(ctx, f.platformUser, f.orgID)
	if err != nil || !ok {
		t.Fatalf("resolve: ok=%v err=%v", ok, err)
	}
	if !authority.CanAccessBranch(b3BranchOne) {
		t.Fatalf("member branch refused")
	}
	if authority.CanAccessBranch(b3BranchTwo) {
		t.Fatalf("non-member branch admitted")
	}

	// A second platform user with no users row and no grants resolves
	// authority with an empty grant set: no branch passes.
	if _, err := f.pool.Exec(ctx,
		`INSERT INTO staff_role_grant (platform_user_id, role_id, org_id, branch_id) VALUES ($1, 'driver', $2, $3)`,
		f.platformUser2, f.orgID, b3BranchOne); err != nil {
		t.Fatalf("insert grant: %v", err)
	}
	other, ok, err := resolver.ResolveStaff(ctx, f.platformUser2, f.orgID)
	if err != nil || !ok {
		t.Fatalf("resolve: ok=%v err=%v", ok, err)
	}
	if len(other.Branches) != 0 {
		t.Fatalf("branches without a users link: %v", other.Branches)
	}
	if other.CanAccessBranch(b3BranchOne) {
		t.Fatalf("branch admitted with no grant")
	}
}
