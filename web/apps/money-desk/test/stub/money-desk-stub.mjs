// The Money Desk's local stub: the demo way in and the receivables routes the
// app calls, answering with the core's wire shapes and the core's words. It is
// a development and test double only; the real routes are the core's. The
// stub contract test (test/stub-contract.test.mjs) holds every answer here to
// the generated wire descriptors and every refusal sentence to the core's
// source, so the browser legs never lean on an answer the server would not give.
//
// The world is invented and small: five trade accounts and nine invoices, one
// of them in each aging bucket the landing draws, one payment already bounced.
// Dates are cut from the host's own day so no calendar literal is written.
import { createServer } from "node:http";
import { createGlWorld } from "./gl-world.mjs";
import { createBankrecWorld } from "./bankrec-world.mjs";
import { createLienWorld } from "./lien-world.mjs";

// The ledger slice's world (approvals, journals, trial balance), answered by its own module.
const glWorld = createGlWorld();
// The bank reconciliation slice's world (accounts, statement lines, matches, rules, months), answered by its own module.
const bankrecWorld = createBankrecWorld();
// The liens and compliance slice's world (jobs, the holdback ledger, documents, the calendar, the roster), answered by its own module.
const lienWorld = createLienWorld();

const port = Number(process.env.ROLE_STUB_PORT ?? 4959);

const BRANCH = "b0000000-0000-4000-8000-000000000001";
// The core's closed table, in its order: the till's pair, the Yard manager, the Yard Team and the
// Driver, Payables and Receivables, the Procurement manager, then the Money Desk's controller. The
// table is listed whole so the way in's filter and the cross app card legs mean something against it. The
// stub mints a session for the Sales pair, Payables and Receivables, the Procurement manager and the
// Financial Controller; the yard manager, the Yard Team and the Driver are rows only, and the mint answers
// them 404.
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

const BRANCHES = [
  { id: "b0000000-0000-4000-8000-000000000001", code: "PLM", name: "Point Loma (demo)", address: { line1: "1 Demo Road", city: "Point Loma", province: "CA", postal: "92106", country: "US" }, is_default: true, is_system_default: false, is_active: true },
  { id: "b0000000-0000-4000-8000-000000000002", code: "JUL", name: "Julian (demo)", address: { line1: "2 Demo Road", city: "Julian", province: "CA", postal: "92036", country: "US" }, is_default: false, is_system_default: false, is_active: true },
];
const branchOf = (n) => (n <= 3 ? BRANCHES[0].id : BRANCHES[1].id);

const USERS = {
  sales: {
    user_id: "de110000-0000-4000-9000-000000000001",
    email: "sales@hh-demo.invalid",
    display_name: "Sales (demo)",
    role_code: "inside_sales",
    role_name: "Inside Sales",
    tier: 2,
    scope: "BRANCH",
  },
  "sales-manager": {
    user_id: "de110000-0000-4000-9000-000000000002",
    email: "sales.manager@hh-demo.invalid",
    display_name: "Sales manager (demo)",
    role_code: "sales_manager",
    role_name: "Sales Manager",
    tier: 2,
    scope: "BRANCH",
  },
  "payables-receivables": {
    user_id: "de110000-0000-4000-9000-000000000007",
    email: "payables.receivables@hh-demo.invalid",
    display_name: "Payables and Receivables (demo)",
    role_code: "payables_receivables",
    role_name: "Payables and Receivables",
    tier: 2,
    scope: "GLOBAL",
  },
  "procurement-manager": {
    user_id: "de64b000-0000-4000-9000-000000000001",
    email: "procurement.manager@hh-demo.invalid",
    display_name: "Procurement manager (demo)",
    role_code: "procurement_manager",
    role_name: "Procurement manager",
    tier: 2,
    scope: "BRANCH",
  },
  "financial-controller": {
    user_id: "de110000-0000-4000-9000-000000000008",
    email: "financial.controller@hh-demo.invalid",
    display_name: "Financial Controller (demo)",
    role_code: "financial_controller",
    role_name: "Financial Controller",
    tier: 1,
    scope: "GLOBAL",
  },
};

// The controller's id rides the token's sub; the walled moves read it from there.
function isController(req) {
  const token = (req.headers.authorization ?? "").replace(/^Bearer /, "");
  try {
    const payload = JSON.parse(Buffer.from(token.split(".")[1] ?? "", "base64url").toString("utf8"));
    return payload.sub === USERS["financial-controller"].user_id;
  } catch {
    return false;
  }
}

// A token with the shape the app reads an expiry from: three parts, the
// middle one base64url JSON carrying sub and exp. The signature is a stub.
function mintToken(sub) {
  const part = (value) => Buffer.from(JSON.stringify(value), "utf8").toString("base64url");
  const issuedAtSeconds = Math.floor(Date.now() / 1000);
  const payload = { iss: "hh-demo-stub", sub, aud: "staff", iat: issuedAtSeconds, exp: issuedAtSeconds + 3600 };
  return `${part({ alg: "ES256", typ: "JWT" })}.${part(payload)}.c3R1Yi1zaWduYXR1cmUtYnl0ZXM`;
}

function dayOffset(offset) {
  const at = new Date();
  at.setUTCDate(at.getUTCDate() + offset);
  return new Intl.DateTimeFormat("en-CA", { timeZone: "America/Toronto", year: "numeric", month: "2-digit", day: "2-digit" }).format(at);
}

function uuid(kind, n) {
  return `${kind}0000000-0000-4000-8000-${String(n).padStart(12, "0")}`;
}

const NOW_ISO = () => new Date().toISOString();

// ---- the world ---------------------------------------------------------------------------------

const ACCOUNTS = [
  { n: 1, name: "Granite Edge Landscaping (demo)", no: "GE-100", terms: 30, limit: 5_000_000 },
  { n: 2, name: "Oakline Builders (demo)", no: "OB-200", terms: 30, limit: 3_000_000 },
  { n: 3, name: "Riverstone Paving (demo)", no: "RP-300", terms: 45, limit: 2_500_000 },
  { n: 4, name: "Summit Patio Co (demo)", no: "SP-400", terms: 30, limit: 1_500_000 },
  { n: 5, name: "Cedar Ridge Contractors (demo)", no: "CR-500", terms: 30, limit: 4_000_000 },
];

// One row per invoice: account, how many days past due (negative is not yet due), the total, and the
// legally retained holdback (the core's walk pays balance less holdback, so one invoice carries one).
const INVOICES = [
  { n: 1, acct: 1, late: 104, total: 482_500 },
  { n: 2, acct: 1, late: 12, total: 96_000 },
  { n: 3, acct: 2, late: 75, total: 215_000 },
  { n: 4, acct: 2, late: 41, total: 130_500 },
  { n: 5, acct: 3, late: 18, total: 88_750 },
  { n: 6, acct: 3, late: -9, total: 154_000 },
  { n: 7, acct: 4, late: 5, total: 62_300 },
  { n: 8, acct: 5, late: -20, total: 301_000, holdback: 30_000 },
  { n: 9, acct: 5, late: 33, total: 74_900 },
  { n: 10, acct: 1, late: 171, total: 231_640, paid: true },
];

const NSF_FEE = 3500;

// The wide world's extra invoices: 99 open $10.00 invoices on the first account, so a Pick all open
// names 101 invoices and the pick ceiling leg can draw the one body the app can build that the core's
// validateApply refuses ahead of its replay judgement ("a received payment may name at most 100
// invoices"). The full world's accounts carry too few to draw it.
const WIDE_EXTRA = Array.from({ length: 99 }, (_, i) => ({ n: 101 + i, acct: 1, late: -3, total: 1_000 }));

let world;

// The payments writer's park: the next real apply's ANSWER is held for the asked window, the
// shape of a payments writer (a till payment settling under the payments counter) that a desk's
// preview must not wait behind. A preview never touches it: the final route's preview draws no
// counter and no row lock (core/internal/payments received_apply.go previewApply, item 127), so
// the desk's typing answers at once while a writer is mid flight.
let parkedApplyMs = 0;

function reset(kind = "full") {
  parkedApplyMs = 0;
  const source = kind === "wide" ? [...INVOICES, ...WIDE_EXTRA] : INVOICES;
  world = {
    empty: kind === "empty",
    invoices: source.map((i) => ({
      id: uuid("a", i.n),
      invoice_no: `INV-${1000 + i.n}`,
      acct: i.acct,
      late: i.late,
      total_cents: i.total,
      balance_cents: i.paid === true ? 0 : i.total,
      holdback_cents: i.holdback ?? 0,
      promise: null,
      disputed: i.n === 4,
      payments: [],
    })),
    payments: [],
    holds: [],
    statements: new Map(),
    keys: new Map(),
    counter: 0,
  };
  if (kind === "empty") {
    world.invoices = [];
    return;
  }
  world.invoices[0].promise = {
    id: uuid("f", 1),
    promised_cents: 100_000,
    promised_date: dayOffset(7),
    status: "OPEN",
    promised_by: "Accounts payable",
    note: "Cheque is being cut",
    taken_at: NOW_ISO(),
  };
  // One payment already bounced: it reopened the second invoice of the first account.
  const bounced = {
    id: uuid("c", 1),
    no: "PAY-0001",
    acct: 1,
    method: "CHEQUE",
    amount_cents: 96_000,
    status: "BOUNCED",
    reason: "Cheque returned NSF",
    received_at: NOW_ISO(),
    bounced_at: NOW_ISO(),
    allocations: [{ invoice_n: 2, amount_cents: 96_000 }],
  };
  world.payments.push(bounced);
  const hold = (n, cust, kind, auto, reason, daysAgo, extra = {}) => ({
    id: uuid("d", n),
    customer_id: custId(cust),
    status: "ACTIVE",
    hold_kind: kind,
    auto_placed: auto,
    reason,
    created_at: new Date(Date.now() - daysAgo * 86_400_000).toISOString(),
    ...extra,
  });
  world.holds.push(
    hold(1, 1, "NSF", true, "Payment PAY-0001 came back from the bank", 2),
    hold(2, 2, "AR_AGING", true, "Receivables are more than 90 days past due", 6),
    hold(3, 5, "CREDIT_LIMIT", false, "Balance is over the credit limit; override until the deposit clears", 1, {
      override_until: new Date(Date.now() + 5 * 86_400_000).toISOString(),
      override_reason: "Deposit is coming",
    }),
  );
}

const acct = (n) => ACCOUNTS.find((a) => a.n === n);
const custId = (n) => uuid("e", n);
const acctOfCust = (id) => ACCOUNTS.find((a) => custId(a.n) === id);
const invById = (id) => world.invoices.find((i) => i.id === id);
reset();

function statusOf(inv) {
  if (inv.balance_cents === 0) return "PAID";
  if (inv.disputed === true) return "DISPUTED";
  const held = world.holds.some((h) => h.status === "ACTIVE" && h.customer_id === custId(inv.acct));
  if (inv.late > 0) return "OVERDUE";
  if (held) return "CREDIT_HOLD";
  if (inv.late >= -7) return "DUE_SOON";
  return inv.balance_cents < inv.total_cents ? "PARTIALLY_PAID" : "OPEN";
}

function accountStatus(n) {
  return world.holds.some((h) => h.status === "ACTIVE" && h.customer_id === custId(n)) ? "CREDIT_HOLD" : "CURRENT";
}

function position(n) {
  const open = world.invoices.filter((i) => i.acct === n);
  const open_balance_cents = open.reduce((s, i) => s + i.balance_cents, 0);
  const overdue_cents = open.filter((i) => i.late > 0).reduce((s, i) => s + i.balance_cents, 0);
  const credit_limit_cents = acct(n).limit;
  return { open_balance_cents, credit_limit_cents, available_credit_cents: credit_limit_cents - open_balance_cents, overdue_cents };
}

function registerRow(inv) {
  const a = acct(inv.acct);
  const row = {
    id: inv.id,
    invoice_no: inv.invoice_no,
    doc_type: "INVOICE",
    customer_id: custId(a.n),
    customer_name: a.name,
    account_no: a.no,
    branch_id: branchOf(a.n),
    order_no: `SO-${2000 + invNumber(inv)}`,
    issue_date: dayOffset(-inv.late - a.terms),
    due_date: dayOffset(-inv.late),
    total_cents: inv.total_cents,
    balance_cents: inv.balance_cents,
    stored_status: inv.balance_cents === 0 ? "PAID" : inv.balance_cents < inv.total_cents ? "PARTIALLY_PAID" : "FINALIZED",
    status: statusOf(inv),
    days_overdue: Math.max(inv.late, 0),
    account_status: accountStatus(a.n),
    disputed: inv.disputed === true,
    account: position(a.n),
  };
  if (inv.holdback_cents > 0) row.holdback_cents = inv.holdback_cents;
  if (inv.promise !== null) {
    row.promised_date = inv.promise.promised_date;
    row.promised_cents = inv.promise.promised_cents;
  }
  return row;
}

function bucketOf(late) {
  if (late <= 0) return "current_cents";
  if (late <= 30) return "days_30_cents";
  if (late <= 60) return "days_60_cents";
  return "days_90_plus_cents";
}

function agingRows() {
  return ACCOUNTS.map((a) => {
    const row = { current_cents: 0, days_30_cents: 0, days_60_cents: 0, days_90_plus_cents: 0 };
    for (const inv of world.invoices.filter((i) => i.acct === a.n)) row[bucketOf(inv.late)] += inv.balance_cents;
    const total = row.current_cents + row.days_30_cents + row.days_60_cents + row.days_90_plus_cents;
    const held = world.holds.find((h) => h.status === "ACTIVE" && h.customer_id === custId(a.n));
    const out = {
      customer_id: custId(a.n),
      customer_name: a.name,
      account_no: a.no,
      branch_id: branchOf(a.n),
      payment_terms_days: a.terms,
      credit_limit_cents: a.limit,
      ...row,
      total_cents: total,
      past_bucket_cents: total - row.current_cents,
      on_hold: held !== undefined,
    };
    if (held !== undefined) out.hold_kind = held.hold_kind;
    return out;
  }).filter((r) => r.total_cents > 0);
}

function landing() {
  const bucketFor = (late) => (late <= 0 ? "CURRENT" : late <= 60 ? "WARN" : late < 90 ? "LATE" : "ESCALATION");
  const spec = [
    ["card.escalation", "ESCALATION", 90, 0],
    ["card.late", "LATE", 61, 89],
    ["card.warn", "WARN", 1, 60],
    ["card.current", "CURRENT", 0, 0],
  ];
  const open = world.invoices.filter((i) => i.balance_cents > 0);
  const cards = spec.map(([id, bucket, from, to]) => {
    const mine = open.filter((i) => bucketFor(i.late) === bucket);
    const accounts = [...new Set(mine.map((i) => custId(i.acct)))].sort();
    const card = {
      id,
      bucket,
      from_days: from,
      accounts: accounts.length,
      invoices: mine.length,
      total_cents: mine.reduce((s, i) => s + i.balance_cents, 0),
      account_ids: accounts,
    };
    if (to !== 0) card.to_days = to;
    return card;
  });
  const escalated = open.filter((i) => bucketFor(i.late) === "ESCALATION");
  const queue = [...new Set(escalated.map((i) => i.acct))].map((n) => {
    const mine = escalated.filter((i) => i.acct === n);
    return {
      customer_id: custId(n),
      customer_name: acct(n).name,
      account_no: acct(n).no,
      oldest_days_past_due: Math.max(...mine.map((i) => i.late)),
      invoices: mine.length,
      balance_cents: mine.reduce((s, i) => s + i.balance_cents, 0),
      on_hold: world.holds.some((h) => h.status === "ACTIVE" && h.customer_id === custId(n)),
    };
  });
  return {
    as_of: dayOffset(0),
    policy: {
      aging_warn_days: 60,
      aging_escalate_days: 90,
      reminder_cadence_days: 7,
      reminder_max_per_cycle: 3,
      black_hole_window_days: 45,
      black_hole_min_sends: 3,
      reminder_email_enabled: true,
      reminder_sms_enabled: false,
      untagged_fallback: "CONFIRM",
      ar_tier: "BASE",
    },
    cards,
    escalation_queue: queue,
    paper_path: world.empty ? [] : [
      {
        flag_id: uuid("6", 1),
        customer_id: custId(4),
        customer_name: acct(4).name,
        account_no: acct(4).no,
        delivered_sends: 3,
        window_days: 45,
        raised_at: NOW_ISO(),
      },
    ],
    capabilities: {},
  };
}

function detail(inv) {
  const a = acct(inv.acct);
  const row = registerRow(inv);
  const activity = [{ kind: "ISSUED", at: NOW_ISO(), detail: `Invoice ${inv.invoice_no} issued` }];
  for (const p of inv.payments) activity.push({ kind: "PAYMENT", at: p.at, amount_cents: p.amount_cents, detail: `Payment ${p.no}` });
  if (inv.promise !== null) activity.push({ kind: "PROMISE", at: inv.promise.taken_at, amount_cents: inv.promise.promised_cents, detail: `Promised for ${inv.promise.promised_date}` });
  const out = {
    id: inv.id,
    invoice_no: inv.invoice_no,
    doc_type: "INVOICE",
    stored_status: row.stored_status,
    status: row.status,
    days_overdue: row.days_overdue,
    issue_date: row.issue_date,
    due_date: row.due_date,
    branch_id: branchOf(a.n),
    customer: {
      customer_id: custId(a.n),
      name: a.name,
      account_no: a.no,
      terms_days: a.terms,
      account_status: accountStatus(a.n),
      account: position(a.n),
    },
    lines: [
      {
        line_no: 1,
        description: "Materials and delivery",
        qty_entered: "1",
        uom_entered: "lot",
        unit_price_cents: inv.total_cents,
        line_total_cents: inv.total_cents,
      },
    ],
    totals: {
      subtotal_cents: inv.total_cents,
      tax_cents: 0,
      total_cents: inv.total_cents,
      applied_cents: inv.total_cents - inv.balance_cents,
      holdback_cents: inv.holdback_cents,
      balance_cents: inv.balance_cents,
      payable_cents: Math.max(inv.balance_cents - inv.holdback_cents, 0),
    },
    activity,
    reminder_tones: ["FRIENDLY", "FIRM", "FINAL"],
    as_of: dayOffset(0),
  };
  if (inv.promise !== null) out.promise = inv.promise;
  if (inv.disputed === true) {
    out.dispute = {
      id: uuid("7", inv.n ?? 1),
      status: "OPEN",
      reason: "SHORT_DELIVERY",
      note: "Two pallets were short on the drop",
      amount_cents: 20_000,
      raised_at: NOW_ISO(),
    };
  }
  out.order_no = `SO-${2000 + invNumber(inv)}`;
  return out;
}

function invoicePayments(inv) {
  const items = world.payments
    .filter((p) => p.allocations.some((al) => al.invoice_n === invNumber(inv)))
    .map((p) => {
      const al = p.allocations.find((x) => x.invoice_n === invNumber(inv));
      return {
        payment_id: p.id,
        payment_no: p.no,
        method: p.method,
        status: p.status,
        amount_cents: al.amount_cents,
        payment_amount_cents: p.amount_cents,
        allocated_at: p.received_at,
        received_at: p.received_at,
      };
    });
  return { invoice_id: inv.id, invoice_no: inv.invoice_no, branch_id: BRANCH, items };
}

const invNumber = (inv) => Number(inv.id.slice(-12));

function bouncedPage() {
  const items = world.payments
    .filter((p) => p.status === "BOUNCED")
    .map((p) => ({
      payment_id: p.id,
      payment_no: p.no,
      customer_id: custId(p.acct),
      branch_id: BRANCH,
      method: p.method,
      amount_cents: p.amount_cents,
      reason: p.reason,
      fee_cents: NSF_FEE,
      fee_taxable: false,
      bounced_at: p.bounced_at,
    }));
  return { items };
}

function history(p) {
  return {
    payment_id: p.id,
    payment_no: p.no,
    customer_id: custId(p.acct),
    branch_id: BRANCH,
    method: p.method,
    status: p.status,
    amount_cents: p.amount_cents,
    received_at: p.received_at,
    reversed: p.status === "BOUNCED",
    allocations: p.allocations.map((al) => {
      const inv = world.invoices.find((i) => invNumber(i) === al.invoice_n);
      return {
        invoice_id: inv.id,
        invoice_no: inv.invoice_no,
        amount_cents: al.amount_cents,
        allocated_at: p.received_at,
        reversed_by_nsf: p.status === "BOUNCED",
      };
    }),
  };
}

function holdsPage(status) {
  const items = world.holds
    .filter((h) => status === "" || h.status === status)
    .map((h) => ({ ...h, customer_name: acctOfCust(h.customer_id)?.name }));
  return { items };
}

// ---- the server ------------------------------------------------------------------------------

function send(res, status, data) {
  res.writeHead(status, { "Content-Type": "application/json" });
  res.end(JSON.stringify(data));
}
const refuse = (res, status, error) => send(res, status, { error });

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

async function handle(req, res) {
  const url = new URL(req.url, `http://127.0.0.1:${port}`);
  const path = url.pathname;
  const q = url.searchParams;
  const method = req.method;

  if (path === "/healthz") return send(res, 200, { ok: true });
  if (method === "POST" && path === "/_stub/reset") {
    reset();
    glWorld.reset();
    bankrecWorld.reset();
    lienWorld.reset();
    return send(res, 200, { ok: true });
  }
  if (method === "POST" && path === "/_stub/scenario") {
    const body = await readBody(req);
    reset(body?.name === "empty" ? "empty" : body?.name === "wide" ? "wide" : "full");
    return send(res, 200, { ok: true });
  }
  if (method === "POST" && path === "/_stub/park-apply") {
    const body = await readBody(req);
    parkedApplyMs = Math.max(0, Number(body?.ms ?? 0));
    return send(res, 200, { ok: true, parkedApplyMs });
  }
  if (path === "/api/v1/demo/flows") return send(res, 200, { flows: FLOWS });
  if (method === "POST" && path === "/api/v1/demo/session") {
    const body = await readBody(req);
    const account = USERS[body?.flow];
    if (!account) return refuse(res, 404, "not found");
    return send(res, 200, { token: mintToken(account.user_id), user: account, flow: body.flow });
  }
  if (method === "GET" && path === "/api/v1/branches") return send(res, 200, BRANCHES);
  if (path.startsWith("/api/v1/staff/approvals") || path.startsWith("/api/v1/gl/")) {
    if ((req.headers.authorization ?? "") === "") return refuse(res, 401, "Unauthorized");
    const body = method === "POST" ? await readBody(req) : null;
    const answer = glWorld.handle(req, url, JSON.stringify(body ?? {}));
    return answer === null ? refuse(res, 404, "not found") : send(res, answer.status, answer.body);
  }
  if (path.startsWith("/api/v1/bankrec/")) {
    if ((req.headers.authorization ?? "") === "") return refuse(res, 401, "Unauthorized");
    const body = method === "POST" ? await readBody(req) : null;
    const answer = bankrecWorld.handle(req, url, JSON.stringify(body ?? {}));
    return answer === null ? refuse(res, 404, "not found") : send(res, answer.status, answer.body);
  }
  if (path.startsWith("/api/v1/lien/") || path.startsWith("/api/v1/compliance/") || path === "/api/v1/staff") {
    if ((req.headers.authorization ?? "") === "") return refuse(res, 401, "Unauthorized");
    const body = method === "POST" || method === "PUT" ? await readBody(req) : null;
    if (body === null && (method === "POST" || method === "PUT")) return refuse(res, 400, "invalid_json");
    const answer = lienWorld.handle(req, url, JSON.stringify(body ?? {}));
    return answer === null ? refuse(res, 404, "not found") : send(res, answer.status, answer.body);
  }
  if (!path.startsWith("/api/v1/ar/")) return refuse(res, 404, "not found");
  if ((req.headers.authorization ?? "") === "") return refuse(res, 401, "Unauthorized");

  // The received payment route (core/internal/billing register_service.go ApplyReceivedPayment and
  // core/internal/payments received_apply.go), its checks in the core's order and its sentences word for word:
  // the service's three checks, then the request's, then the replay judgement (never on a preview), then the
  // locks in id order (a missing or another account's invoice is a 404, one not open is refused), then one
  // branch per payment, then the ceiling. Every refusal is a 422 but the 404. The split is the server's: oldest
  // first by issue date, due date, invoice number and id whatever order the list came in, one line per named
  // invoice, unapplied always zero. A preview takes the same walk and writes nothing, and never reads the key.
  if (method === "POST" && path === "/api/v1/ar/payments/apply") {
    const body = await readBody(req);
    if (body === null) return refuse(res, 422, "the request body must be JSON");
    const isUuid = (v) => typeof v === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(v.trim());
    const key = typeof body.idempotency_key === "string" ? body.idempotency_key : "";
    const ids = Array.isArray(body.invoice_ids) ? body.invoice_ids : [];
    const method_ = typeof body.method === "string" ? body.method.trim().toUpperCase() : "";
    const preview = body.preview === true;
    if (key === "") return refuse(res, 422, "recording a payment needs an idempotency key");
    if (!Number.isInteger(body.amount_cents) || body.amount_cents <= 0) return refuse(res, 422, "a payment amount must be greater than zero");
    if (ids.length === 0) return refuse(res, 422, "a payment must be applied to at least one invoice");
    if (!["EFT", "CHEQUE", "CARD", "CASH"].includes(method_)) return refuse(res, 422, "tender must be one of EFT, CHEQUE, CARD, CASH");
    if (!isUuid(body.customer_id)) return refuse(res, 422, "customer id must be a uuid");
    if (ids.length > 100) return refuse(res, 422, "a received payment may name at most 100 invoices; record it as more than one payment");
    const seen = new Set();
    for (const raw of ids) {
      if (!isUuid(raw)) return refuse(res, 422, "invoice ids must be uuids");
      const id = raw.trim().toLowerCase();
      if (seen.has(id)) return refuse(res, 422, `the same invoice is named twice: ${id}`);
      seen.add(id);
    }
    const named = [...seen].sort();
    const customerId = body.customer_id.trim().toLowerCase();
    const reference = typeof body.reference === "string" ? body.reference : "";
    const oldestFirst = (rows) =>
      [...rows].sort((a, b) => {
        const ra = registerRow(a);
        const rb = registerRow(b);
        return ra.issue_date.localeCompare(rb.issue_date) || ra.due_date.localeCompare(rb.due_date) || a.invoice_no.localeCompare(b.invoice_no) || a.id.localeCompare(b.id);
      });
    // The core's walk (received_apply.go allocateOldestFirst): each invoice's payable is its balance
    // less the retained holdback, floored at zero, and that payable is both what a payment can take
    // and what the ceiling counts; an invoice whose retained holdback keeps a balance stays PART
    // however much of its payable it paid, and stays_open is the balance's payable remainder.
    const payableOf = (inv) => Math.max(inv.balance_cents - inv.holdback_cents, 0);
    const lineFor = (inv, take) => {
      const after = inv.balance_cents - take;
      return {
        invoice_id: inv.id,
        invoice_no: inv.invoice_no,
        applied_cents: take,
        outcome: take > 0 && after <= 0 ? "CLOSES" : take > 0 ? "PART" : "NONE",
        balance_after_cents: after,
        stays_open_cents: Math.max(after - inv.holdback_cents, 0),
      };
    };
    if (!preview) {
      const first = world.keys.get(`apply:${key}`);
      if (first !== undefined) {
        if (first.fingerprint.customer !== customerId) return refuse(res, 422, "that idempotency key already recorded a payment for another account; use a new key");
        const same =
          first.fingerprint.amount === body.amount_cents && first.fingerprint.method === method_ && first.fingerprint.reference === reference && first.fingerprint.named.join(",") === named.join(",");
        if (!same) return refuse(res, 422, "that idempotency key already recorded a different payment; use a new key");
        const lines = oldestFirst(first.invoices.map((id) => invById(id))).map((inv) => {
          const line = lineFor(inv, first.applied.get(inv.id) ?? 0);
          return { ...line, outcome: line.applied_cents > 0 ? (inv.balance_cents === 0 ? "CLOSES" : "PART") : "NONE", balance_after_cents: inv.balance_cents, stays_open_cents: payableOf(inv) };
        });
        return send(res, 200, { ...first.answer, lines, replayed: true });
      }
    }
    const picked = [];
    for (const id of named) {
      const inv = invById(id);
      if (inv === undefined || custId(inv.acct) !== customerId) return refuse(res, 404, `no such invoice: ${id}`);
      if (inv.balance_cents === 0) return refuse(res, 422, `invoice ${inv.invoice_no} (paid) is not open; a payment applies to a finalized or partially paid invoice`);
      picked.push(inv);
    }
    if (new Set(picked.map((inv) => branchOf(inv.acct))).size > 1) return refuse(res, 422, "one payment applies to one branch's invoices; record a payment per branch");
    const capacity = picked.reduce((t, inv) => t + payableOf(inv), 0);
    if (body.amount_cents > capacity) return refuse(res, 422, `the selected invoices can take at most ${capacity} cents right now; refresh and retry`);
    let left = body.amount_cents;
    const lines = oldestFirst(picked).map((inv) => {
      const take = Math.min(left, payableOf(inv));
      left -= take;
      return lineFor(inv, take);
    });
    const answer = { preview, amount_cents: body.amount_cents, applied_cents: body.amount_cents - left, unapplied_cents: left, lines, replayed: false };
    if (preview) return send(res, 200, answer);
    // The parked writer answers late, after every refusal walk has run: the park is a writer
    // mid flight (holding the payments counter in the core's terms), never a change in the
    // route's judgements, and a preview in this window answers ahead of it (see parkedApplyMs).
    if (parkedApplyMs > 0) {
      const ms = parkedApplyMs;
      parkedApplyMs = 0;
      await new Promise((resolve) => setTimeout(resolve, ms));
    }
    world.counter += 1;
    const no = `PAY-${String(100 + world.counter).padStart(4, "0")}`;
    const payment = {
      id: uuid("c", 100 + world.counter),
      no,
      acct: picked[0].acct,
      method: method_,
      amount_cents: body.amount_cents,
      status: "RECEIVED",
      reason: "",
      received_at: NOW_ISO(),
      bounced_at: "",
      allocations: lines.filter((l) => l.applied_cents > 0).map((l) => ({ invoice_n: invNumber(invById(l.invoice_id)), amount_cents: l.applied_cents })),
    };
    world.payments.push(payment);
    for (const l of lines) invById(l.invoice_id).balance_cents = l.balance_after_cents;
    const done = { ...answer, payment_id: payment.id, payment_no: no };
    world.keys.set(`apply:${key}`, {
      answer: done,
      invoices: lines.filter((l) => l.applied_cents > 0).map((l) => l.invoice_id),
      applied: new Map(lines.map((l) => [l.invoice_id, l.applied_cents])),
      fingerprint: { customer: customerId, amount: body.amount_cents, method: method_, reference, named },
    });
    return send(res, 200, done);
  }

  if (method === "GET" && path === "/api/v1/ar/landing") return send(res, 200, landing());
  if (method === "GET" && path === "/api/v1/ar/aging") return send(res, 200, { items: agingRows(), as_of: dayOffset(0) });

  if (method === "GET" && path === "/api/v1/ar/register") {
    const customer = q.get("customer_id") ?? "";
    const rows = world.invoices
      .filter((i) => customer === "" || custId(i.acct) === customer)
      .map(registerRow)
      .filter((r) => r.balance_cents > 0 || q.get("chip") === "PAID");
    const chip = q.get("chip") ?? "";
    const shown = chip === "" || chip === "PAID" ? rows : rows.filter((r) => r.status === chip);
    const count = (s) => rows.filter((r) => r.status === s).length;
    return send(res, 200, {
      items: shown,
      counts: { open: count("OPEN"), due_soon: count("DUE_SOON"), overdue: count("OVERDUE"), disputed: 0, paid: 0, credits: 0 },
      as_of: dayOffset(0),
    });
  }

  let m = path.match(/^\/api\/v1\/ar\/register\/invoices\/([^/]+)$/);
  if (m && method === "GET") {
    const inv = invById(m[1]);
    return inv ? send(res, 200, detail(inv)) : refuse(res, 404, "not found");
  }

  m = path.match(/^\/api\/v1\/ar\/register\/invoices\/([^/]+)\/payments$/);
  if (m && method === "POST") {
    const inv = invById(m[1]);
    if (!inv) return refuse(res, 404, "not found");
    const body = await readBody(req);
    if (body === null) return refuse(res, 422, "the request body must be JSON");
    if (typeof body.idempotency_key !== "string" || body.idempotency_key === "") return refuse(res, 400, "recording a payment needs an idempotency key");
    if (!Number.isInteger(body.amount_cents) || body.amount_cents <= 0) return refuse(res, 400, "a payment amount must be greater than zero");
    const replay = world.keys.get(body.idempotency_key);
    if (replay !== undefined) return send(res, 200, { ...replay, replayed: true, balance_cents: inv.balance_cents });
    if (body.amount_cents > inv.balance_cents) return refuse(res, 400, `this invoice can take at most ${inv.balance_cents} cents right now; refresh and retry`);
    world.counter += 1;
    const no = `PAY-${String(100 + world.counter).padStart(4, "0")}`;
    const payment = {
      id: uuid("c", 100 + world.counter),
      no,
      acct: inv.acct,
      method: body.method,
      amount_cents: body.amount_cents,
      status: "RECEIVED",
      reason: "",
      received_at: NOW_ISO(),
      bounced_at: "",
      allocations: [{ invoice_n: invNumber(inv), amount_cents: body.amount_cents }],
    };
    world.payments.push(payment);
    inv.payments.push({ no, amount_cents: body.amount_cents, at: payment.received_at });
    inv.balance_cents -= body.amount_cents;
    const result = { payment_id: payment.id, payment_no: no, amount_cents: body.amount_cents, balance_cents: inv.balance_cents, status: "RECEIVED", replayed: false };
    world.keys.set(body.idempotency_key, result);
    return send(res, 200, result);
  }

  m = path.match(/^\/api\/v1\/ar\/register\/invoices\/([^/]+)\/promise$/);
  if (m && method === "POST") {
    const inv = invById(m[1]);
    if (!inv) return refuse(res, 404, "not found");
    const body = await readBody(req);
    if (body === null) return refuse(res, 422, "the request body must be JSON");
    if (typeof body.idempotency_key !== "string" || body.idempotency_key === "") return refuse(res, 400, "recording a promise needs an idempotency key");
    if (!Number.isInteger(body.promised_cents) || body.promised_cents <= 0) return refuse(res, 400, "a promise names an amount greater than zero");
    if (!/^\d{4}-\d{2}-\d{2}$/.test(String(body.promised_date))) return refuse(res, 400, "the promised date must be a calendar date (YYYY-MM-DD)");
    if (body.promised_date < dayOffset(0)) return refuse(res, 400, "the promised date is already past");
    if (body.promised_cents > inv.balance_cents) return refuse(res, 400, "a promise cannot exceed the outstanding balance");
    if (inv.promise !== null) {
      return send(res, 409, { error: "this invoice already carries a live promise; settle it before recording another", promise: inv.promise });
    }
    world.counter += 1;
    inv.promise = {
      id: uuid("f", world.counter),
      promised_cents: body.promised_cents,
      promised_date: body.promised_date,
      status: "OPEN",
      promised_by: body.promised_by,
      note: body.note,
      taken_at: NOW_ISO(),
    };
    return send(res, 200, inv.promise);
  }

  m = path.match(/^\/api\/v1\/ar\/invoices\/([^/]+)\/payments$/);
  if (m && method === "GET") {
    const inv = invById(m[1]);
    return inv ? send(res, 200, invoicePayments(inv)) : refuse(res, 404, "not found");
  }

  if (method === "GET" && path === "/api/v1/ar/bounced") return send(res, 200, bouncedPage());

  m = path.match(/^\/api\/v1\/ar\/payments\/([^/]+)\/history$/);
  if (m && method === "GET") {
    const p = world.payments.find((x) => x.id === m[1]);
    return p ? send(res, 200, history(p)) : refuse(res, 404, "not found");
  }

  m = path.match(/^\/api\/v1\/ar\/payments\/([^/]+)\/nsf$/);
  if (m && method === "POST") {
    const p = world.payments.find((x) => x.id === m[1]);
    if (!p) return refuse(res, 404, "not found");
    const body = await readBody(req);
    if (body === null) return refuse(res, 422, "the request body must be JSON");
    if (p.status === "BOUNCED") return refuse(res, 409, "payment already marked BOUNCED");
    p.status = "BOUNCED";
    p.reason = String(body.reason ?? "");
    p.bounced_at = NOW_ISO();
    let reopened = 0;
    for (const al of p.allocations) {
      const inv = world.invoices.find((i) => invNumber(i) === al.invoice_n);
      inv.balance_cents += al.amount_cents;
      reopened += 1;
    }
    const hold = {
      id: uuid("d", 100 + world.holds.length),
      customer_id: custId(p.acct),
      status: "ACTIVE",
      hold_kind: "NSF",
      auto_placed: true,
      reason: `Payment ${p.no} came back from the bank`,
      created_at: NOW_ISO(),
    };
    const already = world.holds.some((h) => h.status === "ACTIVE" && h.customer_id === hold.customer_id);
    if (!already) world.holds.push(hold);
    return send(res, 200, {
      payment_id: p.id,
      customer_id: custId(p.acct),
      fee_cents: NSF_FEE,
      fee_taxable: false,
      reversed_cents: p.amount_cents,
      reopened_invoices: reopened,
      hold_placed: !already,
      status: "BOUNCED",
    });
  }

  if (method === "GET" && path === "/api/v1/ar/holds") return send(res, 200, holdsPage(q.get("status") ?? ""));

  // The release, the override and the sweep are an administrator's: any other role is refused
  // with the gate's shared body.
  m = path.match(/^\/api\/v1\/ar\/holds\/([^/]+)\/(ar-release|override)$/);
  if (m && method === "POST") {
    if (!isController(req)) return send(res, 403, { error: "Forbidden" });
    const body = await readBody(req);
    const hold = world.holds.find((h) => h.id === m[1]);
    if (!hold) return refuse(res, 404, "not found");
    if (m[2] === "ar-release") {
      hold.status = "RELEASED";
      return send(res, 200, { ok: true });
    }
    hold.override_until = new Date(Date.now() + (body?.hours ?? 24) * 3_600_000).toISOString();
    hold.override_reason = String(body?.reason ?? "");
    return send(res, 200, { ok: true, override_until: hold.override_until, reason: hold.override_reason });
  }
  if (method === "POST" && path === "/api/v1/ar/credit-sweep") {
    if (!isController(req)) return send(res, 403, { error: "Forbidden" });
    return send(res, 200, { evaluated: ACCOUNTS.length, placed: 0, would_hold: 0, dry_run: false });
  }

  if (method === "POST" && path === "/api/v1/ar/register/selection") {
    const body = await readBody(req);
    const picked = (body?.invoice_ids ?? []).map(invById).filter(Boolean);
    return send(res, 200, { count: picked.length, balance_cents: picked.reduce((t, i) => t + i.balance_cents, 0) });
  }

  if (method === "POST" && path === "/api/v1/ar/register/reminders") {
    const body = await readBody(req);
    if (!Array.isArray(body?.invoice_ids) || body.invoice_ids.length === 0) return refuse(res, 400, "choose at least one invoice to chase");
    return send(res, 200, { sent: body.invoice_ids.length, replayed: 0, tone: body.tone });
  }

  if (method === "POST" && path === "/api/v1/ar/statement-runs") {
    const body = await readBody(req);
    const targets = body?.customer_id ? ACCOUNTS.filter((a) => custId(a.n) === body.customer_id) : ACCOUNTS;
    const statements = [];
    for (const a of targets) {
      const row = agingRows().find((r) => r.customer_id === custId(a.n));
      if (!row) continue;
      world.counter += 1;
      const st = {
        id: uuid("8", world.counter),
        run_id: uuid("9", world.counter),
        customer_id: custId(a.n),
        customer_name: a.name,
        as_of_date: dayOffset(0),
        current_cents: row.current_cents,
        days_30_cents: row.days_30_cents,
        days_60_cents: row.days_60_cents,
        days_90_plus_cents: row.days_90_plus_cents,
        closing_balance_cents: row.total_cents,
        delivery_status: "QUEUED",
        created_at: NOW_ISO(),
      };
      world.statements.set(custId(a.n), st);
      statements.push(st);
    }
    return send(res, 200, {
      run: {
        id: uuid("9", 999),
        mode: body?.mode ?? "MASS",
        as_of_date: dayOffset(0),
        status: "COMPLETED",
        generated_count: statements.length,
        queued_count: statements.length,
        created_at: NOW_ISO(),
        notices_enabled: true,
        notice_queued_count: statements.length,
        notice_suppressed_count: 0,
        notice_accounting_recorded: true,
      },
      statements,
    });
  }

  if (method === "GET" && path === "/api/v1/ar/register/export") {
    const rows = world.invoices.filter((i) => i.balance_cents > 0).map((i) => `${i.invoice_no},${acct(i.acct).name},${i.balance_cents}`);
    res.writeHead(200, { "Content-Type": "text/csv" });
    return res.end(`Invoice,Customer,Balance\n${rows.join("\n")}\n`);
  }

  m = path.match(/^\/api\/v1\/ar\/customers\/([^/]+)\/statement$/);
  if (m && method === "GET") {
    const a = acctOfCust(m[1]);
    const kept = world.statements.get(m[1]);
    if (!a || kept === undefined) return refuse(res, 404, "not found");
    return send(res, 200, kept);
  }

  if (method === "POST" && path === "/api/v1/ar/register/statements") {
    const body = await readBody(req);
    if (body === null) return refuse(res, 422, "the request body must be JSON");
    if (!Array.isArray(body.invoice_ids) || body.invoice_ids.length === 0) return refuse(res, 400, "choose at least one invoice");
    const custs = new Set();
    for (const id of body.invoice_ids) {
      const inv = invById(id);
      if (inv) custs.add(inv.acct);
    }
    const run_ids = [];
    for (const n of custs) {
      world.counter += 1;
      const rows = agingRows().find((r) => r.customer_id === custId(n));
      const runId = uuid("9", world.counter);
      run_ids.push(runId);
      world.statements.set(custId(n), {
        id: uuid("8", world.counter),
        run_id: runId,
        customer_id: custId(n),
        customer_name: acct(n).name,
        as_of_date: dayOffset(0),
        current_cents: rows.current_cents,
        days_30_cents: rows.days_30_cents,
        days_60_cents: rows.days_60_cents,
        days_90_plus_cents: rows.days_90_plus_cents,
        closing_balance_cents: rows.total_cents,
        delivery_status: "NOT_QUEUED",
        created_at: NOW_ISO(),
      });
    }
    return send(res, 200, { accounts: custs.size, generated: custs.size, run_ids });
  }

  return refuse(res, 404, "not found");
}

const server = createServer((req, res) => {
  handle(req, res).catch(() => refuse(res, 500, "internal error"));
});
server.listen(port, () => {
  console.log(`money-desk-stub listening on ${port}`);
});

// A contract test that starts this stub ends it by closing the stub's standard input, never by a signal: with
// ROLE_STUB_STDIN_EXIT set, the stub exits when that stream ends.
if (process.env.ROLE_STUB_STDIN_EXIT === "1") {
  process.stdin.resume();
  process.stdin.on("end", () => process.exit(0));
}
