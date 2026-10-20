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
  dayPeriodInZone,
  dealerLine,
  firstNameOf,
  focusAfter,
  heldRolesFor,
  methodsFor,
  fetchMethods,
  landingFor,
  pickerGreeting,
  signInNotice,
} from "../src/signin.js";
import { LIVE_ROLES, primaryTileFor } from "../src/roles.js";
import { resetHouseZone, serveHouseZoneFrom } from "@hardscapehouse/pos-kit/src/ported/host-facts.js";

const answer = (status, body) => async () => new Response(body === undefined ? null : JSON.stringify(body), { status });

test("the words of each sign in state are the ones the brief names", () => {
  assert.equal(signInNotice("forbidden"), "This account is not set up for this site. Ask your administrator to add you.");
  assert.equal(signInNotice("bad_credentials"), "That sign in did not work. Check the email and the password and try again.");
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

test("dealerLine shows dealer and branch when configured, or dealer name alone when branch is absent", () => {
  assert.equal(dealerLine({ name: "Gable Landscape Supply", mark: "gable" }), "Gable Landscape Supply");
  assert.equal(
    dealerLine({ name: "Gable Landscape Supply", mark: "gable", branch: "Point Loma" }),
    "Gable Landscape Supply · Point Loma",
  );
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
  assert.equal(landingFor("contractor_buyer"), null);
  assert.equal(landingFor("platform_admin"), null);
  assert.equal(landingFor(""), null);
});

test("the micro apps' role table is the front door's role catalog: each live role opens exactly the apps its tiles name", async () => {
  const { APP_IDS, APP_ROLES, ROLE_FLOWS } = await import("@hardscapehouse/pos-kit/src/ported/boot-custody.js");
  const appOfTile = (tile) => {
    if (tile.name === "Dealer Admin") return "dealer-admin";
    const byHref = { "/till/": "counter-till", "/quote/": "quote-desk", "/home/": "manager-home", "/drive/": "drive-run", "/buying-and-stock/": "buying-and-stock", "/money-desk/": "money-desk", "/yard/": "yard-pick", "/dispatch/": "dispatch-board", "/messages/": "house-messages" };
    return byHref[tile.href] ?? null;
  };
  for (const appId of APP_IDS.filter((id) => id !== "role-template")) {
    const fromCatalog = LIVE_ROLES.filter((role) => role.tiles.some((tile) => appOfTile(tile) === appId)).map((role) => role.id);
    assert.deepEqual([...APP_ROLES[appId]].sort(), fromCatalog.sort(), appId);
  }
  for (const role of LIVE_ROLES) assert.equal(ROLE_FLOWS[role.id], role.flow, role.id);
});

test("firstNameOf and pickerGreeting greet by first name and period in houseZone", () => {
  resetHouseZone();
  assert.equal(firstNameOf("Partner One"), "Partner");
  assert.equal(firstNameOf("Sales manager (demo)"), "Sales");
  assert.equal(firstNameOf("   "), "");

  // 02:30 UTC is 22:30 (evening) in America/Toronto and 19:30 (evening) in America/Los_Angeles.
  const utcEarlyMorning = new Date(1_000_002_600_000);
  assert.equal(dayPeriodInZone(utcEarlyMorning, "America/Toronto"), "evening");
  assert.equal(
    pickerGreeting("Partner One", utcEarlyMorning),
    "Good evening, Partner. What are you working on today?",
  );

  // 17:30 UTC is 13:30 (afternoon) in America/Toronto, but 10:30 (morning) in America/Los_Angeles.
  const utcNoonish = new Date(1_000_056_600_000);
  assert.equal(dayPeriodInZone(utcNoonish, "America/Toronto"), "afternoon");
  serveHouseZoneFrom({ business_zone: "America/Los_Angeles" });
  assert.equal(
    pickerGreeting("Partner One", utcNoonish),
    "Good morning, Partner. What are you working on today?",
  );
  resetHouseZone();
});

test("heldRolesFor returns only the live roles held, and primaryTileFor names the primary openable app", () => {
  assert.deepEqual(
    heldRolesFor("inside_sales").map((r) => r.id),
    ["inside_sales"],
  );
  assert.deepEqual(
    heldRolesFor("general_manager,sales_manager").map((r) => r.id),
    ["general_manager", "sales_manager"],
  );
  assert.deepEqual(heldRolesFor("platform_admin"), []);
  for (const role of LIVE_ROLES) {
    const primary = primaryTileFor(role);
    assert.ok(primary && primary.href !== null, role.id);
  }
});
