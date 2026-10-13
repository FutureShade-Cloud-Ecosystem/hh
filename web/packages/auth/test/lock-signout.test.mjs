// Colton's ruling C on the lock at sign-out (relay message 048), in one
// sentence as staff are told it: the lock is yours while you are signed in;
// a lock belongs to the signed-in person, or to nobody. The nine tests:
// a lock then a sign-out; a lock, a sign-out, another user signs in; a lock,
// another user signs in over a live session; a lock, the same user signs in
// again; a sign-out during the engage prompt; a sign-out during the unlock
// prompt; the token refreshed during the prompt; the token expired during
// the prompt; a stale lock at start-up. Each states what is in storage
// afterwards and what the person sees, and each is proven able to fail by
// its own mutation.
//
// How the code tells a sign-out apart from the engaged user's own token
// moving underneath: a sign-out is an ACT, counted by the sign-out counter
// (SignOutCounter, shared between the controller and the lock), and an
// expiry is not. signOut() bumps that counter on both sides of its await
// and disengages the lock beside the custody it clears; an engage or an
// unlock captures the count before it awaits the platform and refuses when
// it has moved. A token that refreshes or expires moves no counter, and the
// prompt's answer still lands.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  AuthController,
  DeviceLock,
  SessionStore,
  SignOutCounter,
  ThrowSafeMirror,
  LOCK_MIRROR_KEY,
  SESSION_MIRROR_KEY,
} from "../src/index.ts";
import {
  FAKE_ACCOUNT,
  FAKE_JWT,
  fakeExchangeAnswer,
  FAKE_PASSWORD,
  DeferredAccountSurface,
  DeferredCall,
  FakeCredentialProvider,
  RecordedStorage,
} from "../src/testing.ts";

const START_MS = 1_000_000_000_000;

// A second identity for the shared-tablet rulings: person B signs in while
// person A's lock is engaged.
const OTHER_ACCOUNT = {
  userId: "fake-user-2",
  email: "other.user@example.invalid",
  name: "Other User",
};

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

// A credential provider whose create and verify are both held open per
// call, so an engage or an unlock can be parked mid ceremony while a
// sign-out lands, and answered in the window or after it. All values fake.
function parkedCredentialProvider() {
  const pendingCreates = [];
  const pendingVerifies = [];
  let created = 0;
  return {
    provider: {
      isAvailable: async () => true,
      create: () => {
        created += 1;
        return new Promise((resolve) => pendingCreates.push({ id: `fake-credential-id-${created}`, resolve }));
      },
      verify: () => {
        const call = new DeferredCall();
        pendingVerifies.push(call);
        return call.promise;
      },
    },
    pendingCreates,
    pendingVerifies,
  };
}

const PENDING_LIMIT = 10_000;

async function untilCreate(parked, count) {
  for (let i = 0; i < PENDING_LIMIT; i += 1) {
    if (parked.pendingCreates.length >= count) return parked.pendingCreates[count - 1];
    await new Promise((resolve) => setImmediate(resolve));
  }
  throw new Error(`the lock never made create call ${count} the test waits to release`);
}

async function untilVerify(parked, count) {
  for (let i = 0; i < PENDING_LIMIT; i += 1) {
    if (parked.pendingVerifies.length >= count) return parked.pendingVerifies[count - 1];
    await new Promise((resolve) => setImmediate(resolve));
  }
  throw new Error(`the lock never made verify call ${count} the test waits to complete`);
}

async function untilHandoffAt(account, name, index) {
  for (let i = 0; i < PENDING_LIMIT; i += 1) {
    const calls = account.handoffs(name);
    if (calls.length > index) return calls[index];
    await new Promise((resolve) => setImmediate(resolve));
  }
  throw new Error(`the controller never made the ${name} call at ${index} the test waits to complete`);
}

// The wired rig: the shell's true wiring, one custody, one lock, one
// controller holding both the lock handle and the shared sign-out counter,
// all over one recorded storage. The account surface is deferred so a test
// can hold the platform's answers, and the sign-out's own deleteSession can
// be parked too, which is how a prompt is made to answer in the window
// between the sign-out's first bump and its clear.
function wiredRig({ parked = false, deferDeleteSession = false } = {}) {
  const recorded = new RecordedStorage();
  const time = clock();
  const sessions = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded), now: time.now });
  const signOuts = new SignOutCounter();
  const credentials = parked ? parkedCredentialProvider() : { provider: new FakeCredentialProvider() };
  const lock = new DeviceLock({
    credentials: credentials.provider,
    sessions,
    mirror: new ThrowSafeMirror(LOCK_MIRROR_KEY, recorded),
    now: time.now,
    signOuts,
    openWindowMs: 3_600_000,
  });
  const account = new DeferredAccountSurface({ deferDeleteSession });
  const controller = new AuthController({ activeOrg: () => "yard-ten", account, sessions, lock, signOuts });
  const seedSignedIn = (user = FAKE_ACCOUNT) =>
    sessions.adopt({ token: FAKE_JWT, tokenExpiresAtMs: time.now() + 3_600_000, user: { ...user } });
  return { recorded, time, sessions, signOuts, credentials, lock, account, controller, seedSignedIn };
}

// One person's explicit sign-in through the controller, against the
// deferred surface: the credential answer and the mint are completed by the
// test, exactly as a slow platform answers. One sign-in per test, so the
// handoff indexes are the first of each.
async function signInThroughController(rig, user, mintName) {
  const signing = rig.controller.signInWithPassword(user.email, FAKE_PASSWORD);
  (await untilHandoffAt(rig.account, "createEmailPasswordSession", 0)).complete({ ...user });
  const mint = await untilHandoffAt(rig.account, "exchange", 0);
  mint.complete(fakeExchangeAnswer(`${FAKE_JWT}-${mintName}`, rig.time.now() + 900_000, {}, user));
  const result = await signing;
  assert.equal(result.ok, true, `the sign-in of ${user.userId} resolves a Result: ${result.ok ? "" : result.error}`);
  return result;
}

test("a lock then a sign-out: no lock in storage afterwards, and the person sees the signed-out state", async () => {
  const rig = wiredRig();
  assert.equal(rig.seedSignedIn(), true, "person A is signed in");
  assert.equal((await rig.lock.engage()).ok, true);
  assert.equal(rig.lock.state, "locked", "A's lock is engaged");
  assert.notEqual(rig.recorded.getItem(LOCK_MIRROR_KEY), null, "the engaged lock is in storage before the sign-out");

  await rig.controller.signOut();

  // What is in storage afterwards: nothing at all. The lock's key went with
  // the session mirror's key, under the same guards, and the sign-out's
  // platform call failed nowhere (a failing deleteSession clears either way,
  // which the controller tests hold).
  assert.equal(rig.recorded.getItem(LOCK_MIRROR_KEY), null, "no lock key remains in storage after the sign-out");
  assert.equal(rig.recorded.getItem(SESSION_MIRROR_KEY), null, "no session mirror remains after the sign-out");
  assert.equal(rig.recorded.size, 0, "storage holds nothing: the sign-out leaves no lock behind");

  // What the person sees: the signed-out state, no lock screen.
  assert.equal(rig.sessions.current, null, "custody holds nobody");
  assert.equal(rig.sessions.profile, null, "no display profile for the signed-out screen to show");
  assert.equal(rig.lock.state, "absent", "the screen shows no lock: the lock is nobody's once its person signed out");
});

test("a lock, a sign-out, then another user signs in: storage holds B's profile and no lock, and B sees no lock screen", async () => {
  const rig = wiredRig();
  assert.equal(rig.seedSignedIn(), true);
  assert.equal((await rig.lock.engage()).ok, true);
  assert.equal(rig.lock.state, "locked", "A's lock is engaged");

  await rig.controller.signOut();
  assert.equal(rig.recorded.getItem(LOCK_MIRROR_KEY), null, "A's sign-out left no lock key in storage");
  assert.equal(rig.recorded.size, 0, "storage holds nothing after A's sign-out");

  await signInThroughController(rig, OTHER_ACCOUNT, "B");

  // What is in storage afterwards: B's display profile under the session
  // key, and no lock key at all.
  assert.equal(rig.recorded.getItem(LOCK_MIRROR_KEY), null, "no lock came back with the new user");
  const mirrored = rig.recorded.getItem(SESSION_MIRROR_KEY);
  assert.notEqual(mirrored, null, "B's display profile is in storage");
  assert.equal(mirrored.includes(OTHER_ACCOUNT.userId), true, "storage holds B's profile, not A's");

  // What the person sees: B signed in, and no lock screen.
  assert.equal(rig.sessions.current.user.userId, OTHER_ACCOUNT.userId, "custody holds B's session");
  assert.equal(rig.lock.state, "absent", "B is not shown A's lock: the lock was nobody's between the two");
});

test("a lock, then another user signs in over the live session: the lock is cleared before the new session is shown", async () => {
  const rig = wiredRig();
  assert.equal(rig.seedSignedIn(), true);
  assert.equal((await rig.lock.engage()).ok, true);
  assert.equal(rig.lock.state, "locked", "A's lock is engaged over A's live session");
  const lockWritesBefore = rig.recorded.writes.filter((w) => w.key === LOCK_MIRROR_KEY).length;
  assert.equal(lockWritesBefore, 1, "the engage wrote the lock once");

  await signInThroughController(rig, OTHER_ACCOUNT, "B");

  // What is in storage afterwards: B's profile, and no lock key. The
  // adoption of a user whose id is not the lock's disengaged the lock as
  // the adoption landed, before this sign-in could show B anything.
  assert.equal(rig.recorded.getItem(LOCK_MIRROR_KEY), null, "no lock key remains: A's lock was cleared, not carried into B's session");
  assert.equal(rig.recorded.getItem(SESSION_MIRROR_KEY)?.includes(OTHER_ACCOUNT.userId), true, "storage holds B's profile");

  // What the person sees: B signed in over the live session, and no lock
  // screen.
  assert.equal(rig.sessions.current.user.userId, OTHER_ACCOUNT.userId, "custody holds B's session");
  assert.equal(rig.lock.state, "absent", "B sees no lock screen: the lock belonged to A, who is no longer signed in");
});

test("a lock, then the same user signs in again: the lock stays theirs, in memory and in storage", async () => {
  const rig = wiredRig();
  assert.equal(rig.seedSignedIn(), true);
  assert.equal((await rig.lock.engage()).ok, true);
  assert.equal(rig.lock.state, "locked");
  const engaged = JSON.parse(rig.recorded.getItem(LOCK_MIRROR_KEY));
  assert.equal(engaged.credentialId, "fake-credential-id-1", "A's lock holds A's credential");

  await signInThroughController(rig, FAKE_ACCOUNT, "EX-1"); // A, again, no sign-out in between

  // What is in storage afterwards: the SAME lock entry, untouched (one
  // write, the engage's), beside the refreshed profile of the same user.
  const lockWrites = rig.recorded.writes.filter((w) => w.key === LOCK_MIRROR_KEY);
  assert.equal(lockWrites.length, 1, "the re-sign-in wrote no lock and cleared none");
  const kept = JSON.parse(rig.recorded.getItem(LOCK_MIRROR_KEY));
  assert.deepEqual(kept, engaged, "the lock entry is untouched: the same user keeps their lock");
  assert.equal(rig.recorded.getItem(SESSION_MIRROR_KEY)?.includes(FAKE_ACCOUNT.userId), true, "storage holds A's profile");

  // What the person sees: still signed in as A, on their own still-engaged
  // lock, with the fresh token in custody.
  assert.equal(rig.sessions.current.token, `${FAKE_JWT}-EX-1`, "custody holds the fresh mint");
  assert.equal(rig.sessions.current.user.userId, FAKE_ACCOUNT.userId);
  assert.equal(rig.lock.state, "locked", "the screen still shows A's lock: their lock survived their own re-sign-in");
});

test("a sign-out during the engage prompt: the prompt answers to a signed-out person and stores nothing", async () => {
  const rig = wiredRig({ parked: true, deferDeleteSession: true });
  assert.equal(rig.seedSignedIn(), true);

  const engaging = rig.lock.engage(); // parked at the platform's create
  const create = await untilCreate(rig.credentials, 1);
  const signingOut = rig.controller.signOut(); // the sign-out's counter bumps; its own deleteSession is parked
  create.resolve(create.id); // the platform answers while the sign-out is still mid await

  const result = await engaging;
  assert.equal(result.ok, false, `the engage stores nothing for a person who signed out (${result.ok ? "it engaged" : result.error})`);
  assert.match(result.error, /signed out/, "the refusal names the sign-out: the counter moved, so this was no expiry");
  assert.equal(rig.lock.state, "absent", "nothing was engaged");
  assert.equal(rig.recorded.getItem(LOCK_MIRROR_KEY), null, "the mirror holds nothing: the engage wrote no lock");

  const deleteCall = await untilHandoffAt(rig.account, "deleteSession", 0);
  deleteCall.complete(undefined); // the sign-out completes
  await signingOut;

  // What is in storage afterwards: nothing. What the person sees: the
  // signed-out state, no lock.
  assert.equal(rig.recorded.size, 0, "storage holds nothing after the sign-out completes");
  assert.equal(rig.sessions.current, null, "custody holds nobody");
  assert.equal(rig.lock.state, "absent", "the screen shows the signed-out state with no lock");
});

test("a sign-out during the unlock prompt: the prompt answers to a signed-out person, and the screen shows the signed-out state", async () => {
  const rig = wiredRig({ parked: true, deferDeleteSession: true });
  assert.equal(rig.seedSignedIn(), true);
  const engaging = rig.lock.engage();
  (await untilCreate(rig.credentials, 1)).resolve("fake-credential-id-1");
  assert.equal((await engaging).ok, true, "A's lock is engaged");

  const opening = rig.lock.unlock(); // parked at the platform prompt
  const verify = await untilVerify(rig.credentials, 1);
  const signingOut = rig.controller.signOut(); // the counter bumps; deleteSession is parked
  verify.complete(undefined); // the prompt answers with a success while the sign-out is still mid await

  const result = await opening;
  assert.equal(result.ok, false, `the prompt's success opens nothing for a person who signed out (${result.ok ? "it opened" : result.error})`);
  assert.match(result.error, /signed out/, "the refusal names the sign-out: the counter moved, so this was no expiry");

  const deleteCall = await untilHandoffAt(rig.account, "deleteSession", 0);
  deleteCall.complete(undefined); // the sign-out completes
  await signingOut;

  // What is in storage afterwards: nothing. What the person sees: the
  // signed-out state; the lock is already gone, not merely closed.
  assert.equal(rig.recorded.size, 0, "storage holds nothing: no session mirror, no lock");
  assert.equal(rig.sessions.current, null, "custody holds nobody");
  assert.equal(rig.sessions.profile, null, "no profile for a screen to show");
  assert.equal(rig.lock.state, "absent", "the screen shows the signed-out state: the lock found itself gone");
});

test("the engaged user's own token refreshed during the prompt still unlocks: no sign-out, no counter movement", async () => {
  const rig = wiredRig({ parked: true });
  assert.equal(rig.seedSignedIn(), true);
  const engaging = rig.lock.engage();
  (await untilCreate(rig.credentials, 1)).resolve("fake-credential-id-1");
  assert.equal((await engaging).ok, true, "A's lock is engaged");

  const opening = rig.lock.unlock(); // parked at the platform prompt
  const verify = await untilVerify(rig.credentials, 1);
  assert.equal(
    rig.sessions.adopt({ token: `${FAKE_JWT}-FRESH`, tokenExpiresAtMs: rig.time.now() + 3_600_000, user: { ...FAKE_ACCOUNT } }),
    true,
    "the engaged user's own refresh lands mid prompt: a new token, the same user, no sign-out",
  );
  verify.complete(undefined); // the platform answers

  const result = await opening;
  assert.equal(result.ok, true, `the bound user's own refresh is no refusal (${result.ok ? "" : result.error})`);
  assert.equal(rig.lock.state, "open", "the person sees the unlocked screen");

  // What is in storage afterwards: the lock entry, still theirs, beside the
  // refreshed profile.
  const payload = JSON.parse(rig.recorded.getItem(LOCK_MIRROR_KEY));
  assert.equal(payload.userId, FAKE_ACCOUNT.userId, "the lock still belongs to the user who engaged it");
  assert.equal(payload.credentialId, "fake-credential-id-1", "the same credential unlocks them");
  assert.equal(rig.recorded.getItem(SESSION_MIRROR_KEY)?.includes(FAKE_ACCOUNT.userId), true, "storage holds the refreshed profile");
});

test("the engaged user's own token expiring during the prompt still unlocks: an expiry moves no counter", async () => {
  const rig = wiredRig({ parked: true });
  assert.equal(rig.seedSignedIn(), true);
  const engaging = rig.lock.engage();
  (await untilCreate(rig.credentials, 1)).resolve("fake-credential-id-1");
  assert.equal((await engaging).ok, true, "A's lock is engaged");

  const opening = rig.lock.unlock(); // parked at the platform prompt
  const verify = await untilVerify(rig.credentials, 1);
  rig.time.advance(3_600_001); // the token expires while the prompt is open; nobody signed out
  assert.equal(rig.sessions.current, null, "the custody holds no live session while the prompt is open");
  verify.complete(undefined); // the platform answers

  const result = await opening;
  assert.equal(result.ok, true, `an expiry is not an act: the prompt's answer lands (${result.ok ? "" : result.error})`);
  assert.equal(rig.lock.state, "open", "the person sees the unlocked screen");

  // What is in storage afterwards: the lock entry and the display profile
  // (an expiry clears neither), with no live token in memory.
  const payload = JSON.parse(rig.recorded.getItem(LOCK_MIRROR_KEY));
  assert.equal(payload.userId, FAKE_ACCOUNT.userId, "the lock still belongs to the user who engaged it");
  assert.notEqual(rig.recorded.getItem(SESSION_MIRROR_KEY), null, "the display profile survives an expiry");
  assert.equal(rig.sessions.current, null, "no live session: the token is expired");
});

test("a stale lock at start-up is cleared, not shown: a reopened tab meets the signed-out state", async () => {
  // The tab was closed with the lock engaged, and the session ended before
  // it reopened (a sign-out wipes the display profile; a reload keeps it).
  // Storage holds a lock payload and no session payload.
  const recorded = new RecordedStorage();
  recorded.setItem(
    LOCK_MIRROR_KEY,
    JSON.stringify({ v: 1, userId: FAKE_ACCOUNT.userId, credentialId: "fake-credential-left", engagedAtMs: START_MS }),
  );
  assert.equal(recorded.getItem(SESSION_MIRROR_KEY), null, "the session mirror is gone: the session ended");

  const time = clock();
  const sessions = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded), now: time.now });
  const lock = new DeviceLock({
    credentials: new FakeCredentialProvider(),
    sessions,
    mirror: new ThrowSafeMirror(LOCK_MIRROR_KEY, recorded),
    now: time.now,
    signOuts: new SignOutCounter(),
  });

  // What is in storage afterwards: the lock payload was cleared from
  // storage, not merely hidden from view.
  assert.equal(lock.state, "absent", "the lock is not shown: it belongs to nobody");
  assert.equal(recorded.getItem(LOCK_MIRROR_KEY), null, "the stale lock was cleared from storage");

  // What the person sees: the signed-out state, no lock screen.
  assert.equal(sessions.current, null, "custody holds nobody");
  assert.equal(sessions.profile, null, "no display profile survived the sign-out");
});

// A failed resume clears the lock beside the custody it clears (relay
// message 052, P3: a lock belongs to the signed-in person, or to nobody).
test("a failed resume clears the custody and the lock with it: the lock never outlives the signed-in state it belongs to", async () => {
  // The second-tab case the review found: this tab holds its session and
  // its lock, the first tab signs out, and this tab's resume then FAILS
  // (the platform holds no session for it any more). The failure cleared
  // the custody but kept the lock, and the lock then opened with nobody
  // signed in. The lock goes with the custody it belongs to.
  const rig = wiredRig();
  assert.equal(rig.seedSignedIn(), true);
  assert.equal((await rig.lock.engage()).ok, true);
  assert.equal(rig.lock.state, "locked", "A's lock is engaged, and A is signed in");
  assert.notEqual(rig.recorded.getItem(LOCK_MIRROR_KEY), null, "the engaged lock is in storage before the resume");

  const resuming = rig.controller.resume(); // this tab's re-sign-in, in flight
  const getSession = await untilHandoffAt(rig.account, "getSession", 0);
  getSession.fail(new Error("platform: session gone")); // the platform holds no session for this tab any more
  const refused = await resuming;
  assert.equal(refused.ok, false, "the resume is refused as data");
  assert.match(refused.error, /no platform session to resume/);

  assert.equal(rig.sessions.current, null, "the failed resume cleared the custody");
  assert.equal(rig.sessions.profile, null, "and the display profile with it");
  assert.equal(rig.lock.state, "absent", "the lock went with the signed-in state: it belongs to nobody now");
  assert.equal(rig.recorded.getItem(LOCK_MIRROR_KEY), null, "no lock key remains in storage");
  assert.equal(rig.recorded.getItem(SESSION_MIRROR_KEY), null, "no session key remains in storage");
  assert.equal(rig.recorded.size, 0, "storage holds nothing after the failed resume");
});

// The four guards the review found untested, one test each: the adoption
// reconciliation in the controller's adopt(), the one in resume(), unlock's
// failure-path sign-out check, and the start-up check's second arm (a lock
// whose profile names somebody else). Each fails when its guard is removed.
test("an out-of-band adoption of another user disengages the lock before the new session is shown", async () => {
  // The controller's adopt() reconciles the lock with the user it adopted
  // (the Google callback through the Console bridge adopts out of band, no
  // sign-out in between): a lock bound to a different user goes, before B's
  // session is shown.
  const rig = wiredRig();
  assert.equal(rig.seedSignedIn(), true);
  assert.equal((await rig.lock.engage()).ok, true);
  assert.equal(rig.lock.state, "locked", "A's lock is engaged over A's live session");

  assert.equal(
    rig.controller.adopt({ token: `${FAKE_JWT}-B`, tokenExpiresAtMs: rig.time.now() + 3_600_000, user: { ...OTHER_ACCOUNT } }),
    true,
    "B's session was adopted out of band",
  );

  assert.equal(rig.sessions.current.user.userId, OTHER_ACCOUNT.userId, "custody holds B's session");
  assert.equal(rig.lock.state, "absent", "B is not shown A's lock: the adoption disengaged it");
  assert.equal(rig.recorded.getItem(LOCK_MIRROR_KEY), null, "no lock key remains in storage");
  assert.equal(rig.recorded.getItem(SESSION_MIRROR_KEY)?.includes(OTHER_ACCOUNT.userId), true, "storage holds B's profile");
});

test("a resume that answers for another user disengages the lock as it adopts", async () => {
  // The controller's resume() reconciles the lock with the user the issuer's
  // cookie answered for: a reload's re-sign-in landing on a different person
  // than the one whose lock is engaged clears the lock before the new
  // session is shown.
  const rig = wiredRig();
  assert.equal(rig.seedSignedIn(), true);
  assert.equal((await rig.lock.engage()).ok, true);
  assert.equal(rig.lock.state, "locked", "A's lock is engaged");

  const resuming = rig.controller.resume(); // the reload's re-sign-in, in flight
  const getSession = await untilHandoffAt(rig.account, "getSession", 0);
  getSession.complete({ ...OTHER_ACCOUNT }); // the issuer's cookie answers for B
  const mint = await untilHandoffAt(rig.account, "exchange", 0);
  mint.complete(fakeExchangeAnswer(`${FAKE_JWT}-B`, rig.time.now() + 900_000, {}, OTHER_ACCOUNT));
  const result = await resuming;
  assert.equal(result.ok, true, `the resume landed (${result.ok ? "" : result.error})`);

  assert.equal(rig.sessions.current.user.userId, OTHER_ACCOUNT.userId, "custody holds B's resumed session");
  assert.equal(rig.lock.state, "absent", "the resume's adoption disengaged A's lock");
  assert.equal(rig.recorded.getItem(LOCK_MIRROR_KEY), null, "no lock key remains in storage");
  assert.equal(rig.recorded.getItem(SESSION_MIRROR_KEY)?.includes(OTHER_ACCOUNT.userId), true, "storage holds B's profile");
});

test("a failed unlock whose person signed out mid prompt records no backoff and names the sign-out", async () => {
  // Unlock's failure path carries the sign-out counter check as well as the
  // success path does, so the rule has no exception: a prompt that FAILS
  // after its person signed out records no backoff and tells the screen it
  // was the sign-out, not a supersession and not a wrong PIN against
  // nobody.
  const rig = wiredRig({ parked: true, deferDeleteSession: true });
  assert.equal(rig.seedSignedIn(), true);
  const engaging = rig.lock.engage();
  (await untilCreate(rig.credentials, 1)).resolve("fake-credential-id-1");
  assert.equal((await engaging).ok, true, "A's lock is engaged");

  const opening = rig.lock.unlock(); // parked at the platform prompt
  const verify = await untilVerify(rig.credentials, 1);
  const signingOut = rig.controller.signOut(); // the counter bumps; the sign-out's own deleteSession is parked
  verify.fail(new Error("fake: user verification failed (simulated wrong PIN)")); // the prompt FAILS after the sign-out landed

  const result = await opening;
  assert.equal(result.ok, false, "the failed attempt resolves an error Result");
  assert.match(
    result.error,
    /signed out while the platform prompt was in flight, so nothing was recorded/,
    `the failure names the sign-out: this is the failure path's own check (${result.ok ? "" : result.error})`,
  );

  const deleteCall = await untilHandoffAt(rig.account, "deleteSession", 0);
  deleteCall.complete(undefined); // the sign-out completes
  await signingOut;

  assert.equal(rig.recorded.size, 0, "storage holds nothing: no lock, no session mirror");
  assert.equal(rig.lock.state, "absent", "the screen shows the signed-out state with no lock");
});

test("a start-up lock whose display profile names somebody else is cleared, not shown", async () => {
  // The start-up check's second arm: a lock in the mirror bound to A beside
  // a display profile for B. A payload combination only an older version or
  // a hand edit produces (a sign-out clears both, and a different user's
  // sign-in disengages the lock before writing their profile), but the
  // start-up check does not argue with storage: the lock belongs to the
  // signed-in person, or to nobody, and B is the one signed in.
  const recorded = new RecordedStorage();
  recorded.setItem(
    SESSION_MIRROR_KEY,
    JSON.stringify({ v: 2, userId: OTHER_ACCOUNT.userId, email: OTHER_ACCOUNT.email, name: OTHER_ACCOUNT.name }),
  );
  recorded.setItem(
    LOCK_MIRROR_KEY,
    JSON.stringify({ v: 1, userId: FAKE_ACCOUNT.userId, credentialId: "fake-credential-left", engagedAtMs: START_MS }),
  );

  const time = clock();
  const sessions = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded), now: time.now });
  const lock = new DeviceLock({
    credentials: new FakeCredentialProvider(),
    sessions,
    mirror: new ThrowSafeMirror(LOCK_MIRROR_KEY, recorded),
    now: time.now,
    signOuts: new SignOutCounter(),
  });

  assert.equal(sessions.profile?.userId, OTHER_ACCOUNT.userId, "B's profile is what the screen would show");
  assert.equal(lock.state, "absent", "A's lock is not shown: it is not B's");
  assert.equal(recorded.getItem(LOCK_MIRROR_KEY), null, "the lock bound to A was cleared from storage");
  assert.equal(recorded.getItem(SESSION_MIRROR_KEY)?.includes(OTHER_ACCOUNT.userId), true, "B's profile stays");
});
