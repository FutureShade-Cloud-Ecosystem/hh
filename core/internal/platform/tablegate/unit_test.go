package tablegate

// unit_test.go is the gate's own suite: SQL strings fed straight to the
// extraction engine and the three rules, no repository files, no database.
//
// FAIL FIRST: the planted fixture is a cross-module read (tax reaching into
// orders) and the planted case asserts the gate FAILS it, which is the
// proof the gate bites before anything else is trusted. The edge fixtures
// beside it prove the gate does not bite where it must not: the same-module
// read passes, the allowlisted read passes, the unmapped table fails closed,
// the CTE name is not a table, and every SQL spelling the tree uses
// extracts the way the census assumes it does.
//
// The fixture file string for the cross-module cases names the real pricing
// repository, so the allowlisted case exercises the real allowlist entry
// the item gives (D-PA-3's live-sales feed), and the bite proof in the item
// report removes that entry and watches the census go red.

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// gateViolations feeds one corpus to the engine and the rules as if it came
// from the named module, file and declaration, and answers the violations.
// The corpus is planted as the argument of a pgx Query call, the position
// repository SQL reaches the database from.
func gateViolations(module, file, decl, corpus string) []string {
	src := "package p\n\nfunc f(db *pgxpool.Pool) {\n\t_, _ = db.Query(ctx, `" + corpus + "`)\n}\n"
	units, err := goFileUnits([]byte(src), "fixture.go", fixtureIndex([]byte(src)))
	if err != nil {
		return []string{"fixture parse error: " + err.Error()}
	}
	if len(units) == 0 {
		return []string{"fixture produced no corpus"}
	}
	var out []string
	for _, u := range units {
		for _, ref := range extractTables(u.corpus) {
			a := fixtureAccess(module, file, decl, ref.table, ref.write)
			out = append(out, checkAccess(a, ownedObjects[ref.table])...)
		}
	}
	return out
}

// fixtureIndex builds the module index the census builds, over one in
// memory fixture file, so const fragments and query helpers resolve.
func fixtureIndex(src []byte) *pkgIndex {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "fixture.go", src, 0)
	if err != nil {
		return newPkgIndex(map[string][]string{}, map[string][]string{})
	}
	idx := newPkgIndex(map[string][]string{}, map[string][]string{})
	building := map[string]bool{}
	for round := 0; round < 4; round++ {
		changed := false
		for _, name := range packageSinks(f, idx.sinks) {
			if !idx.sinks[name] {
				idx.sinks[name] = true
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	for round := 0; round < 6; round++ {
		changed := false
		if got := packageFuncParts(f, idx, building); len(got) > 0 {
			changed = true
		}
		for name, parts := range packageStringValues(f, idx.funcs) {
			if _, done := idx.consts[name]; !done {
				idx.consts[name] = parts
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return idx
}

// TestPlantedCrossModuleReadFails is the fail-first fixture: a cross-module
// read planted as a plain SQL string, and the assertion that the gate
// fails it.
func TestPlantedCrossModuleReadFails(t *testing.T) {
	v := gateViolations("tax", "core/internal/tax/repository.go", "fixture",
		"SELECT jurisdiction FROM orders WHERE id = $1")
	if len(v) == 0 {
		t.Fatal("the planted cross-module read (tax reading orders) was NOT failed: the gate does not bite")
	}
	if !strings.Contains(strings.Join(v, "\n"), "CROSS-MODULE ACCESS") {
		t.Fatalf("the planted read was failed, but not as a cross-module access: %v", v)
	}
}

// TestPlantedCrossModuleWriteFails plants the write shape too: a cross
// module UPDATE from a module that owns none of it.
func TestPlantedCrossModuleWriteFails(t *testing.T) {
	v := gateViolations("tax", "core/internal/tax/repository.go", "fixture",
		"UPDATE stock_balances SET qty_reserved = qty_reserved + 1 WHERE product_id = $1")
	if len(v) == 0 {
		t.Fatal("the planted cross-module write (tax writing inventory's stock_balances) was NOT failed")
	}
	if !strings.Contains(strings.Join(v, "\n"), "write") {
		t.Fatalf("the planted write was failed, but not reported as a write: %v", v)
	}
}

// TestSameModuleReadPasses proves the gate does not bite on a module's own
// tables.
func TestSameModuleReadPasses(t *testing.T) {
	v := gateViolations("tax", "core/internal/tax/repository.go", "fixture",
		"SELECT MAX(rate_bps) FROM tax_rates WHERE jurisdiction = $1")
	if len(v) != 0 {
		t.Fatalf("the same-module read was failed: %v", v)
	}
}

// TestAllowlistedReadPasses proves the real allowlist entry admits the real
// cross-module read: pricing's LowMarginSalesLines, the item's given first
// entry, tables resolved through the real ownership map.
func TestAllowlistedReadPasses(t *testing.T) {
	for _, table := range []string{"order_lines", "orders", "customers", "products", "product_branch_costs", "pos_sale"} {
		a := fixtureAccess("pricing", "core/internal/pricing/repository.go", "LowMarginSalesLines", table, false)
		if v := checkAccess(a, ownedObjects[table]); len(v) != 0 {
			t.Fatalf("the allowlisted pricing read of %s was failed: %v", table, v)
		}
	}
}

// TestUnmappedTableFailsClosed proves a referenced table the ownership map
// does not carry fails even when the accessing module would be its only
// plausible owner: the map must cover everything the scan meets.
func TestUnmappedTableFailsClosed(t *testing.T) {
	v := gateViolations("tax", "core/internal/tax/repository.go", "fixture",
		"SELECT rate FROM a_table_no_migration_creates")
	if len(v) == 0 {
		t.Fatal("the unmapped table was NOT failed: the census no longer fails closed")
	}
	if !strings.Contains(strings.Join(v, "\n"), "UNMAPPED OBJECT") {
		t.Fatalf("the unmapped table was failed, but not as unmapped: %v", v)
	}
}

// TestCTENameIsNotATable proves a WITH name is never resolved as a table,
// scoped to its statement.
func TestCTENameIsNotATable(t *testing.T) {
	v := gateViolations("tax", "core/internal/tax/repository.go", "fixture",
		"WITH scoped AS (SELECT id FROM tax_rates WHERE jurisdiction = $1)\nSELECT * FROM scoped")
	if len(v) != 0 {
		t.Fatalf("the CTE name scoped was resolved as a table (or the statement failed another way): %v", v)
	}
	// the CTE's own body still counts: the read of tax_rates inside it is
	// same-module and passes, a foreign body read must still be failed
	v = gateViolations("tax", "core/internal/tax/repository.go", "fixture",
		"WITH scoped AS (SELECT id FROM tax_rates)\nSELECT s.id FROM scoped s JOIN orders o ON o.id = s.id")
	if len(v) != 1 || !strings.Contains(v[0], `"orders"`) {
		t.Fatalf("the CTE arm with a real cross-module join did not fail on orders alone: %v", v)
	}
}

// TestDataModifyingCTEKeepsItsAllocator proves the sync scope pattern as
// the real portal site runs it: the WITH seq AS (...) allocator const
// concatenated with the statement body and passed through the module's own
// exec wrapper (whose string parameter flows into the pgx Exec), hiding the
// allocator name.
func TestDataModifyingCTEKeepsItsAllocator(t *testing.T) {
	cte := "WITH seq AS (INSERT INTO scope_sequences (scope) VALUES ('team') RETURNING next_value) "
	body := "UPDATE portal_users SET seq = (SELECT next_value FROM seq) WHERE id = $1"
	src := "package p\n\n" +
		"const cte = `" + cte + "`\n\n" +
		"func (r *repo) exec(ctx context.Context, sql string, args ...any) error {\n" +
		"\t_, err := r.pool.Exec(ctx, sql, args...)\n" +
		"\treturn err\n" +
		"}\n\n" +
		"func (r *repo) f(ctx context.Context, id string) error {\n" +
		"\treturn r.exec(ctx, cte+`" + body + "`, id)\n" +
		"}\n"
	units, err := goFileUnits([]byte(src), "fixture.go", fixtureIndex([]byte(src)))
	if err != nil {
		t.Fatal(err)
	}
	var sawSeq, sawPortal bool
	for _, u := range units {
		for _, ref := range extractTables(u.corpus) {
			if ref.table == "seq" {
				sawSeq = true
			}
			if ref.table == "portal_users" && ref.write {
				sawPortal = true
			}
		}
	}
	if sawSeq {
		t.Fatal("the CTE allocator name seq was resolved as a table")
	}
	if !sawPortal {
		t.Fatal("the statement body's write was not extracted alongside the const CTE")
	}
}

// TestFunctionCallIsNotATable proves a captured name followed by an open
// parenthesis is a function call, and a FROM inside a call's argument list
// is a column argument unless a SELECT runs in that group.
func TestFunctionCallIsNotATable(t *testing.T) {
	for _, corpus := range []string{
		"SELECT * FROM generate_series(1, 10) g",
		"SELECT rate FROM tax_rates WHERE EXTRACT(YEAR FROM effective_from) = $1",
		"SELECT rate FROM tax_rates WHERE SUBSTRING(jurisdiction FROM 1 FOR 2) = 'ON'",
		"SELECT rate FROM tax_rates WHERE TRIM(BOTH ' ' FROM jurisdiction) = 'ON'",
		"SELECT EXISTS(SELECT 1 FROM tax_decision_snapshot WHERE order_id = $1)",
		"INSERT INTO tax_config (branch_id, round_mode) VALUES ($1, $2)",
		"INSERT INTO tax_config (branch_id) VALUES ($1) ON CONFLICT (branch_id) DO UPDATE SET round_mode = EXCLUDED.round_mode",
		"UPDATE tax_config SET round_mode = $2 WHERE branch_id = $1",
		"SELECT * FROM tax_config WHERE branch_id = $1 FOR UPDATE",
		"SELECT * FROM tax_config WHERE branch_id IS DISTINCT FROM $1",
		"DELETE FROM tax_config WHERE branch_id = $1",
		"UPDATE tax_config SET n = b.n FROM (SELECT branch_id, 1 AS n FROM tax_config) b WHERE b.branch_id = tax_config.branch_id",
		"INSERT INTO tax_config (branch_id) SELECT branch_id FROM tax_config WHERE round_mode = 'X'",
		"SELECT id::text, branch_id::text FROM tax_config t JOIN ONLY public.tax_evidence e ON e.branch_id = t.branch_id",
		"SELECT rate FROM tax_rates WHERE created_at >= $2::date + INTERVAL '1 day'",
		"SELECT count(*) FROM tax_rates, NULL WHERE 1=0",
	} {
		v := gateViolations("tax", "core/internal/tax/repository.go", "fixture", corpus)
		if len(v) != 0 {
			t.Fatalf("same-module corpus extracted a foreign or phantom table:\n  %s\n  %v", corpus, v)
		}
	}
}

// TestWordBoundaryProvesNoSubstring proves table names match on token
// boundaries: orders never matches order_lines, invoice never matches
// invoices.
func TestWordBoundaryProvesNoSubstring(t *testing.T) {
	// order_lines is ONE token; a table named orders must not be seen in it
	v := gateViolations("order", "core/internal/order/repository.go", "fixture",
		"SELECT id FROM order_lines WHERE line_total_cents > 0")
	if len(v) != 0 {
		t.Fatalf("order_lines leaked as another table (word boundary broken): %v", v)
	}
}

// TestSchemaQualifiedResolvesByTablePart proves a.b resolves by its table
// part.
func TestSchemaQualifiedResolvesByTablePart(t *testing.T) {
	v := gateViolations("tax", "core/internal/tax/repository.go", "fixture",
		"SELECT id FROM public.orders WHERE id = $1")
	if len(v) != 1 || !strings.Contains(v[0], `"orders"`) {
		t.Fatalf("schema-qualified orders did not resolve to orders: %v", v)
	}
}

// TestSprintfBuiltQueryKeepsItsTable proves the sync scope shape (a
// Sprintf'd fragment feeding a pgx call through a local) still lands in the
// corpus.
func TestSprintfBuiltQueryKeepsItsTable(t *testing.T) {
	src := "package p\n\nimport \"fmt\"\n\nfunc f(db *pgxpool.Pool, cols string) {\n\tq := fmt.Sprintf(`SELECT %s FROM orders WHERE id = $1`, cols)\n\t_, _ = db.Query(ctx, q)\n}\n"
	units, err := goFileUnits([]byte(src), "fixture.go", fixtureIndex([]byte(src)))
	if err != nil {
		t.Fatal(err)
	}
	sawOrders := false
	for _, u := range units {
		for _, ref := range extractTables(u.corpus) {
			if ref.table == "orders" {
				sawOrders = true
			}
		}
	}
	if !sawOrders {
		t.Fatal("the Sprintf-built query's table was not extracted")
	}
}

// TestHelperReturnedQueryKeepsItsTable proves the query-helper shape (a
// package function returning the statement) resolves at the call site.
func TestHelperReturnedQueryKeepsItsTable(t *testing.T) {
	src := "package p\n\nfunc build() string {\n\treturn `INSERT INTO orders (id) VALUES ($1)`\n}\n\nfunc f(db *pgxpool.Pool) {\n\t_, _ = db.Exec(ctx, build())\n}\n"
	units, err := goFileUnits([]byte(src), "fixture.go", fixtureIndex([]byte(src)))
	if err != nil {
		t.Fatal(err)
	}
	sawOrders := false
	for _, u := range units {
		for _, ref := range extractTables(u.corpus) {
			if ref.table == "orders" && ref.write {
				sawOrders = true
			}
		}
	}
	if !sawOrders {
		t.Fatal("the helper-returned INSERT was not resolved at the call site")
	}
}

// TestProseNeverEntersACorpus proves prompt text and error strings are not
// read as SQL: they never reach a pgx call site.
func TestProseNeverEntersACorpus(t *testing.T) {
	src := "package p\n\nimport (\n\t\"errors\"\n\t\"fmt\"\n)\n\nfunc g() error {\n\treturn errors.New(\"failed to delete from the orders table\")\n}\n\nfunc h(name string) string {\n\treturn fmt.Sprintf(\"select from the %s list below\", name)\n}\n"
	units, err := goFileUnits([]byte(src), "fixture.go", fixtureIndex([]byte(src)))
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range units {
		if refs := extractTables(u.corpus); len(refs) != 0 {
			t.Fatalf("prose leaked into a corpus as table references: %v (from %q)", refs, u.corpus)
		}
	}
}
