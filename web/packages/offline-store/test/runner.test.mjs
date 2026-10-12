// The runner glues the pure machine to the store: it performs the machine's
// effects (replay the outbox, then pull), turns outcomes into events, and
// keeps the conflict state in step with the open cards.
import { test } from "node:test";
import assert from "node:assert/strict";
import { SyncRunner } from "../src/runner.ts";
import { OfflineStore } from "../src/store.ts";
import { createSqliteBackend } from "../src/backend-sqlite.ts";
import * as machine from "../../../../statecharts/sync/machine.ts";
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

test("losing the network during a push goes offline and keeps everything", async () => {
  const { runner, store, server } = await rig();
  await store.enqueue(pick(1));
  server.failNext("dropResponse");
  await runner.dispatch({ type: "ONLINE" });
  assert.equal(runner.state, "offline");
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
