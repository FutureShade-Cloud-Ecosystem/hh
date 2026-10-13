// The builder's api door, on its own: every whitelist projection the arrange
// view is fed through, against raw wire-shaped objects. No network, no
// browser. The rule the door carries: a field the projection does not name is
// dropped here and never reaches a screen.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  toGalleryCard,
  toGalleryEntryRow,
  toHomeConfig,
  toLayout,
  toMeasure,
  toMyHome,
  toPin,
  toProfileDetail,
  toProfileGallery,
  toProfileWidgets,
  toProfilesView,
  toTabStrip,
  toTileCatalog,
  toTileCatalogEntry,
  toTileRow,
  toVizTile,
  toWidgetCatalog,
  toWidgetRow,
  toWidgetView,
} from "../src/builder-api.ts";

test("a widget row keeps the stored fields and drops what the screen never reads", () => {
  const row = toWidgetRow({
    widget_key: "orders.open",
    shape: "stat",
    measure: "orders.open",
    split: "none",
    branch_id: "5d1b3f6e",
    range: "open",
    size: "S",
    cols: 8,
    rows: 2,
    title: "Open orders",
    position: 5,
    visible: true,
    label: "should not survive the door",
  });
  assert.deepEqual(row, {
    widget_key: "orders.open",
    shape: "stat",
    measure: "orders.open",
    split: "none",
    branch_id: "5d1b3f6e",
    range: "open",
    size: "S",
    cols: 8,
    rows: 2,
    title: "Open orders",
    position: 5,
    visible: true,
  });
});

test("a widget row without the free span carries no pair, and a half pair is dropped", () => {
  const lone = toWidgetRow({ widget_key: "k", shape: "stat", measure: "m", split: "none", range: "open", size: "M", title: "", position: 0, visible: true, cols: 4 });
  assert.equal("cols" in lone, false);
  assert.equal("rows" in lone, false);
  const pair = toWidgetRow({ widget_key: "k", shape: "stat", measure: "m", split: "none", range: "open", size: "M", title: "", position: 0, visible: true, cols: 8, rows: 2 });
  assert.equal(pair.cols, 8);
  assert.equal(pair.rows, 2);
});

test("a resolved widget view carries the catalog metadata beside the stored row", () => {
  const view = toWidgetView({
    widget_key: "hero.deliveries", shape: "stat", measure: "deliveries.today", split: "none",
    range: "today", size: "L", title: "Deliveries today", position: 0, visible: true,
    label: "Deliveries today", kind: "count", module: "dispatch", unit: "dispatch", screen: "dispatch",
    span_cols: 6, span_rows: 2, sneaky: "dropped",
  });
  assert.equal(view.label, "Deliveries today");
  assert.equal(view.kind, "count");
  assert.equal(view.module, "dispatch");
  assert.equal(view.unit, "dispatch");
  assert.equal(view.screen, "dispatch");
  assert.equal(view.span_cols, 6);
  assert.equal(view.span_rows, 2);
  assert.equal("sneaky" in view, false);
  assert.equal("branch_id" in view, false);
});

test("a layout keeps the profile and the tier the row came from", () => {
  const layout = toLayout({
    profile: "manager.yard",
    source: "role",
    widgets: [{ widget_key: "needs.you", shape: "list", measure: "needs.you", split: "none", range: "open", size: "L", title: "Needs you", position: 0, visible: true, label: "Needs you", kind: "count", module: "dashboard", span_cols: 6, span_rows: 2 }],
  });
  assert.equal(layout.profile, "manager.yard");
  assert.equal(layout.source, "role");
  assert.equal(layout.widgets.length, 1);
  assert.equal(layout.widgets[0].widget_key, "needs.you");
});

test("a measure carries its own windows, splits and body forms, defaulted when absent", () => {
  const full = toMeasure({
    metric_id: "deliveries.today", label: "Deliveries today", module: "dispatch", kind: "count",
    dimensions: ["branch", "status"], shapes: ["bar", "list"], splits: ["none", "branch"],
    widget_shapes: ["stat", "bars"], ranges: ["today", "open"], lead: "Deliveries booked today",
  });
  assert.deepEqual(full.ranges, ["today", "open"]);
  assert.equal(full.unit, "");
  assert.equal(full.screen, "");
  const bare = toMeasure({ metric_id: "m", label: "M", module: "x", kind: "count", dimensions: [], shapes: [], splits: [], widget_shapes: [] });
  assert.deepEqual(bare.ranges, []);
  assert.equal(bare.lead, "");
});

test("the widget catalog keeps the grid's own facts beside the measures", () => {
  const catalog = toWidgetCatalog({
    measures: [],
    shapes: ["stat", "bars"],
    sizes: ["S", "T", "M", "L", "XL"],
    spans: { S: [3], XL: [12] },
    span_cols: [3, 4, 6, 8, 9, 12],
    max_rows: 3,
    splits: ["none", "branch"],
    ranges: ["today", "open"],
    profile: "manager.yard",
  });
  assert.deepEqual(catalog.spanCols, [3, 4, 6, 8, 9, 12]);
  assert.equal(catalog.maxRows, 3);
  assert.deepEqual(catalog.sizes, ["S", "T", "M", "L", "XL"]);
  assert.equal(catalog.profile, "manager.yard");
});

test("a gallery card arrives resolved: the words and the span ride on it", () => {
  const card = toGalleryCard({
    entry_key: "deliveries-today", shape: "stat", measure: "deliveries.today", split: "none",
    range: "today", size: "M", title: "Deliveries today", blurb: "The day going out the gate.",
    position: 0, visible: true, label: "Deliveries today", kind: "count", module: "dispatch",
    unit: "dispatch", screen: "dispatch", span_cols: 6, span_rows: 1,
  });
  assert.equal(card.blurb, "The day going out the gate.");
  assert.equal(card.label, "Deliveries today");
  assert.equal(card.span_cols, 6);
});

test("a gallery entry the server sends unresolved keeps only the stored fields", () => {
  const entry = toGalleryEntryRow({
    entry_key: "e", shape: "stat", measure: "m", split: "none", range: "open", size: "M",
    title: "T", blurb: "B", position: 2, visible: true,
  });
  assert.equal(entry.entry_key, "e");
  assert.equal("label" in entry, false);
  assert.equal("span_cols" in entry, false);
});

test("a tab pin keeps visible false as false: an unpinned row is kept, not dropped", () => {
  const pin = toPin({ tab_key: "sales/pos", position: 1, visible: false });
  assert.equal(pin.visible, false);
  assert.equal(pin.tab_key, "sales/pos");
});

test("a tab strip keeps its source word", () => {
  const strip = toTabStrip({ source: "user", pins: [{ tab_key: "sales", position: 0, visible: true }] });
  assert.equal(strip.source, "user");
  assert.equal(strip.pins.length, 1);
});

test("a tile row keeps order and visibility only", () => {
  assert.deepEqual(toTileRow({ tile_id: "orders.open", position: 3, visible: false, number: 12, state: "ok" }), {
    tile_id: "orders.open", position: 3, visible: false,
  });
});

test("a tile catalog entry carries the wired flag and the words, never the server's colour", () => {
  const entry = toTileCatalogEntry({
    tile_id: "payments.today", label: "Payments today", module: "till", kind: "money_cents",
    glyph: "$", accent: "#abc123", default_visible: true, lead: "Taken at the counter", wired: false,
  });
  assert.equal(entry.wired, false);
  assert.equal(entry.label, "Payments today");
  assert.equal("glyph" in entry, false);
  assert.equal("accent" in entry, false);
});

test("the tile catalog keeps the profile list and the caller's own profile", () => {
  const catalog = toTileCatalog({
    tiles: [{ tile_id: "t", label: "T", module: "m", kind: "count", glyph: "", accent: "", default_visible: true, lead: "", wired: true }],
    profiles: ["manager.yard", "manager.sales", "admin"],
    profile: "manager.yard",
  });
  assert.deepEqual(catalog.profiles, ["manager.yard", "manager.sales", "admin"]);
  assert.equal(catalog.profile, "manager.yard");
  assert.equal(catalog.tiles.length, 1);
});

test("a home config keeps the takeover facts with the profile named", () => {
  const home = toHomeConfig({ profile: "manager.yard", takeover: true, landing_unit: "yard", landing_module: "yard", mobile_only: true });
  assert.equal(home.takeover, true);
  assert.equal(home.landing_unit, "yard");
  assert.equal(home.mobile_only, true);
});

test("my tile-era home narrows to rows, config and the tab strip", () => {
  const home = toMyHome({
    profile: "manager.yard",
    source: "user",
    tiles: [{ tile_id: "orders.open", position: 0, visible: true, state: "ok", number: 12 }],
    home: { profile: "manager.yard", takeover: false, mobile_only: false },
    tabs: { source: "user", pins: [{ tab_key: "yard", position: 0, visible: true }] },
    hotkeys: [{ binding: "g o", action: "open orders" }],
  });
  assert.deepEqual(home.tiles, [{ tile_id: "orders.open", position: 0, visible: true }]);
  assert.equal(home.tabs.source, "user");
  assert.equal("hotkeys" in home, false);
  assert.equal("number" in home.tiles[0], false);
});

test("a tile-era home without tabs answers an empty catalog strip, never undefined", () => {
  const home = toMyHome({ profile: "manager.yard", source: "catalog", tiles: [], home: { profile: "manager.yard", takeover: false, mobile_only: false } });
  assert.deepEqual(home.tabs, { source: "catalog", pins: [] });
});

test("the profiles list keeps each profile's landing config", () => {
  const view = toProfilesView({
    profiles: [
      { profile: "manager.yard", home: { profile: "manager.yard", takeover: false, mobile_only: false } },
      { profile: "admin", home: { profile: "admin", takeover: true, landing_unit: "admin", mobile_only: true } },
    ],
  });
  assert.equal(view.profiles.length, 2);
  assert.equal(view.profiles[1].home.landing_unit, "admin");
});

test("a role detail keeps its tile rows and its landing config", () => {
  const detail = toProfileDetail({
    profile: "manager.yard",
    tiles: [{ tile_id: "orders.open", position: 0, visible: true }],
    home: { profile: "manager.yard", takeover: false, mobile_only: false },
  });
  assert.equal(detail.profile, "manager.yard");
  assert.deepEqual(detail.tiles, [{ tile_id: "orders.open", position: 0, visible: true }]);
});

test("a role's stored widgets and gallery entries keep the stored shape, unresolved", () => {
  const widgets = toProfileWidgets({ profile: "manager.yard", widgets: [{ widget_key: "needs.you", shape: "list", measure: "needs.you", split: "none", range: "open", size: "L", title: "Needs you", position: 0, visible: true }] });
  assert.equal(widgets.widgets.length, 1);
  assert.equal("label" in widgets.widgets[0], false);
  const gallery = toProfileGallery({ profile: "manager.yard", entries: [{ entry_key: "e", shape: "stat", measure: "m", split: "none", range: "open", size: "M", title: "T", blurb: "B", position: 0, visible: true }] });
  assert.equal(gallery.entries.length, 1);
  assert.equal("label" in gallery.entries[0], false);
});

test("a viz tile keeps the series and the state, null total stays null", () => {
  const tile = toVizTile({
    tile_id: "viz.picks.queue-status-bar", metric_id: "picks.queue", dimension: "status", shape: "bar",
    label: "Pick queue", kind: "count", module: "tasks", position: 0, state: "ok", total: 20,
    points: [{ label: "Waiting", number: 8, tone: "watch" }],
  });
  assert.equal(tile.state, "ok");
  assert.equal(tile.total, 20);
  assert.deepEqual(tile.points, [{ label: "Waiting", number: 8, tone: "watch" }]);
  const dead = toVizTile({ tile_id: "viz.x-y-bar", metric_id: "x", dimension: "status", shape: "bar", label: "X", kind: "count", module: "m", position: 0, state: "unwired", total: null, note: "Not connected (x)" });
  assert.equal(dead.total, null);
  assert.deepEqual(dead.points, []);
  assert.equal(dead.note, "Not connected (x)");
});

test("everything survives a payload of nothing: lists land empty, never undefined", () => {
  assert.deepEqual(toLayout({ profile: "p", source: "role", widgets: null }).widgets, []);
  assert.deepEqual(toWidgetCatalog({ measures: null, shapes: null, sizes: null, splits: null, ranges: null, profile: "p" }).measures, []);
  assert.deepEqual(toTabStrip({ source: "user", pins: null }).pins, []);
  assert.deepEqual(toTileCatalog({ tiles: null, profiles: null, profile: "p" }).tiles, []);
  assert.deepEqual(toProfilesView({ profiles: null }).profiles, []);
  assert.deepEqual(toProfileGallery({ profile: "p", entries: null }).entries, []);
  assert.deepEqual(toProfileWidgets({ profile: "p", widgets: null }).widgets, []);
});
