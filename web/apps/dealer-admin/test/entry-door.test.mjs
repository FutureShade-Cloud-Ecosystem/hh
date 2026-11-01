// UX-128: Dealer Admin's door on a dealer's own box asks for a sign in. Its renderEntry is a private method of the
// app element, so the test reads the source: the signin state is typed, drawn through the shared line, and the demo
// message stays on the unavailable state alone.
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const source = readFileSync(new URL("../src/app-root.ts", import.meta.url), "utf8");
const door = source.slice(source.indexOf("private renderEntry()"), source.indexOf("private renderEntry()") + 1200);

test("the signin state is part of the entry view type and is drawn by the shared sign in line", () => {
  assert.match(source, /flows: \{ kind: "loading" \} \| \{ kind: "unavailable" \} \| \{ kind: "signin" \}/);
  assert.match(door, /view\.flows\.kind === "signin"[\s\S]*signInLine\("Dealer Admin"/);
});

test("the demo roles words stay on the unavailable state only", () => {
  const signin = door.slice(door.indexOf('"signin"'), door.indexOf('"unavailable"'));
  assert.doesNotMatch(signin, /demo/i);
  assert.match(door, /The demo roles could not be loaded\. Reload to try again\./);
});

test("Round 2: the offline state is typed and drawn by the shared offline line, with no demo words", () => {
  assert.match(source, /\{ kind: "offline"; reason: "offline" \| "unavailable" \| "rate_limited" \}/);
  assert.match(door, /view\.flows\.kind === "offline"[\s\S]*offlineLine\("Dealer Admin"/);
});
