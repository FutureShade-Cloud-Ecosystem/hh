// Model tests for the sync statechart and the store under it. A seeded
// generator walks random paths of user actions, connectivity changes, auth
// changes, server moves, injected faults and process restarts, and after
// every step the invariants below are checked. A failure names its seed and
// prints the path, so it replays exactly.
//
// Invariants (the plan's B.3 guarantees 1 to 4, seen from the device):
//   I1 order      the server first applies operations in the order they were enqueued; while a scope is
//                 parked by a refusal, only the order within each scope is promised (the rows of the other
//                 scopes carry on, and the parked scope's rows go out later, in their own order)
//   I2 once       no operation is applied by the server twice
//   I3 conflicts  a closed conflict or rejection always has its card, and a card is only ever dismissed by the user
//   I4 money      nothing is queued for a money scope, and every attempt is refused
//   I5 parked     while auth is expired nothing is sent and nothing leaves the outbox
//   I6 drain      after a reconnect (with a good network and session) the outbox drains, in order, to a rest state
//   I7 honesty    the client calls an operation applied only if the server applied it exactly once, and calls it
//                 conflict or rejected only if the server did not apply it
//   I8 keys       every send of an operation carries the same key and the same body
//   I9 rows       no outbox row is deleted, and a closed row never reopens
//   I10 offline   the runner rests in offline only when the platform said so; a failed request is unreachable
//   I7 and I9 are walked over refusals too: the draws include one shot 400, 403 and 404 answers, and a
//   refusal that follows an attempt the server may have applied. A refusal never closes such a row.
import { test } from "node:test";
import assert from "node:assert/strict";
import { OfflineStore } from "../src/store.ts";
import { SyncRunner } from "../src/runner.ts";
import { createSqliteBackend } from "../src/backend-sqlite.ts";
import { MONEY_SCOPES, scopeBookFor } from "../src/scopes.ts";
import { migrate } from "../src/schema.ts";
import { nodeSqliteExecutor, FakeSyncServer } from "../src/testing.ts";
import * as machine from "../../../../statecharts/sync/machine.ts";
import { uuid, FIELD_APPS } from "./_rig.mjs";

const PATHS = Number(process.env.HH_MODEL_PATHS ?? 120);
const STEPS = Number(process.env.HH_MODEL_STEPS ?? 40);

function mulberry32(seed) {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

async function newWorld(seed) {
  const rng = mulberry32(seed);
  const scopes = scopeBookFor(FIELD_APPS);
  const sql = nodeSqliteExecutor();
  await migrate(sql, scopes.declared());
  const server = new FakeSyncServer();
  for (const scope of ["pick_tasks", "shortages"]) for (let n = 1; n <= 9; n++) server.seed(scope, uuid(n), { n });
  for (let n = 1; n <= 4; n++) server.seed("attachments", uuid(n), { a: 1, b: 1 });
  const world = {
    seed,
    rng,
    sql,
    scopes,
    server,
    trace: [],
    online: false,
    index: new Map(),
    scopeOf: new Map(),
    relaxed: false,
    dismissed: new Set(),
    maxCards: 0,
    prevRows: new Map(),
    keyN: 5000,
    tick: 0,
    store: null,
    runner: null,
  };
  await open(world);
  return world;
}

async function open(world) {
  const now = () => `2000-01-01T00:00:${String(Math.min(59, ++world.tick % 60)).padStart(2, "0")}Z`;
  world.store = await OfflineStore.open({
    backend: createSqliteBackend(world.sql),
    scopes: world.scopes,
    now,
    newKey: () => uuid(++world.keyN),
  });
  world.runner = new SyncRunner({ store: world.store, transport: world.server.transport(), machine });
}

const pickOne = (rng, list) => list[Math.floor(rng() * list.length)];

async function enqueueOk(world, input) {
  const res = await world.store.enqueue(input);
  if (res.ok) {
    if (!res.duplicate) {
      world.index.set(res.key, world.index.size);
      world.scopeOf.set(res.key, input.scope);
    }
  }
  return res;
}

const ACTIONS = [
  [30, "enqueue pick", async (w) => {
    const n = 1 + Math.floor(w.rng() * 9);
    const fresh = w.rng() < 0.6;
    await enqueueOk(w, { scope: "pick_tasks", op: "update", payload: { id: uuid(n), picked: Math.floor(w.rng() * 50) }, baseSeq: fresh ? w.server.row("pick_tasks", uuid(n)).seq : 1 });
  }],
  [8, "enqueue shortage", async (w) => {
    const n = 1 + Math.floor(w.rng() * 9);
    await enqueueOk(w, { scope: "shortages", op: "update", payload: { id: uuid(n), short_by: Math.floor(w.rng() * 9) }, baseSeq: w.rng() < 0.6 ? w.server.row("shortages", uuid(n)).seq : 1 });
  }],
  [6, "enqueue attachment", async (w) => {
    const n = 1 + Math.floor(w.rng() * 4);
    await enqueueOk(w, { scope: "attachments", op: "update", payload: { id: uuid(n), a: Math.floor(w.rng() * 9) }, baseSeq: w.rng() < 0.5 ? w.server.row("attachments", uuid(n)).seq : 1 });
  }],
  [6, "enqueue money", async (w) => {
    const scope = pickOne(w.rng, MONEY_SCOPES);
    const before = (await w.store.outbox.list()).length;
    const res = await w.store.enqueue({ scope, op: "update", payload: { id: uuid(3), amount: 100 }, baseSeq: 1 });
    assert.equal(res.ok, false, `I4: ${scope} was admitted`);
    assert.equal(res.reason, "money_scope", `I4: ${scope} refused for the wrong reason`);
    assert.equal((await w.store.outbox.list()).length, before, "I4: a refused money write left a row");
  }],
  [2, "enqueue undeclared scope", async (w) => {
    const res = await w.store.enqueue({ scope: "products", op: "update", payload: { id: uuid(1) } });
    assert.equal(res.ok, false);
  }],
  [8, "another device moves a row", async (w) => {
    const scope = pickOne(w.rng, ["pick_tasks", "shortages", "attachments"]);
    const n = 1 + Math.floor(w.rng() * (scope === "attachments" ? 4 : 9));
    w.server.touch(scope, uuid(n), { b: Math.floor(w.rng() * 99) });
  }],
  [4, "the module refuses a pending operation", async (w) => {
    const pending = await w.store.outbox.pending();
    if (pending.length) w.server.refuse(pickOne(w.rng, pending).key, "closed");
  }],
  [6, "network lost", async (w) => {
    w.online = false;
    w.server.setOffline(true);
    await w.runner.dispatch({ type: "OFFLINE" });
  }],
  [8, "network back", async (w) => {
    w.online = true;
    w.server.setOffline(false);
    await w.runner.dispatch({ type: "ONLINE" });
  }],
  [12, "sync requested", async (w) => w.runner.request()],
  [8, "arm a server fault", async (w) => {
    const k = Math.floor(w.rng() * 8);
    if (k === 0) w.server.failNext("status", 500);
    else if (k === 1) w.server.failNext("status", 503);
    else if (k === 2) w.server.failNext("afterApply", 1 + Math.floor(w.rng() * 3));
    else if (k === 3) w.server.failNext("dropResponse");
    else if (k === 4) w.server.failNext("malformed", pickOne(w.rng, ["short", "reordered", "foreign"]));
    else {
      const status = pickOne(w.rng, [400, 403, 404]);
      if (status !== 404) w.relaxed = true; // a 400 or 403 may park a scope; a 404 is a fault
      // A 400 is either about the body (invalid_json: split, judged per row) or about the scope or a hop (parks).
      if (status === 400 && w.rng() < 0.5) w.server.failNext("status", 400, "invalid_json");
      else w.server.failNext("status", status);
    }
  }],
  // A refusal on the heels of an attempt the server may have applied: the case a plain draw rarely lines up.
  [5, "an ambiguous attempt, then a refusal", async (w) => {
    if (w.rng() < 0.5) w.server.failNext("afterApply", 1 + Math.floor(w.rng() * 3));
    else w.server.failNext("dropResponse");
    w.relaxed = true;
    const status = pickOne(w.rng, [400, 403, 400]);
    if (status === 400 && w.rng() < 0.7) w.server.failNext("status", 400, "invalid_json");
    else w.server.failNext("status", status);
  }],
  [4, "session expires on the server", async (w) => {
    w.server.setAuthValid(false);
    if (w.rng() < 0.5) await w.runner.dispatch({ type: "AUTH_EXPIRED" });
  }],
  [6, "session refreshed", async (w) => {
    w.server.setAuthValid(true);
    await w.runner.dispatch({ type: "SESSION_REFRESHED", online: w.online });
  }],
  [3, "process restart", async (w) => {
    const before = (await w.store.outbox.pending()).map((r) => [r.key, r.seq]);
    await open(w);
    const after = (await w.store.outbox.pending()).map((r) => [r.key, r.seq]);
    assert.deepEqual(after, before, "I9: the pending outbox changed across a restart");
    if (w.online) await w.runner.dispatch({ type: "ONLINE" });
  }],
  [4, "user dismisses a card", async (w) => {
    const open = await w.store.cards.listOpen();
    if (!open.length) return;
    const card = pickOne(w.rng, open);
    w.dismissed.add(card.id);
    await w.store.cards.dismiss(card.id);
    await w.runner.settled();
  }],
];
const TOTAL = ACTIONS.reduce((s, [w]) => s + w, 0);

function chooseAction(rng) {
  let r = rng() * TOTAL;
  for (const a of ACTIONS) {
    if ((r -= a[0]) < 0) return a;
  }
  return ACTIONS[0];
}

async function quiesce(w) {
  w.trace.push("quiesce");
  w.server.clearFaults();
  w.server.setOffline(false);
  w.server.setAuthValid(true);
  w.online = true;
  // Only the platform's own offline needs the platform's ONLINE. A failed request (unreachable) must
  // recover from a plain request: an ONLINE here used to mask a runner parked after one dropped response.
  if (w.runner.state === "auth_expired") await w.runner.dispatch({ type: "SESSION_REFRESHED", online: true });
  else if (w.runner.state === "offline") await w.runner.dispatch({ type: "ONLINE" });
  for (let i = 0; i < 3 && (await w.store.outbox.pending()).length > 0; i++) await w.runner.request();
  await w.runner.request();
  const pending = await w.store.outbox.pending();
  assert.equal(pending.length, 0, "I6: the outbox did not drain after the reconnect");
  assert.ok(["idle", "conflict"].includes(w.runner.state), `I6: rest state is ${w.runner.state}`);
  const open = await w.store.cards.listOpen();
  assert.equal(w.runner.state === "conflict", open.length > 0, `I6: state ${w.runner.state} with ${open.length} open cards`);
  for (const row of await w.store.outbox.list()) {
    const n = w.server.applyCount(row.key);
    if (row.status === "applied") assert.equal(n, 1, `I7: ${row.key} is applied on the device but ${n} times on the server`);
    else assert.equal(n, 0, `I7: ${row.key} is ${row.status} on the device but the server applied it ${n} times`);
  }
}

async function check(w, pre, action) {
  const { server, store } = w;
  // I1 order: always within a scope; across scopes until a scope has been parked in this walk
  if ((await store.outbox.health()).stalls.length > 0) w.relaxed = true;
  let last = -1;
  const lastIn = new Map();
  for (const key of server.appliedOrder) {
    const i = w.index.get(key);
    assert.notEqual(i, undefined, `I1: the server applied unknown key ${key}`);
    const scope = w.scopeOf.get(key);
    assert.ok(i > (lastIn.get(scope) ?? -1), `I1: ${key} (enqueue ${i}) was applied after a later enqueue of ${scope}`);
    lastIn.set(scope, i);
    if (!w.relaxed) assert.ok(i > last, `I1: ${key} (enqueue ${i}) was applied after enqueue ${last}`);
    last = i;
  }
  // I2 once
  for (const key of server.appliedOrder) assert.equal(server.applyCount(key), 1, `I2: ${key} applied ${server.applyCount(key)} times`);
  const rows = await store.outbox.list();
  const cards = await store.cards.list();
  // I3 conflicts
  const byClient = new Map(cards.map((c) => [c.clientId, c]));
  for (const row of rows) {
    if (row.status === "conflict" || row.status === "rejected") assert.ok(byClient.has(row.key), `I3: ${row.status} row ${row.key} has no card`);
  }
  for (const key of server.conflictedKeys) {
    const row = rows.find((r) => r.key === key);
    if (row && row.status !== "pending") assert.ok(byClient.has(key), `I3: the server's conflict for ${key} was closed without a card`);
  }
  assert.ok(cards.length >= w.maxCards, "I3: a card was deleted");
  w.maxCards = cards.length;
  for (const c of cards) if (c.status === "dismissed") assert.ok(w.dismissed.has(c.id), `I3: card ${c.id} was dismissed by nobody`);
  // I4 money
  for (const row of rows) assert.ok(!MONEY_SCOPES.includes(row.scope), `I4: ${row.scope} is in the outbox`);
  // I5 parked
  if (pre.state === "auth_expired" && !["session refreshed", "process restart"].includes(action)) {
    assert.equal(server.requests.length, pre.requests, `I5: the device sent ${server.requests.length - pre.requests} requests while auth was expired (${action})`);
    for (const [key, status] of pre.rows) assert.equal(rows.find((r) => r.key === key)?.status, status, `I5: ${key} moved while auth was expired`);
  }
  // I7 honesty, at every step: closed as applied means applied once; closed otherwise means never applied
  for (const row of rows) {
    if (row.status === "pending") continue;
    const n = server.applyCount(row.key);
    if (row.status === "applied") assert.equal(n, 1, `I7: ${row.key} is applied on the device but ${n} times on the server (${action})`);
    else assert.equal(n, 0, `I7: ${row.key} is ${row.status} on the device but the server applied it ${n} times (${action})`);
  }
  // A stall always names a pending row that the outbox still holds
  const health = await store.outbox.health();
  for (const stall of health.stalls) {
    const row = rows.find((r) => r.key === stall.key);
    assert.ok(row && row.status === "pending", `I9: stall for ${stall.key} names a row that is not pending (${action})`);
    if (stall.kind === "check_applied") assert.ok(row.sent, `I7: ${stall.key} is a check_applied stall but was never sent`);
  }
  // I8 keys
  const bodies = new Map();
  for (const q of server.requests) {
    if (q.kind !== "push") continue;
    for (const op of q.ops) {
      const text = JSON.stringify(op);
      assert.equal(bodies.get(op.client_id) ?? text, text, `I8: ${op.client_id} was sent with two different bodies`);
      bodies.set(op.client_id, text);
    }
  }
  // I9 rows
  for (const [key, status] of w.prevRows) {
    const now = rows.find((r) => r.key === key);
    assert.ok(now, `I9: outbox row ${key} was deleted`);
    if (status !== "pending") assert.equal(now.status, status, `I9: closed row ${key} changed from ${status} to ${now.status}`);
  }
  w.prevRows = new Map(rows.map((r) => [r.key, r.status]));
  assert.ok(!["pushing", "pulling"].includes(w.runner.state), "the runner stopped mid cycle");
  // I10 offline
  if (w.runner.state === "offline") assert.equal(w.online, false, `I10: the runner is offline but the platform is online (${action})`);
}

async function walk(seed) {
  const w = await newWorld(seed);
  try {
    for (let step = 0; step < STEPS; step++) {
      const [, name, run] = chooseAction(w.rng);
      w.trace.push(`${name} [${w.runner.state}]`);
      const pre = {
        state: w.runner.state,
        requests: w.server.requests.length,
        rows: new Map((await w.store.outbox.list()).map((r) => [r.key, r.status])),
      };
      await run(w);
      await check(w, pre, name);
      if (w.rng() < 0.05) {
        await quiesce(w);
        await check(w, { state: "idle", requests: 0, rows: new Map() }, "quiesce");
      }
    }
    await quiesce(w);
    await check(w, { state: "idle", requests: 0, rows: new Map() }, "quiesce");
  } catch (error) {
    error.message = `seed ${seed}\npath:\n  ${w.trace.join("\n  ")}\n${error.message}`;
    throw error;
  }
}

test(`model: ${PATHS} generated paths of ${STEPS} steps keep every invariant`, { timeout: 600000 }, async () => {
  for (let seed = 1; seed <= PATHS; seed++) await walk(seed);
});

test("model: the generator reaches every state, every outcome kind and every fault", async () => {
  const states = new Set();
  const kinds = new Set();
  const stalls = new Set();
  let restarts = 0;
  for (let seed = 1; seed <= 60; seed++) {
    const w = await newWorld(seed);
    w.runner.onState((s) => states.add(s));
    for (let step = 0; step < STEPS; step++) {
      const [, name, run] = chooseAction(w.rng);
      if (name === "process restart") restarts++;
      await run(w);
      for (const stall of (await w.store.outbox.health()).stalls) stalls.add(stall.kind);
    }
    await quiesce(w);
    for (const c of await w.store.cards.list()) kinds.add(c.kind);
    for (const r of await w.store.outbox.list()) kinds.add(`row:${r.status}`);
  }
  assert.deepEqual([...states].sort(), ["auth_expired", "conflict", "idle", "offline", "pulling", "pushing", "unreachable"]);
  for (const k of ["superseded", "review", "merge_report", "rejected", "row:applied", "row:conflict", "row:rejected"]) assert.ok(kinds.has(k), `never produced ${k}`);
  assert.ok(restarts > 0);
  assert.deepEqual([...stalls].sort(), ["check_applied", "scope_refused"], "the draws must reach both stalls");
  assert.ok(kinds.has("rejected"));
});

test("a reconnect drains offline work in enqueue order across scopes", async () => {
  const w = await newWorld(7);
  const keys = [];
  for (const [scope, n] of [["pick_tasks", 1], ["pick_tasks", 2], ["shortages", 3], ["attachments", 1], ["pick_tasks", 4], ["shortages", 5]]) {
    const input = scope === "attachments" ? { scope, op: "update", payload: { id: uuid(n), a: 1 } } : { scope, op: "update", payload: { id: uuid(n), n: 1 } };
    keys.push((await enqueueOk(w, input)).key);
  }
  await w.runner.dispatch({ type: "ONLINE" });
  assert.deepEqual(w.server.appliedOrder, keys);
  assert.equal(w.runner.state, "idle");
});

test("a lost response for a per field merge costs the merge report, and nothing else (a known gap)", async () => {
  const w = await newWorld(9);
  w.server.touch("attachments", uuid(1), { b: 2 });
  const op = await enqueueOk(w, { scope: "attachments", op: "update", payload: { id: uuid(1), a: 9 }, baseSeq: 1 });
  w.server.failNext("dropResponse");
  await w.runner.dispatch({ type: "ONLINE" });
  await w.runner.request();
  const row = (await w.store.outbox.list()).find((r) => r.key === op.key);
  assert.equal(row.status, "applied");
  assert.equal(w.server.applyCount(op.key), 1);
  assert.equal((await w.store.cards.list()).length, 0, "the engine answers a replay with a plain applied, so no envelope reaches the device");
});
