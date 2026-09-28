// The wiring into the api-client, proven against the real ApiClient: every
// request carries Authorization from memory, X-Org from the active
// organisation, and the audit pair exactly while a lock is open; a request
// without an active organisation is refused before it leaves; and the
// package exposes no role state at all.
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

async function rig({ activeOrg = () => ORG, withSession = true, lockOpen = false } = {}) {
  const recorded = new RecordedStorage();
  const sessions = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded), now: () => START_MS });
  if (withSession) {
    const adopted = sessions.adopt({
      token: FAKE_JWT,
      tokenExpiresAtMs: START_MS + 60_000,
      user: { ...FAKE_ACCOUNT },
      refreshHandle: { handle: "fake-refresh-handle", expiresAtMs: START_MS + 3_600_000 },
    });
    assert.equal(adopted, true);
  }
  const credentials = new FakeCredentialProvider();
  const lock = new DeviceLock({ credentials, mirror: new ThrowSafeMirror(LOCK_MIRROR_KEY, recorded) });
  if (lockOpen) {
    assert.equal((await lock.engage(FAKE_ACCOUNT.userId)).ok, true);
    credentials.present(FAKE_PIN);
    assert.equal((await lock.unlock()).ok, true);
  }
  const provider = authHeaderProvider({ sessions, lock, activeOrg });
  const { calls, fetchLike } = recordingFetch();
  const client = new ApiClient({ baseUrl: "https://edge.example.test", fetch: fetchLike, headers: provider });
  return { calls, client, credentials, lock, recorded, sessions };
}

test("every request carries Authorization from memory, X-Org, and the audit pair exactly when the lock is open", async () => {
  const { calls, client, lock } = await rig({ lockOpen: true });
  const result = await client.request({ method: "GET", path: "/api/v1/tenant/ping", operationId: "probe" });
  assert.equal(result.ok, true);
  assert.equal(calls.length, 1, "exactly one request left");
  assert.deepEqual(
    calls[0].init.headers,
    {
      Authorization: `Bearer ${FAKE_JWT}`,
      "X-Org": ORG,
      "X-Unlocked-By": FAKE_ACCOUNT.userId,
      "X-Unlocked-At": String(lock.openUnlock.unlockedAtMs),
    },
    "the wire carries exactly the four headers this package owns, nothing else",
  );
});

test("with the lock engaged but closed, the audit headers are absent", async () => {
  const { calls, client } = await rig();
  const result = await client.request({ method: "GET", path: "/api/v1/tenant/ping", operationId: "probe" });
  assert.equal(result.ok, true);
  assert.deepEqual(
    calls[0].init.headers,
    { Authorization: `Bearer ${FAKE_JWT}`, "X-Org": ORG },
    "locked: exactly Authorization and X-Org",
  );
});

test("with no live session, no Authorization header is sent", async () => {
  const { calls, client } = await rig({ withSession: false });
  const result = await client.request({ method: "GET", path: "/api/v1/tenant/ping", operationId: "probe" });
  assert.equal(result.ok, true);
  assert.deepEqual(calls[0].init.headers, { "X-Org": ORG });
});

test("a request without an active organisation is refused before it leaves", async () => {
  for (const org of [null, undefined, "", "   ", "bad\norg"]) {
    const { calls, client } = await rig({ activeOrg: () => org });
    const result = await client.request({ method: "GET", path: "/api/v1/tenant/ping", operationId: "probe" });
    assert.equal(result.ok, false, `org ${JSON.stringify(org)} must be refused`);
    assert.match(result.error, /X-Org/, "the refusal names the missing header");
    assert.equal(result.status, undefined, "nothing answered: the refusal carries no status");
    assert.equal(calls.length, 0, `the request for org ${JSON.stringify(org)} never left`);
  }
});

test("the controller signs in through the fake account surface and custody holds the token in memory only", async () => {
  const recorded = new RecordedStorage();
  const sessions = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded), now: () => START_MS });
  const account = new FakeAccountSurface();
  const controller = new AuthController({ account, sessions });

  const result = await controller.signInWithPassword(FAKE_ACCOUNT.email, FAKE_PASSWORD);
  assert.equal(result.ok, true, `sign-in resolves a Result: ${result.ok ? "" : result.error}`);
  assert.deepEqual(result.value, { ...FAKE_ACCOUNT });
  assert.equal(sessions.current.token, FAKE_JWT, "the fake JWT (documented fake) lives in memory");

  const payload = JSON.parse(recorded.writes.find((w) => w.key === SESSION_MIRROR_KEY).value);
  assert.equal(payload.handle, "fake-refresh-handle-1", "the refresh handle is mirrored");
  for (const write of recorded.writes) {
    assert.equal(write.value.includes(FAKE_JWT), false, "no write carries the token");
  }

  const wrong = await controller.signInWithPassword(FAKE_ACCOUNT.email, "not-even-the-fake-password");
  assert.equal(wrong.ok, false, "a wrong credential is an error Result, never a throw");
  assert.match(wrong.error, /invalid credentials/);
});

test("the controller adopts an out-of-band session and signs out", async () => {
  const recorded = new RecordedStorage();
  const sessions = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded), now: () => START_MS });
  const account = new FakeAccountSurface();
  const controller = new AuthController({ account, sessions });

  assert.equal(
    controller.adopt({
      token: FAKE_JWT,
      tokenExpiresAtMs: START_MS + 60_000,
      user: { ...FAKE_ACCOUNT },
      refreshHandle: { handle: "fake-refresh-handle", expiresAtMs: START_MS + 3_600_000 },
    }),
    true,
    "the Google callback through the Console bridge adopts as data",
  );
  assert.notEqual(sessions.current, null);

  await controller.signOut();
  assert.equal(account.deleteCount, 1, "the platform session was ended");
  assert.equal(sessions.current, null, "custody is wiped");
  assert.equal(sessions.restorable, null);
  assert.equal(recorded.size, 0, "the mirror holds nothing after sign-out");
});

test("refreshJWT mints a fresh in-memory token and carries the handle through", async () => {
  let nowMs = START_MS;
  const sessions = new SessionStore({
    mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, new RecordedStorage()),
    now: () => nowMs,
  });
  const account = new FakeAccountSurface();
  account.setClock(() => nowMs);
  const controller = new AuthController({ account, sessions });
  await controller.signInWithPassword(FAKE_ACCOUNT.email, FAKE_PASSWORD);
  const handleBefore = sessions.restorable.handle;

  nowMs += 40_000; // still live, but the token is aging
  const refreshed = await controller.refreshJWT();
  assert.equal(refreshed.ok, true);
  assert.equal(sessions.current.token, FAKE_JWT, "the fake surface mints the same fake value; the custody contract is what is under test");
  assert.notEqual(sessions.current, null);
  assert.equal(sessions.restorable.handle, handleBefore, "the refresh handle rides through unchanged");

  nowMs += 10_000_000; // expired: no live session to refresh
  assert.equal(sessions.current, null);
  const refused = await controller.refreshJWT();
  assert.equal(refused.ok, false);
  assert.match(refused.error, /no live session/);
});

test("the public surface exposes no role state at all", async () => {
  const auth = await import("../src/index.ts");
  const keys = Object.keys(auth);
  assert.ok(keys.length >= 10, "the surface is present");
  const offenders = keys.filter((key) => /role/i.test(key));
  assert.deepEqual(offenders, [], `no exported name carries role state (offenders: ${offenders.join(", ")})`);
});
