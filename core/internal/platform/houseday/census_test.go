package houseday

// census_test.go, the second half of the zone census (item 3b slice 3). The first half
// (dashboard's TestHouseZone_EveryCopyInTheBackendNamesTheSameZone) pins every module's copy of the
// zone name to the Ontario default. This half pins the other direction: a business day decision takes
// the DEALER's zone. It scans production Go for two things and rules on both:
//
//  1. Nothing outside this package calls the package level Today, AddDays, ParseDay, EndOfDay or
//     Location, which are the Ontario default's shorthand, except the few sites listed below with the
//     reason each still reads the default. The list may shrink and may not grow: a new site takes a
//     Zone from its dealer (Source and Resolve) instead.
//  2. Every package that keeps its own copy of the zone constant also imports this package, so it has
//     the type it takes the dealer's zone in.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// defaultUses lists the production files that still call a package level (Ontario default) function,
// with how many calls and why. Raising a count or adding a file is the failure this test exists for.
var defaultUses = map[string]struct {
	calls int
	why   string
}{
	"internal/purchasing/repository.go": {1, "the bill date fallback for a caller that bound no day; the service binds the dealer's day before the transaction"},
	"internal/tax/model.go":             {1, "the package normDate helper kept for the default zone; the service's own normDate takes the dealer's"},
}

func coreRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the houseday package")
		}
		dir = parent
	}
}

func productionFiles(t *testing.T, root string, visit func(rel string, f *ast.File)) {
	t.Helper()
	for _, sub := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, sub), func(path string, d os.DirEntry, werr error) error {
			if werr != nil {
				return werr
			}
			if d.IsDir() {
				if n := d.Name(); n == "node_modules" || n == "testdata" || n == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			if strings.HasPrefix(rel, "internal/platform/houseday/") {
				return nil
			}
			f, perr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if perr != nil {
				t.Fatalf("%s does not parse: %v", rel, perr)
			}
			visit(rel, f)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func importsHouseday(f *ast.File) string {
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if strings.HasSuffix(p, "/internal/platform/houseday") {
			if imp.Name != nil {
				return imp.Name.Name
			}
			return "houseday"
		}
	}
	return ""
}

func TestCensus_BusinessDayDecisionsTakeTheDealersZone(t *testing.T) {
	root := coreRoot(t)
	defaultFns := map[string]bool{"Today": true, "AddDays": true, "ParseDay": true, "EndOfDay": true, "Location": true}
	found := map[string]int{}
	productionFiles(t, root, func(rel string, f *ast.File) {
		name := importsHouseday(f)
		if name == "" {
			return
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == name && defaultFns[sel.Sel.Name] {
				found[rel]++
			}
			return true
		})
	})
	var files []string
	for rel := range found {
		files = append(files, rel)
	}
	sort.Strings(files)
	for _, rel := range files {
		allowed, ok := defaultUses[rel]
		if !ok {
			t.Errorf("%s calls the Ontario default business day shorthand %d time(s); a business day decision takes the dealer's zone (houseday.Source, Resolve)", rel, found[rel])
			continue
		}
		if found[rel] > allowed.calls {
			t.Errorf("%s calls the default shorthand %d time(s), %d are accounted for (%s)", rel, found[rel], allowed.calls, allowed.why)
		}
	}
	for rel, allowed := range defaultUses {
		if found[rel] == 0 {
			t.Errorf("%s no longer calls the default shorthand: remove it from defaultUses (%s)", rel, allowed.why)
		}
	}
}

func TestCensus_EveryPackageWithItsOwnZoneCopyTakesTheDealersZone(t *testing.T) {
	root := coreRoot(t)
	copies := map[string]string{} // package dir -> file holding the zone constant
	imports := map[string]bool{}  // package dir -> some production file imports houseday
	productionFiles(t, root, func(rel string, f *ast.File) {
		dir := filepath.ToSlash(filepath.Dir(rel))
		if importsHouseday(f) != "" {
			imports[dir] = true
		}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				for _, val := range vs.Values {
					if lit, ok := val.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if s, err := strconv.Unquote(lit.Value); err == nil && s == houseZone {
							copies[dir] = rel
						}
					}
				}
			}
		}
	})
	if len(copies) < 8 {
		t.Fatalf("found %d packages keeping a copy of the zone, want at least the 8 the first census counts: the scan lost sight of them", len(copies))
	}
	// internal/settings owns the dealer's configuration: its DefaultBusinessZone is the default a dealer
	// with no row answers, the source of the zone and not a consumer of it.
	delete(copies, "internal/settings")
	for dir, rel := range copies {
		if !imports[dir] {
			t.Errorf("%s keeps its own copy of the house zone (%s) but no production file in the package imports platform/houseday: it cannot take the dealer's zone", dir, rel)
		}
	}
}
