// Dealer Admin's local stub: the demo way in, and the dealer's staff world
// answered in the core's shapes (the staff register, the roles catalog, the
// branches, the platform hotkey rosters, the audit trail, and the pricing
// governance routes the Pricing surface reads and writes). It is a
// development and test double only; the real routes are the core's, and the
// staff identity admin family is contract-only there until the auth lane
// lands it.
import { createServer } from "node:http";

const port = Number(process.env.DA_STUB_PORT ?? 56610);

// The real flows route lists the closed table's logins; this app's door
// draws a card per flow it answers here. Two: the dealer admin (the
// general manager persona the admin surfaces answer), and inside sales for
// the refusal walk (a frontline caller is refused the staff read).
const FLOWS = [
  { flow: "dealer-admin", role_code: "general_manager", label: "Dealer admin" },
  { flow: "inside-sales", role_code: "inside_sales", label: "Inside sales" },
];

const USERS = {
  "dealer-admin": {
    user_id: "da110000-0000-4000-9000-000000000001",
    email: "dealer.admin@hh-demo.invalid",
    display_name: "Dealer admin (demo)",
    role_code: "general_manager",
    role_name: "General Manager",
    tier: 1,
    scope: "GLOBAL",
  },
  "inside-sales": {
    user_id: "da110000-0000-4000-9000-000000000002",
    email: "inside.sales@hh-demo.invalid",
    display_name: "Inside sales (demo)",
    role_code: "inside_sales",
    role_name: "Inside Sales",
    tier: 3,
    scope: "BRANCH",
  },
};

// The branches (GLS-TENANT-r3): Point Loma, the main yard, and Julian.
const BRANCHES = [
  {
    id: "5d1b3f6e-0000-4000-9000-0000000000pl",
    code: "PLM",
    name: "Gable Landscape Supply - Point Loma",
    is_active: true,
  },
  {
    id: "5d1b3f6e-0000-4000-9000-0000000000ju",
    code: "JUL",
    name: "Gable Landscape Supply - Julian",
    is_active: true,
  },
];
const PLM = BRANCHES[0].id;
const JUL = BRANCHES[1].id;

// The roles catalog, the core's own list (staffcontext/rbac.go AllRoles):
// the platform role and the thirteen business roles.
const ROLES = [
  { code: "SUPER_ADMIN", name: "Platform Administrator", tier: 1, scope: "GLOBAL" },
  { code: "GENERAL_MANAGER", name: "General Manager", tier: 1, scope: "GLOBAL" },
  { code: "FINANCIAL_CONTROLLER", name: "Financial Controller", tier: 1, scope: "GLOBAL" },
  { code: "HR", name: "HR", tier: 1, scope: "GLOBAL" },
  { code: "BRANCH_MANAGER", name: "Branch Manager", tier: 2, scope: "BRANCH" },
  { code: "PROCUREMENT_MANAGER", name: "Procurement Manager", tier: 2, scope: "BRANCH" },
  { code: "SALES_MANAGER", name: "Sales Manager", tier: 2, scope: "BRANCH" },
  { code: "YARD_MANAGER", name: "Yard Manager", tier: 2, scope: "BRANCH" },
  { code: "LOGISTICS_MANAGER", name: "Logistics Manager", tier: 2, scope: "BRANCH" },
  { code: "PAYABLES_RECEIVABLES", name: "Payables/Receivables", tier: 2, scope: "GLOBAL" },
  { code: "INSIDE_SALES", name: "Inside Sales", tier: 3, scope: "BRANCH" },
  { code: "OUTSIDE_SALES", name: "Outside Sales", tier: 3, scope: "BRANCH" },
  { code: "YARD_TEAM", name: "Yard Team", tier: 3, scope: "BRANCH" },
  { code: "DRIVERS", name: "Drivers", tier: 3, scope: "BRANCH" },
];

// The staff register: leadership, a frontline pair with branch grants, and
// the platform role row that renders read-only.
function buildStaff() {
  return [
    {
      user_id: USERS["dealer-admin"].user_id,
      email: USERS["dealer-admin"].email,
      display_name: USERS["dealer-admin"].display_name,
      role_code: "GENERAL_MANAGER",
      role_name: "General Manager",
      tier: 1,
      scope: "GLOBAL",
      branch_ids: [],
    },
    {
      user_id: "da110000-0000-4000-9000-0000000000hr",
      email: "hr@hh-demo.invalid",
      display_name: "HR (demo)",
      role_code: "HR",
      role_name: "HR",
      tier: 1,
      scope: "GLOBAL",
      branch_ids: [],
    },
    {
      user_id: USERS["inside-sales"].user_id,
      email: USERS["inside-sales"].email,
      display_name: USERS["inside-sales"].display_name,
      role_code: "INSIDE_SALES",
      role_name: "Inside Sales",
      tier: 3,
      scope: "BRANCH",
      branch_ids: [PLM],
    },
    {
      user_id: "da110000-0000-4000-9000-0000000000yt",
      email: "yard.team@hh-demo.invalid",
      display_name: "Yard team (demo)",
      role_code: "YARD_TEAM",
      role_name: "Yard Team",
      tier: 3,
      scope: "BRANCH",
      branch_ids: [PLM, JUL],
    },
    {
      user_id: "da110000-0000-4000-9000-0000000000sa",
      email: "platform.admin@hh-demo.invalid",
      display_name: "Platform admin (demo)",
      role_code: "super_admin",
      role_name: "Platform Administrator",
      tier: 1,
      scope: "GLOBAL",
      branch_ids: [],
    },
  ];
}

// A token with the shape the app reads an expiry from: three parts, the
// middle one base64url JSON carrying sub and exp. The signature is a stub.
function mintToken(sub) {
  const part = (value) => Buffer.from(JSON.stringify(value), "utf8").toString("base64url");
  const issuedAtSeconds = Math.floor(Date.now() / 1000);
  const payload = { iss: "hh-demo-stub", sub, aud: "staff", iat: issuedAtSeconds, exp: issuedAtSeconds + 3600 };
  return `${part({ alg: "ES256", typ: "JWT" })}.${part(payload)}.c3R1Yi1zaWduYXR1cmUtYnl0ZXM`;
}

function json(res, data, status = 200) {
  res.writeHead(status, { "Content-Type": "application/json" });
  res.end(JSON.stringify(data));
}

// The core's error envelope: {"error": sentence}. The role gate's shared
// Forbidden body is exactly {"error":"Forbidden"}.
function refuse(res, status, message) {
  return json(res, { error: message }, status);
}

function notFound(res) {
  return refuse(res, 404, "not found");
}

// A minimal multipart/form-data reader for the price file road: text fields
// and one file part, by boundary.
function parseMultipart(body, contentType) {
  const match = contentType.match(/boundary=([^;]+)/);
  if (!match) return {};
  const boundary = Buffer.from("--" + match[1]);
  const parts = [];
  let start = body.indexOf(boundary);
  while (start !== -1) {
    const next = body.indexOf(boundary, start + boundary.length);
    if (next === -1) break;
    parts.push(body.slice(start + boundary.length, next));
    start = next;
  }
  const form = {};
  for (const part of parts) {
    const headerEnd = part.indexOf("\r\n\r\n");
    if (headerEnd === -1) continue;
    const headers = part.slice(0, headerEnd).toString("utf8");
    const content = part.slice(headerEnd + 4, part.length - 2);
    const nameMatch = headers.match(/name="([^"]+)"/);
    const filenameMatch = headers.match(/filename="([^"]*)"/);
    if (nameMatch === null) continue;
    if (filenameMatch !== null) {
      form.file = content;
      form.fileFilename = filenameMatch[1];
    } else {
      form[nameMatch[1]] = content.toString("utf8");
    }
  }
  return form;
}

function readBody(req) {
  return new Promise((resolve) => {
    const chunks = [];
    req.on("data", (c) => chunks.push(c));
    req.on("end", () => resolve(Buffer.concat(chunks)));
  });
}

// The configurable surfaces kit's world: the dealer's curation keyed
// surface|profile (empty everywhere, every cell answers registry default
// until a save), and each caller's own override and workspace row keyed by
// user id. Both start empty, which is the demo's real state.
const PROFILE_KINDS = ["panel", "column", "chip", "action"];
const PROFILES = [
  "counter", "yard", "driver", "manager", "admin",
  "manager.branch", "manager.sales", "manager.yard", "manager.logistics",
  "manager.procurement", "manager.payables", "manager.controller", "manager.general",
];
const SETTINGS_ADMIN_READ_WORDS =
  "settings: only the General Manager, the Financial Controller, HR or the Platform Administrator see another profile's dealer screen settings";
const SETTINGS_ADMIN_WRITE_WORDS =
  "settings: only the General Manager, the Financial Controller, HR or the Platform Administrator change the dealer screen settings";

let surfaces;
let mine;

function buildSurfaces() {
  return new Map();
}

function buildMine() {
  return new Map();
}

function itemsOf(map, surface, profile) {
  return (map.get(surface + "|" + profile) ?? []).slice();
}

function myWorld(userId) {
  let world = mine.get(userId);
  if (world === undefined) {
    world = { items: new Map(), workspace: new Map() };
    mine.set(userId, world);
  }
  return world;
}

// The kit's validators, in the core's own words, so a refused shape reads
// the same on the stub as it does on the real core.
function validateItems(items) {
  if (!Array.isArray(items) || items.length === 0) {
    return "settings: the item set is empty, to reset, use DELETE";
  }
  if (items.length > 128) {
    return "settings: at most 128 items per write";
  }
  const seen = new Set();
  for (const it of items) {
    if (!PROFILE_KINDS.includes(it?.kind)) {
      return "settings: item kind must be panel, column, chip or action";
    }
    if (!/^[a-z0-9][a-z0-9._-]{0,63}$/.test(String(it?.id ?? ""))) {
      return `settings: item id ${JSON.stringify(String(it?.id ?? ""))} must be 1-64 lowercase letters, digits, dots, dashes or underscores`;
    }
    if (!Number.isInteger(it?.position) || it.position < 0) {
      return "settings: item positions cannot be negative";
    }
    const key = it.kind + "\u0000" + it.id;
    if (seen.has(key)) {
      return `settings: item ${it.kind}/${it.id} appears twice`;
    }
    seen.add(key);
  }
  return "";
}

let staff;
let hotkeys;
let audit;
let scenario;
let log;
let assist;
let printJobs;
let products;

// The catalog world: two invented Gable products, the unit catalogue and the
// categories. The writes carry the core's own gates and refusals: the
// attribute PUT and the picture routes are the manager gate (tier 1 to 2), an
// unknown attribute key is named by the column, and the picture's content
// type must be one the contract declares.
const PRODUCT_TEXT_KEYS = new Set([
  "name",
  "description",
  "manufacturer",
  "collection",
  "color",
  "finish",
  "application",
  "color_family",
  "texture",
  "product_size",
  "sold_by",
  "dimensions_lwh",
  "is_featured",
  "is_special_order",
  "contractor_pricing_event_discount",
  "full_skid_quantity",
  "layer_quantity",
  "pallet_count",
  "minimum_order_qty",
  "metadata",
]);

function buildProducts() {
  return [
    {
      id: "3a000000-0000-4000-9000-000000000001",
      sku: "PAV-60-CH",
      name: "Charcoal 60 paver",
      description: "A 60 mm paver in charcoal.",
      manufacturer: "Techo-Bloc",
      collection: "Blu",
      color: "Charcoal",
      color_family: "Grey",
      finish: "smooth",
      application: "patios and walkways",
      texture: "",
      product_size: "60 mm",
      sold_by: "SF",
      base_uom: "SF",
      dimensions_lwh: "",
      is_featured: false,
      is_special_order: false,
      contractor_pricing_event_discount: false,
      full_skid_quantity: null,
      layer_quantity: null,
      pallet_count: null,
      minimum_order_qty: "120",
      is_active: true,
      metadata: {},
      list_price_cents: 488,
      list_price_uom: "SF",
    },
    {
      id: "3a000000-0000-4000-9000-000000000002",
      sku: "WALL-SEM-BR",
      name: "Seminole wall block",
      description: "A split face wall block for garden walls.",
      manufacturer: "Risi",
      collection: "Seminole",
      color: "Brown",
      color_family: "Brown",
      finish: "split",
      application: "garden walls",
      texture: "",
      product_size: "8 inch",
      sold_by: "EA",
      base_uom: "EA",
      dimensions_lwh: "",
      is_featured: false,
      is_special_order: false,
      contractor_pricing_event_discount: false,
      full_skid_quantity: 144,
      layer_quantity: 36,
      pallet_count: 4,
      minimum_order_qty: null,
      is_active: true,
      metadata: {},
      list_price_cents: null,
      list_price_uom: "",
    },
  ];
}

// The categories read is the pricing world's (its three categories, below); the
// catalog's finder and cards read the same route.
const UOMS = [
  { code: "SF", name: "square feet", dimension: "area", is_active: true },
  { code: "CYD", name: "cubic yards", dimension: "volume", is_active: true },
  { code: "EA", name: "each", dimension: "count", is_active: true },
  { code: "TON", name: "tons", dimension: "weight", is_active: true },
];

// The print queue: one sale's receipt history, a first print and a reprint,
// and one label nobody has printed. The reference is the stub sale's own id,
// the same document the audit world's trail spans.
function buildPrintJobs() {
  const ago = (minutes) => new Date(Date.now() - minutes * 60 * 1000).toISOString();
  return [
    {
      id: "9a000000-0000-4000-9000-000000000001",
      doc_type: "RECEIPT",
      ref_id: SALE_REF,
      branch_id: BRANCHES[0].id,
      render_target: "BROWSER",
      status: "PRINTED",
      requested_by: USERS["dealer-admin"].user_id,
      requested_at: ago(40),
      printed_at: ago(39),
    },
    {
      id: "9a000000-0000-4000-9000-000000000002",
      doc_type: "RECEIPT",
      ref_id: SALE_REF,
      branch_id: BRANCHES[0].id,
      render_target: "BROWSER",
      status: "QUEUED",
      requested_by: USERS["dealer-admin"].user_id,
      requested_at: ago(5),
    },
    {
      id: "9a000000-0000-4000-9000-000000000003",
      doc_type: "LABEL_ZPL",
      ref_id: "0f000000-0000-4000-9000-0000000000s2",
      branch_id: BRANCHES[1].id,
      render_target: "ZPL",
      status: "FAILED",
      failure_reason: "the label printer was not reachable",
      requested_at: ago(30),
    },
  ];
}

const SALE_REF = "0f000000-0000-4000-9000-000000000001";
const DOC_TYPES = new Set(["RECEIPT", "LABEL_ZPL", "DELIVERY_DOC"]);

// The assist admin world, the core's own shapes: the spend report zero filled
// from the staff list (every active person appears, the ones who never opened
// the assistant included), the budgets with one role default and one person's
// override, and the four settings keys with the key's NAME and a no.
function buildAssist() {
  return {
    role_budgets: { GENERAL_MANAGER: 500000 },
    user_overrides: { [USERS["inside-sales"].user_id]: 0 },
    default_budget: 60000,
    exchanges: {
      [USERS["dealer-admin"].user_id]: { exchanges: 4, tokens_in: 300, tokens_out: 200 },
    },
    settings: {
      settings: { provider: "stub", model: "hos-stub-1", chat_enabled: true, spend_visible_to_non_admin: false },
      key_env_var: "HH_ASSIST_API_KEY",
      key_present: false,
    },
  };
}
let payments;
let posPolicy;
let policyHistory;
let warrantyPolicy;
let warrantyVendors;
let warrantyAgreements;

// The counter's org wide policy, the demo's real defaults (credit at the
// till is on, the restocking fee is the seeded 250 basis points, the
// workspace split is the kit's default 58).
function buildPayments() {
  return { processor: "run_payments", merchant_id: "gls-demo-rail", currency: "usd" };
}

function buildPosPolicy() {
  return {
    branch_id: "",
    cod_enabled: true,
    restocking_fee_bps: 250,
    notes: "",
    line_presentation: "LINES",
    quick_chips_enabled: true,
    workspace_split_pct: 58,
    workspace_arrangement: "LOOKUP_LEFT",
    lookup_presentation: "CARDS",
    margin_visibility: "MANAGER_UP",
    quick_chip_set: [],
  };
}

// The walk change 6 warranty world: the claim threshold at 0396's seed
// ($50.00), and the demo's four suppliers in the boards' own states. A
// vendor with no row answers the default claim_by_claim, the way the core's
// read does.
function buildWarrantyPolicy() {
  return { branch_id: "", claim_threshold_cents: 5000 };
}

function buildWarrantyVendors() {
  return [
    { id: "aa000000-0000-4000-9000-000000000001", vendor_no: "V-COPPER", name: "Copperline Pavers" },
    { id: "aa000000-0000-4000-9000-000000000002", vendor_no: "V-GRANITE", name: "Granite Run Quarry" },
    { id: "aa000000-0000-4000-9000-000000000003", vendor_no: "V-HERON", name: "Heronfield Stone" },
    { id: "aa000000-0000-4000-9000-000000000004", vendor_no: "V-PRAIRIE", name: "Prairie Bend Soils" },
  ];
}

function buildWarrantyAgreements() {
  return {
    "aa000000-0000-4000-9000-000000000002": { agreement_kind: "periodic_credit_note", period: "quarterly", allowance_bp: 0 },
    "aa000000-0000-4000-9000-000000000003": { agreement_kind: "fixed_allowance", period: "quarterly", allowance_bp: 100 },
    "aa000000-0000-4000-9000-000000000004": { agreement_kind: "periodic_credit_note", period: "monthly", allowance_bp: 0 },
  };
}

function effectiveAgreement(vendor) {
  const row = warrantyAgreements[vendor.id];
  const head = { vendor_id: vendor.id, vendor_no: vendor.vendor_no, vendor_name: vendor.name };
  if (!row || row.agreement_kind === "claim_by_claim") {
    return { ...head, agreement_kind: "claim_by_claim", allowance_bp: 0 };
  }
  return { ...head, agreement_kind: row.agreement_kind, period: row.period, allowance_bp: row.allowance_bp };
}

// The agreement write's validators, in the core's own words.
function validateAgreement(b) {
  const kind = String(b.agreement_kind ?? "");
  if (kind !== "claim_by_claim" && kind !== "periodic_credit_note" && kind !== "fixed_allowance") {
    return "warranty: agreement_kind must be one of claim_by_claim, periodic_credit_note, fixed_allowance";
  }
  if (kind !== "claim_by_claim") {
    const period = String(b.period ?? "");
    if (period !== "monthly" && period !== "quarterly") {
      return `warranty: period must be monthly or quarterly for a ${kind} agreement`;
    }
    const bp = Number(b.allowance_bp ?? 0);
    if (!Number.isInteger(bp) || bp < 0 || bp > 10000) {
      return "warranty: allowance_bp must be between 0 and 10000 (basis points of the vendor's purchases)";
    }
  }
  return "";
}

// The validators the pos policy write carries, in the core's own words.
function validatePosPolicy(b) {
  const line = String(b.line_presentation ?? "LINES");
  if (line !== "" && line !== "LINES" && line !== "CARDS") {
    return "pos: line_presentation must be LINES or CARDS";
  }
  const split = Number(b.workspace_split_pct ?? 58);
  if (split !== 0 && (split < 20 || split > 80)) {
    return "pos: workspace split_pct must be between 20 and 80";
  }
  const arrangement = String(b.workspace_arrangement ?? "LOOKUP_LEFT");
  if (arrangement !== "" && arrangement !== "LOOKUP_LEFT" && arrangement !== "SALE_LEFT") {
    return "pos: workspace arrangement must be LOOKUP_LEFT or SALE_LEFT";
  }
  const lookup = String(b.lookup_presentation ?? "CARDS");
  if (lookup !== "" && lookup !== "CARDS" && lookup !== "ROWS") {
    return "pos: lookup_presentation must be CARDS or ROWS";
  }
  const margin = String(b.margin_visibility ?? "MANAGER_UP");
  if (margin !== "" && margin !== "MANAGER_UP" && margin !== "ALL_STAFF" && margin !== "HIDDEN") {
    return "pos: margin_visibility must be MANAGER_UP, ALL_STAFF or HIDDEN";
  }
  const chips = Array.isArray(b.quick_chip_set) ? b.quick_chip_set : [];
  if (chips.length > 8) {
    return "pos: quick_chip_set holds at most 8 categories";
  }
  return "";
}

function normalizePosPolicy(b) {
  const split = Number(b.workspace_split_pct ?? 58);
  return {
    cod_enabled: b.cod_enabled === true,
    restocking_fee_bps: Number(b.restocking_fee_bps ?? 0),
    notes: String(b.notes ?? ""),
    line_presentation: String(b.line_presentation ?? "") === "" ? "LINES" : String(b.line_presentation),
    quick_chips_enabled: b.quick_chips_enabled === true,
    workspace_split_pct: split === 0 ? 58 : split,
    workspace_arrangement: String(b.workspace_arrangement ?? "") === "" ? "LOOKUP_LEFT" : String(b.workspace_arrangement),
    lookup_presentation: String(b.lookup_presentation ?? "") === "" ? "CARDS" : String(b.lookup_presentation),
    margin_visibility: String(b.margin_visibility ?? "") === "" ? "MANAGER_UP" : String(b.margin_visibility),
    quick_chip_set: Array.isArray(b.quick_chip_set) ? b.quick_chip_set : [],
  };
}

// The walkthrough's two chapters for the live scenarios: the counter's and
// the yard's, each with two steps, the shape the tour read serves.
function buildTourChapters() {
  return [
    {
      chapter_key: "counter.first-sale",
      position: 0,
      title: "Your first sale at the counter",
      promise: "Ring a sale, take a tender, hand over the receipt.",
      role: "counter",
      route: "#sales/pos",
      steps: [
        { step_key: "browse", position: 0, route: "#sales/pos", anchor: "testid:pos-browse", line: "Find the pavers the customer wants.", advance: "CLICK" },
        { step_key: "tender", position: 1, route: "#sales/pos", anchor: "testid:pos-tender", line: "Take the tender and finish the sale.", advance: "NEXT" },
      ],
    },
    {
      chapter_key: "yard.pick-queue",
      position: 1,
      title: "Working the pick queue",
      promise: "Pick, stage and load without a paper ticket.",
      role: "yard",
      route: "#yard",
      steps: [
        { step_key: "queue", position: 0, route: "#yard", anchor: "testid:yard-queue", line: "The queue is yours: take the top pick.", advance: "CLICK" },
        { step_key: "short", position: 1, route: "#yard", anchor: "testid:yard-short", line: "Short on stock? Flag it and move on.", advance: "NEXT" },
      ],
    },
  ];
}

function buildPolicyHistory() {
  return [
    {
      field: "restocking_fee_bps",
      before: "0",
      after: "250",
      who: "da110000-0000-4000-9000-000000000001",
      when: new Date(Date.now() - 3 * 24 * 3600 * 1000).toISOString(),
    },
  ];
}
let policies;

// The pricing world: the categories the pricing screens name (the catalog
// read serves them at this app), and the category margin policies.
const CATEGORIES = [
  { id: "ca7a0000-0000-4000-9000-0000000000a1", code: "PAV", name: "Pavers", is_active: true },
  { id: "ca7a0000-0000-4000-9000-0000000000a2", code: "WAL", name: "Retaining Walls", is_active: true },
  { id: "ca7a0000-0000-4000-9000-0000000000a3", code: "SLB", name: "Slabs and Steps", is_active: true },
];
const PAV = CATEGORIES[0].id;

function buildPolicies() {
  return [{ category_id: PAV, margin_target_bps: 3500, margin_floor_bps: 2000, margin_enforcement_disabled: false }];
}

// The pricing rules: one tier and category rule for Pavers.
const RULES_WORLD = [
  {
    id: "9a110000-0000-4000-9000-0000000000a1",
    scope: "TIER_CATEGORY",
    customer_id: "",
    customer_type: "",
    category_id: PAV,
    product_id: "",
    branch_id: "",
    calc_type: "PCT_OFF_LIST",
    price_cents: null,
    discount_bps: 1000,
    markup_bps: null,
    price_uom: "SF",
    priority: 10,
    valid_from: null,
    valid_to: null,
    detail_text: "Trade discount",
    is_active: true,
    version: 3,
  },
];

let rules;
let overrides;
let breaks;
let promotions;
let clearance;
let rebates;
let ledgerRows;
let conversions;
let batches;
let importProfiles;
let lastPreviewDigest;
let priceFileRuns;

// The customer overrides: one yard account's price on Retaining Walls.
const CUST_YARD = "cu110000-0000-4000-9000-0000000000a1";
const OVERRIDES_WORLD = [
  {
    id: "8a110000-0000-4000-9000-0000000000a1",
    customer_id: CUST_YARD,
    category_id: "ca7a0000-0000-4000-9000-0000000000a2",
    calc_type: "PCT_OFF_LIST",
    price_cents: null,
    discount_bps: 800,
    markup_bps: null,
    price_uom: "SF",
    notes: "Yard account discount",
    version: 2,
  },
];

// The promotions: one category window for Pavers; one clearance mark that
// is the badge only case.
const PROMOS_WORLD = [
  {
    id: "6a110000-0000-4000-9000-0000000000a1",
    code: "SUMMER-PAVERS",
    name: "Summer pavers",
    category_id: PAV,
    product_id: "",
    branch_id: "",
    starts_at: "2026-06-01T00:00:00Z",
    ends_at: "2026-08-31T23:59:59Z",
    calc_type: "PCT_OFF",
    discount_bps: 1000,
    discount_cents: null,
    price_cents: null,
    price_uom: "",
    priority: 5,
    detail_text: "Summer sale",
    is_active: true,
    version: 4,
  },
];
const CLEARANCE_WORLD = [
  {
    product_id: "fa110000-0000-4000-9000-0000000000a1",
    sku: "PAV-60-CH",
    name: "Paver 60 Charcoal",
    reason: "Display batch",
    clearance_price_cents: null,
    price_uom: "",
    started_at: "2026-09-01T00:00:00Z",
    ends_at: null,
    is_active: true,
    version: 1,
  },
];

// The rebates: one vendor program for Pavers, and its claim side.
const REBATES_WORLD = [
  {
    id: "5a110000-0000-4000-9000-0000000000a1",
    code: "NL-2026",
    name: "Northline rebate",
    vendor_id: "vd110000-0000-4000-9000-0000000000a1",
    vendor_name: "Northline Supply",
    category_id: "",
    product_id: "",
    basis: "PCT_OF_SALE",
    rate_bps: 200,
    rate_cents_per_unit: null,
    starts_at: "2026-01-01T00:00:00Z",
    ends_at: null,
    notes: "Yearly program",
    is_active: true,
    version: 2,
  },
];
const LEDGER_WORLD = {
  vendor_id: "vd110000-0000-4000-9000-0000000000a1",
  vendor_name: "Northline Supply",
  total_cents: 14000,
  accrual_count: 1,
  rows: [
    {
      id: "5b110000-0000-4000-9000-0000000000a1",
      program_id: "5a110000-0000-4000-9000-0000000000a1",
      program_code: "NL-2026",
      vendor_id: "vd110000-0000-4000-9000-0000000000a1",
      product_id: "fa110000-0000-4000-9000-0000000000a1",
      product_sku: "PAV-60-CH",
      source_type: "POS_SALE",
      source_id: "0f110000-0000-4000-9000-0000000000a1",
      qty_base: 12,
      basis_cents: 700000,
      accrual_cents: 14000,
      rate_note: "2.00% of sale",
      accrued_at: "2026-09-15T18:00:00Z",
    },
  ],
};

// The conversions: one seeded edge, the placeholder the warn line names.
const UOM_WORLD = [
  {
    id: "4a110000-0000-4000-9000-0000000000a1",
    product_id: "fa110000-0000-4000-9000-0000000000a1",
    product_sku: "PAV-60-CH",
    category_id: "",
    from_uom: "SF",
    to_uom: "PLT",
    factor: 0.08,
    round_up_at_pick: true,
    source: "SEEDED_DEFAULT",
    source_note: "Seeded by migration",
    is_active: true,
    version: 1,
  },
];

// The batches: one staged manufacturer update with two lines, and one
// profile with its mapping.
const BATCHES_WORLD = [
  {
    id: "3a110000-0000-4000-9000-0000000000a1",
    batch_no: "NL-2026-07",
    vendor_id: "vd110000-0000-4000-9000-0000000000a1",
    vendor_name: "Northline Supply",
    profile_id: "2a110000-0000-4000-9000-0000000000a1",
    status: "STAGED",
    effective_date: null,
    source_ref: "nl-pricefile.csv",
    line_count: 2,
    outlier_count: 1,
    created_at: "2026-09-01T09:00:00Z",
    approved_at: null,
    applied_at: null,
  },
];
const BATCH_LINES_WORLD = [
  {
    id: "3b110000-0000-4000-9000-0000000000a1",
    batch_id: "3a110000-0000-4000-9000-0000000000a1",
    product_id: "fa110000-0000-4000-9000-0000000000a1",
    sku: "PAV-60-CH",
    before_cost_cents: 30000,
    after_cost_cents: 31500,
    before_retail_cents: 480,
    after_retail_cents: 520,
    variance_bps: 833,
    is_outlier: false,
    derived_from: "COST_MARKUP",
    unmatched: false,
  },
  {
    id: "3b110000-0000-4000-9000-0000000000a2",
    batch_id: "3a110000-0000-4000-9000-0000000000a1",
    product_id: "",
    sku: "UNKNOWN-SKU",
    before_cost_cents: null,
    after_cost_cents: null,
    before_retail_cents: null,
    after_retail_cents: null,
    variance_bps: null,
    is_outlier: true,
    derived_from: "NONE",
    unmatched: true,
  },
];
const PROFILES_WORLD = [
  {
    id: "2a110000-0000-4000-9000-0000000000a1",
    vendor_id: "vd110000-0000-4000-9000-0000000000a1",
    vendor_name: "Northline Supply",
    name: "Northline standard",
    mapping: { sku_col: "Item", cost_col: "Dealer Net", retail_col: "MSRP", trade_col: "Trade" },
    markup_bps: 6000,
    is_active: true,
  },
];

// The quantity breaks: one category schedule for Pavers, one open top tier.
const BREAKS_WORLD = [
  {
    id: "7a110000-0000-4000-9000-0000000000a1",
    code: "PB-3",
    name: "Paver pallet breaks",
    category_id: PAV,
    product_id: "",
    threshold_uom: "SF",
    is_active: true,
    version: 2,
    items: [
      {
        id: "7a110000-0000-4000-9000-0000000000b1",
        from_qty: 10,
        to_qty: null,
        value_type: "PCT_OFF",
        discount_bps: 500,
        discount_cents: null,
        surcharge_bps: null,
        surcharge_cents: null,
        price_uom: "SF",
      },
    ],
  },
];

function reset() {
  staff = buildStaff();
  hotkeys = buildHotkeys();
  audit = buildAudit();
  assist = buildAssist();
  printJobs = buildPrintJobs();
  products = buildProducts();
  surfaces = buildSurfaces();
  mine = buildMine();
  payments = buildPayments();
  posPolicy = buildPosPolicy();
  policyHistory = buildPolicyHistory();
  warrantyPolicy = buildWarrantyPolicy();
  warrantyVendors = buildWarrantyVendors();
  warrantyAgreements = buildWarrantyAgreements();
  policies = buildPolicies();
  rules = structuredClone(RULES_WORLD);
  overrides = structuredClone(OVERRIDES_WORLD);
  breaks = structuredClone(BREAKS_WORLD);
  promotions = structuredClone(PROMOS_WORLD);
  clearance = structuredClone(CLEARANCE_WORLD);
  rebates = structuredClone(REBATES_WORLD);
  ledgerRows = structuredClone(LEDGER_WORLD.rows);
  conversions = structuredClone(UOM_WORLD);
  batches = structuredClone(BATCHES_WORLD);
  importProfiles = structuredClone(PROFILES_WORLD);
  lastPreviewDigest = null;
  priceFileRuns = [
    {
      id: "1a110000-0000-4000-9000-0000000000a1",
      vendor_id: "vd110000-0000-4000-9000-0000000000a1",
      batch_id: "3a110000-0000-4000-9000-0000000000a1",
      batch_no: "NL-2026-07",
      source_name: "nl-pricefile.csv",
      source_format: "CSV",
      source_bytes: 412,
      content_digest: "1a2b",
      imported_by: "da110000-0000-4000-9000-000000000001",
      imported_by_name: "Dealer admin (demo)",
      created_at: "2026-09-01T09:00:00Z",
      counts: { rows: 12, new: 1, changed: 6, unchanged: 4, unknown: 1, outliers: 1, unpriced: 0 },
    },
  ];
  scenario = "";
  log = [];
}
reset();

// The audit world: one sale whose trail spans three rows (the transaction
// drilldown's case), one sensitive price override, one ordinary status
// change. The masked projection is what the routes answer, the way the core
// enforces it.
function buildAudit() {
  const ago = (minutes) => new Date(Date.now() - minutes * 60 * 1000).toISOString();
  return [
    {
      id: "07000000-0000-4000-9000-0000000000a1",
      action: "SALE_COMPLETED",
      entity_type: "pos.sale",
      entity_id: "0f000000-0000-4000-9000-0000000000s1",
      actor_id: "da110000-0000-4000-9000-000000000002",
      actor_role: "",
      branch_id: BRANCHES[0].id,
      changes: { total_cents: 19680, lines: 2, tender: "CASH" },
      request_id: "req-0001",
      created_at: ago(30),
      seq: 41,
    },
    {
      id: "07000000-0000-4000-9000-0000000000a2",
      action: "ORDER_STATUS_CHANGED",
      entity_type: "pos.sale",
      entity_id: "0f000000-0000-4000-9000-0000000000s1",
      actor_id: "da110000-0000-4000-9000-000000000002",
      actor_role: "",
      branch_id: BRANCHES[0].id,
      changes: { from: "OPEN", to: "PICKED" },
      request_id: "req-0002",
      created_at: ago(20),
      seq: 42,
    },
    {
      id: "07000000-0000-4000-9000-0000000000a3",
      action: "PRICE_OVERRIDE",
      entity_type: "pricing.override",
      entity_id: "0f000000-0000-4000-9000-0000000000o1",
      actor_id: "da110000-0000-4000-9000-000000000001",
      actor_role: "",
      branch_id: BRANCHES[0].id,
      changes: { sku: "PAV-60-CH", from_cents: 480, to_cents: 420, reason: "contract price match" },
      request_id: "req-0003",
      created_at: ago(10),
      seq: 43,
    },
  ];
}

// The platform rosters as the read serves them: the shell's map with one
// seeded binding (F2, deliberately also the counter's search key: the cross
// substrate claim is the shipped design, and the read-only walk sees the
// shared note when it is rebound), the counter's with the shipped BisTrack
// defaults, and the shell's own reserved keys, served rather than mirrored.
function buildHotkeys() {
  return {
    rosters: [
      {
        surface: "app.shell",
        keys: [{ action: "sale.cash.new", binding: "F2", position: 0 }],
        dealer: [],
      },
      {
        surface: "pos.counter",
        keys: [
          { action: "step-next", binding: "f4", position: 0 },
          { action: "step-back", binding: "shift-f4", position: 1 },
          { action: "pay", binding: "f12", position: 2 },
          { action: "park", binding: "f9", position: 3 },
          { action: "search", binding: "f2", position: 4 },
        ],
        dealer: [],
      },
    ],
    can_edit: true,
    reserved: [{ binding: "F1", holds: "opens the shortcut list" }],
  };
}

function flowOf(token) {
  for (const [flow, account] of Object.entries(USERS)) {
    const payload = token.split(".")[1];
    if (payload === undefined) return null;
    try {
      const claims = JSON.parse(Buffer.from(payload, "base64url").toString("utf8"));
      if (claims.sub === account.user_id) return flow;
    } catch {
      return null;
    }
  }
  return null;
}

// The core's gates: the staff read is a manager's (tier 1 or 2); the writes
// are leadership's (tier 1 GLOBAL). A frontline caller is refused both with
// the shared Forbidden body.
function isManager(flow) {
  return flow === "dealer-admin";
}

async function handle(req, res) {
  const url = new URL(req.url, `http://127.0.0.1:${port}`);
  const path = url.pathname;
  if (path === "/healthz") return json(res, { ok: true });
  // The public tenant config route (no credential): the dealer the band and the tab name.
  if (req.method === "GET" && path === "/api/v1/tenant/config") {
    return json(res, { id: "demo-dealer", dealer_name: "Gable Landscape Supply", logo_url: "", primary_color: "#3a7d56", support_email: "support@gablelandscape.invalid", support_phone: "" });
  }

  // ---- the stub's own controls (never the core's) ----
  if (req.method === "POST" && path === "/_stub/reset") {
    reset();
    return json(res, { ok: true });
  }
  if (req.method === "POST" && path === "/_stub/scenario") {
    const body = JSON.parse((await readBody(req)).toString("utf8"));
    scenario = String(body.name ?? "");
    return json(res, { ok: true, scenario });
  }
  if (req.method === "GET" && path === "/_stub/log") return json(res, log);

  // ---- the demo way in ----
  if (path === "/api/v1/demo/flows") return json(res, { flows: FLOWS });
  if (req.method === "POST" && path === "/api/v1/demo/session") {
    const body = await readBody(req);
    try {
      const { flow } = JSON.parse(body.toString("utf8"));
      const account = USERS[flow];
      if (!account) return notFound(res);
      return json(res, { token: mintToken(account.user_id), user: account, flow });
    } catch {
      return refuse(res, 400, "bad request");
    }
  }

  // ---- the staff routes ----
  const authorization = req.headers.authorization ?? "";
  const raw = await readBody(req);
  let body = null;
  const contentType = String(req.headers["content-type"] ?? "").split(";")[0].trim();
  if (contentType === "application/json" && raw.length > 0) {
    try {
      body = JSON.parse(raw.toString("utf8"));
    } catch {
      return refuse(res, 400, "bad request");
    }
  }
  log.push({
    method: req.method,
    path,
    query: Object.fromEntries(url.searchParams),
    bearer: authorization.startsWith("Bearer "),
    org: req.headers["x-org"] ?? "",
    body,
  });
  if (!authorization.startsWith("Bearer ")) return refuse(res, 401, "unauthenticated");
  const flow = flowOf(authorization.slice("Bearer ".length));
  if (flow === null) return refuse(res, 401, "unauthenticated");

  if (req.method === "GET" && path === "/api/v1/branches") {
    return json(res, BRANCHES);
  }

  // The real core's mux answers the unregistered staff family with a bare 404.
  if (scenario === "unregistered-staff" && path.startsWith("/api/v1/staff")) {
    res.writeHead(404, { "content-type": "text/plain; charset=utf-8" });
    return res.end("404 page not found\n");
  }

  if (req.method === "GET" && path === "/api/v1/staff") {
    if (!isManager(flow) || scenario === "refused-staff") return refuse(res, 403, "Forbidden");
    const items = scenario === "empty-staff" ? [] : staff;
    return json(res, { items, next_cursor: null, limit: 200 });
  }

  if (req.method === "GET" && path === "/api/v1/staff/roles") {
    if (!isManager(flow) || scenario === "refused-staff") return refuse(res, 403, "Forbidden");
    return json(res, { items: ROLES, next_cursor: null, limit: 200 });
  }

  const roleMatch = path.match(/^\/api\/v1\/staff\/([^/]+)\/role$/);
  if (req.method === "PUT" && roleMatch) {
    if (!isManager(flow) || scenario === "refused-assign") return refuse(res, 403, "Forbidden");
    const member = staff.find((m) => m.user_id === roleMatch[1]);
    if (!member) return refuse(res, 404, "staff not found");
    const roleCode = String(body?.role_code ?? "");
    const role = ROLES.find((r) => r.code === roleCode);
    if (roleCode !== "" && role === undefined) return refuse(res, 400, "unknown role");
    member.role_code = roleCode;
    member.role_name = role === undefined ? "" : role.name;
    member.tier = role === undefined ? 0 : role.tier;
    member.scope = role === undefined ? "" : role.scope;
    return json(res, { status: "ok", user_id: member.user_id, role_code: roleCode });
  }

  const resetMatch = path.match(/^\/api\/v1\/staff\/([^/]+)\/password-reset$/);
  if (req.method === "POST" && resetMatch) {
    if (!isManager(flow) || scenario === "refused-reset") return refuse(res, 403, "Forbidden");
    const member = staff.find((m) => m.user_id === resetMatch[1]);
    if (!member) return refuse(res, 404, "staff not found");
    const token = `st-${member.user_id.slice(0, 8)}-${Date.now().toString(36)}`;
    return json(res, {
      reset_token: token,
      reset_url: `https://reset.hh-demo.invalid/${token}`,
      expires_at: new Date(Date.now() + 24 * 3600 * 1000).toISOString(),
    });
  }

  // ---- the platform hotkeys ----

  if (req.method === "GET" && path === "/api/v1/platform/hotkeys") {
    if (scenario === "refused-hotkeys") return refuse(res, 403, "Forbidden");
    const view = { ...hotkeys, can_edit: scenario === "readonly-hotkeys" ? false : hotkeys.can_edit };
    return json(res, view);
  }

  const surfaceMatch = path.match(/^\/api\/v1\/platform\/hotkeys\/([^/]+)$/);
  if (req.method === "PUT" && surfaceMatch) {
    if (!isManager(flow) || scenario === "refused-hotkeys") return refuse(res, 403, "Forbidden");
    const surface = decodeURIComponent(surfaceMatch[1]);
    const roster = hotkeys.rosters.find((r) => r.surface === surface);
    if (roster === undefined) return refuse(res, 404, "unknown surface");
    const keys = Array.isArray(body?.keys) ? body.keys : null;
    if (keys === null || keys.length === 0) return refuse(res, 400, "settings: the roster may not be empty");
    roster.keys = keys.map((k, i) => ({
      action: String(k.action ?? ""),
      binding: String(k.binding ?? ""),
      position: typeof k.position === "number" ? k.position : i,
    }));
    return json(res, { saved: true });
  }

  // ---- the walk change 6 warranty routes: the claim threshold, the vendors' credit agreements ----
  // The gates are the core's: the reads are staff's, the writes are the
  // manager tier's (the dealer's administrators' own). A refused read or
  // write answers the shared Forbidden body, exactly {"error":"Forbidden"}.

  if (req.method === "GET" && path === "/api/v1/warranty/policy") {
    if (scenario === "refused-warranty") return refuse(res, 403, "Forbidden");
    return json(res, warrantyPolicy);
  }
  if (req.method === "PUT" && path === "/api/v1/warranty/policy") {
    if (!isManager(flow) || scenario === "refused-warranty") return refuse(res, 403, "Forbidden");
    const cents = Number(body?.claim_threshold_cents);
    if (!Number.isInteger(cents) || cents < 0) return refuse(res, 422, "warranty: claim_threshold_cents must be >= 0");
    warrantyPolicy = { branch_id: String(body?.branch_id ?? ""), claim_threshold_cents: cents };
    return json(res, warrantyPolicy);
  }
  if (req.method === "GET" && path === "/api/v1/warranty/agreements") {
    if (scenario === "refused-warranty") return refuse(res, 403, "Forbidden");
    const items = warrantyVendors.map(effectiveAgreement);
    return json(res, { items });
  }
  const agreementMatch = path.match(/^\/api\/v1\/warranty\/agreements\/([^/]+)$/);
  if (req.method === "PUT" && agreementMatch) {
    if (!isManager(flow) || scenario === "refused-warranty") return refuse(res, 403, "Forbidden");
    const vendor = warrantyVendors.find((v) => v.id === agreementMatch[1]);
    if (!vendor) return refuse(res, 422, `warranty: unknown vendor "${agreementMatch[1]}"`);
    const fault = validateAgreement(body ?? {});
    if (fault !== "") return refuse(res, 422, fault);
    const kind = String(body.agreement_kind);
    warrantyAgreements[vendor.id] =
      kind === "claim_by_claim"
        ? { agreement_kind: kind }
        : { agreement_kind: kind, period: String(body.period), allowance_bp: Number(body.allowance_bp ?? 0) };
    return json(res, effectiveAgreement(vendor));
  }

  // ---- the pricing routes: the categories read, the margin policies, the floor pair ----  // The core's gates: the pricing reads are staff's, the writes are the
  // manager tier's. A refused read or write answers the shared Forbidden
  // body, exactly {"error":"Forbidden"}.

  if (req.method === "GET" && path === "/api/v1/product-categories") {
    if (scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    return json(res, { items: CATEGORIES, next_cursor: null, limit: 500 });
  }

  if (req.method === "GET" && path === "/api/v1/pricing/category-policies") {
    if (scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    return json(res, { items: scenario === "empty-pricing" ? [] : policies, next_cursor: null, limit: 500 });
  }

  const policyMatch = path.match(/^\/api\/v1\/pricing\/category-policies\/([^/]+)$/);
  if (req.method === "PUT" && policyMatch) {
    if (!isManager(flow) || scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const categoryId = decodeURIComponent(policyMatch[1]);
    if (!CATEGORIES.some((c) => c.id === categoryId)) return refuse(res, 404, "category not found");
    const row = {
      category_id: categoryId,
      margin_target_bps: typeof body?.margin_target_bps === "number" ? body.margin_target_bps : 0,
      margin_floor_bps: typeof body?.margin_floor_bps === "number" ? body.margin_floor_bps : 0,
      margin_enforcement_disabled: body?.margin_enforcement_disabled === true,
    };
    const existing = policies.findIndex((p) => p.category_id === categoryId);
    if (existing >= 0) policies[existing] = row;
    else policies.push(row);
    return json(res, row);
  }

  if (req.method === "POST" && path === "/api/v1/pricing/floor-preview") {
    if (!isManager(flow) || scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    if (scenario === "floor-warning") {
      return json(res, {
        items: [
          {
            product_id: "fa110000-0000-4000-9000-0000000000a1",
            sku: "PAV-60-CH",
            name: "Paver 60 Charcoal",
            category_id: PAV,
            customer_type: "RETAIL",
            branch_code: "PLM",
            branch_name: "Gable Landscape Supply - Point Loma",
            final_cents: 420,
            margin_bps: 1500,
            floor_bps: 2000,
            price_uom: "SF",
            source: "TIER_DEFAULT",
            previous_final_cents: 480,
            previous_margin_bps: 2600,
            already_under_floor: false,
          },
        ],
        evaluated: 42,
        not_graded: 0,
        no_cost: 0,
        truncated: false,
      });
    }
    return json(res, { items: [], evaluated: 42, not_graded: 0, no_cost: 0, truncated: false });
  }

  if (req.method === "POST" && path === "/api/v1/pricing/floor-confirmations") {
    if (!isManager(flow) || scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const ids = Array.isArray(body?.product_ids) ? body.product_ids : [];
    if (ids.length === 0) return refuse(res, 400, "product_ids may not be empty");
    return json(res, { status: "logged" });
  }

  // ---- the pricing rules ----

  if (req.method === "GET" && path === "/api/v1/pricing/rules") {
    if (scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const scope = url.searchParams.get("scope") ?? "";
    const customerType = url.searchParams.get("customer_type") ?? "";
    const items = rules.filter((r) => {
      if (scope !== "" && r.scope !== scope) return false;
      if (customerType !== "" && r.customer_type !== customerType) return false;
      return true;
    });
    return json(res, { items: scenario === "empty-pricing" ? [] : items, next_cursor: null, limit: 200 });
  }

  const ruleMatch = path.match(/^\/api\/v1\/pricing\/rules\/([^/]+)$/);
  if (req.method === "GET" && ruleMatch) {
    if (scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const rule = rules.find((r) => r.id === decodeURIComponent(ruleMatch[1]));
    if (rule === undefined) return refuse(res, 404, "rule not found");
    return json(res, rule);
  }

  if (req.method === "POST" && path === "/api/v1/pricing/rules") {
    if (!isManager(flow) || scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const id = `9a110000-0000-4000-9000-${Math.random().toString(16).slice(2, 14).padStart(12, "0")}`;
    rules.push({
      id,
      scope: String(body?.scope ?? ""),
      customer_id: String(body?.customer_id ?? ""),
      customer_type: String(body?.customer_type ?? ""),
      category_id: String(body?.category_id ?? ""),
      product_id: String(body?.product_id ?? ""),
      branch_id: String(body?.branch_id ?? ""),
      calc_type: String(body?.calc_type ?? ""),
      price_cents: body?.price_cents ?? null,
      discount_bps: body?.discount_bps ?? null,
      markup_bps: body?.markup_bps ?? null,
      price_uom: String(body?.price_uom ?? ""),
      priority: typeof body?.priority === "number" ? body.priority : 0,
      valid_from: body?.valid_from ?? null,
      valid_to: body?.valid_to ?? null,
      detail_text: String(body?.detail_text ?? ""),
      is_active: body?.is_active !== false,
      version: 1,
    });
    return json(res, { id, version: 1 });
  }

  if (req.method === "PUT" && ruleMatch) {
    if (!isManager(flow) || scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const rule = rules.find((r) => r.id === decodeURIComponent(ruleMatch[1]));
    if (rule === undefined) return refuse(res, 404, "rule not found");
    if (typeof body?.version === "number" && body.version !== rule.version) return refuse(res, 409, "version conflict: the rule changed since you opened it");
    rule.scope = String(body?.scope ?? rule.scope);
    rule.customer_id = body?.customer_id !== undefined ? String(body.customer_id) : rule.customer_id;
    rule.customer_type = body?.customer_type !== undefined ? String(body.customer_type) : rule.customer_type;
    rule.category_id = body?.category_id !== undefined ? String(body.category_id) : rule.category_id;
    rule.product_id = body?.product_id !== undefined ? String(body.product_id) : rule.product_id;
    rule.branch_id = body?.branch_id !== undefined ? String(body.branch_id) : rule.branch_id;
    rule.calc_type = String(body?.calc_type ?? rule.calc_type);
    if (body?.price_cents !== undefined) rule.price_cents = body.price_cents;
    if (body?.discount_bps !== undefined) rule.discount_bps = body.discount_bps;
    if (body?.markup_bps !== undefined) rule.markup_bps = body.markup_bps;
    rule.price_uom = String(body?.price_uom ?? rule.price_uom);
    if (typeof body?.priority === "number") rule.priority = body.priority;
    if (body?.valid_from !== undefined) rule.valid_from = body.valid_from;
    if (body?.valid_to !== undefined) rule.valid_to = body.valid_to;
    if (body?.detail_text !== undefined) rule.detail_text = String(body.detail_text);
    if (typeof body?.is_active === "boolean") rule.is_active = body.is_active;
    rule.version += 1;
    return json(res, rule);
  }

  if (req.method === "DELETE" && ruleMatch) {
    if (!isManager(flow) || scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const rule = rules.find((r) => r.id === decodeURIComponent(ruleMatch[1]));
    if (rule === undefined) return refuse(res, 404, "rule not found");
    const version = Number(url.searchParams.get("version") ?? "0");
    if (version !== rule.version) return refuse(res, 409, "version conflict: the rule changed since you opened it");
    rule.is_active = false;
    rule.version += 1;
    return json(res, { status: "retired" });
  }

  // ---- the customer overrides ----

  const custOverridesMatch = path.match(/^\/api\/v1\/pricing\/customers\/([^/]+)\/overrides$/);
  if (req.method === "GET" && custOverridesMatch) {
    if (scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const customerId = decodeURIComponent(custOverridesMatch[1]);
    const items = overrides.filter((o) => o.customer_id === customerId);
    return json(res, { items: scenario === "empty-pricing" ? [] : items, next_cursor: null, limit: 200 });
  }

  if (req.method === "POST" && custOverridesMatch) {
    if (!isManager(flow) || scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const customerId = decodeURIComponent(custOverridesMatch[1]);
    const id = `8a110000-0000-4000-9000-${Math.random().toString(16).slice(2, 14).padStart(12, "0")}`;
    overrides.push({
      id,
      customer_id: customerId,
      category_id: String(body?.category_id ?? ""),
      calc_type: String(body?.calc_type ?? ""),
      price_cents: body?.price_cents ?? null,
      discount_bps: body?.discount_bps ?? null,
      markup_bps: body?.markup_bps ?? null,
      price_uom: String(body?.price_uom ?? ""),
      notes: String(body?.notes ?? ""),
      version: 1,
    });
    return json(res, { id, version: 1 });
  }

  const overrideMatch = path.match(/^\/api\/v1\/pricing\/overrides\/([^/]+)$/);
  if (req.method === "PUT" && overrideMatch) {
    if (!isManager(flow) || scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const override = overrides.find((o) => o.id === decodeURIComponent(overrideMatch[1]));
    if (override === undefined) return refuse(res, 404, "override not found");
    if (typeof body?.version === "number" && body.version !== override.version) return refuse(res, 409, "version conflict: the override changed since you opened it");
    override.category_id = String(body?.category_id ?? override.category_id);
    override.calc_type = String(body?.calc_type ?? override.calc_type);
    if (body?.price_cents !== undefined) override.price_cents = body.price_cents;
    if (body?.discount_bps !== undefined) override.discount_bps = body.discount_bps;
    if (body?.markup_bps !== undefined) override.markup_bps = body.markup_bps;
    override.price_uom = String(body?.price_uom ?? override.price_uom);
    if (body?.notes !== undefined) override.notes = String(body.notes);
    override.version += 1;
    return json(res, override);
  }

  if (req.method === "DELETE" && overrideMatch) {
    if (!isManager(flow) || scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const idx = overrides.findIndex((o) => o.id === decodeURIComponent(overrideMatch[1]));
    if (idx < 0) return refuse(res, 404, "override not found");
    const version = Number(url.searchParams.get("version") ?? "0");
    if (version !== overrides[idx].version) return refuse(res, 409, "version conflict: the override changed since you opened it");
    overrides.splice(idx, 1);
    return json(res, { status: "deleted" });
  }

  // ---- the quantity breaks ----

  if (req.method === "GET" && path === "/api/v1/pricing/quantity-breaks") {
    if (scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    return json(res, { items: scenario === "empty-pricing" ? [] : breaks, next_cursor: null, limit: 200 });
  }

  const breakMatch = path.match(/^\/api\/v1\/pricing\/quantity-breaks\/([^/]+)$/);
  if (req.method === "GET" && breakMatch) {
    if (scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const schedule = breaks.find((b) => b.id === decodeURIComponent(breakMatch[1]));
    if (schedule === undefined) return refuse(res, 404, "schedule not found");
    return json(res, schedule);
  }

  if (req.method === "POST" && path === "/api/v1/pricing/quantity-breaks") {
    if (!isManager(flow) || scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const id = `7a110000-0000-4000-9000-${Math.random().toString(16).slice(2, 14).padStart(12, "0")}`;
    breaks.push({
      id,
      code: String(body?.code ?? ""),
      name: String(body?.name ?? ""),
      category_id: String(body?.category_id ?? ""),
      product_id: String(body?.product_id ?? ""),
      threshold_uom: String(body?.threshold_uom ?? ""),
      is_active: body?.is_active !== false,
      version: 1,
      items: Array.isArray(body?.items) ? body.items.map((it, i) => ({ id: `${id}-i${i}`, ...it })) : [],
    });
    return json(res, { id, version: 1 });
  }

  if (req.method === "PUT" && breakMatch) {
    if (!isManager(flow) || scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const schedule = breaks.find((b) => b.id === decodeURIComponent(breakMatch[1]));
    if (schedule === undefined) return refuse(res, 404, "schedule not found");
    if (typeof body?.version === "number" && body.version !== schedule.version) return refuse(res, 409, "version conflict: the schedule changed since you opened it");
    schedule.code = String(body?.code ?? schedule.code);
    schedule.name = String(body?.name ?? schedule.name);
    schedule.category_id = String(body?.category_id ?? schedule.category_id);
    schedule.product_id = String(body?.product_id ?? schedule.product_id);
    schedule.threshold_uom = String(body?.threshold_uom ?? schedule.threshold_uom);
    if (typeof body?.is_active === "boolean") schedule.is_active = body.is_active;
    if (Array.isArray(body?.items)) schedule.items = body.items.map((it, i) => ({ id: `${schedule.id}-i${i}`, ...it }));
    schedule.version += 1;
    return json(res, schedule);
  }

  if (req.method === "DELETE" && breakMatch) {
    if (!isManager(flow) || scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const idx = breaks.findIndex((b) => b.id === decodeURIComponent(breakMatch[1]));
    if (idx < 0) return refuse(res, 404, "schedule not found");
    const version = Number(url.searchParams.get("version") ?? "0");
    if (version !== breaks[idx].version) return refuse(res, 409, "version conflict: the schedule changed since you opened it");
    breaks[idx].is_active = false;
    breaks[idx].version += 1;
    return json(res, { status: "retired" });
  }

  // ---- the promotions and clearance ----

  if (req.method === "GET" && path === "/api/v1/pricing/promotions") {
    if (scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    return json(res, { items: scenario === "empty-pricing" ? [] : promotions, next_cursor: null, limit: 200 });
  }

  if (req.method === "POST" && path === "/api/v1/pricing/promotions") {
    if (!isManager(flow) || scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const id = `6a110000-0000-4000-9000-${Math.random().toString(16).slice(2, 14).padStart(12, "0")}`;
    promotions.push({ id, ...body, version: 1 });
    return json(res, { id, ...body, version: 1 });
  }

  const promoMatch = path.match(/^\/api\/v1\/pricing\/promotions\/([^/]+)$/);
  if (req.method === "PUT" && promoMatch) {
    if (!isManager(flow) || scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const promo = promotions.find((p) => p.id === decodeURIComponent(promoMatch[1]));
    if (promo === undefined) return refuse(res, 404, "promotion not found");
    if (typeof body?.version === "number" && body.version !== promo.version) return refuse(res, 409, "version conflict: the promotion changed since you opened it");
    Object.assign(promo, body, { version: promo.version + 1 });
    return json(res, promo);
  }

  if (req.method === "DELETE" && promoMatch) {
    if (!isManager(flow) || scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const promo = promotions.find((p) => p.id === decodeURIComponent(promoMatch[1]));
    if (promo === undefined) return refuse(res, 404, "promotion not found");
    const version = Number(url.searchParams.get("version") ?? "0");
    if (version !== promo.version) return refuse(res, 409, "version conflict: the promotion changed since you opened it");
    promo.is_active = false;
    promo.version += 1;
    return json(res, { status: "retired" });
  }

  if (req.method === "GET" && path === "/api/v1/pricing/clearance") {
    if (scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    return json(res, { items: scenario === "empty-pricing" ? [] : clearance, next_cursor: null, limit: 200 });
  }

  const clearMatch = path.match(/^\/api\/v1\/pricing\/clearance\/([^/]+)$/);
  if (req.method === "PUT" && clearMatch) {
    if (!isManager(flow) || scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const productId = decodeURIComponent(clearMatch[1]);
    let item = clearance.find((c) => c.product_id === productId);
    if (item === undefined) {
      item = { product_id: productId, sku: "", name: "", version: 0 };
      clearance.push(item);
    }
    Object.assign(item, body, { version: item.version + 1 });
    return json(res, item);
  }

  if (req.method === "DELETE" && clearMatch) {
    if (!isManager(flow) || scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const idx = clearance.findIndex((c) => c.product_id === decodeURIComponent(clearMatch[1]));
    if (idx < 0) return refuse(res, 404, "clearance item not found");
    clearance.splice(idx, 1);
    return json(res, { status: "deleted" });
  }

  // ---- the rebates ----

  if (req.method === "GET" && path === "/api/v1/pricing/rebates/programs") {
    if (scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    return json(res, { items: scenario === "empty-pricing" ? [] : rebates, next_cursor: null, limit: 200 });
  }

  if (req.method === "POST" && path === "/api/v1/pricing/rebates/programs") {
    if (!isManager(flow) || scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const id = `5a110000-0000-4000-9000-${Math.random().toString(16).slice(2, 14).padStart(12, "0")}`;
    const program = { id, ...body, version: 1 };
    rebates.push(program);
    return json(res, program);
  }

  const rebateMatch = path.match(/^\/api\/v1\/pricing\/rebates\/programs\/([^/]+)$/);
  if (req.method === "PUT" && rebateMatch) {
    if (!isManager(flow) || scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const program = rebates.find((r) => r.id === decodeURIComponent(rebateMatch[1]));
    if (program === undefined) return refuse(res, 404, "program not found");
    if (typeof body?.version === "number" && body.version !== program.version) return refuse(res, 409, "version conflict: the program changed since you opened it");
    Object.assign(program, body, { version: program.version + 1 });
    return json(res, program);
  }

  if (req.method === "DELETE" && rebateMatch) {
    if (!isManager(flow) || scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const program = rebates.find((r) => r.id === decodeURIComponent(rebateMatch[1]));
    if (program === undefined) return refuse(res, 404, "program not found");
    const version = Number(url.searchParams.get("version") ?? "0");
    if (version !== program.version) return refuse(res, 409, "version conflict: the program changed since you opened it");
    program.is_active = false;
    program.version += 1;
    return json(res, { status: "retired" });
  }

  const ledgerMatch = path.match(/^\/api\/v1\/pricing\/rebates\/vendors\/([^/]+)\/ledger$/);
  if (req.method === "GET" && ledgerMatch) {
    if (scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const vendorId = decodeURIComponent(ledgerMatch[1]);
    const rows = ledgerRows.filter((r) => r.vendor_id === vendorId);
    return json(res, {
      vendor_id: vendorId,
      vendor_name: "Northline Supply",
      total_cents: rows.reduce((sum, r) => sum + r.accrual_cents, 0),
      accrual_count: rows.length,
      rows,
    });
  }

  if (req.method === "POST" && path === "/api/v1/pricing/rebates/accrue") {
    if (!isManager(flow) || scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const lines = Array.isArray(body?.lines) ? body.lines : [];
    const accrued = lines.map((line, i) => ({
      id: `5b110000-0000-4000-9000-${i.toString().padStart(12, "0")}`,
      program_id: "5a110000-0000-4000-9000-0000000000a1",
      program_code: "NL-2026",
      vendor_id: "vd110000-0000-4000-9000-0000000000a1",
      product_id: String(line?.product_id ?? ""),
      product_sku: "PAV-60-CH",
      source_type: String(body?.source_type ?? ""),
      source_id: String(body?.source_id ?? ""),
      qty_base: Number(line?.qty_base ?? 0),
      basis_cents: Number(line?.sale_cents ?? 0),
      accrual_cents: Math.round(Number(line?.sale_cents ?? 0) * 0.02),
      rate_note: "2.00% of sale",
      accrued_at: new Date().toISOString(),
    }));
    ledgerRows.push(...accrued);
    return json(res, { accrued, skipped_count: 0, total_cents: accrued.reduce((sum, r) => sum + r.accrual_cents, 0) });
  }

  // ---- the units of measure ----

  if (req.method === "GET" && path === "/api/v1/pricing/uom/conversions") {
    if (scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    return json(res, { items: scenario === "empty-pricing" ? [] : conversions, next_cursor: null, limit: 500 });
  }

  if (req.method === "PUT" && path === "/api/v1/pricing/uom/conversions") {
    if (!isManager(flow) || scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const id = String(body?.id ?? "");
    let edge = id === "" ? undefined : conversions.find((c) => c.id === id);
    if (edge === undefined) {
      edge = { id: id === "" ? `4a110000-0000-4000-9000-${Math.random().toString(16).slice(2, 14).padStart(12, "0")}` : id, version: 0 };
      conversions.push(edge);
    }
    Object.assign(edge, body, { version: edge.version + 1 });
    return json(res, edge);
  }

  const conversionMatch = path.match(/^\/api\/v1\/pricing\/uom\/conversions\/([^/]+)$/);
  if (req.method === "DELETE" && conversionMatch) {
    if (!isManager(flow) || scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const idx = conversions.findIndex((c) => c.id === decodeURIComponent(conversionMatch[1]));
    if (idx < 0) return refuse(res, 404, "conversion not found");
    conversions.splice(idx, 1);
    return json(res, { status: "deleted" });
  }

  if (req.method === "POST" && path === "/api/v1/pricing/uom/plan") {
    if (scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const qty = Number(body?.qty_entered ?? 0);
    const factor = 0.08;
    const exact = qty * factor;
    const pick = Math.ceil(exact);
    return json(res, {
      product_id: String(body?.product_id ?? ""),
      qty_entered: qty,
      uom_entered: String(body?.uom_entered ?? ""),
      pick_uom: "PLT",
      pick_qty_exact: exact,
      pick_qty: pick,
      rounded_up: pick > exact,
      overage_qty: pick - exact,
      pick_steps: [{ from_uom: String(body?.uom_entered ?? "SF"), to_uom: "PLT", factor, source: "SEEDED_DEFAULT" }],
      weight_qty: 0,
      unconfirmed: true,
    });
  }

  // ---- the price batches and import profiles ----

  if (req.method === "GET" && path === "/api/v1/pricing/batches") {
    if (scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const status = url.searchParams.get("status") ?? "";
    const items = batches.filter((b) => status === "" || b.status === status);
    return json(res, { items: scenario === "empty-pricing" ? [] : items, next_cursor: null, limit: 200 });
  }

  if (req.method === "POST" && path === "/api/v1/pricing/batches") {
    if (!isManager(flow) || scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const id = `3a110000-0000-4000-9000-${Math.random().toString(16).slice(2, 14).padStart(12, "0")}`;
    const batch = {
      id,
      batch_no: String(body?.batch_no ?? ""),
      vendor_id: String(body?.vendor_id ?? ""),
      vendor_name: "Northline Supply",
      profile_id: String(body?.profile_id ?? ""),
      status: "DRAFT",
      effective_date: body?.effective_date ? String(body.effective_date) : null,
      source_ref: String(body?.source_ref ?? ""),
      line_count: 0,
      outlier_count: 0,
      created_at: new Date().toISOString(),
      approved_at: null,
      applied_at: null,
    };
    batches.push(batch);
    return json(res, batch);
  }

  if (req.method === "POST" && path === "/api/v1/pricing/batches/apply-due") {
    if (!isManager(flow) || scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    let applied = 0;
    for (const batch of batches) {
      if (batch.status === "APPROVED" && batch.effective_date !== null && batch.effective_date.slice(0, 10) <= new Date().toISOString().slice(0, 10)) {
        batch.status = "APPLIED";
        batch.applied_at = new Date().toISOString();
        applied += 1;
      }
    }
    return json(res, { applied });
  }

  const batchMatch = path.match(/^\/api\/v1\/pricing\/batches\/([^/]+)$/);
  if (req.method === "GET" && batchMatch) {
    if (scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const batch = batches.find((b) => b.id === decodeURIComponent(batchMatch[1]));
    if (batch === undefined) return refuse(res, 404, "batch not found");
    return json(res, batch);
  }

  const linesMatch = path.match(/^\/api\/v1\/pricing\/batches\/([^/]+)\/lines$/);
  if (req.method === "GET" && linesMatch) {
    if (scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const batchId = decodeURIComponent(linesMatch[1]);
    return json(res, { items: BATCH_LINES_WORLD.filter((l) => l.batch_id === batchId), next_cursor: null, limit: 500 });
  }

  const importMatch = path.match(/^\/api\/v1\/pricing\/batches\/([^/]+)\/import$/);
  if (req.method === "POST" && importMatch) {
    if (!isManager(flow) || scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const batch = batches.find((b) => b.id === decodeURIComponent(importMatch[1]));
    if (batch === undefined) return refuse(res, 404, "batch not found");
    const csv = Buffer.from(raw).toString("utf8");
    const rows = Math.max(0, csv.trim().split("\n").length - 1);
    batch.line_count += rows;
    batch.status = "STAGED";
    return json(res, {
      batch,
      preview: [],
      unmatched: [],
      estimated: [],
      outliers: rows > 2 ? [{ sku: "OUTLIER-SKU" }] : [],
      total_lines: rows,
      matched_lines: rows,
    });
  }

  const approveMatch = path.match(/^\/api\/v1\/pricing\/batches\/([^/]+)\/approve$/);
  if (req.method === "POST" && approveMatch) {
    if (!isManager(flow) || scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const batch = batches.find((b) => b.id === decodeURIComponent(approveMatch[1]));
    if (batch === undefined) return refuse(res, 404, "batch not found");
    if (batch.status !== "STAGED") return refuse(res, 409, "only a staged batch can be approved");
    batch.status = "APPROVED";
    batch.approved_at = new Date().toISOString();
    return json(res, batch);
  }

  const applyMatch = path.match(/^\/api\/v1\/pricing\/batches\/([^/]+)\/apply$/);
  if (req.method === "POST" && applyMatch) {
    if (!isManager(flow) || scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const batch = batches.find((b) => b.id === decodeURIComponent(applyMatch[1]));
    if (batch === undefined) return refuse(res, 404, "batch not found");
    if (batch.status !== "APPROVED") return refuse(res, 409, "only an approved batch can be applied");
    batch.status = "APPLIED";
    batch.applied_at = new Date().toISOString();
    return json(res, { batch, applied: batch.line_count });
  }

  if (req.method === "GET" && path === "/api/v1/pricing/import-profiles") {
    if (scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    return json(res, { items: scenario === "empty-pricing" ? [] : importProfiles, next_cursor: null, limit: 200 });
  }

  const profileMatch = path.match(/^\/api\/v1\/pricing\/import-profiles\/([^/]+)$/);
  if (req.method === "PUT" && profileMatch) {
    if (!isManager(flow) || scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const profile = importProfiles.find((p) => p.id === decodeURIComponent(profileMatch[1]));
    if (profile === undefined) return refuse(res, 404, "profile not found");
    if (body?.mapping !== undefined && typeof body.mapping === "object") profile.mapping = body.mapping;
    return json(res, profile);
  }

  // ---- the price file road ----

  if (req.method === "GET" && path === "/api/v1/pricing/price-file/vendors") {
    if (scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const items = [
      {
        vendor_id: "vd110000-0000-4000-9000-0000000000a1",
        vendor_no: "NL-001",
        name: "Northline Supply",
        profile_id: "2a110000-0000-4000-9000-0000000000a1",
        origin: "FIRST_IMPORT",
        mapping: { sku_col: "Item", cost_col: "Dealer Net", retail_col: "MSRP" },
        last_import_at: "2026-09-01T09:00:00Z",
      },
    ];
    return json(res, { items: scenario === "empty-pricing" ? [] : items, next_cursor: null, limit: 200 });
  }

  if (req.method === "POST" && path === "/api/v1/pricing/price-file/preview") {
    if (!isManager(flow) || scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const form = parseMultipart(raw, req.headers["content-type"] ?? "");
    const csv = form.file ? Buffer.from(form.file).toString("utf8") : "";
    const lines = csv.trim().split("\n").slice(1).filter((l) => l.trim() !== "");
    lastPreviewDigest = `digest-${csv.length}-${lines.length}`;
    return json(res, {
      vendor_id: form.vendor_id ?? "",
      format: "CSV",
      mapping: {},
      mapping_source: "STORED",
      counts: { rows: lines.length, new: lines.length - 1, changed: 1, unchanged: 0, unknown: 0, outliers: lines.length > 3 ? 1 : 0, unpriced: 0 },
      lines: lines.map((l, i) => {
        const cells = l.split(",");
        return {
          row: i + 2,
          sku: cells[0] ?? "",
          outcome: i === 0 ? "CHANGED" : "NEW",
          current_retail_cents: i === 0 ? 480 : null,
          new_retail_cents: Number(cells[2] ?? 0) * 100,
          variance_bps: i === 0 ? 833 : null,
          is_outlier: false,
          derived_from: "COST_MARKUP",
          freight_estimated: false,
        };
      }),
      truncated: false,
      catalog_checked: true,
      digest: lastPreviewDigest,
      costs_visible: true,
    });
  }

  if (req.method === "POST" && path === "/api/v1/pricing/price-file/apply") {
    if (!isManager(flow) || scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const form = parseMultipart(raw, req.headers["content-type"] ?? "");
    if (form.digest !== lastPreviewDigest) return refuse(res, 409, "digest mismatch: the file changed since the preview");
    const id = `3a110000-0000-4000-9000-${Math.random().toString(16).slice(2, 14).padStart(12, "0")}`;
    const batch = {
      id,
      batch_no: `NL-PF-${priceFileRuns.length + 1}`,
      vendor_id: form.vendor_id ?? "",
      vendor_name: "Northline Supply",
      profile_id: "2a110000-0000-4000-9000-0000000000a1",
      status: "STAGED",
      effective_date: form.effective_date ?? null,
      source_ref: form.fileFilename ?? "price-file.csv",
      line_count: 4,
      outlier_count: 1,
      created_at: new Date().toISOString(),
      approved_at: null,
      applied_at: null,
    };
    batches.push(batch);
    priceFileRuns.unshift({
      id: `1a110000-0000-4000-9000-${Math.random().toString(16).slice(2, 14).padStart(12, "0")}`,
      vendor_id: form.vendor_id ?? "",
      batch_id: id,
      batch_no: batch.batch_no,
      source_name: "price-file.csv",
      source_format: "CSV",
      source_bytes: form.file ? form.file.length : 0,
      content_digest: form.digest ?? "",
      imported_by: "da110000-0000-4000-9000-000000000001",
      imported_by_name: "Dealer admin (demo)",
      created_at: new Date().toISOString(),
      counts: { rows: 4, new: 3, changed: 1, unchanged: 0, unknown: 0, outliers: 1, unpriced: 0 },
    });
    return json(res, { run_id: priceFileRuns[0].id, batch, counts: { rows: 4, new: 3, changed: 1, unchanged: 0, unknown: 0, outliers: 1, unpriced: 0 }, staged: 4, profile_created: false });
  }

  if (req.method === "GET" && path === "/api/v1/pricing/price-file/runs") {
    if (scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    return json(res, { items: priceFileRuns, next_cursor: null, limit: 100 });
  }

  // ---- the low margin report ----

  if (req.method === "GET" && path === "/api/v1/pricing/reports/low-margin/scan") {
    if (scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const items = scenario === "empty-pricing"
      ? []
      : [
          {
            doc_type: "quote",
            line_id: "0c110000-0000-4000-9000-0000000000a1",
            doc_id: "0c110000-0000-4000-9000-0000000000b1",
            doc_no: "Q-1042",
            doc_at: "2026-09-20T15:30:00Z",
            branch_id: BRANCHES[0].id,
            customer_id: "cu110000-0000-4000-9000-0000000000a1",
            customer_type: "RETAIL",
            product_id: "fa110000-0000-4000-9000-0000000000a1",
            sku: "PAV-60-CH",
            product_name: "Paver 60 Charcoal",
            qty_entered: 120,
            uom_entered: "SF",
            unit_price_cents: 420,
            price_uom: "SF",
            cost_in_price_uom_cents: 300,
            margin_bps: 2857,
            target_bps: 3500,
            floor_bps: 2000,
            level: "warn",
            salesperson_id: "da110000-0000-4000-9000-000000000002",
            salesperson: "Inside sales (demo)",
            cleared_below_floor: false,
          },
          {
            doc_type: "order",
            line_id: "0c110000-0000-4000-9000-0000000000a2",
            doc_id: "0c110000-0000-4000-9000-0000000000b2",
            doc_no: "SO-2077",
            doc_at: "2026-09-28T11:00:00Z",
            branch_id: BRANCHES[0].id,
            customer_id: "cu110000-0000-4000-9000-0000000000a1",
            customer_type: "CONTRACTOR",
            product_id: "fa110000-0000-4000-9000-0000000000a2",
            sku: "WAL-8",
            product_name: "Wall block 8",
            qty_entered: 40,
            uom_entered: "EA",
            unit_price_cents: 210,
            price_uom: "EA",
            cost_in_price_uom_cents: 190,
            margin_bps: 952,
            target_bps: 3000,
            floor_bps: 1500,
            level: "critical",
            cleared_below_floor: true,
            cleared_by_id: "da110000-0000-4000-9000-000000000001",
            cleared_by: "Branch manager (demo)",
            clear_authority: "MANAGER_SCOPE",
            clear_disc_pct: 10,
            clear_margin_bps: 1100,
          },
        ];
    return json(res, { items, next_cursor: null, limit: 200 });
  }

  if (req.method === "POST" && path === "/api/v1/pricing/reports/low-margin") {
    if (scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const lines = Array.isArray(body?.lines) ? body.lines : [];
    return json(res, lines.map((line) => ({
      product_id: String(line?.product_id ?? ""),
      final_cents: 210,
      margin_bps: 952,
      target_bps: 3000,
      floor_bps: 1500,
      level: "critical",
      source: "TIER_CATEGORY",
      price_uom: String(line?.uom_entered ?? "EA"),
      invalid: false,
      reason: "",
    })));
  }

  // ---- the pricing log and the change log ----

  if (req.method === "GET" && path === "/api/v1/pricing/audit") {
    if (scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const entityType = url.searchParams.get("entity_type") ?? "";
    const action = url.searchParams.get("action") ?? "";
    const ago = (days) => new Date(Date.now() - days * 86400000).toISOString();
    const items = [
      {
        id: "0e110000-0000-4000-9000-0000000000a1",
        entity_type: "category_policy",
        entity_id: "ca7a0000-0000-4000-9000-0000000000a1",
        action: "UPDATE",
        field_name: "margin_floor_bps",
        old_value: "2000",
        new_value: "1000",
        user_id: "da110000-0000-4000-9000-000000000001",
        created_at: ago(3),
        bypass_reason: "",
      },
      {
        id: "0e110000-0000-4000-9000-0000000000a2",
        entity_type: "customer_override",
        entity_id: "8a110000-0000-4000-9000-0000000000a1",
        action: "BYPASS",
        field_name: "",
        old_value: "",
        new_value: "",
        user_id: "da110000-0000-4000-9000-000000000002",
        created_at: ago(1),
        bypass_reason: "APPROVED_OVERRIDE",
      },
    ].filter((e) => (entityType === "" || e.entity_type === entityType) && (action === "" || e.action === action));
    return json(res, { items: scenario === "empty-pricing" ? [] : items, next_cursor: null, limit: 200 });
  }

  if (req.method === "GET" && path === "/api/v1/pricing/change-log") {
    if (scenario === "refused-pricing") return refuse(res, 403, "Forbidden");
    const productId = url.searchParams.get("product_id") ?? "";
    const batchId = url.searchParams.get("batch_id") ?? "";
    const items = [
      {
        id: "0d110000-0000-4000-9000-0000000000a1",
        product_id: "fa110000-0000-4000-9000-0000000000a1",
        batch_id: "3a110000-0000-4000-9000-0000000000a1",
        field: "retail_cents",
        before_cents: 480,
        after_cents: 520,
        effective_date: null,
        changed_by: "da110000-0000-4000-9000-000000000001",
        created_at: "2026-09-01T09:05:00Z",
      },
    ].filter((e) => (productId === "" || e.product_id === productId) && (batchId === "" || e.batch_id === batchId));
    return json(res, { items: scenario === "empty-pricing" ? [] : items, next_cursor: null, limit: 200 });
  }

  // ---- the audit trail ----

  if (req.method === "GET" && path === "/api/v1/audit") {
    // The trail is an admin read: a frontline caller is refused with the
    // shared Forbidden body.
    if (!isManager(flow) || scenario === "refused-audit") return refuse(res, 403, "Forbidden");
    const entityType = url.searchParams.get("entity_type") ?? "";
    const action = url.searchParams.get("action") ?? "";
    const actorId = url.searchParams.get("actor_id") ?? "";
    const since = url.searchParams.get("since") ?? "";
    // The core refuses an entity_type with spaces or an actor id that is not a UUID with a 400 in words.
    if (entityType !== "" && /\s/.test(entityType)) {
      return refuse(res, 400, "audit: entity_type must not contain spaces");
    }
    if (actorId !== "" && !staff.some((s) => s.user_id === actorId) && !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(actorId)) {
      return refuse(res, 400, "audit: actor_id must be a UUID");
    }
    const items = audit.filter((e) => {
      if (entityType !== "" && e.entity_type !== entityType) return false;
      if (action !== "" && e.action !== action) return false;
      if (actorId !== "" && e.actor_id !== actorId) return false;
      if (since !== "" && new Date(e.created_at).getTime() < new Date(since).getTime()) return false;
      return true;
    });
    return json(res, { items, next_cursor: null, limit: 200 });
  }

  const entryMatch = path.match(/^\/api\/v1\/audit\/([^/]+)$/);
  if (req.method === "GET" && entryMatch && entryMatch[1] !== "transaction") {
    if (!isManager(flow) || scenario === "refused-audit") return refuse(res, 403, "Forbidden");
    const entry = audit.find((e) => e.id === entryMatch[1]);
    if (entry === undefined) return refuse(res, 404, "entry not found");
    return json(res, entry);
  }

  const transactionMatch = path.match(/^\/api\/v1\/audit\/transaction\/([^/]+)\/([^/]+)$/);
  if (req.method === "GET" && transactionMatch) {
    if (!isManager(flow) || scenario === "refused-audit") return refuse(res, 403, "Forbidden");
    const entityType = decodeURIComponent(transactionMatch[1]);
    const entityId = decodeURIComponent(transactionMatch[2]);
    const items = audit.filter((e) => e.entity_type === entityType && e.entity_id === entityId);
    // The real core's reopen handler writes a bare array, not the page.
    if (scenario === "bare-trail") return json(res, items);
    if (scenario === "empty-trail") return json(res, []);
    return json(res, { items, next_cursor: null, limit: 200 });
  }

  // ---- the assist admin (the core's RequireAdmin: tier 1 global) ----

  const assistSpendReport = () => {
    // The core zero fills the report from the staff list, biggest spender
    // first: the exchange count is the answer, the tokens the next question.
    const rows = staff.map((m) => {
      const used = assist.exchanges[m.user_id] ?? { exchanges: 0, tokens_in: 0, tokens_out: 0 };
      const override = assist.user_overrides[m.user_id];
      const roleBudget = assist.role_budgets[m.role_code];
      const budget =
        override !== undefined
          ? { tokens: override, source: "user" }
          : roleBudget !== undefined
            ? { tokens: roleBudget, source: "role" }
            : { tokens: assist.default_budget, source: "default" };
      return {
        user_id: m.user_id,
        display_name: m.display_name,
        email: m.email,
        role_code: m.role_code,
        exchanges: used.exchanges,
        tokens_in: used.tokens_in,
        tokens_out: used.tokens_out,
        budget,
      };
    });
    rows.sort((a, b) => b.tokens_in + b.tokens_out - (a.tokens_in + a.tokens_out) || b.exchanges - a.exchanges);
    return { month: "2026-10", users: rows };
  };

  if (req.method === "GET" && path === "/api/v1/assist/admin/spend") {
    if (!isManager(flow) || scenario === "refused-assist") return refuse(res, 403, "Forbidden");
    return json(res, assistSpendReport());
  }

  if (req.method === "GET" && path === "/api/v1/assist/admin/limits") {
    if (!isManager(flow) || scenario === "refused-assist") return refuse(res, 403, "Forbidden");
    return json(res, {
      role_budgets: assist.role_budgets,
      user_overrides: assist.user_overrides,
      default_budget: assist.default_budget,
    });
  }

  const roleLimitMatch = path.match(/^\/api\/v1\/assist\/admin\/limits\/role\/([^/]+)$/);
  if (req.method === "PUT" && roleLimitMatch) {
    if (!isManager(flow) || scenario === "refused-assist") return refuse(res, 403, "Forbidden");
    const role = decodeURIComponent(roleLimitMatch[1]);
    const tokens = Number(body?.tokens);
    if (!Number.isInteger(tokens) || tokens < 0) {
      return refuse(res, 400, "A budget can't be negative. Use 0 to switch the assistant off for this role.");
    }
    assist.role_budgets[role] = tokens;
    return json(res, { role_code: role, tokens });
  }

  const userLimitMatch = path.match(/^\/api\/v1\/assist\/admin\/limits\/user\/([^/]+)$/);
  if (req.method === "PUT" && userLimitMatch) {
    if (!isManager(flow) || scenario === "refused-assist") return refuse(res, 403, "Forbidden");
    const user = decodeURIComponent(userLimitMatch[1]);
    const tokens = Number(body?.tokens);
    if (!Number.isInteger(tokens) || tokens < 0) {
      return refuse(res, 400, "A budget can't be negative. Use 0 to switch the assistant off for this person.");
    }
    assist.user_overrides[user] = tokens;
    return json(res, { user_id: user, tokens, source: "user" });
  }

  if (req.method === "DELETE" && userLimitMatch) {
    if (!isManager(flow) || scenario === "refused-assist") return refuse(res, 403, "Forbidden");
    // Reset to role is idempotent: resetting a person with no override succeeds.
    delete assist.user_overrides[decodeURIComponent(userLimitMatch[1])];
    return json(res, { user_id: decodeURIComponent(userLimitMatch[1]), source: "role" });
  }

  if (req.method === "GET" && path === "/api/v1/assist/admin/settings") {
    if (!isManager(flow) || scenario === "refused-assist") return refuse(res, 403, "Forbidden");
    return json(res, assist.settings);
  }

  if (req.method === "PUT" && path === "/api/v1/assist/admin/settings") {
    if (!isManager(flow) || scenario === "refused-assist") return refuse(res, 403, "Forbidden");
    const key = String(body?.key ?? "");
    const value = String(body?.value ?? "");
    const yesNo = key === "chat_enabled" || key === "spend_visible_to_non_admin";
    if (!yesNo) return refuse(res, 400, `"${key}" isn't an assistant setting.`);
    if (value !== "true" && value !== "false") {
      return refuse(res, 400, `${key} is a yes/no setting, use true or false.`);
    }
    assist.settings.settings[key] = value === "true";
    return json(res, { key, value });
  }

  // ---- the print queue (the core's RequireStaff: any signed in caller) ----

  if (req.method === "GET" && path === "/api/v1/printing/jobs") {
    const docType = url.searchParams.get("doc_type") ?? "";
    const refId = url.searchParams.get("ref_id") ?? "";
    // The core's own refusals, in its own words and in its own order: the
    // paper first, then the reference.
    if (!DOC_TYPES.has(docType)) return refuse(res, 400, `unknown print doc_type "${docType}"`);
    if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(refId)) {
      return refuse(res, 404, "not found");
    }
    const jobs = printJobs.filter((j) => j.doc_type === docType && j.ref_id === refId);
    return json(res, { jobs });
  }

  // ---- the exports (the audit workbook is RequireAdmin like the trail it mirrors; the messages export is RequireAdmin) ----

  if (req.method === "GET" && path === "/api/v1/exports/audit.xlsx") {
    // Same gate as the audit read it mirrors, with the shared Forbidden body.
    if (!isManager(flow) || scenario === "refused-audit") return refuse(res, 403, "Forbidden");
    // A double's workbook: the xlsx magic followed by filler. The route's
    // contract is the raw bytes, and the app never parses them.
    const body = Buffer.concat([Buffer.from([0x50, 0x4b, 0x03, 0x04]), Buffer.alloc(1020, 7)]);
    res.writeHead(200, {
      "Content-Type": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
      "Content-Length": body.byteLength,
    });
    res.end(body);
    return;
  }

  if (req.method === "GET" && path === "/api/v1/messaging/channels") {
    return json(res, {
      channels: [
        {
          id: "bb000000-0000-4000-9000-000000000001",
          kind: "BRANCH",
          branch_id: PLM,
          slug: "yard-and-dock",
          name: "Yard and Dock",
          topic: "Point Loma yard dispatches and dock staging",
          tier: "STANDARD",
          visibility: "PUBLIC",
          is_archived: false,
          created_at: "2026-01-01T00:00:00Z",
          unread_count: 0,
          mentioned: false,
        },
        {
          id: "bb000000-0000-4000-9000-000000000002",
          kind: "APPROVAL",
          slug: "approvals-and-counter",
          name: "Approvals and Counter",
          topic: "Counter payment approvals and price overrides",
          tier: "ESCALATION",
          visibility: "PUBLIC",
          is_archived: false,
          created_at: "2026-01-01T00:00:00Z",
          unread_count: 0,
          mentioned: false,
        },
      ],
    });
  }

  if (req.method === "GET" && path === "/api/v1/messaging/export") {
    // The core's RequireAdmin: a frontline caller is refused the export.
    if (!isManager(flow) || scenario === "refused-assist") return refuse(res, 403, "Forbidden");
    const since = url.searchParams.get("since") ?? "";
    const until = url.searchParams.get("until") ?? "";
    if (since !== "" && Number.isNaN(new Date(since).getTime())) {
      return refuse(res, 400, "since must be an RFC3339 timestamp");
    }
    if (until !== "" && Number.isNaN(new Date(until).getTime())) {
      return refuse(res, 400, "until must be an RFC3339 timestamp");
    }
    const channelId = url.searchParams.get("channel_id") ?? "";
    const rows = buildMessageExport().filter((m) => channelId === "" || m.channel_id === channelId);
    return json(res, { messages: rows });
  }

  // ---- the customer search and product counter-search (for entity pickers) ----

  if (req.method === "GET" && path === "/api/v1/customers/search") {
    const q = (url.searchParams.get("q") ?? "").trim().toLowerCase();
    const customers = [
      {
        id: CUST_YARD,
        name: "Point Loma Hardscape & Masonry",
        account_no: "ACCT-1042",
        customer_type: "CONTRACTOR",
        payment_terms_days: 30,
      },
      {
        id: "cu110000-0000-4000-9000-0000000000a2",
        name: "Julian Stoneworks",
        account_no: "ACCT-1088",
        customer_type: "BUILDER",
        payment_terms_days: 30,
      },
    ];
    const hits = q === ""
      ? customers
      : customers.filter((c) => `${c.name} ${c.account_no} ${c.customer_type}`.toLowerCase().includes(q));
    return json(res, hits);
  }

  if (req.method === "GET" && path === "/api/v1/products/counter-search") {
    const q = (url.searchParams.get("q") ?? "").trim().toLowerCase();
    const catalogHits = [
      {
        product_id: "fa110000-0000-4000-9000-0000000000a1",
        sku: "PAV-60-CH",
        name: "Paver 60 Charcoal",
        collection: "Blu",
        color_family: "Grey",
        base_uom: "SF",
        sold_by: "SF",
        has_image: false,
        match_reason: "NAME_PREFIX",
      },
      {
        product_id: "fa110000-0000-4000-9000-0000000000a2",
        sku: "WAL-8",
        name: "Wall block 8",
        collection: "Seminole",
        color_family: "Brown",
        base_uom: "EA",
        sold_by: "EA",
        has_image: false,
        match_reason: "NAME_PREFIX",
      },
      ...products.map((p) => ({
        product_id: p.id,
        sku: p.sku,
        name: p.name,
        collection: p.collection,
        color_family: p.color_family,
        base_uom: p.base_uom,
        sold_by: p.sold_by,
        has_image: false,
        match_reason: "NAME",
      })),
    ];
    const items = q === ""
      ? catalogHits
      : catalogHits.filter((p) => `${p.name} ${p.sku} ${p.collection}`.toLowerCase().includes(q));
    return json(res, { items });
  }

  // ---- the catalog (the reads are the public catalogue; the writes are the manager gate) ----

  if (req.method === "GET" && path === "/api/v1/products") {
    const q = (url.searchParams.get("q") ?? "").toLowerCase();
    const items = q === "" ? products : products.filter((p) => `${p.name} ${p.sku} ${p.collection}`.toLowerCase().includes(q));
    return json(res, { items, next_cursor: null, limit: 20 });
  }

  const productMatch = path.match(/^\/api\/v1\/products\/([^/]+)$/);
  if (req.method === "GET" && productMatch) {
    const p = products.find((x) => x.id === productMatch[1]);
    if (p === undefined) return refuse(res, 404, "not found");
    return json(res, p);
  }

  if (req.method === "PUT" && productMatch) {
    // The core's RequireManager: a frontline caller is refused the write.
    if (!isManager(flow) || scenario === "refused-product") return refuse(res, 403, "Forbidden");
    const p = products.find((x) => x.id === productMatch[1]);
    if (p === undefined) return refuse(res, 404, "not found");
    for (const [key, value] of Object.entries(body ?? {})) {
      if (!PRODUCT_TEXT_KEYS.has(key)) return refuse(res, 400, `update product: column "${key}" is not editable`);
      p[key] = value;
    }
    return json(res, p);
  }

  const imageMatch = path.match(/^\/api\/v1\/products\/([^/]+)\/image$/);
  if (req.method === "PUT" && imageMatch) {
    if (!isManager(flow) || scenario === "refused-product") return refuse(res, 403, "Forbidden");
    const p = products.find((x) => x.id === imageMatch[1]);
    if (p === undefined) return refuse(res, 404, "not found");
    return json(res, { product_id: p.id, byte_size: raw.byteLength });
  }

  if (req.method === "DELETE" && imageMatch) {
    if (!isManager(flow) || scenario === "refused-product") return refuse(res, 403, "Forbidden");
    const p = products.find((x) => x.id === imageMatch[1]);
    if (p === undefined) return refuse(res, 404, "not found");
    return json(res, { status: "DELETED" });
  }

  if (req.method === "GET" && path === "/api/v1/uoms") {
    return json(res, { items: UOMS, next_cursor: null, limit: 100 });
  }

  // ---- the platform keys (staff wide read; the core does not serve it yet, the parity fact the waits state names) ----

  if (req.method === "GET" && path === "/api/v1/operator/platform-keys") {
    if (scenario === "unregistered-keys") {
      // The real mux answers the unregistered route with a bare 404.
      res.writeHead(404, { "content-type": "text/plain; charset=utf-8" });
      return res.end("404 page not found\n");
    }
    return json(res, {
      items: [
        {
          key: "launcher.tiles",
          platform_floor: { launcher: true, order: ["sales", "stock", "money"] },
          dealer_override: { launcher: true, order: ["money", "sales", "stock"] },
          credential_configured: false,
          updated_at: new Date(Date.now() - 20 * 60 * 1000).toISOString(),
          updated_by: "platform operator",
        },
        {
          key: "providers.card",
          platform_floor: { mode: "hosted" },
          dealer_override: null,
          credential_configured: true,
          updated_at: new Date(Date.now() - 3 * 24 * 60 * 60 * 1000).toISOString(),
          updated_by: "platform operator",
        },
      ],
      next_cursor: null,
    });
  }

  // ---- the configurable surfaces (the settings kit) ----
  // The curation writes are admin gated in the kit's service, so the refusal
  // is the service's own sentence, not the shared Forbidden body.

  const configMatch = path.match(/^\/api\/v1\/surfaces\/([^/]+)\/config$/);
  if (req.method === "GET" && configMatch) {
    if (!isManager(flow)) return refuse(res, 403, SETTINGS_ADMIN_READ_WORDS);
    const profile = url.searchParams.get("profile") ?? "";
    if (!PROFILES.includes(profile)) {
      return refuse(res, 422, "settings: profile must be counter, yard, driver, manager or admin");
    }
    const surface = decodeURIComponent(configMatch[1]);
    const userId = USERS[flow].user_id;
    const world = myWorld(userId);
    const wsRow = world.workspace.get(surface);
    return json(res, {
      surface_id: surface,
      profile,
      items_profile: profile,
      role_items: itemsOf(surfaces, surface, profile),
      user_items: world.items.get(surface) ?? [],
      workspace: wsRow ?? {
        split_pct: 58,
        arrangement: "PRIMARY_LEFT",
        primary_collapsed: false,
        secondary_collapsed: false,
        cost_hidden: false,
        source: "dealer",
      },
    });
  }

  const itemsMatch = path.match(/^\/api\/v1\/surfaces\/([^/]+)\/items$/);
  if (req.method === "PUT" && itemsMatch) {
    if (!isManager(flow) || scenario === "refused-role-config") {
      return refuse(res, 403, SETTINGS_ADMIN_WRITE_WORDS);
    }
    const profile = String(body?.profile ?? "");
    if (!PROFILES.includes(profile)) {
      return refuse(res, 422, "settings: profile must be counter, yard, driver, manager or admin");
    }
    const fault = validateItems(body?.items);
    if (fault !== "") return refuse(res, 422, fault);
    surfaces.set(decodeURIComponent(itemsMatch[1]) + "|" + profile, body.items);
    return json(res, { saved: true });
  }
  if (req.method === "DELETE" && itemsMatch) {
    if (!isManager(flow) || scenario === "refused-role-config") {
      return refuse(res, 403, SETTINGS_ADMIN_WRITE_WORDS);
    }
    const profile = url.searchParams.get("profile") ?? "";
    if (!PROFILES.includes(profile)) {
      return refuse(res, 422, "settings: profile must be counter, yard, driver, manager or admin");
    }
    const kind = url.searchParams.get("kind") ?? "";
    if (!PROFILE_KINDS.includes(kind)) {
      return refuse(res, 422, "settings: item kind must be panel, column, chip or action");
    }
    const key = decodeURIComponent(itemsMatch[1]) + "|" + profile;
    surfaces.set(key, itemsOf(surfaces, decodeURIComponent(itemsMatch[1]), profile).filter((it) => it.kind !== kind));
    return json(res, { reset: true });
  }

  const myItemsMatch = path.match(/^\/api\/v1\/surfaces\/([^/]+)\/my-items$/);
  if (req.method === "PUT" && myItemsMatch) {
    const fault = validateItems(body?.items);
    if (fault !== "") return refuse(res, 422, fault);
    myWorld(USERS[flow].user_id).items.set(decodeURIComponent(myItemsMatch[1]), body.items);
    return json(res, { saved: true });
  }
  if (req.method === "DELETE" && myItemsMatch) {
    myWorld(USERS[flow].user_id).items.delete(decodeURIComponent(myItemsMatch[1]));
    return json(res, { reset: true });
  }

  const myWorkspaceMatch = path.match(/^\/api\/v1\/surfaces\/([^/]+)\/my-workspace$/);
  if (req.method === "PUT" && myWorkspaceMatch) {
    const surface = decodeURIComponent(myWorkspaceMatch[1]);
    const world = myWorld(USERS[flow].user_id);
    const key = String(body?.idempotency_key ?? "");
    if (key === "") {
      return refuse(res, 422, "settings: idempotency_key is required on a workspace write");
    }
    const prev = world.workspace.get(surface);
    if (prev !== undefined && prev.write_key === key) {
      const { write_key: _drop, ...row } = prev;
      return json(res, { ...row, replayed: true });
    }
    let split = Number(body?.split_pct ?? 0);
    if (split === 0) split = 58;
    if (split < 20 || split > 80) {
      return refuse(res, 422, "settings: workspace split_pct must be between 20 and 80");
    }
    const arrangement = body?.arrangement === "PRIMARY_RIGHT" ? "PRIMARY_RIGHT" : "PRIMARY_LEFT";
    const row = {
      split_pct: split,
      arrangement,
      primary_collapsed: body?.primary_collapsed === true,
      secondary_collapsed: body?.secondary_collapsed === true,
      cost_hidden: body?.cost_hidden === true,
      source: "user",
      write_key: key,
    };
    world.workspace.set(surface, row);
    const { write_key: _drop, ...answer } = row;
    return json(res, answer);
  }
  if (req.method === "DELETE" && myWorkspaceMatch) {
    myWorld(USERS[flow].user_id).workspace.delete(decodeURIComponent(myWorkspaceMatch[1]));
    return json(res, { reset: true });
  }

  // ---- deployment settings: the brand (held), the card rail, the counter's policy, the walkthrough ----
  // The brand pair is registered in the contract and not yet served by the
  // real core (the parity ledger's own hold), so the stub answers the mux's
  // bare 404 unless the brand-live scenario flips it, mirroring reality.

  const brandMatch = path.match(/^\/api\/v1\/tenants\/([^/]+)\/brand$/);
  if (brandMatch && (req.method === "GET" || req.method === "PUT")) {
    if (scenario !== "brand-live") {
      res.writeHead(404, { "content-type": "text/plain; charset=utf-8" });
      return res.end("404 page not found\n");
    }
    if (req.method === "PUT") {
      brand = {
        tenant_id: decodeURIComponent(brandMatch[1]),
        display_name: String(body?.display_name ?? ""),
        logo_attachment_id: body?.logo_attachment_id ?? null,
        primary_color: body?.primary_color ?? null,
        footer_text: String(body?.footer_text ?? ""),
      };
      return json(res, brand);
    }
    return json(res, brand ?? {
      tenant_id: decodeURIComponent(brandMatch[1]),
      display_name: "Gable Landscape Supply",
      logo_attachment_id: null,
      primary_color: null,
      footer_text: "Thank you for growing with us",
    });
  }

  if (req.method === "GET" && path === "/api/v1/payments/config") {
    return json(res, { configured: true, merchant_id: payments.merchant_id, processor: payments.processor, currency: payments.currency, simulated: true });
  }

  if (req.method === "GET" && path === "/api/v1/pos/policy") {
    return json(res, posPolicy);
  }
  if (req.method === "PUT" && path === "/api/v1/pos/policy") {
    if (!isManager(flow) || scenario === "refused-policy") return refuse(res, 403, "Forbidden");
    const fault = validatePosPolicy(body ?? {});
    if (fault !== "") return refuse(res, 422, fault);
    const before = posPolicy;
    posPolicy = { ...before, ...normalizePosPolicy(body) };
    for (const key of Object.keys(normalizePosPolicy(body))) {
      const beforeValue = JSON.stringify(before[key] ?? "");
      const afterValue = JSON.stringify(posPolicy[key]);
      if (beforeValue !== afterValue) {
        policyHistory.unshift({
          field: key,
          before: before[key] === undefined ? "" : String(before[key]),
          after: String(posPolicy[key]),
          who: USERS[flow].user_id,
          when: new Date().toISOString(),
        });
      }
    }
    return json(res, posPolicy);
  }
  if (req.method === "GET" && path === "/api/v1/pos/policy/history") {
    if (!isManager(flow) || scenario === "refused-policy") return refuse(res, 403, "Forbidden");
    return json(res, { items: policyHistory, next_cursor: null, limit: 50 });
  }

  if (req.method === "GET" && path === "/api/v1/tour") {
    if (scenario === "tour-live") {
      return json(res, { chapters: buildTourChapters(), can_edit: false });
    }
    // The refused scenario mirrors a stale can_edit: the read said editor,
    // the write answers the platform gate's own sentence, the way a real
    // core would answer a dealer role whose read had lied to it.
    if (scenario === "tour-platform" || scenario === "tour-refused") {
      return json(res, { chapters: buildTourChapters(), can_edit: true });
    }
    return json(res, {});
  }
  if (req.method === "PUT" && path === "/api/v1/platform/tour/policy") {
    if (!isManager(flow)) return refuse(res, 403, "Forbidden");
    if (scenario === "tour-refused") {
      return refuse(res, 403, `tour: the walkthrough is the platform administrator's to set up, ${USERS[flow].role_code} cannot change its copy or its demo switch`);
    }
    return json(res, { saved: true });
  }
  if (req.method === "PUT" && path === "/api/v1/platform/tour/steps") {
    if (!isManager(flow)) return refuse(res, 403, "Forbidden");
    if (scenario === "tour-refused") {
      return refuse(res, 403, `tour: the walkthrough is the platform administrator's to set up, ${USERS[flow].role_code} cannot change its copy or its demo switch`);
    }
    const line = String(body?.line ?? "");
    if (line === "") return refuse(res, 422, "tour: line is what the step says, a step with nothing to say is a step to delete");
    if ([...line].length > 72) {
      return refuse(res, 422, `tour: line is ${[...line].length} characters, a step says one line and the limit is 72, because a walkthrough speaks in headlines`);
    }
    return json(res, { saved: true });
  }

  return notFound(res);
}

// The masked messages export: a text row with an author, an event row, and a
// deleted row the server has tombstoned. The masking is the server's own rule
// stated in the schema; the double carries its shape.
function buildMessageExport() {
  const at = (minutes) => new Date(Date.now() - minutes * 60 * 1000).toISOString();
  return [
    {
      id: "aa000000-0000-4000-9000-000000000001",
      channel_id: "bb000000-0000-4000-9000-000000000001",
      kind: "TEXT",
      author_name: "Dealer admin (demo)",
      body: "Yard two, the point loma truck is back with the pallet racking.",
      created_at: at(50),
      deleted_at: null,
    },
    {
      id: "aa000000-0000-4000-9000-000000000002",
      channel_id: "bb000000-0000-4000-9000-000000000001",
      kind: "EVENT",
      body: "order 4f000000-0000-4000-9000-000000000001 moved to PICKED",
      created_at: at(40),
      deleted_at: null,
    },
    {
      id: "aa000000-0000-4000-9000-000000000003",
      channel_id: "bb000000-0000-4000-9000-000000000002",
      kind: "APPROVAL",
      body: "payment of $196.80 approved against order 4f000000-0000-4000-9000-000000000002",
      created_at: at(30),
      deleted_at: at(10),
    },
  ];
}

const server = createServer((req, res) => {
  handle(req, res).catch(() => refuse(res, 500, "stub error"));
});

server.listen(port, "127.0.0.1", () => {
  process.stdout.write(`dealer-admin stub on 127.0.0.1:${port}\n`);
});
