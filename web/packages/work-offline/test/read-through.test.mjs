// The read through fetch: a GET that has a cached answer is served from the
// device when the network does not answer, in the shape the app already
// parses, and says it did; every other request goes where it was going.
import { test } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "@hardscapehouse/api-client";
import { fixedSignal, noSignalFetch } from "../src/signal.ts";
import { readThroughFetch, readThroughWebFetch } from "../src/read-through.ts";

function rig({ fail = false } = {}) {
  const calls = [];
  const state = { fail };
  const base = async (url, init) => {
    calls.push(`${init.method} ${url}`);
    if (state.fail) throw new TypeError("Failed to fetch");
    return { status: 200, ok: true, text: async () => JSON.stringify({ live: true }), arrayBuffer: async () => new ArrayBuffer(0) };
  };
  const events = [];
  const hooks = { onCached: (words) => events.push(`cached:${words}`), onLive: () => events.push("live") };
  const routes = [
    {
      match: (url) => url.pathname === "/api/v1/products/search",
      answer: async (url) => ({ body: { items: [{ q: url.searchParams.get("q") }] }, words: "Pulled 3 min ago" }),
    },
    { match: (url) => url.pathname === "/api/v1/empty", answer: async () => null },
  ];
  return { calls, state, base, events, hooks, routes };
}

const get = { method: "GET", headers: {}, redirect: "error" };

test("online, a read goes to the server and is told to the hook as live", async () => {
  const r = rig();
  const f = readThroughFetch(r.base, fixedSignal(true), r.routes, r.hooks);
  const res = await f("/api/v1/products/search?q=paver", get);
  assert.deepEqual(JSON.parse(await res.text()), { live: true });
  assert.deepEqual(r.events, ["live"]);
});

test("with no signal a cached read never dials and is answered from the device, with its words", async () => {
  const r = rig();
  const f = readThroughFetch(r.base, fixedSignal(false), r.routes, r.hooks);
  const res = await f("/api/v1/products/search?q=paver", get);
  assert.equal(res.ok, true);
  assert.deepEqual(JSON.parse(await res.text()), { items: [{ q: "paver" }] });
  assert.deepEqual(r.calls, []);
  assert.deepEqual(r.events, ["cached:Pulled 3 min ago"]);
});

test("online but the network fails, the read is answered from the device", async () => {
  const r = rig({ fail: true });
  const f = readThroughFetch(r.base, fixedSignal(true), r.routes, r.hooks);
  const res = await f("/api/v1/products/search?q=sand", get);
  assert.deepEqual(JSON.parse(await res.text()), { items: [{ q: "sand" }] });
  assert.deepEqual(r.events, ["cached:Pulled 3 min ago"]);
});

test("a route with no answer lets the failure through as it was", async () => {
  const r = rig({ fail: true });
  const f = readThroughFetch(r.base, fixedSignal(true), r.routes, r.hooks);
  await assert.rejects(() => f("/api/v1/empty", get), /Failed to fetch/);
  await assert.rejects(() => f("/api/v1/unrouted", get), /Failed to fetch/);
  assert.deepEqual(r.events, []);
});

test("a server refusal is not a lost network: it is the server's word and stands", async () => {
  const calls = [];
  const base = async (url, init) => {
    calls.push(url);
    return { status: 403, ok: false, text: async () => JSON.stringify({ error: "not yours" }), arrayBuffer: async () => new ArrayBuffer(0) };
  };
  const r = rig();
  const f = readThroughFetch(base, fixedSignal(true), r.routes, r.hooks);
  const res = await f("/api/v1/products/search?q=x", get);
  assert.equal(res.status, 403);
  assert.deepEqual(r.events, []);
});

test("writes pass straight through: the read through never answers one", async () => {
  const r = rig();
  const f = readThroughFetch(r.base, fixedSignal(false), r.routes, r.hooks);
  await f("/api/v1/products/search", { method: "POST", headers: {}, redirect: "error" });
  assert.deepEqual(r.calls, ["POST /api/v1/products/search"]);
});

test("composed under the no signal guard: a read is served from the device, a write is refused, nothing is queued", async () => {
  const r = rig();
  const signal = fixedSignal(false);
  const client = new ApiClient({ baseUrl: "", fetch: noSignalFetch(readThroughFetch(r.base, signal, r.routes, r.hooks), signal) });
  const read = await client.request({ method: "GET", path: "/api/v1/products/search", query: { q: "paver" } });
  assert.equal(read.ok, true);
  assert.deepEqual(read.value, { items: [{ q: "paver" }] });
  const write = await client.request({ method: "POST", path: "/api/v1/pos/tender", body: {} });
  assert.equal(write.ok, false);
  assert.match(write.error, /^No signal\./);
  assert.deepEqual(r.calls, [], "neither request dialed");
});

test("the web fetch form answers a real Response, the same way", async () => {
  const r = rig({ fail: true });
  const web = readThroughWebFetch(async () => { throw new TypeError("Failed to fetch"); }, fixedSignal(true), r.routes, r.hooks);
  const res = await web("/api/v1/products/search?q=paver", { method: "GET" });
  assert.ok(res instanceof Response);
  assert.equal(res.status, 200);
  assert.deepEqual(await res.json(), { items: [{ q: "paver" }] });
  assert.deepEqual(r.events, ["cached:Pulled 3 min ago"]);
  const missing = await web("/api/v1/unrouted", {}).then(() => "answered", (e) => String(e));
  assert.match(missing, /Failed to fetch/);
});

test("the web fetch form defaults to GET when no method is given", async () => {
  const r = rig();
  const web = readThroughWebFetch(async () => { throw new TypeError("Failed to fetch"); }, fixedSignal(false), r.routes, r.hooks);
  const res = await web("/api/v1/products/search?q=a");
  assert.equal(res.status, 200);
});
