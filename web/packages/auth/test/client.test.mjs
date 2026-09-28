// The wiring into the api-client, proven against the real ApiClient: a staff
// caller sends Authorization from memory and X-Org from the active
// organisation; a tenant caller sends Authorization only, because the core
// answers 400 to X-Org on tenant routes; a staff request without an active
// organisation is refused before it leaves; and the package exposes no role
// state at all.
import { test } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "@hh/api-client";
import {
  AuthController,
  DeviceLock,
  SessionStore,
  ThrowSafeMirror,
  authHeaderProvider,
  LOCK_MIRROR_KEY,
  SESSION_MIRROR_KEY,
} from "../src/index.ts";
import {
  FAKE_ACCOUNT,
  FAKE_JWT,
  FAKE_PASSWORD,
  FAKE_PIN,
  DeferredAccountSurface,
  FakeAccountSurface,
  FakeCredentialProvider,
  RecordedStorage,
} from "../src/testing.ts";

const START_MS = 1_000_000_000_000;
const ORG = "yard-ten";

function recordingFetch() {
  const calls = [];
  const fetchLike = async (url, init) => {
    calls.push({ url, init });
    return { status: 200, ok: true, text: async () => "{}", arrayBuffer: async () => new ArrayBuffer(0) };
  };
  return { calls, fetchLike };
}

function custody({ now = () => START_MS } = {}) {
  const recorded = new RecordedStorage();
  const sessions = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded), now });
  return { recorded, sessions };
}

function adoptedSessions({ now = () => START_MS } = {}) {
  const { recorded, sessions } = custody({ now });
  const okAdopted = sessions.adopt({ token: FAKE_JWT, tokenExpiresAtMs: START_MS + 60_000, user: { ...FAKE_ACCOUNT } });
  assert.equal(okAdopted, true);
  return { recorded, sessions };
}

async function staffRig({ activeOrg = () => ORG, withSession = true, lockOpen = false, lockEngaged = false } = {}) {
  const { recorded, sessions } = withSession ? adoptedSessions() : custody();
  const credentials = new FakeCredentialProvider();
  const lock = new DeviceLock({ credentials, sessions, mirror: new ThrowSafeMirror(LOCK_MIRROR_KEY, recorded) });
  if (lockEngaged) {
    assert.equal((await lock.engage()).ok, true);
    assert.equal(lock.state, "locked", "the lock is engaged and closed");
  }
  if (lockOpen) {
    assert.equal((await lock.engage()).ok, true);
    credentials.present(FAKE_PIN);
    assert.equal((await lock.unlock()).ok, true);
  }
  const provider = authHeaderProvider({ callerClass: "staff", sessions, activeOrg });
  const { calls, fetchLike } = recordingFetch();
  const client = new ApiClient({ baseUrl: "https://edge.example.test", fetch: fetchLike, headers: provider });
  return { calls, client, credentials, lock, recorded, sessions };
}

async function tenantRig({ withSession = true } = {}) {
  const { recorded, sessions } = withSession ? adoptedSessions() : custody();
  const provider = authHeaderProvider({ callerClass: "tenant", sessions });
  const { calls, fetchLike } = recordingFetch();
  const client = new ApiClient({ baseUrl: "https://edge.example.test", fetch: fetchLike, headers: provider });
  return { calls, client, recorded, sessions };
}

test("a staff request carries exactly Authorization and X-Org, even with the lock open", async () => {
  const { calls, client } = await staffRig({ lockOpen: true });
  const result = await client.request({ method: "GET", path: "/api/v1/identity/ping", operationId: "identityPing" });
  assert.equal(result.ok, true);
  assert.equal(calls.length, 1, "exactly one request left");
  assert.deepEqual(
    calls[0].init.headers,
    {
      Authorization: `Bearer ${FAKE_JWT}`,
      "X-Org": ORG,
    },
    "the lock is a screen courtesy: it gates nothing and adds nothing; the audit pair is gone",
  );
});

test("a staff request with the lock engaged but closed carries exactly Authorization and X-Org", async () => {
  const { calls, client, lock } = await staffRig({ lockEngaged: true });
  assert.equal(lock.state, "locked", "the lock really is engaged and closed for this request");
  const result = await client.request({ method: "GET", path: "/api/v1/identity/ping", operationId: "identityPing" });
  assert.equal(result.ok, true);
  assert.deepEqual(calls[0].init.headers, { Authorization: `Bearer ${FAKE_JWT}`, "X-Org": ORG });
});

test("a staff request with no live session sends X-Org and no Authorization", async () => {
  const { calls, client, sessions } = await staffRig({ withSession: false });
  assert.equal(sessions.current, null, "no live session is held");
  const result = await client.request({ method: "GET", path: "/api/v1/identity/ping", operationId: "identityPing" });
  assert.equal(result.ok, true);
  assert.equal(calls.length, 1, "exactly one request left");
  assert.deepEqual(
    calls[0].init.headers,
    { "X-Org": ORG },
    "the staff rule is X-Org always, Authorization only while a live session exists",
  );
});

test("a tenant request sends no X-Org: the core refuses that header on tenant routes", async () => {
  const { calls, client } = await tenantRig();
  const result = await client.request({ method: "GET", path: "/api/v1/tenant/dashboard", operationId: "tenantDashboard" });
  assert.equal(result.ok, true);
  assert.equal(calls.length, 1, "exactly one request left");
  assert.deepEqual(
    calls[0].init.headers,
    { Authorization: `Bearer ${FAKE_JWT}` },
    "Authorization only: an org state may exist in the shell, and the tenant provider takes none of it",
  );
});

test("a tenant request with no live session sends no headers at all", async () => {
  const { calls, client } = await tenantRig({ withSession: false });
  const result = await client.request({ method: "GET", path: "/api/v1/tenant/dashboard", operationId: "tenantDashboard" });
  assert.equal(result.ok, true);
  assert.deepEqual(calls[0].init.headers, {}, "no session, no Authorization, and never an X-Org");
});

test("a staff request without an active organisation is refused before it leaves", async () => {
  for (const org of [null, undefined, "", "   ", "bad\norg"]) {
    const { calls, client } = await staffRig({ activeOrg: () => org });
    const result = await client.request({ method: "GET", path: "/api/v1/identity/ping", operationId: "identityPing" });
    assert.equal(result.ok, false, `org ${JSON.stringify(org)} must be refused`);
    assert.match(result.error, /X-Org/, "the refusal names the missing header");
    assert.equal(result.status, undefined, "nothing answered: the refusal carries no status");
    assert.equal(calls.length, 0, `the request for org ${JSON.stringify(org)} never left`);
  }
});

test("the staff provider's own control-character rule on X-Org, asserted on the provider directly", () => {
  // Directly on the provider, deliberately not through the ApiClient: the
  // transport's own header-value rule would mask the removal of this check,
  // so the package's rule is asserted where only it is under test.
  const { sessions } = adoptedSessions();
  for (const org of ["bad\norg", "bad\rorg", "bad\u0000org"]) {
    const provider = authHeaderProvider({ callerClass: "staff", sessions, activeOrg: () => org });
    assert.throws(() => provider(), /X-Org is malformed/, `org ${JSON.stringify(org)} is refused by the package's own rule`);
  }
  const clean = authHeaderProvider({ callerClass: "staff", sessions, activeOrg: () => ORG });
  assert.deepEqual(
    clean(),
    { "X-Org": ORG, Authorization: `Bearer ${FAKE_JWT}` },
    "a normal organisation passes the package's rule and sends both headers",
  );
});

test("the tenant provider type carries no organisation input to misconfigure", async () => {
  const { sessions } = adoptedSessions();
  const provider = authHeaderProvider({ callerClass: "tenant", sessions });
  assert.equal(typeof provider, "function");
  const headers = provider();
  assert.deepEqual(headers, { Authorization: `Bearer ${FAKE_JWT}` }, "Authorization only");
  assert.equal("X-Org" in headers, false, "the tenant provider cannot mint X-Org by construction");
});

test("the controller signs in through the fake account surface and custody holds the token in memory only", async () => {
  const { recorded, sessions } = custody();
  const account = new FakeAccountSurface();
  const controller = new AuthController({ account, sessions });

  const result = await controller.signInWithPassword(FAKE_ACCOUNT.email, FAKE_PASSWORD);
  assert.equal(result.ok, true, `sign-in resolves a Result: ${result.ok ? "" : result.error}`);
  assert.deepEqual(result.value, { ...FAKE_ACCOUNT });
  assert.equal(sessions.current.token, `${FAKE_JWT}-MINT-1`, "the fake surface's first mint (documented fake) lives in memory");

  const payload = JSON.parse(recorded.writes.find((w) => w.key === SESSION_MIRROR_KEY).value);
  assert.deepEqual(
    Object.keys(payload).sort(),
    ["email", "name", "userId", "v"],
    "the mirror received the display profile and nothing else: no handle, no secret",
  );
  for (const write of recorded.writes) {
    assert.equal(write.value.includes(FAKE_JWT), false, "no write carries the token");
  }

  const wrong = await controller.signInWithPassword(FAKE_ACCOUNT.email, "not-even-the-fake-password");
  assert.equal(wrong.ok, false, "a wrong credential is an error Result, never a throw");
  assert.match(wrong.error, /invalid credentials/);
});

test("the controller adopts an out-of-band session and signs out", async () => {
  const { recorded, sessions } = custody();
  const account = new FakeAccountSurface();
  const controller = new AuthController({ account, sessions });

  assert.equal(
    controller.adopt({
      token: FAKE_JWT,
      tokenExpiresAtMs: START_MS + 60_000,
      user: { ...FAKE_ACCOUNT },
    }),
    true,
    "the Google callback through the Console bridge adopts as data",
  );
  assert.notEqual(sessions.current, null);

  await controller.signOut();
  assert.equal(account.deleteCount, 1, "the platform session was ended");
  assert.equal(sessions.current, null, "custody is wiped");
  assert.equal(recorded.size, 0, "the mirror holds nothing after sign-out");
});

test("adopt with a null user returns false instead of throwing", () => {
  const { sessions } = custody();
  const controller = new AuthController({ account: new FakeAccountSurface(), sessions });
  assert.equal(
    controller.adopt({ token: FAKE_JWT, tokenExpiresAtMs: START_MS + 60_000, user: null }),
    false,
    "a null user is unusable input, resolved as data",
  );
  assert.equal(sessions.current, null, "nothing was adopted");
  assert.equal(sessions.profile, null, "no profile was mirrored");
});

test("a failed resume clears the stale profile it failed from", async () => {
  const { recorded, sessions } = custody();
  sessions.adopt({ token: FAKE_JWT, tokenExpiresAtMs: START_MS + 60_000, user: { ...FAKE_ACCOUNT } });
  assert.notEqual(sessions.profile, null, "the stale display profile is what a failed resume must not leave behind");

  const account = new FakeAccountSurface(); // no platform session: getSession throws
  const refused = await new AuthController({ account, sessions }).resume();
  assert.equal(refused.ok, false);
  assert.match(refused.error, /no platform session/);
  assert.equal(sessions.current, null);
  assert.equal(sessions.profile, null, "the stale profile went with the failed resume");
  assert.equal(recorded.size, 0, "storage holds nothing after the failed resume");
});

test("resume re-signs in through the issuer after a reload, holding nothing to trade", async () => {
  let nowMs = START_MS;
  const account = new FakeAccountSurface();
  account.setClock(() => nowMs);
  const first = custody({ now: () => nowMs });
  const signIn = await new AuthController({ account, sessions: first.sessions }).signInWithPassword(FAKE_ACCOUNT.email, FAKE_PASSWORD);
  assert.equal(signIn.ok, true);

  // The reload: a fresh custody over the SAME browser storage, no live session.
  const reloaded = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, first.recorded), now: () => nowMs });
  assert.equal(reloaded.current, null, "a reload holds no token: none was ever stored");
  assert.notEqual(reloaded.profile, null, "the display profile is what a reload may show");
  const storedPayload = JSON.parse(first.recorded.writes.find((w) => w.key === SESSION_MIRROR_KEY).value);
  assert.deepEqual(
    Object.keys(storedPayload).sort(),
    ["email", "name", "userId", "v"],
    "nothing restorable is credential-shaped: the payload is the display profile",
  );

  const controller = new AuthController({ account, sessions: reloaded });
  nowMs += 1_000;
  const resumed = await controller.resume();
  assert.equal(resumed.ok, true, `resume resolves a Result: ${resumed.ok ? "" : resumed.error}`);
  assert.deepEqual(resumed.value, { ...FAKE_ACCOUNT }, "the issuer's cookie-backed session answered");
  assert.equal(reloaded.current.token, `${FAKE_JWT}-MINT-2`, "a fresh mint, distinct from the sign-in's first, rides memory again");
});

test("resume mints a fresh token after the old one expired: the refresh path is the same seam", async () => {
  let nowMs = START_MS;
  const account = new FakeAccountSurface();
  account.setClock(() => nowMs);
  const { sessions } = custody({ now: () => nowMs });
  const controller = new AuthController({ account, sessions });
  assert.equal((await controller.signInWithPassword(FAKE_ACCOUNT.email, FAKE_PASSWORD)).ok, true);
  const oldToken = sessions.current.token;
  assert.equal(oldToken, `${FAKE_JWT}-MINT-1`, "the sign-in's mint is in custody");

  nowMs += 900_000 + 1; // past the minted token's lifetime
  assert.equal(sessions.current, null, "the token expired");
  const resumed = await controller.resume();
  assert.equal(resumed.ok, true);
  assert.equal(account.getSessionCount, 1, "resume answered through the platform session seam exactly once");
  assert.equal(account.jwtCount, 2, "the refresh minted a second token");
  assert.equal(sessions.current.token, `${FAKE_JWT}-MINT-2`, "custody now holds the FRESH mint, not the expired one");
  assert.notEqual(sessions.current.token, oldToken, "the fresh token is distinguishable from the old one");
});

test("resume is refused as data when the platform holds no session", async () => {
  const account = new FakeAccountSurface(); // no sign-in ever happened
  const { sessions } = custody();
  const refused = await new AuthController({ account, sessions }).resume();
  assert.equal(refused.ok, false);
  assert.match(refused.error, /no platform session/);
  assert.equal(sessions.current, null, "nothing was adopted");
});

// The deferred fakes hold one platform call open at a time; this loop waits
// until the controller has actually made the named call. The bound keeps a
// regression a fast red instead of a hang: if the controller never makes the
// call, this throws and the waiting test fails with the reason.
const HANDOFF_LIMIT = 10_000;

async function untilHandoff(account, name) {
  for (let i = 0; i < HANDOFF_LIMIT; i += 1) {
    const call = account.handoff(name);
    if (call !== null) return call;
    await new Promise((resolve) => setImmediate(resolve));
  }
  throw new Error(`the controller never made the ${name} call the test waits to hand off`);
}

test("sign out wins over a resume in flight: the late platform answer adopts nothing", async () => {
  const { recorded, sessions } = custody();
  const account = new DeferredAccountSurface();
  const controller = new AuthController({ account, sessions });

  const pending = controller.resume(); // the refresh path is in flight
  const getSession = await untilHandoff(account, "getSession");

  await controller.signOut(); // the user signs out while the resume is mid flight
  assert.equal(account.deleteCount, 1);
  assert.equal(sessions.current, null, "sign-out wiped the custody");
  assert.equal(recorded.size, 0, "sign-out left nothing in storage");

  getSession.complete({ ...FAKE_ACCOUNT }); // the platform answers late
  const result = await pending;
  assert.equal(result.ok, false, "the resume resolves an error Result after sign-out");
  assert.match(result.error, /cancelled/);
  assert.equal(sessions.current, null, "custody is empty: the late answer was not adopted");
  assert.equal(sessions.profile, null, "storage holds no profile after the late answer");
  assert.equal(recorded.size, 0, "nothing was written back to the mirror after sign-out");

  // The shared-tablet proof: the next person's request sends no Authorization.
  const tenant = authHeaderProvider({ callerClass: "tenant", sessions });
  assert.deepEqual(tenant(), {}, "the tenant provider sends no Authorization");
  const staff = authHeaderProvider({ callerClass: "staff", sessions, activeOrg: () => ORG });
  assert.deepEqual(staff(), { "X-Org": ORG }, "the staff provider sends X-Org and no Authorization");
  const { calls, fetchLike } = recordingFetch();
  const client = new ApiClient({
    baseUrl: "https://edge.example.test",
    fetch: fetchLike,
    headers: authHeaderProvider({ callerClass: "tenant", sessions }),
  });
  const sent = await client.request({ method: "GET", path: "/api/v1/tenant/dashboard", operationId: "tenantDashboard" });
  assert.equal(sent.ok, true);
  assert.deepEqual(calls[0].init.headers, {}, "the wire carries no Authorization after the cancelled resume");
});

test("sign out wins over a sign-in in flight: the late credential and mint adopt nothing", async () => {
  const { recorded, sessions } = custody();
  const account = new DeferredAccountSurface();
  const controller = new AuthController({ account, sessions });

  const pending = controller.signInWithPassword(FAKE_ACCOUNT.email, FAKE_PASSWORD);
  const created = await untilHandoff(account, "createEmailPasswordSession");
  created.complete({ ...FAKE_ACCOUNT }); // the platform accepts the credential
  const minted = await untilHandoff(account, "createJWT"); // the token mint is in flight

  await controller.signOut(); // the user signs out while the mint is mid flight
  assert.equal(account.deleteCount, 1);
  assert.equal(sessions.current, null);
  assert.equal(recorded.size, 0);

  minted.complete({ jwt: `${FAKE_JWT}-MINT-9`, expiresAtMs: START_MS + 900_000 }); // the mint answers late
  const result = await pending;
  assert.equal(result.ok, false, "the sign-in resolves an error Result after sign-out");
  assert.match(result.error, /cancelled/);
  assert.equal(sessions.current, null, "custody is empty: the late mint was not adopted");
  assert.equal(sessions.profile, null, "storage holds no profile after the late mint");
  assert.equal(recorded.size, 0, "nothing was written back to the mirror after sign-out");

  const tenant = authHeaderProvider({ callerClass: "tenant", sessions });
  assert.deepEqual(tenant(), {}, "the tenant provider sends no Authorization");
  const staff = authHeaderProvider({ callerClass: "staff", sessions, activeOrg: () => ORG });
  assert.equal("Authorization" in staff(), false, "the staff provider sends no Authorization");
});

// One scheduler turn: enough for a resolved platform promise's continuation
// (the controller code that decides) to have run.
function flush() {
  return new Promise((resolve) => setImmediate(resolve));
}

// The deferred fake's handoff(name) returns the LATEST call of that name, so
// a test that parks two of the same call must be able to wait for the one
// that is not the parked previous call. Bounded like untilHandoff.
async function untilNextHandoff(account, name, previous) {
  for (let i = 0; i < HANDOFF_LIMIT; i += 1) {
    const call = account.handoff(name);
    if (call !== null && call !== previous) return call;
    await new Promise((resolve) => setImmediate(resolve));
  }
  throw new Error(`the controller never made a new ${name} call the test waits to hand off`);
}

test("a resume begun while sign-out's own deleteSession is pending adopts nothing once sign-out has returned", async () => {
  // The far side of the await: the resume STARTS during deleteSession, so it
  // captures the generation the sign-out bumped before its await and passes
  // its check unless the sign-out bumps again after the await.
  const { recorded, sessions } = custody();
  const account = new DeferredAccountSurface();
  const controller = new AuthController({ account, sessions });

  const signingOut = controller.signOut(); // deleteSession is in flight
  const pending = controller.resume(); // the resume starts during that await
  const getSession = await untilHandoff(account, "getSession");
  await signingOut; // deleteSession resolves at once; the sign-out completes
  assert.equal(account.deleteCount, 1);
  assert.equal(sessions.current, null, "sign-out wiped the custody");
  assert.equal(recorded.size, 0, "sign-out left nothing in storage");

  getSession.complete({ ...FAKE_ACCOUNT }); // the platform answers late
  await flush();
  assert.equal(
    account.handoff("createJWT"),
    null,
    "the mint was never asked: the sign-out's post-await bump superseded the resume",
  );
  const result = await pending;
  assert.equal(result.ok, false, "the resume resolves an error Result after sign-out");
  assert.match(result.error, /cancelled/);
  assert.equal(sessions.current, null, "custody is empty: the late answer was not adopted");
  assert.equal(sessions.profile, null, "storage holds no profile after the late answer");
  assert.equal(recorded.size, 0, "nothing was written back to the mirror after sign-out");
  const tenant = authHeaderProvider({ callerClass: "tenant", sessions });
  assert.deepEqual(tenant(), {}, "the tenant provider sends no Authorization");
  const staff = authHeaderProvider({ callerClass: "staff", sessions, activeOrg: () => ORG });
  assert.deepEqual(staff(), { "X-Org": ORG }, "the staff provider sends X-Org and no Authorization");
});

test("a sign-in begun while sign-out's own deleteSession is pending adopts nothing once sign-out has returned", async () => {
  const { recorded, sessions } = custody();
  const account = new DeferredAccountSurface();
  const controller = new AuthController({ account, sessions });

  const signingOut = controller.signOut(); // deleteSession is in flight
  const pending = controller.signInWithPassword(FAKE_ACCOUNT.email, FAKE_PASSWORD); // starts during that await
  const created = await untilHandoff(account, "createEmailPasswordSession");
  await signingOut;
  assert.equal(account.deleteCount, 1);
  assert.equal(sessions.current, null);
  assert.equal(recorded.size, 0);

  created.complete({ ...FAKE_ACCOUNT }); // the platform accepts the credential late
  await flush();
  assert.equal(
    account.handoff("createJWT"),
    null,
    "the mint was never asked: the sign-out's post-await bump superseded the sign-in",
  );
  const result = await pending;
  assert.equal(result.ok, false, "the sign-in resolves an error Result after sign-out");
  assert.match(result.error, /cancelled/);
  assert.equal(sessions.current, null, "custody is empty: the late credential was not adopted");
  assert.equal(sessions.profile, null, "storage holds no profile after the late credential");
  assert.equal(recorded.size, 0, "nothing was written back to the mirror after sign-out");
  const tenant = authHeaderProvider({ callerClass: "tenant", sessions });
  assert.deepEqual(tenant(), {}, "the tenant provider sends no Authorization");
  const staff = authHeaderProvider({ callerClass: "staff", sessions, activeOrg: () => ORG });
  assert.deepEqual(staff(), { "X-Org": ORG }, "the staff provider sends X-Org and no Authorization");
});

test("a resume in flight when sign-out is called resolves cancelled even while deleteSession is still pending", async () => {
  // The first bump's case: the resume began BEFORE the sign-out, and its
  // answer lands while the sign-out's own deleteSession is still open. The
  // bump that precedes the await is what cancels it here; a post-await bump
  // alone would let it adopt and resolve ok during the window and only wipe
  // the adoption afterwards.
  const { recorded, sessions } = custody();
  const account = new DeferredAccountSurface({ deferDeleteSession: true });
  const controller = new AuthController({ account, sessions });

  const pending = controller.resume(); // the resume is in flight when the user signs out
  const getSession = await untilHandoff(account, "getSession");

  const signingOut = controller.signOut(); // deleteSession is parked: the sign-out waits
  const deleteCall = await untilHandoff(account, "deleteSession");
  getSession.complete({ ...FAKE_ACCOUNT }); // the platform answers while the sign-out waits
  await flush();
  assert.equal(
    account.handoff("createJWT"),
    null,
    "the mint was never asked: the bump before the await superseded the resume before its answer landed",
  );
  const result = await pending;
  assert.equal(result.ok, false, "the resume resolves an error Result: the sign-out had already been called");
  assert.match(result.error, /cancelled/);
  assert.equal(sessions.current, null, "custody is empty while deleteSession is still in flight");
  assert.equal(sessions.profile, null, "storage holds no profile while deleteSession is still in flight");
  assert.equal(recorded.size, 0, "nothing was written while deleteSession is still in flight");

  deleteCall.complete(undefined); // the platform session ends; the sign-out completes
  await signingOut;
  assert.equal(account.deleteCount, 1);
  assert.equal(sessions.current, null);
  assert.equal(recorded.size, 0);
  const tenant = authHeaderProvider({ callerClass: "tenant", sessions });
  assert.deepEqual(tenant(), {}, "the tenant provider sends no Authorization");
  const staff = authHeaderProvider({ callerClass: "staff", sessions, activeOrg: () => ORG });
  assert.deepEqual(staff(), { "X-Org": ORG }, "the staff provider sends X-Org and no Authorization");
});

test("a sign-out landing between getSession and createJWT cancels the resume's mint", async () => {
  // The resume is parked at its SECOND await: the first check has already
  // passed when the sign-out lands, so this is the second check's case.
  const { recorded, sessions } = custody();
  const account = new DeferredAccountSurface();
  const controller = new AuthController({ account, sessions });

  const pending = controller.resume();
  (await untilHandoff(account, "getSession")).complete({ ...FAKE_ACCOUNT });
  const minted = await untilHandoff(account, "createJWT"); // the token mint is in flight
  await controller.signOut(); // the sign-out lands between the two awaits and completes
  assert.equal(account.deleteCount, 1);
  assert.equal(sessions.current, null);
  assert.equal(recorded.size, 0);

  minted.complete({ jwt: `${FAKE_JWT}-MINT-9`, expiresAtMs: START_MS + 900_000 }); // the mint answers late
  const result = await pending;
  assert.equal(result.ok, false, "the resume resolves an error Result after sign-out");
  assert.match(result.error, /cancelled/);
  assert.equal(sessions.current, null, "custody is empty: the late mint was not adopted");
  assert.equal(sessions.profile, null, "storage holds no profile after the late mint");
  assert.equal(recorded.size, 0, "nothing was written back to the mirror after sign-out");
  const tenant = authHeaderProvider({ callerClass: "tenant", sessions });
  assert.deepEqual(tenant(), {}, "the tenant provider sends no Authorization");
  const staff = authHeaderProvider({ callerClass: "staff", sessions, activeOrg: () => ORG });
  assert.deepEqual(staff(), { "X-Org": ORG }, "the staff provider sends X-Org and no Authorization");
});

// A second identity for the shared-tablet races: person B signs in while
// person A's stale platform answer is still in flight.
const OTHER_ACCOUNT = {
  userId: "fake-user-2",
  email: "other.user@example.invalid",
  name: "Other User",
};

// Sign A out, sign B in through the same deferred surface, and hand back
// once B's mint is completed: the state every stale-answer test below
// asserts against. staleMint is A's parked mint handoff, if A reached it:
// B's mint handoff is the next createJWT call that is not that one.
async function signOutThenOtherSignIn(account, controller, sessions, staleMint = null) {
  await controller.signOut(); // person A signs out (deleteSession resolves at once)
  const signingIn = controller.signInWithPassword(OTHER_ACCOUNT.email, FAKE_PASSWORD);
  const created = await untilHandoff(account, "createEmailPasswordSession");
  created.complete({ ...OTHER_ACCOUNT });
  const mint = await untilNextHandoff(account, "createJWT", staleMint);
  mint.complete({ jwt: `${FAKE_JWT}-B`, expiresAtMs: START_MS + 900_000 });
  assert.equal((await signingIn).ok, true, "person B's sign-in succeeded");
  assert.equal(sessions.current.token, `${FAKE_JWT}-B`, "B's token is in custody");
}

function otherUserCustodyHolds(sessions, recorded) {
  const tenant = authHeaderProvider({ callerClass: "tenant", sessions });
  assert.deepEqual(tenant(), { Authorization: `Bearer ${FAKE_JWT}-B` }, "the tenant provider still sends B's token");
  const staff = authHeaderProvider({ callerClass: "staff", sessions, activeOrg: () => ORG });
  assert.deepEqual(
    staff(),
    { "X-Org": ORG, Authorization: `Bearer ${FAKE_JWT}-B` },
    "the staff provider still sends B's token and the org",
  );
  const stored = recorded.getItem(SESSION_MIRROR_KEY);
  assert.notEqual(stored, null, "B's profile is still in storage");
  assert.equal(stored.includes(OTHER_ACCOUNT.userId), true, "storage holds B's profile, not A's");
}

test("a stale resume whose getSession FAILS after another user signed in leaves that user's custody alone", async () => {
  const { recorded, sessions } = custody();
  const account = new DeferredAccountSurface();
  const controller = new AuthController({ account, sessions });

  const pending = controller.resume(); // person A's resume, in flight
  const staleSession = await untilHandoff(account, "getSession");
  await signOutThenOtherSignIn(account, controller, sessions);

  staleSession.fail(new Error("platform: session gone")); // A's stale answer FAILS late
  const result = await pending;
  assert.equal(result.ok, false, "the stale resume still resolves an error Result");
  assert.match(result.error, /no platform session to resume/);
  assert.equal(sessions.current.token, `${FAKE_JWT}-B`, "B's custody is untouched: the stale failure cleared nothing");
  assert.deepEqual(sessions.profile, OTHER_ACCOUNT, "B's profile is untouched");
  otherUserCustodyHolds(sessions, recorded);
});

test("a stale resume whose mint FAILS after another user signed in leaves that user's custody alone", async () => {
  const { recorded, sessions } = custody();
  const account = new DeferredAccountSurface();
  const controller = new AuthController({ account, sessions });

  const pending = controller.resume(); // person A's resume, in flight
  (await untilHandoff(account, "getSession")).complete({ ...FAKE_ACCOUNT });
  const staleMint = await untilHandoff(account, "createJWT"); // A's mint is parked
  await signOutThenOtherSignIn(account, controller, sessions, staleMint);

  staleMint.fail(new Error("platform: session gone")); // A's stale mint FAILS late
  const result = await pending;
  assert.equal(result.ok, false, "the stale resume still resolves an error Result");
  assert.match(result.error, /the token refresh failed/);
  assert.equal(sessions.current.token, `${FAKE_JWT}-B`, "B's custody is untouched: the stale failure cleared nothing");
  assert.deepEqual(sessions.profile, OTHER_ACCOUNT, "B's profile is untouched");
  otherUserCustodyHolds(sessions, recorded);
});

test("a failed mint with no sign-out in between still clears the custody it failed from", async () => {
  // The generation guard on the failure paths must not overcorrect: when
  // nothing superseded the resume, the failed refresh still clears the stale
  // profile, exactly as the unguarded paths did.
  const { recorded, sessions } = custody();
  sessions.adopt({ token: FAKE_JWT, tokenExpiresAtMs: START_MS + 60_000, user: { ...FAKE_ACCOUNT } });
  assert.notEqual(sessions.profile, null, "the stale display profile is what a failed refresh must not leave behind");
  const account = new DeferredAccountSurface();
  const controller = new AuthController({ account, sessions });

  const pending = controller.resume();
  (await untilHandoff(account, "getSession")).complete({ ...FAKE_ACCOUNT });
  const mint = await untilHandoff(account, "createJWT");
  mint.fail(new Error("network down"));
  const result = await pending;
  assert.equal(result.ok, false);
  assert.match(result.error, /the token refresh failed/);
  assert.equal(sessions.current, null, "the failed refresh cleared the custody it failed from");
  assert.equal(sessions.profile, null, "the stale profile went with the failed refresh");
  assert.equal(recorded.size, 0, "storage holds nothing after the failed refresh");
});

// Two overlapping resumes with NO sign-out between them: both begin from the
// same custody and the same generation, so the guard that keeps a stale
// answer from wiping a newer session is the adoption bump. The newer
// session's mint is named MINT-NEWER so the assertions can tell the two
// adoptions apart; every value stays an obvious fake.
async function twoOverlappingResumes() {
  const { recorded, sessions } = custody();
  sessions.adopt({ token: FAKE_JWT, tokenExpiresAtMs: START_MS + 60_000, user: { ...FAKE_ACCOUNT } });
  const account = new DeferredAccountSurface();
  const controller = new AuthController({ account, sessions });

  const older = controller.resume();
  const olderSession = await untilHandoff(account, "getSession"); // the older resume's answer, parked
  const newer = controller.resume();
  const newerSession = await untilNextHandoff(account, "getSession", olderSession); // the newer's, parked too
  newerSession.complete({ ...FAKE_ACCOUNT });
  const newerMint = await untilHandoff(account, "createJWT");
  newerMint.complete({ jwt: `${FAKE_JWT}-MINT-NEWER`, expiresAtMs: START_MS + 900_000 });
  assert.equal((await newer).ok, true, "the newer resume succeeded and adopted");
  assert.equal(sessions.current.token, `${FAKE_JWT}-MINT-NEWER`, "the newer session is the one in custody");
  return { account, controller, older, olderSession, recorded, sessions };
}

test("two overlapping resumes, no sign-out: the older resume's late failure wipes nothing of the newer session", async () => {
  const { older, olderSession, recorded, sessions } = await twoOverlappingResumes();

  olderSession.fail(new Error("platform: session gone")); // the older resume's answer FAILS, after the newer adopted
  const stale = await older;
  assert.equal(stale.ok, false, "the older resume still resolves an error Result");
  assert.match(stale.error, /no platform session to resume/);
  assert.equal(sessions.current.token, `${FAKE_JWT}-MINT-NEWER`, "the newer session was not wiped by the older resume's late failure");
  assert.deepEqual(sessions.profile, { ...FAKE_ACCOUNT }, "the newer profile is untouched");
  assert.equal(
    recorded.getItem(SESSION_MIRROR_KEY)?.includes(FAKE_ACCOUNT.userId),
    true,
    "storage still holds the newer profile",
  );
  const staff = authHeaderProvider({ callerClass: "staff", sessions, activeOrg: () => ORG });
  assert.deepEqual(
    staff(),
    { "X-Org": ORG, Authorization: `Bearer ${FAKE_JWT}-MINT-NEWER` },
    "the wire still carries the newer session's token",
  );
});

test("two overlapping resumes, no sign-out: the older resume's late success replaces nothing of the newer session", async () => {
  const { account, older, olderSession, sessions } = await twoOverlappingResumes();

  const mintBefore = account.handoff("createJWT"); // the newer resume's mint is the latest createJWT call
  olderSession.complete({ ...FAKE_ACCOUNT }); // the older resume's answer lands late, after the newer adopted
  await flush();
  assert.equal(
    account.handoff("createJWT"),
    mintBefore,
    "the older resume asked for no mint: it was superseded before it could carry on",
  );
  const stale = await older;
  assert.equal(stale.ok, false, "the older resume resolves an error Result instead of replacing the newer session");
  assert.match(stale.error, /cancelled/);
  assert.equal(sessions.current.token, `${FAKE_JWT}-MINT-NEWER`, "the newer session was not replaced by the older resume's late success");
  assert.deepEqual(sessions.profile, { ...FAKE_ACCOUNT }, "the newer profile is untouched");
});

test("the public surface exposes no role state at all", async () => {
  const auth = await import("../src/index.ts");
  const keys = Object.keys(auth);
  assert.ok(keys.length >= 8, "the surface is present");
  const offenders = keys.filter((key) => /role/i.test(key));
  assert.deepEqual(offenders, [], `no exported name carries role state (offenders: ${offenders.join(", ")})`);
});
