// Yard Pick's offline data layer, over the store's node:sqlite executor and
// the fake sync server (the Go engine's order of decisions). The layer is the
// app's api port: reads compose from the store, writes enqueue and patch the
// local projection, a cycle pushes the outbox and pulls the scopes back, and
// every conflict comes back as a card the layer reads for the apps.
//
// These cases are the red first proof for the data layer: they were written
// against the app before yard-offline.ts existed and failed on the missing
// module, then passed on the implementation.
import { test } from "node:test";
import assert from "node:assert/strict";

import { nodeSqliteExecutor, FakeSyncServer } from "@hardscapehouse/offline-store/testing";
import { OfflineStore, createSqliteBackend, scopeBookFor } from "@hardscapehouse/offline-store";
import { YardOffline, bannerMode } from "../src/yard-offline.js";
import { payloadOffenders } from "./_lib/gosets.mjs";

const ACTOR = "d0200000-0000-4000-9000-000000000002";
const BRANCH = "5d1b3f6e-0000-4000-9000-000000000001";
const TASK = "0a000000-0000-4000-9000-000000000042";
const TICKET = "0b000000-0000-4000-9000-000000000042";

let keyCounter = 0;
function nextKey() {
  keyCounter += 1;
  return `00000000-0000-4000-8000-${String(keyCounter).padStart(12, "0")}`;
}

/** Back online. Every write fires a background sync it does not await, and one
 * that started while the fake server was offline can reach its push after the
 * flip and send a lone write ahead of the explicit cycle. Let those rest first,
 * so a test counting pushes sees the queue go out together. */
async function online(layer, server) {
  await layer.runner.settled();
  server.setOffline(false);
}

async function rig({ wrapTransport, ...extra } = {}) {
  const server = new FakeSyncServer();
  // The world, in the projection the demo sync surface serves: the task rows
  // carry the task and the ticket words; the shortage rows carry the shortage.
  server.seed("pick_tasks", TASK, {
    id: TASK,
    order_id: TICKET,
    branch_id: BRANCH,
    task_type: "PICK",
    priority: "URGENT",
    status: "QUEUED",
    claimed_by: "",
    created_at: "2026-10-08T13:00:00Z",
    updated_at: "2026-10-08T13:00:00Z",
    open_shortages: 0,
    total_shortages: 0,
    stage_location: "",
    stage_ref: "",
    ticket: {
      order_id: TICKET,
      order_no: "SO-1042",
      customer_name: "Northline Hardscapes Ltd (demo)",
      fulfillment: "PICKUP",
      mto_class: "STANDARD",
      priority: "URGENT",
      reference: "SO-1042",
      source: "DISPATCH",
      held_from_picking: false,
      hold_words: "",
      lines: [
        { line_no: 1, product_id: "p1", sku: "PAVER-CH-1212", description: "Charcoal pavers, 12 by 12 in", qty_ordered: "24.0000", qty_backordered: "", uom: "EA", location: "Yard A" },
        { line_no: 2, product_id: "p2", sku: "SAND-POLY-G", description: "Polymeric sand, grey, 18 kg", qty_ordered: "6.0000", qty_backordered: "", uom: "BAG", location: "Rack 3" },
      ],
    },
    picked: [],
  });
  const executor = nodeSqliteExecutor();
  const backend = createSqliteBackend(executor);
  const store = await OfflineStore.open({ backend, scopes: scopeBookFor(["yard-pick"]) });
  const layer = new YardOffline({
    store,
    sql: backend.sql,
    transport: wrapTransport ? wrapTransport(server.transport()) : server.transport(),
    actorId: ACTOR,
    newKey: nextKey,
    ...extra,
  });
  await layer.cycle();
  return { layer, server, store, backend };
}

test("the first cycle pulls the world, and the queue reads answer from the store with staleness", async () => {
  const { layer } = await rig();
  const answer = await layer.queue(BRANCH);
  assert.equal(answer.ok, true);
  if (!answer.ok) return;
  assert.equal(answer.value.picks.length, 1);
  const pick = answer.value.picks[0];
  assert.equal(pick.task.id, TASK);
  assert.equal(pick.ticket.order_no, "SO-1042");
  assert.equal(answer.staleness.neverPulled, false);
  assert.ok((answer.staleness.ageMs ?? 0) < 60_000, "the staleness is fresh after a pull");
});

test("a pick composes the task, its ticket and its shortages from the store", async () => {
  const { layer } = await rig();
  const answer = await layer.pick(TASK);
  assert.equal(answer.ok, true);
  if (!answer.ok) return;
  assert.equal(answer.value.task.id, TASK);
  assert.equal(answer.value.ticket.lines.length, 2);
  assert.equal(answer.value.shortages.length, 0);
  const missing = await layer.pick("0a000000-0000-4000-9000-000000009999");
  assert.equal(missing.ok, false);
  if (missing.ok) return;
  assert.match(missing.error, /not found/);
});

test("offline, a claim and a start stay local, then drain in order", async () => {
  const { layer, server } = await rig();
  server.setOffline(true);
  const claimed = await layer.claim(TASK);
  assert.equal(claimed.ok, true);
  const started = await layer.start(TASK);
  assert.equal(started.ok, true);
  // The reads answer from the local projection while offline.
  const read = await layer.pick(TASK);
  assert.equal(read.ok, true);
  if (!read.ok) return;
  assert.equal(read.value.task.status, "IN_PROGRESS");
  assert.equal(read.value.task.claimed_by, ACTOR);
  assert.equal(await layer.waiting(), 2);
  // Back online: the outbox drains, in enqueue order, and the reads keep the truth.
  await online(layer, server);
  await layer.cycle();
  assert.equal(await layer.waiting(), 0);
  assert.equal(server.appliedOrder.length, 2);
  const after = await layer.pick(TASK);
  if (!after.ok) return assert.fail("the pick should read back");
  assert.equal(after.value.task.status, "IN_PROGRESS");
  const pushes = server.requests.filter((r) => r.kind === "push");
  assert.equal(pushes.length, 1);
  assert.equal(pushes[0].scope, "pick_tasks");
  assert.equal(pushes[0].ops[0].op, "update");
  assert.equal(pushes[0].ops[1].op, "update");
  // The chained write on the same row goes blind: the only stale base the
  // device could name is its own first write.
  assert.equal(pushes[0].ops[0].base_seq !== undefined, true);
  assert.equal(pushes[0].ops[1].base_seq === undefined, true);
});

test("a background sync and an explicit cycle that overlap push each queued write once", async () => {
  // Hold the server's answer to a push so the background sync a write fires
  // is still in flight when the explicit cycle starts.
  let held = false;
  let open = () => {};
  let arrived = () => {};
  const arrival = new Promise((resolve) => (arrived = resolve));
  const gate = new Promise((resolve) => (open = resolve));
  const { layer, server } = await rig({
    wrapTransport: (inner) => ({
      pull: (...args) => inner.pull(...args),
      push: async (...args) => {
        const answer = await inner.push(...args);
        if (held) {
          held = false;
          arrived();
          await gate;
        }
        return answer;
      },
    }),
  });
  held = true;
  const claimed = await layer.claim(TASK);
  assert.equal(claimed.ok, true);
  await arrival; // the background sync's push is at the server, unanswered
  const explicit = layer.cycle();
  open();
  await explicit;
  await layer.cycle();
  assert.equal(await layer.waiting(), 0);
  const pushes = server.requests.filter((r) => r.kind === "push");
  const sent = pushes.flatMap((p) => p.ops).filter((o) => o.payload.id === TASK);
  assert.equal(sent.length, 1, "the claim reaches the server in one push, not two");
});

test("a shortage reported offline lands, and a correction the server moved first waits as a review card", async () => {
  const { layer, server, store } = await rig();
  // Offline: the picker reports the shortage.
  server.setOffline(true);
  const reported = await layer.reportShortage(TASK, {
    orderLineNo: 2,
    description: "Polymeric sand, grey, 18 kg",
    qtyShort: "4",
    uom: "BAG",
    note: "Split bags at the rack",
  });
  assert.equal(reported.ok, true);
  await online(layer, server);
  await layer.cycle();
  assert.equal(await layer.waiting(), 0);
  // The shortage is in the store now, with the server's seq.
  const first = await layer.pick(TASK);
  if (!first.ok) return assert.fail("the pick should read back");
  assert.equal(first.value.shortages.length, 1);
  const shortageId = first.value.shortages[0].id;
  // A correctable leg (the core's shortage set admits a note) queued against
  // the pulled seq, while the desk moves the row first.
  const row = await store.read("shortages", shortageId);
  server.setOffline(true);
  const corrected = await layer.enqueueRaw({
    scope: "shortages",
    op: "update",
    entity: "shortages",
    payload: { id: shortageId, note: "Four bags split, not three" },
    baseSeq: row.row.seq,
  });
  assert.equal(corrected.ok, true);
  server.touch("shortages", shortageId, { qty_short: "5.0000" });
  await online(layer, server);
  await layer.cycle();
  // The stale correction never merges on its own: a review card waits.
  const cards = await layer.openCards();
  const review = cards.find((c) => c.kind === "review");
  assert.ok(review !== undefined, "a review card waits for the person");
  assert.equal(review.scope, "shortages");
  assert.equal(review.policy, "manual");
  assert.match(review.clientPayload.note, /Four bags split/);
  // Dismiss is the only exit, and the card stays dismissed, never deleted.
  await layer.dismissCard(review.id);
  assert.equal((await layer.openCards()).length, 0);
  const all = await store.cards.list();
  assert.equal(all.length, 1);
  assert.equal(all[0].status, "dismissed");
});

test("a queued pick the server reassigned first comes back superseded, and the server row is what reads show", async () => {
  const { layer, server, store } = await rig();
  server.setOffline(true);
  await layer.claim(TASK);
  // The server reassigns the ticket while the claim waits in the outbox.
  server.touch("pick_tasks", TASK, { status: "CANCELLED", claimed_by: "dispatch" });
  await online(layer, server);
  await layer.cycle();
  const cards = await layer.openCards();
  const superseded = cards.find((c) => c.kind === "superseded");
  assert.ok(superseded !== undefined, "the server wins card shows");
  assert.equal(superseded.scope, "pick_tasks");
  // The server row is what a read shows now.
  const read = await layer.pick(TASK);
  if (!read.ok) return assert.fail("the pick should read back");
  assert.equal(read.value.task.status, "CANCELLED");
  assert.equal(await layer.waiting(), 0);
});

test("a chained write goes blind even when the earlier op drained before the pull landed", async () => {
  // The pull for pick tasks is delayed a beat once the device has pulled its
  // first world: the window between a push applying and the pull landing is
  // where a stale base could sneak in and drop the second move as a false
  // server wins conflict.
  const server = new FakeSyncServer();
  server.seed("pick_tasks", TASK, {
    id: TASK, order_id: TICKET, branch_id: BRANCH, task_type: "PICK", priority: "URGENT", status: "QUEUED",
    claimed_by: "", created_at: "2026-10-08T13:00:00Z", updated_at: "2026-10-08T13:00:00Z",
    open_shortages: 0, total_shortages: 0, stage_location: "", stage_ref: "",
    ticket: {
      order_id: TICKET, order_no: "SO-1042", customer_name: "Northline Hardscapes Ltd (demo)", fulfillment: "PICKUP",
      mto_class: "STANDARD", priority: "URGENT", reference: "SO-1042", source: "DISPATCH",
      held_from_picking: false, hold_words: "",
      lines: [{ line_no: 1, product_id: "p1", sku: "PAVER-CH-1212", description: "Charcoal pavers, 12 by 12 in", qty_ordered: "24.0000", qty_backordered: "", uom: "EA", location: "Yard A" }],
    },
    picked: [],
  });
  const backend = createSqliteBackend(nodeSqliteExecutor());
  const store = await OfflineStore.open({ backend, scopes: scopeBookFor(["yard-pick"]) });
  const inner = server.transport();
  const delayedTransport = {
    push: (scope, ops) => inner.push(scope, ops),
    pull: (scope, since, limit) => {
      if (scope === "pick_tasks" && since > 0) {
        return new Promise((resolve) => setTimeout(() => resolve(inner.pull(scope, since, limit)), 120));
      }
      return inner.pull(scope, since, limit);
    },
  };
  const layer = new YardOffline({ store, sql: backend.sql, transport: delayedTransport, actorId: ACTOR, newKey: nextKey });
  await layer.cycle();
  await layer.claim(TASK);
  for (let waited = 0; server.appliedOrder.length < 1 && waited < 5_000; waited += 50) {
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  assert.equal(server.appliedOrder.length, 1, "the claim applied");
  // The pull is still delayed: the local row's seq is stale now. The start
  // must go blind, never name the stale base.
  await layer.start(TASK);
  await layer.cycle();
  const cards = await layer.openCards();
  assert.equal(cards.length, 0, "no conflict: the chained write went blind");
  assert.equal(await layer.waiting(), 0);
});

test("a money scope is refused at the door, before anything is stored", async () => {
  const { layer, store } = await rig();
  const refused = await layer.enqueueRaw({ scope: "price_matrix", op: "update", payload: { id: TASK } });
  assert.equal(refused.ok, false);
  assert.equal(await layer.waiting(), 0);
  assert.equal((await store.outbox.list()).length, 0);
});

test("the banner mode maps the runner state and the ownership onto the chrome's modes", () => {
  assert.equal(bannerMode("idle", "owner"), "");
  assert.equal(bannerMode("pushing", "owner"), "");
  assert.equal(bannerMode("pulling", "owner"), "");
  assert.equal(bannerMode("conflict", "owner"), "");
  assert.equal(bannerMode("offline", "owner"), "offline");
  assert.equal(bannerMode("unreachable", "owner"), "unreachable");
  assert.equal(bannerMode("auth_expired", "owner"), "auth-expired");
  assert.equal(bannerMode("idle", "read_cache_only"), "read-only");
  assert.equal(bannerMode("offline", "read_cache_only"), "read-only");
});

test("a second tab (read cache only) opens no layer, so the app stays online, and says it is read only", async () => {
  const { openYardOfflineOver } = await import("../src/yard-offline.js");
  const executor = nodeSqliteExecutor();
  const backend = { ...createSqliteBackend(executor), ownership: () => "read_cache_only" };
  const server = new FakeSyncServer();
  const opened = await openYardOfflineOver(backend, { transport: server.transport(), direct: {}, actorId: ACTOR });
  assert.equal(opened.layer, null, "no layer: the empty read cache must not stand in for the board");
  assert.equal(opened.readOnly, true, "the strip still says read only");
  const owner = await openYardOfflineOver(createSqliteBackend(nodeSqliteExecutor()), { transport: server.transport(), direct: {}, actorId: ACTOR });
  assert.equal(owner.readOnly, false);
  assert.notEqual(owner.layer, null);
});

test("every queued write declares only fields the core's sync stores admit (the Go writable sets)", async () => {
  const { layer, server } = await rig();
  server.setOffline(true);
  await layer.claim(TASK);
  await layer.start(TASK);
  await layer.stage(TASK, "Bay 4", "");
  const reported = await layer.reportShortage(TASK, { orderLineNo: 2, description: "Polymeric sand, grey, 18 kg", qtyShort: "4", uom: "BAG", note: "Split bags" });
  assert.equal(reported.ok, true);
  await online(layer, server);
  await layer.cycle();
  assert.equal(await layer.waiting(), 0);
  assert.deepEqual(payloadOffenders(server.requests), []);
});

test("the skid count and the shortage settle never queue: they go to their direct doors, in order, after the outbox", async () => {
  const calls = [];
  const direct = {
    recordSkids: async (id, count) => (calls.push(["recordSkids", id, count]), { ok: true, value: { skid_count: count, previous_skid_count: null, changed: true, order_no: "SO-1042" } }),
    resolveShortage: async (id, shortageId) => (calls.push(["resolveShortage", shortageId]), { ok: true, value: { id: shortageId, resolution: "done" } }),
  };
  const { layer, server } = await rig({ direct });
  await layer.claim(TASK);
  await layer.runner.settled();
  await layer.recordSkids(TASK, 3, "COUNT_CORRECTION", "");
  await layer.resolveShortage(TASK, "3a000000-0000-4000-9000-000000000001", "done");
  assert.deepEqual(calls.map((c) => c[0]), ["recordSkids", "resolveShortage"]);
  assert.equal(await layer.waiting(), 0, "nothing was queued for them");
  const pushedScopes = server.requests.filter((r) => r.kind === "push").map((r) => r.scope);
  assert.deepEqual([...new Set(pushedScopes)], ["pick_tasks"]);
});

test("a direct door refuses in words while a write is still waiting, and says no signal when the platform is offline", async () => {
  const calls = [];
  const direct = {
    recordSkids: async () => (calls.push("recordSkids"), { ok: false, error: "x", status: 400 }),
    resolveShortage: async () => (calls.push("resolveShortage"), { ok: false, error: "x", status: 400 }),
  };
  const { layer, server } = await rig({ direct });
  server.setOffline(true);
  await layer.claim(TASK);
  const waiting = await layer.recordSkids(TASK, 4, "COUNT_CORRECTION", "");
  assert.equal(waiting.ok, false);
  if (waiting.ok) return;
  assert.match(waiting.error, /still waiting/);
  await online(layer, server);
  await layer.setOnline(false);
  const nosignal = await layer.recordSkids(TASK, 2, "COUNT_CORRECTION", "");
  assert.equal(nosignal.ok, false);
  if (nosignal.ok) return;
  assert.equal(nosignal.status, undefined, "no answer from a server: the app's own check your signal words show");
  assert.deepEqual(calls, [], "no direct door was reached");
});

test("a write made while an earlier request settles keeps its mark, so a third write still goes blind", async () => {
  const { layer: _unused, server } = await rig();
  const backend = createSqliteBackend(nodeSqliteExecutor());
  const store = await OfflineStore.open({ backend, scopes: scopeBookFor(["yard-pick"]) });
  const inner = server.transport();
  const delayedTransport = {
    push: (scope, ops) => inner.push(scope, ops),
    pull: (scope, since, limit) =>
      scope === "pick_tasks" && since > 0
        ? new Promise((resolve) => setTimeout(() => resolve(inner.pull(scope, since, limit)), 150))
        : inner.pull(scope, since, limit),
  };
  const layer = new YardOffline({ store, sql: backend.sql, transport: delayedTransport, direct: {}, actorId: ACTOR, newKey: nextKey });
  await layer.cycle();
  const until = async (done) => {
    for (let waited = 0; !done() && waited < 5_000; waited += 10) await new Promise((r) => setTimeout(r, 10));
    assert.equal(done(), true, "the server reached the awaited state");
  };
  const before = server.appliedOrder.length;
  await layer.claim(TASK);
  await until(() => server.appliedOrder.length === before + 1);
  await layer.start(TASK);
  await until(() => server.appliedOrder.length === before + 2);
  await layer.stage(TASK, "Bay 4", "");
  await layer.runner.settled();
  await layer.runner.settled();
  const staged = server.requests.filter((r) => r.kind === "push").flatMap((r) => r.ops).find((op) => op.payload.status === "STAGED");
  assert.equal(staged.base_seq, undefined, "the third write went blind");
  assert.equal((await layer.openCards()).length, 0, "no false conflict against the device's own move");
});

test("a write that read its row before a cycle's pull landed does not name the stale seq as its base", async () => {
  const { server } = await rig();
  const backend = createSqliteBackend(nodeSqliteExecutor());
  const store = await OfflineStore.open({ backend, scopes: scopeBookFor(["yard-pick"]) });
  const inner = server.transport();
  const delayedTransport = {
    push: (scope, ops) => inner.push(scope, ops),
    pull: (scope, since, limit) =>
      scope === "pick_tasks" && since > 0
        ? new Promise((resolve) => setTimeout(() => resolve(inner.pull(scope, since, limit)), 100))
        : inner.pull(scope, since, limit),
  };
  const layer = new YardOffline({ store, sql: backend.sql, transport: delayedTransport, direct: {}, actorId: ACTOR, newKey: nextKey });
  await layer.cycle();
  const before = server.appliedOrder.length;
  await layer.claim(TASK);
  for (let waited = 0; server.appliedOrder.length < before + 1 && waited < 5_000; waited += 10) await new Promise((r) => setTimeout(r, 10));
  assert.equal(server.appliedOrder.length, before + 1, "the claim applied");
  let release;
  const gate = new Promise((resolve) => (release = resolve));
  const read = store.read.bind(store);
  store.read = async (scope, id) => {
    const out = await read(scope, id);
    if (scope === "pick_tasks") await gate;
    return out;
  };
  const second = layer.start(TASK);
  await layer.runner.settled();
  store.read = read;
  release();
  await second;
  await layer.runner.settled();
  await layer.runner.settled();
  assert.equal(server.appliedOrder.length, before + 2, "the start applied, never superseded by the device's own claim");
  assert.equal((await layer.openCards()).length, 0);
});

// ---- the pick record through the outbox (item 6c) ----

const LEGS = ["order_line_no", "product_id", "sku", "description", "qty_ordered", "qty_picked", "uom", "short_reason", "short_note"];

function recordLine(over = {}) {
  return {
    order_line_no: 1,
    product_id: "p1",
    sku: "PAVER-CH-1212",
    description: "Charcoal pavers, 12 by 12 in",
    qty_ordered: "24.0000",
    qty_picked: "24",
    uom: "EA",
    short_reason: "",
    short_note: "",
    ...over,
  };
}

const SHORT = recordLine({ order_line_no: 2, sku: "SAND-POLY-G", description: "Polymeric sand, grey, 18 kg", qty_ordered: "6.0000", qty_picked: "4", uom: "BAG", short_reason: "NOT_IN_YARD", short_note: "Two bags short on the rack" });

test("offline, the pick record queues as its own op with exactly the door's line legs, and the local row shows it", async () => {
  const { layer, server } = await rig();
  server.setOffline(true);
  // A caller that hands over more than the door's legs (the local row's own ids and stamps) is stripped to the legs.
  const noisy = { ...recordLine(), id: "local-1", task_id: TASK, picked_at: "2026-10-08T14:00:00Z" };
  const recorded = await layer.recordLines(TASK, [noisy, SHORT]);
  assert.equal(recorded.ok, true, JSON.stringify(recorded));
  assert.equal(await layer.waiting(), 1);
  const [queued] = await layer.store.outbox.pending();
  assert.equal(queued.scope, "pick_tasks");
  assert.equal(queued.op, "update");
  assert.deepEqual(Object.keys(queued.payload).sort(), ["id", "picked"], "the record travels with the row address and nothing else");
  assert.equal(queued.payload.id, TASK);
  for (const line of queued.payload.picked) assert.deepEqual(Object.keys(line).sort(), [...LEGS].sort());
  assert.equal(typeof queued.payload.picked[0].order_line_no, "number");
  // The local optimistic row: the pick reads back with the recorded lines.
  const read = await layer.pick(TASK);
  if (!read.ok) return assert.fail("the pick should read back");
  assert.equal(read.value.picked.length, 2);
  assert.equal(read.value.picked[1].qty_picked, "4");
  assert.equal(read.value.picked[1].short_reason, "NOT_IN_YARD");
  assert.equal(await layer.pendingRecord(TASK), true, "the pick shows its record as waiting to send");
});

test("the record drains in order behind the claim and the start, lands, and the pending state clears", async () => {
  const { layer, server } = await rig();
  server.setOffline(true);
  await layer.claim(TASK);
  await layer.start(TASK);
  await layer.recordLines(TASK, [recordLine(), SHORT]);
  const shortage = await layer.reportShortage(TASK, { orderLineNo: 2, description: "Polymeric sand, grey, 18 kg", qtyShort: "2", uom: "BAG", note: "" });
  assert.equal(shortage.ok, true);
  assert.equal(await layer.waiting(), 4);
  await online(layer, server);
  await layer.cycle();
  assert.equal(await layer.waiting(), 0);
  assert.equal(server.appliedOrder.length, 4, "all four writes applied");
  const pushes = server.requests.filter((r) => r.kind === "push");
  const order = pushes.flatMap((r) => r.ops.map((o) => [r.scope, Object.keys(o.payload).sort().join(",")]));
  assert.deepEqual(order.map((o) => o[0]), ["pick_tasks", "pick_tasks", "pick_tasks", "shortages"], "enqueue order is replay order");
  assert.deepEqual(order[2], ["pick_tasks", "id,picked"]);
  assert.deepEqual(payloadOffenders(server.requests), []);
  assert.equal(await layer.pendingRecord(TASK), false);
  const after = await layer.pick(TASK);
  if (!after.ok) return assert.fail("the pick should read back");
  assert.equal(after.value.picked.length, 2, "the pulled row carries the recorded lines");
  assert.equal(after.value.shortages.length, 1);
  assert.equal(after.value.task.status, "IN_PROGRESS");
});

test("a record the core refuses comes back as a Not accepted card with the core's words, and the local lines are taken back", async () => {
  const { layer, server } = await rig();
  server.setOffline(true);
  await layer.recordLines(TASK, [recordLine()]);
  const [queued] = await layer.store.outbox.pending();
  server.refuse(queued.key, "line 1: that is more than was ordered, the yard cannot raise a quantity");
  await online(layer, server);
  await layer.cycle();
  const card = (await layer.openCards()).find((c) => c.kind === "rejected");
  assert.ok(card !== undefined, "a rejected card waits");
  assert.equal(card.scope, "pick_tasks");
  assert.match(card.error, /more than was ordered/);
  const after = await layer.pick(TASK);
  if (!after.ok) return assert.fail("the pick should read back");
  assert.equal(after.value.picked.length, 0, "the refused record is not shown as recorded");
  assert.equal(await layer.pendingRecord(TASK), false);
});

test("a record the server moved the ticket under comes back superseded, and the server row stands", async () => {
  const { layer, server } = await rig();
  server.setOffline(true);
  await layer.recordLines(TASK, [recordLine()]);
  server.touch("pick_tasks", TASK, { status: "CANCELLED", claimed_by: "dispatch" });
  await online(layer, server);
  await layer.cycle();
  const card = (await layer.openCards()).find((c) => c.kind === "superseded");
  assert.ok(card !== undefined, "the server wins card shows");
  assert.deepEqual(Object.keys(card.clientPayload).sort(), ["id", "picked"]);
  const after = await layer.pick(TASK);
  if (!after.ok) return assert.fail("the pick should read back");
  assert.equal(after.value.task.status, "CANCELLED");
  assert.equal(after.value.picked.length, 0, "the server's row holds no record");
});

test("a record is refused with words before it queues: empty, too many, control characters", async () => {
  const { layer } = await rig();
  const empty = await layer.recordLines(TASK, []);
  assert.equal(empty.ok, false);
  assert.match(empty.error, /say what was picked/i);
  const many = await layer.recordLines(TASK, Array.from({ length: 501 }, (_, i) => recordLine({ order_line_no: i + 1 })));
  assert.equal(many.ok, false);
  assert.match(many.error, /more lines than any one ticket carries/);
  for (const [leg, bad] of [["short_note", "a\u0000b"], ["short_note", "a\u202eb"], ["description", "a\u001bb"], ["sku", "a\tb"], ["uom", "a\nb"]]) {
    const refused = await layer.recordLines(TASK, [recordLine({ [leg]: bad })]);
    assert.equal(refused.ok, false, `${leg} ${JSON.stringify(bad)}`);
    assert.match(refused.error, /control characters/);
  }
  const multi = await layer.recordLines(TASK, [recordLine({ short_note: "two\nlines\tand\rmore" })]);
  assert.equal(multi.ok, true, "tab, LF and CR stay in a note");
  assert.equal(await layer.waiting(), 1, "only the good record queued");
});

test("a second record of the same pick before any pull goes blind and still drains clean", async () => {
  const { layer, server } = await rig();
  server.setOffline(true);
  await layer.recordLines(TASK, [recordLine({ qty_picked: "20", short_reason: "NOT_IN_YARD" })]);
  await layer.recordLines(TASK, [recordLine({ qty_picked: "24" })]);
  assert.equal(await layer.waiting(), 2);
  await online(layer, server);
  await layer.cycle();
  assert.equal(await layer.waiting(), 0);
  assert.equal((await layer.openCards()).length, 0);
  const pushes = server.requests.filter((r) => r.kind === "push");
  assert.equal(pushes[0].ops[1].base_seq, undefined, "the chained write goes blind");
  const after = await layer.pick(TASK);
  if (!after.ok) return assert.fail("the pick should read back");
  assert.equal(after.value.picked.length, 1);
  assert.equal(after.value.picked[0].qty_picked, "24");
});

test("the shortage report's payload drops the stamped legs the core refuses", async () => {
  const { layer, server } = await rig();
  server.setOffline(true);
  await layer.reportShortage(TASK, { orderLineNo: 2, description: "Polymeric sand, grey, 18 kg", qtyShort: "2", uom: "BAG", note: "" });
  const [queued] = await layer.store.outbox.pending();
  for (const stamped of ["reported_at", "resolved_at", "resolved_by", "resolution", "reported_by"]) {
    assert.equal(stamped in queued.payload, false, `${stamped} is the server's`);
  }
  await online(layer, server);
  await layer.cycle();
  assert.deepEqual(payloadOffenders(server.requests), []);
});

test("a record queued behind this device's own shortage report goes blind: the report moves the task row's seq on the server, and a base named before it would come back superseded", async () => {
  const { layer, server } = await rig();
  server.setOffline(true);
  const reported = await layer.reportShortage(TASK, { orderLineNo: 2, description: "Polymeric sand, grey, 18 kg", qtyShort: "2", uom: "BAG", note: "" });
  assert.equal(reported.ok, true);
  // The picker takes their time: the cycle the report asked for has come to rest before the record.
  await layer.runner.settled();
  const recorded = await layer.recordLines(TASK, [recordLine(), SHORT]);
  assert.equal(recorded.ok, true);
  await layer.runner.settled();
  const claimed = await layer.claim(TASK);
  assert.equal(claimed.ok, true);
  const queued = await layer.store.outbox.pending();
  for (const op of queued.filter((row) => row.scope === "pick_tasks")) {
    assert.equal(op.baseSeq, null, `the ${Object.keys(op.payload).filter((k) => k !== "id").join(",")} write names no base after the report`);
  }
  await online(layer, server);
  await layer.cycle();
  assert.equal(await layer.waiting(), 0);
  assert.equal((await layer.openCards()).length, 0, "no server wins card for the device's own earlier report");
});

// ---- review 6c: rollback and the connected supersede ----

test("a refused second record puts the server's earlier lines back, not an empty list", async () => {
  const { layer, server } = await rig();
  await layer.recordLines(TASK, [recordLine({ qty_picked: "20", short_reason: "DAMAGED" })]);
  await layer.cycle();
  const first = await layer.pick(TASK);
  assert.equal(first.value.picked[0].qty_picked, "20", "the server's first record is on the row");
  server.setOffline(true);
  await layer.recordLines(TASK, [recordLine({ qty_picked: "24" })]);
  const [queued] = await layer.store.outbox.pending();
  server.refuse(queued.key, "line 1: refused by test");
  await online(layer, server);
  await layer.cycle();
  const after = await layer.pick(TASK);
  assert.equal(after.value.picked.length, 1);
  assert.equal(after.value.picked[0].qty_picked, "20", "the earlier lines are back");
  assert.equal(after.value.picked[0].id.startsWith("local:"), false);
});

test("a refused record is taken back when the refusal arrives after a restart", async () => {
  const { layer, server, store, backend } = await rig();
  server.setOffline(true);
  await layer.recordLines(TASK, [recordLine()]);
  const [queued] = await layer.store.outbox.pending();
  server.refuse(queued.key, "line 1: refused by test");
  await online(layer, server);
  const fresh = new YardOffline({ store, sql: backend.sql, transport: server.transport(), actorId: ACTOR, newKey: nextKey });
  await fresh.cycle();
  const after = await fresh.pick(TASK);
  assert.equal(after.value.picked.length, 0);
  assert.deepEqual((await fresh.openCards()).map((c) => c.kind), ["rejected"]);
});

test("a connected record the server superseded is answered in words, not as saved", async () => {
  const { layer, server } = await rig();
  server.touch("pick_tasks", TASK, { priority: "NORMAL" });
  const saved = await layer.recordLines(TASK, [recordLine()]);
  assert.equal(saved.ok, true);
  const words = await layer.sendRecordNow(TASK);
  assert.match(words, /changed on the server first/);
  assert.match(words, /not saved/);
  assert.deepEqual((await layer.openCards()).map((c) => c.kind), ["superseded"]);
  const after = await layer.pick(TASK);
  assert.equal(after.value.picked.length, 0);
});

// ---- the refusal of a record onto a closed pick (FROM-PRINCIPAL 49) ----

const HANDED_OVER = "This pick was already handed over, so your short was not saved. Tell the counter: the order still bills the full quantity.";
const CANCELLED_WORDS = "This pick was cancelled, so your record was not saved. Tell the counter.";

for (const [sentence, yardWords, label] of [
  ["tasks: this pick is done, its record cannot change", HANDED_OVER, "handed over"],
  ["tasks: this pick was cancelled, its record cannot change", CANCELLED_WORDS, "cancelled"],
]) {
  test(`an offline record the core refuses on a ${label} pick comes back as a card in the yard's words, and the local lines are taken back`, async () => {
    const { layer, server } = await rig();
    server.setOffline(true);
    await layer.recordLines(TASK, [SHORT]);
    const [queued] = await layer.store.outbox.pending();
    server.refuse(queued.key, sentence);
    server.setOffline(false);
    await layer.cycle();
    const card = (await layer.openCards()).find((c) => c.kind === "rejected");
    assert.equal(card.error, yardWords);
    const after = await layer.pick(TASK);
    assert.equal(after.value.picked.length, 0);
  });

  test(`a connected record the core refuses on a ${label} pick is answered in the yard's words`, async () => {
    const { layer, server } = await rig();
    const saved = await layer.recordLines(TASK, [SHORT]);
    assert.equal(saved.ok, true);
    const [queued] = await layer.store.outbox.pending();
    server.refuse(queued.key, sentence);
    assert.equal(await layer.sendRecordNow(TASK), yardWords);
  });
}
