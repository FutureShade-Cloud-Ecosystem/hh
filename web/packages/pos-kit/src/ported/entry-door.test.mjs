// UX-128: a dealer's own box with no session is asked to sign in; it never hears about demo roles.
// Every demo-page message stays exactly as it was.
import test from "node:test";
import assert from "node:assert/strict";
import { entryUnavailable } from "./boot-custody.ts";
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
  assert.match(text, /Sign in to open the Money Desk\./);
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
