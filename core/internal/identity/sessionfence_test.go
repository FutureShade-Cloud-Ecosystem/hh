package identity

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// signingIdentifiers derives the fence from sessionsigner.go itself: every
// top level function, type, constant and variable it declares, and every
// method name except the ones a formatter or accessor shares with ordinary
// code. The set follows the file, so a new declaration is fenced without
// anyone remembering to list it.
func signingIdentifiers(t *testing.T) map[string]bool {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "sessionsigner.go", nil, 0)
	if err != nil {
		t.Fatalf("parse sessionsigner.go: %v", err)
	}
	shared := map[string]bool{"String": true, "Format": true, "GoString": true, "PublicKey": true}
	names := map[string]bool{}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv != nil && shared[d.Name.Name] {
				continue
			}
			names[d.Name.Name] = true
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch sp := spec.(type) {
				case *ast.TypeSpec:
					names[sp.Name.Name] = true
				case *ast.ValueSpec:
					for _, n := range sp.Names {
						names[n.Name] = true
					}
				}
			}
		}
	}
	return names
}

// The signing path is fenced from the verifier by source: the verifier files
// reference none of the identifiers sessionsigner.go declares. Both live in
// package identity, so the compiler does not enforce this; this test does.
func TestVerifierFilesReferenceNoSigningIdentifier(t *testing.T) {
	signing := signingIdentifiers(t)
	for _, must := range []string{"SessionMinter", "NewSessionMinter", "LoadSessionKeys", "wipe", "demoIssuerValue", "maxKeyFileBytes", "redactedMinter", "loadSessionPublicKey", "readCapped", "MintAt", "Mint"} {
		if !signing[must] {
			t.Fatalf("the derived fence lacks %s: the derivation is broken", must)
		}
	}
	for _, file := range []string{"verifier.go", "jwks.go", "claims.go", "sessionkeys.go"} {
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && signing[id.Name] {
				t.Errorf("%s references the signing identifier %s", file, id.Name)
			}
			return true
		})
	}
}
