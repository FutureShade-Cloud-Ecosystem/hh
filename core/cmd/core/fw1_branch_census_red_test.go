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

	t.Run("dodge_new_gl_service_method", func(t *testing.T) {
		tmpRoot := fw1WriteProbeFile(t, `package probe

func (s *Svc) sneakNewGLMethod() {
	_, _ = s.gl.ZzS4Book(nil, nil)
}
`)
		glDir := filepath.Join(tmpRoot, "internal", "gl")
		if err := os.MkdirAll(glDir, 0o755); err != nil {
			t.Fatalf("mkdir gl: %v", err)
		}
		glSrc := `package gl

type Service struct{ store glStore }

func (s *Service) CreateJournal()   { s.store.InsertJournal() }
func (s *Service) ReverseJournal()  { s.store.InsertJournal() }
func (s *Service) RunRecurring()    { s.store.InsertJournal() }
func (s *Service) CloseYear()       { s.store.InsertJournal() }
func (s *Service) PostExternal()    { s.postExternal() }
func (s *Service) PostExternalTx()  { s.postExternalTx() }
func (s *Service) ZzS4Book()        { s.postExternal() }
func (s *Service) postExternal()    { s.store.InsertJournal() }
func (s *Service) postExternalTx()  { s.store.InsertJournalTx() }
`
		if err := os.WriteFile(filepath.Join(glDir, "gl.go"), []byte(glSrc), 0o644); err != nil {
			t.Fatalf("write gl.go: %v", err)
		}
		probeErrs, err := fw1VerifyNonGLSites(tmpRoot, nil)
		if err != nil {
			t.Fatalf("scan probe: %v", err)
		}
		if !fw1HasErrorContaining(probeErrs, "ZzS4Book") || !fw1HasErrorContaining(probeErrs, "kind=ZzS4Book") {
			t.Fatalf("RED dodge probe failed: new exported gl.Service writer method ZzS4Book and its caller were not caught (errors: %v)", probeErrs)
		}
	})

	t.Run("dodge_concat_sql_and_topic", func(t *testing.T) {
		tmpRoot := fw1WriteProbeFile(t, `package probe

const zzS4Prefix = "till.session."

func (s *Svc) sneakConcat() {
	_, _ = s.tx.Exec(nil, "INSERT INTO events_outbox (topic, payload) "+"VALUES ('till.session.completed', '{}')")
	_ = s.repo.EmitEvent(nil, nil, "till.session."+"completed", nil, nil)
	_ = emitOutbox(nil, nil, zzS4Prefix+"completed", nil)
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
		if count != 3 {
			t.Fatalf("RED dodge probe failed: expected 3 emit:till.session.completed errors from string concatenation, got %d (errors: %v)", count, probeErrs)
		}
	})

	t.Run("dodge_unrecorded_events_outbox_literal", func(t *testing.T) {
		tmpRoot := fw1WriteProbeFile(t, `package probe

func (s *Svc) sneakUnrecordedOutboxLiteral() {
	q := "INSERT INTO events_outbox (topic, payload) VALUES ('till.session.completed', '{}')"
	_, _ = s.tx.Exec(nil, q)
}
`)
		probeErrs, err := fw1VerifyNonGLSites(tmpRoot, nil)
		if err != nil {
			t.Fatalf("scan probe: %v", err)
		}
		if !fw1HasErrorContaining(probeErrs, "UNRECORDED events_outbox string literal") {
			t.Fatalf("RED dodge probe failed: unrecorded events_outbox literal was not flagged (errors: %v)", probeErrs)
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
	expectedGLServiceWriters := map[string]bool{
		"CreateJournal":  true,
		"ReverseJournal": true,
		"RunRecurring":   true,
		"CloseYear":      true,
		"PostExternal":   true,
		"PostExternalTx": true,
		// ParkExternalJournalTx is the parked journal a credit memo waits on
		// (FW1-12a): it reaches the journal insert path, so it is a writer.
		"ParkExternalJournalTx": true,
	}
	writerCalls := map[string]bool{
		"PostExternal":          true,
		"PostExternalTx":        true,
		"PostJournal":           true,
		"PostJournalTx":         true,
		"PostCreditMemoJournal": true,
		"ParkExternalJournalTx": true,
		"CreateJournal":         true,
		"ReverseJournal":        true,
		"RunRecurring":          true,
		"CloseYear":             true,
		"InsertJournal":         true,
		"InsertJournalTx":       true,
		"InsertParkedJournal":   true,
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

	type parsedFile struct {
		rel     string
		pkgDir  string
		pkgName string
		ast     *ast.File
	}

	fset := token.NewFileSet()
	var files []parsedFile
	var glFiles []parsedFile
	dirConsts := make(map[string]map[string]string)
	pkgConsts := make(map[string]map[string]string)
	dirConstExprs := make(map[string]map[string]ast.Expr)
	pkgConstExprs := make(map[string]map[string]ast.Expr)

	for _, sub := range []string{"cmd", "internal"} {
		root := filepath.Join(coreRoot, sub)
		if _, statErr := os.Stat(root); os.IsNotExist(statErr) {
			continue
		}
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			relFromCore, rerr := filepath.Rel(coreRoot, path)
			if rerr != nil {
				return rerr
			}
			rel := filepath.ToSlash(filepath.Join("core", relFromCore))
			if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
				return nil
			}
			fileAST, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				return fmt.Errorf("parse %s: %w", rel, perr)
			}
			pkgDir := filepath.ToSlash(filepath.Dir(rel))
			pkgName := fileAST.Name.Name
			pf := parsedFile{
				rel:     rel,
				pkgDir:  pkgDir,
				pkgName: pkgName,
				ast:     fileAST,
			}
			files = append(files, pf)
			if rel == "core/internal/gl" || strings.HasPrefix(rel, "core/internal/gl/") {
				glFiles = append(glFiles, pf)
			}
			if dirConsts[pkgDir] == nil {
				dirConsts[pkgDir] = make(map[string]string)
				dirConstExprs[pkgDir] = make(map[string]ast.Expr)
			}
			if pkgConsts[pkgName] == nil {
				pkgConsts[pkgName] = make(map[string]string)
				pkgConstExprs[pkgName] = make(map[string]ast.Expr)
			}
			for _, decl := range fileAST.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok || gd.Tok != token.CONST {
					continue
				}
				for _, spec := range gd.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, name := range vs.Names {
						if i >= len(vs.Values) {
							continue
						}
						dirConstExprs[pkgDir][name.Name] = vs.Values[i]
						pkgConstExprs[pkgName][name.Name] = vs.Values[i]
						if s, ok := fw1StringLiteral(vs.Values[i]); ok {
							dirConsts[pkgDir][name.Name] = s
							pkgConsts[pkgName][name.Name] = s
						}
					}
				}
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walking %s: %w", root, err)
		}
	}

	var resolveString func(pf parsedFile, e ast.Expr) (string, bool)
	resolveString = func(pf parsedFile, e ast.Expr) (string, bool) {
		if s, ok := fw1StringLiteral(e); ok {
			return s, true
		}
		switch v := e.(type) {
		case *ast.ParenExpr:
			return resolveString(pf, v.X)
		case *ast.BinaryExpr:
			if v.Op == token.ADD {
				lx, okX := resolveString(pf, v.X)
				ly, okY := resolveString(pf, v.Y)
				if okX && okY {
					return lx + ly, true
				}
			}
		case *ast.Ident:
			if v.Obj != nil && v.Obj.Kind != ast.Con {
				return "", false
			}
			if s, ok := dirConsts[pf.pkgDir][v.Name]; ok {
				return s, true
			}
		case *ast.SelectorExpr:
			pkgIdent, ok := v.X.(*ast.Ident)
			if !ok {
				return "", false
			}
			if m, ok := pkgConsts[pkgIdent.Name]; ok {
				if s, ok := m[v.Sel.Name]; ok {
					return s, true
				}
			}
			for _, imp := range pf.ast.Imports {
				if imp.Name != nil && imp.Name.Name == pkgIdent.Name {
					if impPath, err := strconv.Unquote(imp.Path.Value); err == nil {
						basePkg := filepath.Base(impPath)
						if m, ok := pkgConsts[basePkg]; ok {
							if s, ok := m[v.Sel.Name]; ok {
								return s, true
							}
						}
					}
				}
			}
		}
		return "", false
	}

	// Fold multi-part const expressions (such as const B = A + "suffix").
	for pass := 0; pass < 3; pass++ {
		for _, pf := range files {
			for _, decl := range pf.ast.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok || gd.Tok != token.CONST {
					continue
				}
				for _, spec := range gd.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, name := range vs.Names {
						if i >= len(vs.Values) {
							continue
						}
						if s, ok := resolveString(pf, vs.Values[i]); ok {
							dirConsts[pf.pkgDir][name.Name] = s
							pkgConsts[pf.pkgName][name.Name] = s
						}
					}
				}
			}
		}
	}

	var errs []string

	// Derive the exported gl.Service writer method set from core/internal/gl itself.
	if len(glFiles) > 0 {
		glReachesInsert := map[string]bool{
			"InsertJournal":       true,
			"InsertJournalTx":     true,
			"InsertParkedJournal": true,
			"postExternal":        true,
			"postExternalTx":      true,
			"insertJournalIn":     true,
			// The year end's dedicated store insert (FW1-09b round 5): CloseYear
			// reaches the journal insert path through it in the merged tree.
			"InsertClosingJournalTx": true,
		}
		glDispatcherMethods := map[string]bool{
			"consume":                 true,
			"postEvent":               true,
			"RegisterPostingHandlers": true,
		}
		var serviceMethods []*ast.FuncDecl
		for _, pf := range glFiles {
			for _, decl := range pf.ast.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil || fn.Recv == nil || len(fn.Recv.List) == 0 {
					continue
				}
				recvType := fw1FormatExpr(fn.Recv.List[0].Type)
				if recvType == "Service" || recvType == "*Service" {
					if !glDispatcherMethods[fn.Name.Name] {
						serviceMethods = append(serviceMethods, fn)
					}
				}
			}
		}
		changed := true
		for changed {
			changed = false
			for _, fn := range serviceMethods {
				if glReachesInsert[fn.Name.Name] {
					continue
				}
				reaches := false
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					if sel, ok := n.(*ast.SelectorExpr); ok && glReachesInsert[sel.Sel.Name] {
						reaches = true
						return false
					}
					return true
				})
				if reaches {
					glReachesInsert[fn.Name.Name] = true
					changed = true
				}
			}
		}
		derivedGLServiceWriters := make(map[string]bool)
		for _, fn := range serviceMethods {
			name := fn.Name.Name
			if ast.IsExported(name) && glReachesInsert[name] {
				derivedGLServiceWriters[name] = true
				writerCalls[name] = true
				if !expectedGLServiceWriters[name] {
					errs = append(errs, fmt.Sprintf("UNLISTED exported gl.Service writer method %s in core/internal/gl reaches journal insert path (add to expectedGLServiceWriters, writerCalls, and TestFW1JournalLinesCarryBranch)", name))
				}
			}
		}
		for name := range expectedGLServiceWriters {
			if !derivedGLServiceWriters[name] {
				errs = append(errs, fmt.Sprintf("STALE expectedGLServiceWriters entry %s no longer reaches journal insert path in core/internal/gl", name))
			}
		}
	}

	recordedOutboxLits := make(map[*ast.BasicLit]bool)
	var markExprLits func(pf parsedFile, e ast.Expr)
	markExprLits = func(pf parsedFile, e ast.Expr) {
		if e == nil {
			return
		}
		ast.Inspect(e, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.BasicLit:
				recordedOutboxLits[v] = true
			case *ast.Ident:
				if v.Obj == nil || v.Obj.Kind == ast.Con {
					if ce, ok := dirConstExprs[pf.pkgDir][v.Name]; ok && ce != e {
						markExprLits(pf, ce)
					}
				}
			case *ast.SelectorExpr:
				if pkgIdent, ok := v.X.(*ast.Ident); ok {
					if m, ok := pkgConstExprs[pkgIdent.Name]; ok {
						if ce, ok := m[v.Sel.Name]; ok && ce != e {
							markExprLits(pf, ce)
						}
					}
				}
			}
			return true
		})
	}

	var discovered []fw1FoundSite
	for _, pf := range files {
		if pf.rel == "core/internal/gl" || strings.HasPrefix(pf.rel, "core/internal/gl/") {
			continue
		}
		for _, decl := range pf.ast.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			fnName := fw1FormatFuncName(fn)
			recordOutboxCall := func(callName string, pos token.Pos, args []ast.Expr) {
				line := fset.Position(pos).Line
				if len(args) >= 3 {
					if tp, ok := resolveString(pf, args[2]); ok {
						if payloadTopicSet[tp] {
							kind := "emit:" + tp
							key := pf.rel + "|" + fnName + "|" + kind
							discovered = append(discovered, fw1FoundSite{
								key: key, file: pf.rel, fn: fnName, kind: kind, line: line,
							})
						}
						return
					}
				}
				kind := "dynamic-topic:" + callName
				key := pf.rel + "|" + fnName + "|" + kind
				discovered = append(discovered, fw1FoundSite{
					key: key, file: pf.rel, fn: fnName, kind: kind, line: line,
				})
			}

			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok {
					name := sel.Sel.Name
					if writerCalls[name] {
						line := fset.Position(sel.Sel.Pos()).Line
						key := pf.rel + "|" + fnName + "|" + name
						discovered = append(discovered, fw1FoundSite{
							key: key, file: pf.rel, fn: fnName, kind: name, line: line,
						})
					}
				}
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch fun := call.Fun.(type) {
				case *ast.SelectorExpr:
					name := fun.Sel.Name
					if name == "EmitEvent" || name == "EnqueueTx" || name == "emitOutbox" {
						recordOutboxCall(name, fun.Sel.Pos(), call.Args)
						return true
					}
					if name == "Exec" || name == "QueryRow" {
						for argIdx, arg := range call.Args {
							sqlStr, ok := resolveString(pf, arg)
							if !ok || !strings.Contains(strings.ToUpper(sqlStr), "INSERT INTO EVENTS_OUTBOX") {
								continue
							}
							markExprLits(pf, arg)
							line := fset.Position(fun.Sel.Pos()).Line
							tp, resolved := fw1ExtractOutboxSQLTopic(sqlStr, call.Args[argIdx+1:], func(e ast.Expr) (string, bool) {
								return resolveString(pf, e)
							})
							if resolved {
								if payloadTopicSet[tp] {
									kind := "emit:" + tp
									key := pf.rel + "|" + fnName + "|" + kind
									discovered = append(discovered, fw1FoundSite{
										key: key, file: pf.rel, fn: fnName, kind: kind, line: line,
									})
								}
							} else {
								kind := "dynamic-topic:INSERT INTO events_outbox"
								key := pf.rel + "|" + fnName + "|" + kind
								discovered = append(discovered, fw1FoundSite{
									key: key, file: pf.rel, fn: fnName, kind: kind, line: line,
								})
							}
						}
					}
				case *ast.Ident:
					if fun.Name == "emitOutbox" {
						recordOutboxCall("emitOutbox", fun.Pos(), call.Args)
					}
				}
				return true
			})
		}

		if pf.rel != "core/cmd/core/fw1_branch_census_red.go" {
			ast.Inspect(pf.ast, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING || recordedOutboxLits[lit] {
					return true
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil || !strings.Contains(strings.ToLower(s), "events_outbox") {
					return true
				}
				if (pf.rel == "core/internal/notify/store.go" || pf.rel == "core/internal/notify/requeue.go") &&
					!strings.Contains(strings.ToUpper(s), "INSERT") {
					return true
				}
				errs = append(errs, fmt.Sprintf("UNRECORDED events_outbox string literal at %s:%d: %q was not recorded by the outbox AST scanner",
					pf.rel, fset.Position(lit.Pos()).Line, s))
				return true
			})
		}
	}

	sort.Slice(discovered, func(i, j int) bool {
		if discovered[i].file != discovered[j].file {
			return discovered[i].file < discovered[j].file
		}
		return discovered[i].line < discovered[j].line
	})

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

	foundCounts := make(map[string]int, len(discovered))
	for _, site := range discovered {
		foundCounts[site.key]++
		if _, ok := tableByKey[site.key]; !ok {
			errs = append(errs, fmt.Sprintf("UNLISTED non-GL journal writer / payload-branch emitter at %s:%d: func=%s kind=%s (add to fw1NonGLJournalWriterCallSites with its branch source)",
				site.file, site.line, site.fn, site.kind))
		} else if foundCounts[site.key] > 1 {
			errs = append(errs, fmt.Sprintf("DUPLICATE call site in same function at %s:%d: func=%s kind=%s (found %d occurrences, table lists 1)",
				site.file, site.line, site.fn, site.kind, foundCounts[site.key]))
		}
	}

	for key, entry := range tableByKey {
		if foundCounts[key] == 0 {
			errs = append(errs, fmt.Sprintf("STALE entry in fw1NonGLJournalWriterCallSites not found in source tree: file=%s func=%s kind=%s",
				entry.File, entry.Func, entry.Kind))
		}
	}
	return errs, nil
}

func fw1ExtractOutboxSQLTopic(sqlStr string, extraArgs []ast.Expr, resolve func(ast.Expr) (string, bool)) (string, bool) {
	upper := strings.ToUpper(sqlStr)
	idx := strings.Index(upper, "INSERT INTO EVENTS_OUTBOX")
	if idx < 0 {
		return "", false
	}
	rest := sqlStr[idx+len("INSERT INTO EVENTS_OUTBOX"):]
	restUpper := upper[idx+len("INSERT INTO EVENTS_OUTBOX"):]
	valIdx := strings.Index(restUpper, "VALUES")
	if valIdx < 0 {
		return "", false
	}
	colsPart := rest[:valIdx]
	valsPart := rest[valIdx+len("VALUES"):]

	topicColIdx := 0
	if openCol := strings.IndexByte(colsPart, '('); openCol >= 0 {
		if closeCol := strings.IndexByte(colsPart[openCol+1:], ')'); closeCol >= 0 {
			cols := strings.Split(colsPart[openCol+1:openCol+1+closeCol], ",")
			foundCol := false
			for i, c := range cols {
				if strings.EqualFold(strings.TrimSpace(c), "topic") {
					topicColIdx = i
					foundCol = true
					break
				}
			}
			if !foundCol {
				return "", false
			}
		}
	}

	openVal := strings.IndexByte(valsPart, '(')
	if openVal < 0 {
		return "", false
	}
	closeVal := strings.IndexByte(valsPart[openVal+1:], ')')
	if closeVal < 0 {
		return "", false
	}
	vals := strings.Split(valsPart[openVal+1:openVal+1+closeVal], ",")
	if topicColIdx >= len(vals) {
		return "", false
	}
	rawVal := strings.TrimSpace(vals[topicColIdx])
	if len(rawVal) >= 2 && ((rawVal[0] == '\'' && rawVal[len(rawVal)-1] == '\'') || (rawVal[0] == '"' && rawVal[len(rawVal)-1] == '"')) {
		return rawVal[1 : len(rawVal)-1], true
	}
	if strings.HasPrefix(rawVal, "$") {
		paramStr := strings.TrimPrefix(rawVal, "$")
		if colonIdx := strings.IndexByte(paramStr, ':'); colonIdx >= 0 {
			paramStr = paramStr[:colonIdx]
		}
		paramNum, err := strconv.Atoi(strings.TrimSpace(paramStr))
		if err != nil || paramNum < 1 || paramNum > len(extraArgs) {
			return "", false
		}
		return resolve(extraArgs[paramNum-1])
	}
	return "", false
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
