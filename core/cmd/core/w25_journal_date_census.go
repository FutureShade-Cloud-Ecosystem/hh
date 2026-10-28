package main

// w25_journal_date_census.go is the source check for W2-5 fix round 8: EVERY journal inventory
// posts carries a document date.
//
// WHAT IT CHECKS. The inventory module builds its GL journals as `GLJournal{...}` literals. A
// journal with no document date is dated at the GL's clock, the moment it is posted, which is a
// DIFFERENT instant from the movement it books: the stock ledger's move carries the movement's
// instant and the journal carries the drain's. A month end that falls between the two finds the
// two sides of one movement on different sides of itself and refuses the month by the whole
// movement (S5's P2-2 for the receipts, P3-1 for the rest).
//
// The scan walks every non-test Go file in the inventory package, finds every `GLJournal{...}`
// literal, and fails when one names no `DocumentDate`. Every such literal in this package is
// POSTED (each is handed to the GL poster or to a helper that hands it over), so the check needs
// no separate "is it posted" test: a literal that is built and never posted would still be asked
// for a date, which is the safe direction to be wrong in.
//
// It mirrors FW1-08b's branch census shape: a source scan, a failure on any writer that does not
// carry the thing, and a probe that proves the scan is not vacuous.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// w25UndatedJournal is one journal literal that names no document date.
type w25UndatedJournal struct {
	File string
	Line int
	Func string
}

// w25JournalDateResult is what the scan found.
type w25JournalDateResult struct {
	// Undated are the literals that name no DocumentDate: the census fails on each of them.
	Undated []w25UndatedJournal
	// Literals is how many journal literals the scan saw at all. A scan that saw none has
	// proved nothing, so the caller checks this number is not zero.
	Literals int
}

// w25JournalDateCensus scans one directory of Go source for every inventory journal literal and
// reports the ones that name no document date.
func w25JournalDateCensus(dir string) (w25JournalDateResult, error) {
	var result w25JournalDateResult
	entries, err := os.ReadDir(dir)
	if err != nil {
		return result, fmt.Errorf("read %s: %w", dir, err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		files = append(files, filepath.Join(dir, e.Name()))
	}
	sort.Strings(files)
	for _, path := range files {
		found, err := w25GoJournalDates(path)
		if err != nil {
			return result, err
		}
		result.Literals += found.Literals
		result.Undated = append(result.Undated, found.Undated...)
	}
	sort.Slice(result.Undated, func(i, j int) bool {
		if result.Undated[i].File != result.Undated[j].File {
			return result.Undated[i].File < result.Undated[j].File
		}
		return result.Undated[i].Line < result.Undated[j].Line
	})
	return result, nil
}

// w25GoJournalDates scans one Go file for inventory journal literals and the ones that name no
// document date.
func w25GoJournalDates(path string) (w25JournalDateResult, error) {
	var result w25JournalDateResult
	fset := token.NewFileSet()
	fileAST, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return result, fmt.Errorf("parse %s: %w", path, err)
	}
	base := filepath.Base(path)
	for _, decl := range fileAST.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok || !w25IsGLJournalType(lit.Type) {
				return true
			}
			result.Literals++
			if w25NamesDocumentDate(lit) {
				return true
			}
			result.Undated = append(result.Undated, w25UndatedJournal{
				File: base,
				Line: fset.Position(lit.Pos()).Line,
				Func: w25JournalFuncName(fn),
			})
			return true
		})
	}
	return result, nil
}

// w25IsGLJournalType answers whether a composite literal's type names the inventory journal.
func w25IsGLJournalType(expr ast.Expr) bool {
	switch t := expr.(type) {
	case *ast.SelectorExpr:
		return t.Sel.Name == "GLJournal"
	case *ast.Ident:
		return t.Name == "GLJournal"
	}
	return false
}

// w25NamesDocumentDate answers whether a journal literal names a document date.
func w25NamesDocumentDate(lit *ast.CompositeLit) bool {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if ok && key.Name == "DocumentDate" {
			return true
		}
	}
	return false
}

// w25JournalFuncName formats the function a journal literal sits in.
func w25JournalFuncName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	return "(" + w25JournalTypeString(fn.Recv.List[0].Type) + ")." + fn.Name.Name
}

// w25JournalTypeString returns a readable form of a receiver type.
func w25JournalTypeString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + w25JournalTypeString(t.X)
	case *ast.SelectorExpr:
		return w25JournalTypeString(t.X) + "." + t.Sel.Name
	}
	return "?"
}

// w25UndatedJournalError names one journal literal that names no document date.
func w25UndatedJournalError(u w25UndatedJournal) string {
	return fmt.Sprintf("%s:%d (%s): the journal names no document date, so the GL dates it at the moment it is posted and the two sides of the movement it books are cut at two different instants", u.File, u.Line, u.Func)
}
