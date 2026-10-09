// build.go, turns the tables in plan.go into the ordered INSERT list.
//
// ORDER IS FOREIGN-KEY ORDER, not narrative order. Customers before orders, orders before stops,
// signatures before the stops that name them. The applier walks the slice once, so anything that
// references something else has to come after it.
//
// EVERY TIMESTAMP COMES FROM in.Now. There is no time.Now() in this file, and there must never be
// one: a bare wall-clock read is exactly the thing that would make two runs on the same injected
// clock produce different rows and quietly break the idempotency guarantee that lets this command
// point at staging at all.
// demoseed_build carries the pin's cmd/demoseed/build.go (the fold:
// one-shot subcommand of cmd/core, plan and apply modes preserved).
package main

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

const hstBps = 1300 // 13% HST, the rate the demo world prices at

// taxOn is the money rule, done in integer cents with half-up rounding. The server is the
// arithmetic authority everywhere else in this system; here the seed is writing the rows the server
// would have written, so it uses the same integer discipline rather than floats.
func taxOn(subtotalCents int64) int64 {
	return (subtotalCents*int64(hstBps) + 5000) / 10000
}

type builder struct {
	in   planInput
	rows []row
	sum  planSummary

	prod   map[string]prodSpec
	cust   map[string]custSpec
	orders map[string]orderSpec
	invs   map[string]invSpec

	// dayKeyed dedupes the keys reported in planSummary.DayKeys. dayID is called once for the row's
	// own id and again wherever a child row references it, and reporting the same key three times
	// would make the operator's "how much does a deploy add" line wrong in the alarming direction.
	dayKeyed map[string]bool
}

func (b *builder) add(r row) { b.rows = append(b.rows, r) }

// THE HOUSE ZONE IS ASKED, NOT DUPLICATED. This command once kept its own copy of the zone
// constant; the shared houseday helper (the platform package the house settled on for business
// dates) replaced the copies, and the seed asks it here: daily means the dealer's day, and a UTC
// midnight rolls the day over at dinner time in Ontario, which for the day-keyed rows is the
// difference between stamping tonight's sale tonight and stamping it tomorrow. main.go embeds
// time/tzdata, so the helper's UTC fallback is the insurance, never the plan.
//
// day returns midnight-relative calendar date `off` days from the clock's day IN THE HOUSE ZONE,
// as a DATE literal. The clock itself stays UTC everywhere else; only the CALENDAR the day is read
// on is the dealer's, the "daily means the dealer's day" invariant every other daily read in the
// system already obeys (the sales.today window, the rollup day, the ledger's period, the tax
// assessor's today).
func (b *builder) day(off int) time.Time {
	y, m, d := b.in.Now.In(houseday.Location()).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).AddDate(0, 0, off)
}

// dayStamp is the plan's calendar day as the segment that goes INTO a day-keyed id: 20260903. It is
// the HOUSE ZONE's day (b.day(0) above) and not a second format of b.in.Now, so a day-keyed row's
// key and the scheduled_date/created_at on that same row can never disagree about which day it is,
// and the counter till's acceptance run, which reads the stamp off its own clock in the house zone,
// finds the sale the seed wrote under the day it is still busy being.
func (b *builder) dayStamp() string { return b.day(0).Format("20060102") }

// dayID is demoID for a row that MUST be re-minted on each new calendar day rather than recognised.
// ids.go argues the whole case; §15 of plan.go says which rows get it and why nothing else does.
// Every call is recorded, so `demoseed -plan` can print the exact list and the plan tests can hold
// every other id still while the clock moves.
func (b *builder) dayID(key string) string {
	k := dayKey(b.dayStamp(), key)
	if !b.dayKeyed[k] {
		b.dayKeyed[k] = true
		b.sum.DayKeys = append(b.sum.DayKeys, k)
	}
	return demoID(k)
}

// ago returns a timestamp `h` hours before the clock.
func (b *builder) ago(h int) time.Time { return b.in.Now.UTC().Add(-time.Duration(h) * time.Hour) }

// agoDays returns a timestamp `d` days before the clock, at the clock's time of day.
func (b *builder) agoDays(d int) time.Time { return b.in.Now.UTC().AddDate(0, 0, -d) }

func (b *builder) branchID(i int) string {
	if i < len(b.in.Branches) {
		return b.in.Branches[i].ID
	}
	return b.in.Branches[0].ID // a single-branch deployment folds branch 1 onto branch 0
}

func (b *builder) branchCode(i int) string {
	if i < len(b.in.Branches) {
		return b.in.Branches[i].Code
	}
	return b.in.Branches[0].Code
}

// unitPrice resolves what this customer pays for this product, from the demo rules only.
//
// It applies the CUSTOMER rule and STOPS. It deliberately does NOT also apply the quantity break,
// because the precedence between a customer rule and a volume break belongs to internal/pricing and
// is not this command's to assert: a seeded line price that disagreed with what the counter resolves
// live would be a bug planted in the demo data. The breaks are seeded as CONFIGURATION whose
// thresholds the seeded quantities cross; the counter resolves them when a clerk re-enters the qty.
func (b *builder) unitPrice(custKey, prodKey string) int64 {
	p := b.prod[prodKey]
	price := p.ListCents
	for _, r := range demoRules {
		if r.Cust != custKey {
			continue
		}
		switch {
		case r.Scope == "CUSTOMER_PRODUCT" && r.Product == prodKey:
			return r.Cents
		case r.Scope == "CUSTOMER_CATEGORY" && r.Category == p.Category:
			price = price - (price*int64(r.Bps)+5000)/10000
		}
	}
	return price
}

// -- the build -------------------------------------------------------------------------------------

func buildPlan(in planInput) ([]row, planSummary, error) {
	if len(in.Branches) == 0 {
		return nil, planSummary{}, fmt.Errorf("demoseed: no BRANCH locations found, the seed attaches to the branches that already exist and never invents one")
	}
	b := &builder{
		in: in,
		sum: planSummary{
			ByTable: map[string]int{}, Orders: map[string]int{}, Aging: map[string]int{},
			Stops: map[string]int{}, Tasks: map[string]int{},
		},
		prod:     map[string]prodSpec{},
		cust:     map[string]custSpec{},
		orders:   map[string]orderSpec{},
		invs:     map[string]invSpec{},
		dayKeyed: map[string]bool{},
	}
	for _, p := range demoProducts {
		b.prod[p.Key] = p
	}
	for _, c := range demoCustomers {
		b.cust[c.Key] = c
	}
	for _, o := range demoOrders {
		b.orders[o.Key] = o
	}
	for _, i := range demoInvoices {
		b.invs[i.Key] = i
	}

	b.customers()
	b.products()
	b.pricing()
	b.stock()
	b.orderBook()
	b.invoices()
	b.counterSales()
	// Item 9.9: two deposit sales a tester can walk, after the counter sales whose trio they follow.
	b.depositSales()
	// Item 64: the receivables world's bounced payment, after the invoices it was tendered against.
	b.bouncedPayments()
	b.fleet()
	b.runs()
	if err := b.stops(); err != nil {
		return nil, planSummary{}, err
	}
	b.scanTrail()
	if err := b.willCall(); err != nil {
		return nil, planSummary{}, err
	}
	b.picking()
	b.parkedTickets()
	b.freightApprovals()
	// The quote world, after the customers and products it prices and before the day-keyed rows.
	b.quoteWorld()
	// The CP-03 island's missing lines, beside the quote world because they are documents too, and
	// after the products their foreign keys name (demoseed_island.go argues the whole case).
	b.islandLines()
	// LAST, AND THE ORDER IS THE FOREIGN KEY'S. §15's rows reference the products and customers above
	// them and nothing references them back, so they sit at the end where a reader looking for "what
	// grows every deploy" finds the whole of it in one place.
	b.today()

	b.sum.Rows = len(b.rows)
	for _, r := range b.rows {
		b.sum.ByTable[r.table]++
	}
	b.sum.FirstDate = b.day(-152).Format("2006-01-02")
	b.sum.LastDate = b.day(3).Format("2006-01-02")
	return b.rows, b.sum, nil
}

// -- §2 customers ---------------------------------------------------------------------------------

func (b *builder) customers() {
	for _, c := range demoCustomers {
		b.add(insert("customers", "(id)",
			"id", demoID("customer:"+c.Key),
			"account_no", demoPrefix+c.Key,
			"name", c.Name,
			"customer_type", c.Type,
			"home_branch_id", b.branchID(c.Branch),
			"payment_terms_days", c.TermsDays,
			"credit_limit_cents", c.CreditCents,
			"on_hold", c.OnHold,
			"metadata", demoMetadata,
			"created_at", b.agoDays(180), "updated_at", b.agoDays(180),
		))
	}
	// The one deliberately-held account gets the hold row that explains WHY it is held. A customer
	// row with on_hold = true and no credit_holds row is a hold nobody can lift or account for.
	b.add(insert("credit_holds", "(id)",
		"id", demoID("hold:C09"),
		"customer_id", demoID("customer:C09"),
		"status", "ACTIVE",
		"hold_kind", "MANUAL",
		"auto_placed", false,
		"reason", "Demo account, held so the counter's credit refusal has something to refuse",
		"created_at", b.agoDays(30),
	))
}

// -- §3 products, stock --------------------------------------------------------------------------

func (b *builder) products() {
	for _, p := range demoProducts {
		var catID any
		if id, ok := b.in.Categories[p.Category]; ok {
			catID = id
		}
		var skid any
		if p.SkidQty > 0 {
			skid = p.SkidQty
		}
		b.add(insert("products", "(id)",
			"id", demoID("product:"+p.Key),
			"sku", p.SKU,
			"name", p.Name,
			"description", "Seeded demo catalogue item.",
			"category_id", catID,
			"application", p.Application,
			"base_uom", p.UOM,
			"price_uom", p.UOM,
			"weight_per_unit_kg", p.WeightKg,
			"full_skid_quantity", skid,
			"is_active", true,
			"metadata", demoMetadata,
			"created_at", b.agoDays(180), "updated_at", b.agoDays(180),
		))
		// The base UOM row is what every conversion hangs off; ck_base_factor_is_one forces the 1.
		b.add(insert("product_uoms", "(product_id, uom_code)",
			"product_id", demoID("product:"+p.Key),
			"uom_code", p.UOM, "qty_in_base", "1", "is_base", true,
			"created_at", b.agoDays(180), "updated_at", b.agoDays(180),
		))
		if p.SkidQty > 0 {
			b.add(insert("product_uoms", "(product_id, uom_code)",
				"product_id", demoID("product:"+p.Key),
				"uom_code", "PLT", "qty_in_base", fmt.Sprintf("%d", p.SkidQty), "is_base", false,
				"created_at", b.agoDays(180), "updated_at", b.agoDays(180),
			))
		}
	}
}

// onHand is each demo product's opening balance per branch, as NUMERIC literals in the base UOM.
// The three marked below sit UNDER the reorder point seeded in the same loop, which is what puts a
// real number on the Low-stock tile instead of a zero state.
var onHand = map[string][2]string{
	"P01": {"9800.0000", "4200.0000"},
	"P02": {"5200.0000", "2600.0000"},
	"P03": {"3100.0000", "1450.0000"},
	"P04": {"1400.0000", "620.0000"},
	"P05": {"46.0000", "9.0000"}, // low at branch 1
	"P06": {"280.0000", "160.0000"},
	"P07": {"340.0000", "22.0000"}, // low at branch 1, the procurement gap's product
	"P08": {"210.0000", "130.0000"},
	"P09": {"38.0000", "74.0000"}, // low at branch 0
	"P10": {"640.0000", "310.0000"},
}

// reorderPoint is the level below which purchasing should be buying. Set ABOVE the on-hand for the
// three products above.
var reorderPoint = map[string][2]string{
	"P01": {"2000.0000", "1200.0000"},
	"P04": {"400.0000", "200.0000"},
	"P05": {"12.0000", "24.0000"},
	"P06": {"80.0000", "60.0000"},
	"P07": {"100.0000", "60.0000"},
	"P09": {"60.0000", "40.0000"},
}

func (b *builder) stock() {
	for _, p := range demoProducts {
		for i := 0; i < 2 && i < len(b.in.Branches); i++ {
			qty := onHand[p.Key][i]
			b.add(insert("stock_balances", "(product_id, location_id)",
				"product_id", demoID("product:"+p.Key),
				"location_id", b.branchID(i),
				"branch_id", b.branchID(i),
				"on_hand_base", qty,
				"allocated_base", "0",
				"updated_at", b.agoDays(180),
			))
			// The ledger entry the balance came from. A balance with no move behind it is the exact
			// discrepancy the inventory recon surface exists to shout about.
			b.add(insert("stock_moves", "(id)",
				"id", demoID("move:"+p.Key+":"+b.branchCode(i)),
				"product_id", demoID("product:"+p.Key),
				"to_location_id", b.branchID(i),
				"qty_base", qty, "qty_entered", qty, "uom_entered", p.UOM,
				"move_type", "ADJUST",
				"reason", "demoseed opening balance",
				"branch_id", b.branchID(i),
				"metadata", demoMetadata,
				"occurred_at", b.agoDays(180), "created_at", b.agoDays(180),
			))
			// §16 · what this product costs at this branch. The arbiter is the NATURAL key, not a
			// surrogate: (product_id, branch_id) is the table's whole primary key (0005), and a
			// weighted average somebody has since maintained by hand is the row that must survive.
			// Without this the two cost-class measures read a missing row as a cost of zero, see
			// §16's header for the measurement.
			if c, ok := branchCost[p.Key]; ok {
				b.add(insert("product_branch_costs", "(product_id, branch_id)",
					"product_id", demoID("product:"+p.Key),
					"branch_id", b.branchID(i),
					"avg_unit_cost_cents", c[i],
					"updated_at", b.agoDays(180),
				))
			}
			if rp, ok := reorderPoint[p.Key]; ok {
				b.add(insert("product_reorder_points", "(product_id, branch_id)",
					"product_id", demoID("product:"+p.Key),
					"branch_id", b.branchID(i),
					"reorder_point_base", rp[i],
					"reorder_qty_base", rp[i],
					"lead_time_days", 7,
					"is_active", true,
					"notes", "Seeded demo reorder point.",
					"created_at", b.agoDays(180), "updated_at", b.agoDays(180),
				))
			}
		}
	}
}

// -- §4 pricing -----------------------------------------------------------------------------------

func (b *builder) pricing() {
	for _, p := range demoProducts {
		b.add(insert("pricing_list_prices", "(product_id)",
			"product_id", demoID("product:"+p.Key),
			"list_price_cents", p.ListCents,
			"price_uom", p.UOM,
			"created_at", b.agoDays(180), "updated_at", b.agoDays(180),
		))
	}
	for _, r := range demoRules {
		cols := []any{
			"id", demoID("rule:" + r.Key),
			"scope", r.Scope,
			"customer_id", demoID("customer:" + r.Cust),
			"calc_type", r.Calc,
			"price_uom", "EA",
			"priority", 100,
			"detail_text", r.Detail,
			"is_active", true,
			"created_at", b.agoDays(120), "updated_at", b.agoDays(120),
		}
		switch r.Scope {
		case "CUSTOMER_CATEGORY":
			cols = append(cols, "category_id", b.in.Categories[r.Category], "discount_bps", r.Bps)
		case "CUSTOMER_PRODUCT":
			p := b.prod[r.Product]
			cols = append(cols, "product_id", demoID("product:"+r.Product), "price_cents", r.Cents)
			cols[9] = p.UOM // price_uom follows the product it prices
		}
		b.add(insert("pricing_rules", "(id)", cols...))
	}
	for _, q := range demoBreaks {
		p := b.prod[q.Product]
		b.add(insert("quantity_break_headers", "(id)",
			"id", demoID("break:"+q.Key),
			"code", demoPrefix+q.Key,
			"name", q.Name,
			"product_id", demoID("product:"+q.Product),
			"threshold_uom", q.UOM,
			"is_active", true,
			"created_at", b.agoDays(120), "updated_at", b.agoDays(120),
		))
		for i, t := range q.Tiers {
			var to any
			if t.To != "" {
				to = t.To
			}
			b.add(insert("quantity_break_items", "(id)",
				"id", demoID(fmt.Sprintf("break:%s:%d", q.Key, i)),
				"header_id", demoID("break:"+q.Key),
				"from_qty", t.From, "to_qty", to,
				// PCT_OFF, not PCT_OFF_LIST: 0023 shipped the latter and a later migration renamed
				// the item-level enum (and price_cents → discount_cents). The live CHECK is the
				// authority, not the migration that first created the table.
				"value_type", "PCT_OFF",
				"discount_bps", t.Bps,
				"price_uom", p.UOM,
				"created_at", b.agoDays(120),
			))
		}
	}
}

// -- §5 orders ------------------------------------------------------------------------------------

// orderNo mints a DEMO-prefixed order number. It does NOT call next_order_no: see the ruling in
// plan.go's header, bumping a shared sequence on a live staging database is a mutation.
func (b *builder) orderNo(branch int, key string) string {
	return fmt.Sprintf("%s%s-%s", demoPrefix, b.branchCode(branch), key)
}

// orderSubtotal prices an order's lines without emitting anything, so the order header can be
// written BEFORE the lines that reference it, foreign-key order, not narrative order.
func (b *builder) orderSubtotal(o orderSpec) int64 {
	var subtotal int64
	for _, l := range o.Lines {
		subtotal += int64(l.QtyF * float64(b.unitPrice(o.Cust, l.Prod)))
	}
	return subtotal
}

func (b *builder) orderLines(key string, o orderSpec) {
	created := b.agoDays(o.AgeDays)
	for i, l := range o.Lines {
		p := b.prod[l.Prod]
		unit := b.unitPrice(o.Cust, l.Prod)
		total := int64(l.QtyF * float64(unit))
		// fulfilled_base is the honest half of the status: a FULFILLED order has its lines fulfilled,
		// a PARTIALLY_FULFILLED order has exactly one line half-picked (which is what "partially"
		// means on the floor), and everything upstream of picking has nothing fulfilled at all.
		fulfilled := "0"
		switch {
		case o.Status == "FULFILLED" || o.Status == "INVOICED" || o.Status == "CLOSED":
			fulfilled = l.Qty
		case o.Status == "PARTIALLY_FULFILLED" && i == 0:
			fulfilled = fmt.Sprintf("%.4f", l.QtyF/2)
		}
		b.add(insert("order_lines", "(id)",
			"id", demoID(fmt.Sprintf("orderline:%s:%d", key, i+1)),
			"order_id", demoID("order:"+key),
			"line_no", i+1,
			"product_id", demoID("product:"+l.Prod),
			"description", p.Name,
			"qty_entered", l.Qty, "uom_entered", p.UOM, "qty_base", l.Qty,
			"fulfilled_base", fulfilled,
			"unit_price_cents", unit, "price_uom", p.UOM,
			"line_total_cents", total,
			"metadata", demoMetadata,
			"created_at", created, "updated_at", created,
		))
	}
}

func (b *builder) orderBook() {
	for _, o := range demoOrders {
		created := b.agoDays(o.AgeDays)
		subtotal := b.orderSubtotal(o)
		tax := taxOn(subtotal)
		b.add(insert("orders", "(id)",
			"id", demoID("order:"+o.Key),
			"order_no", b.orderNo(o.Branch, o.Key),
			"customer_id", demoID("customer:"+o.Cust),
			"branch_id", b.branchID(o.Branch),
			"status", o.Status,
			"fulfillment", o.Fulfillment,
			"requested_date", b.day(o.ReqOffset),
			"subtotal_cents", subtotal, "tax_cents", tax, "total_cents", subtotal+tax,
			"metadata", demoMetadata,
			"created_at", created, "updated_at", created,
		))
		b.orderLines(o.Key, o)
		b.sum.Orders[o.Status]++
	}
}

// -- §6 invoices and the money that settled them --------------------------------------------------

func (b *builder) invoices() {
	for _, s := range demoInvoices {
		p := b.prod[s.Prod]
		tax := taxOn(s.Subtotal)
		total := s.Subtotal + tax
		paid := s.Paid
		if paid < 0 {
			paid = total
		}
		balance := total - paid
		status := "FINALIZED"
		switch {
		case balance <= 0:
			status, balance = "PAID", 0
		case paid > 0:
			status = "PARTIALLY_PAID"
		}
		issued := b.agoDays(s.IssueAge)
		var orderID any
		if s.Order != "" {
			orderID = demoID("order:" + s.Order)
		}
		b.add(insert("invoices", "(id)",
			"id", demoID("invoice:"+s.Key),
			"invoice_no", demoPrefix+"INV-"+s.Key,
			"customer_id", demoID("customer:"+s.Cust),
			"order_id", orderID,
			"branch_id", b.branchID(s.Branch),
			"status", status,
			"issue_date", b.agoDays(s.IssueAge),
			"due_date", b.day(s.DueOffset),
			"subtotal_cents", s.Subtotal, "tax_cents", tax, "total_cents", total,
			"balance_cents", balance,
			"finalized_at", issued,
			"metadata", demoMetadata,
			"created_at", issued, "updated_at", issued,
		))
		unit := s.Subtotal
		if s.QtyF > 0 {
			unit = int64(float64(s.Subtotal) / s.QtyF)
		}
		b.add(insert("invoice_lines", "(id)",
			"id", demoID("invoiceline:"+s.Key),
			"invoice_id", demoID("invoice:"+s.Key),
			"line_no", 1,
			"product_id", demoID("product:"+s.Prod),
			"description", p.Name,
			"qty_entered", s.Qty, "uom_entered", p.UOM, "qty_base", s.Qty,
			"unit_price_cents", unit, "price_uom", p.UOM,
			"line_total_cents", s.Subtotal,
			"created_at", issued,
		))
		// A PARTIALLY_PAID or PAID invoice with no payment behind it is a balance nobody can
		// explain. The payment and its allocation are seeded so the AR detail reconciles to the
		// same cent the tile totals.
		if paid > 0 {
			received := b.agoDays(s.PaidAge)
			b.add(insert("payments", "(id)",
				"id", demoID("payment:"+s.Key),
				"payment_no", demoPrefix+"PAY-"+s.Key,
				"customer_id", demoID("customer:"+s.Cust),
				"method", "CHEQUE",
				"amount_cents", paid,
				"status", "RECEIVED",
				"received_at", received,
				"branch_id", b.branchID(s.Branch),
				"metadata", demoMetadata,
				"created_at", received, "updated_at", received,
			))
			b.add(insert("payment_allocations", "(payment_id, invoice_id)",
				"payment_id", demoID("payment:"+s.Key),
				"invoice_id", demoID("invoice:"+s.Key),
				"amount_cents", paid,
				"created_at", received,
			))
		}
		switch {
		case status == "PAID":
			b.sum.Aging["settled"]++
		case s.DueOffset >= 0:
			b.sum.Aging["current"]++
		case s.DueOffset >= -30:
			b.sum.Aging["1-30"]++
		case s.DueOffset >= -60:
			b.sum.Aging["31-60"]++
		default:
			b.sum.Aging["60+"]++
		}
	}
}

// -- §7 counter sales -----------------------------------------------------------------------------

func (b *builder) counterSales() {
	for _, s := range demoSales {
		// The stable half of §7: ten sales written once, ever, under demoID and their own S-keys.
		b.counterSale(s, demoID, s.Key)
	}
}

// counterSale writes the row trio a counter sale leaves behind, order + line, invoice + line,
// pos_sale, and the payment that settled it unless it went on account.
//
// IT TAKES ITS ID MINTER AND ITS NUMBER SUFFIX AS ARGUMENTS, and that is the only difference between
// §7's history and §15's today. counterSales passes demoID and the sale's own key, so its ten sales
// are stable-keyed and their ids are byte-for-byte what they were before this parameter existed.
// today() passes b.dayID and a key carrying the plan date, so its two sales are re-minted each day.
// Writing today's sale as a second copy of this body would have been the alternative, and a second
// copy is where the two would have drifted, one of them learning about a new tender, a rounding
// rule or a column, and the demo world quietly disagreeing with itself about what a counter sale is.
func (b *builder) counterSale(s saleSpec, mint func(string) string, no string) {
	{
		p := b.prod[s.Prod]
		unit := b.unitPrice(s.Cust, s.Prod)
		subtotal := int64(s.QtyF * float64(unit))
		tax := taxOn(subtotal)
		total := subtotal + tax
		at := b.agoDays(s.AgeDay)
		if s.AgeDay > 0 {
			at = b.day(-s.AgeDay).Add(14 * time.Hour) // a plausible mid-afternoon, on that date
		}
		orderKey, invKey := "sale-order:"+s.Key, "sale-invoice:"+s.Key
		b.add(insert("orders", "(id)",
			"id", mint(orderKey),
			"order_no", fmt.Sprintf("%s%s-%s", demoPrefix, b.branchCode(s.Branch), no),
			"customer_id", demoID("customer:"+s.Cust),
			"branch_id", b.branchID(s.Branch),
			"status", "CLOSED", "fulfillment", "PICKUP",
			"requested_date", b.day(-s.AgeDay),
			"subtotal_cents", subtotal, "tax_cents", tax, "total_cents", total,
			"metadata", demoMetadata,
			"created_at", at, "updated_at", at,
		))
		b.add(insert("order_lines", "(id)",
			"id", mint(orderKey+":1"),
			"order_id", mint(orderKey), "line_no", 1,
			"product_id", demoID("product:"+s.Prod), "description", p.Name,
			"qty_entered", s.Qty, "uom_entered", p.UOM, "qty_base", s.Qty,
			"fulfilled_base", s.Qty,
			"unit_price_cents", unit, "price_uom", p.UOM, "line_total_cents", subtotal,
			"metadata", demoMetadata,
			"created_at", at, "updated_at", at,
		))
		// A counter sale settles at the till EXCEPT when it is put on account, and the invoice has to
		// say which happened. A CASH/CARD/CHEQUE sale is PAID and carries the payment row that paid
		// it; an ACCOUNT sale is FINALIZED with the whole balance outstanding and flows into AR like
		// any other credit sale. Marking every counter sale PAID would have been simpler and would
		// have planted an invoice with no money behind it, a balance nobody can explain, which is
		// exactly what the AR surfaces exist to make impossible.
		onAccount := s.Tender == "ACCOUNT"
		status, balance := "PAID", int64(0)
		if onAccount {
			status, balance = "FINALIZED", total
		}
		due := b.day(-s.AgeDay)
		if onAccount {
			due = b.day(-s.AgeDay + b.cust[s.Cust].TermsDays)
		}
		b.add(insert("invoices", "(id)",
			"id", mint(invKey),
			"invoice_no", demoPrefix+"INV-"+no,
			"customer_id", demoID("customer:"+s.Cust),
			"order_id", mint(orderKey),
			"branch_id", b.branchID(s.Branch),
			"status", status,
			"issue_date", b.day(-s.AgeDay), "due_date", due,
			"subtotal_cents", subtotal, "tax_cents", tax, "total_cents", total,
			"balance_cents", balance, "finalized_at", at,
			"metadata", demoMetadata,
			"created_at", at, "updated_at", at,
		))
		b.add(insert("invoice_lines", "(id)",
			"id", mint(invKey+":1"),
			"invoice_id", mint(invKey), "line_no", 1,
			"product_id", demoID("product:"+s.Prod), "description", p.Name,
			"qty_entered", s.Qty, "uom_entered", p.UOM, "qty_base", s.Qty,
			"unit_price_cents", unit, "price_uom", p.UOM, "line_total_cents", subtotal,
			"created_at", at,
		))
		saleCols := []any{
			"id", mint("possale:" + s.Key),
			"idempotency_key", mint("possale-idem:" + s.Key),
			"order_id", mint(orderKey),
			"invoice_id", mint(invKey),
			"branch_id", b.branchID(s.Branch),
			"sale_type", "COLLECT_NOW",
			"cash_sale", s.Tender == "CASH",
			"tender", s.Tender,
			"status", "COMMITTED",
			"created_at", at,
		}
		if !onAccount {
			payKey := "sale-payment:" + s.Key
			b.add(insert("payments", "(id)",
				"id", mint(payKey),
				"payment_no", demoPrefix+"PAY-"+no,
				"customer_id", demoID("customer:"+s.Cust),
				"method", s.Tender,
				"amount_cents", total,
				"status", "RECEIVED",
				"received_at", at,
				"branch_id", b.branchID(s.Branch),
				"metadata", demoMetadata,
				"created_at", at, "updated_at", at,
			))
			b.add(insert("payment_allocations", "(payment_id, invoice_id)",
				"payment_id", mint(payKey),
				"invoice_id", mint(invKey),
				"amount_cents", total,
				"created_at", at,
			))
			saleCols = append(saleCols, "payment_id", mint(payKey))
		}
		b.add(insert("pos_sale", "(id)", saleCols...))
	}
}

// -- §8 fleet -------------------------------------------------------------------------------------

func (b *builder) fleet() {
	for _, t := range demoTrucks {
		b.add(insert("dispatch_trucks", "(id)",
			"id", demoID("truck:"+t.Key),
			"branch_id", b.branchID(t.Branch),
			"code", t.Code, "name", t.Name, "truck_type", t.Type,
			"capacity_kg", t.CapacityKg, "is_active", true,
			"created_at", b.agoDays(150), "updated_at", b.agoDays(150),
		))
	}
	for _, d := range demoDrivers {
		cols := []any{
			"id", demoID("driver:" + d.Key),
			"branch_id", b.branchID(d.Branch),
			"name", d.Name, "phone", d.Phone, "licence_class", d.Licence,
			"is_active", true,
			"created_at", b.agoDays(150), "updated_at", b.agoDays(150),
		}
		// THE EDGE, WRITTEN AT BIRTH BECAUSE IT CANNOT BE WRITTEN LATER. user_id is what
		// DriverForUser reads (dispatch/repository.go:177: `WHERE user_id = $1 AND is_active`), and
		// it is the difference between a driver persona whose phone shows their day and one whose
		// phone shows "You are not set up as a driver at this branch." apply.go has no UPDATE path
		// by design, so the only moment this command can ever set it is the moment the row is first
		// inserted, which is why this is here and why migration 0206 exists for every database
		// where that moment has already passed.
		//
		// The column is OMITTED, not set to NULL, when the persona did not resolve. A database
		// short of 0200 has no such account, and a dispatch_drivers row with no login is the
		// ordinary case (0090:47), so the plan simply builds the row this command has always built.
		if uid := b.in.Personas[d.Persona]; d.Persona != "" && uid != "" {
			cols = append(cols, "user_id", uid)
		}
		b.add(insert("dispatch_drivers", "(id)", cols...))
	}
	// A depot is written ONLY for a branch whose code we have a real coordinate for, and ONLY when
	// that branch has no depot yet (the conflict arbiter is the branch PK). A branch with no depot
	// produces no distance estimate, which 0090 already treats as the normal case, far better than
	// planting a guessed origin that silently skews every manifest.
	depots := map[string][2]float64{
		"KGN": {44.2312, -76.4860},
		"TRT": {44.1001, -77.5766},
	}
	for _, br := range b.in.Branches {
		c, ok := depots[br.Code]
		if !ok {
			continue
		}
		b.add(insert("dispatch_branch_depots", "(branch_id)",
			"branch_id", br.ID, "latitude", c[0], "longitude", c[1],
			"updated_at", b.agoDays(150),
		))
	}
}

// -- §9 runs --------------------------------------------------------------------------------------

func (b *builder) runs() {
	for _, r := range demoRuns {
		var truck, driver, dispatched, completed any
		if r.Truck != "" {
			truck = demoID("truck:" + r.Truck)
		}
		if r.Driver != "" {
			driver = demoID("driver:" + r.Driver)
		}
		if r.Status == "DISPATCHED" || r.Status == "COMPLETED" {
			dispatched = b.day(r.DayOff).Add(7 * time.Hour)
		}
		if r.Status == "COMPLETED" {
			completed = b.day(r.DayOff).Add(15 * time.Hour)
		}
		b.add(insert("delivery_runs", "(id)",
			"id", demoID("run:"+r.Key),
			"run_no", fmt.Sprintf("#%sRUN-%s-%s", demoPrefix, b.branchCode(r.Branch), r.Key),
			"branch_id", b.branchID(r.Branch),
			"scheduled_date", b.day(r.DayOff),
			"truck_id", truck, "driver_id", driver,
			"status", r.Status, "notes", r.Notes,
			"dispatched_at", dispatched, "completed_at", completed,
			"created_at", b.day(r.DayOff).Add(-6*time.Hour),
			"updated_at", b.day(r.DayOff).Add(-6*time.Hour),
		))
	}
	// The reschedule trail. Two real moves, so the history panel is not an empty state on day one.
	b.add(insert("dispatch_reschedules", "(id)",
		"id", demoID("resched:1"),
		"branch_id", b.branchID(0), "entity_type", "delivery_stop",
		"entity_id", demoID("stop:SP11"),
		"from_date", b.day(0), "to_date", b.day(1),
		"reason", "Customer asked for the morning instead", "stops_moved", 1,
		"created_at", b.ago(30),
	))
	b.add(insert("dispatch_reschedules", "(id)",
		"id", demoID("resched:2"),
		"branch_id", b.branchID(1), "entity_type", "delivery_run",
		"entity_id", demoID("run:R7"),
		"from_date", b.day(2), "to_date", b.day(3),
		"reason", "Truck in for a brake inspection", "stops_moved", 2,
		"created_at", b.ago(20),
	))
}

// -- §10 stops, and their proof of delivery -------------------------------------------------------

func (b *builder) stops() error {
	// Fill arithmetic for the summary, so a run's gauge band is reported rather than guessed at.
	weighed := map[string]float64{}
	unweighed := map[string]int{}

	for _, s := range demoStops {
		stopID := demoID("stop:" + s.Key)
		date := b.day(s.DayOff)
		cols := []any{
			"id", stopID,
			"branch_id", b.branchID(s.Branch),
			"order_id", demoID("order:" + s.Order),
			"stop_no", s.StopNo,
			"scheduled_date", date,
			"status", s.Status,
			"time_window", s.Window,
			"contact_name", s.Contact, "contact_phone", s.Phone,
			"address_line", s.Addr, "city", s.City, "postal_code", s.Postal,
			"latitude", s.Lat, "longitude", s.Lng,
			"mto_class", s.MTOClass, "mto_note", s.MTONote,
			"failure_reason", s.Failure,
			"created_at", date.Add(-8 * time.Hour), "updated_at", date.Add(-8 * time.Hour),
		}
		if s.Run != "" {
			cols = append(cols, "run_id", demoID("run:"+s.Run))
		} else {
			// AN UNASSIGNED STOP IS NOT A COMMITTED ONE, and the column's own comment says so: 0185
			// defines COMMITTED as "a dispatcher put it on a truck and stood behind it" and REQUESTED
			// as "a salesperson asked and it is on the board". delivery_stops.commit_state DEFAULTS to
			// COMMITTED, so every stop this seed wrote, including the four with run_id NULL, claimed
			// a dispatcher had stood behind a load that is on no truck. Measured consequence: the load
			// board's Requested/Committed split showed zero requested, and the Needs-you
			// dispatch.due_to_commit source, which counts unassigned-and-not-committed inside the
			// 48-hour prompt, could never count anything at all.
			//
			// It is set on the run-less stops ONLY. A stop that IS on a run keeps the default, which is
			// then true of it.
			cols = append(cols, "commit_state", "REQUESTED")
		}
		if s.WeightKg != "" {
			cols = append(cols, "weight_kg", s.WeightKg)
			var f float64
			fmt.Sscanf(s.WeightKg, "%f", &f)
			if s.Run != "" {
				weighed[s.Run] += f
			}
		} else if s.Run != "" {
			unweighed[s.Run]++
		}
		switch s.Status {
		case "LOADED":
			cols = append(cols, "loaded_at", date.Add(7*time.Hour))
		case "EN_ROUTE":
			cols = append(cols, "loaded_at", date.Add(7*time.Hour), "en_route_at", date.Add(8*time.Hour))
		case "FAILED":
			cols = append(cols, "loaded_at", date.Add(7*time.Hour), "en_route_at", date.Add(8*time.Hour))
		case "DELIVERED":
			// A DELIVERED stop needs REAL evidence. See seal.go: the seed captures a genuine typed
			// attestation, sealed with the same vault primitive the running server uses, and points
			// the stop at it. It does not fabricate a reference to a signature that does not exist -
			// an unevidenced delivery is the precise failure 0090's POD check was written to stop,
			// and planting one in demo data would teach the exact wrong lesson.
			at := date.Add(11 * time.Hour)
			ref, enc, err := b.in.Seal("podsig:"+s.Key, s.Received, "PROOF_OF_DELIVERY", "delivery_stop", stopID, at)
			if err != nil {
				return fmt.Errorf("demoseed: sealing POD for stop %s: %w", s.Key, err)
			}
			b.add(insert("pos_signature", "(id)",
				"id", demoID("podsig:"+s.Key),
				"purpose", "PROOF_OF_DELIVERY", "ref_type", "delivery_stop", "ref_id", stopID,
				"signature_enc", enc, "provider", "TYPED_NAME_V1",
				"captured_at", at,
			))
			cols = append(cols,
				"loaded_at", date.Add(7*time.Hour),
				"en_route_at", date.Add(8*time.Hour),
				"delivered_at", at,
				"pod_signature_ref", ref, "pod_received_by", s.Received, "pod_at", at,
				"handoff_at", at.Add(2*time.Minute),
			)
		}
		b.add(insert("delivery_stops", "(id)", cols...))
		b.sum.Stops[s.Status]++
	}

	caps := map[string]float64{}
	for _, t := range demoTrucks {
		var c float64
		fmt.Sscanf(t.CapacityKg, "%f", &c)
		caps[t.Key] = c
	}
	keys := make([]string, 0, len(demoRuns))
	for _, r := range demoRuns {
		keys = append(keys, r.Key)
	}
	sort.Strings(keys)
	byKey := map[string]runSpec{}
	for _, r := range demoRuns {
		byKey[r.Key] = r
	}
	for _, k := range keys {
		r := byKey[k]
		capKg := caps[r.Truck]
		if capKg == 0 || weighed[k] == 0 {
			continue
		}
		note := ""
		if unweighed[k] > 0 {
			note = fmt.Sprintf(" (+%d not weighed, a partial total can never raise an over-capacity warning)", unweighed[k])
		}
		b.sum.RunFill = append(b.sum.RunFill,
			fmt.Sprintf("%s · %s %.0fkg · loaded %.0fkg · %.0f%%%s", k, r.Truck, capKg, weighed[k], weighed[k]/capKg*100, note))
	}
	return nil
}

// -- §11 the load scan trail and will-call --------------------------------------------------------

func (b *builder) scanTrail() {
	for _, s := range demoScans {
		var stopID any
		if s.Stop != "" {
			stopID = demoID("stop:" + s.Stop)
		}
		b.add(insert("load_scan_checkpoints", "(order_id, load_ref, checkpoint)",
			"id", demoID("scan:"+s.Key),
			"branch_id", b.branchID(s.Branch),
			"order_id", demoID("order:"+s.Order),
			"stop_id", stopID,
			"load_ref", s.LoadRef, "checkpoint", s.Checkpoint,
			"scanned_at", b.ago(s.AgeHours), "note", s.Note,
			"created_at", b.ago(s.AgeHours),
		))
	}
}

func (b *builder) willCall() error {
	for _, w := range demoWillCall {
		raw := demoRef("dwc_", "willcall:"+w.Key)
		cols := []any{
			"id", demoID("willcall:" + w.Key),
			"order_id", demoID("order:" + w.Order),
			"branch_id", b.branchID(w.Branch),
			"token_hash", demoTokenHash(raw),
			"status", w.Status,
			"expires_at", b.agoDays(-w.DaysLeft),
			"created_at", b.agoDays(2),
		}
		switch w.Status {
		case "CONFIRMED":
			at := b.ago(6)
			ref, enc, err := b.in.Seal("wcsig:"+w.Key, w.Received, "PROOF_OF_DELIVERY", "will_call_pickup",
				demoID("willcall:"+w.Key), at)
			if err != nil {
				return fmt.Errorf("demoseed: sealing pickup attestation for %s: %w", w.Key, err)
			}
			b.add(insert("pos_signature", "(id)",
				"id", demoID("wcsig:"+w.Key),
				"purpose", "PROOF_OF_DELIVERY", "ref_type", "will_call_pickup",
				"ref_id", demoID("willcall:"+w.Key),
				"signature_enc", enc, "provider", "TYPED_NAME_V1",
				"captured_at", at,
			))
			cols = append(cols, "signature_ref", ref, "collected_by", w.Received, "confirmed_at", at)
		case "REVOKED":
			cols = append(cols, "revoked_at", b.ago(30))
		}
		b.add(insert("will_call_pickups", "(id)", cols...))
		b.sum.WillCall = append(b.sum.WillCall,
			fmt.Sprintf("%s · order %s · %s · token %s", w.Key, w.Order, w.Status, raw))
	}
	return nil
}

// -- §12 picking ----------------------------------------------------------------------------------

// taskLane is the transition history a task of each status must have behind it. task_events is
// append-only and IS the timeline the pick detail renders, so a task that arrived at STAGED without
// passing through ACCEPTED and IN_PROGRESS would render as a task that teleported.
var taskLane = map[string][]string{
	"QUEUED":      {"QUEUED"},
	"PUSHED":      {"QUEUED", "PUSHED"},
	"ACCEPTED":    {"QUEUED", "PUSHED", "ACCEPTED"},
	"IN_PROGRESS": {"QUEUED", "PUSHED", "ACCEPTED", "IN_PROGRESS"},
	"STAGED":      {"QUEUED", "PUSHED", "ACCEPTED", "IN_PROGRESS", "STAGED"},
	"DONE":        {"QUEUED", "PUSHED", "ACCEPTED", "IN_PROGRESS", "STAGED", "DONE"},
	"CANCELLED":   {"QUEUED", "CANCELLED"},
}

func (b *builder) picking() {
	for _, t := range demoTasks {
		created := b.ago(t.AgeHours)
		cols := []any{
			"id", demoID("task:" + t.Key),
			"task_type", t.Type,
			"order_id", demoID("order:" + t.Order),
			"branch_id", b.branchID(t.Branch),
			"priority", t.Priority,
			"status", t.Status,
			"source", "SYSTEM",
			"created_at", created, "updated_at", created,
		}
		if t.LineNo > 0 {
			cols = append(cols, "order_line_no", t.LineNo)
		}
		if t.Deadline > 0 {
			cols = append(cols, "pushed_at", b.ago(t.AgeHours), "accept_deadline_at", b.ago(t.Deadline))
		}
		claimed := false
		switch t.Status {
		case "ACCEPTED", "IN_PROGRESS":
			cols = append(cols, "claimed_at", b.ago(t.AgeHours-1))
			claimed = true
		case "STAGED":
			cols = append(cols, "claimed_at", b.ago(t.AgeHours-1),
				"staged_at", b.ago(t.AgeHours-2), "stage_location", t.Stage, "stage_ref", t.StageRef)
			claimed = true
		case "DONE":
			cols = append(cols, "claimed_at", b.ago(t.AgeHours-1),
				"staged_at", b.ago(t.AgeHours-2), "stage_location", "Bay 2", "stage_ref", demoPrefix+"STG-"+t.Key,
				"done_at", b.ago(t.AgeHours-3))
			claimed = true
		}
		// EVERY claimed_at GETS A claimed_by, AND THE RULE IS THE STATUS-INDEPENDENT ONE ON PURPOSE.
		//
		// 0073:32-33 gave tasks two person columns and this seed wrote neither, so nine of seventeen
		// demo tasks carried a claim stamp with nobody on it, "somebody accepted this at 08:00" with
		// no somebody. That is not a missing nicety, it is a row that contradicts itself, and every
		// surface reading tasks.claimed_by (yard_repository.go:69 → tasks/model.go:108 → the yard
		// console) had an empty string where a name belongs.
		//
		// The condition is "this task carries a claimed_at", not a list of statuses, because the
		// defect is the ORPHANED STAMP and not any particular lane. A future status that also claims
		// gets the person for free; a list would have to be remembered. Migration 0206 repairs
		// already-seeded databases with the identical predicate (`claimed_at IS NOT NULL AND
		// claimed_by IS NULL`) so the two mechanisms cannot disagree about what a claimed task is.
		//
		// ONE PERSON HOLDS ALL OF THEM, and that is a deliberate simplification rather than an
		// oversight: 0200 plants exactly one yard login, and inventing a second picker would mean
		// inventing a second account with a password nobody knows. A demo yard with one busy picker
		// is honest; a demo yard with nine unowned claims is not.
		//
		// assigned_to is deliberately LEFT NULL. 0073:32 calls it the PUSH seam and marks it unused,
		// and nothing in the tree reads it back, a value there would be state no screen can show,
		// which is how demo data starts lying. When the push timer is built it can seed itself.
		if claimed {
			if uid := b.in.Personas[personaYard]; uid != "" {
				cols = append(cols, "claimed_by", uid)
			}
		}
		b.add(insert("tasks", "(id)", cols...))
		b.sum.Tasks[t.Status]++

		lane := taskLane[t.Status]
		for i, to := range lane {
			var from any
			if i > 0 {
				from = lane[i-1]
			}
			b.add(insert("task_events", "(id)",
				"id", demoID(fmt.Sprintf("taskevent:%s:%d", t.Key, i)),
				"task_id", demoID("task:"+t.Key),
				"from_status", from, "to_status", to,
				"note", "",
				"created_at", b.ago(t.AgeHours-i),
			))
		}
	}
	for _, s := range demoShortages {
		p := b.prod[s.Prod]
		cols := []any{
			"id", demoID("shortage:" + s.Key),
			"task_id", demoID("task:" + s.Task),
			"order_line_no", s.LineNo,
			"product_id", demoID("product:" + s.Prod),
			"sku", p.SKU,
			"description", p.Name,
			"qty_short", s.Qty, "uom", s.UOM,
			"note", s.Note,
			"reported_at", b.ago(s.AgeHours),
		}
		if s.Resolution != "" {
			cols = append(cols, "resolved_at", b.ago(s.AgeHours-1), "resolution", s.Resolution)
		}
		b.add(insert("task_shortages", "(id)", cols...))
	}
}

// -- §13 parked tickets ---------------------------------------------------------------------------

func (b *builder) parkedTickets() {
	for _, t := range demoParked {
		// The lines column is the SHAPE pos.ParkedLineInput unmarshals (pos/model.go:659), product
		// id, quantity, unit, and not a projection of an order line. A parked ticket holds no prices:
		// 0043's own comment says they are "re-priced on resume", which is the whole reason parking is
		// safe when a price changes overnight. Writing a price here would plant the one thing the
		// design exists to avoid, and ListParked hands the payload straight to the client.
		lines := fmt.Sprintf(`[{"product_id":%q,"qty_entered":%s,"uom_entered":%q}]`,
			demoID("product:"+t.Prod), strconv.FormatFloat(t.Qty, 'f', -1, 64), t.UOM)
		cols := []any{
			"id", demoID("parked:" + t.Key),
			"ticket_no", t.No,
			"branch_id", b.branchID(t.Branch),
			"sale_type", t.Type,
			"notes", t.Notes,
			"lines", lines,
			"status", "PARKED",
			"created_at", b.ago(3 + 2*len(t.Key)), "updated_at", b.ago(3 + 2*len(t.Key)),
		}
		// A walk-in leaves the column OUT rather than setting it NULL, which is the same posture the
		// driver edge takes for a persona it cannot resolve: the schema's own default is the honest
		// value, and 0043:29 spells that default out, "NULL = walk-in".
		if t.Cust != "" {
			cols = append(cols, "customer_id", demoID("customer:"+t.Cust))
		}
		b.add(insert("pos_parked_tickets", "(id)", cols...))
	}
}

// -- §14 approvals, and the proposals behind them -------------------------------------------------

func (b *builder) freightApprovals() {
	// NO PERSONA, NO REQUEST. permission_approval_requests.requested_by is NOT NULL with a live FK to
	// users (0016), and delivery_freight_raises.requested_by is NOT NULL with ON DELETE RESTRICT
	// (0195), deliberately stricter, because that row is the evidence for a figure that reached an
	// invoice. On a database behind 0200 there is no demo dispatcher, and there is no honest
	// substitute: filing the raise under some other account would put a real person's name on an ask
	// they never made. So this section builds nothing, exactly as the driver edge omits its column.
	requester := b.in.Personas[personaDispatch]
	if requester == "" {
		return
	}
	for _, a := range demoApprovals {
		var (
			approvalID = demoID("approval:" + a.Key)
			raiseID    = demoID("freightraise:" + a.Key)
			at         = b.ago(a.AgeHours)
			orderNo    = b.orderNo(a.Branch, a.Order)
			custName   = b.cust[a.Cust].Name
		)
		// THE APPROVAL FIRST, because delivery_freight_raises.approval_id carries a foreign key onto
		// it and the applier walks this slice once. The reference the other way, subject_ref, is
		// TEXT with no constraint behind it (0020), which is what lets the pair point at each other
		// at all; dispatch has the same problem at runtime and solves it by filing the approval second
		// and stamping the back-reference afterwards. A seed cannot UPDATE, so it computes both ids up
		// front and writes them in FK order instead.
		b.add(insert("permission_approval_requests", "(id)",
			"id", approvalID,
			"requested_by", requester,
			"branch_id", b.branchID(a.Branch),
			"policy_type", "FREIGHT_ABOVE_QUOTE",
			// The detail is dispatch's own sentence, from raiseDetail (dispatch/freightraise.go:162),
			// reproduced to the word. A demo card that read differently from a real one would be
			// teaching people to recognise the wrong thing, and the sentence carries NO NUMBERS on
			// purpose: GET /api/v1/staff/approvals is open to every authenticated staff member and has
			// no money gate, so the figures stay on the raise behind dispatch's own visibility policy.
			"detail", "Freight on "+orderNo+" for "+custName+" is being raised above the delivery the customer was quoted.",
			// subject_ref IS the parked raise's id. That binding is what makes an approve APPLY the
			// split rather than merely stamp a row (0195), and a demo approval with an invented
			// subject_ref would be the exact "approval bound to nothing" the policy is system-only to
			// prevent.
			"subject_ref", raiseID,
			// The marker rides in `data`, which is this table's only JSONB. ListApprovals reads
			// data->>'amount_cents' out of it under a COALESCE, and an object without that key answers
			// zero, which is the right answer here anyway: a freight card shows no magnitude.
			"data", demoMetadata,
			"status", "PENDING",
			"requested_at", at,
		))
		b.add(insert("delivery_freight_raises", "(id)",
			"id", raiseID,
			"order_id", demoID("order:"+a.Order),
			"branch_id", b.branchID(a.Branch),
			// order_no and customer_name are COPIED, not joined, and 0195 argues why at length: an
			// approval is a record of what somebody was asked to agree to, so the subject cannot be
			// allowed to re-derive into a name that was edited afterwards.
			"order_no", orderNo,
			"customer_name", custName,
			// Three figures, not two. `quoted` is what the customer was shown, `current` is where the
			// charge actually stands, and `proposed` is the ask. They are equal on the first two here
			// because nothing has been raised yet, this raise IS the first one, which is the state
			// the gate leaves behind and the one that makes the CHECK (proposed > quoted) meaningful.
			"quoted_freight_cents", a.Quoted,
			"current_freight_cents", a.Quoted,
			"proposed_freight_cents", a.Proposed,
			"loads", a.Loads,
			// amounts_cents is OMITTED, and NULL there is not an empty list: it means "divide it
			// evenly against the loads that exist when this is applied". Materialising the split now
			// would freeze an arithmetic that has to be redone at apply time.
			"requested_by", requester,
			"requested_by_role", a.Role,
			"requested_at", at,
			// PARKED with no decider, which the table's own CHECK pairs: an undecided row must not
			// pretend to have one.
			"state", "PARKED",
			"approval_id", approvalID,
		))
	}
}

// -- §15 the day-keyed rows -----------------------------------------------------------------------

// today writes the handful of rows that carry the plan's DATE in their key, so a deploy tomorrow
// adds tomorrow's beside today's rather than recognising today's and writing nothing. plan.go's §15
// says which rows and why; ids.go says what day-keying costs and why it is confined to this function.
func (b *builder) today() {
	for _, t := range demoTodaySales {
		// A day-keyed sale is the SAME sale §7 writes, same builder, same tender arithmetic, same
		// invoice-versus-payment ruling, reached with a different id minter and a dated number. The
		// AgeDay is 0 because it IS today; b.agoDays(0) is the clock itself, so created_at lands
		// inside the half-open day sales.today asks for.
		b.counterSale(saleSpec{
			Key: t.Key, Branch: t.Branch, Cust: t.Cust, AgeDay: 0,
			Tender: t.Tender, Prod: t.Prod, Qty: t.Qty, QtyF: t.QtyF,
		}, b.dayID, t.Key+"-"+b.dayStamp())
	}
	for _, t := range demoTodayStops {
		p := b.prod[t.Prod]
		unit := b.unitPrice(t.Cust, t.Prod)
		subtotal := int64(t.QtyF * float64(unit))
		tax := taxOn(subtotal)
		orderID := b.dayID("today-order:" + t.Key)
		at := b.in.Now.UTC()
		// The order this delivery is about. One row and one line, the cheapest honest subject for a
		// stop. Pointing at §5's stable order book instead would hang a hundredth stop off order
		// DEMO-KGN-O01 after a hundred deploys, which is a customer who has been delivered the same
		// order every day since August.
		b.add(insert("orders", "(id)",
			"id", orderID,
			"order_no", fmt.Sprintf("%s%s-D%s-%s", demoPrefix, b.branchCode(t.Branch), b.dayStamp(), t.Key),
			"customer_id", demoID("customer:"+t.Cust),
			"branch_id", b.branchID(t.Branch),
			"status", "CONFIRMED", "fulfillment", "DELIVERY",
			"requested_date", b.day(0),
			"subtotal_cents", subtotal, "tax_cents", tax, "total_cents", subtotal+tax,
			"metadata", demoMetadata,
			"created_at", at, "updated_at", at,
		))
		b.add(insert("order_lines", "(id)",
			"id", b.dayID("today-orderline:"+t.Key),
			"order_id", orderID, "line_no", 1,
			"product_id", demoID("product:"+t.Prod), "description", p.Name,
			"qty_entered", t.Qty, "uom_entered", p.UOM, "qty_base", t.Qty,
			"fulfilled_base", "0",
			"unit_price_cents", unit, "price_uom", p.UOM, "line_total_cents", subtotal,
			"metadata", demoMetadata,
			"created_at", at, "updated_at", at,
		))
		// THE STOP, AND THE SHAPE IS THE POINT. run_id is omitted (unassigned), scheduled_date is
		// today, status is READY and commit_state is REQUESTED. Three of the four measurements this
		// section exists for read exactly that row: deliveries.today counts a stop scheduled today
		// that is not DELIVERED or CANCELLED; dispatch.unassigned counts a run-less stop inside a
		// working window that starts today; dispatch.due_to_commit counts a run-less, not-committed
		// stop inside the 48-hour commit prompt, which a stop scheduled for today is by definition.
		//
		// stop_no is 0, the number §10 already gives its unassigned stops, because a stop with no run
		// has no position in a sequence to be.
		b.add(insert("delivery_stops", "(id)",
			"id", b.dayID("today-stop:"+t.Key),
			"branch_id", b.branchID(t.Branch),
			"order_id", orderID,
			"stop_no", 0,
			"scheduled_date", b.day(0),
			"status", "READY",
			"commit_state", "REQUESTED",
			"time_window", "PM",
			"contact_name", t.Contact, "contact_phone", t.Phone,
			"address_line", t.Addr, "city", t.City, "postal_code", t.Postal,
			"latitude", t.Lat, "longitude", t.Lng,
			"mto_class", "STANDARD", "mto_note", "",
			"failure_reason", "",
			"weight_kg", t.WeightKg,
			"created_at", at, "updated_at", at,
		))
	}
}
