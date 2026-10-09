// Yard Pick's local stub: the demo way in, and the yard team's world
// answered in the core's shapes. It is a development and test double only;
// the real routes are the core's (core/internal/tasks, yard_handler.go).
// The contract test holds every payload below to the contract's field list,
// field by field, and every refusal to the core's own sentences.
import { createServer } from "node:http";

const port = Number(process.env.YARD_STUB_PORT ?? 4959);

// The flow table as the core's stands after the roles personas PR (274):
// the till's pair plus the Yard Team and the Driver. The app offers only its
// own card.
const FLOWS = [
  { flow: "sales", role_code: "inside_sales", label: "Sales" },
  { flow: "sales-manager", role_code: "sales_manager", label: "Sales manager" },
  { flow: "yard-team", role_code: "yard_team", label: "Yard Team" },
  { flow: "driver", role_code: "driver", label: "Driver" },
];

const USERS = {
  "yard-team": {
    user_id: "d0200000-0000-4000-9000-000000000002",
    email: "yard.demo@dibbits.example",
    display_name: "Yard persona (demo)",
    role_code: "yard_team",
    role_name: "Yard Team",
    tier: 3,
    scope: "BRANCH",
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

// The core's error envelope: {"error": sentence}, the module prefix already
// stripped (httpx.CounterMessage).
function refuse(res, status, message) {
  return json(res, { error: message }, status);
}

function readBody(req) {
  return new Promise((resolve) => {
    const chunks = [];
    req.on("data", (c) => chunks.push(c));
    req.on("end", () => resolve(Buffer.concat(chunks)));
  });
}

// ---- the world ---------------------------------------------------------
//
// One branch the yard team reaches (Trenton, the populate's own id).

const TRENTON = "5d1b3f6e-0000-4000-9000-000000000001";

const BRANCH = {
  id: TRENTON,
  code: "TRT",
  name: "Hardscape House (demo), Trenton",
  address: { line1: "857 Bayly Rd", city: "Trenton", province: "ON", postal: "K8V 1N1", country: "CA" },
  is_default: true,
  is_system_default: false,
  is_active: true,
};

let scenario;

function reset() {
  scenario = "";
}
reset();

// ---- the routes ---------------------------------------------------------

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

  // ---- the demo way in ----
  if (path === "/api/v1/demo/flows") return json(res, { flows: FLOWS });
  if (req.method === "POST" && path === "/api/v1/demo/session") {
    const body = await readBody(req);
    try {
      const { flow } = JSON.parse(body.toString("utf8"));
      const account = USERS[flow];
      if (!account) return refuse(res, 404, "not found");
      return json(res, { token: mintToken(account.user_id), user: account, flow });
    } catch {
      return refuse(res, 400, "bad request");
    }
  }

  // ---- the routes the yard uses ----
  const yardRoute = path.startsWith("/api/v1/yard/") || path === "/api/v1/branches" || (path.startsWith("/api/v1/tasks/") && req.method === "POST");
  if (!yardRoute) return refuse(res, 404, "task not found");
  const authorization = req.headers.authorization ?? "";
  if (!authorization.startsWith("Bearer ")) return refuse(res, 401, "unauthenticated");

  // The branches this login may reach. A login off the yard answers none.
  if (req.method === "GET" && path === "/api/v1/branches") {
    return json(res, scenario === "not-yard" ? [] : [BRANCH]);
  }

  if (req.method === "GET" && path === "/api/v1/yard/pick-queue") {
    // The branch fence (yard_handler.go): a branch-scoped caller must name a
    // reachable branch, and a branch out of reach is refused.
    const branchId = url.searchParams.get("branch_id") ?? "";
    if (branchId === "") return refuse(res, 403, "a branch-scoped caller must name a reachable branch_id");
    if (branchId !== TRENTON) return refuse(res, 403, "branch out of scope");
    return json(res, { items: [], next_cursor: "", limit: 50 });
  }

  return refuse(res, 404, "task not found");
}

const server = createServer((req, res) => {
  handle(req, res).catch(() => refuse(res, 500, "internal error"));
});
server.listen(port, () => {
  console.log(`yard-stub listening on ${port}`);
});
