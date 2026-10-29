// UX-121: the Money Desk's register words, read off the register itself. The
// status counts say how many of the loaded rows carry them while the core's
// cursor says more remain, the posted date range is said in the desk's own date
// style, and the note on Pending approval says what it means to a bookkeeper.
//
// Red first: today the counts claim a total ("Posted 98") while more rows are
// still to come, the range says the core's ISO digits beside date inputs a
// dealer reads as mm/dd/yyyy, and the note explains itself in engine words
// ("Pending approval is a derived label ... Nothing writes it to the row").
//
// The legs read the view's own markup (a Lit template walked as text), the way
// test/fw1-01c-periods-ui.test.mjs reads the books screens, so the words are
// held where a person reads them.
//
// Run it with the app's own loader:
//   node --import ../../packages/api-client/src/_ts-js-loader.mjs --test test/ux121-register-words.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import * as views from "../src/gl/gl-views.ts";

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
    periods: { phase: "ready", value: [], error: "" },
    nextOpenableYear: 0,
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

function glModel(over = {}) {
  return {
    phase: "ready",
    tab: "journals",
    userId: "u1",
    approvals: [],
    selectedApproval: null,
    choice: "approve",
    approvalJournal: null,
    journals: [],
    journalFilter: "all",
    journalFrom: "",
    journalTo: "",
    journalsNext: "",
    reaching: false,
    selectedJournal: null,
    trial: null,
    trialBranch: "",
    branches: [],
    busy: false,
    error: "",
    notice: "",
    postingQueueWords: "",
    books: booksModel(),
    ...over,
  };
}

/** One loaded journal: posted, from a manual entry, carrying its posted day. */
function journal(entryNo, over = {}) {
  return {
    id: `7a1e0000-0000-4000-9000-${String(entryNo).padStart(12, "0")}`,
    fiscal_year: 2026,
    entry_no: entryNo,
    period_id: "9e000000-0000-4000-9000-000000000008",
    source: "MANUAL",
    memo: `Journal ${entryNo}`,
    status: "POSTED",
    derived_status: "POSTED",
    posted_at: "2026-08-23T12:00:00.000Z",
    created_at: "2026-08-23T12:00:00.000Z",
    lines: [],
    ...over,
  };
}

/** A loaded page of a hundred: ninety-eight posted, two parked and waiting. */
function loadedPage() {
  const rows = [];
  for (let entryNo = 1; entryNo <= 100; entryNo += 1) {
    const parked = entryNo <= 2;
    rows.push(
      journal(entryNo, {
        status: parked ? "DRAFT" : "POSTED",
        derived_status: parked ? "PENDING_APPROVAL" : "POSTED",
        posted_at: parked ? null : "2026-08-23T12:00:00.000Z",
      }),
    );
  }
  return rows;
}

// ---- the counts -------------------------------------------------------------------------------------

test("the status counts say how many of the loaded rows carry them while the core's cursor says more remain", () => {
  const view = markup(views.renderGl(glModel({ journals: loadedPage(), journalsNext: "v1:2025/137" })));
  assert.ok(view.includes("Posted 98 of the loaded"), "the Posted count claims a total while more rows remain");
  assert.ok(view.includes("Pending 2 of the loaded"), "the Pending count claims a total while more rows remain");
  assert.ok(!/Posted 98\b(?! of the loaded)/.test(view), "the Posted count still reads as a total of every posted journal");
});

test("at the end of the register the counts stand alone, because then they are the whole of them", () => {
  const view = markup(views.renderGl(glModel({ journals: loadedPage(), journalsNext: "" })));
  assert.ok(view.includes("Posted 98"), "the Posted count is missing at the end of the register");
  assert.ok(!view.includes("of the loaded"), "the count still says it is of the loaded rows when the register is at its end");
});

test("the tab's own count of the parked journals is honest the same way", () => {
  const view = markup(views.renderGl(glModel({ journals: loadedPage(), journalsNext: "v1:2025/137" })));
  assert.ok(view.includes("2 of the loaded"), "the Journals tab claims a total of the parked journals while more remain");
});

// ---- the date range ---------------------------------------------------------------------------------

test("the posted date range is said in the desk's own date style, not in the core's ISO digits", () => {
  const view = markup(views.renderGl(glModel({ journalFrom: "2025-06-01", journalTo: "2025-06-30" })));
  assert.ok(view.includes("Jun 1, 2025 through Jun 30, 2025"), "the range is not said in the desk's own date words");
  assert.ok(!view.includes("2025-06-01"), "the range still shows the core's ISO day beside the date inputs");
});

test("an open bound of the range is said in the desk's own date style too", () => {
  const from = markup(views.renderGl(glModel({ journalFrom: "2025-06-01" })));
  assert.ok(from.includes("Jun 1, 2025 or later"), "the open end of the range is not in the desk's own date words");
  assert.ok(!from.includes("2025-06-01"), "the open end of the range still shows the core's ISO day");
  const to = markup(views.renderGl(glModel({ journalTo: "2025-06-30" })));
  assert.ok(to.includes("through Jun 30, 2025"), "the open start of the range is not in the desk's own date words");
  assert.ok(!to.includes("2025-06-30"), "the open start of the range still shows the core's ISO day");
});

// ---- the note on Pending approval -------------------------------------------------------------------

test("the note on Pending approval says what it means to a bookkeeper", () => {
  const view = markup(views.renderGl(glModel({ journals: loadedPage() })));
  assert.ok(!view.includes("derived label"), "the note still explains itself in engine words");
  assert.ok(!view.includes("Nothing writes it to the row"), "the note still speaks of the row instead of the journal");
  assert.ok(
    view.includes("Pending approval means the journal is a draft someone entered"),
    "the note does not say that a parked journal is a draft a person entered",
  );
  assert.ok(
    view.includes("waits for another approver to post it"),
    "the note does not say that the journal waits for someone else to post it",
  );
});
