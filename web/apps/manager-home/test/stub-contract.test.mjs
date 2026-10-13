// The stub against the contract and against the core, field by field.
//
// A stub that drifts from the server makes every browser leg a flattering
// lie, so this test starts the stub, mints the way the app does, makes every
// call the landing makes, and holds each answer to the generated wire
// descriptors: required fields present, types and vocabularies right, and no
// field the contract does not declare (the descriptors are open objects, so
// the undeclared field check is this file's own). Then each refusal is held
// to the core's words: the status, the envelope, and the sentence itself,
// which must appear in the core's source (a formatted sentence checks its
// format string). A refusal the stub invented fails here.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { readdirSync, readFileSync } from "node:fs";
import { createServer } from "node:net";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { SCHEMA_DESCRIPTORS } from "../../../packages/api-client/src/testing.ts";
import { validateValue } from "../../../packages/api-client/test/_validate.mjs";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const coreDirs = [
  join(appDir, "..", "..", "..", "core", "internal", "dashboard"),
  join(appDir, "..", "..", "..", "core", "internal", "tasks"),
  join(appDir, "..", "..", "..", "core", "internal", "purchasing"),
  join(appDir, "..", "..", "..", "core", "internal", "identity", "staffcontext"),
  join(appDir, "..", "..", "..", "core", "internal", "platform", "authz"),
  join(appDir, "..", "..", "..", "core", "internal", "platform", "httpx"),
];

const coreSource = coreDirs
  .flatMap((dir) => readdirSync(dir).filter((f) => f.endsWith(".go") && !f.endsWith("_test.go")).map((f) => readFileSync(join(dir, f), "utf8")))
  .join("\n");

let child;
let base;

async function freePort() {
  return new Promise((resolve) => {
    const probe = createServer();
    probe.listen(0, "127.0.0.1", () => {
      const { port } = probe.address();
      probe.close(() => resolve(port));
    });
  });
}

before(async () => {
  const port = await freePort();
  base = `http://127.0.0.1:${port}`;
  child = spawn(process.execPath, [join(appDir, "test", "stub", "manager-stub.mjs")], {
    env: { MH_STUB_PORT: String(port) },
    stdio: "ignore",
  });
  for (let i = 0; i < 100; i += 1) {
    try {
      if ((await fetch(`${base}/healthz`)).ok) return;
    } catch {
      /* not up yet */
    }
    await new Promise((r) => setTimeout(r, 50));
  }
  throw new Error("the stub did not start");
});

after(() => {
  child?.kill();
});

async function call(method, path, { body, token = "stub-token" } = {}) {
  const headers = { Authorization: `Bearer ${token}`, "X-Org": "hh-demo" };
  let payload;
  if (body !== undefined) {
    headers["Content-Type"] = "application/json";
    payload = JSON.stringify(body);
  }
  const res = await fetch(`${base}${path}`, { method, headers, body: payload });
  const text = await res.text();
  let json = null;
  try {
    json = JSON.parse(text);
  } catch {
    /* not json */
  }
  return { status: res.status, json };
}

const reset = () => fetch(`${base}/_stub/reset`, { method: "POST" });
const scenario = (name) =>
  fetch(`${base}/_stub/scenario`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ name }) });

// ---- the declared keys of a descriptor, through refs and intersections ----

function declared(d) {
  if (d.k === "ref") return declared(SCHEMA_DESCRIPTORS[d.name]);
  if (d.k === "allOf") return Object.assign({}, ...d.of.map(declared));
  if (d.k === "object") return d.props;
  return {};
}

function undeclared(value, d, path = "value", out = []) {
  if (d.k === "ref") return undeclared(value, SCHEMA_DESCRIPTORS[d.name], path, out);
  if (d.k === "allOf") {
    const props = declared(d);
    if (value !== null && typeof value === "object" && !Array.isArray(value)) {
      for (const key of Object.keys(value)) if (props[key] === undefined) out.push(`${path}.${key}`);
      for (const [key, leg] of Object.entries(props)) if (value[key] !== undefined) undeclared(value[key], leg, `${path}.${key}`, out);
    }
    return out;
  }
  if (d.k === "object") {
    if (value === null || typeof value === "object" || Array.isArray(value)) {
      // fallthrough to the key walk below
    }
    if (value === null || typeof value !== "object" || Array.isArray(value)) return out;
    for (const key of Object.keys(value)) if (d.props[key] === undefined) out.push(`${path}.${key}`);
    for (const [key, leg] of Object.entries(d.props)) if (value[key] !== undefined) undeclared(value[key], leg, `${path}.${key}`, out);
    return out;
  }
  if (d.k === "array" && Array.isArray(value)) value.forEach((v, i) => undeclared(v, d.of, `${path}[${i}]`, out));
  if (d.k === "record" && value !== null && typeof value === "object") for (const [k, v] of Object.entries(value)) undeclared(v, d.of, `${path}.${k}`, out);
  if (d.k === "union") {
    const leg = d.of.find((l) => l.k !== "null");
    if (leg !== undefined && value !== null) undeclared(value, leg, path, out);
  }
  return out;
}

function holds(schemaName, value) {
  const descriptor = SCHEMA_DESCRIPTORS[schemaName];
  assert.ok(descriptor !== undefined, `no descriptor named ${schemaName}`);
  assert.deepEqual(validateValue(value, descriptor), [], `${schemaName} does not fit the contract`);
  assert.deepEqual(undeclared(value, descriptor), [], `${schemaName} carries fields the contract does not declare`);
}

function refused(res, status, fragment, formatString) {
  assert.equal(res.status, status);
  holds("Error", res.json);
  assert.ok(res.json.error.includes(fragment), `expected "${fragment}" in "${res.json.error}"`);
  assert.ok(
    coreSource.includes(formatString ?? fragment),
    `the core's source does not contain the sentence "${formatString ?? fragment}": the stub invented it`,
  );
}

/** Mints the demo way the app does and returns the bearer token. */
async function mint(flow) {
  const minted = await call("POST", "/api/v1/demo/session", { body: { flow }, token: "" });
  assert.equal(minted.status, 200);
  holds("DemoSessionResponse", minted.json);
  return minted.json.token;
}

test("the demo way in answers the contract's shapes for the flows this app's door serves", async () => {
  const flows = await (await fetch(`${base}/api/v1/demo/flows`)).json();
  holds("DemoFlowList", flows);
  assert.deepEqual(
    flows.flows.map((f) => f.flow),
    ["sales", "sales-manager", "yard-manager", "financial-controller"],
  );
});

test("the builder's reads fit the dashboard contract, field by field", async () => {
  await reset();
  const token = await mint("yard-manager");

  const home = await call("GET", "/api/v1/dashboard", { token });
  assert.equal(home.status, 200);
  holds("DashboardLayout", home.json);
  assert.equal(home.json.profile, "manager.yard");
  for (const tile of home.json.tiles) holds("DashboardTile", tile);
  holds("DashboardTabStrip", home.json.tabs);
  assert.ok(Array.isArray(home.json.hotkeys));

  const catalog = await call("GET", "/api/v1/dashboard/catalog", { token });
  assert.equal(catalog.status, 200);
  holds("DashboardCatalogView", catalog.json);
  for (const entry of catalog.json.tiles) {
    assert.ok(typeof entry.wired === "boolean", "a catalog entry carries its wiring state");
  }

  const widgetCatalog = await call("GET", "/api/v1/dashboard/widgets/catalog", { token });
  assert.equal(widgetCatalog.status, 200);
  holds("DashboardWidgetCatalog", widgetCatalog.json);
  for (const m of widgetCatalog.json.measures) holds("DashboardMetricMeta", m);

  const gallery = await call("GET", "/api/v1/dashboard/widgets/gallery", { token });
  assert.equal(gallery.status, 200);
  holds("DashboardGallery", gallery.json);
  for (const entry of gallery.json.entries) {
    assert.ok(typeof entry.label === "string" && entry.label !== "", "a gallery card arrives resolved: its words ride on it");
  }

  const metrics = await call("GET", "/api/v1/dashboard/metrics", { token });
  assert.equal(metrics.status, 200);
  holds("DashboardMetricCatalogView", metrics.json);

  const viz = await call("GET", "/api/v1/dashboard/viz?tile=viz.picks.queue-status-bar", { token });
  assert.equal(viz.status, 200);
  holds("DashboardVizResponse", viz.json);
  assert.equal(viz.json.tiles.length, 1);
  assert.equal(viz.json.tiles[0].state, "ok");
});

test("the landing's reads fit the dashboard contract, field by field", async () => {
  await reset();
  const token = await mint("yard-manager");
  const branches = await call("GET", "/api/v1/branches", { token });
  assert.equal(branches.status, 200);
  branches.json.forEach((b) => holds("Branch", b));

  const needs = await call("GET", "/api/v1/dashboard/needs", { token });
  assert.equal(needs.status, 200);
  holds("DashboardNeedsQueue", needs.json);
  assert.equal(needs.json.profile, "manager.yard");
  for (const row of needs.json.rows) {
    assert.ok(row.count > 0, "a needs row with count zero is absent by design, never on the wire");
    assert.ok(["good", "watch", "stop"].includes(row.severity));
  }

  const widgets = await call("GET", "/api/v1/dashboard/widgets", { token });
  assert.equal(widgets.status, 200);
  holds("DashboardWidgetLayout", widgets.json);
  assert.equal(widgets.json.widgets.length, 7, "the yard manager's profile carries its board's seven");
  for (const w of widgets.json.widgets) {
    assert.ok(["stat", "trend", "bars", "list", "board", "gauge"].includes(w.shape), `shape ${w.shape} outside the closed set`);
    assert.ok(w.position >= 0);
  }
  const order = widgets.json.widgets.map((w) => w.widget_key);
  assert.deepEqual(order, [
    "hero.deliveries",
    "needs.you",
    "picks.board",
    "deliveries.branch",
    "picks.gauge",
    "orders.open",
    "warranty.open",
  ]);

  const keys = order.map((k) => `key=${encodeURIComponent(k)}`).join("&");
  const data = await call("GET", `/api/v1/dashboard/widgets/data?${keys}`, { token });
  assert.equal(data.status, 200);
  holds("DashboardWidgetDataResponse", data.json);
  for (const datum of data.json.data) {
    if (datum.state === "ok") assert.ok(typeof datum.total === "number", "an ok datum carries its total");
    else assert.ok(datum.note !== "", "a not-ok datum explains itself in words");
  }
});

test("the yard board and the shortage resolve fit the tasks contract", async () => {
  await reset();
  const token = await mint("yard-manager");
  const queue = await call("GET", "/api/v1/yard/pick-queue?branch_id=5d1b3f6e-0000-4000-9000-000000000001&limit=50", { token });
  assert.equal(queue.status, 200);
  holds("TaskPickQueuePage", queue.json);
  const entry = queue.json.items.find((e) => e.task.open_shortages > 0);
  assert.ok(entry !== undefined);
  holds("TaskPickQueueEntry", entry);
  for (const s of entry.shortages) holds("TaskShortage", s);

  const detail = await call("GET", `/api/v1/yard/picks/${entry.task.id}`, { token });
  assert.equal(detail.status, 200);
  holds("TaskPickQueueEntry", detail.json);

  const openShortages = detail.json.shortages.filter((s) => s.resolved_at == null);
  assert.equal(openShortages.length, 2, "the world holds the two open shortages the needs row counts");

  // A blank resolution is refused before anything changes, on an open shortage.
  const blank = await call("POST", `/api/v1/yard/picks/${entry.task.id}/shortages/${openShortages[1].id}/resolve`, {
    body: { resolution: "  " },
    token,
  });
  refused(blank, 400, "resolution is required");

  const resolved = await call("POST", `/api/v1/yard/picks/${entry.task.id}/shortages/${openShortages[0].id}/resolve`, {
    body: { resolution: "Backordered the shortfall; the order keeps the short line open." },
    token,
  });
  assert.equal(resolved.status, 200);
  holds("TaskShortage", resolved.json);

  const again = await call("POST", `/api/v1/yard/picks/${entry.task.id}/shortages/${openShortages[0].id}/resolve`, {
    body: { resolution: "Twice." },
    token,
  });
  refused(again, 409, "shortage already resolved");

  await scenario("refused-resolve");
  const other = (await call("GET", "/api/v1/yard/pick-queue?branch_id=5d1b3f6e-0000-4000-9000-000000000001&limit=50", { token })).json.items
    .find((e) => e.task.open_shortages > 0)
    .shortages.find((s) => s.resolved_at == null);
  const walled = await call("POST", `/api/v1/yard/picks/${entry.task.id}/shortages/${other.id}/resolve`, {
    body: { resolution: "Approve the short pick." },
    token,
  });
  // The role gate's shared Forbidden body, the one word, exactly as the middleware writes it.
  assert.equal(walled.status, 403);
  assert.deepEqual(walled.json, { error: "Forbidden" });
  assert.ok(coreSource.includes('{"error":"Forbidden"}'), "the core's authz middleware does not write the shared Forbidden body");
});

test("the receiving dock fits the purchasing contract", async () => {
  await reset();
  const token = await mint("yard-manager");
  const list = await call("GET", "/api/v1/purchasing/receiving?branch_id=5d1b3f6e-0000-4000-9000-000000000001&limit=50", { token });
  assert.equal(list.status, 200);
  holds("PurchasingWorklistItemPage", list.json);
  assert.ok(list.json.items.some((i) => i.status === "PARTIAL"), "the stub's dock holds the difference the needs row counts");
});

test("the approvals list fits the identity contract, and the refusals are the core's own words", async () => {
  await reset();
  const yard = await mint("yard-manager");
  const sales = await mint("sales-manager");

  const whole = await call("GET", "/api/v1/staff/approvals", { token: sales });
  assert.equal(whole.status, 200);
  holds("IdentityApprovalPage", whole.json);

  const margin = await call("GET", "/api/v1/staff/approvals?kind=MARGIN_BELOW_MINIMUM", { token: sales });
  assert.equal(margin.status, 200);
  holds("IdentityApprovalPage", margin.json);
  assert.equal(margin.json.items.length, 1);

  const credit = await call("GET", "/api/v1/staff/approvals?kind=CREDIT_LIMIT_EXCEEDED", { token: sales });
  refused(credit, 403, "you do not decide CREDIT_LIMIT_EXCEEDED approvals", "you do not decide %s approvals");

  const unknown = await call("GET", "/api/v1/staff/approvals?kind=NOT_A_KIND", { token: sales });
  refused(unknown, 400, "unknown approval kind");

  const yardMargin = await call("GET", "/api/v1/staff/approvals?kind=MARGIN_BELOW_MINIMUM", { token: yard });
  refused(yardMargin, 403, "you do not decide MARGIN_BELOW_MINIMUM approvals", "you do not decide %s approvals");

  const decided = await call("POST", `/api/v1/staff/approvals/${margin.json.items[0].id}/decide`, {
    body: { approve: true, reason: "" },
    token: sales,
  });
  assert.equal(decided.status, 200);

  const yardDecide = await call("POST", `/api/v1/staff/approvals/${margin.json.items[0].id}/decide`, {
    body: { approve: true, reason: "" },
    token: yard,
  });
  refused(yardDecide, 403, "you do not decide MARGIN_BELOW_MINIMUM approvals", "you do not decide %s approvals");
});

test("the builder's writes fit the dashboard contract and answer what the write stored", async () => {
  await reset();
  const token = await mint("yard-manager");

  const saved = await call("PUT", "/api/v1/dashboard/widgets", {
    body: {
      widgets: [
        { widget_key: "needs.you", shape: "list", measure: "needs.you", split: "none", range: "open", size: "L", title: "Needs you", position: 0, visible: true },
        { widget_key: "orders.open", shape: "stat", measure: "orders.open", split: "none", range: "open", size: "S", title: "", position: 1, visible: true },
      ],
    },
    token,
  });
  assert.equal(saved.status, 200);
  holds("DashboardWidgetsBody", saved.json);

  const resetWidgets = await call("DELETE", "/api/v1/dashboard/widgets", { token });
  assert.equal(resetWidgets.status, 200);
  holds("DashboardWidgetsReset", resetWidgets.json);
  assert.ok(resetWidgets.json.layout.widgets.length > 0, "the reset answers the layout it landed on, never an empty shell");

  const tabs = await call("PUT", "/api/v1/dashboard/tabs", {
    body: { pins: [{ tab_key: "yard", position: 0, visible: true }, { tab_key: "sales", position: 1, visible: false }] },
    token,
  });
  assert.equal(tabs.status, 200);
  holds("DashboardTabStrip", tabs.json);

  const tabsReset = await call("DELETE", "/api/v1/dashboard/tabs", { token });
  assert.equal(tabsReset.status, 200);
  holds("DashboardTabsReset", tabsReset.json);

  const tiles = await call("PUT", "/api/v1/dashboard/layout", {
    body: { tiles: [{ tile_id: "orders.open", position: 0, visible: true }] },
    token,
  });
  assert.equal(tiles.status, 200);
  holds("DashboardLayoutBody", tiles.json);

  const tilesReset = await call("DELETE", "/api/v1/dashboard/layout", { token });
  assert.equal(tilesReset.status, 200);
  holds("DashboardLayoutReset", tilesReset.json);
});

test("the role defaults refuse a caller who is not an admin, in the core's own word, and answer an admin whole", async () => {
  await reset();
  const yard = await mint("yard-manager");
  const admin = await mint("financial-controller");

  const refusedProfiles = await call("GET", "/api/v1/dashboard/profiles", { token: yard });
  assert.equal(refusedProfiles.status, 403);
  assert.deepEqual(refusedProfiles.json, { error: "Forbidden" });

  const refusedDetail = await call("GET", "/api/v1/dashboard/profiles/manager.yard", { token: yard });
  assert.equal(refusedDetail.status, 403);

  const profiles = await call("GET", "/api/v1/dashboard/profiles", { token: admin });
  assert.equal(profiles.status, 200);
  holds("DashboardProfilesView", profiles.json);

  const detail = await call("GET", "/api/v1/dashboard/profiles/manager.yard", { token: admin });
  assert.equal(detail.status, 200);
  holds("DashboardProfileDetail", detail.json);

  const unknown = await call("GET", "/api/v1/dashboard/profiles/nowhere", { token: admin });
  refused(unknown, 404, "unknown dashboard profile");

  const widgets = await call("GET", "/api/v1/dashboard/profiles/manager.yard/widgets", { token: admin });
  assert.equal(widgets.status, 200);
  holds("DashboardProfileWidgets", widgets.json);

  const savedWidgets = await call("PUT", "/api/v1/dashboard/profiles/manager.yard/widgets", {
    body: {
      widgets: [
        { widget_key: "needs.you", shape: "list", measure: "needs.you", split: "none", range: "open", size: "L", title: "Needs you", position: 0, visible: true },
      ],
    },
    token: admin,
  });
  assert.equal(savedWidgets.status, 200);
  holds("DashboardProfileWidgets", savedWidgets.json);

  const tabs = await call("GET", "/api/v1/dashboard/profiles/manager.yard/tabs", { token: admin });
  assert.equal(tabs.status, 200);
  holds("DashboardProfileTabs", tabs.json);

  const savedTabs = await call("PUT", "/api/v1/dashboard/profiles/manager.yard/tabs", {
    body: { pins: [{ tab_key: "yard", position: 0, visible: true }] },
    token: admin,
  });
  assert.equal(savedTabs.status, 200);
  holds("DashboardProfileTabs", savedTabs.json);

  const gallery = await call("GET", "/api/v1/dashboard/profiles/manager.yard/gallery", { token: admin });
  assert.equal(gallery.status, 200);
  holds("DashboardProfileGallery", gallery.json);

  const savedGallery = await call("PUT", "/api/v1/dashboard/profiles/manager.yard/gallery", {
    body: { entries: [{ entry_key: "deliveries-today", shape: "stat", measure: "deliveries.today", split: "none", range: "today", size: "M", title: "Deliveries today", blurb: "B", position: 0, visible: true }] },
    token: admin,
  });
  assert.equal(savedGallery.status, 200);
  holds("DashboardProfileGallery", savedGallery.json);

  const savedTiles = await call("PUT", "/api/v1/dashboard/profiles/manager.yard", {
    body: { tiles: [{ tile_id: "orders.open", position: 0, visible: true }] },
    token: admin,
  });
  assert.equal(savedTiles.status, 200);
  holds("DashboardLayoutBody", savedTiles.json);

  const savedHome = await call("PUT", "/api/v1/dashboard/profiles/manager.yard/home", {
    body: { profile: "manager.yard", takeover: true, landing_unit: "yard", mobile_only: true },
    token: admin,
  });
  assert.equal(savedHome.status, 200);
  holds("DashboardHomeConfig", savedHome.json);

  const takeoverWithoutUnit = await call("PUT", "/api/v1/dashboard/profiles/manager.yard/home", {
    body: { profile: "manager.yard", takeover: true, mobile_only: false },
    token: admin,
  });
  refused(takeoverWithoutUnit, 400, "a takeover must name a landing unit");
});

test("a caller without a bearer is refused before any route answers", async () => {
  const res = await call("GET", "/api/v1/dashboard/needs", { token: "" });
  refused(res, 401, "unauthenticated");
});
