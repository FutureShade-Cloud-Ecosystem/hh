// The contractor's board vocabulary: which stage an order, a project or a
// quote sits in and what its tracking reads. The tables are pinned to the
// contract's OrderCard x-state-map (core/api/openapi.yaml), re-read on every
// run, so a state the core adds and this file misses fails here.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { stageOfOrder, stageOfProject, stageOfQuote, trackingOf, TRACKING_WORDS, TRACKING_STATES } from "../src/board.ts";

const openapi = readFileSync(fileURLToPath(new URL("../../../../core/api/openapi.yaml", import.meta.url)), "utf8");
const block = (from, to) => {
  const start = openapi.indexOf(from);
  assert.ok(start > 0, `${from} was not found in the contract`);
  return openapi.slice(start, openapi.indexOf(to, start));
};

test("every dealer order state maps to the stage the contract names", () => {
  const map = block("        dealer_order_to_stage:", "        stage_sources:");
  const pairs = [...map.matchAll(/^\s{10}([A-Z_]+): (plan|quote|order|invoice)$/gm)];
  assert.equal(pairs.length, 9, "the contract maps nine order states");
  for (const [, status, stage] of pairs) assert.equal(stageOfOrder(status), stage, status);
});

test("an order state the table does not know has no stage rather than a wrong one", () => {
  assert.equal(stageOfOrder("SOMETHING_NEW"), null);
});

test("a planning project is in Plan, a draft or sent quote is in Quote, nothing else is claimed", () => {
  assert.equal(stageOfProject("PLANNING"), "plan");
  assert.equal(stageOfProject("ORDERED"), null);
  assert.equal(stageOfQuote("DRAFT"), "quote");
  assert.equal(stageOfQuote("SENT"), "quote");
  assert.equal(stageOfQuote("ACCEPTED"), null);
});

test("the tracking vocabulary equals the contract's, each with a plain word", () => {
  const union = block("        supplier_status:\n          type:", "        fulfillment:\n          type:");
  const states = [...union.matchAll(/^\s+- ([a-z-]+)$/gm)].map((m) => m[1]).filter((v) => v !== "string" && v !== "null");
  assert.deepEqual([...TRACKING_STATES], states);
  for (const state of states) assert.match(TRACKING_WORDS[state], /^[A-Z][a-z]/, `${state} reads as a sentence case word`);
});

test("tracking follows the contract's map from order state and fulfilment", () => {
  assert.equal(trackingOf("DRAFT", "DELIVERY"), "submitted");
  assert.equal(trackingOf("CONFIRMED", "DELIVERY"), "confirmed");
  assert.equal(trackingOf("PICKING", "PICKUP"), "picking");
  assert.equal(trackingOf("READY", "PICKUP"), "ready-willcall");
  assert.equal(trackingOf("READY", "WILL_CALL"), "ready-willcall");
  assert.equal(trackingOf("FULFILLED", "DELIVERY"), "delivered");
  assert.equal(trackingOf("INVOICED", "DELIVERY"), "invoiced");
  assert.equal(trackingOf("CLOSED", "PICKUP"), "invoiced");
  assert.equal(trackingOf("CANCELLED", "DELIVERY"), "cancelled");
});

test("a state the contract leaves to the delivery stop reads as no tracking, never a guess", () => {
  // out-for-delivery comes from a stop that is en route, which no tenant route serves.
  assert.equal(trackingOf("READY", "DELIVERY"), null);
  assert.equal(trackingOf("PARTIALLY_FULFILLED", "DELIVERY"), null);
  assert.equal(trackingOf("MYSTERY", "DELIVERY"), null);
});
