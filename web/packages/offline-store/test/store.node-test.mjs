// The store: pull with since seq per scope, reads that answer from the store
// with their staleness, and the conflict outcomes the server returns turned
// into cards that are kept locally and exposed to the UI.
import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { OfflineStore } from "../src/store.ts";
import { createSqliteBackend } from "../src/backend-sqlite.ts";
import { createOpfsBackend } from "../src/backend-opfs.ts";
import { scopeBookFor } from "../src/scopes.ts";
import { nodeSqliteExecutor, FakeLocks, FakeWorker } from "../src/testing.ts";
import { bodyHash } from "../src/canonical.ts";
import { baseRig, pick, uuid, FIELD_APPS } from "./_rig.mjs";

async function rig(options = {}) {
  const r = await baseRig();
  const backend = createSqliteBackend(r.sql);
  const store = await OfflineStore.open({ backend, scopes: r.scopes, now: r.now, newKey: r.newKey, ...options });
  return { ...r, backend, store };
}

const pulls = (server) => server.requests.filter((q) => q.kind === "pull");

test("open migrates the declared scopes' tables for the owner", async () => {
  const { sql } = await rig();
  const names = (await sql.all("SELECT name FROM sqlite_master WHERE type = 'table' AND name LIKE 'ent_%' ORDER BY name")).map((r) => r.name);
  assert.deepEqual(names, ["ent_attachments", "ent_inventory_lookup", "ent_pick_tasks", "ent_pod", "ent_receiving", "ent_runs", "ent_shortages", "ent_stops", "ent_weights"]);
});

test("pull stores the scope's rows and records the cursor and the time", async () => {
  const { store, server, transport, sql } = await rig();
  const out = await store.pull(transport, "pick_tasks");
  assert.deepEqual(out, { kind: "done", scopes: 1, items: 9, tombstones: 0 });
  const state = await sql.all("SELECT scope, last_seq, last_pull_at FROM sync_state");
  assert.deepEqual(state, [{ scope: "pick_tasks", last_seq: 9, last_pull_at: state[0].last_pull_at }]);
  assert.ok(state[0].last_pull_at);
  const list = await store.list("pick_tasks");
  assert.equal(list.rows.length, 9);
  assert.deepEqual(list.rows[0], { id: uuid(1), seq: 1, data: { n: 1 } });
  assert.deepEqual(pulls(server).map((q) => q.since), [0]);
});

test("the next pull asks only for what is newer than the stored cursor", async () => {
  const { store, server, transport } = await rig();
  await store.pull(transport, "pick_tasks");
  server.touch("pick_tasks", uuid(3), { n: 33 });
  const out = await store.pull(transport, "pick_tasks");
  assert.equal(out.items, 1);
  assert.deepEqual(pulls(server).map((q) => q.since), [0, 9]);
  assert.deepEqual((await store.read("pick_tasks", uuid(3))).row.data, { n: 33 });
  // the cursor moved on with the second pull (the touched row has the server's newest seq)
  const touchedSeq = server.row("pick_tasks", uuid(3)).seq;
  assert.equal((await store.staleness("pick_tasks")).lastSeq, touchedSeq);
  await store.pull(transport, "pick_tasks");
  assert.deepEqual(pulls(server).map((q) => q.since), [0, 9, touchedSeq]);
});

test("pull with no scope pulls every declared scope, each with its own cursor", async () => {
  const { store, server, transport, scopes } = await rig();
  const out = await store.pull(transport);
  assert.equal(out.kind, "done");
  assert.equal(out.scopes, scopes.declared().length);
  assert.deepEqual(pulls(server).map((q) => q.scope), scopes.declared());
  assert.equal((await store.list("shortages")).rows.length, 9);
  assert.equal((await store.list("runs")).rows.length, 0);
});

test("pages are applied one at a time, and a failure part way keeps the pages already stored", async () => {
  const { store, server, transport, sql } = await rig({ pageLimit: 4 });
  server.failNext("status", 500); // consumed by the first request
  let out = await store.pull(transport, "pick_tasks");
  assert.equal(out.kind, "fault");
  assert.deepEqual(await sql.all("SELECT last_seq FROM sync_state"), []);
  // first page lands, the second one faults
  const real = transport.pull;
  let calls = 0;
  const flaky = { ...transport, pull: async (...a) => (++calls === 2 ? { ok: false, error: "boom", status: 503 } : real(...a)) };
  out = await store.pull(flaky, "pick_tasks");
  assert.equal(out.kind, "fault");
  assert.deepEqual(await sql.all("SELECT last_seq FROM sync_state"), [{ last_seq: 4 }]);
  assert.equal((await store.list("pick_tasks")).rows.length, 4);
  // the retry resumes from the stored cursor
  out = await store.pull(transport, "pick_tasks");
  assert.equal(out.kind, "done");
  assert.equal((await store.list("pick_tasks")).rows.length, 9);
  // the faulted first request, page one, then the resumed pages from the stored cursor
  assert.deepEqual(pulls(server).map((q) => q.since), [0, 0, 4, 8]);
});

test("a tombstone hides the row from reads and keeps the delete", async () => {
  const { store, server, transport, sql } = await rig();
  await store.pull(transport, "pick_tasks");
  server.remove("pick_tasks", uuid(2));
  await store.pull(transport, "pick_tasks");
  assert.equal((await store.read("pick_tasks", uuid(2))).row, null);
  assert.equal((await store.list("pick_tasks")).rows.length, 8);
  assert.deepEqual(await sql.all("SELECT deleted FROM ent_pick_tasks WHERE id = ?", [uuid(2)]), [{ deleted: 1 }]);
});

test("reads say how stale they are", async () => {
  const { store, transport } = await rig();
  const before = await store.list("pick_tasks");
  assert.deepEqual(before.staleness, { scope: "pick_tasks", neverPulled: true, lastSeq: 0, lastPullAt: null, ageMs: null });
  await store.pull(transport, "pick_tasks");
  const after = await store.read("pick_tasks", uuid(1));
  assert.equal(after.staleness.neverPulled, false);
  assert.equal(after.staleness.lastSeq, 9);
  assert.match(after.staleness.lastPullAt, /^2000-01-01T/);
  assert.ok(after.staleness.ageMs >= 0);
  const missing = await store.read("pick_tasks", uuid(99));
  assert.equal(missing.row, null);
  assert.equal(missing.staleness.lastSeq, 9);
});

test("an empty page refreshes the time and never moves the cursor back", async () => {
  const { store, transport, sql } = await rig();
  await store.pull(transport, "pick_tasks");
  const first = (await sql.all("SELECT last_pull_at FROM sync_state"))[0].last_pull_at;
  await store.pull(transport, "pick_tasks");
  const row = (await sql.all("SELECT last_seq, last_pull_at FROM sync_state"))[0];
  assert.equal(row.last_seq, 9);
  assert.notEqual(row.last_pull_at, first);
  const backwards = { ...transport, pull: async (scope) => ({ ok: true, value: { scope, items: [], tombstones: [], last_seq: 2, next_cursor: null } }) };
  await store.pull(backwards, "pick_tasks");
  assert.equal((await sql.all("SELECT last_seq FROM sync_state"))[0].last_seq, 9);
});

test("a row delivered with an older seq than the one stored does not overwrite it", async () => {
  const { store, transport, sql } = await rig();
  await store.pull(transport, "pick_tasks");
  const stale = { ...transport, pull: async (scope) => ({ ok: true, value: { scope, items: [{ id: uuid(1), seq: 0, data: { n: "old" } }], tombstones: [], last_seq: 9, next_cursor: null } }) };
  await store.pull(stale, "pick_tasks");
  assert.deepEqual((await store.read("pick_tasks", uuid(1))).row.data, { n: 1 });
  assert.equal((await sql.all("SELECT seq FROM ent_pick_tasks WHERE id = ?", [uuid(1)]))[0].seq, 1);
});

test("pull failures are sorted: offline, auth expired, fault; nothing is written", async () => {
  const { store, server, transport, sql } = await rig();
  server.setOffline(true);
  assert.equal((await store.pull(transport, "pick_tasks")).kind, "offline");
  server.setOffline(false);
  server.failNext("status", 401);
  assert.equal((await store.pull(transport, "pick_tasks")).kind, "auth_expired");
  server.failNext("status", 500);
  const fault = await store.pull(transport, "pick_tasks");
  assert.equal(fault.kind, "fault");
  assert.equal(fault.status, 500);
  assert.deepEqual(await sql.all("SELECT * FROM sync_state"), []);
});

test("a page for another scope, or one that makes no progress, is a fault and not a loop", async () => {
  const { store, transport } = await rig();
  const wrong = { ...transport, pull: async () => ({ ok: true, value: { scope: "shortages", items: [], tombstones: [], last_seq: 0, next_cursor: null } }) };
  assert.equal((await store.pull(wrong, "pick_tasks")).kind, "fault");
  const stuck = { ...transport, pull: async (scope) => ({ ok: true, value: { scope, items: [], tombstones: [], last_seq: 0, next_cursor: "0" } }) };
  assert.equal((await store.pull(stuck, "pick_tasks")).kind, "fault");
});

test("a scope the profile does not declare cannot be read or pulled", async () => {
  const { store, transport } = await rig();
  await assert.rejects(store.list("products"), /not a scope of this profile/);
  await assert.rejects(store.pull(transport, "products"), /not a scope of this profile/);
});

// ---- conflict cards ----------------------------------------------------

async function conflictRig() {
  const r = await rig();
  await r.store.pull(r.transport); // the device knows the world
  return r;
}

test("server wins: the stale pick becomes a superseded card, the outbox row is closed as conflict", async () => {
  const { store, server, transport } = await conflictRig();
  server.touch("pick_tasks", uuid(1), { n: 100, reassigned: true });
  const op = await store.enqueue({ ...pick(1, { picked: 3 }), baseSeq: 1 });
  const out = await store.replay(transport);
  assert.equal(out.kind, "done");
  assert.equal(out.conflicts, 1);
  const cards = await store.cards.listOpen();
  assert.equal(cards.length, 1);
  assert.deepEqual(
    { kind: cards[0].kind, policy: cards[0].policy, scope: cards[0].scope, clientId: cards[0].clientId, entityId: cards[0].entityId, status: cards[0].status },
    { kind: "superseded", policy: "server_wins", scope: "pick_tasks", clientId: op.key, entityId: uuid(1), status: "open" },
  );
  assert.deepEqual(cards[0].serverState, { n: 100, reassigned: true });
  assert.deepEqual(cards[0].clientPayload, { id: uuid(1), picked: 3 });
  assert.equal((await store.outbox.list("conflict")).length, 1);
});

test("manual: a stale shortage becomes a review card and nothing of the client's edit reached the server", async () => {
  const { store, server, transport } = await conflictRig();
  server.touch("shortages", uuid(2), { line: "moved" });
  const op = await store.enqueue({ scope: "shortages", op: "update", payload: { id: uuid(2), short_by: 2 }, baseSeq: 1 });
  await store.replay(transport);
  const [card] = await store.cards.listOpen();
  assert.equal(card.kind, "review");
  assert.equal(card.policy, "manual");
  assert.equal(card.clientId, op.key);
  assert.equal(server.row("shortages", uuid(2)).data.short_by, undefined);
});

test("last write wins by field: the merge is applied and reported as a merge card", async () => {
  const { store, server, transport } = await conflictRig();
  server.seed("attachments", uuid(5), { a: 1, b: 1 });
  await store.pull(transport, "attachments");
  server.touch("attachments", uuid(5), { b: 2 });
  const op = await store.enqueue({ scope: "attachments", op: "update", payload: { id: uuid(5), a: 9 }, baseSeq: 1 });
  const out = await store.replay(transport);
  assert.equal(out.conflicts, 1);
  const [card] = await store.cards.listOpen();
  assert.equal(card.kind, "merge_report");
  assert.equal(card.policy, "last_write_wins_by_field");
  assert.deepEqual(card.serverState, { id: uuid(5), a: 9, b: 2 });
  const row = (await store.outbox.list()).find((r) => r.key === op.key);
  assert.equal(row.status, "applied");
  assert.ok(Number.isInteger(row.serverSeq));
});

test("a rejected operation also leaves a card with the module's words", async () => {
  const { store, server, transport } = await conflictRig();
  const op = await store.enqueue(pick(4));
  server.refuse(op.key, "pick task is closed");
  await store.replay(transport);
  const [card] = await store.cards.listOpen();
  assert.equal(card.kind, "rejected");
  assert.equal(card.error, "pick task is closed");
  assert.equal(card.clientId, op.key);
});

test("a plain applied operation leaves no card", async () => {
  const { store, transport } = await conflictRig();
  await store.enqueue(pick(1));
  await store.replay(transport);
  assert.deepEqual(await store.cards.listOpen(), []);
});

test("dismissing a card hides it from the open list and keeps it on record", async () => {
  const { store, server, transport } = await conflictRig();
  server.touch("pick_tasks", uuid(1), { n: 100 });
  await store.enqueue({ ...pick(1), baseSeq: 1 });
  await store.replay(transport);
  const [card] = await store.cards.listOpen();
  await store.cards.dismiss(card.id);
  assert.deepEqual(await store.cards.listOpen(), []);
  const all = await store.cards.list();
  assert.equal(all.length, 1);
  assert.equal(all[0].status, "dismissed");
});

test("subscribers hear a card arrive and a card leave", async () => {
  const { store, server, transport } = await conflictRig();
  const heard = [];
  const off = store.cards.subscribe(() => heard.push("changed"));
  server.touch("pick_tasks", uuid(1), { n: 100 });
  await store.enqueue({ ...pick(1), baseSeq: 1 });
  await store.replay(transport);
  assert.equal(heard.length, 1);
  const [card] = await store.cards.listOpen();
  await store.cards.dismiss(card.id);
  assert.equal(heard.length, 2);
  off();
  await store.cards.dismiss(card.id);
  assert.equal(heard.length, 2);
});

test("cards survive a restart", async () => {
  const dir = mkdtempSync(join(tmpdir(), "hh-cards-"));
  try {
    const path = join(dir, "s.db");
    const scopes = scopeBookFor(FIELD_APPS);
    const server = (await baseRig()).server;
    const first = await OfflineStore.open({ backend: createSqliteBackend(nodeSqliteExecutor(path)), scopes });
    await first.pull(server.transport());
    server.touch("pick_tasks", uuid(1), { n: 100 });
    await first.enqueue({ ...pick(1), baseSeq: 1 });
    await first.replay(server.transport());
    await first.close();
    const second = await OfflineStore.open({ backend: createSqliteBackend(nodeSqliteExecutor(path)), scopes });
    const cards = await second.cards.listOpen();
    assert.equal(cards.length, 1);
    assert.equal(cards[0].kind, "superseded");
    assert.equal((await second.list("pick_tasks")).rows.length, 9);
    await second.close();
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("a replay that is repeated cannot make a second card for the same operation", async () => {
  const { store, server, transport, sql } = await conflictRig();
  server.touch("pick_tasks", uuid(1), { n: 100 });
  const op = await store.enqueue({ ...pick(1), baseSeq: 1 });
  await store.replay(transport);
  await sql.run("UPDATE outbox SET status = 'pending' WHERE idempotency_key = ?", [op.key]); // as if the answer had been lost
  await store.replay(transport);
  assert.equal((await store.cards.list()).length, 1);
});

// ---- read cache only tab -------------------------------------------------

test("a read cache only tab opens without touching the database and refuses every write", async () => {
  const locks = new FakeLocks();
  const owner = await createOpfsBackend({ dbName: "hh", locks, createWorker: () => new FakeWorker(nodeSqliteExecutor()) });
  const scopes = scopeBookFor(FIELD_APPS);
  const second = await createOpfsBackend({ dbName: "hh", locks, createWorker: () => new FakeWorker(nodeSqliteExecutor()) });
  const store = await OfflineStore.open({ backend: second, scopes });
  assert.equal(store.ownership(), "read_cache_only");
  const enq = await store.enqueue(pick(1));
  assert.deepEqual({ ok: enq.ok, reason: enq.reason }, { ok: false, reason: "read_only" });
  const server = (await baseRig()).server;
  assert.deepEqual(await store.replay(server.transport()), { kind: "read_only" });
  assert.deepEqual(await store.pull(server.transport()), { kind: "read_only" });
  assert.equal(server.requests.length, 0);
  const list = await store.list("pick_tasks");
  assert.deepEqual({ rows: list.rows, readOnly: list.readOnly }, { rows: [], readOnly: true });
  assert.deepEqual(await store.cards.listOpen(), []);
  await owner.close();
});

// ---- replay integrity: a stored row that cannot be trusted is closed with a card ------------

const hashOf = (row) => bodyHash({ scope: row.scope, op: row.op, entity: row.entity ?? null, payload: row.payload, baseSeq: row.baseSeq ?? null });

test("a stored body that fails its hash is closed as rejected WITH a card, never silently", async () => {
  const { store, sql, transport, server } = await rig();
  const a = await store.enqueue(pick(1));
  const b = await store.enqueue(pick(2));
  await sql.run("UPDATE outbox SET payload = ? WHERE idempotency_key = ?", [JSON.stringify({ id: uuid(1), picked: 999 }), a.key]);
  const out = await store.replay(transport);
  assert.equal(out.kind, "done");
  assert.deepEqual(server.appliedOrder, [b.key]);
  const cards = await store.cards.listOpen();
  assert.deepEqual(cards.map((c) => [c.clientId, c.kind]), [[a.key, "rejected"]]);
  assert.match(cards[0].error, /body hash/);
});

test("a stored payload that does not parse is closed as rejected with a card, and the list still reads", async () => {
  const { store, sql, transport, server } = await rig();
  const a = await store.enqueue(pick(1));
  const b = await store.enqueue(pick(2));
  await sql.run("UPDATE outbox SET payload = ? WHERE idempotency_key = ?", ['{"id": [,]}', a.key]);
  assert.equal((await store.outbox.list()).length, 2, "an unreadable row must not make the whole outbox unreadable");
  const out = await store.replay(transport);
  assert.equal(out.kind, "done");
  assert.deepEqual(server.appliedOrder, [b.key]);
  assert.deepEqual((await store.outbox.list()).map((r) => r.status), ["rejected", "applied"]);
  const cards = await store.cards.listOpen();
  assert.deepEqual(cards.map((c) => [c.clientId, c.kind]), [[a.key, "rejected"]]);
  assert.match(cards[0].error, /not readable|does not parse|unreadable/);
});

test("replay re-checks the money fence: a stored money row is closed with a card and never sent", async () => {
  const { store, sql, transport, server } = await rig();
  const a = await store.enqueue(pick(1));
  const b = await store.enqueue(pick(2));
  // as if an older build had queued it before the scope joined the money list
  const row = (await store.outbox.list())[0];
  await sql.run("UPDATE outbox SET scope = 'payments', body_hash = ? WHERE idempotency_key = ?", [await hashOf({ ...row, scope: "payments" }), a.key]);
  const out = await store.replay(transport);
  assert.equal(out.kind, "done");
  assert.deepEqual(server.appliedOrder, [b.key]);
  assert.ok(!server.requests.some((q) => q.kind === "push" && q.scope === "payments"), "a money scope must never be pushed");
  const cards = await store.cards.listOpen();
  assert.deepEqual(cards.map((c) => [c.clientId, c.kind]), [[a.key, "rejected"]]);
  assert.match(cards[0].error, /money/);
});

test("replay re-checks the scope name shape: a stored name that is not a registry scope is closed, never spliced or sent", async () => {
  const { store, sql, transport, server } = await rig();
  const a = await store.enqueue(pick(1));
  const row = (await store.outbox.list())[0];
  await sql.run("UPDATE outbox SET scope = ?, body_hash = ? WHERE idempotency_key = ?", ["Pick Tasks/../x", await hashOf({ ...row, scope: "Pick Tasks/../x" }), a.key]);
  const out = await store.replay(transport);
  assert.equal(out.kind, "done");
  assert.equal(server.requests.length, 0);
  assert.deepEqual((await store.outbox.list()).map((r) => r.status), ["rejected"]);
  assert.equal((await store.cards.listOpen()).length, 1);
});

test("an applied result with an envelope but no server state, for a create with no id, still records its card", async () => {
  const { store, sql } = await rig();
  const op = await store.enqueue({ scope: "shortages", op: "create", payload: { line: 3 } });
  const answer = {
    ok: true,
    value: { results: [{ client_id: op.key, status: "applied", seq: 40, conflict: { client_id: op.key, policy: "last_write_wins_by_field", applied: true } }] },
  };
  const out = await store.replay({ push: async () => answer, pull: async () => assert.fail("no pull") });
  assert.equal(out.kind, "done", JSON.stringify(out));
  const [card] = await store.cards.listOpen();
  assert.equal(card.kind, "merge_report");
  assert.deepEqual(card.serverState, {});
  void sql;
});
