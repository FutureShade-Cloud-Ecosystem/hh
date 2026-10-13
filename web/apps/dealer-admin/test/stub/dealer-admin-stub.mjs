// Dealer Admin's local stub: the demo way in, and the dealer's staff world
// answered in the core's shapes (the staff register, the roles catalog, the
// branches, the platform hotkey rosters, the audit trail). It is a
// development and test double only; the real routes are the core's, and the
// staff identity admin family is contract-only there until the auth lane
// lands it.
import { createServer } from "node:http";

const port = Number(process.env.DA_STUB_PORT ?? 56610);

// The real flows route lists the closed table's logins; this app's door
// draws a card per flow it answers here. Two: the dealer admin (the
// general manager persona the admin surfaces answer), and inside sales for
// the refusal walk (a frontline caller is refused the staff read).
const FLOWS = [
  { flow: "dealer-admin", role_code: "general_manager", label: "Dealer admin" },
  { flow: "inside-sales", role_code: "inside_sales", label: "Inside sales" },
];

const USERS = {
  "dealer-admin": {
    user_id: "da110000-0000-4000-9000-000000000001",
    email: "dealer.admin@hh-demo.invalid",
    display_name: "Dealer admin (demo)",
    role_code: "general_manager",
    role_name: "General Manager",
    tier: 1,
    scope: "GLOBAL",
  },
  "inside-sales": {
    user_id: "da110000-0000-4000-9000-000000000002",
    email: "inside.sales@hh-demo.invalid",
    display_name: "Inside sales (demo)",
    role_code: "inside_sales",
    role_name: "Inside Sales",
    tier: 3,
    scope: "BRANCH",
  },
};

// The branches (GLS-TENANT-r3): Point Loma, the main yard, and Julian.
const BRANCHES = [
  {
    id: "5d1b3f6e-0000-4000-9000-0000000000pl",
    code: "PLM",
    name: "Gable Landscape Supply - Point Loma",
    is_active: true,
  },
  {
    id: "5d1b3f6e-0000-4000-9000-0000000000ju",
    code: "JUL",
    name: "Gable Landscape Supply - Julian",
    is_active: true,
  },
];
const PLM = BRANCHES[0].id;
const JUL = BRANCHES[1].id;

// The roles catalog, the core's own list (staffcontext/rbac.go AllRoles):
// the platform role and the thirteen business roles.
const ROLES = [
  { code: "SUPER_ADMIN", name: "Platform Administrator", tier: 1, scope: "GLOBAL" },
  { code: "GENERAL_MANAGER", name: "General Manager", tier: 1, scope: "GLOBAL" },
  { code: "FINANCIAL_CONTROLLER", name: "Financial Controller", tier: 1, scope: "GLOBAL" },
  { code: "HR", name: "HR", tier: 1, scope: "GLOBAL" },
  { code: "BRANCH_MANAGER", name: "Branch Manager", tier: 2, scope: "BRANCH" },
  { code: "PROCUREMENT_MANAGER", name: "Procurement Manager", tier: 2, scope: "BRANCH" },
  { code: "SALES_MANAGER", name: "Sales Manager", tier: 2, scope: "BRANCH" },
  { code: "YARD_MANAGER", name: "Yard Manager", tier: 2, scope: "BRANCH" },
  { code: "LOGISTICS_MANAGER", name: "Logistics Manager", tier: 2, scope: "BRANCH" },
  { code: "PAYABLES_RECEIVABLES", name: "Payables/Receivables", tier: 2, scope: "GLOBAL" },
  { code: "INSIDE_SALES", name: "Inside Sales", tier: 3, scope: "BRANCH" },
  { code: "OUTSIDE_SALES", name: "Outside Sales", tier: 3, scope: "BRANCH" },
  { code: "YARD_TEAM", name: "Yard Team", tier: 3, scope: "BRANCH" },
  { code: "DRIVERS", name: "Drivers", tier: 3, scope: "BRANCH" },
];

// The staff register: leadership, a frontline pair with branch grants, and
// the platform role row that renders read-only.
function buildStaff() {
  return [
    {
      user_id: USERS["dealer-admin"].user_id,
      email: USERS["dealer-admin"].email,
      display_name: USERS["dealer-admin"].display_name,
      role_code: "GENERAL_MANAGER",
      role_name: "General Manager",
      tier: 1,
      scope: "GLOBAL",
      branch_ids: [],
    },
    {
      user_id: "da110000-0000-4000-9000-0000000000hr",
      email: "hr@hh-demo.invalid",
      display_name: "HR (demo)",
      role_code: "HR",
      role_name: "HR",
      tier: 1,
      scope: "GLOBAL",
      branch_ids: [],
    },
    {
      user_id: USERS["inside-sales"].user_id,
      email: USERS["inside-sales"].email,
      display_name: USERS["inside-sales"].display_name,
      role_code: "INSIDE_SALES",
      role_name: "Inside Sales",
      tier: 3,
      scope: "BRANCH",
      branch_ids: [PLM],
    },
    {
      user_id: "da110000-0000-4000-9000-0000000000yt",
      email: "yard.team@hh-demo.invalid",
      display_name: "Yard team (demo)",
      role_code: "YARD_TEAM",
      role_name: "Yard Team",
      tier: 3,
      scope: "BRANCH",
      branch_ids: [PLM, JUL],
    },
    {
      user_id: "da110000-0000-4000-9000-0000000000sa",
      email: "platform.admin@hh-demo.invalid",
      display_name: "Platform admin (demo)",
      role_code: "super_admin",
      role_name: "Platform Administrator",
      tier: 1,
      scope: "GLOBAL",
      branch_ids: [],
    },
  ];
}

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
// Forbidden body is exactly {"error":"Forbidden"}.
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

let staff;
let hotkeys;
let audit;
let scenario;
let log;
let assist;

// The assist admin world, the core's own shapes: the spend report zero filled
// from the staff list (every active person appears, the ones who never opened
// the assistant included), the budgets with one role default and one person's
// override, and the four settings keys with the key's NAME and a no.
function buildAssist() {
  return {
    role_budgets: { GENERAL_MANAGER: 500000 },
    user_overrides: { [USERS["inside-sales"].user_id]: 0 },
    default_budget: 60000,
    exchanges: {
      [USERS["dealer-admin"].user_id]: { exchanges: 4, tokens_in: 300, tokens_out: 200 },
    },
    settings: {
      settings: { provider: "stub", model: "hos-stub-1", chat_enabled: true, spend_visible_to_non_admin: false },
      key_env_var: "HH_ASSIST_API_KEY",
      key_present: false,
    },
  };
}

function reset() {
  staff = buildStaff();
  hotkeys = buildHotkeys();
  audit = buildAudit();
  assist = buildAssist();
  scenario = "";
  log = [];
}
reset();

// The audit world: one sale whose trail spans three rows (the transaction
// drilldown's case), one sensitive price override, one ordinary status
// change. The masked projection is what the routes answer, the way the core
// enforces it.
function buildAudit() {
  const ago = (minutes) => new Date(Date.now() - minutes * 60 * 1000).toISOString();
  return [
    {
      id: "07000000-0000-4000-9000-0000000000a1",
      action: "SALE_COMPLETED",
      entity_type: "pos.sale",
      entity_id: "0f000000-0000-4000-9000-0000000000s1",
      actor_id: "da110000-0000-4000-9000-000000000002",
      actor_role: "",
      branch_id: BRANCHES[0].id,
      changes: { total_cents: 19680, lines: 2, tender: "CASH" },
      request_id: "req-0001",
      created_at: ago(30),
      seq: 41,
    },
    {
      id: "07000000-0000-4000-9000-0000000000a2",
      action: "ORDER_STATUS_CHANGED",
      entity_type: "pos.sale",
      entity_id: "0f000000-0000-4000-9000-0000000000s1",
      actor_id: "da110000-0000-4000-9000-000000000002",
      actor_role: "",
      branch_id: BRANCHES[0].id,
      changes: { from: "OPEN", to: "PICKED" },
      request_id: "req-0002",
      created_at: ago(20),
      seq: 42,
    },
    {
      id: "07000000-0000-4000-9000-0000000000a3",
      action: "PRICE_OVERRIDE",
      entity_type: "pricing.override",
      entity_id: "0f000000-0000-4000-9000-0000000000o1",
      actor_id: "da110000-0000-4000-9000-000000000001",
      actor_role: "",
      branch_id: BRANCHES[0].id,
      changes: { sku: "PAV-60-CH", from_cents: 480, to_cents: 420, reason: "contract price match" },
      request_id: "req-0003",
      created_at: ago(10),
      seq: 43,
    },
  ];
}

// The platform rosters as the read serves them: the shell's map with one
// seeded binding (F2, deliberately also the counter's search key: the cross
// substrate claim is the shipped design, and the read-only walk sees the
// shared note when it is rebound), the counter's with the shipped BisTrack
// defaults, and the shell's own reserved keys, served rather than mirrored.
function buildHotkeys() {
  return {
    rosters: [
      {
        surface: "app.shell",
        keys: [{ action: "sale.cash.new", binding: "F2", position: 0 }],
        dealer: [],
      },
      {
        surface: "pos.counter",
        keys: [
          { action: "step-next", binding: "f4", position: 0 },
          { action: "step-back", binding: "shift-f4", position: 1 },
          { action: "pay", binding: "f12", position: 2 },
          { action: "park", binding: "f9", position: 3 },
          { action: "search", binding: "f2", position: 4 },
        ],
        dealer: [],
      },
    ],
    can_edit: true,
    reserved: [{ binding: "F1", holds: "opens the shortcut list" }],
  };
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

// The core's gates: the staff read is a manager's (tier 1 or 2); the writes
// are leadership's (tier 1 GLOBAL). A frontline caller is refused both with
// the shared Forbidden body.
function isManager(flow) {
  return flow === "dealer-admin";
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
      return json(res, { token: mintToken(account.user_id), user: account, flow });
    } catch {
      return refuse(res, 400, "bad request");
    }
  }

  // ---- the staff routes ----
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

  if (req.method === "GET" && path === "/api/v1/branches") {
    return json(res, BRANCHES);
  }

  // The real core's mux answers the unregistered staff family with a bare 404.
  if (scenario === "unregistered-staff" && path.startsWith("/api/v1/staff")) {
    res.writeHead(404, { "content-type": "text/plain; charset=utf-8" });
    return res.end("404 page not found\n");
  }

  if (req.method === "GET" && path === "/api/v1/staff") {
    if (!isManager(flow) || scenario === "refused-staff") return refuse(res, 403, "Forbidden");
    const items = scenario === "empty-staff" ? [] : staff;
    return json(res, { items, next_cursor: null, limit: 200 });
  }

  if (req.method === "GET" && path === "/api/v1/staff/roles") {
    if (!isManager(flow) || scenario === "refused-staff") return refuse(res, 403, "Forbidden");
    return json(res, { items: ROLES, next_cursor: null, limit: 200 });
  }

  const roleMatch = path.match(/^\/api\/v1\/staff\/([^/]+)\/role$/);
  if (req.method === "PUT" && roleMatch) {
    if (!isManager(flow) || scenario === "refused-assign") return refuse(res, 403, "Forbidden");
    const member = staff.find((m) => m.user_id === roleMatch[1]);
    if (!member) return refuse(res, 404, "staff not found");
    const roleCode = String(body?.role_code ?? "");
    const role = ROLES.find((r) => r.code === roleCode);
    if (roleCode !== "" && role === undefined) return refuse(res, 400, "unknown role");
    member.role_code = roleCode;
    member.role_name = role === undefined ? "" : role.name;
    member.tier = role === undefined ? 0 : role.tier;
    member.scope = role === undefined ? "" : role.scope;
    return json(res, { status: "ok", user_id: member.user_id, role_code: roleCode });
  }

  const resetMatch = path.match(/^\/api\/v1\/staff\/([^/]+)\/password-reset$/);
  if (req.method === "POST" && resetMatch) {
    if (!isManager(flow) || scenario === "refused-reset") return refuse(res, 403, "Forbidden");
    const member = staff.find((m) => m.user_id === resetMatch[1]);
    if (!member) return refuse(res, 404, "staff not found");
    const token = `st-${member.user_id.slice(0, 8)}-${Date.now().toString(36)}`;
    return json(res, {
      reset_token: token,
      reset_url: `https://reset.hh-demo.invalid/${token}`,
      expires_at: new Date(Date.now() + 24 * 3600 * 1000).toISOString(),
    });
  }

  // ---- the platform hotkeys ----

  if (req.method === "GET" && path === "/api/v1/platform/hotkeys") {
    if (scenario === "refused-hotkeys") return refuse(res, 403, "Forbidden");
    const view = { ...hotkeys, can_edit: scenario === "readonly-hotkeys" ? false : hotkeys.can_edit };
    return json(res, view);
  }

  const surfaceMatch = path.match(/^\/api\/v1\/platform\/hotkeys\/([^/]+)$/);
  if (req.method === "PUT" && surfaceMatch) {
    if (!isManager(flow) || scenario === "refused-hotkeys") return refuse(res, 403, "Forbidden");
    const surface = decodeURIComponent(surfaceMatch[1]);
    const roster = hotkeys.rosters.find((r) => r.surface === surface);
    if (roster === undefined) return refuse(res, 404, "unknown surface");
    const keys = Array.isArray(body?.keys) ? body.keys : null;
    if (keys === null || keys.length === 0) return refuse(res, 400, "settings: the roster may not be empty");
    roster.keys = keys.map((k, i) => ({
      action: String(k.action ?? ""),
      binding: String(k.binding ?? ""),
      position: typeof k.position === "number" ? k.position : i,
    }));
    return json(res, { saved: true });
  }

  // ---- the audit trail ----

  if (req.method === "GET" && path === "/api/v1/audit") {
    // The trail is an admin read: a frontline caller is refused with the
    // shared Forbidden body.
    if (!isManager(flow) || scenario === "refused-audit") return refuse(res, 403, "Forbidden");
    const entityType = url.searchParams.get("entity_type") ?? "";
    const action = url.searchParams.get("action") ?? "";
    const actorId = url.searchParams.get("actor_id") ?? "";
    const since = url.searchParams.get("since") ?? "";
    // The core refuses an actor id that is not a UUID with a 400 in words.
    if (actorId !== "" && !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(actorId)) {
      return refuse(res, 400, "audit: actor_id must be a UUID");
    }
    const items = audit.filter((e) => {
      if (entityType !== "" && e.entity_type !== entityType) return false;
      if (action !== "" && e.action !== action) return false;
      if (actorId !== "" && e.actor_id !== actorId) return false;
      if (since !== "" && new Date(e.created_at).getTime() < new Date(since).getTime()) return false;
      return true;
    });
    return json(res, { items, next_cursor: null, limit: 200 });
  }

  const entryMatch = path.match(/^\/api\/v1\/audit\/([^/]+)$/);
  if (req.method === "GET" && entryMatch && entryMatch[1] !== "transaction") {
    if (!isManager(flow) || scenario === "refused-audit") return refuse(res, 403, "Forbidden");
    const entry = audit.find((e) => e.id === entryMatch[1]);
    if (entry === undefined) return refuse(res, 404, "entry not found");
    return json(res, entry);
  }

  const transactionMatch = path.match(/^\/api\/v1\/audit\/transaction\/([^/]+)\/([^/]+)$/);
  if (req.method === "GET" && transactionMatch) {
    if (!isManager(flow) || scenario === "refused-audit") return refuse(res, 403, "Forbidden");
    const entityType = decodeURIComponent(transactionMatch[1]);
    const entityId = decodeURIComponent(transactionMatch[2]);
    const items = audit.filter((e) => e.entity_type === entityType && e.entity_id === entityId);
    // The real core's reopen handler writes a bare array, not the page.
    if (scenario === "bare-trail") return json(res, items);
    if (scenario === "empty-trail") return json(res, []);
    return json(res, { items, next_cursor: null, limit: 200 });
  }

  // ---- the assist admin (the core's RequireAdmin: tier 1 global) ----

  const assistSpendReport = () => {
    // The core zero fills the report from the staff list, biggest spender
    // first: the exchange count is the answer, the tokens the next question.
    const rows = staff.map((m) => {
      const used = assist.exchanges[m.user_id] ?? { exchanges: 0, tokens_in: 0, tokens_out: 0 };
      const override = assist.user_overrides[m.user_id];
      const roleBudget = assist.role_budgets[m.role_code];
      const budget =
        override !== undefined
          ? { tokens: override, source: "user" }
          : roleBudget !== undefined
            ? { tokens: roleBudget, source: "role" }
            : { tokens: assist.default_budget, source: "default" };
      return {
        user_id: m.user_id,
        display_name: m.display_name,
        email: m.email,
        role_code: m.role_code,
        exchanges: used.exchanges,
        tokens_in: used.tokens_in,
        tokens_out: used.tokens_out,
        budget,
      };
    });
    rows.sort((a, b) => b.tokens_in + b.tokens_out - (a.tokens_in + a.tokens_out) || b.exchanges - a.exchanges);
    return { month: "2026-10", users: rows };
  };

  if (req.method === "GET" && path === "/api/v1/assist/admin/spend") {
    if (!isManager(flow) || scenario === "refused-assist") return refuse(res, 403, "Forbidden");
    return json(res, assistSpendReport());
  }

  if (req.method === "GET" && path === "/api/v1/assist/admin/limits") {
    if (!isManager(flow) || scenario === "refused-assist") return refuse(res, 403, "Forbidden");
    return json(res, {
      role_budgets: assist.role_budgets,
      user_overrides: assist.user_overrides,
      default_budget: assist.default_budget,
    });
  }

  const roleLimitMatch = path.match(/^\/api\/v1\/assist\/admin\/limits\/role\/([^/]+)$/);
  if (req.method === "PUT" && roleLimitMatch) {
    if (!isManager(flow) || scenario === "refused-assist") return refuse(res, 403, "Forbidden");
    const role = decodeURIComponent(roleLimitMatch[1]);
    const tokens = Number(body?.tokens);
    if (!Number.isInteger(tokens) || tokens < 0) {
      return refuse(res, 400, "A budget can't be negative. Use 0 to switch the assistant off for this role.");
    }
    assist.role_budgets[role] = tokens;
    return json(res, { role_code: role, tokens });
  }

  const userLimitMatch = path.match(/^\/api\/v1\/assist\/admin\/limits\/user\/([^/]+)$/);
  if (req.method === "PUT" && userLimitMatch) {
    if (!isManager(flow) || scenario === "refused-assist") return refuse(res, 403, "Forbidden");
    const user = decodeURIComponent(userLimitMatch[1]);
    const tokens = Number(body?.tokens);
    if (!Number.isInteger(tokens) || tokens < 0) {
      return refuse(res, 400, "A budget can't be negative. Use 0 to switch the assistant off for this person.");
    }
    assist.user_overrides[user] = tokens;
    return json(res, { user_id: user, tokens, source: "user" });
  }

  if (req.method === "DELETE" && userLimitMatch) {
    if (!isManager(flow) || scenario === "refused-assist") return refuse(res, 403, "Forbidden");
    // Reset to role is idempotent: resetting a person with no override succeeds.
    delete assist.user_overrides[decodeURIComponent(userLimitMatch[1])];
    return json(res, { user_id: decodeURIComponent(userLimitMatch[1]), source: "role" });
  }

  if (req.method === "GET" && path === "/api/v1/assist/admin/settings") {
    if (!isManager(flow) || scenario === "refused-assist") return refuse(res, 403, "Forbidden");
    return json(res, assist.settings);
  }

  if (req.method === "PUT" && path === "/api/v1/assist/admin/settings") {
    if (!isManager(flow) || scenario === "refused-assist") return refuse(res, 403, "Forbidden");
    const key = String(body?.key ?? "");
    const value = String(body?.value ?? "");
    const yesNo = key === "chat_enabled" || key === "spend_visible_to_non_admin";
    if (!yesNo) return refuse(res, 400, `"${key}" isn't an assistant setting.`);
    if (value !== "true" && value !== "false") {
      return refuse(res, 400, `${key} is a yes/no setting, use true or false.`);
    }
    assist.settings.settings[key] = value === "true";
    return json(res, { key, value });
  }

  return notFound(res);
}

const server = createServer((req, res) => {
  handle(req, res).catch(() => refuse(res, 500, "stub error"));
});

server.listen(port, "127.0.0.1", () => {
  process.stdout.write(`dealer-admin stub on 127.0.0.1:${port}\n`);
});
