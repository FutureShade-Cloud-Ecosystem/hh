// The box's web runtime config, written at boot from named public variables
// (deploy/demo/write-web-config.sh, sourced by the demo entrypoint). The
// output is held to the REAL browser parser: whatever the writer emits for a
// console copy must parse as console, and each refusal names its variable and
// never repeats the value. The writer is run with a clean environment (PATH
// only), so nothing of the runner's own leaks in.
import { test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, writeFileSync, existsSync, readdirSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { parseRuntimeConfig } from "../src/runtime-config.ts";

const ROOT = resolve(import.meta.dirname, "..", "..", "..", "..");
const WRITER = join(ROOT, "deploy", "demo", "write-web-config.sh");
const ENTRYPOINT = join(ROOT, "deploy", "demo", "entrypoint.sh");

const CONSOLE_ENV = {
  HH_WEB_MODE: "console",
  HH_WEB_ORG: "hh-demo",
  HH_WEB_CONSOLE_ENDPOINT: "https://console.example.test/v1",
  HH_WEB_CONSOLE_PROJECT_ID: "fake-project",
  HH_WEB_DEALER_NAME: "Gable Landscape Supply",
  HH_WEB_DEALER_MARK: "gable",
};

const BAKED = '{"mode":"demo"}';

function write(env, { prior = BAKED } = {}) {
  const dir = mkdtempSync(join(tmpdir(), "webcfg-"));
  const out = join(dir, "config.json");
  if (prior !== null) writeFileSync(out, prior);
  const run = spawnSync("sh", ["-c", '. "$1"; write_web_config "$2"', "sh", WRITER, out], {
    env: { PATH: process.env.PATH, ...env },
    encoding: "utf8",
  });
  const body = existsSync(out) ? readFileSync(out, "utf8") : null;
  const leftovers = readdirSync(dir).filter((n) => n !== "config.json");
  rmSync(dir, { recursive: true, force: true });
  return { status: run.status, stderr: run.stderr, stdout: run.stdout, body, leftovers };
}

function refused(env, variable, secretValue) {
  const r = write(env);
  assert.notEqual(r.status, 0, `${variable}: the writer accepted it`);
  assert.ok(r.stderr.includes(variable), `${variable}: the refusal does not name it: ${r.stderr}`);
  if (secretValue) {
    assert.ok(!r.stderr.includes(secretValue) && !r.stdout.includes(secretValue), `${variable}: the refusal repeats the value`);
  }
  assert.equal(r.body, BAKED, `${variable}: a refused boot changed the file`);
  assert.deepEqual(r.leftovers, [], `${variable}: a temp file was left behind`);
}

test("a console boot's config.json parses with the real parser", () => {
  const r = write({ ...CONSOLE_ENV, HH_WEB_PERSONAS: "true" });
  assert.equal(r.status, 0, r.stderr);
  const parsed = parseRuntimeConfig(JSON.parse(r.body));
  assert.equal(parsed.ok, true);
  assert.deepEqual(parsed.config, {
    mode: "console",
    org: "hh-demo",
    consoleEndpoint: "https://console.example.test/v1",
    consoleProjectId: "fake-project",
    coreBaseUrl: "",
    personas: true,
    personasOpen: false,
    personaCaller: "partner",
    dealer: { name: "Gable Landscape Supply", mark: "gable" },
  });
  assert.deepEqual(r.leftovers, []);
});

test("the public copy: personas open, caller public, a core base url, no mark", () => {
  const { HH_WEB_DEALER_MARK: _mark, ...rest } = CONSOLE_ENV;
  const r = write({
    ...rest,
    HH_WEB_PERSONAS: "true",
    HH_WEB_PERSONAS_OPEN: "true",
    HH_WEB_PERSONA_CALLER: "public",
    HH_WEB_CORE_BASE_URL: "https://core.example.test",
  });
  assert.equal(r.status, 0, r.stderr);
  const parsed = parseRuntimeConfig(JSON.parse(r.body));
  assert.equal(parsed.ok, true);
  assert.equal(parsed.config.personasOpen, true);
  assert.equal(parsed.config.personaCaller, "public");
  assert.equal(parsed.config.coreBaseUrl, "https://core.example.test");
  assert.deepEqual(parsed.config.dealer, { name: "Gable Landscape Supply" });
});

test("a local http endpoint is accepted", () => {
  const r = write({ ...CONSOLE_ENV, HH_WEB_CONSOLE_ENDPOINT: "http://localhost:8080/v1" });
  assert.equal(r.status, 0, r.stderr);
  assert.equal(parseRuntimeConfig(JSON.parse(r.body)).ok, true);
});

test("quotes, backslashes and non ASCII in the dealer name survive the encoder", () => {
  const name = 'O\'Neil "Stone" \\ Supply é';
  const r = write({ ...CONSOLE_ENV, HH_WEB_DEALER_NAME: name });
  assert.equal(r.status, 0, r.stderr);
  assert.equal(JSON.parse(r.body).dealer.name, name);
});

test("demo mode, set or unset, leaves the baked file byte for byte", () => {
  for (const env of [{}, { HH_WEB_MODE: "demo" }]) {
    const r = write(env, { prior: BAKED });
    assert.equal(r.status, 0, r.stderr);
    assert.equal(r.body, BAKED);
    assert.deepEqual(r.leftovers, []);
  }
});

test("refusals name the variable and never repeat its value", () => {
  const phrase = "correct horse battery staple";
  refused({ ...CONSOLE_ENV, HH_WEB_CONSOLE_ENDPOINT: phrase }, "HH_WEB_CONSOLE_ENDPOINT", phrase);
  refused({ ...CONSOLE_ENV, HH_WEB_CONSOLE_ENDPOINT: "http://console.example.test/v1" }, "HH_WEB_CONSOLE_ENDPOINT", "console.example.test");
  refused({ ...CONSOLE_ENV, HH_WEB_ORG: "Bad Org!" }, "HH_WEB_ORG", "Bad Org!");
  refused({ ...CONSOLE_ENV, HH_WEB_ORG: "hh-demo\nBAD ORG" }, "HH_WEB_ORG", "BAD ORG");
  refused({ ...CONSOLE_ENV, HH_WEB_ORG: "Bad!\nhh-demo" }, "HH_WEB_ORG", "Bad!");
  refused({ ...CONSOLE_ENV, HH_WEB_CORE_BASE_URL: "ftp://core.example.test" }, "HH_WEB_CORE_BASE_URL", "ftp://core.example.test");
  refused({ ...CONSOLE_ENV, HH_WEB_PERSONAS: "yes" }, "HH_WEB_PERSONAS", "yes");
  refused({ ...CONSOLE_ENV, HH_WEB_PERSONAS_OPEN: "1" }, "HH_WEB_PERSONAS_OPEN");
  refused({ ...CONSOLE_ENV, HH_WEB_PERSONAS_OPEN: "true", HH_WEB_PERSONA_CALLER: "partner" }, "HH_WEB_PERSONA_CALLER");
  refused({ ...CONSOLE_ENV, HH_WEB_PERSONA_CALLER: "public" }, "HH_WEB_PERSONA_CALLER");
  refused({ ...CONSOLE_ENV, HH_WEB_PERSONA_CALLER: "admin" }, "HH_WEB_PERSONA_CALLER", "admin");
  refused({ ...CONSOLE_ENV, HH_WEB_DEALER_NAME: "Line\nBreak" }, "HH_WEB_DEALER_NAME", "Break");
  refused({ ...CONSOLE_ENV, HH_WEB_MODE: "staging" }, "HH_WEB_MODE", "staging");
});

test("a half set console mode names each missing variable", () => {
  for (const missing of ["HH_WEB_ORG", "HH_WEB_CONSOLE_ENDPOINT", "HH_WEB_CONSOLE_PROJECT_ID", "HH_WEB_DEALER_NAME"]) {
    const env = { ...CONSOLE_ENV };
    delete env[missing];
    refused(env, missing);
  }
});

test("a console variable without console mode is a refusal, never a quiet demo", () => {
  refused({ HH_WEB_ORG: "hh-demo" }, "HH_WEB_ORG");
  refused({ HH_WEB_MODE: "demo", HH_WEB_CONSOLE_PROJECT_ID: "p" }, "HH_WEB_CONSOLE_PROJECT_ID");
});

test("the entrypoint refuses a bad web config before the database wait and names the variable", () => {
  const run = spawnSync("sh", [ENTRYPOINT], {
    env: {
      PATH: process.env.PATH,
      DEMO_GATE_BCRYPT_HASH: "$2a$10$abcdefghijklmnopqrstuuabcdefghijklmnopqrstuvwxyz01234",
      DATABASE_URL: "postgres://unused:unused@127.0.0.1:1/unused",
      PAYMENT_VAULT_KEY: "0".repeat(64),
      HH_ACCEPTANCE_SIGN_KEY_ID: "webcfg-test",
      HH_ACCEPTANCE_SIGN_KEY: "0".repeat(64),
      CORE_SIGNIN_MODE: "console",
      ...CONSOLE_ENV,
      HH_WEB_CONSOLE_ENDPOINT: "not a url at all",
    },
    encoding: "utf8",
    timeout: 20000,
  });
  assert.notEqual(run.status, 0);
  assert.ok(run.stderr.includes("HH_WEB_CONSOLE_ENDPOINT"), run.stderr);
  assert.ok(!run.stderr.includes("not a url at all"), "the refusal repeats the value");
});
