// The local quote stub: the demo way in, the quote operations, the surface
// settings kit and the catalogue reads in the contract's shapes, for the dev
// server's /api proxy and the Playwright harness. This is a development and
// test double only: the real routes are the core's (the mint is the demo
// build's, the floor decision is the pricing engine's, the workspace rows are
// the settings kit's), and nothing in the app imports this file. When the real
// routes land, the app and this suite keep working against them unchanged,
// because both speak only the contract's shapes:
//   GET  /api/v1/demo/flows                      -> DemoFlowList
//   POST /api/v1/demo/session                    -> DemoSessionResponse
//   GET  /api/v1/branches                        -> Branch[]
//   GET  /api/v1/quotes?status=                  -> QuoteSummaryPage
//   POST /api/v1/quotes                          -> QuoteView
//   GET  /api/v1/quotes/{id}                     -> QuoteView
//   PATCH /api/v1/quotes/{id}                    -> QuoteEditResult
//   POST /api/v1/quotes/{id}/lines               -> QuoteEditResult
//   PATCH /api/v1/quotes/{id}/lines/{n}          -> QuoteEditResult | 409 QuoteFloorRefusal
//   DELETE /api/v1/quotes/{id}/lines/{n}         -> QuoteEditResult
//   GET/PUT /api/v1/quotes/{id}/terms            -> QuoteTerms
//   GET  /api/v1/quotes/{id}/margin-glance       -> QuoteMargin (byte-absent for an off-list caller)
//   POST /api/v1/quotes/{id}/share-links         -> QuoteMintedShareLink
//   POST /api/v1/quotes/{id}/sms-share           -> QuoteSMSShareResult | 409
//   POST /api/v1/quotes/{id}/convert             -> QuoteConvertResult | 409
//   GET  /api/v1/quotes/{id}/related-documents   -> QuoteRelatedDocumentList
//   GET  /api/v1/orders/{id}/related-documents   -> QuoteRelatedDocumentList
//   POST /api/v1/quotes/{id}/order-payment       -> QuoteOrderPaymentResult | 400 | 403 | 404 | 409
//   POST /api/v1/quotes/{id}/lines/{n}/floor-approval -> QuoteFloorApprovalAnswer
//   GET/PUT/DELETE /api/v1/surfaces/quotes.builder/my-workspace  -> the workspace row
//   GET/PUT/DELETE /api/v1/surfaces/quotes.builder/my-items      -> the column curation
//   GET  /api/v1/surfaces/quotes.builder/config  -> SettingsSurfaceConfig
//   GET  /api/v1/quotes/{id}/edits               -> QuoteEditHistory
//   GET  /api/v1/quotes/{id}/share-links         -> QuoteShareLinkList
//   DELETE /api/v1/quotes/{id}/share-links/{id}  -> 204
//   POST /api/v1/quotes/{id}/lines/{n}/compare   -> QuoteLineComparison
//   GET/PUT /api/v1/quotes/policy                -> QuotePolicy | 403 | 422
//   GET/PUT /api/v1/quotes/settings/acceptance   -> QuoteAcceptanceSettings | 403
//   GET  /api/v1/quote-assist/scope-rules        -> QuoteAssistScopeRuleList | 403
//   PATCH /api/v1/quote-assist/scope-rules/{code} -> QuoteAssistScopeRule | 403
//   PUT  /api/v1/surfaces/quotes.builder/items   -> { saved: true } | 403
//   GET  /api/v1/products/search                 -> ProductSearchResults
//   GET  /api/v1/customers/search                -> PosCustomer[]
//   GET  /api/v1/pos/salespeople                 -> PosSalespersonPage
//   GET  /api/v1/pos/account-standing            -> PosAccountStanding ({} unless a hold is on file)
//   GET  /api/v1/pos/addon-suggestions           -> { suggestions: [] }
//   GET  /api/v1/products?active=true            -> PosProduct[]
//   GET  /api/v1/products/counter-search         -> ProductCounterSearchResults
//   GET  /api/v1/products/scan                   -> CounterHit | 404
//   GET  /api/v1/products/{id}/image             -> 404 (the placeholder thumb)
//   POST /api/v1/quote-assist/extract            -> QuoteAssistExtraction
//   GET  /api/v1/quote-assist/scope-gaps         -> QuoteAssistScopeGaps
//   GET  /api/v1/quotes/{id}/followups           -> QuoteFollowUpHistory
//   POST /api/v1/quotes/{id}/followups/{disable|enable} -> QuoteFollowUpHistory | 409
//   GET  /api/v1/quotes/{id}/hold-expiry         -> QuoteHoldExpiryView
//   POST /api/v1/quotes/{id}/holds/resync        -> QuoteHoldResyncResult | 409
//   GET  /api/v1/quotes/{id}/availability        -> QuoteLineAvailabilityList
//   GET  /api/v1/quote-availability/{id}/branches -> QuoteBranchStockList
//   POST /api/v1/staff/policy/check              -> IdentityPolicyCheckResponse
//   GET  /api/v1/staff/approvals                 -> IdentityApprovalPage
//   POST /api/v1/staff/approvals/{id}/decide     -> IdentityStatusResponse | 403 | 409
//
// The demo routes are public in the contract (security: []), so they check no
// credential. The staff routes read the minted bearer the way the core's
// middleware does (the token's own sub claim), hold the demo org header beside
// it, and refuse a call without either.
//
// Money is integer cents throughout, computed HERE (the stub stands in for the
// core, the only place money is ever computed) and never in the app. The floor
// decision is the stub's too: the category floors and the caller's role are
// the plan's own rows, and the typed 409 body is the contract's
// QuoteFloorRefusal, field for field, with no error field, exactly as the
// core's own FloorRefusalError marshals.
import { createServer } from "node:http";
import { handlePricing, resetPricing } from "./pricing-stub.mjs";

const port = Number(process.env.QD_STUB_PORT ?? 4839);

// The closed flow table: the two logins this release shows.
const FLOWS = [
  { flow: "sales", role_code: "inside_sales", label: "Sales" },
  { flow: "sales-manager", role_code: "sales_manager", label: "Sales manager" },
];

// The minted accounts, one per flow, shaped field for field like the staff
// login user block (core/api/openapi.yaml, IdentityUser). The tiers are the
// core resolver's own: 1 leadership, 2 manager, 3 frontline. The accounts are
// demo labels on reserved domains, never people. Each account's grant reaches
// its own branch only (the demo world's Point Loma).
const USERS = {
  sales: {
    user_id: "demo-account-sales",
    email: "sales@demo.invalid",
    display_name: "Sales (demo)",
    role_code: "inside_sales",
    role_name: "Inside Sales",
    tier: 3,
    scope: "BRANCH",
  },
  "sales-manager": {
    user_id: "demo-account-sales-manager",
    email: "sales-manager@demo.invalid",
    display_name: "Sales manager (demo)",
    role_code: "sales_manager",
    role_name: "Sales Manager",
    tier: 2,
    scope: "BRANCH",
  },
};

// ---------------------------------------------------------------------------
// The invented world: one yard, a counter catalogue, two accounts. Every
// identity is an invented label on a reserved domain, never a real one.
// ---------------------------------------------------------------------------

const POINT_LOMA_ID = "6f1c2b3a-0000-4000-8000-000000000001";

const BRANCHES = [
  {
    id: POINT_LOMA_ID,
    code: "PLM",
    name: "Point Loma",
    address: { line1: "12 Riverside Yard Road", city: "Point Loma", province: "CA", postal: "92106", country: "US" },
    is_default: true,
    is_system_default: true,
    is_active: true,
  },
];

// The core's MarginFloorBps (staffcontext rbac.go): the floor the policy check holds a quote to.
const MARGIN_FLOOR_BPS = 1200;
// The core bounds the check's amounts (staffcontext maxPolicyCheckCents) and parses the branch as a uuid.
const MAX_CHECK_CENTS = 1_000_000_000_000;
const UUID_SHAPE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

const ACCOUNT_BRANCH = {
  "demo-account-sales": POINT_LOMA_ID,
  "demo-account-sales-manager": POINT_LOMA_ID,
};

// The catalogue: list_cents is the retail anchor, branch_cost_cents the
// weighted cost the margin glance reads, floor_bps the category margin floor
// the discount decision reads. Charcoal's rule price for the contractor
// account sits below the Pavers floor on purpose: the plan's ruled mark (a
// customer's own rule price under the floor is accepted and marked) has to be
// reachable, and that is the one line that carries it.
const PRODUCTS = [
  {
    product_id: "p-charcoal-60",
    sku: "HPV-60",
    category: "Pavers",
    name: "Charcoal Stone 60 mm",
    description: "The Charcoal paver, 60 mm, per square foot",
    aliases: ["charcoal paver", "charcoal stone"],
    base_uom: "sf",
    sold_by: "sf",
    list_cents: 400,
    branch_cost_cents: { [POINT_LOMA_ID]: 276 },
    floor_bps: 2500,
    featured: true,
  },
  {
    product_id: "p-polymer-sand",
    sku: "PSN-100",
    category: "Sand and base",
    name: "Polymeric Locking Sand",
    description: "Jointing sand for paver courses",
    aliases: ["polymer sand", "jointing sand"],
    base_uom: "bag",
    sold_by: "bag",
    list_cents: 1400,
    branch_cost_cents: { [POINT_LOMA_ID]: 980 },
    floor_bps: 1000,
    featured: true,
  },
  {
    product_id: "p-edge-8",
    sku: "EDG-08",
    category: "Edging",
    name: "Edge Restraint 8 ft",
    description: "Flexible paver edge restraint, eight foot strip",
    aliases: ["edge restraint", "edging"],
    base_uom: "pc",
    sold_by: "pc",
    list_cents: 3200,
    branch_cost_cents: { [POINT_LOMA_ID]: 2100 },
    floor_bps: 1200,
    featured: false,
  },
  {
    product_id: "p-wall-std",
    sku: "WAT-STD",
    category: "Walls",
    name: "Retaining Wall Stone, Standard",
    description: "Standard wall unit, per piece",
    aliases: ["wall stone", "retaining wall"],
    base_uom: "pc",
    sold_by: "pc",
    list_cents: 5200,
    branch_cost_cents: { [POINT_LOMA_ID]: 3400 },
    floor_bps: 1200,
    featured: true,
  },
  {
    product_id: "p-clear-stone",
    sku: "GRA-CL",
    category: "Sand and base",
    name: "Clear Stone",
    description: "Clear gravel base, per cubic yard",
    aliases: ["gravel", "clear gravel"],
    base_uom: "yd",
    sold_by: "yd",
    list_cents: 3800,
    branch_cost_cents: { [POINT_LOMA_ID]: 2500 },
    floor_bps: 1000,
    featured: false,
  },
];

const productById = new Map(PRODUCTS.map((p) => [p.product_id, p]));

// The account customers. The percent is the account's own pricing rule: the
// first contractor account's is the eighteen percent the plan's demo world
// names, and that rule is what puts Charcoal under the Pavers floor.
const CUSTOMERS = [
  {
    // the core's own uuid, the id the search answers and the pricing routes carry, so a
    // name learned at a customer pick answers for the customer the rules list names by id
    id: "c0000000-0000-4000-9000-000000000001",
    account_no: "C01",
    name: "Maple Ridge Contracting",
    customer_type: "CONTRACTOR",
    payment_terms_days: 30,
    credit_limit_cents: 500000,
    open_balance_cents: 125000,
    percent_off: 18,
  },
  {
    id: "c0000000-0000-4000-9000-000000000002",
    account_no: "C02",
    name: "Fenwick Builders Ltd",
    customer_type: "BUILDER",
    payment_terms_days: 30,
    credit_limit_cents: 300000,
    open_balance_cents: 0,
    percent_off: 10,
  },
  // The convert surface's two standings: an account sitting near its limit (the
  // gate refuses an order that carries it over) and a cash customer with no
  // credit account at all (terms are cash on delivery).
  {
    id: "c-granite",
    account_no: "C03",
    name: "Granite Overlimit Co",
    customer_type: "CONTRACTOR",
    payment_terms_days: 30,
    credit_limit_cents: 100000,
    open_balance_cents: 95000,
    percent_off: 0,
  },
  {
    id: "c-walkin",
    account_no: "C04",
    name: "Walkin Cash Customer",
    customer_type: "RETAIL",
    payment_terms_days: 0,
    credit_limit_cents: 0,
    open_balance_cents: 0,
    percent_off: 0,
  },
  {
    // The customer with no credit account: every conversion draws on credit
    // at the plain door, so for this account the QD-5 door is the whole
    // counter action (convert, invoice, pay in full).
    id: "c-quarry-lane",
    account_no: "C06",
    name: "Quarry Lane Paving",
    customer_type: "CONTRACTOR",
    payment_terms_days: 0,
    credit_limit_cents: 0,
    open_balance_cents: 0,
    percent_off: 0,
  },
  // The demo core's Cash On Demand account: zero-day terms WITH a credit line (the product's own rule is the terms, not the line).
  {
    id: "c-cod-terms",
    account_no: "C10",
    name: "Cash On Demand customer",
    customer_type: "CONTRACTOR",
    payment_terms_days: 0,
    credit_limit_cents: 500000,
    open_balance_cents: 0,
    percent_off: 0,
  },
  // An account with money past due that is not on hold: the standing read says past_due.
  {
    id: "c-pastdue",
    account_no: "C07",
    name: "Pastdue Paving Ltd",
    customer_type: "CONTRACTOR",
    payment_terms_days: 30,
    credit_limit_cents: 400000,
    open_balance_cents: 150000,
    percent_off: 0,
  },
  // An account inside its limit that sits on an active credit hold: the server's gate
  // refuses its order whatever the limit says (core/internal/till/holds.go).
  {
    id: "c-held",
    account_no: "C05",
    name: "Held Account Builders",
    customer_type: "CONTRACTOR",
    payment_terms_days: 30,
    credit_limit_cents: 900000,
    open_balance_cents: 0,
    percent_off: 0,
  },
];

// The account standing the counter reads for a customer: the past due and the hold flag.
// Every other customer answers the empty object, exactly as the contract's read does for
// a walk-in, a current account and a caller it will not tell.
const HELD_STANDINGS = new Map([
  ["c-held", { standing: "on_hold", past_due_cents: 12000, oldest_bucket: "60", on_hold: true }],
  ["c-pastdue", { standing: "past_due", past_due_cents: 45000, oldest_bucket: "30" }],
]);

const customerById = new Map([
  ...CUSTOMERS.map((c) => [c.id, c]),
  // the short labels the seeded quotes were written with stay resolvable
  ["c-maple-ridge", CUSTOMERS[0]],
  ["c-fenwick", CUSTOMERS[1]],
]);

const HST_RATE_BPS = 1300;

// ---------------------------------------------------------------------------
// The stub's own switches, held in memory.
// ---------------------------------------------------------------------------

// The cost and margin list switch: the plan's ledger row for the wrong-role
// proof. True (the shipped configuration: both demo roles sit on the list)
// answers the glance with figures; false answers the glance as the empty
// object, byte-absent, exactly as the core's marshal gate does for a caller
// the list refuses. A test flips this for a page and flips it back.
// The General Manager switch: neither demo login is the General Manager, and
// the dealer wide writes (the policy's default row, the acceptance switch)
// are that role's alone (CP0-5 answer A). A test flips this on to prove the
// permitted write, and the default proves the refusal.
// The floor raise: a test lifts every category floor by a whole number of
// basis points, so a discount that was inside the floor when it was set lands
// below it on a later quantity reprice (the qty arm of the typed refusal).
// approvalsShape: "page" is the contract's IdentityApprovalPage, "bare" is what
// the core's handler writes today (a bare array), "paged" is the page object
// split into pages of one row with next_cursor.
// The feedback route (the contract's staff-feedback create): the answer mode
// the suite flips, and the submissions seen, for the sender's assertions.
const feedbackFiled = [];
const feedbackMode = { value: "ok" };
const switches = { convertWall: false, callerOnCostList: true, callerIsGeneralManager: false, chipSetNull: false, grantAllTrue: false, floorRaiseBps: 0, approvalsShape: "page", quotePageSize: 0, paymentRoleRefused: false, paymentGhost: false, paymentKeyCrossed: false, paymentGrantRefused: false };

// Every write the stub sees, for the suite's layout save assertions. The
// ledger is test scaffolding: no contract path reads it.
const workspaceWrites = [];
const itemsWrites = [];
const policyWrites = [];
const decisions = [];
// The share links minted, by quote id (a list), and the request door's filed requests.
const shareLinks = new Map();
const floorRequests = new Map(); // `${quoteId}:${lineNo}` -> request row
// The conversions seen, for the suite's fulfilment assertion (scaffolding).
const conversions = [];
// The staff accepts and rejects on the customer's behalf (PR 247), as they arrived, and the replay store by key.
const staffDecisions = [];
const staffDecisionAnswers = new Map();
const decisionSwitches = { moved: false, failCount: 0, grantRefused: false };
// The order payments seen: the body and the Idempotency-Key header each carried, and the first answer per key.
const payments = [];
const paymentsByKey = new Map();
// Every post to the conversion and the payment door as it arrived, replays and refusals included:
// the conversions and payments lists above hold only what was taken.
const posts = [];
// A one-shot refusal of the payment door: the next `count` posts answer this and write nothing.
const doorRefusal = { count: 0, status: 409, error: "" };

// ---------------------------------------------------------------------------
// The QD-5 world: the orders a quote became, their invoices, and the payments
// the counter door took. The stub stands in for the core's one transaction:
// the conversion (for a customer with no credit), the invoice and the payment
// are written together here, and the door's refusals carry the core's own
// sentences so the panel's words are tested against them.
// ---------------------------------------------------------------------------

const orders = new Map(); // order id -> { id, order_no, quote_id, status, origin, accept_seq, total_cents, created_at }
const invoices = new Map(); // invoice id -> { id, invoice_no, order_id, quote_id, status, total_cents, balance_cents, source, created_at }
const orderPayments = new Map(); // namespaced key -> the stored answer (for a replay)
let orderCounter = 2000;
let invoiceCounter = 3000;
let paymentCounter = 4000;

/** The core's own money word (quote/credit.go): "$1,234.00", a minus when negative. */
function coreMoney(cents) {
  const neg = cents < 0;
  const abs = Math.abs(cents);
  const whole = String(Math.floor(abs / 100)).replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  return `${neg ? "-" : ""}$${whole}.${String(abs % 100).padStart(2, "0")}`;
}

/** The orders a quote became, in the order they were minted. */
function ordersOf(quoteId) {
  return [...orders.values()].filter((o) => o.quote_id === quoteId);
}

/** The live invoices of one order. */
function invoicesOf(orderId) {
  return [...invoices.values()].filter((i) => i.order_id === orderId && i.status !== "VOID");
}

function newOrder(quote, origin) {
  orderCounter += 1;
  const order = {
    id: `o-${orderCounter}`,
    order_no: `SO-${String(orderCounter).padStart(4, "0")}`,
    quote_id: quote.id,
    status: "DRAFT",
    origin,
    accept_seq: ordersOf(quote.id).length + 1,
    total_cents: quote.total_cents,
    created_at: new Date().toISOString(),
  };
  orders.set(order.id, order);
  return order;
}

/** Raise the order's agreed invoice at the whole order key: the document when it exists, else what would be raised. */
function agreedInvoiceOf(order) {
  const live = invoicesOf(order.id);
  return live.length > 0 ? live[0] : null;
}

function raiseAgreedInvoice(order, source = "COUNTER") {
  const existing = agreedInvoiceOf(order);
  if (existing !== null) return existing;
  invoiceCounter += 1;
  const invoice = {
    id: `inv-${invoiceCounter}`,
    invoice_no: `INV-${String(invoiceCounter).padStart(4, "0")}`,
    order_id: order.id,
    quote_id: order.quote_id,
    status: "FINALIZED",
    total_cents: order.total_cents,
    balance_cents: order.total_cents,
    source,
    created_at: new Date().toISOString(),
  };
  invoices.set(invoice.id, invoice);
  return invoice;
}

/** The payment key the core namespaces by quote, so a key used on one quote can never replay another's. */
function paymentKeyOf(quoteId, key) {
  return `quote-order-payment:${quoteId}:${key}`;
}

// ---------------------------------------------------------------------------
// The third coder's panels' world: the assist key switch, the follow-up
// plans and events, the soft holds, the branch stock, and the approval cards
// the floor request door files. Every figure is the stub's own, standing in
// for the core; nothing in the app imports any of it.
// ---------------------------------------------------------------------------

const JULIAN_ID = "6f1c2b3a-0000-4000-8000-000000000002";
const seamSwitches = { aiConfigured: false, seamWired: true };
const followPlans = new Map(); // quote id -> plan row
const followEvents = new Map(); // quote id -> events
const lapsedHolds = new Map(); // quote id -> expired hold rows
const approvalCards = new Map(); // request id -> the card the server wrote
const stockByProduct = new Map(); // product id -> { point-loma, julian }
const productApps = {
  "p-charcoal-60": ["PAVER"],
  "p-polymer-sand": ["JOINTING"],
  "p-edge-8": ["EDGING"],
  "p-wall-std": ["WALL"],
  "p-clear-stone": ["BASE"],
};
function resetSeamWorld() {
  seamSwitches.aiConfigured = false;
  seamSwitches.seamWired = true;
  followPlans.clear();
  followEvents.clear();
  lapsedHolds.clear();
  approvalCards.clear();
  stockByProduct.clear();
  for (const product of PRODUCTS) stockByProduct.set(product.product_id, { pointLoma: 400, julian: 400 });
  // One short product on purpose: the seeded accepted quote asks for three of it.
  stockByProduct.set("p-clear-stone", { pointLoma: 2, julian: 60 });
}
resetSeamWorld();

function branchStockRow(productId, branchId, caller) {
  const stock = stockByProduct.get(productId) ?? { pointLoma: 0, julian: 0 };
  const here = branchId === POINT_LOMA_ID;
  const available = here ? stock.pointLoma : stock.julian;
  const product = productById.get(productId);
  return {
    product_id: productId,
    branch_id: branchId,
    branch_code: here ? "PLM" : "JUL",
    branch_name: here ? "Point Loma" : "Julian",
    on_hand_base: available,
    reserved_base: 0,
    in_transit_base: 0,
    available_base: available,
    soft_held_base: 0,
    ...(switches.callerOnCostList && product !== undefined ? { avg_cost_cents: product.branch_cost_cents[POINT_LOMA_ID] ?? 0 } : {}),
  };
}

function gradeOf(available, qty) {
  if (!seamSwitches.seamWired) return "UNKNOWN";
  if (available <= 0) return "NONE";
  return available >= qty ? "GREEN" : "THIN";
}

function followUpView(quote) {
  const plan = followPlans.get(quote.id) ?? (quote.status === "SENT" ? ensurePlan(quote) : undefined);
  const events = followEvents.get(quote.id) ?? [];
  const upcoming = [];
  if (plan !== undefined && plan.enabled) {
    for (const day of plan.offsets_days) {
      if (events.some((event) => event.step_day === day)) continue;
      upcoming.push({
        step_day: day,
        channel: plan.channel,
        due_at: new Date(Date.parse(plan.started_at) + day * 86400000).toISOString(),
        scheduled: true,
      });
    }
  }
  return { quote_id: quote.id, ...(plan === undefined ? {} : { plan }), events, upcoming, automated: plan !== undefined && plan.enabled };
}

function ensurePlan(quote) {
  const plan = { quote_id: quote.id, enabled: true, channel: "EMAIL", offsets_days: [3, 7, 14], started_at: new Date().toISOString() };
  followPlans.set(quote.id, plan);
  return plan;
}

function holdExpiryView(quote) {
  const expired = lapsedHolds.get(quote.id) ?? [];
  const lapsedIds = new Set(expired.map((row) => row.product_id));
  const active = quote.lines.filter((line) => !lapsedIds.has(line.product_id)).length;
  return { expiry: { quote_id: quote.id, active_count: active, expired, any_expired: expired.length > 0 }, seam_wired: seamSwitches.seamWired };
}

function lineAppsOf(line) {
  return productApps[line.product_id] ?? [];
}

function scopeGapsOf(quote) {
  const findings = [];
  const have = new Set(quote.lines.flatMap(lineAppsOf));
  for (const rule of scopeRules) {
    if (!rule.enabled) continue;
    const triggered = quote.lines.filter((line) => lineAppsOf(line).some((app) => rule.when_apps.includes(app)));
    if (triggered.length === 0) continue;
    if (rule.expect_apps.some((app) => have.has(app))) continue;
    findings.push({
      rule_code: rule.code,
      title: rule.title,
      advice: rule.advice,
      triggered_by: triggered.map((line) => ({ line_no: line.line_no, name: line.description })),
      missing: rule.expect_apps.map((app) => app.toLowerCase()),
      suggest_query: rule.suggest_query,
    });
  }
  return { quote_id: quote.id, findings, lines_checked: quote.lines.length, rules_evaluated: scopeRules.filter((r) => r.enabled).length };
}

// The plain parser: one segment per line or comma, a product by alias, name or
// sku, a quantity by the first number, a unit by the word after it.
function extractOf(text, useAi) {
  const proposals = [];
  const unmatched = [];
  const segments = text.split(/[\n,;]+/).map((segment) => segment.trim()).filter((segment) => segment !== "");
  segments.forEach((segment, index) => {
    const lowered = segment.toLowerCase();
    const product = PRODUCTS.find(
      (p) => lowered.includes(p.name.toLowerCase()) || lowered.includes(p.sku.toLowerCase()) || p.aliases.some((alias) => lowered.includes(alias)),
    );
    if (product === undefined) {
      unmatched.push({ source_text: segment, reason: "No product in the catalogue matches this text." });
      return;
    }
    const numberMatch = /(\d+(?:\.\d+)?)\s*([a-z]+)?/.exec(lowered);
    const qty = numberMatch === null ? "" : numberMatch[1];
    const unitWord = numberMatch === null || numberMatch[2] === undefined ? "" : numberMatch[2];
    const uom = unitWord === product.sold_by ? product.sold_by : "";
    const alternates = PRODUCTS.filter((p) => p.product_id !== product.product_id && p.category === product.category).map((p) => ({
      product_id: p.product_id,
      sku: p.sku,
      name: p.name,
      match_reason: "SAME_CATEGORY",
      sell_uom: p.sold_by,
      base_uom: p.base_uom,
    }));
    proposals.push({
      ref: `r${index + 1}`,
      source_text: segment,
      product_id: product.product_id,
      sku: product.sku,
      name: product.name,
      sell_uom: product.sold_by,
      base_uom: product.base_uom,
      qty_entered: qty,
      uom_entered: uom,
      confidence: qty !== "" && uom !== "" ? "HIGH" : "MEDIUM",
      match_reason: "ALIAS",
      why: ["Matched by its name in your text."],
      needs_qty: qty === "",
      needs_uom: uom === "",
      alternates,
    });
  });
  const aiRan = useAi && seamSwitches.aiConfigured;
  return {
    proposals,
    unmatched,
    source: aiRan ? "ai-assisted" : "deterministic",
    ...(useAi && !seamSwitches.aiConfigured
      ? { ai_note: "AI-assisted reading isn't switched on here, so I used the plain parser." }
      : {}),
  };
}


// ---------------------------------------------------------------------------
// The pricing and floor rules, in the plan's own rows (section 3.3). All
// arithmetic here is integer cents, server-stated.
// ---------------------------------------------------------------------------

function ruleUnitCents(product, customer) {
  const percent = customer ? customer.percent_off : 0;
  return Math.round((product.list_cents * (100 - percent)) / 100);
}

function netUnitCents(ruleCents, discPct) {
  return Math.round((ruleCents * (100 - discPct)) / 100);
}

function marginBps(unitCents, costCents) {
  if (unitCents <= 0) return 0;
  return Math.trunc(((unitCents - costCents) * 10000) / unitCents);
}

// The floor decision on a discount write. Returns null when the write goes
// through, or the typed refusal body when it does not. The rows: a Work
// caller that does not clear the floor is refused with the approvers named
// (the General Manager, the Financial Controller and, since CP0-1 answer B,
// the branch's Sales manager); a Manage caller (the Sales manager) may set
// the price itself, so the same write goes through for it and the clearing is
// stamped on the line.
function floorDecision(caller, product, customer, discPct) {
  const ruleCents = ruleUnitCents(product, customer);
  const unitCents = netUnitCents(ruleCents, discPct);
  const costCents = product.branch_cost_cents[POINT_LOMA_ID] ?? 0;
  const margin = marginBps(unitCents, costCents);
  const floorBps = product.floor_bps + switches.floorRaiseBps;
  if (margin >= floorBps) return null;
  const isManager = caller.account.role_code === "sales_manager";
  if (isManager) return { cleared: true, by: USERS[caller.flow].user_id };
  return {
    cleared: false,
    body: {
      policy: "category_margin_floor",
      approver_roles: ["General Manager", "Financial Controller", "Sales Manager"],
      manager_may_set: true,
      ...(switches.callerOnCostList ? { margin_bps: margin, floor_bps: floorBps } : {}),
    },
  };
}

// ---------------------------------------------------------------------------
// The quote world. Every figure below is computed here from the catalogue:
// the stub stands in for the core, which is the only place money is computed.
// ---------------------------------------------------------------------------

const editableByStatus = {
  DRAFT: ["line_qty", "line_add", "line_remove", "valid_until"],
  SENT: ["line_qty", "line_add", "line_remove", "valid_until"],
};

function newLine(product, customer, qty, opts = {}) {
  const discPct = opts.discPct ?? 0;
  const ruleCents = ruleUnitCents(product, customer);
  const unitCents = discPct > 0 ? netUnitCents(ruleCents, discPct) : ruleCents;
  const costCents = product.branch_cost_cents[POINT_LOMA_ID] ?? 0;
  const qtyNumber = Number(qty);
  return {
    line_no: opts.lineNo,
    product_id: product.product_id,
    description: product.name,
    qty_entered: String(qty),
    uom_entered: product.sold_by,
    qty_base: String(qty),
    unit_price_cents: unitCents,
    price_uom: product.sold_by,
    line_total_cents: Math.round(unitCents * qtyNumber),
    disc_pct: discPct,
    pre_discount_total_cents: Math.round(ruleCents * qtyNumber),
    rule_price_below_floor: marginBps(ruleCents, costCents) < product.floor_bps,
    floor_cleared_by: opts.floorClearedBy ?? null,
  };
}

function totalsOf(quote) {
  let subtotal = 0;
  for (const line of quote.lines) subtotal += line.line_total_cents;
  const base = subtotal + quote.terms.delivery_fee_cents;
  const tax = Math.round((base * HST_RATE_BPS) / 10000);
  return { subtotal_cents: subtotal, tax_cents: tax, total_cents: base + tax };
}

let quoteCounter = 1000;
const quotes = new Map(); // id -> quote row

function seedQuote(status, customerId, ownerFlow, lines, terms = {}, fixed = null) {
  if (fixed === null) quoteCounter += 1;
  const id = fixed !== null ? fixed.id : `q-${quoteCounter}`;
  const customer = customerById.get(customerId);
  const quote = {
    id,
    quote_no: fixed !== null ? fixed.quoteNo : `Q-${quoteCounter}`,
    // stored under the id the search answers, so the workarea's account match hits
    // whichever form (core uuid or short label) the caller seeded with
    customer_id: customer.id,
    customer_name: customer.name,
    branch_id: POINT_LOMA_ID,
    status,
    valid_until: "",
    expired: false,
    created_at: new Date().toISOString(),
    owner_id: USERS[ownerFlow].user_id,
    owner_name: USERS[ownerFlow].display_name,
    owner_flow: ownerFlow,
    lines: lines.map((spec, index) => newLine(productById.get(spec.p), customer, spec.q, { lineNo: index + 1, discPct: spec.d ?? 0 })),
    terms: {
      quote_id: id,
      deposit_required: false,
      deposit_pct: 0,
      include_images: true,
      customer_notes: "",
      contact_name: "",
      project_job: "",
      customer_po: "",
      salesperson_id: "",
      salesperson_name: "",
      fulfillment: "PICKUP",
      delivery_address: "",
      requested_date: "",
      delivery_fee_cents: 0,
      ...terms,
    },
  };
  const totals = totalsOf(quote);
  quote.subtotal_cents = totals.subtotal_cents;
  quote.tax_cents = totals.tax_cents;
  quote.total_cents = totals.total_cents;
  quotes.set(id, quote);
  return quote;
}

function seedWorld() {
  quoteCounter = 1000;
  quotes.clear();
  orders.clear();
  invoices.clear();
  orderPayments.clear();
  orderCounter = 2000;
  invoiceCounter = 3000;
  paymentCounter = 4000;
  seedQuote("DRAFT", "c-maple-ridge", "sales", [
    { p: "p-charcoal-60", q: 120 },
    { p: "p-polymer-sand", q: 2 },
  ]);
  seedQuote("SENT", "c-fenwick", "sales-manager", [{ p: "p-wall-std", q: 10 }]);
  seedQuote("ACCEPTED", "c-maple-ridge", "sales", [{ p: "p-clear-stone", q: 3 }]);
  // The QD-5 world, seeded on fixed ids past the dynamic range so a test's
  // own seeded quotes keep their numbers (the first dynamic seed stays 1004):
  // a credit customer's converted quote with its order (the deposit path),
  // and a cash customer's accepted quote (the one action).
  const converted = seedQuote("CONVERTED", "c-maple-ridge", "sales", [{ p: "p-charcoal-60", q: 10 }], {}, { id: "q-2101", quoteNo: "Q-2101" });
  newOrder(converted, "COUNTER");
  seedQuote("ACCEPTED", "c-quarry-lane", "sales", [{ p: "p-polymer-sand", q: 4 }], {}, { id: "q-2102", quoteNo: "Q-2102" });
}
seedWorld();

// The core does not renumber after a removal (DeleteLineGuarded): a removed
// line leaves a gap and a new line takes the next number past the highest.
function nextLineNo(quote) {
  return quote.lines.reduce((high, line) => Math.max(high, line.line_no), 0) + 1;
}

function restamp(quote) {
  const totals = totalsOf(quote);
  quote.subtotal_cents = totals.subtotal_cents;
  quote.tax_cents = totals.tax_cents;
  quote.total_cents = totals.total_cents;
}

// The change trail. Every guarded write appends its column rows under one
// request id, oldest first (the audit sequence), and the actor is the caller's
// display name. Timestamps are taken from the clock at write time, so no
// calendar value is written down here.
let auditSeq = 0;
function audit(quote, caller, rows) {
  auditSeq += 1;
  const requestId = `req-${auditSeq}`;
  const at = new Date(Date.now() + auditSeq * 1000).toISOString();
  quote.history = quote.history ?? [];
  for (const row of rows) {
    quote.history.push({
      field: row.field,
      line_no: row.line_no ?? 0,
      ...(row.before === undefined ? {} : { before: String(row.before) }),
      ...(row.after === undefined ? {} : { after: String(row.after) }),
      actor_id: caller.account.user_id,
      actor_name: caller.account.display_name,
      request_id: requestId,
      occurred_at: at,
    });
  }
}

// The money columns every line write moves, as history rows.
function moneyRows(quote, before, line, lineNo) {
  const rows = [];
  if (line !== null && before.unit !== line.unit_price_cents) rows.push({ field: "unit_price_cents", line_no: lineNo, before: before.unit, after: line.unit_price_cents });
  if (line !== null && before.lineTotal !== line.line_total_cents) rows.push({ field: "line_total_cents", line_no: lineNo, before: before.lineTotal, after: line.line_total_cents });
  rows.push({ field: "subtotal_cents", line_no: 0, before: before.subtotal, after: quote.subtotal_cents });
  rows.push({ field: "total_cents", line_no: 0, before: before.total, after: quote.total_cents });
  return rows;
}

function snapshotOf(quote, line) {
  return {
    unit: line === undefined || line === null ? undefined : line.unit_price_cents,
    lineTotal: line === undefined || line === null ? undefined : line.line_total_cents,
    subtotal: quote.subtotal_cents,
    total: quote.total_cents,
  };
}

// The view the read answers. editable_fields follows the QD-1 offer: a Work
// caller that does not own the quote is offered nothing, whatever its status;
// a Manage caller (the Sales manager) is offered the edit on any quote in its
// branch.
function viewOf(caller, quote) {
  const isOwner = quote.owner_id === USERS[caller.flow].user_id;
  const isManager = caller.account.role_code === "sales_manager";
  const editable = (editableByStatus[quote.status] ?? []).length > 0 && (isOwner || isManager)
    ? editableByStatus[quote.status]
    : [];
  return {
    id: quote.id,
    quote_no: quote.quote_no,
    customer_id: quote.customer_id,
    customer_name: quote.customer_name,
    branch_id: quote.branch_id,
    status: quote.status,
    valid_until: quote.valid_until === "" ? undefined : quote.valid_until,
    expired: quote.expired,
    editable_fields: editable,
    // The core's own offer (staff.go): ACCEPTED always, and CONVERTED again only where the dealer's
    // multi accept switch is on. The desk's convert surface still refuses a converted quote on its own
    // (PR 296 round 4): no fresh key is minted for a quote whose conversion may already stand.
    can_convert: quote.status === "ACCEPTED" || (quote.status === "CONVERTED" && acceptance.allow_multi_accept),
    // PR 247: the offer is the server's; a role without the grant to decide is offered neither.
    can_accept: (quote.status === "DRAFT" || quote.status === "SENT") && !quote.expired && !decisionSwitches.grantRefused,
    can_reject: (quote.status === "DRAFT" || quote.status === "SENT") && !quote.expired && !decisionSwitches.grantRefused,
    quantity_notice: "All quantities are to be confirmed by the customer. Please review them before you accept it.",
    acceptances: quote.staffAcceptances ?? [],
    ...(quote.declined === undefined ? {} : { declined: quote.declined }),
    owner_id: quote.owner_id,
    owner_name: quote.owner_name,
    lines: quote.lines.map((line) => ({
      line_no: line.line_no,
      product_id: line.product_id,
      description: line.description,
      qty_entered: line.qty_entered,
      uom_entered: line.uom_entered,
      qty_base: line.qty_base,
      unit_price_cents: line.unit_price_cents,
      price_uom: line.price_uom,
      line_total_cents: line.line_total_cents,
      ...(line.rule_price_below_floor ? { rule_price_below_floor: true } : {}),
      ...(line.floor_cleared_by !== null ? { floor_cleared_by: line.floor_cleared_by } : {}),
    })),
    subtotal_cents: quote.subtotal_cents,
    tax_cents: quote.tax_cents,
    total_cents: quote.total_cents,
    updated_at: "2026-01-01T00:00:00Z",
  };
}

function termsOf(quote) {
  return {
    quote_id: quote.id,
    deposit_required: quote.terms.deposit_required,
    deposit_pct: quote.terms.deposit_pct,
    include_images: quote.terms.include_images,
    customer_notes: quote.terms.customer_notes,
    contact_name: quote.terms.contact_name,
    project_job: quote.terms.project_job,
    customer_po: quote.terms.customer_po,
    salesperson_id: quote.terms.salesperson_id === "" ? undefined : quote.terms.salesperson_id,
    salesperson_name: quote.terms.salesperson_name === "" ? undefined : quote.terms.salesperson_name,
    fulfillment: quote.terms.fulfillment,
    delivery_address: quote.terms.delivery_address,
    requested_date: quote.terms.requested_date === "" ? undefined : quote.terms.requested_date,
    delivery_fee_cents: quote.terms.delivery_fee_cents,
    deposit_cents: quote.terms.deposit_required
      ? Math.round((quote.total_cents * quote.terms.deposit_pct) / 100)
      : 0,
    subtotal_cents: quote.subtotal_cents,
    tax_cents: quote.tax_cents,
    total_cents: quote.total_cents,
    lines: quote.lines.map((line) => ({
      line_no: line.line_no,
      disc_pct: line.disc_pct,
      pre_discount_total_cents: line.pre_discount_total_cents,
      has_image: false,
      sku: productById.get(line.product_id)?.sku,
    })),
  };
}

// The margin glance: figures for a caller on the cost list, the empty object
// byte-absent for one off it. The marshal gate is the plan's own D8 row.
function glanceOf(quote) {
  if (!switches.callerOnCostList) return {};
  let revenue = 0;
  let cost = 0;
  for (const line of quote.lines) {
    revenue += line.line_total_cents;
    cost += Math.round((productById.get(line.product_id)?.branch_cost_cents[POINT_LOMA_ID] ?? 0) * Number(line.qty_entered));
  }
  const partial = quote.lines.some((line) => {
    const product = productById.get(line.product_id);
    return product === undefined || product.branch_cost_cents[POINT_LOMA_ID] === undefined;
  });
  return {
    lines: quote.lines.map((line) => {
      const unitCost = productById.get(line.product_id)?.branch_cost_cents[POINT_LOMA_ID];
      const lineCost = unitCost === undefined ? undefined : Math.round(unitCost * Number(line.qty_entered));
      return {
        line_no: line.line_no,
        ...(lineCost === undefined ? {} : { cost_cents: lineCost }),
        ...(lineCost === undefined ? {} : { margin_bps: marginBps(line.line_total_cents, lineCost) }),
        ...(lineCost === undefined ? {} : { est_profit_cents: line.line_total_cents - lineCost }),
      };
    }),
    margin_bps: revenue > 0 ? marginBps(revenue, cost) : 0,
    est_profit_cents: revenue - cost,
    ...(partial ? { partial: true } : {}),
  };
}

// ---------------------------------------------------------------------------
// The surface settings kit's rows: the config read and the per-person
// workspace and curation writes.
// ---------------------------------------------------------------------------

const SURFACE_ID = "quotes.builder";

const POLICY = {
  cost_visibility: "ALL_STAFF",
  default_validity_days: 30,
  default_deposit_pct: 10,
  line_discounts_enabled: true,
  send_email_enabled: true,
  send_sms_enabled: true,
  include_product_images: true,
  quick_chip_set: ["Pavers", "Sand and base", "Walls"],
  effective_chips: ["Pavers", "Sand and base", "Walls"],
  quantity_notice: "All quantities are to be confirmed by the customer. Please review them before you accept it.",
  workspace_split_pct: 58,
  workspace_arrangement: "PRIMARY_LEFT",
};

const workspaces = new Map(); // user_id -> saved row
const myItems = new Map(); // user_id -> items array

// The typed quote policy: the dealer default row ("") and one row per branch.
// A branch row, when one exists, wins over the dealer default for that branch.
const policyRows = new Map();
function resetPolicy() {
  policyRows.clear();
  policyRows.set("", { ...POLICY, quick_chip_set: [...POLICY.quick_chip_set] });
}
function effectivePolicy(branchId) {
  const row = policyRows.get(branchId) ?? policyRows.get("");
  const { branch_id: _drop, ...rest } = row;
  const effective = rest.quick_chip_set.length > 0 ? [...rest.quick_chip_set] : ["Pavers"];
  // The core before round 14 (a) answered null for a scope whose chip set was never authored (a nil slice):
  // the switch reproduces that body so the policy read of that core stays readable.
  return { ...rest, quick_chip_set: switches.chipSetNull ? null : rest.quick_chip_set, effective_chips: effective };
}

// The dealer's one acceptance switch, and the scope rule table (seven rules,
// the reference's own set, wording fixed: only the switch is writable).
const acceptance = { allow_multi_accept: false, switch_wired: true };
/** The orders made under a convert Idempotency-Key, by quote and key (the core keeps them on the conversion's audit row). */
const convertKeys = new Map();
const SCOPE_RULES = [
  ["SURFACE_NEEDS_BASE", "Granular base for the paved area", ["PAVER", "PATIO"], ["BASE"]],
  ["SURFACE_NEEDS_BEDDING", "Bedding course under the units", ["PAVER", "PATIO"], ["BEDDING"]],
  ["SURFACE_NEEDS_JOINTING", "Jointing sand for the paved area", ["PAVER", "PATIO"], ["JOINTING"]],
  ["SURFACE_NEEDS_EDGING", "Edge restraint around the perimeter", ["PAVER", "PATIO"], ["EDGING", "RESTRAINT"]],
  ["WALL_NEEDS_BASE", "Levelling pad under the wall", ["WALL"], ["BASE"]],
  ["WALL_NEEDS_DRAINAGE", "Drainage stone behind the wall", ["WALL"], ["DRAINAGE"]],
  ["CAPS_NEED_ADHESIVE", "Adhesive for the caps and steps", ["COPING", "STEP"], ["ADHESIVE"]],
];
let scopeRules = [];
function resetScopeRules() {
  scopeRules = SCOPE_RULES.map(([code, title, whenApps, expectApps], index) => ({
    code,
    title,
    advice: `${title}: the yard's own installation advice.`,
    when_apps: whenApps,
    expect_apps: expectApps,
    suggest_query: expectApps[0].toLowerCase(),
    enabled: true,
    sort_order: index + 1,
  }));
}
resetScopeRules();
resetPolicy();

// The dealer's curation rows per role profile.
const roleItemsByProfile = new Map();

// The caller's own profile word, and whether it may administer the settings.
function profileOf(caller) {
  return caller.account.role_code === "sales_manager" ? "admin" : "counter";
}
function isGeneralManager(caller) {
  return switches.callerIsGeneralManager && caller.account.role_code === "sales_manager";
}
// isSettingsAdmin: whether the caller holds the quote-desk Manage level (LevelManage on quote-desk
// surface) -- the Sales manager. Used for acceptance read and policy write gates.
function isSettingsAdmin(caller) {
  return caller.account.role_code === "sales_manager";
}
// isAdmin: whether the caller is a Tier-1 GLOBAL user -- the General Manager or the platform
// SUPER_ADMIN. Used for the scope rules gate (IsAdmin predicate). The switch covers the
// sign-in-without-a-real-role-code that the GM test uses.
function isAdmin(caller) {
  return (
    switches.callerIsGeneralManager ||
    caller.account.role_code === "general_manager" ||
    caller.account.role_code === "super_admin"
  );
}

function workspaceRow(userId, defaults) {
  const saved = workspaces.get(userId);
  if (saved) return { ...saved, source: "user" };
  return {
    split_pct: defaults.workspace_split_pct,
    arrangement: defaults.workspace_arrangement,
    primary_collapsed: false,
    secondary_collapsed: false,
    cost_hidden: false,
    open_ref: "",
    source: "dealer",
  };
}

// ---------------------------------------------------------------------------
// The mint, the auth read, and the response helpers.
// ---------------------------------------------------------------------------

function base64UrlJson(value) {
  return Buffer.from(JSON.stringify(value), "utf8").toString("base64url");
}

function mintToken(flow) {
  const issuedAtSeconds = Math.floor(Date.now() / 1000);
  const header = { alg: "ES256", typ: "JWT" };
  const payload = {
    iss: "hh-demo-stub",
    sub: USERS[flow].user_id,
    aud: "staff",
    iat: issuedAtSeconds,
    exp: issuedAtSeconds + 60 * 60,
  };
  return `${base64UrlJson(header)}.${base64UrlJson(payload)}.c3R1Yi1zaWduYXR1cmUtYnl0ZXM`;
}

// The auth read the staff routes answer behind, the way the core's middleware
// does: the bearer's own sub claim names the account, the account's grant
// names the branch, and the demo org header rides beside the bearer.
function callerOf(request) {
  const header = request.headers.authorization ?? "";
  if (!header.startsWith("Bearer ")) return { refused: 401, error: "no bearer on a staff route" };
  const parts = header.slice("Bearer ".length).split(".");
  if (parts.length !== 3) return { refused: 401, error: "the bearer is not a readable token" };
  let payload;
  try {
    payload = JSON.parse(Buffer.from(parts[1], "base64url").toString("utf8"));
  } catch {
    return { refused: 401, error: "the bearer's claims are not readable" };
  }
  const userId = payload?.sub;
  const branchId = ACCOUNT_BRANCH[userId];
  if (typeof userId !== "string" || branchId === undefined) {
    return { refused: 401, error: "the bearer names no demo account" };
  }
  const org = request.headers["x-org"];
  if (org !== "hh-demo") return { refused: 400, error: "X-Org is missing or not the demo org" };
  const flow = Object.keys(USERS).find((name) => USERS[name].user_id === userId);
  return { flow, account: USERS[flow], branchId };
}

function respondJson(response, status, payload, extraHeaders = {}) {
  response.writeHead(status, { "content-type": "application/json", ...extraHeaders });
  response.end(JSON.stringify(payload));
}

// The credit gate's words, as the core's CreditRefusalMessage writes them (credit.go).
function dollars(cents) {
  const whole = String(Math.floor(cents / 100)).replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  return `$${whole}.${String(cents % 100).padStart(2, "0")}`;
}

function creditRefusal(quote) {
  const buyer = customerById.get(quote.customer_id);
  if (buyer === undefined) return null;
  const tail = "the quote is unchanged and no order was created; accounts receivable can release or override the hold";
  if (HELD_STANDINGS.get(buyer.id)?.on_hold === true) {
    return `this conversion was refused by a credit policy (customer is on an active credit hold), ${tail}`;
  }
  if (buyer.credit_limit_cents <= 0) {
    return `this customer cannot buy on account right now (account has no credit; cash or card only (COD)), ${tail}`;
  }
  if (buyer.open_balance_cents + quote.total_cents > buyer.credit_limit_cents) {
    const over = buyer.open_balance_cents + quote.total_cents - buyer.credit_limit_cents;
    return `this order is ${dollars(quote.total_cents)} against a ${dollars(buyer.credit_limit_cents)} credit limit with ${dollars(buyer.open_balance_cents)} already owing (order would put the balance ${over} cents over the ${buyer.credit_limit_cents} cents limit), ${tail}`;
  }
  return null;
}

function readBody(request, response, next) {
  let body = "";
  request.on("data", (chunk) => {
    body += chunk;
  });
  request.on("end", () => {
    if (body === "") {
      next({});
      return;
    }
    try {
      next(JSON.parse(body));
    } catch {
      respondJson(response, 400, { error: "the request body is not JSON" });
    }
  });
}

function quoteOr404(id) {
  const quote = quotes.get(id);
  if (quote === undefined) return null;
  return quote;
}

// The edit result the line writes answer: the line snapshot shape the
// contract's QuoteEditResult carries, plus the recomputed totals.
function editResult(quote, line) {
  return {
    ...(line === null ? {} : { line: { LineNo: line.line_no, ProductID: line.product_id, QtyEntered: line.qty_entered, QtyBase: line.qty_base, UnitPriceCents: line.unit_price_cents, LineTotalCents: line.line_total_cents } }),
    totals: { SubtotalCents: quote.subtotal_cents, TotalCents: quote.total_cents },
    status: quote.status,
  };
}

// ---------------------------------------------------------------------------
// The server.
// ---------------------------------------------------------------------------

const server = createServer(async (request, response) => {
  const url = new URL(request.url ?? "/", `http://127.0.0.1:${port}`);
  const path = url.pathname;

  if (path === "/healthz") {
    response.writeHead(200, { "content-type": "text/plain" });
    response.end("ok");
    return;
  }

  // The suite's peek routes: the ledgers the layout save assertions read, and
  // the cost list switch the wrong-role page flips. Test scaffolding; no
  // contract path shares the namespace.
  if (path === "/_stub/state") {
    respondJson(response, 200, { workspaceWrites, itemsWrites, policyWrites, decisions, staffDecisions, conversions, payments, posts, switches: { ...switches } });
    return;
  }
  // Test scaffolding: one more quote with the given status, customer, lines and terms, so a race probe
  // does not have to build its quotes through the screen.
  if (path === "/_stub/seed-quote" && request.method === "POST") {
    readBody(request, response, (body) => {
      const lines = body.empty === true ? [] : Array.isArray(body.lines) && body.lines.length > 0 ? body.lines : [{ p: "p-wall-std", q: 4 }];
      const made = seedQuote(typeof body.status === "string" ? body.status : "DRAFT", String(body.customer_id ?? "c-fenwick"), String(body.owner_flow ?? "sales"), lines, body.terms ?? {});
      respondJson(response, 200, { id: made.id, quote_no: made.quote_no });
    });
    return;
  }
  // The role wall: a role curation that withholds the convert action from the caller's profile.
  if (path === "/_stub/convert-wall" && request.method === "PUT") {
    readBody(request, response, (body) => {
      switches.convertWall = body.on === true;
      respondJson(response, 200, { convertWall: switches.convertWall });
    });
    return;
  }
  // The dealer's multi accept switch (the core's allow_quote_multi_accept): on, a converted quote converts again.
  if (path === "/_stub/multi-accept" && request.method === "PUT") {
    readBody(request, response, (body) => {
      acceptance.allow_multi_accept = body.on === true;
      respondJson(response, 200, { allow_multi_accept: acceptance.allow_multi_accept });
    });
    return;
  }
  // The payment door refuses its next `count` posts with this answer and writes nothing.
  if (path === "/_stub/door-refusal" && request.method === "PUT") {
    readBody(request, response, (body) => {
      doorRefusal.count = Number(body.count ?? 1);
      doorRefusal.status = Number(body.status ?? 409);
      doorRefusal.error = String(body.error ?? "");
      respondJson(response, 200, { ...doorRefusal });
    });
    return;
  }
  if (path === "/_stub/decision-moved" && request.method === "PUT") {
    readBody(request, response, (body) => {
      decisionSwitches.moved = body.moved === true;
      respondJson(response, 200, { ...decisionSwitches });
    });
    return;
  }
  if (path === "/_stub/decision-grant" && request.method === "PUT") {
    readBody(request, response, (body) => {
      decisionSwitches.grantRefused = body.refused === true;
      respondJson(response, 200, { ...decisionSwitches });
    });
    return;
  }
  if (path === "/_stub/decision-fail" && request.method === "PUT") {
    readBody(request, response, (body) => {
      decisionSwitches.failCount = Number(body.count ?? 1);
      respondJson(response, 200, { ...decisionSwitches });
    });
    return;
  }
  if (path === "/_stub/grant-all-true" && request.method === "PUT") {
    readBody(request, response, (body) => {
      switches.grantAllTrue = body.on === true;
      respondJson(response, 200, { on: switches.grantAllTrue });
    });
    return;
  }
  if (path === "/_stub/chip-set-null" && request.method === "PUT") {
    readBody(request, response, (body) => {
      switches.chipSetNull = body.on === true;
      respondJson(response, 200, { on: switches.chipSetNull });
    });
    return;
  }
  if (path === "/_stub/general-manager" && request.method === "PUT") {
    readBody(request, response, (body) => {
      switches.callerIsGeneralManager = body.member === true;
      respondJson(response, 200, { member: switches.callerIsGeneralManager });
    });
    return;
  }
  if (path === "/_stub/approvals-shape" && request.method === "PUT") {
    readBody(request, response, (body) => {
      switches.approvalsShape = ["bare", "paged"].includes(body.shape) ? body.shape : "page";
      respondJson(response, 200, { shape: switches.approvalsShape });
    });
    return;
  }
  // File a floor request on a line as the user of a named flow, so a list can
  // hold rows the caller filed and rows somebody else did.
  if (path === "/_stub/plant-approval" && request.method === "POST") {
    readBody(request, response, (body) => {
      const quote = quoteOr404(String(body.quote_id));
      const lineNo = Number(body.line_no);
      const line = quote === null ? undefined : quote.lines.find((row) => row.line_no === lineNo);
      if (quote === null || line === undefined) {
        respondJson(response, 404, { error: "no such line" });
        return;
      }
      const key = `${quote.id}:${lineNo}`;
      const request = { id: `fr-${key}`, status: body.status === "APPROVED" ? "APPROVED" : "PENDING", line_no: lineNo, disc_pct: Number(body.disc_pct) || 10, ...(body.used === true ? { used: true } : {}) };
      floorRequests.set(key, request);
      approvalCards.set(request.id, {
        quote_id: quote.id,
        requested_by: USERS[body.requested_by_flow]?.user_id ?? "u-another-seller",
        detail: `Quote ${quote.quote_no}, line ${lineNo}, ${line.description}, ${request.disc_pct}% discount`,
      });
      respondJson(response, 200, request);
    });
    return;
  }
  if (path === "/_stub/quote-page-size" && request.method === "PUT") {
    readBody(request, response, (body) => {
      switches.quotePageSize = Number.isInteger(body.size) && body.size > 0 ? body.size : 0;
      respondJson(response, 200, { size: switches.quotePageSize });
    });
    return;
  }
  if (path === "/_stub/floor-raise" && request.method === "PUT") {
    readBody(request, response, (body) => {
      switches.floorRaiseBps = Number.isInteger(body.bps) && body.bps > 0 ? body.bps : 0;
      respondJson(response, 200, { bps: switches.floorRaiseBps });
    });
    return;
  }
  // The QD-5 peeks: the role gate's refusal (the core gate answers the RBAC
  // 403 body "Forbidden"), the ghost answer (the payment is taken and stored
  // but the answer is a 500, so the desk reads an unknown outcome and a retry
  // with the same key replays it), a foreign invoice raised another way (the
  // door's 409 names who can record the payment), and a second order for a
  // multi acceptance quote (the door's 409 names the order's own document).
  if (path === "/_stub/payment-role" && request.method === "PUT") {
    readBody(request, response, (body) => {
      switches.paymentRoleRefused = body.refused === true;
      respondJson(response, 200, { refused: switches.paymentRoleRefused });
    });
    return;
  }
  if (path === "/_stub/payment-grant" && request.method === "PUT") {
    readBody(request, response, (body) => {
      switches.paymentGrantRefused = body.refused === true;
      respondJson(response, 200, { refused: switches.paymentGrantRefused });
    });
    return;
  }
  if (path === "/_stub/payment-ghost" && request.method === "PUT") {
    readBody(request, response, (body) => {
      switches.paymentGhost = body.ghost === true;
      respondJson(response, 200, { ghost: switches.paymentGhost });
    });
    return;
  }
  if (path === "/_stub/payment-key-crossed" && request.method === "PUT") {
    // The crossed key: the next payment's key has ALREADY taken a different
    // payment (another tab, an earlier post this desk never saw the answer
    // of): the phantom CARD 5.00 is stored and applied under that key, so
    // the post is answered with the core's 409 sentence and the family read
    // shows what landed.
    readBody(request, response, (body) => {
      switches.paymentKeyCrossed = body.crossed === true;
      respondJson(response, 200, { crossed: switches.paymentKeyCrossed });
    });
    return;
  }
  if (path === "/_stub/foreign-invoice" && request.method === "POST") {
    readBody(request, response, (body) => {
      const quote = quoteOr404(String(body.quote_id ?? ""));
      const quoteOrders = quote === null ? [] : ordersOf(quote.id);
      if (quote === null || quoteOrders.length === 0) {
        respondJson(response, 404, { error: "no converted quote with an order" });
        return;
      }
      const invoice = raiseAgreedInvoice(quoteOrders[0], "SHIPMENT_GROUP");
      respondJson(response, 200, { invoice_no: invoice.invoice_no });
    });
    return;
  }
  if (path === "/_stub/plant-order" && request.method === "POST") {
    readBody(request, response, (body) => {
      const quote = quoteOr404(String(body.quote_id ?? ""));
      if (quote === null) {
        respondJson(response, 404, { error: "no quote with that id" });
        return;
      }
      const order = newOrder(quote, body.origin === "PORTAL" ? "PORTAL" : "COUNTER");
      respondJson(response, 200, { order_id: order.id, order_no: order.order_no });
    });
    return;
  }
  if (path === "/_stub/feedback-mode" && request.method === "PUT") {
    readBody(request, response, (body) => {
      feedbackMode.value = ["fail", "no-number"].includes(body.mode) ? body.mode : "ok";
      respondJson(response, 200, { mode: feedbackMode.value });
    });
    return;
  }
  if (path === "/_stub/feedback" && request.method === "GET") {
    respondJson(response, 200, { filed: feedbackFiled });
    return;
  }
  // The feedback route: a multipart body, kept raw (the readBody helper parses JSON).
  if (path === "/api/v1/feedback" && request.method === "POST") {
    const chunks = [];
    request.on("data", (chunk) => chunks.push(chunk));
    request.on("end", () => {
      const text = Buffer.concat(chunks).toString("latin1");
      const field = (name) => {
        const match = new RegExp(`name="${name}"\\r\\n\\r\\n([^\\r]*)`).exec(text);
        return match === null ? "" : match[1];
      };
      if (feedbackMode.value === "fail") {
        respondJson(response, 502, { error: "the board is not answering" });
        return;
      }
      feedbackFiled.push({ type: field("type"), message: field("message"), screen: field("screen"), images: (text.match(/name="images"/g) ?? []).length });
      respondJson(response, 201, feedbackMode.value === "no-number" ? {} : { issue_number: `HH-${feedbackFiled.length}` });
    });
    return;
  }
  // Put the whole in-memory world back to its seeded state, so a suite that
  // changes quotes, policy or switches leaves the next one a clean stub.
  if (path === "/_stub/reset" && request.method === "POST") {
    seedWorld();
    shareLinks.clear();
    floorRequests.clear();
    resetSeamWorld();
    workspaces.clear();
    myItems.clear();
    roleItemsByProfile.clear();
    workspaceWrites.length = 0;
    itemsWrites.length = 0;
    policyWrites.length = 0;
    decisions.length = 0;
    conversions.length = 0;
    convertKeys.clear();
    payments.length = 0;
    paymentsByKey.clear();
    posts.length = 0;
    doorRefusal.count = 0;
    switches.convertWall = false;
    feedbackFiled.length = 0;
    feedbackMode.value = "ok";
    acceptance.allow_multi_accept = false;
    resetPricing();
    resetPolicy();
    resetScopeRules();
    switches.callerOnCostList = true;
    switches.callerIsGeneralManager = false;
    switches.chipSetNull = false;
    switches.grantAllTrue = false;
    switches.floorRaiseBps = 0;
    switches.approvalsShape = "page";
    switches.quotePageSize = 0;
    switches.paymentRoleRefused = false;
    switches.paymentGhost = false;
    switches.paymentKeyCrossed = false;
    switches.paymentGrantRefused = false;
    staffDecisions.length = 0;
    staffDecisionAnswers.clear();
    decisionSwitches.moved = false;
    decisionSwitches.failCount = 0;
    decisionSwitches.grantRefused = false;
    respondJson(response, 200, { reset: true });
    return;
  }
  // The third coder's peek routes: the assist key, a follow-up that went out,
  // a quote's holds lapsing, and the branch stock a chip grades against.
  if (path === "/_stub/ai-key" && request.method === "PUT") {
    readBody(request, response, (body) => {
      seamSwitches.aiConfigured = body.configured === true;
      respondJson(response, 200, { configured: seamSwitches.aiConfigured });
    });
    return;
  }
  if (path === "/_stub/followup-event" && request.method === "POST") {
    readBody(request, response, (body) => {
      const quote = quoteOr404(String(body.quote_id));
      if (quote === null) {
        respondJson(response, 404, { error: "no quote with that id" });
        return;
      }
      ensurePlan(quote);
      const events = followEvents.get(quote.id) ?? [];
      const at = new Date().toISOString();
      events.push({ step_day: Number(body.step_day ?? 3), channel: body.channel ?? "EMAIL", status: body.status ?? "SENT", scheduled_for: at, occurred_at: at });
      followEvents.set(quote.id, events);
      respondJson(response, 200, { events: events.length });
    });
    return;
  }
  if (path === "/_stub/hold-lapse" && request.method === "POST") {
    readBody(request, response, (body) => {
      const quote = quoteOr404(String(body.quote_id));
      if (quote === null) {
        respondJson(response, 404, { error: "no quote with that id" });
        return;
      }
      if (body.quote_expired === true) quote.expired = true;
      const at = new Date().toISOString();
      lapsedHolds.set(
        quote.id,
        quote.lines.filter((line) => body.line_no === undefined || line.line_no === Number(body.line_no)).map((line) => ({
          reservation_id: `res-${quote.id}-${line.line_no}`,
          product_id: line.product_id,
          product_name: line.description,
          sku: productById.get(line.product_id)?.sku ?? "",
          branch_id: quote.branch_id,
          qty_base: Number(line.qty_base),
          status: "EXPIRED",
          expires_at: at,
          ended_at: at,
        })),
      );
      respondJson(response, 200, { lapsed: (lapsedHolds.get(quote.id) ?? []).length });
    });
    return;
  }
  if (path === "/_stub/stock" && request.method === "PUT") {
    readBody(request, response, (body) => {
      const row = stockByProduct.get(String(body.product_id));
      if (row === undefined) {
        respondJson(response, 404, { error: "no product with that id" });
        return;
      }
      if (Number.isFinite(body.pointLoma)) row.pointLoma = body.pointLoma;
      if (Number.isFinite(body.julian)) row.julian = body.julian;
      respondJson(response, 200, row);
    });
    return;
  }
  if (path === "/_stub/seam-wired" && request.method === "PUT") {
    readBody(request, response, (body) => {
      seamSwitches.seamWired = body.wired !== false;
      respondJson(response, 200, { wired: seamSwitches.seamWired });
    });
    return;
  }
  if (path === "/_stub/cost-list" && request.method === "PUT") {
    readBody(request, response, (body) => {
      switches.callerOnCostList = body.member !== false;
      respondJson(response, 200, { member: switches.callerOnCostList });
    });
    return;
  }

  // The demo way in: public in the contract.
  if (request.method === "GET" && path === "/api/v1/demo/flows") {
    respondJson(response, 200, { flows: FLOWS });
    return;
  }
  if (request.method === "POST" && path === "/api/v1/demo/session") {
    readBody(request, response, (body) => {
      const flow = body.flow;
      if (typeof flow !== "string" || USERS[flow] === undefined) {
        respondJson(response, 404, { error: "no such demo flow" });
        return;
      }
      respondJson(response, 200, { token: mintToken(flow), user: USERS[flow], flow });
    });
    return;
  }

  // The pricing console's routes and switches (QD-4), behind the same bearer check.
  if (handlePricing({ request, response, path, url, callerOf, readBody, respondJson })) return;

  // The staff routes, behind the minted bearer and the demo org header.
  const caller = callerOf(request);
  if (caller.refused !== undefined) {
    respondJson(response, caller.refused, { error: caller.error });
    return;
  }
  const userId = caller.account.user_id;

  if (request.method === "GET" && path === "/api/v1/branches") {
    respondJson(response, 200, BRANCHES);
    return;
  }

  // The quote policy: any staff caller reads it; the write follows the CP0-5
  // answer A rule, in words: the Sales manager writes a branch row it reaches,
  // the General Manager alone writes the dealer default, anyone else is refused.
  if (path === "/api/v1/quotes/policy" && request.method === "GET") {
    const branchId = url.searchParams.get("branch_id");
    respondJson(response, 200, effectivePolicy(branchId === null || branchId === "" ? "" : branchId));
    return;
  }
  if (path === "/api/v1/quotes/policy" && request.method === "PUT") {
    readBody(request, response, (body) => {
      const dealerRow = body.branch_id === undefined || body.branch_id === "";
      if (!isSettingsAdmin(caller)) {
        respondJson(response, 403, { error: "quote: only a sales manager or the general manager changes the quote settings" });
        return;
      }
      if (dealerRow && !isGeneralManager(caller)) {
        respondJson(response, 403, { error: "quote: only the general manager changes the dealer default quote settings" });
        return;
      }
      if (!dealerRow && body.branch_id !== caller.branchId) {
        respondJson(response, 403, { error: "quote: only a sales manager of that branch changes its quote settings" });
        return;
      }
      const days = body.default_validity_days;
      if (!Number.isInteger(days) || days < 1 || days > 365) {
        respondJson(response, 422, { error: "quote: default validity must be between 1 and 365 days" });
        return;
      }
      if (!Number.isInteger(body.default_deposit_pct) || body.default_deposit_pct < 0 || body.default_deposit_pct > 100) {
        respondJson(response, 422, { error: "quote: default deposit must be between 0 and 100 percent" });
        return;
      }
      if (!Number.isInteger(body.workspace_split_pct) || body.workspace_split_pct < 20 || body.workspace_split_pct > 80) {
        respondJson(response, 422, { error: "quote: the panel split must be between 20 and 80 percent" });
        return;
      }
      if (typeof body.quantity_notice !== "string") {
        respondJson(response, 422, { error: "quote: the quantity notice is required" });
        return;
      }
      if (Array.from(body.quantity_notice).length > 500) {
        respondJson(response, 400, { error: "the quantity notice is too long, keep it to 500 characters" });
        return;
      }
      const { effective_chips: _skip, ...row } = body;
      // A blank notice goes back to the default wording, as the core's policy write does.
      if (row.quantity_notice.trim() === "") row.quantity_notice = POLICY.quantity_notice;
      policyRows.set(dealerRow ? "" : body.branch_id, row);
      policyWrites.push({ scope: dealerRow ? "dealer" : "branch", user_id: userId, branch_id: body.branch_id ?? null, default_validity_days: body.default_validity_days });
      respondJson(response, 200, { ...effectivePolicy(dealerRow ? "" : body.branch_id), ...(dealerRow ? {} : { branch_id: body.branch_id }) });
    });
    return;
  }


  if (request.method === "GET" && path === "/api/v1/quotes") {
    const status = url.searchParams.get("status");
    // The QD-1 list rule: a Work caller's list defaults to its own quotes; a
    // Manage caller (the Sales manager) reaches every quote in its branch.
    const isManager = caller.account.role_code === "sales_manager";
    let rows = [...quotes.values()];
    if (!isManager) rows = rows.filter((quote) => quote.owner_id === userId);
    if (status !== null && status !== "") rows = rows.filter((quote) => quote.status === status);
    const items = rows.map((quote) => ({
      id: quote.id,
      quote_no: quote.quote_no,
      customer_id: quote.customer_id,
      customer_name: quote.customer_name,
      branch_id: quote.branch_id,
      status: quote.status,
      valid_until: quote.valid_until === "" ? undefined : quote.valid_until,
      expired: quote.expired,
      subtotal_cents: quote.subtotal_cents,
      tax_cents: quote.tax_cents,
      total_cents: quote.total_cents,
      updated_at: "2026-01-01T00:00:00Z",
      owner_id: quote.owner_id,
      owner_name: quote.owner_name,
    }));
    // The core pages (fifty by default, newest first). The stub pages at a small size on request, so a suite can reach the next page.
    const size = switches.quotePageSize > 0 ? switches.quotePageSize : 50;
    const start = Number(url.searchParams.get("cursor") ?? "0") || 0;
    const page = items.slice(start, start + size);
    respondJson(response, 200, { items: page, next_cursor: start + size < items.length ? String(start + size) : null, limit: size });
    return;
  }

  if (request.method === "POST" && path === "/api/v1/quotes") {
    readBody(request, response, (body) => {
      const customer = customerById.get(body.customer_id);
      if (customer === undefined) {
        respondJson(response, 400, { error: "no customer with that id" });
        return;
      }
      // Creating a quote sets the salesperson to the caller (plan 3.3).
      const quote = seedQuote("DRAFT", body.customer_id, caller.flow, [], {
        salesperson_id: userId,
        salesperson_name: caller.account.display_name,
      });
      if (typeof body.valid_until === "string" && body.valid_until !== "") quote.valid_until = body.valid_until;
      respondJson(response, 200, viewOf(caller, quote));
    });
    return;
  }

  // round 14 (a): settings grant -- mirrors the three predicates the real server uses.
  if (path === "/api/v1/quotes/settings-grant" && request.method === "GET") {
    // can_read_acceptance + can_edit_policy: holdsQuoteSettingsAuthority (LevelManage on quote-desk).
    // The grantAllTrue switch is a grant that outran a role change: it says yes where the routes still refuse.
    const canReadAcceptance = switches.grantAllTrue || isSettingsAdmin(caller);
    const canEditPolicy = switches.grantAllTrue || isSettingsAdmin(caller);
    // can_read_scope_rules: IsAdmin (TierLeadership + ScopeGlobal).
    const canReadScopeRules = switches.grantAllTrue || isAdmin(caller);
    respondJson(response, 200, {
      can_read_acceptance: canReadAcceptance,
      can_read_scope_rules: canReadScopeRules,
      can_edit_policy: canEditPolicy,
    });
    return;
  }

  let match = /^\/api\/v1\/quotes\/([^/]+)$/.exec(path);
  if (match !== null) {
    const quote = quoteOr404(match[1]);
    if (quote === null) {
      respondJson(response, 404, { error: "no quote with that id" });
      return;
    }
    if (request.method === "GET") {
      respondJson(response, 200, viewOf(caller, quote));
      return;
    }
    if (request.method === "PATCH") {
      readBody(request, response, (body) => {
        const view = viewOf(caller, quote);
        if (!view.editable_fields.includes("valid_until")) {
          respondJson(response, 409, { error: `quote: this quote is ${quote.status.toLowerCase()} and can no longer be changed` });
          return;
        }
        const previous = quote.valid_until;
        quote.valid_until = typeof body.valid_until === "string" ? body.valid_until : "";
        audit(quote, caller, [{ field: "valid_until", line_no: 0, before: previous, after: quote.valid_until }]);
        respondJson(response, 200, {
          totals: { SubtotalCents: quote.subtotal_cents, TotalCents: quote.total_cents },
          status: quote.status,
        });
      });
      return;
    }
  }

  match = /^\/api\/v1\/quotes\/([^/]+)\/lines$/.exec(path);
  if (match !== null && request.method === "POST") {
    const quote = quoteOr404(match[1]);
    if (quote === null) {
      respondJson(response, 404, { error: "no quote with that id" });
      return;
    }
    readBody(request, response, (body) => {
      const view = viewOf(caller, quote);
      if (!view.editable_fields.includes("line_add")) {
        respondJson(response, 409, { error: `quote: this quote is ${quote.status.toLowerCase()} and can no longer be changed` });
        return;
      }
      const product = productById.get(body.product_id);
      if (product === undefined) {
        respondJson(response, 400, { error: "no product with that id" });
        return;
      }
      const customer = customerById.get(quote.customer_id);
      const before = snapshotOf(quote, null);
      const line = newLine(product, customer, body.qty_entered, { lineNo: nextLineNo(quote) });
      quote.lines.push(line);
      restamp(quote);
      audit(quote, caller, [
        { field: "line_added", line_no: line.line_no, after: product.product_id },
        { field: "qty_entered", line_no: line.line_no, after: line.qty_entered },
        { field: "qty_base", line_no: line.line_no, after: line.qty_base },
        ...moneyRows(quote, before, line, line.line_no),
      ]);
      respondJson(response, 200, editResult(quote, line));
    });
    return;
  }

  match = /^\/api\/v1\/quotes\/([^/]+)\/lines\/(\d+)$/.exec(path);
  if (match !== null) {
    const quote = quoteOr404(match[1]);
    if (quote === null) {
      respondJson(response, 404, { error: "no quote with that id" });
      return;
    }
    const lineNo = Number(match[2]);
    const line = quote.lines.find((row) => row.line_no === lineNo);
    if (request.method === "DELETE") {
      const view = viewOf(caller, quote);
      if (!view.editable_fields.includes("line_remove")) {
        respondJson(response, 409, { error: `quote: this quote is ${quote.status.toLowerCase()} and can no longer be changed` });
        return;
      }
      if (line === undefined) {
        respondJson(response, 404, { error: "no line with that number" });
        return;
      }
      const before = snapshotOf(quote, line);
      quote.lines = quote.lines.filter((row) => row.line_no !== lineNo);
      restamp(quote);
      audit(quote, caller, [{ field: "line_removed", line_no: lineNo, before: line.product_id }, ...moneyRows(quote, before, null, lineNo)]);
      respondJson(response, 200, editResult(quote, null));
      return;
    }
    if (request.method === "PATCH") {
      readBody(request, response, (body) => {
        const view = viewOf(caller, quote);
        if (!view.editable_fields.includes("line_qty")) {
          respondJson(response, 409, { error: `quote: this quote is ${quote.status.toLowerCase()} and can no longer be changed` });
          return;
        }
        if (line === undefined) {
          respondJson(response, 404, { error: "no line with that number" });
          return;
        }
        const customer = customerById.get(quote.customer_id);
        const product = productById.get(line.product_id);
        // The discount arm: the plan's floor decision runs here, on the write
        // that puts a line below its rule price.
        if (body.disc_pct !== undefined) {
          const disc = Number(body.disc_pct);
          if (!Number.isInteger(disc) || disc < 0 || disc > 100) {
            respondJson(response, 400, { error: "quote: the discount must be a whole percent from 0 to 100" });
            return;
          }
          let decision = floorDecision(caller, product, customer, disc);
          if (decision !== null && !decision.cleared) {
            // The requester's own APPROVED, unused request for this line clears the write once and is used up
            // (the core's ConsumeApprovedFloorTx).
            const standing = floorRequests.get(`${quote.id}:${lineNo}`);
            const card = standing === undefined ? undefined : approvalCards.get(standing.id);
            if (standing !== undefined && standing.status === "APPROVED" && standing.used !== true && card !== undefined && card.requested_by === caller.account.user_id) {
              standing.used = true;
              decision = { cleared: true, by: card.decided_by ?? caller.account.user_id };
            }
          }
          if (decision !== null && decision.cleared) {
            // The manager's own clearing: accepted once, stamped on the line.
            const before = snapshotOf(quote, line);
            quote.lines = quote.lines.map((row) =>
              row.line_no === lineNo ? newLine(product, customer, row.qty_entered, { lineNo, discPct: disc, floorClearedBy: decision.by }) : row,
            );
            restamp(quote);
            audit(quote, caller, [
              { field: "disc_pct", line_no: lineNo, before: line.disc_pct, after: disc },
              { field: "floor_cleared_by", line_no: lineNo, after: decision.by },
              ...moneyRows(quote, before, quote.lines.find((row) => row.line_no === lineNo), lineNo),
            ]);
            respondJson(response, 200, editResult(quote, quote.lines.find((row) => row.line_no === lineNo)));
            return;
          }
          if (decision !== null) {
            const standing = floorRequests.get(`${quote.id}:${lineNo}`);
            const body = {
              ...decision.body,
              ...(standing === undefined ? {} : { existing_request: standing }),
            };
            respondJson(response, 409, body);
            return;
          }
          const before = snapshotOf(quote, line);
          quote.lines = quote.lines.map((row) =>
            row.line_no === lineNo ? newLine(product, customer, row.qty_entered, { lineNo, discPct: disc }) : row,
          );
          restamp(quote);
          audit(quote, caller, [
            { field: "disc_pct", line_no: lineNo, before: line.disc_pct, after: disc },
            ...moneyRows(quote, before, quote.lines.find((row) => row.line_no === lineNo), lineNo),
          ]);
          respondJson(response, 200, editResult(quote, quote.lines.find((row) => row.line_no === lineNo)));
          return;
        }
        // The quantity arm.
        const qty = String(body.qty_entered ?? "");
        const qtyNumber = Number(qty);
        if (qty === "" || !Number.isFinite(qtyNumber) || qtyNumber <= 0) {
          respondJson(response, 400, { error: "quote: the quantity must be a positive number" });
          return;
        }
        // The qty arm reprices at the discount the line already carries, so the
        // floor is asked again at that discount: a refusal here is the same
        // typed body the discount arm answers.
        let clearedBy = line.floor_cleared_by;
        if (line.disc_pct > 0) {
          const decision = floorDecision(caller, product, customer, line.disc_pct);
          if (decision !== null && !decision.cleared) {
            const standing = floorRequests.get(`${quote.id}:${lineNo}`);
            respondJson(response, 409, { ...decision.body, ...(standing === undefined ? {} : { existing_request: standing }) });
            return;
          }
          if (decision !== null) clearedBy = decision.by;
        }
        const before = snapshotOf(quote, line);
        quote.lines = quote.lines.map((row) =>
          row.line_no === lineNo ? newLine(product, customer, qty, { lineNo, discPct: row.disc_pct, floorClearedBy: clearedBy }) : row,
        );
        restamp(quote);
        audit(quote, caller, [
          { field: "qty_entered", line_no: lineNo, before: line.qty_entered, after: qty },
          { field: "qty_base", line_no: lineNo, before: line.qty_base, after: qty },
          ...moneyRows(quote, before, quote.lines.find((row) => row.line_no === lineNo), lineNo),
        ]);
        respondJson(response, 200, editResult(quote, quote.lines.find((row) => row.line_no === lineNo)));
      });
      return;
    }
  }

  match = /^\/api\/v1\/quotes\/([^/]+)\/terms$/.exec(path);
  if (match !== null) {
    const quote = quoteOr404(match[1]);
    if (quote === null) {
      respondJson(response, 404, { error: "no quote with that id" });
      return;
    }
    if (request.method === "GET") {
      respondJson(response, 200, termsOf(quote));
      return;
    }
    if (request.method === "PUT") {
      readBody(request, response, (body) => {
        Object.assign(quote.terms, {
          deposit_required: body.deposit_required === true,
          deposit_pct: Number.isInteger(body.deposit_pct) ? body.deposit_pct : quote.terms.deposit_pct,
          include_images: body.include_images === true,
          customer_notes: typeof body.customer_notes === "string" ? body.customer_notes : quote.terms.customer_notes,
          contact_name: typeof body.contact_name === "string" ? body.contact_name : quote.terms.contact_name,
          project_job: typeof body.project_job === "string" ? body.project_job : quote.terms.project_job,
          customer_po: typeof body.customer_po === "string" ? body.customer_po : quote.terms.customer_po,
          salesperson_id: typeof body.salesperson_id === "string" ? body.salesperson_id : "",
          fulfillment: body.fulfillment === "DELIVERY" ? "DELIVERY" : "PICKUP",
          delivery_address: typeof body.delivery_address === "string" ? body.delivery_address : quote.terms.delivery_address,
          requested_date: typeof body.requested_date === "string" ? body.requested_date : "",
          delivery_fee_cents: Number.isInteger(body.delivery_fee_cents) && body.delivery_fee_cents > 0 ? body.delivery_fee_cents : 0,
        });
        restamp(quote);
        respondJson(response, 200, termsOf(quote));
      });
      return;
    }
  }

  match = /^\/api\/v1\/quotes\/([^/]+)\/margin-glance$/.exec(path);
  if (match !== null && request.method === "GET") {
    const quote = quoteOr404(match[1]);
    if (quote === null) {
      respondJson(response, 404, { error: "no quote with that id" });
      return;
    }
    respondJson(response, 200, glanceOf(quote));
    return;
  }

  // PR 247: staff accept or reject on the customer's behalf. The channel is closed, an accept carries the total
  // the staff member read and the quantities statement, a reject a reason from a closed list; a keyed replay
  // answers the first outcome and records nothing more.
  match = /^\/api\/v1\/quotes\/([^/]+)\/(accept|reject)$/.exec(path);
  if (match !== null && request.method === "POST") {
    const quote = quoteOr404(match[1]);
    if (quote === null) {
      respondJson(response, 404, { error: "no quote with that id" });
      return;
    }
    const kind = match[2];
    if (decisionSwitches.grantRefused) {
      respondJson(response, 403, { error: "Forbidden" });
      return;
    }
    readBody(request, response, (body) => {
      const key = String(request.headers["idempotency-key"] ?? "");
      staffDecisions.push({ kind, quote_id: quote.id, key, body });
      if (decisionSwitches.failCount > 0) {
        decisionSwitches.failCount -= 1;
        respondJson(response, 500, { error: "quote: the decision could not be recorded right now" });
        return;
      }
      const replay = staffDecisionAnswers.get(`${quote.id}:${key}`);
      if (replay !== undefined) {
        respondJson(response, 200, { ...replay, replayed: true });
        return;
      }
      if (!["phone", "text", "email", "in_person"].includes(String(body.channel))) {
        respondJson(response, 400, { error: "quote: the channel must be phone, text, email or in_person" });
        return;
      }
      if (quote.status !== "DRAFT" && quote.status !== "SENT") {
        respondJson(response, 409, { error: `quote: this quote is ${quote.status.toLowerCase()}, so a decision cannot be recorded` });
        return;
      }
      if (decisionSwitches.moved) {
        quote.total_cents += 100;
        decisionSwitches.moved = false;
      }
      const previous = quote.status;
      if (kind === "accept") {
        if (body.quantities_confirmed !== true) {
          respondJson(response, 422, { error: "quote: confirm that the customer confirmed the quantities before the quote is marked accepted" });
          return;
        }
        if (body.expected_total_cents !== quote.total_cents) {
          respondJson(response, 409, { error: `quote: the quote total is now ${coreMoney(quote.total_cents)}, not the ${coreMoney(Number(body.expected_total_cents))} you read to the customer; read it again` });
          return;
        }
        audit(quote, callerOf(request), [{ field: "status", before: previous, after: "ACCEPTED" }, { field: "qty_base", line_no: 1, before: "1", after: "1" }]);
        quote.status = "ACCEPTED";
        quote.staffAcceptances = [...(quote.staffAcceptances ?? []), { source: "STAFF_ON_BEHALF", accepted_at: new Date().toISOString(), quantities_confirmed: true, amount_cents: quote.total_cents, channel: body.channel, ...(body.note ? { note: body.note } : {}) }];
      } else {
        if (!["price", "competitor", "project_cancelled", "timing", "no_response", "other"].includes(String(body.reason_code))) {
          respondJson(response, 400, { error: "quote: the reason must be one of the listed reasons" });
          return;
        }
        if (body.reason_code === "other" && String(body.note ?? "").trim() === "") {
          respondJson(response, 422, { error: "quote: a reason of other needs a note" });
          return;
        }
        audit(quote, callerOf(request), [{ field: "status", before: previous, after: "LOST" }]);
        quote.status = "LOST";
        quote.declined = { reason_code: String(body.reason_code), channel: String(body.channel), ...(body.note ? { note: String(body.note) } : {}), declined_at: new Date().toISOString() };
      }
      const answer = {
        quote_id: quote.id,
        quote_no: quote.quote_no,
        status: quote.status,
        previous_status: previous,
        channel: body.channel,
        on_behalf: true,
        decided_at: new Date().toISOString(),
        total_cents: quote.total_cents,
        replayed: false,
      };
      staffDecisionAnswers.set(`${quote.id}:${key}`, answer);
      respondJson(response, 200, answer);
    });
    return;
  }

  // The preview read (PR 238): the customer's own page as the desk's session may read it. It never mints a link
  // and never moves the status; the answer says preview true and acceptable false, so no accept area exists.
  match = /^\/api\/v1\/quotes\/([^/]+)\/customer-view$/.exec(path);
  if (match !== null && request.method === "GET") {
    const quote = quoteOr404(match[1]);
    if (quote === null) {
      respondJson(response, 404, { error: "no quote with that id" });
      return;
    }
    const terms = termsOf(quote);
    respondJson(response, 200, {
      quote_no: quote.quote_no,
      customer_name: quote.customer_name,
      status: quote.status,
      valid_until: quote.valid_until === "" ? undefined : quote.valid_until,
      expired: quote.expired,
      lines: quote.lines.map((line, index) => ({
        line_no: line.line_no,
        description: line.description,
        qty_entered: line.qty_entered,
        uom_entered: line.uom_entered,
        unit_price_cents: line.unit_price_cents,
        line_total_cents: line.line_total_cents,
        ...(terms.lines[index].disc_pct ? { disc_pct: terms.lines[index].disc_pct, pre_discount_total_cents: terms.lines[index].pre_discount_total_cents } : {}),
      })),
      subtotal_cents: quote.subtotal_cents,
      tax_cents: quote.tax_cents,
      total_cents: quote.total_cents,
      as_of: "2026-01-01T00:00:00Z",
      acceptable: false,
      preview: true,
      customer_notes: terms.customer_notes === "" ? undefined : terms.customer_notes,
    });
    return;
  }

  match = /^\/api\/v1\/quotes\/([^/]+)\/share-links$/.exec(path);
  if (match !== null && request.method === "POST") {
    const quote = quoteOr404(match[1]);
    if (quote === null) {
      respondJson(response, 404, { error: "no quote with that id" });
      return;
    }
    readBody(request, response, () => {
      if (quote.status !== "DRAFT" && quote.status !== "SENT") {
        respondJson(response, 409, { error: `quote: this quote is ${quote.status.toLowerCase()} and cannot be shared` });
        return;
      }
      // Minting is the send: the reference's own rule, DRAFT moves to SENT.
      if (quote.status === "DRAFT") quote.status = "SENT";
      const list = shareLinks.get(quote.id) ?? [];
      const now = Date.now();
      const row = {
        id: `sl-${quote.id}-${list.length + 1}`,
        quote_id: quote.id,
        expires_at: new Date(now + 14 * 86400000).toISOString(),
        created_by: userId,
        created_at: new Date(now).toISOString(),
        live: true,
        revoked_at: null,
      };
      list.push(row);
      shareLinks.set(quote.id, list);
      // The token is the mint's answer alone: it is never stored or listed.
      respondJson(response, 200, { ...row, token: `qd-demo-${row.id}` });
    });
    return;
  }

  match = /^\/api\/v1\/quotes\/([^/]+)\/share-links$/.exec(path);
  if (match !== null && request.method === "GET") {
    const quote = quoteOr404(match[1]);
    if (quote === null) {
      respondJson(response, 404, { error: "no quote with that id" });
      return;
    }
    respondJson(response, 200, { items: shareLinks.get(quote.id) ?? [] });
    return;
  }

  match = /^\/api\/v1\/quotes\/([^/]+)\/share-links\/([^/]+)$/.exec(path);
  if (match !== null && request.method === "DELETE") {
    const quote = quoteOr404(match[1]);
    const row = quote === null ? undefined : (shareLinks.get(quote.id) ?? []).find((link) => link.id === match[2]);
    if (row === undefined) {
      respondJson(response, 404, { error: "no share link with that id" });
      return;
    }
    row.live = false;
    row.revoked_at = new Date().toISOString();
    response.writeHead(204);
    response.end();
    return;
  }

  match = /^\/api\/v1\/quotes\/([^/]+)\/edits$/.exec(path);
  if (match !== null && request.method === "GET") {
    const quote = quoteOr404(match[1]);
    if (quote === null) {
      respondJson(response, 404, { error: "no quote with that id" });
      return;
    }
    respondJson(response, 200, { quote_id: quote.id, changes: quote.history ?? [] });
    return;
  }

  match = /^\/api\/v1\/quotes\/([^/]+)\/lines\/(\d+)\/compare$/.exec(path);
  if (match !== null && request.method === "POST") {
    const quote = quoteOr404(match[1]);
    const line = quote === null ? undefined : quote.lines.find((row) => row.line_no === Number(match[2]));
    if (line === undefined) {
      respondJson(response, 404, { error: "no line with that number" });
      return;
    }
    readBody(request, response, (body) => {
      const customer = customerById.get(quote.customer_id);
      const qty = Number(line.qty_entered);
      const source = customer && customer.percent_off > 0 ? "CUSTOMER_RULE" : "LIST";
      const options = [
        {
          product_id: line.product_id,
          is_current: true,
          qty_entered: line.qty_entered,
          uom_entered: line.uom_entered,
          unit_price_cents: line.unit_price_cents,
          price_uom: line.price_uom,
          line_total_cents: line.line_total_cents,
          list_price_cents: productById.get(line.product_id).list_cents,
          discount_bps: customer ? customer.percent_off * 100 : 0,
          source,
          delta_cents: 0,
          blocked: false,
        },
      ];
      for (const alt of Array.isArray(body.alternates) ? body.alternates : []) {
        const product = productById.get(alt.product_id);
        if (product === undefined) continue;
        const unit = ruleUnitCents(product, customer);
        const row = {
          product_id: product.product_id,
          is_current: false,
          qty_entered: line.qty_entered,
          uom_entered: product.sold_by,
          unit_price_cents: unit,
          price_uom: product.sold_by,
          line_total_cents: Math.round(unit * qty),
          list_price_cents: product.list_cents,
          discount_bps: customer ? customer.percent_off * 100 : 0,
          source,
          delta_cents: Math.round(unit * qty) - line.line_total_cents,
          blocked: marginBps(unit, product.branch_cost_cents[POINT_LOMA_ID] ?? 0) < product.floor_bps + switches.floorRaiseBps,
          ...(product.sold_by === line.price_uom ? {} : { unavailable: "cannot be priced in this unit" }),
        };
        if (row.blocked) row.block_policy = "category_margin_floor";
        options.push(row);
      }
      respondJson(response, 200, { quote_id: quote.id, line_no: line.line_no, options });
    });
    return;
  }

  match = /^\/api\/v1\/quotes\/([^/]+)\/sms-share$/.exec(path);
  if (match !== null && request.method === "POST") {
    const quote = quoteOr404(match[1]);
    if (quote === null) {
      respondJson(response, 404, { error: "no quote with that id" });
      return;
    }
    readBody(request, response, () => {
      // The reference world's own refusal: no text provider is keyed and the
      // customer has not opted in, so the text leg is refused in words.
      respondJson(response, 409, { error: "quote: the customer has not opted in to text messages" });
    });
    return;
  }

  match = /^\/api\/v1\/quotes\/([^/]+)\/convert$/.exec(path);
  if (match !== null && request.method === "POST") {
    const quote = quoteOr404(match[1]);
    if (quote === null) {
      respondJson(response, 404, { error: "no quote with that id" });
      return;
    }
    const convertKey = String(request.headers["idempotency-key"] ?? "").toLowerCase();
    posts.push({ kind: "convert", quote_id: quote.id, key: convertKey === "" ? null : convertKey });
    readBody(request, response, (body) => {
      const fulfilment = body.fulfillment === "WILL_CALL" ? "WILL_CALL" : "PICKUP";
      // The core's own first check on the key (share_idempotency.go normalizeIdempotencyKey): a UUID,
      // case insensitive, anything else a 400 in the same words. The desk always sends a UUID.
      if (convertKey !== "" && !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(convertKey)) {
        respondJson(response, 400, { error: "Idempotency-Key must be a UUID" });
        return;
      }
      // The core's order (repository.go Convert): a keyed repeat answers the FIRST order and writes
      // nothing, whatever the status is now and whatever the multi accept switch says; the same key
      // with another fulfilment is refused in words.
      if (convertKey !== "") {
        const first = convertKeys.get(`${quote.id}:${convertKey}`);
        if (first !== undefined) {
          if (first.fulfilment !== fulfilment) {
            respondJson(response, 409, { error: "quote: this idempotency key was used for a different conversion; use a new key for a new conversion" });
            return;
          }
          respondJson(response, 201, { order_id: first.order.id, order_no: first.order.order_no });
          return;
        }
      }
      // A converted quote converts again only when the dealer switched multi accept on (DIB-491).
      if (quote.status !== "ACCEPTED" && !(quote.status === "CONVERTED" && acceptance.allow_multi_accept)) {
        respondJson(response, 409, { error: `quote: this quote is ${quote.status.toLowerCase()}, only an accepted quote converts` });
        return;
      }
      // The plain conversion books the order unpaid, so it draws on the account: the real gate
      // (credit.go checkCredit with termsAreCredit true) refuses a customer with no credit at
      // all under the COD policy, an over-limit account under the credit policy, and any held
      // account whatever its limit.
      const refused = creditRefusal(quote);
      if (refused !== null) {
        respondJson(response, 409, { error: refused });
        return;
      }
      quote.status = "CONVERTED";
      quote.fulfilment = fulfilment;
      conversions.push({ quote_id: quote.id, fulfillment: body.fulfillment ?? null });
      const order = newOrder(quote, "COUNTER");
      if (convertKey !== "") convertKeys.set(`${quote.id}:${convertKey}`, { order, fulfilment });
      respondJson(response, 201, { order_id: order.id, order_no: order.order_no });
    });
    return;
  }

  // ── QD-5: the document family reads and the counter payment door ──────────

  /** The family rows of a quote: itself, the orders it became, and every order's invoices. */
  function familyOf(quote, selfKind, selfId) {
    const rows = [
      {
        kind: "QUOTE",
        id: quote.id,
        number: quote.quote_no,
        status: quote.status,
        date: quote.created_at ?? quote.updated_at ?? new Date().toISOString(),
        total_cents: quote.total_cents,
        ...(selfKind === "QUOTE" ? { self: true } : {}),
      },
    ];
    for (const order of ordersOf(quote.id)) {
      rows.push({
        kind: "ORDER",
        id: order.id,
        number: order.order_no,
        status: order.status,
        date: order.created_at,
        total_cents: order.total_cents,
        parent_id: quote.id,
        origin: order.origin,
        accept_seq: order.accept_seq,
        ...(selfKind === "ORDER" && selfId === order.id ? { self: true } : {}),
      });
      for (const invoice of invoicesOf(order.id)) {
        rows.push({
          kind: "INVOICE",
          id: invoice.id,
          number: invoice.invoice_no,
          status: invoice.status,
          date: invoice.created_at,
          total_cents: invoice.total_cents,
          parent_id: order.id,
        });
      }
    }
    return rows;
  }

  match = /^\/api\/v1\/quotes\/([^/]+)\/related-documents$/.exec(path);
  if (match !== null && request.method === "GET") {
    const quote = quoteOr404(match[1]);
    if (quote === null) {
      respondJson(response, 404, { error: "no quote with that id" });
      return;
    }
    respondJson(response, 200, { items: familyOf(quote, "QUOTE", quote.id) });
    return;
  }

  match = /^\/api\/v1\/orders\/([^/]+)\/related-documents$/.exec(path);
  if (match !== null && request.method === "GET") {
    const order = [...orders.values()].find((o) => o.id === match[1] || o.order_no === match[1]);
    if (order === undefined) {
      respondJson(response, 404, { error: "no order with that id" });
      return;
    }
    const quote = quotes.get(order.quote_id);
    if (quote === undefined) {
      respondJson(response, 404, { error: "no quote with that id" });
      return;
    }
    respondJson(response, 200, { items: familyOf(quote, "ORDER", order.id) });
    return;
  }

  const paymentTenders = new Set(["CASH", "CARD", "CHEQUE", "EFT"]);
  const paymentKeyPattern = /^[A-Za-z0-9._:-]{1,64}$/;

  match = /^\/api\/v1\/quotes\/([^/]+)\/order-payment$/.exec(path);
  if (match !== null && request.method === "POST") {
    // The gate's own refusal, exactly as the core's role gate answers it.
    if (switches.paymentRoleRefused) {
      respondJson(response, 403, { error: "Forbidden" });
      return;
    }
    const quote = quoteOr404(match[1]);
    if (quote === null) {
      respondJson(response, 404, { error: "no quote with that id" });
      return;
    }
    readBody(request, response, (body) => {
      // Every post as it arrived, replays and refusals included, and the one-shot refusal the suite arms.
      posts.push({ kind: "order-payment", quote_id: quote.id, key: String(body.idempotency_key ?? ""), header: request.headers["idempotency-key"] ?? null, fulfillment: body.fulfillment ?? null });
      if (doorRefusal.count > 0) {
        doorRefusal.count -= 1;
        respondJson(response, doorRefusal.status, { error: doorRefusal.error });
        return;
      }
      const method = String(body.method ?? "").trim().toUpperCase();
      const reference = String(body.reference ?? "").trim();
      const key = String(body.idempotency_key ?? "");
      const amount = body.amount_cents;
      if (!paymentTenders.has(method)) {
        respondJson(response, 400, { error: "method must be one of CASH, CARD, CHEQUE, EFT" });
        return;
      }
      if (!paymentKeyPattern.test(key)) {
        respondJson(response, 400, { error: "a payment needs an idempotency key of 1 to 64 letters, digits, dot, colon, dash or underscore" });
        return;
      }
      if (!Number.isInteger(amount) || amount <= 0) {
        respondJson(response, 400, { error: "the payment amount must be more than zero" });
        return;
      }
      if (reference.length > 80) {
        respondJson(response, 400, { error: "the payment reference is limited to 80 characters" });
        return;
      }
      const customer = customerById.get(quote.customer_id);
      const cash = (customer?.credit_limit_cents ?? 0) <= 0;
      const namespaced = paymentKeyOf(quote.id, key);
      let converted = false;
      let order;
      if (quote.status === "CONVERTED") {
        const quoteOrders = ordersOf(quote.id);
        if (quoteOrders.length === 0) {
          respondJson(response, 409, { error: "this quote is converted but no order is recorded against it; look it up before taking payment" });
          return;
        }
        if (quoteOrders.length > 1) {
          respondJson(response, 409, { error: `this quote became ${quoteOrders.length} orders; take the payment from the order's own document` });
          return;
        }
        order = quoteOrders[0];
      } else if (quote.status === "ACCEPTED") {
        if (!cash) {
          respondJson(response, 409, { error: "convert this quote to an order first; a customer with a credit account converts through the conversion door and then pays" });
          return;
        }
        if (amount !== quote.total_cents) {
          respondJson(response, 400, {
            error: `a customer with no credit account pays the whole agreed total of ${coreMoney(quote.total_cents)}, a balance left on the invoice would be credit the customer does not have`,
          });
          return;
        }
        quote.status = "CONVERTED";
        quote.fulfilment = body.fulfillment === "WILL_CALL" ? "WILL_CALL" : "PICKUP";
        conversions.push({ quote_id: quote.id, fulfillment: body.fulfillment ?? null });
        order = newOrder(quote, "COUNTER");
        converted = true;
      } else {
        respondJson(response, 409, { error: `this quote is ${quote.status.toLowerCase()}; only a converted quote's order takes a payment` });
        return;
      }
      // The replay is told apart first and answered with the stored payment, so a replay is never refused for a
      // balance it has since moved.
      const stored = orderPayments.get(namespaced);
      if (stored === undefined && switches.paymentKeyCrossed) {
        // The crossed key: this key already took a DIFFERENT payment than the one posted. The
        // phantom is a real stored CARD 5.00 applied to the invoice, so a later family read
        // shows what landed and only a NEW key can take another payment.
        const crossedInvoice = raiseAgreedInvoice(order);
        paymentCounter += 1;
        crossedInvoice.balance_cents -= 500;
        crossedInvoice.status = crossedInvoice.balance_cents === 0 ? "PAID" : "PARTIALLY_PAID";
        orderPayments.set(namespaced, {
          method: "CARD",
          amount_cents: 500,
          answer: {
            order_id: order.id,
            order_no: order.order_no,
            invoice_id: crossedInvoice.id,
            invoice_no: crossedInvoice.invoice_no,
            invoice_total_cents: crossedInvoice.total_cents,
            payment_id: `pay-${paymentCounter}`,
            payment_no: `P-${String(paymentCounter).padStart(4, "0")}`,
            method: "CARD",
            amount_cents: 500,
            received_at: new Date().toISOString(),
            reference: "CROSSED",
            received_by: userId,
            balance_cents: crossedInvoice.balance_cents,
            replayed: false,
            converted: false,
          },
        });
        respondJson(response, 409, { error: "this idempotency key took a different payment; use a new key for a new payment" });
        return;
      }
      if (stored !== undefined) {
        if (stored.method !== method || stored.amount_cents !== amount) {
          respondJson(response, 409, { error: "this idempotency key took a different payment; use a new key for a new payment" });
          return;
        }
        const live = agreedInvoiceOf(order);
        respondJson(response, 200, {
          ...stored.answer,
          balance_cents: live !== null ? live.balance_cents : stored.answer.balance_cents,
          replayed: true,
        });
        return;
      }
      // An invoice raised another way refuses the whole action: the order is never billed twice.
      const foreign = invoicesOf(order.id).find((i) => i.source !== "COUNTER");
      if (foreign !== undefined) {
        respondJson(response, 409, {
          error: `this order is already invoiced as ${foreign.invoice_no} by another route, so the counter cannot bill it again; hand the customer to Payables and receivables, or to the General Manager or Financial controller, who record the payment against that invoice`,
        });
        return;
      }
      const invoice = raiseAgreedInvoice(order);
      if (cash && amount !== invoice.balance_cents) {
        if (invoice.balance_cents === 0) {
          // The core's own words for the paid order (order_payment.go): the order is paid, never a
          // balance of $0.00 asked for. One wording for the state, here and in the core.
          respondJson(response, 400, { error: "The order is paid in full." });
          return;
        }
        if (invoice.balance_cents === invoice.total_cents) {
          respondJson(response, 400, { error: `a customer with no credit account pays the whole agreed total of ${coreMoney(invoice.total_cents)}` });
        } else {
          respondJson(response, 400, { error: `a customer with no credit account pays the whole balance of ${coreMoney(invoice.balance_cents)}` });
        }
        return;
      }
      if (amount > invoice.balance_cents) {
        respondJson(response, 400, { error: `the payment is more than the balance of ${coreMoney(invoice.balance_cents)}` });
        return;
      }
      paymentCounter += 1;
      invoice.balance_cents -= amount;
      invoice.status = invoice.balance_cents === 0 ? "PAID" : "PARTIALLY_PAID";
      const answer = {
        order_id: order.id,
        order_no: order.order_no,
        invoice_id: invoice.id,
        invoice_no: invoice.invoice_no,
        invoice_total_cents: invoice.total_cents,
        payment_id: `pay-${paymentCounter}`,
        payment_no: `P-${String(paymentCounter).padStart(4, "0")}`,
        method,
        amount_cents: amount,
        received_at: new Date().toISOString(),
        reference,
        received_by: userId,
        balance_cents: invoice.balance_cents,
        replayed: false,
        converted,
      };
      orderPayments.set(namespaced, { method, amount_cents: amount, answer });
      payments.push({ quote_id: quote.id, body, header: request.headers["idempotency-key"] ?? null });
      // The ghost: the payment was taken and stored, but the answer never reaches the desk.
      if (switches.paymentGhost) {
        respondJson(response, 500, { error: "internal" });
        return;
      }
      respondJson(response, 200, answer);
    });
    return;
  }

  match = /^\/api\/v1\/quotes\/([^/]+)\/lines\/(\d+)\/floor-approval$/.exec(path);
  if (match !== null && request.method === "POST") {
    const quote = quoteOr404(match[1]);
    if (quote === null) {
      respondJson(response, 404, { error: "no quote with that id" });
      return;
    }
    const lineNo = Number(match[2]);
    readBody(request, response, (body) => {
      const line = quote.lines.find((row) => row.line_no === lineNo);
      if (line === undefined) {
        respondJson(response, 404, { error: "no line with that number" });
        return;
      }
      const disc = Number(body.disc_pct);
      const product = productById.get(line.product_id);
      const customer = customerById.get(quote.customer_id);
      const decision = floorDecision(caller, product, customer, disc);
      if (decision === null) {
        respondJson(response, 409, { error: "quote: nothing to approve: the asked discount does not land below the floor" });
        return;
      }
      if (decision.cleared) {
        respondJson(response, 201, { filed: false, may_clear_itself: true });
        return;
      }
      const key = `${quote.id}:${lineNo}`;
      const standing = floorRequests.get(key);
      if (standing !== undefined && standing.status !== "REJECTED") {
        // The core answers a standing request as filed:false with the request.
        respondJson(response, 201, { filed: false, may_clear_itself: false, request: standing });
        return;
      }
      const request = { id: `fr-${key}`, status: "PENDING", line_no: lineNo, disc_pct: disc };
      floorRequests.set(key, request);
      const askedUnit = netUnitCents(ruleUnitCents(product, customer), disc);
      const askedMargin = marginBps(askedUnit, product.branch_cost_cents[POINT_LOMA_ID] ?? 0);
      // The detail is the server's own sentence, built from its own figures: the
      // line, the discount and the margin against the floor. The core files it
      // for the filer, who reads it in the filer's own card (qd1c).
      approvalCards.set(request.id, {
        quote_id: quote.id,
        requested_by: userId,
        // The core's own shape, an id in parentheses after the line included: the desk says it in words.
        detail: `quote ${quote.quote_no} line ${lineNo} (a7000000-0000-4000-9000-${String(PRODUCTS.indexOf(product) + 1).padStart(12, "0")}): ${disc} percent discount` +
          `, resulting margin ${askedMargin} bps against the ${product.floor_bps + switches.floorRaiseBps} bps floor`,
      });
      respondJson(response, 201, { filed: true, may_clear_itself: false, request });
    });
    return;
  }

  if (path === "/api/v1/quotes/settings/acceptance" && request.method === "GET") {
    if (!isSettingsAdmin(caller)) {
      respondJson(response, 403, { error: "quote: only a sales manager or the general manager changes the quote settings" });
      return;
    }
    respondJson(response, 200, acceptance);
    return;
  }
  if (path === "/api/v1/quotes/settings/acceptance" && request.method === "PUT") {
    readBody(request, response, (body) => {
      // The core's order: the settings authority first (a sales manager or the general manager), then the
      // dealer default row, which the general manager alone writes.
      if (!isSettingsAdmin(caller)) {
        respondJson(response, 403, { error: "quote: only a sales manager or the general manager changes the quote settings" });
        return;
      }
      if (!isGeneralManager(caller)) {
        respondJson(response, 403, { error: "quote: only the general manager changes the dealer default quote settings" });
        return;
      }
      if (typeof body.allow_multi_accept !== "boolean") {
        respondJson(response, 400, { error: "quote: allow_multi_accept is required" });
        return;
      }
      acceptance.allow_multi_accept = body.allow_multi_accept;
      respondJson(response, 200, acceptance);
    });
    return;
  }

  if (path === "/api/v1/quote-assist/scope-rules" && request.method === "GET") {
    if (!isAdmin(caller)) {
      respondJson(response, 403, { error: "quoteassist: only an administrator reads the scope rules" });
      return;
    }
    respondJson(response, 200, { rules: scopeRules });
    return;
  }
  match = /^\/api\/v1\/quote-assist\/scope-rules\/([^/]+)$/.exec(path);
  if (match !== null && request.method === "PATCH") {
    readBody(request, response, (body) => {
      if (!isAdmin(caller)) {
        respondJson(response, 403, { error: "quoteassist: only an administrator changes the scope rules" });
        return;
      }
      const rule = scopeRules.find((row) => row.code === match[1]);
      if (rule === undefined) {
        respondJson(response, 404, { error: "no scope rule with that code" });
        return;
      }
      if (typeof body.enabled !== "boolean") {
        respondJson(response, 400, { error: "quoteassist: enabled is required" });
        return;
      }
      rule.enabled = body.enabled;
      respondJson(response, 200, rule);
    });
    return;
  }

  if (path === "/api/v1/surfaces/quotes.builder/items" && request.method === "PUT") {
    readBody(request, response, (body) => {
      if (!isSettingsAdmin(caller)) {
        respondJson(response, 403, { error: "settings: only an administrator saves the dealer's screen layout" });
        return;
      }
      if (!["counter", "yard", "driver", "manager", "admin"].includes(body.profile) || !Array.isArray(body.items)) {
        respondJson(response, 400, { error: "settings: a profile and its items are required" });
        return;
      }
      roleItemsByProfile.set(body.profile, body.items);
      itemsWrites.push({ user_id: userId, body: { profile: body.profile, count: body.items.length } });
      respondJson(response, 200, { saved: true });
    });
    return;
  }

  if (request.method === "GET" && path === "/api/v1/products/search") {
    const q = (url.searchParams.get("q") ?? "").toLowerCase();
    const limit = Number(url.searchParams.get("limit") ?? 10);
    const items = PRODUCTS.filter((product) => [product.sku, product.name, ...product.aliases].join(" ").toLowerCase().includes(q))
      .slice(0, Number.isInteger(limit) && limit > 0 ? limit : 10)
      .map((product) => ({ product_id: product.product_id, sku: product.sku, name: product.name, category: product.category, base_uom: product.sold_by }));
    respondJson(response, 200, { items });
    return;
  }

  // The surface settings kit.
  match = /^\/api\/v1\/surfaces\/([^/]+)\/config$/.exec(path);
  if (match !== null && request.method === "GET" && match[1] === SURFACE_ID) {
    const policy = effectivePolicy(caller.branchId);
    const workspace = workspaceRow(userId, policy);
    const asked = url.searchParams.get("profile");
    respondJson(response, 200, {
      surface_id: SURFACE_ID,
      // The core maps the Sales manager to the manager profile (migration
      // 0140), never admin; the app offers the settings entry to everyone and
      // the server answers who may write.
      profile: caller.account.role_code === "sales_manager" ? "manager" : "staff",
      role_items: switches.convertWall
        ? [{ kind: "action", id: "quote.convert_order", position: 0, visible: false }]
        : (roleItemsByProfile.get(asked ?? profileOf(caller)) ?? []),
      user_items: myItems.get(userId) ?? [],
      workspace: { ...workspace, open_ref: workspace.open_ref === "" ? undefined : workspace.open_ref },
      policy,
    });
    return;
  }

  match = /^\/api\/v1\/surfaces\/([^/]+)\/my-workspace$/.exec(path);
  if (match !== null && match[1] === SURFACE_ID) {
    if (request.method === "PUT") {
      readBody(request, response, (body) => {
        const current = workspaceRow(userId, POLICY);
        const row = {
          split_pct: Number.isFinite(body.split_pct) ? body.split_pct : current.split_pct,
          arrangement: body.arrangement === "PRIMARY_RIGHT" ? "PRIMARY_RIGHT" : "PRIMARY_LEFT",
          primary_collapsed: body.primary_collapsed === true,
          secondary_collapsed: body.secondary_collapsed === true,
          cost_hidden: body.cost_hidden === true,
          open_ref: typeof body.open_ref === "string" ? body.open_ref : "",
        };
        workspaces.set(userId, row);
        workspaceWrites.push({ user_id: userId, body: { ...body } });
        respondJson(response, 200, { ...row, source: "user" });
      });
      return;
    }
    if (request.method === "DELETE") {
      workspaces.delete(userId);
      workspaceWrites.push({ user_id: userId, body: null });
      respondJson(response, 200, { reset: true });
      return;
    }
  }

  match = /^\/api\/v1\/surfaces\/([^/]+)\/my-items$/.exec(path);
  if (match !== null && match[1] === SURFACE_ID) {
    if (request.method === "PUT") {
      readBody(request, response, (body) => {
        myItems.set(userId, Array.isArray(body.items) ? body.items : []);
        itemsWrites.push({ user_id: userId, body: { ...body } });
        respondJson(response, 200, { saved: true });
      });
      return;
    }
    if (request.method === "DELETE") {
      myItems.delete(userId);
      itemsWrites.push({ user_id: userId, body: null });
      respondJson(response, 200, { reset: true });
      return;
    }
  }

  // The catalogue and account reads the shared kit's components speak.
  if (request.method === "GET" && path === "/api/v1/products") {
    respondJson(
      response,
      200,
      PRODUCTS.map((product) => ({
        id: product.product_id,
        sku: product.sku,
        name: product.name,
        color_family: product.category,
        application: product.category,
        sold_by: product.sold_by,
        base_uom: product.base_uom,
        is_active: true,
        list_price_cents: product.list_cents,
        list_price_uom: product.sold_by,
        on_hand_by_branch: { [POINT_LOMA_ID]: 400 },
        is_featured: product.featured,
      })),
    );
    return;
  }

  if (request.method === "GET" && path === "/api/v1/products/counter-search") {
    const q = (url.searchParams.get("q") ?? "").toLowerCase();
    const hits = PRODUCTS.filter((product) => {
      const hay = [product.sku, product.name, product.category, product.description, ...product.aliases].join(" ").toLowerCase();
      return q === "" || hay.includes(q);
    }).map((product) => ({
      product_id: product.product_id,
      sku: product.sku,
      name: product.name,
      description: product.description,
      base_uom: product.base_uom,
      sold_by: product.sold_by,
      has_image: false,
      match_reason: "NAME",
      on_hand_branch: 400,
      on_hand_by_branch: { [POINT_LOMA_ID]: 400 },
      list_price_cents: product.list_cents,
    }));
    respondJson(response, 200, { items: hits });
    return;
  }

  if (request.method === "POST" && path === "/api/v1/pos/counter-prices") {
    // The customer aware price picture: the server's answer, one entry per asked product. The account's own
    // rule is the customer's price and retail stands beside it.
    readBody(request, response, (body) => {
      const customer = customerById.get(String(body.customer_id ?? ""));
      const items = Array.isArray(body.items) ? body.items : [];
      const prices = [];
      for (const item of items) {
        const product = productById.get(String(item.product_id ?? ""));
        if (product === undefined) continue;
        const off = customer === undefined ? 0 : customer.percent_off;
        const customerCents = Math.round((product.list_cents * (100 - off)) / 100);
        prices.push({
          product_id: product.product_id,
          price_uom: product.sold_by,
          retail_cents: product.list_cents,
          customer_cents: customerCents,
          saving_cents: product.list_cents - customerCents,
          basis: off > 0 ? "ACCOUNT" : "",
        });
      }
      respondJson(response, 200, { customer_type: customer === undefined ? "" : customer.customer_type, prices });
    });
    return;
  }

  if (request.method === "GET" && path === "/api/v1/products/scan") {
    const code = (url.searchParams.get("code") ?? "").trim().toLowerCase();
    const product = PRODUCTS.find((row) => row.sku.toLowerCase() === code || row.name.toLowerCase() === code || row.aliases.some((alias) => alias.trim().toLowerCase() === code));
    if (product === undefined) {
      respondJson(response, 404, { error: "no product answers that code" });
      return;
    }
    respondJson(response, 200, {
      product_id: product.product_id,
      sku: product.sku,
      name: product.name,
      description: product.description,
      base_uom: product.base_uom,
      sold_by: product.sold_by,
      has_image: false,
      match_reason: "SKU_PREFIX",
      on_hand_branch: 400,
      on_hand_by_branch: { [POINT_LOMA_ID]: 400 },
      list_price_cents: product.list_cents,
    });
    return;
  }

  match = /^\/api\/v1\/products\/([^/]+)\/image$/.exec(path);
  if (match !== null) {
    // The stub carries no photo bytes: the thumb latches to its placeholder.
    respondJson(response, 404, { error: "no image" });
    return;
  }

  if (request.method === "GET" && path === "/api/v1/customers/search") {
    const q = (url.searchParams.get("q") ?? "").toLowerCase();
    const rows = CUSTOMERS.filter((customer) => customer.name.toLowerCase().includes(q) || customer.account_no.toLowerCase().includes(q));
    respondJson(response, 200, rows);
    return;
  }

  if (request.method === "GET" && path === "/api/v1/pos/account-standing") {
    const asked = url.searchParams.get("customer_id") ?? "";
    // The contract's PosAccountStanding: the standing is always said (walk_in for no account on file).
    respondJson(response, 200, HELD_STANDINGS.get(asked) ?? { standing: customerById.has(asked) && (customerById.get(asked)?.credit_limit_cents ?? 0) > 0 ? "clear" : "walk_in" });
    return;
  }

  // PR 239's payment grant: whether this caller's role may take a payment at all.
  if (request.method === "GET" && path === "/api/v1/pos/payment-grant") {
    respondJson(response, 200, { can_take_payment: !switches.paymentGrantRefused });
    return;
  }

  // PR 239's customer by id read: the limit and the balance the standing step names.
  match = /^\/api\/v1\/customers\/([^/]+)$/.exec(path);
  if (match !== null && request.method === "GET" && match[1] !== "search") {
    const customer = customerById.get(decodeURIComponent(match[1]));
    if (customer === undefined) {
      respondJson(response, 404, { error: "no customer with that id" });
      return;
    }
    respondJson(response, 200, {
      customer_id: customer.id,
      account_no: customer.account_no,
      name: customer.name,
      customer_type: customer.customer_type,
      branch_id: POINT_LOMA_ID,
      payment_terms_days: customer.payment_terms_days,
      credit_limit_cents: customer.credit_limit_cents,
      on_hold: HELD_STANDINGS.get(customer.id)?.on_hold === true,
      tax_exempt: false,
      balance_cents: customer.open_balance_cents,
      is_active: true,
    });
    return;
  }

  if (request.method === "GET" && path === "/api/v1/pos/salespeople") {
    respondJson(response, 200, {
      items: [
        { id: USERS.sales.user_id, name: USERS.sales.display_name },
        { id: USERS["sales-manager"].user_id, name: USERS["sales-manager"].display_name },
      ],
    });
    return;
  }

  if (request.method === "GET" && path === "/api/v1/pos/addon-suggestions") {
    // The reference's own strip: a paver course suggests the jointing sand and
    // the edge restraint beside it. Suggestions ride the product ids the
    // caller names, so a quote with no pavers answers none and the strip
    // hides, exactly as the empty answer is configured to.
    const asked = (url.searchParams.get("product_ids") ?? "").split(",").filter((id) => id !== "");
    const suggestsPavers = asked.some((id) => productById.get(id)?.category === "Pavers");
    const suggestions = suggestsPavers
      ? [
          {
            product_id: "p-polymer-sand",
            sku: "PSN-100",
            name: "Polymeric Locking Sand",
            size_line: "One bag covers about 40 square feet",
            uom: "bag",
            default_qty: 2,
            list_price_cents: 1400,
            price_uom: "bag",
            has_image: false,
            application: "Sand and base",
          },
          {
            product_id: "p-edge-8",
            sku: "EDG-08",
            name: "Edge Restraint 8 ft",
            size_line: "One strip per eight feet of edge",
            uom: "pc",
            default_qty: 10,
            list_price_cents: 3200,
            price_uom: "pc",
            has_image: false,
            application: "Edging",
          },
        ]
      : [];
    respondJson(response, 200, { suggestions });
    return;
  }

  // ── the third coder's panels' routes ───────────────────────────────────────

  if (path === "/api/v1/quote-assist/extract" && request.method === "POST") {
    readBody(request, response, (body) => {
      if (typeof body.text !== "string" || body.text.trim() === "" || typeof body.branch_id !== "string" || typeof body.use_ai !== "boolean") {
        respondJson(response, 400, { error: "quoteassist: text, branch_id and use_ai are required" });
        return;
      }
      respondJson(response, 200, extractOf(body.text, body.use_ai));
    });
    return;
  }
  if (path === "/api/v1/quote-assist/scope-gaps" && request.method === "GET") {
    const quote = quoteOr404(url.searchParams.get("quote_id") ?? "");
    if (quote === null) {
      respondJson(response, 404, { error: "no quote with that id" });
      return;
    }
    respondJson(response, 200, scopeGapsOf(quote));
    return;
  }

  match = /^\/api\/v1\/quotes\/([^/]+)\/followups(?:\/(disable|enable))?$/.exec(path);
  if (match !== null) {
    const quote = quoteOr404(match[1]);
    if (quote === null) {
      respondJson(response, 404, { error: "no quote with that id" });
      return;
    }
    if (match[2] === undefined && request.method === "GET") {
      respondJson(response, 200, followUpView(quote));
      return;
    }
    if (match[2] !== undefined && request.method === "POST") {
      const plan = followPlans.get(quote.id) ?? (quote.status === "SENT" ? ensurePlan(quote) : undefined);
      if (plan === undefined) {
        respondJson(response, 409, { error: "quote: this quote has no follow-ups to change; they begin when the quote is sent" });
        return;
      }
      if (match[2] === "disable") {
        plan.enabled = false;
        plan.disabled_at = new Date().toISOString();
        plan.disabled_why = `Stopped by ${caller.account.display_name}`;
      } else {
        plan.enabled = true;
        delete plan.disabled_at;
        delete plan.disabled_why;
      }
      respondJson(response, 200, followUpView(quote));
      return;
    }
  }

  match = /^\/api\/v1\/quotes\/([^/]+)\/hold-expiry$/.exec(path);
  if (match !== null && request.method === "GET") {
    const quote = quoteOr404(match[1]);
    if (quote === null) {
      respondJson(response, 404, { error: "no quote with that id" });
      return;
    }
    respondJson(response, 200, holdExpiryView(quote));
    return;
  }
  match = /^\/api\/v1\/quotes\/([^/]+)\/holds\/resync$/.exec(path);
  if (match !== null && request.method === "POST") {
    const quote = quoteOr404(match[1]);
    if (quote === null) {
      respondJson(response, 404, { error: "no quote with that id" });
      return;
    }
    if (quote.expired) {
      respondJson(response, 409, { error: "quote: this quote has expired; extend its expiry date before holding stock again" });
      return;
    }
    const lapsed = lapsedHolds.get(quote.id) ?? [];
    lapsedHolds.delete(quote.id);
    respondJson(response, 200, { holds_applied: lapsed.length });
    return;
  }

  match = /^\/api\/v1\/quotes\/([^/]+)\/availability$/.exec(path);
  if (match !== null && request.method === "GET") {
    const quote = quoteOr404(match[1]);
    if (quote === null) {
      respondJson(response, 404, { error: "no quote with that id" });
      return;
    }
    respondJson(response, 200, {
      lines: quote.lines.map((line) => {
        const branch = branchStockRow(line.product_id, quote.branch_id, caller);
        return {
          line_no: line.line_no,
          product_id: line.product_id,
          qty_base: Number(line.qty_base),
          grade: gradeOf(branch.available_base, Number(line.qty_base)),
          branch,
        };
      }),
      seam_wired: seamSwitches.seamWired,
    });
    return;
  }
  match = /^\/api\/v1\/quote-availability\/([^/]+)\/branches$/.exec(path);
  if (match !== null && request.method === "GET") {
    if (!productById.has(match[1])) {
      respondJson(response, 404, { error: "no product with that id" });
      return;
    }
    respondJson(response, 200, {
      branches: [branchStockRow(match[1], POINT_LOMA_ID, caller), branchStockRow(match[1], JULIAN_ID, caller)],
      seam_wired: true,
    });
    return;
  }

  if (path === "/api/v1/staff/policy/check" && request.method === "POST") {
    // The core's order (identity staffcontext, policyCheck): the body, then the caller's branch grants, then
    // the margin against the 12% floor with the caller's own approved override; below the floor with no
    // override is a 403 whose error is the block's sentence and whose block key carries the action block.
    readBody(request, response, (body) => {
      if (!Number.isInteger(body.price_cents) || !Number.isInteger(body.cost_cents) || typeof body.branch_id !== "string" || body.branch_id === "") {
        respondJson(response, 400, { error: "price_cents, cost_cents and branch_id are required" });
        return;
      }
      if (!UUID_SHAPE.test(body.branch_id)) {
        respondJson(response, 400, { error: "branch_id must be a branch id" });
        return;
      }
      if (body.price_cents < 0 || body.cost_cents < 0 || body.price_cents > MAX_CHECK_CENTS || body.cost_cents > MAX_CHECK_CENTS) {
        respondJson(response, 400, { error: "price_cents and cost_cents must be between 0 and 1000000000000" });
        return;
      }
      // A global caller reaches any branch; every stub account is branch scoped today, so this is its own branch.
      if (caller.account.scope !== "GLOBAL" && body.branch_id !== caller.branchId) {
        respondJson(response, 403, { error: "that branch is outside your grants" });
        return;
      }
      const margin = marginBps(body.price_cents, body.cost_cents);
      // The caller's own APPROVED request at the branch that no line write has used up yet (the core's
      // HasUnusedApprovedOverride).
      let override = false;
      for (const row of floorRequests.values()) {
        const card = approvalCards.get(row.id);
        const quote = card === undefined ? undefined : quotes.get(card.quote_id);
        if (row.status === "APPROVED" && row.used !== true && card !== undefined && card.requested_by === caller.account.user_id && (quote === undefined ? TRENTON_ID : quote.branch_id) === body.branch_id) override = true;
      }
      if (margin < MARGIN_FLOOR_BPS && !override) {
        const detail = `margin ${margin} bps is below the ${MARGIN_FLOOR_BPS} bps floor`;
        respondJson(response, 403, {
          error: detail,
          block: { policy_type: "MARGIN_BELOW_MINIMUM", branch_id: body.branch_id, detail, data: { margin_bps: String(margin), floor_bps: String(MARGIN_FLOOR_BPS) } },
        });
        return;
      }
      respondJson(response, 200, { allowed: true, margin_bps: margin, override });
    });
    return;
  }

  if (path === "/api/v1/staff/approvals" && request.method === "GET") {
    const wanted = url.searchParams.get("status") ?? "";
    const items = [];
    for (const [key, row] of floorRequests) {
      const card = approvalCards.get(row.id);
      if (card === undefined) continue;
      if (wanted !== "" && row.status !== wanted) continue;
      const quote = quotes.get(card.quote_id);
      items.push({
        id: row.id,
        requested_by: card.requested_by,
        branch_id: quote === undefined ? POINT_LOMA_ID : quote.branch_id,
        policy_type: "MARGIN_BELOW_MINIMUM",
        detail: card.detail,
        subject_ref: `quote:${card.quote_id}`,
        status: row.status,
        ...(card.decided_by === undefined ? {} : { decided_by: card.decided_by }),
      });
      void key;
    }
    if (switches.approvalsShape === "bare") {
      respondJson(response, 200, items);
      return;
    }
    if (switches.approvalsShape === "paged") {
      const start = Number(url.searchParams.get("cursor") ?? "0") || 0;
      const next = start + 1;
      respondJson(response, 200, { items: items.slice(start, next), ...(next < items.length ? { next_cursor: String(next) } : {}) });
      return;
    }
    respondJson(response, 200, { items });
    return;
  }
  match = /^\/api\/v1\/staff\/approvals\/([^/]+)\/decide$/.exec(path);
  if (match !== null && request.method === "POST") {
    readBody(request, response, (body) => {
      // The id rides the path percent encoded (the stub's ids carry a colon).
      const approvalId = decodeURIComponent(match[1]);
      const card = approvalCards.get(approvalId);
      const row = [...floorRequests.values()].find((r) => r.id === approvalId);
      if (card === undefined || row === undefined) {
        respondJson(response, 404, { error: "no approval with that id" });
        return;
      }
      if (typeof body.approve !== "boolean" || typeof body.reason !== "string") {
        respondJson(response, 400, { error: "identity: approve and reason are required" });
        return;
      }
      if (caller.account.role_code !== "sales_manager") {
        respondJson(response, 403, { error: "identity: deciding a margin floor request is for the branch's Sales manager, the General Manager or the Financial Controller" });
        return;
      }
      if (card.requested_by === caller.account.user_id) {
        respondJson(response, 403, { error: "separation of duties: you cannot approve your own request" });
        return;
      }
      if (row.status !== "PENDING") {
        respondJson(response, 409, { error: "identity: this request has already been decided" });
        return;
      }
      row.status = body.approve ? "APPROVED" : "REJECTED";
      card.decided_by = caller.account.user_id;
      card.reason = body.reason;
      decisions.push({ id: approvalId, approve: body.approve, reason: body.reason });
      respondJson(response, 200, { status: row.status });
    });
    return;
  }

  respondJson(response, 404, { error: `no stub route for ${request.method} ${path}` });
});

server.on("error", (error) => {
  console.error(`qd-stub: ${error instanceof Error ? error.message : String(error)}`);
  process.exit(1);
});

server.listen(port, "127.0.0.1", () => {
  console.log(`qd-stub listening on 127.0.0.1:${port}`);
});
