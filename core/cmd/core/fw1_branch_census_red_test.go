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

type fw1FoundSite struct {
	key  string
	file string
	fn   string
	kind string
	line int
}

// TestFW1NonGLJournalWriterCallSitesComplete scans the Go source tree outside
// core/internal/gl for every PostExternal, PostExternalTx, PostJournal,
// PostJournalTx, PostCreditMemoJournal, CreateJournal, ReverseJournal,
// RunRecurring, and CloseYear selector and every emitter of an outbox topic
// whose payload carries a branch (plus any dynamic-topic outbox emitter), and
// fails on any site not listed in fw1NonGLJournalWriterCallSites or any stale
// entry in that table.
func TestFW1NonGLJournalWriterCallSitesComplete(t *testing.T) {
	coreRoot := filepath.Clean(filepath.Join("..", ".."))
	errs, err := fw1VerifyNonGLSites(coreRoot, fw1NonGLJournalWriterCallSites)
	if err != nil {
		t.Fatalf("scanning %s: %v", coreRoot, err)
	}
	for _, e := range errs {
		t.Error(e)
	}

	t.Run("dodge_method_value", func(t *testing.T) {
		tmpRoot := fw1WriteProbeFile(t, `package probe

func (s *Svc) sneakMethodValue() {
	post := s.gl.PostJournal
	_, _ = post(nil, nil)
}
`)
		probeErrs, err := fw1VerifyNonGLSites(tmpRoot, nil)
		if err != nil {
			t.Fatalf("scan probe: %v", err)
		}
		if !fw1HasErrorContaining(probeErrs, "kind=PostJournal") {
			t.Fatalf("RED dodge probe failed: method-value selector s.gl.PostJournal was not caught by scanner (errors: %v)", probeErrs)
		}
	})

	t.Run("dodge_direct_gl_methods", func(t *testing.T) {
		tmpRoot := fw1WriteProbeFile(t, `package probe

func (s *Svc) sneakDirectGL() {
	_, _ = s.gl.CreateJournal(nil, nil)
	_, _ = s.gl.ReverseJournal(nil, nil, nil)
	_, _ = s.gl.RunRecurring(nil, nil)
	_, _ = s.gl.CloseYear(nil, nil)
}
`)
		probeErrs, err := fw1VerifyNonGLSites(tmpRoot, nil)
		if err != nil {
			t.Fatalf("scan probe: %v", err)
		}
		for _, want := range []string{"CreateJournal", "ReverseJournal", "RunRecurring", "CloseYear"} {
			if !fw1HasErrorContaining(probeErrs, "kind="+want) {
				t.Fatalf("RED dodge probe failed: direct GL writer %s was not caught by scanner (errors: %v)", want, probeErrs)
			}
		}
	})

	t.Run("dodge_const_topic", func(t *testing.T) {
		tmpRoot := fw1WriteProbeFile(t, `package probe

const zzS4Topic = "till.session.completed"

func (s *Svc) sneakConstTopic() {
	_, _ = s.tx.Exec(nil, "INSERT INTO events_outbox (topic, payload) VALUES ($1, '{}')", zzS4Topic)
	_ = s.repo.EmitEvent(nil, nil, zzS4Topic, nil, nil)
	_ = emitOutbox(nil, nil, zzS4Topic, nil)
	_ = s.outbox.EnqueueTx(nil, nil, zzS4Topic, nil, nil)
}
`)
		probeErrs, err := fw1VerifyNonGLSites(tmpRoot, nil)
		if err != nil {
			t.Fatalf("scan probe: %v", err)
		}
		count := 0
		for _, e := range probeErrs {
			if strings.Contains(e, "kind=emit:till.session.completed") {
				count++
			}
		}
		if count != 4 {
			t.Fatalf("RED dodge probe failed: expected 4 emit:till.session.completed errors from const topic across Exec/EmitEvent/emitOutbox/EnqueueTx, got %d (errors: %v)", count, probeErrs)
		}
	})

	t.Run("dodge_dynamic_topic", func(t *testing.T) {
		tmpRoot := fw1WriteProbeFile(t, `package probe

func (s *Svc) sneakDynamicTopic(dynTopic string) {
	_, _ = s.tx.Exec(nil, "INSERT INTO events_outbox (topic, payload) VALUES ($1, $2)", dynTopic, nil)
	_ = s.repo.EmitEvent(nil, nil, dynTopic, nil, nil)
	_ = emitOutbox(nil, nil, dynTopic, nil)
	_ = s.outbox.EnqueueTx(nil, nil, dynTopic, nil, nil)
}
`)
		probeErrs, err := fw1VerifyNonGLSites(tmpRoot, nil)
		if err != nil {
			t.Fatalf("scan probe: %v", err)
		}
		for _, want := range []string{
			"kind=dynamic-topic:INSERT INTO events_outbox",
			"kind=dynamic-topic:EmitEvent",
			"kind=dynamic-topic:emitOutbox",
			"kind=dynamic-topic:EnqueueTx",
		} {
			if !fw1HasErrorContaining(probeErrs, want) {
				t.Fatalf("RED dodge probe failed: expected %s from dynamic topic probe, got: %v", want, probeErrs)
			}
		}
	})
}

func fw1WriteProbeFile(t *testing.T, src string) string {
	t.Helper()
	tmpRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpRoot, "cmd"), 0o755); err != nil {
		t.Fatalf("mkdir cmd: %v", err)
	}
	probeDir := filepath.Join(tmpRoot, "internal", "probe")
	if err := os.MkdirAll(probeDir, 0o755); err != nil {
		t.Fatalf("mkdir probe: %v", err)
	}
	if err := os.WriteFile(filepath.Join(probeDir, "probe.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write probe.go: %v", err)
	}
	return tmpRoot
}

func fw1HasErrorContaining(errs []string, sub string) bool {
	for _, e := range errs {
		if strings.Contains(e, sub) {
			return true
		}
	}
	return false
}

func fw1VerifyNonGLSites(coreRoot string, table []fw1NonGLWriterSite) ([]string, error) {
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

	fset := token.NewFileSet()
	var discovered []fw1FoundSite

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
							discovered = append(discovered, fw1FoundSite{
								key: key, file: rel, fn: fnName, kind: name, line: line,
							})
							return true
						}
						if name == "EmitEvent" && len(call.Args) >= 3 {
							if tp, ok := fw1StringLiteral(call.Args[2]); ok && payloadTopicSet[tp] {
								line := fset.Position(fun.Sel.Pos()).Line
								kind := "emit:" + tp
								key := rel + "|" + fnName + "|" + kind
								discovered = append(discovered, fw1FoundSite{
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
										discovered = append(discovered, fw1FoundSite{
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
								discovered = append(discovered, fw1FoundSite{
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
			return nil, fmt.Errorf("walking %s: %w", root, err)
		}
	}

	sort.Slice(discovered, func(i, j int) bool {
		if discovered[i].file != discovered[j].file {
			return discovered[i].file < discovered[j].file
		}
		return discovered[i].line < discovered[j].line
	})

	var errs []string
	tableByKey := make(map[string]fw1NonGLWriterSite, len(table))
	for _, entry := range table {
		key := entry.File + "|" + entry.Func + "|" + entry.Kind
		if _, dup := tableByKey[key]; dup {
			errs = append(errs, fmt.Sprintf("duplicate entry in fw1NonGLJournalWriterCallSites: %s (%s -> %s)", entry.File, entry.Func, entry.Kind))
		}
		if strings.TrimSpace(entry.BranchSource) == "" {
			errs = append(errs, fmt.Sprintf("fw1NonGLJournalWriterCallSites entry %s (%s -> %s) has empty BranchSource", entry.File, entry.Func, entry.Kind))
		}
		tableByKey[key] = entry
	}

	foundKeys := make(map[string]bool, len(discovered))
	for _, site := range discovered {
		foundKeys[site.key] = true
		if _, ok := tableByKey[site.key]; !ok {
			errs = append(errs, fmt.Sprintf("UNLISTED non-GL journal writer / payload-branch emitter at %s:%d: func=%s kind=%s (add to fw1NonGLJournalWriterCallSites with its branch source)",
				site.file, site.line, site.fn, site.kind))
		}
	}

	for key, entry := range tableByKey {
		if !foundKeys[key] {
			errs = append(errs, fmt.Sprintf("STALE entry in fw1NonGLJournalWriterCallSites not found in source tree: file=%s func=%s kind=%s",
				entry.File, entry.Func, entry.Kind))
		}
	}
	return errs, nil
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
