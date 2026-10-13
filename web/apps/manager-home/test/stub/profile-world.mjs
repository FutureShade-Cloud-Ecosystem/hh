// The stub's per role landings: one world for each of the eight manager
// profiles, seeded from what the core files. The base shapes are 0201's
// (admin, manager, yard), 0381 copies them (branch, payables, controller from
// manager; logistics from yard; general from admin) and the 0392 landings
// replace procurement and sales widgets, procurement and branch needs. The yard
// manager's own widgets and needs live in manager-stub.mjs beside the shortage
// and receiving world they count.
//
// Test double only. The widget titles are the words the dashboard catalog files
// (0363 spelled out), the numbers are invented demo figures.

const SPAN = { L: [6, 2], M: [6, 1], T: [3, 1], S: [3, 1], XL: [12, 1] };

// measure -> [module, kind, unit, screen]
const MEASURE = {
  "needs.you": ["dashboard", "count", "", ""],
  "sales.today": ["pos", "money_cents", "", ""],
  "orders.open": ["order", "count", "", ""],
  "ar.aging": ["billing", "money_cents", "", ""],
  "warranty.open_claims": ["warranty", "count", "", ""],
  "deliveries.today": ["dispatch", "count", "dispatch", "dispatch"],
  "picks.queue": ["tasks", "count", "yard", "yard"],
  "inventory.value": ["inventory", "money_cents", "", ""],
  "margin.gross_bps": ["pos", "basis_points", "", ""],
};

function widget(profile, position, [key, shape, measure, split, range, size, title]) {
  const [module, kind, unit, screen] = MEASURE[measure];
  const [span_cols, span_rows] = SPAN[size];
  return {
    widget_key: key, shape, measure, split, range, size, title, position, visible: true, label: title, kind, module,
    ...(unit === "" ? {} : { unit, screen }), span_cols, span_rows,
  };
}

const ADMIN_BASE = [
  ["hero.sales", "stat", "sales.today", "none", "today", "L", "Sales today"],
  ["needs.you", "list", "needs.you", "none", "open", "L", "Needs you"],
  ["ar.aging", "bars", "ar.aging", "bucket", "open", "L", "Receivables aging"],
  ["orders.trend", "trend", "orders.open", "none", "last_14", "M", "Open orders"],
  ["margin.branch", "bars", "margin.gross_bps", "branch", "mtd", "M", "Margin by branch"],
  ["deliveries.board", "board", "deliveries.today", "status", "today", "M", "Deliveries today"],
  ["picks.queue", "stat", "picks.queue", "none", "today", "S", "Pick queue"],
  ["warranty.open", "stat", "warranty.open_claims", "none", "open", "S", "Open claims"],
  ["inventory.value", "stat", "inventory.value", "none", "open", "S", "Inventory value"],
];

const MANAGER_BASE = [
  ["hero.ar", "stat", "ar.aging", "none", "open", "L", "Receivables"],
  ["needs.you", "list", "needs.you", "none", "open", "L", "Needs you"],
  ["ar.aging", "bars", "ar.aging", "bucket", "open", "L", "Aging by bucket"],
  ["sales.trend", "trend", "sales.today", "none", "last_30", "M", "Sales trend"],
  ["margin.branch", "bars", "margin.gross_bps", "branch", "mtd", "M", "Margin by branch"],
  ["orders.open", "stat", "orders.open", "none", "open", "S", "Open orders"],
  ["inventory.value", "stat", "inventory.value", "none", "open", "S", "Inventory value"],
  ["warranty.open", "stat", "warranty.open_claims", "none", "open", "S", "Open claims"],
];

const YARD_BASE = [
  ["hero.deliveries", "stat", "deliveries.today", "none", "today", "L", "Deliveries today"],
  ["needs.you", "list", "needs.you", "none", "open", "L", "Needs you"],
  ["picks.board", "board", "picks.queue", "status", "today", "L", "Pick queue"],
  ["deliveries.branch", "bars", "deliveries.today", "branch", "today", "M", "By branch"],
  ["picks.gauge", "gauge", "picks.queue", "none", "today", "S", "Queue depth"],
  ["orders.open", "stat", "orders.open", "none", "open", "S", "Open orders"],
  ["warranty.open", "stat", "warranty.open_claims", "none", "open", "S", "Open claims"],
];

// 0392 section 3 and 4.
const SALES_0392 = [
  ["hero.sales", "stat", "sales.today", "none", "today", "L", "Sales today"],
  ["needs.you", "list", "needs.you", "none", "open", "L", "Needs you"],
  ["sales.branch", "bars", "sales.today", "branch", "today", "L", "Sales by branch"],
  ["margin.branch", "bars", "margin.gross_bps", "branch", "all_time", "M", "Margin by branch"],
  ["sales.trend", "trend", "sales.today", "none", "today", "M", "Sales trend"],
  ["orders.status", "bars", "orders.open", "status", "open", "M", "Orders by status"],
  ["orders.open", "stat", "orders.open", "none", "open", "S", "Open orders"],
  ["margin.gross", "stat", "margin.gross_bps", "none", "all_time", "S", "Gross margin"],
];

const PROCUREMENT_0392 = [
  ["hero.inventory", "stat", "inventory.value", "none", "now", "L", "Inventory value"],
  ["needs.you", "list", "needs.you", "none", "open", "L", "Needs you"],
  ["inventory.branch", "bars", "inventory.value", "branch", "now", "L", "Inventory by branch"],
  ["orders.status", "bars", "orders.open", "status", "open", "M", "Orders by status"],
  ["picks.board", "board", "picks.queue", "status", "open", "M", "Pick queue"],
  ["picks.gauge", "gauge", "picks.queue", "none", "open", "S", "Queue depth"],
  ["orders.open", "stat", "orders.open", "none", "open", "S", "Open orders"],
  ["warranty.open", "stat", "warranty.open_claims", "none", "open", "S", "Open claims"],
];

/** The widget lists of the seven profiles the yard manager's file does not hold. */
export const SHAPES = {
  "manager.branch": MANAGER_BASE,
  "manager.sales": SALES_0392,
  "manager.logistics": YARD_BASE,
  "manager.procurement": PROCUREMENT_0392,
  "manager.payables": MANAGER_BASE,
  "manager.controller": MANAGER_BASE,
  "manager.general": ADMIN_BASE,
};

export function widgetLayouts() {
  const out = {};
  for (const [profile, rows] of Object.entries(SHAPES)) {
    out[profile] = { profile, source: "role", widgets: rows.map((row, index) => widget(profile, index, row)) };
  }
  return out;
}

// ---- the numbers behind the widgets --------------------------------------------------------------

const BRANCHES = [{ label: "Trenton", number: 62 }, { label: "Kingston", number: 38 }];
const STATUS = [{ label: "Waiting", number: 8 }, { label: "Claimed", number: 5 }, { label: "Picked", number: 3, tone: "good" }, { label: "Staged", number: 4 }];
const BUCKETS = [
  { label: "Current", number: 410000 }, { label: "1 to 30", number: 220000 },
  { label: "31 to 60", number: 90000, tone: "watch" }, { label: "Over 60", number: 40000, tone: "stop" },
];

function sum(points) {
  return points.reduce((total, p) => total + p.number, 0);
}

function datumFor(w) {
  const base = { widget_key: w.widget_key, state: "ok", range: w.range };
  if (w.measure === "needs.you") return { ...base, total: 0 };
  const pointsBySplit = {
    branch: w.kind === "money_cents" ? BRANCHES.map((p) => ({ ...p, number: p.number * 1000 })) : w.kind === "basis_points" ? [{ label: "Trenton", number: 3120 }, { label: "Kingston", number: 2850 }] : BRANCHES,
    status: STATUS,
    bucket: BUCKETS,
  };
  if (w.shape === "bars" || w.shape === "board") {
    const points = pointsBySplit[w.split] ?? BRANCHES;
    return { ...base, total: w.kind === "basis_points" ? 2990 : sum(points), points };
  }
  if (w.shape === "trend") {
    // The core measures a trend over its series dimension; the stub sends the daily points the card draws.
    const days = [34, 38, 31, 42, 45, 40, 47].map((n, i) => ({ label: `Day ${i + 1}`, number: w.kind === "money_cents" ? n * 3000 : n }));
    return { ...base, total: days[days.length - 1].number, points: days };
  }
  if (w.shape === "gauge") return { ...base, total: sum(STATUS), points: STATUS };
  const totals = { money_cents: 123456, basis_points: 2990, count: 12, days: 4 };
  return { ...base, total: w.measure === "ar.aging" ? 760000 : totals[w.kind] };
}

export function dataLayouts(layouts) {
  const out = {};
  for (const [profile, layout] of Object.entries(layouts)) {
    out[profile] = {};
    for (const w of layout.widgets) out[profile][w.widget_key] = datumFor(w);
  }
  return out;
}

// ---- the needs-you sources ----------------------------------------------------------------------------

export const NEEDS_META = {
  "approvals.waiting": { what: "Approvals waiting", module: "identity", unit: "admin", screen: "approvals", view: "Waiting on you", action: "Review", count: 0, minutes: 45 },
  "ar.escalation": { what: "Accounts past the escalation edge", module: "billing", unit: "finance", screen: "ar", view: "Escalation queue", action: "Escalate", count: 3, minutes: 600 },
  "dispatch.unassigned": { what: "Loads with no truck", module: "dispatch", unit: "dispatch", screen: "dispatch", view: "Staging · unassigned", action: "Assign", count: 1, minutes: 90 },
  "dispatch.due_to_commit": { what: "Loads due to commit", module: "dispatch", unit: "dispatch", screen: "dispatch", view: "Load board · due to commit", action: "Commit", count: 2, minutes: 150 },
  "pos.parked": { what: "Parked tickets", module: "pos", unit: "sales", screen: "pos", view: "Parked tickets", action: "Resume", count: 2, minutes: 130 },
  "tasks.stale_picks": { what: "Picks past their deadline", module: "tasks", unit: "yard", screen: "yard", view: "Picks · past deadline", action: "Chase", count: 1, minutes: 1560 },
  "tasks.open_shortages": { what: "Open shortages", module: "tasks", unit: "yard", screen: "yard", view: "Shortages · open", action: "Resolve", count: 2, minutes: 120 },
  "purchasing.receiving_differences": { what: "Receipts that landed short", module: "purchasing", unit: "yard", screen: "yard", view: "Receiving · short", action: "Settle", count: 1, minutes: 180 },
};

/** Which sources each profile files, in order (0201 and 0381 copies; the 0392 landings for procurement and branch). */
export const NEEDS_SOURCES = {
  "manager.branch": ["dispatch.due_to_commit", "dispatch.unassigned", "approvals.waiting", "ar.escalation", "pos.parked"],
  "manager.sales": ["approvals.waiting", "ar.escalation", "pos.parked"],
  "manager.logistics": ["dispatch.unassigned", "dispatch.due_to_commit", "tasks.stale_picks"],
  "manager.procurement": ["tasks.open_shortages", "purchasing.receiving_differences", "approvals.waiting"],
  "manager.payables": ["approvals.waiting", "ar.escalation", "pos.parked"],
  "manager.controller": ["approvals.waiting", "ar.escalation", "pos.parked"],
  "manager.general": ["approvals.waiting", "ar.escalation", "dispatch.unassigned", "pos.parked"],
};

/** One profile's queue. `waiting` is the count of approvals this caller decides, recomputed per read. */
export function needsQueue(profile, waiting) {
  const rows = (NEEDS_SOURCES[profile] ?? [])
    .map((key, position) => {
      const meta = NEEDS_META[key];
      const count = key === "approvals.waiting" ? waiting : meta.count;
      const age = meta.minutes >= 1440 ? `${Math.round(meta.minutes / 60)}h` : meta.minutes >= 60 ? `${Math.round(meta.minutes / 60)}h` : `${meta.minutes}m`;
      const severity = count >= 5 ? "stop" : meta.minutes >= 1440 ? "stop" : "watch";
      return { source_key: key, what: meta.what, count, age, age_minutes: meta.minutes, severity, module: meta.module, unit: meta.unit, screen: meta.screen, view: meta.view, action: meta.action, position };
    })
    .filter((row) => row.count > 0)
    .map(({ position: _position, ...row }) => row);
  const total = rows.reduce((n, r) => n + r.count, 0);
  const worst = rows.some((r) => r.severity === "stop") ? "stop" : rows.length > 0 ? "watch" : "good";
  return { profile, rows, total, worst };
}
