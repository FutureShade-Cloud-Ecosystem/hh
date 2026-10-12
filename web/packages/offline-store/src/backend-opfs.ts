// The wa-sqlite on OPFS backend (probe 8). The database lives in a dedicated
// worker on AccessHandlePoolVFS, whose exclusive handles allow one tab at a
// time. A Web Lock names the owner tab; a tab that does not get the lock, or
// whose worker is refused the handle, is read cache only and reports it.
import { StoreReadOnlyError, type Ownership, type StorageBackend } from "./backend.js";
import type { WorkerLike, WorkerReply, WorkerRequest } from "./opfs-protocol.js";
import type { SqlExecutor, SqlRow, SqlStatement, SqlValue } from "./sql.js";

export { StoreReadOnlyError } from "./backend.js";

/** The slice of navigator.locks the backend uses. */
export interface LockManagerLike {
  request<T>(name: string, options: { ifAvailable: true }, callback: (lock: unknown | null) => Promise<T>): Promise<T>;
}

export interface OpfsBackendOptions {
  dbName: string;
  locks: LockManagerLike;
  createWorker: () => WorkerLike;
  /** A read-only executor (IndexedDB, say) that answers reads in a degraded tab. */
  readFallback?: SqlExecutor;
  /** Called with the initial ownership, and again if it changes. */
  onOwnership?: (state: Ownership) => void;
}

// What the browser says when a second Access Handle is refused (probe 8).
const EXCLUSIVE_HANDLE = /another open Access Handle|createSyncAccessHandle|NoModificationAllowedError/i;

/** Take the named lock if it is free. The returned function gives it back. */
async function takeLock(locks: LockManagerLike, name: string): Promise<(() => Promise<void>) | null> {
  return new Promise((resolve) => {
    const finished = locks.request(name, { ifAvailable: true }, (lock) => {
      if (!lock) {
        resolve(null);
        return Promise.resolve();
      }
      return new Promise<void>((release) => resolve(async () => {
        release();
        await finished;
      }));
    });
  });
}

class WorkerClient {
  #next = 1;
  readonly #pending = new Map<number, { resolve: (reply: WorkerReply) => void; reject: (error: Error) => void }>();

  readonly #worker: WorkerLike;

  constructor(worker: WorkerLike) {
    this.#worker = worker;
    worker.onmessage = ({ data }) => {
      const waiting = this.#pending.get(data.id);
      if (!waiting) return;
      this.#pending.delete(data.id);
      waiting.resolve(data);
    };
    worker.onerror = (event) => {
      const error = new Error(`offline-store worker failed: ${event.message ?? "unknown error"}`);
      for (const waiting of this.#pending.values()) waiting.reject(error);
      this.#pending.clear();
    };
  }

  call(build: (id: number) => WorkerRequest): Promise<WorkerReply> {
    const id = this.#next++;
    return new Promise((resolve, reject) => {
      this.#pending.set(id, { resolve, reject });
      this.#worker.postMessage(build(id));
    });
  }

  terminate(): void {
    this.#worker.terminate();
  }
}

async function expectOk(reply: Promise<WorkerReply>): Promise<WorkerReply & { ok: true }> {
  const r = await reply;
  if (!r.ok) throw new Error(r.error);
  return r;
}

export async function createOpfsBackend(options: OpfsBackendOptions): Promise<StorageBackend> {
  const { dbName, locks, createWorker, readFallback, onOwnership } = options;
  const release = await takeLock(locks, `hh-offline-store:${dbName}`);

  const degraded = (): StorageBackend => {
    onOwnership?.("read_cache_only");
    const refuse = (): Promise<never> => Promise.reject(new StoreReadOnlyError());
    return {
      kind: "opfs",
      ownership: () => "read_cache_only",
      sql: {
        run: refuse,
        atomic: refuse,
        all: <T = SqlRow>(sql: string, params?: SqlValue[]) => (readFallback ? readFallback.all<T>(sql, params) : Promise.resolve([] as T[])),
      },
      close: async () => {},
    };
  };

  if (!release) return degraded();

  const worker = createWorker();
  const client = new WorkerClient(worker);
  const init = await client.call((id) => ({ id, op: "init", dbName }));
  if (!init.ok) {
    client.terminate();
    await release();
    if (EXCLUSIVE_HANDLE.test(init.error)) return degraded();
    throw new Error(init.error);
  }

  onOwnership?.("owner");
  const sql: SqlExecutor = {
    run: async (statement: string, params?: SqlValue[]) => {
      await expectOk(client.call((id) => ({ id, op: "run", sql: statement, params })));
    },
    all: async <T = SqlRow>(statement: string, params?: SqlValue[]) => {
      const reply = await expectOk(client.call((id) => ({ id, op: "all", sql: statement, params })));
      return (reply.rows ?? []) as T[];
    },
    atomic: async (statements: SqlStatement[]) => {
      await expectOk(client.call((id) => ({ id, op: "atomic", statements })));
    },
  };
  return {
    kind: "opfs",
    sql,
    ownership: () => "owner",
    async close() {
      try {
        await client.call((id) => ({ id, op: "close" }));
      } finally {
        client.terminate();
        await release();
      }
    },
  };
}
