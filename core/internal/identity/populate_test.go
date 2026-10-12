package identity

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The B4 population proofs. Every database leg runs against a THROWAWAY
// database (migrations plus the catalog seeds only, never hh31_qa or
// hh31_scrubbed) pointed to by HH_B4_TEST_DATABASE_URL; absent the variable
// the legs skip. The FROM side is a synthetic rebuild of the pin-era
// fixture shape recorded in from-counts.txt (16 users, 13 user_roles on 13
// distinct users, 11 branch grants over 8 users and 2 branches with 3
// multi-branch users, 2 portal users both account_admin, 7 dispatch driver
// registry rows with 1 user-linked), plus three probe rows that exist only
// to prove the both-halves legs (a pre-stamped row, an unmatched-directory
// row, a pre-stamped tenant row); the expected counts below name them
// separately from the fixture classes. All values are invented: reserved
// .test logins, b4pop- subjects, bb400000 UUID block.

func b4Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HH_B4_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("HH_B4_TEST_DATABASE_URL is not set; population legs need the throwaway database")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func b4Count(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v (query %.60s...)", err, query)
	}
	return n
}

const (
	b4BranchA   = "bb400000-0000-4000-8000-0000000000a1"
	b4BranchB   = "bb400000-0000-4000-8000-0000000000a2"
	b4CustomerA = "bb400000-0000-4000-8000-0000000000c1"
	b4CustomerB = "bb400000-0000-4000-8000-0000000000c2"
	b4OrgSlug   = "b4-fixture-org"
	// The pre-stamped probes carry foreign scope values on purpose: no
	// foreign key constrains the additive columns (0213), and the stamp
	// steps must leave non-NULL values untouched.
	b4PreSetOrg    = "bb400000-0000-4000-8000-000000000fff"
	b4PreSetTenant = "bb400000-0000-4000-8000-000000000ffe"
)

func b4UserID(i int) string { return fmt.Sprintf("bb400000-0000-4000-8000-0000000001%02x", i) }

// Probe and planted-leg user ids, outside the 1..16 fixture block.
const (
	b4UserPre            = "bb400000-0000-4000-8000-0000000001f1" // org pre-stamped probe
	b4UserNoMatch        = "bb400000-0000-4000-8000-0000000001f2" // directory-unmatched probe
	b4UserBranchless     = "bb400000-0000-4000-8000-0000000001f3" // F3 leg
	b4UserTombstoned     = "bb400000-0000-4000-8000-0000000001f4" // tombstone leg
	b4UserInactiveDriver = "bb400000-0000-4000-8000-0000000001f5" // inactive driver leg
	b4UserGate           = "bb400000-0000-4000-8000-0000000001f6" // seed-gate leg
)

func b4PortalID(i int) string { return fmt.Sprintf("bb400000-0000-4000-8000-000000000d%02x", i) }
func b4DriverID(i int) string { return fmt.Sprintf("bb400000-0000-4000-8000-000000000e%02x", i) }
func b4Subject(i int) string  { return fmt.Sprintf("b4pop-u%02d", i) }

// b4Directory is the fixture PlatformDirectory: exact email answers for the
// linked rows, nothing for the unmatched probe, an injectable error for the
// loud-failure leg. Values are invented subjects, never real platform ids.
type b4Directory struct {
	byEmail map[string]string
	err     error
}

func (d b4Directory) PlatformUserByEmail(_ context.Context, email string) (string, bool, error) {
	if d.err != nil {
		return "", false, d.err
	}
	s, ok := d.byEmail[email]
	return s, ok, nil
}

// b4Fixture builds the synthetic FROM side and owns its exact cleanup.
type b4Fixture struct {
	pool  *pgxpool.Pool
	orgID string // set by the first Populate run
}

func b4RoleID(t *testing.T, pool *pgxpool.Pool, code string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM roles WHERE code = $1`, code).Scan(&id); err != nil {
		t.Fatalf("role id for %s: %v (is the catalog applied?)", code, err)
	}
	return id
}

// b4BuildFromFixture inserts the FROM side. The catalog rows (staff_role,
// hhpro_contractor_role_map, roles) are migration seeds and are never
// written here.
func b4BuildFromFixture(t *testing.T, pool *pgxpool.Pool) *b4Fixture {
	t.Helper()
	ctx := context.Background()
	f := &b4Fixture{pool: pool}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatalf("fixture build: %v (statement %.60s...)", err, query)
		}
	}

	exec(`INSERT INTO locations (id, branch_id, parent_id, loc_type, code, name, address) VALUES
		($1, $1, NULL, 'BRANCH', 'B4A', 'B4 Fixture Branch A', '{}'),
		($2, $2, NULL, 'BRANCH', 'B4B', 'B4 Fixture Branch B', '{}')`, b4BranchA, b4BranchB)

	// Sixteen carried users: fifteen ACTIVE, one INVITED (from-counts
	// battery 1), none linked, none org-stamped.
	for i := 1; i <= 16; i++ {
		status := "ACTIVE"
		if i == 16 {
			status = "INVITED"
		}
		exec(`INSERT INTO users (id, email, display_name, status) VALUES ($1, $2, $3, $4)`,
			b4UserID(i), fmt.Sprintf("b4u%02d@fixture.test", i), fmt.Sprintf("B4 Fixture User %02d", i), status)
	}
	// The probes: one org pre-stamped (the stamp step must not touch it),
	// one that the directory will never answer.
	exec(`INSERT INTO users (id, email, display_name, status, org_id) VALUES ($1, 'b4upre@fixture.test', 'B4 Fixture Pre Stamped', 'ACTIVE', $2)`,
		b4UserPre, b4PreSetOrg)
	exec(`INSERT INTO users (id, email, display_name, status) VALUES ($1, 'b4unomatch@fixture.test', 'B4 Fixture No Match', 'ACTIVE')`,
		b4UserNoMatch)

	// Thirteen user_roles rows on thirteen distinct users, mirroring the
	// fixture class: 5 SUPER_ADMIN plus one each of the eight other used
	// codes (from-counts battery 1 and 2).
	roleByUser := map[int]string{
		1: "SUPER_ADMIN", 2: "SUPER_ADMIN", 3: "SUPER_ADMIN", 4: "SUPER_ADMIN", 5: "SUPER_ADMIN",
		6: "GENERAL_MANAGER", 7: "FINANCIAL_CONTROLLER", 8: "YARD_MANAGER", 9: "SALES_MANAGER",
		10: "LOGISTICS_MANAGER", 11: "YARD_TEAM", 12: "INSIDE_SALES", 13: "DRIVERS",
	}
	for i, code := range roleByUser {
		exec(`INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			b4UserID(i), b4RoleID(t, pool, code))
	}

	// Eleven branch grants over eight users and two branches, three users
	// multi-branch (from-counts battery 2). Every branch-role holder
	// (users 8 to 13) holds branch A; the deterministic primary of every
	// multi-branch holder is A (the lower uuid).
	grants := []struct {
		user   int
		branch string
	}{
		{1, b4BranchA}, {1, b4BranchB}, {2, b4BranchA},
		{8, b4BranchA}, {8, b4BranchB}, {9, b4BranchA}, {9, b4BranchB},
		{10, b4BranchA}, {11, b4BranchA}, {12, b4BranchA}, {13, b4BranchA},
	}
	for _, g := range grants {
		exec(`INSERT INTO user_branch_grants (user_id, branch_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			b4UserID(g.user), g.branch)
	}

	exec(`INSERT INTO customers (id, account_no, name, customer_type, home_branch_id) VALUES
		($1, 'FXB4-C1', 'B4 Fixture Contractor A', 'CONTRACTOR', $3),
		($2, 'FXB4-C2', 'B4 Fixture Contractor B', 'CONTRACTOR', $3)`,
		b4CustomerA, b4CustomerB, b4BranchA)

	// Two carried portal rows, both account_admin and active, plus the
	// tenant pre-stamped probe.
	exec(`INSERT INTO portal_users (id, customer_id, email, name, password_hash, role, status) VALUES
		($1, $2, 'b4p01@fixture.test', 'B4 Fixture Portal One', 'b4-not-a-hash-1', 'account_admin', 'active'),
		($3, $4, 'b4p02@fixture.test', 'B4 Fixture Portal Two', 'b4-not-a-hash-2', 'account_admin', 'active')`,
		b4PortalID(1), b4CustomerA, b4PortalID(2), b4CustomerB)
	exec(`INSERT INTO portal_users (id, customer_id, email, name, password_hash, role, status, tenant_id) VALUES
		($1, $2, 'b4ppre@fixture.test', 'B4 Fixture Portal Pre', 'b4-not-a-hash-3', 'account_admin', 'active', $3)`,
		b4PortalID(3), b4CustomerB, b4PreSetTenant)

	// Seven registry rows: one user-linked (the DRIVERS holder, ACTIVE),
	// six casual (from-counts battery 1).
	exec(`INSERT INTO dispatch_drivers (id, branch_id, user_id, name, licence_class, is_active) VALUES
		($1, $2, $3, 'B4 Fixture Driver Linked', 'B4G', TRUE)`, b4DriverID(1), b4BranchA, b4UserID(13))
	for i := 2; i <= 7; i++ {
		branch := b4BranchA
		if i%2 == 1 {
			branch = b4BranchB
		}
		exec(`INSERT INTO dispatch_drivers (id, branch_id, user_id, name, licence_class, is_active) VALUES
			($1, $2, NULL, $3, 'B4G', TRUE)`, b4DriverID(i), branch, fmt.Sprintf("B4 Fixture Casual %02d", i))
	}

	t.Cleanup(func() { f.cleanup(t) })
	return f
}

// cleanup removes exactly the fixture rows, children before parents, and
// every row the program wrote for them (grants and driver profiles by the
// fixture subject prefix or the fixture org). The catalog seeds are never
// touched.
func (f *b4Fixture) cleanup(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := f.pool.Exec(ctx, query, args...); err != nil {
			t.Fatalf("fixture cleanup: %v (statement %.60s...)", err, query)
		}
	}
	exec(`DELETE FROM staff_role_grant WHERE platform_user_id LIKE 'b4pop-%'
		OR org_id IN (SELECT id FROM org WHERE slug = $1)`, b4OrgSlug)
	exec(`DELETE FROM driver_profile WHERE platform_user_id LIKE 'b4pop-%'
		OR org_id IN (SELECT id FROM org WHERE slug = $1)`, b4OrgSlug)
	exec(`DELETE FROM dispatch_drivers WHERE id::text LIKE 'bb400000-0000-4000-8000-000000000e%'`)
	exec(`DELETE FROM user_roles WHERE user_id::text LIKE 'bb400000-0000-4000-8000-0000000001%'`)
	exec(`DELETE FROM user_branch_grants WHERE user_id::text LIKE 'bb400000-0000-4000-8000-0000000001%'`)
	exec(`DELETE FROM portal_users WHERE id::text LIKE 'bb400000-0000-4000-8000-000000000d%'`)
	exec(`DELETE FROM customers WHERE id IN ($1, $2)`, b4CustomerA, b4CustomerB)
	exec(`DELETE FROM users WHERE id::text LIKE 'bb400000-0000-4000-8000-0000000001%'`)
	exec(`DELETE FROM locations WHERE id IN ($1, $2)`, b4BranchA, b4BranchB)
	exec(`DELETE FROM org WHERE slug = $1`, b4OrgSlug)
}

// directory answers the sixteen fixture users, the pre-stamped probe, and
// the three portal rows; the no-match probe is absent by construction.
func (f *b4Fixture) directory() b4Directory {
	m := map[string]string{}
	for i := 1; i <= 16; i++ {
		m[fmt.Sprintf("b4u%02d@fixture.test", i)] = b4Subject(i)
	}
	m["b4upre@fixture.test"] = "b4pop-upre"
	m["b4p01@fixture.test"] = "b4pop-p01"
	m["b4p02@fixture.test"] = "b4pop-p02"
	m["b4ppre@fixture.test"] = "b4pop-ppre"
	return b4Directory{byEmail: m}
}

func (f *b4Fixture) config() PopulateConfig {
	return PopulateConfig{OrgSlug: b4OrgSlug, OrgName: "B4 Fixture Org", Directory: f.directory()}
}

// The end-to-end legs over the fixture shape, in dependency order: the
// pre-S2 run writes nothing link-derived, the linked run writes the
// expected classes, a re-run inside one transaction is a no-op, the
// verification pass counts the classes, the read paths resolve populated
// principals, and the planted legs prove the skip classes. Subtests share
// the one fixture and run in source order.
func TestPopulateEndToEnd(t *testing.T) {
	pool := b4Pool(t)
	ctx := context.Background()
	f := b4BuildFromFixture(t, pool)

	t.Run("step 0 gate green on the fixture", func(t *testing.T) {
		seed, err := VerifySeedCoverage(ctx, pool)
		if err != nil || !seed.OK() {
			t.Fatalf("gate: report=%+v err=%v", seed, err)
		}
	})

	t.Run("pre-S2 run writes no linked rows", func(t *testing.T) {
		// A nil directory is the pre-S2 shape (plan step 4): steps 5 and 8
		// skip every row and the skip classes are counted, never written.
		report, err := Populate(ctx, pool, PopulateConfig{OrgSlug: b4OrgSlug, OrgName: "B4 Fixture Org"})
		if err != nil {
			t.Fatalf("populate: %v", err)
		}
		if !report.Seed.OK() || !report.OrgInserted || report.OrgID == "" {
			t.Fatalf("seed/org: %+v", report)
		}
		f.orgID = report.OrgID
		wantStamp := StampReport{UsersStamped: 17, UserRolesStamped: 13, UserBranchGrantsStamped: 11, PortalTenantsStamped: 2}
		if report.Stamp != wantStamp {
			t.Fatalf("stamp = %+v, want %+v", report.Stamp, wantStamp)
		}
		if report.Link != (LinkReport{}) {
			t.Fatalf("link without a directory = %+v, want zero", report.Link)
		}
		wantGrants := GrantReport{SkippedNoLinkage: 13}
		if report.Grants != wantGrants {
			t.Fatalf("grants = %+v, want %+v", report.Grants, wantGrants)
		}
		wantDrivers := DriverReport{SkippedNoIdentity: 7}
		if report.Drivers != wantDrivers {
			t.Fatalf("drivers = %+v, want %+v", report.Drivers, wantDrivers)
		}
		counts, err := VerifyPopulation(ctx, pool, f.orgID)
		if err != nil {
			t.Fatalf("verify: %v", err)
		}
		if counts.StaffGrants != 0 || counts.DriverProfiles != 0 {
			t.Fatalf("pre-S2 wrote linked rows: %+v", counts)
		}
		if counts.UsersLinked != 0 || counts.UsersUnlinked != 18 || counts.PortalLinked != 0 || counts.PortalUnlinked != 3 {
			t.Fatalf("pre-S2 linkage counts = %+v", counts)
		}
		if counts.UsersUnstampedOrg != 0 || counts.UserRolesUnstampedOrg != 0 ||
			counts.UserBranchGrantsUnstampedOrg != 0 || counts.PortalUsersUnstampedTenant != 0 {
			t.Fatalf("unstamped classes after steps 2 and 3: %+v", counts)
		}
	})

	t.Run("linked run writes the expected classes", func(t *testing.T) {
		report, err := Populate(ctx, pool, f.config())
		if err != nil {
			t.Fatalf("populate: %v", err)
		}
		if report.OrgInserted || report.OrgID != f.orgID {
			t.Fatalf("org re-provision moved the anchor: %+v", report)
		}
		if report.Stamp != (StampReport{}) {
			t.Fatalf("second stamp = %+v, want zero", report.Stamp)
		}
		// 16 fixture users plus the pre-stamped probe link; the no-match
		// probe is counted and left alone. All three portal rows link.
		wantLink := LinkReport{UsersLinked: 17, UsersUnmatched: 1, PortalLinked: 3}
		if report.Link != wantLink {
			t.Fatalf("link = %+v, want %+v", report.Link, wantLink)
		}
		// Thirteen grants from thirteen user_roles rows (from-counts
		// reading): 7 global (5 super_admin, general_manager,
		// financial_controller), 6 branch. Zero skips, zero violations.
		wantGrants := GrantReport{Inserted: 13}
		if report.Grants != wantGrants {
			t.Fatalf("grants = %+v, want %+v", report.Grants, wantGrants)
		}
		wantDrivers := DriverReport{Inserted: 1, SkippedNoIdentity: 6}
		if report.Drivers != wantDrivers {
			t.Fatalf("drivers = %+v, want %+v", report.Drivers, wantDrivers)
		}
	})

	t.Run("verification pass counts the classes", func(t *testing.T) {
		counts, err := VerifyPopulation(ctx, pool, f.orgID)
		if err != nil {
			t.Fatalf("verify: %v", err)
		}
		want := PopulationCounts{
			OrgRows:                   1,
			UsersLinked:               17,
			UsersUnlinked:             1,
			PortalLinked:              3,
			PortalUnlinked:            0,
			StaffGrants:               13,
			StaffGrantsGlobal:         7,
			StaffGrantsBranch:         6,
			StaffGrantShapeViolations: 0,
			StaffGrantsUnlinked:       0,
			DriverProfiles:            1,
			DriverProfilesInactive:    0,
			DispatchDriversNoIdentity: 6,
			PortalRolesNonCanonical:   0,
		}
		if counts != want {
			t.Fatalf("counts = %+v, want %+v", counts, want)
		}
		// Carried grants never invent a granter and never take a seq
		// (0213); branch-class grants all carry the deterministic primary
		// branch A, never B, and the driver profile carries the registry
		// licence class with the three sourceless columns NULL.
		if n := b4Count(t, pool, `SELECT count(*) FROM staff_role_grant WHERE org_id = $1 AND (granted_by IS NOT NULL OR seq IS NOT NULL)`, f.orgID); n != 0 {
			t.Fatalf("carried grants invented a granter or seq: %d", n)
		}
		if n := b4Count(t, pool, `SELECT count(*) FROM staff_role_grant WHERE org_id = $1 AND branch_id = $2`, f.orgID, b4BranchA); n != 6 {
			t.Fatalf("branch-class grants on the primary branch = %d, want 6", n)
		}
		if n := b4Count(t, pool, `SELECT count(*) FROM staff_role_grant WHERE org_id = $1 AND branch_id = $2`, f.orgID, b4BranchB); n != 0 {
			t.Fatalf("grants on the non-primary branch = %d, want 0", n)
		}
		if n := b4Count(t, pool, `SELECT count(*) FROM driver_profile WHERE org_id = $1 AND licence_class = 'B4G' AND truck_id IS NULL AND licence_number IS NULL AND licence_expiry IS NULL AND seq IS NULL`, f.orgID); n != 1 {
			t.Fatalf("driver profile carry shape = %d, want 1", n)
		}
	})

	t.Run("re-run inside one transaction is a no-op", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer tx.Rollback(ctx)
		report, err := Populate(ctx, tx, f.config())
		if err != nil {
			t.Fatalf("populate in tx: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
		// The unmatched probe is a standing class: it has no platform
		// counterpart by construction, so every run recounts it. Nothing
		// else moved.
		if report.OrgInserted || report.Stamp != (StampReport{}) || report.Link != (LinkReport{UsersUnmatched: 1}) {
			t.Fatalf("re-run wrote: %+v", report)
		}
		if report.Grants != (GrantReport{Existing: 13}) {
			t.Fatalf("re-run grants = %+v, want 13 existing", report.Grants)
		}
		if report.Drivers != (DriverReport{Existing: 1, SkippedNoIdentity: 6}) {
			t.Fatalf("re-run drivers = %+v, want 1 existing, 6 skipped", report.Drivers)
		}
		counts, err := VerifyPopulation(ctx, pool, f.orgID)
		if err != nil {
			t.Fatalf("verify after re-run: %v", err)
		}
		if counts.StaffGrants != 13 || counts.DriverProfiles != 1 {
			t.Fatalf("re-run changed the populated classes: %+v", counts)
		}
	})

	t.Run("read paths resolve populated principals", func(t *testing.T) {
		resolver, err := NewStaffAuthorityResolver(pool)
		if err != nil {
			t.Fatalf("resolver: %v", err)
		}
		// Tier 1 global twice over: the general manager and a super_admin
		// holder both resolve IsAdmin, with no branch set.
		gm, ok, err := resolver.ResolveStaff(ctx, b4Subject(6), f.orgID)
		if err != nil || !ok || gm.RoleID != "general_manager" || gm.Tier != TierLeadership || gm.Scope != ScopeGlobal || !gm.IsAdmin() {
			t.Fatalf("general manager resolve = %+v ok=%v err=%v", gm, ok, err)
		}
		sa, ok, err := resolver.ResolveStaff(ctx, b4Subject(3), f.orgID)
		if err != nil || !ok || sa.RoleID != "super_admin" || !sa.IsAdmin() || sa.Branches != nil {
			t.Fatalf("super admin resolve = %+v ok=%v err=%v", sa, ok, err)
		}
		// The branch-scope holders resolve their primary-grant breadth:
		// branch A passes, branch B refuses for the A-only holder.
		is, ok, err := resolver.ResolveStaff(ctx, b4Subject(12), f.orgID)
		if err != nil || !ok || is.RoleID != "inside_sales" || is.Tier != TierFrontline || is.Scope != ScopeBranch {
			t.Fatalf("inside sales resolve = %+v ok=%v err=%v", is, ok, err)
		}
		if len(is.Branches) != 1 || is.Branches[0] != b4BranchA || !is.CanAccessBranch(b4BranchA) || is.CanAccessBranch(b4BranchB) {
			t.Fatalf("inside sales branches = %v", is.Branches)
		}
		if is.IsManager() {
			t.Fatalf("frontline passes the manager gate")
		}
		// The driver resolves the driver role as a staff grant (plan step
		// 8: a role plus a profile row, not a separate identity).
		dr, ok, err := resolver.ResolveStaff(ctx, b4Subject(13), f.orgID)
		if err != nil || !ok || dr.RoleID != RoleDriver || dr.Scope != ScopeBranch {
			t.Fatalf("driver resolve = %+v ok=%v err=%v", dr, ok, err)
		}
		// A user holding no role resolves to no authority (the RBAC-403
		// upstream), never an error.
		if _, ok, err := resolver.ResolveStaff(ctx, b4Subject(14), f.orgID); err != nil || ok {
			t.Fatalf("role-less user resolve: ok=%v err=%v", ok, err)
		}
		// Status carrying (recorded ruling R2): the grant stays written
		// regardless of users.status, and the read path gates. Suspending
		// the general manager flips resolution to ErrStaffUserInactive;
		// restoring ACTIVE resolves again.
		if _, err := pool.Exec(ctx, `UPDATE users SET status = 'SUSPENDED' WHERE id = $1`, b4UserID(6)); err != nil {
			t.Fatalf("suspend: %v", err)
		}
		if n := b4Count(t, pool, `SELECT count(*) FROM staff_role_grant WHERE platform_user_id = $1 AND deleted_at IS NULL`, b4Subject(6)); n != 1 {
			t.Fatalf("suspension removed the grant: %d", n)
		}
		if _, _, err := resolver.ResolveStaff(ctx, b4Subject(6), f.orgID); !errors.Is(err, ErrStaffUserInactive) {
			t.Fatalf("suspended resolve: err=%v, want ErrStaffUserInactive", err)
		}
		if _, err := pool.Exec(ctx, `UPDATE users SET status = 'ACTIVE' WHERE id = $1`, b4UserID(6)); err != nil {
			t.Fatalf("restore: %v", err)
		}
		if _, ok, err := resolver.ResolveStaff(ctx, b4Subject(6), f.orgID); err != nil || !ok {
			t.Fatalf("restored resolve: ok=%v err=%v", ok, err)
		}

		// The portal read path: the account_admin resolves inside its own
		// tenant, the same subject in the other tenant is no authority
		// (tenant fence), and a non-active row is ErrPortalUserInactive.
		portal, err := NewPortalRoleResolver(pool)
		if err != nil {
			t.Fatalf("portal resolver: %v", err)
		}
		role, ok, err := portal.PortalRole(ctx, "b4pop-p01", b4CustomerA)
		if err != nil || !ok || role != PortalRoleAccountAdmin {
			t.Fatalf("portal resolve = %q ok=%v err=%v", role, ok, err)
		}
		if _, ok, err := portal.PortalRole(ctx, "b4pop-p01", b4CustomerB); err != nil || ok {
			t.Fatalf("cross-tenant portal resolve: ok=%v err=%v", ok, err)
		}
		if _, err := pool.Exec(ctx, `UPDATE portal_users SET status = 'inactive' WHERE id = $1`, b4PortalID(2)); err != nil {
			t.Fatalf("deactivate portal row: %v", err)
		}
		if _, _, err := portal.PortalRole(ctx, "b4pop-p02", b4CustomerB); !errors.Is(err, ErrPortalUserInactive) {
			t.Fatalf("inactive portal resolve: err=%v, want ErrPortalUserInactive", err)
		}
		if _, err := pool.Exec(ctx, `UPDATE portal_users SET status = 'active' WHERE id = $1`, b4PortalID(2)); err != nil {
			t.Fatalf("restore portal row: %v", err)
		}
	})

	t.Run("branchless branch-role holder is skipped and counted", func(t *testing.T) {
		// Plan finding F3 (recorded ruling R5): a branch-scope role with
		// zero live branch grants derives no grant, counted as the skip
		// class, never inserted with a NULL branch_id.
		if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, display_name, status, platform_user_id, org_id)
			VALUES ($1, 'b4u18@fixture.test', 'B4 Fixture Branchless', 'ACTIVE', 'b4pop-u18', $2)`, b4UserBranchless, f.orgID); err != nil {
			t.Fatalf("plant branchless user: %v", err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2)`,
			b4UserBranchless, b4RoleID(t, pool, "BRANCH_MANAGER")); err != nil {
			t.Fatalf("plant branchless role: %v", err)
		}
		report, err := PopulateStaffRoleGrants(ctx, pool, f.orgID)
		if err != nil {
			t.Fatalf("grants: %v", err)
		}
		if report.Inserted != 0 || report.Existing != 13 || report.SkippedBranchless != 1 {
			t.Fatalf("branchless leg report = %+v", report)
		}
		if n := b4Count(t, pool, `SELECT count(*) FROM staff_role_grant WHERE platform_user_id = 'b4pop-u18'`); n != 0 {
			t.Fatalf("branchless holder received a grant: %d", n)
		}
	})

	t.Run("tombstoned rows never derive grants or profiles", func(t *testing.T) {
		// A tombstoned users row is never a candidate (C35), a tombstoned
		// user_roles row is never a candidate, and a registry row whose
		// user is tombstoned joins to no identity.
		if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, display_name, status, platform_user_id, org_id, deleted_at)
			VALUES ($1, 'b4u19@fixture.test', 'B4 Fixture Tombstoned', 'ACTIVE', 'b4pop-u19', $2, now())`, b4UserTombstoned, f.orgID); err != nil {
			t.Fatalf("plant tombstoned user: %v", err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2)`,
			b4UserTombstoned, b4RoleID(t, pool, "GENERAL_MANAGER")); err != nil {
			t.Fatalf("plant tombstoned user role: %v", err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO user_roles (user_id, role_id, deleted_at) VALUES ($1, $2, now())`,
			b4UserID(15), b4RoleID(t, pool, "INSIDE_SALES")); err != nil {
			t.Fatalf("plant tombstoned role row: %v", err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO dispatch_drivers (id, branch_id, user_id, name, licence_class, is_active)
			VALUES ($1, $2, $3, 'B4 Fixture Tombstoned Driver', 'B4G', TRUE)`, b4DriverID(8), b4BranchA, b4UserTombstoned); err != nil {
			t.Fatalf("plant tombstoned registry row: %v", err)
		}
		grants, err := PopulateStaffRoleGrants(ctx, pool, f.orgID)
		if err != nil {
			t.Fatalf("grants: %v", err)
		}
		if grants.Inserted != 0 || grants.Existing != 13 || grants.SkippedNoLinkage != 0 {
			t.Fatalf("tombstone leg grants = %+v", grants)
		}
		if n := b4Count(t, pool, `SELECT count(*) FROM staff_role_grant WHERE platform_user_id IN ('b4pop-u19', $1)`, b4Subject(15)); n != 0 {
			t.Fatalf("tombstoned rows derived grants: %d", n)
		}
		drivers, err := PopulateDriverProfiles(ctx, pool, f.orgID)
		if err != nil {
			t.Fatalf("drivers: %v", err)
		}
		if drivers.Inserted != 0 || drivers.Existing != 1 || drivers.SkippedNoIdentity != 7 {
			t.Fatalf("tombstone leg drivers = %+v", drivers)
		}
	})

	t.Run("inactive registry row still carries its profile", func(t *testing.T) {
		// is_active is an ERP fact and carries verbatim; the profile row
		// is written regardless, both halves of the carry rule.
		if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, display_name, status, platform_user_id, org_id)
			VALUES ($1, 'b4u20@fixture.test', 'B4 Fixture Inactive Driver', 'ACTIVE', 'b4pop-u20', $2)`, b4UserInactiveDriver, f.orgID); err != nil {
			t.Fatalf("plant inactive driver user: %v", err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO dispatch_drivers (id, branch_id, user_id, name, licence_class, is_active)
			VALUES ($1, $2, $3, 'B4 Fixture Inactive Driver', 'B4H', FALSE)`, b4DriverID(9), b4BranchA, b4UserInactiveDriver); err != nil {
			t.Fatalf("plant inactive registry row: %v", err)
		}
		drivers, err := PopulateDriverProfiles(ctx, pool, f.orgID)
		if err != nil {
			t.Fatalf("drivers: %v", err)
		}
		if drivers.Inserted != 1 || drivers.Existing != 1 {
			t.Fatalf("inactive driver leg = %+v", drivers)
		}
		counts, err := VerifyPopulation(ctx, pool, f.orgID)
		if err != nil {
			t.Fatalf("verify: %v", err)
		}
		if counts.DriverProfiles != 2 || counts.DriverProfilesInactive != 1 {
			t.Fatalf("driver profile counts = %+v", counts)
		}
		if n := b4Count(t, pool, `SELECT count(*) FROM driver_profile WHERE org_id = $1 AND licence_class = 'B4H' AND NOT is_active`, f.orgID); n != 1 {
			t.Fatalf("inactive carry shape = %d, want 1", n)
		}
	})
}

// The branch-id rule predicate, both halves of every arm: the two legal
// shapes pass, the two illegal shapes and the unknown scope refuse.
func TestValidateGrantShapeLegs(t *testing.T) {
	branch := b4BranchA
	for _, leg := range []struct {
		name    string
		scope   string
		branch  *string
		wantErr bool
	}{
		{"global with NULL branch is legal", ScopeGlobal, nil, false},
		{"branch with a branch is legal", ScopeBranch, &branch, false},
		{"branch with NULL branch refuses", ScopeBranch, nil, true},
		{"global with a branch refuses", ScopeGlobal, &branch, true},
		{"unknown scope refuses", "weird", nil, true},
	} {
		err := validateGrantShape(leg.scope, leg.branch)
		if leg.wantErr && !errors.Is(err, ErrGrantShape) {
			t.Fatalf("%s: err=%v, want ErrGrantShape", leg.name, err)
		}
		if !leg.wantErr && err != nil {
			t.Fatalf("%s: err=%v, want nil", leg.name, err)
		}
	}
}

// A malformed grant candidate is refused at the insert boundary, counted,
// and never lands in the table; the two legal shapes insert and re-run to a
// no-op. The derivation never produces a malformed row (the only reachable
// shape defect is the branchless class, which the plan counts as a skip),
// so this leg exercises the boundary itself directly.
func TestInsertGrantRefusesMalformed(t *testing.T) {
	pool := b4Pool(t)
	ctx := context.Background()
	orgID, _, err := ProvisionOrgAnchor(ctx, pool, "b4-malformed-org", "B4 Malformed Org")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	// The legal branch candidate references a real locations row
	// (branch_id carries a foreign key); the malformed legs never reach
	// the insert, so they need none.
	if _, err := pool.Exec(ctx, `INSERT INTO locations (id, branch_id, parent_id, loc_type, code, name, address)
		VALUES ($1, $1, NULL, 'BRANCH', 'B4A', 'B4 Fixture Branch A', '{}') ON CONFLICT (id) DO NOTHING`, b4BranchA); err != nil {
		t.Fatalf("plant branch: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM staff_role_grant WHERE org_id = $1`, orgID); err != nil {
			t.Fatalf("clean grants: %v", err)
		}
		if _, err := pool.Exec(context.Background(), `DELETE FROM org WHERE id = $1`, orgID); err != nil {
			t.Fatalf("clean org: %v", err)
		}
		if _, err := pool.Exec(context.Background(), `DELETE FROM locations WHERE id = $1`, b4BranchA); err != nil {
			t.Fatalf("clean branch: %v", err)
		}
	})
	branch := b4BranchA
	for _, c := range []grantCandidate{
		{platformUserID: "b4pop-mal-1", roleID: "yard_team", scope: ScopeBranch},
		{platformUserID: "b4pop-mal-2", roleID: "hr", scope: ScopeGlobal, branchID: &branch},
		{platformUserID: "b4pop-mal-3", roleID: "hr", scope: "weird"},
	} {
		if _, err := insertGrant(ctx, pool, orgID, c); !errors.Is(err, ErrGrantShape) {
			t.Fatalf("malformed %+v: err=%v, want ErrGrantShape", c, err)
		}
	}
	if n := b4Count(t, pool, `SELECT count(*) FROM staff_role_grant WHERE org_id = $1`, orgID); n != 0 {
		t.Fatalf("a malformed row landed: %d", n)
	}
	// The control half: both legal shapes insert, and re-inserting is the
	// idempotent no-op the unique index guarantees.
	for _, c := range []grantCandidate{
		{platformUserID: "b4pop-ok-1", roleID: "hr", scope: ScopeGlobal},
		{platformUserID: "b4pop-ok-2", roleID: "yard_team", scope: ScopeBranch, branchID: &branch},
	} {
		inserted, err := insertGrant(ctx, pool, orgID, c)
		if err != nil || !inserted {
			t.Fatalf("legal %+v: inserted=%v err=%v", c, inserted, err)
		}
	}
	inserted, err := insertGrant(ctx, pool, orgID, grantCandidate{platformUserID: "b4pop-ok-1", roleID: "hr", scope: ScopeGlobal})
	if err != nil || inserted {
		t.Fatalf("re-insert: inserted=%v err=%v, want a no-op", inserted, err)
	}
	if n := b4Count(t, pool, `SELECT count(*) FROM staff_role_grant WHERE org_id = $1`, orgID); n != 2 {
		t.Fatalf("legal rows = %d, want 2", n)
	}
}

// Org anchor provisioning: the pinned slug shape is enforced before insert
// in both directions, mixed case normalizes to the lowercase form, the
// upsert converges the display name without moving the id, and every
// malformed class refuses with no row written (B3 review item 5).
func TestProvisionOrgAnchorSlugLegs(t *testing.T) {
	pool := b4Pool(t)
	ctx := context.Background()
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM org WHERE slug LIKE 'b4-anchor-%'`); err != nil {
			t.Fatalf("clean anchors: %v", err)
		}
	})

	id1, inserted, err := ProvisionOrgAnchor(ctx, pool, "b4-anchor-one", "B4 Anchor One")
	if err != nil || !inserted {
		t.Fatalf("first provision: inserted=%v err=%v", inserted, err)
	}
	id2, inserted, err := ProvisionOrgAnchor(ctx, pool, "b4-anchor-one", "B4 Anchor One Renamed")
	if err != nil || inserted || id2 != id1 {
		t.Fatalf("re-provision: id moved or insert reported: %v %v", id2 != id1, inserted)
	}
	if n := b4Count(t, pool, `SELECT count(*) FROM org WHERE slug = 'b4-anchor-one' AND name = 'B4 Anchor One Renamed'`); n != 1 {
		t.Fatalf("upsert did not converge the name: %d", n)
	}

	// Mixed case normalizes to the lowercase form the resolver reads.
	id3, _, err := ProvisionOrgAnchor(ctx, pool, "B4-Anchor-TWO", "B4 Anchor Two")
	if err != nil {
		t.Fatalf("mixed case provision: %v", err)
	}
	if n := b4Count(t, pool, `SELECT count(*) FROM org WHERE id = $1 AND slug = 'b4-anchor-two'`, id3); n != 1 {
		t.Fatalf("stored slug is not the lowercase form: %d", n)
	}

	for _, slug := range []string{
		"",
		"-b4-anchor-bad",        // must start lowercase alphanumeric
		"b4 anchor bad",         // no spaces
		"b4/anchor",             // outside the vocabulary
		"B4 ANCHOR",             // folds to a space-carrying value, still refused
		strings.Repeat("a", 33), // over the 32 character bound
	} {
		if _, _, err := ProvisionOrgAnchor(ctx, pool, slug, "B4 Anchor Bad"); !errors.Is(err, ErrOrgSlugShape) {
			t.Fatalf("slug %q: err=%v, want ErrOrgSlugShape", slug, err)
		}
	}
	if _, _, err := ProvisionOrgAnchor(ctx, pool, "b4-anchor-three", ""); err == nil {
		t.Fatalf("empty display name admitted")
	}
	if n := b4Count(t, pool, `SELECT count(*) FROM org WHERE slug LIKE 'b4-anchor-%'`); n != 2 {
		t.Fatalf("anchor rows = %d, want exactly the two legal ones", n)
	}
}

// TestProvisionOrgAnchorRerunWritesNothing holds the update leg's value
// guard: a re-provision under an unchanged name reports not-inserted and
// leaves the row exactly as it found it, row version and updated_at
// included, while a changed name still converges. The demo populate's
// rerun (the internal/demo exit test) holds the same fact end to end; this
// is the one-place unit half of that proof.
func TestProvisionOrgAnchorRerunWritesNothing(t *testing.T) {
	pool := b4Pool(t)
	ctx := context.Background()
	slug := "b4-anchor-rerun"
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM org WHERE slug = $1`, slug); err != nil {
			t.Fatalf("clean anchor: %v", err)
		}
	})

	if _, inserted, err := ProvisionOrgAnchor(ctx, pool, slug, "B4 Anchor Rerun"); err != nil || !inserted {
		t.Fatalf("first provision: inserted=%v err=%v", inserted, err)
	}
	var rowVersion, updatedAt, name string
	if err := pool.QueryRow(ctx,
		`SELECT xmin::text, updated_at::text, name FROM org WHERE slug = $1`, slug).
		Scan(&rowVersion, &updatedAt, &name); err != nil {
		t.Fatal(err)
	}

	_, inserted, err := ProvisionOrgAnchor(ctx, pool, slug, "B4 Anchor Rerun")
	if err != nil || inserted {
		t.Fatalf("unchanged re-provision: inserted=%v err=%v", inserted, err)
	}
	var againVersion, againUpdatedAt, againName string
	if err := pool.QueryRow(ctx,
		`SELECT xmin::text, updated_at::text, name FROM org WHERE slug = $1`, slug).
		Scan(&againVersion, &againUpdatedAt, &againName); err != nil {
		t.Fatal(err)
	}
	if againVersion != rowVersion || againUpdatedAt != updatedAt || againName != name {
		t.Fatalf("the unchanged re-provision rewrote the anchor row: version %s to %s, updated_at %s to %s, name %q to %q",
			rowVersion, againVersion, updatedAt, againUpdatedAt, name, againName)
	}

	if _, inserted, err := ProvisionOrgAnchor(ctx, pool, slug, "B4 Anchor Rerun Renamed"); err != nil || inserted {
		t.Fatalf("renamed re-provision: inserted=%v err=%v", inserted, err)
	}
	if n := b4Count(t, pool, `SELECT count(*) FROM org WHERE slug = $1 AND name = 'B4 Anchor Rerun Renamed'`, slug); n != 1 {
		t.Fatalf("the renamed re-provision did not converge the name: %d", n)
	}
}

// The step 0 gate fails loudly on each defect class and recovers when the
// defect is removed; the green half is the fixture run's first subtest.
func TestSeedCoverageGateLegs(t *testing.T) {
	pool := b4Pool(t)
	ctx := context.Background()
	if seed, err := VerifySeedCoverage(ctx, pool); err != nil || !seed.OK() {
		t.Fatalf("baseline gate: %+v err=%v", seed, err)
	}

	// A missing carried roles row fails the row-count check. The row is
	// re-inserted with its migration-fixed values (0015), never edited.
	if _, err := pool.Exec(ctx, `DELETE FROM roles WHERE code = 'HR'`); err != nil {
		t.Fatalf("remove roles row: %v", err)
	}
	seed, err := VerifySeedCoverage(ctx, pool)
	if !errors.Is(err, ErrSeedCoverage) || seed.CarriedRoleRows != ExpectedCarriedRoleRows-1 {
		t.Fatalf("missing roles row: seed=%+v err=%v", seed, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO roles (id, code, name, tier, scope)
		VALUES ('e7000000-0000-4000-9000-000000000003', 'HR', 'HR', 1, 'GLOBAL') ON CONFLICT (code) DO NOTHING`); err != nil {
		t.Fatalf("restore roles row: %v", err)
	}

	// An ERP-origin code with no staff_role counterpart fails both
	// coverage checks (the catalog one and the used-code one).
	if _, err := pool.Exec(ctx, `INSERT INTO roles (id, code, name, tier, scope)
		VALUES ('bb400000-0000-4000-8000-0000000000b1', 'B4ORPHAN', 'B4 Orphan Role', 1, 'GLOBAL')`); err != nil {
		t.Fatalf("plant orphan role: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, display_name, status)
		VALUES ($1, 'b4gate@fixture.test', 'B4 Gate User', 'ACTIVE')`, b4UserGate); err != nil {
		t.Fatalf("plant gate user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_roles (user_id, role_id)
		VALUES ($1, 'bb400000-0000-4000-8000-0000000000b1')`, b4UserGate); err != nil {
		t.Fatalf("plant gate user_role: %v", err)
	}
	seed, err = VerifySeedCoverage(ctx, pool)
	if !errors.Is(err, ErrSeedCoverage) || seed.RolesWithoutCounterpart != 1 || seed.UsedCodesUncovered != 1 {
		t.Fatalf("orphan code: seed=%+v err=%v", seed, err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM user_roles WHERE user_id = $1`, b4UserGate); err != nil {
		t.Fatalf("remove gate user_role: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, b4UserGate); err != nil {
		t.Fatalf("remove gate user: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM roles WHERE code = 'B4ORPHAN'`); err != nil {
		t.Fatalf("remove orphan role: %v", err)
	}

	// Tier and scope disagreements between the two catalogs fail the
	// agreement check, each direction; staff_role is the authority and the
	// carried roles row is what moved.
	if _, err := pool.Exec(ctx, `UPDATE roles SET tier = 2 WHERE code = 'HR'`); err != nil {
		t.Fatalf("move tier: %v", err)
	}
	seed, err = VerifySeedCoverage(ctx, pool)
	if !errors.Is(err, ErrSeedCoverage) || seed.TierScopeMismatches != 1 {
		t.Fatalf("tier mismatch: seed=%+v err=%v", seed, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE roles SET tier = 1 WHERE code = 'HR'`); err != nil {
		t.Fatalf("restore tier: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE roles SET scope = 'BRANCH' WHERE code = 'HR'`); err != nil {
		t.Fatalf("move scope: %v", err)
	}
	seed, err = VerifySeedCoverage(ctx, pool)
	if !errors.Is(err, ErrSeedCoverage) || seed.TierScopeMismatches != 1 {
		t.Fatalf("scope mismatch: seed=%+v err=%v", seed, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE roles SET scope = 'GLOBAL' WHERE code = 'HR'`); err != nil {
		t.Fatalf("restore scope: %v", err)
	}
	if seed, err := VerifySeedCoverage(ctx, pool); err != nil || !seed.OK() {
		t.Fatalf("gate did not recover: %+v err=%v", seed, err)
	}
}

// The linkage pass, both halves of every rule: exact email matches link,
// unmatched rows are counted and left NULL, a pre-linked row is never
// overwritten, a directory error stops the pass loudly, and an empty
// subject answer is a directory defect, never a match.
func TestLinkPlatformIdentitiesLegs(t *testing.T) {
	pool := b4Pool(t)
	ctx := context.Background()
	ids := []string{
		"bb400000-0000-4000-8000-0000000001f7",
		"bb400000-0000-4000-8000-0000000001f8",
		"bb400000-0000-4000-8000-0000000001f9",
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM users WHERE id IN ($1, $2, $3)`, ids[0], ids[1], ids[2]); err != nil {
			t.Fatalf("clean link users: %v", err)
		}
	})
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatalf("plant: %v", err)
		}
	}
	exec(`INSERT INTO users (id, email, display_name, status) VALUES ($1, 'b4link01@fixture.test', 'B4 Link One', 'ACTIVE')`, ids[0])
	exec(`INSERT INTO users (id, email, display_name, status) VALUES ($1, 'b4link02@fixture.test', 'B4 Link Two', 'ACTIVE')`, ids[1])
	exec(`INSERT INTO users (id, email, display_name, status, platform_user_id) VALUES ($1, 'b4link03@fixture.test', 'B4 Link Three', 'ACTIVE', 'b4pop-preset-link')`, ids[2])

	dir := b4Directory{byEmail: map[string]string{
		"b4link01@fixture.test": "b4pop-link-1",
		// The second row has no platform counterpart; the third is
		// pre-linked and must keep its value even though the directory
		// answers a different subject.
		"b4link03@fixture.test": "b4pop-link-WRONG",
	}}
	report, err := LinkPlatformIdentities(ctx, pool, dir)
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if report.UsersLinked != 1 || report.UsersUnmatched != 1 {
		t.Fatalf("link report = %+v", report)
	}
	if n := b4Count(t, pool, `SELECT count(*) FROM users WHERE id = $1 AND platform_user_id = 'b4pop-link-1'`, ids[0]); n != 1 {
		t.Fatalf("exact match did not link: %d", n)
	}
	if n := b4Count(t, pool, `SELECT count(*) FROM users WHERE id = $1 AND platform_user_id IS NULL`, ids[1]); n != 1 {
		t.Fatalf("unmatched row was guessed a link: %d", n)
	}
	if n := b4Count(t, pool, `SELECT count(*) FROM users WHERE id = $1 AND platform_user_id = 'b4pop-preset-link'`, ids[2]); n != 1 {
		t.Fatalf("pre-linked row was overwritten: %d", n)
	}
	// Idempotent: a second run links nothing and recounts the unmatched
	// row.
	report, err = LinkPlatformIdentities(ctx, pool, dir)
	if err != nil {
		t.Fatalf("re-link: %v", err)
	}
	if report.UsersLinked != 0 || report.UsersUnmatched != 1 {
		t.Fatalf("re-link report = %+v", report)
	}

	// A directory failure stops the pass loudly rather than skipping
	// rows silently.
	if _, err := LinkPlatformIdentities(ctx, pool, b4Directory{err: errors.New("b4 fixture directory down")}); err == nil {
		t.Fatalf("directory error swallowed")
	}
	// The empty-subject defect: ok true with no subject is refused.
	exec(`UPDATE users SET platform_user_id = NULL WHERE id = $1`, ids[0])
	defect := b4Directory{byEmail: map[string]string{"b4link01@fixture.test": ""}}
	if _, err := LinkPlatformIdentities(ctx, pool, defect); err == nil {
		t.Fatalf("empty subject admitted as a link")
	}
	if n := b4Count(t, pool, `SELECT count(*) FROM users WHERE id = $1 AND platform_user_id IS NULL`, ids[0]); n != 1 {
		t.Fatalf("the defect leg wrote a link: %d", n)
	}
	// The nil-directory arm refuses with guidance, and the table guard
	// refuses an unknown table.
	if _, err := LinkPlatformIdentities(ctx, pool, nil); err == nil {
		t.Fatalf("nil directory admitted")
	}
	if _, _, err := linkOneTable(ctx, pool, dir, "audit_log"); err == nil {
		t.Fatalf("unknown table admitted")
	}
}

// The HH Pro contractor mapping (plan step 7's import half): the four
// recorded mappings resolve, estimator takes the capability-equal fallback
// with the marker set (recorded ruling R4, plan finding F1), an unknown
// value finds nothing and never defaults, and the empty value is an error.
func TestMapHHProContractorRoleLegs(t *testing.T) {
	pool := b4Pool(t)
	ctx := context.Background()
	for _, leg := range []struct {
		hhpro    string
		wantRole string
		wantFall bool
	}{
		{"owner", PortalRoleAccountAdmin, false},
		{"pm", PortalRoleBuyerNoPay, false},
		{"ap", PortalRoleAPFinance, false},
		{"field", PortalRoleFieldCrew, false},
		{"estimator", PortalRoleBuyerNoPay, true},
	} {
		mapping, found, err := MapHHProContractorRole(ctx, pool, leg.hhpro)
		if err != nil || !found {
			t.Fatalf("%s: found=%v err=%v", leg.hhpro, found, err)
		}
		if mapping.CanonicalRole != leg.wantRole || mapping.CapabilityFallback != leg.wantFall {
			t.Fatalf("%s: mapping = %+v", leg.hhpro, mapping)
		}
	}
	if _, found, err := MapHHProContractorRole(ctx, pool, "not-a-role"); err != nil || found {
		t.Fatalf("unknown hhpro role: found=%v err=%v", found, err)
	}
	if _, _, err := MapHHProContractorRole(ctx, pool, ""); err == nil {
		t.Fatalf("empty hhpro role admitted")
	}
}
