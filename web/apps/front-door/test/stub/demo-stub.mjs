// The front door's local demo stub: the one demo route the app speaks, in the
// contract's shape, for the dev server's /api proxy and the acceptance
// harness. This is a development and test double only: the real route is the
// core's (the mint is the demo build's), and nothing in the app imports this
// file. The phrase gate in front of the real demo box is the image's Caddy;
// this stub checks no credential, like the core's own public demo routes.
//
//   GET  /api/v1/demo/flows   -> the closed table with each row's class (staff rows, then the five contractor rows)
//   POST /api/v1/demo/session -> DemoSessionResponse, or the armed refusal
//   POST /api/v1/feedback     -> 201 { issue_id, issue_number: HH-42 }, or the
//                                armed board refusal; the last submission's
//                                fields and image parts are recorded for peek
//   GET  /healthz             -> 200, for the harness's readiness probe
//   GET  /_stub/peek          -> what the last mint request carried (the org
//                                header the app's client configuration sends)
//                                and what the last feedback submission carried
//   POST /_stub/refuse        -> arm a refusal: { status, error, flows }
//   POST /_stub/refuse-feedback -> arm the board refusal the next feedback
//                                submission answers, once per arming
//   POST /_stub/feedback-numberless -> the next feedback answers carry no
//                                issue_number, until the next reset
//   POST /_stub/reset         -> clear the armed refusals and the ledgers
//
// The console mode's doubles (item 7b), one world behind the same port:
//   GET  /config.json         -> the served runtime configuration; demo mode
//                                until a leg posts another to /_stub/config
//   POST /_stub/config        -> { config } the file to serve from now on
//   POST /_stub/console       -> arm the console world: { signin, exchange,
//                                methods, persona, windowSeconds, role,
//                                delayMs } (see CONSOLE below)
//   /console/v1/account...    -> a fake FB Console: email session create (sets
//                                an httpOnly cookie), account read, JWT mint,
//                                session delete, the Google route
//   POST /api/v1/auth/exchange, GET /api/v1/auth/methods,
//   POST /api/v1/auth/persona -> the core's three new public routes
//
// The mint answers a JWT-shaped token so the app's expiry read (the token's
// exp claim) sees the same shape the real mint issues: three segments, a
// payload with sub and exp, and a signature segment. The signature here is
// stub bytes; on the real box it is the demo issuer's, verified by the core.
// The user block is shaped field for field like the staff login user block
// (core/api/openapi.yaml, IdentityUser), the same accounts the Counter
// Till's stub mints, so both apps' validations read the same answers.
import { createServer } from "node:http";

const port = Number(process.env.FD_STUB_PORT ?? 4927);

// The flow table as the front door offers it: the live roles whose screens
// this box serves, with the labels the cards carry and the role each minted
// account holds. The core's table mints the personas the roles roadmap is
// bringing live beside these; the front door offers a card only for a role
// whose screens exist, so its stub answers the same set (the financial
// controller's mint exists for the money lanes' tests while its card is
// still coming).
const FLOWS = [
  { flow: "sales", role_code: "inside_sales", label: "Sales" },
  { flow: "sales-manager", role_code: "sales_manager", label: "Sales manager" },
  { flow: "yard-manager", role_code: "yard_manager", label: "Yard manager" },
  { flow: "yard-team", role_code: "yard_team", label: "Yard Team" },
  { flow: "driver", role_code: "driver", label: "Driver" },
  { flow: "payables-receivables", role_code: "payables_receivables", label: "Payables and Receivables" },
  { flow: "procurement-manager", role_code: "procurement_manager", label: "Procurement manager" },
  { flow: "financial-controller", role_code: "financial_controller", label: "Financial Controller" },
  { flow: "logistics-manager", role_code: "logistics_manager", label: "Logistics Manager" },
];

// Summit Ridge Builders' contractor personas, as the core's listing carries them (class "tenant"). The front
// door offers a card for each and opens the Pro app for the role; it never mints them, so USERS holds none.
const CONTRACTOR_FLOWS = [
  { flow: "contractor-account-admin", role_code: "account_admin", label: "Contractor owner", class: "tenant" },
  { flow: "contractor-buyer", role_code: "buyer", label: "Contractor buyer", class: "tenant" },
  { flow: "contractor-buyer-no-pay", role_code: "buyer_no_pay", label: "Contractor buyer (no pay)", class: "tenant" },
  { flow: "contractor-ap-finance", role_code: "ap_finance", label: "Contractor AP", class: "tenant" },
  { flow: "contractor-field-crew", role_code: "field_crew", label: "Contractor field crew", class: "tenant" },
];

const USERS = {
  sales: {
    user_id: "demo-account-sales",
    email: "sales@demo.invalid",
    display_name: "Sales (demo)",
    role_code: "inside_sales",
    role_name: "Inside Sales",
    tier: 3,
    scope: "BRANCH",
  },
  "sales-manager": {
    user_id: "demo-account-sales-manager",
    email: "sales-manager@demo.invalid",
    display_name: "Sales manager (demo)",
    role_code: "sales_manager",
    role_name: "Sales Manager",
    tier: 2,
    scope: "BRANCH",
  },
  "yard-manager": {
    user_id: "demo-account-yard-manager",
    email: "yard-manager@demo.invalid",
    display_name: "Yard manager (demo)",
    role_code: "yard_manager",
    role_name: "Yard manager",
    tier: 2,
    scope: "BRANCH",
  },
  "yard-team": {
    user_id: "demo-account-yard-team",
    email: "yard-team@demo.invalid",
    display_name: "Yard persona (demo)",
    role_code: "yard_team",
    role_name: "Yard Team",
    tier: 3,
    scope: "BRANCH",
  },
  "payables-receivables": {
    user_id: "demo-account-payables-receivables",
    email: "payables-receivables@demo.invalid",
    display_name: "Payables and Receivables (demo)",
    role_code: "payables_receivables",
    role_name: "Payables and Receivables",
    tier: 2,
    scope: "GLOBAL",
  },
  "procurement-manager": {
    user_id: "demo-account-procurement-manager",
    email: "procurement-manager@demo.invalid",
    display_name: "Procurement manager (demo)",
    role_code: "procurement_manager",
    role_name: "Procurement Manager",
    tier: 2,
    scope: "BRANCH",
  },
  "financial-controller": {
    user_id: "demo-account-financial-controller",
    email: "financial-controller@demo.invalid",
    display_name: "Financial controller (demo)",
    role_code: "financial_controller",
    role_name: "Financial Controller",
    tier: 1,
    scope: "GLOBAL",
  },
  "logistics-manager": {
    user_id: "demo-account-logistics-manager",
    email: "logistics-manager@demo.invalid",
    display_name: "Dispatch persona (demo)",
    role_code: "logistics_manager",
    role_name: "Logistics Manager",
    tier: 2,
    scope: "BRANCH",
  },
  driver: {
    user_id: "demo-account-driver",
    email: "driver@demo.invalid",
    display_name: "Driver (demo)",
    role_code: "driver",
    role_name: "Driver",
    tier: 3,
    scope: "BRANCH",
  },
};

// The armed refusal: a test points the stub at a failure (the gate's 401, the
// mint's 429 limit, a 503 for an account the box cannot resolve) and the next
// mint for one of the named flows answers it, once per arming.
let refusal = null;
// The flows listing answers 503 while armed (the contractor cards then show nothing; the staff cards still work).
let listingFails = false;
let lastOrg = "";
let mints = 0;
let pendingApprovalCount = 3;
let extraApprovals = 0;
let approvalsDelayMs = 0;

// The feedback bridge's own state: the armed board refusal (answered once)
// and the last submission the widget posted, its text fields read out of the
// multipart body and its images counted, so the acceptance spec asserts what
// the widget actually sent without ever reaching a real board.
let feedbackRefusal = null;
let feedbackNumberless = false;
let lastFeedback = null;

function base64Url(value) {
  return Buffer.from(JSON.stringify(value), "utf8")
    .toString("base64")
    .replaceAll("+", "-")
    .replaceAll("/", "_")
    .replace(/=+$/, "");
}

// Three segments, a payload with sub and exp, a signature of stub bytes: the
// shape the app's expiry read needs, nothing more.
function mintToken(userId) {
  const header = base64Url({ alg: "HS256", typ: "JWT" });
  const payload = base64Url({ sub: userId, exp: Math.floor(Date.now() / 1000) + 3600 });
  const signature = base64Url({ stub: "signature-bytes" });
  return `${header}.${payload}.${signature}`;
}

async function readBody(request) {
  const chunks = [];
  for await (const chunk of request) chunks.push(chunk);
  const raw = Buffer.concat(chunks).toString("utf8");
  if (raw === "") return null;
  try {
    return JSON.parse(raw);
  } catch {
    return null;
  }
}

function sendJson(response, status, body) {
  const raw = JSON.stringify(body);
  response.writeHead(status, {
    "content-type": "application/json",
    "content-length": Buffer.byteLength(raw),
  });
  response.end(raw);
}

// ── CONSOLE: the fake FB Console and the core's auth routes ──────────────────
// Every value is fake. The fake console issues an httpOnly cookie on a good
// sign in, so the page's credentialed fetches behave as they do against the
// real one, and the core's exchange trades a minted "JWT" for a token whose
// life is windowSeconds. Refusals use the shared bodies (the core never
// explains).
const CONSOLE_EMAIL = "partner.one@hh-demo.invalid";
const CONSOLE_PASSWORD = "correct horse battery";
const CONSOLE_USER = { $id: "console-user-1", email: CONSOLE_EMAIL, name: "Partner One" };
const SHARED_BODIES = {
  400: { error: "Bad Request" },
  401: { error: "Unauthorized" },
  403: { error: "Forbidden" },
  429: { error: "Too Many Requests" },
  503: { error: "Service Unavailable" },
};
let servedConfig = { mode: "demo" };
let consoleCookie = false;
let consoleArms = {};
let consoleLedger = { signins: [], exchanges: 0, jwts: 0, personas: [], methodsAsked: [] };

function resetConsole() {
  servedConfig = { mode: "demo" };
  consoleCookie = false;
  consoleArms = {};
  consoleLedger = { signins: [], exchanges: 0, jwts: 0, personas: [], methodsAsked: [] };
}

function sharedBody(status) {
  return SHARED_BODIES[status] ?? { error: "Error" };
}

async function pause(ms) {
  if (typeof ms === "number" && ms > 0) await new Promise((resolve) => setTimeout(resolve, ms));
}

function consoleUserBlock() {
  const role = consoleArms.role ?? { role_code: "sales_manager", role_name: "Sales Manager" };
  return {
    user_id: CONSOLE_USER.$id,
    email: CONSOLE_USER.email,
    display_name: CONSOLE_USER.name,
    role_code: role.role_code,
    role_name: role.role_name,
    tier: 2,
    scope: "BRANCH",
  };
}

function coreToken(userId, seconds) {
  const header = base64Url({ alg: "ES256", typ: "JWT" });
  const payload = base64Url({ sub: userId, exp: Math.floor(Date.now() / 1000) + seconds });
  return `${header}.${payload}.${base64Url({ stub: "signature-bytes" })}`;
}

/** Answer an armed status once if one is set for the route; true when it answered. */
function answerArmed(response, key) {
  const armed = consoleArms[key];
  if (armed === undefined || armed === null || typeof armed.status !== "number") return false;
  if (typeof armed.times === "number") {
    armed.times -= 1;
    if (armed.times <= 0) delete consoleArms[key];
  }
  sendJson(response, armed.status, sharedBody(armed.status));
  return true;
}

async function handleConsole(request, response, url) {
  const path = url.pathname;
  const sub = path.startsWith("/console/v1") ? path.slice("/console/v1".length) : null;

  if (path === "/config.json") {
    sendJson(response, 200, servedConfig);
    return true;
  }
  if (path === "/_stub/config" && request.method === "POST") {
    const body = await readBody(request);
    servedConfig = body !== null && typeof body === "object" && "config" in body ? body.config : { mode: "demo" };
    sendJson(response, 200, { served: true });
    return true;
  }
  if (path === "/_stub/console" && request.method === "POST") {
    const body = await readBody(request);
    consoleArms = { ...consoleArms, ...(body ?? {}) };
    sendJson(response, 200, { armed: true });
    return true;
  }
  if (path === "/_stub/console-peek") {
    sendJson(response, 200, { ...consoleLedger, cookie: consoleCookie });
    return true;
  }

  if (sub === "/account/sessions/email" && request.method === "POST") {
    const body = await readBody(request);
    await pause(consoleArms.delayMs?.signin);
    consoleLedger.signins.push({ email: body?.email ?? "", passwordLength: String(body?.password ?? "").length });
    if (answerArmed(response, "signin")) return true;
    if (body?.email !== CONSOLE_EMAIL || body?.password !== CONSOLE_PASSWORD) {
      sendJson(response, 401, { message: "Invalid credentials", code: 401, type: "user_invalid_credentials" });
      return true;
    }
    consoleCookie = true;
    const raw = JSON.stringify({ $id: "sess-1", userId: CONSOLE_USER.$id, provider: "email" });
    response.writeHead(201, {
      "content-type": "application/json",
      "content-length": Buffer.byteLength(raw),
      "set-cookie": "a_session_stub=1; Path=/; HttpOnly; SameSite=Lax",
    });
    response.end(raw);
    return true;
  }
  if (sub === "/account" && request.method === "GET") {
    const cookie = String(request.headers.cookie ?? "").includes("a_session_stub=1");
    if (!cookie || !consoleCookie) {
      sendJson(response, 401, { message: "No session", code: 401, type: "general_unauthorized_scope" });
      return true;
    }
    sendJson(response, 200, CONSOLE_USER);
    return true;
  }
  if (sub === "/account/jwts" && request.method === "POST") {
    const cookie = String(request.headers.cookie ?? "").includes("a_session_stub=1");
    if (!cookie || !consoleCookie) {
      sendJson(response, 401, { message: "No session", code: 401 });
      return true;
    }
    consoleLedger.jwts += 1;
    sendJson(response, 201, { jwt: `stub.jwt.${consoleLedger.jwts}` });
    return true;
  }
  if (sub === "/account/sessions/current" && request.method === "DELETE") {
    consoleCookie = false;
    response.writeHead(204, { "set-cookie": "a_session_stub=; Path=/; HttpOnly; Max-Age=0" });
    response.end();
    return true;
  }
  if (sub === "/account/sessions/oauth2/google" && request.method === "GET") {
    const failed = consoleArms.google === "fail";
    const target = failed ? url.searchParams.get("failure") : url.searchParams.get("success");
    if (!failed) consoleCookie = true;
    response.writeHead(302, {
      location: target ?? "/",
      ...(failed ? {} : { "set-cookie": "a_session_stub=1; Path=/; HttpOnly; SameSite=Lax" }),
    });
    response.end();
    return true;
  }

  if (path === "/api/v1/auth/exchange" && request.method === "POST") {
    await pause(consoleArms.delayMs?.exchange);
    consoleLedger.exchanges += 1;
    const body = await readBody(request);
    if (body === null || typeof body.jwt !== "string" || typeof body.org !== "string") {
      sendJson(response, 400, sharedBody(400));
      return true;
    }
    if (answerArmed(response, "exchange")) return true;
    const seconds = typeof consoleArms.windowSeconds === "number" ? consoleArms.windowSeconds : 900;
    const user = consoleUserBlock();
    sendJson(response, 200, {
      token: coreToken(user.user_id, seconds),
      expiresAtMs: Date.now() + seconds * 1000,
      user,
    });
    return true;
  }
  if (path === "/api/v1/auth/methods" && request.method === "GET") {
    consoleLedger.methodsAsked.push(url.searchParams.get("org") ?? "");
    await pause(consoleArms.delayMs?.methods);
    if (answerArmed(response, "methodsStatus")) return true;
    sendJson(response, 200, { methods: consoleArms.methods ?? ["email_password", "google"] });
    return true;
  }
  if (path === "/api/v1/auth/persona" && request.method === "POST") {
    const body = await readBody(request);
    const persona = typeof body?.persona === "string" ? body.persona : "";
    consoleLedger.personas.push({ persona, authorized: String(request.headers.authorization ?? "") !== "" });
    await pause(consoleArms.delayMs?.persona);
    const perPersona = consoleArms.personaStatus?.[persona];
    if (typeof perPersona === "number") {
      sendJson(response, perPersona, sharedBody(perPersona));
      return true;
    }
    if (answerArmed(response, "persona")) return true;
    const seconds = typeof consoleArms.personaWindowSeconds === "number" ? consoleArms.personaWindowSeconds : 600;
    // Keyed by the core's persona id, which is the flow id (sales-manager), not the role code.
    const row = FLOWS.find((candidate) => candidate.flow === persona);
    const user = row === undefined ? undefined : USERS[row.flow];
    if (user === undefined) {
      sendJson(response, 400, sharedBody(400));
      return true;
    }
    const block = { ...user, user_id: `persona:${persona}`, email: `${row.role_code}@hh-demo.invalid`, display_name: user.role_name };
    sendJson(response, 200, { token: coreToken(block.user_id, seconds), expiresAtMs: Date.now() + seconds * 1000, user: block });
    return true;
  }
  return false;
}

const server = createServer(async (request, response) => {
  const url = new URL(request.url ?? "/", `http://127.0.0.1:${port}`);
  const path = url.pathname;

  if (path === "/healthz") {
    sendJson(response, 200, { ok: true });
    return;
  }

  if (await handleConsole(request, response, url)) return;

  if (path === "/_stub/peek") {
    sendJson(response, 200, {
      lastOrg,
      mints,
      refusalArmed: refusal !== null,
      feedback: lastFeedback,
      feedbackRefusalArmed: feedbackRefusal !== null,
    });
    return;
  }

  if (path === "/_stub/feedback-numberless" && request.method === "POST") {
    feedbackNumberless = true;
    sendJson(response, 200, { armed: true });
    return;
  }

  if (path === "/_stub/refuse-feedback" && request.method === "POST") {
    const body = await readBody(request);
    if (body === null || typeof body !== "object" || typeof body.error !== "string") {
      sendJson(response, 400, { error: "arm the board refusal with an error" });
      return;
    }
    feedbackRefusal = { status: typeof body.status === "number" ? body.status : 502, error: body.error };
    sendJson(response, 200, { armed: true });
    return;
  }

  if (path === "/_stub/refuse" && request.method === "POST") {
    const body = await readBody(request);
    if (
      body === null ||
      typeof body !== "object" ||
      typeof body.status !== "number" ||
      typeof body.error !== "string" ||
      !Array.isArray(body.flows)
    ) {
      sendJson(response, 400, { error: "arm a refusal with status, error and flows" });
      return;
    }
    refusal = body;
    sendJson(response, 200, { armed: true });
    return;
  }

  if (path === "/_stub/listing-fails" && request.method === "POST") {
    listingFails = true;
    sendJson(response, 200, { armed: true });
    return;
  }

  if (path === "/_stub/reset" && request.method === "POST") {
    refusal = null;
    listingFails = false;
    feedbackRefusal = null;
    feedbackNumberless = false;
    lastFeedback = null;
    lastOrg = "";
    mints = 0;
    pendingApprovalCount = 3;
    extraApprovals = 0;
    approvalsDelayMs = 0;
    resetConsole();
    sendJson(response, 200, { reset: true });
    return;
  }

  if (path === "/api/v1/feedback" && request.method === "POST") {
    // The widget posts multipart/form-data (text fields plus image parts).
    // The fields are read out of the raw body by boundary scanning, enough
    // for the spec's assertions without a multipart parser dependency.
    const contentType = String(request.headers["content-type"] ?? "");
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    const raw = Buffer.concat(chunks);
    const fields = {};
    const boundary = /boundary=(.+)$/.exec(contentType)?.[1]?.trim();
    if (boundary !== undefined) {
      const parts = raw.toString("binary").split(`--${boundary}`);
      for (const part of parts) {
        const name = /name="([^"]+)"/.exec(part)?.[1];
        if (name === undefined) continue;
        if (/filename="/.test(part)) {
          fields[name] = Number(fields[name] ?? 0) + 1;
        } else {
          fields[name] = part.split("\r\n\r\n")[1]?.replace(/\r\n$/, "") ?? "";
        }
      }
    }
    lastFeedback = { fields, bytes: raw.length, org: request.headers["x-org"] ?? "" };
    if (feedbackRefusal !== null) {
      const armed = feedbackRefusal;
      feedbackRefusal = null;
      sendJson(response, armed.status, { error: armed.error });
      return;
    }
    sendJson(response, 201, feedbackNumberless ? { issue_id: "issue-stub-1" } : { issue_id: "issue-stub-1", issue_number: "HH-42" });
    return;
  }

  if (path === "/_stub/set-approvals" && request.method === "POST") {
    const body = await readBody(request);
    if (body === null || typeof body !== "object" || typeof body.count !== "number") {
      sendJson(response, 400, { error: "set approvals with { count: number }" });
      return;
    }
    pendingApprovalCount = Math.max(0, body.count);
    // extra rows that are not pending margin requests and must not be counted.
    extraApprovals = typeof body.extra === "number" ? Math.max(0, body.extra) : 0;
    approvalsDelayMs = typeof body.delayMs === "number" ? Math.max(0, body.delayMs) : 0;
    sendJson(response, 200, { count: pendingApprovalCount });
    return;
  }

  if (path === "/api/v1/staff/approvals" && request.method === "GET") {
    if (approvalsDelayMs > 0) await new Promise((resolve) => setTimeout(resolve, approvalsDelayMs));
    const row = (id, policy, status) => ({
      id,
      status,
      requested_by: "demo-account-sales-manager",
      branch_id: "demo-branch",
      policy_type: policy,
      detail: "Demo approval",
    });
    const items = Array.from({ length: pendingApprovalCount }, (_, i) => row(`approval-${i + 1}`, "MARGIN_BELOW_MINIMUM", "PENDING"));
    for (let i = 0; i < extraApprovals; i += 1) {
      items.push(row(`other-${i + 1}`, "CREDIT_LIMIT_EXCEEDED", "PENDING"));
      items.push(row(`decided-${i + 1}`, "MARGIN_BELOW_MINIMUM", "APPROVED"));
    }
    // The contract's page, over two pages when there is more than one row so the cursor is followed.
    const cursor = url.searchParams.get("cursor");
    if (items.length > 1 && cursor === null) {
      sendJson(response, 200, { items: items.slice(0, 1), next_cursor: "p2", limit: 1 });
    } else if (items.length > 1) {
      sendJson(response, 200, { items: items.slice(1), next_cursor: null, limit: 20 });
    } else {
      sendJson(response, 200, { items, next_cursor: null, limit: 20 });
    }
    return;
  }

  if (path === "/api/v1/demo/flows" && request.method === "GET") {
    if (listingFails) {
      sendJson(response, 503, { error: "the listing is not answering" });
      return;
    }
    sendJson(response, 200, { flows: [...FLOWS.map((row) => ({ ...row, class: "staff" })), ...CONTRACTOR_FLOWS] });
    return;
  }

  if (path === "/api/v1/demo/session" && request.method === "POST") {
    lastOrg = request.headers["x-org"] ?? "";
    const body = await readBody(request);
    const flow = body !== null && typeof body === "object" && typeof body.flow === "string" ? body.flow : "";
    if (refusal !== null && refusal.flows.includes(flow)) {
      const armed = refusal;
      refusal = null;
      sendJson(response, armed.status, { error: armed.error });
      return;
    }
    const row = FLOWS.find((candidate) => candidate.flow === flow);
    const user = row === undefined ? undefined : USERS[row.flow];
    if (row === undefined || user === undefined) {
      sendJson(response, 422, { error: `the demo flow table holds no ${flow} login` });
      return;
    }
    mints += 1;
    sendJson(response, 200, { token: mintToken(user.user_id), user, flow: row.flow });
    return;
  }

  sendJson(response, 404, { error: `the stub serves the demo mint alone, not ${path}` });
});

server.listen(port, "127.0.0.1", () => {
  process.stdout.write(`front-door demo stub on 127.0.0.1:${port}\n`);
});
