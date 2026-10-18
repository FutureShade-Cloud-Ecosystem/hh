// The read cache of the desk apps: the catalogue, customers, the price band,
// parked tickets and open documents answer from the offline store with their
// staleness shown, and the apps that carry no outbox carry none at all.
import { test } from "node:test";
import assert from "node:assert/strict";
import { OfflineStore, SCOPE_POLICIES } from "@hardscapehouse/offline-store";
import { WorkModeError, WorkReader, openWorkStore, stalenessWords } from "../src/read-cache.ts";
import { BRANCH, CUSTOMER, PRODUCT, rig, uuid } from "./_rig.mjs";

async function opened(appId = "quote-desk", options) {
  const r = await rig(appId, options);
  const store = await openWorkStore({ appId, backend: r.backend, now: r.now, newKey: r.newKey });
  return { ...r, store, reader: new WorkReader(store, appId) };
}

test("a desk app with no cache mode cannot open a store: Money Desk and Dealer Admin are online only", async () => {
  for (const appId of ["money-desk", "dealer-admin"]) {
    const r = await rig(appId);
    await assert.rejects(() => openWorkStore({ appId, backend: r.backend }), (error) => error instanceof WorkModeError && /online only/.test(error.message));
  }
});

test("an app that is not in the manifest cannot open a store", async () => {
  const r = await rig("quote-desk");
  await assert.rejects(() => openWorkStore({ appId: "no-such-app", backend: r.backend }), /no frontend no-such-app/);
});

test("the till has NO outbox: every scope of the registry is refused at enqueue", async () => {
  const { store } = await opened("counter-till");
  for (const scope of Object.keys(SCOPE_POLICIES)) {
    const answer = await store.enqueue({ scope, op: "create", payload: { id: uuid(9) } });
    assert.equal(answer.ok, false, scope);
  }
  assert.equal((await store.outbox.health()).waiting, 0);
});

test("the till's money scopes are refused as money, the rest as not writable", async () => {
  const { store } = await opened("counter-till");
  const money = await store.enqueue({ scope: "invoices", op: "update", payload: { id: uuid(9) } });
  assert.equal(money.ok, false);
  assert.equal(money.reason, "money_scope");
  const other = await store.enqueue({ scope: "parked_tickets", op: "update", payload: { id: uuid(9) } });
  assert.equal(other.reason, "scope_not_writable");
});

test("Quote Desk's only writable scope is quotes, and a price change never queues", async () => {
  const { store } = await opened("quote-desk");
  const price = await store.enqueue({ scope: "price_matrix", op: "update", payload: { id: PRODUCT, list_price_cents: 1 } });
  assert.equal(price.ok, false);
  assert.equal(price.reason, "money_scope");
  const approval = await store.enqueue({ scope: "approvals", op: "update", payload: { id: uuid(9) } });
  assert.equal(approval.ok, false, "an approval is not even a scope of this app");
  const customers = await store.enqueue({ scope: "customers", op: "update", payload: { id: CUSTOMER } });
  assert.equal(customers.reason, "scope_not_writable");
  const quotes = await store.enqueue({ scope: "quotes", op: "create", payload: { id: uuid(9) } });
  assert.equal(quotes.ok, true);
});

test("before any pull the reads are empty and say they were never pulled", async () => {
  const { reader } = await opened("quote-desk");
  const answer = await reader.catalogue("paver");
  assert.deepEqual(answer.hits, []);
  assert.equal(answer.staleness.neverPulled, true);
  assert.equal(answer.words, "Never pulled");
});

test("after a pull the catalogue answers from the store, active products only, with the price band joined", async () => {
  const { reader, store, transport } = await opened("quote-desk");
  const pulled = await store.pull(transport);
  assert.equal(pulled.kind, "done");
  const answer = await reader.catalogue("paver");
  assert.deepEqual(answer.hits.map((h) => h.sku), ["PAVER-CH-1212"], "the retired paver is not offered");
  assert.equal(answer.hits[0].listPriceCents, 449);
  assert.equal(answer.hits[0].priceUom, "EA");
  assert.equal(answer.hits[0].baseUom, "EA");
  assert.equal(answer.staleness.neverPulled, false);
  assert.match(answer.words, /^Pulled /);
});

test("a product with no price row has no price, never a zero", async () => {
  const { reader, store, transport } = await opened("quote-desk");
  await store.pull(transport);
  const sand = (await reader.catalogue("SAND-POLY-G")).hits[0];
  assert.equal(sand.sku, "SAND-POLY-G");
  assert.equal(sand.listPriceCents, null);
  assert.equal(sand.priceUom, null);
});

test("the catalogue search matches the sku, the name and the description, any case, and caps the answer", async () => {
  const { reader, store, transport } = await opened("quote-desk");
  await store.pull(transport);
  assert.equal((await reader.catalogue("poly")).hits.length, 1);
  assert.equal((await reader.catalogue("18 KG")).hits.length, 1);
  assert.equal((await reader.catalogue("demo stone")).hits.length, 2, "the manufacturer is searched too");
  assert.equal((await reader.catalogue("", 1)).hits.length, 1);
  assert.deepEqual((await reader.catalogue("nothing like this")).hits, []);
});

test("customers answer from the store, active accounts only, in name order", async () => {
  const { reader, store, transport } = await opened("quote-desk");
  await store.pull(transport);
  const all = await reader.customers("");
  assert.deepEqual(all.hits.map((c) => c.name), ["Harbor Pavers (demo)", "Northline Hardscapes Ltd (demo)"]);
  assert.equal((await reader.customers("a-1001")).hits[0].id, CUSTOMER);
  assert.equal((await reader.customers("closed")).hits.length, 0);
});

test("open documents are the quotes the store holds, with the customer named from the cache", async () => {
  const { reader, store, transport } = await opened("quote-desk");
  await store.pull(transport);
  const docs = await reader.openDocuments();
  assert.equal(docs.rows.length, 1);
  assert.equal(docs.rows[0].quoteNo, "#Q-100");
  assert.equal(docs.rows[0].customerName, "Northline Hardscapes Ltd (demo)");
  assert.equal(docs.rows[0].status, "SENT");
  assert.equal(docs.rows[0].totalCents, 10800);
});

test("the till reads the price band, the parked tickets and the catalogue, and holds no quotes", async () => {
  const { reader, store, transport } = await opened("counter-till");
  await store.pull(transport);
  const parked = await reader.parkedTickets();
  assert.equal(parked.rows.length, 1);
  assert.equal(parked.rows[0].ticketNo, "P-7");
  assert.equal(parked.rows[0].lineCount, 1);
  assert.equal((await reader.catalogue("paver")).hits[0].listPriceCents, 449);
  await assert.rejects(() => reader.openDocuments().then(() => reader.scope("quotes")), /not a scope of this profile/);
});

test("offline, the pull reports offline and the reads still answer from the last pull", async () => {
  const { reader, store, transport, server } = await opened("quote-desk");
  await store.pull(transport);
  server.setOffline(true);
  const out = await store.pull(transport);
  assert.equal(out.kind, "offline");
  assert.equal((await reader.catalogue("paver")).hits.length, 1);
});

test("a branch filter keeps another branch's parked tickets out", async () => {
  const { reader, store, transport, server } = await opened("counter-till");
  server.seed("parked_tickets", uuid(32), { id: uuid(32), ticket_no: "P-8", branch_id: uuid(99), customer_id: CUSTOMER, sale_type: "COUNTER", notes: "", status: "PARKED", lines: [], created_at: "2000-01-01T00:00:00Z", updated_at: "2000-01-01T00:00:00Z" });
  await store.pull(transport);
  assert.deepEqual((await reader.parkedTickets(BRANCH)).rows.map((t) => t.ticketNo), ["P-7"]);
  assert.equal((await reader.parkedTickets()).rows.length, 2);
});

test("staleness words: never, just now, minutes, hours", () => {
  assert.equal(stalenessWords({ neverPulled: true, ageMs: null }), "Never pulled");
  assert.equal(stalenessWords({ neverPulled: false, ageMs: null }), "Never pulled");
  assert.equal(stalenessWords({ neverPulled: false, ageMs: 10_000 }), "Pulled just now");
  assert.equal(stalenessWords({ neverPulled: false, ageMs: 5 * 60_000 }), "Pulled 5 min ago");
  assert.equal(stalenessWords({ neverPulled: false, ageMs: 3 * 3_600_000 }), "Pulled 3 h ago");
  assert.equal(stalenessWords({ neverPulled: false, ageMs: 50 * 3_600_000 }), "Pulled over a day ago");
});

test("the store type is the offline store's own", async () => {
  const { store } = await opened("quote-desk");
  assert.ok(store instanceof OfflineStore);
});
