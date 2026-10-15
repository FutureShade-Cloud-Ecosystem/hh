// The generic controller of a desk app's offline layer: it opens the person's
// store, runs the sync cycles, reports the banner's mode, the copy's age and
// the outbox depth, and releases the store on close. An app that is read cache
// only (the Counter Till) runs exactly this and nothing more: no keeper, no
// outbox, nothing to push.
import { test } from "node:test";
import assert from "node:assert/strict";
import { createSqliteBackend } from "@hardscapehouse/offline-store";
import { nodeSqliteExecutor } from "@hardscapehouse/offline-store/testing";
import { fixedSignal } from "../src/signal.ts";
import { WorkDesk } from "../src/desk.ts";
import { openWorkOfflineOver } from "../src/sync.ts";
import { BRANCH, rig } from "./_rig.mjs";

async function till(options = {}) {
  const r = await rig("counter-till");
  const signal = fixedSignal(true);
  const events = [];
  const desk = new WorkDesk({
    appId: "counter-till",
    signal,
    transport: r.transport,
    dbName: "test",
    open: async (o) => openWorkOfflineOver(createSqliteBackend(nodeSqliteExecutor()), o),
    ...options,
  });
  desk.subscribe(() => events.push("changed"));
  return { r, signal, desk, events };
}

test("opening a read cache only app pulls its scopes and answers reads from the store; there is nothing to send", async () => {
  const { r, desk } = await till();
  await desk.open(BRANCH);
  await desk.cycle();
  assert.equal(desk.available, true);
  assert.equal((await desk.reader().parkedTickets()).rows.length, 1);
  assert.equal(await desk.waitingCount(), 0);
  assert.ok(!r.server.requests.some((q) => q.kind === "push"), "no push ever left");
  assert.equal(desk.bannerMode(), "");
});

test("the till's store admits no write at all: its layer cannot enqueue", async () => {
  const { desk } = await till();
  await desk.open(BRANCH);
  const answer = await desk.layer().store.enqueue({ scope: "parked_tickets", op: "create", payload: { id: "x" } });
  assert.equal(answer.ok, false);
});

test("offline the banner says so; online again it says nothing", async () => {
  const { r, signal, desk } = await till();
  await desk.open(BRANCH);
  await desk.cycle();
  signal.set(false);
  r.server.setOffline(true);
  await desk.setOnline(false);
  assert.equal(desk.bannerMode(), "offline");
  signal.set(true);
  r.server.setOffline(false);
  await desk.setOnline(true);
  await desk.cycle();
  assert.equal(desk.bannerMode(), "");
});

test("the cached note follows the read through hooks: a cached read sets it, a live read clears it, and a repeat tells nobody", async () => {
  const { desk, events } = await till();
  await desk.open(BRANCH);
  events.length = 0;
  desk.hooks.onCached("Pulled 2 min ago");
  desk.hooks.onCached("Pulled 2 min ago");
  assert.equal(desk.cachedWords(), "Pulled 2 min ago");
  assert.equal(events.length, 1);
  desk.hooks.onLive();
  desk.hooks.onLive();
  assert.equal(desk.cachedWords(), "");
  assert.equal(events.length, 2);
});

test("a host that cannot carry a store leaves the app online as it was", async () => {
  const { desk } = await till({ open: async () => ({ layer: null, readOnly: false }) });
  await desk.open(BRANCH);
  assert.equal(desk.available, false);
  assert.equal(desk.reader(), null);
  assert.equal(desk.bannerMode(), "");
  assert.throws(() => desk.layer(), /not open/);
});

test("a second tab says it is read only", async () => {
  const { desk } = await till({ open: async () => ({ layer: null, readOnly: true }) });
  await desk.open(BRANCH);
  assert.equal(desk.bannerMode(), "read-only");
});

test("the person's hooks run at open and at close, and close releases the store", async () => {
  const seen = [];
  const { desk } = await till({ onOpened: () => seen.push("opened"), onClosing: () => seen.push("closing") });
  await desk.open(BRANCH);
  await desk.close();
  assert.deepEqual(seen, ["opened", "closing"]);
  assert.equal(desk.available, false);
  assert.equal(desk.reader(), null);
});

test("opening twice opens once", async () => {
  let opens = 0;
  const { desk } = await till({
    open: async (o) => {
      opens += 1;
      return openWorkOfflineOver(createSqliteBackend(nodeSqliteExecutor()), o);
    },
  });
  await desk.open(BRANCH);
  await desk.open(BRANCH);
  assert.equal(opens, 1);
});
