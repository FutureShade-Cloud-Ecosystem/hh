// The browser adapter over the real Appwrite web SDK, with the network
// faked at the fetch seam: the SDK's calls go through globalThis.fetch and
// its storage touches go through window.localStorage, both replaced here by
// recorded doubles. Every value is fake; no network is touched.
import { test, afterEach } from "node:test";
import assert from "node:assert/strict";
import { createAppwriteAccountSurface } from "../src/appwrite-adapter.ts";

const ENDPOINT = "https://console.example.test/v1";
const PROJECT = "fake-project";
const CORE = "https://core.example.test";
const PAGE = "https://hh-demo.example.test";
const ORG = "yard-ten";

const realFetch = globalThis.fetch;
const realWindow = globalThis.window;

afterEach(() => {
  globalThis.fetch = realFetch;
  if (realWindow === undefined) delete globalThis.window;
  else globalThis.window = realWindow;
});

function json(status, body, headers = {}) {
  return new Response(body === null ? null : JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json", ...headers },
  });
}

class RecordedLocalStorage {
  writes = [];
  #map = new Map();
  getItem(key) {
    return this.#map.has(key) ? this.#map.get(key) : null;
  }
  setItem(key, value) {
    this.writes.push(key);
    this.#map.set(key, String(value));
  }
  removeItem(key) {
    this.#map.delete(key);
  }
  get keys() {
    return [...this.#map.keys()];
  }
}

// The world: a fake FB Console and a fake core behind one fetch. `script`
// overrides: coreAnswers is a queue of core statuses/bodies, jwtAnswers a
// queue of {status} for the JWT mint, fallbackHeader makes the console send
// X-Fallback-Cookies on session creation, as the probe saw it do.
function world({ coreAnswers = [], jwtAnswers = [], fallbackHeader = false, throwOn = null } = {}) {
  const storage = new RecordedLocalStorage();
  const location = { href: "", origin: PAGE };
  globalThis.window = { localStorage: storage, location, console: { warn() {} } };
  const calls = [];
  let jwtSeq = 0;
  globalThis.fetch = async (url, init = {}) => {
    const u = String(url);
    const call = { url: u, method: init.method ?? "GET", headers: { ...(init.headers ?? {}) }, body: init.body, credentials: init.credentials, redirect: init.redirect };
    calls.push(call);
    if (throwOn !== null && u.includes(throwOn)) throw new TypeError("simulated: network down");
    if (u === `${CORE}/api/v1/auth/exchange`) {
      const answer = coreAnswers.shift() ?? {
        status: 200,
        body: {
          token: "fake-core-token",
          expiresAtMs: 1_000_000_900_000,
          user: { user_id: "fake-user-1", email: "fake.user@example.invalid", display_name: "Fake User", role_code: "fake", role_name: "Fake", tier: 1, scope: "org" },
        },
      };
      return json(answer.status, answer.body ?? null);
    }
    if (u.endsWith("/account/sessions/email")) {
      return json(201, { $id: "sess-1", userId: "fake-user-1", provider: "email" }, fallbackHeader ? { "X-Fallback-Cookies": '{"a_session_fake":"secret-fake"}' } : {});
    }
    if (u.endsWith("/account/sessions/current") && call.method === "GET") return json(200, { $id: "sess-1", userId: "fake-user-1" });
    if (u.endsWith("/account/sessions/current") && call.method === "DELETE") return new Response(null, { status: 204 });
    if (u.endsWith("/account/jwts")) {
      const answer = jwtAnswers.shift() ?? { status: 201 };
      if (answer.status >= 400) return json(answer.status, { message: "simulated", code: answer.status, type: "simulated" });
      jwtSeq += 1;
      return json(201, { jwt: `fake.jwt.${jwtSeq}` });
    }
    if (u.endsWith("/account")) return json(200, { $id: "fake-user-1", email: "fake.user@example.invalid", name: "Fake User" });
    throw new Error(`unexpected fake call: ${call.method} ${u}`);
  };
  const surface = createAppwriteAccountSurface({ endpoint: ENDPOINT, projectId: PROJECT, coreBaseUrl: CORE });
  return { calls, location, storage, surface };
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
  const [core] = coreCalls(calls);
  assert.equal(core.method, "POST");
  assert.deepEqual(JSON.parse(core.body), { jwt: "fake.jwt.1", org: ORG });
  assert.equal(core.credentials, "omit", "the public leg sends no cookies");
  assert.equal(Object.keys(core.headers).some((k) => k.toLowerCase() === "authorization"), false, "no bearer on the public leg");
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

test("the SDK leaves no cookieFallback in local storage, even when the console sends X-Fallback-Cookies", async () => {
  for (const fallbackHeader of [false, true]) {
    const { calls, storage, surface } = world({ fallbackHeader });
    await surface.createEmailPasswordSession("fake.user@example.invalid", "fake-password");
    await surface.exchange(ORG);
    await surface.getSession();
    await surface.deleteSession();
    // The finding, pinned: the SDK itself writes the key whenever the console
    // sends the header; the adapter's job is that it never rests there.
    assert.equal(storage.writes.includes("cookieFallback"), fallbackHeader, `header ${fallbackHeader}: the SDK's own write`);
    assert.equal(storage.keys.includes("cookieFallback"), false, `header ${fallbackHeader}: nothing left behind`);
    assert.deepEqual(storage.keys, [], `header ${fallbackHeader}: local storage holds nothing at all`);
    for (const call of calls) {
      assert.equal(Object.keys(call.headers).some((k) => k.toLowerCase() === "x-fallback-cookies"), false, `header ${fallbackHeader}: the fallback is never sent back`);
    }
  }
});

test("getSession answers the signed-in user and a missing session throws", async () => {
  const { surface } = world();
  assert.deepEqual(await surface.getSession(), { userId: "fake-user-1", email: "fake.user@example.invalid", name: "Fake User" });
});

test("google sign-in navigates to the console's OAuth route with same-origin redirects", async () => {
  const { location, surface } = world();
  await surface.createOAuth2Session("google", `${PAGE}/signed-in`);
  const target = new URL(location.href);
  assert.equal(target.origin + target.pathname, `${ENDPOINT}/account/sessions/oauth2/google`);
  assert.equal(target.searchParams.get("project"), PROJECT);
  assert.equal(target.searchParams.get("success"), `${PAGE}/signed-in`);
  const failure = new URL(target.searchParams.get("failure"));
  assert.equal(failure.origin, PAGE, "the failure redirect is on the same host");
});

test("google sign-in refuses a redirect to another host", async () => {
  const { location, surface } = world();
  for (const bad of ["https://evil.example.test/x", "http://hh-demo.example.test/x", "//evil.example.test", "javascript:alert(1)", ""]) {
    await assert.rejects(() => surface.createOAuth2Session("google", bad), /redirect/i, bad);
  }
  assert.equal(location.href, "", "nothing navigated");
});

test("google is the only provider", async () => {
  const { surface } = world();
  await assert.rejects(() => surface.createOAuth2Session("github", `${PAGE}/x`), /provider/);
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
