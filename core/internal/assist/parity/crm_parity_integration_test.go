//go:build integration

package parity

// Per record and per column parity for the customer views (plan r3 section 1, slice 7b).
//
//   - ai.customers is the account the customer screens return: customer.Service.GetAccount under the caller's
//     staff context (an account homed to a branch the caller reaches, or homed to none), with the same cells. The
//     credit limit and the balance are not in the view.
//   - ai.customer_contacts is the contact the receivables register's invoice detail shows: the account's first
//     active contact (the primary one first), for a caller who reaches one of the account's invoices
//     (billing.RegisterService.Detail), and no other contact of the account.

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx/testsupport"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

type crmWorld struct {
	a, b      string
	tag       string
	customers map[string]string
	contacts  map[string]string
}

func plantCRMWorld(t *testing.T, env *testsupport.Env) *crmWorld {
	t.Helper()
	ctx := testsupport.Ctx(t)
	tag := randomHex(t, 3)
	w := &crmWorld{tag: tag, customers: map[string]string{}, contacts: map[string]string{}}
	w.a = plantBranch(t, env, tag+"CA", "S7b crm A")
	w.b = plantBranch(t, env, tag+"CB", "S7b crm B")
	cust := func(name, home string, active bool) {
		var id string
		if err := env.Admin.QueryRow(ctx, `INSERT INTO customers (account_no, name, home_branch_id, on_hold, tax_exempt, is_active, credit_limit_cents, billing_address)
			VALUES ($1, $2, NULLIF($3,'')::uuid, $4, $5, $6, 987654, '{"line1":"9 Billing Secret Road"}') RETURNING id::text`,
			"S7B-"+tag+name, "Planted CRM "+name+" "+tag, home, name == "held", name == "exempt", active).Scan(&id); err != nil {
			t.Fatalf("plant customer %s: %v", name, err)
		}
		w.customers[name] = id
	}
	cust("home-a", w.a, true)
	cust("home-b", w.b, true)
	cust("company", "", true)
	cust("held", w.a, true)
	cust("exempt", w.b, true)
	cust("inactive", w.a, false)
	cust("no-invoice", w.a, true)
	contact := func(name, cust, who string, primary, active bool, ageDays int) {
		var id string
		if err := env.Admin.QueryRow(ctx, `INSERT INTO customer_contacts (customer_id, name, email, phone, role, is_primary, is_active, created_at)
			VALUES ($1::uuid, $2, $3, $4, 'Buyer', $5, $6, now() - make_interval(days => $7)) RETURNING id::text`,
			w.customers[cust], who+" "+tag, who+"."+tag+"@example.test", "555-02"+fmt.Sprintf("%02d", len(w.contacts)), primary, active, ageDays).Scan(&id); err != nil {
			t.Fatalf("plant contact %s: %v", name, err)
		}
		w.contacts[name] = id
	}
	// home-a: an inactive primary, then an active non primary, then the active primary: the primary wins.
	contact("a-inactive-primary", "home-a", "Ina", true, false, 30)
	contact("a-plain", "home-a", "Plain", false, true, 20)
	contact("a-primary", "home-a", "Prim", true, true, 10)
	// home-b: two active non primary: the older one wins.
	contact("b-late", "home-b", "Later", false, true, 5)
	contact("b-early", "home-b", "Earlier", false, true, 15)
	// company: one contact. held: only an inactive contact. no-invoice: a contact but no invoice.
	contact("c-one", "company", "Company", true, true, 1)
	contact("h-inactive", "held", "Gone", true, false, 1)
	contact("n-one", "no-invoice", "Lonely", true, true, 1)
	inv := func(name, cust, br string) {
		if _, err := env.Admin.Exec(ctx, `INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date, subtotal_cents, total_cents, balance_cents)
			VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', (now() AT TIME ZONE 'America/Toronto')::date - 20, (now() AT TIME ZONE 'America/Toronto')::date + 10, 100, 100, 100)`,
			fmt.Sprintf("INV-S7B-%s-%s", tag, name), w.customers[cust], br); err != nil {
			t.Fatalf("plant invoice %s: %v", name, err)
		}
	}
	inv("home-a", "home-a", w.a)
	inv("home-b", "home-b", w.b)
	inv("company", "company", w.b) // a company wide account invoiced at b
	inv("held", "held", w.a)
	inv("exempt", "exempt", w.b)
	inv("cross", "home-a", w.b) // home-a is also invoiced at b
	dsn := env.AdminURL
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			return
		}
		defer c.Close(context.Background())
		_, _ = c.Exec(context.Background(), `DELETE FROM invoices WHERE invoice_no LIKE $1`, "INV-S7B-"+tag+"-%")
		_, _ = c.Exec(context.Background(), `DELETE FROM customers WHERE account_no LIKE $1`, "S7B-"+tag+"%")
	})
	return w
}

func (w *crmWorld) branches() []string { return []string{w.a, w.b} }

func TestParity_EveryCustomerFollowsTheCustomerScreensOwnRead(t *testing.T) {
	env := testsupport.Open(t)
	w := plantCRMWorld(t, env)
	ctx := testsupport.Ctx(t)
	pool, err := pgxpool.New(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	svc := customer.NewService(customer.NewRepository(pool))
	h := assist.NewHandler(nil, nil)
	var all []string
	rows, err := env.Admin.Query(ctx, `SELECT id::text FROM customers`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		all = append(all, id)
	}
	rows.Close()
	compared := 0
	for _, c := range deliveryCallers(w.branches()...) {
		_, claims := claimsOf(t, h, c.sc)
		floor := c.sc.Tier == identity.TierLeadership || c.sc.Tier == identity.TierManager
		type cells struct {
			acct, name, typ, label, hold, exempt, active string
			terms                                        int
		}
		got := map[string]cells{}
		readRows(t, env, claims, `SELECT id::text, account_no, name, customer_type, branch_label, on_hold, tax_exempt, is_active, payment_terms_days FROM ai.customers`, func(rs pgx.Rows) error {
			var id string
			var x cells
			if err := rs.Scan(&id, &x.acct, &x.name, &x.typ, &x.label, &x.hold, &x.exempt, &x.active, &x.terms); err != nil {
				return err
			}
			got[id] = x
			return nil
		})
		octx := identity.ContextWithStaff(ctx, c.sc)
		for _, id := range all {
			a, aerr := svc.GetAccount(octx, id)
			want := aerr == nil && floor
			x, in := got[id]
			if in != want {
				t.Errorf("%s: customer %s is in the view = %v, want %v (screen read ok=%v)", c.name, id, in, want, aerr == nil)
				continue
			}
			if !in {
				continue
			}
			compared++
			yn := func(b bool) string {
				if b {
					return "yes"
				}
				return "no"
			}
			if x.acct != a.AccountNo || x.name != a.Name || x.typ != a.CustomerType || x.terms != a.PaymentTermsDays || x.hold != yn(a.OnHold) || x.exempt != yn(a.TaxExempt) || x.active != yn(a.IsActive) {
				t.Errorf("%s: customer %s differs: view %+v, screen %+v", c.name, id, x, a)
			}
		}
	}
	if compared < 25 {
		t.Fatalf("too little was compared: %d customers", compared)
	}
}

func TestParity_EveryCustomerContactFollowsTheRegistersInvoiceDetail(t *testing.T) {
	env := testsupport.Open(t)
	w := plantCRMWorld(t, env)
	ctx := testsupport.Ctx(t)
	pool, err := pgxpool.New(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	reg := billing.NewRegisterService(billing.NewRepository(pool))
	h := assist.NewHandler(nil, nil)
	invoicesOf := map[string][]string{}
	rows, err := env.Admin.Query(ctx, `SELECT customer_id::text, id::text FROM invoices`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var cu, id string
		if err := rows.Scan(&cu, &id); err != nil {
			t.Fatal(err)
		}
		invoicesOf[cu] = append(invoicesOf[cu], id)
	}
	rows.Close()
	var withContacts []string
	rows, err = env.Admin.Query(ctx, `SELECT DISTINCT customer_id::text FROM customer_contacts`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var cu string
		if err := rows.Scan(&cu); err != nil {
			t.Fatal(err)
		}
		withContacts = append(withContacts, cu)
	}
	rows.Close()
	compared := 0
	for _, c := range deliveryCallers(w.branches()...) {
		_, claims := claimsOf(t, h, c.sc)
		floor := c.sc.Tier == identity.TierLeadership || c.sc.Tier == identity.TierManager
		type cells struct{ name, email, phone, acct, cust string }
		got := map[string][]cells{}
		readRows(t, env, claims, `SELECT customer_id::text, contact_name, contact_email, contact_phone, account_no, customer_name FROM ai.customer_contacts`, func(rs pgx.Rows) error {
			var id string
			var x cells
			if err := rs.Scan(&id, &x.name, &x.email, &x.phone, &x.acct, &x.cust); err != nil {
				return err
			}
			got[id] = append(got[id], x)
			return nil
		})
		octx := identity.ContextWithStaff(ctx, c.sc)
		for _, cu := range withContacts {
			var block *billing.CustomerBlock
			for _, inv := range invoicesOf[cu] {
				d, err := reg.Detail(octx, inv, false)
				if err == nil {
					b := d.Customer
					block = &b
					break
				}
			}
			want := block != nil && block.ContactName != "" && floor
			rowsOf := got[cu]
			if (len(rowsOf) > 0) != want || len(rowsOf) > 1 {
				t.Errorf("%s: customer %s has %d contact rows in the view, want %v", c.name, cu, len(rowsOf), want)
				continue
			}
			if !want {
				continue
			}
			compared++
			x := rowsOf[0]
			if x.name != block.ContactName || x.email != block.ContactEmail || x.phone != block.ContactPhone || x.acct != block.AccountNo || x.cust != block.Name {
				t.Errorf("%s: customer %s contact differs: view %+v, invoice detail %+v", c.name, cu, x, block)
			}
		}
		// The planted choices: the primary wins over an older plain contact, the older of two plain contacts wins,
		// an inactive contact is never shown, and an account with no invoice in reach has no row.
		if c.name == "global manager" {
			want := map[string]string{"home-a": "Prim " + w.tag, "home-b": "Earlier " + w.tag, "company": "Company " + w.tag}
			for cu, name := range want {
				if rowsOf := got[w.customers[cu]]; len(rowsOf) != 1 || rowsOf[0].name != name {
					t.Errorf("global manager: %s contact = %+v, want %s", cu, rowsOf, name)
				}
			}
			for _, cu := range []string{"held", "no-invoice"} {
				if len(got[w.customers[cu]]) != 0 {
					t.Errorf("global manager: %s should have no contact row: %+v", cu, got[w.customers[cu]])
				}
			}
		}
	}
	if compared < 8 {
		t.Fatalf("too little was compared: %d contacts", compared)
	}
}
