package uuidguard

// uuidguard_test.go is the gate against one SQL shape that took the register, the credit policy and the GL posting
// rules down: a parameter guarded by <> '' (or != '') beside a ::uuid cast of the same parameter, as in
//
//	WHERE branch_id IS NULL OR ($1 <> '' AND branch_id = $1::uuid)
//
// SQL does not promise the guard runs first. Under a generic plan the planner lifts `branch_id = $1::uuid` into an
// index condition and casts the parameter when the scan starts, so an empty value fails with invalid input syntax
// for type uuid (22P02). The safe spelling is `branch_id = NULLIF($1, '')::uuid`, which compares against NULL for an
// empty value and so matches nothing, the same rows the guard meant.
//
// The census walks every Go file under core and checks each string literal as one SQL text, so a statement in a
// test file is held to the same rule. It runs in the untagged suite, which CI runs on every pull request.
//
// The other shape, `$1 = '' OR col = $1::uuid`, is not checked: it is safe today because `$1 = ''` cannot be an
// index condition, the sites that use it are many, and SQL does not guarantee it either. See PR 585's report.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// guardRe finds `$N <> ”` and `$N != ”`.
var guardRe = regexp.MustCompile(`\$(\d+)\s*(?:<>|!=)\s*''`)

// castRe finds `$N::uuid` and `CAST($N AS uuid)`, with the parameter number captured in one of two groups.
var castRe = regexp.MustCompile(`(?i)\$(\d+)\s*::\s*uuid\b|CAST\(\s*\$(\d+)\s+AS\s+uuid\s*\)`)

// guardedCasts returns the 1-based line, within sql, of every cast of a parameter that the same text also guards
// with <> ” or != ”.
func guardedCasts(sql string) []int {
	guarded := map[string]bool{}
	for _, m := range guardRe.FindAllStringSubmatch(sql, -1) {
		guarded[m[1]] = true
	}
	if len(guarded) == 0 {
		return nil
	}
	var lines []int
	for _, m := range castRe.FindAllStringSubmatchIndex(sql, -1) {
		n := ""
		if m[2] >= 0 {
			n = sql[m[2]:m[3]]
		} else if m[4] >= 0 {
			n = sql[m[4]:m[5]]
		}
		if guarded[n] {
			lines = append(lines, 1+strings.Count(sql[:m[0]], "\n"))
		}
	}
	return lines
}

func coreRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed: cannot locate the package")
	}
	// core/internal/platform/uuidguard/uuidguard_test.go
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
}

// scan returns "file:line: text" for every guarded cast in the Go files under root.
func scan(t *testing.T, root string) []string {
	t.Helper()
	var hits []string
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", "vendor", ".git", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		rel, _ := filepath.Rel(root, path)
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			start := fset.Position(lit.Pos()).Line
			for _, ln := range guardedCasts(s) {
				text := strings.TrimSpace(strings.Split(s, "\n")[ln-1])
				hits = append(hits, fmt.Sprintf("%s:%d: %s", filepath.ToSlash(rel), start+ln-1, text))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(hits)
	return hits
}

// The tree holds no guarded cast. A hit names the file and line; rewrite the comparison as
// col = NULLIF($N, ”)::uuid.
func TestNoSQLGuardsAUUIDParameterWithAnEmptyStringCheck(t *testing.T) {
	hits := scan(t, coreRoot(t))
	if len(hits) > 0 {
		t.Fatalf("a parameter is guarded by <> '' beside a ::uuid cast of itself; SQL does not run the guard first, so under a generic plan an empty value fails with invalid input syntax for type uuid. Rewrite as col = NULLIF($N, '')::uuid:\n  %s", strings.Join(hits, "\n  "))
	}
}

// The detector bites: the shapes that failed, planted as text, are found; the safe spellings are not.
func TestDetectorFindsTheFailingShapesAndNotTheSafeOnes(t *testing.T) {
	// The fixtures spell the guards with placeholders, so this file's own literals pass the census it feeds.
	plant := strings.NewReplacer("§", "<> ''", "¶", "!= ''").Replace
	bad := map[string]string{
		"or guard":        "SELECT 1 FROM t WHERE branch_id IS NULL OR ($1 § AND branch_id = $1::uuid)",
		"bang guard":      "SELECT 1 FROM t WHERE ($1 ¶ AND id = $1::uuid)",
		"cast guard":      "SELECT 1 FROM t WHERE $2 § AND id = CAST($2 AS uuid)",
		"guard line away": "SELECT 1 FROM t\n WHERE x\n   AND ($3 §\n        AND id = $3::UUID)",
	}
	for name, sql := range bad {
		if len(guardedCasts(plant(sql))) == 0 {
			t.Errorf("%s: the detector missed %q", name, sql)
		}
	}
	good := map[string]string{
		"nullif":           "SELECT 1 FROM t WHERE branch_id IS NULL OR branch_id = NULLIF($1, '')::uuid",
		"other parameter":  "SELECT 1 FROM t WHERE $1 § AND id = $2::uuid",
		"empty or":         "SELECT 1 FROM t WHERE $1 = '' OR id = $1::uuid",
		"guard without":    "SELECT 1 FROM t WHERE $1 § AND name = $1",
		"nullif and guard": "SELECT 1 FROM t WHERE ($1 § AND name = $1) OR id = NULLIF($1,'')::uuid",
	}
	for name, sql := range good {
		if got := guardedCasts(plant(sql)); len(got) != 0 {
			t.Errorf("%s: the detector flagged %q at lines %v", name, sql, got)
		}
	}
	if got := guardedCasts(plant("SELECT 1\n WHERE ($1 §\n   AND id = $1::uuid)")); len(got) != 1 || got[0] != 3 {
		t.Errorf("the line must be the cast's, got %v", got)
	}
}
