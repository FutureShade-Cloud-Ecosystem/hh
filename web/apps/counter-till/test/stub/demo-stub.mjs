// The local demo stub: the demo routes, the counter's routes and the till's
// return paths in the contract's shapes, for the dev server's /api proxy and
// the Playwright harness. This is a development and test double only: the
// real routes are the core's (the mint is the demo build's, the pricing is
// the engine's, the returns rules are RT-B's), and nothing in the app imports
// this file. When the real routes land, the app and this suite keep working
// against them unchanged, because both speak only the contract's shapes:
//   GET  /api/v1/demo/flows                 -> DemoFlowList
//   POST /api/v1/demo/session               -> DemoSessionResponse
//   GET  /api/v1/branches                   -> Branch[]
//   GET  /api/v1/till/sessions/open         -> TillOpenLookup
//   POST /api/v1/till/sessions              -> TillOpenResult
//   GET  /api/v1/till/sessions/{id}         -> TillSessionDetail
//   GET  /api/v1/till/sessions/{id}/zreport -> TillZReport
//   GET  /api/v1/products/counter-search    -> ProductCounterSearchResults
//   GET  /api/v1/customers/search           -> CustomerSearchResult[]
//   GET  /api/v1/pos/account-standing       -> PosAccountStanding
//   POST /api/v1/pos/counter-prices         -> PosCounterPrices
//   POST /api/v1/pos/margin-glance          -> PosMarginGlance
//   GET  /api/v1/payments/config            -> PaymentConfigStatus
//   POST /api/v1/pos/sales                  -> PosSale
//   GET  /api/v1/pos/sales/{id}             -> PosSaleView
//   POST /api/v1/pos/parked                 -> PosParkedTicket
//   GET  /api/v1/pos/parked                 -> PosParkedTicketPage
//   POST /api/v1/pos/parked/{id}/resume     -> PosParkedTicket
//   DELETE /api/v1/pos/parked/{id}          -> PosParkedDiscard
//   GET  /api/v1/pos/returns/customer-lines -> PosCustomerLines (422 for the house WALK-IN)
//   POST /api/v1/pos/returns/preview        -> PosReturnPreview (the commit's own pricing, nothing written)
//   POST /api/v1/pos/swaps/preview          -> PosSwapPreview (the credit, the new order, applied, leftover, shortfall)
//   POST /api/v1/pos/swaps                  -> PosSwapResult (409 changed, 502 processor, 409 approval on the note total)
//   POST /api/v1/pos/returns-across         -> PosReturnNote (409 PosRefundApprovalRequired on the note total)
//   GET  /api/v1/pos/policy                 -> PosPolicy
//   GET  /api/v1/pos/workspace              -> PosWorkspacePrefs (PUT writes, DELETE resets)
//   GET  /api/v1/pos/hotkeys                -> PosHotkeys (the shipped default: no keys)
//   GET  /api/v1/pos/salespeople            -> { items: [{id, name}] }
//   GET  /api/v1/pos/discount-approvers     -> { items: [{id, name, role_label}] }
//   GET  /api/v1/pos/quick-picks            -> PosQuickPicks (the demo dealer's three-tile hot list)
//   GET  /api/v1/products                   -> PosProduct[] (the browse grid's read)
//   GET  /api/v1/products/scan              -> CounterHit | 404
//   GET  /api/v1/pos/stock-glance           -> StockGlance
//   GET  /api/v1/pos/addon-suggestions      -> { suggestions: [] }
//   POST /api/v1/pricing/resolve            -> PricingBreakdown
//   POST /api/v1/pos/signatures(/typed)     -> { signature_ref }
//   POST /api/v1/pos/assess-relief          -> ReliefOutcome (the standard rate)
//   GET  /api/v1/pos/sales/{id}/receipt     -> the rendered HTML document (format=roll, the 80 mm
//                                              counter receipt, or format=page, the full-page
//                                              document: the demo mirror of the core's own two)
// The demo routes are public in the contract (security: []), so they check no
// credential; the phrase gate in front of the real demo box is Caddy's (W5's
// image), not the routes'. The staff routes are not public, and they answer
// behind two readers, one per lane's suite. The counter's routes read the
// minted bearer the way the core's middleware does (the token's own sub
// claim), hold the demo org header beside it, and refuse a call without
// either, so the app's header plumbing is exercised exactly as it would be
// against the real server. The returns desk's customer lines read and the Z
// report answer the bearer alone and read the account back out of the token
// this stub minted itself: what the real core resolves from the verified
// token, this stub reads from its own mint.
//
// The mint answers a JWT-shaped token so the app's expiry read (the token's
// exp claim) sees the same shape the real mint issues: three segments, a
// payload with sub and exp, and a signature segment. The signature here is
// stub bytes; on the real box it is the demo issuer's, verified by the core's
// verifier, which this stub stands in for.
//
// Money is integer cents throughout, as the house invariant asks, computed
// here (the stub stands in for the core, which is the only place money is
// ever computed) and never in the app. Quantities ride as the contract's
// numbers, every one carrying its unit of measure.
import { createSyncStub } from "./sync-stub.mjs";
import { createServer } from "node:http";

const port = Number(process.env.CT_STUB_PORT ?? 4821);

// The stub's clock. It reads the host's, unless a leg pins it (POST /_stub/clock with {"now"}):
// the dealer's day the world is built on must be able to stand at the evening instant at any wall
// hour, the same pin the drive run's stub takes, so every read of the clock below goes through
// nowMs(). Unpinned legs see the host clock exactly as before.
let clockOffsetMs = 0;
const nowMs = () => Date.now() + clockOffsetMs;

// The closed flow table, mirroring the core's (core/internal/demo/flows):
// the Counter Till's pair, the yard manager, and the personas and roles the
// roles roadmap brought live. The till's entry offers a card for its own
// pair alone; the listing still answers the whole table, as the core's route
// does (the mint's role label comes from this table, never from the roles
// table).
const FLOWS = [
  { flow: "sales", role_code: "inside_sales", label: "Sales" },
  { flow: "sales-manager", role_code: "sales_manager", label: "Sales manager" },
  { flow: "yard-manager", role_code: "yard_manager", label: "Yard manager" },
  { flow: "yard-team", role_code: "yard_team", label: "Yard Team" },
  { flow: "driver", role_code: "driver", label: "Driver" },
  { flow: "payables-receivables", role_code: "payables_receivables", label: "Payables and Receivables" },
  { flow: "procurement-manager", role_code: "procurement_manager", label: "Procurement manager" },
  { flow: "financial-controller", role_code: "financial_controller", label: "Financial Controller" },
];

// The minted accounts, one per flow, shaped field for field like the staff
// login user block (core/api/openapi.yaml, IdentityUser). The tiers are the
// core resolver's own answer (core/internal/identity/authority.go, read only):
// 1 leadership, 2 manager, 3 frontline; inside_sales is frontline,
// sales_manager is management, and the two personas are frontline, and the
// real mint carries the resolver's live tier, so the stub says the same. The
// accounts are demo labels on reserved domains, not people. Each account's
// grant reaches its own branch only (the
// demo world's Point Loma), which is the branch fence the core's middleware
// holds: the stub reads the same grant from the token's account, so one branch
// never answers for another.
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
  "yard-manager": {
    user_id: "demo-account-yard-manager",
    email: "yard-manager@demo.invalid",
    display_name: "Yard manager (demo)",
    role_code: "yard_manager",
    role_name: "Yard manager",
    tier: 2,
    scope: "BRANCH",
  },
  "yard-team": {
    user_id: "demo-account-yard-team",
    email: "yard-team@demo.invalid",
    display_name: "Yard persona (demo)",
    role_code: "yard_team",
    role_name: "Yard Team",
    tier: 3,
    scope: "BRANCH",
  },
  driver: {
    user_id: "demo-account-driver",
    email: "driver@demo.invalid",
    display_name: "Driver persona (demo)",
    role_code: "driver",
    role_name: "Driver",
    tier: 3,
    scope: "BRANCH",
  },
  "payables-receivables": {
    user_id: "demo-account-payables-receivables",
    email: "payables-receivables@demo.invalid",
    display_name: "Payables and Receivables (demo)",
    role_code: "payables_receivables",
    role_name: "Payables and Receivables",
    tier: 2,
    scope: "GLOBAL",
  },
  "procurement-manager": {
    user_id: "demo-account-procurement-manager",
    email: "procurement-manager@demo.invalid",
    display_name: "Procurement manager (demo)",
    role_code: "procurement_manager",
    role_name: "Procurement Manager",
    tier: 2,
    scope: "BRANCH",
  },
  "financial-controller": {
    user_id: "demo-account-financial-controller",
    email: "financial-controller@demo.invalid",
    display_name: "Financial controller (demo)",
    role_code: "financial_controller",
    role_name: "Financial Controller",
    tier: 1,
    scope: "GLOBAL",
  },
};

// ---------------------------------------------------------------------------
// The invented world: the first dealer's demo box. Two branches, a counter
// catalogue, three accounts. Every identity is an invented label on a reserved
// domain or a doc number, never a real one, and the world's history is dated
// from nothing: the numbers below are counters, not calendar facts.
// ---------------------------------------------------------------------------

const POINT_LOMA_ID = "6f1c2b3a-0000-4000-8000-000000000001";
const JULIAN_ID = "6f1c2b3a-0000-4000-8000-000000000002";

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
  {
    id: JULIAN_ID,
    code: "JUL",
    name: "Julian",
    address: { line1: "8 Quarry Gate Road", city: "Julian", province: "CA", postal: "92036", country: "US" },
    is_default: false,
    is_system_default: false,
    is_active: true,
  },
];

// Each account's grant reaches one branch: the demo logins work the Point Loma
// counter. Julian exists so a fence is observable, never reachable.
const ACCOUNT_BRANCH = {
  "demo-account-sales": POINT_LOMA_ID,
  "demo-account-sales-manager": POINT_LOMA_ID,
  "demo-account-yard-manager": POINT_LOMA_ID,
  "demo-account-procurement-manager": POINT_LOMA_ID,
};

// The counter catalogue. list_cents is the retail anchor; branch_cost_cents is
// the weighted cost the margin glance reads, per branch. The edge restraint
// carries no cost at Point Loma on purpose: a glance line with no cost stays
// byte-absent in the real answer, and the till shows the explicit placeholder
// there, never a fake margin. sold_by is the unit the counter rings in.
// The demo dealer's curated hot list: the quick-pick tiles, in tap order.
const HOT_LIST_SKUS = ["HPV-60", "PSN-100", "WAT-STD"];

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
    branch_cost_cents: { [POINT_LOMA_ID]: 276, [JULIAN_ID]: 268 },
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
    branch_cost_cents: { [POINT_LOMA_ID]: 980, [JULIAN_ID]: 1010 },
  },
  {
    product_id: "p-edge-8",
    sku: "EDG-08",
    category: "Edging",
    name: "Edge Restraint 8 ft",
    description: "Flexible paver edge restraint, eight foot strip",
    aliases: ["edge restraint", " edging"],
    base_uom: "pc",
    sold_by: "pc",
    list_cents: 3200,
    branch_cost_cents: { [JULIAN_ID]: 2450 },
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
    branch_cost_cents: { [POINT_LOMA_ID]: 3900, [JULIAN_ID]: 3860 },
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
    branch_cost_cents: { [POINT_LOMA_ID]: 2900, [JULIAN_ID]: 2950 },
  },
];

// The account customers of the demo world. The house walk-in is deliberately
// absent from this table: the search route excludes it server-side (the
// contract's own words), and a walk-in sale sends no customer at all, so the
// core resolves the house walk-in itself.
// The percent is the account's own pricing rule: C01's is the eighteen percent
// the plan's screen 4 names. basis ACCOUNT marks a rule price, so the counter
// can show what the saving is over retail.
const CUSTOMERS = [
  {
    id: "c-maple-ridge",
    account_no: "C01",
    name: "Maple Ridge Contracting",
    customer_type: "CONTRACTOR",
    payment_terms_days: 30,
    credit_limit_cents: 500000,
    open_balance_cents: 125000,
    percent_off: 18,
    on_hold: false,
  },
  {
    id: "c-fenwick",
    account_no: "C02",
    name: "Fenwick Builders Ltd",
    customer_type: "BUILDER",
    payment_terms_days: 30,
    credit_limit_cents: 300000,
    open_balance_cents: 87500,
    percent_off: 10,
    on_hold: false,
  },
  {
    id: "c-harbour",
    account_no: "C09",
    name: "Harbour Drywall Inc",
    customer_type: "CONTRACTOR",
    payment_terms_days: 15,
    credit_limit_cents: 200000,
    open_balance_cents: 0,
    percent_off: 12,
    on_hold: true,
  },
  // Round 15: DEMO-C07's shape, a retail account with no credit line: the gate refuses it on
  // account, so its payment step is pay before release words that say what the account is.
  {
    id: "c-retail",
    account_no: "C07",
    name: "Homeowner B Retail",
    customer_type: "RETAIL",
    payment_terms_days: 0,
    credit_limit_cents: 0,
    open_balance_cents: 0,
    percent_off: 0,
    on_hold: false,
  },
  // Item 9.9: the pay before release account (zero day terms with a credit line, as the core's C10), the stub's
  // Cash On Demand customer: the preview offers it the deposit with the balance due later.
  {
    id: "c-cod",
    account_no: "C10",
    name: "Cash On Demand Landscaping",
    customer_type: "CONTRACTOR",
    payment_terms_days: 0,
    credit_limit_cents: 500000, // the demo world's own C10 line: Cash On Demand is zero day terms WITH a line
    open_balance_cents: 0,
    percent_off: 0,
    on_hold: false,
  },
];

const WALK_IN = {
  id: "",
  account_no: "WALK-IN",
  name: "Walk-in",
  customer_type: "RETAIL",
  percent_off: 0,
  on_hold: false,
};

/** 26600 reads $266.00, the way the contract's refusal sentence speaks. */
function dollarsWord(cents) {
  return `$${(cents / 100).toLocaleString("en-CA", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}
const HST_RATE_BPS = 1300; // the Ontario rate, as basis points

// The dealer's house zone: the same constant the core itself compiles. The
// ring's requested date must be THIS zone's calendar day, which is the
// invariant "daily means the dealer's day" held at the counter: a till that
// asks in the browser's UTC day is refused, and the two days differ every
// evening house time.
const HOUSE_ZONE = "America/Toronto";

function houseDay(now = new Date(nowMs())) {
  return new Intl.DateTimeFormat("en-CA", {
    timeZone: HOUSE_ZONE,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).format(now);
}

// ---------------------------------------------------------------------------
// The stub's state, held in memory. A restart of the stub is a fresh world,
// which is what the plan's wipe-before-each-round posture means locally.
// ---------------------------------------------------------------------------

let docCounter = 1000;
const nextDocNo = (prefix) => {
  docCounter += 1;
  return `${prefix}-${docCounter}`;
};

const uuidFromCounter = (n) => `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`;
// The returns world below spells this same helper `uuid`: two names, one id
// spelling, because both worlds shape their ids by it.
const uuid = uuidFromCounter;

// One open drawer per branch, station MAIN: the counter's shared drawer.
const drawers = new Map(); // branch_id -> session row
// Committed sales, by order id, in ring order.
const sales = new Map();
const salesByKey = new Map(); // idempotency_key -> order_id
// Item 9.4 to 9.8: the orders that still owe, by order id (the core's pay balance world). Two are
// planted so a tester can walk them: a credit customer's deposit sale (the rest rides the account,
// not held) and the Cash On Demand account's (the rest due later, held from picking until paid).
// A deposit sale rung through the till joins them. Each carries what the core answers: the money
// trail oldest first, the hold words when held, and the lines for the held order card.
const owing = new Map();
const PAY_HOLD_WORDS = "This order cannot reach picking until the balance is paid.";
const IN_PROCESS_WORDS = "In process: deposit taken";
const PAY_BALANCE_KEYS = new Map(); // idempotency_key -> the take's answer body
function addOwing(order) {
  owing.set(order.order_id, order);
}
function owingStill(order) {
  return order.order_total_cents - order.payments.reduce((sum, p) => sum + p.amount_cents, 0);
}
function seedOwingOrders() {
  addOwing({
    order_id: uuidFromCounter(4901),
    order_no: "DEMO-PLM-D01",
    branch_id: POINT_LOMA_ID,
    customer_id: "c-fenwick",
    customer_name: "Fenwick Builders Ltd",
    account_line: "C02 · Builder · on account",
    job: "Marina Road patio",
    fulfilment: "WILL_CALL",
    sale_type: "COLLECT_LATER",
    order_total_cents: 191359,
    held: false,
    payments: [{ payment_id: uuidFromCounter(4911), method: "CARD", amount_cents: 50000 }],
    lines: [{ description: "Charcoal Stone 60 mm", qty: "200 sf", line_total_cents: 169359 }],
  });
  addOwing({
    order_id: uuidFromCounter(4902),
    order_no: "DEMO-PLM-D02",
    branch_id: POINT_LOMA_ID,
    customer_id: "c-cod",
    customer_name: "Cash On Demand Landscaping",
    account_line: "C10 · Contractor · Cash On Demand",
    fulfilment: "WILL_CALL",
    sale_type: "COD",
    order_total_cents: 6102,
    held: true,
    payments: [{ payment_id: uuidFromCounter(4912), method: "CASH", amount_cents: 2000 }],
    lines: [{ description: "Clear Stone", qty: "1 yd", line_total_cents: 5400 }],
  });
}

// Parked tickets, by id. Julian's pre-seeded ticket proves the fence: a
// Point Loma login lists parked tickets for its own branch and never this one.
const parked = new Map();

function seedParkedJulianTicket() {
  const id = uuidFromCounter(9001);
  parked.set(id, {
    id,
    ticket_no: "PARK-9001",
    branch_id: JULIAN_ID,
    customer_id: "c-maple-ridge",
    sale_type: "COLLECT_NOW",
    notes: "Parked at the Julian counter",
    status: "PARKED",
    lines: [{ product_id: "p-charcoal-60", qty_entered: 40, uom_entered: "sf" }],
    created_at: "2026-01-01T00:00:00Z",
  });
}
seedParkedJulianTicket();
seedOwingOrders();

// ---------------------------------------------------------------------------
// The change-order world (change orders stage 1): the order statuses the
// core's transitions move, the change requests raised against rung orders,
// the edits trail a line edit appends to, and the matrix answers the role
// gate gives the two demo logins. The stub stands in for the core, so the
// statuses, the ladder, the threshold and the refusal words below are the
// core's own (order/change.go, order/edit.go, authz BodyForbidden), field by
// field: the till's legs must see what the served core would answer.
// ---------------------------------------------------------------------------

// order_id -> status. A rung order starts CONFIRMED (the ring's own ladder);
// the transitions route below moves it the way the core's manual matrix does.
const ORDER_STATUS = new Map();
// change_id -> one change request row (the core's ChangeOrder shape).
const CHANGES = new Map();
// order_id -> the edits trail rows, oldest first (the core's OrderFieldChange shape).
const EDITS = new Map();
let changeCounter = 0;
let editRequestCounter = 0;
// The SCOPE:F10 default (core change.go defaultFinanceThresholdCents), the
// figure stamped on a new request, |delta| graded against it.
const FINANCE_THRESHOLD_CENTS = 50000;

// The census walls (the routes' x-access rows against the default matrix):
// what each demo login may reach on the change surface. sales is inside_sales
// (quote-desk work: raise, list, read, reject, cancel; no manage cell), and
// sales-manager is sales_manager (quote-desk manage: the line edits and the
// apply beside them). Neither holds a yard-pick or money-desk manage cell, so
// the yard ack and the finance signature answer the shared Forbidden word
// exactly as the served core's role gate answers them.
function changeWalls(caller) {
  const account = Object.values(USERS).find((u) => u.user_id === caller.userId) ?? null;
  const manager = account !== null && account.tier <= 2;
  return {
    manager,
    lineEdits: manager,
    raise: true,
    decide: true,
    yardAck: false,
    financeApprove: false,
    apply: manager,
  };
}

// editRules (core order/edit.go): line edits die at READY and are the
// manager's alone; the requested date lives to PARTIALLY_FULFILLED for
// everyone. editable_fields on the order read is computed from THIS, never
// offered by the client.
function editableFieldsFor(status, manager) {
  const fields = [];
  const lineStatuses = new Set(["DRAFT", "CONFIRMED", "PICKING"]);
  const dateStatuses = new Set(["DRAFT", "CONFIRMED", "PICKING", "READY", "PARTIALLY_FULFILLED"]);
  if (manager && lineStatuses.has(status)) fields.push("line_qty", "line_add", "line_remove");
  if (dateStatuses.has(status)) fields.push("requested_date");
  return fields;
}

// The manual matrix's own answers for the statuses this world reaches.
function legalActionsFor(status) {
  if (status === "CONFIRMED") return ["PICKING", "CANCELLED"];
  if (status === "PICKING") return ["READY", "CANCELLED"];
  return [];
}

// statusIsPostPick (core change.go): the status-ladder half of the raise gate.
function statusIsPostPick(status) {
  return status === "PICKING" || status === "READY" || status === "PARTIALLY_FULFILLED";
}

// changeNextActions (core change.go), for the caller this stub minted.
function changeNextActions(status, manager) {
  if (status === "PENDING_YARD") return ["yard_ack", "reject", "cancel"];
  if (status === "PENDING_FINANCE") return manager ? ["finance_approve", "reject", "cancel"] : ["reject", "cancel"];
  if (status === "APPROVED") return manager ? ["apply", "reject", "cancel"] : ["cancel"];
  return [];
}

function isTerminalChange(status) {
  return status === "APPLIED" || status === "REJECTED" || status === "CANCELLED";
}

function qtyWord(qty) {
  return (Math.round(qty * 10000) / 10000).toFixed(4);
}

// One rung order or owing order by id, with the branch fence the core holds.
function orderForRead(id, callerBranch) {
  const sale = sales.get(id);
  if (sale !== undefined) return { kind: "sale", row: sale };
  const owed = owing.get(id);
  if (owed !== undefined) return { kind: "owing", row: owed };
  return null;
}

function orderStatusOf(orderId) {
  return ORDER_STATUS.get(orderId) ?? "CONFIRMED";
}

// ---------------------------------------------------------------------------
// The returns world: the demo branch's sales, the drawer, and the returns
// already taken. The sale numbers are the counter's own spellings, every id
// is uuid shaped as the contract's formats name them, the money is integer
// cents straight from the (stubbed) core, and tax is zero in this world so
// every figure a test reads is the line's own total. A sale numbered outside
// this table (another branch's sale among them: the real lookup is branch
// fenced, and a fenced sale is an invisible one) answers not found.
//
// BRANCH_ID is this world's own demo branch, no relation to the counter
// world's Point Loma and Julian above: the returns desk reads the drawer for
// the branch the found sale names, so this one answers its drawer and its Z
// report, and the counter's routes keep fencing by the login's own grant.
// ---------------------------------------------------------------------------

const BRANCH_ID = "5d1b3f6e-0000-4000-9000-000000000001";

const SALES = {
  "CT-1001": {
    order_id: uuid(1001),
    order_no: "CT-1001",
    branch_id: BRANCH_ID,
    customer_id: uuid(100001),
    invoice_id: uuid(2001),
    sale_tender: "CASH",
    card_refundable: false,
    lines: [
      {
        order_line_id: uuid(11011),
        product_id: uuid(50001),
        description: "Charcoal paver, bevelled, each",
        qty_sold: 2,
        unit_price_cents: 6000,
        line_total_cents: 12000,
        tax_cents: 0,
      },
      {
        order_line_id: uuid(11012),
        product_id: uuid(50002),
        description: "Wall block, charcoal, each",
        qty_sold: 6,
        unit_price_cents: 6000,
        line_total_cents: 36000,
        tax_cents: 0,
      },
    ],
  },
  "CT-1002": {
    order_id: uuid(1002),
    order_no: "CT-1002",
    branch_id: BRANCH_ID,
    customer_id: uuid(100001),
    invoice_id: uuid(2002),
    sale_tender: "CASH",
    card_refundable: false,
    lines: [
      {
        order_line_id: uuid(11021),
        product_id: uuid(50002),
        description: "Wall block, charcoal, each",
        qty_sold: 6,
        unit_price_cents: 6000,
        line_total_cents: 36000,
        tax_cents: 0,
      },
    ],
  },
  "CT-1003": {
    order_id: uuid(1003),
    order_no: "CT-1003",
    branch_id: BRANCH_ID,
    customer_id: uuid(100001),
    invoice_id: uuid(2003),
    sale_tender: "CASH",
    card_refundable: false,
    lines: [
      {
        order_line_id: uuid(11031),
        product_id: uuid(50002),
        description: "Wall block, charcoal, each",
        qty_sold: 6,
        unit_price_cents: 6000,
        line_total_cents: 36000,
        tax_cents: 0,
      },
    ],
  },
  "CT-1004": {
    order_id: uuid(1004),
    order_no: "CT-1004",
    branch_id: BRANCH_ID,
    customer_id: uuid(100001),
    invoice_id: uuid(2004),
    sale_tender: "CASH",
    card_refundable: false,
    lines: [
      {
        order_line_id: uuid(11041),
        product_id: uuid(50002),
        description: "Wall block, charcoal, each",
        qty_sold: 6,
        unit_price_cents: 6000,
        line_total_cents: 36000,
        tax_cents: 0,
      },
    ],
  },
  "CT-2001": {
    order_id: uuid(2001),
    order_no: "CT-2001",
    branch_id: BRANCH_ID,
    customer_id: uuid(100002),
    invoice_id: uuid(3001),
    sale_tender: "CARD",
    card_refundable: true,
    lines: [
      {
        order_line_id: uuid(12011),
        product_id: uuid(50003),
        description: "Garden edger, grey, each",
        qty_sold: 1,
        unit_price_cents: 8900,
        line_total_cents: 8900,
        tax_cents: 0,
      },
    ],
  },
  "CT-2002": {
    order_id: uuid(2002),
    order_no: "CT-2002",
    branch_id: BRANCH_ID,
    customer_id: uuid(100002),
    invoice_id: uuid(3002),
    sale_tender: "CARD",
    card_refundable: true,
    lines: [
      {
        order_line_id: uuid(12021),
        product_id: uuid(50004),
        description: "Garden edger, sandstone, each",
        qty_sold: 2,
        unit_price_cents: 7500,
        line_total_cents: 15000,
        tax_cents: 0,
      },
    ],
  },
  // Walk in sales (ruling A): found only by a receipt or order number. One cash, one card.
  "CT-3001": {
    order_id: uuid(3001),
    order_no: "CT-3001",
    branch_id: BRANCH_ID,
    customer_id: uuid(100009),
    invoice_id: uuid(4001),
    sale_tender: "CASH",
    card_refundable: false,
    lines: [
      { order_line_id: uuid(13011), product_id: uuid(50002), description: "Wall block, charcoal, each", qty_sold: 4, unit_price_cents: 6000, line_total_cents: 24000, tax_cents: 0 },
    ],
  },
  "CT-3002": {
    order_id: uuid(3002),
    order_no: "CT-3002",
    branch_id: BRANCH_ID,
    customer_id: uuid(100009),
    invoice_id: uuid(4002),
    sale_tender: "CARD",
    card_refundable: true,
    lines: [
      { order_line_id: uuid(13021), product_id: uuid(50003), description: "Garden edger, grey, each", qty_sold: 3, unit_price_cents: 8900, line_total_cents: 26700, tax_cents: 0 },
    ],
  },
  // Orders not fully paid (the cash or card split): half paid, and not paid at all.
  "CT-3003": {
    order_id: uuid(3003),
    order_no: "CT-3003",
    branch_id: BRANCH_ID,
    customer_id: uuid(100007),
    invoice_id: uuid(4003),
    sale_tender: "CASH",
    card_refundable: false,
    paid_cents: 5000,
    lines: [
      { order_line_id: uuid(13031), product_id: uuid(50002), description: "Wall block, charcoal, each", qty_sold: 2, unit_price_cents: 6000, line_total_cents: 12000, tax_cents: 0 },
    ],
  },
  "CT-3004": {
    order_id: uuid(3004),
    order_no: "CT-3004",
    branch_id: BRANCH_ID,
    customer_id: uuid(100007),
    invoice_id: uuid(4004),
    sale_tender: "CASH",
    card_refundable: false,
    paid_cents: 0,
    lines: [
      { order_line_id: uuid(13041), product_id: uuid(50002), description: "Wall block, charcoal, each", qty_sold: 1, unit_price_cents: 6000, line_total_cents: 6000, tax_cents: 0 },
    ],
  },
};

// The customer lines read's world (item 10.1's contract, field for field): every order a customer
// holds, newest first, each line with its open quantity read off the same returned counts the old
// return path kept. Tax is zero in this world, so the money returned is the unit price times the
// quantity returned. The second branch exists to show the one branch per credit note rule.
const BRANCH_ID_2 = "5d1b3f6e-0000-4000-9000-000000000002";
const BRANCH_NAMES = { [BRANCH_ID]: "Point Loma", [BRANCH_ID_2]: "Julian" };
// The canonical house WALK-IN row (migration 0029), the id the till names in its Walk in sale entry.
const WALK_IN_ID = "a1900000-0000-4000-9000-000000000001";

const CUSTOMER_ORDER_FACTS = {
  "CT-1001": { created_at: "2026-10-03T15:20:00Z", job_label: "Marina Road patio", source: "TILL", sale_type: "COLLECT_NOW" },
  "CT-1002": { created_at: "2026-10-01T09:05:00Z", job_label: "Front walk", source: "TILL", sale_type: "COLLECT_NOW" },
  "CT-1003": { created_at: "2026-10-05T10:00:00Z", job_label: "", source: "TILL", sale_type: "COLLECT_NOW" },
  "CT-1004": { created_at: "2026-10-06T10:00:00Z", job_label: "Garden steps", source: "QUOTE", sale_type: "" },
  "CT-2001": { created_at: "2026-10-04T11:40:00Z", job_label: "", source: "TILL", sale_type: "COLLECT_NOW" },
  "CT-2002": { created_at: "2026-10-02T13:10:00Z", job_label: "Garden wall", source: "TILL", sale_type: "COLLECT_NOW" },
  "CT-3001": { created_at: "2026-10-07T09:00:00Z", job_label: "", source: "TILL", sale_type: "COLLECT_NOW" },
  "CT-3002": { created_at: "2026-10-07T09:30:00Z", job_label: "", source: "TILL", sale_type: "COLLECT_NOW" },
  "CT-3003": { created_at: "2026-10-07T10:00:00Z", job_label: "Back steps", source: "TILL", sale_type: "COLLECT_LATER" },
  "CT-3004": { created_at: "2026-10-07T10:30:00Z", job_label: "Side path", source: "TILL", sale_type: "COLLECT_LATER" },
};

function customerOrderOf(customerId, orderNo, over = {}) {
  return { ...SALES[orderNo], ...CUSTOMER_ORDER_FACTS[orderNo], customer_id: customerId, fulfilment_state: "FULFILLED", ...over };
}

const CUSTOMER_ORDERS = {
  "c-maple-ridge": [customerOrderOf("c-maple-ridge", "CT-1001"), customerOrderOf("c-maple-ridge", "CT-1002")],
  "c-fenwick": [
    customerOrderOf("c-fenwick", "CT-2001"),
    customerOrderOf("c-fenwick", "CT-2002", { branch_id: BRANCH_ID_2, source: "QUOTE", sale_tender: "", sale_type: "" }),
  ],
  "c-harbour": [],
  // An account with no terms and no credit line: the account rail is refused in the core's words.
  "c-retail": [
    // An order a quote became with no live invoice yet: the read marks it blocked, with the core's words.
    customerOrderOf("c-retail", "CT-1004", {
      sale_tender: "",
      returnable: false,
      not_returnable_reason: "no invoice has been raised for this order yet, so there is nothing to refund against",
    }),
    customerOrderOf("c-retail", "CT-1003"),
    customerOrderOf("c-retail", "CT-3003"),
    customerOrderOf("c-retail", "CT-3004"),
  ],
  [WALK_IN_ID]: [customerOrderOf(WALK_IN_ID, "CT-3001"), customerOrderOf(WALK_IN_ID, "CT-3002")],
};

function customerOrderView(order) {
  return {
    order_id: order.order_id,
    order_no: order.order_no,
    branch_id: order.branch_id,
    branch_name: BRANCH_NAMES[order.branch_id] ?? "",
    created_at: order.created_at,
    job_label: order.job_label,
    source: order.source,
    sale_type: order.sale_type,
    tender: order.sale_tender,
    fulfilment_state: order.fulfilment_state,
    invoice_id: order.returnable === false ? "" : order.invoice_id,
    card_refundable: order.card_refundable,
    returnable: order.returnable !== false,
    not_returnable_reason: order.returnable === false ? order.not_returnable_reason : "",
    lines: order.lines.map((line) => {
      const returned = RETURNED_QTY.get(line.order_line_id) ?? 0;
      return {
        order_line_id: line.order_line_id,
        product_id: line.product_id,
        sku: `SKU-${line.product_id.slice(-5)}`,
        description: line.description,
        qty_sold: line.qty_sold,
        qty_returned: returned,
        qty_open: line.qty_sold - returned,
        unit_price_cents: line.unit_price_cents,
        line_total_cents: line.line_total_cents,
        tax_cents: line.tax_cents,
        money_returned_cents: returned * line.unit_price_cents,
        tax_returned_cents: 0,
        shelf_state: "FULFILLED",
      };
    }),
  };
}

// The drawer's world: one open session at the returns branch, with its opening
// float and the cash that has moved through it. The Z report's expected cash
// is read off these counters, so a cash return moving the cash out counter is
// the same movement the Z report shows. It stands beside the counter world's
// per branch drawers: that one is opened and found by the till, this one is
// the returns desk's own, and neither answers for the other.
const DRAWER = {
  session_id: uuid(90001),
  branch_id: BRANCH_ID,
  opening_float_cents: 20000,
  cash_in_cents: 0,
  cash_out_cents: 0,
};

// The returned quantity netted per line, which the customer lines read answers as qty_returned and
// qty_open. The write path that grows it is rebuilt with the refund step; one line is seeded
// partly returned so the screens show an open quantity below the quantity sold.
const RETURNED_QTY = new Map([[uuid(11012), 2]]);
function reseedReturns() {
  RETURNED_QTY.clear();
  RETURNED_QTY.set(uuid(11012), 2);
  NOTES_BY_KEY.clear();
  NOTE_META.clear();
  CARD_REFUNDED.clear();
  PAYOUTS.clear();
  OFFSETS.clear();
  PREVIEW_BODIES.length = 0;
  SWAPS_BY_KEY.clear();
  SWAP_KEYS_SEEN.length = 0;
  LAST_SWAP_BODY = null;
  swapFailure = null;
  DRAWER.cash_in_cents = 0;
  NOTE_KEYS_SEEN.length = 0;
  LAST_NOTE_BODY = null;
  SIGHTINGS.length = 0;
  DRAWER.cash_out_cents = 0;
  approvalLimitEnabled = false;
}

// What the harness's peek route reads back: every note body the write route saw (keys in send order,
// the last body verbatim), who each committed note names as clerk and approver, and whether the
// approver's header rode each api call.
const SIGHTINGS = [];
const NOTE_KEYS_SEEN = [];
let LAST_NOTE_BODY = null;
const NOTE_META = new Map();
const CARD_REFUNDED = new Map();
const PREVIEW_BODIES = [];
const NOTES_BY_KEY = new Map();
let NOTE_SEQ = 0;
let RETURN_SEQ = 0;

// The approval above which a refund needs a manager, judged on the NOTE total (ruling Q3), and the
// roles that hold it. The demo dealer's configuration turns the rule off, so the stub starts that
// way; POST /_stub/refund-approval-limit flips it for the legs that exercise the approval path.
const APPROVAL_AMOUNT_CENTS = 25000;
const APPROVER_ROLES = ["sales_manager", "branch_manager"];
let approvalLimitEnabled = process.env.CT_REFUND_APPROVAL_LIMIT === "on";

// ---------------------------------------------------------------------------
// The pricing stand-in: the same waterfall-shaped table the ring and the two
// counter reads answer from, so a line's screen figure and the committed
// sale's figure cannot drift. Integer cents throughout.
// ---------------------------------------------------------------------------

function customerFor(customerId) {
  if (typeof customerId !== "string" || customerId === "") return WALK_IN;
  return CUSTOMERS.find((c) => c.id === customerId) ?? null;
}

// Item 9.1's menu, the core's own rule (payment_options.go): a credit customer is terms above
// zero AND a credit line and not on hold; everything else (Cash On Demand, walk in, no credit,
// a held credit account) is offered the deposit with the balance due later. The row 1214 ruling
// adds the no deposit pay before release shape, the Cash On Demand account's alone (zero day
// terms with a line, the same fact that picks the sentence), between pay in full and the deposit.
const SENTENCE_ON_ACCOUNT = "A credit customer: the rest rides their account and is paid from the statement.";
const SENTENCE_CASH_ON_DEMAND =
  "Cash On Demand means pay before release, never cash on delivery: the deposit puts the order in process, and picking waits for paid in full.";
const SENTENCE_CANNOT_CARRY =
  "This account cannot carry this sale on account: pay in full now, or take a deposit with the balance due later. The deposit puts the order in process, and picking waits for paid in full.";

function paymentOptionsFor(customer) {
  const terms = customer.payment_terms_days ?? 0;
  const limit = customer.credit_limit_cents ?? 0;
  if (terms > 0 && limit > 0 && customer.on_hold !== true) {
    return [
      { shape: "PAY_IN_FULL_NOW" },
      { shape: "ALL_ON_ACCOUNT" },
      { shape: "DEPOSIT_NOW_REST_ON_ACCOUNT", line_two_words: "On account", line_two_sentence: SENTENCE_ON_ACCOUNT },
    ];
  }
  const options = [{ shape: "PAY_IN_FULL_NOW" }];
  if (terms <= 0 && limit > 0) {
    options.push({ shape: "PAY_BEFORE_RELEASE" });
  }
  options.push({
    shape: "DEPOSIT_NOW_REST_DUE_LATER",
    line_two_words: "Balance due later",
    line_two_sentence: terms <= 0 && limit > 0 ? SENTENCE_CASH_ON_DEMAND : SENTENCE_CANNOT_CARRY,
  });
  return options;
}

// Item 9.5's allowance, the core's own rule (sale_supplier_deposit.go): the supplier's demanded
// deposit is billed to a credit account's own on-account ring, so the same facts that offer
// ALL_ON_ACCOUNT allow it, and nobody else does.
function supplierDepositFor(customer) {
  const terms = customer.payment_terms_days ?? 0;
  const limit = customer.credit_limit_cents ?? 0;
  if (terms > 0 && limit > 0 && customer.on_hold !== true) {
    return { allowed: true, words: "Supplier deposit, billed to the account" };
  }
  return { allowed: false };
}

// One resolved line: the price the engine would answer for this customer, this
// branch, this product at this quantity. The bulk table is flat in the demo
// world: every quantity rings at the same rule price.
function resolveLine(customerId, productId, branchId) {
  const product = PRODUCTS.find((p) => p.product_id === productId);
  const customer = customerFor(customerId);
  if (product === undefined || customer === null) return null;
  const retail = product.list_cents;
  const percentOff = customer.percent_off ?? 0;
  const customerCents = percentOff === 0 ? retail : Math.round((retail * (100 - percentOff)) / 100);
  return {
    product,
    customer,
    price_uom: product.sold_by,
    retail_cents: retail,
    customer_cents: customerCents,
    saving_cents: retail - customerCents,
    basis: percentOff > 0 ? "ACCOUNT" : undefined,
  };
}


// The counter discount (row 1047 item 3), the core's own rules in the stub's words: a line or the
// ticket may carry a percent (basis points) or an amount and the manager who said yes; the approver
// must be one the discount-approvers read names. The percent rounds half up to the cent, the ticket
// discount comes off the subtotal after the line discounts and is shared over the lines by largest
// remainder, and every line total below is net of both, which is what a return refunds against.
const DISCOUNT_APPROVERS = [
  { id: "c0c0a0a0-0000-4000-8000-000000000001", name: "Sales manager", role_label: "Sales Manager" },
  { id: "c0c0a0a0-0000-4000-8000-000000000002", name: "Branch manager", role_label: "Branch Manager" },
];

const ZERO_TOTAL_LINE_WORDS =
  "a sale cannot total $0.00: a discount must leave something to pay (a line discount can take off at most the line total less one cent)";
const ZERO_TOTAL_WORDS =
  "a sale cannot total $0.00: a discount must leave something to pay (a ticket discount can take off at most the ticket total less one cent)";

function discountRefusal(error) {
  return { refusal: { status: 422, body: { error } } };
}

function oneDiscount(raw, base, what, label, qty) {
  const percent = raw.percent_bps ?? 0;
  const amount = raw.amount_cents ?? 0;
  if (!Number.isInteger(percent) || !Number.isInteger(amount) || percent < 0 || amount < 0 || (percent > 0) === (amount > 0)) {
    return discountRefusal(`${label}: a discount is either a percent or an amount, not both and not neither`);
  }
  let cents = amount;
  if (percent > 0) {
    if (percent > 10000) return discountRefusal(`${label}: a discount percent must be more than 0 and no more than 100`);
    cents = Math.floor((base * percent + 5000) / 10000);
    if (cents === 0) return discountRefusal(`${label}: that discount comes to less than a cent`);
  } else if (amount > base) {
    return discountRefusal(`${label}: a discount cannot be more than ${what}`);
  }
  const approver = typeof raw.approved_by === "string" ? raw.approved_by : "";
  if (approver === "") return discountRefusal(`${label}: a discount needs the manager who approved it (approved_by)`);
  const who = DISCOUNT_APPROVERS.find((a) => a.id === approver);
  if (who === undefined) return discountRefusal(`${label}: that person cannot approve a discount at this branch`);
  // A yes is for the quantity and base it was given for (the core's own check, in its words).
  const line = qty !== null;
  if (!(raw.approved_base_cents > 0) || (line && !(raw.approved_qty > 0))) {
    return discountRefusal(`${label}: a discount carries the quantity and the amount it was approved for`);
  }
  if (raw.approved_base_cents !== base || (line && raw.approved_qty !== qty)) {
    return discountRefusal(`${label}: the discount was approved for a different quantity or price: ask a manager again`);
  }
  return { cents, percent, who };
}

// grossTotals: each line's list total; rawLines: the body's lines; ticket: the body's discount or undefined.
function applyStubDiscounts(grossTotals, rawLines, ticket) {
  const nets = grossTotals.slice();
  const rows = grossTotals.map(() => ({ discount: 0, sale: 0 }));
  const discounts = [];
  for (let i = 0; i < grossTotals.length; i += 1) {
    const raw = rawLines[i]?.discount;
    if (raw === undefined || raw === null) continue;
    const got = oneDiscount(raw, grossTotals[i], "the line", `line ${i + 1}`, rawLines[i]?.qty_entered ?? 0);
    if (got.refusal) return got;
    nets[i] -= got.cents;
    rows[i].discount = got.cents;
    discounts.push({
      scope: "LINE", line_no: i + 1, ...(got.percent > 0 ? { percent_bps: got.percent } : {}), amount_cents: got.cents,
      approved_by: got.who.id, approved_by_role: "sales_manager", approved_by_label: got.who.role_label,
    });
  }
  let saleCents = 0;
  if (ticket !== undefined && ticket !== null) {
    const base = nets.reduce((sum, n) => sum + n, 0);
    const got = oneDiscount(ticket, base, "the ticket", "ticket", null);
    if (got.refusal) return got;
    saleCents = got.cents;
    // Largest remainder over the net line totals, ties to the lowest line.
    const shares = nets.map((n) => Math.floor((got.cents * n) / base));
    let left = got.cents - shares.reduce((sum, n) => sum + n, 0);
    const order = nets
      .map((n, i) => ({ i, rem: (got.cents * n) % base }))
      .sort((a, b) => b.rem - a.rem || a.i - b.i);
    for (const { i } of order) {
      if (left <= 0) break;
      shares[i] += 1;
      left -= 1;
    }
    for (let i = 0; i < nets.length; i += 1) {
      nets[i] -= shares[i];
      rows[i].sale = shares[i];
    }
    discounts.push({
      scope: "SALE", ...(got.percent > 0 ? { percent_bps: got.percent } : {}), amount_cents: got.cents,
      approved_by: got.who.id, approved_by_role: "sales_manager", approved_by_label: got.who.role_label,
    });
  }
  return { nets, rows, discounts, saleCents, discountCents: discounts.reduce((sum, d) => sum + d.amount_cents, 0) };
}

function taxCents(subtotal) {
  return Math.round((subtotal * HST_RATE_BPS) / 10000);
}

// A line's committed total: the rule price at the rung quantity, rounded to
// the whole cent exactly as the engine's fixed-precision decimal does. The
// ring, the price read and the glance all answer through this one function,
// so a screen figure and a committed figure cannot drift.
function lineTotalCents(unitCents, qtyEntered) {
  return Math.round(unitCents * qtyEntered);
}

function marginBpsOf(revenue, cost) {
  return Math.round(((revenue - cost) / revenue) * 10000);
}

// The card rail: the demo's card tender charges the simulator, so the status
// route answers simulated: true and every capture reference begins like the
// simulator's. When the real core lands on the integration branch before the
// card rail fix, its route answers simulated: false; the till's notice
// renders exactly when the value is true, so the same app serves both worlds
// unchanged and only the words move. One status answers both desks: the till
// and the returns desk read the same rail, and the suites hold only the
// simulated flag on it (true here; each suite fulfils the false side itself),
// so the returns lane's own field spelling for the merchant and processor
// words yields to this one constant.
const PAYMENT_CONFIG = {
  configured: true,
  merchant_id: "demo-merchant",
  processor: "simulator",
  currency: "CAD",
  simulated: true,
};

// The credit gate, in the shape the core's handler answers: a refusal the
// client reads by its error word and status, with the policy named beside it.
function creditBlocked(policy) {
  return { status: 409, body: { error: "credit_blocked", policy, queue: "staff_approvals" } };
}

// ---------------------------------------------------------------------------
// The mint, the auth reads, and the response helpers.
// ---------------------------------------------------------------------------

function base64UrlJson(value) {
  return Buffer.from(JSON.stringify(value), "utf8").toString("base64url");
}

function mintToken(flow) {
  const issuedAtSeconds = Math.floor(nowMs() / 1000);
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

// The approver credential, read the way the caller's is: a bearer this stub
// minted, carried in its own header. `present` says whether the header rode
// the request at all; `account` is the verified approver or null. Verified
// means unexpired too: the real rule takes a credential issued within the
// short window the configuration sets, and an expired mint is not one.
function approverFromRequest(request) {
  const header = request.headers["x-approver-authorization"] ?? "";
  if (header === "") return { present: false, account: null };
  const match = /^Bearer (.+)$/.exec(header);
  if (match === null) return { present: true, account: null };
  const parts = match[1].split(".");
  if (parts.length !== 3) return { present: true, account: null };
  try {
    const payload = JSON.parse(Buffer.from(parts[1], "base64url").toString("utf8"));
    if (payload === null || typeof payload !== "object") return { present: true, account: null };
    const sub = payload.sub;
    const exp = payload.exp;
    if (typeof sub !== "string") return { present: true, account: null };
    if (typeof exp !== "number" || !Number.isFinite(exp) || exp * 1000 <= nowMs()) {
      return { present: true, account: null };
    }
    for (const flow of Object.keys(USERS)) {
      if (USERS[flow].user_id === sub) return { present: true, account: USERS[flow] };
    }
    return { present: true, account: null };
  } catch {
    return { present: true, account: null };
  }
}

// The staff caller for the returns desk's routes and the Z report, read back
// out of a bearer this stub itself minted: the payload's sub names the
// account. Null when there is no usable bearer, and those routes answer 401
// like the real gate does. This reader holds no org header requirement: its
// suite reads the Z report straight, under a bare minted bearer, the way the
// harness itself asks one.
function callerFromRequest(request) {
  const header = request.headers["authorization"] ?? "";
  const match = /^Bearer (.+)$/.exec(header);
  if (match === null) return null;
  const parts = match[1].split(".");
  if (parts.length !== 3) return null;
  try {
    const payload = JSON.parse(Buffer.from(parts[1], "base64url").toString("utf8"));
    const sub = payload === null || typeof payload !== "object" ? undefined : payload.sub;
    if (typeof sub !== "string") return null;
    for (const flow of Object.keys(USERS)) {
      if (USERS[flow].user_id === sub) return { flow, account: USERS[flow] };
    }
    return null;
  } catch {
    return null;
  }
}

// The auth read the counter's routes answer behind, the way the core's
// middleware does: the bearer's own sub claim names the account, and the
// account's grant names the branch, and the demo org header rides beside the
// bearer. No bearer, no unreadable bearer, an account without a grant, or a
// call without the org header: refused before any route logic runs.
function callerOf(request, url) {
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
  return { userId, branchId };
}

function respondJson(response, status, payload, extraHeaders = {}) {
  response.writeHead(status, { "content-type": "application/json", ...extraHeaders });
  response.end(JSON.stringify(payload));
}

// Two small body readers, one per lane's shape. The returns desk's routes read
// the raw body and parse it after the credential checks; the counter's routes
// parse inside the read and answer the bad body themselves.
function readRawBody(request) {
  return new Promise((resolve, reject) => {
    let body = "";
    request.on("data", (chunk) => {
      body += chunk;
    });
    request.on("end", () => resolve(body));
    request.on("error", reject);
  });
}

function readBody(request, respond) {
  let body = "";
  request.on("data", (chunk) => {
    body += chunk;
  });
  request.on("end", () => {
    if (body === "") {
      respond({});
      return;
    }
    try {
      respond(JSON.parse(body));
    } catch {
      respondJson(response, 400, { error: "the request body is not JSON" });
    }
  });
}

// ---------------------------------------------------------------------------
// The credit note across orders: the core's returns_across.go, in the same order of refusals
// (note level 400s, the house WALK-IN, the replay, then each order through the single return's own
// guards, one customer, one branch, the approval on the note total), then one commit.
// ---------------------------------------------------------------------------

function orderById(orderId) {
  for (const held of Object.values(CUSTOMER_ORDERS)) {
    const found = held.find((row) => row.order_id === orderId);
    if (found !== undefined) return found;
  }
  return undefined;
}

function refuse(status, error, extra = {}) {
  return { status, body: { error, ...extra } };
}

const NAME_THE_CUSTOMER = "pos: name the customer, or give the receipt or order number of a walk-in sale";

// The customer a write or a preview is for: the named one, or, with an empty customer, the house walk in row
// when a receipt or order number rides along. Null when neither is given.
function effectiveCustomer(body) {
  const named = typeof body.customer_id === "string" ? body.customer_id.trim() : "";
  if (named !== "") return named;
  return normalizeSaleRef(body.number ?? "") === "" ? null : WALK_IN_ID;
}

// A receipt or order number as the core reads it: an order number or an invoice number, with or without
// the hash, or the bare digits a branch's numbers end in.
function normalizeSaleRef(raw) {
  return String(raw).trim().replace(/^#/, "").toLowerCase();
}

function saleRefMatches(order, number) {
  const orderNo = order.order_no.toLowerCase();
  const invoiceNo = `inv-${orderNo}`;
  if (number === orderNo || number === invoiceNo) return true;
  return /^[0-9]+$/.test(number) && orderNo.endsWith(number);
}

// Cash or card only up to what was actually paid on the order's invoice, less earlier cash or card
// payouts; the rest comes off the open invoice (the principal's ruling), in the core's words.
const PAYOUTS = new Map();
const OFFSETS = new Map();

function railSplit(order, method, total) {
  if ((method !== "CASH" && method !== "CARD") || order.paid_cents === undefined) return { rail: total, offset: 0, note: "" };
  const invoiceTotal = order.lines.reduce((sum, row) => sum + row.line_total_cents, 0);
  const balance = invoiceTotal - order.paid_cents - (OFFSETS.get(order.order_id) ?? 0);
  const avail = Math.max(order.paid_cents - (PAYOUTS.get(order.order_id) ?? 0), 0);
  let rail = total;
  let offset = 0;
  if (avail < total) {
    rail = avail;
    offset = total - avail;
    if (offset > balance) {
      rail += offset - balance;
      offset = balance;
    }
  }
  let note = "";
  if (offset > 0 && rail === 0) {
    note = `This order is not paid yet: nothing can go back in cash or card; the whole ${centsWords(offset)} comes off the open invoice`;
  } else if (offset > 0) {
    note = `This order is not fully paid: up to ${centsWords(rail)} can go back in cash or card; the rest, ${centsWords(offset)}, comes off the open invoice`;
  }
  return { rail, offset, note };
}

function centsWords(cents) {
  return `$${(cents / 100).toFixed(2)}`;
}

// The shape, wall and guard rules shared by the commit and the preview, in the core's words and order
// (checkNoteShape, then each order through the walls and the line guard). `commit` adds the rail,
// the replay, the 10.4 account and card refusals and the approval, which the preview never judges.
function noteRefusal(caller, body, approver, commit = true, allowWalkIn = true) {
  if (body === null || typeof body !== "object") return refuse(400, "the request body is not JSON");
  if (commit) {
    if (typeof body.idempotency_key !== "string" || body.idempotency_key === "") {
      return refuse(400, "idempotency_key (client UUID) is required");
    }
    if (body.idempotency_key.startsWith("rreq:")) return refuse(400, "idempotency_key: the rreq: prefix is reserved");
  }
  if (!Array.isArray(body.orders) || body.orders.length === 0) return refuse(400, "pos: a return note needs at least one order");
  if (commit && !["CASH", "CARD", "ACCOUNT_CREDIT"].includes(body.refund_method)) return refuse(400, "pos: refund_method must be CASH, CARD or ACCOUNT_CREDIT");
  const customerId = effectiveCustomer(body);
  if (customerId === null) return refuse(400, NAME_THE_CUSTOMER);
  if (customerId === WALK_IN_ID && !allowWalkIn) {
    return refuse(422, "pos: a swap is rung for a customer's own account, not the WALK-IN house account; return the walk-in sale to the card it was paid on or in cash, and ring the new order apart");
  }
  const seen = new Set();
  for (const entry of body.orders) {
    if (typeof entry.order_id !== "string" || entry.order_id === "") return refuse(400, "pos: every order on a return note needs an order_id");
    if (seen.has(entry.order_id)) return refuse(422, `pos: order ${entry.order_id} appears twice on one note`);
    seen.add(entry.order_id);
    if (!Array.isArray(entry.lines) || entry.lines.length === 0) return refuse(422, `pos: order ${entry.order_id} carries no return lines`);
    for (const asked of entry.lines) {
      if (typeof asked.order_line_id !== "string" || asked.order_line_id === "" || !(asked.qty_base > 0)) {
        return refuse(422, "pos: every return line needs an order_line_id and a positive qty_base");
      }
    }
  }
  if (commit && NOTES_BY_KEY.has(body.idempotency_key)) return null;
  if (commit && body.refund_method === "ACCOUNT_CREDIT" && customerId === WALK_IN_ID) {
    return refuse(422, "pos: the WALK-IN house account holds no credit; refund this walk-in sale to the card it was paid on or in cash");
  }
  if (commit && body.refund_method === "ACCOUNT_CREDIT") {
    const account = CUSTOMERS.find((row) => row.id === customerId);
    if (account === undefined || account.payment_terms_days <= 0 || account.credit_limit_cents <= 0) {
      return refuse(
        422,
        "pos: this account cannot spend a credit (it has no credit terms or no credit line); refund to the card it was paid on or in cash, or set the account up with terms and a credit line first",
      );
    }
  }
  let branchId = null;
  let totalCents = 0;
  for (const entry of body.orders) {
    const order = orderById(entry.order_id);
    if (order === undefined) return refuse(404, `pos: no sale ${entry.order_id}`);
    if (order.returnable === false) return refuse(422, `pos: ${order.not_returnable_reason}`);
    if (commit && body.refund_method === "CASH" && body.till_session_id !== DRAWER.session_id) {
      return refuse(422, "pos: a cash refund needs the open drawer at the sale's branch");
    }
    if (commit && body.refund_method === "CARD" && !order.card_refundable) {
      return refuse(422, `pos: order ${order.order_no} has no card capture to reverse`);
    }
    if (order.customer_id !== customerId) {
      return refuse(422, `pos: order ${order.order_no} belongs to another customer, not the note's customer`);
    }
    if (branchId !== null && branchId !== order.branch_id) {
      return refuse(422, `pos: a return note covers one branch, order ${order.order_no} sits in another`);
    }
    branchId = order.branch_id;
    const named = new Set();
    let orderCents = 0;
    for (const asked of entry.lines) {
      if (named.has(asked.order_line_id)) {
        return refuse(422, `pos: line ${asked.order_line_id} appears twice on order ${order.order_no}, send it once with the whole quantity`);
      }
      named.add(asked.order_line_id);
      if (asked.disposition !== undefined && !["RESTOCK", "DAMAGED", "NO_RETURN"].includes(asked.disposition)) {
        return refuse(400, "pos: disposition must be RESTOCK, DAMAGED or NO_RETURN");
      }
      const line = order.lines.find((row) => row.order_line_id === asked.order_line_id);
      if (line === undefined) return refuse(422, `pos: line ${asked.order_line_id} does not belong to order ${order.order_no}`);
      const open = line.qty_sold - (RETURNED_QTY.get(line.order_line_id) ?? 0);
      if (asked.qty_base > open + 1e-6) {
        return refuse(422, `pos: over-return on line ${asked.order_line_id}, ${asked.qty_base.toFixed(4)} requested but only ${open.toFixed(4)} returnable`);
      }
      orderCents += line.unit_price_cents * asked.qty_base;
    }
    if (commit && (body.refund_method === "CASH" || body.refund_method === "CARD")) {
      const split = railSplit(order, body.refund_method, orderCents);
      if (split.rail === 0 && split.offset > 0) return refuse(422, `pos: ${split.note}`);
    }
    if (commit && body.refund_method === "CARD") {
      const captured = order.lines.reduce((sum, row) => sum + row.line_total_cents, 0);
      const refunded = CARD_REFUNDED.get(order.order_id) ?? 0;
      if (refunded + orderCents > captured) {
        return refuse(
          422,
          `pos: a card refund of ${centsWords(orderCents)} would take the card past what the original capture holds (${centsWords(captured)} captured, ${centsWords(refunded)} already refunded); refund the rest in cash or to the account`,
        );
      }
    }
    totalCents += orderCents;
  }
  if (commit && approvalLimitEnabled && totalCents > APPROVAL_AMOUNT_CENTS && !APPROVER_ROLES.includes(caller.account.role_code) && !approver.present) {
    return refuse(409, "refund_approval_required", { approval: { amount_cents: APPROVAL_AMOUNT_CENTS, approvers: APPROVER_ROLES } });
  }
  return null;
}

// The preview: the commit's own figures per line, per order and for the note (this world carries no
// tax), nothing written.
function previewNote(body, method = null) {
  const orders = [];
  let railSum = 0;
  let offsetSum = 0;
  let subtotal = 0;
  let branchId = "";
  for (const entry of body.orders) {
    const order = orderById(entry.order_id);
    branchId = order.branch_id;
    const lines = entry.lines.map((asked) => {
      const line = order.lines.find((row) => row.order_line_id === asked.order_line_id);
      const total = line.unit_price_cents * asked.qty_base;
      return { order_line_id: asked.order_line_id, qty_base: asked.qty_base, subtotal_cents: total, tax_cents: 0, total_cents: total };
    });
    const orderTotal = lines.reduce((sum, row) => sum + row.total_cents, 0);
    subtotal += orderTotal;
    const entryOut = { order_id: order.order_id, order_no: order.order_no, subtotal_cents: orderTotal, tax_cents: 0, total_cents: orderTotal, lines };
    if (method === "CASH" || method === "CARD") {
      const split = railSplit(order, method, orderTotal);
      entryOut.rail_cents = split.rail;
      entryOut.invoice_offset_cents = split.offset;
      entryOut.rail_note = split.note;
      railSum += split.rail;
      offsetSum += split.offset;
    }
    orders.push(entryOut);
  }
  const out = { customer_id: effectiveCustomer(body), branch_id: branchId, subtotal_cents: subtotal, tax_cents: 0, total_cents: subtotal, orders };
  if (method === "CASH" || method === "CARD") {
    out.rail_cents = railSum;
    out.invoice_offset_cents = offsetSum;
  }
  return out;
}

function commitNote(caller, body, approver, swap = null) {
  const replay = NOTES_BY_KEY.get(body.idempotency_key);
  if (replay !== undefined) return { status: 200, body: { ...replay, replayed: true } };
  NOTE_SEQ += 1;
  const returns = [];
  let subtotal = 0;
  let railTotal = 0;
  let branchId = "";
  for (const entry of body.orders) {
    const order = orderById(entry.order_id);
    branchId = order.branch_id;
    RETURN_SEQ += 1;
    let childTotal = 0;
    for (const asked of entry.lines) {
      const line = order.lines.find((row) => row.order_line_id === asked.order_line_id);
      RETURNED_QTY.set(line.order_line_id, (RETURNED_QTY.get(line.order_line_id) ?? 0) + asked.qty_base);
      childTotal += line.unit_price_cents * asked.qty_base;
      if (body.refund_method === "CARD") CARD_REFUNDED.set(order.order_id, (CARD_REFUNDED.get(order.order_id) ?? 0) + line.unit_price_cents * asked.qty_base);
    }
    subtotal += childTotal;
    const split = railSplit(order, body.refund_method, childTotal);
    PAYOUTS.set(order.order_id, (PAYOUTS.get(order.order_id) ?? 0) + split.rail);
    OFFSETS.set(order.order_id, (OFFSETS.get(order.order_id) ?? 0) + split.offset);
    railTotal += split.rail;
    let applied;
    if (swap !== null) {
      applied = Math.min(childTotal, swap.remaining);
      swap.remaining -= applied;
    }
    returns.push({
      return_id: uuid(40000 + RETURN_SEQ),
      return_no: `CT-R-${100 + RETURN_SEQ}`,
      order_id: order.order_id,
      invoice_id: order.invoice_id,
      refund_method: body.refund_method,
      subtotal_cents: childTotal,
      tax_cents: 0,
      total_cents: childTotal,
      ...(applied === undefined ? {} : { applied_cents: applied }),
      ...(split.offset > 0 ? { invoice_offset_cents: split.offset, payment_note: split.note } : {}),
      pickup_status: "NONE",
      status: "COMMITTED",
      replayed: false,
    });
  }
  const answer = {
    ...(swap === null ? {} : { swap_id: swap.swapId }),
    note_id: uuid(60000 + NOTE_SEQ),
    note_no: `CT-CN-${100 + NOTE_SEQ}`,
    customer_id: effectiveCustomer(body),
    branch_id: branchId,
    refund_method: body.refund_method,
    subtotal_cents: subtotal,
    tax_cents: 0,
    total_cents: subtotal,
    returns,
    replayed: false,
  };
  NOTES_BY_KEY.set(body.idempotency_key, answer);
  NOTE_META.set(body.idempotency_key, {
    clerk: caller.account.user_id,
    approver: approver.present && approver.account !== null ? approver.account.user_id : null,
  });
  if (body.refund_method === "CASH" && body.till_session_id === DRAWER.session_id) {
    DRAWER.cash_out_cents += swap === null ? railTotal : subtotal - swap.applied;
  }
  return { status: 201, body: answer };
}

// ---------------------------------------------------------------------------
// The swap: the core's swap.go, one function for the preview and the ring (swapFigures). The credit is
// the note's own preview, the new order is priced from the stub's catalogue at the customer's price
// with the stub's flat tax, applied = min(credit, total), leftover = credit - applied, shortfall =
// total - applied, and the leftover goes back by the original tender (Q7).
// ---------------------------------------------------------------------------

const SWAPS_BY_KEY = new Map();
const SWAP_KEYS_SEEN = [];
let LAST_SWAP_BODY = null;
let SWAP_SEQ = 0;
// One shot harness switch for the two failures a till must say in words: "changed" (409) and "processor" (502).
let swapFailure = null;

function swapFigures(caller, body, approver, commit) {
  if (body === null || typeof body !== "object") return refuse(400, "the request body is not JSON");
  if (!Array.isArray(body.lines) || body.lines.length === 0) return refuse(422, "pos: a sale needs at least one line");
  const saleType = body.sale_type ?? "COLLECT_NOW";
  if (!["COLLECT_NOW", "COLLECT_LATER", "DELIVERED"].includes(saleType)) {
    return refuse(422, `pos: a swap rings a collect now, collect later or delivered order, not ${saleType}`);
  }
  const tender = body.tender ?? "CASH";
  if (!["CASH", "CARD", "CHEQUE", "ACCOUNT"].includes(tender)) return refuse(400, "pos: tender must be CASH, CARD, CHEQUE or ACCOUNT");
  // The credit through the note's own shape, walls and guard (preview mode: no rail yet).
  const shape = noteRefusal(caller, { ...body, refund_method: "CASH", idempotency_key: "swap-preview" }, approver, false, false);
  if (shape !== null) return shape;
  const credit = previewNote(body);
  const customer = customerFor(body.customer_id);
  let newSub = 0;
  for (const asked of body.lines) {
    const resolved = resolveLine(body.customer_id, asked.product_id, credit.branch_id);
    if (resolved === null || customer === null) return refuse(422, `pos: product ${asked.product_id} is not sold here`);
    newSub += lineTotalCents(resolved.customer_cents, asked.qty_entered);
  }
  const newTax = taxCents(newSub);
  const newTotal = newSub + newTax;
  const applied = Math.min(credit.total_cents, newTotal);
  const leftover = credit.total_cents - applied;
  const shortfall = newTotal - applied;
  let method = "";
  let uniform = true;
  for (const [index, entry] of body.orders.entries()) {
    const order = orderById(entry.order_id);
    if (order.invoice_status === "OPEN") {
      return refuse(422, `pos: order ${order.order_no} still has an open invoice, so its return is not credit in hand; return it as a credit note first, then ring the new order`);
    }
    const m = order.sale_tender === "CASH" ? "CASH" : order.sale_tender === "CARD" ? "CARD" : order.sale_tender === "" || order.sale_tender === "ACCOUNT" ? "ACCOUNT_CREDIT" : "";
    if (m === "") {
      if (leftover > 0) return refuse(422, `pos: order ${order.order_no} was paid by ${order.sale_tender.toLowerCase()}, which has no refund rail; return it as a credit note and ring the new order apart`);
    }
    if (index === 0) method = m || "ACCOUNT_CREDIT";
    else if (m !== method) uniform = false;
  }
  if (leftover > 0) {
    if (!uniform) return refuse(422, "pos: these orders were paid by different tenders and the leftover goes back by the original tender; swap them one tender at a time");
    if (body.leftover_method && body.leftover_method !== method) {
      return refuse(422, `pos: the leftover goes back by the original tender, ${method}, not ${body.leftover_method}`);
    }
  }
  if (commit) {
    // The note's own rail checks and the approval, judged on the note total, the way the note's commit does.
    const railed = noteRefusal(caller, { ...body, refund_method: leftover > 0 ? method : "ACCOUNT_CREDIT", idempotency_key: body.idempotency_key }, approver, true, false);
    if (railed !== null && !(leftover === 0 && railed.status === 422)) return railed;
  }
  return {
    ok: true,
    figures: { credit, newSub, newTax, newTotal, applied, leftover, shortfall, method: method || "ACCOUNT_CREDIT", tender, saleType },
  };
}

function swapPreviewBody(f) {
  return {
    credit: f.credit,
    new_order: { subtotal_cents: f.newSub, tax_cents: f.newTax, total_cents: f.newTotal },
    applied_cents: f.applied,
    leftover_cents: f.leftover,
    shortfall_cents: f.shortfall,
    leftover_method: f.method,
  };
}

function commitSwap(caller, body, approver, f) {
  const replay = SWAPS_BY_KEY.get(body.idempotency_key);
  if (replay !== undefined) return { status: 200, body: { ...replay, replayed: true } };
  SWAP_SEQ += 1;
  const swapId = uuid(70000 + SWAP_SEQ);
  const noted = commitNote(
    caller,
    { ...body, idempotency_key: `swap:${body.idempotency_key}`, refund_method: f.method },
    approver,
    { swapId, remaining: f.applied, applied: f.applied },
  );
  const sale = {
    swap_id: swapId,
    order_id: uuid(80000 + SWAP_SEQ),
    order_no: `CT-S-${200 + SWAP_SEQ}`,
    invoice_id: uuid(81000 + SWAP_SEQ),
    invoice_no: `CT-INV-${300 + SWAP_SEQ}`,
    tender: f.tender,
    sale_type: f.saleType,
    subtotal_cents: f.newSub,
    tax_cents: f.newTax,
    total_cents: f.newTotal,
    tax_reason_code: "STD_ON_HST_13",
    status: "COMMITTED",
    replayed: false,
  };
  if (f.tender === "CASH" && body.till_session_id === DRAWER.session_id) DRAWER.cash_in_cents += f.shortfall;
  const answer = {
    swap_id: swapId,
    note: noted.body,
    sale,
    credit_cents: f.credit.total_cents,
    applied_cents: f.applied,
    leftover_cents: f.leftover,
    shortfall_cents: f.shortfall,
    leftover_method: f.method,
    replayed: false,
  };
  SWAPS_BY_KEY.set(body.idempotency_key, answer);
  return { status: 201, body: answer };
}

// ---------------------------------------------------------------------------
// The routes' row helpers.
// ---------------------------------------------------------------------------

function tillSessionRow(branchId) {
  return drawers.get(branchId) ?? null;
}

function tillSessionDetail(row) {
  return {
    id: row.id,
    branch_id: row.branch_id,
    station: row.station,
    status: row.status,
    opened_by: row.opened_by,
    float_cents: row.float_cents,
    blind_close: false,
    tenders: [],
    movements: [],
  };
}

// The demo world's standing stock per branch: one number, the same read the
// catalogue list answers, so the counter's own branch shows what it holds.
const ON_HAND_BY_BRANCH = { [POINT_LOMA_ID]: 120, [JULIAN_ID]: 80 };

function counterHit(product, branchId) {
  return {
    product_id: product.product_id,
    sku: product.sku,
    name: product.name,
    description: product.description,
    // The real counter search carries the product's application word; the
    // thumb chooses its drawn tile by it.
    application: product.category,
    base_uom: product.base_uom,
    sold_by: product.sold_by,
    has_image: false,
    match_reason: "NAME",
    on_hand_branch: ON_HAND_BY_BRANCH[branchId],
    on_hand_by_branch: ON_HAND_BY_BRANCH,
  };
}

// The catalogue list read the browse grid fills from: every row enriched with
// the retail anchor and the per-branch on-hand (the list read's own contract:
// absent-is-honest, and the demo world carries both), the featured flag, and
// no image (the category's drawn tile is the picture).
function productRow(product) {
  return {
    id: product.product_id,
    sku: product.sku,
    name: product.name,
    color_family: "",
    // The browse grid's category chips read the list row's application word;
    // the demo catalogue groups its five products under four of them.
    application: product.category,
    sold_by: product.sold_by,
    base_uom: product.base_uom,
    is_active: true,
    list_price_cents: product.list_cents,
    list_price_uom: product.sold_by,
    on_hand_by_branch: ON_HAND_BY_BRANCH,
    is_featured: product.sku === "HPV-60",
    has_image: false,
  };
}

// The pricing resolve's answer: one line priced through the same table the
// ring and the counter-price read answer from, in the contract's
// PricingBreakdown shape (the reference's own field names, byte for byte).
function resolveBreakdown(customerId, productId, branchId, qtyEntered, uomEntered) {
  const resolved = resolveLine(customerId, productId, branchId);
  const product = PRODUCTS.find((p) => p.product_id === productId);
  if (resolved === null || product === undefined) {
    return { status: 422, body: { error: `line could not be priced by the engine (${productId})` } };
  }
  const qty = typeof qtyEntered === "number" && qtyEntered > 0 ? qtyEntered : 1;
  const unit = resolved.customer_cents;
  const lineTotal = lineTotalCents(unit, qty);
  const costPerUnit = product.branch_cost_cents[branchId];
  const revenue = lineTotal;
  const cost = costPerUnit === undefined ? 0 : lineTotalCents(costPerUnit, qty);
  const source = resolved.basis === "ACCOUNT" ? "CUSTOMER_PRODUCT_RULE" : "LIST";
  return {
    status: 200,
    body: {
      original_cents: resolved.retail_cents,
      final_cents: unit,
      discount_bps:
        resolved.saving_cents > 0 ? Math.round((resolved.saving_cents / resolved.retail_cents) * 10000) : 0,
      source,
      detail: resolved.basis === "ACCOUNT" ? "The account's own rule price" : "Retail",
      price_uom: resolved.price_uom,
      qty_base: qty,
      line_total_cents: lineTotal,
      margin_bps: costPerUnit === undefined || revenue <= 0 ? 0 : marginBpsOf(revenue, cost),
      floor_bps: 0,
      blocked: false,
    },
  };
}

// The workspace row: one per account (the caller's own ergonomics), dealer
// defaults until a PUT writes the caller's own. The layout choice rides the
// same row (the save body's counter_layout), so the pick survives a reload.
const workspaceRows = new Map(); // user_id -> row

function workspaceRowFor(userId) {
  const row = workspaceRows.get(userId);
  if (row !== undefined) return { ...row, source: "user" };
  return {
    split_pct: 55,
    arrangement: "LOOKUP_LEFT",
    lookup_collapsed: false,
    sale_collapsed: false,
    lookup_presentation: "CARDS",
    margin_hidden: false,
    counter_layout: "STEPPER",
    source: "dealer",
  };
}

// The counter's salesperson roster: the demo accounts, in flow-table order.
const SALESPEOPLE = Object.values(USERS).map((account) => ({ id: account.user_id, name: account.display_name }));

function customerSearchRow(customer) {
  return {
    id: customer.id,
    name: customer.name,
    account_no: customer.account_no,
    customer_type: customer.customer_type,
    payment_terms_days: customer.payment_terms_days,
    credit_limit_cents: customer.credit_limit_cents,
    open_balance_cents: customer.open_balance_cents,
  };
}

// The test suite asserts the app sends the demo org on its demo calls, so the
// stub echoes the org it received back as a response header: a wrong or
// missing X-Org on the app side fails the suite right here, which is where a
// slip in the client configuration would surface on the real box too (the
// core's membership source answers for hh-demo and nothing else).
function sawOrg(request) {
  return { "x-stub-saw-org": request.headers["x-org"] ?? "" };
}

// The offline store's pull routes over this world (sync-stub.mjs): the till is read cache only, so it pulls and never pushes.
const syncStub = createSyncStub({
  products: () => PRODUCTS,
  customers: () => CUSTOMERS,
  parked: () => parked,
  homeBranchId: TRENTON_ID,
});

const server = createServer(async (request, response) => {
  const url = new URL(request.url ?? "/", `http://127.0.0.1:${port}`);
  const path = url.pathname;
  // Every api request is sighted for the harness's peek route: the path, and whether the approver's
  // header rode it (the credential's whole claim, one send and the approver's client alone).
  if (path.startsWith("/api/")) {
    SIGHTINGS.push({ path, approverHeader: (request.headers["x-approver-authorization"] ?? "") !== "" });
  }
  if (request.method === "GET" && path === "/healthz") {
    respondJson(response, 200, { ok: true });
    return;
  }
  if (request.method === "POST" && path === "/_stub/reseed-owing") {
    // Harness-only: put the two planted owing orders back as they were (a leg that takes a balance
    // leaves the world changed; the next leg starts from the planted one).
    owing.clear();
    PAY_BALANCE_KEYS.clear();
    seedOwingOrders();
    respondJson(response, 200, { ok: true });
    return;
  }

  if (request.method === "POST" && path === "/_stub/swap-failure") {
    // Harness-only: the next swap ring fails once, as "changed" (409) or "processor" (502).
    readBody(request, (parsed) => {
      swapFailure = parsed !== null && typeof parsed === "object" && ["changed", "processor"].includes(parsed.mode) ? parsed.mode : null;
      respondJson(response, 200, { swapFailure });
    });
    return;
  }
  if (request.method === "POST" && path === "/_stub/reseed-returns") {
    // Harness-only: put the returns world back as it started (a leg that commits a note leaves it changed).
    reseedReturns();
    respondJson(response, 200, { ok: true });
    return;
  }
  // Harness-only: the yard's acknowledgement and the finance signature, the
  // two halves stages 2 and 3 carry. The stub world performs them the way the
  // yard app and the money desk will (the ladder's own moves), so the till's
  // legs can walk a change to APPROVED without an identity this world cannot
  // mint. No contract path shares the namespace, and the app never calls it.
  // It sits with the other /_stub routes ABOVE the counter caller gate: the
  // world acts, and no counter identity is involved.
  if (request.method === "POST" && path === "/_stub/change-decide") {
    readBody(request, (parsed) => {
      const co = CHANGES.get(typeof parsed?.change_id === "string" ? parsed.change_id : "");
      if (co === undefined) {
        respondJson(response, 404, { error: "no such change" });
        return;
      }
      if (parsed?.decide === "yard-ack" && co.status === "PENDING_YARD") {
        co.status = co.finance_required ? "PENDING_FINANCE" : "APPROVED";
        co.yard_acked_by = "yard (world)";
        co.yard_acked_at = new Date().toISOString();
        co.yard_ack_note = typeof parsed.note === "string" ? parsed.note : "";
      } else if (parsed?.decide === "finance-approve" && co.status === "PENDING_FINANCE") {
        co.status = "APPROVED";
        co.finance_approved_by = "finance (world)";
        co.finance_approved_at = new Date().toISOString();
        co.finance_note = typeof parsed.note === "string" ? parsed.note : "";
      } else {
        respondJson(response, 409, { error: `cannot ${String(parsed?.decide)} from ${co.status}` });
        return;
      }
      respondJson(response, 200, co);
    });
    return;
  }

  if (request.method === "GET" && path === "/_stub/state") {
    // Harness-only: the tests' read back of this stub's ledgers. No contract path shares the
    // namespace, and the app never calls it.
    respondJson(response, 200, {
      keys: NOTE_KEYS_SEEN,
      lastNoteBody: LAST_NOTE_BODY,
      previewBodies: PREVIEW_BODIES,
      swapKeys: SWAP_KEYS_SEEN,
      lastSwapBody: LAST_SWAP_BODY,
      sightings: SIGHTINGS,
      approvalLimitEnabled,
      notes: [...NOTE_META.entries()].map(([key, meta]) => ({ key, ...meta })),
    });
    return;
  }

  if (request.method === "POST" && path === "/_stub/clock") {
    // Harness-only: an optional body {"now": <epoch ms>} pins the stub's clock to that instant (it
    // then runs on); an empty body puts it back on the host clock. A leg that pins the page's
    // clock to the dealer's evening pins this clock to the same instant, so the world the stub
    // builds and the day the page asks for can never disagree by the wall hour the suite ran at.
    readBody(request, (parsed) => {
      clockOffsetMs = parsed !== null && typeof parsed === "object" && typeof parsed.now === "number" ? parsed.now - Date.now() : 0;
      respondJson(response, 200, { ok: true, now: nowMs() });
    });
    return;
  }

  if (request.method === "POST" && path === "/_stub/refund-approval-limit") {
    readBody(request, (parsed) => {
      approvalLimitEnabled = parsed !== null && typeof parsed === "object" && parsed.enabled === true;
      respondJson(response, 200, { approvalLimitEnabled });
    });
    return;
  }

  // The demo routes stay public, exactly as the contract marks them, and echo
  // the org they were sent for the suite's own assertion.
  if (request.method === "GET" && path === "/api/v1/demo/flows") {
    respondJson(response, 200, { flows: FLOWS }, sawOrg(request));
    return;
  }
  if (request.method === "POST" && path === "/api/v1/demo/session") {
    const org = sawOrg(request);
    let body = "";
    request.on("data", (chunk) => {
      body += chunk;
    });
    request.on("end", () => {
      let flow;
      try {
        const parsed = JSON.parse(body);
        flow = parsed === null || typeof parsed !== "object" ? undefined : parsed.flow;
      } catch {
        respondJson(response, 400, { error: "the request body is not JSON" }, org);
        return;
      }
      if (typeof flow !== "string" || !Object.hasOwn(USERS, flow)) {
        respondJson(response, 400, { error: "unknown flow" }, org);
        return;
      }
      respondJson(response, 200, { token: mintToken(flow), user: USERS[flow], flow }, org);
    });
    return;
  }

  // The returns desk's routes and the Z report: staff routes that answer the
  // bearer alone (the reader above), ahead of the counter's own gate, because
  // their suite's own reads carry no org header.
  if (request.method === "GET" && path === "/api/v1/pos/returns/customer-lines") {
    const caller = callerFromRequest(request);
    if (caller === null) {
      respondJson(response, 401, { error: "no usable staff credential" });
      return;
    }
    // The core's own order of refusals: customer_id first (400), then the house WALK-IN (422 in
    // words), then the branch wall (403 naming the branch). Orders answer newest first, bounded.
    // A named customer is that customer (the spelled out walk in id still works). An EMPTY customer with a
    // number is the house walk in row, as the ring treats an empty customer; neither is a 400 in words.
    let customerId = (url.searchParams.get("customer_id") ?? "").trim();
    if (customerId === "") {
      if (normalizeSaleRef(url.searchParams.get("number") ?? "") === "") {
        respondJson(response, 400, { error: NAME_THE_CUSTOMER });
        return;
      }
      customerId = WALK_IN_ID;
    }
    // Ruling A: the house WALK-IN row is answered only through a receipt or order number, never listed.
    const number = normalizeSaleRef(url.searchParams.get("number") ?? "");
    if (customerId === WALK_IN_ID && number === "") {
      respondJson(response, 422, {
        error: "pos: walk-in sales are never listed; find the sale by the receipt or order number, and it goes back to the card it was paid on or in cash",
      });
      return;
    }
    const branchId = (url.searchParams.get("branch_id") ?? "").trim();
    if (branchId !== "" && branchId !== BRANCH_ID && branchId !== BRANCH_ID_2) {
      respondJson(response, 403, { error: `pos: branch ${branchId} is not one your grant reaches` });
      return;
    }
    const limit = Math.min(Math.max(Number(url.searchParams.get("limit")) || 25, 1), 100);
    // The number filter sits inside the customer's own orders, so it can never reach another's.
    const held = (CUSTOMER_ORDERS[customerId] ?? []).filter((order) => number === "" || saleRefMatches(order, number));
    const items = held
      .filter((order) => branchId === "" || order.branch_id === branchId)
      .slice(0, limit)
      .map(customerOrderView);
    respondJson(response, 200, { customer_id: customerId, items });
    return;
  }
  if (
    request.method === "GET" &&
    path.startsWith("/api/v1/till/sessions/") &&
    path.endsWith("/zreport")
  ) {
    const caller = callerFromRequest(request);
    if (caller === null) {
      respondJson(response, 401, { error: "no usable staff credential" });
      return;
    }
    const sessionId = path.split("/")[5];
    if (sessionId !== DRAWER.session_id) {
      respondJson(response, 404, { error: "no such till session" });
      return;
    }
    respondJson(response, 200, {
      session_id: DRAWER.session_id,
      per_tender: [{ method: "CASH", total_cents: DRAWER.cash_in_cents - DRAWER.cash_out_cents }],
      cash: {
        opening_float_cents: DRAWER.opening_float_cents,
        expected_cents: DRAWER.opening_float_cents + DRAWER.cash_in_cents - DRAWER.cash_out_cents,
        counted_cents: 0,
        over_short_cents: 0,
      },
      closing_float_cents: 0,
    });
    return;
  }
  if (request.method === "POST" && (path === "/api/v1/pos/swaps/preview" || path === "/api/v1/pos/swaps")) {
    const caller = callerFromRequest(request);
    if (caller === null) {
      respondJson(response, 401, { error: "no usable staff credential" });
      return;
    }
    const commit = path === "/api/v1/pos/swaps";
    const approver = approverFromRequest(request);
    if (
      approver.present &&
      (approver.account === null ||
        !APPROVER_ROLES.includes(approver.account.role_code) ||
        approver.account.user_id === caller.account.user_id)
    ) {
      respondJson(response, 403, { error: "the approver credential does not name an approver other than the clerk" });
      return;
    }
    let body;
    try {
      body = JSON.parse(await readRawBody(request));
    } catch {
      respondJson(response, 400, { error: "the request body is not JSON" });
      return;
    }
    if (!commit) {
      PREVIEW_BODIES.push(body);
      const figured = swapFigures(caller, body, approver, false);
      if (figured.ok !== true) {
        respondJson(response, figured.status, figured.body);
        return;
      }
      respondJson(response, 200, swapPreviewBody(figured.figures));
      return;
    }
    if (body !== null && typeof body === "object" && typeof body.idempotency_key === "string") {
      SWAP_KEYS_SEEN.push(body.idempotency_key);
      LAST_SWAP_BODY = body;
    }
    if (body !== null && typeof body === "object" && typeof body.idempotency_key === "string" && SWAPS_BY_KEY.has(body.idempotency_key)) {
      respondJson(response, 200, { ...SWAPS_BY_KEY.get(body.idempotency_key), replayed: true });
      return;
    }
    const figured = swapFigures(caller, body, approver, true);
    if (figured.ok !== true) {
      respondJson(response, figured.status, figured.body);
      return;
    }
    if (swapFailure === "changed") {
      swapFailure = null;
      respondJson(response, 409, { error: "pos: the swap's figures changed while it was being rung; review the swap and ring it again" });
      return;
    }
    if (swapFailure === "processor") {
      swapFailure = null;
      respondJson(response, 502, { error: "pos: card refund failed, the processor refused the refund" });
      return;
    }
    const committed = commitSwap(caller, body, approver, figured.figures);
    respondJson(response, committed.status, committed.body);
    return;
  }
  if (request.method === "POST" && path === "/api/v1/pos/returns/preview") {
    const caller = callerFromRequest(request);
    if (caller === null) {
      respondJson(response, 401, { error: "no usable staff credential" });
      return;
    }
    let body;
    try {
      body = JSON.parse(await readRawBody(request));
    } catch {
      respondJson(response, 400, { error: "the request body is not JSON" });
      return;
    }
    PREVIEW_BODIES.push(body);
    const refusal = noteRefusal(caller, body, { present: false, account: null }, false);
    if (refusal !== null) {
      respondJson(response, refusal.status, refusal.body);
      return;
    }
    respondJson(response, 200, previewNote(body, body.refund_method ?? null));
    return;
  }
  if (request.method === "POST" && path === "/api/v1/pos/returns-across") {
    const caller = callerFromRequest(request);
    if (caller === null) {
      respondJson(response, 401, { error: "no usable staff credential" });
      return;
    }
    const approver = approverFromRequest(request);
    if (
      approver.present &&
      (approver.account === null ||
        !APPROVER_ROLES.includes(approver.account.role_code) ||
        approver.account.user_id === caller.account.user_id)
    ) {
      respondJson(response, 403, { error: "the approver credential does not name an approver other than the clerk" });
      return;
    }
    let body;
    try {
      body = JSON.parse(await readRawBody(request));
    } catch {
      respondJson(response, 400, { error: "the request body is not JSON" });
      return;
    }
    if (body !== null && typeof body === "object" && typeof body.idempotency_key === "string") {
      NOTE_KEYS_SEEN.push(body.idempotency_key);
      LAST_NOTE_BODY = body;
    }
    const refusal = noteRefusal(caller, body, approver);
    if (refusal !== null) {
      respondJson(response, refusal.status, refusal.body);
      return;
    }
    const committed = commitNote(caller, body, approver);
    respondJson(response, committed.status, committed.body);
    return;
  }

  // The catalogue thumbnail route is ungated by design (the contract's own
  // words: an <img src> cannot carry an Authorization header), and a missing
  // image is a plain 404 the UI turns into its placeholder glyph. The demo
  // world carries no images.
  const imageMatch = path.match(/^\/api\/v1\/products\/([^/]+)\/image$/);
  if (request.method === "GET" && imageMatch !== null) {
    response.writeHead(404, { "content-type": "text/plain" });
    response.end("no image");
    return;
  }

  // The suite's peek at the sync ledger (a harness route, no credential).
  if (path === "/_stub/sync-log") {
    respondJson(response, 200, syncStub.log);
    return;
  }

  // Every route below is a counter route: the caller read comes first, org
  // header and all.
  const caller = callerOf(request, url);
  if (caller.refused !== undefined) {
    respondJson(response, caller.refused, { error: caller.error });
    return;
  }
  const callerBranch = caller.branchId;

  // The offline store's pull routes (and the refusals of any push: the till has no outbox).
  if (syncStub.handle({ request, response, path, url, respondJson })) return;

  if (request.method === "GET" && path === "/api/v1/branches") {
    respondJson(response, 200, BRANCHES.filter((b) => b.id === callerBranch));
    return;
  }

  if (request.method === "GET" && path === "/api/v1/till/sessions/open") {
    const branchId = url.searchParams.get("branch_id") ?? "";
    // The returns desk asks this route for the branch the found sale names
    // (the returns world's own branch), and the answer is that world's open
    // drawer, the session the cash rail and the Z report count. The counter's
    // own fence follows: a login asks about its branch, or it is refused.
    if (branchId === BRANCH_ID) {
      respondJson(response, 200, { open: true, id: DRAWER.session_id });
      return;
    }
    if (branchId !== callerBranch) {
      respondJson(response, 403, { error: "that branch is not one your grant reaches" });
      return;
    }
    const row = tillSessionRow(branchId);
    respondJson(
      response,
      200,
      row === null ? { open: false } : { open: true, id: row.id, opened_at: row.opened_at },
    );
    return;
  }

  if (request.method === "POST" && path === "/api/v1/till/sessions") {
    readBody(request, (parsed) => {
      const branchId = typeof parsed.branch_id === "string" ? parsed.branch_id : "";
      const station = typeof parsed.station === "string" ? parsed.station : "";
      const floatCents = parsed.float_cents;
      if (branchId !== callerBranch) {
        respondJson(response, 403, { error: "that branch is not one your grant reaches" });
        return;
      }
      if (station === "" || !Number.isInteger(floatCents) || floatCents < 0) {
        respondJson(response, 400, { error: "a drawer opens for a station with a counted float" });
        return;
      }
      if (tillSessionRow(branchId) !== null) {
        respondJson(response, 409, { error: "this branch's drawer is already open" });
        return;
      }
      const id = uuidFromCounter(7000 + drawers.size + 1);
      const row = {
        id,
        branch_id: branchId,
        station,
        status: "OPEN",
        opened_by: caller.userId,
        float_cents: floatCents,
        opened_at: "2026-01-01T00:00:00Z",
      };
      drawers.set(branchId, row);
      respondJson(response, 201, { id: row.id, status: "OPEN" });
    });
    return;
  }

  const tillSessionMatch = path.match(/^\/api\/v1\/till\/sessions\/([^/]+)$/);
  if (request.method === "GET" && tillSessionMatch !== null) {
    const row = [...drawers.values()].find((candidate) => candidate.id === tillSessionMatch[1]) ?? null;
    if (row === null) {
      respondJson(response, 404, { error: "no such till session" });
      return;
    }
    if (row.branch_id !== callerBranch) {
      respondJson(response, 403, { error: "that drawer is not one your grant reaches" });
      return;
    }
    respondJson(response, 200, tillSessionDetail(row));
    return;
  }

  if (request.method === "GET" && path === "/api/v1/products/counter-search") {
    const q = (url.searchParams.get("q") ?? "").trim().toLowerCase();
    if (q === "") {
      respondJson(response, 200, { items: [] });
      return;
    }
    // The requested branch is the counter's own (the fence is the caller's
    // grant); the stock is the contract's own per-branch answer.
    const requestedBranch = url.searchParams.get("branch_id") ?? callerBranch;
    const items = PRODUCTS.filter(
      (p) =>
        p.sku.toLowerCase().startsWith(q) ||
        p.name.toLowerCase().includes(q) ||
        p.aliases.some((alias) => alias.includes(q)),
    ).map((p) => counterHit(p, requestedBranch));
    respondJson(response, 200, { items });
    return;
  }

  // The account picker's read: the walk-in is excluded server-side (the
  // contract's own words), so the till never picks what it cannot bill.
  if (request.method === "GET" && path === "/api/v1/customers/search") {
    const q = (url.searchParams.get("q") ?? "").trim().toLowerCase();
    if (q === "") {
      respondJson(response, 200, []);
      return;
    }
    const rows = CUSTOMERS.filter(
      (c) => c.name.toLowerCase().includes(q) || c.account_no.toLowerCase().startsWith(q),
    ).map(customerSearchRow);
    respondJson(response, 200, rows);
    return;
  }

  // The standing read: what the account owes, its oldest bucket, and whether
  // a credit hold sits on it. The demo world carries one past-due bucket
  // (Fenwick's) so the three postures a counter meets are all visible: a
  // current account, one past due, and one on hold. The house walk-in has no
  // account and no balance, and the empty answer is the walk-in's answer.
  if (request.method === "GET" && path === "/api/v1/pos/account-standing") {
    const customerId = url.searchParams.get("customer_id") ?? "";
    if (customerId === "") {
      respondJson(response, 200, {});
      return;
    }
    const customer = CUSTOMERS.find((c) => c.id === customerId) ?? null;
    if (customer === null) {
      respondJson(response, 404, { error: "no such customer account" });
      return;
    }
    const pastDue = customer.account_no === "C02" ? 41500 : 0;
    respondJson(response, 200, {
      past_due_cents: pastDue,
      oldest_bucket: pastDue > 0 ? "30" : undefined,
      on_hold: customer.on_hold,
    });
    return;
  }

  // The counter price picture: the same table the ring answers from, with the
  // customer type resolved server-side so the screen says whose prices these
  // are without trusting the browser's claim.
  if (request.method === "POST" && path === "/api/v1/pos/counter-prices") {
    readBody(request, (parsed) => {
      const branchId = typeof parsed.branch_id === "string" ? parsed.branch_id : "";
      if (branchId !== callerBranch) {
        respondJson(response, 403, { error: "that branch is not one your grant reaches" });
        return;
      }
      const customerId = typeof parsed.customer_id === "string" ? parsed.customer_id : "";
      const customer = customerFor(customerId);
      if (customer === null) {
        respondJson(response, 404, { error: "no such customer account" });
        return;
      }
      const items = Array.isArray(parsed.items) ? parsed.items : [];
      const prices = items.map((item) => {
        const productId = typeof item?.product_id === "string" ? item.product_id : "";
        const resolved = resolveLine(customer.id, productId, branchId);
        if (resolved === null) {
          return { product_id: productId, price_uom: "", retail_cents: 0, customer_cents: 0, unpriceable: true };
        }
        return {
          product_id: resolved.product.product_id,
          price_uom: resolved.price_uom,
          retail_cents: resolved.retail_cents,
          customer_cents: resolved.customer_cents,
          ...(resolved.saving_cents > 0 ? { saving_cents: resolved.saving_cents } : {}),
          ...(resolved.basis !== undefined ? { basis: resolved.basis } : {}),
        };
      });
      respondJson(response, 200, { customer_type: customer.customer_type, prices });
    });
    return;
  }

  // The margin glance: cost against the SAME engine revenue, per line. A line
  // with no cost at this branch stays byte-absent in its position (the empty
  // object), which is exactly the placeholder case the till renders; the edge
  // restraint carries no Point Loma cost on purpose, so the demo shows a real
  // unknown beside real margins. Both demo logins are answered: the
  // configuration's cost and margin list puts the sales roles inside the
  // glance, and the till renders whatever arrives either way.
  if (request.method === "POST" && path === "/api/v1/pos/margin-glance") {
    readBody(request, (parsed) => {
      const branchId = typeof parsed.branch_id === "string" ? parsed.branch_id : "";
      if (branchId !== callerBranch) {
        respondJson(response, 403, { error: "that branch is not one your grant reaches" });
        return;
      }
      const customerId = typeof parsed.customer_id === "string" ? parsed.customer_id : "";
      const customer = customerFor(customerId);
      if (customer === null) {
        respondJson(response, 404, { error: "no such customer account" });
        return;
      }
      const lines = Array.isArray(parsed.lines) ? parsed.lines : [];
      const outLines = [];
      let revSum = 0;
      let costSum = 0;
      let known = 0;
      let partial = false;
      for (const line of lines) {
        const productId = typeof line?.product_id === "string" ? line.product_id : "";
        const qty = typeof line?.qty_entered === "number" ? line.qty_entered : 0;
        const resolved = resolveLine(customer.id, productId, branchId);
        const product = PRODUCTS.find((p) => p.product_id === productId) ?? null;
        const costPerUnit = product === null ? undefined : product.branch_cost_cents[branchId];
        if (resolved === null || product === null || costPerUnit === undefined || qty <= 0) {
          partial = true;
          outLines.push({});
          continue;
        }
        const revenue = lineTotalCents(resolved.customer_cents, qty);
        const cost = lineTotalCents(costPerUnit, qty);
        if (revenue <= 0) {
          partial = true;
          outLines.push({});
          continue;
        }
        outLines.push({ margin_bps: marginBpsOf(revenue, cost), est_profit_cents: revenue - cost });
        revSum += revenue;
        costSum += cost;
        known += 1;
      }
      respondJson(response, 200, {
        lines: outLines,
        ...(known > 0
          ? { margin_bps: marginBpsOf(revSum, costSum), est_profit_cents: revSum - costSum }
          : {}),
        ...(partial ? { partial: true } : {}),
      });
    });
    return;
  }

  // The card rail's status read: the simulated flag both desks' notices ride.
  if (request.method === "GET" && path === "/api/v1/payments/config") {
    respondJson(response, 200, PAYMENT_CONFIG);
    return;
  }

  // The ring: order, invoice and tender composed as one. The body carries no
  // price: every line is priced here through the same table the price read
  // answers from. The credit gate runs before any money moves, exactly where
  // the core's own gate sits.
  if (request.method === "POST" && path === "/api/v1/pos/sale-preview") {
    // The read-only preview (PR 222): the ring's own subtotal, tax and total for the body, with
    // nothing written. The tender-less body of a sale; a test that needs it slow holds the route itself.
    readBody(request, (parsed) => {
      const branchId = typeof parsed.branch_id === "string" ? parsed.branch_id : "";
      if (branchId !== callerBranch) {
        respondJson(response, 403, { error: "that branch is not one your grant reaches" });
        return;
      }
      const lines = Array.isArray(parsed.lines) ? parsed.lines : [];
      if (lines.length === 0) {
        respondJson(response, 400, { error: "a sale rings at least one line" });
        return;
      }
      const customer = customerFor(typeof parsed.customer_id === "string" ? parsed.customer_id : "");
      if (customer === null) {
        respondJson(response, 400, { error: "that customer is not in this dealer's book" });
        return;
      }
      // Item 9 delta review P3-d: the core's pay before release guard (item 9 review P3-7),
      // mirrored at the preview: the sale type is the guard of a Cash On Demand account, and an
      // account that pays on terms is refused here first, in the core's problem body, so the total
      // row says it before the ring.
      if (parsed.sale_type === "COD" && (customer.payment_terms_days ?? 0) > 0) {
        respondJson(response, 422, {
          error: "cod_not_for_terms_account",
          message: "This account pays on terms, so its sale is not pay before release. Ring it on account or paid in full.",
        });
        return;
      }
      const gross = [];
      for (let index = 0; index < lines.length; index += 1) {
        const line = lines[index] ?? {};
        const productId = typeof line.product_id === "string" ? line.product_id : "";
        const qty = typeof line.qty_entered === "number" ? line.qty_entered : 0;
        const resolved = resolveLine(customer.id, productId, branchId);
        if (resolved === null || qty <= 0) {
          respondJson(response, 400, { error: `line ${index + 1} could not be priced by the engine` });
          return;
        }
        gross.push(lineTotalCents(resolved.customer_cents, qty));
      }
      const discounted = applyStubDiscounts(gross, lines, parsed.discount);
      if (discounted.refusal) {
        respondJson(response, discounted.refusal.status, discounted.refusal.body);
        return;
      }
      const subtotal = discounted.nets.reduce((sum, n) => sum + n, 0);
      if (discounted.discountCents > 0 && subtotal <= 0) {
        respondJson(response, 422, { error: parsed.discount ? ZERO_TOTAL_WORDS : ZERO_TOTAL_LINE_WORDS });
        return;
      }
      const tax = taxCents(subtotal);
      respondJson(response, 200, {
        subtotal_cents: subtotal,
        tax_cents: tax,
        tax_lines: tax > 0 ? [{ reason_code: "STD_ON_HST_13", rate_bps: HST_RATE_BPS, tax_cents: tax }] : [],
        total_cents: subtotal + tax,
        tax_reason_code: "STD_ON_HST_13",
        payment_options: paymentOptionsFor(customer),
        supplier_deposit: supplierDepositFor(customer),
        ticket_base_cents: subtotal + discounted.saleCents,
        ticket_discount_max_cents: subtotal + discounted.saleCents - 1,
        ...(discounted.discountCents > 0
          ? {
              discount_cents: discounted.discountCents,
              sale_discount_cents: discounted.saleCents,
              lines: discounted.nets.map((net, i) => ({
                line_no: i + 1,
                line_total_cents: net,
                before_ticket_discount_cents: net + discounted.rows[i].sale,
                ...(discounted.rows[i].discount > 0 ? { discount_cents: discounted.rows[i].discount } : {}),
                ...(discounted.rows[i].sale > 0 ? { sale_discount_cents: discounted.rows[i].sale } : {}),
              })),
            }
          : {}),
      });
    });
    return;
  }

  if (request.method === "POST" && path === "/api/v1/pos/sales") {
    readBody(request, (parsed) => {
      const branchId = typeof parsed.branch_id === "string" ? parsed.branch_id : "";
      if (branchId !== callerBranch) {
        respondJson(response, 403, { error: "that branch is not one your grant reaches" });
        return;
      }
      const session = drawers.get(branchId);
      if (session === null || parsed.till_session_id !== session.id) {
        respondJson(response, 409, { error: "the sale's drawer is not this branch's open till session" });
        return;
      }
      const lines = Array.isArray(parsed.lines) ? parsed.lines : [];
      if (lines.length === 0) {
        respondJson(response, 400, { error: "a sale rings at least one line" });
        return;
      }
      const customerId = typeof parsed.customer_id === "string" ? parsed.customer_id : "";
      const customer = customerFor(customerId);
      if (customer === null) {
        respondJson(response, 404, { error: "no such customer account" });
        return;
      }
      const tender = parsed.tender;
      if (tender === "ACCOUNT" && customer.id === "") {
        respondJson(response, 400, { error: "the walk-in has no account to bill" });
        return;
      }
      // The ring's deposit, as the body sent it. The bound refusal below judges
      // the raw integer the way the core's own ring does: a negative deposit is
      // no deposit at all and meets the bound, it is not quietly read as none.
      const requestedDeposit =
        typeof parsed.deposit_cents === "number" && Number.isInteger(parsed.deposit_cents)
          ? parsed.deposit_cents
          : 0;
      // Item 9.5's refusals, the core's own rule: the supplier's demanded deposit rides only a
      // credit account's plain on-account ring (it is BILLED to the account), never above the
      // sale's pre-tax amount.
      const requestedSupplierDeposit =
        typeof parsed.supplier_deposit_cents === "number" &&
        Number.isInteger(parsed.supplier_deposit_cents) &&
        parsed.supplier_deposit_cents !== 0
          ? parsed.supplier_deposit_cents
          : 0;
      if (requestedSupplierDeposit !== 0) {
        const shapeFits =
          tender === "ACCOUNT" &&
          !Array.isArray(parsed.tenders) &&
          requestedDeposit === 0 &&
          (customer.payment_terms_days ?? 0) > 0 &&
          (customer.credit_limit_cents ?? 0) > 0;
        if (!shapeFits) {
          respondJson(response, 422, {
            error: "supplier_deposit_not_credit",
            message:
              "A supplier deposit is billed to a credit account, on account. This sale is not that shape: ring it as the account fits, or take the deposit at the counter.",
          });
          return;
        }
      }
      // Item 9 review P3-7, the core's own ruling: the COD sale type is the
      // guard of a Cash On Demand ACCOUNT (zero day terms, the walk in). An
      // account that pays on terms is not in it, whatever the tender.
      if (parsed.sale_type === "COD" && (customer.payment_terms_days ?? 0) > 0) {
        respondJson(response, 422, {
          error: "cod_not_for_terms_account",
          message:
            "This account pays on terms, so its sale is not pay before release. Ring it on account or paid in full.",
        });
        return;
      }
      // The COD constraint, the core's own gate for an unpaid-at-ring sale: a
      // COD sale draws on an account, and the walk-in has none (its limit is
      // no credit at all), so the ring answers the credit gate's refusal
      // before any money moves: the single tender and the split settle both
      // pass through here. The same word an on-hold account's sale meets
      // below; the policy names the constraint, and the account sales keep
      // their own arms.
      if (parsed.sale_type === "COD" && customer.id === "" && !(parsed.deposit_cents > 0)) {
        const blocked = creditBlocked("COD_CONSTRAINT");
        respondJson(response, blocked.status, blocked.body);
        return;
      }
      if (parsed.requested_date !== houseDay()) {
        respondJson(response, 400, {
          error: `the sale's requested date is not the dealer's day in the house zone (${HOUSE_ZONE})`,
        });
        return;
      }
      const idempotencyKey = typeof parsed.idempotency_key === "string" ? parsed.idempotency_key : "";
      const cachedOrderId = idempotencyKey === "" ? null : (salesByKey.get(idempotencyKey) ?? null);
      if (cachedOrderId !== null) {
        const cached = sales.get(cachedOrderId);
        if (cached !== undefined) {
          respondJson(response, 200, saleResponseBody(cached, true));
          return;
        }
      }
      let subtotal = 0;
      const outLines = [];
      const priced = [];
      const grossTotals = [];
      for (let index = 0; index < lines.length; index += 1) {
        const line = lines[index] ?? {};
        const productId = typeof line.product_id === "string" ? line.product_id : "";
        const qty = typeof line.qty_entered === "number" ? line.qty_entered : 0;
        const resolved = resolveLine(customer.id, productId, branchId);
        if (resolved === null || qty <= 0) {
          respondJson(response, 400, { error: `line ${index + 1} could not be priced by the engine` });
          return;
        }
        priced.push({ line, qty, resolved });
        grossTotals.push(lineTotalCents(resolved.customer_cents, qty));
      }
      const discounted = applyStubDiscounts(grossTotals, lines, parsed.discount);
      if (discounted.refusal) {
        respondJson(response, discounted.refusal.status, discounted.refusal.body);
        return;
      }
      if (discounted.discountCents > 0 && discounted.nets.reduce((sum, n) => sum + n, 0) <= 0) {
        respondJson(response, 422, { error: parsed.discount ? ZERO_TOTAL_WORDS : ZERO_TOTAL_LINE_WORDS });
        return;
      }
      for (let index = 0; index < priced.length; index += 1) {
        const { line, qty, resolved } = priced[index];
        const total = discounted.nets[index];
        subtotal += total;
        outLines.push({
          line_no: index + 1,
          // The line's own id and its share of the sale's tax: the returns
          // desk's reads and refund price the lines by them.
          order_line_id: uuidFromCounter(30000 + sales.size * 10 + index),
          product_id: resolved.product.product_id,
          description: resolved.product.name,
          qty_entered: qty,
          uom_entered: typeof line.uom_entered === "string" ? line.uom_entered : resolved.price_uom,
          unit_price_cents: resolved.customer_cents,
          price_uom: resolved.price_uom,
          line_total_cents: total,
          ...(discounted.rows[index].discount > 0 ? { discount_cents: discounted.rows[index].discount } : {}),
          ...(discounted.rows[index].sale > 0 ? { sale_discount_cents: discounted.rows[index].sale } : {}),
          tax_cents: Math.round((total * HST_RATE_BPS) / 10000),
        });
      }
      const tax = taxCents(subtotal);
      if (requestedSupplierDeposit > subtotal) {
        respondJson(response, 422, {
          error: "supplier_deposit_over_pretax",
          deposit_cents: requestedSupplierDeposit,
          subtotal_cents: subtotal,
          message: `The supplier deposit of ${dollarsWord(requestedSupplierDeposit)} is more than the sale's pre-tax ${dollarsWord(subtotal)}.`,
        });
        return;
      }
      const total = subtotal + tax;
      // The core's own deposit bound, judged once the lines are priced so the
      // body names the real total (0 < deposit < total: a deposit equal to the
      // total is just a paid sale, ring it paid; a negative one is no deposit
      // at all), and the collect now refusal AFTER it, in the server's order
      // (service.go prices, answers the bound, then refuses the collect now
      // deposit): so a collect now sale with a deposit at or over the total
      // meets deposit_out_of_range from the stub as from the server, and the
      // stub never hides a divergence behind the earlier word. Both answer as
      // the problem body the other deposit refusals use: a stable code, the
      // money as integer cents fields, and a message in dollars, the total
      // named the way the server's own dollars word names it.
      if (requestedDeposit !== 0 && (requestedDeposit < 0 || requestedDeposit >= total)) {
        respondJson(response, 422, {
          error: "deposit_out_of_range",
          deposit_cents: requestedDeposit,
          total_cents: total,
          message: `The deposit must be more than ${dollarsWord(0)} and less than the sale total of ${dollarsWord(total)}.`,
        });
        return;
      }
      if (requestedDeposit > 0 && (parsed.sale_type ?? "COLLECT_NOW") === "COLLECT_NOW") {
        respondJson(response, 422, {
          error: "deposit_not_collect_now",
          deposit_cents: requestedDeposit,
          total_cents: total,
          message: `A collect now sale is paid in full, the goods do not leave on a deposit. Take the whole ${dollarsWord(total)} now, or choose will call, delivery or a pay before release sale and leave a deposit.`,
        });
        return;
      }
      const tenders = [];
      let cashTendered;
      let changeDue;
      let paymentId;
      let gatewayRef;
      // The split settlement (the contract's tenders, 2 to 6 legs): the legs
      // must sum exactly to the sale's total, every leg names a rail, a card
      // leg carries the terminal's token, and an account leg needs an account
      // (the credit gate runs below, on the whole sale). The single-tender
      // arms below answer every other ring.
      const legs = Array.isArray(parsed.tenders) ? parsed.tenders : null;
      // Item 9.2's shape refusal, the core's own rule and order: whatever this
      // sale books ON ACCOUNT (the rest of a single ACCOUNT tender, a split's
      // account leg) needs an account the shape was offered to, terms above
      // zero AND a credit line. A zero terms or no credit account is refused
      // here, before the credit gate, with the on account amount named in
      // dollars (the core's problem body: integer cents fields, never cents in
      // words). The credit gate's own refusals keep their arms below, for the
      // accounts the shape fits.
      const accountBooks = tender === "ACCOUNT" || (legs !== null && legs.some((leg) => leg?.method === "ACCOUNT"));
      if (accountBooks && ((customer.payment_terms_days ?? 0) <= 0 || (customer.credit_limit_cents ?? 0) <= 0)) {
        const onAccountCents =
          legs !== null
            ? (legs.find((leg) => leg?.method === "ACCOUNT")?.amount_cents ?? 0)
            : total - requestedDeposit;
        respondJson(response, 422, {
          error: "on_account_not_available",
          amount_cents: onAccountCents,
          credit_limit_cents: customer.credit_limit_cents ?? 0,
          message: `This account cannot carry ${dollarsWord(onAccountCents)} on account. Pay in full now, or take a deposit with the balance due later.`,
        });
        return;
      }
      if (legs !== null) {
        if (legs.length < 2 || legs.length > 6) {
          respondJson(response, 400, { error: "a split settlement carries two to six legs" });
          return;
        }
        if (customer.id === "" && legs.some((leg) => leg?.method === "ACCOUNT")) {
          respondJson(response, 400, { error: "the walk-in has no account to bill" });
          return;
        }
        if (legs.some((leg) => leg?.method === "ACCOUNT")) {
          // The credit gate, before any money moves, on the whole sale: a
          // hold is a hard stop, and so is a total that would carry the
          // account past its limit.
          if (customer.on_hold === true) {
            const blocked = creditBlocked("COD_CONSTRAINT");
            respondJson(response, blocked.status, blocked.body);
            return;
          }
          if (customer.open_balance_cents + total > customer.credit_limit_cents) {
            const blocked = creditBlocked("CREDIT_LIMIT_EXCEEDED");
            respondJson(response, blocked.status, blocked.body);
            return;
          }
          customer.open_balance_cents += total;
        }
        let sum = 0;
        for (const [index, leg] of legs.entries()) {
          const method = leg?.method;
          const amount = leg?.amount_cents;
          if (!["CASH", "CARD", "CHEQUE", "ACCOUNT"].includes(method)) {
            respondJson(response, 400, { error: `leg ${index + 1} does not name a rail` });
            return;
          }
          if (!Number.isInteger(amount) || amount <= 0) {
            respondJson(response, 400, { error: `leg ${index + 1} carries no amount` });
            return;
          }
          if (method === "CARD" && typeof leg.payment_token !== "string") {
            respondJson(response, 400, { error: "a card leg carries the terminal's token, never a card number" });
            return;
          }
          sum += amount;
        }
        if (sum !== total) {
          // The contract's refusal (row 985): the stable word, the two amounts as
          // integer cents fields, and a sentence in dollars.
          respondJson(response, 422, {
            error: "split_total_mismatch",
            tendered_cents: sum,
            total_cents: total,
            message: `The tenders add up to ${dollarsWord(sum)}, but the sale total is ${dollarsWord(total)}.`,
          });
          return;
        }
        // The core's own capture shape: an ACCOUNT leg captures nothing (its
        // remainder rides the invoice's balance), so only the captured legs
        // land as tenders with their payment numbers, and the balance is the
        // total less what the ring itself took.
        legs.forEach((leg, index) => {
          if (leg.method === "ACCOUNT") return;
          const legPaymentId = nextDocNo("pay");
          if (paymentId === undefined) paymentId = legPaymentId;
          tenders.push({
            seq: tenders.length + 1,
            method: leg.method,
            amount_cents: leg.amount_cents,
            payment_id: legPaymentId,
            payment_no: nextDocNo("PMT"),
            ...(leg.method === "CARD" ? { gateway_ref: `rp_sim_${docCounter + 1}` } : {}),
          });
        });
      } else if (parsed.sale_type === "COD") {
        // Pay before release: nothing is captured at ring, whatever the tender tile names (it is
        // the settlement method only, as the core's ring takes no tender for the sale type). With
        // a deposit, the deposit is the only capture (its own leg lands below); the rest is the
        // invoice's balance, held from picking until it is paid.
        if (requestedDeposit > 0 && parsed.deposit_method === "CARD" && (typeof parsed.payment_token !== "string" || parsed.payment_token === "")) {
          respondJson(response, 400, { error: "a card deposit carries the terminal's token, never a card number" });
          return;
        }
      } else if (tender === "CASH") {
        // The contract's own rule: cash_tendered_cents 0 (or absent) means not
        // entered, and the sale rings for the exact total. A typed figure that
        // falls short is refused; one that covers names the change due.
        cashTendered = typeof parsed.cash_tendered_cents === "number" ? parsed.cash_tendered_cents : 0;
        if (!Number.isInteger(cashTendered) || cashTendered < 0) {
          respondJson(response, 400, { error: "the cash tendered is not an amount" });
          return;
        }
        if (cashTendered > 0 && cashTendered < total) {
          respondJson(response, 400, { error: "the cash tendered does not cover the sale's total" });
          return;
        }
        changeDue = cashTendered > 0 ? cashTendered - total : 0;
        paymentId = nextDocNo("pay");
        tenders.push({ seq: 1, method: "CASH", amount_cents: total, payment_id: paymentId, payment_no: nextDocNo("PMT") });
      } else if (tender === "CARD") {
        const token = parsed.payment_token;
        if (typeof token !== "string" || token === "") {
          respondJson(response, 400, { error: "a card tender carries the terminal's token, never a card number" });
          return;
        }
        paymentId = nextDocNo("pay");
        gatewayRef = `rp_sim_${docCounter + 1}`;
        tenders.push({
          seq: 1,
          method: "CARD",
          amount_cents: total,
          payment_id: paymentId,
          payment_no: nextDocNo("PMT"),
          gateway_ref: gatewayRef,
        });
      } else if (tender === "ACCOUNT") {
        // The credit gate, before any money moves: a hold is a hard stop, and
        // so is a total that would carry the account past its limit.
        if (customer.on_hold === true) {
          const blocked = creditBlocked("COD_CONSTRAINT");
          respondJson(response, blocked.status, blocked.body);
          return;
        }
        if (customer.open_balance_cents + total - requestedDeposit > customer.credit_limit_cents) {
          const blocked = creditBlocked("CREDIT_LIMIT_EXCEEDED");
          respondJson(response, blocked.status, blocked.body);
          return;
        }
        customer.open_balance_cents += total - requestedDeposit;
      } else if (tender !== "CHEQUE") {
        respondJson(response, 400, { error: "unknown tender" });
        return;
      }
      // The deposit rides an unpaid-at-ring sale (Charge Account or COD):
      // the ring's answer carries the figure back, because the
      // confirmation's balance is the total less this deposit and the wire's
      // own number is the one the till repeats. The deposit is money taken
      // through the drawer at ring, so it lands as a tender leg here the way
      // the core's own capture leg does, and the sale carries the invoice's
      // balance (the total less every captured leg) for the receipt.
      const depositCents =
        typeof parsed.deposit_cents === "number" &&
        Number.isInteger(parsed.deposit_cents) &&
        parsed.deposit_cents > 0
          ? parsed.deposit_cents
          : 0;
      if (depositCents > 0) {
        // The deposit's own capture leg: the till's sole writer shape, the
        // method the ring named (CASH when the body omits it), its own
        // payment number for the receipt's tender reference.
        const depositPaymentId = nextDocNo("pay");
        tenders.push({
          seq: tenders.length + 1,
          method: typeof parsed.deposit_method === "string" && parsed.deposit_method !== "" ? parsed.deposit_method : "CASH",
          amount_cents: depositCents,
          payment_id: depositPaymentId,
          payment_no: nextDocNo("PMT"),
        });
        if (paymentId === undefined) paymentId = depositPaymentId;
      }
      const orderId = uuidFromCounter(4000 + sales.size + 1);
      // The sale's tender word: with a split settlement the contract says the
      // body's tender field is ignored, so the row derives one word from the
      // legs (the confirmation's own vocabulary) instead of storing ''.
      const saleTender =
        legs !== null
          ? legs.some((leg) => leg.method === "ACCOUNT")
            ? "ACCOUNT"
            : legs.some((leg) => leg.method === "CARD")
              ? "CARD"
              : "CASH"
          : tender;
      const sale = {
        order_id: orderId,
        order_no: nextDocNo("ORD"),
        invoice_id: uuidFromCounter(5000 + sales.size + 1),
        invoice_no: nextDocNo("INV"),
        tender: saleTender,
        split_tender: legs !== null,
        sale_type: typeof parsed.sale_type === "string" ? parsed.sale_type : "COLLECT_NOW",
        branch_id: branchId,
        customer_id: customer.id,
        subtotal_cents: subtotal,
        tax_cents: tax,
        total_cents: total,
        tax_reason_code: "STD_ON_HST_13",
        status: "COMMITTED",
        cash_tendered_cents: cashTendered,
        change_due_cents: changeDue,
        deposit_cents: depositCents,
        supplier_deposit_cents: requestedSupplierDeposit > 0 ? requestedSupplierDeposit : undefined,
        supplier_deposit_invoice_id:
          requestedSupplierDeposit > 0 ? uuidFromCounter(7000 + sales.size + 1) : undefined,
        supplier_deposit_invoice_no: requestedSupplierDeposit > 0 ? nextDocNo("INV") : undefined,
        balance_cents: total - tenders.reduce((sum, leg) => sum + leg.amount_cents, 0),
        created_at: new Date(nowMs()).toISOString(),
        created_by: caller.userId,
        payment_id: paymentId,
        gateway_ref: gatewayRef,
        tenders,
        lines: outLines,
        discount_cents: discounted.discountCents > 0 ? discounted.discountCents : undefined,
        discounts: discounted.discounts.length > 0 ? discounted.discounts : undefined,
      };
      sales.set(orderId, sale);
      if (depositCents > 0 && sale.balance_cents > 0) {
        // A deposit sale owes: it joins the pay balance world, held when it is a pay before release sale.
        addOwing({
          order_id: orderId,
          order_no: sale.order_no,
          branch_id: branchId,
          customer_id: customer.id,
          customer_name: customer.name,
          account_line: `${customer.account_no} · ${customer.customer_type === "RETAIL" ? "Walk-in" : "Account"}`,
          fulfilment: parsed.fulfilment ?? (parsed.sale_type === "DELIVERED" ? "DELIVERY" : "WILL_CALL"),
          sale_type: sale.sale_type,
          order_total_cents: total,
          held: parsed.sale_type === "COD",
          payments: tenders.map((t) => ({ payment_id: t.payment_id, method: t.method, amount_cents: t.amount_cents })),
          lines: outLines.map((l) => ({
            description: l.description,
            qty: `${l.qty_entered} ${l.uom_entered}`,
            line_total_cents: l.line_total_cents,
          })),
        });
      }
      if (idempotencyKey !== "") salesByKey.set(idempotencyKey, orderId);
      respondJson(response, 201, saleResponseBody(sale, false));
    });
    return;
  }

  // Item 9.4: find the orders that owe, by order number or customer name (the core also matches a phone).
  if (request.method === "GET" && path === "/api/v1/pos/pay-balance") {
    const term = (url.searchParams.get("q") ?? "").trim().toLowerCase();
    if (term === "") {
      respondJson(response, 400, { error: "q is required: an order number, a customer name or a phone" });
      return;
    }
    const items = [...owing.values()]
      .filter((o) => o.branch_id === callerBranch && owingStill(o) > 0)
      .filter((o) => o.order_no.toLowerCase().includes(term) || o.customer_name.toLowerCase().includes(term))
      .map((o) => owingBody(o));
    // The core answers at most twenty and says so when more match (item 9 F review N5).
    respondJson(response, 200, { items: items.slice(0, 20), truncated: items.length > 20 });
    return;
  }

  // Item 9.4: take the whole balance, CASH or CARD, idempotent by key.
  if (request.method === "POST" && path === "/api/v1/pos/pay-balance") {
    readBody(request, (parsed) => {
      const key = typeof parsed.idempotency_key === "string" ? parsed.idempotency_key : "";
      const replay = key === "" ? undefined : PAY_BALANCE_KEYS.get(key);
      if (replay !== undefined) {
        respondJson(response, 200, { ...replay, replayed: true });
        return;
      }
      const order = owing.get(typeof parsed.order_id === "string" ? parsed.order_id : "");
      if (order === undefined || order.branch_id !== callerBranch) {
        respondJson(response, 404, { error: "no such order at this branch" });
        return;
      }
      const balance = owingStill(order);
      if (balance <= 0) {
        respondJson(response, 409, { error: "The order is paid in full." });
        return;
      }
      if (parsed.method !== "CASH" && parsed.method !== "CARD") {
        respondJson(response, 400, { error: "the method is CASH or CARD" });
        return;
      }
      if (parsed.amount_cents !== balance) {
        respondJson(response, 422, {
          error: "pay_balance_whole_only",
          sent_cents: parsed.amount_cents,
          balance_cents: balance,
          message: `This door takes the whole balance of ${dollarsWord(balance)}, not a part payment.`,
        });
        return;
      }
      const session = drawers.get(callerBranch);
      if (session === undefined || session === null || parsed.till_session_id !== session.id) {
        respondJson(response, 409, { error: "the payment's drawer is not this branch's open till session" });
        return;
      }
      if (parsed.method === "CARD" && (typeof parsed.payment_token !== "string" || parsed.payment_token === "")) {
        respondJson(response, 400, { error: "a card payment carries the terminal's token, never a card number" });
        return;
      }
      const paymentId = nextDocNo("pay");
      order.payments.push({ payment_id: paymentId, method: parsed.method, amount_cents: balance });
      const wasHeld = order.held;
      order.held = false;
      const answer = {
        order_id: order.order_id,
        order_no: order.order_no,
        customer_id: order.customer_id,
        payment_id: paymentId,
        method: parsed.method,
        amount_cents: balance,
        paid_words: "Paid in full",
        released_to_picking: wasHeld,
        still_owed_cents: 0,
        held_from_picking_now: false,
        payments: order.payments.map((pay) => ({ ...pay })),
      };
      PAY_BALANCE_KEYS.set(key, answer);
      respondJson(response, 201, answer);
    });
    return;
  }

  // Item 9.3 plus the change-order stage: the staff order read, the core's
  // OrderView field by field (status, this caller's legal actions and editable
  // fields, the lines and the money, the pay hold on an order that owes).
  const orderMatch = path.match(/^\/api\/v1\/orders\/([^/]+)$/);
  if (request.method === "GET" && orderMatch !== null) {
    const found = orderForRead(orderMatch[1], callerBranch);
    if (found === null || found.row.branch_id !== callerBranch) {
      respondJson(response, 404, { error: "no such order" });
      return;
    }
    const order = found.row;
    const status = orderStatusOf(order.order_id);
    const walls = changeWalls(caller);
    const viewLines =
      found.kind === "sale"
        ? order.lines.map((l) => ({
            line_no: l.line_no,
            product_id: l.product_id,
            description: l.description,
            qty_entered: qtyWord(l.qty_entered),
            uom_entered: l.uom_entered,
            qty_base: qtyWord(l.qty_entered),
            allocated_base: "0.0000",
            fulfilled_base: "0.0000",
            unit_price_cents: l.unit_price_cents,
            line_total_cents: l.line_total_cents,
          }))
        : order.lines.map((l, index) => {
            const parts = /^([0-9.]+)\s+(\S+)$/.exec(l.qty ?? "") ?? null;
            return {
              line_no: index + 1,
              product_id: "",
              description: l.description,
              qty_entered: parts === null ? "0.0000" : qtyWord(Number(parts[1])),
              uom_entered: parts === null ? "" : parts[2],
              qty_base: parts === null ? "0.0000" : qtyWord(Number(parts[1])),
              allocated_base: "0.0000",
              fulfilled_base: "0.0000",
              unit_price_cents: 0,
              line_total_cents: l.line_total_cents,
            };
          });
    const subtotal = found.kind === "sale" ? order.subtotal_cents : order.order_total_cents;
    const tax = found.kind === "sale" ? order.tax_cents : 0;
    // A deposit sale that still owes joins the pay balance world too (the ring
    // plants it in both): the sale row serves the lines and the money here,
    // and the owing twin decides the hold, exactly the one OrderView the core
    // serves with pay_hold riding beside the lines.
    const owed = owing.get(order.order_id);
    respondJson(response, 200, {
      id: order.order_id,
      order_no: order.order_no,
      customer_id: order.customer_id,
      branch_id: order.branch_id,
      status,
      legal_actions: legalActionsFor(status),
      editable_fields: editableFieldsFor(status, walls.manager),
      lines: viewLines,
      subtotal_cents: subtotal,
      tax_cents: tax,
      total_cents: subtotal + tax,
      has_backorder: false,
      ...(found.kind === "owing" || owed !== undefined ? { pay_hold: payHoldBody(owed ?? order) } : {}),
    });
    return;
  }

  // The staff transition the legs use to start a pick (the core's manual
  // matrix: CONFIRMED takes PICKING; the census row is staff at quote-desk
  // work, so both logins may move it).
  const pickingMatch = path.match(/^\/api\/v1\/orders\/([^/]+)\/picking$/);
  if (request.method === "POST" && pickingMatch !== null) {
    const found = orderForRead(pickingMatch[1], callerBranch);
    if (found === null || found.row.branch_id !== callerBranch) {
      respondJson(response, 404, { error: "not found" });
      return;
    }
    const from = orderStatusOf(found.row.order_id);
    if (from !== "CONFIRMED") {
      respondJson(response, 409, { error: `order: this order is ${from}, picking is no longer available` });
      return;
    }
    ORDER_STATUS.set(found.row.order_id, "PICKING");
    respondJson(response, 200, { order_id: found.row.order_id, status: "PICKING" });
    return;
  }

  // The line edits (DIB-175): the census walls first (the shared Forbidden
  // word a non-manager earns), then the matrix's own refusal naming the
  // status, then the write, priced at the line's own unit (the stub stands in
  // for the pricing seam; no price ever arrives from the client).
  const lineMatch = path.match(/^\/api\/v1\/orders\/([^/]+)\/lines\/([0-9]+)$/);
  if (request.method === "PATCH" && lineMatch !== null) {
    const walls = changeWalls(caller);
    if (!walls.lineEdits) {
      respondJson(response, 403, { error: "Forbidden" });
      return;
    }
    const found = orderForRead(lineMatch[1], callerBranch);
    if (found === null || found.row.branch_id !== callerBranch || found.kind !== "sale") {
      respondJson(response, 404, { error: "not found" });
      return;
    }
    const status = orderStatusOf(found.row.order_id);
    if (!["DRAFT", "CONFIRMED", "PICKING"].includes(status)) {
      respondJson(response, 409, {
        error: `order: line quantity cannot be edited while the order is ${status}`,
      });
      return;
    }
    readBody(request, (parsed) => {
      const raw = typeof parsed?.qty_entered === "string" ? parsed.qty_entered.trim() : "";
      const qty = Number(raw);
      if (raw === "") {
        respondJson(response, 400, { error: "qty_entered is required" });
        return;
      }
      if (!Number.isFinite(qty) || qty <= 0) {
        respondJson(response, 400, { error: "qty_entered must be greater than zero, remove the line instead" });
        return;
      }
      const lineNo = Number(lineMatch[2]);
      const line = found.row.lines.find((l) => l.line_no === lineNo);
      if (line === undefined) {
        respondJson(response, 404, { error: "order: line not found" });
        return;
      }
      const beforeQty = line.qty_entered;
      const beforeTotal = line.line_total_cents;
      line.qty_entered = qty;
      line.qty_base = qty;
      line.line_total_cents = lineTotalCents(line.unit_price_cents, qty);
      line.tax_cents = Math.round((line.line_total_cents * HST_RATE_BPS) / 10000);
      const subtotal = found.row.lines.reduce((sum, l) => sum + l.line_total_cents, 0);
      found.row.subtotal_cents = subtotal;
      found.row.tax_cents = taxCents(subtotal);
      found.row.total_cents = subtotal + found.row.tax_cents;
      const requestId = `req-${++editRequestCounter}`;
      const actor = Object.values(USERS).find((u) => u.user_id === caller.userId) ?? {
        user_id: caller.userId,
        display_name: "Staff (demo)",
      };
      const stamp = new Date().toISOString();
      const rows = [
        { field: "qty_entered", line_no: lineNo, before: qtyWord(beforeQty), after: qtyWord(qty), actor_id: actor.user_id, actor_name: actor.display_name, request_id: requestId, occurred_at: stamp },
        { field: "qty_base", line_no: lineNo, before: qtyWord(beforeQty), after: qtyWord(qty), actor_id: actor.user_id, actor_name: actor.display_name, request_id: requestId, occurred_at: stamp },
        { field: "line_total_cents", line_no: lineNo, before: String(beforeTotal), after: String(line.line_total_cents), actor_id: actor.user_id, actor_name: actor.display_name, request_id: requestId, occurred_at: stamp },
        { field: "subtotal_cents", line_no: 0, before: "", after: String(subtotal), actor_id: actor.user_id, actor_name: actor.display_name, request_id: requestId, occurred_at: stamp },
      ];
      const trail = EDITS.get(found.row.order_id) ?? [];
      trail.push(...rows);
      EDITS.set(found.row.order_id, trail);
      respondJson(response, 200, {
        order_id: found.row.order_id,
        status,
        subtotal_cents: subtotal,
        total_cents: found.row.total_cents,
        line: {
          line_no: lineNo,
          product_id: line.product_id,
          qty_entered: qtyWord(qty),
          qty_base: qtyWord(qty),
          unit_price_cents: line.unit_price_cents,
          line_total_cents: line.line_total_cents,
        },
      });
    });
    return;
  }

  // The edits trail (DIB-175): the audit rows the writes above appended,
  // oldest first, grouped client-side by request id.
  const editsMatch = path.match(/^\/api\/v1\/orders\/([^/]+)\/edits$/);
  if (request.method === "GET" && editsMatch !== null) {
    const found = orderForRead(editsMatch[1], callerBranch);
    if (found === null || found.row.branch_id !== callerBranch) {
      respondJson(response, 404, { error: "not found" });
      return;
    }
    respondJson(response, 200, {
      order_id: found.row.order_id,
      changes: EDITS.get(found.row.order_id) ?? [],
    });
    return;
  }

  // The reservations read (W5 seam 3): the lines' holds, HARD in this world.
  const reservationsMatch = path.match(/^\/api\/v1\/orders\/([^/]+)\/reservations$/);
  if (request.method === "GET" && reservationsMatch !== null) {
    const found = orderForRead(reservationsMatch[1], callerBranch);
    if (found === null || found.row.branch_id !== callerBranch || found.kind !== "sale") {
      respondJson(response, 404, { error: "not found" });
      return;
    }
    const status = orderStatusOf(found.row.order_id);
    const holds =
      status === "PICKING" || status === "READY" || status === "PARTIALLY_FULFILLED"
        ? found.row.lines.map((l) => {
            const product = PRODUCTS.find((p) => p.product_id === l.product_id) ?? null;
            return {
              reservation_id: `res-${found.row.order_id}-${l.line_no}`,
              product_id: l.product_id,
              product_name: product === null ? l.description : product.name,
              sku: product === null ? "" : product.sku,
              branch_id: found.row.branch_id,
              hold_mode: "HARD",
              qty_base: l.qty_entered,
              status: "ACTIVE",
              created_at: found.row.created_at,
            };
          })
        : [];
    respondJson(response, 200, {
      order_id: found.row.order_id,
      status,
      seam_wired: true,
      reservations: holds,
      held_base: 0,
    });
    return;
  }

  // The change orders (SCOPE:F10): list and raise, then the decisions on one
  // request. Every refusal below is the core's own sentence; the walls answer
  // the role gate's shared Forbidden word.
  const changesMatch = path.match(/^\/api\/v1\/orders\/([^/]+)\/changes$/);
  if (request.method === "GET" && changesMatch !== null) {
    const found = orderForRead(changesMatch[1], callerBranch);
    if (found === null || found.row.branch_id !== callerBranch) {
      respondJson(response, 404, { error: "not found" });
      return;
    }
    const walls = changeWalls(caller);
    const rows = [...CHANGES.values()]
      .filter((co) => co.order_id === found.row.order_id)
      .map((co) => ({ ...co, next_actions: changeNextActions(co.status, walls.manager) }))
      .reverse();
    respondJson(response, 200, { changes: rows });
    return;
  }
  if (request.method === "POST" && changesMatch !== null) {
    const found = orderForRead(changesMatch[1], callerBranch);
    if (found === null || found.row.branch_id !== callerBranch) {
      respondJson(response, 404, { error: "not found" });
      return;
    }
    readBody(request, (parsed) => {
      const status = orderStatusOf(found.row.order_id);
      // uq_change_orders_one_open: at most one live request per order, live
      // being DRAFT, PENDING_YARD, PENDING_FINANCE or APPROVED.
      const openExists = [...CHANGES.values()].some(
        (co) =>
          co.order_id === found.row.order_id &&
          ["DRAFT", "PENDING_YARD", "PENDING_FINANCE", "APPROVED"].includes(co.status),
      );
      if (openExists) {
        respondJson(response, 409, {
          error: "this order already has an open change request, decide it before raising another",
        });
        return;
      }
      const rawLines = Array.isArray(parsed?.lines) ? parsed.lines : [];
      if (rawLines.length === 0) {
        respondJson(response, 400, { error: "order: a change order must carry at least one line change" });
        return;
      }
      // The post-pick gate, the core's own order: the status ladder decides
      // here (this world has no tasks substrate), and the refusal names the
      // line-edit path.
      if (!statusIsPostPick(status)) {
        respondJson(response, 409, {
          error: "order: this order's pick has not started, use the line-edit endpoints, not a change order",
        });
        return;
      }
      // Price the delta once, at request time, at the line's own unit price.
      const lines = [];
      let deltaCents = 0;
      for (const raw of rawLines) {
        const op = typeof raw?.op === "string" ? raw.op.toUpperCase() : "";
        if (op !== "QTY") {
          respondJson(response, 400, { error: `line 1: unknown change op ${JSON.stringify(raw?.op ?? "")} (ADD|QTY|REMOVE)` });
          return;
        }
        const lineNo = raw.line_no;
        const line = found.row.lines.find((l) => l.line_no === lineNo);
        if (typeof lineNo !== "number" || line === undefined) {
          respondJson(response, 404, { error: "order: line not found" });
          return;
        }
        const qty = Number(typeof raw.qty_entered === "string" ? raw.qty_entered.trim() : "");
        if (!Number.isFinite(qty) || qty <= 0) {
          respondJson(response, 400, { error: "qty_entered must be greater than zero, remove the line instead" });
          return;
        }
        const before = line.qty_entered;
        const after = lineTotalCents(line.unit_price_cents, qty);
        lines.push({
          seq: lines.length + 1,
          op: "QTY",
          line_no: lineNo,
          product_id: line.product_id,
          product_name: line.description,
          description: line.description,
          qty_entered_new: qtyWord(qty),
          uom_entered: line.uom_entered,
          qty_base_before: before,
          qty_base_after: qty,
          qty_delta_base: qty - before,
          unit_price_cents: line.unit_price_cents,
          price_uom: line.uom_entered,
          line_delta_cents: after - line.line_total_cents,
        });
        deltaCents += after - line.line_total_cents;
      }
      const financeRequired = Math.abs(deltaCents) >= FINANCE_THRESHOLD_CENTS;
      const id = uuidFromCounter(60000 + ++changeCounter);
      const co = {
        id,
        order_id: found.row.order_id,
        branch_id: found.row.branch_id,
        change_no: `CHG-${String(changeCounter).padStart(4, "0")}`,
        status: "PENDING_YARD",
        reason: typeof parsed?.reason === "string" ? parsed.reason.trim() : "",
        delta_cents: deltaCents,
        finance_threshold_cents: FINANCE_THRESHOLD_CENTS,
        finance_required: financeRequired,
        order_status_at_request: status,
        pick_started_at_request: true,
        requested_by: caller.userId,
        requested_at: new Date().toISOString(),
        yard_task_id: `task-${found.row.order_id}`,
        lines,
      };
      CHANGES.set(id, co);
      respondJson(response, 201, { ...co, next_actions: changeNextActions(co.status, changeWalls(caller).manager) });
    });
    return;
  }

  const changeMatch = path.match(/^\/api\/v1\/order-changes\/([^/]+)(\/([a-z-]+))?$/);
  if (changeMatch !== null && request.method === "POST") {
    const co = CHANGES.get(changeMatch[1]);
    if (co === undefined || co.branch_id !== callerBranch) {
      respondJson(response, 404, { error: "not found" });
      return;
    }
    const action = changeMatch[3] ?? "";
    const walls = changeWalls(caller);
    const manager = walls.manager;
    const terminal = () =>
      respondJson(response, 409, { error: "order: this change order is already closed" });
    if (action === "yard-ack") {
      // The census row walls the ack to yard-pick work: neither demo login
      // holds it, so the answer is the role gate's own shared body.
      respondJson(response, 403, { error: "Forbidden" });
      return;
    }
    if (action === "finance-approve") {
      // money-desk manage: financial controller and general manager alone.
      respondJson(response, 403, { error: "Forbidden" });
      return;
    }
    if (action === "reject" || action === "cancel") {
      if (isTerminalChange(co.status)) return terminal();
      co.status = action === "reject" ? "REJECTED" : "CANCELLED";
      co.decision_note = "";
      respondJson(response, 200, { ...co, next_actions: changeNextActions(co.status, manager) });
      return;
    }
    if (action === "apply") {
      if (!walls.apply) {
        respondJson(response, 403, { error: "Forbidden" });
        return;
      }
      if (isTerminalChange(co.status)) return terminal();
      if (co.status === "PENDING_YARD") {
        respondJson(response, 409, { error: "the yard has not acknowledged this change yet" });
        return;
      }
      if (co.status === "PENDING_FINANCE") {
        respondJson(response, 409, {
          error: "this change is over the finance threshold and has not been approved",
        });
        return;
      }
      if (co.status !== "APPROVED") {
        respondJson(response, 409, { error: "order: this change is not approved" });
        return;
      }
      // Apply writes the priced delta onto the lines and recomputes the
      // totals from the lines, never a delta increment.
      const sale = sales.get(co.order_id);
      if (sale === undefined) {
        respondJson(response, 409, { error: "order: this change order is already closed" });
        return;
      }
      for (const l of co.lines) {
        const line = sale.lines.find((row) => row.line_no === l.line_no);
        line.qty_entered = l.qty_base_after;
        line.qty_base = l.qty_base_after;
        line.line_total_cents = lineTotalCents(line.unit_price_cents, l.qty_base_after);
        line.tax_cents = Math.round((line.line_total_cents * HST_RATE_BPS) / 10000);
      }
      const subtotal = sale.lines.reduce((sum, l) => sum + l.line_total_cents, 0);
      sale.subtotal_cents = subtotal;
      sale.tax_cents = taxCents(subtotal);
      sale.total_cents = subtotal + sale.tax_cents;
      // The order's owing twin (a deposit sale that still owes) follows the
      // new total, so the pay balance world and the order read never disagree.
      const owed = owing.get(sale.order_id);
      if (owed !== undefined) owed.order_total_cents = sale.total_cents;
      co.status = "APPLIED";
      co.applied_at = new Date().toISOString();
      respondJson(response, 200, { ...co, next_actions: changeNextActions(co.status, manager) });
      return;
    }
    respondJson(response, 404, { error: "not found" });
    return;
  }
  if (changeMatch !== null && request.method === "GET" && changeMatch[3] === undefined) {
    const co = CHANGES.get(changeMatch[1]);
    if (co === undefined || co.branch_id !== callerBranch) {
      respondJson(response, 404, { error: "not found" });
      return;
    }
    respondJson(response, 200, {
      ...co,
      next_actions: changeNextActions(co.status, changeWalls(caller).manager),
    });
    return;
  }

  const saleMatch = path.match(/^\/api\/v1\/pos\/sales\/([^/]+)$/);
  if (request.method === "GET" && saleMatch !== null) {
    const sale = sales.get(saleMatch[1]) ?? null;
    if (sale === null) {
      respondJson(response, 404, { error: "no such sale" });
      return;
    }
    if (sale.branch_id !== callerBranch) {
      respondJson(response, 403, { error: "that sale is not one your grant reaches" });
      return;
    }
    respondJson(response, 200, {
      order_id: sale.order_id,
      order_no: sale.order_no,
      customer_id: sale.customer_id,
      branch_id: sale.branch_id,
      sale_type: sale.sale_type,
      tender: sale.tender,
      status: sale.status,
      split_tender: sale.split_tender === true,
      subtotal_cents: sale.subtotal_cents,
      tax_cents: sale.tax_cents,
      total_cents: sale.total_cents,
      tax_reason_code: sale.tax_reason_code,
      invoice_id: sale.invoice_id,
      invoice_no: sale.invoice_no,
      payment_id: sale.payment_id,
      lines: sale.lines,
      tenders: sale.tenders,
    });
    return;
  }

  // Park the ticket: quantities and units only, no prices. The contract says
  // why: prices re-derive from the engine on resume, so a parked ticket never
  // rings at a stale price.
  if (request.method === "POST" && path === "/api/v1/pos/parked") {
    readBody(request, (parsed) => {
      const branchId = typeof parsed.branch_id === "string" ? parsed.branch_id : "";
      if (branchId !== callerBranch) {
        respondJson(response, 403, { error: "that branch is not one your grant reaches" });
        return;
      }
      const rawLines = Array.isArray(parsed.lines) ? parsed.lines : [];
      const lines = [];
      for (const line of rawLines) {
        const productId = typeof line?.product_id === "string" ? line.product_id : "";
        const qty = typeof line?.qty_entered === "number" ? line.qty_entered : 0;
        const uom = typeof line?.uom_entered === "string" ? line.uom_entered : "";
        if (productId === "" || qty <= 0 || uom === "") {
          respondJson(response, 400, { error: "a ticket parks a quantity and a unit for each line" });
          return;
        }
        lines.push({ product_id: productId, qty_entered: qty, uom_entered: uom });
      }
      if (lines.length === 0) {
        respondJson(response, 400, { error: "a ticket parks at least one line" });
        return;
      }
      const customerId = typeof parsed.customer_id === "string" ? parsed.customer_id : "";
      if (customerId !== "" && customerFor(customerId) === null) {
        respondJson(response, 404, { error: "no such customer account" });
        return;
      }
      const id = uuidFromCounter(9000 + parked.size + 1);
      const ticket = {
        id,
        ticket_no: nextDocNo("PARK"),
        branch_id: branchId,
        customer_id: customerId,
        sale_type: typeof parsed.sale_type === "string" ? parsed.sale_type : "COLLECT_NOW",
        notes: typeof parsed.notes === "string" ? parsed.notes : "",
        status: "PARKED",
        lines,
        created_at: new Date(nowMs()).toISOString(),
      };
      parked.set(id, ticket);
      respondJson(response, 201, ticket);
    });
    return;
  }

  // The parked list: fenced to the caller's own branch before any filter the
  // query asks for, so Julian's tickets never reach the Point Loma counter,
  // whichever branch the query names.
  if (request.method === "GET" && path === "/api/v1/pos/parked") {
    const items = [...parked.values()].filter(
      (ticket) => ticket.branch_id === callerBranch && ticket.status === "PARKED",
    );
    respondJson(response, 200, { items, limit: items.length });
    return;
  }

  const resumeMatch = path.match(/^\/api\/v1\/pos\/parked\/([^/]+)\/resume$/);
  if (request.method === "POST" && resumeMatch !== null) {
    const ticket = parked.get(resumeMatch[1]) ?? null;
    if (ticket === null) {
      respondJson(response, 404, { error: "no such parked ticket" });
      return;
    }
    if (ticket.branch_id !== callerBranch) {
      respondJson(response, 403, { error: "that parked ticket is not one your grant reaches" });
      return;
    }
    ticket.status = "RESUMED";
    respondJson(response, 200, ticket);
    return;
  }

  // Discard a parked ticket: the contract's acknowledgement shape
  // (PosParkedDiscard), fenced to the caller's own branch like the resume,
  // and out of the branch's parked list from then on.
  const discardMatch = path.match(/^\/api\/v1\/pos\/parked\/([^/]+)$/);
  if (request.method === "DELETE" && discardMatch !== null) {
    const ticket = parked.get(discardMatch[1]) ?? null;
    if (ticket === null) {
      respondJson(response, 404, { error: "no such parked ticket" });
      return;
    }
    if (ticket.branch_id !== callerBranch) {
      respondJson(response, 403, { error: "that parked ticket is not one your grant reaches" });
      return;
    }
    ticket.status = "DISCARDED";
    respondJson(response, 200, { status: "DISCARDED" });
    return;
  }

  // ── the ported surface's reads: the counter's configuration, roster and ──
  // ── catalogue, each in the contract's shape, empty or defaulted where   ──
  // ── the demo world simply does not curate.                              ──

  if (request.method === "GET" && path === "/api/v1/pos/policy") {
    const branchId = url.searchParams.get("branch_id") ?? "";
    if (branchId !== callerBranch) {
      respondJson(response, 403, { error: "that branch is not one your grant reaches" });
      return;
    }
    respondJson(response, 200, {
      branch_id: branchId,
      cod_enabled: true,
      notes: "The demo dealer's counter policy.",
      line_presentation: "LINES",
      quick_chips_enabled: true,
      workspace_split_pct: 55,
      workspace_arrangement: "LOOKUP_LEFT",
      lookup_presentation: "CARDS",
      // P3-10 (the comprehensive train review): the real demo box answers
      // MANAGER_UP here, so the stub does too. The margin glance below KEEPS
      // answering to every caller (faithful to today's core, which serves it
      // through the cost and margin configuration): the SURFACE's refusal is
      // the thing under test, not the glance's.
      margin_visibility: "MANAGER_UP",
      balance_visibility: "ALL_STAFF",
      // The cancelled special order's deposit disposition: the ruling's default.
      supplier_deposit_cancel: "CREDIT_BACK",
      quick_chip_set: ["Pavers", "Sand and base", "Walls"],
    });
    return;
  }

  if (request.method === "GET" && path === "/api/v1/pos/workspace") {
    respondJson(response, 200, workspaceRowFor(caller.userId));
    return;
  }
  if (request.method === "PUT" && path === "/api/v1/pos/workspace") {
    readBody(request, (parsed) => {
      const row = {
        split_pct: typeof parsed.split_pct === "number" ? parsed.split_pct : 55,
        arrangement: parsed.arrangement === "SALE_LEFT" ? "SALE_LEFT" : "LOOKUP_LEFT",
        lookup_collapsed: parsed.lookup_collapsed === true,
        sale_collapsed: parsed.sale_collapsed === true,
        lookup_presentation: parsed.lookup_presentation === "ROWS" ? "ROWS" : "CARDS",
        margin_hidden: parsed.margin_hidden === true,
        counter_layout: parsed.counter_layout === "PANELS" ? "PANELS" : "STEPPER",
      };
      workspaceRows.set(caller.userId, row);
      respondJson(response, 200, { ...row, source: "user" });
    });
    return;
  }
  if (request.method === "DELETE" && path === "/api/v1/pos/workspace") {
    // Reset IS a delete of the per-user row (the dashboard ruling).
    workspaceRows.delete(caller.userId);
    respondJson(response, 204, {});
    return;
  }

  if (request.method === "GET" && path === "/api/v1/pos/hotkeys") {
    // The demo dealer curates nothing: the shipped default is the answer, and
    // the client's fallback roster stands either way.
    respondJson(response, 200, { source: "default", keys: [] });
    return;
  }

  if (request.method === "GET" && path === "/api/v1/pos/discount-approvers") {
    const branchId = url.searchParams.get("branch_id") ?? "";
    if (branchId !== callerBranch) {
      respondJson(response, 403, { error: "that branch is not one your grant reaches" });
      return;
    }
    respondJson(response, 200, { items: DISCOUNT_APPROVERS });
    return;
  }

  if (request.method === "GET" && path === "/api/v1/pos/salespeople") {
    respondJson(response, 200, { items: SALESPEOPLE });
    return;
  }

  if (request.method === "GET" && path === "/api/v1/pos/quick-picks") {
    const branchId = url.searchParams.get("branch_id") ?? "";
    if (branchId !== callerBranch) {
      respondJson(response, 403, { error: "that branch is not one your grant reaches" });
      return;
    }
    const customerId = url.searchParams.get("customer_id") ?? "";
    const customer = customerFor(customerId);
    // The demo dealer curates a short hot list (the same three SKUs the chip
    // set names), grouped by the catalogue's category word and priced for the
    // customer on the sale through the same engine the counter prices read
    // answer from, so the filled quick-pick tiles and chips are exercised.
    // The contract is unchanged: PosQuickPicks is this shape already.
    const items = HOT_LIST_SKUS.map((sku) => {
      const product = PRODUCTS.find((p) => p.sku === sku);
      const resolved = product === undefined ? null : resolveLine(customerId, product.product_id, branchId);
      if (product === undefined || resolved === null) return null;
      return {
        product_id: product.product_id,
        sku: product.sku,
        name: product.name,
        uom: product.sold_by,
        has_image: false,
        category: product.category,
        subcategory: product.category,
        price: {
          product_id: product.product_id,
          price_uom: resolved.price_uom,
          retail_cents: resolved.retail_cents,
          customer_cents: resolved.customer_cents,
          ...(resolved.saving_cents > 0 ? { saving_cents: resolved.saving_cents } : {}),
          ...(resolved.basis !== undefined ? { basis: resolved.basis } : {}),
        },
      };
    }).filter((row) => row !== null);
    respondJson(response, 200, {
      source: "dealer",
      customer_type: customer === null ? "RETAIL" : customer.customer_type,
      items,
    });
    return;
  }

  if (request.method === "GET" && path === "/api/v1/products") {
    respondJson(response, 200, PRODUCTS.map(productRow));
    return;
  }

  if (request.method === "GET" && path === "/api/v1/products/scan") {
    const code = (url.searchParams.get("code") ?? "").trim().toLowerCase();
    const product = PRODUCTS.find(
      (p) => p.sku.toLowerCase() === code || p.aliases.some((alias) => alias.toLowerCase() === code),
    );
    if (product === undefined) {
      respondJson(response, 404, { error: "no product answers that code" });
      return;
    }
    const requestedBranch = url.searchParams.get("branch_id") ?? callerBranch;
    respondJson(response, 200, counterHit(product, requestedBranch));
    return;
  }

  if (request.method === "GET" && path === "/api/v1/pos/stock-glance") {
    const productId = url.searchParams.get("product_id") ?? "";
    const product = PRODUCTS.find((p) => p.product_id === productId);
    if (product === undefined) {
      respondJson(response, 404, { error: "no such product" });
      return;
    }
    respondJson(response, 200, {
      product_id: product.product_id,
      on_hand: { [POINT_LOMA_ID]: 120, [JULIAN_ID]: 80 },
      base_uom: product.base_uom,
      sell_uom: product.sold_by,
      sell_factor: 1,
      weight_kg_per_base: product.base_uom === "yd" ? 1450 : undefined,
    });
    return;
  }

  if (request.method === "GET" && path === "/api/v1/pos/addon-suggestions") {
    // The strip hides entirely on an empty answer: the demo world curates none.
    respondJson(response, 200, { suggestions: [] });
    return;
  }

  if (request.method === "POST" && path === "/api/v1/pricing/resolve") {
    readBody(request, (parsed) => {
      const branchId = typeof parsed.branch_id === "string" ? parsed.branch_id : "";
      if (branchId !== callerBranch) {
        respondJson(response, 403, { error: "that branch is not one your grant reaches" });
        return;
      }
      const customerId = typeof parsed.customer_id === "string" ? parsed.customer_id : "";
      const customer = customerFor(customerId);
      if (customer === null) {
        respondJson(response, 404, { error: "no such customer account" });
        return;
      }
      const answer = resolveBreakdown(
        customer.id,
        typeof parsed.product_id === "string" ? parsed.product_id : "",
        branchId,
        parsed.qty_entered,
        parsed.uom_entered,
      );
      respondJson(response, answer.status, answer.body);
    });
    return;
  }

  const signatureRoute =
    request.method === "POST" &&
    (path === "/api/v1/pos/signatures" || path === "/api/v1/pos/signatures/typed");
  if (signatureRoute) {
    readBody(request, () => {
      respondJson(response, 201, { signature_ref: uuidFromCounter(60000 + docCounter) });
    });
    return;
  }

  if (request.method === "POST" && path === "/api/v1/pos/assess-relief") {
    readBody(request, (parsed) => {
      // The demo world carries no relief profile: the engine's answer is the
      // standard rate on whatever subtotal the ask named, collected in full,
      // no evidence required.
      const subtotal = typeof parsed.subtotal_cents === "number" ? parsed.subtotal_cents : 0;
      const fullHst = Math.round((subtotal * HST_RATE_BPS) / 10000);
      respondJson(response, 200, {
        purchaser_type: "OTHER",
        masked_identifier: "",
        outcome: "STANDARD",
        reason_code: "NOT_ELIGIBLE",
        rate_bps: HST_RATE_BPS,
        full_hst_cents: fullHst,
        on_credit_cents: 0,
        net_collected_cents: fullHst,
        automated: true,
        requires_evidence: false,
        evidence_satisfied: false,
      });
    });
    return;
  }

  const receiptMatch = path.match(/^\/api\/v1\/pos\/sales\/([^/]+)\/receipt$/);
  if (request.method === "GET" && receiptMatch !== null) {
    // The route's one optional format value, the core's own refusal shape: an
    // unknown value is a 400 naming it, never a silent fallthrough.
    const format = url.searchParams.get("format") ?? "";
    if (format !== "" && format !== "roll" && format !== "page") {
      respondJson(response, 400, { error: `unknown receipt format "${format}", want roll or page` });
      return;
    }
    const sale = sales.get(receiptMatch[1]) ?? null;
    if (sale === null) {
      respondJson(response, 404, { error: "no such sale" });
      return;
    }
    if (sale.branch_id !== callerBranch) {
      respondJson(response, 403, { error: "that sale is not one your grant reaches" });
      return;
    }
    // The reprint marker is a count: the fetch serves the ordinal of the
    // prints already recorded, then records its own, the core's own shape
    // (the count lives with the sale, so a later fetch reprints as #N).
    const priorPrints = sale.prints ?? 0;
    sale.prints = priorPrints + 1;
    const view = receiptView(sale);
    const document =
      format === "page" ? pageReceiptDocument(view, priorPrints) : rollReceiptDocument(view, priorPrints);
    response.writeHead(200, { "content-type": "text/html; charset=utf-8" });
    response.end(document);
    return;
  }

  respondJson(response, 404, { error: "not found" });
});

// The ring's response body, in the contract's PosSale shape: the committed
// totals and tenders, the change due on a cash ring, and no line rows (the
// operator-reopen read carries those).
// ---------------------------------------------------------------------------
// The receipt: the demo mirror of the core's printed documents. The core's
// printing package renders the real paper (the DIB-410 lineage and the
// full-page format beside it); this mirror keeps the app's suites on the same
// words and the same layout against the stub, built from the same committed
// sale shapes this stub serves, the demo branch's invented values only. When
// the core's template changes its words, this mirror changes with it.
// ---------------------------------------------------------------------------

// The letterhead's org line: the invented trading name the demo database's
// own settings row carries (the populate's convergence).
const RECEIPT_ORG_NAME = "Gable Landscape Supply";

// The fulfilment words the receipt prints, the core's own map keyed on the
// contract's sale types: the paper speaks the counter's words, never the
// wire's constants, and an unknown value passes through verbatim.
function fulfilmentWords(saleType) {
  switch (saleType) {
    case "COLLECT_NOW":
      return "Collected at the counter";
    case "COLLECT_LATER":
      return "For later collection";
    case "DELIVERED":
      return "Delivery";
    case "COD":
      return "Invoice issued, payment before fulfilment";
    default:
      return saleType;
  }
}

// The receipt's one money rendering: "$1,234.56" from integer cents,
// thousands separated, never a float.
function receiptMoney(cents) {
  const negative = cents < 0;
  const magnitude = Math.abs(cents);
  const whole = String(Math.floor(magnitude / 100)).replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  const pennies = String(magnitude % 100).padStart(2, "0");
  return `${negative ? "-" : ""}$${whole}.${pennies}`;
}

// The tax line's words, the rate from the demo world's own setting, never a
// literal in the layout.
function receiptTaxLabel() {
  const whole = HST_RATE_BPS / 100;
  return `HST (${whole}%)`;
}

// "YYYY-MM-DD HH:MM", the stamp shape the documents print, from an ISO
// moment.
function receiptStamp(iso) {
  return `${iso.slice(0, 10)} ${iso.slice(11, 16)}`;
}

// The one view both receipt documents render: the demo branch's letterhead,
// the lines with their SKUs, the totals, the tenders with their references,
// and the cash facts the committed sale itself states (the change due, the
// deposit taken at ring, the balance that remains). The ON ACCOUNT row is
// the core's own synthetic tender: a receipt that lists no tender at all
// reads like an unpaid mistake.
function receiptView(sale) {
  const branch = BRANCHES.find((candidate) => candidate.id === sale.branch_id) ?? BRANCHES[0];
  const customer = CUSTOMERS.find((candidate) => candidate.id === sale.customer_id) ?? null;
  const operator = Object.values(USERS).find((candidate) => candidate.user_id === sale.created_by) ?? null;
  const address = [branch.address.line1, branch.address.city, branch.address.province, branch.address.postal]
    .filter(Boolean)
    .join(", ");
  const tenders = sale.tenders.map((leg) => ({
    label: leg.method,
    ref: typeof leg.payment_no === "string" ? leg.payment_no : "",
    amountCents: leg.amount_cents,
    simulated:
      leg.method === "CARD" && typeof leg.gateway_ref === "string" && leg.gateway_ref.startsWith("rp_sim_"),
  }));
  if (tenders.length === 0) {
    tenders.push({ label: "ON ACCOUNT", ref: sale.invoice_no, amountCents: sale.total_cents, simulated: false });
  }
  const balanceCents = sale.balance_cents ?? 0;
  // The deposit row's gate, the core's own: the word names the plain
  // unpaid-at-ring ring's deposit (ACCOUNT or COD, no split: a split settles
  // the sale itself), and nowhere else.
  const depositApplies =
    balanceCents > 0 &&
    sale.split_tender !== true &&
    (sale.tender === "ACCOUNT" || sale.sale_type === "COD");
  return {
    orgName: RECEIPT_ORG_NAME,
    branchName: branch.name,
    branchCode: branch.code,
    branchAddress: address,
    orderNo: sale.order_no,
    invoiceNo: sale.invoice_no,
    soldAt: receiptStamp(sale.created_at),
    customerName: customer?.name ?? "",
    customerAccountNo: customer?.account_no ?? "",
    // The customer row is required on every sale (row 666): the walk-in speaks
    // the counter's own word, the core's own fallback.
    customerLine: customer?.name
      ? `Customer: ${customer.name}${customer.account_no ? ` (${customer.account_no})` : ""}`
      : "Customer: Walk-in",
    fulfilment: fulfilmentWords(sale.sale_type),
    servedBy: operator?.display_name ?? "",
    lines: sale.lines.map((line) => {
      const product = PRODUCTS.find((candidate) => candidate.product_id === line.product_id) ?? null;
      return {
        sku: product?.sku ?? "",
        description: line.description,
        qty: String(line.qty_entered),
        uom: line.uom_entered,
        unitPriceCents: line.unit_price_cents,
        priceUom: line.price_uom,
        lineTotalCents: line.line_total_cents,
      };
    }),
    subtotalCents: sale.subtotal_cents,
    taxLabel: receiptTaxLabel(),
    taxCents: sale.tax_cents,
    totalCents: sale.total_cents,
    tenders,
    changeDueCents: sale.change_due_cents ?? 0,
    depositCents: depositApplies ? sale.deposit_cents ?? 0 : 0,
    balanceCents,
    printedAt: receiptStamp(new Date(nowMs()).toISOString()),
  };
}

// The 80 mm counter receipt, the demo mirror of the core's roll document:
// the same blocks in the same order, the print CSS sized for the roll, the
// Print button that is not printed.
function rollReceiptDocument(v, priorPrints) {
  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Receipt ${v.orderNo}</title>
<style>
  @page { margin: 10mm; }
  * { box-sizing: border-box; }
  body {
    font-family: ui-monospace, "SFMono-Regular", "Menlo", "Consolas", monospace;
    font-size: 12px; line-height: 1.45; color: #000; background: #fff;
    max-width: 80mm; margin: 0 auto; padding: 8px;
  }
  h1 { font-size: 14px; margin: 0 0 2px; letter-spacing: .02em; }
  .muted { color: #444; }
  .center { text-align: center; }
  .right { text-align: right; }
  .rule { border-top: 1px dashed #000; margin: 8px 0; }
  .order-no { font-size: 20px; font-weight: 700; letter-spacing: .04em; margin: 6px 0 2px; }
  table { width: 100%; border-collapse: collapse; }
  td, th { padding: 1px 0; vertical-align: top; }
  th { text-align: left; font-weight: 700; border-bottom: 1px solid #000; }
  .desc { word-break: break-word; }
  .totals td { padding: 1px 0; }
  .grand { font-weight: 700; font-size: 14px; border-top: 1px solid #000; }
  .reprint {
    border: 2px solid #000; padding: 4px; margin: 6px 0; text-align: center;
    font-weight: 700; letter-spacing: .12em;
  }
  .foot { margin-top: 10px; font-size: 10px; }
  .mark { margin-top: 4px; font-size: 9px; letter-spacing: .06em; color: #555; }
  @media print { .noprint { display: none !important; } }
</style>
</head>
<body>
  <header class="center">
    <h1>${v.orgName}</h1>
    <div>${v.branchName} (${v.branchCode})</div>
    <div class="muted">${v.branchAddress}</div>
  </header>

  <div class="rule"></div>

  ${priorPrints > 0 ? `<div class="reprint">REPRINT #${priorPrints}</div>` : ""}

  <div class="center">
    <div class="order-no">${v.orderNo}</div>
    ${v.invoiceNo ? `<div class="muted">Invoice ${v.invoiceNo}</div>` : ""}
    <div class="muted">${v.soldAt}</div>
  </div>

  <div class="rule"></div>

  <div>
    <div>${v.customerLine}</div>
    ${v.fulfilment ? `<div class="muted">${v.fulfilment}</div>` : ""}
    ${v.servedBy ? `<div class="muted">Served by ${v.servedBy}</div>` : ""}
  </div>

  <div class="rule"></div>

  <table>
    <thead>
      <tr><th>Item</th><th class="right">Qty</th><th class="right">Unit</th><th class="right">Total</th></tr>
    </thead>
    <tbody>
      ${v.lines
        .map(
          (line) => `<tr class="line">
        <td class="desc" colspan="4">${line.sku ? `${line.sku} · ` : ""}${line.description}</td>
      </tr>
      <tr class="line-money">
        <td></td>
        <td class="right">${line.qty} ${line.uom}</td>
        <td class="right">${receiptMoney(line.unitPriceCents)}${line.priceUom ? `/${line.priceUom}` : ""}</td>
        <td class="right">${receiptMoney(line.lineTotalCents)}</td>
      </tr>`,
        )
        .join("\n")}
    </tbody>
  </table>

  <div class="rule"></div>

  <table class="totals">
    <tr><td>Subtotal</td><td class="right">${receiptMoney(v.subtotalCents)}</td></tr>
    <tr class="tax"><td>${v.taxLabel}</td><td class="right">${receiptMoney(v.taxCents)}</td></tr>
    <tr class="grand"><td>Total</td><td class="right">${receiptMoney(v.totalCents)}</td></tr>
  </table>

  <div class="rule"></div>

  <table class="tenders">
    ${v.tenders
      .map(
        (tender) =>
          `<tr class="tender"><td>${tender.label}${tender.simulated ? ` <span class="muted">Simulated card: nothing reached a processor.</span>` : ""}${tender.ref ? ` <span class="muted">${tender.ref}</span>` : ""}</td><td class="right">${receiptMoney(tender.amountCents)}</td></tr>`,
      )
      .join("\n")}
    ${v.changeDueCents > 0 ? `<tr class="change-due"><td>Change due</td><td class="right">${receiptMoney(v.changeDueCents)}</td></tr>` : ""}
    ${v.depositCents > 0 ? `<tr class="deposit"><td>Deposit</td><td class="right">${receiptMoney(v.depositCents)}</td></tr>` : ""}
    ${v.balanceCents > 0 ? `<tr class="balance"><td>Balance due</td><td class="right">${receiptMoney(v.balanceCents)}</td></tr>` : ""}
  </table>

  <div class="foot center muted">
    Printed ${v.printedAt}
    <div>Keep this receipt, returns require it.</div>
    <div class="mark">HH Yard · a Hardscape House product</div>
  </div>

  <div class="noprint center" style="margin-top:12px">
    <button onclick="window.print()">Print</button>
  </div>
</body>
</html>`;
}

// The full-page receipt (format=page), the demo mirror of the core's second
// document: the roll's blocks and words laid out as a letter and A4 sheet,
// the print CSS sized for paper instead of the thermal roll.
function pageReceiptDocument(v, priorPrints) {
  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Receipt ${v.orderNo}</title>
<style>
  @page { margin: 18mm; }
  * { box-sizing: border-box; }
  body {
    font-family: ui-monospace, "SFMono-Regular", "Menlo", "Consolas", monospace;
    font-size: 13px; line-height: 1.5; color: #000; background: #fff;
    max-width: 180mm; margin: 0 auto; padding: 8px 0;
  }
  h1 { font-size: 20px; margin: 0 0 4px; letter-spacing: .02em; }
  .muted { color: #444; }
  .center { text-align: center; }
  .right { text-align: right; }
  .rule { border-top: 1px solid #000; margin: 14px 0; }
  .order-no { font-size: 24px; font-weight: 700; letter-spacing: .04em; margin: 10px 0 4px; }
  table { width: 100%; border-collapse: collapse; }
  td, th { padding: 4px 0; vertical-align: top; }
  th { text-align: left; font-weight: 700; border-bottom: 1px solid #000; }
  .desc { word-break: break-word; }
  .totals td { padding: 3px 0; }
  .grand { font-weight: 700; font-size: 15px; border-top: 1px solid #000; }
  .reprint {
    border: 2px solid #000; padding: 6px; margin: 10px 0; text-align: center;
    font-weight: 700; letter-spacing: .12em;
  }
  .foot { margin-top: 16px; font-size: 11px; }
  .mark { margin-top: 6px; font-size: 10px; letter-spacing: .06em; color: #555; }
  @media print { .noprint { display: none !important; } }
</style>
</head>
<body>
  <header class="center">
    <h1>${v.orgName}</h1>
    <div>${v.branchName} (${v.branchCode})</div>
    <div class="muted">${v.branchAddress}</div>
  </header>

  <div class="rule"></div>

  ${priorPrints > 0 ? `<div class="reprint">REPRINT #${priorPrints}</div>` : ""}

  <div class="center">
    <div class="order-no">${v.orderNo}</div>
    ${v.invoiceNo ? `<div class="muted">Invoice ${v.invoiceNo}</div>` : ""}
    <div class="muted">${v.soldAt}</div>
  </div>

  <div class="rule"></div>

  <div>
    <div>${v.customerLine}</div>
    ${v.fulfilment ? `<div class="muted">${v.fulfilment}</div>` : ""}
    ${v.servedBy ? `<div class="muted">Served by ${v.servedBy}</div>` : ""}
  </div>

  <div class="rule"></div>

  <table>
    <thead>
      <tr><th>Item</th><th class="right">Qty</th><th class="right">Unit</th><th class="right">Total</th></tr>
    </thead>
    <tbody>
      ${v.lines
        .map(
          (line) => `<tr class="line">
        <td class="desc" colspan="4">${line.sku ? `${line.sku} · ` : ""}${line.description}</td>
      </tr>
      <tr class="line-money">
        <td></td>
        <td class="right">${line.qty} ${line.uom}</td>
        <td class="right">${receiptMoney(line.unitPriceCents)}${line.priceUom ? `/${line.priceUom}` : ""}</td>
        <td class="right">${receiptMoney(line.lineTotalCents)}</td>
      </tr>`,
        )
        .join("\n")}
    </tbody>
  </table>

  <div class="rule"></div>

  <table class="totals">
    <tr><td>Subtotal</td><td class="right">${receiptMoney(v.subtotalCents)}</td></tr>
    <tr class="tax"><td>${v.taxLabel}</td><td class="right">${receiptMoney(v.taxCents)}</td></tr>
    <tr class="grand"><td>Total</td><td class="right">${receiptMoney(v.totalCents)}</td></tr>
  </table>

  <div class="rule"></div>

  <table class="tenders">
    ${v.tenders
      .map(
        (tender) =>
          `<tr class="tender"><td>${tender.label}${tender.simulated ? ` <span class="muted">Simulated card: nothing reached a processor.</span>` : ""}${tender.ref ? ` <span class="muted">${tender.ref}</span>` : ""}</td><td class="right">${receiptMoney(tender.amountCents)}</td></tr>`,
      )
      .join("\n")}
    ${v.changeDueCents > 0 ? `<tr class="change-due"><td>Change due</td><td class="right">${receiptMoney(v.changeDueCents)}</td></tr>` : ""}
    ${v.depositCents > 0 ? `<tr class="deposit"><td>Deposit</td><td class="right">${receiptMoney(v.depositCents)}</td></tr>` : ""}
    ${v.balanceCents > 0 ? `<tr class="balance"><td>Balance due</td><td class="right">${receiptMoney(v.balanceCents)}</td></tr>` : ""}
  </table>

  <div class="foot center muted">
    Printed ${v.printedAt}
    <div>Keep this receipt, returns require it.</div>
    <div class="mark">HH Yard · a Hardscape House product</div>
  </div>

  <div class="noprint center" style="margin-top:16px">
    <button onclick="window.print()">Print</button>
  </div>
</body>
</html>`;
}

function payHoldBody(order) {
  const paid = order.payments.reduce((sum, p) => sum + p.amount_cents, 0);
  const owed = order.order_total_cents - paid;
  return {
    held_from_picking: order.held && owed > 0,
    ...(order.held && owed > 0 ? { words: PAY_HOLD_WORDS } : {}),
    ...(paid > 0 && owed > 0 ? { in_process_words: IN_PROCESS_WORDS } : {}),
    order_total_cents: order.order_total_cents,
    paid_cents: paid,
    owed_cents: owed,
  };
}

function owingBody(order) {
  const hold = payHoldBody(order);
  return {
    order_id: order.order_id,
    order_no: order.order_no,
    customer_id: order.customer_id,
    customer_name: order.customer_name,
    ...(order.job ? { job: order.job } : {}),
    fulfilment: order.fulfilment,
    order_total_cents: order.order_total_cents,
    paid_cents: hold.paid_cents,
    still_owed_cents: hold.owed_cents,
    held_from_picking: hold.held_from_picking,
    ...(hold.words ? { hold_words: hold.words } : {}),
    payments: order.payments.map((pay) => ({ ...pay })),
  };
}

function saleResponseBody(sale, replayed) {
  return {
    order_id: sale.order_id,
    order_no: sale.order_no,
    invoice_id: sale.invoice_id,
    invoice_no: sale.invoice_no,
    payment_id: sale.payment_id,
    gateway_ref: sale.gateway_ref,
    tender: sale.tender,
    sale_type: sale.sale_type,
    subtotal_cents: sale.subtotal_cents,
    tax_cents: sale.tax_cents,
    total_cents: sale.total_cents,
    tax_reason_code: sale.tax_reason_code,
    status: sale.status,
    replayed,
    split_tender: sale.split_tender === true,
    cash_tendered_cents: sale.cash_tendered_cents,
    change_due_cents: sale.change_due_cents,
    deposit_cents: sale.deposit_cents,
    supplier_deposit_cents: sale.supplier_deposit_cents,
    supplier_deposit_invoice_id: sale.supplier_deposit_invoice_id,
    supplier_deposit_invoice_no: sale.supplier_deposit_invoice_no,
    tenders: sale.tenders,
    discount_cents: sale.discount_cents,
    discounts: sale.discounts,
  };
}

server.on("error", (error) => {
  console.error(`demo stub could not listen on 127.0.0.1:${port}: ${error instanceof Error ? error.message : String(error)}`);
  process.exit(1);
});

server.keepAliveTimeout = 120 * 1000;
server.headersTimeout = 125 * 1000;
server.listen(port, "127.0.0.1", () => {
  console.log(`demo stub on http://127.0.0.1:${port}`);
});
