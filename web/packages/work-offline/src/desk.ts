// The generic controller of a desk app's offline layer, the part every desk
// app shares: open the person's store (their own database, so no second
// person ever replays the first person's work), run the sync cycles, follow
// the connection, and tell the app's panel what to say (the banner's mode,
// the copy's age, how much waits). An app that carries an outbox adds its own
// keeper in `onOpened`; a read cache only app (the Counter Till) adds nothing,
// and there is nothing here that could queue a write for it.
import type { OfflineStore, SyncTransport } from "@hardscapehouse/offline-store";
import type { ReadThroughHooks } from "./read-through.js";
import type { SignalSource } from "./signal.js";
import { bannerMode as modeFor, type WorkOffline, type WorkOpened } from "./sync.js";
import { WorkReader } from "./read-cache.js";

export interface WorkOpenOptions {
  appId: string;
  dbName: string;
  transport: SyncTransport;
  onChange: () => void;
  onPulled: () => void;
}

export interface WorkDeskOptions {
  appId: string;
  signal: SignalSource;
  transport: SyncTransport;
  /** The store's database name for the signed in person. */
  dbName: string;
  /** Opens the layer. The app supplies it: the store over its wa-sqlite OPFS backend, with its own `new Worker(new URL(...))` so its bundler sees the entry. */
  open: (options: WorkOpenOptions) => Promise<WorkOpened>;
  /** The layer just opened: an app with an outbox hands its client the keeper here. */
  onOpened?: (layer: WorkOffline, reader: WorkReader) => void;
  /** The layer is about to close: take the keeper away here. */
  onClosing?: () => void;
  /** A pull landed with the cycle at rest. */
  onPulled?: () => void;
}

export class WorkDesk {
  /** Fed to the read through fetch: the panel hears whether the last read was the device's copy. */
  readonly hooks: ReadThroughHooks;
  readonly #options: WorkDeskOptions;
  readonly #signal: SignalSource;
  readonly #listeners = new Set<() => void>();
  #layer: WorkOffline | null = null;
  #reader: WorkReader | null = null;
  #readOnly = false;
  #branchId = "";
  #cached = "";
  #watch: ReturnType<typeof setInterval> | null = null;

  constructor(options: WorkDeskOptions) {
    this.#options = options;
    this.#signal = options.signal;
    this.hooks = {
      onCached: (words) => {
        if (this.#cached === words) return;
        this.#cached = words;
        this.changed();
      },
      onLive: () => {
        if (this.#cached === "") return;
        this.#cached = "";
        this.changed();
      },
    };
  }

  get available(): boolean {
    return this.#layer !== null;
  }

  get branchId(): string {
    return this.#branchId;
  }

  reader(): WorkReader | null {
    return this.#reader;
  }

  /** The open layer; a defect to ask for it when there is none. */
  layer(): WorkOffline {
    if (this.#layer === null) throw new Error("the desk's offline layer is not open");
    return this.#layer;
  }

  store(): OfflineStore {
    return this.layer().store;
  }

  subscribe(listener: () => void): () => void {
    this.#listeners.add(listener);
    return () => void this.#listeners.delete(listener);
  }

  changed(): void {
    for (const listener of [...this.#listeners]) listener();
  }

  /** Open the layer and follow the connection. The store opens from the device, so this does not wait on the network. */
  async open(branchId: string): Promise<void> {
    if (this.#layer !== null) return;
    this.#branchId = branchId;
    const opened = await this.#options.open({
      appId: this.#options.appId,
      dbName: this.#options.dbName,
      transport: this.#options.transport,
      onChange: () => this.changed(),
      onPulled: () => this.#options.onPulled?.(),
    });
    this.#readOnly = opened.readOnly;
    const layer = opened.layer;
    if (layer === null) {
      this.changed();
      return;
    }
    this.#layer = layer;
    this.#reader = new WorkReader(layer.store, this.#options.appId);
    this.#options.onOpened?.(layer, this.#reader);
    layer.sync.runner.onState(() => this.changed());
    this.#startWatch();
    await layer.sync.setOnline(this.#signal.online());
    this.changed();
  }

  #startWatch(): void {
    if (typeof window === "undefined") return;
    window.addEventListener("online", this.#onConnectivity);
    window.addEventListener("offline", this.#onConnectivity);
    // Some hosts deliver the platform's word late or not at all; the layer dispatches only on a change.
    this.#watch = setInterval(this.#onConnectivity, 1_000);
  }

  readonly #onConnectivity = (): void => {
    if (this.#layer === null) return;
    void this.#layer.sync.setOnline(this.#signal.online()).then(() => {
      if (this.#signal.online()) return this.#layer?.sync.cycle();
    });
  };

  setOnline(online: boolean): Promise<void> {
    return this.#layer === null ? Promise.resolve() : this.#layer.sync.setOnline(online);
  }

  /** One push-then-pull cycle to rest. */
  async cycle(): Promise<void> {
    if (this.#layer !== null) await this.#layer.sync.cycle();
  }

  /** The strip's mode: a second tab says read only whether or not a layer opened; otherwise the runner's state. */
  bannerMode(): string {
    if (this.#layer === null) return this.#readOnly ? "read-only" : "";
    return modeFor(this.#layer.sync.runner.state, this.#layer.store.ownership());
  }

  cachedWords(): string {
    return this.#cached;
  }

  /** Whether the device has a signal now: a copy still on screen with the signal back says to refresh. */
  online(): boolean {
    return this.#signal.online();
  }

  async waitingCount(): Promise<number> {
    return this.#layer === null ? 0 : this.#layer.sync.waiting();
  }

  /** Sign out: the app's keeper goes, the connectivity watch stops, the store is released for the next sign in. */
  async close(): Promise<void> {
    this.#options.onClosing?.();
    if (this.#watch !== null) clearInterval(this.#watch);
    this.#watch = null;
    if (typeof window !== "undefined") {
      window.removeEventListener("online", this.#onConnectivity);
      window.removeEventListener("offline", this.#onConnectivity);
    }
    const layer = this.#layer;
    this.#layer = null;
    this.#reader = null;
    this.#cached = "";
    this.#readOnly = false;
    if (layer !== null) {
      layer.sync.runner.stop();
      await layer.store.close();
    }
    this.changed();
  }
}
