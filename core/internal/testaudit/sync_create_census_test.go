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
// WHAT THIS CENSUS PINS. Every type in the module that has both a Create and a Load method (and
// every type whose own Create calls a Load it inherits) is classified here, by an AST walk of the production sources (no database), into exactly one of:
//   - fixed: Create reaches the loader through the transaction (s.load(ctx, tx, ...)) and never
//     calls the public Load;
//   - noTxHeld: Create calls the public Load but holds no transaction of its own at that point
//     (each entry says why, read at source);
//   - fenced: Create still calls the public Load under its own transaction; the owning lane has not
//     fixed it yet (the entry names the owner). The census FAILS when a fenced store is fixed without
//     moving its entry, so the list can only shrink;
//   - noLoadCall: Create (followed through the receiver's own helper methods) makes no call on the
//     receiver's Load or load.
// A NEW store with both methods that is in none of the lists fails the census until someone
// classifies it, and a fixed store that regresses to the public Load fails it too.
//
// HOW IT READS A STORE. Create's body is followed through the receiver's own methods (a helper such
// as writeAnchor counts as part of Create); a type that embeds another store and declares no Create or Load of its own inherits the embedded
// store's class (two dispatch stores do); a type that declares its own Create over an inherited Load
// is scanned like any store, with the embedded store's loader checked;
// a fixed store's unexported loader must exist and must not name `.pool` anywhere in its body.
//
// WHAT IT CANNOT KNOW. It is name based and file scoped: it sees `<receiver>.Load(` and
// `<receiver>.load(<ctx>, tx, ...)` in Create and the receiver's own helpers, not calls through
// function valued fields or another type's methods; it cannot prove the transaction is the one
// Create opened, and for a noTxHeld store it cannot prove the transaction is STILL absent at the
// call (a Load moved under a Begin stays green: each noTxHeld reason is a claim read at source).
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

// Store names are "<package dir under internal>.<Type>".

// syncCensusFixed: Create reaches the loader through the transaction (the fifteen stores of the sweep).
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

// syncCensusPromoted: stores that embed a fixed store and inherit its Create and Load without
// declaring either, so they carry its class.
var syncCensusPromoted = map[string]string{
	"dispatch.DeliveryStopsSyncStore": "embeds StopsSyncStore",
	"dispatch.LoadsSyncStore":         "embeds StopsSyncStore",
}

// syncCensusNoTxHeld: Create reaches the public Load but holds no transaction of its own at that
// point; each reason was read at source.
var syncCensusNoTxHeld = map[string]string{
	"billing.InvoicesSyncStore":      "Create's transaction lives inside writer.InvoiceOrderNamed, which has returned (committed or rolled back) before the conflict arm calls Load",
	"planflow.InstructionsSyncStore": "Create delegates to OrdersSyncStore.Create (fixed above) and calls its own Load only after that Create has returned its connection",
	"pricing.SyncStore":              "Create delegates to writeAnchor, which calls the public Load BEFORE its own Begin (the anchor check in pricing/syncscope.go), so no transaction of its own is held at that read",
}

// syncCensusFenced: Create still calls the public Load under its own transaction; the owner has not
// fixed it yet. The census fails when one of these is fixed without moving its entry.
var syncCensusFenced = map[string]string{
	"quote.SyncStore":                          "owner: the Quote Desk lane (quote/syncscope.go)",
	"identity/staffcontext.ApprovalsSyncStore": "owner: identity approvals (staffcontext/syncscope_approvals.go)",
}

// syncCensusNoLoadCall: stores with both methods whose Create never reads the row back through its
// own Load (so the arm cannot starve this way); reviewed at source.
var syncCensusNoLoadCall = []string{
	"billing.AgingStore",
	"dashboard.WidgetMeasuresStore",
	// The messaging feeds (House Messages core): each Create validates and
	// writes through the repository field in one statement (an upsert or an
	// INSERT..SELECT whose empty result IS the refusal), so there is no row
	// to read back through any Load; channels is pull-only and refuses.
	"messaging.channelsSyncStore",
	"messaging.messagesSyncStore",
	"messaging.acksSyncStore",
	"messaging.notifyPrefsSyncStore",
	"payments.PaymentMethodsStore",
	"payments.PaymentsSyncStore",
	"sync/readcache.Store",
}

type storeFacts struct {
	create, load *ast.FuncDecl
	methods      map[string]*ast.FuncDecl
	recv         map[string]string // method name -> receiver identifier
	embeds       []string          // embedded type names of the same package
}

// classifySyncStores walks the module's production sources and classifies every receiver type that
// has both a Create and a Load method (declared or promoted from an embedded type of its package).
func classifySyncStores(t *testing.T, internalRoot string) map[string]censusClass {
	t.Helper()
	type typeKey struct{ dir, typ string }
	facts := map[typeKey]*storeFacts{}
	get := func(k typeKey) *storeFacts {
		if facts[k] == nil {
			facts[k] = &storeFacts{methods: map[string]*ast.FuncDecl{}, recv: map[string]string{}}
		}
		return facts[k]
	}

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
		dir := filepath.ToSlash(filepath.Dir(path))
		for _, decl := range f.Decls {
			switch dd := decl.(type) {
			case *ast.GenDecl:
				for _, sp := range dd.Specs {
					ts, ok := sp.(*ast.TypeSpec)
					if !ok {
						continue
					}
					st, ok := ts.Type.(*ast.StructType)
					if !ok {
						continue
					}
					sf := get(typeKey{dir, ts.Name.Name})
					for _, fld := range st.Fields.List {
						if len(fld.Names) != 0 {
							continue // a named field is not an embedding
						}
						ft := fld.Type
						if star, ok := ft.(*ast.StarExpr); ok {
							ft = star.X
						}
						switch e := ft.(type) {
						case *ast.Ident:
							sf.embeds = append(sf.embeds, e.Name)
						case *ast.SelectorExpr:
							sf.embeds = append(sf.embeds, e.Sel.Name) // an embedded type of another package
						}
					}
				}
			case *ast.FuncDecl:
				if dd.Body == nil || dd.Recv == nil || len(dd.Recv.List) == 0 {
					continue
				}
				field := dd.Recv.List[0]
				rt := field.Type
				if star, ok := rt.(*ast.StarExpr); ok {
					rt = star.X
				}
				id, ok := rt.(*ast.Ident)
				if !ok {
					continue
				}
				sf := get(typeKey{dir, id.Name})
				sf.methods[dd.Name.Name] = dd
				if len(field.Names) > 0 {
					sf.recv[dd.Name.Name] = field.Names[0].Name
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", internalRoot, err)
	}

	// scan follows Create through the receiver's own methods and counts the two call shapes.
	scan := func(sf *storeFacts) (pool, viaTx int) {
		visited := map[string]bool{}
		var walk func(name string)
		walk = func(name string) {
			if visited[name] {
				return
			}
			visited[name] = true
			fd := sf.methods[name]
			if fd == nil {
				return
			}
			recv := sf.recv[name]
			ast.Inspect(fd.Body, func(n ast.Node) bool {
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
				default:
					if _, own := sf.methods[sel.Sel.Name]; own && sel.Sel.Name != "Create" {
						walk(sel.Sel.Name)
					}
				}
				return true
			})
		}
		walk("Create")
		return pool, viaTx
	}

	// loaderUsesPool reports whether the unexported loader's body names .pool anywhere.
	loaderUsesPool := func(sf *storeFacts) bool {
		fd := sf.methods["load"]
		if fd == nil {
			return true // a "fixed" store with no loader is not fixed
		}
		bad := false
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "pool" {
				bad = true
			}
			return true
		})
		return bad
	}

	// classOf classifies a store from Create's reach (scan of sf) and the loader's body (loaderFrom:
	// the store itself, or the embedded store it inherits Load and load from).
	classOf := func(sf, loaderFrom *storeFacts) censusClass {
		if _, own := sf.methods["load"]; own {
			loaderFrom = sf // a store that declares its own loader is judged by it, not by an inherited one
		}
		pool, viaTx := scan(sf)
		switch {
		case pool > 0:
			return classPoolLoad
		case viaTx > 0 && !loaderUsesPool(loaderFrom):
			return classFixed
		case viaTx > 0:
			return classPoolLoad // the call shape is right but the loader itself reads the pool
		default:
			return classNoLoadCall
		}
	}

	out := map[string]censusClass{}
	root := filepath.ToSlash(internalRoot) + "/"
	for k, sf := range facts {
		_, hasCreate := sf.methods["Create"]
		_, hasLoad := sf.methods["Load"]
		name := strings.TrimPrefix(k.dir, root) + "." + k.typ
		// findEmbedded returns the facts of an embedded store type that declares a Load (the same
		// package first, then any package: embedded types of other packages are matched by name).
		findEmbedded := func() *storeFacts {
			for _, e := range sf.embeds {
				if esf := facts[typeKey{k.dir, e}]; esf != nil {
					if _, el := esf.methods["Load"]; el {
						return esf
					}
				}
			}
			keys := make([]typeKey, 0, len(facts))
			for ek := range facts {
				keys = append(keys, ek)
			}
			sort.Slice(keys, func(i, j int) bool {
				if keys[i].dir != keys[j].dir {
					return keys[i].dir < keys[j].dir
				}
				return keys[i].typ < keys[j].typ
			})
			for _, e := range sf.embeds {
				for _, ek := range keys {
					if ek.typ != e {
						continue
					}
					if _, el := facts[ek].methods["Load"]; el {
						return facts[ek]
					}
				}
			}
			return nil
		}
		if hasCreate && hasLoad {
			out[name] = classOf(sf, sf)
			continue
		}
		// A Create of its own over a Load inherited by embedding: the shape that carries the defect.
		if hasCreate && !hasLoad {
			if esf := findEmbedded(); esf != nil {
				out[name] = classOf(sf, esf)
			} else if pool, viaTx := scan(sf); pool > 0 || viaTx > 0 {
				// A Load inherited some other way (two levels down, an embedded interface): a call to
				// s.Load in Create only compiles if Load is reachable, so counting it is sound evidence.
				out[name] = classOf(sf, sf)
			}
			continue
		}
		// Promotion: no Create and no Load of its own, an embedded store of the same package has both.
		if !hasCreate && !hasLoad {
			for _, e := range sf.embeds {
				esf := facts[typeKey{k.dir, e}]
				if esf == nil {
					continue
				}
				_, ec := esf.methods["Create"]
				_, el := esf.methods["Load"]
				if ec && el {
					out[name] = classOf(esf, esf)
					break
				}
			}
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
	for n := range syncCensusPromoted {
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
			problems = append(problems, n+": a store with both Create and Load (declared or promoted) that no census list classifies (it is "+g.String()+"); classify it after reading its Create at source")
		case !inGot && inWant:
			problems = append(problems, n+": listed in the census but the census no longer sees it as a store (it has no Create and Load, declared or promoted from an embedded store): read its Create at source BEFORE editing the list; moving or renaming a method does not make a store safe")
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

	// The reasons and owners are claims; they must not be blank.
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
