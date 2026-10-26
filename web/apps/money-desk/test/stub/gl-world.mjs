// The ledger slice's stub world: the Financial Controller's approvals queue,
// the journals, the trial balance and the branches, answered the way the core
// answers them. This is a test double only; the real routes are the core's
// (core/internal/identity/staffcontext approvals, core/internal/gl). Its rules
// and its refusal sentences are held to the contract and to the core's own
// source by test/gl-stub-contract.test.mjs, so it cannot drift in silence.
//
// The world is the demo world's (core/cmd/core/demoseed_controller.go and the
// quote world's FR1): a below floor quote request filed by Sales, a parked
// journal made by the Sales manager, a parked journal the controller made
// itself, and two freight raises. The controller decides all but its own.

export const IDS = {
  // The controller's stub id is the receivables stub's (the one stub serves both slices); the core's own
  // account is de110000-0000-4000-9000-0000000000c1.
  controller: "de110000-0000-4000-9000-000000000008",
  sales: "de110000-0000-4000-9000-000000000001",
  manager: "de110000-0000-4000-9000-000000000002",
  pointLoma: "b0000000-0000-4000-8000-000000000001",
  julian: "b0000000-0000-4000-8000-000000000002",
  fr1: "2f6a1c00-0000-4000-9000-0000000000f1",
  j1Request: "2f6a1c00-0000-4000-9000-0000000000a1",
  j2Request: "2f6a1c00-0000-4000-9000-0000000000a2",
  freight1: "2f6a1c00-0000-4000-9000-0000000000b1",
  freight2: "2f6a1c00-0000-4000-9000-0000000000b2",
  j1: "7a1e0000-0000-4000-9000-000000000001",
  j2: "7a1e0000-0000-4000-9000-000000000002",
  payRec: "de110000-0000-4000-9000-000000000007",
  // Period ids carry their month in the last two digits; the stub's clock stands in the ninth.
  period: (month) => `9e000000-0000-4000-9000-0000000000${String(month).padStart(2, "0")}`,
  recurringRent: "6c000000-0000-4000-9000-000000000001",
  recurringInsurance: "6c000000-0000-4000-9000-000000000002",
};

// The stub's chart, code to [name, type]. Normal balance follows the type, as the core's seed has it.
const CHART = {
  "1000": ["Cash", "ASSET"],
  "1100": ["Accounts Receivable", "ASSET"],
  "1200": ["Inventory", "ASSET"],
  "1300": ["Prepaid Expenses", "ASSET"],
  "2100": ["Accounts Payable", "LIABILITY"],
  "3000": ["Owner's Equity", "EQUITY"],
  "4000": ["Product Revenue", "REVENUE"],
  "5000": ["Cost of Goods Sold", "EXPENSE"],
  "3900": ["Retained Earnings", "EQUITY"],
  "5200": ["Inventory Adjustments", "EXPENSE"],
};
const RETAINED_EARNINGS = "3900";
// The approval threshold the stub's policy row carries: the org default of the core's seed.
const THRESHOLD_CENTS = 500000;
const NOW = Date.UTC(2026, 8, 1, 9, 0, 0);

const FY = 2026;
const stamp = (hoursAgo) => new Date(Date.UTC(2026, 8, 1, 9, 0, 0) - hoursAgo * 3_600_000).toISOString();

function journal(id, entryNo, source, memo, status, branch, createdBy, hoursAgo, lines, extra = {}) {
  const j = {
    id,
    fiscal_year: FY,
    entry_no: entryNo,
    period_id: IDS.period(new Date(NOW - hoursAgo * 3_600_000).getUTCMonth() + 1),
    source,
    memo,
    status,
    derived_status: status === "DRAFT" ? "PENDING_APPROVAL" : status,
    branch_id: branch,
    created_by: createdBy,
    created_at: stamp(hoursAgo),
    lines: lines.map(([code, debit, credit, lineMemo], i) => ({
      line_no: i + 1,
      account_code: code,
      account_name: CHART[code][0],
      debit_cents: debit,
      credit_cents: credit,
      ...(lineMemo ? { memo: lineMemo } : {}),
    })),
    ...extra,
  };
  if (status === "POSTED") j.posted_at = stamp(hoursAgo);
  return j;
}

const periodRows = () =>
  Array.from({ length: 12 }, (_, i) => {
    const month = i + 1;
    // January to July are sealed history; August on is open.
    return month <= 7
      ? { id: IDS.period(month), year: FY, month, status: "CLOSED", closed_at: new Date(Date.UTC(FY, month, 1)).toISOString() }
      : { id: IDS.period(month), year: FY, month, status: "OPEN" };
  });

const accountRows = () =>
  Object.entries(CHART).map(([code, [name, type]], i) => ({
    id: `a0000000-0000-4000-9000-${String(i + 1).padStart(12, "0")}`,
    code,
    name,
    account_type: type,
    normal_balance: type === "ASSET" || type === "EXPENSE" ? "DEBIT" : "CREDIT",
    is_active: true,
  }));

function fresh() {
  return {
    // The dealer's legacy import source, as the surface config serves it. The first dealer names its
    // legacy system; "" is a dealer with none (the demo tenant), whose check is off.
    legacyImport: "bistrack",
    accounts: accountRows(),
    periods: periodRows(),
    // The first time the AR tie-out was seen out of tie, by branch key; the core persists it (gl_tieout_state).
    tieSince: {},
    nextEntryNo: 7,
    recurring: [
      {
        id: IDS.recurringRent,
        name: "Yard lease accrual",
        memo: "Monthly yard lease accrual",
        schedule: "MONTHLY",
        is_active: true,
        lines: [
          { account_code: "5200", debit_cents: 180000, credit_cents: 0, memo: "Lease expense" },
          { account_code: "2100", debit_cents: 0, credit_cents: 180000, memo: "Lease payable" },
        ],
      },
      {
        id: IDS.recurringInsurance,
        name: "Insurance prepaid release",
        memo: "",
        schedule: "MONTHLY",
        is_active: true,
        lines: [
          { account_code: "5200", debit_cents: 650000, credit_cents: 0, memo: "Insurance expense" },
          { account_code: "1300", debit_cents: 0, credit_cents: 650000, memo: "Prepaid released" },
        ],
      },
    ],
    approvals: [
      {
        id: IDS.fr1,
        requested_by: IDS.sales,
        branch_id: IDS.pointLoma,
        policy_type: "MARGIN_BELOW_MINIMUM",
        detail: "quote DEMO-PLM-Q1 line 1 (62e4ad55-5a14-5945-80ee-866864a6c774): 12 percent discount, resulting margin 1630 bps against the 2000 bps floor",
        subject_ref: "5c7d0e0a-0000-4000-9000-000000000001",
        status: "PENDING",
      },
      {
        id: IDS.j1Request,
        requested_by: IDS.manager,
        branch_id: IDS.pointLoma,
        policy_type: "JE_OVER_THRESHOLD",
        detail: "Journal entry at/over the maker-checker threshold (750000 cents) awaiting approval",
        subject_ref: IDS.j1,
        amount_cents: 750000,
        status: "PENDING",
      },
      {
        id: IDS.j2Request,
        requested_by: IDS.controller,
        branch_id: IDS.pointLoma,
        policy_type: "JE_OVER_THRESHOLD",
        detail: "Journal entry at/over the maker-checker threshold (620000 cents) awaiting approval",
        subject_ref: IDS.j2,
        amount_cents: 620000,
        status: "PENDING",
      },
      {
        id: IDS.freight1,
        requested_by: "de110000-0000-4000-9000-0000000000d1",
        branch_id: IDS.julian,
        policy_type: "FREIGHT_ABOVE_QUOTE",
        detail: "Freight on DEMO-JUL-O01 for Contractor account (demo) is being raised above the delivery the customer was quoted.",
        subject_ref: "6b2c0000-0000-4000-9000-0000000000b1",
        status: "PENDING",
      },
      {
        id: IDS.freight2,
        requested_by: "de110000-0000-4000-9000-0000000000d1",
        branch_id: IDS.pointLoma,
        policy_type: "FREIGHT_ABOVE_QUOTE",
        detail: "Freight on DEMO-PLM-O03 for Builder account (demo) is being raised above the delivery the customer was quoted.",
        subject_ref: "6b2c0000-0000-4000-9000-0000000000b2",
        status: "PENDING",
      },
    ],
    journals: [
      journal(IDS.j1, 5, "MANUAL", "Insurance premium paid ahead for the year (demo)", "DRAFT", IDS.pointLoma, IDS.manager, 30, [
        ["1300", 750000, 0],
        ["1000", 0, 750000],
      ]),
      journal(IDS.j2, 6, "MANUAL", "Inventory write-down after the cycle count (demo)", "DRAFT", IDS.pointLoma, IDS.controller, 12, [
        ["5200", 620000, 0],
        ["1200", 0, 620000],
      ]),
      // A balance sheet move only, so the asserted net income and the 1100 tie-out figures stand.
      journal("7a1e0000-0000-4000-9000-000000000007", 7, "MANUAL", "Julian prepaid transfer (demo)", "POSTED", IDS.julian, IDS.controller, 90, [
        ["1300", 4000, 0, "prepaid"],
        ["1000", 0, 4000, "cash"],
      ]),
      journal("7a1e0000-0000-4000-9000-000000000004", 4, "ADJUSTMENT", "Till over/short", "POSTED", IDS.pointLoma, IDS.manager, 200, [
        ["1000", 2500, 0],
        ["5200", 0, 2500],
      ]),
      journal("7a1e0000-0000-4000-9000-000000000003", 3, "PAYMENT", "Payment received", "POSTED", IDS.pointLoma, null, 300, [
        ["1000", 500000, 0],
        ["1100", 0, 500000],
      ]),
      journal("7a1e0000-0000-4000-9000-000000000002", 2, "INVOICE", "Invoice posted", "POSTED", IDS.pointLoma, null, 400, [
        ["1100", 1200000, 0],
        ["4000", 0, 1200000],
      ]),
      journal("7a1e0000-0000-4000-9000-000000000001", 1, "MANUAL", "Opening balances", "POSTED", IDS.pointLoma, IDS.manager, 500, [
        ["1000", 2000000, 0],
        ["3000", 0, 2000000],
      ]),
    ].map((j) => (j.created_by === null ? Object.fromEntries(Object.entries(j).filter(([k]) => k !== "created_by")) : j)),
  };
}

export function createGlWorld() {
  let world = fresh();

  const reset = () => {
    world = fresh();
  };

  const monthOf = (j) => world.periods.find((p) => p.id === j.period_id)?.month ?? 0;

  const trialBalance = (branch, year = FY, month = 0) => {
    const byCode = new Map();
    for (const j of world.journals) {
      if (j.status !== "POSTED" || j.fiscal_year !== year) continue;
      if (month > 0 && monthOf(j) !== month) continue;
      if (branch !== null && branch !== "" && j.branch_id !== branch) continue;
      for (const l of j.lines) {
        const row = byCode.get(l.account_code) ?? { debit: 0, credit: 0 };
        row.debit += l.debit_cents;
        row.credit += l.credit_cents;
        byCode.set(l.account_code, row);
      }
    }
    const rows = [...byCode.keys()].sort().map((code) => {
      const { debit, credit } = byCode.get(code);
      const [name, type] = CHART[code];
      return {
        account_code: code,
        account_name: name,
        account_type: type,
        normal_balance: type === "ASSET" || type === "EXPENSE" ? "DEBIT" : "CREDIT",
        debit_cents: debit,
        credit_cents: credit,
        balance_cents: debit - credit,
      };
    });
    const total_debit_cents = rows.reduce((n, r) => n + r.debit_cents, 0);
    const total_credit_cents = rows.reduce((n, r) => n + r.credit_cents, 0);
    return {
      fiscal_year: year,
      ...(month > 0 ? { month } : {}),
      ...(branch ? { branch_id: branch } : {}),
      rows,
      total_debit_cents,
      total_credit_cents,
      balance_cents: total_debit_cents - total_credit_cents,
    };
  };

  const branches = () => [
    { id: IDS.julian, code: "JUL", name: "Julian", address: { line1: "1 Demo Road", city: "Julian", province: "CA", postal: "92036", country: "US" }, is_default: false, is_system_default: false, is_active: true },
    { id: IDS.pointLoma, code: "PLM", name: "Point Loma", address: { line1: "2 Demo Road", city: "Point Loma", province: "CA", postal: "92106", country: "US" }, is_default: true, is_system_default: true, is_active: true },
  ];

  /** The caller, read from the stub token's subject. The level is the role's configured level on the Money Desk. */
  const callerOf = (req) => {
    const token = String(req.headers.authorization ?? "").replace(/^Bearer /, "");
    try {
      const sub = JSON.parse(Buffer.from(token.split(".")[1], "base64url").toString("utf8")).sub;
      if (sub === IDS.controller) return { id: sub, role: "FINANCIAL_CONTROLLER", tier: 1, global: true, level: "admin" };
      if (sub === IDS.payRec) return { id: sub, role: "PAYABLES_RECEIVABLES", tier: 2, global: true, level: "work" };
      if (sub === IDS.manager) return { id: sub, role: "SALES_MANAGER", tier: 2, global: false, level: "read" };
      return { id: sub, role: "INSIDE_SALES", tier: 3, global: false, level: "read" };
    } catch {
      return { id: "", role: "", tier: 0, global: false, level: "" };
    }
  };

  const RANK = { read: 1, work: 2, manage: 3, admin: 4 };
  /** The role gate: a caller below the route's level gets the gate's shared body, never a sentence. */
  const gate = (caller, need) => ((RANK[caller.level] ?? 0) >= RANK[need] ? null : { status: 403, body: { error: "Forbidden" } });
  const say = (status, error) => ({ status, body: { error } });
  const badBranch = (url) => {
    const b = url.searchParams.get("branch_id") ?? "";
    return b !== "" && !UUID.test(b) ? say(400, "invalid branch_id") : null;
  };

  /** The branch reach fence (FW1-10), the core's ListJournals rule: a branch-scoped caller (the
   *  Sales manager, Point Loma) must name a branch it holds, and an unnamed branch is the whole
   *  company's books, a dealer wide caller's read. */
  const reach = (caller, url) => {
    if (caller.global) return null;
    return (url.searchParams.get("branch_id") ?? "") === IDS.pointLoma ? null : say(403, "gl: caller cannot access this branch");
  };

  const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;


  // ---- the rest of the ledger: chart, journals, recurring, periods, checks, policy, export ----------

  const derived = (j) => (j.status === "DRAFT" && totalOf(j) >= THRESHOLD_CENTS ? "PENDING_APPROVAL" : j.status);
  const totalOf = (j) => j.lines.reduce((n, l) => n + l.debit_cents, 0);
  const refreshDerived = (j) => {
    j.derived_status = j.status === "VOID" ? "VOID" : derived(j);
    return j;
  };
  const periodOfNow = () => world.periods.find((p) => p.month === 9);

  /** The core's repository.InsertJournal checks, in its order: period, then each line's account. */
  function insertJournal(draft) {
    const period = periodOfNow();
    if (period === undefined) return say(422, "gl: no fiscal period for 2026-09");
    if (period.status === "CLOSED") return say(409, "gl: fiscal period is CLOSED");
    for (const line of draft.lines) {
      const account = world.accounts.find((a) => a.code === line.account_code);
      if (account === undefined) return say(422, `gl: unknown account code "${line.account_code}"`);
      if (!account.is_active) return say(422, `gl: account "${line.account_code}" is inactive; cannot post`);
    }
    const entryNo = world.nextEntryNo;
    world.nextEntryNo += 1;
    const id = `7a1e0000-0000-4000-9000-${String(100 + entryNo).padStart(12, "0")}`;
    const j = {
      id,
      fiscal_year: FY,
      entry_no: entryNo,
      period_id: period.id,
      source: draft.source,
      ...(draft.source_ref_id ? { source_ref_id: draft.source_ref_id } : {}),
      memo: draft.memo,
      status: draft.status,
      derived_status: draft.status,
      ...(draft.status === "POSTED" ? { posted_at: new Date(NOW).toISOString() } : {}),
      ...(draft.branch_id ? { branch_id: draft.branch_id } : {}),
      ...(draft.reverses_journal_id ? { reverses_journal_id: draft.reverses_journal_id } : {}),
      created_by: draft.created_by,
      created_at: new Date(NOW).toISOString(),
      lines: draft.lines.map((l, i) => ({
        line_no: i + 1,
        account_code: l.account_code,
        account_name: world.accounts.find((a) => a.code === l.account_code).name,
        debit_cents: l.debit_cents,
        credit_cents: l.credit_cents,
        ...(l.memo ? { memo: l.memo } : {}),
      })),
    };
    refreshDerived(j);
    world.journals.push(j);
    return { journal: j };
  }

  function books(req, url, path, bodyText, caller) {
    const m = req.method;
    let body = {};
    if (m === "POST") {
      try {
        body = bodyText === "" ? {} : JSON.parse(bodyText);
      } catch {
        return say(400, "invalid_json");
      }
    }

    // chart of accounts
    if (m === "GET" && path === "/api/v1/gl/accounts") {
      const refused = gate(caller, "read");
      return refused ?? { status: 200, body: { items: world.accounts } };
    }
    if (m === "POST" && path === "/api/v1/gl/accounts") {
      const refused = gate(caller, "manage");
      if (refused !== null) return refused;
      const code = String(body.code ?? "").trim();
      const name = String(body.name ?? "").trim();
      const type = String(body.account_type ?? "").trim().toUpperCase();
      const normal = String(body.normal_balance ?? "").trim().toUpperCase();
      if (code === "" || name === "") return say(400, "gl: account code and name are required");
      if (!["ASSET", "LIABILITY", "EQUITY", "REVENUE", "EXPENSE"].includes(type)) return say(400, "gl: account_type must be one of ASSET/LIABILITY/EQUITY/REVENUE/EXPENSE");
      if (normal !== "DEBIT" && normal !== "CREDIT") return say(400, "gl: normal_balance must be DEBIT or CREDIT");
      if (world.accounts.some((a) => a.code === code)) return say(409, `gl: account code "${code}" already exists`);
      const account = { id: `a0000000-0000-4000-9000-${String(world.accounts.length + 1).padStart(12, "0")}`, code, name, account_type: type, normal_balance: normal, is_active: true };
      if (typeof body.parent_id === "string" && body.parent_id !== "") account.parent_id = body.parent_id;
      world.accounts.push(account);
      CHART[code] = [name, type];
      return { status: 201, body: account };
    }
    const deactivate = path.match(/^\/api\/v1\/gl\/accounts\/([^/]+)\/deactivate$/);
    if (m === "POST" && deactivate) {
      const refused = gate(caller, "manage");
      if (refused !== null) return refused;
      const account = world.accounts.find((a) => a.code === decodeURIComponent(deactivate[1]).trim());
      if (account === undefined) return say(404, "gl: not found");
      const inUse = world.journals.some((j) => j.status === "DRAFT" && j.lines.some((l) => l.account_code === account.code));
      if (inUse) return say(409, "gl: account is referenced by DRAFT journal(s); cannot deactivate");
      account.is_active = false;
      return { status: 200, body: account };
    }

    // journals
    if (m === "POST" && path === "/api/v1/gl/journals") {
      const refused = gate(caller, "manage");
      if (refused !== null) return refused;
      const lines = Array.isArray(body.lines) ? body.lines : [];
      if (lines.length < 2) return say(400, "gl: a journal needs at least two lines");
      for (const l of lines) {
        if ((l.debit_cents > 0) === (l.credit_cents > 0)) return say(400, "gl: each line must have exactly one of debit_cents / credit_cents > 0");
        if (!l.account_code) return say(400, "gl: each line needs an account_code");
      }
      const debit = lines.reduce((n, l) => n + (l.debit_cents || 0), 0);
      const credit = lines.reduce((n, l) => n + (l.credit_cents || 0), 0);
      if (debit !== credit) return say(422, "gl: journal is unbalanced (\u03a3debit \u2260 \u03a3credit)");
      const source = body.source || "MANUAL";
      const parked = debit >= THRESHOLD_CENTS;
      if (parked && source !== "MANUAL") {
        return say(400, `gl: only a manual journal can be parked for approval at or over the approval threshold; a "${source}" journal at this amount cannot be created here`);
      }
      if (parked && !body.branch_id) {
        return say(400, "gl: a journal at or over the approval threshold needs a branch so its approval request can be filed; choose a branch");
      }
      const made = insertJournal({
        source,
        memo: body.memo ?? "",
        status: parked ? "DRAFT" : "POSTED",
        branch_id: body.branch_id || "",
        created_by: caller.id,
        lines: lines.map((l) => ({ account_code: l.account_code, debit_cents: l.debit_cents || 0, credit_cents: l.credit_cents || 0, memo: l.memo || "" })),
      });
      if (made.journal === undefined) return made;
      if (parked) {
        world.approvals.push({
          id: `2f6a1c00-0000-4000-9000-${String(world.approvals.length + 1).padStart(12, "0")}`,
          requested_by: caller.id,
          branch_id: body.branch_id,
          policy_type: "JE_OVER_THRESHOLD",
          detail: `Journal entry at/over the maker-checker threshold (${debit} cents) awaiting approval`,
          subject_ref: made.journal.id,
          amount_cents: debit,
          status: "PENDING",
        });
      }
      return { status: 201, body: made.journal };
    }
    const act = path.match(/^\/api\/v1\/gl\/journals\/([^/]+)\/(approve|void|reverse)$/);
    if (m === "POST" && act) {
      const refused = gate(caller, "manage");
      if (refused !== null) return refused;
      if (!UUID.test(act[1])) return say(400, "invalid id");
      const j = world.journals.find((x) => x.id === act[1].toLowerCase());
      if (j === undefined) return say(404, "gl: not found");
      if (act[2] === "void") {
        if (j.status !== "DRAFT") return say(409, "gl: only a DRAFT journal can be voided (POSTED journals must be reversed)");
        j.status = "VOID";
        j.derived_status = "VOID";
        return { status: 200, body: j };
      }
      if (act[2] === "reverse") {
        if (j.status !== "POSTED") return say(409, `gl: only a POSTED journal can be reversed (status=${j.status})`);
        const made = insertJournal({
          source: "ADJUSTMENT",
          memo: `Reversal of entry #${j.entry_no}`,
          status: "POSTED",
          branch_id: j.branch_id || "",
          reverses_journal_id: j.id,
          created_by: caller.id,
          lines: j.lines.map((l) => ({ account_code: l.account_code, debit_cents: l.credit_cents, credit_cents: l.debit_cents, memo: "reversal" })),
        });
        return made.journal === undefined ? made : { status: 201, body: made.journal };
      }
      // approve, the checker half: the core's order is state, four eyes, then what the approvals queue holds.
      if (j.status !== "DRAFT") return say(409, `gl: journal is ${j.status}, not DRAFT, cannot approve`);
      if (j.created_by !== undefined && j.created_by === caller.id) return say(403, "gl: an approver must differ from the journal's creator (four-eyes)");
      const request = world.approvals.find((a) => a.subject_ref === j.id);
      if (request === undefined) {
        if (totalOf(j) < THRESHOLD_CENTS) {
          j.status = "POSTED";
          j.approved_by = caller.id;
          j.posted_at = new Date(NOW).toISOString();
          refreshDerived(j);
          return { status: 200, body: j };
        }
        if (j.source === "MANUAL" && j.source_ref_id) {
          const template = world.recurring.find((t) => t.id === j.source_ref_id);
          const named = template?.name ?? j.source_ref_id;
          return say(409, `gl: this journal came from the recurring template "${named}" and is over the approval threshold; a recurring run cannot yet file an approval request, so it cannot be posted from here`);
        }
        return say(409, "gl: this journal is over the approval threshold and has no approval request in the approvals queue; it cannot be posted from here");
      }
      if (request.status === "PENDING") return say(409, "gl: this journal has a pending approval request; decide it from the approvals queue, which posts it");
      if (request.status === "REJECTED") return say(409, "gl: this journal's approval request was rejected in the approvals queue; it cannot be posted from here");
      j.status = "POSTED";
      j.approved_by = caller.id;
      j.posted_at = new Date(NOW).toISOString();
      refreshDerived(j);
      return { status: 200, body: j };
    }

    // recurring templates
    if (m === "GET" && path === "/api/v1/gl/recurring") {
      const refused = gate(caller, "read");
      return refused ?? { status: 200, body: { items: world.recurring } };
    }
    const run = path.match(/^\/api\/v1\/gl\/recurring\/([^/]+)\/run$/);
    if (m === "POST" && run) {
      const refused = gate(caller, "manage");
      if (refused !== null) return refused;
      if (!UUID.test(run[1])) return say(400, "invalid id");
      const template = world.recurring.find((t) => t.id === run[1].toLowerCase());
      if (template === undefined) return say(404, "gl: not found");
      const made = insertJournal({
        source: "MANUAL",
        source_ref_id: template.id,
        memo: template.memo === "" ? `Recurring: ${template.name}` : template.memo,
        status: "DRAFT",
        branch_id: "",
        created_by: caller.id,
        lines: template.lines,
      });
      return made.journal === undefined ? made : { status: 201, body: made.journal };
    }

    // periods
    if (m === "GET" && path === "/api/v1/gl/periods") {
      const refused = gate(caller, "read");
      return refused ?? { status: 200, body: { items: world.periods } };
    }
    const close = path.match(/^\/api\/v1\/gl\/periods\/([^/]+)\/(close|reopen)$/);
    if (m === "POST" && close) {
      const refused = gate(caller, close[2] === "close" ? "manage" : "admin");
      if (refused !== null) return refused;
      if (!UUID.test(close[1])) return say(400, "invalid id");
      const period = world.periods.find((p) => p.id === close[1].toLowerCase());
      if (close[2] === "close") {
        if (period === undefined || period.status !== "OPEN") return say(409, "gl: period is not OPEN");
        period.status = "CLOSED";
        period.closed_at = new Date(NOW).toISOString();
        return { status: 200, body: period };
      }
      if (caller.role !== "FINANCIAL_CONTROLLER") return say(403, "gl: only the Financial Controller may reopen a closed period");
      if (String(body.reason ?? "").trim() === "") return say(422, "gl: a reason is required");
      if (period === undefined || period.status !== "CLOSED") return say(409, "gl: period is not CLOSED");
      period.status = "OPEN";
      delete period.closed_at;
      return { status: 200, body: period };
    }
    if (m === "POST" && path === "/api/v1/gl/periods/close-year") {
      const refused = gate(caller, "admin");
      if (refused !== null) return refused;
      const year = Number(body.year ?? 0);
      if (!(year > 0)) return say(400, "gl: year is required");
      const sums = new Map();
      for (const j of world.journals) {
        if (j.status !== "POSTED" || j.fiscal_year !== year) continue;
        for (const l of j.lines) {
          const type = CHART[l.account_code]?.[1];
          if (type !== "REVENUE" && type !== "EXPENSE") continue;
          sums.set(l.account_code, (sums.get(l.account_code) ?? 0) + l.debit_cents - l.credit_cents);
        }
      }
      const lines = [];
      let totalNetDebit = 0;
      for (const [code, net] of [...sums.entries()].sort()) {
        if (net === 0) continue;
        lines.push(net > 0 ? { account_code: code, debit_cents: 0, credit_cents: net, memo: "close to retained earnings" } : { account_code: code, debit_cents: -net, credit_cents: 0, memo: "close to retained earnings" });
        totalNetDebit += net;
      }
      const result = { fiscal_year: year, net_income_cents: -totalNetDebit, periods_closed: true };
      if (lines.length > 0) {
        lines.push(
          totalNetDebit > 0
            ? { account_code: RETAINED_EARNINGS, debit_cents: totalNetDebit, credit_cents: 0, memo: "net loss to retained earnings" }
            : { account_code: RETAINED_EARNINGS, debit_cents: 0, credit_cents: -totalNetDebit, memo: "net income to retained earnings" },
        );
        // The closing entry is dated the last day of the year, so it needs that year's last period open.
        const last = world.periods.find((p) => p.year === year && p.month === 12);
        if (last === undefined) return say(422, `gl: no fiscal period for ${year}-12`);
        if (last.status === "CLOSED") return say(409, "gl: fiscal period is CLOSED");
        const entryNo = world.nextEntryNo;
        world.nextEntryNo += 1;
        const closing = {
          id: `7a1e0000-0000-4000-9000-${String(100 + entryNo).padStart(12, "0")}`,
          fiscal_year: year,
          entry_no: entryNo,
          period_id: last.id,
          source: "CLOSING",
          memo: `Year-end close FY${year}`,
          status: "POSTED",
          derived_status: "POSTED",
          posted_at: new Date(Date.UTC(year, 11, 31, 12)).toISOString(),
          created_by: caller.id,
          created_at: new Date(NOW).toISOString(),
          lines: lines.map((l, i) => ({ line_no: i + 1, account_name: CHART[l.account_code][0], ...l })),
        };
        world.journals.push(closing);
        result.closing_journal_id = closing.id;
      }
      for (const p of world.periods) {
        if (p.year === year && p.status === "OPEN") {
          p.status = "CLOSED";
          p.closed_at = new Date(NOW).toISOString();
        }
      }
      return { status: 200, body: result };
    }

    // policy
    if (m === "GET" && path === "/api/v1/gl/policy") {
      const refused = gate(caller, "read");
      if (refused !== null) return refused;
      const bad = badBranch(url);
      if (bad !== null) return bad;
      const branch = url.searchParams.get("branch_id") ?? "";
      return {
        status: 200,
        body: {
          ...(branch === "" ? {} : { branch_id: branch }),
          je_approval_threshold_cents: THRESHOLD_CENTS,
          fiscal_year_start_month: 1,
          cost_basis_strategy: "WEIGHTED_AVG",
          exclude_special_buys: false,
          notes: "Org default policy row.",
        },
      };
    }

    // the receivables tie-out: the subledger's figure is the receivables desk's (the stub holds it fixed at what
    // the opening journals carry), the ledger's is account 1100 net debit as of now.
    if (m === "GET" && path === "/api/v1/gl/tie-out") {
      const refused = gate(caller, "read");
      if (refused !== null) return refused;
      const walled = reach(caller, url);
      if (walled !== null) return walled;
      const bad = badBranch(url);
      if (bad !== null) return bad;
      const branch = url.searchParams.get("branch_id") ?? "";
      let gl = 0;
      for (const j of world.journals) {
        if (j.status !== "POSTED") continue;
        if (branch !== "" && j.branch_id !== branch) continue;
        for (const l of j.lines) if (l.account_code === "1100") gl += l.debit_cents - l.credit_cents;
      }
      const subledger = branch === "" || branch === IDS.pointLoma ? 700000 : 0;
      const delta = subledger - gl;
      const key = branch === "" ? "all" : branch;
      if (delta !== 0 && world.tieSince[key] === undefined) world.tieSince[key] = new Date(NOW).toISOString();
      if (delta === 0) delete world.tieSince[key];
      return {
        status: 200,
        body: {
          items: [
            {
              subledger: "AR",
              account_code: "1100",
              ...(branch === "" ? {} : { branch_id: branch }),
              subledger_cents: subledger,
              gl_cents: gl,
              delta_cents: delta,
              in_tie: delta === 0,
              out_of_tie_since: world.tieSince[key] ?? null,
              age_days: 0,
              last_checked_at: new Date(NOW).toISOString(),
            },
          ],
        },
      };
    }

    // the static BisTrack reconciliation, admin only: one synthetic closing trial balance for one period end.
    if (m === "GET" && path === "/api/v1/gl/tieout/bistrack") {
      const refused = gate(caller, "admin");
      if (refused !== null) return refused;
      const bad = badBranch(url);
      if (bad !== null) return bad;
      // The core's order: the guard, the branch, then the dealer's legacy import source.
      if (world.legacyImport === "") return say(404, "the legacy import check is not enabled for this dealer");
      const year = Number(url.searchParams.get("year") ?? FY) || FY;
      const month = Number(url.searchParams.get("month") ?? 0) || 0;
      const tolerance = Number(url.searchParams.get("tolerance_cents") ?? 0) || 0;
      if (year <= 0) return say(400, "gl: bistrack tie-out requires a fiscal_year");
      if (year !== FY) return say(400, `gl: bistrack snapshot is FY${FY} but tie-out requested FY${year}`);
      if (month < 1 || month > 12) return say(400, "gl: bistrack tie-out requires a fiscal month 1-12 (a closing balance is reconciled at ONE period end, not whole-year)");
      if (month !== 6) return say(400, `gl: bistrack snapshot is month 6 but tie-out requested month ${month}`);
      const erp = trialBalance(url.searchParams.get("branch_id"), FY, 0);
      const lines = [
        { account_code: "1000", account_name: "Cash", normal_balance: "DEBIT", erp_cents: 2002500, bistrack_cents: 2002500, delta_cents: 0, source: "BOTH", bistrack_ref: "BT-1000" },
        { account_code: "1100", account_name: "Accounts Receivable", normal_balance: "DEBIT", erp_cents: 700000, bistrack_cents: 704500, delta_cents: -4500, source: "BOTH", bistrack_ref: "BT-1100" },
        { account_code: "5200", account_name: "Inventory Adjustments", normal_balance: "DEBIT", erp_cents: 0, bistrack_cents: 12000, delta_cents: -12000, source: "BISTRACK_ONLY", bistrack_ref: "BT-5200" },
      ];
      const discrepancies = lines.filter((l) => Math.abs(l.delta_cents) > tolerance);
      return {
        status: 200,
        body: {
          fiscal_year: FY,
          month,
          ...(url.searchParams.get("branch_id") ? { branch_id: url.searchParams.get("branch_id") } : {}),
          source: "static snapshot",
          is_synthetic: true,
          tolerance_cents: tolerance,
          lines,
          discrepancies,
          total_erp_cents: erp.balance_cents,
          total_bistrack_cents: 0,
          grand_delta_cents: erp.balance_cents,
          ties_within_tolerance: discrepancies.length === 0,
        },
      };
    }

    // the GP parity export, admin only: a banner, a header and one CSV row per posted line.
    if (m === "POST" && path === "/api/v1/gl/export") {
      const refused = gate(caller, "admin");
      if (refused !== null) return refused;
      const year = Number(body.year ?? 0);
      const month = Number(body.month ?? 0);
      const branch = String(body.branch_id ?? "");
      if (branch !== "" && !UUID.test(branch)) return say(400, "invalid branch_id");
      if (!(year > 0)) return say(400, "gl: export requires a fiscal_year");
      const rows = [];
      for (const j of [...world.journals].sort((a, b) => a.entry_no - b.entry_no)) {
        if (j.status !== "POSTED" || j.fiscal_year !== year) continue;
        if (month > 0 && monthOf(j) !== month) continue;
        if (branch !== "" && j.branch_id !== branch) continue;
        for (const l of j.lines) rows.push([j.entry_no, j.posted_at ?? "", l.account_code, l.account_name, l.debit_cents, l.credit_cents, j.memo]);
      }
      const dollars = (cents) => (cents === 0 ? "" : `${Math.trunc(cents / 100)}.${String(cents % 100).padStart(2, "0")}`);
      const csv = (v) => (/[,"\n]/.test(String(v)) ? `"${String(v).replace(/"/g, '""')}"` : String(v));
      const content =
        `# PARITY DRAFT, GP-format parity export; extract artifacts absent, pending cutover confirmation (Q-006). Not a filing.\n# fiscal_year=${year} month=${month} rows=${rows.length}\n` +
        "entry_no,posted_at,account_code,account_name,debit,credit,memo\n" +
        rows.map((r) => `${r[0]},${r[1]},${r[2]},${csv(r[3])},${dollars(r[4])},${dollars(r[5])},${csv(r[6])}`).join("\n") +
        (rows.length > 0 ? "\n" : "");
      return { status: 200, body: { batch_id: "e0000000-0000-4000-9000-000000000001", format: "GP_PARITY", row_count: rows.length, is_draft: true, content } };
    }
    return null;
  }

  /**
   * Answer one request or return null when it is not this world's. The shape
   * is { status, body }, the body already a JSON value.
   */
  function handle(req, url, bodyText) {
    const path = url.pathname;
    if (req.method === "POST" && path === "/_stub/reset") {
      reset();
      return { status: 200, body: { ok: true } };
    }
    if (req.method === "POST" && path === "/_stub/legacy-import") {
      world.legacyImport = String(JSON.parse(bodyText || "{}").source ?? "");
      return { status: 200, body: { ok: true, source: world.legacyImport } };
    }
    if (req.method === "GET" && path === "/api/v1/branches") return { status: 200, body: branches() };
    const caller = callerOf(req);
    // The surface config read the desk makes for the dealer's legacy import source. The source field
    // is absent, not empty, for a dealer with none, as the core's omitempty field is.
    if (req.method === "GET" && path === "/api/v1/surfaces/money-desk/config") {
      return {
        status: 200,
        body: {
          surface_id: "money-desk",
          profile: caller.level === "admin" ? "admin" : "manager",
          role_items: [],
          user_items: [],
          workspace: { split_pct: 50, arrangement: "PRIMARY_LEFT", primary_collapsed: false, secondary_collapsed: false, cost_hidden: false, source: "dealer" },
          ...(world.legacyImport === "" ? {} : { legacy_import_source: world.legacyImport }),
        },
      };
    }
    if (req.method === "GET" && path === "/api/v1/staff/approvals") {
      const wanted = url.searchParams.get("status");
      const rows = world.approvals.filter((a) => (wanted === null || wanted === "" || a.status === wanted) && (caller.global || a.branch_id === IDS.pointLoma));
      // The contract's page: items, next_cursor (the id of the last row of the page) and the limit.
      const limit = Number(url.searchParams.get("limit") ?? 20) || 20;
      const cursor = url.searchParams.get("cursor");
      const from = cursor === null ? 0 : rows.findIndex((r) => r.id === cursor) + 1;
      const items = rows.slice(from, from + limit);
      const more = from + limit < rows.length;
      return { status: 200, body: { items, next_cursor: more ? items[items.length - 1].id : null, limit } };
    }
    const decide = path.match(/^\/api\/v1\/staff\/approvals\/([^/]+)\/decide$/);
    if (req.method === "POST" && decide) {
      if (!UUID.test(decide[1])) return { status: 400, body: { error: "invalid approval id" } };
      const row = world.approvals.find((a) => a.id === decide[1]);
      if (row === undefined) return { status: 404, body: { error: "not found" } };
      let input;
      try {
        input = JSON.parse(bodyText);
      } catch {
        return { status: 400, body: { error: "invalid body" } };
      }
      if (row.status !== "PENDING") return { status: 409, body: { error: `request already ${row.status}` } };
      if (row.requested_by === caller.id) return { status: 403, body: { error: "separation of duties: you cannot approve your own request" } };
      if (!caller.global || caller.tier !== 1) {
        return { status: 403, body: { error: `approver ${caller.role} (tier ${caller.tier}) not authorized for ${row.policy_type} in branch ${row.branch_id}` } };
      }
      row.status = input.approve === true ? "APPROVED" : "REJECTED";
      row.decided_by = caller.id;
      if (row.policy_type === "JE_OVER_THRESHOLD" && input.approve === true) {
        const j = world.journals.find((x) => x.id === row.subject_ref);
        if (j !== undefined) {
          j.status = "POSTED";
          j.derived_status = "POSTED";
          j.approved_by = caller.id;
          j.posted_at = new Date().toISOString();
        }
      }
      return { status: 200, body: { status: "ok" } };
    }
    if (req.method === "GET" && path === "/api/v1/gl/journals") {
      const walled = reach(caller, url);
      if (walled !== null) return walled;
      const wanted = url.searchParams.get("status");
      const at = url.searchParams.get("branch_id") ?? "";
      const rows = [...world.journals]
        .filter((j) => wanted === null || wanted === "" || j.derived_status === wanted)
        .filter((j) => at === "" || j.branch_id === at)
        .sort((a, b) => b.entry_no - a.entry_no);
      return { status: 200, body: { items: rows, next_cursor: null, limit: 100 } };
    }
    const one = path.match(/^\/api\/v1\/gl\/journals\/([^/]+)$/);
    if (req.method === "GET" && one) {
      const j = world.journals.find((x) => x.id === one[1]);
      // Out of reach answers as missing (FW1-10): an id is an existence oracle, and the list already
      // hides these rows.
      if (j !== undefined && !caller.global && j.branch_id !== IDS.pointLoma) {
        return { status: 404, body: { error: "not found" } };
      }
      return j === undefined ? { status: 404, body: { error: "not found" } } : { status: 200, body: j };
    }
    if (req.method === "GET" && path === "/api/v1/gl/trial-balance") {
      const walled = reach(caller, url);
      if (walled !== null) return walled;
      const bad = badBranch(url);
      if (bad !== null) return bad;
      const year = Number(url.searchParams.get("year") ?? FY) || FY;
      const month = Number(url.searchParams.get("month") ?? 0) || 0;
      return { status: 200, body: trialBalance(url.searchParams.get("branch_id"), year, month) };
    }
    return books(req, url, path, bodyText, caller);
  }

  return { handle, reset };
}
