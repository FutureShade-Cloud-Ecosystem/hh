// The two backends over one SqlExecutor contract: the SQLite backend takes an
// injected executor (the Tauri SQL plugin plugs in here later); the OPFS
// backend owns a worker holding wa-sqlite, behind a single tab owner. A second
// tab degrades to read cache only and the backend says so as a state.
import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createSqliteBackend } from "../src/backend-sqlite.ts";
import { createOpfsBackend, StoreReadOnlyError } from "../src/backend-opfs.ts";
import { createWaSqliteExecutor } from "../src/wasqlite-executor.ts";
import { migrate } from "../src/schema.ts";
import { nodeSqliteExecutor, waSqliteWasm, FakeLocks, FakeWorker } from "../src/testing.ts";
import { executorContract } from "./executor-contract.mjs";

executorContract("node:sqlite executor", async () => nodeSqliteExecutor());
executorContract("wa-sqlite executor (memory VFS in node)", async () => createWaSqliteExecutor({ vfs: "memory", wasmBinary: waSqliteWasm() }));

test("the wa-sqlite engine runs the whole schema, not just toy tables", async () => {
  const sql = await createWaSqliteExecutor({ vfs: "memory", wasmBinary: waSqliteWasm() });
  await migrate(sql, ["pick_tasks", "pod"]);
  const names = (await sql.all("SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name")).map((r) => r.name);
  assert.deepEqual(names, ["attachments_queue", "cards", "ent_pick_tasks", "ent_pod", "outbox", "schema_meta", "sync_state"]);
});

test("sqlite backend: reports kind sqlite, owner, and passes the injected executor through", async () => {
  const exec = nodeSqliteExecutor();
  const backend = createSqliteBackend(exec);
  assert.equal(backend.kind, "sqlite");
  assert.equal(backend.ownership(), "owner");
  await backend.sql.run("CREATE TABLE t (x INTEGER)");
  await backend.sql.run("INSERT INTO t (x) VALUES (4)");
  assert.deepEqual(await exec.all("SELECT x FROM t"), [{ x: 4 }]);
  await backend.close();
});

test("sqlite backend: a file database keeps its rows across close and reopen", async () => {
  const dir = mkdtempSync(join(tmpdir(), "hh-offline-store-"));
  try {
    const path = join(dir, "store.db");
    const first = createSqliteBackend(nodeSqliteExecutor(path));
    await migrate(first.sql, ["pick_tasks"]);
    await first.sql.run("INSERT INTO sync_state (scope, last_seq, last_pull_at) VALUES ('pick_tasks', 9, 't')");
    await first.close();
    const second = createSqliteBackend(nodeSqliteExecutor(path));
    assert.deepEqual(await second.sql.all("SELECT scope, last_seq FROM sync_state"), [{ scope: "pick_tasks", last_seq: 9 }]);
    await second.close();
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("opfs backend: the first tab takes the lock, starts the worker, and owns the store", async () => {
  const locks = new FakeLocks();
  const worker = new FakeWorker(nodeSqliteExecutor());
  const backend = await createOpfsBackend({ dbName: "hh", locks, createWorker: () => worker });
  assert.equal(backend.kind, "opfs");
  assert.equal(backend.ownership(), "owner");
  assert.deepEqual(locks.held(), ["hh-offline-store:hh"]);
  assert.deepEqual(worker.initCalls, ["hh"]);
  await backend.sql.run("CREATE TABLE t (x INTEGER)");
  await backend.sql.atomic([{ sql: "INSERT INTO t (x) VALUES (?)", params: [1] }, { sql: "INSERT INTO t (x) VALUES (?)", params: [2] }]);
  assert.deepEqual(await backend.sql.all("SELECT x FROM t ORDER BY x"), [{ x: 1 }, { x: 2 }]);
  await backend.close();
  assert.deepEqual(locks.held(), []);
  assert.equal(worker.terminated, true);
});

test("opfs backend: a second tab does not get the lock, degrades to read cache only, and says so", async () => {
  const locks = new FakeLocks();
  const first = await createOpfsBackend({ dbName: "hh", locks, createWorker: () => new FakeWorker(nodeSqliteExecutor()) });
  let secondWorkerStarted = false;
  const states = [];
  const second = await createOpfsBackend({
    dbName: "hh",
    locks,
    createWorker: () => {
      secondWorkerStarted = true;
      return new FakeWorker(nodeSqliteExecutor());
    },
    onOwnership: (s) => states.push(s),
  });
  assert.equal(first.ownership(), "owner");
  assert.equal(second.ownership(), "read_cache_only");
  assert.equal(secondWorkerStarted, false, "a tab without the lock must not open the database");
  assert.deepEqual(states, ["read_cache_only"]);
  await assert.rejects(second.sql.run("CREATE TABLE t (x INTEGER)"), StoreReadOnlyError);
  await assert.rejects(second.sql.atomic([{ sql: "SELECT 1" }]), StoreReadOnlyError);
  assert.deepEqual(await second.sql.all("SELECT 1 AS one"), []);
  await first.close();
  await second.close();
});

test("opfs backend: a read fallback answers reads in the degraded tab, writes still refuse", async () => {
  const locks = new FakeLocks();
  await createOpfsBackend({ dbName: "hh", locks, createWorker: () => new FakeWorker(nodeSqliteExecutor()) });
  const cache = nodeSqliteExecutor();
  await cache.run("CREATE TABLE t (x INTEGER)");
  await cache.run("INSERT INTO t (x) VALUES (3)");
  const second = await createOpfsBackend({ dbName: "hh", locks, createWorker: () => new FakeWorker(nodeSqliteExecutor()), readFallback: cache });
  assert.equal(second.ownership(), "read_cache_only");
  assert.deepEqual(await second.sql.all("SELECT x FROM t"), [{ x: 3 }]);
  await assert.rejects(second.sql.run("INSERT INTO t (x) VALUES (4)"), StoreReadOnlyError);
  assert.deepEqual(await cache.all("SELECT x FROM t"), [{ x: 3 }]);
});

test("opfs backend: the browser's exclusive handle error (probe 8) also degrades, and releases the lock", async () => {
  const locks = new FakeLocks();
  const worker = new FakeWorker(nodeSqliteExecutor(), {
    initError: "Failed to execute 'createSyncAccessHandle' on 'FileSystemFileHandle': Access Handles cannot be created if there is another open Access Handle or Writable",
  });
  const states = [];
  const backend = await createOpfsBackend({ dbName: "hh", locks, createWorker: () => worker, onOwnership: (s) => states.push(s) });
  assert.equal(backend.ownership(), "read_cache_only");
  assert.deepEqual(states, ["read_cache_only"]);
  assert.deepEqual(locks.held(), [], "a tab that cannot open the database must not keep the lock");
  assert.equal(worker.terminated, true);
});

test("opfs backend: an unrelated worker failure is an error, not a silent degrade", async () => {
  const locks = new FakeLocks();
  const worker = new FakeWorker(nodeSqliteExecutor(), { initError: "out of memory" });
  await assert.rejects(createOpfsBackend({ dbName: "hh", locks, createWorker: () => worker }), /out of memory/);
  assert.deepEqual(locks.held(), []);
});

test("opfs backend: a worker error on a statement rejects that call and leaves the backend usable", async () => {
  const locks = new FakeLocks();
  const backend = await createOpfsBackend({ dbName: "hh", locks, createWorker: () => new FakeWorker(nodeSqliteExecutor()) });
  await assert.rejects(backend.sql.run("INSERT INTO missing (x) VALUES (1)"), /missing/);
  await backend.sql.run("CREATE TABLE ok (x INTEGER)");
  assert.deepEqual(await backend.sql.all("SELECT COUNT(*) AS n FROM ok"), [{ n: 0 }]);
});

test("opfs backend: the lock is handed back when the previous owner closes", async () => {
  const locks = new FakeLocks();
  const first = await createOpfsBackend({ dbName: "hh", locks, createWorker: () => new FakeWorker(nodeSqliteExecutor()) });
  await first.close();
  const again = await createOpfsBackend({ dbName: "hh", locks, createWorker: () => new FakeWorker(nodeSqliteExecutor()) });
  assert.equal(again.ownership(), "owner");
});

// ---- the worker settle guarantee ------------------------------------------------------------

async function ownerWith(worker, extra = {}) {
  const locks = new FakeLocks();
  const states = [];
  const backend = await createOpfsBackend({ dbName: "hh", locks, createWorker: () => worker, onOwnership: (s) => states.push(s), ...extra });
  return { backend, locks, states };
}

test("opfs backend: a worker that dies rejects the call in flight, and every later call, instead of hanging", async () => {
  const worker = new FakeWorker(nodeSqliteExecutor());
  const { backend } = await ownerWith(worker);
  await backend.sql.run("CREATE TABLE t (x INTEGER)");
  worker.silent = true;
  const inFlight = backend.sql.run("INSERT INTO t (x) VALUES (1)");
  worker.die("out of memory");
  await assert.rejects(inFlight, /worker failed.*out of memory/);
  await assert.rejects(backend.sql.run("INSERT INTO t (x) VALUES (2)"), /worker/);
  await assert.rejects(backend.sql.atomic([{ sql: "SELECT 1" }]), /worker/);
  await assert.rejects(backend.sql.all("SELECT 1"), /worker/);
});

test("opfs backend: ownership reflects a dead worker, tells the shell, and gives the lock back", async () => {
  const worker = new FakeWorker(nodeSqliteExecutor());
  const { backend, locks, states } = await ownerWith(worker);
  assert.equal(backend.ownership(), "owner");
  worker.die();
  assert.equal(backend.ownership(), "read_cache_only");
  assert.deepEqual(states, ["owner", "read_cache_only"]);
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(locks.held(), [], "a tab with a dead worker must not keep the store from the next tab");
  assert.equal(worker.terminated, true);
  await backend.close();
});

test("opfs backend: a call the worker never answers times out and counts as a dead worker", async () => {
  const worker = new FakeWorker(nodeSqliteExecutor());
  const { backend, states } = await ownerWith(worker, { callTimeoutMs: 20 });
  worker.silent = true;
  await assert.rejects(backend.sql.run("CREATE TABLE t (x INTEGER)"), /timed out/);
  assert.equal(backend.ownership(), "read_cache_only");
  assert.deepEqual(states, ["owner", "read_cache_only"]);
  worker.silent = false;
  await assert.rejects(backend.sql.run("CREATE TABLE t (x INTEGER)"), /worker/);
});

test("opfs backend: a store over a dead worker refuses writes as read only instead of stalling", async () => {
  const { OfflineStore } = await import("../src/store.ts");
  const { scopeBookFor } = await import("../src/scopes.ts");
  const { baseRig, pick } = await import("./_rig.mjs");
  const worker = new FakeWorker(nodeSqliteExecutor());
  const { backend } = await ownerWith(worker);
  const store = await OfflineStore.open({ backend, scopes: scopeBookFor(["yard-pick", "drive-run"]) });
  worker.die();
  const res = await store.enqueue(pick(1));
  assert.deepEqual({ ok: res.ok, reason: res.reason }, { ok: false, reason: "read_only" });
  assert.deepEqual(await store.replay((await baseRig()).transport), { kind: "read_only" });
});
