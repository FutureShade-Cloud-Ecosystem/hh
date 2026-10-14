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
