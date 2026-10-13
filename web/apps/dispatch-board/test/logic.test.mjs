// The dispatch board's rules, pinned: the lifecycle matrix, the capacity bands, the quick
// cards' filters, the keyboard path's move projection, the bulk load words, the reschedule
// check and the sentences. Every case is a rule that would be quietly wrong on screen.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  AMBER_FROM,
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
  scheduleBody,
  scheduleNotice,
  scheduleRefusal,
  validateReason,
  truckBody,
  truckLine,
  validateDriver,
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
  assert.equal(fmtNum(null), "n/a");
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

test("a card's scope is named: the day, the working window or the branch", () => {
  const v = view();
  assert.equal(cardScopeLabel(v, "card.ready"), "this day");
  assert.equal(cardScopeLabel(v, "card.unassigned"), "next 14 days");
  assert.equal(cardScopeLabel(v, "card.pickups"), "this branch");
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

test("a refusal reads in words by status, and a transport error says the board could not reach the server", () => {
  assert.match(refusalWords("The reopen", "", null), /could not reach the server/);
  assert.match(refusalWords("The reopen", "", 401), /session ended/);
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
  assert.equal(fmtWeight(null), "n/a");
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
  assert.equal(made.capacity_kg, 9071.8);
  assert.equal(made.capacity_m3, 7.6);
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
  assert.equal(changed.capacity_kg, 9071.8);
  const out = truckBody(tf({ capacity: "19842" }), truck({ is_active: false }), "b1", false);
  assert.equal(out.is_active, false);
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
  assert.match(validateDriver(df({ login: "not-an-id" })), /A staff login id is a long id with dashes/);
  assert.equal(validateDriver(df({ login: "d0200000-0000-4000-9000-000000000001" })), "");
  const body = driverBody(df({ name: " Driver D ", phone: " 619-555-0144 ", licence: "B" }), undefined, "b1");
  assert.deepEqual(body, { id: "", branch_id: "b1", name: "Driver D", phone: "619-555-0144", licence_class: "B", user_id: "", is_active: true });
  const kept = driverBody(df({ name: "Driver A" }), { id: "d1", branch_id: "b1", name: "x", is_active: false }, "b1", true);
  assert.equal(kept.id, "d1");
  assert.equal(kept.is_active, true);
  assert.equal(driverLine({ id: "d1", branch_id: "b", name: "x", is_active: true, licence_class: "A", phone: "619-555-0141", user_id: "u" }).join(" | "), "Licence A | 619-555-0141 | Has a staff login");
  assert.equal(driverLine({ id: "d1", branch_id: "b", name: "x", is_active: true }).join(" | "), "No licence on file | No phone | No staff login");
});
