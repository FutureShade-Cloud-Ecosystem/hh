// The payment keys the desk holds until the payment route has answered them.
//
// A key is minted at an account's first Record payment press and sent again on every retry. It ends only on
// the route's own answer, so it has to outlive a sign out, a sign in and a page reload: it rides the auth
// package's throw safe session mirror (the tab's own session storage, memory only when that is blocked),
// one record per signed in user, one entry per account. A key is a random label, never a credential. A
// different user reads a different record and never sees these, and these are left untouched while the
// other user is signed in.
import { ThrowSafeMirror, type StorageLike } from "@hardscapehouse/auth";

const KEY_PREFIX = "hh-money-desk.held-payment-keys.v1.";

export class HeldKeys {
  readonly #mirror: ThrowSafeMirror | null;
  readonly #memory = new Map<string, string>();

  /** A user id of "" (no session) holds in memory only: it must never share a record between users. */
  constructor(userId: string, storage?: StorageLike) {
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
    const raw = this.#mirror.read();
    const out = new Map<string, string>();
    if (raw === null || typeof raw !== "object" || Array.isArray(raw)) return out;
    const keys = (raw as { keys?: unknown }).keys;
    if (keys === null || typeof keys !== "object" || Array.isArray(keys)) return out;
    for (const [account, key] of Object.entries(keys as Record<string, unknown>)) {
      if (typeof key === "string" && key !== "") out.set(account, key);
    }
    return out;
  }

  #write(all: Map<string, string>): void {
    if (this.#mirror === null) {
      this.#memory.clear();
      for (const [account, key] of all) this.#memory.set(account, key);
      return;
    }
    if (all.size === 0) this.#mirror.clear();
    else this.#mirror.write({ v: 1, keys: Object.fromEntries(all) });
  }
}
