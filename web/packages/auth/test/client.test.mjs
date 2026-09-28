// The wiring into the api-client, proven against the real ApiClient: a staff
// caller sends Authorization from memory and X-Org from the active
// organisation; a tenant caller sends Authorization only, because the core
// answers 400 to X-Org on tenant routes; a staff request without an active
// organisation is refused before it leaves; and the package exposes no role
// state at all.
import { test } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "../../api-client/src/index.ts";
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

async function staffRig({ activeOrg = () => ORG, withSession = true, lockOpen = false } = {}) {
  const { recorded, sessions } = withSession ? adoptedSessions() : custody();
  const credentials = new FakeCredentialProvider();
  const lock = new DeviceLock({ credentials, sessions, mirror: new ThrowSafeMirror(LOCK_MIRROR_KEY, recorded) });
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
  const { calls, client } = await staffRig();
  const result = await client.request({ method: "GET", path: "/api/v1/identity/ping", operationId: "identityPing" });
  assert.equal(result.ok, true);
  assert.deepEqual(calls[0].init.headers, { Authorization: `Bearer ${FAKE_JWT}`, "X-Org": ORG });
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
  assert.equal(sessions.current.token, FAKE_JWT, "the fake JWT (documented fake) lives in memory");

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
  assert.equal(reloaded.current.token, FAKE_JWT, "a fresh token rides memory again");
});

test("resume mints a fresh token after the old one expired: the refresh path is the same seam", async () => {
  let nowMs = START_MS;
  const account = new FakeAccountSurface();
  account.setClock(() => nowMs);
  const { sessions } = custody({ now: () => nowMs });
  const controller = new AuthController({ account, sessions });
  assert.equal((await controller.signInWithPassword(FAKE_ACCOUNT.email, FAKE_PASSWORD)).ok, true);

  nowMs += 900_000 + 1; // past the minted token's lifetime
  assert.equal(sessions.current, null, "the token expired");
  const resumed = await controller.resume();
  assert.equal(resumed.ok, true);
  assert.equal(sessions.current.token, FAKE_JWT, "the fake surface mints the same fake value; the custody contract is what is under test");
  assert.equal(account.getSession.length, 0, "resume goes through the platform session seam");
});

test("resume is refused as data when the platform holds no session", async () => {
  const account = new FakeAccountSurface(); // no sign-in ever happened
  const { sessions } = custody();
  const refused = await new AuthController({ account, sessions }).resume();
  assert.equal(refused.ok, false);
  assert.match(refused.error, /no platform session/);
  assert.equal(sessions.current, null, "nothing was adopted");
});

test("the public surface exposes no role state at all", async () => {
  const auth = await import("../src/index.ts");
  const keys = Object.keys(auth);
  assert.ok(keys.length >= 8, "the surface is present");
  const offenders = keys.filter((key) => /role/i.test(key));
  assert.deepEqual(offenders, [], `no exported name carries role state (offenders: ${offenders.join(", ")})`);
});
