// The sync driver of a desk app over the store and the fake server: the
// connectivity word moves the statechart, a cycle pushes then pulls, the
// banner mode follows the state, and a read cache only app cycles with an
// empty outbox. Plus the words of a conflict card.
import { test } from "node:test";
import assert from "node:assert/strict";
import { WorkSync, bannerMode, openWorkOfflineOver } from "../src/sync.ts";
import { QuoteDrafts } from "../src/quote-drafts.ts";
import { cardWords } from "../src/cards.ts";
import { WorkReader } from "../src/read-cache.ts";
import { BRANCH, CUSTOMER, rig } from "./_rig.mjs";

const NEW_ID = (n) => `0d000000-0000-4000-9000-${String(n).padStart(12, "0")}`;

async function deskSync(appId = "quote-desk") {
  const r = await rig(appId);
  let pulledTimes = 0;
  const opened = await openWorkOfflineOver(r.backend, { appId, transport: r.transport, now: r.now, newKey: r.newKey, onPulled: () => (pulledTimes += 1) });
  assert.ok(opened.layer);
  const { store, sync, sql } = opened.layer;
  let n = 0;
  const drafts = appId === "quote-desk" ? new QuoteDrafts({ store, sql, branchId: BRANCH, now: r.now, newId: () => NEW_ID(++n) }) : null;
  return { ...r, store, sync, drafts, reader: new WorkReader(store, appId), pulled: () => pulledTimes };
}

test("the first cycle pulls the scopes the app declares", async () => {
  const d = await deskSync();
  await d.sync.cycle();
  assert.equal(d.sync.runner.state, "idle");
  assert.equal((await d.reader.catalogue("paver")).hits.length, 1);
  assert.equal(d.pulled(), 1);
});

test("offline the machine rests offline, drafts wait, and the banner says so with the count", async () => {
  const d = await deskSync();
  await d.sync.cycle();
  d.server.setOffline(true);
  await d.sync.setOnline(false);
  assert.equal(d.sync.runner.state, "offline");
  assert.equal(bannerMode(d.sync.runner.state, d.store.ownership()), "offline");
  await d.drafts.createDraft({ customerId: CUSTOMER, validUntil: "" });
  assert.equal(await d.sync.waiting(), 1);
});

test("back online a cycle sends the draft, in order, and pulls what the server holds", async () => {
  const d = await deskSync();
  await d.sync.cycle();
  d.server.setOffline(true);
  await d.sync.setOnline(false);
  await d.drafts.createDraft({ customerId: CUSTOMER, validUntil: "2999-12-31" });
  d.server.setOffline(false);
  await d.sync.setOnline(true);
  await d.sync.cycle();
  assert.equal(await d.sync.waiting(), 0);
  assert.ok(d.server.row("quotes", NEW_ID(1)));
  assert.equal(d.sync.runner.state, "idle");
  assert.equal(bannerMode(d.sync.runner.state, d.store.ownership()), "");
});

test("a read cache only app cycles as a pull and has nothing to send", async () => {
  const d = await deskSync("counter-till");
  await d.sync.cycle();
  assert.equal(await d.sync.waiting(), 0);
  assert.equal((await d.reader.parkedTickets()).rows.length, 1);
  assert.ok(!d.server.requests.some((r) => r.kind === "push"), "no push ever left the till");
});

test("a repeated connectivity word is ignored", async () => {
  const d = await deskSync();
  await d.sync.setOnline(true);
  await d.sync.setOnline(true);
  assert.equal(d.sync.syncing, false);
});

test("banner modes: read only wins, then the state's own word, and rest says nothing", () => {
  assert.equal(bannerMode("idle", "read_cache_only"), "read-only");
  assert.equal(bannerMode("offline", "owner"), "offline");
  assert.equal(bannerMode("unreachable", "owner"), "unreachable");
  assert.equal(bannerMode("auth_expired", "owner"), "auth-expired");
  assert.equal(bannerMode("idle", "owner"), "");
  assert.equal(bannerMode("conflict", "owner"), "");
});

test("a second tab opens no layer and says it is read only", async () => {
  const r = await rig("quote-desk");
  const second = { ...r.backend, ownership: () => "read_cache_only" };
  const opened = await openWorkOfflineOver(second, { appId: "quote-desk", transport: r.transport });
  assert.equal(opened.layer, null);
  assert.equal(opened.readOnly, true);
});

test("a card says what happened in plain words for each kind, and never shows a blank refusal", () => {
  const base = { id: "c", clientId: "k", scope: "quotes", policy: null, entityId: null, serverState: {}, clientPayload: {}, error: null, status: "open", createdAt: "x" };
  for (const kind of ["merge_report", "superseded", "review", "rejected"]) {
    const words = cardWords({ ...base, kind });
    assert.ok(words.title.length > 0 && words.body.length > 0, kind);
    assert.doesNotMatch(`${words.title} ${words.body}`, /[\u2013\u2014]/);
  }
  assert.match(cardWords({ ...base, kind: "rejected", error: "the quote is SENT and its expiry date is frozen" }).body, /frozen/);
  assert.match(cardWords({ ...base, kind: "merge_report" }).title, /quote/);
  assert.match(cardWords({ ...base, kind: "merge_report", scope: "purchase_orders" }).title, /purchase order/);
});
