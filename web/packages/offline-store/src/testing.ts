// The test entry point, following the auth and api-client precedent: doubles
// that never ride the public entry into a browser bundle. node:sqlite is the
// engine behind the executor double (Node 22.13 or later).
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { DatabaseSync } from "node:sqlite";
import type { LockManagerLike } from "./backend-opfs.js";
import { createRequestHandler, type WorkerLike, type WorkerReply, type WorkerRequest } from "./opfs-protocol.js";
import { err, ok, type Result, type SyncPullResponse, type SyncPushOperation, type SyncPushResponse, type SyncPushResult } from "@hardscapehouse/api-client";
import { SCOPE_POLICIES } from "./scopes.js";
import type { ClosableSqlExecutor, SqlRow, SqlStatement, SqlValue } from "./sql.js";
import type { SyncTransport } from "./transport.js";

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

type Fault =
  | { kind: "status"; status: number }
  | { kind: "afterApply"; applied: number }
  | { kind: "dropResponse" }
  | { kind: "malformed"; shape: "short" | "reordered" | "foreign" };

interface ServerRow {
  seq: number;
  data: Record<string, unknown>;
  deleted: boolean;
}

export type RecordedRequest =
  | { kind: "push"; scope: string; ops: SyncPushOperation[] }
  | { kind: "pull"; scope: string; since: number; limit: number };

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/**
 * A fake of the Go sync engine (core/internal/sync/engine.go) with the same
 * order of decisions: shape, then dedup on the key, then the operation with
 * the scope's conflict policy. Faults are queued per request.
 */
export class FakeSyncServer {
  readonly requests: RecordedRequest[] = [];
  /** Keys in the order the server first applied them. */
  readonly appliedOrder: string[] = [];
  readonly #applyCount = new Map<string, number>();
  readonly #applied = new Map<string, { scope: string; id: string; seq: number }>();
  readonly #rows = new Map<string, Map<string, ServerRow>>();
  readonly #refused = new Map<string, string>();
  readonly #faults: Fault[] = [];
  /** Keys the server answered with a conflict that applied nothing (server wins, manual). */
  readonly conflictedKeys = new Set<string>();
  #offline = false;
  #authValid = true;
  #seq = 0;

  /** How many times the server really applied this key (a dedup answer is not an application). */
  applyCount(key: string): number {
    return this.#applyCount.get(key) ?? 0;
  }

  setOffline(offline: boolean): void {
    this.#offline = offline;
  }

  /** While invalid, every request is answered 401 (after it is recorded). */
  setAuthValid(valid: boolean): void {
    this.#authValid = valid;
  }

  clearFaults(): void {
    this.#faults.length = 0;
  }

  /** Make the module refuse this key with this message (an operation level rejection). */
  refuse(key: string, message: string): void {
    this.#refused.set(key, message);
  }

  failNext(kind: "status", status: number): void;
  failNext(kind: "afterApply", applied: number): void;
  failNext(kind: "dropResponse"): void;
  failNext(kind: "malformed", shape: "short" | "reordered" | "foreign"): void;
  failNext(kind: Fault["kind"], arg?: number | string): void {
    if (kind === "status") this.#faults.push({ kind, status: arg as number });
    else if (kind === "afterApply") this.#faults.push({ kind, applied: arg as number });
    else if (kind === "dropResponse") this.#faults.push({ kind });
    else this.#faults.push({ kind, shape: arg as "short" | "reordered" | "foreign" });
  }

  #table(scope: string): Map<string, ServerRow> {
    let t = this.#rows.get(scope);
    if (!t) this.#rows.set(scope, (t = new Map()));
    return t;
  }

  /** A row that exists on the server before the client's operations (another device's work). */
  seed(scope: string, id: string, data: Record<string, unknown>): number {
    this.#table(scope).set(id, { seq: ++this.#seq, data: { ...data }, deleted: false });
    return this.#seq;
  }

  /** Another device changes a row: fields merge, the row gets a fresh seq. */
  touch(scope: string, id: string, data: Record<string, unknown>): number {
    const row = this.#table(scope).get(id);
    if (!row) throw new Error("touch: no such row");
    row.data = { ...row.data, ...data };
    row.seq = ++this.#seq;
    return row.seq;
  }

  /** Another device deletes a row: a tombstone with a fresh seq. */
  remove(scope: string, id: string): number {
    const row = this.#table(scope).get(id);
    if (!row) throw new Error("remove: no such row");
    row.deleted = true;
    row.seq = ++this.#seq;
    return row.seq;
  }

  row(scope: string, id: string): ServerRow | undefined {
    return this.#table(scope).get(id);
  }

  transport(): SyncTransport {
    return {
      push: async (scope, ops) => this.#push(scope, ops),
      pull: async (scope, since, limit) => this.#pull(scope, since, limit),
    };
  }

  #pull(scope: string, since: number, limit: number): Result<SyncPullResponse> {
    if (this.#offline) return err("network unreachable");
    this.requests.push({ kind: "pull", scope, since, limit });
    if (!this.#authValid) return err("unauthorized", 401);
    const fault = this.#faults[0];
    if (fault?.kind === "status") {
      this.#faults.shift();
      return err(`status ${fault.status}`, fault.status);
    }
    const all = [...this.#table(scope).entries()]
      .filter(([, r]) => r.seq > since)
      .sort((a, b) => a[1].seq - b[1].seq)
      .slice(0, limit);
    const items = all.filter(([, r]) => !r.deleted).map(([id, r]) => ({ id, seq: r.seq, data: { ...r.data } }));
    const tombstones = all.filter(([, r]) => r.deleted).map(([id, r]) => ({ id, seq: r.seq }));
    const lastSeq = all.length > 0 ? all[all.length - 1]![1].seq : since;
    return ok({
      scope: scope as SyncPullResponse["scope"],
      items,
      tombstones,
      last_seq: lastSeq,
      next_cursor: all.length === limit ? String(lastSeq) : null,
    });
  }

  #push(scope: string, ops: SyncPushOperation[]): Result<SyncPushResponse> {
    if (this.#offline) return err("network unreachable");
    this.requests.push({ kind: "push", scope, ops });
    if (!this.#authValid) return err("unauthorized", 401);
    const fault = this.#faults[0];
    if (fault?.kind === "status") {
      this.#faults.shift();
      return err(`status ${fault.status}`, fault.status);
    }
    const stopAfter = fault?.kind === "afterApply" ? fault.applied : ops.length;
    const results: SyncPushResult[] = [];
    for (const op of ops.slice(0, stopAfter)) results.push(this.#pushOne(scope, op));
    if (fault) this.#faults.shift();
    if (fault?.kind === "afterApply") return err("operational fault", 500);
    if (fault?.kind === "dropResponse") return err("connection lost");
    if (fault?.kind === "malformed") {
      if (fault.shape === "short") return ok({ results: results.slice(0, -1) });
      if (fault.shape === "reordered") return ok({ results: [...results].reverse() });
      return ok({ results: results.map((r) => ({ ...r, client_id: "00000000-0000-4000-8000-00000000dead" })) });
    }
    return ok({ results });
  }

  #applyKey(key: string, scope: string, id: string, seq: number): void {
    this.#applyCount.set(key, this.applyCount(key) + 1);
    this.appliedOrder.push(key);
    this.#applied.set(key, { scope, id, seq });
  }

  #pushOne(scope: string, op: SyncPushOperation): SyncPushResult {
    const rejected = (message: string): SyncPushResult => ({ client_id: op.client_id, status: "rejected", error: message });
    if (!UUID.test(op.client_id)) return rejected("client_id must be a uuid");
    if (!["create", "update", "delete"].includes(op.op)) return rejected("op must be create, update or delete");
    const id = typeof op.payload?.["id"] === "string" ? (op.payload["id"] as string) : "";
    if (op.op !== "create" && !UUID.test(id)) return rejected("payload id must be a uuid");

    const done = this.#applied.get(op.client_id);
    if (done) return { client_id: op.client_id, status: "applied", seq: done.seq };
    const refusal = this.#refused.get(op.client_id);
    if (refusal) return rejected(refusal);

    const table = this.#table(scope);
    if (op.op === "create") {
      const newId = id || `00000000-0000-4000-8000-${String(++this.#seq + 900000).padStart(12, "0")}`;
      if (table.has(newId)) return rejected("row exists");
      const seq = ++this.#seq;
      table.set(newId, { seq, data: { ...op.payload, id: newId }, deleted: false });
      this.#applyKey(op.client_id, scope, newId, seq);
      return { client_id: op.client_id, status: "applied", seq };
    }
    const row = table.get(id);
    if (!row) return rejected("row not found");
    if (op.op === "delete") {
      if (row.deleted) return { client_id: op.client_id, status: "applied", seq: row.seq };
      row.deleted = true;
      row.seq = ++this.#seq;
      this.#applyKey(op.client_id, scope, id, row.seq);
      return { client_id: op.client_id, status: "applied", seq: row.seq };
    }
    const stale = op.base_seq !== undefined && row.seq > op.base_seq;
    if (stale) {
      const policy = SCOPE_POLICIES[scope] ?? "server_wins";
      if (policy === "last_write_wins_by_field") {
        row.data = { ...row.data, ...op.payload };
        row.seq = ++this.#seq;
        this.#applyKey(op.client_id, scope, id, row.seq);
        return {
          client_id: op.client_id,
          status: "applied",
          seq: row.seq,
          conflict: { client_id: op.client_id, policy, server_state: { ...row.data }, applied: true },
        };
      }
      const p = policy === "manual" ? "manual" : "server_wins";
      this.conflictedKeys.add(op.client_id);
      return { client_id: op.client_id, status: "conflict", conflict: { client_id: op.client_id, policy: p, server_state: { ...row.data }, applied: false } };
    }
    row.data = { ...row.data, ...op.payload };
    row.seq = ++this.#seq;
    this.#applyKey(op.client_id, scope, id, row.seq);
    return { client_id: op.client_id, status: "applied", seq: row.seq };
  }
}
