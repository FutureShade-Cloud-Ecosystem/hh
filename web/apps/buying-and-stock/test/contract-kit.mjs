// The shared half of the new stub contract tests: start the stub on a free port, call it, and hold
// each answer to the generated wire descriptors (required fields, types, vocabularies, and no field
// the contract does not declare) and each refusal sentence to the core's source.
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

/** The non test Go source of the named core packages, joined. */
export function coreSourceOf(dirs) {
  return dirs
    .flatMap((dir) =>
      readdirSync(join(coreInternal, dir))
        .filter((f) => f.endsWith(".go") && !f.endsWith("_test.go"))
        .map((f) => readFileSync(join(coreInternal, dir, f), "utf8")),
    )
    .join("\n");
}

async function freePort() {
  return new Promise((resolve) => {
    const probe = createServer();
    probe.listen(0, "127.0.0.1", () => {
      const { port } = probe.address();
      probe.close(() => resolve(port));
    });
  });
}

/** Starts the stub; returns { call, post, stop } for a test file's before and after. */
export async function startStub() {
  const port = await freePort();
  const base = `http://127.0.0.1:${port}`;
  const child = spawn(process.execPath, [join(appDir, "test", "stub", "buying-stock-stub.mjs")], {
    env: { ROLE_STUB_PORT: String(port) },
    stdio: "ignore",
  });
  let up = false;
  for (let i = 0; i < 100 && !up; i += 1) {
    try {
      up = (await fetch(`${base}/healthz`)).ok;
    } catch {
      await new Promise((r) => setTimeout(r, 50));
    }
  }
  if (!up) throw new Error("the stub did not start");
  async function call(method, path, body, headers = {}) {
    const h = { Authorization: "Bearer stub-token", "X-Org": "hh-demo", ...headers };
    let payload;
    if (body !== undefined) {
      h["Content-Type"] = "application/json";
      payload = JSON.stringify(body);
    }
    const res = await fetch(`${base}${path}`, { method, headers: h, body: payload });
    const text = await res.text();
    let parsed = null;
    try {
      parsed = JSON.parse(text);
    } catch {
      /* not json */
    }
    return { status: res.status, json: parsed, text, headers: res.headers };
  }
  const post = (path, body) => fetch(`${base}${path}`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body ?? {}) });
  return { call, post, reset: () => post("/_stub/reset"), stop: () => child.kill() };
}

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

/** The value fits the named contract schema, and carries nothing the schema does not declare. */
export function holds(schemaName, value) {
  const descriptor = SCHEMA_DESCRIPTORS[schemaName];
  assert.ok(descriptor !== undefined, `no descriptor named ${schemaName}`);
  assert.deepEqual(validateValue(value, descriptor), [], `${schemaName} does not fit the contract`);
  assert.deepEqual(undeclared(value, descriptor), [], `${schemaName} carries fields the contract does not declare`);
}

/** The answer is a refusal of this status carrying this sentence, and the sentence is the core's. */
export function refusedBy(coreSource) {
  return (res, status, fragment, inSource = fragment) => {
    assert.equal(res.status, status);
    holds("Error", res.json);
    assert.ok(res.json.error.includes(fragment), `expected "${fragment}" in "${res.json.error}"`);
    assert.ok(coreSource.includes(inSource), `the core's source does not contain the sentence "${inSource}": the stub invented it`);
  };
}

export const BRANCH = "b0000000-0000-4000-8000-000000000001";
