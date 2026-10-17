// The SQLite backend: the executor is injected, so the Tauri shell plugs the
// SQL plugin in behind it and a test plugs node:sqlite. The device owns its
// database, so this backend is always the owner.
import type { StorageBackend } from "./backend.js";
import type { ClosableSqlExecutor, SqlExecutor } from "./sql.js";

export function createSqliteBackend(executor: SqlExecutor | ClosableSqlExecutor): StorageBackend {
  return {
    kind: "sqlite",
    sql: executor,
    ownership: () => "owner",
    async close() {
      if ("close" in executor) await executor.close();
    },
  };
}
