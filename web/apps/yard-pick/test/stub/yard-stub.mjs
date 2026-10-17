// Yard Pick's local stub: the demo way in, and the yard team's world
// answered in the core's shapes. It is a development and test double only;
// the real routes are the core's (core/internal/tasks, yard_handler.go, and
// the change queue of core/internal/order/change_queue.go). Item 7's contract
// test holds every payload below to the contract's field list, field by
// field, and every refusal to the core's own sentences.
//
// The rep notices are modelled the way the core builds them
// (core/internal/tasks/yard_notice.go): the double records the sentence the
// order's rep receives on a short pick, money included, because the notice
// is the rep's, not the yard's. The app never reads that channel; the leg
// reads it through the stub's own control endpoint, the double of the rep's
// API read the acceptance leg makes against the image.
//
// The yard payloads' money (the held ticket's still_owed_cents and the skid
// answer's deposit pair) is fenced by the caller's role the way the core
// fences it as merged with PR 302: see COST_LIST below.
import { createServer } from "node:http";

const port = Number(process.env.YARD_STUB_PORT ?? 4959);

// The flow table as the core's stands after the Manager Home merge (PR 298,
// main 04194fb2): the till's pair, the yard's two, the Driver and the office
// roles, in the table's own order. The app offers only its own card.
const FLOWS = [
  { flow: "sales", role_code: "inside_sales", label: "Sales" },
  { flow: "sales-manager", role_code: "sales_manager", label: "Sales manager" },
  { flow: "yard-manager", role_code: "yard_manager", label: "Yard manager" },
  { flow: "yard-team", role_code: "yard_team", label: "Yard Team" },
  { flow: "driver", role_code: "driver", label: "Driver" },
  { flow: "payables-receivables", role_code: "payables_receivables", label: "Payables and Receivables" },
  { flow: "procurement-manager", role_code: "procurement_manager", label: "Procurement manager" },
  { flow: "financial-controller", role_code: "financial_controller", label: "Financial Controller" },
];

const USERS = {
  "yard-team": {
    user_id: "d0200000-0000-4000-9000-000000000002",
    email: "yard.demo@hh-demo.invalid",
    display_name: "Yard persona (demo)",
    role_code: "yard_team",
    role_name: "Yard Team",
    tier: 3,
    scope: "BRANCH",
  },
  // The yard manager's persona: the core's closed table mints the login with
  // the Manager Home app (PR 298, on main), the core's yard routes answer a
  // yard_manager caller by role today, and that caller is on the cost and
  // margin list below, so the fence's kept half needs the persona here. The
  // app offers only its own card, so no other card or listing claims the
  // login here.
  "yard-manager": {
    user_id: "demo-account-yard-manager",
    email: "yard-manager@demo.invalid",
    display_name: "Yard manager (demo)",
    role_code: "yard_manager",
    role_name: "Yard Manager",
    tier: 2,
    scope: "BRANCH",
  },
};

// The yard money fence, the double of the core's (core/internal/tasks/
// cost_gate.go, merged with PR 302 under ruling 91): the figures a yard
// payload carries about what a customer owes ride only to a caller on the
// configuration's cost and margin list, the core's own default
// (core/internal/roleconfig/default.json, settings.cost_visibility). The
// yard manager is on that list; the yard team is not.
//
// THE SHAPE IS ABSENCE, NOT ZERO: a caller off the list receives each
// figure byte-absent, never a zero that reads as "nothing owed". The ticket
// keeps its hold mark and its words; the skid answer keeps its count.
const COST_LIST = new Set([
  "inside_sales",
  "outside_sales",
  "sales_manager",
  "yard_manager",
  "logistics_manager",
  "branch_manager",
  "procurement_manager",
  "payables_receivables",
  "financial_controller",
  "general_manager",
]);

// The caller the yard routes answer, read from the bearer the mint issued
// (the token's sub names the persona). The node legs' fixed stub-token and
// any token the stub cannot read are the yard persona, the app's own caller.
function callerRole(req) {
  const token = String(req.headers.authorization ?? "").replace(/^Bearer /, "");
  const parts = token.split(".");
  if (parts.length === 3) {
    try {
      const payload = JSON.parse(Buffer.from(parts[1], "base64url").toString("utf8"));
      const user = Object.values(USERS).find((u) => u.user_id === payload.sub);
      if (user !== undefined) return user.role_code;
    } catch {
      /* not the stub's token shape */
    }
  }
  return "yard_team";
}

// The caller's own id, the ack's by word (the demo mint's sub; the node legs'
// fixed token and anything unreadable are the yard persona).
function callerUserId(req) {
  const token = String(req.headers.authorization ?? "").replace(/^Bearer /, "");
  const parts = token.split(".");
  if (parts.length === 3) {
    try {
      const payload = JSON.parse(Buffer.from(parts[1], "base64url").toString("utf8"));
      const user = Object.values(USERS).find((u) => u.user_id === payload.sub);
      if (user !== undefined) return user.user_id;
    } catch {
      /* not the stub's token shape */
    }
  }
  return YARD_USER;
}

// blankTicketMoney, the double of the core's (tasks/cost_gate.go): the owed
// figure is held out of the ticket for a caller the cost list refuses. The
// hold mark and its words stay.
function blankTicketMoney(ticket, allowed) {
  if (allowed) return ticket;
  const out = { ...ticket };
  delete out.still_owed_cents;
  return out;
}

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
// One branch the yard team reaches (Point Loma, the populate's own id), five
// pick tasks: two unclaimed, one claimed by the yard persona, one held from
// picking (the pay before release wall), one in progress with an open
// shortage and a resolved one.

const YARD_USER = "d0200000-0000-4000-9000-000000000002";
const POINT_LOMA = "5d1b3f6e-0000-4000-9000-000000000001";

const BRANCH = { id: POINT_LOMA, code: "PLM", name: "Gable Landscape Supply, Point Loma", address: { line1: "857 Bayly Rd", city: "Point Loma", province: "CA", postal: "92106", country: "US" }, is_default: true, is_system_default: false, is_active: true };

const TICKETS = {
  "0b000000-0000-4000-9000-000000000042": {
    order_id: "0b000000-0000-4000-9000-000000000042",
    order_no: "SO-1042",
    branch_id: POINT_LOMA,
    customer_name: "Bluffside Builders",
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
    branch_id: POINT_LOMA,
    customer_name: "Harbourview Builders",
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
    branch_id: POINT_LOMA,
    customer_name: "Valley Municipal Works",
    fulfillment: "DELIVERY",
    mto_class: "STANDARD",
    priority: "NORMAL",
    reference: "ST-2202",
    requested_at: "2026-10-08T16:00:00Z",
    source: "DISPATCH",
    lines: [
      { line_no: 1, product_id: "1a000000-0000-4000-9000-0000000000c1", sku: "GRAN-A", description: "Class 2 base rock gravel, bulk", qty_ordered: "10.0000", uom: "TONNE", location: "Bulk 1" },
      { line_no: 2, product_id: "1a000000-0000-4000-9000-0000000000c2", sku: "GRAN-B", description: "Granular B gravel, bulk", qty_ordered: "6.0000", uom: "TONNE", location: "Bulk 1" },
      { line_no: 3, product_id: "1a000000-0000-4000-9000-0000000000c3", sku: "FAB-GEO", description: "Geotextile fabric, 4 by 300 ft", qty_ordered: "2.0000", uom: "ROLL", location: "Rack 2" },
      { line_no: 4, product_id: "1a000000-0000-4000-9000-0000000000c4", sku: "PIPE-CORR-4", description: "Corrugated pipe, 4 in", qty_ordered: "8.0000", uom: "EA", location: "Rack 5" },
    ],
  },
  "0b000000-0000-4000-9000-000000000044": {
    order_id: "0b000000-0000-4000-9000-000000000044",
    order_no: "SO-1044",
    branch_id: POINT_LOMA,
    customer_name: "Bayside Landscaping (demo)",
    fulfillment: "PICKUP",
    mto_class: "STANDARD",
    priority: "NORMAL",
    reference: "SO-1044",
    requested_at: "2026-10-08T17:00:00Z",
    source: "DISPATCH",
    held_from_picking: true,
    hold_words: "Balance due",
    still_owed_cents: 43210,
    lines: [
      { line_no: 1, product_id: "1a000000-0000-4000-9000-0000000000d1", sku: "PAVER-HOLLAND", description: "Charcoal paver, sandstone", qty_ordered: "30.0000", uom: "EA", location: "Yard A" },
    ],
  },
  "0b000000-0000-4000-9000-000000000041": {
    order_id: "0b000000-0000-4000-9000-000000000041",
    order_no: "SO-1041",
    branch_id: POINT_LOMA,
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

// The changes waiting on the yard (the core's change_orders, migration 0125,
// read through the change queue of change_queue.go): one under the threshold,
// one over it, so an acknowledgement lands on both of the ladder's next
// stages. The delta figures are the double of the core's stored numbers, held
// for the till's kind; the yard app's projection drops them.
const CHANGE_SEED = [
  {
    id: "5c000000-0000-4000-9000-000000000002",
    change_no: "CO-2043",
    order_id: "0b000000-0000-4000-9000-000000000039",
    order_no: "SO-1039",
    branch_id: POINT_LOMA,
    status: "PENDING_YARD",
    reason: "Customer halved the wall block, the load is too heavy for one truck",
    delta_cents: -63000,
    finance_threshold_cents: 50000,
    finance_required: true,
    requested_by_role: "Sales Manager",
    requested_min: 150,
    order_status_at_request: "READY",
    lines: [
      {
        seq: 1,
        op: "QTY",
        line_no: 1,
        product_id: "1a000000-0000-4000-9000-0000000000b1",
        product_name: "Retaining wall block",
        description: "Retaining wall block, charcoal",
        qty_entered_new: "42.0000",
        uom_entered: "EA",
        qty_base_before: 84,
        qty_base_after: 42,
        qty_delta_base: -42,
        unit_price_cents: 1500,
        price_uom: "EA",
        line_delta_cents: -63000,
      },
    ],
  },
  {
    id: "5c000000-0000-4000-9000-000000000001",
    change_no: "CO-2042",
    order_id: "0b000000-0000-4000-9000-000000000042",
    order_no: "SO-1042",
    branch_id: POINT_LOMA,
    status: "PENDING_YARD",
    reason: "Customer added two more pallets of the charcoal paver",
    delta_cents: 43200,
    finance_threshold_cents: 50000,
    finance_required: false,
    requested_by_role: "Inside Sales",
    requested_min: 50,
    order_status_at_request: "PICKING",
    lines: [
      {
        seq: 1,
        op: "QTY",
        line_no: 1,
        product_id: "1a000000-0000-4000-9000-0000000000a1",
        product_name: "Charcoal paver 60 mm",
        description: "Charcoal pavers, 12 by 12 in",
        qty_entered_new: "72.0000",
        uom_entered: "EA",
        qty_base_before: 24,
        qty_base_after: 72,
        qty_delta_base: 48,
        unit_price_cents: 900,
        price_uom: "EA",
        line_delta_cents: 43200,
      },
    ],
  },
];

let world;
let scenario;
let log;
let shortageSeq;
let repNotices;

const SYNC_SCOPES = ["pick_tasks", "shortages", "receiving", "inventory_lookup", "weights", "attachments"];
const PULL_ONLY = new Set(["inventory_lookup"]);
const WRITE_SCOPES = new Set(["pick_tasks", "shortages", "weights", "attachments"]);
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

const syncSeq = { pick_tasks: 0, shortages: 0, weights: 0, attachments: 0, receiving: 0 };
const syncRowSeq = { pick_tasks: new Map(), shortages: new Map(), weights: new Map(), attachments: new Map(), receiving: new Map() };
const appliedKeys = new Map();

function syncTouch(scope, id) {
  syncSeq[scope] = (syncSeq[scope] ?? 0) + 1;
  syncRowSeq[scope].set(id, syncSeq[scope]);
  return syncSeq[scope];
}

function syncSeqOf(scope, id) {
  return syncRowSeq[scope].get(id) ?? 0;
}


function buildWorld() {
  const now = Date.now();
  const tasks = TASK_SEED.map((seed) => ({
    id: seed.id,
    task_type: "PICK",
    order_id: TICKETS[seed.ticket].order_id,
    branch_id: POINT_LOMA,
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
  const nowMs = Date.now();
  const changes = CHANGE_SEED.map((seed) => ({
    ...seed,
    requested_at: new Date(nowMs - seed.requested_min * 60000).toISOString(),
  }));
  return {
    tasks,
    changes,
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
  appliedKeys.clear();
  // The seq books are NOT reset: a server's seq never walks backwards, and a
  // device that kept its store across a stub reset must see the fresh morning
  // rows as changes, not as rows it already holds.
  for (const task of world.tasks) syncTouch("pick_tasks", task.id);
  for (const s of world.shortages) syncTouch("shortages", s.id);
}
reset();

// The "poisoned" scenario is NOT the core's shape: it lays money fields on
// every read the yard makes, to prove the app neither reads nor shows them
// even if a server one day sent them. The clean shapes below are the core's.
export const POISON = 987654;

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
  if (scenario === "poisoned") {
    base.avg_cost_cents = POISON;
    base.gauge = { value_cents: POISON, unit_price: POISON };
    base.lines = base.lines.map((l) => ({ ...l, unit_cost_cents: POISON, unit_price: POISON, margin: POISON }));
  }
  return base;
}

function taskOut(task) {
  const shortages = world.shortages.filter((s) => s.task_id === task.id);
  const open = shortages.filter((s) => s.resolved_at === "").length;
  const out = { ...task, open_shortages: open, total_shortages: shortages.length };
  if (scenario === "poisoned") out.value_cents = POISON;
  return out;
}

function entryOut(task, openOnly = false, moneyAllowed = true) {
  const shortages = world.shortages.filter((s) => s.task_id === task.id && (openOnly ? s.resolved_at === "" : true));
  const picked = world.picked.get(task.id) ?? [];
  const entry = { task: taskOut(task), ticket: blankTicketMoney(ticketFor(task), moneyAllowed) };
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

// ---- the changes waiting on the yard (the core's change queue) ----------

const CHANGE_STATUSES = ["PENDING_YARD", "PENDING_FINANCE", "APPROVED", "APPLIED", "REJECTED", "CANCELLED"];
const TERMINAL_CHANGES = ["APPLIED", "REJECTED", "CANCELLED"];
// The core's changeNextActions (order/change.go) for a caller of each tier.
const MANAGER_ROLES = new Set(["sales_manager", "yard_manager", "branch_manager", "general_manager", "financial_controller"]);

function changeNextActions(status, isManager, isRequester = false) {
  // Reject is any staff while the yard decides and a manager's alone after; cancel is the requester or a
  // manager; sign_apply and finance_approve are a manager's (every manager is a named approver here).
  const cancel = isManager || isRequester ? ["cancel"] : [];
  switch (status) {
    case "PENDING_YARD":
      return ["yard_ack", "reject", ...cancel];
    case "PENDING_FINANCE":
      return [...(isManager ? ["sign_apply", "finance_approve", "reject"] : []), ...cancel];
    case "APPROVED":
      return [...(isManager ? ["sign_apply", "apply", "reject"] : []), ...cancel];
    default:
      return [];
  }
}

function poisonChange(out) {
  out.delta_cents = POISON;
  out.finance_threshold_cents = POISON;
  out.gauge = { value_cents: POISON, unit_price: POISON };
  // Queue rows carry no lines; the whole change does. Both get the poison.
  if (Array.isArray(out.lines)) out.lines = out.lines.map((l) => ({ ...l, unit_price_cents: POISON, line_delta_cents: POISON, margin: POISON }));
  return out;
}

/** The queue row: the core's ChangeQueueItem (change_queue_repository.go). */
function changeItemOut(change) {
  const out = {
    id: change.id,
    change_no: change.change_no,
    order_id: change.order_id,
    order_no: change.order_no,
    branch_id: change.branch_id,
    status: change.status,
    reason: change.reason,
    delta_cents: change.delta_cents,
    finance_threshold_cents: change.finance_threshold_cents,
    finance_required: change.finance_required,
    requested_by_role: change.requested_by_role,
    requested_at: change.requested_at,
    next_actions: changeNextActions(change.status, false),
  };
  if (change.yard_ack_note) out.yard_ack_note = change.yard_ack_note;
  return scenario === "poisoned" ? poisonChange(out) : out;
}

/** The whole change: the core's ChangeOrder, the acknowledgement's answer shape. */
function changeOut(change, isManager) {
  const out = {
    id: change.id,
    order_id: change.order_id,
    branch_id: change.branch_id,
    change_no: change.change_no,
    status: change.status,
    reason: change.reason,
    delta_cents: change.delta_cents,
    finance_threshold_cents: change.finance_threshold_cents,
    finance_required: change.finance_required,
    order_status_at_request: change.order_status_at_request,
    pick_started_at_request: true,
    requested_by: "d0100000-0000-4000-9000-0000000000a1",
    requested_at: change.requested_at,
    lines: change.lines.map((l) => ({ ...l })),
    next_actions: changeNextActions(change.status, isManager),
  };
  if (change.yard_acked_by) out.yard_acked_by = change.yard_acked_by;
  if (change.yard_acked_at) out.yard_acked_at = change.yard_acked_at;
  if (change.yard_ack_note) out.yard_ack_note = change.yard_ack_note;
  return scenario === "poisoned" ? poisonChange(out) : out;
}

function changeQueue(res, url) {
  const rawStatus = url.searchParams.get("status") ?? "";
  const status = rawStatus.toUpperCase();
  if (rawStatus !== "" && !CHANGE_STATUSES.includes(status)) {
    return refuse(res, 400, `unknown status "${rawStatus}" (${CHANGE_STATUSES.join("|")})`);
  }
  if (scenario === "empty") return json(res, { items: [], next_cursor: null, limit: 50 });
  // A yard with a full day of changes: twelve waiting rows, oldest first,
  // the same shapes as the two seeded ones, so the phone legs can open a
  // row below the first screen.
  if (scenario === "changes-many") {
    const nowMs = Date.now();
    const rows = Array.from({ length: 12 }, (_, index) =>
      changeItemOut({
        id: `5c000000-0000-4000-9000-0000000001${index + 10}`,
        change_no: `CO-21${index + 10}`,
        order_id: "0b000000-0000-4000-9000-000000000042",
        order_no: `SO-11${index + 10}`,
        branch_id: POINT_LOMA,
        status: "PENDING_YARD",
        reason: "Customer added two more pallets of the charcoal paver",
        requested_by_role: "Inside Sales",
        requested_at: new Date(nowMs - (12 - index) * 60000).toISOString(),
      }),
    );
    return json(res, { items: rows, next_cursor: null, limit: 50 });
  }
  const limit = Math.max(1, Math.min(100, Number(url.searchParams.get("limit") ?? "50")));
  const rows = world.changes
    .filter((c) => status === "" || c.status === status)
    .sort((a, b) => a.requested_at.localeCompare(b.requested_at) || a.id.localeCompare(b.id));
  const cursor = url.searchParams.get("cursor") ?? "";
  let start = 0;
  if (cursor !== "") {
    if (!/^\d+$/.test(cursor)) return refuse(res, 400, "cursor is malformed");
    start = Number(cursor);
  }
  const page = rows.slice(start, start + limit);
  const next = start + limit < rows.length ? String(start + limit) : null;
  return json(res, { items: page.map(changeItemOut), next_cursor: next, limit });
}

function yardAck(res, changeId, input, req) {
  const change = world.changes.find((c) => c.id === changeId);
  if (!change) return refuse(res, 404, "not found");
  // A colleague's acknowledgement this screen never saw: the race the core
  // refuses by name (change.go YardAck), scripted so a browser leg can meet it.
  if (scenario === "change-decided") {
    return refuse(res, 409, "this change order is already closed: the yard has already acknowledged this change");
  }
  if (change.status !== "PENDING_YARD") {
    if (TERMINAL_CHANGES.includes(change.status)) return refuse(res, 409, "this change order is already closed");
    return refuse(res, 409, "this change order is already closed: the yard has already acknowledged this change");
  }
  // The note is optional and clamped, never refused (the core's clampNote).
  const note = String(input.note ?? "").trim().slice(0, 400);
  change.status = change.finance_required ? "PENDING_FINANCE" : "APPROVED";
  change.yard_acked_by = callerUserId(req);
  change.yard_acked_at = new Date().toISOString();
  if (note !== "") change.yard_ack_note = note;
  return json(res, changeOut(change, MANAGER_ROLES.has(callerRole(req))));
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
  // The server reassigns a ticket out from under the device: the status moves
  // and the row's seq moves with it, the server wins story the exit test asks
  // for ("a pick recorded offline against a ticket the server reassigned").
  // The desk settles a shortage while the device cannot hear it: the row
  // moves and its seq moves with it, so the device's queued resolution lands
  // stale, and the manual story plays for real.
  if (req.method === "POST" && path === "/_stub/desk-resolve") {
    const body = JSON.parse((await readBody(req)).toString("utf8"));
    const shortage = world.shortages.find((s) => s.id === String(body.shortage_id ?? ""));
    if (!shortage) return notFound(res);
    if (shortage.resolved_at !== undefined && shortage.resolved_at !== "") return refuse(res, 409, "shortage already resolved");
    shortage.resolved_at = new Date().toISOString();
    shortage.resolved_by = "desk";
    shortage.resolution = String(body.resolution ?? "Settled at the counter");
    syncTouch("shortages", shortage.id);
    return json(res, { ok: true, seq: syncSeqOf("shortages", shortage.id) });
  }
  if (req.method === "POST" && path === "/_stub/reassign") {
    const body = JSON.parse((await readBody(req)).toString("utf8"));
    const task = findTask(String(body.task_id ?? ""));
    if (!task) return notFound(res);
    task.status = "CANCELLED";
    task.claimed_by = "dispatch";
    task.updated_at = new Date().toISOString();
    syncTouch("pick_tasks", task.id);
    return json(res, { ok: true, seq: syncSeqOf("pick_tasks", task.id) });
  }
  // The desk changes an OPEN ticket (its grade): the row's seq moves and the task stays open, so a record
  // based on the older seq is superseded (the engine's stale decision) rather than refused as closed.
  if (req.method === "POST" && path === "/_stub/touch") {
    const body = JSON.parse((await readBody(req)).toString("utf8"));
    const task = findTask(String(body.task_id ?? ""));
    if (!task) return notFound(res);
    task.priority = task.priority === "HIGH" ? "NORMAL" : "HIGH";
    task.updated_at = new Date().toISOString();
    syncTouch("pick_tasks", task.id);
    return json(res, { ok: true, seq: syncSeqOf("pick_tasks", task.id) });
  }
  if (req.method === "GET" && path === "/_stub/log") return json(res, log);
  // The rep's API read, doubled: the notices the order's rep holds.
  if (req.method === "GET" && path === "/_stub/rep-notices") return json(res, repNotices);
  if (req.method === "GET" && path === "/_stub/state") return json(res, { tasks: world.tasks.map(taskOut), shortages: world.shortages });

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
  const changeAckMatch = /^\/api\/v1\/order-changes\/([^/]+)\/yard-ack$/.exec(path);
  const yardRoute =
    path.startsWith("/api/v1/yard/") ||
    path === "/api/v1/branches" ||
    (path.startsWith("/api/v1/tasks/") && req.method === "POST") ||
    path.startsWith("/api/v1/sync/") ||
    // The change queue and its acknowledgement (the core's order module,
    // change_queue.go): the two routes the yard's stage 2 screen calls.
    path === "/api/v1/order-changes" ||
    (changeAckMatch !== null && req.method === "POST");
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
    if (branchId !== POINT_LOMA) return refuse(res, 403, "branch out of scope");
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
    const allowed = COST_LIST.has(callerRole(req));
    return json(res, { items: page.map((t) => entryOut(findTask(t.id), true, allowed)), next_cursor: next, limit });
  }

  // The change queue: the yard's stage of the ladder, oldest first, the
  // core's page envelope (items, next_cursor null on the last page, limit).
  if (req.method === "GET" && path === "/api/v1/order-changes") {
    return changeQueue(res, url);
  }
  if (req.method === "POST" && changeAckMatch) {
    return yardAck(res, changeAckMatch[1], body ?? {}, req);
  }

  const pickMatch = /^\/api\/v1\/yard\/picks\/([^/]+)(?:\/([a-z-]+))?$/.exec(path);
  if (pickMatch) {
    const task = findTask(pickMatch[1]);
    if (!task) return notFound(res);
    const action = pickMatch[2] ?? "";
    const allowed = COST_LIST.has(callerRole(req));
    if (req.method === "GET" && action === "") return json(res, entryOut(task, false, allowed));
    if (req.method === "POST" && action === "stage") return stage(res, task, body ?? {});
    if (req.method === "POST" && action === "shortages") return reportShortage(res, task, body ?? {});
    if (req.method === "PUT" && action === "lines") return recordPick(res, task, body ?? {});
    if (req.method === "PUT" && action === "skids") return recordSkids(res, task, body ?? {}, allowed);
    return notFound(res);
  }

  const shortageMatch = /^\/api\/v1\/yard\/picks\/([^/]+)\/shortages\/([^/]+)\/resolve$/.exec(path);
  if (req.method === "POST" && shortageMatch) {
    const task = findTask(shortageMatch[1]);
    if (!task) return notFound(res);
    return resolveShortage(res, task, shortageMatch[2], body ?? {});
  }

  const taskMatch = /^\/api\/v1\/tasks\/([^/]+)\/(claim|accept)$/.exec(path);
  if (req.method === "POST" && taskMatch) {
    const task = findTask(taskMatch[1]);
    if (!task) return notFound(res);
    return moveTask(res, task, taskMatch[2]);
  }

  if (path.startsWith("/api/v1/sync/")) return handleSync(req, res, url, body);
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
  syncTouch("pick_tasks", task.id);
  return json(res, taskOut(task));
}

function parseQty(raw) {
  const v = String(raw ?? "").trim();
  if (v === "") return { error: null, empty: true };
  const n = Number(v);
  if (!Number.isFinite(n)) return { error: "a picked quantity must be a number" };
  return { value: n, empty: false };
}

function recordPick(res, task, input) {
  const record = recordPickedLines(task, input.lines);
  if (record.error) return refuse(res, record.status ?? 400, record.error);
  task.updated_at = new Date().toISOString();
  syncTouch("pick_tasks", task.id);
  return json(res, { items: record.picked.map(pickedOut) });
}

let pickedCounter = 0;
function pickedSeq() {
  pickedCounter += 1;
  return pickedCounter;
}


function recordSkids(res, task, input, moneyAllowed = true) {
  if (input.skid_count === undefined || input.skid_count === null) {
    return refuse(res, 400, "skid_count is required, zero is an answer");
  }
  const count = Number(input.skid_count);
  if (!Number.isInteger(count) || count < 0) return refuse(res, 400, "a skid count cannot be negative");
  if (count > 5000) return refuse(res, 400, "that skid count is implausibly large");
  const reason = String(input.reason ?? "").trim();
  const note = String(input.note ?? "").trim();
  if (note.length > 400) return refuse(res, 400, "the note is too long (max 400)");
  if (reason === "" || !SHORT_REASONS.includes(reason)) {
    return refuse(res, 400, `give the reason for the skid count (${SHORT_REASONS.join(", ")})`);
  }
  if (reason === "OTHER" && note === "") return refuse(res, 400, "say what happened in the note when the reason is OTHER");
  const ticket = ticketFor(task);
  const before = world.skids.get(task.id) ?? null;
  const after = count;
  const changed = before !== after;
  world.skids.set(task.id, after);
  syncTouch("weights", task.id);
  // The skid deposit is the office's money: it rides the core's answer (the
  // contract declares it) for a caller on the cost list alone, byte-absent
  // for every other caller (COST_LIST above); the app's projection drops it
  // either way.
  const answer = {
    skid_count: after,
    previous_skid_count: before,
    changed,
    order_no: ticket.order_no,
  };
  if (moneyAllowed) {
    answer.deposit_cents = changed ? 4500 * after : 4500 * (before ?? 0);
    answer.previous_deposit_cents = before === null ? 0 : 4500 * before;
  }
  return json(res, answer);
}




// stableRef: the stub's stand in for the core's shortRef (a stable 6-char
// handle off the task id, the alphabet without I O 0 1).
function stableRef(id) {
  let hash = 2166136261;
  for (let i = 0; i < id.length; i += 1) {
    hash ^= id.charCodeAt(i);
    hash = Math.imul(hash, 16777619);
  }
  const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
  let out = "";
  let n = hash >>> 0;
  for (let i = 0; i < 6; i += 1) {
    out += alphabet[n % alphabet.length];
    n = Math.floor(n / alphabet.length);
  }
  return out;
}

function stage(res, task, input) {
  const location = String(input.location ?? "").trim();
  if (location === "") return refuse(res, 400, "stage location is required");
  if (location.length > 120) return refuse(res, 400, "stage location is too long (max 120)");
  const ref = String(input.ref ?? "").trim();
  if (ref.length > 40) return refuse(res, 400, "stage ref is too long (max 40)");
  const ticket = ticketFor(task);
  if (ticket.held_from_picking === true) {
    return refuse(res, 409, "This order cannot reach picking until the balance is paid.");
  }
  if (task.status !== "IN_PROGRESS") return refuse(res, 409, "illegal task transition");
  task.status = "STAGED";
  task.staged_at = new Date().toISOString();
  task.stage_location = location;
  task.stage_ref = ref !== "" ? ref : `STG-${stableRef(task.id)}`;
  task.updated_at = new Date().toISOString();
  syncTouch("pick_tasks", task.id);
  return json(res, taskOut(task));
}

function reportShortage(res, task, input) {
  const description = String(input.description ?? "").trim();
  if (description === "") return refuse(res, 400, "description is required");
  if (description.length > 400) return refuse(res, 400, "description is too long (max 400)");
  const qtyRaw = String(input.qty_short ?? "").trim();
  if (qtyRaw === "") return refuse(res, 400, "qty_short is required");
  const qty = Number(qtyRaw);
  if (!Number.isFinite(qty)) return refuse(res, 400, "qty_short must be a number");
  if (qty <= 0) return refuse(res, 400, "qty_short must be greater than zero");
  if (qty > 1e12) return refuse(res, 400, "qty_short is implausibly large");
  const lineNo = input.order_line_no === null || input.order_line_no === undefined ? null : Number(input.order_line_no);
  if (lineNo !== null && !(lineNo > 0)) return refuse(res, 400, "order_line_no must be positive");
  shortageSeq += 1;
  const shortage = {
    id: `3a000000-0000-4000-9000-${String(shortageSeq).padStart(12, "0")}`,
    task_id: task.id,
    ...(lineNo !== null ? { order_line_no: lineNo } : {}),
    sku: String(input.sku ?? ""),
    description,
    qty_short: qty.toFixed(4),
    uom: String(input.uom ?? "").trim() === "" ? "EA" : String(input.uom).trim(),
    ...(String(input.note ?? "").trim() !== "" ? { note: String(input.note).trim() } : {}),
    reported_by: YARD_USER,
    reported_at: new Date().toISOString(),
  };
  world.shortages.push(shortage);
  const t = findTask(task.id);
  t.updated_at = new Date().toISOString();
  syncTouch("shortages", shortage.id);
  return json(res, shortageOut(shortage), 201);
}

function resolveShortage(res, task, shortageId, input) {
  // The shortage resolvers list wall (PR 274): the role gate answers a
  // caller off the list with its shared Forbidden body, byte for byte.
  if (scenario === "resolve-walled") {
    return json(res, { error: "Forbidden" }, 403);
  }
  const resolution = String(input.resolution ?? "").trim();
  if (resolution === "") return refuse(res, 400, "resolution is required");
  if (resolution.length > 400) return refuse(res, 400, "resolution is too long (max 400)");
  const shortage = world.shortages.find((s) => s.id === shortageId && s.task_id === task.id);
  if (!shortage) return refuse(res, 404, "shortage not found");
  if (shortage.resolved_at !== undefined && shortage.resolved_at !== "") return refuse(res, 409, "shortage already resolved");
  shortage.resolved_at = new Date().toISOString();
  shortage.resolved_by = YARD_USER;
  shortage.resolution = resolution;
  const t = findTask(task.id);
  t.updated_at = new Date().toISOString();
  syncTouch("shortages", shortage.id);
  return json(res, shortageOut(shortage));
}

// ---- the sync surface ----------------------------------------------------
//
// The demo double of the core's sync surface (core/internal/sync): the same
// pull page shape and the same push order of decisions, per operation, in
// push order: shape, then dedup on the client's key, then the operation under
// the scope's own conflict policy. The projections read the same world the
// REST routes above serve, and the pushes mutate that world through the same
// rules, so a device's pulled rows and a desk's screen never disagree.


// The stub's world, projected for the sync pull. No money anywhere: the core's
// pull drops the ticket's held figure for every caller, the cost list included
// (the sync filter carries no role: tasks/syncscope.go attachTicketData), and
// the poison scenario never reaches these rows (the sync projection is the
// clean shape). The moneyAllowed argument is kept for the callers' shape only.
function projectTaskRow(task, _moneyAllowed) {
  return {
    id: task.id,
    seq: syncSeqOf("pick_tasks", task.id),
    data: {
      ...taskOut(task),
      ticket: blankTicketMoney(ticketFor(task), false),
      picked: world.picked.get(task.id) ?? [],
    },
  };
}

function projectShortageRow(s) {
  return { id: s.id, seq: syncSeqOf("shortages", s.id), data: shortageOut(s) };
}

function handleSyncPull(res, scope, url, moneyAllowed) {
  const since = Number(url.searchParams.get("since") ?? "0");
  const limit = Math.max(1, Math.min(200, Number(url.searchParams.get("limit") ?? "200")));
  let rows = [];
  if (scope === "pick_tasks") rows = scenario === "empty" ? [] : world.tasks.map((t) => projectTaskRow(t, moneyAllowed));
  else if (scope === "shortages") rows = world.shortages.map(projectShortageRow);
  // The core's weights feed is the NEED_WEIGHT task feed: the demo world has
  // no such task (a skid count is a direct door, and money), so it is empty.
  // receiving and inventory_lookup answer empty pages in the demo: the yard's
  // receiving story rides the desk, and the lookup is the read seam, not this
  // slice's.
  const page = rows.filter((r) => r.seq > since).sort((a, b) => a.seq - b.seq).slice(0, limit);
  const lastSeq = page.length > 0 ? page[page.length - 1].seq : since;
  return json(res, {
    scope,
    items: page,
    tombstones: [],
    last_seq: lastSeq,
    next_cursor: page.length === limit ? String(lastSeq) : null,
  });
}

function syncRejected(op, message) {
  return { client_id: op.client_id, status: "rejected", error: message };
}

function syncTaskRowData(task, moneyAllowed) {
  return projectTaskRow(task, moneyAllowed).data;
}

function syncApplyTaskUpdate(task, op, moneyAllowed) {
  const fields = op.payload ?? {};
  // The engine's order: shape, dedup, load, STALE, then the write. A stale
  // write never reaches the module's own rules: the policy answers it.
  const rowSeq = syncSeqOf("pick_tasks", task.id);
  if (op.base_seq !== undefined && rowSeq > op.base_seq) {
    return {
      client_id: op.client_id,
      status: "conflict",
      conflict: { client_id: op.client_id, policy: "server_wins", server_state: syncTaskRowData(task, moneyAllowed), applied: false },
    };
  }
  const declared = Object.keys(fields).filter((k) => k !== "id");
  // The core's writableFields (core/internal/tasks/syncscope.go): the status,
  // the pick record (`picked`), the photo, the stage legs. test/stub-sync-
  // contract.test.mjs reads the Go map and holds this set to it.
  const allowed = new Set(["status", "picked", "photo_url", "stage_location", "stage_ref"]);
  for (const key of declared) {
    if (!allowed.has(key)) return syncRejected(op, `tasks: field "${key}" is not writable through sync`);
  }
  if (fields.picked !== undefined) return syncApplyPicked(task, op);
  if (fields.status !== undefined) {
    const to = String(fields.status);
    if (!(TRANSITIONS[task.status] ?? []).includes(to)) {
      return syncRejected(op, "illegal task transition");
    }
    task.status = to;
    if (to === "ACCEPTED") {
      task.claimed_by = YARD_USER;
      task.claimed_at = new Date().toISOString();
    }
  }
  if (fields.stage_location !== undefined) {
    if (task.status !== "STAGED") return syncRejected(op, "illegal task transition");
    task.stage_location = String(fields.stage_location);
    task.stage_ref = String(fields.stage_ref ?? task.stage_ref ?? "");
  }
  if (fields.photo_url !== undefined) task.photo_url = String(fields.photo_url);
  task.updated_at = new Date().toISOString();
  syncTouch("pick_tasks", task.id);
  return { client_id: op.client_id, status: "applied", seq: syncSeqOf("pick_tasks", task.id) };
}

// A CANCELLED or DONE pick takes no record (core/internal/tasks/yard_short.go ErrPickClosed): both are
// terminal and the module has no correction path for a done pick. The REST door answers 409, a sync op
// is rejected with the same words.
function closedPickWords(task) {
  if (task.status === "CANCELLED") return "tasks: this pick was cancelled, its record cannot change";
  if (task.status === "DONE") return "tasks: this pick is done, its record cannot change";
  return "";
}

// The core's pickedLineLegs (core/internal/tasks/syncpick.go): the direct door's
// body keys, one for one. The client's local row id, task id and picked_at do
// not go on the wire.
const PICKED_LINE_LEGS = new Set(["order_line_no", "product_id", "sku", "description", "qty_ordered", "qty_picked", "uom", "short_reason", "short_note"]);
const PICKED_MAX_LINES = 500;

// The pick record door (syncpick.go writePicked): its own operation, the lines
// parsed for shape, then the service body's rules (recordPickedLines, the
// direct door's), then the stamp that re-appears the task in the pull.
function syncApplyPicked(task, op) {
  const fields = op.payload ?? {};
  for (const key of Object.keys(fields)) {
    if (key !== "id" && key !== "picked") return syncRejected(op, `tasks: field "${key}" cannot travel with picked, a pick record is its own operation`);
  }
  const closed = closedPickWords(task);
  if (closed !== "") return syncRejected(op, closed);
  const raw = fields.picked;
  if (!Array.isArray(raw)) return syncRejected(op, "tasks: picked must be an array of lines");
  if (raw.length === 0) return syncRejected(op, "tasks: say what was picked, an empty record is not the same as a pick of nothing");
  if (raw.length > PICKED_MAX_LINES) return syncRejected(op, `tasks: that is more lines than any one ticket carries (max ${PICKED_MAX_LINES})`);
  for (let i = 0; i < raw.length; i += 1) {
    const line = raw[i];
    if (line === null || typeof line !== "object" || Array.isArray(line)) return syncRejected(op, `tasks: picked line ${i + 1} must be an object`);
    for (const [key, value] of Object.entries(line)) {
      if (!PICKED_LINE_LEGS.has(key)) return syncRejected(op, `tasks: picked line ${i + 1}: field "${key}" is not writable through sync`);
      if (key === "order_line_no") {
        if (typeof value !== "number" || !Number.isInteger(value)) return syncRejected(op, `tasks: picked line ${i + 1}: order_line_no must be an integer`);
      } else if (typeof value !== "string") {
        return syncRejected(op, `tasks: picked line ${i + 1}: ${key} must be a string`);
      }
    }
  }
  const record = recordPickedLines(task, raw);
  if (record.error) return syncRejected(op, record.error);
  task.updated_at = new Date().toISOString();
  syncTouch("pick_tasks", task.id);
  return { client_id: op.client_id, status: "applied", seq: syncSeqOf("pick_tasks", task.id) };
}

// The service body's text rule (tasks.checkPickText): every C0 control, DEL and
// the bidi controls are refused in every text leg; the note and the
// description keep tab, LF and CR.
const PICK_TEXT_LEGS = [
  ["product_id", "product id", false], ["sku", "sku", false], ["description", "description", true],
  ["qty_ordered", "ordered quantity", false], ["qty_picked", "picked quantity", false], ["uom", "unit", false],
  ["short_reason", "short reason", false], ["short_note", "note", true],
];

function pickTextRefusal(line) {
  for (const [key, name, multiline] of PICK_TEXT_LEGS) {
    for (const ch of String(line[key] ?? "")) {
      const c = ch.codePointAt(0);
      if ((c < 0x20 && !(multiline && (c === 9 || c === 10 || c === 13))) || c === 0x7f || (c >= 0x202a && c <= 0x202e) || (c >= 0x2066 && c <= 0x2069)) {
        return `line ${Number(line.order_line_no ?? 0)}: the ${name} cannot hold control characters, enter it as plain text`;
      }
    }
  }
  return "";
}

// The record half of the REST route: validate the
// whole set, replace what was there, tell the rep at once.
function recordPickedLines(task, lines) {
  const ticket = ticketFor(task);
  const list = Array.isArray(lines) ? lines : [];
  if (list.length === 0) return { error: "say what was picked, an empty record is not the same as a pick of nothing" };
  const seen = new Set();
  for (const l of list) {
    const text = pickTextRefusal(l);
    if (text !== "") return { error: text };
    const lineNo = Number(l.order_line_no ?? 0);
    if (!(lineNo > 0)) return { error: "every recorded line needs the ticket's own line number" };
    if (seen.has(lineNo)) return { error: `line ${lineNo} was sent twice, one number per line` };
    seen.add(lineNo);
    const qty = parseQty(l.qty_picked);
    if (qty.empty === true) return { error: "say how many were picked, zero is an answer, blank is not" };
    if (qty.error) return { error: qty.error };
    if (qty.value < 0) return { error: "a picked quantity cannot be negative" };
    const ticketLine = ticket.lines.find((t) => t.line_no === lineNo);
    const ordered = ticketLine === undefined ? NaN : Number(ticketLine.qty_ordered);
    const reason = String(l.short_reason ?? "").trim();
    const note = String(l.short_note ?? "").trim();
    if (reason !== "" && !SHORT_REASONS.includes(reason)) {
      return { error: `line ${lineNo}: "${reason}" is not a short pick reason (${SHORT_REASONS.join(", ")})` };
    }
    if (note.length > 400) return { error: `line ${lineNo}: the note is too long (max 400)` };
    if (qty.value > ordered) {
      return { error: `line ${lineNo}: that is more than was ordered, the yard cannot raise a quantity` };
    }
    if (qty.value < ordered) {
      if (reason === "") return { error: `line ${lineNo} is short, give the reason (${SHORT_REASONS.join(", ")})` };
      if (reason === "OTHER" && note === "") return { error: `line ${lineNo}: say what happened in the note when the reason is OTHER` };
    }
  }
  const closed = closedPickWords(task);
  if (closed !== "") return { error: closed, status: 409 };
  const picked = list.map((l) => {
    const lineNo = Number(l.order_line_no);
    const ticketLine = ticket.lines.find((t) => t.line_no === lineNo) ?? {};
    const qtyPicked = Number(String(l.qty_picked).trim()).toFixed(4);
    const ordered = Number(ticketLine.qty_ordered ?? 0);
    const short = Number(qtyPicked) < ordered;
    return {
      id: `2a000000-0000-4000-9000-${String(pickedSeq()).padStart(12, "0")}`,
      task_id: task.id,
      order_line_no: lineNo,
      product_id: String(ticketLine.product_id ?? ""),
      sku: String(ticketLine.sku ?? ""),
      description: String(ticketLine.description ?? ""),
      qty_ordered: ticketLine.qty_ordered ?? "0.0000",
      qty_picked: qtyPicked,
      uom: String(ticketLine.uom ?? "EA"),
      picked_by: YARD_USER,
      picked_at: new Date().toISOString(),
      ...(short ? { short_reason: String(l.short_reason ?? "").trim() } : {}),
      ...(short && String(l.short_note ?? "").trim() !== "" ? { short_note: String(l.short_note ?? "").trim() } : {}),
    };
  });
  world.picked.set(task.id, picked);
  for (const p of picked) {
    const ordered = Number(p.qty_ordered);
    const pickedQty = Number(p.qty_picked);
    if (pickedQty >= ordered) continue;
    const back = (Number((ordered - pickedQty).toFixed(4))).toFixed(4);
    repNotices.push({
      topic: "yard.short_pick",
      order_no: ticket.order_no,
      line_no: p.order_line_no,
      body: shortPickNotice(ticket.order_no, p, p.qty_ordered, p.qty_picked, back, p.short_reason ?? "", p.short_note ?? ""),
    });
  }
  return { picked };
}

// The core's shortageWritableCreate (core/internal/tasks/syncshortage.go): the
// report itself. The resolution legs and the stamps are the server's.
const SHORTAGE_CREATE_FIELDS = new Set(["id", "task_id", "order_line_no", "product_id", "sku", "description", "qty_short", "uom", "note"]);
// The core's shortageWritable: the report's correctable legs. The resolution
// legs are the counter's act and refuse here.
const SHORTAGE_UPDATE_FIELDS = new Set(["id", "qty_short", "uom", "sku", "description", "note"]);
const WEIGHT_UPDATE_FIELDS = new Set(["id", "status", "weight_entered", "weight_uom", "photo_url"]);

function syncFieldRefusal(op, allowed) {
  for (const key of Object.keys(op.payload ?? {})) {
    if (!allowed.has(key)) return syncRejected(op, `tasks: field "${key}" is not writable through sync`);
  }
  return null;
}

function syncApplyShortageCreate(op) {
  const refused = syncFieldRefusal(op, SHORTAGE_CREATE_FIELDS);
  if (refused) return refused;
  const input = op.payload ?? {};
  const description = String(input.description ?? "").trim();
  if (description === "") return syncRejected(op, "description is required");
  const qtyRaw = String(input.qty_short ?? "").trim();
  if (qtyRaw === "") return syncRejected(op, "qty_short is required");
  const qty = Number(qtyRaw);
  if (!Number.isFinite(qty) || qty <= 0) return syncRejected(op, "qty_short must be greater than zero");
  const id = typeof input.id === "string" && UUID_RE.test(input.id) ? input.id : `3a000000-0000-4000-9000-${String(++shortageSeq).padStart(12, "0")}`;
  if (world.shortages.some((s) => s.id === id)) return syncRejected(op, "row exists");
  const lineNo = input.order_line_no === null || input.order_line_no === undefined ? null : Number(input.order_line_no);
  const shortage = {
    id,
    task_id: String(input.task_id ?? ""),
    ...(lineNo !== null && lineNo > 0 ? { order_line_no: lineNo } : {}),
    sku: String(input.sku ?? ""),
    description,
    qty_short: qty.toFixed(4),
    uom: String(input.uom ?? "").trim() === "" ? "EA" : String(input.uom).trim(),
    ...(String(input.note ?? "").trim() !== "" ? { note: String(input.note).trim() } : {}),
    reported_by: YARD_USER,
    reported_at: new Date().toISOString(),
  };
  world.shortages.push(shortage);
  const t = findTask(shortage.task_id);
  if (t) {
    // The core's create bumps the parent task's pick seq (the task projection
    // carries the shortage counts), so the task row re-appears in the pull.
    t.updated_at = new Date().toISOString();
    syncTouch("pick_tasks", t.id);
  }
  syncTouch("shortages", id);
  return { client_id: op.client_id, status: "applied", seq: syncSeqOf("shortages", id) };
}

function syncApplyShortageUpdate(op) {
  const refused = syncFieldRefusal(op, SHORTAGE_UPDATE_FIELDS);
  if (refused) return refused;
  const id = String(op.payload?.id ?? "");
  const shortage = world.shortages.find((s) => s.id === id);
  if (!shortage) return syncRejected(op, "shortage not found");
  const rowSeq = syncSeqOf("shortages", id);
  if (op.base_seq !== undefined && rowSeq > op.base_seq) {
    // The manual policy: the server row is what stands, and a person reviews.
    return {
      client_id: op.client_id,
      status: "conflict",
      conflict: { client_id: op.client_id, policy: "manual", server_state: shortageOut(shortage), applied: false },
    };
  }
  for (const key of ["qty_short", "uom", "sku", "description", "note"]) {
    if (op.payload[key] !== undefined) shortage[key] = String(op.payload[key]);
  }
  syncTouch("shortages", id);
  return { client_id: op.client_id, status: "applied", seq: syncSeqOf("shortages", id) };
}

// The core's weights feed is the NEED_WEIGHT task feed and the demo world has
// no such task: the fields are judged first, then the row is not found.
function syncApplyWeightsUpdate(op) {
  const refused = syncFieldRefusal(op, WEIGHT_UPDATE_FIELDS);
  if (refused) return refused;
  return syncRejected(op, "row not found");
}

function handleSyncPush(res, scope, body, moneyAllowed) {
  const ops = Array.isArray(body?.ops) ? body.ops : [];
  const results = [];
  for (const op of ops) {
    if (!UUID_RE.test(String(op.client_id ?? ""))) {
      results.push(syncRejected(op, "client_id must be a uuid"));
      continue;
    }
    if (!["create", "update", "delete"].includes(String(op.op ?? ""))) {
      results.push(syncRejected(op, "op must be create, update or delete"));
      continue;
    }
    const payload = op.payload;
    if (payload === null || typeof payload !== "object" || Array.isArray(payload)) {
      results.push(syncRejected(op, "payload is required"));
      continue;
    }
    const done = appliedKeys.get(op.client_id);
    if (done !== undefined) {
      results.push({ client_id: op.client_id, status: "applied", seq: done });
      continue;
    }
    let result;
    if (scope === "pick_tasks") {
      if (op.op === "create") {
        result = syncRejected(op, "tasks: creates through sync are not wired in this demo");
      } else {
        const task = findTask(String(payload.id ?? ""));
        if (!task) result = syncRejected(op, "row not found");
        else result = syncApplyTaskUpdate(task, op, moneyAllowed);
      }
    } else if (scope === "shortages") {
      result = op.op === "create" ? syncApplyShortageCreate(op) : syncApplyShortageUpdate(op);
    } else if (scope === "weights") {
      result = op.op === "update" ? syncApplyWeightsUpdate(op) : syncRejected(op, "row not found");
    } else {
      result = syncRejected(op, `sync: scope ${scope} is not writable in this demo`);
    }
    if (result.status === "applied") appliedKeys.set(op.client_id, result.seq);
    results.push(result);
  }
  return json(res, { results });
}

async function handleSync(req, res, url, body) {
  const scope = url.pathname.slice("/api/v1/sync/".length);
  if (!SYNC_SCOPES.includes(scope)) return refuse(res, 400, "unknown sync scope");
  const moneyAllowed = COST_LIST.has(callerRole(req));
  if (req.method === "GET") return handleSyncPull(res, scope, url, moneyAllowed);
  // The role gate rides the sync surface as it rides every surface: the
  // shortage resolvers wall refuses the caller the same way, byte for byte.
  if (scenario === "resolve-walled" && scope === "shortages") return json(res, { error: "Forbidden" }, 403);
  if (PULL_ONLY.has(scope)) return refuse(res, 400, `sync: scope ${scope} is pull-only`);
  if (!WRITE_SCOPES.has(scope)) return refuse(res, 501, `sync: scope ${scope} is not wired yet`);
  // The body is already read and parsed by the shared block above; a second
  // read of an ended stream would never answer.
  return handleSyncPush(res, scope, body, moneyAllowed);
}

const server = createServer((req, res) => {
  handle(req, res).catch(() => refuse(res, 500, "internal error"));
});
server.listen(port, () => {
  console.log(`yard-stub listening on ${port}`);
});
