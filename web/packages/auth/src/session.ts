// Session custody for @hh/auth: the JWT lives in memory only, and NO
// credential of any kind is ever written to any browser storage,
// sessionStorage included. The throw-safe mirror carries only the display
// profile (user id, email, name): what a reload may show while it signs in
// again through the issuer. There is no refresh handle, there is no session
// secret, and there is nothing a reload can trade for a session. After a
// reload the shell signs in again through the issuer, whose own session is
// an httpOnly cookie script cannot read; the package exposes that seam
// (AccountSurface.getSession, AuthController.resume) and holds nothing to
// trade.
//
// Provenance, the throw-safe mirror: the ERP's storage shim in
// hardscapeos_dibbits/app/src/api.ts (READ ONLY reference tree), which
// carries the recorded rationale (finding DIB-488 rev 5): the ERP's
// module-scope storage reads ran at import time, and browsers set to deny
// site data make the storage ACCESSOR ITSELF throw (SecurityError)
// before any component mounted, so the bundle died and the page rendered
// nothing. The shim degrades to an in-memory map: login still works and the
// session simply does not survive a reload, which is the honest behaviour
// for that posture. Some browsers throw on ACCESS of the accessor, others on
// USE, so the shim probes a read and a write before trusting it. This
// package carries the same rationale and the same probe discipline, and adds
// what the ERP shim did not have: the degradation is a flag the shell can
// read, every write is itself throw-safe (quota and serialisation failures
// are never fatal), and the mirrored payload never carries anything
// credential-shaped.
//
// What a page reload may restore, stated plainly (the README restates it):
// the mirror holds the display profile (user id, email, name) and nothing
// else. It does NOT hold the JWT, a refresh handle, a session secret, or any
// other bearer-capable value. In degraded mode it holds nothing that
// survives a reload. A signed-out custody (clear) holds nothing either.

/** The three storage operations the mirror needs, so tests can inject doubles. */
export interface StorageLike {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

const PROBE_KEY = "hh-auth.storage-probe";

function memoryStorage(): StorageLike {
  const mem = new Map<string, string>();
  return {
    getItem: (key) => (mem.has(key) ? (mem.get(key) as string) : null),
    setItem: (key, value) => {
      mem.set(key, value);
    },
    removeItem: (key) => {
      mem.delete(key);
    },
  };
}

// Some browsers throw on ACCESS of the storage object, others on use, so the
// probe touches all three operations before the accessor is trusted (the ERP
// shim's discipline, one operation wider).
function probe(accessor: StorageLike): boolean {
  try {
    accessor.getItem(PROBE_KEY);
    accessor.setItem(PROBE_KEY, "1");
    accessor.removeItem(PROBE_KEY);
    return true;
  } catch {
    return false;
  }
}

/**
 * The throw-safe mirror: browser-session-backed where the accessor works,
 * memory-only when it does not, never a persistent surface. Reads and writes never
 * throw; write reports whether the value actually stuck, so a caller can
 * tell "mirrored" from "memory only" without a second read.
 */
export class ThrowSafeMirror {
  readonly key: string;
  /** True when the mirror could not attach to a working storage accessor. */
  readonly degraded: boolean;
  readonly #backing: StorageLike;

  constructor(key: string, storage?: StorageLike) {
    this.key = key;
    let chosen: StorageLike = memoryStorage();
    let degraded = true;
    if (storage !== undefined) {
      if (probe(storage)) {
        chosen = storage;
        degraded = false;
      }
    } else {
      // Resolve the platform's browser-session storage. The whole resolution
      // sits in the try because hostile contexts throw on the property access
      // itself, which is exactly the ERP's recorded finding. Node (the test
      // process) has no such accessor and degrades, which is the same
      // posture.
      try {
        const candidate = (globalThis as { sessionStorage?: StorageLike }).sessionStorage;
        if (candidate !== undefined && probe(candidate)) {
          chosen = candidate;
          degraded = false;
        }
      } catch {
        // denied site data: keep the memory backing, degraded stays true
      }
    }
    this.#backing = chosen;
    this.degraded = degraded;
  }

  /** Store one payload. Returns false when serialisation or the write failed (quota, blocked storage): the value then lives in memory only. */
  write(value: unknown): boolean {
    let raw: string;
    try {
      raw = JSON.stringify(value);
    } catch {
      return false;
    }
    try {
      this.#backing.setItem(this.key, raw);
      return true;
    } catch {
      return false;
    }
  }

  /** Read the stored payload, or undefined when absent, corrupt, or unreadable. Never throws; callers validate the shape (cast nothing). */
  read(): unknown {
    try {
      const raw = this.#backing.getItem(this.key);
      if (raw === null || typeof raw !== "string") return undefined;
      return JSON.parse(raw) as unknown;
    } catch {
      return undefined;
    }
  }

  /** Drop the stored payload. Never throws; nothing to restore either way. */
  clear(): void {
    try {
      this.#backing.removeItem(this.key);
    } catch {
      // the payload was never written, or the accessor is gone
    }
  }
}

export interface SessionUser {
  userId: string;
  email: string;
  name: string;
}

/** The live session. The token rides private memory and the caller that presents it; nothing here is persisted. */
export interface Session {
  token: string;
  tokenExpiresAtMs: number;
  user: SessionUser;
}

export const SESSION_MIRROR_KEY = "hh-auth.session.v2";

interface StoredMirrorPayload {
  v: 2;
  userId: string;
  email: string;
  name: string;
}

// Validated rather than cast (the ERP readRememberedLanding lesson): a
// hand-edited or corrupt storage entry reads back as absent, never as a
// profile with fields it does not carry. Payload version 2 carries the
// display profile only. Version 1 carried a refresh handle; this package was
// reworked so that no credential of any kind is ever stored, and a v1
// payload is refused like any other unusable input and dropped from storage.
function parseStoredPayload(raw: unknown): StoredMirrorPayload | null {
  if (raw === null || typeof raw !== "object" || Array.isArray(raw)) return null;
  const candidate = raw as Record<string, unknown>;
  if (candidate["v"] !== 2) return null;
  if (typeof candidate["userId"] !== "string" || candidate["userId"] === "") return null;
  if (typeof candidate["email"] !== "string" || typeof candidate["name"] !== "string") return null;
  return {
    v: 2,
    userId: candidate["userId"],
    email: candidate["email"],
    name: candidate["name"],
  };
}

function validateUser(user: SessionUser): SessionUser | null {
  if (user === null || typeof user !== "object") return null;
  if (typeof user.userId !== "string" || user.userId === "") return null;
  if (typeof user.email !== "string" || typeof user.name !== "string") return null;
  return { userId: user.userId, email: user.email, name: user.name };
}

/**
 * The custody. adopt() takes a full session handed over from out of band (a
 * sign-in answer, a Google callback through the Console bridge, a reload's
 * resume through the issuer) exactly as the ERP's adoptSession did: the
 * token lives in private memory, the mirror receives the display profile,
 * never the token and never any other credential. adopt returns false and
 * adopts nothing when the input is unusable (an empty token, an already
 * expired token, a user without an id), mirroring the ERP's boolean
 * contract.
 */
export class SessionStore {
  readonly #mirror: ThrowSafeMirror;
  readonly #now: () => number;
  #token: string | null = null;
  #tokenExpiresAtMs = 0;
  #user: SessionUser | null = null;
  #profile: SessionUser | null = null;

  constructor(options?: { mirror?: ThrowSafeMirror; now?: () => number }) {
    this.#mirror = options?.mirror ?? new ThrowSafeMirror(SESSION_MIRROR_KEY);
    this.#now = options?.now ?? Date.now;
    const restored = parseStoredPayload(this.#mirror.read());
    if (restored !== null) {
      this.#profile = { userId: restored.userId, email: restored.email, name: restored.name };
    } else {
      // A payload we cannot validate is a payload we do not keep: it is
      // dropped from storage, not merely hidden from view, so nothing
      // credential-shaped survives in storage under this key.
      this.#mirror.clear();
    }
  }

  /** The display profile, or null. What a reload may show while it signs in again through the issuer; never a credential. */
  get profile(): SessionUser | null {
    return this.#profile === null ? null : { ...this.#profile };
  }

  /** True when the mirror is degraded (blocked or absent storage): a reload restores nothing. */
  get degraded(): boolean {
    return this.#mirror.degraded;
  }

  /**
   * Adopt a session: the token and its expiry into private memory, the
   * display profile into the mirror. Returns false and changes nothing when
   * the input is unusable.
   */
  adopt(input: { token: string; tokenExpiresAtMs: number; user: SessionUser }): boolean {
    if (typeof input.token !== "string" || input.token === "") return false;
    if (typeof input.tokenExpiresAtMs !== "number" || !Number.isFinite(input.tokenExpiresAtMs)) return false;
    if (input.tokenExpiresAtMs <= this.#now()) return false;
    const user = validateUser(input.user);
    if (user === null) return false;
    this.#token = input.token;
    this.#tokenExpiresAtMs = input.tokenExpiresAtMs;
    this.#user = user;
    this.#profile = { ...user };
    this.#mirror.write({ v: 2, userId: user.userId, email: user.email, name: user.name });
    return true;
  }

  /** The live session, or null when there is none or the token has expired. */
  get current(): Session | null {
    if (this.#token === null || this.#user === null) return null;
    if (this.#now() >= this.#tokenExpiresAtMs) return null;
    return { token: this.#token, tokenExpiresAtMs: this.#tokenExpiresAtMs, user: { ...this.#user } };
  }

  /** Wipe custody entirely: memory and mirror. Called on sign-out. */
  clear(): void {
    this.#token = null;
    this.#tokenExpiresAtMs = 0;
    this.#user = null;
    this.#profile = null;
    this.#mirror.clear();
  }

  /**
   * Serialises to nothing secret: the degradation flag, whether a session is
   * live, and the display profile the mirror holds. Never the token. A
   * logger or error reporter that stringifies the store prints the profile a
   * page already renders, and nothing else.
   */
  toJSON(): { degraded: boolean; signedIn: boolean; profile: SessionUser | null } {
    return {
      degraded: this.#mirror.degraded,
      signedIn: this.current !== null,
      profile: this.profile,
    };
  }
}
