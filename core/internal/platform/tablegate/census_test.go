package tablegate

// census_test.go runs the gate over the real tree: every scanned module's
// non-test .go files, plus core/db/queries (the sqlc query directory, one
// file per module by its README's convention, empty today except the
// README). It fails closed: an unknown directory under core/internal, a
// referenced table or view missing from the ownership map, or a cross
// module access without an allowlist entry all fail the census with the
// file named.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// access is one module touching one object in one file.
type access struct {
	module string
	file   string
	decl   string
	table  string
	write  bool
}

func (a access) String() string {
	verb := "read"
	if a.write {
		verb = "write"
	}
	return fmt.Sprintf("%s %s.%s (%s) %s", verb, a.module, a.decl, a.table, a.file)
}

// repoRoot answers the repository root the package is checked out in,
// walking up from this file's own location (the package lives at
// core/internal/platform/tablegate).
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed: cannot locate the package")
	}
	root := filepath.Dir(thisFile)
	for i := 0; i < 4; i++ {
		root = filepath.Dir(root)
	}
	if _, err := os.Stat(filepath.Join(root, "core", "db", "migrations")); err != nil {
		t.Fatalf("repo root not found from %s: %v", root, err)
	}
	return root
}

// buildPkgIndex parses every non-test .go file under the module's directory
// tree and resolves the module index: package level const and var string
// values, and function and method return string parts, across all its
// subpackages, converged in rounds so a fragment built from another file's
// const or helper resolves regardless of file order. A cycle-guard keeps a
// mutually recursive pair of string helpers from spinning.
func buildPkgIndex(t *testing.T, dir string) (*pkgIndex, []*ast.File) {
	t.Helper()
	var files []*ast.File
	fset := token.NewFileSet()
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			return err
		}
		files = append(files, f)
		return nil
	})
	if err != nil {
		t.Fatalf("parse module %s: %v", dir, err)
	}
	idx := newPkgIndex(map[string][]string{}, map[string][]string{})
	building := map[string]bool{}
	for _, f := range files {
		for _, name := range packageSinks(f) {
			idx.sinks[name] = true
		}
	}
	for round := 0; round < 6; round++ {
		changed := false
		for _, f := range files {
			if got := packageFuncParts(f, idx, building); len(got) > 0 {
				changed = true
			}
			for name, parts := range packageStringValues(f, idx.funcs) {
				if _, done := idx.consts[name]; !done {
					idx.consts[name] = parts
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}
	return idx, files
}

// TestCensusZeroCrossModuleTableAccess is the gate itself: it walks the
// stated module list over the real tree and fails on any directory the
// stated list does not know, any referenced object the ownership map does
// not carry, and any cross-module access the allowlist does not name.
func TestCensusZeroCrossModuleTableAccess(t *testing.T) {
	root := repoRoot(t)

	// fail closed on unknown directories under core/internal
	entries, err := os.ReadDir(filepath.Join(root, "core", "internal"))
	if err != nil {
		t.Fatalf("read core/internal: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue // README.md and friends are files, not modules
		}
		if !knownDirs[e.Name()] {
			t.Fatalf("census fails closed: unknown directory core/internal/%s is not in the stated module list; state it in knownDirs (module or exclusion) before this census can run", e.Name())
		}
	}

	// core/db/queries: the sqlc query directory. One .sql file per module,
	// attributed by filename: <module>.sql where <module> is a scanned
	// module. The directory is empty today except its README; the census
	// still walks it, so the day the first query file lands it is checked.
	queriesDir := filepath.Join(root, "core", "db", "queries")
	qEntries, err := os.ReadDir(queriesDir)
	if err != nil {
		t.Fatalf("read core/db/queries: %v", err)
	}
	var violations []string
	for _, e := range qEntries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		module := strings.TrimSuffix(e.Name(), ".sql")
		if !knownDirs[module] || module == "testkit" || module == "testaudit" {
			t.Fatalf("census fails closed: core/db/queries/%s does not follow the one file per module convention (its module name is not a scanned module); name the module in knownDirs", e.Name())
		}
		// the content is checked by the same engine, attributed to the file's
		// module by the one file per module convention the README states.
		content, err := os.ReadFile(filepath.Join(queriesDir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		violations = append(violations, queriesFileViolations(module,
			"core/db/queries/"+e.Name(), string(content))...)
	}
	for _, module := range scanTargets {
		for _, a := range moduleAccesses(t, root, module) {
			violations = append(violations, checkAccess(a, ownedObjects[a.table])...)
		}
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("the zero cross-module table access rule is violated:\n%s", strings.Join(violations, "\n"))
	}
}

// checkAccess runs the three rules over one access and answers its
// violation strings: an unmapped object fails closed (the map must cover
// everything the scan meets), a same-module access passes, an allowlisted
// cross-module access passes, anything else is a violation. ownerFor is the
// mapped owner, or the empty string when the object is not in the map.
func checkAccess(a access, ownerFor string) []string {
	if ownerFor == "" {
		return []string{fmt.Sprintf(
			"UNMAPPED OBJECT (fails closed): %s touches %q, which the ownership map does not carry; derive it from core/db/migrations and state it in ownedObjects before this access can pass",
			a.String(), a.table)}
	}
	if ownerFor == a.module {
		return nil
	}
	if allowlisted(a) {
		return nil
	}
	return []string{fmt.Sprintf(
		"CROSS-MODULE ACCESS: %s touches %q owned by %q; go through the owning module's interface (reads) or the event bus (writes), or state the access in the allowlist with its reason",
		a.String(), a.table, ownerFor)}
}

// fixtureAccess builds an access for the unit fixtures: SQL fed straight to
// the gate as if extracted from the named module and file.
func fixtureAccess(module, file, decl, table string, write bool) access {
	return access{module: module, file: file, decl: decl, table: table, write: write}
}

// queriesFileViolations runs one core/db/queries file's content through the
// same tokenizer, statement extraction and rules, attributed to the file's
// module by the one file per module convention.
func queriesFileViolations(module, file, content string) []string {
	var out []string
	for _, ref := range extractTables(content) {
		a := fixtureAccess(module, file, filepath.Base(file), ref.table, ref.write)
		out = append(out, checkAccess(a, ownedObjects[ref.table])...)
	}
	return out
}

// moduleAccesses walks one module's non-test .go files and returns every
// access its SQL makes against the ownership map.
func moduleAccesses(t *testing.T, root, module string) []access {
	t.Helper()
	dir := filepath.Join(root, "core", "internal", module)
	pkg, _ := buildPkgIndex(t, dir)
	var out []access
	seen := map[access]bool{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		units, err := goFileUnits(src, rel, pkg)
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		for _, u := range units {
			for _, ref := range extractTables(u.corpus) {
				a := access{module: module, file: rel, decl: u.decl, table: ref.table, write: ref.write}
				if !seen[a] {
					seen[a] = true
					out = append(out, a)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", module, err)
	}
	return out
}

// allowlisted answers whether the access matches an allowlist entry: same
// module, same file, table named in the verb-matched list, reads against
// tables and writes against writes. A write under a read-only listing
// fails, and so does a read under a write-only listing.
func allowlisted(a access) bool {
	for _, e := range allowlist {
		if e.module != a.module || e.file != a.file {
			continue
		}
		names := e.tables
		if a.write {
			names = e.writes
		}
		for _, tbl := range names {
			if tbl == a.table {
				return true
			}
		}
	}
	return false
}
