// The layout store on the Node runner: the queue and the closed flag hold at
// the unit level, where a held request is a promise the test resolves by hand,
// before any of it is written against a screen. Every guard the store owns has
// its probe here; the screen level story (whose session a save rides under)
// is the Playwright suite's.
import { test } from "node:test";
import assert from "node:assert/strict";
import { WorkspaceStore, DEFAULT_WORKSPACE } from "../src/workspace-store.js";

/** A client whose every send the test holds until it opens the gate, in the shape the store uses. */
function heldClient() {
  const calls = [];
  const gateFor = (call) => {
    let open = () => undefined;
    const opened = new Promise((resolve) => {
      open = resolve;
    });
    return {
      promise: opened.then(() => call()),
      open,
    };
  };
  const client = {
    calls,
    putMyWorkspace: (body) => {
      const held = gateFor(() => ({ ok: true, value: { ...body, source: "user" } }));
      calls.push({ kind: "put", body, open: held.open });
      return held.promise;
    },
    deleteMyWorkspace: () => {
      const held = gateFor(() => ({ ok: true, value: { reset: true } }));
      calls.push({ kind: "delete", body: null, open: held.open });
      return held.promise;
    },
  };
  return client;
}

/** Let the store's own continuations run after a gate opened, so a queued send has had its chance. */
async function turns(count = 3) {
  for (let i = 0; i < count; i += 1) await new Promise((resolve) => setTimeout(resolve, 0));
}

test("a save asked while one is out is sent only after it, with the row as it is then", async () => {
  const client = heldClient();
  const store = new WorkspaceStore(client, null);
  const first = store.save();
  await turns();
  store.state.splitPct = 70;
  const second = store.save();
  await turns();
  assert.equal(client.calls.length, 1, "the first save is still out: no second one is sent beside it");
  client.calls[0].open();
  await turns();
  assert.equal(client.calls.length, 2);
  assert.equal(client.calls[1].body.splitPct, 70, "the queued save carries the latest row");
  client.calls[1].open();
  await Promise.all([first, second]);
});

test("a closed store sends no further save after the one already out, and drops what is queued", async () => {
  const client = heldClient();
  const store = new WorkspaceStore(client, null);
  const first = store.save();
  await turns();
  store.state.splitPct = 70;
  const queued = store.save(); // waits behind the first
  await turns();
  store.close();
  client.calls[0].open(); // the save that was already on its way answers
  await Promise.allSettled([first, queued]);
  await turns();
  assert.equal(client.calls.length, 1, "the queued save is dropped: a stopped store sends nothing more");
});

test("a closed store sends nothing for a save asked after it was closed", async () => {
  const client = heldClient();
  const store = new WorkspaceStore(client, null);
  store.close();
  await store.save();
  await turns();
  assert.equal(client.calls.length, 0);
});

test("a closed store sends nothing for a debounced save asked after it was closed", async () => {
  const client = heldClient();
  const store = new WorkspaceStore(client, null);
  store.close();
  store.scheduleSave();
  await new Promise((resolve) => setTimeout(resolve, 500));
  assert.equal(client.calls.length, 0);
});

test("a closed store sends nothing for a reset asked after it was closed", async () => {
  const client = heldClient();
  const store = new WorkspaceStore(client, null);
  store.close();
  const landed = await store.reset();
  await turns();
  assert.equal(landed, false);
  assert.equal(client.calls.length, 0);
});

test("the reset goes through the same queue as the saves: it is not sent while a save is still out", async () => {
  const client = heldClient();
  const store = new WorkspaceStore(client, null);
  const save = store.save();
  await turns();
  const reset = store.reset();
  await turns();
  assert.equal(client.calls.length, 1, "the save is still out: the reset does not jump the queue");
  assert.equal(client.calls[0].kind, "put");
  client.calls[0].open();
  await turns();
  assert.equal(client.calls.length, 2);
  assert.equal(client.calls[1].kind, "delete", "the reset is sent once the save before it has answered");
  client.calls[1].open();
  await Promise.all([save, reset]);
  assert.equal(await reset, true);
  assert.deepEqual(store.state, { ...DEFAULT_WORKSPACE });
});

test("a save queued behind a reset is sent after the reset, not beside it", async () => {
  const client = heldClient();
  const store = new WorkspaceStore(client, null);
  const reset = store.reset();
  await turns();
  store.state.splitPct = 70;
  const save = store.save();
  await turns();
  assert.equal(client.calls.length, 1);
  assert.equal(client.calls[0].kind, "delete");
  client.calls[0].open();
  await turns();
  assert.equal(client.calls.length, 2);
  assert.equal(client.calls[1].kind, "put");
  client.calls[1].open();
  await Promise.all([reset, save]);
});

test("flush waits for a queued save to be sent and answered, and a closed flush waits for nothing new", async () => {
  const client = heldClient();
  const store = new WorkspaceStore(client, null);
  const first = store.save();
  await turns();
  store.state.splitPct = 70;
  void store.save();
  await turns();
  const flushed = store.flush();
  client.calls[0].open();
  await turns();
  client.calls[1].open();
  await flushed;
  store.close();
  await store.flush();
});
