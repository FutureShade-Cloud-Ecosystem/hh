// HH Drive's offline data layer, over the store's node:sqlite executor and the
// fake sync server. The driver's reads compose from the store (runs and stops
// with staleness), the writes enqueue on their own scope (stops, pod, runs)
// and patch the local projection, and every conflict comes back as a card.
//
// Red first: written against the app before drive-offline.ts existed, failing
// on the missing module, then passing on the implementation.
import { test } from "node:test";
import assert from "node:assert/strict";

import { ApiClient } from "@hardscapehouse/api-client";
import { nodeSqliteExecutor, FakeSyncServer } from "@hardscapehouse/offline-store/testing";
import { OfflineStore, createSqliteBackend, scopeBookFor } from "@hardscapehouse/offline-store";
import { houseZone, resetHouseZone, serveHouseZoneFrom } from "@hardscapehouse/pos-kit/src/ported/host-facts.ts";
import { DriveApi } from "../src/drive-api.ts";
import { DriveOffline, bannerMode } from "../src/drive-offline.js";
import { goTypedNameBytes, payloadOffenders } from "./_lib/gosets.mjs";

const DRIVER = "de110000-0000-4000-9000-000000000003";
const RUN = "a1000000-0000-4000-9000-000000000005";
const STOP_1 = "5a000000-0000-4000-9000-000000000001";
const STOP_2 = "5a000000-0000-4000-9000-000000000002";

let keyCounter = 0;
function nextKey() {
  keyCounter += 1;
  return `00000000-0000-4000-8000-${String(keyCounter).padStart(12, "0")}`;
}

async function rig(extra = {}) {
  const server = new FakeSyncServer();
  server.seed("runs", RUN, {
    id: RUN,
    run_no: "RUN-R5",
    truck_code: "FLAT-1",
    status: "DISPATCHED",
    scheduled_date: "2026-10-08",
  });
  server.seed("stops", STOP_1, {
    id: STOP_1,
    run_id: RUN,
    stop_no: 1,
    order_no: "SO-1001",
    customer_name: "Northline Hardscapes Ltd (demo)",
    status: "READY",
    time_window: "AM",
    contact_name: "Site contact (demo)",
    contact_phone: "",
    address_line: "95 Bagot St",
    city: "Kingston",
    postal_code: "K7L 3E7",
    instructions: "Gate code on the paperwork.",
    pod_received_by: "",
    pod_at: "",
    failure_reason: "",
    photo_count: 0,
    weight_kg: 400,
    lines: [
      { line_no: 1, sku: "", description: "Natural stone pavers, grey", qty: "120.000", uom: "sqft" },
      { line_no: 2, sku: "", description: "Polymeric sand, 50 lb bag", qty: "6.000", uom: "bag" },
    ],
  });
  server.seed("stops", STOP_2, {
    id: STOP_2,
    run_id: RUN,
    stop_no: 2,
    order_no: "SO-1002",
    customer_name: "Harbourview Builders (demo)",
    status: "READY",
    time_window: "AM",
    contact_name: "",
    contact_phone: "",
    address_line: "1201 Division St",
    city: "Kingston",
    postal_code: "K7K 6X4",
    instructions: "",
    pod_received_by: "",
    pod_at: "",
    failure_reason: "",
    photo_count: 0,
    weight_kg: 800,
    lines: [{ line_no: 1, sku: "", description: "Retaining wall block, charcoal", qty: "84.000", uom: "each" }],
  });
  const executor = nodeSqliteExecutor();
  const backend = createSqliteBackend(executor);
  const store = await OfflineStore.open({ backend, scopes: scopeBookFor(["drive-run"]) });
  const layer = new DriveOffline({
    store,
    sql: backend.sql,
    transport: server.transport(),
    newKey: nextKey,
    ...extra,
  });
  await layer.cycle();
  return { layer, server, store, backend };
}

test("the first cycle pulls the run, and the run sheet reads answer from the store with staleness", async () => {
  const { layer } = await rig();
  const answer = await layer.runs("2026-10-08");
  assert.equal(answer.ok, true);
  if (!answer.ok) return;
  assert.equal(answer.value.runs.length, 1);
  const run = answer.value.runs[0];
  assert.equal(run.run_no, "RUN-R5");
  assert.equal(run.stops.length, 2);
  assert.equal(run.stops[0].customer_name, "Northline Hardscapes Ltd (demo)");
  assert.equal(run.lines[STOP_1].length, 2);
  assert.equal(answer.staleness.neverPulled, false);
});

test("the sheet answers only the dealer's own day, never another day's run", async () => {
  const { layer } = await rig();
  const today = await layer.runs("2026-10-08");
  assert.equal(today.ok, true);
  if (!today.ok) return;
  assert.equal(today.value.runs.length, 1);
  const other = await layer.runs("2026-10-09");
  if (!other.ok) return assert.fail("the answer should read");
  assert.equal(other.value.runs.length, 0);
});

test("offline, loading and going en route stay local, then drain in order", async () => {

  const { layer, server, store } = await rig();
  server.setOffline(true);
  const loaded = await layer.advance(STOP_1, "LOADED");
  assert.equal(loaded.ok, true);
  const enRoute = await layer.advance(STOP_1, "EN_ROUTE");
  assert.equal(enRoute.ok, true);
  const read = await layer.runs("2026-10-08");
  if (!read.ok) return assert.fail("the run sheet should read back");
  assert.equal(read.value.runs[0].stops[0].status, "EN_ROUTE");
  assert.equal(await layer.waiting(), 2);
  server.setOffline(false);
  await layer.cycle();
  assert.equal(await layer.waiting(), 0);
  assert.equal(server.appliedOrder.length, 2);
  const pushes = server.requests.filter((r) => r.kind === "push");
  assert.equal(pushes.length, 1);
  assert.equal(pushes[0].scope, "stops");
  // The chained writes on one stop go blind, the device's own chain.
  assert.equal(pushes[0].ops[0].base_seq !== undefined, true);
  assert.equal(pushes[0].ops[1].base_seq === undefined, true);
});

test("a stop the server moved first comes back superseded, and the server row is what reads show", async () => {
  const { layer, server, store } = await rig();
  server.setOffline(true);
  await layer.advance(STOP_2, "LOADED");
  // Dispatch moves the stop while the device cannot hear it.
  server.touch("stops", STOP_2, { status: "CANCELLED" });
  server.setOffline(false);
  await layer.cycle();
  const cards = await layer.openCards();
  const superseded = cards.find((c) => c.kind === "superseded");
  assert.ok(superseded !== undefined, "the server wins card shows");
  assert.equal(superseded.scope, "stops");
  const read = await layer.runs("2026-10-08");
  if (!read.ok) return assert.fail("the run sheet should read back");
  assert.equal(read.value.runs[0].stops[1].status, "CANCELLED");
  assert.equal(await layer.waiting(), 0);
});

test("the run close cannot queue: the manifest gives the runs scope no writable policy", async () => {
  const { layer } = await rig();
  const refused = await layer.enqueueRaw({ scope: "runs", op: "update", payload: { id: RUN, status: "COMPLETED" } });
  assert.equal(refused.ok, false);
  if (refused.ok) return;
  assert.match(refused.message, /read only in this profile/);
});

test("a drive the run's own status refuses stays local, in the server's words", async () => {
  const { layer, server } = await rig();
  // The run was never dispatched: the pulled row carries it.
  server.touch("runs", RUN, { status: "PLANNED" });
  await layer.cycle();
  const refused = await layer.advance(STOP_1, "EN_ROUTE");
  assert.equal(refused.ok, false);
  if (refused.ok) return;
  assert.equal(refused.status, 409);
  assert.match(refused.error, /the run has not been dispatched/);
  assert.equal(await layer.waiting(), 0);
});

test("a chained write goes blind even when the earlier op drained before the pull landed", async () => {
  // The rig, with the pull for stops delayed a beat once the device has
  // pulled its first world: the window between a push applying and the pull
  // landing is where a stale base could sneak in.
  const server = new FakeSyncServer();
  server.seed("runs", RUN, { id: RUN, run_no: "RUN-R5", truck_code: "FLAT-1", status: "DISPATCHED", scheduled_date: "2026-10-08" });
  server.seed("stops", STOP_1, {
    id: STOP_1, run_id: RUN, stop_no: 1, order_no: "SO-1001", customer_name: "Northline Hardscapes Ltd (demo)",
    status: "READY", time_window: "AM", contact_name: "", contact_phone: "", address_line: "95 Bagot St", city: "Kingston",
    postal_code: "K7K 6X4", instructions: "", pod_received_by: "", pod_at: "", failure_reason: "", photo_count: 0,
    weight_kg: 400,
    lines: [{ line_no: 1, sku: "", description: "Natural stone pavers, grey", qty: "120.000", uom: "sqft" }],
  });
  const backend = createSqliteBackend(nodeSqliteExecutor());
  const store = await OfflineStore.open({ backend, scopes: scopeBookFor(["drive-run"]) });
  const inner = server.transport();
  const delayedTransport = {
    push: (scope, ops) => inner.push(scope, ops),
    pull: (scope, since, limit) => {
      if (scope === "stops" && since > 0) {
        return new Promise((resolve) => setTimeout(() => resolve(inner.pull(scope, since, limit)), 120));
      }
      return inner.pull(scope, since, limit);
    },
  };
  const layer = new DriveOffline({ store, sql: backend.sql, transport: delayedTransport, newKey: nextKey });
  await layer.cycle();
  // First move: it queues with the row's pulled seq as its base and drains.
  await layer.advance(STOP_1, "LOADED");
  for (let waited = 0; server.appliedOrder.length < 1 && waited < 5_000; waited += 50) {
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  assert.equal(server.appliedOrder.length, 1, "the load applied");
  // The pull is still delayed: the local row's seq is stale now. The second
  // move on the same row must go blind, never name the stale base.
  await layer.advance(STOP_1, "EN_ROUTE");
  await layer.cycle();
  const cards = await layer.openCards();
  assert.equal(cards.length, 0, "no conflict: the chained write went blind");
  const read = await layer.runs("2026-10-08");
  if (!read.ok) return assert.fail("the run sheet should read back");
  assert.equal(read.value.runs[0].stops[0].status, "EN_ROUTE");
  assert.equal(await layer.waiting(), 0);
});

test("a money scope is refused at the door, before anything is stored", async () => {


  const { layer, store } = await rig();
  const refused = await layer.enqueueRaw({ scope: "price_matrix", op: "update", payload: { id: RUN } });
  assert.equal(refused.ok, false);
  assert.equal(await layer.waiting(), 0);
  assert.equal((await store.outbox.list()).length, 0);
});

test("the banner mode maps the runner state and the ownership onto the chrome's modes", () => {
  assert.equal(bannerMode("idle", "owner"), "");
  assert.equal(bannerMode("offline", "owner"), "offline");
  assert.equal(bannerMode("unreachable", "owner"), "unreachable");
  assert.equal(bannerMode("auth_expired", "owner"), "auth-expired");
  assert.equal(bannerMode("idle", "read_cache_only"), "read-only");
});

test("a later write on a stop names its base once the earlier request has settled, so a dispatcher's move is caught", async () => {
  // The reviewer's probe: advance LOADED and let the request's own cycle
  // settle (not cycle(), which also clears the written since pull set), have
  // the server move the stop, then advance EN_ROUTE. The second write must
  // carry the row's pulled seq as its base, and the server's move must come
  // back as a card.
  const { layer, server } = await rig();
  await layer.advance(STOP_1, "LOADED");
  await layer.runner.settled();
  assert.equal(await layer.waiting(), 0, "the load drained");
  server.touch("stops", STOP_1, { status: "CANCELLED" });
  await layer.advance(STOP_1, "EN_ROUTE");
  await layer.runner.settled();
  const pushes = server.requests.filter((r) => r.kind === "push" && r.scope === "stops");
  const last = pushes[pushes.length - 1].ops[0];
  assert.equal(typeof last.base_seq, "number", "the second write names the pulled seq as its base");
  const cards = await layer.openCards();
  assert.equal(cards.length, 1, "the dispatcher's move shows as a card, never overwritten silently");
});

test("the run close drains the outbox first, and refuses with words while a move is still waiting", async () => {
  const closes = [];
  const direct = {
    completeRun: async (id) => {
      closes.push({ id, applied: server.appliedOrder.length });
      return { ok: true, value: true };
    },
  };
  const { layer, server } = await rig({ direct });
  // A move queued with no signal: the close must not jump it.
  server.setOffline(true);
  await layer.advance(STOP_1, "LOADED");
  const refused = await layer.completeRun(RUN);
  assert.equal(refused.ok, false);
  if (refused.ok) return;
  assert.match(refused.error, /still waiting/);
  assert.equal(closes.length, 0, "the direct close was never sent");
  // Back on signal: the queued move goes first, then the close is sent.
  server.setOffline(false);
  const closed = await layer.completeRun(RUN);
  assert.equal(closed.ok, true);
  assert.equal(closes.length, 1);
  assert.equal(closes[0].applied, 1, "the queued move had applied before the close went");
});

test("a second tab (read cache only) opens no layer, so the app stays online, and says it is read only", async () => {
  const { openDriveOfflineOver } = await import("../src/drive-offline.js");
  const executor = nodeSqliteExecutor();
  const backend = { ...createSqliteBackend(executor), ownership: () => "read_cache_only" };
  const server = new FakeSyncServer();
  const opened = await openDriveOfflineOver(backend, { transport: server.transport(), direct: {} });
  assert.equal(opened.layer, null, "no layer: the empty read cache must not stand in for the board");
  assert.equal(opened.readOnly, true, "the strip still says read only");
  const owner = await openDriveOfflineOver(createSqliteBackend(nodeSqliteExecutor()), { transport: server.transport(), direct: {} });
  assert.equal(owner.readOnly, false);
  assert.notEqual(owner.layer, null);
});

test("every queued write declares only fields the core's sync stores admit (the Go writable sets)", async () => {
  const { layer, server } = await rig();
  server.setOffline(true);
  await layer.advance(STOP_1, "LOADED");
  await layer.advance(STOP_1, "EN_ROUTE");
  await layer.advance(STOP_1, "FAILED", "Gate locked, no answer");
  server.setOffline(false);
  await layer.cycle();
  assert.equal(await layer.waiting(), 0);
  const pushed = server.requests.filter((r) => r.kind === "push").flatMap((r) => r.ops);
  const failed = pushed.find((op) => op.payload.status === "FAILED");
  assert.equal(failed.payload.failure_reason, "Gate locked, no answer", "the failed stop carries the core's own key");
  assert.deepEqual(payloadOffenders(server.requests), []);
});

test("a write made while an earlier request settles keeps its mark, so a third write still goes blind", async () => {
  // The window: write one's request is in its delayed pull when write two is
  // made; write one's settle must not forget write two. Write two drains in
  // the next cycle, and write three, made while that cycle's pull is delayed,
  // would otherwise name the stale pulled seq and meet its own device's move.
  const server = new FakeSyncServer();
  server.seed("runs", RUN, { id: RUN, run_no: "RUN-R5", truck_code: "FLAT-1", status: "DISPATCHED", scheduled_date: "2026-10-08" });
  server.seed("stops", STOP_1, {
    id: STOP_1, run_id: RUN, stop_no: 1, order_no: "SO-1001", customer_name: "Northline Hardscapes Ltd (demo)",
    status: "READY", time_window: "AM", contact_name: "", contact_phone: "", address_line: "95 Bagot St", city: "Kingston",
    postal_code: "K7K 6X4", instructions: "", pod_received_by: "", pod_at: "", failure_reason: "", photo_count: 0,
    weight_kg: 400,
    lines: [{ line_no: 1, sku: "", description: "Natural stone pavers, grey", qty: "120.000", uom: "sqft" }],
  });
  const backend = createSqliteBackend(nodeSqliteExecutor());
  const store = await OfflineStore.open({ backend, scopes: scopeBookFor(["drive-run"]) });
  const inner = server.transport();
  const delayedTransport = {
    push: (scope, ops) => inner.push(scope, ops),
    pull: (scope, since, limit) =>
      scope === "stops" && since > 0
        ? new Promise((resolve) => setTimeout(() => resolve(inner.pull(scope, since, limit)), 150))
        : inner.pull(scope, since, limit),
  };
  const layer = new DriveOffline({ store, sql: backend.sql, transport: delayedTransport, direct: {} });
  await layer.cycle();
  const until = async (done) => {
    for (let waited = 0; !done() && waited < 5_000; waited += 10) await new Promise((r) => setTimeout(r, 10));
    assert.equal(done(), true, "the server reached the awaited state");
  };
  await layer.advance(STOP_1, "LOADED");
  await until(() => server.appliedOrder.length === 1);
  await layer.advance(STOP_1, "EN_ROUTE");
  await until(() => server.appliedOrder.length === 2);
  await layer.advance(STOP_1, "FAILED", "Gate locked");
  await layer.runner.settled();
  await layer.runner.settled();
  const failed = server.requests.filter((r) => r.kind === "push").flatMap((r) => r.ops).find((op) => op.payload.status === "FAILED");
  assert.equal(failed.base_seq, undefined, "the third write went blind");
  assert.equal((await layer.openCards()).length, 0, "no false conflict against the device's own move");
});

test("a write that read its row before a cycle's pull landed does not name the stale seq as its base", async () => {
  // The row is read, the cycle for the earlier write settles (its pull lands,
  // the mark clears), and only then is the base decided: it must come from the
  // row as it stands then, not from the read made before the pull.
  const server = new FakeSyncServer();
  server.seed("runs", RUN, { id: RUN, run_no: "RUN-R5", truck_code: "FLAT-1", status: "DISPATCHED", scheduled_date: "2026-10-08" });
  server.seed("stops", STOP_1, {
    id: STOP_1, run_id: RUN, stop_no: 1, order_no: "SO-1001", customer_name: "Northline Hardscapes Ltd (demo)",
    status: "READY", time_window: "AM", contact_name: "", contact_phone: "", address_line: "95 Bagot St", city: "Kingston",
    postal_code: "K7K 6X4", instructions: "", pod_received_by: "", pod_at: "", failure_reason: "", photo_count: 0,
    weight_kg: 400,
    lines: [{ line_no: 1, sku: "", description: "Natural stone pavers, grey", qty: "120.000", uom: "sqft" }],
  });
  const backend = createSqliteBackend(nodeSqliteExecutor());
  const store = await OfflineStore.open({ backend, scopes: scopeBookFor(["drive-run"]) });
  const inner = server.transport();
  const delayedTransport = {
    push: (scope, ops) => inner.push(scope, ops),
    pull: (scope, since, limit) =>
      scope === "stops" && since > 0
        ? new Promise((resolve) => setTimeout(() => resolve(inner.pull(scope, since, limit)), 100))
        : inner.pull(scope, since, limit),
  };
  const layer = new DriveOffline({ store, sql: backend.sql, transport: delayedTransport, direct: {} });
  await layer.cycle();
  await layer.advance(STOP_1, "LOADED");
  for (let waited = 0; server.appliedOrder.length < 1 && waited < 5_000; waited += 10) await new Promise((r) => setTimeout(r, 10));
  assert.equal(server.appliedOrder.length, 1, "the load applied");
  let release;
  const gate = new Promise((resolve) => (release = resolve));
  const read = store.read.bind(store);
  store.read = async (scope, id) => {
    const out = await read(scope, id);
    if (scope === "stops") await gate;
    return out;
  };
  const second = layer.advance(STOP_1, "EN_ROUTE");
  await layer.runner.settled();
  store.read = read;
  release();
  await second;
  await layer.runner.settled();
  await layer.runner.settled();
  assert.equal(server.appliedOrder.length, 2, "the second move applied, never superseded by the device's own first");
  assert.equal((await layer.openCards()).length, 0);
});

// ---- the proof of delivery attestation through the outbox (item 6c) ----

const NAME = "Site Foreman (demo)";

/** A stop on the road, offline: loaded and en route queued, the proof next. */
async function onTheRoad(extra = {}) {
  const made = await rig(extra);
  made.server.setOffline(true);
  assert.equal((await made.layer.advance(STOP_1, "LOADED")).ok, true);
  assert.equal((await made.layer.advance(STOP_1, "EN_ROUTE")).ok, true);
  return made;
}

async function stopOf(layer, id = STOP_1) {
  const read = await layer.runs("2026-10-08");
  if (!read.ok) assert.fail("the run sheet should read back");
  return read.value.runs[0].stops.find((s) => s.id === id);
}

test("offline, the proof queues on pod create with the attestation's exact fields, and the local stop shows delivered and waiting", async () => {
  const direct = { completeRun: async () => assert.fail("no direct door"), pod: async () => assert.fail("the attestation is not a direct door") };
  const { layer } = await onTheRoad({ direct });
  const signed = await layer.pod(STOP_1, `  ${NAME}  `);
  assert.equal(signed.ok, true, JSON.stringify(signed));
  assert.equal(await layer.waiting(), 3);
  const pending = await layer.store.outbox.pending();
  const proof = pending.find((row) => row.scope === "pod");
  assert.equal(proof.op, "create");
  assert.deepEqual(Object.keys(proof.payload).sort(), ["affirmed", "id", "received_by", "stop_id"]);
  assert.equal(proof.payload.stop_id, STOP_1);
  assert.equal(proof.payload.received_by, NAME, "the name is trimmed, as the core trims it");
  assert.equal(proof.payload.affirmed, true);
  assert.match(proof.payload.id, /^[0-9a-f-]{36}$/, "the outbox's local row id is a uuid");
  assert.equal(proof.baseSeq, null, "a create names no base");
  const stop = await stopOf(layer);
  assert.equal(stop.status, "DELIVERED");
  assert.equal(stop.pod_received_by, NAME);
  assert.notEqual(stop.pod_at, "");
  assert.equal(stop.proof_waiting, true, "the stop says the proof has not gone yet");
});

test("back online the moves and the proof drain in the order they were made, and the waiting state clears", async () => {
  const { layer, server } = await onTheRoad();
  await layer.pod(STOP_1, NAME);
  server.setOffline(false);
  await layer.cycle();
  assert.equal(await layer.waiting(), 0);
  const pushes = server.requests.filter((r) => r.kind === "push");
  assert.deepEqual(pushes.flatMap((r) => r.ops.map(() => r.scope)), ["stops", "stops", "pod"]);
  assert.deepEqual(payloadOffenders(server.requests), []);
  assert.equal((await stopOf(layer)).proof_waiting, false);
  assert.equal((await layer.openCards()).length, 0);
});

test("a proof the core refuses comes back as a Not accepted card with the core's words, and the local stop is put back", async () => {
  const { layer, server } = await onTheRoad();
  await layer.pod(STOP_1, NAME);
  const proof = (await layer.store.outbox.pending()).find((row) => row.scope === "pod");
  server.refuse(proof.key, "illegal status transition");
  server.setOffline(false);
  await layer.cycle();
  const card = (await layer.openCards()).find((c) => c.kind === "rejected");
  assert.ok(card !== undefined, "a Not accepted card waits");
  assert.equal(card.scope, "pod");
  assert.match(card.error, /illegal status transition/);
  const stop = await stopOf(layer);
  assert.equal(stop.status, "EN_ROUTE", "the stop is not shown delivered when the server refused");
  assert.equal(stop.pod_received_by, "");
  assert.equal(stop.proof_waiting, false);
});

test("a name the door would refuse is refused with words before it queues: blank, over the core's bound, control and bidi characters", async () => {
  const { layer } = await onTheRoad();
  const bound = goTypedNameBytes();
  const before = await layer.waiting();
  const long = await layer.pod(STOP_1, "e".repeat(bound + 1));
  assert.equal(long.ok, false);
  assert.match(long.error, /too long/);
  const multibyte = await layer.pod(STOP_1, "\u00e9".repeat(bound / 2 + 1));
  assert.equal(multibyte.ok, false, "the bound is bytes, not characters");
  assert.match(multibyte.error, /too long/);
  for (const bad of ["Clerk\u0000", "Cl\terk", "Cl\nerk", "Clerk\u007f", "Clerk\u202e", "Clerk\u2067"]) {
    const refused = await layer.pod(STOP_1, bad);
    assert.equal(refused.ok, false, JSON.stringify(bad));
    assert.match(refused.error, /control characters/);
  }
  const blank = await layer.pod(STOP_1, "   ");
  assert.equal(blank.ok, false);
  assert.match(blank.error, /name/i);
  assert.equal(await layer.waiting(), before, "nothing was queued");
  // Exactly the bound lands, in bytes.
  const exact = await layer.pod(STOP_1, "\u00e9".repeat(bound / 2));
  assert.equal(exact.ok, true, JSON.stringify(exact));
});

test("a stop that is not on the road, or not on the sheet, refuses before it queues", async () => {
  const { layer } = await rig();
  const ready = await layer.pod(STOP_1, NAME);
  assert.equal(ready.ok, false);
  assert.equal(ready.status, 409);
  assert.equal(await layer.waiting(), 0);
  const unknown = await layer.pod("5a000000-0000-4000-9000-0000000000ff", NAME);
  assert.equal(unknown.ok, false);
  assert.equal(unknown.status, 404);
});

test("a second signature on a stop that is waiting to deliver refuses, the first one stands", async () => {
  const { layer } = await onTheRoad();
  assert.equal((await layer.pod(STOP_1, NAME)).ok, true);
  const again = await layer.pod(STOP_1, "Someone Else");
  assert.equal(again.ok, false);
  assert.equal(again.status, 409);
  assert.equal((await layer.store.outbox.pending()).filter((row) => row.scope === "pod").length, 1);
});

test("the payload check flags a field the attestation form does not admit (a forbidden field added to a queued write fails)", () => {
  const request = (payload) => [{ kind: "push", scope: "pod", ops: [{ op: "create", payload }] }];
  const good = { id: "7b000000-0000-4000-9000-0000000fb001", stop_id: STOP_1, received_by: "A B", affirmed: true };
  assert.deepEqual(payloadOffenders(request(good)), []);
  assert.deepEqual(payloadOffenders(request({ ...good, created_at: "x" })), ["pod attest create: created_at"]);
  assert.deepEqual(payloadOffenders(request({ ...good, caption: "x" })), ["pod attest create: caption"]);
  assert.deepEqual(payloadOffenders(request({ ...good, affirmed: "yes" })), ["pod attest create: affirmed is not true"]);
});

// ---- review 6c: the proof's rollback ----

async function toTheRoad(layer) {
  await layer.advance(STOP_1, "LOADED");
  await layer.advance(STOP_1, "EN_ROUTE");
  await layer.cycle();
}

test("a proof the core refuses while the stop is unchanged on the server is taken back", async () => {
  const { layer, server } = await rig();
  await toTheRoad(layer);
  server.setOffline(true);
  assert.equal((await layer.pod(STOP_1, "Site lead")).ok, true);
  const [proof] = (await layer.store.outbox.pending()).filter((r) => r.scope === "pod");
  server.refuse(proof.key, "dispatch: no active staff authority in this org");
  server.setOffline(false);
  await layer.cycle();
  const stop = await stopOf(layer);
  assert.equal(stop.status, "EN_ROUTE");
  assert.equal(stop.pod_received_by, "");
  assert.equal(stop.proof_waiting, false);
  assert.deepEqual((await layer.openCards()).map((c) => c.kind), ["rejected"]);
});

test("the same refusal arriving through a fresh layer over the same store (a restart) takes the proof back", async () => {
  const { layer, server, store, backend } = await rig();
  await toTheRoad(layer);
  server.setOffline(true);
  await layer.pod(STOP_1, "Site lead");
  const [proof] = (await layer.store.outbox.pending()).filter((r) => r.scope === "pod");
  server.refuse(proof.key, "dispatch: no active staff authority in this org");
  server.setOffline(false);
  const fresh = new DriveOffline({ store, sql: backend.sql, transport: server.transport(), newKey: nextKey });
  await fresh.cycle();
  const stop = await stopOf(fresh);
  assert.equal(stop.status, "EN_ROUTE");
  assert.equal(stop.pod_received_by, "");
  assert.deepEqual((await fresh.openCards()).map((c) => c.kind), ["rejected"]);
});

// The dealer's zone, persisted in the store: a fully offline cold start reads the
// run sheet on the zone the last online session served, never on the Ontario
// default. The instant is built from numbers as house-zone.test.mjs does: 21:30
// on the second of a month in Los Angeles is the third in Toronto and in UTC.
const PACIFIC_EVENING = new Date(Date.UTC(2026, 6, 3, 4, 30));
const PACIFIC_DAY = "2026-07-02";
const RUN_PACIFIC = "a2000000-0000-4000-9000-000000000006";

test("the served zone round trips through the store, and an offline cold start reads the Pacific day", async (t) => {
  // A failed config read must never be able to move the held zone in this test:
  // the zone module state resets around it.
  t.after(() => resetHouseZone());
  const { layer, server } = await rig();
  // A second run on the Pacific day, so the sheet has something to read there.
  server.seed("runs", RUN_PACIFIC, {
    id: RUN_PACIFIC,
    run_no: "RUN-P1",
    truck_code: "FLAT-2",
    status: "DISPATCHED",
    scheduled_date: PACIFIC_DAY,
  });
  await layer.cycle();
  // Nothing stored yet: the read answers null, never a made up zone.
  assert.equal(await layer.readZone(), null);
  // The zone the config read served is saved, and reads back.
  await layer.saveZone("America/Los_Angeles");
  assert.equal(await layer.readZone(), "America/Los_Angeles");
  // The cold start: the zone module is fresh, the config read fails (503, the
  // device has no signal), and the app restores the stored zone before asking
  // for the day. The day is the Pacific dealer's own, and the sheet reads that
  // day's run from the store.
  resetHouseZone();
  const offlineFetch = async () =>
    new Response(JSON.stringify({ error: "unreachable" }), { status: 503, headers: { "content-type": "application/json" } });
  const drive = new DriveApi(new ApiClient({ baseUrl: "http://core.test", fetch: offlineFetch }));
  const stored = await layer.readZone();
  assert.ok(stored !== null, "the zone is in the store");
  serveHouseZoneFrom({ business_zone: stored });
  assert.equal(houseZone(), "America/Los_Angeles");
  assert.equal(await drive.dealersDay(PACIFIC_EVENING), PACIFIC_DAY);
  assert.equal(houseZone(), "America/Los_Angeles", "a failed config read never overwrites the restored zone");
  const answer = await layer.runs(PACIFIC_DAY);
  if (!answer.ok) return assert.fail("the run sheet should read back");
  assert.equal(answer.value.runs.length, 1);
  assert.equal(answer.value.runs[0].run_no, "RUN-P1");
});
