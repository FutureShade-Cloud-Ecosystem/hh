// The no signal guard: every money moving action resolves online with an
// honest "no signal" state and is never queued. The guard is a fetch wrapper,
// so one rule covers every write an app makes through its client: a write
// offline is refused before anything dials, a read is not touched, and the
// refusal reads the same wherever it surfaces.
import { test } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "@hardscapehouse/api-client";
import { NO_SIGNAL_ERROR, fixedSignal, isNoSignal, noSignal, noSignalFetch, noSignalWebFetch, onlineOnly } from "../src/signal.ts";

function spyFetch(answer = { status: 200, ok: true, body: "{}" }) {
  const calls = [];
  const fetchLike = async (url, init) => {
    calls.push({ url, method: init.method });
    return { status: answer.status, ok: answer.ok, text: async () => answer.body, arrayBuffer: async () => new ArrayBuffer(0) };
  };
  return { fetchLike, calls };
}

test("the words are plain and say nothing waits", () => {
  assert.match(NO_SIGNAL_ERROR, /^No signal\./);
  assert.match(NO_SIGNAL_ERROR, /not wait|does not queue|never queue/i);
});

test("offline, every write verb is refused before anything dials", async () => {
  const signal = fixedSignal(false);
  const spy = spyFetch();
  const guarded = noSignalFetch(spy.fetchLike, signal);
  for (const method of ["POST", "PUT", "PATCH", "DELETE", "post", "Delete"]) {
    const res = await guarded("/api/v1/pos/ring", { method, headers: {}, redirect: "error" });
    assert.equal(res.ok, false, method);
    assert.match(await res.text(), /No signal/);
  }
  assert.equal(spy.calls.length, 0, "nothing reached the network");
});

test("offline, a read still dials, and online a write goes through", async () => {
  const signal = fixedSignal(false);
  const spy = spyFetch();
  const guarded = noSignalFetch(spy.fetchLike, signal);
  const read = await guarded("/api/v1/quotes", { method: "GET", headers: {}, redirect: "error" });
  assert.equal(read.ok, true);
  signal.set(true);
  const write = await guarded("/api/v1/quotes", { method: "POST", headers: {}, redirect: "error" });
  assert.equal(write.ok, true);
  assert.deepEqual(spy.calls.map((c) => c.method), ["GET", "POST"]);
});

test("through the api client the refusal is a Result every app already knows how to show", async () => {
  const signal = fixedSignal(false);
  const spy = spyFetch();
  const client = new ApiClient({ baseUrl: "", fetch: noSignalFetch(spy.fetchLike, signal) });
  const result = await client.request({ method: "POST", path: "/api/v1/pos/tender", body: { amount: 1 } });
  assert.equal(result.ok, false);
  assert.equal(result.error, NO_SIGNAL_ERROR);
  assert.equal(isNoSignal(result), true);
  assert.equal(spy.calls.length, 0);
});

test("the guard reads the signal at call time, not at build time", async () => {
  const signal = fixedSignal(true);
  const spy = spyFetch();
  const guarded = noSignalFetch(spy.fetchLike, signal);
  assert.equal((await guarded("/x", { method: "POST", headers: {}, redirect: "error" })).ok, true);
  signal.set(false);
  assert.equal((await guarded("/x", { method: "POST", headers: {}, redirect: "error" })).ok, false);
  signal.set(true);
  assert.equal((await guarded("/x", { method: "POST", headers: {}, redirect: "error" })).ok, true);
});

test("onlineOnly: offline it never runs the action and never stores anything", async () => {
  const signal = fixedSignal(false);
  let ran = 0;
  const result = await onlineOnly(signal, async () => {
    ran += 1;
    return { ok: true, value: 1 };
  });
  assert.equal(ran, 0);
  assert.deepEqual(result, noSignal());
  assert.equal(isNoSignal(result), true);
});

test("onlineOnly: online it runs the action once and hands the answer through, a refusal included", async () => {
  const signal = fixedSignal(true);
  let ran = 0;
  const refused = { ok: false, error: "balance changed", status: 409 };
  const result = await onlineOnly(signal, async () => {
    ran += 1;
    return refused;
  });
  assert.equal(ran, 1);
  assert.deepEqual(result, refused);
  assert.equal(isNoSignal(result), false, "a server refusal is not a lost signal");
});

test("a lost answer in flight is not dressed up as no signal", async () => {
  const signal = fixedSignal(true);
  const lost = { ok: false, error: "transport error: connection reset" };
  const result = await onlineOnly(signal, async () => lost);
  assert.deepEqual(result, lost, "the app's own words stand: nothing says the write did not land");
  assert.equal(isNoSignal(result), false);
});

test("the platform fetch form refuses a write offline with a real Response carrying the same words", async () => {
  const signal = fixedSignal(false);
  const calls = [];
  const web = noSignalWebFetch(async (input, init) => {
    calls.push(`${init?.method ?? "GET"} ${input}`);
    return new Response("{}", { status: 200 });
  }, signal);
  for (const method of ["POST", "PUT", "PATCH", "DELETE"]) {
    const res = await web("/api/v1/pos/sales", { method, body: "{}" });
    assert.equal(res.ok, false, method);
    assert.equal(res.status, 503, "a Response cannot carry status 0; 503 is the transient one");
    assert.deepEqual(await res.json(), { error: NO_SIGNAL_ERROR });
  }
  assert.deepEqual(calls, [], "nothing dialed");
  const read = await web("/api/v1/pos/parked");
  assert.equal(read.ok, true);
  assert.deepEqual(calls, ["GET /api/v1/pos/parked"]);
  signal.set(true);
  assert.equal((await web("/api/v1/pos/sales", { method: "POST", body: "{}" })).ok, true);
});
