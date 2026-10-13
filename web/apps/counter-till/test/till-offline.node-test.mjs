// The Counter Till's offline layer: read cache only. The till has NO outbox
// (plan D5, manifest mode read_cache_only): it pulls the catalogue, the
// customers, the price band, the parked tickets and the invoices, and answers
// its reads from the copy when the network does not answer. It carries no
// keeper, queues nothing, and every write it makes goes through the no signal
// guard and is refused when the signal is gone. Run over the store's
// node:sqlite executor and the fake sync server.
import { test } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "@hardscapehouse/api-client";
import { createSqliteBackend, OfflineStore, scopeBookFor, SCOPE_POLICIES } from "@hardscapehouse/offline-store";
import { FakeSyncServer, nodeSqliteExecutor } from "@hardscapehouse/offline-store/testing";
import { fixedSignal, noSignalFetch, noSignalWebFetch, openWorkOfflineOver, readThroughFetch, readThroughWebFetch } from "@hardscapehouse/work-offline";
import { createTillDesk, dbNameFor, tillRoutes } from "../src/till-offline.js";

const BRANCH = "5d1b3f6e-0000-4000-9000-000000000001";
const P1 = "a0000000-0000-4000-9000-000000000001";
const C1 = "c0000000-0000-4000-9000-000000000001";

function world() {
  const server = new FakeSyncServer();
  server.seed("products", P1, { id: P1, sku: "HPV-60", name: "Holland Stone 60 mm", description: "", manufacturer: "", collection: "", sold_by: "sf", base_uom: "sf", is_special_order: false, is_active: true, lead_time_days: null });
  server.seed("price_matrix", P1, { id: P1, list_price_cents: 400, price_uom: "sf", updated_at: "2000-01-01T00:00:00Z" });
  server.seed("customers", C1, { id: C1, account_no: "C01", name: "Maple Ridge Contracting", home_branch_id: BRANCH, customer_type: "CONTRACTOR", is_active: true });
  server.seed("parked_tickets", "00000000-0000-4000-8000-000000000031", { id: "00000000-0000-4000-8000-000000000031", ticket_no: "PARK-1", branch_id: BRANCH, customer_id: C1, sale_type: "COLLECT_NOW", notes: "", status: "PARKED", lines: [{ product_id: P1, qty_entered: 40, uom_entered: "sf" }], created_at: "2000-01-01T00:00:00Z", updated_at: "2000-01-01T00:00:00Z" });
  return server;
}

async function rig() {
  const server = world();
  const signal = fixedSignal(true);
  const desk = createTillDesk({
    signal,
    transport: server.transport(),
    dbName: dbNameFor("u"),
    open: async (o) => openWorkOfflineOver(createSqliteBackend(nodeSqliteExecutor()), o),
  });
  await desk.open(BRANCH);
  await desk.cycle();
  return { server, signal, desk };
}

const ask = async (routes, path) => {
  const url = new URL(path, "http://work.local");
  const route = routes.find((r) => r.match(url));
  assert.ok(route, `a route for ${path}`);
  return route.answer(url);
};

test("the db name is the person's own and safe for OPFS", () => {
  assert.equal(dbNameFor("demo-account-sales"), "hh-counter-till-demo-account-sales");
  assert.match(dbNameFor("../x y"), /^hh-counter-till-[A-Za-z0-9_-]+$/);
});

test("the till's layer is a pull and nothing else: no push ever leaves, and nothing in the registry can be enqueued", async () => {
  const { server, desk } = await rig();
  assert.ok(server.requests.some((r) => r.kind === "pull"));
  assert.ok(!server.requests.some((r) => r.kind === "push"));
  for (const scope of Object.keys(SCOPE_POLICIES)) {
    const answer = await desk.store().enqueue({ scope, op: "create", payload: { id: "x" } });
    assert.equal(answer.ok, false, scope);
  }
  assert.equal(await desk.waitingCount(), 0);
});

test("the till's scope book is the manifest's read cache only block: no writable scope", () => {
  const book = scopeBookFor(["counter-till"]);
  assert.deepEqual(book.declared(), ["customers", "invoices", "parked_tickets", "price_matrix", "products", "warranty_claims"]);
  for (const scope of book.declared()) assert.notEqual(book.writeRefusal(scope), null, scope);
  assert.equal(OfflineStore.name, "OfflineStore");
});

test("offline, the till's reads are answered from the copy: catalogue with the price band, customers, parked tickets", async () => {
  const { desk } = await rig();
  const routes = tillRoutes(() => desk.reader());
  const hits = await ask(routes, "/api/v1/products/counter-search?q=holland&limit=20");
  assert.equal(hits.body.items[0].list_price_cents, 400);
  assert.equal((await ask(routes, "/api/v1/customers/search?q=maple")).body[0].id, C1);
  const parked = await ask(routes, "/api/v1/pos/parked?branch_id=" + BRANCH);
  assert.equal(parked.body.items.length, 1);
  assert.equal(parked.body.items[0].ticket_no, "PARK-1");
  assert.deepEqual(parked.body.items[0].lines, [{ product_id: P1, qty_entered: 40, uom_entered: "sf" }]);
});

test("composed: a read is served from the copy and every write is refused, in both fetch forms; nothing dials", async () => {
  const { desk, signal } = await rig();
  const routes = tillRoutes(() => desk.reader());
  const dialed = [];
  const base = async (url, init) => (dialed.push(`${init.method} ${url}`), { status: 200, ok: true, text: async () => "{}", arrayBuffer: async () => new ArrayBuffer(0) });
  signal.set(false);
  const client = new ApiClient({ baseUrl: "", fetch: noSignalFetch(readThroughFetch(base, signal, routes, desk.hooks), signal) });
  const read = await client.request({ method: "GET", path: "/api/v1/customers/search", query: { q: "maple" } });
  assert.equal(read.ok, true);
  assert.equal(desk.cachedWords().startsWith("Pulled"), true);
  for (const [method, path] of [["POST", "/api/v1/pos/sales"], ["POST", "/api/v1/pos/sales/s/refund"], ["POST", "/api/v1/pos/parked"], ["POST", "/api/v1/pos/parked/p/resume"], ["DELETE", "/api/v1/pos/parked/p"], ["POST", "/api/v1/payments"], ["PUT", "/api/v1/pricing/rules/r"]]) {
    const result = await client.request({ method, path, body: {} });
    assert.equal(result.ok, false, `${method} ${path}`);
    assert.match(result.error, /^No signal\./);
  }
  const web = noSignalWebFetch(readThroughWebFetch(async (input, init) => (dialed.push(`${init?.method ?? "GET"} ${input}`), new Response("{}")), signal, routes, desk.hooks), signal);
  assert.equal((await web("/api/v1/products/counter-search?q=holland&limit=20")).status, 200);
  const sale = await web("/api/v1/pos/sales", { method: "POST", body: "{}" });
  assert.equal(sale.ok, false);
  assert.match((await sale.json()).error, /^No signal\./);
  assert.deepEqual(dialed, [], "neither fetch form dialed");
});

test("the copy's age is shown after a cached read, and a live read clears it", async () => {
  const { desk, signal } = await rig();
  const routes = tillRoutes(() => desk.reader());
  const base = async () => ({ status: 200, ok: true, text: async () => "[]", arrayBuffer: async () => new ArrayBuffer(0) });
  const client = new ApiClient({ baseUrl: "", fetch: readThroughFetch(base, signal, routes, desk.hooks) });
  signal.set(false);
  await client.request({ method: "GET", path: "/api/v1/customers/search", query: { q: "maple" } });
  assert.match(desk.cachedWords(), /^Pulled /);
  signal.set(true);
  await client.request({ method: "GET", path: "/api/v1/customers/search", query: { q: "maple" } });
  assert.equal(desk.cachedWords(), "");
});

test("only the till's reads are routed: a write path is never answered from the copy", async () => {
  const { desk } = await rig();
  const routes = tillRoutes(() => desk.reader());
  for (const path of ["/api/v1/pos/sales", "/api/v1/pos/tender", "/api/v1/payments", "/api/v1/pos/counter-prices", "/api/v1/pos/account-standing", "/api/v1/products/scan"]) {
    assert.ok(!routes.some((r) => r.match(new URL(path, "http://work.local"))), path);
  }
});

test("the till never answers a customer from the copy: the copy holds no terms or credit, and unknown must not read as zero", async () => {
  const { desk } = await rig();
  const routes = tillRoutes(() => desk.reader());
  assert.ok(!routes.some((r) => r.match(new URL("/api/v1/customers/search?q=maple", "http://work.local"))));
});
