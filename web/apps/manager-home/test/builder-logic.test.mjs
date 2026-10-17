// The builder's pure decisions, on their own: the preset spans, what a move
// or a resize does to the working set, the whole-layout body a save PUTs, the
// builder form's draft rules, the pinned-tab strip's edit rule, and the
// classic tile editor's rows. No DOM, no network. Every fact here is the
// core's own arithmetic (widget.go) or the original app's tested rule, ported.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  BENTO_MAX_ROWS,
  COLSNAP,
  MEASURE_NEEDS_YOU,
  applyPin,
  autoTitle,
  buildTileEditorRows,
  canRemove,
  draftFromCatalog,
  draftFromWidget,
  entryTitle,
  isBentoSpan,
  layoutBody,
  layoutChanged,
  movePin,
  moveTo,
  nextSize,
  nextWidgetKey,
  pinWord,
  reconcileDraft,
  removeKey,
  respanTo,
  sizeForSpan,
  sizeWord,
  sourceWord,
  spanOf,
  spanTag,
  splitsFor,
  swapTo,
  tileLayoutBody,
  toTileEditorRow,
  vizIdFor,
  vizIdsFor,
  widgetFromDraft,
  widgetFromEntry,
  widgetSpan,
  widgetTitle,
} from "../src/builder-logic.ts";

const view = (over = {}) => ({
  widget_key: "k",
  shape: "stat",
  measure: "orders.open",
  split: "none",
  branch_id: "",
  range: "open",
  size: "M",
  title: "",
  position: 0,
  visible: true,
  label: "Open orders",
  kind: "count",
  module: "order",
  unit: "",
  screen: "",
  span_cols: 6,
  span_rows: 1,
  ...over,
});

test("the five presets mirror the core's SpanOf: S 3x1, T 3x2, M 6x1, L 6x2, XL 12x1", () => {
  assert.deepEqual(spanOf("S"), { cols: 3, rows: 1 });
  assert.deepEqual(spanOf("T"), { cols: 3, rows: 2 });
  assert.deepEqual(spanOf("M"), { cols: 6, rows: 1 });
  assert.deepEqual(spanOf("L"), { cols: 6, rows: 2 });
  assert.deepEqual(spanOf("XL"), { cols: 12, rows: 1 });
  assert.deepEqual(spanOf("Q"), { cols: 0, rows: 0 });
});

test("an unknown size is a zero span, so the caller decides rather than inventing 1x1", () => {
  assert.equal(spanOf("nope").cols, 0);
});

test("the snap set is the divisors and half divisors of twelve, and the ceiling is three rows", () => {
  assert.deepEqual([...COLSNAP], [3, 4, 6, 8, 9, 12]);
  assert.equal(BENTO_MAX_ROWS, 3);
  assert.equal(isBentoSpan(5, 2), false);
  assert.equal(isBentoSpan(8, 4), false);
  assert.equal(isBentoSpan(8, 2), true);
});

test("the free pair wins, then the preset, then the served span, then the 1x1 floor", () => {
  assert.deepEqual(widgetSpan(view({ cols: 8, rows: 2 })), { cols: 8, rows: 2 });
  assert.deepEqual(widgetSpan(view({ size: "L" })), { cols: 6, rows: 2 });
  assert.deepEqual(widgetSpan(view({ size: "zzz", span_cols: 9, span_rows: 1 })), { cols: 9, rows: 1 });
  assert.deepEqual(widgetSpan(view({ size: "zzz", span_cols: 0, span_rows: 0 })), { cols: 1, rows: 1 });
});

test("a span that is a preset names it; one that is not reads as the bare arithmetic", () => {
  assert.equal(sizeForSpan(6, 2), "L");
  assert.equal(sizeForSpan(8, 2), null);
  assert.equal(spanTag(6, 2), "Large");
  assert.equal(spanTag(8, 2), "8\u00d72");
});

test("the keyboard walks the five presets in area order, clamped at both ends, never wrapped", () => {
  assert.equal(nextSize("S", 1), "T");
  assert.equal(nextSize("XL", 1), "XL");
  assert.equal(nextSize("XL", -1), "L");
  assert.equal(nextSize("S", -1), "S");
  assert.equal(nextSize("mystery", 1), "T");
});

test("every closed-set value a screen could reach carries its words", () => {
  assert.equal(sizeWord("S"), "Small");
  assert.equal(sizeWord("XL"), "Wide");
  assert.equal(sizeWord("zz"), "zz");
  assert.equal(sourceWord("user"), "Your layout");
  assert.equal(sourceWord("role"), "The role default");
  assert.equal(sourceWord("catalog"), "The catalog default");
  assert.equal(sourceWord(""), "");
});

test("a move closes the gap it left; out of range moves nothing", () => {
  const three = [view({ widget_key: "a" }), view({ widget_key: "b" }), view({ widget_key: "c" })];
  assert.deepEqual(moveTo(three, 0, 2).map((w) => w.widget_key), ["b", "c", "a"]);
  assert.deepEqual(moveTo(three, 0, 9).map((w) => w.widget_key), ["b", "c", "a"]);
  assert.deepEqual(moveTo(three, -1, 0).map((w) => w.widget_key), ["a", "b", "c"]);
});

test("a resize to a preset rewrites the span and clears the free pair", () => {
  const rows = respanTo([view({ cols: 8, rows: 2 })], "k", 6, 2);
  assert.equal(rows[0].size, "L");
  assert.equal("cols" in rows[0], false);
  assert.equal(rows[0].span_cols, 6);
  const free = respanTo([view()], "k", 8, 2);
  assert.equal(free[0].size, "M");
  assert.equal(free[0].cols, 8);
  assert.equal(free[0].rows, 2);
  const refused = respanTo([view()], "k", 5, 2);
  assert.equal("cols" in refused[0], false);
});

test("every widget may be removed except the queue that earns the page", () => {
  assert.equal(MEASURE_NEEDS_YOU, "needs.you");
  assert.equal(canRemove(view({ measure: "needs.you" })), false);
  assert.equal(canRemove(view({ measure: "orders.open" })), true);
  assert.deepEqual(removeKey([view({ measure: "needs.you" })], "k").length, 1);
  assert.deepEqual(removeKey([view()], "k"), []);
});

test("the PUT body is the whole list, renumbered, view fields dropped, the pair only when set", () => {
  const body = layoutBody([
    view({ widget_key: "a", branch_id: "5d1b", cols: 8, rows: 2, label: "drop me", span_cols: 8 }),
    view({ widget_key: "b" }),
  ]);
  assert.deepEqual(body, [
    { widget_key: "a", shape: "stat", measure: "orders.open", split: "none", branch_id: "5d1b", range: "open", size: "M", title: "", position: 0, visible: true, cols: 8, rows: 2 },
    { widget_key: "b", shape: "stat", measure: "orders.open", split: "none", range: "open", size: "M", title: "", position: 1, visible: true },
  ]);
});

test("a layout is changed by order, size, free span, title, shape, split, window or branch", () => {
  const before = [view()];
  assert.equal(layoutChanged(before, [view()]), false);
  assert.equal(layoutChanged(before, [view({ title: "renamed" })]), true);
  assert.equal(layoutChanged(before, [view(), view({ widget_key: "z" })]), true);
  assert.equal(layoutChanged(before, [view({ cols: 9, rows: 1 })]), true);
  assert.equal(layoutChanged(before, [view({ size: "S" })]), true);
});

test("a new widget's key is derived from its measure with the smallest unused ordinal", () => {
  assert.equal(nextWidgetKey("deliveries.today", []), "deliveries.today");
  assert.equal(nextWidgetKey("deliveries.today", ["deliveries.today"]), "deliveries.today-2");
  assert.equal(nextWidgetKey("deliveries.today", ["deliveries.today", "deliveries.today-2"]), "deliveries.today-3");
  assert.equal(nextWidgetKey("Deliveries Today!", []), "deliveries-today");
  assert.equal(nextWidgetKey("", ["widget"]), "widget-2");
});

test("a derived title is a preview: typing it back stores the empty string", () => {
  assert.equal(autoTitle("Receivables", "none"), "Receivables");
  assert.equal(autoTitle("Receivables", "bucket"), "Receivables by age");
  assert.equal(widgetTitle({ title: "", label: "Open orders", split: "none" }), "Open orders");
  assert.equal(widgetTitle({ title: "Mine", label: "Open orders", split: "none" }), "Mine");
  assert.equal(entryTitle({ title: "", label: "Deliveries today", split: "none" }), "Deliveries today");
});

test("the form's choices come off the measure, None first, defended against an empty entry", () => {
  const m = { splits: ["branch", "status"], widget_shapes: ["bars", "stat"], ranges: ["today", "open"] };
  assert.deepEqual(splitsFor(m), ["none", "branch", "status"]);
  assert.deepEqual(splitsFor(undefined), ["none"]);
  assert.deepEqual(draftFromCatalog([
    { metric_id: "a", label: "A", module: "m", kind: "count", dimensions: ["branch"], shapes: ["bar"], splits: ["branch"], widget_shapes: ["stat"], ranges: ["today"], lead: "" },
  ], ["today"]).measure, "a");
});

test("a draft is corrected into the catalog: a measure the caller cannot see never appears", () => {
  const catalog = [
    { metric_id: "a", label: "A", module: "m", kind: "count", dimensions: ["branch"], shapes: ["bar"], splits: ["none", "branch"], widget_shapes: ["stat", "bars"], ranges: ["today"], lead: "" },
    { metric_id: "b", label: "B", module: "m", kind: "count", dimensions: [], shapes: [], splits: ["none"], widget_shapes: ["stat"], ranges: ["open"], lead: "" },
  ];
  const fixed = reconcileDraft({ shape: "list", measure: "gone", split: "bucket", branch_id: "", range: "last_90", size: "S", title: "" }, catalog, ["open"]);
  assert.equal(fixed.measure, "a");
  assert.equal(fixed.shape, "stat");
  assert.equal(fixed.split, "none");
  assert.equal(fixed.range, "today");
  const empty = reconcileDraft({ shape: "stat", measure: "b", split: "none", branch_id: "", range: "open", size: "M", title: "" }, catalog, ["open"]);
  assert.equal(empty.measure, "b");
  assert.equal(empty.shape, "stat");
  assert.deepEqual(draftFromWidget(view({ measure: "a", split: "branch", range: "today", size: "L", title: "T" })), {
    shape: "stat", measure: "a", split: "branch", branch_id: "", range: "today", size: "L", title: "T",
  });
});

test("a finished draft resolves against its measure; a gallery entry arrives already resolved", () => {
  const m = { metric_id: "a", label: "A", module: "m", kind: "count", dimensions: ["branch"], shapes: ["bar"], splits: ["none"], widget_shapes: ["stat"], ranges: ["today"], lead: "", unit: "dispatch", screen: "dispatch" };
  const w = widgetFromDraft({ shape: "stat", measure: "a", split: "none", branch_id: "5d1b", range: "today", size: "L", title: " T " }, m, "a", 3);
  assert.equal(w.widget_key, "a");
  assert.equal(w.position, 3);
  assert.equal(w.span_cols, 6);
  assert.equal(w.span_rows, 2);
  assert.equal(w.branch_id, "5d1b");
  assert.equal(w.unit, "dispatch");
  const e = widgetFromEntry({ entry_key: "e", shape: "bars", measure: "picks.queue", split: "status", range: "open", size: "L", cols: 8, rows: 2, title: "Pick queue", blurb: "B", position: 0, visible: true, label: "Pick queue", kind: "count", module: "tasks", unit: "yard", screen: "yard", span_cols: 8, span_rows: 2 }, "picks.queue", 0);
  assert.equal(e.cols, 8);
  assert.equal(e.label, "Pick queue");
});

test("a widget is measured by composing the id its measure accepts, or nothing", () => {
  const m = { metric_id: "picks.queue", dimensions: ["branch", "status"], shapes: ["bar", "list"] };
  assert.equal(vizIdFor({ measure: "picks.queue", split: "status" }, m), "viz.picks.queue-status-bar");
  assert.equal(vizIdFor({ measure: "picks.queue", split: "branch" }, m), "viz.picks.queue-branch-bar");
  assert.equal(vizIdFor({ measure: "picks.queue", split: "none" }, m), "viz.picks.queue-branch-bar");
  assert.equal(vizIdFor({ measure: "x", split: "none" }, undefined), null);
  assert.equal(vizIdFor({ measure: "x", split: "none" }, { metric_id: "x", dimensions: [], shapes: ["bar"] }), null);
  const two = [
    view({ measure: "picks.queue", split: "status", widget_key: "a" }),
    view({ measure: "orders.open", split: "none", widget_key: "b" }),
    view({ measure: "picks.queue", split: "status", widget_key: "c" }),
  ];
  assert.deepEqual(vizIdsFor(two, [m, { metric_id: "orders.open", dimensions: [], shapes: [] }]), ["viz.picks.queue-status-bar"]);
});

test("pinning materialises the role's set as the caller's own; an unpin keeps its row", () => {
  const role = [
    { tab_key: "sales", position: 0, visible: true },
    { tab_key: "yard", position: 1, visible: true },
  ];
  const pinned = applyPin(role, "purchasing", true);
  assert.deepEqual(pinned.map((p) => p.tab_key), ["sales", "yard", "purchasing"]);
  assert.deepEqual(pinned.map((p) => p.position), [0, 1, 2]);
  const unpinned = applyPin(role, "sales", false);
  assert.equal(unpinned.find((p) => p.tab_key === "sales").visible, false);
  assert.equal(unpinned.length, 2);
});

test("a swap names both rows; an unknown pair renumbers and changes nothing", () => {
  const pins = [
    { tab_key: "b", position: 0, visible: true },
    { tab_key: "a", position: 1, visible: true },
    { tab_key: "c", position: 2, visible: true },
  ];
  assert.deepEqual(swapTo(pins, "b", "c").map((p) => p.tab_key), ["c", "a", "b"]);
  assert.deepEqual(swapTo(pins, "b", "gone").map((p) => p.position), [0, 1, 2]);
  assert.deepEqual(movePin(pins, "a", -1).map((p) => p.tab_key), ["a", "b", "c"]);
  assert.deepEqual(movePin(pins, "c", 5).map((p) => p.tab_key), ["b", "a", "c"]);
});

test("a stored key a person reads opens its separators out; a word never shown raw", () => {
  assert.equal(pinWord("sales/pos"), "sales, pos");
  assert.equal(pinWord("yard"), "yard");
});

test("the classic tile editor's rows carry the catalog's words and the layout's order, hidden catalog tiles appended", () => {
  const catalog = [
    { tile_id: "a", label: "A", module: "m", default_visible: true, lead: "first", wired: true },
    { tile_id: "b", label: "B", module: "m", default_visible: true, lead: "second", wired: false },
  ];
  const rows = buildTileEditorRows(catalog, [{ tile_id: "b", position: 0, visible: true }]);
  assert.deepEqual(rows.map((r) => r.tile_id), ["b", "a"]);
  assert.equal(rows[0].label, "B");
  assert.equal(rows[1].visible, false);
  assert.equal(rows[1].wired, true);
  assert.deepEqual(toTileEditorRow({ tile_id: "a", position: 2, visible: false, number: 5 }), { tile_id: "a", position: 2, visible: false });
  assert.deepEqual(tileLayoutBody(rows), [
    { tile_id: "b", position: 0, visible: true },
    { tile_id: "a", position: 1, visible: false },
  ]);
});
