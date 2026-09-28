// The device unlock flow with the fake credential provider (the acceptance
// line names it): engage, unlock, the wrong-PIN path, the local exponential
// rate limiter, the mirror discipline across a reload, and the audit headers
// present exactly while a lock is open.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  AUDIT_AT_HEADER,
  AUDIT_USER_HEADER,
  DeviceLock,
  ThrowSafeMirror,
  LOCK_MIRROR_KEY,
} from "../src/index.ts";
import {
  FAKE_ACCOUNT,
  FAKE_PIN,
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

async function rig() {
  const recorded = new RecordedStorage();
  const time = clock();
  const credentials = new FakeCredentialProvider();
  const lock = new DeviceLock({
    credentials,
    mirror: new ThrowSafeMirror(LOCK_MIRROR_KEY, recorded),
    now: time.now,
  });
  return { recorded, credentials, lock, time };
}

test("engage and unlock with the fake credential provider", async () => {
  const { credentials, lock, time } = await rig();
  assert.equal(lock.state, "absent");
  assert.deepEqual(lock.auditHeaders(), {}, "no audit headers with no lock");

  const engaged = await lock.engage(FAKE_ACCOUNT.userId);
  assert.equal(engaged.ok, true, `engage resolves a Result: ${engaged.ok ? "" : engaged.error}`);
  assert.equal(credentials.creationCount, 1, "the platform credential was registered once");
  assert.equal(lock.state, "locked");

  credentials.present(FAKE_PIN);
  const opened = await lock.unlock();
  assert.equal(opened.ok, true, `unlock resolves a Result: ${opened.ok ? "" : opened.error}`);
  assert.deepEqual(opened.value, { userId: FAKE_ACCOUNT.userId, unlockedAtMs: time.time() });
  assert.equal(lock.state, "open");
  assert.deepEqual(lock.openUnlock, { userId: FAKE_ACCOUNT.userId, unlockedAtMs: time.time() });
});

test("the audit headers are present exactly while a lock is open", async () => {
  const { credentials, lock, time } = await rig();
  assert.deepEqual(lock.auditHeaders(), {}, "absent: no audit headers");
  await lock.engage(FAKE_ACCOUNT.userId);
  assert.deepEqual(lock.auditHeaders(), {}, "locked: no audit headers");
  credentials.present(FAKE_PIN);
  await lock.unlock();
  assert.deepEqual(
    lock.auditHeaders(),
    {
      [AUDIT_USER_HEADER]: FAKE_ACCOUNT.userId,
      [AUDIT_AT_HEADER]: String(time.time()),
    },
    "open: exactly the audit pair, exactly who and when",
  );
  lock.lock();
  assert.equal(lock.state, "locked");
  assert.deepEqual(lock.auditHeaders(), {}, "re-locked: the audit pair is gone");
});

test("a wrong PIN is refused and feeds the exponential backoff, capped, reset by success", async () => {
  const { credentials, lock, time } = await rig();
  await lock.engage(FAKE_ACCOUNT.userId);

  credentials.present(WRONG_PIN);
  const first = await lock.unlock();
  assert.equal(first.ok, false);
  assert.match(first.error, /device unlock failed/);
  assert.match(first.error, /failed attempts: 1/);
  assert.equal(lock.state, "locked", "a failed attempt does not open the lock");
  assert.deepEqual(lock.auditHeaders(), {}, "a failed attempt stamps no audit header");

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
  const { credentials, lock } = await rig();
  await lock.engage(FAKE_ACCOUNT.userId);
  credentials.present(null);
  const cancelled = await lock.unlock();
  assert.equal(cancelled.ok, false);
  assert.match(cancelled.error, /cancelled/);
  const tooSoon = await lock.unlock();
  assert.match(tooSoon.error, /rate limited/, "the backoff applies after a cancelled prompt too");
});

test("unlock and engage are refused outside an engaged lock", async () => {
  const { lock } = await rig();
  const unlocked = await lock.unlock();
  assert.equal(unlocked.ok, false);
  assert.match(unlocked.error, /no device lock is engaged/);

  const noUser = await lock.engage("");
  assert.equal(noUser.ok, false);
  assert.match(noUser.error, /user id/);
});

test("engage is refused when the platform offers no credential (WebAuthn absent)", async () => {
  const recorded = new RecordedStorage();
  const lock = new DeviceLock({
    credentials: new FakeCredentialProvider({ available: false }),
    mirror: new ThrowSafeMirror(LOCK_MIRROR_KEY, recorded),
  });
  const engaged = await lock.engage(FAKE_ACCOUNT.userId);
  assert.equal(engaged.ok, false);
  assert.match(engaged.error, /unavailable/);
  assert.equal(lock.state, "absent");
  assert.equal(recorded.writes.some((w) => w.key === LOCK_MIRROR_KEY), false, "nothing was mirrored");
});

test("a second engage without disengage is refused", async () => {
  const { lock } = await rig();
  assert.equal((await lock.engage(FAKE_ACCOUNT.userId)).ok, true);
  const second = await lock.engage(FAKE_ACCOUNT.userId);
  assert.equal(second.ok, false);
  assert.match(second.error, /already engaged/);
});

test("the engaged lock survives a reload through the mirror; the open unlock does not", async () => {
  const recorded = new RecordedStorage();
  const time = clock();
  const credentials = new FakeCredentialProvider(); // the platform persists across a reload
  const before = new DeviceLock({
    credentials,
    mirror: new ThrowSafeMirror(LOCK_MIRROR_KEY, recorded),
    now: time.now,
  });
  await before.engage(FAKE_ACCOUNT.userId);
  credentials.present(FAKE_PIN);
  await before.unlock();
  assert.equal(before.state, "open");

  const after = new DeviceLock({
    credentials,
    mirror: new ThrowSafeMirror(LOCK_MIRROR_KEY, recorded),
    now: time.now,
  });
  assert.equal(after.state, "locked", "a reload restores the engaged lock");
  assert.equal(after.openUnlock, null, "a reload re-locks: the open unlock does not survive");
  assert.deepEqual(after.auditHeaders(), {}, "a re-locked device stamps no audit header");

  credentials.present(FAKE_PIN);
  const reopened = await after.unlock();
  assert.equal(reopened.ok, true, "the restored credential id is the one the verification targets");
  assert.equal(reopened.value.userId, FAKE_ACCOUNT.userId, "the restored lock still protects the same user");
});

test("a degraded mirror forgets the lock across a reload, observably", async () => {
  const throwing = () => new ThrowSafeMirror(LOCK_MIRROR_KEY, new ThrowingStorage());
  const before = new DeviceLock({ credentials: new FakeCredentialProvider(), mirror: throwing() });
  assert.equal(before.degraded, true);
  assert.equal((await before.engage(FAKE_ACCOUNT.userId)).ok, true, "the lock works in memory in a degraded browser");
  assert.equal(before.state, "locked");

  const after = new DeviceLock({ credentials: new FakeCredentialProvider(), mirror: throwing() });
  assert.equal(after.degraded, true, "the degradation is observable");
  assert.equal(after.state, "absent", "a degraded reload restores no lock");
});

test("the lock mirror payload carries no token, no PIN, no secret", async () => {
  const { recorded, lock } = await rig();
  await lock.engage(FAKE_ACCOUNT.userId);

  const lockWrites = recorded.writes.filter((w) => w.key === LOCK_MIRROR_KEY);
  assert.equal(lockWrites.length, 1);
  const payload = JSON.parse(lockWrites[0].value);
  assert.deepEqual(
    Object.keys(payload).sort(),
    ["credentialId", "engagedAtMs", "userId", "v"],
    "the lock mirror carries who, which credential, when: nothing else",
  );
  for (const write of recorded.writes) {
    assert.equal(write.value.includes(FAKE_PIN), false, "no lock-path write carries the PIN: the package never holds one");
  }
});

test("disengage removes the lock from memory and mirror", async () => {
  const { recorded, lock } = await rig();
  await lock.engage(FAKE_ACCOUNT.userId);
  assert.equal(lock.state, "locked");
  lock.disengage();
  assert.equal(lock.state, "absent");
  assert.equal(recorded.size, 0, "the mirror was cleared");

  const after = new DeviceLock({ credentials: new FakeCredentialProvider(), mirror: new ThrowSafeMirror(LOCK_MIRROR_KEY, recorded) });
  assert.equal(after.state, "absent", "a reload after disengage restores nothing");
});
