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
//  7. A refusal closes a row only when the server cannot hold it. The core's
//     own refusals are 400, 403 and 501 (404 and 413 come from a hop in front
//     and are faults). A 400 on a batch is split to single operations, and one
//     still refused alone is closed as rejected with a card, but only if no
//     earlier request for it could have reached the server (`sent`, set
//     durably before the first request leaves, in the same atomic step as the
//     attempt count). A row that was sent before is UNCERTAIN: it stays
//     pending and the scope shows a `check_applied` stall. A 403 or 501 parks
//     the scope: its rows stay pending, a `scope_refused` stall shows, the
//     other scopes and the pull carry on, and every replay tries again.
//  8. A push stays under MAX_REQUEST_BYTES as well as under maxBatch rows.
//  9. A stuck outbox is visible: `health()` reports the writes waiting, the
//     attempts and last error of a standing fault, and every stall.
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
  /** A request for this row may have reached the server and was not definitively answered. */
  sent: boolean;
  stall: StallKind | null;
}

/** Why a scope's pending work is not moving, apart from a plain fault. */
export type StallKind = "scope_refused" | "check_applied";

/** A stall the UI shows: the scope, the status, the last error, and words for the person. */
export interface OutboxStall {
  kind: StallKind;
  scope: string;
  /** The operation to look at: the one the refusal was about. */
  key: string;
  status: number;
  error: string | null;
  /** Pending operations in the scope, the stalled one included. */
  waiting: number;
  message: string;
}

/** The stuck signal: what the shell shows when writes are waiting and not moving. */
export interface OutboxHealth {
  /** Pending operations, all scopes. */
  waiting: number;
  /** Set while any pending operation has been attempted: the most attempts and that operation's last error. */
  stuck: { attempts: number; lastError: string | null } | null;
  stalls: OutboxStall[];
}

export type ReplayOutcome =
  | { kind: "done"; sent: number; applied: number; conflicts: number; rejected: number; stalledScopes?: string[] }
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
  sent: number;
  stall: StallKind | null;
  stall_status: number | null;
}

/** The words for a stall. */
function stallWords(kind: StallKind, scope: string, status: number): string {
  if (kind === "check_applied") {
    return `The server now refuses an operation in ${scope} (${status}), but an earlier request for it may already have been applied. Check whether it was applied before it is entered again; it is kept and will be tried again.`;
  }
  return `The server refuses writes to ${scope} for this sign in (${status}). The queued work is kept, not sent, and tried again on every cycle until the refusal is lifted.`;
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
    sent: r.sent === 1,
    stall: r.stall,
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

  /** The stuck signal and the stalls, read from the rows, so they survive a restart. */
  async health(): Promise<OutboxHealth> {
    const pending = await this.#sql.all<DbRow>("SELECT * FROM outbox WHERE status = 'pending' ORDER BY seq");
    let stuck: OutboxHealth["stuck"] = null;
    for (const r of pending) {
      if (r.attempts > 0 && (stuck === null || r.attempts > stuck.attempts)) stuck = { attempts: r.attempts, lastError: r.error };
    }
    const waitingIn = new Map<string, number>();
    for (const r of pending) waitingIn.set(r.scope, (waitingIn.get(r.scope) ?? 0) + 1);
    const stalls: OutboxStall[] = pending
      .filter((r) => r.stall !== null)
      .map((r) => {
        const status = r.stall_status ?? 0;
        return {
          kind: r.stall!,
          scope: r.scope,
          key: r.idempotency_key,
          status,
          error: r.error,
          waiting: waitingIn.get(r.scope) ?? 0,
          message: stallWords(r.stall!, r.scope, status),
        };
      });
    return { waiting: pending.length, stuck, stalls };
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
    // Scopes the server refused in this replay: their rows stay pending, the others carry on.
    const parked = new Set<string>();
    for (;;) {
      const skip = [...parked];
      const head = await this.#sql.all<DbRow>(
        `SELECT * FROM outbox WHERE status = 'pending'${skip.length > 0 ? ` AND scope NOT IN (${skip.map(() => "?").join(", ")})` : ""} ORDER BY seq LIMIT ?`,
        [...skip, this.#maxBatch],
      );
      if (head.length === 0) return { kind: "done", ...totals, ...(skip.length > 0 ? { stalledScopes: skip } : {}) };
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
      const outcome = await this.#push(transport, scope, batch, totals, true, parked);
      if (outcome) return outcome;
    }
  }

  /**
   * Send one batch and record the answer. Returns null when the batch was dealt
   * with and the replay should carry on, or the outcome that ends the replay.
   * The rows are marked sent (and the attempt counted) durably before the
   * request leaves; a definitive answer that the server applied nothing (a
   * refusal, a 401) puts the mark back.
   * A 400 on a batch of several is split to single operations; one still
   * refused alone is closed as rejected with its card only if it was never sent
   * before, else it is a `check_applied` stall. A 403 or 501 parks the scope.
   */
  async #push(
    transport: SyncTransport,
    scope: string,
    run: OutboxRow[],
    totals: Totals,
    mayRetrySingly: boolean,
    parked: Set<string>,
  ): Promise<ReplayOutcome | null> {
    const ops: SyncPushOperation[] = run.map(toOperation);
    try {
      await this.#mark(run);
    } catch (error) {
      // No durable mark, no request: it could not be told apart from a send that never was.
      return { kind: "fault", message: error instanceof Error ? error.message : String(error) };
    }
    const response = await transport.push(scope, ops);
    if (!response.ok) {
      const failure = classifyFailure(response.error, response.status);
      if (failure.kind === "refused") {
        await this.#unmark(run);
        if (failure.status === 400) {
          if (mayRetrySingly && run.length > 1) {
            for (const row of run) {
              if (parked.has(scope)) break;
              const outcome = await this.#push(transport, scope, [row], totals, false, parked);
              if (outcome) return outcome;
            }
            return null;
          }
          const row = run[0]!;
          if (!row.sent) {
            return this.#closeRejected([{ row, message: `request refused (${failure.status}): ${failure.message}` }], totals);
          }
          return this.#park(scope, row, "check_applied", failure.status, failure.message, parked);
        }
        return this.#park(scope, run[0]!, "scope_refused", failure.status, failure.message, parked);
      }
      if (failure.kind === "auth_expired") {
        await this.#unmark(run);
        return failure;
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

  /**
   * Durably mark rows as possibly sent and count the attempt, in one atomic
   * step, before their request leaves. Any stall they carried is cleared: the
   * answer to this request says whether it stands.
   */
  async #mark(run: readonly OutboxRow[]): Promise<void> {
    await this.#sql.atomic(
      run.map((row) => ({
        sql: "UPDATE outbox SET sent = 1, attempts = attempts + 1, stall = NULL, stall_status = NULL WHERE idempotency_key = ? AND status = 'pending'",
        params: [row.key],
      })),
    );
  }

  /**
   * Put the mark back to what it was before the request: the server answered
   * definitively that it applied nothing. Best effort and safe to fail: a row
   * left marked is only treated as uncertain.
   */
  async #unmark(run: readonly OutboxRow[]): Promise<void> {
    try {
      await this.#sql.atomic(
        run.map((row) => ({
          sql: "UPDATE outbox SET sent = ?, attempts = MAX(attempts - 1, 0) WHERE idempotency_key = ? AND status = 'pending'",
          params: [row.sent ? 1 : 0, row.key],
        })),
      );
    } catch {
      // The rows are still pending and marked sent: the safe side.
    }
  }

  /** Park a scope for the rest of this replay and record the stall on the row to look at. */
  async #park(scope: string, row: OutboxRow, kind: StallKind, status: number, message: string, parked: Set<string>): Promise<ReplayOutcome | null> {
    parked.add(scope);
    try {
      await this.#sql.atomic([
        {
          sql: "UPDATE outbox SET stall = ?, stall_status = ?, error = ? WHERE idempotency_key = ? AND status = 'pending'",
          params: [kind, status, `request refused (${status}): ${message}`, row.key],
        },
      ]);
      return null;
    } catch (error) {
      return { kind: "fault", message: error instanceof Error ? error.message : String(error) };
    }
  }

  /** Record why a sent attempt failed. The attempt was already counted when the rows were marked. Best effort. */
  async #noteFault(run: readonly OutboxRow[], message: string): Promise<void> {
    try {
      await this.#sql.atomic(
        run.map((row) => ({
          sql: "UPDATE outbox SET error = ? WHERE idempotency_key = ? AND status = 'pending'",
          params: [message, row.key],
        })),
      );
    } catch {
      // The rows are still pending; the message is a courtesy.
    }
  }
}
