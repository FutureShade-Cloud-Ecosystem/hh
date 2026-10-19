// Switch role ends the Console session whenever the host holds a Console
// session object, whatever source the desk's session carries: a person the
// host restored from the front door's key is always a persona to the host
// (handoff.ts), even when the record was a Console session's, and the
// Counter Till keeps that key for the life of its frame. Without the sign out
// the Rust side's custody would resume the first person for the next one.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
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

// PRR-531: in a native Tauri profile the webview's own origin is the asset
// server, so a relative sign-out post reaches a static file. The host resolves
// the core's base URL from the served config and forwards it to both the
// revocation post and the offline flush.
const CORE = "https://core.futurebuild.ai";

test("with a coreBaseUrl the revocation post reaches the core, not the asset server", async () => {
  clearPendingRevocationsForTest();
  const calls = [];
  await endSession({ consoleSession: null, storage: fakeStorage(), token: TOKEN, fetch: recordingFetch(calls), coreBaseUrl: CORE });
  assert.equal(calls[0].url, `${CORE}/api/v1/auth/sign-out`);
});

test("the offline flush retries against the coreBaseUrl it is given", async () => {
  clearPendingRevocationsForTest();
  const offline = async () => {
    throw new TypeError("fetch failed");
  };
  await endSession({ consoleSession: null, storage: fakeStorage(), token: TOKEN, fetch: offline, coreBaseUrl: CORE });
  assert.deepEqual(pendingRevocationsForTest(), [TOKEN], "the offline post is held");
  const calls = [];
  await flushPendingRevocations({ fetch: recordingFetch(calls), coreBaseUrl: CORE });
  assert.equal(calls.length, 1, "the held post flushed");
  assert.equal(calls[0].url, `${CORE}/api/v1/auth/sign-out`, "the flush posts to the core, not the asset server");
  assert.deepEqual(pendingRevocationsForTest(), []);
});

// PR #495 round 1 part B: in a native Tauri profile the revocation never
// posts from the page (the CSP holds no network origin). The shell's command
// holds the core URL and posts it; an IPC refusal that is an outage word is
// held pending like a 5xx, a final verdict code is not.
const IPC_TOKEN = "aaa.bbb.ccc";

function recordingInvoke(calls, answer) {
  return async (command, args) => {
    calls.push({ command, args });
    if (answer instanceof Error) throw answer;
    if (answer !== undefined) throw answer;
    return null;
  };
}

test("in a native profile the revocation goes through the shell command, never the page's fetch", async () => {
  clearPendingRevocationsForTest();
  const ipcCalls = [];
  const fetchCalls = [];
  const fetch = async (url, init) => {
    fetchCalls.push({ url, init });
    return { status: 200 };
  };
  await endSession({ consoleSession: null, storage: fakeStorage(), token: IPC_TOKEN, fetch, invoke: recordingInvoke(ipcCalls) });
  assert.equal(ipcCalls.length, 1, "one revocation command");
  assert.deepEqual(ipcCalls[0], { command: "cmd_sign_out_revoke", args: { jwt: IPC_TOKEN } });
  assert.equal(fetchCalls.length, 0, "the page never posts to the network in a native profile");
  assert.deepEqual(pendingRevocationsForTest(), [], "a resolved command leaves nothing pending");
});

test("an IPC outage is held pending and flushed through the command; a final code is not held", async () => {
  clearPendingRevocationsForTest();
  const outage = [];
  await endSession({ consoleSession: null, storage: fakeStorage(), token: IPC_TOKEN, invoke: recordingInvoke(outage, { code: "unavailable" }) });
  assert.deepEqual(pendingRevocationsForTest(), [IPC_TOKEN], "an outage word is held like a 5xx");

  const flushed = [];
  await flushPendingRevocations({ invoke: recordingInvoke(flushed) });
  assert.deepEqual(flushed, [{ command: "cmd_sign_out_revoke", args: { jwt: IPC_TOKEN } }], "the held token flushed through the command");
  assert.deepEqual(pendingRevocationsForTest(), []);

  await endSession({ consoleSession: null, storage: fakeStorage(), token: IPC_TOKEN, invoke: recordingInvoke([], { code: "unauthorized" }) });
  assert.deepEqual(pendingRevocationsForTest(), [], "a final verdict code is never retried");
});

const hostSrc = readFileSync(fileURLToPath(new URL("../src/host-element.ts", import.meta.url)), "utf8");

test("the host resolves the core base once at boot and forwards it to every revocation path", () => {
  assert.ok(
    /resolveCoreBaseUrl\(\{ win: window \}\)/.test(hostSrc),
    "the host resolves the core base URL through the one function, passing the page window (the global is read)",
  );
  assert.ok(
    /endSession\(\{[^}]*coreBaseUrl: this\.#coreBaseUrl/.test(hostSrc),
    "Switch role's endSession carries the resolved core base URL",
  );
  assert.ok(
    /#coreReady\.then\(\(\) => flushPendingRevocations\(\{ coreBaseUrl: this\.#coreBaseUrl, invoke: this\.#shellInvoke \?\? undefined \}\)\)/.test(hostSrc),
    "the back-online flush waits for boot and carries the resolved core base URL and the shell's invoke",
  );
});
