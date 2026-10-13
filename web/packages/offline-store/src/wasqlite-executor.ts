// The wa-sqlite engine behind the SqlExecutor seam. Runs in the OPFS worker
// on AccessHandlePoolVFS, and in node on the memory VFS so the same SQL and
// the same executor contract are tested without a browser.
import SQLiteESMFactory from "wa-sqlite/dist/wa-sqlite.mjs";
import * as SQLite from "wa-sqlite";
import { serialQueue, type ClosableSqlExecutor, type SqlRow, type SqlStatement, type SqlValue } from "./sql.js";

export type WaSqliteVfs = "memory" | "opfs";

export interface WaSqliteOptions {
  vfs: WaSqliteVfs;
  /** Database file name inside the VFS. */
  name?: string;
  /** OPFS directory for the access handle pool. */
  directory?: string;
  /** The wasm bytes, for a host that cannot fetch them (node). Browsers leave this out. */
  wasmBinary?: Uint8Array;
}

export async function createWaSqliteExecutor(options: WaSqliteOptions): Promise<ClosableSqlExecutor> {
  const module = await SQLiteESMFactory(options.wasmBinary ? { wasmBinary: options.wasmBinary } : undefined);
  const sqlite3 = SQLite.Factory(module);
  if (options.vfs === "memory") {
    const { MemoryVFS } = await import("wa-sqlite/src/examples/MemoryVFS.js");
    sqlite3.vfs_register(new MemoryVFS() as unknown as SQLiteVFS, true);
  } else {
    const { AccessHandlePoolVFS } = await import("wa-sqlite/src/examples/AccessHandlePoolVFS.js");
    const vfs = new AccessHandlePoolVFS(options.directory ?? "/hh-offline-store");
    await vfs.isReady;
    sqlite3.vfs_register(vfs as unknown as SQLiteVFS, true);
  }
  const db = await sqlite3.open_v2(options.name ?? "offline-store");
  const serial = serialQueue();

  async function exec(sql: string, params: SqlValue[] | undefined, collect: boolean): Promise<SqlRow[]> {
    const rows: SqlRow[] = [];
    for await (const stmt of sqlite3.statements(db, sql)) {
      if (params && params.length > 0) sqlite3.bind_collection(stmt, params);
      const names = sqlite3.column_names(stmt);
      while ((await sqlite3.step(stmt)) === SQLite.SQLITE_ROW) {
        if (!collect) continue;
        const values = sqlite3.row(stmt);
        rows.push(Object.fromEntries(names.map((n: string, i: number) => [n, values[i] as SqlValue])));
      }
    }
    return rows;
  }

  return {
    run: (sql, params) => serial(async () => void (await exec(sql, params, false))),
    all: <T = SqlRow>(sql: string, params?: SqlValue[]) => serial(async () => (await exec(sql, params, true)) as T[]),
    atomic: (statements: SqlStatement[]) =>
      serial(async () => {
        await exec("BEGIN", undefined, false);
        try {
          for (const s of statements) await exec(s.sql, s.params, false);
          await exec("COMMIT", undefined, false);
        } catch (error) {
          await exec("ROLLBACK", undefined, false).catch(() => undefined);
          throw error;
        }
      }),
    close: () => serial(async () => void (await sqlite3.close(db))),
  };
}
