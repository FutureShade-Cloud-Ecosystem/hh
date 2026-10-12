// The durable outbox. Invariants, for the readers:
//  1. Order: rows replay in `seq` order, the order of enqueue, across scopes.
//  2. Key: an operation's idempotency key is generated once, at enqueue, and
//     stored; every attempt, in every process, sends that key and that body.
//  3. Money: a money scope is refused at enqueue and never stored; replay checks
//     the fence again on every stored row, so a row an older build wrote is
//     closed with a card and never sent.
//  4. Atomic outcome: the status change of a pushed operation and whatever the
//     recorder adds for it (a conflict card, an entity row) land in one
//     atomic statement list, or none of it does.
//  5. Nothing is dropped: no row is ever deleted; a row ends applied,
//     conflict or rejected, and stays readable.
//  6. A fault is whole: an operational fault, a malformed answer or an
//     unreadable network marks nothing, and the next replay sends the same
//     rows again, where the server dedups on the keys.
//  7. A refusal is a verdict: a standing refusal of a whole request (400, 403,
//     404, 413, 501) is split to single operations, and one still refused
//     alone is closed as rejected with a card. It is never retried forever.
//  8. A push stays under MAX_REQUEST_BYTES as well as under maxBatch rows.
import type { SyncPushOperation, SyncPushResult } from "@hardscapehouse/api-client";
import { bodyHash, canonicalJson } from "./canonical.js";
import { scopeFence, type ScopeBook, type WriteRefusalReason } from "./scopes.js";
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
  /** Most operations in one push request; the bytes are capped separately (MAX_REQUEST_BYTES). */
  maxBatch?: number;
  onResult?: ResultRecorder;
}

/** One push request body stays under the core's 1 MiB cap (handler.go) with headroom. */
export const MAX_REQUEST_BYTES = 1024 * 1024 - 64 * 1024;
/** `{"ops":[` and `]}` around the operations, and one comma between neighbours. */
const ENVELOPE_BYTES = 10;
const encoder = new TextEncoder();

const opBytes = (op: SyncPushOperation): number => encoder.encode(JSON.stringify(op)).length;

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

/** The stored payload as an object, or null when the text is not a JSON object (disk damage, an old build). */
function parsePayload(text: string): Record<string, unknown> | null {
  try {
    const value: unknown = JSON.parse(text);
    return value !== null && typeof value === "object" && !Array.isArray(value) ? (value as Record<string, unknown>) : null;
  } catch {
    return null;
  }
}

/** A row for reading. A payload that cannot be read shows as empty here; replay closes that row with a card. */
function toRow(r: DbRow): OutboxRow {
  return {
    seq: r.seq,
    key: r.idempotency_key,
    scope: r.scope,
    op: r.op,
    entity: r.entity,
    entityId: r.entity_id,
    payload: parsePayload(r.payload) ?? {},
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

interface Totals {
  sent: number;
  applied: number;
  conflicts: number;
  rejected: number;
}

function toOperation(row: OutboxRow): SyncPushOperation {
  return {
    client_id: row.key,
    op: row.op,
    ...(row.entity !== null ? { entity: row.entity } : {}),
    payload: row.payload,
    ...(row.baseSeq !== null ? { base_seq: row.baseSeq } : {}),
  };
}

/** The longest prefix of a run whose push body stays under MAX_REQUEST_BYTES (always at least one row). */
function fitBytes(run: readonly OutboxRow[]): OutboxRow[] {
  let bytes = ENVELOPE_BYTES;
  const fit: OutboxRow[] = [];
  for (const row of run) {
    const next = opBytes(toOperation(row)) + (fit.length > 0 ? 1 : 0);
    if (fit.length > 0 && bytes + next > MAX_REQUEST_BYTES) break;
    bytes += next;
    fit.push(row);
  }
  return fit;
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
    const size = opBytes({
      client_id: key,
      op: input.op as OpKind,
      ...(entity !== null ? { entity } : {}),
      payload: JSON.parse(text) as Record<string, unknown>,
      ...(baseSeq !== null ? { base_seq: baseSeq } : {}),
    });
    if (size + ENVELOPE_BYTES > MAX_REQUEST_BYTES) {
      return invalid(`operation too large: ${size} bytes, a push request carries at most ${MAX_REQUEST_BYTES}`);
    }
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
    const totals: Totals = { sent: 0, applied: 0, conflicts: 0, rejected: 0 };
    for (;;) {
      const head = await this.#sql.all<DbRow>(
        "SELECT * FROM outbox WHERE status = 'pending' ORDER BY seq LIMIT ?",
        [this.#maxBatch],
      );
      if (head.length === 0) return { kind: "done", ...totals };
      const scope = head[0]!.scope;
      const dbRun: DbRow[] = [];
      for (const r of head) {
        if (r.scope !== scope) break;
        dbRun.push(r);
      }

      // A row that cannot be trusted is never sent: the money fence and the
      // scope name shape are checked again, the stored text must parse, and
      // the body must still match its hash. Each such row is closed as
      // rejected WITH a card, and the rows behind it carry on.
      const bad: { row: OutboxRow; message: string }[] = [];
      const run: OutboxRow[] = [];
      for (const r of dbRun) {
        const row = toRow(r);
        const fence = scopeFence(r.scope);
        const payload = parsePayload(r.payload);
        let message: string | null = null;
        if (fence) message = `${fence.message}; not sent`;
        else if (payload === null) message = "stored payload is not readable JSON; not sent";
        else if ((await bodyHash(hashInput({ scope: row.scope, op: row.op, entity: row.entity, payload, baseSeq: row.baseSeq }))) !== row.bodyHash) {
          message = "stored body does not match its body hash; not sent";
        }
        if (message === null) run.push(row);
        else bad.push({ row, message });
      }
      if (bad.length > 0) {
        const fault = await this.#closeRejected(bad, totals);
        if (fault) return fault;
        continue;
      }

      // One request carries at most MAX_REQUEST_BYTES; the rest of the run waits for the next turn of the loop.
      const batch = fitBytes(run);
      const outcome = await this.#push(transport, scope, batch, totals, true);
      if (outcome) return outcome;
    }
  }

  /**
   * Send one batch and record the answer. Returns null when the batch was dealt
   * with and the replay should carry on, or the outcome that ends the replay.
   * A standing refusal (400, 403, 404, 413, 501) is a verdict on the request:
   * a batch of several is split to single operations, and one still refused
   * alone is closed as rejected with its card.
   */
  async #push(transport: SyncTransport, scope: string, run: OutboxRow[], totals: Totals, mayRetrySingly: boolean): Promise<ReplayOutcome | null> {
    const ops: SyncPushOperation[] = run.map(toOperation);
    const response = await transport.push(scope, ops);
    if (!response.ok) {
      const failure = classifyFailure(response.error, response.status);
      if (failure.kind === "refused") {
        if (mayRetrySingly && run.length > 1) {
          for (const row of run) {
            const outcome = await this.#push(transport, scope, [row], totals, false);
            if (outcome) return outcome;
          }
          return null;
        }
        return this.#closeRejected(run.map((row) => ({ row, message: `request refused (${failure.status}): ${failure.message}` })), totals);
      }
      totals.sent += ops.length;
      if (failure.kind === "fault") {
        await this.#noteFault(run, failure.message);
        return { kind: "fault", message: failure.message, status: failure.status };
      }
      return failure;
    }
    totals.sent += ops.length;

    const results = response.value.results;
    const answered =
      Array.isArray(results) && results.length === run.length && results.every((res, i) => res.client_id === run[i]!.key);
    if (!answered) {
      const message = "push response did not answer each operation in order";
      await this.#noteFault(run, message);
      return { kind: "fault", message };
    }

    try {
      const statements: SqlStatement[] = [];
      const counted: Totals = { sent: 0, applied: 0, conflicts: 0, rejected: 0 };
      for (let i = 0; i < run.length; i++) {
        const row = run[i]!;
        const result = results[i]!;
        let status: OutboxStatus;
        if (result.status === "applied") status = "applied";
        else if (result.status === "conflict") status = "conflict";
        else if (result.status === "rejected") status = "rejected";
        else throw new Error(`push response carries an unknown status for ${row.key}`);
        statements.push({
          sql: "UPDATE outbox SET status = ?, server_seq = ?, error = ? WHERE idempotency_key = ? AND status = 'pending'",
          params: [status, result.seq ?? null, status === "rejected" ? (result.error ?? "rejected") : null, row.key],
        });
        if (this.#onResult) statements.push(...this.#onResult({ row, result }));
        if (status === "applied") counted.applied++;
        else if (status === "conflict") counted.conflicts++;
        else counted.rejected++;
        if (status === "applied" && result.conflict) counted.conflicts++;
      }
      await this.#sql.atomic(statements);
      totals.applied += counted.applied;
      totals.conflicts += counted.conflicts;
      totals.rejected += counted.rejected;
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      await this.#noteFault(run, message);
      return { kind: "fault", message };
    }
    return null;
  }

  /** Close rows as rejected, each with its card, in one atomic unit. Returns a fault outcome when that failed. */
  async #closeRejected(items: readonly { row: OutboxRow; message: string }[], totals: Totals): Promise<ReplayOutcome | null> {
    try {
      const statements: SqlStatement[] = [];
      for (const { row, message } of items) {
        statements.push({
          sql: "UPDATE outbox SET status = 'rejected', error = ? WHERE idempotency_key = ? AND status = 'pending'",
          params: [message, row.key],
        });
        if (this.#onResult) statements.push(...this.#onResult({ row, result: { client_id: row.key, status: "rejected", error: message } }));
      }
      await this.#sql.atomic(statements);
      totals.rejected += items.length;
      return null;
    } catch (error) {
      const fault = error instanceof Error ? error.message : String(error);
      await this.#noteFault(items.map((i) => i.row), fault);
      return { kind: "fault", message: fault };
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
