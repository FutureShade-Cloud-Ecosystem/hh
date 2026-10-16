package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
)

// G1: the production build has no demo code. The demo build compiles its
// pieces from core/internal/demo/ and demo_on.go under -tags demo; this
// test holds the default build's dependency closure free of all of it, so
// a demo import that escapes the tag fails every plain go test run. The
// second half proves the closure actually responds to the tag (under
// -tags demo the demo packages appear), so the first half cannot pass
// vacuously: if the demo code ever leaked outside the tagged files, both
// halves would go red together.
func TestDefaultBuildDependencyClosureCarriesNoDemoPackages(t *testing.T) {
	defaultClosure := goListDeps(t, "")
	for _, pkg := range defaultClosure {
		if strings.Contains(pkg, "/internal/demo") {
			t.Fatalf("the default build's dependency closure carries demo code: %s", pkg)
		}
	}
	demoClosure := goListDeps(t, "-tags=demo")
	found := false
	for _, pkg := range demoClosure {
		if strings.Contains(pkg, "/internal/demo") {
			found = true
		}
	}
	if !found {
		t.Fatal("even the demo build's dependency closure shows no /internal/demo package, so the guard would never fire")
	}
}

func goListDeps(t *testing.T, extra string) []string {
	t.Helper()
	args := []string{"list"}
	if extra != "" {
		args = append(args, extra)
	}
	args = append(args, "-deps", ".")
	cmd := exec.Command("go", args...)
	cmd.Dir = "."
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go %s: %v", strings.Join(args, " "), err)
	}
	return strings.Fields(string(out))
}

// G1, second leg (the persona entry rides it too): the demo pair stays on the not-yet-registered ledger
// and stays documented, so the default build's registration parity keeps
// accounting for the two operations the demo module registers (in the
// demo build only). The parity test beside this file fails the moment the
// pair registers without a ledger removal, and the closure test above
// fails the moment any demo package could register it here; between them
// demoCreateSession cannot quietly become a default-build surface.
func TestLedgerKeepsTheDemoPair(t *testing.T) {
	for _, op := range []string{"demoCreateSession", "demoListFlows", "identityPersona"} {
		reason, ok := notYetRegistered[op]
		if !ok {
			t.Fatalf("%s left the not-yet-registered ledger without landing in the default build", op)
		}
		if reason == "" {
			t.Fatalf("%s carries an empty ledger reason", op)
		}
	}
	for _, op := range []string{"demoCreateSession", "demoListFlows", "identityPersona"} {
		documented := false
		for _, r := range apigen.Routes {
			if r.OperationID == op {
				documented = true
			}
		}
		if !documented {
			t.Fatalf("%s is on the ledger but no longer in the contract", op)
		}
	}
}
