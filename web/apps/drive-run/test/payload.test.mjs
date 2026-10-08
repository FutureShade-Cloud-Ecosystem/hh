// The read side of the money rule: whatever the server sends, the app's
// projection keeps only the fields the driver's screens name. A payload laced
// with money everywhere (the core never sends it on these reads; this is the
// second wall) comes through with none of it.
import { test } from "node:test";
import assert from "node:assert/strict";
import { toLine, toRun, toStop } from "../src/drive-api.ts";
import { MONEY_WORDS } from "./_lib/fence.mjs";

const POISON = 987654;

const rawStop = {
  id: "s1",
  stop_no: 1,
  order_no: "SO-1",
  customer_name: "Customer",
  status: "READY",
  value_cents: POISON,
  freight_cents: POISON,
  total_cents: POISON,
  freight_is_override: true,
  unit_price: POISON,
  payment_hold: { held: true, amount_due_cents: POISON },
  photos: [{ id: "p1", caption: "Drop", unit_price: POISON }],
};

const rawRun = {
  id: "r1",
  run_no: "RUN-1",
  truck_code: "FLAT-1",
  status: "DISPATCHED",
  gauge: { value_cents: POISON, freight_cents: POISON, band: "GREEN" },
  stops: [rawStop],
  stop_lines: { s1: [{ line_no: 1, sku: "X", description: "Pavers", qty: "2.000", uom: "each", unit_price: POISON, extended_cents: POISON }] },
};

function everyKeyAndValue(value, out = []) {
  if (value !== null && typeof value === "object") {
    for (const [k, v] of Object.entries(value)) {
      out.push(k);
      everyKeyAndValue(v, out);
    }
  } else {
    out.push(String(value));
  }
  return out;
}

test("a stop keeps only what the screens name and drops every money field the server might send", () => {
  const stop = toStop(rawStop);
  const seen = everyKeyAndValue(stop).join("|").toLowerCase();
  assert.ok(!seen.includes(String(POISON)), "a poisoned figure survived the projection");
  for (const word of MONEY_WORDS) assert.ok(!seen.includes(word), `the projection kept a field named ${word}`);
  assert.equal(stop.customer_name, "Customer");
  assert.deepEqual(stop.photos, [{ id: "p1", caption: "Drop" }]);
});

test("a run keeps its stops and lines and drops the gauge and every figure on a line", () => {
  const run = toRun(rawRun);
  const seen = everyKeyAndValue(run).join("|").toLowerCase();
  assert.ok(!seen.includes(String(POISON)));
  for (const word of MONEY_WORDS) assert.ok(!seen.includes(word), `the projection kept ${word}`);
  assert.equal(run.stops.length, 1);
  assert.deepEqual(run.lines.s1, [{ line_no: 1, sku: "X", description: "Pavers", qty: "2.000", uom: "each" }]);
  assert.ok(!("gauge" in run));
});

test("a line is exactly the five fields the core's driver line has, and nothing else", () => {
  assert.deepEqual(Object.keys(toLine({ line_no: 1, sku: "", description: "d", qty: "1", uom: "ea", cost: 1 })).sort(), [
    "description",
    "line_no",
    "qty",
    "sku",
    "uom",
  ]);
});

test("garbage in is empty out, never a guess", () => {
  assert.equal(toStop(null).customer_name, "");
  assert.equal(toStop("x").stop_no, 0);
  assert.deepEqual(toRun(undefined).stops, []);
  assert.deepEqual(toRun({ stops: "no", stop_lines: [] }).lines, {});
});
