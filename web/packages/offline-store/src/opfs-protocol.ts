// The message protocol between the main thread and the worker that owns the
// wa-sqlite database. One request, one reply, matched by id. The handler is
// engine-agnostic: the real worker opens wa-sqlite behind it, the tests open
// node:sqlite behind the same handler.
import type { ClosableSqlExecutor, SqlRow, SqlStatement, SqlValue } from "./sql.js";

export type WorkerRequest =
  | { id: number; op: "init"; dbName: string }
  | { id: number; op: "run"; sql: string; params?: SqlValue[] }
  | { id: number; op: "all"; sql: string; params?: SqlValue[] }
  | { id: number; op: "atomic"; statements: SqlStatement[] }
  | { id: number; op: "close" };

export type WorkerReply = { id: number; ok: true; rows?: SqlRow[] } | { id: number; ok: false; error: string };

/** The slice of a Worker the backend uses (a DOM Worker satisfies it). */
export interface WorkerLike {
  postMessage(message: WorkerRequest): void;
  onmessage: ((event: { data: WorkerReply }) => void) | null;
  onerror: ((event: { message?: string }) => void) | null;
  terminate(): void;
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/**
 * Build the request handler a worker runs. `open` is called once, by the
 * `init` request, and may throw (the browser refuses a second exclusive
 * handle); the failure travels back as that request's reply.
 */
export function createRequestHandler(
  open: (dbName: string) => Promise<ClosableSqlExecutor>,
): (request: WorkerRequest) => Promise<WorkerReply> {
  let executor: ClosableSqlExecutor | undefined;
  return async (request) => {
    try {
      if (request.op === "init") {
        executor = await open(request.dbName);
        return { id: request.id, ok: true };
      }
      if (!executor) throw new Error("offline-store worker: not initialised");
      switch (request.op) {
        case "run":
          await executor.run(request.sql, request.params);
          return { id: request.id, ok: true };
        case "all":
          return { id: request.id, ok: true, rows: await executor.all(request.sql, request.params) };
        case "atomic":
          await executor.atomic(request.statements);
          return { id: request.id, ok: true };
        case "close":
          await executor.close();
          executor = undefined;
          return { id: request.id, ok: true };
      }
    } catch (error) {
      return { id: request.id, ok: false, error: message(error) };
    }
  };
}
