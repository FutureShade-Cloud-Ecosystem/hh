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
  join(appDir, "..", "..", "..", "core", "internal", "warranty"),
  join(appDir, "..", "..", "..", "core", "internal", "platform", "authz"),
  join(appDir, "..", "..", "..", "core", "internal", "platform", "httpx"),
  join(appDir, "..", "..", "..", "core", "internal", "reporting"),
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

// ---- reporting: the catalogue, the runs, the saved views, the register ----

const BRANCH = "5d1b3f6e-0000-4000-9000-000000000001";
const FROM = "2026-10-01";
const TO = "2026-10-14";

test("the reporting catalogue, a run and an export fit the reporting contract", async () => {
  await reset();
  const token = await mint("yard-manager");

  const catalog = await call("GET", "/api/v1/reporting/templates", { token });
  assert.equal(catalog.status, 200);
  holds("ReportingTemplateCatalog", catalog.json);
  assert.ok(catalog.json.categories.length > 0);
  for (const category of catalog.json.categories) {
    for (const t of category.templates) {
      holds("ReportingTemplate", t);
      for (const p of t.params) {
        assert.ok(["date", "uuid", "enum", "int"].includes(p.kind), `param kind ${p.kind} outside the closed set`);
      }
      for (const c of t.columns) {
        assert.ok(["text", "int", "cents", "qty", "date", "bool"].includes(c.kind), `column kind ${c.kind} outside the closed set`);
      }
    }
  }

  const code = catalog.json.categories[0].templates[0].code;
  const detail = await call("GET", `/api/v1/reporting/templates/${code}`, { token });
  assert.equal(detail.status, 200);
  holds("ReportingTemplate", detail.json);

  const run = await call("GET", `/api/v1/reporting/templates/${code}/run?from_date=${FROM}&to_date=${TO}&branch_id=${BRANCH}`, { token });
  assert.equal(run.status, 200);
  holds("ReportingResult", run.json);
  assert.equal(run.json.row_count, run.json.rows.length);
  for (const total of Object.values(run.json.totals)) assert.ok(typeof total === "number", "a total is a figure the server computed, never a string");

  const unknown = await call("GET", `/api/v1/reporting/templates/${code}/run?from_date=${FROM}&to_date=${TO}&branch=oops`, { token });
  refused(unknown, 400, 'unknown report parameter "branch"', 'unknown report parameter %q');

  const missing = await call("GET", `/api/v1/reporting/templates/${code}/run?from_date=${FROM}`, { token });
  refused(missing, 400, `report parameter "to_date" is required`, 'report parameter %q is required');

  await scenario("reporting-out-of-scope");
  const walled = await call("GET", `/api/v1/reporting/templates/${code}/run?from_date=${FROM}&to_date=${TO}`, { token });
  refused(walled, 403, "this report is outside your branch scope");
  await reset();

  const csv = await call("GET", `/api/v1/reporting/templates/${code}/export.csv?from_date=${FROM}&to_date=${TO}`, { token });
  assert.equal(csv.status, 200);
  assert.ok(typeof csv.json !== "object" || csv.json === null, "the export answers text, not a JSON envelope");
});

test("a print freezes a run, the document answers, and a consolidated print is refused in the core's words", async () => {
  await reset();
  const token = await mint("yard-manager");

  const consolidated = await call("POST", "/api/v1/reporting/templates/sales_by_day/print", {
    body: { params: { from_date: FROM, to_date: TO }, print_branch_id: "" },
    token,
  });
  refused(consolidated, 400, "this report covers every branch, name the branch to print it at");

  const print = await call("POST", "/api/v1/reporting/templates/sales_by_day/print", {
    body: { params: { from_date: FROM, to_date: TO, branch_id: BRANCH }, print_branch_id: "" },
    token,
  });
  assert.equal(print.status, 201);
  holds("ReportingRun", print.json);
  assert.ok(print.json.print_job_id !== "");

  const runs = await call("GET", "/api/v1/reporting/runs?limit=25", { token });
  assert.equal(runs.status, 200);
  holds("ReportingRunPage", runs.json);
  assert.ok(runs.json.items.some((r) => r.id === print.json.id), "the frozen print lands in the runs list");

  const one = await call("GET", `/api/v1/reporting/runs/${print.json.id}`, { token });
  assert.equal(one.status, 200);
  holds("ReportingRun", one.json);

  const doc = await call("GET", `/api/v1/reporting/runs/${print.json.id}/document`, { token });
  assert.equal(doc.status, 200);
  assert.ok(typeof doc.json !== "object" || doc.json === null, "the document answers html, not a JSON envelope");
});

test("saved views carry a name and answer the contract", async () => {
  await reset();
  const token = await mint("yard-manager");

  const list = await call("GET", "/api/v1/reporting/saved-views?template_code=sales_by_day", { token });
  assert.equal(list.status, 200);
  holds("ReportingSavedViewPage", list.json);

  const unnamed = await call("POST", "/api/v1/reporting/saved-views", {
    body: { template_code: "sales_by_day", name: "  ", params: { from_date: FROM, to_date: TO }, is_shared: false },
    token,
  });
  refused(unnamed, 400, "a saved view needs a name");

  const saved = await call("POST", "/api/v1/reporting/saved-views", {
    body: { template_code: "sales_by_day", name: "The fortnight", params: { from_date: FROM, to_date: TO }, is_shared: false },
    token,
  });
  assert.equal(saved.status, 201);
  holds("ReportingSavedView", saved.json);

  const dropped = await call("DELETE", `/api/v1/reporting/saved-views/${saved.json.id}`, { token });
  assert.equal(dropped.status, 204);
});

test("the commission register fits the contract, and the rule wall is the shared Forbidden body", async () => {
  await reset();
  const manager = await mint("yard-manager");
  const admin = await mint("financial-controller");

  const preview = await call("GET", `/api/v1/reporting/commission/preview?from=${FROM}&to=${TO}&branch_id=${BRANCH}`, { token: manager });
  assert.equal(preview.status, 200);
  holds("ReportingCommissionPeriodPage", preview.json);
  assert.equal(preview.json.posts_to_gl, false, "the register accrues and says so, it does not post");
  for (const period of preview.json.items) {
    holds("ReportingCommissionPeriod", { ...period, lines: undefined });
    for (const line of period.lines ?? []) {
      holds("ReportingCommissionLine", line);
      assert.ok(["REVENUE", "MARGIN"].includes(line.basis), `basis ${line.basis} outside the closed pair`);
    }
  }

  const badWindow = await call("GET", `/api/v1/reporting/commission/preview?from=nope&to=${TO}`, { token: manager });
  refused(badWindow, 400, "from must be a date (YYYY-MM-DD)");

  const accruals = await call("GET", `/api/v1/reporting/commission/accruals?from=${FROM}&to=${TO}`, { token: manager });
  assert.equal(accruals.status, 200);
  holds("ReportingAccrual", accruals.json);
  assert.ok(accruals.json.note.includes("Accrued, not posted"), "the register's caveat is the server's own sentence");

  const attribution = await call("GET", `/api/v1/reporting/commission/attribution-check?from=${FROM}&to=${TO}`, { token: manager });
  assert.equal(attribution.status, 200);
  holds("ReportingAttributionCheck", attribution.json);

  const runs = await call("GET", `/api/v1/reporting/commission/runs?from=${FROM}&to=${TO}`, { token: manager });
  assert.equal(runs.status, 200);
  holds("ReportingCommissionPeriodPage", runs.json);
  const stored = runs.json.items[0];
  assert.ok(stored.run_id !== undefined && stored.run_id !== "", "a stored run names its run");

  const one = await call("GET", `/api/v1/reporting/commission/runs/${stored.run_id}`, { token: manager });
  assert.equal(one.status, 200);
  holds("ReportingCommissionPeriod", one.json);
  for (const line of one.json.lines ?? []) holds("ReportingCommissionLine", line);

  const computed = await call("POST", "/api/v1/reporting/commission/runs", {
    body: { rep_user_id: stored.rep_user_id, from: FROM, to: TO, branch_id: "" },
    token: manager,
  });
  assert.equal(computed.status, 201);
  holds("ReportingCommissionPeriod", computed.json);

  const noRep = await call("POST", "/api/v1/reporting/commission/runs", {
    body: { rep_user_id: "", from: FROM, to: TO, branch_id: "" },
    token: manager,
  });
  refused(noRep, 400, "rep_user_id is required to compute a commission period");

  const consolidatedPrint = await call("POST", `/api/v1/reporting/commission/runs/${computed.json.run_id}/print`, {
    body: { print_branch_id: "" },
    token: manager,
  });
  refused(consolidatedPrint, 400, "this commission period covers every branch, name the branch to print it at");

  const crunCsv = await call("GET", `/api/v1/reporting/commission/runs/${stored.run_id}/export.csv`, { token: manager });
  assert.equal(crunCsv.status, 200);
  assert.ok(typeof crunCsv.json !== "object" || crunCsv.json === null, "the commission export answers text");

  // The rules read is a manager read; the writes are the admin window.
  const rules = await call("GET", "/api/v1/reporting/commission/rules", { token: manager });
  assert.equal(rules.status, 200);
  holds("ReportingCommissionRulePage", rules.json);

  const managerWrite = await call("POST", "/api/v1/reporting/commission/rules", {
    body: { rep_user_id: stored.rep_user_id, basis: "REVENUE", rate_bps: 250, effective_from: FROM },
    token: manager,
  });
  assert.equal(managerWrite.status, 403);
  assert.deepEqual(managerWrite.json, { error: "Forbidden" });
  assert.ok(coreSource.includes('{"error":"Forbidden"}'), "the core's authz middleware does not write the shared Forbidden body");

  const adminWrite = await call("POST", "/api/v1/reporting/commission/rules", {
    body: { rep_user_id: stored.rep_user_id, basis: "MARGIN", rate_bps: 100, effective_from: FROM },
    token: admin,
  });
  assert.equal(adminWrite.status, 201);
  holds("ReportingCommissionRule", adminWrite.json);
  assert.equal(adminWrite.json.is_active, true);

  const badRate = await call("POST", "/api/v1/reporting/commission/rules", {
    body: { rep_user_id: stored.rep_user_id, basis: "REVENUE", rate_bps: 20000, effective_from: FROM },
    token: admin,
  });
  refused(badRate, 400, "rate_bps must be between 0 and 10000 (0-100%)", "rate_bps must be between 0 and 10000 (0-100%%)");

  const retire = await call("DELETE", `/api/v1/reporting/commission/rules/${adminWrite.json.id}`, { token: admin });
  assert.equal(retire.status, 204);

  const managerRetire = await call("DELETE", `/api/v1/reporting/commission/rules/${rules.json.items[0].id}`, { token: manager });
  assert.equal(managerRetire.status, 403);
  assert.deepEqual(managerRetire.json, { error: "Forbidden" });
});

test("the warranty board and the claims list fit the warranty contract, field by field", async () => {
  await reset();
  const token = await mint("yard-manager");
  const board = await call("GET", "/api/v1/warranty/board", { token });
  assert.equal(board.status, 200);
  holds("WarrantyBoard", board.json);
  const populated = board.json.columns.filter((c) => c.claims.length > 0);
  assert.ok(populated.length >= 3, "the world carries claims in at least three phases");
  for (const col of board.json.columns) holds("WarrantyBoardColumn", col);
  for (const claim of board.json.columns.flatMap((c) => c.claims)) holds("WarrantyClaim", claim);

  const claims = await call("GET", "/api/v1/warranty/claims", { token });
  assert.equal(claims.status, 200);
  holds("WarrantyClaimPage", claims.json);

  const triage = await call("GET", "/api/v1/warranty/claims?phase=TRIAGE", { token });
  assert.equal(triage.status, 200);
  holds("WarrantyClaimPage", triage.json);
  assert.equal(triage.json.items.length, 1);
});

test("a warranty claim's detail, events, checklist and postings fit the contract, and the checklist gate speaks the core's sentence", async () => {
  await reset();
  const token = await mint("yard-manager");
  const board = (await (await call("GET", "/api/v1/warranty/board", { token })).json);
  const triageClaim = board.columns.find((c) => c.phase === "TRIAGE").claims[0];

  const detail = await call("GET", `/api/v1/warranty/claims/${triageClaim.id}`, { token });
  assert.equal(detail.status, 200);
  holds("WarrantyClaim", detail.json);

  const events = await call("GET", `/api/v1/warranty/claims/${triageClaim.id}/events`, { token });
  assert.equal(events.status, 200);
  holds("WarrantyClaimEventPage", events.json);

  const checklist = await call("GET", `/api/v1/warranty/claims/${triageClaim.id}/checklist`, { token });
  assert.equal(checklist.status, 200);
  holds("WarrantyChecklistStatePage", checklist.json);

  // The gate: the one required item still open is named in the core's own words.
  const blocked = await call("POST", `/api/v1/warranty/claims/${triageClaim.id}/advance`, {
    body: { to_phase: "SUBMITTED", note: "", occurred_at: "" },
    token,
  });
  refused(blocked, 422, "required checklist items incomplete", "warranty: cannot advance, required checklist items incomplete: %s");

  const tick = await call("POST", `/api/v1/warranty/claims/${triageClaim.id}/checklist`, {
    body: { item_key: "invoice_copy", checked: true },
    token,
  });
  assert.equal(tick.status, 200);
  holds("WarrantyChecklistState", tick.json);

  const moved = await call("POST", `/api/v1/warranty/claims/${triageClaim.id}/advance`, {
    body: { to_phase: "SUBMITTED", note: "", occurred_at: "" },
    token,
  });
  assert.equal(moved.status, 200);
  holds("WarrantyClaim", moved.json);
  assert.equal(moved.json.phase, "SUBMITTED");
});

test("a settlement records through the settle route and refuses a second one in the core's words", async () => {
  await reset();
  const token = await mint("yard-manager");
  const board = (await (await call("GET", "/api/v1/warranty/board", { token })).json);
  const decisionClaim = board.columns.find((c) => c.phase === "DECISION").claims[0];

  // Settlement books only from DECISION or SETTLEMENT: an early claim is refused, named.
  const early = await call("POST", `/api/v1/warranty/claims/${board.columns.find((c) => c.phase === "TRIAGE").claims[0].id}/settle`, {
    body: { outcome: "WRITE_OFF", expected_cents: 1, realized_cents: 1, note: "" },
    token,
  });
  refused(
    early,
    422,
    "cannot be settled from TRIAGE",
    "warranty: claim %s cannot be settled from %s, advance to DECISION or SETTLEMENT first",
  );

  // Advance to SETTLEMENT first (the phase's required item is ticked in the world).
  const advance = await call("POST", `/api/v1/warranty/claims/${decisionClaim.id}/advance`, {
    body: { to_phase: "SETTLEMENT", note: "", occurred_at: "" },
    token,
  });
  assert.equal(advance.status, 200);

  const settled = await call("POST", `/api/v1/warranty/claims/${decisionClaim.id}/settle`, {
    body: { outcome: "VENDOR_CREDIT", expected_cents: 24000, realized_cents: 24000, note: "credit approved" },
    token,
  });
  assert.equal(settled.status, 200);
  holds("WarrantySettlementResult", settled.json);
  assert.equal(settled.json.gl_posted, true);

  const postings = await call("GET", `/api/v1/warranty/claims/${decisionClaim.id}/postings`, { token });
  assert.equal(postings.status, 200);
  holds("WarrantySettlementPostingPage", postings.json);
  assert.equal(postings.json.items.length, 1);

  const again = await call("POST", `/api/v1/warranty/claims/${decisionClaim.id}/settle`, {
    body: { outcome: "WRITE_OFF", expected_cents: 1, realized_cents: 1, note: "" },
    token,
  });
  refused(
    again,
    409,
    "is already settled (VENDOR_CREDIT), re-settlement is not permitted in P1",
    "warranty: claim %s is already settled (%s), re-settlement is not permitted in P1",
  );
});

test("the vendor registry, the recovery board, the service levels and the reports fit the contract", async () => {
  await reset();
  const token = await mint("yard-manager");

  const vendors = await call("GET", "/api/v1/warranty/vendors", { token });
  assert.equal(vendors.status, 200);
  holds("WarrantyVendorProfilePage", vendors.json);
  assert.ok(vendors.json.items.some((v) => v.status === "UNDOCUMENTED"), "the world carries the explicit undocumented state");

  const one = await call("GET", `/api/v1/warranty/vendors/${vendors.json.items[0].vendor_id}`, { token });
  assert.equal(one.status, 200);
  holds("WarrantyVendorProfile", one.json);

  // The registry write is admin-gated: the shared Forbidden body, one word.
  const save = await call("PUT", `/api/v1/warranty/vendors/${vendors.json.items[0].vendor_id}`, {
    body: { vendor_id: vendors.json.items[0].vendor_id, status: "DOCUMENTED", coverage: "", term_months: null, channel: "EMAIL", channel_ref: "", docs: [], notes: "" },
    token,
  });
  assert.equal(save.status, 403);
  assert.deepEqual(save.json, { error: "Forbidden" });
  assert.ok(coreSource.includes('{"error":"Forbidden"}'), "the core's authz middleware does not write the shared Forbidden body");

  const recovery = await call("GET", "/api/v1/warranty/recovery", { token });
  assert.equal(recovery.status, 200);
  holds("WarrantyRecoveryBoard", recovery.json);
  assert.ok(recovery.json.total_outstanding_cents > 0, "the world carries the outstanding credit the chase log works down");

  const claimId = recovery.json.vendors[0].claims[0].claim_id;
  const chase = await call("GET", `/api/v1/warranty/claims/${claimId}/recovery`, { token });
  assert.equal(chase.status, 200);
  holds("WarrantyRecoveryActionPage", chase.json);

  const overReceipt = await call("POST", `/api/v1/warranty/claims/${claimId}/recovery`, {
    body: { action: "RECEIVED", amount_cents: recovery.json.total_outstanding_cents + 1, promised_date: "", reference: "", note: "", occurred_at: "" },
    token,
  });
  refused(
    overReceipt,
    422,
    "violates the amount rule",
    "warranty: the recovery action references an unknown claim/vendor, or violates the amount rule",
  );

  const receipt = await call("POST", `/api/v1/warranty/claims/${claimId}/recovery`, {
    body: { action: "RECEIVED", amount_cents: 15000, promised_date: "", reference: "CN-7719", note: "", occurred_at: "" },
    token,
  });
  assert.equal(receipt.status, 200);
  holds("WarrantyRecoveryAction", receipt.json);
  const after = await (await call("GET", "/api/v1/warranty/recovery", { token })).json;
  assert.equal(after.total_received_cents, 15000, "the board recomputes the subtraction server side");

  const sla = await call("GET", "/api/v1/warranty/sla", { token });
  assert.equal(sla.status, 200);
  holds("WarrantySLABoard", sla.json);
  assert.equal(sla.json.breached_count, 1);

  const targets = await call("GET", "/api/v1/warranty/sla/targets", { token });
  assert.equal(targets.status, 200);
  holds("WarrantySLATargetPage", targets.json);

  const ack = await call("POST", `/api/v1/warranty/claims/${sla.json.alerts[0].claim_id}/sla/${sla.json.alerts[0].id}/ack`, { token });
  assert.equal(ack.status, 200);

  const sweep = await call("POST", "/api/v1/warranty/sla/sweep", { token });
  assert.equal(sweep.status, 200);
  holds("WarrantySLASweepResult", sweep.json);

  const reports = await call("GET", "/api/v1/warranty/reports", { token });
  assert.equal(reports.status, 200);
  holds("WarrantyReportBundle", reports.json);
});

test("the warranty walls answer the shared Forbidden body, and the intake photos serve their bytes", async () => {
  await reset();
  const token = await mint("yard-manager");
  await scenario("refused-settle");
  const board = (await (await call("GET", "/api/v1/warranty/board", { token })).json);
  const decisionClaim = board.columns.find((c) => c.phase === "DECISION").claims[0];
  const walled = await call("POST", `/api/v1/warranty/claims/${decisionClaim.id}/settle`, {
    body: { outcome: "WRITE_OFF", expected_cents: 1, realized_cents: 1, note: "" },
    token,
  });
  assert.equal(walled.status, 403);
  assert.deepEqual(walled.json, { error: "Forbidden" });
  await reset();

  const webClaim = board.columns.find((c) => c.phase === "INTAKE").claims.find((c) => c.source === "WEB");
  assert.ok(webClaim !== undefined, "the world carries the public intake claim");
  const photos = await call("GET", `/api/v1/warranty/claims/${webClaim.id}/photos`, { token });
  assert.equal(photos.status, 200);
  holds("WarrantyIntakePhotoPage", photos.json);
  const res = await fetch(`${base}/api/v1/warranty/claims/${webClaim.id}/photos/${photos.json.items[0].id}`, {
    headers: { Authorization: `Bearer ${token}`, "X-Org": "hh-demo" },
  });
  assert.equal(res.status, 200);
  assert.match(res.headers.get("content-type") ?? "", /image\/png/);
});
