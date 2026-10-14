//go:build demo

// The contractor world's exit test (PRO-H). The demo box's contractor account, Summit Ridge Builders,
// answered empty lists for orders, quotes, invoices and procurement projects; this test builds the
// image's own pipeline from zero (migrate, demoseed -apply, demo-populate, demoseed -apply, the
// entrypoint's order) and reads the world back through the SAME services the tenant routes call, one
// role at a time, each role reading only what the capability matrix lets it read.
//
// It runs on the same throwaway Postgres as the populate's exit tests and skips without DATABASE_URL.
package demo

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/flows"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/portal"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/procurement"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
	"github.com/jackc/pgx/v5/pgxpool"
)

// californiaBps is the San Diego County combined sales tax (GLS-TENANT-r3), the rate the contractor
// world's documents carry.
const californiaBps = 775

// halfUp is the core's own header rounding (tax.hstOn): subtotal times the rate over ten thousand,
// half up, once.
func halfUp(subtotal int64) int64 { return (subtotal*californiaBps + 5000) / 10000 }

// sandboxLast4 is the only last four a sandbox token view carries: no real card ends in it.
var sandboxLast4 = regexp.MustCompile(`^000[0-9]$`)

// contractorRoles are the five role codes the Pro apps are signed in as.
var contractorRoles = []string{"account_admin", "buyer", "buyer_no_pay", "ap_finance", "field_crew"}

// worldPipeline runs the entrypoint's order through the built binary and returns the pool and the DSN.
// The second seed apply is the one that adds the contractor world (the populate has made the account by
// then); a third apply and a second populate must write nothing.
func worldPipeline(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	pool, dsn := freshMigratedDB(t, "contractor")
	bin := buildDemoCore(t)
	env := subEnv(t, populateEnv(dsn))
	now := seedClock.Format(time.RFC3339)
	runStep(t, bin, []string{"demoseed", "-apply", "-now", now}, env)
	runStep(t, bin, []string{"demo-populate", "-now", now}, env)
	runStep(t, bin, []string{"demoseed", "-apply", "-now", now}, env)

	ctx := context.Background()
	tables := []string{"orders", "order_lines", "invoices", "invoice_lines", "payments", "payment_allocations",
		"quotes", "quote_lines", "procurement_projects", "procurement_project_lines", "portal_payment_methods",
		"projects", "portal_users", "customers", "scope_sequences"}
	before := snapshotWorld(t, ctx, pool, tables)
	out := runStep(t, bin, []string{"demoseed", "-apply", "-now", now}, env)
	runStep(t, bin, []string{"demo-populate", "-now", now}, env)
	assertWorldUnchanged(t, before, snapshotWorld(t, ctx, pool, tables))
	if !strings.Contains(out, "already complete") {
		t.Errorf("a third seed apply must say the world was already complete, got:\n%s", out)
	}
	return pool, dsn
}

func TestContractorWorld_EachRoleReadsWhatTheMatrixLetsItRead(t *testing.T) {
	ctx := context.Background()
	pool, _ := worldPipeline(t)

	cust := flows.TenantAccountID
	orders := order.NewService(order.NewRepository(pool))
	invoices := billing.NewService(billing.NewRepository(pool))
	quotes := quote.NewService(quote.NewRepository(pool))
	projects := procurement.NewService(procurement.NewRepository(pool))
	pay := payments.NewService(payments.NewRepository(pool), nil)
	jobs := portal.NewProjectsService(portal.NewProjectRepository(pool))
	team := portal.NewRepository(pool)

	for _, role := range contractorRoles {
		caps := portal.CapabilitiesFor(role)

		if caps.ViewOrdersDeliveries {
			list, err := orders.ListOrders(ctx, cust, order.Filter{Limit: 100})
			if err != nil {
				t.Fatalf("%s: orders: %v", role, err)
			}
			statuses := map[string]int{}
			delivery := 0
			for _, o := range list {
				statuses[o.Status]++
				if o.TaxCents != halfUp(o.SubtotalCents) || o.TotalCents != o.SubtotalCents+o.TaxCents {
					t.Errorf("%s: order %s money is %d + %d = %d, want tax %d at %d bps", role, o.OrderNo,
						o.SubtotalCents, o.TaxCents, o.TotalCents, halfUp(o.SubtotalCents), californiaBps)
				}
				d, err := orders.GetOrder(ctx, cust, o.ID)
				if err != nil {
					t.Fatalf("%s: order %s: %v", role, o.OrderNo, err)
				}
				var sum int64
				for _, l := range d.Lines {
					sum += l.LineTotalCents
				}
				if len(d.Lines) == 0 || sum != o.SubtotalCents {
					t.Errorf("%s: order %s lines sum to %d over %d lines, header subtotal %d", role, o.OrderNo, sum, len(d.Lines), o.SubtotalCents)
				}
				if strings.TrimSpace(d.DeliveryInstructions) != "" {
					delivery++
				}
			}
			for _, want := range []string{"CONFIRMED", "READY", "PARTIALLY_FULFILLED", "FULFILLED", "INVOICED"} {
				if statuses[want] == 0 {
					t.Errorf("%s: no %s order in the contractor's list %v", role, want, statuses)
				}
			}
			if delivery == 0 {
				t.Errorf("%s: no order carries delivery instructions", role)
			}
		}

		if caps.ViewBilling {
			list, err := invoices.ListInvoices(ctx, cust, billing.Filter{Limit: 100})
			if err != nil {
				t.Fatalf("%s: invoices: %v", role, err)
			}
			var overdue, openBalance, partial, paid int
			for _, inv := range list {
				if inv.TaxCents != halfUp(inv.SubtotalCents) || inv.TotalCents != inv.SubtotalCents+inv.TaxCents {
					t.Errorf("%s: invoice %s money is %d + %d = %d, want tax %d", role, inv.InvoiceNo,
						inv.SubtotalCents, inv.TaxCents, inv.TotalCents, halfUp(inv.SubtotalCents))
				}
				d, err := invoices.GetInvoice(ctx, cust, inv.ID)
				if err != nil {
					t.Fatalf("%s: invoice %s: %v", role, inv.InvoiceNo, err)
				}
				var sum int64
				for _, l := range d.Lines {
					sum += l.LineTotalCents
				}
				if len(d.Lines) == 0 || sum != inv.SubtotalCents {
					t.Errorf("%s: invoice %s lines sum to %d over %d lines, header %d", role, inv.InvoiceNo,
						sum, len(d.Lines), inv.SubtotalCents)
				}
				switch {
				case inv.Status == "PAID":
					paid++
				case inv.Status == "PARTIALLY_PAID":
					partial++
				case inv.Status == "FINALIZED" && inv.DueDate.Before(seedClock):
					overdue++
				case inv.Status == "FINALIZED" && inv.BalanceCents > 0:
					openBalance++
				}
			}
			if overdue != 1 || openBalance != 1 || partial != 1 || paid != 1 {
				t.Errorf("%s: invoices overdue %d, open with a balance %d, partly paid %d, paid %d; want one of each", role, overdue, openBalance, partial, paid)
			}

			history, err := pay.ListPayments(ctx, cust)
			if err != nil {
				t.Fatalf("%s: payments: %v", role, err)
			}
			if len(history) < 2 {
				t.Errorf("%s: payment history has %d rows, want at least 2", role, len(history))
			}
			for _, p := range history {
				var allocated int64
				for _, a := range p.Allocations {
					allocated += a.AmountCents
				}
				if allocated != p.AmountCents {
					t.Errorf("%s: payment %s is %d but allocates %d", role, p.PaymentNo, p.AmountCents, allocated)
				}
			}
		}

		if caps.ManagePaymentMethods {
			ms, err := pay.ListMethods(ctx, cust)
			if err != nil {
				t.Fatalf("%s: payment methods: %v", role, err)
			}
			defaults := 0
			for _, m := range ms {
				if m.IsDefault {
					defaults++
				}
				// The service never returns the token; the stored one is read here to hold it a sandbox token.
				var token string
				if err := pool.QueryRow(ctx, `SELECT gateway_token FROM portal_payment_methods WHERE id = $1::uuid`, m.ID).Scan(&token); err != nil {
					t.Fatalf("%s: method %s token: %v", role, m.ID, err)
				}
				if !strings.HasPrefix(token, "tok_sandbox_") || !sandboxLast4.MatchString(m.Last4) {
					t.Errorf("%s: method %s is not a sandbox token view (last4 %q)", role, m.ID, m.Last4)
				}
			}
			if len(ms) != 2 || defaults != 1 {
				t.Errorf("%s: %d saved methods with %d default, want 2 with 1", role, len(ms), defaults)
			}
		}

		if caps.SubmitRFQ {
			qs, err := quotes.ListQuotes(ctx, cust, quote.Filter{Limit: 100})
			if err != nil {
				t.Fatalf("%s: quotes: %v", role, err)
			}
			got := map[string]int{}
			for _, q := range qs {
				got[q.Status]++
				if q.TaxCents != halfUp(q.SubtotalCents) || q.TotalCents != q.SubtotalCents+q.TaxCents {
					t.Errorf("%s: quote %s money is %d + %d = %d", role, q.QuoteNo, q.SubtotalCents, q.TaxCents, q.TotalCents)
				}
			}
			for _, want := range []string{"SENT", "ACCEPTED", "LOST"} {
				if got[want] == 0 {
					t.Errorf("%s: no %s quote %v", role, want, got)
				}
			}

			ps, err := projects.ListProjects(ctx, cust)
			if err != nil {
				t.Fatalf("%s: procurement projects: %v", role, err)
			}
			stage := map[string]int{}
			for _, p := range ps {
				stage[p.Status]++
				if p.LineCount == 0 {
					t.Errorf("%s: procurement project %q has no lines", role, p.Name)
				}
			}
			if stage["PLANNING"] == 0 || stage["QUOTING"] < 2 {
				t.Errorf("%s: procurement stages %v, want a plan and two submitted RFQs", role, stage)
			}
			for _, p := range ps {
				d, err := projects.GetProject(ctx, cust, p.ID)
				if err != nil {
					t.Fatalf("%s: project %q: %v", role, p.Name, err)
				}
				priced := 0
				for _, l := range d.Lines {
					if l.Priced {
						priced++
					}
				}
				if p.Status == "QUOTING" && priced != len(d.Lines) {
					t.Errorf("%s: submitted project %q has %d of %d lines priced", role, p.Name, priced, len(d.Lines))
				}
				if p.Status == "PLANNING" && priced != 0 {
					t.Errorf("%s: planning project %q already has %d priced lines", role, p.Name, priced)
				}
			}
		}

		// Every role reads the three jobs and their addresses.
		sums, err := jobs.GetProjectDashboard(ctx, cust)
		if err != nil {
			t.Fatalf("%s: jobs: %v", role, err)
		}
		if len(sums) != 3 {
			t.Errorf("%s: %d jobs, want 3", role, len(sums))
		}
		addrs, err := jobs.ListAddresses(ctx, cust)
		if err != nil {
			t.Fatalf("%s: addresses: %v", role, err)
		}
		// The job address is one free text line (projects.job_address), city included.
		for _, city := range []string{"La Jolla", "Santa Fe", "Point Loma"} {
			seen := false
			for _, a := range addrs {
				seen = seen || a.Kind == portal.AddressKindJobSite && strings.Contains(a.Line1, city)
			}
			if !seen {
				t.Errorf("%s: no job site address in %s among %d", role, city, len(addrs))
			}
		}
	}

	members, err := team.ListUsers(ctx, cust)
	if err != nil {
		t.Fatalf("team: %v", err)
	}
	roles := map[string]bool{}
	for _, m := range members {
		roles[m.Role] = true
		if !strings.HasSuffix(m.Email, "@"+flows.ReservedDomain) {
			t.Errorf("team member %s sits outside %s", m.Role, flows.ReservedDomain)
		}
	}
	for _, r := range contractorRoles {
		if !roles[r] {
			t.Errorf("no team member holds %s", r)
		}
	}
}
