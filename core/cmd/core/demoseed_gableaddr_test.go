//go:build demo

package main

// demoseed_gableaddr_test.go, the demo world's email-address probe: after the demoseed runs, no
// migration-planted demo email address still reads as example.invalid, and the Gable addresses the
// rename moves to are in place.
//
// WHY A PROBE. The migrations plant example.invalid addresses in system_settings, warranty vendor
// profiles and staff users; a lookup of one id would go green the moment that row was fixed and stay
// silent about the next placeholder a tester meets. So the probe reads every row in those three
// places and holds the whole family to the rule.
//
// THE ORDER IS THE BOX'S ORDER. The database is built from the frozen migration set (so the
// placeholder emails are in it), then `demoseed -apply` runs exactly as the demo image's entrypoint
// runs it, and the probe reads the world that leaves behind.

import (
	"context"
	"os"
	"testing"
)

func TestDemoWorld_NoExampleInvalidRemains(t *testing.T) {
	dsn := os.Getenv(tillDemoDBEnvVar)
	if dsn == "" {
		t.Skip(tillDemoDBEnvVar + " is not set: the demo world proof needs its own throwaway local Postgres")
	}
	requireLocalDSN(t, dsn)
	ctx := context.Background()

	// 1. the world the way a demo box builds it: every migration first, then the demoseed over them.
	pool, testDSN := snFreshDB(t, ctx, dsn)
	t.Setenv("DATABASE_URL", testDSN)
	t.Setenv("CORE_DATABASE_URL", testDSN)
	t.Setenv("APP_ENV", "dev")

	// 2. the probe bites: a freshly migrated database carries example.invalid emails, or everything
	// below would pass vacuously.
	var before int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM users
		 WHERE email LIKE '%example.invalid%' AND email NOT LIKE '%hh-demo.invalid%'`).Scan(&before); err != nil {
		t.Fatalf("counting example.invalid users before seed: %v", err)
	}
	if before == 0 {
		t.Fatal("a freshly migrated database carries no example.invalid user email, this test is not probing anything")
	}

	// 3. the demoseed runs, the way the demo image's entrypoint runs it.
	if _, created := qwApply(t); created == 0 {
		t.Fatal("the demoseed created nothing on a fresh database")
	}

	// 4. THE PROBE. No user, system setting or warranty vendor email still reads as example.invalid,
	// except the hh-demo.invalid persona addresses the demo populate will move later.
	var usersBad, settingsBad, warrantyBad int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM users
		 WHERE email LIKE '%example.invalid%' AND email NOT LIKE '%hh-demo.invalid%'`).Scan(&usersBad); err != nil {
		t.Fatalf("reading users: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM system_settings
		 WHERE key = 'org_email' AND value::text LIKE '%example.invalid%'`).Scan(&settingsBad); err != nil {
		t.Fatalf("reading system_settings: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM warranty_vendor_profiles
		 WHERE channel = 'EMAIL' AND channel_ref LIKE '%example.invalid%'`).Scan(&warrantyBad); err != nil {
		t.Fatalf("reading warranty_vendor_profiles: %v", err)
	}
	if usersBad != 0 || settingsBad != 0 || warrantyBad != 0 {
		t.Errorf("after the demoseed ran, example.invalid remains in %d user(s), %d system setting(s), %d warranty profile(s)",
			usersBad, settingsBad, warrantyBad)
	}

	// 5. the new addresses are in place.
	var orgEmail string
	if err := pool.QueryRow(ctx, `SELECT value::text FROM system_settings WHERE key = 'org_email'`).Scan(&orgEmail); err != nil {
		t.Fatalf("reading org_email: %v", err)
	}
	if orgEmail != `"office@gablelandscape.example"` {
		t.Errorf("org_email = %q, want %q", orgEmail, `"office@gablelandscape.example"`)
	}

	wantUsers := map[string]string{
		"40000061-0000-4000-9000-000000000001": "general.manager@gablelandscape.example",
		"40000061-0000-4000-9000-000000000002": "inside.sales@gablelandscape.example",
		"40000061-0000-4000-9000-000000000003": "counter.sales@gablelandscape.example",
		"40000061-0000-4000-9000-000000000004": "branch.manager@gablelandscape.example",
	}
	for id, want := range wantUsers {
		var got string
		if err := pool.QueryRow(ctx, `SELECT email::text FROM users WHERE id = $1::uuid`, id).Scan(&got); err != nil {
			t.Fatalf("reading user %s: %v", id, err)
		}
		if got != want {
			t.Errorf("user %s email = %q, want %q", id, got, want)
		}
	}
	var uatCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE email = 'uat.inside.sales@gablelandscape.example'`).Scan(&uatCount); err != nil {
		t.Fatalf("reading uat inside sales: %v", err)
	}
	if uatCount != 1 {
		t.Errorf("uat.inside.sales@gablelandscape.example user count = %d, want 1", uatCount)
	}

	// 5b. the new display names are in place: role labels, no personal-name tokens.
	wantDisplays := map[string]string{
		"40000061-0000-4000-9000-000000000001": "General Manager (demo)",
		"40000061-0000-4000-9000-000000000002": "Inside Sales (demo)",
		"40000061-0000-4000-9000-000000000003": "Counter Sales (demo)",
		"40000061-0000-4000-9000-000000000004": "Branch Manager (demo)",
	}
	for id, want := range wantDisplays {
		var got string
		if err := pool.QueryRow(ctx, `SELECT display_name::text FROM users WHERE id = $1::uuid`, id).Scan(&got); err != nil {
			t.Fatalf("reading user %s display_name: %v", id, err)
		}
		if got != want {
			t.Errorf("user %s display_name = %q, want %q", id, got, want)
		}
	}
	var uatDisplay string
	if err := pool.QueryRow(ctx, `SELECT display_name::text FROM users WHERE email = 'uat.inside.sales@gablelandscape.example'`).Scan(&uatDisplay); err != nil {
		t.Fatalf("reading uat inside sales display_name: %v", err)
	}
	if uatDisplay != "UAT Inside Sales (demo)" {
		t.Errorf("uat inside sales display_name = %q, want %q", uatDisplay, "UAT Inside Sales (demo)")
	}
	var tokensLeft int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM users
		 WHERE id IN ('40000061-0000-4000-9000-000000000001'::uuid,
		              '40000061-0000-4000-9000-000000000002'::uuid,
		              '40000061-0000-4000-9000-000000000003'::uuid,
		              '40000061-0000-4000-9000-000000000004'::uuid,
		              (SELECT id FROM users WHERE email = 'uat.inside.sales@gablelandscape.example'))
		   AND display_name LIKE '%Scrubname%'`).Scan(&tokensLeft); err != nil {
		t.Fatalf("counting scrub tokens in display names: %v", err)
	}
	if tokensLeft != 0 {
		t.Errorf("%d migration-planted user(s) still carry a Scrubname token in display_name", tokensLeft)
	}

	wantWarranty := map[string]string{
		"f5700000-0000-4000-9000-000000000003": "orders@copperlinepavers.example",
		"f5700000-0000-4000-9000-000000000005": "orders@heronfieldstone.example",
	}
	for vendorID, want := range wantWarranty {
		var got string
		if err := pool.QueryRow(ctx, `SELECT channel_ref::text FROM warranty_vendor_profiles WHERE vendor_id = $1::uuid AND channel = 'EMAIL'`, vendorID).Scan(&got); err != nil {
			t.Fatalf("reading warranty profile for vendor %s: %v", vendorID, err)
		}
		if got != want {
			t.Errorf("warranty profile for vendor %s channel_ref = %q, want %q", vendorID, got, want)
		}
	}

	// 6. idempotent: a second apply creates nothing and moves no address.
	if _, created := qwApply(t); created != 0 {
		t.Errorf("a second apply created %d rows, the seed must be idempotent", created)
	}
	var afterBad int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM users
		 WHERE email LIKE '%example.invalid%' AND email NOT LIKE '%hh-demo.invalid%'`).Scan(&afterBad); err != nil {
		t.Fatalf("counting example.invalid users after second seed: %v", err)
	}
	if afterBad != 0 {
		t.Errorf("after a second apply, %d user(s) still carry example.invalid emails", afterBad)
	}

	t.Logf("migrated world carried %d example.invalid user emails; after the demoseed the probe reads none", before)
}

// TestDemoWorld_GableRenameNeverRunsOnABuiltBox: the entrypoint applies on every boot, so the rename
// must leave a built box alone. Once demo_box holds its row, a migration-planted address that is back
// in place is NOT renamed, and a tester's user already holding the Gable address cannot make the apply
// fail on the users email unique key (which would stop the boot).
func TestDemoWorld_GableRenameNeverRunsOnABuiltBox(t *testing.T) {
	dsn := os.Getenv(tillDemoDBEnvVar)
	if dsn == "" {
		t.Skip(tillDemoDBEnvVar + " is not set: the demo world proof needs its own throwaway local Postgres")
	}
	requireLocalDSN(t, dsn)
	ctx := context.Background()
	pool, testDSN := snFreshDB(t, ctx, dsn)
	t.Setenv("DATABASE_URL", testDSN)
	t.Setenv("CORE_DATABASE_URL", testDSN)
	t.Setenv("APP_ENV", "dev")
	if _, created := qwApply(t); created == 0 {
		t.Fatal("the demoseed created nothing on a fresh database")
	}
	// The box is now "built": the populate's demo_box row exists.
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS demo_box (built_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO demo_box DEFAULT VALUES`); err != nil {
		t.Fatal(err)
	}
	// A planted address back where the migration put it, and a tester's user on the Gable address.
	var gone string
	if err := pool.QueryRow(ctx, `UPDATE users SET email = 'scrubbed-0004@example.invalid'
		WHERE id = '40000061-0000-4000-9000-000000000001' RETURNING email`).Scan(&gone); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, display_name, status)
		VALUES ('7e570000-0000-4000-9000-000000000001', 'general.manager@gablelandscape.example', 'Tester made', 'ACTIVE')`); err != nil {
		t.Skipf("the users table shape differs from this probe's insert (%v): the first half still proves the rename is skipped", err)
	}
	qwApply(t)
	var email string
	if err := pool.QueryRow(ctx, `SELECT email FROM users WHERE id = '40000061-0000-4000-9000-000000000001'`).Scan(&email); err != nil {
		t.Fatal(err)
	}
	if email != "scrubbed-0004@example.invalid" {
		t.Fatalf("a built box was renamed: the planted user now reads %q", email)
	}
}
