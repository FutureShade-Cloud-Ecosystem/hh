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
const switches = { callerOnCostList: true, callerIsGeneralManager: false, floorRaiseBps: 0, approvalsShape: "page", quotePageSize: 0 };

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

// ---------------------------------------------------------------------------
// The third coder's panels' world: the assist key switch, the follow-up
// plans and events, the soft holds, the branch stock, and the approval cards
// the floor request door files. Every figure is the stub's own, standing in
// for the core; nothing in the app imports any of it.
// ---------------------------------------------------------------------------

const BELLEVILLE_ID = "6f1c2b3a-0000-4000-8000-000000000002";
const seamSwitches = { aiConfigured: false, seamWired: true };
const followPlans = new Map(); // quote id -> plan row
const followEvents = new Map(); // quote id -> events
const lapsedHolds = new Map(); // quote id -> expired hold rows
const approvalCards = new Map(); // request id -> the card the server wrote
const stockByProduct = new Map(); // product id -> { trenton, belleville }
const productApps = {
  "p-holland-60": ["PAVER"],
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
  for (const product of PRODUCTS) stockByProduct.set(product.product_id, { trenton: 400, belleville: 400 });
  // One short product on purpose: the seeded accepted quote asks for three of it.
  stockByProduct.set("p-clear-stone", { trenton: 2, belleville: 60 });
}
resetSeamWorld();

function branchStockRow(productId, branchId, caller) {
  const stock = stockByProduct.get(productId) ?? { trenton: 0, belleville: 0 };
  const here = branchId === TRENTON_ID;
  const available = here ? stock.trenton : stock.belleville;
  const product = productById.get(productId);
  return {
    product_id: productId,
    branch_id: branchId,
    branch_code: here ? "TRE" : "BEL",
    branch_name: here ? "Trenton" : "Belleville",
    on_hand_base: available,
    reserved_base: 0,
    in_transit_base: 0,
    available_base: available,
    soft_held_base: 0,
    ...(switches.callerOnCostList && product !== undefined ? { avg_cost_cents: product.branch_cost_cents[TRENTON_ID] ?? 0 } : {}),
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
  const floorBps = product.floor_bps + switches.floorRaiseBps;
  if (margin >= floorBps) return null;
  const isManager = caller.account.role_code === "sales_manager";
  if (isManager) return { cleared: true, by: USERS[caller.flow].display_name };
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

function seedWorld() {
  quoteCounter = 1000;
  quotes.clear();
  seedQuote("DRAFT", "c-maple-ridge", "sales", [
    { p: "p-holland-60", q: 120 },
    { p: "p-polymer-sand", q: 2 },
  ]);
  seedQuote("SENT", "c-fenwick", "sales-manager", [{ p: "p-wall-std", q: 10 }]);
  seedQuote("ACCEPTED", "c-maple-ridge", "sales", [{ p: "p-clear-stone", q: 3 }]);
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
  return { ...rest, effective_chips: rest.quick_chip_set.length > 0 ? [...rest.quick_chip_set] : ["Pavers"] };
}

// The dealer's one acceptance switch, and the scope rule table (seven rules,
// the reference's own set, wording fixed: only the switch is writable).
const acceptance = { allow_multi_accept: false, switch_wired: true };
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
function isSettingsAdmin(caller) {
  return caller.account.role_code === "sales_manager";
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
    respondJson(response, 200, { workspaceWrites, itemsWrites, policyWrites, decisions, conversions, switches: { ...switches } });
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
      const request = { id: `fr-${key}`, status: "PENDING", line_no: lineNo, disc_pct: Number(body.disc_pct) || 10 };
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
    acceptance.allow_multi_accept = false;
    resetPolicy();
    resetScopeRules();
    switches.callerOnCostList = true;
    switches.callerIsGeneralManager = false;
    switches.floorRaiseBps = 0;
    switches.approvalsShape = "page";
    switches.quotePageSize = 0;
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
      if (Number.isFinite(body.trenton)) row.trenton = body.trenton;
      if (Number.isFinite(body.belleville)) row.belleville = body.belleville;
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
        respondJson(response, 403, { error: "quote: the Inside Sales role may not change the quote policy" });
        return;
      }
      if (dealerRow && !isGeneralManager(caller)) {
        respondJson(response, 403, { error: "quote: the dealer wide default is set by the General Manager; the Sales Manager sets a branch row" });
        return;
      }
      if (!dealerRow && body.branch_id !== caller.branchId) {
        respondJson(response, 403, { error: "quote: that branch is not one your role reaches" });
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
      const { effective_chips: _skip, ...row } = body;
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
          const decision = floorDecision(caller, product, customer, disc);
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
          blocked: marginBps(unit, product.branch_cost_cents[TRENTON_ID] ?? 0) < product.floor_bps + switches.floorRaiseBps,
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
    readBody(request, response, (body) => {
      if (quote.status !== "ACCEPTED") {
        respondJson(response, 409, { error: `quote: this quote is ${quote.status.toLowerCase()}, only an accepted quote converts` });
        return;
      }
      quote.status = "CONVERTED";
      quote.fulfilment = body.fulfillment === "WILL_CALL" ? "WILL_CALL" : "PICKUP";
      conversions.push({ quote_id: quote.id, fulfillment: body.fulfillment ?? null });
      respondJson(response, 201, { order_id: `o-${quote.id}`, order_no: `SO-${quote.id.slice(-4)}` });
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
      const askedMargin = marginBps(askedUnit, product.branch_cost_cents[TRENTON_ID] ?? 0);
      // The detail is the server's own sentence, built from its own figures: the
      // line, the discount and the margin against the floor. The core files it
      // for the filer, who reads it in the filer's own card (qd1c).
      approvalCards.set(request.id, {
        quote_id: quote.id,
        requested_by: userId,
        detail: `Quote ${quote.quote_no}, line ${lineNo}, ${product.name}, ${disc}% discount` +
          `, margin ${(askedMargin / 100).toFixed(1)}% against a floor of ${((product.floor_bps + switches.floorRaiseBps) / 100).toFixed(1)}%`,
      });
      respondJson(response, 201, { filed: true, may_clear_itself: false, request });
    });
    return;
  }

  if (path === "/api/v1/quotes/settings/acceptance" && request.method === "GET") {
    if (!isSettingsAdmin(caller)) {
      respondJson(response, 403, { error: "quote: the acceptance settings are for administrators; the Inside Sales role may not read them" });
      return;
    }
    respondJson(response, 200, acceptance);
    return;
  }
  if (path === "/api/v1/quotes/settings/acceptance" && request.method === "PUT") {
    readBody(request, response, (body) => {
      if (!isGeneralManager(caller)) {
        respondJson(response, 403, { error: "quote: the acceptance switch is dealer wide and only the General Manager changes it" });
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
    if (!isSettingsAdmin(caller)) {
      respondJson(response, 403, { error: "quoteassist: only an administrator reads the scope rules" });
      return;
    }
    respondJson(response, 200, { rules: scopeRules });
    return;
  }
  match = /^\/api\/v1\/quote-assist\/scope-rules\/([^/]+)$/.exec(path);
  if (match !== null && request.method === "PATCH") {
    readBody(request, response, (body) => {
      if (!isSettingsAdmin(caller)) {
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
      role_items: roleItemsByProfile.get(asked ?? profileOf(caller)) ?? [],
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
      branches: [branchStockRow(match[1], TRENTON_ID, caller), branchStockRow(match[1], BELLEVILLE_ID, caller)],
      seam_wired: true,
    });
    return;
  }

  if (path === "/api/v1/staff/policy/check" && request.method === "POST") {
    readBody(request, response, (body) => {
      if (!Number.isInteger(body.price_cents) || !Number.isInteger(body.cost_cents) || typeof body.branch_id !== "string") {
        respondJson(response, 400, { error: "identity: price_cents, cost_cents and branch_id are required" });
        return;
      }
      const margin = marginBps(body.price_cents, body.cost_cents);
      const override = caller.account.role_code === "sales_manager";
      respondJson(response, 200, { allowed: margin >= 2000 || override, margin_bps: margin, override });
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
        branch_id: quote === undefined ? TRENTON_ID : quote.branch_id,
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
