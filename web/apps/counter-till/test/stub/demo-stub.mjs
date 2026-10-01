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
// The demo routes are public in the contract (security: []), so they check no
// credential; the phrase gate in front of the real demo box is Caddy's (W5's
// image), not the routes'. The staff routes are not public: this stub reads
// the minted bearer the same way the core's middleware does (the token's own
// sub claim) and refuses a call without one, so the app's header plumbing is
// exercised exactly as it would be against the real server.
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
      if (typeof flow !== "string" || !Object.hasOwn(USERS, flow)) {
        respondJson(response, 400, { error: "unknown flow" });
        return;
      }
      respondJson(response, 200, { token: mintToken(flow), user: USERS[flow], flow });
    });
    return;
  }

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

  respondJson(response, 404, { error: "not found" });
});

server.keepAliveTimeout = 120 * 1000;
server.headersTimeout = 125 * 1000;
server.listen(port, "127.0.0.1", () => {
  console.log(`demo stub on http://127.0.0.1:${port}`);
});
