// The Buying and Stock stub's landed cost routes against the contract and against the core, field
// by field: every answer fits the generated wire descriptors, every refusal sentence is the core's,
// the refusal order is the core's, and the quiet no-op the core answers like a real apply is
// reproduced (the screens are built around it).
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { BRANCH, coreSourceOf, holds, refusedBy, startStub } from "./contract-kit.mjs";

const refused = refusedBy(coreSourceOf(["landedcost", "purchasing"]));

let stub;
before(async () => {
  stub = await startStub();
});
after(() => stub.stop());

const LC = "/api/v1/landed-costs";
const orderId = async (no) => (await stub.call("GET", "/api/v1/purchasing/pos")).json.items.find((p) => p.po_no === no).id;

test("the list answers in the contract's shape and a scoped caller must name a branch", async () => {
  await stub.reset();
  const list = await stub.call("GET", `${LC}?branch_id=${BRANCH}`);
  assert.equal(list.status, 200);
  holds("LandedCostPage", list.json);
  assert.equal(list.json.items.length, 1);
  assert.equal(list.json.items[0].status, "POSTED");
  refused(await stub.call("GET", LC), 403, "caller cannot access this branch");
  refused(await stub.call("GET", `${LC}?branch_id=b0000000-0000-4000-8000-000000000002`), 403, "caller cannot access this branch");
  const drafts = await stub.call("GET", `${LC}?branch_id=${BRANCH}&status=DRAFT`);
  assert.deepEqual(drafts.json.items, []);
});

test("a capture is a draft, and the core's validations come in the core's order", async () => {
  await stub.reset();
  const po = await orderId("PO-TRT-66");
  refused(await stub.call("POST", LC, {}), 400, "branch_id required");
  refused(await stub.call("POST", LC, { branch_id: BRANCH, currency: "EUR" }), 400, "currency must be CAD or USD");
  refused(await stub.call("POST", LC, { branch_id: BRANCH, allocation_basis: "MASS" }), 400, "allocation_basis must be WEIGHT, VALUE or QUANTITY");
  refused(await stub.call("POST", LC, { branch_id: BRANCH, freight_cents: -1 }), 400, "freight/duty/handling must be non-negative");
  const made = await stub.call("POST", LC, { po_id: po, po_ref: "PO-TRT-66", branch_id: BRANCH, currency: "USD", fx_rate: 1.352, freight_cents: 10_000, duty_cents: 0, handling_cents: 505, allocation_basis: "WEIGHT" });
  assert.equal(made.status, 201);
  holds("LandedCostCreateResult", made.json);
  assert.equal(made.json.status, "DRAFT");
  const one = await stub.call("GET", `${LC}/${made.json.id}`);
  holds("LandedCostDetail", one.json);
  // USD cents times the rate, rounded, are the books' cents (the core's convertToCAD).
  assert.equal(one.json.freight_cad_cents, 13_520);
  assert.equal(one.json.handling_cad_cents, 683);
  assert.deepEqual(one.json.allocations, []);
  // A CAD document never carries a manual rate.
  const cad = await stub.call("POST", LC, { po_id: po, branch_id: BRANCH, currency: "CAD", fx_rate: 9, freight_cents: 100 });
  assert.equal((await stub.call("GET", `${LC}/${cad.json.id}`)).json.fx_rate, 1);
  refused(await stub.call("GET", `${LC}/e0000000-0000-4000-8000-0000000000ff`), 404, "not found");
});

test("the preview splits the total by the compared basis, largest remainder exact, special buys flagged and left out", async () => {
  await stub.reset();
  const po = await orderId("PO-TRT-66");
  const made = await stub.call("POST", LC, { po_id: po, po_ref: "PO-TRT-66", branch_id: BRANCH, freight_cents: 10_001, allocation_basis: "VALUE" });
  const value = await stub.call("GET", `${LC}/${made.json.id}/preview`);
  assert.equal(value.status, 200);
  holds("LandedCostAllocationPreview", value.json);
  assert.equal(value.json.basis, "VALUE");
  assert.equal(value.json.total_cad_cents, 10_001);
  const special = value.json.lines.filter((l) => l.is_special_buy);
  assert.equal(special.length, 1);
  assert.equal(special[0].allocated_cad_cents, 0);
  const shares = value.json.lines.filter((l) => !l.is_special_buy);
  assert.equal(shares.map((l) => l.allocated_cad_cents).reduce((a, b) => a + b, 0), 10_001);
  const weight = await stub.call("GET", `${LC}/${made.json.id}/preview?basis=WEIGHT`);
  assert.equal(weight.json.basis, "WEIGHT");
  const unknown = await stub.call("GET", `${LC}/${made.json.id}/preview?basis=MASS`);
  assert.equal(unknown.json.basis, "VALUE", "an unknown basis falls back to the document's own");
  const noOrder = await stub.call("POST", LC, { branch_id: BRANCH, freight_cents: 100 });
  refused(await stub.call("GET", `${LC}/${noOrder.json.id}/preview`), 422, "landed cost has no po_id to allocate across");
});

test("apply posts a draft, and an apply of anything else answers the same body and changes nothing", async () => {
  await stub.reset();
  const po = await orderId("PO-TRT-66");
  const made = await stub.call("POST", LC, { po_id: po, po_ref: "PO-TRT-66", branch_id: BRANCH, freight_cents: 20_000, allocation_basis: "QUANTITY" });
  const id = made.json.id;
  const applied = await stub.call("POST", `${LC}/${id}/apply`);
  assert.equal(applied.status, 200);
  holds("LandedCostStatusResult", applied.json);
  assert.equal(applied.json.status, "POSTED");
  const doc = (await stub.call("GET", `${LC}/${id}`)).json;
  assert.equal(doc.status, "POSTED");
  assert.equal(doc.allocations.reduce((a, x) => a + x.allocated_cad_cents + x.sold_through_cad_cents, 0), 20_000);
  // The quirk: a second apply, and a retro of a posted document, answer as if they worked.
  const again = await stub.call("POST", `${LC}/${id}/apply`);
  assert.deepEqual(again.json, { status: "POSTED" });
  const retro = await stub.call("POST", `${LC}/${id}/retro`);
  assert.deepEqual(retro.json, { status: "RETRO_ADJUSTED" });
  assert.equal((await stub.call("GET", `${LC}/${id}`)).json.status, "POSTED", "the document did not move");
  assert.ok(/StatusPosted/.test(coreSourceOf(["landedcost"])) && /idempotent no-op/.test(coreSourceOf(["landedcost"])), "the core no longer answers a no-op like a real apply");
});

test("a late freight splits each share by what is still on hand", async () => {
  await stub.reset();
  const po = await orderId("PO-TRT-66");
  const prior = (await stub.call("GET", `${LC}?branch_id=${BRANCH}`)).json.items[0];
  const lines = (await stub.call("GET", `/api/v1/purchasing/pos/${po}`)).json.lines;
  const keep = lines.find((l) => l.sku === "ALC-STEP-48");
  const made = await stub.call("POST", LC, {
    po_id: po,
    po_ref: "PO-TRT-66",
    branch_id: BRANCH,
    freight_cents: 10_000,
    retro_of_id: prior.id,
    on_hand: [{ product_id: keep.product_id, po_line_id: keep.id, on_hand_base: 4 }],
  });
  const retro = await stub.call("POST", `${LC}/${made.json.id}/retro`);
  assert.deepEqual(retro.json, { status: "RETRO_ADJUSTED" });
  const doc = (await stub.call("GET", `${LC}/${made.json.id}`)).json;
  holds("LandedCostDetail", doc);
  assert.equal(doc.retro_of_id, prior.id);
  const row = doc.allocations[0];
  assert.equal(row.allocated_cad_cents, 4000);
  assert.equal(row.sold_through_cad_cents, 6000);
});

test("void reverses an applied document; a draft cannot be voided; a void of a voided one is a no-op", async () => {
  await stub.reset();
  const po = await orderId("PO-TRT-66");
  const made = await stub.call("POST", LC, { po_id: po, po_ref: "PO-TRT-66", branch_id: BRANCH, freight_cents: 500 });
  refused(await stub.call("POST", `${LC}/${made.json.id}/void`), 409, "cannot void a DRAFT landed cost", "cannot void a %s landed cost");
  const prior = (await stub.call("GET", `${LC}?branch_id=${BRANCH}&status=POSTED`)).json.items[0];
  const voided = await stub.call("POST", `${LC}/${prior.id}/void`);
  holds("LandedCostStatusResult", voided.json);
  assert.equal(voided.json.status, "VOIDED");
  assert.equal((await stub.call("GET", `${LC}/${prior.id}`)).json.status, "VOIDED");
  assert.deepEqual((await stub.call("POST", `${LC}/${prior.id}/void`)).json, { status: "VOIDED" });
});

test("the wall refuses the writes of a role that is not a manager, and the reads stay open", async () => {
  await stub.reset();
  await stub.post("/_stub/role", { role: "payables-receivables" });
  const walled = await stub.call("POST", LC, { branch_id: BRANCH, freight_cents: 100 });
  assert.equal(walled.status, 403);
  assert.deepEqual(walled.json, { error: "Forbidden" });
  assert.equal((await stub.call("GET", `${LC}?branch_id=${BRANCH}`)).status, 200);
  await stub.reset();
  assert.equal((await stub.call("POST", LC, { branch_id: BRANCH, freight_cents: 100 })).status, 201);
});
