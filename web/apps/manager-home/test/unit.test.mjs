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
  RESOLUTION_CHOICES,
  SHORTAGE_RESOLVER_WORDS,
  UNSOURCED_NEEDS,
  UNSOURCED_TILES,
  approvalsRefusalWords,
  canResolve,
  dateWords,
  errorWords,
  figureFor,
  grouped,
  isReceivingDifference,
  kindWord,
  needsRowTarget,
  pickedWords,
  qtyWords,
  rangeWord,
  shortageCard,
  resolutionText,
  resolveRefusalWords,
  severityWord,
  shareNote,
  stateNote,
  taskStatusWord,
  welcomeLine,
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
  const dispatch = needsRowTarget(row("dispatch.unassigned", "Staging · unassigned", "dispatch"));
  assert.equal(dispatch.view, null);
  assert.equal(dispatch.screen, "Staging · unassigned");
});

test("the board row the core could not measure is said plainly, never counted", () => {
  assert.equal(UNSOURCED_NEEDS.length, 1);
  assert.equal(UNSOURCED_NEEDS[0].what, "Weights pending");
  assert.match(UNSOURCED_NEEDS[0].words, /not counted/i);
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

test("each resolution choice carries its one line consequence, the board's own words", () => {
  const byKey = Object.fromEntries(RESOLUTION_CHOICES.map((c) => [c.key, c]));
  assert.equal(byKey.backorder.consequence, "The line is backordered and the order's rep is notified.");
  assert.equal(byKey.substitute.consequence, "Pick the swap line before the truck leaves.");
  assert.equal(byKey.approve.consequence, "The order ships as picked; the shortfall is written off.");
});

test("the list wall's Forbidden body becomes words naming who may resolve", () => {
  assert.equal(resolveRefusalWords("Forbidden", 403), "You may not resolve this shortage. Shortages are resolved by the yard team and the yard manager.");
  assert.equal(resolveRefusalWords("shortage already resolved", 409), "shortage already resolved");
  assert.equal(SHORTAGE_RESOLVER_WORDS, "Shortages are resolved by the yard team and the yard manager.");
});

test("a kind this caller does not decide becomes plain words naming who decides it, no enum on screen", () => {
  const words = approvalsRefusalWords("MARGIN_BELOW_MINIMUM", "you do not decide MARGIN_BELOW_MINIMUM approvals", 403);
  assert.equal(
    words,
    "You do not decide margin approvals. Margin approvals are decided by the financial controller, " +
      "the general manager, and a sales manager in their own branch.",
  );
  const cod = approvalsRefusalWords("COD_CONSTRAINT", "you do not decide COD_CONSTRAINT approvals", 403);
  assert.equal(
    cod,
    "You do not decide Cash On Demand approvals. Cash On Demand approvals are decided by the " +
      "financial controller and the general manager.",
  );
  assert.equal(approvalsRefusalWords("MARGIN_BELOW_MINIMUM", "boom", 500), "boom");
});

test("the approval kind vocabulary is the core's closed set, in its order", () => {
  assert.deepEqual(
    APPROVAL_KINDS.map((k) => k.kind),
    ["MARGIN_BELOW_MINIMUM", "CREDIT_LIMIT_EXCEEDED", "COD_CONSTRAINT", "JE_OVER_THRESHOLD", "FREIGHT_ABOVE_QUOTE"],
  );
  assert.equal(kindWord("COD_CONSTRAINT"), "Cash On Demand");
  assert.equal(Object.keys(APPROVAL_DECIDER_WORDS).length, 5);
});

test("a receiving difference is a PARTIAL purchase order, per the ruled definition", () => {
  assert.equal(isReceivingDifference({ po_id: "1", po_no: "PO-1", vendor_name: "", branch_id: "", status: "PARTIAL", expected_at: "", open_lines: 1, qty_outstanding_base: 2 }), true);
  assert.equal(isReceivingDifference({ po_id: "1", po_no: "PO-1", vendor_name: "", branch_id: "", status: "ORDERED", expected_at: "", open_lines: 3, qty_outstanding_base: 30 }), false);
  assert.match(RECEIVING_DIFFERENCE_WORDS, /landed part of what was ordered/);
  assert.match(RECEIVING_DIFFERENCE_WORDS, /closed short/);
});

test("quantities render as a person reads them, never the wire's padded decimal", () => {
  assert.equal(qtyWords("4.0000"), "4");
  assert.equal(qtyWords("12.5000"), "12.5");
  assert.equal(qtyWords("0.7500"), "0.75");
  assert.equal(qtyWords("0.0000"), "0");
  assert.equal(qtyWords(""), "");
});

test("picked is ordered less short, computed only when both are honest decimals", () => {
  assert.equal(pickedWords("12.0000", "4.0000"), "8");
  assert.equal(pickedWords("12.0000", "12.0000"), "0");
  assert.equal(pickedWords("12.0000", "13.0000"), null);
  assert.equal(pickedWords("", "4.0000"), null);
  assert.equal(pickedWords("12.0000", ""), null);
});

test("task statuses map to plain words; an unknown one is omitted, never shown raw", () => {
  assert.equal(taskStatusWord("QUEUED"), "Waiting");
  assert.equal(taskStatusWord("PUSHED"), "Sent");
  assert.equal(taskStatusWord("ACCEPTED"), "Claimed");
  assert.equal(taskStatusWord("IN_PROGRESS"), "In progress");
  assert.equal(taskStatusWord("STAGED"), "Staged");
  assert.equal(taskStatusWord("DONE"), "Done");
  assert.equal(taskStatusWord("CANCELLED"), "Cancelled");
  assert.equal(taskStatusWord("SOMETHING_ELSE"), "");
});

test("expected dates render as words; anything unparsable is omitted", () => {
  assert.equal(dateWords("2026-10-14"), "Oct 14");
  assert.equal(dateWords("2026-01-02"), "Jan 2");
  assert.equal(dateWords("not-a-date"), "");
  assert.equal(dateWords(""), "");
});

test("a one word server error becomes plain words; a sentence stays the server's own", () => {
  assert.equal(errorWords("internal"), "Try again in a moment.");
  assert.equal(errorWords("MARGIN_BELOW_MINIMUM"), "Try again in a moment.");
  assert.equal(errorWords("shortage already resolved"), "shortage already resolved");
  assert.equal(errorWords(""), "Try again in a moment.");
});

test("the welcome line names the caller in the board's own sentence", () => {
  assert.equal(welcomeLine("Yard manager"), "Welcome back, yard manager.");
  assert.equal(welcomeLine("Sales manager"), "Welcome back, sales manager.");
  assert.equal(welcomeLine("General Manager"), "Welcome back, general manager.");
  assert.equal(welcomeLine(""), "Welcome back.");
});

test("Cash On Demand is named as the house names it, never cash on delivery (decision row 999)", () => {
  const words = [kindWord("COD_CONSTRAINT"), approvalsRefusalWords("COD_CONSTRAINT", "x", 403), APPROVAL_DECIDER_WORDS.COD_CONSTRAINT].join(" ");
  assert.match(words, /Cash On Demand/);
  assert.doesNotMatch(words, /on delivery/i);
});

test("the Branch Manager decides no approval kind, so no kind's words name a branch manager", () => {
  for (const words of Object.values(APPROVAL_DECIDER_WORDS)) assert.doesNotMatch(words, /branch manager/i);
  assert.equal(
    APPROVAL_DECIDER_WORDS.MARGIN_BELOW_MINIMUM,
    "Margin approvals are decided by the financial controller, the general manager, and a sales manager in their own branch.",
  );
});

test("a shortage review card says what was short, how much, who reported it by role, when, and the status word", () => {
  const now = Date.parse("2030-01-02T12:00:00Z");
  const open = {
    id: "s1", task_id: "t1", order_line_no: 2, sku: "G", description: "Granular A gravel", qty_short: "4.0000",
    uom: "CYD", note: "Only 8 yards left", reported_by: "11111111-1111-4111-8111-111111111111",
    reported_at: "2030-01-02T10:30:00Z", resolved_at: null, resolution: "",
  };
  const card = shortageCard(open, now);
  assert.equal(card.what, "Granular A gravel");
  assert.equal(card.howMuch, "line 2, short 4 CYD");
  assert.equal(card.reporter, "Reported by the yard team");
  assert.equal(card.when, "1h ago");
  assert.equal(card.status, "Needs review");
  assert.equal(card.resolved, false);
  assert.doesNotMatch(JSON.stringify(card), /1111/);
});

test("a resolved shortage card reads Resolved with the core's resolution, and a bad time is omitted", () => {
  const now = Date.parse("2030-01-02T12:00:00Z");
  const done = {
    id: "s2", task_id: "t1", order_line_no: null, sku: "", description: "Paver", qty_short: "40", uom: "PC",
    note: "", reported_by: "", reported_at: "nonsense", resolved_at: "2030-01-02T11:00:00Z", resolution: "Substituted.",
  };
  const card = shortageCard(done, now);
  assert.equal(card.status, "Resolved");
  assert.equal(card.howMuch, "short 40 PC");
  assert.equal(card.when, "");
  assert.equal(card.resolved, true);
});
