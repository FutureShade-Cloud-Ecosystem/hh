package assist

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// The package doc states how the assistant reads data, so the sentence cannot
// drift from the design (plan 3.1): the assist logins, holding nothing but the ai
// and ai_pro objects, and never the app role.
func TestPackageDocStatesTheReadBoundary(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, parser.ParseComments|parser.PackageClauseOnly)
	if err != nil {
		t.Fatal(err)
	}
	var doc strings.Builder
	for _, f := range pkgs["assist"].Files {
		if f.Doc != nil {
			doc.WriteString(f.Doc.Text())
		}
	}
	text := strings.Join(strings.Fields(doc.String()), " ")
	for _, want := range []string{
		"reads data only through the assist logins",
		"hold no privilege but the ai and ai_pro objects",
		"never the app role",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the package doc does not say %q", want)
		}
	}
}
