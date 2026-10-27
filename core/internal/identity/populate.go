package identity

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// This file is the B4 identity population and provisioning program (lane
// HH-3.3, Phase B): the query half of the Integrations population plan
// (tools/org/outbox/integrations/b4-population/population-plan.md, steps 0
// to 9) over the tables migrations 0213 and 0215 added or extended. Every
// step is idempotent and safe to re-run; every write touches only additive
// columns and the new tables, so the running ERP is unaffected
// (0213/0215's additive principle).
//
// Placement note for review: population is the carry act itself, so these
// functions read and write across module tables (org, users, user_roles,
// user_branch_grants, portal_users, dispatch_drivers beside the identity
// tables) by design; this is the same exception class the B3 review
// recorded for the resolvers (arbitrations A-6, A-7). The program is a
// one-shot provisioning tool, not a module repository on a request path.
//
// The S2 boundary is honored exactly as the plan states it: Phase B writes
// zero production platform links. The linkage step exists, is idempotent,
// and is proven against fixture directory values only; on a database with
// no platform directory every linked write skips cleanly and the report
// says so.

// DBTX is the least the populator needs from a database handle: run a
// statement, read rows, read one row. Both *pgxpool.Pool and pgx.Tx satisfy
// it, so a caller may run the whole program inside one transaction or step
// by step against a pool; every step is independently idempotent either
// way.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// The seeded catalog row counts step 0 gates on (migration
// core/db/migrations/0215_identity_audit.sql: the sixteen staff_role rows
// and the five hhpro_contractor_role_map rows; migrations 0015 and 0043:
// the fourteen carried roles rows). These are migration-fixed constants,
// never fixture measurements.
const (
	ExpectedStaffRoleRows   = 16
	ExpectedHHProMapRows    = 5
	ExpectedCarriedRoleRows = 14
)

// ErrSeedCoverage names a failed step 0 gate: the seeded catalog and the
// carried roles table disagree with the shape the plan is built on, so no
// population write may run. The error carries counts by class, never
// values.
var ErrSeedCoverage = errors.New("identity: seed and coverage gate failed")

// ErrOrgSlugShape names a configured org slug that fails the pinned shape
// after lowercase normalization. Provisioning validates before insert so a
// nonconforming slug can never reach the anchor table, where the middleware
// would answer 400 at the header and 503 at the anchor read (B3 review item
// 5, ADR-0020 addendum). The value itself is never echoed into the error.
var ErrOrgSlugShape = errors.New("identity: org slug does not match the pinned shape")

// orgSlugPattern is byte-identical to the pinned server-side slug shape in
// core/internal/platform/authz/middleware.go (the orgSlugPattern there,
// ADR-0020 addendum: lowercase letters, digits, dot, underscore and hyphen,
// starting lowercase alphanumeric, at most 32 characters, so org-<slug> fits a 36 character Team id). identity cannot
// import authz (authz imports identity for the verifier), so the pin is
// duplicated here; the two must move together if the ADR ever changes it.
var orgSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)

// SeedReport is the step 0 gate's outcome: counts by class, never values.
type SeedReport struct {
	// StaffRoleRows must equal ExpectedStaffRoleRows (0215 seeds 16).
	StaffRoleRows int
	// HHProMapRows must equal ExpectedHHProMapRows (0215 seeds 5).
	HHProMapRows int
	// CarriedRoleRows must equal ExpectedCarriedRoleRows (0015's thirteen
	// plus 0043's SUPER_ADMIN).
	CarriedRoleRows int
	// RolesWithoutCounterpart counts carried roles rows with no staff_role
	// origin_code counterpart; must be zero.
	RolesWithoutCounterpart int
	// UsedCodesUncovered counts user_roles rows whose roles.code has no
	// staff_role origin_code counterpart; must be zero. Tombstoned
	// user_roles rows count too: history still needs its catalog.
	UsedCodesUncovered int
	// TierScopeMismatches counts ERP-origin rows where staff_role disagrees
	// with roles on tier or scope; must be zero. Population never copies
	// tier or scope from the ERP: staff_role is the authority, this check
	// proves the two catalogs agree before any write runs.
	TierScopeMismatches int
}

// OK reports whether every gate check passed.
func (r SeedReport) OK() bool {
	return r.StaffRoleRows == ExpectedStaffRoleRows &&
		r.HHProMapRows == ExpectedHHProMapRows &&
		r.CarriedRoleRows == ExpectedCarriedRoleRows &&
		r.RolesWithoutCounterpart == 0 &&
		r.UsedCodesUncovered == 0 &&
		r.TierScopeMismatches == 0
}

// VerifySeedCoverage is plan step 0: the read-only gate that runs before
// any write and aborts the program on any mismatch. It never writes.
func VerifySeedCoverage(ctx context.Context, db DBTX) (SeedReport, error) {
	if db == nil {
		return SeedReport{}, errors.New("identity: seed coverage gate requires a database handle")
	}
	var r SeedReport
	scalars := []struct {
		dst  *int
		sql  string
		name string
	}{
		{&r.StaffRoleRows, `SELECT count(*) FROM staff_role`, "staff_role rows"},
		{&r.HHProMapRows, `SELECT count(*) FROM hhpro_contractor_role_map`, "hhpro map rows"},
		{&r.CarriedRoleRows, `SELECT count(*) FROM roles`, "carried roles rows"},
		{&r.RolesWithoutCounterpart, `
			SELECT count(*) FROM roles r
			WHERE NOT EXISTS (SELECT 1 FROM staff_role sr WHERE sr.origin_code = r.code)`, "roles without staff_role counterpart"},
		{&r.UsedCodesUncovered, `
			SELECT count(*) FROM user_roles ur
			JOIN roles r ON r.id = ur.role_id
			WHERE NOT EXISTS (SELECT 1 FROM staff_role sr WHERE sr.origin_code = r.code)`, "user_roles rows without staff_role counterpart"},
		{&r.TierScopeMismatches, `
			SELECT count(*) FROM roles r
			JOIN staff_role sr ON sr.origin_code = r.code
			WHERE sr.tier <> r.tier OR sr.scope <> lower(r.scope)`, "tier/scope mismatches"},
	}
	for _, s := range scalars {
		if err := db.QueryRow(ctx, s.sql).Scan(s.dst); err != nil {
			return SeedReport{}, fmt.Errorf("identity: seed coverage gate read failed (%s): %w", s.name, err)
		}
	}
	if !r.OK() {
		return r, fmt.Errorf("%w: staff_role %d (want %d), hhpro map %d (want %d), roles %d (want %d), uncovered roles %d, uncovered user_roles rows %d, tier/scope mismatches %d",
			ErrSeedCoverage, r.StaffRoleRows, ExpectedStaffRoleRows, r.HHProMapRows, ExpectedHHProMapRows,
			r.CarriedRoleRows, ExpectedCarriedRoleRows, r.RolesWithoutCounterpart, r.UsedCodesUncovered, r.TierScopeMismatches)
	}
	return r, nil
}

// ProvisionOrgAnchor is plan step 1: one org anchor row per configured org.
// The slug is normalized to lowercase (the resolver reads the normalized
// form and the CITEXT column would fold anyway; one spelling of one org
// exists), validated against the pinned shape before insert, and upserted
// on the slug key so re-runs converge the display name and update no id.
// Neither source repository has an org table (0213): the slug and name are
// deployment configuration, and in production the slug must name the
// platform team org-<slug> the callers hold membership in (ADR-0020 leg 3),
// a platform fact to confirm at provisioning time, not a value this code
// fixes. inserted reports whether this call created the row.
func ProvisionOrgAnchor(ctx context.Context, db DBTX, slug, name string) (orgID string, inserted bool, err error) {
	if db == nil {
		return "", false, errors.New("identity: org anchor provisioning requires a database handle")
	}
	normalized := strings.ToLower(slug)
	if !orgSlugPattern.MatchString(normalized) {
		return "", false, ErrOrgSlugShape
	}
	if name == "" {
		return "", false, errors.New("identity: org anchor provisioning requires a display name")
	}
	// xmax is zero exactly on the insert path of the upsert, which is how
	// the report distinguishes created from converged without a second
	// read. The update leg is guarded on the value it would change, so a
	// re-run against an anchor it already provisioned under the same name
	// touches nothing: a one-shot that is re-run must leave the row it
	// finds as it found it, updated_at included, or the row version moves
	// under every idempotent caller that holds one. A quelled update
	// returns no row, and that is the converged case, not a failure: the
	// anchor's id is read back and reported as not-inserted.
	err = db.QueryRow(ctx, `
		INSERT INTO org (slug, name) VALUES ($1, $2)
		ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name, updated_at = now()
			WHERE org.name IS DISTINCT FROM EXCLUDED.name
		RETURNING id::text, (xmax = 0)`, normalized, name).Scan(&orgID, &inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := db.QueryRow(ctx,
			`SELECT id::text FROM org WHERE slug = $1`, normalized).Scan(&orgID); err != nil {
			return "", false, fmt.Errorf("identity: org anchor read-back failed: %w", err)
		}
		return orgID, false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("identity: org anchor upsert failed: %w", err)
	}
	return orgID, inserted, nil
}

// StampReport counts the additive backfills (plan steps 2 and 3) by table:
// rows this run stamped. A re-run stamps zero rows because every statement
// guards on the column being NULL.
type StampReport struct {
	UsersStamped            int64
	UserRolesStamped        int64
	UserBranchGrantsStamped int64
	PortalTenantsStamped    int64
	// LocationsStamped counts the location rows (the branches the migrations plant and their zones)
	// that carried no org and took the anchor. Every stock, branch and reach read holds to the caller's
	// org through the location's org_id (UX-88), so a box whose branches carry none answers empty.
	LocationsStamped int64
}

// StampOrgScope is plan step 2: org_id backfill on the three carried staff
// tables and on the locations (the branches and their zones). The ERP is single-org (0213), so every carried row takes the one
// anchor. user_branch_grants.org_id is read-path load-bearing (the
// resolver's branchGrants predicates on it); users.org_id and
// user_roles.org_id are sync-scope consistency stamping, not read-path
// load-bearing, so they may trail if the program is ever partial. Tombstoned
// rows are stamped too: the statement is the plan's exact rule and a
// tombstone is a sync fact, not a reason to skip.
func StampOrgScope(ctx context.Context, db DBTX, orgID string) (StampReport, error) {
	if db == nil {
		return StampReport{}, errors.New("identity: org stamping requires a database handle")
	}
	if orgID == "" {
		return StampReport{}, errors.New("identity: org stamping requires the anchor org id")
	}
	var r StampReport
	steps := []struct {
		dst *int64
		sql string
	}{
		{&r.UsersStamped, `UPDATE users SET org_id = $1 WHERE org_id IS NULL`},
		{&r.UserRolesStamped, `UPDATE user_roles SET org_id = $1 WHERE org_id IS NULL`},
		{&r.UserBranchGrantsStamped, `UPDATE user_branch_grants SET org_id = $1 WHERE org_id IS NULL`},
		{&r.LocationsStamped, `UPDATE locations SET org_id = $1, updated_at = now() WHERE org_id IS NULL`},
	}
	for _, s := range steps {
		tag, err := db.Exec(ctx, s.sql, orgID)
		if err != nil {
			return r, fmt.Errorf("identity: org stamping failed: %w", err)
		}
		*s.dst = tag.RowsAffected()
	}
	return r, nil
}

// StampPortalTenants is plan step 3: portal_users.tenant_id backfill from
// the row's own customer_id. The customer is the contractor account, the
// tenant claim carries the contractor account UUID (ADR-0020 addendum), and
// the portal resolver predicates tenant_id = token tenant, so tenant_id
// takes the customer_id value (plan finding F4, recorded ruling R3 in the
// B4 backend deliverable).
func StampPortalTenants(ctx context.Context, db DBTX) (int64, error) {
	if db == nil {
		return 0, errors.New("identity: tenant stamping requires a database handle")
	}
	tag, err := db.Exec(ctx, `UPDATE portal_users SET tenant_id = customer_id WHERE tenant_id IS NULL`)
	if err != nil {
		return 0, fmt.Errorf("identity: tenant stamping failed: %w", err)
	}
	return tag.RowsAffected(), nil
}

// PlatformDirectory is the S2-era source of platform users (real Appwrite
// users once S2 lands; a fixture double in Phase B tests). The match key is
// email, CITEXT and unique on both users.email and portal_users.email. ok
// false is no exact match: the row is counted and left unlinked, never
// guessed (C31 fail closed). Implementations must return value-free
// errors: never the queried email, never a platform user record, because
// LinkPlatformIdentities wraps the directory error into its own chain and
// the S2-era implementation is not yet written to know that.
type PlatformDirectory interface {
	PlatformUserByEmail(ctx context.Context, email string) (platformUserID string, ok bool, err error)
}

// LinkReport counts the linkage pass (plan step 4) by class: rows this run
// linked, and rows with no exact platform match, which stay unlinked and
// are never guessed.
type LinkReport struct {
	UsersLinked     int64
	UsersUnmatched  int64
	PortalLinked    int64
	PortalUnmatched int64
}

// LinkPlatformIdentities is plan step 4: users.platform_user_id and
// portal_users.platform_user_id set from the platform directory by exact
// email match. It never overwrites a non-NULL value (the WHERE guard) and
// the partial unique indexes (0215) enforce one-to-one in both directions:
// a directory answer that collides surfaces as an error, loudly. Phase B
// populates nothing through this step in production (real platform user ids
// do not exist until S2); the path is proven on fixture databases.
func LinkPlatformIdentities(ctx context.Context, db DBTX, dir PlatformDirectory) (LinkReport, error) {
	if db == nil {
		return LinkReport{}, errors.New("identity: platform linkage requires a database handle")
	}
	if dir == nil {
		return LinkReport{}, errors.New("identity: platform linkage requires a directory (nil means the pre-S2 shape, run Populate without one instead)")
	}
	var r LinkReport
	linked, unmatched, err := linkOneTable(ctx, db, dir, "users")
	if err != nil {
		return r, err
	}
	r.UsersLinked, r.UsersUnmatched = linked, unmatched
	linked, unmatched, err = linkOneTable(ctx, db, dir, "portal_users")
	if err != nil {
		return r, err
	}
	r.PortalLinked, r.PortalUnmatched = linked, unmatched
	return r, nil
}

// linkOneTable links one carried identity table. Tombstoned rows are not
// candidates (C35). A directory answer of the empty string with ok true is
// a directory defect, not a match. An update that lands on zero rows means
// the row changed under the pass; it is counted as unmatched (conservative:
// no false link claims) rather than erroring a re-runnable program.
func linkOneTable(ctx context.Context, db DBTX, dir PlatformDirectory, table string) (linked, unmatched int64, err error) {
	var rows pgx.Rows
	switch table {
	case "users":
		rows, err = db.Query(ctx, `SELECT id::text, email::text FROM users WHERE platform_user_id IS NULL AND deleted_at IS NULL ORDER BY id`)
	case "portal_users":
		rows, err = db.Query(ctx, `SELECT id::text, email::text FROM portal_users WHERE platform_user_id IS NULL AND deleted_at IS NULL ORDER BY id`)
	default:
		return 0, 0, fmt.Errorf("identity: linkage refuses unknown table %q", table)
	}
	if err != nil {
		return 0, 0, fmt.Errorf("identity: linkage read failed on %s: %w", table, err)
	}
	defer rows.Close()
	type candidate struct{ id, email string }
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.email); err != nil {
			return 0, 0, fmt.Errorf("identity: linkage read failed on %s: %w", table, err)
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return 0, 0, fmt.Errorf("identity: linkage read failed on %s: %w", table, err)
	}
	for _, c := range candidates {
		subject, ok, err := dir.PlatformUserByEmail(ctx, c.email)
		if err != nil {
			return linked, unmatched, fmt.Errorf("identity: platform directory read failed: %w", err)
		}
		if !ok {
			unmatched++
			continue
		}
		if subject == "" {
			return linked, unmatched, errors.New("identity: platform directory answered an empty subject")
		}
		tag, err := db.Exec(ctx, fmt.Sprintf(
			`UPDATE %s SET platform_user_id = $1 WHERE id = $2 AND platform_user_id IS NULL AND deleted_at IS NULL`, table),
			subject, c.id)
		if err != nil {
			return linked, unmatched, fmt.Errorf("identity: linkage write failed on %s: %w", table, err)
		}
		if tag.RowsAffected() == 1 {
			linked++
		} else {
			unmatched++
		}
	}
	return linked, unmatched, nil
}

// grantCandidate is one derived staff grant ahead of the insert boundary.
type grantCandidate struct {
	platformUserID string
	roleID         string
	scope          string
	branchID       *string
}

// validateGrantShape is the 0215 header's branch-id rule as a predicate:
// a branch-scoped role's grant requires a non-NULL branch_id, a global
// role's grant carries NULL, and any other scope value is unknown. Anything
// else is ErrGrantShape, the data-defect family the guard answers with the
// 503 family (pin P-1); population refuses such a row at the insert
// boundary, counted and never written.
func validateGrantShape(scope string, branchID *string) error {
	switch {
	case scope == ScopeGlobal && branchID == nil:
		return nil
	case scope == ScopeBranch && branchID != nil:
		return nil
	case scope == ScopeBranch:
		return fmt.Errorf("%w: branch-scoped grant derived with a NULL branch_id", ErrGrantShape)
	case scope == ScopeGlobal:
		return fmt.Errorf("%w: global grant derived with a branch_id", ErrGrantShape)
	default:
		return fmt.Errorf("%w: grant derived with the unknown scope %q", ErrGrantShape, scope)
	}
}

// insertGrant writes one candidate after re-validating the branch-id rule
// at the insert boundary (defense in depth: the pipeline validates, and the
// boundary validates again so no caller can bypass the rule). Idempotent
// through the partial unique index staff_role_grant_unique (0215): a bare
// ON CONFLICT DO NOTHING makes a re-run a no-op, reported as inserted
// false. granted_by and seq stay NULL on carried rows (0213: a backfill
// order is not authoritative; population is a carry act, not an
// administrative grant).
func insertGrant(ctx context.Context, db DBTX, orgID string, c grantCandidate) (inserted bool, err error) {
	if err := validateGrantShape(c.scope, c.branchID); err != nil {
		return false, err
	}
	tag, err := db.Exec(ctx, `
		INSERT INTO staff_role_grant (platform_user_id, role_id, org_id, branch_id, granted_by, seq)
		VALUES ($1, $2, $3, $4, NULL, NULL)
		ON CONFLICT DO NOTHING`, c.platformUserID, c.roleID, orgID, c.branchID)
	if err != nil {
		return false, fmt.Errorf("identity: staff grant write failed: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// GrantReport counts plan step 5 by class. SkippedBranchless is plan
// finding F3: a branch-scope role holder with zero live branch grants gets
// no grant (the stricter-than-ERP reading; granting every branch would be a
// fail-open invention, C31). Violations counts candidates that broke the
// branch-id rule at the insert boundary; the derivation never produces one
// (the only reachable shape defect is the branchless class, which the plan
// counts as a skip), so a nonzero value names a catalog or derivation
// defect and the step returns it as an error after counting.
type GrantReport struct {
	Inserted          int64
	Existing          int64
	SkippedNoLinkage  int64
	SkippedBranchless int64
	Violations        int64
}

// PopulateStaffRoleGrants is plan step 5: staff_role_grant derived from
// user_roles joined roles joined users, with role_id from staff_role via
// the origin_code join (the join never misses once step 0 passed; run the
// gate first). Tier and scope are never copied onto the grant: they are
// read from staff_role at resolve time. A user_roles row whose user has no
// platform linkage is skipped and counted (pre-S2 every row skips; the
// class collapses to zero once step 4 links). A branch-scope role takes the
// holder's deterministic primary branch (the lowest live branch id); global
// roles carry NULL by construction. Tombstoned user_roles and users rows
// are never candidates. Rows are carried regardless of users.status: the
// read path gates a linked non-ACTIVE row with the 401 family at resolve
// time (ErrStaffUserInactive), which is the ERP's own per-request
// re-resolution behavior (recorded ruling R2).
func PopulateStaffRoleGrants(ctx context.Context, db DBTX, orgID string) (GrantReport, error) {
	if db == nil {
		return GrantReport{}, errors.New("identity: staff grant population requires a database handle")
	}
	if orgID == "" {
		return GrantReport{}, errors.New("identity: staff grant population requires the anchor org id")
	}
	rows, err := db.Query(ctx, `
		SELECT u.platform_user_id, sr.id, sr.scope,
		       (SELECT ubg.branch_id::text FROM user_branch_grants ubg
		         WHERE ubg.user_id = ur.user_id AND ubg.deleted_at IS NULL
		         ORDER BY ubg.branch_id LIMIT 1) AS primary_branch
		FROM user_roles ur
		JOIN roles r ON r.id = ur.role_id
		JOIN users u ON u.id = ur.user_id
		JOIN staff_role sr ON sr.origin_code = r.code
		WHERE ur.deleted_at IS NULL
		  AND u.deleted_at IS NULL
		  AND sr.deleted_at IS NULL
		ORDER BY u.id, sr.id`)
	if err != nil {
		return GrantReport{}, fmt.Errorf("identity: staff grant derivation read failed: %w", err)
	}
	defer rows.Close()
	var candidates []grantCandidate
	for rows.Next() {
		var (
			platformUserID *string
			roleID, scope  string
			primaryBranch  *string
		)
		if err := rows.Scan(&platformUserID, &roleID, &scope, &primaryBranch); err != nil {
			return GrantReport{}, fmt.Errorf("identity: staff grant derivation read failed: %w", err)
		}
		if platformUserID == nil {
			// Counted here rather than after the scan loop so the class is
			// visible even on the pre-S2 shape where every row is in it.
			candidates = append(candidates, grantCandidate{scope: scope})
			continue
		}
		c := grantCandidate{platformUserID: *platformUserID, roleID: roleID, scope: scope}
		if scope == ScopeBranch {
			c.branchID = primaryBranch
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return GrantReport{}, fmt.Errorf("identity: staff grant derivation read failed: %w", err)
	}
	var report GrantReport
	for _, c := range candidates {
		switch {
		case c.platformUserID == "":
			report.SkippedNoLinkage++
			continue
		case c.scope == ScopeBranch && c.branchID == nil:
			// Plan finding F3: no live branch grant means no grant at all;
			// the 0215 rule refuses a NULL branch_id and inventing branches
			// would fail open. Stricter than the ERP, deliberately.
			report.SkippedBranchless++
			continue
		}
		inserted, err := insertGrant(ctx, db, orgID, c)
		if errors.Is(err, ErrGrantShape) {
			report.Violations++
			continue
		}
		if err != nil {
			return report, err
		}
		if inserted {
			report.Inserted++
		} else {
			report.Existing++
		}
	}
	if report.Violations > 0 {
		return report, fmt.Errorf("%w: %d staff grant candidate(s) violated the branch-id rule and were refused before insert", ErrGrantShape, report.Violations)
	}
	return report, nil
}

// DriverReport counts plan step 8 by class. SkippedNoIdentity is plan
// finding F5 plus its edge cases: registry rows with NULL user_id (casual
// and contract drivers who never hold a login, 0090), rows whose users row
// is tombstoned (C35), and rows whose user has no platform linkage yet. All
// three stay dispatch registry rows and never gain a driver_profile.
type DriverReport struct {
	Inserted          int64
	Existing          int64
	SkippedNoIdentity int64
}

// PopulateDriverProfiles is plan step 8: driver_profile from
// dispatch_drivers joined users on user_id. Driver is a role (the step 5
// grant on staff_role 'driver') plus this row, not a separate identity
// (D3). licence_class and is_active carry from the registry row;
// truck_id, licence_number and licence_expiry stay NULL because no ERP fact
// sources them: the registry binds trucks to runs, not drivers, and licence
// renewals are deferred to a fleet module (0090's header; plan finding F2).
// Sourcing them is a schema question for HH-3.7, never this lane. The
// partial unique index driver_profile_user_org_key (0215) makes re-runs
// no-ops.
func PopulateDriverProfiles(ctx context.Context, db DBTX, orgID string) (DriverReport, error) {
	if db == nil {
		return DriverReport{}, errors.New("identity: driver profile population requires a database handle")
	}
	if orgID == "" {
		return DriverReport{}, errors.New("identity: driver profile population requires the anchor org id")
	}
	rows, err := db.Query(ctx, `
		SELECT u.platform_user_id, dd.licence_class, dd.is_active
		FROM dispatch_drivers dd
		LEFT JOIN users u ON u.id = dd.user_id AND u.deleted_at IS NULL
		WHERE dd.deleted_at IS NULL
		ORDER BY dd.id`)
	if err != nil {
		return DriverReport{}, fmt.Errorf("identity: driver derivation read failed: %w", err)
	}
	defer rows.Close()
	type driverRow struct {
		platformUserID *string
		licenceClass   string
		isActive       bool
	}
	var drivers []driverRow
	for rows.Next() {
		var d driverRow
		if err := rows.Scan(&d.platformUserID, &d.licenceClass, &d.isActive); err != nil {
			return DriverReport{}, fmt.Errorf("identity: driver derivation read failed: %w", err)
		}
		drivers = append(drivers, d)
	}
	if err := rows.Err(); err != nil {
		return DriverReport{}, fmt.Errorf("identity: driver derivation read failed: %w", err)
	}
	var report DriverReport
	for _, d := range drivers {
		if d.platformUserID == nil {
			report.SkippedNoIdentity++
			continue
		}
		tag, err := db.Exec(ctx, `
			INSERT INTO driver_profile (platform_user_id, org_id, truck_id, licence_number, licence_class, licence_expiry, is_active, seq)
			VALUES ($1, $2, NULL, NULL, $3, NULL, $4, NULL)
			ON CONFLICT DO NOTHING`, *d.platformUserID, orgID, d.licenceClass, d.isActive)
		if err != nil {
			return report, fmt.Errorf("identity: driver profile write failed: %w", err)
		}
		if tag.RowsAffected() == 1 {
			report.Inserted++
		} else {
			report.Existing++
		}
	}
	return report, nil
}

// HHProRoleMapping is the outcome of mapping one HH Pro contractor role
// onto the canonical set (plan step 7's import half, D3, 0215's seeded
// hhpro_contractor_role_map). CapabilityFallback marks the values with no
// canonical equivalent (0215 seeds canonical_role NULL for estimator): the
// person reconciles to a capability (D3: any extras become capabilities)
// and enters under the capability-equal canonical role, with the
// distinction carried as capability data rather than a role.
type HHProRoleMapping struct {
	CanonicalRole      string
	CapabilityFallback bool
}

// MapHHProContractorRole reads the seeded one-time mapping for the S2/HH-5
// era HH Pro import; on ERP-origin data it never fires (those rows already
// carry canonical roles and no statement runs for them). An unknown HH Pro
// value answers found false, never a default (C31). The estimator arm
// answers buyer_no_pay, the capability-equal canonical role (its set
// contains submit_rfq, the capability the estimator job reconciles to):
// this is recorded ruling R4 in the B4 backend deliverable, flagged as plan
// finding F1 for the Tech Lead, because no decision names the capability
// and portal_users.role is NOT NULL inside the canonical CHECK, so a
// role-less row cannot exist.
func MapHHProContractorRole(ctx context.Context, db DBTX, hhproRole string) (mapping HHProRoleMapping, found bool, err error) {
	if db == nil {
		return HHProRoleMapping{}, false, errors.New("identity: hhpro role mapping requires a database handle")
	}
	if hhproRole == "" {
		return HHProRoleMapping{}, false, errors.New("identity: hhpro role mapping requires a role value")
	}
	var canonical *string
	err = db.QueryRow(ctx, `
		SELECT canonical_role FROM hhpro_contractor_role_map WHERE hhpro_role = $1`,
		hhproRole).Scan(&canonical)
	if errors.Is(err, pgx.ErrNoRows) {
		return HHProRoleMapping{}, false, nil
	}
	if err != nil {
		return HHProRoleMapping{}, false, fmt.Errorf("identity: hhpro role map read failed: %w", err)
	}
	if canonical == nil {
		return HHProRoleMapping{CanonicalRole: PortalRoleBuyerNoPay, CapabilityFallback: true}, true, nil
	}
	return HHProRoleMapping{CanonicalRole: *canonical}, true, nil
}

// PopulateConfig is the one Populate entrypoint's input. The org slug and
// name are deployment configuration (plan step 1). Directory nil is the
// pre-S2 shape: the linkage step is skipped, and steps 5 and 8 then skip
// every row as no-linkage, which is exactly the plan's pre-S2 end state of
// zero writes with the skip classes counted.
type PopulateConfig struct {
	OrgSlug   string
	OrgName   string
	Directory PlatformDirectory
	// OrgTables are the other modules' org stamps, run right after StampOrgScope (SF-2p2). The identity module never
	// names their tables: each entry is its owning module's one line seam.
	OrgTables []OrgStampEntry
}

// OrgStampEntry is one table's org stamp: its name for the report, and the owning module's seam that stamps every NULL
// org_id from the row's branch location and adds the table's NOT NULL check, by the one SQL rule (org_stamp_and_check,
// 0432's body parameterised). Stamp answers the rows it stamped, refuses in 0432's words when a row has no branch or sits
// at a location with no org, and is idempotent. Unstamped, when set, counts the rows still carrying no org (the
// verification pass). A later table (orders, tasks) is one more entry wired in the composition root, never a second hook.
type OrgStampEntry struct {
	Name      string
	Stamp     func(ctx context.Context, db DBTX) (int64, error)
	Unstamped func(ctx context.Context, db DBTX) (int64, error)
}

// StampOrgTables is the one named hook (SF-2p2): it runs each entry in order, after the locations carry their org, and
// answers the rows each stamped by name. A refusal stops the run at that entry and names it; the caller's transaction
// rolls the run back, so nothing is half written.
func StampOrgTables(ctx context.Context, db DBTX, entries []OrgStampEntry) (map[string]int64, error) {
	out := map[string]int64{}
	for _, e := range entries {
		if e.Stamp == nil {
			return out, fmt.Errorf("identity: org stamp entry %q has no stamp", e.Name)
		}
		n, err := e.Stamp(ctx, db)
		if err != nil {
			return out, fmt.Errorf("identity: org stamp of %s: %w", e.Name, err)
		}
		out[e.Name] = n
	}
	return out, nil
}

// UnstampedOrgTables is the verification half: the rows each entry still holds with no org, by name (zero after a run).
func UnstampedOrgTables(ctx context.Context, db DBTX, entries []OrgStampEntry) (map[string]int64, error) {
	out := map[string]int64{}
	for _, e := range entries {
		if e.Unstamped == nil {
			continue
		}
		n, err := e.Unstamped(ctx, db)
		if err != nil {
			return out, fmt.Errorf("identity: org verification of %s: %w", e.Name, err)
		}
		out[e.Name] = n
	}
	return out, nil
}

// PopulateReport is the program's outcome: every step's counts by class,
// never a value.
type PopulateReport struct {
	Seed        SeedReport
	OrgID       string
	OrgInserted bool
	Stamp       StampReport
	// OrgTables is the rows each OrgTables entry stamped, by name (a separate field: StampReport stays comparable).
	OrgTables map[string]int64
	Link      LinkReport
	Grants    GrantReport
	Drivers   DriverReport
}

// Populate runs the plan in order: step 0's read-only gate first (a
// mismatch aborts before any write), then the org anchor, the two additive
// backfills, the linkage when a directory is wired, and the two
// S2-dependent derivations. Step 6 is the carry statement (user branch
// grants change only by step 2's stamping, no row writes) and step 7
// changes nothing on ERP-origin data, so neither has a statement here; the
// import half of step 7 is MapHHProContractorRole, and step 9 names what
// population never touches (audit attribution, seq, portal_invites). A
// partial run is safe: every step is independently idempotent, so a re-run
// converges.
func Populate(ctx context.Context, db DBTX, cfg PopulateConfig) (PopulateReport, error) {
	var report PopulateReport
	seed, err := VerifySeedCoverage(ctx, db)
	report.Seed = seed
	if err != nil {
		return report, err
	}
	orgID, inserted, err := ProvisionOrgAnchor(ctx, db, cfg.OrgSlug, cfg.OrgName)
	if err != nil {
		return report, err
	}
	report.OrgID, report.OrgInserted = orgID, inserted
	stamp, err := StampOrgScope(ctx, db, orgID)
	report.Stamp = stamp
	if err != nil {
		return report, err
	}
	orgTables, err := StampOrgTables(ctx, db, cfg.OrgTables)
	report.OrgTables = orgTables
	if err != nil {
		return report, err
	}
	tenants, err := StampPortalTenants(ctx, db)
	report.Stamp.PortalTenantsStamped = tenants
	if err != nil {
		return report, err
	}
	if cfg.Directory != nil {
		link, err := LinkPlatformIdentities(ctx, db, cfg.Directory)
		report.Link = link
		if err != nil {
			return report, err
		}
	}
	grants, err := PopulateStaffRoleGrants(ctx, db, orgID)
	report.Grants = grants
	if err != nil {
		return report, err
	}
	drivers, err := PopulateDriverProfiles(ctx, db, orgID)
	report.Drivers = drivers
	if err != nil {
		return report, err
	}
	return report, nil
}

// PopulationCounts is the verification pass (the plan's verification
// section, counts only, never values): the target side of the program after
// a run. The expected fixture state is asserted by the tests; on the
// production database before S2 the linked classes are zero and the
// fail-closed behaviors of plan step 4 hold.
type PopulationCounts struct {
	// OrgRows is the anchor count for this org id: 1 after step 1.
	OrgRows int
	// The four unstamped classes must be zero after steps 2 and 3. They
	// count carried rows whose additive scope column is still NULL,
	// tombstoned rows included (a tombstone is a sync fact, the row is
	// still carried).
	UsersUnstampedOrg            int
	UserRolesUnstampedOrg        int
	UserBranchGrantsUnstampedOrg int
	PortalUsersUnstampedTenant   int
	// Linkage completeness: live rows carrying a platform subject versus
	// live rows still unlinked. Pre-S2 the unlinked side is the whole
	// table and that is correct.
	UsersLinked    int
	UsersUnlinked  int
	PortalLinked   int
	PortalUnlinked int
	// The grant table, scoped to this org: total live grants, then the two
	// branch-id classes by staff_role scope. Global plus branch must equal
	// the total.
	StaffGrants       int
	StaffGrantsGlobal int
	StaffGrantsBranch int
	// StaffGrantShapeViolations counts live grant rows in this org that
	// break the 0215 branch-id rule (branch scope with NULL branch_id,
	// global scope with a branch_id, an unknown scope). Expected zero
	// always: population refuses such rows before insert, and the guard
	// answers any that exist with the 503 family (pin P-1), so a nonzero
	// count here is a provisioning defect made visible before it becomes
	// an outage.
	StaffGrantShapeViolations int
	// StaffGrantsUnlinked counts live grant rows in this org whose
	// platform subject has no live users row. Population never writes a
	// grant except through a linked users row (recorded ruling R1, plan
	// finding F6), so a nonzero count names hand-minted rows or a
	// tombstoned-after-link users row.
	StaffGrantsUnlinked int
	// DriverProfiles and DriverProfilesInactive count the live profile
	// rows in this org; DispatchDriversNoIdentity is the F5 class
	// (registry rows with NULL user_id, never profiled by design).
	DriverProfiles            int
	DriverProfilesInactive    int
	DispatchDriversNoIdentity int
	// PortalRolesNonCanonical counts live portal_users rows outside the
	// canonical five (0160's CHECK set). Expected zero: ERP-origin rows are
	// already canonical and the CHECK refuses anything else.
	PortalRolesNonCanonical int
}

// VerifyPopulation is the read-only verification pass over the populated
// tables: counts by class, branch-id violation count, unlinked grant count,
// and linkage completeness, scoped to the one org anchor where the table
// carries org_id. It writes nothing and is safe to run at any point of the
// program, including before it.
func VerifyPopulation(ctx context.Context, db DBTX, orgID string) (PopulationCounts, error) {
	if db == nil {
		return PopulationCounts{}, errors.New("identity: verification pass requires a database handle")
	}
	if orgID == "" {
		return PopulationCounts{}, errors.New("identity: verification pass requires the anchor org id")
	}
	var c PopulationCounts
	scalars := []struct {
		dst  *int
		sql  string
		args []any
		name string
	}{
		{&c.OrgRows, `SELECT count(*) FROM org WHERE id = $1`, []any{orgID}, "org anchor rows"},
		{&c.UsersUnstampedOrg, `SELECT count(*) FROM users WHERE org_id IS NULL`, nil, "users unstamped"},
		{&c.UserRolesUnstampedOrg, `SELECT count(*) FROM user_roles WHERE org_id IS NULL`, nil, "user_roles unstamped"},
		{&c.UserBranchGrantsUnstampedOrg, `SELECT count(*) FROM user_branch_grants WHERE org_id IS NULL`, nil, "user_branch_grants unstamped"},
		{&c.PortalUsersUnstampedTenant, `SELECT count(*) FROM portal_users WHERE tenant_id IS NULL`, nil, "portal_users unstamped"},
		{&c.UsersLinked, `SELECT count(*) FROM users WHERE platform_user_id IS NOT NULL AND deleted_at IS NULL`, nil, "users linked"},
		{&c.UsersUnlinked, `SELECT count(*) FROM users WHERE platform_user_id IS NULL AND deleted_at IS NULL`, nil, "users unlinked"},
		{&c.PortalLinked, `SELECT count(*) FROM portal_users WHERE platform_user_id IS NOT NULL AND deleted_at IS NULL`, nil, "portal users linked"},
		{&c.PortalUnlinked, `SELECT count(*) FROM portal_users WHERE platform_user_id IS NULL AND deleted_at IS NULL`, nil, "portal users unlinked"},
		{&c.StaffGrants, `SELECT count(*) FROM staff_role_grant WHERE org_id = $1 AND deleted_at IS NULL`, []any{orgID}, "staff grants"},
		{&c.StaffGrantsGlobal, `
			SELECT count(*) FROM staff_role_grant g JOIN staff_role sr ON sr.id = g.role_id
			WHERE g.org_id = $1 AND g.deleted_at IS NULL AND sr.scope = 'global'`, []any{orgID}, "global grants"},
		{&c.StaffGrantsBranch, `
			SELECT count(*) FROM staff_role_grant g JOIN staff_role sr ON sr.id = g.role_id
			WHERE g.org_id = $1 AND g.deleted_at IS NULL AND sr.scope = 'branch'`, []any{orgID}, "branch grants"},
		{&c.StaffGrantShapeViolations, `
			SELECT count(*) FROM staff_role_grant g JOIN staff_role sr ON sr.id = g.role_id
			WHERE g.org_id = $1 AND g.deleted_at IS NULL AND (
				(sr.scope = 'branch' AND g.branch_id IS NULL) OR
				(sr.scope = 'global' AND g.branch_id IS NOT NULL) OR
				(sr.scope NOT IN ('global', 'branch')))`, []any{orgID}, "branch-id rule violations"},
		{&c.StaffGrantsUnlinked, `
			SELECT count(*) FROM staff_role_grant g
			WHERE g.org_id = $1 AND g.deleted_at IS NULL
			  AND NOT EXISTS (SELECT 1 FROM users u WHERE u.platform_user_id = g.platform_user_id AND u.deleted_at IS NULL)`,
			[]any{orgID}, "grants without a linked users row"},
		{&c.DriverProfiles, `SELECT count(*) FROM driver_profile WHERE org_id = $1 AND deleted_at IS NULL`, []any{orgID}, "driver profiles"},
		{&c.DriverProfilesInactive, `SELECT count(*) FROM driver_profile WHERE org_id = $1 AND deleted_at IS NULL AND NOT is_active`, []any{orgID}, "inactive driver profiles"},
		{&c.DispatchDriversNoIdentity, `SELECT count(*) FROM dispatch_drivers WHERE user_id IS NULL AND deleted_at IS NULL`, nil, "registry rows with no user"},
		{&c.PortalRolesNonCanonical, `
			SELECT count(*) FROM portal_users
			WHERE deleted_at IS NULL AND role NOT IN ('account_admin', 'buyer', 'buyer_no_pay', 'ap_finance', 'field_crew')`,
			nil, "portal rows outside the canonical set"},
	}
	for _, s := range scalars {
		if err := db.QueryRow(ctx, s.sql, s.args...).Scan(s.dst); err != nil {
			return PopulationCounts{}, fmt.Errorf("identity: verification read failed (%s): %w", s.name, err)
		}
	}
	return c, nil
}

// PortalUserSeed is one contractor portal user written by a provisioning
// program under a caller chosen id, already linked to its platform identity
// and stamped with its tenant (the contractor account, which is the
// customer). The row carries no usable password: the core has no password
// route and the sign in is the platform's.
type PortalUserSeed struct {
	ID             string
	CustomerID     string
	Email          string
	Name           string
	Role           string
	PlatformUserID string
}

// portalNoPassword is the marker stored in password_hash for a seeded user:
// not a hash of anything, so no password verifies against it.
const portalNoPassword = "seeded-no-password"

// ProvisionPortalUsers writes the contractor portal users once: a row
// already holding an id is left as it is, so a re-run counts zero. A role
// outside the canonical set (PortalRoleAccountAdmin and its four siblings)
// is refused before any row is written, the canonical set being the
// portal_users CHECK of migration 0160. Rows land active, tenant_id equal
// to customer_id, platform_user_id set (the 0215 linkage), so the portal
// role resolver finds them with no further population step.
func ProvisionPortalUsers(ctx context.Context, db DBTX, users []PortalUserSeed) (created int, err error) {
	if db == nil {
		return 0, errors.New("identity: portal provisioning requires a database handle")
	}
	for _, u := range users {
		if u.ID == "" || u.CustomerID == "" || u.Email == "" || u.PlatformUserID == "" {
			return created, errors.New("identity: a portal user seed needs an id, a customer, an email and a platform user id")
		}
		switch u.Role {
		case PortalRoleAccountAdmin, PortalRoleBuyer, PortalRoleBuyerNoPay, PortalRoleAPFinance, PortalRoleFieldCrew:
		default:
			return created, fmt.Errorf("identity: portal user seed role %q is outside the canonical set", u.Role)
		}
	}
	for _, u := range users {
		tag, err := db.Exec(ctx, `
			INSERT INTO portal_users (id, customer_id, email, name, password_hash, role, status, platform_user_id, tenant_id)
			VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $2::uuid)
			ON CONFLICT (id) DO NOTHING`,
			u.ID, u.CustomerID, u.Email, u.Name, portalNoPassword, u.Role, PortalStatusActive, u.PlatformUserID)
		if err != nil {
			return created, fmt.Errorf("identity: portal user write failed: %w", err)
		}
		created += int(tag.RowsAffected())
	}
	return created, nil
}
