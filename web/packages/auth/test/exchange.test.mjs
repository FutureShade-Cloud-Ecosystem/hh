// The exchange flow: the controller adopts the core's exchange answer as the
// bearer (the FB Console JWT is never the bearer and is never stored), and a
// live session re-exchanges before it expires. Time is the injected
// FakeScheduler's, so no test waits. Every value is fake.
import { test } from "node:test";
import assert from "node:assert/strict";
import { AuthController, SessionStore, ThrowSafeMirror, SESSION_MIRROR_KEY } from "../src/index.ts";
import {
  FAKE_ACCOUNT,
  FAKE_JWT,
  FAKE_PASSWORD,
  DeferredAccountSurface,
  FakeAccountSurface,
  FakeScheduler,
  RecordedStorage,
  fakeExchangeAnswer,
  fakeExchangeRefusal,
} from "../src/testing.ts";

const START_MS = 1_000_000_000_000;
const ORG = "yard-ten";
const WINDOW_MS = 900_000;

function rig({ account = new FakeAccountSurface(), leadMs, backoffMs, org = () => ORG } = {}) {
  const clock = new FakeScheduler(START_MS);
  const recorded = new RecordedStorage();
  const sessions = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded), now: clock.now });
  if (typeof account.setClock === "function") account.setClock(clock.now);
  const controller = new AuthController({
    account,
    sessions,
    activeOrg: org,
    now: clock.now,
    timers: clock,
    ...(leadMs === undefined ? {} : { refreshLeadMs: leadMs }),
    ...(backoffMs === undefined ? {} : { refreshBackoffMs: backoffMs }),
  });
  return { account, clock, controller, recorded, sessions };
}

async function signedIn(options) {
  const r = rig(options);
  const result = await r.controller.signInWithPassword(FAKE_ACCOUNT.email, FAKE_PASSWORD);
  assert.equal(result.ok, true, `sign-in: ${result.ok ? "" : result.error}`);
  return r;
}

test("sign-in adopts the exchange answer as the bearer, asking with the active org", async () => {
  const { account, sessions } = await signedIn();
  assert.deepEqual(account.exchangeOrgs, [ORG], "one exchange, for the active organisation");
  assert.equal(sessions.current.token, `${FAKE_JWT}-EX-1`, "the core's token is the bearer");
  assert.equal(sessions.current.tokenExpiresAtMs, START_MS + WINDOW_MS, "the core's expiry is custody's expiry");
  assert.deepEqual(sessions.current.user, { ...FAKE_ACCOUNT }, "the display profile comes from the exchange user block");
  assert.equal(typeof account.createJWT, "undefined", "the account surface no longer hands a Console JWT to the package");
});

test("no write to any storage carries a token, and no fallback key is written", async () => {
  const { recorded } = await signedIn();
  assert.ok(recorded.writes.length > 0, "the profile mirror was written");
  for (const write of recorded.writes.filter((w) => w.key !== "hh-auth.storage-probe")) {
    assert.equal(write.key, SESSION_MIRROR_KEY, `only the profile mirror is written (saw ${write.key})`);
    assert.equal(write.value.includes(FAKE_JWT), false, "no write carries the token");
    assert.equal(/cookieFallback/i.test(write.key + write.value), false, "no cookie fallback entry");
  }
});

test("sign-in refused by the exchange adopts nothing and ends the platform session", async () => {
  for (const code of ["unauthorized", "forbidden", "no_session"]) {
    const account = new FakeAccountSurface();
    account.script(fakeExchangeRefusal(code));
    const { controller, sessions } = rig({ account });
    const result = await controller.signInWithPassword(FAKE_ACCOUNT.email, FAKE_PASSWORD);
    assert.equal(result.ok, false, code);
    assert.match(result.error, new RegExp(code), "the refusal code is named, nothing else");
    assert.equal(sessions.current, null, `${code}: nothing adopted`);
    assert.equal(sessions.profile, null, `${code}: no profile kept`);
    assert.equal(account.deleteCount, 1, `${code}: the platform session is not left behind`);
  }
});

test("sign-in refused for a transient reason adopts nothing and leaves the platform session alone", async () => {
  for (const code of ["offline", "unavailable", "rate_limited", "bad_request"]) {
    const account = new FakeAccountSurface();
    account.script(fakeExchangeRefusal(code));
    const { controller, sessions } = rig({ account });
    const result = await controller.signInWithPassword(FAKE_ACCOUNT.email, FAKE_PASSWORD);
    assert.equal(result.ok, false, code);
    assert.equal(sessions.current, null);
    assert.equal(account.deleteCount, 0, `${code}: the platform session stays for a retry`);
  }
});

test("sign-in without an active organisation is refused before any platform call", async () => {
  const account = new FakeAccountSurface();
  const { controller } = rig({ account, org: () => null });
  const result = await controller.signInWithPassword(FAKE_ACCOUNT.email, FAKE_PASSWORD);
  assert.equal(result.ok, false);
  assert.match(result.error, /organisation/);
  assert.equal(account.creationCount, 0);
  assert.equal(account.exchangeCount, 0);
});

test("resume adopts the exchange answer, not a Console JWT", async () => {
  const { account, controller, sessions } = rig();
  await account.createEmailPasswordSession(FAKE_ACCOUNT.email, FAKE_PASSWORD); // the platform cookie survives a reload
  const resumed = await controller.resume();
  assert.equal(resumed.ok, true, resumed.ok ? "" : resumed.error);
  assert.deepEqual(account.exchangeOrgs, [ORG]);
  assert.equal(sessions.current.token, `${FAKE_JWT}-EX-1`);
});

test("the refresh runs one lead before expiry and adopts the new token", async () => {
  const { account, clock, sessions } = await signedIn({ leadMs: 60_000 });
  await clock.advance(WINDOW_MS - 60_000 - 1);
  assert.equal(account.exchangeCount, 1, "not yet");
  await clock.advance(1);
  assert.equal(account.exchangeCount, 2, "exactly at expiry minus the lead");
  assert.equal(sessions.current.token, `${FAKE_JWT}-EX-2`);
  assert.equal(sessions.current.tokenExpiresAtMs, START_MS + WINDOW_MS - 60_000 + WINDOW_MS);
  await clock.advance(WINDOW_MS - 60_000);
  assert.equal(account.exchangeCount, 3, "and it keeps going while the session is live");
});

test("the default lead is a minute", async () => {
  const { account, clock } = await signedIn();
  await clock.advance(WINDOW_MS - 60_000);
  assert.equal(account.exchangeCount, 2);
});

test("a short window caps the lead at half the window, so a refresh never hammers", async () => {
  const account = new FakeAccountSurface();
  account.tokenLifetimeMs = 30_000;
  const { clock } = await signedIn({ account, leadMs: 60_000 });
  await clock.advance(14_999);
  assert.equal(account.exchangeCount, 1);
  await clock.advance(1);
  assert.equal(account.exchangeCount, 2);
});

test("429 and 503 keep the current token and back off; the next try can succeed", async () => {
  for (const code of ["rate_limited", "unavailable"]) {
    const account = new FakeAccountSurface();
    const { clock, sessions } = await signedIn({ account, backoffMs: 10_000 });
    const first = sessions.current.token;
    account.script(fakeExchangeRefusal(code), fakeExchangeRefusal(code));
    await clock.advance(WINDOW_MS - 60_000);
    assert.equal(account.exchangeCount, 2, `${code}: refused`);
    assert.equal(sessions.current.token, first, `${code}: the token stays until it expires`);
    assert.equal(account.deleteCount, 0, `${code}: not a sign out`);
    await clock.advance(9_999);
    assert.equal(account.exchangeCount, 2, `${code}: backing off`);
    await clock.advance(1);
    assert.equal(account.exchangeCount, 3, `${code}: first retry after the base backoff`);
    await clock.advance(19_999);
    assert.equal(account.exchangeCount, 3, `${code}: the backoff doubled`);
    await clock.advance(1);
    assert.equal(account.exchangeCount, 4, `${code}: second retry succeeds`);
    assert.notEqual(sessions.current.token, first, `${code}: the new token is adopted`);
  }
});

test("offline and a thrown exchange never end a session, even past expiry", async () => {
  const account = new FakeAccountSurface();
  const { clock, sessions } = await signedIn({ account, backoffMs: 10_000 });
  const offline = fakeExchangeRefusal("offline");
  account.script(offline, new Error("simulated: the adapter threw"), offline, offline);
  await clock.advance(WINDOW_MS - 60_000); // first try, 10 s backoff
  await clock.advance(10_000); // the throw, 20 s backoff
  await clock.advance(20_000); // offline, 40 s backoff
  await clock.advance(40_000); // offline again, now past the token's expiry
  assert.equal(account.exchangeCount, 5);
  assert.equal(sessions.current, null, "the token expired while the network was down");
  assert.notEqual(sessions.profile, null, "the profile is kept: offline never ends a session");
  assert.equal(account.deleteCount, 0);
  await clock.advance(80_000);
  assert.notEqual(sessions.current, null, "the next try after the network returns restores the bearer");
});

test("a 401 or 403 or no_session from the refresh ends the session", async () => {
  for (const code of ["unauthorized", "forbidden", "no_session"]) {
    const account = new FakeAccountSurface();
    const { clock, recorded, sessions } = await signedIn({ account });
    account.script(fakeExchangeRefusal(code));
    await clock.advance(WINDOW_MS - 60_000);
    assert.equal(sessions.current, null, `${code}: the bearer is gone`);
    assert.equal(sessions.profile, null, `${code}: custody cleared`);
    assert.equal(account.deleteCount, 1, `${code}: AccountSurface.deleteSession was called`);
    assert.equal(clock.pending, 0, `${code}: no timer is left armed`);
    assert.equal(recorded.getItem(SESSION_MIRROR_KEY), null, `${code}: the mirror is cleared`);
  }
});

test("an exchange answering for a different person ends the session", async () => {
  const account = new FakeAccountSurface();
  const { clock, sessions } = await signedIn({ account });
  account.script(fakeExchangeAnswer(`${FAKE_JWT}-OTHER`, START_MS + 2 * WINDOW_MS, { user_id: "someone-else" }));
  await clock.advance(WINDOW_MS - 60_000);
  assert.equal(sessions.current, null, "a different subject is never adopted over the first");
  assert.equal(account.deleteCount, 1);
});

test("sign-out wins a refresh that is in flight", async () => {
  const account = new DeferredAccountSurface();
  const r = rig({ account });
  const signIn = r.controller.signInWithPassword(FAKE_ACCOUNT.email, FAKE_PASSWORD);
  (await until(() => account.handoff("createEmailPasswordSession"))).complete({ ...FAKE_ACCOUNT });
  (await until(() => account.handoffs("exchange")[0])).complete(
    fakeExchangeAnswer(`${FAKE_JWT}-EX-1`, START_MS + WINDOW_MS),
  );
  assert.equal((await signIn).ok, true);

  const advancing = r.clock.advance(WINDOW_MS - 60_000);
  const refresh = await until(() => account.handoffs("exchange")[1]);
  await r.controller.signOut();
  assert.equal(r.sessions.current, null);
  refresh.complete(fakeExchangeAnswer(`${FAKE_JWT}-EX-LATE`, START_MS + 2 * WINDOW_MS));
  await advancing;
  await flush();
  assert.equal(r.sessions.current, null, "the late answer adopted nothing");
  assert.equal(r.clock.pending, 0, "and armed nothing");
});

test("sign-out clears the pending refresh", async () => {
  const { clock, controller } = await signedIn();
  assert.equal(clock.pending, 1, "one refresh is armed while signed in");
  await controller.signOut();
  assert.equal(clock.pending, 0);
});

test("a controller with no active organisation wired never arms a refresh (the demo apps)", async () => {
  const clock = new FakeScheduler(START_MS);
  const sessions = new SessionStore({ now: clock.now });
  const controller = new AuthController({ account: new FakeAccountSurface(), sessions, now: clock.now, timers: clock });
  assert.equal(controller.adopt({ token: FAKE_JWT, tokenExpiresAtMs: START_MS + WINDOW_MS, user: { ...FAKE_ACCOUNT } }), true);
  assert.equal(clock.pending, 0);
});

async function flush() {
  await new Promise((resolve) => setImmediate(resolve));
}

async function until(read) {
  for (let i = 0; i < 100; i += 1) {
    const value = read();
    if (value) return value;
    await flush();
  }
  throw new Error("the awaited platform call never arrived");
}
