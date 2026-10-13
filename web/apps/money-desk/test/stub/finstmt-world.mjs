// The statements slice's stub world: the books' periods, the posted journals behind the five statements, the
// receivables aging and the saved pack registry, answered the way the core answers them. This is a test double
// only; the real routes are the core's (core/internal/finstmt, core/internal/gl periods). Its rules and its
// refusal sentences are held to the contract and to the core's own source by test/finstmt-stub-contract.test.mjs,
// so it cannot drift in silence.
//
// The books are Gable Landscape Supply's, invented: two yards, a chart of nine accounts and the California sales
// tax payable, invoices that carry the 7.75 percent combined rate, payments, cost of goods and yard payroll, for
// the fiscal year the host's own clock names and the year before it. Years are cut from the host's day so no
// calendar literal is written. Every posted journal balances, so the statements tie by construction.
//
// Where the stub stands in for a view the core computes in SQL (the 0034 views), it follows the repository's
// own reading of them: the income statement sums a fiscal year or one of its periods, the balance sheet and the
// trial balance sum a fiscal year's postings, and a statement figure ties to the detail rows behind its account.
import { randomUUID } from "node:crypto";
import { IDS as GL_IDS } from "./gl-world.mjs";

export const IDS = {
  controller: GL_IDS.controller,
  payrec: "de110000-0000-4000-9000-000000000007",
  manager: GL_IDS.manager,
  sales: GL_IDS.sales,
  trenton: GL_IDS.trenton,
  kingston: GL_IDS.kingston,
  pack1: "5c4a0000-0000-4000-9000-0000000000a1",
  pack2: "5c4a0000-0000-4000-9000-0000000000a2",
};

export const CHART = {
  "1000": ["Cash", "ASSET"],
  "1100": ["Accounts Receivable", "ASSET"],
  "1200": ["Inventory", "ASSET"],
  "2100": ["Accounts Payable", "LIABILITY"],
  "2200": ["Sales Tax Payable", "LIABILITY"],
  "3000": ["Owner's Equity", "EQUITY"],
  "4000": ["Product Revenue", "REVENUE"],
  "4100": ["Delivery Revenue", "REVENUE"],
  "5000": ["Cost of Goods Sold", "EXPENSE"],
  "6100": ["Yard Payroll", "EXPENSE"],
};

const FY = new Date().getUTCFullYear();
const PERIODS_PER_YEAR = 12;
// Postings run through this period of the current year; the later periods exist and are open and empty.
const LAST_POSTED = 6;
const CLOSED_THROUGH = 5;

const normal = (type) => (type === "ASSET" || type === "EXPENSE" ? "DEBIT" : "CREDIT");

function buildBooks() {
  const lines = [];
  let entryNo = 0;
  let seq = 0;
  const post = (year, month, branch, source, memo, postings) => {
    entryNo += 1;
    const journalId = `7a1e0000-0000-4000-9000-${String(entryNo).padStart(12, "0")}`;
    for (const [code, debit, credit] of postings) {
      seq += 1;
      lines.push({
        line_id: `7a1f0000-0000-4000-9000-${String(seq).padStart(12, "0")}`,
        journal_id: journalId,
        entry_no: entryNo,
        source,
        period_year: year,
        period_month: month,
        account_code: code,
        account_name: CHART[code][0],
        account_type: CHART[code][1],
        branch_id: branch,
        debit_cents: debit,
        credit_cents: credit,
        memo,
        posted_at: new Date(Date.UTC(year, month - 1, 15, 9, 0, 0)).toISOString(),
      });
    }
  };
  for (const year of [FY - 1, FY]) {
    const last = year === FY ? LAST_POSTED : PERIODS_PER_YEAR;
    // The prior year ran a little smaller, so the comparison has something to say.
    const scale = year === FY ? 100 : 86;
    for (let month = 1; month <= last; month += 1) {
      for (const [bi, branch] of [[0, GL_IDS.trenton], [1, GL_IDS.kingston]]) {
        const rev = Math.round(((820000 + month * 41000 + bi * 133000) * scale) / 100);
        const delivery = Math.round(((46000 + bi * 12000) * scale) / 100);
        const tax = Math.round(((rev + delivery) * 775) / 10000);
        const paid = Math.round(((rev + delivery + tax) * (68 - bi * 6)) / 100);
        const cogs = Math.round((rev * 61) / 100);
        const payroll = Math.round(((210000 + bi * 38000) * scale) / 100);
        if (month === 1 && bi === 0) post(year, month, branch, "MANUAL", "Opening balances", [["1000", 4000000, 0], ["3000", 0, 4000000]]);
        post(year, month, branch, "INVOICE", "Invoice posted", [["1100", rev + delivery + tax, 0], ["4000", 0, rev], ["4100", 0, delivery], ["2200", 0, tax]]);
        post(year, month, branch, "PAYMENT", "Payment received", [["1000", paid, 0], ["1100", 0, paid]]);
        post(year, month, branch, "FULFILLMENT", "Cost of goods shipped", [["5000", cogs, 0], ["1200", 0, cogs]]);
        post(year, month, branch, "MANUAL", "Yard payroll", [["6100", payroll, 0], ["1000", 0, payroll]]);
      }
    }
  }
  return lines;
}

function buildPeriods() {
  const items = [];
  for (const year of [FY - 1, FY]) {
    for (let month = 1; month <= PERIODS_PER_YEAR; month += 1) {
      const closed = year < FY || month <= CLOSED_THROUGH;
      items.push({
        id: `9e000000-0000-4000-9000-${String(year % 100).padStart(2, "0")}${String(month).padStart(10, "0")}`,
        year,
        month,
        status: closed ? "CLOSED" : "OPEN",
        ...(closed ? { closed_at: new Date(Date.UTC(year, month, 3, 9, 0, 0)).toISOString() } : {}),
      });
    }
  }
  return items;
}

const AGING_BUCKET = (days) => (days <= 0 ? "CURRENT" : days <= 30 ? "1_30" : days <= 60 ? "31_60" : days <= 90 ? "61_90" : "90_PLUS");

function buildAging() {
  const customers = [
    ["c0000000-0000-4000-9000-000000000001", "Summit Ridge Builders"],
    ["c0000000-0000-4000-9000-000000000002", "Ridgeview Outdoor Living"],
    ["c0000000-0000-4000-9000-000000000003", "Fieldstone Pools and Patios"],
  ];
  // [customer, branch, source type, cents, age in days past due]
  const spec = [
    [0, GL_IDS.trenton, "INVOICE", 1284500, 0],
    [0, GL_IDS.trenton, "INVOICE", 640000, 24],
    [0, GL_IDS.kingston, "INVOICE", 215075, 47],
    [1, GL_IDS.trenton, "INVOICE", 905000, 12],
    [1, GL_IDS.kingston, "INVOICE", 330000, 75],
    [2, GL_IDS.kingston, "INVOICE", 478250, 102],
    [2, GL_IDS.trenton, "ADJUSTMENT", -25000, 8],
  ];
  return spec
    .map(([ci, branch, type, cents, days], i) => ({
      customer_id: customers[ci][0],
      customer_name: customers[ci][1],
      source_type: type,
      source_ref_id: `a0000000-0000-4000-9000-${String(i + 1).padStart(12, "0")}`,
      branch_id: branch,
      amount_cents: cents,
      age_days: days,
      bucket: AGING_BUCKET(days),
    }))
    .sort((a, b) => (a.customer_id < b.customer_id ? -1 : a.customer_id > b.customer_id ? 1 : b.age_days - a.age_days));
}

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const REPORTS = ["INCOME_STATEMENT", "BALANCE_SHEET", "TRIAL_BALANCE", "GL_DETAIL", "AR_AGING"];
const CADENCES = ["DAILY", "WEEKLY", "MONTHLY"];

function freshPacks() {
  // Newest first, as the core lists them.
  return [
    { id: IDS.pack2, report: "AR_AGING", recipients: "financial.controller@hh-demo.invalid", cadence: "WEEKLY", branch_id: "", is_active: true },
    { id: IDS.pack1, report: "INCOME_STATEMENT", recipients: "financial.controller@hh-demo.invalid,general.manager@hh-demo.invalid", cadence: "MONTHLY", branch_id: "", is_active: true },
  ];
}

export function createFinstmtWorld() {
  const lines = buildBooks();
  const periods = buildPeriods();
  const aging = buildAging();
  let packs = freshPacks();
  // The stub counts what the screen asked, so a leg can hold "nothing was sent".
  let calls = 0;

  let exports = 0;

  const reset = () => {
    packs = freshPacks();
    calls = 0;
    exports = 0;
  };

  const dollars = (cents) => `${cents < 0 ? "-" : ""}${Math.floor(Math.abs(cents) / 100)}.${String(Math.abs(cents) % 100).padStart(2, "0")}`;
  const csvCell = (v) => (/[",\r\n]/.test(String(v)) ? `"${String(v).replace(/"/g, '""')}"` : String(v));
  /** The core's renderCSV: the title as a one cell row, the headers, then the rows. */
  const renderCsv = (t) => [[t.title], t.headers, ...t.rows].map((r) => r.map(csvCell).join(",") + "\r\n").join("");

  /** JSON by default; with ?format=csv the core's CSV (and its export stamp), any other format its 400. */
  const done = (url, body, table) => {
    const format = url.searchParams.get("format") ?? "";
    if (format === "") return { status: 200, body };
    if (format !== "csv") return bad("format must be csv or xlsx");
    exports += 1;
    return { status: 200, csv: renderCsv(table(body)) };
  };

  /** The caller, read from the stub token's subject. */
  const callerOf = (req) => {
    const token = String(req.headers.authorization ?? "").replace(/^Bearer /, "");
    try {
      const sub = JSON.parse(Buffer.from(token.split(".")[1], "base64url").toString("utf8")).sub;
      if (sub === GL_IDS.controller) return { id: sub, tier: 1, global: true, branches: [] };
      if (sub === IDS.payrec) return { id: sub, tier: 2, global: true, branches: [] };
      if (sub === GL_IDS.manager) return { id: sub, tier: 2, global: false, branches: [GL_IDS.trenton] };
      return { id: sub, tier: 3, global: false, branches: [] };
    } catch {
      return { id: "", tier: 0, global: false, branches: [] };
    }
  };

  const forbidden = { status: 403, body: { error: "Forbidden" } };
  const bad = (error) => ({ status: 400, body: { error } });

  /** The branch fence (core/internal/finstmt/service.go authorizeBranch), word for word. */
  const fence = (caller, branch) => {
    if (branch === "") {
      return caller.global ? null : { status: 403, body: { error: "finstmt: caller cannot access this branch" } };
    }
    if (caller.global || caller.branches.includes(branch)) return null;
    return { status: 403, body: { error: "finstmt: caller cannot access this branch" } };
  };

  // The read behind an unknown branch id is a database error in the core, an opaque 500.
  const knownBranch = (branch) => branch === "" || UUID.test(branch);

  const queryYear = (url) => {
    const raw = url.searchParams.get("year") ?? "";
    if (raw === "") return { error: bad("year is required") };
    const year = /^-?\d+$/.test(raw) ? Number(raw) : Number.NaN;
    if (!Number.isInteger(year) || year < 1900 || year > 9999) return { error: bad("year must be a 4-digit fiscal year") };
    return { year };
  };

  const int = (raw, fallback) => (raw !== null && /^-?\d+$/.test(raw) ? Number(raw) : fallback);

  /** Natural-orientation amounts per account, over a filter of the posted lines. */
  const amounts = (filter, types) => {
    const byCode = new Map();
    for (const l of lines) {
      if (!filter(l) || !types.includes(l.account_type)) continue;
      const row = byCode.get(l.account_code) ?? { debit: 0, credit: 0 };
      row.debit += l.debit_cents;
      row.credit += l.credit_cents;
      byCode.set(l.account_code, row);
    }
    return [...byCode.keys()].sort().map((code) => {
      const { debit, credit } = byCode.get(code);
      const [name, type] = CHART[code];
      return { account_code: code, account_name: name, account_type: type, amount_cents: normal(type) === "DEBIT" ? debit - credit : credit - debit };
    });
  };

  const sum = (rows) => rows.reduce((n, r) => n + r.amount_cents, 0);
  const sameBranch = (branch) => (l) => branch === "" || l.branch_id === branch;

  const incomeRows = (year, month, branch) =>
    amounts((l) => l.period_year === year && (month === 0 || l.period_month === month) && sameBranch(branch)(l), ["REVENUE", "EXPENSE"]);
  const balanceRows = (year, branch) => amounts((l) => l.period_year === year && sameBranch(branch)(l), ["ASSET", "LIABILITY", "EQUITY"]);

  const compare = (cur, prior) => {
    const codes = [...new Set([...cur, ...prior].map((r) => r.account_code))].sort();
    return codes.map((code) => {
      const c = cur.find((r) => r.account_code === code);
      const p = prior.find((r) => r.account_code === code);
      const ref = c ?? p;
      const current_cents = c?.amount_cents ?? 0;
      const prior_cents = p?.amount_cents ?? 0;
      return { account_code: code, account_name: ref.account_name, account_type: ref.account_type, current_cents, prior_cents, delta_cents: current_cents - prior_cents };
    });
  };

  function income(url, caller) {
    const y = queryYear(url);
    if (y.error) return y.error;
    const month = int(url.searchParams.get("month"), 0);
    const branch = url.searchParams.get("branch_id") ?? "";
    const wall = fence(caller, branch);
    if (wall) return wall;
    if (!knownBranch(branch)) return { status: 500, body: { error: "internal error" } };
    if (url.searchParams.get("compare") === "1") {
      return { status: 200, body: { rows: compare(incomeRows(y.year, month, branch), incomeRows(y.year - 1, month, branch)) } };
    }
    const rows = incomeRows(y.year, month, branch);
    const revenue = rows.filter((r) => r.account_type === "REVENUE");
    const expenses = rows.filter((r) => r.account_type === "EXPENSE");
    const body = { year: y.year, month, branch_id: branch, revenue, expenses, revenue_cents: sum(revenue), expense_cents: sum(expenses), net_income_cents: sum(revenue) - sum(expenses) };
    return done(url, body, (r) => ({
      title: "Income Statement",
      headers: ["Section", "Account", "Name", "Amount"],
      rows: [
        ...r.revenue.map((x) => ["REVENUE", x.account_code, x.account_name, dollars(x.amount_cents)]),
        ["", "", "Total Revenue", dollars(r.revenue_cents)],
        ...r.expenses.map((x) => ["EXPENSE", x.account_code, x.account_name, dollars(x.amount_cents)]),
        ["", "", "Total Expenses", dollars(r.expense_cents)],
        ["", "", "Net Income", dollars(r.net_income_cents)],
      ],
    }));
  }

  function balance(url, caller) {
    const y = queryYear(url);
    if (y.error) return y.error;
    const branch = url.searchParams.get("branch_id") ?? "";
    const wall = fence(caller, branch);
    if (wall) return wall;
    if (!knownBranch(branch)) return { status: 500, body: { error: "internal error" } };
    if (url.searchParams.get("compare") === "1") {
      return { status: 200, body: { rows: compare(balanceRows(y.year, branch), balanceRows(y.year - 1, branch)) } };
    }
    const rows = balanceRows(y.year, branch);
    const assets = rows.filter((r) => r.account_type === "ASSET");
    const liabilities = rows.filter((r) => r.account_type === "LIABILITY");
    const equity = rows.filter((r) => r.account_type === "EQUITY");
    const net = sum(incomeRows(y.year, 0, branch).filter((r) => r.account_type === "REVENUE")) - sum(incomeRows(y.year, 0, branch).filter((r) => r.account_type === "EXPENSE"));
    const body = {
      year: y.year,
      branch_id: branch,
      assets,
      liabilities,
      equity,
      total_assets_cents: sum(assets),
      total_liabilities_cents: sum(liabilities),
      total_equity_cents: sum(equity),
      net_income_cents: net,
      balanced: sum(assets) === sum(liabilities) + sum(equity) + net,
    };
    return done(url, body, (r) => {
      const add = (section, list) => list.map((x) => [section, x.account_code, x.account_name, dollars(x.amount_cents)]);
      return {
        title: "Balance Sheet",
        headers: ["Section", "Account", "Name", "Amount"],
        rows: [
          ...add("ASSET", r.assets), ["", "", "Total Assets", dollars(r.total_assets_cents)],
          ...add("LIABILITY", r.liabilities), ["", "", "Total Liabilities", dollars(r.total_liabilities_cents)],
          ...add("EQUITY", r.equity), ["", "", "Total Equity", dollars(r.total_equity_cents)],
          ["", "", "Net Income", dollars(r.net_income_cents)],
        ],
      };
    });
  }

  function trial(url, caller) {
    const y = queryYear(url);
    if (y.error) return y.error;
    // No branch dimension: only a global caller reads the consolidated ledger.
    const wall = fence(caller, "");
    if (wall) return wall;
    const byCode = new Map();
    for (const l of lines) {
      if (l.period_year !== y.year) continue;
      const row = byCode.get(l.account_code) ?? { debit: 0, credit: 0 };
      row.debit += l.debit_cents;
      row.credit += l.credit_cents;
      byCode.set(l.account_code, row);
    }
    const rows = [...byCode.keys()].sort().map((code) => {
      const { debit, credit } = byCode.get(code);
      const [name, type] = CHART[code];
      return { account_code: code, account_name: name, account_type: type, normal_balance: normal(type), total_debit_cents: debit, total_credit_cents: credit, balance_cents: debit - credit };
    });
    const total_debit_cents = rows.reduce((n, r) => n + r.total_debit_cents, 0);
    const total_credit_cents = rows.reduce((n, r) => n + r.total_credit_cents, 0);
    const body = { year: y.year, rows, total_debit_cents, total_credit_cents, in_balance: total_debit_cents === total_credit_cents };
    return done(url, body, (r) => ({
      title: "Trial Balance",
      headers: ["Account", "Name", "Type", "Debit", "Credit"],
      rows: [...r.rows.map((x) => [x.account_code, x.account_name, x.account_type, dollars(x.total_debit_cents), dollars(x.total_credit_cents)]), ["", "TOTAL", "", dollars(r.total_debit_cents), dollars(r.total_credit_cents)]],
    }));
  }

  function glDetail(url, caller) {
    const year = int(url.searchParams.get("year"), 0);
    const month = int(url.searchParams.get("month"), 0);
    const code = url.searchParams.get("account_code") ?? "";
    const branch = url.searchParams.get("branch_id") ?? "";
    const wall = fence(caller, branch);
    if (wall) return wall;
    if (!knownBranch(branch)) return { status: 500, body: { error: "internal error" } };
    const rows = lines
      .filter((l) => (year === 0 || l.period_year === year) && (month === 0 || l.period_month === month) && (code === "" || l.account_code === code) && (branch === "" || l.branch_id === branch))
      .sort((a, b) => (a.posted_at < b.posted_at ? -1 : a.posted_at > b.posted_at ? 1 : a.entry_no - b.entry_no || (a.line_id < b.line_id ? -1 : 1)));
    const debit = rows.reduce((n, r) => n + r.debit_cents, 0);
    const credit = rows.reduce((n, r) => n + r.credit_cents, 0);
    const type = url.searchParams.get("account_type") ?? "";
    const tie_cents = type === "REVENUE" || type === "LIABILITY" || type === "EQUITY" ? credit - debit : debit - credit;
    return done(url, { rows, tie_cents }, (r) => ({
      title: "GL Detail",
      headers: ["Entry", "Posted", "Account", "Name", "Branch", "Debit", "Credit", "Memo"],
      rows: r.rows.map((x) => [String(x.entry_no), x.posted_at, x.account_code, x.account_name, x.branch_id, dollars(x.debit_cents), dollars(x.credit_cents), x.memo]),
    }));
  }

  function arAging(url, caller) {
    const branch = url.searchParams.get("branch_id") ?? "";
    const wall = fence(caller, branch);
    if (wall) return wall;
    if (!knownBranch(branch)) return { status: 500, body: { error: "internal error" } };
    const rows = aging.filter((r) => branch === "" || r.branch_id === branch);
    const order = ["CURRENT", "1_30", "31_60", "61_90", "90_PLUS"];
    const buckets = order.map((bucket) => ({ bucket, cents: rows.filter((r) => r.bucket === bucket).reduce((n, r) => n + r.amount_cents, 0) }));
    const body = { branch_id: branch, buckets, rows, grand_total_cents: rows.reduce((n, r) => n + r.amount_cents, 0) };
    return done(url, body, (r) => ({
      title: "AR Aging",
      headers: ["Customer", "Source", "Ref", "Branch", "Bucket", "AgeDays", "Amount"],
      rows: [...r.rows.map((x) => [x.customer_id, x.source_type, x.source_ref_id, x.branch_id, x.bucket, String(x.age_days), dollars(x.amount_cents)]), ["", "", "", "", "GRAND TOTAL", "", dollars(r.grand_total_cents)]],
    }));
  }

  /** The service's checks on a pack, in the core's order (validateScheduleInput). */
  function checkPack(input) {
    const report = String(input.report ?? "").trim().toUpperCase();
    if (!REPORTS.includes(report)) return bad("report must be one of INCOME_STATEMENT, BALANCE_SHEET, TRIAL_BALANCE, GL_DETAIL, AR_AGING");
    const cadence = String(input.cadence ?? "").trim().toUpperCase();
    if (!CADENCES.includes(cadence)) return bad("cadence must be one of DAILY, WEEKLY, MONTHLY");
    const recipients = String(input.recipients ?? "").split(",").map((r) => r.trim()).filter((r) => r !== "");
    if (recipients.length === 0) return bad("at least one recipient e-mail is required");
    for (const address of recipients) {
      if (!/^[^\s@]+@[^\s@]+$/.test(address)) return bad(`invalid recipient e-mail: ${JSON.stringify(address)}`);
    }
    return { report, cadence, recipients: recipients.join(","), branch_id: String(input.branch_id ?? "").trim(), is_active: input.is_active === true };
  }

  function parse(bodyText) {
    try {
      const v = JSON.parse(bodyText);
      return v !== null && typeof v === "object" && !Array.isArray(v) ? v : null;
    } catch {
      return null;
    }
  }

  /**
   * Answer one request or return null when it is not this world's. The shape is { status, body }, the body
   * already a JSON value; a 204 carries no body.
   */
  function handle(req, url, bodyText) {
    const path = url.pathname;
    const method = req.method;
    if (method === "POST" && path === "/_stub/reset") {
      reset();
      return { status: 200, body: { ok: true } };
    }
    const caller = callerOf(req);
    if (method === "GET" && path === "/api/v1/gl/periods") {
      // The ledger's period list is the manager's read as well.
      if (caller.tier < 1 || caller.tier > 2) return forbidden;
      calls += 1;
      const limit = int(url.searchParams.get("limit"), 50) || 50;
      const cursor = url.searchParams.get("cursor");
      const from = cursor === null ? 0 : periods.findIndex((p) => p.id === cursor) + 1;
      const items = periods.slice(from, from + limit);
      const more = from + limit < periods.length;
      return { status: 200, body: { items, next_cursor: more ? items[items.length - 1].id : null, limit } };
    }
    if (!path.startsWith("/api/v1/finstmt/")) return null;
    // Every finstmt route sits behind the manager gate; the schedule writes behind the admin gate.
    if (caller.tier < 1 || caller.tier > 2) return forbidden;
    calls += 1;
    if (method === "GET" && path === "/api/v1/finstmt/income-statement") return income(url, caller);
    if (method === "GET" && path === "/api/v1/finstmt/balance-sheet") return balance(url, caller);
    if (method === "GET" && path === "/api/v1/finstmt/trial-balance") return trial(url, caller);
    if (method === "GET" && path === "/api/v1/finstmt/gl-detail") return glDetail(url, caller);
    if (method === "GET" && path === "/api/v1/finstmt/ar-aging") return arAging(url, caller);
    if (method === "GET" && path === "/api/v1/finstmt/schedules") {
      // The core writes the list under "schedules" today (its shape census records the contract's "items").
      return { status: 200, body: { schedules: packs } };
    }
    const admin = caller.tier === 1 && caller.global;
    if (method === "POST" && path === "/api/v1/finstmt/schedules") {
      if (!admin) return forbidden;
      const input = parse(bodyText);
      if (input === null) return bad("invalid_json");
      const checked = checkPack(input);
      if (checked.status) return checked;
      const pack = { id: randomUUID(), ...checked };
      packs = [pack, ...packs];
      return { status: 201, body: pack };
    }
    const one = path.match(/^\/api\/v1\/finstmt\/schedules\/([^/]+)$/);
    if (one && method === "PUT") {
      if (!admin) return forbidden;
      const input = parse(bodyText);
      if (input === null) return bad("invalid_json");
      // The service rejects a malformed id before it validates the input.
      if (!UUID.test(one[1])) return bad("id must be a uuid");
      const checked = checkPack(input);
      if (checked.status) return checked;
      const at = packs.findIndex((p) => p.id === one[1]);
      if (at < 0) return { status: 404, body: { error: "not found" } };
      packs[at] = { id: one[1], ...checked };
      return { status: 200, body: packs[at] };
    }
    if (one && method === "DELETE") {
      if (!admin) return forbidden;
      if (!UUID.test(one[1])) return bad("id must be a uuid");
      const at = packs.findIndex((p) => p.id === one[1]);
      if (at < 0) return { status: 404, body: { error: "not found" } };
      packs.splice(at, 1);
      return { status: 204, body: null };
    }
    return null;
  }

  return { handle, reset, fiscalYear: FY, calls: () => calls, exports: () => exports };
}
