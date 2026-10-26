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
import { createOfficeWorld } from "./ar-office-world.mjs";
import { createFinstmtWorld } from "./finstmt-world.mjs";
import { createBankrecWorld } from "./bankrec-world.mjs";
import { createTaxWorld } from "./tax-world.mjs";
import { createLienWorld } from "./lien-world.mjs";
import { createAssistWorld } from "./assist-world.mjs";

// The ledger slice's world (approvals, journals, trial balance), answered by its own module.
const glWorld = createGlWorld();
// The statements slice's world (periods, the five statements, aging, saved packs), answered by its own module.
const finstmtWorld = createFinstmtWorld();
// The bank reconciliation slice's world (accounts, statement lines, matches, rules, months), answered by its own module.
const bankrecWorld = createBankrecWorld();
// The tax administration slice's world (rates, profiles, config, evidence, recompute, recon), answered by its own module.
const taxWorld = createTaxWorld();
// The liens and compliance slice's world (jobs, the holdback ledger, documents, the calendar, the roster), answered by its own module.
const lienWorld = createLienWorld();
// The TanyAI panel's staff assist routes (PANEL-API r1), answered by its own module.
const assistWorld = createAssistWorld();

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

// Documents the register serves that are not collectible invoices (the core's no chip read returns every
// INVOICE document whatever its stored status, and credit memos only with show_credits): a draft, a void
// and a credit memo on the first account. They live outside world.invoices on purpose: they owe nothing,
// so the aging, the payments and the statements never see them.
const EXTRA_DOCS = [
  { n: 201, stored: "DRAFT", doc_type: "INVOICE", total: 50_000 },
  { n: 202, stored: "VOID", doc_type: "INVOICE", total: 70_000 },
  { n: 203, stored: "FINALIZED", doc_type: "CREDIT_MEMO", total: 25_000 },
];

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

// The receivables office's world (AP contacts, reminders, the paper path, dispute, settings), answered by its own module.
const officeWorld = createOfficeWorld({
  isController: (req) => isController(req),
  uuid: (kind, n) => uuid(kind, n),
  accounts: () => ACCOUNTS.map((a) => ({ id: uuid("e", a.n), name: a.name, no: a.no })),
  world: () => world,
  acct: (n) => acct(n),
  custId: (n) => custId(n),
  invNumber: (inv) => invNumber(inv),
  invById: (id) => invById(id),
  dayOffset: (n) => dayOffset(n),
  customerIdOfNo: (no) => uuid("e", ACCOUNTS.find((a) => a.no === no).n),
  openInvoices: (customerId) =>
    world.invoices.filter((i) => uuid("e", i.acct) === customerId && i.balance_cents > 0).map((i) => ({ id: i.id, invoice_no: i.invoice_no, late: i.late, balance_cents: i.balance_cents })),
  taggedAccounts: () => ACCOUNTS.filter((a) => a.n !== 5).map((a) => ({ id: uuid("e", a.n), label: "Accounts payable desk" })),
});

function reset(kind = "full") {
  parkedApplyMs = 0;
  officeWorld.reset(kind);
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
      dispute:
        i.n === 4
          ? { id: uuid("7", 4), status: "OPEN", reason: "SHORT_DELIVERY", note: "Two pallets were short on the drop", amount_cents: 20_000, raised_at: NOW_ISO() }
          : null,
      payments: [],
    })),
    payments: [],
    credits: [{ acct: 4, amount_cents: 12_500 }],
    holds: [],
    credit_memos: [],
    ap_contacts: new Map(),
    statements: new Map(),
    workspace_statements: [],
    runs: new Map(),
    keys: new Map(),
    notes: new Map(),
    counter: 0,
    // Walk change 8: cross-account Promises tab read seed.
    promises: [],
    // Walk change 8 S6: per-account payment policy keyed by customer_id.
    paymentPolicies: {},
    // Walk change 8 S8 / S5M: reminder rules and per-(customer,rule) exemption state.
    reminderRules: [],
    exemptions: new Map(),
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

  // Walk change 8 S5: seed a couple of cross-account promises so the Promises tab has live rows.
  world.promises.push({
    id: uuid("p", 1),
    customer_id: custId(1),
    invoice_id: uuid("a", 1),
    promised_cents: 100_000,
    promised_date: dayOffset(-7), // 7 days ago, so the row reads BROKEN
    status: "BROKEN",
    promised_by: "Accounts payable desk",
    note: "Cheque went out Friday",
    taken_by: uuid("u", 9),
    taken_at: dayOffset(-14),
    settled_at: null,
  });
  world.promises.push({
    id: uuid("p", 2),
    customer_id: custId(2),
    invoice_id: uuid("a", 3),
    promised_cents: 250_000,
    promised_date: dayOffset(7), // 7 days from now, OPEN
    status: "OPEN",
    promised_by: "Owner",
    note: "Clearing at month end",
    taken_by: uuid("u", 9),
    taken_at: dayOffset(-2),
    settled_at: null,
  });
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
    hold(1, 1, "NSF", true, "Payment PAY-0001 came back from the bank", 2, { src: uuid("c", 1) }),
    hold(2, 2, "AR_AGING", true, "Receivables are more than 90 days past due", 6),
    hold(3, 5, "CREDIT_LIMIT", false, "Balance is over the credit limit; override until the deposit clears", 1, {
      override_until: new Date(Date.now() + 5 * 86_400_000).toISOString(),
      override_reason: "Deposit is coming",
    }),
  );

  // Workspace seed: credit memos the payments tab carries as unapplied figures, the
  // statements the statements tab carries, and the AP contacts the header shows.
  world.credit_memos.push(
    { id: uuid("a", 500), no: "CM-0001", acct: 1, issued_at: NOW_ISO(), amount_cents: 25_000, applied_cents: 0, reason: "Damaged on arrival, partial credit", status: "POSTED" },
    { id: uuid("a", 501), no: "CM-0002", acct: 2, issued_at: NOW_ISO(), amount_cents: 50_000, applied_cents: 50_000, reason: "Returned full pallet", status: "POSTED" },
  );
  world.ap_contacts.set(custId(1), { name: "Maria Quinn", email: "ap@graniteedge.example", phone: "+1-555-0101" });
  world.ap_contacts.set(custId(2), { name: "Otis Bell", email: "ops@oakline.example", phone: "+1-555-0102" });
  world.workspace_statements.push(
    { id: uuid("a", 700), run_id: uuid("a", 701), acct: 1, mode: "MASS", as_of: dayOffset(-30), closing: 728_140, delivery: "SENT", created_at: dayOffset(-30) },
    { id: uuid("a", 702), run_id: uuid("a", 703), acct: 1, mode: "INTERIM", as_of: dayOffset(-2), closing: 728_140, delivery: "QUEUED", created_at: dayOffset(-2) },
    { id: uuid("a", 704), run_id: uuid("a", 705), acct: 2, mode: "MASS", as_of: dayOffset(-30), closing: 345_500, delivery: "SENT", created_at: dayOffset(-30) },
  );
}

const acct = (n) => ACCOUNTS.find((a) => a.n === n);
const custId = (n) => uuid("e", n);
const acctOfCust = (id) => ACCOUNTS.find((a) => custId(a.n) === id);
const invById = (id) => world.invoices.find((i) => i.id === id);

// workspaceFor builds the full workspace payload for one customer. The 5-bucket aging
// is the same arithmetic the core uses; the five buckets sum to openCents and the
// four late buckets sum to overdueCents, so the workspace and the AR console quote
// the same numbers. When the customer is not in the demo's account list the stub
// still answers (404 would be dishonest from the stub; a zero-everything answer is
// the truth).
function workspaceFor(customerId) {
  const a = acctOfCust(customerId);
  if (!a) {
    return {
      header: emptyHeader(customerId),
      invoices_open: [], invoices_closed: [], invoices_disputed: [],
      payments: [], credit_notes: [], statements: [], notes: [], activity: [],
      as_of: NOW_ISO(),
    };
  }
  const invs = world.invoices.filter((i) => i.acct === a.n);
  const open = [];
  const closed = [];
  const disputed = [];
  for (const inv of invs) {
    const row = workspaceInvoiceRow(inv);
    if (inv.dispute && inv.dispute.status === "OPEN") {
      disputed.push(row);
      if (inv.balance_cents > 0) open.push(row);
    } else if (inv.balance_cents > 0) {
      open.push(row);
    } else if (inv.paid) {
      closed.push(row);
    }
  }
  const payments = world.payments
    .filter((p) => p.acct === a.n && p.status === "RECEIVED")
    .map(workspacePayment);
  const creditNotes = world.credit_memos
    .filter((m) => m.acct === a.n && m.status !== "VOID")
    .map(workspaceCredit);
  const statements = (world.workspace_statements ?? []).filter((s) => s.acct === a.n).map(workspaceStatement);
  const notes = (world.notes.get(customerId) ?? []).map(workspaceNote);
  const activity = workspaceActivity(a);
  const aging = agingFor(a);
  const lastPay = payments.length > 0 ? payments[0] : null;
  const openCents = open.reduce((s, r) => s + r.balance_cents, 0);
  const overdueCents = open.reduce((s, r) => s + (r.days_overdue > 0 ? r.balance_cents : 0), 0);
  const utilization = a.limit > 0 ? Math.round((openCents * 10000) / a.limit) : 0;
  const tagged = world.ap_contacts.get(customerId);
  const header = {
    customer_id: customerId,
    name: a.name,
    account_no: a.no,
    status: accountStatus(a.n),
    terms_days: a.terms,
    credit_limit_cents: a.limit,
    open_cents: openCents,
    available_credit_cents: a.limit - openCents,
    overdue_cents: overdueCents,
    utilization_bps: utilization,
    primary_ap_contact_name: tagged ? tagged.name : "",
    primary_ap_contact_email: tagged ? tagged.email : "",
    primary_ap_contact_phone: tagged ? tagged.phone : "",
    last_payment_at: lastPay ? lastPay.received_at : null,
    last_payment_cents: lastPay ? lastPay.amount_cents : 0,
    aging,
  };
  return {
    header,
    invoices_open: open,
    invoices_closed: closed,
    invoices_disputed: disputed,
    payments,
    credit_notes: creditNotes,
    statements,
    notes,
    activity,
    as_of: NOW_ISO(),
  };
}

function emptyHeader(customerId) {
  return {
    customer_id: customerId,
    name: "",
    account_no: "",
    status: "CURRENT",
    terms_days: 30,
    credit_limit_cents: 0,
    open_cents: 0,
    available_credit_cents: 0,
    overdue_cents: 0,
    utilization_bps: 0,
    primary_ap_contact_name: "",
    primary_ap_contact_email: "",
    primary_ap_contact_phone: "",
    last_payment_at: null,
    last_payment_cents: 0,
    aging: { current_cents: 0, days_30_cents: 0, days_60_cents: 0, days_90_cents: 0, days_90_plus_cents: 0, open_cents: 0, overdue_cents: 0 },
  };
}

function workspaceInvoiceRow(inv) {
  const bucket = c8BucketOf(inv.late);
  return {
    id: inv.id,
    invoice_no: inv.invoice_no,
    doc_type: "INVOICE",
    status: inv.balance_cents === 0 ? "PAID" : "FINALIZED",
    issue_date: dayOffset(-inv.late - 30),
    due_date: dayOffset(-inv.late),
    total_cents: inv.total,
    balance_cents: inv.balance_cents,
    days_overdue: inv.late > 0 ? inv.late : 0,
    days_overdue_bucket: bucket.label,
    is_disputed: !!(inv.dispute && inv.dispute.status === "OPEN"),
    has_open_promise: !!inv.promise,
  };
}

function workspacePayment(p) {
  const allocated = (p.allocations ?? []).reduce((s, a) => s + a, 0);
  return {
    id: p.id,
    payment_no: p.no,
    received_at: p.received_at,
    amount_cents: p.amount_cents,
    method: p.method,
    unapplied_cents: Math.max(0, p.amount_cents - allocated),
    branch_id: p.branch ?? "",
  };
}

function workspaceCredit(m) {
  return {
    id: m.id,
    memo_no: m.no,
    issued_at: m.issued_at,
    amount_cents: m.amount_cents,
    unapplied_cents: Math.max(0, m.amount_cents - (m.applied_cents ?? 0)),
    reason: m.reason ?? "",
  };
}

function workspaceStatement(s) {
  return {
    id: s.id,
    run_id: s.run_id,
    mode: s.mode,
    as_of_date: s.as_of,
    closing_balance_cents: s.closing,
    delivery_status: s.delivery,
    created_at: s.created_at,
  };
}

function workspaceNote(n) {
  return {
    id: n.id,
    kind: n.kind,
    body: n.body,
    author_id: n.author_id ?? "",
    author_name: n.author_name ?? "",
    pinned: !!n.pinned,
    created_at: n.created_at,
  };
}

// agingFor returns the 5-bucket breakdown for one account, by summing open invoice
// balances. The five buckets sum to openCents (the workspace reconciliation check
// the server also enforces) and the four late buckets sum to overdueCents.
function agingFor(a) {
  const buckets = { Current: 0, "1 to 30": 0, "31 to 60": 0, "61 to 90": 0, "91 and over": 0 };
  let open = 0;
  let overdue = 0;
  for (const inv of world.invoices.filter((i) => i.acct === a.n && i.balance_cents > 0)) {
    const b = c8BucketOf(inv.late);
    buckets[b.label] = (buckets[b.label] ?? 0) + inv.balance_cents;
    open += inv.balance_cents;
    if (inv.late > 0) overdue += inv.balance_cents;
  }
  return {
    current_cents: buckets.Current,
    days_30_cents: buckets["1 to 30"],
    days_60_cents: buckets["31 to 60"],
    days_90_cents: buckets["61 to 90"],
    days_90_plus_cents: buckets["91 and over"],
    open_cents: open,
    overdue_cents: overdue,
  };
}

// workspaceActivity returns the last 10 AR subledger rows the activity feed renders.
// The stub simulates a customer_transactions feed by interleaving the customer's
// invoices, payments and credit memos newest first; a real implementation reads the
// customer_transactions table (0009_invoices_ar) the same way.
function workspaceActivity(a) {
  const rows = [];
  for (const inv of world.invoices.filter((i) => i.acct === a.n)) {
    rows.push({ kind: "INVOICE", reference: inv.invoice_no, amount_cents: inv.total, occurred_at: dayOffset(-inv.late - 30), memo: "" });
  }
  for (const p of world.payments.filter((p) => p.acct === a.n && p.status === "RECEIVED")) {
    rows.push({ kind: "PAYMENT", reference: p.no, amount_cents: -p.amount_cents, occurred_at: p.received_at, memo: "" });
  }
  for (const m of world.credit_memos.filter((m) => m.acct === a.n && m.status !== "VOID")) {
    rows.push({ kind: "CREDIT_MEMO", reference: m.no, amount_cents: -m.amount_cents, occurred_at: m.issued_at, memo: m.reason ?? "" });
  }
  rows.sort((x, y) => new Date(y.occurred_at).getTime() - new Date(x.occurred_at).getTime());
  return rows.slice(0, 10).map((r) => ({
    kind: r.kind,
    reference: r.reference,
    amount_cents: r.amount_cents,
    balance_after_cents: 0,
    branch_id: "",
    memo: r.memo,
    occurred_at: r.occurred_at,
  }));
}

// workspaceAddNote writes one customer_ar_notes row. Pinned=true first un-pins any
// existing pinned note on the same account (the partial unique index 0405 declares
// refuses a second pinned row, so this is the order the server takes).
function workspaceAddNote(customerId, body) {
  if (!world.notes.has(customerId)) world.notes.set(customerId, []);
  const list = world.notes.get(customerId);
  if (body.pinned) for (const n of list) n.pinned = false;
  const id = uuid("a", 9000 + list.length + 1);
  const row = {
    id,
    kind: typeof body.kind === "string" && body.kind !== "" ? body.kind : "NOTE",
    body: String(body.body),
    pinned: !!body.pinned,
    created_at: NOW_ISO(),
    author_id: "de110000-0000-4000-9000-000000000007",
    author_name: "Payables and Receivables (demo)",
  };
  list.unshift(row);
  return workspaceNote(row);
}

reset();

function statusOf(inv) {
  if (inv.balance_cents === 0) return "PAID";
  if (inv.dispute !== null && inv.dispute.status === "OPEN") return "DISPUTED";
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

function c8BucketOf(days) {
  if (days <= 0) return { label: "Current", key: "CURRENT" };
  if (days <= 30) return { label: "1 to 30", key: "1_30" };
  if (days <= 60) return { label: "31 to 60", key: "31_60" };
  if (days <= 90) return { label: "61 to 90", key: "61_90" };
  return { label: "91 and over", key: "90_PLUS" };
}

function worklist(q) {
  const rows = [];
  for (const a of ACCOUNTS) {
    const open = world.invoices.filter((i) => i.acct === a.n && i.balance_cents > 0);
    const openARCents = open.reduce((sum, i) => sum + i.balance_cents, 0);
    const pastDueCents = open.filter((i) => i.late > 0).reduce((sum, i) => sum + i.balance_cents, 0);
    const over60Cents = open.filter((i) => i.late > 60).reduce((sum, i) => sum + i.balance_cents, 0);
    const unappliedCreditCents = world.credits.filter((c) => c.acct === a.n).reduce((sum, c) => sum + c.amount_cents, 0);
    const promises = open.filter((i) => i.promise !== null && ["OPEN", "BROKEN"].includes(i.promise.status));
    const brokenPromises = promises.filter((i) => i.promise.status === "BROKEN" || (i.promise.status === "OPEN" && i.promise.promised_date < dayOffset(0))).length + (a.n === 2 ? 1 : 0);
    const duePromises = promises.filter((i) => i.promise.status === "OPEN" && i.promise.promised_date === dayOffset(0)).length + (a.n === 3 ? 1 : 0);
    const openPromises = promises.filter((i) => i.promise.status === "OPEN").length + (a.n === 3 ? 1 : 0);
    const promisedCents = promises.filter((i) => i.promise.status === "OPEN").reduce((sum, i) => sum + i.promise.promised_cents, 0) + (a.n === 3 ? 20_000 : 0);
    const nextDates = promises.filter((i) => i.promise.status === "OPEN").map((i) => i.promise.promised_date).concat(a.n === 3 ? [dayOffset(0)] : []).sort();
    if (openARCents === 0 && unappliedCreditCents === 0 && brokenPromises + duePromises + openPromises === 0) continue;
    const onHold = world.holds.some((h) => h.status === "ACTIVE" && h.customer_id === custId(a.n));
    const creditHoldCandidate = onHold || (a.terms > 0 && a.limit > 0 && openARCents > a.limit) || over60Cents > 0;
    const urgency = brokenPromises > 0 ? "broken_promise" : over60Cents > 0 ? "over_60" : duePromises > 0 ? "promise_due" : creditHoldCandidate ? "credit_hold" : unappliedCreditCents > 0 ? "credit" : pastDueCents > 0 ? "past_due" : "open";
    const recommendedAction = ["broken_promise", "promise_due"].includes(urgency) ? "review_promise" : urgency === "credit_hold" ? "review_credit_hold" : urgency === "credit" ? "apply_credit" : ["over_60", "past_due"].includes(urgency) ? "record_payment" : "open_account";
    const oldest = c8BucketOf(open.length === 0 ? 0 : Math.max(...open.map((i) => i.late)));
    const row = {
      customer_id: custId(a.n),
      customer_name: a.name,
      account_no: a.no,
      branch_id: branchOf(a.n),
      open_ar_cents: openARCents,
      past_due_cents: pastDueCents,
      over_60_cents: over60Cents,
      available_credit_cents: Math.max(a.limit - openARCents, 0),
      unapplied_credit_cents: unappliedCreditCents,
      oldest_bucket: oldest.label,
      oldest_bucket_key: oldest.key,
      broken_promises: brokenPromises,
      due_promises: duePromises,
      open_promises: openPromises,
      promised_cents: promisedCents,
      on_hold: onHold,
      credit_hold_candidate: creditHoldCandidate,
      action_needed: urgency !== "open",
      urgency,
      recommended_action: recommendedAction,
    };
    if (nextDates.length > 0) row.next_promise_date = nextDates[0];
    rows.push(row);
  }
  const summary = {
    total_past_due_cents: rows.reduce((sum, r) => sum + r.past_due_cents, 0),
    over_60_cents: rows.reduce((sum, r) => sum + r.over_60_cents, 0),
    broken_promises: rows.reduce((sum, r) => sum + r.broken_promises, 0),
    unapplied_credits_cents: rows.reduce((sum, r) => sum + r.unapplied_credit_cents, 0),
  };
  const counts = {
    all_open: rows.length,
    action_needed: rows.filter((r) => r.action_needed).length,
    over_60: rows.filter((r) => r.over_60_cents > 0).length,
    credit_hold: rows.filter((r) => r.credit_hold_candidate).length,
  };
  const rank = { broken_promise: 0, over_60: 1, promise_due: 2, credit_hold: 3, credit: 4, past_due: 5, open: 6 };
  rows.sort((a, b) => rank[a.urgency] - rank[b.urgency] || b.past_due_cents - a.past_due_cents || b.over_60_cents - a.over_60_cents || a.customer_name.localeCompare(b.customer_name) || a.customer_id.localeCompare(b.customer_id));
  const chip = q.get("chip") ?? "all";
  const query = (q.get("q") ?? "").trim().toLowerCase();
  const shown = rows.filter((r) => {
    if (query !== "" && !`${r.customer_name} ${r.account_no} ${r.customer_id}`.toLowerCase().includes(query)) return false;
    if (chip === "action_needed") return r.action_needed;
    if (chip === "over_60") return r.over_60_cents > 0;
    if (chip === "credit_hold") return r.credit_hold_candidate;
    return true;
  });
  return { rows: shown, summary, counts, as_of: dayOffset(0) };
}

function registerRow(inv) {
  const a = acct(inv.acct);
  const days = Math.max(inv.late, 0);
  const bucket = c8BucketOf(days);
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
    days_overdue: days,
    bucket_label: bucket.label,
    bucket_key: bucket.key,
    account_status: accountStatus(a.n),
    disputed: inv.dispute !== null && inv.dispute.status === "OPEN",
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
    policy: officeWorld.policy(),
    cards,
    escalation_queue: queue,
    paper_path: officeWorld.paperPath(),
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
  if (inv.dispute !== null) out.dispute = inv.dispute;
  out.order_no = `SO-${2000 + invNumber(inv)}`;
  out.order_id = uuid("o", invNumber(inv));
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
        // The core stamps reversed_at on the original of a reversal (its status stays as it was).
        ...(p.reversed === true ? { reversed_at: p.received_at } : {}),
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
      fee_cents: p.fee_cents ?? NSF_FEE,
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
    reversed: p.status === "BOUNCED" || p.reversed === true,
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
    .map(({ src: _src, ...h }) => ({ ...h, customer_name: acctOfCust(h.customer_id)?.name }));
  return { items };
}

// ---- the server ------------------------------------------------------------------------------

function send(res, status, data) {
  res.writeHead(status, { "Content-Type": "application/json" });
  res.end(JSON.stringify(data));
}
const refuse = (res, status, error) => send(res, status, { error });

function readRaw(req) {
  return new Promise((resolve) => {
    let raw = "";
    req.on("data", (c) => (raw += c));
    req.on("end", () => resolve(raw));
  });
}

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
  // The public tenant config route (no credential): the dealer the band and the tab name.
  if (method === "GET" && path === "/api/v1/tenant/config") {
    return send(res, 200, { id: "demo-dealer", dealer_name: "Gable Landscape Supply", logo_url: "", primary_color: "#3a7d56", support_email: "support@gablelandscape.invalid", support_phone: "" });
  }
  if (method === "POST" && path === "/_stub/reset") {
    reset();
    glWorld.reset();
    finstmtWorld.reset();
    bankrecWorld.reset();
    taxWorld.reset();
    lienWorld.reset();
    assistWorld.reset();
    return send(res, 200, { ok: true });
  }
  if (method === "GET" && path === "/_stub/finstmt-calls") return send(res, 200, { calls: finstmtWorld.calls(), exports: finstmtWorld.exports(), year: finstmtWorld.fiscalYear });
  if (method === "POST" && path === "/_stub/scenario") {
    const body = await readBody(req);
    reset(["empty", "wide", "pro", "unwired"].includes(body?.name) ? body.name : "full");
    return send(res, 200, { ok: true });
  }
  if (method === "POST" && path === "/_stub/park-apply") {
    const body = await readBody(req);
    parkedApplyMs = Math.max(0, Number(body?.ms ?? 0));
    return send(res, 200, { ok: true, parkedApplyMs });
  }
  if (method === "POST" && path === "/_stub/reverse-payment") {
    const body = await readBody(req);
    const answer = bankrecWorld.handle(req, url, JSON.stringify(body ?? {}));
    return send(res, answer.status, answer.body);
  }
  if (path.startsWith("/api/v1/assist/") || path.startsWith("/_stub/assist")) {
    if (path.startsWith("/api/v1/") && (req.headers.authorization ?? "") === "") return refuse(res, 401, "Unauthorized");
    const body = method === "POST" ? await readBody(req) : null;
    if (body === null && method === "POST") return refuse(res, 400, "invalid_json");
    const answer = await assistWorld.handle(req, url, body);
    return answer === null ? refuse(res, 404, "not found") : send(res, answer.status, answer.body);
  }
  if (path === "/api/v1/demo/flows") return send(res, 200, { flows: FLOWS });
  if (method === "POST" && path === "/api/v1/demo/session") {
    const body = await readBody(req);
    const account = USERS[body?.flow];
    if (!account) return refuse(res, 404, "not found");
    return send(res, 200, { token: mintToken(account.user_id), user: account, flow: body.flow });
  }
  if (method === "GET" && path === "/api/v1/branches") return send(res, 200, BRANCHES);
  if (method === "POST" && path === "/_stub/posting-queue") {
    const body = await readBody(req);
    const answer = glWorld.handle(req, url, JSON.stringify(body ?? {}));
    return send(res, answer.status, answer.body);
  }
  if (method === "POST" && path === "/_stub/legacy-import") {
    const body = await readBody(req);
    const answer = glWorld.handle(req, url, JSON.stringify(body ?? {}));
    return send(res, answer.status, answer.body);
  }
  // The statements slice reads the books' periods paged (any page size but 100, two fiscal years); the ledger
  // slice reads a page of 100 (one year, closed and reopened by its own presses), so the two worlds share the route by that line.
  if (path.startsWith("/api/v1/finstmt/") || (path === "/api/v1/gl/periods" && method === "GET" && url.searchParams.has("limit") && url.searchParams.get("limit") !== "100")) {
    if ((req.headers.authorization ?? "") === "") return refuse(res, 401, "Unauthorized");
    const raw = method === "POST" || method === "PUT" ? await readRaw(req) : "";
    const answer = finstmtWorld.handle(req, url, raw);
    if (answer === null) return refuse(res, 404, "not found");
    if (typeof answer.csv === "string") {
      res.writeHead(answer.status, { "Content-Type": "text/csv; charset=utf-8", "Content-Disposition": 'attachment; filename="export.csv"' });
      return res.end(answer.csv);
    }
    if (answer.status === 204) {
      res.writeHead(204);
      return res.end();
    }
    return send(res, answer.status, answer.body);
  }
  if (path.startsWith("/api/v1/staff/approvals") || path.startsWith("/api/v1/gl/") || path === "/api/v1/surfaces/money-desk/config") {
    if ((req.headers.authorization ?? "") === "") return refuse(res, 401, "Unauthorized");
    const body = method === "POST" ? await readBody(req) : null;
    const answer = glWorld.handle(req, url, JSON.stringify(body ?? {}));
    return answer === null ? refuse(res, 404, "not found") : send(res, answer.status, answer.body);
  }
  if (path.startsWith("/api/v1/tax/")) {
    if ((req.headers.authorization ?? "") === "") return refuse(res, 401, "Unauthorized");
    const body = method === "POST" ? await readBody(req) : null;
    const answer = taxWorld.handle(req, url, JSON.stringify(body ?? {}));
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
  if (method === "GET" && path === "/api/v1/billing/ar/worklist") {
    if ((req.headers.authorization ?? "") === "") return refuse(res, 401, "Unauthorized");
    const chip = q.get("chip") ?? "all";
    if (!["all", "action_needed", "over_60", "credit_hold"].includes(chip)) return refuse(res, 400, "a worklist filter must be all, action_needed, over_60, or credit_hold");
    return send(res, 200, worklist(q));
  }
  if (!path.startsWith("/api/v1/ar/")) return refuse(res, 404, "not found");
  if ((req.headers.authorization ?? "") === "") return refuse(res, 401, "Unauthorized");

  // The received payment route (core/internal/billing register_service.go ApplyReceivedPayment and
  // core/internal/payments received_apply.go), its checks in the core's order and its sentences word for word:
  // the service's key check, then the replay judgement (never on a preview) BEFORE the request's own checks,
  // then the locks in id order (a missing or another account's invoice is a 404, one not open is refused), then
  // one branch per payment, then the ceiling. An unreadable body is a 400, a spent key's conflict is a 409, and
  // every other refusal is a 422 but the 404. The split is the server's: oldest
  // first by issue date, due date, invoice number and id whatever order the list came in, one line per named
  // invoice, unapplied always zero. A preview takes the same walk and writes nothing, and never reads the key.
  if (method === "POST" && path === "/api/v1/ar/payments/apply") {
    const body = await readBody(req);
    if (body === null) return refuse(res, 400, "the request body must be JSON");
    const isUuid = (v) => typeof v === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(v.trim());
    const key = typeof body.idempotency_key === "string" ? body.idempotency_key : "";
    const ids = Array.isArray(body.invoice_ids) ? body.invoice_ids : [];
    const method_ = typeof body.method === "string" ? body.method.trim().toUpperCase() : "";
    const preview = body.preview === true;
    if (key === "") return refuse(res, 422, "recording a payment needs an idempotency key");
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
    // The replay judgement runs BEFORE the body validation (FP 186 and 187), and its refusals are
    // 409 Conflict so a client reads a spent key by status alone: whatever malformed body the retry
    // carries under the held key, the answer is the conflict, never a fresh key's validation words.
    if (!preview) {
      const first = world.keys.get(`apply:${key}`);
      if (first !== undefined) {
        if (!isUuid(body.customer_id) || first.fingerprint.customer !== body.customer_id.trim().toLowerCase()) return refuse(res, 409, "that idempotency key already recorded a payment for another account; use a new key");
        // The named list is normalised the way Go's normalisedInvoiceIDs does it: an empty list, a
        // non uuid id, or an id named twice cannot match the stored payment's list.
        const replaySeen = new Set();
        let listMatches = ids.length > 0;
        for (const raw of ids) {
          if (!isUuid(raw)) { listMatches = false; break; }
          const id = raw.trim().toLowerCase();
          if (replaySeen.has(id)) { listMatches = false; break; }
          replaySeen.add(id);
        }
        const same =
          listMatches && Number.isInteger(body.amount_cents) && first.fingerprint.amount === body.amount_cents && first.fingerprint.method === method_ && first.fingerprint.reference === reference && first.fingerprint.named.join(",") === [...replaySeen].sort().join(",");
        if (!same) return refuse(res, 409, "that idempotency key already recorded a different payment; use a new key");
        const lines = oldestFirst(first.invoices.map((id) => invById(id))).map((inv) => {
          const line = lineFor(inv, first.applied.get(inv.id) ?? 0);
          return { ...line, outcome: line.applied_cents > 0 ? (inv.balance_cents === 0 ? "CLOSES" : "PART") : "NONE", balance_after_cents: inv.balance_cents, stays_open_cents: payableOf(inv) };
        });
        return send(res, 200, { ...first.answer, lines, replayed: true });
      }
    }
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

  // The customer account workspace (Change 8 S2 + S2M). One round trip the panel
  // renders in one paint, with the 5-bucket aging, the open/closed/disputed invoice
  // lists, payments, credit notes, statements, notes and the activity timeline. The
  // answers reconcile to the penny with S0 (the server reconciliation primitive),
  // the same arithmetic the core checks.
  let mWorkspace = path.match(/^\/api\/v1\/billing\/ar\/customers\/([^/]+)\/workspace$/);
  if (mWorkspace && method === "GET") return send(res, 200, workspaceFor(mWorkspace[1]));
  mWorkspace = path.match(/^\/api\/v1\/billing\/ar\/customers\/([^/]+)\/notes$/);
  if (mWorkspace && method === "POST") {
    const body = await readBody(req);
    if (body === null) return refuse(res, 400, "the request body must be JSON");
    if (typeof body.body !== "string" || body.body.trim() === "") return refuse(res, 422, "note body is required");
    if (body.body.length > 4096) return refuse(res, 422, "note body exceeds 4096 characters");
    return send(res, 201, { note: workspaceAddNote(mWorkspace[1], body) });
  }

  if (method === "GET" && path === "/api/v1/ar/register") {
    const customer = q.get("customer_id") ?? "";
    // The core's reading (register_repository.go chipPredicates): no chip is every invoice document,
    // paid ones too, unpaid ahead of settled; PAID is the paid ones only; "all" is every unpaid one;
    // past_due, current and the legacy status names narrow the unpaid ones. The wire upper-cases the chip.
    const chip = (q.get("chip") ?? "").toUpperCase();
    const unpaid = (r) => r.stored_status === "FINALIZED" || r.stored_status === "PARTIALLY_PAID";
    const chips = {
      "": () => true,
      PAID: (r) => r.stored_status === "PAID",
      ALL: unpaid,
      OPEN: unpaid,
      PAST_DUE: (r) => unpaid(r) && r.days_overdue > 0,
      OVERDUE: (r) => unpaid(r) && r.days_overdue > 0,
      CURRENT: (r) => unpaid(r) && r.days_overdue <= 0,
      DISPUTED: (r) => r.disputed,
    };
    // An unknown chip adds no predicate in the core (chipPredicates misses), so it reads like no chip.
    const known = chips[chip] ?? chips[""];
    const showCredits = q.get("show_credits") === "1" || q.get("show_credits") === "true";
    const mine = world.invoices.filter((i) => customer === "" || custId(i.acct) === customer).map(registerRow);
    const extra = world.empty || (customer !== "" && customer !== custId(1)) ? [] : EXTRA_DOCS.map((d) => {
      const row = registerRow({ id: uuid("b", d.n), invoice_no: `${d.doc_type === "CREDIT_MEMO" ? "CM" : "INV"}-${1000 + d.n}`, acct: 1, late: 20, total_cents: d.total, balance_cents: d.total, holdback_cents: 0, promise: null, dispute: null });
      return { ...row, doc_type: d.doc_type, stored_status: d.stored, status: d.doc_type === "CREDIT_MEMO" ? "CREDIT" : d.stored };
    });
    // Credit memos are left out unless asked for (registerWhere: doc_type = 'INVOICE' without show_credits).
    const docs = [...mine, ...extra].filter((r) => showCredits || r.doc_type === "INVOICE");
    const filtered = docs.filter(known);
    filtered.sort((a, b) => Number(unpaid(b)) - Number(unpaid(a)) || a.due_date.localeCompare(b.due_date) || a.invoice_no.localeCompare(b.invoice_no));
    const asked = Number(q.get("limit") ?? "50");
    const limit = !Number.isInteger(asked) || asked <= 0 || asked > 200 ? 50 : asked;
    const offset = Math.max(0, Number(q.get("offset") ?? "0") || 0);
    const count = (s) => mine.filter((r) => r.status === s).length;
    return send(res, 200, {
      items: filtered.slice(offset, offset + limit),
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
    if (body === null) return refuse(res, 400, "the request body must be JSON");
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
    if (body === null) return refuse(res, 400, "the request body must be JSON");
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
    if (body === null) return refuse(res, 400, "the request body must be JSON");
    if (p.status === "BOUNCED") return refuse(res, 409, "payment already marked BOUNCED");
    // A payment is reversed or bounced, never both (SF-2d round 7).
    if (p.reversed === true) return refuse(res, 409, "this payment was reversed, so it cannot be marked bounced (a payment is reversed or bounced, never both)");
    p.status = "BOUNCED";
    p.fee_cents = officeWorld.state().nsf.fee_cents;
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
      src: p.id,
    };
    const already = world.holds.some((h) => h.status === "ACTIVE" && h.customer_id === hold.customer_id);
    if (!already) world.holds.push(hold);
    return send(res, 200, {
      payment_id: p.id,
      customer_id: custId(p.acct),
      fee_cents: p.fee_cents,
      fee_taxable: false,
      reversed_cents: p.amount_cents,
      reopened_invoices: reopened,
      hold_placed: !already,
      status: "BOUNCED",
    });
  }

  if (method === "GET" && path === "/api/v1/ar/holds") return send(res, 200, holdsPage(q.get("status") ?? ""));

  // C8 S4: place a manual credit hold by hand. Manager-gated (the role gate refuses
  // the call with 403 in production; the stub mirrors it with the same gate word).
  if (method === "POST" && path === "/api/v1/ar/holds") {
    if (!isController(req)) return send(res, 403, { error: "Forbidden" });
    const body = await readBody(req);
    const cid = String(body?.customer_id ?? "");
    const reason = String(body?.reason ?? "").trim();
    if (cid === "") return refuse(res, 400, "customer_id is required");
    if (reason === "") return refuse(res, 400, "reason is required");
    const acct = ACCOUNTS.find((a) => custId(a.n) === cid);
    if (!acct) return refuse(res, 404, "no such customer");
    const already = world.holds.find((h) => h.status === "ACTIVE" && h.customer_id === cid);
    if (already) return send(res, 200, already);
    const hold = {
      id: uuid("h", 1000 + world.holds.length),
      customer_id: cid,
      customer_name: acct.name,
      status: "ACTIVE",
      hold_kind: "MANUAL",
      auto_placed: false,
      reason,
      override_until: null,
      override_reason: "",
      created_at: NOW_ISO(),
    };
    world.holds.push(hold);
    return send(res, 201, hold);
  }

  // C8 S3: send a paylink (the receivables reminder). Staff-gated; the stub gates it
  // the same way every other reads route is gated, manager+staff carry it.
  if (method === "POST" && path === "/api/v1/ar/payments/paylink") {
    const body = await readBody(req);
    const cid = String(body?.customer_id ?? "");
    if (cid === "") return refuse(res, 400, "customer_id is required");
    const acct = ACCOUNTS.find((a) => custId(a.n) === cid);
    if (!acct) return refuse(res, 404, "no such customer");
    const amount = Number.isFinite(Number(body?.amount_cents)) ? Number(body?.amount_cents) : 0;
    const channel = String(body?.channel ?? "EMAIL").toUpperCase();
    const invoice = body?.invoice_id ? String(body.invoice_id) : "";
    const recipient = String(body?.recipient ?? "");
    // The deterministic token mirrors the server's SHA-256-of-pipe-joined payload,
    // truncated to 16 hex chars. The stub keeps the contract honest; the browser's
    // deterministicPaylinkToken mirrors the same shape so the Copy URL action lands
    // on the same URL a Send action would have produced.
    const payload = [cid, invoice, amount > 0 ? String(amount) : "", channel].join("|");
    const token = simpleHashHex(payload, 16);
    const paylink_url = `/paylink/${token}`;
    // Idempotent: a repeated call with the same tuple yields the same delivery row.
    const idemKey = `paylink-${token}`;
    if (!world.paylinks) world.paylinks = [];
    const existing = world.paylinks.find((p) => p.idem_key === idemKey);
    const sent_at = NOW_ISO();
    if (!existing) {
      world.paylinks.push({
        id: uuid("p", 1000 + world.paylinks.length),
        idem_key: idemKey,
        customer_id: cid,
        invoice_id: invoice || null,
        amount_cents: amount > 0 ? amount : null,
        channel,
        recipient,
        paylink_url,
        sent_at,
      });
    }
    return send(res, 200, { paylink_url, status: "QUEUED", sent_at });
  }

  // C8 S3: statement preview (artefact + open lines + 5-bucket aging). The stub
  // computes the same five buckets the core computes, so a Drift shows up the same
  // way the server would show it (reconciled = false).
  m = path.match(/^\/api\/v1\/billing\/ar\/customers\/([^/]+)\/statements\/([^/]+)\/preview$/);
  if (m && method === "GET") {
    const cid = m[1];
    const sid = m[2];
    const acct = ACCOUNTS.find((a) => custId(a.n) === cid);
    if (!acct) return refuse(res, 404, "no such customer");
    const statement = (world.workspace_statements ?? []).find((s) => s.id === sid && s.acct === acct.n);
    if (!statement) return refuse(res, 404, "no such statement for this customer");
    const open = (world.invoices ?? []).filter(
      (i) => i.acct === acct.n && (i.status === "FINALIZED" || i.status === "PARTIALLY_PAID") && i.balance_cents > 0 && i.doc_type !== "CREDIT_MEMO",
    );
    const aging = bucketsFor(open);
    const sumBuckets = aging.current_cents + aging.days_30_cents + aging.days_60_cents + aging.days_90_cents + aging.days_90_plus_cents;
    const sumLate = aging.days_30_cents + aging.days_60_cents + aging.days_90_cents + aging.days_90_plus_cents;
    const reconciled = sumBuckets === aging.open_cents && sumLate === aging.overdue_cents;
    return send(res, 200, {
      statement,
      open_lines: open.map((i) => ({
        invoice_id: i.id,
        invoice_no: invNumber(i),
        due_date: i.due_date,
        balance_cents: i.balance_cents,
        days_overdue: Math.max(0, Math.round((Date.now() - Date.parse(i.due_date)) / 86_400_000)),
        bucket_label: bucketLabel(Math.max(0, Math.round((Date.now() - Date.parse(i.due_date)) / 86_400_000))),
      })),
      aging,
      reconciled,
    });
  }

  // The release, the override and the sweep are an administrator's: any other role is refused
  // with the gate's shared body.
  m = path.match(/^\/api\/v1\/ar\/holds\/([^/]+)\/(ar-release|override)$/);
  if (m && method === "POST") {
    if (!isController(req)) return send(res, 403, { error: "Forbidden" });
    const body = await readBody(req);
    const hold = world.holds.find((h) => h.id === m[1]);
    if (!hold) return refuse(res, 404, "not found");
    if (m[2] === "ar-release") {
      if (hold.hold_kind === "NSF" || hold.status !== "ACTIVE") return refuse(res, 400, `billing: hold ${hold.id} is not an ACTIVE non-NSF hold, an NSF hold is released through the till's Tier-1 path, which enforces an authorization this module does not`);
      hold.status = "RELEASED";
      return send(res, 200, { ok: true });
    }
    hold.override_until = new Date(Date.now() + (body?.hours ?? 24) * 3_600_000).toISOString();
    hold.override_reason = String(body?.reason ?? "");
    return send(res, 200, { ok: true, override_until: hold.override_until, reason: hold.override_reason });
  }
  if (method === "POST" && path === "/api/v1/ar/credit-sweep") {
    if (!isController(req)) return send(res, 403, { error: "Forbidden" });
    // The sweep (core credit_gate.go Evaluate): an account whose oldest open invoice is at the escalation edge and has no
    // active hold would be held. While the automatic switch is off it only reports (dry_run, would_hold) and places nothing;
    // with it on it places the hold.
    const edge = officeWorld.policy().aging_escalate_days;
    const due = ACCOUNTS.filter((a) => {
      const open = world.invoices.filter((i) => i.acct === a.n && i.balance_cents > 0);
      const aged = open.some((i) => i.late >= edge);
      return aged && !world.holds.some((h) => h.status === "ACTIVE" && h.customer_id === custId(a.n));
    });
    const dry = !officeWorld.state().autoHold;
    if (!dry) {
      for (const a of due) {
        world.holds.push({ id: uuid("d", 100 + world.holds.length), customer_id: custId(a.n), status: "ACTIVE", hold_kind: "AR_AGING", auto_placed: true, reason: `Receivables are more than ${edge} days past due`, created_at: NOW_ISO() });
      }
    }
    return send(res, 200, { evaluated: ACCOUNTS.length, placed: dry ? 0 : due.length, would_hold: dry ? due.length : 0, dry_run: dry, ...(due.length === 0 ? {} : { customers: due.map((a) => custId(a.n)) }) });
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

  // The statement run (core/internal/billing monthend_handler.go statementRun and statements.go GenerateRun): the body
  // must be JSON, the as_of a calendar day, the mode MASS or INTERIM, and an INTERIM run names exactly one account.
  // A created run answers 201 and is stored, so the run reads back by its id.
  if (method === "POST" && path === "/api/v1/ar/statement-runs") {
    const body = await readBody(req);
    if (body === null) return refuse(res, 400, "invalid JSON body");
    if (body.as_of !== undefined && body.as_of !== "" && !/^\d{4}-\d{2}-\d{2}$/.test(String(body.as_of))) return refuse(res, 400, "as_of must be YYYY-MM-DD");
    const mode = body.mode === undefined || body.mode === "" ? "MASS" : body.mode;
    if (mode !== "MASS" && mode !== "INTERIM") return refuse(res, 400, `billing: unknown statement mode "${mode}"`);
    if (mode === "INTERIM" && !body.customer_id) return refuse(res, 400, "billing: an INTERIM run is for exactly one account");
    const targets = body.customer_id ? ACCOUNTS.filter((a) => custId(a.n) === body.customer_id) : ACCOUNTS;
    world.counter += 1;
    const runId = uuid("9", world.counter);
    const apTagged = (customerId) => officeWorld.state().apContacts.has(customerId);
    const statements = [];
    for (const a of targets) {
      const row = agingRows().find((r) => r.customer_id === custId(a.n));
      if (!row) continue;
      world.counter += 1;
      const st = {
        id: uuid("8", world.counter),
        run_id: runId,
        customer_id: custId(a.n),
        customer_name: a.name,
        as_of_date: dayOffset(0),
        current_cents: row.current_cents,
        days_30_cents: row.days_30_cents,
        days_60_cents: row.days_60_cents,
        days_90_plus_cents: row.days_90_plus_cents,
        closing_balance_cents: row.total_cents,
        delivery_status: apTagged(custId(a.n)) ? "QUEUED" : "NOT_QUEUED",
        created_at: NOW_ISO(),
      };
      world.statements.set(custId(a.n), st);
      statements.push(st);
    }
    const queued = statements.filter((s) => s.delivery_status === "QUEUED").length;
    const result = {
      run: {
        id: runId,
        mode,
        as_of_date: dayOffset(0),
        status: "COMPLETED",
        generated_count: statements.length,
        queued_count: statements.length,
        created_at: NOW_ISO(),
        finished_at: NOW_ISO(),
        notices_enabled: true,
        notice_queued_count: queued,
        notice_suppressed_count: statements.length - queued,
        notice_accounting_recorded: true,
      },
      statements,
    };
    world.runs.set(runId, result);
    return send(res, 201, result);
  }

  m = path.match(/^\/api\/v1\/ar\/statement-runs\/([^/]+)$/);
  if (m && method === "GET") {
    const found = world.runs.get(m[1]);
    return found === undefined ? refuse(res, 404, "not found") : send(res, 200, found);
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
    if (body === null) return refuse(res, 400, "the request body must be JSON");
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

  {
    const body = method === "POST" || method === "PUT" ? await readBody(req) : null;
    const answer = officeWorld.handle(req, url, body);
    if (answer !== null && answer.text !== undefined) {
      res.writeHead(answer.status, { "Content-Type": answer.type, "Cache-Control": "no-store" });
      return res.end(answer.text);
    }
    if (answer !== null) return send(res, answer.status, answer.body);
  }
  return refuse(res, 404, "not found");
}

const server = createServer((req, res) => {
  handle(req, res).catch(() => refuse(res, 500, "internal error"));
});
server.listen(port, () => {
  console.log(`money-desk-stub listening on ${port}`);
});

// --- Change 8 S3 + S4 + S3M stub helpers -------------------------------------------------------
// Pure helpers used by the C8 paylink, statement preview and hold-by-hand routes. The
// bucket derivation mirrors the server's INCLUSIVE-at-the-top-of-each-band bands (so
// a balance exactly 30 days past due sits in "1 to 30", never "31 to 60"); the hash
// is the same deterministic-pipe-joined-payload shape the core uses, so the stub and
// the browser land on the same URL for the same tuple.

function bucketLabel(days) {
  if (days <= 0) return "Current";
  if (days <= 30) return "1 to 30";
  if (days <= 60) return "31 to 60";
  if (days <= 90) return "61 to 90";
  return "91 and over";
}

function bucketsFor(openInvoices) {
  const a = { current_cents: 0, days_30_cents: 0, days_60_cents: 0, days_90_cents: 0, days_90_plus_cents: 0, open_cents: 0, overdue_cents: 0 };
  const now = Date.now();
  for (const i of openInvoices) {
    a.open_cents += i.balance_cents;
    const days = Math.max(0, Math.round((now - Date.parse(i.due_date)) / 86_400_000));
    if (days > 0) a.overdue_cents += i.balance_cents;
    if (days <= 0) a.current_cents += i.balance_cents;
    else if (days <= 30) a.days_30_cents += i.balance_cents;
    else if (days <= 60) a.days_60_cents += i.balance_cents;
    else if (days <= 90) a.days_90_cents += i.balance_cents;
    else a.days_90_plus_cents += i.balance_cents;
  }
  return a;
}

function simpleHashHex(payload, hexChars) {
  // FNV-1a 32-bit; the token's role is determinism, not cryptographic secrecy, so the
  // stub keeps the same shape the browser's deterministicPaylinkToken uses. Truncates
  // to hexChars chars (the server uses 16).
  let h1 = 0x811c9dc5;
  let h2 = 0xdeadbeef;
  for (let i = 0; i < payload.length; i++) {
    const c = payload.charCodeAt(i);
    h1 = Math.imul(h1 ^ c, 0x01000193);
    h2 = Math.imul(h2 ^ c, 0x85ebca6b);
  }
  const hex = (h1 >>> 0).toString(16).padStart(8, "0") + (h2 >>> 0).toString(16).padStart(8, "0");
  return hex.slice(0, hexChars);
}

// A contract test that starts this stub ends it by closing the stub's standard input, never by a signal: with
// ROLE_STUB_STDIN_EXIT set, the stub exits when that stream ends.
if (process.env.ROLE_STUB_STDIN_EXIT === "1") {
  process.stdin.resume();
  process.stdin.on("end", () => process.exit(0));
}
