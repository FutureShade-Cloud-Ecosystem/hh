// The device unlock flow with the fake credential provider (the acceptance
// line names it): engage bound to the signed-in user, unlock, the wrong-PIN
// path, the local exponential rate limiter, the open-window relock, the
// mirror discipline across a reload, and the platform adapter's
// user-verification options. The lock is a screen courtesy: no audit pair
// exists anywhere.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  DeviceLock,
  SessionStore,
  ThrowSafeMirror,
  LOCK_MIRROR_KEY,
  SESSION_MIRROR_KEY,
} from "../src/index.ts";
import {
  FAKE_ACCOUNT,
  FAKE_JWT,
  FAKE_PIN,
  DeferredCredentialProvider,
  FakeCredentialProvider,
  RecordedStorage,
  ThrowingStorage,
} from "../src/testing.ts";

const START_MS = 1_000_000_000_000;
const WRONG_PIN = "999999"; // a fake wrong PIN for tests

function clock(startMs = START_MS) {
  let nowMs = startMs;
  return {
    now: () => nowMs,
    advance: (ms) => {
      nowMs += ms;
    },
    time: () => nowMs,
  };
}

async function rig({ openWindowMs, withSession = true } = {}) {
  const recorded = new RecordedStorage();
  const time = clock();
  const sessions = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded), now: time.now });
  if (withSession) {
    sessions.adopt({ token: FAKE_JWT, tokenExpiresAtMs: time.now() + 3_600_000, user: { ...FAKE_ACCOUNT } });
  }
  const credentials = new FakeCredentialProvider();
  const lock = new DeviceLock({
    credentials,
    sessions,
    mirror: new ThrowSafeMirror(LOCK_MIRROR_KEY, recorded),
    now: time.now,
    openWindowMs,
  });
  return { recorded, credentials, lock, sessions, time };
}

test("engage binds to the signed-in user's id and takes no identity from its caller", async () => {
  const { recorded, credentials, lock } = await rig();
  assert.equal(lock.state, "absent");

  const engaged = await lock.engage();
  assert.equal(engaged.ok, true, `engage resolves a Result: ${engaged.ok ? "" : engaged.error}`);
  assert.equal(credentials.creationCount, 1, "the platform credential was registered once");
  assert.equal(lock.state, "locked");

  const payload = JSON.parse(recorded.writes.find((w) => w.key === LOCK_MIRROR_KEY).value);
  assert.equal(
    payload.userId,
    FAKE_ACCOUNT.userId,
    "the lock binds to the id of the signed-in user, not to any name a caller passed",
  );
  assert.equal("name" in payload, false, "no name is stored anywhere");
});

test("engage is refused without a signed-in user: there is nobody to bind to", async () => {
  const { recorded, credentials, lock, sessions } = await rig({ withSession: false });
  const refused = await lock.engage();
  assert.equal(refused.ok, false);
  assert.match(refused.error, /signed-in user/);
  assert.equal(credentials.creationCount, 0, "no credential was created for nobody");
  assert.equal(lock.state, "absent");
  assert.equal(recorded.writes.some((w) => w.key === LOCK_MIRROR_KEY), false, "nothing was mirrored");

  sessions.adopt({ token: FAKE_JWT, tokenExpiresAtMs: START_MS + 60_000, user: { ...FAKE_ACCOUNT } });
  assert.equal((await lock.engage()).ok, true, "once a user is signed in, engage binds to them");
});

test("no audit pair exists on the lock, in the open state or any other", async () => {
  const { credentials, lock } = await rig();
  await lock.engage();
  assert.equal(lock.auditHeaders, undefined, "the lock carries no audit header method at all");
  credentials.present(FAKE_PIN);
  assert.equal((await lock.unlock()).ok, true);
  assert.equal(lock.state, "open");
  assert.equal(lock.openUnlock, undefined, "no open-unlock identity record exists");
  const auth = await import("../src/index.ts");
  assert.equal(auth.AUDIT_USER_HEADER, undefined, "X-Unlocked-By is gone until the core can verify an unlock");
  assert.equal(auth.AUDIT_AT_HEADER, undefined, "X-Unlocked-At is gone until the core can verify an unlock");
});

test("unlock opens with the fake credential provider and is idempotent while open", async () => {
  const { credentials, lock, time } = await rig();
  await lock.engage();
  credentials.present(FAKE_PIN);
  const opened = await lock.unlock();
  assert.equal(opened.ok, true, `unlock resolves a Result: ${opened.ok ? "" : opened.error}`);
  assert.equal(lock.state, "open");

  credentials.present(null); // nobody at the device
  const again = await lock.unlock();
  assert.equal(again.ok, true, "an open lock stays open without running another prompt");
  assert.equal(credentials.verificationCount, 1, "the idempotent unlock ran no verification");
  assert.notEqual(time.time(), 0);
});

test("a wrong PIN is refused and feeds the exponential backoff, capped, reset by success", async () => {
  const { credentials, lock, time } = await rig({ openWindowMs: 3_600_000 });
  await lock.engage();

  credentials.present(WRONG_PIN);
  const first = await lock.unlock();
  assert.equal(first.ok, false);
  assert.match(first.error, /device unlock failed/);
  assert.match(first.error, /failed attempts: 1/);
  assert.equal(lock.state, "locked", "a failed attempt does not open the lock");

  const backoffs = [];
  for (let attempt = 0; attempt < 7; attempt++) {
    const tooSoon = await lock.unlock();
    assert.equal(tooSoon.ok, false, "an attempt inside the backoff is refused before the provider runs");
    assert.match(tooSoon.error, /rate limited/);
    const waitMs = Number(/try again in (\d+) ms/.exec(tooSoon.error)[1]);
    backoffs.push(waitMs);
    time.advance(waitMs);
    credentials.present(WRONG_PIN);
    const failed = await lock.unlock();
    assert.equal(failed.ok, false);
    assert.match(failed.error, /device unlock failed/);
  }
  // One second after the first failure, doubling, capped.
  assert.deepEqual(backoffs, [1000, 2000, 4000, 8000, 16000, 32000, 60000], "the backoff is exponential and capped");

  time.advance(backoffs[backoffs.length - 1]); // wait out the capped backoff
  credentials.present(FAKE_PIN);
  const recovered = await lock.unlock();
  assert.equal(recovered.ok, true, "after the backoff the correct PIN unlocks");

  lock.lock(); // re-engage so the next attempt is a real verification
  credentials.present(WRONG_PIN);
  const restart = await lock.unlock();
  assert.equal(restart.ok, false);
  const again = await lock.unlock();
  assert.match(again.error, /try again in 1000 ms/, "a success resets the backoff to the base");
});

test("a cancelled prompt counts as a failed attempt", async () => {
  const { credentials, lock } = await rig({ openWindowMs: 3_600_000 });
  await lock.engage();
  credentials.present(null);
  const cancelled = await lock.unlock();
  assert.equal(cancelled.ok, false);
  assert.match(cancelled.error, /cancelled/);
  const tooSoon = await lock.unlock();
  assert.match(tooSoon.error, /rate limited/, "the backoff applies after a cancelled prompt too");
});

test("unlock is refused outside an engaged lock", async () => {
  const { lock } = await rig();
  const unlocked = await lock.unlock();
  assert.equal(unlocked.ok, false);
  assert.match(unlocked.error, /no device lock is engaged/);
});

test("engage is refused when the platform offers no credential (WebAuthn absent)", async () => {
  const recorded = new RecordedStorage();
  const sessions = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded), now: () => START_MS });
  sessions.adopt({ token: FAKE_JWT, tokenExpiresAtMs: START_MS + 60_000, user: { ...FAKE_ACCOUNT } });
  const lock = new DeviceLock({
    credentials: new FakeCredentialProvider({ available: false }),
    sessions,
    mirror: new ThrowSafeMirror(LOCK_MIRROR_KEY, recorded),
  });
  const engaged = await lock.engage();
  assert.equal(engaged.ok, false);
  assert.match(engaged.error, /unavailable/);
  assert.equal(lock.state, "absent");
  assert.equal(recorded.writes.some((w) => w.key === LOCK_MIRROR_KEY), false, "nothing was mirrored");
});

test("a second engage without disengage is refused", async () => {
  const { lock } = await rig();
  assert.equal((await lock.engage()).ok, true);
  const second = await lock.engage();
  assert.equal(second.ok, false);
  assert.match(second.error, /already engaged/);
});

test("an open unlock relocks after its open window, tested with the injected clock", async () => {
  const { credentials, lock, time } = await rig({ openWindowMs: 5_000 });
  await lock.engage();
  credentials.present(FAKE_PIN);
  assert.equal((await lock.unlock()).ok, true);
  assert.equal(lock.state, "open");

  time.advance(2_500);
  assert.equal(lock.state, "open", "inside the open window the unlock stays open");

  time.advance(2_499);
  assert.equal(lock.state, "open", "reads do not extend the window: it is absolute from the unlock");

  time.advance(1);
  assert.equal(lock.state, "locked", "after the open window elapses the courtesy relocks itself");

  credentials.present(FAKE_PIN);
  assert.equal((await lock.unlock()).ok, true, "a relocked lock unlocks again with user verification");
});

test("the engaged lock survives a reload through the mirror; the open unlock does not", async () => {
  const recorded = new RecordedStorage();
  const time = clock();
  const sessions = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded), now: time.now });
  sessions.adopt({ token: FAKE_JWT, tokenExpiresAtMs: time.now() + 3_600_000, user: { ...FAKE_ACCOUNT } });
  const credentials = new FakeCredentialProvider(); // the platform persists across a reload
  const before = new DeviceLock({
    credentials,
    sessions,
    mirror: new ThrowSafeMirror(LOCK_MIRROR_KEY, recorded),
    now: time.now,
    openWindowMs: 3_600_000,
  });
  await before.engage();
  credentials.present(FAKE_PIN);
  await before.unlock();
  assert.equal(before.state, "open");

  const after = new DeviceLock({
    credentials,
    sessions,
    mirror: new ThrowSafeMirror(LOCK_MIRROR_KEY, recorded),
    now: time.now,
    openWindowMs: 3_600_000,
  });
  assert.equal(after.state, "locked", "a reload restores the engaged lock");
  assert.deepEqual(after.auditHeaders, undefined, "a re-locked device carries no audit method");

  credentials.present(FAKE_PIN);
  const reopened = await after.unlock();
  assert.equal(reopened.ok, true, "the restored credential id is the one the verification targets");
});

test("a degraded mirror forgets the lock across a reload, observably", async () => {
  const throwing = () => new ThrowSafeMirror(LOCK_MIRROR_KEY, new ThrowingStorage());
  const sessions = new SessionStore({ now: () => START_MS });
  sessions.adopt({ token: FAKE_JWT, tokenExpiresAtMs: START_MS + 60_000, user: { ...FAKE_ACCOUNT } });
  const before = new DeviceLock({ credentials: new FakeCredentialProvider(), sessions, mirror: throwing() });
  assert.equal(before.degraded, true);
  assert.equal((await before.engage()).ok, true, "the lock works in memory in a degraded browser");
  assert.equal(before.state, "locked");

  const after = new DeviceLock({ credentials: new FakeCredentialProvider(), sessions, mirror: throwing() });
  assert.equal(after.degraded, true, "the degradation is observable");
  assert.equal(after.state, "absent", "a degraded reload restores no lock");
});

test("the lock mirror payload carries no token, no PIN, no name, no secret", async () => {
  const { recorded, lock } = await rig();
  await lock.engage();

  const lockWrites = recorded.writes.filter((w) => w.key === LOCK_MIRROR_KEY);
  assert.equal(lockWrites.length, 1);
  const payload = JSON.parse(lockWrites[0].value);
  assert.deepEqual(
    Object.keys(payload).sort(),
    ["credentialId", "engagedAtMs", "userId", "v"],
    "the lock mirror carries the bound user id, which credential unlocks it, when: nothing else",
  );
  for (const write of lockWrites) {
    assert.equal(write.value.includes(FAKE_PIN), false, "no lock-path write carries the PIN: the package never holds one");
    assert.equal(write.value.includes(FAKE_ACCOUNT.name), false, "no lock-path write carries a display name");
  }
});

test("a stored lock payload with unknown extra fields is rewritten to the known fields on read", async () => {
  const recorded = new RecordedStorage();
  recorded.setItem(
    LOCK_MIRROR_KEY,
    JSON.stringify({
      v: 1,
      userId: "u-1",
      credentialId: "leftover-credential",
      engagedAtMs: START_MS,
      name: "Leftover Display Name",
    }),
  );
  const credentials = new FakeCredentialProvider();
  const time = clock();
  const sessions = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded), now: time.now });
  sessions.adopt({ token: FAKE_JWT, tokenExpiresAtMs: time.now() + 3_600_000, user: { ...FAKE_ACCOUNT } });
  const lock = new DeviceLock({
    credentials,
    sessions,
    mirror: new ThrowSafeMirror(LOCK_MIRROR_KEY, recorded),
    now: time.now,
    openWindowMs: 3_600_000,
  });
  assert.equal(lock.state, "locked", "the valid engaged lock restores");

  const rewritten = recorded.getItem(LOCK_MIRROR_KEY);
  assert.notEqual(rewritten, null, "the payload was rewritten in storage");
  const payload = JSON.parse(rewritten);
  assert.deepEqual(
    Object.keys(payload).sort(),
    ["credentialId", "engagedAtMs", "userId", "v"],
    "storage now holds exactly the known fields",
  );
  assert.equal(rewritten.includes("Leftover Display Name"), false, "the unknown field is gone from storage");
});

test("disengage removes the lock from memory and mirror", async () => {
  const { recorded, lock, sessions } = await rig();
  await lock.engage();
  assert.equal(lock.state, "locked");
  lock.disengage();
  assert.equal(lock.state, "absent");
  const mirrorNow = new ThrowSafeMirror(LOCK_MIRROR_KEY, recorded);
  assert.equal(mirrorNow.read(), undefined, "the lock's mirror entry was cleared, not left for a later reader");

  const after = new DeviceLock({ credentials: new FakeCredentialProvider(), sessions, mirror: new ThrowSafeMirror(LOCK_MIRROR_KEY, recorded) });
  assert.equal(after.state, "absent", "a reload after disengage restores nothing");
});

// The deferred credential provider holds one platform verification open per
// call; this loop waits until the lock has actually asked the platform.
async function untilPending(provider, count) {
  while (provider.pending.length < count) {
    await new Promise((resolve) => setImmediate(resolve));
  }
  return provider.pending[count - 1];
}

// The rig for the in-flight races: every verify is held open for the test to
// complete, so a lock change can land mid prompt.
async function deferredRig() {
  const recorded = new RecordedStorage();
  const time = clock();
  const sessions = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded), now: time.now });
  sessions.adopt({ token: FAKE_JWT, tokenExpiresAtMs: time.now() + 3_600_000, user: { ...FAKE_ACCOUNT } });
  const credentials = new DeferredCredentialProvider();
  const lock = new DeviceLock({
    credentials,
    sessions,
    mirror: new ThrowSafeMirror(LOCK_MIRROR_KEY, recorded),
    now: time.now,
    openWindowMs: 3_600_000,
  });
  return { recorded, credentials, lock, sessions, time };
}

test("a lock during an unlock in flight ends locked: the late verification adopts nothing", async () => {
  const { credentials, lock } = await deferredRig();
  assert.equal((await lock.engage()).ok, true);

  const opening = lock.unlock();
  const verify = await untilPending(credentials, 1);
  lock.lock(); // the user taps lock while the platform prompt is still up
  verify.complete(undefined); // the platform answers late with a success
  const result = await opening;

  assert.equal(result.ok, false, "the superseded attempt resolves an error Result");
  assert.match(result.error, /superseded/);
  assert.equal(lock.state, "locked", "the late verification must not open the locked lock");

  const again = lock.unlock();
  const againVerify = await untilPending(credentials, 2);
  againVerify.complete(undefined); // the next attempt reaches the platform at once
  assert.equal((await again).ok, true, "no backoff was recorded from the superseded failure");
  assert.equal(lock.state, "open");
});

test("a disengage during an unlock in flight stays absent, and a re-engage starts locked", async () => {
  const { credentials, lock } = await deferredRig();
  assert.equal((await lock.engage()).ok, true);

  const opening = lock.unlock();
  const verify = await untilPending(credentials, 1);
  lock.disengage(); // the lock is removed while the platform prompt is still up
  verify.complete(undefined); // the platform answers late with a success
  const result = await opening;

  assert.equal(result.ok, false);
  assert.match(result.error, /superseded/);
  assert.equal(lock.state, "absent", "the late verification must not reopen a removed lock");
  assert.equal((await lock.engage()).ok, true, "the device can be re-engaged");
  assert.equal(lock.state, "locked", "the re-engaged lock starts locked: no stale open unlock survived");
});

test("lock() on an already locked lock does not clear the failure backoff", async () => {
  const { credentials, lock } = await rig({ openWindowMs: 3_600_000 });
  await lock.engage();

  credentials.present(WRONG_PIN);
  const failed = await lock.unlock();
  assert.equal(failed.ok, false);

  lock.lock(); // a no-op on a locked lock: it must not reset the courtesy backoff
  const tooSoon = await lock.unlock();
  assert.equal(tooSoon.ok, false);
  assert.match(tooSoon.error, /rate limited/, "lock() on a locked lock must not clear the backoff");
});

test("of two concurrent unlocks only the newer attempt's outcome lands", async () => {
  const { credentials, lock } = await deferredRig();
  assert.equal((await lock.engage()).ok, true);

  const first = lock.unlock();
  const second = lock.unlock();
  const firstVerify = await untilPending(credentials, 1);
  const secondVerify = await untilPending(credentials, 2);

  secondVerify.fail(new Error("fake: user verification failed")); // the newer attempt fails...
  firstVerify.complete(undefined); // ...and the older attempt succeeds late: superseded either way

  const firstResult = await first;
  assert.equal(firstResult.ok, false, "the older attempt's late success adopts nothing");
  assert.match(firstResult.error, /superseded/);
  const secondResult = await second;
  assert.equal(secondResult.ok, false, "the newer attempt's failure is the one that lands");
  assert.match(secondResult.error, /failed attempts: 1/, "exactly one failure was recorded");

  const third = await lock.unlock();
  assert.match(third.error, /try again in 1000 ms/, "the superseded success fed no failure count: the backoff is the base");
});

test("unlock binds to the user who engaged: a different signed-in user is refused without a prompt", async () => {
  const { credentials, lock, sessions, time } = await rig({ openWindowMs: 3_600_000 });
  await lock.engage();
  const promptCountBefore = credentials.verificationCount;

  sessions.adopt({
    token: FAKE_JWT,
    tokenExpiresAtMs: time.now() + 3_600_000,
    user: { ...FAKE_ACCOUNT, userId: "someone-else", email: "someone.else@example.invalid", name: "Someone Else" },
  });
  credentials.present(FAKE_PIN);
  const refused = await lock.unlock();
  assert.equal(refused.ok, false, "another user's session cannot unlock alice's lock");
  assert.match(refused.error, /binds to the user who engaged/);
  assert.equal(credentials.verificationCount, promptCountBefore, "the platform was never asked");

  sessions.adopt({ token: FAKE_JWT, tokenExpiresAtMs: time.now() + 3_600_000, user: { ...FAKE_ACCOUNT } });
  credentials.present(FAKE_PIN);
  assert.equal((await lock.unlock()).ok, true, "the engaging user's session still unlocks");
});

test("the platform adapter demands user verification on create and on get", async () => {
  const calls = [];
  const stubCredentials = {
    create: async (options) => {
      calls.push(["create", options]);
      return { rawId: new Uint8Array([1, 2, 3, 4]).buffer };
    },
    get: async (options) => {
      calls.push(["get", options]);
      return { rawId: new Uint8Array([1, 2, 3, 4]).buffer };
    },
  };
  const injectedNavigator = { credentials: stubCredentials };
  const realDescriptor = Object.getOwnPropertyDescriptor(globalThis, "navigator");
  Object.defineProperty(globalThis, "navigator", { value: injectedNavigator, configurable: true });
  try {
    const { webAuthnCredentialProvider } = await import("../src/lock.ts");
    const provider = webAuthnCredentialProvider({ rpId: "edge.example.test" });
    await provider.create();
    await provider.verify("aGF3");
  } finally {
    Object.defineProperty(globalThis, "navigator", realDescriptor);
  }

  assert.equal(calls.length, 2, "one create ceremony and one get ceremony ran against the platform");
  const [createName, createOptions] = calls[0];
  assert.equal(createName, "create");
  assert.equal(
    createOptions.publicKey.authenticatorSelection.userVerification,
    "required",
    "registration pins userVerification to required: the discouraged spelling must turn this test red",
  );
  assert.equal(createOptions.publicKey.authenticatorSelection.residentKey, "required");
  assert.equal(createOptions.publicKey.authenticatorSelection.authenticatorAttachment, "platform");
  assert.equal(createOptions.publicKey.rp.id, "edge.example.test");
  assert.ok(createOptions.publicKey.challenge instanceof Uint8Array, "the challenge is real random bytes");
  assert.equal(createOptions.publicKey.challenge.length, 32);

  const [getName, getOptions] = calls[1];
  assert.equal(getName, "get");
  assert.equal(
    getOptions.publicKey.userVerification,
    "required",
    "verification pins userVerification to required: the discouraged spelling must turn this test red",
  );
  assert.equal(getOptions.publicKey.allowCredentials.length, 1, "the assertion targets exactly the registered credential");
});
