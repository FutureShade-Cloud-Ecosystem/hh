package main

// demoseed_gableaddr_build_test.go, the build gate's proof: the Gable-address rename is the demo
// build's write, and the DEFAULT build of this binary carries no trace of it. The pair of files this
// test guards (demoseed_gableaddr_demo.go with //go:build demo, demoseed_gableaddr_default.go with
// //go:build !demo) is what keeps the rename out of a default binary.
//
// The markers are the rename pass's own literals: the UPDATE statements it runs, the old
// example.invalid addresses it moves from, and the new Gable Landscape Supply addresses it moves to.
// Both directions are asserted so neither can pass vacuously.

import (
	"bytes"
	"testing"
)

// gableAddrSignatures are the rename pass's load-bearing literals. Nothing else a default build
// compiles carries any of them: the placeholder emails enter databases through the migrations, which
// the migrate role reads from disk at run time, not through the binary.
var gableAddrSignatures = []string{
	`UPDATE system_settings SET value = to_jsonb($1::text), updated_at = $2 WHERE key = $3 AND value::text = $4`,
	`UPDATE warranty_vendor_profiles SET channel_ref = $1, updated_at = $2 WHERE vendor_id = $3::uuid AND channel = 'EMAIL' AND channel_ref = $4`,
	`UPDATE users SET email = $1, updated_at = $2 WHERE id = $3::uuid AND email = $4`,
	`UPDATE users SET email = $1, updated_at = $2 WHERE email = $3`,
	`UPDATE users SET display_name = $1, updated_at = $2 WHERE id = $3::uuid AND display_name = $4`,
	`UPDATE users SET display_name = $1, updated_at = $2 WHERE email = 'scrubbed-0009@example.invalid' AND display_name = $3`,
	"scrubbed-0001@example.invalid",
	"scrubbed-0002@example.invalid",
	"scrubbed-0003@example.invalid",
	"scrubbed-0004@example.invalid",
	"scrubbed-0005@example.invalid",
	"scrubbed-0007@example.invalid",
	"scrubbed-0008@example.invalid",
	"scrubbed-0009@example.invalid",
	"office@gablelandscape.example",
	"orders@copperlinepavers.example",
	"orders@heronfieldstone.example",
	"general.manager@gablelandscape.example",
	"inside.sales@gablelandscape.example",
	"counter.sales@gablelandscape.example",
	"branch.manager@gablelandscape.example",
	"uat.inside.sales@gablelandscape.example",
	"General Manager (demo)",
	"Inside Sales (demo)",
	"Counter Sales (demo)",
	"Branch Manager (demo)",
	"UAT Inside Sales (demo)",
	"f5700000-0000-4000-9000-000000000003",
	"f5700000-0000-4000-9000-000000000005",
}

func TestDefaultBuildCarriesNoGableAddressRename(t *testing.T) {
	def := buildCoreBinary(t, "core-default-gableaddr", "")
	for _, sig := range gableAddrSignatures {
		if bytes.Contains(def, []byte(sig)) {
			t.Errorf("the default build carries the Gable-address rename's %q; the rename must compile only into the demo build", sig)
		}
	}
	demo := buildCoreBinary(t, "core-demo-gableaddr", "-tags=demo")
	for _, sig := range gableAddrSignatures {
		if !bytes.Contains(demo, []byte(sig)) {
			t.Errorf("the demo build has lost the Gable-address rename's %q, so the default leg above passes vacuously", sig)
		}
	}
}
