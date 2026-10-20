// Switch role ends the Console session whenever the host holds a Console
// session object, whatever source the desk's session carries: a person the
// host restored from the front door's key is always a persona to the host
// (handoff.ts), even when the record was a Console session's, and the
// Counter Till keeps that key for the life of its frame. Without the sign out
// the Rust side's custody would resume the first person for the next one.
import { test } from "node:test";
import assert from "node:assert/strict";
import { SESSION_MIRROR_KEY, SessionStore, ThrowSafeMirror } from "@hardscapehouse/auth";
import { createConsoleSession } from "../src/console-session.js";
import { FRONT_DOOR_SESSION_KEY, readDoorSession } from "../src/handoff.js";
import { clearPendingRevocationsForTest, endSession, flushPendingRevocations, pendingRevocationsForTest } from "../src/switch-role.js";
import { NOW, doorRecord, fakeStorage } from "./support-data.mjs";

function consoleWith(calls) {
  const invoke = async (command) => {
    calls.push(command);
    if (command === "auth_sign_out") return null;
    throw { code: "unavailable" };
  };
  const sessions = new SessionStore({ mirror: new ThrowSafeMirror("test.mirror", fakeStorage()) });
  return createConsoleSession({ invoke, org: "hh-demo", sessions });
}

test("a Console record restored from the key, then Switch role: one auth_sign_out and both keys cleared", async () => {
  const storage = fakeStorage();
  // the Console person's record as the host wrote it for the till, which kept it
  storage.setItem(FRONT_DOOR_SESSION_KEY, JSON.stringify(doorRecord({ flow: "sales_manager", roleCode: "sales_manager", roleLabel: "Sales Manager" })));
  storage.setItem(SESSION_MIRROR_KEY, JSON.stringify({ v: 2, userId: "demo-account-sales", email: "e", name: "n" }));
  const restored = readDoorSession({ storage, now: () => NOW });
  assert.equal(restored?.source, "persona", "a restored record always reads as a persona");

  const calls = [];
  await endSession({ consoleSession: consoleWith(calls), storage });
  assert.deepEqual(calls, ["auth_sign_out"]);
  assert.deepEqual(storage.keys(), []);
});

test("without a Console session object (a browser) Switch role only clears the keys", async () => {
  const storage = fakeStorage();
  storage.setItem(FRONT_DOOR_SESSION_KEY, "x");
  await endSession({ consoleSession: null, storage });
  assert.deepEqual(storage.keys(), []);
});

// HH-678: the core session token is revoked server side. The tests below
// drive their own fetch, so no network is ever touched.
const TOKEN = "aaa.bbb.ccc";

function recordingFetch(calls, ok = true) {
  return async (url, init) => {
    calls.push({ url, init });
    return { status: ok ? 200 : 503 };
  };
}

test("endSession posts the token to /api/v1/auth/sign-out", async () => {
  clearPendingRevocationsForTest();
  const calls = [];
  await endSession({ consoleSession: null, storage: fakeStorage(), token: TOKEN, fetch: recordingFetch(calls) });
  assert.equal(calls.length, 1, "one revocation post");
  assert.equal(calls[0].url, "/api/v1/auth/sign-out");
  assert.equal(calls[0].init.method, "POST");
  assert.deepEqual(JSON.parse(calls[0].init.body), { token: TOKEN });
  assert.deepEqual(pendingRevocationsForTest(), [], "a 200 leaves nothing pending");
});

test("offline, the revocation is held pending and flushed when the connection returns", async () => {
  clearPendingRevocationsForTest();
  const offline = async () => {
    throw new TypeError("fetch failed");
  };
  await endSession({ consoleSession: null, storage: fakeStorage(), token: TOKEN, fetch: offline });
  assert.deepEqual(pendingRevocationsForTest(), [TOKEN], "the offline post is held");

  // A 5xx is held too: the core could not write the revocation.
  await endSession({ consoleSession: null, storage: fakeStorage(), token: TOKEN, fetch: recordingFetch([], false) });
  assert.deepEqual(pendingRevocationsForTest(), [TOKEN], "the 5xx post is held");

  const calls = [];
  await flushPendingRevocations({ fetch: recordingFetch(calls) });
  // One token, one pending entry (the set dedupes), so one flushed post.
  assert.equal(calls.length, 1, "the held post flushed");
  assert.deepEqual(pendingRevocationsForTest(), [], "nothing is held after the flush");
});

test("a 4xx is a final answer, never retried", async () => {
  clearPendingRevocationsForTest();
  const calls = [];
  const refuse = async (url, init) => {
    calls.push({ url, init });
    return { status: 401 };
  };
  await endSession({ consoleSession: null, storage: fakeStorage(), token: TOKEN, fetch: refuse });
  assert.equal(calls.length, 1);
  assert.deepEqual(pendingRevocationsForTest(), [], "a 401 is not held");
});
