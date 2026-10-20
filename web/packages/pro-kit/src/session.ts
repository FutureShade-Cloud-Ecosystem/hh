// The TenantSession seam: the one narrow place a Pro app asks "who is signed
// in, and what may they do". It sits over the auth package's custody
// (SessionStore). The auth controller's exchange needs an organisation a tenant
// caller does not have, so the adapter's answer is adopted straight into custody
// and an expired token reads as signed out.
//
// It also guards overlapping calls. Every signIn takes a generation at entry
// and signOut bumps it, so a sign in that is superseded while it waits (on the
// adapter or on tenant me) adopts nothing, never clears the newer custody, and
// answers in plain words: a sign out always wins over a sign in in flight, and
// the newest sign in wins over an older one whichever answers first. A new
// sign in also drops the earlier profile at once, so no role is ever readable
// over a bearer it did not come from.
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
import type { SignInMethod } from "./signin-methods.js";

/** What an adapter hands back once a person has proved who they are. */
export interface TenantAnswer {
  token: string;
  expiresAtMs: number;
  user: AccountUser;
  /** Words when an adapter signs the person in directly in place of sending a link. */
  notice?: string;
}

/**
 * The way in. An adapter turns an email and a password into a bearer for the
 * tenant routes. Its refusals are plain words a person reads (never a code, a
 * stack or the names of the machinery behind it).
 */
export interface TenantSignInAdapter {
  signIn(email: string, password: string): Promise<Result<TenantAnswer>>;
  /** The dealer's allowed methods, for the sign in card. Absent reads as the password way only. */
  methods?(): readonly SignInMethod[];
  /** A method the dealer refuses, shown as the card's refusal; absent or null shows none. */
  refused?(): SignInMethod | null;
  /** The role labels a demo build lets a person pick instead of proving who they are. Absent means none. */
  personas?(): readonly string[];
  /** Any way in other than email and password: an email link, a provider, a demo role. */
  signInWith?(request: SignInRequest): Promise<Result<TenantAnswer>>;
  /** End whatever the adapter holds. Best effort; the custody is cleared either way. */
  signOut(): Promise<void>;
}

/** One way in other than email and password: an email link or a provider, or (demo build only) a role label. */
export type SignInRequest =
  | { method: "persona"; roleLabel: string }
  | { method: SignInMethod; email?: string };

/** What the sign in card is told: the dealer's methods, one it refuses, and the demo roles on offer. */
export interface SignInOptions {
  methods: readonly SignInMethod[];
  refused: SignInMethod | null;
  personas: readonly string[];
}

/** The signed in person, as the core describes them. */
export interface TenantProfile {
  userId: string;
  email: string;
  name: string;
  role: TenantRole;
  capabilities: TenantCapabilities;
  /** Words when an adapter signed the person in directly in place of sending a link. */
  notice?: string;
}

export interface TenantSession {
  /** Prove who you are. On success the profile is the core's answer from tenant me. */
  signIn(email: string, password: string): Promise<Result<TenantProfile>>;
  /** Any way in other than email and password; the profile is the core's answer from tenant me. */
  signInWith(request: SignInRequest): Promise<Result<TenantProfile>>;
  /** Re-read tenant me for the live token after a network failure on the session check. */
  check(): Promise<Result<TenantProfile>>;
  /** What the sign in card offers, from the adapter. */
  options(): SignInOptions;
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
const REPLACED_WORDS = "That sign in was replaced by a newer one or by a sign out. Sign in again.";
const UNAVAILABLE_METHOD_WORDS = "That way of signing in is not open yet. Use another way.";
const UNAVAILABLE_WORDS = "Pro is not reachable right now. Try again in a moment.";

export interface InMemoryTenantSessionOptions {
  adapter: TenantSignInAdapter;
  /** The base URL the tenant routes answer on; empty for same origin (the dev server proxy, the demo box). */
  baseUrl?: string;
  fetch?: FetchLike;
  now?: () => number;
  /** Heard as soon as a bearer is adopted into custody or cleared, while tenant me may still be in flight. */
  onCustody?: () => void;
}

export class InMemoryTenantSession implements TenantSession {
  readonly #sessions: SessionStore;
  readonly #adapter: TenantSignInAdapter;
  readonly #client: ApiClient;
  readonly #listeners = new Set<() => void>();
  readonly #onCustody?: () => void;
  #profile: TenantProfile | null = null;
  #notice: string | undefined = undefined;
  // Bumped by every sign in at entry and by sign out; a call that finds it moved has been superseded.
  #generation = 0;
  readonly headers: HeaderProvider;

  constructor(options: InMemoryTenantSessionOptions) {
    const now = options.now ?? Date.now;
    this.#sessions = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, memoryStorage()), now });
    this.#adapter = options.adapter;
    this.#onCustody = options.onCustody;
    this.headers = authHeaderProvider({ callerClass: "tenant", sessions: this.#sessions });
    this.#client = new ApiClient({ baseUrl: options.baseUrl ?? "", fetch: options.fetch, headers: this.headers });
  }

  async signIn(email: string, password: string): Promise<Result<TenantProfile>> {
    return this.#enter(() => this.#adapter.signIn(email, password));
  }

  async signInWith(request: SignInRequest): Promise<Result<TenantProfile>> {
    const adapter = this.#adapter;
    return this.#enter(async () => (adapter.signInWith === undefined ? err<TenantAnswer>(UNAVAILABLE_METHOD_WORDS) : adapter.signInWith(request)));
  }

  async check(): Promise<Result<TenantProfile>> {
    if (this.current() === null) return err(NOT_SIGNED_IN_WORDS, 401);
    const generation = ++this.#generation;
    return this.#readMe(generation);
  }

  options(): SignInOptions {
    const adapter = this.#adapter;
    return {
      methods: adapter.methods?.() ?? ["futurebuild"],
      refused: adapter.refused?.() ?? null,
      personas: adapter.personas?.() ?? [],
    };
  }

  async #enter(prove: () => Promise<Result<TenantAnswer>>): Promise<Result<TenantProfile>> {
    // A tenant sign in has no organisation and no platform exchange to ask: the adapter's answer is the
    // session, adopted straight into custody (the auth controller's exchange path is for staff).
    const generation = ++this.#generation;
    this.#notice = undefined;
    if (this.#profile !== null) {
      this.#profile = null;
      this.#notify();
    }
    const signedIn = await prove();
    if (generation !== this.#generation) return err(REPLACED_WORDS);
    if (!signedIn.ok) return signedIn.status === undefined ? err(signedIn.error) : err(signedIn.error, signedIn.status);
    const held = signedIn.value;
    if (!this.#sessions.adopt({ token: held.token, tokenExpiresAtMs: held.expiresAtMs, user: { ...held.user } })) {
      await this.#clearCustody();
      return err(UNAVAILABLE_WORDS);
    }
    this.#notice = held.notice;
    this.#onCustody?.();
    return this.#readMe(generation);
  }

  async #readMe(generation: number): Promise<Result<TenantProfile>> {
    const asked = this.#sessions.current?.token ?? null;
    const answer = await tenantAuth.tenantGetMe(this.#client);
    // A refusal is read before the generation: the core refused the token it was asked with, so that token goes
    // whatever call is newest. Only a success or a non-refusal failure of a superseded call is dropped as replaced.
    if (!answer.ok && (answer.status === 401 || answer.status === 403)) {
      const words = answer.status === 401 ? NOT_SIGNED_IN_WORDS : CANNOT_OPEN_WORDS;
      if (asked !== null && this.#sessions.current?.token === asked) {
        const ended = this.#clearCustody();
        this.#profile = null;
        this.#notify();
        await ended;
        return err(words, answer.status);
      }
      return generation !== this.#generation ? err(REPLACED_WORDS) : err(words, answer.status);
    }
    if (generation !== this.#generation) return err(REPLACED_WORDS);
    if (!answer.ok) {
      return answer.status === undefined ? err(UNAVAILABLE_WORDS) : err(UNAVAILABLE_WORDS, answer.status);
    }
    const me = answer.value;
    this.#profile = {
      userId: me.id,
      email: me.email,
      name: me.name,
      role: me.role,
      capabilities: { ...me.capabilities },
      ...(this.#notice === undefined ? {} : { notice: this.#notice }),
    };
    this.#notify();
    return { ok: true, value: this.#copy(this.#profile) };
  }

  async signOut(): Promise<void> {
    // Any sign in still in flight is now superseded: it adopts nothing when it lands.
    this.#generation += 1;
    await this.#clearCustody();
    this.#profile = null;
    this.#notify();
  }

  async #clearCustody(): Promise<void> {
    // Custody goes first and at once, so nothing that lands during the adapter's own sign out can keep it alive.
    this.#sessions.clear();
    this.#notice = undefined;
    this.#onCustody?.();
    try {
      await this.#adapter.signOut();
    } catch {
      // the far side was already gone; custody is cleared regardless
    }
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
