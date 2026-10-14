// The Tauri shell adapter over a fake invoke: the exact commands and
// arguments, the contract's error codes mapped to the package's kinds, and
// nothing echoed. Every value is fake; no shell is touched.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { AuthController, PlatformUnreachableError, SessionStore, ThrowSafeMirror, SESSION_MIRROR_KEY } from "../src/index.ts";
import { FakeScheduler, RecordedStorage } from "../src/testing.ts";
import { createTauriAccountSurface, onShellSignedOut, SIGNED_OUT_EVENT } from "../src/tauri-adapter.ts";

const ORG = "yard-ten";
const PASSWORD = "fake-password-sentinel";
const TOKEN = "fake-core-token-sentinel";
const USER = { user_id: "fake-user-1", email: "fake.user@example.invalid", name: "Fake User" };
const ANSWER = {
  token: TOKEN,
  expiresAtMs: 1_000_000_900_000,
  user: { user_id: "fake-user-1", email: "fake.user@example.invalid", display_name: "Fake User", role_code: "fake", role_name: "Fake", tier: 1, scope: "org" },
};

/** A fake invoke: replies is a map of command to a queue of ({value} | {reject}) entries. */
function shell(replies = {}) {
  const calls = [];
  const queues = new Map(Object.entries(replies).map(([k, v]) => [k, [...v]]));
  const invoke = async (command, args) => {
    calls.push({ command, args });
    const next = queues.get(command)?.shift();
    if (next === undefined) throw { code: "unavailable" };
    if ("reject" in next) throw next.reject;
    return next.value;
  };
  return { invoke, calls };
}

const ok = (value) => ({ value });
const refuse = (code) => ({ reject: { code } });

test("an invoke function is required, and construction touches nothing", () => {
  assert.throws(() => createTauriAccountSurface({}), /invoke/);
  assert.throws(() => createTauriAccountSurface({ invoke: "no" }), /invoke/);
  const { invoke, calls } = shell();
  createTauriAccountSurface({ invoke });
  assert.equal(calls.length, 0);
});

test("sign in calls auth_sign_in with the credentials and returns the user only", async () => {
  const { invoke, calls } = shell({ auth_sign_in: [ok({ user: USER })] });
  const user = await createTauriAccountSurface({ invoke }).createEmailPasswordSession("fake.user@example.invalid", PASSWORD);
  assert.deepEqual(user, { userId: "fake-user-1", email: "fake.user@example.invalid", name: "Fake User" });
  assert.deepEqual(calls, [{ command: "auth_sign_in", args: { email: "fake.user@example.invalid", password: PASSWORD } }]);
});

test("a sign in refused is an error naming the code, and an outage is the unreachable kind", async () => {
  for (const [code, unreachable] of [
    ["bad_credentials", false],
    ["no_session", false],
    ["forbidden", false],
    ["offline", true],
    ["unavailable", true],
    ["rate_limited", true],
    ["keychain_unavailable", true],
  ]) {
    const { invoke } = shell({ auth_sign_in: [refuse(code)] });
    await assert.rejects(
      createTauriAccountSurface({ invoke }).createEmailPasswordSession("a@b.invalid", PASSWORD),
      (error) => {
        assert.equal(error instanceof PlatformUnreachableError, unreachable, code);
        assert.match(error.message, new RegExp(code));
        assert.equal(error.message.includes(PASSWORD), false, "no input in the message");
        assert.equal(error.message.includes("a@b.invalid"), false, "no input in the message");
        return true;
      },
      code,
    );
  }
});

test("a malformed account answer or a rejection with no known code is not a session", async () => {
  for (const reply of [ok({}), ok({ user: { user_id: "" , email: "e", name: "n" } }), ok({ user: { user_id: "u", email: 3, name: "n" } }), ok(null), ok("x")]) {
    const { invoke } = shell({ auth_sign_in: [reply] });
    await assert.rejects(createTauriAccountSurface({ invoke }).createEmailPasswordSession("a@b.invalid", PASSWORD), /not usable/);
  }
  for (const reject of ["boom", null, 7, { message: "x" }, { code: "something_else" }, new Error("raw")]) {
    const { invoke } = shell({ auth_resume: [{ reject }] });
    await assert.rejects(createTauriAccountSurface({ invoke }).getSession(), (error) => {
      assert.equal(error instanceof PlatformUnreachableError, true, "an unknown rejection says nothing about the person");
      assert.equal(error.message.includes("boom"), false);
      return true;
    });
  }
});

test("resume calls auth_resume with no arguments; no_session is a refusal and an outage is not", async () => {
  const { invoke, calls } = shell({ auth_resume: [ok({ user: USER }), refuse("no_session"), refuse("offline"), refuse("unavailable")] });
  const account = createTauriAccountSurface({ invoke });
  assert.deepEqual(await account.getSession(), { userId: "fake-user-1", email: "fake.user@example.invalid", name: "Fake User" });
  assert.deepEqual(calls[0], { command: "auth_resume", args: undefined });
  await assert.rejects(account.getSession(), (e) => !(e instanceof PlatformUnreachableError) && /no_session/.test(e.message));
  await assert.rejects(account.getSession(), (e) => e instanceof PlatformUnreachableError);
  await assert.rejects(account.getSession(), (e) => e instanceof PlatformUnreachableError, "an outage is never no_session");
});

test("exchange calls auth_exchange with the org and returns the core's answer as is", async () => {
  const { invoke, calls } = shell({ auth_exchange: [ok(ANSWER)] });
  const result = await createTauriAccountSurface({ invoke }).exchange(ORG);
  assert.deepEqual(result, { ok: true, answer: ANSWER });
  assert.deepEqual(calls, [{ command: "auth_exchange", args: { org: ORG } }]);
});

test("exchange maps every contract code to the package's kinds, and only three end a session", async () => {
  const expected = {
    bad_request: "bad_request",
    unauthorized: "unauthorized",
    forbidden: "forbidden",
    rate_limited: "rate_limited",
    unavailable: "unavailable",
    offline: "offline",
    no_session: "no_session",
    // Not exchange words, and never a verdict on the person:
    keychain_unavailable: "unavailable",
    bad_credentials: "unavailable",
  };
  for (const [rust, kind] of Object.entries(expected)) {
    const { invoke } = shell({ auth_exchange: [refuse(rust)] });
    assert.deepEqual(await createTauriAccountSurface({ invoke }).exchange(ORG), { ok: false, code: kind }, rust);
  }
  for (const reject of ["boom", null, { code: "nope" }, new Error("raw")]) {
    const { invoke } = shell({ auth_exchange: [{ reject }] });
    assert.deepEqual(await createTauriAccountSurface({ invoke }).exchange(ORG), { ok: false, code: "unavailable" });
  }
});

test("an exchange answer short of a usable one is unavailable, never a session", async () => {
  const bad = [
    {},
    { ...ANSWER, token: "" },
    { ...ANSWER, expiresAtMs: "soon" },
    { ...ANSWER, expiresAtMs: Number.NaN },
    { ...ANSWER, user: null },
    { ...ANSWER, user: { ...ANSWER.user, tier: 1.5 } },
    { ...ANSWER, user: { ...ANSWER.user, user_id: "" } },
    null,
    "x",
  ];
  for (const answer of bad) {
    const { invoke } = shell({ auth_exchange: [ok(answer)] });
    assert.deepEqual(await createTauriAccountSurface({ invoke }).exchange(ORG), { ok: false, code: "unavailable" });
  }
});

test("a blank org never reaches the shell", async () => {
  const { invoke, calls } = shell();
  const account = createTauriAccountSurface({ invoke });
  for (const org of ["", "   ", undefined, null]) {
    assert.deepEqual(await account.exchange(org), { ok: false, code: "bad_request" });
  }
  assert.equal(calls.length, 0);
});

test("sign out calls auth_sign_out, and a failure is reported", async () => {
  const { invoke, calls } = shell({ auth_sign_out: [ok({}), refuse("keychain_unavailable")] });
  const account = createTauriAccountSurface({ invoke });
  await account.deleteSession();
  assert.deepEqual(calls, [{ command: "auth_sign_out", args: undefined }]);
  await assert.rejects(account.deleteSession(), /keychain_unavailable/);
});

test("Google is not offered in a shell", async () => {
  const { invoke, calls } = shell();
  await assert.rejects(createTauriAccountSurface({ invoke }).createOAuth2Session("google", "https://x.invalid/"), /not supported/);
  assert.equal(calls.length, 0);
});

test("only the four contract commands are ever invoked, and none is a keychain or a free-form call", async () => {
  const { invoke, calls } = shell({
    auth_sign_in: [ok({ user: USER })],
    auth_resume: [ok({ user: USER })],
    auth_exchange: [ok(ANSWER)],
    auth_sign_out: [ok({})],
  });
  const account = createTauriAccountSurface({ invoke });
  await account.createEmailPasswordSession("a@b.invalid", PASSWORD);
  await account.getSession();
  await account.exchange(ORG);
  await account.deleteSession();
  assert.deepEqual(calls.map((c) => c.command), ["auth_sign_in", "auth_resume", "auth_exchange", "auth_sign_out"]);
});

test("the sign out event is subscribed by name", async () => {
  const seen = [];
  const unlisten = () => {};
  const off = await onShellSignedOut(async (event, handler) => {
    seen.push([event, typeof handler]);
    return unlisten;
  }, () => {});
  assert.equal(off, unlisten);
  assert.deepEqual(seen, [[SIGNED_OUT_EVENT, "function"]]);
  assert.equal(SIGNED_OUT_EVENT, "auth:signed-out", "the Rust side's event name");
});

test("the source imports nothing from Tauri and touches no storage, fetch or cookie", () => {
  const source = readFileSync(new URL("../src/tauri-adapter.ts", import.meta.url), "utf8");
  assert.equal(/from\s+["']@tauri-apps/.test(source), false, "no @tauri-apps import: the host injects invoke");
  assert.equal(/\b(fetch|XMLHttpRequest|localStorage|sessionStorage|indexedDB)\b\s*[.(\[]/.test(source), false);
  assert.equal(/document\.cookie/.test(source), false);
});

test("the controller signs in through the adapter and holds only the core's token", async () => {
  const { invoke, calls } = shell({
    auth_sign_in: [ok({ user: USER })],
    auth_exchange: [ok(ANSWER)],
    auth_sign_out: [ok({})],
  });
  const clock = new FakeScheduler(1_000_000_000_000);
  const recorded = new RecordedStorage();
  const sessions = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded), now: clock.now });
  const controller = new AuthController({
    account: createTauriAccountSurface({ invoke }),
    sessions,
    activeOrg: () => ORG,
    now: clock.now,
    timers: clock,
  });
  const result = await controller.signInWithPassword(USER.email, PASSWORD);
  assert.equal(result.ok, true);
  assert.equal(sessions.current.token, TOKEN);
  for (const write of recorded.writes) {
    assert.equal(write.value.includes(TOKEN), false, "the token is in memory only");
    assert.equal(write.value.includes(PASSWORD), false);
  }
  assert.deepEqual(calls.map((c) => c.command), ["auth_sign_in", "auth_exchange"]);
  await controller.signOut();
  assert.equal(sessions.current, null);
  assert.deepEqual(calls.map((c) => c.command), ["auth_sign_in", "auth_exchange", "auth_sign_out"]);
});
