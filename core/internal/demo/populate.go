//go:build demo

// Package demo holds the demo build's own code, every file under the demo
// tag so the production build compiles none of it (the plan's G1). This
// file is the demo populate (W3): the one-shot `core demo-populate` that
// turns a freshly migrated and seeded database into the demo box's world.
//
// The populate runs fourth in the entrypoint's order, after `migrate`,
// `demo-check` and `demoseed -apply`, and before `serve`. It is the only
// writer of the demo_box row the demo boot guard reads, and it refuses
// before any write unless the database is already a demo database in
// shape: one org (hh-demo, which this populate itself provisions), no user
// outside the migrations' planted set and the role accounts this file
// defines, and no payment configuration but this file's own.
//
// IDEMPOTENCE, the demoseed_apply discipline: every insert carries an
// explicit deterministic id and ends in ON CONFLICT DO NOTHING, every
// update guards on the value it would change, and the whole run is one
// transaction. A second run recognises its own first run's rows, writes
// nothing, and reports so.
package demo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/flows"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/messaging"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/db"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/portal"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
	"github.com/jackc/pgx/v5"
)

// The reserved demo domain, invented (the .invalid top-level domain never
// resolves). The demo's own accounts live here and nowhere else, and the
// boot guard admits no ACTIVE user outside it and the reserved .example
// and .invalid forms.
const demoDomain = "hh-demo.invalid"

// demoBoxMarker marks the demo_box row: the marker a guard reads to tell
// "a database this populate filled" from "a database that happens to hold
// a table of the same name". A later wave that changes the world's shape
// writes v2 beside it, never a mutation of this one.
const demoBoxMarker = "hh-demo-populate-v1"

// The demo org. One org only: the boot guard refuses a database that
// carries any other.
const (
	demoOrgSlug = "hh-demo"
	demoOrgName = "Gable Landscape Supply"
)

// The role accounts the demo sign-in mints for (the Counter Till's
// pair, D49, Manager Home's yard manager, and the roles the roadmap
// added one reviewed row at a time). The ids and the platform ids are
// pinned literals in code: they are part of the contract the demo
// module's flow table codes against, so a tester's "Sales" tap resolves
// this row, at this org, with this grant, and nothing else on the box.
const (
	salesUserID      = "de110000-0000-4000-9000-000000000001"
	salesPlatformID  = "de110000-0000-4000-9000-000000000001"
	salesEmail       = "sales@hh-demo.invalid"
	salesDisplayName = "Sales (demo)"

	managerUserID      = "de110000-0000-4000-9000-000000000002"
	managerPlatformID  = "de110000-0000-4000-9000-000000000002"
	managerEmail       = "sales.manager@hh-demo.invalid"
	managerDisplayName = "Sales manager (demo)"

	// The yard manager, Manager Home's first per-role landing (roadmap 2.3):
	// its needs-you queue counts the open shortages the demoseed plants at
	// Trenton, the one branch every demo grant reaches.
	yardMgrUserID      = "de110000-0000-4000-9000-000000000003"
	yardMgrPlatformID  = "de110000-0000-4000-9000-000000000003"
	yardMgrEmail       = "yard.manager@hh-demo.invalid"
	yardMgrDisplayName = "Yard manager (demo)"

	// Payables and Receivables, the third role account (global, tier two):
	// the flow table's row for the receivables desk, minted by the roles
	// roadmap's first increment (PR 300).
	payRecUserID      = "de110000-0000-4000-9000-0000000000a1"
	payRecPlatformID  = "de110000-0000-4000-9000-0000000000a1"
	payRecEmail       = "payables.receivables@hh-demo.invalid"
	payRecDisplayName = "Payables and Receivables (demo)"

	// The procurement manager's account, the fourth role login (branch,
	// tier two): the Buying and Stock row the roles roadmap added next
	// (PR 303). Same id block as the flow table's row, reconciled by copy.
	buyerUserID      = "de64b000-0000-4000-9000-000000000001"
	buyerPlatformID  = "de64b000-0000-4000-9000-000000000001"
	buyerEmail       = "procurement.manager@hh-demo.invalid"
	buyerDisplayName = "Procurement manager (demo)"

	// The Financial Controller, the fifth role account (global, tier one):
	// the flow table's row for the approvals queue and the general ledger.
	controllerUserID      = "de110000-0000-4000-9000-0000000000c1"
	controllerPlatformID  = "de110000-0000-4000-9000-0000000000c1"
	controllerEmail       = "financial.controller@hh-demo.invalid"
	controllerDisplayName = "Financial controller (demo)"

	// The roles behind the accounts, resolved by code against the
	// roles catalog 0015 plants. staff_role_grant rows derive from these
	// through identity.Populate, branch-scoped to the one branch below.
	salesRoleCode      = "INSIDE_SALES"
	managerRoleCode    = "SALES_MANAGER"
	yardMgrRoleCode    = "YARD_MANAGER"
	payRecRoleCode     = "PAYABLES_RECEIVABLES"
	buyerRoleCode      = "PROCUREMENT_MANAGER"
	controllerRoleCode = "FINANCIAL_CONTROLLER"

	// The two personas migration 0200 plants, named by the same literals the
	// flow table carries: the yard team's login and the driver's. The rows
	// are the migration's, not this populate's (the ids and emails are fixed
	// by 0200, and the demoseed resolves the personas by these emails), so
	// the populate verifies them, links their platform ids through the same
	// directory as the till's accounts, and converges their branch grants;
	// it never creates them. The display names are the renames this file's
	// plantedDisplayNames list leaves on the rows.
	dispatchUserID  = "d0200000-0000-4000-9000-000000000001"
	yardUserID      = "d0200000-0000-4000-9000-000000000002"
	yardEmail       = "yard.demo@" + demoDomain
	yardDisplayName = "Yard persona (demo)"
	yardRoleCode    = "YARD_TEAM"

	driverUserID      = "d0200000-0000-4000-9000-000000000003"
	driverEmail       = "driver.demo@" + demoDomain
	driverDisplayName = "Driver persona (demo)"
	driverRoleCode    = "DRIVERS"

	// Trenton, the till's branch (migration 0003's root branch; 0060
	// cites the same id). Every grant the demo world carries points here.
	trentonBranchID = "5d1b3f6e-0000-4000-9000-000000000001"

	// Kingston, the seed's second branch (migration 0003's pair, the id
	// the locations table carries). No demo login reaches it; only the
	// dealer-name rename below touches its row.
	kingstonBranchID = "5d1b3f6e-0000-4000-9000-000000000002"
)

// The invented card rail values. The merchant id names no real merchant
// account, the credential is not a key to anything (the wired rail is the
// simulator, which reads no credential), and no host is set, so nothing
// here could reach a real processor if it tried (plan section 6).
const (
	demoMerchantID     = "DEMO-MERCHANT-0001"
	demoCardCredential = "demo-not-a-key-0001"
	demoProcessor      = "run_payments"
	demoCurrency       = "USD" // the demo dealer trades in US dollars (item 3b slice 2)
)

// demoPaymentConfigID is the populate's payment configuration row's fixed
// id, so a re-run recognises its own row and so any OTHER row, present or
// appearing later, is a refusal.
const demoPaymentConfigID = "de110000-0000-4000-9000-0000000000ca"

// plantedUserIDs is the FIXED list of user ids the migrations plant on a
// fresh `migrate`, read off the migrations and pinned here in code, never
// queried (the refusal's set is a constant, so a real database's rows can
// never talk their way into it):
//
//	0043  the platform owner (an address on a real domain)
//	0060  the demo teammate (an address on a real domain)
//	0061  four homework accounts, scrubbed-0004/0005/0006/0007@example.invalid
//	      (0066 later moves 0006's email to scrubbed-0008@example.invalid;
//	      the id does not move)
//	0200  three tour personas, dispatch/yard/driver.demo@dibbits.example
var plantedUserIDs = []string{
	"d7000000-0000-4000-9000-000000000010",
	"d7000000-0000-4000-9000-0000000000da",
	"40000061-0000-4000-9000-000000000001",
	"40000061-0000-4000-9000-000000000002",
	"40000061-0000-4000-9000-000000000003",
	"40000061-0000-4000-9000-000000000004",
	"d0200000-0000-4000-9000-000000000001",
	"d0200000-0000-4000-9000-000000000002",
	"d0200000-0000-4000-9000-000000000003",
}

// plantedUserEmails admits the one planted row the migrations mint WITHOUT
// a fixed id: 0077's UAT account lets the database generate its id
// (users.id DEFAULT gen_random_uuid), so the email 0077 plants is the only
// stable handle it has, pinned here in code beside the ids.
var plantedUserEmails = []string{
	"scrubbed-0009@example.invalid",
}

// deactivatedUserIDs is the pair of planted rows whose emails sit on a
// real domain. The demo carries no real person's account (D49), so these
// two are made DEACTIVATED on the demo database only and their addresses
// move to the reserved demo domain under scrubbed local parts (D49's
// "renamed" reaching the address as well as the display name): no address
// of a real person remains anywhere in the demo database, and no session
// can resolve either row.
var deactivatedUserIDs = map[string]bool{
	"d7000000-0000-4000-9000-000000000010": true,
	"d7000000-0000-4000-9000-0000000000da": true,
}

// demoPersonas is the one branch each persona works on the demo box.
// Migration 0200 grants every BRANCH location so a walkthrough can walk
// both yards; the demo world narrows each persona to the branch its seeded
// work sits at, so its login reaches its own branch and nothing more: the
// yard team's queue and claimed picks are the roadmap's Trenton story, and
// the driver's seeded driver D1 and its runs are Kingston's. The dispatch
// persona is no lane's yet and keeps 0200's shape.
var demoPersonas = []struct{ userID, email, roleCode, branchID string }{
	{yardUserID, yardEmail, yardRoleCode, trentonBranchID},
	{driverUserID, driverEmail, driverRoleCode, kingstonBranchID},
}

// plantedEmailMoves is where the two real-domain addresses go: the
// migration numbers that planted them and the scrubbed local part each
// takes on the reserved demo domain.
var plantedEmailMoves = []struct{ id, email string }{
	{"d7000000-0000-4000-9000-000000000010", "platform.admin@hh-demo.invalid"},
	{"d7000000-0000-4000-9000-0000000000da", "demo.teammate@hh-demo.invalid"},
	// The three tour personas 0200 plants carry the first dealer's trading
	// name in the domain; the demo dealer is Gable Landscape Supply, so the
	// addresses move to the reserved demo domain too. The demoseed finds
	// them under either form (its persona aliases), so a world seeded before
	// the move and one seeded after it resolve the same three logins.
	{dispatchUserID, "dispatch.demo@" + demoDomain},
	{yardUserID, yardEmail},
	{driverUserID, driverEmail},
}

// plantedDisplayNames replaces every planted row's display name with an
// invented name on the demo database only. The migrations' placeholders
// are scrubbed forms, but two of them still carry a surname token beside
// the placeholder (0060's and 0200's, a record matter raised in the plan's
// section 6), and no token of any person's name belongs on a demo box.
// None of these names is a person: they are role labels.
var plantedDisplayNames = []struct{ id, name string }{
	{"d7000000-0000-4000-9000-000000000010", "Platform admin (demo)"},
	{"d7000000-0000-4000-9000-0000000000da", "Demo teammate (demo)"},
	{"40000061-0000-4000-9000-000000000001", "Homework account one (demo)"},
	{"40000061-0000-4000-9000-000000000002", "Homework account two (demo)"},
	{"40000061-0000-4000-9000-000000000003", "Homework account three (demo)"},
	{"40000061-0000-4000-9000-000000000004", "Homework account four (demo)"},
	{"d0200000-0000-4000-9000-000000000001", "Dispatch persona (demo)"},
	{"d0200000-0000-4000-9000-000000000002", "Yard persona (demo)"},
	{"d0200000-0000-4000-9000-000000000003", "Driver persona (demo)"},
}

// plantedDisplayNamesByEmail renames the one planted row known by email
// rather than id (0077's, whose id the database minted).
var plantedDisplayNamesByEmail = []struct{ email, name string }{
	{"scrubbed-0009@example.invalid", "Counter UAT account (demo)"},
}

// Options is RunPopulate's input. Getenv is the environment reader (the
// populate reads DATABASE_URL, CORE_DATABASE_URL and PAYMENT_VAULT_KEY and
// nothing else); Out receives the run report; Now pins the clock, nil
// meaning the wall clock read exactly once at the start (the demoseed
// discipline: nothing downstream of that one read calls time again).
type Options struct {
	Getenv func(string) string
	Out    io.Writer
	Now    *time.Time
}

// Report is what one run did, counts only. A second run against a database
// it already filled reports zero created everywhere, which is the
// idempotence promise in the command's own words.
type Report struct {
	Clock                time.Time
	UsersCreated         int
	UserRolesCreated     int
	BranchGrantsCreated  int
	PersonasConverged    int
	PersonaGrantsRemoved int
	DisplayNamesRenamed  int
	DealerNamesReplaced  int
	UsersDeactivated     int
	EmailsMoved          int
	CredentialsCleared   int
	OrgInserted          bool
	StaffGrantsInserted  int64
	PaymentConfigCreated int
	DemoBoxCreated       bool
	// The contractor world (the Pro apps' sign in): Summit Ridge Builders'
	// customers row, its five portal users and its three job sites.
	TenantAccountsCreated int
	PortalUsersCreated    int
	TenantJobsCreated     int
	// The persona inboxes (House Messages): memberships materialized for the
	// persona roles and rep DM channels ensured for the sales floor, the
	// messaging module's own seeding keyed by role.
	PersonaMemberships int
	PersonaRepChannels int
}

// staticDirectory is the populate's PlatformDirectory: it answers exactly
// the demo role accounts this file creates (Manager Home's yard manager
// among them), the two personas migration 0200 plants, the Financial
// Controller the controller lane adds and the Procurement Manager the
// procurement lane adds, and nothing else.
// identity.Populate's linkage pass counts every other row as unmatched and
// leaves it unlinked, never guessed, so the remaining migration-planted
// rows keep no platform identity on the demo box and derive no staff grants.
// The personas' ids are their 0200 users row ids, the same convention the
// till's accounts follow (the platform id IS the row id), so the mint's
// subject names the row the flow table carries.
type staticDirectory map[string]string

func (d staticDirectory) PlatformUserByEmail(_ context.Context, email string) (string, bool, error) {
	id, ok := d[strings.ToLower(email)]
	return id, ok, nil
}

// RunPopulate is the demo populate's entry: the refusals first, then the
// writes, all in one transaction that commits or leaves the database as it
// found it.
func RunPopulate(ctx context.Context, opt Options) (Report, error) {
	var report Report
	if opt.Getenv == nil {
		return report, errors.New("demo-populate: no environment reader")
	}
	if opt.Out == nil {
		opt.Out = io.Discard
	}

	// REFUSAL 1, the two database variables. serve reads CORE_DATABASE_URL
	// and every one-shot reads DATABASE_URL (the gap the entrypoint closes
	// by setting both from one value); a populate that ran against a
	// different database than the server would fill the wrong box, so the
	// mismatch is refused here rather than survived.
	databaseURL := strings.TrimSpace(opt.Getenv("DATABASE_URL"))
	coreDatabaseURL := strings.TrimSpace(opt.Getenv("CORE_DATABASE_URL"))
	if databaseURL == "" || coreDatabaseURL == "" {
		return report, errors.New("demo-populate: refusing: DATABASE_URL and CORE_DATABASE_URL must both be set and equal")
	}
	if databaseURL != coreDatabaseURL {
		return report, errors.New("demo-populate: refusing: DATABASE_URL and CORE_DATABASE_URL name different databases")
	}

	// REFUSAL 2, the box's own vault key. The payment configuration is
	// sealed under PAYMENT_VAULT_KEY and the serve role opens it again on
	// the first card charge, so an absent or malformed key would write a
	// configuration the box itself can never read. demoseed's own sealing
	// tolerates an empty key (its POD attestations are never opened, and
	// payment.NewVault builds an ephemeral one); this row is opened in
	// anger, so an empty key is refused here rather than sealed against.
	vaultKey := opt.Getenv("PAYMENT_VAULT_KEY")
	if strings.TrimSpace(vaultKey) == "" {
		return report, errors.New("demo-populate: refusing: PAYMENT_VAULT_KEY is not set, the demo card rail is sealed under the box's own vault key")
	}
	vault, err := payment.NewVault(vaultKey)
	if err != nil {
		return report, fmt.Errorf("demo-populate: refusing: PAYMENT_VAULT_KEY: %w", err)
	}

	// THE ONE CLOCK. Read exactly once, threaded through every write as a
	// value; the demo_box row records it, and the seed's world (applied
	// moments before, from its own single read) is dated within the same
	// run.
	now := time.Now().UTC()
	if opt.Now != nil {
		now = opt.Now.UTC()
	}
	report.Clock = now

	pool, err := db.Connect(ctx, databaseURL)
	if err != nil {
		return report, fmt.Errorf("demo-populate: cannot connect: %w", err)
	}
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		return report, fmt.Errorf("demo-populate: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// REFUSALS 3 to 5, the populate's own shape check, before any write.
	if err := refuseForeignWorld(ctx, tx); err != nil {
		return report, err
	}

	// The role accounts. Inserted first so identity.Populate's stamping
	// pass reaches them in the same run.
	usersCreated, err := insertRoleAccounts(ctx, tx)
	report.UsersCreated = usersCreated
	if err != nil {
		return report, err
	}
	rolesCreated, grantsCreated, err := insertRoleAccountAccess(ctx, tx)
	report.UserRolesCreated = rolesCreated
	report.BranchGrantsCreated = grantsCreated
	if err != nil {
		return report, err
	}

	// The migrations' planted rows: display names replaced, the two
	// real-domain addresses moved to the reserved demo domain and their
	// rows made inactive, every planted credential cleared, on the demo
	// database only.
	renamed, deactivated, moved, cleared, err := scrubPlantedUsers(ctx, tx, now)
	report.DisplayNamesRenamed = renamed
	report.UsersDeactivated = deactivated
	report.EmailsMoved = moved
	report.CredentialsCleared = cleared
	if err != nil {
		return report, err
	}

	// The personas: verified against 0200's plant, then converged onto the
	// one branch each works, BEFORE the identity program derives grants, so
	// the derived grant and the caller's branch reach are the converged
	// world's.
	converged, removed, err := convergePersonas(ctx, tx, now)
	report.PersonasConverged = converged
	report.PersonaGrantsRemoved = removed
	if err != nil {
		return report, err
	}

	// The dealer's own display words: the org anchor's name, the two demo
	// branches' names and the system_settings org_name row carry the
	// invented trading name, so no screen (the till's drawer strip and
	// parked list header above all) and no printed document (the receipt's
	// letterhead) renders the first dealer's real trading name from the
	// demo database. The updates are guarded on the value they would write,
	// so a re-run touches nothing, the same discipline the planted renames
	// follow. The branch display words' replacement is recorded in
	// docs/BLOCKERS.md as the granted exception to the Touches of the lane
	// that wrote it (the review round's P2-1); the org_name row is this
	// item's own extension of that convergence.
	dealerNames, err := renameDemoDealer(ctx, tx)
	report.DealerNamesReplaced = dealerNames
	if err != nil {
		return report, err
	}

	// The org, the org scope, the platform links for the role accounts
	// and the staff grants that derive from them, all through the identity
	// program itself, so the demo world is provisioned exactly the way a
	// real dealer's is.
	pop, err := identity.Populate(ctx, tx, identity.PopulateConfig{
		OrgSlug: demoOrgSlug,
		OrgName: demoOrgName,
		Directory: staticDirectory{
			salesEmail:      salesPlatformID,
			managerEmail:    managerPlatformID,
			yardMgrEmail:    yardMgrPlatformID,
			yardEmail:       yardUserID,
			driverEmail:     driverUserID,
			payRecEmail:     payRecPlatformID,
			buyerEmail:      buyerPlatformID,
			controllerEmail: controllerPlatformID,
		},
	})
	if err != nil {
		return report, fmt.Errorf("demo-populate: identity populate: %w", err)
	}
	report.OrgInserted = pop.OrgInserted
	report.StaffGrantsInserted = pop.Grants.Inserted

	// The contractor world, after the org anchor exists: Summit Ridge
	// Builders, its five portal users and its three jobs, written through
	// the owning modules' seams so the table fence has nothing to allow.
	tenantAccounts, portalUsers, tenantJobs, err := writeTenantWorld(ctx, tx, pop.OrgID)
	report.TenantAccountsCreated, report.PortalUsersCreated, report.TenantJobsCreated = tenantAccounts, portalUsers, tenantJobs
	if err != nil {
		return report, err
	}

	// REFUSAL 6 belongs beside the write it guards: exactly one payment
	// configuration, this populate's own. A row any other run or hand put
	// there is a refusal, so the box can never grow a second rail.
	payCreated, err := writePaymentConfig(ctx, tx, vault)
	report.PaymentConfigCreated = payCreated
	if err != nil {
		return report, err
	}
	// The rail's row says demoCurrency, and the card rail refuses a charge whose row disagrees with
	// the dealer's currency, so the dealer's own currency is written beside it, through the settings
	// module's seam. The build's seed writes the whole configuration after this.
	if err := settings.WriteDealerCurrencyTx(ctx, tx, demoCurrency); err != nil {
		return report, fmt.Errorf("demo-populate: dealer currency: %w", err)
	}

	// The demo_box row: the one-row table only this populate creates,
	// outside the migration chain (the schema is frozen), recording the
	// moment the world was rebuilt.
	boxCreated, err := writeDemoBox(ctx, tx, now)
	report.DemoBoxCreated = boxCreated
	if err != nil {
		return report, err
	}

	if err := tx.Commit(ctx); err != nil {
		return report, fmt.Errorf("demo-populate: commit: %w", err)
	}

	// The personas' House Messages inboxes, seeded BY ROLE after the
	// transactional world commits (the seeding's every statement converges
	// on its own rows, so it needs no share of the all-or-nothing
	// transaction above): every persona role's derived membership in its
	// branch's rooms, and the sales floor's rep DM channels, the path a yard
	// short pick's notice reaches the order's rep through. Keyed by role, so
	// a persona whose rows land on a later branch gets its rooms the next
	// time this populate runs.
	seedPool, err := db.Connect(ctx, databaseURL)
	if err != nil {
		return report, fmt.Errorf("demo-populate: persona seed connect: %w", err)
	}
	seeded, err := messaging.SeedPersonaInboxes(ctx, seedPool)
	seedPool.Close()
	report.PersonaMemberships = seeded.MembershipsSynced
	report.PersonaRepChannels = seeded.RepChannelsSeeded
	if err != nil {
		return report, fmt.Errorf("demo-populate: persona inboxes: %w", err)
	}

	writeReport(opt.Out, report)
	return report, nil
}

// insertRoleAccounts writes the demo role accounts, idempotently. The
// rows carry no password hash (the core has no password route; the demo
// sign-in mints its sessions from the flow table, never from a secret) and
// no platform id yet: the platform id arrives through identity.Populate's
// linkage pass, answered by the static directory, exactly the way a real
// dealer's accounts are linked.
func insertRoleAccounts(ctx context.Context, tx pgx.Tx) (int, error) {
	created := 0
	for _, u := range []struct{ id, email, name string }{
		{salesUserID, salesEmail, salesDisplayName},
		{managerUserID, managerEmail, managerDisplayName},
		{yardMgrUserID, yardMgrEmail, yardMgrDisplayName},
		{payRecUserID, payRecEmail, payRecDisplayName},
		{buyerUserID, buyerEmail, buyerDisplayName},
		{controllerUserID, controllerEmail, controllerDisplayName},
	} {
		tag, err := tx.Exec(ctx, `
			INSERT INTO users (id, email, display_name, status)
			VALUES ($1, $2, $3, 'ACTIVE')
			ON CONFLICT (id) DO NOTHING`,
			u.id, u.email, u.name)
		if err != nil {
			return 0, fmt.Errorf("demo-populate: role account %s: %w", u.name, err)
		}
		created += int(tag.RowsAffected())
	}
	return created, nil
}

// insertRoleAccountAccess writes each account's carried role (by code,
// against the catalog 0015 plants) and its one branch grant at Trenton. A
// tombstone left by anything is healed rather than honoured: the upsert
// fires only when the row sits tombstoned, so a re-run still writes
// nothing. The inserts are INSERT ... SELECT, which writes nothing without
// an error when the catalog lacks the role or the locations table lacks the
// branch, so after them the live rows are verified and a missing one is a
// refusal: the demo world must not ship role accounts that reach nothing.
func insertRoleAccountAccess(ctx context.Context, tx pgx.Tx) (roles, grants int, err error) {
	for _, a := range []struct{ userID, roleCode string }{
		{salesUserID, salesRoleCode},
		{managerUserID, managerRoleCode},
		{yardMgrUserID, yardMgrRoleCode},
		{payRecUserID, payRecRoleCode},
		{buyerUserID, buyerRoleCode},
		{controllerUserID, controllerRoleCode},
	} {
		tag, err := tx.Exec(ctx, `
			INSERT INTO user_roles (user_id, role_id)
			SELECT $1, r.id FROM roles r WHERE r.code = $2
			ON CONFLICT (user_id, role_id) DO UPDATE SET deleted_at = NULL
			WHERE user_roles.deleted_at IS NOT NULL`,
			a.userID, a.roleCode)
		if err != nil {
			return 0, 0, fmt.Errorf("demo-populate: role %s: %w", a.roleCode, err)
		}
		roles += int(tag.RowsAffected())
	}
	// Payables and Receivables and the Financial Controller are global roles,
	// so the staff grants derived for them carry no branch and their reach
	// is every branch; the Trenton rows below are inert for them (reach
	// follows the role's scope, never user_branch_grants) and are written
	// only so the accounts have the same shape as the others. The buyer is
	// branch scoped, so its Trenton row is its real grant.
	for _, userID := range []string{salesUserID, managerUserID, yardMgrUserID, payRecUserID, buyerUserID, controllerUserID} {
		tag, err := tx.Exec(ctx, `
			INSERT INTO user_branch_grants (user_id, branch_id)
			SELECT $1, l.id FROM locations l WHERE l.id = $2::uuid
			ON CONFLICT (user_id, branch_id) DO UPDATE SET deleted_at = NULL
			WHERE user_branch_grants.deleted_at IS NOT NULL`,
			userID, trentonBranchID)
		if err != nil {
			return 0, 0, fmt.Errorf("demo-populate: branch grant: %w", err)
		}
		grants += int(tag.RowsAffected())
	}
	// The verify half: a live role row per account and a live branch grant
	// per account, whatever this run inserted.
	for _, a := range []struct{ userID, roleCode string }{
		{salesUserID, salesRoleCode},
		{managerUserID, managerRoleCode},
		{yardMgrUserID, yardMgrRoleCode},
		{payRecUserID, payRecRoleCode},
		{buyerUserID, buyerRoleCode},
		{controllerUserID, controllerRoleCode},
	} {
		var n int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM user_roles ur
			  JOIN roles r ON r.id = ur.role_id
			 WHERE ur.user_id = $1::uuid AND r.code = $2 AND ur.deleted_at IS NULL`,
			a.userID, a.roleCode).Scan(&n); err != nil {
			return 0, 0, fmt.Errorf("demo-populate: role verify: %w", err)
		}
		if n == 0 {
			return 0, 0, fmt.Errorf("demo-populate: refusing: the roles catalog holds no %s row, the %s account would reach nothing", a.roleCode, a.userID)
		}
	}
	for _, userID := range []string{salesUserID, managerUserID, yardMgrUserID, payRecUserID, buyerUserID, controllerUserID} {
		var n int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM user_branch_grants
			 WHERE user_id = $1::uuid AND branch_id = $2::uuid AND deleted_at IS NULL`,
			userID, trentonBranchID).Scan(&n); err != nil {
			return 0, 0, fmt.Errorf("demo-populate: branch grant verify: %w", err)
		}
		if n == 0 {
			return 0, 0, fmt.Errorf("demo-populate: refusing: the locations table holds no branch %s, the account %s would reach no branch", trentonBranchID, userID)
		}
	}
	return roles, grants, nil
}

// convergePersonas verifies each persona's planted role and narrows its
// branch grants onto the one branch it works, on the demo database only.
// The verify half refuses before any write: a persona the flow table mints
// whose role is missing would be a login that reaches nothing, and that is
// a refusal, not a world. (The account-lifetime half is not this pass's to
// check: the row is 0200's planted ACTIVE row, and the mint itself refuses
// a non-ACTIVE account before any session exists, so the door that matters
// already fails closed.) The converge half tombstones every live grant
// outside the persona's branch (0200 plants every BRANCH location) and
// heals a tombstone at its own, each guarded so a re-run writes nothing.
// The dispatch persona is deliberately absent from demoPersonas: no lane
// has converged it yet, and 0200's both-branch shape is its world until
// one does.
func convergePersonas(ctx context.Context, tx pgx.Tx, now time.Time) (converged, removed int, err error) {
	for _, p := range demoPersonas {
		var roles int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM user_roles ur
			  JOIN roles r ON r.id = ur.role_id
			 WHERE ur.user_id = $1::uuid AND r.code = $2 AND ur.deleted_at IS NULL`,
			p.userID, p.roleCode).Scan(&roles); err != nil {
			return 0, 0, fmt.Errorf("demo-populate: persona role verify: %w", err)
		}
		if roles == 0 {
			return 0, 0, fmt.Errorf("demo-populate: refusing: the persona %s holds no live %s row, its login would reach nothing", p.email, p.roleCode)
		}
		tag, err := tx.Exec(ctx, `
			UPDATE user_branch_grants SET deleted_at = $3
			 WHERE user_id = $1::uuid AND deleted_at IS NULL AND branch_id <> $2::uuid`,
			p.userID, p.branchID, now)
		if err != nil {
			return 0, 0, fmt.Errorf("demo-populate: persona branch grants: %w", err)
		}
		removed += int(tag.RowsAffected())
		tag, err = tx.Exec(ctx, `
			INSERT INTO user_branch_grants (user_id, branch_id)
			SELECT $1::uuid, l.id FROM locations l WHERE l.id = $2::uuid
			ON CONFLICT (user_id, branch_id) DO UPDATE SET deleted_at = NULL
			WHERE user_branch_grants.deleted_at IS NOT NULL`,
			p.userID, p.branchID)
		if err != nil {
			return 0, 0, fmt.Errorf("demo-populate: persona branch grant heal: %w", err)
		}
		if tag.RowsAffected() > 0 {
			converged++
		}
		// The verify half, whatever this run wrote: exactly one live grant,
		// at the persona's own branch.
		var live, atOwn int
		if err := tx.QueryRow(ctx, `
			SELECT count(*), count(*) FILTER (WHERE branch_id = $2::uuid)
			  FROM user_branch_grants WHERE user_id = $1::uuid AND deleted_at IS NULL`,
			p.userID, p.branchID).Scan(&live, &atOwn); err != nil {
			return 0, 0, fmt.Errorf("demo-populate: persona branch grant verify: %w", err)
		}
		if live != 1 || atOwn != 1 {
			return 0, 0, fmt.Errorf("demo-populate: refusing: the persona %s holds %d live branch grant(s) (%d at its own), the demo world works one branch", p.email, live, atOwn)
		}
	}
	return converged, removed, nil
}

// scrubPlantedUsers rewrites the planted rows on the demo database only:
// display names replaced with the invented ones, the two real-domain
// addresses moved to the reserved demo domain, the two rows deactivated,
// and every planted password hash cleared (the demo carries no credential,
// not even a dead one: the core has no password route and no planted hash
// belongs on a box of invented data). Each update is guarded on the value
// it would change and dated from the run's one clock, so a re-run touches
// nothing.
func scrubPlantedUsers(ctx context.Context, tx pgx.Tx, now time.Time) (renamed, deactivated, moved, cleared int, err error) {
	for _, r := range plantedDisplayNames {
		tag, err := tx.Exec(ctx,
			`UPDATE users SET display_name = $2, updated_at = $3
			  WHERE id = $1::uuid AND display_name IS DISTINCT FROM $2`,
			r.id, r.name, now)
		if err != nil {
			return 0, 0, 0, 0, fmt.Errorf("demo-populate: planted rename: %w", err)
		}
		renamed += int(tag.RowsAffected())
	}
	for _, r := range plantedDisplayNamesByEmail {
		tag, err := tx.Exec(ctx,
			`UPDATE users SET display_name = $2, updated_at = $3
			  WHERE lower(email::text) = $1 AND display_name IS DISTINCT FROM $2`,
			r.email, r.name, now)
		if err != nil {
			return 0, 0, 0, 0, fmt.Errorf("demo-populate: planted rename: %w", err)
		}
		renamed += int(tag.RowsAffected())
	}
	for _, r := range plantedEmailMoves {
		tag, err := tx.Exec(ctx,
			`UPDATE users SET email = $2, updated_at = $3
			  WHERE id = $1::uuid AND email IS DISTINCT FROM $2`,
			r.id, r.email, now)
		if err != nil {
			return 0, 0, 0, 0, fmt.Errorf("demo-populate: planted email move: %w", err)
		}
		moved += int(tag.RowsAffected())
	}
	for id := range deactivatedUserIDs {
		tag, err := tx.Exec(ctx,
			`UPDATE users SET status = 'DEACTIVATED', updated_at = $2
			  WHERE id = $1::uuid AND status <> 'DEACTIVATED'`,
			id, now)
		if err != nil {
			return 0, 0, 0, 0, fmt.Errorf("demo-populate: planted deactivate: %w", err)
		}
		deactivated += int(tag.RowsAffected())
	}
	// The planted credentials, cleared in one guarded statement: the nine
	// fixed ids and 0077's row by its migration-fixed email, the one
	// planted row whose id the database minted.
	tag, err := tx.Exec(ctx, `
		UPDATE users SET password_hash = NULL, updated_at = $2
		 WHERE (id = ANY($1::uuid[]) OR lower(email::text) = ANY($3))
		   AND password_hash IS NOT NULL`,
		plantedUserIDs, now, plantedUserEmails)
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("demo-populate: planted credential clear: %w", err)
	}
	cleared += int(tag.RowsAffected())
	return renamed, deactivated, moved, cleared, nil
}

// ConvergeDealerBranches moves the two BRANCH rows onto the demo dealer: code,
// display name and address. Each statement is guarded on the values it would
// write, so a re-run matches nothing and the count is only what this run
// changed. It runs inside the caller's transaction.
func ConvergeDealerBranches(ctx context.Context, tx pgx.Tx) (replaced int, err error) {
	for _, b := range DealerBranches {
		tag, err := tx.Exec(ctx,
			`UPDATE locations
			    SET code = $2, name = $3, updated_at = now()
			  WHERE id = $1::uuid AND (code::text IS DISTINCT FROM $2 OR name IS DISTINCT FROM $3)`,
			b.ID, b.Code, b.Name)
		if err != nil {
			return 0, fmt.Errorf("demo dealer branch %s: %w", b.Code, err)
		}
		replaced += int(tag.RowsAffected())
		tag, err = tx.Exec(ctx,
			`UPDATE locations
			    SET address = jsonb_build_object(
			          'line1', $2::text, 'city', $3::text, 'province', $4::text,
			          'postal', $5::text, 'country', 'US'),
			        updated_at = now()
			  WHERE id = $1::uuid
			    AND address IS DISTINCT FROM jsonb_build_object(
			          'line1', $2::text, 'city', $3::text, 'province', $4::text,
			          'postal', $5::text, 'country', 'US')`,
			b.ID, b.Line1, b.City, b.Province, b.Postal)
		if err != nil {
			return 0, fmt.Errorf("demo dealer branch %s address: %w", b.Code, err)
		}
		replaced += int(tag.RowsAffected())
	}
	return replaced, nil
}

// renameDemoDealer converges the demo database's dealer display words onto
// the invented trading name. The org anchor's name is already the identity
// program's to converge (ProvisionOrgAnchor's guarded upsert writes the
// configured name on every run); what it does not reach are the two demo
// branches' names, which the migrations plant with the first dealer's real
// trading name joined to the city by an em dash (migrations 0003 and 0014)
// and which the till's screens render verbatim (the drawer strip, the
// parked list header), those branches' address line1, which 0003 plants as
// a note to the first dealer and the printed receipt's letterhead serves
// verbatim (printing's ReadBranchHeader), and the system_settings org_name
// row (0014), which the same letterhead reads verbatim. Each write is
// guarded on the value it would write, so a re-run writes nothing, and the
// count is only what this run changed.
func renameDemoDealer(ctx context.Context, tx pgx.Tx) (replaced int, err error) {
	replaced, err = ConvergeDealerBranches(ctx, tx)
	if err != nil {
		return 0, err
	}
	// The settings row the receipt's letterhead reads, converged the way
	// the identity program converges the org anchor: an upsert whose
	// update leg fires only when the stored value differs, so a re-run
	// writes nothing. Real deployments keep their own org_name; only the
	// demo database is converged.
	tag, err := tx.Exec(ctx, `
		INSERT INTO system_settings (key, value) VALUES ('org_name', to_jsonb($1::text))
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
		 WHERE system_settings.value IS DISTINCT FROM EXCLUDED.value`,
		demoOrgName)
	if err != nil {
		return 0, fmt.Errorf("demo-populate: dealer name: %w", err)
	}
	replaced += int(tag.RowsAffected())
	return replaced, nil
}

// writePaymentConfig seals the invented credentials under the box's own
// vault key (the demoseed_seal path: payment.NewVault over
// PAYMENT_VAULT_KEY, AES-256-GCM) and inserts the one active row. No host,
// surcharges off, no creator: the populate is not a person.
func writePaymentConfig(ctx context.Context, tx pgx.Tx, vault *payment.Vault) (int, error) {
	plain, err := json.Marshal(map[string]string{"api_key": demoCardCredential})
	if err != nil {
		return 0, fmt.Errorf("demo-populate: payment credentials: %w", err)
	}
	ciphertext, keyVersion, err := vault.Encrypt(plain)
	if err != nil {
		return 0, fmt.Errorf("demo-populate: payment sealing: %w", err)
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO payment_config
			(id, processor, merchant_id, currency, credentials_ciphertext, key_version,
			 base_url, mdr_bps, surcharge_enabled, status, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, '', 0, FALSE, 'active', NULL)
		ON CONFLICT (id) DO NOTHING`,
		demoPaymentConfigID, demoProcessor, demoMerchantID, demoCurrency, ciphertext, keyVersion)
	if err != nil {
		return 0, fmt.Errorf("demo-populate: payment config: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// writeDemoBox creates the demo_box table (runtime, outside the frozen
// migration chain: no migration is ever written for this) and inserts the
// one row recording the run's moment. The boot guard reads this row; a
// database without it is not a demo database.
func writeDemoBox(ctx context.Context, tx pgx.Tx, now time.Time) (bool, error) {
	if _, err := tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS demo_box (
		  singleton    TEXT PRIMARY KEY,
		  populated_at TIMESTAMPTZ NOT NULL,
		  marker       TEXT NOT NULL
		)`); err != nil {
		return false, fmt.Errorf("demo-populate: demo_box table: %w", err)
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO demo_box (singleton, populated_at, marker)
		VALUES ('demo', $1, $2)
		ON CONFLICT (singleton) DO NOTHING`,
		now, demoBoxMarker)
	if err != nil {
		return false, fmt.Errorf("demo-populate: demo_box row: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// writeTenantWorld creates the demo's one contractor account, Summit Ridge
// Builders (GLS-TENANT-r3): the customers row under the flow table's pinned
// id (top pricing tier is the customer type CONTRACTOR on account terms),
// one portal user per contractor flow with the platform identity and the
// tenant already set, and the three job sites. Every write goes through the
// module that owns the table (customer, identity, portal) and converges on
// its own rows, so a second run writes nothing. The portal rows are keyed
// by the flow table: the same id is the row id, the platform user id and the
// token subject.
func writeTenantWorld(ctx context.Context, tx pgx.Tx, orgID string) (accounts, users, jobs int, err error) {
	made, err := customer.EnsureAccount(ctx, tx, customer.PinnedAccount{
		ID:               flows.TenantAccountID,
		AccountNo:        flows.TenantAccountNo,
		Name:             flows.TenantAccountName,
		CustomerType:     "CONTRACTOR",
		HomeBranchID:     trentonBranchID,
		OrgID:            orgID,
		PaymentTermsDays: 30,
		CreditLimitCents: 5_000_000,
	})
	if err != nil {
		return 0, 0, 0, fmt.Errorf("demo-populate: contractor account: %w", err)
	}
	if made {
		accounts = 1
	}
	var seeds []identity.PortalUserSeed
	for _, a := range flows.All() {
		if a.Class() != flows.ClassTenant {
			continue
		}
		seeds = append(seeds, identity.PortalUserSeed{
			ID: a.PlatformUserID, CustomerID: a.Tenant, Email: a.Email,
			Name: a.DisplayName, Role: a.Role, PlatformUserID: a.PlatformUserID,
		})
	}
	users, err = identity.ProvisionPortalUsers(ctx, tx, seeds)
	if err != nil {
		return accounts, users, 0, fmt.Errorf("demo-populate: contractor portal users: %w", err)
	}
	for _, j := range flows.TenantJobs() {
		made, err := portal.EnsureProject(ctx, tx, portal.PinnedProject{
			ID: j.ID, CustomerID: flows.TenantAccountID, Name: j.Name, JobAddress: j.Address,
		})
		if err != nil {
			return accounts, users, jobs, fmt.Errorf("demo-populate: contractor job: %w", err)
		}
		if made {
			jobs++
		}
	}
	return accounts, users, jobs, nil
}

// writeReport prints what the run did, in the demoseed run report's
// spirit: the clock, the counts, and the second-run line stated plainly.
// Every figure counts what THIS RUN created or changed, never what the
// database already held.
func writeReport(out io.Writer, r Report) {
	fmt.Fprintf(out, "demo-populate %s\n", demoBoxMarker)
	fmt.Fprintf(out, "  clock            %s\n", r.Clock.Format(time.RFC3339))
	fmt.Fprintf(out, "  org              %s (inserted: %v)\n", demoOrgSlug, r.OrgInserted)
	fmt.Fprintf(out, "  role accounts    %d created, %d role rows, %d branch grants at %s\n",
		r.UsersCreated, r.UserRolesCreated, r.BranchGrantsCreated, trentonBranchID)
	fmt.Fprintf(out, "  personas         %d converged, %d branch grants outside their own removed\n",
		r.PersonasConverged, r.PersonaGrantsRemoved)
	fmt.Fprintf(out, "  planted rows     %d display names replaced, %d deactivated, %d addresses moved to %s, %d planted credentials cleared\n",
		r.DisplayNamesRenamed, r.UsersDeactivated, r.EmailsMoved, demoDomain, r.CredentialsCleared)
	fmt.Fprintf(out, "  dealer names     %d replaced with the invented trading name\n", r.DealerNamesReplaced)
	fmt.Fprintf(out, "  staff grants     %d inserted\n", r.StaffGrantsInserted)
	fmt.Fprintf(out, "  payment config   %d created (merchant %s, simulator, no host, surcharges off)\n",
		r.PaymentConfigCreated, demoMerchantID)
	fmt.Fprintf(out, "  contractor world %d account, %d portal users, %d jobs (%s)\n",
		r.TenantAccountsCreated, r.PortalUsersCreated, r.TenantJobsCreated, flows.TenantAccountName)
	fmt.Fprintf(out, "  demo_box         row created: %v\n", r.DemoBoxCreated)
	fmt.Fprintf(out, "  persona inboxes  %d memberships, %d rep channels\n", r.PersonaMemberships, r.PersonaRepChannels)
	if r.createdNothing() {
		fmt.Fprintln(out, "  the demo world was already complete, this run wrote nothing.")
	}
}

func (r Report) createdNothing() bool {
	return r.UsersCreated == 0 && r.UserRolesCreated == 0 && r.BranchGrantsCreated == 0 &&
		r.PersonasConverged == 0 && r.PersonaGrantsRemoved == 0 &&
		r.DisplayNamesRenamed == 0 && r.DealerNamesReplaced == 0 &&
		r.UsersDeactivated == 0 && r.EmailsMoved == 0 &&
		r.CredentialsCleared == 0 &&
		r.PaymentConfigCreated == 0 && !r.DemoBoxCreated && r.StaffGrantsInserted == 0 &&
		r.TenantAccountsCreated == 0 && r.PortalUsersCreated == 0 && r.TenantJobsCreated == 0
}
