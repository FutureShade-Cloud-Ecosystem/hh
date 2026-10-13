// The dispatch board's local stub: the demo way in and the dispatch routes the board calls, over
// one in-memory yard. A development and test double only; the real routes are the core's
// (core/internal/dispatch), and test/stub-contract.test.mjs holds every answer here to the wire
// descriptors and every refusal sentence to the core's source.
//
// The world is Gable Landscape Supply's Point Loma yard, dated from the day the stub starts: four
// trucks, three drivers, a handful of delivery stops across the working window, one truck that
// needs a crew, one over capacity, one already out on the road. Customers and addresses are
// invented; people are role labels. The stub keeps the core's rules where the board leans on
// them: a load is committed by being put on a truck, a committed load is changed only by a caller
// who may reopen it, a run is dispatched only with a truck and a driver, a stop is en route only
// from a dispatched run, and a failed stop needs a reason.
//
// The stub holds no money: the payload carries no value or freight, the way the core omits them
// for a caller the dealer's policy refuses. A payment hold carries its words and no amount unless
// a test arms one (/_stub/hold-amount), which is the core letting a caller on the cost list see it.
import { createServer } from "node:http";

const port = Number(process.env.DISPATCH_STUB_PORT ?? 4979);

const FLOWS = [
  { flow: "yard-manager", role_code: "yard_manager", label: "Yard manager" },
  { flow: "logistics-manager", role_code: "logistics_manager", label: "Logistics Manager" },
];

const USERS = {
  "logistics-manager": {
    user_id: "d0200000-0000-4000-9000-000000000001",
    email: "dispatch.demo@hh-demo.invalid",
    display_name: "Dispatch persona (demo)",
    role_code: "logistics_manager",
    role_name: "Logistics Manager",
    tier: 2,
    scope: "BRANCH",
  },
  "yard-manager": {
    user_id: "de110000-0000-4000-9000-000000000003",
    email: "yard.manager@hh-demo.invalid",
    display_name: "Yard manager (demo)",
    role_code: "yard_manager",
    role_name: "Yard Manager",
    tier: 2,
    scope: "BRANCH",
  },
};

// A token with the shape the app reads an expiry from: three parts, the middle one base64url JSON
// carrying sub and exp. The signature is a stub.
function mintToken(sub) {
  const part = (value) => Buffer.from(JSON.stringify(value), "utf8").toString("base64url");
  const issuedAtSeconds = Math.floor(Date.now() / 1000);
  const payload = { iss: "hh-demo-stub", sub, aud: "staff", iat: issuedAtSeconds, exp: issuedAtSeconds + 3600 };
  return `${part({ alg: "ES256", typ: "JWT" })}.${part(payload)}.c3R1Yi1zaWduYXR1cmUtYnl0ZXM`;
}

// ---- dates ---------------------------------------------------------------------------------------

function iso(d) {
  const pad = (n) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}
function addDays(base, n) {
  const [y, m, d] = base.split("-").map(Number);
  const date = new Date(y, m - 1, d);
  date.setDate(date.getDate() + n);
  return iso(date);
}
const TODAY = iso(new Date());

// ---- the world -----------------------------------------------------------------------------------

const BRANCH = "5d1b3f6e-0000-4000-9000-000000000001";
const BRANCHES = [
  {
    id: BRANCH,
    code: "PLM",
    name: "Point Loma",
    address: { line1: "2210 Harbor Drive", city: "San Diego", province: "CA", postal: "92106", country: "US" },
    is_default: true,
    is_system_default: true,
    is_active: true,
  },
];

// A second yard the caller may reach, offered only when a test arms it (/_stub/branches).
const OTHER_BRANCH = "5d1b3f6e-0000-4000-9000-000000000002";
const JULIAN = {
  id: OTHER_BRANCH,
  code: "JUL",
  name: "Julian",
  address: { line1: "1500 Farmers Road", city: "Julian", province: "CA", postal: "92036", country: "US" },
  is_default: false,
  is_system_default: false,
  is_active: true,
};
let manyBranches = false;

const DEFAULT_POLICY = {
  branch_id: BRANCH,
  commit_prompt_hours: 48,
  reopen_policy: "MANAGER",
  load_value_visibility: "MANAGER_UP",
  commit_horizon_weeks: 4,
  working_view_days: 14,
  freight_split_mode: "EVEN",
  driver_session_hours: 12,
};
let POLICY = { ...DEFAULT_POLICY };
// The policy trail the settings screen reads: one row per knob that moved, newest first.
let policyTrail = [];

function seedWorld() {
  const trucks = [
    { id: "t0000001-0000-4000-9000-000000000001", branch_id: BRANCH, code: "Truck 01", name: "Truck 01 · flatbed 5 ton", truck_type: "FLATBED", capacity_kg: 9000, capacity_m3: null, pallet_positions: null, is_active: true },
    { id: "t0000001-0000-4000-9000-000000000002", branch_id: BRANCH, code: "Truck 02", name: "Truck 02 · boom truck", truck_type: "BOOM", capacity_kg: 6500, capacity_m3: null, pallet_positions: null, is_active: true },
    { id: "t0000001-0000-4000-9000-000000000003", branch_id: BRANCH, code: "Truck 03", name: "Truck 03 · tandem dump", truck_type: "DUMP", capacity_kg: 13000, capacity_m3: null, pallet_positions: null, is_active: true },
    { id: "t0000001-0000-4000-9000-000000000004", branch_id: BRANCH, code: "Truck 04", name: "Truck 04 · pickup and trailer", truck_type: "PICKUP", capacity_kg: 1100, capacity_m3: null, pallet_positions: null, is_active: true },
  ];
  const drivers = [
    { id: "d0000001-0000-4000-9000-000000000001", branch_id: BRANCH, name: "Driver A (demo)", phone: "619-555-0141", licence_class: "A", is_active: true },
    { id: "d0000001-0000-4000-9000-000000000002", branch_id: BRANCH, name: "Driver B (demo)", phone: "619-555-0142", licence_class: "B", is_active: true },
    { id: "d0000001-0000-4000-9000-000000000003", branch_id: BRANCH, name: "Driver C (demo)", phone: "619-555-0143", licence_class: "C", is_active: true },
  ];
  const run = (n, over) => ({
    id: `r0000001-0000-4000-9000-00000000000${n}`,
    run_no: `RUN-PLM-${n}`,
    branch_id: BRANCH,
    scheduled_date: TODAY,
    truck_id: "",
    driver_id: "",
    status: "PLANNED",
    notes: "",
    dispatched_at: null,
    completed_at: null,
    created_at: `${TODAY}T06:00:00Z`,
    commit_state: "AUTO",
    ...over,
  });
  const stop = (n, over) => ({
    id: `5a000001-0000-4000-9000-0000000000${String(n).padStart(2, "0")}`,
    branch_id: BRANCH,
    order_id: `0a000001-0000-4000-9000-0000000000${String(n).padStart(2, "0")}`,
    run_id: "",
    stop_no: 0,
    scheduled_date: TODAY,
    status: "READY",
    time_window: "",
    contact_name: "",
    contact_phone: "",
    address_line: "",
    city: "",
    postal_code: "",
    instructions: "",
    photo_count: 0,
    created_at: `${TODAY}T05:00:00Z`,
    weight_kg: null,
    volume_m3: null,
    pallet_positions: null,
    requested_date: "",
    commit_state: "REQUESTED",
    committed_at: null,
    ...over,
  });
  const runs = [
    run(1, { truck_id: trucks[0].id, driver_id: drivers[0].id }),
    run(2, { truck_id: trucks[2].id }),
    run(3, { truck_id: trucks[3].id, driver_id: drivers[2].id }),
    run(4, { truck_id: trucks[1].id, driver_id: drivers[1].id, status: "DISPATCHED", dispatched_at: `${TODAY}T07:00:00Z` }),
  ];
  const stops = [
    // Open work with no truck: the load board's staging lane.
    stop(1, { order_no: "ORD-0029", customer_name: "Summit Ridge Builders", address_line: "7420 Prospect Lane", city: "La Jolla", postal_code: "92037", time_window: "AM", weight_kg: 3400, requested_date: TODAY }),
    stop(2, { order_no: "ORD-0030", customer_name: "Ridgeview Outdoor Living", address_line: "88 Wynola Ridge Road", city: "Julian", postal_code: "92036", scheduled_date: addDays(TODAY, 1), weight_kg: 5200, requested_date: addDays(TODAY, 1) }),
    stop(3, { order_no: "ORD-0031", customer_name: "Homeowner A", address_line: "19 Apple Orchard Lane", city: "Julian", postal_code: "92036", scheduled_date: addDays(TODAY, 3), requested_date: addDays(TODAY, 3) }),
    // Truck 01: two committed loads, light.
    stop(4, { order_no: "ORD-0012", customer_name: "Fieldstone Pools and Patios", run_id: runs[0].id, stop_no: 1, address_line: "1500 Farmers Road", city: "Julian", postal_code: "92036", weight_kg: 4100, commit_state: "COMMITTED", committed_at: `${TODAY}T06:30:00Z`, requested_date: TODAY, time_window: "AM" }),
    stop(5, { order_no: "ORD-0014", customer_name: "Summit Ridge Builders", run_id: runs[0].id, stop_no: 2, address_line: "233 Cuyamaca Heights Drive", city: "Julian", postal_code: "92036", weight_kg: 980, commit_state: "COMMITTED", committed_at: `${TODAY}T06:31:00Z`, requested_date: TODAY, time_window: "PM" }),
    // Truck 03: no driver yet, near full, still asks.
    stop(6, { order_no: "ORD-0021", customer_name: "Ridgeview Outdoor Living", run_id: runs[1].id, stop_no: 1, address_line: "640 Pine Hills Road", city: "Julian", postal_code: "92036", weight_kg: 12500, requested_date: TODAY }),
    // Truck 04: over capacity, on purpose.
    stop(7, { order_no: "ORD-0017", customer_name: "Homeowner A", run_id: runs[2].id, stop_no: 1, address_line: "77 Kentwood Lane", city: "Julian", postal_code: "92036", weight_kg: 900, requested_date: TODAY }),
    stop(8, { order_no: "ORD-0018", customer_name: "Fieldstone Pools and Patios", run_id: runs[2].id, stop_no: 2, address_line: "418 Oak Hollow Road", city: "Julian", postal_code: "92036", weight_kg: 620, requested_date: TODAY }),
    // Truck 02: out on the road.
    stop(9, { order_no: "ORD-0009", customer_name: "Summit Ridge Builders", run_id: runs[3].id, stop_no: 1, address_line: "4410 Torrey Pines Road", city: "La Jolla", postal_code: "92037", weight_kg: 2900, status: "EN_ROUTE", commit_state: "COMMITTED", committed_at: `${TODAY}T05:50:00Z`, requested_date: TODAY }),
    stop(10, { order_no: "ORD-0010", customer_name: "Ridgeview Outdoor Living", run_id: runs[3].id, stop_no: 2, address_line: "8 Santa Fe Court", city: "San Diego", postal_code: "92106", weight_kg: null, status: "LOADED", commit_state: "COMMITTED", committed_at: `${TODAY}T05:51:00Z`, requested_date: TODAY }),
  ];
  // Orders the counter can hand to dispatch, by the number on the paperwork. ORD-0040 and ORD-0043 are
  // confirmed and not yet scheduled; the rest of the stops above hold their own orders.
  const orders = [
    { id: "0a000001-0000-4000-9000-000000000040", order_no: "ORD-0040", customer_name: "Cedar Court Homeowner", status: "CONFIRMED", fulfilment: "DELIVERY" },
    { id: "0a000001-0000-4000-9000-000000000041", order_no: "ORD-0041", customer_name: "Harbor View Landscaping", status: "DRAFT", fulfilment: "WILL_CALL" },
    { id: "0a000001-0000-4000-9000-000000000042", order_no: "ORD-0042", customer_name: "Mesa Stoneworks", status: "CANCELLED", fulfilment: "WILL_CALL" },
    { id: "0a000001-0000-4000-9000-000000000043", order_no: "ORD-0043", customer_name: "Coastline Patios", status: "CONFIRMED", fulfilment: "DELIVERY" },
    // Will call: staged and ready for a tag, never picked, held for the balance, and one with its history.
    { id: "0a000001-0000-4000-9000-000000000044", order_no: "ORD-0044", customer_name: "Bayside Pavers", status: "CONFIRMED", fulfilment: "WILL_CALL", pick: "STAGED" },
    { id: "0a000001-0000-4000-9000-000000000045", order_no: "ORD-0045", customer_name: "Pine Hill Supply", status: "CONFIRMED", fulfilment: "WILL_CALL", pick: "NONE" },
    { id: "0a000001-0000-4000-9000-000000000046", order_no: "ORD-0046", customer_name: "Eastlake Masonry", status: "CONFIRMED", fulfilment: "WILL_CALL", pick: "STAGED", held: true },
    { id: "0a000001-0000-4000-9000-000000000047", order_no: "ORD-0047", customer_name: "Terrace Landscape Co", status: "CONFIRMED", fulfilment: "WILL_CALL", pick: "STAGED" },
  ];
  const pickups = [
    { id: "9c000001-0000-4000-9000-000000000001", order_id: orders[7].id, branch_id: BRANCH, status: "CONFIRMED", expires_at: `${addDays(TODAY, 9)}T18:00:00Z`, collected_by: "Pickup contact (demo)", confirmed_at: `${TODAY}T10:00:00Z`, created_at: `${addDays(TODAY, -2)}T09:00:00Z` },
    { id: "9c000001-0000-4000-9000-000000000002", order_id: orders[7].id, branch_id: BRANCH, status: "REVOKED", expires_at: `${addDays(TODAY, 8)}T18:00:00Z`, created_at: `${addDays(TODAY, -3)}T09:00:00Z` },
  ];
  // Freight per order, in cents: what it charges now and what the customer was quoted (null: never quoted).
  const freight = {
    [stops[3].order_id]: { current: 30000, quoted: 30000 },
    [stops[4].order_id]: { current: 10000, quoted: 10000 },
    [stops[5].order_id]: { current: 20000, quoted: null },
  };
  return { trucks, drivers, runs, stops, orders, pickups, freight, raises: [], shares: {}, seq: { run: 5, resched: 1, stop: 50, truck: 5, driver: 4, pickup: 10, job: 10, raise: 1, split: 60 } };
}

let world = seedWorld();
// What a test arms: the next write's refusal, the caller's reopen right, and a hold's amount.
let armed = null;
let caller = { can_reopen: true, can_commit: true, can_manage: true, can_set_policy: true, can_see_value: true };
let holdAmount = false;
const HOLD_STOP = "5a000001-0000-4000-9000-000000000005";

// ---- answers -------------------------------------------------------------------------------------

function json(res, data, status = 200) {
  res.writeHead(status, { "Content-Type": "application/json", "Cache-Control": "no-store" });
  res.end(JSON.stringify(data));
}
function fail(res, status, error) {
  json(res, { error }, status);
}

const truckOf = (id) => world.trucks.find((t) => t.id === id);
const driverOf = (id) => world.drivers.find((d) => d.id === id);
const runOf = (id) => world.runs.find((r) => r.id === id);
const stopOf = (id) => world.stops.find((s) => s.id === id);

function band(used, capacity) {
  if (typeof capacity !== "number" || capacity <= 0) return "UNKNOWN";
  if (typeof used !== "number" || used < 0) return "UNKNOWN";
  const ratio = used / capacity;
  if (ratio > 1) return "RED";
  if (ratio >= 0.85) return "AMBER";
  return "GREEN";
}
const RANK = { UNKNOWN: 0, GREEN: 1, AMBER: 2, RED: 3 };
function worst(...bands) {
  let w = "UNKNOWN";
  for (const b of bands) if (RANK[b] > RANK[w]) w = b;
  return w;
}

function stopsOnRun(runId) {
  return world.stops.filter((s) => s.run_id === runId).sort((a, b) => a.stop_no - b.stop_no);
}

function stopView(s) {
  const run = s.run_id ? runOf(s.run_id) : null;
  const out = { ...s };
  for (const k of Object.keys(out)) if (out[k] === "" && !["address_line", "city", "postal_code"].includes(k)) delete out[k];
  if (run) out.run_no = run.run_no;
  if (out.weight_kg === null) delete out.weight_kg;
  if (out.volume_m3 === null) delete out.volume_m3;
  if (out.pallet_positions === null) delete out.pallet_positions;
  if (out.committed_at === null) delete out.committed_at;
  if (!out.address_line) delete out.address_line;
  if (!out.city) delete out.city;
  if (!out.postal_code) delete out.postal_code;
  return out;
}

function commitOf(run) {
  const stops = stopsOnRun(run.id).filter((s) => s.status !== "CANCELLED");
  const committed = stops.filter((s) => s.commit_state === "COMMITTED").length;
  const requested = stops.filter((s) => s.commit_state === "REQUESTED").length;
  const out = {
    committed: committed > 0,
    reopenable: POLICY.reopen_policy !== "NOBODY",
    can_reopen: caller.can_reopen,
    can_commit: caller.can_commit,
    due_to_commit: requested > 0 && run.scheduled_date <= addDays(TODAY, 2),
    prompt_hours: POLICY.commit_prompt_hours,
    reopened: run.commit_state === "REOPENED",
  };
  if (committed > 0) out.reason = `${committed} load${committed === 1 ? "" : "s"} committed, reopen it to move what is on it.`;
  if (run.reopened_at) out.reopened_at = run.reopened_at;
  if (run.reopen_reason) out.reopen_reason = run.reopen_reason;
  return out;
}

function gaugeOf(run) {
  const stops = stopsOnRun(run.id).filter((s) => s.status !== "CANCELLED");
  const truck = run.truck_id ? truckOf(run.truck_id) : null;
  const weighed = stops.filter((s) => typeof s.weight_kg === "number");
  const weight = weighed.length > 0 ? weighed.reduce((a, s) => a + s.weight_kg, 0) : null;
  const cap = truck ? truck.capacity_kg : null;
  const weightBand = band(weight, cap);
  const g = {
    stop_count: stops.length,
    weight_band: weightBand,
    weighed_stops: weighed.length,
    volume_band: "UNKNOWN",
    measured_stops: 0,
    pallet_band: "UNKNOWN",
    band: worst(weightBand),
  };
  if (weight !== null) g.weight_kg = weight;
  if (cap !== null && cap !== undefined) g.capacity_kg = cap;
  return g;
}

function runView(r) {
  const truck = r.truck_id ? truckOf(r.truck_id) : null;
  const driver = r.driver_id ? driverOf(r.driver_id) : null;
  const out = {
    id: r.id,
    run_no: r.run_no,
    branch_id: r.branch_id,
    scheduled_date: r.scheduled_date,
    status: r.status,
    stop_count: stopsOnRun(r.id).length,
    created_at: r.created_at,
    commit_state: r.commit_state,
    commit: commitOf(r),
    gauge: gaugeOf(r),
  };
  if (r.notes) out.notes = r.notes;
  if (r.truck_id) out.truck_id = r.truck_id;
  if (r.driver_id) out.driver_id = r.driver_id;
  if (truck) out.truck_code = truck.code;
  if (driver) out.driver_name = driver.name;
  if (r.dispatched_at) out.dispatched_at = r.dispatched_at;
  if (r.completed_at) out.completed_at = r.completed_at;
  if (r.reopened_at) out.reopened_at = r.reopened_at;
  if (r.reopen_reason) out.reopen_reason = r.reopen_reason;
  return out;
}

function truckView(t) {
  const out = { ...t };
  for (const k of ["capacity_m3", "pallet_positions"]) if (out[k] === null) delete out[k];
  return out;
}

function loadBoard(date) {
  const runs = world.runs.filter((r) => r.scheduled_date === date && r.status !== "CANCELLED");
  const loads = runs.map((r) => ({ run: runView(r), gauge: gaugeOf(r), commit: commitOf(r), stops: stopsOnRun(r.id).map(stopView) }));
  const to = addDays(date, POLICY.working_view_days - 1);
  const staging = world.stops
    .filter((s) => !s.run_id && s.status === "READY" && s.scheduled_date >= date && s.scheduled_date <= to)
    .sort((a, b) => a.scheduled_date.localeCompare(b.scheduled_date) || a.order_no.localeCompare(b.order_no))
    .map(stopView);
  const day = world.stops.filter((s) => s.scheduled_date === date && s.status !== "CANCELLED");
  const waiting = world.stops.filter((s) => !s.run_id && s.status === "READY" && s.scheduled_date >= date && s.scheduled_date <= to);
  return {
    branch_id: BRANCH,
    date,
    policy: POLICY,
    value_visible: false,
    gaps_checked: true,
    can_commit: caller.can_commit,
    can_reopen: caller.can_reopen,
    working_from: date,
    working_to: to,
    working_days: POLICY.working_view_days,
    counts: {
      deliveries: day.length,
      pickups: 3,
      pickups_awaiting_pick: 2,
      pickups_ready_to_collect: 1,
      unassigned: waiting.length,
      ready: day.filter((s) => s.status === "READY").length,
      requested: day.filter((s) => s.commit_state === "REQUESTED").length,
      committed: day.filter((s) => s.commit_state === "COMMITTED").length,
      due: waiting.filter((s) => s.commit_state !== "COMMITTED" && s.scheduled_date <= addDays(TODAY, 2)).length,
    },
    trucks: world.trucks.filter((t) => t.is_active).map(truckView),
    loads,
    staging,
  };
}

function dayBoard(date) {
  const runs = world.runs.filter((r) => r.scheduled_date === date && r.status !== "CANCELLED");
  const dayStops = world.stops.filter((s) => s.scheduled_date === date);
  return {
    branch_id: BRANCH,
    date,
    runs: runs.map(runView),
    stops: dayStops.filter((s) => s.run_id).map(stopView),
    unassigned: dayStops.filter((s) => !s.run_id && s.status === "READY").map(stopView),
  };
}

function holdOf(s) {
  const base = { stop_id: s.id, order_id: s.order_id, held: false };
  if (s.id !== HOLD_STOP || s.status === "DELIVERED") return base;
  const hold = { ...base, held: true, code: "AR", reason: "payment needed before release, call the office" };
  if (holdAmount) {
    hold.reason = "Payment is due on this order before it can be released.";
    hold.amount_due_cents = 125050;
  }
  return hold;
}

// ---- the routes ----------------------------------------------------------------------------------

function readBody(req) {
  return new Promise((resolve) => {
    let raw = "";
    req.on("data", (c) => (raw += c));
    req.on("end", () => {
      try {
        resolve(raw === "" ? {} : JSON.parse(raw));
      } catch {
        resolve(null);
      }
    });
  });
}

function reasonOf(body) {
  return typeof body?.reason === "string" ? body.reason.trim() : "";
}

function validDate(s) {
  return typeof s === "string" && /^\d{4}-\d{2}-\d{2}$/.test(s);
}

function load(stop) {
  return stop.order_no || "the load";
}

// A committed load is changed only by a caller who may reopen it (assertLoadChangeable).
function assertChangeable(res, stop) {
  if (stop.commit_state === "COMMITTED" && !caller.can_reopen) {
    fail(res, 409, "dispatch: this load is committed");
    return false;
  }
  return true;
}

// The order reference the way the core's resolver grades and resolves it. Returns the order, or sends the refusal.
function resolveOrder(res, raw) {
  const ref = typeof raw === "string" ? raw.trim() : "";
  if (ref === "") {
    fail(res, 400, "enter an order number (like #SO-TRT-1041) or an order id");
    return null;
  }
  const bare = ref.replace(/^#/, "");
  if (!/^[A-Za-z0-9_-]+$/.test(bare) || !/[0-9]/.test(bare)) {
    fail(res, 400, `"${ref}" is not an order number, enter a number like #SO-TRT-1041, or paste the order id`);
    return null;
  }
  const order = world.orders.find((o) => o.order_no === bare || o.id === ref) ?? world.stops.map((s) => ({ id: s.order_id, order_no: s.order_no, customer_name: s.customer_name, status: "CONFIRMED", fulfilment: "DELIVERY" })).find((o) => o.order_no === bare || o.id === ref);
  if (!order) {
    fail(res, 404, "dispatch: not found");
    return null;
  }
  return order;
}

function manifestHtml(run) {
  const rows = stopsOnRun(run.id)
    .map((s) => `<tr><td>${s.stop_no}</td><td>${s.order_no}</td><td>${s.customer_name}</td><td>${s.address_line || "-"}</td></tr>`)
    .join("");
  return `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Run manifest ${run.run_no}</title></head><body><h1>Run manifest ${run.run_no}</h1><table>${rows}</table></body></html>`;
}

// The scan trail an order has: ORD-0012 has two pallets, one still in the yard and one delivered and signed for.
function trailFor(orderId) {
  const stop = world.stops.find((s) => s.order_id === orderId);
  if (!stop) return null;
  const at = (hh) => `${TODAY}T${hh}:00:00Z`;
  const cp = (n, load, checkpoint, hh, extra) => ({ id: `ca000001-0000-4000-9000-0000000000${n}`, branch_id: BRANCH, order_id: orderId, load_ref: load, checkpoint, scanned_at: at(hh), ...extra });
  if (stop.order_no !== "ORD-0012") return { order_id: orderId, checkpoints: [], loads: [], complete: false };
  const checkpoints = [
    cp(10, "PLT-0012-A", "RECEIVED", "06"),
    cp(11, "PLT-0012-A", "YARD", "07"),
    cp(12, "PLT-0012-A", "PICKED", "08"),
    cp(13, "PLT-0012-B", "RECEIVED", "06"),
    cp(14, "PLT-0012-B", "DELIVERED", "09", { pod_signature_ref: "sig-demo" }),
  ];
  return {
    order_id: orderId,
    checkpoints,
    loads: [
      { load_ref: "PLT-0012-A", reached: ["RECEIVED", "YARD", "PICKED"], furthest: "PICKED", last_scanned_at: at("08") },
      { load_ref: "PLT-0012-B", reached: ["RECEIVED", "DELIVERED"], furthest: "DELIVERED", pod_signature_ref: "sig-demo", last_scanned_at: at("09") },
    ],
    complete: false,
  };
}

const usd = (cents) => `${cents < 0 ? "-" : ""}$${Math.floor(Math.abs(cents) / 100)}.${String(Math.abs(cents) % 100).padStart(2, "0")}`;

function evenSplit(total, n) {
  const base = Math.floor(total / n);
  const rem = total % n;
  return Array.from({ length: n }, (_, i) => base + (i < rem ? 1 : 0));
}

function raiseView(r) {
  const out = {
    id: r.id,
    order_id: r.order_id,
    order_no: r.order_no,
    customer_name: r.customer_name,
    branch_id: BRANCH,
    loads: r.loads,
    asked_by: "a dispatch manager",
    asked_at: r.asked_at,
    state: r.state,
    approval_id: r.approval_id,
  };
  // The figures ride only for a caller the load value policy lets see them: absent by type, no key.
  if (caller.can_see_value) {
    out.money = { quoted_freight_cents: r.quoted, current_freight_cents: r.current, proposed_freight_cents: r.proposed, delta_cents: r.proposed - r.quoted };
  }
  return out;
}

function splitLoads(orderId) {
  return world.stops.filter((s) => s.order_id === orderId && s.status !== "CANCELLED").sort((a, b) => (a.load_seq ?? 1) - (b.load_seq ?? 1));
}

function splitView(s) {
  const out = stopView(s);
  if (typeof world.shares[s.id] === "number") out.freight_cents = world.shares[s.id];
  return out;
}

function refuseIfArmed(res, method, path) {
  if (armed === null) return false;
  if (armed.path !== undefined && !path.includes(armed.path)) return false;
  if (method === "GET" && armed.reads !== true) return false;
  const a = armed;
  armed = null;
  fail(res, a.status, a.error);
  return true;
}

async function route(req, res, url) {
  const path = url.pathname;
  const method = req.method;
  const q = (k) => url.searchParams.get(k) ?? "";

  if (path === "/healthz") return json(res, { ok: true });

  // the test controls
  if (path === "/_stub/reset") {
    world = seedWorld();
    armed = null;
    caller = { can_reopen: true, can_commit: true, can_manage: true, can_set_policy: true, can_see_value: true };
    POLICY = { ...DEFAULT_POLICY };
    policyTrail = [];
    holdAmount = false;
    manyBranches = false;
    return json(res, { ok: true });
  }
  if (path === "/_stub/refuse" && method === "POST") {
    armed = await readBody(req);
    return json(res, { ok: true });
  }
  if (path === "/_stub/caller" && method === "POST") {
    caller = { ...caller, ...(await readBody(req)) };
    return json(res, { ok: true });
  }
  if (path === "/_stub/hold-amount" && method === "POST") {
    holdAmount = Boolean((await readBody(req)).on);
    return json(res, { ok: true });
  }
  if (path === "/_stub/branches" && method === "POST") {
    manyBranches = Boolean((await readBody(req)).many);
    return json(res, { ok: true });
  }
  if (path === "/_stub/raise" && method === "POST") {
    const b = await readBody(req);
    const r = world.raises.find((x) => x.id === b.id);
    if (r) r.state = b.state;
    return json(res, { ok: true });
  }
  if (path === "/_stub/world") {
    return json(res, { today: TODAY, runs: world.runs.map(runView), stops: world.stops.map(stopView) });
  }

  // the demo way in
  if (path === "/api/v1/demo/flows") return json(res, { flows: FLOWS });
  if (method === "POST" && path === "/api/v1/demo/session") {
    const body = await readBody(req);
    const account = body === null ? undefined : USERS[body.flow];
    if (!account) return fail(res, 404, "not found");
    return json(res, { token: mintToken(account.user_id), user: account, flow: body.flow });
  }

  // everything else is a staff route behind a bearer
  if (!String(req.headers.authorization ?? "").startsWith("Bearer ")) return fail(res, 401, "Missing or invalid credentials");

  if (path === "/api/v1/branches") return json(res, manyBranches ? [...BRANCHES, JULIAN] : BRANCHES);

  if (!path.startsWith("/api/v1/dispatch/")) return fail(res, 404, "not found");
  const rest = path.slice("/api/v1/dispatch/".length);

  if (refuseIfArmed(res, method, path)) return;

  // ---- reads
  if (method === "GET") {
    // The other yard is a quiet one: no trucks, no drivers, nothing on its board.
    const yard = q("branch_id") === OTHER_BRANCH;
    if (rest === "loadboard") {
      const date = validDate(q("date")) ? q("date") : TODAY;
      const view = loadBoard(date);
      if (!yard) return json(res, view);
      return json(res, { ...view, branch_id: OTHER_BRANCH, trucks: [], loads: [], staging: [], counts: { ...view.counts, deliveries: 0, unassigned: 0, ready: 0, requested: 0, committed: 0, due: 0 } });
    }
    if (rest === "board") {
      const date = validDate(q("date")) ? q("date") : TODAY;
      return json(res, yard ? { branch_id: OTHER_BRANCH, date, runs: [], stops: [], unassigned: [] } : dayBoard(date));
    }
    // A retired truck or driver leaves the pick lists and stays in the registry (include_inactive).
    const withInactive = q("include_inactive") === "true";
    if (rest === "trucks") return json(res, { items: yard ? [] : world.trucks.filter((t) => withInactive || t.is_active).map(truckView), limit: 50 });
    if (rest === "drivers") return json(res, { items: yard ? [] : world.drivers.filter((d) => withInactive || d.is_active), limit: 50 });
    if (rest === "loadboard/gaps") {
      const items = [
        { stop_id: stopOf("5a000001-0000-4000-9000-000000000002").id, order_no: "ORD-0030", sku: "PVR-CBL-60", description: "Copperline cobble paver, 60 mm", qty_needed: 480, qty_on_hand: 310, qty_short: 170, uom: "sq ft" },
      ];
      return json(res, { items, checked: true, limit: 50 });
    }
    if (rest === "loadboard/calendar") {
      const from = validDate(q("from")) ? q("from") : TODAY;
      const to = validDate(q("to")) ? q("to") : addDays(from, 6);
      const items = [];
      for (let d = from; d <= to && items.length < 60; d = addDays(d, 1)) {
        const stops = world.stops.filter((s) => s.scheduled_date === d && s.status !== "CANCELLED");
        items.push({
          date: d,
          runs: world.runs.filter((r) => r.scheduled_date === d && r.status !== "CANCELLED").length,
          stops: stops.length,
          unassigned: stops.filter((s) => !s.run_id && s.status === "READY").length,
          full: 0,
          requested: stops.filter((s) => s.commit_state === "REQUESTED").length,
        });
      }
      return json(res, { items, limit: 60 });
    }
    let m1 = /^runs\/([^/]+)\/manifest$/.exec(rest);
    if (m1) {
      const run = runOf(m1[1]);
      if (!run) return fail(res, 404, "dispatch: not found");
      res.writeHead(200, { "Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store" });
      return res.end(manifestHtml(run));
    }
    if (rest === "scan-trail") {
      if (q("order_id") === "") return fail(res, 400, "order_id is required");
      const trail = trailFor(q("order_id"));
      return trail === null ? fail(res, 404, "dispatch: not found") : json(res, trail);
    }
    let m2 = /^freight-raises\/([^/]+)$/.exec(rest);
    if (m2) {
      const raise = world.raises.find((r) => r.id === m2[1]);
      return raise ? json(res, raiseView(raise)) : fail(res, 404, "dispatch: not found");
    }
    if (rest === "policy") return json(res, POLICY);
    if (rest === "policy/history") {
      if (!caller.can_manage) return fail(res, 403, "Forbidden");
      return json(res, { items: policyTrail });
    }
    if (rest === "will-call") {
      const order = resolveOrder(res, q("order_id"));
      if (order === null) return;
      const items = world.pickups.filter((w) => w.order_id === order.id).map((w) => ({ ...w }));
      return json(res, { items, limit: 50 });
    }
    let m = /^runs\/([^/]+)\/payment-holds$/.exec(rest);
    if (m) {
      const run = runOf(m[1]);
      if (!run) return fail(res, 404, "not found");
      const items = stopsOnRun(run.id).map(holdOf).filter((h) => h.held);
      return json(res, { items, limit: 50 });
    }
    return fail(res, 404, "not found");
  }

  if (method !== "POST" && method !== "PUT") return fail(res, 404, "not found");
  const body = await readBody(req);
  if (body === null) return fail(res, 400, "bad request");

  if (rest === "policy" && method === "PUT") {
    if (!caller.can_manage || !caller.can_set_policy) return fail(res, 403, "Forbidden");
    const int = (v) => (Number.isInteger(v) ? v : 0);
    const next = {
      branch_id: BRANCH,
      commit_prompt_hours: int(body.commit_prompt_hours),
      reopen_policy: body.reopen_policy || "MANAGER",
      load_value_visibility: body.load_value_visibility || "MANAGER_UP",
      commit_horizon_weeks: int(body.commit_horizon_weeks) || 8,
      working_view_days: int(body.working_view_days) || 14,
      freight_split_mode: body.freight_split_mode || "EVEN",
      driver_session_hours: int(body.driver_session_hours) || 12,
    };
    if (next.commit_prompt_hours < 0 || next.commit_prompt_hours > 336) return fail(res, 422, "dispatch: commit_prompt_hours must be between 0 and 336");
    if (!["ANYONE", "MANAGER", "NOBODY"].includes(next.reopen_policy)) return fail(res, 422, "dispatch: reopen_policy must be ANYONE, MANAGER or NOBODY");
    if (!["MANAGER_UP", "ALL_STAFF", "HIDDEN"].includes(next.load_value_visibility)) return fail(res, 422, "dispatch: cost_visibility must be MANAGER_UP, ALL_STAFF or HIDDEN");
    if (next.commit_horizon_weeks < 1 || next.commit_horizon_weeks > 26) return fail(res, 422, "dispatch: commit_horizon_weeks must be between 1 and 26");
    if (next.working_view_days < 1 || next.working_view_days > 90) return fail(res, 422, "dispatch: working_view_days must be between 1 and 90");
    if (!["EVEN", "MANUAL"].includes(next.freight_split_mode)) return fail(res, 422, "dispatch: freight_split_mode must be EVEN or MANUAL");
    if (next.driver_session_hours < 1 || next.driver_session_hours > 168) return fail(res, 422, "dispatch: driver_session_hours must be between 1 and 168");
    const moved = [];
    for (const field of ["commit_prompt_hours", "reopen_policy", "load_value_visibility", "commit_horizon_weeks", "working_view_days", "freight_split_mode", "driver_session_hours"]) {
      if (String(POLICY[field]) !== String(next[field])) moved.push({ field, before: String(POLICY[field]), after: String(next[field]), who: "Logistics Manager", when: `${TODAY}T18:00:00Z` });
    }
    policyTrail = [...moved, ...policyTrail];
    POLICY = next;
    return json(res, POLICY);
  }

  // ---- the registry: the role gate answers a bare Forbidden below the manage level
  if ((rest === "trucks" || rest === "drivers" || /^(trucks|drivers)\/[^/]+$/.test(rest)) && (method === "POST" || method === "PUT")) {
    if (!caller.can_manage) return fail(res, 403, "Forbidden");
    const isTruck = rest.startsWith("trucks");
    const existing = (isTruck ? world.trucks : world.drivers).find((r) => r.id === rest.split("/")[1]);
    if (method === "PUT" && !existing) return fail(res, 404, "not found");
    if (method === "POST" && body.branch_id !== BRANCH) return fail(res, 400, "dispatch: no branch with that id");
    if (method === "PUT" && body.branch_id !== existing.branch_id) {
      return fail(res, 400, isTruck ? "dispatch: a truck's branch does not move; retire it here and register it at the other yard" : "dispatch: a driver's branch does not move; retire them here and register them at the other yard");
    }
    if (isTruck) {
      const code = String(body.code ?? "").trim();
      if (code === "") return fail(res, 400, "dispatch: a truck needs a code");
      const type = String(body.truck_type || "FLATBED").toUpperCase();
      if (!["FLATBED", "BOOM", "DUMP", "PICKUP", "TRAILER"].includes(type)) return fail(res, 400, `dispatch: unknown truck_type "${type}"`);
      if (typeof body.capacity_kg === "number" && body.capacity_kg < 0) return fail(res, 400, "dispatch: capacity_kg must not be negative");
      if (world.trucks.some((t) => t.code === code && t.id !== existing?.id)) return fail(res, 409, "dispatch: a truck with that code already exists at this branch");
      const row = {
        id: existing?.id ?? `t0000001-0000-4000-9000-0000000000${String(world.seq.truck++).padStart(2, "0")}`,
        branch_id: BRANCH,
        code,
        name: String(body.name ?? ""),
        truck_type: type,
        capacity_kg: body.capacity_kg ?? null,
        capacity_m3: body.capacity_m3 ?? null,
        pallet_positions: body.pallet_positions ?? null,
        is_active: method === "POST" ? true : body.is_active !== false,
      };
      if (existing) Object.assign(existing, row);
      else world.trucks.push(row);
      return method === "POST" ? json(res, { id: row.id }, 201) : json(res, truckView(row));
    }
    const name = String(body.name ?? "").trim();
    if (name === "") return fail(res, 400, "dispatch: a driver needs a name");
    const login = String(body.user_id ?? "");
    if (login !== "" && !/^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/.test(login)) {
      return fail(res, 400, "dispatch: user_id must be a user id, or left out for a driver with no login");
    }
    const row = {
      id: existing?.id ?? `d0000001-0000-4000-9000-0000000000${String(world.seq.driver++).padStart(2, "0")}`,
      branch_id: BRANCH,
      name,
      phone: String(body.phone ?? ""),
      licence_class: String(body.licence_class ?? ""),
      is_active: method === "POST" ? true : body.is_active !== false,
    };
    if (login !== "") row.user_id = login;
    if (existing) {
      delete existing.user_id;
      Object.assign(existing, row);
    } else world.drivers.push(row);
    return method === "POST" ? json(res, { id: row.id }, 201) : json(res, row);
  }

  if (method !== "POST") return fail(res, 404, "not found");

  let m0;
  // ---- the auto assign seam: the core build answers 501 and says what would answer it
  if (rest === "auto-assign") {
    return fail(res, 501, "dispatch: automatic load assignment is part of the Pro module, build the loads here in the meantime");
  }

  if (rest === "freight-split") {
    const order = resolveOrder(res, body.order_id);
    if (order === null) return;
    if (!caller.can_see_value) return fail(res, 403, "dispatch: forbidden");
    const loads = Number(body.loads);
    if (!(loads >= 1 && loads <= 10)) return fail(res, 422, "dispatch: an order goes out on between 1 and 10 loads");
    const existing = splitLoads(order.id);
    if (existing.length === 0) return fail(res, 409, "dispatch: schedule this order for delivery before splitting its freight");
    if (existing.length > loads) return fail(res, 409, `dispatch: this order is already on ${existing.length} loads, cancel one before splitting into ${loads}`);
    for (const st of existing) {
      if (st.commit_state === "COMMITTED" && !caller.can_reopen) return fail(res, 409, `dispatch: ${st.order_no} is committed, reopen it first, or ask a dispatch manager to`);
    }
    let fr = world.freight[order.id];
    const proposal = body.freight_cents === undefined || body.freight_cents === null ? null : Number(body.freight_cents);
    if (!fr) {
      if (proposal === null) return fail(res, 409, "dispatch: this order carries no freight to split");
      fr = { current: 0, quoted: null };
    }
    const current = fr.current;
    const proposed = proposal === null ? current : proposal;
    if (proposed < 0) return fail(res, 422, "dispatch: freight cannot be negative");
    if (proposed < current) return fail(res, 422, `dispatch: this order's freight is ${usd(current)}, lowering it is a credit note, not a split`);
    let amounts = evenSplit(proposed, loads);
    let override = false;
    if (Array.isArray(body.amounts) && body.amounts.length > 0) {
      if (body.amounts.length !== loads) return fail(res, 422, `dispatch: the split has ${body.amounts.length} amounts and the order is on ${loads} loads`);
      if (body.amounts.some((a) => a < 0)) return fail(res, 422, "dispatch: a load's freight cannot be negative");
      const sum = body.amounts.reduce((a, b) => a + b, 0);
      if (sum !== proposed) return fail(res, 422, `dispatch: the split comes to ${usd(sum)} and the order's freight is ${usd(proposed)}, they have to match`);
      amounts = body.amounts;
      override = true;
    }
    // The gate: a raise above the quote that nobody has agreed to parks, and nothing is written.
    if (fr.quoted !== null && proposed > fr.quoted && proposed > current) {
      const parked = world.raises.find((r) => r.order_id === order.id && r.state === "PARKED");
      const same = (r) => r.proposed === proposed && r.loads === loads && JSON.stringify(r.amounts ?? []) === JSON.stringify(body.amounts ?? []);
      if (parked && !same(parked)) return fail(res, 409, "dispatch: a freight raise on this order is already waiting for approval, that one has to be decided before another can be asked for");
      const raise = parked ?? {
        id: `fa000001-0000-4000-9000-0000000000${String(world.seq.raise++).padStart(2, "0")}`,
        order_id: order.id,
        order_no: order.order_no,
        customer_name: order.customer_name,
        loads,
        asked_at: `${TODAY}T08:00:00Z`,
        state: "PARKED",
        approval_id: `ab000001-0000-4000-9000-0000000000${String(world.seq.raise).padStart(2, "0")}`,
        quoted: fr.quoted,
        current,
        proposed,
        amounts: body.amounts,
      };
      if (!parked) world.raises.push(raise);
      return json(res, { order_id: order.id, freight_cents: current, override: false, loads: existing.map(splitView), parked: raiseView(raise) });
    }
    const parent = existing[0];
    while (existing.length < loads) {
      const n = world.seq.split++;
      const copy = { ...parent, id: `5a000001-0000-4000-9000-0000000000${n}`, run_id: "", stop_no: 0, status: "READY", commit_state: "REQUESTED", committed_at: null };
      world.stops.push(copy);
      existing.push(copy);
    }
    existing.forEach((st, i) => {
      st.load_seq = i + 1;
      st.load_count = loads;
      world.shares[st.id] = amounts[i];
    });
    world.freight[order.id] = { current: proposed, quoted: fr.quoted };
    return json(res, { order_id: order.id, freight_cents: proposed, override, loads: existing.map(splitView) });
  }

  if (rest === "will-call") {
    const order = resolveOrder(res, body.order_id);
    if (order === null) return;
    if (order.fulfilment !== "WILL_CALL") return fail(res, 409, `dispatch: this is ${order.fulfilment === "DELIVERY" ? "a delivery" : "a counter pickup"}, not will-call, a rack tag prints only for a will-call order`);
    if (order.status === "DRAFT") return fail(res, 409, "dispatch: confirm the order before printing a pickup code");
    if (order.status === "CANCELLED" || order.status === "CLOSED") return fail(res, 409, order.status === "CANCELLED" ? "dispatch: this order was cancelled" : "dispatch: this order is already closed");
    if (order.pick === "NONE") return fail(res, 409, "dispatch: nothing has been picked for this order yet, it is in the yard's queue, and the tag prints once the pick is staged");
    if (order.held) return fail(res, 409, "dispatch: This order cannot reach picking until the balance is paid.");
    if (world.pickups.some((w) => w.order_id === order.id && w.status === "ACTIVE")) {
      return fail(res, 409, "dispatch: this order already has a live pickup code, revoke it before printing another");
    }
    const n = world.seq.pickup++;
    const row = {
      id: `9c000001-0000-4000-9000-0000000000${String(n).padStart(2, "0")}`,
      order_id: order.id,
      branch_id: BRANCH,
      status: "ACTIVE",
      expires_at: `${addDays(TODAY, Number(body.ttl_hours) > 0 ? Math.ceil(Number(body.ttl_hours) / 24) : 14)}T18:00:00Z`,
      created_at: `${TODAY}T08:00:00Z`,
    };
    world.pickups.push(row);
    const token = Buffer.from(`${row.id}-${n}-stub-pickup-token-0000`).toString("base64url");
    res.setHeader("Cache-Control", "no-store");
    return json(res, { ...row, token }, 201);
  }

  m0 = /^will-call\/([^/]+)\/revoke$/.exec(rest);
  if (m0) {
    if (!caller.can_manage) return fail(res, 403, "Forbidden");
    if (!/^[0-9a-f]{8}-/.test(m0[1])) return fail(res, 404, "dispatch: not found");
    const row = world.pickups.find((w) => w.id === m0[1]);
    if (!row) return fail(res, 404, "dispatch: not found");
    if (row.status !== "ACTIVE") return fail(res, 409, `dispatch: this pickup code is already ${row.status.toLowerCase()}`);
    row.status = "REVOKED";
    return json(res, { status: "REVOKED" });
  }

  if (rest === "stops") {
    const order = resolveOrder(res, body.order_id);
    if (order === null) return;
    const date = typeof body.scheduled_date === "string" ? body.scheduled_date.trim() : "";
    if (date === "") return fail(res, 400, "dispatch: scheduled_date is required (YYYY-MM-DD)");
    if (!validDate(date)) return fail(res, 400, "dispatch: scheduled_date must be YYYY-MM-DD");
    if ((body.latitude === null || body.latitude === undefined) !== (body.longitude === null || body.longitude === undefined)) {
      return fail(res, 400, "dispatch: latitude and longitude must be given together");
    }
    if (order.status === "DRAFT") return fail(res, 409, "dispatch: confirm the order before scheduling a delivery for it");
    if (order.status === "CANCELLED" || order.status === "CLOSED") return fail(res, 409, `dispatch: order is ${order.status} and cannot be scheduled`);
    const live = world.stops.find((s) => s.order_id === order.id && s.status !== "CANCELLED");
    if (live) return fail(res, 409, `dispatch: this order already has a delivery scheduled for ${live.scheduled_date}`);
    if (date > addDays(TODAY, POLICY.commit_horizon_weeks * 7) || date < addDays(TODAY, -1)) {
      return fail(res, 422, `dispatch: ${date} is outside the ${POLICY.commit_horizon_weeks}-week planning horizon`);
    }
    const n = world.seq.stop++;
    const stop = {
      id: `5a000001-0000-4000-9000-0000000000${n}`,
      branch_id: BRANCH,
      order_id: order.id,
      order_no: order.order_no,
      customer_name: order.customer_name,
      run_id: "",
      stop_no: 0,
      scheduled_date: date,
      status: "READY",
      time_window: body.time_window ?? "",
      contact_name: body.contact_name ?? "",
      contact_phone: body.contact_phone ?? "",
      address_line: body.address_line ?? "",
      city: body.city ?? "",
      postal_code: body.postal_code ?? "",
      latitude: body.latitude ?? null,
      longitude: body.longitude ?? null,
      instructions: body.instructions ?? "",
      photo_count: 0,
      created_at: `${TODAY}T08:00:00Z`,
      weight_kg: null,
      volume_m3: null,
      pallet_positions: null,
      requested_date: typeof body.requested_date === "string" && body.requested_date !== "" ? body.requested_date : date,
      commit_state: "REQUESTED",
      committed_at: null,
    };
    world.stops.push(stop);
    return json(res, stopView(stop), 201);
  }

  if (rest === "runs") {
    if (!validDate(body.scheduled_date)) return fail(res, 400, "dispatch: scheduled_date must be YYYY-MM-DD");
    const n = world.seq.run++;
    const run = {
      id: `r0000001-0000-4000-9000-0000000000${String(n).padStart(2, "0")}`,
      run_no: `RUN-PLM-${n}`,
      branch_id: BRANCH,
      scheduled_date: body.scheduled_date,
      truck_id: body.truck_id ?? "",
      driver_id: body.driver_id ?? "",
      status: "PLANNED",
      notes: body.notes ?? "",
      dispatched_at: null,
      completed_at: null,
      created_at: `${TODAY}T08:00:00Z`,
      commit_state: "AUTO",
    };
    world.runs.push(run);
    return json(res, runView(run), 201);
  }

  if (rest === "loads/mark-loaded") {
    const ids = Array.isArray(body.stop_ids) ? body.stop_ids : [];
    const items = ids.map((id) => {
      const s = stopOf(id);
      if (!s) return { stop_id: id, loaded: false, refusal: "dispatch: not found" };
      const o = { stop_id: id, order_no: s.order_no, loaded: false };
      if (!s.run_id) o.refusal = "dispatch: the stop is not assigned to a run";
      else if (s.status !== "READY") o.refusal = "dispatch: illegal status transition";
      else if (s.id === HOLD_STOP) o.refusal = "dispatch: this order is held for payment";
      else {
        s.status = "LOADED";
        o.loaded = true;
      }
      return o;
    });
    return json(res, { items });
  }

  let m = /^runs\/([^/]+)\/(crew|dispatch|cancel|complete|reopen|reopen\/clear|reschedule)$/.exec(rest);
  if (m) {
    const run = runOf(m[1]);
    if (!run) return fail(res, 404, "not found");
    const act = m[2];
    if (act === "crew") {
      if (run.status !== "PLANNED") return fail(res, 409, `dispatch: a ${run.status} run cannot be re-crewed`);
      const onRun = stopsOnRun(run.id);
      if (onRun.some((s) => s.commit_state === "COMMITTED") && !caller.can_reopen) return fail(res, 409, "dispatch: this load is committed");
      if (body.truck_id && !truckOf(body.truck_id)) return fail(res, 404, "not found");
      if (body.driver_id && !driverOf(body.driver_id)) return fail(res, 404, "not found");
      run.truck_id = body.truck_id ?? "";
      run.driver_id = body.driver_id ?? "";
      return json(res, runView(run));
    }
    if (act === "dispatch") {
      if (run.status !== "PLANNED") return fail(res, 409, "dispatch: illegal status transition");
      if (!run.truck_id || !run.driver_id) return fail(res, 409, "dispatch: a run needs both a truck and a driver before it can be dispatched");
      run.status = "DISPATCHED";
      run.dispatched_at = `${TODAY}T08:15:00Z`;
      for (const s of stopsOnRun(run.id)) if (s.status === "READY") s.status = "LOADED";
      return json(res, { ...runView(run), stops: stopsOnRun(run.id).map(stopView), geocoded_stops: 0, ungeocoded_stops: stopsOnRun(run.id).length });
    }
    if (act === "cancel") {
      if (run.status !== "PLANNED") return fail(res, 409, "dispatch: illegal status transition");
      for (const s of stopsOnRun(run.id)) {
        if (s.status === "READY") {
          s.run_id = "";
          s.stop_no = 0;
        }
      }
      run.status = "CANCELLED";
      return json(res, runView(run));
    }
    if (act === "complete") {
      if (run.status !== "DISPATCHED") return fail(res, 409, "dispatch: illegal status transition");
      if (stopsOnRun(run.id).some((s) => !["DELIVERED", "FAILED", "CANCELLED"].includes(s.status))) {
        return fail(res, 409, "dispatch: the run still has stops that are not finished");
      }
      run.status = "COMPLETED";
      run.completed_at = `${TODAY}T16:00:00Z`;
      return json(res, runView(run));
    }
    if (act === "reopen") {
      if (!caller.can_reopen) return fail(res, 403, "dispatch: only a dispatch manager can reopen a committed load");
      const why = reasonOf(body);
      if (why === "") return fail(res, 422, "dispatch: say why this load is being reopened, somebody will read it after the truck has gone");
      for (const s of stopsOnRun(run.id)) if (s.commit_state === "COMMITTED") s.commit_state = "REQUESTED";
      run.commit_state = "REOPENED";
      run.reopened_at = `${TODAY}T09:00:00Z`;
      run.reopen_reason = why;
      return json(res, runView(run));
    }
    if (act === "reopen/clear") {
      run.commit_state = "AUTO";
      delete run.reopened_at;
      delete run.reopen_reason;
      return json(res, runView(run));
    }
    if (act === "reschedule") {
      if (run.status !== "PLANNED") return fail(res, 409, "dispatch: this delivery has already left the planning stage and its date can no longer be changed");
      if (!validDate(body.to_date)) return fail(res, 400, "dispatch: to_date must be YYYY-MM-DD");
      if (body.to_date === run.scheduled_date) return fail(res, 400, "dispatch: that is already the scheduled date");
      if (body.to_date < TODAY) return fail(res, 400, `dispatch: ${body.to_date} has already passed, a delivery cannot be promised for a past date`);
      if (reasonOf(body) === "") return fail(res, 400, "dispatch: say why the delivery is moving, the customer will ask");
      const from = run.scheduled_date;
      run.scheduled_date = body.to_date;
      const moved = stopsOnRun(run.id);
      for (const s of moved) s.scheduled_date = body.to_date;
      return json(res, { entity_type: "delivery_run", entity_id: run.id, from_date: from, to_date: body.to_date, stops_moved: moved.length, reschedule_id: `rs000001-0000-4000-9000-00000000000${world.seq.resched++}`, run: runView(run) });
    }
  }

  m = /^runs\/([^/]+)\/manifest\/print$/.exec(rest);
  if (m) {
    if (!runOf(m[1])) return fail(res, 404, "dispatch: not found");
    return json(res, { job_id: `pj000001-0000-4000-9000-0000000000${String(world.seq.job++).padStart(2, "0")}`, doc_type: "DELIVERY_DOC" }, 201);
  }

  m = /^stops\/([^/]+)\/(assign|commit|status|reopen|reschedule)$/.exec(rest);
  if (m) {
    const stop = stopOf(m[1]);
    if (!stop) return fail(res, 404, "not found");
    const act = m[2];
    if (act === "assign") {
      if (stop.status !== "READY") return fail(res, 409, `dispatch: a ${stop.status} stop cannot be re-assigned`);
      if (!caller.can_commit) return fail(res, 403, "dispatch: a salesperson can request a delivery date, only dispatch can commit one");
      if (!assertChangeable(res, stop)) return;
      if (body.run_id) {
        const run = runOf(body.run_id);
        if (!run) return fail(res, 404, "not found");
        if (run.status !== "PLANNED") return fail(res, 409, `dispatch: stops cannot be added to a ${run.status} run`);
        if (run.scheduled_date !== stop.scheduled_date) return fail(res, 409, `dispatch: the stop is scheduled for ${stop.scheduled_date} and the run is ${run.scheduled_date}`);
        stop.run_id = run.id;
        stop.stop_no = body.stop_no > 0 ? body.stop_no : stopsOnRun(run.id).length;
        if (stop.commit_state === "REQUESTED") {
          stop.commit_state = "COMMITTED";
          stop.committed_at = `${TODAY}T08:30:00Z`;
        }
      } else {
        stop.run_id = "";
        stop.stop_no = 0;
      }
      return json(res, stopView(stop));
    }
    if (act === "commit") {
      if (!caller.can_commit) return fail(res, 403, "dispatch: a salesperson can request a delivery date, only dispatch can commit one");
      const date = typeof body.scheduled_date === "string" ? body.scheduled_date.trim() : "";
      if (date !== "" && !validDate(date)) return fail(res, 400, "dispatch: scheduled_date must be YYYY-MM-DD");
      if (date !== "" && date !== stop.scheduled_date && !assertChangeable(res, stop)) return;
      if (date !== "") stop.scheduled_date = date;
      stop.commit_state = "COMMITTED";
      stop.committed_at = `${TODAY}T08:40:00Z`;
      return json(res, stopView(stop));
    }
    if (act === "status") {
      const to = String(body.status ?? "").toUpperCase();
      if (to === "DELIVERED") return fail(res, 409, "dispatch: a delivery must carry a proof-of-delivery attestation");
      const legal = { READY: ["LOADED", "CANCELLED"], LOADED: ["EN_ROUTE", "CANCELLED"], EN_ROUTE: ["DELIVERED", "FAILED"] };
      if (!(legal[stop.status] ?? []).includes(to)) return fail(res, 409, "dispatch: illegal status transition");
      if (to === "LOADED" && !stop.run_id) return fail(res, 409, "dispatch: the stop is not assigned to a run");
      if (to === "LOADED" && stop.id === HOLD_STOP) return fail(res, 409, "dispatch: this order is held for payment");
      if (to === "EN_ROUTE") {
        const run = runOf(stop.run_id);
        if (!run) return fail(res, 409, "dispatch: the stop is not assigned to a run");
        if (run.status !== "DISPATCHED") return fail(res, 409, "dispatch: the run has not been dispatched");
      }
      if (to === "FAILED" && reasonOf(body) === "") return fail(res, 400, "dispatch: a failed stop needs a reason");
      stop.status = to;
      if (to === "FAILED") stop.failure_reason = reasonOf(body);
      return json(res, stopView(stop));
    }
    if (act === "reopen") {
      if (!caller.can_reopen) return fail(res, 403, "dispatch: only a dispatch manager can reopen a committed load");
      if (stop.commit_state !== "COMMITTED") return fail(res, 409, `dispatch: ${load(stop)} is already open, nobody has committed it`);
      const why = reasonOf(body);
      if (why === "") return fail(res, 422, "dispatch: say why this load is being reopened, somebody will read it after the truck has gone");
      stop.commit_state = "REQUESTED";
      stop.reopened_at = `${TODAY}T09:05:00Z`;
      stop.reopen_reason = why;
      return json(res, stopView(stop));
    }
    if (act === "reschedule") {
      if (stop.status !== "READY" || stop.run_id) return fail(res, 409, "dispatch: this delivery has already left the planning stage and its date can no longer be changed");
      if (!validDate(body.to_date)) return fail(res, 400, "dispatch: to_date must be YYYY-MM-DD");
      if (body.to_date === stop.scheduled_date) return fail(res, 400, "dispatch: that is already the scheduled date");
      if (body.to_date < TODAY) return fail(res, 400, `dispatch: ${body.to_date} has already passed, a delivery cannot be promised for a past date`);
      if (reasonOf(body) === "") return fail(res, 400, "dispatch: say why the delivery is moving, the customer will ask");
      const from = stop.scheduled_date;
      stop.scheduled_date = body.to_date;
      return json(res, { entity_type: "delivery_stop", entity_id: stop.id, from_date: from, to_date: body.to_date, stops_moved: 1, reschedule_id: `rs000001-0000-4000-9000-00000000000${world.seq.resched++}`, stop: stopView(stop) });
    }
  }

  return fail(res, 404, "not found");
}

const server = createServer((req, res) => {
  const url = new URL(req.url, `http://127.0.0.1:${port}`);
  route(req, res, url).catch((err) => {
    fail(res, 500, `stub error: ${String(err)}`);
  });
});
server.listen(port, () => {
  console.log(`dispatch-stub listening on ${port}`);
});
