// The front door's local demo stub: the one demo route the app speaks, in the
// contract's shape, for the dev server's /api proxy and the acceptance
// harness. This is a development and test double only: the real route is the
// core's (the mint is the demo build's), and nothing in the app imports this
// file. The phrase gate in front of the real demo box is the image's Caddy;
// this stub checks no credential, like the core's own public demo routes.
//
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
//   POST /_stub/reset         -> clear the armed refusals and the ledgers
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

// The closed flow table: the two logins this release shows, with the labels
// the cards carry and the role each minted account holds.
const FLOWS = [
  { flow: "sales", role_code: "inside_sales", label: "Sales" },
  { flow: "sales-manager", role_code: "sales_manager", label: "Sales manager" },
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
};

// The armed refusal: a test points the stub at a failure (the gate's 401, the
// mint's 429 limit, a 503 for an account the box cannot resolve) and the next
// mint for one of the named flows answers it, once per arming.
let refusal = null;
let lastOrg = "";
let mints = 0;
let pendingApprovalCount = 3;
let approvalsShape = "array";
let extraApprovals = 0;
let approvalsDelayMs = 0;

// The feedback bridge's own state: the armed board refusal (answered once)
// and the last submission the widget posted, its text fields read out of the
// multipart body and its images counted, so the acceptance spec asserts what
// the widget actually sent without ever reaching a real board.
let feedbackRefusal = null;
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

const server = createServer(async (request, response) => {
  const url = new URL(request.url ?? "/", `http://127.0.0.1:${port}`);
  const path = url.pathname;

  if (path === "/healthz") {
    sendJson(response, 200, { ok: true });
    return;
  }

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

  if (path === "/_stub/reset" && request.method === "POST") {
    refusal = null;
    feedbackRefusal = null;
    lastFeedback = null;
    lastOrg = "";
    mints = 0;
    pendingApprovalCount = 3;
    approvalsShape = "array";
    extraApprovals = 0;
    approvalsDelayMs = 0;
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
    sendJson(response, 201, { issue_id: "issue-stub-1", issue_number: "HH-42" });
    return;
  }

  if (path === "/_stub/set-approvals" && request.method === "POST") {
    const body = await readBody(request);
    if (body === null || typeof body !== "object" || typeof body.count !== "number") {
      sendJson(response, 400, { error: "set approvals with { count: number }" });
      return;
    }
    pendingApprovalCount = Math.max(0, body.count);
    // shape "array" is the bare array the core really sends; "page" is the contract's page object.
    approvalsShape = body.shape === "page" ? "page" : "array";
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
    if (approvalsShape === "page") {
      // Two pages, so the cursor is followed: the first holds one row.
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
    sendJson(response, 200, items);
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
