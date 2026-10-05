package testaudit

// sync_create_census_test.go, the census of the sync Create conflict arm (hardening row 788).
//
// THE DEFECT CLASS. A sync scope store's Create opens a transaction, inserts with ON CONFLICT DO
// NOTHING RETURNING, and when the insert answers no row (the client chosen id is taken) it reads the
// row back to decide between "exists" and "not found". Read through the store's PUBLIC Load, that read
// goes through the shared pool while the transaction still holds its own connection: a second
// acquisition before the first is released. Pool-size such creates in flight starve the process.
// Fifteen stores carried it and each was fixed by an unexported loader over a querier that the
// conflict arm calls with the transaction (`s.load(ctx, tx, ...)`); each fix has its own pool-size leg.
//
// WHAT THIS CENSUS PINS. Every type in the module that has both a Create and a Load method is
// classified here, by an AST walk of the production sources (no database), into exactly one of:
//   - fixed: Create reaches the loader through the transaction (s.load(ctx, tx, ...)) and never
//     calls the public Load;
//   - noTxHeld: Create calls the public Load but holds no transaction of its own at that point
//     (each entry says why, read at source);
//   - fenced: Create still calls the public Load under its own transaction; the owning lane has not
//     fixed it yet (the entry names the owner). The census FAILS when a fenced store is fixed without
//     moving its entry, so the list can only shrink;
//   - noLoadCall: Create makes no call on the receiver's Load or load.
// A NEW store with both methods that is in none of the lists fails the census until someone
// classifies it, and a fixed store that regresses to the public Load fails it too.
//
// WHAT IT CANNOT KNOW. It is name based and file scoped: it sees `<receiver>.Load(` and
// `<receiver>.load(<ctx>, tx, ...)` inside Create's own body, not calls through helpers, function
// valued fields or another type's methods; it cannot prove the transaction is the one Create opened.
// The pool-size legs (one per fixed store) are the proof; this census only keeps the shape from
// re-arming quietly.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

type censusClass int

const (
	classNoLoadCall censusClass = iota
	classFixed
	classPoolLoad
)

func (c censusClass) String() string {
	return [...]string{"noLoadCall", "fixed", "poolLoad"}[c]
}

// storeKey names a store as "<package dir under internal>.<Type>".
var syncCensusFixed = []string{
	"attachments.SyncStore",
	"customerquote.SyncStore",
	"dispatch.PODSyncStore",
	"dispatch.PickupsSyncStore",
	"dispatch.RunsSyncStore",
	"dispatch.StopsSyncStore",
	"dispatch.WillCallSyncStore",
	"invcount.SyncStore",
	"planflow.ItemsSyncStore",
	"planflow.OrdersSyncStore",
	"portal.TeamSyncStore",
	"purchasing.SyncStore",
	"tasks.ShortageSyncStore",
	"tasks.SyncStore",
	"tasks.WeightSyncStore",
}

var syncCensusNoTxHeld = map[string]string{
	"billing.InvoicesSyncStore":      "Create's transaction lives inside writer.InvoiceOrderNamed, which has returned (committed or rolled back) before the conflict arm calls Load",
	"planflow.InstructionsSyncStore": "Create delegates to OrdersSyncStore.Create (fixed above) and calls its own Load only after that Create has returned its connection",
}

var syncCensusFenced = map[string]string{
	"quote.SyncStore":                          "owner: the Quote Desk lane (quote/syncscope.go)",
	"identity/staffcontext.ApprovalsSyncStore": "owner: identity approvals (staffcontext/syncscope_approvals.go)",
}

// syncCensusNoLoadCall lists the stores with both methods whose Create never reads the row back
// through its own Load (so the arm cannot starve this way): reviewed at source.
var syncCensusNoLoadCall = []string{
	"billing.AgingStore",
	"dashboard.WidgetMeasuresStore",
	"payments.PaymentMethodsStore",
	"payments.PaymentsSyncStore",
	"pricing.SyncStore",
	"sync/readcache.Store",
}

// classifySyncStores walks the module's production sources and classifies every receiver type with
// both a Create and a Load method.
func classifySyncStores(t *testing.T, internalRoot string) map[string]censusClass {
	t.Helper()
	type typeKey struct{ dir, typ string }
	methods := map[typeKey]map[string]*ast.FuncDecl{}
	recvIdent := map[typeKey]string{}

	err := filepath.WalkDir(internalRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); n == "testdata" || n == ".git" || n == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if perr != nil {
			return perr
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil || fd.Recv == nil || len(fd.Recv.List) == 0 {
				continue
			}
			field := fd.Recv.List[0]
			rt := field.Type
			if star, ok := rt.(*ast.StarExpr); ok {
				rt = star.X
			}
			id, ok := rt.(*ast.Ident)
			if !ok {
				continue
			}
			k := typeKey{dir: filepath.ToSlash(filepath.Dir(path)), typ: id.Name}
			if methods[k] == nil {
				methods[k] = map[string]*ast.FuncDecl{}
			}
			methods[k][fd.Name.Name] = fd
			if len(field.Names) > 0 {
				recvIdent[k] = field.Names[0].Name
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", internalRoot, err)
	}

	out := map[string]censusClass{}
	root := filepath.ToSlash(internalRoot) + "/"
	for k, m := range methods {
		create, hasCreate := m["Create"]
		_, hasLoad := m["Load"]
		if !hasCreate || !hasLoad {
			continue
		}
		recv := recvIdent[k]
		pool, viaTx := 0, 0
		ast.Inspect(create.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			x, ok := sel.X.(*ast.Ident)
			if !ok || x.Name != recv {
				return true
			}
			switch sel.Sel.Name {
			case "Load":
				pool++
			case "load":
				if len(call.Args) > 1 {
					if a, ok := call.Args[1].(*ast.Ident); ok && a.Name == "tx" {
						viaTx++
					}
				}
			}
			return true
		})
		name := strings.TrimPrefix(k.dir, root) + "." + k.typ
		switch {
		case pool > 0:
			out[name] = classPoolLoad
		case viaTx > 0:
			out[name] = classFixed
		default:
			out[name] = classNoLoadCall
		}
	}
	return out
}

func TestSyncCreateConflictArmCensus(t *testing.T) {
	got := classifySyncStores(t, filepath.Join(moduleRoot(t), "internal"))

	want := map[string]censusClass{}
	for _, n := range syncCensusFixed {
		want[n] = classFixed
	}
	for n := range syncCensusNoTxHeld {
		want[n] = classPoolLoad
	}
	for n := range syncCensusFenced {
		want[n] = classPoolLoad
	}
	for _, n := range syncCensusNoLoadCall {
		want[n] = classNoLoadCall
	}

	var problems []string
	names := make([]string, 0, len(got)+len(want))
	seen := map[string]bool{}
	for n := range got {
		names = append(names, n)
		seen[n] = true
	}
	for n := range want {
		if !seen[n] {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		g, inGot := got[n]
		w, inWant := want[n]
		switch {
		case inGot && !inWant:
			problems = append(problems, n+": a store with both Create and Load that no census list classifies (it is "+g.String()+"); classify it after reading its Create at source")
		case !inGot && inWant:
			problems = append(problems, n+": listed in the census but no longer has both a Create and a Load method (renamed or removed: update the list)")
		case g != w:
			if _, fenced := syncCensusFenced[n]; fenced && g == classFixed {
				problems = append(problems, n+": FENCED entry is now fixed (Create reaches the loader through the transaction): move it to syncCensusFixed and drop it from syncCensusFenced")
			} else {
				problems = append(problems, n+": census says "+w.String()+" but the source says "+g.String())
			}
		}
	}
	if len(problems) > 0 {
		t.Fatalf("the sync Create conflict arm census disagrees with the source (%d):\n  %s", len(problems), strings.Join(problems, "\n  "))
	}

	// The two no-transaction reasons and the fenced owners are claims; they must not be blank.
	for n, why := range syncCensusNoTxHeld {
		if strings.TrimSpace(why) == "" {
			t.Errorf("%s: a noTxHeld entry needs its reason", n)
		}
	}
	for n, why := range syncCensusFenced {
		if strings.TrimSpace(why) == "" {
			t.Errorf("%s: a fenced entry needs its owner", n)
		}
	}
	if len(syncCensusFixed) != 15 {
		t.Errorf("the sweep fixed fifteen stores, the census lists %d", len(syncCensusFixed))
	}
}
