// The native profile's core base resolution (PRR-531, PR #495 round 1 part
// C): the host passes the page window (so the page global a native shell sets
// is read), and when no global is set the native dist's own /config.json (the
// slim body assemble-dist.sh writes) answers. These tests drive their own
// fetch and window; no network is touched.
import { test } from "node:test";
import assert from "node:assert/strict";
import { resolveCoreBaseUrl } from "../src/core-base.js";

const notFound = async () => ({ ok: false, status: 404, json: async () => ({}) });

test("the host's own call shape: the page global wins over a missing config and the base is not undefined", async () => {
  // Exactly how the host calls it at boot: the page window, default fetch
  // (stubbed here). A native shell sets the global before the bundle runs;
  // the dist may serve no readable /config.json at all.
  const base = await resolveCoreBaseUrl({
    win: { __HH_WEB_CONFIG__: { coreBaseUrl: "https://core.example" }, location: { origin: "tauri://localhost" } },
    fetch: notFound,
  });
  assert.equal(base, "https://core.example");
  assert.notEqual(base, undefined);
});

test("without a global, the native dist's own config.json answers with its coreBaseUrl", async () => {
  const slim = async () => ({ ok: true, status: 200, json: async () => ({ coreBaseUrl: "https://core.example", consoleEndpoint: "https://api.futurebuild.ai/v1" }) });
  const base = await resolveCoreBaseUrl({ win: { location: { origin: "tauri://localhost" } }, fetch: slim });
  assert.equal(base, "https://core.example");
});

test("the native config's base is accepted only when it is a secure URL", async () => {
  for (const coreBaseUrl of ["", "http://core.example", "ftp://core.example", "javascript:alert(1)"]) {
    const slim = async () => ({ ok: true, status: 200, json: async () => ({ coreBaseUrl }) });
    const base = await resolveCoreBaseUrl({ win: {}, fetch: slim });
    assert.equal(base, undefined, `refused ${coreBaseUrl}`);
  }
  const loopback = async () => ({ ok: true, status: 200, json: async () => ({ coreBaseUrl: "http://127.0.0.1:8080/" }) });
  assert.equal(await resolveCoreBaseUrl({ win: {}, fetch: loopback }), "http://127.0.0.1:8080", "a dev build's loopback http is accepted");
});
