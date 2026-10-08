// HH Drive's local stub: the demo way in, and the driver's world answered in
// the core's shapes. It is a development and test double only; the real
// routes are the core's (core/internal/dispatch). Item 6's contract test holds
// every payload below to the contract's field list, field by field.
import { createServer } from "node:http";

const port = Number(process.env.DRIVE_STUB_PORT ?? 4949);

const FLOWS = [{ flow: "driver", role_code: "driver", label: "Driver" }];

const USERS = {
  driver: {
    user_id: "de110000-0000-4000-9000-000000000003",
    email: "driver@hh-demo.invalid",
    display_name: "Driver (demo)",
    role_code: "driver",
    role_name: "Driver",
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

function notFound(res) {
  res.writeHead(404, { "Content-Type": "application/json" });
  res.end(JSON.stringify({ error: "not found" }));
}

function readBody(req) {
  return new Promise((resolve) => {
    const chunks = [];
    req.on("data", (c) => chunks.push(c));
    req.on("end", () => resolve(Buffer.concat(chunks)));
  });
}

const state = { reset: () => {} };

async function handle(req, res) {
  const url = new URL(req.url, `http://127.0.0.1:${port}`);
  if (url.pathname === "/healthz") return json(res, { ok: true });
  if (req.method === "POST" && url.pathname === "/_stub/reset") {
    state.reset();
    return json(res, { ok: true });
  }
  if (url.pathname === "/api/v1/demo/flows") {
    return json(res, { flows: FLOWS });
  }
  if (req.method === "POST" && url.pathname === "/api/v1/demo/session") {
    const body = await readBody(req);
    try {
      const { flow } = JSON.parse(body.toString("utf8"));
      const account = USERS[flow];
      if (!account) return notFound(res);
      return json(res, { token: mintToken(account.user_id), user: account, flow });
    } catch {
      return json(res, { error: "bad request" }, 400);
    }
  }
  return notFound(res);
}

const server = createServer((req, res) => {
  handle(req, res).catch(() => json(res, { error: "internal error" }, 500));
});
server.listen(port, () => {
  console.log(`drive-stub listening on ${port}`);
});
