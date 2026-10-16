// The office slice's contract harness: the stub started on a free port, one call helper, and the three holds the
// contract tests make of every answer: it fits the generated wire descriptors, it carries no field the contract
// does not declare, and every refusal sentence is found word for word in the core's source. The receivables slice's
// own test (stub-contract.test.mjs) does the same for the first slice's routes; this file is shared by the office
// contract test files so none repeats the boilerplate.
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

export const coreSource = ["billing", "payments", "till", "settings", join("platform", "authz"), join("platform", "httpx")]
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

export async function startStub() {
  const port = await freePort();
  base = `http://127.0.0.1:${port}`;
  child = spawn(process.execPath, [join(appDir, "test", "stub", "money-desk-stub.mjs")], { env: { ROLE_STUB_PORT: String(port) }, stdio: "ignore" });
  for (let i = 0; i < 100; i += 1) {
    try {
      if ((await fetch(`${base}/healthz`)).ok) return;
    } catch {
      /* not up yet */
    }
    await new Promise((r) => setTimeout(r, 50));
  }
  throw new Error("the stub did not start");
}

export const baseUrl = () => base;

export function stopStub() {
  child?.kill();
}

/** A token whose sub is the controller's id (an administrator) or the Payables and Receivables id (a manager). */
export function tokenFor(who) {
  const part = (value) => Buffer.from(JSON.stringify(value), "utf8").toString("base64url");
  const sub = who === "controller" ? "de110000-0000-4000-9000-000000000008" : "de110000-0000-4000-9000-000000000007";
  return `${part({ alg: "ES256", typ: "JWT" })}.${part({ sub })}.c2ln`;
}

export async function call(method, path, body, who = "controller") {
  const headers = { Authorization: `Bearer ${tokenFor(who)}`, "X-Org": "hh-demo" };
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
  return { status: res.status, json, text };
}

export const reset = (scenario) =>
  scenario === undefined
    ? fetch(`${base}/_stub/reset`, { method: "POST" })
    : fetch(`${base}/_stub/scenario`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ name: scenario }) });

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

export function holds(schemaName, value) {
  const descriptor = SCHEMA_DESCRIPTORS[schemaName];
  assert.ok(descriptor !== undefined, `no descriptor named ${schemaName}`);
  assert.deepEqual(validateValue(value, descriptor), [], `${schemaName} does not fit the contract`);
  assert.deepEqual(undeclared(value, descriptor), [], `${schemaName} carries fields the contract does not declare`);
}

export function refused(res, status, fragment) {
  assert.equal(res.status, status, `${fragment}: ${JSON.stringify(res.json)}`);
  holds("Error", res.json);
  assert.ok(res.json.error.includes(fragment), `expected "${fragment}" in "${res.json.error}"`);
  assert.ok(coreSource.includes(fragment), `the core's source does not contain the sentence "${fragment}": the stub invented it`);
}

/** The gate's shared refusal for a role below the route's tier: status 403, the body the gate writes. */
export function forbidden(res) {
  assert.equal(res.status, 403);
  holds("Error", res.json);
  assert.equal(res.json.error, "Forbidden");
}

export const later = (days) => {
  const at = new Date();
  at.setUTCDate(at.getUTCDate() + days);
  return new Intl.DateTimeFormat("en-CA", { timeZone: "America/Toronto", year: "numeric", month: "2-digit", day: "2-digit" }).format(at);
};
