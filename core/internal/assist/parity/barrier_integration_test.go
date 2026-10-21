//go:build integration

package parity

// Every view that joins customers for a name is a security barrier (PR 581 read P3 1, FROM-PRINCIPAL item 52). The
// customers table carries no row security (a policy there would blank the names the other views join), so a name is
// visible to a view only through the view's own reach. A WHERE on a name from the reader could otherwise be
// evaluated at the customers scan, ahead of the view's reach, and a function in it would see a name the asker may
// not: the safety of the older views would depend on no assist role ever being able to create a function. The
// barrier removes the dependence. Two tests: the catalogue (every such view is a barrier) and the probe (a leaky
// function planted by the superuser, granted to the reader, sees no hidden name through any of them).

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx/testsupport"
)

func TestBarrier_EveryViewThatJoinsCustomersIsASecurityBarrier(t *testing.T) {
	env := testsupport.Open(t)
	rows, err := env.Admin.Query(testsupport.Ctx(t), `SELECT v.viewname, COALESCE(c.reloptions, '{}')
		FROM pg_views v JOIN pg_class c ON c.relname = v.viewname AND c.relnamespace = 'ai'::regnamespace
		WHERE v.schemaname = 'ai' AND v.definition ~* '\y(from|join)\s+(\(\s*)*(public\.)?customers\y'
		ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var name string
		var opts []string
		if err := rows.Scan(&name, &opts); err != nil {
			t.Fatal(err)
		}
		n++
		if !strings.Contains(strings.Join(opts, ","), "security_barrier=true") {
			t.Errorf("ai.%s joins customers and is not a security barrier (options %v)", name, opts)
		}
	}
	if n < 7 {
		t.Errorf("found only %d views joining customers, want the 8 known (customers, orders, quotes, invoices, payments, delivery stops, contacts, aging)", n)
	}
}

func TestBarrier_ALeakyFunctionInTheWhereSeesNoHiddenNameThroughAnyViewThatJoinsCustomers(t *testing.T) {
	env := testsupport.Open(t)
	ctx := testsupport.Ctx(t)
	tag := randomHex(t, 3)
	ba, bb := plantBranch(t, env, tag+"BA", "S7c barrier visible"), plantBranch(t, env, tag+"BB", "S7c barrier hidden")
	hidden, visible := "S7CHIDDEN-"+tag, "S7CVISIBLE-"+tag
	cust := map[string]string{}
	for _, c := range []struct{ name, acct, home string }{{hidden, "S7C-" + tag + "H", bb}, {visible, "S7C-" + tag + "V", ba}} {
		name := c.name
		var id string
		if err := env.Admin.QueryRow(ctx, `INSERT INTO customers (account_no, name, home_branch_id) VALUES ($1, $2, $3::uuid) RETURNING id::text`, c.acct, name, c.home).Scan(&id); err != nil {
			t.Fatal(err)
		}
		cust[name] = id
	}
	plant := func(name, branch, suffix string) {
		c := cust[name]
		var oid string
		if err := env.Admin.QueryRow(ctx, `INSERT INTO orders (order_no, customer_id, branch_id, status, total_cents) VALUES ($1, $2::uuid, $3::uuid, 'CONFIRMED', 100) RETURNING id::text`, "SO-S7C-"+tag+suffix, c, branch).Scan(&oid); err != nil {
			t.Fatal(err)
		}
		stmts := []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO quotes (quote_no, customer_id, branch_id, status, fulfillment, subtotal_cents, tax_cents, total_cents) VALUES ($1, $2::uuid, $3::uuid, 'DRAFT', 'DELIVERY', 100, 0, 100)`, []any{"Q-S7C-" + tag + suffix, c, branch}},
			{`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date, subtotal_cents, total_cents, balance_cents) VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', CURRENT_DATE - 40, CURRENT_DATE - 10, 500, 500, 500)`, []any{"INV-S7C-" + tag + suffix, c, branch}},
			{`INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, branch_id) VALUES ($1, $2::uuid, 'CASH', 100, 'RECEIVED', $3::uuid)`, []any{"PAY-S7C-" + tag + suffix, c, branch}},
			{`INSERT INTO delivery_stops (branch_id, order_id, stop_no, scheduled_date, status) VALUES ($1::uuid, $2::uuid, 1, CURRENT_DATE, 'READY')`, []any{branch, oid}},
			{`INSERT INTO customer_contacts (customer_id, name, email, phone) VALUES ($1::uuid, $2, 'c@example.test', '555-0100')`, []any{c, "Contact " + suffix + " " + tag}},
		}
		for _, s := range stmts {
			if _, err := env.Admin.Exec(ctx, s.sql, s.args...); err != nil {
				t.Fatalf("plant %s: %v", suffix, err)
			}
		}
	}
	plant(visible, ba, "-v")
	plant(hidden, bb, "-h")
	dsn := env.AdminURL
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			return
		}
		defer c.Close(context.Background())
		_, _ = c.Exec(context.Background(), `DROP FUNCTION IF EXISTS public.s7c_leak(text)`)
		_, _ = c.Exec(context.Background(), `DELETE FROM delivery_stops WHERE order_id IN (SELECT id FROM orders WHERE order_no LIKE $1)`, "SO-S7C-"+tag+"%")
		_, _ = c.Exec(context.Background(), `DELETE FROM payments WHERE payment_no LIKE $1`, "PAY-S7C-"+tag+"%")
		_, _ = c.Exec(context.Background(), `DELETE FROM invoices WHERE invoice_no LIKE $1`, "INV-S7C-"+tag+"%")
		_, _ = c.Exec(context.Background(), `DELETE FROM quotes WHERE quote_no LIKE $1`, "Q-S7C-"+tag+"%")
		_, _ = c.Exec(context.Background(), `DELETE FROM orders WHERE order_no LIKE $1`, "SO-S7C-"+tag+"%")
		_, _ = c.Exec(context.Background(), `DELETE FROM customers WHERE account_no LIKE $1`, "S7C-"+tag+"%")
	})
	// The leaky function: created by the superuser (no assist role can create one), nearly free so the planner
	// wants it as early as it can, it raises when it is shown the hidden name. EXECUTE goes to the reader only.
	if _, err := env.Admin.Exec(ctx, `CREATE OR REPLACE FUNCTION public.s7c_leak(text) RETURNS boolean LANGUAGE plpgsql COST 0.0001 AS $f$
		BEGIN
		  IF $1 LIKE '%S7CHIDDEN%' THEN RAISE EXCEPTION 'LEAK saw %', $1; END IF;
		  RETURN true;
		END $f$`); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Admin.Exec(ctx, `GRANT EXECUTE ON FUNCTION public.s7c_leak(text) TO `+pgx.Identifier{env.Roles.Reader}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	claims := readtx.Claims{Audience: readtx.AudienceStaff, AskerID: "s7c-probe", Tier: 2, BranchIDs: []string{ba}, PaymentTrail: true}
	for _, v := range []struct{ view, col string }{
		{"ai.orders", "customer_name"}, {"ai.quotes", "customer_name"}, {"ai.invoices", "customer_name"}, {"ai.payments", "customer_name"},
		{"ai.delivery_stops", "customer_name"}, {"ai.customer_contacts", "customer_name"}, {"ai.ar_aging", "customer_name"}, {"ai.customers", "name"},
	} {
		var seen []string
		err := env.Readers.Staff.Run(ctx, claims, `SELECT `+v.col+` FROM `+v.view+` WHERE public.s7c_leak(`+v.col+`)`, nil, func(rs pgx.Rows) error {
			for rs.Next() {
				var n string
				if err := rs.Scan(&n); err != nil {
					return err
				}
				seen = append(seen, n)
			}
			return nil
		})
		if err != nil {
			t.Errorf("%s: a leaky function in the WHERE on %s raised (it saw a hidden name): %v", v.view, v.col, err)
			continue
		}
		visibleSeen := false
		for _, n := range seen {
			if strings.Contains(n, "S7CHIDDEN") {
				t.Errorf("%s returned the hidden name %q", v.view, n)
			}
			visibleSeen = visibleSeen || strings.Contains(n, "S7CVISIBLE")
		}
		if !visibleSeen {
			t.Errorf("%s: the probe never saw the visible control name, so it proves nothing (%v)", v.view, seen)
		}
	}
}
