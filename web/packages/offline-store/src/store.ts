// The offline store: one object over a backend that holds the outbox, the
// pulled projections with their cursors, and the conflict cards.
import type { StorageBackend, Ownership } from "./backend.js";
import { CardStore } from "./cards.js";
import { Outbox, type EnqueueInput, type EnqueueResult, type ReplayOutcome } from "./outbox.js";
import { entityTable, migrate } from "./schema.js";
import type { ScopeBook } from "./scopes.js";
import type { SqlStatement } from "./sql.js";
import { classifyFailure, type SyncTransport } from "./transport.js";

export interface OfflineStoreOptions {
  backend: StorageBackend;
  scopes: ScopeBook;
  now?: () => string;
  newKey?: () => string;
  /** Rows per pull page; the core clamps to 200. */
  pageLimit?: number;
  maxBatch?: number;
}

export interface Staleness {
  scope: string;
  neverPulled: boolean;
  lastSeq: number;
  lastPullAt: string | null;
  /** Milliseconds since the last successful pull, or null when never pulled. */
  ageMs: number | null;
}

export interface StoredRow {
  id: string;
  seq: number;
  data: Record<string, unknown>;
}

export type StoreEnqueueResult = EnqueueResult | { ok: false; reason: "read_only"; message: string };
export type StoreReplayOutcome = ReplayOutcome | { kind: "read_only" };
export type PullOutcome =
  | { kind: "done"; scopes: number; items: number; tombstones: number }
  | { kind: "offline"; message: string }
  | { kind: "auth_expired"; message: string }
  | { kind: "fault"; message: string; status?: number }
  | { kind: "read_only" };

export class OfflineStore {
  readonly outbox: Outbox;
  readonly cards: CardStore;
  readonly #backend: StorageBackend;
  readonly #scopes: ScopeBook;
  readonly #now: () => string;
  readonly #pageLimit: number;
  #cardsTouched = false;

  private constructor(options: OfflineStoreOptions) {
    this.#backend = options.backend;
    this.#scopes = options.scopes;
    this.#now = options.now ?? (() => new Date().toISOString());
    this.#pageLimit = options.pageLimit ?? 200;
    this.cards = new CardStore(options.backend.sql, this.#now);
    this.outbox = new Outbox({
      sql: options.backend.sql,
      scopes: options.scopes,
      now: this.#now,
      ...(options.newKey ? { newKey: options.newKey } : {}),
      ...(options.maxBatch ? { maxBatch: options.maxBatch } : {}),
      onResult: ({ row, result }) => {
        const statements = this.cards.statementsFor(row, result);
        if (statements.length > 0) this.#cardsTouched = true;
        return statements;
      },
    });
  }

  /** Open the store. The owner migrates the schema; a read cache only tab touches nothing. */
  static async open(options: OfflineStoreOptions): Promise<OfflineStore> {
    if (options.backend.ownership() === "owner") await migrate(options.backend.sql, options.scopes.declared());
    return new OfflineStore(options);
  }

  ownership(): Ownership {
    return this.#backend.ownership();
  }

  async close(): Promise<void> {
    await this.#backend.close();
  }

  async enqueue(input: EnqueueInput): Promise<StoreEnqueueResult> {
    if (this.ownership() !== "owner") {
      return { ok: false, reason: "read_only", message: "this tab is read cache only; another tab owns the store" };
    }
    return this.outbox.enqueue(input);
  }

  async replay(transport: SyncTransport): Promise<StoreReplayOutcome> {
    if (this.ownership() !== "owner") return { kind: "read_only" };
    this.#cardsTouched = false;
    try {
      return await this.outbox.replay(transport);
    } finally {
      if (this.#cardsTouched) this.cards.notify();
    }
  }

  // ---- pull -----------------------------------------------------------

  /** Pull one scope, or every declared scope in order. Stops at the first failure; pages already stored stay. */
  async pull(transport: SyncTransport, scope?: string): Promise<PullOutcome> {
    if (this.ownership() !== "owner") return { kind: "read_only" };
    const targets = scope === undefined ? this.#scopes.declared() : [this.#declared(scope)];
    const total = { scopes: 0, items: 0, tombstones: 0 };
    for (const target of targets) {
      const out = await this.#pullScope(transport, target);
      if (out.kind !== "done") return out;
      total.scopes++;
      total.items += out.items;
      total.tombstones += out.tombstones;
    }
    return { kind: "done", ...total };
  }

  async #pullScope(
    transport: SyncTransport,
    scope: string,
  ): Promise<{ kind: "done"; items: number; tombstones: number } | Exclude<PullOutcome, { kind: "done" } | { kind: "read_only" }>> {
    const table = entityTable(scope);
    const sql = this.#backend.sql;
    let since = (await this.#state(scope)).lastSeq;
    let items = 0;
    let tombstones = 0;
    for (;;) {
      const res = await transport.pull(scope, since, this.#pageLimit);
      if (!res.ok) {
        const failure = classifyFailure(res.error, res.status);
        return failure.kind === "fault" || failure.kind === "refused" ? { kind: "fault", message: failure.message, status: failure.status } : failure;
      }
      const page = res.value;
      if (page.scope !== scope) return { kind: "fault", message: `pull answered scope ${page.scope} for ${scope}` };
      const more = page.next_cursor !== null && page.next_cursor !== undefined;
      if (more && page.last_seq <= since) return { kind: "fault", message: "pull made no progress" };

      const statements: SqlStatement[] = [];
      for (const item of page.items) {
        statements.push({
          sql: `INSERT INTO ${table} (id, seq, data, deleted) VALUES (?, ?, ?, 0)
                ON CONFLICT(id) DO UPDATE SET seq = excluded.seq, data = excluded.data, deleted = 0 WHERE excluded.seq >= ${table}.seq`,
          params: [item.id, item.seq, JSON.stringify(item.data)],
        });
      }
      for (const t of page.tombstones) {
        statements.push({
          sql: `INSERT INTO ${table} (id, seq, data, deleted) VALUES (?, ?, '{}', 1)
                ON CONFLICT(id) DO UPDATE SET seq = excluded.seq, data = '{}', deleted = 1 WHERE excluded.seq >= ${table}.seq`,
          params: [t.id, t.seq],
        });
      }
      statements.push({
        sql: `INSERT INTO sync_state (scope, last_seq, last_pull_at) VALUES (?, ?, ?)
              ON CONFLICT(scope) DO UPDATE SET last_seq = MAX(sync_state.last_seq, excluded.last_seq), last_pull_at = excluded.last_pull_at`,
        params: [scope, page.last_seq, this.#now()],
      });
      try {
        await sql.atomic(statements);
      } catch (error) {
        return { kind: "fault", message: error instanceof Error ? error.message : String(error) };
      }
      items += page.items.length;
      tombstones += page.tombstones.length;
      if (!more) return { kind: "done", items, tombstones };
      since = page.last_seq;
    }
  }

  // ---- reads ----------------------------------------------------------

  #declared(scope: string): string {
    if (!this.#scopes.has(scope)) throw new Error(`offline-store: ${scope} is not a scope of this profile`);
    return scope;
  }

  async staleness(scope: string): Promise<Staleness> {
    return this.#state(this.#declared(scope));
  }

  async #state(scope: string): Promise<Staleness> {
    let found: { last_seq: number; last_pull_at: string | null }[] = [];
    try {
      found = await this.#backend.sql.all("SELECT last_seq, last_pull_at FROM sync_state WHERE scope = ?", [scope]);
    } catch (error) {
      if (this.ownership() === "owner") throw error;
    }
    const row = found[0];
    if (!row || row.last_pull_at === null) {
      return { scope, neverPulled: row === undefined, lastSeq: row?.last_seq ?? 0, lastPullAt: null, ageMs: null };
    }
    const age = Date.parse(this.#now()) - Date.parse(row.last_pull_at);
    return { scope, neverPulled: false, lastSeq: row.last_seq, lastPullAt: row.last_pull_at, ageMs: Number.isNaN(age) ? null : Math.max(0, age) };
  }

  /** Every live row of a scope, with how stale the scope is. */
  async list(scope: string): Promise<{ rows: StoredRow[]; staleness: Staleness; readOnly: boolean }> {
    const table = entityTable(this.#declared(scope));
    const readOnly = this.ownership() !== "owner";
    const staleness = await this.#state(scope);
    let raw: { id: string; seq: number; data: string }[] = [];
    try {
      raw = await this.#backend.sql.all(`SELECT id, seq, data FROM ${table} WHERE deleted = 0 ORDER BY id`);
    } catch (error) {
      if (!readOnly) throw error;
    }
    return { rows: raw.map((r) => ({ id: r.id, seq: r.seq, data: JSON.parse(r.data) as Record<string, unknown> })), staleness, readOnly };
  }

  /** One live row, or null (never pulled, deleted, or unknown), with how stale the scope is. */
  async read(scope: string, id: string): Promise<{ row: StoredRow | null; staleness: Staleness; readOnly: boolean }> {
    const table = entityTable(this.#declared(scope));
    const readOnly = this.ownership() !== "owner";
    const staleness = await this.#state(scope);
    let raw: { id: string; seq: number; data: string }[] = [];
    try {
      raw = await this.#backend.sql.all(`SELECT id, seq, data FROM ${table} WHERE id = ? AND deleted = 0`, [id]);
    } catch (error) {
      if (!readOnly) throw error;
    }
    const r = raw[0];
    return { row: r ? { id: r.id, seq: r.seq, data: JSON.parse(r.data) as Record<string, unknown> } : null, staleness, readOnly };
  }
}
