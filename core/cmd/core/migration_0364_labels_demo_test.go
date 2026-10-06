//go:build demo

package main

// migration_0364_labels_demo_test.go pins label equality between migration 0364 and the demo build's own
// rename for the rows both touch, so a default build and a demo build read the same. Of the rows 0364
// rewrites, three logins have a counterpart in the demo populate (pinned in internal/demo, by id) and the
// demo DM title (a6...d1) has one in scrubTextRenames, pinned here. The three fleet drivers (0093) and the
// two staff DM titles (0052) have no counterpart anywhere in the populate or the demo seed (their ids occur
// in no Go file), so there is nothing to equal and they are not pinned.

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestMigration0364DMTitleEqualsTheDemoSeedsRename(t *testing.T) {
	const dmID = "a6000000-0000-4000-9000-0000000000d1"
	body, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "0364_tour_dispatch_persona_surname_goes.sql"))
	if err != nil {
		t.Fatalf("reading 0364: %v", err)
	}
	m := regexp.MustCompile(`(?s)UPDATE msg_channels\s+SET name = '([^']+)'.*?WHERE id = '` + dmID + `'::uuid`).FindStringSubmatch(string(body))
	if m == nil {
		t.Fatalf("0364 has no UPDATE of the demo DM title %s: the leg is not testing what it says", dmID)
	}
	var seen int
	for _, r := range scrubTextRenames {
		if r.id != dmID {
			continue
		}
		seen++
		if r.toText != m[1] {
			t.Errorf("0364 titles the demo DM %q, the demo seed renames it %q", m[1], r.toText)
		}
	}
	if seen != 1 {
		t.Fatalf("scrubTextRenames carries %d rows for the demo DM, want 1: the leg is not testing what it says", seen)
	}
}
