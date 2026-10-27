// The password recovery call: the console's confirmation endpoint, the exact
// body it is given, and the answer each refusal becomes. Every value is fake;
// no network is touched and no secret is ever logged.
//
// FB Console is Appwrite 2.1.0. Its confirmation route is PUT /account/recovery
// and it takes exactly three fields: userId, secret and password. Nothing else
// is sent, so nothing else can leak.
import { test } from "node:test";
import assert from "node:assert/strict";
import { createAppwriteAccountSurface } from "../src/appwrite-adapter.ts";

const ENDPOINT = "https://console.example.test/v1";
const PROJECT = "fake-project";
const CORE = "https://core.example.test";

/** A fake console that answers one recovery call and records how it was asked. */
function consoleAnswer(status, body, { throwOn = false } = {}) {
  const calls = [];
  const fetchFake = async (url, init = {}) => {
    const call = { url: String(url), method: init.method ?? "GET", headers: { ...(init.headers ?? {}) }, body: init.body };
    calls.push(call);
    if (throwOn) throw new TypeError("simulated: network down");
    return new Response(body === null ? null : JSON.stringify(body), {
      status,
      headers: { "content-type": "application/json" },
    });
  };
  const surface = createAppwriteAccountSurface({
    endpoint: ENDPOINT,
    projectId: PROJECT,
    coreBaseUrl: CORE,
    fetch: fetchFake,
    pageOrigin: "https://hh.example.test",
  });
  return { calls, surface };
}

const recoveryCall = (calls) => calls.find((c) => c.url === `${ENDPOINT}/account/recovery`);

test("completeRecovery sends PUT /account/recovery with the three fields and nothing else", async () => {
  const { calls, surface } = consoleAnswer(200, { $id: "token-1" });
  const answer = await surface.completeRecovery("user-42", "secret-abc", "a-good-password");
  assert.equal(answer.ok, true);

  const call = recoveryCall(calls);
  assert.ok(call, "the recovery endpoint was called");
  assert.equal(call.method, "PUT");
  assert.equal(call.headers["X-Appwrite-Project"], PROJECT);
  assert.deepEqual(JSON.parse(call.body), {
    userId: "user-42",
    secret: "secret-abc",
    password: "a-good-password",
  });
  assert.deepEqual(Object.keys(JSON.parse(call.body)).sort(), ["password", "secret", "userId"]);
});

test("completeRecovery answers expired for the console's invalid token", async () => {
  const { surface } = consoleAnswer(401, { message: "Invalid token", code: 401, type: "user_invalid_token" });
  const answer = await surface.completeRecovery("user-42", "stale-secret", "a-good-password");
  assert.equal(answer.ok, false);
  assert.equal(answer.code, "expired");
  assert.equal(answer.consoleType, "user_invalid_token");
});

test("completeRecovery answers password_refused and keeps the console's rule", async () => {
  const { surface } = consoleAnswer(400, { message: "Password is pwned", code: 400, type: "user_password_pwned" });
  const answer = await surface.completeRecovery("user-42", "secret-abc", "password123");
  assert.equal(answer.ok, false);
  assert.equal(answer.code, "password_refused");
  assert.equal(answer.consoleType, "user_password_pwned");
});

test("completeRecovery answers expired when the link names a user that is gone", async () => {
  const { surface } = consoleAnswer(404, { message: "User not found", code: 404, type: "user_not_found" });
  const answer = await surface.completeRecovery("user-42", "secret-abc", "a-good-password");
  assert.equal(answer.ok, false);
  assert.equal(answer.code, "expired");
  assert.equal(answer.consoleType, "user_not_found");
});

test("completeRecovery answers rate_limited for a 429", async () => {
  const { surface } = consoleAnswer(429, { message: "Too many requests", code: 429, type: "general_rate_limit_exceeded" });
  const answer = await surface.completeRecovery("user-42", "secret-abc", "a-good-password");
  assert.equal(answer.ok, false);
  assert.equal(answer.code, "rate_limited");
});

test("completeRecovery answers offline when the network is down", async () => {
  const { surface } = consoleAnswer(0, null, { throwOn: true });
  const answer = await surface.completeRecovery("user-42", "secret-abc", "a-good-password");
  assert.equal(answer.ok, false);
  assert.equal(answer.code, "offline");
});

test("completeRecovery answers unavailable for anything else the console says", async () => {
  const { surface } = consoleAnswer(500, { message: "Server error", code: 500, type: "general_internal_server_error" });
  const answer = await surface.completeRecovery("user-42", "secret-abc", "a-good-password");
  assert.equal(answer.ok, false);
  assert.equal(answer.code, "unavailable");
});

test("completeRecovery never sends an empty user id or secret", async () => {
  const { calls, surface } = consoleAnswer(200, { $id: "token-1" });
  const answer = await surface.completeRecovery("", "", "a-good-password");
  assert.equal(answer.ok, false);
  assert.equal(answer.code, "unavailable");
  assert.equal(recoveryCall(calls), undefined, "the console was never asked");
});
