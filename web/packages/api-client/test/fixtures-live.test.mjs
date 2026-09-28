// The Stage-1a contract, re-proven against the captures CAPTURED OFF THE LIVE
// WIRE, ported from HH Pro's recorded-fixture suite
// (/home/colton/Desktop/hh_pro_dibbits/src/core/supplier/__tests__/
// erp-staging-recorded.test.ts, a read-only reference) to this package's
// generated client.
//
// Every capture in core/api/fixtures-live/ is a verbatim live response,
// vendored under D22 with exactly one edit: bearer tokens are replaced with a
// redaction marker, because a credential has no business in a repo. Each
// capture is replayed through the operation the contract assigns to its route
// with a recorded FetchLike (no network, ever), asserting the request the
// client builds (method, filled path, serialized query) and that the response
// parses and structurally validates against the document's schema. Where a
// capture's route were not in the document, the mapping below fails loudly; a
// capture that does not validate is a finding, never a schema to loosen.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import * as api from "../src/index.ts";
import { SCHEMA_DESCRIPTORS } from "../src/gen/descriptors.ts";
import { need, recorder, unwrap } from "./_recorded.mjs";
import { validateValue } from "./_validate.mjs";

const BASE_URL = "https://hh-core.example";
// test/ -> api-client -> packages -> web -> the repository root.
const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..", "..", "..", "..");
const fixturesDir = join(repoRoot, "core", "api", "fixtures-live");

const capture = (file) => JSON.parse(readFileSync(join(fixturesDir, file), "utf8"));

// Every vendored capture, the contract operation it belongs to (route and
// request exactly as the test issues them), the schema the contract assigns
// to the route's success response, and the wire pins the capture earns.
const CAPTURES = [
  {
    file: "login.json",
    schema: "TenantLoginResponse",
    route: "POST /api/v1/tenant/login",
    method: "POST",
    path: "/api/v1/tenant/login",
    query: "",
    call: (client) => api.tenantAuth.tenantLogin(client, { email: "portal.demo@dibbits.example", password: "pw" }),
    pins: (v) => {
      assert.equal(v.user.name, "Dibbits Demo (Owner)");
      assert.equal(v.user.role, "account_admin");
      // The capture's one edit, visible on the wire: the token is the
      // redaction marker, never a credential.
      assert.equal(v.token, "live-capture-token-redacted");
      assert.equal(v.config.dealer_name, "Dibbits Landscape Supply");
    },
  },
  {
    file: "refresh.json",
    schema: "TenantRefreshResponse",
    route: "POST /api/v1/tenant/token/refresh",
    method: "POST",
    path: "/api/v1/tenant/token/refresh",
    query: "",
    // The document declares no request body for the refresh; the client sends none.
    call: (client) => api.tenantAuth.tenantRefreshToken(client),
    pins: (v) => {
      assert.equal(v.user.status, "active");
      assert.equal(v.token, "live-capture-token-redacted");
    },
  },
  {
    file: "me.json",
    schema: "TenantMe",
    route: "GET /api/v1/tenant/me",
    method: "GET",
    path: "/api/v1/tenant/me",
    query: "",
    call: (client) => api.tenantAuth.tenantGetMe(client),
    pins: (v) => {
      assert.equal(v.name, "Dibbits Demo (Owner)");
      assert.equal(v.role, "account_admin");
      assert.equal(v.customer_id, "df000000-0000-4000-9000-000000000001");
    },
  },
  {
    file: "config.json",
    schema: "TenantPublicConfig",
    route: "GET /api/v1/tenant/config",
    method: "GET",
    path: "/api/v1/tenant/config",
    query: "",
    call: (client) => api.tenantAuth.tenantGetConfig(client),
    pins: (v) => {
      assert.equal(v.dealer_name, "Dibbits Landscape Supply");
      assert.equal(v.primary_color, "#E8A74E");
    },
  },
  {
    file: "dashboard.json",
    schema: "TenantDashboard",
    route: "GET /api/v1/tenant/dashboard",
    method: "GET",
    path: "/api/v1/tenant/dashboard",
    query: "",
    call: (client) => api.tenantDashboard.tenantGetDashboard(client),
    pins: (v) => {
      assert.equal(v.account.account_no, "CUST-DEMO-CP03");
      assert.equal(v.account.customer_type, "CONTRACTOR");
    },
  },
  {
    file: "billing-summary.json",
    schema: "TenantBillingSummary",
    route: "GET /api/v1/tenant/billing/summary",
    method: "GET",
    path: "/api/v1/tenant/billing/summary",
    query: "",
    call: (client) => api.tenantDashboard.tenantGetBillingSummary(client),
    pins: (v) => {
      // Money is integer cents on the wire, computed by the core.
      assert.equal(v.open_balance_cents, 70000);
      assert.equal(v.credit_limit_cents, 5000000);
      assert.equal(v.available_credit_cents, 4930000);
      assert.equal(v.on_hold, false);
    },
  },
  {
    file: "catalog-search-paver.json",
    schema: "TenantCatalogSearchResponse",
    route: "GET /api/v1/tenant/catalog/search",
    method: "GET",
    path: "/api/v1/tenant/catalog/search",
    query: "q=paver",
    call: (client) => api.tenantCatalog.tenantSearchCatalog(client, { q: "paver" }),
    pins: (v) => {
      // The live wire answers the ERP's own UOM vocabulary; the contract
      // serves it verbatim.
      const oaks = need(v.items.find((hit) => hit.sku === "DBS-OAKS-VINTAGE"), "the Oaks Vintage hit");
      assert.equal(oaks.base_uom, "PC");
      const sealer = need(v.items.find((hit) => hit.sku === "DBS-SIKA-SEALER"), "the sealer hit");
      assert.equal(sealer.base_uom, "EA");
    },
  },
  {
    file: "catalog-search-sand.json",
    schema: "TenantCatalogSearchResponse",
    route: "GET /api/v1/tenant/catalog/search",
    method: "GET",
    path: "/api/v1/tenant/catalog/search",
    query: "q=sand",
    call: (client) => api.tenantCatalog.tenantSearchCatalog(client, { q: "sand" }),
    pins: (v) => {
      const uoms = new Map(v.items.map((hit) => [hit.sku, hit.base_uom]));
      assert.equal(uoms.get("DBS-AGG-SAND"), "T");
      assert.equal(uoms.get("DBS-GATOR-SAND"), "BAG");
      assert.equal(uoms.get("DBS-BANAS-COPING"), "PC");
    },
  },
  {
    file: "orders-page.json",
    schema: "TenantOrderPage",
    route: "GET /api/v1/tenant/orders",
    method: "GET",
    path: "/api/v1/tenant/orders",
    query: "limit=3",
    call: (client) => api.tenantOrders.tenantListOrders(client, { limit: 3 }),
    pins: (v) => {
      const so = need(v.items.find((order) => order.order_no === "#SO-TRT-43"), "the TRT order");
      assert.equal(so.status, "INVOICED");
      assert.equal(so.fulfillment, "PICKUP");
      assert.equal(so.total_cents, 101700);
      // The capture carries the legacy offset fields the document marks
      // deprecated; they ride the type and stay data.
      assert.equal(typeof v.total, "number");
    },
  },
  {
    file: "order-detail.json",
    schema: "TenantOrderDetail",
    route: "GET /api/v1/tenant/orders/86b744fc-c0bf-4b07-a3c7-91b860d428f4",
    method: "GET",
    path: "/api/v1/tenant/orders/86b744fc-c0bf-4b07-a3c7-91b860d428f4",
    query: "",
    call: (client) => api.tenantOrders.tenantGetOrder(client, "86b744fc-c0bf-4b07-a3c7-91b860d428f4"),
    pins: (v) => {
      assert.equal(v.order_no, "#SO-TRT-43");
      assert.equal(v.status, "INVOICED");
    },
  },
  {
    file: "invoices-page.json",
    schema: "TenantInvoicePage",
    route: "GET /api/v1/tenant/invoices",
    method: "GET",
    path: "/api/v1/tenant/invoices",
    query: "limit=3&status=PAID",
    call: (client) => api.tenantBilling.tenantListInvoices(client, { limit: 3, status: "PAID" }),
    pins: (v) => {
      const inv = need(v.items.find((invoice) => invoice.invoice_no === "#INV-30"), "invoice 30");
      assert.equal(inv.status, "PAID");
    },
  },
  {
    file: "invoice-detail.json",
    schema: "TenantInvoiceDetail",
    route: "GET /api/v1/tenant/invoices/0dd4ec29-9616-4754-9f5f-667da1722018",
    method: "GET",
    path: "/api/v1/tenant/invoices/0dd4ec29-9616-4754-9f5f-667da1722018",
    query: "",
    call: (client) => api.tenantBilling.tenantGetInvoice(client, "0dd4ec29-9616-4754-9f5f-667da1722018"),
    pins: (v) => {
      assert.equal(v.invoice_no, "#INV-30");
      assert.equal(v.order_id, "ae58d658-427f-40a1-b10e-e7603cb2a7ba");
      assert.equal(v.status, "PAID");
    },
  },
  {
    file: "quotes-page.json",
    schema: "TenantQuotePage",
    route: "GET /api/v1/tenant/quotes",
    method: "GET",
    path: "/api/v1/tenant/quotes",
    query: "limit=3",
    call: (client) => api.tenantQuotes.tenantListQuotes(client, { limit: 3 }),
    pins: (v) => {
      const statuses = new Map(v.items.map((quote) => [quote.quote_no, quote.status]));
      assert.equal(statuses.get("#Q-4"), "DRAFT");
      assert.equal(statuses.get("Q-DEMO-CP03"), "ACCEPTED");
    },
  },
  {
    file: "quote-detail.json",
    schema: "TenantQuoteDetail",
    route: "GET /api/v1/tenant/quotes/2cbb6e96-c88d-4a05-a2c9-d2709a15a718",
    method: "GET",
    path: "/api/v1/tenant/quotes/2cbb6e96-c88d-4a05-a2c9-d2709a15a718",
    query: "",
    call: (client) => api.tenantQuotes.tenantGetQuote(client, "2cbb6e96-c88d-4a05-a2c9-d2709a15a718"),
    pins: (v) => {
      assert.equal(v.quote_no, "#Q-4");
      assert.equal(v.status, "DRAFT");
      assert.equal(v.lines.length, 3);
    },
  },
];

test("every vendored capture is mapped to a contract operation", (t) => {
  const onDisk = readdirSync(fixturesDir)
    .filter((f) => f.endsWith(".json"))
    .sort();
  // The happy-path captures replay above; the two error captures replay as
  // the refusal-path proof below. Anything on disk outside this mapping is a
  // stop, not a skip.
  const mapped = [...CAPTURES.map((c) => c.file), "error-401.json", "error-404.json"].sort();
  assert.deepEqual(onDisk, mapped, "a capture without a mapping is a stop, not a skip");
  for (const c of CAPTURES) {
    assert.notEqual(SCHEMA_DESCRIPTORS[c.schema], undefined, `${c.schema} is a schema of the document`);
  }
});

for (const c of CAPTURES) {
  test(`live capture ${c.file} replays through ${c.route}`, async (t) => {
    const body = capture(c.file);
    const rec = recorder({ [c.route]: { body } });
    const client = new api.ApiClient({ baseUrl: BASE_URL, fetch: rec.fetch });

    // The call resolves (never throws), the request is the documented one,
    // and the response parses and structurally validates against the schema
    // the contract assigns to the route.
    const result = await c.call(client);
    const value = unwrap(result);
    assert.equal(rec.calls.length, 1, "the replay dialed exactly one route");
    const call = rec.calls[0];
    assert.equal(call.method, c.method);
    assert.equal(call.route, c.path);
    assert.equal(call.url, `${BASE_URL}${c.path}${c.query === "" ? "" : `?${c.query}`}`);
    assert.deepEqual(validateValue(value, SCHEMA_DESCRIPTORS[c.schema], "capture"), [], `validates against ${c.schema}`);

    c.pins(value, call);
  });
}

// The two error captures: the refusal carries the Error envelope's message
// verbatim, exactly what the server said.
test("the recorded 401 body surfaces verbatim on a refused read", async (t) => {
  const body = capture("error-401.json");
  const rec = recorder({ "GET /api/v1/tenant/me": { status: 401, body } });
  const client = new api.ApiClient({ baseUrl: BASE_URL, fetch: rec.fetch });
  assert.deepEqual(validateValue(body, SCHEMA_DESCRIPTORS.Error, "capture"), [], "validates against Error");
  const result = await api.tenantAuth.tenantGetMe(client);
  assert.equal(result.ok, false);
  if (!result.ok) assert.equal(result.error, "unauthorized");
});

test("the recorded 404 body surfaces verbatim on a missing order", async (t) => {
  const body = capture("error-404.json");
  const rec = recorder({
    "GET /api/v1/tenant/orders/00000000-0000-4000-9000-00000000dead": { status: 404, body },
  });
  const client = new api.ApiClient({ baseUrl: BASE_URL, fetch: rec.fetch });
  assert.deepEqual(validateValue(body, SCHEMA_DESCRIPTORS.Error, "capture"), [], "validates against Error");
  const result = await api.tenantOrders.tenantGetOrder(client, "00000000-0000-4000-9000-00000000dead");
  assert.equal(result.ok, false);
  if (!result.ok) assert.equal(result.error, "not found");
});

// Transport discipline, as the HH Pro suite asserts it: every replayed read
// went out as a GET, the only writes are the two auth POSTs, and no tenant
// route carried an org header (the tenant scope never takes X-Org).
test("the replays dialed only their documented routes, with no surprises on the wire", async (t) => {
  // Two captures share the catalog route; the table queues them in replay
  // order, exactly as the recorded harness consumes a queue.
  const routes = {};
  for (const c of CAPTURES) (routes[c.route] ??= []).push({ body: capture(c.file) });
  const rec = recorder(routes);
  const client = new api.ApiClient({ baseUrl: BASE_URL, fetch: rec.fetch });
  for (const c of CAPTURES) unwrap(await c.call(client));

  assert.equal(rec.calls.length, CAPTURES.length);
  const writes = rec.writes();
  assert.deepEqual(
    writes.map((w) => w.route).sort(),
    ["/api/v1/tenant/login", "/api/v1/tenant/token/refresh"],
    "only the two auth POSTs are writes",
  );
  for (const call of rec.calls) {
    assert.ok(call.url.startsWith(`${BASE_URL}/api/v1/`), `url under the base: ${call.url}`);
    assert.equal(call.headers["X-Org"], undefined, "tenant routes never take X-Org");
  }
  const login = need(rec.calls.find((c) => c.route === "/api/v1/tenant/login"), "the login call");
  assert.equal(login.headers["Content-Type"], "application/json");
  assert.deepEqual(JSON.parse(login.body), { email: "portal.demo@dibbits.example", password: "pw" });
  const refresh = need(rec.calls.find((c) => c.route === "/api/v1/tenant/token/refresh"), "the refresh call");
  assert.equal(refresh.body, undefined, "the refresh sends no body");
});
