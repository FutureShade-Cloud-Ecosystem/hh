// HH Drive's local stub: the demo way in, and the driver's world answered in
// the core's shapes. It is a development and test double only; the real
// routes are the core's (core/internal/dispatch). Item 6's contract test holds
// every payload below to the contract's field list, field by field.
import { createServer } from "node:http";

const port = Number(process.env.DRIVE_STUB_PORT ?? 4949);

// The real flows route lists every live role; the app offers only its own card.
const FLOWS = [
  { flow: "sales", role_code: "inside_sales", label: "Sales" },
  { flow: "driver", role_code: "driver", label: "Driver" },
];

const USERS = {
  driver: {
    user_id: "de110000-0000-4000-9000-000000000003",
    email: "driver@hh-demo.invalid",
    display_name: "Driver (demo)",
    role_code: "driver",
    role_name: "Driver",
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
// stripped (httpx.CounterMessage), the status the dispatch taxonomy grades.
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

// ---- the world ---------------------------------------------------------
//
// One branch, one driver, one run dispatched on the dealer's local day, three
// stops, each with the lines that come off the truck. The day is named the way
// the core names it for this dealer (the house zone), never the host's UTC day.

const HOUSE_ZONE = "America/Toronto";
const BRANCH_ID = "b0000000-0000-4000-9000-000000000001";
const DRIVER_ID = "d1000000-0000-4000-9000-000000000001";
const RUN_ID = "a1000000-0000-4000-9000-000000000005";

function houseDay(now = new Date()) {
  return new Intl.DateTimeFormat("en-CA", { timeZone: HOUSE_ZONE, year: "numeric", month: "2-digit", day: "2-digit" }).format(now);
}

const LINES = {
  1: [
    { line_no: 1, sku: "", description: "Natural stone pavers, grey", qty: "120.000", uom: "sqft" },
    { line_no: 2, sku: "", description: "Polymeric sand, 50 lb bag", qty: "6.000", uom: "bag" },
  ],
  2: [{ line_no: 1, sku: "", description: "Retaining wall block, charcoal", qty: "84.000", uom: "each" }],
  3: [{ line_no: 1, sku: "", description: "Paver base gravel, bulk", qty: "2.500", uom: "tonne" }],
};

const STOPS_SEED = [
  { n: 1, customer: "Northline Hardscapes Ltd (demo)", order: "SO-1001", address: "95 Bagot St", postal: "K7L 3E7", window: "AM", contact: "Site contact (demo)", phone: "613-555-0101", note: "Gate code on the paperwork. Unload on the driveway." },
  { n: 2, customer: "Harbourview Builders (demo)", order: "SO-1002", address: "1201 Division St", postal: "K7K 6X4", window: "AM", contact: "", phone: "", note: "" },
  { n: 3, customer: "County of Hastings (demo)", order: "SO-1003", address: "216 Ontario St", postal: "K7L 2Z3", window: "PM", contact: "Yard lead (demo)", phone: "613-555-0103", note: "Call ahead. Forklift on site." },
];

let world;
let scenario;
let log;
let photoSeq;

function buildWorld() {
  const day = houseDay();
  const created = new Date().toISOString();
  const stops = STOPS_SEED.map((seed) => ({
    id: `5a000000-0000-4000-9000-00000000000${seed.n}`,
    branch_id: BRANCH_ID,
    order_id: `0d000000-0000-4000-9000-00000000000${seed.n}`,
    order_no: seed.order,
    customer_name: seed.customer,
    run_id: RUN_ID,
    run_no: "RUN-R5",
    stop_no: seed.n,
    scheduled_date: day,
    status: "READY",
    time_window: seed.window,
    ...(seed.contact ? { contact_name: seed.contact } : {}),
    ...(seed.phone ? { contact_phone: seed.phone } : {}),
    address_line: seed.address,
    city: "Kingston",
    postal_code: seed.postal,
    ...(seed.note ? { instructions: seed.note } : {}),
    photo_count: 0,
    created_at: created,
    weight_kg: 400 * seed.n,
  }));
  return {
    day,
    run: {
      id: RUN_ID,
      run_no: "RUN-R5",
      branch_id: BRANCH_ID,
      scheduled_date: day,
      truck_id: "c1000000-0000-4000-9000-000000000001",
      driver_id: DRIVER_ID,
      truck_code: "FLAT-1",
      driver_name: "Driver (demo)",
      status: "DISPATCHED",
      dispatched_at: created,
      stop_count: stops.length,
      created_at: created,
    },
    stops,
    photos: new Map(),
  };
}

function reset() {
  world = buildWorld();
  scenario = "";
  log = [];
  photoSeq = 0;
}
reset();

const stopLabel = (stop) => `stop ${stop.stop_no} (${stop.order_no})`;
const SETTLED = new Set(["DELIVERED", "FAILED", "CANCELLED"]);
const UNRESOLVED_WORD = { READY: "not loaded yet", LOADED: "on the truck", EN_ROUTE: "still en route" };

const TRANSITIONS = {
  READY: ["LOADED", "CANCELLED"],
  LOADED: ["EN_ROUTE", "CANCELLED"],
  EN_ROUTE: ["DELIVERED", "FAILED"],
  DELIVERED: [],
  FAILED: [],
  CANCELLED: [],
};

function stopsInOrder() {
  return [...world.stops].sort((a, b) => a.stop_no - b.stop_no);
}

function findStop(id) {
  return world.stops.find((s) => s.id === id);
}

// The "poisoned" scenario is NOT the core's shape: it lays money fields on
// every read the driver makes, to prove the app neither reads nor shows them
// even if a server one day sent them. The clean shapes below are the core's.
export const POISON = 987654;

function runDetail() {
  const stops = stopsInOrder();
  const stopLines = {};
  for (const stop of stops) stopLines[stop.id] = LINES[stop.stop_no] ?? [];
  const run = { ...world.run, stop_count: stops.length };
  if (scenario === "poisoned") {
    run.gauge = { value_cents: POISON, freight_cents: POISON, band: "GREEN" };
    for (const id of Object.keys(stopLines)) {
      stopLines[id] = stopLines[id].map((l) => ({ ...l, unit_price: POISON, extended_cents: POISON }));
    }
    return {
      ...run,
      stops: stops.map((s) => ({ ...s, value_cents: POISON, freight_cents: POISON, total_cents: POISON, payment_hold: { held: true, amount_due_cents: POISON } })),
      geocoded_stops: 0,
      ungeocoded_stops: stops.length,
      stop_lines: stopLines,
    };
  }
  return { ...run, stops, geocoded_stops: 0, ungeocoded_stops: stops.length, stop_lines: stopLines };
}

function stopDetail(stop) {
  return { ...stop, photos: world.photos.get(stop.id) ?? [], mto: { mto_class: "STANDARD" } };
}

const PHOTO_TYPES = new Set(["image/jpeg", "image/png", "image/webp"]);

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
    // A cancelled stop is something only dispatch records; the world starts with one.
    if (scenario === "has-cancelled") world.stops[2].status = "CANCELLED";
    return json(res, { ok: true, scenario });
  }
  if (req.method === "GET" && path === "/_stub/log") return json(res, log);
  if (req.method === "GET" && path === "/_stub/state") return json(res, { run: world.run, stops: stopsInOrder() });

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

  // ---- the dispatch routes the driver uses ----
  if (!path.startsWith("/api/v1/dispatch/")) return notFound(res);
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
    contentType,
    bytes: contentType.startsWith("image/") ? raw.length : undefined,
    body,
  });
  if (!authorization.startsWith("Bearer ")) return refuse(res, 401, "unauthenticated");

  if (req.method === "GET" && path === "/api/v1/dispatch/my-runs") {
    if (scenario === "not-driver") return notFound(res);
    const date = url.searchParams.get("date") ?? houseDay();
    if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) return refuse(res, 400, "date must be YYYY-MM-DD");
    const items = scenario === "empty" || date !== world.day ? [] : [runDetail()];
    return json(res, { items });
  }

  const stopMatch = /^\/api\/v1\/dispatch\/stops\/([^/]+)(?:\/([a-z-]+))?$/.exec(path);
  if (stopMatch) {
    const stop = findStop(stopMatch[1]);
    if (!stop) return notFound(res);
    const action = stopMatch[2] ?? "";
    if (req.method === "GET" && action === "") return json(res, stopDetail(stop));
    if (req.method === "POST" && action === "status") return advance(res, stop, body ?? {});
    if (req.method === "POST" && action === "pod") return capturePod(res, stop, body ?? {});
    if (req.method === "POST" && action === "photos") return addPhoto(res, stop, contentType, url.searchParams.get("caption") ?? "", raw);
    if (req.method === "POST" && action === "attach-photos") return json(res, stopDetail(stop));
    return notFound(res);
  }

  const runMatch = /^\/api\/v1\/dispatch\/runs\/([^/]+)\/complete$/.exec(path);
  if (req.method === "POST" && runMatch) {
    if (runMatch[1] !== world.run.id) return notFound(res);
    return completeRun(res);
  }
  return notFound(res);
}

function advance(res, stop, input) {
  const to = String(input.status ?? "").trim().toUpperCase();
  if (to === "DELIVERED") return refuse(res, 409, "a delivery must carry a proof-of-delivery attestation");
  if (!(TRANSITIONS[stop.status] ?? []).includes(to)) return refuse(res, 409, "illegal status transition");
  if (to === "EN_ROUTE" && (scenario === "run-planned" || world.run.status !== "DISPATCHED")) {
    return refuse(res, 409, "the run has not been dispatched");
  }
  const reason = String(input.reason ?? "").trim();
  if (to === "FAILED" && reason === "") return refuse(res, 400, "a failed stop needs a reason");
  const now = new Date().toISOString();
  stop.status = to;
  if (to === "LOADED") stop.loaded_at = now;
  if (to === "EN_ROUTE") stop.en_route_at = now;
  if (to === "FAILED") stop.failure_reason = reason;
  return json(res, stop);
}

function capturePod(res, stop, input) {
  if (stop.status !== "EN_ROUTE") return refuse(res, 409, "illegal status transition");
  const name = String(input.received_by ?? "").trim();
  if (name === "") return refuse(res, 400, "type the full name of the person receiving this delivery");
  if (input.affirmed !== true) return refuse(res, 400, "the recipient must confirm the delivery before it can be signed");
  const now = new Date().toISOString();
  stop.status = "DELIVERED";
  stop.pod_signature_ref = `sig-${stop.stop_no}`;
  stop.pod_received_by = name;
  stop.pod_at = now;
  stop.delivered_at = now;
  return json(res, stopDetail(stop));
}

// The core's own limit (dispatch.MaxPhotoBytes): the handler stops reading one
// byte past it (413), and the service refuses anything over it (400).
const MAX_PHOTO_BYTES = 4 << 20;

function addPhoto(res, stop, contentType, caption, raw) {
  if (raw.length > MAX_PHOTO_BYTES + 1) return refuse(res, 413, "the photo is too large");
  if (scenario === "photo-fails") return refuse(res, 500, "internal error");
  if (!["EN_ROUTE", "DELIVERED", "FAILED"].includes(stop.status)) {
    return refuse(res, 409, `a ${stop.status} stop has nothing to photograph yet`);
  }
  if (!PHOTO_TYPES.has(contentType.toLowerCase())) return refuse(res, 400, "a proof photo must be a jpeg, png or webp");
  if (raw.length === 0) return refuse(res, 400, "the photo is empty");
  if (raw.length > MAX_PHOTO_BYTES) return refuse(res, 400, "the photo is too large (max 4 MB)");
  photoSeq += 1;
  const meta = {
    id: `9b000000-0000-4000-9000-${String(photoSeq).padStart(12, "0")}`,
    stop_id: stop.id,
    content_type: contentType.toLowerCase(),
    byte_size: raw.length,
    ...(caption.trim() ? { caption: caption.trim() } : {}),
    captured_at: new Date().toISOString(),
  };
  world.photos.set(stop.id, [...(world.photos.get(stop.id) ?? []), meta]);
  stop.photo_count += 1;
  return json(res, meta, 201);
}

function completeRun(res) {
  if (scenario === "close-refused") {
    return refuse(res, 409, "stop 2 (SO-1002) is on the truck, every stop must be delivered or marked not delivered before this run can be closed");
  }
  if (world.run.status !== "DISPATCHED") return refuse(res, 409, "illegal status transition");
  const open = stopsInOrder().find((s) => !SETTLED.has(s.status));
  if (open) {
    return refuse(
      res,
      409,
      `${stopLabel(open)} is ${UNRESOLVED_WORD[open.status] ?? open.status.toLowerCase()}, every stop must be delivered or marked not delivered before this run can be closed`,
    );
  }
  // The driver's narrower gate: delivered or not delivered are the two outcomes a driver records.
  const unanswered = stopsInOrder().find((s) => s.status !== "DELIVERED" && s.status !== "FAILED");
  if (unanswered) {
    return refuse(
      res,
      409,
      `${stopLabel(unanswered)} is ${unanswered.status.toLowerCase()}, and that is not an outcome a driver records, ask dispatch to close this run`,
    );
  }
  world.run.status = "COMPLETED";
  world.run.completed_at = new Date().toISOString();
  return json(res, world.run);
}

const server = createServer((req, res) => {
  handle(req, res).catch(() => refuse(res, 500, "internal error"));
});
server.listen(port, () => {
  console.log(`drive-stub listening on ${port}`);
});
