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
import { toChange, toChangeAnswer, toLine, toPick, toSkidAnswer, toTask, toTicket } from "../src/yard-api.ts";
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
  hold_words: "Balance due",
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
  assert.equal(ticket.hold_words, "Balance due");
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

// The change queue (stage 2): a row of "changes waiting on the yard" carries
// the priced delta for other callers; the yard's projection keeps the facts
// the row answers on and drops every figure, the same wall the ticket and the
// skid answer sit behind.

const rawQueueItem = {
  id: "5c000000-0000-4000-9000-000000000001",
  change_no: "CO-2042",
  order_id: "0b000000-0000-4000-9000-000000000042",
  order_no: "SO-1042",
  branch_id: "5d1b3f6e-0000-4000-9000-000000000001",
  status: "PENDING_YARD",
  reason: "Customer added two more pallets",
  delta_cents: POISON,
  finance_threshold_cents: POISON,
  finance_required: true,
  requested_by_role: "Inside Sales",
  requested_at: "2026-01-01T00:00:00Z",
  yard_ack_note: "",
  next_actions: ["yard_ack", "reject", "cancel"],
};

test("a change queue row keeps the facts the yard answers on and drops the priced delta", () => {
  const change = toChange(rawQueueItem);
  assert.ok(!everyKeyAndValue(change).join("|").includes(String(POISON)), "a poisoned figure survived the projection");
  const keys = everyKey(change).join("|").toLowerCase();
  for (const word of MONEY_WORDS) assert.ok(!keys.includes(word), `the projection kept a field named ${word}`);
  assert.deepEqual(Object.keys(change).sort(), [
    "change_no",
    "id",
    "next_actions",
    "order_no",
    "reason",
    "requested_at",
    "requested_by_role",
    "status",
  ]);
  assert.equal(change.status, "PENDING_YARD");
  assert.equal(change.reason, "Customer added two more pallets");
  assert.deepEqual(change.next_actions, ["yard_ack", "reject", "cancel"]);
});

const rawAckAnswer = {
  id: "5c000000-0000-4000-9000-000000000001",
  order_id: "0b000000-0000-4000-9000-000000000042",
  branch_id: "5d1b3f6e-0000-4000-9000-000000000001",
  change_no: "CO-2042",
  status: "PENDING_FINANCE",
  reason: "Customer added two more pallets",
  delta_cents: POISON,
  finance_threshold_cents: POISON,
  finance_required: true,
  order_status_at_request: "READY",
  pick_started_at_request: true,
  requested_by: "d0100000-0000-4000-9000-000000000001",
  requested_at: "2026-01-01T00:00:00Z",
  yard_acked_by: "d0200000-0000-4000-9000-000000000002",
  yard_acked_at: "2026-01-01T01:00:00Z",
  lines: [
    {
      seq: 1,
      op: "QTY",
      line_no: 1,
      product_id: "1a000000-0000-4000-9000-0000000000a1",
      product_name: "Charcoal pavers",
      description: "Charcoal pavers, 12 by 12 in",
      qty_base_before: 24,
      qty_base_after: 72,
      qty_delta_base: 48,
      unit_price_cents: POISON,
      price_uom: "EA",
      line_delta_cents: POISON,
    },
  ],
  next_actions: ["reject", "cancel"],
};

test("the acknowledgement's answer carries the new stage and no priced field", () => {
  const answer = toChangeAnswer(rawAckAnswer);
  assert.ok(!everyKeyAndValue(answer).join("|").includes(String(POISON)), "a poisoned figure survived the projection");
  const keys = everyKey(answer).join("|").toLowerCase();
  for (const word of MONEY_WORDS) assert.ok(!keys.includes(word), `the projection kept ${word}`);
  assert.deepEqual(Object.keys(answer).sort(), ["change_no", "id", "order_id", "status"]);
  assert.equal(answer.status, "PENDING_FINANCE");
});

test("garbage in is empty out on the change reads too", () => {
  assert.equal(toChange(null).id, "");
  assert.equal(toChange("x").status, "");
  assert.deepEqual(toChange(undefined).next_actions, []);
  assert.deepEqual(toChangeAnswer(null), { id: "", change_no: "", order_id: "", status: "" });
});
