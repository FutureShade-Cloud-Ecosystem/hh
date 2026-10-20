//go:build integration

package readtx

import (
	"testing"

	"github.com/jackc/pgx/v5"
)

// The seeded world has orders at two branches. These read ai.orders and
// ai.order_lines the way the assistant does: as the staff reader, with a signed
// claim carried through Run.

type orderRow struct{ id, no, branch string }

// plantSecondBranch makes sure orders exist at more than one branch: the seed keeps
// them all at one, so it adds two committed orders (one with two lines) at another
// branch, and removes them when the test ends. Parity tests that would otherwise skip
// on a one branch world must not.
func plantSecondBranch(t *testing.T, admin *pgx.Conn) {
	t.Helper()
	var n int
	if err := admin.QueryRow(ctxT(t), `SELECT count(DISTINCT branch_id) FROM orders`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n >= 2 {
		return
	}
	tag := randomHex(t, 3)
	var other string
	if err := admin.QueryRow(ctxT(t), `SELECT id::text FROM locations WHERE id NOT IN (SELECT branch_id FROM orders) ORDER BY id LIMIT 1`).Scan(&other); err != nil {
		t.Fatalf("no second branch to plant orders at: %v", err)
	}
	ids := map[string]string{}
	for i, no := range []string{"SO-S5-" + tag + "-A", "SO-S5-" + tag + "-B"} {
		var id string
		err := admin.QueryRow(ctxT(t), `INSERT INTO orders (order_no, customer_id, branch_id, status, total_cents)
			SELECT $1, (SELECT customer_id FROM orders LIMIT 1), $2::uuid, 'CONFIRMED', 12345
			RETURNING id::text`, no, other).Scan(&id)
		if err != nil {
			t.Fatalf("plant order: %v", err)
		}
		ids[no] = id
		for ln := 1; ln <= i+1; ln++ {
			if _, err := admin.Exec(ctxT(t), `INSERT INTO order_lines (order_id, line_no, description, qty_entered, uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents)
				SELECT $1::uuid, $2, 'planted line', 2, uom_entered, 2, 100, price_uom, 200 FROM order_lines LIMIT 1`, id, ln); err != nil {
				t.Fatalf("plant line: %v", err)
			}
		}
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(ctxT(t), gateURL(t))
		if err != nil {
			return
		}
		defer c.Close(ctxT(t))
		for _, id := range ids {
			_, _ = c.Exec(ctxT(t), `DELETE FROM order_lines WHERE order_id = $1::uuid`, id)
			_, _ = c.Exec(ctxT(t), `DELETE FROM orders WHERE id = $1::uuid`, id)
		}
	})
}

func seededOrders(t *testing.T, admin *pgx.Conn) (all []orderRow, branches []string) {
	t.Helper()
	plantSecondBranch(t, admin)
	rows, err := admin.Query(ctxT(t), `SELECT id::text, order_no::text, branch_id::text FROM orders ORDER BY order_no`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var o orderRow
		if err := rows.Scan(&o.id, &o.no, &o.branch); err != nil {
			t.Fatal(err)
		}
		all = append(all, o)
		if !seen[o.branch] {
			seen[o.branch] = true
			branches = append(branches, o.branch)
		}
	}
	if len(all) < 2 || len(branches) < 2 {
		t.Skipf("the seeded world needs orders at two branches (have %d orders, %d branches)", len(all), len(branches))
	}
	return
}

func stringsOf(t *testing.T, r *Reader, c Claims, sql string, args ...any) []string {
	t.Helper()
	var out []string
	err := r.Run(ctxT(t), c, sql, args, func(rows pgx.Rows) error {
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				return err
			}
			out = append(out, s)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("run %q: %v", sql, err)
	}
	return out
}

func staffReader(t *testing.T) (*Reader, []orderRow, []string, *pgx.Conn) {
	t.Helper()
	admin := connect(t, gateURL(t))
	_, secret := testKey(t, admin)
	staffURL, _ := loginPasswords(t, admin)
	rs, err := Open(ctxT(t), Config{ReaderURL: redactNew(staffURL), ClaimsKey: redactNew(secret)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(rs.Close)
	all, branches := seededOrders(t, admin)
	return rs.Staff, all, branches, admin
}

func TestOrdersViewReachFollowsTheSignedClaim(t *testing.T) {
	r, all, branches, _ := staffReader(t)
	ids := func(c Claims) map[string]bool {
		m := map[string]bool{}
		for _, id := range stringsOf(t, r, c, `SELECT id::text FROM ai.orders`) {
			m[id] = true
		}
		return m
	}
	staff := func(branchIDs []string, global bool) Claims {
		return Claims{Audience: AudienceStaff, AskerID: "asker", Tier: 2, BranchIDs: branchIDs, Global: global}
	}
	g := ids(staff(nil, true))
	if len(g) != len(all) {
		t.Fatalf("a global claim must see every seeded order: %d of %d", len(g), len(all))
	}
	for _, b := range branches {
		got := ids(staff([]string{b}, false))
		for _, o := range all {
			if want := o.branch == b; got[o.id] != want {
				t.Errorf("branch claim %s: order %s in view = %v, want %v", b, o.no, got[o.id], want)
			}
		}
	}
	if n := len(ids(staff(nil, false))); n != 0 {
		t.Errorf("a claim with no branches saw %d orders", n)
	}
	// Lines follow their order's branch.
	lineOrders := func(c Claims) map[string]bool {
		m := map[string]bool{}
		for _, id := range stringsOf(t, r, c, `SELECT order_id::text FROM ai.order_lines`) {
			m[id] = true
		}
		return m
	}
	for _, b := range branches {
		for oid := range lineOrders(staff([]string{b}, false)) {
			if !ids(staff([]string{b}, false))[oid] {
				t.Errorf("a line of order %s is visible at branch %s but its order is not", oid, b)
			}
		}
	}
}

func TestOrdersViewHasPlainColumnsOnly(t *testing.T) {
	_, _, _, admin := staffReader(t)
	rows, err := admin.Query(ctxT(t), `SELECT c.relname, a.attname FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'ai' AND c.relkind = 'v' AND a.attnum > 0 AND NOT a.attisdropped ORDER BY 1, 2`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	forbidden := map[string]bool{
		"deposit_required_cents": true, "metadata": true, "job_address": true, "delivery_instructions": true,
		"created_by": true, "salesperson_id": true, "po_job_ref": true, "job_reference": true, "freight_cents": true,
		"discount_approved_by": true, "price_snapshot": true, "snapshot": true, "supplier_ref": true,
	}
	n := 0
	for rows.Next() {
		var rel, col string
		if err := rows.Scan(&rel, &col); err != nil {
			t.Fatal(err)
		}
		n++
		if forbidden[col] {
			t.Errorf("ai.%s carries %s, which is not a plain order screen column", rel, col)
		}
	}
	if n == 0 {
		t.Fatal("no views in schema ai")
	}
}

func TestPolicedTablesAreEnabledNeverForcedAndTheAppStillReadsThemInFull(t *testing.T) {
	admin := connect(t, gateURL(t))
	for _, table := range []string{"orders", "order_lines", "stock_balances", "product_branch_costs", "quotes", "quote_lines", "purchasing_pos", "purchasing_po_lines"} {
		var enabled, forced bool
		var policies int
		if err := admin.QueryRow(ctxT(t), `SELECT c.relrowsecurity, c.relforcerowsecurity,
			(SELECT count(*) FROM pg_policy p WHERE p.polrelid = c.oid) FROM pg_class c WHERE c.oid = $1::regclass`, "public."+table).Scan(&enabled, &forced, &policies); err != nil {
			t.Fatal(err)
		}
		if !enabled || forced || policies != 1 {
			t.Errorf("%s: row security enabled=%v forced=%v policies=%d, want enabled, not forced, one policy", table, enabled, forced, policies)
		}
		var active bool
		if err := admin.QueryRow(ctxT(t), `SELECT row_security_active($1::regclass)`, "public."+table).Scan(&active); err != nil || active {
			t.Errorf("%s: row security is active for the app role (%v, %v): the app would read a filtered table", table, active, err)
		}
		var n, direct int
		if err := admin.QueryRow(ctxT(t), `SELECT count(*) FROM `+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if err := admin.QueryRow(ctxT(t), `SELECT reltuples::int FROM pg_class WHERE oid = $1::regclass`, "public."+table).Scan(&direct); err != nil {
			t.Fatal(err)
		}
		// The seed holds no purchase orders; for those two tables row_security_active above is the whole check
		// that the app role is not filtered, and the purchasing parity tests read them with rows planted.
		seedEmpty := table == "purchasing_pos" || table == "purchasing_po_lines"
		if n == 0 && !seedEmpty {
			t.Errorf("the app role reads no rows of %s", table)
		}
	}
}
