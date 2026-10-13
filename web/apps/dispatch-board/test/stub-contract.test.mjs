// The dispatch stub against the contract and against the core, field by field.
//
// A stub that drifts from the server makes every browser leg a flattering lie, so this test
// starts the stub, makes every call the board makes, and holds each answer to the generated wire
// descriptors: required fields present, types and vocabularies right, and no field the contract
// does not declare (the descriptors are open objects, so the undeclared field check is this
// file's own). Then each refusal is held to the core's words: the status the dispatch taxonomy
// grades, the envelope, and the sentence itself, which must appear in the core's source. A
// refusal the stub invented fails here.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { readdirSync, readFileSync } from "node:fs";
import { createServer } from "node:net";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { SCHEMA_DESCRIPTORS } from "../../../packages/api-client/src/testing.ts";
import { validateValue } from "../../../packages/api-client/test/_validate.mjs";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const coreInternal = join(appDir, "..", "..", "..", "core", "internal");

const coreSource = ["dispatch", "settings", join("platform", "httpx")]
  .flatMap((dir) =>
    readdirSync(join(coreInternal, dir))
      .filter((f) => f.endsWith(".go") && !f.endsWith("_test.go"))
      .map((f) => readFileSync(join(coreInternal, dir, f), "utf8")),
  )
  .join("\n");

let child;
let base;

async function freePort() {
  return new Promise((resolve) => {
    const probe = createServer();
    probe.listen(0, "127.0.0.1", () => {
      const { port } = probe.address();
      probe.close(() => resolve(port));
    });
  });
}

before(async () => {
  const port = await freePort();
  base = `http://127.0.0.1:${port}`;
  child = spawn(process.execPath, [join(appDir, "test", "stub", "dispatch-stub.mjs")], {
    env: { DISPATCH_STUB_PORT: String(port) },
    stdio: "ignore",
  });
  for (let i = 0; i < 100; i += 1) {
    try {
      if ((await fetch(`${base}/healthz`)).ok) return;
    } catch {
      /* not up yet */
    }
    await new Promise((r) => setTimeout(r, 50));
  }
  throw new Error("the stub did not start");
});

after(() => {
  child?.kill();
});

async function call(method, path, body) {
  const headers = { Authorization: "Bearer stub-token", "X-Org": "hh-demo" };
  let payload;
  if (body !== undefined) {
    headers["Content-Type"] = "application/json";
    payload = JSON.stringify(body);
  }
  const res = await fetch(`${base}${path}`, { method, headers, body: payload });
  const text = await res.text();
  let json = null;
  try {
    json = JSON.parse(text);
  } catch {
    /* not json */
  }
  return { status: res.status, json };
}

const reset = () => fetch(`${base}/_stub/reset`, { method: "POST" });
const post = (path, body) =>
  fetch(`${base}${path}`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
const world = async () => (await fetch(`${base}/_stub/world`)).json();

// ---- the declared keys of a descriptor, through refs and intersections ----

function declared(d) {
  if (d.k === "ref") return declared(SCHEMA_DESCRIPTORS[d.name]);
  if (d.k === "allOf") return Object.assign({}, ...d.of.map(declared));
  if (d.k === "object") return d.props;
  return {};
}

function undeclared(value, d, path = "value", out = []) {
  if (d.k === "ref") return undeclared(value, SCHEMA_DESCRIPTORS[d.name], path, out);
  if (d.k === "allOf") {
    const props = declared(d);
    if (value !== null && typeof value === "object" && !Array.isArray(value)) {
      for (const key of Object.keys(value)) if (props[key] === undefined) out.push(`${path}.${key}`);
      for (const [key, leg] of Object.entries(props)) if (value[key] !== undefined) undeclared(value[key], leg, `${path}.${key}`, out);
    }
    return out;
  }
  if (d.k === "object") {
    if (value === null || typeof value !== "object" || Array.isArray(value)) return out;
    for (const key of Object.keys(value)) if (d.props[key] === undefined) out.push(`${path}.${key}`);
    for (const [key, leg] of Object.entries(d.props)) if (value[key] !== undefined) undeclared(value[key], leg, `${path}.${key}`, out);
    return out;
  }
  if (d.k === "array" && Array.isArray(value)) value.forEach((v, i) => undeclared(v, d.of, `${path}[${i}]`, out));
  if (d.k === "record" && value !== null && typeof value === "object") for (const [k, v] of Object.entries(value)) undeclared(v, d.of, `${path}.${k}`, out);
  if (d.k === "union") {
    const leg = d.of.find((l) => l.k !== "null");
    if (leg !== undefined && value !== null) undeclared(value, leg, path, out);
  }
  return out;
}

function holds(schemaName, value) {
  const descriptor = SCHEMA_DESCRIPTORS[schemaName];
  assert.ok(descriptor !== undefined, `no descriptor named ${schemaName}`);
  assert.deepEqual(validateValue(value, descriptor), [], `${schemaName} does not fit the contract`);
  assert.deepEqual(undeclared(value, descriptor), [], `${schemaName} carries fields the contract does not declare`);
}

function refused(res, status, fragment) {
  assert.equal(res.status, status);
  holds("Error", res.json);
  assert.ok(res.json.error.includes(fragment), `expected "${fragment}" in "${res.json.error}"`);
  assert.ok(coreSource.includes(fragment), `the core's source does not contain the sentence "${fragment}": the stub invented it`);
}

const ids = async () => {
  const w = await world();
  const stop = (no) => w.stops.find((s) => s.order_no === no);
  const run = (no) => w.runs.find((r) => r.run_no === no);
  return { w, stop, run };
};

test("the demo way in answers the contract's shapes", async () => {
  const flows = await (await fetch(`${base}/api/v1/demo/flows`)).json();
  holds("DemoFlowList", flows);
  const minted = await call("POST", "/api/v1/demo/session", { flow: "logistics-manager" });
  assert.equal(minted.status, 200);
  assert.ok(typeof minted.json.token === "string");
  assert.deepEqual(Object.keys(minted.json).sort(), ["flow", "token", "user"]);
});

test("the branches, trucks and drivers fit the contract", async () => {
  await reset();
  const branches = await call("GET", "/api/v1/branches");
  assert.equal(branches.status, 200);
  for (const b of branches.json) holds("Branch", b);
  const trucks = await call("GET", "/api/v1/dispatch/trucks");
  holds("DispatchTruckPage", trucks.json);
  assert.equal(trucks.json.items.length, 4);
  const drivers = await call("GET", "/api/v1/dispatch/drivers");
  holds("DispatchDriverPage", drivers.json);
  assert.equal(drivers.json.items.length, 3);
});

test("the load board, the day board, the gaps and the calendar fit the contract", async () => {
  await reset();
  const board = await call("GET", "/api/v1/dispatch/loadboard");
  assert.equal(board.status, 200);
  holds("DispatchLoadBoardView", board.json);
  assert.equal(board.json.loads.length, 4);
  assert.ok(board.json.staging.length >= 2);
  assert.equal(board.json.value_visible, false);
  for (const row of board.json.loads) {
    assert.equal("value_cents" in row.gauge, false, "no money rides a gauge the policy refuses");
    for (const s of row.stops) assert.equal("freight_cents" in s, false);
  }
  const day = await call("GET", "/api/v1/dispatch/board");
  holds("DispatchBoard", day.json);
  const gaps = await call("GET", "/api/v1/dispatch/loadboard/gaps");
  holds("DispatchGapPage", gaps.json);
  assert.equal(gaps.json.checked, true);
  const calendar = await call("GET", "/api/v1/dispatch/loadboard/calendar");
  holds("DispatchCalendarDayPage", calendar.json);
  assert.equal(calendar.json.items.length, 7);
});

test("building a load: assign, crew, dispatch, load, and every answer fits the contract", async () => {
  await reset();
  const { stop, run } = await ids();
  const created = await call("POST", "/api/v1/dispatch/runs", { branch_id: "b", scheduled_date: (await world()).today, truck_id: "", driver_id: "", notes: "" });
  assert.equal(created.status, 201);
  holds("DispatchRun", created.json);
  const planned = run("RUN-PLM-3");
  const assigned = await call("POST", `/api/v1/dispatch/stops/${stop("ORD-0029").id}/assign`, { run_id: created.json.id, stop_no: 0 });
  assert.equal(assigned.status, 200);
  holds("DispatchStop", assigned.json);
  assert.equal(assigned.json.commit_state, "COMMITTED", "putting a load on a truck is the commitment");
  const crew = await call("POST", `/api/v1/dispatch/runs/${created.json.id}/crew`, { truck_id: planned.truck_id, driver_id: (await call("GET", "/api/v1/dispatch/drivers")).json.items[0].id });
  holds("DispatchRun", crew.json);
  const sent = await call("POST", `/api/v1/dispatch/runs/${created.json.id}/dispatch`);
  assert.equal(sent.status, 200);
  holds("DispatchRunDetail", sent.json);
  assert.equal(sent.json.stops[0].status, "LOADED", "dispatching loads every ready stop on the run");
  const enRoute = await call("POST", `/api/v1/dispatch/stops/${stop("ORD-0029").id}/status`, { status: "EN_ROUTE", reason: "" });
  holds("DispatchStop", enRoute.json);
});

test("the bulk load answers a per stop outcome and refuses a held order in words", async () => {
  await reset();
  const { stop } = await ids();
  const out = await call("POST", "/api/v1/dispatch/loads/mark-loaded", { stop_ids: [stop("ORD-0012").id, stop("ORD-0014").id, stop("ORD-0029").id] });
  assert.equal(out.status, 200);
  holds("DispatchLoadOutcomeList", out.json);
  assert.deepEqual(out.json.items.map((o) => o.loaded), [true, false, false]);
  assert.ok(coreSource.includes(out.json.items[1].refusal.replace(/^dispatch: /, "")), "the hold refusal is the core's");
  assert.ok(coreSource.includes(out.json.items[2].refusal.replace(/^dispatch: /, "")), "the unassigned refusal is the core's");
});

test("reopening a committed truck asks the load again, and the marker clears", async () => {
  await reset();
  const { run, stop } = await ids();
  const r1 = run("RUN-PLM-1");
  const reopened = await call("POST", `/api/v1/dispatch/runs/${r1.id}/reopen`, { reason: "A better fit turned up" });
  assert.equal(reopened.status, 200);
  holds("DispatchRun", reopened.json);
  assert.equal(reopened.json.commit_state, "REOPENED");
  assert.equal(reopened.json.commit.committed, false);
  assert.equal(reopened.json.reopen_reason, "A better fit turned up");
  const cleared = await call("POST", `/api/v1/dispatch/runs/${r1.id}/reopen/clear`);
  holds("DispatchRun", cleared.json);
  assert.equal(cleared.json.commit_state, "AUTO");
  const committed = await call("POST", `/api/v1/dispatch/stops/${stop("ORD-0012").id}/commit`, { scheduled_date: "" });
  holds("DispatchStop", committed.json);
  assert.equal(committed.json.commit_state, "COMMITTED");
  const one = await call("POST", `/api/v1/dispatch/stops/${stop("ORD-0012").id}/reopen`, { reason: "Customer moved the day" });
  holds("DispatchStop", one.json);
  assert.equal(one.json.commit_state, "REQUESTED");
});

test("a stop and a whole truck move to another day", async () => {
  await reset();
  const { w, run, stop } = await ids();
  const next = (() => {
    const [y, m, d] = w.today.split("-").map(Number);
    const date = new Date(y, m - 1, d + 2);
    const pad = (n) => String(n).padStart(2, "0");
    return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
  })();
  const one = await call("POST", `/api/v1/dispatch/stops/${stop("ORD-0029").id}/reschedule`, { to_date: next, reason: "Gate is closed that day" });
  assert.equal(one.status, 200);
  holds("DispatchRescheduleResult", one.json);
  assert.equal(one.json.entity_type, "delivery_stop");
  const whole = await call("POST", `/api/v1/dispatch/runs/${run("RUN-PLM-2").id}/reschedule`, { to_date: next, reason: "Truck is in the shop" });
  holds("DispatchRescheduleResult", whole.json);
  assert.equal(whole.json.entity_type, "delivery_run");
  assert.equal(whole.json.stops_moved, 1);
});

test("the payment holds of a run read as holds, and the amount rides only when the core allows it", async () => {
  await reset();
  const { run } = await ids();
  const holdsRead = await call("GET", `/api/v1/dispatch/runs/${run("RUN-PLM-1").id}/payment-holds`);
  holds("DispatchPaymentHoldPage", holdsRead.json);
  assert.equal(holdsRead.json.items.length, 1);
  assert.equal("amount_due_cents" in holdsRead.json.items[0], false);
  await post("/_stub/hold-amount", { on: true });
  const withAmount = await call("GET", `/api/v1/dispatch/runs/${run("RUN-PLM-1").id}/payment-holds`);
  holds("DispatchPaymentHoldPage", withAmount.json);
  assert.equal(typeof withAmount.json.items[0].amount_due_cents, "number");
});

test("the refusals carry the core's statuses, envelope and sentences", async () => {
  await reset();
  const { stop, run } = await ids();
  const r1 = run("RUN-PLM-1");
  const r2 = run("RUN-PLM-2");
  const r4 = run("RUN-PLM-4");
  // The auto assign seam: the core build answers 501 and says what would answer it.
  refused(await call("POST", "/api/v1/dispatch/auto-assign"), 501, "automatic load assignment is part of the Pro module");
  // A run is dispatched with a truck and a driver, only from planned.
  refused(await call("POST", `/api/v1/dispatch/runs/${r2.id}/dispatch`), 409, "a run needs both a truck and a driver before it can be dispatched");
  refused(await call("POST", `/api/v1/dispatch/runs/${r4.id}/dispatch`), 409, "illegal status transition");
  refused(await call("POST", `/api/v1/dispatch/runs/${r4.id}/complete`), 409, "the run still has stops that are not finished");
  refused(await call("POST", `/api/v1/dispatch/runs/${r4.id}/crew`, { truck_id: "", driver_id: "" }), 409, "run cannot be re-crewed");
  // The matrix, then the per status checks.
  refused(await call("POST", `/api/v1/dispatch/stops/${stop("ORD-0029").id}/status`, { status: "DELIVERED", reason: "" }), 409, "a delivery must carry a proof-of-delivery attestation");
  refused(await call("POST", `/api/v1/dispatch/stops/${stop("ORD-0029").id}/status`, { status: "LOADED", reason: "" }), 409, "the stop is not assigned to a run");
  refused(await call("POST", `/api/v1/dispatch/stops/${stop("ORD-0012").id}/status`, { status: "EN_ROUTE", reason: "" }), 409, "illegal status transition");
  refused(await call("POST", `/api/v1/dispatch/stops/${stop("ORD-0009").id}/status`, { status: "FAILED", reason: " " }), 400, "a failed stop needs a reason");
  // Assign: the stop, the run, the date.
  refused(await call("POST", `/api/v1/dispatch/stops/${stop("ORD-0009").id}/assign`, { run_id: r1.id, stop_no: 0 }), 409, "stop cannot be re-assigned");
  refused(await call("POST", `/api/v1/dispatch/stops/${stop("ORD-0030").id}/assign`, { run_id: r1.id, stop_no: 0 }), 409, "the stop is scheduled for");
  refused(await call("POST", `/api/v1/dispatch/stops/${stop("ORD-0029").id}/assign`, { run_id: r4.id, stop_no: 0 }), 409, "stops cannot be added to a");
  // Reopen: a reason, and an ask is already open.
  refused(await call("POST", `/api/v1/dispatch/runs/${r1.id}/reopen`, { reason: " " }), 422, "say why this load is being reopened");
  refused(await call("POST", `/api/v1/dispatch/stops/${stop("ORD-0029").id}/reopen`, { reason: "x" }), 409, "is already open, nobody has committed it");
  // Reschedule: the date, the day, the reason, and a stop past planning.
  refused(await call("POST", `/api/v1/dispatch/stops/${stop("ORD-0029").id}/reschedule`, { to_date: "tomorrow", reason: "x" }), 400, "to_date must be YYYY-MM-DD");
  refused(await call("POST", `/api/v1/dispatch/stops/${stop("ORD-0029").id}/reschedule`, { to_date: (await world()).today, reason: "x" }), 400, "that is already the scheduled date");
  refused(await call("POST", `/api/v1/dispatch/stops/${stop("ORD-0029").id}/reschedule`, { to_date: "2000-01-01", reason: "x" }), 400, "a delivery cannot be promised for a past date");
  refused(await call("POST", `/api/v1/dispatch/stops/${stop("ORD-0029").id}/reschedule`, { to_date: "2099-01-01", reason: " " }), 400, "say why the delivery is moving");
  refused(await call("POST", `/api/v1/dispatch/stops/${stop("ORD-0012").id}/reschedule`, { to_date: "2099-01-01", reason: "x" }), 409, "can no longer be changed");
  // The caller who may not reopen or commit: the core's own sentences.
  await post("/_stub/caller", { can_reopen: false });
  refused(await call("POST", `/api/v1/dispatch/runs/${r1.id}/reopen`, { reason: "x" }), 403, "only a dispatch manager can reopen a committed load");
  refused(await call("POST", `/api/v1/dispatch/stops/${stop("ORD-0029").id}/reopen`, { reason: "x" }), 403, "only a dispatch manager can reopen a committed load");
  refused(await call("POST", `/api/v1/dispatch/runs/${r1.id}/crew`, { truck_id: r1.truck_id, driver_id: r1.driver_id }), 409, "this load is committed");
  await post("/_stub/caller", { can_commit: false });
  refused(await call("POST", `/api/v1/dispatch/stops/${stop("ORD-0029").id}/commit`, { scheduled_date: "" }), 403, "a salesperson can request a delivery date, only dispatch can commit one");
  // Not signed in.
  const anon = await fetch(`${base}/api/v1/dispatch/loadboard`);
  assert.equal(anon.status, 401);
  holds("Error", await anon.json());
});

test("scheduling a delivery: the order resolves by its number, the stop fits the contract, and every refusal is the core's sentence", async () => {
  await reset();
  const today = (await world()).today;
  const tomorrow = new Date(`${today}T12:00:00`);
  tomorrow.setDate(tomorrow.getDate() + 1);
  const day = `${tomorrow.getFullYear()}-${String(tomorrow.getMonth() + 1).padStart(2, "0")}-${String(tomorrow.getDate()).padStart(2, "0")}`;
  const input = (over) => ({
    order_id: "ORD-0040", scheduled_date: day, time_window: "AM", contact_name: "", contact_phone: "", address_line: "12 Cedar Court", city: "Julian", postal_code: "92036",
    latitude: null, longitude: null, instructions: "", commit_state: "REQUESTED", requested_date: "", weight_kg: null, volume_m3: null, pallet_positions: null, ...over,
  });
  const made = await call("POST", "/api/v1/dispatch/stops", input());
  assert.equal(made.status, 201);
  holds("DispatchStop", made.json);
  assert.equal(made.json.order_no, "ORD-0040");
  assert.equal(made.json.commit_state, "REQUESTED");
  assert.equal(made.json.requested_date, day);
  assert.equal(made.json.status, "READY");
  const board = await call("GET", `/api/v1/dispatch/board?date=${day}`);
  assert.ok(board.json.unassigned.some((s) => s.order_no === "ORD-0040"), "the new stop is on that day's board, not on a truck");
  refused(await call("POST", "/api/v1/dispatch/stops", input({ order_id: "ORD-0040" })), 409, "this order already has a delivery scheduled for");
  refused(await call("POST", "/api/v1/dispatch/stops", input({ order_id: " " })), 400, "enter an order number (like #SO-TRT-1041) or an order id");
  refused(await call("POST", "/api/v1/dispatch/stops", input({ order_id: "banana" })), 400, "is not an order number, enter a number like #SO-TRT-1041, or paste the order id");
  refused(await call("POST", "/api/v1/dispatch/stops", input({ order_id: "ORD-9999" })), 404, "not found");
  refused(await call("POST", "/api/v1/dispatch/stops", input({ order_id: "ORD-0041" })), 409, "confirm the order before scheduling a delivery for it");
  refused(await call("POST", "/api/v1/dispatch/stops", input({ order_id: "ORD-0042" })), 409, "cannot be scheduled");
  refused(await call("POST", "/api/v1/dispatch/stops", input({ order_id: "ORD-0043", scheduled_date: "" })), 400, "scheduled_date is required (YYYY-MM-DD)");
  refused(await call("POST", "/api/v1/dispatch/stops", input({ order_id: "ORD-0043", scheduled_date: "soon" })), 400, "scheduled_date must be YYYY-MM-DD");
  refused(await call("POST", "/api/v1/dispatch/stops", input({ order_id: "ORD-0043", scheduled_date: "2099-01-01" })), 422, "planning horizon");
  refused(await call("POST", "/api/v1/dispatch/stops", input({ order_id: "ORD-0043", latitude: 32.7 })), 400, "latitude and longitude must be given together");
});

test("the registry: trucks and drivers are created and rewritten, retired and brought back, and every answer fits the contract", async () => {
  await reset();
  const branch = (await call("GET", "/api/v1/branches")).json[0].id;
  const truckIn = (over) => ({ id: "", branch_id: branch, code: "Truck 05", name: "Truck 05, stake body", truck_type: "FLATBED", capacity_kg: 5000, is_active: true, ...over });
  const made = await call("POST", "/api/v1/dispatch/trucks", truckIn());
  assert.equal(made.status, 201);
  holds("DispatchCreatedRef", made.json);
  const listed = await call("GET", "/api/v1/dispatch/trucks");
  assert.equal(listed.json.items.length, 5);
  holds("DispatchTruckPage", listed.json);
  const row = listed.json.items.find((t) => t.id === made.json.id);
  const renamed = await call("PUT", `/api/v1/dispatch/trucks/${made.json.id}`, { ...row, name: "Truck 05, longer deck", capacity_kg: 6000, capacity_m3: 9, pallet_positions: 8 });
  assert.equal(renamed.status, 200);
  holds("DispatchTruck", renamed.json);
  assert.equal(renamed.json.name, "Truck 05, longer deck");
  assert.equal(renamed.json.capacity_m3, 9);
  const retired = await call("PUT", `/api/v1/dispatch/trucks/${made.json.id}`, { ...renamed.json, is_active: false });
  assert.equal(retired.json.is_active, false);
  assert.equal((await call("GET", "/api/v1/dispatch/trucks")).json.items.length, 4, "a retired truck leaves the pick list");
  const all = await call("GET", "/api/v1/dispatch/trucks?include_inactive=true");
  assert.equal(all.json.items.length, 5, "the registry still lists it");
  holds("DispatchTruckPage", all.json);
  const driverIn = (over) => ({ id: "", branch_id: branch, name: "Driver D (demo)", phone: "619-555-0144", licence_class: "B", is_active: true, ...over });
  const driver = await call("POST", "/api/v1/dispatch/drivers", driverIn());
  assert.equal(driver.status, 201);
  holds("DispatchCreatedRef", driver.json);
  const drivers = await call("GET", "/api/v1/dispatch/drivers?include_inactive=true");
  holds("DispatchDriverPage", drivers.json);
  const drow = drivers.json.items.find((d) => d.id === driver.json.id);
  const linked = await call("PUT", `/api/v1/dispatch/drivers/${driver.json.id}`, { ...drow, user_id: "d0200000-0000-4000-9000-000000000001", is_active: false });
  assert.equal(linked.status, 200);
  holds("DispatchDriver", linked.json);
  assert.equal(linked.json.user_id, "d0200000-0000-4000-9000-000000000001");
  assert.equal((await call("GET", "/api/v1/dispatch/drivers")).json.items.length, 3);
  // The core's sentences.
  refused(await call("POST", "/api/v1/dispatch/trucks", truckIn({ code: " " })), 400, "a truck needs a code");
  refused(await call("POST", "/api/v1/dispatch/trucks", truckIn({ code: "Truck 06", truck_type: "SPACESHIP" })), 400, "unknown truck_type");
  refused(await call("POST", "/api/v1/dispatch/trucks", truckIn({ code: "Truck 06", capacity_kg: -1 })), 400, "capacity_kg must not be negative");
  refused(await call("POST", "/api/v1/dispatch/trucks", truckIn({ code: "Truck 01" })), 409, "a truck with that code already exists at this branch");
  refused(await call("PUT", `/api/v1/dispatch/trucks/${made.json.id}`, { ...row, code: "Truck 01" }), 409, "a truck with that code already exists at this branch");
  refused(await call("PUT", `/api/v1/dispatch/trucks/${made.json.id}`, { ...row, branch_id: "other" }), 400, "a truck's branch does not move");
  refused(await call("POST", "/api/v1/dispatch/drivers", driverIn({ name: " " })), 400, "a driver needs a name");
  refused(await call("POST", "/api/v1/dispatch/drivers", driverIn({ user_id: "not-an-id" })), 400, "user_id must be a user id, or left out for a driver with no login");
  refused(await call("PUT", `/api/v1/dispatch/drivers/${driver.json.id}`, { ...drow, branch_id: "other" }), 400, "a driver's branch does not move");
  // Below the manage level the role gate answers a bare Forbidden on the writes and still serves the reads.
  await post("/_stub/caller", { can_manage: false });
  refused(await call("POST", "/api/v1/dispatch/trucks", truckIn({ code: "Truck 07" })), 403, "Forbidden");
  refused(await call("PUT", `/api/v1/dispatch/drivers/${driver.json.id}`, drow), 403, "Forbidden");
  assert.equal((await call("GET", "/api/v1/dispatch/trucks")).status, 200);
});

test("will call: a code is minted once, listed without its token, cancelled once, and every refusal is the core's sentence", async () => {
  await reset();
  const mint = (order) => call("POST", "/api/v1/dispatch/will-call", { order_id: order, ttl_hours: 0 });
  const minted = await mint("ORD-0044");
  assert.equal(minted.status, 201);
  holds("DispatchMintedPickup", minted.json);
  assert.equal(minted.json.status, "ACTIVE");
  assert.ok(minted.json.token.length >= 32, "the raw token rides the mint answer");
  const listed = await call("GET", "/api/v1/dispatch/will-call?order_id=ORD-0044");
  assert.equal(listed.status, 200);
  holds("DispatchWillCallPage", listed.json);
  assert.equal(listed.json.items.length, 1);
  assert.equal("token" in listed.json.items[0], false, "the list never carries a token");
  const history = await call("GET", "/api/v1/dispatch/will-call?order_id=ORD-0047");
  holds("DispatchWillCallPage", history.json);
  assert.deepEqual(history.json.items.map((w) => w.status).sort(), ["CONFIRMED", "REVOKED"]);
  refused(await mint("ORD-0044"), 409, "this order already has a live pickup code, revoke it before printing another");
  const revoked = await call("POST", `/api/v1/dispatch/will-call/${minted.json.id}/revoke`);
  assert.equal(revoked.status, 200);
  holds("DispatchWillCallRevokeResult", revoked.json);
  refused(await call("POST", `/api/v1/dispatch/will-call/${minted.json.id}/revoke`), 409, "this pickup code is already");
  refused(await call("POST", "/api/v1/dispatch/will-call/not-an-id/revoke"), 404, "not found");
  refused(await mint("ORD-0043"), 409, "not will-call, a rack tag prints only for a will-call order");
  refused(await mint("ORD-0041"), 409, "confirm the order before printing a pickup code");
  refused(await mint("ORD-0042"), 409, "this order was cancelled");
  refused(await mint("ORD-0045"), 409, "nothing has been picked for this order yet");
  refused(await mint("ORD-0046"), 409, "This order cannot reach picking until the balance is paid.");
  refused(await mint("ORD-9999"), 404, "not found");
  refused(await mint(" "), 400, "enter an order number (like #SO-TRT-1041) or an order id");
  refused(await mint("banana"), 400, "is not an order number");
  refused(await call("GET", "/api/v1/dispatch/will-call?order_id=ORD-9999"), 404, "not found");
  await post("/_stub/caller", { can_manage: false });
  refused(await call("POST", `/api/v1/dispatch/will-call/${history.json.items[0].id}/revoke`), 403, "Forbidden");
  assert.equal((await mint("ORD-0044")).status, 201, "minting is work, not manage");
});

test("the dispatch rules: read, rewritten, each change recorded with who and when, and every refusal is the core's sentence", async () => {
  await reset();
  const branch = (await call("GET", "/api/v1/branches")).json[0].id;
  const read = await call("GET", `/api/v1/dispatch/policy?branch_id=${branch}`);
  assert.equal(read.status, 200);
  holds("DispatchPolicy", read.json);
  const empty = await call("GET", `/api/v1/dispatch/policy/history?branch_id=${branch}`);
  holds("DispatchPolicyChangePage", empty.json);
  assert.deepEqual(empty.json.items, []);
  const saved = await call("PUT", "/api/v1/dispatch/policy", { ...read.json, branch_id: branch, commit_prompt_hours: 24, reopen_policy: "ANYONE" });
  assert.equal(saved.status, 200);
  holds("DispatchPolicy", saved.json);
  assert.equal(saved.json.commit_prompt_hours, 24);
  const board = await call("GET", "/api/v1/dispatch/loadboard");
  assert.equal(board.json.policy.reopen_policy, "ANYONE", "the board reads the rule the screen just saved");
  const trail = await call("GET", `/api/v1/dispatch/policy/history?branch_id=${branch}`);
  holds("DispatchPolicyChangePage", trail.json);
  assert.deepEqual(trail.json.items.map((c) => [c.field, c.before, c.after]).sort(), [["commit_prompt_hours", "48", "24"], ["reopen_policy", "MANAGER", "ANYONE"]]);
  assert.ok(trail.json.items.every((c) => c.who && c.when), "every change carries who and when");
  const same = await call("PUT", "/api/v1/dispatch/policy", { ...saved.json, branch_id: branch });
  assert.equal(same.status, 200);
  assert.equal((await call("GET", `/api/v1/dispatch/policy/history?branch_id=${branch}`)).json.items.length, 2, "saving nothing new records nothing");
  const put = (over) => call("PUT", "/api/v1/dispatch/policy", { ...saved.json, branch_id: branch, ...over });
  refused(await put({ commit_prompt_hours: 999 }), 422, "commit_prompt_hours must be between 0 and 336");
  refused(await put({ reopen_policy: "EVERYONE" }), 422, "reopen_policy must be ANYONE, MANAGER or NOBODY");
  refused(await put({ load_value_visibility: "SOME" }), 422, "cost_visibility must be MANAGER_UP, ALL_STAFF or HIDDEN");
  refused(await put({ commit_horizon_weeks: 99 }), 422, "commit_horizon_weeks must be between 1 and 26");
  refused(await put({ working_view_days: 200 }), 422, "working_view_days must be between 1 and 90");
  refused(await put({ driver_session_hours: 500 }), 422, "driver_session_hours must be between 1 and");
  refused(await put({ freight_split_mode: "ODD" }), 422, "freight_split_mode must be EVEN or MANUAL");
  // A manager who is not on the setters list meets the wall; the read stays open and the trail is a manager's.
  await post("/_stub/caller", { can_set_policy: false });
  refused(await put({ commit_prompt_hours: 12 }), 403, "Forbidden");
  assert.equal((await call("GET", `/api/v1/dispatch/policy?branch_id=${branch}`)).status, 200);
  await post("/_stub/caller", { can_set_policy: true, can_manage: false });
  refused(await call("GET", `/api/v1/dispatch/policy/history?branch_id=${branch}`), 403, "Forbidden");
});

test("the manifest and the scan trail: a run's document, a print recorded, an order's pallets by stage, and the core's sentences", async () => {
  await reset();
  const { stop, run } = await ids();
  const doc = await fetch(`${base}/api/v1/dispatch/runs/${run("RUN-PLM-1").id}/manifest`, { headers: { Authorization: "Bearer stub-token", "X-Org": "hh-demo" } });
  assert.equal(doc.status, 200);
  assert.match(doc.headers.get("content-type") ?? "", /^text\/html/);
  assert.equal(doc.headers.get("cache-control"), "no-store", "a manifest is a point in time document");
  const html = await doc.text();
  assert.ok(html.includes("Run manifest RUN-PLM-1"));
  assert.ok(html.includes("ORD-0012"));
  const printed = await call("POST", `/api/v1/dispatch/runs/${run("RUN-PLM-1").id}/manifest/print`);
  assert.equal(printed.status, 201);
  holds("DispatchPrintJobRef", printed.json);
  assert.equal(printed.json.doc_type, "DELIVERY_DOC");
  refused(await call("GET", "/api/v1/dispatch/runs/00000000-0000-4000-9000-000000000000/manifest"), 404, "not found");
  refused(await call("POST", "/api/v1/dispatch/runs/00000000-0000-4000-9000-000000000000/manifest/print"), 404, "not found");
  const trail = await call("GET", `/api/v1/dispatch/scan-trail?order_id=${stop("ORD-0012").order_id}`);
  assert.equal(trail.status, 200);
  holds("DispatchScanTrail", trail.json);
  assert.equal(trail.json.complete, false);
  assert.equal(trail.json.loads.length, 2);
  assert.deepEqual(trail.json.loads.map((l) => l.furthest).sort(), ["DELIVERED", "PICKED"]);
  const quiet = await call("GET", `/api/v1/dispatch/scan-trail?order_id=${stop("ORD-0029").order_id}`);
  holds("DispatchScanTrail", quiet.json);
  assert.deepEqual(quiet.json.loads, []);
  refused(await call("GET", "/api/v1/dispatch/scan-trail"), 400, "order_id is required");
  refused(await call("GET", "/api/v1/dispatch/scan-trail?order_id=00000000-0000-4000-9000-000000000000"), 404, "not found");
});
