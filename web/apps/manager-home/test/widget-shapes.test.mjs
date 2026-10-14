// Gap 10 of the per role homes: the gauge shape draws a meter from the server's
// points, never a plain number, and never a shape the points cannot honestly
// make. (The trend half was reverted, review P2-3.)
import { test } from "node:test";
import assert from "node:assert/strict";
import { gaugeSegments } from "../src/widget-shapes.ts";

const p = (label, number, tone = "") => ({ label, number, tone });

test("a gauge is the points' shares of their own sum, with the tone the server named", () => {
  const segs = gaugeSegments([p("Waiting", 8), p("Claimed", 5), p("Picked", 3, "good"), p("Staged", 4)]);
  assert.deepEqual(segs.map((s) => s.label), ["Waiting", "Claimed", "Picked", "Staged"]);
  assert.deepEqual(segs.map((s) => s.widthClass), ["w40", "w25", "w15", "w20"]);
  assert.deepEqual(segs.map((s) => s.toneClass), ["tone-none", "tone-none", "tone-good", "tone-none"]);
});

test("a gauge with no points, a zero sum or a negative point draws nothing", () => {
  assert.equal(gaugeSegments([]), null);
  assert.equal(gaugeSegments([p("a", 0), p("b", 0)]), null);
  assert.equal(gaugeSegments([p("a", -1), p("b", 5)]), null);
});

test("a tone outside the closed set is neutral, never a made-up class", () => {
  const segs = gaugeSegments([p("a", 1, "purple"), p("b", 1, "watch"), p("c", 2, "stop")]);
  assert.deepEqual(segs.map((s) => s.toneClass), ["tone-none", "tone-watch", "tone-stop"]);
});

test("a money_cents gauge shows dollars in every segment, a count keeps its grouping, basis points a percent", () => {
  const money = gaugeSegments([p("Current", 410000), p("Over 60", 40000, "stop")], "money_cents");
  assert.deepEqual(money.map((s) => s.figure), ["$4,100.00", "$400.00"]);
  assert.deepEqual(gaugeSegments([p("Waiting", 1200), p("Claimed", 5)], "count").map((s) => s.figure), ["1,200", "5"]);
  assert.deepEqual(gaugeSegments([p("a", 250), p("b", 50)], "basis_points").map((s) => s.figure), ["2.50%", "0.50%"]);
});
