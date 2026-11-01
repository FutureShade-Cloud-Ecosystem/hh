// UX-128: Manager Home on a dealer's own box with no session asks for a sign in; demo page messages are unchanged.
import test from "node:test";
import assert from "node:assert/strict";
import { renderEntry } from "../src/views.ts";

function words(value) {
  if (value === null || value === undefined || typeof value === "boolean") return "";
  if (Array.isArray(value)) return value.map(words).join("");
  if (typeof value === "object" && "strings" in value) {
    return value.strings.map((s, i) => s + (i < value.values.length ? words(value.values[i]) : "")).join("");
  }
  return typeof value === "symbol" ? "" : String(value);
}
const markup = (v) => words(v).replace(/\s+/g, " ");
const text = (v) => markup(v).replace(/<[^>]*>/g, "").trim();
const view = (flows) => ({ flows, busy: false, message: null });

test("the sign in state names Manager Home, links to the front door and never says demo", () => {
  const door = renderEntry(view({ kind: "signin" }));
  assert.match(text(door), /^Sign in to open Manager Home\./);
  assert.match(markup(door), /href="\/"/);
  assert.doesNotMatch(text(door), /demo/i);
});

test("the demo page messages are exactly as they were", () => {
  assert.equal(text(renderEntry(view({ kind: "loading" }))), "Loading the demo roles.");
  assert.equal(
    text(renderEntry(view({ kind: "unavailable", status: null }))),
    "The demo roles could not be loaded: the call did not go through. Reload to try again.",
  );
  assert.equal(
    text(renderEntry(view({ kind: "unavailable", status: 404 }))),
    "This deployment does not serve the demo sign-in, so there are no role cards to tap.",
  );
  assert.equal(text(renderEntry(view({ kind: "ready", flows: [] }))), "The demo sign-in answered with no roles: there is nothing to tap.");
});

test("Round 2: the offline state says it cannot reach the server, with no demo roles and no sign in", () => {
  for (const reason of ["offline", "unavailable", "rate_limited"]) {
    const door = renderEntry(view({ kind: "offline", reason }));
    assert.match(text(door), /Manager Home|The server/, reason);
    assert.match(text(door), /will try again on its own|It will try again on its own/, reason);
    assert.doesNotMatch(text(door), /demo|sign in|reload/i, reason);
  }
});
