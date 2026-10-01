<<<<<<< HEAD
// The local demo stub: the demo routes and the till's return paths in the
// contract's shapes, for the dev server's /api proxy and the Playwright
// harness. This is a development and test double only: the real routes are
// the core's (the mint is the demo build's, the returns rules are RT-B's),
// and nothing in the app imports this file. When the real routes land, the
// app and this suite keep working against them unchanged, because both speak
// only the contract's shapes:
//   GET  /api/v1/demo/flows            -> DemoFlowList
//   POST /api/v1/demo/session          -> DemoSessionResponse
//   GET  /api/v1/pos/returns/lookup    -> PosReturnLookup
//   POST /api/v1/pos/returns           -> PosReturn (409 PosRefundApprovalRequired)
//   GET  /api/v1/till/sessions/open    -> TillOpenLookup
//   GET  /api/v1/till/sessions/{id}/zreport -> TillZReport
//   GET  /api/v1/payments/config       -> PaymentConfigStatus
// The demo routes are public in the contract (security: []), so the stub
// checks no credential there; the phrase gate in front of the real demo box
// is Caddy's (W5's image), not the routes'. The staff routes the returns desk
// calls do carry the caller's bearer, and the stub reads the account back out
// of the token it minted itself, because the refusal rules it stands in for
// (the approval above the amount) are answered per role: what the real core
// resolves from the verified token, this stub reads from its own mint.
=======
// The local demo stub: the demo routes and the counter's read routes in the
// contract's shapes, for the dev server's /api proxy and the Playwright
// harness. This is a development and test double only: the real routes are
// the core's (the mint is the demo build's, the pricing is the engine's), and
// nothing in the app imports this file. When the real routes land, the app
// and this suite keep working against them unchanged, because both speak only
// the contract's shapes:
//   GET  /api/v1/demo/flows               -> DemoFlowList
//   POST /api/v1/demo/session             -> DemoSessionResponse
//   GET  /api/v1/branches                 -> Branch[]
//   GET  /api/v1/till/sessions/open       -> TillOpenLookup
//   POST /api/v1/till/sessions            -> TillOpenResult
//   GET  /api/v1/till/sessions/{id}       -> TillSessionDetail
//   GET  /api/v1/products/counter-search  -> ProductCounterSearchResults
//   GET  /api/v1/customers/search         -> CustomerSearchResult[]
//   GET  /api/v1/pos/account-standing     -> PosAccountStanding
//   POST /api/v1/pos/counter-prices       -> PosCounterPrices
//   POST /api/v1/pos/margin-glance        -> PosMarginGlance
//   GET  /api/v1/payments/config          -> PaymentConfigStatus
//   POST /api/v1/pos/sales                -> PosSale
//   GET  /api/v1/pos/sales/{id}           -> PosSaleView
//   POST /api/v1/pos/parked               -> PosParkedTicket
//   GET  /api/v1/pos/parked               -> PosParkedTicketPage
//   POST /api/v1/pos/parked/{id}/resume   -> PosParkedTicket
// The demo routes are public in the contract (security: []), so they check no
// credential; the phrase gate in front of the real demo box is Caddy's (W5's
// image), not the routes'. The staff routes are not public: this stub reads
// the minted bearer the same way the core's middleware does (the token's own
// sub claim) and refuses a call without one, so the app's header plumbing is
// exercised exactly as it would be against the real server.
>>>>>>> origin/till/l5-w7
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
import { createServer } from "node:http";

const port = Number(process.env.CT_STUB_PORT ?? 4821);

// The closed flow table: the two logins this release shows, with the labels
// the cards carry and the role each minted account holds (the mint's role
// label comes from this table, never from the roles table).
const FLOWS = [
  { flow: "sales", role_code: "inside_sales", label: "Sales" },
  { flow: "sales-manager", role_code: "sales_manager", label: "Sales manager" },
];

// The minted accounts, one per flow, shaped field for field like the staff
// login user block (core/api/openapi.yaml, IdentityUser). The accounts are
// demo labels on reserved domains, not people. Each account's grant reaches
// its own branch only (the demo world's Trenton), which is the branch fence
// the core's middleware holds: the stub reads the same grant from the token's
// account, so one branch never answers for another.
const USERS = {
  sales: {
    user_id: "demo-account-sales",
    email: "sales@demo.invalid",
    display_name: "Sales (demo)",
    role_code: "inside_sales",
    role_name: "Inside Sales",
    tier: 2,
    scope: "BRANCH",
  },
  "sales-manager": {
    user_id: "demo-account-sales-manager",
    email: "sales-manager@demo.invalid",
    display_name: "Sales manager (demo)",
    role_code: "sales_manager",
    role_name: "Sales Manager",
    tier: 3,
    scope: "BRANCH",
  },
};

// ---------------------------------------------------------------------------
// The invented world: the first dealer's demo box. Two branches, a counter
// catalogue, three accounts. Every identity is an invented label on a reserved
// domain or a doc number, never a real one, and the world's history is dated
// from nothing: the numbers below are counters, not calendar facts.
// ---------------------------------------------------------------------------

const TRENTON_ID = "6f1c2b3a-0000-4000-8000-000000000001";
const KINGSTON_ID = "6f1c2b3a-0000-4000-8000-000000000002";

const BRANCHES = [
  {
    id: TRENTON_ID,
    code: "TRE",
    name: "Trenton",
    address: { line1: "12 Riverside Yard Road", city: "Trenton", province: "Ontario", postal: "K8V 0A1", country: "Canada" },
    is_default: true,
    is_system_default: true,
    is_active: true,
  },
  {
    id: KINGSTON_ID,
    code: "KGN",
    name: "Kingston",
    address: { line1: "8 Cataquoui Yard Road", city: "Kingston", province: "Ontario", postal: "K7M 0A2", country: "Canada" },
    is_default: false,
    is_system_default: false,
    is_active: true,
  },
];

// Each account's grant reaches one branch: the demo logins work the Trenton
// counter. Kingston exists so a fence is observable, never reachable.
const ACCOUNT_BRANCH = { "demo-account-sales": TRENTON_ID, "demo-account-sales-manager": TRENTON_ID };

// The counter catalogue. list_cents is the retail anchor; branch_cost_cents is
// the weighted cost the margin glance reads, per branch. The edge restraint
// carries no cost at Trenton on purpose: a glance line with no cost stays
// byte-absent in the real answer, and the till shows the explicit placeholder
// there, never a fake margin. sold_by is the unit the counter rings in.
const PRODUCTS = [
  {
    product_id: "p-holland-60",
    sku: "HPV-60",
    name: "Holland Stone 60 mm",
    description: "The Holland paver, 60 mm, per square foot",
    aliases: ["holland paver", "holland stone"],
    base_uom: "sf",
    sold_by: "sf",
    list_cents: 400,
    branch_cost_cents: { [TRENTON_ID]: 276, [KINGSTON_ID]: 268 },
  },
  {
    product_id: "p-polymer-sand",
    sku: "PSN-100",
    name: "Polymeric Locking Sand",
    description: "Jointing sand for paver courses",
    aliases: ["polymer sand", "jointing sand"],
    base_uom: "bag",
    sold_by: "bag",
    list_cents: 1400,
    branch_cost_cents: { [TRENTON_ID]: 980, [KINGSTON_ID]: 1010 },
  },
  {
    product_id: "p-edge-8",
    sku: "EDG-08",
    name: "Edge Restraint 8 ft",
    description: "Flexible paver edge restraint, eight foot strip",
    aliases: ["edge restraint", " edging"],
    base_uom: "pc",
    sold_by: "pc",
    list_cents: 3200,
    branch_cost_cents: { [KINGSTON_ID]: 2450 },
  },
  {
    product_id: "p-wall-std",
    sku: "WAT-STD",
    name: "Retaining Wall Stone, Standard",
    description: "Standard wall unit, per piece",
    aliases: ["wall stone", "retaining wall"],
    base_uom: "pc",
    sold_by: "pc",
    list_cents: 5200,
    branch_cost_cents: { [TRENTON_ID]: 3900, [KINGSTON_ID]: 3860 },
  },
  {
    product_id: "p-clear-stone",
    sku: "GRA-CL",
    name: "Clear Stone",
    description: "Clear gravel base, per cubic yard",
    aliases: ["gravel", "clear gravel"],
    base_uom: "yd",
    sold_by: "yd",
    list_cents: 3800,
    branch_cost_cents: { [TRENTON_ID]: 2900, [KINGSTON_ID]: 2950 },
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
];

const WALK_IN = {
  id: "",
  account_no: "WALK-IN",
  name: "Walk-in",
  customer_type: "RETAIL",
  percent_off: 0,
  on_hold: false,
};

const HST_RATE_BPS = 1300; // the Ontario rate, as basis points

// The dealer's house zone: the same constant the core itself compiles. The
// ring's requested date must be THIS zone's calendar day, which is the
// invariant "daily means the dealer's day" held at the counter: a till that
// asks in the browser's UTC day is refused, and the two days differ every
// evening house time.
const HOUSE_ZONE = "America/Toronto";

function houseDay(now = new Date()) {
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

// One open drawer per branch, station MAIN: the counter's shared drawer.
const drawers = new Map(); // branch_id -> session row
// Committed sales, by order id, in ring order.
const sales = new Map();
const salesByKey = new Map(); // idempotency_key -> order_id
// Parked tickets, by id. Kingston's pre-seeded ticket proves the fence: a
// Trenton login lists parked tickets for its own branch and never this one.
const parked = new Map();

function seedParkedKingstonTicket() {
  const id = uuidFromCounter(9001);
  parked.set(id, {
    id,
    ticket_no: "PARK-9001",
    branch_id: KINGSTON_ID,
    customer_id: "c-maple-ridge",
    sale_type: "COLLECT_NOW",
    notes: "Parked at the Kingston counter",
    status: "PARKED",
    lines: [{ product_id: "p-holland-60", qty_entered: 40, uom_entered: "sf" }],
    created_at: "2026-01-01T00:00:00Z",
  });
}
seedParkedKingstonTicket();

// ---------------------------------------------------------------------------
// The pricing stand-in: the same waterfall-shaped table the ring and the two
// counter reads answer from, so a line's screen figure and the committed
// sale's figure cannot drift. Integer cents throughout.
// ---------------------------------------------------------------------------

function customerFor(customerId) {
  if (typeof customerId !== "string" || customerId === "") return WALK_IN;
  return CUSTOMERS.find((c) => c.id === customerId) ?? null;
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
// unchanged and only the words move.
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

<<<<<<< HEAD
// The stub's till world: the demo branch's sales, the drawer, and the returns
// already taken. The sale numbers are the counter's own spellings, every id
// is uuid shaped as the contract's formats name them, the money is integer
// cents straight from the (stubbed) core, and tax is zero in this world so
// every figure a test reads is the line's own total. A sale numbered outside
// this table (another branch's sale among them: the real lookup is branch
// fenced, and a fenced sale is an invisible one) answers not found.
const BRANCH_ID = "5d1b3f6e-0000-4000-9000-000000000001";

// Deterministic uuid shaped ids, one per row of this invented world: the
// digits are the world's own numbering, the shape is the contract's.
const uuid = (n) => `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`;

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
        description: "Holland paver, bevelled, each",
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
};

// The drawer's world: one open session at the demo branch, with its opening
// float and the cash that has moved through it. The Z report's expected cash
// is read off these counters, so a cash return moving the cash out counter is
// the same movement the Z report shows.
const DRAWER = {
  session_id: uuid(90001),
  branch_id: BRANCH_ID,
  opening_float_cents: 20000,
  cash_in_cents: 0,
  cash_out_cents: 0,
};

// The returns committed in this stub's life, keyed by the request's
// idempotency key (a replay answers the stored return again and moves the
// drawer nothing), plus the returned quantity netted per line for the
// lookup's still-returnable counts.
const RETURNS_BY_KEY = new Map();
const RETURNED_QTY = new Map();

// What the harness's tests read back (the peek route below): every api
// request's path and whether it carried the approver's header, the returns
// posts' idempotency keys in send order, the last return body verbatim, and
// who each committed return names as its clerk and its approver. Test
// scaffolding only, on a route no contract path shares.
const SIGHTINGS = [];
const KEYS_SEEN = [];
let LAST_RETURN_BODY = null;
const RETURN_META = new Map();

// The approval above which a refund needs a manager, and the roles that hold
// the approval, as the demo dealer's configuration sets them (D49's 250.00
// and its two approver roles). The real values are RT-B's configuration read;
// this stub pins the demo's.
const APPROVAL_AMOUNT_CENTS = 25000;
const APPROVER_ROLES = ["sales_manager", "branch_manager"];

let RETURN_SEQ = 0;

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
    if (typeof exp !== "number" || !Number.isFinite(exp) || exp * 1000 <= Date.now()) {
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

// What the return's own rails refuse before any money moves, in the
// contract's envelope. The approval refusal names itself in the error string
// and carries the requirement under the approval key: the amount and the
// approver roles, present exactly on that refusal (the contract's own words),
// so the desk can name both. A verified approver's credential turns the same
// return into the approver's decision, committed as one.
function returnsRefusal(caller, body, approver) {
  if (body === null || typeof body !== "object") {
    return { status: 400, body: { error: "the return's body is not readable" } };
  }
  const key = body.idempotency_key;
  if (typeof key !== "string" || key === "") {
    return { status: 400, body: { error: "the return carries no idempotency key" } };
  }
  const sale = saleFromInput(body);
  if (sale === undefined) {
    return { status: 404, body: { error: "no sale with that number answers at this counter" } };
  }
  if (!Array.isArray(body.lines) || body.lines.length === 0) {
    return { status: 400, body: { error: "the return carries no lines" } };
  }
  if (!["CASH", "CARD", "ACCOUNT_CREDIT"].includes(body.refund_method)) {
    return { status: 400, body: { error: "the refund method is not one of the rails" } };
  }
  for (const line of body.lines) {
    const saleLine = sale.lines.find((row) => row.order_line_id === line.order_line_id);
    if (saleLine === undefined) {
      return { status: 400, body: { error: "the return names a line the sale does not carry" } };
    }
    const returnable = saleLine.qty_sold - (RETURNED_QTY.get(saleLine.order_line_id) ?? 0);
    if (!Number.isInteger(line.qty_base) || line.qty_base <= 0 || line.qty_base > returnable) {
      return { status: 409, body: { error: "the return asks for more than the line still has to return" } };
    }
  }
  if (body.refund_method === "CARD" && !sale.card_refundable) {
    return { status: 409, body: { error: "the sale was not paid by card, so there is no capture to reverse" } };
  }
  if (body.refund_method === "CASH" && body.till_session_id !== DRAWER.session_id) {
    return { status: 409, body: { error: "the drawer named is not the open one" } };
  }
  const totalCents = returnTotal(sale, body.lines);
  if (totalCents > APPROVAL_AMOUNT_CENTS && !APPROVER_ROLES.includes(caller.account.role_code) && !approver.present) {
    return {
      status: 409,
      body: {
        error: "refund_approval_required",
        approval: { amount_cents: APPROVAL_AMOUNT_CENTS, approvers: APPROVER_ROLES },
      },
    };
  }
  return null;
}

// The refund's total: the returned lines' own totals by their quantities, in
// a world whose tax is zero. Stub-side arithmetic, standing in the core's
// shoes; the app computes nothing.
function returnTotal(sale, lines) {
  let total = 0;
  for (const line of lines) {
    const saleLine = sale.lines.find((row) => row.order_line_id === line.order_line_id);
    total += saleLine.unit_price_cents * line.qty_base;
  }
  return total;
}

function saleFromInput(body) {
  if (typeof body.order_no === "string" && SALES[body.order_no] !== undefined) return SALES[body.order_no];
  if (typeof body.order_id === "string") {
    return Object.values(SALES).find((row) => row.order_id === body.order_id);
  }
  return undefined;
}

// The commit, as the core's would be: the refund's total is the returned
// lines' own totals (this world carries no tax), the cash rail moves the
// drawer's cash out counter, and the idempotency key makes a replay answer
// the stored return without a second movement. The approver, verified by the
// caller of this function, is recorded beside the clerk for the harness's
// peek route: the response body's shape carries neither, by the contract.
function commitReturn(caller, body, approver) {
  const key = body.idempotency_key;
  const replay = RETURNS_BY_KEY.get(key);
  if (replay !== undefined) return { ...replay, replayed: true };
  const sale = saleFromInput(body);
  RETURN_SEQ += 1;
  const subtotalCents = returnTotal(sale, body.lines);
  for (const line of body.lines) {
    const saleLine = sale.lines.find((row) => row.order_line_id === line.order_line_id);
    RETURNED_QTY.set(saleLine.order_line_id, (RETURNED_QTY.get(saleLine.order_line_id) ?? 0) + line.qty_base);
  }
  const answer = {
    return_id: uuid(40000 + RETURN_SEQ),
    return_no: `CT-R-${100 + RETURN_SEQ}`,
    order_id: sale.order_id,
    invoice_id: sale.invoice_id,
    refund_method: body.refund_method,
    subtotal_cents: subtotalCents,
    tax_cents: 0,
    total_cents: subtotalCents,
    pickup_status: "NONE",
    status: "COMMITTED",
    replayed: false,
  };
  RETURNS_BY_KEY.set(key, answer);
  RETURN_META.set(key, {
    clerk: caller.account.user_id,
    approver: approver.present && approver.account !== null ? approver.account.user_id : null,
  });
  if (body.refund_method === "CASH") DRAWER.cash_out_cents += answer.total_cents;
  return answer;
}

function readBody(request) {
  return new Promise((resolve, reject) => {
    let body = "";
    request.on("data", (chunk) => {
      body += chunk;
    });
    request.on("end", () => resolve(body));
    request.on("error", reject);
  });
}

// The staff caller, read back out of a bearer this stub itself minted: the
// payload's sub names the account. Null when there is no usable bearer, and
// the staff routes answer 401 like the real gate does.
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

function respondJson(response, status, payload, extraHeaders = {}) {
  response.writeHead(status, { "content-type": "application/json", ...extraHeaders });
  response.end(JSON.stringify(payload));
}

// The test suite asserts the app sends the demo org on its demo calls, so the
// stub echoes the org it received back as a response header: a wrong or
// missing X-Org on the app side fails the suite right here, which is where a
// slip in the client configuration would surface on the real box too (the
// core's membership source answers for hh-demo and nothing else).
function sawOrg(request) {
  return { "x-stub-saw-org": request.headers["x-org"] ?? "" };
}

const server = createServer(async (request, response) => {
  const url = new URL(request.url ?? "/", `http://127.0.0.1:${port}`);
  // Every api request is sighted for the harness's peek route: the path, and
  // whether the approver's header rode it. The credential's whole claim
  // (one send, the approver's client alone) is asserted against this ledger.
  if (url.pathname.startsWith("/api/")) {
    SIGHTINGS.push({
      path: url.pathname,
      approverHeader: (request.headers["x-approver-authorization"] ?? "") !== "",
    });
  }
  if (request.method === "GET" && url.pathname === "/healthz") {
    respondJson(response, 200, { ok: true });
    return;
  }
  if (request.method === "GET" && url.pathname === "/_stub/state") {
    // Harness-only: the tests' read back of this stub's ledgers. No contract
    // path shares the namespace, and the app never calls it.
    respondJson(response, 200, {
      keys: KEYS_SEEN,
      lastReturnBody: LAST_RETURN_BODY,
      sightings: SIGHTINGS,
      returns: [...RETURN_META.entries()].map(([key, meta]) => ({ key, ...meta })),
    });
    return;
  }
  if (request.method === "GET" && url.pathname === "/api/v1/demo/flows") {
    respondJson(response, 200, { flows: FLOWS }, sawOrg(request));
    return;
  }
  if (request.method === "POST" && url.pathname === "/api/v1/demo/session") {
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
=======
// The auth read the core's middleware does: the bearer's own sub claim names
// the account, and the account's grant names the branch. No bearer, no
// unreadable bearer, or an account without a grant: refused before any route
// logic runs.
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

function respondJson(response, status, payload) {
  response.writeHead(status, { "content-type": "application/json" });
  response.end(JSON.stringify(payload));
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
// The routes.
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

function counterHit(product) {
  return {
    product_id: product.product_id,
    sku: product.sku,
    name: product.name,
    description: product.description,
    base_uom: product.base_uom,
    sold_by: product.sold_by,
    has_image: false,
    match_reason: "NAME",
  };
}

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

const server = createServer((request, response) => {
  const url = new URL(request.url ?? "/", `http://127.0.0.1:${port}`);
  const path = url.pathname;

  if (request.method === "GET" && path === "/healthz") {
    respondJson(response, 200, { ok: true });
    return;
  }

  // The demo routes stay public, exactly as the contract marks them.
  if (request.method === "GET" && path === "/api/v1/demo/flows") {
    respondJson(response, 200, { flows: FLOWS });
    return;
  }
  if (request.method === "POST" && path === "/api/v1/demo/session") {
    readBody(request, (parsed) => {
      const flow = parsed === null || typeof parsed !== "object" ? undefined : parsed.flow;
>>>>>>> origin/till/l5-w7
      if (typeof flow !== "string" || !Object.hasOwn(USERS, flow)) {
        respondJson(response, 400, { error: "unknown flow" }, org);
        return;
      }
      respondJson(response, 200, { token: mintToken(flow), user: USERS[flow], flow }, org);
    });
    return;
  }
<<<<<<< HEAD
  if (request.method === "GET" && url.pathname === "/api/v1/pos/returns/lookup") {
    const caller = callerFromRequest(request);
    if (caller === null) {
      respondJson(response, 401, { error: "no usable staff credential" });
      return;
    }
    const orderNo = url.searchParams.get("order_no");
    const orderId = url.searchParams.get("order_id");
    const sale =
      (typeof orderNo === "string" && orderNo !== "" && SALES[orderNo]) ||
      (typeof orderId === "string" && orderId !== "" && Object.values(SALES).find((row) => row.order_id === orderId)) ||
      undefined;
    if (sale === undefined) {
      // A sale this caller's branch does not reach (another branch's sale
      // among them: the real lookup is branch fenced) is an invisible one.
      respondJson(response, 404, { error: "no sale with that number answers at this counter" });
      return;
    }
      respondJson(response, 200, {
        order_id: sale.order_id,
        order_no: sale.order_no,
        branch_id: sale.branch_id,
        customer_id: sale.customer_id,
        invoice_id: sale.invoice_id,
        sale_tender: sale.sale_tender,
        card_refundable: sale.card_refundable,
        lines: sale.lines.map((line) => ({
          ...line,
          qty_returned: RETURNED_QTY.get(line.order_line_id) ?? 0,
        })),
      });
      return;
    }
    if (request.method === "GET" && url.pathname === "/api/v1/till/sessions/open") {
      const caller = callerFromRequest(request);
      if (caller === null) {
        respondJson(response, 401, { error: "no usable staff credential" });
        return;
      }
      const branchId = url.searchParams.get("branch_id");
      const open = branchId === DRAWER.branch_id;
      respondJson(response, 200, open ? { open: true, id: DRAWER.session_id } : { open: false });
      return;
    }
    if (
      request.method === "GET" &&
      url.pathname.startsWith("/api/v1/till/sessions/") &&
      url.pathname.endsWith("/zreport")
    ) {
      const caller = callerFromRequest(request);
      if (caller === null) {
        respondJson(response, 401, { error: "no usable staff credential" });
        return;
      }
      const sessionId = url.pathname.split("/")[5];
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
    if (request.method === "GET" && url.pathname === "/api/v1/payments/config") {
      const caller = callerFromRequest(request);
      if (caller === null) {
        respondJson(response, 401, { error: "no usable staff credential" });
        return;
      }
      // The demo's card rail is the simulator, always (guard G7): the status
      // route says so, and the desk's card method repeats it in words.
      respondJson(response, 200, {
        configured: true,
        merchant_id: "hh-demo-merchant",
        processor: "stub",
        currency: "USD",
        simulated: true,
      });
      return;
    }
    if (request.method === "POST" && url.pathname === "/api/v1/pos/returns") {
      const caller = callerFromRequest(request);
      if (caller === null) {
        respondJson(response, 401, { error: "no usable staff credential" });
        return;
      }
      // The approver's credential beside the clerk's bearer, verified the way
      // the real rule verifies it: the same mint, an account whose role holds
      // the approval, a person other than the clerk. A header that is present
      // and not valid is refused before anything else is judged.
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
        body = JSON.parse(await readBody(request));
      } catch {
        respondJson(response, 400, { error: "the request body is not JSON" });
        return;
      }
      if (body !== null && typeof body === "object" && typeof body.idempotency_key === "string") {
        KEYS_SEEN.push(body.idempotency_key);
        LAST_RETURN_BODY = body;
      }
      const refusal = returnsRefusal(caller, body, approver);
      if (refusal !== null) {
        respondJson(response, refusal.status, refusal.body);
        return;
      }
      respondJson(response, 201, commitReturn(caller, body, approver));
      return;
    }
    respondJson(response, 404, { error: "not found" });
  });

server.on("error", (error) => {
  console.error(`demo stub could not listen on 127.0.0.1:${port}: ${error instanceof Error ? error.message : String(error)}`);
  process.exit(1);
=======

  // Every route below is a staff route: the caller read comes first.
  const caller = callerOf(request, url);
  if (caller.refused !== undefined) {
    respondJson(response, caller.refused, { error: caller.error });
    return;
  }
  const callerBranch = caller.branchId;

  if (request.method === "GET" && path === "/api/v1/branches") {
    respondJson(response, 200, BRANCHES.filter((b) => b.id === callerBranch));
    return;
  }

  if (request.method === "GET" && path === "/api/v1/till/sessions/open") {
    const branchId = url.searchParams.get("branch_id") ?? "";
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
    const items = PRODUCTS.filter(
      (p) =>
        p.sku.toLowerCase().startsWith(q) ||
        p.name.toLowerCase().includes(q) ||
        p.aliases.some((alias) => alias.includes(q)),
    ).map(counterHit);
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
  // restraint carries no Trenton cost on purpose, so the demo shows a real
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

  // The card rail's status read: the simulated flag the till's notices ride.
  if (request.method === "GET" && path === "/api/v1/payments/config") {
    respondJson(response, 200, PAYMENT_CONFIG);
    return;
  }

  // The ring: order, invoice and tender composed as one. The body carries no
  // price: every line is priced here through the same table the price read
  // answers from. The credit gate runs before any money moves, exactly where
  // the core's own gate sits.
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
      for (let index = 0; index < lines.length; index += 1) {
        const line = lines[index] ?? {};
        const productId = typeof line.product_id === "string" ? line.product_id : "";
        const qty = typeof line.qty_entered === "number" ? line.qty_entered : 0;
        const resolved = resolveLine(customer.id, productId, branchId);
        if (resolved === null || qty <= 0) {
          respondJson(response, 400, { error: `line ${index + 1} could not be priced by the engine` });
          return;
        }
        const total = lineTotalCents(resolved.customer_cents, qty);
        subtotal += total;
        outLines.push({
          line_no: index + 1,
          product_id: resolved.product.product_id,
          description: resolved.product.name,
          qty_entered: qty,
          uom_entered: typeof line.uom_entered === "string" ? line.uom_entered : resolved.price_uom,
          unit_price_cents: resolved.customer_cents,
          price_uom: resolved.price_uom,
          line_total_cents: total,
        });
      }
      const tax = taxCents(subtotal);
      const total = subtotal + tax;
      const tenders = [];
      let cashTendered;
      let changeDue;
      let paymentId;
      let gatewayRef;
      if (tender === "CASH") {
        cashTendered = parsed.cash_tendered_cents;
        if (!Number.isInteger(cashTendered) || cashTendered < total) {
          respondJson(response, 400, { error: "the cash tendered does not cover the sale's total" });
          return;
        }
        changeDue = cashTendered - total;
        paymentId = nextDocNo("pay");
        tenders.push({ seq: 1, method: "CASH", amount_cents: total, payment_id: paymentId });
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
        if (customer.open_balance_cents + total > customer.credit_limit_cents) {
          const blocked = creditBlocked("CREDIT_LIMIT_EXCEEDED");
          respondJson(response, blocked.status, blocked.body);
          return;
        }
        customer.open_balance_cents += total;
      } else if (tender !== "CHEQUE") {
        respondJson(response, 400, { error: "unknown tender" });
        return;
      }
      const orderId = uuidFromCounter(4000 + sales.size + 1);
      const sale = {
        order_id: orderId,
        order_no: nextDocNo("ORD"),
        invoice_id: uuidFromCounter(5000 + sales.size + 1),
        invoice_no: nextDocNo("INV"),
        tender,
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
        payment_id: paymentId,
        gateway_ref: gatewayRef,
        tenders,
        lines: outLines,
      };
      sales.set(orderId, sale);
      if (idempotencyKey !== "") salesByKey.set(idempotencyKey, orderId);
      respondJson(response, 201, saleResponseBody(sale, false));
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
        created_at: "2026-01-01T00:00:00Z",
      };
      parked.set(id, ticket);
      respondJson(response, 201, ticket);
    });
    return;
  }

  // The parked list: fenced to the caller's own branch before any filter the
  // query asks for, so Kingston's tickets never reach the Trenton counter,
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

  respondJson(response, 404, { error: "not found" });
>>>>>>> origin/till/l5-w7
});

// The ring's response body, in the contract's PosSale shape: the committed
// totals and tenders, the change due on a cash ring, and no line rows (the
// operator-reopen read carries those).
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
    cash_tendered_cents: sale.cash_tendered_cents,
    change_due_cents: sale.change_due_cents,
    tenders: sale.tenders,
  };
}

server.keepAliveTimeout = 120 * 1000;
server.headersTimeout = 125 * 1000;
server.listen(port, "127.0.0.1", () => {
  console.log(`demo stub on http://127.0.0.1:${port}`);
});
