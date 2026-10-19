// HH Drive's thin offline data layer: the api port over the offline store,
// the mirror of Yard Pick's. The driver's reads compose from the store (the
// pulled runs and stops, with the scope's staleness); the stop moves and the
// proof of delivery enqueue on their own scopes and patch the local projection,
// so the run sheet shows the work before the replay lands; a cycle pushes the
// outbox and pulls the scopes back through the sync runner and the injected
// transport. The proof is the typed name attestation (core/internal/dispatch
// syncscope_pod_attest.go): a pod create {id, stop_id, received_by, affirmed}
// whose effect is the direct door's own, and the stop reaches the device
// delivered through the stops feed. The writes the core does not admit through
// sync stay on their direct doors and need signal: the run close (the manifest
// gives the runs scope no writable policy), which waits for the queued moves
// first. The proof photos stay on their own direct door too: they are the
// attachments queue's own story, not this slice's, and say no signal offline.
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
import { nameProblem, normalizeName } from "./drive-logic.js";

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

/** The writes the core does not admit through sync: the run close. It waits
 * on the outbox and needs signal; nothing about it queues. */
export type DriveDirectDoors = Pick<DriveApi, "completeRun">;

/** What a stop held before this device's signature, so a signature the core
 * refuses puts the stop back as the server last had it. */
const PROOF_BEFORE = "__proof_before";

export interface DriveOfflineOptions {
  store: OfflineStore;
  sql: SqlExecutor;
  transport: SyncTransport;
  direct: DriveDirectDoors;
  /** The id for the outbox's local row of a create. */
  newKey?: () => string;
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
  readonly #direct: DriveDirectDoors;
  readonly #newKey: () => string;
  readonly #onChange: (() => void) | undefined;
  readonly #now: () => string;
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

  constructor(options: DriveOfflineOptions) {
    this.store = options.store;
    this.#sql = options.sql;
    this.#direct = options.direct;
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

  /** One cycle the caller waits on (a direct door just moved server state the
   * next pull should carry: a photo uploaded beside the proof). */
  async syncNow(): Promise<void> {
    const mark = this.#writes;
    await this.#sync(() => this.runner.request());
    await this.#rested(mark);
    this.#onChange?.();
  }

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
   * any signature the core refused (the card carries its words). */
  async #rested(mark: number): Promise<void> {
    this.#pulled(mark);
    await this.#takeBackRefusedProofs();
  }

  /** A signature the core refused no longer stands on the glass: the stop
   * goes back to what the server last had it as, so the run sheet never shows
   * a delivery the server does not hold. Only a REFUSED proof is taken back;
   * a proof that applied is replaced by the stops pull, which carries the
   * delivery. Runs only at rest, with the pull landed. */
  async #takeBackRefusedProofs(): Promise<void> {
    if (this.runner.state !== "idle" && this.runner.state !== "conflict") return;
    const stops = await this.store.list("stops");
    const held = stops.rows.filter((row) => PROOF_BEFORE in row.data);
    if (held.length === 0) return;
    // By the proof's own id: an earlier refused signature on the stop must not
    // take back a later one that applied.
    const refused = new Set((await this.store.outbox.list("rejected")).filter((r) => r.scope === "pod").map((r) => String(r.payload["id"])));
    for (const row of held) {
      if (!refused.has(String((row.data[PROOF_BEFORE] as Bag)["proof_id"]))) continue;
      await this.#patchRow("stops", row.id, (data) => {
        const { [PROOF_BEFORE]: before, ...rest } = data;
        const { proof_id: _proofId, ...fields } = before as Bag;
        return { ...rest, ...fields };
      });
    }
  }

  /** Send the queue now (the driver is standing at a connected device and
   * wants the server's answer) and give back the core's words if it refused
   * the stop's signature during the send; empty when it took it or could not
   * be reached. The refusal is also a card, which waits to be dismissed. */
  async sendProofNow(stopId: string): Promise<string> {
    const before = new Set((await this.openCards()).map((card) => card.id));
    await this.#drain();
    const fresh = (await this.openCards()).find(
      (card) => !before.has(card.id) && card.kind === "rejected" && card.scope === "pod" && card.clientPayload["stop_id"] === stopId,
    );
    return fresh?.error ?? "";
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

  /** The dealer's business zone, as the surface config read last served it:
   * a small metadata table beside the scopes, so a fully offline cold start
   * reads the run sheet on the zone the last online session held instead of
   * the Ontario default. Nothing here is a credential or a user's word: it is
   * the dealer's own IANA zone name from the config read. */
  async saveZone(zone: string): Promise<void> {
    await this.#sql.run("CREATE TABLE IF NOT EXISTS _drive_meta (key TEXT PRIMARY KEY, value TEXT)", []);
    await this.#sql.run(
      "INSERT INTO _drive_meta (key, value) VALUES ('business_zone', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
      [zone],
    );
  }

  /** The zone the last served read stored, or null when none has been (an
   * empty or absent value reads as none, so a cleared zone restores nothing). */
  async readZone(): Promise<string | null> {
    await this.#sql.run("CREATE TABLE IF NOT EXISTS _drive_meta (key TEXT PRIMARY KEY, value TEXT)", []);
    const rows = await this.#sql.all<{ value: string }>("SELECT value FROM _drive_meta WHERE key = 'business_zone'", []);
    const value = rows[0]?.value ?? "";
    return value !== "" ? value : null;
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
    const waitingProofs = new Set(
      (await this.store.outbox.pending()).filter((r) => r.scope === "pod").map((r) => String(r.payload["stop_id"])),
    );
    const runStops = stops.rows
      .map((row) => this.#composeStop(row, waitingProofs.has(row.id)))
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

  #composeStop(row: StoredRowLike, proofWaiting: boolean): DriverStop {
    return toStop({ ...row.data, proof_waiting: proofWaiting });
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
    this.#markWritten(`stops:${stopId}`);
    this.requestSync();
    return { ok: true, value: toStop({ ...found.row.data, ...fields }) };
  }

  /** The delivery's proof: the typed name, the recipient's own confirmation.
   * Queued as a pod create with the attestation's exact fields and an id for
   * the outbox's local row; the local stop shows delivered and waiting until
   * the pull carries the server's. A name the door would refuse, or a stop
   * that is not on the road, is refused here with words, before anything is
   * queued: a queued poison op only comes back as a card the driver cannot
   * act on after leaving the stop. */
  async pod(stopId: string, receivedBy: string): Promise<Result<DriverStop>> {
    const name = normalizeName(receivedBy);
    if (name === "") return { ok: false, error: "Type the name of the person receiving.", status: 400 };
    const problem = nameProblem(name);
    if (problem !== "") return { ok: false, error: problem, status: 400 };
    const found = await this.store.read("stops", stopId);
    if (found.row === null) return { ok: false, error: "stop not found", status: 404 };
    // The core's transition rule, in its words: only a stop on the road is delivered.
    if (found.row.data["status"] !== "EN_ROUTE") return { ok: false, error: "illegal status transition", status: 409 };
    const proofId = this.#newKey();
    const enqueued = await this.store.enqueue({
      scope: "pod",
      op: "create",
      entity: "pod",
      payload: { id: proofId, stop_id: stopId, received_by: name, affirmed: true },
    });
    if (!enqueued.ok) return { ok: false, error: enqueued.message, status: 400 };
    const at = this.#now();
    const fields: Bag = { status: "DELIVERED", pod_received_by: name, pod_at: at, delivered_at: at };
    await this.#patchRow("stops", stopId, (data) => ({
      ...data,
      [PROOF_BEFORE]: {
        proof_id: proofId,
        status: data["status"],
        pod_received_by: data["pod_received_by"] ?? "",
        pod_at: data["pod_at"] ?? "",
        delivered_at: data["delivered_at"] ?? "",
      },
      ...fields,
    }));
    this.#markWritten(`stops:${stopId}`);
    this.requestSync();
    return { ok: true, value: toStop({ ...found.row.data, ...fields, proof_waiting: true }) };
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
    direct: DriveDirectDoors;
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
  direct: DriveDirectDoors;
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
