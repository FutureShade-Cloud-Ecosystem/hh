// The returns draft's own unit tests: the framework-free state behind the Lines step. Each rule here
// is one the screen leans on and the commit re-guards: a quantity above open is refused before the
// wire, lines from two orders live in one draft, every line carries its own disposition, and one
// credit note is for one branch.
import { test } from "node:test";
import assert from "node:assert/strict";
import { ReturnDraft } from "./draft.js";
import { filterOrders, readCustomerLines } from "./lines.js";

const uuid = (n) => `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`;

function line(n, over = {}) {
  return {
    order_line_id: uuid(n),
    product_id: uuid(n + 500),
    sku: `SKU-${n}`,
    description: `Product ${n}`,
    qty_sold: 10,
    qty_returned: 0,
    qty_open: 10,
    unit_price_cents: 656,
    unit_cost_cents: 410,
    line_total_cents: 6560,
    tax_cents: 853,
    money_returned_cents: 0,
    tax_returned_cents: 0,
    shelf_state: "FULFILLED",
    ...over,
  };
}

function order(n, over = {}) {
  return {
    order_id: uuid(n),
    order_no: `SO-PLM-${n}`,
    branch_id: uuid(9000),
    branch_name: "Point Loma",
    created_at: "2026-10-01T10:00:00Z",
    job_label: `Job ${n}`,
    source: "TILL",
    sale_type: "COLLECT_NOW",
    tender: "CARD",
    fulfilment_state: "FULFILLED",
    invoice_id: uuid(n + 100),
    card_refundable: true,
    returnable: true,
    not_returnable_reason: "",
    lines: [line(n * 10 + 1), line(n * 10 + 2, { qty_open: 4, qty_returned: 6 })],
    ...over,
  };
}

const answer = (orders) => ({ customer_id: uuid(1), items: orders });

test("a readable customer lines answer becomes orders; a half readable one becomes nothing", () => {
  const read = readCustomerLines(answer([order(16), order(11)]));
  assert.ok(read !== null);
  assert.equal(read.length, 2);
  assert.equal(read[0].orderNo, "SO-PLM-16");
  assert.equal(read[0].lines[1].qtyOpen, 4);
  assert.equal(readCustomerLines({ customer_id: uuid(1) }), null, "no items array");
  const broken = order(16);
  delete broken.lines[0].qty_open;
  assert.equal(readCustomerLines(answer([broken])), null, "a line missing a field refuses the whole answer");
  assert.equal(readCustomerLines(answer([order(16, { source: "PORTAL" })])), null, "an unknown source");
  assert.equal(readCustomerLines(null), null);
});

test("a quantity above the open quantity is refused before the wire, and the draft keeps its old quantity", () => {
  const draft = new ReturnDraft(readCustomerLines(answer([order(16)])));
  const id = uuid(161);
  assert.deepEqual(draft.setQty(id, "3"), { ok: true });
  const refused = draft.setQty(id, "11");
  assert.equal(refused.ok, false);
  assert.match(refused.words, /only 10 open/);
  assert.equal(draft.qtyOf(id), 3, "the refused entry changed nothing");
  assert.equal(draft.setQty(id, "10").ok, true, "the open quantity itself is fine");
  // The second line has 4 open of 10 sold: the cap is open, never sold.
  const capped = draft.setQty(uuid(162), "5");
  assert.equal(capped.ok, false);
  assert.match(capped.words, /only 4 open/);
});

test("a blank, zero or unreadable quantity takes the line out of the draft", () => {
  const draft = new ReturnDraft(readCustomerLines(answer([order(16)])));
  const id = uuid(161);
  draft.setQty(id, "2");
  assert.equal(draft.qtyOf(id), 2);
  assert.equal(draft.setQty(id, "").ok, true);
  assert.equal(draft.qtyOf(id), 0);
  draft.setQty(id, "2");
  draft.setQty(id, "0");
  assert.equal(draft.qtyOf(id), 0);
  draft.setQty(id, "2");
  assert.equal(draft.setQty(id, "abc").ok, true);
  assert.equal(draft.qtyOf(id), 0);
  assert.equal(draft.setQty(id, "-3").ok, true);
  assert.equal(draft.qtyOf(id), 0);
});

test("lines from two orders ride one draft, in order of the screen", () => {
  const draft = new ReturnDraft(readCustomerLines(answer([order(16), order(11)])));
  draft.setQty(uuid(161), "2");
  draft.setQty(uuid(111), "1");
  const picked = draft.pickedLines();
  assert.equal(picked.length, 2);
  assert.deepEqual(
    picked.map((p) => [p.orderNo, p.orderLineId, p.qty]),
    [
      ["SO-PLM-16", uuid(161), 2],
      ["SO-PLM-11", uuid(111), 1],
    ],
  );
  assert.equal(draft.orderCount, 2);
});

test("each line carries its own disposition; a line with no choice keeps the draft from being ready", () => {
  const draft = new ReturnDraft(readCustomerLines(answer([order(16), order(11)])));
  assert.equal(draft.ready, false, "nothing picked is not ready");
  draft.setQty(uuid(161), "2");
  draft.setQty(uuid(111), "1");
  assert.equal(draft.ready, false, "two lines with no disposition");
  assert.equal(draft.missingDispositions, 2);
  draft.setDisposition(uuid(161), "RESTOCK");
  assert.equal(draft.missingDispositions, 1);
  draft.setDisposition(uuid(111), "DAMAGED");
  assert.equal(draft.ready, true);
  assert.deepEqual(
    draft.pickedLines().map((p) => p.disposition),
    ["RESTOCK", "DAMAGED"],
  );
  draft.setDisposition(uuid(111), "NO_RETURN");
  assert.equal(draft.dispositionOf(uuid(111)), "NO_RETURN");
  assert.equal(draft.setDisposition(uuid(111), "SCRAP").ok, false, "only the three choices exist");
  assert.equal(draft.dispositionOf(uuid(111)), "NO_RETURN");
});

test("choosing a disposition on an unpicked line picks one unit; an exhausted line cannot be picked", () => {
  const exhausted = order(16, { lines: [line(161, { qty_open: 0, qty_returned: 10 })] });
  const draft = new ReturnDraft(readCustomerLines(answer([exhausted, order(11)])));
  assert.equal(draft.setDisposition(uuid(161), "RESTOCK").ok, false);
  assert.equal(draft.qtyOf(uuid(161)), 0);
  assert.equal(draft.setQty(uuid(161), "1").ok, false, "nothing is open on it");
  assert.equal(draft.setDisposition(uuid(111), "RESTOCK").ok, true);
  assert.equal(draft.qtyOf(uuid(111)), 1);
});

test("one credit note is for one branch: a line from another branch's order is refused in words", () => {
  const other = order(11, { branch_id: uuid(9001), branch_name: "Julian" });
  const draft = new ReturnDraft(readCustomerLines(answer([order(16), other])));
  assert.equal(draft.setQty(uuid(161), "1").ok, true);
  const refused = draft.setQty(uuid(111), "1");
  assert.equal(refused.ok, false);
  assert.match(refused.words, /one branch/);
  assert.match(refused.words, /Julian/, "the refusal names the order's branch");
  assert.match(refused.words, /Point Loma/, "and the branch the note is already at");
  assert.equal(draft.qtyOf(uuid(111)), 0);
  // Taking the first branch's lines out frees the draft to start on the other branch.
  draft.setQty(uuid(161), "");
  assert.equal(draft.setQty(uuid(111), "1").ok, true);
});

test("the sales order number filters the line list only; it never changes the draft", () => {
  const orders = readCustomerLines(answer([order(16), order(11)]));
  assert.deepEqual(filterOrders(orders, "").map((o) => o.orderNo), ["SO-PLM-16", "SO-PLM-11"]);
  assert.deepEqual(filterOrders(orders, " so-plm-11 ").map((o) => o.orderNo), ["SO-PLM-11"]);
  assert.deepEqual(filterOrders(orders, "16").map((o) => o.orderNo), ["SO-PLM-16"]);
  assert.deepEqual(filterOrders(orders, "nope"), []);
  const draft = new ReturnDraft(orders);
  draft.setQty(uuid(161), "2");
  filterOrders(orders, "11");
  assert.equal(draft.qtyOf(uuid(161)), 2, "a filter hides rows; a picked line stays picked");
});

test("a draft with a picked line from a hidden order still counts it, and the counts are the draft's own", () => {
  const draft = new ReturnDraft(readCustomerLines(answer([order(16), order(11)])));
  draft.setQty(uuid(161), "2");
  draft.setQty(uuid(162), "4");
  assert.equal(draft.pickedLines().length, 2);
  assert.equal(draft.orderCount, 1);
});

test("branch_name must be a string on every order", () => {
  const broken = order(16);
  delete broken.branch_name;
  assert.equal(readCustomerLines(answer([broken])), null);
});

test("an order the read marks returnable false keeps its reason and its lines cannot be drafted", () => {
  const blocked = order(16, { returnable: false, not_returnable_reason: "This quote order was not rung at the counter." });
  const read = readCustomerLines(answer([blocked, order(11)]));
  assert.equal(read[0].returnable, false);
  assert.equal(read[0].notReturnableReason, "This quote order was not rung at the counter.");
  assert.equal(read[1].returnable, true, "absent means returnable");
  const draft = new ReturnDraft(read);
  const refused = draft.setQty(uuid(161), "1");
  assert.equal(refused.ok, false);
  assert.match(refused.words, /not rung at the counter/);
  assert.equal(draft.setDisposition(uuid(161), "RESTOCK").ok, false);
  assert.equal(draft.qtyOf(uuid(161)), 0);
  assert.equal(draft.setQty(uuid(111), "1").ok, true);
});

test("returnable false without a reason still refuses, in generic words", () => {
  const read = readCustomerLines(answer([order(16, { returnable: false, not_returnable_reason: "" })]));
  assert.match(read[0].notReturnableReason, /cannot be returned/);
});

test("the guessed field name is gone: returnable_reason is not read, and returnable must be a boolean", () => {
  const guessed = readCustomerLines(answer([order(16, { returnable: false, not_returnable_reason: "", returnable_reason: "guess" })]));
  assert.doesNotMatch(guessed[0].notReturnableReason, /guess/);
  assert.equal(readCustomerLines(answer([order(16, { returnable: "no" })])), null);
  const missing = order(16);
  delete missing.returnable;
  assert.equal(readCustomerLines(answer([missing])), null, "the contract makes both fields required");
});

test("a converted order with no live invoice reads with an empty invoice id, blocked; the same gap on a returnable order is unreadable", () => {
  const blocked = order(16, {
    source: "QUOTE", tender: "", sale_type: "", invoice_id: "", returnable: false,
    not_returnable_reason: "no invoice has been raised for this order yet, so there is nothing to refund against",
  });
  const read = readCustomerLines(answer([blocked, order(11)]));
  assert.ok(read !== null, "the empty invoice id of a blocked order does not sink the whole read");
  assert.equal(read[0].invoiceId, "");
  assert.equal(read[0].returnable, false);
  assert.equal(readCustomerLines(answer([order(16, { invoice_id: "" })])), null);
});

test("a unit cost on the wire reads as an integer cents figure on the line; an absent unit cost is null, never a guessed zero", () => {
  const withCost = readCustomerLines(answer([order(16)]));
  assert.ok(withCost !== null);
  assert.equal(withCost[0].lines[0].unitCostCents, 410, "the line fixture carries an integer cents unit cost");
  // A line that omits unit_cost_cents (legacy rows without the field) reads as null so the
  // drawer can refuse its threshold sentence rather than guess at zero.
  const legacy = order(16, { lines: [line(161, { unit_cost_cents: undefined })] });
  const read = readCustomerLines(answer([legacy, order(11)]));
  assert.ok(read !== null);
  assert.equal(read[0].lines[0].unitCostCents, null, "absent unit cost stays null");
  // A non integer or negative unit cost is refused so the threshold comparison cannot read a
  // silently rounded figure.
  const badInt = order(16, { lines: [line(161, { unit_cost_cents: 12.5 })] });
  assert.equal(readCustomerLines(answer([badInt, order(11)])), null, "non integer unit cost refuses the whole read");
  const negative = order(16, { lines: [line(161, { unit_cost_cents: -10 })] });
  assert.equal(readCustomerLines(answer([negative, order(11)])), null, "negative unit cost refuses the whole read");
});

test("a manufacturer defect is one of the four dispositions and works like the other choices once a quantity is picked", () => {
  const draft = new ReturnDraft(readCustomerLines(answer([order(16)])));
  // MANUFACTURER_DEFECT is not a hard refusal like SCRAP: it is one of the four choices.
  const picking = draft.setDisposition(uuid(161), "MANUFACTURER_DEFECT");
  // Disposition on its own is allowed (the test in this file already covers the implicit qty = 1 path
  // for RESTOCK); here the answer is the relaxed path that lets the drawer read it.
  assert.equal(picking.ok, true, "manufacturer defect can be set on an unpicked line");
  assert.equal(draft.dispositionOf(uuid(161)), "MANUFACTURER_DEFECT");
  // Picking the disposition on an unpicked line picks one unit (the same implicit qty the other
  // three dispositions take).
  assert.equal(draft.qtyOf(uuid(161)), 1);
  draft.setQty(uuid(161), "3");
  assert.equal(draft.qtyOf(uuid(161)), 3);
  // Ready when every picked line has a disposition; MANUFACTURER_DEFECT counts.
  assert.equal(draft.ready, true);
  // And it round trips the picked view: the screen lists the line and its disposition.
  const picked = draft.pickedLines();
  assert.equal(picked.length, 1);
  assert.equal(picked[0].disposition, "MANUFACTURER_DEFECT");
});

test("manufacturer defect can mix with the other three on one credit note, every line carries its own disposition", () => {
  const draft = new ReturnDraft(readCustomerLines(answer([order(16), order(11)])));
  draft.setQty(uuid(161), "1");
  draft.setQty(uuid(162), "2");
  draft.setQty(uuid(111), "3");
  draft.setDisposition(uuid(161), "RESTOCK");
  draft.setDisposition(uuid(162), "MANUFACTURER_DEFECT");
  draft.setDisposition(uuid(111), "NO_RETURN");
  assert.equal(draft.ready, true);
  assert.equal(draft.missingDispositions, 0);
  assert.deepEqual(
    draft.pickedLines().map((p) => [p.orderLineId, p.disposition]),
    [
      [uuid(161), "RESTOCK"],
      [uuid(162), "MANUFACTURER_DEFECT"],
      [uuid(111), "NO_RETURN"],
    ],
  );
});

