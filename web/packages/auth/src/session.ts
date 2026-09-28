// Session custody for @hh/auth: the JWT lives in memory only. A throw-safe
// mirror carries, on sessionStorage, only what a page reload may restore: a
// short-lived refresh handle plus the profile it belongs to, never the token
// itself. When the storage accessor is broken or denied, the mirror degrades
// to a memory map and the degradation is observable (mirror.degraded), so a
// shell can tell "a reload keeps the session" from "a reload restores
// nothing".
//
// Provenance, the throw-safe mirror: the ERP's storage shim in
// hardscapeos_dibbits/app/src/api.ts (READ ONLY reference tree at
// /home/colton/Desktop/FBHQ/session-2/hh-4/hardscapeos_dibbits), which
// carries the recorded rationale (finding DIB-488 rev 5): the ERP's
// module-scope storage reads ran at import time, and browsers set to deny
// site data make the sessionStorage ACCESSOR ITSELF throw (SecurityError)
// before any component mounted, so the bundle died and the page rendered
// nothing. The shim degrades to an in-memory map: login still works and the
// session simply does not survive a reload, which is the honest behaviour
// for that posture. Some browsers throw on ACCESS of the accessor, others on
// USE, so the shim probes a read and a write before trusting it. This
// package carries the same rationale and the same probe discipline, and adds
// what the ERP shim did not have: the degradation is a flag the shell can
// read, every write is itself throw-safe (quota and serialisation failures
// are never fatal), and the mirrored payload never carries the token.
//
// What a page reload may restore, stated plainly (the README restates it):
// the mirror holds the short-lived refresh handle (an opaque, adapter-defined
// value a consuming shell trades for a fresh session through the platform
// adapter) and the display profile (user id, email, name) with the handle's
// expiry. It does NOT hold the JWT. In degraded mode it holds nothing that
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
 * The throw-safe mirror: sessionStorage-backed where the accessor works,
 * memory-only when it does not, never a persistent surface. Reads and writes never
 * throw; write reports whether the value actually stuck, so a caller can
 * tell "mirrored" from "memory only" without a second read.
 */
export class ThrowSafeMirror {
  readonly key: string;
  /** True when the mirror could not attach to a working storage accessor. */
  readonly degraded: boolean;
  private readonly backing: StorageLike;

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
      // Resolve the platform's sessionStorage. The whole resolution sits in
      // the try because hostile contexts throw on the property access itself,
      // which is exactly the ERP's recorded finding. Node (the test process)
      // has no sessionStorage and degrades, which is the same posture.
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
    this.backing = chosen;
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
      this.backing.setItem(this.key, raw);
      return true;
    } catch {
      return false;
    }
  }

  /** Read the stored payload, or undefined when absent, corrupt, or unreadable. Never throws; callers validate the shape (cast nothing). */
  read(): unknown {
    try {
      const raw = this.backing.getItem(this.key);
      if (raw === null || typeof raw !== "string") return undefined;
      return JSON.parse(raw) as unknown;
    } catch {
      return undefined;
    }
  }

  /** Drop the stored payload. Never throws; nothing to restore either way. */
  clear(): void {
    try {
      this.backing.removeItem(this.key);
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

/** The live session. The token rides memory and the caller that presents it; nothing here is persisted. */
export interface Session {
  token: string;
  tokenExpiresAtMs: number;
  user: SessionUser;
}

/** The short-lived, adapter-defined handle a reload may trade for a fresh session (the Appwrite session secret is the natural fit). */
export interface RefreshHandle {
  handle: string;
  expiresAtMs: number;
}

/** What a page reload may restore: the refresh handle and the profile it belongs to. Never a token. */
export interface RestorableSession {
  handle: string;
  expiresAtMs: number;
  user: SessionUser;
}

interface StoredMirrorPayload {
  v: 1;
  handle: string;
  handleExpiresAtMs: number;
  userId: string;
  email: string;
  name: string;
}

export const SESSION_MIRROR_KEY = "hh-auth.session.v1";

// Validated rather than cast (the ERP readRememberedLanding lesson): a
// hand-edited or corrupt storage entry reads back as absent, never as a
// session with fields it does not carry.
function parseStoredPayload(raw: unknown, nowMs: number): StoredMirrorPayload | null {
  if (raw === null || typeof raw !== "object" || Array.isArray(raw)) return null;
  const candidate = raw as Record<string, unknown>;
  if (candidate["v"] !== 1) return null;
  if (typeof candidate["handle"] !== "string" || candidate["handle"] === "") return null;
  if (typeof candidate["handleExpiresAtMs"] !== "number" || !Number.isFinite(candidate["handleExpiresAtMs"])) return null;
  if (typeof candidate["userId"] !== "string" || candidate["userId"] === "") return null;
  if (typeof candidate["email"] !== "string" || typeof candidate["name"] !== "string") return null;
  if (candidate["handleExpiresAtMs"] <= nowMs) return null; // an expired handle is dropped, never surfaced
  return {
    v: 1,
    handle: candidate["handle"],
    handleExpiresAtMs: candidate["handleExpiresAtMs"],
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
 * restored platform session) exactly as the ERP's adoptSession did: the
 * token lives in module memory, the mirror receives the refresh handle and
 * the profile, never the token. adopt returns false and adopts nothing when
 * the input is unusable (an empty token, an already expired token, a user
 * without an id), mirroring the ERP's boolean contract.
 */
export class SessionStore {
  /** Exposed for diagnostics and tests; production code goes through the custody API. */
  readonly mirror: ThrowSafeMirror;
  private readonly now: () => number;
  private token: string | null = null;
  private tokenExpiresAtMs = 0;
  private user: SessionUser | null = null;
  private handle: RefreshHandle | null = null;
  private handleUser: SessionUser | null = null;

  constructor(options?: { mirror?: ThrowSafeMirror; now?: () => number }) {
    this.mirror = options?.mirror ?? new ThrowSafeMirror(SESSION_MIRROR_KEY);
    this.now = options?.now ?? Date.now;
    const restored = parseStoredPayload(this.mirror.read(), this.now());
    if (restored !== null) {
      this.handle = { handle: restored.handle, expiresAtMs: restored.handleExpiresAtMs };
      this.handleUser = { userId: restored.userId, email: restored.email, name: restored.name };
    } else {
      // A payload we cannot validate is a payload we do not keep.
      this.mirror.clear();
    }
  }

  /** True when the mirror is degraded (blocked or absent storage): a reload restores nothing. */
  get degraded(): boolean {
    return this.mirror.degraded;
  }

  /**
   * Adopt a session. The mirror is rewritten from refreshHandle: pass the
   * handle to keep a reload restorable, pass null (or omit) to leave nothing
   * restorable, because a new identity inherits nobody's handle. Returns
   * false and changes nothing when the input is unusable.
   */
  adopt(input: {
    token: string;
    tokenExpiresAtMs: number;
    user: SessionUser;
    refreshHandle?: RefreshHandle | null;
  }): boolean {
    if (typeof input.token !== "string" || input.token === "") return false;
    if (typeof input.tokenExpiresAtMs !== "number" || !Number.isFinite(input.tokenExpiresAtMs)) return false;
    if (input.tokenExpiresAtMs <= this.now()) return false;
    const user = validateUser(input.user);
    if (user === null) return false;
    let handle: RefreshHandle | null = null;
    if (input.refreshHandle != null) {
      const candidate = input.refreshHandle;
      if (typeof candidate.handle !== "string" || candidate.handle === "") return false;
      if (typeof candidate.expiresAtMs !== "number" || !Number.isFinite(candidate.expiresAtMs)) return false;
      handle = { handle: candidate.handle, expiresAtMs: candidate.expiresAtMs };
    }
    this.token = input.token;
    this.tokenExpiresAtMs = input.tokenExpiresAtMs;
    this.user = user;
    if (handle !== null) {
      this.handle = handle;
      this.handleUser = user;
      this.mirror.write({
        v: 1,
        handle: handle.handle,
        handleExpiresAtMs: handle.expiresAtMs,
        userId: user.userId,
        email: user.email,
        name: user.name,
      });
    } else {
      this.handle = null;
      this.handleUser = null;
      this.mirror.clear();
    }
    return true;
  }

  /** The live session, or null when there is none or the token has expired. An expired token keeps the refresh handle: the platform session may outlive it. */
  get current(): Session | null {
    if (this.token === null || this.user === null) return null;
    if (this.now() >= this.tokenExpiresAtMs) return null;
    return { token: this.token, tokenExpiresAtMs: this.tokenExpiresAtMs, user: { ...this.user } };
  }

  /** What a page reload may restore, or null. An expired handle reads as null (the constructor already dropped it). */
  get restorable(): RestorableSession | null {
    if (this.handle === null || this.handleUser === null) return null;
    if (this.now() >= this.handle.expiresAtMs) return null;
    return {
      handle: this.handle.handle,
      expiresAtMs: this.handle.expiresAtMs,
      user: { ...this.handleUser },
    };
  }

  /** Wipe custody entirely: memory and mirror. Called on sign-out and on a new identity without a handle. */
  clear(): void {
    this.token = null;
    this.tokenExpiresAtMs = 0;
    this.user = null;
    this.handle = null;
    this.handleUser = null;
    this.mirror.clear();
  }
}
