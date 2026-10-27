// FW1-01c: the Controller's two Periods tab controls: the early open of the next
// fiscal year, and the late posted list of the journals that carry a date from a
// closed month and landed in an open one.
//
// Red first: the Periods tab has neither control today. The legs read the view's
// own markup (a Lit template walked as text) and the slice's plain words, so a
// control that is not drawn, or a row that does not say what it must, fails here.
//
// Run it with the app's own loader:
//   node --import ../../packages/api-client/src/_ts-js-loader.mjs --test test/fw1-01c-periods-ui.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import * as books from "../src/gl/gl-books.ts";
import * as logic from "../src/gl/gl-logic.ts";

const PERIODS = [
  { id: "9e000000-0000-4000-9000-000000000007", year: 2026, month: 7, status: "CLOSED" },
  { id: "9e000000-0000-4000-9000-000000000008", year: 2026, month: 8, status: "OPEN" },
  { id: "9e000000-0000-4000-9000-000000000009", year: 2026, month: 9, status: "OPEN" },
];

/** The July invoice that landed in August: a closed month's date in an open month's period. */
const LATE = {
  id: "7a1e0000-0000-4000-9000-000000000012",
  fiscal_year: 2026,
  entry_no: 2,
  period_id: "9e000000-0000-4000-9000-000000000008",
  source: "INVOICE",
  memo: "July invoice booked in August (demo)",
  status: "POSTED",
  derived_status: "POSTED",
  posted_at: "2026-07-18T00:00:00.000Z",
  created_at: "2026-08-15T09:00:00.000Z",
  late_posted: true,
  lines: [],
};

function model(over = {}) {
  return {
    mode: "controller",
    chart: { phase: "ready", value: [], error: "" },
    recurring: { phase: "ready", value: [], error: "" },
    periods: { phase: "ready", value: PERIODS, error: "" },
    latePosted: { phase: "ready", value: [], error: "" },
    policy: { phase: "idle", value: null, error: "" },
    tie: { phase: "idle", value: [], error: "" },
    bis: { phase: "idle", value: null, error: "" },
    legacyImport: "",
    confirm: null,
    composing: false,
    lineCount: 2,
    review: null,
    composeError: "",
    trialMonth: 0,
    exporting: false,
    ...over,
  };
}

function frame(over = {}) {
  return { error: "", branches: [], journals: [], busy: false, books: model(), ...over };
}

/** The markup of a Lit template, as text: the static chunks with the nested templates folded in. */
function markup(node) {
  if (node === null || node === undefined || node === false || node === true) return "";
  if (typeof node === "symbol") return "";
  if (Array.isArray(node)) return node.map(markup).join("");
  if (typeof node === "object" && Array.isArray(node.strings)) {
    let out = "";
    node.strings.forEach((chunk, i) => {
      out += chunk;
      if (i < node.values.length) out += markup(node.values[i]);
    });
    return out;
  }
  return String(node);
}

test("the Controller's Periods tab carries the control that opens the next fiscal year", () => {
  const view = markup(books.periodsView(frame()));
  assert.ok(view.includes('data-action="gl-open-year"'), "the open year control is not on the Periods tab");
  assert.ok(view.includes("Open the next fiscal year"), "the control does not say what it opens");
});

test("the Controller's Periods tab carries the late posted list, each journal with its document date, the month it landed in and a way to the journal", () => {
  const late = model({ latePosted: { phase: "ready", value: [logic.latePostedRow(LATE, PERIODS[1])], error: "" } });
  const view = markup(books.periodsView(frame({ books: late })));
  assert.ok(view.includes("data-late-posted"), "the late posted list is not on the Periods tab");
  assert.ok(view.includes("Late posted"), "the list does not say what it is");
  assert.ok(view.includes("Jul 18, 2026"), "the row does not carry the document date");
  assert.ok(view.includes("August 2026"), "the row does not carry the month it landed in");
  assert.ok(view.includes('data-action="gl-open-journal"'), "the row has no way to the journal");
});

test("a reader's Periods tab carries neither control", () => {
  const view = markup(books.periodsView(frame({ books: model({ mode: "reader" }) })));
  assert.ok(!view.includes('data-action="gl-open-year"'), "a reader sees the open year control");
  assert.ok(!view.includes("data-late-posted"), "a reader sees the late posted list");
});

test("the open year refusal names the Financial Controller, in dealer words", () => {
  const words = logic.refusalWords("open-year", "gl: only the Financial Controller may reopen a closed period", 403);
  assert.ok(words.includes("Financial Controller"), words);
  assert.ok(!words.includes("General Manager"), words);
  assert.ok(!words.includes("Forbidden"), words);
  assert.ok(!words.startsWith("gl:"), words);
});

test("a late posted row reads its document date, the month it landed in and the journal's number", () => {
  assert.equal(typeof logic.latePostedRow, "function", "the slice has no words for a late posted row");
  const row = logic.latePostedRow(LATE, PERIODS[1]);
  assert.equal(row.entry, "JE-0002");
  assert.equal(row.documentDate, "Jul 18, 2026");
  assert.equal(row.landedIn, "August 2026");
  assert.equal(row.memo, "July invoice booked in August (demo)");
  assert.equal(row.id, LATE.id);
});
