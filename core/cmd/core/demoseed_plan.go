// plan.go, WHAT the demo world is, as data.
//
// This file is a pure function of (clock, branch list). It touches no database, makes no network
// call and reads no wall clock: buildPlan(in) returns the same []row for the same planInput on any
// machine, any day. That is requirement E, an injected clock, dates seeded relative to it, no bare
// time calls, and it is what makes the idempotency test meaningful: if the plan drifted between
// two runs, "the row counts are identical" would only be telling us the conflict clause works.
//
// WHAT THE WORLD HAS TO COVER, and where each part lives below:
//
//	A · Home tiles show distributions, not a single bar   → §5 orders, §6 invoices, §7 counter sales
//	B · Dispatch is testable end to end                   → §8 fleet, §9 runs, §10 stops, §11 will-call
//	C · Picking and loading are testable                  → §12 tasks, shortages, scan trail
//	D · Counter/POS is testable                           → §2 customers, §3 products, §4 pricing
//	E · Determinism                                       → the whole file, and planInput.Now
//	F · The Needs-you queue has something in every row    → §13 parked tickets, §14 approvals
//	G · The "today" widgets are still alive next week     → §15, and dayKey in ids.go
//
// TWO RULINGS MADE HERE, both recorded in NOTES-DEMO-DATA.md so they can be reversed cheaply:
//
//   - NO NUMBER IS MINTED FROM A SHARED SEQUENCE. Order numbers, invoice numbers and run numbers
//     are DEMO-prefixed literals, not next_order_no()/next_run_no() calls. Minting would bump
//     order_no_sequences and run_no_sequences, which means a live tester's next real order number
//     jumps because a demo seed ran. A sequence bump is a mutation of shared state even though no
//     row was edited, and this command promised not to make one.
//
//   - NO PRICING RULE IS WIDER THAN A DEMO ROW. Every pricing_rules row is scoped to a demo
//     CUSTOMER, and every quantity_break_headers row to a demo PRODUCT. A TIER_CATEGORY or
//     TIER_DEFAULT rule would be additive in the row sense and still change what a REAL customer is
//     quoted on staging, an additive row with a destructive effect. The tier scopes are therefore
//     not used at all.
//
// AND A THIRD, ADDED WITH §15: EXACTLY ONE CLASS OF ROW CARRIES THE PLAN DATE IN ITS KEY. Everything
// in §2-§14 is stable-keyed and is written once, ever. §15's rows are day-keyed, so a deploy on the
// 4th adds that day's counter sale and that day's unassigned delivery beside the 3rd's rather than
// recognising them and writing nothing. ids.go argues the whole case; the short version is that a
// tile reading a POPULATION wants the population to hold still, a tile reading a DAY needs a row
// dated today, and only four reads in the product are of the second kind.
// demoseed_plan carries the pin's cmd/demoseed/plan.go (the fold:
// one-shot subcommand of cmd/core, plan and apply modes preserved).
package main

import "time"

// -- inputs ----------------------------------------------------------------------------------------

// branch is a resolved locations row. The seed NEVER creates a branch: branches are the spine every
// real fence and every real screen hangs off, and inventing one would put a fake yard in every
// tester's branch picker.
type branch struct {
	ID   string
	Code string
	Name string
}

// sealer produces a sealed proof-of-delivery attestation for one stop. It is a seam so the plan
// stays pure: main.go passes the real vault-backed sealer, the plan test passes a stub, and neither
// needs the other. See seal.go for why a delivered demo stop gets a REAL sealed attestation rather
// than a fabricated reference.
type sealer func(refKey, typedName, purpose, refType, refID string, at time.Time) (ref string, enc []byte, err error)

type planInput struct {
	Now      time.Time
	Branches []branch
	// Personas maps a demo persona's LOGIN EMAIL (the three 0200 plants) to the users.id that email
	// carries on the target database. Resolved in resolve.go for the same reason Categories is: the
	// answer lives in the database and buildPlan may not ask it one.
	//
	// AN EMPTY MAP IS A SUPPORTED WORLD, not a broken one. A deployment whose migration chain stops
	// short of 0200 has no demo logins, and the plan it gets is the plan this command has always
	// built, the same rows, minus the two columns that name a person. Nothing here is conditional
	// on the personas existing except the edge itself.
	Personas map[string]string
	// Categories maps a demo product's category code ("PAVER", "BULK") to the id of the
	// product_categories row that ALREADY EXISTS on the target database. The seed never creates a
	// category: categories are a curation somebody owns, and a code it cannot resolve simply leaves
	// that product uncategorised rather than inventing a taxonomy on a live system.
	Categories map[string]string
	Seal       sealer
}

// planSummary is what the command prints. It is the human-readable half of the same arithmetic the
// idempotency test asserts on.
type planSummary struct {
	Rows      int
	ByTable   map[string]int
	Orders    map[string]int // status → count
	Aging     map[string]int // bucket → invoice count
	Stops     map[string]int // status → count
	Tasks     map[string]int // status → count
	RunFill   []string       // one line per run: capacity vs recorded weight
	WillCall  []string       // one line per will-call pickup: its raw token
	FirstDate string
	LastDate  string
	// DayKeys is every key this build minted through dayKey, the rows that are RE-CREATED on each
	// new calendar day rather than recognised (ids.go). It is reported rather than derived at the
	// call site for two reasons: the operator needs to be able to read off exactly which rows grow
	// per deploy, and the plan tests need to hold every OTHER id still while the clock moves. A key
	// that ever appears here and should not is the whole failure mode this list exists to expose.
	DayKeys []string
	// The quote world (demoseed_quotes.go): one line per quote, one per share link a tester can open,
	// and the id of every row the section wrote (the rows that exist only while both demo role logins
	// resolve).
	Quotes      []string
	QuoteLinks  []string
	QuoteRowIDs []string
	// The deposit world (demoseed_deposits.go): one line per order a tester can walk, and the id of
	// every row the section wrote.
	DepositOrders []string
	DepositRowIDs []string
}

// -- the catalogue of demo entities, as tables ----------------------------------------------------

type custSpec struct {
	Key, Name, Type string
	TermsDays       int
	CreditCents     int64
	Branch          int
	OnHold          bool
}

// Ten accounts, chosen so the counter surface has one of every pricing shape to render: two plain
// RETAIL walk-ups (list price, no rule), four CONTRACTOR accounts (account price via a customer
// rule), two BUILDER accounts, one GOVERNMENT account, one contractor deliberately ON HOLD so
// the credit gate has something to refuse, and one Cash On Demand account so the pay-before-release
// path has a face. THAT LAST ONE IS THE PRODUCT'S OWN RULE, not a label the seed invented: an
// account with zero-day terms is the defining COD account (billing/credit_gate.go CanDispatch,
// "payment_terms_days = 0 with an unpaid balance"), and it carries a real credit line because the
// product's COD posture is not "cannot buy", it is "the goods do not leave until the money is in"
// (the ring gates the uncaptured exposure against the limit, the dispatch gate holds the release).
// Cash On Demand means pay before release, never cash on delivery. The name is a role label on
// purpose (the house rule), the tester's way to find and type it, and the home is the branch the
// demo logins work so the branch-fenced search answers it.
//
// RULING, C01 IS HOMED AT THE BRANCH THE DEMO LOGINS WORK. The staff customer search is
// branch-fenced (customer/search_scope.go, U-park E4): it admits accounts homed to the searched
// branch and accounts with no home, and that fence is the product's own security posture, not a
// gap for a demo to route around. Every demo login carries exactly one branch grant, at Trenton,
// and the quote world's documents sit at Trenton, so a customer homed to the other branch is
// invisible to every search a tester can run while holding quotes the same tester is looking at:
// Northline's quote was on the Quote Desk and "Northline" answered nothing at the till's picker,
// the same account visible one screen over and unfindable on this one. C01's home is therefore
// Trenton, beside its quotes. Its older order book stays where it always was, on the other
// branch's dashboards, which read orders.branch_id and never the customer's home; the repair for
// databases seeded before this ruling sits in the demoseed_repair pair.
var demoCustomers = []custSpec{
	{"C01", "Northline Hardscapes Ltd (demo)", "CONTRACTOR", 30, 5_000_000, 1, false},
	{"C02", "Bay Ridge Landscaping (demo)", "CONTRACTOR", 30, 2_500_000, 1, false},
	{"C03", "Quinte Paving & Design (demo)", "CONTRACTOR", 45, 7_500_000, 1, false},
	{"C04", "Harbourview Builders (demo)", "BUILDER", 60, 12_000_000, 0, false},
	{"C05", "Cedar Hollow Homes (demo)", "BUILDER", 30, 4_000_000, 1, false},
	{"C06", "M. Okafor (demo retail)", "RETAIL", 0, 0, 0, false},
	{"C07", "D. Lachance (demo retail)", "RETAIL", 0, 0, 1, false},
	{"C08", "County of Hastings (demo)", "GOVERNMENT", 60, 20_000_000, 0, false},
	{"C09", "Stonebrook Contracting (demo)", "CONTRACTOR", 30, 1_500_000, 0, true},
	{"C10", "Cash On Demand customer (demo)", "CONTRACTOR", 0, 500_000, 1, false},
}

type prodSpec struct {
	Key, SKU, Name string
	UOM            string // base AND price UOM, see the ruling below
	ListCents      int64
	WeightKg       string // per BASE unit, as a NUMERIC literal
	Category       string // "PAVER" | "BULK", resolved to an existing product_categories row
	// Application is the product's family as the counter shows it ("Paver", "Wall", "Sand"). It is
	// also the one category-like word every till and Quote Desk surface carries for a product, so it
	// is what chooses the product's drawn tile (the design system's product tiles): the demo plants
	// no photographs, every demo product's picture is its family's tile.
	Application string
	SkidQty     int // full_skid_quantity, and the PLT product_uoms factor; 0 = not palletised
}

// RULING: every demo product prices in its BASE uom (base_uom = price_uom). The catalogue supports
// pricing in a different unit (a paver priced per SF but stocked per PC) and real SKUs use it, but
// that path needs a product_uoms conversion factor to be exactly right or every line total is
// quietly wrong by a rounding step. A demo world whose money does not add up is worse than a demo
// world that does not exercise one conversion, so the seed keeps its arithmetic exact and leaves
// the SF-priced case to the real catalogue. Recorded in NOTES.
var demoProducts = []prodSpec{
	{"P01", demoPrefix + "PAV-HOLLAND", "Holland Paver, Charcoal (demo)", "PC", 595, "2.6000", "PAVER", "Paver", 480},
	{"P02", demoPrefix + "PAV-BRUSSELS", "Brussels Dimensional, Sandstone (demo)", "PC", 745, "3.1000", "PAVER", "Paver", 400},
	{"P03", demoPrefix + "PAV-COURTSTONE", "Courtstone, Basalt (demo)", "PC", 890, "3.4000", "PAVER", "Paver", 360},
	{"P04", demoPrefix + "WALL-BLOCK", "Retaining Wall Block 8x18 (demo)", "PC", 1250, "15.4000", "PAVER", "Wall", 60},
	{"P05", demoPrefix + "STEP-SLAB", "Step Slab 24x48, Natural (demo)", "PC", 9800, "118.0000", "PAVER", "Step", 12},
	{"P06", demoPrefix + "AGG-HPB", "HPB Bedding Stone (demo)", "T", 4950, "1000.0000", "BULK", "Base", 0},
	{"P07", demoPrefix + "AGG-GA", "Granular A (demo)", "T", 3250, "1000.0000", "BULK", "Base", 0},
	{"P08", demoPrefix + "AGG-SAND", "Concrete Sand (demo)", "T", 3850, "1000.0000", "BULK", "Sand", 0},
	{"P09", demoPrefix + "MULCH-BLACK", "Black Cedar Mulch (demo)", "CYD", 5400, "350.0000", "BULK", "Mulch", 0},
	{"P10", demoPrefix + "EDGE-ALU", "Aluminum Edging 8ft (demo)", "EA", 2100, "1.8000", "PAVER", "Edging", 50},
}

// customerRule is one pricing_rules row, always scoped to ONE demo customer (see the file header).
type customerRule struct {
	Key, Cust string
	Scope     string // CUSTOMER_CATEGORY | CUSTOMER_PRODUCT
	Category  string // for CUSTOMER_CATEGORY
	Product   string // for CUSTOMER_PRODUCT
	Calc      string // PCT_OFF_LIST | ABSOLUTE
	Bps       int
	Cents     int64
	Detail    string
}

var demoRules = []customerRule{
	{"R01", "C01", "CUSTOMER_CATEGORY", "PAVER", "", "PCT_OFF_LIST", 1800, 0, "Account pricing, 18% off list on pavers"},
	{"R02", "C02", "CUSTOMER_CATEGORY", "PAVER", "", "PCT_OFF_LIST", 1200, 0, "Account pricing, 12% off list on pavers"},
	{"R03", "C03", "CUSTOMER_CATEGORY", "BULK", "", "PCT_OFF_LIST", 1000, 0, "Account pricing, 10% off list on bulk"},
	{"R04", "C08", "CUSTOMER_CATEGORY", "PAVER", "", "PCT_OFF_LIST", 1500, 0, "Municipal supply agreement, 15% off list"},
	{"R05", "C04", "CUSTOMER_PRODUCT", "", "P04", "ABSOLUTE", 0, 1050, "Negotiated block price for the Harbourview build"},
	// The Cash On Demand account's pricing. Without a rule of its own the account reads the shared
	// CONTRACTOR tier for its pavers (the migrations' TIER_DEFAULT, fifteen percent off), which
	// prices every paver in the catalogue under the PAVER floor at Trenton (round 14's finding (b)):
	// a tester adding a paver line for the account met the "rule under floor" chip on a price no
	// demo row chose. Ten percent off keeps every paver above the floor at both branches' costs, so
	// the account's quote desk story carries no margin story riding it. The apply is keyed on the
	// rule's stable id, so a database seeded before this row existed gains it on the next apply.
	{"R06", "C10", "CUSTOMER_CATEGORY", "PAVER", "", "PCT_OFF_LIST", 1000, 0, "Cash On Demand account pricing, 10% off list on pavers"},
}

// quantity breaks. Both are PRODUCT-scoped onto a demo SKU, and both have their middle and top
// thresholds crossed by orders in §5, a break nobody's quantity reaches renders as a table of
// prices that never apply, which is exactly the "tile shows a single bar" failure at line level.
type breakTier struct {
	From, To string // NUMERIC literals; To == "" is the open-ended top tier
	Bps      int
}
type breakSpec struct {
	Key, Product, UOM, Name string
	Tiers                   []breakTier
}

var demoBreaks = []breakSpec{
	{"QB1", "P01", "PC", "Holland Paver volume breaks (demo)", []breakTier{
		{"0", "500", 0}, {"500", "2000", 500}, {"2000", "", 1000},
	}},
	{"QB2", "P07", "T", "Granular A volume breaks (demo)", []breakTier{
		{"0", "10", 0}, {"10", "40", 400}, {"40", "", 800},
	}},
}

type lineSpec struct {
	Prod string
	Qty  string // NUMERIC literal in the product's base UOM
	QtyF float64
}

type orderSpec struct {
	Key         string
	Branch      int
	Cust        string
	Status      string
	Fulfillment string
	AgeDays     int // created_at = now - AgeDays
	ReqOffset   int // requested_date = today + ReqOffset
	Lines       []lineSpec
}

func ln(prod, qty string, q float64) lineSpec { return lineSpec{prod, qty, q} }

// §5's order book. 28 orders across all nine statuses and all three fulfillment kinds, spread over
// a trailing 90 days. The two large Holland lines (O07: 2400 PC, O21: 900 PC) and the two large
// Granular A lines (O12: 46 T, O18: 22 T) are the quantities that cross QB1's and QB2's thresholds.
var demoOrders = []orderSpec{
	// -- in flight --
	{"O01", 0, "C01", "CONFIRMED", "DELIVERY", 3, 0, []lineSpec{ln("P01", "640.0000", 640), ln("P06", "8.0000", 8)}},
	{"O02", 0, "C04", "CONFIRMED", "DELIVERY", 2, 1, []lineSpec{ln("P04", "180.0000", 180)}},
	{"O03", 1, "C03", "CONFIRMED", "DELIVERY", 2, 1, []lineSpec{ln("P07", "18.0000", 18), ln("P08", "6.0000", 6)}},
	{"O04", 1, "C02", "CONFIRMED", "PICKUP", 1, 0, []lineSpec{ln("P02", "220.0000", 220)}},
	{"O05", 0, "C08", "CONFIRMED", "DELIVERY", 5, 2, []lineSpec{ln("P03", "410.0000", 410)}},
	{"O06", 0, "C01", "PICKING", "DELIVERY", 1, 0, []lineSpec{ln("P01", "320.0000", 320), ln("P10", "24.0000", 24)}},
	{"O07", 0, "C04", "PICKING", "DELIVERY", 1, 0, []lineSpec{ln("P01", "2400.0000", 2400)}},
	{"O08", 1, "C05", "PICKING", "WILL_CALL", 1, 0, []lineSpec{ln("P09", "12.0000", 12)}},
	{"O09", 0, "C01", "READY", "DELIVERY", 2, 0, []lineSpec{ln("P02", "150.0000", 150)}},
	{"O10", 0, "C06", "READY", "WILL_CALL", 1, 0, []lineSpec{ln("P10", "6.0000", 6)}},
	{"O11", 1, "C07", "READY", "WILL_CALL", 1, 0, []lineSpec{ln("P09", "3.0000", 3)}},
	{"O12", 1, "C03", "READY", "DELIVERY", 2, 1, []lineSpec{ln("P07", "46.0000", 46)}},
	{"O13", 0, "C09", "PARTIALLY_FULFILLED", "DELIVERY", 9, -2, []lineSpec{ln("P04", "90.0000", 90), ln("P06", "4.0000", 4)}},
	{"O14", 1, "C05", "PARTIALLY_FULFILLED", "PICKUP", 7, -1, []lineSpec{ln("P02", "300.0000", 300)}},
	// -- finished, recent --
	{"O15", 0, "C01", "FULFILLED", "DELIVERY", 6, -4, []lineSpec{ln("P01", "780.0000", 780)}},
	{"O16", 1, "C03", "FULFILLED", "DELIVERY", 8, -6, []lineSpec{ln("P08", "14.0000", 14)}},
	{"O17", 0, "C04", "FULFILLED", "PICKUP", 4, -3, []lineSpec{ln("P05", "8.0000", 8)}},
	{"O18", 1, "C02", "INVOICED", "DELIVERY", 14, -12, []lineSpec{ln("P07", "22.0000", 22)}},
	{"O19", 0, "C08", "INVOICED", "DELIVERY", 21, -19, []lineSpec{ln("P03", "260.0000", 260)}},
	{"O20", 0, "C01", "INVOICED", "PICKUP", 34, -32, []lineSpec{ln("P01", "410.0000", 410)}},
	{"O21", 1, "C05", "INVOICED", "DELIVERY", 47, -45, []lineSpec{ln("P01", "900.0000", 900)}},
	{"O22", 0, "C04", "CLOSED", "DELIVERY", 61, -59, []lineSpec{ln("P04", "240.0000", 240)}},
	{"O23", 1, "C03", "CLOSED", "PICKUP", 74, -72, []lineSpec{ln("P06", "26.0000", 26)}},
	{"O24", 0, "C09", "CLOSED", "DELIVERY", 88, -86, []lineSpec{ln("P05", "5.0000", 5)}},
	// -- never happened --
	{"O25", 0, "C06", "DRAFT", "PICKUP", 1, 3, []lineSpec{ln("P10", "12.0000", 12)}},
	{"O26", 1, "C07", "DRAFT", "WILL_CALL", 2, 4, []lineSpec{ln("P09", "2.0000", 2)}},
	{"O27", 0, "C09", "CANCELLED", "DELIVERY", 12, -9, []lineSpec{ln("P02", "160.0000", 160)}},
	{"O28", 1, "C02", "CANCELLED", "PICKUP", 25, -22, []lineSpec{ln("P03", "120.0000", 120)}},
}

// counter sales (§7). Each is its own small CLOSED pickup order plus a PAID invoice plus a
// COMMITTED pos_sale, because that is the row trio a real counter sale leaves behind and the
// "Today's sales" tile reads all three (COALESCE invoice total, then order total).
type saleSpec struct {
	Key    string
	Branch int
	Cust   string
	AgeDay int    // 0 = today
	Tender string // ACCOUNT | CASH | CARD | CHEQUE
	Prod   string
	Qty    string
	QtyF   float64
}

var demoSales = []saleSpec{
	{"S01", 0, "C06", 0, "CASH", "P10", "4.0000", 4},
	{"S02", 0, "C01", 0, "ACCOUNT", "P01", "120.0000", 120},
	{"S03", 1, "C07", 0, "CARD", "P09", "2.0000", 2},
	{"S04", 1, "C03", 0, "ACCOUNT", "P07", "6.0000", 6},
	{"S05", 0, "C06", 1, "CARD", "P02", "60.0000", 60},
	{"S06", 0, "C02", 1, "ACCOUNT", "P04", "30.0000", 30},
	{"S07", 1, "C07", 1, "CASH", "P10", "8.0000", 8},
	{"S08", 0, "C05", 2, "CHEQUE", "P08", "3.0000", 3},
	{"S09", 1, "C03", 2, "ACCOUNT", "P06", "5.0000", 5},
	{"S10", 0, "C06", 2, "CASH", "P09", "1.0000", 1},
}

// invoices (§6). DueOffset is days from today: >= 0 is Current, -1..-30 is the 1-30 bucket,
// -31..-60 is 31-60, and anything below -60 is the 60+ bucket the collections conversation is
// actually about. Paid < total and > 0 makes a PARTIALLY_PAID row; Paid == total makes it PAID and
// it drops out of aging (that is the point of including a few).
type invSpec struct {
	Key       string
	Branch    int
	Cust      string
	Order     string // order key, or "" for a standalone invoice
	IssueAge  int
	DueOffset int
	Subtotal  int64
	// Paid is the cash received against this invoice, in cents. -1 is the "settled in full"
	// sentinel: build.go resolves it to the invoice's own total, so a paid demo invoice is paid to
	// the cent no matter how the tax rate is computed. A positive value under the total makes it
	// PARTIALLY_PAID; zero leaves it FINALIZED and fully outstanding.
	Paid int64
	// PaidAge is how many days ago the money arrived. Two invoices are paid TODAY on purpose, so the
	// Payments-today tile has a real count and a real total rather than a zero state.
	PaidAge int
	Prod    string
	Qty     string
	QtyF    float64
}

var demoInvoices = []invSpec{
	// current
	{"I01", 0, "C01", "O15", 6, 24, 464_100, 0, 0, "P01", "780.0000", 780},
	{"I02", 1, "C03", "O16", 8, 37, 53_900, 0, 0, "P08", "14.0000", 14},
	{"I03", 0, "C04", "O17", 4, 26, 78_400, 0, 0, "P05", "8.0000", 8},
	{"I04", 1, "C05", "", 3, 27, 121_500, 40_000, 0, "P09", "22.5000", 22.5},
	{"I05", 0, "C08", "", 9, 51, 208_000, 0, 0, "P03", "260.0000", 260},
	// 1-30 days past due
	{"I06", 1, "C02", "O18", 44, -14, 71_500, 0, 0, "P07", "22.0000", 22},
	{"I07", 0, "C08", "O19", 51, -21, 196_560, 60_000, 12, "P03", "260.0000", 260},
	{"I08", 0, "C01", "O20", 38, -8, 200_090, 0, 0, "P01", "410.0000", 410},
	{"I09", 0, "C09", "", 40, -10, 96_250, 25_000, 0, "P04", "77.0000", 77},
	// 31-60 days past due
	{"I10", 1, "C05", "O21", 77, -47, 535_500, 0, 0, "P01", "900.0000", 900},
	{"I11", 0, "C04", "", 82, -52, 143_750, 0, 0, "P04", "115.0000", 115},
	{"I12", 1, "C02", "", 71, -41, 64_900, 20_000, 44, "P06", "13.1111", 13.1111},
	// 60+ days past due, the bucket the AR tile exists to make impossible to ignore
	{"I13", 0, "C09", "", 118, -88, 187_600, 0, 0, "P05", "19.1428", 19.1428},
	{"I14", 1, "C03", "", 134, -104, 89_100, 30_000, 96, "P07", "27.4153", 27.4153},
	{"I15", 0, "C08", "", 152, -122, 312_000, 0, 0, "P03", "350.5617", 350.5617},
	// settled, history, and deliberately absent from every aging bucket
	{"I16", 0, "C04", "O22", 61, -31, 252_000, -1, 30, "P04", "240.0000", 240},
	{"I17", 1, "C03", "O23", 74, -44, 115_830, -1, 43, "P06", "26.0000", 26},
	{"I18", 0, "C09", "O24", 88, -58, 49_000, -1, 57, "P05", "5.0000", 5},
}

// -- fleet, runs and stops (§8-§10) ---------------------------------------------------------------

type truckSpec struct {
	Key, Code, Name, Type string
	Branch                int
	CapacityKg            string
}

var demoTrucks = []truckSpec{
	{"T1", demoPrefix + "FLAT-1", "Demo flatbed, 5 ton", "FLATBED", 0, "9000.00"},
	{"T2", demoPrefix + "BOOM-1", "Demo boom truck", "BOOM", 0, "6500.00"},
	{"T3", demoPrefix + "DUMP-1", "Demo tandem dump", "DUMP", 1, "13000.00"},
	{"T4", demoPrefix + "PICK-1", "Demo pickup + trailer", "PICKUP", 1, "1100.00"},
}

// -- the demo logins, and the edge from a demo ROW to a demo PERSON -------------------------------
//
// THE TWO HALVES OF THE DEMO WORLD WERE BUILT BY TWO MECHANISMS AND NEVER JOINED. Migration 0200
// plants three logins with roles and branch grants and NO WORK; this command plants drivers, runs,
// stops, tasks and shortages and NO PEOPLE. Nothing wrote the edge between them, and the two places
// it was missing were both visible on a screen:
//
//  1. dispatch_drivers went in with no user_id, so DriverForUser (dispatch/repository.go:177)
//     returned pgx.ErrNoRows for driver.demo@ and the run sheet said, correctly and uselessly,
//     "You are not set up as a driver at this branch."
//  2. Every ACCEPTED / IN_PROGRESS / STAGED / DONE task went in with a claimed_at and a NULL
//     claimed_by, a stamp saying somebody took this pick, with nobody's name on it.
//
// These are the login emails 0200 writes. They are the JOIN KEY and not the uuid, because 0200's
// `ON CONFLICT (email) DO NOTHING` keeps a pre-existing account's own id, see resolve.go.
const (
	personaDispatch = "dispatch.demo@dibbits.example"
	personaYard     = "yard.demo@dibbits.example"
	personaDriver   = "driver.demo@dibbits.example"
)

// demoPersonaEmails is the set resolve.go looks up. All three, not the two the plan currently uses:
// the resolve is one query either way, and the command prints which of the three it found, which is
// the fastest way to see that a deployment is missing 0200 entirely.
var demoPersonaEmails = []string{personaDispatch, personaYard, personaDriver}

// personaDriverPerson is the HUMAN 0200 named, "Priya Nair (demo, Driver)" at 0200's section 1.
// The driver RECORD below is built from this same string so the two can never drift apart. See the
// name ruling under demoDrivers.
const personaDriverPerson = "Priya Nair"

type driverSpec struct {
	Key, Name, Phone, Licence string
	Branch                    int
	// Persona is the login email this driver record belongs to, or "" for a driver who has no login.
	// Most of them have none, and that is the ordinary case rather than a gap: 0090:47 wrote the
	// column nullable precisely because "yards run casual and contract drivers who will never hold a
	// login".
	Persona string
}

// RULING, THE PERSONA IS D1, AND D1 IS RENAMED TO BE HER. Two decisions, and they are one decision.
//
// WHICH DRIVER. The persona has to be a driver with a DAY, or linking the login only upgrades the
// run sheet from an error to an empty list. D1 owns R1 (completed, two days back, both stops
// DELIVERED with a real sealed POD), R4 (TODAY) and R5 (tomorrow), a past, a present and a future,
// which is the whole walkable set. D2 owns one dispatched run, D3 one completed run, D4 the
// deliberately over-capacity one. So: D1.
//
// WHY NOT A NEW DRIVER ROW INSTEAD. apply.go can only emit INSERT … ON CONFLICT DO NOTHING (it says
// so at its line 3 and TestApply_EmitsOnlyConflictYieldingInserts holds it there), so on a database
// this command has ALREADY seeded, D1's row conflicts and the new user_id is not written. The
// tempting fix is a fifth driver under a new key, which would insert cleanly, and it would be a
// driver with no truck, no runs and no stops, sitting in the dispatch board's crew picker as
// somebody who never drives. That is a worse demo than the one we are fixing. The already-seeded
// database is repaired by migration 0206 instead, which is where an UPDATE belongs and the only
// mechanism that reaches staging anyway (staging runs ./bin/migrate and never ./bin/seed).
//
// THE NAME. demoDrivers used to carry "Priya Raman (demo)" on D3 while 0200 carried "Priya Nair
// (demo, Driver)" as the driver login, TWO PRIYAS in one demo world, one of whom is not the one
// signing in. And once D1 holds the login, D1's name is not cosmetic: dispatch-board.ts:495 and
// dispatch-loadboard.ts:1141 both print `run.driver_name` on the crew line, so a dispatcher would
// watch "Marcy Cole (demo)" drive the run that the person signed in as Priya Nair has open on a
// phone. So D1 takes the persona's name and D3 keeps only its surname. There is now exactly one
// Priya in the demo world and she is the one with the password.
//
// The record says "Priya Nair (demo)" and the login says "Priya Nair (demo, Driver)", the same
// human under each surface's own convention. 0200's suffix marks WHICH PERSONA A LOGIN IS and is
// right on an account list; on a crew line it would read "DEMO-FLAT-1 · Priya Nair (demo, Driver)",
// which is a label wearing its own caption.
//
// REVERT: put the two name literals back and drop the Persona column. The ids do not move, so a
// re-seed after a revert changes nothing but the two names.
var demoDrivers = []driverSpec{
	{"D1", personaDriverPerson + " (demo)", "613-555-0141", "DZ", 0, personaDriver},
	{"D2", "Ted Nurse (demo)", "613-555-0162", "AZ", 0, ""},
	{"D3", "Ravi Raman (demo)", "613-555-0178", "DZ", 1, ""},
	{"D4", "Owen Sharpe (demo)", "613-555-0193", "G", 1, ""},
}

type runSpec struct {
	Key    string
	Branch int
	Truck  string // truck key, or "" for an unassigned-crew planned run
	Driver string
	DayOff int // scheduled_date = today + DayOff
	Status string
	Notes  string
}

// Seven runs over six days: two already completed, one out on the road right now, four planned
// ahead. A PLANNED run with no truck and no driver is legal and common (the board's "needs a crew"
// column), and R6 is exactly that.
var demoRuns = []runSpec{
	{"R1", 0, "T1", "D1", -2, "COMPLETED", "Kingston east loop"},
	{"R2", 1, "T3", "D3", -1, "COMPLETED", "Trenton bulk run"},
	{"R3", 0, "T2", "D2", 0, "DISPATCHED", "Boom set, wall block"},
	{"R4", 0, "T1", "D1", 0, "PLANNED", "Kingston afternoon"},
	{"R5", 0, "T1", "D1", 1, "PLANNED", "Kingston west loop"},
	{"R6", 1, "", "", 2, "PLANNED", "Needs a truck and a driver"},
	{"R7", 1, "T4", "D4", 3, "PLANNED", "Small drops, over capacity, on purpose"},
}

type stopSpec struct {
	Key      string
	Run      string // run key, or "" for an UNASSIGNED stop (run_id NULL)
	Branch   int
	Order    string
	StopNo   int
	DayOff   int
	Status   string
	Window   string
	Contact  string
	Phone    string
	Addr     string
	City     string
	Postal   string
	Lat, Lng float64
	MTOClass string
	MTONote  string
	WeightKg string // "" = never weighed; the manifest prints "-" and refuses to total it
	Received string // POD display name, for DELIVERED stops
	Failure  string
}

// THE LOAD GAUGE. A run's fill is its COMPLETE recorded weight against its truck's advisory
// capacity (dispatch/model_w3.go: a partial total can never raise an over-capacity warning), so the
// stops below are weighed to put each run in a different band and give a green/yellow/red gauge
// something to be each colour about:
//
//	R4 · FLAT-1  9000kg  · 1400 + 1750          =  3150kg  →  35%   green
//	R1 · FLAT-1  9000kg  · 4100 + 980           =  5080kg  →  56%   green/amber boundary
//	R5 · FLAT-1  9000kg  · 3200 + 2100 + 1250   =  6550kg  →  73%   yellow
//	R2 · DUMP-1  13000kg · 9800 + 2340          = 12140kg  →  93%   red, and still legal
//	R7 · PICK-1  1100kg  · 900 + 620            =  1520kg  → 138%   OVER CAPACITY, deliberately
//	R3 · BOOM-1  6500kg  · 2900 + (one unweighed)         →  partial: the honest "not weighed" case,
//	                                                         which can never raise an over-capacity
//	                                                         warning no matter how heavy it gets
var demoStops = []stopSpec{
	// completed history
	{"SP01", "R1", 0, "O15", 1, -2, "DELIVERED", "AM", "Site foreman", "613-555-0201", "418 Bath Rd", "Kingston", "K7M 4X5", 44.2312, -76.5460, "STANDARD", "", "4100.0000", "R. Vance (demo)", ""},
	{"SP02", "R1", 0, "O17", 2, -2, "DELIVERED", "AM", "Yard gate", "613-555-0202", "77 Gardiners Rd", "Kingston", "K7M 3X9", 44.2489, -76.5591, "MTO", "Cut to 1150mm, do not substitute", "980.0000", "T. Okonkwo (demo)", ""},
	{"SP03", "R2", 1, "O16", 1, -1, "DELIVERED", "8-10", "Back lot", "613-555-0203", "290 Dundas St W", "Trenton", "K8V 3S5", 44.1001, -77.5766, "STANDARD", "", "9800.0000", "L. Beaupre (demo)", ""},
	{"SP04", "R2", 1, "O21", 2, -1, "FAILED", "PM", "Nobody on site", "613-555-0204", "12 Sidney St", "Trenton", "K8V 2H5", 44.1063, -77.5824, "STANDARD", "", "2340.0000", "", "Nobody on site and the gate was locked"},
	// out on the road now
	{"SP05", "R3", 0, "O02", 1, 0, "EN_ROUTE", "AM", "Harbourview site office", "613-555-0205", "1500 Princess St", "Kingston", "K7M 3E7", 44.2437, -76.5372, "STANDARD", "", "2900.0000", "", ""},
	{"SP06", "R3", 0, "O13", 2, 0, "LOADED", "PM", "Call ahead", "613-555-0206", "88 Days Rd", "Kingston", "K7M 3R6", 44.2551, -76.5688, "MIXED", "Wall block is MTO; bedding stone is stock", "", "", ""},
	// planned, the fill bands
	{"SP07", "R4", 0, "O01", 1, 0, "READY", "PM", "Driveway, tight turn", "613-555-0207", "233 Johnson St", "Kingston", "K7L 1Y4", 44.2299, -76.4951, "STANDARD", "", "1400.0000", "", ""},
	{"SP08", "R4", 0, "O09", 2, 0, "READY", "PM", "Side entrance", "613-555-0208", "640 Cataraqui Woods Dr", "Kingston", "K7P 1T9", 44.2669, -76.5834, "STANDARD", "", "1750.0000", "", ""},
	{"SP09", "R5", 0, "O06", 1, 1, "READY", "AM", "Foreman on site", "613-555-0209", "95 Bagot St", "Kingston", "K7L 3E7", 44.2295, -76.4831, "STANDARD", "", "3200.0000", "", ""},
	{"SP10", "R5", 0, "O07", 2, 1, "READY", "AM", "Large drop, clear the pad", "613-555-0210", "1201 Division St", "Kingston", "K7K 6X4", 44.2711, -76.5089, "STANDARD", "", "2100.0000", "", ""},
	{"SP11", "R5", 0, "O05", 3, 1, "READY", "PM", "Municipal yard", "613-555-0211", "216 Ontario St", "Kingston", "K7L 2Z3", 44.2286, -76.4809, "STANDARD", "", "1250.0000", "", ""},
	{"SP12", "R7", 1, "O03", 1, 3, "READY", "AM", "Residential", "613-555-0212", "44 Quinte St", "Trenton", "K8V 3S9", 44.0994, -77.5738, "STANDARD", "", "900.0000", "", ""},
	{"SP13", "R7", 1, "O12", 2, 3, "READY", "AM", "Residential", "613-555-0213", "7 Marmora St", "Trenton", "K8V 4R4", 44.1042, -77.5691, "STANDARD", "", "620.0000", "", ""},
	// unassigned, scheduled for a day, not yet on a truck. Most stops are born here.
	{"SP14", "", 0, "O19", 0, 0, "READY", "", "Awaiting a truck", "613-555-0214", "801 Development Dr", "Kingston", "K7M 5V6", 44.2603, -76.5726, "STANDARD", "", "2600.0000", "", ""},
	{"SP15", "", 1, "O14", 0, 1, "READY", "AM", "Awaiting a truck", "613-555-0215", "35 Monogram Pl", "Trenton", "K8V 6S3", 44.0967, -77.5647, "STANDARD", "", "1180.0000", "", ""},
	{"SP16", "", 1, "O04", 0, 2, "READY", "", "Awaiting a truck", "613-555-0216", "160 Dundas St E", "Trenton", "K8V 1L6", 44.1015, -77.5688, "MTO", "Bullnose cut to order", "", "", ""},
	{"SP17", "", 0, "O22", 0, -1, "CANCELLED", "", "Customer collected instead", "613-555-0217", "19 Union St", "Kingston", "K7L 2N8", 44.2263, -76.4966, "STANDARD", "", "", "", ""},
}

// will-call pickups (§11). ACTIVE and REVOKED are seeded freely; the single CONFIRMED pickup gets a
// REAL sealed attestation, for the same reason the DELIVERED stops do, see seal.go.
type willCallSpec struct {
	Key      string
	Order    string
	Branch   int
	Status   string
	DaysLeft int    // expires_at = now + DaysLeft days
	Received string // for CONFIRMED
}

var demoWillCall = []willCallSpec{
	{"W1", "O10", 0, "ACTIVE", 3, ""},
	{"W2", "O11", 1, "ACTIVE", 5, ""},
	{"W3", "O08", 1, "CONFIRMED", -1, "K. Villeneuve (demo)"},
	{"W4", "O26", 1, "REVOKED", -2, ""},
}

// picking (§12). Every lifecycle state the tasks table admits appears at least once, including the
// two the queue tile deliberately excludes (STAGED, DONE) so a tester can see the difference
// between "waiting" and "parked in a bay".
type taskSpec struct {
	Key      string
	Order    string
	Branch   int
	Type     string
	Status   string
	Priority string
	AgeHours int
	LineNo   int    // 0 = whole-order task
	Stage    string // stage_location, for STAGED
	StageRef string
	Deadline int // accept_deadline_at = now - Deadline hours; 0 = none
	Weight   string
}

var demoTasks = []taskSpec{
	{"K01", "O06", 0, "PICK", "QUEUED", "NORMAL", 2, 0, "", "", 0, ""},
	{"K02", "O07", 0, "PICK", "QUEUED", "HIGH", 5, 0, "", "", 0, ""},
	{"K03", "O01", 0, "PICK", "PUSHED", "URGENT", 9, 0, "", "", 4, ""},
	{"K04", "O05", 0, "PICK", "PUSHED", "NORMAL", 26, 0, "", "", 20, ""},
	{"K05", "O02", 0, "PICK", "ACCEPTED", "NORMAL", 3, 0, "", "", 0, ""},
	{"K06", "O03", 1, "PICK", "ACCEPTED", "LOW", 4, 0, "", "", 0, ""},
	{"K07", "O08", 1, "PICK", "IN_PROGRESS", "NORMAL", 1, 0, "", "", 0, ""},
	{"K08", "O12", 1, "PICK", "IN_PROGRESS", "HIGH", 2, 0, "", "", 0, ""},
	{"K09", "O04", 1, "PICK", "IN_PROGRESS", "NORMAL", 6, 1, "", "", 0, ""},
	{"K10", "O09", 0, "PICK", "STAGED", "NORMAL", 7, 0, "Bay 3", demoPrefix + "STG-014", 0, ""},
	{"K11", "O10", 0, "PICK", "STAGED", "NORMAL", 11, 0, "North fence", demoPrefix + "STG-015", 0, ""},
	{"K12", "O11", 1, "PICK", "STAGED", "LOW", 30, 0, "Bay 1", demoPrefix + "STG-016", 0, ""},
	{"K13", "O15", 0, "PICK", "DONE", "NORMAL", 50, 0, "", "", 0, ""},
	{"K14", "O16", 1, "PICK", "DONE", "NORMAL", 74, 0, "", "", 0, ""},
	{"K15", "O27", 0, "PICK", "CANCELLED", "NORMAL", 96, 0, "", "", 0, ""},
	{"K16", "O13", 0, "NEED_WEIGHT", "IN_PROGRESS", "NORMAL", 4, 1, "", "", 0, ""},
	{"K17", "O14", 1, "NEED_WEIGHT", "QUEUED", "HIGH", 2, 0, "", "", 0, ""},
}

// shortages. K07 is the SHORT PICK: the yard found less than the ticket asked for and said so, and
// nobody has settled it with the customer yet. K13's shortage is the resolved counter-case, so the
// surface has both a live problem and a closed one.
//
// THE PROCUREMENT GAP is K08's: Granular A is short, it sits below its reorder point at that branch
// (§3 seeds the reorder point under the on-hand), and there is no inbound purchase order covering
// it. The shortfall therefore has nothing arriving to close it, which is the state the yard needs
// to be able to see and purchasing needs to be told about, and it is the one this seed plants on
// purpose. (Interpretation recorded in NOTES: "procurement gap" is read as an open shortage with no
// inbound supply, not as a portal procurement_projects row.)
type shortageSpec struct {
	Key, Task, Prod string
	LineNo          int
	Qty, UOM        string
	Note            string
	Resolution      string // "" = still open
	AgeHours        int
}

var demoShortages = []shortageSpec{
	{"SH1", "K07", "P09", 1, "4.0000", "CYD", "Only 8 yards left in the bin", "", 1},
	{"SH2", "K08", "P07", 1, "12.0000", "T", "Pile is down to fines, nothing on order to cover it", "", 2},
	{"SH3", "K13", "P01", 1, "40.0000", "PC", "Two banded layers were cracked", "Substituted from the Trenton skid", 48},
}

type scanSpec struct {
	Key, Order, Stop, LoadRef, Checkpoint string
	Branch                                int
	AgeHours                              int
	Note                                  string
}

var demoScans = []scanSpec{
	{"SC1", "O15", "SP01", demoPrefix + "LOAD-0041", "YARD", 0, 60, "Banded and wrapped"},
	{"SC2", "O15", "SP01", demoPrefix + "LOAD-0041", "PICKED", 0, 54, ""},
	{"SC3", "O15", "SP01", demoPrefix + "LOAD-0041", "IN_TRANSIT", 0, 50, ""},
	{"SC4", "O15", "SP01", demoPrefix + "LOAD-0041", "DELIVERED", 0, 48, "Signed at the gate"},
	{"SC5", "O07", "", demoPrefix + "LOAD-0057", "YARD", 0, 6, "Two skids staged"},
	{"SC6", "O02", "SP05", demoPrefix + "LOAD-0058", "IN_TRANSIT", 0, 2, ""},
	{"SC7", "O03", "", demoPrefix + "LOAD-0059", "RECEIVED", 1, 30, "Supplier drop, awaiting a stop"},
}

// -- §13 the counter's parked tickets (Needs-you · pos.parked) ------------------------------------
//
// MEASURED, NOT ASSUMED. The Needs-you widget's pos.parked source calls pos.Service.ListParked per
// branch, which is `SELECT … FROM pos_parked_tickets WHERE branch_id = $1 AND status = 'PARKED'`
// (pos/repository_w7.go). demoseed already writes pos_sale rows and NONE of them is a parked
// ticket, a parked ticket is not a sale in any state, it is a basket somebody put down before a
// sale existed, in its own table with no order, no stock effect and no AR. So the widget counted
// zero on a fully seeded database, which reads on screen as "nothing is waiting at the counter"
// rather than as "this surface has no data".
//
// TWO TICKETS, ONE PER BRANCH, AND ONE OF THEM IS A WALK-IN. The two shapes the counter actually
// parks: an account customer who went to measure, and an anonymous walk-in who left the basket at
// the till. customer_id is nullable precisely for the second (0043:29 "NULL = walk-in") and a demo
// with only the named case would leave the harder render untested.
//
// NOT DAY-KEYED, AND THAT IS THE READ'S OWN DOING. ListParked has no date predicate at all: a
// ticket is waiting until somebody resumes or discards it, however long that takes. A day-keyed
// parked ticket would therefore not refresh anything, it would pile a new abandoned basket onto
// the counter every deploy, which is a worse lie than a stale one.
//
// ticket_no IS A DEMO- LITERAL. The live path mints '#PARK-' || nextval('parked_ticket_no_seq'), and
// this file's first ruling forbids that: a demo seed must not move a live tester's next ticket
// number. The number therefore reads '#PARK-DEMO-01', which is also how a clerk knows at a glance
// which basket is not theirs.
//
// parked_by IS LEFT NULL ON PURPOSE, and it is the opposite call from tasks.claimed_by. There the
// row carried a claimed_at, a stamp asserting somebody took the pick, so leaving the name off made
// the row contradict itself. Here there is no paired stamp: created_at is a fact about the row, not
// a claim about a person. And 0200 plants a dispatcher, a yard hand and a driver, none of whom rings
// a till, so naming one of them would be inventing a counter clerk. Nothing reads the column back
// either, ListParkedTickets does not select it, so a value there would be a person no screen shows.
type parkedSpec struct {
	Key    string
	No     string // ticket_no, DEMO-prefixed: never drawn from parked_ticket_no_seq
	Branch int
	Cust   string // customer key, or "" for a walk-in
	Type   string // COLLECT_NOW
	Notes  string
	Prod   string
	Qty    float64
	UOM    string
}

var demoParked = []parkedSpec{
	{"PK1", "#PARK-" + demoPrefix + "01", 0, "C01", "COLLECT_NOW",
		"Gone to measure the driveway, hold the skid count until she calls back.", "P01", 240, "PC"},
	{"PK2", "#PARK-" + demoPrefix + "02", 1, "", "COLLECT_NOW",
		"Walk-in went to fetch a trailer. Basket left at till 2.", "P09", 3, "CYD"},
}

// -- §14 an approval that is waiting on somebody ELSE (Needs-you · approvals.waiting) -------------
//
// WHAT THE WIDGET ACTUALLY COUNTS, read out of the adapter rather than guessed at. needs_adapters.go
// asks identity.Service.ListApprovals(caller, PENDING), which is
// `SELECT … FROM permission_approval_requests WHERE status = 'PENDING'` narrowed to the caller's
// granted branches, and then keeps a row only when BOTH of these hold:
//
//	r.RequestedBy != caller.UserID          four eyes: your own request never waits on you
//	identity.CanApprove(caller, r.Policy, r.BranchID)
//
// demoseed wrote no approval rows at all, so the count was structurally zero for every caller.
//
// WHICH POLICY, AND WHY THERE IS ONLY ONE HONEST ANSWER. The queue admits five (0016 widened by 0075
// and 0195): MARGIN_BELOW_MINIMUM, CREDIT_LIMIT_EXCEEDED, COD_CONSTRAINT, JE_OVER_THRESHOLD and
// FREIGHT_ABOVE_QUOTE. The requester has to be somebody this demo world contains, and the demo world
// contains exactly three logins, 0200's dispatcher, yard hand and driver. A margin override is
// filed by whoever quoted; the two credit policies are filed by whoever rang an order on credit, and
// identity.sellingRoles admits neither LOGISTICS_MANAGER nor YARD_TEAM nor DRIVERS; a JE approval is
// filed by the ledger and refuses a caller-facing filer outright. FREIGHT_ABOVE_QUOTE is filed by
// the DISPATCHER, from the load board, when a split would charge a customer more delivery than they
// were quoted, which is precisely the dispatcher persona's job (the login 0200 planted, now "Dispatch persona
// (demo)"). So one policy fits, twice, once per branch,
// and the alternative was to plant a margin override under a driver's name.
//
// WHO IT WAITS ON. CanApprove routes FREIGHT_ABOVE_QUOTE to identity's rail-approver set -
// GENERAL_MANAGER, FINANCIAL_CONTROLLER, SUPER_ADMIN, all Tier-1 GLOBAL, so branch access is
// automatic. On a migrations-only staging box those roles are held by 0061's
// scrubbed-0004@example.invalid and scrubbed-0006@example.invalid and by
// 0043's platform admin, all planted by MIGRATIONS and therefore all
// present on the one box a demo is shown from. Four eyes holds because the dispatcher is none of them.
//
// AND THE PROPOSAL IS PLANTED WITH THE REQUEST, which is the same argument seal.go makes about a
// delivered stop. dispatch parks a delivery_freight_raises row and then files the approval whose
// subject_ref IS that row's id (dispatch/service_freightraise.go). An approval with no raise behind
// it would render as a card asking somebody to agree to a figure that exists nowhere, and would
// teach that subject_ref is decorative. Both rows are written, pointing at each other, in the state
// the gate leaves them: raise PARKED with no decider, request PENDING.
//
// NOT DAY-KEYED. ListApprovals has no date predicate, a pending request is waiting until it is
// decided. Re-filing the same raise daily would also collide with 0195's one-parked-raise-per-order
// unique index the moment two days' rows named the same order.
//
// IF 0200 IS ABSENT THIS SECTION WRITES NOTHING. requested_by is NOT NULL with a live foreign key,
// so with no resolved persona there is no honest requester and the rows are simply not built, the
// same posture the driver edge takes, and the same one resolveCategories takes for a missing code.
type approvalSpec struct {
	Key      string
	Branch   int
	Order    string // the order whose freight is being raised
	Cust     string // customer key, for the frozen display name on the raise
	Persona  string // the login email that filed it
	Role     string // requested_by_role, frozen on the raise the way dispatch freezes it
	Quoted   int64  // what the customer was quoted, in cents
	Proposed int64  // what the dispatcher is asking for, CHECKed above Quoted by 0195
	Loads    int
	AgeHours int
}

// Two, one per branch, so a global approver's queue is not a single row and a branch-scoped one is
// not empty. Proposed is above Quoted on both, which 0195 enforces with a CHECK rather than trusting
// the writer: a row in that table IS a raise by definition.
var demoApprovals = []approvalSpec{
	{"AP1", 0, "O01", "C01", personaDispatch, "LOGISTICS_MANAGER", 18_000, 26_500, 2, 20},
	{"AP2", 1, "O03", "C03", personaDispatch, "LOGISTICS_MANAGER", 9_500, 14_000, 1, 5},
}

// -- §15 the rows that carry the plan's DATE in their key -----------------------------------------
//
// THE PROBLEM, STATED AS ARITHMETIC. Every §2-§14 row is stable-keyed, so the first apply freezes
// what "today" meant on that date and every later apply recognises those rows and writes nothing.
// The commander auto-applies on each deploy. Therefore on the day after the first apply,
// sales.today = $0, deliveries.today = 0, dispatch.unassigned = 0 within a working window that has
// moved past every seeded stop, and dispatch.due_to_commit = 0. Four measurements dark on a box
// people look at weekly. ids.go's dayKey is the fix and argues the case; this is what it is applied to.
//
// TWO SALES AND TWO DELIVERIES, ONE OF EACH PER BRANCH, AND THE SIZE IS THE DESIGN. Eighteen rows a
// day, forever. Making it four sales and five deliveries, matching §7's and §10's first-day
// richness, would be sixty, and the first-day richness is already there: the day-keyed pair sits
// BESIDE §7's ten sales and §10's seventeen stops on day one and is all that is left on day thirty.
// A demo that is thin on its second Tuesday is still a demo; one that has quietly written twenty
// thousand rows into somebody's staging database is a bug with a good excuse.
//
// THE SALES ARE ONE CASH AND ONE ACCOUNT because sales.today COALESCEs the invoice total over the
// order total and the two tenders take different paths through §7's builder, an ACCOUNT sale is
// FINALIZED with the balance outstanding and no payment row, a CASH sale is PAID with one. Seeding
// only one tender would leave whichever path it is not exercised every day after the first.
//
// THE DELIVERIES ARE UNASSIGNED, a stop with no run, scheduled for today, REQUESTED rather than
// committed. That single shape is read by three of the four dark measurements at once:
// deliveries.today counts it (scheduled today, not DELIVERED or CANCELLED), dispatch.unassigned
// counts it (run_id NULL inside the working window that starts today) and dispatch.due_to_commit
// counts it (unassigned, not committed, and inside the 48-hour commit prompt by definition, since it
// is scheduled for today). Putting it on a run instead would need a run scheduled today, and the
// seven-run fleet is stable-keyed on purpose, so a day-keyed stop pointing at a run frozen on the
// first-apply date would be a delivery scheduled for Thursday riding a truck that went out on Monday.
//
// EACH ONE BRINGS ITS OWN ORDER. A stop and a counter sale both need an order to be about, and
// pointing at §5's stable order book would attach a hundred stops to order DEMO-KGN-O01 over a
// hundred deploys. The order is one row and one line; it costs less than the incoherence.
type todaySpec struct {
	Key    string // the day-independent half of the key: which of today's rows this is
	Branch int
	Cust   string
	Prod   string
	Qty    string // NUMERIC literal in the product's base UOM
	QtyF   float64
	Tender string // for a sale: CASH | ACCOUNT, "" means this is a delivery, not a sale
	// The delivery half. Empty on a sale.
	Contact, Phone, Addr, City, Postal string
	Lat, Lng                           float64
	WeightKg                           string
}

// Today's counter sales. Keys are T1/T2 and the DAY supplies the rest, so the ids move and the
// prose does not have to.
var demoTodaySales = []todaySpec{
	{Key: "T1", Branch: 0, Cust: "C06", Prod: "P01", Qty: "60.0000", QtyF: 60, Tender: "CASH"},
	{Key: "T2", Branch: 1, Cust: "C03", Prod: "P07", Qty: "4.0000", QtyF: 4, Tender: "ACCOUNT"},
}

// Today's unassigned deliveries, the stop that has arrived and has not been put on a truck.
var demoTodayStops = []todaySpec{
	{Key: "T3", Branch: 0, Cust: "C04", Prod: "P04", Qty: "60.0000", QtyF: 60,
		Contact: "Site foreman", Phone: "613-555-0231", Addr: "77 Sir John A. Macdonald Blvd",
		City: "Kingston", Postal: "K7L 4W6", Lat: 44.2352, Lng: -76.5061, WeightKg: "924.0000"},
	{Key: "T4", Branch: 1, Cust: "C02", Prod: "P08", Qty: "5.0000", QtyF: 5,
		Contact: "Back gate", Phone: "613-555-0232", Addr: "271 Dundas St E",
		City: "Trenton", Postal: "K8V 1M1", Lat: 44.1022, Lng: -77.5652, WeightKg: "5000.0000"},
}

// -- §16 what the demo stock COSTS, so the two cost-class measures stop lying ---------------------
//
// THIS ONE WAS NOT BLANK, IT WAS WRONG, which is worse and is why it is here rather than in the
// report's "still blank" column. Both cost-class measures join product_branch_costs:
//
//	inventory.value  stock_balances JOIN product_branch_costs … , an INNER join, so a product with
//	                 no cost row contributes nothing and the demo yard's twenty stock balances were
//	                 invisible. Blank, and honestly blank.
//	margin.gross_bps order_lines LEFT JOIN product_branch_costs …, a LEFT join under
//	                 COALESCE(SUM(qty * avg_unit_cost_cents), 0), so a missing cost row is read as a
//	                 cost of ZERO. Measured on a demoseeded database: three order lines of forty-two
//	                 had a cost and the rest were free, so the branch bars reported a gross margin
//	                 near 100%. A confident wrong number on a cost widget, under a caption that says
//	                 which branch is doing better.
//
// TWENTY ROWS CLOSE BOTH. One weighted average per demo product per branch, which is what the real
// receiving path maintains and what the GL's cost-basis posting reads, so the chart, the tile and
// the ledger are answering from one number.
//
// THE TWO BRANCHES DIFFER ON PURPOSE. Trenton's costs are about 4% over Kingston's, the freight to
// the second yard, because a by-branch margin chart whose two bars are identical says nothing, and
// the whole reason the measure offers a branch dimension is to let somebody see which yard is
// buying better.
//
// NOT DAY-KEYED: a weighted average cost is a current-state fact per product per branch, and the
// natural key (product_id, branch_id) is the arbiter, so a cost somebody has since maintained by
// hand survives untouched.
//
// The pair is {branch 0, branch 1} in cents, in the product's BASE uom, the same unit
// stock_balances.on_hand_base and order_lines.qty_base are in, which is what makes the two
// multiplications above correct rather than merely typed.
var branchCost = map[string][2]int64{
	"P01": {395, 411},
	"P02": {492, 512},
	"P03": {596, 620},
	"P04": {838, 872},
	"P05": {6620, 6885},
	"P06": {3560, 3702},
	"P07": {2380, 2475},
	"P08": {2790, 2902},
	"P09": {3620, 3765},
	"P10": {1290, 1342},
}
