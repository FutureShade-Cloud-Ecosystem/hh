// The ledger slice's stub against the contract and against the core, field by
// field. A stub that drifts from the server makes every browser leg a
// flattering lie, so this test starts the stub, makes every call the slice
// makes, and holds each answer to the generated wire descriptors: required
// fields present, types and vocabularies right, and no field the contract
// does not declare. Then each refusal is held to the core's words: the status,
// the envelope, and the sentence itself, which must appear in the core's
// source. A refusal the stub invented fails here.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { readdirSync, readFileSync } from "node:fs";
import { createServer } from "node:net";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { SCHEMA_DESCRIPTORS } from "../../../packages/api-client/src/testing.ts";
import { validateValue } from "../../../packages/api-client/test/_validate.mjs";
import { IDS } from "./stub/gl-world.mjs";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const coreInternal = join(appDir, "..", "..", "..", "core", "internal");

const coreSource = [join("identity", "staffcontext"), "gl", join("platform", "authz"), join("platform", "httpx")]
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
  child = spawn(process.execPath, [join(appDir, "test", "stub", "money-desk-stub.mjs")], {
    env: { ROLE_STUB_PORT: String(port) },
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

// A stub token for one account: the shape the stub reads a subject from.
function tokenFor(sub) {
  const part = (v) => Buffer.from(JSON.stringify(v), "utf8").toString("base64url");
  return `${part({ alg: "ES256" })}.${part({ sub, exp: 4102444800 })}.c3R1Yg`;
}

async function call(as, method, path, body) {
  const headers = { Authorization: `Bearer ${tokenFor(as)}`, "X-Org": "hh-demo" };
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

const decide = (as, id, approve) => call(as, "POST", `/api/v1/staff/approvals/${id}/decide`, { approve, reason: "stub" });

test("the approvals queue answers the contract's page, every branch to the controller and Trenton alone to the branch manager", async () => {
  await reset();
  const mine = await call(IDS.controller, "GET", "/api/v1/staff/approvals?status=PENDING");
  assert.equal(mine.status, 200);
  holds("IdentityApprovalPage", mine.json);
  assert.equal(mine.json.items.length, 5);
  assert.equal(mine.json.next_cursor, null);
  assert.deepEqual([...new Set(mine.json.items.map((r) => r.branch_id))].sort(), [IDS.kingston, IDS.trenton].sort());
  const theirs = await call(IDS.manager, "GET", "/api/v1/staff/approvals?status=PENDING");
  assert.ok(theirs.json.items.every((r) => r.branch_id === IDS.trenton));
  // The cursor walks every row once.
  const first = await call(IDS.controller, "GET", "/api/v1/staff/approvals?status=PENDING&limit=2");
  assert.equal(first.json.items.length, 2);
  assert.ok(typeof first.json.next_cursor === "string");
  const second = await call(IDS.controller, "GET", `/api/v1/staff/approvals?status=PENDING&limit=2&cursor=${first.json.next_cursor}`);
  assert.equal(second.json.items.length, 2);
  assert.ok(second.json.items.every((r) => !first.json.items.some((f) => f.id === r.id)));
});

test("the journals, a journal, the trial balance and the branches answer the contract's shapes", async () => {
  await reset();
  const list = await call(IDS.controller, "GET", "/api/v1/gl/journals");
  assert.equal(list.status, 200);
  holds("GlJournalPage", list.json);
  assert.ok(list.json.items.length >= 6);
  const one = await call(IDS.controller, "GET", `/api/v1/gl/journals/${IDS.j1}`);
  assert.equal(one.status, 200);
  holds("GlJournal", one.json);
  assert.equal(one.json.derived_status, "PENDING_APPROVAL");
  const tb = await call(IDS.controller, "GET", "/api/v1/gl/trial-balance");
  assert.equal(tb.status, 200);
  holds("GlTrialBalanceResult", tb.json);
  assert.equal(tb.json.balance_cents, 0);
  const branches = await call(IDS.controller, "GET", "/api/v1/branches");
  assert.equal(branches.status, 200);
  branches.json.forEach((b) => holds("Branch", b));
});

test("the controller decides the floor request and the parked journal, and the trial balance carries the journal", async () => {
  await reset();
  const before = (await call(IDS.controller, "GET", "/api/v1/gl/trial-balance")).json;
  const floor = await decide(IDS.controller, IDS.fr1, true);
  assert.equal(floor.status, 200);
  holds("IdentityStatusResponse", floor.json);
  const posted = await decide(IDS.controller, IDS.j1Request, true);
  assert.equal(posted.status, 200);
  const journal = (await call(IDS.controller, "GET", `/api/v1/gl/journals/${IDS.j1}`)).json;
  assert.equal(journal.status, "POSTED");
  assert.equal(journal.approved_by, IDS.controller);
  holds("GlJournal", journal);
  const after = (await call(IDS.controller, "GET", "/api/v1/gl/trial-balance")).json;
  assert.equal(after.total_debit_cents - before.total_debit_cents, 750000);
  assert.equal(after.total_credit_cents - before.total_credit_cents, 750000);
  assert.equal(after.balance_cents, 0);
  refused(await decide(IDS.controller, IDS.j1Request, true), 409, "request already");
});

test("the refusals are the core's sentences, in the core's order", async () => {
  await reset();
  refused(await decide(IDS.controller, IDS.j2Request, true), 403, "separation of duties: you cannot approve your own request");
  refused(await decide(IDS.controller, IDS.j2Request, false), 403, "separation of duties: you cannot approve your own request");
  // The branch Sales manager filed J1 itself, so its own is refused by the four eyes rule first; a freight
  // raise it did not file is refused by the policy's approver set.
  refused(await decide(IDS.manager, IDS.j1Request, true), 403, "separation of duties: you cannot approve your own request");
  refused(await decide(IDS.manager, IDS.freight2, true), 403, "not authorized for");
  refused(await decide(IDS.controller, "not-a-uuid", true), 400, "invalid approval id");
  const own = (await call(IDS.controller, "GET", `/api/v1/gl/journals/${IDS.j2}`)).json;
  assert.equal(own.status, "DRAFT");
  assert.equal(own.derived_status, "PENDING_APPROVAL");
});
