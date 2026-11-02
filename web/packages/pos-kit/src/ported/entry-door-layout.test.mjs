// UX-143: one door layout for every desk's sign in and offline door. Every
// line door carries the app's name as its heading (a desk that renders its own
// title declines it), and the stage sits on the desk's own left gutter: the
// centring auto margin is gone.
import test from "node:test";
import assert from "node:assert/strict";
import { entryStyles, renderEntry } from "./entry.ts";

/** A lit template as its own source words: strings and values joined, nested templates flattened. */
function words(value) {
  if (value === null || value === undefined || typeof value === "boolean") return "";
  if (Array.isArray(value)) return value.map(words).join("");
  if (typeof value === "object" && "strings" in value) {
    return value.strings.map((s, i) => s + (i < value.values.length ? words(value.values[i]) : "")).join("");
  }
  return typeof value === "symbol" ? "" : String(value);
}
const squash = (value) => words(value).replace(/\s+/g, " ").trim();

test("each line door carries a heading with the app's name above the line", () => {
  const kinds = [
    { kind: "signin" },
    { kind: "offline", reason: "offline" },
    { kind: "loading" },
    { kind: "unavailable", status: 404 },
  ];
  for (const flows of kinds) {
    const door = squash(renderEntry({ flows, busy: false, message: null }, { app: "Money Desk" }));
    assert.match(door, /<h2 class="headline">Money Desk<\/h2>/, flows.kind);
  }
  // The line keeps its own words under the heading.
  const door = squash(renderEntry({ flows: { kind: "signin" }, busy: false, message: null }, { app: "Money Desk" }));
  assert.match(door, /Sign in to open Money Desk\./);
});

test("a desk that renders its own title declines the door's heading", () => {
  const door = squash(
    renderEntry({ flows: { kind: "signin" }, busy: false, message: null }, { app: "Dispatch Board", title: false }),
  );
  assert.doesNotMatch(door, /class="headline"/);
  assert.match(door, /Sign in to open Dispatch Board\./);
});

test("the stage sits on the desk's left gutter: no centring auto margin", () => {
  assert.doesNotMatch(entryStyles.cssText, /\.stage\s*\{[^}]*margin:\s*0 auto/);
});
