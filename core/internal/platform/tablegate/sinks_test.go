package tablegate

// sinks_test.go, the round 2 fixtures: the SQL shapes that reach pgx
// through something other than a direct pgx call argument. Each fixture
// fed the round 1 engine produced no access at all (the fail-first runs
// are recorded in the item report); each asserts the access is seen and
// failed here.
//
// The shapes, taken from the tree's real sites:
//
//   - the wrapper method: portal's r.exec and guardedAdminUpdate, quote's
//     invoicesForTx, messaging's scanMessagePage, order's
//     guardedChangeUpdate, all taking the statement as a string parameter
//     that flows into a pgx call;
//   - the closure parameter: customer's scan := func(sql string) feeding
//     r.pool.QueryRow(ctx, sql, ...);
//   - the composite literal field: reporting's catalogEntry{sql: ...} and
//     the sync readcache.Query{Page: ..., ByID: ...} the shared skeleton
//     executes;
//   - the sqlc query file: core/db/queries/<module>.sql content, executed
//     one day by the generated repository layer (proven failing on the
//     round 1 code by the planted tax.sql probe, whose green is the
//     round 1 evidence);
//   - the allowlist verb: a cross-module UPDATE under a read-only entry
//     must fail, which the round 1 table-only matching admitted.

import (
	"strings"
	"testing"
)

// sinkViolations runs one fixture source through the engine and the rules
// as if it were a file of the named module, and answers the violations.
func sinkViolations(module, file, src string) []string {
	units, err := goFileUnits([]byte(src), file, fixtureIndex([]byte(src)))
	if err != nil {
		return []string{"fixture parse error: " + err.Error()}
	}
	var out []string
	for _, u := range units {
		for _, ref := range extractTables(u.corpus) {
			a := fixtureAccess(module, file, u.decl, ref.table, ref.write)
			out = append(out, checkAccess(a, ownedObjects[ref.table])...)
		}
	}
	return out
}

// TestWrapperSinkSQLIsSeen plants the portal r.exec shape: a wrapper method
// whose string parameter flows into a pgx Exec, called with a cross-module
// UPDATE at the call site. The write must be seen and failed.
func TestWrapperSinkSQLIsSeen(t *testing.T) {
	src := "package p\n\n" +
		"func (r *repo) exec(ctx context.Context, sql string, args ...any) error {\n" +
		"\t_, err := r.pool.Exec(ctx, sql, args...)\n" +
		"\treturn err\n" +
		"}\n\n" +
		"func (r *repo) touch(ctx context.Context, id string) error {\n" +
		"\treturn r.exec(ctx, `UPDATE orders SET status = status WHERE id = $1`, id)\n" +
		"}\n"
	v := sinkViolations("portal", "core/internal/portal/repository.go", src)
	if len(v) == 0 {
		t.Fatal("the wrapper-carried UPDATE of orders was NOT seen: the sink shape is still blind")
	}
	if !strings.Contains(strings.Join(v, "\n"), `"orders"`) ||
		!strings.Contains(strings.Join(v, "\n"), "write") {
		t.Fatalf("the wrapper shape surfaced something else: %v", v)
	}
}

// TestClosureSinkSQLIsSeen plants the customer account_lookup shape: a
// local closure taking the statement as a string parameter and feeding it
// to QueryRow, called with a cross-module SELECT.
func TestClosureSinkSQLIsSeen(t *testing.T) {
	src := "package p\n\n" +
		"func lookup(r *repo, ctx context.Context) {\n" +
		"\tscan := func(sql string) (Result, bool, error) {\n" +
		"\t\tvar s Result\n" +
		"\t\terr := r.pool.QueryRow(ctx, sql, ref).Scan(&s.ID)\n" +
		"\t\treturn s, err == nil, err\n" +
		"\t}\n" +
		"\t_, _, _ = scan(`SELECT t.balance_after FROM customer_transactions t WHERE t.customer_id = $1`)\n" +
		"}\n"
	v := sinkViolations("customer", "core/internal/customer/fixture_lookup.go", src)
	if len(v) == 0 {
		t.Fatal("the closure-carried read of customer_transactions was NOT seen: the closure shape is still blind")
	}
	if !strings.Contains(strings.Join(v, "\n"), `"customer_transactions"`) {
		t.Fatalf("the closure shape surfaced something else: %v", v)
	}
}

// TestCompositeFieldSQLIsSeen plants the reporting catalogEntry and sync
// readcache.Query shape: a struct literal whose string field carries a
// statement the runner executes later.
func TestCompositeFieldSQLIsSeen(t *testing.T) {
	src := "package p\n\n" +
		"var catalog = map[string]entry{\n" +
		"\t\"sales\": {sql: `SELECT i.id FROM invoices i JOIN pos_sale zz ON false WHERE i.issue_date >= $1::date`},\n" +
		"}\n"
	v := sinkViolations("reporting", "core/internal/reporting/catalog.go", src)
	if len(v) == 0 {
		t.Fatal("the composite-literal field's cross-module read of pos_sale was NOT seen: the composite shape is still blind")
	}
	if !strings.Contains(strings.Join(v, "\n"), `"pos_sale"`) {
		t.Fatalf("the composite shape surfaced something else: %v", v)
	}
}

// TestAllowlistVerbMatching plants the reviewer's probe (e): a cross-module
// UPDATE under an entry whose reason promises read only. Table-only
// matching admitted it; verb matching must fail it.
func TestAllowlistVerbMatching(t *testing.T) {
	v := gateViolations("billing", "core/internal/billing/ar_landing_store.go", "EngagementFor",
		"UPDATE customers SET name = name WHERE false")
	if len(v) == 0 {
		t.Fatal("the planted cross-module UPDATE under a read-only entry was admitted: the allowlist still matches without the verb")
	}
	if !strings.Contains(strings.Join(v, "\n"), "CROSS-MODULE ACCESS") {
		t.Fatalf("the planted write was failed, but not as a cross-module access: %v", v)
	}
}

// TestQueriesFileContentIsChecked runs the sqlc query file's content
// through the same extraction, attributed by filename. The round 1 census
// checked only the filename; the planted tax.sql probe stayed green there,
// which is the fail-first evidence for this shape.
func TestQueriesFileContentIsChecked(t *testing.T) {
	v := queriesFileViolations("tax", "core/db/queries/tax.sql",
		"SELECT o.id FROM orders o JOIN invoices i ON i.order_id = o.id;")
	if len(v) == 0 {
		t.Fatal("the planted tax.sql reading orders and invoices was NOT checked: the queries directory content is still blind")
	}
	joined := strings.Join(v, "\n")
	if !strings.Contains(joined, `"orders"`) || !strings.Contains(joined, `"invoices"`) {
		t.Fatalf("the planted queries file surfaced something else: %v", v)
	}
}
