// Manager Home's pure decisions, on their own: the widget figure rules, the
// needs row targets, the walled refusal words, the resolution sentences and
// the receiving difference rule. No browser, no network. Every case is a fact
// the core holds (the wire contract) or the matrix names (the list words).
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  APPROVAL_KINDS,
  APPROVAL_DECIDER_WORDS,
  RECEIVING_DIFFERENCE_WORDS,
  SHORTAGE_RESOLVER_WORDS,
  UNSOURCED_NEEDS,
  UNSOURCED_TILES,
  approvalsRefusalWords,
  canResolve,
  figureFor,
  grouped,
  isReceivingDifference,
  kindWord,
  needsRowTarget,
  rangeWord,
  resolutionText,
  resolveRefusalWords,
  severityWord,
  shareNote,
  stateNote,
} from "../src/manager-logic.ts";

test("a widget figure exists only for an ok state with a real total, and null is never zero", () => {
  const ok = { key: "k", state: "ok", total: 14, points: [], note: "", range: "today" };
  assert.equal(figureFor(ok, "count"), "14");
  assert.equal(figureFor({ ...ok, state: "unwired" }, "count"), null);
  assert.equal(figureFor({ ...ok, state: "error" }, "count"), null);
  assert.equal(figureFor({ ...ok, total: null }, "count"), null);
  assert.equal(figureFor(undefined, "count"), null);
});

test("money renders from the core's integer cents through the shared formatter", () => {
  const ok = { key: "k", state: "ok", total: 1968, points: [], note: "", range: "today" };
  assert.equal(figureFor(ok, "money_cents"), "$19.68");
  assert.equal(figureFor({ ...ok, total: 123456 }, "money_cents"), "$1,234.56");
});

test("days and basis points render as words a person reads", () => {
  const ok = { key: "k", state: "ok", total: 3, points: [], note: "", range: "open" };
  assert.equal(figureFor(ok, "days"), "3 days");
  assert.equal(figureFor({ ...ok, total: 1 }, "days"), "1 day");
  assert.equal(figureFor({ ...ok, total: 1585 }, "basis_points"), "15.85%");
  assert.equal(figureFor({ ...ok, total: 1500 }, "basis_points"), "15%");
});

test("counts group their thousands and keep a negative sign", () => {
  assert.equal(grouped(1234), "1,234");
  assert.equal(grouped(-1234), "-1,234");
  assert.equal(grouped(0), "0");
});

test("an unmeasured card says which module is not connected, never a number", () => {
  assert.equal(stateNote(undefined, "warranty"), "Not connected (warranty)");
  assert.equal(
    stateNote({ key: "k", state: "unwired", total: null, points: [], note: "", range: "open" }, "warranty"),
    "Not connected (warranty)",
  );
  assert.equal(
    stateNote({ key: "k", state: "error", total: null, points: [], note: "", range: "open" }, "warranty"),
    "Not measured right now. Try again in a moment.",
  );
});

test("a bars row's share is the note, and a total of zero answers nothing", () => {
  assert.equal(shareNote(10, 40), "25% of 40");
  assert.equal(shareNote(40, 40), "100% of 40");
  assert.equal(shareNote(5, 0), "");
});

test("severity and window words match the original bento's vocabulary", () => {
  assert.equal(severityWord("good"), "Routine");
  assert.equal(severityWord("watch"), "Watch");
  assert.equal(severityWord("stop"), "Urgent");
  assert.equal(rangeWord("today"), "Today");
  assert.equal(rangeWord("open"), "Everything open");
  assert.equal(rangeWord("last_14"), "Last 14 days");
});

test("the two new needs links open this app's views, and receiving opens Receiving", () => {
  const row = (source_key, view = "", screen = "") => ({ source_key, what: "", count: 1, age: "", severity: "good", module: "", unit: "", screen, view, action: "" });
  assert.deepEqual(needsRowTarget(row("tasks.open_shortages", "Shortages · open")), { view: "shortages" });
  assert.deepEqual(needsRowTarget(row("purchasing.receiving_differences", "Receiving · short")), { view: "receiving" });
  assert.deepEqual(needsRowTarget(row("approvals.waiting", "Waiting on you")), { view: "approvals" });
  const stale = needsRowTarget(row("tasks.stale_picks", "Picks · past deadline", "yard"));
  assert.equal(stale.view, null);
  assert.equal(stale.screen, "Picks · past deadline");
});

test("the board rows nothing measures are said plainly, never counted", () => {
  assert.equal(UNSOURCED_NEEDS.length, 1);
  assert.equal(UNSOURCED_NEEDS[0].what, "Weights pending");
  assert.match(UNSOURCED_NEEDS[0].words, /no source is wired/i);
  assert.equal(UNSOURCED_TILES.length, 1);
  assert.equal(UNSOURCED_TILES[0].title, "Unread messages");
  assert.match(UNSOURCED_TILES[0].words, /no measure is wired/i);
});

test("a resolve needs one of the three choices; the note rides beside the sentence", () => {
  assert.equal(canResolve("backorder", ""), true);
  assert.equal(canResolve("", "a note"), false);
  assert.equal(canResolve("something-else", ""), false);
  assert.equal(resolutionText("substitute", ""), "Substituted from stock at this branch.");
  assert.equal(
    resolutionText("backorder", "Customer ok with Thursday"),
    "Backordered the shortfall; the order keeps the short line open. Customer ok with Thursday",
  );
});

test("the list wall's Forbidden body becomes words naming who may resolve", () => {
  assert.equal(resolveRefusalWords("Forbidden", 403), "You may not resolve this shortage. Shortages are resolved by the yard team and the yard manager.");
  assert.equal(resolveRefusalWords("shortage already resolved", 409), "shortage already resolved");
  assert.equal(SHORTAGE_RESOLVER_WORDS, "Shortages are resolved by the yard team and the yard manager.");
});

test("a kind this caller does not decide becomes the core's own words plus who decides it", () => {
  const words = approvalsRefusalWords("MARGIN_BELOW_MINIMUM", "you do not decide MARGIN_BELOW_MINIMUM approvals", 403);
  assert.match(words, /^you do not decide MARGIN_BELOW_MINIMUM approvals /);
  assert.match(words, /financial controller/);
  assert.match(words, /sales manager or branch manager in their own branch/);
  const cod = approvalsRefusalWords("COD_CONSTRAINT", "you do not decide COD_CONSTRAINT approvals", 403);
  assert.match(cod, /financial controller and the general manager/);
  assert.equal(approvalsRefusalWords("MARGIN_BELOW_MINIMUM", "boom", 500), "boom");
});

test("the approval kind vocabulary is the core's closed set, in its order", () => {
  assert.deepEqual(
    APPROVAL_KINDS.map((k) => k.kind),
    ["MARGIN_BELOW_MINIMUM", "CREDIT_LIMIT_EXCEEDED", "COD_CONSTRAINT", "JE_OVER_THRESHOLD", "FREIGHT_ABOVE_QUOTE"],
  );
  assert.equal(kindWord("COD_CONSTRAINT"), "Cash on delivery");
  assert.equal(Object.keys(APPROVAL_DECIDER_WORDS).length, 5);
});

test("a receiving difference is a PARTIAL purchase order, per the ruled definition", () => {
  assert.equal(isReceivingDifference({ po_id: "1", po_no: "PO-1", vendor_name: "", branch_id: "", status: "PARTIAL", expected_at: "", open_lines: 1, qty_outstanding_base: 2 }), true);
  assert.equal(isReceivingDifference({ po_id: "1", po_no: "PO-1", vendor_name: "", branch_id: "", status: "ORDERED", expected_at: "", open_lines: 3, qty_outstanding_base: 30 }), false);
  assert.match(RECEIVING_DIFFERENCE_WORDS, /landed part of what was ordered/);
  assert.match(RECEIVING_DIFFERENCE_WORDS, /closed short/);
});
