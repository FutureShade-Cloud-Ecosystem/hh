// Session custody: the JWT lives in memory only, the mirror carries a
// refresh handle and a profile (never the token), and the throw-safe mirror
// degrades observably when the storage accessor throws, the ERP shim's
// recorded finding carried with its rationale in src/session.ts.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  SessionStore,
  ThrowSafeMirror,
  SESSION_MIRROR_KEY,
} from "../src/session.ts";
import {
  FAKE_ACCOUNT,
  FAKE_JWT,
  RecordedStorage,
  ThrowingStorage,
} from "../src/testing.ts";

const START_MS = 1_000_000_000_000;
const HANDLE = "fake-refresh-handle-1";

function rig() {
  const recorded = new RecordedStorage();
  const mirror = new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded);
  let nowMs = START_MS;
  const now = () => nowMs;
  const store = new SessionStore({ mirror, now });
  return {
    recorded,
    store,
    now,
    advance: (ms) => {
      nowMs += ms;
    },
    sessionInput: () => ({
      token: FAKE_JWT,
      tokenExpiresAtMs: nowMs + 60_000,
      user: { ...FAKE_ACCOUNT },
      refreshHandle: { handle: HANDLE, expiresAtMs: nowMs + 3_600_000 },
    }),
  };
}

test("adopt holds the token in memory and mirrors the handle, never the token", () => {
  const { recorded, store, sessionInput } = rig();
  assert.equal(store.degraded, false, "a working accessor is not degraded");
  assert.equal(store.current, null, "no session before adopt");
  assert.equal(store.adopt(sessionInput()), true);
  assert.equal(store.current.token, FAKE_JWT, "the live token reads from memory");

  const writes = recorded.writes.filter((w) => w.key === SESSION_MIRROR_KEY);
  assert.equal(writes.length, 1, "exactly one mirrored payload");
  const payload = JSON.parse(writes[0].value);
  assert.deepEqual(
    Object.keys(payload).sort(),
    ["email", "handle", "handleExpiresAtMs", "name", "userId", "v"],
    "the mirrored payload carries the handle and the profile and nothing else",
  );
  assert.equal("token" in payload, false, "no token field in the mirror");
  for (const write of recorded.writes) {
    assert.equal(
      write.value.includes(FAKE_JWT),
      false,
      `no mirrored write carries the token (key ${write.key})`,
    );
  }
});

test("a reload restores the handle and the profile, never a token", () => {
  const { recorded, sessionInput } = rig();
  const first = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded), now: () => START_MS });
  first.adopt(sessionInput());

  const reloaded = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded), now: () => START_MS });
  assert.equal(reloaded.current, null, "a reload restores no token: none was ever stored");
  assert.deepEqual(
    reloaded.restorable,
    { handle: HANDLE, expiresAtMs: START_MS + 3_600_000, user: { ...FAKE_ACCOUNT } },
    "a reload restores the refresh handle and its profile",
  );
});

test("clear wipes memory and mirror", () => {
  const { recorded, store, sessionInput } = rig();
  store.adopt(sessionInput());
  store.clear();
  assert.equal(store.current, null);
  assert.equal(store.restorable, null);
  assert.equal(recorded.size, 0, "the mirror holds nothing after clear");
});

test("an expired token reads as null while the handle stays; an expired handle is dropped", () => {
  const { store, sessionInput, advance } = rig();
  store.adopt(sessionInput());
  assert.notEqual(store.current, null);
  advance(60_000);
  assert.equal(store.current, null, "an expired token is not a live session");
  assert.notEqual(store.restorable, null, "the handle may outlive the token");
  advance(3_600_000);
  assert.equal(store.restorable, null, "an expired handle is dropped, never surfaced");
});

test("adopt refuses unusable input and changes nothing", () => {
  const { recorded, store, sessionInput } = rig();
  const good = sessionInput();
  assert.equal(store.adopt({ ...good, token: "" }), false, "an empty token is refused");
  assert.equal(store.adopt({ ...good, tokenExpiresAtMs: START_MS - 1 }), false, "an expired token is refused");
  assert.equal(store.adopt({ ...good, user: { ...FAKE_ACCOUNT, userId: "" } }), false, "a user without an id is refused");
  assert.equal(store.adopt({ ...good, refreshHandle: { handle: "", expiresAtMs: START_MS + 1 } }), false, "an empty handle is refused");
  assert.equal(store.current, null, "nothing was adopted");
  assert.equal(recorded.writes.some((w) => w.key === SESSION_MIRROR_KEY), false, "nothing was mirrored");
});

test("adopt without a refresh handle leaves nothing restorable: a new identity inherits nobody's handle", () => {
  const { recorded, store, sessionInput } = rig();
  store.adopt(sessionInput());
  assert.notEqual(store.restorable, null);
  store.adopt({ ...sessionInput(), refreshHandle: null });
  assert.notEqual(store.current, null, "the new token is live");
  assert.equal(store.restorable, null, "the previous handle did not survive the new identity");
  assert.equal(recorded.size, 0, "the mirror was cleared");
});

test("the mirror degrades observably on a throwing accessor, and a degraded reload restores nothing", () => {
  const throwing = () => new ThrowSafeMirror(SESSION_MIRROR_KEY, new ThrowingStorage());
  const mirror = throwing();
  const store = new SessionStore({ mirror, now: () => START_MS });
  assert.equal(store.degraded, true, "the degradation is observable");
  store.adopt({
    token: FAKE_JWT,
    tokenExpiresAtMs: START_MS + 60_000,
    user: { ...FAKE_ACCOUNT },
    refreshHandle: { handle: HANDLE, expiresAtMs: START_MS + 3_600_000 },
  });
  assert.notEqual(store.current, null, "custody works in a degraded browser: sign-in still works");
  assert.notEqual(store.restorable, null, "within the process the handle still reads");

  const reloaded = new SessionStore({ mirror: throwing(), now: () => START_MS });
  assert.equal(reloaded.degraded, true);
  assert.equal(reloaded.restorable, null, "a degraded reload restores nothing, which is the honest posture");
});

test("the default resolution degrades where no sessionStorage exists (the Node test process)", () => {
  const store = new SessionStore();
  assert.equal(store.degraded, true);
});

test("a corrupt or hand-edited mirror payload is dropped, never trusted", () => {
  const recorded = new RecordedStorage();
  recorded.setItem(SESSION_MIRROR_KEY, "not json at all");
  let store = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded), now: () => START_MS });
  assert.equal(store.restorable, null, "a corrupt payload reads as absent");

  recorded.setItem(
    SESSION_MIRROR_KEY,
    JSON.stringify({ v: 1, handle: HANDLE, handleExpiresAtMs: START_MS + 1000, userId: "", email: "x", name: "y", token: FAKE_JWT }),
  );
  store = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded), now: () => START_MS });
  assert.equal(store.restorable, null, "a payload without a user id is dropped");

  recorded.setItem(
    SESSION_MIRROR_KEY,
    JSON.stringify({ v: 2, handle: HANDLE, handleExpiresAtMs: START_MS + 1000, userId: "u", email: "x", name: "y" }),
  );
  store = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded), now: () => START_MS });
  assert.equal(store.restorable, null, "an unknown payload version is dropped");
});

test("mirror writes are throw-safe: a serialisation failure returns false and throws nothing", () => {
  const mirror = new ThrowSafeMirror(SESSION_MIRROR_KEY, new RecordedStorage());
  const circular = {};
  circular.self = circular;
  assert.equal(mirror.write(circular), false, "an unserialisable payload is refused as data");
  assert.equal(mirror.write({ v: 1 }), true, "a plain payload writes");
});

test("mirror reads are throw-safe and validated rather than cast", () => {
  const mirror = new ThrowSafeMirror(SESSION_MIRROR_KEY, new RecordedStorage());
  assert.equal(mirror.read(), undefined, "an empty mirror reads as undefined");
  mirror.write({ v: 1 });
  assert.deepEqual(mirror.read(), { v: 1 });
  mirror.clear();
  assert.equal(mirror.read(), undefined);
});
