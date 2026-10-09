// Yard Pick's local stub: the demo way in, and the yard team's world
// answered in the core's shapes. It is a development and test double only;
// the real routes are the core's (core/internal/tasks, yard_handler.go).
// Item 7's contract test holds every payload below to the contract's field
// list, field by field, and every refusal to the core's own sentences.
//
// The rep notices are modelled the way the core builds them
// (core/internal/tasks/yard_notice.go): the double records the sentence the
// order's rep receives on a short pick, money included, because the notice
// is the rep's, not the yard's. The app never reads that channel; the leg
// reads it through the stub's own control endpoint, the double of the rep's
// API read the acceptance leg makes against the image.
import { createServer } from "node:http";

const port = Number(process.env.YARD_STUB_PORT ?? 4959);

// The flow table as the core's stands after the roles personas PR (274):
// the till's pair plus the Yard Team and the Driver. The app offers only its
// own card.
const FLOWS = [
  { flow: "sales", role_code: "inside_sales", label: "Sales" },
  { flow: "sales-manager", role_code: "sales_manager", label: "Sales manager" },
  { flow: "yard-team", role_code: "yard_team", label: "Yard Team" },
  { flow: "driver", role_code: "driver", label: "Driver" },
];

const USERS = {
  "yard-team": {
    user_id: "d0200000-0000-4000-9000-000000000002",
    email: "yard.demo@dibbits.example",
    display_name: "Yard persona (demo)",
    role_code: "yard_team",
    role_name: "Yard Team",
    tier: 3,
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

// The core's error envelope: {"error": sentence}, the module prefix already
// stripped (httpx.CounterMessage), except the role gate's shared Forbidden
// body, which is written by the gate itself and carries no sentence.
function refuse(res, status, message) {
  return json(res, { error: message }, status);
}

function notFound(res) {
  return refuse(res, 404, "task not found");
}

function readBody(req) {
  return new Promise((resolve) => {
    const chunks = [];
    req.on("data", (c) => chunks.push(c));
    req.on("end", () => resolve(Buffer.concat(chunks)));
  });
}

// ---- the world ---------------------------------------------------------
//
// One branch the yard team reaches (Trenton, the populate's own id), five
// pick tasks: two unclaimed, one claimed by the yard persona, one held from
// picking (the pay before release wall), one in progress with an open
// shortage and a resolved one.

const YARD_USER = "d0200000-0000-4000-9000-000000000002";
const TRENTON = "5d1b3f6e-0000-4000-9000-000000000001";

const BRANCH = { id: TRENTON, code: "TRT", name: "Hardscape House (demo), Trenton", address: { line1: "857 Bayly Rd", city: "Trenton", province: "ON", postal: "K8V 1N1", country: "CA" }, is_default: true, is_system_default: false, is_active: true };

const TICKETS = {
  "0b000000-0000-4000-9000-000000000042": {
    order_id: "0b000000-0000-4000-9000-000000000042",
    order_no: "SO-1042",
    branch_id: TRENTON,
    customer_name: "Northline Hardscapes Ltd (demo)",
    fulfillment: "PICKUP",
    mto_class: "STANDARD",
    priority: "URGENT",
    reference: "SO-1042",
    requested_at: "2026-10-08T13:00:00Z",
    source: "DISPATCH",
    lines: [
      { line_no: 1, product_id: "1a000000-0000-4000-9000-0000000000a1", sku: "PAVER-CH-1212", description: "Charcoal pavers, 12 by 12 in", qty_ordered: "24.0000", uom: "EA", location: "Yard A" },
      { line_no: 2, product_id: "1a000000-0000-4000-9000-0000000000a2", sku: "SAND-POLY-G", description: "Polymeric sand, grey, 18 kg", qty_ordered: "6.0000", uom: "BAG", location: "Rack 3" },
    ],
  },
  "0b000000-0000-4000-9000-000000000039": {
    order_id: "0b000000-0000-4000-9000-000000000039",
    order_no: "SO-1039",
    branch_id: TRENTON,
    customer_name: "Harbourview Builders (demo)",
    fulfillment: "DELIVERY",
    mto_class: "MIXED",
    priority: "HIGH",
    reference: "ST-2201",
    requested_at: "2026-10-08T15:00:00Z",
    source: "DISPATCH",
    lines: [
      { line_no: 1, product_id: "1a000000-0000-4000-9000-0000000000b1", sku: "WALL-BLK-CH", description: "Retaining wall block, charcoal", qty_ordered: "84.0000", uom: "EA", location: "Yard B" },
      { line_no: 2, product_id: "1a000000-0000-4000-9000-0000000000b2", sku: "HPB-BULK", description: "HPB bedding sand, bulk", qty_ordered: "2.5000", uom: "TONNE", location: "Bulk 2" },
      { line_no: 3, product_id: "1a000000-0000-4000-9000-0000000000b3", sku: "EDGE-REST", description: "Paver edge restraint, 10 ft", qty_ordered: "12.0000", uom: "EA", location: "Rack 7" },
      { line_no: 4, product_id: "1a000000-0000-4000-9000-0000000000b4", sku: "STONE-FLAG", description: "Flagstone, random, mixed grey", qty_ordered: "120.0000", uom: "SQFT", location: "Yard C" },
    ],
  },
  "0b000000-0000-4000-9000-000000000040": {
    order_id: "0b000000-0000-4000-9000-000000000040",
    order_no: "SO-1040",
    branch_id: TRENTON,
    customer_name: "County of Hastings (demo)",
    fulfillment: "DELIVERY",
    mto_class: "STANDARD",
    priority: "NORMAL",
    reference: "ST-2202",
    requested_at: "2026-10-08T16:00:00Z",
    source: "DISPATCH",
    lines: [
      { line_no: 1, product_id: "1a000000-0000-4000-9000-0000000000c1", sku: "GRAN-A", description: "Granular A gravel, bulk", qty_ordered: "10.0000", uom: "TONNE", location: "Bulk 1" },
      { line_no: 2, product_id: "1a000000-0000-4000-9000-0000000000c2", sku: "GRAN-B", description: "Granular B gravel, bulk", qty_ordered: "6.0000", uom: "TONNE", location: "Bulk 1" },
      { line_no: 3, product_id: "1a000000-0000-4000-9000-0000000000c3", sku: "FAB-GEO", description: "Geotextile fabric, 4 by 300 ft", qty_ordered: "2.0000", uom: "ROLL", location: "Rack 2" },
      { line_no: 4, product_id: "1a000000-0000-4000-9000-0000000000c4", sku: "PIPE-CORR-4", description: "Corrugated pipe, 4 in", qty_ordered: "8.0000", uom: "EA", location: "Rack 5" },
    ],
  },
  "0b000000-0000-4000-9000-000000000044": {
    order_id: "0b000000-0000-4000-9000-000000000044",
    order_no: "SO-1044",
    branch_id: TRENTON,
    customer_name: "Bayside Landscaping (demo)",
    fulfillment: "PICKUP",
    mto_class: "STANDARD",
    priority: "NORMAL",
    reference: "SO-1044",
    requested_at: "2026-10-08T17:00:00Z",
    source: "DISPATCH",
    held_from_picking: true,
    hold_words: "Balance due: this order waits until the balance is paid.",
    still_owed_cents: 43210,
    lines: [
      { line_no: 1, product_id: "1a000000-0000-4000-9000-0000000000d1", sku: "PAVER-HOLLAND", description: "Holland paver, sandstone", qty_ordered: "30.0000", uom: "EA", location: "Yard A" },
    ],
  },
  "0b000000-0000-4000-9000-000000000041": {
    order_id: "0b000000-0000-4000-9000-000000000041",
    order_no: "SO-1041",
    branch_id: TRENTON,
    customer_name: "Ridgeway Stone (demo)",
    fulfillment: "DELIVERY",
    mto_class: "MTO",
    priority: "HIGH",
    reference: "ST-2203",
    requested_at: "2026-10-08T12:00:00Z",
    source: "DISPATCH",
    lines: [
      { line_no: 1, product_id: "1a000000-0000-4000-9000-0000000000e1", sku: "STONE-FLAG", description: "Flagstone, random, mixed grey", qty_ordered: "46.0000", uom: "SQFT", location: "Yard C" },
      { line_no: 2, product_id: "1a000000-0000-4000-9000-0000000000e2", sku: "SAND-HPB", description: "HPB bedding sand, bulk", qty_ordered: "1.0000", uom: "TONNE", location: "Bulk 2" },
    ],
  },
};

const TASK_SEED = [
  { id: "0a000000-0000-4000-9000-000000000042", ticket: "0b000000-0000-4000-9000-000000000042", status: "QUEUED", priority: "URGENT", created_min: 95 },
  { id: "0a000000-0000-4000-9000-000000000039", ticket: "0b000000-0000-4000-9000-000000000039", status: "QUEUED", priority: "HIGH", created_min: 240 },
  { id: "0a000000-0000-4000-9000-000000000040", ticket: "0b000000-0000-4000-9000-000000000040", status: "ACCEPTED", priority: "NORMAL", claimed: true, created_min: 320 },
  { id: "0a000000-0000-4000-9000-000000000044", ticket: "0b000000-0000-4000-9000-000000000044", status: "QUEUED", priority: "NORMAL", created_min: 410 },
  { id: "0a000000-0000-4000-9000-000000000041", ticket: "0b000000-0000-4000-9000-000000000041", status: "IN_PROGRESS", priority: "HIGH", claimed: true, created_min: 500 },
];

let world;
let scenario;
let log;
let shortageSeq;
let repNotices;

function buildWorld() {
  const now = Date.now();
  const tasks = TASK_SEED.map((seed) => ({
    id: seed.id,
    task_type: "PICK",
    order_id: TICKETS[seed.ticket].order_id,
    branch_id: TRENTON,
    priority: seed.priority,
    status: seed.status,
    ...(seed.claimed ? { claimed_by: YARD_USER, claimed_at: new Date(now - 30 * 60000).toISOString() } : {}),
    source: "MANUAL",
    created_at: new Date(now - seed.created_min * 60000).toISOString(),
    updated_at: new Date(now - 10 * 60000).toISOString(),
    open_shortages: 0,
    total_shortages: 0,
    stage_location: "",
    stage_ref: "",
  }));
  // The in progress pick carries one open shortage and one cleared one.
  const openShortage = {
    id: "3a000000-0000-4000-9000-000000000001",
    task_id: "0a000000-0000-4000-9000-000000000041",
    order_line_no: 1,
    sku: "STONE-FLAG",
    description: "Flagstone, random, mixed grey",
    qty_short: "22.0000",
    uom: "SQFT",
    note: "Twenty two square feet came up short on the second pallet",
    reported_by: YARD_USER,
    reported_at: new Date(now - 45 * 60000).toISOString(),
    resolved_at: "",
    resolved_by: "",
    resolution: "",
  };
  const clearedShortage = {
    id: "3a000000-0000-4000-9000-000000000002",
    task_id: "0a000000-0000-4000-9000-000000000041",
    order_line_no: 2,
    sku: "SAND-HPB",
    description: "HPB bedding sand, bulk",
    qty_short: "0.5000",
    uom: "TONNE",
    note: "Half a tonne left the pile wet",
    reported_by: YARD_USER,
    reported_at: new Date(now - 120 * 60000).toISOString(),
    resolved_at: new Date(now - 100 * 60000).toISOString(),
    resolved_by: YARD_USER,
    resolution: "Substituted dry stock from Bulk 2 and the pick went out full",
  };
  const inProgress = tasks.find((t) => t.id === "0a000000-0000-4000-9000-000000000041");
  inProgress.open_shortages = 1;
  inProgress.total_shortages = 2;
  return {
    tasks,
    shortages: [openShortage, clearedShortage],
    picked: new Map(),
    skids: new Map(),
    notices: [],
  };
}

function reset() {
  world = buildWorld();
  scenario = "";
  log = [];
  shortageSeq = 2;
  repNotices = [];
}
reset();

function ticketFor(task) {
  const base = { ...TICKETS[Object.keys(TICKETS).find((k) => TICKETS[k].order_id === task.order_id)] };
  const picked = world.picked.get(task.id) ?? [];
  // A short pick backorders the line: ordered less picked, the order's own rule.
  base.lines = base.lines.map((line) => {
    const recorded = picked.find((p) => p.order_line_no === line.line_no);
    if (recorded === undefined) return { ...line };
    const ordered = Number(line.qty_ordered);
    const pickedQty = Number(recorded.qty_picked);
    const back = pickedQty < ordered ? (Number((ordered - pickedQty).toFixed(4))).toFixed(4) : "";
    return back === "" ? { ...line } : { ...line, qty_backordered: back };
  });
  return base;
}

function taskOut(task) {
  const shortages = world.shortages.filter((s) => s.task_id === task.id);
  const open = shortages.filter((s) => s.resolved_at === "").length;
  const out = { ...task, open_shortages: open, total_shortages: shortages.length };
  return out;
}

function entryOut(task, openOnly = false) {
  const shortages = world.shortages.filter((s) => s.task_id === task.id && (openOnly ? s.resolved_at === "" : true));
  const picked = world.picked.get(task.id) ?? [];
  const entry = { task: taskOut(task), ticket: ticketFor(task) };
  if (shortages.length > 0) entry.shortages = shortages.map(shortageOut);
  if (picked.length > 0) entry.picked = picked.map(pickedOut);
  return entry;
}

function shortageOut(s) {
  const out = { ...s };
  if (out.resolved_at === "") delete out.resolved_at;
  if (out.resolved_by === "") delete out.resolved_by;
  if (out.resolution === "") delete out.resolution;
  if (out.note === "") delete out.note;
  if (out.order_line_no === null) delete out.order_line_no;
  if (out.sku === "") delete out.sku;
  if (out.reported_by === "") delete out.reported_by;
  return out;
}

function pickedOut(p) {
  const out = { ...p };
  if (out.short_reason === "") delete out.short_reason;
  if (out.short_note === "") delete out.short_note;
  if (out.sku === "") delete out.sku;
  if (out.description === "") delete out.description;
  return out;
}

function findTask(id) {
  return world.tasks.find((t) => t.id === id);
}

const SHORT_REASONS = ["CONDENSED_SKIDS", "DAMAGED", "NOT_IN_YARD", "COUNT_CORRECTION", "OTHER"];

function reasonPhrase(code) {
  return {
    CONDENSED_SKIDS: "Skids condensed",
    DAMAGED: "Damaged",
    NOT_IN_YARD: "Not in the yard",
    COUNT_CORRECTION: "Count corrected",
    OTHER: "Other",
  }[code] ?? code;
}

// The notice the order's rep receives, the core's own sentence shape
// (yard_notice.go shortPickMessage): what was ordered, what was picked, what
// is on backorder, the reason, and the order's money. The rep's money rides
// the rep's channel; the yard's reads never carry it.
function shortPickNotice(orderNo, line, ordered, picked, backordered, reason, note) {
  let s = `Short pick on order ${orderNo}, line ${line.order_line_no} (${line.description}): ordered ${ordered} ${line.uom}, picked ${picked} ${line.uom}, ${backordered} ${line.uom} on backorder.`;
  if (reason !== "") s += ` Reason: ${reasonPhrase(reason)}${note !== "" ? `, ${note}` : ""}.`;
  s += ` The order total is now $1,234.56 (was $1,411.44).`;
  return s;
}

// ---- the routes ---------------------------------------------------------

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
  if (req.method === "GET" && path === "/_stub/state") return json(res, { tasks: world.tasks.map(taskOut), shortages: world.shortages });
  // The rep's API read, doubled: the notices the order's rep holds.
  if (req.method === "GET" && path === "/_stub/rep-notices") return json(res, repNotices);

  // ---- the demo way in ----
  if (path === "/api/v1/demo/flows") return json(res, { flows: FLOWS });
  if (req.method === "POST" && path === "/api/v1/demo/session") {
    const body = await readBody(req);
    try {
      const { flow } = JSON.parse(body.toString("utf8"));
      const account = USERS[flow];
      if (!account) return refuse(res, 404, "not found");
      return json(res, { token: mintToken(account.user_id), user: account, flow });
    } catch {
      return refuse(res, 400, "bad request");
    }
  }

  // ---- the routes the yard uses ----
  const yardRoute = path.startsWith("/api/v1/yard/") || path === "/api/v1/branches" || (path.startsWith("/api/v1/tasks/") && req.method === "POST");
  if (!yardRoute) return notFound(res);
  const authorization = req.headers.authorization ?? "";
  const raw = await readBody(req);
  let body = null;
  const contentType = String(req.headers["content-type"] ?? "").split(";")[0].trim();
  if (contentType === "application/json" && raw.length > 0) {
    try {
      body = JSON.parse(raw.toString("utf8"));
    } catch {
      return refuse(res, 400, "invalid_json");
    }
  }
  log.push({ method: req.method, path, query: Object.fromEntries(url.searchParams), bearer: authorization.startsWith("Bearer "), org: req.headers["x-org"] ?? "", contentType, body });
  if (!authorization.startsWith("Bearer ")) return refuse(res, 401, "unauthenticated");

  // The branches this login may reach. A login off the yard answers none.
  if (req.method === "GET" && path === "/api/v1/branches") {
    return json(res, scenario === "not-yard" ? [] : [BRANCH]);
  }

  if (req.method === "GET" && path === "/api/v1/yard/pick-queue") {
    // The branch fence (yard_handler.go): a branch-scoped caller must name a
    // reachable branch, and a branch out of reach is refused.
    const branchId = url.searchParams.get("branch_id") ?? "";
    if (branchId === "") return refuse(res, 403, "a branch-scoped caller must name a reachable branch_id");
    if (branchId !== TRENTON) return refuse(res, 403, "branch out of scope");
    const limit = Math.max(1, Math.min(100, Number(url.searchParams.get("limit") ?? "50")));
    let tasks = world.tasks.map(taskOut);
    const status = url.searchParams.get("status");
    if (status !== null && status !== "") tasks = tasks.filter((t) => t.status === status);
    // The core's stable order: open first, priority rank, created, id.
    const rank = { URGENT: 0, HIGH: 1, NORMAL: 2, LOW: 3 };
    const openFlag = (t) => (t.status === "QUEUED" || t.status === "PUSHED" ? 0 : 1);
    const sorted = [...tasks].sort((a, b) =>
      openFlag(a) - openFlag(b) ||
      (rank[a.priority] ?? 2) - (rank[b.priority] ?? 2) ||
      a.created_at.localeCompare(b.created_at) ||
      a.id.localeCompare(b.id),
    );
    if (scenario === "empty") return json(res, { items: [], next_cursor: "", limit });
    const cursor = url.searchParams.get("cursor") ?? "";
    let start = 0;
    if (cursor !== "") {
      if (!/^\d+$/.test(cursor)) return refuse(res, 400, "cursor is malformed");
      start = Number(cursor);
    }
    const page = sorted.slice(start, start + limit);
    const next = start + limit < sorted.length ? String(start + limit) : "";
    return json(res, { items: page.map((t) => entryOut(findTask(t.id), true)), next_cursor: next, limit });
  }

  const pickMatch = /^\/api\/v1\/yard\/picks\/([^/]+)(?:\/([a-z-]+))?$/.exec(path);
  if (pickMatch) {
    const task = findTask(pickMatch[1]);
    if (!task) return notFound(res);
    const action = pickMatch[2] ?? "";
    if (req.method === "GET" && action === "") return json(res, entryOut(task));
    return notFound(res);
  }

  const taskMatch = /^\/api\/v1\/tasks\/([^/]+)\/(claim|accept)$/.exec(path);
  if (req.method === "POST" && taskMatch) {
    const task = findTask(taskMatch[1]);
    if (!task) return notFound(res);
    return moveTask(res, task, taskMatch[2]);
  }
  return notFound(res);
}

const TRANSITIONS = {
  QUEUED: ["PUSHED", "ACCEPTED", "CANCELLED"],
  PUSHED: ["ACCEPTED", "CANCELLED"],
  ACCEPTED: ["IN_PROGRESS", "CANCELLED"],
  IN_PROGRESS: ["STAGED", "DONE", "CANCELLED"],
  STAGED: ["DONE", "CANCELLED"],
  DONE: [],
  CANCELLED: [],
};

function moveTask(res, task, move) {
  const to = move === "claim" ? "ACCEPTED" : "IN_PROGRESS";
  const ticket = ticketFor(task);
  // Item 9's pay before release wall: a held order's material does not reach
  // the yard, at claim and at stage, with the mandated sentence.
  if (ticket.held_from_picking === true) {
    return refuse(res, 409, "This order cannot reach picking until the balance is paid.");
  }
  if (task.status === to) return refuse(res, 409, "task already in that status");
  if (!(TRANSITIONS[task.status] ?? []).includes(to)) return refuse(res, 409, "illegal task transition");
  task.status = to;
  task.updated_at = new Date().toISOString();
  if (to === "ACCEPTED") {
    task.claimed_by = YARD_USER;
    task.claimed_at = new Date().toISOString();
  }
  return json(res, taskOut(task));
}

const server = createServer((req, res) => {
  handle(req, res).catch(() => refuse(res, 500, "internal error"));
});
server.listen(port, () => {
  console.log(`yard-stub listening on ${port}`);
});
