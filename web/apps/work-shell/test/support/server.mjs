// The acceptance legs' server: the composed dist from disk plus the demo mint
// the front door speaks (POST /api/v1/demo/session, GET /api/v1/demo/flows),
// in the contract's shape with a JWT shaped token. It binds port 0 and the
// caller awaits close() (RULES-S4 addendum 7); it is a test double only.
import { createServer } from "node:http";
import { readFile } from "node:fs/promises";
import { extname, join, normalize } from "node:path";

const MIME = new Map([
  [".html", "text/html; charset=utf-8"],
  [".js", "text/javascript; charset=utf-8"],
  [".css", "text/css; charset=utf-8"],
  [".svg", "image/svg+xml"],
  [".png", "image/png"],
  [".ico", "image/x-icon"],
  [".woff2", "font/woff2"],
  [".json", "application/json"],
]);

const USERS = {
  sales: ["demo-account-sales", "inside_sales", "Inside Sales", 3, "BRANCH"],
  "sales-manager": ["demo-account-sales-manager", "sales_manager", "Sales Manager", 2, "BRANCH"],
  "yard-manager": ["demo-account-yard-manager", "yard_manager", "Yard manager", 2, "BRANCH"],
  "yard-team": ["demo-account-yard-team", "yard_team", "Yard Team", 3, "BRANCH"],
  "logistics-manager": ["demo-account-logistics-manager", "logistics_manager", "Logistics Manager", 2, "BRANCH"],
  "financial-controller": ["demo-account-financial-controller", "financial_controller", "Financial Controller", 1, "GLOBAL"],
  driver: ["demo-account-driver", "driver", "Driver", 3, "BRANCH"],
};

function token(sub) {
  const exp = Math.floor(Date.now() / 1000) + 3600;
  const part = (value) => Buffer.from(JSON.stringify(value)).toString("base64url");
  return `${part({ alg: "HS256", typ: "JWT" })}.${part({ sub, exp })}.stub-signature`;
}

/**
 * The stub FB Console the host's Tauri commands reach in the sign out legs
 * (the page's injected invoke forwards each command here). It models the one
 * server side session the Work shell can revoke: live after /__console/open,
 * gone after auth_sign_out, and counted. The core's token has no server side
 * row to model: it is a stateless signed token (core/internal/identity/sessionsigner.go),
 * so there is nothing for a stub core to record beyond this.
 */
function consoleStub() {
  const state = { live: false, signOuts: 0, opened: 0, calls: [] };
  const user = USERS["sales-manager"];
  const [userId, roleCode, roleName, tier, scope] = user;
  return {
    state,
    handle(command) {
      if (command.startsWith("auth_")) state.calls.push(`${Date.now() % 100000} ${command}`);
      if (command === "open") {
        // each leg starts from its own count: the server lives for the whole worker
        state.live = true;
        state.opened += 1;
        state.signOuts = 0;
        state.calls = [];
        return [200, { live: true }];
      }
      if (command === "state") return [200, { ...state }];
      if (command === "auth_resume") {
        return state.live ? [200, { user: { user_id: userId, email: "sales-manager@demo.invalid", name: `${roleName} (demo)` } }] : [401, { code: "no_session" }];
      }
      if (command === "auth_exchange") {
        if (!state.live) return [401, { code: "no_session" }];
        return [
          200,
          {
            token: token(userId),
            expiresAtMs: Date.now() + 3600_000,
            user: { user_id: userId, email: "sales-manager@demo.invalid", display_name: `${roleName} (demo)`, role_code: roleCode, role_name: roleName, tier, scope },
          },
        ];
      }
      if (command === "auth_sign_out") {
        state.live = false;
        state.signOuts += 1;
        return [200, null];
      }
      return [400, { code: "unavailable" }];
    },
  };
}

function readBody(request) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    request.on("data", (chunk) => chunks.push(chunk));
    request.on("end", () => resolve(Buffer.concat(chunks).toString("utf8")));
    request.on("error", reject);
  });
}

function json(response, status, body) {
  response.writeHead(status, { "content-type": "application/json" });
  response.end(JSON.stringify(body));
}

/**
 * Serve `dist` on a free loopback port. Resolves { url, close }.
 * With `holdApi`, every /api/ call other than the demo mint is held: no answer
 * is ever sent, like a core that has not replied yet. A real desk app that is
 * hosted with no core behind it ends its own walk on an answer that fails
 * (the Counter Till signs out and goes to the front door when its branch
 * cannot resolve), so the legs that load the real bundles hold the request
 * instead of failing it.
 */
export async function startServer(dist, { holdApi = false } = {}) {
  const consoleSide = consoleStub();
  const server = createServer(async (request, response) => {
    const path = (request.url ?? "/").split("?")[0];
    if (path.startsWith("/__console/")) {
      const [status, body] = consoleSide.handle(path.slice("/__console/".length));
      json(response, status, body);
      return;
    }
    if (path === "/api/v1/demo/flows" && request.method === "GET") {
      json(response, 200, { flows: Object.keys(USERS).map((flow) => ({ flow, role_code: USERS[flow][1], label: flow, class: "staff" })) });
      return;
    }
    if (path === "/api/v1/demo/session" && request.method === "POST") {
      let flow = "";
      try {
        flow = JSON.parse(await readBody(request)).flow;
      } catch {
        // an unreadable body is a refusal below
      }
      const row = Object.hasOwn(USERS, flow) ? USERS[flow] : undefined;
      if (row === undefined) {
        json(response, 400, { error: "unknown flow" });
        return;
      }
      const [userId, roleCode, roleName, tier, scope] = row;
      json(response, 200, {
        token: token(userId),
        user: { user_id: userId, email: `${flow}@demo.invalid`, display_name: `${roleName} (demo)`, role_code: roleCode, role_name: roleName, tier, scope },
      });
      return;
    }
    // The image writes {"mode":"demo"} at /config.json; the front door reads it before it draws a role card.
    if (path === "/config.json") {
      json(response, 200, { mode: "demo" });
      return;
    }
    if (path.startsWith("/api/")) {
      if (!holdApi) json(response, 404, { error: "not found" });
      return;
    }
    const relative = normalize(path).replace(/^([/\\])+/, "");
    let file = join(dist, relative);
    if (!file.startsWith(dist)) {
      response.writeHead(403).end();
      return;
    }
    if (path.endsWith("/") || relative === "") file = join(file, "index.html");
    readFile(file)
      .then((body) => {
        response.writeHead(200, { "content-type": MIME.get(extname(file)) ?? "application/octet-stream" });
        response.end(body);
      })
      .catch(() => {
        response.writeHead(404).end();
      });
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const { port } = server.address();
  return {
    url: `http://127.0.0.1:${port}`,
    close: () =>
      new Promise((resolve) => {
        server.close(() => resolve());
        server.closeAllConnections();
      }),
  };
}
