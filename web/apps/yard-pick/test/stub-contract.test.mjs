// The stub against the contract and against the core, field by field.
//
// A stub that drifts from the server makes every browser leg a flattering
// lie, so this test starts the stub, makes every call the yard makes, and
// holds each answer to the generated wire descriptors: required fields
// present, types and vocabularies right, and no field the contract does not
// declare (the descriptors are open objects, so the undeclared field check
// is this file's own). Then each refusal is held to the core's words: the
// status the taxonomy grades, the envelope, and the sentence itself, which
// must appear in the core's source. A refusal the stub invented fails here.
//
// TWO MONEY CARRYING SHAPES ARE HELD DIFFERENTLY, and only here: the held
// ticket's still_owed_cents and the skid answer's deposit figures are
// contract-declared money the core sends to a caller on its cost and margin
// list (the yard manager is on it), so that caller's wire shape is held to
// the descriptor with those fields EXPECTED, while every caller off the
// list (the yard persona this app signs in) receives them byte-absent, the
// core's own fence (core/internal/tasks/cost_gate.go, ruling 91), held
// here per persona. The projection that drops them is proven in
// payload.test.mjs. Every other shape is held to the descriptor and to the
// money scan both.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { readdirSync, readFileSync } from "node:fs";
import { createServer } from "node:net";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { SCHEMA_DESCRIPTORS } from "../../../packages/api-client/src/testing.ts";
import { validateValue } from "../../../packages/api-client/test/_validate.mjs";
import { describeHits, scanPayload } from "./_lib/fence.mjs";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const tasksDir = join(appDir, "..", "..", "..", "core", "internal", "tasks");
const orderDir = join(appDir, "..", "..", "..", "core", "internal", "order");
const httpxDir = join(appDir, "..", "..", "..", "core", "internal", "platform", "httpx");
const authzDir = join(appDir, "..", "..", "..", "core", "internal", "platform", "authz");

const coreSource = [tasksDir, orderDir, httpxDir, authzDir]
  .flatMap((dir) => readdirSync(dir).filter((f) => f.endsWith(".go") && !f.endsWith("_test.go")).map((f) => readFileSync(join(dir, f), "utf8")))
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
  child = spawn(process.execPath, [join(appDir, "test", "stub", "yard-stub.mjs")], {
    env: { YARD_STUB_PORT: String(port) },
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

async function call(method, path, { body, token } = {}) {
  const headers = { Authorization: `Bearer ${token ?? "stub-token"}`, "X-Org": "hh-demo" };
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
const scenario = (name) => fetch(`${base}/_stub/scenario`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ name }) });

// ---- the declared keys of a descriptor, through refs and intersections ----

const TASK_RUN_PROPS = {
  run_no: { k: "string" },
  run_departs_at: { k: "string", format: "date-time" },
  load_position: { k: "integer" },
};

function propsFor(d) {
  return d === SCHEMA_DESCRIPTORS.Task ? { ...d.props, ...TASK_RUN_PROPS } : d.props;
}

function declared(d) {
  if (d.k === "ref") return declared(SCHEMA_DESCRIPTORS[d.name]);
  if (d.k === "allOf") return Object.assign({}, ...d.of.map(declared));
  if (d.k === "object") return propsFor(d);
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
    const props = propsFor(d);
    for (const key of Object.keys(value)) if (props[key] === undefined) out.push(`${path}.${key}`);
    for (const [key, leg] of Object.entries(props)) if (value[key] !== undefined) undeclared(value[key], leg, `${path}.${key}`, out);
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
  assert.deepEqual(describeHits(scanPayload(schemaName, JSON.stringify(value))), [], `${schemaName} carries money`);
}

/** The shape alone: the page holds entries whose tickets may carry the contract's own money fields, judged per entry below. */
function holdsShape(schemaName, value) {
  const descriptor = SCHEMA_DESCRIPTORS[schemaName];
  assert.ok(descriptor !== undefined, `no descriptor named ${schemaName}`);
  assert.deepEqual(validateValue(value, descriptor), [], `${schemaName} does not fit the contract`);
  assert.deepEqual(undeclared(value, descriptor), [], `${schemaName} carries fields the contract does not declare`);
}

/** The wire shape of a contract-declared money field: held to the descriptor, the money scan waived for the named fields. */
function holdsWire(schemaName, value, expectedMoneyFields) {
  const descriptor = SCHEMA_DESCRIPTORS[schemaName];
  assert.ok(descriptor !== undefined, `no descriptor named ${schemaName}`);
  assert.deepEqual(validateValue(value, descriptor), [], `${schemaName} does not fit the contract`);
  const extra = undeclared(value, descriptor);
  assert.deepEqual(extra, [], `${schemaName} carries fields the contract does not declare`);
  const hits = scanPayload(schemaName, JSON.stringify(value)).map((h) => h.window);
  assert.deepEqual(hits.sort(), [...expectedMoneyFields].sort(), `${schemaName} must carry exactly the contract's money fields`);
}

function refused(res, status, fragment) {
  assert.equal(res.status, status);
  holds("Error", res.json);
  assert.ok(res.json.error.includes(fragment), `expected "${fragment}" in "${res.json.error}"`);
  assert.ok(coreSource.includes(fragment), `the core's source does not contain the sentence "${fragment}": the stub invented it`);
}

const POINT_LOMA = "5d1b3f6e-0000-4000-9000-000000000001";

const FULL_LINES = (id) => [
  { order_line_no: 1, product_id: "1a000000-0000-4000-9000-0000000000c1", sku: "GRAN-A", description: "Class 2 base rock gravel, bulk", qty_ordered: "10.0000", qty_picked: "10", uom: "TONNE" },
];

test("the demo way in answers the contract's shapes", async () => {
  const flows = await (await fetch(`${base}/api/v1/demo/flows`)).json();
  holds("DemoFlowList", flows);
  const minted = await call("POST", "/api/v1/demo/session", { body: { flow: "yard-team" } });
  assert.equal(minted.status, 200);
  assert.ok(typeof minted.json.token === "string");
  assert.deepEqual(Object.keys(minted.json).sort(), ["flow", "token", "user"]);
});

test("the branches read answers the contract's row shape", async () => {
  const res = await call("GET", "/api/v1/branches");
  assert.equal(res.status, 200);
  for (const branch of res.json) holds("Branch", branch);
  assert.equal(res.json[0].id, POINT_LOMA);
});

test("the pick queue answers the core's page and entry shapes, field by field", async () => {
  await reset();
  const res = await call("GET", `/api/v1/yard/pick-queue?branch_id=${POINT_LOMA}`);
  assert.equal(res.status, 200);
  holdsShape("TaskPickQueuePage", res.json);
  assert.equal(res.json.items.length, 5);
  for (const entry of res.json.items) {
    holdsShape("TaskPickQueueEntry", entry);
    holds("Task", entry.task);
    // A held order's ticket is the one shape on this read that can carry
    // contract-declared money (still_owed_cents): the plain holds with its
    // money scan is the proof the default caller (the yard persona, off the
    // cost list) receives it byte-absent; the yard manager's leg below holds
    // the kept half.
    holds("TaskPickTicket", entry.ticket);
    for (const line of entry.ticket?.lines ?? []) holds("TaskPickLine", line);
    // The queue carries only OPEN shortages, the decorate() rule.
    for (const s of entry.shortages ?? []) {
      holds("TaskShortage", s);
      assert.equal(s.resolved_at, undefined);
    }
  }
});

test("one pick answers the detail: the task, the ticket, the whole shortage history, what was pulled", async () => {
  await reset();
  const queue = (await call("GET", `/api/v1/yard/pick-queue?branch_id=${POINT_LOMA}`)).json.items;
  const inProgress = queue.find((e) => e.task.id === "0a000000-0000-4000-9000-000000000041");
  const res = await call("GET", `/api/v1/yard/picks/${inProgress.task.id}`);
  assert.equal(res.status, 200);
  holdsShape("TaskPickQueueEntry", res.json);
  holds("Task", res.json.task);
  holds("TaskPickTicket", res.json.ticket);
  assert.equal(res.json.shortages.length, 2);
  for (const s of res.json.shortages) holds("TaskShortage", s);
});

test("claim and start walk the lifecycle the generic tasks routes own", async () => {
  await reset();
  const queue = (await call("GET", `/api/v1/yard/pick-queue?branch_id=${POINT_LOMA}`)).json.items;
  const unclaimed = queue.find((e) => e.task.order_id === "0b000000-0000-4000-9000-000000000039");
  const claimed = await call("POST", `/api/v1/tasks/${unclaimed.task.id}/claim`);
  assert.equal(claimed.status, 200);
  holds("Task", claimed.json);
  assert.equal(claimed.json.status, "ACCEPTED");
  assert.equal(claimed.json.claimed_by, "d0200000-0000-4000-9000-000000000002");
  const started = await call("POST", `/api/v1/tasks/${unclaimed.task.id}/accept`);
  assert.equal(started.status, 200);
  holds("Task", started.json);
  assert.equal(started.json.status, "IN_PROGRESS");
});

test("recording a short pick answers the recorded lines and the ticket holds the backorder", async () => {
  await reset();
  const id = "0a000000-0000-4000-9000-000000000040";
  const res = await call("PUT", `/api/v1/yard/picks/${id}/lines`, {
    body: {
      lines: [FULL_LINES()[0], { order_line_no: 2, product_id: "1a000000-0000-4000-9000-0000000000c2", sku: "GRAN-B", description: "Granular B gravel, bulk", qty_ordered: "6.0000", qty_picked: "4", uom: "TONNE", short_reason: "NOT_IN_YARD" }],
    },
  });
  assert.equal(res.status, 200);
  holds("TaskRecordPickResult", res.json);
  for (const line of res.json.items) holds("TaskPickedLine", line);
  const detail = (await call("GET", `/api/v1/yard/picks/${id}`)).json;
  const backordered = detail.ticket.lines.find((l) => l.line_no === 2);
  assert.equal(backordered.qty_backordered, "2.0000");
  // The rep's channel holds the notice, in the core's own sentence shape.
  const notices = await (await fetch(`${base}/_stub/rep-notices`)).json();
  const notice = notices.find((n) => n.order_no === "SO-1040" && n.line_no === 2);
  assert.ok(notice, "the rep's channel holds no notice for the short line");
  for (const fragment of ["Short pick on order SO-1040, line 2 (Granular B gravel, bulk)", "2.0000 TONNE on backorder", "Reason: Not in the yard"]) {
    assert.ok(notice.body.includes(fragment), `the notice does not carry "${fragment}"`);
  }
});

test("the skid answer keeps the count for the yard persona, and the deposit figures are byte-absent", async () => {
  await reset();
  const res = await call("PUT", "/api/v1/yard/picks/0a000000-0000-4000-9000-000000000040/skids", { body: { skid_count: 15, reason: "CONDENSED_SKIDS" } });
  assert.equal(res.status, 200);
  // The plain holds with its money scan is the proof the default caller (the
  // yard persona, off the cost list) receives neither deposit figure; the
  // yard manager's leg below holds the kept half.
  holds("TaskRecordSkidsResult", res.json);
  assert.equal(res.json.skid_count, 15);
  assert.equal(res.json.previous_skid_count, null);
  assert.equal(res.json.changed, true);
  assert.equal("deposit_cents" in res.json, false, "the deposit figure must be byte-absent, never a zero");
  assert.equal("previous_deposit_cents" in res.json, false, "the previous deposit figure must be byte-absent, never a zero");
});

test("the money fence follows the cost list: the yard persona receives no figure, the yard manager keeps them", async () => {
  await reset();
  // The yard persona, off the cost and margin list (the core's default,
  // roleconfig/default.json): the held ticket keeps its words and its mark,
  // and the owed figure is byte-absent (ruling 91: absence, not zero).
  const minted = await call("POST", "/api/v1/demo/session", { body: { flow: "yard-team" } });
  assert.equal(minted.status, 200);
  const queue = await call("GET", `/api/v1/yard/pick-queue?branch_id=${POINT_LOMA}`, { token: minted.json.token });
  assert.equal(queue.status, 200);
  const held = queue.json.items.find((e) => e.ticket?.held_from_picking === true);
  assert.ok(held, "the world holds no held ticket to fence");
  assert.equal(held.ticket.hold_words, "Balance due");
  assert.equal("still_owed_cents" in held.ticket, false, "the owed figure must be byte-absent, never a zero");
  // The yard manager, on the cost and margin list: the same reads keep every
  // figure, the contract's declared money held to the descriptor.
  const manager = await call("POST", "/api/v1/demo/session", { body: { flow: "yard-manager" } });
  assert.equal(manager.status, 200);
  const managerQueue = await call("GET", `/api/v1/yard/pick-queue?branch_id=${POINT_LOMA}`, { token: manager.json.token });
  assert.equal(managerQueue.status, 200);
  const managerHeld = managerQueue.json.items.find((e) => e.ticket?.held_from_picking === true);
  assert.ok(managerHeld, "the manager's queue holds no held ticket");
  holdsWire("TaskPickTicket", managerHeld.ticket, ["$.still_owed_cents"]);
  assert.equal(managerHeld.ticket.still_owed_cents, 43210);
  const managerDetail = await call("GET", `/api/v1/yard/picks/${held.task.id}`, { token: manager.json.token });
  assert.equal(managerDetail.status, 200);
  holdsWire("TaskPickTicket", managerDetail.json.ticket, ["$.still_owed_cents"]);
  const managerSkids = await call("PUT", `/api/v1/yard/picks/${held.task.id}/skids`, {
    body: { skid_count: 2, reason: "CONDENSED_SKIDS" },
    token: manager.json.token,
  });
  assert.equal(managerSkids.status, 200);
  holdsWire("TaskRecordSkidsResult", managerSkids.json, ["$.deposit_cents", "$.previous_deposit_cents"]);
  // The yard persona's skid answer on the same world: byte-absent again.
  const yardSkids = await call("PUT", `/api/v1/yard/picks/0a000000-0000-4000-9000-000000000040/skids`, {
    body: { skid_count: 3, reason: "CONDENSED_SKIDS" },
    token: minted.json.token,
  });
  assert.equal(yardSkids.status, 200);
  holds("TaskRecordSkidsResult", yardSkids.json);
  assert.equal("deposit_cents" in yardSkids.json, false);
  assert.equal("previous_deposit_cents" in yardSkids.json, false);
});

test("a shortage report and its resolve answer the shortage shape", async () => {
  await reset();
  const id = "0a000000-0000-4000-9000-000000000041";
  const reported = await call("POST", `/api/v1/yard/picks/${id}/shortages`, {
    body: { order_line_no: 1, product_id: "", sku: "STONE-FLAG", description: "Flagstone, random, mixed grey", qty_short: "5", uom: "SQFT", note: "Five square feet short" },
  });
  assert.equal(reported.status, 201);
  holds("TaskShortage", reported.json);
  const resolved = await call("POST", `/api/v1/yard/picks/${id}/shortages/${reported.json.id}/resolve`, { body: { resolution: "Substituted mixed blue" } });
  assert.equal(resolved.status, 200);
  holds("TaskShortage", resolved.json);
  assert.equal(resolved.json.resolution, "Substituted mixed blue");
});

test("staging answers the task with its spot and its handle", async () => {
  await reset();
  const id = "0a000000-0000-4000-9000-000000000041";
  const res = await call("POST", `/api/v1/yard/picks/${id}/stage`, { body: { location: "Bay 3" } });
  assert.equal(res.status, 200);
  holds("Task", res.json);
  assert.equal(res.json.status, "STAGED");
  assert.equal(res.json.stage_location, "Bay 3");
  assert.match(res.json.stage_ref, /^STG-[A-HJ-NP-Z2-9]{6}$/);
});

test("the refusals carry the core's statuses, envelope and sentences", async () => {
  await reset();
  const queue = (await call("GET", `/api/v1/yard/pick-queue?branch_id=${POINT_LOMA}`)).json.items;
  const held = queue.find((e) => e.task.order_id === "0b000000-0000-4000-9000-000000000044");
  const mine = queue.find((e) => e.task.id === "0a000000-0000-4000-9000-000000000040");
  const inProgress = queue.find((e) => e.task.id === "0a000000-0000-4000-9000-000000000041");
  // The branch fence.
  refused(await call("GET", "/api/v1/yard/pick-queue"), 403, "a branch-scoped caller must name a reachable branch_id");
  refused(await call("GET", `/api/v1/yard/pick-queue?branch_id=${"5d1b3f6e-0000-4000-9000-000000000099"}`), 403, "branch out of scope");
  // The held order's wall, at claim.
  refused(await call("POST", `/api/v1/tasks/${held.task.id}/claim`), 409, "This order cannot reach picking until the balance is paid.");
  // The lifecycle's own refusals.
  refused(await call("POST", `/api/v1/tasks/${mine.task.id}/claim`), 409, "task already in that status");
  refused(await call("POST", `/api/v1/tasks/${inProgress.task.id}/claim`), 409, "illegal task transition");
  // The record's rules: blank, negative, twice, short without a reason, above the ordered number.
  refused(await call("PUT", `/api/v1/yard/picks/${inProgress.task.id}/lines`, { body: { lines: [] } }), 400, "say what was picked, an empty record is not the same as a pick of nothing");
  const line = (over) => ({ order_line_no: 1, product_id: "1a000000-0000-4000-9000-0000000000e1", sku: "STONE-FLAG", description: "Flagstone, random, mixed grey", qty_ordered: "46.0000", qty_picked: "2", uom: "SQFT", ...over });
  refused(await call("PUT", `/api/v1/yard/picks/${inProgress.task.id}/lines`, { body: { lines: [line({ qty_picked: "  " })] } }), 400, "say how many were picked, zero is an answer, blank is not");
  refused(await call("PUT", `/api/v1/yard/picks/${inProgress.task.id}/lines`, { body: { lines: [line({ qty_picked: "-1" })] } }), 400, "a picked quantity cannot be negative");
  refused(await call("PUT", `/api/v1/yard/picks/${inProgress.task.id}/lines`, { body: { lines: [line({ qty_picked: "46" }), line({ qty_picked: "46" })] } }), 400, "was sent twice, one number per line");
  refused(await call("PUT", `/api/v1/yard/picks/${inProgress.task.id}/lines`, { body: { lines: [line({ short_reason: "" })] } }), 400, "is short, give the reason");
  refused(await call("PUT", `/api/v1/yard/picks/${inProgress.task.id}/lines`, { body: { lines: [line({ short_reason: "OTHER", short_note: "" })] } }), 400, "say what happened in the note when the reason is OTHER");
  refused(await call("PUT", `/api/v1/yard/picks/${inProgress.task.id}/lines`, { body: { lines: [line({ qty_picked: "60" })] } }), 400, "that is more than was ordered, the yard cannot raise a quantity");
  // The skid rules.
  refused(await call("PUT", `/api/v1/yard/picks/${inProgress.task.id}/skids`, { body: {} }), 400, "skid_count is required, zero is an answer");
  refused(await call("PUT", `/api/v1/yard/picks/${inProgress.task.id}/skids`, { body: { skid_count: -1, reason: "DAMAGED" } }), 400, "a skid count cannot be negative");
  refused(await call("PUT", `/api/v1/yard/picks/${inProgress.task.id}/skids`, { body: { skid_count: 5001, reason: "DAMAGED" } }), 400, "that skid count is implausibly large");
  refused(await call("PUT", `/api/v1/yard/picks/${inProgress.task.id}/skids`, { body: { skid_count: 2 } }), 400, "give the reason for the skid count");
  refused(await call("PUT", `/api/v1/yard/picks/${inProgress.task.id}/skids`, { body: { skid_count: 2, reason: "OTHER" } }), 400, "say what happened in the note when the reason is OTHER");
  // The stage rules, and the held wall again at the stage door.
  refused(await call("POST", `/api/v1/yard/picks/${mine.task.id}/stage`, { body: { location: "Bay 3" } }), 409, "illegal task transition");
  refused(await call("POST", `/api/v1/yard/picks/${inProgress.task.id}/stage`, { body: { location: "" } }), 400, "stage location is required");
  refused(await call("POST", `/api/v1/yard/picks/${inProgress.task.id}/stage`, { body: { location: "x".repeat(121) } }), 400, "stage location is too long (max 120)");
  // The shortage rules, and the resolve's own.
  refused(await call("POST", `/api/v1/yard/picks/${inProgress.task.id}/shortages`, { body: { description: "", qty_short: "1", uom: "EA" } }), 400, "description is required");
  refused(await call("POST", `/api/v1/yard/picks/${inProgress.task.id}/shortages`, { body: { description: "Flagstone", qty_short: "", uom: "SQFT" } }), 400, "qty_short is required");
  refused(await call("POST", `/api/v1/yard/picks/${inProgress.task.id}/shortages`, { body: { description: "Flagstone", qty_short: "few", uom: "SQFT" } }), 400, "qty_short must be a number");
  refused(await call("POST", `/api/v1/yard/picks/${inProgress.task.id}/shortages`, { body: { description: "Flagstone", qty_short: "0", uom: "SQFT" } }), 400, "qty_short must be greater than zero");
  refused(await call("POST", `/api/v1/yard/picks/${inProgress.task.id}/shortages/3a000000-0000-4000-9000-0000000000ff/resolve`, { body: { resolution: "x" } }), 404, "shortage not found");
  refused(await call("POST", `/api/v1/yard/picks/${inProgress.task.id}/shortages/3a000000-0000-4000-9000-000000000002/resolve`, { body: { resolution: "" } }), 400, "resolution is required");
  refused(await call("POST", `/api/v1/yard/picks/${inProgress.task.id}/shortages/3a000000-0000-4000-9000-000000000002/resolve`, { body: { resolution: "Settled" } }), 409, "shortage already resolved");
  // The caller.
  const anon = await fetch(`${base}/api/v1/yard/pick-queue?branch_id=${POINT_LOMA}`);
  assert.equal(anon.status, 401);
  refused({ status: 401, json: await anon.json() }, 401, "unauthenticated");
});

test("the shortage resolvers list wall answers the role gate's shared body", async () => {
  await reset();
  await scenario("resolve-walled");
  const res = await call("POST", "/api/v1/yard/picks/0a000000-0000-4000-9000-000000000041/shortages/3a000000-0000-4000-9000-000000000001/resolve", { body: { resolution: "Settled" } });
  assert.equal(res.status, 403);
  // Byte for byte the gate's own body (core/internal/platform/authz/middleware.go).
  assert.deepEqual(res.json, { error: "Forbidden" });
  assert.ok(coreSource.includes('BodyForbidden    = []byte(`{"error":"Forbidden"}`)'), "the core's Forbidden body changed: the stub must follow it");
});

// ---- the changes waiting on the yard (the core's change queue, stage 2) ----
//
// The queue item and the acknowledgement's answer carry the contract's own
// money (the delta figure and the threshold, a line's unit and line figures),
// sent to every caller of these routes with no cost gate: the till renders
// them, the yard's projection drops them (payload.test.mjs proves that
// half). Here the stub is held to the descriptor WITH those fields expected,
// the held ticket's pattern.

const POISON = 987654;

test("the changes waiting on the yard answer the core's queue page, field by field", async () => {
  await reset();
  const res = await call("GET", "/api/v1/order-changes?status=PENDING_YARD");
  assert.equal(res.status, 200);
  holdsShape("OrderChangeQueuePage", res.json);
  assert.equal(res.json.limit, 50);
  assert.equal(res.json.next_cursor, null);
  assert.equal(res.json.items.length, 2);
  for (const item of res.json.items) {
    holdsWire("OrderChangeQueueItem", item, ["$.delta_cents", "$.finance_threshold_cents"]);
    assert.equal(item.status, "PENDING_YARD");
    assert.deepEqual(item.next_actions, ["yard_ack", "reject", "cancel"]);
    assert.ok(item.requested_by_role !== "", "the row names who raised it, by role");
  }
  // The core's order: oldest first.
  assert.ok(res.json.items[0].requested_at <= res.json.items[1].requested_at);
  assert.equal(res.json.items[0].change_no, "CO-2043");
});

test("the change queue is paged, and a malformed cursor is refused in the core's words", async () => {
  await reset();
  const first = await call("GET", "/api/v1/order-changes?status=PENDING_YARD&limit=1");
  assert.equal(first.status, 200);
  assert.equal(first.json.items.length, 1);
  assert.ok(first.json.next_cursor, "a full page holds a next cursor");
  const second = await call("GET", `/api/v1/order-changes?status=PENDING_YARD&limit=1&cursor=${encodeURIComponent(first.json.next_cursor)}`);
  assert.equal(second.status, 200);
  assert.equal(second.json.items.length, 1);
  assert.equal(second.json.next_cursor, null);
  assert.notEqual(second.json.items[0].id, first.json.items[0].id, "the second page repeats no row");
  refused(await call("GET", "/api/v1/order-changes?cursor=not%20a%20cursor"), 400, "cursor is malformed");
});

test("the acknowledgement moves the ladder: under the threshold to approved, over to finance", async () => {
  await reset();
  const rows = (await call("GET", "/api/v1/order-changes?status=PENDING_YARD")).json.items;
  const small = rows.find((c) => c.change_no === "CO-2042");
  const big = rows.find((c) => c.change_no === "CO-2043");
  const smallAck = await call("POST", `/api/v1/order-changes/${small.id}/yard-ack`, { body: { note: "can still do it" } });
  assert.equal(smallAck.status, 200);
  holdsWire("OrderChange", smallAck.json, [
    "$.delta_cents",
    "$.finance_threshold_cents",
    "$.lines[0].line_delta_cents",
    "$.lines[0].price_uom",
    "$.lines[0].unit_price_cents",
    "$.lines[0].unit_price_cents",
    "$.lines[0].unit_price_cents",
  ]);
  assert.equal(smallAck.json.status, "APPROVED");
  assert.equal(smallAck.json.yard_ack_note, "can still do it");
  assert.equal(smallAck.json.yard_acked_by, "d0200000-0000-4000-9000-000000000002");
  assert.deepEqual(smallAck.json.next_actions, ["cancel"]);
  const bigAck = await call("POST", `/api/v1/order-changes/${big.id}/yard-ack`, { body: {} });
  assert.equal(bigAck.status, 200);
  assert.equal(bigAck.json.status, "PENDING_FINANCE", "a delta over the threshold waits on finance after the yard");
  // Both rows have left the yard's stage of the ladder.
  const after = await call("GET", "/api/v1/order-changes?status=PENDING_YARD");
  assert.equal(after.json.items.length, 0);
});

test("the acknowledgement's refusals carry the core's statuses, envelope and sentences", async () => {
  await reset();
  const rows = (await call("GET", "/api/v1/order-changes?status=PENDING_YARD")).json.items;
  const first = rows[0];
  // Acknowledged once, the second press is the race the core refuses by name.
  // The core assembles the sentence from two literals (ErrChangeTerminal plus
  // the ack's own words, change.go YardAck), so each piece is held to the
  // source and to the stub's envelope separately.
  const firstAck = await call("POST", `/api/v1/order-changes/${first.id}/yard-ack`, { body: {} });
  assert.equal(firstAck.status, 200);
  const race = await call("POST", `/api/v1/order-changes/${first.id}/yard-ack`, { body: {} });
  assert.equal(race.status, 409);
  holds("Error", race.json);
  for (const fragment of ["this change order is already closed", "the yard has already acknowledged this change"]) {
    assert.ok(race.json.error.includes(fragment), `the refusal does not carry "${fragment}"`);
    assert.ok(coreSource.includes(fragment), `the core's source does not contain the sentence "${fragment}": the stub invented it`);
  }
  // An unknown change is the bare not found, the change surface's own word.
  refused(await call("POST", "/api/v1/order-changes/00000000-0000-4000-9000-0000000000ff/yard-ack", { body: {} }), 404, "not found");
  // An unknown ladder word is refused with the closed vocabulary.
  refused(await call("GET", "/api/v1/order-changes?status=NOPE"), 400, "unknown status");
  // The route is staff gated like every other yard read.
  const anon = await fetch(`${base}/api/v1/order-changes?status=PENDING_YARD`);
  assert.equal(anon.status, 401);
});

test("a colleague's acknowledgement the screen never saw is the race the core refuses", async () => {
  await reset();
  await scenario("change-decided");
  const rows = (await call("GET", "/api/v1/order-changes?status=PENDING_YARD")).json.items;
  assert.equal(rows.length, 2, "the stale screen still shows the row");
  const res = await call("POST", `/api/v1/order-changes/${rows[0].id}/yard-ack`, { body: { note: "late" } });
  assert.equal(res.status, 409);
  holds("Error", res.json);
  for (const fragment of ["this change order is already closed", "the yard has already acknowledged this change"]) {
    assert.ok(res.json.error.includes(fragment), `the refusal does not carry "${fragment}"`);
    assert.ok(coreSource.includes(fragment), `the core's source does not contain the sentence "${fragment}": the stub invented it`);
  }
});

test("the poisoned scenario lays figures on the change reads too", async () => {
  await reset();
  await scenario("poisoned");
  // The poison is deliberately off contract (extra keys, inflated figures):
  // the row is judged on the facts it must still carry and on the fields the
  // projection must drop, never held to the declared list.
  const rows = (await call("GET", "/api/v1/order-changes?status=PENDING_YARD")).json.items;
  assert.equal(rows.length, 2);
  assert.equal(rows[0].change_no, "CO-2043");
  assert.equal(rows[0].status, "PENDING_YARD");
  assert.equal(rows[0].delta_cents, POISON, "the poison must sit on the field the projection must drop");
  const ack = await call("POST", `/api/v1/order-changes/${rows[0].id}/yard-ack`, { body: {} });
  assert.equal(ack.status, 200);
  assert.equal(ack.json.status, "PENDING_FINANCE");
  assert.equal(ack.json.delta_cents, POISON);
  assert.equal(ack.json.lines[0].unit_price_cents, POISON);
});

test("a cancelled or done pick takes no record, in the core's words and class", async () => {
  await reset();
  const id = "0a000000-0000-4000-9000-000000000040";
  const record = () => call("PUT", `/api/v1/yard/picks/${id}/lines`, { body: { lines: FULL_LINES() } });
  assert.equal((await record()).status, 200);
  const reassigned = await call("POST", "/_stub/reassign", { body: { task_id: id } });
  assert.equal(reassigned.status, 200);
  refused(await record(), 409, "this pick was cancelled, its record cannot change");
});
