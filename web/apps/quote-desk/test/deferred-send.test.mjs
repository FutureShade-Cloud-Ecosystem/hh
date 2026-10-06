// The sender's two checks at the unit level: the session (the same client, still attached, the same signed in
// person) and the quote (the same id and epoch, not left). The screen level story is test/deferred-send.pw.ts.
import assert from "node:assert/strict";
import { test } from "node:test";
import { captureSend, deferredSend, wasUnsent } from "../src/deferred-send.js";
import { QuoteContext } from "../src/quote-context.js";

function fakeClient(subject) {
  const client = { who: subject, callerId: () => client.who };
  return client;
}

function here(client, quote, attached = true) {
  const now = { client, attached, quote };
  return { now, read: () => now };
}

const sendOk = (calls) => async () => {
  calls.push("sent");
  return { ok: true, value: 1 };
};

test("sender session: a write goes out when the same person is still on the page", async () => {
  const client = fakeClient("sales");
  const calls = [];
  const state = here(client, null);
  const capture = captureSend(state.read);
  const result = await deferredSend(capture, sendOk(calls));
  assert.equal(result.ok, true);
  assert.deepEqual(calls, ["sent"]);
});

test("sender session: a write is not sent after the signed in person changed", async () => {
  const client = fakeClient("sales");
  const calls = [];
  const state = here(client, null);
  const capture = captureSend(state.read);
  client.who = "manager";
  const result = await deferredSend(capture, sendOk(calls));
  assert.equal(wasUnsent(result), true);
  assert.equal(result.unsent, "session");
  assert.deepEqual(calls, []);
});

test("sender session: a write is not sent after the component left the page or the client was replaced", async () => {
  const client = fakeClient("sales");
  const calls = [];
  const state = here(client, null);
  const capture = captureSend(state.read);
  state.now.attached = false;
  assert.equal(wasUnsent(await deferredSend(capture, sendOk(calls))), true);
  state.now.attached = true;
  state.now.client = fakeClient("sales");
  assert.equal(wasUnsent(await deferredSend(capture, sendOk(calls))), true);
  assert.deepEqual(calls, []);
});

test("sender quote: a write is not sent after the quote context moved, and the person still there is told", async () => {
  const client = fakeClient("sales");
  const calls = [];
  const said = [];
  const asked = new QuoteContext("q-1", 4);
  const state = here(client, asked);
  const capture = captureSend(state.read, { quote: asked, label: "Q-1", say: (line) => said.push(line) });
  state.now.quote = new QuoteContext("q-2", 5);
  const result = await deferredSend(capture, sendOk(calls));
  assert.equal(result.unsent, "quote");
  assert.deepEqual(calls, []);
  assert.equal(said.length, 1);
  assert.match(said[0], /Q-1/);
});

test("sender quote: a write is not sent when the same quote was left and opened again (a new epoch) or its context was cancelled", async () => {
  const client = fakeClient("sales");
  const calls = [];
  const asked = new QuoteContext("q-1", 4);
  const state = here(client, asked);
  const capture = captureSend(state.read, { quote: asked });
  state.now.quote = new QuoteContext("q-1", 5);
  assert.equal((await deferredSend(capture, sendOk(calls))).unsent, "quote");
  state.now.quote = asked;
  asked.cancel();
  assert.equal((await deferredSend(capture, sendOk(calls))).unsent, "quote");
  assert.deepEqual(calls, []);
});

// The epoch alone is not the quote's identity: a work area replaced at sign out starts its scope over, so the quote
// another instance opens can carry the captured epoch. Only the id tells those quotes apart (the principal's look at
// round 9, P3-3: removing the id sub-check used to fail no behavioral test).
test("sender quote: a write under the same session is refused on another quote's id and sent on the same quote's id", async () => {
  const client = fakeClient("sales");
  const calls = [];
  const asked = new QuoteContext("q-1", 4);
  const state = here(client, asked);
  const capture = captureSend(state.read, { quote: asked });
  state.now.quote = new QuoteContext("q-2", 4);
  const refused = await deferredSend(capture, sendOk(calls));
  assert.equal(wasUnsent(refused), true);
  assert.equal(refused.unsent, "quote");
  assert.deepEqual(calls, []);
  state.now.quote = new QuoteContext("q-1", 4);
  assert.equal((await deferredSend(capture, sendOk(calls))).ok, true);
  assert.deepEqual(calls, ["sent"]);
});

test("sender quote: a write that follows its own quote is sent after the quote on screen changed, under the same session", async () => {
  const client = fakeClient("sales");
  const calls = [];
  const state = here(client, new QuoteContext("q-1", 1));
  const capture = captureSend(state.read, { follows: true });
  state.now.quote = new QuoteContext("q-2", 2);
  assert.equal((await deferredSend(capture, sendOk(calls))).ok, true);
  assert.deepEqual(calls, ["sent"]);
});

test("sender: an unsent answer is an error result the callers already read as a refusal", async () => {
  const client = fakeClient("sales");
  const state = here(client, null);
  const capture = captureSend(state.read);
  state.now.attached = false;
  const result = await deferredSend(capture, sendOk([]));
  assert.equal(result.ok, false);
  assert.ok(result.error.length > 20);
});
