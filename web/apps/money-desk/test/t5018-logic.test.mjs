// The T5018 view's own vocabulary: the words the report wears, the words a contractor with no
// business number wears, and the file's name. The money is the server's: the view reads the
// figures the route sends and never adds them up (the no-app-money gate reads this folder too).
import { test } from "node:test";
import assert from "node:assert/strict";
import { businessNumberWords, t5018EmptyKind, t5018Filename, t5018Rows, t5018VendorsWord } from "../src/ar/t5018-logic.ts";

const ROW = {
  vendor_id: "11111111-1111-4111-8111-111111111111",
  vendor_no: "VM-0001",
  name: "Marked (demo)",
  business_number: "123456789",
  business_number_missing: false,
  paid_cents: 125000,
};

test("the rows the view shows are the route's rows, in the route's order", () => {
  const report = { year: 2026, items: [ROW, { ...ROW, vendor_id: "22222222-2222-4222-8222-222222222222" }], total_cents: 250000 };
  assert.deepEqual(t5018Rows(report), report.items);
});

test("a contractor with no business number says so rather than showing a blank", () => {
  assert.equal(businessNumberWords({ ...ROW, business_number: "", business_number_missing: true }), "(missing)");
  assert.equal(businessNumberWords(ROW), "123456789");
});

test("the count of marked vendors is said the way a person says it", () => {
  assert.equal(t5018VendorsWord(0), "No vendor is marked as a subcontractor");
  assert.equal(t5018VendorsWord(1), "1 vendor is marked as a subcontractor");
  assert.equal(t5018VendorsWord(4), "4 vendors are marked as subcontractors");
});

test("the file is named for the year it covers", () => {
  assert.equal(t5018Filename(2026), "t5018-2026.csv");
});

test("an empty year says which of its two empties it is", () => {
  assert.equal(t5018EmptyKind({ year: 2026, items: [], total_cents: 0 }), "none-marked");
  assert.equal(t5018EmptyKind({ year: 2026, items: [{ ...ROW, paid_cents: 0 }], total_cents: 0 }), "none-paid");
  assert.equal(t5018EmptyKind({ year: 2026, items: [ROW], total_cents: 125000 }), null);
});
