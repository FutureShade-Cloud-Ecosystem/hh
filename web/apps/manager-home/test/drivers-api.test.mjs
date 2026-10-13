// The Drivers view's read projections and row assembly, on their own: the
// whitelist that copies the board, the driver registry and the truck registry
// into the narrow shapes the screen reads, the assembly of one row per run,
// and the truck words with their registry fallback. No browser, no network.
import { test } from "node:test";
import assert from "node:assert/strict";
import { rowsForBoard, toDispatchBoard, toDispatchDriver, toDispatchRun, toDispatchStop, toDispatchTruck, truckWords } from "../src/drivers-api.ts";

test("a run projects through the whitelist: the row's fields in, everything else dropped", () => {
  const run = toDispatchRun({
    id: "run-1",
    run_no: "RUN-1",
    branch_id: "b1",
    scheduled_date: "2026-10-12",
    status: "DISPATCHED",
    truck_id: "t1",
    driver_id: "d1",
    truck_code: "T1",
    driver_name: "Driver persona (demo)",
    notes: "the truck carries a boom",
    dispatched_at: "2026-10-12T07:00:00Z",
    completed_at: null,
    stop_count: 3,
    created_at: "2026-10-12T06:00:00Z",
    commit_state: "AUTO",
    reopened_at: "",
    reopen_reason: "",
  });
  assert.equal(run.status, "DISPATCHED");
  assert.equal(run.driver_name, "Driver persona (demo)");
  assert.equal(run.stop_count, 3);
  assert.equal(run.dispatched_at, "2026-10-12T07:00:00Z");
  assert.equal(run.completed_at, null);
  assert.equal("notes" in run, false, "a field the screen does not read is dropped at the door");
  assert.equal("commit_state" in run, false);
});

test("a run missing optional fields projects to honest empties, never invented ones", () => {
  const run = toDispatchRun({ id: "run-2" });
  assert.equal(run.run_no, "");
  assert.equal(run.status, "");
  assert.equal(run.stop_count, 0);
  assert.equal(run.dispatched_at, null);
  assert.equal(run.created_at, "");
});

test("a stop projects with its place in the day and its status timestamps", () => {
  const stop = toDispatchStop({
    id: "stop-1",
    branch_id: "b1",
    order_id: "o1",
    run_id: "run-1",
    stop_no: 2,
    status: "EN_ROUTE",
    address_line: "12 invented street",
    city: "Stonebridge",
    loaded_at: "2026-10-12T06:30:00Z",
    en_route_at: null,
    delivered_at: null,
    reopened_at: null,
    photo_count: 0,
    created_at: "2026-10-12T05:00:00Z",
  });
  assert.equal(stop.run_id, "run-1");
  assert.equal(stop.stop_no, 2);
  assert.equal(stop.status, "EN_ROUTE");
  assert.equal(stop.en_route_at, null);
  assert.equal("address_line" in stop, false, "the row carries the state of the day, not the address");
  assert.equal("city" in stop, false);
});

test("the board projects into runs, stops and the unassigned lane", () => {
  const board = toDispatchBoard({
    branch_id: "b1",
    date: "2026-10-12",
    runs: [{ id: "run-1", run_no: "RUN-1", status: "PLANNED", stop_count: 1, created_at: "2026-10-12T06:00:00Z" }],
    stops: [{ id: "stop-1", run_id: "run-1", stop_no: 1, status: "READY" }],
    unassigned: [{ id: "stop-9", run_id: null, stop_no: 1, status: "READY" }],
  });
  assert.equal(board.branch_id, "b1");
  assert.equal(board.runs.length, 1);
  assert.equal(board.runs[0].id, "run-1");
  assert.equal(board.stops.length, 1);
  assert.equal(board.unassigned.length, 1);
});

test("a driver and a truck project to their registry facts", () => {
  const driver = toDispatchDriver({ id: "d1", branch_id: "b1", name: "Driver persona (demo)", phone: "555 0100", licence_class: "DZ", is_active: true, user_id: "u1" });
  assert.equal(driver.name, "Driver persona (demo)");
  assert.equal(driver.is_active, true);
  assert.equal("phone" in driver, false);
  const truck = toDispatchTruck({ id: "t1", branch_id: "b1", code: "T1", name: "Truck 1 flatbed", truck_type: "FLATBED", is_active: true, capacity_kg: 9000 });
  assert.equal(truck.code, "T1");
  assert.equal(truck.truck_type, "FLATBED");
  assert.equal("capacity_kg" in truck, false);
});

test("one row is built per run, from that run's own stops only", () => {
  const board = toDispatchBoard({
    branch_id: "b1",
    date: "2026-10-12",
    runs: [
      { id: "run-1", run_no: "RUN-1", status: "DISPATCHED", driver_id: "d1", driver_name: "Driver persona (demo)", truck_code: "T1", truck_id: "t1", stop_count: 2, created_at: "2026-10-12T06:00:00Z", dispatched_at: "2026-10-12T07:00:00Z" },
      { id: "run-2", run_no: "RUN-2", status: "PLANNED", driver_id: "d2", driver_name: "Driver two (demo)", truck_code: "", truck_id: "t2", stop_count: 1, created_at: "2026-10-12T06:30:00Z" },
    ],
    stops: [
      { id: "stop-3", run_id: "run-1", stop_no: 2, status: "READY" },
      { id: "stop-1", run_id: "run-1", stop_no: 1, status: "DELIVERED", delivered_at: "2026-10-12T07:40:00Z" },
      { id: "stop-9", run_id: null, stop_no: 1, status: "READY" },
      { id: "stop-4", run_id: "run-2", stop_no: 1, status: "READY" },
    ],
    unassigned: [],
  });
  const rows = rowsForBoard(board.runs, board.stops);
  assert.equal(rows.length, 2);
  const first = rows.find((r) => r.run.id === "run-1");
  assert.equal(first.group, "between-stops", "one delivered, the next stop ready: between stops");
  assert.equal(first.done_stops, 1);
  assert.equal(first.left_stops, 1);
  const second = rows.find((r) => r.run.id === "run-2");
  assert.equal(second.group, "not-started");
  assert.equal(second.current_stop?.id, "stop-4", "the unassigned stop is nobody's row");
});

test("the truck words come from the run's own code, else the registry, else nothing", () => {
  const trucks = [toDispatchTruck({ id: "t2", code: "T2", name: "Truck 2 dump", truck_type: "DUMP", is_active: true })];
  assert.equal(truckWords({ truck_code: "T1", truck_id: "t1" }, trucks), "T1");
  assert.equal(truckWords({ truck_code: "", truck_id: "t2" }, trucks), "T2");
  assert.equal(truckWords({ truck_code: "", truck_id: "t3" }, trucks), "");
  assert.equal(truckWords({ truck_code: "", truck_id: "" }, trucks), "");
});
