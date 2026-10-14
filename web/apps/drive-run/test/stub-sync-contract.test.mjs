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
import { GO_SETS, PROBE_EXTRAS } from "./_lib/gosets.mjs";

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

async function probe(scope, op, setName, id) {
  const set = GO_SETS[setName]();
  for (const field of UNIVERSE()) {
    const answer = await push(scope, op, { id, [field]: "x" });
    const refused = answer.status === "rejected" && NOT_WRITABLE.test(String(answer.error ?? ""));
    assert.equal(refused, !set.has(field), `${setName}: field ${field} should be ${set.has(field) ? "admitted" : "refused"} by the stub exactly as the core does (answer: ${JSON.stringify(answer)})`);
  }
}

test("stops update: the stub admits exactly the core's writable fields", async () => {
  await probe("stops", "update", "stops update", (await pullIds("stops"))[0]);
});

test("pod create: the stub admits exactly the core's writable fields", async () => {
  await probe("pod", "create", "pod create", "7b000000-0000-4000-9000-0000000fffff");
});

