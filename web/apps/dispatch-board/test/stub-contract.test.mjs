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

const coreSource = ["dispatch", join("platform", "httpx")]
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
