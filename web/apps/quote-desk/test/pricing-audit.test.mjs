// Ported from hardscapeos_dibbits master 87d7941b app/src/_pricing-audit.test.mjs; adapted for HH.
//
// The pricing log"s date filter and pager, and the link from a log row to the record it
// is about. The filter narrows the rows fetched, the pager walks what is left, and a row
// whose date cannot be read is kept, never hidden.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  localDay,
  normaliseRange,
  inDateRange,
  filterByDate,
  pageOf,
  pageCount,
  clampPage,
  auditWindowNote,
  entriesCountText,
  PAGE_SIZE,
  drillTarget,
  bypassWord,
  floorProductIds,
  floorConfirmationText,
} from "../src/_pricing-audit.js";

// Rows built from LOCAL midday, so the fixtures mean the same calendar day in any timezone the test
// runner happens to sit in. The timezone subtlety itself is covered by its own test below.
const onDay = (day, hour = 12) => ({
  created_at: new Date(`${day}T${String(hour).padStart(2, "0")}:00:00`).toISOString(),
});

const rows = [onDay("2026-08-04"), onDay("2026-08-03"), onDay("2026-07-20"), onDay("2026-06-01")];

// ── the date range ─────────────────────────────────────────────────────────────────────────────────

test("an open range keeps everything", () => {
  assert.equal(filterByDate(rows, { from: "", to: "" }).length, rows.length);
  assert.equal(filterByDate(rows, {}).length, rows.length);
});

test("both bounds are INCLUSIVE", () => {
  // A user who types the 3rd to the 4th means both of those days. An exclusive end silently drops
  // today"s entries, which is the single most confusing thing a date filter can do.
  const got = filterByDate(rows, { from: "2026-08-03", to: "2026-08-04" });
  assert.equal(got.length, 2);
  assert.deepEqual(got.map((r) => localDay(r.created_at)), ["2026-08-04", "2026-08-03"]);
});

test("a single-day range returns that day", () => {
  const got = filterByDate(rows, { from: "2026-07-20", to: "2026-07-20" });
  assert.equal(got.length, 1);
  assert.equal(localDay(got[0].created_at), "2026-07-20");
});

test("an open-ended from or to bounds only that side", () => {
  assert.equal(filterByDate(rows, { from: "2026-08-01", to: "" }).length, 2);
  assert.equal(filterByDate(rows, { from: "", to: "2026-07-20" }).length, 2);
});

test("the filter compares the LOCAL day the row displays, not the UTC instant", () => {
  // The When column renders local time. A row shown as "2026-08-03 23:30" must be found by asking
  // for the 3rd, even though it is stored (and would compare) as the 4th in UTC west of Greenwich.
  const late = onDay("2026-08-03", 23);
  assert.equal(localDay(late.created_at), "2026-08-03");
  assert.equal(inDateRange(late, { from: "2026-08-03", to: "2026-08-03" }), true);
});

test("a transposed range is corrected, not answered with nothing", () => {
  assert.deepEqual(normaliseRange({ from: "2026-08-04", to: "2026-08-03" }), {
    from: "2026-08-03",
    to: "2026-08-04",
  });
  assert.equal(filterByDate(rows, { from: "2026-08-04", to: "2026-08-03" }).length, 2);
});

test("a half-typed or junk bound is ignored rather than emptying the table", () => {
  // Native date inputs emit partial values while being typed.
  for (const bad of ["2026-08", "", "yesterday", "04/08/2026", null, undefined, 20260804]) {
    assert.equal(normaliseRange({ from: bad, to: "" }).from, "");
    assert.equal(filterByDate(rows, { from: bad, to: "" }).length, rows.length);
  }
  assert.deepEqual(normaliseRange(null), { from: "", to: "" });
});

test("a row whose timestamp will not parse is KEPT, never silently hidden", () => {
  // This is an audit log. A row disappearing because its date could not be read is the one failure
  // mode that must not happen quietly.
  const broken = { created_at: "not-a-timestamp" };
  assert.equal(localDay(broken.created_at), "");
  assert.equal(inDateRange(broken, { from: "2026-08-01", to: "2026-08-02" }), true);
  assert.equal(filterByDate([...rows, broken], { from: "2026-08-04", to: "2026-08-04" }).length, 2);
});

test("filtering does not mutate the source list", () => {
  const source = [...rows];
  filterByDate(source, { from: "2026-08-04", to: "2026-08-04" });
  assert.equal(source.length, rows.length);
});

// ── the pager ──────────────────────────────────────────────────────────────────────────────────────

const many = (n) => Array.from({ length: n }, (_, i) => ({ id: i }));

test("an empty set still reads as page 1 of 1 with no rows", () => {
  const p = pageOf([], 1);
  assert.deepEqual(
    { page: p.page, pages: p.pages, total: p.total, rows: p.rows.length },
    { page: 1, pages: 1, total: 0, rows: 0 },
  );
  assert.equal(p.hasPrev, false);
  assert.equal(p.hasNext, false);
  assert.equal(p.firstIndex, 0);
  assert.equal(p.lastIndex, 0);
});

test("a full set splits into pages of PAGE_SIZE", () => {
  const p = pageOf(many(60), 1);
  assert.equal(p.rows.length, PAGE_SIZE);
  assert.equal(p.pages, pageCount(60));
  assert.equal(p.pages, 3);
});

test("the last page holds the remainder, not a padded page", () => {
  const p = pageOf(many(60), 3);
  assert.equal(p.rows.length, 60 - 2 * PAGE_SIZE);
  assert.equal(p.hasNext, false);
  assert.equal(p.hasPrev, true);
});

test("Prev and Next are offered only where they lead somewhere", () => {
  assert.equal(pageOf(many(60), 1).hasPrev, false);
  assert.equal(pageOf(many(60), 1).hasNext, true);
  assert.equal(pageOf(many(60), 2).hasPrev, true);
  assert.equal(pageOf(many(60), 2).hasNext, true);
  assert.equal(pageOf(many(60), 3).hasNext, false);
  assert.equal(pageOf(many(10), 1).hasNext, false);
});

test("the counts describe the FILTERED set, so the pager cannot lie about what it walks", () => {
  const p = pageOf(many(137), 2);
  assert.equal(p.firstIndex, PAGE_SIZE + 1);
  assert.equal(p.lastIndex, 2 * PAGE_SIZE);
  assert.equal(p.total, 137);
});

test("a page past the end clamps to the last page instead of showing nothing", () => {
  // The case this exists for: you are on page 5 and then narrow the date range to a single day.
  const p = pageOf(many(30), 99);
  assert.equal(p.page, 2);
  assert.equal(p.rows.length, 5);
  assert.ok(p.rows.length > 0, "a narrowed filter must never leave the user on a blank page");
});

test("a page below 1, or junk, clamps to 1", () => {
  for (const bad of [0, -3, Number.NaN, Number.POSITIVE_INFINITY]) {
    assert.equal(pageOf(many(30), bad).page, 1);
  }
  assert.equal(clampPage(1.9, 30), 1);
});

test("every row appears on exactly one page", () => {
  const all = many(137);
  const seen = new Set();
  for (let page = 1; page <= pageCount(137); page++) {
    for (const r of pageOf(all, page).rows) {
      assert.equal(seen.has(r.id), false, `row ${r.id} appears twice`);
      seen.add(r.id);
    }
  }
  assert.equal(seen.size, 137);
});

test("the date filter and the pager compose", () => {
  const set = [
    ...Array.from({ length: 40 }, () => onDay("2026-08-04")),
    ...Array.from({ length: 40 }, () => onDay("2026-06-01")),
  ];
  const filtered = filterByDate(set, { from: "2026-08-04", to: "2026-08-04" });
  const p = pageOf(filtered, 2);
  assert.equal(p.total, 40, "the pager walks the filtered set, not the whole log");
  assert.equal(p.pages, 2);
  assert.equal(p.rows.length, 15);
});

// ── the honesty line ───────────────────────────────────────────────────────────────────────────────

test("a capped fetch says so, because the filter cannot see past it", () => {
  // Client-side filtering over a server-capped window under-reports silently unless it admits the
  // window. This is the sentence that admits it.
  const note = auditWindowNote(200, 200);
  assert.ok(note && note.includes("200"), note);
});

test("a short fetch means the window IS the whole log, and says nothing", () => {
  assert.equal(auditWindowNote(37, 200), null);
  assert.equal(auditWindowNote(0, 200), null);
});

test("the heading's entry count is said in the singular and the plural", () => {
  // One row reads "1 entry"; the filtered shape names both counts and still agrees in number.
  assert.equal(entriesCountText(2, 2), "2 entries");
  assert.equal(entriesCountText(1, 1), "1 entry");
  assert.equal(entriesCountText(0, 0), "0 entries");
  assert.equal(entriesCountText(1, 2), "1 of 2 entries");
  assert.equal(entriesCountText(1, 1), entriesCountText(1, 1).trim());
});

// ── the link from a row to its record ───────────────────────────────────────────────────────────────

const CAT = "ca700000-0000-4000-9000-000000000001";
const CUST = "c0000000-0000-4000-9000-000000000001";

test("a customer or category reference opens the tab that owns it, with the record named", () => {
  assert.deepEqual(drillTarget({ ref_entity_type: "customer", ref_entity_id: CUST }), {
    kind: "customer",
    id: CUST,
    tab: "customers",
    label: "Open customer pricing",
  });
  assert.deepEqual(drillTarget({ ref_entity_type: "category", ref_entity_id: CAT }), {
    kind: "category",
    id: CAT,
    tab: "margin",
    label: "Open category policy",
  });
});

test("a product, quote or order reference has no tab here, so it is not a link", () => {
  for (const kind of ["product", "quote", "order"]) {
    assert.equal(drillTarget({ ref_entity_type: kind, ref_entity_id: CAT }), null);
  }
});

test("a missing, half set, unknown or malformed reference is no link at all", () => {
  assert.equal(drillTarget(null), null);
  assert.equal(drillTarget({}), null);
  assert.equal(drillTarget({ ref_entity_type: "customer" }), null);
  assert.equal(drillTarget({ ref_entity_id: CUST }), null);
  assert.equal(drillTarget({ ref_entity_type: "warehouse", ref_entity_id: CUST }), null);
  assert.equal(drillTarget({ ref_entity_type: "customer", ref_entity_id: "not-a-uuid" }), null);
});

// ── the Change column of the log ───────────────────────────────────────────────────────────────────

const P1 = "a7000000-0000-4000-9000-000000000001";
const P2 = "a7000000-0000-4000-9000-000000000002";

test("a BYPASS row's reason is kept and read in words", () => {
  assert.equal(bypassWord("PRODUCT_POLICY_DISABLED"), "the product's margin policy is switched off");
  assert.equal(bypassWord("CATEGORY_POLICY_DISABLED"), "the category's margin policy is switched off");
  assert.equal(bypassWord("APPROVED_OVERRIDE"), "an approved override");
  assert.equal(bypassWord("MANAGER_SCOPE_OVERRIDE"), "a manager's override");
  assert.equal(bypassWord("SOMETHING_NEW"), "SOMETHING NEW");
  assert.equal(bypassWord(undefined), "");
});

test("the products a floor confirmation names are found in its text, once each, in order", () => {
  const text = `the editor confirmed a floor warning naming 3 products: ${P1}, ${P2}, ${P1}`;
  assert.deepEqual(floorProductIds(text), [P1, P2]);
  assert.deepEqual(floorProductIds("1 product under the margin floor"), []);
  assert.deepEqual(floorProductIds(undefined), []);
});

test("a floor confirmation shows product names where they are known and a plain word where not", () => {
  const text = `the editor confirmed a floor warning naming 2 products: ${P1}, ${P2}`;
  const names = new Map([[P1, "HPV-60 · Holland Stone 60 mm"]]);
  assert.equal(
    floorConfirmationText(text, names),
    "the editor confirmed a floor warning naming 2 products: HPV-60 · Holland Stone 60 mm, a record",
  );
  assert.equal(floorConfirmationText("size changed", names), "size changed");
});
