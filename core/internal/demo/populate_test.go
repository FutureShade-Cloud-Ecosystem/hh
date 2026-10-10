//go:build demo

// The demo populate's exit tests (W3). Each test builds its OWN database
// from zero on a throwaway local Postgres (the migrations are applied in
// process, the seed and the populate run through the real built binary in
// the demo shape), so nothing is shared between tests and the claim under
// test is about the whole pipeline: migrate, demoseed -apply, demo-populate,
// demo-populate again.
//
//	Run: hh-till-l3b-pg up (a local Postgres on 127.0.0.1:5591), then
//	     DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:5591/postgres?sslmode=disable' \
//	     go test -tags demo ./internal/demo/ -count=1 -p 1 -v
//
// Without DATABASE_URL every test here skips, so the suite stays green
// wherever no throwaway Postgres exists. The harness refuses a database
// address that is not local.
package demo

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/printing"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedClock pins the pipeline's clock, the same discipline as the demoseed
// integration tests: one fixed instant passed to both `demoseed -apply
// -now` and `demo-populate -now`, so the world's dates, and every count
// and day assertion below, are the same on every run.
var seedClock = time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)

// testVaultKey is 64 hex chars (32 bytes): a well-formed shape for a test
// key, generated here, guarding nothing.
const testVaultKey = "ab01ab01ab01ab01ab01ab01ab01ab01ab01ab01ab01ab01ab01ab01ab01ab01"

// testDatabaseURL reads the throwaway Postgres's base URL and refuses any
// address that is not local.
func testDatabaseURL(t *testing.T) string {
	t.Helper()
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL is unset, the demo populate's exit tests need a throwaway local Postgres")
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("DATABASE_URL does not parse: %v", err)
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1", "":
		return base
	default:
		t.Fatalf("DATABASE_URL points at %q, the exit tests run only against a local throwaway Postgres", u.Hostname())
		return ""
	}
}

// freshMigratedDB builds a database from zero and migrates it with the same
// files `core migrate` applies, in the same order. Nothing is shared.
func freshMigratedDB(t *testing.T, suffix string) (*pgxpool.Pool, string) {
	t.Helper()
	base := testDatabaseURL(t)
	name := "demo_pop_it_" + suffix
	adminDSN, testDSN := swapDatabase(base, "postgres"), swapDatabase(base, name)

	ctx := context.Background()
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatalf("connecting to the admin database: %v", err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
		t.Fatalf("dropping %s: %v", name, err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("creating %s: %v", name, err)
	}

	pool, err := pgxpool.New(ctx, testDSN)
	if err != nil {
		t.Fatalf("connecting to %s: %v", name, err)
	}
	t.Cleanup(pool.Close)

	files, err := filepath.Glob(filepath.Join("..", "..", "db", "migrations", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no migrations found (err=%v)", err)
	}
	sort.Strings(files)
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			t.Fatalf("applying %s: %v", filepath.Base(f), err)
		}
	}
	return pool, testDSN
}

func swapDatabase(dsn, name string) string {
	slash := strings.LastIndex(dsn, "/")
	if slash < 0 {
		return dsn
	}
	tail := ""
	if q := strings.Index(dsn[slash:], "?"); q >= 0 {
		tail = dsn[slash+q:]
	}
	return dsn[:slash+1] + name + tail
}

// buildDemoCore builds the demo-shaped core binary once and returns its
// path: the binary that carries both `demoseed` and the demo-populate
// subcommand, exactly the image's shape.
func buildDemoCore(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "core-demo")
	cmd := exec.Command("go", "build", "-tags", "demo", "-o", bin, "./cmd/core")
	cmd.Dir = "../.."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the demo core: %v\n%s", err, out)
	}
	return bin
}

// subEnv builds the subprocess environment with the database variables and
// the vault key set from the overrides, never inherited.
func subEnv(t *testing.T, overrides map[string]string) []string {
	t.Helper()
	drop := map[string]bool{"DATABASE_URL": true, "CORE_DATABASE_URL": true, "PAYMENT_VAULT_KEY": true}
	var env []string
	for _, kv := range os.Environ() {
		key := strings.SplitN(kv, "=", 2)[0]
		if !drop[key] {
			env = append(env, kv)
		}
	}
	for k, v := range overrides {
		env = append(env, k+"="+v)
	}
	return env
}

// runStep runs one pipeline step through the built binary and fails on a
// non-zero exit, printing the step's output.
func runStep(t *testing.T, bin string, args []string, env []string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s: %v\n%s", strings.Join(args, " "), err, out.String())
	}
	return out.String()
}

// populateEnv is the environment a populate (or seed) step runs under.
func populateEnv(dsn string) map[string]string {
	return map[string]string{
		"DATABASE_URL":      dsn,
		"CORE_DATABASE_URL": dsn,
		"PAYMENT_VAULT_KEY": testVaultKey,
	}
}

// runPopulateOptions is the in-process Options shape, for the refusal tests.
func runPopulateOptions(dsn string) Options {
	getenv := func(k string) string {
		switch k {
		case "DATABASE_URL", "CORE_DATABASE_URL":
			return dsn
		case "PAYMENT_VAULT_KEY":
			return testVaultKey
		default:
			return ""
		}
	}
	now := seedClock
	return Options{Getenv: getenv, Out: io.Discard, Now: &now}
}

// seedAndPopulate runs the whole pipeline through the binary: seed, then
// populate, then populate again, returning both populate reports' output.
func seedAndPopulate(t *testing.T, bin, dsn string) (first, second string) {
	t.Helper()
	env := subEnv(t, populateEnv(dsn))
	runStep(t, bin, []string{"demoseed", "-apply", "-now", seedClock.Format(time.RFC3339)}, env)
	first = runStep(t, bin, []string{"demo-populate", "-now", seedClock.Format(time.RFC3339)}, env)
	second = runStep(t, bin, []string{"demo-populate", "-now", seedClock.Format(time.RFC3339)}, env)
	return first, second
}

// worldTables is every table the populate writes. demo_box joins only once
// it exists (after the first populate run); system_settings joins because
// the populate converges the org_name row (the receipt's letterhead row)
// onto the invented demo name.
var worldTables = []string{"users", "user_roles", "user_branch_grants", "staff_role_grant", "payment_config", "org", "system_settings", "msg_channels", "msg_members", "demo_box"}

// migratedWorldTables is worldTables before the populate has run, when no
// demo_box table exists yet.
var migratedWorldTables = worldTables[:len(worldTables)-1]

// snapshotWorld fingerprints every row of the given tables, contents and
// row versions, so an assertion can hold that a run changed nothing at all
// rather than merely created nothing: any update, delete or re-dated
// timestamp on any row moves the fingerprint.
func snapshotWorld(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tables []string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, table := range tables {
		var sum string
		if err := pool.QueryRow(ctx, `
			SELECT md5(coalesce(string_agg(line, E'\n' ORDER BY line), 'empty'))
			  FROM (
			    SELECT to_jsonb(r)::text AS line
			      FROM (SELECT *, xmin::text AS row_version FROM `+table+`) r
			  ) s`).Scan(&sum); err != nil {
			t.Fatalf("snapshot %s: %v", table, err)
		}
		out[table] = sum
	}
	return out
}

// assertWorldUnchanged holds that a run between two snapshots touched no
// row of any snapshotted table, naming the tables that moved.
func assertWorldUnchanged(t *testing.T, before, after map[string]string) {
	t.Helper()
	for table, sum := range before {
		if after[table] != sum {
			t.Errorf("%s changed across a run that must have written nothing", table)
		}
	}
}

// -- The end-to-end exit test ----------------------------------------------------------------------

func TestPopulate_BuildsTheDemoWorldAndRunsTwiceCreatingNothing(t *testing.T) {
	ctx := context.Background()
	pool, dsn := freshMigratedDB(t, "e2e")
	bin := buildDemoCore(t)

	first, second := seedAndPopulate(t, bin, dsn)

	if !strings.Contains(first, demoMerchantID) {
		t.Errorf("the first run should name the demo merchant, it printed:\n%s", first)
	}
	if !strings.Contains(second, "wrote nothing") {
		t.Errorf("the second run should report it wrote nothing, it printed:\n%s", second)
	}

	// The rerun changed nothing, row by row: every table the populate
	// writes, contents and row versions, is fingerprinted before and after
	// a further run, and the fingerprints must agree. This is the
	// assertion that holds identity.Populate's org anchor upsert to the
	// same discipline as the populate's own statements.
	before := snapshotWorld(t, ctx, pool, worldTables)
	runStep(t, bin, []string{"demo-populate", "-now", seedClock.Format(time.RFC3339)}, subEnv(t, populateEnv(dsn)))
	after := snapshotWorld(t, ctx, pool, worldTables)
	assertWorldUnchanged(t, before, after)

	// One org, and it is the demo org.
	var orgID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM org WHERE lower(slug::text) = $1`, demoOrgSlug).Scan(&orgID); err != nil {
		t.Fatalf("the hh-demo org is missing: %v", err)
	}
	var orgCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM org`).Scan(&orgCount); err != nil {
		t.Fatal(err)
	}
	if orgCount != 1 {
		t.Errorf("the database holds %d org rows, want exactly the one demo org", orgCount)
	}

	// identity.Populate's own verification pass agrees.
	counts, err := identity.VerifyPopulation(ctx, pool, orgID)
	if err != nil {
		t.Fatalf("VerifyPopulation: %v", err)
	}
	if counts.OrgRows != 1 {
		t.Errorf("OrgRows = %d, want 1", counts.OrgRows)
	}
	if counts.UsersUnstampedOrg != 0 || counts.UserRolesUnstampedOrg != 0 ||
		counts.UserBranchGrantsUnstampedOrg != 0 || counts.PortalUsersUnstampedTenant != 0 {
		t.Errorf("the org scope did not stamp clean: %+v", counts)
	}
	if counts.UsersLinked != 4 || counts.UsersUnlinked != 10 {
		t.Errorf("linked %d / unlinked %d users, want the 4 demo role accounts linked and the 10 planted rows unlinked", counts.UsersLinked, counts.UsersUnlinked)
	}
	if counts.StaffGrants != 4 || counts.StaffGrantsBranch != 2 || counts.StaffGrantsGlobal != 2 {
		t.Errorf("staff grants %d (branch %d, global %d), want the two counter accounts' Trenton grants and the one global grant each of Payables and Receivables and the Financial Controller", counts.StaffGrants, counts.StaffGrantsBranch, counts.StaffGrantsGlobal)
	}
	if counts.StaffGrantShapeViolations != 0 || counts.StaffGrantsUnlinked != 0 {
		t.Errorf("grant shape or linkage defects: %+v", counts)
	}

	// No user row at all sits outside the reserved demo domain and
	// .example and .invalid: the two planted rows that arrived on a real
	// domain had their addresses moved with their deactivation, so no
	// address of a real person is anywhere in the demo database.
	var outside int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM users
		 WHERE lower(email::text) NOT LIKE '%@hh-demo.invalid'
		   AND lower(email::text) NOT LIKE '%.example'
		   AND lower(email::text) NOT LIKE '%.invalid'`).Scan(&outside); err != nil {
		t.Fatal(err)
	}
	if outside != 0 {
		t.Errorf("%d user row(s) sit outside the reserved domains", outside)
	}

	// The two role accounts are exactly as the demo sign-in will resolve them.
	assertRoleAccount(t, ctx, pool, salesUserID, salesEmail, salesDisplayName, "INSIDE_SALES")
	assertRoleAccount(t, ctx, pool, managerUserID, managerEmail, managerDisplayName, "SALES_MANAGER")
	assertRoleAccount(t, ctx, pool, payRecUserID, payRecEmail, payRecDisplayName, "PAYABLES_RECEIVABLES")
	assertRoleAccount(t, ctx, pool, controllerUserID, controllerEmail, controllerDisplayName, "FINANCIAL_CONTROLLER")

	// The real-domain planted rows are inactive, renamed, re-addressed on
	// the reserved demo domain and carry no credential.
	for _, r := range []struct{ id, name, email string }{
		{"d7000000-0000-4000-9000-000000000010", "Platform admin (demo)", "platform.admin@hh-demo.invalid"},
		{"d7000000-0000-4000-9000-0000000000da", "Demo teammate (demo)", "demo.teammate@hh-demo.invalid"},
	} {
		var status, display, gotEmail string
		var hash *string
		if err := pool.QueryRow(ctx,
			`SELECT status, display_name, email::text, password_hash::text FROM users WHERE id = $1::uuid`, r.id).
			Scan(&status, &display, &gotEmail, &hash); err != nil {
			t.Fatalf("planted row %s missing: %v", r.id, err)
		}
		if status != "DEACTIVATED" {
			t.Errorf("the real-domain planted row %s carries status %s, want DEACTIVATED", r.id, status)
		}
		if display != r.name {
			t.Errorf("the real-domain planted row %s carries display name %q, want %q", r.id, display, r.name)
		}
		if gotEmail != r.email {
			t.Errorf("the real-domain planted row %s carries email %q, want %q", r.id, gotEmail, r.email)
		}
		if hash != nil {
			t.Errorf("the planted row %s still carries a password hash", r.id)
		}
	}

	// Every users row renders an invented name: the thirteen display names
	// this populate and the migrations' planted set allow, exactly, and
	// nothing else. A surname token or an unreplaced placeholder on any row
	// fails the allowlist, without the test itself naming a token.
	allowedNames := map[string]bool{
		salesDisplayName:                true,
		managerDisplayName:              true,
		payRecDisplayName:               true,
		controllerDisplayName:           true,
		"Platform admin (demo)":         true,
		"Demo teammate (demo)":          true,
		"Homework account one (demo)":   true,
		"Homework account two (demo)":   true,
		"Homework account three (demo)": true,
		"Homework account four (demo)":  true,
		"Counter UAT account (demo)":    true,
		"Dispatch persona (demo)":       true,
		"Yard persona (demo)":           true,
		"Driver persona (demo)":         true,
	}
	nameRows, err := pool.Query(ctx, `SELECT display_name FROM users`)
	if err != nil {
		t.Fatal(err)
	}
	defer nameRows.Close()
	saw := map[string]int{}
	for nameRows.Next() {
		var name string
		if err := nameRows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		saw[name]++
		if !allowedNames[name] {
			t.Errorf("a users row carries display name %q, which is not one of the invented demo names", name)
		}
	}
	if err := nameRows.Err(); err != nil {
		t.Fatal(err)
	}
	// 14 since the merge of PR 300: the two Counter Till accounts, the planted
	// demo teammates, and both roadmap personas (Payables and Receivables and
	// the Financial Controller), each one invented name.
	if len(saw) != len(allowedNames) || len(saw) != 14 {
		t.Errorf("the users rows render %d distinct display names, want exactly the 14 invented ones (saw: %v)", len(saw), saw)
	}

	// The receipt's letterhead row: system_settings org_name carries the
	// invented demo name exactly. The migrations seed this row with the
	// real dealer's trading name (0014) and the printed receipt reads it
	// verbatim (printing's ReadBranchHeader), so a demo database whose
	// settings row was never converged would print that name on every
	// receipt while every screen shows the invented one.
	var orgSetting string
	if err := pool.QueryRow(ctx,
		`SELECT value #>> '{}' FROM system_settings WHERE key = 'org_name'`).Scan(&orgSetting); err != nil {
		t.Fatalf("system_settings org_name is missing: %v", err)
	}
	if orgSetting != demoOrgName {
		t.Errorf("system_settings org_name carries %q, want the invented demo name %q", orgSetting, demoOrgName)
	}

	// No row of the demo database carries a credential: the planted
	// password hashes are cleared (the core has no password route) and the
	// two role accounts are inserted without one.
	var credentialed int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE password_hash IS NOT NULL`).Scan(&credentialed); err != nil {
		t.Fatal(err)
	}
	if credentialed != 0 {
		t.Errorf("%d users row(s) still carry a password hash", credentialed)
	}

	// The card rail answers configured with the demo's invented merchant id,
	// through the payment store read the way W3 owns it. (The serve leg of
	// this check, the route behind the boot guards, is W1's to run.)
	vault, err := payment.NewVault(testVaultKey)
	if err != nil {
		t.Fatal(err)
	}
	configured, merchantID, processor, currency, err := payment.NewStore(pool, vault).Status(ctx)
	if err != nil {
		t.Fatalf("payment status: %v", err)
	}
	if !configured || merchantID != demoMerchantID || processor != demoProcessor || currency != demoCurrency {
		t.Errorf("payment config: configured=%v merchant=%q processor=%q currency=%q", configured, merchantID, processor, currency)
	}

	// The demo_box row records the run's moment, and the newest seeded
	// counter sale falls on the same run's day. The day compare is the
	// seed's own frame, UTC: the seed dates its world from the UTC instant
	// it is given and the throwaway box's clock reads UTC, so the day
	// boundary here is the one the seed itself draws.
	var populatedAt time.Time
	var marker string
	if err := pool.QueryRow(ctx,
		`SELECT populated_at, marker FROM demo_box WHERE singleton = 'demo'`).Scan(&populatedAt, &marker); err != nil {
		t.Fatalf("the demo_box row is missing: %v", err)
	}
	if !populatedAt.Equal(seedClock) || marker != demoBoxMarker {
		t.Errorf("demo_box records %s with marker %q, want the run's clock %s and marker %q",
			populatedAt.Format(time.RFC3339), marker, seedClock.Format(time.RFC3339), demoBoxMarker)
	}
	var newestSale time.Time
	if err := pool.QueryRow(ctx, `
		SELECT max(ps.created_at) FROM pos_sale ps
		  JOIN orders o ON o.id = ps.order_id
		 WHERE o.metadata->>'demo_seed' = 'hos-demoseed-v1'`).Scan(&newestSale); err != nil {
		t.Fatal(err)
	}
	if newestSale.IsZero() {
		t.Fatal("no seeded counter sale exists, the day assertion would be vacuous")
	}
	if !newestSale.UTC().Truncate(24 * time.Hour).Equal(seedClock.Truncate(24 * time.Hour)) {
		t.Errorf("the newest seeded counter sale falls on %s, want the run's day %s",
			newestSale.UTC().Format(time.RFC3339), seedClock.Format(time.RFC3339))
	}
}

func assertRoleAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID, email, display, roleCode string) {
	t.Helper()
	var status string
	var platformID *string
	if err := pool.QueryRow(ctx,
		`SELECT status, platform_user_id::text FROM users WHERE id = $1::uuid`, userID).Scan(&status, &platformID); err != nil {
		t.Fatalf("role account %s missing: %v", email, err)
	}
	if status != "ACTIVE" {
		t.Errorf("role account %s carries status %s, want ACTIVE", email, status)
	}
	if platformID == nil || *platformID != userID {
		t.Errorf("role account %s carries platform id %v, want %s", email, platformID, userID)
	}
	var gotDisplay string
	if err := pool.QueryRow(ctx,
		`SELECT display_name FROM users WHERE id = $1::uuid`, userID).Scan(&gotDisplay); err != nil {
		t.Fatal(err)
	}
	if gotDisplay != display {
		t.Errorf("role account %s carries display name %q, want %q", email, gotDisplay, display)
	}
	var gotEmail string
	if err := pool.QueryRow(ctx,
		`SELECT email::text FROM users WHERE id = $1::uuid`, userID).Scan(&gotEmail); err != nil {
		t.Fatal(err)
	}
	if gotEmail != email {
		t.Errorf("role account id %s carries email %q, want %q", userID, gotEmail, email)
	}
	// The grant resolves: one live staff_role_grant, the account's own role,
	// in the demo org, branch-scoped to Trenton (a global role's grant carries
	// no branch: Payables and Receivables and the Financial Controller reach every branch).
	var gotRole string
	var gotBranch *string
	err := pool.QueryRow(ctx, `
		SELECT sr.origin_code::text, g.branch_id::text
		  FROM staff_role_grant g
		  JOIN staff_role sr ON sr.id = g.role_id
		 WHERE g.platform_user_id = $1 AND g.deleted_at IS NULL`,
		userID).Scan(&gotRole, &gotBranch)
	if err != nil {
		t.Fatalf("role account %s resolves no staff grant: %v", email, err)
	}
	global := roleCode == "PAYABLES_RECEIVABLES" || roleCode == "FINANCIAL_CONTROLLER"
	if gotRole != roleCode || (global && gotBranch != nil) || (!global && (gotBranch == nil || *gotBranch != trentonBranchID)) {
		t.Errorf("role account %s resolves role %s at branch %v, want %s at %s", email, gotRole, gotBranch, roleCode, trentonBranchID)
	}
}

// TestPopulate_ReceiptHeaderCarriesTheInventedName: the printed receipt's
// letterhead is the document that reads system_settings org_name verbatim
// (printing's ReadBranchHeader), the row the migrations seed with the real
// dealer's trading name (0014). Over the fully populated world the header's
// org field is the invented demo name and its branch field the invented
// Trenton name, so a demo box prints no receipt headed by the seeded
// trading name. This is the ReadBranchHeader leg, not a served-receipt leg:
// proving the letterhead needs no whole sale first.
func TestPopulate_ReceiptHeaderCarriesTheInventedName(t *testing.T) {
	ctx := context.Background()
	pool, dsn := freshMigratedDB(t, "receipt")
	bin := buildDemoCore(t)
	seedAndPopulate(t, bin, dsn)

	header, err := printing.NewRepository(pool).ReadBranchHeader(ctx, trentonBranchID)
	if err != nil {
		t.Fatalf("ReadBranchHeader over the populated world: %v", err)
	}
	if header.OrgName != demoOrgName {
		t.Errorf("the receipt header's org field carries %q, want the invented demo name %q", header.OrgName, demoOrgName)
	}
	if header.Name != "Hardscape House (demo), Trenton" {
		t.Errorf("the receipt header's branch field carries %q, want the invented branch name %q", header.Name, "Hardscape House (demo), Trenton")
	}
}

// -- The refusals ----------------------------------------------------------------------------------

// TestPopulate_RefusesASecondPaymentConfiguration: a second payment
// configuration present, the populate refuses and the world it found is
// exactly the world it left.
func TestPopulate_RefusesASecondPaymentConfiguration(t *testing.T) {
	ctx := context.Background()
	pool, dsn := freshMigratedDB(t, "payrefuse")
	bin := buildDemoCore(t)
	seedAndPopulate(t, bin, dsn)

	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_config (id, processor, merchant_id, currency, credentials_ciphertext,
		                            key_version, base_url, mdr_bps, surcharge_enabled, status)
		VALUES ('de110000-0000-4000-9000-0000000000cb', 'other_demo_rail', 'OTHER-MERCHANT', 'CAD',
		        '\x00'::bytea, 1, '', 0, FALSE, 'active')`); err != nil {
		t.Fatal(err)
	}

	before := snapshotWorld(t, ctx, pool, worldTables)
	_, err := RunPopulate(ctx, runPopulateOptions(dsn))
	if err == nil {
		t.Fatal("the populate ran against a database holding a second payment configuration, it must refuse")
	}
	if !strings.Contains(err.Error(), "payment configuration row(s) the demo populate did not write") {
		t.Errorf("the refusal does not name the second payment configuration: %v", err)
	}
	after := snapshotWorld(t, ctx, pool, worldTables)
	assertWorldUnchanged(t, before, after)
}

// TestPopulate_RefusesADriftedPaymentConfiguration: the populate's own
// payment row is known by its content as well as its id. A row with the
// right id and the wrong merchant, or a host where the demo wants none, is
// a refusal, not a run that reports the world complete.
func TestPopulate_RefusesADriftedPaymentConfiguration(t *testing.T) {
	ctx := context.Background()
	pool, dsn := freshMigratedDB(t, "paydrift")
	bin := buildDemoCore(t)
	seedAndPopulate(t, bin, dsn)

	// The drift the reviewer probed: the same row, another merchant, a
	// host where the demo writes none.
	if _, err := pool.Exec(ctx, `
		UPDATE payment_config SET merchant_id = 'OTHER-MERCHANT', base_url = 'https://drift.example'
		 WHERE id = $1::uuid`, demoPaymentConfigID); err != nil {
		t.Fatal(err)
	}

	before := snapshotWorld(t, ctx, pool, worldTables)
	_, err := RunPopulate(ctx, runPopulateOptions(dsn))
	if err == nil {
		t.Fatal("the populate ran against a drifted payment configuration, it must refuse")
	}
	if !strings.Contains(err.Error(), "has drifted from the values this populate writes") {
		t.Errorf("the refusal does not name the drift: %v", err)
	}
	after := snapshotWorld(t, ctx, pool, worldTables)
	assertWorldUnchanged(t, before, after)
}

// TestPopulate_RefusesAForeignUser: one user outside the planted set, the
// populate refuses. The planted set is the pinned list, so a real
// database's rows can never be admitted.
func TestPopulate_RefusesAForeignUser(t *testing.T) {
	ctx := context.Background()
	pool, dsn := freshMigratedDB(t, "userrefuse")

	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, display_name, status)
		VALUES ('11111111-2222-4333-8444-555555555555', 'owner@realyard.ca', 'A real person', 'ACTIVE')`); err != nil {
		t.Fatal(err)
	}

	before := snapshotWorld(t, ctx, pool, migratedWorldTables)
	_, err := RunPopulate(ctx, runPopulateOptions(dsn))
	if err == nil {
		t.Fatal("the populate ran against a database holding a foreign user, it must refuse")
	}
	if !strings.Contains(err.Error(), "outside the migrations' planted set") {
		t.Errorf("the refusal does not name the planted-set check: %v", err)
	}
	after := snapshotWorld(t, ctx, pool, migratedWorldTables)
	assertWorldUnchanged(t, before, after)
}

// TestPopulate_RefusesAForeignOrg: one org that is not hh-demo, the
// populate refuses.
func TestPopulate_RefusesAForeignOrg(t *testing.T) {
	ctx := context.Background()
	pool, dsn := freshMigratedDB(t, "orgrefuse")

	if _, err := pool.Exec(ctx, `
		INSERT INTO org (slug, name) VALUES ('other-yard', 'Some Other Dealer')`); err != nil {
		t.Fatal(err)
	}

	before := snapshotWorld(t, ctx, pool, migratedWorldTables)
	_, err := RunPopulate(ctx, runPopulateOptions(dsn))
	if err == nil {
		t.Fatal("the populate ran against a database holding another org, it must refuse")
	}
	if !strings.Contains(err.Error(), "org row(s) other than hh-demo") {
		t.Errorf("the refusal does not name the org check: %v", err)
	}
	after := snapshotWorld(t, ctx, pool, migratedWorldTables)
	assertWorldUnchanged(t, before, after)
}

// TestPopulate_RefusesAMissingRoleCatalogRow: the role and branch inserts
// are INSERT ... SELECT, which write nothing without an error when the
// catalog lacks the role, so the populate verifies the live rows and
// refuses a demo world whose role accounts would reach nothing. The branch
// leg rides the same verify-and-refuse path; a clean mutation of the
// Trenton row on a migrated database is impossible (its id is behind a web
// of restricting foreign keys), so the catalog leg carries the proof.
func TestPopulate_RefusesAMissingRoleCatalogRow(t *testing.T) {
	ctx := context.Background()
	pool, dsn := freshMigratedDB(t, "rolerefuse")

	if _, err := pool.Exec(ctx, `DELETE FROM roles WHERE code = 'SALES_MANAGER'`); err != nil {
		t.Fatal(err)
	}

	before := snapshotWorld(t, ctx, pool, migratedWorldTables)
	_, err := RunPopulate(ctx, runPopulateOptions(dsn))
	if err == nil {
		t.Fatal("the populate ran against a catalog missing SALES_MANAGER, it must refuse")
	}
	if !strings.Contains(err.Error(), "roles catalog holds no SALES_MANAGER") {
		t.Errorf("the refusal does not name the missing role: %v", err)
	}
	after := snapshotWorld(t, ctx, pool, migratedWorldTables)
	assertWorldUnchanged(t, before, after)
}

// TestPopulate_RefusesEnvironmentMistakes, with no database at all: the
// two database variables must be set and equal, and the vault key must be
// a real key shape, before the populate will even connect.
func TestPopulate_RefusesEnvironmentMistakes(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name    string
		getenv  func(string) string
		wantExt string
	}{
		{
			name:    "both variables unset",
			getenv:  func(string) string { return "" },
			wantExt: "DATABASE_URL",
		},
		{
			name: "the variables name different databases",
			getenv: func(k string) string {
				if k == "DATABASE_URL" {
					return "postgres://hardscape:hardscape@127.0.0.1:5591/one?sslmode=disable"
				}
				if k == "CORE_DATABASE_URL" {
					return "postgres://hardscape:hardscape@127.0.0.1:5591/two?sslmode=disable"
				}
				return testVaultKey
			},
			wantExt: "different databases",
		},
		{
			name: "the vault key is missing",
			getenv: func(k string) string {
				if k == "PAYMENT_VAULT_KEY" {
					return ""
				}
				return "postgres://hardscape:hardscape@127.0.0.1:5591/one?sslmode=disable"
			},
			wantExt: "PAYMENT_VAULT_KEY",
		},
		{
			name: "the vault key is malformed",
			getenv: func(k string) string {
				if k == "PAYMENT_VAULT_KEY" {
					return "not-hex"
				}
				return "postgres://hardscape:hardscape@127.0.0.1:5591/one?sslmode=disable"
			},
			wantExt: "PAYMENT_VAULT_KEY",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := RunPopulate(ctx, Options{Getenv: tc.getenv, Out: io.Discard})
			if err == nil {
				t.Fatal("the populate ran with a broken environment, it must refuse")
			}
			if !strings.Contains(err.Error(), tc.wantExt) {
				t.Errorf("the refusal does not name %q: %v", tc.wantExt, err)
			}
		})
	}
}

// Every person's login that migration 0364 rewrites in the default build carries exactly the label the
// demo populate writes for the same row, so a default build and a demo build read the same. The pairs are
// read from the migration file (a users UPDATE with its fixed id and the new label), the labels from
// plantedDisplayNames.
func TestMigration0364LabelsEqualThePopulatesLabels(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "0364_tour_dispatch_persona_surname_goes.sql"))
	if err != nil {
		t.Fatalf("reading 0364: %v", err)
	}
	re := regexp.MustCompile(`(?s)UPDATE users\s+SET display_name = '([^']+)'.*?WHERE id = '([0-9a-f-]{36})'::uuid`)
	pairs := re.FindAllStringSubmatch(string(body), -1)
	if len(pairs) != 3 {
		t.Fatalf("0364 rewrites %d logins, want 3 (dispatch, yard, demo teammate): the leg is not testing what it says", len(pairs))
	}
	want := map[string]string{}
	for _, p := range plantedDisplayNames {
		want[p.id] = p.name
	}
	for _, p := range pairs {
		label, id := p[1], p[2]
		if got, ok := want[id]; !ok || got != label {
			t.Errorf("0364 labels login %s %q, the populate labels it %q (renamed: %v)", id, label, got, ok)
		}
	}
}
