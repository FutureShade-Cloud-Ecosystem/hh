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
    display_name: "Payables and Receivables (demo)",
    role_code: "payables_receivables",
    role_name: "Payables and Receivables",
    tier: 3,
    scope: "BRANCH",
  },
};
const BY_ID = Object.fromEntries(Object.entries(USERS).map(([flow, u]) => [u.user_id, { flow, ...u }]));

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

const T0 = Date.now();
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
  world = {
    channels: [
      { id: CH.branch, kind: "BRANCH", branch_id: BRANCH, slug: "branch-point-loma", name: "Point Loma", topic: "Everyone at the yard", tier: "STANDARD", is_archived: false, created_at: at(9000), members: [S, Y, P] },
      { id: CH.yard, kind: "TEAM", slug: "yard-point-loma", name: "Yard floor", topic: "Picks, staging and the gate", tier: "STANDARD", is_archived: false, created_at: at(9000), members: [S, Y] },
      { id: CH.deliveries, kind: "TOPIC", slug: "deliveries", name: "Deliveries", topic: "Truck 01 to Truck 04", tier: "STANDARD", is_archived: false, created_at: at(9000), members: [S, Y, P] },
      { id: CH.esc, kind: "TOPIC", slug: "escalations", name: "Escalations", topic: "Alerts that must be acknowledged", tier: "ESCALATION", is_archived: false, created_at: at(9000), members: [S, Y, P] },
      { id: CH.house, kind: "TOPIC", branch_id: BRANCH, slug: "house-point-loma", name: "House queue", topic: "Customers with no assigned rep", tier: "STANDARD", is_archived: false, created_at: at(9000), members: [S, Y] },
      { id: CH.dm, kind: "DM", slug: "dm-sales-yard", name: "Direct message", topic: "", tier: "STANDARD", is_archived: false, created_at: at(9000), members: [S, Y], dmNames: { [S]: "Yard manager (demo)", [Y]: "Inside Sales (demo)" } },
      { id: CH.pay, kind: "TOPIC", branch_id: BRANCH, slug: "payments-point-loma", name: "Payments Point Loma", topic: "The day's payments", tier: "STANDARD", is_archived: false, created_at: at(9000), members: [P], defaultMode: "MUTED" },
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
  // A payment event that carries a figure the core sent, with the rules that routed it.
  world.messages.push({
    id: id("b"), channel_id: CH.pay, kind: "EVENT", body: "Payment received: $1,240.00 on order SO-1042 (ACH).", created_at: at(30),
    ref: { topic: "payment.received", entity: "order:SO-1042", summary: "Payment received", amount_cents: 124000, rules: ["OWN_ORDER"] },
  });
  world.messages.push({
    id: id("b"), channel_id: CH.pay, kind: "EVENT", body: "Day total: 4 payments, $6,810.50.", created_at: at(20),
    ref: { topic: "payment.rollup", count: 4, amount_cents: 681050, rollup_day: new Date(T0).toISOString().slice(0, 10) },
  });
  // The forced acknowledgement: a system notice on a credit hold, in the escalations room.
  world.messages.push({
    id: "b9000000-0000-4000-9000-000000000001", channel_id: CH.esc, kind: "EVENT", body: "Order SO-1042 is on credit hold. Do not release it to the yard.", created_at: at(25), requires_ack: true, priority: "HIGH", state_key: "order:SO-1042",
    ref: { topic: "order.credit_hold", entity: "order:SO-1042", summary: "Credit hold" },
  });
  // House conversation messages, so the room reads.
  world.messages.push({ id: id("b"), channel_id: CH.house, kind: "EVENT", body: "Ridgeview Outdoor Living wrote in: asking for a delivery window on Friday.", created_at: at(95), ref: { topic: "msg.event.customer.inbound", entity: "customer:e0000000-0000-4000-9000-000000000001" } });
}
reset();

// ---- helpers ------------------------------------------------------------------------------------

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
      streams.forEach((s) => s.res.destroy());
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
  if (!isMember(ch, uid)) return fail(res, 403, "not a channel member");

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
