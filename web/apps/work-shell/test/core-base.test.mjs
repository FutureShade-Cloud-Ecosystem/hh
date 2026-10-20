// Where the Work shell's server side calls go (PRR-531): in a native Tauri
// profile the webview's own origin is the asset server, so a relative
// /api/v1/auth/sign-out posts to a static file. The core's base URL comes
// from the served runtime config (the same /config.json the front door
// reads), or from the page global a native shell can set before the bundle
// runs. These tests drive their own fetch and window; no network is touched.
import { test } from "node:test";
import assert from "node:assert/strict";
import { resolveCoreBaseUrl } from "../src/core-base.js";

function win(overrides = {}) {
  return { location: { origin: "http://localhost:5173" }, ...overrides };
}

/** A complete console body, the parser refuses anything less. */
function consoleBody(coreBaseUrl) {
  return {
    mode: "console",
    org: "hh-demo",
    consoleEndpoint: "https://console.futurebuild.ai",
    consoleProjectId: "hh",
    coreBaseUrl,
    personas: false,
    personasOpen: false,
    dealer: { name: "Gable Landscape Supply" },
  };
}

function configFetch(body, calls = []) {
  return async (url, init) => {
    calls.push({ url: String(url), init });
    return { ok: true, status: 200, json: async () => body };
  };
}

test("the page global's coreBaseUrl wins and loses its trailing slash", async () => {
  const calls = [];
  const base = await resolveCoreBaseUrl({
    win: win({ __HH_WEB_CONFIG__: { coreBaseUrl: "https://core.futurebuild.ai/" } }),
    fetch: configFetch({ mode: "demo" }, calls),
  });
  assert.equal(base, "https://core.futurebuild.ai");
  assert.equal(calls.length, 0, "an explicit global short circuits the config read");
});

test("a console config's coreBaseUrl is used, and an empty one means the page's own origin", async () => {
  const remote = await resolveCoreBaseUrl({
    win: win(),
    fetch: configFetch(consoleBody("https://core.futurebuild.ai")),
  });
  assert.equal(remote, "https://core.futurebuild.ai");
  const sameOrigin = await resolveCoreBaseUrl({
    win: win(),
    fetch: configFetch(consoleBody("")),
  });
  assert.equal(sameOrigin, "http://localhost:5173");
});

test("a demo copy, or a config that cannot be read, resolves no core base: calls stay relative", async () => {
  for (const body of [{ mode: "demo" }, {}]) {
    const base = await resolveCoreBaseUrl({ win: win(), fetch: configFetch(body) });
    assert.equal(base, undefined);
  }
  const refused = async () => ({ ok: false, status: 503, json: async () => ({}) });
  assert.equal(await resolveCoreBaseUrl({ win: win(), fetch: refused }), undefined);
});
