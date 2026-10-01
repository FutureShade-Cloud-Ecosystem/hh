package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// G4's inventory half: every environment name the core reads outside
// tests is inventoried in demoenv_inventory.go and classed (denied on the
// demo box, allowed on it, or the card processor's own rule). The test
// walks every non-test Go file under the core module, resolves each
// environment reader's argument (a string literal, a package constant, or
// a constant of another package of this tree named through an import),
// and holds the fixture to what the source does in both directions: a new
// read missing from the fixture fails, a stale fixture entry naming a
// read that is gone fails, and an unclassed name fails. A dynamic read
// (a helper body that takes the name from its own parameter) is pinned
// per file by count, so a new one fails until someone pins it with the
// count it should carry.
//
// What the scanner sees, exactly: any call whose function is a selector
// named Getenv or LookupEnv, under any receiver or import alias (so an
// os import under another name feeds them and is caught); any call whose
// function is an identifier in the reader set below, or a selector whose
// field or method carries one of those names (a reader held in a struct
// field); and each such call's argument, when it is a string literal, a
// constant of the file's own package, or a constant of another package of
// this tree named through the file's imports. What it does not see: a
// reader passed under a name outside the reader set (a helper named
// something else), a name computed at run time, or a read through an
// interface of another shape. The reader set and the per-file dynamic
// site counts are the maintained half: a new shape joins them in a
// reviewed one-line change, and the plant tests at the bottom of this
// file prove the shapes the reviewer found stay caught.

// moduleImportPath is the core module's own import path prefix; an import
// under it resolves to a directory of this tree, whose constants the
// resolver can read.
const moduleImportPath = "github.com/FutureShade-Cloud-Ecosystem/hh/core/"

// envReaderHelpers are the non-os helper names the core reads environment
// names through, beside os.Getenv and os.LookupEnv: the configuration
// package's three, the messaging provider's pair, the getenv and
// lookupEnv parameter shapes the entrypoint files use, and the bare get
// shape planted by the review (kept in the set so the shape it names
// stays caught).
var envReaderHelpers = map[string]bool{
	"strEnv":    true,
	"boolEnv":   true,
	"intEnv":    true,
	"env":       true,
	"getenv":    true,
	"get":       true,
	"lookupEnv": true,
}

// envNameShape is the shape of an environment variable name.
var envNameShape = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// unresolvedReaderSiteCounts pins the dynamic read sites: helper bodies
// whose argument is the helper's own parameter, and the two local get
// closures the widened reader set sweeps in without being environment
// readers at all (internal/gl/bistrack_tieout.go's tie-out lookup and
// internal/finstmt/service.go's row lookup; a base name here can stand
// for more than one file, so a new unresolved site anywhere changes a
// count and stops the build). A file not listed must carry none; a listed
// file must carry exactly its count.
var unresolvedReaderSiteCounts = map[string]int{
	"bistrack_tieout.go": 2,
	"config.go":          3,
	"main.go":            2,
	"policy.go":          1,
	"provider.go":        1,
	"service.go":         2,
}

// envInventoryDynamicNames are the names read only through the pinned
// dynamic sites, which the resolver cannot see: main.go's loadServeConfig
// walks a literal table of the three verifier timing knobs. A change to
// that loop trips main.go's site-count pin, and whoever reconciles it
// updates this list in the same commit.
var envInventoryDynamicNames = []string{
	"CORE_CLOCK_SKEW",
	"CORE_JWKS_CACHE_TTL",
	"CORE_JWKS_REFRESH_FLOOR",
}

func TestEnvironmentInventoryIsCompleteAndClassed(t *testing.T) {
	found := scanEnvironmentReads(t)

	fixture := map[string]bool{}
	for _, name := range envInventoryFixture {
		if fixture[name] {
			t.Fatalf("the inventory lists %s twice", name)
		}
		fixture[name] = true
	}
	for _, name := range sortedNames(found) {
		if !fixture[name] {
			t.Errorf("%s is read by the core but missing from the inventory: add it to envInventoryFixture and class it", name)
		}
	}
	dynamic := map[string]bool{}
	for _, name := range envInventoryDynamicNames {
		dynamic[name] = true
	}
	for _, name := range envInventoryFixture {
		if !found[name] && !dynamic[name] {
			t.Errorf("%s is in the inventory but no longer read: remove it", name)
		}
	}
	for _, name := range envInventoryFixture {
		if !classedEnvironmentName(name) {
			t.Errorf("%s is inventoried but on no list: deny it on the demo box or add it to the allow list, by name", name)
		}
	}
	for _, name := range envInventoryDynamicNames {
		if !classedEnvironmentName(name) {
			t.Errorf("%s is a dynamic read on no list: class it by name", name)
		}
	}
}

// classedEnvironmentName reports whether an inventoried name is denied on
// the demo box, allowed on it, or ruled by the card processor's own
// clause.
func classedEnvironmentName(name string) bool {
	for _, prefix := range demoEnvDenyPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	for _, refused := range demoEnvDenyNames {
		if name == refused {
			return true
		}
	}
	if name == demoEnvCardProcessorVar {
		return true
	}
	for _, allowed := range demoEnvAllowNames {
		if name == allowed {
			return true
		}
	}
	for _, prefix := range demoEnvAllowPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// scanEnvironmentReads walks the core module's non-test Go files and
// returns the set of environment names the code reads. Test files are out
// of scope: the inventory is what the core reads outside tests.
func scanEnvironmentReads(t *testing.T) map[string]bool {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("module root: %v", err)
	}

	// One parse pass over the tree: each directory's files, each
	// directory's string-constant table.
	filesByDir := map[string][]parsedFile{}
	fset := token.NewFileSet()
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "testdata", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		r, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		dir := filepath.ToSlash(filepath.Dir(r))
		filesByDir[dir] = append(filesByDir[dir], parsedFile{rel: filepath.ToSlash(r), file: f})
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk the core module: %v", walkErr)
	}

	constsByDir := map[string]map[string]string{}
	for dir, files := range filesByDir {
		consts := map[string]string{}
		for _, pf := range files {
			for _, decl := range pf.file.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.CONST {
					continue
				}
				for _, spec := range gen.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok || len(vs.Values) != len(vs.Names) {
						continue
					}
					for i, value := range vs.Values {
						lit, ok := value.(*ast.BasicLit)
						if !ok || lit.Kind != token.STRING {
							continue
						}
						consts[vs.Names[i].Name] = unquoteGo(lit.Value)
					}
				}
			}
		}
		constsByDir[dir] = consts
	}

	found := map[string]bool{}
	unresolved := map[string]int{}
	for _, files := range filesByDir {
		for _, pf := range files {
			collectReadsFrom(pf.file, pf.rel, constsByDir, found, unresolved)
		}
	}
	for file, count := range unresolved {
		want, pinned := unresolvedReaderSiteCounts[file]
		if !pinned {
			t.Errorf("%s reads an environment name the scanner cannot resolve to a literal or a constant: give the read a literal argument, or pin the file's dynamic site count in unresolvedReaderSiteCounts with a reason", file)
			continue
		}
		if count != want {
			t.Errorf("%s carries %d dynamic environment read(s), the pin says %d: a read moved, name it in the pin or in the inventory", file, count, want)
		}
	}

	set := map[string]bool{}
	for name := range found {
		set[name] = true
	}
	return set
}

// parsedFile is one walked source file with its parse tree.
type parsedFile struct {
	rel  string
	file *ast.File
}

func isEnvironmentReader(fun ast.Expr) bool {
	switch fn := fun.(type) {
	case *ast.SelectorExpr:
		// Any receiver or alias: os.Getenv, a stdos alias of the same,
		// and a reader held in a struct field whose name is in the
		// reader set. A selector named Getenv or LookupEnv under
		// something that is not os would be a coincidence worth
		// investigating, so the conservative read treats it as a read.
		return fn.Sel.Name == "Getenv" || fn.Sel.Name == "LookupEnv" || envReaderHelpers[fn.Sel.Name]
	case *ast.Ident:
		return envReaderHelpers[fn.Name]
	}
	return false
}

// resolveEnvironmentName resolves a reader's first argument to a string:
// a literal, a constant of the same package, or a constant of another
// package of this tree named through an import.
func resolveEnvironmentName(arg ast.Expr, fileDir string, constsByDir map[string]map[string]string, imports map[string]string) (string, bool) {
	switch a := arg.(type) {
	case *ast.BasicLit:
		if a.Kind == token.STRING {
			return unquoteGo(a.Value), true
		}
	case *ast.Ident:
		if v, ok := constsByDir[fileDir][a.Name]; ok {
			return v, true
		}
	case *ast.SelectorExpr:
		x, ok := a.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		path, ok := imports[x.Name]
		if !ok || !strings.HasPrefix(path, moduleImportPath) {
			return "", false
		}
		other := strings.TrimPrefix(path, moduleImportPath)
		if v, ok := constsByDir[other][a.Sel.Name]; ok {
			return v, true
		}
	}
	return "", false
}

// unquoteGo strips the quoting of a Go string or raw string literal.
func unquoteGo(lit string) string {
	if strings.HasPrefix(lit, "`") {
		return strings.Trim(lit, "`")
	}
	unquoted, err := strconv.Unquote(lit)
	if err != nil {
		return lit
	}
	return unquoted
}

func sortedNames(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// collectReadsFrom records one parsed file's environment reads into the
// walk's tables: the literal or resolved constant arguments of the reader
// calls, and the per-file count of dynamic sites the resolver could not
// pin. The walk and the planted-shape tests drive the same function, so
// what the tests prove is what the walk does.
func collectReadsFrom(f *ast.File, rel string, constsByDir map[string]map[string]string, found map[string]bool, unresolved map[string]int) {
	imports := map[string]string{}
	for _, imp := range f.Imports {
		path := unquoteGo(imp.Path.Value)
		alias := filepath.Base(path)
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		imports[alias] = path
	}
	fileBase := filepath.Base(rel)
	fileDir := filepath.Dir(rel)
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		if !isEnvironmentReader(call.Fun) {
			return true
		}
		name, ok := resolveEnvironmentName(call.Args[0], fileDir, constsByDir, imports)
		if !ok {
			// Only the shapes a helper body takes (Getenv or LookupEnv
			// under any name, and a bare helper identifier) count as
			// dynamic sites. A helper-word selector whose argument does
			// not resolve (a method named get on something that is not
			// an environment reader) cannot be told from an environment
			// reader without dataflow, so it is left alone rather than
			// pinned; the shapes the plant tests hold are the
			// literal-argument ones.
			if sel, isSel := call.Fun.(*ast.SelectorExpr); isSel && envReaderHelpers[sel.Sel.Name] {
				return true
			}
			unresolved[fileBase]++
			return true
		}
		if envNameShape.MatchString(name) {
			found[name] = true
		}
		return true
	})
}

// The reviewer's three planted shapes, as permanent cases: a reader held
// in a struct field, a reader held under a bare short name, and an os
// import under an alias. Each synthetic file goes through the same
// collection the walk uses, and each plant must surface as a read the
// inventory would then have to class. The shapes are kept here so a
// scanner regression fails this test and not only a future review.
func TestEnvironmentInventoryScannerCatchesThePlantedShapes(t *testing.T) {
	shapes := []struct {
		name   string
		source string
		want   string
	}{
		{"reader in a struct field", `package p
type c struct{ getenv func(string) string }
func f(x c) string { return x.getenv("FAKE_FIELD_PROVIDER_URL") }`, "FAKE_FIELD_PROVIDER_URL"},
		{"reader under a bare name", `package p
func f(get func(string) string) string { return get("FAKE_BARE_PROVIDER_URL") }`, "FAKE_BARE_PROVIDER_URL"},
		{"os import under an alias", `package p
import stdos "os"
func f() string { return stdos.Getenv("FAKE_ALIAS_PROVIDER_URL") }`, "FAKE_ALIAS_PROVIDER_URL"},
	}
	constsByDir := map[string]map[string]string{"p": {}}
	for _, s := range shapes {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, s.name+".go", s.source, 0)
		if err != nil {
			t.Fatalf("%s: parse: %v", s.name, err)
		}
		found := map[string]bool{}
		unresolved := map[string]int{}
		collectReadsFrom(f, "plant/"+s.name+".go", constsByDir, found, unresolved)
		for name := range unresolved {
			t.Fatalf("%s: the shape landed as an unresolved dynamic site (%s), not a read", s.name, name)
		}
		if !found[s.want] {
			t.Fatalf("%s: the planted shape was not caught: %v", s.name, found)
		}
	}
}
