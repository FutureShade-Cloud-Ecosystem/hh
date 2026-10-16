package main

// demoseed_scrubname_build_test.go, the build gate's proof: the scrub rename is the demo
// build's write, and the DEFAULT build of this binary carries no trace of it. The PR 205
// review's P2 ruled the untagged rename not a gate (demoseed dispatches in every build,
// so a default binary could run it); this test holds the fixed shape the same way the
// till's no-default-adopt test (web/apps/counter-till/test/no-default-adopt.test.mjs)
// holds the front door's seam, because the rule is about what SHIPS, not what the
// sources happen to say: a rename compiled into every build is runnable in every build,
// and the pair of files this test guards (demoseed_scrubname_demo.go with //go:build
// demo, demoseed_scrubname_default.go with //go:build !demo) is what keeps it out.
//
// The markers are the rename's own literals, each carried by nothing else in the
// dependency closure (the migrations that plant the placeholder names are read from disk
// at run time, and the one embedded SQL file, dibbits_seed.sql, carries none of them):
//
//   - the UPDATE statement itself, the only write of customers.name by fixed id and
//     placeholder pair anywhere in the tree;
//   - the traced island's placeholder name, the string the principal's look met on the
//     Quote Desk's list;
//   - one of the to-names, the rename's other half.
//
// Both directions are asserted so neither can pass vacuously: the default build must
// carry none of the three, and the demo build must carry all three, or this test has
// stopped probing the thing it exists to hold.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// scrubRenameSignatures are the rename pass's load-bearing literals. Nothing else a
// default build compiles carries any of them: the placeholder names enter databases
// through the migrations, which the migrate role reads from disk at run time, not
// through the binary.
var scrubRenameSignatures = []string{
	`UPDATE customers SET name = $1, updated_at = $2 WHERE id = $3::uuid AND name = $4`,
	"Scrubbed Party 0004",
	"Riverside Walkway Contracting (demo)",
	"Scrubname0014 Scrubname0027",
	"Platform admin (demo), Demo teammate (demo)",
}

// buildCoreBinary builds this package the named way into its own scratch file and
// returns the bytes it produced. The default way carries no tags (the same plain build a
// production binary rides); the demo way passes the tag the demo image's build carries.
func buildCoreBinary(t *testing.T, name, tags string) []byte {
	t.Helper()
	bin := filepath.Join(t.TempDir(), name)
	args := []string{"build"}
	if tags != "" {
		args = append(args, tags)
	}
	args = append(args, "-o", bin, ".")
	cmd := exec.Command("go", args...)
	cmd.Dir = "."
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	body, err := os.ReadFile(bin)
	if err != nil {
		t.Fatalf("reading the built binary: %v", err)
	}
	return body
}

func TestDefaultBuildCarriesNoScrubRename(t *testing.T) {
	def := buildCoreBinary(t, "core-default", "")
	for _, sig := range scrubRenameSignatures {
		if bytes.Contains(def, []byte(sig)) {
			t.Errorf("the default build carries the scrub rename's %q; the rename must compile only into the demo build", sig)
		}
	}
	demo := buildCoreBinary(t, "core-demo", "-tags=demo")
	for _, sig := range scrubRenameSignatures {
		if !bytes.Contains(demo, []byte(sig)) {
			t.Errorf("the demo build has lost the scrub rename's %q, so the default leg above passes vacuously", sig)
		}
	}
}

// homeRepairSignatures are the seed-repair pass's load-bearing literals (the demoseed_repair
// pair): the one guarded home-branch UPDATE, and the plan's own words about it. The customer id
// the statement addresses is derived (demoID), so the statement text is the marker that cannot
// appear in a default build by accident.
var homeRepairSignatures = []string{
	`UPDATE customers SET home_branch_id = $1::uuid, updated_at = $2 WHERE id = $3::uuid AND home_branch_id = $4::uuid`,
	"re-homing the Northline account",
}

// TestDefaultBuildCarriesNoHomeBranchRepair holds the repair pair's gate the same way the rename
// leg above holds its own: the repair is the demo build's write, and a default build must carry
// no trace of it, while the demo build must carry both markers or this test has stopped probing.
func TestDefaultBuildCarriesNoHomeBranchRepair(t *testing.T) {
	def := buildCoreBinary(t, "core-default-repair", "")
	for _, sig := range homeRepairSignatures {
		if bytes.Contains(def, []byte(sig)) {
			t.Errorf("the default build carries the home-branch repair's %q; the repair must compile only into the demo build", sig)
		}
	}
	demo := buildCoreBinary(t, "core-demo-repair", "-tags=demo")
	for _, sig := range homeRepairSignatures {
		if !bytes.Contains(demo, []byte(sig)) {
			t.Errorf("the demo build has lost the home-branch repair's %q, so the default leg above passes vacuously", sig)
		}
	}
}

// productApplicationRepairSignatures are the product-application repair's load-bearing literals:
// the one guarded UPDATE and the plan's own words about it. The statement text is carried by
// nothing a default build compiles.
var productApplicationRepairSignatures = []string{
	`UPDATE products SET application = $1, updated_at = $2 WHERE id = $3::uuid AND (application IS NULL OR application = '')`,
	"get their family with -apply",
}

// TestDefaultBuildCarriesNoProductApplicationRepair holds the same gate for the product repair:
// the default binary carries none of it, and the demo binary carries both markers or this test has
// stopped probing.
func TestDefaultBuildCarriesNoProductApplicationRepair(t *testing.T) {
	def := buildCoreBinary(t, "core-default-prodrepair", "")
	for _, sig := range productApplicationRepairSignatures {
		if bytes.Contains(def, []byte(sig)) {
			t.Errorf("the default build carries the product repair's %q; the repair must compile only into the demo build", sig)
		}
	}
	demo := buildCoreBinary(t, "core-demo-prodrepair", "-tags=demo")
	for _, sig := range productApplicationRepairSignatures {
		if !bytes.Contains(demo, []byte(sig)) {
			t.Errorf("the demo build has lost the product repair's %q, so the default leg above passes vacuously", sig)
		}
	}
}

// driverOrgRepairSignatures are the org stamp repair's load-bearing literals: the two guarded
// UPDATEs and the plan's own words about them. The insert-side stamp (driverOrgStamp in
// demoseed_build.go) is an INSERT-time subselect every build compiles, like every other row the
// shared seed writes; the REPAIR is the demo build's write alone.
var driverOrgRepairSignatures = []string{
	`UPDATE delivery_runs
	   SET org_id = (SELECT id FROM org WHERE slug = $1::citext), updated_at = $2
	 WHERE id = $3::uuid AND org_id IS NULL
	   AND EXISTS (SELECT 1 FROM org WHERE slug = $1::citext)`,
	`UPDATE delivery_stops
	   SET org_id = (SELECT id FROM org WHERE slug = $1::citext), updated_at = $2
	 WHERE id = $3::uuid AND org_id IS NULL
	   AND EXISTS (SELECT 1 FROM org WHERE slug = $1::citext)`,
	"take the demo org with -apply",
}

// TestDefaultBuildCarriesNoDriverOrgRepair holds the same gate for the org stamp repair: the
// default binary carries none of it, and the demo binary carries all three markers or this test
// has stopped probing.
func TestDefaultBuildCarriesNoDriverOrgRepair(t *testing.T) {
	def := buildCoreBinary(t, "core-default-orgrepair", "")
	for _, sig := range driverOrgRepairSignatures {
		if bytes.Contains(def, []byte(sig)) {
			t.Errorf("the default build carries the org stamp repair's %q; the repair must compile only into the demo build", sig)
		}
	}
	demo := buildCoreBinary(t, "core-demo-orgrepair", "-tags=demo")
	for _, sig := range driverOrgRepairSignatures {
		if !bytes.Contains(demo, []byte(sig)) {
			t.Errorf("the demo build has lost the org stamp repair's %q, so the default leg above passes vacuously", sig)
		}
	}
}

// taskSyncRepairSignatures are the task sync repair's load-bearing literals: the one relative,
// locked counter write and the seq derived from what it returned. The repair is the demo build's
// write alone.
var taskSyncRepairSignatures = []string{
	`ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + EXCLUDED.next_value`,
	`SET seq = (SELECT next_value FROM bump) - (SELECT count(*) FROM todo) + todo.n`,
	"the demo %s tasks into the %s sync feed",
}

// TestDefaultBuildCarriesNoTaskSyncRepair holds the same gate for the task sync repair: the
// default binary carries none of it, and the demo binary carries all three markers or this test
// has stopped probing.
func TestDefaultBuildCarriesNoTaskSyncRepair(t *testing.T) {
	def := buildCoreBinary(t, "core-default-taskrepair", "")
	for _, sig := range taskSyncRepairSignatures {
		if bytes.Contains(def, []byte(sig)) {
			t.Errorf("the default build carries the task sync repair's %q; the repair must compile only into the demo build", sig)
		}
	}
	demo := buildCoreBinary(t, "core-demo-taskrepair", "-tags=demo")
	for _, sig := range taskSyncRepairSignatures {
		if !bytes.Contains(demo, []byte(sig)) {
			t.Errorf("the demo build has lost the task sync repair's %q, so the default leg above passes vacuously", sig)
		}
	}
}
