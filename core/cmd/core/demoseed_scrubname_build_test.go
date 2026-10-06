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
