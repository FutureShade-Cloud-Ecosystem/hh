// HH Drive's local stub: the demo way in, and the driver's world answered in
// the core's shapes. It is a development and test double only; the real
// routes are the core's (core/internal/dispatch). Item 6's contract test holds
// every payload below to the contract's field list, field by field.
import { createServer } from "node:http";

// The stub's clock. It reads the host's, unless a leg pins it: the dealer's day
// the world is built on must be the day the pinned browser sees, at any wall
// hour, so every time the stub reads goes through nowMs().
let clockOffsetMs = 0;
const nowMs = () => Date.now() + clockOffsetMs;

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
  const issuedAtSeconds = Math.floor(nowMs() / 1000);
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

function houseDay(now = new Date(nowMs())) {
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
  { n: 1, customer: "Summit Ridge Builders", order: "SO-1001", address: "418 Oak Hollow Road", postal: "92036", window: "AM", contact: "Site contact (demo)", phone: "619-555-0101", note: "Gate code on the paperwork. Unload on the driveway." },
  { n: 2, customer: "Harbourview Builders", order: "SO-1002", address: "77 Kentwood Lane", postal: "92036", window: "AM", contact: "", phone: "", note: "" },
  { n: 3, customer: "Valley Municipal Works", order: "SO-1003", address: "1500 Farmers Road", postal: "92036", window: "PM", contact: "Yard lead (demo)", phone: "619-555-0103", note: "Call ahead. Forklift on site." },
];

let world;
let scenario;
let log;
let photoSeq;

// ---- the sync surface's books (see the sync block by the server below) ----
const SYNC_SCOPES = ["runs", "stops", "pod", "attachments"];
const PULL_ONLY = new Set(["attachments"]);
const WRITE_SCOPES = new Set(["stops", "pod"]);
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const syncSeq = { runs: 0, stops: 0, pod: 0, attachments: 0 };
const syncRowSeq = { runs: new Map(), stops: new Map(), pod: new Map(), attachments: new Map() };
const podRows = new Map();
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
  const day = houseDay();
  const created = new Date(nowMs()).toISOString();
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
    city: "Julian",
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
  appliedKeys.clear();
  podRows.clear();
  // The seq books are NOT reset: a server's seq never walks backwards, and a
  // device that kept its store across a stub reset must see the fresh world
  // as changes, not as rows it already holds.
  syncTouch("runs", world.run.id);
  for (const stop of world.stops) syncTouch("stops", stop.id);
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
    // An optional body {"now": <epoch ms>} pins the stub's clock to that instant (it then runs on).
    const raw = (await readBody(req)).toString("utf8");
    const asked = raw === "" ? {} : JSON.parse(raw);
    clockOffsetMs = typeof asked.now === "number" ? asked.now - Date.now() : 0;
    reset();
    return json(res, { ok: true });
  }
  if (req.method === "POST" && path === "/_stub/scenario") {
    const body = JSON.parse((await readBody(req)).toString("utf8"));
    scenario = String(body.name ?? "");
    // A cancelled stop is something only dispatch records; the world starts with one.
    if (scenario === "has-cancelled") world.stops[2].status = "CANCELLED";
    // A run the dispatcher never sent: the run's own status carries it, so
    // every door (REST and sync) refuses the same move with the same words.
    if (scenario === "run-planned") world.run.status = "PLANNED";
    return json(res, { ok: true, scenario });
  }
  // Dispatch moves a stop out from under the device: the status moves and the
  // row's seq moves with it, the server wins story the exit test asks for.
  if (req.method === "POST" && path === "/_stub/move-stop") {
    const body = JSON.parse((await readBody(req)).toString("utf8"));
    const stop = findStop(String(body.stop_id ?? ""));
    if (!stop) return notFound(res);
    stop.status = String(body.status ?? "CANCELLED");
    syncTouch("stops", stop.id);
    return json(res, { ok: true, seq: syncSeqOf("stops", stop.id) });
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
  if (path.startsWith("/api/v1/sync/")) {
    const authorization0 = req.headers.authorization ?? "";
    const raw0 = await readBody(req);
    let body0 = null;
    const ct0 = String(req.headers["content-type"] ?? "").split(";")[0].trim();
    if (ct0 === "application/json" && raw0.length > 0) {
      try {
        body0 = JSON.parse(raw0.toString("utf8"));
      } catch {
        return refuse(res, 400, "bad request");
      }
    }
    log.push({ method: req.method, path, query: Object.fromEntries(url.searchParams), bearer: authorization0.startsWith("Bearer "), org: req.headers["x-org"] ?? "", contentType: ct0, body: body0 });
    if (!authorization0.startsWith("Bearer ")) return refuse(res, 401, "unauthenticated");
    return handleSync(req, res, url, body0);
  }
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
  if (to === "EN_ROUTE" && world.run.status !== "DISPATCHED") {
    return refuse(res, 409, "the run has not been dispatched");
  }
  const reason = String(input.reason ?? "").trim();
  if (to === "FAILED" && reason === "") return refuse(res, 400, "a failed stop needs a reason");
  const now = new Date(nowMs()).toISOString();
  stop.status = to;
  if (to === "LOADED") stop.loaded_at = now;
  if (to === "EN_ROUTE") stop.en_route_at = now;
  if (to === "FAILED") stop.failure_reason = reason;
  syncTouch("stops", stop.id);
  return json(res, stop);
}

function capturePod(res, stop, input) {
  if (stop.status !== "EN_ROUTE") return refuse(res, 409, "illegal status transition");
  const name = String(input.received_by ?? "").trim();
  if (name === "") return refuse(res, 400, "type the full name of the person receiving this delivery");
  // The core's rule (CapturePOD checkTypedName): at most 200 bytes, no C0 or DEL, no bidi controls.
  if (Buffer.byteLength(name, "utf8") > 200) return refuse(res, 400, "that name is too long, type the full name in at most 200 bytes");
  if (/[\u0000-\u001f\u007f\u202a-\u202e\u2066-\u2069]/.test(name)) return refuse(res, 400, "the name cannot hold control characters, type the full name as plain text");
  if (input.affirmed !== true) return refuse(res, 400, "the recipient must confirm the delivery before it can be signed");
  const now = new Date(nowMs()).toISOString();
  stop.status = "DELIVERED";
  stop.pod_signature_ref = `sig-${stop.stop_no}`;
  stop.pod_received_by = name;
  stop.pod_at = now;
  stop.delivered_at = now;
  // The attestation has no sync door in the core: the pod feed carries the
  // photo records only, so the proof moves the stop and nothing else.
  syncTouch("stops", stop.id);
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
    captured_at: new Date(nowMs()).toISOString(),
  };
  world.photos.set(stop.id, [...(world.photos.get(stop.id) ?? []), meta]);
  stop.photo_count += 1;
  syncTouch("stops", stop.id);
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
  world.run.completed_at = new Date(nowMs()).toISOString();
  syncTouch("runs", world.run.id);
  return json(res, world.run);
}

// ---- the sync surface ----------------------------------------------------
//
// The demo double of the core's sync surface: the same pull page shape and
// the same push order of decisions, per operation, in push order. The pull
// projects the same world the REST routes serve, no money anywhere (the
// poisoned scenario never reaches these rows), and a proof of delivery moves
// its stop the way the REST route does, so a device's pulled rows and the
// day board never disagree.

function projectRunRow() {
  const run = { ...world.run };
  delete run.dispatched_at;
  return { id: run.id, seq: syncSeqOf("runs", run.id), data: run };
}

function projectStopRow(stop) {
  const data = { ...stop };
  delete data.branch_id;
  data.lines = LINES[stop.stop_no] ?? [];
  return { id: stop.id, seq: syncSeqOf("stops", stop.id), data };
}

function projectPodRow(pod) {
  return { id: pod.id, seq: syncSeqOf("pod", pod.id), data: { ...pod } };
}

function handleSyncPull(res, scope, url) {
  const since = Number(url.searchParams.get("since") ?? "0");
  const limit = Math.max(1, Math.min(200, Number(url.searchParams.get("limit") ?? "200")));
  let rows = [];
  const empty = scenario === "empty" || scenario === "not-driver";
  if (!empty && scope === "runs") rows = [projectRunRow()];
  else if (!empty && scope === "stops") rows = stopsInOrder().map(projectStopRow);
  else if (scope === "pod") rows = [...podRows.values()].map(projectPodRow);
  // attachments answers empty pages in the demo: the photos ride their own
  // door this slice, and the queue to Storage is item 6's other half.
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

function syncApplyStopUpdate(stop, op) {
  const fields = op.payload ?? {};
  const declared = Object.keys(fields).filter((k) => k !== "id");
  // The core's stopWriteFields (core/internal/dispatch/syncscope_stops.go).
  const allowed = new Set(["status", "failure_reason"]);
  for (const key of declared) {
    if (!allowed.has(key)) return syncRejected(op, `dispatch: field "${key}" is not writable through sync`);
  }
  // The engine's order: shape, dedup, load, STALE, then the write. A stale
  // write never reaches the module's own rules: the policy answers it.
  const rowSeq = syncSeqOf("stops", stop.id);
  if (op.base_seq !== undefined && rowSeq > op.base_seq) {
    return {
      client_id: op.client_id,
      status: "conflict",
      conflict: { client_id: op.client_id, policy: "server_wins", server_state: projectStopRow(stop).data, applied: false },
    };
  }
  const to = String(fields.status ?? "").trim().toUpperCase();
  if (to === "DELIVERED") return syncRejected(op, "a delivery must carry a proof-of-delivery attestation");
  if (!(TRANSITIONS[stop.status] ?? []).includes(to)) return syncRejected(op, "illegal status transition");
  if (to === "EN_ROUTE" && world.run.status !== "DISPATCHED") return syncRejected(op, "the run has not been dispatched");
  if (to === "FAILED" && String(fields.failure_reason ?? "").trim() === "") return syncRejected(op, "a failed stop needs a reason");
  const now = new Date(nowMs()).toISOString();
  stop.status = to;
  if (to === "LOADED") stop.loaded_at = now;
  if (to === "EN_ROUTE") stop.en_route_at = now;
  if (to === "FAILED") stop.failure_reason = String(fields.failure_reason ?? "").trim();
  syncTouch("stops", stop.id);
  return { client_id: op.client_id, status: "applied", seq: syncSeqOf("stops", stop.id) };
}

// The core's pod scope is the photo record (core/internal/dispatch/syncscope_pod.go
// podCreateFields): the stop it proves, the content type, the bytes base64 and
// a caption. The core now also has the typed name attestation form on this
// scope (the 6c client item builds its stub side); it rides the REST proof
// here until then, so a pod create here never delivers a stop.
const POD_CREATE_FIELDS = new Set(["id", "stop_id", "content_type", "image_bytes_b64", "caption"]);

function syncApplyPodCreate(op) {
  const input = op.payload ?? {};
  for (const key of Object.keys(input)) {
    if (!POD_CREATE_FIELDS.has(key)) return syncRejected(op, `dispatch: field "${key}" is not writable through sync`);
  }
  const stop = findStop(String(input.stop_id ?? ""));
  if (!stop) return syncRejected(op, "stop not found");
  const id = typeof input.id === "string" && UUID_RE.test(input.id) ? input.id : `7b000000-0000-4000-9000-${String(stop.stop_no).padStart(12, "0")}`;
  const now = new Date(nowMs()).toISOString();
  podRows.set(id, { id, stop_id: stop.id, content_type: String(input.content_type ?? ""), caption: String(input.caption ?? ""), captured_at: now });
  syncTouch("pod", id);
  return { client_id: op.client_id, status: "applied", seq: syncSeqOf("pod", id) };
}

function handleSyncPush(res, scope, body) {
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
    if (scope === "stops") {
      const stop = findStop(String(payload.id ?? ""));
      if (!stop) result = syncRejected(op, "row not found");
      else result = op.op === "update" ? syncApplyStopUpdate(stop, op) : syncRejected(op, "row not found");
    } else if (scope === "pod") {
      result = op.op === "create" ? syncApplyPodCreate(op) : syncRejected(op, "row not found");
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
  if (req.method === "GET") return handleSyncPull(res, scope, url);
  if (PULL_ONLY.has(scope)) return refuse(res, 400, `sync: scope ${scope} is pull-only`);
  if (!WRITE_SCOPES.has(scope)) return refuse(res, 501, `sync: scope ${scope} is not wired yet`);
  // The body is already read and parsed by the caller; a second read of an
  // ended stream would never answer.
  return handleSyncPush(res, scope, body);
}

const server = createServer((req, res) => {
  handle(req, res).catch(() => refuse(res, 500, "internal error"));
});
server.listen(port, () => {
  console.log(`drive-stub listening on ${port}`);
});
