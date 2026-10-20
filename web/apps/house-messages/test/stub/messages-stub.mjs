// House Messages local stub: the demo way in and the messaging routes the app calls, over one
// in-memory world. A development and test double only; the real routes are the core's
// (core/internal/messaging), and test/stub-contract.test.mjs holds every answer here to the wire
// descriptors and every refusal sentence to the core's source.
//
// The world is Gable Landscape Supply's Point Loma yard, dated from the day the stub starts: three
// personas (Inside Sales, Yard manager, Payables and Receivables) who each see only the rooms they
// are members of, a branch room, a yard room, a deliveries topic, a direct message, the house queue
// with two waiting customers, the escalations room with one forced acknowledgement, and a payments
// room only the Payables persona holds. People are role labels; customers and amounts are invented.
//
// The stub keeps the core's rules where the app leans on them: a non member gets 403 "not a channel
// member" (404 when the channel is gone), an empty body is a 400, a body over 8 KB is CUT OFF at a
// rune boundary and not refused, an escalation channel cannot be muted (409), a second claim of a
// house conversation is a 409, a re-ack never double counts, a superseded forced ack stops being
// returned. Streams: POST /stream-ticket mints a single use ticket and GET /stream?ticket= serves
// server sent events for the ticket's user.
//
// Controls (the dev server proxies /_stub to this stub): POST /_stub/reset, /_stub/drop-streams
// (close every open stream, as a network drop), /_stub/tickets (body {enabled:false} makes the
// ticket mint answer 501), /_stub/as/{flow} (a message posted by another persona, so the app can
// show the arrival), /_stub/offline (body {on:true} makes every route answer 503 until cleared),
// /_stub/page-shape (body {shape:"items"|"messages"}), GET /_stub/state.
import { createServer } from "node:http";

const port = Number(process.env.MSG_STUB_PORT ?? 4989);
const MAX_BODY = 8 * 1024;

const FLOWS = [
  { flow: "sales", role_code: "inside_sales", label: "Sales" },
  { flow: "yard-manager", role_code: "yard_manager", label: "Yard manager" },
  { flow: "payables-receivables", role_code: "payables_receivables", label: "Payables and Receivables" },
];

const USERS = {
  sales: {
    user_id: "a0000000-0000-4000-9000-000000000001",
    email: "sales@hh-demo.invalid",
    display_name: "Inside Sales (demo)",
    role_code: "inside_sales",
    role_name: "Inside Sales",
    tier: 3,
    scope: "BRANCH",
  },
  "yard-manager": {
    user_id: "a0000000-0000-4000-9000-000000000002",
    email: "yard.manager@hh-demo.invalid",
    display_name: "Yard manager (demo)",
    role_code: "yard_manager",
    role_name: "Yard Manager",
    tier: 2,
    scope: "BRANCH",
  },
  "payables-receivables": {
    user_id: "a0000000-0000-4000-9000-000000000003",
    email: "payables@hh-demo.invalid",
    display_name: "Priya Shah",
    role_code: "payables_receivables",
    role_name: "Dispatch",
    tier: 3,
    scope: "BRANCH",
  },
  admin: {
    user_id: "a0000000-0000-4000-9000-000000000004",
    email: "admin@hh-demo.invalid",
    display_name: "General Manager (demo)",
    role_code: "general_manager",
    role_name: "General Manager",
    tier: 1,
    scope: "COMPANY",
  },
  "elena-ruiz": {
    user_id: "a0000000-0000-4000-9000-000000000005",
    email: "elena@hh-demo.invalid",
    display_name: "Elena Ruiz",
    role_code: "driver",
    role_name: "Driver",
    tier: 3,
    scope: "BRANCH",
  },
  "tom-alvarez": {
    user_id: "a0000000-0000-4000-9000-000000000006",
    email: "tom@hh-demo.invalid",
    display_name: "Tom Alvarez",
    role_code: "counter_sales",
    role_name: "Counter Sales",
    tier: 3,
    scope: "BRANCH",
  },
  "marcus-webb": {
    user_id: "a0000000-0000-4000-9000-000000000007",
    email: "marcus@hh-demo.invalid",
    display_name: "Marcus Webb",
    role_code: "yard_staff",
    role_name: "Yard staff",
    tier: 3,
    scope: "BRANCH",
  },
};
const BY_ID = Object.fromEntries(Object.entries(USERS).map(([flow, u]) => [u.user_id, { flow, ...u }]));

const ROSTER = [
  { user_id: "a0000000-0000-4000-9000-000000000001", display_name: "Inside Sales (demo)", role_name: "Inside Sales", branch_name: "Point Loma" },
  { user_id: "a0000000-0000-4000-9000-000000000002", display_name: "Yard manager (demo)", role_name: "Yard Manager", branch_name: "Point Loma" },
  { user_id: "a0000000-0000-4000-9000-000000000003", display_name: "Priya Shah", role_name: "Dispatch", branch_name: "Point Loma" },
  { user_id: "a0000000-0000-4000-9000-000000000004", display_name: "General Manager (demo)", role_name: "General Manager", branch_name: "Point Loma" },
  { user_id: "a0000000-0000-4000-9000-000000000005", display_name: "Elena Ruiz", role_name: "Driver", branch_name: "Point Loma" },
  { user_id: "a0000000-0000-4000-9000-000000000006", display_name: "Tom Alvarez", role_name: "Counter Sales", branch_name: "Point Loma" },
  { user_id: "a0000000-0000-4000-9000-000000000007", display_name: "Marcus Webb", role_name: "Yard staff", branch_name: "Point Loma" },
];

function mintToken(sub) {
  const part = (value) => Buffer.from(JSON.stringify(value), "utf8").toString("base64url");
  const issuedAtSeconds = Math.floor(Date.now() / 1000);
  const payload = { iss: "hh-demo-stub", sub, aud: "staff", iat: issuedAtSeconds, exp: issuedAtSeconds + 3600 };
  return `${part({ alg: "ES256", typ: "JWT" })}.${part(payload)}.c3R1Yi1zaWduYXR1cmUtYnl0ZXM`;
}

function userOfToken(header) {
  const token = String(header ?? "").replace(/^Bearer /, "");
  const body = token.split(".")[1];
  if (!body) return null;
  try {
    const sub = JSON.parse(Buffer.from(body, "base64url").toString("utf8")).sub;
    return BY_ID[sub] ?? null;
  } catch {
    return null;
  }
}

// ---- the world ----------------------------------------------------------------------------------

const BRANCH = "5d1b3f6e-0000-4000-9000-000000000001";
const CH = {
  branch: "c0000000-0000-4000-9000-000000000001",
  yard: "c0000000-0000-4000-9000-000000000002",
  deliveries: "c0000000-0000-4000-9000-000000000003",
  esc: "c0000000-0000-4000-9000-000000000004",
  house: "c0000000-0000-4000-9000-000000000005",
  dm: "c0000000-0000-4000-9000-000000000006",
  pay: "c0000000-0000-4000-9000-000000000007",
};

// The world's clock starts at the last reset, so "40 minutes ago" is still 40 minutes ago when a
// test reads it a few seconds after resetting, however long the stub has been up.
let T0 = Date.now();
const at = (minutesAgo) => new Date(T0 - minutesAgo * 60_000).toISOString();

let world;
let seq;
let streams = [];
let tickets = new Map();
let ticketsOn = true;
let offline = false;
let pageShape = "messages";

function id(prefix) {
  seq += 1;
  return `${prefix}${String(seq).padStart(7, "0")}-0000-4000-9000-000000000000`.slice(0, 36);
}

function reset() {
  T0 = Date.now();
  seq = 0;
  streams.forEach((s) => s.res.end());
  streams = [];
  tickets = new Map();
  ticketsOn = true;
  offline = false;
  pageShape = "messages";
  const S = USERS.sales.user_id;
  const Y = USERS["yard-manager"].user_id;
  const P = USERS["payables-receivables"].user_id;
  const A = USERS.admin.user_id;
  world = {
    channels: [
      { id: CH.branch, kind: "BRANCH", branch_id: BRANCH, slug: "branch-point-loma", name: "Point Loma", topic: "Everyone at the yard", tier: "STANDARD", visibility: "PUBLIC", is_archived: false, created_at: at(9000), members: [S, Y, P] },
      { id: CH.yard, kind: "TEAM", slug: "yard-point-loma", name: "Yard floor", topic: "Picks, staging and the gate", tier: "STANDARD", visibility: "PUBLIC", is_archived: false, created_at: at(9000), members: [S, Y, A] },
      { id: CH.deliveries, kind: "TOPIC", slug: "deliveries", name: "Deliveries", topic: "Truck 01 to Truck 04", tier: "STANDARD", visibility: "PUBLIC", is_archived: false, created_at: at(9000), members: [S, Y, P, A] },
      { id: CH.esc, kind: "TOPIC", slug: "escalations", name: "Escalations", topic: "Alerts that must be acknowledged", tier: "ESCALATION", visibility: "PUBLIC", is_archived: false, created_at: at(9000), members: [S, Y, P] },
      { id: CH.house, kind: "TOPIC", branch_id: BRANCH, slug: "house-point-loma", name: "House queue", topic: "Customers with no assigned rep", tier: "STANDARD", visibility: "PUBLIC", is_archived: false, created_at: at(9000), members: [S, Y, A] },
      { id: CH.dm, kind: "DM", slug: "dm-sales-yard", name: "Direct message", topic: "", tier: "STANDARD", visibility: "PRIVATE", is_archived: false, created_at: at(9000), members: [S, Y], dmNames: { [S]: "Yard manager (demo)", [Y]: "Inside Sales (demo)" } },
      { id: CH.pay, kind: "TOPIC", branch_id: BRANCH, slug: "payments-point-loma", name: "Payments Point Loma", topic: "The day's payments", tier: "STANDARD", visibility: "PUBLIC", is_archived: false, created_at: at(9000), members: [P], defaultMode: "MUTED" },
    ],
    messages: [],
    lastRead: {}, // `${user}|${channel}` -> iso
    acks: [], // { message_id, user_id, method, acked_at }
    prefs: {}, // `${user}|${channel}` -> prefs
    paymentRules: {}, // user -> { RULE: bool }
    houseQueue: [
      { id: "d0000000-0000-4000-9000-000000000001", customer_id: "e0000000-0000-4000-9000-000000000001", customer_name: "Ridgeview Outdoor Living", branch_id: BRANCH, channel_id: CH.house, first_at: at(95), last_at: at(12), message_count: 3 },
      { id: "d0000000-0000-4000-9000-000000000002", customer_id: "e0000000-0000-4000-9000-000000000002", customer_name: "Fieldstone Pools and Patios", branch_id: BRANCH, channel_id: CH.house, first_at: at(40), last_at: at(40), message_count: 1 },
      { id: "d0000000-0000-4000-9000-000000000003", customer_id: "e0000000-0000-4000-9000-000000000003", customer_name: "Summit Ridge Builders", branch_id: BRANCH, channel_id: CH.house, first_at: at(300), last_at: at(280), message_count: 2, claimed_by: Y, claimed_by_name: "Yard manager (demo)", claimed_at: at(270) },
    ],
    reactions: [],
    saved: [],
    membershipRules: [
      { channel_id: CH.branch, channel_name: "Point Loma", joins_automatically: "Role All staff at Point Loma", leaves_automatically: "When status or role changes", visibility: "PUBLIC" },
      { channel_id: CH.yard, channel_name: "Yard floor", joins_automatically: "Role Yard staff, Driver at Point Loma", leaves_automatically: "When status or role changes", visibility: "PUBLIC" },
      { channel_id: CH.deliveries, channel_name: "Deliveries", joins_automatically: "Role Driver at Point Loma", leaves_automatically: "When status or role changes", visibility: "PUBLIC" },
      { channel_id: CH.esc, channel_name: "Escalations", joins_automatically: "Role Manager, Admin at Point Loma", leaves_automatically: "When status or role changes", visibility: "PRIVATE" },
      { channel_id: CH.house, channel_name: "House queue", joins_automatically: "Role Inside Sales, Yard Manager at Point Loma", leaves_automatically: "When status or role changes", visibility: "PUBLIC" },
    ],
    membersByHand: [
      { channel_id: CH.yard, user_id: "a0000000-0000-4000-9000-000000000005", display_name: "Elena Ruiz", role_name: "Driver", added_by_name: "General Manager (demo)", added_reason: "Temporary cover for morning picks", joined_at: at(300) },
    ],
    eventClasses: [
      { topic: "orders_released_and_blocked", name: "Orders released and blocked", description: "Credit releases and order holds" },
      { topic: "stock_received", name: "Stock received", description: "PO receipts and staging notifications" },
      { topic: "credit_holds_and_credit_owed", name: "Credit holds and credit owed", description: "Overdue balances and holds" },
      { topic: "deliveries_scheduled", name: "Deliveries scheduled", description: "Truck dispatch and route updates" },
      { topic: "payments", name: "Payments", description: "Customer payments received" },
      { topic: "approvals_waiting_on_me", name: "Approvals waiting on me", description: "Price and credit overrides" },
    ],
    repAssignments: [
      { id: "rep-001", customer_id: "e0000000-0000-4000-9000-000000000001", customer_name: "Ridgeview Outdoor Living", rep_id: S, rep_name: "Inside Sales (demo)", assigned_at: at(500) },
      { id: "rep-002", customer_id: "e0000000-0000-4000-9000-000000000002", customer_name: "Fieldstone Pools and Patios", rep_id: Y, rep_name: "Yard manager (demo)", assigned_at: at(400) },
    ],
    attachments: [
      { id: "att-001", channel_id: CH.yard, filename: "staging-bay-north.jpg", url: "/img/staging-bay.jpg", content_type: "image/jpeg", size_bytes: 840 * 1024, author_id: Y, author_name: "Yard manager (demo)", created_at: at(120), caption: "North bay clear for the noon delivery" },
      { id: "att-002", channel_id: CH.yard, filename: "pallets-damage.jpg", url: "/img/pallets.jpg", content_type: "image/jpeg", size_bytes: 1200 * 1024, author_id: S, author_name: "Inside Sales (demo)", created_at: at(80), caption: "Shortage on Copperline coping" },
      { id: "att-003", channel_id: CH.yard, filename: "Daily yard checklist.pdf", url: "/docs/checklist.pdf", content_type: "application/pdf", size_bytes: 142 * 1024, author_id: Y, author_name: "Yard manager (demo)", created_at: at(400) },
      { id: "att-004", channel_id: CH.yard, filename: "Santa Fe delivery ticket.pdf", url: "/docs/ticket.pdf", content_type: "application/pdf", size_bytes: 2 * 1024 * 1024, author_id: S, author_name: "Inside Sales (demo)", created_at: at(210) },
      { id: "att-005", channel_id: CH.branch, filename: "gate-whiteboard.jpg", url: "/img/whiteboard.jpg", content_type: "image/jpeg", size_bytes: 512 * 1024, author_id: S, author_name: "Inside Sales (demo)", created_at: at(90), caption: "Whiteboard notes" },
      { id: "att-006", channel_id: CH.branch, filename: "Branch safety policy.pdf", url: "/docs/policy.pdf", content_type: "application/pdf", size_bytes: 256 * 1024, author_id: A, author_name: "General Manager (demo)", created_at: at(800) },
    ],
    quietHours: {},
    userEventClasses: {},
  };
  const text = (channel, author, body, minutesAgo, extra = {}) => {
    const m = { id: id("b"), channel_id: channel, author_id: author, kind: "TEXT", body, created_at: at(minutesAgo), ...extra };
    world.messages.push(m);
    return m;
  };
  text(CH.branch, Y, "Yard open at 6:00 AM. Truck 02 is out for the La Jolla pavers.", 600);
  text(CH.branch, S, "Counter is slammed with will-call. Two pallets of coping to stage by noon.", 420);
  const root = text(CH.yard, Y, "Pallet of Copperline coping is short on the Santa Fe order. Count again?", 200);
  text(CH.yard, S, "Counted twice. 13 on the pallet, the ticket says 14.", 190, { thread_root_id: root.id });
  text(CH.yard, Y, "Thanks. I will note the shortage and call the vendor.", 185, { thread_root_id: root.id });
  text(CH.yard, S, "Gate code for the second yard is on the whiteboard.", 90);
  text(CH.deliveries, Y, "Truck 04 to Julian leaves at 10:40. Load the polymeric sand last.", 150);
  text(CH.dm, Y, "Can you look at the Ridgeview quote before lunch?", 60);
  text(CH.dm, S, "On it.", 55);
  text(CH.dm, Y, "Hey @Inside Sales check the pallet count please.", 40);
  world.saved.push({ user_id: S, message_id: root.id, saved_at: at(100) });
  // A payment event that carries a figure the core sent, with the rules that routed it.
  world.messages.push({
    id: id("b"), channel_id: CH.pay, kind: "EVENT", body: "Payment received: $1,240.00 on order SO-1042 (ACH).", created_at: at(30),
    ref: { topic: "payment.received", entity: "order:SO-1042", summary: "Payment received", amount_cents: 124000, rules: ["OWN_ORDER"] },
  });
  world.messages.push({
    id: id("b"), channel_id: CH.pay, kind: "EVENT", body: "Day total: 4 payments, $6,810.50.", created_at: at(20),
    ref: { topic: "payment.rollup", count: 4, amount_cents: 681050, rollup_day: new Date(T0).toISOString().slice(0, 10) },
  });
  // The forced acknowledgement lives on the HOME message, in a room the credit hold belongs to. The
  // escalation room holds a mirror that does not ask anyone to acknowledge (the core's copies never set
  // requires_ack: acking is done once, on the home message).
  world.messages.push({
    id: "b9000000-0000-4000-9000-000000000001", channel_id: CH.branch, kind: "EVENT", body: "Order SO-1042 is on credit hold. Do not release it to the yard.", created_at: at(25), requires_ack: true, priority: "HIGH", state_key: "order:SO-1042",
    ref: { topic: "order.credit_hold", entity: "order:SO-1042", summary: "Credit hold" },
  });
  world.messages.push({
    id: "b9000000-0000-4000-9000-000000000002", channel_id: CH.esc, kind: "EVENT", body: "Order SO-1042 is on credit hold. Do not release it to the yard.", created_at: at(25), priority: "HIGH", state_key: "order:SO-1042",
    ref: { topic: "order.credit_hold", entity: "order:SO-1042", summary: "Credit hold" },
  });
  // House conversation messages, so the room reads.
  world.messages.push({ id: id("b"), channel_id: CH.house, kind: "EVENT", body: "Ridgeview Outdoor Living wrote in: asking for a delivery window on Friday.", created_at: at(95), ref: { topic: "msg.event.customer.inbound", entity: "customer:e0000000-0000-4000-9000-000000000001" } });
}
reset();

// ---- helpers ------------------------------------------------------------------------------------

function reactionListFor(msgId, uid) {
  const map = new Map();
  for (const r of (world.reactions ?? []).filter((x) => x.message_id === msgId)) {
    if (!map.has(r.emoji)) map.set(r.emoji, { emoji: r.emoji, count: 0, mine: false, users: [] });
    const item = map.get(r.emoji);
    item.count += 1;
    item.users.push(r.user_id);
    if (r.user_id === uid) item.mine = true;
  }
  return [...map.values()];
}

const channelOf = (cid) => world.channels.find((c) => c.id === cid);
const isMember = (ch, uid) => ch.members.includes(uid);
const nameOf = (uid) => BY_ID[uid]?.display_name ?? "";

function view(m) {
  const { requires_ack: _a, priority: _p, state_key: _s, ...rest } = m;
  const out = { ...rest, author_name: m.author_id ? nameOf(m.author_id) : "", reply_count: world.messages.filter((r) => r.thread_root_id === m.id).length };
  if (m.author_id === undefined) delete out.author_id;
  return out;
}

function unreadFor(uid, ch) {
  const since = world.lastRead[`${uid}|${ch.id}`] ?? "";
  return world.messages.filter((m) => m.channel_id === ch.id && m.author_id !== uid && m.created_at > since);
}

function prefsFor(uid, ch) {
  const row = world.prefs[`${uid}|${ch.id}`];
  if (row) return { ...row, inherited: false };
  const mode = ch.defaultMode ?? "ALL";
  return {
    channel_id: ch.id, user_id: uid, desktop_enabled: true, escalation_override: true, muted: mode === "MUTED", mode,
    inherited: ch.defaultMode !== undefined ? true : true,
    updated_at: new Date(T0).toISOString(),
  };
}

function channelView(uid, ch) {
  const unread = unreadFor(uid, ch);
  const mode = prefsFor(uid, ch).mode;
  const count = mode === "MUTED" ? 0 : unread.length;
  const latest = world.messages.filter((m) => m.channel_id === ch.id).map((m) => m.created_at).sort().at(-1) ?? null;
  const dm = ch.kind === "DM" ? { name: ch.dmNames[uid] ?? ch.name } : {};
  const { members: _m, dmNames: _d, defaultMode: _dm, ...rest } = ch;
  return { ...rest, ...dm, unread_count: count, mentioned: false, latest_at: latest };
}

function unackedFor(uid) {
  return world.messages
    .filter((m) => m.requires_ack && !m.superseded_by)
    .filter((m) => isMember(channelOf(m.channel_id), uid))
    .filter((m) => !world.acks.some((a) => a.message_id === m.id && a.user_id === uid))
    .sort((a, b) => (a.created_at < b.created_at ? 1 : -1))
    .map((m) => ({ ...view(m), requires_ack: true, priority: m.priority, state_key: m.state_key }));
}

function truncate(body) {
  const bytes = Buffer.from(body, "utf8");
  if (bytes.length <= MAX_BODY) return body;
  let cut = MAX_BODY;
  while (cut > 0 && (bytes[cut] & 0xc0) === 0x80) cut -= 1;
  return bytes.subarray(0, cut).toString("utf8");
}

function emit(channelId, event, data) {
  const ch = channelOf(channelId);
  for (const s of streams) if (isMember(ch, s.uid)) s.res.write(`event: ${event}\ndata: ${JSON.stringify(data)}\n\n`);
}

function postMessage(ch, uid, body, rootId) {
  const m = { id: id("b"), channel_id: ch.id, author_id: uid, kind: "TEXT", body, created_at: new Date().toISOString() };
  if (rootId) m.thread_root_id = rootId;
  world.messages.push(m);
  emit(ch.id, "message", view(m));
  emit(ch.id, "unread", { channel_id: ch.id });
  return m;
}

// ---- http ---------------------------------------------------------------------------------------

function json(res, body, status = 200) {
  res.writeHead(status, { "Content-Type": "application/json" });
  res.end(JSON.stringify(body));
}
const fail = (res, status, error) => json(res, { error }, status);

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

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

async function route(req, res, url) {
  const { pathname: path } = url;
  const method = req.method ?? "GET";

  if (path === "/healthz") return json(res, { ok: true });

  // ---- controls
  if (path.startsWith("/_stub/")) {
    const body = method === "POST" ? await readBody(req) : {};
    if (path === "/_stub/reset") {
      reset();
      return json(res, { ok: true });
    }
    if (path === "/_stub/drop-streams") {
      const n = streams.length;
      streams.forEach((s) => s.res.end());
      streams = [];
      return json(res, { dropped: n });
    }
    if (path === "/_stub/tickets") {
      ticketsOn = body?.enabled !== false;
      return json(res, { enabled: ticketsOn });
    }
    if (path === "/_stub/offline") {
      offline = body?.on === true;
      return json(res, { offline });
    }
    if (path === "/_stub/page-shape") {
      pageShape = body?.shape === "items" ? "items" : "messages";
      return json(res, { shape: pageShape });
    }
    if (path === "/_stub/supersede") {
      const target = world.messages.find((x) => x.id === body?.id);
      if (!target) return fail(res, 404, "not found");
      target.superseded_by = id("b");
      target.superseded_at = new Date().toISOString();
      return json(res, { ok: true });
    }
    if (path === "/_stub/state") return json(res, { streams: streams.length, acks: world.acks.length, messages: world.messages.length });
    const as = /^\/_stub\/as\/([a-z-]+)$/.exec(path);
    if (as && method === "POST") {
      const user = USERS[as[1]];
      const ch = channelOf(body?.channel_id);
      if (!user || !ch) return fail(res, 404, "not found");
      return json(res, view(postMessage(ch, user.user_id, String(body.body ?? ""), body.thread_root_id)), 201);
    }
    return fail(res, 404, "not found");
  }

  // ---- the demo way in
  // The public tenant config: the demo tenant's dealer name, which the Field frame's lockup shows.
  if (path === "/api/v1/tenant/config") return json(res, { dealer_name: "Gable Landscape Supply" });
  if (path === "/api/v1/demo/flows") return json(res, { flows: FLOWS });
  if (method === "POST" && path === "/api/v1/demo/session") {
    const body = await readBody(req);
    const account = body === null ? undefined : USERS[body.flow];
    if (!account) return fail(res, 404, "not found");
    return json(res, { token: mintToken(account.user_id), user: account, flow: body.flow });
  }

  if (offline) return fail(res, 503, "database unavailable");

  // ---- the stream: a ticket, not a Bearer
  if (method === "GET" && path === "/api/v1/messaging/stream") {
    const ticket = url.searchParams.get("ticket") ?? "";
    const uid = tickets.get(ticket);
    if (!ticketsOn) return fail(res, 501, "sse stream not enabled; poll /api/v1/messaging/unread");
    if (!uid) return fail(res, 401, "invalid or expired stream ticket");
    tickets.delete(ticket);
    res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache", Connection: "keep-alive" });
    res.write(": open\n\n");
    const entry = { uid, res };
    streams.push(entry);
    req.on("close", () => {
      streams = streams.filter((s) => s !== entry);
    });
    return;
  }

  // everything else is a staff route behind a bearer
  if (!String(req.headers.authorization ?? "").startsWith("Bearer ")) return fail(res, 401, "Missing or invalid credentials");
  const me = userOfToken(req.headers.authorization);
  if (!me) return fail(res, 401, "Missing or invalid credentials");
  const uid = me.user_id;
  if (!path.startsWith("/api/v1/messaging/")) return fail(res, 404, "not found");
  const rest = path.slice("/api/v1/messaging/".length);

  if (method === "POST" && rest === "stream-ticket") {
    if (!ticketsOn) return fail(res, 501, "stream tickets not enabled; use the Bearer stream");
    const ticket = `tk-${Math.random().toString(36).slice(2)}${Date.now().toString(36)}`;
    tickets.set(ticket, uid);
    return json(res, { ticket, expires_in_seconds: 60 });
  }

  if (method === "GET" && rest === "roster") {
    return json(res, { roster: ROSTER });
  }

  if (method === "POST" && rest === "dms") {
    const body = await readBody(req);
    if (body === null || !body.user_id) return fail(res, 400, "user_id required");
    const targetUid = body.user_id;
    const existing = world.channels.find((c) => c.kind === "DM" && isMember(c, uid) && isMember(c, targetUid));
    if (existing) return json(res, channelView(uid, existing), 200);
    const other = BY_ID[targetUid];
    const otherName = other?.display_name ?? "Colleague";
    const myName = nameOf(uid);
    const newCh = {
      id: id("c"),
      kind: "DM",
      slug: `dm-${uid.slice(0, 8)}-${targetUid.slice(0, 8)}`,
      name: otherName,
      topic: "",
      tier: "STANDARD",
      is_archived: false,
      created_at: new Date().toISOString(),
      members: [uid, targetUid],
      dmNames: { [uid]: otherName, [targetUid]: myName },
    };
    world.channels.push(newCh);
    return json(res, channelView(uid, newCh), 201);
  }

  if (method === "POST" && rest === "groups") {
    const body = await readBody(req);
    if (body === null) return fail(res, 400, "invalid_json");
    const rawIds = Array.isArray(body.member_ids) ? body.member_ids : [];
    const members = Array.from(new Set([uid, ...rawIds]));
    const rawName = String(body.name ?? "").trim();
    const groupName = rawName !== "" ? rawName : "Group direct message";
    const newCh = {
      id: id("c"),
      kind: "GROUP",
      slug: `group-${Date.now().toString(36)}`,
      name: groupName,
      topic: "",
      tier: "STANDARD",
      is_archived: false,
      created_at: new Date().toISOString(),
      members,
    };
    world.channels.push(newCh);
    if (rawIds.length > 0) {
      const addedNames = rawIds.map((id) => BY_ID[id]?.display_name ?? nameOf(id)).filter(Boolean);
      let sysText = "";
      if (addedNames.length === 1) {
        sysText = `You created the group ${groupName} and added ${addedNames[0]}.`;
      } else {
        sysText = `You created the group ${groupName} and added ${addedNames.slice(0, -1).join(", ")} and ${addedNames.at(-1)}.`;
      }
      world.messages.push({
        id: id("b"),
        channel_id: newCh.id,
        author_id: uid,
        kind: "EVENT",
        body: sysText,
        created_at: new Date().toISOString(),
      });
    }
    return json(res, channelView(uid, newCh), 201);
  }

  if (method === "POST" && rest === "channels") {
    const body = await readBody(req);
    if (body === null) return fail(res, 400, "invalid_json");
    const name = String(body.name ?? "").trim();
    if (name === "") return fail(res, 400, "channel name is required");
    const kind = body.kind ?? "TOPIC";
    const topic = String(body.topic ?? "").trim();
    const slug = String(body.slug ?? "").trim() || name.toLowerCase().replace(/[^a-z0-9]+/g, "-");
    const newCh = {
      id: id("c"),
      kind,
      name,
      topic,
      slug,
      tier: "STANDARD",
      is_archived: false,
      created_at: new Date().toISOString(),
      members: [uid],
      branch_id: body.branch_id ?? BRANCH,
    };
    world.channels.push(newCh);
    world.membershipRules.push({
      channel_id: newCh.id,
      channel_name: name,
      joins_automatically: "Role Yard staff at Point Loma",
      leaves_automatically: "When status or role changes",
      visibility: "PUBLIC",
    });
    return json(res, channelView(uid, newCh), 201);
  }

  if (method === "GET" && rest === "channels") {
    const mine = world.channels.filter((c) => isMember(c, uid) && !c.is_archived).map((c) => channelView(uid, c));
    mine.sort((a, b) => ((b.latest_at ?? b.created_at) > (a.latest_at ?? a.created_at) ? 1 : -1));
    return json(res, { channels: mine });
  }

  if (method === "GET" && rest === "unread") {
    const rows = world.channels
      .filter((c) => isMember(c, uid))
      .map((c) => channelView(uid, c))
      .filter((c) => c.unread_count > 0)
      .map((c) => ({ channel_id: c.id, unread_count: c.unread_count, mentioned: false, latest_at: c.latest_at }));
    return json(res, { total: rows.reduce((n, r) => n + r.unread_count, 0), channels: rows });
  }

  if (method === "GET" && rest === "unacked") return json(res, { messages: unackedFor(uid) });

  if (method === "GET" && rest === "mentions") {
    const myName = nameOf(uid).toLowerCase();
    const myRole = (BY_ID[uid]?.role_name ?? "").toLowerCase();
    const rows = world.messages
      .filter((m) => {
        const ch = channelOf(m.channel_id);
        if (!ch || !isMember(ch, uid)) return false;
        const b = m.body.toLowerCase();
        return b.includes("@" + myName) || (myRole !== "" && b.includes("@" + myRole)) || b.includes("@sales") || b.includes("@all");
      })
      .map((m) => ({
        message: view(m),
        channel_id: m.channel_id,
        channel_name: channelOf(m.channel_id)?.name ?? "",
      }));
    return json(res, { mentions: rows });
  }

  if (method === "GET" && rest === "saved") {
    const rows = (world.saved ?? [])
      .filter((s) => s.user_id === uid)
      .map((s) => {
        const m = world.messages.find((x) => x.id === s.message_id);
        if (!m) return null;
        return {
          message: view(m),
          channel_id: m.channel_id,
          channel_name: channelOf(m.channel_id)?.name ?? "",
          saved_at: s.saved_at,
        };
      })
      .filter(Boolean);
    return json(res, { saved: rows });
  }
  const saveAction = /^saved\/([^/]+)$/.exec(rest);
  if (saveAction) {
    const msgId = saveAction[1];
    if (method === "POST") {
      if (!world.saved.some((s) => s.user_id === uid && s.message_id === msgId)) {
        world.saved.push({ user_id: uid, message_id: msgId, saved_at: new Date().toISOString() });
      }
      return json(res, { status: "ok", saved: true });
    }
    if (method === "DELETE") {
      world.saved = world.saved.filter((s) => !(s.user_id === uid && s.message_id === msgId));
      return json(res, { status: "ok", saved: false });
    }
  }

  if (method === "GET" && rest.startsWith("search")) {
    const q = (url.searchParams.get("q") ?? "").toLowerCase().trim();
    if (q === "") return json(res, { query: "", messages: [], people: [], channels: [], files: [] });
    const matchedMessages = world.messages
      .filter((m) => {
        const ch = channelOf(m.channel_id);
        return ch && isMember(ch, uid) && m.body.toLowerCase().includes(q);
      })
      .map((m) => ({ message: view(m), channel_id: m.channel_id, channel_name: channelOf(m.channel_id)?.name ?? "" }));
    const matchedPeople = ROSTER.filter((p) => p.display_name.toLowerCase().includes(q) || p.role_name.toLowerCase().includes(q) || p.branch_name.toLowerCase().includes(q));
    const matchedChannels = world.channels.filter((c) => isMember(c, uid) && (c.name.toLowerCase().includes(q) || c.topic.toLowerCase().includes(q))).map((c) => channelView(uid, c));
    const matchedFiles = (world.attachments ?? []).filter((a) => {
      const ch = channelOf(a.channel_id);
      return ch && isMember(ch, uid) && (a.filename.toLowerCase().includes(q) || (a.caption ?? "").toLowerCase().includes(q));
    });
    return json(res, { query: q, messages: matchedMessages, people: matchedPeople, channels: matchedChannels, files: matchedFiles });
  }

  if (method === "GET" && rest === "directory") {
    const list = world.channels
      .filter((c) => c.kind !== "DM" && c.kind !== "GROUP")
      .map((c) => ({
        id: c.id,
        name: c.name,
        topic: c.topic,
        member_count: c.members.length,
        is_member: isMember(c, uid),
        membership_source: c.id === CH.branch ? "DERIVED" : isMember(c, uid) ? "EXPLICIT" : "NONE",
      }));
    return json(res, { channels: list });
  }

  if (rest === "notification-settings") {
    if (method === "GET") {
      const quiet = world.quietHours[uid] ?? { enabled: false, start_time: "20:00", end_time: "07:00", timezone: "America/Los_Angeles" };
      const events = world.userEventClasses[uid] ?? {
        orders_released_and_blocked: { in_app: true, push: true },
        stock_received: { in_app: true, push: false },
        credit_holds_and_credit_owed: { in_app: true, push: true },
        deliveries_scheduled: { in_app: true, push: false },
        payments: { in_app: true, push: false },
        approvals_waiting_on_me: { in_app: true, push: true },
      };
      return json(res, { quiet_hours: quiet, event_classes: events });
    }
    if (method === "PUT") {
      const body = await readBody(req);
      if (body?.quiet_hours) world.quietHours[uid] = body.quiet_hours;
      if (body?.event_classes) world.userEventClasses[uid] = body.event_classes;
      return json(res, {
        quiet_hours: world.quietHours[uid] ?? { enabled: false, start_time: "20:00", end_time: "07:00", timezone: "America/Los_Angeles" },
        event_classes: world.userEventClasses[uid] ?? {},
      });
    }
  }

  if (method === "GET" && rest === "membership-rules") {
    return json(res, { rules: world.membershipRules ?? [] });
  }
  if (method === "GET" && rest === "members-by-hand") {
    return json(res, { members: world.membersByHand ?? [] });
  }
  if (method === "GET" && rest === "event-classes") {
    return json(res, { classes: world.eventClasses ?? [] });
  }
  const putEventClass = /^event-classes\/([^/]+)$/.exec(rest);
  if (putEventClass && method === "PUT") {
    const body = await readBody(req);
    const item = world.eventClasses?.find((c) => c.topic === putEventClass[1]);
    if (item && body) Object.assign(item, body);
    return json(res, item ?? { topic: putEventClass[1] });
  }
  if (rest === "rep-assignments") {
    if (method === "GET") return json(res, { assignments: world.repAssignments ?? [] });
    if (method === "POST") {
      const body = await readBody(req);
      const row = { id: id("rep"), customer_id: body.customer_id ?? id("cust"), customer_name: body.customer_name ?? "Customer", rep_id: body.rep_id ?? uid, rep_name: nameOf(body.rep_id ?? uid), assigned_at: new Date().toISOString() };
      world.repAssignments.push(row);
      return json(res, row, 201);
    }
    if (method === "DELETE") {
      const body = await readBody(req);
      world.repAssignments = world.repAssignments.filter((a) => a.id !== body.id && a.customer_id !== body.customer_id);
      return json(res, { status: "ok" });
    }
  }

  if (rest === "payment-rules") {
    const rules = { ONLINE: false, CROSS_BRANCH: false, OWN_ORDER: false, ...(world.paymentRules[uid] ?? {}) };
    if (method === "GET") return json(res, { user_id: uid, rules });
  }
  const rule = /^payment-rules\/([A-Za-z_]+)$/.exec(rest);
  if (rule && method === "PUT") {
    const body = await readBody(req);
    if (body === null) return fail(res, 400, "invalid_json");
    if (!["ONLINE", "CROSS_BRANCH", "OWN_ORDER"].includes(rule[1])) return fail(res, 400, "rule must be one of ONLINE, CROSS_BRANCH, OWN_ORDER");
    world.paymentRules[uid] = { ...(world.paymentRules[uid] ?? {}), [rule[1]]: body.enabled === true };
    return json(res, { user_id: uid, rules: { ONLINE: false, CROSS_BRANCH: false, OWN_ORDER: false, ...world.paymentRules[uid] } });
  }

  const claim = /^house-conversations\/([^/]+)\/claim$/.exec(rest);
  if (claim && method === "POST") {
    if (!UUID.test(claim[1])) return fail(res, 404, "not found");
    const conv = world.houseQueue.find((c) => c.id === claim[1]);
    if (!conv) return fail(res, 404, "not found");
    const ch = channelOf(conv.channel_id);
    if (!isMember(ch, uid)) return fail(res, 403, "not a channel member");
    if (conv.claimed_by) return fail(res, 409, "conversation already claimed");
    conv.claimed_by = uid;
    conv.claimed_by_name = nameOf(uid);
    conv.claimed_at = new Date().toISOString();
    world.messages.push({
      id: id("b"), channel_id: ch.id, kind: "EVENT", body: `${nameOf(uid)} claimed the conversation with ${conv.customer_name}.`, created_at: new Date().toISOString(),
      ref: { topic: "msg.event.conversation.claimed", entity: `customer:${conv.customer_id}` },
    });
    emit(ch.id, "unread", { channel_id: ch.id });
    return json(res, conv);
  }

  // ---- channel routes
  const m = /^channels\/([^/]+)(?:\/(.*))?$/.exec(rest);
  if (!m) return fail(res, 404, "not found");
  const [, cid, tail = ""] = m;
  if (!UUID.test(cid)) return fail(res, 404, "not found");
  const ch = channelOf(cid);
  if (!ch) return fail(res, 404, "not found");

  if (method === "POST" && tail === "join") {
    if (!isMember(ch, uid)) ch.members.push(uid);
    return json(res, { status: "ok", is_member: true });
  }

  if (!isMember(ch, uid)) return fail(res, 403, "not a channel member");

  if (method === "POST" && tail === "leave") {
    if (ch.id === CH.branch) return fail(res, 409, "Joined by rule, you stay");
    ch.members = ch.members.filter((m) => m !== uid);
    return json(res, { status: "ok", is_member: false });
  }

  if (method === "PATCH" && tail === "") {
    const body = await readBody(req);
    if (body === null || !body.name) return fail(res, 400, "name required");
    ch.name = String(body.name).trim();
    world.messages.push({
      id: id("b"),
      channel_id: ch.id,
      author_id: uid,
      kind: "EVENT",
      body: `${nameOf(uid)} renamed the channel to ${ch.name}.`,
      created_at: new Date().toISOString(),
    });
    return json(res, channelView(uid, ch));
  }

  if (method === "POST" && tail === "members") {
    const body = await readBody(req);
    if (!body?.user_id) return fail(res, 400, "user_id required");
    if (!isMember(ch, body.user_id)) ch.members.push(body.user_id);
    return json(res, { channel_id: ch.id, user_id: body.user_id, role: "MEMBER", source: "EXPLICIT", joined_at: new Date().toISOString() });
  }
  const remMember = /^members\/([^/]+)$/.exec(tail);
  if (method === "DELETE" && remMember) {
    ch.members = ch.members.filter((m) => m !== remMember[1]);
    world.membersByHand = (world.membersByHand ?? []).filter((m) => !(m.channel_id === ch.id && m.user_id === remMember[1]));
    return json(res, { status: "ok" });
  }

  if (method === "GET" && tail === "members-by-hand") {
    return json(res, { members: (world.membersByHand ?? []).filter((m) => m.channel_id === ch.id) });
  }
  if (method === "POST" && tail === "members-by-hand") {
    const body = await readBody(req);
    const row = { channel_id: ch.id, user_id: body.user_id, display_name: nameOf(body.user_id), role_name: BY_ID[body.user_id]?.role_name ?? "Staff", added_by_name: nameOf(uid), added_reason: body.added_reason ?? "Added by hand", joined_at: new Date().toISOString() };
    if (!isMember(ch, body.user_id)) ch.members.push(body.user_id);
    world.membersByHand.push(row);
    return json(res, row, 201);
  }

  if (method === "GET" && tail === "membership-rule") {
    return json(res, { rule: world.membershipRules?.find((r) => r.channel_id === ch.id) ?? null });
  }
  if (method === "PUT" && tail === "membership-rule") {
    const body = await readBody(req);
    const existing = world.membershipRules?.find((r) => r.channel_id === ch.id);
    if (existing && body) Object.assign(existing, body);
    return json(res, { rule: existing ?? body });
  }

  if (method === "GET" && tail === "attachments") {
    const list = (world.attachments ?? []).filter((a) => a.channel_id === ch.id);
    return json(res, { attachments: list });
  }
  if (method === "POST" && tail === "attachments") {
    const body = await readBody(req);
    if (!body?.filename) return fail(res, 400, "filename required");
    const isImage = body.filename.endsWith(".png") || body.filename.endsWith(".jpg") || body.filename.endsWith(".webp");
    const att = {
      id: id("att"),
      channel_id: ch.id,
      filename: body.filename,
      content_type: body.content_type ?? (isImage ? "image/jpeg" : "application/pdf"),
      size_bytes: body.size_bytes ?? (isImage ? 840 * 1024 : 142 * 1024),
      author_id: uid,
      author_name: nameOf(uid),
      created_at: new Date().toISOString(),
      caption: body.caption ?? "",
      url: `/files/${body.filename}`,
    };
    world.attachments.push(att);
    world.messages.push({
      id: id("b"),
      channel_id: ch.id,
      author_id: uid,
      kind: "TEXT",
      body: body.caption ? `${body.caption} (${body.filename})` : `Attached: ${body.filename}`,
      created_at: new Date().toISOString(),
    });
    return json(res, { attachment: att }, 201);
  }

  if (method === "GET" && tail === "reactions") {
    const byMsg = {};
    for (const msg of world.messages.filter((x) => x.channel_id === ch.id)) {
      byMsg[msg.id] = reactionListFor(msg.id, uid);
    }
    return json(res, { reactions: byMsg });
  }

  const msgRoute = /^messages\/([^/]+)(?:\/(.*))?$/.exec(tail);
  if (msgRoute) {
    const [, msgId, msgTail = ""] = msgRoute;
    if (msgTail === "" && method === "PATCH") {
      const body = await readBody(req);
      const target = world.messages.find((x) => x.id === msgId && x.channel_id === cid);
      if (!target) return fail(res, 404, "not found");
      if (target.author_id !== uid) return fail(res, 403, "not your message");
      target.body = body.body;
      target.edited_at = new Date().toISOString();
      return json(res, view(target));
    }
    if (msgTail === "" && method === "DELETE") {
      const target = world.messages.find((x) => x.id === msgId && x.channel_id === cid);
      if (!target) return fail(res, 404, "not found");
      if (target.author_id !== uid) return fail(res, 403, "not your message");
      target.body = "This message was deleted";
      target.deleted_at = new Date().toISOString();
      return json(res, view(target));
    }
    if (msgTail === "reactions") {
      if (method === "GET") return json(res, { reactions: reactionListFor(msgId, uid) });
      if (method === "PUT") {
        const body = await readBody(req);
        const emoji = body?.emoji;
        if (!emoji) return fail(res, 400, "emoji required");
        const idx = world.reactions.findIndex((r) => r.message_id === msgId && r.user_id === uid && r.emoji === emoji);
        if (idx >= 0) world.reactions.splice(idx, 1);
        else world.reactions.push({ message_id: msgId, user_id: uid, emoji });
        return json(res, { reactions: reactionListFor(msgId, uid) });
      }
      if (method === "DELETE") {
        const emoji = url.searchParams.get("emoji");
        world.reactions = world.reactions.filter((r) => !(r.message_id === msgId && r.user_id === uid && (!emoji || r.emoji === emoji)));
        return json(res, { reactions: reactionListFor(msgId, uid) });
      }
    }
    if (msgTail === "seen-by" && method === "GET") {
      return json(res, {
        count: 2,
        readers: [
          { user_id: uid, display_name: nameOf(uid), seen_at: new Date().toISOString() },
          { user_id: "a0000000-0000-4000-9000-000000000002", display_name: "Yard manager (demo)", seen_at: new Date().toISOString() },
        ],
      });
    }
  }

  if (method === "GET" && tail === "messages") {
    const limitRaw = Number(url.searchParams.get("limit") ?? 0);
    const limit = limitRaw > 100 ? 100 : limitRaw > 0 ? limitRaw : 30;
    const cursor = url.searchParams.get("cursor") ?? "";
    let rows = world.messages.filter((x) => x.channel_id === cid && !x.thread_root_id).sort((a, b) => (a.created_at === b.created_at ? (a.id < b.id ? 1 : -1) : a.created_at < b.created_at ? 1 : -1));
    if (cursor !== "") {
      const [ts, rid = "ffffffff-ffff-ffff-ffff-ffffffffffff"] = cursor.split("|");
      if (Number.isNaN(Date.parse(ts))) return fail(res, 400, "malformed pagination cursor");
      rows = rows.filter((x) => x.created_at < ts || (x.created_at === ts && x.id < rid));
    }
    const page = rows.slice(0, limit);
    const next = rows.length > limit ? `${page.at(-1).created_at}|${page.at(-1).id}` : undefined;
    const views = page.map(view);
    return json(res, pageShape === "items" ? { items: views, ...(next ? { next_cursor: next } : {}), limit } : { messages: views, ...(next ? { next_cursor: next } : {}) });
  }

  if (method === "POST" && tail === "messages") {
    const body = await readBody(req);
    if (body === null) return fail(res, 400, "invalid_json");
    let text = String(body.body ?? "").trim();
    if (text === "") return fail(res, 400, "message body is empty");
    if (body.thread_root_id !== undefined && body.thread_root_id !== "") {
      if (!UUID.test(body.thread_root_id)) return fail(res, 404, "not found");
      const root = world.messages.find((x) => x.id === body.thread_root_id && x.channel_id === cid && !x.thread_root_id);
      if (!root) return fail(res, 404, "not found");
    }
    text = truncate(text);
    return json(res, view(postMessage(ch, uid, text, body.thread_root_id || undefined)), 201);
  }

  const thread = /^threads\/([^/]+)$/.exec(tail);
  if (method === "GET" && thread) {
    if (!UUID.test(thread[1])) return fail(res, 404, "not found");
    const replies = world.messages.filter((x) => x.channel_id === cid && x.thread_root_id === thread[1]).sort((a, b) => (a.created_at < b.created_at ? -1 : 1)).map((x) => ({ ...view(x), reply_count: 0 }));
    return json(res, { messages: replies });
  }

  if (method === "POST" && tail === "read") {
    world.lastRead[`${uid}|${cid}`] = new Date().toISOString();
    return json(res, { status: "ok" });
  }

  const ack = /^messages\/([^/]+)\/ack$/.exec(tail);
  if (method === "POST" && ack) {
    if (!UUID.test(ack[1])) return fail(res, 404, "not found");
    const body = await readBody(req);
    if (body === null) return fail(res, 400, "invalid_json");
    const methodWord = body.method === undefined || body.method === "" ? "TAP" : body.method;
    if (!["TAP", "QUICK_REPLY", "VOICE"].includes(methodWord)) return fail(res, 400, "ack method must be one of TAP, QUICK_REPLY, VOICE");
    const target = world.messages.find((x) => x.id === ack[1] && x.channel_id === cid);
    if (!target) return fail(res, 404, "not found");
    const existing = world.acks.find((a) => a.message_id === target.id && a.user_id === uid);
    const row = { message_id: target.id, user_id: uid, method: methodWord, acked_at: new Date().toISOString() };
    if (existing) Object.assign(existing, row);
    else world.acks.push(row);
    emit(ch.id, "unread", { channel_id: ch.id });
    return json(res, row);
  }

  const progress = /^messages\/([^/]+)\/ack-progress$/.exec(tail);
  if (method === "GET" && progress) {
    if (!UUID.test(progress[1])) return fail(res, 404, "not found");
    const target = world.messages.find((x) => x.id === progress[1] && x.channel_id === cid);
    if (!target) return fail(res, 404, "not found");
    const acks = world.acks.filter((a) => a.message_id === target.id);
    const required = ch.members.length;
    const acked = new Set(acks.map((a) => a.user_id)).size;
    return json(res, { message_id: target.id, required, acked, remaining: Math.max(0, required - acked), complete: required > 0 && acked >= required, acks });
  }

  if (method === "GET" && tail === "house-queue") {
    const open = url.searchParams.get("open");
    if (open !== null && open !== "1") return fail(res, 400, "open must be 1");
    const rows = world.houseQueue.filter((c) => c.channel_id === cid && (open !== "1" || !c.claimed_by));
    return json(res, { conversations: rows });
  }

  if (tail === "notify-prefs") {
    if (method === "GET") return json(res, prefsFor(uid, ch));
    if (method === "PUT") {
      const body = await readBody(req);
      if (body === null) return fail(res, 400, "invalid_json");
      const cur = prefsFor(uid, ch);
      const next = { ...cur };
      delete next.inherited;
      if (body.mode !== undefined && body.mode !== "" && !["ALL", "MENTIONS", "MUTED"].includes(body.mode)) return fail(res, 400, "mode must be one of ALL, MENTIONS, MUTED");
      const silencing = body.mode === "MUTED" || (body.mode === undefined && body.muted === true);
      if (silencing && ch.tier === "ESCALATION") return fail(res, 409, "escalation channels cannot be muted");
      if (body.desktop_enabled !== undefined) next.desktop_enabled = body.desktop_enabled === true;
      if (body.escalation_override !== undefined) next.escalation_override = body.escalation_override === true;
      if (body.muted !== undefined) {
        next.muted = body.muted === true;
        next.mode = next.muted ? "MUTED" : next.mode === "MUTED" ? "ALL" : next.mode;
      }
      if (body.mode !== undefined && body.mode !== "") {
        next.mode = body.mode;
        next.muted = body.mode === "MUTED";
      }
      next.updated_at = new Date().toISOString();
      world.prefs[`${uid}|${cid}`] = next;
      return json(res, { ...next, inherited: false });
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
  console.log(`messages-stub listening on ${port}`);
});
