// HH Drive's thin offline data layer: the api port over the offline store,
// the mirror of Yard Pick's. The driver's reads compose from the store (the
// pulled runs and stops, with the scope's staleness); every write enqueues on
// its own scope (stops, pod, runs) and patches the local projection, so the
// run sheet shows the work before the replay lands; a cycle pushes the outbox
// and pulls the scopes back through the sync runner and the injected
// transport. The proof photos stay on their direct door: they are the
// attachments queue's own story, not this slice's.
//
// The base rule is the yard's, stated once there and held here: a write to a
// row with nothing queued names the row's pulled seq as its base; a write to a
// row this device already has queued goes blind, because the only stale base
// it could name is the device's own first write.
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
import type { DriverLine, DriverRun, DriverStop } from "./drive-api.js";
import { toLine, toRun, toStop } from "./drive-api.js";

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

export interface DriveOfflineOptions {
  store: OfflineStore;
  sql: SqlExecutor;
  transport: SyncTransport;
  /** Told whenever the outbox's depth or the cards may have moved, so the
   * chrome's counts are never a stale word. */
  onChange?: () => void;
  now?: () => string;
  newKey?: () => string;
}

export type RunsAnswer = Result<{ runs: DriverRun[] }> & { staleness: Staleness };

interface StoredRowLike {
  id: string;
  seq: number;
  data: Bag;
}

export class DriveOffline {
  readonly store: OfflineStore;
  readonly runner: SyncRunner;
  readonly #sql: SqlExecutor;
  readonly #onChange: (() => void) | undefined;
  readonly #now: () => string;
  readonly #newKey: () => string;

  constructor(options: DriveOfflineOptions) {
    this.store = options.store;
    this.#sql = options.sql;
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
    this.#onChange?.();
  }

  /** Ask for a cycle without waiting for it (a write just landed in the outbox). */
  requestSync(): void {
    this.#onChange?.();
    void this.runner.request().then(() => this.#onChange?.());
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

  /** One cycle the caller waits on (a direct door just moved server state the
   * next pull should carry: a photo uploaded beside the proof). */
  async syncNow(): Promise<void> {
    await this.runner.request();
    this.#onChange?.();
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

  /** The raw door, for the fence proof. */
  enqueueRaw(input: EnqueueInput) {
    return this.store.enqueue(input);
  }

  // ---- reads ----------------------------------------------------------

  async staleness(scope: string): Promise<Staleness> {
    return this.store.staleness(scope);
  }

  /** The day's runs, composed from the store. The day is the dealer's own
   * naming; the demo world holds one day, so the answer is the same runs for
   * any day it was pulled on, with the staleness saying how old they are. */
  async runs(day: string): Promise<RunsAnswer> {
    const { rows, staleness } = await this.store.list("runs");
    const stops = await this.store.list("stops");
    const runStops = stops.rows
      .map((row) => this.#composeStop(row))
      .sort((a, b) => a.stop_no - b.stop_no);
    const lines: Record<string, readonly DriverLine[]> = {};
    for (const row of stops.rows) {
      const raw = Array.isArray(row.data["lines"]) ? (row.data["lines"] as unknown[]) : [];
      lines[row.id] = raw.map(toLine);
    }
    const runs = rows
      .filter((runRow) => runRow.data["scheduled_date"] === day)
      .map((runRow) => {
        const own = runStops.filter((stop) => stops.rows.find((r) => r.id === stop.id)?.data["run_id"] === runRow.id);
        return toRun({ ...runRow.data, stops: own, stop_lines: lines });
      })
      .sort((a, b) => a.run_no.localeCompare(b.run_no));
    return { ok: true, value: { runs }, staleness };
  }

  #composeStop(row: StoredRowLike): DriverStop {
    return toStop({ ...row.data });
  }

  // ---- writes ---------------------------------------------------------

  /** A plain move along the stop's lifecycle. Delivered is never asked of this
   * door: the core takes it only through the proof. */
  async advance(stopId: string, status: "LOADED" | "EN_ROUTE" | "FAILED", reason = ""): Promise<Result<DriverStop>> {
    const found = await this.store.read("stops", stopId);
    if (found.row === null) return { ok: false, error: "stop not found", status: 404 };
    // The wall the store already knows is checked at the door, in the server's
    // own words: an undelivered run the dispatcher never sent refuses the
    // drive here, before anything is queued.
    if (status === "EN_ROUTE") {
      const runRow = await this.store.read("runs", String(found.row.data["run_id"] ?? ""));
      if (runRow.row !== null && runRow.row.data["status"] !== "DISPATCHED") {
        return { ok: false, error: "the run has not been dispatched", status: 409 };
      }
    }
    // The payload declares the wire shape (status, and the reason a failed
    // stop carries); the local projection keeps the stamped fields too.
    const fields: Bag = { status };
    if (status === "FAILED") fields["failure_reason"] = reason;
    const payload: Bag = { id: stopId, status };
    if (status === "FAILED" && reason !== "") payload["reason"] = reason;
    const enqueued = await this.#enqueueWithBase({
      scope: "stops",
      op: "update",
      entity: "stops",
      payload,
      row: found.row,
    });
    if (!enqueued.ok) return { ok: false, error: enqueued.message, status: 400 };
    const at = this.#now();
    await this.#patchRow("stops", stopId, (data) => ({
      ...data,
      ...fields,
      ...(status === "LOADED" ? { loaded_at: at } : {}),
      ...(status === "EN_ROUTE" ? { en_route_at: at } : {}),
    }));
    this.requestSync();
    return { ok: true, value: toStop({ ...found.row.data, ...fields }) };
  }

  /** The delivery's proof: the typed name, the recipient's own confirmation. */
  async pod(stopId: string, receivedBy: string): Promise<Result<DriverStop>> {
    const found = await this.store.read("stops", stopId);
    if (found.row === null) return { ok: false, error: "stop not found", status: 404 };
    const id = this.#newKey();
    const at = this.#now();
    const enqueued = await this.store.enqueue({
      scope: "pod",
      op: "create",
      entity: "pod",
      payload: { id, stop_id: stopId, received_by: receivedBy, affirmed: true, created_at: at },
    });
    if (!enqueued.ok) return { ok: false, error: enqueued.message, status: 400 };
    const fields: Bag = { status: "DELIVERED", pod_received_by: receivedBy, pod_at: at, delivered_at: at };
    await this.#patchRow("stops", stopId, (data) => ({ ...data, ...fields }));
    this.requestSync();
    return { ok: true, value: toStop({ ...found.row.data, ...fields }) };
  }

  // ---- internals ------------------------------------------------------

  async #enqueueWithBase(input: {
    scope: string;
    op: "update";
    entity: string;
    payload: Bag;
    row: StoredRowLike | null;
  }): Promise<{ ok: true } | { ok: false; message: string }> {
    let baseSeq: number | undefined;
    if (input.row !== null) {
      const pending = await this.store.outbox.pending();
      const queued = pending.some((row) => row.scope === input.scope && row.entityId === input.row!.id);
      baseSeq = queued ? undefined : input.row.seq;
    }
    const answer = await this.store.enqueue({
      scope: input.scope,
      op: input.op,
      entity: input.entity,
      payload: input.payload,
      ...(baseSeq !== undefined ? { baseSeq } : {}),
    });
    return answer.ok ? { ok: true } : { ok: false, message: answer.message };
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
    await this.#sql.run(`UPDATE ${entityTable(scope)} SET data = ? WHERE id = ?`, [JSON.stringify(patch(data)), id]);
  }
}

/** Opens the layer over the store's wa-sqlite OPFS backend (the browser demo
 * and the PWA). Answers null when the host cannot carry a store at all: the
 * app then behaves online as today, and the strip keeps quiet. */
export async function openDriveOfflineOpfs(options: {
  dbName: string;
  transport: SyncTransport;
  onChange?: () => void;
}): Promise<DriveOffline | null> {
  const locks = (globalThis as { navigator?: { locks?: unknown } }).navigator?.locks;
  if (typeof Worker === "undefined" || !locks || typeof globalThis.crypto?.subtle !== "object") return null;
  try {
    const backend: StorageBackend = await createOpfsBackend({
      dbName: options.dbName,
      locks: locks as Parameters<typeof createOpfsBackend>[0]["locks"],
      createWorker: () =>
        new Worker(new URL("../../../packages/offline-store/src/opfs-worker.ts", import.meta.url), {
          type: "module",
        }) as unknown as WorkerLike,
    });
    const store = await OfflineStore.open({ backend, scopes: scopeBookFor(["drive-run"]) });
    return new DriveOffline({
      store,
      sql: backend.sql,
      transport: options.transport,
      ...(options.onChange !== undefined ? { onChange: options.onChange } : {}),
    });
  } catch {
    return null;
  }
}
