// The Counter Till's offline layer: READ CACHE ONLY. The till has no outbox
// (plan D5, the manifest's read_cache_only block): it pulls the catalogue, the
// customers, the price band, the parked tickets and the invoices into the
// device's copy, and answers the catalogue and the parked tickets from it, with
// the copy's age shown, when the network does not answer (customers are not
// answered: see tillRoutes). It carries no keeper and queues nothing:
// every write the till makes (the ring, every tender, a refund, a payment, a
// park, a resume) is refused with no signal by the guard its client is built
// on, and a refused write is never stored or retried by anything but the
// person pressing it again with the signal back (the ring's idempotency key and
// its held key behaviour are the terminal's own and are untouched).
import {
  openWorkOfflineOpfs,
  parkedRoute,
  productRoutes,
  WorkDesk,
  type CachedRoute,
  type SignalSource,
  type WorkOpenOptions,
  type WorkOpened,
  type WorkReader,
} from "@hardscapehouse/work-offline";
import { apiClientTransport, type SyncTransport, type WorkerLike } from "@hardscapehouse/offline-store";

export { apiClientTransport };

/** The store's database name for one person: theirs alone, and safe as an OPFS file name. */
export function dbNameFor(userId: string): string {
  return `hh-counter-till-${userId.replace(/[^A-Za-z0-9_-]/g, "_")}`;
}

/**
 * The till's cached reads: the catalogue and the branch's parked tickets. The customer type-ahead is NOT
 * answered from the copy: the copy holds no terms, credit or balance, the till's picker and card render
 * those as numbers, and an unknown figure must never read as zero (review of item 8b, P2-1). The till
 * cannot ring offline, so a customer search with no signal is the plain no signal refusal.
 */
export function tillRoutes(reader: () => WorkReader | null): CachedRoute[] {
  return [...productRoutes(reader), parkedRoute(reader)];
}

export function createTillDesk(options: {
  signal: SignalSource;
  transport: SyncTransport;
  dbName: string;
  open?: (options: WorkOpenOptions) => Promise<WorkOpened>;
}): WorkDesk {
  return new WorkDesk({
    appId: "counter-till",
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
  });
}
