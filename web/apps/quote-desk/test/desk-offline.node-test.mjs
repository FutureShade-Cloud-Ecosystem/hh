// The Quote Desk's offline controller over the store's node:sqlite executor
// and the fake sync server behind the core's quotes rules: it opens the layer
// for the signed in person, keeps the draft the client hands it, reports what
// waits and what the server answered, and gives the desk its cached reads.
import { test } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "@hardscapehouse/api-client";
import { createSqliteBackend } from "@hardscapehouse/offline-store";
import { FakeSyncServer, nodeSqliteExecutor } from "@hardscapehouse/offline-store/testing";
import { fixedSignal, noSignalFetch, openWorkOfflineOver } from "@hardscapehouse/work-offline";
import { strictQuotesTransport } from "@hardscapehouse/work-offline/testing";
import { DeskOffline, dbNameFor } from "../src/desk-offline.js";
import { KEPT_DRAFT_WORDS, QdClient } from "../src/qd-client.js";

const BRANCH = "6f1c2b3a-0000-4000-8000-000000000001";
const MAPLE = "c0000000-0000-4000-9000-000000000001";

async function rig() {
  const server = new FakeSyncServer();
  server.seed("customers", MAPLE, { id: MAPLE, account_no: "C01", name: "Maple Ridge Contracting", home_branch_id: BRANCH, customer_type: "CONTRACTOR", is_active: true });
  server.seed("products", "a0000000-0000-4000-9000-000000000001", { id: "a0000000-0000-4000-9000-000000000001", sku: "HPV-60", name: "Charcoal Stone 60 mm", description: "", manufacturer: "", collection: "", sold_by: "sf", base_uom: "sf", is_special_order: false, is_active: true, lead_time_days: null });
  server.seed("price_matrix", "a0000000-0000-4000-9000-000000000001", { id: "a0000000-0000-4000-9000-000000000001", list_price_cents: 400, price_uom: "sf", updated_at: "2000-01-01T00:00:00Z" });
  const strict = strictQuotesTransport(server);
  const signal = fixedSignal(true);
  const qd = new QdClient(new ApiClient({ baseUrl: "", fetch: noSignalFetch(async () => ({ status: 200, ok: true, text: async () => "{}", arrayBuffer: async () => new ArrayBuffer(0) }), signal) }), () => "u");
  let ids = 0;
  const events = [];
  const desk = new DeskOffline({
    signal,
    qd,
    transport: strict.transport,
    dbName: "test-db",
    open: async (options) => {
      const sql = nodeSqliteExecutor();
      return openWorkOfflineOver(createSqliteBackend(sql), options);
    },
    newId: () => `0d000000-0000-4000-9000-${String(++ids).padStart(12, "0")}`,
  });
  desk.subscribe(() => events.push("changed"));
  return { server, strict, signal, qd, desk, events };
}

test("the db name is the person's own and safe for OPFS: no second person replays the first's outbox", () => {
  assert.equal(dbNameFor("demo-account-sales"), "hh-quote-desk-demo-account-sales");
  assert.notEqual(dbNameFor("a"), dbNameFor("b"));
  assert.match(dbNameFor("../../etc/x y"), /^hh-quote-desk-[A-Za-z0-9_-]+$/);
});

test("boot pulls the world; the desk then reads the catalogue and customers from the device", async () => {
  const r = await rig();
  await r.desk.boot(BRANCH);
  assert.equal(r.desk.available, true);
  const found = await r.desk.reader().catalogue("charcoal");
  assert.equal(found.hits.length, 1);
  assert.equal(found.hits[0].listPriceCents, 400);
  assert.equal((await r.desk.reader().customers("maple")).hits.length, 1);
});

test("a host that cannot carry a store leaves the desk online as it was: no reader, no keeper, nothing said", async () => {
  const r = await rig();
  const bare = new DeskOffline({ signal: r.signal, qd: r.qd, transport: r.strict.transport, dbName: "x", open: async () => ({ layer: null, readOnly: false }) });
  await bare.boot(BRANCH);
  assert.equal(bare.available, false);
  assert.equal(bare.reader(), null);
  assert.equal(bare.bannerMode(), "");
  r.signal.set(false);
  const result = await r.qd.createQuote(MAPLE, BRANCH, "");
  assert.match(result.error, /^No signal\./);
  assert.notEqual(result.error, KEPT_DRAFT_WORDS);
});

test("a second tab says it is read only and holds no layer", async () => {
  const r = await rig();
  const second = new DeskOffline({ signal: r.signal, qd: r.qd, transport: r.strict.transport, dbName: "x", open: async () => ({ layer: null, readOnly: true }) });
  await second.boot(BRANCH);
  assert.equal(second.bannerMode(), "read-only");
});

test("offline, the client's new quote is kept; the desk shows it waiting, and the banner counts it", async () => {
  const r = await rig();
  await r.desk.boot(BRANCH);
  r.signal.set(false);
  r.server.setOffline(true);
  await r.desk.setOnline(false);
  const result = await r.qd.createQuote(MAPLE, BRANCH, "2999-12-31");
  assert.equal(result.error, KEPT_DRAFT_WORDS);
  const waiting = await r.desk.waiting();
  assert.equal(waiting.length, 1);
  assert.equal(waiting[0].customerName, "Maple Ridge Contracting");
  assert.equal(waiting[0].kind, "new");
  assert.equal(r.desk.bannerMode(), "offline");
  assert.equal(await r.desk.waitingCount(), 1);
  assert.ok(r.events.includes("changed"));
});

test("the signal returns: the draft lands, the waiting list empties, no card is owed", async () => {
  const r = await rig();
  await r.desk.boot(BRANCH);
  r.signal.set(false);
  r.server.setOffline(true);
  await r.desk.setOnline(false);
  await r.qd.createQuote(MAPLE, BRANCH, "");
  r.signal.set(true);
  r.server.setOffline(false);
  await r.desk.setOnline(true);
  await r.desk.cycle();
  assert.equal((await r.desk.waiting()).length, 0);
  assert.equal(r.desk.bannerMode(), "");
  assert.equal((await r.desk.cards()).length, 0);
  assert.deepEqual(r.strict.offenders, []);
  const landed = [...r.server.requests].filter((q) => q.kind === "push").flatMap((q) => q.ops);
  assert.equal(landed.length, 1);
  assert.deepEqual(Object.keys(landed[0].payload).sort(), ["branch_id", "customer_id", "id"]);
});

test("withdrawing a waiting draft sends nothing the server keeps", async () => {
  const r = await rig();
  await r.desk.boot(BRANCH);
  r.signal.set(false);
  r.server.setOffline(true);
  await r.desk.setOnline(false);
  await r.qd.createQuote(MAPLE, BRANCH, "");
  const [draft] = await r.desk.waiting();
  const gone = await r.desk.withdraw(draft.quoteId);
  assert.equal(gone.ok, true);
  r.signal.set(true);
  r.server.setOffline(false);
  await r.desk.setOnline(true);
  await r.desk.cycle();
  assert.equal(r.server.row("quotes", draft.quoteId)?.deleted, true);
  assert.equal((await r.desk.waiting()).length, 0);
});

test("a server refusal of a queued draft comes back as a card the person reads and dismisses", async () => {
  const r = await rig();
  await r.desk.boot(BRANCH);
  r.signal.set(false);
  r.server.setOffline(true);
  await r.desk.setOnline(false);
  await r.qd.createQuote(MAPLE, BRANCH, "");
  const [draft] = await r.desk.waiting();
  r.server.refuse((await r.desk.layerStore().outbox.pending())[0].key, "quote: customer is closed");
  r.signal.set(true);
  r.server.setOffline(false);
  await r.desk.setOnline(true);
  await r.desk.cycle();
  const cards = await r.desk.cards();
  assert.equal(cards.length, 1);
  assert.equal(cards[0].words.title, "The server did not take the quote change");
  assert.match(cards[0].words.body, /customer is closed/);
  await r.desk.dismiss(cards[0].id);
  assert.equal((await r.desk.cards()).length, 0);
  assert.equal(draft.kind, "new");
});

test("the cached note: a cached read says how old the copy is, a live read clears it", async () => {
  const r = await rig();
  await r.desk.boot(BRANCH);
  assert.equal(r.desk.cachedWords(), "");
  r.desk.hooks.onCached("Pulled 3 min ago");
  assert.equal(r.desk.cachedWords(), "Pulled 3 min ago");
  r.desk.hooks.onLive();
  assert.equal(r.desk.cachedWords(), "");
});

test("closing takes the keeper away: a new quote with no signal is the plain refusal again", async () => {
  const r = await rig();
  await r.desk.boot(BRANCH);
  await r.desk.close();
  r.signal.set(false);
  const result = await r.qd.createQuote(MAPLE, BRANCH, "");
  assert.notEqual(result.error, KEPT_DRAFT_WORDS);
  assert.equal(r.desk.available, false);
});
