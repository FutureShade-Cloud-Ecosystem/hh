// UX-136: a resolved shortage stays on its pick's card, with its words, until the sheet is next opened.
// Pure decisions: the resolve answer merges into the entry the sheet renders; a card whose last open shortage
// was resolved is still shown; the header count and the card's words stay true.
import { test } from "node:test";
import assert from "node:assert/strict";
import { cardsToShow, headCountWords, settleShortage } from "../src/manager-logic.ts";

const short = (id, over = {}) => ({
  id, task_id: "t1", order_line_no: 1, sku: "G", description: `Item ${id}`, qty_short: "4.0000", uom: "CYD",
  note: "", reported_at: "2030-01-02T10:30:00Z", resolved_at: null, resolution: "", ...over,
});
const entry = (shortages, id = "t1") => ({
  task: { id, order_id: "o1", task_type: "PICK", status: "IN_PROGRESS", priority: "NORMAL", branch_id: "b",
    open_shortages: shortages.filter((s) => s.resolved_at === null).length, total_shortages: shortages.length,
    stage_location: "", stage_ref: "", created_at: "", accept_deadline_at: null },
  order_no: "SO-1", customer_name: "", lines: [], shortages,
});
const answer = (id) => short(id, { resolved_at: "2030-01-02T11:00:00Z", resolution: "Backordered the shortfall." });

test("a resolve answer merges into its pick: the row turns done in place, with its words, and the count drops", () => {
  const entries = [entry([short("a"), short("b")])];
  const next = settleShortage(entries, answer("a"));
  assert.deepEqual(next[0].shortages.map((s) => s.id), ["a", "b"]);
  assert.equal(next[0].shortages[0].resolution, "Backordered the shortfall.");
  assert.equal(next[0].shortages[1].resolved_at, null);
  assert.equal(next[0].task.open_shortages, 1);
  assert.equal(entries[0].task.open_shortages, 2, "the input is not mutated");
});

test("a pick whose last open shortage was resolved still has its card", () => {
  const settled = settleShortage([entry([answer("a"), short("b")])], answer("b"));
  assert.equal(settled[0].task.open_shortages, 0);
  assert.equal(cardsToShow(settled, ["t1"]).length, 1);
  assert.equal(cardsToShow(settled, []).length, 0, "reopened clean, a pick with none open has no card");
});

test("an answer for a shortage the sheet does not hold changes nothing", () => {
  const entries = [entry([short("a")])];
  assert.deepEqual(settleShortage(entries, answer("zzz")), entries);
});

test("the header says how many are open, or that all are resolved", () => {
  assert.equal(headCountWords(2), "2 open");
  assert.equal(headCountWords(0), "All resolved");
});
