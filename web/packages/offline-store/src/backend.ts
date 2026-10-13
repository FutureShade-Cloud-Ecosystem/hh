// The storage interface: a SQL executor plus who may write through it.
import type { SqlExecutor } from "./sql.js";

export type BackendKind = "sqlite" | "opfs";

/**
 * `owner` may read and write. `read_cache_only` is a second browser tab (or a
 * tab that could not open the database): it never writes, and the shell shows
 * it as a state rather than failing quietly.
 */
export type Ownership = "owner" | "read_cache_only";

export interface StorageBackend {
  readonly kind: BackendKind;
  readonly sql: SqlExecutor;
  ownership(): Ownership;
  close(): Promise<void>;
}

/** A write reached a backend that is read cache only. */
export class StoreReadOnlyError extends Error {
  constructor() {
    super("offline-store: this tab is read cache only; another tab owns the store");
    this.name = "StoreReadOnlyError";
  }
}
