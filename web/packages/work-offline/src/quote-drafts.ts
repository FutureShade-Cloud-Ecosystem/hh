// The quote draft outbox of Quote Desk. A quote started with no signal is kept
// on the device and sent when the signal returns; last write wins by field is
// the quotes scope's policy (the manifest and the core's registry).
//
// What the core admits through sync on a quote is narrow, and this module
// queues exactly that and nothing more (the Field offline review's lesson: a
// payload may only carry what the Go writable sets admit, and the contract
// test reads those sets from core/internal/quote/syncscope.go):
//   create  id, customer_id, branch_id, valid_until   a DRAFT header
//   update  id, valid_until                           the expiry date
//   delete  id                                        a DRAFT only
// Lines, discounts, terms, the status, the number and every money leg are the
// server's, reached online through the desk's own routes. A draft made offline
// is therefore a header: the customer and the branch, kept until it lands.
import { err, ok, type Result } from "@hardscapehouse/api-client";
import { entityTable, type OfflineStore, type SqlExecutor } from "@hardscapehouse/offline-store";
import { stalenessWords } from "./read-cache.js";

/** The fields a pushed quotes create may declare (core/internal/quote/syncscope.go, quoteCreateFields). */
export const QUOTE_CREATE_FIELDS: readonly string[] = ["id", "customer_id", "branch_id", "valid_until"];

/** The fields a pushed quotes update may declare (quoteWriteFields). */
export const QUOTE_UPDATE_FIELDS: readonly string[] = ["id", "valid_until"];

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** A real calendar date in the shape the core reads, YYYY-MM-DD, or the empty word that clears the leg. */
function validityProblem(value: string): string | null {
  if (value === "") return null;
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return "The valid until date must be a date, year then month then day.";
  const parsed = new Date(`${value}T00:00:00Z`);
  if (Number.isNaN(parsed.getTime()) || parsed.toISOString().slice(0, 10) !== value) return "The valid until date is not a real date.";
  return null;
}

export interface DraftRef {
  id: string;
  key: string;
  seq: number;
}

export interface WaitingDraft {
  quoteId: string;
  kind: "new" | "edit" | "withdraw";
  customerId: string;
  customerName: string;
  validUntil: string | null;
  key: string;
  queuedAt: string;
}

export interface QuoteDraftsOptions {
  store: OfflineStore;
  /** The executor the store was opened over: the local projection rides it. */
  sql: SqlExecutor;
  /** The branch the signed in desk works: a draft is opened in it. */
  branchId: string;
  now?: () => string;
  newId?: () => string;
}

type Bag = Record<string, unknown>;

export class QuoteDrafts {
  readonly #store: OfflineStore;
  readonly #sql: SqlExecutor;
  readonly #branchId: string;
  readonly #now: () => string;
  readonly #newId: () => string;
  /** Rows this device wrote since its last pull: a write to one of them names no base, because the only seq it could name is stale. */
  readonly #written = new Set<string>();

  constructor(options: QuoteDraftsOptions) {
    this.#store = options.store;
    this.#sql = options.sql;
    this.#branchId = options.branchId;
    this.#now = options.now ?? (() => new Date().toISOString());
    this.#newId = options.newId ?? (() => globalThis.crypto.randomUUID());
  }

  /** A pull landed: the rows carry the server's seq again, so a base may be named. */
  markPulled(): void {
    this.#written.clear();
  }

  /** Keep a new draft: the customer and the branch (and the expiry date when one is given), to send when the signal returns. */
  async createDraft(input: { customerId: string; validUntil: string }): Promise<Result<DraftRef>> {
    if (!UUID.test(input.customerId)) return err("Pick a customer from the list first.");
    if (!UUID.test(this.#branchId)) return err("This desk has no branch to open a draft in.");
    const problem = validityProblem(input.validUntil);
    if (problem !== null) return err(problem);
    // One waiting draft per customer: another press while one is still waiting names that draft and queues nothing
    // new, so a run of line adds with no signal does not replay as a run of duplicate creates.
    const held = await this.#waitingCreateFor(input.customerId);
    if (held !== null) return ok(held);
    const id = this.#newId();
    const payload: Bag = {
      id,
      customer_id: input.customerId,
      branch_id: this.#branchId,
      ...(input.validUntil !== "" ? { valid_until: input.validUntil } : {}),
    };
    const queued = await this.#store.enqueue({ scope: "quotes", op: "create", entity: "quotes", payload });
    if (!queued.ok) return err(queued.message);
    // The local row shows the draft before the replay lands. It states no
    // figure: a total that is not known is null, never a zero, and the number
    // is the server's to mint.
    await this.#sql.run(`INSERT OR REPLACE INTO ${entityTable("quotes")} (id, seq, data, deleted) VALUES (?, 0, ?, 0)`, [
      id,
      JSON.stringify({
        id,
        quote_no: "",
        customer_id: input.customerId,
        status: "DRAFT",
        valid_until: input.validUntil === "" ? null : input.validUntil,
        subtotal_cents: null,
        tax_cents: null,
        total_cents: null,
        created_at: this.#now(),
      }),
    ]);
    this.#written.add(id);
    return ok({ id, key: queued.key, seq: queued.seq });
  }

  /** The create still waiting for this customer, unless its draft has been withdrawn on the device. */
  async #waitingCreateFor(customerId: string): Promise<DraftRef | null> {
    const pending = (await this.#store.outbox.pending()).filter((row) => row.scope === "quotes" && row.entityId !== null);
    const held = pending.find(
      (row) => row.op === "create" && row.payload["customer_id"] === customerId && !pending.some((other) => other.op === "delete" && other.entityId === row.entityId),
    );
    return held === undefined ? null : { id: held.entityId!, key: held.key, seq: held.seq };
  }

  /** Change a quote's valid until date, or clear it with the empty word. */
  async setValidUntil(quoteId: string, validUntil: string): Promise<Result<DraftRef>> {
    if (!UUID.test(quoteId)) return err("That quote is not on this device.");
    const problem = validityProblem(validUntil);
    if (problem !== null) return err(problem);
    const found = await this.#store.read("quotes", quoteId);
    if (found.row === null) return err("That quote is not on this device.");
    const baseSeq = await this.#baseFor(quoteId, found.row.seq);
    const queued = await this.#store.enqueue({
      scope: "quotes",
      op: "update",
      entity: "quotes",
      payload: { id: quoteId, valid_until: validUntil },
      ...(baseSeq !== undefined ? { baseSeq } : {}),
    });
    if (!queued.ok) return err(queued.message);
    await this.#patch(quoteId, (data) => ({ ...data, valid_until: validUntil === "" ? null : validUntil }));
    this.#written.add(quoteId);
    return ok({ id: quoteId, key: queued.key, seq: queued.seq });
  }

  /** Withdraw a draft. Only a draft: a quote somebody has seen is a record, and the desk's own path withdraws it. */
  async withdraw(quoteId: string): Promise<Result<DraftRef>> {
    if (!UUID.test(quoteId)) return err("That quote is not on this device.");
    const found = await this.#store.read("quotes", quoteId);
    if (found.row === null) return err("That quote is not on this device.");
    if (found.row.data["status"] !== "DRAFT") return err("Only a draft can be withdrawn here.");
    const baseSeq = await this.#baseFor(quoteId, found.row.seq);
    const queued = await this.#store.enqueue({
      scope: "quotes",
      op: "delete",
      entity: "quotes",
      payload: { id: quoteId },
      ...(baseSeq !== undefined ? { baseSeq } : {}),
    });
    if (!queued.ok) return err(queued.message);
    await this.#sql.run(`UPDATE ${entityTable("quotes")} SET deleted = 1 WHERE id = ?`, [quoteId]);
    this.#written.add(quoteId);
    return ok({ id: quoteId, key: queued.key, seq: queued.seq });
  }

  /** The base rule: the pulled seq when nothing of this device's is queued or written for the row, else none. */
  async #baseFor(quoteId: string, pulledSeq: number): Promise<number | undefined> {
    if (pulledSeq <= 0 || this.#written.has(quoteId)) return undefined;
    const queued = (await this.#store.outbox.pending()).some((row) => row.scope === "quotes" && row.entityId === quoteId);
    return queued ? undefined : pulledSeq;
  }

  async #patch(id: string, change: (data: Bag) => Bag): Promise<void> {
    const raw = await this.#sql.all<{ data: string }>(`SELECT data FROM ${entityTable("quotes")} WHERE id = ?`, [id]);
    const first = raw[0];
    if (first === undefined) return;
    let data: Bag;
    try {
      data = JSON.parse(first.data) as Bag;
    } catch {
      return;
    }
    await this.#sql.run(`UPDATE ${entityTable("quotes")} SET data = ? WHERE id = ?`, [JSON.stringify(change(data)), id]);
  }

  /** The quote work still waiting to be sent, one entry per quote, oldest first. */
  async waiting(): Promise<WaitingDraft[]> {
    const pending = (await this.#store.outbox.pending()).filter((row) => row.scope === "quotes" && row.entityId !== null);
    const names = new Map<string, string>();
    try {
      for (const row of (await this.#store.list("customers")).rows) names.set(row.id, typeof row.data["name"] === "string" ? (row.data["name"] as string) : "");
    } catch {
      // an app without the customers scope names nobody
    }
    const byQuote = new Map<string, typeof pending>();
    for (const row of pending) byQuote.set(row.entityId!, [...(byQuote.get(row.entityId!) ?? []), row]);
    const out: WaitingDraft[] = [];
    for (const [quoteId, rows] of byQuote) {
      const first = rows[0]!;
      // A draft made and withdrawn here is moot: both still go in order (the server holds the truth), but there is nothing to show.
      if (rows.some((r) => r.op === "create") && rows.some((r) => r.op === "delete")) continue;
      const kind = rows.some((r) => r.op === "delete") ? "withdraw" : rows.some((r) => r.op === "create") ? "new" : "edit";
      const create = rows.find((r) => r.op === "create");
      const local = (await this.#store.read("quotes", quoteId)).row;
      const customerId = typeof create?.payload["customer_id"] === "string" ? (create.payload["customer_id"] as string) : typeof local?.data["customer_id"] === "string" ? (local.data["customer_id"] as string) : "";
      let validUntil: string | null = null;
      for (const r of rows) if (typeof r.payload["valid_until"] === "string") validUntil = r.payload["valid_until"] === "" ? null : (r.payload["valid_until"] as string);
      out.push({ quoteId, kind, customerId, customerName: names.get(customerId) ?? "", validUntil, key: first.key, queuedAt: first.createdAt });
    }
    return out.sort((a, b) => a.queuedAt.localeCompare(b.queuedAt) || a.quoteId.localeCompare(b.quoteId));
  }
}

export { stalenessWords };
