// Conflict cards. The server returns one of three outcomes per stale write
// (engine.go, conflict.go) and the shell has to show each of them:
//   server_wins              superseded     "superseded on the server"
//   last_write_wins_by_field merge_report   the merge that was applied
//   manual                   review         waits for a person
// A rejected operation also gets a card, carrying the module's own words.
// A card is written in the same atomic unit as the outbox status change that
// produced it, so a conflict is never closed without its card, and a card is
// only ever dismissed, never deleted.
import type { SyncPushResult } from "@hardscapehouse/api-client";
import type { OutboxRow } from "./outbox.js";
import type { SqlExecutor, SqlStatement } from "./sql.js";

export type CardKind = "superseded" | "merge_report" | "review" | "rejected";

export interface Card {
  id: string;
  clientId: string;
  scope: string;
  kind: CardKind;
  policy: string | null;
  entityId: string | null;
  serverState: Record<string, unknown>;
  clientPayload: Record<string, unknown>;
  error: string | null;
  status: "open" | "dismissed";
  createdAt: string;
}

interface CardDraft {
  kind: CardKind;
  policy: string | null;
  serverState: Record<string, unknown>;
  error: string | null;
}

/** What card, if any, a push result owes the person. */
export function draftFor(result: SyncPushResult): CardDraft | null {
  const envelope = result.conflict;
  if (result.status === "rejected") {
    return { kind: "rejected", policy: null, serverState: {}, error: result.error ?? "rejected" };
  }
  if (result.status === "conflict") {
    const kind: CardKind =
      envelope?.policy === "server_wins" ? "superseded" : envelope?.policy === "last_write_wins_by_field" ? "merge_report" : "review";
    return { kind, policy: envelope?.policy ?? null, serverState: envelope?.server_state ?? {}, error: null };
  }
  if (result.status === "applied" && envelope) {
    // The only applied result with an envelope is a per-field merge.
    return {
      kind: envelope.applied === false ? "review" : "merge_report",
      policy: envelope.policy,
      serverState: envelope.server_state ?? {},
      error: null,
    };
  }
  return null;
}

interface DbCard {
  id: string;
  client_id: string;
  scope: string;
  kind: CardKind;
  policy: string | null;
  entity_id: string | null;
  server_state: string;
  client_payload: string;
  error: string | null;
  status: "open" | "dismissed";
  created_at: string;
}

function toCard(r: DbCard): Card {
  return {
    id: r.id,
    clientId: r.client_id,
    scope: r.scope,
    kind: r.kind,
    policy: r.policy,
    entityId: r.entity_id,
    serverState: JSON.parse(r.server_state) as Record<string, unknown>,
    clientPayload: JSON.parse(r.client_payload) as Record<string, unknown>,
    error: r.error,
    status: r.status,
    createdAt: r.created_at,
  };
}

export class CardStore {
  readonly #sql: SqlExecutor;
  readonly #now: () => string;
  readonly #listeners = new Set<() => void>();

  constructor(sql: SqlExecutor, now: () => string) {
    this.#sql = sql;
    this.#now = now;
  }

  /**
   * The statements that record the card for one result, to run in the same
   * atomic unit as the outbox status change. Insert-or-ignore on the
   * operation's key: a result seen twice makes one card.
   */
  statementsFor(row: OutboxRow, result: SyncPushResult): SqlStatement[] {
    const draft = draftFor(result);
    if (!draft) return [];
    const entityId = row.entityId ?? (typeof draft.serverState["id"] === "string" ? draft.serverState["id"] : null);
    return [
      {
        sql: `INSERT OR IGNORE INTO cards (id, client_id, scope, kind, policy, entity_id, server_state, client_payload, error, status, created_at)
              VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'open', ?)`,
        params: [
          `card:${row.key}`,
          row.key,
          row.scope,
          draft.kind,
          draft.policy,
          entityId,
          JSON.stringify(draft.serverState),
          JSON.stringify(row.payload),
          draft.error,
          this.#now(),
        ],
      },
    ];
  }

  async listOpen(): Promise<Card[]> {
    return (await this.#sql.all<DbCard>("SELECT * FROM cards WHERE status = 'open' ORDER BY created_at, id")).map(toCard);
  }

  /** Every card, open and dismissed. */
  async list(): Promise<Card[]> {
    return (await this.#sql.all<DbCard>("SELECT * FROM cards ORDER BY created_at, id")).map(toCard);
  }

  async dismiss(id: string): Promise<void> {
    const open = await this.#sql.all("SELECT id FROM cards WHERE id = ? AND status = 'open'", [id]);
    if (open.length === 0) return;
    await this.#sql.run("UPDATE cards SET status = 'dismissed' WHERE id = ?", [id]);
    this.notify();
  }

  subscribe(listener: () => void): () => void {
    this.#listeners.add(listener);
    return () => void this.#listeners.delete(listener);
  }

  notify(): void {
    for (const listener of [...this.#listeners]) listener();
  }
}
