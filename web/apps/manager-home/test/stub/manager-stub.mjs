// Manager Home's local stub: the demo way in, and the manager's world
// answered in the core's shapes (the dashboard profile rows 0381 seeds, the
// needs queue, the yard board with its shortages, the receiving worklist,
// the approvals list by kind). It is a development and test double only; the
// real routes are the core's (core/internal/dashboard, tasks, purchasing,
// identity/staffcontext). The stub contract test holds every payload below
// to the core's field list, field by field.
//
// TWO BEARERS, TWO PROFILES. The stub answers the dashboard routes per
// bearer, the way the core does: the yard manager's mint reads the
// manager.yard profile (the seven widgets its board draws), the sales
// manager's reads manager.sales. That is the one-app-per-role decision (5)
// made testable at the stub: the same code renders both landings from rows.
import { createServer } from "node:http";

const port = Number(process.env.MH_STUB_PORT ?? 4959);

// The real flows route lists every live role; the app offers only its own card.
const FLOWS = [
  { flow: "sales", role_code: "inside_sales", label: "Sales" },
  { flow: "sales-manager", role_code: "sales_manager", label: "Sales manager" },
  { flow: "yard-manager", role_code: "yard_manager", label: "Yard manager" },
];

const USERS = {
  "sales-manager": {
    user_id: "de110000-0000-4000-9000-000000000002",
    email: "sales.manager@hh-demo.invalid",
    display_name: "Sales manager (demo)",
    role_code: "sales_manager",
    role_name: "Sales manager",
    tier: 2,
    scope: "BRANCH",
  },
  "yard-manager": {
    user_id: "de110000-0000-4000-9000-000000000003",
    email: "yard.manager@hh-demo.invalid",
    display_name: "Yard manager (demo)",
    role_code: "yard_manager",
    role_name: "Yard manager",
    tier: 2,
    scope: "BRANCH",
  },
};

// A token with the shape the app reads an expiry from: three parts, the
// middle one base64url JSON carrying sub and exp. The signature is a stub.
function mintToken(sub) {
  const part = (value) => Buffer.from(JSON.stringify(value), "utf8").toString("base64url");
  const issuedAtSeconds = Math.floor(Date.now() / 1000);
  const payload = { iss: "hh-demo-stub", sub, aud: "staff", iat: issuedAtSeconds, exp: issuedAtSeconds + 3600 };
  return `${part({ alg: "ES256", typ: "JWT" })}.${part(payload)}.c3R1Yi1zaWduYXR1cmUtYnl0ZXM`;
}

function json(res, data, status = 200) {
  res.writeHead(status, { "Content-Type": "application/json" });
  res.end(JSON.stringify(data));
}

// The core's error envelope: {"error": sentence}. The role gate's shared
// Forbidden body is exactly {"error":"Forbidden"}; the list wall words the
// app maps it to are tested against that shape.
function refuse(res, status, message) {
  return json(res, { error: message }, status);
}

function notFound(res) {
  return refuse(res, 404, "not found");
}

function readBody(req) {
  return new Promise((resolve) => {
    const chunks = [];
    req.on("data", (c) => chunks.push(c));
    req.on("end", () => resolve(Buffer.concat(chunks)));
  });
}

// ---- the world -----------------------------------------------------------------------
//
// Trenton, the one branch the demo grants reach. Two open shortages on two
// picks, one resolved shortage beside them (the counter-case), one stale
// pick past its accept deadline, one purchase order PARTIAL with a short
// line (the receiving difference), and one margin approval the sales
// manager decides.

const BRANCH_ID = "5d1b3f6e-0000-4000-9000-000000000001";
const TASK_IN_PROGRESS = "07000000-0000-4000-9000-000000000007";
const TASK_STALE = "07000000-0000-4000-9000-000000000003";
const SHORTAGE_OPEN_1 = "0a000000-0000-4000-9000-0000000000e1";
const SHORTAGE_OPEN_2 = "0a000000-0000-4000-9000-0000000000e2";
const SHORTAGE_DONE = "0a000000-0000-4000-9000-0000000000e3";
const PO_PARTIAL = "0b000000-0000-4000-9000-0000000000p1";

const ago = (hours) => new Date(Date.now() - hours * 3600 * 1000).toISOString();

function buildWorld() {
  const shortages = [
    {
      id: SHORTAGE_OPEN_1,
      task_id: TASK_IN_PROGRESS,
      order_line_no: 1,
      product_id: "00000000-0000-4000-9000-0000000000p9",
      sku: "GRAN-A-CYD",
      description: "Granular A gravel",
      qty_short: "4.0000",
      uom: "CYD",
      note: "Only 8 yards left in the bin",
      reported_by: "",
      reported_at: ago(1),
    },
    {
      id: SHORTAGE_OPEN_2,
      task_id: TASK_IN_PROGRESS,
      order_line_no: 1,
      product_id: "00000000-0000-4000-9000-0000000000p7",
      sku: "HPB-DUST-T",
      description: "HPB dust, bulk",
      qty_short: "12.0000",
      uom: "T",
      note: "Pile is down to fines, nothing on order to cover it",
      reported_by: "",
      reported_at: ago(2),
    },
    {
      id: SHORTAGE_DONE,
      task_id: TASK_IN_PROGRESS,
      order_line_no: 1,
      product_id: "00000000-0000-4000-9000-0000000000p1",
      sku: "PAVE-60MM",
      description: "Standard paver, 60 mm",
      qty_short: "40.0000",
      uom: "PC",
      note: "Two banded layers were cracked",
      reported_by: "",
      reported_at: ago(48),
      resolved_at: ago(47),
      resolved_by: "de110000-0000-4000-9000-000000000003",
      resolution: "Substituted from the Trenton skid",
    },
  ];
  const tasks = [
    {
      task: {
        id: TASK_IN_PROGRESS,
        order_id: "0d000000-0000-4000-9000-000000000008",
        task_type: "PICK",
        status: "IN_PROGRESS",
        priority: "NORMAL",
        branch_id: BRANCH_ID,
        open_shortages: 2,
        total_shortages: 3,
        stage_location: "",
        stage_ref: "",
        created_at: ago(1),
      },
      ticket: {
        order_no: "SO-TRT-1042",
        customer_name: "K. Villeneuve (demo)",
        fulfillment: "WILL_CALL",
      },
      shortages,
    },
    {
      task: {
        id: TASK_STALE,
        order_id: "0d000000-0000-4000-9000-000000000001",
        task_type: "PICK",
        status: "PUSHED",
        priority: "URGENT",
        branch_id: BRANCH_ID,
        open_shortages: 0,
        total_shortages: 0,
        stage_location: "",
        stage_ref: "",
        created_at: ago(9),
        accept_deadline_at: ago(4),
      },
      ticket: { order_no: "SO-TRT-1038", customer_name: "Northline Hardscapes Ltd (demo)", fulfillment: "DELIVERY" },
      shortages: [],
    },
  ];
  return {
    shortages,
    tasks,
    needsByProfile: {
      "manager.yard": {
        profile: "manager.yard",
        rows: [
          {
            source_key: "tasks.open_shortages",
            what: "Open shortages",
            count: 2,
            age: "2h",
            age_minutes: 120,
            severity: "watch",
            module: "tasks",
            unit: "yard",
            screen: "yard",
            view: "Shortages · open",
            action: "Resolve",
          },
          {
            source_key: "tasks.stale_picks",
            what: "Picks past their deadline",
            count: 1,
            age: "26h",
            age_minutes: 1560,
            severity: "stop",
            module: "tasks",
            unit: "yard",
            screen: "yard",
            view: "Picks · past deadline",
            action: "Chase",
          },
          {
            source_key: "purchasing.receiving_differences",
            what: "Receipts that landed short",
            count: 1,
            age: "3h",
            age_minutes: 180,
            severity: "watch",
            module: "purchasing",
            unit: "yard",
            screen: "yard",
            view: "Receiving · short",
            action: "Settle",
          },
        ],
        total: 4,
        worst: "stop",
      },
      "manager.sales": {
        profile: "manager.sales",
        rows: [
          {
            source_key: "approvals.waiting",
            what: "Approvals waiting",
            count: 1,
            age: "45m",
            age_minutes: 45,
            severity: "watch",
            module: "identity",
            unit: "admin",
            screen: "approvals",
            view: "Waiting on you",
            action: "Review",
          },
        ],
        total: 1,
        worst: "watch",
      },
    },
    widgetsByProfile: {
      "manager.yard": {
        profile: "manager.yard",
        source: "role",
        widgets: [
          { widget_key: "hero.deliveries", shape: "stat", measure: "deliveries.today", split: "none", range: "today", size: "L", title: "Deliveries today", position: 0, visible: true, label: "Deliveries today", kind: "count", module: "dispatch", unit: "dispatch", screen: "dispatch", span_cols: 6, span_rows: 2 },
          { widget_key: "needs.you", shape: "list", measure: "needs.you", split: "none", range: "open", size: "L", title: "Needs you", position: 1, visible: true, label: "Needs you", kind: "count", module: "dashboard", span_cols: 6, span_rows: 2 },
          { widget_key: "picks.board", shape: "board", measure: "picks.queue", split: "status", range: "open", size: "L", title: "Pick queue", position: 2, visible: true, label: "Pick queue", kind: "count", module: "tasks", unit: "yard", screen: "yard", span_cols: 6, span_rows: 2 },
          { widget_key: "deliveries.branch", shape: "bars", measure: "deliveries.today", split: "branch", range: "today", size: "M", title: "By branch", position: 3, visible: true, label: "Deliveries today", kind: "count", module: "dispatch", span_cols: 6, span_rows: 1 },
          { widget_key: "picks.gauge", shape: "gauge", measure: "picks.queue", split: "none", range: "open", size: "S", title: "Queue depth", position: 4, visible: true, label: "Pick queue", kind: "count", module: "tasks", span_cols: 3, span_rows: 1 },
          { widget_key: "orders.open", shape: "stat", measure: "orders.open", split: "none", range: "open", size: "S", title: "Open orders", position: 5, visible: true, label: "Open orders", kind: "count", module: "order", span_cols: 3, span_rows: 1 },
          { widget_key: "warranty.open", shape: "stat", measure: "warranty.open_claims", split: "none", range: "open", size: "S", title: "Open claims", position: 6, visible: true, label: "Open claims", kind: "count", module: "warranty", span_cols: 3, span_rows: 1 },
        ],
      },
      "manager.sales": {
        profile: "manager.sales",
        source: "role",
        widgets: [
          { widget_key: "needs.you", shape: "list", measure: "needs.you", split: "none", range: "open", size: "L", title: "Needs you", position: 0, visible: true, label: "Needs you", kind: "count", module: "dashboard", span_cols: 6, span_rows: 2 },
          { widget_key: "sales.today", shape: "stat", measure: "sales.today", split: "none", range: "today", size: "M", title: "Today's sales", position: 1, visible: true, label: "Today's sales", kind: "money_cents", module: "pos", span_cols: 6, span_rows: 1 },
        ],
      },
    },
    dataByProfile: {
      "manager.yard": {
        "hero.deliveries": { widget_key: "hero.deliveries", state: "ok", total: 14, range: "today" },
        "needs.you": { widget_key: "needs.you", state: "ok", total: 4, range: "open" },
        "picks.board": {
          widget_key: "picks.board",
          state: "ok",
          total: 20,
          range: "open",
          points: [
            { label: "Waiting", number: 8 },
            { label: "Claimed", number: 5 },
            { label: "Picked", number: 3, tone: "good" },
            { label: "Staged", number: 4 },
          ],
        },
        "deliveries.branch": {
          widget_key: "deliveries.branch",
          state: "ok",
          total: 14,
          range: "today",
          points: [
            { label: "Trenton", number: 10 },
            { label: "Kingston", number: 4 },
          ],
        },
        "picks.gauge": { widget_key: "picks.gauge", state: "ok", total: 20, range: "open" },
        "orders.open": { widget_key: "orders.open", state: "ok", total: 12, range: "open" },
        "warranty.open": { widget_key: "warranty.open", state: "ok", total: 3, range: "open" },
      },
      "manager.sales": {
        "needs.you": { widget_key: "needs.you", state: "ok", total: 1, range: "open" },
        "sales.today": { widget_key: "sales.today", state: "ok", total: 123456, range: "today" },
      },
    },
    receiving: [
      {
        po_id: PO_PARTIAL,
        po_no: "PO-TRT-77",
        vendor_id: "0c000000-0000-4000-9000-0000000000v1",
        vendor_name: "Northland Stone Supply (demo)",
        branch_id: BRANCH_ID,
        status: "PARTIAL",
        expected_at: ago(-24).slice(0, 10),
        open_lines: 1,
        qty_outstanding_base: 2,
      },
      {
        po_id: "0b000000-0000-4000-9000-0000000000p2",
        po_no: "PO-TRT-78",
        vendor_id: "0c000000-0000-4000-9000-0000000000v2",
        vendor_name: "Bay Aggregate (demo)",
        branch_id: BRANCH_ID,
        status: "ORDERED",
        expected_at: ago(-72).slice(0, 10),
        open_lines: 3,
        qty_outstanding_base: 30,
      },
    ],
    approvals: [
      {
        id: "0e000000-0000-4000-9000-0000000000a1",
        requested_by: "de110000-0000-4000-9000-000000000001",
        branch_id: BRANCH_ID,
        policy_type: "MARGIN_BELOW_MINIMUM",
        detail: "Quote Q-2001 line 2 at 12% margin, floor is 15%",
        subject_ref: "quote:0f000000-0000-4000-9000-0000000000q1",
        amount_cents: 0,
        line_no: 2,
        disc_pct: 8,
        status: "PENDING",
        created_at: ago(1),
      },
    ],
  };
}

let world;
let scenario;
let log;

function reset() {
  world = buildWorld();
  scenario = "";
  log = [];
}
reset();

// The kinds the stub's yard manager decides: none, the matrix's lists name
// the financial controller and the general manager. The sales manager
// decides margin in branch.
function decidesMargin(flow) {
  return flow === "sales-manager";
}

function flowOf(token) {
  for (const [flow, account] of Object.entries(USERS)) {
    const payload = token.split(".")[1];
    if (payload === undefined) return null;
    try {
      const claims = JSON.parse(Buffer.from(payload, "base64url").toString("utf8"));
      if (claims.sub === account.user_id) return flow;
    } catch {
      return null;
    }
  }
  return null;
}

function profileOf(flow) {
  return flow === "yard-manager" ? "manager.yard" : "manager.sales";
}

function openShortageCount() {
  return world.shortages.filter((s) => s.resolved_at === undefined).length;
}

function yardNeeds() {
  // The queue is recomputed from the world, the way the core recounts per read.
  const rows = world.needsByProfile["manager.yard"].rows.map((row) => {
    if (row.source_key === "tasks.open_shortages") {
      return { ...row, count: openShortageCount() };
    }
    return row;
  }).filter((row) => row.count > 0);
  const total = rows.reduce((sum, row) => sum + row.count, 0);
  const worst = rows.some((r) => r.severity === "stop") ? "stop" : rows.some((r) => r.severity === "watch") ? "watch" : "good";
  return { profile: "manager.yard", rows, total, worst };
}

function needsFor(flow) {
  if (flow !== "yard-manager") return world.needsByProfile["manager.sales"];
  return yardNeeds();
}

async function handle(req, res) {
  const url = new URL(req.url, `http://127.0.0.1:${port}`);
  const path = url.pathname;
  if (path === "/healthz") return json(res, { ok: true });

  // ---- the stub's own controls (never the core's) ----
  if (req.method === "POST" && path === "/_stub/reset") {
    reset();
    return json(res, { ok: true });
  }
  if (req.method === "POST" && path === "/_stub/scenario") {
    const body = JSON.parse((await readBody(req)).toString("utf8"));
    scenario = String(body.name ?? "");
    return json(res, { ok: true, scenario });
  }
  if (req.method === "GET" && path === "/_stub/log") return json(res, log);

  // ---- the demo way in ----
  if (path === "/api/v1/demo/flows") return json(res, { flows: FLOWS });
  if (req.method === "POST" && path === "/api/v1/demo/session") {
    const body = await readBody(req);
    try {
      const { flow } = JSON.parse(body.toString("utf8"));
      const account = USERS[flow];
      if (!account) return notFound(res);
      return json(res, { token: mintToken(account.user_id), user: account, flow });
    } catch {
      return refuse(res, 400, "bad request");
    }
  }

  // ---- the staff routes the landing uses ----
  const authorization = req.headers.authorization ?? "";
  const raw = await readBody(req);
  let body = null;
  const contentType = String(req.headers["content-type"] ?? "").split(";")[0].trim();
  if (contentType === "application/json" && raw.length > 0) {
    try {
      body = JSON.parse(raw.toString("utf8"));
    } catch {
      return refuse(res, 400, "bad request");
    }
  }
  log.push({
    method: req.method,
    path,
    query: Object.fromEntries(url.searchParams),
    bearer: authorization.startsWith("Bearer "),
    org: req.headers["x-org"] ?? "",
    body,
  });
  if (!authorization.startsWith("Bearer ")) return refuse(res, 401, "unauthenticated");
  const flow = flowOf(authorization.slice("Bearer ".length));
  if (flow === null) return refuse(res, 401, "unauthenticated");
  const profile = profileOf(flow);

  if (req.method === "GET" && path === "/api/v1/branches") {
    return json(res, [
      {
        id: BRANCH_ID,
        code: "TRT",
        name: "Dibbits Landscape Supply · Trenton",
        address: { line1: "TBD", city: "Trenton", province: "ON", country: "CA" },
        is_default: true,
        is_system_default: false,
        is_active: true,
      },
    ]);
  }

  if (req.method === "GET" && path === "/api/v1/dashboard/needs") {
    return json(res, needsFor(flow));
  }

  if (req.method === "GET" && path === "/api/v1/dashboard/widgets") {
    return json(res, world.widgetsByProfile[profile]);
  }

  if (req.method === "GET" && path === "/api/v1/dashboard/widgets/data") {
    const keys = url.searchParams.getAll("key");
    const data = world.dataByProfile[profile];
    const out = keys.map((key) => {
      if (scenario === "unwired-one" && key === "warranty.open") {
        return { widget_key: key, state: "unwired", range: "open", note: "Not connected (warranty)" };
      }
      return data[key] ?? { widget_key: key, state: "unwired", range: "open", note: "Not connected" };
    });
    return json(res, { data: out });
  }

  if (req.method === "GET" && path === "/api/v1/yard/pick-queue") {
    const items = world.tasks.map((entry) => ({
      task: { ...entry.task, open_shortages: entry.shortages.filter((s) => s.resolved_at === undefined).length },
      ticket: entry.ticket,
      shortages: entry.shortages,
    }));
    return json(res, { items, next_cursor: null, limit: 50 });
  }

  const pickMatch = path.match(/^\/api\/v1\/yard\/picks\/([^/]+)$/);
  if (req.method === "GET" && pickMatch) {
    const entry = world.tasks.find((t) => t.task.id === pickMatch[1]);
    if (!entry) return notFound(res);
    return json(res, { task: entry.task, ticket: entry.ticket, shortages: entry.shortages });
  }

  const resolveMatch = path.match(/^\/api\/v1\/yard\/picks\/([^/]+)\/shortages\/([^/]+)\/resolve$/);
  if (req.method === "POST" && resolveMatch) {
    // The list wall, in the role gate's own shared body: a caller the
    // shortage_resolvers list does not name is refused with Forbidden, the
    // one word, and the app maps it to plain words naming who may act.
    if (scenario === "refused-resolve") return refuse(res, 403, "Forbidden");
    const shortage = world.shortages.find((s) => s.id === resolveMatch[2]);
    if (!shortage) return refuse(res, 404, "shortage not found");
    if (shortage.resolved_at !== undefined) return refuse(res, 409, "shortage already resolved");
    const resolution = String(body?.resolution ?? "").trim();
    if (resolution === "") return refuse(res, 400, "resolution is required");
    shortage.resolved_at = new Date().toISOString();
    shortage.resolved_by = USERS[flow].user_id;
    shortage.resolution = resolution;
    return json(res, shortage);
  }

  if (req.method === "GET" && path === "/api/v1/purchasing/receiving") {
    return json(res, { items: world.receiving, next_cursor: null, limit: 50 });
  }

  if (req.method === "GET" && path === "/api/v1/staff/approvals") {
    const kind = url.searchParams.get("kind") ?? "";
    if (kind !== "") {
      const known = ["MARGIN_BELOW_MINIMUM", "CREDIT_LIMIT_EXCEEDED", "COD_CONSTRAINT", "JE_OVER_THRESHOLD", "FREIGHT_ABOVE_QUOTE"];
      if (!known.includes(kind)) return refuse(res, 400, "unknown approval kind");
      if (!decidesMargin(flow)) return refuse(res, 403, `you do not decide ${kind} approvals`);
    }
    const status = url.searchParams.get("status") ?? "";
    const items = world.approvals.filter(
      (a) => (kind === "" || a.policy_type === kind) && (status === "" || a.status === status),
    );
    return json(res, { items, next_cursor: null, limit: 50 });
  }

  const decideMatch = path.match(/^\/api\/v1\/staff\/approvals\/([^/]+)\/decide$/);
  if (req.method === "POST" && decideMatch) {
    const approval = world.approvals.find((a) => a.id === decideMatch[1]);
    if (!approval) return notFound(res);
    if (!decidesMargin(flow)) return refuse(res, 403, "you do not decide MARGIN_BELOW_MINIMUM approvals");
    if (approval.status !== "PENDING") return refuse(res, 409, "approval already decided");
    approval.status = body?.approve === true ? "APPROVED" : "REJECTED";
    approval.decided_by = USERS[flow].user_id;
    return json(res, { ok: true });
  }

  return notFound(res);
}

const server = createServer((req, res) => {
  handle(req, res).catch(() => refuse(res, 500, "stub error"));
});

server.listen(port, "127.0.0.1", () => {
  process.stdout.write(`manager stub on 127.0.0.1:${port}\n`);
});
