package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestFW1BranchCensusRedMapNamesRealWriters(t *testing.T) {
	known := make(map[string]bool, len(fw1BranchCensusWriters))
	for _, w := range fw1BranchCensusWriters {
		if known[w] {
			t.Fatalf("duplicate writer %q in fw1BranchCensusWriters", w)
		}
		known[w] = true
	}
	for writer, owner := range fw1BranchCensusRed {
		if !known[writer] {
			t.Errorf("fw1BranchCensusRed names unknown writer %q (stale or misspelled entry)", writer)
		}
		if owner != "FW1-05" && owner != "FW1-08b" {
			t.Errorf("fw1BranchCensusRed[%q] has owner %q, want FW1-05 or FW1-08b", writer, owner)
		}
	}
	// Verify every green writer is absent from fw1BranchCensusRed so adding a
	// green writer to fw1BranchCensusRed is caught by TestFW1JournalLinesCarryBranch.
	if fw1BranchCensusRed["manual_journal_with_branch"] != "" {
		t.Errorf("manual_journal_with_branch already stamps branch_id and must not be in fw1BranchCensusRed")
	}
}

func TestFW1BranchCensusRedEmptyOnWaveFinal(t *testing.T) {
	if os.Getenv("FW1_WAVE_FINAL") != "1" {
		return
	}
	if len(fw1BranchCensusRed) > 0 {
		t.Fatalf("FW1_WAVE_FINAL=1 requires fw1BranchCensusRed to be empty, still has %d entries: %v", len(fw1BranchCensusRed), fw1BranchCensusRed)
	}
}

// TestFW1NonGLJournalWriterCallSitesComplete scans the Go source tree outside
// core/internal/gl for every PostExternal, PostExternalTx, PostJournal,
// PostJournalTx, and PostCreditMemoJournal call and every emitter of an outbox
// topic whose payload carries a branch, and fails on any call site not listed
// in fw1NonGLJournalWriterCallSites or any stale entry in that table.
func TestFW1NonGLJournalWriterCallSitesComplete(t *testing.T) {
	writerCalls := map[string]bool{
		"PostExternal":          true,
		"PostExternalTx":        true,
		"PostJournal":           true,
		"PostJournalTx":         true,
		"PostCreditMemoJournal": true,
	}
	payloadBranchTopics := []string{
		"payment.bounced",
		"payment.reversed",
		"payment.reapplied",
		"order.fulfilled",
		"pos.return.committed",
		"order.returned",
		"pos.return.written_off",
		"till.session.completed",
		"po.received",
		"inventory.adjusted",
		"tax.relief.assessed",
	}
	payloadTopicSet := make(map[string]bool, len(payloadBranchTopics))
	for _, tp := range payloadBranchTopics {
		payloadTopicSet[tp] = true
	}

	type foundSite struct {
		key  string
		file string
		fn   string
		kind string
		line int
	}

	coreRoot := filepath.Clean(filepath.Join("..", ".."))
	fset := token.NewFileSet()
	var discovered []foundSite

	for _, sub := range []string{"cmd", "internal"} {
		root := filepath.Join(coreRoot, sub)
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			relFromCore, rerr := filepath.Rel(coreRoot, path)
			if rerr != nil {
				return rerr
			}
			rel := filepath.ToSlash(filepath.Join("core", relFromCore))
			if d.IsDir() {
				if rel == "core/internal/gl" || strings.HasPrefix(rel, "core/internal/gl/") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
				return nil
			}
			fileAST, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				return fmt.Errorf("parse %s: %w", rel, perr)
			}
			for _, decl := range fileAST.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				fnName := fw1FormatFuncName(fn)
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					switch fun := call.Fun.(type) {
					case *ast.SelectorExpr:
						name := fun.Sel.Name
						if writerCalls[name] {
							line := fset.Position(fun.Sel.Pos()).Line
							key := rel + "|" + fnName + "|" + name
							discovered = append(discovered, foundSite{
								key: key, file: rel, fn: fnName, kind: name, line: line,
							})
							return true
						}
						if name == "EmitEvent" && len(call.Args) >= 3 {
							if tp, ok := fw1StringLiteral(call.Args[2]); ok && payloadTopicSet[tp] {
								line := fset.Position(fun.Sel.Pos()).Line
								kind := "emit:" + tp
								key := rel + "|" + fnName + "|" + kind
								discovered = append(discovered, foundSite{
									key: key, file: rel, fn: fnName, kind: kind, line: line,
								})
							}
							return true
						}
						if name == "Exec" || name == "QueryRow" {
							for _, arg := range call.Args {
								s, ok := fw1StringLiteral(arg)
								if !ok || !strings.Contains(s, "INSERT INTO events_outbox") {
									continue
								}
								for _, tp := range payloadBranchTopics {
									if strings.Contains(s, "'"+tp+"'") || strings.Contains(s, `"`+tp+`"`) {
										line := fset.Position(fun.Sel.Pos()).Line
										kind := "emit:" + tp
										key := rel + "|" + fnName + "|" + kind
										discovered = append(discovered, foundSite{
											key: key, file: rel, fn: fnName, kind: kind, line: line,
										})
									}
								}
							}
						}
					case *ast.Ident:
						if fun.Name == "emitOutbox" && len(call.Args) >= 3 {
							if tp, ok := fw1StringLiteral(call.Args[2]); ok && payloadTopicSet[tp] {
								line := fset.Position(fun.Pos()).Line
								kind := "emit:" + tp
								key := rel + "|" + fnName + "|" + kind
								discovered = append(discovered, foundSite{
									key: key, file: rel, fn: fnName, kind: kind, line: line,
								})
							}
						}
					}
					return true
				})
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}

	sort.Slice(discovered, func(i, j int) bool {
		if discovered[i].file != discovered[j].file {
			return discovered[i].file < discovered[j].file
		}
		return discovered[i].line < discovered[j].line
	})

	tableByKey := make(map[string]fw1NonGLWriterSite, len(fw1NonGLJournalWriterCallSites))
	for _, entry := range fw1NonGLJournalWriterCallSites {
		key := entry.File + "|" + entry.Func + "|" + entry.Kind
		if _, dup := tableByKey[key]; dup {
			t.Errorf("duplicate entry in fw1NonGLJournalWriterCallSites: %s (%s -> %s)", entry.File, entry.Func, entry.Kind)
		}
		if strings.TrimSpace(entry.BranchSource) == "" {
			t.Errorf("fw1NonGLJournalWriterCallSites entry %s (%s -> %s) has empty BranchSource", entry.File, entry.Func, entry.Kind)
		}
		tableByKey[key] = entry
	}

	foundKeys := make(map[string]bool, len(discovered))
	for _, site := range discovered {
		foundKeys[site.key] = true
		if _, ok := tableByKey[site.key]; !ok {
			t.Errorf("UNLISTED non-GL journal writer / payload-branch emitter at %s:%d: func=%s kind=%s (add to fw1NonGLJournalWriterCallSites with its branch source)",
				site.file, site.line, site.fn, site.kind)
		}
	}

	for key, entry := range tableByKey {
		if !foundKeys[key] {
			t.Errorf("STALE entry in fw1NonGLJournalWriterCallSites not found in source tree: file=%s func=%s kind=%s",
				entry.File, entry.Func, entry.Kind)
		}
	}
}

func fw1FormatFuncName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	return "(" + fw1FormatExpr(fn.Recv.List[0].Type) + ")." + fn.Name.Name
}

func fw1FormatExpr(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.StarExpr:
		return "*" + fw1FormatExpr(v.X)
	case *ast.Ident:
		return v.Name
	default:
		return fmt.Sprintf("%T", e)
	}
}

func fw1StringLiteral(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return s, true
}
