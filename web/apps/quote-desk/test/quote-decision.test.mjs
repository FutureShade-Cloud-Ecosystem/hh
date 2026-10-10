// The pure side of recording the customer's decision for them (PR 247).
import assert from "node:assert/strict";
import test from "node:test";
import { acceptReady, acceptedNotice, declinedNotice, keyFor, rejectReady } from "../src/_quote-decision.ts";

test("an accept needs a known channel and the quantities statement", () => {
  assert.equal(acceptReady("phone", true), true);
  assert.equal(acceptReady("phone", false), false);
  assert.equal(acceptReady("", true), false);
  assert.equal(acceptReady("carrier_pigeon", true), false);
});

test("a decline needs a channel and a reason, and a note when the reason is something else", () => {
  assert.equal(rejectReady("text", "price", ""), true);
  assert.equal(rejectReady("text", "other", "  "), false);
  assert.equal(rejectReady("text", "other", "Bought elsewhere"), true);
  assert.equal(rejectReady("text", "", ""), false);
  assert.equal(rejectReady("", "price", ""), false);
});

test("the notices say what was recorded and that nothing was sent", () => {
  assert.match(acceptedNotice("Q-7", "phone", 12345), /Q-7 was accepted for the customer, on the phone, at \$123\.45/);
  assert.match(declinedNotice("Q-7", "in_person"), /closed as lost for the customer, in person\. Nothing was sent/);
});

test("the same intent keeps its key and a changed one gets a new key", () => {
  let n = 0;
  const fresh = () => `k${(n += 1)}`;
  const first = keyFor(null, "a", fresh);
  assert.equal(keyFor(first, "a", fresh).key, first.key);
  assert.notEqual(keyFor(first, "b", fresh).key, first.key);
});

test("the panel's static options are exactly the channels and reasons the lists name", async () => {
  const { readFileSync } = await import("node:fs");
  const { DECISION_CHANNELS, DECLINE_REASONS } = await import("../src/_quote-decision.ts");
  const source = readFileSync(new URL("../src/quote-decision-panel.ts", import.meta.url), "utf8");
  const options = [...source.matchAll(/<option value="([a-z_]+)">([^<]+)<\/option>/g)].map((m) => [m[1], m[2]]);
  for (const row of [...DECISION_CHANNELS, ...DECLINE_REASONS]) {
    assert.ok(options.some(([value, label]) => value === row.value && label === row.label), `${row.value} is offered as ${row.label}`);
  }
});
