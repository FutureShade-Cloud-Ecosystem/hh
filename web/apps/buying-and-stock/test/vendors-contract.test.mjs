// The Buying and Stock stub's vendor routes against the contract and against the core, field by
// field: every answer fits the generated wire descriptors, every refusal sentence is the core's,
// and the refusal order is the core's (vendor_master.go and vendor_order.go).
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { coreSourceOf, holds, refusedBy, startStub } from "./contract-kit.mjs";

const refused = refusedBy(coreSourceOf(["purchasing"]));
const V = "/api/v1/purchasing/vendors";

let stub;
before(async () => {
  stub = await startStub();
});
after(() => stub.stop());

const alcli = async () => (await stub.call("GET", V)).json.items.find((v) => v.vendor_no === "V-ALC");

test("the vendor list answers in the contract's shape", async () => {
  await stub.reset();
  const list = await stub.call("GET", V);
  assert.equal(list.status, 200);
  holds("PurchasingVendorPage", list.json);
  assert.equal(list.json.items.length, 4);
});

test("a vendor is created with the number minted, and the validators come in the core's order", async () => {
  await stub.reset();
  refused(await stub.call("POST", V, { name: "  " }), 400, "vendor name is required");
  refused(await stub.call("POST", V, { name: "x".repeat(201) }), 400, "vendor name is too long (200 characters at most)");
  refused(await stub.call("POST", V, { name: "Rymar", order_email: "a@b.test, c@d.test" }), 400, "order_email must be one email address");
  refused(await stub.call("POST", V, { name: "Rymar", payment_terms_days: 366 }), 400, "payment_terms_days must be between 0 and 365");
  // The name is judged before the email, the email before the terms.
  refused(await stub.call("POST", V, { name: "", order_email: "bad", payment_terms_days: 999 }), 400, "vendor name is required");
  refused(await stub.call("POST", V, { name: "Rymar", order_email: "bad", payment_terms_days: 999 }), 400, "order_email must be one email address");
  const made = await stub.call("POST", V, { name: " Rymar Turf ", order_email: "orders@rymar.test" });
  assert.equal(made.status, 201);
  holds("PurchasingVendorMaster", made.json);
  assert.equal(made.json.name, "Rymar Turf");
  assert.equal(made.json.payment_terms_days, 30, "the core fills 30 days");
  assert.equal(made.json.is_active, true);
  assert.equal((await stub.call("GET", V)).json.items.length, 5);
});

test("a correction changes what the body names, and refuses an empty body or a bad id", async () => {
  await stub.reset();
  const v = await alcli();
  const renamed = await stub.call("PUT", `${V}/${v.id}`, { name: "Alcli Stone", payment_terms_days: 45 });
  assert.equal(renamed.status, 200);
  holds("PurchasingVendorMaster", renamed.json);
  assert.equal(renamed.json.name, "Alcli Stone");
  assert.equal(renamed.json.payment_terms_days, 45);
  const off = await stub.call("PUT", `${V}/${v.id}`, { is_active: false });
  assert.equal(off.json.is_active, false);
  refused(await stub.call("PUT", `${V}/${v.id}`, {}), 400, "the body names nothing to change");
  refused(await stub.call("PUT", `${V}/not-a-uuid`, { name: "x" }), 400, "vendor id must be a uuid");
  refused(await stub.call("PUT", `${V}/a0000000-0000-4000-8000-0000000000ff`, { name: "x" }), 404, "not found");
});

test("the ordering contact: not tagged reads 200, tagging needs a way to reach, and removing untags", async () => {
  await stub.reset();
  const v = await alcli();
  const none = await stub.call("GET", `${V}/${v.id}/contact`);
  assert.equal(none.status, 200);
  holds("PurchasingVendorContactResult", none.json);
  assert.equal(none.json.tagged, false);
  refused(await stub.call("PUT", `${V}/${v.id}/contact`, { contact_name: "Desk" }), 400, "an ordering contact needs an email or a phone, one of the two is what makes it a contact");
  refused(await stub.call("PUT", `${V}/${v.id}/contact`, { email: "a b@c.test" }), 400, "that ordering email address is not an address");
  refused(await stub.call("PUT", `${V}/${v.id}/contact`, { contact_name: "x".repeat(121), email: "a@b.test" }), 400, "contact_name is at most 120 characters");
  refused(await stub.call("PUT", `${V}/${v.id}/contact`, { email: "a@b.test", phone: "9".repeat(33) }), 400, "an ordering email is at most 254 characters and a phone at most 32");
  const tagged = await stub.call("PUT", `${V}/${v.id}/contact`, { contact_name: "Orders desk", email: "po@alcli.test" });
  assert.equal(tagged.status, 200);
  holds("PurchasingVendorContactResult", tagged.json);
  assert.equal(tagged.json.tagged, true);
  assert.equal((await stub.call("GET", `${V}/${v.id}/contact`)).json.contact.email, "po@alcli.test");
  const removed = await stub.call("DELETE", `${V}/${v.id}/contact`);
  holds("PurchasingVendorContactResult", removed.json);
  assert.equal(removed.json.tagged, false);
});

test("the vendor's codes: list, map a product, correct it, remove it", async () => {
  await stub.reset();
  const v = await alcli();
  const list = await stub.call("GET", `${V}/${v.id}/skus`);
  holds("PurchasingVendorSKUPage", list.json);
  const brus = list.json.items.find((k) => k.sku === "DBS-BRUS-DIM");
  assert.equal(brus.vendor_sku, "ALC-BRU-DIM-60");
  const saved = await stub.call("PUT", `${V}/${v.id}/skus`, { product_id: brus.product_id, vendor_sku: "ALC-BRU-DIM-61", vendor_uom: "bdl", qty_per_vendor_uom: 12.5 });
  assert.equal(saved.status, 200);
  holds("PurchasingVendorSKU", saved.json);
  assert.equal(saved.json.vendor_uom, "BDL", "the core upper cases the unit");
  const after = (await stub.call("GET", `${V}/${v.id}/skus`)).json.items;
  assert.equal(after.length, list.json.items.length, "a correction replaces the mapping, it does not add one");
  const gone = await stub.call("DELETE", `${V}/${v.id}/skus/${brus.product_id}`);
  assert.equal(gone.status, 204);
  assert.equal((await stub.call("GET", `${V}/${v.id}/skus`)).json.items.some((k) => k.product_id === brus.product_id), false);
});

test("a mapping the core would refuse is refused in the core's words and order", async () => {
  await stub.reset();
  const v = await alcli();
  const brus = (await stub.call("GET", `${V}/${v.id}/skus`)).json.items[0];
  const put = (body) => stub.call("PUT", `${V}/${v.id}/skus`, body);
  refused(await put({ vendor_sku: "A" }), 400, "a mapping needs a product");
  refused(await put({ product_id: brus.product_id, vendor_sku: "x".repeat(65) }), 400, "a vendor code is at most 64 characters");
  refused(await put({ product_id: brus.product_id, vendor_sku: "A", vendor_uom: "BDL" }), 400, "a vendor unit needs the number of our units it holds, one without the other cannot be counted");
  refused(await put({ product_id: brus.product_id, vendor_sku: "A", qty_per_vendor_uom: 12 }), 400, "a vendor unit needs the number of our units it holds, one without the other cannot be counted");
  refused(await put({ product_id: brus.product_id }), 400, "an empty mapping is the same as no mapping, remove it instead");
});

test("the echoed reference finds an order however the vendor wrote it", async () => {
  await stub.reset();
  const found = await stub.call("GET", `/api/v1/purchasing/vendor-ref?ref=${encodeURIComponent("#po trt 66")}`);
  assert.equal(found.status, 200);
  holds("PurchasingPO", found.json);
  assert.equal(found.json.po_no, "PO-TRT-66");
  refused(await stub.call("GET", "/api/v1/purchasing/vendor-ref?ref=%20"), 400, "type the order number the vendor quoted");
  refused(await stub.call("GET", "/api/v1/purchasing/vendor-ref?ref=nothing"), 404, "not found");
});

test("the wall refuses the writes of a role that is not a manager, and the reads stay open", async () => {
  await stub.reset();
  await stub.post("/_stub/role", { role: "payables-receivables" });
  const v = await alcli();
  for (const [method, path, body] of [
    ["POST", V, { name: "x" }],
    ["PUT", `${V}/${v.id}`, { name: "x" }],
    ["PUT", `${V}/${v.id}/contact`, { email: "a@b.test" }],
    ["DELETE", `${V}/${v.id}/contact`],
    ["PUT", `${V}/${v.id}/skus`, { product_id: "x", vendor_sku: "A" }],
    ["DELETE", `${V}/${v.id}/skus/x`],
  ]) {
    const res = await stub.call(method, path, body);
    assert.equal(res.status, 403, `${method} ${path}`);
    assert.deepEqual(res.json, { error: "Forbidden" });
  }
  assert.equal((await stub.call("GET", V)).status, 200);
  assert.equal((await stub.call("GET", `${V}/${v.id}/skus`)).status, 200);
});
