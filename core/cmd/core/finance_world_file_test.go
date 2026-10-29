// finance_world_file_test.go, the world file source's resolution legs,
// without a database: where the reset finds its dump when the environment
// names a file, names the URL, or names neither and the world is expected
// inside the image at /srv/world/.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeWorldCandidates(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("not a real dump"), 0600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
}

func TestFinanceWorldDumpPath(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "absent")
	envOnly := func(pairs ...string) func(string) string {
		return func(k string) string {
			for i := 0; i+1 < len(pairs); i += 2 {
				if pairs[i] == k {
					return pairs[i+1]
				}
			}
			return ""
		}
	}

	// A named file wins: it is returned as it stands, and neither the URL
	// nor the world directory is consulted.
	got, err := financeWorldDumpPath(envOnly(envFinanceWorldFile, "/tmp/the-world.sql.gz", envFinanceWorldURL, "https://storage.example.test/world"), dir)
	if err != nil {
		t.Fatalf("a named file: %v", err)
	}
	if got != "/tmp/the-world.sql.gz" {
		t.Fatalf("a named file answered %q", got)
	}

	// A named URL answers the empty path: the caller fetches over HTTP.
	got, err = financeWorldDumpPath(envOnly(envFinanceWorldURL, "https://storage.example.test/world"), dir)
	if err != nil {
		t.Fatalf("a named URL: %v", err)
	}
	if got != "" {
		t.Fatalf("a named URL answered a file path %q", got)
	}

	// No name at all and an empty world directory: no world is named, and
	// the refusal names the variables.
	if _, err = financeWorldDumpPath(envOnly(), empty); err == nil {
		t.Fatal("no source at all must refuse")
	} else if !strings.Contains(err.Error(), envFinanceWorldFile) || !strings.Contains(err.Error(), envFinanceWorldURL) {
		t.Fatalf("the no-source refusal must name both names, got %v", err)
	}

	// Exactly one candidate is the world.
	writeWorldCandidates(t, dir, "world.sql.gz")
	got, err = financeWorldDumpPath(envOnly(), dir)
	if err != nil {
		t.Fatalf("one candidate: %v", err)
	}
	if got != filepath.Join(dir, "world.sql.gz") {
		t.Fatalf("one candidate answered %q", got)
	}

	// Files that are not the candidate shape are not candidates.
	other := filepath.Join(dir, "other")
	if err := os.MkdirAll(other, 0700); err != nil {
		t.Fatalf("the other directory: %v", err)
	}
	writeWorldCandidates(t, other, "world.sql", "notes.txt", "world.sql.gz")
	got, err = financeWorldDumpPath(envOnly(), other)
	if err != nil {
		t.Fatalf("one candidate among other files: %v", err)
	}
	if got != filepath.Join(other, "world.sql.gz") {
		t.Fatalf("one candidate among other files answered %q", got)
	}

	// More than one candidate refuses, rather than picking one.
	writeWorldCandidates(t, other, "second.sql.gz")
	if _, err = financeWorldDumpPath(envOnly(), other); err == nil {
		t.Fatal("two candidates must refuse")
	} else if !strings.Contains(err.Error(), "one") {
		t.Fatalf("the two candidate refusal must say why, got %v", err)
	}
}
