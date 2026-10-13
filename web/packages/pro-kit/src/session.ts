// The TenantSession seam: the one narrow place a Pro app asks "who is signed
// in, and what may they do". It sits over the auth package's custody
// (SessionStore). The auth controller's exchange needs an organisation a tenant
// caller does not have, so the adapter's answer is adopted straight into custody
// and an expired token reads as signed out.
//
// What it keeps, and what it never does:
// - The bearer token and its expiry live in the custody's private memory.
//   Nothing is written to web storage: the custody's display mirror is given
//   an in memory double, never the page's storage, so a reload signs the
//   person out rather than restoring anything.
// - The token is not in the URL, not in a log, and not in any serialised form
//   of this object (toJSON names the role only).
// - The contractor role and the capabilities are the core's own answer from
//   GET /api/v1/tenant/me, read once after the token is adopted. They are not
//   derived locally; the local matrix (capabilities.ts) is the fallback for a
//   screen that must decide before an answer exists, and it is pinned to the
//   core by a test.
//
// The way a person actually proves who they are is an adapter behind
// TenantSignInAdapter. The shipped adapters are the stub (stub-adapter.ts, for
// tests and the dev server) and the named seam realExchangeAdapter()
// (real-exchange.ts), which the auth lead's go will fill. Contractor sign in
// is not served by the core today: the tenant login routes are unregistered
// there, and the platform bearer is the session (auth contract, "Not in this
// contract").
import { err, tenantAuth, type FetchLike, type HeaderProvider, type Result, type TenantCapabilities, type TenantRole } from "@hardscapehouse/api-client";
import {
  SessionStore,
  ThrowSafeMirror,
  authHeaderProvider,
  SESSION_MIRROR_KEY,
  type AccountUser,
  type StorageLike,
} from "@hardscapehouse/auth";
import { ApiClient } from "@hardscapehouse/api-client";
import { capabilitiesFor } from "./capabilities.js";

/** What an adapter hands back once a person has proved who they are. */
export interface TenantAnswer {
  token: string;
  expiresAtMs: number;
  user: AccountUser;
}

/**
 * The way in. An adapter turns an email and a password into a bearer for the
 * tenant routes. Its refusals are plain words a person reads (never a code, a
 * stack or the names of the machinery behind it).
 */
export interface TenantSignInAdapter {
  signIn(email: string, password: string): Promise<Result<TenantAnswer>>;
  /** End whatever the adapter holds. Best effort; the custody is cleared either way. */
  signOut(): Promise<void>;
}

/** The signed in person, as the core describes them. */
export interface TenantProfile {
  userId: string;
  email: string;
  name: string;
  role: TenantRole;
  capabilities: TenantCapabilities;
}

export interface TenantSession {
  /** Prove who you are. On success the profile is the core's answer from tenant me. */
  signIn(email: string, password: string): Promise<Result<TenantProfile>>;
  signOut(): Promise<void>;
  /** The live bearer and its expiry, or null when signed out or expired. For the transport only. */
  current(): { token: string; expiresAtMs: number } | null;
  profile(): TenantProfile | null;
  /** Whether the signed in person holds a capability; false when signed out. */
  can(capability: string): boolean;
  /** Hear every change of who is signed in. Returns the unsubscribe. */
  subscribe(listener: () => void): () => void;
  /** The header provider the tenant client rides: Authorization while signed in, never X-Org. */
  readonly headers: HeaderProvider;
}

/** A storage double that lives and dies with the page: the custody mirror writes here, never to web storage. */
export function memoryStorage(): StorageLike {
  const map = new Map<string, string>();
  return {
    getItem: (key) => (map.has(key) ? (map.get(key) as string) : null),
    setItem: (key, value) => void map.set(key, value),
    removeItem: (key) => void map.delete(key),
  };
}

const NOT_SIGNED_IN_WORDS = "You are signed out. Sign in again.";
const CANNOT_OPEN_WORDS = "This account cannot open Pro. Ask the dealer to check your access.";
const UNAVAILABLE_WORDS = "Pro is not reachable right now. Try again in a moment.";

export interface InMemoryTenantSessionOptions {
  adapter: TenantSignInAdapter;
  /** The base URL the tenant routes answer on; empty for same origin (the dev server proxy, the demo box). */
  baseUrl?: string;
  fetch?: FetchLike;
  now?: () => number;
}

export class InMemoryTenantSession implements TenantSession {
  readonly #sessions: SessionStore;
  readonly #adapter: TenantSignInAdapter;
  readonly #client: ApiClient;
  readonly #listeners = new Set<() => void>();
  #profile: TenantProfile | null = null;
  readonly headers: HeaderProvider;

  constructor(options: InMemoryTenantSessionOptions) {
    const now = options.now ?? Date.now;
    this.#sessions = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, memoryStorage()), now });
    this.#adapter = options.adapter;
    this.headers = authHeaderProvider({ callerClass: "tenant", sessions: this.#sessions });
    this.#client = new ApiClient({ baseUrl: options.baseUrl ?? "", fetch: options.fetch, headers: this.headers });
  }

  async signIn(email: string, password: string): Promise<Result<TenantProfile>> {
    // A tenant sign in has no organisation and no platform exchange to ask: the adapter's answer is the
    // session, adopted straight into custody (the auth controller's exchange path is for staff).
    const signedIn = await this.#adapter.signIn(email, password);
    if (!signedIn.ok) return signedIn.status === undefined ? err(signedIn.error) : err(signedIn.error, signedIn.status);
    const held = signedIn.value;
    if (!this.#sessions.adopt({ token: held.token, tokenExpiresAtMs: held.expiresAtMs, user: { ...held.user } })) {
      await this.#clearCustody();
      return err("sign-in failed: the sign in returned an unusable session");
    }
    const answer = await tenantAuth.tenantGetMe(this.#client);
    if (!answer.ok) {
      await this.#clearCustody();
      this.#profile = null;
      this.#notify();
      const words = answer.status === 401 ? NOT_SIGNED_IN_WORDS : answer.status === 403 ? CANNOT_OPEN_WORDS : UNAVAILABLE_WORDS;
      return answer.status === undefined ? err(words) : err(words, answer.status);
    }
    const me = answer.value;
    this.#profile = {
      userId: me.id,
      email: me.email,
      name: me.name,
      role: me.role,
      capabilities: { ...me.capabilities },
    };
    this.#notify();
    return { ok: true, value: this.#copy(this.#profile) };
  }

  async signOut(): Promise<void> {
    await this.#clearCustody();
    this.#profile = null;
    this.#notify();
  }

  async #clearCustody(): Promise<void> {
    try {
      await this.#adapter.signOut();
    } catch {
      // the far side was already gone; custody is cleared regardless
    }
    this.#sessions.clear();
  }

  current(): { token: string; expiresAtMs: number } | null {
    const live = this.#sessions.current;
    return live === null ? null : { token: live.token, expiresAtMs: live.tokenExpiresAtMs };
  }

  profile(): TenantProfile | null {
    // An expired token is signed out, whatever the profile still holds.
    if (this.#profile === null || this.current() === null) return null;
    return this.#copy(this.#profile);
  }

  can(capability: string): boolean {
    const profile = this.profile();
    if (profile === null) return false;
    return (profile.capabilities as unknown as Record<string, boolean>)[capability] === true;
  }

  subscribe(listener: () => void): () => void {
    this.#listeners.add(listener);
    return () => void this.#listeners.delete(listener);
  }

  /** Names the role only: a logger or an error reporter that stringifies the session prints nothing secret. */
  toJSON(): { signedIn: boolean; role: TenantRole | null } {
    const profile = this.profile();
    return { signedIn: profile !== null, role: profile === null ? null : profile.role };
  }

  #copy(profile: TenantProfile): TenantProfile {
    return { ...profile, capabilities: { ...profile.capabilities } };
  }

  #notify(): void {
    for (const listener of [...this.#listeners]) listener();
  }
}

/** The matrix fallback for a screen that must decide before tenant me has answered. */
export { capabilitiesFor };
