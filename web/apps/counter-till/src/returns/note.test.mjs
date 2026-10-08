// The credit note's own unit tests (item 10.7): the body the till sends (one key, lines grouped
// by order, the rail, the whole-note restock flag the contract carries), and the committed note
// read field by field. No money is computed here: every figure read is the server's.
import { test } from "node:test";
import assert from "node:assert/strict";
import { ReturnDraft } from "./draft.js";
import { readCustomerLines } from "./lines.js";
import { buildNoteInput, readNote, railAvailable } from "./note.js";

const uuid = (n) => `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`;
const line = (n, over = {}) => ({
  order_line_id: uuid(n), product_id: uuid(n + 500), sku: `S${n}`, description: `Product ${n}`,
  qty_sold: 10, qty_returned: 0, qty_open: 10, unit_price_cents: 656, line_total_cents: 6560, tax_cents: 853,
  money_returned_cents: 0, tax_returned_cents: 0, shelf_state: "FULFILLED", ...over,
});
const order = (n, over = {}) => ({
  order_id: uuid(n), order_no: `SO-${n}`, branch_id: uuid(9000), created_at: "2026-10-01T10:00:00Z", job_label: "",
  source: "TILL", sale_type: "COLLECT_NOW", tender: "CARD", fulfilment_state: "FULFILLED", invoice_id: uuid(n + 100),
  card_refundable: true, lines: [line(n * 10 + 1), line(n * 10 + 2)], ...over,
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
      { order_id: uuid(16), lines: [{ order_line_id: uuid(161), qty_base: 2, reason_code: "OTHER" }] },
      { order_id: uuid(11), lines: [{ order_line_id: uuid(111), qty_base: 1, reason_code: "OTHER" }] },
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

test("damaged and nothing returned send restock false, and damaged names its reason", () => {
  const d = draftOf([order(16)]);
  d.setQty(uuid(161), "1"); d.setDisposition(uuid(161), "DAMAGED");
  d.setQty(uuid(162), "1"); d.setDisposition(uuid(162), "NO_RETURN");
  const built = buildNoteInput({ customerId: uuid(1), key: KEY, draft: d, method: "ACCOUNT_CREDIT", drawerSessionId: "" });
  assert.equal(built.input.restock, false);
  assert.deepEqual(built.input.orders[0].lines.map((l) => l.reason_code), ["DAMAGED", "OTHER"]);
});

test("a mix of back to stock with damaged or nothing returned is refused before the wire, in words", () => {
  const d = draftOf([order(16)]);
  d.setQty(uuid(161), "1"); d.setDisposition(uuid(161), "RESTOCK");
  d.setQty(uuid(162), "1"); d.setDisposition(uuid(162), "DAMAGED");
  const built = buildNoteInput({ customerId: uuid(1), key: KEY, draft: d, method: "CARD", drawerSessionId: "" });
  assert.equal(built.ok, false);
  assert.match(built.words, /one choice for the whole note/);
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
