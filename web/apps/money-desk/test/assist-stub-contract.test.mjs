// The TanyAI stub against the contract: the v1 answers of the four staff assist routes held to the generated
// wire descriptors, field by field, and the two fixed sentences held to the core's source. The scripted
// questions that emit the section 2 fields (citations, notice, scope, answer_id) are NOT held here: those fields
// are not in the contract until the core ships them (PANEL-API r1 section 2), and the stub says so.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { readFileSync } from "node:fs";
import { createServer } from "node:net";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { SCHEMA_DESCRIPTORS } from "../../../packages/api-client/src/testing.ts";
import { validateValue } from "../../../packages/api-client/test/_validate.mjs";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const coreService = readFileSync(join(appDir, "..", "..", "..", "core", "internal", "assist", "service.go"), "utf8");

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

const sentence = (go) => go.replace(/^"|"$/g, "");

test("me, the command list and a command answer fit the contract", async () => {
  const me = await call("GET", "/api/v1/assist/me");
  assert.equal(me.status, 200);
  holds("AssistMe", me.json);
  const list = await call("GET", "/api/v1/assist/commands");
  holds("AssistCommandList", list.json);
  const ran = await call("POST", "/api/v1/assist/command", { command: "ar_aging", args: {} });
  holds("AssistResult", ran.json);
});

for (const question of ["who owes us", "stock of blu", "stock dbs-tb-blu60", "nonsense", "something plain", "over budget"]) {
  test(`a v1 chat answer fits the contract: ${question}`, async () => {
    await reset();
    const a = await call("POST", "/api/v1/assist/chat", { question, history: [] });
    assert.equal(a.status, 200);
    holds("AssistAnswer", a.json);
  });
}

test("the two fixed sentences are the core's own, word for word", async () => {
  const world = readFileSync(join(appDir, "test", "stub", "assist-world.mjs"), "utf8");
  const limit = /export const LIMIT_REACHED = (".*");/.exec(world)[1];
  const off = /export const CHAT_DISABLED = (".*");/.exec(world)[1];
  const goLimit = coreService.match(/const LimitReachedMessage = ((?:"[^\n]*"\s*\+?\s*)+)/)[1].match(/"((?:[^"\\]|\\.)*)"/g).map((s) => JSON.parse(s)).join("");
  const goOff = coreService.match(/const ChatDisabledMessage = ((?:"[^\n]*"\s*\+?\s*)+)/)[1].match(/"((?:[^"\\]|\\.)*)"/g).map((s) => JSON.parse(s)).join("");
  assert.equal(JSON.parse(limit), goLimit);
  assert.equal(JSON.parse(off), goOff);
});
