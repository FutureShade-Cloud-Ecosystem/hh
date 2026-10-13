// The Drivers view's pure decisions, on their own: how a run and its stops
// classify into the board's four states, what the row's counts and words are,
// and how long ago the last change came. No browser, no network. Every case
// is a fact the core holds (the wire contract's closed status vocabularies) or
// the original board's own words.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  EMPTY_BOARD_WORDS,
  DRIVER_GROUP_ORDER,
  buildDriverRow,
  driverGroupWord,
  groupDriverRows,
  idleDriverWords,
  runStatusWord,
  sinceWords,
  stopPositionWords,
  stopStatusWord,
} from "../src/drivers-logic.ts";

// The suite's relative stamps: every instant derives from one module clock,
// never a calendar date. The clock rides the call where a function takes now.
const NOW = Date.now();
const minutesAgo = (m) => new Date(NOW - m * 60000).toISOString();
const CREATED_AT = minutesAgo(240);
const DISPATCHED_AT = minutesAgo(180);
const DELIVERED_AT = minutesAgo(140);
const EN_ROUTE_AT = minutesAgo(120);

// A dispatched run with two stops, the second of them en route: the walking
// case the board's first group is drawn from.
const enRouteRun = {
  id: "run-1",
  run_no: "RUN-1",
  status: "DISPATCHED",
  driver_id: "drv-1",
  driver_name: "Driver persona (demo)",
  truck_id: "truck-1",
  truck_code: "T1",
  stop_count: 3,
  created_at: CREATED_AT,
  dispatched_at: DISPATCHED_AT,
  completed_at: null,
  reopened_at: "",
};
const deliveredStop = {
  id: "stop-1",
  run_id: "run-1",
  stop_no: 1,
  status: "DELIVERED",
  loaded_at: minutesAgo(210),
  en_route_at: DISPATCHED_AT,
  delivered_at: DELIVERED_AT,
  reopened_at: null,
};
const enRouteStop = {
  id: "stop-2",
  run_id: "run-1",
  stop_no: 2,
  status: "EN_ROUTE",
  loaded_at: minutesAgo(210),
  en_route_at: EN_ROUTE_AT,
  delivered_at: null,
  reopened_at: null,
};
const readyStop = {
  id: "stop-3",
  run_id: "run-1",
  stop_no: 3,
  status: "READY",
  loaded_at: null,
  en_route_at: null,
  delivered_at: null,
  reopened_at: null,
};

test("a dispatched run with a stop en route is on a stop; its current stop is the lowest unsettled one", () => {
  const row = buildDriverRow(enRouteRun, [readyStop, deliveredStop, enRouteStop]);
  assert.equal(row.group, "on-stop");
  assert.equal(row.current_stop?.id, "stop-2");
});

test("a dispatched run whose next stop is loaded or ready is between stops", () => {
  const loaded = { ...enRouteStop, status: "LOADED" };
  const row = buildDriverRow(enRouteRun, [deliveredStop, loaded, readyStop]);
  assert.equal(row.group, "between-stops");
  assert.equal(row.current_stop?.id, "stop-2");
  const readyOnly = buildDriverRow(enRouteRun, [deliveredStop, { ...loaded, status: "DELIVERED" }, readyStop]);
  assert.equal(readyOnly.group, "between-stops");
});

test("a planned run has not started, and a completed or cancelled run is done", () => {
  assert.equal(buildDriverRow({ ...enRouteRun, status: "PLANNED" }, []).group, "not-started");
  assert.equal(buildDriverRow({ ...enRouteRun, status: "COMPLETED" }, []).group, "done");
  assert.equal(buildDriverRow({ ...enRouteRun, status: "CANCELLED" }, []).group, "done");
});

test("a dispatched run with every stop settled is done: the driving is over, the paper step is not", () => {
  const row = buildDriverRow(enRouteRun, [deliveredStop, { ...enRouteStop, status: "DELIVERED" }]);
  assert.equal(row.group, "done");
  assert.equal(row.current_stop, null);
});

test("the row counts the stops done, left and did not go", () => {
  const failed = { ...readyStop, status: "FAILED" };
  const row = buildDriverRow(enRouteRun, [deliveredStop, enRouteStop, readyStop, failed]);
  assert.equal(row.done_stops, 1);
  assert.equal(row.left_stops, 2);
  assert.equal(row.did_not_go, 1);
});

test("the last change is the newest status timestamp on the run or any of its stops", () => {
  const row = buildDriverRow(enRouteRun, [deliveredStop, enRouteStop, readyStop]);
  assert.equal(row.last_changed_at, EN_ROUTE_AT);
});

test("a run with no transition timestamps falls back to the run's own creation", () => {
  const opened = minutesAgo(285);
  const row = buildDriverRow(
    { ...enRouteRun, status: "PLANNED", dispatched_at: null, completed_at: null, created_at: opened },
    [],
  );
  assert.equal(row.last_changed_at, opened);
  assert.equal(row.group, "not-started");
});

test("the four groups sit in the brief's order, empty ones are dropped, and the freshest row leads", () => {
  const planned = buildDriverRow(
    { ...enRouteRun, id: "run-2", run_no: "RUN-2", status: "PLANNED", dispatched_at: null, created_at: CREATED_AT },
    [],
  );
  const completed = buildDriverRow(
    { ...enRouteRun, id: "run-3", run_no: "RUN-3", status: "COMPLETED", completed_at: minutesAgo(60) },
    [deliveredStop, { ...enRouteStop, status: "DELIVERED" }],
  );
  const slowOnStop = buildDriverRow({ ...enRouteRun, id: "run-4", run_no: "RUN-4" }, [
    deliveredStop,
    { ...enRouteStop, en_route_at: minutesAgo(150) },
  ]);
  const fastOnStop = buildDriverRow(enRouteRun, [deliveredStop, enRouteStop]);
  const between = buildDriverRow({ ...enRouteRun, id: "run-5", run_no: "RUN-5" }, [deliveredStop, { ...enRouteStop, status: "LOADED" }]);
  const groups = groupDriverRows([planned, completed, slowOnStop, fastOnStop, between]);
  assert.deepEqual(
    groups.map((g) => g.key),
    ["on-stop", "between-stops", "not-started", "done"],
  );
  const onStop = groups.find((g) => g.key === "on-stop");
  assert.equal(onStop.rows.length, 2);
  assert.equal(onStop.rows[0].run.id, "run-1", "the row that changed most recently leads its group");
});

test("the group words are the board's own, and the order constant matches them", () => {
  assert.deepEqual(DRIVER_GROUP_ORDER, ["on-stop", "between-stops", "not-started", "done"]);
  assert.equal(driverGroupWord("on-stop"), "On a stop");
  assert.equal(driverGroupWord("between-stops"), "Between stops");
  assert.equal(driverGroupWord("not-started"), "Not started");
  assert.equal(driverGroupWord("done"), "Done");
});

test("run and stop statuses carry the original board's words; an unknown one is omitted, never raw", () => {
  assert.equal(runStatusWord("PLANNED"), "Planned");
  assert.equal(runStatusWord("DISPATCHED"), "Dispatched");
  assert.equal(runStatusWord("COMPLETED"), "Completed");
  assert.equal(runStatusWord("CANCELLED"), "Cancelled");
  assert.equal(runStatusWord("WHATEVER"), "");
  assert.equal(stopStatusWord("READY"), "Ready");
  assert.equal(stopStatusWord("LOADED"), "Loaded");
  assert.equal(stopStatusWord("EN_ROUTE"), "En route");
  assert.equal(stopStatusWord("DELIVERED"), "Delivered");
  assert.equal(stopStatusWord("FAILED"), "Failed");
  assert.equal(stopStatusWord("CANCELLED"), "Cancelled");
  assert.equal(stopStatusWord("WHATEVER"), "");
});

test("the time since the last change reads in minutes, hours and days; unparsable answers nothing", () => {
  assert.equal(sinceWords(minutesAgo(4), NOW), "4m ago");
  assert.equal(sinceWords(minutesAgo(120), NOW), "2h ago");
  assert.equal(sinceWords(minutesAgo(2880), NOW), "2d ago");
  assert.equal(sinceWords(minutesAgo(0.3), NOW), "under a minute");
  assert.equal(sinceWords("not-a-time", NOW), "");
  assert.equal(sinceWords("", NOW), "");
});

test("the row's stop position reads as stop n of m, and answers nothing when there is no current stop", () => {
  const row = buildDriverRow(enRouteRun, [deliveredStop, enRouteStop, readyStop]);
  assert.equal(stopPositionWords(row), "stop 2 of 3");
  assert.equal(stopPositionWords({ ...row, current_stop: null }), "");
  assert.equal(stopPositionWords({ ...row, run: { ...enRouteRun, stop_count: 0 } }), "");
});

test("drivers with no run on the board are counted by words, and inactive drivers never are", () => {
  const drivers = [
    { id: "drv-1", name: "Driver persona (demo)", is_active: true },
    { id: "drv-2", name: "Driver two (demo)", is_active: true },
    { id: "drv-3", name: "Driver three (demo)", is_active: false },
  ];
  assert.equal(idleDriverWords(drivers, [enRouteRun]), "1 of the 2 drivers on the roster holds no run today.");
  assert.equal(idleDriverWords(drivers, [enRouteRun, { ...enRouteRun, id: "run-9", driver_id: "drv-2" }]), "");
  assert.equal(idleDriverWords([], []), "");
  assert.equal(idleDriverWords([drivers[0]], [enRouteRun]), "");
  const roster = [drivers[1], { id: "drv-4", name: "Driver four (demo)", is_active: true }, { id: "drv-5", name: "Driver five (demo)", is_active: false }];
  assert.equal(idleDriverWords(roster, [enRouteRun]), "2 of the 2 drivers on the roster hold no run today.");
});

test("the empty board's words are the brief's own sentence", () => {
  assert.equal(EMPTY_BOARD_WORDS, "No runs on the board today");
});
