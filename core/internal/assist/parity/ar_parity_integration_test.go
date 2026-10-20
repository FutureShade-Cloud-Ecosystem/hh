//go:build integration

package parity

// Per record and per column parity for the receivables views (plan r3 section 1, slice 7a). Callers are built by
// the handler's own path; the payment trail answer comes from a reader keyed by the staff context's platform
// user id, standing for the role gate's admission to the two trail routes (the gate itself is the composition
// root's and has its own tests). The oracles are the modules' own scoped reads:
//   - invoices: the register (billing.RegisterService.Register, branch reach from the caller's context);
//   - aging: the aging screen's service read (billing.CreditService.Aging, same reach);
//   - payments: for a caller who holds the trail the payment history (till.Service.PaymentHistory), for one who
//     does not the customer workspace's payment list (billing.Repository.WorkspacePayments), the only screen that
//     shows a payment without the trail;
//   - allocations: the payment history's allocations and the invoice's payments (till.Service.InvoicePayments),
//     both trail routes, so only for a caller who holds the trail.
// The assistant's tier floor sits on top of every screen: a tier 3 caller reads each of these screens (the
// register is staff gated) and none of these views.

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx/testsupport"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

// trailBy answers HoldsPaymentTrail from a map of platform user ids.
type trailBy map[string]bool

func (m trailBy) HoldsPaymentTrail(_ context.Context, platformUserID, _ string) (bool, error) {
	return m[platformUserID], nil
}

type arCaller struct {
	name  string
	pid   string // the platform user id the trail reader is keyed by
	sc    identity.StaffContext
	trail bool
}

// arCallers: with and without the trail, branch scoped and global, plus a frontline caller (tier 3) and one
// with no grant.
func arCallers(w *arWorld) []arCaller {
	mk := func(name, role string, trail bool, branches ...string) arCaller {
		return arCaller{name: name, pid: "pu-" + name, sc: scoped(role, branches...), trail: trail}
	}
	gm := arCaller{name: "global with trail", pid: "pu-gm1", trail: true, sc: identity.StaffContext{UserID: "u-gm", RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal, Status: "ACTIVE"}}
	gmNo := arCaller{name: "global without trail", pid: "pu-gm2", trail: false, sc: identity.StaffContext{UserID: "u-gm2", RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal, Status: "ACTIVE"}}
	front := mk("frontline at a with trail", "YARD", true, w.a)
	front.sc.Tier = identity.TierFrontline
	return []arCaller{
		gm, gmNo,
		mk("manager at a with trail", "BRANCH_MANAGER", true, w.a),
		mk("manager at a without trail", "BRANCH_MANAGER", false, w.a),
		mk("manager at b with trail", "BRANCH_MANAGER", true, w.b),
		mk("manager at b without trail", "BRANCH_MANAGER", false, w.b),
		mk("manager at both without trail", "BRANCH_MANAGER", false, w.a, w.b),
		mk("manager with no grant with trail", "BRANCH_MANAGER", true),
		front,
	}
}

func trailMap(cs []arCaller) trailBy {
	m := trailBy{}
	for _, c := range cs {
		m[c.pid] = c.trail
	}
	return m
}

type arRig struct {
	env   *testsupport.Env
	w     *arWorld
	pool  *pgxpool.Pool
	h     *assist.Handler
	cs    []arCaller
	reg   *billing.RegisterService
	cred  *billing.CreditService
	till  *till.Service
	bill  *billing.Repository
	floor func(c arCaller) bool
}

func newARRig(t *testing.T) *arRig {
	t.Helper()
	env := testsupport.Open(t)
	w := plantARWorld(t, env)
	ctx := testsupport.Ctx(t)
	pool, err := pgxpool.New(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	cs := arCallers(w)
	bill := billing.NewRepository(pool)
	return &arRig{
		env: env, w: w, pool: pool, cs: cs,
		h:    assist.NewHandler(nil, nil).WithPaymentTrail(trailMap(cs)),
		reg:  billing.NewRegisterService(bill),
		cred: billing.NewCreditService(bill),
		till: till.NewService(till.NewRepository(pool)),
		bill: bill,
		floor: func(c arCaller) bool {
			return c.sc.Tier == identity.TierLeadership || c.sc.Tier == identity.TierManager
		},
	}
}

// claimsFor builds the asker's signed claims by the handler's own path.
func (r *arRig) claimsFor(t *testing.T, c arCaller) (assist.Caller, readtx.Claims) {
	t.Helper()
	req := newReq(testsupport.Ctx(t), c.sc)
	var sc guards.StaffCaller
	sc.PlatformUserID = c.pid
	caller, err := r.h.CallerFor(req, sc)
	if err != nil {
		t.Fatal(err)
	}
	if caller.PaymentTrail != c.trail {
		t.Fatalf("%s: the caller's payment trail is %v, want %v", c.name, caller.PaymentTrail, c.trail)
	}
	claims, err := caller.ReadClaims()
	if err != nil {
		t.Fatal(err)
	}
	return caller, claims
}

func (r *arRig) run(t *testing.T, claims readtx.Claims, sql string, each func(rs pgx.Rows) error) {
	t.Helper()
	if err := r.env.Readers.Staff.Run(testsupport.Ctx(t), claims, sql, nil, func(rs pgx.Rows) error {
		for rs.Next() {
			if err := each(rs); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func cents(s *string) *int64 { return i64p(s) }

// -- invoices -------------------------------------------------------------------------------------------

func TestParity_EveryInvoiceFollowsTheRegistersOwnRead(t *testing.T) {
	r := newARRig(t)
	ctx := testsupport.Ctx(t)
	compared := 0
	for _, c := range r.cs {
		_, claims := r.claimsFor(t, c)
		type row struct {
			no, docType, status, cust, acct, order, branchLabel string
			issue, due                                          string
			sub, tax, hold, total, bal                          int64
			name                                                string
		}
		got := map[string]row{}
		r.run(t, claims, `SELECT id::text, invoice_no, doc_type, status, issue_date::text, due_date::text, customer_name, account_no, order_no, subtotal_cents, tax_cents, holdback_cents, total_cents, balance_cents FROM ai.invoices`, func(rs pgx.Rows) error {
			var id string
			var x row
			if err := rs.Scan(&id, &x.no, &x.docType, &x.status, &x.issue, &x.due, &x.name, &x.acct, &x.order, &x.sub, &x.tax, &x.hold, &x.total, &x.bal); err != nil {
				return err
			}
			got[id] = x
			return nil
		})
		octx := identity.ContextWithStaff(ctx, c.sc)
		page, err := r.reg.Register(octx, billing.RegisterFilter{All: true, ShowCredits: true})
		if err != nil {
			t.Fatalf("%s: the register read: %v", c.name, err)
		}
		want := map[string]billing.RegisterRow{}
		for _, it := range page.Items {
			want[it.ID] = it
		}
		if len(want) == 0 && c.name != "manager with no grant with trail" {
			t.Fatalf("%s: the register returns nothing", c.name)
		}
		if !r.floor(c) {
			if len(got) != 0 {
				t.Errorf("%s: a tier 3 asker reads %d invoices through the assistant", c.name, len(got))
			}
			continue
		}
		for id, wl := range want {
			g, ok := got[id]
			if !ok {
				t.Errorf("%s: the register returns %s and the view does not", c.name, wl.InvoiceNo)
				continue
			}
			compared++
			if g.no != wl.InvoiceNo || g.docType != wl.DocType || g.status != wl.StoredStatus || g.name != wl.CustomerName || g.acct != wl.AccountNo ||
				g.order != wl.OrderNo || g.issue != wl.IssueDate.Format("2006-01-02") || g.due != wl.DueDate.Format("2006-01-02") ||
				g.total != wl.TotalCents || g.bal != wl.BalanceCents || g.hold != wl.HoldbackCents {
				t.Errorf("%s: %s differs: view %+v, register %+v", c.name, wl.InvoiceNo, g, wl)
			}
		}
		for id, g := range got {
			if _, ok := want[id]; !ok {
				t.Errorf("%s: the view returns %s and the register does not", c.name, g.no)
			}
		}
	}
	if compared < 30 {
		t.Fatalf("too little was compared: %d invoices", compared)
	}
	_ = fmt.Sprint
}

// -- aging ----------------------------------------------------------------------------------------------

func TestParity_EveryAgingRowFollowsTheAgingScreensOwnRead(t *testing.T) {
	r := newARRig(t)
	ctx := testsupport.Ctx(t)
	compared := 0
	for _, c := range r.cs {
		_, claims := r.claimsFor(t, c)
		type row struct{ cur, d30, d60, d90, d90p, open int64 }
		got := map[string]row{}
		r.run(t, claims, `SELECT customer_id::text, current_cents, days_1_30_cents, days_31_60_cents, days_61_90_cents, days_90_plus_cents, open_cents FROM ai.ar_aging`, func(rs pgx.Rows) error {
			var id string
			var x row
			if err := rs.Scan(&id, &x.cur, &x.d30, &x.d60, &x.d90, &x.d90p, &x.open); err != nil {
				return err
			}
			got[id] = x
			return nil
		})
		octx := identity.ContextWithStaff(ctx, c.sc)
		rows, err := r.cred.Aging(octx, time.Now(), "")
		if err != nil {
			t.Fatalf("%s: the aging read: %v", c.name, err)
		}
		want := map[string]row{}
		for _, a := range rows {
			want[a.CustomerID] = row{a.CurrentCents, a.Days30Cents, a.Days60Cents, a.Days60To90Cents, a.Days90PlusCents, a.TotalCents}
		}
		if !r.floor(c) {
			if len(got) != 0 {
				t.Errorf("%s: a tier 3 asker reads %d aging rows through the assistant", c.name, len(got))
			}
			continue
		}
		for id, wl := range want {
			g, ok := got[id]
			if !ok {
				t.Errorf("%s: the aging screen returns account %s and the view does not", c.name, id)
				continue
			}
			compared++
			if g != wl {
				t.Errorf("%s: account %s differs: view %+v, screen %+v", c.name, id, g, wl)
			}
		}
		for id := range got {
			if _, ok := want[id]; !ok {
				t.Errorf("%s: the view returns account %s and the aging screen does not", c.name, id)
			}
		}
	}
	if compared < 8 {
		t.Fatalf("too little was compared: %d aging rows", compared)
	}
}

// -- payments -------------------------------------------------------------------------------------------

func TestParity_EveryPaymentFollowsTheTrailOrTheWorkspacesRead(t *testing.T) {
	r := newARRig(t)
	ctx := testsupport.Ctx(t)
	type pay struct{ id, no, customer, branch string }
	var all []pay
	rows, err := r.env.Admin.Query(ctx, `SELECT id::text, payment_no::text, COALESCE(customer_id::text, ''), branch_id::text FROM payments`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var p pay
		if err := rows.Scan(&p.id, &p.no, &p.customer, &p.branch); err != nil {
			t.Fatal(err)
		}
		all = append(all, p)
	}
	rows.Close()
	if len(all) < 7 {
		t.Fatalf("the payment world holds %d payments", len(all))
	}
	workspace := map[string]map[string]billing.CustomerWorkspacePayment{} // customer -> payment id -> row
	for _, p := range all {
		if p.customer == "" || workspace[p.customer] != nil {
			continue
		}
		list, err := r.bill.WorkspacePayments(ctx, p.customer)
		if err != nil {
			t.Fatal(err)
		}
		workspace[p.customer] = map[string]billing.CustomerWorkspacePayment{}
		for _, wp := range list {
			workspace[p.customer][wp.ID] = wp
		}
	}
	compared, withCells, withoutCells := 0, 0, 0
	for _, c := range r.cs {
		caller, claims := r.claimsFor(t, c)
		type row struct {
			no, label, cust   string
			received          int64
			method, status    *string
			amount, unapplied *int64
		}
		got := map[string]row{}
		r.run(t, claims, `SELECT id::text, payment_no, branch_label, customer_name, floor(extract(epoch from received_at))::bigint, method, status, amount_cents::text, unapplied_cents::text FROM ai.payments`, func(rs pgx.Rows) error {
			var id string
			var x row
			var amt, un *string
			if err := rs.Scan(&id, &x.no, &x.label, &x.cust, &x.received, &x.method, &x.status, &amt, &un); err != nil {
				return err
			}
			x.amount, x.unapplied = cents(amt), cents(un)
			got[id] = x
			return nil
		})
		octx := identity.ContextWithStaff(ctx, c.sc)
		for _, p := range all {
			var hist till.PaymentHistory
			var herr error
			expect := false
			if c.trail {
				hist, herr = r.till.PaymentHistory(octx, p.id)
				expect = herr == nil
			} else {
				_, inWorkspace := workspace[p.customer][p.id]
				expect = inWorkspace && reaches(caller, p.branch)
			}
			expect = expect && r.floor(c)
			g, in := got[p.id]
			if in != expect {
				t.Errorf("%s: payment %s is in the view = %v, want %v", c.name, p.no, in, expect)
				continue
			}
			if !in {
				continue
			}
			compared++
			if g.no != p.no {
				t.Errorf("%s: payment number %q, want %q", c.name, g.no, p.no)
			}
			if c.trail {
				withCells++
				applied := int64(0)
				for _, a := range hist.Allocations {
					if !a.ReversedByNSF {
						applied += a.AmountCents
					}
				}
				un := hist.AmountCents - applied
				if un < 0 {
					un = 0
				}
				if g.method == nil || *g.method != hist.Method || g.status == nil || *g.status != hist.Status || g.amount == nil || *g.amount != hist.AmountCents ||
					g.unapplied == nil || *g.unapplied != un || g.received != hist.ReceivedAt.Unix() {
					t.Errorf("%s: payment %s differs from the history: view %+v, history %+v (unapplied %d)", c.name, p.no, g, hist, un)
				}
			} else {
				withoutCells++
				wp := workspace[p.customer][p.id]
				if g.method != nil || g.status != nil || g.amount != nil || g.unapplied != nil {
					t.Errorf("%s: a payment cell is present for a caller without the trail: %+v", c.name, g)
				}
				if g.received != wp.ReceivedAt.Unix() || g.no != wp.PaymentNo {
					t.Errorf("%s: payment %s differs from the workspace: view %+v, workspace %+v", c.name, p.no, g, wp)
				}
			}
		}
	}
	if compared < 25 || withCells < 8 || withoutCells < 8 {
		t.Fatalf("too little was compared: %d payments, %d with the trail, %d without", compared, withCells, withoutCells)
	}
}

// -- allocations ----------------------------------------------------------------------------------------

func TestParity_EveryAllocationFollowsTheTrailRoutes(t *testing.T) {
	r := newARRig(t)
	ctx := testsupport.Ctx(t)
	type pair struct{ payment, invoice string }
	type alloc struct{ payment, invoice string }
	var pays, invs []string
	for _, q := range []struct {
		sql string
		dst *[]string
	}{{`SELECT DISTINCT payment_id::text FROM payment_allocations`, &pays}, {`SELECT DISTINCT invoice_id::text FROM payment_allocations`, &invs}} {
		rows, err := r.env.Admin.Query(ctx, q.sql)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			*q.dst = append(*q.dst, id)
		}
		rows.Close()
	}
	compared, cross := 0, 0
	for _, c := range r.cs {
		_, claims := r.claimsFor(t, c)
		type row struct {
			pno, ino string
			amount   *int64
			at       *int64
		}
		got := map[pair]row{}
		r.run(t, claims, `SELECT payment_id::text, invoice_id::text, payment_no, invoice_no, amount_cents::text, extract(epoch from allocated_at)::bigint::text FROM ai.payment_allocations`, func(rs pgx.Rows) error {
			var p pair
			var x row
			var amt, at *string
			if err := rs.Scan(&p.payment, &p.invoice, &x.pno, &x.ino, &amt, &at); err != nil {
				return err
			}
			x.amount, x.at = cents(amt), cents(at)
			got[p] = x
			return nil
		})
		if !c.trail {
			if len(got) != 0 {
				t.Errorf("%s: a caller without the trail reads %d allocations", c.name, len(got))
			}
			continue
		}
		if !r.floor(c) {
			if len(got) != 0 {
				t.Errorf("%s: a tier 3 asker reads %d allocations through the assistant", c.name, len(got))
			}
			continue
		}
		octx := identity.ContextWithStaff(ctx, c.sc)
		want := map[pair]int64{}
		for _, id := range pays {
			h, err := r.till.PaymentHistory(octx, id)
			if err != nil {
				continue
			}
			for _, a := range h.Allocations {
				if !a.ReversedByNSF {
					want[pair{id, a.InvoiceID}] = a.AmountCents
				}
			}
		}
		for _, id := range invs {
			ip, err := r.till.InvoicePayments(octx, id)
			if err != nil {
				continue
			}
			for _, it := range ip.Items {
				want[pair{it.PaymentID, id}] = it.AmountCents
			}
		}
		for p, amt := range want {
			g, ok := got[p]
			if !ok {
				t.Errorf("%s: a trail route returns allocation %v and the view does not", c.name, p)
				continue
			}
			compared++
			if g.amount == nil || *g.amount != amt || g.at == nil {
				t.Errorf("%s: allocation %v differs: view %v, route %d", c.name, p, g.amount, amt)
			}
			if p.payment == r.w.payments["a-cross"] {
				cross++
			}
		}
		for p := range got {
			if _, ok := want[p]; !ok {
				t.Errorf("%s: the view returns allocation %v and no trail route does", c.name, p)
			}
		}
		// The numbers come through the row security of their own tables: empty only where that side is out of reach.
		if c.name == "manager at a with trail" {
			x := got[pair{r.w.payments["a-cross"], r.w.invoices["b-cust-a"]}]
			if x.pno == "" || x.ino != "" {
				t.Errorf("%s: the cross branch allocation should name the payment (in reach) and not the invoice (out of reach): %+v", c.name, x)
			}
		}
	}
	if compared < 9 || cross < 3 {
		t.Fatalf("too little was compared: %d allocations, %d cross branch", compared, cross)
	}
	_ = strconv.Itoa
}

// The aging day is the dealer's day: with the business zone set to a zone a whole day apart from the database's,
// the view and the aging screen still age every account against the same day, and the view reports that day.
func TestParity_AgingUsesTheDealersDay(t *testing.T) {
	r := newARRig(t)
	ctx := testsupport.Ctx(t)
	var prior *string
	err := r.env.Admin.QueryRow(ctx, `SELECT value::text FROM system_settings WHERE key = 'business_zone'`).Scan(&prior)
	if err != nil && err != pgx.ErrNoRows {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, cerr := pgx.Connect(context.Background(), r.env.AdminURL)
		if cerr != nil {
			return
		}
		defer c.Close(context.Background())
		if prior == nil {
			_, _ = c.Exec(context.Background(), `DELETE FROM system_settings WHERE key = 'business_zone'`)
		} else {
			_, _ = c.Exec(context.Background(), `UPDATE system_settings SET value = $1::jsonb WHERE key = 'business_zone'`, *prior)
		}
	})
	var gm arCaller
	for _, c := range r.cs {
		if c.name == "global with trail" {
			gm = c
		}
	}
	_, claims := r.claimsFor(t, gm)
	days := map[string]string{}
	for _, zone := range []string{"Pacific/Kiritimati", "Pacific/Pago_Pago"} {
		if _, err := r.env.Admin.Exec(ctx, `INSERT INTO system_settings (key, value) VALUES ('business_zone', to_jsonb($1::text)) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, zone); err != nil {
			t.Fatal(err)
		}
		var asOf string
		r.run(t, claims, `SELECT DISTINCT as_of::text FROM ai.ar_aging`, func(rs pgx.Rows) error { return rs.Scan(&asOf) })
		var want string
		if err := r.env.Admin.QueryRow(ctx, `SELECT (now() AT TIME ZONE $1)::date::text`, zone).Scan(&want); err != nil {
			t.Fatal(err)
		}
		if asOf != want {
			t.Errorf("zone %s: the view ages as of %s, the dealer's day is %s", zone, asOf, want)
		}
		days[zone] = asOf
		got := map[string]int64{}
		r.run(t, claims, `SELECT customer_id::text, open_cents, days_90_plus_cents + days_61_90_cents * 1000000 FROM ai.ar_aging`, func(rs pgx.Rows) error {
			var id string
			var open, mix int64
			if err := rs.Scan(&id, &open, &mix); err != nil {
				return err
			}
			got[id] = mix
			return nil
		})
		rows, err := r.cred.Aging(identity.ContextWithStaff(ctx, gm.sc), time.Now(), "")
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range rows {
			if w, ok := got[a.CustomerID]; !ok || w != a.Days90PlusCents+a.Days60To90Cents*1000000 {
				t.Errorf("zone %s: account %s buckets differ between the view and the screen", zone, a.CustomerID)
			}
		}
		if len(got) != len(rows) {
			t.Errorf("zone %s: the view has %d accounts, the screen %d", zone, len(got), len(rows))
		}
	}
	if days["Pacific/Kiritimati"] == days["Pacific/Pago_Pago"] {
		t.Errorf("the two zones gave the same day %s: the view ignores the dealer's zone", days["Pacific/Kiritimati"])
	}
}
