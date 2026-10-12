// The pricing double for the Quote Desk's stub: the pricing console's routes in the
// contract's shapes, a small resolver that stands in for the core's (the only place a
// price or a margin is ever computed), and the floor warning's two routes. Mounted by
// qd-stub.mjs with one call. A development and test double only: nothing in the app
// imports it, and the real routes are the core's.
//
//   GET  /api/v1/pricing/category-policies              -> policies
//   PUT  /api/v1/pricing/category-policies/{id}         -> policy
//   GET/POST /api/v1/pricing/rules, GET/PUT/DELETE /rules/{id}
//   GET/POST /api/v1/pricing/customers/{id}/overrides, PUT/DELETE /overrides/{id}
//   GET/POST /api/v1/pricing/quantity-breaks, GET/PUT/DELETE /quantity-breaks/{id}
//   GET  /api/v1/pricing/audit
//   POST /api/v1/pricing/floor-preview, POST /api/v1/pricing/floor-confirmations
//   GET  /api/v1/staff                                  -> the roster (names for the log)
//   GET  /api/v1/products/{id}                          -> one product (a break's scope label)
//
// Lists answer a bare array, which is what the core writes today (the contract says a
// page); a test can ask for the page shape. The role rule is the stub's own, in words:
// the Sales manager edits, the Inside Sales login is refused everything with the core's
// 403 body, and two switches stand for the roles in between (read only, and a log the
// cost and margin list refuses).

const CAT = {
  pavers: "ca700000-0000-4000-9000-000000000001",
  walls: "ca700000-0000-4000-9000-000000000002",
  slabs: "ca700000-0000-4000-9000-000000000003",
  aggregates: "ca700000-0000-4000-9000-000000000004",
  accessories: "ca700000-0000-4000-9000-000000000005",
  bulk: "ca700000-0000-4000-9000-000000000006",
};

// The stub's catalogue: list and cost per base unit, in integer cents. The cost is
// Point Loma's; Julian's own cost sits beside it (dearer on the pavers), because a change
// that names no branch is graded at every yard at its own cost, and the worst branch must
// be a real choice rather than the same yard every time.
const JULIAN_ID = "6f1c2b3a-0000-4000-8000-000000000002";
const YARDS = [
  { id: "6f1c2b3a-0000-4000-8000-000000000001", code: "PLM", name: "Point Loma" },
  { id: JULIAN_ID, code: "JUL", name: "Julian" },
];
const JULIAN_COST = new Map([
  ["a7000000-0000-4000-9000-000000000001", 300], // HPV-60: 276 at Point Loma
  ["a7000000-0000-4000-9000-000000000002", 395], // HPV-80: 360 at Point Loma
]);
const yardCost = (product, yard) => (yard.id === JULIAN_ID ? JULIAN_COST.get(product.id) ?? product.cost : product.cost);
const PRODUCTS = [
  { id: "a7000000-0000-4000-9000-000000000001", sku: "HPV-60", name: "Charcoal Stone 60 mm", category: CAT.pavers, uom: "SF", list: 400, cost: 276 },
  { id: "a7000000-0000-4000-9000-000000000002", sku: "HPV-80", name: "Charcoal Stone 80 mm", category: CAT.pavers, uom: "SF", list: 520, cost: 360 },
  { id: "a7000000-0000-4000-9000-000000000003", sku: "WALL-12", name: "Retaining wall block", category: CAT.walls, uom: "EA", list: 900, cost: 610 },
  { id: "a7000000-0000-4000-9000-000000000004", sku: "POLY-SAND", name: "Polymeric sand", category: CAT.accessories, uom: "EA", list: 4200, cost: 2800 },
  { id: "a7000000-0000-4000-9000-000000000005", sku: "BASE-T", name: "Base gravel", category: CAT.aggregates, uom: "T", list: 3800, cost: 2500 },
  { id: "a7000000-0000-4000-9000-000000000006", sku: "GLS-BLUFF-STEP", name: "Bluff Natural Step", category: CAT.slabs, uom: "KG", list: 120, cost: 70 },
];
const productById = new Map(PRODUCTS.map((p) => [p.id, p]));

// The stub's customers by the id the quote stub uses, with the uuid the pricing log
// links to (the core's ids are uuids; the quote stub's are labels).
const CUSTOMER_ALIAS = new Map([
  ["c0000000-0000-4000-9000-000000000001", "c-maple-ridge"],
  ["c0000000-0000-4000-9000-000000000002", "c-fenwick"],
]);
const CUSTOMER_UUID = new Map([...CUSTOMER_ALIAS].map(([uuid, label]) => [label, uuid]));
const canonCustomer = (id) => CUSTOMER_ALIAS.get(id) ?? id;
const uuidOfCustomer = (id) => CUSTOMER_UUID.get(id) ?? id;

// The pricing type of each stub customer, which the core reads from the account.
const CUSTOMER_TYPE = new Map([
  ["c-maple-ridge", "CONTRACTOR"],
  ["c-fenwick", "RETAIL"],
]);
const customerTypeOf = (id) => CUSTOMER_TYPE.get(canonCustomer(id)) ?? "RETAIL";

const TYPES = ["CONTRACTOR", "RETAIL", "BUILDER", "GOVERNMENT", "INTERNAL"];
const UNITS_PER = { PLT: 120, SF: 1, EA: 1, T: 1, KG: 1, PC: 1, LF: 1, BAG: 1 };
const DEFAULT_FLOOR_BPS = 1200;

let seq = 0;
const newId = (prefix) => {
  seq += 1;
  return `${prefix}0000000-0000-4000-9000-${String(seq).padStart(12, "0")}`;
};

const switches = { mode: "editor", auditAllowed: true, shape: "bare", failNext: null, failConfirm: false, forcePreview: null, productNames: true };
let world;

function seedWorld() {
  seq = 100;
  world = {
    // The categories the dealer's catalogue holds (product_categories). The server's PUT refuses any other id.
    catalogue: new Set(Object.values(CAT)),
    policies: new Map([
      [CAT.pavers, { category_id: CAT.pavers, margin_target_bps: 3300, margin_floor_bps: 2000, margin_enforcement_disabled: false }],
      [CAT.walls, { category_id: CAT.walls, margin_target_bps: 3000, margin_floor_bps: 2000, margin_enforcement_disabled: false }],
      [CAT.slabs, { category_id: CAT.slabs, margin_target_bps: 3000, margin_floor_bps: 2000, margin_enforcement_disabled: false }],
      [CAT.aggregates, { category_id: CAT.aggregates, margin_target_bps: 3000, margin_floor_bps: 2000, margin_enforcement_disabled: false }],
      [CAT.accessories, { category_id: CAT.accessories, margin_target_bps: 3000, margin_floor_bps: 2000, margin_enforcement_disabled: false }],
      [CAT.bulk, { category_id: CAT.bulk, margin_target_bps: 4000, margin_floor_bps: 2000, margin_enforcement_disabled: false }],
    ]),
    rules: [
      rule("TIER_CATEGORY", { customer_type: "CONTRACTOR", category_id: CAT.bulk }, "PCT_OFF_LIST", { discount_bps: 0 }, "SF", 100, "Provisional: bulk default; real breaks land with the general manager's worksheet"),
      rule("TIER_CATEGORY", { customer_type: "CONTRACTOR", category_id: CAT.pavers }, "PCT_OFF_LIST", { discount_bps: 1800 }, "SF", 40, "Contractor, Pavers: 18% off"),
      rule("TIER_CATEGORY", { customer_type: "CONTRACTOR", category_id: CAT.accessories }, "PCT_OFF_LIST", { discount_bps: 800 }, "SF", 40, "Contractor, Accessories: 8% off"),
      rule("CUSTOMER_CATEGORY", { customer_id: "c0000000-0000-4000-9000-000000000001", category_id: CAT.pavers }, "PCT_OFF_LIST", { discount_bps: 800 }, "SF", 20, "Maple Ridge, Pavers: 8% off"),
      // Limited to one branch, on a scope whose editor shows no branch field: the round trip
      // must carry the branch the rule already has (the server's patch clears it only on "").
      rule("TIER_DEFAULT", { customer_type: "CONTRACTOR", branch_id: "6f1c2b3a-0000-4000-8000-000000000001" }, "PCT_OFF_LIST", { discount_bps: 1500 }, "SF", 10, "Contractor, default: 15% off"),
    ],
    overrides: [],
    breaks: [
      breakRow("NATSTONE-PARTIAL-SKID", "Natural stone, partial-skid surcharge (PROVISIONAL)", { product_id: "a7000000-0000-4000-9000-000000000006" }, "KG", [
        { from_qty: 0, to_qty: 2000, value_type: "SURCHARGE_PCT", surcharge_bps: 1000, price_uom: "KG" },
      ]),
      breakRow("PAVER-FULL-SKID", "Pavers, full-skid discount (PROVISIONAL)", { category_id: CAT.pavers }, "PLT", [
        { from_qty: 1, to_qty: null, value_type: "PCT_OFF", discount_bps: 500, price_uom: "SF" },
      ]),
    ],
    audit: [],
    ledger: { confirmations: [], previews: [], writes: [] },
  };
}

function serveRule(r) {
  return { ...r, valid_from: servedDate(r.valid_from ?? null), valid_to: servedDate(r.valid_to ?? null) };
}

function rule(scope, dims, calc, value, uom, priority, detail) {
  return {
    id: newId("b"), scope, customer_id: "", customer_type: "", category_id: "", product_id: "", branch_id: "",
    ...dims, calc_type: calc, price_uom: uom, priority, detail_text: detail, is_active: true, version: 1, ...value,
  };
}

function breakRow(code, name, scope, threshold, items) {
  return { id: newId("d"), code, name, threshold_uom: threshold, is_active: true, version: 1, ...scope, items };
}

seedWorld();

// ── the role rule, the stub's own ───────────────────────────────────────────

function forbidden(respondJson, response) {
  respondJson(response, 403, { error: "Forbidden" });
}

// ── the stub's resolver: the core's job, in a few lines ─────────────────────

const applyPct = (base, bps) => Math.floor((base * (10000 - bps)) / 10000);
const marginOf = (price, cost) => (price <= 0 ? 0 : Math.floor(((price - cost) * 10000) / price));

function priceFor(product, ctx, qty, uom, w, yard) {
  const branchId = yard === null ? "" : yard.id;
  const cost = yard === null ? product.cost : yardCost(product, yard);
  const matchRule = (r) => {
    if (!r.is_active) return false;
    // A rule limited to one branch reaches a resolve at that branch alone (the core's rules
    // read drops it everywhere else), so a branch-less change still meets it at its own yard.
    if ((r.branch_id ?? "") !== "" && r.branch_id !== branchId) return false;
    if (r.scope === "CUSTOMER_PRODUCT") return ctx.customerId !== "" && r.customer_id === ctx.customerId && r.product_id === product.id;
    if (r.scope === "CUSTOMER_CATEGORY") return ctx.customerId !== "" && r.customer_id === ctx.customerId && r.category_id === product.category;
    if (r.scope === "TIER_CATEGORY") return r.customer_type === ctx.customerType && r.category_id === product.category;
    return r.customer_type === ctx.customerType;
  };
  const calc = (r) => {
    if (r.calc_type === "ABSOLUTE") return r.price_cents;
    if (r.calc_type === "MARKUP_ON_COST") return Math.floor((cost * (10000 + r.markup_bps)) / 10000);
    return applyPct(product.list, r.discount_bps);
  };
  const best = (scope) => w.rules.filter((r) => r.scope === scope && matchRule(r)).sort((a, b) => b.priority - a.priority)[0];
  let price = product.list;
  let source = "LIST";
  const cp = best("CUSTOMER_PRODUCT");
  const ov = ctx.customerId === "" ? undefined : w.overrides.find((o) => o.customer_id === ctx.customerId && o.category_id === product.category);
  if (cp !== undefined) {
    price = calc(cp);
    source = "CUSTOMER_PRODUCT_RULE";
  } else {
    let chosen = null;
    if (ov !== undefined) chosen = { price: calc(ov), source: "CUSTOMER_OVERRIDE" };
    else if (best("CUSTOMER_CATEGORY") !== undefined) chosen = { price: calc(best("CUSTOMER_CATEGORY")), source: "CUSTOMER_CATEGORY_RULE" };
    else if (best("TIER_CATEGORY") !== undefined) chosen = { price: calc(best("TIER_CATEGORY")), source: "TIER_CATEGORY" };
    else if (best("TIER_DEFAULT") !== undefined) chosen = { price: calc(best("TIER_DEFAULT")), source: "TIER_DEFAULT" };
    if (chosen !== null) ({ price, source } = chosen);
    // A quantity break replaces the price only when it is cheaper; a surcharge never is.
    const inThreshold = (qty * (UNITS_PER[uom] ?? 1)) / (UNITS_PER.PLT ?? 1);
    for (const b of w.breaks) {
      if (!b.is_active) continue;
      if (!((b.product_id ?? "") === product.id || (b.category_id ?? "") === product.category)) continue;
      const qtyInThreshold = b.threshold_uom === "PLT" ? inThreshold : qty;
      const tier = b.items.find((it) => qtyInThreshold >= it.from_qty && (it.to_qty === null || it.to_qty === undefined || qtyInThreshold < it.to_qty));
      if (tier === undefined || tier.value_type === "SURCHARGE_PCT" || tier.value_type === "SURCHARGE_ABS") continue;
      const off = tier.value_type === "PCT_OFF" ? applyPct(product.list, tier.discount_bps) : product.list - tier.discount_cents;
      if (off < price) {
        price = off;
        source = "QUANTITY_BREAK";
      }
    }
  }
  const policy = w.policies.get(product.category);
  const floor = policy === undefined ? DEFAULT_FLOOR_BPS : policy.margin_floor_bps;
  const margin = marginOf(price, cost);
  const gated = policy === undefined || !policy.margin_enforcement_disabled;
  return { price, source, margin, floor, blocked: gated && margin < floor };
}

function overlay(body) {
  const w = { policies: new Map(world.policies), rules: world.rules.map((r) => ({ ...r })), overrides: world.overrides.map((o) => ({ ...o })), breaks: world.breaks.map((b) => ({ ...b })) };
  if (body.kind === "rule" && body.rule !== undefined) {
    const r = body.rule;
    const next = { ...r, id: body.id ?? "candidate", is_active: true };
    w.rules = w.rules.filter((x) => x.id !== body.id);
    w.rules.push(next);
  }
  if (body.kind === "override" && body.override !== undefined) {
    const o = { ...body.override, customer_id: canonCustomer(body.customer_id), id: body.id ?? "candidate" };
    w.overrides = w.overrides.filter((x) => x.id !== body.id);
    w.overrides.push(o);
  }
  if (body.kind === "category_policy" && body.policy !== undefined) {
    w.policies.set(body.category_id, { category_id: body.category_id, ...body.policy });
  }
  if (body.kind === "quantity_break" && body.quantity_break !== undefined) {
    const b = { ...body.quantity_break, id: body.id ?? "candidate", is_active: true };
    w.breaks = w.breaks.filter((x) => x.id !== body.id);
    w.breaks.push(b);
  }
  return w;
}

function floorPreview(body) {
  const after = overlay(body);
  const before = { policies: world.policies, rules: world.rules, overrides: world.overrides, breaks: world.breaks };
  let products = PRODUCTS;
  let contexts = TYPES.map((t) => ({ customerType: t, customerId: "" }));
  let qtys = [{ qty: 1, uom: null }];
  if (body.kind === "rule") {
    const r = body.rule;
    if (r.scope === "TIER_CATEGORY") products = PRODUCTS.filter((p) => p.category === r.category_id);
    if (r.scope === "CUSTOMER_PRODUCT") products = PRODUCTS.filter((p) => p.id === r.product_id);
    contexts = r.scope === "CUSTOMER_PRODUCT" || r.scope === "CUSTOMER_CATEGORY"
      ? [{ customerType: customerTypeOf(r.customer_id), customerId: canonCustomer(r.customer_id) }]
      : [{ customerType: r.customer_type, customerId: "" }];
    if (r.scope === "CUSTOMER_CATEGORY") products = PRODUCTS.filter((p) => p.category === r.category_id);
  } else if (body.kind === "override") {
    products = PRODUCTS.filter((p) => p.category === body.override.category_id);
    contexts = [{ customerType: customerTypeOf(body.customer_id), customerId: canonCustomer(body.customer_id) }];
  } else if (body.kind === "category_policy") {
    products = PRODUCTS.filter((p) => p.category === body.category_id);
  } else if (body.kind === "quantity_break") {
    const b = body.quantity_break;
    products = PRODUCTS.filter((p) => (b.product_id !== "" && p.id === b.product_id) || (b.category_id !== "" && p.category === b.category_id));
    qtys = b.items.filter((it) => it.value_type === "PCT_OFF" || it.value_type === "ABS_OFF").map((it) => (it.from_qty > 0 ? { qty: it.from_qty, uom: b.threshold_uom } : { qty: 1, uom: null }));
  }
  const items = [];
  let evaluated = 0;
  const truncated = products.length > 200;
  if (qtys.length === 0) return { items, evaluated: 0, not_graded: 0, no_cost: 0, no_cost_branches: [], not_graded_branches: 0, truncated };
  // A change that names a branch (the request's own or the rule's) is graded at that branch
  // alone, its id in each item and no name; one that names none is graded at every yard, each
  // at its own cost with its own branch-limited rules, and the worst item names its yard (the
  // core's round 3 fix, mirrored).
  const namedBranch = body.branch_id ?? (body.kind === "rule" ? body.rule?.branch_id ?? "" : "");
  const sites = namedBranch === ""
    ? YARDS.map((yard) => ({ yard, branch_id: yard.id, branch_code: yard.code, branch_name: yard.name }))
    : [{ yard: null, branch_id: namedBranch, branch_code: "", branch_name: "" }];
  for (const product of products.slice(0, 200)) {
    evaluated += 1;
    let worst = null;
    for (const ctx of contexts) {
      for (const q of qtys) {
        for (const site of sites) {
          const uom = q.uom ?? product.uom;
          const a = priceFor(product, ctx, q.qty, uom, after, site.yard);
          if (!a.blocked) continue;
          const b = priceFor(product, ctx, q.qty, uom, before, site.yard);
          const item = {
            product_id: product.id, sku: product.sku, name: product.name, category_id: product.category,
            ...(ctx.customerType === "" ? {} : { customer_type: ctx.customerType }),
            branch_id: site.branch_id, ...(site.branch_name === "" ? {} : { branch_code: site.branch_code, branch_name: site.branch_name }),
            final_cents: a.price, margin_bps: a.margin, floor_bps: a.floor, price_uom: product.uom.toLowerCase(), source: a.source,
            previous_final_cents: b.price, previous_margin_bps: b.margin, already_under_floor: b.blocked,
          };
          // A newly under case outranks one that was under already; among the already-under ones a
          // price the change DROPPED outranks one it left standing; then the lower margin. The
          // core's worseBreach, mirrored (round 2 P2-B).
          const dropped = (x) => x.final_cents < x.previous_final_cents;
          if (worst === null || (item.already_under_floor !== worst.already_under_floor
            ? !item.already_under_floor
            : dropped(item) !== dropped(worst)
              ? dropped(item)
              : item.margin_bps < worst.margin_bps)) worst = item;
        }
      }
    }
    if (worst !== null) items.push(worst);
  }
  items.sort((x, y) => x.margin_bps - y.margin_bps);
  // Every stub product carries a cost at both yards, so no yard is ever left ungraded for
  // want of one; the two round 4 fields ride along empty, as the core answers them.
  return { items, evaluated, not_graded: 0, no_cost: 0, no_cost_branches: [], not_graded_branches: 0, truncated };
}

// ── the pricing log ─────────────────────────────────────────────────────────

let clock = 0;
function logRow(userId, entityType, entityId, action, fieldName, oldValue, newValue, ref) {
  clock += 1;
  const when = new Date(Date.UTC(2026, 9, 4, 0, 0, 0) + clock * 60000).toISOString();
  world.audit.unshift({
    id: newId("a"), entity_type: entityType, entity_id: entityId, action,
    ...(fieldName === undefined ? {} : { field_name: fieldName }),
    ...(oldValue === undefined ? {} : { old_value: oldValue }),
    ...(newValue === undefined ? {} : { new_value: newValue }),
    user_id: userId, created_at: when,
    ...(ref === undefined ? {} : { ref_entity_type: ref.type, ref_entity_id: ref.id }),
  });
}

const asList = (rows) => (switches.shape === "page" ? { items: rows, next_cursor: null } : rows);

// The core stores a typed date as an instant: a valid_from day at its midnight, a valid_to day as
// the midnight of the NEXT day (the upper bound is exclusive, so the typed day is inclusive), and
// an RFC3339 value as it came. An empty string clears the bound.
// A core not hosted in UTC serves the stored instants with its own offset: render the stored
// UTC instant at a fixed UTC-8 offset, the way such a core's rule reads read. The panel must
// take the date part in UTC whatever offset the string carries.
function servedDate(value) {
  if (typeof value !== "string" || value === "") return value;
  const at = new Date(value);
  if (Number.isNaN(at.getTime())) return value;
  return `${new Date(at.getTime() - 8 * 3600000).toISOString().slice(0, 19)}-08:00`;
}

function storedDate(value, endOfDay) {
  if (value === "") return null;
  if (typeof value !== "string") return undefined;
  if (/^\d{4}-\d{2}-\d{2}$/.test(value)) {
    const at = Date.parse(`${value}T00:00:00Z`) + (endOfDay ? 86400000 : 0);
    return new Date(at).toISOString().replace(".000Z", "Z");
  }
  return value;
}

function validBps(v) {
  return v === undefined || v === null || (Number.isInteger(v) && v >= 0 && v <= 10000);
}

// ── the handler ─────────────────────────────────────────────────────────────

export function resetPricing() {
  seedWorld();
  switches.mode = "editor";
  switches.auditAllowed = true;
  switches.shape = "bare";
  switches.failNext = null;
  switches.failConfirm = false;
  switches.forcePreview = null;
  switches.productNames = true;
  clock = 0;
}

export function handlePricing({ request, response, path, url, callerOf, readBody, respondJson }) {
  // The suite's switches and peek routes.
  if (path === "/_stub/pricing-mode" && request.method === "PUT") {
    readBody(request, response, (body) => {
      if (typeof body.mode === "string") switches.mode = body.mode;
      if (typeof body.audit_allowed === "boolean") switches.auditAllowed = body.audit_allowed;
      if (typeof body.shape === "string") switches.shape = body.shape === "page" ? "page" : "bare";
      if (body.fail_next !== undefined) switches.failNext = body.fail_next;
      if (body.product_names !== undefined) switches.productNames = body.product_names;
      if (typeof body.fail_confirm === "boolean") switches.failConfirm = body.fail_confirm;
      // Category ids whose margin policy the world does not hold (the live demo holds two): the add row's picker lists them.
      if (Array.isArray(body.drop_policies)) for (const id of body.drop_policies) world.policies.delete(id);
      // The catalogue the world holds from now on (a migrate-only database holds Pavers and Bulk): the policy PUT refuses the rest.
      if (Array.isArray(body.catalogue)) world.catalogue = new Set(body.catalogue);
      // Category ids a tier rule is scoped to: the pricing reads then prove those categories exist.
      if (Array.isArray(body.rule_categories)) for (const id of body.rule_categories) world.rules.push(rule("TIER_CATEGORY", { customer_type: "RETAIL", category_id: id }, "PCT_OFF_LIST", { discount_bps: 0 }, "SF", 10, "Retail default"));
      // Fields laid over the next previews' answer (not_graded, no_cost, truncated, items), or null to stop.
      if (body.force_preview !== undefined) switches.forcePreview = body.force_preview;
      respondJson(response, 200, { ...switches });
    });
    return true;
  }
  if (path === "/_stub/pricing-state" && request.method === "GET") {
    respondJson(response, 200, {
      rules: world.rules, policies: [...world.policies.values()], overrides: world.overrides, breaks: world.breaks,
      audit: world.audit, confirmations: world.ledger.confirmations, previews: world.ledger.previews, writes: world.ledger.writes,
    });
    return true;
  }
  // Pad the log with plain rows, to walk the pager and the window note.
  if (path === "/_stub/pricing-seed-audit" && request.method === "POST") {
    readBody(request, response, (body) => {
      for (let i = 0; i < Number(body.count); i += 1) {
        logRow("demo-account-sales-manager", "pricing_rule", newId("f"), "UPDATE", "priority", String(i), String(i + 1), undefined);
      }
      respondJson(response, 200, { rows: world.audit.length });
    });
    return true;
  }
  // One BYPASS row with its reason, as the core writes when a floor is switched off for a category.
  if (path === "/_stub/pricing-seed-bypass" && request.method === "POST") {
    logRow("demo-account-sales-manager", "margin_enforcement", CAT.pavers, "BYPASS", "margin_floor", undefined, "bypassed", { type: "category", id: CAT.pavers });
    world.audit[0].bypass_reason = "CATEGORY_POLICY_DISABLED";
    respondJson(response, 200, { rows: world.audit.length });
    return true;
  }
  // The log's widest honest row: a rule note carrying a long link, the kind an editor pastes.
  // A URL has no break opportunity, so this one cell's floor sits above the 820 card's width
  // and the table cannot shrink inside it: at that width this row is what the table's own
  // sideways scroll must reach, and what the card's own overflow check must see (round 2 P2-D).
  if (path === "/_stub/pricing-seed-wide-row" && request.method === "POST") {
    const ruleId = world.rules[0].id;
    logRow("demo-account-sales-manager", "pricing_rule", ruleId, "UPDATE", "detail_text",
      "Contractor, default: 15% off",
      "Reviewed with purchasing, tracker: https://intranet.hardscape.example/pricing-reviews/floors/contractor-default-tier-and-branch-consolidation",
      { type: "rule", id: ruleId });
    respondJson(response, 200, { rows: world.audit.length });
    return true;
  }
  // Another editor saved first: the record's version moves under an open form.
  if (path === "/_stub/pricing-bump" && request.method === "POST") {
    readBody(request, response, (body) => {
      const target = [...world.rules, ...world.overrides, ...world.breaks].find((x) => x.id === body.id);
      if (target === undefined) return respondJson(response, 404, { error: "no such record" });
      target.version += 1;
      respondJson(response, 200, { version: target.version });
    });
    return true;
  }
  const product = /^\/api\/v1\/products\/([^/]+)$/.exec(path);
  const isPricing = path.startsWith("/api/v1/pricing/") || path === "/api/v1/staff" || path === "/api/v1/product-categories" || (product !== null && productById.has(product[1]));
  if (!isPricing) return false;

  const caller = callerOf(request);
  if (caller.refused !== undefined) {
    respondJson(response, caller.refused, { error: caller.error });
    return true;
  }
  const role = caller.account.role_code;
  const editor = role === "sales_manager" && switches.mode === "editor";
  const reader = role === "sales_manager";

  if (path === "/api/v1/staff" && request.method === "GET") {
    if (!reader) return forbidden(respondJson, response), true;
    respondJson(response, 200, [{ user_id: "demo-account-sales-manager", display_name: "Sales manager (demo)", email: "x@demo.invalid", role_code: "sales_manager", role_name: "Sales Manager", tier: 2, scope: "BRANCH", branch_ids: [] }]);
    return true;
  }
  if (product !== null) {
    // Names off stands for a core whose product read the panel cannot reach: the log then
    // lists the confirmation's products by their raw ids, the widest text the log holds.
    if (!switches.productNames) return respondJson(response, 404, { error: "product names are off" }), true;
    const p = productById.get(product[1]);
    respondJson(response, 200, { id: p.id, sku: p.sku, name: p.name });
    return true;
  }

  const isRead = request.method === "GET";
  const isLog = path === "/api/v1/pricing/audit";
  if (!reader || (isLog && !switches.auditAllowed)) return forbidden(respondJson, response), true;
  if (!isRead && !editor) return forbidden(respondJson, response), true;

  // A one shot failure the suite asks for, to prove a failed save is shown and never silent.
  const failing = (kind) => {
    if (switches.failNext !== null && switches.failNext.kind === kind) {
      const f = switches.failNext;
      switches.failNext = null;
      respondJson(response, f.status ?? 500, { error: f.message ?? "pricing: the server could not do that" });
      return true;
    }
    return false;
  };

  const userId = caller.account.user_id;
  let m;

  // ── the catalogue's categories (GET /api/v1/product-categories, the picker's own read) ──
  if (path === "/api/v1/product-categories" && isRead) {
    if (failing("categories")) return true;
    const names = { [CAT.pavers]: ["PAV", "Pavers"], [CAT.walls]: ["WAL", "Retaining Walls"], [CAT.slabs]: ["SLB", "Slabs & Steps"], [CAT.aggregates]: ["AGG", "Aggregates"], [CAT.accessories]: ["ACC", "Accessories"], [CAT.bulk]: ["BLK", "Bulk"] };
    const rows = [...world.catalogue].map((id) => ({ id, code: names[id]?.[0] ?? id.slice(0, 6), name: names[id]?.[1] ?? id, is_active: true }));
    respondJson(response, 200, { items: rows, next_cursor: null });
    return true;
  }

  // ── the category margin policy ──
  if (path === "/api/v1/pricing/category-policies" && isRead) {
    if (failing("list")) return true;
    respondJson(response, 200, asList([...world.policies.values()]));
    return true;
  }
  if ((m = /^\/api\/v1\/pricing\/category-policies\/([^/]+)$/.exec(path)) !== null && request.method === "PUT") {
    readBody(request, response, (body) => {
      if (failing("save")) return;
      const id = m[1];
      const cur = world.policies.get(id);
      if (!validBps(body.margin_target_bps) || !validBps(body.margin_floor_bps)) return respondJson(response, 422, { error: "margin must be between 0% and 100%" });
      if (body.margin_floor_bps > body.margin_target_bps) return respondJson(response, 422, { error: "margin floor must be at or below the target" });
      // The policy's category is a foreign key to product_categories: the server maps the violation to a 422 with this sentence.
      if (!world.catalogue.has(id)) return respondJson(response, 422, { error: "references a nonexistent customer, product, category or branch" });
      const next = { category_id: id, margin_target_bps: body.margin_target_bps, margin_floor_bps: body.margin_floor_bps, margin_enforcement_disabled: body.margin_enforcement_disabled === true };
      // The server's PUT creates or replaces: a category with no policy yet is created.
      for (const field of ["margin_target_bps", "margin_floor_bps", "margin_enforcement_disabled"]) {
        if (cur === undefined) logRow(userId, "category_policy", id, "CREATE", field, "", String(next[field]), { type: "category", id });
        else if (cur[field] !== next[field]) logRow(userId, "category_policy", id, "UPDATE", field, String(cur[field]), String(next[field]), { type: "category", id });
      }
      world.policies.set(id, next);
      world.ledger.writes.push({ kind: "policy", id });
      respondJson(response, 200, next);
    });
    return true;
  }

  // ── rules ──
  if (path === "/api/v1/pricing/rules" && isRead) {
    if (failing("list")) return true;
    const scope = url.searchParams.get("scope");
    const type = url.searchParams.get("customer_type");
    const cat = url.searchParams.get("category_id");
    const rows = world.rules.filter((r) => (scope === null || r.scope === scope) && (type === null || r.customer_type === type) && (cat === null || r.category_id === cat));
    respondJson(response, 200, asList(rows.sort((a, b) => b.priority - a.priority).map(serveRule)));
    return true;
  }
  if (path === "/api/v1/pricing/rules" && request.method === "POST") {
    readBody(request, response, (body) => {
      if (failing("save")) return;
      const bad = validateRule(body);
      if (bad !== null) return respondJson(response, 422, { error: bad });
      const r = { ...rule(body.scope, {}, body.calc_type, {}, body.price_uom, body.priority, body.detail_text), customer_id: body.customer_id, customer_type: body.customer_type, category_id: body.category_id, product_id: body.product_id, branch_id: body.branch_id, price_cents: body.price_cents, discount_bps: body.discount_bps, markup_bps: body.markup_bps };
      world.rules.push(r);
      logRow(userId, "pricing_rule", r.id, "CREATE", undefined, undefined, undefined, refOfRule(r));
      world.ledger.writes.push({ kind: "rule", id: r.id });
      respondJson(response, 201, { id: r.id, version: 1 });
    });
    return true;
  }
  if ((m = /^\/api\/v1\/pricing\/rules\/([^/]+)$/.exec(path)) !== null) {
    const r = world.rules.find((x) => x.id === m[1]);
    if (isRead) {
      if (r === undefined) return respondJson(response, 404, { error: "not found" }), true;
      respondJson(response, 200, serveRule(r));
      return true;
    }
    if (request.method === "PUT") {
      readBody(request, response, (body) => {
        if (failing("save")) return;
        if (r === undefined) return respondJson(response, 404, { error: "not found" });
        if (body.version !== r.version) return respondJson(response, 409, { error: "version conflict" });
        const next = { ...r, ...body };
        delete next.version;
        const bad = validateRule(next);
        if (bad !== null) return respondJson(response, 422, { error: bad });
        for (const field of ["discount_bps", "markup_bps", "price_cents", "priority", "detail_text", "price_uom"]) {
          if (String(r[field]) !== String(next[field])) logRow(userId, "pricing_rule", r.id, "UPDATE", field, String(r[field] ?? ""), String(next[field] ?? ""), refOfRule(next));
        }
        const from = storedDate(body.valid_from, false);
        const to = storedDate(body.valid_to, true);
        Object.assign(r, next, { version: r.version + 1, valid_from: from === undefined ? (r.valid_from ?? null) : from, valid_to: to === undefined ? (r.valid_to ?? null) : to });
        world.ledger.writes.push({ kind: "rule", id: r.id });
        respondJson(response, 200, r);
      });
      return true;
    }
    if (request.method === "DELETE") {
      if (r === undefined) return respondJson(response, 404, { error: "not found" }), true;
      if (Number(url.searchParams.get("version")) !== r.version) return respondJson(response, 409, { error: "version conflict" }), true;
      r.is_active = false;
      r.version += 1;
      logRow(userId, "pricing_rule", r.id, "DELETE", "is_active", "true", "false", refOfRule(r));
      respondJson(response, 200, { status: "retired" });
      return true;
    }
  }

  // ── customer prices ──
  if ((m = /^\/api\/v1\/pricing\/customers\/([^/]+)\/overrides$/.exec(path)) !== null) {
    const customerId = canonCustomer(m[1]);
    if (isRead) {
      if (failing("list")) return true;
      respondJson(response, 200, asList(world.overrides.filter((o) => o.customer_id === customerId)));
      return true;
    }
    if (request.method === "POST") {
      readBody(request, response, (body) => {
        if (failing("save")) return;
        if (!["PCT_OFF_LIST", "MARKUP_ON_COST"].includes(body.calc_type)) return respondJson(response, 422, { error: "ABSOLUTE is forbidden on a customer override" });
        if (world.overrides.some((o) => o.customer_id === customerId && o.category_id === body.category_id)) return respondJson(response, 409, { error: "an override already exists for this customer + category" });
        const o = { id: newId("e"), customer_id: customerId, category_id: body.category_id, calc_type: body.calc_type, price_uom: body.price_uom, notes: body.notes ?? "", version: 1, ...(body.discount_bps === undefined ? {} : { discount_bps: body.discount_bps }), ...(body.markup_bps === undefined ? {} : { markup_bps: body.markup_bps }) };
        world.overrides.push(o);
        logRow(userId, "customer_override", o.id, "CREATE", undefined, undefined, undefined, { type: "customer", id: uuidOfCustomer(customerId) });
        world.ledger.writes.push({ kind: "override", id: o.id });
        respondJson(response, 201, { id: o.id, version: 1 });
      });
      return true;
    }
  }
  if ((m = /^\/api\/v1\/pricing\/overrides\/([^/]+)$/.exec(path)) !== null) {
    const o = world.overrides.find((x) => x.id === m[1]);
    if (request.method === "PUT") {
      readBody(request, response, (body) => {
        if (failing("save")) return;
        if (o === undefined) return respondJson(response, 404, { error: "not found" });
        if (body.version !== o.version) return respondJson(response, 409, { error: "version conflict" });
        for (const field of ["discount_bps", "markup_bps", "notes", "price_uom"]) {
          if (String(o[field] ?? "") !== String(body[field] ?? "")) logRow(userId, "customer_override", o.id, "UPDATE", field, String(o[field] ?? ""), String(body[field] ?? ""), { type: "customer", id: uuidOfCustomer(o.customer_id) });
        }
        delete o.discount_bps;
        delete o.markup_bps;
        Object.assign(o, { calc_type: body.calc_type, price_uom: body.price_uom, notes: body.notes ?? "", ...(body.discount_bps === undefined ? {} : { discount_bps: body.discount_bps }), ...(body.markup_bps === undefined ? {} : { markup_bps: body.markup_bps }), version: o.version + 1 });
        world.ledger.writes.push({ kind: "override", id: o.id });
        respondJson(response, 200, o);
      });
      return true;
    }
    if (request.method === "DELETE") {
      if (o === undefined) return respondJson(response, 404, { error: "not found" }), true;
      if (Number(url.searchParams.get("version")) !== o.version) return respondJson(response, 409, { error: "version conflict" }), true;
      world.overrides = world.overrides.filter((x) => x.id !== o.id);
      logRow(userId, "customer_override", o.id, "DELETE", undefined, undefined, undefined, { type: "customer", id: uuidOfCustomer(o.customer_id) });
      respondJson(response, 200, { status: "deleted" });
      return true;
    }
  }

  // ── quantity breaks ──
  if (path === "/api/v1/pricing/quantity-breaks" && isRead) {
    if (failing("list")) return true;
    respondJson(response, 200, asList(world.breaks));
    return true;
  }
  if (path === "/api/v1/pricing/quantity-breaks" && request.method === "POST") {
    readBody(request, response, (body) => {
      if (failing("save")) return;
      if (world.breaks.some((b) => b.code === body.code)) return respondJson(response, 409, { error: "a quantity break with this code already exists" });
      if ((body.product_id === "") === (body.category_id === "")) return respondJson(response, 422, { error: "a quantity break must be scoped to EXACTLY ONE of product or category" });
      const b = breakRow(body.code, body.name, { ...(body.product_id === "" ? {} : { product_id: body.product_id }), ...(body.category_id === "" ? {} : { category_id: body.category_id }) }, body.threshold_uom, body.items);
      world.breaks.push(b);
      logRow(userId, "quantity_break", b.id, "CREATE", undefined, undefined, undefined, b.category_id === undefined ? { type: "product", id: b.product_id } : { type: "category", id: b.category_id });
      world.ledger.writes.push({ kind: "break", id: b.id });
      respondJson(response, 201, { id: b.id, version: 1 });
    });
    return true;
  }
  if ((m = /^\/api\/v1\/pricing\/quantity-breaks\/([^/]+)$/.exec(path)) !== null) {
    const b = world.breaks.find((x) => x.id === m[1]);
    if (isRead) {
      if (b === undefined) return respondJson(response, 404, { error: "not found" }), true;
      respondJson(response, 200, b);
      return true;
    }
    if (request.method === "PUT") {
      readBody(request, response, (body) => {
        if (failing("save")) return;
        if (b === undefined) return respondJson(response, 404, { error: "not found" });
        if (body.version !== b.version) return respondJson(response, 409, { error: "version conflict" });
        Object.assign(b, { code: body.code, name: body.name, threshold_uom: body.threshold_uom, items: body.items, version: b.version + 1 });
        delete b.product_id;
        delete b.category_id;
        if (body.product_id !== "") b.product_id = body.product_id;
        if (body.category_id !== "") b.category_id = body.category_id;
        logRow(userId, "quantity_break", b.id, "UPDATE", "items", undefined, undefined, b.category_id === undefined ? { type: "product", id: b.product_id } : { type: "category", id: b.category_id });
        world.ledger.writes.push({ kind: "break", id: b.id });
        respondJson(response, 200, b);
      });
      return true;
    }
    if (request.method === "DELETE") {
      if (b === undefined) return respondJson(response, 404, { error: "not found" }), true;
      if (Number(url.searchParams.get("version")) !== b.version) return respondJson(response, 409, { error: "version conflict" }), true;
      b.is_active = false;
      b.version += 1;
      logRow(userId, "quantity_break", b.id, "DELETE", "is_active", "true", "false", undefined);
      respondJson(response, 200, { status: "retired" });
      return true;
    }
  }

  // ── the log ──
  if (isLog && isRead) {
    if (failing("list")) return true;
    const entity = url.searchParams.get("entity_type");
    const action = url.searchParams.get("action");
    const limit = Number(url.searchParams.get("limit") ?? 100);
    const rows = world.audit.filter((r) => (entity === null || r.entity_type === entity) && (action === null || r.action === action));
    respondJson(response, 200, asList(rows.slice(0, limit)));
    return true;
  }

  // ── the floor warning ──
  if (path === "/api/v1/pricing/floor-preview" && request.method === "POST") {
    readBody(request, response, (body) => {
      if (failing("preview")) return;
      world.ledger.previews.push(body);
      const bad = body.kind === "rule" && body.rule !== undefined ? validateRule(body.rule) : null;
      if (bad !== null) return respondJson(response, 422, { error: bad });
      respondJson(response, 200, { ...floorPreview(body), ...(switches.forcePreview ?? {}) });
    });
    return true;
  }
  if (path === "/api/v1/pricing/floor-confirmations" && request.method === "POST") {
    readBody(request, response, (body) => {
      if (switches.failConfirm) {
        switches.failConfirm = false;
        return respondJson(response, 500, { error: "internal error" });
      }
      if (!Array.isArray(body.product_ids) || body.product_ids.length === 0) return respondJson(response, 400, { error: "a confirmation names at least one product" });
      const refType = body.ref_entity_type ?? "";
      const refId = body.ref_entity_id ?? "";
      if ((refType === "") !== (refId === "") || (refType !== "" && !["product", "customer", "category", "quote", "order"].includes(refType))) {
        return respondJson(response, 400, { error: "ref_entity_type must be one of product, customer, category, quote, order and come with ref_entity_id" });
      }
      const known = { pricing_rule: world.rules.some((r) => r.id === body.entity_id), customer_override: world.overrides.some((o) => o.id === body.entity_id), quantity_break: world.breaks.some((b) => b.id === body.entity_id), category_policy: world.policies.has(body.entity_id) };
      if (known[body.entity_type] !== true) return respondJson(response, 404, { error: "not found" });
      world.ledger.confirmations.push(body);
      logRow(userId, body.entity_type, body.entity_id, "UPDATE", "floor_warning_confirmed", undefined, `the editor confirmed a floor warning naming ${body.product_ids.length} product${body.product_ids.length === 1 ? "" : "s"}: ${body.product_ids.join(", ")}`, body.ref_entity_type === undefined ? undefined : { type: body.ref_entity_type, id: body.ref_entity_id });
      respondJson(response, 200, { status: "recorded" });
    });
    return true;
  }

  respondJson(response, 404, { error: `no pricing stub route for ${request.method} ${path}` });
  return true;
}

function refOfRule(r) {
  if (r.scope === "CUSTOMER_PRODUCT" && r.product_id) return { type: "product", id: r.product_id };
  if (r.category_id) return { type: "category", id: r.category_id };
  return undefined;
}

function validateRule(r) {
  if (!["CUSTOMER_PRODUCT", "CUSTOMER_CATEGORY", "TIER_CATEGORY", "TIER_DEFAULT"].includes(r.scope)) return "scope must be one of CUSTOMER_PRODUCT, CUSTOMER_CATEGORY, TIER_CATEGORY, TIER_DEFAULT";
  if (r.scope !== "CUSTOMER_PRODUCT" && r.calc_type === "ABSOLUTE") return "ABSOLUTE price is only allowed on CUSTOMER_PRODUCT (discount-percent-only rule)";
  if (r.calc_type === "PCT_OFF_LIST" && !validBps(r.discount_bps)) return "discount_bps must be between 0 and 10000 basis points";
  if (r.calc_type === "PCT_OFF_LIST" && (r.discount_bps === undefined || r.discount_bps === null)) return "PCT_OFF_LIST rule requires discount_bps";
  return null;
}
