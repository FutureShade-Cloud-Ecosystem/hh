// The sync driver of a Work desk app: the sync statechart's runner over the
// store, the connectivity word fed to it, and the cycle (push the outbox, then
// pull the scopes) the app asks for. A read cache only app runs the same
// driver; its outbox is empty by construction, so a cycle is only a pull.
import {
  OfflineStore,
  SyncRunner,
  createOpfsBackend,
  type Card,
  type OutboxStall,
  type Ownership,
  type SqlExecutor,
  type StorageBackend,
  type SyncTransport,
  type WorkerLike,
} from "@hardscapehouse/offline-store";
import * as syncMachine from "../../../../statecharts/sync/machine.js";
import { openWorkStore } from "./read-cache.js";

/** The banner mode the chrome's offline strip takes, from the runner's state and the backend's ownership. */
export function bannerMode(state: string, ownership: Ownership): string {
  if (ownership !== "owner") return "read-only";
  if (state === "offline") return "offline";
  if (state === "unreachable") return "unreachable";
  if (state === "auth_expired") return "auth-expired";
  return "";
}

export interface WorkSyncOptions {
  store: OfflineStore;
  transport: SyncTransport;
  /** Told whenever the outbox's depth or the cards may have moved. */
  onChange?: () => void;
  /** Told after a cycle rested with its pull landed. */
  onPulled?: () => void;
}

export class WorkSync {
  readonly store: OfflineStore;
  readonly runner: SyncRunner;
  readonly #onChange: (() => void) | undefined;
  readonly #onPulled: (() => void) | undefined;
  #inFlight = 0;
  #lastOnline: boolean | null = null;

  constructor(options: WorkSyncOptions) {
    this.store = options.store;
    this.#onChange = options.onChange;
    this.#onPulled = options.onPulled;
    this.runner = new SyncRunner({ store: options.store, transport: options.transport, machine: syncMachine });
  }

  /** One full cycle, to rest: push the outbox, then pull the scopes. */
  async cycle(): Promise<void> {
    await this.#sync(async () => {
      await this.runner.dispatch({ type: "ONLINE" });
      await this.runner.request();
      await this.runner.settled();
    });
    this.#rested();
  }

  /** Ask for a cycle without waiting for it (a write just landed in the outbox). */
  requestSync(): void {
    this.#onChange?.();
    void this.#sync(() => this.runner.request()).then(() => this.#rested());
  }

  /** Feed the platform's connectivity to the machine; a repeated word is ignored. */
  setOnline(online: boolean): Promise<void> {
    if (this.#lastOnline === online) return Promise.resolve();
    this.#lastOnline = online;
    return this.#sync(() => this.runner.dispatch(online ? { type: "ONLINE" } : { type: "OFFLINE" }));
  }

  get syncing(): boolean {
    return this.#inFlight > 0;
  }

  async #sync<T>(run: () => Promise<T>): Promise<T> {
    this.#inFlight += 1;
    try {
      return await run();
    } finally {
      this.#inFlight -= 1;
    }
  }

  #rested(): void {
    if (this.runner.state === "idle" || this.runner.state === "conflict") this.#onPulled?.();
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
}

/** What opening answers: the store and its sync driver, or none (the app then goes online as today), and whether this tab is a second one that only reads the cache. */
export interface WorkOffline {
  store: OfflineStore;
  sync: WorkSync;
  sql: SqlExecutor;
}

export interface WorkOpened {
  layer: WorkOffline | null;
  readOnly: boolean;
}

export async function openWorkOfflineOver(
  backend: StorageBackend,
  options: { appId: string; transport: SyncTransport; onChange?: () => void; onPulled?: () => void; now?: () => string; newKey?: () => string },
): Promise<WorkOpened> {
  const store = await openWorkStore({
    appId: options.appId,
    backend,
    ...(options.now ? { now: options.now } : {}),
    ...(options.newKey ? { newKey: options.newKey } : {}),
  });
  // A second tab reads an empty cache: it must never stand in for the board.
  if (store.ownership() !== "owner") return { layer: null, readOnly: true };
  return {
    readOnly: false,
    layer: {
      store,
      sql: backend.sql,
      sync: new WorkSync({
        store,
        transport: options.transport,
        ...(options.onChange ? { onChange: options.onChange } : {}),
        ...(options.onPulled ? { onPulled: options.onPulled } : {}),
      }),
    },
  };
}

/**
 * Open the layer over the store's wa-sqlite OPFS backend (the browser copies
 * and the PWA). A host that cannot carry a store at all (no Web Locks, no
 * worker, no secure context) answers no layer: the app then behaves online as
 * today and the strip keeps quiet, rather than the glass pretending to hold a
 * durable store it does not have.
 */
export async function openWorkOfflineOpfs(options: {
  appId: string;
  dbName: string;
  transport: SyncTransport;
  /** The app builds the worker itself (`new Worker(new URL(...), { type: "module" })`), so its bundler sees and bundles the entry. */
  createWorker: () => WorkerLike;
  onChange?: () => void;
  onPulled?: () => void;
}): Promise<WorkOpened> {
  const locks = (globalThis as { navigator?: { locks?: unknown } }).navigator?.locks;
  if (typeof Worker === "undefined" || !locks || typeof globalThis.crypto?.subtle !== "object") {
    return { layer: null, readOnly: false };
  }
  try {
    const backend: StorageBackend = await createOpfsBackend({
      dbName: options.dbName,
      locks: locks as Parameters<typeof createOpfsBackend>[0]["locks"],
      createWorker: options.createWorker,
    });
    return await openWorkOfflineOver(backend, options);
  } catch {
    return { layer: null, readOnly: false };
  }
}
