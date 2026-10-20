// The two Messages notices say "it is not available yet", never "on this box" (a phrase a dealer
// has no use for). The words are read from the source: the notices sit behind a sign in.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const source = readFileSync(new URL("../src/app-root.ts", import.meta.url), "utf8");

test("the Messaging and Escalation notices end with it is not available yet", () => {
  assert.ok(source.includes("Messaging opens in the Messages app; it is not available yet."));
  assert.ok(source.includes("opens in the Messages app; it is not available yet."));
  assert.ok(source.includes("Escalation (${targetRef}) opens in the Messages app; it is not available yet."));
  assert.doesNotMatch(source, /Messages app; it is not on this box yet/);
});
