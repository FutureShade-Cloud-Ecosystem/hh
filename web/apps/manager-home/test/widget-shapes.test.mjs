// Gap 10 of the per role homes: the trend and gauge shapes draw a trend line
// and a gauge from the server's points, never a plain number, and never a
// shape the points cannot honestly make.
import { test } from "node:test";
import assert from "node:assert/strict";
import { gaugeSegments, sparkColumns, sparkLabel } from "../src/widget-shapes.ts";

const p = (label, number, tone = "") => ({ label, number, tone });

test("a trend draws one column per point, tallest at the full height, quantized in fives", () => {
  const cols = sparkColumns([p("Day 1", 10), p("Day 2", 20), p("Day 3", 15)]);
  assert.deepEqual(cols.map((c) => c.heightClass), ["h50", "h100", "h75"]);
  assert.deepEqual(cols.map((c) => c.label), ["Day 1", "Day 2", "Day 3"]);
});

test("a small positive point keeps a visible stub; a zero point has none", () => {
  const cols = sparkColumns([p("a", 0), p("b", 1), p("c", 1000)]);
  assert.deepEqual(cols.map((c) => c.heightClass), ["h0", "h5", "h100"]);
});

test("fewer than two points, or no height anywhere, is no line at all", () => {
  assert.equal(sparkColumns([]), null);
  assert.equal(sparkColumns([p("only", 5)]), null);
  assert.equal(sparkColumns([p("a", 0), p("b", 0)]), null);
  assert.equal(sparkColumns([p("a", -3), p("b", 4)]), null, "a negative point is not a height this line can draw");
});

test("the trend's spoken label names its first and last point with the card's own formatting", () => {
  const points = [p("Day 1", 100000), p("Day 7", 123456)];
  assert.equal(sparkLabel(points, "money_cents"), "Trend from Day 1, $1,000.00, to Day 7, $1,234.56");
  assert.equal(sparkLabel([p("a", 3), p("b", 1200)], "count"), "Trend from a, 3, to b, 1,200");
});

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
