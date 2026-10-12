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
import { err } from "@hardscapehouse/api-client";
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
// The round 2 columns: whether a request may have reached the server, and the stall record.
const marks = (sql) => sql.all("SELECT seq, idempotency_key AS k, scope, status, attempts, sent, stall, stall_status, error FROM outbox ORDER BY seq");

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

test("a transport failure with no status is offline: the row stays pending, and is marked as possibly sent", async () => {
  // No status can mean the request never left or that its response was lost after the server applied it;
  // the device cannot tell which, so the attempt counts and the row is treated as sent.
  const { outbox, server, transport, sql } = await rig();
  await outbox.enqueue(pick(1));
  server.setOffline(true);
  const out = await outbox.replay(transport);
  assert.equal(out.kind, "offline");
  assert.deepEqual((await marks(sql)).map((r) => [r.status, r.attempts, r.sent]), [["pending", 1, 1]]);
});

test("a 401 is auth expired and parks the outbox untouched", async () => {
  const { outbox, server, transport, sql } = await rig();
  const a = await outbox.enqueue(pick(1));
  const b = await outbox.enqueue(pick(2));
  server.failNext("status", 401);
  const out = await outbox.replay(transport);
  assert.equal(out.kind, "auth_expired");
  assert.deepEqual((await marks(sql)).map((r) => [r.status, r.attempts, r.sent]), [["pending", 0, 0], ["pending", 0, 0]]);
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

// ---- request level refusals and the byte cap --------------------------------
// The core answers a whole push with a verdict in a few cases (core/internal/sync/handler.go):
// 400 for a body past 1 MiB or an unknown scope, 403 for a caller class that does not fit the
// scope, 501 for a scope not wired. Only these three are deterministic answers of the core on these
// routes (404 and 413 can only come from a proxy in front, so they are faults). A 400 on one
// operation that was never sent before closes it; a 403 or 501 parks the scope without losing work.

const MIB = 1024 * 1024;
const bodyBytes = (ops) => new TextEncoder().encode(JSON.stringify({ ops })).length;
const fat = (n, bytes = 400_000) => pick(n, { note: "x".repeat(bytes) });
const seen = () => {
  const results = [];
  return { results, onResult: ({ row, result }) => (results.push([row.key, result.status, result.error ?? null]), []) };
};

test("a push batch is capped by encoded bytes under 1 MiB, as well as by count", async () => {
  const { outbox, server, transport } = await rig();
  for (let n = 1; n <= 4; n++) await outbox.enqueue(fat(n));
  let refused = 0;
  const limited = { ...transport, push: async (scope, ops) => (bodyBytes(ops) >= MIB ? (refused++, err("invalid_json", 400)) : transport.push(scope, ops)) };
  const out = await outbox.replay(limited);
  assert.equal(refused, 0, "the batch must be cut before it is sent, not rescued by the split");
  assert.deepEqual(out, { kind: "done", sent: 4, applied: 4, conflicts: 0, rejected: 0 });
  for (const q of server.requests) if (q.kind === "push") assert.ok(bodyBytes(q.ops) < MIB, `a push of ${bodyBytes(q.ops)} bytes`);
  assert.ok(server.requests.filter((q) => q.kind === "push").length >= 2, "four 400 KB operations cannot share one request");
});

test("one operation that can never fit in a request is refused at enqueue, not queued to stall", async () => {
  const { outbox, sql } = await rig();
  const res = await outbox.enqueue(fat(1, 2 * MIB));
  assert.equal(res.ok, false);
  assert.equal(res.reason, "invalid");
  assert.match(res.message, /too large/);
  assert.deepEqual(await rows(sql), []);
});

test("a refused batch is split to single operations, and each one lands", async () => {
  const { outbox, server, transport } = await rig();
  const keys = [];
  for (let n = 1; n <= 3; n++) keys.push((await outbox.enqueue(pick(n))).key);
  const refuseMany = { ...transport, push: async (scope, ops) => (ops.length > 1 ? err("invalid_json", 400) : transport.push(scope, ops)) };
  const out = await outbox.replay(refuseMany);
  assert.equal(out.kind, "done");
  assert.equal(out.rejected, 0);
  assert.deepEqual(server.appliedOrder, keys, "order changed across the split");
  assert.deepEqual(await outbox.pending(), []);
});

test("an operation the server still refuses alone is closed as rejected with its card, and the rest carry on", async () => {
  const rec = seen();
  const { outbox, server, transport, sql } = await rig({ onResult: rec.onResult });
  const a = await outbox.enqueue(pick(1));
  const poison = await outbox.enqueue(pick(2));
  const c = await outbox.enqueue(pick(3));
  const d = await outbox.enqueue({ scope: "shortages", op: "update", payload: { id: uuid(4), short_by: 1 } });
  const refusing = { ...transport, push: async (scope, ops) => (ops.some((o) => o.client_id === poison.key) ? err("invalid_json", 400) : transport.push(scope, ops)) };
  const out = await outbox.replay(refusing);
  assert.deepEqual(out, { kind: "done", sent: out.sent, applied: 3, conflicts: 0, rejected: 1 });
  assert.deepEqual((await rows(sql)).map((r) => r.status), ["applied", "rejected", "applied", "applied"]);
  assert.deepEqual(server.appliedOrder, [a.key, c.key, d.key]);
  const refused = rec.results.find(([k]) => k === poison.key);
  assert.equal(refused[1], "rejected");
  assert.match(refused[2], /400/, "the card carries the status of the refusal");
  assert.match(refused[2], /invalid_json/);
  const again = await outbox.replay(refusing);
  assert.deepEqual(again, { kind: "done", sent: 0, applied: 0, conflicts: 0, rejected: 0 });
});

test("an operational fault (500) is still a fault: it stops the replay and marks nothing as rejected", async () => {
  const { outbox, server, transport, sql } = await rig();
  await outbox.enqueue(pick(1));
  server.failNext("status", 500);
  const out = await outbox.replay(transport);
  assert.equal(out.kind, "fault");
  assert.deepEqual((await rows(sql)).map((r) => r.status), ["pending"]);
});

test("a recorder that throws is a fault with the rows counted, never a throw out of replay", async () => {
  const { outbox, sql, transport } = await rig({
    onResult: () => {
      throw new Error("recorder broke");
    },
  });
  await outbox.enqueue(pick(1));
  const out = await outbox.replay(transport);
  assert.equal(out.kind, "fault");
  assert.match(out.message, /recorder broke/);
  assert.deepEqual((await rows(sql)).map((r) => [r.status, r.attempts]), [["pending", 1]]);
});

// ---- round 2: what a refusal may close, the sent flag, parked scopes, the stuck signal ------------

for (const status of [404, 413]) {
  test(`a ${status} comes from a hop in front of the core: it is a fault, every row stays pending, nothing is closed`, async () => {
    const rec = seen();
    const { outbox, server, transport, sql } = await rig({ onResult: rec.onResult });
    await outbox.enqueue(pick(1));
    await outbox.enqueue(pick(2));
    await outbox.enqueue({ scope: "shortages", op: "update", payload: { id: uuid(3), short_by: 2 } });
    server.failNext("status", status);
    const out = await outbox.replay(transport);
    assert.equal(out.kind, "fault");
    assert.equal(out.status, status);
    assert.deepEqual((await rows(sql)).map((r) => r.status), ["pending", "pending", "pending"]);
    assert.deepEqual(rec.results, [], "no card for a fault");
    assert.deepEqual((await outbox.health()).stalls, [], "a fault is the stuck signal, not a stall");
    const again = await outbox.replay(transport);
    assert.equal(again.kind, "done");
    assert.equal(again.applied, 3);
  });
}

for (const status of [403, 501]) {
  test(`a ${status} parks the scope: its rows stay pending with a visible stall, other scopes carry on`, async () => {
    const rec = seen();
    const { outbox, server, transport, sql } = await rig({ onResult: rec.onResult });
    const a = await outbox.enqueue(pick(1));
    const b = await outbox.enqueue(pick(2));
    const short = await outbox.enqueue({ scope: "shortages", op: "update", payload: { id: uuid(3), short_by: 2 } });
    const refusing = { ...transport, push: async (scope, ops) => (scope === "pick_tasks" ? err("no grant for this scope", status) : transport.push(scope, ops)) };
    const out = await outbox.replay(refusing);
    assert.equal(out.kind, "done");
    assert.equal(out.rejected, 0, "a scope wide refusal must not destroy queued work");
    assert.deepEqual(server.appliedOrder, [short.key], "the other scope must be sent");
    assert.deepEqual((await rows(sql)).map((r) => r.status), ["pending", "pending", "applied"]);
    assert.deepEqual(rec.results.filter(([, st]) => st === "rejected"), [], "no card for parked work");
    const pushes = server.requests.filter((q) => q.kind === "push").map((q) => q.scope);
    assert.deepEqual(pushes, ["shortages"], "the refusing scope never reached the server's recorder; one request was enough");
    const { stalls, waiting } = await outbox.health();
    assert.equal(waiting, 2);
    assert.equal(stalls.length, 1);
    assert.deepEqual({ kind: stalls[0].kind, scope: stalls[0].scope, status: stalls[0].status, waiting: stalls[0].waiting }, { kind: "scope_refused", scope: "pick_tasks", status, waiting: 2 });
    assert.match(stalls[0].error, /no grant for this scope/, "the last error is carried");
    assert.match(stalls[0].message, /kept/, "the words say the work is kept");
    // The refusal is not an attempt the server answered about the rows: the sent mark is as it was.
    assert.deepEqual((await marks(sql)).slice(0, 2).map((r) => r.sent), [0, 0]);
    // The grant comes back: the parked rows go out in order, and the stall clears.
    const healed = await outbox.replay(transport);
    assert.equal(healed.kind, "done");
    assert.deepEqual(server.appliedOrder, [short.key, a.key, b.key]);
    assert.deepEqual((await outbox.health()).stalls, []);
    assert.deepEqual((await marks(sql)).map((r) => r.stall), [null, null, null]);
  });
}

test("a parked scope is retried on every replay, not forgotten, and the stall survives a restart", async () => {
  const { outbox, server, transport, sql, scopes, now, newKey } = await rig();
  await outbox.enqueue(pick(1));
  const refusing = { ...transport, push: async () => err("no grant", 403) };
  await outbox.replay(refusing);
  await outbox.replay(refusing);
  assert.equal(server.requests.length, 0);
  const reopened = new Outbox({ sql, scopes, now, newKey });
  const { stalls } = await reopened.health();
  assert.equal(stalls.length, 1);
  assert.equal(stalls[0].kind, "scope_refused");
  assert.equal((await reopened.replay(transport)).kind, "done");
  assert.deepEqual((await reopened.health()).stalls, []);
});

test("a 400 on one operation that was never sent before closes it as rejected with its card", async () => {
  const rec = seen();
  const { outbox, sql, transport } = await rig({ onResult: rec.onResult });
  await outbox.enqueue(pick(1));
  const out = await outbox.replay({ ...transport, push: async () => err("invalid_json", 400) });
  assert.equal(out.rejected, 1);
  assert.deepEqual((await marks(sql)).map((r) => [r.status, r.sent, r.stall]), [["rejected", 0, null]]);
  assert.equal(rec.results.length, 1);
});

test("the sent mark is durable before the first request leaves, in the same atomic step as the attempt count", async () => {
  const { outbox, sql, transport } = await rig();
  await outbox.enqueue(pick(1));
  await outbox.enqueue(pick(2));
  let inside;
  const spying = { ...transport, push: async (scope, ops) => ((inside = await marks(sql)), transport.push(scope, ops)) };
  await outbox.replay(spying);
  assert.deepEqual(inside.map((r) => [r.sent, r.attempts]), [[1, 1], [1, 1]], "the mark must exist while the request is out");
});

test("a mark that cannot be written means nothing is sent", async () => {
  const { sql, transport, scopes, now, newKey } = await rig();
  let failMarks = false;
  const flaky = {
    ...sql,
    atomic: async (statements) => {
      if (failMarks && statements.some((s) => /sent = 1/.test(s.sql))) throw new Error("disk full");
      return sql.atomic(statements);
    },
  };
  const outbox = new Outbox({ sql: flaky, scopes, now, newKey });
  await outbox.enqueue(pick(1));
  failMarks = true;
  let sent = 0;
  const out = await outbox.replay({ ...transport, push: async (scope, ops) => (sent++, transport.push(scope, ops)) });
  assert.equal(out.kind, "fault");
  assert.match(out.message, /disk full/);
  assert.equal(sent, 0, "a request must not leave without its durable mark");
});

for (const status of [400, 403, 501]) {
  test(`a ${status} after an earlier attempt the server may have applied never closes the row as rejected`, async () => {
    const rec = seen();
    const { outbox, server, transport, sql } = await rig({ onResult: rec.onResult });
    const op = await outbox.enqueue(pick(1));
    server.failNext("afterApply", 1); // the server applies it, the answer is a 500
    assert.equal((await outbox.replay(transport)).kind, "fault");
    assert.equal(server.applyCount(op.key), 1);
    const refusal = status === 400 ? "invalid_json" : "refused later";
    const out = await outbox.replay({ ...transport, push: async () => err(refusal, status) });
    assert.equal(out.kind, "done");
    assert.equal(out.rejected, 0);
    assert.deepEqual((await marks(sql)).map((r) => [r.status, r.sent]), [["pending", 1]]);
    assert.deepEqual(rec.results.filter(([, st]) => st === "rejected"), [], "no rejected card for work the server holds");
    const { stalls } = await outbox.health();
    assert.equal(stalls.length, 1);
    assert.equal(stalls[0].kind, status === 400 ? "check_applied" : "scope_refused");
    assert.equal(stalls[0].status, status);
    assert.equal(stalls[0].scope, "pick_tasks");
    assert.match(stalls[0].error, new RegExp(refusal));
    if (status === 400) assert.match(stalls[0].message, /may already have been applied/);
    // The refusal passes: the retry reaches the server, which answers from its record. Applied exactly once.
    const healed = await outbox.replay(transport);
    assert.equal(healed.kind, "done");
    assert.equal(server.applyCount(op.key), 1);
    assert.deepEqual((await rows(sql)).map((r) => r.status), ["applied"]);
    assert.deepEqual((await outbox.health()).stalls, []);
  });
}

test("a check_applied stall holds the rows behind it in its scope, and other scopes carry on", async () => {
  const { outbox, server, transport, sql } = await rig();
  const a = await outbox.enqueue(pick(1));
  const b = await outbox.enqueue(pick(2));
  const s = await outbox.enqueue({ scope: "shortages", op: "update", payload: { id: uuid(3), short_by: 1 } });
  server.failNext("dropResponse"); // all three pushes are separate requests: pick batch first
  assert.equal((await outbox.replay(transport)).kind, "offline");
  const refusingPicks = { ...transport, push: async (scope, ops) => (scope === "pick_tasks" ? err("invalid_json", 400) : transport.push(scope, ops)) };
  const out = await outbox.replay(refusingPicks);
  assert.equal(out.rejected, 0);
  assert.deepEqual((await rows(sql)).map((r) => r.status), ["pending", "pending", "applied"]);
  const { stalls } = await outbox.health();
  assert.deepEqual(stalls.map((x) => [x.kind, x.scope, x.waiting]), [["check_applied", "pick_tasks", 2]]);
  assert.equal(stalls[0].key, a.key, "the stall names the first operation, the one to check");
  assert.equal(server.applyCount(b.key), 1);
  assert.equal(server.applyCount(s.key), 1);
});

test("a refused batch of several that were sent before is split, and each single is judged on its own history", async () => {
  const { outbox, server, transport, sql } = await rig();
  const a = await outbox.enqueue(pick(1));
  server.failNext("dropResponse"); // a is sent, applied, never heard of
  await outbox.replay(transport);
  const b = await outbox.enqueue(pick(2)); // never sent
  const refuseAll = { ...transport, push: async () => err("invalid_json", 400) };
  const before = server.requests.length;
  const out = await outbox.replay(refuseAll);
  // a was sent before: it stalls and holds the scope; b sits behind it and is not sent or closed.
  assert.equal(out.rejected, 0);
  assert.deepEqual((await marks(sql)).map((r) => [r.status, r.sent]), [["pending", 1], ["pending", 0]]);
  assert.equal(server.requests.length, before, "the refusing transport never reached the server");
  const { stalls } = await outbox.health();
  assert.deepEqual(stalls.map((x) => [x.kind, x.key]), [["check_applied", a.key]]);
  assert.equal((await rows(sql))[1].k, b.key);
  // The refusal passes: both go out, a is deduped by the server.
  assert.equal((await outbox.replay(transport)).kind, "done");
  assert.equal(server.applyCount(a.key), 1);
  assert.equal(server.applyCount(b.key), 1);
});

test("the stuck signal: a standing 500 or 503 shows attempts, the last error and the writes waiting", async () => {
  const { outbox, server, transport } = await rig();
  assert.deepEqual(await outbox.health(), { waiting: 0, stuck: null, stalls: [] });
  await outbox.enqueue(pick(1));
  await outbox.enqueue(pick(2));
  for (let i = 0; i < 3; i++) {
    server.failNext("status", i === 1 ? 503 : 500);
    assert.equal((await outbox.replay(transport)).kind, "fault");
  }
  const health = await outbox.health();
  assert.equal(health.waiting, 2);
  assert.equal(health.stuck.attempts, 3);
  assert.match(health.stuck.lastError, /503|500/);
  assert.deepEqual(health.stalls, []);
  assert.equal((await outbox.replay(transport)).kind, "done");
  assert.deepEqual(await outbox.health(), { waiting: 0, stuck: null, stalls: [] });
});

test("a 401 puts the sent mark back: nothing was answered about the rows", async () => {
  const { outbox, server, transport, sql } = await rig();
  await outbox.enqueue(pick(1));
  server.failNext("status", 401);
  assert.equal((await outbox.replay(transport)).kind, "auth_expired");
  assert.deepEqual((await marks(sql)).map((r) => [r.sent, r.attempts]), [[0, 0]]);
});

// ---- round 3: only invalid_json is about the body; any other 400 is about the scope or a hop ----------------

// The core answers a push with 400 for three reasons (core/internal/sync/handler.go): an unknown scope, a pull
// only scope, and invalid_json. A hop in front of the core answers 400 for its own reasons, about every request.
const SCOPE_WIDE_400 = [
  ["the core says the scope is unknown", "unknown sync scope"],
  ["the core says the scope is pull only", "sync: scope pick_tasks is pull-only"],
  ["a hop answers with a plain text body", "Request Header Or Cookie Too Large"],
  ["a hop answers with an HTML body", "<html><body><h1>400 Bad Request</h1></body></html>"],
];

for (const [name, message] of SCOPE_WIDE_400) {
  test(`a 400 where ${name} refuses the scope, not a row: every never sent row stays pending with a scope_refused stall and no card`, async () => {
    const rec = seen();
    const { outbox, server, transport, sql } = await rig({ onResult: rec.onResult });
    const a = await outbox.enqueue(pick(1));
    await outbox.enqueue(pick(2));
    await outbox.enqueue(pick(3));
    const short = await outbox.enqueue({ scope: "shortages", op: "update", payload: { id: uuid(4), short_by: 1 } });
    const pushes = [];
    const refusing = {
      ...transport,
      push: async (scope, ops) => (pushes.push([scope, ops.length]), scope === "pick_tasks" ? err(message, 400) : transport.push(scope, ops)),
    };
    const out = await outbox.replay(refusing);
    assert.equal(out.kind, "done");
    assert.equal(out.rejected, 0, "a scope wide 400 must not destroy queued work");
    assert.deepEqual((await rows(sql)).map((r) => r.status), ["pending", "pending", "pending", "applied"]);
    assert.deepEqual(rec.results.filter(([, st]) => st === "rejected"), [], "no card for work the scope refused");
    assert.deepEqual(pushes[0], ["pick_tasks", 3], "the whole batch went out once");
    assert.equal(pushes.filter(([scope]) => scope === "pick_tasks").length, 1, "a scope wide refusal is not split into singles");
    assert.deepEqual(server.appliedOrder, [short.key]);
    const { stalls, waiting } = await outbox.health();
    assert.equal(waiting, 3);
    assert.deepEqual(stalls.map((x) => [x.kind, x.scope, x.status, x.key, x.waiting]), [["scope_refused", "pick_tasks", 400, a.key, 3]]);
    // The refusal is not an attempt the server answered about the rows: the mark is as it was.
    assert.deepEqual((await marks(sql)).slice(0, 3).map((r) => [r.sent, r.attempts]), [[0, 0], [0, 0], [0, 0]]);
    // It is lifted: the parked rows go out in order and the stall clears.
    const healed = await outbox.replay(transport);
    assert.equal(healed.kind, "done");
    assert.equal(healed.applied, 3);
    assert.deepEqual((await outbox.health()).stalls, []);
  });
}

test("a scope wide 400 on a row that was sent before parks the scope as scope_refused too, not as check_applied", async () => {
  const { outbox, server, transport, sql } = await rig();
  const op = await outbox.enqueue(pick(1));
  server.failNext("dropResponse");
  await outbox.replay(transport);
  const out = await outbox.replay({ ...transport, push: async () => err("unknown sync scope", 400) });
  assert.equal(out.rejected, 0);
  const { stalls } = await outbox.health();
  assert.deepEqual(stalls.map((x) => [x.kind, x.key]), [["scope_refused", op.key]]);
  assert.deepEqual((await marks(sql)).map((r) => [r.status, r.sent]), [["pending", 1]]);
});

test("only a 400 whose message is exactly invalid_json is split and judged row by row", async () => {
  for (const message of ["invalid_json ", "Invalid_JSON", "invalid_json: unexpected end", "error: invalid_json"]) {
    const { outbox, transport, sql } = await rig();
    await outbox.enqueue(pick(1));
    await outbox.enqueue(pick(2));
    const out = await outbox.replay({ ...transport, push: async () => err(message, 400) });
    assert.equal(out.rejected, 0, `${JSON.stringify(message)} closed rows`);
    assert.deepEqual((await rows(sql)).map((r) => r.status), ["pending", "pending"]);
    assert.deepEqual((await outbox.health()).stalls.map((x) => x.kind), ["scope_refused"]);
  }
});

// ---- round 3: the person settles a stalled row --------------------------------------------------------------
//
// settle(key, 'applied' | 'withdrawn' | 'resend') is the one way out of a stall (check_applied or scope_refused).
// The stall UI belongs to item 7; the shell calls this.

/** Two picks and a shortage; the server applied the first pick but its answer was lost; then a 400 invalid_json
 * on the pick scope stalls it as check_applied with the second pick held behind it. */
async function checkAppliedRig(opts = {}) {
  const r = await rig(opts);
  const a = await r.outbox.enqueue(pick(1));
  const b = await r.outbox.enqueue(pick(2));
  const s = await r.outbox.enqueue({ scope: "shortages", op: "update", payload: { id: uuid(3), short_by: 1 } });
  r.server.failNext("afterApply", 1);
  assert.equal((await r.outbox.replay(r.transport)).kind, "fault");
  const refusingPicks = { ...r.transport, push: async (scope, ops) => (scope === "pick_tasks" ? err("invalid_json", 400) : r.transport.push(scope, ops)) };
  await r.outbox.replay(refusingPicks);
  const { stalls } = await r.outbox.health();
  assert.deepEqual(stalls.map((x) => [x.kind, x.key]), [["check_applied", a.key]], "the rig must start from a check_applied stall");
  return { ...r, a, b, s, refusingPicks };
}

const audit = (sql) => sql.all("SELECT idempotency_key AS k, status, sent, stall, stall_status, settled, settled_at, error FROM outbox ORDER BY seq");
const pushedKeys = (server, from = 0) => server.requests.slice(from).filter((q) => q.kind === "push").flatMap((q) => q.ops.map((o) => o.client_id));

test("settle applied: the stalled row is closed as applied by the person, with a card, and is not sent again", async () => {
  const rec = seen();
  const { outbox, server, transport, sql, a, b } = await checkAppliedRig({ onResult: rec.onResult });
  const before = server.requests.length;
  const res = await outbox.settle(a.key, "applied");
  assert.deepEqual(res, { ok: true, key: a.key, choice: "applied" });
  const [row] = await audit(sql);
  assert.deepEqual({ status: row.status, stall: row.stall, stall_status: row.stall_status, settled: row.settled }, { status: "applied", stall: null, stall_status: null, settled: "applied" });
  assert.ok(row.settled_at, "the time of the settlement is kept");
  assert.match(row.error, /person/, "the row says a person closed it");
  const card = rec.results.find(([k]) => k === a.key);
  assert.ok(card, "a card for the settlement");
  assert.equal(card[1], "applied");
  assert.match(card[2], /person/);
  assert.deepEqual((await outbox.health()).stalls, []);
  assert.equal(server.requests.length, before, "settle sends nothing");
  // The rows behind the stall are released, in order, and the settled row is never sent again.
  const out = await outbox.replay(transport);
  assert.equal(out.kind, "done");
  assert.deepEqual(pushedKeys(server, before), [b.key]);
  assert.equal(server.applyCount(a.key), 1);
  assert.deepEqual((await rows(sql)).map((r) => r.status), ["applied", "applied", "applied"]);
});

test("settle withdrawn: the stalled row is closed rejected with a card, and the rows behind it are released", async () => {
  const rec = seen();
  const { outbox, server, transport, sql, a, b } = await checkAppliedRig({ onResult: rec.onResult });
  const before = server.requests.length;
  assert.deepEqual(await outbox.settle(a.key, "withdrawn"), { ok: true, key: a.key, choice: "withdrawn" });
  const [row] = await audit(sql);
  assert.deepEqual({ status: row.status, stall: row.stall, settled: row.settled }, { status: "rejected", stall: null, settled: "withdrawn" });
  assert.ok(row.settled_at);
  assert.match(row.error, /withdrawn/);
  const card = rec.results.find(([k]) => k === a.key);
  assert.equal(card[1], "rejected");
  assert.match(card[2], /withdrawn/);
  assert.deepEqual((await outbox.health()).stalls, []);
  assert.equal((await outbox.replay(transport)).kind, "done");
  assert.deepEqual(pushedKeys(server, before), [b.key], "the withdrawn row is not sent");
});

test("settle resend: the row is sent again as a new attempt, ahead of the rows behind it, and the server dedups", async () => {
  const { outbox, server, transport, sql, a, b } = await checkAppliedRig();
  const before = server.requests.length;
  assert.deepEqual(await outbox.settle(a.key, "resend"), { ok: true, key: a.key, choice: "resend" });
  const [row] = await audit(sql);
  assert.deepEqual({ status: row.status, sent: row.sent, stall: row.stall, stall_status: row.stall_status, settled: row.settled }, { status: "pending", sent: 0, stall: null, stall_status: null, settled: "resend" });
  assert.ok(row.settled_at);
  assert.deepEqual((await outbox.health()).stalls, []);
  assert.equal(server.requests.length, before, "settle sends nothing; the next replay does");
  const out = await outbox.replay(transport);
  assert.equal(out.kind, "done");
  assert.deepEqual(pushedKeys(server, before), [a.key, b.key], "in order, the resent row first");
  assert.equal(server.applyCount(a.key), 1, "the same key: the server answers from its record, nothing applied twice");
  assert.deepEqual((await rows(sql)).map((r) => r.status), ["applied", "applied", "applied"]);
});

test("a resent row that is refused again is judged as a row never sent: a 400 invalid_json closes it rejected with its card", async () => {
  const rec = seen();
  const { outbox, sql, a, refusingPicks } = await checkAppliedRig({ onResult: rec.onResult });
  await outbox.settle(a.key, "resend");
  const out = await outbox.replay(refusingPicks);
  assert.equal(out.rejected, 2, "the resent row and the one behind it, each refused alone, neither sent before");
  assert.deepEqual((await rows(sql)).map((r) => r.status).slice(0, 2), ["rejected", "rejected"]);
});

test("settle on a scope_refused stall: withdrawn closes the row with a card, resend clears the park", async () => {
  const rec = seen();
  const { outbox, server, transport, sql } = await rig({ onResult: rec.onResult });
  const a = await outbox.enqueue(pick(1));
  const b = await outbox.enqueue(pick(2));
  const refusing = { ...transport, push: async () => err("no grant", 403) };
  await outbox.replay(refusing);
  assert.deepEqual((await outbox.health()).stalls.map((x) => [x.kind, x.key]), [["scope_refused", a.key]]);
  assert.equal((await outbox.settle(a.key, "withdrawn")).ok, true);
  assert.deepEqual((await rows(sql)).map((r) => r.status), ["rejected", "pending"]);
  assert.deepEqual((await outbox.health()).stalls, []);
  assert.equal((await outbox.replay(transport)).kind, "done");
  assert.deepEqual(server.appliedOrder, [b.key]);
  assert.equal(server.applyCount(a.key), 0);
});

test("settle cannot be called on a row that is not stalled, and changes nothing when refused", async () => {
  const rec = seen();
  const { outbox, server, transport, sql } = await rig({ onResult: rec.onResult });
  const pending = await outbox.enqueue(pick(1));
  const done = await outbox.enqueue(pick(2));
  server.failNext("status", 500);
  await outbox.replay(transport); // a fault: pending, attempted, no stall
  const first = await outbox.replay(transport);
  assert.equal(first.kind, "done");
  const later = await outbox.enqueue(pick(3));
  const snapshot = await audit(sql);
  const cards = rec.results.length;
  for (const choice of ["applied", "withdrawn", "resend"]) {
    const onPending = await outbox.settle(later.key, choice);
    assert.equal(onPending.ok, false);
    assert.equal(onPending.reason, "not_stalled");
    const onClosed = await outbox.settle(done.key, choice);
    assert.equal(onClosed.ok, false);
    assert.equal(onClosed.reason, "not_stalled");
    const unknown = await outbox.settle(uuid(999), choice);
    assert.equal(unknown.ok, false);
    assert.equal(unknown.reason, "unknown_key");
  }
  assert.equal((await outbox.settle(later.key, "bogus")).reason, "invalid_choice");
  assert.deepEqual(await audit(sql), snapshot, "a refused settle leaves every row as it was");
  assert.equal(rec.results.length, cards, "and makes no card");
  assert.ok(pending.ok);
});

test("a row can be settled once: the second call finds it no longer stalled", async () => {
  const { outbox, a } = await checkAppliedRig();
  assert.equal((await outbox.settle(a.key, "applied")).ok, true);
  const again = await outbox.settle(a.key, "applied");
  assert.equal(again.ok, false);
  assert.equal(again.reason, "not_stalled");
});

test("settle waits for a replay in flight: it never changes a row while its request is out", async () => {
  const { outbox, server, transport, a, b } = await checkAppliedRig();
  const events = [];
  let release;
  const gate = new Promise((resolve) => (release = resolve));
  const slow = {
    ...transport,
    push: async (scope, ops) => (events.push("push"), await gate, transport.push(scope, ops)),
  };
  const replaying = outbox.replay(slow); // the shortages row is not pending; the stalled pick scope is retried
  await new Promise((resolve) => setTimeout(resolve, 20));
  const settling = outbox.settle(a.key, "withdrawn").then((res) => (events.push("settled"), res));
  await new Promise((resolve) => setTimeout(resolve, 20));
  assert.deepEqual(events, ["push"], "settle must wait for the replay");
  release();
  await replaying;
  const res = await settling;
  assert.deepEqual(events, ["push", "settled"]);
  // The replay's own answer decided the row first (it applied a deduped and b); the settlement then found it closed.
  assert.equal(res.ok, false);
  assert.equal(res.reason, "not_stalled");
  assert.equal(server.applyCount(b.key), 1);
});
