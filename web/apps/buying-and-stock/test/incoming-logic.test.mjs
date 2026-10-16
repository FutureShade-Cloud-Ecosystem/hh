// The Incoming area's plain logic: one row per line of a placed order that is
// still to arrive, and the soonest promised first. Nothing here computes money;
// every figure is the server's own, carried into the row's shape.
import { test } from "node:test";
import assert from "node:assert/strict";
import { arrivalRows, sortByPromise } from "../src/incoming/incoming-logic.ts";

const ORDER = {
  id: "po-1",
  po_no: "PO-PLM-141",
  vendor_name: "Techo Bloc (demo)",
  status: "ORDERED",
  expected_at: "2031-03-04",
  delivery_method: "DELIVERY",
};

test("a placed order's lines still to arrive are the rows: what is outstanding, with its unit", () => {
  const detail = {
    lines: [
      { id: "l1", description: "Techo Bloc Paver 60mm", qty_ordered_base: 12, qty_received_base: 0, uom: "pc" },
      { id: "l2", description: "Unilock Wall Cap", qty_ordered_base: 10, qty_received_base: 6, uom: "pc" },
    ],
  };
  assert.deepEqual(sortByPromise(arrivalRows(ORDER, detail)), [
    { poId: "po-1", poNo: "PO-PLM-141", vendor: "Techo Bloc (demo)", product: "Techo Bloc Paver 60mm", qtyBase: 12, uom: "pc", expected: "2031-03-04", method: "DELIVERY" },
    { poId: "po-1", poNo: "PO-PLM-141", vendor: "Techo Bloc (demo)", product: "Unilock Wall Cap", qtyBase: 4, uom: "pc", expected: "2031-03-04", method: "DELIVERY" },
  ]);
});

test("a line that has all landed is not coming, and an order with none left draws no row", () => {
  const landed = { lines: [{ id: "l1", description: "Alcli Step 48in", qty_ordered_base: 10, qty_received_base: 10, uom: "pc" }] };
  assert.deepEqual(arrivalRows(ORDER, landed), []);
  assert.deepEqual(arrivalRows(ORDER, { lines: [] }), []);
});

test("an order without a promised date or a way reads as no date and not set, never an invention", () => {
  const bare = { ...ORDER, expected_at: undefined, delivery_method: undefined };
  const rows = arrivalRows(bare, { lines: [{ id: "l1", description: "Paver", qty_ordered_base: 1, qty_received_base: 0, uom: "pc" }] });
  assert.deepEqual(rows, [{ poId: "po-1", poNo: "PO-PLM-141", vendor: "Techo Bloc (demo)", product: "Paver", qtyBase: 1, uom: "pc", expected: "", method: "" }]);
});

test("the list reads soonest promised first, and an order without a date waits at the foot", () => {
  const sorted = sortByPromise([
    { poId: "c", poNo: "PO-3", vendor: "V", product: "P", qtyBase: 1, uom: "pc", expected: "", method: "PICKUP" },
    { poId: "b", poNo: "PO-2", vendor: "V", product: "P", qtyBase: 1, uom: "pc", expected: "2031-03-06", method: "PICKUP" },
    { poId: "a", poNo: "PO-1", vendor: "V", product: "P", qtyBase: 1, uom: "pc", expected: "2031-03-04", method: "PICKUP" },
  ]);
  assert.deepEqual(sorted.map((r) => r.poNo), ["PO-1", "PO-2", "PO-3"]);
});
