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
	"go/build/constraint"
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

// seqStreams is the trigger table to stream map (seqstreams.go).
var seqStreams = SeqTriggerStreams()

// seqExcluded: served tables with no trigger, each with its reason.
var seqExcluded = map[string]string{
	"stock_balances":         "SF-7 owns it with its place in the money lock order (a trigger there deadlocks a sale against a restocking return)",
	"msg_messages":           "seq is an IDENTITY column (0385), not a scope_sequences stream; the gap is SF-15",
	"msg_acks":               "seq is an IDENTITY column (0385); SF-15",
	"msg_channels":           "seq is an IDENTITY column (0385); SF-15",
	"msg_notification_prefs": "seq is an IDENTITY column (0385); SF-15",
	"tasks":                  "excluded for good (row 2696): two streams by task type, pick_tasks for PICK rows and weights for NEED_WEIGHT rows; its writers draw them explicitly",
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
			f, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution|parser.ParseComments)
			if perr != nil {
				return fmt.Errorf("parse %s: %w", path, perr)
			}
			// A file that builds only under the finworld tag (W2-1b's synthetic world harness) is never in
			// a production binary, so the catalogue does not judge it.
			if seqBuildsOnlyUnderTag(f, "finworld") {
				return nil
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

// drawBeforeLockAccepted is check (g): an explicit seq on a single row UPDATE of a trigger table, where the
// function holds no earlier lock of that table, names its reason here. Key: "<package dir>.<function> <table>".
// On a trigger table the row comes before the counter: a statement that draws the counter in a CTE (or takes the
// seq as a bound value drawn before the UPDATE) and has not locked the row first goes counter, then row, and
// crosses a writer that relies on the trigger (row, then counter). The fix is to drop the explicit draw, or to
// lock the row first.
var drawBeforeLockAccepted = map[string]string{
	"billing.clearDepositDocument invoices":                   "deposit_cancel.go re-reads and locks the deposit document FOR NO KEY UPDATE (lockDepositDocumentRow) before the settlement calls it",
	"payments.applyAllocations invoices":                      "lockAndTotal locks every allocated invoice FOR NO KEY UPDATE earlier in the same transaction (RecordReceipt, counter receipt)",
	"payments.applySliceTx invoices":                          "the settle slice both callers run under the money lock order holds the invoice row already: the till's tender seam (AllocateToInvoiceTx) and the re-application (ReapplyHeld) lock it FOR NO KEY UPDATE first (payments/LOCK-ORDER.md steps 2 to 4)",
	"quote.recomputeTotals quotes":                            "every caller (line add, edit and delete, terms) runs after lockQuoteForEdit has taken the quote row FOR NO KEY UPDATE",
	"quote.UpdateValidUntilGuarded quotes":                    "lockQuoteForEdit takes the quote row FOR NO KEY UPDATE on the line before",
	"customerquote.RotateOnSend customer_quote":               "the service loads the quote FOR UPDATE (GetQuoteLockTx) before the rotation, so the row is held when the counter is drawn",
	"customerquote.RetireOnRevise customer_quote":             "the same load FOR UPDATE precedes the revise rotation",
	"customerquote.FlipAcceptedTx customer_quote":             "the acceptance locks the quote FOR UPDATE before it draws its two seqs",
	"dispatch.ConfirmPickup will_call_pickups":                "withWillCallSeq takes the pickup row FOR UPDATE before it draws the counter (its comment states the order)",
	"dispatch.RevokePickup will_call_pickups":                 "withWillCallSeq takes the pickup row FOR UPDATE before it draws the counter",
	"product.ApplyAttached products":                          "the loader batch: counter first by LD-5 (the claim transaction holds the products counter from an item's first draw); every trigger relying writer of the table takes that counter first too (check (h)), so no writer is row then counter",
	"customer.Apply customers":                                "the loader batch: counter first by LD-5, and every trigger relying writer of customers takes the counter first (check (h))",
	"staffcontext.writeDecision permission_approval_requests": "locks the request FOR UPDATE OF a first, through approvalSyncQuery (the const names the table and aliases it a)",
}

var (
	// An UPDATE of a table that sets seq from a CTE alias, or from a bound parameter.
	explicitSeqUpdateRe = regexp.MustCompile(`(?is)\bUPDATE\s+(?:ONLY\s+)?([a-z_][a-z0-9_]*)(?:\s+(?:AS\s+)?[a-z_][a-z0-9_]*)?\s+SET\b.*?\bseq\s*=\s*(?:\(\s*SELECT\s+next_value\s+FROM\s+[a-z_]+\s*\)|\$\d+|[a-z_][a-z0-9_]*\.next_value)`)
	forUpdateRe         = regexp.MustCompile(`(?is)\bFOR\s+(?:NO\s+KEY\s+)?UPDATE\b(?:\s+OF\s+([a-z_][a-z0-9_]*(?:\s*,\s*[a-z_][a-z0-9_]*)*))?`)
	singleRowWhereRe    = regexp.MustCompile(`(?is)\bWHERE\b.*?(?:\b[a-z_][a-z0-9_]*\.)?\bid\s*=\s*(?:\$\d+|\()`)
	sqlKeywordRe        = regexp.MustCompile(`(?i)^(where|join|left|right|inner|outer|cross|full|on|for|set|order|limit|group|using|returning|natural)$`)
)

// locksTable reports whether a window of SQL text takes a row lock on the table: it names the table and carries a
// FOR [NO KEY] UPDATE that is either unscoped or scoped (OF) to the table or an alias the window gives it.
func locksTable(window, tb string) bool {
	nameRe := regexp.MustCompile(`(?is)\b` + tb + `\b`)
	if !nameRe.MatchString(window) {
		return false
	}
	names := map[string]bool{tb: true}
	aliasRe := regexp.MustCompile(`(?is)\b(?:FROM|JOIN)\s+` + tb + `(?:\s+(?:AS\s+)?([a-z_][a-z0-9_]*))?`)
	for _, m := range aliasRe.FindAllStringSubmatch(window, -1) {
		if m[1] != "" && !sqlKeywordRe.MatchString(m[1]) {
			names[strings.ToLower(m[1])] = true
		}
	}
	for _, m := range forUpdateRe.FindAllStringSubmatch(window, -1) {
		if m[1] == "" {
			return true
		}
		for _, n := range strings.Split(m[1], ",") {
			if names[strings.ToLower(strings.TrimSpace(n))] {
				return true
			}
		}
	}
	return false
}

// drawBeforeLock reports every non test function that updates ONE row of a trigger table with an explicit seq
// and holds no earlier lock of that table (a string literal before it that names the table and ends in FOR UPDATE
// or FOR NO KEY UPDATE, with or without OF), minus the accepted list.
func drawBeforeLock(files []goFile, streams map[string]string, accepted map[string]string, seen map[string]bool) []string {
	var bad []string
	for _, gf := range files {
		if strings.Contains(filepath.ToSlash(gf.path), "/testkit/") {
			continue
		}
		dir := filepath.Base(filepath.Dir(gf.path))
		for _, d := range gf.file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			var lits []string
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				bl, ok := n.(*ast.BasicLit)
				if !ok || bl.Kind != token.STRING {
					return true
				}
				if s, err := strconv.Unquote(bl.Value); err == nil {
					lits = append(lits, s)
				}
				return true
			})
			for i, s := range lits {
				m := explicitSeqUpdateRe.FindStringSubmatch(s)
				if m == nil || !singleRowWhereRe.MatchString(s) {
					continue
				}
				tb := strings.ToLower(m[1])
				if _, ok := streams[tb]; !ok {
					continue
				}
				// A lock is a literal ending in FOR [NO KEY] UPDATE (the clause may sit in its own literal, joined
				// by +) whose own text or the three literals before it name the table. FOR UPDATE OF <aliases>
				// locks only the named aliases: it counts only when one of them is this table (or its alias).
				locked := false
				for j, prev := range lits[:i] {
					if !forUpdateRe.MatchString(prev) {
						continue
					}
					from := j - 3
					if from < 0 {
						from = 0
					}
					if locksTable(strings.Join(lits[from:j+1], " "), tb) {
						locked = true
					}
				}
				if locked {
					continue
				}
				key := dir + "." + fd.Name.Name + " " + tb
				if _, ok := accepted[key]; ok {
					if seen != nil {
						seen[key] = true
					}
					continue
				}
				bad = append(bad, fmt.Sprintf("%s (%s) updates one row of trigger table %q with an explicit seq and holds no earlier lock of it: on a trigger table the row comes before the counter; drop the explicit draw (the trigger draws) or lock the row first", gf.fset.Position(fd.Pos()), key, tb))
			}
		}
	}
	sort.Strings(bad)
	return bad
}

func TestSeqCatalogue_NoDrawBeforeLockOnATriggerTable(t *testing.T) {
	files := parseTree(t, coreRoots)
	seen := map[string]bool{}
	if bad := drawBeforeLock(files, seqStreams, drawBeforeLockAccepted, seen); len(bad) > 0 {
		t.Fatalf("explicit seq on a single row UPDATE with no earlier row lock:\n  %s", strings.Join(bad, "\n  "))
	}
	for key, why := range drawBeforeLockAccepted {
		if !seen[key] {
			t.Fatalf("accepted draw before lock %q (%s) is gone from the tree: remove it from drawBeforeLockAccepted", key, why)
		}
	}
}

func TestSeqCatalogue_BiteDrawBeforeLock(t *testing.T) {
	cte := "WITH s AS (INSERT INTO scope_sequences (scope) VALUES ('quotes') ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1 RETURNING next_value) "
	// The four shapes S3 planted at 55593fff, each reported (round 3e): an aliased table, any term order in the
	// WHERE, a seq from a joined CTE, and a FOR UPDATE OF another alias that does not lock this table's row.
	for name, src := range map[string]string{
		"aliased":     "package p\nfunc bad() { _ = `UPDATE quotes q SET status = 'X', seq = $2 WHERE q.id = $1` }\n",
		"as alias":    "package p\nfunc bad() { _ = `UPDATE quotes AS q SET status = 'X', seq = $2 WHERE q.id = $1` }\n",
		"where order": "package p\nfunc bad() { _ = `UPDATE quotes SET status = 'X', seq = $4 WHERE org_id = $3 AND id = $1` }\n",
		"joined cte":  "package p\nfunc bad() { _ = `" + cte + "UPDATE quotes SET status = 'X', seq = s.next_value FROM s WHERE quotes.id = $1` }\n",
		"of other":    "package p\nfunc bad() { _ = `SELECT 1 FROM quotes q JOIN customers c ON c.id = q.customer_id WHERE q.id = $1 FOR UPDATE OF c`; _ = `UPDATE quotes SET status = 'X', seq = $2 WHERE id = $1` }\n",
	} {
		if bad := drawBeforeLock(plantTree(t, src), seqStreams, nil, nil); len(bad) != 1 {
			t.Fatalf("the %s shape was not reported: %v", name, bad)
		}
	}
	// FOR UPDATE OF the table's own alias IS a lock of its row.
	if bad := drawBeforeLock(plantTree(t, "package p\nfunc ok() { _ = `SELECT 1 FROM quotes q JOIN customers c ON c.id = q.customer_id WHERE q.id = $1 FOR UPDATE OF q`; _ = `UPDATE quotes q SET status = 'X', seq = $2 WHERE q.id = $1` }\n"), seqStreams, nil, nil); len(bad) != 0 {
		t.Fatalf("a lock of the table's own alias was not accepted: %v", bad)
	}
	// A CTE draw in a single row UPDATE with no earlier lock is reported.
	files := plantTree(t, "package p\nfunc bad() { _ = `"+cte+"UPDATE quotes SET status = 'X', seq = (SELECT next_value FROM s) WHERE id = $1` }\n")
	bad := drawBeforeLock(files, seqStreams, nil, nil)
	if len(bad) != 1 || !strings.Contains(bad[0], `"quotes"`) {
		t.Fatalf("a planted draw before lock was not reported: %v", bad)
	}
	// A bound seq drawn earlier is the same shape.
	files = plantTree(t, "package p\nfunc bad2() { _ = `UPDATE quotes SET status = 'X', seq = $2 WHERE id = $1` }\n")
	if bad = drawBeforeLock(files, seqStreams, nil, nil); len(bad) != 1 {
		t.Fatalf("a planted bound seq update was not reported: %v", bad)
	}
	// An earlier lock of the same table in the same function passes; so does a lock of another table.
	files = plantTree(t, "package p\nfunc ok() { _ = `SELECT 1 FROM quotes WHERE id = $1 FOR NO KEY UPDATE`; _ = `"+cte+"UPDATE quotes SET status = 'X', seq = (SELECT next_value FROM s) WHERE id = $1` }\n")
	if bad = drawBeforeLock(files, seqStreams, nil, nil); len(bad) != 0 {
		t.Fatalf("a locked first update was reported: %v", bad)
	}
	files = plantTree(t, "package p\nfunc other() { _ = `SELECT 1 FROM orders WHERE id = $1 FOR UPDATE`; _ = `UPDATE quotes SET seq = $2 WHERE id = $1` }\n")
	if bad = drawBeforeLock(files, seqStreams, nil, nil); len(bad) != 1 {
		t.Fatalf("a lock of another table excused the update: %v", bad)
	}
	// A trigger relying update, a multi row update and an unserved table pass; the accepted list excuses by key.
	files = plantTree(t, "package p\nfunc fine() { _ = `UPDATE quotes SET status = 'X' WHERE id = $1`; _ = `UPDATE quotes SET seq = $2 WHERE customer_id = $1`; _ = `UPDATE unrelated SET seq = $2 WHERE id = $1` }\n")
	if bad = drawBeforeLock(files, seqStreams, nil, nil); len(bad) != 0 {
		t.Fatalf("a fine update was reported: %v", bad)
	}
	files = plantTree(t, "package p\nfunc excused() { _ = `UPDATE quotes SET seq = $2 WHERE id = $1` }\n")
	if bad = drawBeforeLock(files, seqStreams, map[string]string{filepath.Base(filepath.Dir(files[0].path)) + ".excused quotes": "test"}, map[string]bool{}); len(bad) != 0 {
		t.Fatalf("an accepted update was reported: %v", bad)
	}
}

// counterFirstTables is check (h): the tables whose counter comes BEFORE their rows, so that every writer is
// counter then row. For the loader tables (products, customers, vendors) the reason is the load door: its claim
// transaction takes the table's counter at an item's first draw (LD-5, which closes batch against batch) and holds
// it to the batch commit while it goes on to other item rows, so a writer that took a row and then the counter
// (every writer that relies on the sync seq trigger) crosses it. pricing_list_prices is counter first because every
// one of its writers draws first (the sync writers and ApplyBatch), so a trigger relying writer would cross them.
// The values are the substrings of a counter take helper's name that satisfy the check.
var counterFirstTables = map[string][]string{
	"products":            {"products"},
	"customers":           {"customers"},
	"vendors":             {"vendors"},
	"pricing_list_prices": {"pricematrix", "pricinglistprices"},
}

// counterFirstAccepted: a trigger relying writer of a counter first table that takes no counter first, with the
// reason it cannot cross anything. Key: "<package dir>.<function> <table>".
var counterFirstAccepted = map[string]string{
	"core.repairSharedAccountHomes customers":    "demo seed repair (demo tag): runs once at provisioning of a demo database, alone, with no load batch and no REST traffic",
	"core.renameScrubbedDemoCustomers customers": "demo seed scrub (demo tag): runs once at provisioning of a demo database, alone",
}

var (
	// An UPDATE of a table whose SET list names no seq: the sync seq trigger draws for it.
	updateTableRe = regexp.MustCompile(`(?is)\bUPDATE\s+(?:ONLY\s+)?([a-z_][a-z0-9_]*)(?:\s+(?:AS\s+)?[a-z_][a-z0-9_]*)?\s+SET\b(.*)`)
	setsSeqRe     = regexp.MustCompile(`(?is)\bseq\s*=`)
	// A literal take of the counter row that does not advance it.
	counterTakeRe = regexp.MustCompile(`(?is)scope_sequences.*?DO\s+UPDATE\s+SET\s+next_value\s*=\s*scope_sequences\.next_value\s*(?:[^+\s]|$)`)
)

// counterFirstWriters reports every non test function that updates a row of a counter first table, relying on
// the trigger (no seq in the SET list), without taking that table's counter earlier in the function: neither a
// literal take (INSERT INTO scope_sequences ... DO UPDATE SET next_value = scope_sequences.next_value) nor a call
// of a Lock...Seq helper named for the table.
func counterFirstWriters(files []goFile, tables map[string][]string, accepted map[string]string, seen map[string]bool) []string {
	var bad []string
	for _, gf := range files {
		if strings.Contains(filepath.ToSlash(gf.path), "/testkit/") {
			continue
		}
		dir := filepath.Base(filepath.Dir(gf.path))
		for _, d := range gf.file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			type step struct {
				lit    string
				callee string
			}
			var steps []step
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.BasicLit:
					if x.Kind == token.STRING {
						if str, err := strconv.Unquote(x.Value); err == nil {
							steps = append(steps, step{lit: str})
						}
					}
				case *ast.CallExpr:
					switch f := x.Fun.(type) {
					case *ast.Ident:
						steps = append(steps, step{callee: f.Name})
					case *ast.SelectorExpr:
						steps = append(steps, step{callee: f.Sel.Name})
					}
				}
				return true
			})
			for i, st := range steps {
				if st.lit == "" {
					continue
				}
				m := updateTableRe.FindStringSubmatch(st.lit)
				if m == nil || setsSeqRe.MatchString(m[2]) {
					continue
				}
				tb := strings.ToLower(m[1])
				helpers, ok := tables[tb]
				if !ok {
					continue
				}
				took := false
				for _, prev := range steps[:i] {
					if prev.lit != "" && counterTakeRe.MatchString(prev.lit) && strings.Contains(prev.lit, "'"+SeqTriggerStreams()[tb]+"'") {
						took = true
					}
					low := strings.ToLower(prev.callee)
					if prev.callee != "" && strings.HasPrefix(low, "lock") && strings.Contains(low, "seq") {
						for _, h := range helpers {
							if strings.Contains(low, h) {
								took = true
							}
						}
					}
				}
				if took {
					continue
				}
				key := dir + "." + fd.Name.Name + " " + tb
				if _, ok := accepted[key]; ok {
					if seen != nil {
						seen[key] = true
					}
					continue
				}
				bad = append(bad, fmt.Sprintf("%s (%s) updates a row of counter first table %q relying on the trigger and takes the counter first nowhere earlier: a load batch (or the table's other writers) holds the counter and waits for rows; take the counter without advancing it before the row", gf.fset.Position(fd.Pos()), key, tb))
			}
		}
	}
	sort.Strings(bad)
	return bad
}

func TestSeqCatalogue_CounterFirstTablesAreCounterFirst(t *testing.T) {
	files := parseTree(t, coreRoots)
	seen := map[string]bool{}
	if bad := counterFirstWriters(files, counterFirstTables, counterFirstAccepted, seen); len(bad) > 0 {
		t.Fatalf("a trigger relying writer of a counter first table without the counter first:\n  %s", strings.Join(bad, "\n  "))
	}
	for key, why := range counterFirstAccepted {
		if !seen[key] {
			t.Fatalf("accepted counter first exception %q (%s) is gone from the tree: remove it from counterFirstAccepted", key, why)
		}
	}
	for tb := range counterFirstTables {
		if _, ok := seqStreams[tb]; !ok {
			t.Fatalf("counter first table %q is not a trigger table", tb)
		}
	}
}

func TestSeqCatalogue_BiteCounterFirst(t *testing.T) {
	take := "`INSERT INTO scope_sequences (scope, next_value) VALUES ('products', 0) ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value`"
	// A trigger relying UPDATE with no take is reported (plain, aliased).
	files := plantTree(t, "package p\nfunc bad() { _ = `UPDATE products SET name = $1 WHERE id = $2` }\nfunc bad2() { _ = `UPDATE products AS p SET name = $1 WHERE p.id = $2` }\n")
	if bad := counterFirstWriters(files, counterFirstTables, nil, nil); len(bad) != 2 {
		t.Fatalf("planted trigger relying writers were not reported: %v", bad)
	}
	// A literal take, or a helper call named for the table, satisfies it; an advancing upsert does not.
	files = plantTree(t, "package p\nfunc ok() { _ = "+take+"; _ = `UPDATE products SET name = $1 WHERE id = $2` }\nfunc ok2() { LockProductsSeqTx(); _ = `UPDATE products SET name = $1 WHERE id = $2` }\nfunc LockProductsSeqTx() {}\n")
	if bad := counterFirstWriters(files, counterFirstTables, nil, nil); len(bad) != 0 {
		t.Fatalf("a counter first writer was reported: %v", bad)
	}
	files = plantTree(t, "package p\nfunc adv() { _ = `INSERT INTO scope_sequences (scope) VALUES ('products') ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1`; _ = `UPDATE products SET name = $1 WHERE id = $2` }\n")
	if bad := counterFirstWriters(files, counterFirstTables, nil, nil); len(bad) != 1 {
		t.Fatalf("an advancing draw excused a trigger relying writer: %v", bad)
	}
	// A helper for another table does not satisfy it; an explicit seq is not trigger relying; another table is out of scope.
	files = plantTree(t, "package p\nfunc other() { LockCustomersSeqTx(); _ = `UPDATE products SET name = $1 WHERE id = $2` }\nfunc LockCustomersSeqTx() {}\nfunc explicit() { _ = `UPDATE products SET name = $1, seq = $2 WHERE id = $3` }\nfunc elsewhere() { _ = `UPDATE quotes SET status = 'X' WHERE id = $1` }\n")
	if bad := counterFirstWriters(files, counterFirstTables, nil, nil); len(bad) != 1 {
		t.Fatalf("expected exactly the other-table helper case: %v", bad)
	}
	// pricing_list_prices is under the rule too.
	files = plantTree(t, "package p\nfunc price() { _ = `UPDATE pricing_list_prices SET price_cents = $1 WHERE product_id = $2` }\n")
	if bad := counterFirstWriters(files, counterFirstTables, nil, nil); len(bad) != 1 {
		t.Fatalf("a trigger relying pricing writer was not reported: %v", bad)
	}
}

// seqBuildsOnlyUnderTag reports whether the file's build constraint holds only when tag is set.
func seqBuildsOnlyUnderTag(f *ast.File, tag string) bool {
	for _, cg := range f.Comments {
		if cg.Pos() >= f.Package {
			break
		}
		for _, c := range cg.List {
			if !constraint.IsGoBuild(c.Text) {
				continue
			}
			expr, err := constraint.Parse(c.Text)
			if err != nil {
				return false
			}
			return !expr.Eval(func(string) bool { return false }) && expr.Eval(func(t string) bool { return t == tag })
		}
	}
	return false
}
