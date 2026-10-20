// The dispatch board's rules, pinned: the lifecycle matrix, the capacity bands, the quick
// cards' filters, the keyboard path's move projection, the bulk load words, the reschedule
// check and the sentences. Every case is a rule that would be quietly wrong on screen.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  AMBER_FROM,
  asOfLabel,
  assignTargets,
  askDiffers,
  barPct,
  bandWord,
  bulkLoadLabel,
  bulkLoadOutcome,
  capacityBand,
  cardCount,
  cardMatches,
  cardScopeLabel,
  commitBlockedReason,
  dayOfLabel,
  filterBoard,
  fmtNum,
  fmtWeight,
  formatTime,
  shortDay,
  toCuYd,
  toWeight,
  weightUnit,
  formatDue,
  groupByStatus,
  holdWords,
  laneScopeNote,
  loadableStops,
  moveAnnouncement,
  plainSentence,
  pruneSelection,
  refusalWords,
  rescheduleBlockedReason,
  reopenNotice,
  rowEditable,
  shiftISODate,
  stopActions,
  stopBlockedReason,
  stopNextStates,
  stopWord,
  targetLine,
  unloadableSummary,
  driverBody,
  driverLine,
  fleetStateWord,
  SCAN_SEQUENCE,
  centsText,
  dollarsToCents,
  raiseLine,
  raiseStateWord,
  splitBody,
  splitNotice,
  splitRefusal,
  validateSplit,
  historyFaultWords,
  manifestNotice,
  manifestRefusal,
  scanLoadLine,
  scanStageLabel,
  trailSummary,
  historyRow,
  policyBody,
  policyRefusal,
  policyRows,
  scheduleBody,
  scheduleNotice,
  scheduleRefusal,
  validateReason,
  truckBody,
  volumeText,
  capacityText,
  truckLine,
  willCallLine,
  willCallNotice,
  willCallRefusal,
  willCallWord,
  validateDriver,
  validateOrderRef,
  validatePolicyFields,
  validateReschedule,
  validateSchedule,
  validateTruck,
  worstBand,
} from "../src/dispatch-logic.ts";

const stop = (over = {}) => ({
  id: "s1",
  branch_id: "b",
  order_id: "o",
  order_no: "ORD-1",
  stop_no: 1,
  scheduled_date: "2026-01-15",
  status: "READY",
  photo_count: 0,
  created_at: "2026-01-01T00:00:00Z",
  commit_state: "REQUESTED",
  ...over,
});
const commit = (over = {}) => ({ committed: false, reopenable: true, can_reopen: false, can_commit: true, due_to_commit: false, prompt_hours: 48, reopened: false, ...over });
const gauge = (over = {}) => ({
  stop_count: 1, weight_kg: 1000, capacity_kg: 9000, weight_band: "GREEN", weighed_stops: 1,
  measured_stops: 0, volume_band: "UNKNOWN", pallet_band: "UNKNOWN", band: "GREEN", ...over,
});
const row = (runId, stops = [], over = {}) => ({
  run: { id: runId, run_no: `RUN-${runId}`, branch_id: "b", scheduled_date: "2026-01-15", truck_code: `T-${runId}`, status: "PLANNED", stop_count: stops.length, created_at: "x" },
  gauge: gauge(),
  commit: commit(),
  stops,
  ...over,
});
const view = (over = {}) => ({
  branch_id: "b", date: "2026-01-15", policy: { reopen_policy: "MANAGER", commit_prompt_hours: 48, load_value_visibility: "MANAGER_UP", commit_horizon_weeks: 4, working_view_days: 14, freight_split_mode: "EVEN", driver_session_hours: 12 },
  value_visible: false, gaps_checked: true, can_commit: true, can_reopen: false, working_from: "2026-01-15", working_to: "2026-01-28", working_days: 14,
  counts: { deliveries: 4, pickups: 3, pickups_awaiting_pick: 2, pickups_ready_to_collect: 1, unassigned: 2, ready: 3, requested: 2, committed: 1, due: 1 },
  trucks: [], loads: [], staging: [], ...over,
});

test("the lifecycle matrix offers only legal moves and never everything for an unknown status", () => {
  assert.deepEqual(stopNextStates("READY"), ["LOADED", "CANCELLED"]);
  assert.deepEqual(stopNextStates("DELIVERED"), []);
  assert.deepEqual(stopNextStates("SOMETHING_NEW"), []);
  assert.deepEqual(stopActions("READY", { assigned: true }).map((a) => a.action), ["LOADED", "CANCELLED"]);
});

test("a stop on no run is never offered Mark loaded", () => {
  assert.deepEqual(stopActions("READY", { assigned: false }).map((a) => a.action), ["CANCELLED"]);
});

test("delivered is not an action: the matrix names the proof of delivery the driver signs", () => {
  const actions = stopActions("EN_ROUTE");
  assert.deepEqual(actions.map((a) => a.action), ["POD", "FAILED"]);
  assert.equal(actions[0].label, "Proof of delivery");
  assert.equal(actions[1].destructive, true);
});

test("a state read inside a sentence has no underscore and no capital, and an unmapped one degrades honestly", () => {
  assert.equal(stopWord("EN_ROUTE"), "en route");
  assert.equal(stopWord("A_NEW_STATE"), "a new state");
});

test("an unknown capacity is unknown, never green", () => {
  assert.equal(capacityBand(500, null), "UNKNOWN");
  assert.equal(capacityBand(500, 0), "UNKNOWN");
  assert.equal(capacityBand(null, 9000), "UNKNOWN");
  assert.equal(capacityBand(-1, 9000), "UNKNOWN");
});

test("the bands grade at the amber line and above full", () => {
  assert.equal(AMBER_FROM, 0.85);
  assert.equal(capacityBand(8000, 10000), "GREEN");
  assert.equal(capacityBand(8500, 10000), "AMBER");
  assert.equal(capacityBand(10000, 10000), "AMBER");
  assert.equal(capacityBand(10001, 10000), "RED");
});

test("the worst band folds pessimistically and a bar never draws past its track", () => {
  assert.equal(worstBand("GREEN", "UNKNOWN", "RED", "AMBER"), "RED");
  assert.equal(worstBand(), "UNKNOWN");
  assert.equal(barPct(13000, 10000), 100);
  assert.equal(barPct(2500, 10000), 25);
  assert.equal(barPct(null, 10000), 0);
  assert.equal(bandWord("UNKNOWN"), "Not measured");
});

test("numbers read at arm's length and an absent one says so in words", () => {
  assert.equal(fmtNum(1234.56), "1,235");
  assert.equal(fmtNum(12.34), "12.3");
  assert.equal(fmtNum(null), "Not counted");
});

test("a requested date reads as an ask and the ask rides beside a different promise", () => {
  assert.equal(askDiffers({ requested_date: "2026-01-14", scheduled_date: "2026-01-15", commit_state: "COMMITTED" }), true);
  assert.equal(askDiffers({ requested_date: "2026-01-14", scheduled_date: "2026-01-15", commit_state: "REQUESTED" }), false);
  assert.equal(askDiffers({ scheduled_date: "2026-01-15", commit_state: "COMMITTED" }), false);
});

test("a committed load is editable only by a reader who could reopen it, and only while planned", () => {
  assert.equal(rowEditable(row("a")), true);
  assert.equal(rowEditable(row("a", [], { commit: commit({ committed: true, can_reopen: false }) })), false);
  assert.equal(rowEditable(row("a", [], { commit: commit({ committed: true, can_reopen: true }) })), true);
  const dispatched = row("a");
  dispatched.run.status = "DISPATCHED";
  assert.equal(rowEditable(dispatched), false);
});

test("the blocked reason names the way forward only when there is one", () => {
  assert.match(commitBlockedReason(row("a", [], { commit: commit({ committed: true, reopenable: true }) })), /A dispatch manager can reopen it/);
  assert.match(commitBlockedReason(row("a", [], { commit: commit({ committed: true, reopenable: false }) })), /does not reopen a committed load/);
  const done = row("a");
  done.run.status = "DISPATCHED";
  assert.equal(commitBlockedReason(done), "This load is dispatched. It is a record now, not a plan.");
  assert.equal(commitBlockedReason(row("a")), "");
});

test("a load drawn without a truck row is graded by its own state and the caller flag, never the policy alone", () => {
  const v = view();
  assert.equal(stopBlockedReason({ commit_state: "REQUESTED" }, v), "");
  assert.match(stopBlockedReason({ commit_state: "COMMITTED" }, v), /A dispatch manager can reopen it/);
  assert.equal(stopBlockedReason({ commit_state: "COMMITTED" }, view({ can_reopen: true })), "");
  const nobody = view({ policy: { ...v.policy, reopen_policy: "NOBODY" } });
  assert.match(stopBlockedReason({ commit_state: "COMMITTED" }, nobody), /does not reopen/);
  assert.equal(stopBlockedReason({ commit_state: "COMMITTED" }, null), "");
});

test("a stop that names a run is graded by that run's row, the same answer as the truck lane", () => {
  const v = view({ loads: [row("a", [], { commit: commit({ committed: true, reopenable: false }) })] });
  assert.match(stopBlockedReason({ commit_state: "REQUESTED", run_id: "a" }, v), /does not reopen/);
});

test("the quick cards read the server's counts and add nothing up", () => {
  const v = view();
  assert.equal(cardCount(v, "card.deliveries"), 4);
  assert.equal(cardCount(v, "card.due"), 1);
  assert.equal(cardCount(null, "card.due"), 0);
});

test("each card's filter agrees with its meaning, and the pickups card matches no stop", () => {
  assert.equal(cardMatches("card.unassigned", stop(), false), true);
  assert.equal(cardMatches("card.unassigned", stop({ run_id: "a" }), false), false);
  assert.equal(cardMatches("card.ready", stop({ status: "LOADED" }), false), false);
  assert.equal(cardMatches("card.requested", stop(), false), true);
  assert.equal(cardMatches("card.committed", stop(), true), true);
  assert.equal(cardMatches("card.due", stop(), false), true);
  assert.equal(cardMatches("card.due", stop({ commit_state: "COMMITTED" }), false), false);
  assert.equal(cardMatches("card.pickups", stop(), false), false);
});

test("a card's scope is named: the day, the working window or open now", () => {
  const v = view();
  assert.equal(cardScopeLabel(v, "card.ready"), "this day");
  assert.equal(cardScopeLabel(v, "card.unassigned"), "next 14 days");
  assert.equal(cardScopeLabel(v, "card.pickups"), "open now");
  const stamp = new Date(Date.UTC(2026, 0, 15, 15, 4, 0)).toISOString();
  assert.equal(formatTime(stamp, "America/Los_Angeles"), "7:04 AM");
  assert.equal(formatTime(stamp, "America/Los_Angeles").includes("\u202f"), false);
  assert.equal(asOfLabel(stamp, "America/Los_Angeles", "2026-01-15"), "As of 7:04 AM");
  assert.equal(asOfLabel(stamp, "America/New_York", "2026-01-15"), "As of 10:04 AM");
  assert.equal(asOfLabel("2026-03-02T03:15:00Z", "America/Los_Angeles", "2026-03-02"), "As of Mar 1, 2026, 7:15 PM");
  assert.equal(asOfLabel("2026-03-02T16:15:00Z", "America/Los_Angeles", "2026-03-02"), "As of 8:15 AM");
  assert.equal(asOfLabel(""), "");
});

test("a day card holds its drill down to the board's day, and an absent date stays", () => {
  const v = view({
    staging: [stop({ id: "today" }), stop({ id: "later", scheduled_date: "2026-01-20" }), stop({ id: "nodate", scheduled_date: "" })],
  });
  assert.deepEqual(filterBoard(v, "card.requested").staging.map((s) => s.id), ["today", "nodate"]);
  assert.deepEqual(filterBoard(v, "card.unassigned").staging.map((s) => s.id), ["today", "later", "nodate"]);
  assert.equal(filterBoard(v, ""), v);
});

test("the committed card keeps a truck whose load is committed and drops one that is not", () => {
  const v = view({ loads: [row("a", [stop()], { commit: commit({ committed: true }) }), row("b", [stop({ id: "s2" })])] });
  assert.deepEqual(filterBoard(v, "card.committed").loads.map((r) => r.run.id), ["a"]);
});

test("a lane says it holds another day's work only when it does", () => {
  assert.equal(laneScopeNote([stop()], "2026-01-15", 14), "");
  assert.equal(laneScopeNote([stop({ scheduled_date: "2026-01-16" })], "2026-01-15", 14), "next 14 days");
  assert.equal(dayOfLabel(stop({ scheduled_date: "2026-01-16" }), "2026-01-15"), "Jan 16");
  assert.equal(dayOfLabel(stop(), "2026-01-15"), "");
});

test("the move list projects each truck's gauge with the load on it, and blocks with a reason", () => {
  const heavy = stop({ id: "m", weight_kg: 8000 });
  const v = view({
    loads: [
      row("a", [], { gauge: gauge({ weight_kg: 1000, capacity_kg: 9000 }) }),
      row("b", [], { gauge: gauge({ weight_kg: 5000, capacity_kg: 9000 }), commit: commit({ committed: true, reopenable: true }) }),
      row("c", [heavy], { gauge: gauge({ weight_kg: 8000, capacity_kg: 9000 }) }),
    ],
  });
  const targets = assignTargets(v, { ...heavy, run_id: "c" });
  assert.equal(targets[0].bandAfter, "AMBER");
  assert.equal(targets[0].weightAfter, 9000);
  assert.equal(targets[0].blocked, "");
  assert.match(targets[1].blocked, /A dispatch manager can reopen it/);
  assert.equal(targets[2].blocked, "Already on this load");
  assert.equal(targetLine(targets[0]), "9.9 of 9.9 tons · Near full");
  assert.equal(targetLine(targets[2]), "Already on this load");
});

test("a truck on another day is blocked with the two dates", () => {
  const v = view({ loads: [row("a")] });
  v.loads[0].run.scheduled_date = "2026-01-16";
  const [target] = assignTargets(v, stop());
  assert.equal(target.blocked, "This truck runs on 2026-01-16, the delivery is for 2026-01-15.");
});

test("an unweighed load adds nothing and does not turn a total into zero", () => {
  const v = view({ loads: [row("a", [], { gauge: gauge({ weight_kg: null, capacity_kg: 9000 }) })] });
  const [target] = assignTargets(v, stop({ weight_kg: null }));
  assert.equal(target.weightAfter, null);
  assert.equal(target.bandAfter, "UNKNOWN");
});

test("the announcement repeats the fact that changed", () => {
  assert.equal(moveAnnouncement(stop(), null), "ORD-1 taken off its truck. It is back in the staging lane.");
  const v = view({ loads: [row("a", [], { gauge: gauge({ weight_kg: 1000, capacity_kg: 9000 }) })] });
  const [target] = assignTargets(v, stop({ weight_kg: 500 }));
  assert.equal(moveAnnouncement(stop(), target), "ORD-1 moved to RUN-a · T-a. 1.7 of 9.9 tons, room.");
});

test("a bulk selection prunes the loads the board no longer draws and keeps the rest", () => {
  assert.deepEqual(pruneSelection(["a", "b", "c"], [{ id: "b" }, { id: "c" }]), ["b", "c"]);
  assert.deepEqual(pruneSelection([], [{ id: "b" }]), []);
});

test("the status axis lanes the same data, in the order a day runs, with an other lane", () => {
  const v = view({
    staging: [stop({ id: "s1" }), stop({ id: "s2", status: "FAILED" })],
    loads: [row("a", [stop({ id: "s3", status: "LOADED", run_id: "a" }), stop({ id: "s4", status: "EN_ROUTE", run_id: "a" })])],
  });
  const lanes = groupByStatus(v);
  assert.deepEqual(lanes.READY.map((s) => s.id), ["s1"]);
  assert.deepEqual(lanes.LOADED.map((s) => s.id), ["s3"]);
  assert.deepEqual(lanes.EN_ROUTE.map((s) => s.id), ["s4"]);
  assert.deepEqual(lanes.OTHER.map((s) => s.id), ["s2"]);
});

test("the bulk bar offers only the loads the matrix allows and says why for the rest, never a guess", () => {
  const stops = [
    stop({ id: "a", run_id: "r1" }),
    stop({ id: "b" }),
    stop({ id: "c", run_id: "r1", status: "LOADED" }),
    stop({ id: "d", run_id: "r1", status: "DELIVERED" }),
  ];
  const picked = new Set(["a", "b", "c", "d"]);
  assert.deepEqual(loadableStops(stops, picked).map((s) => s.id), ["a"]);
  assert.equal(unloadableSummary(stops, picked), "1 is not on a truck yet, 1 is already loaded, 1 cannot be loaded from here");
  assert.equal(unloadableSummary(stops, new Set(["a"])), "");
});

test("the bulk button names how many loads and how many trucks", () => {
  assert.equal(bulkLoadLabel([]), "");
  assert.equal(bulkLoadLabel([stop({ run_id: "r1" })]), "Mark 1 load loaded");
  assert.equal(bulkLoadLabel([stop({ run_id: "r1" }), stop({ id: "x", run_id: "r1" })]), "Mark 2 loads loaded");
  assert.equal(bulkLoadLabel([stop({ run_id: "r1" }), stop({ id: "x", run_id: "r2" })]), "Mark 2 loads loaded across 2 trucks");
});

test("a bulk outcome counts what took and names each refusal with its load", () => {
  const out = bulkLoadOutcome([
    { stop_id: "a", order_no: "ORD-1", loaded: true },
    { stop_id: "b", order_no: "ORD-2", loaded: false, refusal: "dispatch: this order is held for payment" },
    { stop_id: "c", loaded: false },
  ]);
  assert.equal(out.notice, "1 load marked loaded.");
  assert.deepEqual(out.refusals, ["ORD-2: this order is held for payment", "A load: did not move"]);
  assert.equal(bulkLoadOutcome([{ stop_id: "b", order_no: "ORD-2", loaded: false, refusal: "no" }]).notice, "");
});

test("the dates stay in the local calendar", () => {
  assert.equal(shiftISODate("2026-01-31", 1), "2026-02-01");
  assert.equal(shiftISODate("2026-03-01", -1), "2026-02-28");
  assert.equal(shiftISODate("garbage", 1), "garbage");
});

test("the reschedule check refuses what the server refuses, in the order it asks", () => {
  const ok = (to, why = "Customer asked") => validateReschedule(to, why, "2026-01-15", "2026-01-15");
  assert.equal(ok(""), "Pick a delivery date.");
  assert.equal(ok("15/01/2026"), "Pick a delivery date.");
  assert.equal(ok("2026-01-15"), "That is already the scheduled date.");
  assert.match(ok("2026-01-10"), /has passed/);
  assert.match(ok("2026-01-20", "  "), /Say why/);
  assert.match(ok("2026-01-20", "x".repeat(401)), /under 400/);
  assert.equal(ok("2026-01-20"), "");
});

test("a reason is required and bounded", () => {
  assert.match(validateReason(" "), /Say why/);
  assert.equal(validateReason("A better fit turned up"), "");
  assert.match(validateReason("x".repeat(401)), /under 400/);
});

test("reschedule is blocked with the reason the stop is past planning or already on a run", () => {
  assert.equal(rescheduleBlockedReason({ status: "READY" }), "");
  assert.equal(rescheduleBlockedReason({ status: "EN_ROUTE" }), "This delivery is en route. Its date is a record now, not a plan.");
  assert.match(rescheduleBlockedReason({ status: "READY", run_id: "r", run_no: "RUN-7" }), /on run RUN-7/);
});

test("a payment hold is a hold in words, with an amount only when the core sent one", () => {
  assert.equal(holdWords(null), "");
  assert.equal(holdWords({ held: false, reason: "AR" }), "");
  assert.equal(holdWords({ held: true }), "Payment is due on this order before it can be delivered.");
  assert.equal(holdWords({ held: true, reason: "payment needed before release, call the office" }), "Payment needed before release, call the office.");
  assert.equal(holdWords({ held: true, reason: "Balance open.", amount_due_cents: 125050 }), "Balance open. $1,250.50 due.");
  assert.equal(holdWords({ held: true, reason: "Balance open.", amount_due_cents: 0 }), "Balance open.");
  assert.equal(formatDue(0), "");
  assert.equal(formatDue(undefined), "");
});

test("a server sentence keeps its words and loses only its module prefix", () => {
  assert.equal(plainSentence("dispatch: the run still has stops that are not finished"), "the run still has stops that are not finished");
  assert.equal(plainSentence("Point Loma: gate locked"), "Point Loma: gate locked");
  assert.equal(plainSentence(null), "");
});

test("a refusal reads in words by status, and a transport error says the board could not reach the desk", () => {
  assert.match(refusalWords("The reopen", "", null), /could not reach the desk/);
  assert.match(refusalWords("The reopen", "", 401), /sign-in ended/);
  assert.equal(refusalWords("The reopen", "dispatch: only a dispatch manager can reopen a committed load", 403), "only a dispatch manager can reopen a committed load");
  assert.equal(refusalWords("Auto assign", "dispatch: automatic load assignment is part of the Pro module, build the loads here in the meantime", 501), "automatic load assignment is part of the Pro module, build the loads here in the meantime");
  assert.match(refusalWords("The reopen", "", 403), /Logistics Manager or a Branch Manager can/);
  assert.match(refusalWords("The reopen", "Forbidden", 403), /Logistics Manager or a Branch Manager can/, "the gate's bare word is no sentence");
  assert.match(refusalWords("The reopen", "forbidden", 403), /Logistics Manager or a Branch Manager can/);
  assert.equal(refusalWords("The reopen", "Forbidden", 409), "Forbidden", "only a 403 gets the fallback");
  assert.match(refusalWords("The reopen", "", 500), /nothing changed/);
  assert.match(refusalWords("The reopen", "internal error", 500), /nothing changed/, "a server fault is not shown as the server's raw word");
});

test("weights show in pounds and short tons for the San Diego yard, converted at display only", () => {
  assert.equal(toWeight(1000, "lb").toFixed(1), "2204.6");
  assert.equal(toWeight(9000, "tons").toFixed(2), "9.92");
  assert.equal(fmtWeight(3400), "3.7 tons");
  assert.equal(fmtWeight(1500), "3,307 lb");
  assert.equal(fmtWeight(1100), "2,425 lb");
  assert.equal(fmtWeight(9000), "9.9 tons");
  assert.equal(fmtWeight(null), "Not weighed");
  assert.equal(weightUnit(1100, 900), "lb");
  assert.equal(weightUnit(1100, 5000), "tons", "one unit for both figures of a gauge, chosen by the larger");
  assert.equal(weightUnit(null, undefined), "lb");
  assert.equal(toCuYd(1).toFixed(3), "1.308");
});

test("a short day reads as month and day, one form for pills and strips", () => {
  assert.equal(shortDay("2026-10-13"), "Oct 13");
  assert.equal(dayOfLabel(stop({ scheduled_date: "2026-01-16" }), "2026-01-15"), "Jan 16");
  assert.equal(shortDay("garbage"), "garbage");
});

// ---- the schedule form (part B, sub item 1) -------------------------------------------------------

const form = (over = {}) => ({
  order: "ORD-0040", date: "2026-01-16", window: "", contact: "", phone: "", address: "", city: "", postal: "", lat: "", lon: "", instructions: "", ...over,
});

test("the schedule form wants an order, a day that has not gone, and coordinates in pairs", () => {
  assert.equal(validateSchedule(form(), "2026-01-15"), "");
  assert.match(validateSchedule(form({ order: "  " }), "2026-01-15"), /Enter an order number/);
  assert.match(validateSchedule(form({ date: "" }), "2026-01-15"), /Pick a delivery date/);
  assert.match(validateSchedule(form({ date: "2026-01-14" }), "2026-01-15"), /That date has passed/);
  assert.equal(validateSchedule(form({ date: "2026-01-15" }), "2026-01-15"), "", "today is a day that has not gone");
  assert.match(validateSchedule(form({ lat: "32.7" }), "2026-01-15"), /Give both coordinates, or neither/);
  assert.match(validateSchedule(form({ lon: "-117.2" }), "2026-01-15"), /Give both coordinates, or neither/);
  assert.match(validateSchedule(form({ lat: "abc", lon: "-117.2" }), "2026-01-15"), /Coordinates are numbers/);
  assert.match(validateSchedule(form({ lat: "91", lon: "-117.2" }), "2026-01-15"), /Latitude is between -90 and 90/);
  assert.match(validateSchedule(form({ lat: "32.7", lon: "181" }), "2026-01-15"), /Longitude is between -180 and 180/);
  assert.equal(validateSchedule(form({ lat: "32.7", lon: "-117.2" }), "2026-01-15"), "");
});

test("the schedule body is the whole wire shape: trimmed text, null for the coordinates left out, and no commitment asked", () => {
  const body = scheduleBody(form({ order: "  #ORD-0040 ", window: " AM ", contact: "Site contact", address: " 7420 Prospect Lane ", lat: "32.8", lon: "-117.25" }));
  assert.equal(body.order_id, "#ORD-0040");
  assert.equal(body.scheduled_date, "2026-01-16");
  assert.equal(body.time_window, "AM");
  assert.equal(body.address_line, "7420 Prospect Lane");
  assert.equal(body.latitude, 32.8);
  assert.equal(body.longitude, -117.25);
  const bare = scheduleBody(form());
  assert.equal(bare.latitude, null);
  assert.equal(bare.longitude, null);
  assert.equal(bare.weight_kg, null);
  assert.equal(bare.volume_m3, null);
  assert.equal(bare.pallet_positions, null);
  assert.equal(bare.commit_state, "REQUESTED", "a date asked for here is a request; putting a load on a truck is the commitment");
  assert.equal(bare.requested_date, "");
});

test("the schedule notice names the order and the day and says the date is still a request", () => {
  assert.equal(scheduleNotice({ order_no: "ORD-0040", scheduled_date: "2026-01-16" }), "ORD-0040 scheduled for Fri, Jan 16, 2026. It is a request until it is on a truck.");
  assert.equal(scheduleNotice({ scheduled_date: "2026-01-16" }), "The order scheduled for Fri, Jan 16, 2026. It is a request until it is on a truck.");
});

test("a schedule refusal keeps the core's sentence, and a missing order says what to check", () => {
  assert.match(scheduleRefusal("dispatch: not found", 404), /No order matches that number at this yard/);
  assert.equal(scheduleRefusal("dispatch: confirm the order before scheduling a delivery for it", 409), "confirm the order before scheduling a delivery for it");
  assert.match(scheduleRefusal("", 500), /nothing changed/);
  assert.match(scheduleRefusal("Forbidden", 403), /not yours to do/);
});

// ---- the trucks and drivers registry (part B, sub item 2) ---------------------------------------------

const tf = (over = {}) => ({ code: "Truck 05", name: "Truck 05, stake body", type: "FLATBED", capacity: "", volume: "", pallets: "", ...over });
const truck = (over = {}) => ({ id: "t1", branch_id: "b1", code: "Truck 01", name: "Flatbed", truck_type: "FLATBED", capacity_kg: 9000, is_active: true, ...over });

test("a truck wants a code, a known type, and capacities that are plain non negative numbers", () => {
  assert.equal(validateTruck(tf()), "");
  assert.match(validateTruck(tf({ code: "  " })), /A truck needs a code/);
  assert.match(validateTruck(tf({ type: "SPACESHIP" })), /Pick a truck type/);
  assert.match(validateTruck(tf({ capacity: "heavy" })), /Capacity is a number of pounds/);
  assert.match(validateTruck(tf({ capacity: "-5" })), /Capacity cannot be negative/);
  assert.match(validateTruck(tf({ volume: "-1" })), /Volume cannot be negative/);
  assert.match(validateTruck(tf({ volume: "x" })), /Volume is a number of cubic yards/);
  assert.match(validateTruck(tf({ pallets: "2.5" })), /Pallet positions are a whole number/);
  assert.match(validateTruck(tf({ pallets: "-1" })), /Pallet positions are a whole number/);
  assert.equal(validateTruck(tf({ capacity: "20000", volume: "12.5", pallets: "10" })), "");
});

test("a truck body is the whole row: pounds become kilograms at the wire, and a capacity left as shown is the stored figure", () => {
  const made = truckBody(tf({ capacity: "20000", volume: "10", pallets: "8" }), undefined, "b1");
  assert.equal(made.id, "");
  assert.equal(made.branch_id, "b1");
  assert.equal(made.code, "Truck 05");
  assert.equal(made.truck_type, "FLATBED");
  assert.equal(made.capacity_kg, 9071.85);
  assert.equal(made.capacity_m3, 7.65);
  assert.equal(made.pallet_positions, 8);
  assert.equal(made.is_active, true);
  const blank = truckBody(tf(), undefined, "b1");
  assert.equal(blank.capacity_kg, null);
  assert.equal(blank.capacity_m3, null);
  assert.equal(blank.pallet_positions, null);
  // The edit sheet shows 9000 kg as 19,842 lb; saving without touching it must not drift the stored figure.
  const base = truck({ capacity_kg: 9000, capacity_m3: 7.65 });
  const kept = truckBody(tf({ code: "Truck 01", capacity: "19842", volume: "10" }), base, "b1");
  assert.equal(kept.id, "t1");
  assert.equal(kept.capacity_kg, 9000);
  assert.equal(kept.capacity_m3, 7.65);
  const changed = truckBody(tf({ capacity: "20000", volume: "10" }), base, "b1");
  assert.equal(changed.capacity_kg, 9071.85);
  const out = truckBody(tf({ capacity: "19842" }), truck({ is_active: false }), "b1", false);
  assert.equal(out.is_active, false);
});

test("a whole figure typed reads back as typed after the first save, over a range of yards and pounds", () => {
  for (const yards of [1, 2, 6, 8, 10, 12, 14, 16, 18, 20, 25, 30, 40]) {
    const body = truckBody(tf({ volume: String(yards) }), undefined, "b1");
    assert.equal(volumeText(body.capacity_m3), String(yards), `${yards} cu yd`);
  }
  for (const lb of [9999, 12345, 20000, 26000, 40000]) {
    const body = truckBody(tf({ capacity: String(lb) }), undefined, "b1");
    assert.equal(capacityText(body.capacity_kg), String(lb), `${lb} lb`);
  }
});

test("a truck reads as facts in the yard's units, and an unmeasured truck says so", () => {
  assert.deepEqual(truckLine(truck({ capacity_kg: 9000, capacity_m3: 10, pallet_positions: 12 })), ["9.9 tons", "13.1 cu yd", "12 pallet positions"]);
  assert.deepEqual(truckLine(truck({ capacity_kg: 1100 })), ["2,425 lb"]);
  assert.deepEqual(truckLine(truck({ capacity_kg: null })), ["Capacity not measured"]);
  assert.equal(fleetStateWord(true), "In use");
  assert.equal(fleetStateWord(false), "Out of use");
});

test("a driver wants a name, and a login link that is a user id or nothing", () => {
  const df = (over = {}) => ({ name: "Driver D (demo)", phone: "", licence: "", login: "", ...over });
  assert.equal(validateDriver(df()), "");
  assert.match(validateDriver(df({ name: " " })), /A driver needs a name/);
  assert.match(validateDriver(df({ login: "not-an-id" })), /A driver's sign in account is a long id with dashes/);
  assert.equal(validateDriver(df({ login: "d0200000-0000-4000-9000-000000000001" })), "");
  const body = driverBody(df({ name: " Driver D ", phone: " 619-555-0144 ", licence: "B" }), undefined, "b1");
  assert.deepEqual(body, { id: "", branch_id: "b1", name: "Driver D", phone: "619-555-0144", licence_class: "B", user_id: "", is_active: true });
  const kept = driverBody(df({ name: "Driver A" }), { id: "d1", branch_id: "b1", name: "x", is_active: false }, "b1", true);
  assert.equal(kept.id, "d1");
  assert.equal(kept.is_active, true);
  assert.equal(driverLine({ id: "d1", branch_id: "b", name: "x", is_active: true, licence_class: "A", phone: "619-555-0141", user_id: "u" }).join(" | "), "Licence A | 619-555-0141 | Has a sign in account");
  assert.equal(driverLine({ id: "d1", branch_id: "b", name: "x", is_active: true }).join(" | "), "No licence on file | No phone | No sign in account");
});

// ---- will call (part B, sub item 3) ---------------------------------------------------------------------

test("a pickup code's state reads in the counter's words, and an unmapped state degrades to itself", () => {
  assert.equal(willCallWord("ACTIVE"), "Live");
  assert.equal(willCallWord("CONFIRMED"), "Collected");
  assert.equal(willCallWord("REVOKED"), "Cancelled");
  assert.equal(willCallWord("ON_HOLD"), "on hold");
});

test("a code's line says how long it is good, who collected, or that it was cancelled", () => {
  assert.equal(willCallLine({ status: "ACTIVE", expires_at: "2026-10-26T18:00:00Z" }), "Good until Oct 26, 2026");
  assert.equal(willCallLine({ status: "CONFIRMED", expires_at: "2026-10-26T18:00:00Z", collected_by: "Pickup contact (demo)" }), "Collected by Pickup contact (demo)");
  assert.equal(willCallLine({ status: "CONFIRMED", expires_at: "2026-10-26T18:00:00Z" }), "Collected");
  assert.equal(willCallLine({ status: "REVOKED", expires_at: "2026-10-26T18:00:00Z" }), "Cancelled before it was used");
});

test("the order box wants something typed, and a missing order says what to check", () => {
  assert.equal(validateOrderRef("ORD-0044"), "");
  assert.match(validateOrderRef("  "), /Enter an order number/);
  assert.match(willCallRefusal("dispatch: not found", 404), /No order matches that number at this yard/);
  assert.equal(willCallRefusal("dispatch: this order already has a live pickup code, revoke it before printing another", 409), "this order already has a live pickup code, revoke it before printing another");
  assert.match(willCallRefusal("Forbidden", 403), /not yours to do/);
});

test("a minted code is announced once, in words that say it will not be shown again", () => {
  assert.equal(willCallNotice({ order_no: "ORD-0044", expires_at: "2026-10-26T18:00:00Z" }), "Pickup code made for ORD-0044, good until Oct 26, 2026. It is shown once: print the tag now.");
});

// ---- the dispatch rules (part B, sub item 4) --------------------------------------------------------------

const policy = (over = {}) => ({
  branch_id: "b1", commit_prompt_hours: 48, reopen_policy: "MANAGER", load_value_visibility: "MANAGER_UP",
  commit_horizon_weeks: 4, working_view_days: 14, freight_split_mode: "EVEN", driver_session_hours: 12, ...over,
});

test("the rules read as sentences a dispatcher would say, each with its unit", () => {
  const rows = policyRows(policy());
  const byKey = Object.fromEntries(rows.map((r) => [r.key, r]));
  assert.equal(rows.length, 7);
  assert.equal(byKey.commit_prompt_hours.word, "48 hours");
  assert.equal(byKey.reopen_policy.word, "A dispatch manager");
  assert.equal(byKey.load_value_visibility.word, "Managers and above");
  assert.equal(byKey.commit_horizon_weeks.word, "4 weeks");
  assert.equal(byKey.working_view_days.word, "14 days");
  assert.equal(byKey.freight_split_mode.word, "Evenly across the loads");
  assert.equal(byKey.driver_session_hours.word, "12 hours");
  assert.equal(byKey.working_view_days.label, "How far the daily view reaches");
  assert.equal(byKey.load_value_visibility.note, "Managers see the load value; others do not.");
  assert.match(byKey.commit_horizon_weeks.note, /how far ahead a delivery may be promised/i, "the two reaches are never left to be confused");
  assert.match(byKey.working_view_days.note, /not how far ahead a delivery may be promised/i);
  assert.equal(policyRows(policy({ reopen_policy: "NOBODY" })).find((r) => r.key === "reopen_policy").word, "Nobody, a commitment is final");
  assert.equal(policyRows(policy({ reopen_policy: "SOMETHING_NEW" })).find((r) => r.key === "reopen_policy").word, "something new", "an unmapped value degrades to itself, never to an invented phrase");
});

test("the rules form wants whole numbers and nothing else; the bounds are the core's to say", () => {
  const f = (over = {}) => ({ prompt: "48", reopen: "MANAGER", visibility: "MANAGER_UP", horizon: "4", view: "14", ...over });
  assert.equal(validatePolicyFields(f()), "");
  assert.match(validatePolicyFields(f({ prompt: "" })), /Hours ahead is a whole number/);
  assert.match(validatePolicyFields(f({ prompt: "4.5" })), /Hours ahead is a whole number/);
  assert.match(validatePolicyFields(f({ horizon: "x" })), /Weeks is a whole number/);
  assert.match(validatePolicyFields(f({ view: "-1" })), /Days is a whole number/);
  assert.equal(validatePolicyFields(f({ prompt: "999999" })), "", "999999 hours is the core's to refuse, in its own words");
});

test("the rules body is the whole policy: the edited knobs, the others as they were, the yard it is for", () => {
  const body = policyBody({ prompt: "24", reopen: "NOBODY", visibility: "HIDDEN", horizon: "6", view: "10" }, policy(), "b1");
  assert.deepEqual(body, { branch_id: "b1", commit_prompt_hours: 24, reopen_policy: "NOBODY", load_value_visibility: "HIDDEN", commit_horizon_weeks: 6, working_view_days: 10, freight_split_mode: "EVEN", driver_session_hours: 12 });
});

test("a refused rule is the core's sentence with the column name turned into the rule's name, and a bare Forbidden says who decides", () => {
  assert.equal(policyRefusal("dispatch: commit_prompt_hours must be between 0 and 336", 422), "Prompt to commit, hours ahead must be between 0 and 336");
  assert.equal(policyRefusal("dispatch: reopen_policy must be ANYONE, MANAGER or NOBODY", 422), "Who may reopen a committed load must be ANYONE, MANAGER or NOBODY");
  assert.match(policyRefusal("Forbidden", 403), /You can read the dispatch rules, but your dealer names who may change them/);
  assert.match(policyRefusal("", 500), /nothing changed/);
});

test("a history row says the rule, what it was, what it is, who and when, in the same words as the controls", () => {
  const row = historyRow({ field: "reopen_policy", before: "MANAGER", after: "ANYONE", who: "Logistics Manager", when: "2026-10-12T18:04:00Z" });
  assert.equal(row.rule, "Who may reopen a committed load");
  assert.equal(row.was, "A dispatch manager");
  assert.equal(row.now, "Anyone who could have committed it");
  assert.equal(row.who, "Logistics Manager");
  assert.match(row.when, /^Oct 12, 2026/);
  assert.equal(historyRow({ field: "commit_prompt_hours", before: "48", after: "24" }).now, "24");
  assert.equal(historyRow({ field: "commit_prompt_hours", before: "48", after: "24" }).who, "Somebody");
  assert.equal(historyRow({ field: "commit_prompt_hours", before: "48", after: "24" }).when, "");
  assert.equal(historyRow({ field: "future_knob", before: "A", after: "B" }).rule, "future knob");
});

test("the history's fault is the core's sentence, and a refusal for a non manager says the trail is a manager's", () => {
  assert.match(historyFaultWords("Forbidden", 403), /managers read the change history/i);
  assert.match(historyFaultWords("", 500), /nothing changed/);
  assert.equal(historyFaultWords("dispatch: branch_id is required", 400), "branch_id is required");
});

// ---- the manifest and the scan trail (part B, sub item 5) ------------------------------------------------

test("the manifest's refusals say what the dispatcher can do, and the notice says it was recorded", () => {
  assert.match(manifestRefusal("Forbidden", 403), /belongs to another yard/);
  assert.equal(manifestRefusal("dispatch: not found", 404), "There is no such run.");
  assert.match(manifestRefusal("", 401), /Your sign-in ended/);
  assert.match(manifestRefusal("", 500), /nothing changed/);
  assert.equal(manifestNotice("RUN-PLM-1"), "Manifest for RUN-PLM-1 sent to the printer window, and the print is recorded.");
});

test("the scan trail's five stages are in the order a pallet moves, in plain words", () => {
  assert.deepEqual([...SCAN_SEQUENCE], ["RECEIVED", "YARD", "PICKED", "IN_TRANSIT", "DELIVERED"]);
  assert.deepEqual(SCAN_SEQUENCE.map(scanStageLabel), ["Received", "In the yard", "Picked", "On the road", "Delivered"]);
  assert.equal(scanStageLabel("SOMETHING_NEW"), "something new", "an unmapped stage degrades to itself");
});

test("the trail's summary never rounds up: two of three delivered is not a delivered order", () => {
  assert.equal(trailSummary(null), "No scans yet");
  assert.equal(trailSummary({ order_id: "o", checkpoints: [], loads: [], complete: false }), "No scans yet");
  const load = (furthest) => ({ load_ref: "L", reached: [furthest], furthest, last_scanned_at: "2026-10-12T18:00:00Z" });
  assert.equal(trailSummary({ order_id: "o", checkpoints: [], loads: [load("DELIVERED"), load("DELIVERED"), load("PICKED")], complete: false }), "2 of 3 loads delivered");
  assert.equal(trailSummary({ order_id: "o", checkpoints: [], loads: [load("DELIVERED")], complete: true }), "1 load, all delivered");
  assert.equal(trailSummary({ order_id: "o", checkpoints: [], loads: [load("DELIVERED"), load("DELIVERED")], complete: true }), "2 loads, all delivered");
  assert.equal(trailSummary({ order_id: "o", checkpoints: [], loads: [load("PICKED")], complete: false }), "0 of 1 load delivered");
});

test("a load's line says how far it got and when it was last scanned", () => {
  assert.match(scanLoadLine({ load_ref: "PLT-1", reached: ["RECEIVED", "YARD", "PICKED"], furthest: "PICKED", last_scanned_at: "2026-10-12T18:04:00Z" }), /^PLT-1: picked, last scanned Oct 12, 2026/);
  assert.match(scanLoadLine({ load_ref: "PLT-2", reached: ["DELIVERED"], furthest: "DELIVERED", last_scanned_at: "2026-10-12T18:04:00Z", pod_signature_ref: "sig" }), /^PLT-2: delivered, signed for/);
});

// ---- split freight (part C, sub item 6) ------------------------------------------------------------------

test("a typed dollar amount becomes integer cents by its digits, never through a float", () => {
  assert.equal(dollarsToCents("125"), 12500);
  assert.equal(dollarsToCents("125.5"), 12550);
  assert.equal(dollarsToCents("$1,250.05"), 125005);
  assert.equal(dollarsToCents(" 0.07 "), 7);
  assert.equal(dollarsToCents("19.99"), 1999, "19.99 times 100 is 1998.9999999999998 as a float");
  assert.equal(dollarsToCents("1.10"), 110);
  assert.equal(dollarsToCents(""), null);
  for (const bad of ["12.345", "-5", "abc", "1.2.3", "12,34", "$", ".5", "5."]) assert.equal(dollarsToCents(bad), undefined, bad);
});

test("cents show as dollars by integer arithmetic, with the cents always", () => {
  assert.equal(centsText(0), "$0.00");
  assert.equal(centsText(7), "$0.07");
  assert.equal(centsText(12550), "$125.50");
  assert.equal(centsText(125005), "$1,250.05");
  assert.equal(centsText(100000000), "$1,000,000.00");
  assert.equal(centsText(-250), "-$2.50");
});

test("the split form wants a whole number of trucks and amounts that are dollars; the bounds and the sum are the core's", () => {
  const f = (over = {}) => ({ loads: "2", freight: "", amounts: "", ...over });
  assert.equal(validateSplit(f()), "");
  assert.match(validateSplit(f({ loads: "" })), /Trucks is a whole number/);
  assert.match(validateSplit(f({ loads: "2.5" })), /Trucks is a whole number/);
  assert.equal(validateSplit(f({ loads: "300" })), "", "300 trucks is the core's to refuse in its words");
  assert.match(validateSplit(f({ freight: "abc" })), /The freight is dollars and cents/);
  assert.match(validateSplit(f({ amounts: "10, x" })), /Each share is dollars and cents/);
  assert.equal(validateSplit(f({ freight: "$300", amounts: "100, 200" })), "");
});

test("the split body carries cents only, the proposal only when typed, the shares only when typed", () => {
  assert.deepEqual(splitBody({ loads: "3", freight: "", amounts: "" }, "o1"), { order_id: "o1", loads: 3 });
  assert.deepEqual(splitBody({ loads: "2", freight: "$150.50", amounts: "100, 50.50" }, "o1"), { order_id: "o1", loads: 2, freight_cents: 15050, amounts: [10000, 5050] });
});

test("an applied split says the order's freight and each truck's share only from what the core sent", () => {
  const load = (c) => ({ id: "s", branch_id: "b", order_id: "o", stop_no: 0, scheduled_date: "d", status: "READY", photo_count: 0, created_at: "x", ...(c === undefined ? {} : { freight_cents: c }) });
  assert.equal(splitNotice({ order_id: "o", freight_cents: 30000, override: false, loads: [load(15000), load(15000)] }), "Freight $300.00 split across 2 trucks: $150.00, $150.00.");
  assert.equal(splitNotice({ order_id: "o", freight_cents: 100, override: false, loads: [load(34), load(33), load(33)] }), "Freight $1.00 split across 3 trucks: $0.34, $0.33, $0.33.");
  assert.equal(splitNotice({ order_id: "o", freight_cents: 30000, override: false, loads: [load(), load()] }), "Freight split across 2 trucks.", "a share the core did not send is not drawn");
  assert.equal(splitNotice({ order_id: "o", freight_cents: 30000, override: false, loads: [load(30000)] }), "Freight $300.00 on 1 truck.");
  assert.match(splitNotice({ order_id: "o", freight_cents: 10000, override: false, loads: [load(10000)], parked: { id: "r", order_id: "o", branch_id: "b", loads: 2, asked_by: "a dispatch manager", asked_at: "x", state: "PARKED" } }), /^Nothing changed\. The new freight is waiting for approval/);
});

test("a parked raise reads by state, and its figures show only when the core sent them", () => {
  assert.equal(raiseStateWord("PARKED"), "Waiting for approval");
  assert.equal(raiseStateWord("APPLIED"), "Approved and applied");
  assert.equal(raiseStateWord("REFUSED"), "Refused");
  assert.equal(raiseStateWord("NEW_STATE"), "new state");
  const base = { id: "r", order_id: "o", order_no: "ORD-0014", customer_name: "Summit Ridge Builders", branch_id: "b", loads: 2, asked_by: "a dispatch manager", asked_at: "2026-10-12T18:00:00Z", state: "PARKED" };
  assert.deepEqual(raiseLine(base), ["ORD-0014 for Summit Ridge Builders", "2 trucks, asked by a dispatch manager"]);
  const withMoney = raiseLine({ ...base, money: { quoted_freight_cents: 10000, current_freight_cents: 10000, proposed_freight_cents: 15000, delta_cents: 5000 } });
  assert.deepEqual(withMoney.slice(2), ["Quoted $100.00, charged now $100.00, asked for $150.00", "$50.00 above the quote"]);
  assert.equal(raiseLine({ ...base, money: null }).length, 2, "null money is absent money");
});

test("a refused split keeps the core's sentence; a lost reply says asking again is safe", () => {
  assert.equal(splitRefusal("dispatch: a freight raise on this order is already waiting for approval, that one has to be decided before another can be asked for", 409), "a freight raise on this order is already waiting for approval, that one has to be decided before another can be asked for");
  assert.match(splitRefusal("dispatch: forbidden", 403), /not yours to do/);
  assert.match(splitRefusal("", null), /could not reach the desk.*asking again is safe/s);
  assert.match(splitRefusal("", 500), /nothing changed/);
});

// WALK-C16 helpers -----------------------------------------------------------------------------------

test("the TIME_AXIS_HOURS is the twelve hours the redesigned board fits the day into, fixed", async () => {
  const { TIME_AXIS_HOURS } = await import("../src/dispatch-logic.js");
  assert.equal(TIME_AXIS_HOURS.length, 12);
  assert.equal(TIME_AXIS_HOURS[0], 5);
  assert.equal(TIME_AXIS_HOURS[TIME_AXIS_HOURS.length - 1], 16);
});

test("MOFFETT_WEIGHT_LB is named once and is the figure the page reads for a Moffett", async () => {
  const { MOFFETT_WEIGHT_LB } = await import("../src/dispatch-logic.js");
  assert.equal(MOFFETT_WEIGHT_LB, 4500);
});

test("carriesMoffett reads the truck type and the flag, in the demo's order: BOOM rides with one", async () => {
  const { carriesMoffett } = await import("../src/dispatch-logic.js");
  assert.equal(carriesMoffett({ truck_type: "BOOM" }), true);
  assert.equal(carriesMoffett({ truck_type: "FLATBED" }), false);
  assert.equal(carriesMoffett({ truck_type: "BOOM", carries_moffett: false }), false, "an explicit off beats the type");
  assert.equal(carriesMoffett({ truck_type: "FLATBED", carries_moffett: true }), true, "an explicit on beats the type");
  assert.equal(carriesMoffett(null), false);
  assert.equal(carriesMoffett(undefined), false);
});

test("netPayloadWords subtracts the Moffett when the truck carries one, and is words when capacity is absent", async () => {
  const { netPayloadWords } = await import("../src/dispatch-logic.js");
  assert.match(netPayloadWords(4536, true), /after Moffett/, "a Moffett truck reads its payload after the Moffett");
  assert.match(netPayloadWords(4536, false), /lb payload$/, "a wide-open truck reads its full payload");
  assert.equal(netPayloadWords(null, false), "Capacity not measured");
  assert.equal(netPayloadWords(undefined, true), "Capacity not measured");
});

test("yardLoadOrder puts PLANNED first, DISPATCHED next, then COMPLETED and CANCELLED last, by run_no within a status", async () => {
  const { yardLoadOrder } = await import("../src/dispatch-logic.js");
  const row = (runNo, status) => ({ run: { run_no: runNo, status }, gauge: {}, commit: {}, stops: [] });
  const ordered = yardLoadOrder([
    row("RUN-PLM-9", "COMPLETED"),
    row("RUN-PLM-1", "PLANNED"),
    row("RUN-PLM-3", "CANCELLED"),
    row("RUN-PLM-2", "DISPATCHED"),
    row("RUN-PLM-0", "PLANNED"),
  ]);
  assert.deepEqual(ordered.map((r) => r.run.run_no), ["RUN-PLM-0", "RUN-PLM-1", "RUN-PLM-2", "RUN-PLM-9", "RUN-PLM-3"]);
});

test("deckSlotsForStops assigns one slot per stop on the lane's day, never on other days", async () => {
  const { deckSlotsForStops } = await import("../src/dispatch-logic.js");
  const slots = deckSlotsForStops(
    [
      { stop_no: 1, scheduled_date: "2026-10-16" },
      { stop_no: 2, scheduled_date: "2026-10-17" },
      { stop_no: 3, scheduled_date: "2026-10-16" },
      { stop_no: 4, scheduled_date: "2026-10-16" },
    ],
    "2026-10-16",
  );
  assert.equal(slots.get(1), 1);
  assert.equal(slots.get(2), 1, "another day still gets a slot so the row draws even on the empty side");
  assert.equal(slots.get(3), 2);
  assert.equal(slots.get(4), 3);
});

test("boardProblems lists missing crews and over-weight runs in one walk, and is empty on a clean day", async () => {
  const { boardProblems } = await import("../src/dispatch-logic.js");
  const view = {
    branch_id: "b", date: "d", policy: null, value_visible: false, gaps_checked: true, can_commit: true, can_reopen: true,
    working_from: "d", working_to: "d", working_days: 14, counts: { deliveries: 0, pickups: 0, unassigned: 0, ready: 0, requested: 0, committed: 0, due: 0 },
    trucks: [], loads: [], staging: [],
  };
  assert.deepEqual(boardProblems(view), [{ kind: "no-runs", runNo: "" }]);
  const rowMissing = (status, truck_id, driver_id, band) => ({
    run: { id: "r1", run_no: "RUN-PLM-1", status, truck_id, driver_id },
    gauge: { weight_kg: 9000, capacity_kg: 9000, band },
    commit: { due_to_commit: false, committed: false, can_reopen: true, reopened: false },
    stops: [],
  });
  const view2 = {
    ...view, loads: [
      rowMissing("PLANNED", null, null, "GREEN"),
      rowMissing("PLANNED", "t1", null, "GREEN"),
      rowMissing("PLANNED", "t1", "d1", "RED"),
    ],
  };
  const problems = boardProblems(view2);
  assert.equal(problems.length, 3, "1 both-missing, 1 driver-missing, 1 over-weight");
  assert.equal(problems[0].kind, "missing-crew");
  assert.equal(problems[0].what, "both");
  assert.equal(problems[1].kind, "missing-crew");
  assert.equal(problems[1].what, "driver");
  assert.equal(problems[2].kind, "over-weight");
});

test("boardProblemLine reads each problem in its own words, never in a generic sentence", async () => {
  const { boardProblemLine } = await import("../src/dispatch-logic.js");
  assert.match(boardProblemLine({ kind: "over-weight", runNo: "RUN-PLM-1", figure: "10.0 of 9.9 tons" }), /RUN-PLM-1 is over weight/);
  assert.match(boardProblemLine({ kind: "missing-crew", runNo: "RUN-PLM-2", what: "both" }), /missing a truck and a driver/);
  assert.match(boardProblemLine({ kind: "missing-crew", runNo: "RUN-PLM-2", what: "driver" }), /missing a driver/);
  assert.match(boardProblemLine({ kind: "due-to-commit", runNo: "RUN-PLM-3", daysToCommit: 1 }), /due to commit in 1 day/);
  assert.match(boardProblemLine({ kind: "due-to-commit", runNo: "RUN-PLM-3", daysToCommit: 2 }), /due to commit in 2 days/);
});

test("dispatchConfirmWords names the truck, driver, and stop count, states that ready loads are marked loaded, and names any stop held for payment", async () => {
  const { PUT_ON_TRUCK_COMMIT_NOTE, dispatchConfirmWords } = await import("../src/dispatch-logic.js");
  assert.equal(PUT_ON_TRUCK_COMMIT_NOTE, "Putting a load on a truck commits the order.");
  const one = dispatchConfirmWords(
    { run_no: "RUN-PLM-2", truck_code: "Truck 03", driver_name: "Driver A (demo)", stop_count: 1 },
    [{ id: "s1", order_no: "ORD-0021", status: "READY" }],
  );
  assert.equal(one.title, "Send Truck 03 with Driver A (demo) and 1 stop?");
  assert.equal(one.body, "The driver sees the run at once. Sending it marks its 1 ready load loaded. A sent run cannot be called back here.");
  assert.deepEqual(one.holds, []);
  const holds = new Map([
    ["s1", { held: false, reason: "" }],
    ["s2", { held: true, reason: "Payment needed before release, call the office.", order_no: "ORD-0014" }],
  ]);
  const many = dispatchConfirmWords(
    { run_no: "RUN-PLM-1", truck_code: "Truck 01", driver_name: "Driver A (demo)", stop_count: 3 },
    [
      { id: "s1", order_no: "ORD-0012", status: "LOADED" },
      { id: "s2", order_no: "ORD-0014", status: "READY" },
      { id: "s3", order_no: "ORD-0029", status: "READY" },
    ],
    holds,
  );
  assert.equal(many.title, "Send Truck 01 with Driver A (demo) and 3 stops?");
  assert.equal(many.body, "The driver sees the run at once. Sending it marks its 2 ready loads loaded. A sent run cannot be called back here.");
  assert.deepEqual(many.holds, ["ORD-0014 is held for payment and cannot be released until it is paid."]);
});

