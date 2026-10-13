// Yard Pick's thin offline data layer: the api port over the offline store.
// Reads compose from the store (the pulled pick_tasks and shortages rows, with
// the scope's staleness); every write enqueues on the write's own scope with a
// stored idempotency key and patches the local projection, so the glass shows
// the work before the replay lands; a cycle pushes the outbox and pulls the
// scopes back through the sync runner and the injected transport. No call in
// this file opens a second door to the network: the transport is the one the
// shell hands over (the api-client adapter in the app, the fake server's in
// tests).
//
// The projection the reads compose from is the demo sync surface's: the
// pick_tasks rows carry the task and its ticket words, the shortages rows the
// shortage. The store's own guarantees do the heavy lifting: replay in
// enqueue order, one key per operation, money refused at the door, and every
// conflict coming back as a card (offline-store README invariants).
//
// The base_seq rule, stated once: a write to a row with no write already
// queued carries the row's pulled seq as its base, so a move the server made
// after the pull is caught as a conflict; a write to a row this device has
// already queued an operation for goes blind (no base), because the only
// stale base it could name is the device's own first write, and the replay
// applies the chain in order anyway.
import type { Result } from "@hardscapehouse/api-client";
import {
  OfflineStore,
  SyncRunner,
  createOpfsBackend,
  entityTable,
  scopeBookFor,
  type Card,
  type EnqueueInput,
  type OutboxStall,
  type Ownership,
  type SqlExecutor,
  type Staleness,
  type StorageBackend,
  type SyncTransport,
  type WorkerLike,
} from "@hardscapehouse/offline-store";
import * as syncMachine from "../../../../statecharts/sync/machine.js";
import type {
  YardApi,
  YardPick,
  YardPickedLine,
  YardRecordLine,
  YardShortage,
  YardSkidAnswer,
  YardTask,
} from "./yard-api.js";
import { toPickedLine, toShortage, toTask, toTicket } from "./yard-api.js";

type Bag = Record<string, unknown>;

/** The banner mode the chrome's offline strip takes, from the runner's state
 * and the backend's ownership. At rest on an owned store the strip says
 * nothing at all: a chrome that says nothing is the point of the states. */
export function bannerMode(state: string, ownership: Ownership): string {
  if (ownership !== "owner") return "read-only";
  if (state === "offline") return "offline";
  if (state === "unreachable") return "unreachable";
  if (state === "auth_expired") return "auth-expired";
  return "";
}

/** The writes the core does not admit through sync: they stay on their direct
 * doors, wait on the outbox and need signal. Nothing about them queues. */
export type YardDirectDoors = Pick<YardApi, "recordLines" | "recordSkids" | "resolveShortage">;

export interface YardOfflineOptions {
  store: OfflineStore;
  direct: YardDirectDoors;
  /** The same executor seam the store was opened over: the layer's local
   * optimistic patches ride it, the replay overwrites them with server truth. */
  sql: SqlExecutor;
  transport: SyncTransport;
  /** The signed in person's id: the optimistic claim is marked theirs. */
  actorId: string;
  /** Told whenever the outbox's depth or the cards may have moved: a write
   * just landed, or a cycle came to rest. The chrome reads health() and the
   * open cards on it, so the banner's count is never a stale word. */
  onChange?: () => void;
  now?: () => string;
  newKey?: () => string;
}

export type QueueAnswer = Result<{ picks: YardPick[]; nextCursor: string }> & { staleness: Staleness };
export type PickAnswer = Result<YardPick> & { staleness: Staleness };

const PRIORITY_RANK: Record<string, number> = { URGENT: 0, HIGH: 1, NORMAL: 2, LOW: 3 };

/** The short stage handle off a task id: the alphabet without I O 0 1, six
 * marks, the same shape the core's own derivation answers. */
function stableRef(id: string): string {
  let hash = 2166136261;
  for (let i = 0; i < id.length; i += 1) {
    hash ^= id.charCodeAt(i);
    hash = Math.imul(hash, 16777619);
  }
  const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
  let out = "";
  let n = hash >>> 0;
  for (let i = 0; i < 6; i += 1) {
    out += alphabet[n % alphabet.length];
    n = Math.floor(n / alphabet.length);
  }
  return `STG-${out}`;
}

interface StoredRowLike {
  id: string;
  seq: number;
  data: Bag;
}

export class YardOffline {
  readonly store: OfflineStore;
  readonly runner: SyncRunner;
  readonly #sql: SqlExecutor;
  readonly #direct: YardDirectDoors;
  readonly #actorId: string;
  readonly #onChange: (() => void) | undefined;
  readonly #now: () => string;
  readonly #newKey: () => string;
  /** Rows this device has written since the last completed cycle: a write to
   * one of them goes blind even when the earlier operation has already
   * drained, because the pulled seq that a base would name is stale until the
   * cycle's pull lands. */
  readonly #writtenSincePull = new Set<string>();

  constructor(options: YardOfflineOptions) {
    this.store = options.store;
    this.#sql = options.sql;
    this.#direct = options.direct;
    this.#actorId = options.actorId;
    this.#onChange = options.onChange;
    this.#now = options.now ?? (() => new Date().toISOString());
    this.#newKey = options.newKey ?? (() => globalThis.crypto.randomUUID());
    this.runner = new SyncRunner({ store: options.store, transport: options.transport, machine: syncMachine });
  }

  /** One full cycle, to rest: push the outbox, then pull the scopes. Works
   * from any resting state: an ONLINE the machine is past is a no-op, and the
   * request that follows runs the cycle the state can take. */
  async cycle(): Promise<void> {
    await this.runner.dispatch({ type: "ONLINE" });
    await this.runner.request();
    await this.runner.settled();
    // The pull has landed: the rows carry the server's seq again.
    this.#writtenSincePull.clear();
    this.#onChange?.();
  }

  /** Ask for a cycle without waiting for it (a write just landed in the outbox). */
  requestSync(): void {
    // The count moved now, and it moves again when the replay lands or fails.
    this.#onChange?.();
    void this.runner.request().then(() => {
      // The cycle's pull has landed: the rows carry the server's seq again.
      this.#writtenSincePull.clear();
      this.#onChange?.();
    });
  }

  /** The last connectivity word the machine was fed, so a watcher that polls
   * the platform dispatches only on a change. */
  #lastOnline: boolean | null = null;

  /** Feed the platform's connectivity to the machine. */
  setOnline(online: boolean): Promise<void> {
    if (this.#lastOnline === online) return Promise.resolve();
    this.#lastOnline = online;
    return this.runner.dispatch(online ? { type: "ONLINE" } : { type: "OFFLINE" });
  }

  async waiting(): Promise<number> {
    return (await this.store.outbox.health()).waiting;
  }

  async stalls(): Promise<OutboxStall[]> {
    return (await this.store.outbox.health()).stalls;
  }

  openCards(): Promise<Card[]> {
    return this.store.cards.listOpen();
  }

  dismissCard(id: string): Promise<void> {
    return this.store.cards.dismiss(id);
  }

  /** The raw door, for the fence proof: the store refuses a money scope
   * before anything is stored. */
  enqueueRaw(input: EnqueueInput) {
    return this.store.enqueue(input);
  }

  // ---- reads ----------------------------------------------------------

  async staleness(scope: string): Promise<Staleness> {
    return this.store.staleness(scope);
  }

  async queue(branchId: string): Promise<QueueAnswer> {
    const { rows, staleness } = await this.store.list("pick_tasks");
    const inBranch = rows.filter((row) => row.data["branch_id"] === branchId);
    const picks = await Promise.all(inBranch.map((row) => this.#composePick(row)));
    picks.sort((a, b) => {
      const openOf = (p: YardPick) => (p.task.status === "QUEUED" || p.task.status === "PUSHED" ? 0 : 1);
      return (
        openOf(a) - openOf(b) ||
        (PRIORITY_RANK[a.ticket?.priority || a.task.priority] ?? 2) - (PRIORITY_RANK[b.ticket?.priority || b.task.priority] ?? 2) ||
        a.task.created_at.localeCompare(b.task.created_at) ||
        a.task.id.localeCompare(b.task.id)
      );
    });
    return { ok: true, value: { picks, nextCursor: "" }, staleness };
  }

  async pick(id: string): Promise<PickAnswer> {
    const found = await this.store.read("pick_tasks", id);
    if (found.row === null) {
      return { ok: false, error: "task not found", status: 404, staleness: found.staleness };
    }
    return { ok: true, value: await this.#composePick(found.row), staleness: found.staleness };
  }

  async #composePick(row: StoredRowLike): Promise<YardPick> {
    const data = row.data;
    const shortages = await this.#shortagesFor(row.id);
    return {
      task: toTask(data),
      ticket: toTicket(data["ticket"]),
      shortages,
      picked: Array.isArray(data["picked"]) ? (data["picked"] as unknown[]).map(toPickedLine) : [],
    };
  }

  async #shortagesFor(taskId: string): Promise<YardShortage[]> {
    const { rows } = await this.store.list("shortages");
    return rows
      .filter((row) => row.data["task_id"] === taskId)
      .map((row) => toShortage(row.data))
      .sort((a, b) => a.reported_at.localeCompare(b.reported_at) || a.id.localeCompare(b.id));
  }

  // ---- writes ---------------------------------------------------------

  /** Claim an unclaimed pick: the claim queues, the local row shows it. The
   * payload declares the status alone: who claimed is the caller's own fact,
   * stamped by the server, never a field the device declares. */
  async claim(taskId: string): Promise<Result<YardTask>> {
    const answer = await this.#taskWrite(taskId, { status: "ACCEPTED", claimed_by: this.#actorId }, { status: "ACCEPTED" });
    if (answer.ok) this.requestSync();
    return answer;
  }

  /** Start picking a claimed pick. */
  async start(taskId: string): Promise<Result<YardTask>> {
    const answer = await this.#taskWrite(taskId, { status: "IN_PROGRESS" });
    if (answer.ok) this.requestSync();
    return answer;
  }

  /** Record what was pulled, the whole set in one queued operation. */
  async recordLines(taskId: string, lines: readonly YardRecordLine[]): Promise<Result<YardPickedLine[]>> {
    const found = await this.store.read("pick_tasks", taskId);
    if (found.row === null) return { ok: false, error: "task not found", status: 404 };
    const at = this.#now();
    const picked: YardPickedLine[] = lines.map((l) => ({
      id: this.#newKey(),
      task_id: taskId,
      order_line_no: l.order_line_no,
      sku: l.sku,
      description: l.description,
      qty_ordered: l.qty_ordered,
      qty_picked: l.qty_picked,
      uom: l.uom,
      short_reason: l.short_reason,
      short_note: l.short_note,
      picked_at: at,
    }));
    const enqueued = await this.#enqueueWithBase({
      scope: "pick_tasks",
      op: "update",
      entity: "pick_tasks",
      payload: { id: taskId, picked: lines.map((l) => ({ ...l })) },
      row: found.row,
    });
    if (!enqueued.ok) return { ok: false, error: enqueued.message, status: 400 };
    // The local read mirrors what the replay will land: the picked set, and
    // the backorder figure each short line leaves on its ticket line.
    const ticket = toTicket(found.row.data["ticket"]);
    const patchedTicket =
      ticket === null
        ? undefined
        : {
            ...ticket,
            lines: ticket.lines.map((line) => {
              const recorded = picked.find((p) => p.order_line_no === line.line_no);
              if (recorded === undefined) return line;
              const ordered = Number(line.qty_ordered);
              const taken = Number(recorded.qty_picked);
              const back = taken < ordered && Number.isFinite(ordered) ? (ordered - taken).toFixed(4) : "";
              return { ...line, qty_backordered: back };
            }),
          };
    await this.#patchRow("pick_tasks", taskId, (data) => ({
      ...data,
      picked,
      ...(patchedTicket !== undefined ? { ticket: patchedTicket } : {}),
    }));
    this.#writtenSincePull.add(`pick_tasks:${taskId}`);
    this.requestSync();
    return { ok: true, value: picked };
  }

  /** Record or change the order's skid count: the weights scope's own write. */
  async recordSkids(taskId: string, skidCount: number, reason: string, note: string): Promise<Result<YardSkidAnswer>> {
    const weightsRow = await this.store.read("weights", taskId);
    const previous =
      weightsRow.row !== null && typeof weightsRow.row.data["skid_count"] === "number"
        ? (weightsRow.row.data["skid_count"] as number)
        : null;
    const taskRow = await this.store.read("pick_tasks", taskId);
    const enqueued = await this.#enqueueWithBase({
      scope: "weights",
      op: "update",
      entity: "weights",
      payload: { id: taskId, skid_count: skidCount, reason, ...(note !== "" ? { note } : {}) },
      // No pulled weights row names no base: the count lands blind, and the
      // server's own rules judge it at replay.
      row: weightsRow.row,
    });
    if (!enqueued.ok) return { ok: false, error: enqueued.message, status: 400 };
    if (weightsRow.row !== null) {
      await this.#patchRow("weights", taskId, (data) => ({ ...data, skid_count: skidCount, reason }));
    } else {
      // No pulled row yet: the device keeps its own count, so a second save
      // before any pull still names the first one as the previous count.
      await this.#sql.run(`INSERT OR IGNORE INTO ${entityTable("weights")} (id, seq, data, deleted) VALUES (?, 0, ?, 0)`, [
        taskId,
        JSON.stringify({ id: taskId, task_id: taskId, skid_count: skidCount, reason }),
      ]);
    }
    this.#writtenSincePull.add(`weights:${taskId}`);
    this.requestSync();
    const ticket = taskRow.row !== null ? toTicket(taskRow.row.data["ticket"]) : null;
    return { ok: true, value: { skid_count: skidCount, previous_skid_count: previous, changed: previous !== skidCount, order_no: ticket?.order_no ?? "" } };
  }

  /** Park the pick: where the material sits. A blank ref derives its short
   * handle on the device, the same shape the core derives, so the counter can
   * be told before the replay lands. */
  async stage(taskId: string, location: string, ref: string): Promise<Result<YardTask>> {
    const fields: Bag = { status: "STAGED", stage_location: location };
    fields["stage_ref"] = ref !== "" ? ref : stableRef(taskId);
    const answer = await this.#taskWrite(taskId, fields);
    if (answer.ok) this.requestSync();
    return answer;
  }

  /** Report one shortage: the deliberate act, queued on the shortages scope. */
  async reportShortage(
    taskId: string,
    line: { orderLineNo: number | null; description: string; qtyShort: string; uom: string; note: string },
  ): Promise<Result<YardShortage>> {
    const id = this.#newKey();
    const at = this.#now();
    const data: Bag = {
      id,
      task_id: taskId,
      ...(line.orderLineNo !== null ? { order_line_no: line.orderLineNo } : {}),
      sku: "",
      description: line.description,
      qty_short: line.qtyShort,
      uom: line.uom,
      ...(line.note !== "" ? { note: line.note } : {}),
      reported_at: at,
      resolved_at: "",
      resolved_by: "",
      resolution: "",
    };
    const enqueued = await this.store.enqueue({ scope: "shortages", op: "create", entity: "shortages", payload: { ...data } });
    if (!enqueued.ok) return { ok: false, error: enqueued.message, status: 400 };
    await this.#sql.run(`INSERT INTO ${entityTable("shortages")} (id, seq, data, deleted) VALUES (?, 0, ?, 0)`, [
      id,
      JSON.stringify(data),
    ]);
    this.requestSync();
    return { ok: true, value: toShortage(data) };
  }

  /** Clear one shortage with a stated resolution, queued on the same scope. */
  async resolveShortage(taskId: string, shortageId: string, resolution: string): Promise<Result<YardShortage>> {
    const found = await this.store.read("shortages", shortageId);
    if (found.row === null) return { ok: false, error: "shortage not found", status: 404 };
    const enqueued = await this.#enqueueWithBase({
      scope: "shortages",
      op: "update",
      entity: "shortages",
      payload: { id: shortageId, resolution },
      row: found.row,
    });
    if (!enqueued.ok) return { ok: false, error: enqueued.message, status: 400 };
    await this.#patchRow("shortages", shortageId, (data) => ({
      ...data,
      resolution,
      resolved_at: this.#now(),
      resolved_by: this.#actorId,
    }));
    this.#writtenSincePull.add(`shortages:${shortageId}`);
    this.requestSync();
    const patched = await this.store.read("shortages", shortageId);
    return { ok: true, value: toShortage(patched.row?.data ?? {}) };
  }

  // ---- internals ------------------------------------------------------

  async #taskWrite(taskId: string, fields: Bag, payloadFields: Bag = fields): Promise<Result<YardTask>> {
    const found = await this.store.read("pick_tasks", taskId);
    if (found.row === null) return { ok: false, error: "task not found", status: 404 };
    const enqueued = await this.#enqueueWithBase({
      scope: "pick_tasks",
      op: "update",
      entity: "pick_tasks",
      payload: { ...payloadFields, id: taskId },
      row: found.row,
    });
    if (!enqueued.ok) return { ok: false, error: enqueued.message, status: 400 };
    await this.#patchRow("pick_tasks", taskId, (data) => ({ ...data, ...fields }));
    this.#writtenSincePull.add(`pick_tasks:${taskId}`);
    return { ok: true, value: toTask({ ...found.row.data, ...fields }) };
  }

  /**
   * Enqueue with the base rule: the row's pulled seq when nothing is queued
   * for the row yet, no base when this device already has an operation for it
   * waiting in the outbox.
   */
  async #enqueueWithBase(input: {
    scope: string;
    op: "update";
    entity: string;
    payload: Bag;
    row: StoredRowLike | null;
  }): Promise<{ ok: true; key: string; seq: number } | { ok: false; message: string }> {
    let baseSeq: number | undefined;
    if (input.row !== null) {
      const key = `${input.scope}:${input.row.id}`;
      const pending = await this.store.outbox.pending();
      const queued = pending.some((row) => row.scope === input.scope && row.entityId === input.row!.id);
      baseSeq = queued || this.#writtenSincePull.has(key) ? undefined : input.row.seq;
    }
    const answer = await this.store.enqueue({
      scope: input.scope,
      op: input.op,
      entity: input.entity,
      payload: input.payload,
      ...(baseSeq !== undefined ? { baseSeq } : {}),
    });
    return answer.ok ? { ok: true, key: answer.key, seq: answer.seq } : { ok: false, message: answer.message };
  }

  async #patchRow(scope: string, id: string, patch: (data: Bag) => Bag): Promise<void> {
    const raw = await this.#sql.all<{ id: string; data: string }>(`SELECT id, data FROM ${entityTable(scope)} WHERE id = ?`, [id]);
    if (raw.length === 0) return;
    let data: Bag = {};
    try {
      data = JSON.parse(raw[0]!.data) as Bag;
    } catch {
      return;
    }
    const next = patch(data);
    await this.#sql.run(`UPDATE ${entityTable(scope)} SET data = ? WHERE id = ?`, [JSON.stringify(next), id]);
  }
}

/** What opening answers: the layer, or none (the app then goes online through
 * the api as today), and whether this tab is a second one, which only reads
 * the cache. A second tab's cache answers every read empty, so it must never
 * stand in for the board: no layer, and the strip says read only. */
export interface YardOfflineOpened {
  layer: YardOffline | null;
  readOnly: boolean;
}

/** Opens the layer over a backend the host handed over. */
export async function openYardOfflineOver(
  backend: StorageBackend,
  options: {
    transport: SyncTransport;
    direct: YardDirectDoors;
    actorId: string;
    onChange?: () => void;
  },
): Promise<YardOfflineOpened> {
  const store = await OfflineStore.open({ backend, scopes: scopeBookFor(["yard-pick"]) });
  if (store.ownership() !== "owner") return { layer: null, readOnly: true };
  return {
    readOnly: false,
    layer: new YardOffline({
      store,
      sql: backend.sql,
      transport: options.transport,
      direct: options.direct,
      actorId: options.actorId,
      ...(options.onChange !== undefined ? { onChange: options.onChange } : {}),
    }),
  };
}

/** Opens the layer over the store's wa-sqlite OPFS backend (the browser demo
 * and the PWA). Answers no layer when the host cannot carry a store at all (no
 * Web Locks, no worker, no secure context): the app then behaves online as
 * today, and the strip keeps quiet, rather than the glass pretending to hold
 * a durable outbox it has. */
export async function openYardOfflineOpfs(options: {
  dbName: string;
  transport: SyncTransport;
  direct: YardDirectDoors;
  actorId: string;
  onChange?: () => void;
}): Promise<YardOfflineOpened> {
  const locks = (globalThis as { navigator?: { locks?: unknown } }).navigator?.locks;
  if (typeof Worker === "undefined" || !locks || typeof globalThis.crypto?.subtle !== "object") {
    return { layer: null, readOnly: false };
  }
  try {
    const backend: StorageBackend = await createOpfsBackend({
      dbName: options.dbName,
      locks: locks as Parameters<typeof createOpfsBackend>[0]["locks"],
      createWorker: () =>
        new Worker(new URL("../../../packages/offline-store/src/opfs-worker.ts", import.meta.url), {
          type: "module",
        }) as unknown as WorkerLike,
    });
    return await openYardOfflineOver(backend, options);
  } catch {
    return { layer: null, readOnly: false };
  }
}
