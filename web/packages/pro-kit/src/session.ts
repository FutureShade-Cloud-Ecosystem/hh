// The TenantSession seam: the one narrow place a Pro app asks "who is signed
// in, and what may they do". It sits over the auth package's custody
// (SessionStore) and sign in orchestration (AuthController), so every guard
// those carry (sign out wins over a late answer, a newer sign in supersedes an
// older one, an expired token reads as signed out) holds here unchanged.
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
  AuthController,
  SessionStore,
  ThrowSafeMirror,
  authHeaderProvider,
  SESSION_MIRROR_KEY,
  type AccountSurface,
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

/** The bridge from a TenantSignInAdapter to the auth package's AccountSurface. */
class AdapterAccount implements AccountSurface {
  readonly #adapter: TenantSignInAdapter;
  #held: TenantAnswer | null = null;
  /** The adapter's own refusal, kept so the screen shows its words, not the controller's wrapper. */
  refusal: { error: string; status?: number } | null = null;

  constructor(adapter: TenantSignInAdapter) {
    this.#adapter = adapter;
  }

  async createEmailPasswordSession(email: string, password: string): Promise<AccountUser> {
    this.refusal = null;
    const answer = await this.#adapter.signIn(email, password);
    if (!answer.ok) {
      this.refusal = answer.status === undefined ? { error: answer.error } : { error: answer.error, status: answer.status };
      throw new Error(answer.error);
    }
    this.#held = answer.value;
    return { ...answer.value.user };
  }

  async createOAuth2Session(): Promise<void> {
    throw new Error("sign in with another service is not offered here");
  }

  async getSession(): Promise<AccountUser> {
    throw new Error("there is no session to resume");
  }

  async createJWT(): Promise<{ jwt: string; expiresAtMs: number }> {
    const held = this.#held;
    this.#held = null;
    if (held === null) throw new Error("there is no sign in to complete");
    return { jwt: held.token, expiresAtMs: held.expiresAtMs };
  }

  async deleteSession(): Promise<void> {
    this.#held = null;
    await this.#adapter.signOut();
  }
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
  readonly #account: AdapterAccount;
  readonly #controller: AuthController;
  readonly #client: ApiClient;
  readonly #listeners = new Set<() => void>();
  #profile: TenantProfile | null = null;
  readonly headers: HeaderProvider;

  constructor(options: InMemoryTenantSessionOptions) {
    const now = options.now ?? Date.now;
    this.#sessions = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, memoryStorage()), now });
    this.#account = new AdapterAccount(options.adapter);
    this.#controller = new AuthController({ account: this.#account, sessions: this.#sessions });
    this.headers = authHeaderProvider({ callerClass: "tenant", sessions: this.#sessions });
    this.#client = new ApiClient({ baseUrl: options.baseUrl ?? "", fetch: options.fetch, headers: this.headers });
  }

  async signIn(email: string, password: string): Promise<Result<TenantProfile>> {
    const adopted = await this.#controller.signInWithPassword(email, password);
    if (!adopted.ok) {
      const refusal = this.#account.refusal;
      if (refusal !== null) return refusal.status === undefined ? err(refusal.error) : err(refusal.error, refusal.status);
      return err(adopted.error);
    }
    const answer = await tenantAuth.tenantGetMe(this.#client);
    if (!answer.ok) {
      await this.#controller.signOut();
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
    await this.#controller.signOut();
    this.#profile = null;
    this.#notify();
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
