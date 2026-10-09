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
  controller: "de110000-0000-4000-9000-0000000000c1",
  sales: "de110000-0000-4000-9000-000000000001",
  manager: "de110000-0000-4000-9000-000000000002",
  trenton: "5d1b3f6e-0000-4000-9000-000000000001",
  kingston: "5d1b3f6e-0000-4000-9000-000000000002",
  fr1: "2f6a1c00-0000-4000-9000-0000000000f1",
  j1Request: "2f6a1c00-0000-4000-9000-0000000000a1",
  j2Request: "2f6a1c00-0000-4000-9000-0000000000a2",
  freight1: "2f6a1c00-0000-4000-9000-0000000000b1",
  freight2: "2f6a1c00-0000-4000-9000-0000000000b2",
  j1: "7a1e0000-0000-4000-9000-000000000001",
  j2: "7a1e0000-0000-4000-9000-000000000002",
};

const CHART = {
  "1000": ["Cash", "ASSET"],
  "1100": ["Accounts Receivable", "ASSET"],
  "1200": ["Inventory", "ASSET"],
  "1300": ["Prepaid Expenses", "ASSET"],
  "2100": ["Accounts Payable", "LIABILITY"],
  "3000": ["Owner's Equity", "EQUITY"],
  "4000": ["Product Revenue", "REVENUE"],
  "5000": ["Cost of Goods Sold", "EXPENSE"],
  "5200": ["Inventory Adjustments", "EXPENSE"],
};

const FY = 2026;
const stamp = (hoursAgo) => new Date(Date.UTC(2026, 8, 1, 9, 0, 0) - hoursAgo * 3_600_000).toISOString();

function journal(id, entryNo, source, memo, status, branch, createdBy, hoursAgo, lines, extra = {}) {
  const j = {
    id,
    fiscal_year: FY,
    entry_no: entryNo,
    period_id: "9e000000-0000-4000-9000-000000000009",
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

function fresh() {
  return {
    approvals: [
      {
        id: IDS.fr1,
        requested_by: IDS.sales,
        branch_id: IDS.trenton,
        policy_type: "MARGIN_BELOW_MINIMUM",
        detail: "quote DEMO-TRT-Q1 line 1 (62e4ad55-5a14-5945-80ee-866864a6c774): 12 percent discount, resulting margin 1630 bps against the 2000 bps floor",
        subject_ref: "5c7d0e0a-0000-4000-9000-000000000001",
        status: "PENDING",
      },
      {
        id: IDS.j1Request,
        requested_by: IDS.manager,
        branch_id: IDS.trenton,
        policy_type: "JE_OVER_THRESHOLD",
        detail: "Journal entry at/over the maker-checker threshold (750000 cents) awaiting approval",
        subject_ref: IDS.j1,
        amount_cents: 750000,
        status: "PENDING",
      },
      {
        id: IDS.j2Request,
        requested_by: IDS.controller,
        branch_id: IDS.trenton,
        policy_type: "JE_OVER_THRESHOLD",
        detail: "Journal entry at/over the maker-checker threshold (620000 cents) awaiting approval",
        subject_ref: IDS.j2,
        amount_cents: 620000,
        status: "PENDING",
      },
      {
        id: IDS.freight1,
        requested_by: "de110000-0000-4000-9000-0000000000d1",
        branch_id: IDS.kingston,
        policy_type: "FREIGHT_ABOVE_QUOTE",
        detail: "Freight on DEMO-KGN-O01 for Contractor account (demo) is being raised above the delivery the customer was quoted.",
        subject_ref: "6b2c0000-0000-4000-9000-0000000000b1",
        status: "PENDING",
      },
      {
        id: IDS.freight2,
        requested_by: "de110000-0000-4000-9000-0000000000d1",
        branch_id: IDS.trenton,
        policy_type: "FREIGHT_ABOVE_QUOTE",
        detail: "Freight on DEMO-TRT-O03 for Builder account (demo) is being raised above the delivery the customer was quoted.",
        subject_ref: "6b2c0000-0000-4000-9000-0000000000b2",
        status: "PENDING",
      },
    ],
    journals: [
      journal(IDS.j1, 5, "MANUAL", "Insurance premium paid ahead for the year (demo)", "DRAFT", IDS.trenton, IDS.manager, 30, [
        ["1300", 750000, 0],
        ["1000", 0, 750000],
      ]),
      journal(IDS.j2, 6, "MANUAL", "Inventory write-down after the cycle count (demo)", "DRAFT", IDS.trenton, IDS.controller, 12, [
        ["5200", 620000, 0],
        ["1200", 0, 620000],
      ]),
      journal("7a1e0000-0000-4000-9000-000000000004", 4, "ADJUSTMENT", "Till over/short", "POSTED", IDS.trenton, IDS.manager, 200, [
        ["1000", 2500, 0],
        ["5200", 0, 2500],
      ]),
      journal("7a1e0000-0000-4000-9000-000000000003", 3, "PAYMENT", "Payment received", "POSTED", IDS.trenton, null, 300, [
        ["1000", 500000, 0],
        ["1100", 0, 500000],
      ]),
      journal("7a1e0000-0000-4000-9000-000000000002", 2, "INVOICE", "Invoice posted", "POSTED", IDS.trenton, null, 400, [
        ["1100", 1200000, 0],
        ["4000", 0, 1200000],
      ]),
      journal("7a1e0000-0000-4000-9000-000000000001", 1, "MANUAL", "Opening balances", "POSTED", IDS.trenton, IDS.manager, 500, [
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

  const trialBalance = (branch) => {
    const byCode = new Map();
    for (const j of world.journals) {
      if (j.status !== "POSTED") continue;
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
      fiscal_year: FY,
      ...(branch ? { branch_id: branch } : {}),
      rows,
      total_debit_cents,
      total_credit_cents,
      balance_cents: total_debit_cents - total_credit_cents,
    };
  };

  const branches = () => [
    { id: IDS.kingston, code: "KGN", name: "Kingston", address: { line1: "1 Demo Road", city: "Kingston", province: "ON", postal: "K7K 0A1", country: "CA" }, is_default: false, is_system_default: false, is_active: true },
    { id: IDS.trenton, code: "TRT", name: "Trenton", address: { line1: "2 Demo Road", city: "Trenton", province: "ON", postal: "K8V 0A1", country: "CA" }, is_default: true, is_system_default: true, is_active: true },
  ];

  /** The caller, read from the stub token's subject. */
  const callerOf = (req) => {
    const token = String(req.headers.authorization ?? "").replace(/^Bearer /, "");
    try {
      const sub = JSON.parse(Buffer.from(token.split(".")[1], "base64url").toString("utf8")).sub;
      if (sub === IDS.controller) return { id: sub, role: "FINANCIAL_CONTROLLER", tier: 1, global: true };
      if (sub === IDS.manager) return { id: sub, role: "SALES_MANAGER", tier: 2, global: false };
      return { id: sub, role: "INSIDE_SALES", tier: 3, global: false };
    } catch {
      return { id: "", role: "", tier: 0, global: false };
    }
  };

  const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

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
    if (req.method === "GET" && path === "/api/v1/branches") return { status: 200, body: branches() };
    const caller = callerOf(req);
    if (req.method === "GET" && path === "/api/v1/staff/approvals") {
      const wanted = url.searchParams.get("status");
      const rows = world.approvals.filter((a) => (wanted === null || wanted === "" || a.status === wanted) && (caller.global || a.branch_id === IDS.trenton));
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
      const wanted = url.searchParams.get("status");
      const rows = [...world.journals]
        .filter((j) => wanted === null || wanted === "" || j.derived_status === wanted)
        .sort((a, b) => b.entry_no - a.entry_no);
      return { status: 200, body: { items: rows, next_cursor: null, limit: 100 } };
    }
    const one = path.match(/^\/api\/v1\/gl\/journals\/([^/]+)$/);
    if (req.method === "GET" && one) {
      const j = world.journals.find((x) => x.id === one[1]);
      return j === undefined ? { status: 404, body: { error: "not found" } } : { status: 200, body: j };
    }
    if (req.method === "GET" && path === "/api/v1/gl/trial-balance") return { status: 200, body: trialBalance(url.searchParams.get("branch_id")) };
    return null;
  }

  return { handle, reset };
}
