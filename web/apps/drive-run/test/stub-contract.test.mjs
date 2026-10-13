// The stub against the contract and against the core, field by field.
//
// A stub that drifts from the server makes every browser leg a flattering
// lie, so this test starts the stub, makes every call the driver makes, and
// holds each answer to the generated wire descriptors: required fields
// present, types and vocabularies right, and no field the contract does not
// declare (the descriptors are open objects, so the undeclared field check is
// this file's own). Then each refusal is held to the core's words: the status
// the dispatch taxonomy grades, the envelope, and the sentence itself, which
// must appear in the core's source. A refusal the stub invented fails here.
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
const dispatchDir = join(appDir, "..", "..", "..", "core", "internal", "dispatch");
const httpxDir = join(appDir, "..", "..", "..", "core", "internal", "platform", "httpx");

const coreSource = [dispatchDir, httpxDir]
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
  child = spawn(process.execPath, [join(appDir, "test", "stub", "drive-stub.mjs")], {
    env: { DRIVE_STUB_PORT: String(port) },
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

async function call(method, path, { body, raw, type } = {}) {
  const headers = { Authorization: "Bearer stub-token", "X-Org": "hh-demo" };
  let payload;
  if (body !== undefined) {
    headers["Content-Type"] = "application/json";
    payload = JSON.stringify(body);
  } else if (raw !== undefined) {
    headers["Content-Type"] = type;
    payload = raw;
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
const day = () => new Intl.DateTimeFormat("en-CA", { timeZone: "America/Toronto", year: "numeric", month: "2-digit", day: "2-digit" }).format(new Date());

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
    // Keys are judged against the union of the legs; each leg's own props recurse below.
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
  assert.deepEqual(describeHits(scanPayload(schemaName, JSON.stringify(value))), [], `${schemaName} carries money`);
}

function refused(res, status, fragment) {
  assert.equal(res.status, status);
  holds("Error", res.json);
  assert.ok(res.json.error.includes(fragment), `expected "${fragment}" in "${res.json.error}"`);
  // A sentence the Go source builds from a constant (a bound in a %d) is read with the number as the verb.
  const template = fragment.replace(/\b200\b/, "%d");
  assert.ok(coreSource.includes(fragment) || coreSource.includes(template), `the core's source does not contain the sentence "${fragment}": the stub invented it`);
}

const PNG = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==", "base64");

test("the demo way in answers the contract's shapes", async () => {
  const flows = await (await fetch(`${base}/api/v1/demo/flows`)).json();
  holds("DemoFlowList", flows);
  const minted = await call("POST", "/api/v1/demo/session", { body: { flow: "driver" } });
  assert.equal(minted.status, 200);
  assert.ok(typeof minted.json.token === "string");
  assert.deepEqual(Object.keys(minted.json).sort(), ["flow", "token", "user"]);
});

test("my runs answers the run detail page: runs, stops, and the lines that come off the truck", async () => {
  await reset();
  const res = await call("GET", `/api/v1/dispatch/my-runs?date=${day()}`);
  assert.equal(res.status, 200);
  holds("DispatchRunDetailPage", res.json);
  assert.equal(res.json.items.length, 1);
  const run = res.json.items[0];
  assert.equal(run.stops.length, 3);
  for (const stop of run.stops) {
    assert.ok(Array.isArray(run.stop_lines[stop.id]), "every stop has its lines keyed by its id");
    assert.equal("value_cents" in stop, false);
  }
  assert.equal("gauge" in run, false, "the driver's read builds no gauge");
});

test("a stop moves along the lifecycle and every answer fits the contract", async () => {
  await reset();
  const runs = (await call("GET", `/api/v1/dispatch/my-runs?date=${day()}`)).json.items;
  const id = runs[0].stops[0].id;
  const detail = await call("GET", `/api/v1/dispatch/stops/${id}`);
  holds("DispatchStopDetail", detail.json);
  const loaded = await call("POST", `/api/v1/dispatch/stops/${id}/status`, { body: { status: "LOADED", reason: "" } });
  assert.equal(loaded.status, 200);
  holds("DispatchStop", loaded.json);
  const enRoute = await call("POST", `/api/v1/dispatch/stops/${id}/status`, { body: { status: "EN_ROUTE", reason: "" } });
  holds("DispatchStop", enRoute.json);
  const photo = await call("POST", `/api/v1/dispatch/stops/${id}/photos?caption=drop`, { raw: PNG, type: "image/png" });
  assert.equal(photo.status, 201);
  holds("DispatchPhotoMeta", photo.json);
  const pod = await call("POST", `/api/v1/dispatch/stops/${id}/pod`, { body: { received_by: "Receiving clerk", affirmed: true } });
  assert.equal(pod.status, 200);
  holds("DispatchStopDetail", pod.json);
  assert.equal(pod.json.status, "DELIVERED");
  const folded = await call("POST", `/api/v1/dispatch/stops/${id}/attach-photos`);
  holds("DispatchStopDetail", folded.json);
});

test("closing the run answers the run, once every stop has an answer", async () => {
  await reset();
  const runs = (await call("GET", `/api/v1/dispatch/my-runs?date=${day()}`)).json.items;
  for (const stop of runs[0].stops) {
    await call("POST", `/api/v1/dispatch/stops/${stop.id}/status`, { body: { status: "LOADED", reason: "" } });
    await call("POST", `/api/v1/dispatch/stops/${stop.id}/status`, { body: { status: "EN_ROUTE", reason: "" } });
    const failed = await call("POST", `/api/v1/dispatch/stops/${stop.id}/status`, { body: { status: "FAILED", reason: "Nobody on site" } });
    holds("DispatchStop", failed.json);
    assert.equal(failed.json.failure_reason, "Nobody on site");
  }
  const closed = await call("POST", `/api/v1/dispatch/runs/${runs[0].id}/complete`);
  assert.equal(closed.status, 200);
  holds("DispatchRun", closed.json);
  assert.equal(closed.json.status, "COMPLETED");
});

test("the refusals carry the core's statuses, envelope and sentences", async () => {
  await reset();
  const runs = (await call("GET", `/api/v1/dispatch/my-runs?date=${day()}`)).json.items;
  const [a, b] = runs[0].stops;
  // The status route: delivered is not reachable here, then the matrix, then the per status checks.
  refused(await call("POST", `/api/v1/dispatch/stops/${a.id}/status`, { body: { status: "DELIVERED", reason: "" } }), 409, "a delivery must carry a proof-of-delivery attestation");
  refused(await call("POST", `/api/v1/dispatch/stops/${a.id}/status`, { body: { status: "EN_ROUTE", reason: "" } }), 409, "illegal status transition");
  await call("POST", `/api/v1/dispatch/stops/${a.id}/status`, { body: { status: "LOADED", reason: "" } });
  await scenario("run-planned");
  refused(await call("POST", `/api/v1/dispatch/stops/${a.id}/status`, { body: { status: "EN_ROUTE", reason: "" } }), 409, "the run has not been dispatched");
  await reset();
  await call("POST", `/api/v1/dispatch/stops/${a.id}/status`, { body: { status: "LOADED", reason: "" } });
  await call("POST", `/api/v1/dispatch/stops/${a.id}/status`, { body: { status: "EN_ROUTE", reason: "" } });
  refused(await call("POST", `/api/v1/dispatch/stops/${a.id}/status`, { body: { status: "FAILED", reason: "  " } }), 400, "a failed stop needs a reason");
  // Proof of delivery: the name, then the confirmation.
  refused(await call("POST", `/api/v1/dispatch/stops/${a.id}/pod`, { body: { received_by: " ", affirmed: true } }), 400, "type the full name of the person receiving this delivery");
  refused(await call("POST", `/api/v1/dispatch/stops/${a.id}/pod`, { body: { received_by: "e".repeat(201), affirmed: true } }), 400, "that name is too long, type the full name in at most 200 bytes");
  refused(await call("POST", `/api/v1/dispatch/stops/${a.id}/pod`, { body: { received_by: "Clerk\u202e", affirmed: true } }), 400, "the name cannot hold control characters, type the full name as plain text");
  refused(await call("POST", `/api/v1/dispatch/stops/${a.id}/pod`, { body: { received_by: "Clerk", affirmed: false } }), 400, "the recipient must confirm the delivery before it can be signed");
  refused(await call("POST", `/api/v1/dispatch/stops/${b.id}/pod`, { body: { received_by: "Clerk", affirmed: true } }), 409, "illegal status transition");
  // Photos: the stop must be on the road or settled, then the type, then the bytes.
  refused(await call("POST", `/api/v1/dispatch/stops/${b.id}/photos`, { raw: PNG, type: "image/png" }), 409, "has nothing to photograph yet");
  refused(await call("POST", `/api/v1/dispatch/stops/${a.id}/photos`, { raw: Buffer.from("x"), type: "text/plain" }), 400, "a proof photo must be a jpeg, png or webp");
  refused(await call("POST", `/api/v1/dispatch/stops/${a.id}/photos`, { raw: Buffer.alloc(0), type: "image/png" }), 400, "the photo is empty");
  refused(await call("POST", `/api/v1/dispatch/stops/${a.id}/photos`, { raw: Buffer.alloc(4 * 1024 * 1024 + 1), type: "image/png" }), 400, "the photo is too large");
  refused(await call("POST", `/api/v1/dispatch/stops/${a.id}/photos`, { raw: Buffer.alloc(4 * 1024 * 1024 + 8), type: "image/png" }), 413, "the photo is too large");
  // Closing: the run's own gate names the stop, and a driver's gate says who closes a cancelled one.
  refused(await call("POST", `/api/v1/dispatch/runs/${runs[0].id}/complete`), 409, "every stop must be delivered or marked not delivered before this run can be closed");
  await reset();
  await scenario("has-cancelled");
  for (const stop of runs[0].stops.slice(0, 2)) {
    await call("POST", `/api/v1/dispatch/stops/${stop.id}/status`, { body: { status: "LOADED", reason: "" } });
    await call("POST", `/api/v1/dispatch/stops/${stop.id}/status`, { body: { status: "EN_ROUTE", reason: "" } });
    await call("POST", `/api/v1/dispatch/stops/${stop.id}/status`, { body: { status: "FAILED", reason: "x" } });
  }
  refused(await call("POST", `/api/v1/dispatch/runs/${runs[0].id}/complete`), 409, "and that is not an outcome a driver records, ask dispatch to close this run");
  // The day and the caller.
  refused(await call("GET", "/api/v1/dispatch/my-runs?date=tomorrow"), 400, "date must be YYYY-MM-DD");
  await reset();
  await scenario("not-driver");
  refused(await call("GET", `/api/v1/dispatch/my-runs?date=${day()}`), 404, "not found");
  const anon = await fetch(`${base}/api/v1/dispatch/my-runs`);
  assert.equal(anon.status, 401);
  refused({ status: 401, json: await anon.json() }, 401, "unauthenticated");
});
