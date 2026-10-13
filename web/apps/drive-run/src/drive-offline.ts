// HH Drive's thin offline data layer: the api port over the offline store,
// the mirror of Yard Pick's. The driver's reads compose from the store (the
// pulled runs and stops, with the scope's staleness); the stop moves enqueue on
// the stops scope and patch the local projection, so the run sheet shows the
// work before the replay lands; a cycle pushes the outbox and pulls the scopes
// back through the sync runner and the injected transport. The writes the core
// does not admit through sync stay on their direct doors and need signal: the
// proof of delivery (the core's pod scope is the photo record, the typed name
// attestation has no sync door) and the run close (the manifest gives the runs
// scope no writable policy). Both wait for the queued moves first. The proof
// photos stay on their own direct door too: they are the attachments queue's
// own story, not this slice's.
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
import type { DriveApi, DriverLine, DriverRun, DriverStop } from "./drive-api.js";
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
  /** The direct doors: the writes the core does not admit through sync (the
   * run close, the proof of delivery). They wait on the outbox and need
   * signal; nothing about them queues. */
  direct: Pick<DriveApi, "completeRun" | "pod">;
  /** Told whenever the outbox's depth or the cards may have moved, so the
   * chrome's counts are never a stale word. */
  onChange?: () => void;
  now?: () => string;
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
  readonly #direct: Pick<DriveApi, "completeRun" | "pod">;
  readonly #onChange: (() => void) | undefined;
  readonly #now: () => string;
  /** Rows this device has written since the last completed cycle: a write to
   * one of them goes blind even when the earlier operation has already
   * drained, because the pulled seq that a base would name is stale until the
   * cycle's pull lands. */
  readonly #writtenSincePull = new Set<string>();

  constructor(options: DriveOfflineOptions) {
    this.store = options.store;
    this.#sql = options.sql;
    this.#direct = options.direct;
    this.#onChange = options.onChange;
    this.#now = options.now ?? (() => new Date().toISOString());
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

  /** One cycle the caller waits on (a direct door just moved server state the
   * next pull should carry: a photo uploaded beside the proof). */
  async syncNow(): Promise<void> {
    await this.runner.request();
    this.#writtenSincePull.clear();
    this.#onChange?.();
  }

  /** Send what is queued and wait for the cycle to rest. True when the
   * outbox is empty after it: only then has the pull landed too, so only then
   * does the written since pull set clear. */
  async #drain(): Promise<boolean> {
    await this.runner.request();
    await this.runner.settled();
    const empty = (await this.waiting()) === 0;
    if (empty) this.#writtenSincePull.clear();
    this.#onChange?.();
    return empty;
  }

  /** The honest state of a direct door with no signal: no answer from a
   * server (no status), so the app's own "check your signal" words show. */
  #noSignal<T>(): Result<T> | null {
    return this.runner.state === "offline" ? { ok: false, error: "no signal" } : null;
  }

  /** A direct door never jumps the outbox: the queued stop moves go first,
   * and while any is still waiting the door is refused in words, since the
   * core would refuse it and name the open stop. */
  async #outboxFirst<T>(what: string): Promise<Result<T> | null> {
    if (await this.#drain()) return null;
    return {
      ok: false,
      error: `Your earlier moves are still waiting to send. ${what} once they have gone through.`,
      status: 409,
    };
  }

  /** Close the run on its direct door (the manifest gives the runs scope no
   * writable policy). */
  async completeRun(runId: string): Promise<Result<true>> {
    const refused = this.#noSignal<true>() ?? (await this.#outboxFirst<true>("Close the run"));
    if (refused !== null) return refused;
    const answer = await this.#direct.completeRun(runId);
    // The closed run lands in the store only through the next pull.
    await this.#drain();
    return answer;
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
    if (status === "FAILED" && reason !== "") payload["failure_reason"] = reason;
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
    this.#writtenSincePull.add(`stops:${stopId}`);
    this.requestSync();
    return { ok: true, value: toStop({ ...found.row.data, ...fields }) };
  }

  /** The delivery's proof: the typed name, the recipient's own confirmation.
   * The attestation has no sync door in the core (its pod scope is the photo
   * record), so it stays on its direct door: it waits for the queued moves of
   * its stop and needs signal, and the delivered stop lands in the store
   * through the pull that follows. */
  async pod(stopId: string, receivedBy: string): Promise<Result<DriverStop>> {
    const refused = this.#noSignal<DriverStop>() ?? (await this.#outboxFirst<DriverStop>("Sign for the delivery"));
    if (refused !== null) return refused;
    const answer = await this.#direct.pod(stopId, receivedBy);
    if (answer.ok) await this.#drain();
    return answer;
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

/** What opening answers: the layer, or none (the app then goes online through
 * the api as today), and whether this tab is a second one, which only reads
 * the cache. A second tab's cache answers every read empty, so it must never
 * stand in for the run sheet: no layer, and the strip says read only. */
export interface DriveOfflineOpened {
  layer: DriveOffline | null;
  readOnly: boolean;
}

/** Opens the layer over a backend the host handed over. */
export async function openDriveOfflineOver(
  backend: StorageBackend,
  options: {
    transport: SyncTransport;
    direct: Pick<DriveApi, "completeRun" | "pod">;
    onChange?: () => void;
  },
): Promise<DriveOfflineOpened> {
  const store = await OfflineStore.open({ backend, scopes: scopeBookFor(["drive-run"]) });
  if (store.ownership() !== "owner") return { layer: null, readOnly: true };
  return {
    readOnly: false,
    layer: new DriveOffline({
      store,
      sql: backend.sql,
      transport: options.transport,
      direct: options.direct,
      ...(options.onChange !== undefined ? { onChange: options.onChange } : {}),
    }),
  };
}

/** Opens the layer over the store's wa-sqlite OPFS backend (the browser demo
 * and the PWA). Answers no layer when the host cannot carry a store at all:
 * the app then behaves online as today, and the strip keeps quiet. */
export async function openDriveOfflineOpfs(options: {
  dbName: string;
  transport: SyncTransport;
  direct: Pick<DriveApi, "completeRun" | "pod">;
  onChange?: () => void;
}): Promise<DriveOfflineOpened> {
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
    return await openDriveOfflineOver(backend, options);
  } catch {
    return { layer: null, readOnly: false };
  }
}
