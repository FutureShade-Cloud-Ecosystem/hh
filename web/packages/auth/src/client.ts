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
// The platform sign-in seam is the narrow AccountSurface below: the Appwrite
// account operations this package needs (createEmailPasswordSession,
// createOAuth2Session, getSession, plus the JWT mint and the session delete
// that custody requires). The Appwrite Web SDK is the one dependency real
// login cannot avoid, so it lives behind this seam in the consuming shell's
// adapter; this package takes no npm dependency on it and vendors no fake of
// it (src/testing.ts carries a fake of THIS interface, with obviously fake
// values, for tests). Sign-in methods confirmed for the first release:
// Google plus the house sign-in through the Console bridge; device unlock is
// in scope (src/lock.ts). getSession is the reload's re-sign-in: the
// issuer's own session is an httpOnly cookie script cannot read, so the
// platform answers for the sign-in while this package holds nothing to
// trade.
import { err, ok, type HeaderProvider, type Result } from "@hardscapehouse/api-client";
import type { DeviceLockHandle } from "./lock.js";
import type { SessionStore } from "./session.js";

function asMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
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

/** The OAuth2 providers the first release confirms: Google (the Console bridge carries it). */
export type OAuth2Provider = "google";

/** The account identity, as the adapter resolves it from the platform. */
export interface AccountUser {
  userId: string;
  email: string;
  name: string;
}

/**
 * The Appwrite account surface this package needs, as a narrow injected
 * interface. The consuming shell's adapter maps these onto the Appwrite Web
 * SDK (the one integration point a shell wires, recorded in the README); the
 * SDK's dependency lives there, not here.
 */
export interface AccountSurface {
  /**
   * The house sign-in (email plus password through the Console bridge).
   * Resolves the signed-in user. The platform's own session stays on the
   * issuer side (an httpOnly cookie script cannot read); nothing session
   * shaped is handed to this package but the user.
   */
  createEmailPasswordSession(email: string, password: string): Promise<AccountUser>;
  /** The Google flow; the platform navigates to redirectTo and returns through it. */
  createOAuth2Session(provider: OAuth2Provider, redirectTo: string): Promise<void>;
  /**
   * The current platform session's user, when one exists. The reload's
   * re-sign-in: the issuer's session cookie, unreadable to script, answers
   * here, so the shell can restore a session from the issuer while this
   * package holds nothing to trade.
   */
  getSession(): Promise<AccountUser>;
  /** Mint the short-lived JWT the transport presents; custody holds it in memory only. */
  createJWT(): Promise<{ jwt: string; expiresAtMs: number }>;
  /** End the platform session. */
  deleteSession(): Promise<void>;
}

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
 */
export class AuthController {
  readonly #account: AccountSurface;
  readonly #sessions: SessionStore;
  readonly #lock: DeviceLockHandle | null;
  readonly #signOuts: SignOutCounter;
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
  }) {
    this.#account = deps.account;
    this.#sessions = deps.sessions;
    this.#lock = deps.lock ?? null;
    this.#signOuts = deps.signOuts ?? new SignOutCounter();
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
    let minted: { jwt: string; expiresAtMs: number };
    try {
      minted = await this.#account.createJWT();
    } catch (error) {
      return err(`sign-in failed minting the token: ${asMessage(error)}`);
    }
    if (this.#signOuts.count !== signOuts) {
      return err("sign-in cancelled: sign-out landed while the sign-in was in flight, so nothing was adopted");
    }
    if (this.#signIns !== signIns) return this.#staleSignIn(signIns, user);
    const adopted = this.#sessions.adopt({
      token: minted.jwt,
      tokenExpiresAtMs: minted.expiresAtMs,
      user,
    });
    if (!adopted) return err("sign-in failed: the platform returned an unusable session");
    this.#generation += 1; // the adoption is a structural change: it supersedes anything already in flight
    this.#lock?.disengageUnlessBoundTo(user.userId);
    return ok({ ...user });
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
   * Console bridge, or a resume's minted token), the ERP's adoptSession
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
    let user: AccountUser;
    try {
      user = await this.#account.getSession();
    } catch (error) {
      if (this.#generation === generation) this.#failSignedOut();
      return err(`no platform session to resume: ${asMessage(error)}`);
    }
    if (this.#generation !== generation) {
      return err("resume cancelled: sign-out or another adoption landed while the resume was in flight, so nothing was adopted");
    }
    let minted: { jwt: string; expiresAtMs: number };
    try {
      minted = await this.#account.createJWT();
    } catch (error) {
      if (this.#generation === generation) this.#failSignedOut();
      return err(`the token refresh failed: ${asMessage(error)}`);
    }
    if (this.#generation !== generation) {
      return err("resume cancelled: sign-out or another adoption landed while the resume was in flight, so nothing was adopted");
    }
    const adopted = this.#sessions.adopt({
      token: minted.jwt,
      tokenExpiresAtMs: minted.expiresAtMs,
      user,
    });
    if (!adopted) {
      if (this.#generation === generation) this.#failSignedOut();
      return err("the platform returned an unusable session");
    }
    this.#generation += 1; // the adoption is a structural change: it supersedes anything already in flight
    this.#lock?.disengageUnlessBoundTo(user.userId);
    return ok({ ...user });
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
    this.#sessions.clear();
    this.#lock?.disengage();
  }
}
