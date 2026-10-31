// The acceptance-real console double (W2-4 (a)): the four FB Console REST
// answers the box actually calls, nothing more. The default build's authz
// resolves team membership and the exchange's gates against the configured
// console endpoint per request (core/internal/platform/authz/appwrite.go,
// console.go); on the acceptance box that endpoint is this double, so the
// specs reach the real core's authz with no FB Console and no demo mint.
// The real exchange route still runs every gate: the minted staff token in
// the browser is the core's own. The double holds the acceptance tester
// alone, answers only the routes below, and trusts only the run's state
// file; every other path 404s. Nothing here is a credential of a real
// system. This stands in the platform directory's seat exactly the way
// W2-2's own test fakes FB Console.
import { createServer } from "node:http";
import { readFileSync } from "node:fs";
import { createHash, randomUUID } from "node:crypto";

const statePath = process.env.MDREAL_STATE === undefined ? "" : `${process.env.MDREAL_STATE}/state.json`;
const state = JSON.parse(readFileSync(statePath, "utf8"));
const port = Number(process.env.MDREAL_CONSOLE_PORT ?? "28082");

// The desk page at the box origin calls this double cross origin; the
// browser reads an answer only with an explicit origin echo (a wildcard
// never answers a credentialed read).
const allowOrigin = (req) => {
  const origin = req.headers.origin;
  return typeof origin === "string"
    ? {
        "Access-Control-Allow-Origin": origin,
        "Vary": "Origin",
        "Access-Control-Allow-Credentials": "true",
      }
    : {};
};

const json = (req, res, status, body) => {
  const text = JSON.stringify(body);
  res.writeHead(status, {
    "Content-Type": "application/json",
    "Content-Length": Buffer.byteLength(text),
    ...allowOrigin(req),
  });
  res.end(text);
};

// The one console session the acceptance sign in names: the email provider
// maps to the email password method, which the org's default allowed set
// carries (identity/signinmethods.go).
const session = () => ({
  $id: "acceptance-console-session",
  $createdAt: new Date().toISOString(),
  provider: "email",
});

// A well formed three segment console JWT; the exchange parses its shape
// (userId, sessionId and exp, the Appwrite payload shape the core reads)
// and asks the console (this double) whether it is live. Every mint carries
// a fresh jti: the exchange claims a console JWT single use, and two mints
// in one second must never collide on the claim.
const jwt = () => {
  const part = (value) => Buffer.from(JSON.stringify(value)).toString("base64url");
  const header = part({ alg: "HS256", typ: "JWT" });
  const body = part({
    userId: state.subject,
    sessionId: "acceptance-console-session",
    jti: randomUUID(),
    exp: Math.floor(Date.now() / 1000) + 900,
  });
  const sig = createHash("sha256").update(`${header}.${body}`).digest().toString("base64url");
  return `${header}.${body}.${sig}`;
};

const server = createServer((req, res) => {
  const url = new URL(req.url ?? "/", "http://127.0.0.1");
  const method = req.method ?? "GET";
  const path = url.pathname;

  // The adapter's reads carry custom headers, so the browser preflights;
  // answer the preflight and nothing else.
  if (method === "OPTIONS") {
    res.writeHead(204, {
      ...allowOrigin(req),
      "Access-Control-Allow-Methods": "GET, POST, PUT, DELETE, OPTIONS",
      "Access-Control-Allow-Headers": "Content-Type, X-Appwrite-Project, X-Appwrite-JWT, X-Appwrite-Key, Authorization",
    });
    res.end();
    return;
  }

  // GET /users/:id/memberships: the core's per request team membership read.
  const memberships = path.match(/^\/users\/([^/]+)\/memberships$/);
  if (method === "GET" && memberships !== null) {
    if (decodeURIComponent(memberships[1]) === state.subject) {
      json(req, res, 200, {
        total: 1,
        memberships: [{ $id: "acceptance-membership", teamId: `org-${state.org}`, userId: state.subject }],
      });
      return;
    }
    json(req, res, 200, { total: 0, memberships: [] });
    return;
  }

  // GET /users/:id/sessions: the exchange's gate 7b session read.
  const sessions = path.match(/^\/users\/([^/]+)\/sessions$/);
  if (method === "GET" && sessions !== null) {
    if (decodeURIComponent(sessions[1]) === state.subject) {
      json(req, res, 200, { total: 1, sessions: [session()] });
      return;
    }
    json(req, res, 200, { total: 0, sessions: [] });
    return;
  }

  // GET /account: the browser's session proof (resume) and the exchange's
  // AccountUnderJWT (X-Appwrite-JWT). The double holds one account.
  if (method === "GET" && path === "/account") {
    json(req, res, 200, {
      $id: state.subject,
      email: state.email,
      name: state.displayName,
      emailVerification: true,
      status: true,
    });
    return;
  }

  // POST /account/jwts: the browser's one exchange attempt, one fresh JWT.
  if (method === "POST" && path === "/account/jwts") {
    json(req, res, 200, { jwt: jwt() });
    return;
  }

  json(req, res, 404, { message: "not found", type: "general_route_not_found" });
});

// It runs inside the box's container, which box.sh publishes on 127.0.0.1 only, so it binds 0.0.0.0 there; the gate (web/scripts/stubs-bind-loopback.node-test.mjs) holds this one file as its single entry.
server.listen(port, "0.0.0.0", () => {
  process.stdout.write(`console-double: listening on 0.0.0.0:${port}\n`);
});
