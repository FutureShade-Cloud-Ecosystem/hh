// The local demo stub: the two demo routes in the contract's shapes, for the
// dev server's /api proxy and the Playwright harness. This is a development
// and test double only: the real routes are the core's demo module (the mint
// is the demo build's), and nothing in the app imports this file. When the
// real flows route lands, the app and this suite keep working against it
// unchanged, because both speak only the contract's shapes:
//   GET  /api/v1/demo/flows   -> DemoFlowList
//   POST /api/v1/demo/session -> DemoSessionResponse
// Both routes are public in the contract (security: []), so the stub checks
// no credential; the phrase gate in front of the real demo box is Caddy's
// (W5's image), not the routes'.
//
// The mint answers a JWT-shaped token so the app's expiry read (the token's
// exp claim) sees the same shape the real mint issues: three segments, a
// payload with sub and exp, and a signature segment. The signature here is
// stub bytes; on the real box it is the demo issuer's, verified by the core's
// verifier, which this stub stands in for.
import { createServer } from "node:http";

const port = Number(process.env.CT_STUB_PORT ?? 4821);

// The closed flow table: the two logins this release shows, with the labels
// the cards carry and the role each minted account holds (the mint's role
// label comes from this table, never from the roles table).
const FLOWS = [
  { flow: "sales", role_code: "inside_sales", label: "Sales" },
  { flow: "sales-manager", role_code: "sales_manager", label: "Sales manager" },
];

// The minted accounts, one per flow, shaped field for field like the staff
// login user block (core/api/openapi.yaml, IdentityUser). The accounts are
// demo labels on reserved domains, not people.
const USERS = {
  sales: {
    user_id: "demo-account-sales",
    email: "sales@demo.invalid",
    display_name: "Sales (demo)",
    role_code: "inside_sales",
    role_name: "Inside Sales",
    tier: 2,
    scope: "BRANCH",
  },
  "sales-manager": {
    user_id: "demo-account-sales-manager",
    email: "sales-manager@demo.invalid",
    display_name: "Sales manager (demo)",
    role_code: "sales_manager",
    role_name: "Sales Manager",
    tier: 3,
    scope: "BRANCH",
  },
};

function base64UrlJson(value) {
  return Buffer.from(JSON.stringify(value), "utf8").toString("base64url");
}

function mintToken(flow) {
  const issuedAtSeconds = Math.floor(Date.now() / 1000);
  const header = { alg: "ES256", typ: "JWT" };
  const payload = {
    iss: "hh-demo-stub",
    sub: USERS[flow].user_id,
    aud: "staff",
    iat: issuedAtSeconds,
    exp: issuedAtSeconds + 60 * 60,
  };
  return `${base64UrlJson(header)}.${base64UrlJson(payload)}.c3R1Yi1zaWduYXR1cmUtYnl0ZXM`;
}

function respondJson(response, status, payload, extraHeaders = {}) {
  response.writeHead(status, { "content-type": "application/json", ...extraHeaders });
  response.end(JSON.stringify(payload));
}

// The test suite asserts the app sends the demo org on its demo calls, so the
// stub echoes the org it received back as a response header: a wrong or
// missing X-Org on the app side fails the suite right here, which is where a
// slip in the client configuration would surface on the real box too (the
// core's membership source answers for hh-demo and nothing else).
function sawOrg(request) {
  return { "x-stub-saw-org": request.headers["x-org"] ?? "" };
}

const server = createServer((request, response) => {
  const url = new URL(request.url ?? "/", `http://127.0.0.1:${port}`);
  if (request.method === "GET" && url.pathname === "/healthz") {
    respondJson(response, 200, { ok: true });
    return;
  }
  if (request.method === "GET" && url.pathname === "/api/v1/demo/flows") {
    respondJson(response, 200, { flows: FLOWS }, sawOrg(request));
    return;
  }
  if (request.method === "POST" && url.pathname === "/api/v1/demo/session") {
    const org = sawOrg(request);
    let body = "";
    request.on("data", (chunk) => {
      body += chunk;
    });
    request.on("end", () => {
      let flow;
      try {
        const parsed = JSON.parse(body);
        flow = parsed === null || typeof parsed !== "object" ? undefined : parsed.flow;
      } catch {
        respondJson(response, 400, { error: "the request body is not JSON" }, org);
        return;
      }
      if (typeof flow !== "string" || !Object.hasOwn(USERS, flow)) {
        respondJson(response, 400, { error: "unknown flow" }, org);
        return;
      }
      respondJson(response, 200, { token: mintToken(flow), user: USERS[flow], flow }, org);
    });
    return;
  }
  respondJson(response, 404, { error: "not found" });
});

server.on("error", (error) => {
  console.error(`demo stub could not listen on 127.0.0.1:${port}: ${error instanceof Error ? error.message : String(error)}`);
  process.exit(1);
});

server.keepAliveTimeout = 120 * 1000;
server.headersTimeout = 125 * 1000;
server.listen(port, "127.0.0.1", () => {
  console.log(`demo stub on http://127.0.0.1:${port}`);
});
