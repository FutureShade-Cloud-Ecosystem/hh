// The Buying and Stock stub against the contract and against the core, field by field.
//
// A stub that drifts from the server makes every browser leg a flattering
// lie, so this test starts the stub, makes every call the procurement
// slice makes, and holds each answer to the generated wire
// descriptors: required fields present, types and vocabularies right, and no
// field the contract does not declare (the descriptors are open objects, so
// the undeclared field check is this file's own). Then each refusal is held to
// the core's words: the status, the envelope, and the sentence itself, which
// must appear in the core's source. A refusal the stub invented fails here.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { readdirSync, readFileSync } from "node:fs";
import { createServer } from "node:net";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { SCHEMA_DESCRIPTORS } from "../../../packages/api-client/src/testing.ts";
import { validateValue } from "../../../packages/api-client/test/_validate.mjs";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const coreInternal = join(appDir, "..", "..", "..", "core", "internal");

const coreSource = ["purchasing", "inventory", join("platform", "authz"), join("platform", "httpx")]
  .flatMap((dir) =>
    readdirSync(join(coreInternal, dir))
      .filter((f) => f.endsWith(".go") && !f.endsWith("_test.go"))
      .map((f) => readFileSync(join(coreInternal, dir, f), "utf8")),
  )
  .join("\n");

let child;
let base;

async function freePort() {
  return new Promise((resolve) => {
    const probe = createServer();
    probe.listen(0, "127.0.0.1", () => {
      const { port } = probe.address();
      probe.close(() => resolve(port));
    });
  });
}

before(async () => {
  const port = await freePort();
  base = `http://127.0.0.1:${port}`;
  child = spawn(process.execPath, [join(appDir, "test", "stub", "buying-stock-stub.mjs")], {
    env: { ROLE_STUB_PORT: String(port) },
    stdio: "ignore",
  });
  for (let i = 0; i < 100; i += 1) {
    try {
      if ((await fetch(`${base}/healthz`)).ok) return;
    } catch {
      /* not up yet */
    }
    await new Promise((r) => setTimeout(r, 50));
  }
  throw new Error("the stub did not start");
});

after(() => {
  child?.kill();
});

async function call(method, path, body) {
  const headers = { Authorization: "Bearer stub-token", "X-Org": "hh-demo" };
  let payload;
  if (body !== undefined) {
    headers["Content-Type"] = "application/json";
    payload = JSON.stringify(body);
  }
  const res = await fetch(`${base}${path}`, { method, headers, body: payload });
  const text = await res.text();
  let json = null;
  try {
    json = JSON.parse(text);
  } catch {
    /* not json */
  }
  return { status: res.status, json };
}

const reset = () => fetch(`${base}/_stub/reset`, { method: "POST" });

function declared(d) {
  if (d.k === "ref") return declared(SCHEMA_DESCRIPTORS[d.name]);
  if (d.k === "allOf") return Object.assign({}, ...d.of.map(declared));
  if (d.k === "object") return d.props;
  return {};
}

function undeclared(value, d, path = "value", out = []) {
  if (d.k === "ref") return undeclared(value, SCHEMA_DESCRIPTORS[d.name], path, out);
  if (d.k === "allOf") {
    const props = declared(d);
    if (value !== null && typeof value === "object" && !Array.isArray(value)) {
      for (const key of Object.keys(value)) if (props[key] === undefined) out.push(`${path}.${key}`);
      for (const [key, leg] of Object.entries(props)) if (value[key] !== undefined) undeclared(value[key], leg, `${path}.${key}`, out);
    }
    return out;
  }
  if (d.k === "object") {
    if (value === null || typeof value !== "object" || Array.isArray(value)) return out;
    for (const key of Object.keys(value)) if (d.props[key] === undefined) out.push(`${path}.${key}`);
    for (const [key, leg] of Object.entries(d.props)) if (value[key] !== undefined) undeclared(value[key], leg, `${path}.${key}`, out);
    return out;
  }
  if (d.k === "array" && Array.isArray(value)) value.forEach((v, i) => undeclared(v, d.of, `${path}[${i}]`, out));
  if (d.k === "record" && value !== null && typeof value === "object") for (const [k, v] of Object.entries(value)) undeclared(v, d.of, `${path}.${k}`, out);
  if (d.k === "union") {
    const leg = d.of.find((l) => l.k !== "null");
    if (leg !== undefined && value !== null) undeclared(value, leg, path, out);
  }
  return out;
}

function holds(schemaName, value) {
  const descriptor = SCHEMA_DESCRIPTORS[schemaName];
  assert.ok(descriptor !== undefined, `no descriptor named ${schemaName}`);
  assert.deepEqual(validateValue(value, descriptor), [], `${schemaName} does not fit the contract`);
  assert.deepEqual(undeclared(value, descriptor), [], `${schemaName} carries fields the contract does not declare`);
}

function refused(res, status, fragment, inSource = fragment) {
  assert.equal(res.status, status);
  holds("Error", res.json);
  assert.ok(res.json.error.includes(fragment), `expected "${fragment}" in "${res.json.error}"`);
  assert.ok(coreSource.includes(inSource), `the core's source does not contain the sentence "${inSource}": the stub invented it`);
}

const later = (days) => {
  const at = new Date();
  at.setUTCDate(at.getUTCDate() + days);
  return new Intl.DateTimeFormat("en-CA", { timeZone: "America/Toronto", year: "numeric", month: "2-digit", day: "2-digit" }).format(at);
};

const BRANCH = "b0000000-0000-4000-8000-000000000001";

test("the shortfall worklist: the contract's key, and each row in the contract's shape", async () => {
  await reset();
  const res = await call("GET", "/api/v1/inventory/shortfall");
  assert.equal(res.status, 200);
  // The contract won (FROM-PRINCIPAL 72): the core writes the list under "items", and the core's
  // source is the proof; the ERP's old "shortfall" key is gone on both sides.
  assert.ok(/"items":\s+rows,\s*"on_order_degraded":/.test(coreSource), "the core no longer writes the shortfall page under the key items");
  assert.equal(typeof res.json.on_order_degraded, "boolean");
  assert.equal(res.json.shortfall, undefined);
  assert.equal(res.json.items.length, 3);
  for (const row of res.json.items) {
    holds("InventoryShortfallRow", row);
  }
  const gap = res.json.items.find((r) => r.sku === "DBS-BRUS-DIM");
  assert.equal(gap.projected_base, -40);
  assert.equal(gap.on_order_base, 0);
  assert.ok(res.json.items.every((r) => r.short_base > 0), "a listed row is always short");
});

test("the shortfall read is fenced to the caller's reach, in the core's words", async () => {
  await reset();
  // The caller holds one branch (Trenton). A branch named outside that reach is refused in words
  // on both reads that name one (the core's fenceNamedBranch: the worklist and the reorder control).
  const other = "b0000000-0000-4000-8000-000000000002";
  refused(await call("GET", `/api/v1/inventory/shortfall?branch_id=${other}`), 403, "you do not have access to that branch");
  const rows = (await call("GET", "/api/v1/inventory/shortfall")).json.items;
  const gap = rows.find((r) => r.sku === "DBS-BRUS-DIM");
  refused(await call("POST", "/api/v1/inventory/reorder-po", { product_id: gap.product_id, branch_id: other, qty_base: 0 }), 403, "you do not have access to that branch");
  assert.equal((await call("GET", `/api/v1/inventory/shortfall?branch_id=${BRANCH}`)).status, 200);
  // A scoped caller with no grants reads nothing on the unnamed read (the reach set), and the
  // branch it names is out of reach: the fence switch is that caller.
  await fetch(`${base}/_stub/fence`, { method: "POST" });
  const empty = await call("GET", "/api/v1/inventory/shortfall");
  assert.equal(empty.status, 200);
  assert.deepEqual(empty.json.items, []);
  refused(await call("POST", "/api/v1/inventory/reorder-po", { product_id: gap.product_id, branch_id: BRANCH, qty_base: 0 }), 403, "you do not have access to that branch");
});

test("the quick reorder names the draft that was already open, and still drafts the full quantity", async () => {
  await reset();
  const rows = (await call("GET", "/api/v1/inventory/shortfall")).json.items;
  const gap = rows.find((r) => r.sku === "DBS-BRUS-DIM");
  const first = await call("POST", "/api/v1/inventory/reorder-po", { product_id: gap.product_id, branch_id: BRANCH, qty_base: 0 });
  assert.equal(first.status, 200);
  // No draft was open before this press: the pointer is absent, not empty.
  assert.equal(first.json.existing_draft_po_id, undefined);
  assert.equal(first.json.existing_draft_po_no, undefined);
  // A draft never covers a shortfall, so the next press drafts again; the core reads the open
  // draft BEFORE the new one is made, and the answer names it (the two keys come together).
  const second = await call("POST", "/api/v1/inventory/reorder-po", { product_id: gap.product_id, branch_id: BRANCH, qty_base: 0 });
  assert.equal(second.status, 200);
  holds("InventoryReorderResult", second.json);
  assert.equal(second.json.existing_draft_po_id, first.json.po_id);
  assert.equal(second.json.existing_draft_po_no, first.json.po_no);
  const po = await call("GET", `/api/v1/purchasing/pos/${second.json.po_id}`);
  assert.equal(po.json.status, "DRAFT");
  assert.notEqual(po.json.id, first.json.po_id);
});

test("the PO register answers in the contract's shapes, and a PO reads with its lines", async () => {
  await reset();
  const list = await call("GET", "/api/v1/purchasing/pos");
  assert.equal(list.status, 200);
  holds("PurchasingPOPage", list.json);
  assert.equal(list.json.items.length, 4);
  const one = await call("GET", `/api/v1/purchasing/pos/${list.json.items[0].id}`);
  assert.equal(one.status, 200);
  holds("PurchasingPODetail", one.json);
});

test("drafting from a shortfall row makes a DRAFT PO the contract's shape, and does not cover the row", async () => {
  await reset();
  const rows = (await call("GET", "/api/v1/inventory/shortfall")).json.items;
  const gap = rows.find((r) => r.sku === "DBS-BRUS-DIM");
  const made = await call("POST", "/api/v1/inventory/reorder-po", { product_id: gap.product_id, branch_id: BRANCH, qty_base: 0 });
  assert.equal(made.status, 200);
  holds("InventoryReorderResult", made.json);
  assert.equal(made.json.suggested_qty_base, 240);
  const po = await call("GET", `/api/v1/purchasing/pos/${made.json.po_id}`);
  holds("PurchasingPODetail", po.json);
  assert.equal(po.json.status, "DRAFT");
  assert.equal(po.json.lines[0].qty_ordered_base, 240);
  const after = (await call("GET", "/api/v1/inventory/shortfall")).json.items;
  // FROM-PRINCIPAL 72: a draft never covers a shortfall. The row stays, with its draft, and the draft is not on order.
  const stays = after.find((r) => r.sku === "DBS-BRUS-DIM");
  assert.equal(stays.draft_po_id, made.json.po_id);
  assert.equal(stays.draft_po_no, made.json.po_no);
  assert.equal(stays.on_order_base, 0);
  assert.equal(stays.projected_base, -40);
});

test("placing moves a draft to ORDERED, and the product's incoming read answers under the contract's key", async () => {
  await reset();
  const rows = (await call("GET", "/api/v1/inventory/shortfall")).json.items;
  const gap = rows.find((r) => r.sku === "DBS-BRUS-DIM");
  const made = await call("POST", "/api/v1/inventory/reorder-po", { product_id: gap.product_id, branch_id: BRANCH, qty_base: 0 });
  const date = later(5);
  const dated = await call("PUT", `/api/v1/purchasing/pos/${made.json.po_id}/expected-date`, { expected_at: date });
  assert.equal(dated.status, 200);
  holds("PurchasingPODetail", dated.json);
  const how = await call("PUT", `/api/v1/purchasing/pos/${made.json.po_id}/delivery-method`, { delivery_method: "PICKUP" });
  assert.equal(how.json.delivery_method, "PICKUP");
  const placed = await call("POST", `/api/v1/purchasing/pos/${made.json.po_id}/transition`, { to: "ORDERED" });
  assert.equal(placed.status, 200);
  holds("PurchasingPODetail", placed.json);
  assert.equal(placed.json.status, "ORDERED");
  // Placing is what closes the gap: only now does the row leave the worklist.
  const left = (await call("GET", "/api/v1/inventory/shortfall")).json.items;
  assert.equal(left.find((r) => r.sku === "DBS-BRUS-DIM"), undefined);
  const incoming = await call("GET", `/api/v1/purchasing/incoming?product_id=${gap.product_id}`);
  assert.equal(incoming.status, 200);
  assert.ok(/map\[string\]any\{"items": lines/.test(coreSource), "the core no longer writes the incoming read under the key items");
  assert.equal(incoming.json.lines, undefined);
  assert.equal(incoming.json.items.length, 1);
  holds("PurchasingIncomingLine", incoming.json.items[0]);
  assert.equal(incoming.json.items[0].expected_at, date);
  assert.equal(incoming.json.items[0].qty_outstanding_base, 240);
});

test("the refusals are the core's sentences, with the module prefix stripped as the core strips it", async () => {
  await reset();
  const rows = (await call("GET", "/api/v1/inventory/shortfall")).json.items;
  const gap = rows.find((r) => r.sku === "DBS-BRUS-DIM");
  const made = await call("POST", "/api/v1/inventory/reorder-po", { product_id: gap.product_id, branch_id: BRANCH, qty_base: 0 });
  const id = made.json.po_id;
  const lineId = (await call("GET", `/api/v1/purchasing/pos/${id}`)).json.lines[0].id;
  await call("DELETE", `/api/v1/purchasing/pos/${id}/lines/${lineId}`);
  refused(await call("POST", `/api/v1/purchasing/pos/${id}/transition`, { to: "ORDERED" }), 409, "cannot order an empty purchase order, add at least one line");
  refused(await call("POST", `/api/v1/purchasing/pos/${id}/transition`, { to: "RECEIVED" }), 409, "is reached by receiving goods, not by a status change");
  refused(await call("PUT", `/api/v1/purchasing/pos/${id}/expected-date`, { expected_at: "soon" }), 400, "expected_at must be a date (YYYY-MM-DD)");
  await call("POST", `/api/v1/purchasing/pos/${id}/transition`, { to: "CANCELLED" });
  refused(await call("DELETE", `/api/v1/purchasing/pos/${id}/lines/${lineId}`), 409, "cannot edit the lines of a CANCELLED purchase order", "cannot edit the lines of a %s purchase order");
  refused(await call("POST", `/api/v1/purchasing/pos/${id}/transition`, { to: "CANCELLED" }), 409, "purchase order is already CANCELLED", "purchase order is already %s");
  refused(await call("GET", "/api/v1/purchasing/incoming"), 400, "product_id is required");
});

test("the line editor on a draft: add, change the quantity or the cost, each in the contract's shape", async () => {
  await reset();
  const rows = (await call("GET", "/api/v1/inventory/shortfall")).json.items;
  const gap = rows.find((r) => r.sku === "DBS-BRUS-DIM");
  const made = await call("POST", "/api/v1/inventory/reorder-po", { product_id: gap.product_id, branch_id: BRANCH, qty_base: 0 });
  const id = made.json.po_id;
  // Change the quantity and the cost of the drafted line.
  const lineId = (await call("GET", `/api/v1/purchasing/pos/${id}`)).json.lines[0].id;
  const changed = await call("PUT", `/api/v1/purchasing/pos/${id}/lines/${lineId}`, { qty_ordered_base: 480, unit_cost_cents: 900 });
  assert.equal(changed.status, 200);
  holds("PurchasingPODetail", changed.json);
  assert.equal(changed.json.lines[0].qty_ordered_base, 480);
  assert.equal(changed.json.lines[0].unit_cost_cents, 900);
  assert.equal(changed.json.lines[0].line_total_cents, 432_000);
  // Add a line from the catalogue.
  const catalogue = await call("GET", "/api/v1/products?active=true&limit=200");
  assert.equal(catalogue.status, 200);
  const added = await call("POST", `/api/v1/purchasing/pos/${id}/lines`, { product_id: catalogue.json.items[0].id, qty_ordered_base: 10 });
  assert.equal(added.status, 200);
  holds("PurchasingPODetail", added.json);
  assert.equal(added.json.line_count, 2);
  assert.equal(added.json.lines[1].qty_ordered_base, 10);
  // Remove the added line again.
  const removed = await call("DELETE", `/api/v1/purchasing/pos/${id}/lines/${added.json.lines[1].id}`);
  assert.equal(removed.status, 200);
  assert.equal(removed.json.line_count, 1);
});

test("the line editor's refusals are the core's sentences, and the freeze once placed", async () => {
  await reset();
  const rows = (await call("GET", "/api/v1/inventory/shortfall")).json.items;
  const gap = rows.find((r) => r.sku === "DBS-BRUS-DIM");
  const made = await call("POST", "/api/v1/inventory/reorder-po", { product_id: gap.product_id, branch_id: BRANCH, qty_base: 0 });
  const id = made.json.po_id;
  const lineId = (await call("GET", `/api/v1/purchasing/pos/${id}`)).json.lines[0].id;
  refused(await call("PUT", `/api/v1/purchasing/pos/${id}/lines/${lineId}`, {}), 400, "the body names nothing to change");
  refused(await call("PUT", `/api/v1/purchasing/pos/${id}/lines/${lineId}`, { qty_ordered_base: 0 }), 400, "line quantity must be positive");
  refused(await call("PUT", `/api/v1/purchasing/pos/${id}/lines/${lineId}`, { unit_cost_cents: -1 }), 400, "line unit cost cannot be negative");
  refused(await call("POST", `/api/v1/purchasing/pos/${id}/lines`, { product_id: "", qty_ordered_base: 10 }), 400, "line 0 needs a product_id", "line %d needs a product_id");
  refused(await call("POST", `/api/v1/purchasing/pos/${id}/lines`, { product_id: gap.product_id, qty_ordered_base: 0 }), 400, "line 0 quantity must be greater than zero", "line %d quantity must be greater than zero");
  await call("POST", `/api/v1/purchasing/pos/${id}/transition`, { to: "ORDERED" });
  refused(await call("PUT", `/api/v1/purchasing/pos/${id}/lines/${lineId}`, { qty_ordered_base: 10 }), 409, "cannot edit the lines of a ORDERED purchase order", "cannot edit the lines of a %s purchase order");
  refused(await call("POST", `/api/v1/purchasing/pos/${id}/lines`, { product_id: gap.product_id, qty_ordered_base: 10 }), 409, "cannot edit the lines of a ORDERED purchase order", "cannot edit the lines of a %s purchase order");
  refused(await call("DELETE", `/api/v1/purchasing/pos/${id}/lines/${lineId}`), 409, "cannot edit the lines of a ORDERED purchase order", "cannot edit the lines of a %s purchase order");
});

test("a line id that is not a uuid, or a quantity no line can hold, is refused in words", async () => {
  await reset();
  const rows = (await call("GET", "/api/v1/inventory/shortfall")).json.items;
  const gap = rows.find((r) => r.sku === "DBS-BRUS-DIM");
  const made = await call("POST", "/api/v1/inventory/reorder-po", { product_id: gap.product_id, branch_id: BRANCH, qty_base: 0 });
  const id = made.json.po_id;
  const lineId = (await call("GET", `/api/v1/purchasing/pos/${id}`)).json.lines[0].id;
  // The core's checkLinePath: an order id that is not a uuid names no order (404); a line id that
  // is not one cannot be right (422, in words), whatever its shape, and it never reaches the rows.
  refused(await call("PUT", `/api/v1/purchasing/pos/not-a-uuid/lines/${lineId}`, { qty_ordered_base: 10 }), 404, "not found");
  refused(await call("PUT", `/api/v1/purchasing/pos/${id}/lines/${"-".repeat(36)}`, { qty_ordered_base: 10 }), 422, "line id must be a uuid");
  refused(await call("PUT", `/api/v1/purchasing/pos/${id}/lines/${"0".repeat(34)}`, { qty_ordered_base: 10 }), 422, "line id must be a uuid");
  refused(await call("PUT", `/api/v1/purchasing/pos/${id}/lines/${"0".repeat(33)}`, { qty_ordered_base: 10 }), 422, "line id must be a uuid");
  refused(await call("DELETE", `/api/v1/purchasing/pos/${id}/lines/${"0".repeat(34)}`), 422, "line id must be a uuid");
  const po = await call("GET", `/api/v1/purchasing/pos/${id}`);
  assert.equal(po.json.line_count, 1, "a refused shape changed nothing");
  // The ceiling the core states (numeric(19,4) overflows past 1e12): both doors say so in words.
  refused(await call("PUT", `/api/v1/purchasing/pos/${id}/lines/${lineId}`, { qty_ordered_base: 1e12 + 1 }), 422, "line quantity is beyond what an order line can hold (1000000000000 at most)", "quantity is beyond what an order line can hold (%.0f at most)");
  refused(await call("POST", `/api/v1/purchasing/pos/${id}/lines`, { product_id: gap.product_id, qty_ordered_base: 1e12 + 1 }), 422, "line 0 quantity is beyond what an order line can hold (1000000000000 at most)", "quantity is beyond what an order line can hold (%.0f at most)");
});

test("the gate's refusal is the shared Forbidden body, and a bill cannot be posted", async () => {
  await reset();
  const bill = await call("POST", "/api/v1/purchasing/bills/x/post", {});
  assert.equal(bill.status, 403);
  assert.deepEqual(bill.json, { error: "Forbidden" });
  assert.ok(coreSource.includes('{"error":"Forbidden"}'), "the shared refusal body is not the core's");
  await fetch(`${base}/_stub/wall`, { method: "POST" });
  const walled = await call("POST", "/api/v1/inventory/reorder-po", { product_id: "x", branch_id: BRANCH, qty_base: 0 });
  assert.equal(walled.status, 403);
  assert.deepEqual(walled.json, { error: "Forbidden" });
});

test("the branch name and a vendor's codes answer in the contract's shapes", async () => {
  await reset();
  const branches = await call("GET", "/api/v1/branches");
  assert.equal(branches.status, 200);
  assert.ok(Array.isArray(branches.json));
  for (const b of branches.json) holds("Branch", b);
  const vendorId = (await call("GET", "/api/v1/purchasing/pos")).json.items.find((p) => p.vendor_name === "Alcli (demo)").vendor_id;
  const skus = await call("GET", `/api/v1/purchasing/vendors/${vendorId}/skus`);
  assert.equal(skus.status, 200);
  for (const k of skus.json.items) holds("PurchasingVendorSKU", k);
  assert.ok(skus.json.items.some((k) => k.vendor_sku === "ALC-BRU-DIM-60"));
});
