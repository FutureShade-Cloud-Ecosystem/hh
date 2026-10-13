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
    ["sales", "sales-manager", "yard-manager"],
  );
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

test("the drivers board and its registries fit the dispatch contract", async () => {
  await reset();
  const token = await mint("yard-manager");
  const board = await call("GET", "/api/v1/dispatch/board?branch_id=5d1b3f6e-0000-4000-9000-000000000001", { token });
  assert.equal(board.status, 200);
  holds("DispatchBoard", board.json);
  assert.ok(board.json.runs.length > 0, "the world holds runs for the board's groups");
  for (const run of board.json.runs) {
    assert.ok(["PLANNED", "DISPATCHED", "COMPLETED", "CANCELLED"].includes(run.status), `run status ${run.status} outside the closed set`);
  }
  for (const stop of board.json.stops) {
    assert.ok(["READY", "LOADED", "EN_ROUTE", "DELIVERED", "FAILED", "CANCELLED"].includes(stop.status), `stop status ${stop.status} outside the closed set`);
  }

  const drivers = await call("GET", "/api/v1/dispatch/drivers?branch_id=5d1b3f6e-0000-4000-9000-000000000001&limit=100", { token });
  assert.equal(drivers.status, 200);
  holds("DispatchDriverPage", drivers.json);

  const trucks = await call("GET", "/api/v1/dispatch/trucks?branch_id=5d1b3f6e-0000-4000-9000-000000000001&limit=100", { token });
  assert.equal(trucks.status, 200);
  holds("DispatchTruckPage", trucks.json);

  // The empty day, the same shape with nothing on it.
  await scenario("no-runs");
  const empty = await call("GET", "/api/v1/dispatch/board?branch_id=5d1b3f6e-0000-4000-9000-000000000001", { token });
  assert.equal(empty.status, 200);
  holds("DispatchBoard", empty.json);
  assert.equal(empty.json.runs.length, 0);

  // The wall: the role gate's shared Forbidden body, the one word, exactly as
  // the middleware writes it, the same answer every staff surface gives.
  await scenario("drivers-refused");
  const refused = await call("GET", "/api/v1/dispatch/board?branch_id=5d1b3f6e-0000-4000-9000-000000000001", { token });
  assert.equal(refused.status, 403);
  assert.deepEqual(refused.json, { error: "Forbidden" });
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

test("a caller without a bearer is refused before any route answers", async () => {
  const res = await call("GET", "/api/v1/dashboard/needs", { token: "" });
  refused(res, 401, "unauthenticated");
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
