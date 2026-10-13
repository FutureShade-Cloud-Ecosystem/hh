// HH Drive's offline data layer, over the store's node:sqlite executor and the
// fake sync server. The driver's reads compose from the store (runs and stops
// with staleness), the writes enqueue on their own scope (stops, pod, runs)
// and patch the local projection, and every conflict comes back as a card.
//
// Red first: written against the app before drive-offline.ts existed, failing
// on the missing module, then passing on the implementation.
import { test } from "node:test";
import assert from "node:assert/strict";

import { nodeSqliteExecutor, FakeSyncServer } from "@hardscapehouse/offline-store/testing";
import { OfflineStore, createSqliteBackend, scopeBookFor } from "@hardscapehouse/offline-store";
import { DriveOffline, bannerMode } from "../src/drive-offline.js";

const DRIVER = "de110000-0000-4000-9000-000000000003";
const RUN = "a1000000-0000-4000-9000-000000000005";
const STOP_1 = "5a000000-0000-4000-9000-000000000001";
const STOP_2 = "5a000000-0000-4000-9000-000000000002";

let keyCounter = 0;
function nextKey() {
  keyCounter += 1;
  return `00000000-0000-4000-8000-${String(keyCounter).padStart(12, "0")}`;
}

async function rig() {
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
  });
  await layer.cycle();
  return { layer, server, store };
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

test("offline, a proof of delivery lands on the local stop, then applies through the pod scope", async () => {
  const { layer, server, store } = await rig();
  server.setOffline(true);
  await layer.advance(STOP_1, "LOADED");
  await layer.advance(STOP_1, "EN_ROUTE");
  const signed = await layer.pod(STOP_1, "Receiver (demo)");
  assert.equal(signed.ok, true);
  const read = await layer.runs("2026-10-08");
  if (!read.ok) return assert.fail("the run sheet should read back");
  const stop = read.value.runs[0].stops[0];
  assert.equal(stop.status, "DELIVERED");
  assert.equal(stop.pod_received_by, "Receiver (demo)");
  server.setOffline(false);
  await layer.cycle();
  assert.equal(await layer.waiting(), 0);
  // The pod create rides the pod scope, after the stop writes it belongs to.
  const podPushes = server.requests.filter((r) => r.kind === "push" && r.scope === "pod");
  assert.equal(podPushes.length, 1);
  assert.equal(podPushes[0].ops[0].op, "create");
  assert.equal(podPushes[0].ops[0].payload.received_by, "Receiver (demo)");
  // No card and no stall: the proof applied clean.
  assert.equal((await layer.openCards()).length, 0);
  assert.equal((await layer.stalls()).length, 0);
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
