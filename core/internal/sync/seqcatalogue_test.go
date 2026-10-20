package sync

// seqcatalogue_test.go: the structural guard of the sync seq trigger (migration 0422, ADR 0025).
//
// The map below declares, for every registered scope, the tables its feed serves, and for every
// such table either the one stream its trigger draws on or the reason it carries no trigger. The
// checks:
//
//	(a) every registered scope has a map entry (a new scope fails here until someone declares it);
//	(b) every mapped table carries sync_seq_draw with its stream, and no excluded table does
//	    (seqcatalogue_integration_test.go, needs the migrated database);
//	(c) no app write draws a stream other than its table's (a static scan of non test Go);
//	(d) no hard DELETE FROM a served table in non test Go, bar the pending list;
//	(e) no json, xml or geometric column on a trigger table (seqcatalogue_integration_test.go).
//
// Each check is a function over its inputs, and each has a bite test that feeds it a mutated
// input and expects the red.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// seqStreams: every table that carries the trigger, with its one stream. The source of each
// stream: the live writers on main that already draw (grep of every scope_sequences draw), the
// plan's section 3, and PR 532's demoSeqTables, which all agree on the entries below. A table no
// writer draws for yet (SF-3 to SF-14 own those) takes the stream its scope's store pulls on.
var seqStreams = map[string]string{
	"payments":                     "payments",
	"invoices":                     "invoices",
	"quotes":                       "quotes",
	"delivery_runs":                "runs",
	"delivery_stops":               "stops",
	"delivery_pod_photos":          "pod",
	"products":                     "products",
	"customers":                    "customers",
	"vendors":                      "vendors",
	"portal_payment_methods":       "payment_methods",
	"portal_users":                 "team",
	"warranty_claims":              "warranty_claims",
	"pos_parked_tickets":           "parked_tickets",
	"customer_quote":               "customer_quote",
	"procurement_projects":         "projects",
	"dispatch_trucks":              "trucks",
	"dispatch_drivers":             "drivers",
	"attachment":                   "attachments",
	"plan":                         "orders",
	"plan_line":                    "scope_items",
	"permission_approval_requests": "approvals",
	"pricing_list_prices":          "price_matrix",
	"purchasing_pos":               "purchase_orders",
	"task_shortages":               "shortages",
	"count_sheets":                 "cycle_counts",
}

// seqExcluded: served tables with no trigger, each with its reason.
var seqExcluded = map[string]string{
	"stock_balances":         "SF-7 owns it with its place in the money lock order (a trigger there deadlocks a sale against a restocking return)",
	"msg_messages":           "seq is an IDENTITY column (0385), not a scope_sequences stream; the gap is SF-15",
	"msg_acks":               "seq is an IDENTITY column (0385); SF-15",
	"msg_channels":           "seq is an IDENTITY column (0385); SF-15",
	"msg_notification_prefs": "seq is an IDENTITY column (0385); SF-15",
	"will_call_pickups":      "its single stream, will_call, waits for UX-72 on main; a one line follow up adds it",
	"tasks":                  "two streams by task type (pick_tasks for PICK rows, weights for NEED_WEIGHT rows), which one trigger argument cannot express; to be ruled",
	"dashboard_user_widgets": "stream disagreement: the plan says widget_measures, PR 532's demo repair map counts it under its own name; to be ruled (SF-13 owns the table)",
}

// scopeTables: every registered scope, with the tables its feed serves. A scope whose store is
// not wired in this build maps to no table, with the reason in unwiredScopes.
var scopeTables = map[string][]string{
	"pick_tasks":            {"tasks"},
	"weights":               {"tasks"},
	"stops":                 {"delivery_stops"},
	"runs":                  {"delivery_runs"},
	"loads":                 {"delivery_stops"},
	"delivery_stops":        {"delivery_stops"},
	"will_call":             {"will_call_pickups"},
	"purchase_orders":       {"purchasing_pos"},
	"approvals":             {"permission_approval_requests"},
	"messages":              {"msg_messages"},
	"acks":                  {"msg_acks"},
	"orders":                {"plan"},
	"tracking_events":       nil,
	"invoices":              {"invoices"},
	"payments":              {"payments"},
	"price_matrix":          {"pricing_list_prices"},
	"attachments":           {"attachment"},
	"quotes":                {"quotes"},
	"notify_prefs":          {"msg_notification_prefs"},
	"scope_items":           {"plan_line"},
	"delivery_instructions": {"plan"},
	"shortages":             {"task_shortages"},
	"receiving":             nil,
	"pod":                   {"delivery_pod_photos"},
	"cycle_counts":          {"count_sheets"},
	"customer_quotes":       {"customer_quote"},
	"team":                  {"portal_users"},
	"pickups":               {"will_call_pickups"},
	"products":              {"products"},
	"customers":             {"customers"},
	"inventory_lookup":      {"stock_balances"},
	"trucks":                {"dispatch_trucks"},
	"drivers":               {"dispatch_drivers"},
	"vendors":               {"vendors"},
	"channels":              {"msg_channels"},
	"projects":              {"procurement_projects"},
	"catalog":               {"products"},
	"parked_tickets":        {"pos_parked_tickets"},
	"warranty_claims":       {"warranty_claims"},
	"widget_measures":       {"dashboard_user_widgets"},
	"warranty_board":        {"warranty_claims"},
	"payment_methods":       {"portal_payment_methods"},
	"aging":                 {"invoices"},
}

// unwiredScopes: registry rows no composition root registers a store for today.
var unwiredScopes = map[string]string{
	"tracking_events": "in the registry and the contract enum, no store is registered",
	"receiving":       "in the registry and the contract enum, no store is registered",
}

// hardDeletePending: the known hard deletes of a served table, each owned by a later item. The
// scan is green now and bites the next one.
var hardDeletePending = map[string]string{
	"procurement_projects":   "SF-12 turns the delete into a tombstone",
	"dashboard_user_widgets": "SF-13 turns the replace and clear deletes into tombstones",
}

// coreRoots are the source roots the static scans read, relative to this package.
var coreRoots = []string{"..", "../../cmd"}

// - (a) -

// scopeCoverage reports every registered scope the map lacks, every map scope the registry lacks,
// every served table the map names that is neither a trigger table nor an excluded one.
func scopeCoverage(registered []string, tables map[string][]string, streams map[string]string, excluded map[string]string) []string {
	var bad []string
	reg := map[string]bool{}
	for _, s := range registered {
		reg[s] = true
		if _, ok := tables[s]; !ok {
			bad = append(bad, fmt.Sprintf("scope %q is registered but has no entry in the seq catalogue", s))
		}
	}
	for s, ts := range tables {
		if !reg[s] {
			bad = append(bad, fmt.Sprintf("catalogue scope %q is not in the registry", s))
		}
		for _, t := range ts {
			_, inS := streams[t]
			_, inE := excluded[t]
			switch {
			case !inS && !inE:
				bad = append(bad, fmt.Sprintf("scope %q serves table %q, which is neither a trigger table nor a declared exclusion", s, t))
			case inS && inE:
				bad = append(bad, fmt.Sprintf("table %q is both a trigger table and an exclusion", t))
			}
		}
	}
	sort.Strings(bad)
	return bad
}

func registeredNames() []string {
	var out []string
	for _, s := range Scopes() {
		out = append(out, s.Name)
	}
	return out
}

func TestSeqCatalogue_EveryScopeIsDeclared(t *testing.T) {
	if bad := scopeCoverage(registeredNames(), scopeTables, seqStreams, seqExcluded); len(bad) > 0 {
		t.Fatalf("seq catalogue is out of step with the registry:\n  %s", strings.Join(bad, "\n  "))
	}
	for s, why := range unwiredScopes {
		if ts, ok := scopeTables[s]; !ok || len(ts) != 0 {
			t.Fatalf("scope %q is declared unwired (%s) but the catalogue maps tables for it", s, why)
		}
	}
	// Every trigger table is served by at least one scope: an orphan entry is a stale map.
	served := map[string]bool{}
	for _, ts := range scopeTables {
		for _, tb := range ts {
			served[tb] = true
		}
	}
	for tb := range seqStreams {
		if !served[tb] {
			t.Fatalf("trigger table %q is served by no scope in the catalogue", tb)
		}
	}
	for tb := range seqExcluded {
		if !served[tb] {
			t.Fatalf("excluded table %q is served by no scope in the catalogue", tb)
		}
	}
}

func TestSeqCatalogue_BiteScopeMissing(t *testing.T) {
	// Remove one entry: a registered scope the map lacks must be reported.
	cut := map[string][]string{}
	for k, v := range scopeTables {
		if k != "quotes" {
			cut[k] = v
		}
	}
	bad := scopeCoverage(registeredNames(), cut, seqStreams, seqExcluded)
	if len(bad) != 1 || !strings.Contains(bad[0], `"quotes"`) {
		t.Fatalf("a scope missing from the catalogue was not reported: %v", bad)
	}
	// A new registry row nobody declared.
	bad = scopeCoverage(append(registeredNames(), "brand_new"), scopeTables, seqStreams, seqExcluded)
	if len(bad) != 1 || !strings.Contains(bad[0], `"brand_new"`) {
		t.Fatalf("an undeclared new scope was not reported: %v", bad)
	}
	// A served table that is neither mapped nor excluded.
	extra := map[string][]string{"quotes": {"quotes", "mystery_table"}}
	bad = scopeCoverage([]string{"quotes"}, extra, seqStreams, seqExcluded)
	if len(bad) != 1 || !strings.Contains(bad[0], "mystery_table") {
		t.Fatalf("an undeclared served table was not reported: %v", bad)
	}
}

// - the static scans (c) and (d) -

var (
	writeTableRe  = regexp.MustCompile(`(?is)\b(?:INSERT\s+INTO|UPDATE)\s+(?:ONLY\s+)?([a-z_][a-z0-9_]*)`)
	deleteTableRe = regexp.MustCompile(`(?is)\bDELETE\s+FROM\s+(?:ONLY\s+)?([a-z_][a-z0-9_]*)`)
	literalDrawRe = regexp.MustCompile(`(?is)INSERT\s+INTO\s+scope_sequences\s*\(\s*scope\s*\)\s*VALUES\s*\(\s*'([a-z_]+)'`)
	seqCallRe     = regexp.MustCompile(`(?i)seq`)
)

// goFile is one parsed non test source file of the scan.
type goFile struct {
	path string
	fset *token.FileSet
	file *ast.File
}

func parseTree(t testing.TB, roots []string) []goFile {
	t.Helper()
	var out []goFile
	for _, root := range roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				if info.Name() == "testdata" || info.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if perr != nil {
				return fmt.Errorf("parse %s: %w", path, perr)
			}
			out = append(out, goFile{path: path, fset: fset, file: f})
			return nil
		})
		if err != nil {
			t.Fatalf("seq catalogue scan: %v", err)
		}
	}
	return out
}

// stringConsts collects package level string constants by package directory, so a stream named
// through a constant resolves.
func stringConsts(files []goFile) map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, gf := range files {
		dir := filepath.Dir(gf.path)
		for _, d := range gf.file.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, sp := range gd.Specs {
				vs := sp.(*ast.ValueSpec)
				for i, n := range vs.Names {
					if i >= len(vs.Values) {
						continue
					}
					if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if s, err := strconv.Unquote(lit.Value); err == nil {
							if out[dir] == nil {
								out[dir] = map[string]string{}
							}
							out[dir][n.Name] = s
						}
					}
				}
			}
		}
	}
	return out
}

// funcFacts is what one function declaration writes and draws.
type funcFacts struct {
	name    string
	pos     string
	writes  map[string]bool // table names written (INSERT INTO / UPDATE)
	deletes map[string]bool // table names hard deleted (DELETE FROM)
	draws   map[string]bool // streams drawn
}

func factsOf(gf goFile, consts map[string]string, knownStreams map[string]bool) []funcFacts {
	var out []funcFacts
	for _, d := range gf.file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		ff := funcFacts{
			name: fd.Name.Name, pos: gf.fset.Position(fd.Pos()).String(),
			writes: map[string]bool{}, deletes: map[string]bool{}, draws: map[string]bool{},
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.BasicLit:
				if x.Kind != token.STRING {
					return true
				}
				s, err := strconv.Unquote(x.Value)
				if err != nil {
					return true
				}
				for _, m := range literalDrawRe.FindAllStringSubmatch(s, -1) {
					ff.draws[m[1]] = true
				}
				for _, m := range writeTableRe.FindAllStringSubmatch(s, -1) {
					ff.writes[strings.ToLower(m[1])] = true
				}
				for _, m := range deleteTableRe.FindAllStringSubmatch(s, -1) {
					ff.deletes[strings.ToLower(m[1])] = true
				}
			case *ast.CallExpr:
				// A call whose name says "seq" and that names a known stream by literal or
				// package constant draws that stream (allocate...SeqTx(ctx, tx, scopeConst),
				// fmt.Sprintf(syncSeqCTE, scopeConst)).
				callee := ""
				switch f := x.Fun.(type) {
				case *ast.Ident:
					callee = f.Name
				case *ast.SelectorExpr:
					callee = f.Sel.Name
				}
				named := seqCallRe.MatchString(callee)
				for i, a := range x.Args {
					if callee == "Sprintf" && i == 0 {
						if id, ok := a.(*ast.Ident); ok && seqCallRe.MatchString(id.Name) {
							named = true
						}
					}
				}
				if !named {
					return true
				}
				for _, a := range x.Args {
					switch v := a.(type) {
					case *ast.BasicLit:
						if v.Kind == token.STRING {
							if s, err := strconv.Unquote(v.Value); err == nil && knownStreams[s] {
								ff.draws[s] = true
							}
						}
					case *ast.Ident:
						if s, ok := consts[v.Name]; ok && knownStreams[s] {
							ff.draws[s] = true
						}
					}
				}
			}
			return true
		})
		out = append(out, ff)
	}
	return out
}

// streamAllowlist: a function that writes a trigger table and draws a stream no written trigger
// table owns, each with the reason the pairing is right.
var streamAllowlist = map[string]string{}

// wrongStreamDraws is check (c): every function that writes a trigger table and draws a stream
// must draw only streams its written trigger tables own. A draw of any other KNOWN stream in a
// function that writes a trigger table is the row 6 class (a table written under another table's
// counter) unless the function also writes the table that owns the stream.
func wrongStreamDraws(files []goFile, streams map[string]string, extraStreams []string) []string {
	known := map[string]bool{}
	for _, s := range streams {
		known[s] = true
	}
	for _, s := range extraStreams {
		known[s] = true
	}
	// A table's feed may be served by a table the function does not write: map stream -> owning
	// tables (trigger tables and the excluded ones alike) so "writes the owner" can be judged.
	owners := map[string][]string{}
	for tb, s := range streams {
		owners[s] = append(owners[s], tb)
	}
	consts := stringConsts(files)
	var bad []string
	for _, gf := range files {
		for _, ff := range factsOf(gf, consts[filepath.Dir(gf.path)], known) {
			var mapped []string
			for tb := range ff.writes {
				if _, ok := streams[tb]; ok {
					mapped = append(mapped, tb)
				}
			}
			if len(mapped) == 0 || len(ff.draws) == 0 {
				continue
			}
			okStreams := map[string]bool{}
			for _, tb := range mapped {
				okStreams[streams[tb]] = true
			}
			for d := range ff.draws {
				if okStreams[d] {
					continue
				}
				if _, allowed := streamAllowlist[ff.pos+" "+d]; allowed {
					continue
				}
				sort.Strings(mapped)
				bad = append(bad, fmt.Sprintf("%s (%s) writes %v but draws stream %q", ff.pos, ff.name, mapped, d))
			}
		}
	}
	sort.Strings(bad)
	return bad
}

func allStreamNames() []string {
	// Streams of the excluded tables, so a draw of one of them is known too.
	return []string{"pick_tasks", "weights", "inventory_lookup", "pickups", "will_call", "widget_measures", "product_categories"}
}

func TestSeqCatalogue_NoWriterDrawsAnotherTablesStream(t *testing.T) {
	files := parseTree(t, coreRoots)
	if len(files) < 100 {
		t.Fatalf("the scan read only %d source files; the roots are wrong", len(files))
	}
	// The scan must not be vacuous: it judged a real number of drawing writers.
	known := map[string]bool{}
	for _, st := range seqStreams {
		known[st] = true
	}
	for _, st := range allStreamNames() {
		known[st] = true
	}
	consts := stringConsts(files)
	judged := 0
	for _, gf := range files {
		for _, ff := range factsOf(gf, consts[filepath.Dir(gf.path)], known) {
			hit := false
			for tb := range ff.writes {
				if _, ok := seqStreams[tb]; ok {
					hit = true
				}
			}
			if hit && len(ff.draws) > 0 {
				judged++
			}
		}
	}
	if judged < 25 {
		t.Fatalf("the stream scan judged only %d drawing writers of trigger tables; the scan lost its grip", judged)
	}
	t.Logf("stream scan judged %d drawing writers of trigger tables", judged)
	if bad := wrongStreamDraws(files, seqStreams, allStreamNames()); len(bad) > 0 {
		t.Fatalf("a write to a trigger table draws another table's stream:\n  %s", strings.Join(bad, "\n  "))
	}
}

// plantTree writes one Go file into a fresh directory and parses it.
func plantTree(t *testing.T, src string) []goFile {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "planted.go"), []byte(src), 0o600); err != nil {
		t.Fatalf("plant: %v", err)
	}
	return parseTree(t, []string{dir})
}

func TestSeqCatalogue_BiteWrongStream(t *testing.T) {
	// A literal draw of the wrong stream beside a write to a trigger table.
	files := plantTree(t, "package p\nfunc bad() { _ = `WITH s AS (INSERT INTO scope_sequences (scope) VALUES ('invoices') ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1 RETURNING next_value) INSERT INTO quotes (seq) SELECT next_value FROM s` }\n")
	bad := wrongStreamDraws(files, seqStreams, allStreamNames())
	if len(bad) != 1 || !strings.Contains(bad[0], `"invoices"`) {
		t.Fatalf("a literal wrong stream was not reported: %v", bad)
	}
	// A constant stream through an allocate helper.
	files = plantTree(t, "package p\nconst wrongScope = \"orders\"\nfunc allocateSeqTx(a, b, c any) {}\nfunc bad2() { allocateSeqTx(nil, nil, wrongScope); _ = `UPDATE quotes SET x = 1` }\n")
	bad = wrongStreamDraws(files, seqStreams, allStreamNames())
	if len(bad) != 1 || !strings.Contains(bad[0], `"orders"`) {
		t.Fatalf("a constant wrong stream was not reported: %v", bad)
	}
	// The right stream passes.
	files = plantTree(t, "package p\nconst okScope = \"quotes\"\nfunc allocateSeqTx(a, b, c any) {}\nfunc good() { allocateSeqTx(nil, nil, okScope); _ = `UPDATE quotes SET x = 1` }\n")
	if bad = wrongStreamDraws(files, seqStreams, allStreamNames()); len(bad) != 0 {
		t.Fatalf("the right stream was reported: %v", bad)
	}
}

// hardDeletes is check (d): every non test function that hard deletes from a served table, minus
// the pending list.
func hardDeletes(files []goFile, served map[string]bool, pending map[string]string) []string {
	var bad []string
	known := map[string]bool{}
	for _, gf := range files {
		// internal/testkit is fixture cleanup for test databases, not an app write.
		if strings.Contains(filepath.ToSlash(gf.path), "/testkit/") {
			continue
		}
		for _, ff := range factsOf(gf, nil, known) {
			for tb := range ff.deletes {
				if !served[tb] {
					continue
				}
				if _, ok := pending[tb]; ok {
					continue
				}
				bad = append(bad, fmt.Sprintf("%s (%s) hard deletes from served table %q; a delete of a served row is a tombstone (deleted_at)", ff.pos, ff.name, tb))
			}
		}
	}
	sort.Strings(bad)
	return bad
}

func servedTables() map[string]bool {
	served := map[string]bool{}
	for tb := range seqStreams {
		served[tb] = true
	}
	for tb := range seqExcluded {
		served[tb] = true
	}
	return served
}

func TestSeqCatalogue_NoHardDeleteOfAServedTable(t *testing.T) {
	files := parseTree(t, coreRoots)
	if bad := hardDeletes(files, servedTables(), hardDeletePending); len(bad) > 0 {
		t.Fatalf("hard delete of a served table in non test Go:\n  %s", strings.Join(bad, "\n  "))
	}
	// The pending list is live: each entry still has its delete in the tree, so a fix removes
	// its line from the list.
	for tb, why := range hardDeletePending {
		found := false
		for _, gf := range files {
			for _, ff := range factsOf(gf, nil, map[string]bool{}) {
				if ff.deletes[tb] {
					found = true
				}
			}
		}
		if !found {
			t.Fatalf("pending hard delete of %q (%s) is gone from the tree: remove it from hardDeletePending", tb, why)
		}
	}
}

func TestSeqCatalogue_BiteHardDelete(t *testing.T) {
	files := plantTree(t, "package p\nfunc bad() { _ = `DELETE FROM quotes WHERE id = $1` }\n")
	bad := hardDeletes(files, servedTables(), hardDeletePending)
	if len(bad) != 1 || !strings.Contains(bad[0], `"quotes"`) {
		t.Fatalf("a planted hard delete was not reported: %v", bad)
	}
	// A pending one passes; an unserved table passes.
	files = plantTree(t, "package p\nfunc ok() { _ = `DELETE FROM procurement_projects WHERE id = $1`; _ = `DELETE FROM unrelated WHERE id = $1` }\n")
	if bad = hardDeletes(files, servedTables(), hardDeletePending); len(bad) != 0 {
		t.Fatalf("a pending or unserved delete was reported: %v", bad)
	}
}
