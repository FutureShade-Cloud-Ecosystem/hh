// Gap 11 of the per role homes: the yard manager's Weights pending row is the
// core's own weight.pending tile once it measures, an honest words row when it
// cannot, and no row at all for a profile with no weights work.
import { test } from "node:test";
import assert from "node:assert/strict";
import { hasWeightsWork, mergeWeights, unsourcedFor } from "../src/weights-logic.ts";
import { toWeightsPending } from "../src/weights-api.ts";

const queue = (rows, total, worst = "good") => ({ profile: "manager.yard", rows, total, worst });
const row = { source_key: "tasks.stale_picks", what: "Picks past their deadline", count: 1, age: "26h", severity: "stop", module: "tasks", unit: "yard", screen: "yard", view: "", action: "Chase" };

test("the tile on the caller's home is read by its id, state and number", () => {
  const home = { tiles: [{ tile_id: "orders.open", state: "ok", number: 12 }, { tile_id: "weight.pending", state: "ok", number: 3 }] };
  assert.deepEqual(toWeightsPending(home), { state: "ok", count: 3 });
});

test("a tile that does not measure is said as such, and a missing tile is absent", () => {
  assert.deepEqual(toWeightsPending({ tiles: [{ tile_id: "weight.pending", state: "unwired", number: null }] }), { state: "unwired", count: null });
  assert.deepEqual(toWeightsPending({ tiles: [{ tile_id: "weight.pending", state: "error" }] }), { state: "error", count: null });
  assert.deepEqual(toWeightsPending({ tiles: [{ tile_id: "weight.pending", state: "hidden" }] }), { state: "hidden", count: null });
  assert.deepEqual(toWeightsPending({ tiles: [{ tile_id: "orders.open", state: "ok", number: 1 }] }), { state: "absent", count: null });
  assert.deepEqual(toWeightsPending(null), { state: "absent", count: null });
  assert.deepEqual(toWeightsPending({ tiles: [{ tile_id: "weight.pending", state: "ok", number: "3" }] }), { state: "ok", count: null }, "a number that is not a number is not a count");
});

test("only the yard shaped profiles carry weights work", () => {
  assert.equal(hasWeightsWork("manager.yard"), true);
  assert.equal(hasWeightsWork("manager.logistics"), true);
  for (const p of ["manager.sales", "manager.procurement", "manager.payables", "manager.controller", "manager.general", "manager.branch", "manager", ""]) {
    assert.equal(hasWeightsWork(p), false, p);
  }
});

test("a measured count joins the queue as a row that links to Yard Pick, and the total counts it", () => {
  const merged = mergeWeights(queue([row], 1, "stop"), { state: "ok", count: 3 }, "manager.yard");
  assert.equal(merged.rows.length, 2);
  const added = merged.rows[1];
  assert.equal(added.source_key, "weights.pending");
  assert.equal(added.what, "Weights pending");
  assert.equal(added.count, 3);
  assert.equal(added.severity, "watch");
  assert.equal(merged.total, 4);
  assert.equal(merged.worst, "stop", "the core's worst stays the worst");
});

test("a measured zero adds no row, as the core leaves a zero out; an empty queue's worst rises to watch with the row", () => {
  assert.equal(mergeWeights(queue([row], 1), { state: "ok", count: 0 }, "manager.yard").rows.length, 1);
  const only = mergeWeights(queue([], 0, "good"), { state: "ok", count: 2 }, "manager.yard");
  assert.equal(only.worst, "watch");
  assert.equal(only.total, 2);
});

test("a profile with no weights work, or a tile off the home, adds nothing", () => {
  const base = queue([row], 1);
  assert.equal(mergeWeights(base, { state: "ok", count: 5 }, "manager.sales"), base);
  assert.equal(mergeWeights(base, { state: "hidden", count: null }, "manager.yard"), base);
  assert.equal(mergeWeights(base, { state: "absent", count: null }, "manager.yard"), base);
});

test("an unmeasurable tile keeps an honest words row, only for profiles with weights work", () => {
  assert.equal(unsourcedFor("manager.yard", { state: "unwired", count: null }).length, 1);
  assert.equal(unsourcedFor("manager.yard", { state: "error", count: null }).length, 1);
  assert.equal(unsourcedFor("manager.yard", null).length, 1, "a home that did not load is not a zero");
  assert.equal(unsourcedFor("manager.yard", { state: "ok", count: 2 }).length, 0);
  assert.equal(unsourcedFor("manager.yard", { state: "hidden", count: null }).length, 0);
  assert.equal(unsourcedFor("manager.sales", { state: "unwired", count: null }).length, 0);
  assert.match(unsourcedFor("manager.yard", { state: "unwired", count: null })[0].words, /not counted/i);
});
