// Dealer Admin's local stub: the demo way in and one placeholder route for the
// dev server's /api proxy and the Playwright harness. This is a development
// and test double only; the real routes are the core's.
import { createServer } from "node:http";

const port = Number(process.env.DA_STUB_PORT ?? 56610);

const FLOWS = [
  { flow: "sales", role_code: "inside_sales", label: "Sales" },
  { flow: "sales-manager", role_code: "sales_manager", label: "Sales manager" },
];

const USERS = {
  sales: {
    user_id: "de110000-0000-4000-9000-000000000001",
    email: "sales@hh-demo.invalid",
    display_name: "Sales (demo)",
    role_code: "inside_sales",
    role_name: "Inside Sales",
    tier: 3,
    scope: "BRANCH",
  },
  "sales-manager": {
    user_id: "de110000-0000-4000-9000-000000000002",
    email: "sales.manager@hh-demo.invalid",
    display_name: "Sales manager (demo)",
    role_code: "sales_manager",
    role_name: "Sales Manager",
    tier: 2,
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

function notFound(res) {
  res.writeHead(404, { "Content-Type": "text/plain" });
  res.end("not found");
}

function handle(req, res) {
  const url = new URL(req.url, `http://127.0.0.1:${port}`);
  if (url.pathname === "/healthz") return json(res, { ok: true });
  if (url.pathname === "/api/v1/demo/flows") {
    return json(res, { flows: FLOWS });
  }
  if (req.method === "POST" && url.pathname === "/api/v1/demo/session") {
    let body = "";
    req.on("data", (c) => (body += c));
    req.on("end", () => {
      try {
        const { flow } = JSON.parse(body);
        const account = USERS[flow];
        if (!account) return notFound(res);
        json(res, { token: mintToken(account.user_id), user: account, flow });
      } catch { json(res, { error: "bad request" }, 400); }
    });
    return;
  }
  notFound(res);
}

const server = createServer(handle);
server.listen(port, () => {
  console.log(`dealer-admin stub listening on ${port}`);
});
