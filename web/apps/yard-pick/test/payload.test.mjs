// The read side of the money rule: whatever the server sends, the app's
// projection keeps only the fields the yard's screens name. A payload laced
// with money everywhere (the core holds cost out of the yard's reads; this
// is the second wall) comes through with none of it. Two contract-declared
// money fields are named here because they are the ones the wire really
// carries: the ticket's still_owed_cents on a held order and the skid
// answer's deposit figures. The projection drops both; the yard reads the
// hold's words and the count.
import { test } from "node:test";
import assert from "node:assert/strict";
import { toLine, toPick, toSkidAnswer, toTask, toTicket } from "../src/yard-api.ts";
import { MONEY_WORDS } from "./_lib/fence.mjs";

const POISON = 987654;

const rawLine = {
  line_no: 1,
  sku: "PAVER-CH-1212",
  description: "Charcoal pavers",
  qty_ordered: "24.0000",
  uom: "EA",
  avg_cost_cents: POISON,
  unit_cost_cents: POISON,
  unit_price: POISON,
  margin: POISON,
};

const rawTicket = {
  order_id: "o1",
  order_no: "SO-1042",
  customer_name: "Customer",
  fulfillment: "PICKUP",
  priority: "URGENT",
  source: "DISPATCH",
  held_from_picking: true,
  hold_words: "Balance due: this order waits until the balance is paid.",
  still_owed_cents: POISON,
  lines: [rawLine],
};

const rawTask = {
  id: "t1",
  task_type: "PICK",
  order_id: "o1",
  priority: "URGENT",
  status: "QUEUED",
  source: "MANUAL",
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
  open_shortages: 0,
  total_shortages: 0,
  value_cents: POISON,
  total_cents: POISON,
};

const rawPick = {
  task: rawTask,
  ticket: rawTicket,
  shortages: [{ id: "s1", task_id: "t1", description: "Flagstone", qty_short: "2.0000", uom: "SQFT", reported_at: "2026-01-01T00:00:00Z", unit_price: POISON }],
  picked: [{ id: "p1", task_id: "t1", order_line_no: 1, qty_ordered: "24.0000", qty_picked: "14.0000", uom: "EA", picked_at: "2026-01-01T00:00:00Z", extended_cents: POISON }],
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

/** The KEYS the projection kept: the money fence scans keys, a word inside a sentence is data. */
function everyKey(value, out = []) {
  if (value !== null && typeof value === "object") {
    for (const [k, v] of Object.entries(value)) {
      out.push(k);
      everyKey(v, out);
    }
  }
  return out;
}

test("a ticket keeps the hold's words and drops the owed figure and every cost on a line", () => {
  const ticket = toTicket(rawTicket);
  assert.ok(!everyKeyAndValue(ticket).join("|").includes(String(POISON)), "a poisoned figure survived the projection");
  const keys = everyKey(ticket).join("|").toLowerCase();
  for (const word of MONEY_WORDS) assert.ok(!keys.includes(word), `the projection kept a field named ${word}`);
  assert.equal(ticket.hold_words, "Balance due: this order waits until the balance is paid.");
  assert.equal(ticket.held_from_picking, true);
  assert.equal(ticket.lines.length, 1);
});

test("a pick keeps its task, shortages and pulled lines and drops every figure the server might send", () => {
  const pick = toPick(rawPick);
  assert.ok(!everyKeyAndValue(pick).join("|").includes(String(POISON)));
  const keys = everyKey(pick).join("|").toLowerCase();
  for (const word of MONEY_WORDS) assert.ok(!keys.includes(word), `the projection kept ${word}`);
  assert.equal(pick.task.status, "QUEUED");
  assert.equal(pick.shortages.length, 1);
  assert.equal(pick.picked.length, 1);
});

test("the skid answer keeps the count and drops the deposit figures the contract declares", () => {
  const answer = toSkidAnswer({ skid_count: 5, previous_skid_count: 2, deposit_cents: POISON, previous_deposit_cents: POISON, changed: true, order_no: "SO-1042" });
  assert.ok(!everyKeyAndValue(answer).join("|").includes(String(POISON)));
  const keys = everyKey(answer).join("|").toLowerCase();
  for (const word of MONEY_WORDS) assert.ok(!keys.includes(word), `the projection kept ${word}`);
  assert.deepEqual(answer, { skid_count: 5, previous_skid_count: 2, changed: true, order_no: "SO-1042" });
});

test("a line is exactly the eight fields the yard's pull list names, and nothing else", () => {
  assert.deepEqual(Object.keys(toLine({ line_no: 1, sku: "x", description: "d", qty_ordered: "1", uom: "ea", avg_cost_cents: 1 })).sort(), [
    "description",
    "line_no",
    "location",
    "product_id",
    "qty_backordered",
    "qty_ordered",
    "sku",
    "uom",
  ]);
});

test("a task is the narrow shape the board reads, and a null ticket stays null", () => {
  assert.deepEqual(Object.keys(toTask(rawTask)).sort(), [
    "claimed_by",
    "created_at",
    "id",
    "open_shortages",
    "order_id",
    "priority",
    "stage_location",
    "stage_ref",
    "status",
    "total_shortages",
    "updated_at",
  ]);
  assert.equal(toTicket(null), null);
  assert.equal(toTicket(undefined), null);
});

test("garbage in is empty out, never a guess", () => {
  assert.equal(toTask(null).id, "");
  assert.equal(toTask("x").status, "");
  assert.deepEqual(toPick({ task: "no" }).shortages, []);
  assert.deepEqual(toPick(undefined).picked, []);
  assert.deepEqual(toSkidAnswer("x").skid_count, 0);
});
