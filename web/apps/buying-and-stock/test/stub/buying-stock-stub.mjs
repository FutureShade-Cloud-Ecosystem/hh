// The Buying and Stock app's local stub: the demo way in and the procurement,
// inventory and counts routes the app calls, answering with the core's wire
// shapes and the core's words. It is a development and test double only; the
// real routes are the core's. Two contract tests hold every answer here to the
// generated wire descriptors and every refusal sentence to the core's source
// (test/stub-contract.test.mjs, test/inventory-contract.test.mjs), so the
// browser legs never lean on an answer the server would not give.
//
// The list pages follow the contract's keys where the core writes them (the
// shortfall worklist and the product incoming read answer under "items") and
// the core's own domain keys where the core writes those (on_hand, moves,
// pending, branches, reservations, transfers, lines, pairs, reorder_points);
// the inventory contract test pins both sides of that to the core's source.
//
// The world is invented and small. The procurement half is one yard, four
// vendors, three managed lines (one of them the K08 gap: promised to
// customers, never inbound) and four purchase orders. The inventory and
// counts half is the second yard pair the demo tenant sheet describes (Point
// Loma and Julian), four trade products, a truck between the yards, one
// reservation, and one polymeric sand line sold before its receipt at Julian
// so the negative-stock read has its row. Dates are cut from the host's own
// day so no calendar literal is written.
import { createServer } from "node:http";
import { createPurchRoutes } from "./purch-routes.mjs";

const port = Number(process.env.ROLE_STUB_PORT ?? 4969);

const BRANCH = "b0000000-0000-4000-8000-000000000001";
const PLM = "e1000000-0000-4000-8000-000000000001";
const JUL = "e1000000-0000-4000-8000-000000000002";
const FLOWS = [
  { flow: "procurement-manager", role_code: "procurement_manager", label: "Procurement manager" },
  { flow: "yard-manager", role_code: "yard_manager", label: "Yard manager" },
  { flow: "payables-receivables", role_code: "payables_receivables", label: "Payables and Receivables" },
  { flow: "inside-sales", role_code: "inside_sales", label: "Inside sales" },
];

const USERS = {
  "inside-sales": {
    user_id: "de110000-0000-4000-9000-0000000000a2",
    email: "inside.sales@hh-demo.invalid",
    display_name: "Inside sales (demo)",
    role_code: "inside_sales",
    role_name: "Inside sales",
    tier: 2,
    scope: "BRANCH",
  },
  "procurement-manager": {
    user_id: "de110000-0000-4000-9000-000000000008",
    email: "procurement.manager@hh-demo.invalid",
    display_name: "Procurement manager (demo)",
    role_code: "procurement_manager",
    role_name: "Procurement manager",
    tier: 2,
    scope: "BRANCH",
  },
  "yard-manager": {
    user_id: "de110000-0000-4000-9000-000000000003",
    email: "yard.manager@hh-demo.invalid",
    display_name: "Yard manager (demo)",
    role_code: "yard_manager",
    role_name: "Yard manager",
    tier: 2,
    scope: "BRANCH",
  },
  "payables-receivables": {
    user_id: "de110000-0000-4000-9000-0000000000a1",
    email: "payables.receivables@hh-demo.invalid",
    display_name: "Payables and Receivables (demo)",
    role_code: "payables_receivables",
    role_name: "Payables and Receivables",
    tier: 2,
    scope: "BRANCH",
  },
};

// The token's sub names the caller; the counts gate reads it (maker against checker).
function callerOf(req) {
  const header = req.headers.authorization ?? "";
  const token = header.startsWith("Bearer ") ? header.slice(7) : "";
  const part = token.split(".")[1] ?? "";
  try {
    const payload = JSON.parse(Buffer.from(part, "base64url").toString("utf8"));
    return typeof payload.sub === "string" ? payload.sub : "";
  } catch {
    return "";
  }
}

const DEFAULT_BUSINESS_ZONE = "America/Los_Angeles";
let businessZone = DEFAULT_BUSINESS_ZONE;
// The surface config route's reads so far and how long it stalls before answering (a test's knob).
let configReads = 0;
let configDelayMs = 0;
let clockOffsetMs = 0;
const nowMs = () => Date.now() + clockOffsetMs;

function mintToken(sub) {
  const part = (value) => Buffer.from(JSON.stringify(value), "utf8").toString("base64url");
  const issuedAtSeconds = Math.floor(nowMs() / 1000);
  const payload = { iss: "hh-demo-stub", sub, aud: "staff", iat: issuedAtSeconds, exp: issuedAtSeconds + 3600 };
  return `${part({ alg: "ES256", typ: "JWT" })}.${part(payload)}.c3R1Yi1zaWduYXR1cmUtYnl0ZXM`;
}

// One source for every date the stub writes: the served business_zone,
// the same zone the surface config route answers to the browser.
function houseDayOffset(offset) {
  const cur = new Intl.DateTimeFormat("en-CA", { timeZone: businessZone, year: "numeric", month: "2-digit", day: "2-digit" }).format(new Date(nowMs()));
  const at = new Date(`${cur}T12:00:00Z`);
  at.setUTCDate(at.getUTCDate() + offset);
  return at.toISOString().slice(0, 10);
}

function dayOffset(offset) {
  return houseDayOffset(offset);
}

function uuid(kind, n) {
  return `${kind}0000000-0000-4000-8000-${String(n).padStart(12, "0")}`;
}

// The core's isUUIDString, exactly: the 8-4-4-4-12 form or 32 hex digits, nothing looser, because
// anything looser reaches the database's uuid cast and comes back a 500.
function isUuid(value) {
  if (typeof value !== "string") return false;
  const hex = (ch) => /[0-9a-fA-F]/.test(ch);
  if (value.length === 36) {
    for (let i = 0; i < 36; i += 1) {
      if (i === 8 || i === 13 || i === 18 || i === 23) {
        if (value[i] !== "-") return false;
      } else if (!hex(value[i])) return false;
    }
    return true;
  }
  if (value.length === 32) return [...value].every(hex);
  return false;
}

// The ceiling the core states on every line door: numeric(19,4) overflows past 1e12.
const MAX_LINE_QTY = 1e12;

const NOW_ISO = () => new Date(nowMs()).toISOString();

// ---- the world ---------------------------------------------------------------------------------

const VENDORS = [
  { n: 1, no: "V-ALC", name: "Alcli (demo)", terms: 30 },
  { n: 2, no: "V-TEC", name: "Techo Bloc (demo)", terms: 30 },
  { n: 3, no: "V-UNI", name: "Unilock (demo)", terms: 45 },
  { n: 4, no: "V-RYM", name: "Rymar (demo)", terms: 30 },
];

// The inventory and counts half's makers, invented names in the trade's own shapes. They are not the
// purchasing areas' vendor list (the vendors area reads VENDORS); a lookup by number reads both.
const GLS_VENDORS = [
  { n: 5, no: "V-CP", name: "Copperline Pavers (demo)", terms: 30 },
  { n: 6, no: "V-GRQ", name: "Granite Run Quarry (demo)", terms: 30 },
  { n: 7, no: "V-HS", name: "Heronfield Stone (demo)", terms: 45 },
  { n: 8, no: "V-PBS", name: "Prairie Bend Soils (demo)", terms: 30 },
];

const ALL_VENDORS = [...VENDORS, ...GLS_VENDORS];

const VENDOR_WARRANTY_CREDITS = {
  [uuid("a", 1)]: {
    claim_id: "c1000000-0000-4000-8000-000000000104",
    claim_no: "WCL-104",
    amount_cents: 26400,
    description: "8 pieces of Paver 60mm Slate",
    summary: "8 pieces of Paver 60mm Slate",
    vendor_id: uuid("a", 1),
    vendor_name: "Alcli (demo)",
  },
};

// The managed lines: what the worklist grades. Quantities are base units.
const PRODUCTS = [
  { n: 1, sku: "DBS-RYMAR-TURF", name: "Rymar Artificial Turf 40mm", uom: "pc", vendor: 4, onHand: 1200, reserved: 900, point: 600, reorderQty: 600, multiple: 600, cost: 3100, lead: 7 },
  { n: 2, sku: "DBS-BRUS-DIM", name: "Brussels Dimensional", uom: "pc", vendor: 1, onHand: 160, reserved: 200, point: 200, reorderQty: 240, multiple: 240, cost: 840, lead: 5 },
  { n: 3, sku: "DBS-AGG-34CLEAR", name: "3/4 Clear Stone", uom: "T", vendor: 1, onHand: 12, reserved: 0, point: 20, reorderQty: 10, multiple: 10, cost: 2500, lead: 3 },
  // Stocked lines the open orders buy that are not on the worklist: the seeded orders do not cover the gap.
  { n: 4, sku: "TB-PAVER-60", name: "Techo Bloc Paver 60mm", uom: "pc", vendor: 2, onHand: 5000, reserved: 0, point: 100, reorderQty: 100, multiple: 100, cost: 82_000, lead: 4, unmanaged: true },
  { n: 5, sku: "UNI-WALL-CAP", name: "Unilock Wall Cap", uom: "pc", vendor: 3, onHand: 800, reserved: 0, point: 100, reorderQty: 100, multiple: 100, cost: 24_000, lead: 4, unmanaged: true },
  { n: 6, sku: "ALC-STEP-48", name: "Alcli Step 48in", uom: "pc", vendor: 1, onHand: 90, reserved: 0, point: 10, reorderQty: 10, multiple: 10, cost: 2500, lead: 4, unmanaged: true },
];

const SEEDED_POS = [
  { n: 141, vendor: 2, status: "ORDERED", expect: 2, method: "DELIVERY", lines: [{ product: 4, qty: 12, cost: 82_000 }] },
  { n: 136, vendor: 1, status: "ORDERED", expect: 4, method: "PICKUP", lines: [{ product: 6, qty: 10, cost: 2500 }] },
  { n: 67, vendor: 3, status: "PARTIAL", expect: null, method: "DELIVERY", lines: [{ product: 5, qty: 10, cost: 24_000, received: 6 }] },
  { n: 66, vendor: 1, status: "RECEIVED", expect: -3, method: "PICKUP", lines: [{ product: 6, qty: 10, cost: 2500, received: 10 }, { product: 3, qty: 4, cost: 2500, received: 4 }] },
];

// ---- the inventory and counts world (Point Loma and Julian) -------------------------------------

// The trade products the stock reads answer for. Unmanaged: they never sit on
// the procurement worklist, which grades the po world's managed lines only.
// The seeded stock, restated as a maker so a reset restores what the writes
// moved (holds, ships and posted counts mutate these numbers in place).
const GLS_STOCK_SEED = {
  1: { [PLM]: { onHand: 400, allocated: 60 }, [JUL]: { onHand: 0, allocated: 0 } },
  2: { [PLM]: { onHand: 120, allocated: 0 }, [JUL]: { onHand: 40, allocated: 0 } },
  3: { [PLM]: { onHand: 8, allocated: 0 }, [JUL]: { onHand: 2, allocated: 0 } },
  4: { [PLM]: { onHand: 25, allocated: 5 }, [JUL]: { onHand: -6, allocated: 0 } },
};

const GLS_PRODUCTS = [
  { n: 1, sku: "GLS-PAV-60", name: "Copperline Paver 60mm", uom: "pc", vendor: 5, cost: 8200, list: 11_900, mfr: "Copperline Pavers", family: "Pavers", size: "60mm", stock: {} },
  { n: 2, sku: "GLS-WALL-CAP", name: "Heronfield Wall Cap", uom: "pc", vendor: 7, cost: 5400, list: 7_900, mfr: "Heronfield Stone", family: "Wall block", size: "12in", stock: {} },
  { n: 3, sku: "GLS-AGG-DG", name: "Decomposed Granite", uom: "T", vendor: 6, cost: 6500, list: 9_800, mfr: "Granite Run Quarry", family: "Aggregates", size: "minus 3/4", stock: {} },
  { n: 4, sku: "GLS-SAND-POLY", name: "Polymeric Sand", uom: "BG", vendor: 8, cost: 1900, list: 3_200, mfr: "Prairie Bend Soils", family: "Sand", size: "50 lb", stock: {} },
];

function seedStock() {
  for (const p of GLS_PRODUCTS) {
    p.stock = JSON.parse(JSON.stringify(GLS_STOCK_SEED[p.n]));
  }
}
seedStock();

// One invented image slot for every trade product: a 1 by 1 transparent PNG, the bytes the image
// read serves so the lookup's thumbs show a served picture and a leg can tell it from the fallback.
const PRODUCT_IMAGE_BYTES = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==",
  "base64",
);

const glsProduct = (n) => GLS_PRODUCTS.find((p) => p.n === n);
const glsId = (n) => uuid("f", 100 + n);

// The moves the ledger answers for the paver and the cap.
const GLS_MOVES = [
  { product: 1, branch: PLM, qty: 400, direction: "IN", moveType: "RECEIPT", source: "purchase_order", ref: "PO-PLM-0110", cost: 8200 },
  { product: 1, branch: PLM, qty: 2, direction: "OUT", moveType: "COUNT", source: "count_adjustment", ref: "CS-PLM-0007", cost: 8200 },
  { product: 2, branch: PLM, qty: 40, direction: "OUT", moveType: "SHIP", source: "stock_transfer", ref: "TRF-PLM-0001", cost: 5400 },
  { product: 3, branch: JUL, qty: 5, direction: "IN", moveType: "RECEIPT", source: "purchase_order", ref: "PO-JUL-0031", cost: 6500 },
];

const REORDER_POINTS = [
  { product: 1, branch: PLM, point: 100, qty: 100, multiple: 50, vendor: 5, lead: 4, notes: "" },
  { product: 3, branch: PLM, point: 10, qty: 10, multiple: 10, vendor: 6, lead: 3, notes: "" },
  { product: 4, branch: PLM, point: 15, qty: 20, multiple: 5, vendor: 8, lead: 7, notes: "spring restock" },
];

// The second yard's lines awaiting fulfilment (sold, not yet moved).
const GLS_PENDING = [
  { order: "SO-3301", product: 1, qty: 12, saleType: "DELIVERED" },
  { order: "SO-3302", product: 3, qty: 3, saleType: "COLLECT_LATER" },
];

// The open sales orders each managed line's shortfall holds up (change 5 slice S3), oldest first.
// The turf row holds up five, so the row names the oldest three and counts two more; the
// dimensional row one; nothing waits on the stone row and its row carries neither key.
const SHORT_SALES_ORDERS = {
  1: ["SO-3305", "SO-3311", "SO-3312", "SO-3318", "SO-3324"],
  2: ["SO-3314"],
};

const REASON_CODES = [
  { code: "DAMAGE", description: "Damaged / broken stock write-down", account: "5200" },
  { code: "SHRINK", description: "Shrinkage / theft / unaccounted loss", account: "5200" },
  { code: "FOUND", description: "Found stock (positive adjustment)", account: "5200" },
  { code: "MISCOUNT", description: "Prior miscount correction", account: "5200" },
  { code: "EXPIRY", description: "Expired / obsolete write-off", account: "5200" },
];

let world;

function productRow(p) {
  const onOrder = world.pos
    .filter((po) => po.status === "ORDERED" || po.status === "PARTIAL")
    .flatMap((po) => po.lines)
    .filter((l) => l.product === p.n)
    .reduce((sum, l) => sum + (l.qty - (l.received ?? 0)), 0);
  // PENDING on lane B core (FROM-PRINCIPAL 72): the row carries the draft that does not cover it.
  const draft = world.pos.find((po) => po.status === "DRAFT" && po.lines.some((l) => l.product === p.n));
  const available = p.onHand - p.reserved;
  const projected = available + onOrder;
  const short = Math.max(p.point - projected, 0);
  const suggested = short === 0 ? 0 : Math.ceil(short / p.multiple) * p.multiple;
  const vendor = ALL_VENDORS.find((v) => v.n === p.vendor);
  // The orders the shortfall holds up (change 5 slice S3): at most three numbers, oldest first,
  // plus the count of the rest, and neither key when nothing waits, the core's shape.
  const waiting = SHORT_SALES_ORDERS[p.n] ?? [];
  const shortNos = waiting.slice(0, 3);
  const shortMore = Math.max(waiting.length - 3, 0);
  return {
    product_id: uuid("f", p.n),
    sku: p.sku,
    product_name: p.name,
    branch_id: BRANCH,
    branch_code: "PLM",
    uom: p.uom,
    on_hand_base: p.onHand,
    reserved_base: p.reserved,
    available_base: available,
    in_transit_base: 0,
    on_order_base: onOrder,
    projected_base: projected,
    reorder_point_base: p.point,
    reorder_qty_base: p.reorderQty,
    order_multiple_base: p.multiple,
    suggested_qty_base: suggested,
    short_base: short,
    preferred_vendor_id: uuid("a", vendor.n),
    preferred_vendor_name: vendor.name,
    lead_time_days: p.lead,
    avg_cost_cents: p.cost,
    suggested_value_cents: suggested * p.cost,
    ...(draft === undefined ? {} : { draft_po_id: draft.id, draft_po_no: draft.no }),
    ...(shortNos.length === 0 ? {} : { short_order_nos: shortNos, short_order_more: shortMore }),
  };
}

function reset() {
  world = { pos: [], counter: 142, lineCounter: 0, keys: new Map() };
  for (const s of SEEDED_POS) {
    world.pos.push({
      id: uuid("c", s.n),
      no: `PO-PLM-${s.n}`,
      vendor: s.vendor,
      status: s.status,
      expect: s.expect === null ? null : dayOffset(s.expect),
      method: s.method,
      created: NOW_ISO(),
      lines: s.lines.map((l) => ({ id: uuid("d", ++world.lineCounter), ...l })),
    });
  }
  // What is coming in for the trade products (change 5 slice S4's coming-in table): one placed
  // order buying the paver, promised in a few days. Not in the register the buyers' screen draws,
  // which is the po world's own list above.
  world.glsIncoming = [
    { product: 1, no: "PO-PLM-0151", vendor: 5, branch: PLM, expected: dayOffset(4), qty: 240, received: 0, status: "ORDERED" },
  ];
  resetInventoryWorld();
}

/** One catalogue row, the shape the picker reads, for either half's products. */
function catalogueRow(p) {
  const gls = p.mfr !== undefined;
  return {
    id: gls ? glsId(p.n) : uuid("f", p.n),
    sku: p.sku,
    name: p.name,
    description: p.name,
    manufacturer: gls ? `${p.mfr} (demo)` : "HH (demo)",
    collection: "",
    color: "",
    finish: "",
    application: "",
    color_family: gls ? p.family : "",
    texture: "",
    product_size: gls ? p.size : "",
    sold_by: p.uom,
    base_uom: p.uom,
    dimensions_lwh: "",
    is_featured: false,
    is_special_order: false,
    contractor_pricing_event_discount: false,
    full_skid_quantity: null,
    layer_quantity: null,
    pallet_count: null,
    minimum_order_qty: null,
    is_active: true,
    metadata: null,
    list_price_cents: p.cost,
  };
}

/** The catalogue entry for an id from either half's id space. */
function catalogueById(id) {
  const po = PRODUCTS.find((p) => uuid("f", p.n) === id);
  if (po !== undefined) return catalogueRow(po);
  const gls = GLS_PRODUCTS.find((p) => glsId(p.n) === id);
  return gls === undefined ? undefined : catalogueRow(gls);
}

// ---- the inventory and counts state -----------------------------------------------------------------

function stockAt(branch, productN) {
  const p = glsProduct(productN);
  return p.stock[branch] ?? { onHand: 0, allocated: 0 };
}

function bumpStock(branch, productN, delta) {
  const p = glsProduct(productN);
  const at = p.stock[branch] ?? (p.stock[branch] = { onHand: 0, allocated: 0 });
  at.onHand += delta;
}

function resetInventoryWorld() {
  seedStock();
  world.trfCounter = 4;
  world.sheetCounter = 8;
  world.receiptCounter = 90;
  world.nudges = new Set();
  world.policies = new Map();
  world.recons = new Map();
  // Transfers: one on the truck, one draft, one history row.
  world.transfers = [
    {
      id: uuid("t", 1), no: "TRF-PLM-0001", from: PLM, to: JUL, status: "IN_TRANSIT",
      shipped: NOW_ISO(), received: "", eta: houseDayOffset(2), created: NOW_ISO(), notes: "the week's cap run",
      lines: [{ id: uuid("u", 1), product: 2, qty: 40, shipped: 40, received: 0, cost: 5400 }],
    },
    {
      id: uuid("t", 2), no: "TRF-PLM-0002", from: PLM, to: JUL, status: "DRAFT",
      shipped: "", received: "", eta: "", created: NOW_ISO(), notes: "pavers for the Julian job",
      lines: [{ id: uuid("u", 2), product: 1, qty: 100, shipped: 0, received: 0, cost: 8200 }],
    },
    {
      id: uuid("t", 3), no: "TRF-JUL-0001", from: JUL, to: PLM, status: "RECEIVED",
      shipped: NOW_ISO(), received: NOW_ISO(), eta: "", created: NOW_ISO(), notes: "granite back from the ridge job",
      lines: [{ id: uuid("u", 3), product: 3, qty: 5, shipped: 5, received: 5, cost: 6500 }],
    },
  ];
  world.transit = [
    { from: PLM, to: JUL, days: 1, at: NOW_ISO() },
    { from: JUL, to: PLM, days: 1, at: NOW_ISO() },
  ];
  // One active hold: the Summit Ridge job's pavers at Point Loma.
  world.reservations = [
    {
      id: uuid("r", 1), product: 1, branch: PLM, location: PLM, resType: "ORDER", hold: "HARD",
      qty: 60, status: "ACTIVE", sourceType: "sales_order", ref: "SO-2201",
      reason: "Summit Ridge job hold", expires: dayOffset(2), created: NOW_ISO(), closed: "",
    },
  ];
  // One posted sheet, so the list shows a finished count and the cancel refusal has its row.
  world.sheets = [
    {
      id: uuid("s", 1), no: "CS-PLM-0001", branch: PLM, scope: "FULL", status: "POSTED",
      freezeAt: NOW_ISO(), countedBy: "de110000-0000-4000-9000-000000000003",
      approvedBy: "de110000-0000-4000-9000-000000000008", approvedAt: NOW_ISO(), postedAt: NOW_ISO(),
      notes: "the first Point Loma count", created: NOW_ISO(),
      lines: [
        { id: uuid("v", 1), product: 1, location: PLM, snapshot: 402, counted: 400, variance: -2, cost: 8200, value: -16400, reason: "SHRINK", move: uuid("m", 1) },
        { id: uuid("v", 2), product: 3, location: PLM, snapshot: 8, counted: 8, variance: 0, cost: 6500, value: 0, reason: "", move: "" },
      ],
    },
  ];
}

const BRANCH_VIEWS = {
  [PLM]: { code: "PLM", name: "Point Loma yard" },
  [JUL]: { code: "JUL", name: "Julian yard" },
};

const branchCode = (id) => BRANCH_VIEWS[id]?.code ?? "";
const branchName = (id) => (id === BRANCH ? "Loma yard" : BRANCH_VIEWS[id]?.name ?? "");

reset();

function lineView(l, n) {
  const p = PRODUCTS.find((x) => x.n === l.product);
  const received = l.received ?? 0;
  return {
    id: l.id,
    line_no: n,
    product_id: uuid("f", p.n),
    sku: p.sku,
    description: p.name,
    qty_ordered_base: l.qty,
    qty_received_base: received,
    qty_billed_base: l.billed ?? 0,
    qty_outstanding_base: l.qty - received,
    uom: p.uom,
    ...(flags.costsHidden ? {} : { unit_cost_cents: l.cost, line_total_cents: l.qty * l.cost, billed_cents: l.billedCents ?? 0 }),
  };
}

function poView(po) {
  const vendor = ALL_VENDORS.find((v) => v.n === po.vendor);
  const subtotal = po.lines.reduce((sum, l) => sum + l.qty * l.cost, 0);
  const applied = po.warranty_credit_applied_cents ?? 0;
  const netTotal = po.net_total_cents ?? Math.max(0, subtotal - applied);
  const view = {
    id: po.id,
    po_no: po.no,
    vendor_id: uuid("a", vendor.n),
    vendor_name: vendor.name,
    branch_id: BRANCH,
    status: po.status,
    notes: "",
    line_count: po.lines.length,
    currency: "CAD",
    fx_rate: 1,
    delivery_method: po.method,
    ...(flags.costsHidden
      ? {}
      : {
          subtotal_cents: subtotal,
          subtotal_cad_cents: subtotal,
          warranty_credit_applied_cents: applied,
          net_total_cents: netTotal,
        }),
    ...(po.warranty_claim_id !== undefined && po.warranty_claim_id !== null ? { warranty_claim_id: po.warranty_claim_id } : {}),
    ...(po.warranty_claim_no !== undefined && po.warranty_claim_no !== null ? { warranty_claim_no: po.warranty_claim_no } : {}),
    created_at: po.created,
    updated_at: po.created,
  };
  if (po.expect !== null) view.expected_at = po.expect;
  if (po.status !== "DRAFT") view.ordered_at = po.created;
  return view;
}

function poDetail(po) {
  const detail = {
    ...poView(po),
    lines: po.lines.map((l, i) => lineView(l, i + 1)),
    receipts: purch.receiptsFor(po.id),
  };
  const credit = VENDOR_WARRANTY_CREDITS[detail.vendor_id];
  if (credit && po.status === "DRAFT") {
    if (po.warranty_claim_id === credit.claim_id || (po.warranty_credit_applied_cents ?? 0) === 0) {
      detail.open_warranty_credit = credit;
      if (po.warranty_claim_id === credit.claim_id && !detail.warranty_claim_no) {
        detail.warranty_claim_no = credit.claim_no;
      }
    }
  }
  return detail;
}

// ---- inventory and counts view helpers -------------------------------------------------------------

/** Reads ?product_id= the way the core's productIDParam grades it. */
function productIDParam(url) {
  const raw = (url.searchParams.get("product_id") ?? "").trim();
  if (raw === "") return { error: "product_id is required" };
  if (!isUuid(raw)) return { error: "product_id must be a product UUID" };
  return { value: raw };
}

/** Reads the optional ?branch_id= the way the core's branchIDParam grades it. */
function branchIDParam(url) {
  const raw = (url.searchParams.get("branch_id") ?? "").trim();
  if (raw === "") return { value: "" };
  if (!isUuid(raw)) return { error: "branch_id must be a branch id, copy it from the branch list, not the branch name" };
  return { value: raw };
}

function onHandRow(p, branch) {
  const at = p.stock[branch];
  return {
    product_id: glsId(p.n),
    location_id: branch,
    branch_id: branch,
    on_hand_base: at.onHand,
    allocated_base: at.allocated,
    available_base: at.onHand - at.allocated,
    avg_cost_cents: p.cost,
  };
}

function moveRow(m) {
  const p = glsProduct(m.product);
  return {
    id: uuid("m", GLS_MOVES.indexOf(m) + 1),
    product_id: glsId(m.product),
    branch_id: m.branch,
    qty_base: m.qty,
    uom: p.uom,
    direction: m.direction,
    move_type: m.moveType,
    source_type: m.source,
    source_ref_id: m.ref,
    unit_cost_cents: m.cost,
    occurred_at: NOW_ISO(),
  };
}

function branchStockRow(p, branch) {
  const at = p.stock[branch];
  const inTransit = world.transfers
    .filter((t) => t.status === "IN_TRANSIT" && t.to === branch)
    .flatMap((t) => t.lines)
    .filter((l) => l.product === p.n)
    .reduce((sum, l) => sum + (l.shipped - l.received), 0);
  const soft = world.reservations
    .filter((r) => r.branch === branch && r.product === p.n && r.status === "ACTIVE" && r.hold === "SOFT")
    .reduce((sum, r) => sum + r.qty, 0);
  return {
    product_id: glsId(p.n),
    branch_id: branch,
    branch_code: branchCode(branch),
    branch_name: branchName(branch),
    on_hand_base: at.onHand,
    reserved_base: at.allocated,
    in_transit_base: inTransit,
    available_base: at.onHand - at.allocated,
    soft_held_base: soft,
    avg_cost_cents: p.cost,
  };
}

function browseRow(p) {
  const buckets = Object.keys(p.stock);
  const onHand = buckets.reduce((sum, b) => sum + p.stock[b].onHand, 0);
  const reserved = buckets.reduce((sum, b) => sum + p.stock[b].allocated, 0);
  // The branches whose record shows nothing available (change 5 slice S4): the core writes the
  // names byte-absent when there are none, so the stub does the same.
  const outAt = buckets.filter((b) => p.stock[b].onHand - p.stock[b].allocated <= 0).map(branchName);
  return {
    product_id: glsId(p.n),
    sku: p.sku,
    name: p.name,
    manufacturer: `${p.mfr} (demo)`,
    collection: "",
    color_family: p.family,
    texture: "",
    product_size: p.size,
    sold_by: p.uom,
    color: "",
    finish: "",
    application: "",
    is_featured: false,
    is_special_order: false,
    on_hand_base: onHand,
    reserved_base: reserved,
    available_base: onHand - reserved,
    in_transit_base: 0,
    avg_cost_cents: p.cost,
    ...(outAt.length > 0 ? { out_at_branches: outAt } : {}),
  };
}

function transitPair(from, to, row) {
  return {
    from_branch_id: from,
    from_branch_code: branchCode(from),
    from_branch_name: branchName(from),
    to_branch_id: to,
    to_branch_code: branchCode(to),
    to_branch_name: branchName(to),
    transit_days: row === undefined ? 0 : row.days,
    configured: row !== undefined,
    updated_at: row === undefined ? "" : row.at,
  };
}

function reservationRow(r) {
  return {
    id: r.id,
    product_id: glsId(r.product),
    branch_id: r.branch,
    location_id: r.location,
    res_type: r.resType,
    hold_mode: r.hold,
    qty_base: r.qty,
    status: r.status,
    source_type: r.sourceType,
    source_ref_id: r.ref,
    reason: r.reason,
    expires_at: r.expires,
    created_at: r.created,
    closed_at: r.closed,
  };
}

function closeReservation(r, status) {
  r.status = status;
  r.closed = NOW_ISO();
  if (r.hold === "HARD" && status !== "CONSUMED") bumpAlloc(r.branch, r.product, -r.qty);
}

function bumpAlloc(branch, productN, delta) {
  const p = glsProduct(productN);
  const at = p.stock[branch] ?? (p.stock[branch] = { onHand: 0, allocated: 0 });
  at.allocated += delta;
}

function policyFor(branch) {
  const stored = world.policies.get(branch);
  if (stored !== undefined) return stored;
  return {
    branch_id: branch,
    order_mode: "HARD",
    quote_mode: "SOFT",
    order_ttl_hours: 72,
    quote_ttl_hours: 72,
    is_default: true,
  };
}

function transferView(t) {
  return {
    id: t.id,
    transfer_no: t.no,
    from_branch_id: t.from,
    to_branch_id: t.to,
    status: t.status,
    notes: t.notes,
    shipped_at: t.shipped,
    received_at: t.received,
    created_at: t.created,
    eta: t.eta,
    lines: t.lines.map((l) => ({
      id: l.id,
      line_no: t.lines.indexOf(l) + 1,
      product_id: glsId(l.product),
      sku: glsProduct(l.product).sku,
      product_name: glsProduct(l.product).name,
      uom: glsProduct(l.product).uom,
      qty_base: l.qty,
      qty_shipped_base: l.shipped,
      qty_received_base: l.received,
      unit_cost_cents: l.cost,
      ship_move_id: "",
      recv_move_id: "",
    })),
  };
}

function reorderPointRow(r) {
  const gls = glsProduct(r.product);
  return {
    product_id: glsId(r.product),
    sku: gls.sku,
    product_name: gls.name,
    branch_id: r.branch,
    reorder_point_base: r.point,
    reorder_qty_base: r.qty,
    order_multiple_base: r.multiple,
    preferred_vendor_id: uuid("a", r.vendor),
    lead_time_days: r.lead,
    is_active: r.retired !== true,
    notes: r.notes,
  };
}

function sheetView(s) {
  const view = {
    id: s.id,
    sheet_no: s.no,
    branch_id: s.branch,
    scope: s.scope,
    status: s.status,
    notes: s.notes,
  };
  if (s.freezeAt !== "") view.freeze_at = s.freezeAt;
  if (s.countedBy !== "") view.counted_by = s.countedBy;
  if (s.approvedBy !== "") {
    view.approved_by = s.approvedBy;
    view.approved_at = s.approvedAt;
  }
  if (s.postedAt !== "") view.posted_at = s.postedAt;
  view.created_at = s.created;
  return view;
}

function sheetLineView(l) {
  const view = {
    id: l.id,
    sheet_id: "",
    product_id: l.product === null ? "" : glsId(l.product),
    location_id: l.location,
    snapshot_qty_base: l.snapshot,
    counted_qty_base: l.counted,
    variance_base: l.variance,
  };
  const gls = l.product === null ? undefined : glsProduct(l.product);
  if (gls !== undefined) {
    view.sku = gls.sku;
    view.product_name = gls.name;
  }
  if (l.counted !== null && l.cost !== undefined) view.unit_cost_cents = l.cost;
  if (l.value !== null && l.value !== undefined) view.variance_value_cents = l.value;
  if (l.reason !== "") view.reason_code = l.reason;
  if (l.move !== "") view.move_id = l.move;
  return view;
}

function sheetDetail(s) {
  const lines = s.lines.map((l) => ({ ...sheetLineView(l), sheet_id: s.id }));
  const counted = lines.filter((l) => l.counted_qty_base !== null);
  const varianceLines = counted.filter((l) => l.variance_base !== 0);
  const net = varianceLines.reduce((sum, l) => sum + (l.variance_value_cents ?? 0), 0);
  const abs = varianceLines.reduce((sum, l) => sum + Math.abs(l.variance_value_cents ?? 0), 0);
  // The core values a variance only inside the post and omits a zero figure (omitempty).
  const summary = { line_count: lines.length, counted_lines: counted.length, variance_lines: varianceLines.length };
  if (net !== 0) summary.net_variance_value_cents = net;
  if (abs !== 0) summary.abs_variance_value_cents = abs;
  return { sheet: sheetView(s), lines, summary };
}

// ---- http --------------------------------------------------------------------------------------

function json(res, data, status = 200) {
  res.writeHead(status, { "Content-Type": "application/json" });
  res.end(JSON.stringify(data));
}

// The core's envelope: {"error": "<sentence>"} with the module prefix stripped.
function refuse(res, status, error) {
  json(res, { error }, status);
}

function readBody(req) {
  return new Promise((resolve) => {
    let body = "";
    req.on("data", (c) => (body += c));
    req.on("end", () => {
      try {
        resolve(body === "" ? {} : JSON.parse(body));
      } catch {
        resolve(null);
      }
    });
  });
}

// What this stub's tests can switch: a role the gate walls out of ordering, and the caller's
// reach taken away (a scoped buyer with no grants: the core's reach set reads nothing and every
// named branch is out of reach, the fence's own 403 in words).
let walled = false;
let fenced = false;
// Who the caller is for the purchasing areas' role walls: the demo's Procurement manager unless a
// test switches it (payables-receivables enters and posts bills; super-admin sets the tolerances).
let role = "procurement-manager";
// Switches a test can set: cost hidden from the caller (the purchasing policy), the mail transport
// failing, the untagged send refused by policy, and the answer to the next write lost on the way back.
const flags = { costsHidden: false, sendFails: false, refuseUntagged: false, dropNext: false };
// The reach covers the main buyer branch and the two Gable yards the inventory and counts areas read.
const reach = () => (fenced ? [] : [BRANCH, PLM, JUL]);
// The caller's seat on this surface, per the role matrix: the procurement manager holds manage,
// the yard manager work, payables and receivables read. roleMode names the seat the writes test.
let roleMode = "procurement";
// The tech admin the transit-days write needs; no demo role holds that tier.
let adminMode = false;
// The route levels the contract's x-access carries for this surface, and what each seat passes.
// Read routes pass for everyone; the role wall answers the shared Forbidden body, the core's
// {"error":"Forbidden"}, before any handler runs.
const WORK_MOVES = new Set([
  "receive",
  "nudge",
  "sheet-create",
  "sheet-freeze",
  "sheet-counts",
  "sheet-cancel",
]);
const MANAGE_MOVES = new Set([
  "reserve",
  "release",
  "expire",
  "policy-put",
  "transfer-create",
  "transfer-ship",
  "transfer-receive",
  "transfer-cancel",
  "point-put",
  "bootstrap",
  "sheet-approve",
  "sheet-post",
]);
const SEAT_LEVEL = { procurement: 2, yard: 1, payables: 0, sales: 0 };
// The seat answers the token the caller carries (the sub names the demo
// account, as the counts gate reads it); a test token with no sub sits at
// the switch's seat.
function seatFor(req) {
  const sub = callerOf(req);
  if (sub.endsWith("000000000003")) return "yard";
  if (sub.endsWith("0000000000a1")) return "payables";
  if (sub.endsWith("0000000000a2")) return "sales";
  return roleMode;
}
const seatPasses = (move, seat) => {
  const level = MANAGE_MOVES.has(move) ? 2 : WORK_MOVES.has(move) ? 1 : 0;
  return SEAT_LEVEL[seat] >= level;
};

// The purchasing areas (landed cost and the rest): parts of their own, fed the world through the kit.
const purch = createPurchRoutes({
  world: () => world,
  uuid,
  json: (res, data, status) => json(res, data, status),
  refuse: (res, status, error) => refuse(res, status, error),
  readBody: (req) => readBody(req),
  isUuid,
  NOW_ISO,
  dayOffset,
  BRANCH,
  VENDORS,
  PRODUCTS,
  reach,
  flags: () => flags,
  // A scoped caller must name a branch; only a global one may read across all of them.
  global: () => false,
  poView: (po) => poView(po),
  poDetail: (po) => poDetail(po),
  role: () => role,
});
purch.reset();

// The core's fenceNamedBranch: a branch the caller named outside its reach is refused in words;
// an unnamed one is left to the caller (the reach set applies itself).
function outOfReach(branchId) {
  const named = String(branchId ?? "").trim();
  return named !== "" && !reach().includes(named);
}

async function handle(req, res) {
  const url = new URL(req.url, `http://127.0.0.1:${port}`);
  const path = url.pathname;
  const method = req.method;

  if (path === "/healthz") return json(res, { ok: true });
  // The public tenant config route (no credential): the dealer the band and the tab name.
  if (req.method === "GET" && path === "/api/v1/tenant/config") {
    return json(res, { id: "demo-dealer", dealer_name: "Gable Landscape Supply", logo_url: "", primary_color: "#3a7d56", support_email: "support@gablelandscape.invalid", support_phone: "" });
  }
  if (method === "POST" && path === "/_stub/reset") {
    const body = await readBody(req);
    clockOffsetMs = typeof body?.now === "number" && Number.isFinite(body.now) ? body.now - Date.now() : 0;
    businessZone = typeof body?.business_zone === "string" && body.business_zone !== "" ? body.business_zone : DEFAULT_BUSINESS_ZONE;
    reset();
    configReads = 0;
    configDelayMs = 0;
    walled = false;
    fenced = false;
    roleMode = "procurement";
    adminMode = false;
    role = "procurement-manager";
    Object.assign(flags, { costsHidden: false, sendFails: false, refuseUntagged: false, dropNext: false });
    purch.reset();
    return json(res, { ok: true, business_zone: businessZone });
  }
  if (method === "POST" && path === "/_stub/clock") {
    const body = await readBody(req);
    clockOffsetMs = typeof body?.now === "number" && Number.isFinite(body.now) ? body.now - Date.now() : 0;
    if (typeof body?.business_zone === "string" && body.business_zone !== "") businessZone = body.business_zone;
    return json(res, { ok: true, business_zone: businessZone });
  }
  if (method === "POST" && path === "/_stub/config") {
    const body = await readBody(req);
    if (typeof body?.delay_ms === "number" && Number.isFinite(body.delay_ms)) configDelayMs = Math.max(0, body.delay_ms);
    return json(res, { ok: true, reads: configReads, delay_ms: configDelayMs });
  }
  if (method === "POST" && path === "/_stub/flag") {
    const body = await readBody(req);
    if (body?.name in flags) flags[body.name] = Boolean(body.value);
    return json(res, { ok: true, flags });
  }
  if (method === "POST" && path === "/_stub/role") {
    const body = await readBody(req);
    // The inventory and counts areas' seat: procurement (manage), yard (work) or payables (read).
    if (body?.as !== undefined) roleMode = ["procurement", "yard", "payables"].includes(body.as) ? body.as : "procurement";
    role = ["procurement-manager", "payables-receivables", "super-admin"].includes(body?.role) ? body.role : role;
    return json(res, { ok: true, role, as: roleMode });
  }
  if (method === "POST" && path === "/_stub/wall") {
    walled = true;
    return json(res, { ok: true });
  }
  if (method === "POST" && path === "/_stub/fence") {
    fenced = true;
    return json(res, { ok: true });
  }
  if (method === "POST" && path === "/_stub/admin") {
    adminMode = true;
    return json(res, { ok: true });
  }
  if (method === "POST" && path === "/_stub/receive") {
    // The yard team receives the order in Yard Pick and Receive; the stub plays that other app so a
    // leg can watch a placed order move to PARTIAL and RECEIVED.
    const body = await readBody(req);
    const po = world.pos.find((p) => p.no === body?.po_no);
    if (po === undefined) return refuse(res, 404, "not found");
    for (const l of po.lines) l.received = l.qty;
    po.status = "RECEIVED";
    return json(res, { ok: true });
  }

  if (path === "/api/v1/demo/flows") return json(res, { flows: FLOWS });
  if (method === "POST" && path === "/api/v1/demo/session") {
    const body = await readBody(req);
    const account = USERS[body?.flow];
    if (!account) return refuse(res, 404, "not found");
    return json(res, { token: mintToken(account.user_id), user: account, flow: body.flow });
  }
  // One product's own record and its image, the two reads the contract words public (the catalogue
  // and the picture a customer sees): served before the staff gate, as the core serves them. The
  // image is the same invented slot for every trade product, one byte row, so a leg can see a
  // served picture and the app's fallback stays honest for an id with none.
  const productImage = path.match(/^\/api\/v1\/products\/([^/]+)\/image$/);
  if (method === "GET" && productImage) {
    const gls = GLS_PRODUCTS.find((p) => glsId(p.n) === productImage[1]);
    if (gls === undefined) return refuse(res, 404, "not found");
    res.writeHead(200, { "Content-Type": "image/png" });
    res.end(PRODUCT_IMAGE_BYTES);
    return;
  }
  const productOne = path.match(/^\/api\/v1\/products\/([^/]+)$/);
  if (method === "GET" && productOne) {
    const gls = GLS_PRODUCTS.find((p) => glsId(p.n) === productOne[1]);
    if (gls === undefined) return refuse(res, 404, "not found");
    return json(res, { ...catalogueRow(gls), list_price_cents: gls.list, list_price_uom: gls.uom });
  }

  if (!path.startsWith("/api/v1/")) return refuse(res, 404, "not found");
  if (!(req.headers.authorization ?? "").startsWith("Bearer ")) return refuse(res, 401, "Unauthorized");

  // The answer to the next write is lost on the way back: the write happens, the caller hears nothing.
  if (flags.dropNext && method !== "GET" && path.startsWith("/api/v1/")) {
    flags.dropNext = false;
    res.end = () => req.socket.destroy();
  }
  // The role wall answers before ANY handler, the shared body the gate writes: the purchasing
  // parts answer their own routes, so a wall behind them would never see the call. The moves map
  // to the wall the core puts in front of each: the po_writers list on the order writes, the work
  // level on receiving, the manage level on the rest.
  const sheetPath = path.match(/^\/api\/v1\/counts\/sheets\/([^/]+)(\/(freeze|counts|approve|post|cancel))?$/);
  const isSheetWrite = sheetPath !== null && sheetPath[3] !== undefined && method === "POST";
  const moveOf = (p) => {
    if (p === "/api/v1/inventory/receive") return "receive";
    if (p === "/api/v1/inventory/nudge-procurement") return "nudge";
    if (p === "/api/v1/inventory/reservations") return "reserve";
    if (p === "/api/v1/inventory/reservations/release") return "release";
    if (p === "/api/v1/inventory/reservations/expire") return "expire";
    if (p === "/api/v1/inventory/allocation-policy") return "policy-put";
    if (p === "/api/v1/inventory/transfers") return "transfer-create";
    if (/^\/api\/v1\/inventory\/transfers\/[^/]+\/(ship|receive|cancel)$/.test(p)) return `transfer-${p.split("/").pop()}`;
    if (p === "/api/v1/inventory/reorder-points") return "point-put";
    if (p === "/api/v1/inventory/reorder-points/bootstrap") return "bootstrap";
    if (p === "/api/v1/inventory/shortfall/create-po") return "po-writer";
    // The order writes the core walls to the po_writers list (change 5 slice S4's sales fence):
    // drafting, editing lines, placing, sending, promising and the delivery method.
    if (p === "/api/v1/purchasing/pos") return "po-writer";
    if (/^\/api\/v1\/purchasing\/pos\/[^/]+\/lines$/.test(p)) return "po-writer";
    if (/^\/api\/v1\/purchasing\/pos\/[^/]+\/lines\/[^/]+$/.test(p)) return "po-writer";
    if (/^\/api\/v1\/purchasing\/pos\/[^/]+\/(transition|expected-date|delivery-method|send|apply-warranty-credit)$/.test(p)) return "po-writer";
    // Receiving goods is work, not buying: the yard's move, the same wall the core's work level is.
    if (/^\/api\/v1\/purchasing\/pos\/[^/]+\/receive$/.test(p)) return "receive";
    if (p === "/api/v1/counts/sheets" && !isSheetWrite) return "sheet-create";
    if (isSheetWrite) return `sheet-${sheetPath[3]}`;
    return "";
  };
  const move = moveOf(path);
  const seat = seatFor(req);
  if (move !== "" && method !== "GET") {
    if (move === "po-writer") {
      // The po_writers list is the wall: the Procurement Manager, the General
      // Manager and the super admin; every other role is refused at the gate.
      if (seat !== "procurement") return json(res, { error: "Forbidden" }, 403);
    } else if (!seatPasses(move, seat)) {
      return json(res, { error: "Forbidden" }, 403);
    }
  }

  if (await purch.handle({ req, res, url, path, method, role })) return;

  // ---- reads ----
  if (method === "GET" && path === "/api/v1/surfaces/buying-and-stock/config") {
    configReads += 1;
    if (configDelayMs > 0) await new Promise((r) => setTimeout(r, configDelayMs));
    const profile = seat === "procurement" ? "manager" : seat === "yard" ? "yard" : "counter";
    return json(res, {
      surface_id: "buying-and-stock",
      profile,
      role_items: [],
      user_items: [],
      workspace: {
        split_pct: 50,
        arrangement: "PRIMARY_LEFT",
        primary_collapsed: false,
        secondary_collapsed: false,
        cost_hidden: flags.costsHidden,
        source: "dealer",
      },
      business_zone: businessZone,
    });
  }
  if (method === "GET" && path === "/api/v1/inventory/shortfall") {
    if (outOfReach(url.searchParams.get("branch_id"))) return refuse(res, 403, "you do not have access to that branch");
    // The unnamed read answers the reach set (the caller's branches only), the core's rule.
    const rows = PRODUCTS.filter((p) => p.unmanaged !== true)
      .map(productRow)
      .filter((r) => r.short_base > 0 && reach().includes(r.branch_id));
    rows.sort((a, b) => b.short_base - a.short_base);
    return json(res, { items: rows, on_order_degraded: false, next_cursor: null });
  }
  if (method === "GET" && path === "/api/v1/products") {
    // The add a line picker's list: the active catalogue, the fields the picker reads.
    // The inventory half's products sit in the same catalogue, prefixed GLS-.
    const items = PRODUCTS.map(catalogueRow).concat(GLS_PRODUCTS.map(catalogueRow));
    return json(res, { items, next_cursor: null, limit: 200 });
  }
  if (method === "GET" && path === "/api/v1/branches") {
    return json(res, [
      { id: BRANCH, code: "STB", name: "Loma yard", address: { line1: "1 Yard Road", city: "Point Loma", province: "ON", postal: "K0K 0K0", country: "CA" }, is_default: true, is_system_default: false, is_active: true },
      { id: PLM, code: "PLM", name: "Point Loma yard", address: { line1: "410 Harbor Drive", city: "San Diego", province: "CA", postal: "92101", country: "US" }, is_default: false, is_system_default: false, is_active: true },
      { id: JUL, code: "JUL", name: "Julian yard", address: { line1: "7 Main Street", city: "Julian", province: "CA", postal: "92036", country: "US" }, is_default: false, is_system_default: false, is_active: true },
    ]);
  }
  if (method === "GET" && path === "/api/v1/purchasing/pos") {
    const status = url.searchParams.get("status");
    const rows = world.pos.filter((p) => status === null || p.status === status).map(poView);
    rows.sort((a, b) => b.created_at.localeCompare(a.created_at) || b.po_no.localeCompare(a.po_no, undefined, { numeric: true }));
    return json(res, { items: rows });
  }
  const one = path.match(/^\/api\/v1\/purchasing\/pos\/([^/]+)$/);
  if (method === "GET" && one) {
    const po = world.pos.find((p) => p.id === one[1]);
    return po === undefined ? refuse(res, 404, "not found") : json(res, poDetail(po));
  }
  if (method === "GET" && path === "/api/v1/purchasing/incoming") {
    const productId = url.searchParams.get("product_id");
    if (productId === null || productId === "") return refuse(res, 400, "product_id is required");
    // The trade products' arrivals (change 5 slice S4's coming-in table), the same line shape the
    // po world's answer carries; the two id spaces are disjoint, so a GLS id never falls through.
    const gls = GLS_PRODUCTS.find((p) => glsId(p.n) === productId);
    if (gls !== undefined) {
      const rows = world.glsIncoming
        .filter((g) => g.product === gls.n)
        .map((g) => {
          const vendor = ALL_VENDORS.find((v) => v.n === g.vendor);
          return {
            po_id: glsId(40 + g.product),
            po_no: g.no,
            po_line_id: glsId(60 + g.product),
            status: g.status,
            vendor_id: uuid("a", vendor.n),
            vendor_name: vendor.name,
            branch_id: g.branch,
            expected_at: g.expected,
            qty_ordered_base: g.qty,
            qty_received_base: g.received,
            qty_outstanding_base: g.qty - g.received,
            uom: gls.uom,
            unit_cost_cents: gls.cost,
          };
        });
      return json(res, { items: rows, next_cursor: null });
    }
    const lines = [];
    for (const po of world.pos) {
      if (po.status === "RECEIVED" || po.status === "CLOSED" || po.status === "CANCELLED") continue;
      po.lines.forEach((l) => {
        if (uuid("f", l.product) !== productId || l.qty <= (l.received ?? 0)) return;
        const vendor = ALL_VENDORS.find((v) => v.n === po.vendor);
        const line = {
          po_id: po.id,
          po_no: po.no,
          po_line_id: l.id,
          status: po.status,
          vendor_id: uuid("a", vendor.n),
          vendor_name: vendor.name,
          branch_id: BRANCH,
          expected_at: po.expect ?? "",
          qty_ordered_base: l.qty,
          qty_received_base: l.received ?? 0,
          qty_outstanding_base: l.qty - (l.received ?? 0),
          uom: PRODUCTS.find((p) => p.n === l.product).uom,
          unit_cost_cents: l.cost,
        };
        lines.push(line);
      });
    }
    return json(res, { items: lines, next_cursor: null });
  }

  // ---- the inventory and counts routes -------------------------------------------------------------

  // The on-hand read: the core writes the page under its own key, `on_hand`
  // (inventory/handler.go onHand). A product with no stock rows is a normal
  // answer: 200 with an empty list, never not-found.
  if (method === "GET" && path === "/api/v1/inventory/on-hand") {
    const product = productIDParam(url);
    if (product.error !== undefined) return refuse(res, 400, product.error);
    const rows = GLS_PRODUCTS.flatMap((p) => Object.keys(p.stock)
      .filter((b) => glsId(p.n) === product.value)
      .map((b) => onHandRow(p, b)));
    return json(res, { on_hand: rows });
  }
  // The move ledger: the core's key is `moves` (inventory/handler.go moves).
  if (method === "GET" && path === "/api/v1/inventory/moves") {
    const product = productIDParam(url);
    if (product.error !== undefined) return refuse(res, 400, product.error);
    const branch = branchIDParam(url);
    if (branch.error !== undefined) return refuse(res, 400, branch.error);
    const rows = GLS_MOVES.filter((m) => glsId(m.product) === product.value)
      .filter((m) => branch.value === "" || m.branch === branch.value)
      .map(moveRow);
    return json(res, { moves: rows });
  }
  // The deferred fulfilment lines: the core's key is `pending`.
  if (method === "GET" && path === "/api/v1/inventory/pending-fulfilment") {
    const branch = branchIDParam(url);
    if (branch.error !== undefined) return refuse(res, 400, branch.error);
    const rows = GLS_PENDING.map((r) => ({
      order_id: r.order,
      product_id: glsId(r.product),
      qty_base: r.qty,
      sale_type: r.saleType,
    }));
    return json(res, { pending: rows });
  }
  // The four-bucket stock view: the core's key is `branches`
  // (inventory/stock_truth_handler.go branchStock). available is the server's.
  if (method === "GET" && path === "/api/v1/inventory/branch-stock") {
    const product = productIDParam(url);
    if (product.error !== undefined) return refuse(res, 400, product.error);
    const branch = branchIDParam(url);
    if (branch.error !== undefined) return refuse(res, 400, branch.error);
    const gls = GLS_PRODUCTS.find((p) => glsId(p.n) === product.value);
    if (gls === undefined) return json(res, { branches: [] });
    const ids = branch.value === "" ? Object.keys(gls.stock) : [branch.value];
    return json(res, { branches: ids.filter((b) => gls.stock[b] !== undefined).map((b) => branchStockRow(gls, b)) });
  }
  // Browse: the core writes rows, facets, total and the echoed page
  // (inventory/browse.go BrowseResult).
  if (method === "GET" && path === "/api/v1/inventory/browse") {
    const offset = url.searchParams.get("offset") ?? "";
    if (offset !== "" && (!/^\d+$/.test(offset.trim()))) {
      return refuse(res, 400, "offset must be a row number, zero or more");
    }
    const limitRaw = Number(url.searchParams.get("limit") ?? 0);
    const limit = limitRaw > 0 ? Math.min(limitRaw, 200) : 60;
    const offsetN = offset === "" ? 0 : Number(offset.trim());
    const text = (url.searchParams.get("text") ?? "").trim().toLowerCase();
    const inStockOnly = ["1", "true"].includes(url.searchParams.get("in_stock_only") ?? "");
    const all = GLS_PRODUCTS.map((p) => browseRow(p));
    const rows = all
      .filter((r) => text === "" || `${r.sku} ${r.name}`.toLowerCase().includes(text))
      .filter((r) => !inStockOnly || r.available_base > 0);
    const facet = (field, label, pick) => {
      const counts = new Map();
      for (const r of rows) {
        const value = pick(r);
        if (value === "") continue;
        counts.set(value, (counts.get(value) ?? 0) + 1);
      }
      return { field, label, values: [...counts.entries()].map(([value, count]) => ({ value, count })) };
    };
    return json(res, {
      rows: rows.slice(offsetN, offsetN + limit),
      facets: [
        facet("manufacturer", "Manufacturer", (r) => r.manufacturer),
        facet("color_family", "Family", (r) => r.color_family),
        facet("product_size", "Size", (r) => r.product_size),
        facet("sold_by", "Sold by", (r) => r.sold_by),
      ],
      total: rows.length,
      limit,
      offset: offsetN,
    });
  }
  // Transit days: the core's key is `pairs`; the write is the admin gate.
  if (method === "GET" && path === "/api/v1/inventory/transit-days") {
    const pairs = [];
    for (const a of [PLM, JUL, BRANCH]) {
      for (const b of [PLM, JUL, BRANCH]) {
        if (a === b) continue;
        const row = world.transit.find((p) => p.from === a && p.to === b);
        pairs.push(transitPair(a, b, row));
      }
    }
    return json(res, { pairs });
  }
  if (method === "PUT" && path === "/api/v1/inventory/transit-days") {
    if (!adminMode) return json(res, { error: "Forbidden" }, 403);
    const body = await readBody(req);
    if (body === null) return refuse(res, 400, "invalid_json");
    if (!body.from_branch_id || !body.to_branch_id) return refuse(res, 400, "from_branch_id and to_branch_id are required");
    if (body.from_branch_id === body.to_branch_id) return refuse(res, 400, "a transit time needs two different branches");
    if (!Number.isInteger(body.transit_days) || body.transit_days < 0 || body.transit_days > 60) {
      return refuse(res, 400, "transit days must be a whole number of days from 0 to 60");
    }
    let row = world.transit.find((p) => p.from === body.from_branch_id && p.to === body.to_branch_id);
    if (row === undefined) {
      row = { from: body.from_branch_id, to: body.to_branch_id, days: body.transit_days, at: NOW_ISO() };
      world.transit.push(row);
    } else {
      row.days = body.transit_days;
      row.at = NOW_ISO();
    }
    return json(res, transitPair(row.from, row.to, row));
  }

  // Reservations: the list under the core's key, a hold through the policy.
  if (method === "GET" && path === "/api/v1/inventory/reservations") {
    const branch = url.searchParams.get("branch_id") ?? "";
    const product = url.searchParams.get("product_id") ?? "";
    const rows = world.reservations
      .filter((r) => branch === "" || r.branch === branch)
      .filter((r) => product === "" || glsId(r.product) === product)
      .map(reservationRow);
    return json(res, { reservations: rows });
  }
  if (method === "POST" && path === "/api/v1/inventory/reservations") {
    const body = await readBody(req);
    if (body === null) return refuse(res, 400, "invalid_json");
    if (!body.product_id) return refuse(res, 400, "product_id is required");
    if (!body.branch_id) return refuse(res, 400, "branch_id is required");
    if (!(body.qty_base > 0)) return refuse(res, 400, "qty_base must be > 0");
    if (!["ORDER", "QUOTE", "TRANSFER"].includes(body.res_type)) return refuse(res, 400, `unknown reservation type "${body.res_type}"`);
    const gls = GLS_PRODUCTS.find((p) => glsId(p.n) === body.product_id);
    if (gls === undefined) return refuse(res, 400, "product_id is required");
    const policy = policyFor(body.branch_id);
    const mode = body.res_type === "ORDER" ? policy.order_mode : body.res_type === "QUOTE" ? policy.quote_mode : "HARD";
    if (mode !== "NONE") {
      const at = stockAt(body.branch_id, gls.n);
      const available = at.onHand - at.allocated;
      if (available + 1e-9 < body.qty_base) {
        return refuse(res, 409, `insufficient available stock at branch, ${available.toFixed(4)} available, ${Number(body.qty_base).toFixed(4)} requested`);
      }
    }
    const hold = mode === "NONE" ? "NONE" : mode;
    const written = mode !== "NONE";
    const reservation = {
      id: uuid("r", ++world.sheetCounter + 90),
      product: gls.n, branch: body.branch_id, location: body.location_id || body.branch_id,
      resType: body.res_type, hold, qty: body.qty_base, status: "ACTIVE",
      sourceType: body.source_type, ref: body.source_ref_id, reason: body.reason ?? "",
      expires: dayOffset(Math.max(1, Math.round((body.res_type === "ORDER" ? policy.order_ttl_hours : policy.quote_ttl_hours) / 24))),
      created: NOW_ISO(), closed: "",
    };
    if (written) {
      world.reservations.push(reservation);
      if (hold === "HARD") bumpAlloc(body.branch_id, gls.n, body.qty_base);
    }
    return json(res, { reservation: reservationRow(reservation), applied: written, policy });
  }
  if (method === "POST" && path === "/api/v1/inventory/reservations/release") {
    const body = await readBody(req);
    if (body === null) return refuse(res, 400, "invalid_json");
    const status = body.status === undefined || body.status === "" ? "RELEASED" : body.status;
    if (!["RELEASED", "CONSUMED", "EXPIRED"].includes(status)) return refuse(res, 400, `invalid terminal reservation status "${status}"`);
    let released = 0;
    if (body.reservation_id !== undefined && body.reservation_id !== "") {
      const target = world.reservations.find((r) => r.id === body.reservation_id);
      if (target === undefined) return refuse(res, 404, "not found");
      closeReservation(target, status);
      released = 1;
    } else if (body.source_ref_id !== undefined && body.source_ref_id !== "") {
      for (const r of world.reservations.filter((r) => r.ref === body.source_ref_id && r.status === "ACTIVE")) {
        closeReservation(r, status);
        released += 1;
      }
    } else {
      return refuse(res, 400, "reservation_id or source_ref_id is required");
    }
    return json(res, { released, status });
  }
  if (method === "POST" && path === "/api/v1/inventory/reservations/expire") {
    let expired = 0;
    const now = Date.now();
    for (const r of world.reservations.filter((r) => r.status === "ACTIVE" && new Date(r.expires).getTime() < now)) {
      closeReservation(r, "EXPIRED");
      expired += 1;
    }
    return json(res, { expired });
  }
  if (method === "GET" && path === "/api/v1/inventory/allocation-policy") {
    return json(res, policyFor(url.searchParams.get("branch_id") ?? ""));
  }
  if (method === "PUT" && path === "/api/v1/inventory/allocation-policy") {
    const body = await readBody(req);
    if (body === null) return refuse(res, 400, "invalid_json");
    if (!body.branch_id) return refuse(res, 400, "branch_id is required");
    for (const mode of [body.order_mode, body.quote_mode]) {
      if (!["HARD", "SOFT", "NONE"].includes(mode)) return refuse(res, 400, `invalid allocation mode "${mode}" (HARD|SOFT|NONE)`);
    }
    if (!(body.order_ttl_hours >= 0) || !(body.quote_ttl_hours >= 0)) return refuse(res, 400, "ttl hours must be >= 0");
    const stored = {
      branch_id: body.branch_id, order_mode: body.order_mode, quote_mode: body.quote_mode,
      order_ttl_hours: body.order_ttl_hours, quote_ttl_hours: body.quote_ttl_hours, is_default: false,
    };
    world.policies.set(body.branch_id, stored);
    return json(res, stored);
  }

  // Transfers: raise, ship, receive, cancel.
  if (method === "GET" && path === "/api/v1/inventory/transfers") {
    const branch = url.searchParams.get("branch_id") ?? "";
    const status = url.searchParams.get("status") ?? "";
    const rows = world.transfers
      .filter((t) => branch === "" || t.from === branch || t.to === branch)
      .filter((t) => status === "" || t.status === status)
      .map(transferView);
    rows.sort((a, b) => b.created_at.localeCompare(a.created_at));
    return json(res, { transfers: rows });
  }
  if (method === "POST" && path === "/api/v1/inventory/transfers") {
    const body = await readBody(req);
    if (body === null) return refuse(res, 400, "invalid_json");
    if (!body.from_branch_id || !body.to_branch_id) return refuse(res, 400, "from_branch_id and to_branch_id are required");
    if (body.from_branch_id === body.to_branch_id) return refuse(res, 400, "a transfer must cross branches");
    if (!Array.isArray(body.lines) || body.lines.length === 0) return refuse(res, 400, "a transfer needs at least one line");
    const lines = [];
    for (let i = 0; i < body.lines.length; i += 1) {
      const l = body.lines[i];
      if (!l.product_id) return refuse(res, 400, `line ${i + 1} has no product_id`);
      if (!(l.qty_base > 0)) return refuse(res, 400, `line ${i + 1} qty_base must be > 0`);
      const gls = GLS_PRODUCTS.find((p) => glsId(p.n) === l.product_id);
      if (gls === undefined) return refuse(res, 400, `line ${i + 1} has no product_id`);
      lines.push({ id: uuid("u", ++world.sheetCounter + 400), product: gls.n, qty: l.qty_base, shipped: 0, received: 0, cost: gls.cost });
    }
    const t = {
      id: uuid("t", ++world.trfCounter + 40),
      no: `TRF-${branchCode(body.from_branch_id)}-${String(world.trfCounter).padStart(4, "0")}`,
      from: body.from_branch_id, to: body.to_branch_id, status: "DRAFT",
      shipped: "", received: "", eta: "", created: NOW_ISO(), notes: body.notes ?? "", lines,
    };
    world.transfers.push(t);
    return json(res, transferView(t));
  }
  if (method === "GET" && sheetPath === null && /^\/api\/v1\/inventory\/transfers\/[^/]+$/.test(path)) {
    const t = world.transfers.find((x) => x.id === path.split("/").pop());
    return t === undefined ? refuse(res, 404, "not found") : json(res, transferView(t));
  }
  const transferAction = path.match(/^\/api\/v1\/inventory\/transfers\/([^/]+)\/(ship|receive|cancel)$/);
  if (method === "POST" && transferAction) {
    const t = world.transfers.find((x) => x.id === transferAction[1]);
    if (t === undefined) return refuse(res, 404, "not found");
    if (transferAction[2] === "ship") {
      if (t.status !== "DRAFT") return refuse(res, 409, `transfer ${t.no} is ${t.status}, only a DRAFT can ship`);
      const body = await readBody(req);
      if (body === null) return refuse(res, 400, "invalid_json");
      if (body.eta !== undefined && body.eta !== null && body.eta !== "" && !/^\d{4}-\d{2}-\d{2}$/.test(body.eta)) {
        return refuse(res, 400, "expected arrival must be a date like 2026-08-20, leave it blank if there is no promised date");
      }
      // The overdraft guard sits at ship: available at the origin must cover the load.
      for (const [index, l] of t.lines.entries()) {
        const at = stockAt(t.from, l.product);
        const available = at.onHand - at.allocated;
        if (available + 1e-6 < l.qty) {
          return refuse(res, 409, `transfer ${t.no} line ${index + 1} would overdraw the source branch, ${available.toFixed(4)} available (${at.onHand.toFixed(4)} on hand, ${at.allocated.toFixed(4)} reserved), ${l.qty.toFixed(4)} requested`);
        }
      }
      t.status = "IN_TRANSIT";
      t.shipped = NOW_ISO();
      if (body.eta !== undefined && body.eta !== null) t.eta = body.eta === "" ? "" : body.eta;
      else {
        const route = world.transit.find((p) => p.from === t.from && p.to === t.to);
        if (route !== undefined) {
          t.eta = houseDayOffset(route.days);
        } else {
          t.eta = "";
        }
      }
      for (const l of t.lines) {
        l.shipped = l.qty;
        bumpStock(t.from, l.product, -l.qty);
      }
      return json(res, transferView(t));
    }
    if (transferAction[2] === "receive") {
      if (t.status !== "IN_TRANSIT") return refuse(res, 409, `transfer ${t.no} is ${t.status}, only an IN_TRANSIT transfer can be received`);
      t.status = "RECEIVED";
      t.received = NOW_ISO();
      for (const l of t.lines) {
        l.received = l.shipped;
        bumpStock(t.to, l.product, l.received);
      }
      return json(res, transferView(t));
    }
    if (t.status === "DRAFT") {
      t.status = "CANCELLED";
      return json(res, transferView(t));
    }
    return refuse(res, 409, `transfer ${t.no} is ${t.status}, a shipped transfer is corrected with a transfer back, not a cancellation`);
  }
  if (method === "GET" && path === "/api/v1/inventory/incoming-transfers") {
    const product = url.searchParams.get("product_id") ?? "";
    if (product === "") return refuse(res, 400, "product_id is required");
    const lines = world.transfers
      .filter((t) => t.status === "IN_TRANSIT" || t.status === "DRAFT")
      .flatMap((t) => t.lines.filter((l) => glsId(l.product) === product && l.qty > (l.received ?? 0) && l.shipped > 0)
        .map((l) => ({
          transfer_id: t.id,
          transfer_no: t.no,
          from_branch_id: t.from,
          from_branch_code: branchCode(t.from),
          from_branch_name: branchName(t.from),
          to_branch_id: t.to,
          to_branch_code: branchCode(t.to),
          qty_base: l.shipped - l.received,
          uom: glsProduct(l.product).uom,
          shipped_at: t.shipped,
          eta: t.eta,
        })));
    return json(res, { lines });
  }

  // Receiving: the whole outstanding, or explicit per-line quantities.
  if (method === "POST" && path === "/api/v1/inventory/receive") {
    const body = await readBody(req);
    if (body === null) return refuse(res, 400, "invalid_json");
    if (!body.po_id) return refuse(res, 400, "po_id is required");
    const po = world.pos.find((p) => p.id === body.po_id);
    if (po === undefined) return refuse(res, 404, "not found");
    if (Array.isArray(body.lines)) {
      for (const l of body.lines) {
        if (l.qty_base < 0) return refuse(res, 400, `negative receive qty on line ${l.po_line_id}`);
      }
    }
    const requested = new Map((body.lines ?? []).map((l) => [l.po_line_id, l.qty_base]));
    let linesReceived = 0;
    let value = 0;
    let allFull = true;
    for (const l of po.lines) {
      const remaining = l.qty - (l.received ?? 0);
      let qty = remaining;
      if (requested.size > 0) {
        const want = requested.get(l.id);
        if (want === undefined || want === 0) {
          if (remaining > 1e-9) allFull = false;
          continue;
        }
        if (want > remaining + 1e-9) {
          return refuse(res, 409, `over-receipt on PO line ${l.id.slice(0, 8)}, requested ${want.toFixed(4)} but only ${remaining.toFixed(4)} outstanding`);
        }
        qty = want;
      }
      if (qty <= 1e-9) continue;
      l.received = (l.received ?? 0) + qty;
      linesReceived += 1;
      value += qty * l.cost;
    }
    if (linesReceived > 0) {
      po.status = allFull || po.lines.every((l) => l.qty - (l.received ?? 0) <= 1e-9) ? "RECEIVED" : "PARTIAL";
    }
    const vendor = ALL_VENDORS.find((v) => v.n === po.vendor);
    void vendor;
    return json(res, {
      receipt_id: uuid("p", ++world.receiptCounter),
      po_id: po.id,
      po_no: po.no,
      branch_id: BRANCH,
      status: po.status,
      currency: "CAD",
      fx_rate: 1,
      lines_received: linesReceived,
      value_cad_cents: value,
    });
  }

  // Replenishment: the managed lines, their edits, the bootstrap, the nudge.
  if (method === "GET" && path === "/api/v1/inventory/reorder-points") {
    const branch = url.searchParams.get("branch_id") ?? "";
    const rows = REORDER_POINTS.filter((r) => branch === "" || r.branch === branch).map(reorderPointRow);
    return json(res, { reorder_points: rows });
  }
  if (method === "PUT" && path === "/api/v1/inventory/reorder-points") {
    const body = await readBody(req);
    if (body === null) return refuse(res, 400, "invalid_json");
    if (!body.product_id || !body.branch_id) return refuse(res, 400, "product_id and branch_id are required");
    if (body.reorder_point_base < 0 || body.reorder_qty_base < 0) return refuse(res, 400, "reorder quantities must be >= 0");
    if (body.order_multiple_base < 0 || (body.order_multiple_base !== 0 && !(body.order_multiple_base > 0))) {
      return refuse(res, 400, "order_multiple_base must be > 0 (or 0 for none)");
    }
    if (body.lead_time_days < 0) return refuse(res, 400, "lead_time_days must be >= 0");
    const gls = GLS_PRODUCTS.find((p) => glsId(p.n) === body.product_id);
    const product = gls ?? PRODUCTS.find((p) => uuid("f", p.n) === body.product_id);
    if (product === undefined) return refuse(res, 400, "product_id and branch_id are required");
    let row = gls !== undefined
      ? REORDER_POINTS.find((r) => r.product === gls.n && r.branch === body.branch_id)
      : undefined;
    if (row === undefined) {
      row = { product: gls.n, branch: body.branch_id, point: 0, qty: 0, multiple: 0, vendor: gls.vendor, lead: 0, notes: "" };
      REORDER_POINTS.push(row);
    }
    row.point = body.reorder_point_base;
    row.qty = body.reorder_qty_base;
    row.multiple = body.order_multiple_base;
    row.lead = body.lead_time_days;
    row.notes = body.notes ?? row.notes;
    if (body.is_active === false) row.retired = true;
    else row.retired = false;
    return json(res, reorderPointRow(row));
  }
  if (method === "POST" && path === "/api/v1/inventory/reorder-points/bootstrap") {
    const body = await readBody(req);
    if (body === null) return refuse(res, 400, "invalid_json");
    if (!body.branch_id) return refuse(res, 400, "branch_id is required");
    let created = 0;
    for (const p of GLS_PRODUCTS) {
      if (p.stock[body.branch_id] === undefined) continue;
      if (REORDER_POINTS.some((r) => r.product === p.n && r.branch === body.branch_id)) continue;
      REORDER_POINTS.push({ product: p.n, branch: body.branch_id, point: Math.floor(p.stock[body.branch_id].onHand / 2), qty: 0, multiple: 0, vendor: p.vendor, lead: 4, notes: "" });
      created += 1;
    }
    return json(res, { created });
  }
  if (method === "POST" && path === "/api/v1/inventory/nudge-procurement") {
    const body = await readBody(req);
    if (body === null) return refuse(res, 400, "invalid_json");
    if (!body.product_id) return refuse(res, 400, "product_id is required");
    const key = `${body.product_id}@${body.branch_id}`;
    const posted = !world.nudges.has(key);
    world.nudges.add(key);
    const gls = GLS_PRODUCTS.find((p) => glsId(p.n) === body.product_id);
    const at = gls === undefined ? null : stockAt(body.branch_id, gls.n);
    return json(res, {
      posted,
      channel: "house",
      class: "procurement",
      body: gls === undefined ? "A product was named for a nudge the catalogue does not carry." : `Reorder check: ${gls.name} at ${branchName(body.branch_id)} sits at ${at === null ? 0 : at.onHand - at.allocated} ${gls.uom} available against its reorder point.`,
      note: "one nudge per product and branch within the posting cycle",
    });
  }

  if (method === "POST" && path === "/api/v1/inventory/shortfall/create-po") {
    const body = await readBody(req);
    if (body === null) return refuse(res, 400, "invalid_json");
    if (!body.branch_id) return refuse(res, 400, "branch_id is required");
    if (!Array.isArray(body.product_ids) || body.product_ids.length === 0) return refuse(res, 400, "select at least one shortfall line");
    const rows = (function () {
      return PRODUCTS.filter((p) => p.unmanaged !== true)
        .map(productRow)
        .filter((r) => r.short_base > 0 && body.product_ids.includes(r.product_id));
    })();
    if (rows.length === 0) return refuse(res, 400, "none of the selected products is currently short");
    let vendorId = "";
    const lines = [];
    for (const r of rows) {
      if (r.preferred_vendor_id === "") {
        return refuse(res, 400, `${r.sku} has no preferred vendor, set one before creating a purchase order`);
      }
      if (vendorId === "") vendorId = r.preferred_vendor_id;
      else if (vendorId !== r.preferred_vendor_id) {
        return refuse(res, 400, "selection spans more than one vendor, create one purchase order per vendor");
      }
      lines.push({ product_id: r.product_id, qty_base: r.suggested_qty_base, unit_cost_cents: r.avg_cost_cents ?? 0 });
    }
    const vendor = ALL_VENDORS.find((v) => uuid("a", v.n) === vendorId);
    const n = world.counter++;
    const po = {
      id: uuid("c", n), no: `PO-PLM-${n}`, vendor: vendor.n, status: "DRAFT", expect: null,
      method: "UNKNOWN", created: NOW_ISO(),
      lines: lines.map((l) => ({ id: uuid("d", ++world.lineCounter), product: PRODUCTS.find((p) => uuid("f", p.n) === l.product_id).n, qty: l.qty_base, cost: l.unit_cost_cents })),
    };
    world.pos.push(po);
    return json(res, {
      po_id: po.id,
      po_no: po.no,
      draft: { branch_id: body.branch_id, vendor_id: vendorId, memo: "drafted from the shortfall worklist", lines },
    });
  }

  // ---- the counts routes ---------------------------------------------------------------------------

  if (method === "GET" && path === "/api/v1/counts/sheets") {
    const branch = url.searchParams.get("branch_id") ?? "";
    const status = url.searchParams.get("status") ?? "";
    const rows = world.sheets
      .filter((s) => branch === "" || s.branch === branch)
      .filter((s) => status === "" || s.status === status)
      .map(sheetView);
    return json(res, { items: rows });
  }
  if (method === "POST" && path === "/api/v1/counts/sheets") {
    const body = await readBody(req);
    if (body === null) return refuse(res, 400, "invalid_json");
    if (!body.branch_id) return refuse(res, 400, "branch_id is required");
    if (body.branch_id !== BRANCH && !reach().includes(body.branch_id)) return refuse(res, 403, "caller cannot access this branch");
    if (!seatPasses("sheet-create", seatFor(req))) return json(res, { error: "Forbidden" }, 403);
    const n = ++world.sheetCounter;
    const sheet = {
      id: uuid("s", n + 40),
      no: `CS-${branchCode(body.branch_id)}-${String(n).padStart(4, "0")}`,
      branch: body.branch_id,
      scope: body.scope === undefined || body.scope === "" ? "FULL" : body.scope,
      status: "OPEN", freezeAt: "", countedBy: "", approvedBy: "", approvedAt: "", postedAt: "",
      notes: body.notes ?? "", created: NOW_ISO(), lines: [],
    };
    world.sheets.push(sheet);
    return json(res, sheetView(sheet), 201);
  }
  if (method === "GET" && sheetPath && sheetPath[3] === undefined) {
    const sheet = world.sheets.find((s) => s.id === sheetPath[1]);
    if (sheet === undefined) return refuse(res, 404, "not found");
    return json(res, sheetDetail(sheet));
  }
  if (method === "POST" && sheetPath && sheetPath[3] === "freeze") {
    const sheet = world.sheets.find((s) => s.id === sheetPath[1]);
    if (sheet === undefined) return refuse(res, 404, "not found");
    if (sheet.status !== "OPEN") return refuse(res, 409, `only an OPEN sheet can be frozen (sheet is ${sheet.status})`);
    sheet.status = "FROZEN";
    sheet.freezeAt = NOW_ISO();
    sheet.lines = GLS_PRODUCTS
      .filter((p) => p.stock[sheet.branch] !== undefined)
      .map((p) => ({
        id: uuid("v", ++world.sheetCounter + 800),
        product: p.n, location: sheet.branch,
        snapshot: p.stock[sheet.branch].onHand, counted: null, variance: 0,
        cost: p.cost, value: null, reason: "", move: "",
      }));
    return json(res, sheetView(sheet));
  }
  if (method === "POST" && sheetPath && sheetPath[3] === "counts") {
    const sheet = world.sheets.find((s) => s.id === sheetPath[1]);
    if (sheet === undefined) return refuse(res, 404, "not found");
    if (sheet.status !== "FROZEN" && sheet.status !== "COUNTED") return refuse(res, 409, "sheet must be FROZEN before counts are entered");
    const body = await readBody(req);
    if (body === null) return refuse(res, 400, "invalid_json");
    if (!Array.isArray(body.lines)) return refuse(res, 400, "counts must be an array of lines");
    for (const ln of body.lines) {
      if (typeof ln !== "object") return refuse(res, 400, "each counted line is an object");
      if (!ln.product_id) return refuse(res, 400, "each counted line names a product");
      if (typeof ln.counted_qty_base !== "number") return refuse(res, 400, "each counted line carries counted_qty_base");
    }
    for (const ln of body.lines) {
      let line = sheet.lines.find((l) => l.product === GLS_PRODUCTS.find((p) => glsId(p.n) === ln.product_id)?.n);
      if (line === undefined) {
        line = { id: uuid("v", ++world.sheetCounter + 800), product: null, location: ln.location_id ?? sheet.branch, snapshot: 0, counted: null, variance: 0, cost: 0, value: null, reason: "", move: "" };
        sheet.lines.push(line);
      }
      line.counted = ln.counted_qty_base;
      line.variance = line.counted - line.snapshot;
      line.reason = ln.reason_code ?? "";
    }
    // Re-saving counts invalidates any prior approval: the checker signs the numbers that will post.
    sheet.status = "COUNTED";
    sheet.countedBy = callerOf(req);
    sheet.approvedBy = "";
    sheet.approvedAt = "";
    return json(res, sheetView(sheet));
  }
  if (method === "POST" && sheetPath && sheetPath[3] === "approve") {
    const sheet = world.sheets.find((s) => s.id === sheetPath[1]);
    if (sheet === undefined) return refuse(res, 404, "not found");
    if (sheet.status !== "COUNTED") return refuse(res, 409, "sheet is not in COUNTED state");
    const checker = callerOf(req);
    if (sheet.countedBy !== "" && sheet.countedBy === checker) {
      return refuse(res, 422, "maker-checker violation, the maker cannot approve their own count");
    }
    sheet.approvedBy = checker;
    sheet.approvedAt = NOW_ISO();
    return json(res, sheetView(sheet));
  }
  if (method === "POST" && sheetPath && sheetPath[3] === "post") {
    const sheet = world.sheets.find((s) => s.id === sheetPath[1]);
    if (sheet === undefined) return refuse(res, 404, "not found");
    // The core's validateMakerChecker: a POSTED sheet replays as a no-op (the
    // idempotent re-post), a COUNTED sheet needs a checker distinct from the
    // maker, anything else is not in COUNTED state.
    if (sheet.status !== "COUNTED" && sheet.status !== "POSTED") return refuse(res, 409, "sheet is not in COUNTED state");
    if (sheet.status === "COUNTED") {
      if (sheet.approvedBy === "") return refuse(res, 422, "post requires a checker approval (approved_by) distinct from the maker");
      if (sheet.approvedBy === sheet.countedBy) {
        return refuse(res, 422, `maker-checker violation, the checker (${sheet.approvedBy}) must differ from the maker`);
      }
      for (const line of sheet.lines) {
        if (line.variance === 0) continue;
        line.value = line.variance * line.cost;
        line.move = uuid("m", ++world.sheetCounter + 900);
        bumpStock(sheet.branch, line.product, line.variance);
      }
      sheet.status = "POSTED";
      sheet.postedAt = NOW_ISO();
    }
    return json(res, sheetDetail(sheet));
  }
  if (method === "POST" && sheetPath && sheetPath[3] === "cancel") {
    const sheet = world.sheets.find((s) => s.id === sheetPath[1]);
    if (sheet === undefined) return refuse(res, 404, "not found");
    if (sheet.status === "POSTED") return refuse(res, 409, "a POSTED sheet cannot be cancelled");
    sheet.status = "CANCELLED";
    return json(res, { status: "CANCELLED" });
  }
  if (method === "GET" && path === "/api/v1/counts/reason-codes") {
    return json(res, {
      items: REASON_CODES.map((c) => ({ code: c.code, description: c.description, account_code: c.account, is_active: true })),
    });
  }
  if (method === "GET" && path === "/api/v1/counts/negative-stock") {
    const branch = url.searchParams.get("branch_id") ?? "";
    const ids = branch === "" ? [PLM, JUL] : [branch];
    const rows = [];
    for (const b of ids) {
      for (const p of GLS_PRODUCTS) {
        const at = p.stock[b];
        if (at === undefined || at.onHand >= 0) continue;
        rows.push({ product_id: glsId(p.n), location_id: b, on_hand_base: at.onHand, avg_cost_cents: p.cost });
      }
    }
    return json(res, { items: rows });
  }
  if (method === "GET" && path === "/api/v1/counts/recon") {
    const branch = url.searchParams.get("branch_id") ?? "";
    const now = new Date();
    const year = Number(url.searchParams.get("year") ?? now.getUTCFullYear());
    let month = Number(url.searchParams.get("month") ?? now.getUTCMonth() + 1);
    if (!(month >= 1 && month <= 12)) month = now.getUTCMonth() + 1;
    const key = `${year}-${month}@${branch}`;
    let run = world.recons.get(key);
    if (run === undefined) {
      const ids = branch === "" ? [PLM, JUL] : [branch];
      let subledger = 0;
      for (const b of ids) {
        for (const p of GLS_PRODUCTS) {
          const at = p.stock[b];
          if (at === undefined) continue;
          subledger += Math.round(at.onHand * p.cost);
        }
      }
      // The GL side ties against the 1200 inventory account; the demo books match Point Loma.
      const gl = branch === JUL ? subledger - 12_500 : subledger;
      run = {
        id: uuid("n", ++world.sheetCounter + 600),
        branch_id: branch === "" ? null : branch,
        fiscal_year: year,
        month,
        subledger_cents: subledger,
        gl_1200_cents: gl,
        delta_cents: subledger - gl,
        status: subledger - gl === 0 ? "TIED" : "OUT_OF_TIE",
        run_by: callerOf(req),
        created_at: NOW_ISO(),
      };
      world.recons.set(key, run);
    }
    const history = [...world.recons.entries()]
      .filter(([k]) => k.endsWith(`@${branch}`))
      .map(([, r]) => r);
    if (!history.some((r) => r.id === run.id)) history.push(run);
    // One older run so the history table has a past to show; the month before this one.
    const previous = { ...run, id: uuid("n", 1), fiscal_year: run.month === 1 ? run.fiscal_year - 1 : run.fiscal_year, month: run.month === 1 ? 12 : run.month - 1, delta_cents: -12_500, status: "OUT_OF_TIE" };
    return json(res, { current: run, history: { items: [previous, ...history] } });
  }

  // ---- the walled moves: the gate answers before any handler ----
  const isOrdering =
    (method === "POST" && (path === "/api/v1/inventory/reorder-po" || path === "/api/v1/purchasing/pos")) ||
    (method !== "GET" && /^\/api\/v1\/purchasing\/pos\//.test(path));
  if (isOrdering && walled) return json(res, { error: "Forbidden" }, 403);

  // ---- writes ----
  if (method === "POST" && path === "/api/v1/inventory/reorder-po") {
    const body = await readBody(req);
    if (body === null) return refuse(res, 400, "invalid_json");
    if (!body.product_id) return refuse(res, 400, "inventory: product_id is required".replace(/^[a-z]+: /, ""));
    // The core fences the named branch first (resolveBranch), so the answer for a branch outside
    // the reach never depends on that branch's stock.
    if (outOfReach(body.branch_id)) return refuse(res, 403, "you do not have access to that branch");
    if (String(body.branch_id ?? "").trim() === "" && reach().length !== 1) {
      return refuse(res, 400, "branch_id is required, your access spans more than one branch");
    }
    const p = PRODUCTS.find((x) => uuid("f", x.n) === body.product_id);
    const row = p === undefined ? undefined : productRow(p);
    if (row === undefined) return refuse(res, 400, "this product has no replenishment line at this branch, set its reorder point and preferred vendor first");
    const qty = body.qty_base > 0 ? body.qty_base : row.suggested_qty_base;
    if (qty <= 0) {
      return refuse(res, 400, `nothing to order for ${row.sku}, its replenishment quantity is zero and its position is above the reorder point`);
    }
    // The draft already open for this product at this branch, read BEFORE the new draft is made
    // (the core's rule): the answer names it, so a surface can say a draft was already open.
    const existing = world.pos.find((o) => o.status === "DRAFT" && o.lines.some((l) => l.product === p.n));
    const n = world.counter++;
    const po = {
      id: uuid("c", n),
      no: `PO-PLM-${n}`,
      vendor: p.vendor,
      status: "DRAFT",
      expect: null,
      method: "UNKNOWN",
      created: NOW_ISO(),
      lines: [{ id: uuid("d", ++world.lineCounter), product: p.n, qty, cost: p.cost }],
    };
    world.pos.push(po);
    return json(res, {
      po_id: po.id,
      po_no: po.no,
      draft: { branch_id: BRANCH, vendor_id: row.preferred_vendor_id, memo: "quick reorder from the product view (DIB-461)", lines: [{ product_id: row.product_id, qty_base: qty, unit_cost_cents: p.cost }] },
      suggested_qty_base: row.suggested_qty_base,
      uom: p.uom,
      vendor_name: row.preferred_vendor_name,
      ...(existing === undefined ? {} : { existing_draft_po_id: existing.id, existing_draft_po_no: existing.no }),
    });
  }

  const sub = path.match(/^\/api\/v1\/purchasing\/pos\/([^/]+)\/(transition|expected-date|delivery-method)$/);
  if (sub) {
    const po = world.pos.find((p) => p.id === sub[1]);
    if (po === undefined) return refuse(res, 404, "not found");
    const body = await readBody(req);
    if (body === null) return refuse(res, 400, "invalid_json");
    if (sub[2] === "transition" && method === "POST") {
      const to = String(body.to ?? "").toUpperCase();
      if (to === po.status) return refuse(res, 409, `purchase order is already ${po.status}`);
      if (to === "PARTIAL" || to === "RECEIVED") return refuse(res, 409, `${to} is reached by receiving goods, not by a status change`);
      if (to === "ORDERED" && po.status === "DRAFT") {
        if (po.lines.length === 0) return refuse(res, 409, "cannot order an empty purchase order, add at least one line");
        po.status = "ORDERED";
        return json(res, poDetail(po));
      }
      if (to === "CANCELLED" && po.status === "DRAFT") {
        po.status = "CANCELLED";
        return json(res, poDetail(po));
      }
      return refuse(res, 409, `cannot move a ${po.status} purchase order to ${to}`);
    }
    if (sub[2] === "expected-date" && method === "PUT") {
      if (!/^\d{4}-\d{2}-\d{2}$/.test(String(body.expected_at ?? ""))) return refuse(res, 400, "expected_at must be a date (YYYY-MM-DD)");
      if (po.status !== "DRAFT" && po.status !== "ORDERED") return refuse(res, 409, `cannot change the expected date of a ${po.status} purchase order`);
      po.expect = body.expected_at;
      return json(res, poDetail(po));
    }
    if (sub[2] === "delivery-method" && method === "PUT") {
      if (!["PICKUP", "DELIVERY", "UNKNOWN"].includes(body.delivery_method)) return refuse(res, 400, "delivery_method is required");
      if (po.status !== "DRAFT" && po.status !== "ORDERED") return refuse(res, 409, `cannot change the arrival of a ${po.status} purchase order`);
      po.method = body.delivery_method;
      return json(res, poDetail(po));
    }
  }
  const applyCredit = path.match(/^\/api\/v1\/purchasing\/pos\/([^/]+)\/apply-warranty-credit$/);
  if (applyCredit && method === "POST") {
    const po = world.pos.find((p) => p.id === applyCredit[1]);
    if (po === undefined) return refuse(res, 404, "not found");
    if (po.status !== "DRAFT") return refuse(res, 409, `cannot apply warranty credit to a ${po.status} purchase order`);
    const subtotal = po.lines.reduce((sum, l) => sum + l.qty * l.cost, 0);
    if (subtotal <= 0) return refuse(res, 400, "cannot apply warranty credit to an order with no lines or zero total");

    const body = (await readBody(req)) ?? {};
    const vendorId = uuid("a", po.vendor);
    const credit = VENDOR_WARRANTY_CREDITS[vendorId];
    if (!credit || credit.amount_cents <= 0) {
      return refuse(res, 400, `vendor ${vendorId} has no open warranty credit`);
    }

    let creditToApply = credit.amount_cents;
    if (body.amount_cents !== undefined && body.amount_cents > 0) {
      creditToApply = body.amount_cents;
    }
    const applied = Math.min(creditToApply, subtotal);
    po.warranty_credit_applied_cents = applied;
    po.warranty_claim_id = credit.claim_id;
    po.warranty_claim_no = credit.claim_no;
    po.net_total_cents = Math.max(0, subtotal - applied);
    return json(res, poDetail(po));
  }
  const line = path.match(/^\/api\/v1\/purchasing\/pos\/([^/]+)\/lines\/([^/]+)$/);
  if (line && (method === "DELETE" || method === "PUT")) {
    // The core's UpdateLine reads the body before the path (service.go:447 then :452): an empty
    // change body is refused first, whatever the ids look like. RemoveLine has no body to read.
    const body = method === "PUT" ? await readBody(req) : null;
    if (method === "PUT" && body === null) return refuse(res, 400, "invalid_json");
    if (method === "PUT" && body.qty_ordered_base === undefined && body.unit_cost_cents === undefined) {
      return refuse(res, 400, "the body names nothing to change");
    }
    // The core's checkLinePath: an order id that is not a uuid names no order; a line id that is
    // not one is a request that cannot be right, 422 in words.
    if (!isUuid(line[1])) return refuse(res, 404, "not found");
    if (!isUuid(line[2])) return refuse(res, 422, "line id must be a uuid");
    const po = world.pos.find((p) => p.id === line[1]);
    if (po === undefined) return refuse(res, 404, "not found");
    const target = po.lines.find((l) => l.id === line[2]);
    if (method === "DELETE") {
      if (po.status !== "DRAFT") return refuse(res, 409, `cannot edit the lines of a ${po.status} purchase order`);
      po.lines = po.lines.filter((l) => l.id !== line[2]);
      return json(res, poDetail(po));
    }
    // PUT: correct the quantity or the cost (purchasing/service.go UpdateLine, its own words and order).
    if (body.qty_ordered_base !== undefined && body.qty_ordered_base <= 0) return refuse(res, 400, "line quantity must be positive");
    if (body.qty_ordered_base !== undefined && body.qty_ordered_base > MAX_LINE_QTY) {
      return refuse(res, 422, `line quantity is beyond what an order line can hold (${MAX_LINE_QTY} at most)`);
    }
    if (body.unit_cost_cents !== undefined && body.unit_cost_cents < 0) return refuse(res, 400, "line unit cost cannot be negative");
    if (po.status !== "DRAFT") return refuse(res, 409, `cannot edit the lines of a ${po.status} purchase order`);
    if (target === undefined) return refuse(res, 404, "not found");
    if (body.qty_ordered_base !== undefined) target.qty = body.qty_ordered_base;
    if (body.unit_cost_cents !== undefined) target.cost = body.unit_cost_cents;
    return json(res, poDetail(po));
  }
  if (method === "POST" && path.match(/^\/api\/v1\/purchasing\/pos\/([^/]+)\/lines$/)) {
    const add = path.match(/^\/api\/v1\/purchasing\/pos\/([^/]+)\/lines$/);
    const po = world.pos.find((p) => p.id === add[1]);
    if (po === undefined) return refuse(res, 404, "not found");
    const body = await readBody(req);
    if (body === null) return refuse(res, 400, "invalid_json");
    // The core's validateLine words, with the line number the add route passes (0: the number is
    // assigned after the insert).
    if (!body.product_id) return refuse(res, 400, "line 0 needs a product_id");
    if (!(body.qty_ordered_base > 0)) return refuse(res, 400, "line 0 quantity must be greater than zero");
    if (body.qty_ordered_base > MAX_LINE_QTY) {
      return refuse(res, 422, `line 0 quantity is beyond what an order line can hold (${MAX_LINE_QTY} at most)`);
    }
    if (body.unit_cost_cents !== undefined && body.unit_cost_cents < 0) return refuse(res, 400, "line 0 unit cost cannot be negative");
    if (po.status !== "DRAFT") return refuse(res, 409, `cannot edit the lines of a ${po.status} purchase order`);
    const p = PRODUCTS.find((x) => uuid("f", x.n) === body.product_id);
    if (p === undefined) return refuse(res, 400, "product not found");
    const cost = body.unit_cost_cents !== undefined ? body.unit_cost_cents : p.cost;
    po.lines.push({ id: uuid("d", ++world.lineCounter), product: p.n, qty: body.qty_ordered_base, cost });
    return json(res, poDetail(po));
  }
  return refuse(res, 404, "not found");
}

const server = createServer((req, res) => {
  handle(req, res).catch(() => refuse(res, 500, "internal error"));
});
server.listen(port, () => {
  console.log(`buying-stock-stub listening on ${port}`);
});
