// Session custody: the JWT lives in memory only, the mirror carries the
// display profile and nothing else (no token, no handle, no secret of any
// kind, in any storage), and the throw-safe mirror degrades observably when
// the storage accessor throws, the ERP shim's recorded finding carried with
// its rationale in src/session.ts.
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

function rig() {
  const recorded = new RecordedStorage();
  const mirror = new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded);
  let nowMs = START_MS;
  const now = () => nowMs;
  const store = new SessionStore({ mirror, now });
  return {
    recorded,
    mirror,
    store,
    now,
    advance: (ms) => {
      nowMs += ms;
    },
    sessionInput: () => ({
      token: FAKE_JWT,
      tokenExpiresAtMs: nowMs + 60_000,
      user: { ...FAKE_ACCOUNT },
    }),
  };
}

test("adopt holds the token in memory and mirrors the display profile, never the token", () => {
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
    ["email", "name", "userId", "v"],
    "the mirrored payload carries the display profile and nothing else",
  );
  assert.equal("token" in payload, false, "no token field in the mirror");
  assert.equal("handle" in payload, false, "no handle field in the mirror: no refresh handle exists");
  for (const write of recorded.writes) {
    assert.equal(
      write.value.includes(FAKE_JWT),
      false,
      `no mirrored write carries the token (key ${write.key})`,
    );
  }
});

test("a reload restores the display profile and nothing else", () => {
  const { recorded, sessionInput } = rig();
  const first = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded), now: () => START_MS });
  first.adopt(sessionInput());

  const reloaded = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded), now: () => START_MS });
  assert.equal(reloaded.current, null, "a reload restores no token: none was ever stored");
  assert.deepEqual(reloaded.profile, { ...FAKE_ACCOUNT }, "a reload restores the display profile");
  const raw = recorded.writes.find((w) => w.key === SESSION_MIRROR_KEY).value;
  assert.equal(raw.includes("handle"), false, "the payload names no handle: nothing in storage trades for a session");
  assert.equal(raw.includes(FAKE_JWT), false, "the payload carries no token");
});

test("the custody holds nothing to trade: no storage write anywhere is bearer-capable", () => {
  const { recorded, store, sessionInput } = rig();
  store.adopt(sessionInput());
  store.clear();
  store.adopt(sessionInput());
  for (const write of recorded.writes) {
    const payload = JSON.parse(write.value);
    for (const key of Object.keys(payload)) {
      assert.equal(
        /token|handle|secret|jwt/i.test(key),
        false,
        `the payload key ${key} is credential-shaped and must not be stored`,
      );
    }
  }
});

test("the store serialises to nothing secret, and the private fields are private at run time", () => {
  const { store, sessionInput } = rig();
  store.adopt(sessionInput());
  assert.equal(Object.keys(store).includes("token"), false, "the token is not an enumerable property");
  assert.equal(store.token, undefined, "the token is not readable as a property");
  assert.equal(store.mirror, undefined, "the mirror is not on the public surface");
  const printed = JSON.stringify(store);
  assert.equal(printed.includes(FAKE_JWT), false, "nothing secret serialises out of the store");
  const parsed = JSON.parse(printed);
  assert.deepEqual(
    Object.keys(parsed).sort(),
    ["degraded", "profile", "signedIn"],
    "the store serialises to the degradation flag, the live flag, and the display profile",
  );
  assert.equal(parsed.signedIn, true);
  assert.deepEqual(parsed.profile, { ...FAKE_ACCOUNT });
});

test("clear wipes memory and mirror", () => {
  const { recorded, store, sessionInput } = rig();
  store.adopt(sessionInput());
  store.clear();
  assert.equal(store.current, null);
  assert.equal(store.profile, null);
  assert.equal(recorded.size, 0, "the mirror holds nothing after clear");
});

test("an expired token reads as null while the display profile stays", () => {
  const { store, sessionInput, advance } = rig();
  store.adopt(sessionInput());
  assert.notEqual(store.current, null);
  advance(60_000);
  assert.equal(store.current, null, "an expired token is not a live session");
  assert.notEqual(store.profile, null, "the display profile is not a live session and not a credential");
});

test("adopt refuses unusable input and changes nothing", () => {
  const { recorded, store, sessionInput } = rig();
  const good = sessionInput();
  assert.equal(store.adopt({ ...good, token: "" }), false, "an empty token is refused");
  assert.equal(store.adopt({ ...good, tokenExpiresAtMs: START_MS - 1 }), false, "an expired token is refused");
  assert.equal(store.adopt({ ...good, user: { ...FAKE_ACCOUNT, userId: "" } }), false, "a user without an id is refused");
  assert.equal(store.current, null, "nothing was adopted");
  assert.equal(recorded.writes.some((w) => w.key === SESSION_MIRROR_KEY), false, "nothing was mirrored");
});

test("an unusable mirror payload is dropped from storage, not merely unread", () => {
  const cases = [
    "not json at all",
    // a version 1 payload carried a refresh handle; the rework stores no
    // credential of any kind, so a leftover v1 payload is refused and wiped
    JSON.stringify({ v: 1, handle: "leftover-handle", handleExpiresAtMs: START_MS + 1000, userId: "u", email: "x", name: "y" }),
    JSON.stringify({ v: 2, userId: "", email: "x", name: "y" }),
    JSON.stringify({ v: 2, email: "x", name: "y" }),
    JSON.stringify({ v: 3, userId: "u", email: "x", name: "y" }),
  ];
  for (const raw of cases) {
    const recorded = new RecordedStorage();
    recorded.setItem(SESSION_MIRROR_KEY, raw);
    const store = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded), now: () => START_MS });
    assert.equal(store.profile, null, `the payload ${raw} reads as absent`);
    assert.equal(
      recorded.size,
      0,
      `the payload ${raw} was dropped from storage, not kept where a later reader could trust it`,
    );
  }
});

test("a stored payload with unknown extra fields is rewritten to the known fields on read", () => {
  // A leftover from an older version: the payload validates, so the profile
  // restores, but what it carries beyond the known fields (here a token
  // shape the current package would never write) must not stay in storage.
  const recorded = new RecordedStorage();
  recorded.setItem(
    SESSION_MIRROR_KEY,
    JSON.stringify({ v: 2, userId: "u-1", email: "x@example.test", name: "Y", token: "LEFTOVER-OLDER-VERSION" }),
  );
  const store = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded), now: () => START_MS });
  assert.deepEqual(store.profile, { userId: "u-1", email: "x@example.test", name: "Y" }, "the valid profile restores");

  const rewritten = recorded.getItem(SESSION_MIRROR_KEY);
  assert.notEqual(rewritten, null, "the payload was rewritten in storage");
  const payload = JSON.parse(rewritten);
  assert.deepEqual(
    Object.keys(payload).sort(),
    ["email", "name", "userId", "v"],
    "storage now holds exactly the known fields",
  );
  assert.equal(rewritten.includes("LEFTOVER-OLDER-VERSION"), false, "the unknown field is gone from storage");
});

test("no field other than the four known ones is ever written to the session mirror: the write side of the read rule", () => {
  // Colton's ruling on the email in session storage (relay message 047):
  // unknown fields are stripped on read (the test above), and this is the
  // same rule on write. Every code path that writes the mirror is driven
  // here, and every write that lands under the key is checked field by
  // field: only v, userId, email and name may ever be written.
  const recorded = new RecordedStorage();

  // Write site one: the constructor's rewrite of a payload that validates
  // but carries fields this version does not know. The seeded leftover is
  // the test's own fixture, not a package write: only writes made after the
  // seed are the package's.
  recorded.setItem(
    SESSION_MIRROR_KEY,
    JSON.stringify({ v: 2, userId: "u-1", email: "x@example.test", name: "Y", token: "LEFTOVER-OLDER-VERSION" }),
  );
  const seedWrites = recorded.writes.length;
  new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded), now: () => START_MS });

  // Write site two: adopt, the one path a session's profile reaches the
  // mirror by (a sign-in answer, a Google callback, a resume's mint all
  // adopt through it).
  const store = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded), now: () => START_MS });
  assert.equal(
    store.adopt({ token: FAKE_JWT, tokenExpiresAtMs: START_MS + 60_000, user: { ...FAKE_ACCOUNT } }),
    true,
  );
  assert.equal(
    store.adopt({
      token: FAKE_JWT,
      tokenExpiresAtMs: START_MS + 120_000,
      user: { userId: "u-2", email: "second@example.test", name: "Second" },
    }),
    true,
    "a second adoption writes again, so the check below is not vacuous",
  );

  const writes = recorded.writes.slice(seedWrites).filter((w) => w.key === SESSION_MIRROR_KEY);
  assert.ok(writes.length >= 3, `the rewrite and both adoptions must have written (saw ${writes.length})`);
  for (const write of writes) {
    const payload = JSON.parse(write.value);
    assert.deepEqual(
      Object.keys(payload).sort(),
      ["email", "name", "userId", "v"],
      "the write carries exactly the four known fields: v, userId, email, name, and nothing else",
    );
    assert.equal("token" in payload, false, "no token field is ever written");
    assert.equal("handle" in payload, false, "no handle field is ever written: no refresh handle exists");
  }
  // A clear is a removal, not a write; drive one so the whole mirror
  // lifecycle runs inside the capture.
  store.clear();
  assert.equal(recorded.getItem(SESSION_MIRROR_KEY), null, "the clear removed the payload");
  const writesAfterClear = recorded.writes.slice(seedWrites).filter((w) => w.key === SESSION_MIRROR_KEY);
  assert.equal(writesAfterClear.length, writes.length, "the clear added no write");
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
  });
  assert.notEqual(store.current, null, "custody works in a degraded browser: sign-in still works");
  assert.notEqual(store.profile, null, "within the process the display profile still reads");

  const reloaded = new SessionStore({ mirror: throwing(), now: () => START_MS });
  assert.equal(reloaded.degraded, true);
  assert.equal(reloaded.profile, null, "a degraded reload restores nothing, which is the honest posture");
});

test("the default resolution degrades where no browser storage exists (the Node test process)", () => {
  const store = new SessionStore();
  assert.equal(store.degraded, true);
});

test("mirror writes are throw-safe: a serialisation failure returns false and throws nothing", () => {
  const mirror = new ThrowSafeMirror(SESSION_MIRROR_KEY, new RecordedStorage());
  const circular = {};
  circular.self = circular;
  assert.equal(mirror.write(circular), false, "an unserialisable payload is refused as data");
  assert.equal(mirror.write({ v: 2 }), true, "a plain payload writes");
});

test("mirror reads are throw-safe and validated rather than cast", () => {
  const mirror = new ThrowSafeMirror(SESSION_MIRROR_KEY, new RecordedStorage());
  assert.equal(mirror.read(), undefined, "an empty mirror reads as undefined");
  mirror.write({ v: 2 });
  assert.deepEqual(mirror.read(), { v: 2 });
  mirror.clear();
  assert.equal(mirror.read(), undefined);
});
