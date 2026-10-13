// The Buying and Stock stub's receiving routes against the contract and against the core, field by
// field: every answer fits the generated wire descriptors, every refusal sentence is the core's, and
// the refusal order is the core's (service.go Receive, AttachLandedCost, vendor_order.go SendPO).
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { request as httpRequest } from "node:http";
import { BRANCH, coreSourceOf, holds, refusedBy, startStub } from "./contract-kit.mjs";

const refused = refusedBy(coreSourceOf(["purchasing", "landedcost"]));
const P = "/api/v1/purchasing";

let stub;
before(async () => {
  stub = await startStub();
});
after(() => stub.stop());

const order = async (no) => {
  const id = (await stub.call("GET", `${P}/pos`)).json.items.find((p) => p.po_no === no).id;
  return (await stub.call("GET", `${P}/pos/${id}`)).json;
};

test("the receiving queue answers in the contract's shape: placed and part received orders, soonest first", async () => {
  await stub.reset();
  const res = await stub.call("GET", `${P}/receiving`);
  assert.equal(res.status, 200);
  holds("PurchasingWorklistItemPage", res.json);
  assert.deepEqual(res.json.items.map((i) => i.po_no), ["PO-TRT-141", "PO-TRT-136", "PO-TRT-67"]);
  assert.ok(res.json.items.every((i) => i.open_lines >= 1 && i.qty_outstanding_base > 0));
  refused(await stub.call("GET", `${P}/receiving?branch_id=b0000000-0000-4000-8000-000000000002`), 403, "caller cannot access this branch");
});

test("a receive moves the order on, makes a receipt in the contract's shape, and the receipt reads back", async () => {
  await stub.reset();
  const po = await order("PO-TRT-136");
  const line = po.lines[0];
  const first = await stub.call("POST", `${P}/pos/${po.id}/receive`, { lines: [{ po_line_id: line.id, qty_base: 4 }], notes: "one pallet short" });
  assert.equal(first.status, 200);
  holds("PurchasingReceipt", first.json);
  assert.equal(first.json.grn_no, "PO-TRT-136/R1");
  assert.equal(first.json.value_cad_cents, 4 * 2500);
  assert.equal(first.json.notes, "one pallet short");
  let after = await order("PO-TRT-136");
  assert.equal(after.status, "PARTIAL");
  assert.equal(after.lines[0].qty_received_base, 4);
  assert.equal(after.receipts.length, 1);
  holds("PurchasingPODetail", after);
  const rest = await stub.call("POST", `${P}/pos/${po.id}/receive`, { lines: [{ po_line_id: line.id, qty_base: 6 }] });
  assert.equal(rest.json.grn_no, "PO-TRT-136/R2");
  after = await order("PO-TRT-136");
  assert.equal(after.status, "RECEIVED");
  const read = await stub.call("GET", `${P}/receipts/${first.json.id}`);
  holds("PurchasingReceipt", read.json);
  assert.equal(read.json.lines.length, 1);
  assert.equal((await stub.call("GET", `${P}/receiving`)).json.items.some((i) => i.po_no === "PO-TRT-136"), false);
});

test("a receive the core would refuse is refused in the core's words and order", async () => {
  await stub.reset();
  const po = await order("PO-TRT-136");
  const line = po.lines[0];
  const recv = (body, id = po.id) => stub.call("POST", `${P}/pos/${id}/receive`, body);
  refused(await recv({ lines: [] }), 400, "nothing to receive, supply at least one line");
  refused(await recv({ lines: [{ po_line_id: "elsewhere", qty_base: 1 }] }), 400, "line elsewhere does not belong to this purchase order", "line %s does not belong to this purchase order");
  refused(await recv({ lines: [{ po_line_id: line.id, qty_base: 0 }] }), 400, "receive quantity on line 1 must be greater than zero", "receive quantity on line %d must be greater than zero");
  refused(await recv({ lines: [{ po_line_id: line.id, qty_base: 10.5 }] }), 409, "over-receipt on line 1, 10.5000 requested but only 10.0000 outstanding", "over-receipt on line %d, %s requested but only %s outstanding");
  // Two entries of 6 against 10 outstanding fold into 12 and are refused as one over receipt.
  refused(await recv({ lines: [{ po_line_id: line.id, qty_base: 6 }, { po_line_id: line.id, qty_base: 6 }] }), 409, "over-receipt on line 1, 12.0000 requested but only 10.0000 outstanding", "over-receipt on line %d, %s requested but only %s outstanding");
  const received = await order("PO-TRT-66");
  refused(await recv({ lines: [{ po_line_id: received.lines[0].id, qty_base: 1 }] }, received.id), 409, "purchase order is already fully received");
  assert.equal((await order("PO-TRT-136")).lines[0].qty_received_base, 0, "a refusal changed nothing");
  assert.ok(coreSourceOf(["purchasing"]).includes("cannot receive against a DRAFT purchase order, place the order first"));
});

test("a landed cost is attached to a receipt once, applied at once, and shows in the landed costs", async () => {
  await stub.reset();
  const po = await order("PO-TRT-136");
  const rc = (await stub.call("POST", `${P}/pos/${po.id}/receive`, { lines: [{ po_line_id: po.lines[0].id, qty_base: 10 }] })).json;
  const attach = (body) => stub.call("POST", `${P}/receipts/${rc.id}/landed-cost`, body);
  refused(await attach({ freight_cents: -1, allocation_basis: "VALUE" }), 400, "freight/duty/handling must be non-negative");
  refused(await attach({ allocation_basis: "VALUE" }), 400, "a landed cost with no freight, duty or handling costs nothing");
  refused(await attach({ freight_cents: 100, currency: "EUR", allocation_basis: "VALUE" }), 400, "currency must be CAD or USD");
  refused(await attach({ freight_cents: 100, currency: "USD", allocation_basis: "VALUE" }), 400, "a USD purchase order needs a positive fx_rate");
  const done = await attach({ freight_cents: 48_000, handling_cents: 3550, allocation_basis: "WEIGHT" });
  assert.equal(done.status, 200);
  holds("PurchasingReceipt", done.json);
  assert.ok(done.json.landed_cost_id);
  refused(await attach({ freight_cents: 100, allocation_basis: "VALUE" }), 409, "receipt PO-TRT-136/R1 already carries a landed cost", "receipt %s already carries a landed cost");
  const lc = await stub.call("GET", `/api/v1/landed-costs/${done.json.landed_cost_id}`);
  holds("LandedCostDetail", lc.json);
  assert.equal(lc.json.status, "POSTED");
  assert.equal(lc.json.po_ref, "PO-TRT-136/R1");
  assert.equal(lc.json.allocations.reduce((a, x) => a + x.allocated_cad_cents + x.sold_through_cad_cents, 0), 51_550);
});

test("the cost payload is withheld as {} from a role the policy hides cost from, and the keys leave the order", async () => {
  await stub.reset();
  const po = await order("PO-TRT-141");
  const open = await stub.call("GET", `${P}/pos/${po.id}/costs`);
  holds("PurchasingPOCosts", open.json);
  assert.equal(open.json.subtotal_cents, 12 * 82_000);
  await stub.post("/_stub/flag", { name: "costsHidden", value: true });
  const hidden = await stub.call("GET", `${P}/pos/${po.id}/costs`);
  assert.deepEqual(hidden.json, {});
  const detail = (await stub.call("GET", `${P}/pos/${po.id}`)).json;
  holds("PurchasingPODetail", detail);
  assert.equal(detail.subtotal_cents, undefined);
  assert.equal(detail.lines[0].unit_cost_cents, undefined);
  const queue = (await stub.call("GET", `${P}/receiving`)).json.items;
  assert.ok(queue.every((i) => i.value_open_cents === undefined));
});

test("the order document is a file in either form, without prices when cost is hidden", async () => {
  await stub.reset();
  const po = await order("PO-TRT-141");
  const csv = await stub.call("GET", `${P}/pos/${po.id}/document.csv`);
  assert.equal(csv.status, 200);
  assert.match(csv.headers.get("content-type"), /text\/csv/);
  assert.match(csv.text, /Unit cost/);
  const printable = await stub.call("GET", `${P}/pos/${po.id}/document`);
  assert.match(printable.headers.get("content-type"), /text\/html/);
  assert.match(printable.text, /Purchase order PO-TRT-141/);
  await stub.post("/_stub/flag", { name: "costsHidden", value: true });
  assert.doesNotMatch((await stub.call("GET", `${P}/pos/${po.id}/document.csv`)).text, /Unit cost/);
});

test("a send goes to the tagged desk, one attempt is one send, and a key cannot cross orders", async () => {
  await stub.reset();
  const a = await order("PO-TRT-141");
  const b = await order("PO-TRT-136");
  const vendor = (id) => id;
  await stub.call("PUT", `${P}/vendors/${vendor(a.vendor_id)}/contact`, { contact_name: "Orders desk", email: "po@techo.test" });
  const sent = await stub.call("POST", `${P}/pos/${a.id}/send`, { idempotency_key: "attempt-1" });
  assert.equal(sent.status, 200);
  holds("PurchasingPOSend", sent.json);
  assert.equal(sent.json.recipient, "po@techo.test");
  assert.equal(sent.json.recipient_kind, "VENDOR_CONTACT");
  assert.equal(sent.json.status, "DELIVERED");
  const again = await stub.call("POST", `${P}/pos/${a.id}/send`, { idempotency_key: "attempt-1" });
  assert.equal(again.json.id, sent.json.id, "the same attempt is the same send");
  const history = await stub.call("GET", `${P}/pos/${a.id}/sends`);
  holds("PurchasingPOSendPage", history.json);
  assert.equal(history.json.items.length, 1);
  refused(await stub.call("POST", `${P}/pos/${b.id}/send`, { idempotency_key: "attempt-1" }), 409, 'already belongs to a send for another order, a new order needs a new key', "already belongs to a send for another order, a new order needs a new key");
});

test("an untagged send is refused until the fallback is confirmed, and every refusal is the core's sentence", async () => {
  await stub.reset();
  const po = await order("PO-TRT-136");
  const send = (body) => stub.call("POST", `${P}/pos/${po.id}/send`, body);
  refused(await send({ idempotency_key: "k1" }), 400, "this vendor has no email on file, tag an ordering contact to send anything at all");
  await stub.call("PUT", `${P}/vendors/${po.vendor_id}`, { order_email: "orders@alcli.test" });
  refused(await send({ idempotency_key: "k1" }), 400, "nobody is tagged for ordering at this vendor, confirm the fallback recipient or tag a contact");
  const ok = await send({ idempotency_key: "k1", confirm_fallback: true });
  holds("PurchasingPOSend", ok.json);
  assert.equal(ok.json.recipient_kind, "FALLBACK");
  assert.equal(ok.json.recipient, "orders@alcli.test");
  await stub.reset();
  const phone = await order("PO-TRT-136");
  await stub.call("PUT", `${P}/vendors/${phone.vendor_id}/contact`, { phone: "613 555 0140" });
  refused(await stub.call("POST", `${P}/pos/${phone.id}/send`, {}), 400, "this vendor's ordering contact is a phone number, add an email to it and the order can be sent");
  await stub.post("/_stub/flag", { name: "refuseUntagged", value: true });
  await stub.call("DELETE", `${P}/vendors/${phone.vendor_id}/contact`);
  refused(await stub.call("POST", `${P}/pos/${phone.id}/send`, {}), 400, "no ordering contact is tagged for this vendor, tag one and the order goes to the right desk");
});

test("a send of a draft or a cancelled order is refused, and a send that fails to deliver is still a 200 that says FAILED", async () => {
  await stub.reset();
  const rows = (await stub.call("GET", "/api/v1/inventory/shortfall")).json.items;
  const made = await stub.call("POST", "/api/v1/inventory/reorder-po", { product_id: rows[0].product_id, branch_id: BRANCH, qty_base: 0 });
  refused(await stub.call("POST", `${P}/pos/${made.json.po_id}/send`, {}), 409, "place the order first, a draft is still being edited");
  await stub.call("POST", `${P}/pos/${made.json.po_id}/transition`, { to: "CANCELLED" });
  refused(await stub.call("POST", `${P}/pos/${made.json.po_id}/send`, {}), 409, "this order was cancelled, nothing to send");
  const po = await order("PO-TRT-141");
  await stub.call("PUT", `${P}/vendors/${po.vendor_id}/contact`, { email: "po@techo.test" });
  await stub.post("/_stub/flag", { name: "sendFails", value: true });
  const failed = await stub.call("POST", `${P}/pos/${po.id}/send`, { idempotency_key: "k9" });
  assert.equal(failed.status, 200);
  holds("PurchasingPOSend", failed.json);
  assert.equal(failed.json.status, "FAILED");
  assert.equal(failed.json.detail, "no mail transport is wired");
});

test("the wall refuses the writes of a role that is not a manager, and the reads stay open", async () => {
  await stub.reset();
  const po = await order("PO-TRT-136");
  await stub.post("/_stub/role", { role: "payables-receivables" });
  for (const [path, body] of [
    [`${P}/pos/${po.id}/receive`, { lines: [{ po_line_id: po.lines[0].id, qty_base: 1 }] }],
    [`${P}/pos/${po.id}/send`, {}],
    [`${P}/receipts/x/landed-cost`, { freight_cents: 1 }],
  ]) {
    const res = await stub.call("POST", path, body);
    assert.equal(res.status, 403, path);
    assert.deepEqual(res.json, { error: "Forbidden" });
  }
  assert.equal((await stub.call("GET", `${P}/receiving`)).status, 200);
});

// One request on a connection of its own, so nothing can reuse a pooled socket or replay the request
// behind the test's back (fetch may retry on a reused keep-alive connection, which would send the write
// twice and swallow the very rejection this test is about). Resolves with what happened.
function oneShot(base, method, path, body) {
  return new Promise((resolve) => {
    const payload = JSON.stringify(body);
    const req = httpRequest(`${base}${path}`, { method, agent: false, headers: { Authorization: "Bearer stub-token", "Content-Type": "application/json", "Content-Length": Buffer.byteLength(payload), Connection: "close" } }, (res) => {
      res.resume();
      res.on("end", () => resolve({ answered: true, status: res.statusCode }));
      res.on("error", () => resolve({ answered: false }));
    });
    req.on("error", () => resolve({ answered: false }));
    req.end(payload);
  });
}

test("an answer lost on the way back still leaves the write done, exactly once", async () => {
  await stub.reset();
  const po = await order("PO-TRT-136");
  await stub.post("/_stub/flag", { name: "dropNext", value: true });
  const result = await oneShot(stub.base, "POST", `${P}/pos/${po.id}/receive`, { lines: [{ po_line_id: po.lines[0].id, qty_base: 2 }] });
  assert.equal(result.answered, false, "the answer was lost");
  assert.equal((await order("PO-TRT-136")).lines[0].qty_received_base, 2, "the write was made once");
});
