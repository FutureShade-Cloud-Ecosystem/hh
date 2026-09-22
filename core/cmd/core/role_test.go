package main

// role_test.go pins the lane's two role-split acceptance lines:
//
//   a test asserts serve starts no scheduler (structural: the serve
//   construction path references no Run method of the five loops, and no
//   goroutine at all), and the outbox drains from worker and not from
//   serve (the drain machinery exists only in the worker; a queued row is
//   claimed only when the worker-shaped dispatcher runs).
//
// The structural halves are source-level by design: a wiring mistake that
// strands a loop in serve is a GRAPH fact no DB-free boot can see, and the
// red proof for it is mechanical (a scratch scheduler registration in
// serve fails the scan).

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/portal"
	"os"
)

// serveScopeNames are the functions that make up serve's construction
// path. None of them may start a goroutine, call a loop's Run, build a
// notify.Dispatcher, or call DrainOnce.
var serveScopeNames = map[string]bool{
	"main":            true,
	"serve":           true,
	"registerRoutes":  true,
	"listenAddr":      true,
	"loadServeConfig": true,
	"buildSmsBelt":    true,
	"wireMoney":       true,
	"wireOperations":  true,
	"wireTops":        true,
	"auditWrap":       true,
	"healthzHandler":  true,
}

// TestServeStartsNoScheduler walks every non-test source file of this
// package and asserts: every goroutine launch, every loop Run call, every
// DrainOnce call and every notify.NewDispatcher construction lives inside
// the worker function (or the adapters no role calls); the serve-scope
// functions contain none of them. The worker's own Run sites are the
// positive control: exactly the five loops the role owns.
func TestServeStartsNoScheduler(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("cannot list package sources: %v", err)
	}
	fset := token.NewFileSet()
	loopRunSites := map[string][]string{} // enclosing func -> sites
	dispatcherSites := map[string][]string{}
	goroutineSites := map[string][]string{}
	drainSites := map[string][]string{}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			name := fn.Name.Name
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.GoStmt:
					// The listener goroutine is serve's one allowed
					// goroutine: its body is ListenAndServe (and nothing
					// that drains or schedules). Anything else fails.
					gs := fset.Position(node.Pos()).String()
					goroutineSites[name] = append(goroutineSites[name], gs)
					if fn.Name.Name == "serve" {
						var body strings.Builder
						ast.Inspect(node.Call, func(m ast.Node) bool {
							if call, ok := m.(*ast.CallExpr); ok {
								if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
									body.WriteString(sel.Sel.Name + " ")
								}
							}
							return true
						})
						calls := body.String()
						if strings.Contains(calls, "ListenAndServe") &&
							!strings.Contains(calls, "Run") && !strings.Contains(calls, "DrainOnce") {
							// Remove ONLY this allowed site; never the whole
							// census key, or a second plain goroutine in
							// serve would pass invisibly (the A6 review's
							// blind-spot finding).
							kept := goroutineSites[name][:0]
							for _, site := range goroutineSites[name] {
								if site != gs {
									kept = append(kept, site)
								}
							}
							goroutineSites[name] = kept
						}
					}
				case *ast.CallExpr:
					switch fun := node.Fun.(type) {
					case *ast.SelectorExpr:
						switch fun.Sel.Name {
						case "Run":
							// A loop START is a Run launched as a goroutine
							// (go x.Run(ctx)); a job body calling a
							// sweep's Run inside the runner is work, not a
							// loop start.
							if _, isGo := node.Fun.(*ast.SelectorExpr); isGo && nodePosIsGoLaunch(fset, path, node.Pos()) {
								loopRunSites[name] = append(loopRunSites[name], fset.Position(node.Pos()).String())
							}
						case "DrainOnce":
							drainSites[name] = append(drainSites[name], fset.Position(node.Pos()).String())
						case "NewDispatcher":
							dispatcherSites[name] = append(dispatcherSites[name], fset.Position(node.Pos()).String())
						}
					case *ast.Ident:
						if fun.Name == "NewDispatcher" {
							dispatcherSites[name] = append(dispatcherSites[name], fset.Position(node.Pos()).String())
						}
					}
				}
				return true
			})
		}
	}

	// The serve scope is clean: no goroutines, no loop Runs, no drains, no
	// dispatcher construction.
	for fn := range serveScopeNames {
		for label, sites := range map[string]map[string][]string{
			"a goroutine launch":        goroutineSites,
			"a loop Run call":           loopRunSites,
			"a DrainOnce call":          drainSites,
			"a dispatcher construction": dispatcherSites,
		} {
			if got := sites[fn]; len(got) > 0 {
				t.Fatalf("serve's construction path (%s) contains %s at %v: serve owns the listener only, every loop that writes starts in the worker (D37)",
					fn, label, got)
			}
		}
	}

	// The positive control: the worker starts exactly the five loops.
	workerRuns := loopRunSites["worker"]
	if len(workerRuns) != 5 {
		t.Fatalf("worker starts %d loop Run sites (%v), want 5 (events dispatcher, sms dispatcher, jobs runner, retention, reconciler)", len(workerRuns), workerRuns)
	}
	// No other function starts a loop.
	for fn, sites := range loopRunSites {
		if fn != "worker" && len(sites) > 0 {
			t.Fatalf("function %s starts a loop at %v; only the worker role may", fn, sites)
		}
	}
	// Both dispatchers are built in the worker.
	if len(dispatcherSites["worker"]) != 2 {
		t.Fatalf("worker builds %d dispatchers, want 2 (events_outbox and sms_outbox)", len(dispatcherSites["worker"]))
	}
}

// nodePosIsGoLaunch reports whether the token at pos sits directly after
// the go keyword (the launch form `go x.Run(ctx)`), by re-parsing the one
// line and checking its first token.
func nodePosIsGoLaunch(fset *token.FileSet, path string, pos token.Pos) bool {
	line := fset.Position(pos).Line
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	lines := strings.Split(string(data), "\n")
	if line <= 0 || line > len(lines) {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(lines[line-1]), "go ")
}

// fakeOutbox is a one-row OutboxStore double: the settle callback runs
// only when the dispatcher claims, which is the drain the acceptance
// names.
type fakeOutbox struct {
	queued  int
	settled int
}

func (f *fakeOutbox) ProcessBatch(_ context.Context, _ int, settle func(context.Context, notify.Event) notify.Settlement) (int, error) {
	if f.queued == 0 {
		return 0, nil
	}
	f.queued--
	settle(context.Background(), notify.Event{Topic: portal.TopicInvite, Payload: []byte(`{}`)})
	f.settled++
	return 1, nil
}

// TestOutboxDrainsFromWorker_NotFromServe proves the drain behaviourally:
// a queued outbox row is claimed only through the worker's dispatcher
// shape (the registration set and the drain call the worker owns). The
// serve half is structural and already proven above: serve constructs no
// dispatcher at all, so no serve-side code path can claim a row.
func TestOutboxDrainsFromWorker_NotFromServe(t *testing.T) {
	store := &fakeOutbox{queued: 1}

	// The worker shape: the dispatcher with the portal's notification
	// handlers registered (the worker's registration order's first rows).
	d := notify.NewDispatcher(store, nil)
	portal.RegisterNotificationHandlers(d, notify.NewLogNotifier(nil), "https://yard.example", nil)

	// Before the worker's drain runs, the queued row is untouched.
	if store.settled != 0 {
		t.Fatalf("a row settled with no drain running: %d", store.settled)
	}
	n := d.DrainOnce(context.Background())
	if n != 1 || store.settled != 1 || store.queued != 0 {
		t.Fatalf("worker drain = %d rows, settled=%d queued=%d; the queued row must be claimed exactly when the worker drains", n, store.settled, store.queued)
	}

	// And the drained row does not resurrect: the second tick finds the
	// queue empty (the claim semantics the pin carries).
	if n := d.DrainOnce(context.Background()); n != 0 {
		t.Fatalf("second drain claimed %d rows, want 0", n)
	}

}
