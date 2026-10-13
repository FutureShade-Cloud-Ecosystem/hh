// The Quote Desk's cached reads: the three routes the desk reads its world
// through (the quotes list, the catalogue search, the customer search) answered
// from the device's copy in the exact shape the server answers, so the desk's
// own parsers take them unchanged. Run over the store's node:sqlite executor
// and the fake sync server.
import { test } from "node:test";
import assert from "node:assert/strict";
import { FakeSyncServer, nodeSqliteExecutor } from "@hardscapehouse/offline-store/testing";
import { createSqliteBackend } from "@hardscapehouse/offline-store";
import { WorkReader, openWorkStore } from "@hardscapehouse/work-offline";
import { quoteDeskRoutes, validUntilDate } from "../src/offline-reads.js";
import { readCatalogHits } from "../src/_panel-wire.js";
import { QdClient } from "../src/qd-client.js";
import { ApiClient } from "@hardscapehouse/api-client";
import { readThroughFetch, fixedSignal } from "@hardscapehouse/work-offline";

const BRANCH = "6f1c2b3a-0000-4000-8000-000000000001";
const MAPLE = "c0000000-0000-4000-9000-000000000001";
const FENWICK = "c0000000-0000-4000-9000-000000000002";
const P1 = "a0000000-0000-4000-9000-000000000001";
const P2 = "a0000000-0000-4000-9000-000000000002";

async function world() {
  const server = new FakeSyncServer();
  server.seed("customers", MAPLE, { id: MAPLE, account_no: "C01", name: "Maple Ridge Contracting", home_branch_id: BRANCH, customer_type: "CONTRACTOR", is_active: true });
  server.seed("customers", FENWICK, { id: FENWICK, account_no: "C02", name: "Fenwick Builders Ltd", home_branch_id: BRANCH, customer_type: "BUILDER", is_active: true });
  server.seed("products", P1, { id: P1, sku: "HPV-60", name: "Holland Stone 60 mm", description: "The Holland paver", manufacturer: "", collection: "", sold_by: "sf", base_uom: "sf", is_special_order: false, is_active: true, lead_time_days: null });
  server.seed("products", P2, { id: P2, sku: "PS-1", name: "Polymeric sand", description: "", manufacturer: "", collection: "", sold_by: "bag", base_uom: "bag", is_special_order: false, is_active: true, lead_time_days: null });
  server.seed("price_matrix", P1, { id: P1, list_price_cents: 400, price_uom: "sf", updated_at: "2000-01-01T00:00:00Z" });
  server.seed("quotes", "0d000000-0000-4000-9000-000000000001", { id: "0d000000-0000-4000-9000-000000000001", quote_no: "Q-1001", customer_id: MAPLE, status: "DRAFT", valid_until: "2999-01-01T00:00:00Z", subtotal_cents: 48000, tax_cents: 6240, total_cents: 54240, created_at: "2000-01-02T00:00:00Z" });
  server.seed("quotes", "0d000000-0000-4000-9000-000000000002", { id: "0d000000-0000-4000-9000-000000000002", quote_no: "Q-1002", customer_id: FENWICK, status: "SENT", valid_until: null, subtotal_cents: 1000, tax_cents: 130, total_cents: 1130, created_at: "2000-01-01T00:00:00Z" });
  const sql = nodeSqliteExecutor();
  const backend = createSqliteBackend(sql);
  const store = await openWorkStore({ appId: "quote-desk", backend });
  await store.pull(server.transport());
  const reader = new WorkReader(store, "quote-desk");
  const routes = quoteDeskRoutes(() => reader, () => BRANCH);
  return { server, store, reader, routes, backend };
}

const answer = async (routes, path) => {
  const url = new URL(path, "http://work.local");
  const route = routes.find((r) => r.match(url));
  assert.ok(route, `a route for ${path}`);
  return route.answer(url);
};

test("the quotes list is answered in the server's page shape, and the desk's own parser takes it", async () => {
  const w = await world();
  const fetchLike = readThroughFetch(async () => { throw new TypeError("Failed to fetch"); }, fixedSignal(false), w.routes);
  const desk = new QdClient(new ApiClient({ baseUrl: "", fetch: fetchLike }), () => "u");
  const page = await desk.listQuotes("");
  assert.equal(page.ok, true);
  assert.deepEqual(page.value.rows.map((r) => r.quoteNo).sort(), ["Q-1001", "Q-1002"]);
  const one = page.value.rows.find((r) => r.quoteNo === "Q-1001");
  assert.equal(one.customerName, "Maple Ridge Contracting");
  assert.equal(one.branchId, BRANCH);
  assert.equal(one.status, "DRAFT");
  assert.equal(one.validUntil, "2999-01-01");
  assert.equal(one.totalCents, 54240);
  assert.equal(page.value.nextCursor, null);
});

test("the list filters by status, and a later page is empty: the device's copy answers whole", async () => {
  const w = await world();
  const sent = await answer(w.routes, "/api/v1/quotes?status=SENT");
  assert.deepEqual(sent.body.items.map((i) => i.quote_no), ["Q-1002"]);
  const later = await answer(w.routes, "/api/v1/quotes?cursor=50");
  assert.deepEqual(later.body.items, []);
  assert.equal(later.body.next_cursor, null);
});

test("a draft waiting in the outbox is not a row of the list: the list never invents a total for it", async () => {
  const w = await world();
  const id = "0d000000-0000-4000-9000-0000000000aa";
  await w.backend.sql.run("INSERT INTO ent_quotes (id, seq, data, deleted) VALUES (?, 0, ?, 0)", [id, JSON.stringify({ id, quote_no: "", customer_id: MAPLE, status: "DRAFT", valid_until: null, subtotal_cents: null, tax_cents: null, total_cents: null, created_at: "2000-01-03T00:00:00Z" })]);
  const out = await answer(w.routes, "/api/v1/quotes");
  assert.ok(!out.body.items.some((i) => i.id === id));
  assert.equal(out.body.items.length, 2);
});

test("the list carries the words for how old the copy is", async () => {
  const w = await world();
  const out = await answer(w.routes, "/api/v1/quotes");
  assert.match(out.words, /^Pulled /);
});

test("the catalogue search is answered in the server's shape: the four facts a pick needs", async () => {
  const w = await world();
  const out = await answer(w.routes, "/api/v1/products/search?q=holland&limit=10");
  const hits = readCatalogHits(out.body);
  assert.deepEqual(hits, [{ product_id: P1, sku: "HPV-60", name: "Holland Stone 60 mm", base_uom: "sf" }]);
  assert.equal(readCatalogHits((await answer(w.routes, "/api/v1/products/search?q=zzz&limit=10")).body).length, 0);
});

test("the customer search is answered with the picker's fields, and what the cache cannot know is not a number", async () => {
  const w = await world();
  const out = await answer(w.routes, "/api/v1/customers/search?q=maple&limit=20&branch_id=" + BRANCH);
  assert.equal(out.body.length, 1);
  assert.equal(out.body[0].id, MAPLE);
  assert.equal(out.body[0].name, "Maple Ridge Contracting");
  assert.equal(out.body[0].account_no, "C01");
  assert.equal(out.body[0].customer_type, "CONTRACTOR");
  for (const unknown of ["payment_terms_days", "credit_limit_cents", "open_balance_cents"]) assert.equal(out.body[0][unknown], null, `${unknown} is not known offline`);
});

test("with no store the routes answer nothing, so the failure stands as it was", async () => {
  const routes = quoteDeskRoutes(() => null, () => BRANCH);
  for (const path of ["/api/v1/quotes", "/api/v1/products/search?q=a", "/api/v1/customers/search?q=a"]) {
    assert.equal(await answer(routes, path), null);
  }
});

test("only the desk's three read routes are routed", async () => {
  const w = await world();
  for (const path of ["/api/v1/quotes/abc", "/api/v1/quotes/policy", "/api/v1/products/scan", "/api/v1/pos/account-standing", "/api/v1/staff/approvals"]) {
    assert.ok(!w.routes.some((r) => r.match(new URL(path, "http://work.local"))), path);
  }
});

test("validUntilDate: the projection's timestamp becomes the date the desk shows", () => {
  assert.equal(validUntilDate("2999-01-01T00:00:00Z"), "2999-01-01");
  assert.equal(validUntilDate("2999-01-01"), "2999-01-01");
  assert.equal(validUntilDate(null), undefined);
  assert.equal(validUntilDate(""), undefined);
  assert.equal(validUntilDate("soon"), undefined);
});

test("a row that exists only on the device is not listed even if it carries figures: the waiting rule stands on its own", async () => {
  const w = await world();
  const id = "0d000000-0000-4000-9000-0000000000ab";
  await w.backend.sql.run("INSERT INTO ent_quotes (id, seq, data, deleted) VALUES (?, 0, ?, 0)", [id, JSON.stringify({ id, quote_no: "", customer_id: MAPLE, status: "DRAFT", valid_until: null, subtotal_cents: 100, tax_cents: 13, total_cents: 113, created_at: "2000-01-03T00:00:00Z" })]);
  const out = await answer(w.routes, "/api/v1/quotes");
  assert.ok(!out.body.items.some((i) => i.id === id));
});
