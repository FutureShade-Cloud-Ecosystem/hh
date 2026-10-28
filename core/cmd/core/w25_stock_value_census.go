package main

// w25_stock_value_census.go is the scanner and allowance table for the W2-5 stock value census.
//
// The census walks every non-test Go file and every SQL file under the core tree, finds every
// writer of inventory value (INSERT / UPDATE / DELETE against product_branch_costs or stock_moves),
// and insists that each writer is either:
//   (a) a value-stamped move writer (the move carries the exact subledger delta the GL booked), or
//   (b) listed in the allowance table below with an honest reason and an accurate site count.
//
// It mirrors FW1-08b's branch census shape: a source scan, an allowance table with a reason and a
// count of sites per entry, failure on any new writer not in the table, and failure on any stale
// allowance entry whose site no longer exists.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// w25InventoryWriterSite is one place the source writes inventory value.
type w25InventoryWriterSite struct {
	file string
	line int
	fn   string
	kind string
}

// key is the site's identity in the allowance table. It omits the line number so a line that moves
// does not break the table.
func (s w25InventoryWriterSite) key() string {
	return s.file + "|" + s.fn + "|" + s.kind
}

// w25WritePattern matches an INSERT / UPDATE / DELETE statement against product_branch_costs or
// stock_moves. It is case-insensitive and permits leading whitespace and newline before the verb.
var w25WritePattern = regexp.MustCompile(`(?mi)^\s*(INSERT\s+INTO\s+(product_branch_costs|stock_moves)|UPDATE\s+(product_branch_costs|stock_moves)\b|DELETE\s+FROM\s+(product_branch_costs|stock_moves))`)

// w25OnHandWritePattern matches a statement that writes stock_balances' on_hand: an INSERT naming
// on_hand_base, an UPDATE setting on_hand_base, or any DELETE of balance rows (a deleted row takes
// its on-hand with it). Writes that touch only the reserved/allocated projections do not move on
// hand and are deliberately not matched (S5's P3 2: the census watches on hand writes).
var w25OnHandWritePattern = regexp.MustCompile(`(?mi)^\s*(INSERT\s+INTO\s+stock_balances\b[\s\S]*?\bon_hand_base\b)|^\s*(UPDATE\s+stock_balances\b[\s\S]*?\bSET\b[\s\S]*?\bon_hand_base\b)|^\s*(DELETE\s+FROM\s+stock_balances\b)`)

// w25OnHandWriteMatchKind turns a w25OnHandWritePattern submatch into the canonical
// "SQL:stock_balances:<verb>[:on_hand]" kind.
func w25OnHandWriteMatchKind(m []string) string {
	switch {
	case m[1] != "":
		return "SQL:stock_balances:INSERT:on_hand"
	case m[2] != "":
		return "SQL:stock_balances:UPDATE:on_hand"
	case m[3] != "":
		return "SQL:stock_balances:DELETE"
	}
	return ""
}

// w25WriteMatchKind turns a w25WritePattern submatch into the canonical "SQL:<table>:<verb>" kind.
func w25WriteMatchKind(m []string) string {
	switch {
	case m[1] != "":
		return "SQL:" + m[2] + ":INSERT"
	case m[3] != "":
		return "SQL:" + m[3] + ":UPDATE"
	case m[4] != "":
		return "SQL:" + m[4] + ":DELETE"
	}
	return ""
}

// w25InventoryValueCall is the set of repository method names that physically write inventory value.
// Every caller of one of these is a writer site and must be accounted for.
var w25InventoryValueCall = map[string]bool{
	"InsertMove":         true,
	"UpsertBranchCost":   true,
	"UpsertBalanceDelta": true,
}

// w25InventoryOnHandCall is the set of repository method names that physically write on hand. Every
// caller of one of these is an on-hand writer site and must be accounted for (S5's P3 2).
var w25InventoryOnHandCall = map[string]bool{
	"UpsertBalanceDelta": true,
}

// w25FindInventoryWriters scans coreRoot for every inventory-value writer.
func w25FindInventoryWriters(coreRoot string) ([]w25InventoryWriterSite, error) {
	var sites []w25InventoryWriterSite
	err := filepath.WalkDir(coreRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(coreRoot, path)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		switch {
		case strings.HasSuffix(path, ".go"):
			goSites, gerr := w25GoInventoryWriters(path, rel)
			if gerr != nil {
				return gerr
			}
			sites = append(sites, goSites...)
		case strings.HasSuffix(path, ".sql"):
			sqlSites, serr := w25SQLInventoryWriters(path, rel)
			if serr != nil {
				return serr
			}
			sites = append(sites, sqlSites...)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(sites, func(i, j int) bool {
		if sites[i].file != sites[j].file {
			return sites[i].file < sites[j].file
		}
		if sites[i].line != sites[j].line {
			return sites[i].line < sites[j].line
		}
		return sites[i].kind < sites[j].kind
	})
	return sites, nil
}

// w25GoInventoryWriters scans one Go file for SQL write literals and calls to inventory write
// methods.
func w25GoInventoryWriters(path, rel string) ([]w25InventoryWriterSite, error) {
	fset := token.NewFileSet()
	fileAST, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", rel, err)
	}
	var sites []w25InventoryWriterSite
	for _, decl := range fileAST.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		fnName := w25FormatFuncName(fn)
		// The repository seam methods themselves are not inventory-value paths; every caller is
		// accounted for as a call site, so counting the method body would double-count and force
		// the generic writer into a stamped/unstamped decision it cannot make on its own.
		if fnName == "(*Repository).InsertMove" || fnName == "(*Repository).UpsertBranchCost" || fnName == "(*Repository).UpsertBalanceDelta" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CallExpr:
				callName := w25CallName(node)
				if w25InventoryValueCall[callName] {
					sites = append(sites, w25InventoryWriterSite{
						file: rel,
						line: fset.Position(node.Pos()).Line,
						fn:   fnName,
						kind: "call:" + callName,
					})
				}
			case *ast.BasicLit:
				if node.Kind != token.STRING {
					return true
				}
				raw, err := strconv.Unquote(node.Value)
				if err != nil {
					return true
				}
				if m := w25WritePattern.FindStringSubmatch(raw); m != nil {
					kind := w25WriteMatchKind(m)
					if kind == "" {
						return true
					}
					sites = append(sites, w25InventoryWriterSite{
						file: rel,
						line: fset.Position(node.Pos()).Line,
						fn:   fnName,
						kind: kind,
					})
				}
				if m := w25OnHandWritePattern.FindStringSubmatch(raw); m != nil {
					kind := w25OnHandWriteMatchKind(m)
					if kind == "" {
						return true
					}
					sites = append(sites, w25InventoryWriterSite{
						file: rel,
						line: fset.Position(node.Pos()).Line,
						fn:   fnName,
						kind: kind,
					})
				}
			}
			return true
		})
	}
	return sites, nil
}

// w25SQLInventoryWriters scans one SQL file for write statements against the inventory value tables
// and on-hand writes against stock_balances.
func w25SQLInventoryWriters(path, rel string) ([]w25InventoryWriterSite, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Strip line comments so they are not mistaken for statements. Keep newlines to preserve line
	// numbers.
	clean := regexp.MustCompile(`(?m)--.*$`).ReplaceAll(body, nil)
	var sites []w25InventoryWriterSite
	for _, loc := range w25WritePattern.FindAllStringIndex(string(clean), -1) {
		start := loc[0]
		line := strings.Count(string(clean[:start]), "\n") + 1
		m := w25WritePattern.FindStringSubmatch(string(clean[start:loc[1]]))
		if m == nil {
			continue
		}
		kind := w25WriteMatchKind(m)
		if kind == "" {
			continue
		}
		sites = append(sites, w25InventoryWriterSite{
			file: rel,
			line: line,
			fn:   "(sql)",
			kind: kind,
		})
	}
	for _, loc := range w25OnHandWritePattern.FindAllStringIndex(string(clean), -1) {
		start := loc[0]
		line := strings.Count(string(clean[:start]), "\n") + 1
		m := w25OnHandWritePattern.FindStringSubmatch(string(clean[start:loc[1]]))
		if m == nil {
			continue
		}
		kind := w25OnHandWriteMatchKind(m)
		if kind == "" {
			continue
		}
		sites = append(sites, w25InventoryWriterSite{
			file: rel,
			line: line,
			fn:   "(sql)",
			kind: kind,
		})
	}
	return sites, nil
}

// w25CallName returns the final selector or identifier name of a call.
func w25CallName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fun.Sel.Name
	case *ast.Ident:
		return fun.Name
	case *ast.CallExpr:
		return w25CallName(fun)
	}
	return ""
}

// w25FormatFuncName formats a function declaration as "Func" or "(Recv).Func".
func w25FormatFuncName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	typ := fn.Recv.List[0].Type
	return "(" + w25TypeString(typ) + ")." + fn.Name.Name
}

// w25TypeString returns a readable form of a receiver type.
func w25TypeString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + w25TypeString(t.X)
	case *ast.SelectorExpr:
		return w25TypeString(t.X) + "." + t.Sel.Name
	}
	return "?"
}
