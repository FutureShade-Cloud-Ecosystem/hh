// The HH Drive stub's sync surface against the core's own writable sets.
//
// The stub drifted once: it admitted fields the core refuses (and the app
// sent them), so every leg passed against a server the core is not. This test
// reads the Go sets (core/internal, through test/_lib/gosets.mjs) and probes
// the stub with every name in the union of the sets and the names the app once
// sent: a name is refused as "not writable through sync" exactly when the
// core's set for that scope and op does not hold it.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:net";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { readFileSync } from "node:fs";
import { GO_SETS, PROBE_EXTRAS, goTypedNameBytes } from "./_lib/gosets.mjs";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
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

const headers = { Authorization: "Bearer stub-token", "X-Org": "hh-demo", "Content-Type": "application/json" };

async function pullIds(scope) {
  const res = await fetch(`${base}/api/v1/sync/${scope}?since=0&limit=200`, { headers });
  const json = await res.json();
  return (json.items ?? []).map((i) => i.id);
}

let opCounter = 0;
async function push(scope, op, payload) {
  opCounter += 1;
  const client_id = `00000000-0000-4000-8000-${String(opCounter).padStart(12, "0")}`;
  const res = await fetch(`${base}/api/v1/sync/${scope}`, {
    method: "POST",
    headers,
    body: JSON.stringify({ ops: [{ client_id, op, payload }] }),
  });
  const json = await res.json();
  return json.results?.[0] ?? { status: "http", error: JSON.stringify(json) };
}

const NOT_WRITABLE = /is not writable through sync/;

const UNIVERSE = () => {
  const all = new Set(PROBE_EXTRAS);
  for (const make of Object.values(GO_SETS)) for (const k of make()) all.add(k);
  all.delete("id");
  return [...all].sort();
};

async function probe(scope, op, setName, id, { base: fixed = {}, skip = [] } = {}) {
  const set = GO_SETS[setName]();
  for (const field of UNIVERSE()) {
    if (skip.includes(field) || field in fixed) continue;
    const answer = await push(scope, op, { id, ...fixed, [field]: "x" });
    const refused = answer.status === "rejected" && NOT_WRITABLE.test(String(answer.error ?? ""));
    assert.equal(refused, !set.has(field), `${setName}: field ${field} should be ${set.has(field) ? "admitted" : "refused"} by the stub exactly as the core does (answer: ${JSON.stringify(answer)})`);
  }
}

test("stops update: the stub admits exactly the core's writable fields", async () => {
  await probe("stops", "update", "stops update", (await pullIds("stops"))[0]);
});

// received_by and affirmed flip a pod create into the attestation form, so the
// photo form's probe leaves them to the attestation form's own probe below.
test("pod create (the photo form): the stub admits exactly the core's writable fields", async () => {
  await probe("pod", "create", "pod create", "7b000000-0000-4000-9000-0000000fffff", { skip: ["received_by", "affirmed"] });
});

test("pod create (the typed name attestation form): the stub admits exactly the core's writable fields", async () => {
  await probe("pod", "create", "pod attest create", "7b000000-0000-4000-9000-0000000fffff", { base: { received_by: "Probe Name" } });
});

// ---- the attestation door (pod create, {id?, stop_id, received_by, affirmed}) ----
//
// The core's door (core/internal/dispatch/syncscope_pod_attest.go) runs the one
// body both doors share, CapturePOD. These legs hold the stub to it: the stop
// is delivered by the create, the answer is the stop's id and seq, the name
// rule is the core's (read from the Go source), a replay applies nothing twice,
// and every sentence is one the Go source holds.

const coreDispatchSource = ["syncscope_pod_attest.go", "syncscope.go", "service.go", "model.go"]
  .map((f) => readFileSync(new URL(`../../../../core/internal/dispatch/${f}`, import.meta.url), "utf8"))
  .join("\n");

function assertCoreWords(answer, template) {
  assert.equal(answer.status, "rejected", JSON.stringify(answer));
  assert.ok(coreDispatchSource.includes(template), `the core's source does not hold "${template}": the stub invented it`);
  const body = template.replace(/[.*+?^${}()|[\]\\]/g, "\\$&").replace(/%d/g, "\\d+").replace(/%s/g, ".+?").replace(/%q/g, '"[^"]*"');
  assert.match(String(answer.error), new RegExp(`^(dispatch: )?${body}$`));
}

async function resetStub() {
  await fetch(`${base}/_stub/reset`, { method: "POST", headers, body: "{}" });
}

async function stopRows() {
  const res = await fetch(`${base}/api/v1/sync/stops?since=0&limit=200`, { headers });
  return (await res.json()).items ?? [];
}

/** A stop on the road: loaded, then en route, through the stops door. */
async function enRouteStop() {
  const [row] = await stopRows();
  assert.equal((await push("stops", "update", { id: row.id, status: "LOADED" })).status, "applied");
  assert.equal((await push("stops", "update", { id: row.id, status: "EN_ROUTE" })).status, "applied");
  return row.id;
}

const KEY = "00000000-0000-4000-8000-0000000fb001";

async function pushKeyed(client_id, payload) {
  const res = await fetch(`${base}/api/v1/sync/pod`, {
    method: "POST",
    headers,
    body: JSON.stringify({ ops: [{ client_id, op: "create", payload }] }),
  });
  return (await res.json()).results[0];
}

test("attestation: it delivers the stop, answers the stop's id and seq, and a replay applies nothing twice", async () => {
  await resetStub();
  const stopId = await enRouteStop();
  const first = await pushKeyed(KEY, { id: "7b000000-0000-4000-9000-0000000fb001", stop_id: stopId, received_by: "  Site Foreman  ", affirmed: true });
  assert.equal(first.status, "applied", JSON.stringify(first));
  const row = (await stopRows()).find((r) => r.id === stopId);
  assert.equal(row.data.status, "DELIVERED");
  assert.equal(row.data.pod_received_by, "Site Foreman");
  assert.ok(row.data.pod_at, "the server stamps pod_at");
  assert.equal(first.seq, row.seq, "the result's seq is the stop's seq");
  const again = await pushKeyed(KEY, { id: "7b000000-0000-4000-9000-0000000fb001", stop_id: stopId, received_by: "Someone Else", affirmed: true });
  assert.equal(again.status, "applied");
  assert.equal(again.seq, first.seq);
  assert.equal((await stopRows()).find((r) => r.id === stopId).data.pod_received_by, "Site Foreman", "the replay changed nothing");
  const second = await push("pod", "create", { stop_id: stopId, received_by: "Another", affirmed: true });
  assertCoreWords({ ...second, status: second.status }, "illegal status transition");
});

test("attestation: the name rule is the core's bound, read from the Go source", async () => {
  await resetStub();
  const stopId = await enRouteStop();
  const bound = goTypedNameBytes();
  assert.equal(bound, 200);
  const long = await push("pod", "create", { stop_id: stopId, received_by: "e".repeat(bound + 1), affirmed: true });
  assertCoreWords(long, "that name is too long, type the full name in at most %d bytes");
  for (const bad of ["Clerk\u0000", "Cl\terk", "Cl\nerk", "Clerk\u007f", "Clerk\u202e", "Clerk\u2067"]) {
    assertCoreWords(await push("pod", "create", { stop_id: stopId, received_by: bad, affirmed: true }), "the name cannot hold control characters, type the full name as plain text");
  }
  assertCoreWords(await push("pod", "create", { stop_id: stopId, received_by: "   ", affirmed: true }), "type the full name of the person receiving this delivery");
  assertCoreWords(await push("pod", "create", { stop_id: stopId, received_by: "Clerk", affirmed: false }), "the recipient must confirm the delivery before it can be signed");
  // Multibyte: 100 two byte characters is exactly the bound and lands.
  const exact = await push("pod", "create", { stop_id: stopId, received_by: "\u00e9".repeat(bound / 2), affirmed: true });
  assert.equal(exact.status, "applied", JSON.stringify(exact));
});

test("attestation: the shape refusals, in the core's words", async () => {
  await resetStub();
  const stopId = await enRouteStop();
  assertCoreWords(await push("pod", "create", { stop_id: "x", received_by: "Clerk", affirmed: true }), "stop_id must be a uuid");
  assertCoreWords(await push("pod", "create", { id: "x", stop_id: stopId, received_by: "Clerk", affirmed: true }), "payload id must be a uuid");
  assertCoreWords(await push("pod", "create", { stop_id: stopId, received_by: 7, affirmed: true }), "received_by must be text");
  assertCoreWords(await push("pod", "create", { stop_id: stopId, received_by: "Clerk", affirmed: "yes" }), "affirmed must be true or false");
  const unknown = await push("pod", "create", { stop_id: "7b000000-0000-4000-9000-0000000fffff", received_by: "Clerk", affirmed: true });
  assert.equal(unknown.status, "rejected");
  assert.match(String(unknown.error), /not found/);
  // The two forms do not mix: a photo leg beside the name refuses.
  assertCoreWords(await push("pod", "create", { stop_id: stopId, received_by: "Clerk", affirmed: true, caption: "x" }), "field %q is not writable through sync");
  assertCoreWords(await push("pod", "create", { stop_id: stopId, received_by: "Clerk", affirmed: true, created_at: "x" }), "field %q is not writable through sync");
});

test("attestation: a stop that cannot move to DELIVERED refuses in the direct door's words", async () => {
  await resetStub();
  const [row] = await stopRows();
  assertCoreWords(await push("pod", "create", { stop_id: row.id, received_by: "Clerk", affirmed: true }), "illegal status transition");
});

// ---- the stops pull row against the contract (UX-61) ----
//
// The stub once handed every stop a `lines` list the core's stops pull did not
// carry, so HH Drive read a load list nobody served and every leg passed
// against a flattering double. The stops pull row is the contract's
// DispatchStop; the stub's row may carry only keys that schema declares, and
// each line only keys DispatchDriverLine declares (no money member among them).

function schemaProperties(yaml, name) {
  const at = yaml.indexOf(`\n    ${name}:\n`);
  if (at < 0) throw new Error(`${name} not found in the contract`);
  const rest = yaml.slice(at + 1);
  const end = rest.slice(1).search(/\n    [A-Za-z]/);
  const block = end < 0 ? rest : rest.slice(0, end + 1);
  const props = block.indexOf("\n      properties:\n");
  if (props < 0) throw new Error(`${name} has no properties`);
  return new Set([...block.slice(props).matchAll(/\n        ([a-z_0-9]+):/g)].map((m) => m[1]));
}

test("stops pull: every key of the stub's stop row, and of each line, is declared by the contract", async () => {
  const yaml = readFileSync(new URL("../../../../core/api/openapi.yaml", import.meta.url), "utf8");
  const stopKeys = schemaProperties(yaml, "DispatchStop");
  const lineKeys = schemaProperties(yaml, "DispatchDriverLine");
  const res = await fetch(`${base}/api/v1/sync/stops?since=0&limit=200`, { headers });
  const items = (await res.json()).items ?? [];
  assert.ok(items.length > 0, "the stub pulled no stop");
  for (const item of items) {
    for (const key of Object.keys(item.data)) {
      assert.ok(stopKeys.has(key), `the stub's stop row carries ${key}, which the contract's DispatchStop does not declare`);
    }
    assert.ok(Array.isArray(item.data.lines) && item.data.lines.length > 0, "the stub's driver stop carries its load list");
    for (const line of item.data.lines) {
      for (const key of Object.keys(line)) {
        assert.ok(lineKeys.has(key), `a stub line carries ${key}, which DispatchDriverLine does not declare`);
      }
    }
  }
});
