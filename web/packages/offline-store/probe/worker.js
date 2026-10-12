// Dedicated worker: owns the wa-sqlite database. The synchronous build plus
// AccessHandlePoolVFS keeps every OPFS call off the main thread.
import SQLiteESMFactory from 'wa-sqlite/dist/wa-sqlite.mjs';
import * as SQLite from 'wa-sqlite';
import { AccessHandlePoolVFS } from 'wa-sqlite/src/examples/AccessHandlePoolVFS.js';
import { MemoryVFS } from 'wa-sqlite/src/examples/MemoryVFS.js';

let kind = 'ahp';
let sqlite3;
let db;

async function open() {
  const module = await SQLiteESMFactory();
  sqlite3 = SQLite.Factory(module);
  const vfs = kind === 'memory' ? new MemoryVFS() : new AccessHandlePoolVFS('/hh-offline-probe8');
  if (vfs.isReady) await vfs.isReady;
  sqlite3.vfs_register(vfs, true);
  db = await sqlite3.open_v2('offline-store');
  await sqlite3.exec(
    db,
    `CREATE TABLE IF NOT EXISTS outbox (
       seq INTEGER PRIMARY KEY AUTOINCREMENT,
       idempotency_key TEXT NOT NULL UNIQUE,
       scope TEXT NOT NULL,
       body TEXT NOT NULL,
       created_at TEXT NOT NULL
     )`,
  );
}

async function insert() {
  // Probe only: the values are interpolated because they are a fresh UUID and an ISO string.
  // The real outbox must bind parameters (sqlite3.statements with bind), never build SQL by string.
  const key = crypto.randomUUID();
  await sqlite3.exec(
    db,
    `INSERT INTO outbox (idempotency_key, scope, body, created_at)
     VALUES ('${key}', 'yard-pick', '{"op":"pick","qty":3}', '${new Date().toISOString()}')`,
  );
  return key;
}

async function read() {
  const rows = [];
  await sqlite3.exec(
    db,
    'SELECT seq, idempotency_key, scope, body, created_at FROM outbox ORDER BY seq',
    (row, columns) => rows.push(Object.fromEntries(columns.map((c, i) => [c, row[i]]))),
  );
  return rows;
}

let ready;

self.onmessage = async ({ data: { id, op, vfs } }) => {
  if (op === 'init') {
    kind = vfs;
    ready = open().then(
      () => ({ ok: true }),
      (e) => ({ ok: false, error: String(e && e.message ? e.message : e) }),
    );
    const s = await ready;
    return self.postMessage({ id, kind, ...(s.ok ? { result: kind } : { error: s.error }) });
  }
  const state = await ready;
  if (!state.ok) return self.postMessage({ id, error: state.error });
  try {
    const result = op === 'insert' ? await insert() : op === 'read' ? await read() : null;
    self.postMessage({ id, result });
  } catch (e) {
    self.postMessage({ id, error: String(e && e.message ? e.message : e) });
  }
};
