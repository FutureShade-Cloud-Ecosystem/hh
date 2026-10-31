// The shared tenant stub: a node http server serving the contractor routes
// (/api/v1/tenant/*) with Summit Ridge Builders data (tenant-data.mjs). A
// development and test double only; the real routes are the core's. It
// answers what the core answers: the same route table (the five session
// minting operations are NOT served, exactly as the core's served mux leaves
// them unregistered), the same refusal order, the same body fields and the same
// words. test/stub-contract.test.mjs holds it to the generated client's
// operation list and to the contract's response shapes, and holds every refusal
// sentence to the core's source.
//
// THE REFUSAL ORDER (core/internal/platform/authz, identity/guards, portal):
//   1. no such path: 404 `404 page not found`; the wrong method on a known
//      path: 405 `Method Not Allowed` (the ServeMux's own words).
//   2. a protected route without a usable bearer: 401 Missing or invalid credentials.
//   3. a bearer of another class (a staff token): 403 Forbidden.
//   4. an X-Org header at all on a tenant route: 400 header X-Org is not accepted on this route.
//   5. a person who is not active: 401 Missing or invalid credentials.
//   6. a capability the role does not hold: 403 Forbidden (the one shared body).
//   7. the handler: body decode (400 invalid_json), then the module's own checks.
//
// Sign in is NOT a core route today. The stub's own door for tests and the dev
// server is POST /_stub/session; POST /_stub/reset puts the world back.
import { createServer } from "node:http";
import { fileURLToPath } from "node:url";
import {
  BRANCH_ID, CUSTOMER_ID, DEALER_CONFIG, PERSONAS, PRODUCTS, STUB_PASSWORD, capabilitiesOf, createWorld, lineTotal, productById, taxOn, uid,
} from "./tenant-data.mjs";

const MAX_BODY = 1 << 20;
const MAX_INSTRUCTIONS = 2000;
const CURSOR_VERSION = "v1:";

const ROLES = ["account_admin", "buyer", "buyer_no_pay", "ap_finance", "field_crew"];
const PROJECT_STATUSES = ["PLANNING", "QUOTING", "ORDERED", "FULFILLED", "CANCELLED"];
const QUOTE_STATUSES = ["DRAFT", "SENT", "ACCEPTED", "CONVERTED", "EXPIRED", "LOST"];
const ORDER_STATUSES = ["DRAFT", "CONFIRMED", "PICKING", "READY", "PARTIALLY_FULFILLED", "FULFILLED", "INVOICED", "CLOSED", "CANCELLED"];
const INVOICE_STATUSES = ["DRAFT", "FINALIZED", "PARTIALLY_PAID", "PAID", "VOID"];

const PORTAL_ACCEPT_REFUSAL =
  "a quote is accepted through the link the dealer sent you, where you confirm the quantities and type your name; ask the dealer for the link";

// ---- the world and the people -------------------------------------------------------------------

let world = createWorld();

// Test controls (stub only, never core behaviour): a fault makes every gated request under a path answer a status
// after the gate has passed, so a leg can show an error or a refusal on demand; a delay holds every handler for a
// while so a leg can see the loading state; empty clears a collection. A reset clears all three.
let faults = [];
let delayMs = 0;

function userFor(token) {
  // A tenant bearer is `pro-stub.<persona>.<expiresAtMs>`, or the demo route's JWT shaped one (a payload carrying
  // the persona and an exp claim); a staff one is `staff-stub.x`.
  const parts = token.split(".");
  if (parts[0] === "staff-stub") return { classOf: "staff" };
  if (parts.length === 3 && parts[0] !== "pro-stub") {
    try {
      const claims = JSON.parse(Buffer.from(parts[1], "base64url").toString("utf8"));
      const persona = PERSONAS[claims.stub_persona];
      if (!persona || !(claims.exp * 1000 > Date.now())) return null;
      const user = world.users.find((u) => u.email === persona.email);
      return user ? { classOf: "tenant", user } : null;
    } catch {
      return null;
    }
  }
  if (parts.length !== 3 || parts[0] !== "pro-stub") return null;
  const persona = PERSONAS[parts[1]];
  if (!persona || !(Number(parts[2]) > Date.now())) return null;
  const user = world.users.find((u) => u.email === persona.email);
  return user ? { classOf: "tenant", user } : null;
}

// ---- response helpers ---------------------------------------------------------------------------

function send(res, status, body) {
  res.writeHead(status, { "Content-Type": "application/json" });
  res.end(body === undefined ? "" : JSON.stringify(body) + "\n");
}
const refuse = (res, status, message) => send(res, status, { error: message });
const unauthorized = (res) => send(res, 401, { error: "Missing or invalid credentials" });
const forbidden = (res) => send(res, 403, { error: "Forbidden" });
const notFound = (res) => refuse(res, 404, "not found");
const badRequest = (res, message) => refuse(res, 400, message);
const invalidTransition = (res) => refuse(res, 409, "invalid state transition");
const ok = (res, body) => send(res, 200, body);
const created = (res, body) => send(res, 201, body);
const noContent = (res) => {
  res.writeHead(204);
  res.end();
};

function encodeCursor(key) {
  return Buffer.from(CURSOR_VERSION + key).toString("base64url");
}
function decodeCursor(raw) {
  try {
    const text = Buffer.from(raw, "base64url").toString("utf8");
    if (!text.startsWith(CURSOR_VERSION)) return null;
    return text.slice(CURSOR_VERSION.length);
  } catch {
    return null;
  }
}

function pageParams(url) {
  let limit = 50;
  const raw = url.searchParams.get("limit");
  if (raw !== null && raw !== "") {
    const n = Number.parseInt(raw, 10);
    if (Number.isFinite(n) && n > 0) limit = n;
  }
  if (limit > 200) limit = 200;
  const cursorRaw = url.searchParams.get("cursor");
  let cursor = "";
  if (cursorRaw) {
    const key = decodeCursor(cursorRaw);
    if (key === null) return { error: "cursor is malformed" };
    cursor = key;
  }
  return { limit, cursor };
}

function splitCursor(key) {
  if (key === "") return { ok: true, a: "", b: "" };
  const i = key.lastIndexOf("/");
  if (i <= 0 || i === key.length - 1) return { ok: false };
  return { ok: true, a: key.slice(0, i), b: key.slice(i + 1) };
}

/** One page of a list sorted by (primary DESC, number ASC): the core's keyset order. */
function cursorPage(res, url, rows, primaryOf, numberOf) {
  const p = pageParams(url);
  if (p.error) return badRequest(res, p.error);
  const split = splitCursor(p.cursor);
  if (!split.ok) return badRequest(res, "cursor is malformed");
  const sorted = [...rows].sort((x, y) => {
    const a = primaryOf(x), b = primaryOf(y);
    if (a !== b) return a < b ? 1 : -1;
    const m = numberOf(x), n = numberOf(y);
    return m < n ? -1 : m > n ? 1 : 0;
  });
  const after = p.cursor === "" ? sorted : sorted.filter((r) => {
    const a = primaryOf(r);
    return a < split.a || (a === split.a && numberOf(r) > split.b);
  });
  const items = after.slice(0, p.limit);
  const more = after.length > p.limit;
  const last = items[items.length - 1];
  return ok(res, { items, next_cursor: more ? encodeCursor(`${primaryOf(last)}/${numberOf(last)}`) : null, limit: p.limit });
}

// ---- views --------------------------------------------------------------------------------------

const userView = (u) => ({ id: u.id, customer_id: u.customer_id, email: u.email, name: u.name, role: u.role, status: u.status, notification_prefs: u.notification_prefs, created_at: u.created_at, updated_at: u.updated_at });
const orderSummary = (o) => ({ id: o.id, order_no: o.order_no, customer_id: o.customer_id, quote_id: o.quote_id, status: o.status, fulfillment: o.fulfillment, requested_date: o.requested_date, subtotal_cents: o.subtotal_cents, tax_cents: o.tax_cents, total_cents: o.total_cents, created_at: o.created_at, has_backorder: o.has_backorder });
const orderDetail = (o) => ({ ...orderSummary(o), lines: o.lines, delivery_instructions: o.delivery_instructions });
// ---- the portal's payoff (TM-2b), following core/internal/payments PayableInvoices ----------------------------
// An open invoice that carries discount terms (the control door's {discount}) and is inside its window quotes the payoff:
// the open amount less the holdback, less the terms' percent of it, half to even. The list shows it and the pay charges it.
// The core judges the window on the dealer's house day (America/Toronto), never the UTC day. The control door can pin the
// clock ({clock}) so a leg can stand in the house evening, when the UTC day is already tomorrow.
const HOUSE_ZONE = "America/Toronto";
let clockOverride = null;
const stubNow = () => (clockOverride === null ? Date.now() : clockOverride);
function houseDay(ms) {
  const p = Object.fromEntries(new Intl.DateTimeFormat("sv-SE", { timeZone: HOUSE_ZONE, year: "numeric", month: "2-digit", day: "2-digit" }).formatToParts(new Date(ms)).map((x) => [x.type, x.value]));
  return `${p.year}-${p.month}-${p.day}`;
}
const stubToday = () => houseDay(stubNow());
function houseDayPlus(days) {
  const [y, m, d] = stubToday().split("-").map(Number);
  return new Date(Date.UTC(y, m - 1, d + days)).toISOString().slice(0, 10);
}
function halfEvenPercent(cents, bps) {
  const n = cents * bps;
  const q = Math.floor(n / 10_000);
  const r = n % 10_000;
  return r > 5_000 ? q + 1 : r === 5_000 ? q + (q % 2) : q;
}
function quoteOf(i) {
  if (!["FINALIZED", "PARTIALLY_PAID"].includes(i.status)) return null;
  const payable = Math.max(i.balance_cents - i.holdback_cents, 0);
  if (payable <= 0) return null;
  const inside = (i.discount_bps ?? 0) > 0 && typeof i.discount_by === "string" && stubToday() <= i.discount_by;
  // The running cap, as the core holds it: the terms' percent of the whole base (total less holdback), less what earlier
  // receipts already took. The percent of the open amount never passes the room that is left.
  const room = inside ? Math.max(halfEvenPercent(Math.max(i.total_cents - i.holdback_cents, 0), i.discount_bps) - (i.discount_taken_cents ?? 0), 0) : 0;
  const discount = inside ? Math.min(halfEvenPercent(payable, i.discount_bps), room) : 0;
  return { payoff: payable - discount, discount, by: inside ? i.discount_by : "" };
}
function quoteFields(i) {
  const q = quoteOf(i);
  if (q === null) return {};
  return q.discount > 0 ? { payoff_cents: q.payoff, discount_cents: q.discount, discount_by: q.by } : { payoff_cents: q.payoff };
}
const invoiceSummary = (i) => ({ ...quoteFields(i), id: i.id, invoice_no: i.invoice_no, customer_id: i.customer_id, order_id: i.order_id, status: i.status, issue_date: i.issue_date, due_date: i.due_date, subtotal_cents: i.subtotal_cents, tax_cents: i.tax_cents, holdback_cents: i.holdback_cents, total_cents: i.total_cents, balance_cents: i.balance_cents });
const quoteSummary = (q) => ({ id: q.id, quote_no: q.quote_no, customer_id: q.customer_id, status: q.status, valid_until: q.valid_until, subtotal_cents: q.subtotal_cents, tax_cents: q.tax_cents, total_cents: q.total_cents, created_at: q.created_at });
const quoteDetail = (q) => ({ ...quoteSummary(q), lines: q.lines });
const lineView = (l) => ({ id: l.id, line_no: l.line_no, product_id: l.product_id, sku: l.sku, description: l.description, qty_entered: l.qty_entered, uom: l.uom, notes: l.notes, indicative_unit_price_cents: l.indicative_unit_price_cents, indicative_line_total_cents: l.indicative_line_total_cents, priced: l.priced });
const projectView = (p) => ({ id: p.id, customer_id: p.customer_id, name: p.name, status: p.status, line_count: p.lines.length, subtotal_cents: p.lines.reduce((s, l) => s + (l.priced ? l.indicative_line_total_cents : 0), 0), created_at: p.created_at, updated_at: p.updated_at });
const projectDetail = (p) => ({ ...projectView(p), lines: p.lines.map(lineView) });
const paymentView = (p) => p;

function jobSummary(job) {
  const orders = world.orders.filter((o) => world.jobs[o.project_job - 1] === job);
  const orderIds = new Set(orders.map((o) => o.id));
  const money = world.invoices.filter((i) => orderIds.has(i.order_id) && ["FINALIZED", "PARTIALLY_PAID", "PAID"].includes(i.status));
  return {
    id: job.id,
    name: job.name,
    job_address: job.job_address,
    order_count: orders.length,
    spend_cents: money.reduce((s, i) => s + i.total_cents, 0),
    delivery_count: orders.filter((o) => ["PARTIALLY_FULFILLED", "FULFILLED", "INVOICED", "CLOSED"].includes(o.status) && o.fulfillment === "DELIVERY").length,
    created_at: job.created_at,
  };
}

// ---- handlers: team and settings --------------------------------------------------------------------

async function readJson(req, res) {
  const chunks = [];
  let size = 0;
  for await (const chunk of req) {
    size += chunk.length;
    if (size > MAX_BODY) {
      badRequest(res, "invalid_json");
      return undefined;
    }
    chunks.push(chunk);
  }
  const text = Buffer.concat(chunks).toString("utf8");
  try {
    return JSON.parse(text);
  } catch {
    badRequest(res, "invalid_json");
    return undefined;
  }
}

const H = {};

H.tenantGetConfig = (ctx) => ok(ctx.res, DEALER_CONFIG);

H.tenantGetMe = (ctx) => ok(ctx.res, { ...userView(ctx.user), capabilities: capabilitiesOf(ctx.user.role) });

H.tenantUpdateMe = async (ctx) => {
  const raw = await readJson(ctx.req, ctx.res);
  if (raw === undefined) return;
  if (raw === null || typeof raw !== "object" || Array.isArray(raw)) return badRequest(ctx.res, "invalid_json");
  let name;
  if ("name" in raw && raw.name !== null) {
    if (typeof raw.name !== "string") return badRequest(ctx.res, "invalid name");
    name = raw.name;
  }
  let prefs;
  if ("notification_prefs" in raw) {
    const v = raw.notification_prefs;
    if (v === null) prefs = {};
    else if (typeof v === "object" && !Array.isArray(v)) prefs = v;
    else return badRequest(ctx.res, "notification_prefs must be a JSON object");
  }
  if (name !== undefined) ctx.user.name = name;
  if (prefs !== undefined) ctx.user.notification_prefs = prefs;
  ctx.user.updated_at = new Date().toISOString();
  ok(ctx.res, userView(ctx.user));
};

H.tenantChangePassword = (ctx) => refuse(ctx.res, 501, "changing your password here is not available yet");

H.tenantListUsers = (ctx) => ok(ctx.res, world.users.map(userView));
H.tenantListInvites = (ctx) => ok(ctx.res, world.invites);
H.tenantCreateInvite = (ctx) => refuse(ctx.res, 501, "inviting users is not available yet");

H.tenantUpdateUserRole = async (ctx) => {
  const body = await readJson(ctx.req, ctx.res);
  if (body === undefined) return;
  const role = body?.role ?? "";
  if (!ROLES.includes(role)) return badRequest(ctx.res, `unknown role "${role}"`);
  const target = world.users.find((u) => u.id === ctx.params.id);
  if (!target) return notFound(ctx.res);
  const demoting = target.role === "account_admin" && role !== "account_admin";
  if (demoting && world.users.filter((u) => u.role === "account_admin" && u.status === "active").length <= 1) {
    return refuse(ctx.res, 409, "last account_admin cannot be demoted or deactivated");
  }
  target.role = role;
  target.updated_at = new Date().toISOString();
  ok(ctx.res, { status: "ok" });
};

H.tenantUpdateUserStatus = async (ctx) => {
  const body = await readJson(ctx.req, ctx.res);
  if (body === undefined) return;
  const status = body?.status ?? "";
  if (!["active", "inactive", "pending"].includes(status)) return badRequest(ctx.res, `unknown status "${status}"`);
  const target = world.users.find((u) => u.id === ctx.params.id);
  if (!target) return notFound(ctx.res);
  const deactivating = target.role === "account_admin" && target.status === "active" && status !== "active";
  if (deactivating && world.users.filter((u) => u.role === "account_admin" && u.status === "active").length <= 1) {
    return refuse(ctx.res, 409, "last account_admin cannot be demoted or deactivated");
  }
  target.status = status;
  target.updated_at = new Date().toISOString();
  ok(ctx.res, { status: "ok" });
};

// ---- handlers: dashboard, billing, invoices, payments ----------------------------------------------

const availableCredit = (limit, balance) => Math.max(0, limit - balance);

H.tenantGetDashboard = (ctx) => {
  world.recompute();
  const a = world.account;
  const recent = [...world.orders].sort((x, y) => (x.created_at < y.created_at ? 1 : -1)).slice(0, 5).map(orderSummary);
  ok(ctx.res, { account: { ...a }, open_balance_cents: a.balance_cents, available_credit_cents: availableCredit(a.credit_limit_cents, a.balance_cents), recent_orders: recent });
};

H.tenantGetBillingSummary = (ctx) => {
  world.recompute();
  const a = world.account;
  ok(ctx.res, { open_balance_cents: a.balance_cents, credit_limit_cents: a.credit_limit_cents, available_credit_cents: availableCredit(a.credit_limit_cents, a.balance_cents), on_hold: a.on_hold, payment_terms_days: a.payment_terms_days });
};

H.tenantListInvoices = (ctx) => {
  const status = ctx.url.searchParams.get("status") ?? "";
  if (status !== "" && !INVOICE_STATUSES.includes(status)) return badRequest(ctx.res, `unknown invoice status "${status}"`);
  const rows = world.invoices.filter((i) => status === "" || i.status === status).map(invoiceSummary);
  cursorPage(ctx.res, ctx.url, rows, (r) => r.issue_date, (r) => r.invoice_no);
};

H.tenantGetInvoice = (ctx) => {
  const inv = world.invoices.find((i) => i.id === ctx.params.id);
  if (!inv) return notFound(ctx.res);
  // The detail carries no payoff fields, as the core's detail does not (the list is where the portal quotes).
  const { payoff_cents, discount_cents, discount_by, ...summary } = invoiceSummary(inv);
  ok(ctx.res, { ...summary, lines: inv.lines, payment_history: inv.payment_history });
};

H.tenantListPayments = (ctx) => ok(ctx.res, [...world.payments].sort((a, b) => (a.received_at < b.received_at ? 1 : -1)).map(paymentView));

H.tenantCreatePayment = async (ctx) => {
  const body = await readJson(ctx.req, ctx.res);
  if (body === undefined) return;
  const ids = Array.isArray(body?.invoice_ids) ? body.invoice_ids : [];
  if (ids.length === 0) return badRequest(ctx.res, "at least one invoice is required");
  const methodId = body.payment_method_id ?? "";
  const nonce = body.nonce ?? "";
  if (methodId === "" && nonce === "") return badRequest(ctx.res, "a stored method or a tokenization nonce is required");
  if (new Set(ids).size !== ids.length) return badRequest(ctx.res, "an invoice may be selected only once");
  const key = body.idempotency_key ?? "";
  if (key !== "") {
    const replay = world.payments.find((p) => p.idempotency_key === key);
    if (replay) return created(ctx.res, paymentView(stripPrivate(replay)));
  }
  const targets = [];
  for (const id of ids) {
    const inv = world.invoices.find((i) => i.id === id);
    if (!inv || !["FINALIZED", "PARTIALLY_PAID"].includes(inv.status) || inv.balance_cents <= 0) {
      return badRequest(ctx.res, `invoice "${id}" is not payable; refresh and retry`);
    }
    targets.push(inv);
  }
  if (methodId !== "" && !world.methods.some((m) => m.id === methodId && m.status === "active")) return notFound(ctx.res);
  // Card data never reaches this server: a hosted field hands over a token (a nonce). The stub's sandbox verdicts ride the nonce.
  if (nonce === "stub-nonce-declined") return refuse(ctx.res, 402, "payment declined");
  if (nonce === "stub-nonce-pending") return refuse(ctx.res, 409, "payment is pending, please retry");
  const quotes = targets.map((i) => ({ inv: i, ...quoteOf(i) }));
  const total = quotes.reduce((s, q) => s + q.payoff, 0);
  // The amount the Pay button showed: the core quotes again at the tap and refuses before anything moves if it differs.
  if (Number.isInteger(body.expected_cents) && body.expected_cents > 0 && body.expected_cents !== total) {
    return refuse(ctx.res, 409, "The amount to pay has changed since you opened this. Please check the new amount and confirm again.");
  }
  const nowIso = new Date().toISOString();
  const method = methodId !== "" ? world.methods.find((m) => m.id === methodId) : null;
  const payment = {
    id: uid("5b", world.counters.payment),
    payment_no: `PMT-${world.counters.payment++}`,
    customer_id: CUSTOMER_ID,
    method: method && method.method_type === "BANK" ? "EFT" : "CARD",
    amount_cents: total,
    status: "RECEIVED",
    gateway_ref: `stub-ref-${world.counters.payment}`,
    received_at: nowIso,
    allocations: quotes.map((q) => ({ invoice_id: q.inv.id, amount_cents: q.payoff, created_at: nowIso })),
    idempotency_key: key,
  };
  for (const q of quotes) {
    const inv = q.inv;
    inv.payment_history.push({ id: payment.id, amount_cents: q.payoff, method: payment.method, status: payment.status, received_at: nowIso });
    // The money and the discount together close the open amount; a held back invoice keeps its holdback.
    inv.balance_cents -= q.payoff + q.discount;
    inv.discount_taken_cents = (inv.discount_taken_cents ?? 0) + q.discount;
    inv.status = inv.balance_cents === 0 ? "PAID" : "PARTIALLY_PAID";
  }
  world.payments.push(payment);
  world.recompute();
  created(ctx.res, paymentView(stripPrivate(payment)));
};

function stripPrivate(p) {
  const { idempotency_key, ...rest } = p;
  return rest;
}

// ---- handlers: payment methods ---------------------------------------------------------------------

H.tenantListPaymentMethods = (ctx) => ok(ctx.res, world.methods.filter((m) => m.status === "active"));

H.tenantAddPaymentMethod = async (ctx) => {
  const body = await readJson(ctx.req, ctx.res);
  if (body === undefined) return;
  if (!body?.nonce) return badRequest(ctx.res, "a tokenization nonce is required");
  const n = world.counters.method++;
  const nowIso = new Date().toISOString();
  const makeDefault = body.make_default === true || world.methods.every((m) => m.status !== "active");
  if (makeDefault) for (const m of world.methods) m.is_default = false;
  const method = { id: uid("5f", n), customer_id: CUSTOMER_ID, gateway: "stub", method_type: "CARD", brand: "VISA", last4: "4242", exp_month: 12, exp_year: new Date().getUTCFullYear() + 3, is_default: makeDefault, status: "active", created_at: nowIso, updated_at: nowIso };
  world.methods.push(method);
  created(ctx.res, method);
};

H.tenantSetDefaultPaymentMethod = (ctx) => {
  const target = world.methods.find((m) => m.id === ctx.params.id && m.status === "active");
  if (!target) return notFound(ctx.res);
  for (const m of world.methods) m.is_default = m === target;
  ok(ctx.res, { status: "ok" });
};

H.tenantRemovePaymentMethod = (ctx) => {
  const target = world.methods.find((m) => m.id === ctx.params.id && m.status === "active");
  if (!target) return notFound(ctx.res);
  target.status = "removed";
  target.is_default = false;
  ok(ctx.res, { status: "ok" });
};

// ---- handlers: orders, reorder, returns -------------------------------------------------------------

H.tenantListOrders = (ctx) => {
  const status = ctx.url.searchParams.get("status") ?? "";
  if (status !== "" && !ORDER_STATUSES.includes(status)) return badRequest(ctx.res, `unknown order status "${status}"`);
  const rows = world.orders.filter((o) => status === "" || o.status === status).map(orderSummary);
  cursorPage(ctx.res, ctx.url, rows, (r) => r.created_at, (r) => r.order_no);
};

H.tenantGetOrder = (ctx) => {
  const o = world.orders.find((x) => x.id === ctx.params.id);
  if (!o) return notFound(ctx.res);
  ok(ctx.res, orderDetail(o));
};

H.tenantSetDeliveryInstructions = async (ctx) => {
  const body = await readJson(ctx.req, ctx.res);
  if (body === undefined) return;
  const text = typeof body?.instructions === "string" ? body.instructions : "";
  if (text.length > MAX_INSTRUCTIONS) return badRequest(ctx.res, `delivery instructions exceed ${MAX_INSTRUCTIONS} characters`);
  const o = world.orders.find((x) => x.id === ctx.params.id);
  if (!o) return notFound(ctx.res);
  o.delivery_instructions = text;
  ok(ctx.res, { status: "ok" });
};

H.tenantGetReorderPreview = (ctx) => {
  const o = world.orders.find((x) => x.id === ctx.params.id);
  if (!o) return notFound(ctx.res);
  ok(ctx.res, {
    source_order_id: o.id,
    source_order_no: o.order_no,
    branch_id: BRANCH_ID,
    lines: o.lines.map((l) => ({ line_no: l.line_no, product_id: l.product_id, description: l.description, qty_entered: l.qty_entered, uom_entered: l.uom_entered, unit_price_cents: l.unit_price_cents, price_uom: l.uom_entered, line_total_cents: l.line_total_cents, available: true })),
  });
};

H.tenantCreateReorder = async (ctx) => {
  const body = await readJson(ctx.req, ctx.res);
  if (body === undefined) return;
  const lines = Array.isArray(body?.lines) ? body.lines : [];
  if (lines.length > 500) return badRequest(ctx.res, "too many lines selected");
  const o = world.orders.find((x) => x.id === ctx.params.id);
  if (!o) return notFound(ctx.res);
  const chosen = o.lines.filter((l) => lines.length === 0 || lines.includes(l.line_no));
  const idx = world.nextOrderIndex++;
  const subtotal = chosen.reduce((s, l) => s + l.line_total_cents, 0);
  const tax = taxOn(subtotal);
  const copy = {
    ...o,
    id: uid("6d", idx),
    order_no: `SO-${world.counters.order + idx}`,
    status: "DRAFT",
    created_at: new Date().toISOString(),
    subtotal_cents: subtotal,
    tax_cents: tax,
    total_cents: subtotal + tax,
    has_backorder: false,
    delivery_instructions: "",
    lines: chosen.map((l, i) => ({ ...l, line_no: i + 1, allocated_base: "0", fulfilled_base: "0" })),
  };
  world.orders.push(copy);
  created(ctx.res, { order_id: copy.id, order_no: copy.order_no, status: copy.status });
};

// A contractor's orders are not counter (POS) sales, and the core answers a return lookup for anything but
// a committed POS sale with the same not found as a missing order (core/internal/pos/return_requests.go).
H.tenantGetReturnEligibility = (ctx) => notFound(ctx.res);
H.tenantCreateReturnRequest = async (ctx) => {
  const body = await readJson(ctx.req, ctx.res);
  if (body === undefined) return;
  notFound(ctx.res);
};
H.tenantListReturnRequests = (ctx) => ok(ctx.res, world.returnRequests);

// ---- handlers: quotes -------------------------------------------------------------------------------

H.tenantListQuotes = (ctx) => {
  const status = ctx.url.searchParams.get("status") ?? "";
  if (status !== "" && !QUOTE_STATUSES.includes(status)) return badRequest(ctx.res, `unknown quote status "${status}"`);
  const rows = world.quotes.filter((q) => status === "" || q.status === status).map(quoteSummary);
  cursorPage(ctx.res, ctx.url, rows, (r) => r.created_at, (r) => r.quote_no);
};

H.tenantGetQuote = (ctx) => {
  const q = world.quotes.find((x) => x.id === ctx.params.id);
  if (!q) return notFound(ctx.res);
  ok(ctx.res, quoteDetail(q));
};

H.tenantAcceptQuote = (ctx) => refuse(ctx.res, 422, PORTAL_ACCEPT_REFUSAL);

H.tenantRejectQuote = (ctx) => {
  const q = world.quotes.find((x) => x.id === ctx.params.id);
  if (!q) return notFound(ctx.res);
  if (!["SENT", "ACCEPTED"].includes(q.status)) return invalidTransition(ctx.res);
  q.status = "LOST";
  ok(ctx.res, quoteSummary(q));
};

H.tenantConvertQuote = async (ctx) => {
  let fulfillment = "";
  if (Number(ctx.req.headers["content-length"] ?? 0) > 0) {
    const body = await readJson(ctx.req, ctx.res);
    if (body === undefined) return;
    fulfillment = String(body?.fulfillment ?? "").trim().toUpperCase().replace(/[ -]/g, "_");
  }
  if (fulfillment === "DELIVERY") {
    return badRequest(ctx.res, "a delivery is booked by scheduling a stop, not chosen at conversion, convert as pickup or will-call, then schedule the delivery");
  }
  if (fulfillment !== "" && !["PICKUP", "WILL_CALL"].includes(fulfillment)) return badRequest(ctx.res, "fulfillment must be PICKUP or WILL_CALL");
  const q = world.quotes.find((x) => x.id === ctx.params.id);
  if (!q) return notFound(ctx.res);
  if (q.status !== "ACCEPTED") return invalidTransition(ctx.res);
  const idx = world.nextOrderIndex++;
  const order = {
    id: uid("6d", idx),
    order_no: `SO-${world.counters.order + idx}`,
    customer_id: CUSTOMER_ID,
    quote_id: q.id,
    status: "DRAFT",
    fulfillment: fulfillment || "DELIVERY",
    requested_date: world.day(5),
    subtotal_cents: q.subtotal_cents,
    tax_cents: q.tax_cents,
    total_cents: q.total_cents,
    created_at: new Date().toISOString(),
    has_backorder: false,
    delivery_instructions: "",
    project_job: 2,
    lines: q.lines.map((l) => ({ line_no: l.line_no, product_id: l.product_id, description: l.description, qty_entered: l.qty_entered, uom_entered: l.uom_entered, qty_base: l.qty_entered, allocated_base: "0", fulfilled_base: "0", unit_price_cents: l.unit_price_cents, line_total_cents: l.line_total_cents })),
  };
  world.orders.push(order);
  q.status = "CONVERTED";
  ok(ctx.res, { order_id: order.id, order_no: order.order_no });
};

// ---- handlers: the planning board (procurement projects) ----------------------------------------------

const findProject = (id) => world.projects.find((p) => p.id === id);
const touch = (p) => {
  p.updated_at = new Date().toISOString();
};

H.tenantListProcurementProjects = (ctx) => ok(ctx.res, { projects: [...world.projects].sort((a, b) => (a.created_at < b.created_at ? 1 : -1)).map(projectView) });

H.tenantCreateProcurementProject = async (ctx) => {
  const body = await readJson(ctx.req, ctx.res);
  if (body === undefined) return;
  const name = String(body?.name ?? "").trim();
  if (name === "") return badRequest(ctx.res, "project name is required");
  const nowIso = new Date().toISOString();
  const p = { id: uid("9a", world.counters.project++), customer_id: CUSTOMER_ID, name, status: "PLANNING", created_at: nowIso, updated_at: nowIso, lines: [] };
  world.projects.push(p);
  created(ctx.res, projectView(p));
};

H.tenantGetProcurementProject = (ctx) => {
  const p = findProject(ctx.params.id);
  if (!p) return notFound(ctx.res);
  ok(ctx.res, projectDetail(p));
};

H.tenantUpdateProcurementProject = async (ctx) => {
  const body = await readJson(ctx.req, ctx.res);
  if (body === undefined) return;
  const hasName = body?.name !== undefined && body?.name !== null;
  const hasStatus = body?.status !== undefined && body?.status !== null;
  if (!hasName && !hasStatus) return badRequest(ctx.res, "nothing to update");
  let name;
  if (hasName) {
    name = String(body.name).trim();
    if (name === "") return badRequest(ctx.res, "project name cannot be empty");
  }
  if (hasStatus) {
    if (!PROJECT_STATUSES.includes(body.status)) return badRequest(ctx.res, `unknown project status "${body.status}"`);
    if (body.status === "QUOTING") return invalidTransition(ctx.res);
  }
  const p = findProject(ctx.params.id);
  if (!p) return notFound(ctx.res);
  if (hasStatus) {
    const allowed = { PLANNING: ["CANCELLED"], QUOTING: ["PLANNING"] };
    if (body.status !== p.status && !(allowed[p.status] ?? []).includes(body.status)) return invalidTransition(ctx.res);
    p.status = body.status;
  }
  if (hasName) p.name = name;
  touch(p);
  ok(ctx.res, projectView(p));
};

H.tenantDeleteProcurementProject = (ctx) => {
  const p = findProject(ctx.params.id);
  if (!p) return notFound(ctx.res);
  if (p.status !== "PLANNING") return invalidTransition(ctx.res);
  world.projects = world.projects.filter((x) => x !== p);
  noContent(ctx.res);
};

const QTY = /^(?:\d+\.?|\d*\.\d+)$/;
function qtyProblem(qty) {
  const s = String(qty ?? "").trim();
  if (s === "") return "qty_entered is required";
  if (s.length > 64) return "qty_entered is out of range";
  if (!QTY.test(s)) return `qty_entered "${qty}" must be a positive decimal`;
  if (Number(s) <= 0) return "qty_entered must be greater than zero";
  return "";
}

H.tenantAddProcurementLine = async (ctx) => {
  const body = await readJson(ctx.req, ctx.res);
  if (body === undefined) return;
  if (!body?.product_id) return badRequest(ctx.res, "product_id is required");
  if (String(body.uom ?? "").trim() === "") return badRequest(ctx.res, "uom is required");
  const bad = qtyProblem(body.qty_entered);
  if (bad) return badRequest(ctx.res, bad);
  const p = findProject(ctx.params.id);
  if (!p) return notFound(ctx.res);
  if (p.status !== "PLANNING") return invalidTransition(ctx.res);
  const product = productById(body.product_id);
  if (!product) return badRequest(ctx.res, `product "${body.product_id}" not found`);
  const qty = String(body.qty_entered).trim();
  const line = {
    id: uid("9c", world.counters.line++),
    line_no: p.lines.reduce((m, l) => Math.max(m, l.line_no), 0) + 1,
    product_id: product.id,
    sku: product.sku,
    description: product.name,
    qty_entered: qty,
    uom: String(body.uom).trim(),
    notes: body.notes ?? "",
    indicative_unit_price_cents: 0,
    indicative_line_total_cents: 0,
    priced: false,
  };
  p.lines.push(line);
  touch(p);
  created(ctx.res, lineView(line));
};

H.tenantUpdateProcurementLine = async (ctx) => {
  const body = await readJson(ctx.req, ctx.res);
  if (body === undefined) return;
  if (body?.qty_entered === undefined && body?.uom === undefined && body?.notes === undefined) return badRequest(ctx.res, "nothing to update");
  if (body.qty_entered !== undefined && body.qty_entered !== null) {
    const bad = qtyProblem(body.qty_entered);
    if (bad) return badRequest(ctx.res, bad);
  }
  if (body.uom !== undefined && body.uom !== null && String(body.uom).trim() === "") return badRequest(ctx.res, "uom cannot be empty");
  const p = findProject(ctx.params.id);
  const line = p?.lines.find((l) => l.id === ctx.params.lineId);
  if (!p || !line) return notFound(ctx.res);
  if (p.status !== "PLANNING") return invalidTransition(ctx.res);
  if (body.qty_entered != null) line.qty_entered = String(body.qty_entered).trim();
  if (body.uom != null) line.uom = String(body.uom).trim();
  if (body.notes != null) line.notes = body.notes;
  line.priced = false;
  line.indicative_unit_price_cents = 0;
  line.indicative_line_total_cents = 0;
  touch(p);
  ok(ctx.res, lineView(line));
};

H.tenantRemoveProcurementLine = (ctx) => {
  const p = findProject(ctx.params.id);
  const line = p?.lines.find((l) => l.id === ctx.params.lineId);
  if (!p || !line) return notFound(ctx.res);
  if (p.status !== "PLANNING") return invalidTransition(ctx.res);
  p.lines = p.lines.filter((l) => l !== line);
  touch(p);
  noContent(ctx.res);
};

H.tenantSubmitProcurementRFQ = (ctx) => {
  const p = findProject(ctx.params.id);
  if (!p) return notFound(ctx.res);
  if (p.status !== "PLANNING") return invalidTransition(ctx.res);
  if (p.lines.length === 0) return badRequest(ctx.res, "project has no lines to quote");
  for (const line of p.lines) {
    const product = productById(line.product_id);
    if (product && QTY.test(line.qty_entered)) {
      line.indicative_unit_price_cents = product.unit;
      line.indicative_line_total_cents = lineTotal(line.qty_entered, product.unit);
      line.priced = true;
    }
  }
  p.status = "QUOTING";
  touch(p);
  ok(ctx.res, projectDetail(p));
};

// ---- handlers: job rollups, catalog, addresses ----------------------------------------------------------

H.tenantListProjectRollup = (ctx) => ok(ctx.res, { projects: [...world.jobs].sort((a, b) => (a.created_at < b.created_at ? 1 : -1)).map(jobSummary) });

H.tenantGetProjectRollup = (ctx) => {
  const job = world.jobs.find((j) => j.id === ctx.params.id);
  if (!job) return notFound(ctx.res);
  const summary = jobSummary(job);
  const orders = world.orders.filter((o) => world.jobs[o.project_job - 1] === job);
  const ids = new Set(orders.map((o) => o.id));
  const money = world.invoices.filter((i) => ids.has(i.order_id) && ["FINALIZED", "PARTIALLY_PAID", "PAID"].includes(i.status));
  const quoted = orders.filter((o) => world.quotes.some((q) => q.id === o.quote_id));
  const quotedCents = quoted.reduce((s, o) => s + world.quotes.find((q) => q.id === o.quote_id).total_cents, 0);
  const quotedSpend = money.filter((i) => quoted.some((o) => o.id === i.order_id)).reduce((s, i) => s + i.total_cents, 0);
  ok(ctx.res, {
    ...summary,
    quoted_cents: quotedCents,
    quoted_spend_cents: quotedSpend,
    variance_cents: quotedCents > 0 ? quotedSpend - quotedCents : 0,
    has_quote: quotedCents > 0,
    orders: [...orders].sort((a, b) => (a.created_at < b.created_at ? 1 : -1)).map((o) => ({ id: o.id, order_no: o.order_no, status: o.status, total_cents: o.total_cents })),
    invoices: money.map((i) => ({ id: i.id, invoice_no: i.invoice_no, status: i.status, total_cents: i.total_cents, balance_cents: i.balance_cents })),
  });
};

H.tenantSearchCatalog = (ctx) => {
  const q = (ctx.url.searchParams.get("q") ?? "").trim();
  if (q === "") return badRequest(ctx.res, "search query is required");
  let limit = Number.parseInt(ctx.url.searchParams.get("limit") ?? "", 10);
  if (!(limit > 0)) limit = 20;
  if (limit > 50) limit = 50;
  const needle = q.toLowerCase();
  const items = PRODUCTS.filter((p) => `${p.sku} ${p.name} ${p.category} ${p.manufacturer}`.toLowerCase().includes(needle))
    .slice(0, limit)
    .map((p) => ({ product_id: p.id, sku: p.sku, name: p.name, category: p.category, base_uom: p.base_uom }));
  ok(ctx.res, { items });
};

H.tenantGetCatalogProduct = (ctx) => {
  const p = productById(ctx.params.id);
  if (!p) return notFound(ctx.res);
  ok(ctx.res, {
    product_id: p.id,
    sku: p.sku,
    name: p.name,
    description: `${p.name} from ${p.manufacturer}`,
    manufacturer: p.manufacturer,
    collection: "",
    color: "",
    color_family: "",
    finish: "",
    texture: "",
    application: p.category,
    product_size: "",
    sold_by: p.base_uom,
    base_uom: p.base_uom,
    dimensions_lwh: "",
    full_skid_quantity: null,
    layer_quantity: null,
    pallet_count: null,
    minimum_order_qty: "1",
    is_special_order: false,
    is_active: true,
    lead_time_days: null,
    availability: [{ branch_id: BRANCH_ID, on_hand_base: 1200, in_stock: true }],
  });
};

H.tenantListAddresses = (ctx) => ok(ctx.res, { addresses: world.addresses });
H.tenantGetAddress = (ctx) => {
  const a = world.addresses.find((x) => x.id === ctx.params.id);
  if (!a) return notFound(ctx.res);
  ok(ctx.res, a);
};

// ---- the route table -----------------------------------------------------------------------------------
// [method, path, operationId, gate]: gate is "public", "tenant" (an active person) or a capability.

export const ROUTES = [
  ["GET", "/api/v1/tenant/config", "tenantGetConfig", "public"],
  ["GET", "/api/v1/tenant/me", "tenantGetMe", "tenant"],
  ["PUT", "/api/v1/tenant/me", "tenantUpdateMe", "tenant"],
  ["POST", "/api/v1/tenant/me/password", "tenantChangePassword", "tenant"],
  ["GET", "/api/v1/tenant/users", "tenantListUsers", "manage_users"],
  ["PUT", "/api/v1/tenant/users/{id}/role", "tenantUpdateUserRole", "manage_users"],
  ["PUT", "/api/v1/tenant/users/{id}/status", "tenantUpdateUserStatus", "manage_users"],
  ["GET", "/api/v1/tenant/invites", "tenantListInvites", "manage_users"],
  ["POST", "/api/v1/tenant/invites", "tenantCreateInvite", "manage_users"],
  ["GET", "/api/v1/tenant/dashboard", "tenantGetDashboard", "view_billing"],
  ["GET", "/api/v1/tenant/billing/summary", "tenantGetBillingSummary", "view_billing"],
  ["GET", "/api/v1/tenant/invoices", "tenantListInvoices", "view_billing"],
  ["GET", "/api/v1/tenant/invoices/{id}", "tenantGetInvoice", "view_billing"],
  ["GET", "/api/v1/tenant/orders", "tenantListOrders", "view_orders_deliveries"],
  ["GET", "/api/v1/tenant/orders/{id}", "tenantGetOrder", "view_orders_deliveries"],
  ["PUT", "/api/v1/tenant/orders/{id}/delivery-instructions", "tenantSetDeliveryInstructions", "edit_delivery_instructions"],
  ["GET", "/api/v1/tenant/orders/{id}/reorder-preview", "tenantGetReorderPreview", "view_orders_deliveries"],
  ["POST", "/api/v1/tenant/orders/{id}/reorder", "tenantCreateReorder", "create_orders"],
  ["GET", "/api/v1/tenant/orders/{id}/return-eligibility", "tenantGetReturnEligibility", "view_orders_deliveries"],
  ["POST", "/api/v1/tenant/orders/{id}/return-requests", "tenantCreateReturnRequest", "create_orders"],
  ["GET", "/api/v1/tenant/return-requests", "tenantListReturnRequests", "view_orders_deliveries"],
  ["GET", "/api/v1/tenant/quotes", "tenantListQuotes", "submit_rfq"],
  ["GET", "/api/v1/tenant/quotes/{id}", "tenantGetQuote", "submit_rfq"],
  ["POST", "/api/v1/tenant/quotes/{id}/accept", "tenantAcceptQuote", "submit_rfq"],
  ["POST", "/api/v1/tenant/quotes/{id}/reject", "tenantRejectQuote", "submit_rfq"],
  ["POST", "/api/v1/tenant/quotes/{id}/convert", "tenantConvertQuote", "create_orders"],
  ["GET", "/api/v1/tenant/payment-methods", "tenantListPaymentMethods", "manage_payment_methods"],
  ["POST", "/api/v1/tenant/payment-methods", "tenantAddPaymentMethod", "manage_payment_methods"],
  ["DELETE", "/api/v1/tenant/payment-methods/{id}", "tenantRemovePaymentMethod", "manage_payment_methods"],
  ["PUT", "/api/v1/tenant/payment-methods/{id}/default", "tenantSetDefaultPaymentMethod", "manage_payment_methods"],
  ["GET", "/api/v1/tenant/payments", "tenantListPayments", "view_billing"],
  ["POST", "/api/v1/tenant/payments", "tenantCreatePayment", "pay_invoices"],
  ["GET", "/api/v1/tenant/procurement/projects", "tenantListProcurementProjects", "submit_rfq"],
  ["POST", "/api/v1/tenant/procurement/projects", "tenantCreateProcurementProject", "submit_rfq"],
  ["GET", "/api/v1/tenant/procurement/projects/{id}", "tenantGetProcurementProject", "submit_rfq"],
  ["PUT", "/api/v1/tenant/procurement/projects/{id}", "tenantUpdateProcurementProject", "submit_rfq"],
  ["DELETE", "/api/v1/tenant/procurement/projects/{id}", "tenantDeleteProcurementProject", "submit_rfq"],
  ["POST", "/api/v1/tenant/procurement/projects/{id}/lines", "tenantAddProcurementLine", "submit_rfq"],
  ["PUT", "/api/v1/tenant/procurement/projects/{id}/lines/{lineId}", "tenantUpdateProcurementLine", "submit_rfq"],
  ["DELETE", "/api/v1/tenant/procurement/projects/{id}/lines/{lineId}", "tenantRemoveProcurementLine", "submit_rfq"],
  ["POST", "/api/v1/tenant/procurement/projects/{id}/submit-rfq", "tenantSubmitProcurementRFQ", "submit_rfq"],
  ["GET", "/api/v1/tenant/projects", "tenantListProjectRollup", "view_billing"],
  ["GET", "/api/v1/tenant/projects/{id}", "tenantGetProjectRollup", "view_billing"],
  ["GET", "/api/v1/tenant/catalog/search", "tenantSearchCatalog", "submit_rfq"],
  ["GET", "/api/v1/tenant/catalog/products/{id}", "tenantGetCatalogProduct", "submit_rfq"],
  ["GET", "/api/v1/tenant/addresses", "tenantListAddresses", "view_orders_deliveries"],
  ["GET", "/api/v1/tenant/addresses/{id}", "tenantGetAddress", "view_orders_deliveries"],
];

const compiled = ROUTES.map(([method, path, operationId, gate]) => {
  const names = [];
  const pattern = new RegExp("^" + path.replace(/\{(\w+)\}/g, (_, n) => (names.push(n), "([^/]+)")) + "$");
  return { method, pattern, names, operationId, gate };
});

function match(method, pathname) {
  let pathKnown = false;
  for (const route of compiled) {
    const m = route.pattern.exec(pathname);
    if (!m) continue;
    pathKnown = true;
    if (route.method !== method) continue;
    const params = {};
    route.names.forEach((name, i) => (params[name] = decodeURIComponent(m[i + 1])));
    return { route, params };
  }
  return { pathKnown };
}

async function handle(req, res) {
  const url = new URL(req.url, "http://stub.local");
  if (url.pathname === "/healthz") return ok(res, { ok: true });
  if (url.pathname === "/_stub/reset" && req.method === "POST") {
    world = createWorld();
    faults = [];
    delayMs = 0;
    return ok(res, { status: "ok" });
  }
  if (url.pathname === "/_stub/control" && req.method === "POST") return stubControl(req, res);
  // A test that starts the stub ends it by this door, so nothing ever signals the process; ROLE_STUB_IDLE_EXIT_MS is
  // the backstop for a test that dies before it can ask.
  if (url.pathname === "/_stub/exit" && req.method === "POST") {
    ok(res, { status: "ok" });
    return setImmediate(() => process.exit(0));
  }
  if (url.pathname === "/_stub/session" && req.method === "POST") return stubSession(req, res);
  // The demo build's two routes, in the core's shape (core/internal/demo/session.go): the closed flow table with each
  // row's class and role code, and one mint per named flow that answers a tenant class bearer for a contractor flow.
  if (url.pathname === "/api/v1/demo/flows" && req.method === "GET") return demoFlows(res);
  if (url.pathname === "/api/v1/demo/session" && req.method === "POST") return demoSession(req, res);

  const found = match(req.method, url.pathname);
  if (!found.route) {
    if (found.pathKnown) {
      res.writeHead(405, { "Content-Type": "text/plain; charset=utf-8" });
      return res.end("Method Not Allowed\n");
    }
    res.writeHead(404, { "Content-Type": "text/plain; charset=utf-8" });
    return res.end("404 page not found\n");
  }
  const { route, params } = found;
  const ctx = { req, res, url, params, user: null };
  if (route.gate !== "public") {
    const header = req.headers.authorization ?? "";
    if (!header.startsWith("Bearer ") || header.slice(7).trim() === "") return unauthorized(res);
    const who = userFor(header.slice(7).trim());
    if (who === null) return unauthorized(res);
    if (who.classOf !== "tenant") return forbidden(res);
    if (req.headers["x-org"] !== undefined) return refuse(res, 400, "header X-Org is not accepted on this route");
    if (who.user.status !== "active") return unauthorized(res);
    if (route.gate !== "tenant" && !capabilitiesOf(who.user.role)[route.gate]) return forbidden(res);
    ctx.user = who.user;
  }
  const fault = faults.find((f) => url.pathname.startsWith(f.path));
  if (fault) {
    if (fault.status === 401) return unauthorized(res);
    if (fault.status === 403) return forbidden(res);
    return refuse(res, fault.status, "internal error");
  }
  if (delayMs > 0) await new Promise((resolve) => setTimeout(resolve, delayMs));
  return H[route.operationId](ctx);
}

// POST /_stub/control {fault: {path, status}} | {delayMs} | {discount: {invoice_no, bps, days, taken_cents?}} | {clock: iso | null} | {empty: "orders" | "invoices" | "projects" | "quotes"}
async function stubControl(req, res) {
  const body = await readJson(req, res);
  if (body === undefined) return;
  if (body?.fault) faults.push({ path: String(body.fault.path), status: Number(body.fault.status) });
  if (typeof body?.delayMs === "number") delayMs = Math.max(0, Math.min(5000, body.delayMs));
  const collections = { orders: "orders", invoices: "invoices", projects: "projects", quotes: "quotes" };
  // {discount: {invoice_no, bps, days}}: the invoice carries discount terms whose last day is `days` from today.
  if (body?.discount) {
    const inv = world.invoices.find((i) => i.invoice_no === body.discount.invoice_no);
    if (inv) {
      inv.discount_bps = Number(body.discount.bps);
      inv.discount_by = houseDayPlus(Number(body.discount.days));
      inv.discount_taken_cents = Number(body.discount.taken_cents ?? 0);
    }
  }
  // {clock: iso | null}: pin the stub's clock (null releases it), so the house day can be judged at a chosen instant.
  if (body && "clock" in body) clockOverride = body.clock === null ? null : Date.parse(String(body.clock));
  if (body?.empty && collections[body.empty]) {
    world[collections[body.empty]] = [];
    world.recompute();
  }
  ok(res, { status: "ok" });
}

async function stubSession(req, res) {
  const body = await readJson(req, res);
  if (body === undefined) return;
  // The stub's persona door (the demo build's role picker, stub only): a role code alone names the persona, no password.
  const byRole = typeof body?.role === "string" && body.email === undefined;
  const persona = byRole ? Object.entries(PERSONAS).find(([, p]) => p.role === body.role) : Object.entries(PERSONAS).find(([, p]) => p.email === body?.email);
  if (!persona || (!byRole && body?.password !== STUB_PASSWORD)) return refuse(res, 401, "That email and password do not match.");
  const expiresAtMs = Date.now() + 3_600_000;
  const user = world.users.find((u) => u.email === persona[1].email);
  ok(res, { token: `pro-stub.${persona[0]}.${expiresAtMs}`, expires_at_ms: expiresAtMs, user: { id: user.id, email: user.email, name: user.name } });
}

const DEMO_FLOWS = [
  { flow: "sales", role_code: "inside_sales", label: "Sales", class: "staff" },
  { flow: "contractor-account-admin", role_code: "account_admin", label: "Contractor owner", class: "tenant", persona: "owner" },
  { flow: "contractor-buyer", role_code: "buyer", label: "Contractor buyer", class: "tenant", persona: "buyer" },
  { flow: "contractor-buyer-no-pay", role_code: "buyer_no_pay", label: "Contractor buyer (no pay)", class: "tenant", persona: "pm" },
  { flow: "contractor-ap-finance", role_code: "ap_finance", label: "Contractor AP", class: "tenant", persona: "ap" },
  { flow: "contractor-field-crew", role_code: "field_crew", label: "Contractor field crew", class: "tenant", persona: "field" },
];

function demoFlows(res) {
  ok(res, { flows: DEMO_FLOWS.map(({ flow, role_code, label, class: kind }) => ({ flow, role_code, label, class: kind })) });
}

async function demoSession(req, res) {
  const body = await readJson(req, res);
  if (body === undefined) return;
  const row = DEMO_FLOWS.find((f) => f.flow === body?.flow);
  if (!row) return refuse(res, 400, "demo: unknown flow: the entry page offers only the flows GET /api/v1/demo/flows lists");
  if (row.class !== "tenant") return refuse(res, 500, "demo: the stub mints contractor flows only");
  const persona = PERSONAS[row.persona];
  const user = world.users.find((u) => u.email === persona.email);
  const part = (o) => Buffer.from(JSON.stringify(o)).toString("base64url");
  const token = `${part({ alg: "none" })}.${part({ sub: user.id, exp: Math.floor((Date.now() + 3_600_000) / 1000), stub_persona: row.persona })}.stub`;
  ok(res, {
    token,
    user: { user_id: user.id, email: user.email, display_name: user.name, role_code: row.role_code, role_name: row.label },
    flow: row.flow,
    class: "tenant",
  });
}

export function startStub(port) {
  let idle;
  const idleMs = Number(process.env.ROLE_STUB_IDLE_EXIT_MS ?? 0);
  const arm = () => {
    if (idleMs <= 0) return;
    clearTimeout(idle);
    idle = setTimeout(() => process.exit(0), idleMs);
  };
  const server = createServer((req, res) => {
    arm();
    handle(req, res).catch(() => refuse(res, 500, "internal error"));
  });
  arm();
  // Port 0 asks the system for a free one; the caller reads the bound port from server.address().
  return new Promise((resolve) => server.listen(port, "127.0.0.1", () => resolve(server)));
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const port = Number(process.env.ROLE_STUB_PORT ?? 56810);
  const server = await startStub(port);
  console.log(`tenant-stub listening on ${server.address().port}`);
}
