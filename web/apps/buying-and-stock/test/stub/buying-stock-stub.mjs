// The Buying and Stock app's local stub: the demo way in and the procurement
// routes the app calls, answering with the core's wire shapes and the core's
// words. It is a development and test double only; the real routes are the
// core's. The stub contract test (test/stub-contract.test.mjs) holds every
// answer here to the generated wire descriptors and every refusal sentence to
// the core's source, so the browser legs never lean on an answer the server
// would not give.
//
// The list pages follow the contract's keys, the ones the core writes since
// FROM-PRINCIPAL 72 settled them: the shortfall worklist and the product
// incoming read both answer under "items".
//
// The world is invented and small: one yard, four vendors, three managed
// lines (one of them the K08 gap: promised to customers, never inbound) and
// four purchase orders. Dates are cut from the host's own day so no calendar
// literal is written.
import { createServer } from "node:http";
import { createPurchRoutes } from "./purch-routes.mjs";

const port = Number(process.env.ROLE_STUB_PORT ?? 4969);

const BRANCH = "b0000000-0000-4000-8000-000000000001";
const FLOWS = [{ flow: "procurement-manager", role_code: "procurement_manager", label: "Procurement manager" }];

const USERS = {
  "procurement-manager": {
    user_id: "de110000-0000-4000-9000-000000000008",
    email: "procurement.manager@hh-demo.invalid",
    display_name: "Procurement manager (demo)",
    role_code: "procurement_manager",
    role_name: "Procurement manager",
    tier: 2,
    scope: "BRANCH",
  },
};

function mintToken(sub) {
  const part = (value) => Buffer.from(JSON.stringify(value), "utf8").toString("base64url");
  const issuedAtSeconds = Math.floor(Date.now() / 1000);
  const payload = { iss: "hh-demo-stub", sub, aud: "staff", iat: issuedAtSeconds, exp: issuedAtSeconds + 3600 };
  return `${part({ alg: "ES256", typ: "JWT" })}.${part(payload)}.c3R1Yi1zaWduYXR1cmUtYnl0ZXM`;
}

function dayOffset(offset) {
  const at = new Date();
  at.setUTCDate(at.getUTCDate() + offset);
  // The app's own "today" is the reader's local day: the same zone here, so a
  // seeded "in 2 days" reads as 2 days at any hour of any machine's clock.
  return new Intl.DateTimeFormat("en-CA", { year: "numeric", month: "2-digit", day: "2-digit" }).format(at);
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

const NOW_ISO = () => new Date().toISOString();

// ---- the world ---------------------------------------------------------------------------------

const VENDORS = [
  { n: 1, no: "V-ALC", name: "Alcli (demo)", terms: 30 },
  { n: 2, no: "V-TEC", name: "Techo Bloc (demo)", terms: 30 },
  { n: 3, no: "V-UNI", name: "Unilock (demo)", terms: 45 },
  { n: 4, no: "V-RYM", name: "Rymar (demo)", terms: 30 },
];

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
  const vendor = VENDORS.find((v) => v.n === p.vendor);
  return {
    product_id: uuid("f", p.n),
    sku: p.sku,
    product_name: p.name,
    branch_id: BRANCH,
    branch_code: "TRT",
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
  };
}

function reset() {
  world = { pos: [], counter: 142, lineCounter: 0, keys: new Map() };
  for (const s of SEEDED_POS) {
    world.pos.push({
      id: uuid("c", s.n),
      no: `PO-TRT-${s.n}`,
      vendor: s.vendor,
      status: s.status,
      expect: s.expect === null ? null : dayOffset(s.expect),
      method: s.method,
      created: NOW_ISO(),
      lines: s.lines.map((l) => ({ id: uuid("d", ++world.lineCounter), ...l })),
    });
  }
}
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
    qty_billed_base: 0,
    qty_outstanding_base: l.qty - received,
    uom: p.uom,
    ...(flags.costsHidden ? {} : { unit_cost_cents: l.cost, line_total_cents: l.qty * l.cost, billed_cents: 0 }),
  };
}

function poView(po) {
  const vendor = VENDORS.find((v) => v.n === po.vendor);
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
          subtotal_cents: po.lines.reduce((sum, l) => sum + l.qty * l.cost, 0),
          subtotal_cad_cents: po.lines.reduce((sum, l) => sum + l.qty * l.cost, 0),
        }),
    created_at: po.created,
    updated_at: po.created,
  };
  if (po.expect !== null) view.expected_at = po.expect;
  if (po.status !== "DRAFT") view.ordered_at = po.created;
  return view;
}

function poDetail(po) {
  return { ...poView(po), lines: po.lines.map((l, i) => lineView(l, i + 1)), receipts: purch.receiptsFor(po.id) };
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
const reach = () => (fenced ? [] : [BRANCH]);

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
  if (method === "POST" && path === "/_stub/reset") {
    reset();
    walled = false;
    fenced = false;
    role = "procurement-manager";
    Object.assign(flags, { costsHidden: false, sendFails: false, refuseUntagged: false, dropNext: false });
    purch.reset();
    return json(res, { ok: true });
  }
  if (method === "POST" && path === "/_stub/flag") {
    const body = await readBody(req);
    if (body?.name in flags) flags[body.name] = Boolean(body.value);
    return json(res, { ok: true, flags });
  }
  if (method === "POST" && path === "/_stub/role") {
    const body = await readBody(req);
    role = ["procurement-manager", "payables-receivables", "super-admin"].includes(body?.role) ? body.role : role;
    return json(res, { ok: true, role });
  }
  if (method === "POST" && path === "/_stub/wall") {
    walled = true;
    return json(res, { ok: true });
  }
  if (method === "POST" && path === "/_stub/fence") {
    fenced = true;
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

  if (!path.startsWith("/api/v1/")) return refuse(res, 404, "not found");
  if (!(req.headers.authorization ?? "").startsWith("Bearer ")) return refuse(res, 401, "Unauthorized");

  // The answer to the next write is lost on the way back: the write happens, the caller hears nothing.
  if (flags.dropNext && method !== "GET" && path.startsWith("/api/v1/")) {
    flags.dropNext = false;
    res.end = () => req.socket.destroy();
  }
  if (await purch.handle({ req, res, url, path, method, role })) return;

  // ---- reads ----
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
    const items = PRODUCTS.map((p) => ({
      id: uuid("f", p.n),
      sku: p.sku,
      name: p.name,
      description: p.name,
      manufacturer: "HH (demo)",
      collection: "",
      color: "",
      finish: "",
      application: "",
      color_family: "",
      texture: "",
      product_size: "",
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
    }));
    return json(res, { items, next_cursor: null, limit: 200 });
  }
  if (method === "GET" && path === "/api/v1/branches") {
    return json(res, [
      { id: BRANCH, code: "TRT", name: "Trenton yard", address: { line1: "1 Yard Road", city: "Trenton", province: "ON", postal: "K0K 0K0", country: "CA" }, is_default: true, is_system_default: false, is_active: true },
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
    const lines = [];
    for (const po of world.pos) {
      if (po.status === "RECEIVED" || po.status === "CLOSED" || po.status === "CANCELLED") continue;
      po.lines.forEach((l) => {
        if (uuid("f", l.product) !== productId || l.qty <= (l.received ?? 0)) return;
        const vendor = VENDORS.find((v) => v.n === po.vendor);
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

  // ---- the walled moves: the gate answers before any handler ----
  const isOrdering =
    (method === "POST" && (path === "/api/v1/inventory/reorder-po" || path === "/api/v1/purchasing/pos")) ||
    (method !== "GET" && /^\/api\/v1\/purchasing\/pos\//.test(path));
  if (isOrdering && walled) return json(res, { error: "Forbidden" }, 403);
  // A vendor bill is posted in the Money Desk: this role's matrix refuses it at the gate.
  if (/^\/api\/v1\/purchasing\/bills/.test(path) && method !== "GET") return json(res, { error: "Forbidden" }, 403);

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
      no: `PO-TRT-${n}`,
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
