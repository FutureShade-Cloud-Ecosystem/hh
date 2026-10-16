// The Sales stock lookup's plain logic: who holds the read only seat, the words
// the list and the detail draw, the chip each row answers to, and the coming-in
// rows built off the purchasing read. The figures are the server's, said; the
// list never draws cost, whatever the answer carries.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  LOOKUP_CHIPS,
  availabilityWords,
  chipGrants,
  comingInRows,
  listPriceWords,
  soldByWords,
  stockLookupSeat,
} from "../src/lookup/lookup-logic.ts";

test("the two sales seats hold the read only stock lookup, and no other role does", () => {
  assert.equal(stockLookupSeat("inside_sales"), true);
  assert.equal(stockLookupSeat("outside_sales"), true);
  for (const role of ["procurement_manager", "yard_manager", "payables_receivables", "branch_manager", "general_manager", "sales_manager", "yard_team", "financial_controller", "super_admin", ""]) {
    assert.equal(stockLookupSeat(role), false, role);
  }
});

test("the chips are All, In stock and Low, in the board's order", () => {
  assert.deepEqual(LOOKUP_CHIPS.map((c) => c.label), ["All", "In stock", "Low"]);
});

test("a row is in stock when the roll-up's available is above zero, low when a branch is out, out of both chips when nothing is available", () => {
  const stocked = { available_base: 340, out_at_branches: [] };
  const low = { available_base: 340, out_at_branches: ["Julian"] };
  const out = { available_base: 0, out_at_branches: ["Julian"] };
  const silent = { available_base: 0 };
  for (const [chip, row, want] of [
    ["all", stocked, true], ["all", out, true],
    ["in-stock", stocked, true], ["in-stock", low, true], ["in-stock", out, false], ["in-stock", silent, false],
    ["low", low, true], ["low", stocked, false], ["low", out, false], ["low", silent, false],
  ]) {
    assert.equal(chipGrants(chip, row), want, `${chip} on ${JSON.stringify(row)}`);
  }
});

test("the availability line is the total with its unit in green words, or the branches it is out at", () => {
  assert.equal(availabilityWords({ available_base: 340, sold_by: "SF" }), "340 SF available");
  assert.equal(availabilityWords({ available_base: 0, out_at_branches: ["Julian"] }), "Out at Julian");
  assert.equal(availabilityWords({ available_base: -6, out_at_branches: ["Julian", "Point Loma"] }), "Out at Julian and Point Loma");
  // No stock record anywhere: out, with no branch to name.
  assert.equal(availabilityWords({ available_base: 0 }), "Out of stock");
  // No figures yet (the answer has not landed): no words at all.
  assert.equal(availabilityWords(undefined), "");
});

test("sold by reads as the trade's words, the code itself when none is known", () => {
  assert.equal(soldByWords("SF"), "the square foot");
  assert.equal(soldByWords("PC"), "the piece");
  assert.equal(soldByWords("EA"), "each");
  assert.equal(soldByWords("T"), "the tonne");
  assert.equal(soldByWords("BG"), "the bag");
  assert.equal(soldByWords("BX"), "BX");
  assert.equal(soldByWords(""), "");
});

test("the list price line is the money and the word list, absent when the product carries none", () => {
  assert.equal(listPriceWords(400), "$4.00 list");
  assert.equal(listPriceWords(8249), "$82.49 list");
  assert.equal(listPriceWords(undefined), "");
});

test("the coming-in rows carry the order and the vendor, the branch's name, the quantity in its unit and the expected words", () => {
  const branches = new Map([["b-plm", "Point Loma"], ["b-jul", "Julian"]]);
  const rows = comingInRows(
    [
      { po_no: "PO-PLM-136", vendor_name: "Copperline Pavers (demo)", branch_id: "b-plm", expected_at: "2026-10-20T00:00:00Z", qty_outstanding_base: 240, uom: "SF", status: "ORDERED" },
      { po_no: "PO-JUL-0031", vendor_name: "Granite Run Quarry (demo)", branch_id: "b-jul", expected_at: "", qty_outstanding_base: 4, uom: "T", status: "DRAFT" },
    ],
    branches,
    "2026-10-16",
  );
  assert.equal(rows.length, 2);
  assert.equal(rows[0].order, "PO-PLM-136 · Copperline Pavers (demo)");
  assert.equal(rows[0].branch, "Point Loma");
  assert.equal(rows[0].qty, "240 SF");
  assert.match(rows[0].expected, /^in \d+ days$/);
  assert.equal(rows[1].branch, "Julian");
  assert.equal(rows[1].expected, "");
  assert.deepEqual(comingInRows([], branches, "2026-10-16"), []);
});
