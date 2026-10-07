package tablegate

import (
	"path/filepath"
	"regexp"
	"testing"
)

// Migrations are numbered, forward-only and never renumbered after they land, so two pull requests that
// each take "the next free number" collide the moment both are on main: the later one must renumber
// before it merges. This holds the line at the tree: every migration number is used once.
// historicDuplicates are the numbers two landed migrations already share. Both ran everywhere long ago and
// the files are frozen, so the pair is recorded here and nothing new may join it.
var historicDuplicates = map[string]bool{"0043": true}

func TestMigrationNumbersAreUnique(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "..", "db", "migrations", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no migrations found: %v", err)
	}
	num := regexp.MustCompile(`^(\d{4})_`)
	seen := map[string][]string{}
	for _, f := range files {
		base := filepath.Base(f)
		if m := num.FindStringSubmatch(base); m != nil {
			seen[m[1]] = append(seen[m[1]], base)
		}
	}

	for n, names := range seen {
		if len(names) > 1 && !historicDuplicates[n] {

			t.Errorf("migration number %s is used by %d files: %v", n, len(names), names)
		}
	}

}
