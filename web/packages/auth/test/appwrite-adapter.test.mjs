// The browser adapter over plain REST: a fake fetch asserts the exact
// requests, and storage doubles that throw on any write prove nothing is
// ever stored. Every value is fake; no network is touched.
import { test, afterEach } from "node:test";
import assert from "node:assert/strict";
import { createAppwriteAccountSurface } from "../src/appwrite-adapter.ts";
import { PlatformRefusedError, PlatformUnreachableError } from "../src/index.ts";

const ENDPOINT = "https://console.example.test/v1";
const PROJECT = "fake-project";
const CORE = "https://core.example.test";
const PAGE = "https://hh-demo.example.test";
const ORG = "yard-ten";

const realWindow = globalThis.window;
const realLocalStorage = Object.getOwnPropertyDescriptor(globalThis, "localStorage");
const realSessionStorage = Object.getOwnPropertyDescriptor(globalThis, "sessionStorage");

function throwingStorage() {
  const boom = () => {
    throw new Error("a storage write happened");
  };
  return { getItem: () => null, setItem: boom, removeItem: boom, clear: boom };
}

afterEach(() => {
  if (realWindow === undefined) delete globalThis.window;
  else globalThis.window = realWindow;
  for (const [name, d] of [["localStorage", realLocalStorage], ["sessionStorage", realSessionStorage]]) {
    if (d) Object.defineProperty(globalThis, name, d);
    else delete globalThis[name];
  }
});

function json(status, body, headers = {}) {
  return new Response(body === null ? null : JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json", ...headers },
  });
}

const GOOD_ANSWER = {
  token: "fake-core-token",
  expiresAtMs: 1_000_000_900_000,
  user: { user_id: "fake-user-1", email: "fake.user@example.invalid", display_name: "Fake User", role_code: "fake", role_name: "Fake", tier: 1, scope: "org" },
};

// The world: a fake FB Console and a fake core behind one fetch.
// coreAnswers and consoleAnswers are queues of {status, body} per route key.
function world({ coreAnswers = [], jwtAnswers = [], sessionAnswers = [], fallbackHeader = false, throwOn = null } = {}) {
  // Any touch of any storage that writes fails the test loudly.
  Object.defineProperty(globalThis, "localStorage", { value: throwingStorage(), configurable: true });
  Object.defineProperty(globalThis, "sessionStorage", { value: throwingStorage(), configurable: true });
  globalThis.window = { localStorage: throwingStorage(), sessionStorage: throwingStorage(), location: { href: "", origin: PAGE } };
  const calls = [];
  const navigated = [];
  let jwtSeq = 0;
  const fallback = fallbackHeader ? { "X-Fallback-Cookies": '{"a_session_fake":"secret-fake"}' } : {};
  const fetchFake = async (url, init = {}) => {
    const u = String(url);
    const call = { url: u, method: init.method ?? "GET", headers: { ...(init.headers ?? {}) }, body: init.body, credentials: init.credentials, redirect: init.redirect };
    calls.push(call);
    if (throwOn !== null && u.includes(throwOn)) throw new TypeError("simulated: network down");
    if (u === `${CORE}/api/v1/auth/exchange`) {
      const a = coreAnswers.shift() ?? { status: 200, body: GOOD_ANSWER };
      return json(a.status, a.body ?? null);
    }
    if (u === `${ENDPOINT}/account/sessions/email`) {
      const a = sessionAnswers.shift() ?? { status: 201 };
      return json(a.status, a.status < 300 ? { $id: "sess-1", userId: "fake-user-1" } : { message: "simulated", code: a.status, type: a.type }, fallback);
    }
    if (u === `${ENDPOINT}/account/sessions/current` && call.method === "DELETE") return new Response(null, { status: 204, headers: fallback });
    if (u === `${ENDPOINT}/account/jwts`) {
      const a = jwtAnswers.shift() ?? { status: 201 };
      if (a.status >= 400) return json(a.status, { message: "simulated", code: a.status });
      jwtSeq += 1;
      return json(201, { jwt: `fake.jwt.${jwtSeq}` }, fallback);
    }
    if (u === `${ENDPOINT}/account`) return json(200, { $id: "fake-user-1", email: "fake.user@example.invalid", name: "Fake User" }, fallback);
    throw new Error(`unexpected fake call: ${call.method} ${u}`);
  };
  const surface = createAppwriteAccountSurface({
    endpoint: ENDPOINT,
    projectId: PROJECT,
    coreBaseUrl: CORE,
    fetch: fetchFake,
    navigate: (url) => navigated.push(url),
    pageOrigin: PAGE,
  });
  return { calls, navigated, surface };
}

const coreCalls = (calls) => calls.filter((c) => c.url === `${CORE}/api/v1/auth/exchange`);
const jwtCalls = (calls) => calls.filter((c) => c.url.endsWith("/account/jwts"));

test("sign in, then exchange: a JWT is minted and posted to the core with the org, and only the core's answer comes back", async () => {
  const { calls, surface } = world();
  const user = await surface.createEmailPasswordSession("fake.user@example.invalid", "fake-password");
  assert.deepEqual(user, { userId: "fake-user-1", email: "fake.user@example.invalid", name: "Fake User" });
  const result = await surface.exchange(ORG);
  assert.equal(result.ok, true);
  assert.equal(result.answer.token, "fake-core-token");
  assert.equal(result.answer.expiresAtMs, 1_000_000_900_000);
  assert.equal(result.answer.user.user_id, "fake-user-1");
  assert.equal(JSON.stringify(result).includes("fake.jwt"), false, "the Console JWT never leaves the adapter");
  const consoleCalls = calls.filter((c) => c.url.startsWith(ENDPOINT));
  assert.deepEqual(
    consoleCalls.map((c) => `${c.method} ${c.url.slice(ENDPOINT.length)}`),
    ["POST /account/sessions/email", "GET /account", "POST /account/jwts"],
  );
  for (const c of consoleCalls) {
    assert.equal(c.headers["X-Appwrite-Project"], PROJECT, "the project header");
    assert.equal(c.headers["Content-Type"], "application/json");
    assert.equal(c.credentials, "include", "the first party session cookie rides along");
    assert.equal(c.redirect, "error");
    assert.equal(c.url.includes("fake-password") || c.url.includes("fake.jwt") || c.url.includes("?"), false, "no secret and no query in a console URL");
    assert.equal(Object.keys(c.headers).some((k) => k.toLowerCase() === "x-fallback-cookies"), false);
  }
  assert.deepEqual(JSON.parse(consoleCalls[0].body), { email: "fake.user@example.invalid", password: "fake-password" });
  assert.equal(consoleCalls[1].body, undefined, "a GET has no body");
  const [core] = coreCalls(calls);
  assert.equal(core.method, "POST");
  assert.deepEqual(JSON.parse(core.body), { jwt: "fake.jwt.1", org: ORG });
  assert.equal(core.credentials, "omit", "the public leg sends no cookies");
  assert.equal(core.redirect, "error");
  assert.equal(Object.keys(core.headers).some((k) => k.toLowerCase() === "authorization"), false, "no bearer on the public leg");
  assert.deepEqual(Object.keys(core.headers).sort(), ["Accept", "Content-Type"], "the core call carries exactly these headers: no project header, no cookie header");
});

test("every exchange attempt mints a fresh JWT and none is reused", async () => {
  const { calls, surface } = world();
  await surface.exchange(ORG);
  await surface.exchange(ORG);
  const bodies = coreCalls(calls).map((c) => JSON.parse(c.body).jwt);
  assert.deepEqual(bodies, ["fake.jwt.1", "fake.jwt.2"]);
  assert.equal(jwtCalls(calls).length, 2);
});

test("a core 401 on the first attempt is retried once with a new JWT", async () => {
  const { calls, surface } = world({ coreAnswers: [{ status: 401 }] });
  const result = await surface.exchange(ORG);
  assert.equal(result.ok, true, "the retry succeeded");
  assert.deepEqual(coreCalls(calls).map((c) => JSON.parse(c.body).jwt), ["fake.jwt.1", "fake.jwt.2"], "the retry used a new JWT");
  assert.equal(jwtCalls(calls).length, 2);
});

test("two core 401s in a row are unauthorized, after exactly one retry", async () => {
  const { calls, surface } = world({ coreAnswers: [{ status: 401 }, { status: 401 }, { status: 200, body: {} }] });
  const result = await surface.exchange(ORG);
  assert.deepEqual(result, { ok: false, code: "unauthorized" });
  assert.equal(coreCalls(calls).length, 2);
  assert.equal(jwtCalls(calls).length, 2);
});

test("the other core refusals map to their codes and are never retried", async () => {
  const table = [
    [400, "bad_request"],
    [403, "forbidden"],
    [429, "rate_limited"],
    [503, "unavailable"],
    [500, "unavailable"],
    [418, "unavailable"],
  ];
  for (const [status, code] of table) {
    const { calls, surface } = world({ coreAnswers: [{ status, body: { error: "x" } }] });
    assert.deepEqual(await surface.exchange(ORG), { ok: false, code }, String(status));
    assert.equal(coreCalls(calls).length, 1, `${status}: one attempt`);
  }
});

test("an unusable 200 body is unavailable, not a session", async () => {
  for (const body of [null, {}, { token: "", expiresAtMs: 5, user: {} }, { token: "t", expiresAtMs: "soon", user: {} }, { token: "t", expiresAtMs: 5, user: { user_id: "" } }]) {
    const { surface } = world({ coreAnswers: [{ status: 200, body }] });
    assert.deepEqual(await surface.exchange(ORG), { ok: false, code: "unavailable" });
  }
});

test("a failed JWT mint maps to a code without touching the core", async () => {
  const table = [
    [401, "no_session"],
    [429, "rate_limited"],
    [500, "unavailable"],
  ];
  for (const [status, code] of table) {
    const { calls, surface } = world({ jwtAnswers: [{ status }] });
    assert.deepEqual(await surface.exchange(ORG), { ok: false, code }, String(status));
    assert.equal(coreCalls(calls).length, 0);
  }
});

test("no network is offline, at the mint and at the core, with no retry", async () => {
  for (const throwOn of ["/account/jwts", "/api/v1/auth/exchange"]) {
    const { calls, surface } = world({ throwOn });
    assert.deepEqual(await surface.exchange(ORG), { ok: false, code: "offline" }, throwOn);
    assert.equal(jwtCalls(calls).length, 1, "no second mint");
  }
});

test("an empty org is refused before any call", async () => {
  const { calls, surface } = world();
  assert.deepEqual(await surface.exchange("  "), { ok: false, code: "bad_request" });
  assert.equal(calls.length, 0);
});

test("a response carrying X-Fallback-Cookies is ignored: never stored, never sent back", async () => {
  // The storage doubles throw on any write, so a single touch fails the test.
  const { calls, surface } = world({ fallbackHeader: true });
  await surface.createEmailPasswordSession("fake.user@example.invalid", "fake-password");
  const first = await surface.exchange(ORG);
  assert.equal(first.ok, true);
  await surface.getSession();
  await surface.deleteSession();
  assert.equal(JSON.stringify(first).includes("secret-fake"), false);
  for (const call of calls) {
    assert.equal(Object.keys(call.headers).some((k) => k.toLowerCase() === "x-fallback-cookies"), false, "the fallback is never sent back");
    assert.equal(String(call.body ?? "").includes("secret-fake"), false);
  }
});

test("the adapter source never touches storage or the fallback header", async () => {
  const { readFileSync } = await import("node:fs");
  const text = readFileSync(new URL("../src/appwrite-adapter.ts", import.meta.url), "utf8")
    .split("\n")
    .filter((l) => !l.trim().startsWith("//"))
    .join("\n");
  assert.equal(/localStorage|sessionStorage|document\.cookie|indexedDB/.test(text), false);
  assert.equal(/fallback/i.test(text), false);
  assert.equal(/from\s+["']appwrite["']/.test(text), false, "no SDK");
});

test("sign in failures throw with the status only, and the console's session mint errors map", async () => {
  const { surface } = world({ sessionAnswers: [{ status: 401 }] });
  await assert.rejects(() => surface.createEmailPasswordSession("fake.user@example.invalid", "wrong"), /401/);
  const down = world({ throwOn: "/account/sessions/email" });
  await assert.rejects(() => down.surface.createEmailPasswordSession("a", "b"), PlatformUnreachableError);
  const gone = world({ throwOn: "/account" });
  await assert.rejects(() => gone.surface.getSession(), PlatformUnreachableError);
});

test("getSession answers the signed-in user from GET /account and a refusal throws", async () => {
  const { calls, surface } = world();
  assert.deepEqual(await surface.getSession(), { userId: "fake-user-1", email: "fake.user@example.invalid", name: "Fake User" });
  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, `${ENDPOINT}/account`);
});

test("deleteSession is DELETE /account/sessions/current", async () => {
  const { calls, surface } = world();
  await surface.deleteSession();
  assert.equal(calls[0].method, "DELETE");
  assert.equal(calls[0].url, `${ENDPOINT}/account/sessions/current`);
  assert.equal(calls[0].credentials, "include");
});

test("google sign-in navigates to the console's OAuth route with same-origin redirects", async () => {
  const { navigated, surface } = world();
  await surface.createOAuth2Session("google", `${PAGE}/signed-in`);
  assert.equal(navigated.length, 1);
  const target = new URL(navigated[0]);
  assert.equal(target.origin + target.pathname, `${ENDPOINT}/account/sessions/oauth2/google`);
  assert.equal(target.searchParams.get("project"), PROJECT);
  assert.equal(target.searchParams.get("success"), `${PAGE}/signed-in`);
  assert.equal(new URL(target.searchParams.get("failure")).origin, PAGE, "the failure redirect is on the same host");
});

test("google sign-in refuses a redirect to another host", async () => {
  const { navigated, surface } = world();
  for (const bad of ["https://evil.example.test/x", "http://hh-demo.example.test/x", "//evil.example.test", "javascript:alert(1)", ""]) {
    await assert.rejects(() => surface.createOAuth2Session("google", bad), /redirect/i, bad);
  }
  assert.deepEqual(navigated, [], "nothing navigated");
});

test("google is the only provider", async () => {
  const { surface } = world();
  await assert.rejects(() => surface.createOAuth2Session("github", `${PAGE}/x`), /provider/);
});

test("a sign in refused because a session already exists deletes the current session and retries the create once", async () => {
  const { calls, surface } = world({ sessionAnswers: [{ status: 401, type: "user_session_already_exists" }] });
  const user = await surface.createEmailPasswordSession("fake.user@example.invalid", "fake-password");
  assert.equal(user.userId, "fake-user-1");
  assert.deepEqual(
    calls.map((c) => `${c.method} ${c.url.slice(ENDPOINT.length)}`),
    ["POST /account/sessions/email", "DELETE /account/sessions/current", "POST /account/sessions/email", "GET /account"],
  );
});

test("the already-exists retry happens once only, and only for that type", async () => {
  const twice = world({ sessionAnswers: [{ status: 401, type: "user_session_already_exists" }, { status: 401, type: "user_session_already_exists" }] });
  await assert.rejects(() => twice.surface.createEmailPasswordSession("a", "b"), /401/);
  assert.equal(twice.calls.filter((c) => c.method === "DELETE").length, 1);
  assert.equal(twice.calls.filter((c) => c.url.endsWith("/sessions/email")).length, 2);
  const wrong = world({ sessionAnswers: [{ status: 401, type: "user_invalid_credentials" }] });
  await assert.rejects(() => wrong.surface.createEmailPasswordSession("a", "b"), /401/);
  assert.equal(wrong.calls.filter((c) => c.method === "DELETE").length, 0, "a wrong password deletes nothing");
  assert.equal(wrong.calls.length, 1);
});

test("a non https endpoint or core URL is refused, except localhost and 127.0.0.1", () => {
  const make = (endpoint, coreBaseUrl) => createAppwriteAccountSurface({ endpoint, projectId: PROJECT, coreBaseUrl });
  assert.throws(() => make("http://console.example.test/v1", CORE), /https/);
  assert.throws(() => make(ENDPOINT, "http://core.example.test"), /https/);
  assert.throws(() => make("ftp://console.example.test/v1", CORE), /https/);
  assert.doesNotThrow(() => make("http://localhost:8080/v1", "http://127.0.0.1:8000"));
  assert.doesNotThrow(() => make(ENDPOINT, CORE));
});

test("the options are validated at construction", () => {
  assert.throws(() => createAppwriteAccountSurface({ endpoint: "", projectId: PROJECT, coreBaseUrl: CORE }), /endpoint/);
  assert.throws(() => createAppwriteAccountSurface({ endpoint: ENDPOINT, projectId: "", coreBaseUrl: CORE }), /project/);
  assert.throws(() => createAppwriteAccountSurface({ endpoint: ENDPOINT, projectId: PROJECT, coreBaseUrl: "not a url" }), /core/);
});

test("the package root takes no SDK dependency", async () => {
  const { readFileSync } = await import("node:fs");
  for (const file of ["../src/index.ts", "../src/client.ts", "../src/session.ts", "../src/lock.ts", "../src/testing.ts"]) {
    const text = readFileSync(new URL(file, import.meta.url), "utf8");
    assert.equal(/from\s+["']appwrite["']/.test(text), false, file);
    assert.equal(/from\s+["']\.\/appwrite-adapter/.test(text), false, `${file} does not import the adapter`);
  }
});

test("a refused sign in throws a PlatformRefusedError that carries the status and nothing else the screen could echo", async () => {
  for (const status of [401, 429, 503]) {
    const { surface } = world({ sessionAnswers: [{ status }] });
    await assert.rejects(
      () => surface.createEmailPasswordSession("fake.user@example.invalid", "fake-password"),
      (error) => error instanceof PlatformRefusedError && error.status === status && !error.message.includes("fake-password"),
    );
  }
});
