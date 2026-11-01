// UX-128: a dealer's own box with no session is asked to sign in; it never hears about demo roles.
// Every demo-page message stays exactly as it was.
import test from "node:test";
import assert from "node:assert/strict";
import { entryAfter, entryUnavailable, reasonForStatus } from "./boot-custody.ts";
import { renderEntry } from "./entry.ts";

/** A lit template as the words it prints: strings and values joined, nested templates flattened. */
function words(value) {
  if (value === null || value === undefined || typeof value === "boolean") return "";
  if (Array.isArray(value)) return value.map(words).join("");
  if (typeof value === "object" && "strings" in value) {
    return value.strings.map((s, i) => s + (i < value.values.length ? words(value.values[i]) : "")).join("");
  }
  return typeof value === "symbol" ? "" : String(value);
}
const squash = (value) => words(value).replace(/\s+/g, " ").trim();
const flat = (value) => squash(value).replace(/<[^>]*>/g, "").trim();

test("a console box with no session gets its own sign in state, not the unavailable one", () => {
  assert.equal(entryUnavailable().flows.kind, "signin");
});

test("the sign in state names the app, links to the box's front door and says nothing of demo roles", () => {
  const door = renderEntry(entryUnavailable(), { app: "Money Desk" });
  const text = flat(door);
  assert.match(text, /Sign in to open Money Desk\./);
  assert.match(squash(door), /href="\/"/);
  assert.doesNotMatch(text, /demo/i);
  assert.doesNotMatch(text, /Reload/);
  assert.match(flat(renderEntry(entryUnavailable())), /Sign in to open this app\./);
});

test("every demo page message is exactly as it was", () => {
  const view = (flows) => ({ flows, busy: false, message: null });
  assert.equal(flat(renderEntry(view({ kind: "loading" }))), "Loading the demo roles.");
  assert.equal(
    flat(renderEntry(view({ kind: "unavailable", status: 404 }))),
    "This deployment does not serve the demo sign-in, so there are no role cards to tap.",
  );
  assert.equal(
    flat(renderEntry(view({ kind: "unavailable", status: null }))),
    "The demo roles could not be loaded: the call did not go through. Reload to try again.",
  );
  assert.equal(
    flat(renderEntry(view({ kind: "unavailable", status: 500 }))),
    "The demo roles could not be loaded (HTTP 500). Reload to try again.",
  );
  assert.equal(
    flat(renderEntry(view({ kind: "ready", flows: [] }))),
    "The demo sign-in answered with no roles: there is nothing to tap.",
  );
});

// Round 2: only a person being sent to sign in reads the sign in line; a blip reads the offline line.
test("each front-door outcome gives the sign in state", () => {
  for (const why of ["no_session", "forbidden", "wrong_role", "config", "persona_refused", "ended"]) {
    assert.equal(entryAfter({ kind: "front-door", why }).flows.kind, "signin", why);
  }
});

test("each unavailable outcome gives the offline state with its reason, and the page says nothing of demo roles or sign in", () => {
  const lines = {
    offline: /Money Desk cannot reach the server just now\. It will try again on its own\./,
    unavailable: /The server is not answering just now\. Money Desk will try again on its own\./,
    rate_limited: /The server is busy just now\. Money Desk will try again on its own\./,
  };
  for (const reason of ["offline", "unavailable", "rate_limited"]) {
    const view = entryAfter({ kind: "unavailable", reason });
    assert.deepEqual(view.flows, { kind: "offline", reason });
    const text = flat(renderEntry(view, { app: "Money Desk" }));
    assert.match(text, lines[reason], reason);
    assert.doesNotMatch(text, /demo|sign in|reload/i, reason);
  }
  assert.match(flat(renderEntry(entryAfter({ kind: "unavailable", reason: "offline" }))), /^This app cannot reach the server/);
});

// UX-128 round 3 (P3-7): an app that reads the status of a failed start tells a lost signal from a 5xx and a throttle.
test("a failed start's status names the offline reason: no status is lost signal, 429 is busy, anything else is not answering", () => {
  assert.equal(reasonForStatus(undefined), "offline");
  assert.equal(reasonForStatus(429), "rate_limited");
  assert.equal(reasonForStatus(503), "unavailable");
  assert.equal(reasonForStatus(500), "unavailable");
});
