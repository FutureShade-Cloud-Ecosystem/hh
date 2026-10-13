// The served runtime configuration (config.json): the sign in mode has no
// default, so a missing, unreadable or unknown configuration is a refusal the
// page words itself, never a quiet fall back to the demo cards.
import { test } from "node:test";
import assert from "node:assert/strict";
import { ADMIN_PERSONA_IDS, loadRuntimeConfig, parseRuntimeConfig, personaVisible } from "../src/runtime-config.ts";

const CONSOLE = {
  mode: "console",
  org: "gable",
  consoleEndpoint: "https://console.example.test/v1",
  consoleProjectId: "fake-project",
  dealer: { name: "Gable Landscape Supply", mark: "gable" },
};

test("demo mode needs nothing else", () => {
  const parsed = parseRuntimeConfig({ mode: "demo" });
  assert.deepEqual(parsed, { ok: true, config: { mode: "demo" } });
});

test("console mode carries the org, the console, the dealer and the persona flags", () => {
  const parsed = parseRuntimeConfig({ ...CONSOLE, personas: true, personasOpen: true, coreBaseUrl: "https://core.example.test" });
  assert.equal(parsed.ok, true);
  assert.deepEqual(parsed.config, {
    mode: "console",
    org: "gable",
    consoleEndpoint: "https://console.example.test/v1",
    consoleProjectId: "fake-project",
    coreBaseUrl: "https://core.example.test",
    personas: true,
    personasOpen: true,
    personaCaller: "public",
    dealer: { name: "Gable Landscape Supply", mark: "gable" },
  });
});

test("the persona caller names who calls the persona route and mirrors the core's setting; it defaults from personasOpen", () => {
  assert.equal(parseRuntimeConfig({ ...CONSOLE, personas: true, personasOpen: true }).config.personaCaller, "public");
  assert.equal(parseRuntimeConfig({ ...CONSOLE, personas: true }).config.personaCaller, "partner");
  assert.equal(parseRuntimeConfig(CONSOLE).config.personaCaller, "partner");
  assert.equal(parseRuntimeConfig({ ...CONSOLE, personas: true, personasOpen: true, personaCaller: "public" }).config.personaCaller, "public");
  assert.equal(parseRuntimeConfig({ ...CONSOLE, personas: true, personaCaller: "partner" }).config.personaCaller, "partner");
  for (const patch of [{ personaCaller: "admin" }, { personaCaller: 1 }, { personaCaller: "partner", personasOpen: true }, { personaCaller: "public", personasOpen: false }]) {
    assert.deepEqual(parseRuntimeConfig({ ...CONSOLE, personas: true, ...patch }), { ok: false, reason: "incomplete" }, JSON.stringify(patch));
  }
});

test("the public copy hides the admin personas; the testers' copy shows every persona", () => {
  assert.deepEqual([...ADMIN_PERSONA_IDS].sort(), ["dealer-admin", "financial-controller", "general-manager", "hr"]);
  const publicCopy = parseRuntimeConfig({ ...CONSOLE, personas: true, personasOpen: true }).config;
  const testers = parseRuntimeConfig({ ...CONSOLE, personas: true }).config;
  for (const id of ADMIN_PERSONA_IDS) {
    assert.equal(personaVisible(publicCopy, id), false, id);
    assert.equal(personaVisible(testers, id), true, id);
  }
  for (const id of ["sales", "sales-manager", "yard-manager", "yard-team", "payables-receivables", "procurement-manager", "driver"]) {
    assert.equal(personaVisible(publicCopy, id), true, id);
  }
  // a config object built without the field is judged by personasOpen
  assert.equal(personaVisible({ personasOpen: true }, "financial-controller"), false);
  assert.equal(personaVisible({ personasOpen: false }, "financial-controller"), true);
});

test("the persona flags default to false and the core base url to the page's own origin (empty)", () => {
  const parsed = parseRuntimeConfig(CONSOLE);
  assert.equal(parsed.config.personas, false);
  assert.equal(parsed.config.personasOpen, false);
  assert.equal(parsed.config.coreBaseUrl, "");
});

test("no mode and an unknown mode are refused, with no default", () => {
  assert.deepEqual(parseRuntimeConfig({}), { ok: false, reason: "no_mode" });
  assert.deepEqual(parseRuntimeConfig({ mode: "staging" }), { ok: false, reason: "unknown_mode" });
  assert.deepEqual(parseRuntimeConfig({ mode: "" }), { ok: false, reason: "unknown_mode" });
  assert.deepEqual(parseRuntimeConfig({ mode: "Console" }), { ok: false, reason: "unknown_mode" });
  for (const bad of [null, undefined, [], "console", 7]) {
    assert.equal(parseRuntimeConfig(bad).ok, false);
  }
});

test("console mode refuses an incomplete or malformed configuration", () => {
  for (const patch of [
    { org: undefined },
    { org: "" },
    { org: "Has Space" },
    { consoleEndpoint: undefined },
    { consoleEndpoint: "http://console.example.test/v1" },
    { consoleEndpoint: "not a url" },
    { consoleProjectId: "" },
    { coreBaseUrl: "http://core.example.test" },
    { personas: "yes" },
    { personasOpen: 1 },
    { dealer: { name: "" } },
    { dealer: "Gable" },
  ]) {
    const parsed = parseRuntimeConfig({ ...CONSOLE, ...patch });
    assert.deepEqual(parsed, { ok: false, reason: "incomplete" }, JSON.stringify(patch));
  }
});

test("a local development console may be http on localhost", () => {
  const parsed = parseRuntimeConfig({ ...CONSOLE, consoleEndpoint: "http://127.0.0.1:4930/v1" });
  assert.equal(parsed.ok, true);
});

test("unknown fields are dropped, not carried", () => {
  const parsed = parseRuntimeConfig({ ...CONSOLE, apiKey: "should-not-travel" });
  assert.equal("apiKey" in parsed.config, false);
});

test("loading: a 200 with json parses; every other outcome has its own reason", async () => {
  const answer = (status, body) => async () => new Response(body, { status });
  assert.equal((await loadRuntimeConfig(answer(200, JSON.stringify({ mode: "demo" })))).ok, true);
  assert.deepEqual(await loadRuntimeConfig(answer(404, "")), { ok: false, reason: "missing" });
  assert.deepEqual(await loadRuntimeConfig(answer(200, "<html>")), { ok: false, reason: "unreadable" });
  assert.deepEqual(await loadRuntimeConfig(async () => { throw new TypeError("down"); }), { ok: false, reason: "unreachable" });
  assert.deepEqual(await loadRuntimeConfig(answer(200, JSON.stringify({ mode: "x" }))), { ok: false, reason: "unknown_mode" });
});

test("loading asks for the file same origin and without the cache", async () => {
  let seen;
  await loadRuntimeConfig(async (url, init) => {
    seen = { url: String(url), init };
    return new Response(JSON.stringify({ mode: "demo" }), { status: 200 });
  });
  assert.equal(seen.url, "/config.json");
  assert.equal(seen.init.cache, "no-store");
  assert.equal(seen.init.credentials, "same-origin", "a gated copy asks for the file with the browser's own authentication, never cross origin");
});
