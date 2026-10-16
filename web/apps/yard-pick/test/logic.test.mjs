// The ported logic's unit leg: every rule the screens rest on, pinned in
// Node with no DOM and no browser. Each case names the port's source: the
// original app's yard-logic.ts, or the core's own file where HH wrote the
// rule against the server.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { goMaxPickedLines } from "./_lib/gosets.mjs";
import { CORE_PICK_CANCELLED, CORE_PICK_DONE, recordRefusalWords } from "../src/yard-logic.ts";
import {
  MAX_PICKED_LINES,
  MAX_SKIDS,
  isLocalLine,
  pickQueuedWords,
  pickRecordProblem,
  RESOLVE_WALL_NAMES,
  SHORT_REASONS,
  FORBIDDEN_BODY,
  CHANGE_ACK_WALL,
  ackDoneWords,
  actionDone,
  actionLabel,
  ageLabel,
  canAcknowledge,
  changeStatusWord,
  isUnclaimed,
  lineIsShort,
  mtoLabel,
  nextAction,
  orderHandle,
  pickSummary,
  priorityWord,
  qtyLabel,
  qtyText,
  queueOrder,
  reasonPhrase,
  reasonWord,
  recordBlockedReason,
  shortOf,
  shortPickWords,
  shortageSummary,
  shortageSentWords,
  skidBlockedReason,
  skidWords,
  statusTone,
  taskNextStates,
  validatePickQty,
  validateShortage,
  validateStage,
  wallWords,
  yardError,
} from "../src/yard-logic.ts";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const viewsSource = readFileSync(join(appDir, "src", "views.ts"), "utf8");

// ---- the lifecycle, ported from the original's yard-logic.ts and held to the core's model.go ----

test("the lifecycle offers claim, start and stage exactly where the core allows them", () => {
  assert.equal(nextAction("QUEUED"), "claim");
  assert.equal(nextAction("PUSHED"), "claim");
  assert.equal(nextAction("ACCEPTED"), "start");
  assert.equal(nextAction("IN_PROGRESS"), "stage");
  assert.equal(nextAction("STAGED"), null);
  assert.equal(nextAction("DONE"), null);
  assert.equal(nextAction("CANCELLED"), null);
  assert.equal(nextAction("SOMETHING_ELSE"), null);
  assert.deepEqual(actionLabel("claim"), "Claim");
  assert.deepEqual(actionLabel("start"), "Start picking");
  assert.deepEqual(actionLabel("stage"), "Stage");
});

test("the ported matrix mirrors the core's allowedTransitions exactly", () => {
  assert.deepEqual(taskNextStates("QUEUED"), ["PUSHED", "ACCEPTED", "CANCELLED"]);
  assert.deepEqual(taskNextStates("IN_PROGRESS"), ["STAGED", "DONE", "CANCELLED"]);
  assert.deepEqual(taskNextStates("STAGED"), ["DONE", "CANCELLED"]);
  assert.deepEqual(taskNextStates("DONE"), []);
  assert.deepEqual(taskNextStates("UNKNOWN"), []);
});

test("the done words carry the two facts that move together on a stage", () => {
  assert.equal(actionDone("claim"), "Claimed. It is yours to pull.");
  assert.equal(actionDone("start"), "Picking started.");
  assert.equal(actionDone("stage", { location: "Bay 3", ref: "STG-AB2CDE" }), "Staged at Bay 3, ref STG-AB2CDE. The counter has been told it is ready.");
  assert.equal(actionDone("stage", {}), "Staged. The counter has been told it is ready.");
});

test("the unclaimed half of the board is the queue's own split", () => {
  assert.equal(isUnclaimed("QUEUED"), true);
  assert.equal(isUnclaimed("PUSHED"), true);
  assert.equal(isUnclaimed("ACCEPTED"), false);
  assert.equal(statusTone("STAGED"), "ready");
  assert.equal(statusTone("CANCELLED"), "stopped");
});

// ---- the board's order and its words ----

test("queueOrder puts an open shortage first, then open work, priority, oldest", () => {
  const now = new Date("2026-01-02T12:00:00Z");
  const row = (over) => ({
    task: {
      open_shortages: over.short ?? 0,
      status: over.status ?? "QUEUED",
      priority: over.priority ?? "NORMAL",
      created_at: over.at ?? "2026-01-01T10:00:00Z",
    },
    ...over.extra,
  });
  const ordered = queueOrder([
    row({ priority: "URGENT", at: "2026-01-01T09:00:00Z", extra: { id: "plain" } }),
    row({ short: 1, extra: { id: "short" } }),
    row({ status: "DONE", extra: { id: "done" } }),
    row({ at: "2025-12-31T08:00:00Z", extra: { id: "older" } }),
  ]);
  assert.deepEqual(ordered.map((r) => r.id), ["short", "plain", "older", "done"]);
});

test("ageLabel speaks in the units a person uses, pinned to a passed clock", () => {
  const now = new Date("2026-01-02T12:00:00Z");
  assert.equal(ageLabel("2026-01-02T11:59:40Z", now), "just now");
  assert.equal(ageLabel("2026-01-02T11:30:00Z", now), "30m");
  assert.equal(ageLabel("2026-01-02T09:00:00Z", now), "3h");
  assert.equal(ageLabel("2025-12-30T12:00:00Z", now), "3d");
  assert.equal(ageLabel("not a date", now), "");
});

test("qtyText trims the storage format and never rounds or edits what it cannot read", () => {
  assert.equal(qtyText("46.0000"), "46");
  assert.equal(qtyText("2.5000"), "2.5");
  assert.equal(qtyText("0.0500"), "0.05");
  assert.equal(qtyText("7"), "7");
  assert.equal(qtyText("?"), "?");
  assert.equal(qtyText(""), "");
  assert.equal(qtyLabel("2.5000", "TONNE"), "2.5 TONNE");
  assert.equal(qtyLabel("3", ""), "3");
});

test("the shortage summary leads with the count and names the first item", () => {
  assert.equal(
    shortageSummary([{ resolved_at: "", qty_short: "22.0000", uom: "SQFT", description: "Flagstone" }]),
    "1 open shortage · short 22 SQFT of Flagstone",
  );
  assert.equal(shortageSummary([{ resolved_at: "x", qty_short: "1", uom: "EA", description: "d" }]), "");
  assert.equal(shortageSummary(undefined), "");
});

test("priority words and the made to order note follow the feed's own rules", () => {
  assert.equal(priorityWord("URGENT"), "Wanted today");
  assert.equal(priorityWord("HIGH"), "Wanted soon");
  assert.equal(priorityWord("NORMAL"), "");
  assert.equal(priorityWord("LOW"), "No rush");
  assert.equal(priorityWord("WHENEVER"), "whenever");
  assert.equal(mtoLabel("MTO"), "Made to order: do not substitute");
  assert.equal(mtoLabel("MIXED"), "Mixed load: check each pallet");
  assert.equal(mtoLabel("MTO", "PICKUP"), "");
  assert.equal(mtoLabel("STANDARD"), "");
});

test("a row is called by its order number, never a uuid and never nothing", () => {
  assert.equal(orderHandle({ order_no: "SO-1042" }), "SO-1042");
  assert.equal(orderHandle({ reference: "ST-2201", order_no: "" }), "ST-2201");
  assert.equal(orderHandle({ reference: "0a000000-0000-4000-9000-000000000042", order_no: "" }), "Order number not on file");
  assert.equal(orderHandle({ customer_name: "Someone (demo)" }), "Someone (demo)");
  assert.equal(orderHandle(null), "Order number not on file");
});

// ---- the short pick reasons, the core's closed list (0369) ----

test("the reason list is the core's own, in the order a screen shows it", () => {
  assert.deepEqual(SHORT_REASONS, ["CONDENSED_SKIDS", "DAMAGED", "NOT_IN_YARD", "COUNT_CORRECTION", "OTHER"]);
  assert.equal(reasonPhrase("CONDENSED_SKIDS"), "Skids condensed");
  assert.equal(reasonPhrase("NOT_IN_YARD"), "Not in the yard");
  assert.equal(reasonPhrase("SOMETHING"), "SOMETHING");
});

test("the picker's reason words are words on the board's pattern, never the core's codes", () => {
  assert.equal(reasonWord("NOT_IN_YARD"), "Not enough on the yard");
  assert.equal(reasonWord("DAMAGED"), "Damaged stock");
  assert.equal(reasonWord("CONDENSED_SKIDS"), "Skids condensed");
  assert.equal(reasonWord("COUNT_CORRECTION"), "Count corrected");
  assert.equal(reasonWord("OTHER"), "Something else");
  for (const code of SHORT_REASONS) {
    const word = reasonWord(code);
    assert.ok(!word.includes(code), `${code} leaked its own code to a screen`);
  }
  assert.equal(reasonWord("SOMETHING"), "SOMETHING");
});

test("the screens draw the reasons as radio rows from the closed list, and no reason select remains", () => {
  assert.ok(viewsSource.includes("SHORT_REASONS.map"), "the rows are generated from the core's closed list");
  assert.ok(viewsSource.includes("data-reason=${code}"), "each row carries its code as the data-reason value");
  assert.ok(viewsSource.includes("${reasonWord(code)}"), "each row shows the picker's words, never the code");
  assert.ok(!viewsSource.includes("<option"), "a reason select survived: the reasons are radio rows now");
});

test("the shortage sheet banner reads in future tense, telling the picker what sending does", () => {
  assert.ok(
    viewsSource.includes("When you send, the line is backordered and the order's rep is told."),
    "the sheet banner uses future tense before send",
  );
});

test("the stage hint says the ref is generated for you, not made up", () => {
  assert.ok(
    viewsSource.includes("The ref is generated for you if you leave it blank."),
    "the stage hint names the action accurately",
  );
  assert.ok(
    !viewsSource.includes("The ref is made up for you"),
    "the old 'made up' wording is gone",
  );
});

test("the empty queue card says 'Nothing to pick here', not 'at this branch'", () => {
  assert.ok(
    viewsSource.includes("Nothing to pick here.</p>"),
    "the empty queue card drops the redundant 'at this branch'",
  );
  assert.ok(
    !viewsSource.includes("Nothing to pick at this branch."),
    "the old 'at this branch' wording is gone",
  );
});

// ---- recording what was pulled ----

test("short means the picked less the ordered, in the one place that decides it", () => {
  assert.equal(lineIsShort("10.0000", "9"), true);
  assert.equal(lineIsShort("10.0000", "10"), false);
  assert.equal(lineIsShort("x", "1"), false);
  assert.equal(shortOf("10.0000", "4"), "6");
  assert.equal(shortOf("10", "10"), "");
  assert.equal(shortOf("x", "1"), "");
});

test("a picked quantity allows zero and refuses blank, negative and nonsense", () => {
  assert.equal(validatePickQty("0"), "");
  assert.equal(validatePickQty(" 7 "), "");
  assert.notEqual(validatePickQty(""), "");
  assert.notEqual(validatePickQty("few"), "");
  assert.notEqual(validatePickQty("-1"), "");
});

test("the record is blocked until every short line carries a reason, and OTHER a note", () => {
  assert.equal(recordBlockedReason([{ qty_picked: "10", qty_ordered: "10", short_reason: "", short_note: "" }]), "");
  assert.equal(
    recordBlockedReason([{ qty_picked: "4", qty_ordered: "10", short_reason: "NOT_IN_YARD", short_note: "" }]),
    "",
  );
  assert.ok(recordBlockedReason([{ qty_picked: "4", qty_ordered: "10", short_reason: "", short_note: "" }]).includes("reason"));
  assert.ok(recordBlockedReason([{ qty_picked: "4", qty_ordered: "10", short_reason: "OTHER", short_note: "" }]).includes("note"));
  assert.ok(recordBlockedReason([{ qty_picked: "", qty_ordered: "10", short_reason: "", short_note: "" }]).includes("blank is not"));
});

test("the pick summary says what was pulled, not what is missing", () => {
  assert.equal(pickSummary([{ qty_ordered: "10", qty_picked: "10", uom: "EA", description: "a" }]), "All 1 line pulled in full.");
  assert.equal(
    pickSummary([
      { qty_ordered: "10", qty_picked: "7", uom: "EA", description: "pavers" },
      { qty_ordered: "2", qty_picked: "1", uom: "ROLL", description: "fabric" },
    ]),
    "7 of 10 EA pulled on pavers, and 1 other line came up short.",
  );
  assert.equal(pickSummary([]), "");
});

test("the short pick's answer words name the backorder and the rep", () => {
  assert.equal(
    shortPickWords({ qty_picked: "14.0000", qty_ordered: "24.0000", uom: "EA" }, "10.0000"),
    "Picked 14 of 24 EA. The rest (10 EA) is on backorder and the order's rep has been told.",
  );
  assert.equal(
    shortPickWords({ qty_picked: "14.0000", qty_ordered: "24.0000", uom: "EA" }, ""),
    "Picked 14 of 24 EA. The rest is on backorder and the order's rep has been told.",
  );
});

// ---- the shortage sheet ----

test("a shortage needs a positive number", () => {
  assert.equal(validateShortage("10"), "");
  assert.notEqual(validateShortage(""), "");
  assert.notEqual(validateShortage("few"), "");
  assert.notEqual(validateShortage("0"), "");
  assert.equal(
    shortageSentWords({ qty_short: "10.0000", uom: "SQFT", description: "Flagstone" }),
    "Shortage sent: 10 SQFT of Flagstone. The counter has been told.",
  );
});

// ---- staging ----

test("a stage without a spot is refused before the round trip", () => {
  assert.notEqual(validateStage(""), "");
  assert.equal(validateStage("Bay 3"), "");
  assert.notEqual(validateStage("x".repeat(121)), "");
});

// ---- skids ----

test("a skid count needs a whole number and a reason, and OTHER a note", () => {
  assert.equal(skidBlockedReason(15, "CONDENSED_SKIDS", ""), "");
  assert.equal(skidBlockedReason(0, "COUNT_CORRECTION", ""), "");
  assert.notEqual(skidBlockedReason(null, "DAMAGED", ""), "");
  assert.notEqual(skidBlockedReason(2, "", ""), "");
  assert.notEqual(skidBlockedReason(2, "OTHER", ""), "");
  assert.notEqual(skidBlockedReason(MAX_SKIDS + 1, "DAMAGED", ""), "");
  assert.equal(skidWords({ skid_count: 15, previous_skid_count: null, changed: true, order_no: "SO-1042" }), "SO-1042 now holds 15 skids.");
  assert.equal(skidWords({ skid_count: 5, previous_skid_count: 2, changed: true, order_no: "SO-1042" }), "SO-1042 changed from 2 to 5 skids.");
  assert.equal(skidWords({ skid_count: 5, previous_skid_count: 5, changed: false, order_no: "SO-1042" }), "Still 5 skids on SO-1042. Nothing changed.");
});

// ---- refusals, and the list wall ----

test("a refusal in the core's words is shown as it came, capped and stopped", () => {
  assert.equal(yardError("line 1 is short, give the reason (CONDENSED_SKIDS)", 400, "fallback"), "Line 1 is short, give the reason (CONDENSED_SKIDS).");
  assert.equal(yardError("tasks: branch out of scope", 403, "fallback"), "Branch out of scope.");
  assert.equal(yardError("anything", 500, "the fallback"), "the fallback");
  assert.equal(yardError("", undefined, "the fallback"), "the fallback");
  assert.equal(yardError("x", 401, "f"), "Your sign in has ended. Sign out and sign in again.");
  assert.equal(yardError("x", 404, "f"), "That is no longer on the board.");
});

test("a 403 that carries the server's own sentence shows it; only an empty one gets the generic words, which name who works here", () => {
  assert.equal(yardError("a branch-scoped caller must name a reachable branch_id", 403, "fallback"), "A branch-scoped caller must name a reachable branch_id.");
  assert.equal(yardError("only the yard manager may do that.", 403, "fallback"), "Only the yard manager may do that.");
  assert.equal(yardError("", 403, "fallback"), "You are not allowed to do that on this yard. Only the Yard Team and the Yard Manager work here.");
  // The bare gate body keeps the wall's words, never the generic ones.
  assert.equal(yardError("Forbidden", 403, "fallback"), "That is not yours to settle. Only the Yard Team and the Yard Manager may resolve a shortage.");
});

test("the role gate's bare Forbidden body is answered with the wall's plain words", () => {
  assert.equal(FORBIDDEN_BODY, "Forbidden");
  assert.deepEqual(RESOLVE_WALL_NAMES, ["Yard Team", "Yard Manager"]);
  assert.equal(wallWords(RESOLVE_WALL_NAMES), "That is not yours to settle. Only the Yard Team and the Yard Manager may resolve a shortage.");
  assert.equal(yardError("Forbidden", 403, "fallback"), "That is not yours to settle. Only the Yard Team and the Yard Manager may resolve a shortage.");
  assert.equal(yardError("Forbidden", 403, "fallback", { wall: ["Yard Manager"] }), "That is not yours to settle. Only the Yard Manager may resolve a shortage.");
});

// ---- the changes waiting on the yard (change orders, stage 2) ----

test("the yard's acknowledge is offered only when the server's next_actions name it", () => {
  assert.equal(canAcknowledge({ next_actions: ["yard_ack", "reject", "cancel"] }), true);
  assert.equal(canAcknowledge({ next_actions: ["reject", "cancel"] }), false);
  // An absent list offers nothing, never everything: the till's own fail closed rule.
  assert.equal(canAcknowledge({}), false);
  assert.equal(canAcknowledge(null), false);
  assert.equal(canAcknowledge(undefined), false);
});

test("a change's state reads in the words the till uses for the same states", () => {
  assert.equal(changeStatusWord("PENDING_YARD"), "With yard");
  assert.equal(changeStatusWord("PENDING_FINANCE"), "With finance");
  assert.equal(changeStatusWord("APPROVED"), "Approved");
  assert.equal(changeStatusWord("APPLIED"), "Applied");
  assert.equal(changeStatusWord("REJECTED"), "Rejected");
  assert.equal(changeStatusWord("CANCELLED"), "Withdrawn");
  assert.equal(changeStatusWord("SOMETHING_ELSE"), "something else");
});

test("the acknowledgement's words name the next stage from the server's answer, never a figure", () => {
  assert.equal(ackDoneWords({ status: "APPROVED" }), "Acknowledged. The change is approved and waits to be applied.");
  assert.equal(ackDoneWords({ status: "PENDING_FINANCE" }), "Acknowledged. The change now waits on finance.");
  assert.equal(ackDoneWords({ status: "APPLIED" }), "Acknowledged. Applied.");
});

test("the acknowledge wall names who may act, in the till's own shared sentence", () => {
  assert.equal(CHANGE_ACK_WALL, "Only the yard team or the yard manager may acknowledge a change for the yard.");
  assert.equal(yardError("Forbidden", 403, "fallback", { wallSentence: CHANGE_ACK_WALL }), CHANGE_ACK_WALL);
  // Without the override the bare body keeps the shortage wall's words, as before.
  assert.equal(yardError("Forbidden", 403, "fallback"), "That is not yours to settle. Only the Yard Team and the Yard Manager may resolve a shortage.");
});

// ---- the pick record door's client rules (item 6c) ----

test("the client's bound on a record is the core's (tasks.maxPickedLines, read from the Go source)", () => {
  assert.equal(MAX_PICKED_LINES, goMaxPickedLines());
});

test("pickRecordProblem names what the door would refuse, in words, and lets a clean record through", () => {
  const line = { order_line_no: 1, product_id: "", sku: "A", description: "d", qty_ordered: "4", qty_picked: "4", uom: "EA", short_reason: "", short_note: "" };
  assert.equal(pickRecordProblem([line]), "");
  assert.match(pickRecordProblem([]), /Say what was picked/);
  assert.match(pickRecordProblem(Array.from({ length: MAX_PICKED_LINES + 1 }, () => line)), /more lines than any one ticket carries/);
  assert.match(pickRecordProblem([{ ...line, short_note: "a\u0000" }]), /Line 1: the note cannot hold control characters/);
  assert.match(pickRecordProblem([{ ...line, sku: "a\n" }]), /the sku cannot hold control characters/);
  assert.match(pickRecordProblem([{ ...line, description: "a\u202e" }]), /the description cannot hold control characters/);
  assert.equal(pickRecordProblem([{ ...line, short_note: "a\n\tb\r", description: "x\ny" }]), "", "a note and a description keep tab, LF and CR");
});

test("a line is local while only this device holds it, and the queued words do not claim the rep was told", () => {
  assert.equal(isLocalLine({ id: "local:t:1" }), true);
  assert.equal(isLocalLine({ id: "2a000000-0000-4000-9000-000000000001" }), false);
  const words = pickQueuedWords([{ qty_ordered: "6", qty_picked: "4" }, { qty_ordered: "2", qty_picked: "2" }]);
  assert.match(words, /saved on this device/i);
  assert.match(words, /when it sends/);
  assert.doesNotMatch(words, /has been told/);
});

// ---- the refusal of a record onto a closed pick (item 6c, FROM-PRINCIPAL 49) ----

test("the sentences the client matches are the core's (yard_short.go pickClosedError), so a wording change there turns this red", () => {
  const goSource = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "..", "..", "..", "..", "core", "internal", "tasks", "yard_short.go"), "utf8");
  assert.ok(goSource.includes(`"tasks: ${CORE_PICK_DONE}"`), "the done sentence is the core's");
  assert.ok(goSource.includes(`"tasks: ${CORE_PICK_CANCELLED}"`), "the cancelled sentence is the core's");
});

test("a record refused on a handed over or cancelled pick says what the picker must tell the counter", () => {
  assert.equal(
    recordRefusalWords(`tasks: ${CORE_PICK_DONE}`),
    "This pick was already handed over, so your short was not saved. Tell the counter: the order still bills the full quantity.",
  );
  assert.equal(recordRefusalWords(`tasks: ${CORE_PICK_CANCELLED}`), "This pick was cancelled, so your record was not saved. Tell the counter.");
  assert.equal(recordRefusalWords("line 1: that is more than was ordered"), "line 1: that is more than was ordered", "other refusals keep the core's words");
});

// ---- a held card is never the cached word (row 2122, P2-1) ----

test("a cached held card is released after the re-read: the server's ticket wins, and an unanswered read keeps the cached card", async () => {
  const { rereadHeldPicks } = await import("../src/yard-logic.ts");
  const pick = (id, held) => ({
    task: { id, status: "QUEUED", claimed_by: "" },
    ticket: { order_id: `o-${id}`, held_from_picking: held, hold_words: held ? "Balance due" : "", lines: [] },
    shortages: [],
    picked: [],
  });
  const cached = [pick("a", true), pick("b", false), pick("c", true), pick("d", true)];
  const asked = [];
  const answers = {
    a: { ok: true, value: pick("a", false) }, // paid since: released
    c: { ok: true, value: pick("c", true) }, // still held
    d: { ok: false, status: 0, error: "offline" }, // unreachable: the cached card stays
  };
  const next = await rereadHeldPicks(cached, async (id) => {
    asked.push(id);
    return answers[id];
  });
  assert.deepEqual(asked.sort(), ["a", "c", "d"], "only the held cards are re-read");
  assert.equal(next[0].ticket.held_from_picking, false, "the paid hold shows Claim again");
  assert.equal(next[1], cached[1], "an unheld card is untouched");
  assert.equal(next[2].ticket.held_from_picking, true);
  assert.equal(next[3], cached[3], "offline the card still says held and the server's claim door decides");
  assert.equal(next.length, cached.length);
});
