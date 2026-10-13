// The quote draft outbox: a draft made offline lands when the signal returns,
// last write wins by field, and what is queued carries only the fields the
// core's quotes store admits (read from the Go source by the strict server).
import { test } from "node:test";
import assert from "node:assert/strict";
import { QuoteDrafts, QUOTE_CREATE_FIELDS, QUOTE_UPDATE_FIELDS } from "../src/quote-drafts.ts";
import { WorkReader, openWorkStore } from "../src/read-cache.ts";
import { goFieldSet } from "../src/testing.ts";
import { BRANCH, CUSTOMER, rig, uuid } from "./_rig.mjs";

const NEW_ID = (n) => `0d000000-0000-4000-9000-${String(n).padStart(12, "0")}`;

async function desk() {
  const r = await rig("quote-desk");
  const store = await openWorkStore({ appId: "quote-desk", backend: r.backend, now: r.now, newKey: r.newKey });
  let n = 0;
  const drafts = new QuoteDrafts({ store, sql: r.backend.sql, branchId: BRANCH, now: r.now, newId: () => NEW_ID(++n) });
  await store.pull(r.transport);
  return { ...r, store, drafts, reader: new WorkReader(store, "quote-desk") };
}

async function sync(d) {
  const pushed = await d.store.replay(d.transport);
  const pulled = await d.store.pull(d.transport);
  return { pushed, pulled };
}

test("offline, a draft is kept on the device, in the outbox, and shown as waiting", async () => {
  const d = await desk();
  d.server.setOffline(true);
  const made = await d.drafts.createDraft({ customerId: CUSTOMER, validUntil: "2999-12-31" });
  assert.equal(made.ok, true);
  assert.equal(made.value.id, NEW_ID(1));
  assert.equal((await d.store.outbox.health()).waiting, 1);
  const waiting = await d.drafts.waiting();
  assert.equal(waiting.length, 1);
  assert.equal(waiting[0].quoteId, NEW_ID(1));
  assert.equal(waiting[0].kind, "new");
  assert.equal(waiting[0].customerName, "Northline Hardscapes Ltd (demo)");
  assert.equal(waiting[0].validUntil, "2999-12-31");
  assert.equal(d.server.row("quotes", NEW_ID(1)), undefined, "nothing reached the server");
  const docs = await d.reader.openDocuments();
  assert.ok(docs.rows.some((row) => row.id === NEW_ID(1) && row.status === "DRAFT"), "the document list shows the draft");
});

test("a pending draft shows no total: an unknown figure is never rendered as a zero", async () => {
  const d = await desk();
  d.server.setOffline(true);
  await d.drafts.createDraft({ customerId: CUSTOMER, validUntil: "" });
  const row = (await d.reader.openDocuments()).rows.find((r) => r.id === NEW_ID(1));
  assert.equal(row.totalCents, null);
  assert.equal(row.quoteNo, "");
});

test("when the signal returns the draft lands, once, and the local row becomes the server's", async () => {
  const d = await desk();
  d.server.setOffline(true);
  await d.drafts.createDraft({ customerId: CUSTOMER, validUntil: "2999-12-31" });
  d.server.setOffline(false);
  const { pushed } = await sync(d);
  assert.equal(pushed.kind, "done");
  assert.equal(pushed.applied, 1);
  const landed = d.server.row("quotes", NEW_ID(1));
  assert.ok(landed, "the server holds the draft");
  assert.deepEqual(Object.keys(landed.data).sort(), ["branch_id", "customer_id", "id", "valid_until"]);
  assert.equal(landed.data.customer_id, CUSTOMER);
  assert.equal(landed.data.branch_id, BRANCH);
  assert.equal((await d.drafts.waiting()).length, 0);
  assert.equal((await d.store.outbox.health()).waiting, 0);
  const row = (await d.store.read("quotes", NEW_ID(1))).row;
  assert.ok(row.seq > 0, "the pulled server row replaced the local one");
});

test("replaying twice never makes a second quote: one key per operation", async () => {
  const d = await desk();
  d.server.setOffline(true);
  const made = await d.drafts.createDraft({ customerId: CUSTOMER, validUntil: "" });
  d.server.setOffline(false);
  await sync(d);
  await sync(d);
  assert.equal(d.server.applyCount(made.value.key), 1);
});

test("every payload sent carries only the fields the core admits, create and update alike", async () => {
  const d = await desk();
  await d.drafts.createDraft({ customerId: CUSTOMER, validUntil: "2999-12-31" });
  await d.drafts.setValidUntil(uuid(21), "2999-06-30");
  await sync(d);
  const pushes = d.requests.filter((r) => r.kind === "push");
  assert.ok(pushes.flatMap((p) => p.ops).length >= 2);
  const create = goFieldSet("quote/syncscope.go", "quoteCreateFields");
  const update = goFieldSet("quote/syncscope.go", "quoteWriteFields");
  for (const push of pushes) {
    for (const op of push.ops) {
      const allowed = op.op === "create" ? create : update;
      for (const field of Object.keys(op.payload)) assert.ok(allowed.has(field), `${op.op} ${field}`);
    }
  }
  assert.deepEqual(d.offenders, []);
  assert.deepEqual([...QUOTE_CREATE_FIELDS].sort(), [...create].sort());
  assert.deepEqual([...QUOTE_UPDATE_FIELDS].sort(), [...update].sort());
});

test("RED PROOF: the strict server refuses a field the core does not admit, as the core does", async () => {
  const d = await desk();
  const queued = await d.store.enqueue({ scope: "quotes", op: "create", payload: { id: NEW_ID(50), customer_id: CUSTOMER, branch_id: BRANCH, total_cents: 1 } });
  assert.equal(queued.ok, true, "the store alone does not know the core's sets");
  const out = await d.store.replay(d.transport);
  assert.equal(out.rejected, 1);
  const card = (await d.store.cards.listOpen())[0];
  assert.equal(card.kind, "rejected");
  assert.match(card.error, /field "total_cents" is not writable through sync/);
  assert.deepEqual(d.offenders, ["quotes create: total_cents"]);
});

test("a validity edit made offline merges by field with the desk's change and leaves a merge card", async () => {
  const d = await desk();
  d.server.setOffline(true);
  const edited = await d.drafts.setValidUntil(uuid(21), "2999-06-30");
  assert.equal(edited.ok, true);
  d.server.touch("quotes", uuid(21), { valid_until: "2999-09-09" });
  d.server.setOffline(false);
  const out = await d.store.replay(d.transport);
  assert.equal(out.kind, "done");
  assert.equal(out.applied, 1);
  const cards = await d.store.cards.listOpen();
  assert.equal(cards.length, 1);
  assert.equal(cards[0].kind, "merge_report");
  assert.equal(cards[0].scope, "quotes");
  assert.equal(d.server.row("quotes", uuid(21)).data.valid_until, "2999-06-30", "last write wins by field");
});

test("a second edit of the same quote waits behind the first and goes blind, in order", async () => {
  const d = await desk();
  d.server.setOffline(true);
  await d.drafts.setValidUntil(uuid(21), "2999-06-30");
  await d.drafts.setValidUntil(uuid(21), "2999-07-31");
  const pending = await d.store.outbox.pending();
  assert.equal(pending.length, 2);
  assert.equal(typeof pending[0].baseSeq, "number");
  assert.equal(pending[1].baseSeq, null, "the second names no base: the first is the device's own write");
  d.server.setOffline(false);
  await sync(d);
  assert.equal(d.server.row("quotes", uuid(21)).data.valid_until, "2999-07-31");
});

test("withdrawing a draft that has not landed sends the create, then the delete, in order", async () => {
  const d = await desk();
  d.server.setOffline(true);
  await d.drafts.createDraft({ customerId: CUSTOMER, validUntil: "" });
  const gone = await d.drafts.withdraw(NEW_ID(1));
  assert.equal(gone.ok, true);
  d.server.setOffline(false);
  await sync(d);
  assert.equal(d.server.row("quotes", NEW_ID(1)).deleted, true);
  const kinds = d.requests.filter((r) => r.kind === "push").flatMap((r) => r.ops.map((o) => o.op));
  assert.deepEqual(kinds, ["create", "delete"]);
});

test("a draft the person withdrew no longer reads as an open document", async () => {
  const d = await desk();
  d.server.setOffline(true);
  await d.drafts.createDraft({ customerId: CUSTOMER, validUntil: "" });
  await d.drafts.withdraw(NEW_ID(1));
  assert.ok(!(await d.reader.openDocuments()).rows.some((r) => r.id === NEW_ID(1)));
  assert.equal((await d.drafts.waiting()).some((w) => w.quoteId === NEW_ID(1) && w.kind === "new"), false);
});

test("the inputs are checked before anything is queued: no garbage waits for a server that would refuse it", async () => {
  const d = await desk();
  for (const bad of [{ customerId: "", validUntil: "" }, { customerId: "not-a-uuid", validUntil: "" }, { customerId: CUSTOMER, validUntil: "31/12/2999" }, { customerId: CUSTOMER, validUntil: "2999-13-45" }]) {
    const answer = await d.drafts.createDraft(bad);
    assert.equal(answer.ok, false, JSON.stringify(bad));
  }
  assert.equal((await d.store.outbox.health()).waiting, 0);
  assert.equal((await d.drafts.setValidUntil("nope", "2999-01-01")).ok, false);
  assert.equal((await d.drafts.setValidUntil(uuid(21), "soon")).ok, false);
});

test("an empty validity clears the leg: the draft then never expires", async () => {
  const d = await desk();
  await d.drafts.createDraft({ customerId: CUSTOMER, validUntil: "" });
  const create = (await d.store.outbox.pending())[0];
  assert.equal("valid_until" in create.payload, false);
});

test("a second tab that only reads the cache cannot draft", async () => {
  const d = await desk();
  const stored = d.store;
  const original = stored.ownership.bind(stored);
  stored.ownership = () => "read_cache_only";
  const answer = await d.drafts.createDraft({ customerId: CUSTOMER, validUntil: "" });
  stored.ownership = original;
  assert.equal(answer.ok, false);
  assert.match(answer.error, /read cache only/);
});
