// The runner glues the pure machine to the store: it performs the machine's
// effects (replay the outbox, then pull), turns outcomes into events, and
// keeps the conflict state in step with the open cards.
import { test } from "node:test";
import assert from "node:assert/strict";
import { SyncRunner } from "../src/runner.ts";
import { OfflineStore } from "../src/store.ts";
import { createSqliteBackend } from "../src/backend-sqlite.ts";
import * as machine from "../../../../statecharts/sync/machine.ts";
import { err } from "@hardscapehouse/api-client";
import { baseRig, pick, uuid } from "./_rig.mjs";

async function rig() {
  const r = await baseRig();
  const store = await OfflineStore.open({ backend: createSqliteBackend(r.sql), scopes: r.scopes, now: r.now, newKey: r.newKey });
  const runner = new SyncRunner({ store, transport: r.transport, machine });
  const visited = [runner.state];
  runner.onState((s) => visited.push(s));
  return { ...r, store, runner, visited };
}

const pushes = (server) => server.requests.filter((q) => q.kind === "push").length;

test("the runner starts in the machine's initial state and sends nothing", async () => {
  const { runner, server } = await rig();
  assert.equal(runner.state, "offline");
  await runner.request();
  assert.equal(runner.state, "offline");
  assert.equal(server.requests.length, 0);
});

test("going online drains the outbox in order, then pulls, then rests in idle", async () => {
  const { runner, store, server, visited } = await rig();
  const keys = [(await store.enqueue(pick(1))).key, (await store.enqueue(pick(2))).key];
  await runner.dispatch({ type: "ONLINE" });
  assert.deepEqual(visited, ["offline", "pushing", "pulling", "idle"]);
  assert.deepEqual(server.appliedOrder, keys);
  assert.deepEqual(await store.outbox.pending(), []);
  const order = server.requests.map((q) => q.kind);
  assert.deepEqual(order.slice(0, 1), ["push"]);
  assert.ok(order.indexOf("pull") > order.lastIndexOf("push"), "the pull must follow the push");
  assert.equal((await store.list("pick_tasks")).rows.length, 9);
});

test("a push fault returns to idle with the outbox kept, and a later request retries whole", async () => {
  const { runner, store, server } = await rig();
  await store.enqueue(pick(1));
  await store.enqueue(pick(2));
  server.failNext("afterApply", 1);
  await runner.dispatch({ type: "ONLINE" });
  assert.equal(runner.state, "idle");
  assert.equal((await store.outbox.pending()).length, 2);
  await runner.request();
  assert.equal(runner.state, "idle");
  assert.deepEqual(await store.outbox.pending(), []);
  assert.equal(server.appliedOrder.length, 2);
});

test("losing the network during a push is unreachable, and keeps everything", async () => {
  const { runner, store, server } = await rig();
  await store.enqueue(pick(1));
  server.failNext("dropResponse");
  await runner.dispatch({ type: "ONLINE" });
  assert.equal(runner.state, "unreachable");
  assert.equal((await store.outbox.pending()).length, 1);
  await runner.dispatch({ type: "ONLINE" });
  assert.equal(runner.state, "idle");
  assert.equal(server.appliedOrder.length, 1);
  assert.equal(server.applyCount((await store.outbox.list())[0].key), 1);
});

test("a 401 parks the outbox in auth expired until the session is refreshed", async () => {
  const { runner, store, server } = await rig();
  const a = await store.enqueue(pick(1));
  server.setAuthValid(false);
  await runner.dispatch({ type: "ONLINE" });
  assert.equal(runner.state, "auth_expired");
  const seen = server.requests.length;
  await runner.request();
  await runner.dispatch({ type: "ONLINE" });
  assert.equal(runner.state, "auth_expired");
  assert.equal(server.requests.length, seen, "a parked outbox must send nothing");
  const b = await store.enqueue(pick(2));
  assert.ok(b.ok, "writes are still accepted while parked");
  server.setAuthValid(true);
  await runner.dispatch({ type: "SESSION_REFRESHED", online: true });
  assert.equal(runner.state, "idle");
  assert.deepEqual(server.appliedOrder, [a.key, b.key]);
});

test("a cycle that leaves open cards rests in conflict, and clearing the cards returns to idle", async () => {
  const { runner, store, server } = await rig();
  await store.enqueue({ ...pick(1), baseSeq: 1 });
  server.touch("pick_tasks", uuid(1), { n: 100 });
  await runner.dispatch({ type: "ONLINE" });
  assert.equal(runner.state, "conflict");
  const [card] = await store.cards.listOpen();
  await store.cards.dismiss(card.id);
  await runner.settled();
  assert.equal(runner.state, "idle");
});

test("requests made while a cycle runs coalesce into one more cycle", async () => {
  const { runner, store, server } = await rig();
  await runner.dispatch({ type: "ONLINE" });
  const before = pushes(server);
  await store.enqueue(pick(1));
  await Promise.all([runner.request(), runner.request(), runner.request(), runner.request()]);
  assert.equal(runner.state, "idle");
  assert.equal(pushes(server) - before, 1);
});

test("every event the runner emits is in the machine's vocabulary", async () => {
  const { runner, store, server } = await rig();
  const seen = new Set();
  const real = machine.transition;
  const spy = { ...machine, transition: (s, e) => (seen.add(e.type), real(s, e)) };
  const spied = new SyncRunner({ store, transport: server.transport(), machine: spy });
  await store.enqueue(pick(1));
  await spied.dispatch({ type: "ONLINE" });
  server.setOffline(true);
  await store.enqueue(pick(2));
  await spied.request();
  server.setOffline(false);
  server.setAuthValid(false);
  await spied.dispatch({ type: "ONLINE" });
  for (const t of seen) assert.ok(machine.EVENT_TYPES.includes(t), t);
  assert.ok(seen.has("PUSH_DONE") && seen.has("PULL_DONE") && seen.has("NETWORK_LOST") && seen.has("AUTH_EXPIRED"));
  void runner;
});

test("one failed request does not park the runner: a sync request retries with no ONLINE event", async () => {
  const { runner, store, server } = await rig();
  const op = await store.enqueue(pick(1));
  server.failNext("dropResponse");
  await runner.dispatch({ type: "ONLINE" });
  assert.equal(runner.state, "unreachable");
  assert.equal((await store.outbox.pending()).length, 1);
  await runner.request();
  assert.equal(runner.state, "idle", "a retry must leave the failed state by itself");
  assert.deepEqual(await store.outbox.pending(), []);
  assert.equal(server.applyCount(op.key), 1);
});

test("a failed pull is unreachable too, and a request retries it", async () => {
  const { runner, server } = await rig();
  await runner.dispatch({ type: "ONLINE" });
  server.setOffline(true);
  await runner.request();
  assert.equal(runner.state, "unreachable");
  server.setOffline(false);
  await runner.request();
  assert.equal(runner.state, "idle");
});

test("the platform saying offline still parks the runner until it says online", async () => {
  const { runner, server } = await rig();
  await runner.dispatch({ type: "ONLINE" });
  await runner.dispatch({ type: "OFFLINE" });
  const seen = server.requests.length;
  await runner.request();
  assert.equal(runner.state, "offline");
  assert.equal(server.requests.length, seen);
  await runner.dispatch({ type: "ONLINE" });
  assert.equal(runner.state, "idle");
});

test("a push fault still lets the pull run, so reads do not go stale behind a stuck write", async () => {
  const { runner, store, server } = await rig();
  await store.enqueue(pick(1));
  server.failNext("status", 500);
  await runner.dispatch({ type: "ONLINE" });
  assert.equal(runner.state, "idle");
  assert.ok(server.requests.some((q) => q.kind === "pull"), "the pull must run after a push fault");
  assert.equal((await store.outbox.pending()).length, 1);
  assert.equal((await store.list("pick_tasks")).rows.length, 9);
});

test("a request level refusal of one scope does not stall the outbox or the pull", async () => {
  const r = await rig();
  const { store, server } = r;
  const refused = (await store.enqueue(pick(1))).key;
  const short = (await store.enqueue({ scope: "shortages", op: "update", payload: { id: uuid(2), short_by: 1 } })).key;
  const transport = { ...r.transport, push: async (scope, ops) => (scope === "pick_tasks" ? err("scope refused", 403) : r.transport.push(scope, ops)) };
  const runner = new SyncRunner({ store, transport, machine });
  await runner.dispatch({ type: "ONLINE" });
  assert.deepEqual(server.appliedOrder, [short], "the shortage behind the refused pick must be sent");
  assert.ok(server.requests.some((q) => q.kind === "pull"), "the pull must run");
  assert.equal((await store.list("pick_tasks")).rows.length, 9);
  const cards = await store.cards.listOpen();
  assert.deepEqual(cards.map((c) => [c.clientId, c.kind]), [[refused, "rejected"]]);
  assert.match(cards[0].error, /403/);
  assert.equal(runner.state, "conflict", "the card waits for a person");
  assert.deepEqual(await store.outbox.pending(), []);
});

test("a standing push fault rests in idle, and the store's public state says the writes are stuck", async () => {
  const { runner, store, server } = await rig();
  await store.enqueue(pick(1));
  await store.enqueue(pick(2));
  await runner.dispatch({ type: "ONLINE" });
  assert.equal(runner.state, "idle");
  for (let i = 0; i < 3; i++) {
    server.failNext("status", 503);
    await runner.request();
    assert.equal(runner.state, "idle", "the machine rests as before; the stuck signal is the store's");
  }
  const health = await store.outbox.health();
  assert.equal(health.waiting, 2);
  assert.ok(health.stuck.attempts >= 3, `attempts ${health.stuck.attempts}`);
  assert.match(health.stuck.lastError, /503/);
  await runner.request();
  assert.deepEqual(await store.outbox.health(), { waiting: 0, stuck: null, stalls: [] });
});

test("a scope parked by a 403 leaves the runner in idle with the stall visible, and the pull still runs", async () => {
  const { runner, store, server, transport } = await rig();
  await store.enqueue(pick(1));
  await runner.dispatch({ type: "ONLINE" });
  assert.deepEqual(await store.outbox.pending(), []);
  await store.enqueue(pick(2));
  const refusing = { ...transport, push: async (scope, ops) => (scope === "pick_tasks" ? err("no grant", 403) : transport.push(scope, ops)) };
  const parked = new SyncRunner({ store, transport: refusing, machine });
  await parked.dispatch({ type: "ONLINE" });
  assert.equal(parked.state, "idle");
  assert.equal((await store.outbox.pending()).length, 1);
  assert.equal((await store.outbox.health()).stalls[0].kind, "scope_refused");
  assert.ok(server.requests.some((q) => q.kind === "pull"));
});
