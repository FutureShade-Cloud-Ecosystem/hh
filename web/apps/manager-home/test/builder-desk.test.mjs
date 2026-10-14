// The builder desk, on its own: the state machine behind the arrange view,
// driven by fake doors. No DOM, no network. Every claim here is the write
// rule the core enforces (whole or absent, the server's own refusal words)
// or the edit rule the pure layer carries.
import { test } from "node:test";
import assert from "node:assert/strict";
import { BuilderDesk } from "../src/builder-desk.ts";

// -- fakes -----------------------------------------------------------------------------------------------

const widget = (key, over = {}) => ({
  widget_key: key,
  shape: "stat",
  measure: "orders.open",
  split: "none",
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

const ok = (value) => ({ ok: true, value });
const refuse = (error, status) => ({ ok: false, error, status });

function fakeDoors(over = {}) {
  const calls = [];
  const yardWidgets = [
    widget("needs.you", { measure: "needs.you", shape: "list", label: "Needs you", module: "dashboard" }),
    widget("orders.open"),
  ];
  const doors = {
    calls,
    builder: {
      async myWidgets() {
        calls.push("myWidgets");
        if (over.mineRefused) return refuse("your login does not reach this", 403);
        return ok({ profile: "manager.yard", source: "user", widgets: yardWidgets });
      },
      async myHome() {
        calls.push("myHome");
        return ok((over.myHome ??= {
          profile: "manager.yard",
          source: "user",
          tiles: [{ tile_id: "orders.open", position: 0, visible: true }],
          home: { profile: "manager.yard", takeover: false, landing_unit: "", landing_module: "", mobile_only: false },
          tabs: { source: "user", pins: [{ tab_key: "yard", position: 0, visible: true }] },
        }));
      },
      async tileCatalog() {
        calls.push("tileCatalog");
        return ok({
          tiles: [{ tile_id: "orders.open", label: "Open orders", module: "order", default_visible: true, lead: "the counter's list", wired: true }],
          profiles: ["manager.yard", "manager.sales", "admin"],
          profile: "manager.yard",
        });
      },
      async saveMyTiles(rows) {
        calls.push(`saveMyTiles:${rows.length}`);
        return ok({ tiles: rows });
      },
      async resetMyTiles() {
        calls.push("resetMyTiles");
        return ok({ cleared: 1, home: over.myHome });
      },
      async saveMyTabs(pins) {
        calls.push(`saveMyTabs:${pins.map((p) => p.tab_key).join(",")}`);
        return ok({ source: "user", pins });
      },
      async resetMyTabs() {
        calls.push("resetMyTabs");
        return ok({ cleared: 1, tabs: { source: "role", pins: [{ tab_key: "yard", position: 0, visible: true }] } });
      },
      async widgetCatalog() {
        calls.push("widgetCatalog");
        return ok({
          measures: [
            { metric_id: "orders.open", label: "Open orders", module: "order", kind: "count", dimensions: ["branch"], shapes: ["bar"], splits: ["none", "branch"], widget_shapes: ["stat", "bars"], ranges: ["open", "today"], unit: "", screen: "", lead: "Open orders" },
            { metric_id: "deliveries.today", label: "Deliveries today", module: "dispatch", kind: "count", dimensions: ["status"], shapes: ["bar"], splits: ["none"], widget_shapes: ["stat"], ranges: ["today"], unit: "", screen: "", lead: "Deliveries" },
          ],
          shapes: ["stat", "bars"],
          sizes: ["S", "T", "M", "L", "XL"],
          span_cols: [3, 4, 6, 8, 9, 12],
          max_rows: 3,
          splits: ["none", "branch"],
          ranges: ["today", "open"],
          profile: "manager.yard",
        });
      },
      async gallery() {
        calls.push("gallery");
        return ok({
          profile: "manager.yard",
          entries: [
            { entry_key: "deliveries-today", shape: "stat", measure: "deliveries.today", split: "none", range: "today", size: "M", title: "Deliveries today", blurb: "The day going out the gate.", position: 0, visible: true, label: "Deliveries today", kind: "count", module: "dispatch", unit: "", screen: "", span_cols: 6, span_rows: 1 },
          ],
        });
      },
      async saveMyWidgets(widgets) {
        calls.push(`saveMyWidgets:${widgets.map((w) => w.widget_key).join(",")}`);
        if (over.saveRefused) return refuse(over.saveRefused, 422);
        return ok({ widgets });
      },
      async resetMyWidgets() {
        calls.push("resetMyWidgets");
        return ok({ cleared: 1, layout: { profile: "manager.yard", source: "role", widgets: [widget("needs.you", { measure: "needs.you", shape: "list", label: "Needs you", module: "dashboard", position: 0 })] } });
      },
      async metricCatalog() {
        calls.push("metricCatalog");
        return ok({
          metrics: [{ metric_id: "deliveries.today", label: "Deliveries today", module: "dispatch", kind: "count", dimensions: ["status"], shapes: ["bar"], splits: ["none"], widget_shapes: ["stat"], ranges: ["today"], unit: "", screen: "", lead: "" }],
          dimensions: ["branch", "status", "bucket"],
          shapes: ["bar", "split", "list"],
          profile: "manager.yard",
        });
      },
      async viz(ids) {
        calls.push(`viz:${ids.join("|")}`);
        if (over.vizRefused) return refuse("no such composition", 400);
        return ok(ids.map((id) => ({ tile_id: id, label: "Deliveries today", kind: "count", module: "dispatch", state: "ok", total: 7, points: [{ label: "Waiting", number: 7, tone: "" }], note: "" })));
      },
      async profiles() {
        calls.push("profiles");
        if (over.profilesRefused) return refuse("Forbidden", 403);
        return ok({
          profiles: [
            { profile: "manager.yard", home: { profile: "manager.yard", takeover: false, landing_unit: "", landing_module: "", mobile_only: false } },
            { profile: "manager.sales", home: { profile: "manager.sales", takeover: false, landing_unit: "", landing_module: "", mobile_only: false } },
          ],
        });
      },
      async profileDetail(profile) {
        calls.push(`profileDetail:${profile}`);
        if (over.roleRefused) return refuse("Forbidden", 403);
        return ok({ profile, tiles: [], home: { profile, takeover: false, landing_unit: "", landing_module: "", mobile_only: false } });
      },
      async saveProfileTiles(profile, rows) {
        calls.push(`saveProfileTiles:${profile}:${rows.length}`);
        return ok({ tiles: rows });
      },
      async saveProfileHome(profile, home) {
        calls.push(`saveProfileHome:${profile}`);
        if (over.roleHomeRefused) return refuse(over.roleHomeRefused, 422);
        return ok(home);
      },
      async profileTabs(profile) {
        calls.push(`profileTabs:${profile}`);
        return ok({ profile, pins: [{ tab_key: "purchasing", position: 0, visible: true }] });
      },
      async saveProfileTabs(profile, pins) {
        calls.push(`saveProfileTabs:${profile}`);
        return ok({ profile, pins });
      },
      async profileWidgets(profile) {
        calls.push(`profileWidgets:${profile}`);
        return ok({ profile, widgets: [widget("needs.you", { measure: "needs.you", shape: "list", label: "Needs you", module: "dashboard" })] });
      },
      async saveProfileWidgets(profile, widgets) {
        calls.push(`saveProfileWidgets:${profile}:${widgets.length}`);
        if (over.roleWriteRefused) return refuse("Forbidden", 403);
        return ok({ profile, widgets });
      },
      async profileGallery(profile) {
        calls.push(`profileGallery:${profile}`);
        return ok({ profile, entries: [{ entry_key: "e1", shape: "stat", measure: "orders.open", split: "none", range: "open", size: "M", title: "Open orders", blurb: "B", position: 0, visible: true }] });
      },
      async saveProfileGallery(profile, entries) {
        calls.push(`saveProfileGallery:${profile}:${entries.length}`);
        return ok({ profile, entries });
      },
    },
    mine: {
      async branches() {
        calls.push("branches");
        return ok([{ id: "5d1b", code: "TRT", name: "Trenton" }]);
      },
    },
  };
  return doors;
}

const deskOf = (over = {}) => {
  const doors = fakeDoors(over);
  const desk = new BuilderDesk(doors);
  return { doors, desk };
};

// -- the tests -------------------------------------------------------------------------------------------

test("open loads the whole edit surface: my layout, both catalogs, the gallery, tabs, tiles, branches", async () => {
  const { desk } = deskOf();
  await desk.open();
  assert.equal(desk.state.phase, "ready");
  assert.deepEqual(desk.state.widgets.map((w) => w.widget_key), ["needs.you", "orders.open"]);
  assert.equal(desk.state.source, "user");
  assert.deepEqual(desk.state.tabs.map((p) => p.tab_key), ["yard"]);
  assert.deepEqual(desk.state.tiles.map((t) => t.tile_id), ["orders.open"]);
  assert.equal(desk.state.catalog.measures.length, 2);
  assert.deepEqual(desk.state.gallery.map((e) => e.entry_key), ["deliveries-today"]);
  assert.equal(desk.state.measures.length, 1);
  assert.deepEqual(desk.state.branches.map((b) => b.id), ["5d1b"]);
  assert.equal(desk.state.profiles.length, 0);
});

test("a failed load becomes an error the view can show, and reload tries again", async () => {
  const { desk } = deskOf({ mineRefused: true });
  await desk.open();
  assert.equal(desk.state.phase, "error");
  assert.match(desk.state.error, /your login does not reach this/);
  await desk.reload();
  assert.equal(desk.state.phase, "error");
});

test("placing a gallery entry lands it at the top under a fresh key, and asks for the save", async () => {
  const { desk } = deskOf();
  await desk.open();
  desk.openGallery();
  desk.placeEntry("deliveries-today");
  assert.equal(desk.state.widgets[0].widget_key, "deliveries.today");
  assert.equal(desk.state.widgets[0].label, "Deliveries today");
  assert.equal(desk.state.galleryOpen, false);
  assert.match(desk.state.notice, /Save to keep it/);
});

test("move, size step and remove act on the working set; the queue refuses to leave", async () => {
  const { desk } = deskOf();
  await desk.open();
  desk.moveWidget("orders.open", -5);
  assert.deepEqual(desk.state.widgets.map((w) => w.widget_key), ["orders.open", "needs.you"]);
  desk.sizeStep("orders.open", 1);
  assert.equal(desk.state.widgets.find((w) => w.widget_key === "orders.open").size, "L");
  desk.sizeStep("orders.open", -5);
  assert.equal(desk.state.widgets.find((w) => w.widget_key === "orders.open").size, "S");
  desk.removeWidget("orders.open");
  assert.deepEqual(desk.state.widgets.map((w) => w.widget_key), ["needs.you"]);
  desk.removeWidget("needs.you");
  assert.deepEqual(desk.state.widgets.map((w) => w.widget_key), ["needs.you"]);
  assert.match(desk.state.notice, /needs-you queue stays/);
});

test("a save PUTs the whole renumbered list; a refusal keeps the working set and shows the server's words", async () => {
  const { desk } = deskOf({ saveRefused: "a widget names a measure that is not in your catalog" });
  await desk.open();
  desk.placeEntry("deliveries-today");
  await desk.saveLayout();
  assert.match(desk.state.writeError, /a widget names a measure/);
  assert.equal(desk.state.widgets[0].widget_key, "deliveries.today");
  assert.equal(desk.state.busy, false);
  const good = deskOf();
  await good.desk.open();
  good.desk.removeWidget("orders.open");
  await good.desk.saveLayout();
  assert.match(good.desk.state.notice, /Your home is saved/);
  assert.equal(good.desk.state.writeError, "");
  assert.deepEqual(
    good.doors.calls.find((c) => c.startsWith("saveMyWidgets:")),
    "saveMyWidgets:needs.you",
  );
});

test("reset replaces the working set with the layout the reset landed on", async () => {
  const { desk } = deskOf();
  await desk.open();
  desk.placeEntry("deliveries-today");
  await desk.resetLayout();
  assert.deepEqual(desk.state.widgets.map((w) => w.widget_key), ["needs.you"]);
  assert.equal(desk.state.source, "role");
  assert.match(desk.state.notice, /back to the role default/);
});

test("tabs pin and unpin whole; a reset answers the strip the reset landed on", async () => {
  const { desk } = deskOf();
  await desk.open();
  desk.pinToggle("sales");
  assert.deepEqual(desk.state.tabs.map((p) => p.tab_key), ["yard", "sales"]);
  desk.pinToggle("yard");
  const yard = desk.state.tabs.find((p) => p.tab_key === "yard");
  assert.equal(yard.visible, false);
  assert.equal(desk.state.tabs.length, 2);
  desk.pinMove("sales", -1);
  assert.deepEqual(desk.state.tabs.filter((p) => p.visible).map((p) => p.tab_key), ["sales"]);
  await desk.saveTabs();
  assert.match(desk.state.notice, /pinned tabs are saved/);
  await desk.resetTabs();
  assert.equal(desk.state.tabs[0].tab_key, "yard");
  assert.equal(desk.state.tabs[0].visible, true);
});

test("the classic tiles toggle, save and reset through the same door", async () => {
  const { desk } = deskOf();
  await desk.open();
  assert.equal(desk.state.tiles[0].label, "Open orders");
  desk.tileToggle("orders.open");
  assert.equal(desk.state.tiles[0].visible, false);
  await desk.saveTiles();
  assert.match(desk.state.notice, /tiles are saved/);
  await desk.resetTiles();
  assert.equal(desk.state.tiles[0].visible, true);
});

test("the form opens on a corrected draft, previews through the composed id, and places", async () => {
  const { doors, desk } = deskOf();
  await desk.open();
  desk.openForm();
  assert.equal(desk.state.formOpen, true);
  assert.equal(desk.state.draft.measure, "orders.open");
  assert.equal(desk.state.previewTile, null);
  assert.match(desk.state.previewNote, /no chart preview/);
  desk.pick("measure", "deliveries.today");
  await desk.waitForPreview();
  assert.equal(desk.state.draft.range, "today");
  assert.deepEqual(doors.calls.filter((c) => c.startsWith("viz:")), ["viz:viz.deliveries.today-status-bar"]);
  assert.equal(desk.state.previewTile.total, 7);
  desk.pick("size", "L");
  desk.placeDraft();
  assert.equal(desk.state.widgets[0].widget_key, "deliveries.today");
  assert.equal(desk.state.widgets[0].size, "L");
  assert.equal(desk.state.formOpen, false);
});

test("the role scope is refused in the core's own words for a caller who is not an admin", async () => {
  const { desk } = deskOf({ profilesRefused: true });
  await desk.open();
  await desk.openRole();
  assert.equal(desk.state.scope, "role");
  assert.match(desk.state.roleError, /Forbidden/);
  assert.match(desk.state.roleError, /administrator/);
});

test("the role scope loads one profile's four defaults, and the picker switches", async () => {
  const { desk } = deskOf();
  await desk.open();
  await desk.openRole();
  assert.equal(desk.state.role.profile, "manager.yard");
  assert.deepEqual(desk.state.role.pins.map((p) => p.tab_key), ["purchasing"]);
  assert.deepEqual(desk.state.role.entries.map((e) => e.entry_key), ["e1"]);
  assert.equal(desk.state.roleLoading, false);
  await desk.pickProfile("manager.sales");
  assert.equal(desk.state.role.profile, "manager.sales");
});

test("a role widgets save that the core refuses shows Forbidden and keeps the working set", async () => {
  const { desk } = deskOf({ roleWriteRefused: true });
  await desk.open();
  await desk.openRole();
  await desk.saveRoleWidgets();
  assert.match(desk.state.writeError, /Forbidden/);
  assert.equal(desk.state.role.widgets.length, 1);
});

test("the role gallery edits wholesale: remove one entry and the save carries the rest", async () => {
  const { doors, desk } = deskOf();
  await desk.open();
  await desk.openRole();
  desk.roleEntryRemove("e1");
  assert.deepEqual(desk.state.role.entries, []);
  await desk.saveRoleGallery();
  assert.equal(doors.calls.find((c) => c.startsWith("saveProfileGallery:")), "saveProfileGallery:manager.yard:0");
});

test("the landing config saves through its own route, and a takeover without a unit is the core's refusal", async () => {
  const { desk } = deskOf();
  await desk.open();
  await desk.openRole();
  desk.setHome({ takeover: true, landing_unit: "yard", landing_module: "yard", mobile_only: true });
  await desk.saveRoleHome();
  assert.match(desk.state.notice, /landing is saved/);
  const refused = deskOf({ roleHomeRefused: "a takeover must name a landing unit" });
  await refused.desk.open();
  await refused.desk.openRole();
  refused.desk.setHome({ takeover: true, landing_unit: "", landing_module: "", mobile_only: false });
  await refused.desk.saveRoleHome();
  assert.match(refused.desk.state.writeError, /a takeover must name a landing unit/);
});

test("close drops the surface; act() dispatches the view's data-op controls", async () => {
  const { desk } = deskOf();
  await desk.open();
  desk.close();
  assert.equal(desk.state.phase, "closed");
  const { desk: d2 } = deskOf();
  await d2.open();
  const control = {
    getAttribute: (name) => (name === "data-op" ? "move" : name === "data-key" ? "orders.open" : name === "data-delta" ? "-1" : null),
  };
  d2.act(control);
  assert.deepEqual(d2.state.widgets.map((w) => w.widget_key), ["orders.open", "needs.you"]);
});
