// The T5018 view's own vocabulary: the words the report wears, the words a contractor with no
// business number wears, and the file's name. The money is the server's: the view reads the
// figures the route sends and never adds them up (the no-app-money gate reads this folder too).
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  businessNumberWords,
  t5018Box22Cents,
  t5018EmptyKind,
  t5018Filename,
  t5018Rows,
  t5018RuleNote,
  t5018ThresholdWords,
  t5018UnmarkedWords,
  t5018VendorsWord,
} from "../src/ar/t5018-logic.ts";
import { renderT5018 } from "../src/ar/ap-views.ts";

function flat(v) {
  if (v == null || v === false) return "";
  if (Array.isArray(v)) return v.map(flat).join("");
  if (typeof v === "object" && v.strings) {
    return Array.from(v.strings).map((s, i) => s + (i < v.values.length ? flat(v.values[i]) : "")).join("");
  }
  return typeof v === "symbol" ? "" : String(v);
}

const ROW = {
  vendor_id: "11111111-1111-4111-8111-111111111111",
  vendor_no: "VM-0001",
  name: "Marked (demo)",
  business_number: "123456789",
  business_number_missing: false,
  paid_cents: 125000,
  box_22_cents: 125000,
  services_subtotal_cents: 110619,
  under_threshold: false,
  unmarked_line_count: 0,
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
  assert.equal(t5018EmptyKind({ year: 2026, rule_note: "", items: [], total_cents: 0, box_22_total_cents: 0 }), "none-marked");
  assert.equal(t5018EmptyKind({ year: 2026, rule_note: "", items: [{ ...ROW, paid_cents: 0, box_22_cents: 0, services_subtotal_cents: 0 }], total_cents: 0, box_22_total_cents: 0 }), "none-paid");
  assert.equal(t5018EmptyKind({ year: 2026, rule_note: "", items: [ROW], total_cents: 125000, box_22_total_cents: 125000 }), null);
});

test("the T5018 screen shows Box 22, the under-threshold mark, the unmarked words, and the construction income sentence under the title", () => {
  const report = {
    year: 2026,
    rule_note: "T5018 applies only where construction is more than half the dealer's business income.",
    total_cents: 179240,
    box_22_total_cents: 158290,
    items: [
      {
        ...ROW,
        paid_cents: 125000,
        box_22_cents: 104050,
        services_subtotal_cents: 92080,
        under_threshold: false,
        unmarked_line_count: 2,
        unmarked_lines_note: "2 bill lines are not marked goods or services; they are counted as services until marked",
      },
      {
        ...ROW,
        vendor_id: "22222222-2222-4222-8222-222222222222",
        vendor_no: "VM-0002",
        name: "Under Threshold Sub (demo)",
        paid_cents: 54240,
        box_22_cents: 54240,
        services_subtotal_cents: 48000,
        under_threshold: true,
        threshold_note: "under the $500 threshold, no slip",
        unmarked_line_count: 0,
      },
    ],
  };
  assert.equal(t5018RuleNote(report), "T5018 applies only where construction is more than half the dealer's business income.");
  assert.equal(t5018Box22Cents(report.items[0]), 104050);
  assert.equal(t5018UnmarkedWords(report.items[0]), "2 bill lines are not marked goods or services; they are counted as services until marked");
  assert.equal(t5018ThresholdWords(report.items[1]), "under the $500 threshold, no slip");

  const html = flat(
    renderT5018({
      state: "ready",
      report,
      error: "",
      year: 2026,
      downloading: false,
      notice: "",
    }),
  );
  assert.ok(html.includes("T5018 applies only where construction is more than half the dealer's business income."));
  assert.ok(html.includes("Box 22"));
  assert.ok(html.includes("$1,040.50"));
  assert.ok(html.includes("$1,582.90"));
  assert.ok(html.includes("under the $500 threshold, no slip"));
  assert.ok(html.includes("2 bill lines are not marked goods or services; they are counted as services until marked"));
});
