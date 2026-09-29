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

test("engage takes no identity argument: a caller-supplied id is never stored", async () => {
  // The untyped call is deliberate: the signature takes no argument, so this
  // compiles and runs, and whatever a caller passes must not become the
  // bound identity. A mutation giving engage an optional caller id that the
  // lock stored would turn this red.
  const { recorded, lock } = await rig();
  const engaged = await lock.engage("someone-else");
  assert.equal(engaged.ok, true, "engage still works; the argument is simply not part of the signature");
  const payload = JSON.parse(recorded.writes.find((w) => w.key === LOCK_MIRROR_KEY).value);
  assert.equal(payload.userId, FAKE_ACCOUNT.userId, "the lock binds to the signed-in user's id from custody");
  assert.notEqual(payload.userId, "someone-else", "no caller-named identity is stored");
  assert.equal("name" in payload, false, "and no display name either");
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
  // A leftover from an older version: the payload validates, so the lock
  // restores, but what it carries beyond the known fields (here a display
  // name the current package would never write) must not stay in storage.
  // The bound user is the signed-in user: a lock for anybody else is a
  // different rule's case (the ruling's start-up check clears it).
  const recorded = new RecordedStorage();
  recorded.setItem(
    LOCK_MIRROR_KEY,
    JSON.stringify({
      v: 1,
      userId: FAKE_ACCOUNT.userId,
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
// call; this loop waits until the lock has actually asked the platform. The
// bound keeps a regression a fast red instead of a hang: if the lock never
// asks the platform, this throws and the waiting test fails with the reason.
const PENDING_LIMIT = 10_000;

async function untilPending(provider, count) {
  for (let i = 0; i < PENDING_LIMIT; i += 1) {
    if (provider.pending.length >= count) return provider.pending[count - 1];
    await new Promise((resolve) => setImmediate(resolve));
  }
  throw new Error(`the lock never made verification call ${count} the test waits to complete`);
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

test("four concurrent wrong attempts all reach the verifier, and the newest failure alone shapes the backoff", async () => {
  // The recorded courtesy gap: the backoff counts a failure only when one
  // LANDS, so concurrent attempts are not pre-refused by it and all of them
  // reach the platform. What the README states is the recorded backoff's
  // shape (one second after the first failure, doubling, capped), and this
  // test holds that shape under the concurrency.
  const { credentials, lock } = await deferredRig();
  assert.equal((await lock.engage()).ok, true);

  const attempts = [lock.unlock(), lock.unlock(), lock.unlock(), lock.unlock()];
  await untilPending(credentials, 4);
  assert.equal(credentials.verificationCount, 4, "every concurrent attempt reached the platform: the backoff refused none of them");

  for (const handoff of credentials.pending) {
    handoff.fail(new Error("fake: user verification failed")); // all four are wrong
  }
  const results = await Promise.all(attempts);
  const superseded = results.filter((r) => !r.ok && /superseded/.test(r.error)).length;
  assert.equal(superseded, 3, "only the newest attempt's outcome lands: the three superseded failures recorded nothing");
  const newest = results.find((r) => !r.ok && /failed attempts: 1/.test(r.error));
  assert.notEqual(newest, undefined, "exactly one failure was recorded, from the newest attempt");
  assert.equal(lock.state, "locked", "no wrong attempt opened the lock");

  const tooSoon = await lock.unlock();
  assert.equal(tooSoon.ok, false);
  assert.match(tooSoon.error, /try again in 1000 ms/, "the recorded backoff is the README's shape: one second after the first failure");
});

test("an unlock with no live session opens: the binding check has no user to compare", async () => {
  // The recorded gap, stated as behaviour and now bounded by Colton's
  // ruling C (relay message 048): the lock binds to the user who engaged,
  // but that check compares against the LIVE session, and with no live
  // session there is nothing to compare. A bare clear() is not a sign-out:
  // no counter moves and the lock is not disengaged, so this custody is the
  // expiry-shaped one, and the unlock proceeds. A sign-out is the act that
  // refuses (the sign-out counter, test/lock-signout.test.mjs); if this
  // acceptance ever narrows further, it is a decision, not an accident.
  const { credentials, lock, sessions } = await rig({ openWindowMs: 3_600_000 });
  assert.equal((await lock.engage()).ok, true);
  sessions.clear(); // the custody holds no live session
  assert.equal(sessions.current, null);

  credentials.present(FAKE_PIN);
  const opened = await lock.unlock();
  assert.equal(opened.ok, true, "an unlock with no session is not refused: there is nobody to bind to");
  assert.equal(lock.state, "open");
});

// A credential provider whose create is parked per call, so an engage can be
// held mid ceremony and released by the test, and two engages can race.
function gatedCreateProvider() {
  const pendingCreates = [];
  return {
    provider: {
      isAvailable: async () => true,
      create: () => new Promise((resolve) => pendingCreates.push({ resolve })),
      verify: async () => {},
    },
    pendingCreates,
  };
}

async function untilCreates(gated, count) {
  for (let i = 0; i < 10_000; i += 1) {
    if (gated.pendingCreates.length >= count) return;
    await new Promise((resolve) => setImmediate(resolve));
  }
  throw new Error(`the lock never made create call ${count} the test waits to release`);
}

function engageRig() {
  const recorded = new RecordedStorage();
  const time = clock();
  const sessions = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded), now: time.now });
  sessions.adopt({ token: FAKE_JWT, tokenExpiresAtMs: time.now() + 3_600_000, user: { ...FAKE_ACCOUNT } });
  const gated = gatedCreateProvider();
  const lock = new DeviceLock({
    credentials: gated.provider,
    sessions,
    mirror: new ThrowSafeMirror(LOCK_MIRROR_KEY, recorded),
    now: time.now,
  });
  return { gated, lock, recorded, sessions, time };
}

test("of two concurrent engages only the first completion lands, and the mirror holds one entry", async () => {
  // Both passes of the entry checks succeed when nothing is engaged yet, so
  // two engages can be in flight at once: the generation guards are what
  // make the second one adopt nothing.
  const { gated, lock, recorded } = engageRig();

  const first = lock.engage();
  const second = lock.engage();
  await untilCreates(gated, 2);
  gated.pendingCreates[0].resolve("fake-credential-id-1"); // the first engage completes...
  gated.pendingCreates[1].resolve("fake-credential-id-2"); // ...then the second

  const firstResult = await first;
  assert.equal(firstResult.ok, true, "the first engage engaged");
  const secondResult = await second;
  assert.equal(secondResult.ok, false, "the second engage resolved an error Result");
  assert.match(secondResult.error, /superseded/);
  assert.equal(lock.state, "locked", "exactly one lock is engaged");

  const lockWrites = recorded.writes.filter((w) => w.key === LOCK_MIRROR_KEY);
  assert.equal(lockWrites.length, 1, "the mirror was written by one engage, not two");
  const payload = JSON.parse(lockWrites[0].value);
  assert.equal(payload.credentialId, "fake-credential-id-1", "the mirror holds the first engage's credential");
});

test("a disengage during an engage in flight cancels the engage, and the mirror is written by neither path", async () => {
  const { gated, lock, recorded } = engageRig();

  const engaging = lock.engage();
  await untilCreates(gated, 1);
  lock.disengage(); // the lock is removed while the credential creation is in flight
  gated.pendingCreates[0].resolve("fake-credential-id-1"); // the platform answers late

  const result = await engaging;
  assert.equal(result.ok, false, "the superseded engage resolves an error Result");
  assert.match(result.error, /superseded/);
  assert.equal(lock.state, "absent", "the late completion engaged nothing");
  assert.equal(
    new ThrowSafeMirror(LOCK_MIRROR_KEY, recorded).read(),
    undefined,
    "the mirror holds nothing: the superseded engage wrote no entry and the disengage cleared what was there",
  );
});

// A second identity for the shared-tablet races: person B signs in while
// person A's platform ceremony is still open.
const OTHER_ACCOUNT = {
  userId: "fake-user-2",
  email: "other.user@example.invalid",
  name: "Other User",
};

function otherUserAdopt(sessions) {
  sessions.clear(); // person A signs out while the platform ceremony is open
  sessions.adopt({
    token: FAKE_JWT,
    tokenExpiresAtMs: START_MS + 3_600_000,
    user: { ...OTHER_ACCOUNT },
  }); // person B signs in during it
}

test("a sign-out and another person's sign-in during the credential prompt refuse the engage: the lock is never stored for the person who left", async () => {
  // The recorded gap this closes: engage bound the user it read BEFORE its
  // awaits, so an A sign-out and a B sign-in during the platform's prompt
  // left the lock stored for A while B was signed in, and B's unlock was
  // refused. The custody is now read again after each await.
  const { gated, lock, recorded, sessions } = engageRig();

  const engaging = lock.engage(); // person A's engage, parked at the platform's create
  await untilCreates(gated, 1);
  otherUserAdopt(sessions);
  gated.pendingCreates[0].resolve("fake-credential-id-1"); // the platform answers late

  const result = await engaging;
  assert.equal(result.ok, false, "the engage is refused: the session it saw is no longer the one held");
  assert.match(result.error, /the session changed while engage was in flight/);
  assert.equal(lock.state, "absent", "nothing was engaged for A while B is signed in");
  assert.equal(
    new ThrowSafeMirror(LOCK_MIRROR_KEY, recorded).read(),
    undefined,
    "the mirror holds nothing: the lock was never stored for the user who signed out",
  );

  // Person B's own engagement works, and binds to B.
  const engagingB = lock.engage();
  await untilCreates(gated, 2);
  gated.pendingCreates[1].resolve("fake-credential-id-2");
  assert.equal((await engagingB).ok, true, "person B engages their own lock");
  const payload = JSON.parse(recorded.getItem(LOCK_MIRROR_KEY));
  assert.equal(payload.userId, OTHER_ACCOUNT.userId, "the lock binds to the user actually signed in");
  assert.equal(lock.state, "locked");
});

test("a sign-out and another person's sign-in during the prompt refuse A's unlock, and B's later unlock works", async () => {
  const { credentials, lock, sessions } = await deferredRig();
  assert.equal((await lock.engage()).ok, true); // person A engages

  const opening = lock.unlock(); // A's unlock, parked at the platform prompt
  const verify = await untilPending(credentials, 1);
  otherUserAdopt(sessions); // A signs out, B signs in during the prompt
  verify.complete(undefined); // the platform answers A's prompt with a success

  const result = await opening;
  assert.equal(result.ok, false, "A's unlock is refused: the session changed while the prompt was open");
  assert.match(result.error, /the session changed while the platform prompt was in flight/);
  assert.equal(lock.state, "locked", "the late success opened nothing for the person who is no longer signed in");

  // B's later unlock works: B disengages A's lock and engages their own.
  lock.disengage();
  assert.equal((await lock.engage()).ok, true, "B engages the lock for their own session");
  const openingB = lock.unlock();
  const verifyB = await untilPending(credentials, 2);
  verifyB.complete(undefined);
  assert.equal((await openingB).ok, true, "B's own unlock works");
  assert.equal(lock.state, "open");
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

// The re-checks after each await refuse a DIFFERENT user only: the bound
// user's own token moving underneath (a refresh mid prompt, a token that
// expires mid prompt) is not a change of person. Each test below parks the
// platform at the await the re-check follows, moves only the bound user's
// own token, and expects the platform's answer to land.
test("the engaged user's token expiring during the credential create does not refuse the engage", async () => {
  // The re-check after create once compared with the session captured at the
  // start, so the engaged user's own token expiring mid prompt (the custody
  // then holds no session at all) refused the engage and the person had to
  // start over. Nobody else signed in: the engage lands.
  const { gated, lock, recorded, sessions, time } = engageRig();

  const engaging = lock.engage(); // person A's engage, parked at the platform's create
  await untilCreates(gated, 1);
  time.advance(3_600_001); // the captured token expires while create is in flight
  assert.equal(sessions.current, null, "the custody holds no live session while create is in flight");
  gated.pendingCreates[0].resolve("fake-credential-id-1"); // the platform answers

  const result = await engaging;
  assert.equal(result.ok, true, `nobody else signed in, so the engage lands (${result.ok ? "" : result.error})`);
  assert.equal(lock.state, "locked");
  const payload = JSON.parse(recorded.getItem(LOCK_MIRROR_KEY));
  assert.equal(payload.userId, FAKE_ACCOUNT.userId, "the lock still binds to the user who engaged it");
});

test("the engaged user's token expiring during the prompt does not refuse the unlock", async () => {
  const { credentials, lock, sessions, time } = await deferredRig();
  assert.equal((await lock.engage()).ok, true);

  const opening = lock.unlock(); // parked at the platform prompt
  const verify = await untilPending(credentials, 1);
  time.advance(3_600_001); // the token expires while the prompt is open
  assert.equal(sessions.current, null, "the custody holds no live session while the prompt is open");
  verify.complete(undefined); // the platform answers with a success

  const result = await opening;
  assert.equal(result.ok, true, `nobody else signed in, so the prompt's answer opens (${result.ok ? "" : result.error})`);
  assert.equal(lock.state, "open");
});

test("an unlock begun with no live session accepts the engaged user's own refresh landing during the prompt", async () => {
  // The recorded case: the token expired while the lock was engaged, so the
  // unlock begins with no session to compare, and the engaged user's own
  // refresh lands mid prompt. The re-check once compared with that null
  // capture, so a live session for the SAME user refused. It refuses a
  // different user only.
  const { credentials, lock, sessions, time } = await deferredRig();
  assert.equal((await lock.engage()).ok, true);
  time.advance(3_600_001); // the token expired: the lock stays engaged, the custody holds no live session
  assert.equal(sessions.current, null);

  const opening = lock.unlock(); // begun with no live session
  const verify = await untilPending(credentials, 1);
  sessions.adopt({
    token: FAKE_JWT,
    tokenExpiresAtMs: time.now() + 3_600_000,
    user: { ...FAKE_ACCOUNT },
  }); // the engaged user's own refresh, mid prompt
  verify.complete(undefined);

  const result = await opening;
  assert.equal(result.ok, true, `the session is the engaged user's own (${result.ok ? "" : result.error})`);
  assert.equal(lock.state, "open");
});

test("a failed prompt with another user's session landed records no backoff: the failure path carries the session re-check", async () => {
  // The success path and the failure path carry the same rule: a failure
  // that lands after a sign-out and another person's sign-in records no
  // backoff, so the rule has no exception.
  const { credentials, lock, sessions } = await deferredRig();
  assert.equal((await lock.engage()).ok, true);

  const opening = lock.unlock(); // person A's unlock, parked at the platform prompt
  const verify = await untilPending(credentials, 1);
  otherUserAdopt(sessions); // A signs out, B signs in while the prompt is open
  verify.fail(new Error("fake: user verification failed")); // and the prompt itself fails

  const result = await opening;
  assert.equal(result.ok, false);
  assert.match(result.error, /the session changed while the platform prompt was in flight/, "the refusal names the session change");
  assert.doesNotMatch(result.error, /failed attempts/, "no failure was recorded against the person no longer signed in");

  // The backoff was never armed: with A's own session back, the next attempt
  // reaches the platform at once.
  sessions.adopt({ token: FAKE_JWT, tokenExpiresAtMs: START_MS + 3_600_000, user: { ...FAKE_ACCOUNT } });
  const again = lock.unlock();
  const verify2 = await untilPending(credentials, 2);
  verify2.complete(undefined);
  assert.equal((await again).ok, true, "the next attempt was not rate limited: the refused failure recorded nothing");
  assert.equal(lock.state, "open");
});

// A credential provider whose isAvailable is parked per call, so a session
// switch can land while engage is still waiting on the platform's
// availability answer: the first re-check's case, before create is asked.
function gatedIsAvailableProvider() {
  const pendingAvailability = [];
  let createCount = 0;
  return {
    provider: {
      isAvailable: () => new Promise((resolve) => pendingAvailability.push({ resolve })),
      create: async () => {
        createCount += 1;
        return `fake-credential-id-${createCount}`;
      },
      verify: async () => {},
    },
    pendingAvailability,
    get createCount() {
      return createCount;
    },
  };
}

test("a session switch during isAvailable asks the platform for no credential: engage's first re-check refuses before create", async () => {
  // Removing the first re-check alone once left every test green (the second
  // re-check still refuses the engage), but the platform was then asked to
  // register a credential for the person who is no longer signed in. This
  // pins the first re-check where only it is under test: create is never
  // asked.
  const recorded = new RecordedStorage();
  const sessions = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded), now: () => START_MS });
  sessions.adopt({ token: FAKE_JWT, tokenExpiresAtMs: START_MS + 3_600_000, user: { ...FAKE_ACCOUNT } });
  const gated = gatedIsAvailableProvider();
  const lock = new DeviceLock({
    credentials: gated.provider,
    sessions,
    mirror: new ThrowSafeMirror(LOCK_MIRROR_KEY, recorded),
    now: () => START_MS,
  });

  const engaging = lock.engage(); // parked at isAvailable
  for (let i = 0; i < PENDING_LIMIT && gated.pendingAvailability.length < 1; i += 1) {
    await new Promise((resolve) => setImmediate(resolve));
  }
  assert.equal(gated.pendingAvailability.length, 1, "engage asked the platform whether a credential is available");
  otherUserAdopt(sessions); // A signs out, B signs in while the availability ask is open
  gated.pendingAvailability[0].resolve(true);

  const result = await engaging;
  assert.equal(result.ok, false);
  assert.match(result.error, /the session changed while engage was in flight/);
  assert.equal(gated.createCount, 0, "the platform was never asked to create a credential for the person who left");
  assert.equal(lock.state, "absent");
  assert.equal(new ThrowSafeMirror(LOCK_MIRROR_KEY, recorded).read(), undefined, "nothing was mirrored");
});
