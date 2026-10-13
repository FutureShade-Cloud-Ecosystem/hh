// The console mode's pure parts: the words of every sign in state, the
// methods route, the persona route, the configuration error page and the
// identity tap. The words are asserted here as text, because the screen's
// words are the contract (the core never explains a refusal).
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  CONFIG_ERROR_WORDS,
  PERSONA_REFUSAL_WORDS,
  SIGN_IN_WORDS,
  focusAfter,
  methodsFor,
  fetchMethods,
  enterPersona,
  tapIdentity,
  landingFor,
  signInNotice,
} from "../src/signin.js";
import { LIVE_ROLES } from "../src/roles.js";

const answer = (status, body) => async () => new Response(body === undefined ? null : JSON.stringify(body), { status });

test("the words of each sign in state are the ones the brief names", () => {
  assert.equal(signInNotice("forbidden"), "This account is not set up for this site. Ask your administrator to add you.");
  assert.equal(signInNotice("bad_credentials"), "That email and password do not match. Check them and try again.");
  assert.equal(signInNotice("rate_limited"), "Too many attempts. Wait a minute, then try again.");
  assert.equal(signInNotice("unavailable"), "Sign in is unavailable right now. Try again in a moment.");
  assert.equal(signInNotice("offline"), "You are offline. Connect to the internet, then try again.");
  assert.equal(signInNotice("no_session"), "Your session ended. Sign in again.");
  assert.equal(signInNotice("unauthorized"), "Sign in did not complete. Try again.");
  assert.equal(signInNotice("bad_request"), "Sign in did not complete. Try again.");
  assert.equal(signInNotice("failed"), "Sign in did not complete. Try again.");
});

test("a cancelled or superseded sign in says nothing", () => {
  assert.equal(signInNotice("cancelled"), "");
  assert.equal(signInNotice("superseded"), "");
});

test("every word is plain: no dash, no exclamation mark, no raw status or code", () => {
  const all = [
    ...Object.values(SIGN_IN_WORDS),
    ...Object.values(PERSONA_REFUSAL_WORDS),
    ...Object.values(CONFIG_ERROR_WORDS).flatMap((entry) => [entry.title, entry.body]),
  ];
  assert.ok(all.length > 12);
  for (const text of all) {
    assert.equal(/[–—!]/.test(text), false, text);
    assert.equal(/\b(40\d|50\d|http|json|token|exchange|jwt)\b/i.test(text), false, text);
  }
});

test("focus returns to the password after wrong credentials and to the button otherwise", () => {
  assert.equal(focusAfter("bad_credentials"), "password");
  for (const reason of ["forbidden", "rate_limited", "unavailable", "offline", "failed"]) {
    assert.equal(focusAfter(reason), "submit");
  }
});

test("the configuration error page has its own words for every refusal and never the demo's", () => {
  for (const reason of ["unreachable", "missing", "unreadable", "no_mode", "unknown_mode", "incomplete"]) {
    const entry = CONFIG_ERROR_WORDS[reason];
    assert.ok(entry && entry.title && entry.body, reason);
    assert.equal(/pick a role/i.test(entry.title + entry.body), false);
  }
});

test("methods: only email_password and google render, in the dealer's order; unknown and not yet built codes drop", () => {
  assert.deepEqual(methodsFor(["google", "email_password"]), ["google", "email_password"]);
  assert.deepEqual(methodsFor(["microsoft", "magic_link", "email_password"]), ["email_password"]);
  assert.deepEqual(methodsFor(["microsoft", "magic_link"]), []);
  assert.deepEqual(methodsFor(["email_password", "email_password", "nonsense", 7]), ["email_password"]);
});

test("the methods route is asked with the org, no credentials, and answered field by field", async () => {
  let seen;
  const result = await fetchMethods(async (url, init) => {
    seen = { url: String(url), init };
    return new Response(JSON.stringify({ methods: ["email_password", "google"] }), { status: 200 });
  }, "https://core.example.test", "gable");
  assert.deepEqual(result, { ok: true, methods: ["email_password", "google"] });
  assert.equal(seen.url, "https://core.example.test/api/v1/auth/methods?org=gable");
  assert.equal(seen.init.credentials, "omit");
  const same = await fetchMethods(async (url) => {
    seen = { url: String(url) };
    return new Response(JSON.stringify({ methods: [] }), { status: 200 });
  }, "", "a b");
  assert.equal(seen.url, "/api/v1/auth/methods?org=a%20b", "an empty base is the page's own origin, and the org is encoded");
  assert.deepEqual(same, { ok: true, methods: [] });
});

test("the methods route failing is reported, not defaulted", async () => {
  assert.deepEqual(await fetchMethods(answer(503), "", "gable"), { ok: false, reason: "unavailable" });
  assert.deepEqual(await fetchMethods(answer(200, { nope: 1 }), "", "gable"), { ok: false, reason: "unavailable" });
  assert.deepEqual(await fetchMethods(async () => { throw new TypeError("down"); }, "", "gable"), { ok: false, reason: "offline" });
});

const GOOD = {
  token: "fake-token",
  expiresAtMs: 2_000_000_000_000,
  user: { user_id: "persona:inside_sales", email: "inside-sales@hh-demo.invalid", display_name: "Sales", role_code: "inside_sales", role_name: "Inside Sales", tier: 3, scope: "BRANCH" },
};

test("a persona entry posts the persona id, with the caller's bearer only when there is one", async () => {
  let seen;
  const fetchFn = async (url, init) => {
    seen = { url: String(url), init };
    return new Response(JSON.stringify(GOOD), { status: 200 });
  };
  const open = await enterPersona(fetchFn, "", "gable", "inside_sales", null);
  assert.equal(open.ok, true);
  assert.equal(open.answer.user.user_id, "persona:inside_sales");
  assert.equal(seen.url, "/api/v1/auth/persona");
  assert.equal(seen.init.method, "POST");
  assert.deepEqual(JSON.parse(seen.init.body), { persona: "inside_sales" });
  assert.equal("Authorization" in seen.init.headers, false);
  assert.equal(seen.init.credentials, "omit");
  assert.equal(seen.init.headers["X-Org"], "gable");
  await enterPersona(fetchFn, "", "gable", "inside_sales", "fake-bearer");
  assert.equal(seen.init.headers["Authorization"], "Bearer fake-bearer");
});

test("persona refusals map to a reason by status, each with its own words", async () => {
  const cases = [[401, "unauthorized"], [403, "forbidden"], [429, "rate_limited"], [400, "bad_request"], [503, "unavailable"], [500, "unavailable"]];
  for (const [status, reason] of cases) {
    const result = await enterPersona(answer(status), "", "gable", "driver", null);
    assert.deepEqual(result, { ok: false, reason }, String(status));
    assert.ok(PERSONA_REFUSAL_WORDS[reason], reason);
  }
  assert.deepEqual(await enterPersona(async () => { throw new TypeError("down"); }, "", "gable", "driver", null), { ok: false, reason: "offline" });
  assert.deepEqual(await enterPersona(answer(200, { token: "" }), "", "gable", "driver", null), { ok: false, reason: "unavailable" });
});

test("the identity tap remembers the last exchange user block and forwards everything", async () => {
  const calls = [];
  const surface = {
    createEmailPasswordSession: async (...a) => { calls.push(["create", ...a]); return { userId: "u", email: "e", name: "n" }; },
    createOAuth2Session: async (...a) => { calls.push(["oauth", ...a]); },
    getSession: async () => ({ userId: "u", email: "e", name: "n" }),
    exchange: async (org) => { calls.push(["exchange", org]); return { ok: true, answer: GOOD }; },
    deleteSession: async () => { calls.push(["delete"]); },
  };
  const tap = tapIdentity(surface);
  assert.equal(tap.identity(), null);
  await tap.surface.createEmailPasswordSession("a@b.invalid", "pw");
  await tap.surface.createOAuth2Session("google", "https://x.test/");
  await tap.surface.exchange("gable");
  await tap.surface.deleteSession();
  assert.deepEqual(calls.map((c) => c[0]), ["create", "oauth", "exchange", "delete"]);
  assert.equal(tap.identity().role_code, "inside_sales");
  tap.forget();
  assert.equal(tap.identity(), null);
  const refused = tapIdentity({ ...surface, exchange: async () => ({ ok: false, code: "forbidden" }) });
  assert.deepEqual(await refused.surface.exchange("gable"), { ok: false, code: "forbidden" });
  assert.equal(refused.identity(), null);
});

test("landing: a role code finds its live role and anything else finds none", () => {
  for (const role of LIVE_ROLES) assert.equal(landingFor(role.id), role);
  assert.equal(landingFor("outside_sales"), null);
  assert.equal(landingFor("platform_admin"), null);
  assert.equal(landingFor(""), null);
});
