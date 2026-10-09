// The procurement slice's plain logic: the words for what the server answered,
// who may do what the role may not, the shapes the core really writes, and the
// quantity and money text. Nothing here computes money; the server decides.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  deliveryWord,
  incomingLines,
  isOpen,
  poRows,
  refusalWords,
  shortfallRows,
  statusTone,
  statusWord,
  validateExpectedDate,
} from "../src/po/po-logic.ts";
import { formatMoney, formatQty } from "../src/po/po-money.ts";

test("the shortfall is read from the key the core writes, and from the contract's key too", () => {
  const row = { sku: "A" };
  assert.deepEqual(shortfallRows({ shortfall: [row], on_order_degraded: false }), [row]);
  assert.deepEqual(shortfallRows({ items: [row], on_order_degraded: false }), [row]);
  assert.deepEqual(shortfallRows({ shortfall: null, on_order_degraded: false }), []);
  assert.deepEqual(shortfallRows({}), []);
});

test("an empty PO list the core answers as null reads as no orders", () => {
  assert.deepEqual(poRows({ items: null }), []);
  assert.deepEqual(poRows({ items: [{ id: "1" }] }), [{ id: "1" }]);
});

test("incoming lines are read from the key the core writes, and from the contract's key too", () => {
  assert.deepEqual(incomingLines({ lines: [{ po_no: "X" }] }), [{ po_no: "X" }]);
  assert.deepEqual(incomingLines({ items: [{ po_no: "Y" }] }), [{ po_no: "Y" }]);
  assert.deepEqual(incomingLines({}), []);
});

test("order states are words, and RECEIVED, PARTIAL and ORDERED are never buttons", () => {
  assert.equal(statusWord("ORDERED"), "Ordered");
  assert.equal(statusWord("PARTIAL"), "Part received");
  assert.equal(statusWord("RECEIVED"), "Received");
  assert.equal(statusWord("DRAFT"), "Draft");
  assert.equal(statusTone("RECEIVED"), "good");
  assert.equal(statusTone("PARTIAL"), "watch");
  assert.equal(isOpen("DRAFT"), true);
  assert.equal(isOpen("ORDERED"), true);
  assert.equal(isOpen("PARTIAL"), true);
  assert.equal(isOpen("RECEIVED"), false);
  assert.equal(isOpen("CANCELLED"), false);
});

test("delivery words follow the contract's three values", () => {
  assert.equal(deliveryWord("DELIVERY"), "Vendor truck");
  assert.equal(deliveryWord("PICKUP"), "Pickup");
  assert.equal(deliveryWord("UNKNOWN"), "Not set");
  assert.equal(deliveryWord(undefined), "Not set");
});

test("the expected date must be a real date", () => {
  assert.deepEqual(validateExpectedDate(""), { ok: false, words: "Pick the date the vendor promised." });
  assert.equal(validateExpectedDate("2031-02-30").ok, false);
  assert.equal(validateExpectedDate("31/02/2031").ok, false);
  assert.deepEqual(validateExpectedDate("2031-03-04"), { ok: true, date: "2031-03-04" });
});

test("a 403 names who may do the move; the server's own sentence is otherwise kept", () => {
  assert.match(refusalWords("order", "Forbidden", 403), /Procurement Manager/);
  assert.match(refusalWords("order", "Forbidden", 403), /read what is on order/);
  const bill = refusalWords("post-bill", "Forbidden", 403);
  assert.match(bill, /Payables and Receivables/);
  assert.match(bill, /does not post them/);
  assert.equal(refusalWords("order", "cannot order an empty purchase order, add at least one line", 409), "Cannot order an empty purchase order, add at least one line.");
  assert.equal(refusalWords("order", "x", undefined), "The server could not be reached. Nothing was changed. Try again.");
  assert.equal(refusalWords("order", "x", 401), "Your sign in has ended. Sign in again.");
  assert.equal(refusalWords("order", "boom", 500), "The server hit a problem. Nothing was changed. Try again.");
});

test("money reads as dollars with cents, and a missing figure is a placeholder, never a zero", () => {
  assert.equal(formatMoney(201600), "$2,016.00");
  assert.equal(formatMoney(undefined), "(no figure)");
  assert.equal(formatMoney(null), "(no figure)");
  assert.equal(formatMoney(12.5), "(no figure)");
});

test("quantities read plainly, with the unit unless it is a piece", () => {
  assert.equal(formatQty(1200, "pc"), "1,200");
  assert.equal(formatQty(40, "T"), "40 T");
  assert.equal(formatQty(-40, "pc"), "-40");
  assert.equal(formatQty(0.5, "T"), "0.5 T");
  assert.equal(formatQty(undefined, "T"), "(unknown)");
});
