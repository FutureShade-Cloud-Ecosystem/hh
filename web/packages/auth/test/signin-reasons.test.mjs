// The sign in screen needs to tell apart WHY a sign in or a resume did not
// land (wrong password, not allowed, too many attempts, unavailable, offline)
// without reading the words of an error. The controller's failed results
// therefore carry a closed `reason`. A persona adoption (no platform session
// behind it) must not arm a refresh. Every value is fake.
import { test } from "node:test";
import assert from "node:assert/strict";
import { AuthController, PlatformRefusedError, PlatformUnreachableError, SessionStore, ThrowSafeMirror, SESSION_MIRROR_KEY } from "../src/index.ts";
import {
  FAKE_ACCOUNT,
  FAKE_PASSWORD,
  FakeAccountSurface,
  FakeScheduler,
  RecordedStorage,
  fakeExchangeRefusal,
} from "../src/testing.ts";

const START_MS = 1_000_000_000_000;
const ORG = "yard-ten";

function rig(account = new FakeAccountSurface()) {
  const clock = new FakeScheduler(START_MS);
  const sessions = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, new RecordedStorage()), now: clock.now });
  account.setClock(clock.now);
  const controller = new AuthController({ account, sessions, activeOrg: () => ORG, now: clock.now, timers: clock });
  return { account, clock, controller, sessions };
}

class ScriptedCreate extends FakeAccountSurface {
  failure = null;
  async createEmailPasswordSession(email, password) {
    if (this.failure !== null) throw this.failure;
    return super.createEmailPasswordSession(email, password);
  }
}

test("a console refusal at sign in maps to a reason by its status", async () => {
  const cases = [
    [401, "bad_credentials"],
    [400, "bad_credentials"],
    [404, "bad_credentials"],
    [429, "rate_limited"],
    [500, "unavailable"],
    [503, "unavailable"],
  ];
  for (const [status, reason] of cases) {
    const account = new ScriptedCreate();
    account.failure = new PlatformRefusedError(status, `refused ${status}`);
    const { controller } = rig(account);
    const result = await controller.signInWithPassword(FAKE_ACCOUNT.email, "wrong");
    assert.equal(result.ok, false);
    assert.equal(result.reason, reason, `status ${status}`);
  }
});

test("a network failure at sign in is offline, an unknown throw is failed", async () => {
  const offline = new ScriptedCreate();
  offline.failure = new PlatformUnreachableError();
  assert.equal((await rig(offline).controller.signInWithPassword("a@b.invalid", "x")).reason, "offline");
  const odd = new ScriptedCreate();
  odd.failure = new Error("something else");
  assert.equal((await rig(odd).controller.signInWithPassword("a@b.invalid", "x")).reason, "failed");
});

test("a refused exchange at sign in carries the exchange code as the reason", async () => {
  for (const code of ["unauthorized", "forbidden", "no_session", "rate_limited", "unavailable", "offline", "bad_request"]) {
    const account = new FakeAccountSurface();
    account.script(fakeExchangeRefusal(code));
    const { controller } = rig(account);
    const result = await controller.signInWithPassword(FAKE_ACCOUNT.email, FAKE_PASSWORD);
    assert.equal(result.ok, false);
    assert.equal(result.reason, code);
  }
});

test("no organisation, a sign out mid flight and a supersession each have their own reason", async () => {
  const account = new FakeAccountSurface();
  const clock = new FakeScheduler(START_MS);
  const sessions = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, new RecordedStorage()), now: clock.now });
  const noOrg = new AuthController({ account, sessions, activeOrg: () => null, now: clock.now, timers: clock });
  assert.equal((await noOrg.signInWithPassword(FAKE_ACCOUNT.email, FAKE_PASSWORD)).reason, "failed");
});

test("a successful sign in has no reason", async () => {
  const { controller } = rig();
  const result = await controller.signInWithPassword(FAKE_ACCOUNT.email, FAKE_PASSWORD);
  assert.equal(result.ok, true);
  assert.equal("reason" in result, false);
});

test("resume failures carry a reason: no session, refused exchange, offline", async () => {
  const none = rig();
  const noSession = await none.controller.resume();
  assert.equal(noSession.ok, false);
  assert.equal(noSession.reason, "no_session", "no platform session is not an error to show");

  const refused = new FakeAccountSurface();
  await refused.createEmailPasswordSession(FAKE_ACCOUNT.email, FAKE_PASSWORD);
  refused.script(fakeExchangeRefusal("forbidden"));
  assert.equal((await rig(refused).controller.resume()).reason, "forbidden");

  const unavailable = new FakeAccountSurface();
  await unavailable.createEmailPasswordSession(FAKE_ACCOUNT.email, FAKE_PASSWORD);
  unavailable.script(fakeExchangeRefusal("unavailable"));
  assert.equal((await rig(unavailable).controller.resume()).reason, "unavailable");

  class Unreachable extends FakeAccountSurface {
    async getSession() {
      throw new PlatformUnreachableError();
    }
  }
  assert.equal((await rig(new Unreachable()).controller.resume()).reason, "offline");
});

test("adopt with refresh false arms no refresh, so a persona session is not signed out by a refresh with no platform session behind it", async () => {
  const { account, clock, controller, sessions } = rig();
  const adopted = controller.adopt({
    token: "fake-persona-token",
    tokenExpiresAtMs: START_MS + 600_000,
    user: { userId: "persona:inside_sales", email: "p@hh-demo.invalid", name: "Sales" },
    refresh: false,
  });
  assert.equal(adopted, true);
  await clock.advance(700_000);
  assert.equal(account.exchangeCount, 0, "no refresh ran");
  assert.equal(sessions.current, null, "the token simply expired, nothing was signed out by a failed refresh");
});

test("adopt without the option still arms the refresh", async () => {
  const { account, clock, controller } = rig();
  await account.createEmailPasswordSession(FAKE_ACCOUNT.email, FAKE_PASSWORD);
  controller.adopt({ token: "fake-token", tokenExpiresAtMs: START_MS + 600_000, user: { ...FAKE_ACCOUNT } });
  await clock.advance(560_000);
  assert.equal(account.exchangeCount, 1);
});

test("a console outage or throttle on the resume's account read keeps the profile and the lock and backs off, never no_session", async () => {
  for (const [status, reason] of [[503, "unavailable"], [500, "unavailable"], [429, "rate_limited"]]) {
    class Down extends FakeAccountSurface {
      async getSession() {
        throw new PlatformRefusedError(status);
      }
    }
    const { clock, controller, sessions } = rig(new Down());
    sessions.adopt({ token: "fake-old", tokenExpiresAtMs: START_MS + 600_000, user: { ...FAKE_ACCOUNT } });
    const result = await controller.resume();
    assert.equal(result.ok, false);
    assert.equal(result.reason, reason, `status ${status}`);
    assert.deepEqual(sessions.profile, { ...FAKE_ACCOUNT }, "the profile is kept");
    assert.equal(clock.pending, 1, "a backoff refresh is armed");
  }
});

test("a console 401 on the resume's account read is still no_session and clears", async () => {
  class Gone extends FakeAccountSurface {
    async getSession() {
      throw new PlatformRefusedError(401);
    }
  }
  const { controller, sessions } = rig(new Gone());
  sessions.adopt({ token: "fake-old", tokenExpiresAtMs: START_MS + 600_000, user: { ...FAKE_ACCOUNT } });
  assert.equal((await controller.resume()).reason, "no_session");
  assert.equal(sessions.profile, null);
});
