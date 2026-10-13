// The wiring into the api-client: one header provider the shell passes to
// `new ApiClient({ headers: authHeaderProvider(...) })`, built for the
// caller's class. A staff caller sends Authorization from memory (the JWT
// never rests anywhere else) and X-Org from the active organisation. A
// tenant caller sends Authorization only: the core answers 400 to X-Org on
// every tenant route and on sync under a tenant bearer
// (core/internal/platform/authz/middleware.go, serveTenant), so a tenant
// provider takes no organisation input at all, not even an ignored one. The
// provider sends nothing else: the unlock audit pair is gone until the core
// can verify an unlock (see src/lock.ts), and the device lock gates nothing
// here because it gates nothing anywhere.
//
// A staff request without an active organisation is refused by the client
// before it leaves: the provider throws naming the missing header, and the
// transport (web/packages/api-client/src/http.ts) turns a provider throw
// into an error Result without dialing. The provider is synchronous on
// purpose: no request ever leaves this process without the org resolved.
//
// NO ROLE PICKER. This package exposes no role state at all: not a role
// field, not a role header, not a role-based branch anywhere below. Roles
// are the server's, and the header provider is exactly where a client-side
// role assumption would creep in (pick a header by role, render by role).
// The rule is HH Pro's, carried with its provenance: src/core/supplier/
// port.ts lines 71 to 77 in the HH Pro reference tree (READ ONLY): a role
// code is the account's own, never re-expressed as another product's role
// vocabulary, because "Shipping a role picker the server cannot enforce is
// the post-M8 bug class the spec's R-4 exists to prevent." What the server
// cannot enforce, this package does not ship.
//
// The platform sign-in seam is the narrow AccountSurface below: the FB Console
// account operations this package needs (createEmailPasswordSession,
// createOAuth2Session, getSession, deleteSession) and the exchange, which
// trades the signed-in FB Console account for the core's own session token
// (POST /api/v1/auth/exchange). The calls to FB Console live behind this seam
// in an adapter (src/appwrite-adapter.ts, a separate entry, plain REST over
// fetch with no SDK, for the browser; the Tauri shells forward to their Rust
// side); the package takes no npm dependency for it and vendors no fake
// of the console (src/testing.ts carries a fake
// of THIS interface, with obviously fake values, for tests). Sign-in methods
// for the first release: the FutureBuild house sign-in (email and password)
// and Google, both directly against FB Console's HH project; device unlock
// is in scope (src/lock.ts). The bearer is always the core's token from the
// exchange. An FB Console JWT is minted inside an adapter for one exchange
// attempt and never reaches this package, custody or any storage.
// getSession is the reload's re-sign-in: FB Console's own session is an
// httpOnly first party cookie script cannot read, so the platform answers
// for the sign-in while this package holds nothing to trade.
import { err, ok, type HeaderProvider, type Result } from "@hardscapehouse/api-client";
import type { DeviceLockHandle } from "./lock.js";
import type { SessionStore } from "./session.js";

function asMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/** The display profile an exchange answer names, read field by field; custody refuses what it cannot use. */
function profileOf(answer: ExchangeAnswer): AccountUser {
  const user = answer.user as Partial<ExchangeUser> | null;
  return {
    userId: user?.user_id as string,
    email: user?.email as string,
    name: user?.display_name as string,
  };
}

/** The caller class a header provider is built for: the core's authz middleware requires X-Org on staff routes and refuses it on tenant routes. */
export type CallerClass = "staff" | "tenant";

export interface StaffHeaderProviderOptions {
  callerClass: "staff";
  /** The custody holding the in-memory token (and nothing persistent). */
  sessions: SessionStore;
  /** The active organisation, resolved per request by the shell. Required for staff: the core answers 400 to a staff request without X-Org. */
  activeOrg: () => string | null | undefined;
}

export interface TenantHeaderProviderOptions {
  callerClass: "tenant";
  /** The custody holding the in-memory token (and nothing persistent). */
  sessions: SessionStore;
  // Deliberately no activeOrg input: a tenant caller has no org header to
  // send, and the core answers 400 to one that tries.
}

export type AuthHeaderProviderOptions = StaffHeaderProviderOptions | TenantHeaderProviderOptions;

/**
 * Build the header provider for the api-client, per caller class.
 *
 * Staff: throws naming X-Org when no active organisation is set or the
 * value is unusable (the transport converts that into an error Result and
 * the request never leaves); returns X-Org and, while a live (unexpired)
 * session exists, Authorization, and nothing else.
 *
 * Tenant: returns Authorization while a live session exists, and nothing
 * else; the type forbids an organisation input, so the provider cannot mint
 * the header the core refuses.
 *
 * There is deliberately no role input to configure, and no unlock audit
 * pair: the lock gates nothing (src/lock.ts).
 */
export function authHeaderProvider(options: AuthHeaderProviderOptions): HeaderProvider {
  if (options.callerClass === "tenant") {
    return () => {
      const headers: Record<string, string> = {};
      const session = options.sessions.current;
      if (session !== null) {
        headers["Authorization"] = `Bearer ${session.token}`;
      }
      return headers;
    };
  }
  return () => {
    const org = options.activeOrg();
    if (typeof org !== "string" || org.trim() === "") {
      throw new Error("X-Org is missing: no active organisation is set for this request");
    }
    if (/[\r\n\0]/.test(org)) {
      throw new Error("X-Org is malformed: the active organisation value carries a control character");
    }
    const headers: Record<string, string> = { "X-Org": org };
    const session = options.sessions.current;
    if (session !== null) {
      headers["Authorization"] = `Bearer ${session.token}`;
    }
    return headers;
  };
}

/** The OAuth2 providers the first release confirms: Google, through FB Console's own OAuth provider. */
export type OAuth2Provider = "google";

/** The account identity, as the adapter resolves it from the platform. */
export interface AccountUser {
  userId: string;
  email: string;
  name: string;
}

/**
 * The user block of the core's exchange answer, in the core's snake case
 * (POST /api/v1/auth/exchange). This package reads user_id, email and
 * display_name into the display profile and keeps no role state: roles are
 * the server's (see the NO ROLE PICKER note above). The rest rides along
 * untouched for the host that wants it.
 */
export interface ExchangeUser {
  user_id: string;
  email: string;
  display_name: string;
  role_code: string;
  role_name: string;
  tier: number;
  scope: string;
}

/** The core's exchange answer: its own session token, the expiry in milliseconds, and the user block. */
export interface ExchangeAnswer {
  token: string;
  expiresAtMs: number;
  user: ExchangeUser;
}

/**
 * Why an exchange did not produce a token. The codes are the shells
 * contract's (shared/AUTH-SHELL-CONTRACT.md): the core's 400, 401, 403, 429
 * and 503, no network, and `no_session` when FB Console no longer holds the
 * session that a JWT must be minted from. Only unauthorized, forbidden and
 * no_session end a session; the rest never do.
 */
export type ExchangeRefusalCode =
  | "bad_request"
  | "unauthorized"
  | "forbidden"
  | "rate_limited"
  | "unavailable"
  | "offline"
  | "no_session";

/** A typed answer, never a throw: the token, or the code of the refusal. */
export type ExchangeResult = { ok: true; answer: ExchangeAnswer } | { ok: false; code: ExchangeRefusalCode };

/**
 * What an adapter throws from getSession, or a sign in, when the platform
 * could not be reached at all. It says nothing about the person: a resume
 * that meets it keeps the profile and the lock and tries again later.
 */
export class PlatformUnreachableError extends Error {
  constructor(message = "the platform could not be reached (network)") {
    super(message);
    this.name = "PlatformUnreachableError";
  }
}

const ENDS_SESSION: ReadonlySet<ExchangeRefusalCode> = new Set(["unauthorized", "forbidden", "no_session"]);

/**
 * The account surface this package needs, as a narrow injected interface.
 * An adapter maps these onto the FB Console account (REST over fetch in a
 * browser, the Rust side in a Tauri shell), the one integration point a
 * host wires, recorded in the README.
 */
export interface AccountSurface {
  /**
   * The house sign-in (FB Console email and password). Resolves the
   * signed-in user. The platform's own session stays on the issuer side (an
   * httpOnly cookie script cannot read); nothing session shaped is handed to
   * this package but the user.
   */
  createEmailPasswordSession(email: string, password: string): Promise<AccountUser>;
  /** The Google flow; the platform navigates to redirectTo and returns through it. */
  createOAuth2Session(provider: OAuth2Provider, redirectTo: string): Promise<void>;
  /**
   * The current platform session's user, when one exists. The reload's
   * re-sign-in: the issuer's session cookie, unreadable to script, answers
   * here, so the host can restore a session from the issuer while this
   * package holds nothing to trade.
   */
  getSession(): Promise<AccountUser>;
  /**
   * Trade the signed-in FB Console account for the core's session token for
   * one organisation. Every attempt mints its own FB Console JWT inside the
   * adapter and drops it at the end of the attempt; an adapter never holds
   * or reuses one, and never lets one out. Resolves a typed refusal rather
   * than throwing.
   */
  exchange(org: string): Promise<ExchangeResult>;
  /** End the platform session. */
  deleteSession(): Promise<void>;
}

/** The two timer functions the refresh uses, injected so tests do not wait. */
export interface RefreshTimers {
  setTimeout(callback: () => void, ms: number): unknown;
  clearTimeout(handle: unknown): void;
}

/** The refresh fires this long before the token expires, unless the window is too short to afford it. */
export const DEFAULT_REFRESH_LEAD_MS = 60_000;
/** The first wait after a refresh the core could not answer (429, 503, offline); it doubles up to the cap. */
export const DEFAULT_REFRESH_BACKOFF_MS = 15_000;
const MAX_REFRESH_BACKOFF_MS = 300_000;
const MIN_REFRESH_DELAY_MS = 1_000;

export interface AdoptSessionInput {
  token: string;
  tokenExpiresAtMs: number;
  user: AccountUser;
}

/**
 * The sign-out counter, made shareable. The controller bumps it on both
 * sides of signOut()'s await, exactly as its guard always has; the device
 * lock reads it before and after every platform await of an engage or an
 * unlock. A sign-out is an ACT, counted here; the engaged user's own token
 * refreshing or expiring mid prompt is nobody's act and moves nothing, and
 * that is how the lock tells the two apart (Colton's ruling C, relay
 * message 048: the lock belongs to the signed-in person, or to nobody). The
 * shell creates ONE counter and hands the same instance to the controller
 * and to the lock; a lock constructed without one holds a private counter
 * that nobody bumps, so its prompts see no sign-out and the expiry rule
 * governs alone.
 */
export class SignOutCounter {
  #count = 0;

  /** Count one sign-out. The controller's sign-out path is the only caller. */
  bump(): void {
    this.#count += 1;
  }

  /** The count so far. */
  get count(): number {
    return this.#count;
  }
}

/**
 * The sign-in orchestration over the seams: an AccountSurface for the
 * platform, a SessionStore for custody. Every path resolves a Result; a
 * wrong credential is data, never a throw.
 *
 * The generation guard: sign-out must win against a platform answer that
 * lands late. signOut() bumps a generation counter before its await and
 * again after it, just before the clear; signInWithPassword and resume
 * capture the counter before their first await and compare it after every
 * await, and on a mismatch they adopt nothing and resolve an error Result.
 * Every successful adoption bumps the counter as well, so an adoption that
 * lands while another platform answer is still in flight supersedes it: of
 * two overlapping resumes with no sign-out between them, only the first
 * adoption lands, and the older one's late failure clears nothing and its
 * late success adopts nothing, so the newer session is neither wiped nor
 * replaced. Without the guard, a resume begun before the user signed out
 * would re-sign the custody in when the platform answered after the
 * sign-out, and a resume begun during the sign-out's own await (it reads
 * the already bumped generation) would do the same once the platform
 * answered after the sign-out returned; the profile would be written back
 * to storage either way.
 *
 * The sign-in answers to two counters of its own, not the shared generation:
 * it is an explicit act by the person at the keyboard. The sign-out counter
 * cancels it: only signOut() bumps that counter, on both sides of its own
 * await, so an adoption that lands first (a resume answering for whoever the
 * issuer still holds, or an out-of-band callback) does not cancel the
 * person's explicit sign-in: the sign-in carries on and its own adoption
 * wins custody, so the person who signed in is the person the tablet shows.
 * The sign-in sequence counter supersedes it: every sign-in submit bumps
 * that counter, and a sign-in that is no longer the newest adopts nothing
 * and mints nothing further. It WAITS for the newest sign-in to settle
 * before it resolves, and then resolves by what custody holds: the same
 * person signed in, success (the double submit of one form lands both
 * submits in either landing order, and neither promise resolves an error
 * while the person is signed in); a different person, or nobody, superseded,
 * and the screen says so, because of two people's sign-ins in flight the
 * older one's late answer must never replace the newer person's session: on
 * a shared tablet that would be the wrong person signed in, silently. A
 * sign-out cancels a sign-in outright, from both sides of the sign-out's
 * await, exactly as the generation guard does, and the cancellation outranks
 * the supersession: a sign-in parked under a sign-out resolves cancelled,
 * never superseded, without waiting.
 *
 * The lock rides the same acts (Colton's ruling C, relay message 048: the
 * lock is yours while you are signed in; a lock belongs to the signed-in
 * person, or to nobody). When the shell wires a lock, signOut() disengages
 * it beside the custody it clears, under the same guards, so a sign-out
 * that fails part way leaves no lock behind; and every successful adoption
 * reconciles the lock with the user it adopted, disengaging a lock bound to
 * somebody else before the new session is shown, while the same user
 * signing in again keeps their lock. The controller takes the lock as the
 * narrow DeviceLockHandle and the counter as a shared SignOutCounter, so
 * the lock can tell a sign-out (the counter moves) from the bound user's
 * own token refreshing or expiring mid prompt (nothing moves).
 *
 * The bearer is the core's exchange answer, nothing else: the sign-in and the
 * resume ask AccountSurface.exchange(org) and adopt its token and expiry, so
 * an FB Console JWT never reaches custody. While a session is live the
 * controller re-exchanges before the token expires (a lead of a minute by
 * default, capped at half the window so a short window never hammers), on
 * the injected clock and timers. The refresh answers to the same generation
 * guard as every other act, so a sign-out that lands while a refresh is in
 * flight wins: the late answer adopts nothing and arms nothing. What a
 * refresh does with each answer: a new token is adopted; unauthorized,
 * forbidden or no_session ends the session (the same path as signOut(),
 * which deletes the platform session and clears custody); a person other
 * than the signed-in one ends it too; rate limited, unavailable, bad
 * request, offline and a thrown adapter keep the session, keep the current
 * token until it expires, and try again after a doubling backoff. A
 * controller built without an active organisation never arms a refresh and
 * refuses to sign in (the demo apps adopt a mint and have nothing to
 * refresh).
 */
export class AuthController {
  readonly #account: AccountSurface;
  readonly #sessions: SessionStore;
  readonly #lock: DeviceLockHandle | null;
  readonly #signOuts: SignOutCounter;
  readonly #activeOrg: (() => string | null | undefined) | null;
  readonly #now: () => number;
  readonly #timers: RefreshTimers;
  readonly #refreshLeadMs: number;
  readonly #refreshBackoffBaseMs: number;
  #refreshBackoffMs: number;
  #refreshTimer: unknown = null;
  #generation = 0;
  #signIns = 0;
  /** The newest submit's number and the promise its own flow resolves when its outcome is decided; what a stale sign-in waits on. */
  #newestSignIn: { signIns: number; settled: Promise<void> } | null = null;

  constructor(deps: {
    account: AccountSurface;
    sessions: SessionStore;
    /** The device lock, when the shell wires one; the sign-out and the adoptions disengage it as the ruling requires. */
    lock?: DeviceLockHandle;
    /** The sign-out counter the lock reads across its prompts; a fresh private one when omitted. */
    signOuts?: SignOutCounter;
    /** The active organisation the exchange is asked for. Without it the controller neither signs in nor refreshes. */
    activeOrg?: () => string | null | undefined;
    /** The clock the refresh is timed on; Date.now when omitted. */
    now?: () => number;
    /** The timers the refresh is armed on; the platform's when omitted. */
    timers?: RefreshTimers;
    /** How long before expiry the refresh fires; a minute when omitted. */
    refreshLeadMs?: number;
    /** The first wait after an unanswered refresh; it doubles up to a cap. */
    refreshBackoffMs?: number;
  }) {
    this.#account = deps.account;
    this.#sessions = deps.sessions;
    this.#lock = deps.lock ?? null;
    this.#signOuts = deps.signOuts ?? new SignOutCounter();
    this.#activeOrg = deps.activeOrg ?? null;
    this.#now = deps.now ?? Date.now;
    this.#timers = deps.timers ?? {
      setTimeout: (callback, ms) => {
        const handle = globalThis.setTimeout(callback, ms);
        // A waiting refresh must not keep a Node process (a test run, a tool) alive; browsers return a number.
        (handle as { unref?: () => void }).unref?.();
        return handle;
      },
      clearTimeout: (handle) => globalThis.clearTimeout(handle as ReturnType<typeof globalThis.setTimeout>),
    };
    this.#refreshLeadMs = deps.refreshLeadMs ?? DEFAULT_REFRESH_LEAD_MS;
    this.#refreshBackoffBaseMs = deps.refreshBackoffMs ?? DEFAULT_REFRESH_BACKOFF_MS;
    this.#refreshBackoffMs = this.#refreshBackoffBaseMs;
  }

  /** The active organisation, or null when none is wired or set. */
  #org(): string | null {
    if (this.#activeOrg === null) return null;
    const org = this.#activeOrg();
    return typeof org === "string" && org.trim() !== "" ? org : null;
  }

  /** One exchange attempt as a typed result: a throwing adapter reads as unavailable, never as a refusal that ends a session. */
  async #callExchange(org: string): Promise<ExchangeResult> {
    try {
      const result = await this.#account.exchange(org);
      if (result !== null && typeof result === "object" && (result.ok === true || result.ok === false)) return result;
    } catch {
      // fall through: the adapter broke its contract, which says nothing about the person
    }
    return { ok: false, code: "unavailable" };
  }

  /**
   * The house sign-in: establish the platform session, mint the JWT, adopt
   * it. Custody keeps the token in memory and the display profile in the
   * mirror; no credential is stored. Two guards run across every await. The
   * sign-out counter: an explicit sign-in is the person's own act, so a
   * resume or an adoption that lands first does not cancel it (the sign-in
   * carries on and its adoption wins custody), and only a sign-out does,
   * from both sides of the sign-out's own await. The sign-in sequence: a
   * newer submit supersedes an older one, which then adopts nothing, minting
   * nothing further; it waits for the newest submit to settle, then resolves
   * by what custody holds: the same person, success; anybody else, or
   * nobody, superseded, and the screen says so.
   */
  async signInWithPassword(email: string, password: string): Promise<Result<AccountUser>> {
    this.#signIns += 1;
    const signIns = this.#signIns;
    // This submit's settlement: what every older submit still in flight
    // waits for before it resolves, and what a still newer submit replaces.
    let settle!: () => void;
    const settled = new Promise<void>((resolve) => {
      settle = resolve;
    });
    this.#newestSignIn = { signIns, settled };
    try {
      return await this.#signInFlow(email, password, signIns);
    } finally {
      settle(); // this submit's outcome is decided, whatever it was
    }
  }

  async #signInFlow(email: string, password: string, signIns: number): Promise<Result<AccountUser>> {
    const org = this.#org();
    if (org === null) return err("sign-in failed: no active organisation is set, so there is nothing to sign in to");
    const signOuts = this.#signOuts.count;
    let user: AccountUser;
    try {
      user = await this.#account.createEmailPasswordSession(email, password);
    } catch (error) {
      return err(`sign-in failed: ${asMessage(error)}`);
    }
    if (this.#signOuts.count !== signOuts) {
      return err("sign-in cancelled: sign-out landed while the sign-in was in flight, so nothing was adopted");
    }
    if (this.#signIns !== signIns) return this.#staleSignIn(signIns, user);
    const exchanged = await this.#callExchange(org);
    if (this.#signOuts.count !== signOuts) {
      return err("sign-in cancelled: sign-out landed while the sign-in was in flight, so nothing was adopted");
    }
    if (this.#signIns !== signIns) return this.#staleSignIn(signIns, user);
    if (!exchanged.ok) {
      // Nothing was adopted, so no platform session is kept "for a retry":
      // a retry is another sign in, and the platform refuses a create while a
      // session is active. The delete is only for the newest submit, which
      // the checks above established.
      await this.#deleteQuietly();
      return err(`sign-in failed: the exchange was refused (${exchanged.code})`);
    }
    const profile = profileOf(exchanged.answer);
    const adopted = this.#sessions.adopt({
      token: exchanged.answer.token,
      tokenExpiresAtMs: exchanged.answer.expiresAtMs,
      user: profile,
    });
    if (!adopted) return err("sign-in failed: the exchange returned an unusable session");
    this.#generation += 1; // the adoption is a structural change: it supersedes anything already in flight
    this.#armRefresh(exchanged.answer.expiresAtMs);
    this.#lock?.disengageUnlessBoundTo(profile.userId);
    return ok({ ...profile });
  }

  async #deleteQuietly(): Promise<void> {
    try {
      await this.#account.deleteSession();
    } catch {
      // already gone
    }
  }

  /** Cancel the pending refresh, if any. */
  #disarmRefresh(): void {
    if (this.#refreshTimer !== null) {
      this.#timers.clearTimeout(this.#refreshTimer);
      this.#refreshTimer = null;
    }
  }

  /**
   * Arm the one pending refresh for a token that expires at expiresAtMs, and
   * reset the backoff: every adoption is a fresh start. The lead never
   * exceeds half the window, and the wait never drops under a second.
   */
  #armRefresh(expiresAtMs: number): void {
    this.#disarmRefresh();
    this.#refreshBackoffMs = this.#refreshBackoffBaseMs;
    if (this.#activeOrg === null) return;
    const windowMs = expiresAtMs - this.#now();
    const lead = Math.min(this.#refreshLeadMs, windowMs / 2);
    this.#scheduleRefresh(Math.max(windowMs - lead, MIN_REFRESH_DELAY_MS));
  }

  #scheduleRefresh(delayMs: number): void {
    this.#disarmRefresh();
    this.#refreshTimer = this.#timers.setTimeout(() => {
      this.#refreshTimer = null;
      void this.#refresh();
    }, delayMs);
  }

  /** The refresh: one exchange for the person already signed in, judged by the rules in the class comment. */
  async #refresh(): Promise<void> {
    const generation = this.#generation;
    const org = this.#org();
    const subject = this.#sessions.profile?.userId ?? null;
    if (org === null || subject === null) return; // nothing to refresh for
    const result = await this.#callExchange(org);
    if (this.#generation !== generation) return; // a sign-out or another adoption won the race
    if (result.ok) {
      const profile = profileOf(result.answer);
      if (profile.userId !== subject) {
        await this.signOut(); // never replace one person's session with another's
        return;
      }
      const adopted = this.#sessions.adopt({
        token: result.answer.token,
        tokenExpiresAtMs: result.answer.expiresAtMs,
        user: profile,
      });
      if (adopted) {
        this.#generation += 1;
        this.#armRefresh(result.answer.expiresAtMs);
        return;
      }
    } else if (ENDS_SESSION.has(result.code)) {
      await this.signOut();
      return;
    }
    // Rate limited, unavailable, bad request, offline, or an answer custody
    // would not take: the session stays, and the next try waits longer.
    this.#backoffRefresh();
  }

  /** Try the refresh again after the current backoff, which then doubles up to the cap. */
  #backoffRefresh(): void {
    const wait = this.#refreshBackoffMs;
    this.#refreshBackoffMs = Math.min(wait * 2, MAX_REFRESH_BACKOFF_MS);
    this.#scheduleRefresh(wait);
  }

  /**
   * The resolution of a sign-in that is no longer the newest: it adopts
   * nothing, whatever the platform answered for it, and it does not resolve
   * straight away: it waits for every sign-in newer than it to settle (a
   * newer submit landing while it waits extends the wait), so its resolution
   * says what custody holds once the submit that beat it is done. The same
   * person signed in resolves successful without adopting (the double
   * submit of one form: the newest submit's adoption is that same user, in
   * either landing order, so neither promise of a double submit resolves an
   * error while the person is signed in); anybody else, or nobody, resolves
   * superseded, an error Result whose message is the screen's, so a person
   * whose answer landed late sees why they are not the one signed in.
   */
  async #staleSignIn(signIns: number, user: AccountUser): Promise<Result<AccountUser>> {
    // Wait for every submit newer than this one to settle. Each pass awaits
    // the newest submit there is and raises the high-water mark to its
    // number, so a newer submit landing mid wait extends the wait, and a
    // pass over an already settled newest exits: every settled submit at or
    // under the mark has decided, and the stale ones among them adopt
    // nothing, so once the newest has settled custody can move no further
    // from sign-ins.
    let waited = signIns;
    for (;;) {
      const newest = this.#newestSignIn;
      if (newest === null || newest.signIns <= waited) break;
      await newest.settled;
      waited = newest.signIns;
    }
    if (this.#sessions.current?.user.userId === user.userId) return ok({ ...user });
    return err("sign-in superseded: a newer sign-in landed while this one was in flight, so nothing was adopted");
  }

  /**
   * Adopt a session established out of band (the Google callback through the
   * FB Console OAuth redirect, or a resume's exchanged token), the ERP's adoptSession
   * posture: custody takes the token into private memory and the display
   * profile into the mirror, nothing else. A null or non-object user is
   * unusable input like any other: adopt returns false and adopts nothing,
   * never a throw.
   */
  adopt(input: AdoptSessionInput): boolean {
    const user: AccountUser | null = input.user;
    if (user === null || typeof user !== "object") return false;
    const adopted = this.#sessions.adopt({
      token: input.token,
      tokenExpiresAtMs: input.tokenExpiresAtMs,
      user: { userId: user.userId, email: user.email, name: user.name },
    });
    if (adopted) {
      this.#generation += 1; // the adoption is a structural change: it supersedes anything already in flight
      this.#armRefresh(input.tokenExpiresAtMs);
      this.#lock?.disengageUnlessBoundTo(user.userId);
    }
    return adopted;
  }

  /**
   * Re-sign-in through the issuer, and the token refresh path in one. The
   * platform's own session is an httpOnly cookie script cannot read, so
   * after a reload the shell calls resume(): getSession answers from the
   * issuer's cookie, a fresh JWT is minted, and custody adopts it into
   * memory. Nothing this package stored is needed or read to do it; the
   * mirror holds the display profile only. Refused as data when the
   * platform holds no session or mints nothing usable, and a failed resume
   * clears the custody it failed from: a stale display profile must not sit
   * in storage and in memory beside a resume that produced nothing, or the
   * shell would render the previous user's name over a signed-out custody.
   * (The one exception is the generation guard's cancellation: a resume
   * that lands after a sign-out writes nothing at all, because the sign-out
   * already wiped the custody it would clear.) A failed resume's custody
   * clear takes the wired lock with it: a resume that produced nothing is
   * the end of the signed-in state this tab knows, and a lock whose person
   * is no longer signed in belongs to nobody (Colton's ruling C, relay
   * message 048). The generation guard runs
   * across every await: a sign-out that lands mid resume cancels it as data
   * and nothing is adopted, so the late platform answer can never re-sign a
   * custody the user signed out. Every successful adoption bumps the same
   * counter, so of two overlapping resumes with no sign-out between them
   * only the first adoption lands: the older one's late failure clears
   * nothing of the newer session and its late success adopts nothing, in
   * either order. The failure paths clear only the custody they began from:
   * each clear is guarded by the same generation check, so a stale resume
   * whose platform answer FAILS after the user signed out and someone else
   * signed in resolves its error Result and leaves the new user's custody
   * alone.
   */
  async resume(): Promise<Result<AccountUser>> {
    const generation = this.#generation;
    const org = this.#org();
    if (org === null) return err("no platform session to resume: no active organisation is set");
    try {
      await this.#account.getSession(); // proves the platform still holds a session; the exchange names the person
    } catch (error) {
      if (this.#generation !== generation) {
        return err(`no platform session to resume: ${asMessage(error)}`);
      }
      // No network says nothing about the person: keep the profile and the
      // lock, and try again when the network may be back.
      if (error instanceof PlatformUnreachableError) {
        this.#backoffRefresh();
        return err(`no platform session to resume: ${asMessage(error)}`);
      }
      this.#failSignedOut();
      return err(`no platform session to resume: ${asMessage(error)}`);
    }
    if (this.#generation !== generation) {
      return err("resume cancelled: sign-out or another adoption landed while the resume was in flight, so nothing was adopted");
    }
    const exchanged = await this.#callExchange(org);
    if (!exchanged.ok) {
      if (this.#generation === generation) {
        if (ENDS_SESSION.has(exchanged.code)) {
          this.#failSignedOut();
          await this.#deleteQuietly();
        } else {
          // offline, unavailable, rate limited, bad request: the person is
          // not refused, so the profile and the lock stay and the bearer is
          // fetched again on the refresh's backoff.
          this.#backoffRefresh();
        }
      }
      return err(`the token exchange failed (${exchanged.code})`);
    }
    if (this.#generation !== generation) {
      return err("resume cancelled: sign-out or another adoption landed while the resume was in flight, so nothing was adopted");
    }
    const profile = profileOf(exchanged.answer);
    const adopted = this.#sessions.adopt({
      token: exchanged.answer.token,
      tokenExpiresAtMs: exchanged.answer.expiresAtMs,
      user: profile,
    });
    if (!adopted) {
      this.#failSignedOut();
      return err("the platform returned an unusable session");
    }
    this.#generation += 1; // the adoption is a structural change: it supersedes anything already in flight
    this.#armRefresh(exchanged.answer.expiresAtMs);
    this.#lock?.disengageUnlessBoundTo(profile.userId);
    return ok({ ...profile });
  }

  /**
   * What a failed resume does beside clearing the custody it failed from
   * (Colton's ruling C, relay message 048: the lock is yours while you are
   * signed in; a lock belongs to the signed-in person, or to nobody). A
   * resume that produces nothing clears the display profile and the live
   * token, so it is the end of the signed-in state this tab knows: the lock
   * goes with it, or a lock would sit in the mirror beside a signed-out
   * custody and open for nobody. The case that makes this more than tidying:
   * in a second tab, after the first tab signed out, this tab's resume
   * fails (the platform holds no session for it any more), and without this
   * the second tab would keep the first tab's lock and open it with nobody
   * signed in.
   */
  #failSignedOut(): void {
    this.#disarmRefresh();
    this.#sessions.clear();
    this.#lock?.disengage();
  }

  /**
   * Sign out: end the platform session (best effort; it may already be
   * gone) and wipe custody either way. The generation is bumped before
   * anything is awaited AND again after the await, just before the clear:
   * a sign-in or a resume that is already in flight adopts nothing when its
   * platform answer lands after this call, and a sign-in or a resume that
   * STARTS while deleteSession is still in flight reads the already bumped
   * generation, passes its check, and would adopt once the platform answers
   * after this method has returned; the second bump supersedes it too. The
   * sign-out wins, always, from both sides of its own await.
   *
   * The lock goes with the session (Colton's ruling C, relay message 048:
   * the lock is yours while you are signed in): the wired lock is
   * disengaged right beside the custody clear, under the same guards, so a
   * sign-out that fails part way (the catch above) still leaves no lock
   * behind. The sign-out counter is bumped on both sides of the await as
   * well, so an engage or an unlock prompt still open across the sign-out
   * is refused by the lock the moment its platform answers, whatever the
   * wiring.
   */
  async signOut(): Promise<void> {
    this.#disarmRefresh();
    this.#generation += 1;
    this.#signOuts.bump();
    try {
      await this.#account.deleteSession();
    } catch {
      // the platform session was already gone; custody is cleared regardless
    }
    // The hole on the far side of the await: anything that began while
    // deleteSession was pending captured the first bump and would adopt a
    // late platform answer. The second bump closes it, for the generation
    // guard and for the sign-in's sign-out counter alike.
    this.#generation += 1;
    this.#signOuts.bump();
    this.#disarmRefresh();
    this.#sessions.clear();
    this.#lock?.disengage();
  }
}
