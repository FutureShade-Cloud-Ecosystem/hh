// The front door's local demo stub: the one demo route the app speaks, in the
// contract's shape, for the dev server's /api proxy and the acceptance
// harness. This is a development and test double only: the real route is the
// core's (the mint is the demo build's), and nothing in the app imports this
// file. The phrase gate in front of the real demo box is the image's Caddy;
// this stub checks no credential, like the core's own public demo routes.
//
//   POST /api/v1/demo/session -> DemoSessionResponse, or the armed refusal
//   GET  /healthz             -> 200, for the harness's readiness probe
//   GET  /_stub/peek          -> what the last mint request carried (the org
//                                header the app's client configuration sends)
//   POST /_stub/refuse        -> arm a refusal: { status, error, flows }
//   POST /_stub/reset         -> clear the armed refusal and the ledgers
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
    sendJson(response, 200, { lastOrg, mints, refusalArmed: refusal !== null });
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
    lastOrg = "";
    mints = 0;
    pendingApprovalCount = 3;
    sendJson(response, 200, { reset: true });
    return;
  }

  if (path === "/_stub/set-approvals" && request.method === "POST") {
    const body = await readBody(request);
    if (body === null || typeof body !== "object" || typeof body.count !== "number") {
      sendJson(response, 400, { error: "set approvals with { count: number }" });
      return;
    }
    pendingApprovalCount = Math.max(0, body.count);
    sendJson(response, 200, { count: pendingApprovalCount });
    return;
  }

  if (path === "/api/v1/staff/approvals" && request.method === "GET") {
    const items = Array.from({ length: pendingApprovalCount }, (_, i) => ({
      id: `approval-${i + 1}`,
      status: "PENDING",
      requested_by: "demo-account-sales-manager",
      branch_id: "demo-branch",
      policy_type: "MARGIN",
      detail: "Demo approval",
    }));
    sendJson(response, 200, {
      items,
      next_cursor: null,
      limit: 20,
    });
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
