// signature_statement_drift_test.go, the drift test for the signature insert statement.
//
// The pool capture (Repository.InsertSignature) and the transaction capture
// (TypedNameSignatureProvider.CaptureTypedTx) must write the signature row through ONE statement.
// While the tx capture carried its own inline twin of the repository's statement, the two copies
// could be edited apart and a signature row would read differently depending on how it was
// captured: the exact drift this file pins shut. Three mechanisms:
//
//   - a recording store proving the provider's tx path persists through store.InsertSignatureTx and
//     never through the pool method;
//   - an AST pass over the package's source files, test helpers included, proving exactly one INSERT
//     INTO pos_signature statement exists in the whole module and that it lives in
//     Repository.InsertSignatureTx, with the tx capture path calling the seam instead of running
//     statements on the transaction handle;
//   - the integration twin (signature_statement_drift_integration_test.go) capturing the same
//     attestation through both paths and comparing the stored rows column by column.
package pos

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// signatureTxRecordingStore counts where the provider persisted: the tx seam call, the tx handle it
// was handed, and any forbidden fall back to the pool method.
type signatureTxRecordingStore struct {
	posStore
	txCalls   int
	poolCalls int
	gotTx     pgx.Tx
	rows      []signatureRow
}

func (s *signatureTxRecordingStore) InsertSignatureTx(_ context.Context, tx pgx.Tx, sig signatureRow) (string, error) {
	s.txCalls++
	s.gotTx = tx
	s.rows = append(s.rows, sig)
	return "sig-row-tx", nil
}

func (s *signatureTxRecordingStore) InsertSignature(context.Context, signatureRow) (string, error) {
	s.poolCalls++
	return "sig-row-pool", nil
}

// TestCaptureTypedTx_InsertsThroughTheStoreSeam pins the transaction capture's persistence to the
// store seam: exactly one InsertSignatureTx carrying the caller's transaction and the sealed row,
// and no pool insert at all.
func TestCaptureTypedTx_InsertsThroughTheStoreSeam(t *testing.T) {
	store := &signatureTxRecordingStore{}
	p := &TypedNameSignatureProvider{store: store, audit: &recordingTxAudit{}}
	in := txTestAttestation()
	tx := captureTx{}
	ref, err := p.CaptureTypedTx(context.Background(), tx, in)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if !strings.HasPrefix(ref, "sig_") {
		t.Fatalf("a capture returns its sig_ reference, got %q", ref)
	}
	if store.txCalls != 1 {
		t.Fatalf("the tx capture must persist through store.InsertSignatureTx exactly once, got %d calls", store.txCalls)
	}
	if store.poolCalls != 0 {
		t.Fatalf("the tx capture must never fall back to the pool insert, got %d pool calls", store.poolCalls)
	}
	if store.gotTx == nil {
		t.Fatal("the insert must ride the CALLER'S transaction, a nil handle means the pool")
	}
	if len(store.rows) != 1 {
		t.Fatalf("exactly one row reaches the store, got %d", len(store.rows))
	}
	row := store.rows[0]
	if row.Purpose != in.Purpose || row.RefType != in.RefType || row.RefID != in.RefID {
		t.Fatalf("the row must name what was attested, got %+v", row)
	}
	if row.Provider != ProviderTypedNameV1 {
		t.Fatalf("provider = %q, want %q", row.Provider, ProviderTypedNameV1)
	}
}

// signatureSourceScan is one source file's statements, grouped by the function holding them.
// freeLiterals holds the string literals declared OUTSIDE any function body (package level consts).
type signatureSourceScan struct {
	funcLiterals map[string][]string
	funcCalls    map[string][]string
	freeLiterals []string
}

func newSignatureSourceScan() signatureSourceScan {
	return signatureSourceScan{
		funcLiterals: map[string][]string{},
		funcCalls:    map[string][]string{},
	}
}

// scanSignatureSourceText parses ONE source file given as text and records, per function, its
// constant string candidates and the selector names it calls. Taking text instead of a path is what
// lets the variant table below feed the pass an in-memory file: a planted statement is proven caught
// without editing the tree.
func scanSignatureSourceText(t *testing.T, name, src string) signatureSourceScan {
	t.Helper()
	scan := newSignatureSourceScan()
	// The pass reads every file in the package except its own two halves: this file and its
	// integration twin name the statement in their own prose and assertions, and a pass that read
	// itself would count its own pin as a second insert. Every other file, test helper or not, is
	// read, because a helper file is exactly where a second copy would hide.
	switch name {
	case "signature_statement_drift_test.go", "signature_statement_drift_integration_test.go":
		return scan
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	for _, d := range f.Decls {
		fd, isFunc := d.(*ast.FuncDecl)
		if !isFunc || fd.Body == nil {
			var free []string
			signatureCollectStrings(d, &free, nil)
			scan.freeLiterals = append(scan.freeLiterals, free...)
			continue
		}
		var lits, calls []string
		signatureCollectStrings(fd.Body, &lits, &calls)
		scan.funcLiterals[fd.Name.Name] = append(scan.funcLiterals[fd.Name.Name], lits...)
		scan.funcCalls[fd.Name.Name] = append(scan.funcCalls[fd.Name.Name], calls...)
	}
	return scan
}

// signatureCollectStrings walks root and records every string candidate the pin can stand over:
// interpreted and raw string literals, adjacent literal concatenations taken WHOLE (the concatenation
// is the one candidate, its halves are not), and fmt.Sprintf calls with a constant format, rendered
// with their constant arguments in the verbs' places. When calls is non nil every selector call name
// is recorded beside them. fmt.Sprintf is recognized by its package selector name; an aliased import
// of fmt is a bound of this pass, not something it invents.
func signatureCollectStrings(root ast.Node, literals, calls *[]string) {
	ast.Inspect(root, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.BasicLit:
			if v.Kind != token.STRING {
				return true
			}
			if s, ok := signatureConstantString(v); ok {
				*literals = append(*literals, s)
			}
		case *ast.BinaryExpr:
			if v.Op == token.ADD {
				if s, ok := signatureConstantString(v); ok {
					*literals = append(*literals, s)
					return false // the chain is taken whole, the halves are not candidates of their own
				}
			}
		case *ast.CallExpr:
			if sel, isSel := v.Fun.(*ast.SelectorExpr); isSel {
				if calls != nil {
					*calls = append(*calls, sel.Sel.Name)
				}
				if x, isIdent := sel.X.(*ast.Ident); isIdent && x.Name == "fmt" && sel.Sel.Name == "Sprintf" {
					if s, ok := signatureSprintfText(v); ok {
						*literals = append(*literals, s)
						return false // the rendered statement is the candidate, the format literal is not a second one
					}
				}
			}
		}
		return true
	})
}

// signatureConstantString resolves a CONSTANT string expression: an interpreted or raw string
// literal, or a chain of + over such literals. ok is false the moment anything non constant appears
// (an identifier, a call), because a value the pass cannot prove must never be invented.
func signatureConstantString(e ast.Expr) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		if strings.HasPrefix(v.Value, "`") {
			return strings.Trim(v.Value, "`"), true
		}
		s, err := strconv.Unquote(v.Value)
		if err != nil {
			return "", false
		}
		return s, true
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false
		}
		l, lok := signatureConstantString(v.X)
		r, rok := signatureConstantString(v.Y)
		if !lok || !rok {
			return "", false
		}
		return l + r, true
	}
	return "", false
}

// signatureSprintfText renders a fmt.Sprintf call with a constant format the loose way a statement
// builder reads: each verb takes the next argument's value, or a blank when the argument is not a
// constant or the arguments have run out, and a leftover argument appends at the end. The pass only
// asks whether an insert into the signature table is being assembled, so a blank never reads as the
// table name and a constant argument always lands where its verb stood.
func signatureSprintfText(call *ast.CallExpr) (string, bool) {
	if len(call.Args) == 0 {
		return "", false
	}
	format, ok := signatureConstantString(call.Args[0])
	if !ok {
		return "", false
	}
	args := make([]string, 0, len(call.Args)-1)
	for _, a := range call.Args[1:] {
		s, _ := signatureConstantString(a)
		args = append(args, s)
	}
	var b strings.Builder
	next := 0
	for i := 0; i < len(format); i++ {
		c := format[i]
		if c != '%' {
			b.WriteByte(c)
			continue
		}
		if i+1 >= len(format) {
			b.WriteByte(' ')
			break
		}
		if format[i+1] == '%' {
			b.WriteByte('%')
			i++
			continue
		}
		j := i + 1
		for j < len(format) && strings.ContainsRune("+-# 0123456789.", rune(format[j])) {
			j++
		}
		if j < len(format) && ((format[j] >= 'a' && format[j] <= 'z') || (format[j] >= 'A' && format[j] <= 'Z')) {
			if next < len(args) {
				b.WriteString(args[next])
				next++
			} else {
				b.WriteByte(' ')
			}
			i = j
			continue
		}
		// a stray percent is not a verb; a blank keeps it from gluing words together
		b.WriteByte(' ')
	}
	for ; next < len(args); next++ {
		b.WriteByte(' ')
		b.WriteString(args[next])
	}
	return b.String(), true
}

// signatureQualifiedTable strips a schema qualifier off the signature table's name in folded text,
// so public.pos_signature reads as the bare table. A schema qualifier never appears on a column in
// the needle's place, so the strip cannot manufacture a match that was not an insert into the table.
var signatureQualifiedTable = regexp.MustCompile(`[a-z0-9_]+\.pos_signature`)

// signatureFoldedSQL folds one candidate the way the pin matches: lowercased, double quotes dropped
// so a quoted table name reads bare, the table's schema qualifier stripped, and every whitespace run
// (spaces, tabs, newlines) collapsed to one space, so padding and line breaks cannot hide the
// statement.
func signatureFoldedSQL(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, `"`, "")
	s = signatureQualifiedTable.ReplaceAllString(s, "pos_signature")
	return strings.Join(strings.Fields(s), " ")
}

// scanSignatureStatementSources walks the package's own .go files (the test lives beside them, so
// runtime.Caller finds the directory both under go test and under the integration tag) and scans
// each one's source text.
func scanSignatureStatementSources(t *testing.T) signatureSourceScan {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not locate this test file's directory")
	}
	dir := filepath.Dir(thisFile)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	scan := newSignatureSourceScan()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		fileScan := scanSignatureSourceText(t, name, string(src))
		for fn, lits := range fileScan.funcLiterals {
			scan.funcLiterals[fn] = append(scan.funcLiterals[fn], lits...)
		}
		for fn, calls := range fileScan.funcCalls {
			scan.funcCalls[fn] = append(scan.funcCalls[fn], calls...)
		}
		scan.freeLiterals = append(scan.freeLiterals, fileScan.freeLiterals...)
	}
	return scan
}

// signatureInsertMatches locates the module's INSERT INTO pos_signature statements in the scan:
// every candidate is folded and matched folded, so case, padding, line breaks, quoting, a schema
// qualifier, literal concatenation and a built statement cannot dress a second copy up as anything
// else. One entry per match, naming the function that holds it, or "package scope" for a candidate
// declared outside any function body. Sorted, so a failure message is deterministic.
func signatureInsertMatches(scan signatureSourceScan) []string {
	var where []string
	for name, lits := range scan.funcLiterals {
		for _, lit := range lits {
			if strings.Contains(signatureFoldedSQL(lit), "insert into pos_signature") {
				where = append(where, name)
			}
		}
	}
	for _, lit := range scan.freeLiterals {
		if strings.Contains(signatureFoldedSQL(lit), "insert into pos_signature") {
			where = append(where, "package scope")
		}
	}
	sort.Strings(where)
	return where
}

// TestSignatureInsertStatementIsShared is the drift pin: one INSERT INTO pos_signature statement in
// the whole module, living in Repository.InsertSignatureTx, and a transaction capture path that
// reaches it through the store seam instead of running its own copy on the transaction handle.
func TestSignatureInsertStatementIsShared(t *testing.T) {
	scan := scanSignatureStatementSources(t)
	var drift []string
	matches := signatureInsertMatches(scan)
	for _, where := range matches {
		switch where {
		case "InsertSignatureTx":
			// the one sanctioned home of the statement
		case "package scope":
			drift = append(drift, "an INSERT INTO pos_signature statement lives at package scope, outside the shared Repository.InsertSignatureTx")
		default:
			drift = append(drift, fmt.Sprintf(
				"an INSERT INTO pos_signature statement lives in %s, outside the shared Repository.InsertSignatureTx", where))
		}
	}
	if len(matches) != 1 {
		drift = append(drift, fmt.Sprintf(
			"the module must carry exactly one INSERT INTO pos_signature statement, found %d: a second copy drifts from the first and a signature row reads differently however it was captured", len(matches)))
	}
	calls := scan.funcCalls["CaptureTypedTx"]
	foundSeamCall := false
	for _, c := range calls {
		if c == "InsertSignatureTx" {
			foundSeamCall = true
		}
		if c == "QueryRow" {
			drift = append(drift, "CaptureTypedTx runs a statement on the transaction handle itself instead of through store.InsertSignatureTx")
		}
	}
	if !foundSeamCall {
		drift = append(drift, "CaptureTypedTx never calls store.InsertSignatureTx, the transaction capture is not on the shared statement")
	}
	if len(drift) > 0 {
		t.Fatalf("the signature insert statement has drifted off the one shared copy: %s", strings.Join(drift, "; "))
	}
}

// TestSignatureInsertDriftVariantsAreCaught feeds the AST pass one planted statement per evading
// variant the PR 174 review listed, each variant a whole source file built in memory: a raw string
// literal parked in a _test.go helper, a fmt.Sprintf built statement, lowercase SQL, a double space,
// a line break between INTO and the table name, adjacent literal concatenation, a schema qualified
// table name, and a quoted table name. A disguise the pass sees through nothing is a second copy of
// the statement drifting under the pin, so every variant must come back as exactly one match in the
// function that plants it.
func TestSignatureInsertDriftVariantsAreCaught(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		source   string
	}{
		{
			name:     "a raw string literal in a _test.go helper file",
			filename: "sig_plant_helper_test.go",
			source:   "package pos\n\nfunc plantSignatureRow() string {\n\treturn `INSERT INTO pos_signature (purpose, ref_type) VALUES ('x', 'order')`\n}\n",
		},
		{
			name:     "a fmt.Sprintf built statement",
			filename: "sig_plant_variant.go",
			source:   "package pos\n\nimport \"fmt\"\n\nfunc plantSignatureRow() string {\n\treturn fmt.Sprintf(\"INSERT INTO %s (purpose, ref_type) VALUES ('x', 'order')\", \"pos_signature\")\n}\n",
		},
		{
			name:     "lowercase SQL",
			filename: "sig_plant_variant.go",
			source:   "package pos\n\nfunc plantSignatureRow() string {\n\treturn \"insert into pos_signature (purpose, ref_type) values ('x', 'order')\"\n}\n",
		},
		{
			name:     "a double space between INTO and the table name",
			filename: "sig_plant_variant.go",
			source:   "package pos\n\nfunc plantSignatureRow() string {\n\treturn \"INSERT INTO  pos_signature (purpose, ref_type) VALUES ('x', 'order')\"\n}\n",
		},
		{
			name:     "a line break between INTO and the table name",
			filename: "sig_plant_variant.go",
			source:   "package pos\n\nfunc plantSignatureRow() string {\n\treturn \"INSERT INTO\\n\\tpos_signature (purpose, ref_type) VALUES ('x', 'order')\"\n}\n",
		},
		{
			name:     "adjacent literal concatenation splitting the table name",
			filename: "sig_plant_variant.go",
			source:   "package pos\n\nfunc plantSignatureRow() string {\n\treturn \"INSERT INTO pos_\" + \"signature (purpose, ref_type) VALUES ('x', 'order')\"\n}\n",
		},
		{
			name:     "a schema qualified table name",
			filename: "sig_plant_variant.go",
			source:   "package pos\n\nfunc plantSignatureRow() string {\n\treturn \"INSERT INTO public.pos_signature (purpose, ref_type) VALUES ('x', 'order')\"\n}\n",
		},
		{
			name:     "a quoted table name",
			filename: "sig_plant_variant.go",
			source:   "package pos\n\nfunc plantSignatureRow() string {\n\treturn \"INSERT INTO \\\"pos_signature\\\" (purpose, ref_type) VALUES ('x', 'order')\"\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scan := scanSignatureSourceText(t, tt.filename, tt.source)
			matches := signatureInsertMatches(scan)
			if len(matches) != 1 || matches[0] != "plantSignatureRow" {
				t.Fatalf("the pass must see the planted statement through this disguise, saw %d matches: %v", len(matches), matches)
			}
		})
	}
}
