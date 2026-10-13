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
  landingFor,
  signInNotice,
} from "../src/signin.js";
import { LIVE_ROLES } from "../src/roles.js";

const answer = (status, body) => async () => new Response(body === undefined ? null : JSON.stringify(body), { status });

test("the words of each sign in state are the ones the brief names", () => {
  assert.equal(signInNotice("forbidden"), "This account is not set up for this site. Ask your administrator to add you.");
  assert.equal(signInNotice("bad_credentials"), "That email and password do not match. Check them and try again.");
  assert.equal(signInNotice("rate_limited"), "Too many attempts. Wait a moment, then try again.");
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
    assert.equal(/[\u2013\u2014!]/.test(text), false, text);
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

test("every persona refusal reason the auth package reports has its own words", () => {
  for (const reason of ["unauthorized", "forbidden", "rate_limited", "bad_request", "unavailable", "offline"]) {
    assert.ok(PERSONA_REFUSAL_WORDS[reason], reason);
  }
});

test("landing: a role code finds its live role and anything else finds none", () => {
  for (const role of LIVE_ROLES) assert.equal(landingFor(role.id), role);
  assert.equal(landingFor("outside_sales"), null);
  assert.equal(landingFor("platform_admin"), null);
  assert.equal(landingFor(""), null);
});
