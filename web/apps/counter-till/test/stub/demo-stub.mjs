// The local demo stub: the demo routes and the till's return paths in the
// contract's shapes, for the dev server's /api proxy and the Playwright
// harness. This is a development and test double only: the real routes are
// the core's (the mint is the demo build's, the returns rules are RT-B's),
// and nothing in the app imports this file. When the real routes land, the
// app and this suite keep working against them unchanged, because both speak
// only the contract's shapes:
//   GET  /api/v1/demo/flows            -> DemoFlowList
//   POST /api/v1/demo/session          -> DemoSessionResponse
//   GET  /api/v1/pos/returns/lookup    -> PosReturnLookup
//   POST /api/v1/pos/returns           -> PosReturn (409 PosRefundApprovalRequired)
//   GET  /api/v1/till/sessions/open    -> TillOpenLookup
//   GET  /api/v1/till/sessions/{id}/zreport -> TillZReport
//   GET  /api/v1/payments/config       -> PaymentConfigStatus
// The demo routes are public in the contract (security: []), so the stub
// checks no credential there; the phrase gate in front of the real demo box
// is Caddy's (W5's image), not the routes'. The staff routes the returns desk
// calls do carry the caller's bearer, and the stub reads the account back out
// of the token it minted itself, because the refusal rules it stands in for
// (the approval above the amount) are answered per role: what the real core
// resolves from the verified token, this stub reads from its own mint.
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

// The stub's till world: the demo branch's sales, the drawer, and the returns
// already taken. The sale numbers are the counter's own spellings, the money
// is integer cents straight from the (stubbed) core, and tax is zero in this
// world so every figure a test reads is the line's own total. A sale numbered
// outside this table (another branch's sale among them: the real lookup is
// branch fenced, and a fenced sale is an invisible one) answers not found.
const BRANCH_ID = "5d1b3f6e-0000-4000-9000-000000000001";

const SALES = {
  "CT-1001": {
    order_id: "demo-order-1001",
    order_no: "CT-1001",
    branch_id: BRANCH_ID,
    customer_id: "demo-customer-01",
    invoice_id: "demo-invoice-1001",
    sale_tender: "CASH",
    card_refundable: false,
    lines: [
      {
        order_line_id: "demo-line-1001-1",
        product_id: "demo-product-paver",
        description: "Holland paver, bevelled, each",
        qty_sold: 2,
        unit_price_cents: 6000,
        line_total_cents: 12000,
        tax_cents: 0,
      },
      {
        order_line_id: "demo-line-1001-2",
        product_id: "demo-product-wallblock",
        description: "Wall block, charcoal, each",
        qty_sold: 6,
        unit_price_cents: 6000,
        line_total_cents: 36000,
        tax_cents: 0,
      },
    ],
  },
  "CT-1002": {
    order_id: "demo-order-1002",
    order_no: "CT-1002",
    branch_id: BRANCH_ID,
    customer_id: "demo-customer-01",
    invoice_id: "demo-invoice-1002",
    sale_tender: "CASH",
    card_refundable: false,
    lines: [
      {
        order_line_id: "demo-line-1002-1",
        product_id: "demo-product-wallblock",
        description: "Wall block, charcoal, each",
        qty_sold: 6,
        unit_price_cents: 6000,
        line_total_cents: 36000,
        tax_cents: 0,
      },
    ],
  },
  "CT-2001": {
    order_id: "demo-order-2001",
    order_no: "CT-2001",
    branch_id: BRANCH_ID,
    customer_id: "demo-customer-02",
    invoice_id: "demo-invoice-2001",
    sale_tender: "CARD",
    card_refundable: true,
    lines: [
      {
        order_line_id: "demo-line-2001-1",
        product_id: "demo-product-edger",
        description: "Garden edger, grey, each",
        qty_sold: 1,
        unit_price_cents: 8900,
        line_total_cents: 8900,
        tax_cents: 0,
      },
    ],
  },
};

// The drawer's world: one open session at the demo branch, with its opening
// float and the cash that has moved through it. The Z report's expected cash
// is read off these counters, so a cash return moving the cash out counter is
// the same movement the Z report shows.
const DRAWER = {
  session_id: "till-session-trenton-1",
  branch_id: BRANCH_ID,
  opening_float_cents: 20000,
  cash_in_cents: 0,
  cash_out_cents: 0,
};

// The returns committed in this stub's life, keyed by the request's
// idempotency key (a replay answers the stored return again and moves the
// drawer nothing), plus the returned quantity netted per line for the
// lookup's still-returnable counts.
const RETURNS_BY_KEY = new Map();
const RETURNED_QTY = new Map();

// The approval above which a refund needs a manager, and the roles that hold
// the approval, as the demo dealer's configuration sets them (D49's 250.00
// and its two approver roles). The real values are RT-B's configuration read;
// this stub pins the demo's.
const APPROVAL_AMOUNT_CENTS = 25000;
const APPROVER_ROLES = ["sales_manager", "branch_manager"];

let RETURN_SEQ = 0;

// What the return's own rails refuse before any money moves, in the
// contract's envelope. The approval refusal names itself in the error string
// and carries the requirement under the approval key: the amount and the
// approver roles, present exactly on that refusal (the contract's own words),
// so the desk can name both.
function returnsRefusal(caller, body) {
  if (body === null || typeof body !== "object") {
    return { status: 400, body: { error: "the return's body is not readable" } };
  }
  const key = body.idempotency_key;
  if (typeof key !== "string" || key === "") {
    return { status: 400, body: { error: "the return carries no idempotency key" } };
  }
  const sale = saleFromInput(body);
  if (sale === undefined) {
    return { status: 404, body: { error: "no sale with that number answers at this counter" } };
  }
  if (!Array.isArray(body.lines) || body.lines.length === 0) {
    return { status: 400, body: { error: "the return carries no lines" } };
  }
  if (!["CASH", "CARD", "ACCOUNT_CREDIT"].includes(body.refund_method)) {
    return { status: 400, body: { error: "the refund method is not one of the rails" } };
  }
  for (const line of body.lines) {
    const saleLine = sale.lines.find((row) => row.order_line_id === line.order_line_id);
    if (saleLine === undefined) {
      return { status: 400, body: { error: "the return names a line the sale does not carry" } };
    }
    const returnable = saleLine.qty_sold - (RETURNED_QTY.get(saleLine.order_line_id) ?? 0);
    if (!Number.isInteger(line.qty_base) || line.qty_base <= 0 || line.qty_base > returnable) {
      return { status: 409, body: { error: "the return asks for more than the line still has to return" } };
    }
  }
  if (body.refund_method === "CARD" && !sale.card_refundable) {
    return { status: 409, body: { error: "the sale was not paid by card, so there is no capture to reverse" } };
  }
  if (body.refund_method === "CASH" && body.till_session_id !== DRAWER.session_id) {
    return { status: 409, body: { error: "the drawer named is not the open one" } };
  }
  const totalCents = returnTotal(sale, body.lines);
  if (totalCents > APPROVAL_AMOUNT_CENTS && !APPROVER_ROLES.includes(caller.account.role_code)) {
    return {
      status: 409,
      body: {
        error: "refund_approval_required",
        approval: { amount_cents: APPROVAL_AMOUNT_CENTS, approvers: APPROVER_ROLES },
      },
    };
  }
  return null;
}

// The refund's total: the returned lines' own totals by their quantities, in
// a world whose tax is zero. Stub-side arithmetic, standing in the core's
// shoes; the app computes nothing.
function returnTotal(sale, lines) {
  let total = 0;
  for (const line of lines) {
    const saleLine = sale.lines.find((row) => row.order_line_id === line.order_line_id);
    total += saleLine.unit_price_cents * line.qty_base;
  }
  return total;
}

function saleFromInput(body) {
  if (typeof body.order_no === "string" && SALES[body.order_no] !== undefined) return SALES[body.order_no];
  if (typeof body.order_id === "string") {
    return Object.values(SALES).find((row) => row.order_id === body.order_id);
  }
  return undefined;
}

// The commit, as the core's would be: the refund's total is the returned
// lines' own totals (this world carries no tax), the cash rail moves the
// drawer's cash out counter, and the idempotency key makes a replay answer
// the stored return without a second movement.
function commitReturn(caller, body) {
  const key = body.idempotency_key;
  const replay = RETURNS_BY_KEY.get(key);
  if (replay !== undefined) return { ...replay, replayed: true };
  const sale = saleFromInput(body);
  RETURN_SEQ += 1;
  const subtotalCents = returnTotal(sale, body.lines);
  for (const line of body.lines) {
    const saleLine = sale.lines.find((row) => row.order_line_id === line.order_line_id);
    RETURNED_QTY.set(saleLine.order_line_id, (RETURNED_QTY.get(saleLine.order_line_id) ?? 0) + line.qty_base);
  }
  const answer = {
    return_id: `demo-return-${RETURN_SEQ}`,
    return_no: `CT-R-${100 + RETURN_SEQ}`,
    order_id: sale.order_id,
    invoice_id: sale.invoice_id,
    refund_method: body.refund_method,
    subtotal_cents: subtotalCents,
    tax_cents: 0,
    total_cents: subtotalCents,
    pickup_status: "NONE",
    status: "COMMITTED",
    replayed: false,
  };
  RETURNS_BY_KEY.set(key, answer);
  if (body.refund_method === "CASH") DRAWER.cash_out_cents += answer.total_cents;
  return answer;
}

function respondJson(response, status, payload) {
  response.writeHead(status, { "content-type": "application/json" });
  response.end(JSON.stringify(payload));
}

function readBody(request) {
  return new Promise((resolve, reject) => {
    let body = "";
    request.on("data", (chunk) => {
      body += chunk;
    });
    request.on("end", () => resolve(body));
    request.on("error", reject);
  });
}

// The staff caller, read back out of a bearer this stub itself minted: the
// payload's sub names the account. Null when there is no usable bearer, and
// the staff routes answer 401 like the real gate does.
function callerFromRequest(request) {
  const header = request.headers["authorization"] ?? "";
  const match = /^Bearer (.+)$/.exec(header);
  if (match === null) return null;
  const parts = match[1].split(".");
  if (parts.length !== 3) return null;
  try {
    const payload = JSON.parse(Buffer.from(parts[1], "base64url").toString("utf8"));
    const sub = payload === null || typeof payload !== "object" ? undefined : payload.sub;
    if (typeof sub !== "string") return null;
    for (const flow of Object.keys(USERS)) {
      if (USERS[flow].user_id === sub) return { flow, account: USERS[flow] };
    }
    return null;
  } catch {
    return null;
  }
}

const server = createServer(async (request, response) => {
  const url = new URL(request.url ?? "/", `http://127.0.0.1:${port}`);
  if (request.method === "GET" && url.pathname === "/healthz") {
    respondJson(response, 200, { ok: true });
    return;
  }
  if (request.method === "GET" && url.pathname === "/api/v1/demo/flows") {
    respondJson(response, 200, { flows: FLOWS });
    return;
  }
  if (request.method === "POST" && url.pathname === "/api/v1/demo/session") {
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
        respondJson(response, 400, { error: "the request body is not JSON" });
        return;
      }
      if (typeof flow !== "string" || !Object.hasOwn(USERS, flow)) {
        respondJson(response, 400, { error: "unknown flow" });
        return;
      }
      respondJson(response, 200, { token: mintToken(flow), user: USERS[flow], flow });
    });
    return;
  }
  if (request.method === "GET" && url.pathname === "/api/v1/pos/returns/lookup") {
    const caller = callerFromRequest(request);
    if (caller === null) {
      respondJson(response, 401, { error: "no usable staff credential" });
      return;
    }
    const orderNo = url.searchParams.get("order_no");
    const orderId = url.searchParams.get("order_id");
    const sale =
      (typeof orderNo === "string" && orderNo !== "" && SALES[orderNo]) ||
      (typeof orderId === "string" && orderId !== "" && Object.values(SALES).find((row) => row.order_id === orderId)) ||
      undefined;
    if (sale === undefined) {
      // A sale this caller's branch does not reach (another branch's sale
      // among them: the real lookup is branch fenced) is an invisible one.
      respondJson(response, 404, { error: "no sale with that number answers at this counter" });
      return;
    }
      respondJson(response, 200, {
        order_id: sale.order_id,
        order_no: sale.order_no,
        branch_id: sale.branch_id,
        customer_id: sale.customer_id,
        invoice_id: sale.invoice_id,
        sale_tender: sale.sale_tender,
        card_refundable: sale.card_refundable,
        lines: sale.lines.map((line) => ({
          ...line,
          qty_returned: RETURNED_QTY.get(line.order_line_id) ?? 0,
        })),
      });
      return;
    }
    if (request.method === "GET" && url.pathname === "/api/v1/till/sessions/open") {
      const caller = callerFromRequest(request);
      if (caller === null) {
        respondJson(response, 401, { error: "no usable staff credential" });
        return;
      }
      const branchId = url.searchParams.get("branch_id");
      const open = branchId === DRAWER.branch_id;
      respondJson(response, 200, open ? { open: true, id: DRAWER.session_id } : { open: false });
      return;
    }
    if (
      request.method === "GET" &&
      url.pathname.startsWith("/api/v1/till/sessions/") &&
      url.pathname.endsWith("/zreport")
    ) {
      const caller = callerFromRequest(request);
      if (caller === null) {
        respondJson(response, 401, { error: "no usable staff credential" });
        return;
      }
      const sessionId = url.pathname.split("/")[5];
      if (sessionId !== DRAWER.session_id) {
        respondJson(response, 404, { error: "no such till session" });
        return;
      }
      respondJson(response, 200, {
        session_id: DRAWER.session_id,
        per_tender: [{ method: "CASH", total_cents: DRAWER.cash_in_cents - DRAWER.cash_out_cents }],
        cash: {
          opening_float_cents: DRAWER.opening_float_cents,
          expected_cents: DRAWER.opening_float_cents + DRAWER.cash_in_cents - DRAWER.cash_out_cents,
          counted_cents: 0,
          over_short_cents: 0,
        },
        closing_float_cents: 0,
      });
      return;
    }
    if (request.method === "GET" && url.pathname === "/api/v1/payments/config") {
      const caller = callerFromRequest(request);
      if (caller === null) {
        respondJson(response, 401, { error: "no usable staff credential" });
        return;
      }
      // The demo's card rail is the simulator, always (guard G7): the status
      // route says so, and the desk's card method repeats it in words.
      respondJson(response, 200, {
        configured: true,
        merchant_id: "hh-demo-merchant",
        processor: "stub",
        currency: "USD",
        simulated: true,
      });
      return;
    }
    if (request.method === "POST" && url.pathname === "/api/v1/pos/returns") {
      const caller = callerFromRequest(request);
      if (caller === null) {
        respondJson(response, 401, { error: "no usable staff credential" });
        return;
      }
      let body;
      try {
        body = JSON.parse(await readBody(request));
      } catch {
        respondJson(response, 400, { error: "the request body is not JSON" });
        return;
      }
      const refusal = returnsRefusal(caller, body);
      if (refusal !== null) {
        respondJson(response, refusal.status, refusal.body);
        return;
      }
      respondJson(response, 201, commitReturn(caller, body));
      return;
    }
    respondJson(response, 404, { error: "not found" });
  });

server.keepAliveTimeout = 120 * 1000;
server.headersTimeout = 125 * 1000;
server.listen(port, "127.0.0.1", () => {
  console.log(`demo stub on http://127.0.0.1:${port}`);
});
