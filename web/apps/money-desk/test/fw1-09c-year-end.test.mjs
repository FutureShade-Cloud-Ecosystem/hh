// FW1-09c: the year end close is the Financial Controller's, the way the early open
// of the next fiscal year is (614's rule, read and matched). The desk holds to the
// core's rule in the two places it can break: the stub refuses a caller who is not
// the Financial Controller with the core's own sentence, and the desk's plain words
// name the same role. The control itself is drawn exactly where the open year
// control is drawn, for the same role.
//
// Red first: the stub's close-year route took any admin (the General Manager among
// them), so the desk's browser legs would have shown a year end close the core
// refuses.
//
// Run it with the app's own loader:
//   node --import ../../packages/api-client/src/_ts-js-loader.mjs --test test/fw1-09c-year-end.test.mjs
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { readdirSync, readFileSync } from "node:fs";
import { createServer } from "node:net";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { SCHEMA_DESCRIPTORS } from "../../../packages/api-client/src/testing.ts";
import { validateValue } from "../../../packages/api-client/test/_validate.mjs";
import * as books from "../src/gl/gl-books.ts";
import * as logic from "../src/gl/gl-logic.ts";
import { IDS } from "./stub/gl-world.mjs";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const coreGl = join(appDir, "..", "..", "..", "core", "internal", "gl");

// The core's own source, so a refusal the stub invented fails here.
const coreSource = readdirSync(coreGl)
  .filter((f) => f.endsWith(".go") && !f.endsWith("_test.go"))
  .map((f) => readFileSync(join(coreGl, f), "utf8"))
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

function holds(schemaName, value) {
  const descriptor = SCHEMA_DESCRIPTORS[schemaName];
  assert.ok(descriptor !== undefined, `no descriptor named ${schemaName}`);
  assert.deepEqual(validateValue(value, descriptor), [], `${schemaName} does not fit the contract`);
}

/** The refusal: the status, the contract's error body, and the core's own sentence. */
function refused(res, status, fragment) {
  assert.equal(res.status, status);
  holds("Error", res.json);
  assert.ok(res.json.error.includes(fragment), `expected "${fragment}" in "${res.json.error}"`);
  assert.ok(coreSource.includes(fragment), `the core's source does not contain the sentence "${fragment}": the stub invented it`);
}

test("the year end close is refused to anyone but the Financial Controller, in the core's own sentence", async () => {
  await reset();
  // The General Manager holds the admin level the route asks for, so the gate lets the call
  // through; the core's rule is the role rule, and the stub must say the same thing.
  refused(
    await call(IDS.generalManager, "POST", "/api/v1/gl/periods/close-year", { year: 2026, reason: "x" }),
    403,
    "gl: only the Financial Controller may close the fiscal year",
  );
});

test("the Controller's year end close still answers the contract's shape", async () => {
  await reset();
  const done = await call(IDS.controller, "POST", "/api/v1/gl/periods/close-year", { year: 2026, reason: "year-end close FY2026" });
  assert.equal(done.status, 200);
  holds("GlYearCloseResult", done.json);
  assert.equal(done.json.fiscal_year, 2026);
  assert.equal(done.json.periods_closed, true);
  assert.equal(done.json.already_closed, false, "a first press closes now");
});

test("a repeat year end press answers already closed and writes nothing (FW1-09g)", async () => {
  await reset();
  const first = await call(IDS.controller, "POST", "/api/v1/gl/periods/close-year", { year: 2026, reason: "year-end close FY2026" });
  assert.equal(first.status, 200);
  assert.equal(first.json.already_closed, false, "a first press closes now");
  const again = await call(IDS.controller, "POST", "/api/v1/gl/periods/close-year", { year: 2026, reason: "again" });
  assert.equal(again.status, 200);
  holds("GlYearCloseResult", again.json);
  assert.equal(again.json.already_closed, true, "a repeat press says the year is already closed");
  assert.equal(again.json.closing_journal_id, first.json.closing_journal_id, "a repeat press answers the first close's result");
});

test("the desk's own words name the Financial Controller, in dealer words", () => {
  const words = logic.refusalWords("close-year", "gl: only the Financial Controller may close the fiscal year", 403);
  assert.ok(words.includes("Financial Controller"), words);
  assert.ok(!words.includes("General Manager"), words);
  assert.ok(!words.includes("Forbidden"), words);
  assert.ok(!words.startsWith("gl:"), words);
});

test("the year end control is drawn for the Controller alone, like the open year control", () => {
  const controller = markup(books.periodsView(frame({ books: model({ mode: "controller" }) })));
  assert.ok(controller.includes('data-action="gl-close-year"'), "the Controller's Periods tab carries no year end control");
  const reader = markup(books.periodsView(frame({ books: model({ mode: "reader" }) })));
  assert.ok(!reader.includes('data-action="gl-close-year"'), "a reader sees the year end control");
  assert.ok(!reader.includes('data-action="gl-open-year"'), "a reader sees the open year control");
});

// ---- the Periods tab's markup, read as text -------------------------------------------------------

const PERIODS = [
  { id: "9e000000-0000-4000-9000-000000000008", year: 2026, month: 8, status: "OPEN" },
  { id: "9e000000-0000-4000-9000-000000000009", year: 2026, month: 9, status: "OPEN" },
];

function model(over = {}) {
  return {
    mode: "controller",
    chart: { phase: "ready", value: [], error: "" },
    recurring: { phase: "ready", value: [], error: "" },
    periods: { phase: "ready", value: PERIODS, error: "" },
    nextOpenableYear: 2028,
    latePosted: { phase: "ready", value: [], error: "" },
    policy: { phase: "idle", value: null, error: "" },
    tie: { phase: "idle", value: [], error: "" },
    bis: { phase: "idle", value: null, error: "" },
    legacyImport: "",
    confirm: null,
    composing: false,
    lineCount: 2,
    review: null,
    composeError: "",
    trialMonth: 0,
    exporting: false,
    ...over,
  };
}

function frame(over = {}) {
  return { error: "", branches: [], journals: [], busy: false, books: model(), ...over };
}

/** The markup of a Lit template, as text: the static chunks with the nested templates folded in. */
function markup(node) {
  if (node === null || node === undefined || node === false || node === true) return "";
  if (typeof node === "symbol") return "";
  if (Array.isArray(node)) return node.map(markup).join("");
  if (typeof node === "object" && Array.isArray(node.strings)) {
    let out = "";
    node.strings.forEach((chunk, i) => {
      out += chunk;
      if (i < node.values.length) out += markup(node.values[i]);
    });
    return out;
  }
  return String(node);
}
