// The Quote Desk's offline controller: the Work shell's layer for this one
// app, on the shared desk controller (work-offline's WorkDesk), plus the two
// writes the core admits through sync on a quote. It opens the offline store
// for the signed in person once the desk is up, keeps a new quote header or
// an expiry date when there is no signal, drives the sync cycle, and tells the
// panel what to say: the banner's mode, the copy's age, the drafts waiting,
// the cards the server's answers left. Everything that moves money stays
// online only: the desk's client is built on the no signal guard, and this
// controller is offered nothing but those two writes.
//
// The store belongs to the person: its database is named for them, so a
// second person signing in on the same device never replays the first
// person's work under their own grant.
import {
  cardWords,
  openWorkOfflineOpfs,
  QuoteDrafts,
  WorkDesk,
  type CardWords,
  type ReadThroughHooks,
  type SignalSource,
  type WaitingDraft,
  type WorkOpenOptions,
  type WorkOpened,
  type WorkReader,
} from "@hardscapehouse/work-offline";
import { apiClientTransport, type Card, type OfflineStore, type SyncTransport, type WorkerLike } from "@hardscapehouse/offline-store";
import { err, type Result } from "@hardscapehouse/api-client";
import type { QdClient } from "./qd-client.js";

export { apiClientTransport };

/** The store's database name for one person: theirs alone, and safe as an OPFS file name. */
export function dbNameFor(userId: string): string {
  return `hh-quote-desk-${userId.replace(/[^A-Za-z0-9_-]/g, "_")}`;
}

export interface DeskCard extends Card {
  words: CardWords;
}

export interface DeskOfflineOptions {
  signal: SignalSource;
  qd: QdClient;
  transport: SyncTransport;
  dbName: string;
  /** Opens the layer. Defaults to the store over its wa-sqlite OPFS backend; a test opens it over node:sqlite. */
  open?: (options: WorkOpenOptions) => Promise<WorkOpened>;
  newId?: () => string;
}

export class DeskOffline {
  readonly #desk: WorkDesk;
  #drafts: QuoteDrafts | null = null;

  constructor(options: DeskOfflineOptions) {
    const qd = options.qd;
    this.#desk = new WorkDesk({
      appId: "quote-desk",
      signal: options.signal,
      transport: options.transport,
      dbName: options.dbName,
      open:
        options.open ??
        ((o) =>
          openWorkOfflineOpfs({
            ...o,
            // The literal form lets the bundler see the worker entry and bundle it.
            createWorker: () => new Worker(new URL("../../../packages/offline-store/src/opfs-worker.ts", import.meta.url), { type: "module" }) as unknown as WorkerLike,
          })),
      onPulled: () => this.#drafts?.markPulled(),
      onOpened: (layer) => {
        const drafts = new QuoteDrafts({
          store: layer.store,
          sql: layer.sql,
          branchId: this.#desk.branchId,
          ...(options.newId ? { newId: options.newId } : {}),
        });
        this.#drafts = drafts;
        qd.setDraftKeeper({
          keep: async (customerId, validUntil) => {
            const kept = await drafts.createDraft({ customerId, validUntil });
            this.#desk.changed();
            if (kept.ok) layer.sync.requestSync();
            return kept;
          },
          keepValidUntil: async (quoteId, validUntil) => {
            const kept = await drafts.setValidUntil(quoteId, validUntil);
            this.#desk.changed();
            if (kept.ok) layer.sync.requestSync();
            return kept;
          },
        });
      },
      onClosing: () => {
        qd.setDraftKeeper(null);
        this.#drafts = null;
      },
    });
  }

  /** Fed to the read through fetch: the panel hears whether the last read was the device's copy. */
  get hooks(): ReadThroughHooks {
    return this.#desk.hooks;
  }

  get available(): boolean {
    return this.#desk.available;
  }

  reader(): WorkReader | null {
    return this.#desk.reader();
  }

  layerStore(): OfflineStore {
    return this.#desk.store();
  }

  subscribe(listener: () => void): () => void {
    return this.#desk.subscribe(listener);
  }

  /** Open the layer and run the first cycle to rest (the pull is the network's to take). */
  async boot(branchId: string): Promise<void> {
    await this.open(branchId);
    await this.cycle();
  }

  /** Open the layer for the signed in person and hand the client its keeper. The store opens from the device, so this does not wait on the network. */
  open(branchId: string): Promise<void> {
    return this.#desk.open(branchId);
  }

  setOnline(online: boolean): Promise<void> {
    return this.#desk.setOnline(online);
  }

  cycle(): Promise<void> {
    return this.#desk.cycle();
  }

  bannerMode(): string {
    return this.#desk.bannerMode();
  }

  cachedWords(): string {
    return this.#desk.cachedWords();
  }

  online(): boolean {
    return this.#desk.online();
  }

  waitingCount(): Promise<number> {
    return this.#desk.waitingCount();
  }

  async waiting(): Promise<WaitingDraft[]> {
    return this.#drafts === null ? [] : this.#drafts.waiting();
  }

  async cards(): Promise<DeskCard[]> {
    if (!this.#desk.available) return [];
    return (await this.#desk.layer().sync.openCards()).map((card) => ({ ...card, words: cardWords(card) }));
  }

  async dismiss(cardId: string): Promise<void> {
    if (!this.#desk.available) return;
    await this.#desk.layer().sync.dismissCard(cardId);
    this.#desk.changed();
  }

  async withdraw(quoteId: string): Promise<Result<unknown>> {
    if (this.#drafts === null || !this.#desk.available) return err("The offline copy is not open.");
    const gone = await this.#drafts.withdraw(quoteId);
    this.#desk.changed();
    if (gone.ok) this.#desk.layer().sync.requestSync();
    return gone;
  }

  /** Sign out: the keeper goes, the connectivity watch stops, the store is released for the next sign in. */
  close(): Promise<void> {
    return this.#desk.close();
  }
}
