// The credit note's own unit tests (item 10.7): the body the till sends (one key, lines grouped
// by order, the rail, the whole-note restock flag the contract carries), and the committed note
// read field by field. No money is computed here: every figure read is the server's.
import { test } from "node:test";
import assert from "node:assert/strict";
import { ReturnDraft } from "./draft.js";
import { readCustomerLines } from "./lines.js";
import { buildNoteInput, buildPreviewInput, readNote, readPreview, railAvailable, WALK_IN_CUSTOMER_ID } from "./note.js";

const uuid = (n) => `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`;
const line = (n, over = {}) => ({
  order_line_id: uuid(n), product_id: uuid(n + 500), sku: `S${n}`, description: `Product ${n}`,
  qty_sold: 10, qty_returned: 0, qty_open: 10, unit_price_cents: 656, line_total_cents: 6560, tax_cents: 853,
  money_returned_cents: 0, tax_returned_cents: 0, shelf_state: "FULFILLED", ...over,
});
const order = (n, over = {}) => ({
  order_id: uuid(n), order_no: `SO-${n}`, branch_id: uuid(9000), branch_name: "Trenton", created_at: "2026-10-01T10:00:00Z", job_label: "",
  source: "TILL", sale_type: "COLLECT_NOW", tender: "CARD", fulfilment_state: "FULFILLED", invoice_id: uuid(n + 100),
  card_refundable: true, returnable: true, not_returnable_reason: "", lines: [line(n * 10 + 1), line(n * 10 + 2)], ...over,
});
const draftOf = (orders) => new ReturnDraft(readCustomerLines({ customer_id: uuid(1), items: orders }));
const KEY = uuid(777);

function twoOrders() {
  const d = draftOf([order(16), order(11)]);
  d.setQty(uuid(161), "2"); d.setDisposition(uuid(161), "RESTOCK");
  d.setQty(uuid(111), "1"); d.setDisposition(uuid(111), "RESTOCK");
  return d;
}

test("the body groups the picked lines by order, one key, one rail, and restock when every line goes back to stock", () => {
  const built = buildNoteInput({ customerId: uuid(1), key: KEY, draft: twoOrders(), method: "CARD", drawerSessionId: "" });
  assert.equal(built.ok, true);
  assert.deepEqual(built.input, {
    idempotency_key: KEY, customer_id: uuid(1), refund_method: "CARD", reason: "", restock: true, till_session_id: "",
    orders: [
      { order_id: uuid(16), lines: [{ order_line_id: uuid(161), qty_base: 2, reason_code: "OTHER", disposition: "RESTOCK" }] },
      { order_id: uuid(11), lines: [{ order_line_id: uuid(111), qty_base: 1, reason_code: "OTHER", disposition: "RESTOCK" }] },
    ],
  });
});

test("two lines of one order share one order entry", () => {
  const d = draftOf([order(16)]);
  d.setQty(uuid(161), "1"); d.setDisposition(uuid(161), "RESTOCK");
  d.setQty(uuid(162), "3"); d.setDisposition(uuid(162), "RESTOCK");
  const built = buildNoteInput({ customerId: uuid(1), key: KEY, draft: d, method: "ACCOUNT_CREDIT", drawerSessionId: "" });
  assert.equal(built.input.orders.length, 1);
  assert.equal(built.input.orders[0].lines.length, 2);
});

test("every line sends its own disposition, and a mix is sent as it is, never refused or flattened", () => {
  const d = draftOf([order(16)]);
  d.setQty(uuid(161), "1"); d.setDisposition(uuid(161), "RESTOCK");
  d.setQty(uuid(162), "1"); d.setDisposition(uuid(162), "DAMAGED");
  const built = buildNoteInput({ customerId: uuid(1), key: KEY, draft: d, method: "ACCOUNT_CREDIT", drawerSessionId: "" });
  assert.equal(built.ok, true);
  assert.deepEqual(built.input.orders[0].lines.map((l) => l.disposition), ["RESTOCK", "DAMAGED"]);
  assert.deepEqual(built.input.orders[0].lines.map((l) => l.reason_code), ["OTHER", "DAMAGED"]);
  const none = draftOf([order(16)]);
  none.setQty(uuid(161), "1"); none.setDisposition(uuid(161), "NO_RETURN");
  const sent = buildNoteInput({ customerId: uuid(1), key: KEY, draft: none, method: "ACCOUNT_CREDIT", drawerSessionId: "" });
  assert.equal(sent.input.orders[0].lines[0].disposition, "NO_RETURN");
  assert.equal(sent.input.restock, false, "the legacy flag is only the all-restock summary");
});

test("cash carries the open drawer and refuses without one; other rails send no drawer", () => {
  const cash = buildNoteInput({ customerId: uuid(1), key: KEY, draft: twoOrders(), method: "CASH", drawerSessionId: uuid(55) });
  assert.equal(cash.input.till_session_id, uuid(55));
  const noDrawer = buildNoteInput({ customerId: uuid(1), key: KEY, draft: twoOrders(), method: "CASH", drawerSessionId: "" });
  assert.equal(noDrawer.ok, false);
  assert.match(noDrawer.words, /drawer/);
  const card = buildNoteInput({ customerId: uuid(1), key: KEY, draft: twoOrders(), method: "CARD", drawerSessionId: uuid(55) });
  assert.equal(card.input.till_session_id, "");
});

test("a draft that is not ready builds nothing", () => {
  const d = draftOf([order(16)]);
  assert.equal(buildNoteInput({ customerId: uuid(1), key: KEY, draft: d, method: "CARD", drawerSessionId: "" }).ok, false);
  d.setQty(uuid(161), "1");
  assert.equal(buildNoteInput({ customerId: uuid(1), key: KEY, draft: d, method: "CARD", drawerSessionId: "" }).ok, false);
});

test("rails: card needs every picked order's capture to be refundable, cash needs the drawer, the account rail is the server's call", () => {
  const d = draftOf([order(16), order(11, { card_refundable: false })]);
  d.setQty(uuid(161), "1"); d.setDisposition(uuid(161), "RESTOCK");
  assert.equal(railAvailable(d, "CARD", { kind: "unknown" }), true);
  d.setQty(uuid(111), "1"); d.setDisposition(uuid(111), "RESTOCK");
  assert.equal(railAvailable(d, "CARD", { kind: "unknown" }), false);
  assert.equal(railAvailable(d, "CASH", { kind: "closed" }), false);
  assert.equal(railAvailable(d, "CASH", { kind: "open", sessionId: uuid(55) }), true);
  assert.equal(railAvailable(d, "ACCOUNT_CREDIT", { kind: "closed" }), true);
});

const child = (n, over = {}) => ({
  return_id: uuid(n), return_no: `R-${n}`, order_id: uuid(n + 1), invoice_id: uuid(n + 2), refund_method: "CARD",
  subtotal_cents: 1000, tax_cents: 130, total_cents: 1130, pickup_status: "NONE", status: "COMMITTED", replayed: false, ...over,
});
const note = (over = {}) => ({
  note_id: uuid(1), note_no: "CN-1", customer_id: uuid(2), branch_id: uuid(3), refund_method: "CARD",
  subtotal_cents: 2000, tax_cents: 260, total_cents: 2260, returns: [child(10), child(20)], replayed: false, ...over,
});

test("a committed note reads whole, each child with its own number and the server's figures", () => {
  const read = readNote(note());
  assert.equal(read.noteNo, "CN-1");
  assert.equal(read.totalCents, 2260);
  assert.deepEqual(read.returns.map((r) => [r.returnNo, r.totalCents]), [["R-10", 1130], ["R-20", 1130]]);
  assert.equal(read.replayed, false);
});

test("a note with a missing field, no children, or a child that is not committed is unreadable", () => {
  assert.equal(readNote(null), null);
  assert.equal(readNote(note({ returns: [] })), null);
  assert.equal(readNote(note({ total_cents: "2260" })), null);
  assert.equal(readNote(note({ note_no: "" })), null);
  assert.equal(readNote(note({ returns: [child(10, { status: "PENDING" })] })), null);
  assert.equal(readNote(note({ refund_method: "BARTER" })), null);
});

test("the preview body carries no key, rail or drawer, and sends a disposition only once one is chosen", () => {
  const d = draftOf([order(16), order(11)]);
  d.setQty(uuid(161), "2");
  d.setQty(uuid(111), "1"); d.setDisposition(uuid(111), "DAMAGED");
  const body = buildPreviewInput(uuid(1), d);
  assert.deepEqual(body, {
    customer_id: uuid(1),
    orders: [
      { order_id: uuid(16), lines: [{ order_line_id: uuid(161), qty_base: 2, reason_code: "OTHER" }] },
      { order_id: uuid(11), lines: [{ order_line_id: uuid(111), qty_base: 1, reason_code: "DAMAGED", disposition: "DAMAGED" }] },
    ],
  });
  assert.equal(buildPreviewInput(uuid(1), draftOf([order(16)])), null, "nothing picked asks nothing");
});

const pline = (n, t) => ({ order_line_id: uuid(n), qty_base: 1, subtotal_cents: t - 10, tax_cents: 10, total_cents: t });
const preview = (over = {}) => ({
  customer_id: uuid(1), branch_id: uuid(2), subtotal_cents: 1980, tax_cents: 20, total_cents: 2000,
  orders: [{ order_id: uuid(16), order_no: "SO-16", subtotal_cents: 990, tax_cents: 10, total_cents: 1000, lines: [pline(161, 1000)] },
           { order_id: uuid(11), order_no: "SO-11", subtotal_cents: 990, tax_cents: 10, total_cents: 1000, lines: [pline(111, 1000)] }],
  ...over,
});

test("a preview reads whole: the note total with tax, and each line's own figure by line id", () => {
  const read = readPreview(preview());
  assert.equal(read.totalCents, 2000);
  assert.equal(read.taxCents, 20);
  assert.equal(read.lines.get(uuid(161)).totalCents, 1000);
  assert.equal(read.lines.get(uuid(111)).taxCents, 10);
});

test("a preview with a missing or unsafe figure is unreadable, so no total is shown rather than half of one", () => {
  assert.equal(readPreview(null), null);
  assert.equal(readPreview(preview({ total_cents: 20.5 })), null);
  assert.equal(readPreview(preview({ orders: [] })), null);
  const bad = preview();
  delete bad.orders[0].lines[0].tax_cents;
  assert.equal(readPreview(bad), null);
});

// ---- the rulings round: the cash or card split, and the walk in customer --------------------------

test("the preview body names the rail once one is chosen, so the server can split an order not fully paid", () => {
  const d = draftOf([order(16)]);
  d.setQty(uuid(161), "1");
  assert.equal(buildPreviewInput(uuid(1), d).refund_method, undefined, "no rail chosen, none sent");
  assert.equal(buildPreviewInput(uuid(1), d, "CASH").refund_method, "CASH");
  assert.equal(buildPreviewInput(uuid(1), d, "ACCOUNT_CREDIT").refund_method, "ACCOUNT_CREDIT");
});

test("a preview with a rail reads each order's cash or card part, the part off the open invoice and the server's words", () => {
  const split = preview({
    rail_cents: 500, invoice_offset_cents: 1500,
    orders: [
      { order_id: uuid(16), order_no: "SO-16", subtotal_cents: 990, tax_cents: 10, total_cents: 1000, rail_cents: 0, invoice_offset_cents: 1000,
        rail_note: "This order is not paid yet: nothing can go back in cash or card; the whole $10.00 comes off the open invoice",
        lines: [pline(161, 1000)] },
      { order_id: uuid(11), order_no: "SO-11", subtotal_cents: 990, tax_cents: 10, total_cents: 1000, rail_cents: 500, invoice_offset_cents: 500,
        rail_note: "This order is not fully paid: up to $5.00 can go back in cash or card; the rest, $5.00, comes off the open invoice",
        lines: [pline(111, 1000)] },
    ],
  });
  const read = readPreview(split);
  assert.equal(read.railCents, 500);
  assert.equal(read.invoiceOffsetCents, 1500);
  assert.equal(read.orders.length, 2);
  assert.deepEqual(read.orders.map((o) => [o.orderNo, o.railCents, o.invoiceOffsetCents]), [["SO-16", 0, 1000], ["SO-11", 500, 500]]);
  assert.match(read.orders[1].railNote, /up to \$5.00 can go back in cash or card/);
});

test("without a rail the preview carries no split, and a half stated split is unreadable", () => {
  const plain = readPreview(preview());
  assert.equal(plain.railCents, null);
  assert.equal(plain.orders[0].railNote, "");
  const half = preview();
  half.orders[0].rail_cents = 100;
  assert.equal(readPreview(half), null, "a rail figure without its offset is not a whole answer");
});

test("a committed return reads its server words and what came off the invoice when the order was not fully paid", () => {
  const read = readNote(note({ returns: [child(10, { invoice_offset_cents: 300, payment_note: "This order is not fully paid: up to $8.30 can go back in cash or card; the rest, $3.00, comes off the open invoice" }), child(20)] }));
  assert.equal(read.returns[0].invoiceOffsetCents, 300);
  assert.match(read.returns[0].paymentNote, /not fully paid/);
  assert.equal(read.returns[1].invoiceOffsetCents, 0);
  assert.equal(read.returns[1].paymentNote, "");
});

test("the account rail is never offered for the walk in customer, and the others follow the draft", () => {
  const d = draftOf([order(16)]);
  d.setQty(uuid(161), "1");
  assert.equal(railAvailable(d, "ACCOUNT_CREDIT", { kind: "open", sessionId: uuid(55) }, true), false);
  assert.equal(railAvailable(d, "ACCOUNT_CREDIT", { kind: "open", sessionId: uuid(55) }, false), true);
  assert.equal(railAvailable(d, "CASH", { kind: "open", sessionId: uuid(55) }, true), true);
  assert.equal(railAvailable(d, "CARD", { kind: "unknown" }, true), true);
  assert.equal(typeof WALK_IN_CUSTOMER_ID, "string");
  assert.match(WALK_IN_CUSTOMER_ID, /^[0-9a-f-]{36}$/);
});
