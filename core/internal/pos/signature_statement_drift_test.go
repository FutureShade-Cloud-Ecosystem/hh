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
//   - an AST pass over the package's production files proving exactly one INSERT INTO pos_signature
//     literal exists in the whole module and that it lives in Repository.InsertSignatureTx, with the
//     tx capture path calling the seam instead of running statements on the transaction handle;
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
	"runtime"
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

// signatureSourceScan is one production file's statements, grouped by the function holding them.
// freeLiterals holds the string literals declared OUTSIDE any function body (package level consts).
type signatureSourceScan struct {
	funcLiterals map[string][]string
	funcCalls    map[string][]string
	freeLiterals []string
}

// scanSignatureStatementSources parses the package's own non-test .go files (the test lives beside
// them, so runtime.Caller finds the directory both under go test and under the integration tag) and
// records, per function, its string literals and the selector names it calls.
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
	scan := signatureSourceScan{
		funcLiterals: map[string][]string{},
		funcCalls:    map[string][]string{},
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, d := range f.Decls {
			fd, isFunc := d.(*ast.FuncDecl)
			if !isFunc || fd.Body == nil {
				ast.Inspect(d, func(n ast.Node) bool {
					if lit, isLit := n.(*ast.BasicLit); isLit && lit.Kind == token.STRING {
						scan.freeLiterals = append(scan.freeLiterals, lit.Value)
					}
					return true
				})
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				switch v := n.(type) {
				case *ast.BasicLit:
					if v.Kind == token.STRING {
						scan.funcLiterals[fd.Name.Name] = append(scan.funcLiterals[fd.Name.Name], v.Value)
					}
				case *ast.CallExpr:
					if sel, isSel := v.Fun.(*ast.SelectorExpr); isSel {
						scan.funcCalls[fd.Name.Name] = append(scan.funcCalls[fd.Name.Name], sel.Sel.Name)
					}
				}
				return true
			})
		}
	}
	return scan
}

// TestSignatureInsertStatementIsShared is the drift pin: one INSERT INTO pos_signature statement in
// the whole module, living in Repository.InsertSignatureTx, and a transaction capture path that
// reaches it through the store seam instead of running its own copy on the transaction handle.
func TestSignatureInsertStatementIsShared(t *testing.T) {
	scan := scanSignatureStatementSources(t)
	var drift []string
	shared := 0
	for name, lits := range scan.funcLiterals {
		for _, lit := range lits {
			if !strings.Contains(lit, "INSERT INTO pos_signature") {
				continue
			}
			shared++
			if name != "InsertSignatureTx" {
				drift = append(drift, fmt.Sprintf(
					"an INSERT INTO pos_signature statement lives in %s, outside the shared Repository.InsertSignatureTx", name))
			}
		}
	}
	for _, lit := range scan.freeLiterals {
		if strings.Contains(lit, "INSERT INTO pos_signature") {
			shared++
			drift = append(drift, "an INSERT INTO pos_signature statement lives at package scope, outside the shared Repository.InsertSignatureTx")
		}
	}
	if shared != 1 {
		drift = append(drift, fmt.Sprintf(
			"the module must carry exactly one INSERT INTO pos_signature statement, found %d: a second copy drifts from the first and a signature row reads differently however it was captured", shared))
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
