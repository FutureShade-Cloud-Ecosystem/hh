// The procurement slice's plain logic: the words for what the server answered,
// who may do what the role may not, the shapes the core really writes, and the
// quantity and money text. Nothing here computes money; the server decides.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  deliveryWord,
  dateWords,
  draftOf,
  landedWords,
  relativeDay,
  incomingLines,
  isOpen,
  poRows,
  refusalWords,
  shortfallRows,
  statusTone,
  statusWord,
  validateExpectedDate,
} from "../src/po/po-logic.ts";
import { costCentsFromTyped, formatMoney, formatQty } from "../src/po/po-money.ts";

test("the shortfall is read from the contract's key, the one key the core writes", () => {
  const row = { sku: "A" };
  assert.deepEqual(shortfallRows({ items: [row], on_order_degraded: false }), [row]);
  assert.deepEqual(shortfallRows({ items: null, on_order_degraded: false }), []);
  assert.deepEqual(shortfallRows({}), []);
  // The ERP's old key is gone from the core (the contract won): a page that still carries it is not read.
  assert.deepEqual(shortfallRows({ shortfall: [row], on_order_degraded: false }), []);
});

test("an empty PO list the core answers as null reads as no orders", () => {
  assert.deepEqual(poRows({ items: null }), []);
  assert.deepEqual(poRows({ items: [{ id: "1" }] }), [{ id: "1" }]);
});

test("incoming lines are read from the contract's key, the one key the core writes", () => {
  assert.deepEqual(incomingLines({ items: [{ po_no: "Y" }] }), [{ po_no: "Y" }]);
  assert.deepEqual(incomingLines({}), []);
  // The ERP's old key is gone from the core (the contract won): a page that still carries it is not read.
  assert.deepEqual(incomingLines({ lines: [{ po_no: "X" }] }), []);
});

test("order states are words, and RECEIVED, PARTIAL and ORDERED are never buttons", () => {
  assert.equal(statusWord("ORDERED"), "Ordered");
  assert.equal(statusWord("PARTIAL"), "Partial");
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
  const wall = refusalWords("order", "Forbidden", 403);
  assert.equal(
    wall,
    "Drafting, placing, editing and cancelling a purchase order is for the Procurement Manager, the General Manager and the super admin only. The Yard Manager receives goods only; other roles can read.",
  );
  const bill = refusalWords("post-bill", "Forbidden", 403);
  assert.match(bill, /Payables and Receivables/);
  assert.match(bill, /does not post them/);
  assert.equal(refusalWords("order", "cannot order an empty purchase order, add at least one line", 409), "Cannot order an empty purchase order, add at least one line.");
  assert.equal(refusalWords("order", "x", undefined), "The server could not be reached. Nothing was changed. Try again.");
  assert.equal(refusalWords("order", "x", 401), "Your sign in has ended. Sign in again.");
  assert.equal(refusalWords("order", "boom", 500), "The server hit a problem. Nothing was changed. Try again.");
});

test("the branch fence's 403 reads in the server's own words, not the role wall's", () => {
  // The core's fenceNamedBranch: a named branch outside the caller's reach is refused in words
  // (reorder_actions.go), unlike the role gate's wordless Forbidden, which the wall words explain.
  assert.equal(refusalWords("order", "you do not have access to that branch", 403), "You do not have access to that branch.");
  assert.equal(refusalWords("load", "you do not have access to that branch", 403), "You do not have access to that branch.");
});

test("money reads as dollars with cents, and a missing figure is a placeholder, never a zero", () => {
  assert.equal(formatMoney(201600), "$2,016.00");
  assert.equal(formatMoney(undefined), "(no figure)");
  assert.equal(formatMoney(null), "(no figure)");
  assert.equal(formatMoney(12.5), "(no figure)");
});

test("typed dollars become cents exactly through the decimal string, never a float multiply", () => {
  // 1.005 is the float edge: Number("1.005") * 100 is 100.49999..., which a Math.round reads as
  // 100 cents, a price the buyer never typed. The split refuses a third decimal in words instead.
  assert.deepEqual(costCentsFromTyped("1.005"), { ok: false, words: "Write the unit cost as dollars and cents, two decimals at most." });
  assert.deepEqual(costCentsFromTyped("0.1"), { ok: true, cents: 10 });
  assert.deepEqual(costCentsFromTyped("19.99"), { ok: true, cents: 1999 });
  assert.deepEqual(costCentsFromTyped("8"), { ok: true, cents: 800 });
  assert.deepEqual(costCentsFromTyped("8.4"), { ok: true, cents: 840 });
  assert.deepEqual(costCentsFromTyped("8.40"), { ok: true, cents: 840 });
  assert.deepEqual(costCentsFromTyped("0.05"), { ok: true, cents: 5 });
  // A negative passes through as negative cents: the core refuses it in its own words, which is
  // the honest answer beside the button, rather than the app inventing a sentence the core lacks.
  assert.deepEqual(costCentsFromTyped("-1.50"), { ok: true, cents: -150 });
  assert.equal(costCentsFromTyped("soon").ok, false);
  assert.equal(costCentsFromTyped(" 1.005 ").ok, false);
});

test("quantities read plainly, with the unit unless it is a piece", () => {
  assert.equal(formatQty(1200, "pc"), "1,200");
  assert.equal(formatQty(40, "T"), "40 T");
  assert.equal(formatQty(-40, "pc"), "-40");
  assert.equal(formatQty(0.5, "T"), "0.5 T");
  assert.equal(formatQty(undefined, "T"), "(unknown)");
});

test("a row carries the draft that does not cover it, or none", () => {
  assert.deepEqual(draftOf({ draft_po_id: "d1", draft_po_no: "PO-TRT-142" }), { id: "d1", no: "PO-TRT-142" });
  assert.equal(draftOf({}), null);
  assert.equal(draftOf({ draft_po_id: "" }), null);
});

test("promised days read against today, and far dates are left to the date", () => {
  assert.equal(relativeDay("2031-03-04", "2031-03-02"), "in 2 days");
  assert.equal(relativeDay("2031-03-03", "2031-03-02"), "tomorrow");
  assert.equal(relativeDay("2031-03-02", "2031-03-02"), "today");
  assert.equal(relativeDay("2031-03-01", "2031-03-02"), "yesterday");
  assert.equal(relativeDay("2031-02-27", "2031-03-02"), "3 days ago");
  assert.equal(relativeDay("2032-03-04", "2031-03-02"), "");
  assert.equal(relativeDay("soon", "2031-03-02"), "");
});

test("what has landed reads as a count against what was ordered", () => {
  assert.equal(landedWords([{ qty_ordered_base: 10, qty_received_base: 6, uom: "pc" }]), "6 of 10 pc landed");
  assert.equal(landedWords([{ qty_ordered_base: 4, qty_received_base: 1, uom: "pc" }, { qty_ordered_base: 6, qty_received_base: 5, uom: "pc" }]), "6 of 10 pc landed");
  assert.equal(landedWords([{ qty_ordered_base: 4, qty_received_base: 1, uom: "pc" }, { qty_ordered_base: 6, qty_received_base: 5, uom: "T" }]), "part landed");
  assert.equal(landedWords([]), "");
});

test("the promised date reads as words with its days from today", () => {
  assert.equal(dateWords("2031-03-04", "2031-02-27"), "Tuesday 4 March 2031 (in 5 days)");
  assert.equal(dateWords("2031-03-04", "2025-01-01"), "Tuesday 4 March 2031");
  assert.equal(dateWords("2031-02-30", "2031-02-27"), "");
  assert.equal(dateWords("", "2031-02-27"), "");
});
