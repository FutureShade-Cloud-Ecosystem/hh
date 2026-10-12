// The one schema both backends carry (context 4.5): a projection table per
// declared scope, the outbox, sync_state, attachments_queue, and the conflict
// cards. Dialect is plain SQLite; nothing here is engine specific.
import type { SqlExecutor, SqlStatement } from "./sql.js";

export const SCHEMA_VERSION = 3;

/** The shape of a scope name; it is spliced into table names, so it is checked before any SQL. */
export const SCOPE_NAME = /^[a-z][a-z_]{0,63}$/;

/** The entity table of one scope. The name is validated because it is spliced into SQL. */
export function entityTable(scope: string): string {
  if (!SCOPE_NAME.test(scope)) {
    throw new Error(`offline-store: scope name ${JSON.stringify(scope)} is not lower snake case`);
  }
  return `ent_${scope}`;
}

const FIXED_DDL = [
  `CREATE TABLE IF NOT EXISTS outbox (
     seq INTEGER PRIMARY KEY AUTOINCREMENT,
     idempotency_key TEXT NOT NULL UNIQUE,
     scope TEXT NOT NULL,
     op TEXT NOT NULL CHECK (op IN ('create', 'update', 'delete')),
     entity TEXT,
     entity_id TEXT,
     payload TEXT NOT NULL,
     base_seq INTEGER,
     body_hash TEXT NOT NULL,
     status TEXT NOT NULL CHECK (status IN ('pending', 'applied', 'conflict', 'rejected')),
     attempts INTEGER NOT NULL DEFAULT 0,
     error TEXT,
     server_seq INTEGER,
     created_at TEXT NOT NULL,
     sent INTEGER NOT NULL DEFAULT 0,
     stall TEXT CHECK (stall IS NULL OR stall IN ('scope_refused', 'check_applied')),
     stall_status INTEGER,
     settled TEXT CHECK (settled IS NULL OR settled IN ('applied', 'withdrawn', 'resend')),
     settled_at TEXT
   )`,
  `CREATE INDEX IF NOT EXISTS outbox_status_seq ON outbox (status, seq)`,
  `CREATE TABLE IF NOT EXISTS sync_state (
     scope TEXT PRIMARY KEY,
     last_seq INTEGER NOT NULL,
     last_pull_at TEXT
   )`,
  `CREATE TABLE IF NOT EXISTS cards (
     id TEXT PRIMARY KEY,
     client_id TEXT NOT NULL UNIQUE,
     scope TEXT NOT NULL,
     kind TEXT NOT NULL CHECK (kind IN ('superseded', 'merge_report', 'review', 'rejected')),
     policy TEXT,
     entity_id TEXT,
     server_state TEXT NOT NULL,
     client_payload TEXT NOT NULL,
     error TEXT,
     status TEXT NOT NULL CHECK (status IN ('open', 'dismissed')),
     created_at TEXT NOT NULL
   )`,
  `CREATE TABLE IF NOT EXISTS attachments_queue (
     id TEXT PRIMARY KEY,
     scope TEXT NOT NULL,
     entity_id TEXT,
     local_ref TEXT NOT NULL,
     mime TEXT,
     size_bytes INTEGER,
     status TEXT NOT NULL CHECK (status IN ('pending', 'uploading', 'uploaded', 'failed')),
     attempts INTEGER NOT NULL DEFAULT 0,
     error TEXT,
     remote_ref TEXT,
     created_at TEXT NOT NULL
   )`,
];

function entityDdl(scope: string): string {
  return `CREATE TABLE IF NOT EXISTS ${entityTable(scope)} (
     id TEXT PRIMARY KEY,
     seq INTEGER NOT NULL,
     data TEXT NOT NULL,
     deleted INTEGER NOT NULL DEFAULT 0
   )`;
}

/**
 * Schema 2 added `sent` (a request for the row may have reached the server and
 * was not definitively answered) and the stall record to the outbox. A table
 * from schema 1 gets the columns; an attempt it already counted may have
 * reached the server, so those rows start as sent.
 * Schema 3 added `settled` and `settled_at`, the audit of a person's
 * resolution of a stalled row. Each step is applied on its own, so a table of
 * any earlier schema reaches this one in a single migrate.
 */
async function upgradeOutbox(sql: SqlExecutor): Promise<void> {
  const cols = (await sql.all<{ name: string }>("SELECT name FROM pragma_table_info('outbox')")).map((c) => c.name);
  if (cols.length === 0) return;
  const steps: SqlStatement[] = [];
  if (!cols.includes("sent")) {
    steps.push(
      { sql: "ALTER TABLE outbox ADD COLUMN sent INTEGER NOT NULL DEFAULT 0" },
      { sql: "ALTER TABLE outbox ADD COLUMN stall TEXT CHECK (stall IS NULL OR stall IN ('scope_refused', 'check_applied'))" },
      { sql: "ALTER TABLE outbox ADD COLUMN stall_status INTEGER" },
      { sql: "UPDATE outbox SET sent = 1 WHERE attempts > 0" },
    );
  }
  if (!cols.includes("settled")) {
    steps.push(
      { sql: "ALTER TABLE outbox ADD COLUMN settled TEXT CHECK (settled IS NULL OR settled IN ('applied', 'withdrawn', 'resend'))" },
      { sql: "ALTER TABLE outbox ADD COLUMN settled_at TEXT" },
    );
  }
  if (steps.length > 0) await sql.atomic(steps);
}

/**
 * Create or extend the schema. Idempotent; a later call with more scopes adds
 * their tables and keeps every row. Every scope name is validated before any
 * SQL runs, and a database written by a newer schema is refused rather than
 * read with the wrong shape.
 */
export async function migrate(sql: SqlExecutor, scopes: readonly string[]): Promise<void> {
  const entityStatements: SqlStatement[] = scopes.map((scope) => ({ sql: entityDdl(scope) }));
  await sql.run("CREATE TABLE IF NOT EXISTS schema_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)");
  const found = await sql.all<{ value: string }>("SELECT value FROM schema_meta WHERE key = 'schema_version'");
  if (found.length > 0 && Number(found[0]!.value) > SCHEMA_VERSION) {
    throw new Error(`offline-store: database schema ${found[0]!.value} is newer than this build (${SCHEMA_VERSION})`);
  }
  await upgradeOutbox(sql);
  await sql.atomic([
    ...FIXED_DDL.map((ddl) => ({ sql: ddl })),
    ...entityStatements,
    {
      sql: "INSERT INTO schema_meta (key, value) VALUES ('schema_version', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
      params: [String(SCHEMA_VERSION)],
    },
  ]);
}
