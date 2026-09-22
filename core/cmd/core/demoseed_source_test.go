// Carried from the pin's cmd/demoseed/source_test.go (the fold: one-shot subcommand).
package main

import (
	"os"
	"strings"
	"testing"
)

// readSource lets a test read this package's own source. TestBuild_UsesNoWallClock is the reason it
// exists: a bare wall-clock read is a defect no output can reveal, so the only place to catch it is
// the text.
func readSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	// Comments are stripped first. Several files in this package DISCUSS the wall clock in prose -
	// explaining why they must not read it, and a scan that could not tell the explanation from the
	// defect would either fail on the documentation or force the documentation to stop saying the
	// thing it exists to say.
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
