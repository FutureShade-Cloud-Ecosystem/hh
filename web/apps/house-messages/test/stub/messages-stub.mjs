// House Messages local stub: the demo way in, for the app's first leg. It grows into the stateful
// messaging double in the next sub item. A development and test double only.
import { createServer } from "node:http";

const port = Number(process.env.MSG_STUB_PORT ?? 4989);

const FLOWS = [
  { flow: "sales", role_code: "inside_sales", label: "Sales" },
  { flow: "yard-manager", role_code: "yard_manager", label: "Yard manager" },
];

const USER = {
  user_id: "a0000000-0000-4000-9000-000000000001",
  email: "sales.demo@hh-demo.invalid",
  display_name: "Sales persona (demo)",
  role_code: "inside_sales",
  role_name: "Inside Sales",
  tier: 3,
  scope: "BRANCH",
};

function mintToken(sub) {
  const part = (value) => Buffer.from(JSON.stringify(value), "utf8").toString("base64url");
  const issuedAtSeconds = Math.floor(Date.now() / 1000);
  const payload = { iss: "hh-demo-stub", sub, aud: "staff", iat: issuedAtSeconds, exp: issuedAtSeconds + 3600 };
  return `${part({ alg: "ES256", typ: "JWT" })}.${part(payload)}.c3R1Yi1zaWduYXR1cmUtYnl0ZXM`;
}

function json(res, body, status = 200) {
  res.writeHead(status, { "Content-Type": "application/json" });
  res.end(JSON.stringify(body));
}

const server = createServer((req, res) => {
  const url = new URL(req.url, `http://127.0.0.1:${port}`);
  if (url.pathname === "/healthz") return json(res, { ok: true });
  if (url.pathname === "/api/v1/demo/flows") return json(res, { flows: FLOWS });
  if (req.method === "POST" && url.pathname === "/api/v1/demo/session") {
    return json(res, { token: mintToken(USER.user_id), user: USER, flow: "sales" });
  }
  return json(res, { error: "not found" }, 404);
});
server.listen(port, () => {
  console.log(`messages-stub listening on ${port}`);
});
