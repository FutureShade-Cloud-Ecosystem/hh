// The swap's own unit tests (item 10.7, the swap half): the replacement basket (goods going out), the
// bodies the till sends for the preview and the ring, and the server's answers read field by field.
// No money is computed here: the credit, the new order, the leftover and the shortfall are the server's.
import { test } from "node:test";
import assert from "node:assert/strict";
import { ReturnDraft } from "./draft.js";
import { readCustomerLines } from "./lines.js";
import { ReplacementBasket, buildSwapInput, buildSwapPreviewInput, readSwapPreview, readSwapResult } from "./swap.js";

const uuid = (n) => `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`;
const line = (n) => ({
  order_line_id: uuid(n), product_id: uuid(n + 500), sku: `S${n}`, description: `Product ${n}`, qty_sold: 10, qty_returned: 0,
  qty_open: 10, unit_price_cents: 656, line_total_cents: 6560, tax_cents: 853, money_returned_cents: 0, tax_returned_cents: 0,
  shelf_state: "FULFILLED",
});
const order = (n) => ({
  order_id: uuid(n), order_no: `SO-${n}`, branch_id: uuid(9000), branch_name: "Point Loma", created_at: "2026-10-01T10:00:00Z",
  job_label: "", source: "TILL", sale_type: "COLLECT_NOW", tender: "CASH", fulfilment_state: "FULFILLED", invoice_id: uuid(n + 100),
  card_refundable: false, returnable: true, not_returnable_reason: "", lines: [line(n * 10 + 1)],
});
const draftOf = () => {
  const d = new ReturnDraft(readCustomerLines({ customer_id: uuid(1), items: [order(16), order(11)] }));
  d.setQty(uuid(161), "2"); d.setDisposition(uuid(161), "RESTOCK");
  d.setQty(uuid(111), "1"); d.setDisposition(uuid(111), "DAMAGED");
  return d;
};
const KEY = uuid(777);
const mulch = { productId: uuid(900), name: "Black Mulch", soldBy: "CYD" };
const edging = { productId: uuid(901), name: "Aluminum Edging", soldBy: "EA" };

test("the basket adds a product once, steps its quantity, and removes it", () => {
  const b = new ReplacementBasket();
  b.add(mulch); b.add(edging); b.add(mulch);
  assert.deepEqual(b.lines.map((l) => [l.name, l.qty]), [["Black Mulch", 2], ["Aluminum Edging", 1]], "adding again steps the same line");
  assert.deepEqual(b.setQty(uuid(900), "5"), { ok: true });
  assert.equal(b.lines[0].qty, 5);
  assert.equal(b.setQty(uuid(900), "abc").ok, true);
  assert.equal(b.lines.length, 1, "an unreadable or zero quantity takes the line out");
  b.remove(uuid(901));
  assert.equal(b.lines.length, 0);
  assert.equal(b.setQty(uuid(999), "2").ok, false, "a line that is not in the basket");
});

test("the preview body carries the credit's lines and the replacement lines in the product's own unit, no key needed", () => {
  const b = new ReplacementBasket(); b.add(mulch); b.setQty(uuid(900), "3");
  const body = buildSwapPreviewInput({ customerId: uuid(1), draft: draftOf(), basket: b, tender: "CASH" });
  assert.equal(body.customer_id, uuid(1));
  assert.deepEqual(body.lines, [{ product_id: uuid(900), qty_entered: 3, uom_entered: "CYD" }]);
  assert.equal(body.orders.length, 2);
  assert.equal(body.tender, "CASH");
  assert.equal(buildSwapPreviewInput({ customerId: uuid(1), draft: draftOf(), basket: new ReplacementBasket(), tender: "CASH" }), null, "no goods going out asks nothing");
});

test("the ring's body adds one key and the open drawer, and refuses without a drawer or without goods", () => {
  const b = new ReplacementBasket(); b.add(mulch);
  const built = buildSwapInput({ customerId: uuid(1), key: KEY, draft: draftOf(), basket: b, tender: "ACCOUNT", drawerSessionId: uuid(55), paymentToken: "" });
  assert.equal(built.ok, true);
  assert.equal(built.input.idempotency_key, KEY);
  assert.equal(built.input.till_session_id, uuid(55));
  assert.equal(built.input.tender, "ACCOUNT");
  assert.equal(built.input.orders[0].lines[0].disposition, "RESTOCK");
  assert.equal(built.input.orders[1].lines[0].disposition, "DAMAGED");
  const noDrawer = buildSwapInput({ customerId: uuid(1), key: KEY, draft: draftOf(), basket: b, tender: "CASH", drawerSessionId: "", paymentToken: "" });
  assert.equal(noDrawer.ok, false);
  assert.match(noDrawer.words, /drawer/);
  const none = buildSwapInput({ customerId: uuid(1), key: KEY, draft: draftOf(), basket: new ReplacementBasket(), tender: "CASH", drawerSessionId: uuid(55), paymentToken: "" });
  assert.equal(none.ok, false);
  assert.match(none.words, /replacement/);
});

test("a card shortfall carries its token; the card tender without one is refused here", () => {
  const b = new ReplacementBasket(); b.add(mulch);
  const withToken = buildSwapInput({ customerId: uuid(1), key: KEY, draft: draftOf(), basket: b, tender: "CARD", drawerSessionId: uuid(55), paymentToken: "tok" });
  assert.equal(withToken.input.payment_token, "tok");
  const without = buildSwapInput({ customerId: uuid(1), key: KEY, draft: draftOf(), basket: b, tender: "CARD", drawerSessionId: uuid(55), paymentToken: "" });
  assert.equal(without.ok, false);
});

const credit = () => ({
  customer_id: uuid(1), branch_id: uuid(2), subtotal_cents: 900, tax_cents: 100, total_cents: 1000,
  orders: [{ order_id: uuid(16), order_no: "SO-16", subtotal_cents: 900, tax_cents: 100, total_cents: 1000, lines: [
    { order_line_id: uuid(161), qty_base: 1, subtotal_cents: 900, tax_cents: 100, total_cents: 1000 }] }],
});
const preview = (over = {}) => ({
  credit: credit(), new_order: { subtotal_cents: 800, tax_cents: 104, total_cents: 904 },
  applied_cents: 904, leftover_cents: 96, shortfall_cents: 0, leftover_method: "CARD", ...over,
});

test("a swap preview reads whole: the credit total, the new order, applied, leftover, shortfall and the rail", () => {
  const read = readSwapPreview(preview());
  assert.equal(read.creditCents, 1000);
  assert.equal(read.newOrderCents, 904);
  assert.equal(read.newOrderTaxCents, 104);
  assert.equal(read.appliedCents, 904);
  assert.equal(read.leftoverCents, 96);
  assert.equal(read.shortfallCents, 0);
  assert.equal(read.leftoverMethod, "CARD");
});

test("a swap preview with a missing figure, a bad rail or a credit that is not readable is unreadable", () => {
  assert.equal(readSwapPreview(null), null);
  assert.equal(readSwapPreview(preview({ leftover_cents: "96" })), null);
  assert.equal(readSwapPreview(preview({ leftover_method: "BARTER" })), null);
  assert.equal(readSwapPreview(preview({ credit: { total_cents: 1000 } })), null);
  const bad = preview(); delete bad.new_order;
  assert.equal(readSwapPreview(bad), null);
});

const note = () => ({
  swap_id: uuid(5), note_id: uuid(1), note_no: "CN-1", customer_id: uuid(2), branch_id: uuid(3), refund_method: "CARD",
  subtotal_cents: 900, tax_cents: 100, total_cents: 1000, replayed: false,
  returns: [{ return_id: uuid(10), return_no: "R-10", order_id: uuid(16), invoice_id: uuid(12), refund_method: "CARD", subtotal_cents: 900,
    tax_cents: 100, total_cents: 1000, applied_cents: 904, pickup_status: "NONE", status: "COMMITTED", replayed: false }],
});
const sale = () => ({
  swap_id: uuid(5), order_id: uuid(30), order_no: "SO-30", invoice_id: uuid(31), invoice_no: "INV-30", tender: "CASH", sale_type: "COLLECT_NOW",
  subtotal_cents: 800, tax_cents: 104, total_cents: 904, tax_reason_code: "STD_ON_HST_13", status: "COMMITTED", replayed: false,
});
const result = (over = {}) => ({
  swap_id: uuid(5), note: note(), sale: sale(), credit_cents: 1000, applied_cents: 904, leftover_cents: 96, shortfall_cents: 0,
  leftover_method: "CARD", replayed: false, ...over,
});

test("a committed swap reads whole: both documents carry the swap id and each its own number", () => {
  const read = readSwapResult(result());
  assert.equal(read.swapId, uuid(5));
  assert.equal(read.note.noteNo, "CN-1");
  assert.equal(read.note.swapId, uuid(5));
  assert.equal(read.sale.orderNo, "SO-30");
  assert.equal(read.sale.invoiceNo, "INV-30");
  assert.equal(read.sale.swapId, uuid(5));
  assert.equal(read.sale.totalCents, 904);
  assert.equal(read.leftoverCents, 96);
  assert.equal(read.replayed, false);
});

test("a swap whose two documents do not name the same swap, or that is not committed, is unreadable", () => {
  assert.equal(readSwapResult(result({ sale: { ...sale(), swap_id: uuid(6) } })), null);
  assert.equal(readSwapResult(result({ note: { ...note(), swap_id: undefined } })), null);
  assert.equal(readSwapResult(result({ sale: { ...sale(), status: "VOIDED" } })), null);
  assert.equal(readSwapResult(result({ applied_cents: 1.5 })), null);
  assert.equal(readSwapResult(null), null);
});
