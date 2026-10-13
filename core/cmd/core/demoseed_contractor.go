// demoseed_contractor.go, the contractor world: what Summit Ridge Builders, the demo's one contractor
// account (GLS-TENANT-r3), holds when the three Pro apps open on the demo box.
//
// The populate makes the account, its five portal users and its three jobs (internal/demo
// populate.go, through the customer, identity and portal seams) and writes no document. Every tenant
// read of orders, quotes, invoices, payments and procurement projects therefore answered an empty
// list. This section is the documents: eight orders in the states the order contract draws, four
// invoices with their payments, three quotes, three procurement projects, and two saved payment
// methods that are sandbox tokens.
//
// WHY THE SEED AND NOT THE POPULATE. The seed is the demo's one writer of documents, INSERT only, on
// stable ids, and it already runs after the populate (the entrypoint's third step, which is how the
// quote world waits for its two logins). No migration is needed and no module table is reached from
// a module: this command sits outside the module fence like every other section of it.
//
// CONDITIONAL, LIKE THE QUOTE WORLD. The section builds nothing until the populate has made the
// account (planInput.Contractor.Ready, read from the database), so the default build and a first
// apply plant exactly what they always planted. Its dates run relative to the plan clock, the same
// discipline as every section: the first apply that sees the account decides what "twenty days ago"
// means, and the invoice that is overdue then stays the overdue invoice.
//
// MONEY. Integer cents, half up, struck the way the core strikes them:
//
//   - a contractor line prices at list less the CONTRACTOR tier (fifteen percent, the migrations'
//     TIER_DEFAULT row, the price half up once on the unit, as pricing applyPctOff), times a whole quantity;
//   - the header tax is the subtotal times the San Diego County rate (775 basis points, GLS-TENANT-r3)
//     over ten thousand, half up ONCE at the header (tax.hstOn), and the per line tax splits that
//     header figure by largest remainder over the line totals (moneyutil.AllocateLargestRemainder),
//     so the lines always add to the header;
//   - an invoice carries its order's lines and totals, its balance is the total less what its
//     payment allocated, and its status follows the balance.
//
// THE TAX ENGINE DOES NOT KNOW CALIFORNIA. internal/tax holds Ontario rate rows only, so a recompute
// through the engine would answer thirteen percent. The documents here are stored figures that no
// read recomputes; the brief for the world named 7.75 percent and the tenant sheet agrees, and the
// disagreement is the modules cycle's to settle (row 1556), reported rather than hidden.
//
// WHAT THIS WORLD DOES NOT CARRY, and what a tester sees for it:
//
//   - seq. The real writers stamp the sync sequence; the INSERT-only applier cannot advance the
//     counter, and a seq written without advancing it would collide with the next real write. The
//     rows stay seq NULL: the Pro apps read over REST and see everything, a sync client does not.
//   - the quote share links, acceptance records and audit rows the real quote paths write. The three
//     quotes are readable and decidable by the tenant routes; the dealer side history is thinner.
//   - a way back. Apply never updates a row, so a payment a tester makes, an order a tester converts
//     or a quote a tester decides stays as they left it, and the demo states do not come back.
//
// NOT DAY-KEYED. A document is not a day: stable keys, written once.
package main

import (
	"fmt"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/moneyutil"
)

const (
	// contractorCustomerID is the account's pinned id (flows.TenantAccountID, repeated here because that
	// package is demo tagged and this file is in both builds; the demo world proof asserts the two agree).
	contractorCustomerID = "de2c0000-0000-4000-9000-000000000001"
	// contractorTaxBps is the San Diego County combined sales tax rate.
	contractorTaxBps = 775
	// contractorTierBps is the CONTRACTOR tier discount off list (the migrations' TIER_DEFAULT).
	contractorTierBps = 1500
	// contractorTermsDays is the account's payment terms (populate.go PaymentTermsDays).
	contractorTermsDays = 30
)

// contractorWorldRows is the number of rows the section writes: 8 orders with 15 lines, 4 invoices with 7
// lines, 2 payments with 2 allocations, 3 quotes with 6 lines, 3 procurement projects with 8 lines, and 2
// saved payment methods. The plan test holds the figure to the plan.
const contractorWorldRows = 8 + 15 + 4 + 7 + 2 + 2 + 3 + 6 + 3 + 8 + 2

// contractorJobIDs are the three job sites the populate makes (flows.TenantJobs), in its order: La Jolla,
// Santa Fe, Point Loma. Asserted against the flow table by the demo world proof.
var contractorJobIDs = [3]string{
	"de2c0000-0000-4000-9000-0000000000b1",
	"de2c0000-0000-4000-9000-0000000000b2",
	"de2c0000-0000-4000-9000-0000000000b3",
}

// contractorJobAddress is each job's delivery line, as the populate wrote it.
var contractorJobAddress = [3]string{
	"4100 Example Bluff Road, La Jolla, CA",
	"2250 Example Ridge Lane, Santa Fe, CA",
	"980 Example Cove Street, Point Loma, CA",
}

// contractorFacts is what the plan needs from the database about the account: that the populate has
// made it, the branch it is homed at, and the portal user id of each role (for the created by column).
type contractorFacts struct {
	Ready    bool
	BranchID string
	Users    map[string]string // portal role -> portal_users.id
}

type cLine struct {
	Prod string
	Qty  int
}

type cOrder struct {
	Key         string
	Job         int
	Status      string
	Fulfillment string
	AgeDays     int
	ReqOffset   int
	Instruction string
	Lines       []cLine
	// Invoice is the key of the invoice this order was billed on, "" for none.
	Invoice string
}

type cInvoice struct {
	Key      string
	Order    string
	IssueAge int // days before the clock the invoice was issued
	PaidPct  int // share of the total paid, 0 to 100
	PaidAge  int // days before the clock the payment was received
}

type cQuote struct {
	Key     string
	Job     int
	Status  string
	AgeDays int
	Valid   int // days past the clock the quote stays valid
	Notes   string
	Lines   []cLine
}

type cProject struct {
	Key    string
	Job    int
	Name   string
	Status string
	Age    int
	Lines  []cLine
	Priced bool // the RFQ was submitted, so the lines carry indicative prices
}

// contractorOrders: one order for every state the contract's order map draws, four of them billed.
var contractorOrders = []cOrder{
	{"CO1", 0, "INVOICED", "DELIVERY", 75, -70, "Deliver to the side gate. The crew starts on site before the first drop.",
		[]cLine{{"P01", 480}, {"P10", 20}}, "IV1"},
	{"CO2", 1, "INVOICED", "DELIVERY", 30, -26, "",
		[]cLine{{"P04", 300}, {"P07", 12}}, "IV2"},
	{"CO3", 2, "INVOICED", "DELIVERY", 95, -92, "Unload at the back driveway only.",
		[]cLine{{"P05", 8}, {"P06", 6}}, "IV3"},
	{"CO4", 0, "INVOICED", "DELIVERY", 25, -22, "",
		[]cLine{{"P02", 360}}, "IV4"},
	{"CO5", 1, "FULFILLED", "DELIVERY", 12, -9, "Stack the block on pallets by the lower wall.",
		[]cLine{{"P04", 200}, {"P10", 16}}, ""},
	{"CO6", 0, "PARTIALLY_FULFILLED", "DELIVERY", 6, -3, "Call the site lead from the gate before the truck enters.",
		[]cLine{{"P01", 600}, {"P09", 10}}, ""},
	{"CO7", 2, "READY", "DELIVERY", 3, 2, "The front of the property is closed for the pour. Use the back driveway.",
		[]cLine{{"P03", 220}, {"P10", 12}}, ""},
	{"CO8", 1, "CONFIRMED", "PICKUP", 1, 6, "",
		[]cLine{{"P07", 18}, {"P06", 9}}, ""},
}

// contractorInvoices: paid, partly paid, open with a balance not yet due, and overdue. The terms are
// thirty days, so the due date is the issue date plus thirty.
var contractorInvoices = []cInvoice{
	{"IV1", "CO1", 70, 100, 62},
	{"IV2", "CO2", 20, 40, 10},
	{"IV3", "CO3", 90, 0, 0},
	{"IV4", "CO4", 15, 0, 0},
}

// contractorQuotes: one the contractor can act on, one accepted and waiting to be converted, one
// declined.
var contractorQuotes = []cQuote{
	{"CQ1", 2, "SENT", 5, 30, "Pricing holds for the quote's validity.",
		[]cLine{{"P02", 500}, {"P06", 14}, {"P07", 9}}},
	{"CQ2", 0, "ACCEPTED", 12, 30, "Accepted. Convert it to an order when the site is ready.",
		[]cLine{{"P01", 900}, {"P10", 30}}},
	{"CQ3", 1, "LOST", 20, 0, "Declined. The pour dates moved.",
		[]cLine{{"P03", 400}}},
}

// contractorProjects: a plan with lines, and two whose RFQ went to the dealer and came back
// indicatively priced (the second is the one a quote is answering).
var contractorProjects = []cProject{
	{"PP1", 0, "Coastal terrace materials", "PLANNING", 8,
		[]cLine{{"P01", 700}, {"P03", 250}, {"P10", 18}}, false},
	{"PP2", 1, "Hillside retaining walls", "QUOTING", 14,
		[]cLine{{"P04", 640}, {"P07", 20}}, true},
	{"PP3", 2, "Courtyard and driveway", "QUOTING", 7,
		[]cLine{{"P02", 500}, {"P06", 14}, {"P07", 9}}, true},
}

// contractorMethods are the two saved payment methods. A sandbox token and a last four of the form
// 000n: no card number is held anywhere, and nothing here is a real account. The card expires
// ExpYearsOut years after the plan clock's year, so no calendar year is written here.
var contractorMethods = []struct {
	Key, Type, Brand, Last4, Token string
	ExpMonth, ExpYearsOut          int
	Default                        bool
}{
	{"card", "CARD", "Sandbox card", "0001", "tok_sandbox_demo_card", 12, 4, true},
	{"bank", "BANK", "Sandbox bank", "0002", "tok_sandbox_demo_bank", 0, 0, false},
}

// contractorUnit is a contractor's unit price, struck as the pricing engine's applyPctOff does: the
// PRICE (list times ten thousand less the tier, over ten thousand) rounded half up, not the discount
// rounded and subtracted, which lands a cent low on every half cent discount.
func (b *builder) contractorUnit(prod string) int64 {
	list := b.prod[prod].ListCents
	return (list*(10000-contractorTierBps) + 5000) / 10000
}

// contractorTax is the header tax: subtotal times the rate over ten thousand, half up, once.
func contractorTax(subtotal int64) int64 {
	return (subtotal*contractorTaxBps + 5000) / 10000
}

// contractorMoney is one document's figures: the line totals, the header subtotal, tax and total, and
// the tax split across the lines by largest remainder.
type contractorMoney struct {
	Unit, Total, Tax []int64
	Subtotal, HTax   int64
}

func (b *builder) contractorMoney(lines []cLine) contractorMoney {
	m := contractorMoney{}
	for _, l := range lines {
		u := b.contractorUnit(l.Prod)
		t := u * int64(l.Qty)
		m.Unit = append(m.Unit, u)
		m.Total = append(m.Total, t)
		m.Subtotal += t
	}
	m.HTax = contractorTax(m.Subtotal)
	m.Tax = moneyutil.AllocateLargestRemainder(m.HTax, m.Total)
	return m
}

func (b *builder) contractorNo(kind, key string) string {
	return fmt.Sprintf("%s%s-%s", demoPrefix, kind, key)
}

// contractorWorld is the section's hook, the one call buildPlan makes. It builds nothing until the
// populate has made the account.
func (b *builder) contractorWorld() {
	f := b.in.Contractor
	if !f.Ready || f.BranchID == "" {
		return
	}
	add := func(r row) {
		b.add(r)
		b.sum.ContractorRowIDs = append(b.sum.ContractorRowIDs, fmt.Sprint(r.vals[0]))
	}
	cust := contractorCustomerID
	orderMoney := map[string]contractorMoney{}
	orderByKey := map[string]cOrder{}
	invoiceByOrder := map[string]cInvoice{}
	for _, iv := range contractorInvoices {
		invoiceByOrder[iv.Order] = iv
	}

	// -- orders and their lines ------------------------------------------------------------------
	for _, o := range contractorOrders {
		orderByKey[o.Key] = o
		m := b.contractorMoney(o.Lines)
		orderMoney[o.Key] = m
		created := b.agoDays(o.AgeDays)
		var instruction any
		if o.Instruction != "" {
			instruction = o.Instruction
		}
		oid := demoID("contractor-order:" + o.Key)
		add(insert("orders", "(id)",
			"id", oid,
			"order_no", b.contractorNo("CO", o.Key),
			"customer_id", cust,
			"branch_id", f.BranchID,
			"status", o.Status,
			"fulfillment", o.Fulfillment,
			"requested_date", b.day(o.ReqOffset),
			"subtotal_cents", m.Subtotal, "tax_cents", m.HTax, "total_cents", m.Subtotal+m.HTax,
			"delivery_instructions", instruction,
			"project_id", contractorJobIDs[o.Job],
			"job_address", contractorJobAddress[o.Job],
			"org_id", driverOrgStamp(),
			"tenant_id", cust,
			"metadata", demoMetadata,
			"created_at", created, "updated_at", created,
		))
		b.sum.ContractorLines = append(b.sum.ContractorLines,
			fmt.Sprintf("order    %s  %-19s  %d line(s), total %d cents", b.contractorNo("CO", o.Key), o.Status,
				len(o.Lines), m.Subtotal+m.HTax))
		for i, l := range o.Lines {
			p := b.prod[l.Prod]
			qty := fmt.Sprintf("%d.0000", l.Qty)
			fulfilled, allocated, backorder := "0", "0", "0"
			switch {
			case o.Status == "FULFILLED" || o.Status == "INVOICED":
				fulfilled = qty
			case o.Status == "PARTIALLY_FULFILLED" && i == 0:
				fulfilled = fmt.Sprintf("%d.0000", l.Qty/2)
				backorder = fmt.Sprintf("%d.0000", l.Qty-l.Qty/2)
			case o.Status == "READY":
				allocated = qty
			}
			add(insert("order_lines", "(id)",
				"id", demoID(fmt.Sprintf("contractor-orderline:%s:%d", o.Key, i+1)),
				"order_id", oid, "line_no", i+1,
				"product_id", demoID("product:"+l.Prod), "description", p.Name,
				"qty_entered", qty, "uom_entered", p.UOM, "qty_base", qty,
				"allocated_base", allocated, "fulfilled_base", fulfilled,
				"backorder_entered", backorder, "backorder_base", backorder,
				"unit_price_cents", m.Unit[i], "price_uom", p.UOM,
				"line_total_cents", m.Total[i], "tax_cents", m.Tax[i],
				"org_id", driverOrgStamp(),
				"tenant_id", cust,
				"metadata", demoMetadata,
				"created_at", created, "updated_at", created,
			))
		}
	}

	// -- invoices, their lines and the payments behind them --------------------------------------
	for _, iv := range contractorInvoices {
		o := orderByKey[iv.Order]
		m := orderMoney[iv.Order]
		total := m.Subtotal + m.HTax
		paid := total * int64(iv.PaidPct) / 100
		balance := total - paid
		status := "FINALIZED"
		switch {
		case balance <= 0:
			status, balance = "PAID", 0
		case paid > 0:
			status = "PARTIALLY_PAID"
		}
		issued := b.agoDays(iv.IssueAge)
		iid := demoID("contractor-invoice:" + iv.Key)
		add(insert("invoices", "(id)",
			"id", iid,
			"invoice_no", b.contractorNo("INV", iv.Key),
			"customer_id", cust,
			"order_id", demoID("contractor-order:"+o.Key),
			"branch_id", f.BranchID,
			"status", status,
			"issue_date", b.day(-iv.IssueAge),
			"due_date", b.day(-iv.IssueAge+contractorTermsDays),
			"subtotal_cents", m.Subtotal, "tax_cents", m.HTax, "total_cents", total,
			"balance_cents", balance,
			"finalized_at", issued,
			"source_kind", "ORDER",
			"org_id", driverOrgStamp(),
			"metadata", demoMetadata,
			"created_at", issued, "updated_at", issued,
		))
		b.sum.ContractorLines = append(b.sum.ContractorLines,
			fmt.Sprintf("invoice  %s  %-14s  total %d, balance %d, due in %d day(s)", b.contractorNo("INV", iv.Key),
				status, total, balance, contractorTermsDays-iv.IssueAge))
		for i, l := range o.Lines {
			p := b.prod[l.Prod]
			qty := fmt.Sprintf("%d.0000", l.Qty)
			add(insert("invoice_lines", "(id)",
				"id", demoID(fmt.Sprintf("contractor-invoiceline:%s:%d", iv.Key, i+1)),
				"invoice_id", iid, "line_no", i+1,
				"order_line_id", demoID(fmt.Sprintf("contractor-orderline:%s:%d", o.Key, i+1)),
				"product_id", demoID("product:"+l.Prod), "description", p.Name,
				"qty_entered", qty, "uom_entered", p.UOM, "qty_base", qty,
				"unit_price_cents", m.Unit[i], "price_uom", p.UOM,
				"line_total_cents", m.Total[i], "tax_cents", m.Tax[i],
				"org_id", driverOrgStamp(),
				"created_at", issued,
			))
		}
		if paid > 0 {
			received := b.agoDays(iv.PaidAge)
			pid := demoID("contractor-payment:" + iv.Key)
			add(insert("payments", "(id)",
				"id", pid,
				"payment_no", b.contractorNo("PAY", iv.Key),
				"customer_id", cust,
				"method", "CARD",
				"amount_cents", paid,
				"status", "RECEIVED",
				"received_at", received,
				"branch_id", f.BranchID,
				"gateway_ref", "sandbox-"+demoID("contractor-gateway:" + iv.Key)[:8],
				"processor", "sandbox",
				"payment_token", "tok_sandbox_demo_card",
				"org_id", driverOrgStamp(),
				"metadata", demoMetadata,
				"created_at", received, "updated_at", received,
			))
			add(insert("payment_allocations", "(payment_id, invoice_id)",
				"payment_id", pid, "invoice_id", iid,
				"amount_cents", paid,
				"org_id", driverOrgStamp(),
				"created_at", received,
			))
		}
	}

	// -- quotes ----------------------------------------------------------------------------------
	for _, q := range contractorQuotes {
		m := b.contractorMoney(q.Lines)
		created := b.agoDays(q.AgeDays)
		qid := demoID("contractor-quote:" + q.Key)
		add(insert("quotes", "(id)",
			"id", qid,
			"quote_no", b.contractorNo("CQ", q.Key),
			"customer_id", cust,
			"branch_id", f.BranchID,
			"status", q.Status,
			"valid_until", b.day(q.Valid),
			"subtotal_cents", m.Subtotal, "tax_cents", m.HTax, "total_cents", m.Subtotal+m.HTax,
			"customer_notes", q.Notes,
			"contact_name", "Project manager",
			"project_job", contractorProjects[q.Job].Name,
			"fulfillment", "DELIVERY",
			"delivery_address", contractorJobAddress[q.Job],
			"org_id", driverOrgStamp(),
			"metadata", demoMetadata,
			"created_at", created, "updated_at", created,
		))
		b.sum.ContractorLines = append(b.sum.ContractorLines,
			fmt.Sprintf("quote    %s  %-8s  %d line(s), total %d cents", b.contractorNo("CQ", q.Key), q.Status,
				len(q.Lines), m.Subtotal+m.HTax))
		for i, l := range q.Lines {
			p := b.prod[l.Prod]
			qty := fmt.Sprintf("%d.0000", l.Qty)
			add(insert("quote_lines", "(id)",
				"id", demoID(fmt.Sprintf("contractor-quoteline:%s:%d", q.Key, i+1)),
				"quote_id", qid, "line_no", i+1,
				"product_id", demoID("product:"+l.Prod), "description", p.Name,
				"qty_entered", qty, "uom_entered", p.UOM, "qty_base", qty,
				"unit_price_cents", m.Unit[i], "price_uom", p.UOM,
				"line_total_cents", m.Total[i], "pre_discount_total_cents", m.Total[i],
				"org_id", driverOrgStamp(),
				"metadata", demoMetadata,
				"created_at", created, "updated_at", created,
			))
		}
	}

	// -- procurement projects --------------------------------------------------------------------
	buyer := f.Users["buyer"]
	for _, p := range contractorProjects {
		created := b.agoDays(p.Age)
		pid := demoID("contractor-project:" + p.Key)
		var by any
		if buyer != "" {
			by = buyer
		}
		add(insert("procurement_projects", "(id)",
			"id", pid, "customer_id", cust, "name", p.Name, "status", p.Status,
			"created_by_portal_user_id", by,
			"tenant_id", cust,
			"created_at", created, "updated_at", created,
		))
		var priced int64
		for i, l := range p.Lines {
			pr := b.prod[l.Prod]
			var unit, total any
			if p.Priced {
				u := b.contractorUnit(l.Prod)
				unit, total = u, u*int64(l.Qty)
				priced += u * int64(l.Qty)
			}
			add(insert("procurement_project_lines", "(id)",
				"id", demoID(fmt.Sprintf("contractor-projectline:%s:%d", p.Key, i+1)),
				"project_id", pid, "line_no", i+1,
				"product_id", demoID("product:"+l.Prod), "description", pr.Name,
				"qty_entered", fmt.Sprintf("%d.0000", l.Qty), "uom", pr.UOM,
				"indicative_unit_price_cents", unit, "indicative_line_total_cents", total,
				"tenant_id", cust,
				"created_at", created,
			))
		}
		b.sum.ContractorLines = append(b.sum.ContractorLines,
			fmt.Sprintf("project  %-26s  %-8s  %d line(s), indicative %d cents", p.Name, p.Status, len(p.Lines), priced))
	}

	// -- saved payment methods: sandbox tokens only ----------------------------------------------
	admin := f.Users["account_admin"]
	for _, m := range contractorMethods {
		var by, expMonth, expYear any
		if admin != "" {
			by = admin
		}
		if m.ExpMonth != 0 {
			expMonth, expYear = m.ExpMonth, b.in.Now.Year()+m.ExpYearsOut
		}
		add(insert("portal_payment_methods", "(id)",
			"id", demoID("contractor-method:"+m.Key),
			"customer_id", cust,
			"created_by_portal_user_id", by,
			"gateway", "stub",
			"gateway_token", m.Token,
			"method_type", m.Type, "brand", m.Brand, "last4", m.Last4,
			"exp_month", expMonth, "exp_year", expYear,
			"is_default", m.Default, "status", "active",
			"tenant_id", cust,
			"metadata", demoMetadata,
			"created_at", b.agoDays(60), "updated_at", b.agoDays(60),
		))
		b.sum.ContractorLines = append(b.sum.ContractorLines,
			fmt.Sprintf("method   %-5s  %s ending %s (sandbox token)", m.Key, m.Brand, m.Last4))
	}
}
