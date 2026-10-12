// The durable outbox: operations appended in order with a key generated once,
// replayed in order against an injected transport. One refused operation does
// not sink the ones behind it; an operational fault fails the whole push and
// the client retries whole; applied replays are recognised by key; a money
// scope never admits a write.
import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Outbox } from "../src/outbox.ts";
import { migrate } from "../src/schema.ts";
import { scopeBookFor, MONEY_SCOPES } from "../src/scopes.ts";
import { nodeSqliteExecutor } from "../src/testing.ts";
import { baseRig, pick, uuid } from "./_rig.mjs";

async function rig(opts = {}) {
  const r = await baseRig();
  const outbox = new Outbox({ sql: r.sql, scopes: r.scopes, now: r.now, newKey: r.newKey, ...opts });
  return { ...r, outbox };
}

const rows = (sql) => sql.all("SELECT seq, idempotency_key AS k, scope, status, attempts, error FROM outbox ORDER BY seq");

test("enqueue appends in order, each with its own uuid key, status pending", async () => {
  const { outbox } = await rig();
  const a = await outbox.enqueue(pick(1));
  const b = await outbox.enqueue(pick(2));
  const c = await outbox.enqueue({ scope: "shortages", op: "create", payload: { id: uuid(9), line: 3 } });
  assert.ok(a.ok && b.ok && c.ok);
  assert.ok(a.seq < b.seq && b.seq < c.seq);
  assert.equal(new Set([a.key, b.key, c.key]).size, 3);
  const pending = await outbox.pending();
  assert.deepEqual(pending.map((r) => r.key), [a.key, b.key, c.key]);
  assert.deepEqual(pending.map((r) => r.status), ["pending", "pending", "pending"]);
  assert.deepEqual(pending[0].payload, { id: uuid(1), picked: 1 });
  assert.equal(pending[0].baseSeq, 1);
});

test("the default key is a random uuid, generated at enqueue", async () => {
  const r = await baseRig();
  const outbox = new Outbox({ sql: r.sql, scopes: r.scopes });
  const a = await outbox.enqueue(pick(1));
  assert.match(a.key, /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
});

test("the key is stable across retries: every attempt sends the same key and body", async () => {
  const { outbox, server, transport } = await rig();
  const a = await outbox.enqueue(pick(1));
  server.failNext("status", 503);
  server.failNext("status", 503);
  await outbox.replay(transport);
  await outbox.replay(transport);
  await outbox.replay(transport);
  const sent = server.requests.filter((q) => q.kind === "push").flatMap((q) => q.ops);
  assert.equal(sent.length, 3);
  assert.deepEqual(new Set(sent.map((o) => o.client_id)), new Set([a.key]));
  assert.equal(new Set(sent.map((o) => JSON.stringify(o))).size, 1, "the body changed between attempts");
  assert.equal(server.applyCount(a.key), 1);
});

test("the key and the order survive a restart (a file database reopened)", async () => {
  const dir = mkdtempSync(join(tmpdir(), "hh-outbox-"));
  try {
    const path = join(dir, "o.db");
    const scopes = scopeBookFor(["yard-pick", "drive-run"]);
    const first = nodeSqliteExecutor(path);
    await migrate(first, scopes.declared());
    const o1 = new Outbox({ sql: first, scopes });
    const k = [(await o1.enqueue(pick(1))).key, (await o1.enqueue(pick(2))).key];
    await first.close();
    const second = nodeSqliteExecutor(path);
    await migrate(second, scopes.declared());
    const o2 = new Outbox({ sql: second, scopes });
    assert.deepEqual((await o2.pending()).map((r) => r.key), k);
    const third = await o2.enqueue(pick(3));
    assert.ok((await o2.pending())[2].seq > (await o2.pending())[1].seq);
    assert.equal((await o2.pending())[2].key, third.key);
    await second.close();
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("a money scope is refused at enqueue and nothing is queued", async () => {
  const r = await baseRig();
  const everything = scopeBookFor(["quote-desk", "counter-till", "pro-pay", "manager-home", "pro-procurement", "yard-pick"]);
  await migrate(r.sql, everything.declared());
  const outbox = new Outbox({ sql: r.sql, scopes: everything, now: r.now, newKey: r.newKey });
  for (const scope of MONEY_SCOPES) {
    const res = await outbox.enqueue({ scope, op: "update", payload: { id: uuid(5), amount: 100 }, baseSeq: 1 });
    assert.equal(res.ok, false, scope);
    assert.equal(res.reason, "money_scope", scope);
  }
  assert.deepEqual(await rows(r.sql), []);
  assert.deepEqual(await outbox.pending(), []);
});

test("a scope the profile does not declare or cannot write is refused", async () => {
  const { outbox, sql } = await rig();
  assert.equal((await outbox.enqueue({ scope: "products", op: "update", payload: { id: uuid(1) } })).reason, "scope_not_declared");
  assert.equal((await outbox.enqueue({ scope: "inventory_lookup", op: "update", payload: { id: uuid(1) } })).reason, "scope_not_writable");
  assert.deepEqual(await rows(sql), []);
});

test("an operation the engine would reject for shape is refused before it is queued", async () => {
  const { outbox, sql } = await rig();
  const bad = [
    { scope: "pick_tasks", op: "update", payload: {} },
    { scope: "pick_tasks", op: "update", payload: { id: "not-a-uuid" } },
    { scope: "pick_tasks", op: "delete", payload: { id: 7 } },
    { scope: "pick_tasks", op: "create" },
    { scope: "pick_tasks", op: "upsert", payload: { id: uuid(1) } },
    { scope: "pick_tasks", op: "update", payload: { id: uuid(1), n: undefined } },
    { scope: "pick_tasks", op: "update", payload: { id: uuid(1) }, baseSeq: -1 },
    { scope: "pick_tasks", op: "update", payload: { id: uuid(1) }, key: "not-a-uuid" },
  ];
  for (const b of bad) assert.equal((await outbox.enqueue(b)).reason, "invalid", JSON.stringify(b));
  assert.deepEqual(await rows(sql), []);
});

test("the same key with the same body is the same operation; with another body it is refused", async () => {
  const { outbox, sql } = await rig();
  const first = await outbox.enqueue({ ...pick(1), key: uuid(500) });
  const again = await outbox.enqueue({ ...pick(1), key: uuid(500) });
  assert.ok(first.ok && again.ok);
  assert.equal(again.duplicate, true);
  assert.equal(again.seq, first.seq);
  const clash = await outbox.enqueue({ ...pick(1, { picked: 99 }), key: uuid(500) });
  assert.equal(clash.ok, false);
  assert.equal(clash.reason, "key_reused");
  assert.equal((await rows(sql)).length, 1);
});

test("replay sends pending operations in enqueue order, batching runs of one scope", async () => {
  const { outbox, server, transport } = await rig();
  const keys = [];
  for (const [scope, n] of [["pick_tasks", 1], ["pick_tasks", 2], ["shortages", 3], ["pick_tasks", 4], ["pick_tasks", 5]]) {
    keys.push((await outbox.enqueue({ scope, op: "update", payload: { id: uuid(n), n } })).key);
  }
  const out = await outbox.replay(transport);
  assert.equal(out.kind, "done");
  const pushes = server.requests.filter((q) => q.kind === "push");
  assert.deepEqual(pushes.map((q) => [q.scope, q.ops.length]), [["pick_tasks", 2], ["shortages", 1], ["pick_tasks", 2]]);
  assert.deepEqual(server.appliedOrder, keys);
  assert.deepEqual((await outbox.pending()), []);
});

test("a batch never exceeds the cap, and order holds across batches", async () => {
  const { outbox, server, transport } = await rig({ maxBatch: 2 });
  const keys = [];
  for (let n = 1; n <= 5; n++) keys.push((await outbox.enqueue(pick(n))).key);
  await outbox.replay(transport);
  assert.deepEqual(server.requests.filter((q) => q.kind === "push").map((q) => q.ops.length), [2, 2, 1]);
  assert.deepEqual(server.appliedOrder, keys);
});

test("one refused operation does not sink the ones behind it", async () => {
  const { outbox, server, transport, sql } = await rig();
  const a = await outbox.enqueue(pick(1));
  const b = await outbox.enqueue(pick(2));
  const c = await outbox.enqueue(pick(3));
  server.refuse(b.key, "pick task is closed");
  const out = await outbox.replay(transport);
  assert.equal(out.kind, "done");
  assert.deepEqual(out.rejected, 1);
  assert.deepEqual(server.appliedOrder, [a.key, c.key]);
  const got = await rows(sql);
  assert.deepEqual(got.map((r) => r.status), ["applied", "rejected", "applied"]);
  assert.equal(got[1].error, "pick task is closed");
  assert.deepEqual(await outbox.pending(), []);
});

test("an operational fault fails the whole push: nothing is marked, order is kept, the retry is whole", async () => {
  const { outbox, server, transport, sql } = await rig();
  const keys = [];
  for (let n = 1; n <= 4; n++) keys.push((await outbox.enqueue(pick(n))).key);
  server.failNext("afterApply", 2); // two operations land, then the engine faults
  const first = await outbox.replay(transport);
  assert.equal(first.kind, "fault");
  assert.equal(first.status, 500);
  assert.deepEqual((await rows(sql)).map((r) => r.status), ["pending", "pending", "pending", "pending"]);
  assert.deepEqual((await rows(sql)).map((r) => r.attempts), [1, 1, 1, 1]);
  const second = await outbox.replay(transport);
  assert.equal(second.kind, "done");
  assert.deepEqual(server.appliedOrder, keys, "order changed across the retry");
  for (const k of keys) assert.equal(server.applyCount(k), 1, "applied twice");
  assert.deepEqual((await rows(sql)).map((r) => r.status), ["applied", "applied", "applied", "applied"]);
});

test("a fault in an early batch stops the replay: operations behind it are not sent", async () => {
  const { outbox, server, transport } = await rig();
  await outbox.enqueue(pick(1));
  await outbox.enqueue({ scope: "shortages", op: "create", payload: { id: uuid(2) } });
  server.failNext("status", 500);
  const out = await outbox.replay(transport);
  assert.equal(out.kind, "fault");
  assert.equal(server.requests.filter((q) => q.kind === "push").length, 1);
});

test("an applied replay is recognised by its key: a lost response costs no second application", async () => {
  const { outbox, server, transport, sql } = await rig();
  const a = await outbox.enqueue(pick(1));
  const b = await outbox.enqueue(pick(2));
  server.failNext("dropResponse"); // the server applies both, the client never hears
  const lost = await outbox.replay(transport);
  assert.equal(lost.kind, "offline");
  assert.deepEqual((await rows(sql)).map((r) => r.status), ["pending", "pending"]);
  const retry = await outbox.replay(transport);
  assert.equal(retry.kind, "done");
  assert.equal(server.applyCount(a.key), 1);
  assert.equal(server.applyCount(b.key), 1);
  assert.deepEqual(server.appliedOrder, [a.key, b.key]);
  const done = await outbox.pending();
  assert.deepEqual(done, []);
  const seqs = await sql.all("SELECT server_seq FROM outbox ORDER BY seq");
  assert.ok(seqs.every((r) => Number.isInteger(r.server_seq)), "the deduped answer's seq was not recorded");
});

test("a transport failure with no status is offline and leaves the outbox as it was", async () => {
  const { outbox, server, transport, sql } = await rig();
  await outbox.enqueue(pick(1));
  server.setOffline(true);
  const out = await outbox.replay(transport);
  assert.equal(out.kind, "offline");
  assert.deepEqual((await rows(sql)).map((r) => [r.status, r.attempts]), [["pending", 0]]);
});

test("a 401 is auth expired and parks the outbox untouched", async () => {
  const { outbox, server, transport, sql } = await rig();
  const a = await outbox.enqueue(pick(1));
  const b = await outbox.enqueue(pick(2));
  server.failNext("status", 401);
  const out = await outbox.replay(transport);
  assert.equal(out.kind, "auth_expired");
  assert.deepEqual((await rows(sql)).map((r) => [r.status, r.attempts]), [["pending", 0], ["pending", 0]]);
  // still accepts writes while parked
  const c = await outbox.enqueue(pick(3));
  assert.ok(c.ok);
  const later = await outbox.replay(transport);
  assert.equal(later.kind, "done");
  assert.deepEqual(server.appliedOrder, [a.key, b.key, c.key]);
});

test("a response that does not answer each operation in order is a fault and marks nothing", async () => {
  const { outbox, server, transport, sql } = await rig();
  await outbox.enqueue(pick(1));
  await outbox.enqueue(pick(2));
  for (const shape of ["short", "reordered", "foreign"]) {
    server.failNext("malformed", shape);
    const out = await outbox.replay(transport);
    assert.equal(out.kind, "fault", shape);
    assert.deepEqual((await rows(sql)).map((r) => r.status), ["pending", "pending"], shape);
  }
});

test("a conflict result closes the operation as conflict, keeps it, and hands the result to the recorder", async () => {
  const seen = [];
  const { outbox, server, transport, sql } = await rig({
    onResult: ({ row, result }) => {
      seen.push([row.key, result.status]);
      return [{ sql: "INSERT INTO sync_state (scope, last_seq, last_pull_at) VALUES (?, 0, NULL)", params: [`marker-${row.key}`] }];
    },
  });
  server.touch("pick_tasks", uuid(1), { picked: 5 }); // another device moved the row past seq 1
  const a = await outbox.enqueue({ scope: "pick_tasks", op: "update", payload: { id: uuid(1), picked: 3 }, baseSeq: 1 });
  const out = await outbox.replay(transport);
  assert.equal(out.kind, "done");
  assert.equal(out.conflicts, 1);
  assert.deepEqual(seen, [[a.key, "conflict"]]);
  assert.deepEqual((await rows(sql)).map((r) => r.status), ["conflict"]);
  assert.deepEqual(await sql.all("SELECT scope FROM sync_state"), [{ scope: `marker-${a.key}` }]);
});

test("the recorder's statements and the status change land together or not at all", async () => {
  const { outbox, server, transport, sql } = await rig({
    onResult: () => [{ sql: "INSERT INTO table_that_does_not_exist (x) VALUES (1)" }],
  });
  await outbox.enqueue(pick(1));
  await outbox.enqueue(pick(2));
  const out = await outbox.replay(transport);
  assert.equal(out.kind, "fault");
  assert.deepEqual((await rows(sql)).map((r) => r.status), ["pending", "pending"]);
  // the server did apply them; a later replay with a working recorder dedups on the keys
  assert.equal(server.appliedOrder.length, 2);
});

test("a stored body that no longer matches its hash is never sent, and does not block the rest", async () => {
  const { outbox, server, transport, sql } = await rig();
  const a = await outbox.enqueue(pick(1));
  const b = await outbox.enqueue(pick(2));
  await sql.run("UPDATE outbox SET payload = ? WHERE idempotency_key = ?", [JSON.stringify({ id: uuid(1), picked: 999 }), a.key]);
  const out = await outbox.replay(transport);
  assert.equal(out.kind, "done");
  assert.deepEqual(server.appliedOrder, [b.key]);
  const got = await rows(sql);
  assert.deepEqual(got.map((r) => r.status), ["rejected", "applied"]);
  assert.match(got[0].error, /body hash/);
});

test("two replays at once never send an operation twice", async () => {
  const { outbox, server, transport } = await rig();
  const keys = [];
  for (let n = 1; n <= 3; n++) keys.push((await outbox.enqueue(pick(n))).key);
  const [x, y] = await Promise.all([outbox.replay(transport), outbox.replay(transport)]);
  assert.equal(x.kind, "done");
  assert.equal(y.kind, "done");
  const sent = server.requests.filter((q) => q.kind === "push").flatMap((q) => q.ops.map((o) => o.client_id));
  assert.deepEqual(sent, keys);
});

test("an empty outbox replays to nothing and sends nothing", async () => {
  const { outbox, server, transport } = await rig();
  const out = await outbox.replay(transport);
  assert.deepEqual(out, { kind: "done", sent: 0, applied: 0, conflicts: 0, rejected: 0 });
  assert.equal(server.requests.length, 0);
});
