// The Buying and Stock app's local stub: the demo way in and the procurement
// routes the app calls, answering with the core's wire shapes and the core's
// words. It is a development and test double only; the real routes are the
// core's. The stub contract test (test/stub-contract.test.mjs) holds every
// answer here to the generated wire descriptors and every refusal sentence to
// the core's source, so the browser legs never lean on an answer the server
// would not give.
//
// Two answers follow the core's handlers and not the generated contract, on
// purpose, because the app has to read what the core really writes (the shape
// census names both): the shortfall worklist under the key "shortfall", and
// the product incoming read under the key "lines".
//
// The world is invented and small: one yard, four vendors, three managed
// lines (one of them the K08 gap: promised to customers, never inbound) and
// four purchase orders. Dates are cut from the host's own day so no calendar
// literal is written.
import { createServer } from "node:http";

const port = Number(process.env.ROLE_STUB_PORT ?? 4969);

const BRANCH = "b0000000-0000-4000-8000-000000000001";
const FLOWS = [{ flow: "procurement", role_code: "procurement_manager", label: "Procurement manager" }];

const USERS = {
  procurement: {
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
  return new Intl.DateTimeFormat("en-CA", { timeZone: "America/Toronto", year: "numeric", month: "2-digit", day: "2-digit" }).format(at);
}

function uuid(kind, n) {
  return `${kind}0000000-0000-4000-8000-${String(n).padStart(12, "0")}`;
}

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
  { n: 66, vendor: 1, status: "RECEIVED", expect: -3, method: "PICKUP", lines: [{ product: 6, qty: 10, cost: 2500, received: 10 }] },
];

let world;

function productRow(p) {
  const onOrder = world.pos
    .filter((po) => po.status === "DRAFT" || po.status === "ORDERED" || po.status === "PARTIAL")
    .flatMap((po) => po.lines)
    .filter((l) => l.product === p.n)
    .reduce((sum, l) => sum + (l.qty - (l.received ?? 0)), 0);
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
    unit_cost_cents: l.cost,
    line_total_cents: l.qty * l.cost,
    billed_cents: 0,
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
    subtotal_cents: po.lines.reduce((sum, l) => sum + l.qty * l.cost, 0),
    subtotal_cad_cents: po.lines.reduce((sum, l) => sum + l.qty * l.cost, 0),
    created_at: po.created,
    updated_at: po.created,
  };
  if (po.expect !== null) view.expected_at = po.expect;
  if (po.status !== "DRAFT") view.ordered_at = po.created;
  return view;
}

function poDetail(po) {
  return { ...poView(po), lines: po.lines.map((l, i) => lineView(l, i + 1)), receipts: [] };
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

// What this stub's tests can switch: a role the gate walls out of ordering.
let walled = false;

async function handle(req, res) {
  const url = new URL(req.url, `http://127.0.0.1:${port}`);
  const path = url.pathname;
  const method = req.method;

  if (path === "/healthz") return json(res, { ok: true });
  if (method === "POST" && path === "/_stub/reset") {
    reset();
    walled = false;
    return json(res, { ok: true });
  }
  if (method === "POST" && path === "/_stub/wall") {
    walled = true;
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

  // ---- reads ----
  if (method === "GET" && path === "/api/v1/inventory/shortfall") {
    const rows = PRODUCTS.filter((p) => p.unmanaged !== true).map(productRow).filter((r) => r.short_base > 0);
    rows.sort((a, b) => b.short_base - a.short_base);
    return json(res, { shortfall: rows, on_order_degraded: false });
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
    return json(res, { lines });
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
    const p = PRODUCTS.find((x) => uuid("f", x.n) === body.product_id);
    const row = p === undefined ? undefined : productRow(p);
    if (row === undefined) return refuse(res, 400, "this product has no replenishment line at this branch, set its reorder point and preferred vendor first");
    const qty = body.qty_base > 0 ? body.qty_base : row.suggested_qty_base;
    if (qty <= 0) {
      return refuse(res, 400, `nothing to order for ${row.sku}, its replenishment quantity is zero and its position is above the reorder point`);
    }
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
  if (method === "DELETE" && line) {
    const po = world.pos.find((p) => p.id === line[1]);
    if (po === undefined) return refuse(res, 404, "not found");
    if (po.status !== "DRAFT") return refuse(res, 409, `cannot edit the lines of a ${po.status} purchase order`);
    po.lines = po.lines.filter((l) => l.id !== line[2]);
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
