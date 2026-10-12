package identity

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// The signing path is fenced from the verifier by source: the verifier files
// reference none of the signing identifiers. Both live in package identity,
// so the compiler does not enforce this; this test does. The signing
// identifiers are the ones sessionsigner.go declares.
func TestVerifierFilesReferenceNoSigningIdentifier(t *testing.T) {
	signing := map[string]bool{
		"SessionMinter": true, "NewSessionMinter": true, "LoadSessionKeys": true,
		"SessionKeyFiles": true, "LoadedSessionKeys": true, "MinSessionWindow": true,
		"loadSessionPrivateKey": true, "MintAt": true, "Mint": true,
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
