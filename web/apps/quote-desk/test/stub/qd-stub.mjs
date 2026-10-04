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
//   POST /api/v1/quotes/{id}/lines/{n}/floor-approval -> QuoteFloorApprovalAnswer
//   GET/PUT/DELETE /api/v1/surfaces/quotes.builder/my-workspace  -> the workspace row
//   GET/PUT/DELETE /api/v1/surfaces/quotes.builder/my-items      -> the column curation
//   GET  /api/v1/surfaces/quotes.builder/config  -> SettingsSurfaceConfig
//   GET  /api/v1/customers/search                -> PosCustomer[]
//   GET  /api/v1/pos/salespeople                 -> PosSalespersonPage
//   GET  /api/v1/pos/addon-suggestions           -> { suggestions: [] }
//   GET  /api/v1/products?active=true            -> PosProduct[]
//   GET  /api/v1/products/counter-search         -> ProductCounterSearchResults
//   GET  /api/v1/products/scan                   -> CounterHit | 404
//   GET  /api/v1/products/{id}/image             -> 404 (the placeholder thumb)
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
// its own branch only (the demo world's Trenton).
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

const TRENTON_ID = "6f1c2b3a-0000-4000-8000-000000000001";

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
];

const ACCOUNT_BRANCH = {
  "demo-account-sales": TRENTON_ID,
  "demo-account-sales-manager": TRENTON_ID,
};

// The catalogue: list_cents is the retail anchor, branch_cost_cents the
// weighted cost the margin glance reads, floor_bps the category margin floor
// the discount decision reads. Holland's rule price for the contractor
// account sits below the Pavers floor on purpose: the plan's ruled mark (a
// customer's own rule price under the floor is accepted and marked) has to be
// reachable, and that is the one line that carries it.
const PRODUCTS = [
  {
    product_id: "p-holland-60",
    sku: "HPV-60",
    category: "Pavers",
    name: "Holland Stone 60 mm",
    description: "The Holland paver, 60 mm, per square foot",
    aliases: ["holland paver", "holland stone"],
    base_uom: "sf",
    sold_by: "sf",
    list_cents: 400,
    branch_cost_cents: { [TRENTON_ID]: 276 },
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
    branch_cost_cents: { [TRENTON_ID]: 980 },
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
    branch_cost_cents: { [TRENTON_ID]: 2100 },
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
    branch_cost_cents: { [TRENTON_ID]: 3400 },
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
    branch_cost_cents: { [TRENTON_ID]: 2500 },
    floor_bps: 1000,
    featured: false,
  },
];

const productById = new Map(PRODUCTS.map((p) => [p.product_id, p]));

// The account customers. The percent is the account's own pricing rule: the
// first contractor account's is the eighteen percent the plan's demo world
// names, and that rule is what puts Holland under the Pavers floor.
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
  },
  {
    id: "c-fenwick",
    account_no: "C02",
    name: "Fenwick Builders Ltd",
    customer_type: "BUILDER",
    payment_terms_days: 30,
    credit_limit_cents: 300000,
    open_balance_cents: 0,
    percent_off: 10,
  },
];

const customerById = new Map(CUSTOMERS.map((c) => [c.id, c]));

const HST_RATE_BPS = 1300;

// ---------------------------------------------------------------------------
// The stub's own switches, held in memory.
// ---------------------------------------------------------------------------

// The cost and margin list switch: the plan's ledger row for the wrong-role
// proof. True (the shipped configuration: both demo roles sit on the list)
// answers the glance with figures; false answers the glance as the empty
// object, byte-absent, exactly as the core's marshal gate does for a caller
// the list refuses. A test flips this for a page and flips it back.
const switches = { callerOnCostList: true };

// Every write the stub sees, for the suite's layout save assertions. The
// ledger is test scaffolding: no contract path reads it.
const workspaceWrites = [];
const itemsWrites = [];
// The share links minted, by quote id, and the request door's filed requests.
const shareLinks = new Map();
const floorRequests = new Map(); // `${quoteId}:${lineNo}` -> request row

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
  return Math.floor(((unitCents - costCents) * 10000) / unitCents);
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
  const costCents = product.branch_cost_cents[TRENTON_ID] ?? 0;
  const margin = marginBps(unitCents, costCents);
  if (margin >= product.floor_bps) return null;
  const isManager = caller.account.role_code === "sales_manager";
  if (isManager) return { cleared: true, by: USERS[caller.flow].display_name };
  return {
    cleared: false,
    body: {
      policy: "category_margin_floor",
      approver_roles: ["General Manager", "Financial Controller", "Sales Manager"],
      manager_may_set: true,
      ...(switches.callerOnCostList ? { margin_bps: margin, floor_bps: product.floor_bps } : {}),
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
  const costCents = product.branch_cost_cents[TRENTON_ID] ?? 0;
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

function seedQuote(status, customerId, ownerFlow, lines, terms = {}) {
  quoteCounter += 1;
  const id = `q-${quoteCounter}`;
  const customer = customerById.get(customerId);
  const quote = {
    id,
    quote_no: `Q-${quoteCounter}`,
    customer_id: customerId,
    customer_name: customer.name,
    branch_id: TRENTON_ID,
    status,
    valid_until: "",
    expired: false,
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

seedQuote("DRAFT", "c-maple-ridge", "sales", [
  { p: "p-holland-60", q: 120 },
  { p: "p-polymer-sand", q: 2 },
]);
seedQuote("SENT", "c-fenwick", "sales-manager", [{ p: "p-wall-std", q: 10 }]);
seedQuote("ACCEPTED", "c-maple-ridge", "sales", [{ p: "p-clear-stone", q: 3 }]);

function renumber(quote) {
  quote.lines.forEach((line, index) => {
    line.line_no = index + 1;
  });
}

function restamp(quote) {
  const totals = totalsOf(quote);
  quote.subtotal_cents = totals.subtotal_cents;
  quote.tax_cents = totals.tax_cents;
  quote.total_cents = totals.total_cents;
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
    can_convert: quote.status === "ACCEPTED",
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
    cost += Math.round((productById.get(line.product_id)?.branch_cost_cents[TRENTON_ID] ?? 0) * Number(line.qty_entered));
  }
  const partial = quote.lines.some((line) => {
    const product = productById.get(line.product_id);
    return product === undefined || product.branch_cost_cents[TRENTON_ID] === undefined;
  });
  return {
    lines: quote.lines.map((line) => {
      const unitCost = productById.get(line.product_id)?.branch_cost_cents[TRENTON_ID];
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
  workspace_split_pct: 58,
  workspace_arrangement: "PRIMARY_LEFT",
};

const workspaces = new Map(); // user_id -> saved row
const myItems = new Map(); // user_id -> items array

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
    respondJson(response, 200, { workspaceWrites, itemsWrites, switches: { ...switches } });
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
    respondJson(response, 200, { items, next_cursor: null, limit: 50 });
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
        quote.valid_until = typeof body.valid_until === "string" ? body.valid_until : "";
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
      const line = newLine(product, customer, body.qty_entered, { lineNo: quote.lines.length + 1 });
      quote.lines.push(line);
      restamp(quote);
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
      quote.lines = quote.lines.filter((row) => row.line_no !== lineNo);
      renumber(quote);
      restamp(quote);
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
          const decision = floorDecision(caller, product, customer, disc);
          if (decision !== null && decision.cleared) {
            // The manager's own clearing: accepted once, stamped on the line.
            quote.lines = quote.lines.map((row) =>
              row.line_no === lineNo ? newLine(product, customer, row.qty_entered, { lineNo, discPct: disc, floorClearedBy: decision.by }) : row,
            );
            restamp(quote);
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
          quote.lines = quote.lines.map((row) =>
            row.line_no === lineNo ? newLine(product, customer, row.qty_entered, { lineNo, discPct: disc }) : row,
          );
          restamp(quote);
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
        quote.lines = quote.lines.map((row) =>
          row.line_no === lineNo ? newLine(product, customer, qty, { lineNo, discPct: row.disc_pct, floorClearedBy: row.floor_cleared_by }) : row,
        );
        restamp(quote);
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

  match = /^\/api\/v1\/quotes\/([^/]+)\/share-links$/.exec(path);
  if (match !== null && request.method === "POST") {
    const quote = quoteOr404(match[1]);
    if (quote === null) {
      respondJson(response, 404, { error: "no quote with that id" });
      return;
    }
    readBody(request, response, () => {
      // Minting is the send: the reference's own rule, DRAFT moves to SENT.
      if (quote.status === "DRAFT") quote.status = "SENT";
      const row = {
        id: `sl-${quote.id}`,
        quote_id: quote.id,
        expires_at: "2027-01-01T00:00:00Z",
        created_by: userId,
        created_at: "2026-01-01T00:00:00Z",
        live: true,
        token: `qd-demo-${quote.id}`,
      };
      shareLinks.set(quote.id, row);
      respondJson(response, 200, row);
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
    readBody(request, response, () => {
      if (quote.status !== "ACCEPTED") {
        respondJson(response, 409, { error: `quote: this quote is ${quote.status.toLowerCase()}, only an accepted quote converts` });
        return;
      }
      quote.status = "CONVERTED";
      respondJson(response, 200, { order_id: `o-${quote.id}`, order_no: `SO-${quote.id.slice(-4)}` });
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
        respondJson(response, 200, { filed: false, may_clear_itself: true });
        return;
      }
      const key = `${quote.id}:${lineNo}`;
      const standing = floorRequests.get(key);
      if (standing !== undefined && standing.status !== "REJECTED") {
        respondJson(response, 200, { filed: true, may_clear_itself: false, request: standing });
        return;
      }
      const request = { id: `fr-${key}`, status: "PENDING", line_no: lineNo, disc_pct: disc };
      floorRequests.set(key, request);
      respondJson(response, 200, { filed: true, may_clear_itself: false, request });
    });
    return;
  }

  // The surface settings kit.
  match = /^\/api\/v1\/surfaces\/([^/]+)\/config$/.exec(path);
  if (match !== null && request.method === "GET" && match[1] === SURFACE_ID) {
    const workspace = workspaceRow(userId, POLICY);
    respondJson(response, 200, {
      surface_id: SURFACE_ID,
      // The manager owns the screen's settings (CP0-5 answer A), so its
      // config answers the admin profile and the work area offers the
      // settings entry.
      profile: caller.account.role_code === "sales_manager" ? "admin" : "staff",
      role_items: [],
      user_items: myItems.get(userId) ?? [],
      workspace: { ...workspace, open_ref: workspace.open_ref === "" ? undefined : workspace.open_ref },
      policy: POLICY,
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
        on_hand_by_branch: { [TRENTON_ID]: 400 },
        is_featured: product.featured,
      })),
    );
    return;
  }

  if (request.method === "GET" && path === "/api/v1/products/counter-search") {
    const q = (url.searchParams.get("q") ?? "").toLowerCase();
    const hits = PRODUCTS.filter((product) => {
      const hay = [product.sku, product.name, product.description, ...product.aliases].join(" ").toLowerCase();
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
      on_hand_by_branch: { [TRENTON_ID]: 400 },
      list_price_cents: product.list_cents,
    }));
    respondJson(response, 200, { items: hits });
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
      on_hand_by_branch: { [TRENTON_ID]: 400 },
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

  respondJson(response, 404, { error: `no stub route for ${request.method} ${path}` });
});

server.on("error", (error) => {
  console.error(`qd-stub: ${error instanceof Error ? error.message : String(error)}`);
  process.exit(1);
});

server.listen(port, "127.0.0.1", () => {
  console.log(`qd-stub listening on 127.0.0.1:${port}`);
});
