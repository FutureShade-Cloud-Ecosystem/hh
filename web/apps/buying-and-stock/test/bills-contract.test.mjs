// The Buying and Stock stub's vendor bill routes against the contract and against the core, field
// by field: every answer fits the generated wire descriptors, every refusal sentence is the core's,
// the three way match gives the verdicts the core's EvaluateLine gives, and a post re-runs it and
// refuses an unaccepted variance (service.go CreateBill, PostBill, match.go).
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { BRANCH, coreSourceOf, holds, refusedBy, startStub } from "./contract-kit.mjs";

const refused = refusedBy(coreSourceOf(["purchasing"]));
const P = "/api/v1/purchasing";

let stub;
before(async () => {
  stub = await startStub();
});
after(() => stub.stop());

const money = async () => stub.post("/_stub/role", { role: "payables-receivables" });
const order = async (no) => {
  const id = (await stub.call("GET", `${P}/pos`)).json.items.find((p) => p.po_no === no).id;
  return (await stub.call("GET", `${P}/pos/${id}`)).json;
};
const bill = async (no) => {
  const item = (await stub.call("GET", `${P}/bills?branch_id=${BRANCH}`)).json.items.find((b) => b.bill_no === no);
  return (await stub.call("GET", `${P}/bills/${item.id}`)).json;
};

test("the bill list answers in the contract's shape, with the match state beside each bill and the filters narrowing it", async () => {
  await stub.reset();
  const res = await stub.call("GET", `${P}/bills?branch_id=${BRANCH}`);
  assert.equal(res.status, 200);
  holds("PurchasingBillPage", res.json);
  assert.deepEqual(res.json.items.map((b) => [b.bill_no, b.status, b.match_state]).sort(), [["INV-7781", "POSTED", "MATCHED"], ["INV-7790", "DRAFT", "VARIANCE"], ["INV-7795", "DRAFT", "SHORT_BILLED"]]);
  assert.equal((await stub.call("GET", `${P}/bills?branch_id=${BRANCH}&status=DRAFT`)).json.items.length, 2);
  assert.equal((await stub.call("GET", `${P}/bills?branch_id=${BRANCH}&match_state=VARIANCE`)).json.items.length, 1);
  refused(await stub.call("GET", `${P}/bills?branch_id=b0000000-0000-4000-8000-000000000002`), 403, "caller cannot access this branch");
});

test("a bill reads with its lines and the verdict on each, in the core's arithmetic", async () => {
  await stub.reset();
  const b = await bill("INV-7790");
  holds("PurchasingBillDetail", b);
  assert.equal(b.status, "DRAFT");
  assert.equal(b.total_cents, 10 * 26200);
  const l = b.lines[0];
  assert.equal(l.match_state, "VARIANCE");
  // Ten billed against six received: four ahead of the goods. 10 x (262.00 - 240.00) is 22000 over.
  assert.equal(l.qty_variance_base, 4);
  assert.equal(l.price_variance_cents, 22_000);
  assert.equal(l.qty_received_base, 6);
  assert.equal(l.po_unit_cost_cents, 24_000);
  const short = await bill("INV-7795");
  assert.equal(short.lines[0].match_state, "SHORT_BILLED");
  assert.equal(short.lines[0].qty_variance_base, -2);
  assert.equal(short.lines[0].price_variance_cents, 0);
  const matched = await bill("INV-7781");
  assert.equal(matched.status, "POSTED");
  assert.ok(matched.gl_journal_id);
  assert.notEqual(matched.posted_at, null);
});

test("entering a bill is a draft, matched on entry; the validators come in the core's order", async () => {
  await stub.reset();
  await money();
  const po = await order("PO-PLM-67");
  const line = po.lines[0];
  const enter = (body) => stub.call("POST", `${P}/bills`, body);
  refused(await enter({}), 400, "bill_no (the vendor's invoice number) is required");
  refused(await enter({ bill_no: "A" }), 400, "po_id is required, a bill with no purchase order cannot be matched");
  refused(await enter({ bill_no: "A", po_id: po.id }), 400, "a bill needs at least one line");
  refused(await enter({ bill_no: "A", po_id: po.id, tax_cents: -1, lines: [{ description: "x", qty_billed_base: 1, unit_cost_cents: 1 }] }), 400, "tax cannot be negative");
  refused(await enter({ bill_no: "A", po_id: "c0000000-0000-4000-8000-0000000000ff", lines: [{ description: "x", qty_billed_base: 1, unit_cost_cents: 1 }] }), 404, "not found");
  refused(await enter({ bill_no: "A", po_id: po.id, currency: "USD", lines: [{ po_line_id: line.id, description: "x", qty_billed_base: 1, unit_cost_cents: 1 }] }), 400, "a USD purchase order needs a positive fx_rate");
  refused(await enter({ bill_no: "A", po_id: po.id, lines: [{ po_line_id: line.id, description: "x", qty_billed_base: 0, unit_cost_cents: 1 }] }), 400, "bill line 1 quantity must be greater than zero", "bill line %d quantity must be greater than zero");
  refused(await enter({ bill_no: "A", po_id: po.id, lines: [{ po_line_id: line.id, description: "x", qty_billed_base: 1, unit_cost_cents: -1 }] }), 400, "bill line 1 unit cost cannot be negative", "bill line %d unit cost cannot be negative");
  refused(await enter({ bill_no: "A", po_id: po.id, lines: [{ po_line_id: "elsewhere", description: "x", qty_billed_base: 1, unit_cost_cents: 1 }] }), 400, "bill line 1 references a line that is not on this purchase order", "bill line %d references a line that is not on this purchase order");
  const made = await enter({ bill_no: "INV-9001", po_id: po.id, tax_cents: 3120, lines: [{ po_line_id: line.id, description: "Unilock Wall Cap", qty_billed_base: 6, unit_cost_cents: 24_000 }] });
  assert.equal(made.status, 201);
  holds("PurchasingBillDetail", made.json);
  assert.equal(made.json.status, "DRAFT");
  assert.equal(made.json.match_state, "MATCHED");
  assert.equal(made.json.subtotal_cents, 144_000);
  assert.equal(made.json.total_cents, 144_000 + 3120);
  assert.equal(made.json.currency, "CAD");
  // A USD bill converts its own unit and tax at its own rate, before it is compared with the order.
  const usd = await enter({ bill_no: "INV-9002", po_id: po.id, currency: "USD", fx_rate: 1.35, tax_cents: 1000, lines: [{ po_line_id: line.id, description: "x", qty_billed_base: 1, unit_cost_cents: 17_778 }] });
  assert.equal(usd.json.lines[0].unit_cost_cents, 24_000);
  assert.equal(usd.json.tax_cents, 1350);
  assert.equal(usd.json.fx_rate, 1.35);
  assert.ok(coreSourceOf(["purchasing"]).includes("cannot bill a %s purchase order"));
  const draft = (await stub.call("GET", "/api/v1/inventory/shortfall")).json.items[0];
  const made2 = await stub.call("POST", "/api/v1/inventory/reorder-po", { product_id: draft.product_id, branch_id: BRANCH, qty_base: 0 });
  refused(await enter({ bill_no: "A", po_id: made2.json.po_id, lines: [{ description: "x", qty_billed_base: 1, unit_cost_cents: 1 }] }), 409, "cannot bill a DRAFT purchase order", "cannot bill a %s purchase order");
});

test("a bill with no order line is unmatched, and the header takes the worst line's state", async () => {
  await stub.reset();
  await money();
  const po = await order("PO-PLM-67");
  const made = await stub.call("POST", `${P}/bills`, {
    bill_no: "INV-9100",
    po_id: po.id,
    lines: [
      { po_line_id: po.lines[0].id, description: "Unilock Wall Cap", qty_billed_base: 6, unit_cost_cents: 24_000 },
      { description: "Freight surcharge", qty_billed_base: 1, unit_cost_cents: 5000 },
    ],
  });
  assert.equal(made.json.lines[0].match_state, "MATCHED");
  assert.equal(made.json.lines[1].match_state, "UNMATCHED");
  assert.equal(made.json.match_state, "UNMATCHED");
});

test("a post of a bill inside the tolerance books it once; a second post is refused", async () => {
  await stub.reset();
  await money();
  const b = await bill("INV-7795");
  assert.equal(b.status, "DRAFT");
  // A short billed bill is not a variance: it posts without an acceptance.
  const posted = await stub.call("POST", `${P}/bills/${b.id}/post`, {});
  assert.equal(posted.status, 200);
  holds("PurchasingBillDetail", posted.json);
  assert.equal(posted.json.status, "POSTED");
  assert.ok(posted.json.gl_journal_id);
  refused(await stub.call("POST", `${P}/bills/${b.id}/post`, {}), 409, "bill is already POSTED", "bill is already %s");
  const po = await order("PO-PLM-67");
  assert.equal(po.lines[0].qty_billed_base, 4, "the posted bill moved the order line's billed quantity");
});

test("a variance bill is refused until the variance is accepted, in the core's words with the core's figure", async () => {
  await stub.reset();
  await money();
  const b = await bill("INV-7790");
  const refusal = await stub.call("POST", `${P}/bills/${b.id}/post`, {});
  // Ten billed at 262.00 against six received at 240.00: 262000 owed, 144000 of it relieved, 118000 over.
  refused(refusal, 409, "bill INV-7790 is in VARIANCE (118000 cents against the purchase order), post it with accept_variance to record who accepted it", "is in VARIANCE (%d cents against the purchase order), post it with accept_variance to record who accepted it");
  assert.equal((await bill("INV-7790")).status, "DRAFT");
  const accepted = await stub.call("POST", `${P}/bills/${b.id}/post`, { accept_variance: true, note: "Vendor agreed to credit the freight." });
  assert.equal(accepted.status, 200);
  holds("PurchasingBillDetail", accepted.json);
  assert.equal(accepted.json.status, "POSTED");
  assert.equal(accepted.json.variance_accepted_note, "Vendor agreed to credit the freight.");
  assert.ok(accepted.json.variance_accepted_by);
});

test("the post runs the match again against what has arrived by then", async () => {
  await stub.reset();
  const b = await bill("INV-7790");
  assert.equal(b.match_state, "VARIANCE");
  // The rest arrives (the yard receives as the Procurement manager does): ten received, so the
  // quantity is in step and only the price is off.
  const po = await order("PO-PLM-67");
  assert.equal((await stub.call("POST", `${P}/pos/${po.id}/receive`, { lines: [{ po_line_id: po.lines[0].id, qty_base: 4 }] })).status, 200);
  await money();
  const refusal = await stub.call("POST", `${P}/bills/${b.id}/post`, {});
  assert.equal(refusal.status, 409);
  const after = await bill("INV-7790");
  assert.equal(after.lines[0].qty_variance_base, 0, "the refused post refreshed the verdict it was refused on");
  assert.equal(after.lines[0].price_variance_cents, 22_000);
});

test("the match tolerance and the cost policy read in the contract's shapes", async () => {
  await stub.reset();
  const tol = await stub.call("GET", `${P}/tolerance?branch_id=${BRANCH}`);
  holds("PurchasingTolerance", tol.json);
  assert.equal(tol.json.is_branch_override, false);
  assert.equal(tol.json.resolved_from_default, true);
  const pol = await stub.call("GET", `${P}/policy?branch_id=${BRANCH}`);
  holds("PurchasingPolicy", pol.json);
  assert.equal(pol.json.cost_visibility, "MANAGER_UP");
  assert.equal(pol.json.untagged_fallback, "CONFIRM");
});

test("the super admin sets the tolerance and the policy, in the core's order and words", async () => {
  await stub.reset();
  await stub.post("/_stub/role", { role: "super-admin" });
  const set = (body) => stub.call("PUT", `${P}/tolerance`, body);
  refused(await set({ qty_tolerance_pct: -1, price_tolerance_pct: 1, price_tolerance_abs_cents: 0 }), 400, "tolerances cannot be negative");
  refused(await set({ qty_tolerance_pct: 26, price_tolerance_pct: 1, price_tolerance_abs_cents: 0 }), 400, "a match tolerance above 25% is not a tolerance, it is an off switch", "a match tolerance above 25%% is not a tolerance, it is an off switch");
  refused(await set({ qty_tolerance_pct: 2, price_tolerance_pct: 1, price_tolerance_abs_cents: 0, branch_id: "b0000000-0000-4000-8000-000000000002" }), 403, "caller cannot access this branch");
  const done = await set({ qty_tolerance_pct: 5, price_tolerance_pct: 0.5, price_tolerance_abs_cents: 100 });
  assert.equal(done.status, 200);
  holds("PurchasingTolerance", done.json);
  assert.equal(done.json.qty_tolerance_pct, 5);
  const branch = await set({ qty_tolerance_pct: 3, price_tolerance_pct: 3, price_tolerance_abs_cents: 0, branch_id: BRANCH });
  assert.equal(branch.json.is_branch_override, true);
  const policy = (body) => stub.call("PUT", `${P}/policy`, body);
  refused(await policy({ cost_visibility: "NOBODY" }), 400, "cost_visibility must be MANAGER_UP, ALL_STAFF or HIDDEN");
  refused(await policy({ cost_visibility: "ALL_STAFF", untagged_fallback: "MAYBE" }), 400, "untagged_fallback must be CONFIRM or REFUSE");
  const hidden = await policy({ cost_visibility: "HIDDEN", untagged_fallback: "REFUSE" });
  holds("PurchasingPolicy", hidden.json);
  assert.equal(hidden.json.cost_visibility, "HIDDEN");
  // The policy is what hides cost: the order read follows it.
  const po = (await stub.call("GET", `${P}/pos`)).json.items[0];
  assert.equal(po.subtotal_cents, undefined);
});

test("the wall refuses bill writes to a role that is not a money seat, and the rules to all but the super admin", async () => {
  await stub.reset();
  const b = await bill("INV-7790");
  for (const [method, path, body] of [
    ["POST", `${P}/bills`, { bill_no: "A" }],
    ["POST", `${P}/bills/${b.id}/post`, {}],
    ["PUT", `${P}/tolerance`, { qty_tolerance_pct: 1, price_tolerance_pct: 1, price_tolerance_abs_cents: 0 }],
    ["PUT", `${P}/policy`, { cost_visibility: "ALL_STAFF" }],
  ]) {
    const res = await stub.call(method, path, body);
    assert.equal(res.status, 403, `${method} ${path}`);
    assert.deepEqual(res.json, { error: "Forbidden" });
  }
  await stub.post("/_stub/role", { role: "payables-receivables" });
  assert.equal((await stub.call("PUT", `${P}/tolerance`, { qty_tolerance_pct: 1, price_tolerance_pct: 1, price_tolerance_abs_cents: 0 })).status, 403, "the rules are the super admin's alone");
  assert.equal((await stub.call("POST", `${P}/bills/${b.id}/post`, { accept_variance: true, note: "ok" })).status, 200);
});
