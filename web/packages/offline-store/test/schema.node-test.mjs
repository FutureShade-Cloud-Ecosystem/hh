// One schema (context 4.5): local entity tables per scope, outbox, sync_state,
// attachments_queue, plus the conflict cards table. The migration is
// idempotent and refuses a scope name that could not be a table suffix.
import { test } from "node:test";
import assert from "node:assert/strict";
import { migrate, SCHEMA_VERSION, entityTable } from "../src/schema.ts";
import { nodeSqliteExecutor, MAX_TEST_STATEMENTS } from "../src/testing.ts";

async function tableNames(sql) {
  const rows = await sql.all("SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name");
  return rows.map((r) => r.name);
}

test("migrate creates the fixed tables and one entity table per scope", async () => {
  const sql = nodeSqliteExecutor();
  await migrate(sql, ["pick_tasks", "shortages"]);
  assert.deepEqual(await tableNames(sql), [
    "attachments_queue",
    "cards",
    "ent_pick_tasks",
    "ent_shortages",
    "outbox",
    "schema_meta",
    "sync_state",
  ]);
});

test("migrate is idempotent and records the schema version", async () => {
  const sql = nodeSqliteExecutor();
  await migrate(sql, ["pick_tasks"]);
  await sql.run(
    "INSERT INTO ent_pick_tasks (id, seq, data, deleted) VALUES ('11111111-1111-4111-8111-111111111111', 3, '{}', 0)",
  );
  await migrate(sql, ["pick_tasks"]);
  assert.equal((await sql.all("SELECT id FROM ent_pick_tasks")).length, 1);
  const meta = await sql.all("SELECT value FROM schema_meta WHERE key = 'schema_version'");
  assert.deepEqual(meta, [{ value: String(SCHEMA_VERSION) }]);
});

test("a later migrate adds the tables of newly declared scopes and keeps the old", async () => {
  const sql = nodeSqliteExecutor();
  await migrate(sql, ["pick_tasks"]);
  await migrate(sql, ["pick_tasks", "pod"]);
  assert.ok((await tableNames(sql)).includes("ent_pod"));
  assert.ok((await tableNames(sql)).includes("ent_pick_tasks"));
});

test("a scope name that is not lower snake case is refused before any SQL runs", async () => {
  const sql = nodeSqliteExecutor();
  for (const bad of ["", "Pick", "a-b", "a b", "a;drop table outbox", "1abc", "pick_tasks'--", "x".repeat(65)]) {
    await assert.rejects(migrate(sql, [bad]), /scope name/, `accepted ${JSON.stringify(bad)}`);
  }
  assert.deepEqual(await tableNames(sql), []);
  assert.throws(() => entityTable("Bad Name"), /scope name/);
  assert.equal(entityTable("pick_tasks"), "ent_pick_tasks");
});

test("the outbox keys are unique and the order column is monotonic", async () => {
  const sql = nodeSqliteExecutor();
  await migrate(sql, []);
  const ins = (key) =>
    sql.run(
      `INSERT INTO outbox (idempotency_key, scope, op, entity_id, payload, base_seq, body_hash, status, attempts, created_at)
       VALUES (?, 'pick_tasks', 'update', NULL, '{}', NULL, 'h', 'pending', 0, 't')`,
      [key],
    );
  await ins("k1");
  await ins("k2");
  await assert.rejects(ins("k1"), /UNIQUE/i);
  const rows = await sql.all("SELECT idempotency_key FROM outbox ORDER BY seq");
  assert.deepEqual(rows.map((r) => r.idempotency_key), ["k1", "k2"]);
});

test("outbox status and card status are constrained to their vocabularies", async () => {
  const sql = nodeSqliteExecutor();
  await migrate(sql, []);
  await assert.rejects(
    sql.run(
      `INSERT INTO outbox (idempotency_key, scope, op, entity_id, payload, base_seq, body_hash, status, attempts, created_at)
       VALUES ('k', 's', 'update', NULL, '{}', NULL, 'h', 'bogus', 0, 't')`,
    ),
    /CHECK/i,
  );
  await assert.rejects(
    sql.run(
      `INSERT INTO cards (id, client_id, scope, kind, policy, entity_id, server_state, client_payload, error, status, created_at)
       VALUES ('c', 'k', 's', 'bogus', 'manual', NULL, '{}', '{}', NULL, 'open', 't')`,
    ),
    /CHECK/i,
  );
});

test("the outbox carries the sent flag and the stall columns, and a database from before them is upgraded in place", async () => {
  const sql = nodeSqliteExecutor();
  // The first schema's outbox, with a row in it.
  await sql.run(`CREATE TABLE outbox (
     seq INTEGER PRIMARY KEY AUTOINCREMENT, idempotency_key TEXT NOT NULL UNIQUE, scope TEXT NOT NULL,
     op TEXT NOT NULL CHECK (op IN ('create', 'update', 'delete')), entity TEXT, entity_id TEXT, payload TEXT NOT NULL,
     base_seq INTEGER, body_hash TEXT NOT NULL,
     status TEXT NOT NULL CHECK (status IN ('pending', 'applied', 'conflict', 'rejected')),
     attempts INTEGER NOT NULL DEFAULT 0, error TEXT, server_seq INTEGER, created_at TEXT NOT NULL)`);
  await sql.run(`INSERT INTO outbox (idempotency_key, scope, op, payload, body_hash, status, attempts, created_at)
                 VALUES ('k1', 'pick_tasks', 'update', '{}', 'h', 'pending', 2, 't')`);
  await sql.run("CREATE TABLE schema_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)");
  await sql.run("INSERT INTO schema_meta (key, value) VALUES ('schema_version', '1')");
  await migrate(sql, ["pick_tasks"]);
  await migrate(sql, ["pick_tasks"]);
  const cols = (await sql.all("SELECT name FROM pragma_table_info('outbox')")).map((c) => c.name);
  for (const c of ["sent", "stall", "stall_status"]) assert.ok(cols.includes(c), `no ${c} column`);
  const [row] = await sql.all("SELECT attempts, sent, stall, stall_status FROM outbox");
  // An attempt made before the flag existed may have reached the server: it counts as sent.
  assert.deepEqual(row, { attempts: 2, sent: 1, stall: null, stall_status: null });
  const meta = await sql.all("SELECT value FROM schema_meta WHERE key = 'schema_version'");
  assert.deepEqual(meta, [{ value: String(SCHEMA_VERSION) }]);
  assert.ok(SCHEMA_VERSION >= 2);
});

test("the outbox carries the settlement audit columns, and a schema 2 database is upgraded in place, keeping its rows and stalls", async () => {
  const sql = nodeSqliteExecutor();
  await sql.run(`CREATE TABLE outbox (
     seq INTEGER PRIMARY KEY AUTOINCREMENT, idempotency_key TEXT NOT NULL UNIQUE, scope TEXT NOT NULL,
     op TEXT NOT NULL CHECK (op IN ('create', 'update', 'delete')), entity TEXT, entity_id TEXT, payload TEXT NOT NULL,
     base_seq INTEGER, body_hash TEXT NOT NULL,
     status TEXT NOT NULL CHECK (status IN ('pending', 'applied', 'conflict', 'rejected')),
     attempts INTEGER NOT NULL DEFAULT 0, error TEXT, server_seq INTEGER, created_at TEXT NOT NULL,
     sent INTEGER NOT NULL DEFAULT 0,
     stall TEXT CHECK (stall IS NULL OR stall IN ('scope_refused', 'check_applied')),
     stall_status INTEGER)`);
  await sql.run(`INSERT INTO outbox (idempotency_key, scope, op, payload, body_hash, status, attempts, sent, stall, stall_status, created_at)
                 VALUES ('k1', 'pick_tasks', 'update', '{}', 'h', 'pending', 1, 1, 'check_applied', 400, 't')`);
  await sql.run("CREATE TABLE schema_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)");
  await sql.run("INSERT INTO schema_meta (key, value) VALUES ('schema_version', '2')");
  await migrate(sql, ["pick_tasks"]);
  await migrate(sql, ["pick_tasks"]);
  const cols = (await sql.all("SELECT name FROM pragma_table_info('outbox')")).map((c) => c.name);
  for (const c of ["settled", "settled_at"]) assert.ok(cols.includes(c), `no ${c} column`);
  const [row] = await sql.all("SELECT attempts, sent, stall, stall_status, settled, settled_at FROM outbox");
  assert.deepEqual(row, { attempts: 1, sent: 1, stall: "check_applied", stall_status: 400, settled: null, settled_at: null });
  await assert.rejects(sql.run("UPDATE outbox SET settled = 'bogus'"), /CHECK/i);
  assert.equal(SCHEMA_VERSION, 3);
  assert.deepEqual(await sql.all("SELECT value FROM schema_meta WHERE key = 'schema_version'"), [{ value: "3" }]);
});

test("a schema 1 outbox is upgraded through both steps in one migrate", async () => {
  const sql = nodeSqliteExecutor();
  await sql.run(`CREATE TABLE outbox (
     seq INTEGER PRIMARY KEY AUTOINCREMENT, idempotency_key TEXT NOT NULL UNIQUE, scope TEXT NOT NULL,
     op TEXT NOT NULL CHECK (op IN ('create', 'update', 'delete')), entity TEXT, entity_id TEXT, payload TEXT NOT NULL,
     base_seq INTEGER, body_hash TEXT NOT NULL,
     status TEXT NOT NULL CHECK (status IN ('pending', 'applied', 'conflict', 'rejected')),
     attempts INTEGER NOT NULL DEFAULT 0, error TEXT, server_seq INTEGER, created_at TEXT NOT NULL)`);
  await sql.run(`INSERT INTO outbox (idempotency_key, scope, op, payload, body_hash, status, attempts, created_at)
                 VALUES ('k1', 'pick_tasks', 'update', '{}', 'h', 'pending', 2, 't')`);
  await migrate(sql, ["pick_tasks"]);
  const [row] = await sql.all("SELECT attempts, sent, stall, settled, settled_at FROM outbox");
  assert.deepEqual(row, { attempts: 2, sent: 1, stall: null, settled: null, settled_at: null });
});

test("the test executor refuses a loop that spins on the database, so a spinning mutant fails instead of hanging", async () => {
  const sql = nodeSqliteExecutor(":memory:", 50);
  await assert.rejects(
    (async () => {
      for (;;) await sql.all("SELECT 1");
    })(),
    /spinning/,
  );
  const batch = nodeSqliteExecutor(":memory:", 5);
  await assert.rejects(batch.atomic(Array.from({ length: 6 }, () => ({ sql: "SELECT 1" }))), /spinning/);
  assert.ok(MAX_TEST_STATEMENTS >= 20_000, "the default must leave every honest test far from the cap");
});
