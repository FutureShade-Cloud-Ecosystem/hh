// The payment keys the desk holds until the payment route has answered them.
//
// A key is minted at an account's first Record payment press and sent again on every retry. It ends only on
// the route's own answer, so it has to outlive a sign out, a sign in and a page reload: it rides the auth
// package's throw safe session mirror (the tab's own session storage, memory only when that is blocked),
// one record per signed in user, one entry per account. A write that fails at run time (quota, or storage
// that passed the probe and later throws) keeps the record in memory for the page's life, so a failed write
// never leaves the account with no key held. A key is a random label, never a credential. A different user
// reads a different record and never sees these, and these are left untouched while the other user is
// signed in.
import { ThrowSafeMirror, type StorageLike } from "@hardscapehouse/auth";

/**
 * A fresh random key. randomUUID needs a secure context, so on a plain http origin that is not localhost the
 * key is a version 4 UUID built from getRandomValues, which every context has; the time and Math.random form is
 * only the last resort when no crypto object exists at all.
 */
export function newKey(): string {
  const c = globalThis.crypto;
  if (c !== undefined && typeof c.randomUUID === "function") return c.randomUUID();
  if (c !== undefined && typeof c.getRandomValues === "function") {
    const b = c.getRandomValues(new Uint8Array(16));
    b[6] = (b[6]! & 0x0f) | 0x40;
    b[8] = (b[8]! & 0x3f) | 0x80;
    const hex = Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
    return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
  }
  return `k-${Date.now()}-${Math.floor(Math.random() * 1e9)}`;
}

const KEY_PREFIX = "hh-money-desk.held-payment-keys.v1.";

/** The records whose storage write failed, kept for the page's life: one map per user id, read ahead of
 * storage, stood down the moment a write lands (storage then carries the whole record). */
const unwritten = new Map<string, Map<string, string>>();

export class HeldKeys {
  readonly #mirror: ThrowSafeMirror | null;
  readonly #userId: string;
  readonly #memory = new Map<string, string>();

  /** A user id of "" (no session) holds in memory only: it must never share a record between users. */
  constructor(userId: string, storage?: StorageLike) {
    this.#userId = userId;
    this.#mirror = userId === "" ? null : new ThrowSafeMirror(`${KEY_PREFIX}${userId}`, storage);
  }

  get(accountId: string): string | undefined {
    return this.#read().get(accountId);
  }

  set(accountId: string, key: string): void {
    const all = this.#read();
    all.set(accountId, key);
    this.#write(all);
  }

  delete(accountId: string): void {
    const all = this.#read();
    if (all.delete(accountId)) this.#write(all);
  }

  #read(): Map<string, string> {
    if (this.#mirror === null) return new Map(this.#memory);
    const out = new Map<string, string>();
    const raw = this.#mirror.read();
    if (raw !== null && typeof raw === "object" && !Array.isArray(raw)) {
      const keys = (raw as { keys?: unknown }).keys;
      if (keys !== null && typeof keys === "object" && !Array.isArray(keys)) {
        for (const [account, key] of Object.entries(keys as Record<string, unknown>)) {
          if (typeof key === "string" && key !== "") out.set(account, key);
        }
      }
    }
    const held = unwritten.get(this.#userId);
    if (held !== undefined) for (const [account, key] of held) out.set(account, key);
    return out;
  }

  #write(all: Map<string, string>): void {
    if (this.#mirror === null) {
      this.#memory.clear();
      for (const [account, key] of all) this.#memory.set(account, key);
      return;
    }
    if (all.size === 0) {
      this.#mirror.clear();
      unwritten.delete(this.#userId);
      return;
    }
    if (this.#mirror.write({ v: 1, keys: Object.fromEntries(all) })) {
      unwritten.delete(this.#userId);
      return;
    }
    unwritten.set(this.#userId, new Map(all));
  }
}
