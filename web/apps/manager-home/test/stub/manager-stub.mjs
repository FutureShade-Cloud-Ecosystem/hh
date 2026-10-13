// Manager Home's local stub: the demo way in, and the manager's world
// answered in the core's shapes (the dashboard profile rows 0381 seeds, the
// needs queue, the yard board with its shortages, the receiving worklist,
// the approvals list by kind). It is a development and test double only; the
// real routes are the core's (core/internal/dashboard, tasks, purchasing,
// identity/staffcontext). The stub contract test holds every payload below
// to the core's field list, field by field.
//
// TWO BEARERS, TWO PROFILES. The stub answers the dashboard routes per
// bearer, the way the core does: the yard manager's mint reads the
// manager.yard profile (the seven widgets its board draws), the sales
// manager's reads manager.sales. That is the one-app-per-role decision (5)
// made testable at the stub: the same code renders both landings from rows.
import { createServer } from "node:http";

const port = Number(process.env.MH_STUB_PORT ?? 4959);

// The real flows route lists the closed table's eight logins; this app's door
// draws a card per flow it answers here, and its test world serves the three
// the door walks (sales, the sales manager, the yard manager).
const FLOWS = [
  { flow: "sales", role_code: "inside_sales", label: "Sales" },
  { flow: "sales-manager", role_code: "sales_manager", label: "Sales manager" },
  { flow: "yard-manager", role_code: "yard_manager", label: "Yard manager" },
];

const USERS = {
  // The front-line mint: the tier the reporting gate keeps out of the runs
  // and the register. The flows table names the account; this app's door
  // draws its card, and the landing answers with the sales profile.
  sales: {
    user_id: "de110000-0000-4000-9000-000000000001",
    email: "sales@hh-demo.invalid",
    display_name: "Sales (demo)",
    role_code: "inside_sales",
    role_name: "Sales",
    tier: 3,
    scope: "BRANCH",
  },
  "sales-manager": {
    user_id: "de110000-0000-4000-9000-000000000002",
    email: "sales.manager@hh-demo.invalid",
    display_name: "Sales manager (demo)",
    role_code: "sales_manager",
    role_name: "Sales manager",
    tier: 2,
    scope: "BRANCH",
  },
  "yard-manager": {
    user_id: "de110000-0000-4000-9000-000000000003",
    email: "yard.manager@hh-demo.invalid",
    display_name: "Yard manager (demo)",
    role_code: "yard_manager",
    role_name: "Yard manager",
    tier: 2,
    scope: "BRANCH",
  },
  // The financial controller mints by API only: the closed flows table names
  // the account, this app's test door draws no card for it. The tier one
  // global mint is what exercises the commission rules' admin window.
  "financial-controller": {
    user_id: "de110000-0000-4000-9000-0000000000c1",
    email: "financial.controller@hh-demo.invalid",
    display_name: "Financial controller (demo)",
    role_code: "financial_controller",
    role_name: "Financial Controller",
    tier: 1,
    scope: "GLOBAL",
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

// The core's error envelope: {"error": sentence}. The role gate's shared
// Forbidden body is exactly {"error":"Forbidden"}; the list wall words the
// app maps it to are tested against that shape.
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

// ---- the world -----------------------------------------------------------------------
//
// Trenton, the one branch the demo grants reach. Two open shortages on two
// picks, one resolved shortage beside them (the counter-case), one stale
// pick past its accept deadline, one purchase order PARTIAL with a short
// line (the receiving difference), and one margin approval the sales
// manager decides.

const BRANCH_ID = "5d1b3f6e-0000-4000-9000-000000000001";
const TASK_IN_PROGRESS = "07000000-0000-4000-9000-000000000007";
const TASK_STALE = "07000000-0000-4000-9000-000000000003";
const SHORTAGE_OPEN_1 = "0a000000-0000-4000-9000-0000000000e1";
const SHORTAGE_OPEN_2 = "0a000000-0000-4000-9000-0000000000e2";
const SHORTAGE_DONE = "0a000000-0000-4000-9000-0000000000e3";
const PO_PARTIAL = "0b000000-0000-4000-9000-0000000000p1";

const ago = (hours) => new Date(Date.now() - hours * 3600 * 1000).toISOString();

function buildWorld() {
  const shortages = [
    {
      id: SHORTAGE_OPEN_1,
      task_id: TASK_IN_PROGRESS,
      order_line_no: 1,
      product_id: "00000000-0000-4000-9000-0000000000p9",
      sku: "GRAN-A-CYD",
      description: "Granular A gravel",
      qty_short: "4.0000",
      uom: "CYD",
      note: "Only 8 yards left in the bin",
      reported_by: "",
      reported_at: ago(1),
    },
    {
      id: SHORTAGE_OPEN_2,
      task_id: TASK_IN_PROGRESS,
      order_line_no: 1,
      product_id: "00000000-0000-4000-9000-0000000000p7",
      sku: "HPB-DUST-T",
      description: "HPB dust, bulk",
      qty_short: "12.0000",
      uom: "T",
      note: "Pile is down to fines, nothing on order to cover it",
      reported_by: "",
      reported_at: ago(2),
    },
    {
      id: SHORTAGE_DONE,
      task_id: TASK_IN_PROGRESS,
      order_line_no: 1,
      product_id: "00000000-0000-4000-9000-0000000000p1",
      sku: "PAVE-60MM",
      description: "Standard paver, 60 mm",
      qty_short: "40.0000",
      uom: "PC",
      note: "Two banded layers were cracked",
      reported_by: "",
      reported_at: ago(48),
      resolved_at: ago(47),
      resolved_by: "de110000-0000-4000-9000-000000000003",
      resolution: "Substituted from the Trenton skid",
    },
  ];
  const tasks = [
    {
      task: {
        id: TASK_IN_PROGRESS,
        order_id: "0d000000-0000-4000-9000-000000000008",
        task_type: "PICK",
        status: "IN_PROGRESS",
        priority: "NORMAL",
        branch_id: BRANCH_ID,
        open_shortages: 2,
        total_shortages: 3,
        stage_location: "",
        stage_ref: "",
        created_at: ago(1),
        updated_at: ago(1),
        source: "SYSTEM",
      },
      ticket: {
        order_id: "0d000000-0000-4000-9000-000000000008",
        order_no: "SO-TRT-1042",
        customer_name: "K. Villeneuve (demo)",
        fulfillment: "WILL_CALL",
        lines: [
          { line_no: 1, product_id: "00000000-0000-4000-9000-0000000000p9", sku: "GRAN-A-CYD", description: "Granular A gravel", qty_ordered: "12.0000", uom: "CYD" },
        ],
        source: "DISPATCH",
      },
      shortages,
    },
    {
      task: {
        id: TASK_STALE,
        order_id: "0d000000-0000-4000-9000-000000000001",
        task_type: "PICK",
        status: "PUSHED",
        priority: "URGENT",
        branch_id: BRANCH_ID,
        open_shortages: 0,
        total_shortages: 0,
        stage_location: "",
        stage_ref: "",
        created_at: ago(9),
        updated_at: ago(4),
        accept_deadline_at: ago(4),
        source: "SYSTEM",
      },
      ticket: {
        order_id: "0d000000-0000-4000-9000-000000000001",
        order_no: "SO-TRT-1038",
        customer_name: "Northline Hardscapes Ltd (demo)",
        fulfillment: "DELIVERY",
        lines: [],
        source: "DISPATCH",
      },
      shortages: [],
    },
  ];
  return {
    shortages,
    tasks,
    needsByProfile: {
      "manager.yard": {
        profile: "manager.yard",
        rows: [
          {
            source_key: "tasks.open_shortages",
            what: "Open shortages",
            count: 2,
            age: "2h",
            age_minutes: 120,
            severity: "watch",
            module: "tasks",
            unit: "yard",
            screen: "yard",
            view: "Shortages · open",
            action: "Resolve",
          },
          {
            source_key: "tasks.stale_picks",
            what: "Picks past their deadline",
            count: 1,
            age: "26h",
            age_minutes: 1560,
            severity: "stop",
            module: "tasks",
            unit: "yard",
            screen: "yard",
            view: "Picks · past deadline",
            action: "Chase",
          },
          {
            source_key: "purchasing.receiving_differences",
            what: "Receipts that landed short",
            count: 1,
            age: "3h",
            age_minutes: 180,
            severity: "watch",
            module: "purchasing",
            unit: "yard",
            screen: "yard",
            view: "Receiving · short",
            action: "Settle",
          },
        ],
        total: 4,
        worst: "stop",
      },
      "manager.sales": {
        profile: "manager.sales",
        rows: [
          {
            source_key: "approvals.waiting",
            what: "Approvals waiting",
            count: 1,
            age: "45m",
            age_minutes: 45,
            severity: "watch",
            module: "identity",
            unit: "admin",
            screen: "approvals",
            view: "Waiting on you",
            action: "Review",
          },
        ],
        total: 1,
        worst: "watch",
      },
    },
    widgetsByProfile: {
      "manager.yard": {
        profile: "manager.yard",
        source: "role",
        widgets: [
          { widget_key: "hero.deliveries", shape: "stat", measure: "deliveries.today", split: "none", range: "today", size: "L", title: "Deliveries today", position: 0, visible: true, label: "Deliveries today", kind: "count", module: "dispatch", unit: "dispatch", screen: "dispatch", span_cols: 6, span_rows: 2 },
          { widget_key: "needs.you", shape: "list", measure: "needs.you", split: "none", range: "open", size: "L", title: "Needs you", position: 1, visible: true, label: "Needs you", kind: "count", module: "dashboard", span_cols: 6, span_rows: 2 },
          { widget_key: "picks.board", shape: "board", measure: "picks.queue", split: "status", range: "open", size: "L", title: "Pick queue", position: 2, visible: true, label: "Pick queue", kind: "count", module: "tasks", unit: "yard", screen: "yard", span_cols: 6, span_rows: 2 },
          { widget_key: "deliveries.branch", shape: "bars", measure: "deliveries.today", split: "branch", range: "today", size: "M", title: "By branch", position: 3, visible: true, label: "Deliveries today", kind: "count", module: "dispatch", span_cols: 6, span_rows: 1 },
          { widget_key: "picks.gauge", shape: "gauge", measure: "picks.queue", split: "none", range: "open", size: "S", title: "Queue depth", position: 4, visible: true, label: "Pick queue", kind: "count", module: "tasks", span_cols: 3, span_rows: 1 },
          { widget_key: "orders.open", shape: "stat", measure: "orders.open", split: "none", range: "open", size: "S", title: "Open orders", position: 5, visible: true, label: "Open orders", kind: "count", module: "order", span_cols: 3, span_rows: 1 },
          { widget_key: "warranty.open", shape: "stat", measure: "warranty.open_claims", split: "none", range: "open", size: "S", title: "Open claims", position: 6, visible: true, label: "Open claims", kind: "count", module: "warranty", span_cols: 3, span_rows: 1 },
        ],
      },
      "manager.sales": {
        profile: "manager.sales",
        source: "role",
        widgets: [
          { widget_key: "needs.you", shape: "list", measure: "needs.you", split: "none", range: "open", size: "L", title: "Needs you", position: 0, visible: true, label: "Needs you", kind: "count", module: "dashboard", span_cols: 6, span_rows: 2 },
          { widget_key: "sales.today", shape: "stat", measure: "sales.today", split: "none", range: "today", size: "M", title: "Today's sales", position: 1, visible: true, label: "Today's sales", kind: "money_cents", module: "pos", span_cols: 6, span_rows: 1 },
        ],
      },
    },
    dataByProfile: {
      "manager.yard": {
        "hero.deliveries": { widget_key: "hero.deliveries", state: "ok", total: 14, range: "today" },
        "needs.you": { widget_key: "needs.you", state: "ok", total: 4, range: "open" },
        "picks.board": {
          widget_key: "picks.board",
          state: "ok",
          total: 20,
          range: "open",
          points: [
            { label: "Waiting", number: 8 },
            { label: "Claimed", number: 5 },
            { label: "Picked", number: 3, tone: "good" },
            { label: "Staged", number: 4 },
          ],
        },
        "deliveries.branch": {
          widget_key: "deliveries.branch",
          state: "ok",
          total: 14,
          range: "today",
          points: [
            { label: "Trenton", number: 10 },
            { label: "Kingston", number: 4 },
          ],
        },
        "picks.gauge": { widget_key: "picks.gauge", state: "ok", total: 20, range: "open" },
        "orders.open": { widget_key: "orders.open", state: "ok", total: 12, range: "open" },
        "warranty.open": { widget_key: "warranty.open", state: "ok", total: 3, range: "open" },
      },
      "manager.sales": {
        "needs.you": { widget_key: "needs.you", state: "ok", total: 1, range: "open" },
        "sales.today": { widget_key: "sales.today", state: "ok", total: 123456, range: "today" },
      },
    },
    receiving: [
      {
        po_id: PO_PARTIAL,
        po_no: "PO-TRT-77",
        vendor_id: "0c000000-0000-4000-9000-0000000000v1",
        vendor_name: "Northland Stone Supply (demo)",
        branch_id: BRANCH_ID,
        status: "PARTIAL",
        expected_at: ago(-24).slice(0, 10),
        open_lines: 1,
        qty_outstanding_base: 2,
        value_open_cents: 5400,
      },
      {
        po_id: "0b000000-0000-4000-9000-0000000000p2",
        po_no: "PO-TRT-78",
        vendor_id: "0c000000-0000-4000-9000-0000000000v2",
        vendor_name: "Bay Aggregate (demo)",
        branch_id: BRANCH_ID,
        status: "ORDERED",
        expected_at: ago(-72).slice(0, 10),
        open_lines: 3,
        qty_outstanding_base: 30,
      },
    ],
    approvals: [
      {
        id: "0e000000-0000-4000-9000-0000000000a1",
        requested_by: "de110000-0000-4000-9000-000000000001",
        branch_id: BRANCH_ID,
        policy_type: "MARGIN_BELOW_MINIMUM",
        detail: "Quote Q-2001 line 2 at 12% margin, floor is 15%",
        subject_ref: "quote:0f000000-0000-4000-9000-0000000000q1",
        amount_cents: 0,
        status: "PENDING",
      },
    ],
  };
}

// ---- the reporting world --------------------------------------------------------------
//
// The reporting surface's test world: two published templates, one frozen
// run with its document, one saved view, one standing commission rule, one
// stored commission period with its drill-through lines, the accrual
// register and the attribution check over the same window. Everything is
// invented data in the demo tenant's own words; the reps are role labels.

const REPORT_REP_ID = "a1000000-0000-4000-9000-00000000c001";
const REPORT_REP_NAME = "Outside sales (demo)";
const ACCRUAL_NOTE =
  "Accrued, not posted. These figures are computed from invoiced sales and have no " +
  "general-ledger entry: nothing here appears in the trial balance and no liability has been raised.";
const ATTRIBUTION_MISMATCH_EXPLANATION =
  "These sales are attributed differently by the commission report and by the " +
  "rep-assignment resolver. Commission computed from this window may be paid to the wrong rep.";

const REPORT_TEMPLATES = [
  {
    code: "sales_by_day",
    name: "Sales by day",
    category: "SALES",
    description: "Invoices and their value, by the day they were issued.",
    legacy_name: "Sales by Day",
    sort_order: 1,
    is_active: true,
    params: [
      { name: "from_date", kind: "date", required: true, label: "From date" },
      { name: "to_date", kind: "date", required: true, label: "To date" },
      { name: "branch_id", kind: "uuid", required: false, label: "Branch" },
    ],
    columns: [
      { name: "day", label: "Day", kind: "date", total: false },
      { name: "invoice_count", label: "Invoices", kind: "int", total: true },
      { name: "subtotal_cents", label: "Subtotal", kind: "cents", total: true },
      { name: "tax_cents", label: "Tax", kind: "cents", total: true },
      { name: "total_cents", label: "Total", kind: "cents", total: true },
    ],
  },
  {
    code: "margin_by_product",
    name: "Margin by product",
    category: "MARGIN",
    description: "What each product sold for, what it cost, and what was left.",
    legacy_name: "",
    sort_order: 2,
    is_active: true,
    params: [
      { name: "from_date", kind: "date", required: true, label: "From date" },
      { name: "to_date", kind: "date", required: true, label: "To date" },
      { name: "branch_id", kind: "uuid", required: false, label: "Branch" },
    ],
    columns: [
      { name: "sku", label: "SKU", kind: "text", total: false },
      { name: "product_name", label: "Product", kind: "text", total: false },
      { name: "qty_base", label: "Qty", kind: "qty", total: true },
      { name: "revenue_cents", label: "Revenue", kind: "cents", total: true },
      { name: "cost_cents", label: "Cost", kind: "cents", total: true },
      { name: "margin_cents", label: "Margin", kind: "cents", total: true },
    ],
  },
];

const REPORT_CATEGORY_LABELS = {
  SALES: "Sales",
  MARGIN: "Margin",
};

function reportingRows(code) {
  if (code === "sales_by_day") {
    return [
      ["2026-10-02", 4, 87000, 11310, 98310],
      ["2026-10-09", 6, 132500, 17225, 149725],
    ];
  }
  return [
    ["GRAN-A-CYD", "Granular A gravel", 18, 54000, 32400, 21600],
    ["PAVE-60MM", "Standard paver, 60 mm", 240, 165500, 118000, 47500],
  ];
}

function totalsOf(code) {
  const cols = REPORT_TEMPLATES.find((t) => t.code === code).columns;
  const rows = reportingRows(code);
  const out = {};
  cols.forEach((c, i) => {
    if (c.total) out[c.name] = rows.reduce((sum, row) => sum + row[i], 0);
  });
  return out;
}

function buildReportingWorld() {
  return {
    runs: [
      {
        id: "0c900000-0000-4000-9000-00000000r001",
        kind: "TEMPLATE",
        template_code: "sales_by_day",
        name: "Sales by day",
        params: { from_date: "2026-10-01", to_date: "2026-10-14", branch_id: BRANCH_ID },
        branch_id: BRANCH_ID,
        row_count: 2,
        totals: totalsOf("sales_by_day"),
        print_job_id: "pj-seed-1",
        run_by: "de110000-0000-4000-9000-000000000003",
        created_at: ago(5),
      },
    ],
    savedViews: [
      {
        id: "0ca00000-0000-4000-9000-00000000v001",
        template_code: "sales_by_day",
        owner_user_id: "de110000-0000-4000-9000-000000000003",
        name: "This month",
        params: { from_date: "2026-10-01", to_date: "2026-10-14" },
        is_shared: false,
        created_at: ago(4),
      },
    ],
    rules: [
      {
        id: "0cb00000-0000-4000-9000-00000000u001",
        rep_user_id: REPORT_REP_ID,
        rep_name: REPORT_REP_NAME,
        basis: "REVENUE",
        rate_bps: 250,
        branch_id: "",
        effective_from: "2026-01-01",
        effective_to: "",
        is_active: true,
        note: "The standing rate",
        created_at: ago(72),
      },
    ],
    commissionRuns: [
      {
        run_id: "0cc00000-0000-4000-9000-00000000n001",
        rep_user_id: REPORT_REP_ID,
        rep_name: REPORT_REP_NAME,
        period_start: "2026-10-01",
        period_end: "2026-10-01",
        branch_id: BRANCH_ID,
        sale_count: 9,
        revenue_cents: 231000,
        cost_cents: 152000,
        margin_cents: 79000,
        commission_cents: 5775,
        status: "ACCRUED",
        computed_at: ago(3),
        posts_to_gl: false,
        unattributed_cents: 12000,
        unattributed_count: 1,
      },
    ],
  };
}

function commissionLines(periodStart) {
  return [
    {
      invoice_id: "0cd00000-0000-4000-9000-00000000i001",
      invoice_line_id: "0cd00000-0000-4000-9000-00000000i001-1",
      invoice_no: "GT-2107",
      invoice_date: periodStart,
      customer_name: "Gable Landscape Supply (demo)",
      product_sku: "PAVE-60MM",
      category_id: "0ce00000-0000-4000-9000-00000000g001",
      category_code: "PAVER",
      branch_id: BRANCH_ID,
      qty_base: 120,
      revenue_cents: 84000,
      cost_cents: 59400,
      rep_user_id: REPORT_REP_ID,
      margin_cents: 24600,
      basis: "REVENUE",
      basis_cents: 84000,
      rate_bps: 250,
      commission_cents: 2100,
      rule_id: "0cb00000-0000-4000-9000-00000000u001",
    },
    {
      invoice_id: "0cd00000-0000-4000-9000-00000000i002",
      invoice_line_id: "0cd00000-0000-4000-9000-00000000i002-1",
      invoice_no: "GT-2112",
      invoice_date: periodStart,
      customer_name: "Northline Hardscapes Ltd (demo)",
      product_sku: "GRAN-A-CYD",
      category_id: "0ce00000-0000-4000-9000-00000000g002",
      category_code: "AGGREGATE",
      branch_id: BRANCH_ID,
      qty_base: 30,
      revenue_cents: 27000,
      cost_cents: 18900,
      rep_user_id: REPORT_REP_ID,
      margin_cents: 8100,
      basis: "REVENUE",
      basis_cents: 27000,
      rate_bps: 250,
      commission_cents: 675,
      rule_id: "0cb00000-0000-4000-9000-00000000u001",
    },
    {
      invoice_id: "0cd00000-0000-4000-9000-00000000i003",
      invoice_line_id: "0cd00000-0000-4000-9000-00000000i003-1",
      invoice_no: "GT-2118",
      invoice_date: periodStart,
      customer_name: "Fairhaven Builds (demo)",
      product_sku: "EDGER-STEEL",
      category_id: "0ce00000-0000-4000-9000-00000000g003",
      category_code: "EDGING",
      branch_id: BRANCH_ID,
      qty_base: 40,
      revenue_cents: 12000,
      cost_cents: 8400,
      rep_user_id: REPORT_REP_ID,
      margin_cents: 3600,
      basis: "REVENUE",
      basis_cents: 12000,
      rate_bps: 0,
      commission_cents: 0,
      no_rule_reason: "no rule covers the EDGING category",
    },
  ];
}

function previewPeriod(from, to, branchId) {
  const stored = world.reporting.commissionRuns[0];
  // A preview computes on the fly and writes nothing, the way the core's own
  // preview does: it never carries a stored run's id.
  const { run_id: _drop, ...rest } = stored;
  return {
    ...rest,
    period_start: from,
    period_end: to,
    branch_id: branchId !== "" ? branchId : stored.branch_id,
  };
}

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const DATE_RE = /^\d{4}-\d{2}-\d{2}$/;

function csvOf(run) {
  const header = "invoice_no,invoice_date,customer_name,sku,qty,revenue_cents,commission_cents";
  const rows = commissionLines(run.period_start).map(
    (l) => `${l.invoice_no},${l.invoice_date},${l.customer_name},${l.product_sku},${l.qty_base},${l.revenue_cents},${l.commission_cents}`,
  );
  return [header, ...rows].join("\n") + "\n";
}

function documentOf(run) {
  return [
    "<!doctype html>",
    "<html lang=\"en\"><head><meta charset=\"utf-8\"><title>Frozen run",
    run.id,
    "</title></head><body><h1>",
    run.name !== "" ? run.name : run.template_code,
    "</h1><p>Frozen copy for print job ",
    run.print_job_id ?? "",
    ".</p></body></html>",
  ].join("");
}

let world;
let scenario;
let log;

function reset() {
  world = buildWorld();
  world.reporting = buildReportingWorld();
  scenario = "";
  log = [];
}
reset();

// The kinds the stub's yard manager decides: none, the matrix's lists name
// the financial controller and the general manager. The sales manager
// decides margin in branch.
function decidesMargin(flow) {
  return flow === "sales-manager";
}

function flowOf(token) {
  for (const [flow, account] of Object.entries(USERS)) {
    const payload = token.split(".")[1];
    if (payload === undefined) return null;
    try {
      const claims = JSON.parse(Buffer.from(payload, "base64url").toString("utf8"));
      if (claims.sub === account.user_id) return flow;
    } catch {
      return null;
    }
  }
  return null;
}

function profileOf(flow) {
  return flow === "yard-manager" ? "manager.yard" : "manager.sales";
}

function openShortageCount() {
  return world.shortages.filter((s) => s.resolved_at === undefined).length;
}

function yardNeeds() {
  // The queue is recomputed from the world, the way the core recounts per read.
  const rows = world.needsByProfile["manager.yard"].rows.map((row) => {
    if (row.source_key === "tasks.open_shortages") {
      return { ...row, count: openShortageCount() };
    }
    return row;
  }).filter((row) => row.count > 0);
  const total = rows.reduce((sum, row) => sum + row.count, 0);
  const worst = rows.some((r) => r.severity === "stop") ? "stop" : rows.some((r) => r.severity === "watch") ? "watch" : "good";
  return { profile: "manager.yard", rows, total, worst };
}

function needsFor(flow) {
  if (flow !== "yard-manager") {
    // Recomputed per read, the way the core recounts: the approvals row is
    // what is still pending for a caller who decides it.
    const pending = world.approvals.filter((a) => a.status === "PENDING").length;
    const rows = pending > 0 ? world.needsByProfile["manager.sales"].rows.map((row) => ({ ...row, count: pending })) : [];
    const total = pending > 0 ? pending : 0;
    const worst = pending > 0 ? "watch" : "good";
    return { profile: "manager.sales", rows, total, worst };
  }
  return yardNeeds();
}

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
    return json(res, { ok: true, scenario });
  }
  if (req.method === "GET" && path === "/_stub/log") return json(res, log);

  // ---- the demo way in ----
  if (path === "/api/v1/demo/flows") return json(res, { flows: FLOWS });
  if (req.method === "POST" && path === "/api/v1/demo/session") {
    const body = await readBody(req);
    try {
      const { flow } = JSON.parse(body.toString("utf8"));
      const account = USERS[flow];
      if (!account) return notFound(res);
      // A mint the server fails with its one word body, the way a core answers an internal error.
      if (scenario === "mint-refused") return refuse(res, 500, "internal");
      return json(res, { token: mintToken(account.user_id), user: account, flow });
    } catch {
      return refuse(res, 400, "bad request");
    }
  }

  // ---- the staff routes the landing uses ----
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
    body,
  });
  if (!authorization.startsWith("Bearer ")) return refuse(res, 401, "unauthenticated");
  const flow = flowOf(authorization.slice("Bearer ".length));
  if (flow === null) return refuse(res, 401, "unauthenticated");
  const profile = profileOf(flow);

  if (req.method === "GET" && path === "/api/v1/branches") {
    return json(res, [
      {
        id: BRANCH_ID,
        code: "TRT",
        name: "Dibbits Landscape Supply · Trenton",
        address: { line1: "TBD", city: "Trenton", province: "ON", postal: "K8V 0A0", country: "CA" },
        is_default: true,
        is_system_default: false,
        is_active: true,
      },
    ]);
  }

  if (req.method === "GET" && path === "/api/v1/dashboard/needs") {
    return json(res, needsFor(flow));
  }

  if (req.method === "GET" && path === "/api/v1/dashboard/widgets") {
    return json(res, world.widgetsByProfile[profile]);
  }

  if (req.method === "GET" && path === "/api/v1/dashboard/widgets/data") {
    const keys = url.searchParams.getAll("key");
    const data = world.dataByProfile[profile];
    const out = keys.map((key) => {
      if (scenario === "unwired-one" && key === "warranty.open") {
        return { widget_key: key, state: "unwired", range: "open", note: "Not connected (warranty)" };
      }
      return data[key] ?? { widget_key: key, state: "unwired", range: "open", note: "Not connected" };
    });
    return json(res, { data: out });
  }

  if (req.method === "GET" && path === "/api/v1/yard/pick-queue") {
    const items = world.tasks.map((entry) => ({
      task: { ...entry.task, open_shortages: entry.shortages.filter((s) => s.resolved_at === undefined).length },
      ticket: entry.ticket,
      shortages: entry.shortages,
    }));
    return json(res, { items, next_cursor: null, limit: 50 });
  }

  const pickMatch = path.match(/^\/api\/v1\/yard\/picks\/([^/]+)$/);
  if (req.method === "GET" && pickMatch) {
    const entry = world.tasks.find((t) => t.task.id === pickMatch[1]);
    if (!entry) return notFound(res);
    return json(res, { task: entry.task, ticket: entry.ticket, shortages: entry.shortages });
  }

  const resolveMatch = path.match(/^\/api\/v1\/yard\/picks\/([^/]+)\/shortages\/([^/]+)\/resolve$/);
  if (req.method === "POST" && resolveMatch) {
    // The list wall, in the role gate's own shared body: a caller the
    // shortage_resolvers list does not name is refused with Forbidden, the
    // one word, and the app maps it to plain words naming who may act.
    if (scenario === "refused-resolve") return refuse(res, 403, "Forbidden");
    const shortage = world.shortages.find((s) => s.id === resolveMatch[2]);
    if (!shortage) return refuse(res, 404, "shortage not found");
    if (shortage.resolved_at !== undefined) return refuse(res, 409, "shortage already resolved");
    const resolution = String(body?.resolution ?? "").trim();
    if (resolution === "") return refuse(res, 400, "resolution is required");
    shortage.resolved_at = new Date().toISOString();
    shortage.resolved_by = USERS[flow].user_id;
    shortage.resolution = resolution;
    return json(res, shortage);
  }

  if (req.method === "GET" && path === "/api/v1/purchasing/receiving") {
    // The empty dock: a branch where every order landed everything ordered.
    const items = scenario === "no-differences" ? [] : world.receiving;
    return json(res, { items, next_cursor: null, limit: 50 });
  }

  if (req.method === "GET" && path === "/api/v1/staff/approvals") {
    const kind = url.searchParams.get("kind") ?? "";
    if (kind !== "") {
      const known = ["MARGIN_BELOW_MINIMUM", "CREDIT_LIMIT_EXCEEDED", "COD_CONSTRAINT", "JE_OVER_THRESHOLD", "FREIGHT_ABOVE_QUOTE"];
      if (!known.includes(kind)) return refuse(res, 400, "unknown approval kind");
      // The matrix's lists: only margin carries an in-branch arm (a sales
      // manager clears a salesperson's floor in their own branch); every
      // other kind is leadership's alone.
      if (kind !== "MARGIN_BELOW_MINIMUM" || !decidesMargin(flow)) {
        return refuse(res, 403, `you do not decide ${kind} approvals`);
      }
    }
    const status = url.searchParams.get("status") ?? "";
    const items = world.approvals.filter(
      (a) => (kind === "" || a.policy_type === kind) && (status === "" || a.status === status),
    );
    return json(res, { items, next_cursor: null, limit: 50 });
  }

  const decideMatch = path.match(/^\/api\/v1\/staff\/approvals\/([^/]+)\/decide$/);
  if (req.method === "POST" && decideMatch) {
    const approval = world.approvals.find((a) => a.id === decideMatch[1]);
    if (!approval) return notFound(res);
    if (!decidesMargin(flow)) return refuse(res, 403, "you do not decide MARGIN_BELOW_MINIMUM approvals");
    if (approval.status !== "PENDING") return refuse(res, 409, "approval already decided");
    approval.status = body?.approve === true ? "APPROVED" : "REJECTED";
    approval.decided_by = USERS[flow].user_id;
    return json(res, { ok: true });
  }

  // ---- the reporting surface -------------------------------------------------------
  //
  // The core's own gates, played honestly: the template catalogue and the
  // saved views are staff reads; running, printing, the frozen runs and the
  // whole commission register are manager reads; writing a commission rule is
  // the admin window (tier one global), refused with the role gate's shared
  // Forbidden body otherwise. Parameter and window validation carry the
  // core's own refusal sentences.

  if (path.startsWith("/api/v1/reporting/")) {
    const tier = USERS[flow].tier;
    const isManager = tier >= 1 && tier <= 2;
    const isAdmin = tier === 1 && USERS[flow].scope === "GLOBAL";
    const managerWord = () => refuse(res, 403, "Forbidden");

    // Bind the query against a template's specs: unknown parameters and
    // missing required ones are refused in the core's own words, empties are
    // dropped (an absence is no filter).
    const bindParams = (specs, query) => {
      const out = {};
      for (const [name, value] of Object.entries(query)) {
        const spec = specs.find((s) => s.name === name);
        if (spec === undefined) return { error: `unknown report parameter "${name}"` };
        if (value !== "") out[name] = value;
      }
      for (const spec of specs) {
        if (spec.required && (out[spec.name] ?? "") === "") return { error: `report parameter "${spec.name}" is required` };
      }
      return { params: out };
    };

    const freshRunId = () => `0c900000-0000-4000-9000-${String(world.reporting.runs.length + 2).padStart(12, "0")}`;

    if (req.method === "GET" && path === "/api/v1/reporting/templates") {
      const categories = Object.entries(REPORT_CATEGORY_LABELS).map(([code, label]) => ({
        code,
        label,
        templates: REPORT_TEMPLATES.filter((t) => t.category === code),
      }));
      return json(res, { categories });
    }

    const templateMatch = path.match(/^\/api\/v1\/reporting\/templates\/([^/]+)$/);
    if (req.method === "GET" && templateMatch) {
      const tmpl = REPORT_TEMPLATES.find((t) => t.code === templateMatch[1]);
      if (!tmpl) return notFound(res);
      return json(res, tmpl);
    }

    const runMatch = path.match(/^\/api\/v1\/reporting\/templates\/([^/]+)\/run$/);
    if (req.method === "GET" && runMatch) {
      if (!isManager) return managerWord();
      const tmpl = REPORT_TEMPLATES.find((t) => t.code === runMatch[1]);
      if (!tmpl) return notFound(res);
      if (scenario === "reporting-out-of-scope") return refuse(res, 403, "this report is outside your branch scope");
      const bound = bindParams(tmpl.params, Object.fromEntries(url.searchParams));
      if (bound.error !== undefined) return refuse(res, 400, bound.error);
      const rows = reportingRows(tmpl.code);
      return json(res, {
        code: tmpl.code,
        name: tmpl.name,
        category: tmpl.category,
        columns: tmpl.columns,
        rows,
        row_count: rows.length,
        totals: totalsOf(tmpl.code),
        params: bound.params,
        run_at: new Date().toISOString(),
      });
    }

    const exportMatch = path.match(/^\/api\/v1\/reporting\/templates\/([^/]+)\/export\.csv$/);
    if (req.method === "GET" && exportMatch) {
      if (!isManager) return managerWord();
      const tmpl = REPORT_TEMPLATES.find((t) => t.code === exportMatch[1]);
      if (!tmpl) return notFound(res);
      const bound = bindParams(tmpl.params, Object.fromEntries(url.searchParams));
      if (bound.error !== undefined) return refuse(res, 400, bound.error);
      const header = tmpl.columns.map((c) => c.name).join(",");
      const body = reportingRows(tmpl.code).map((row) => row.join(",")).join("\n");
      res.writeHead(200, { "Content-Type": "text/csv; charset=utf-8" });
      res.end(`${header}\n${body}\n`);
      return;
    }

    const printMatch = path.match(/^\/api\/v1\/reporting\/templates\/([^/]+)\/print$/);
    if (req.method === "POST" && printMatch) {
      if (!isManager) return managerWord();
      const tmpl = REPORT_TEMPLATES.find((t) => t.code === printMatch[1]);
      if (!tmpl) return notFound(res);
      const bound = bindParams(tmpl.params, { ...Object.fromEntries(url.searchParams), ...(body?.params ?? {}) });
      if (bound.error !== undefined) return refuse(res, 400, bound.error);
      if ((bound.params.branch_id ?? "") === "" && (body?.print_branch_id ?? "") === "") {
        return refuse(res, 400, "this report covers every branch, name the branch to print it at");
      }
      const rows = reportingRows(tmpl.code);
      const run = {
        id: freshRunId(),
        kind: "TEMPLATE",
        template_code: tmpl.code,
        name: tmpl.name,
        params: bound.params,
        branch_id: bound.params.branch_id ?? body?.print_branch_id ?? "",
        row_count: rows.length,
        totals: totalsOf(tmpl.code),
        print_job_id: `pj-${world.reporting.runs.length + 1}`,
        run_by: USERS[flow].user_id,
        created_at: new Date().toISOString(),
        html: documentOf({ id: "x", name: tmpl.name, template_code: tmpl.code, print_job_id: `pj-${world.reporting.runs.length + 1}` }),
      };
      world.reporting.runs.unshift(run);
      return json(res, run, 201);
    }

    if (req.method === "GET" && path === "/api/v1/reporting/runs") {
      if (!isManager) return managerWord();
      const limit = Number(url.searchParams.get("limit") ?? 25);
      return json(res, { items: world.reporting.runs.slice(0, limit), next_cursor: null, limit });
    }

    const runsMatch = path.match(/^\/api\/v1\/reporting\/runs\/([^/]+)$/);
    if (req.method === "GET" && runsMatch) {
      if (!isManager) return managerWord();
      const run = world.reporting.runs.find((r) => r.id === runsMatch[1]);
      if (!run) return notFound(res);
      const { html: _doc, ...rest } = run;
      return json(res, rest);
    }

    const docMatch = path.match(/^\/api\/v1\/reporting\/runs\/([^/]+)\/document$/);
    if (req.method === "GET" && docMatch) {
      if (!isManager) return managerWord();
      const run = world.reporting.runs.find((r) => r.id === docMatch[1]);
      if (!run) return notFound(res);
      res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" });
      res.end(run.html ?? documentOf(run));
      return;
    }

    if (req.method === "GET" && path === "/api/v1/reporting/saved-views") {
      const code = url.searchParams.get("template_code") ?? "";
      const items = world.reporting.savedViews.filter((v) => code === "" || v.template_code === code);
      return json(res, { items, next_cursor: null, limit: 50 });
    }

    if (req.method === "POST" && path === "/api/v1/reporting/saved-views") {
      const name = String(body?.name ?? "").trim();
      if (name === "") return refuse(res, 400, "a saved view needs a name");
      const tmpl = REPORT_TEMPLATES.find((t) => t.code === body?.template_code);
      if (!tmpl) return notFound(res);
      const bound = bindParams(tmpl.params, body?.params ?? {});
      if (bound.error !== undefined) return refuse(res, 400, bound.error);
      const view = {
        id: `0ca00000-0000-4000-9000-${String(world.reporting.savedViews.length + 2).padStart(12, "0")}`,
        template_code: tmpl.code,
        owner_user_id: USERS[flow].user_id,
        name,
        params: bound.params,
        is_shared: body?.is_shared === true,
        created_at: new Date().toISOString(),
      };
      world.reporting.savedViews.unshift(view);
      return json(res, view, 201);
    }

    const deleteViewMatch = path.match(/^\/api\/v1\/reporting\/saved-views\/([^/]+)$/);
    if (req.method === "DELETE" && deleteViewMatch) {
      const index = world.reporting.savedViews.findIndex((v) => v.id === deleteViewMatch[1]);
      if (index === -1) return notFound(res);
      world.reporting.savedViews.splice(index, 1);
      res.writeHead(204);
      res.end();
      return;
    }

    if (req.method === "GET" && path === "/api/v1/reporting/commission/rules") {
      if (!isManager) return managerWord();
      const rep = url.searchParams.get("rep_user_id") ?? "";
      const items = world.reporting.rules.filter((r) => rep === "" || r.rep_user_id === rep);
      return json(res, { items, next_cursor: null, limit: 50 });
    }

    if (req.method === "POST" && path === "/api/v1/reporting/commission/rules") {
      // The admin window: the shared Forbidden body exactly as the middleware writes it.
      if (!isAdmin) return refuse(res, 403, "Forbidden");
      if (!UUID_RE.test(String(body?.rep_user_id ?? ""))) return refuse(res, 400, "rep_user_id must be a uuid");
      if (body?.basis !== "REVENUE" && body?.basis !== "MARGIN") return refuse(res, 400, "basis must be REVENUE or MARGIN");
      const rate = Number(body?.rate_bps);
      if (!Number.isFinite(rate) || rate < 0 || rate > 10000) return refuse(res, 400, "rate_bps must be between 0 and 10000 (0-100%)");
      if (!DATE_RE.test(String(body?.effective_from ?? ""))) return refuse(res, 400, "effective_from must be a date (YYYY-MM-DD)");
      if (body?.effective_to !== undefined && body.effective_to !== "" && !DATE_RE.test(String(body.effective_to))) {
        return refuse(res, 400, "effective_to must be a date (YYYY-MM-DD)");
      }
      const rule = {
        id: `0cb00000-0000-4000-9000-${String(world.reporting.rules.length + 2).padStart(12, "0")}`,
        rep_user_id: body.rep_user_id,
        rep_name: REPORT_REP_NAME,
        basis: body.basis,
        rate_bps: rate,
        branch_id: typeof body.branch_id === "string" ? body.branch_id : "",
        effective_from: body.effective_from,
        effective_to: typeof body.effective_to === "string" ? body.effective_to : "",
        is_active: true,
        note: typeof body.note === "string" ? body.note : "",
        created_at: new Date().toISOString(),
      };
      world.reporting.rules.unshift(rule);
      return json(res, rule, 201);
    }

    const retireMatch = path.match(/^\/api\/v1\/reporting\/commission\/rules\/([^/]+)$/);
    if (req.method === "DELETE" && retireMatch) {
      if (!isAdmin) return refuse(res, 403, "Forbidden");
      if (!UUID_RE.test(retireMatch[1])) return notFound(res);
      const rule = world.reporting.rules.find((r) => r.id === retireMatch[1]);
      if (!rule) return notFound(res);
      rule.is_active = false;
      res.writeHead(204);
      res.end();
      return;
    }

    const windowError = () => {
      const from = url.searchParams.get("from") ?? body?.from ?? "";
      const to = url.searchParams.get("to") ?? body?.to ?? "";
      if (!DATE_RE.test(from)) return "from must be a date (YYYY-MM-DD)";
      if (!DATE_RE.test(to)) return "to must be a date (YYYY-MM-DD)";
      if (to < from) return "to must not be before from";
      return "";
    };

    if (req.method === "GET" && path === "/api/v1/reporting/commission/preview") {
      if (!isManager) return managerWord();
      const windowRefusal = windowError();
      if (windowRefusal !== "") return refuse(res, 400, windowRefusal);
      const from = url.searchParams.get("from");
      const to = url.searchParams.get("to");
      const branchId = url.searchParams.get("branch_id") ?? "";
      const period = previewPeriod(from, to, branchId);
      return json(res, { items: [{ ...period, lines: commissionLines(from) }], posts_to_gl: false, note: ACCRUAL_NOTE });
    }

    if (req.method === "POST" && path === "/api/v1/reporting/commission/runs") {
      if (!isManager) return managerWord();
      if (!UUID_RE.test(String(body?.rep_user_id ?? ""))) return refuse(res, 400, "rep_user_id is required to compute a commission period");
      if (!DATE_RE.test(String(body?.from ?? ""))) return refuse(res, 400, "from must be a date (YYYY-MM-DD)");
      if (!DATE_RE.test(String(body?.to ?? ""))) return refuse(res, 400, "to must be a date (YYYY-MM-DD)");
      if (body.to < body.from) return refuse(res, 400, "to must not be before from");
      const runId = `0cc00000-0000-4000-9000-${String(world.reporting.commissionRuns.length + 2).padStart(12, "0")}`;
      const period = {
        ...previewPeriod(body.from, body.to, typeof body.branch_id === "string" ? body.branch_id : ""),
        // The window's own branch wins: a compute with no branch is a
        // consolidated period, and printing one later is refused for it.
        branch_id: typeof body.branch_id === "string" ? body.branch_id : "",
        run_id: runId,
        computed_at: new Date().toISOString(),
        lines: commissionLines(body.from),
      };
      world.reporting.commissionRuns.unshift(period);
      return json(res, period, 201);
    }

    if (req.method === "GET" && path === "/api/v1/reporting/commission/runs") {
      if (!isManager) return managerWord();
      const windowRefusal = windowError();
      if (windowRefusal !== "") return refuse(res, 400, windowRefusal);
      const from = url.searchParams.get("from");
      const to = url.searchParams.get("to");
      const rep = url.searchParams.get("rep_user_id") ?? "";
      const items = world.reporting.commissionRuns
        .filter((p) => p.period_start >= from && p.period_end <= to && (rep === "" || p.rep_user_id === rep))
        .map((p) => ({ ...p, lines: p.lines ?? undefined }));
      return json(res, { items, posts_to_gl: false });
    }

    const crunMatch = path.match(/^\/api\/v1\/reporting\/commission\/runs\/([^/]+)$/);
    if (req.method === "GET" && crunMatch) {
      if (!isManager) return managerWord();
      const period = world.reporting.commissionRuns.find((p) => p.run_id === crunMatch[1]);
      if (!period) return notFound(res);
      return json(res, { ...period, lines: commissionLines(period.period_start) });
    }

    const crunCsvMatch = path.match(/^\/api\/v1\/reporting\/commission\/runs\/([^/]+)\/export\.csv$/);
    if (req.method === "GET" && crunCsvMatch) {
      if (!isManager) return managerWord();
      const period = world.reporting.commissionRuns.find((p) => p.run_id === crunCsvMatch[1]);
      if (!period) return notFound(res);
      res.writeHead(200, { "Content-Type": "text/csv; charset=utf-8" });
      res.end(csvOf(period));
      return;
    }

    const crunPrintMatch = path.match(/^\/api\/v1\/reporting\/commission\/runs\/([^/]+)\/print$/);
    if (req.method === "POST" && crunPrintMatch) {
      if (!isManager) return managerWord();
      const period = world.reporting.commissionRuns.find((p) => p.run_id === crunPrintMatch[1]);
      if (!period) return notFound(res);
      if ((period.branch_id ?? "") === "" && (body?.print_branch_id ?? "") === "") {
        return refuse(res, 400, "this commission period covers every branch, name the branch to print it at");
      }
      const run = {
        id: freshRunId(),
        kind: "COMMISSION",
        template_code: "commission",
        name: `Commission: ${period.rep_name}`,
        params: { from: period.period_start, to: period.period_end },
        branch_id: period.branch_id ?? "",
        row_count: period.sale_count,
        totals: { commission_cents: period.commission_cents },
        print_job_id: `pj-${world.reporting.runs.length + 1}`,
        run_by: USERS[flow].user_id,
        created_at: new Date().toISOString(),
        html: documentOf({ id: "x", name: `Commission: ${period.rep_name}`, template_code: "commission", print_job_id: `pj-${world.reporting.runs.length + 1}` }),
      };
      world.reporting.runs.unshift(run);
      return json(res, run, 201);
    }

    if (req.method === "GET" && path === "/api/v1/reporting/commission/accruals") {
      if (!isManager) return managerWord();
      const from = url.searchParams.get("from") ?? "";
      const to = url.searchParams.get("to") ?? "";
      if (!DATE_RE.test(from)) return refuse(res, 400, "from must be a date (YYYY-MM-DD)");
      if (!DATE_RE.test(to)) return refuse(res, 400, "to must be a date (YYYY-MM-DD)");
      const lines = world.reporting.commissionRuns
        .filter((p) => p.period_start >= from && p.period_end <= to)
        .map((p) => ({ rep_user_id: p.rep_user_id, rep_name: p.rep_name, period_count: 1, commission_cents: p.commission_cents }));
      return json(res, {
        from,
        to,
        lines,
        total_cents: lines.reduce((sum, l) => sum + l.commission_cents, 0),
        posts_to_gl: false,
        note: ACCRUAL_NOTE,
      });
    }

    if (req.method === "GET" && path === "/api/v1/reporting/commission/attribution-check") {
      if (!isManager) return managerWord();
      const from = url.searchParams.get("from") ?? "";
      const to = url.searchParams.get("to") ?? "";
      if (!DATE_RE.test(from)) return refuse(res, 400, "from must be a date (YYYY-MM-DD)");
      if (!DATE_RE.test(to)) return refuse(res, 400, "to must be a date (YYYY-MM-DD)");
      return json(res, {
        bound: false,
        checked: 3,
        mismatches: [
          {
            invoice_id: "0cd00000-0000-4000-9000-00000000i003",
            invoice_no: "GT-2118",
            customer_id: "0cf00000-0000-4000-9000-00000000p001",
            order_id: "0d000000-0000-4000-9000-00000000o001",
            report_rep_user_id: REPORT_REP_ID,
            seam_rep_user_id: "",
            seam_level: "order",
          },
        ],
        explanation: ATTRIBUTION_MISMATCH_EXPLANATION,
      });
    }
  }

  return notFound(res);
}

const server = createServer((req, res) => {
  handle(req, res).catch(() => refuse(res, 500, "stub error"));
});

server.listen(port, "127.0.0.1", () => {
  process.stdout.write(`manager stub on 127.0.0.1:${port}\n`);
});
