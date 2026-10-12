// The test entry point, following the auth and api-client precedent: doubles
// that never ride the public entry into a browser bundle. node:sqlite is the
// engine behind the executor double (Node 22.13 or later).
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { DatabaseSync } from "node:sqlite";
import type { LockManagerLike } from "./backend-opfs.js";
import { createRequestHandler, type WorkerLike, type WorkerReply, type WorkerRequest } from "./opfs-protocol.js";
import type { ClosableSqlExecutor, SqlRow, SqlStatement, SqlValue } from "./sql.js";

/** An in-memory (or file) SQLite behind the executor seam. */
export function nodeSqliteExecutor(path = ":memory:"): ClosableSqlExecutor {
  const db = new DatabaseSync(path);
  const plain = (row: Record<string, unknown>): SqlRow => ({ ...row }) as SqlRow;
  return {
    async run(sql, params = []) {
      db.prepare(sql).run(...params);
    },
    async all<T = SqlRow>(sql: string, params: SqlValue[] = []) {
      return db.prepare(sql).all(...params).map(plain) as T[];
    },
    async atomic(statements: SqlStatement[]) {
      db.exec("BEGIN");
      try {
        for (const s of statements) db.prepare(s.sql).run(...(s.params ?? []));
        db.exec("COMMIT");
      } catch (error) {
        db.exec("ROLLBACK");
        throw error;
      }
    },
    async close() {
      db.close();
    },
  };
}

/** The wa-sqlite wasm bytes, read from disk because node's fetch cannot load a file URL. */
export function waSqliteWasm(): Uint8Array {
  return readFileSync(createRequire(import.meta.url).resolve("wa-sqlite/dist/wa-sqlite.wasm"));
}

/** A Web Locks double: one holder per name, ifAvailable semantics. */
export class FakeLocks implements LockManagerLike {
  readonly #held = new Set<string>();

  request<T>(name: string, _options: { ifAvailable: true }, callback: (lock: unknown | null) => Promise<T>): Promise<T> {
    if (this.#held.has(name)) return callback(null);
    this.#held.add(name);
    return callback({ name }).finally(() => this.#held.delete(name));
  }

  held(): string[] {
    return [...this.#held].sort();
  }
}

export interface FakeWorkerOptions {
  /** Make `init` fail with this message, as the browser does when it refuses an exclusive handle. */
  initError?: string;
}

/** A worker double that serves the real protocol handler over any executor. */
export class FakeWorker implements WorkerLike {
  onmessage: ((event: { data: WorkerReply }) => void) | null = null;
  onerror: ((event: { message?: string }) => void) | null = null;
  terminated = false;
  readonly initCalls: string[] = [];
  readonly #handle: (request: WorkerRequest) => Promise<WorkerReply>;

  constructor(executor: ClosableSqlExecutor, options: FakeWorkerOptions = {}) {
    this.#handle = createRequestHandler(async (dbName) => {
      this.initCalls.push(dbName);
      if (options.initError) throw new Error(options.initError);
      return executor;
    });
  }

  postMessage(request: WorkerRequest): void {
    void this.#handle(request).then((reply) => this.onmessage?.({ data: reply }));
  }

  terminate(): void {
    this.terminated = true;
  }
}
