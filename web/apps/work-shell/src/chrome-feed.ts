// What the chrome is fed. The sync chip and the offline banner follow the
// connection and, when a store is open, the sync runner's state and the
// outbox's health. The host opens no store today (the Work store wiring is a
// later item), so the chip reads idle online and Offline when the connection
// is gone; a store, when handed in, drives the rest. Offline outranks a
// store's own idle: the person is told the truth about the connection.
import {
  memoryPinStorage,
  type PinStorage,
  type ShellSyncState,
  type SyncChipInput,
} from "@hardscapehouse/shell-chrome/src/shell-state.js";

/** The open store's view: the runner's state and the outbox health. */
export type StoreFeed = SyncChipInput;

export function chipInputFor(input: { online: boolean; store?: StoreFeed | undefined }): SyncChipInput {
  const store = input.store;
  const state: ShellSyncState = input.online ? (store?.state ?? "idle") : "offline";
  return {
    state,
    openCards: store?.openCards ?? 0,
    waiting: store?.waiting ?? 0,
    attempts: store?.attempts ?? 0,
    stalls: store?.stalls ?? 0,
  };
}

/** The banner under the band: the offline words when the connection is gone, nothing otherwise. */
export function bannerModeFor(input: { online: boolean }): "" | "offline" {
  return input.online ? "" : "offline";
}

/**
 * What the banner element is fed. With no outbox handed in (the host opens no
 * store, and the desk apps are read cache or online only: money never queues)
 * the banner runs the chrome's cacheOnly words, which say what the screen
 * shows and never that writes wait or send later. An outbox, when a store is
 * open, brings its depth and the outbox words back.
 */
export function bannerFor(input: { online: boolean; outbox?: { waiting: number } | undefined }): {
  mode: "" | "offline";
  cacheOnly: boolean;
  waiting: number;
} {
  const mode = bannerModeFor(input);
  if (mode === "") return { mode, cacheOnly: false, waiting: 0 };
  return input.outbox === undefined
    ? { mode, cacheOnly: true, waiting: 0 }
    : { mode, cacheOnly: false, waiting: input.outbox.waiting };
}

interface StorageLike {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

/**
 * The pins' storage seam over the platform's persistent storage. It is probed
 * once; a storage that throws on any use (denied site data) is not trusted
 * and the seam is memory, so the chrome never throws and the person's pins
 * live for this document only.
 */
export function pinStorageFrom(candidate: StorageLike | undefined): PinStorage {
  if (candidate !== undefined) {
    try {
      const probe = "hh-work-shell.storage-probe";
      candidate.getItem(probe);
      candidate.setItem(probe, "1");
      candidate.removeItem(probe);
      return {
        read: (key) => candidate.getItem(key),
        write: (key, value) => candidate.setItem(key, value),
      };
    } catch {
      // fall through to memory
    }
  }
  return memoryPinStorage();
}
