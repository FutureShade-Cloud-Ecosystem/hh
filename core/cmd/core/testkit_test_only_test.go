package main

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTestkitIsImportedOnlyByTestFiles proves the claim the fw1 and w25 censuses rest on when they
// treat internal/testkit as test only tooling: no non test file outside the testkit tree imports
// it, so it is never linked into the binary.
func TestTestkitIsImportedOnlyByTestFiles(t *testing.T) {
	for _, sub := range []string{"cmd", "internal"} {
		root := filepath.Join("..", "..", sub)
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			if strings.Contains(filepath.ToSlash(path), "internal/testkit/") {
				return nil // the tree's own files may import each other
			}
			src, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			f, perr := parser.ParseFile(token.NewFileSet(), path, src, parser.ImportsOnly)
			if perr != nil {
				return perr
			}
			for _, imp := range f.Imports {
				if strings.Contains(imp.Path.Value, "/internal/testkit") {
					t.Errorf("%s imports %s: non test code must not import internal/testkit", path, imp.Path.Value)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
}
