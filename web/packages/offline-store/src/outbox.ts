// The durable outbox. Invariants, for the readers:
//  1. Order: rows replay in `seq` order, the order of enqueue, across scopes.
//  2. Key: an operation's idempotency key is generated once, at enqueue, and
//     stored; every attempt, in every process, sends that key and that body.
//  3. Money: a money scope is refused at enqueue and never stored.
//  4. Atomic outcome: the status change of a pushed operation and whatever the
//     recorder adds for it (a conflict card, an entity row) land in one
//     atomic statement list, or none of it does.
//  5. Nothing is dropped: no row is ever deleted; a row ends applied,
//     conflict or rejected, and stays readable.
//  6. A fault is whole: an operational fault, a malformed answer or an
//     unreadable network marks nothing, and the next replay sends the same
//     rows again, where the server dedups on the keys.
import type { SyncPushOperation, SyncPushResult } from "@hardscapehouse/api-client";
import { bodyHash, canonicalJson } from "./canonical.js";
import type { ScopeBook, WriteRefusalReason } from "./scopes.js";
import { serialQueue, type SqlExecutor, type SqlStatement } from "./sql.js";
import { classifyFailure, type SyncTransport } from "./transport.js";

export type OutboxStatus = "pending" | "applied" | "conflict" | "rejected";
export type OpKind = "create" | "update" | "delete";

export interface EnqueueInput {
  scope: string;
  op: string;
  entity?: string;
  payload?: Record<string, unknown>;
  baseSeq?: number;
  /** An idempotency key to use instead of a generated one (a re-submitted user action). */
  key?: string;
}

export type EnqueueResult =
  | { ok: true; key: string; seq: number; duplicate: boolean }
  | { ok: false; reason: WriteRefusalReason | "invalid" | "key_reused"; message: string };

export interface OutboxRow {
  seq: number;
  key: string;
  scope: string;
  op: OpKind;
  entity: string | null;
  entityId: string | null;
  payload: Record<string, unknown>;
  baseSeq: number | null;
  bodyHash: string;
  status: OutboxStatus;
  attempts: number;
  error: string | null;
  serverSeq: number | null;
  createdAt: string;
}

export type ReplayOutcome =
  | { kind: "done"; sent: number; applied: number; conflicts: number; rejected: number }
  | { kind: "offline"; message: string }
  | { kind: "auth_expired"; message: string }
  | { kind: "fault"; message: string; status?: number };

/** Extra statements to land in the same atomic unit as one result's status change. */
export type ResultRecorder = (context: { row: OutboxRow; result: SyncPushResult }) => SqlStatement[];

export interface OutboxOptions {
  sql: SqlExecutor;
  scopes: ScopeBook;
  now?: () => string;
  newKey?: () => string;
  /** Most operations in one push request. The core's body cap is 1 MiB. */
  maxBatch?: number;
  onResult?: ResultRecorder;
}

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const OPS: readonly string[] = ["create", "update", "delete"];

interface DbRow {
  seq: number;
  idempotency_key: string;
  scope: string;
  op: OpKind;
  entity: string | null;
  entity_id: string | null;
  payload: string;
  base_seq: number | null;
  body_hash: string;
  status: OutboxStatus;
  attempts: number;
  error: string | null;
  server_seq: number | null;
  created_at: string;
}

function toRow(r: DbRow): OutboxRow {
  return {
    seq: r.seq,
    key: r.idempotency_key,
    scope: r.scope,
    op: r.op,
    entity: r.entity,
    entityId: r.entity_id,
    payload: JSON.parse(r.payload) as Record<string, unknown>,
    baseSeq: r.base_seq,
    bodyHash: r.body_hash,
    status: r.status,
    attempts: r.attempts,
    error: r.error,
    serverSeq: r.server_seq,
    createdAt: r.created_at,
  };
}

/** The bytes the hash covers: everything about the operation except its key and its clock. */
function hashInput(r: { scope: string; op: string; entity: string | null; payload: unknown; baseSeq: number | null }) {
  return { scope: r.scope, op: r.op, entity: r.entity, payload: r.payload, baseSeq: r.baseSeq };
}

function invalid(message: string): EnqueueResult {
  return { ok: false, reason: "invalid", message };
}

export class Outbox {
  readonly #sql: SqlExecutor;
  readonly #scopes: ScopeBook;
  readonly #now: () => string;
  readonly #newKey: () => string;
  readonly #maxBatch: number;
  readonly #onResult: ResultRecorder | undefined;
  readonly #enqueueQueue = serialQueue();
  readonly #replayQueue = serialQueue();

  constructor(options: OutboxOptions) {
    this.#sql = options.sql;
    this.#scopes = options.scopes;
    this.#now = options.now ?? (() => new Date().toISOString());
    this.#newKey = options.newKey ?? (() => globalThis.crypto.randomUUID());
    this.#maxBatch = options.maxBatch ?? 50;
    this.#onResult = options.onResult;
  }

  enqueue(input: EnqueueInput): Promise<EnqueueResult> {
    return this.#enqueueQueue(() => this.#enqueue(input));
  }

  async #enqueue(input: EnqueueInput): Promise<EnqueueResult> {
    // The money fence first: nothing about a money write is inspected, stored or hashed.
    const refusal = this.#scopes.writeRefusal(input.scope);
    if (refusal) return { ok: false, reason: refusal.reason, message: refusal.message };

    if (!OPS.includes(input.op)) return invalid("op must be create, update or delete");
    const payload = input.payload;
    if (payload === undefined || payload === null || typeof payload !== "object" || Array.isArray(payload)) {
      return invalid("payload must be an object");
    }
    const entityId = typeof payload["id"] === "string" ? payload["id"] : null;
    if (input.op !== "create" && (entityId === null || !UUID.test(entityId))) return invalid("payload id must be a uuid");
    if (input.baseSeq !== undefined && (!Number.isInteger(input.baseSeq) || input.baseSeq < 0)) {
      return invalid("baseSeq must be a non-negative integer");
    }
    if (input.key !== undefined && !UUID.test(input.key)) return invalid("key must be a uuid");

    const entity = input.entity ?? null;
    const baseSeq = input.baseSeq ?? null;
    let hash: string;
    let text: string;
    try {
      text = canonicalJson(payload);
      hash = await bodyHash(hashInput({ scope: input.scope, op: input.op, entity, payload, baseSeq }));
    } catch (error) {
      return invalid(error instanceof Error ? error.message : String(error));
    }

    const key = input.key ?? this.#newKey();
    const existing = await this.#sql.all<{ seq: number; body_hash: string }>(
      "SELECT seq, body_hash FROM outbox WHERE idempotency_key = ?",
      [key],
    );
    if (existing.length > 0) {
      const found = existing[0]!;
      if (found.body_hash === hash) return { ok: true, key, seq: found.seq, duplicate: true };
      return { ok: false, reason: "key_reused", message: "this idempotency key already names a different operation" };
    }
    await this.#sql.run(
      `INSERT INTO outbox (idempotency_key, scope, op, entity, entity_id, payload, base_seq, body_hash, status, attempts, created_at)
       VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending', 0, ?)`,
      [key, input.scope, input.op, entity, entityId, text, baseSeq, hash, this.#now()],
    );
    const stored = await this.#sql.all<{ seq: number }>("SELECT seq FROM outbox WHERE idempotency_key = ?", [key]);
    return { ok: true, key, seq: stored[0]!.seq, duplicate: false };
  }

  /** Every row with this status (all rows when omitted), in enqueue order. */
  async list(status?: OutboxStatus): Promise<OutboxRow[]> {
    const rows =
      status === undefined
        ? await this.#sql.all<DbRow>("SELECT * FROM outbox ORDER BY seq")
        : await this.#sql.all<DbRow>("SELECT * FROM outbox WHERE status = ? ORDER BY seq", [status]);
    return rows.map(toRow);
  }

  pending(): Promise<OutboxRow[]> {
    return this.list("pending");
  }

  /**
   * Replay the pending rows in order against the transport. One caller at a
   * time: a second call waits, then finds what the first one finished.
   */
  replay(transport: SyncTransport): Promise<ReplayOutcome> {
    return this.#replayQueue(() => this.#replay(transport));
  }

  async #replay(transport: SyncTransport): Promise<ReplayOutcome> {
    const totals = { sent: 0, applied: 0, conflicts: 0, rejected: 0 };
    for (;;) {
      const head = await this.#sql.all<DbRow>(
        "SELECT * FROM outbox WHERE status = 'pending' ORDER BY seq LIMIT ?",
        [this.#maxBatch],
      );
      if (head.length === 0) return { kind: "done", ...totals };
      const scope = head[0]!.scope;
      const run: OutboxRow[] = [];
      for (const r of head) {
        if (r.scope !== scope) break;
        run.push(toRow(r));
      }

      // A body that no longer matches its hash is never sent: it is closed as
      // rejected and the rows behind it carry on.
      const tampered: OutboxRow[] = [];
      for (const row of run) {
        const now = await bodyHash(hashInput({ scope: row.scope, op: row.op, entity: row.entity, payload: row.payload, baseSeq: row.baseSeq }));
        if (now !== row.bodyHash) tampered.push(row);
      }
      if (tampered.length > 0) {
        await this.#sql.atomic(
          tampered.map((row) => ({
            sql: "UPDATE outbox SET status = 'rejected', error = ? WHERE idempotency_key = ? AND status = 'pending'",
            params: ["stored body does not match its body hash; not sent", row.key],
          })),
        );
        totals.rejected += tampered.length;
        continue;
      }

      const ops: SyncPushOperation[] = run.map((row) => ({
        client_id: row.key,
        op: row.op,
        ...(row.entity !== null ? { entity: row.entity } : {}),
        payload: row.payload,
        ...(row.baseSeq !== null ? { base_seq: row.baseSeq } : {}),
      }));
      totals.sent += ops.length;
      const response = await transport.push(scope, ops);
      if (!response.ok) {
        const failure = classifyFailure(response.error, response.status);
        if (failure.kind === "fault") await this.#noteFault(run, failure.message);
        return failure.kind === "fault" ? { kind: "fault", message: failure.message, status: failure.status } : failure;
      }

      const results = response.value.results;
      const answered =
        Array.isArray(results) && results.length === run.length && results.every((res, i) => res.client_id === run[i]!.key);
      if (!answered) {
        const message = "push response did not answer each operation in order";
        await this.#noteFault(run, message);
        return { kind: "fault", message };
      }

      const statements: SqlStatement[] = [];
      for (let i = 0; i < run.length; i++) {
        const row = run[i]!;
        const result = results[i]!;
        let status: OutboxStatus;
        if (result.status === "applied") status = "applied";
        else if (result.status === "conflict") status = "conflict";
        else if (result.status === "rejected") status = "rejected";
        else {
          const message = `push response carries an unknown status for ${row.key}`;
          await this.#noteFault(run, message);
          return { kind: "fault", message };
        }
        statements.push({
          sql: "UPDATE outbox SET status = ?, server_seq = ?, error = ? WHERE idempotency_key = ? AND status = 'pending'",
          params: [status, result.seq ?? null, status === "rejected" ? (result.error ?? "rejected") : null, row.key],
        });
        if (this.#onResult) statements.push(...this.#onResult({ row, result }));
        if (status === "applied") totals.applied++;
        else if (status === "conflict") totals.conflicts++;
        else totals.rejected++;
        if (status === "applied" && result.conflict) totals.conflicts++;
      }
      try {
        await this.#sql.atomic(statements);
      } catch (error) {
        const message = error instanceof Error ? error.message : String(error);
        await this.#noteFault(run, message);
        return { kind: "fault", message };
      }
    }
  }

  /** Count a failed attempt on the rows that were sent. Best effort: the rows stay pending either way. */
  async #noteFault(run: readonly OutboxRow[], message: string): Promise<void> {
    try {
      await this.#sql.atomic(
        run.map((row) => ({
          sql: "UPDATE outbox SET attempts = attempts + 1, error = ? WHERE idempotency_key = ? AND status = 'pending'",
          params: [message, row.key],
        })),
      );
    } catch {
      // The rows are still pending; the count is a courtesy.
    }
  }
}
