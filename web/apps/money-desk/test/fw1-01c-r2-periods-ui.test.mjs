// FW1-01c round 2: the visual read's findings on PR 614, the words.
// The open year control, its review and the tab's own line name the fiscal year the control opens,
// the way "Close fiscal year" names its year, and the line under the tab's heading names the new
// controls.
//
// Red first: today the control says "Open the next fiscal year" and the line under the tab's heading
// speaks only of the months and the year end close.
//
// Run it with the app's own loader:
//   node --import ../../packages/api-client/src/_ts-js-loader.mjs --test test/fw1-01c-r2-periods-ui.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import * as books from "../src/gl/gl-books.ts";
import * as views from "../src/gl/gl-views.ts";

const PERIODS = [
  { id: "9e000000-0000-4000-9000-000000000007", year: 2026, month: 7, status: "CLOSED" },
  { id: "9e000000-0000-4000-9000-000000000008", year: 2026, month: 8, status: "OPEN" },
  { id: "9e000000-0000-4000-9000-000000000009", year: 2026, month: 9, status: "OPEN" },
];

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

function booksModel(over = {}) {
  return {
    mode: "controller",
    chart: { phase: "ready", value: [], error: "" },
    recurring: { phase: "ready", value: [], error: "" },
    periods: { phase: "ready", value: PERIODS, error: "" },
    // The core read's answer: the year the Controller's open can still make sure of, 0 when none
    // can. The control and its review name this year and compute no year of their own (S2 P2-1).
    nextOpenableYear: 2028,
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
  return { error: "", branches: [], journals: [], busy: false, books: booksModel(), ...over };
}

function glModel(over = {}) {
  return {
    phase: "ready",
    tab: "periods",
    userId: "u1",
    approvals: [],
    selectedApproval: null,
    choice: "approve",
    approvalJournal: null,
    journals: [],
    journalFilter: "all",
    selectedJournal: null,
    trial: null,
    trialBranch: "",
    branches: [],
    busy: false,
    error: "",
    notice: "",
    books: booksModel(),
    ...over,
  };
}

test("the open year control names the fiscal year the core's read answers", () => {
  const view = markup(books.periodsView(frame()));
  assert.ok(view.includes("Open fiscal year 2028"), "the control does not name the year the core's read answers");
  assert.ok(!view.includes("Open fiscal year 2027"), "the control still computes the year as latest + 1");
  assert.ok(!view.includes("Open the next fiscal year"), "the control still speaks of a next year without naming it");
});

test("when the core's read answers none, neither the control nor its review is drawn", () => {
  const view = markup(books.periodsView(frame({ books: booksModel({ nextOpenableYear: 0, confirm: { kind: "open-year" } }) })));
  assert.ok(!view.includes('data-action="gl-open-year"'), "the control is drawn with no year left to open");
  assert.ok(!view.includes("Open fiscal year"), "a review for the open is drawn with no year left to open");
});

test("the open year review names the fiscal year, on its heading, its bullets and its press", () => {
  const view = markup(books.periodsView(frame({ books: booksModel({ confirm: { kind: "open-year" } }) })));
  assert.ok(view.includes("Open fiscal year 2028?"), "the review heading does not name the year");
  assert.ok(view.includes("twelve monthly periods of fiscal year 2028"), "the review does not say what opens");
  assert.ok(view.includes("Open fiscal year 2028</button>"), "the review's own press does not name the year");
});

test("the line under the tab's heading names the new controls", () => {
  const view = markup(views.renderGl(glModel()));
  // The line under the tab's heading, and not the panels below it.
  const head = /<div class="gl-head">[\s\S]*?<p class="gl-dim">([^<]*)<\/p>/.exec(view);
  assert.ok(head !== null, "the tab has no line under its heading");
  assert.ok(head[1].includes("next fiscal year"), `the line does not name the open year control: ${head[1]}`);
  assert.ok(head[1].includes("late posted"), `the line does not name the late posted list: ${head[1]}`);
});
