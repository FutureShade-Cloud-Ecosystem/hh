// The cached routes the desk apps share: the catalogue (search and list), the
// customer search and the parked tickets, answered from the device's copy in
// the shapes the contract's routes answer, so the ported surface's parsers take
// them unchanged. What a copy cannot know (stock, terms, credit) is left out or
// null, never invented.
import { test } from "node:test";
import assert from "node:assert/strict";
import { WorkReader, openWorkStore } from "../src/read-cache.ts";
import { customerRoutes, parkedRoute, productRoutes } from "../src/desk-routes.ts";
import { BRANCH, CUSTOMER, PRODUCT, rig } from "./_rig.mjs";

async function opened(appId) {
  const r = await rig(appId);
  const store = await openWorkStore({ appId, backend: r.backend, now: r.now, newKey: r.newKey });
  await store.pull(r.transport);
  return { ...r, reader: new WorkReader(store, appId) };
}

const ask = async (routes, path) => {
  const url = new URL(path, "http://work.local");
  const route = routes.find((candidate) => candidate.match(url));
  assert.ok(route, `a route for ${path}`);
  return route.answer(url);
};

test("counter-search answers the hits the ported grid reads, with the list price from the band and no stock figure", async () => {
  const w = await opened("quote-desk");
  const routes = productRoutes(() => w.reader);
  const out = await ask(routes, "/api/v1/products/counter-search?q=paver&limit=20&branch_id=" + BRANCH);
  assert.equal(out.body.items.length, 1);
  const hit = out.body.items[0];
  assert.equal(hit.product_id, PRODUCT);
  assert.equal(hit.sku, "PAVER-CH-1212");
  assert.equal(hit.base_uom, "EA");
  assert.equal(hit.has_image, false);
  assert.equal(hit.match_reason, "NAME");
  assert.equal(hit.list_price_cents, 449);
  assert.equal("on_hand_branch" in hit, false, "stock is not known offline and is not invented");
  assert.match(out.words, /^Pulled /);
});

test("a hit with no price in the band has no list price, never a zero", async () => {
  const w = await opened("quote-desk");
  const out = await ask(productRoutes(() => w.reader), "/api/v1/products/counter-search?q=SAND-POLY&limit=20");
  assert.equal(out.body.items.length, 1);
  assert.equal("list_price_cents" in out.body.items[0], false);
});

test("a term shorter than two characters answers nothing, as the server does", async () => {
  const w = await opened("quote-desk");
  const out = await ask(productRoutes(() => w.reader), "/api/v1/products/counter-search?q=p&limit=20");
  assert.deepEqual(out.body.items, []);
});

test("the active product list is answered as the POS list route answers it", async () => {
  const w = await opened("counter-till");
  const out = await ask(productRoutes(() => w.reader), "/api/v1/products?active=true");
  assert.deepEqual(out.body.map((p) => p.sku).sort(), ["PAVER-CH-1212", "SAND-POLY-G"], "the retired product is not listed");
  const paver = out.body.find((p) => p.id === PRODUCT);
  assert.equal(paver.is_active, true);
  assert.equal(paver.list_price_cents, 449);
  assert.equal(paver.list_price_uom, "EA");
});

test("customer search is answered with the picker's fields and no figure the copy does not hold", async () => {
  const w = await opened("counter-till");
  const out = await ask(customerRoutes(() => w.reader), "/api/v1/customers/search?q=northline&limit=20");
  assert.equal(out.body.length, 1);
  assert.equal(out.body[0].id, CUSTOMER);
  assert.equal(out.body[0].payment_terms_days, null);
  assert.equal(out.body[0].credit_limit_cents, null);
  assert.equal(out.body[0].open_balance_cents, null);
});

test("parked tickets are answered in the server's page shape for the named branch", async () => {
  const r = await rig("counter-till");
  r.server.seed("parked_tickets", "00000000-0000-4000-8000-000000000099", { id: "00000000-0000-4000-8000-000000000099", ticket_no: "P-9", branch_id: "other", customer_id: "", sale_type: "COUNTER", notes: "", status: "PARKED", lines: [], created_at: "2000-01-01T00:00:00Z", updated_at: "2000-01-01T00:00:00Z" });
  const store = await openWorkStore({ appId: "counter-till", backend: r.backend, now: r.now, newKey: r.newKey });
  await store.pull(r.transport);
  const reader = new WorkReader(store, "counter-till");
  const out = await ask([parkedRoute(() => reader)], "/api/v1/pos/parked?branch_id=" + BRANCH);
  assert.equal(out.body.items.length, 1);
  assert.deepEqual(out.body.items[0], {
    id: out.body.items[0].id,
    ticket_no: "P-7",
    branch_id: BRANCH,
    customer_id: CUSTOMER,
    sale_type: "COUNTER",
    notes: "",
    status: "PARKED",
    lines: [{ product_id: PRODUCT, qty: "4" }],
    created_at: "2000-01-01T00:00:00Z",
  });
});

test("only reads are routed: a park or a resume is never answered from the copy", async () => {
  const w = await opened("counter-till");
  const routes = [...productRoutes(() => w.reader), ...customerRoutes(() => w.reader), parkedRoute(() => w.reader)];
  for (const path of ["/api/v1/pos/parked/abc/resume", "/api/v1/pos/sales", "/api/v1/pos/counter-prices", "/api/v1/pos/account-standing", "/api/v1/products/scan"]) {
    assert.ok(!routes.some((r) => r.match(new URL(path, "http://work.local"))), path);
  }
});

test("with no store every route answers nothing", async () => {
  for (const route of [...productRoutes(() => null), ...customerRoutes(() => null), parkedRoute(() => null)]) {
    const sample = route.match(new URL("/api/v1/products/search", "http://x")) ? "/api/v1/products/search?q=aa" : route.match(new URL("/api/v1/products/counter-search", "http://x")) ? "/api/v1/products/counter-search?q=aa" : route.match(new URL("/api/v1/products", "http://x")) ? "/api/v1/products?active=true" : route.match(new URL("/api/v1/customers/search", "http://x")) ? "/api/v1/customers/search?q=aa" : "/api/v1/pos/parked";
    assert.equal(await route.answer(new URL(sample, "http://x")), null);
  }
});
