// Yard Pick's thin offline data layer: the api port over the offline store.
// Reads compose from the store (the pulled pick_tasks and shortages rows, with
// the scope's staleness); the writes the core admits through sync (claim,
// start, stage, the shortage report) enqueue on the write's own scope with a
// stored idempotency key and patch the local projection, so the glass shows
// the work before the replay lands; a cycle pushes the outbox and pulls the
// scopes back through the sync runner and the injected transport.
//
// The pick record (the lines of what was pulled) and the shortage report queue
// as their own operations: the record on pick_tasks with exactly the door's
// line legs (core/internal/tasks/syncpick.go), the report on shortages without
// the stamped legs. The writes the core does not admit through sync stay on
// their direct doors and need signal: the skid count (it moves the order's
// skid deposit, which is money, and the weights scope is a different feed) and
// the shortage settle (the resolution legs are the counter's act). They wait
// for the queued writes first and answer an honest "no signal" offline; the
// pull that follows carries the result into the store. The writable sets are
// the Go sets in core/internal/tasks (syncscope.go, syncpick.go,
// syncweights.go, syncshortage.go), pinned by test/stub-sync-contract.test.mjs.
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
import { LOCAL_LINE, pickRecordProblem } from "./yard-logic.js";

export { isLocalLine } from "./yard-logic.js";
import { isLocalLine } from "./yard-logic.js";

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
export type YardDirectDoors = Pick<YardApi, "recordSkids" | "resolveShortage">;

/** The local row's copy of what it held before this device's record, so a
 * record the core refuses puts the server's lines back, not an empty list. */
const PICKED_BEFORE = "__picked_before";

/** One line as the door reads it: the nine legs and nothing else. */
function wireLine(l: YardRecordLine): Bag {
  return {
    order_line_no: l.order_line_no,
    product_id: l.product_id,
    sku: l.sku,
    description: l.description,
    qty_ordered: l.qty_ordered,
    qty_picked: l.qty_picked,
    uom: l.uom,
    short_reason: l.short_reason,
    short_note: l.short_note,
  };
}

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
  /** Rows this device has written since the last completed cycle, each with
   * the number of its latest write: a write to one of them goes blind even
   * when the earlier operation has already drained, because the pulled seq
   * that a base would name is stale until the cycle's pull lands. A cycle
   * forgets only the writes made before it was asked for: a write made while
   * it ran may reach the server in the next cycle, after this one's pull. */
  readonly #writtenSincePull = new Map<string, number>();
  #writes = 0;
  /** Cycles asked for and not yet rested: while one runs, its pull may land
   * at any moment, so no row's seq can be trusted as a base. */
  #inFlight = 0;

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
    const mark = this.#writes;
    await this.#sync(async () => {
      await this.runner.dispatch({ type: "ONLINE" });
      await this.runner.request();
      await this.runner.settled();
    });
    await this.#rested(mark);
    this.#onChange?.();
  }

  /** Ask for a cycle without waiting for it (a write just landed in the outbox). */
  requestSync(): void {
    // The count moved now, and it moves again when the replay lands or fails.
    this.#onChange?.();
    const mark = this.#writes;
    void this.#sync(() => this.runner.request()).then(async () => {
      await this.#rested(mark);
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
    return this.#sync(() => this.runner.dispatch(online ? { type: "ONLINE" } : { type: "OFFLINE" }));
  }

  async #sync<T>(run: () => Promise<T>): Promise<T> {
    this.#inFlight += 1;
    try {
      return await run();
    } finally {
      this.#inFlight -= 1;
    }
  }

  #markWritten(key: string): void {
    this.#writes += 1;
    this.#writtenSincePull.set(key, this.#writes);
  }

  /** A cycle came to rest: forget the writes its pull covered, and take back
   * any record of lines the core refused (the card carries its words). */
  async #rested(mark: number): Promise<void> {
    this.#pulled(mark);
    await this.#takeBackUnconfirmedRecords();
  }

  /** A pick record the outbox no longer holds, whose local lines the pull did
   * not replace, was refused: the lines come off the row (the pick's own
   * earlier lines return), so the glass never shows a record the server does
   * not hold. Runs only at rest, with the pull landed. */
  async #takeBackUnconfirmedRecords(): Promise<void> {
    if (this.runner.state !== "idle" && this.runner.state !== "conflict") return;
    const queued = new Set(
      (await this.store.outbox.pending())
        .filter((row) => row.scope === "pick_tasks" && "picked" in row.payload)
        .map((row) => row.entityId),
    );
    const { rows } = await this.store.list("pick_tasks");
    for (const row of rows) {
      const lines = row.data["picked"];
      if (queued.has(row.id) || !Array.isArray(lines) || !lines.some((l) => isLocalLine({ id: String((l as Bag)["id"] ?? "") }))) continue;
      await this.#patchRow("pick_tasks", row.id, (data) => {
        const { [PICKED_BEFORE]: before, ...rest } = data;
        return { ...rest, picked: Array.isArray(before) ? before : [] };
      });
    }
  }

  /** Send the queue now (the picker is standing at a connected device and
   * wants the server's answer) and give back words if the server did not take
   * the pick's record during the send: the core's own words for a refusal, or
   * a plain sentence when the server moved the ticket first and the record
   * was superseded. Empty when it took it or could not be reached. Either
   * answer is also a card, which waits to be dismissed. */
  async sendRecordNow(taskId: string): Promise<string> {
    const before = new Set((await this.openCards()).map((card) => card.id));
    await this.#drain();
    const fresh = (await this.openCards()).filter(
      (card) => !before.has(card.id) && card.scope === "pick_tasks" && card.clientPayload["id"] === taskId && "picked" in card.clientPayload,
    );
    const refused = fresh.find((card) => card.kind === "rejected");
    if (refused !== undefined) return refused.error ?? "";
    if (fresh.some((card) => card.kind === "superseded")) {
      return "The ticket changed on the server first, so your record was not saved. Check the ticket and record it again.";
    }
    return "";
  }

  /** True while a record of this pick waits in the outbox. */
  async pendingRecord(taskId: string): Promise<boolean> {
    return (await this.store.outbox.pending()).some((row) => row.scope === "pick_tasks" && row.entityId === taskId && "picked" in row.payload);
  }

  /** A cycle rested with its pull landed: the rows carry the server's seq for
   * every write made up to `mark`. A cycle that rested offline or unreachable
   * pulled nothing, so it forgets nothing. */
  #pulled(mark: number): void {
    if (this.runner.state !== "idle" && this.runner.state !== "conflict") return;
    for (const [key, written] of this.#writtenSincePull) if (written <= mark) this.#writtenSincePull.delete(key);
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

  /** Record what was pulled: queued as its own pick_tasks operation, the
   * door's nine line legs and nothing else beside the row address. The local
   * row shows the lines at once (marked as this device's), the replay
   * overwrites them with the server's, and a record the core refuses comes
   * back as a card with its words. A record the door would refuse for its
   * shape or its text is refused here, in words, before it is queued: a
   * queued poison op would only come back as a card the picker cannot act on. */
  async recordLines(taskId: string, lines: readonly YardRecordLine[]): Promise<Result<YardPickedLine[]>> {
    const problem = pickRecordProblem(lines);
    if (problem !== "") return { ok: false, error: problem, status: 400 };
    const found = await this.store.read("pick_tasks", taskId);
    if (found.row === null) return { ok: false, error: "task not found", status: 404 };
    const wire = lines.map(wireLine);
    const enqueued = await this.#enqueueWithBase({
      scope: "pick_tasks",
      op: "update",
      entity: "pick_tasks",
      payload: { id: taskId, picked: wire },
      row: found.row,
    });
    if (!enqueued.ok) return { ok: false, error: enqueued.message, status: 400 };
    const at = this.#now();
    const local = wire.map((line) => ({
      ...line,
      id: `${LOCAL_LINE}${taskId}:${String(line["order_line_no"])}`,
      task_id: taskId,
      picked_at: at,
    }));
    await this.#patchRow("pick_tasks", taskId, (data) => ({
      ...data,
      [PICKED_BEFORE]: PICKED_BEFORE in data ? data[PICKED_BEFORE] : (data["picked"] ?? []),
      picked: local,
    }));
    this.#markWritten(`pick_tasks:${taskId}`);
    this.requestSync();
    return { ok: true, value: local.map(toPickedLine) };
  }

  /** Record or change the order's skid count: the direct door, always. The
   * count moves the order's skid deposit, which is money, so it never queues. */
  async recordSkids(taskId: string, skidCount: number, reason: string, note: string): Promise<Result<YardSkidAnswer>> {
    const refused = this.#noSignal<YardSkidAnswer>() ?? (await this.#outboxFirst<YardSkidAnswer>("Save the skid count"));
    if (refused !== null) return refused;
    return this.#direct.recordSkids(taskId, skidCount, reason, note);
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
    // The create declares the report itself; the stamps and the resolution
    // legs are the server's, so only the local row carries them.
    const payload: Bag = {
      id,
      task_id: taskId,
      ...(line.orderLineNo !== null ? { order_line_no: line.orderLineNo } : {}),
      description: line.description,
      qty_short: line.qtyShort,
      uom: line.uom,
      ...(line.note !== "" ? { note: line.note } : {}),
    };
    const enqueued = await this.store.enqueue({ scope: "shortages", op: "create", entity: "shortages", payload });
    if (!enqueued.ok) return { ok: false, error: enqueued.message, status: 400 };
    await this.#sql.run(`INSERT INTO ${entityTable("shortages")} (id, seq, data, deleted) VALUES (?, 0, ?, 0)`, [
      id,
      JSON.stringify(data),
    ]);
    // The report moves the task row's seq on the server: later writes to the
    // task go blind until a pull has carried that seq.
    this.#markWritten(`pick_tasks:${taskId}`);
    this.requestSync();
    return { ok: true, value: toShortage(data) };
  }

  /** Clear one shortage with a stated resolution: the direct door. The
   * resolution legs are the counter's act and the core's shortage sync set
   * refuses them, so the settle is sent as itself and the pull lands it. */
  async resolveShortage(taskId: string, shortageId: string, resolution: string): Promise<Result<YardShortage>> {
    const refused = this.#noSignal<YardShortage>() ?? (await this.#outboxFirst<YardShortage>("Settle the shortage"));
    if (refused !== null) return refused;
    const answer = await this.#direct.resolveShortage(taskId, shortageId, resolution);
    if (answer.ok) await this.#drain();
    return answer;
  }

  // ---- internals ------------------------------------------------------

  /** Send what is queued and wait for the cycle to rest. True when the
   * outbox is empty after it: the direct door may then go. */
  async #drain(): Promise<boolean> {
    const mark = this.#writes;
    await this.#sync(async () => {
      await this.runner.request();
      await this.runner.settled();
    });
    const empty = (await this.waiting()) === 0;
    if (empty) await this.#rested(mark);
    this.#onChange?.();
    return empty;
  }

  /** The honest state of a direct door with no signal: no answer from a
   * server (no status), so the app's own "check your signal" words show. */
  #noSignal<T>(): Result<T> | null {
    return this.runner.state === "offline" ? { ok: false, error: "no signal" } : null;
  }

  /** A direct door never jumps the outbox: the queued writes go first, and
   * while any is still waiting the door is refused in words, since the core
   * would judge the direct write against a pick that has not caught up. */
  async #outboxFirst<T>(what: string): Promise<Result<T> | null> {
    if (await this.#drain()) return null;
    return {
      ok: false,
      error: `Your earlier work is still waiting to send. ${what} once it has gone through.`,
      status: 409,
    };
  }

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
    this.#markWritten(`pick_tasks:${taskId}`);
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
      // A shortage this device queued for the task moves the task row's seq on
      // the server too (the report bumps its pick seq so the pull carries the
      // counts), so it counts as a write to the task: a base named before it
      // would come back superseded for the device's own report.
      const queued = pending.some(
        (row) =>
          (row.scope === input.scope && row.entityId === input.row!.id) ||
          (input.scope === "pick_tasks" && row.scope === "shortages" && row.payload["task_id"] === input.row!.id),
      );
      // The row is read again here, after the checks: a pull that landed since
      // the caller read it moved its seq, and a base named from the older read
      // would meet the device's own earlier write as a conflict.
      const fresh = await this.store.read(input.scope, input.row.id);
      const syncing = this.#inFlight > 0 || this.runner.state === "pushing" || this.runner.state === "pulling";
      baseSeq = queued || syncing || fresh.row === null || this.#writtenSincePull.has(key) ? undefined : fresh.row.seq;
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
