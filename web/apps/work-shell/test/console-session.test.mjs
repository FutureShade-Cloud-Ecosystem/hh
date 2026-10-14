// The shell's own sign in path: the auth package's controller over the Tauri
// adapter (the four Rust commands), when the webview has an invoke. The role
// comes from the exchange answer the host's wrapper sees, because the auth
// package keeps no role state; the host holds it beside the session, in
// memory. Fakes only: no network, no keychain.
import { test } from "node:test";
import assert from "node:assert/strict";
import { createConsoleSession } from "../src/console-session.js";
import { ThrowSafeMirror, SessionStore } from "@hardscapehouse/auth";
import { fakeStorage } from "./support-data.mjs";

const NOW = Date.now();

function exchangeAnswer(role = "sales_manager") {
  return {
    token: "core-token-1",
    expiresAtMs: NOW + 3_600_000,
    user: {
      user_id: "console-user-1",
      email: "person@example.invalid",
      display_name: "A Person",
      role_code: role,
      role_name: "Sales Manager",
      tier: 2,
      scope: "BRANCH",
    },
  };
}

function fakeInvoke(script) {
  const calls = [];
  const invoke = async (command, args) => {
    calls.push(command);
    const step = script[command];
    if (step === undefined) throw { code: "unavailable" };
    if (step instanceof Error || (step && typeof step === "object" && "reject" in step)) throw step.reject;
    return typeof step === "function" ? step(args) : step;
  };
  return { invoke, calls };
}

function make(script) {
  const { invoke, calls } = fakeInvoke(script);
  const sessions = new SessionStore({ mirror: new ThrowSafeMirror("test.mirror", fakeStorage()) });
  const consoleSession = createConsoleSession({ invoke, org: "hh-demo", sessions });
  return { consoleSession, calls, sessions };
}

test("a resumed Console session carries the role from the exchange answer", async () => {
  const { consoleSession, calls } = make({
    auth_resume: { user: { user_id: "console-user-1", email: "person@example.invalid", name: "A Person" } },
    auth_exchange: (args) => {
      assert.deepEqual(args, { org: "hh-demo" });
      return exchangeAnswer("sales_manager");
    },
  });
  const session = await consoleSession.resume();
  assert.ok(session !== null);
  assert.equal(session.source, "console");
  assert.equal(session.userId, "console-user-1");
  assert.equal(session.roleCode, "sales_manager");
  assert.equal(session.roleLabel, "Sales Manager");
  assert.equal(session.token, "core-token-1");
  assert.deepEqual(calls, ["auth_resume", "auth_exchange"]);
});

test("a shell with no Console configuration answers unavailable and resumes to nothing without throwing", async () => {
  const { consoleSession } = make({ auth_resume: { reject: { code: "unavailable" } } });
  assert.equal(await consoleSession.resume(), null);
});

test("no session in the keychain resumes to nothing", async () => {
  const { consoleSession } = make({ auth_resume: { reject: { code: "no_session" } } });
  assert.equal(await consoleSession.resume(), null);
});

test("a refused exchange (unauthorized) resumes to nothing and holds no token", async () => {
  const { consoleSession, sessions } = make({
    auth_resume: { user: { user_id: "console-user-1", email: "person@example.invalid", name: "A Person" } },
    auth_exchange: { reject: { code: "unauthorized" } },
    auth_sign_out: undefined,
  });
  assert.equal(await consoleSession.resume(), null);
  assert.equal(sessions.current, null);
});

test("sign out goes through the controller: the platform session is deleted and custody cleared", async () => {
  const { consoleSession, calls, sessions } = make({
    auth_resume: { user: { user_id: "console-user-1", email: "person@example.invalid", name: "A Person" } },
    auth_exchange: exchangeAnswer(),
    auth_sign_out: null,
  });
  await consoleSession.resume();
  assert.notEqual(sessions.current, null);
  await consoleSession.signOut();
  assert.equal(sessions.current, null);
  assert.ok(calls.includes("auth_sign_out"));
});

test("the current token follows the controller, not the first exchange", async () => {
  const { consoleSession } = make({
    auth_resume: { user: { user_id: "console-user-1", email: "person@example.invalid", name: "A Person" } },
    auth_exchange: exchangeAnswer(),
  });
  const session = await consoleSession.resume();
  assert.equal(consoleSession.currentToken(), session.token);
});
