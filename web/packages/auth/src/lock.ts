// Device unlock for shared devices: a courtesy that hides the screen behind
// the platform's user verification (the PIN or the biometric the platform
// authenticator collects), through WebAuthn where the platform exposes it.
//
// THE LOCK IS NOT A SECURITY BOUNDARY, AND THIS PACKAGE NEVER CLAIMS IT IS
// ONE. The token is presented for every request whether the lock is open,
// locked, or absent, because the request must still work while the screen is
// hidden. The lock sends no audit pair: the X-Unlocked-By and X-Unlocked-At
// headers are removed until the core can verify an unlock. Nothing in core/
// reads that pair, the authz middleware checks only Authorization and X-Org,
// and the platform authenticator proves that someone who knows the device's
// own lock was present, not which HH person it was. A header that claims a
// fact the server cannot check poisons the audit trail the moment anything
// trusts it. The README carries the recorded ruling on the lock: the
// courtesy lock ships for the first release, and what a real per-person
// unlock needs is written there beside it, with its trigger.
//
// engage() takes no identity from its caller. It binds to the signed-in
// user's id, read from the custody (`sessions.current`), and no name is
// stored anywhere: the mirror payload carries the bound user id and the
// credential id, never a display name.
//
// The platform seam is the narrow CredentialProvider below (isAvailable,
// create, verify): the same injected-interface discipline the api-client
// uses for its transport. webAuthnCredentialProvider() is the platform
// adapter a consuming shell wires; src/testing.ts carries the fake provider
// the tests run against. The package never touches a typed PIN: the PIN and
// the biometric are the platform prompt's user verification, collected by
// the platform, and the fake models the user's typing with present().
//
// Custody discipline: the engaged lock survives a reload through the same
// throw-safe mirror the session uses (which credential unlocks it, and the
// id of the signed-in user who engaged it). An OPEN unlock does not survive
// a reload: a reload re-locks. The mirror payload carries no token and no
// secret; there is no PIN in this package's state at all. The lock's custody
// follows the person (Colton's ruling C, relay message 048: the lock is
// yours while you are signed in): signOut() disengages it, a different user
// signing in disengages it before the new session is shown, a prompt that
// answers after its owner signed out stores nothing and opens nothing, and
// a lock found at start-up with no session is cleared, not shown.
import { err, ok, type Result } from "@hh/api-client";
import { SignOutCounter } from "./client.js";
import { ThrowSafeMirror } from "./session.js";
import type { SessionStore } from "./session.js";

/**
 * The narrow credential surface this package needs, modelled on what the
 * WebAuthn platform ceremony actually provides: availability, one
 * registration, one verification. verify receives the credential id the
 * lock holds (from engage, or restored from the mirror after a reload) so
 * the assertion targets THAT credential and nobody else's.
 */
export interface CredentialProvider {
  /** Whether the platform can verify a user on this device right now. */
  isAvailable(): Promise<boolean>;
  /** Register the device credential at lock setup; resolves the credential id. */
  create(): Promise<string>;
  /** Run one user verification against the named credential. Resolves on success, rejects on failure (a wrong PIN, a cancelled prompt, no credential). */
  verify(credentialId: string): Promise<void>;
}

function asMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function newChallengeBytes(): Uint8Array<ArrayBuffer> {
  const bytes = new Uint8Array(32);
  crypto.getRandomValues(bytes);
  return bytes;
}

function toBase64Url(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function fromBase64Url(text: string): Uint8Array<ArrayBuffer> {
  const standard = text.replace(/-/g, "+").replace(/_/g, "/");
  const padded = standard + "=".repeat((4 - (standard.length % 4)) % 4);
  const binary = atob(padded);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

export interface WebAuthnProviderOptions {
  /** The relying party id; defaults to the serving origin when omitted. */
  rpId?: string;
  rpName?: string;
  /** How long the platform prompt may take, in milliseconds. */
  timeoutMs?: number;
}

/**
 * The platform adapter a consuming shell wires (the one place WebAuthn is
 * touched). The ceremony's point here is user verification: the platform
 * authenticator proves a local human (PIN or biometric, its choice of
 * factor) by asserting the registered discoverable credential. This is a
 * device-local unlock, not a server-authenticated factor: the JWT and the
 * server remain the authority, and the assertion is not sent anywhere.
 */
export function webAuthnCredentialProvider(options?: WebAuthnProviderOptions): CredentialProvider {
  const rpName = options?.rpName ?? "Hardscape House";
  const timeoutMs = options?.timeoutMs ?? 60_000;
  return {
    async isAvailable(): Promise<boolean> {
      try {
        if (typeof PublicKeyCredential === "undefined") return false;
        const available = await PublicKeyCredential.isUserVerifyingPlatformAuthenticatorAvailable();
        return available === true;
      } catch {
        return false;
      }
    },
    async create(): Promise<string> {
      const credential = await navigator.credentials.create({
        publicKey: {
          challenge: newChallengeBytes(),
          rp: { name: rpName, ...(options?.rpId !== undefined ? { id: options.rpId } : {}) },
          user: {
            id: newChallengeBytes(),
            name: "device-unlock",
            displayName: "Device unlock",
          },
          pubKeyCredParams: [
            { type: "public-key", alg: -7 },
            { type: "public-key", alg: -257 },
          ],
          authenticatorSelection: {
            authenticatorAttachment: "platform",
            residentKey: "required",
            userVerification: "required",
          },
          timeout: timeoutMs,
          attestation: "none",
        },
      });
      if (credential === null) throw new Error("the platform returned no credential");
      // A publicKey request resolves a PublicKeyCredential; the base interface
      // does not carry rawId, so narrow it explicitly.
      const rawId = (credential as PublicKeyCredential).rawId;
      if (!(rawId instanceof ArrayBuffer) || rawId.byteLength === 0) {
        throw new Error("the platform returned a credential without an id");
      }
      return toBase64Url(new Uint8Array(rawId));
    },
    async verify(credentialId: string): Promise<void> {
      const assertion = await navigator.credentials.get({
        publicKey: {
          challenge: newChallengeBytes(),
          allowCredentials: [{ id: fromBase64Url(credentialId), type: "public-key" }],
          userVerification: "required",
          timeout: timeoutMs,
        },
      });
      if (assertion === null) throw new Error("the platform returned no assertion");
    },
  };
}

export type LockState = "absent" | "locked" | "open";

/**
 * The narrow handle the controller takes on the device lock, so a sign-out
 * and an adoption can disengage it without the controller ever touching the
 * lock's state directly (the lock's own generation guard does the work,
 * sending any platform prompt still in flight away empty handed). DeviceLock
 * satisfies it structurally; the shell wires the SAME lock object into the
 * controller and keeps it as the lock, so both see one mirror and one
 * generation (Colton's ruling C, relay message 048).
 */
export interface DeviceLockHandle {
  /** Remove the lock entirely, memory and mirror: the sign-out path. */
  disengage(): void;
  /**
   * The adoption reconciliation: keep the lock only while it is bound to
   * the user the controller just adopted; disengage it when custody adopted
   * somebody else, before the new session is shown. A lock for nobody is a
   * no-op.
   */
  disengageUnlessBoundTo(userId: string): void;
}

interface LockMirrorPayload {
  v: 1;
  userId: string;
  credentialId: string;
  engagedAtMs: number;
}

export const LOCK_MIRROR_KEY = "hh-auth.lock.v1";

// A payload that validates but carries unknown extra fields (a leftover from
// an older version) reports clean=false so the caller can rewrite the stored
// payload down to the known fields; the lock's mirror carries the bound user
// id, the credential id, and the engage instant, and nothing else.
function parseLockPayload(raw: unknown): { payload: LockMirrorPayload; clean: boolean } | null {
  if (raw === null || typeof raw !== "object" || Array.isArray(raw)) return null;
  const candidate = raw as Record<string, unknown>;
  if (candidate["v"] !== 1) return null;
  if (typeof candidate["userId"] !== "string" || candidate["userId"] === "") return null;
  if (typeof candidate["credentialId"] !== "string" || candidate["credentialId"] === "") return null;
  if (typeof candidate["engagedAtMs"] !== "number" || !Number.isFinite(candidate["engagedAtMs"])) return null;
  return {
    payload: {
      v: 1,
      userId: candidate["userId"],
      credentialId: candidate["credentialId"],
      engagedAtMs: candidate["engagedAtMs"],
    },
    clean: Object.keys(candidate).length === 4,
  };
}

/**
 * The device lock: a courtesy that hides the screen. engage() registers the
 * platform credential for the signed-in user and writes the engaged state to
 * the mirror; unlock() runs one verification, rate-limited on failure;
 * lock() re-engages; disengage() removes the lock. The open unlock lives in
 * memory only and re-locks itself when its open window elapses (absolute
 * time from the unlock, not idle time). No method here returns or sends an
 * identity claim: the audit pair is gone until the core can verify an
 * unlock.
 *
 * The generation guard: every structural change (an engage completing, a
 * lock, a disengage) and every unlock attempt bumps a counter. An attempt
 * that awaits the platform captures the counter first and re-checks it after
 * the await; on a mismatch it adopts nothing (no open unlock, no backoff
 * from a superseded failure) and resolves an error Result, so a lock or
 * disengage during a verification in flight ends closed or absent, and of
 * two concurrent attempts only the newer one's outcome lands. The session
 * itself is read again after each await too, on the success path and on the
 * failure path alike: another person's sign-in mid prompt leaves the custody
 * holding a different user, and the custody comparison refuses to engage
 * for, or open the lock for, or record a failure against, the person who is
 * now signed in instead of the person these methods are bound to. The bound
 * user's own token moving underneath is not a change of person: a token
 * refreshed or expired mid prompt leaves the custody holding either no
 * session at all or the same user's fresh session, and both are accepted.
 *
 * The sign-out is different from the expiry, and the difference is an act
 * (Colton's ruling C, relay message 048: the lock is yours while you are
 * signed in; a lock belongs to the signed-in person, or to nobody). A
 * sign-out is counted by the sign-out counter (SignOutCounter, shared with
 * the controller): engage and unlock capture that count before they await
 * the platform and refuse when it has moved, storing nothing and opening
 * nothing, because the person these methods were bound to signed out while
 * the prompt was open. An expiry is not an act: the counter does not move,
 * and the prompt's answer still lands. The controller's signOut() also
 * disengages the lock outright beside the custody it clears, and its
 * adoption paths call disengageUnlessBoundTo() with the user they adopted,
 * so a different user signing in clears the lock before the new session is
 * shown while the same user signing in again keeps theirs. At start-up a
 * lock in the mirror whose person's display profile is gone (or is somebody
 * else's) is cleared, not shown: the profile survives a reload and dies at
 * a sign-out, so a lock without one is a lock whose session ended.
 */
export class DeviceLock {
  readonly #credentials: CredentialProvider;
  /** The custody the lock binds to. engage() protects the signed-in user's session, never a caller-named identity. */
  readonly #sessions: Pick<SessionStore, "current" | "profile">;
  readonly #mirror: ThrowSafeMirror;
  readonly #now: () => number;
  readonly #backoffBaseMs: number;
  readonly #backoffCapMs: number;
  readonly #openWindowMs: number;
  /** The sign-out counter shared with the controller; a private one nobody bumps when the shell omits it. */
  readonly #signOuts: SignOutCounter;
  #generation = 0;
  #engaged: LockMirrorPayload | null = null;
  #openAtMs: number | null = null;
  #failures = 0;
  #backoffUntilMs = 0;

  constructor(options: {
    credentials: CredentialProvider;
    sessions: Pick<SessionStore, "current" | "profile">;
    mirror?: ThrowSafeMirror;
    now?: () => number;
    backoffBaseMs?: number;
    backoffCapMs?: number;
    /**
     * The sign-out counter the controller bumps, shared by instance. A
     * sign-out is an act the lock must see across its prompts (Colton's
     * ruling C, relay message 048); without it the lock sees no sign-out
     * and the expiry rule governs its prompts alone.
     */
    signOuts?: SignOutCounter;
    /**
     * Configuration, not security: the screen hides again this many
     * milliseconds after the unlock, ABSOLUTELY. No activity is tracked and
     * nothing extends the window; it is an open window measured from the
     * unlock, not an idle timeout.
     */
    openWindowMs?: number;
  }) {
    this.#credentials = options.credentials;
    this.#sessions = options.sessions;
    this.#mirror = options?.mirror ?? new ThrowSafeMirror(LOCK_MIRROR_KEY);
    this.#now = options.now ?? Date.now;
    this.#backoffBaseMs = options.backoffBaseMs ?? 1_000;
    this.#backoffCapMs = options.backoffCapMs ?? 60_000;
    this.#openWindowMs = options.openWindowMs ?? 300_000;
    this.#signOuts = options?.signOuts ?? new SignOutCounter();
    const restored = parseLockPayload(this.#mirror.read());
    if (restored !== null) {
      // The engaged lock survives a reload; the open unlock does not. A
      // payload with unknown extra fields is rewritten to the known fields.
      // First the ruling's start-up check: the lock belongs to the
      // signed-in person, or to nobody. The display profile survives a
      // reload and dies at a sign-out (clear wipes it), so a lock in the
      // mirror whose profile is gone, or names somebody else, is a lock
      // whose session ended: cleared from the mirror, not shown.
      const profile = this.#sessions.profile;
      if (profile === null || profile.userId !== restored.payload.userId) {
        this.#engaged = null;
        this.#mirror.clear();
      } else {
        this.#engaged = restored.payload;
        if (!restored.clean) this.#mirror.write(this.#engaged);
      }
    } else {
      this.#mirror.clear();
    }
  }

  get state(): LockState {
    if (this.#engaged === null) return "absent";
    this.#relockAfterWindow();
    return this.#openAtMs === null ? "locked" : "open";
  }

  /** True when the lock's mirror is degraded: a reload forgets the lock entirely. */
  get degraded(): boolean {
    return this.#mirror.degraded;
  }

  /** Close the open unlock once its open window has elapsed (absolute from the unlock; no activity extends it). */
  #relockAfterWindow(): void {
    if (this.#openAtMs === null) return;
    if (this.#now() - this.#openAtMs >= this.#openWindowMs) this.#openAtMs = null;
  }

  /**
   * True when the custody holds no session, or holds one for the user engage
   * or unlock is bound to. The custody is read fresh at every call: engage
   * and unlock await a platform prompt that can stay open across a sign-out
   * and another person's sign-in, and neither the lock's state nor the
   * prompt nor a failure's backoff may land for a user other than the one
   * these methods are bound to. The bound user's own token moving underneath
   * them is not a change of person, so it is not a refusal: a token
   * refreshed mid prompt (a new token, the same user) leaves the custody
   * holding the same user's session, and a token that expires mid prompt
   * leaves it holding no session at all, and both are accepted. What is
   * refused is a session for a different user, which is what another
   * person's sign-in during the prompt leaves behind. A sign-out during the
   * prompt is not this check's case any more (Colton's ruling C, relay
   * message 048): the sign-out counter check that runs beside this one
   * refuses the prompt whose person signed out, and signOut() itself
   * disengages the lock, so the acceptance of an absent custody is the
   * expiry-shaped case only (no act, no counter movement).
   */
  #sessionIsAbsentOrFor(userId: string): boolean {
    const current = this.#sessions.current;
    return current === null || current.user.userId === userId;
  }

  /**
   * Register the platform credential and engage the lock for the signed-in
   * user's session. Takes no identity argument on purpose: the user id comes
   * from custody, so the lock cannot be pointed at a name the caller made
   * up. Refused when no live session is held, when the platform offers no
   * credential (WebAuthn absent), or when a lock is already engaged. The
   * generation guard runs across every await: a disengage or another
   * structural change that lands mid engage cancels it as data and the
   * mirror is written by neither path. The custody is read again after each
   * await as well: another person's sign-in while the platform's credential
   * prompt is open leaves the custody holding a different user, and the
   * engage refuses instead of storing the lock for the person who is no
   * longer signed in. The sign-out counter is captured before the first
   * await and re-read after each: the person it saw signed in signing out
   * mid prompt is an act the counter counts (Colton's ruling C, relay
   * message 048), and the engage stores nothing when it has moved, even in
   * the window where the sign-out's own await is still pending and the
   * custody has not been cleared yet. The bound user's own token refreshing
   * or expiring mid prompt moves no counter and still engages.
   */
  async engage(): Promise<Result<void>> {
    if (this.#engaged !== null) {
      return err("device unlock is already engaged on this device: disengage it first");
    }
    const session = this.#sessions.current;
    if (session === null) {
      return err("device unlock engages only for a signed-in user: no live session is held");
    }
    const generation = this.#generation;
    const signOuts = this.#signOuts.count;
    let available: boolean;
    try {
      available = await this.#credentials.isAvailable();
    } catch (error) {
      return err(`device unlock could not ask the platform: ${asMessage(error)}`);
    }
    if (this.#signOuts.count !== signOuts) {
      return err("device unlock is bound to the person it saw signed in, and that person signed out while engage was in flight, so nothing was engaged");
    }
    if (this.#generation !== generation) {
      return err("device unlock was superseded while engage was in flight, so nothing was engaged");
    }
    if (!this.#sessionIsAbsentOrFor(session.user.userId)) {
      return err("device unlock engages only for the user it saw signed in when it began: the session changed while engage was in flight, so nothing was engaged");
    }
    if (!available) {
      return err("device unlock is unavailable: no platform credential is available on this device");
    }
    let credentialId: string;
    try {
      credentialId = await this.#credentials.create();
    } catch (error) {
      return err(`device unlock could not create the credential: ${asMessage(error)}`);
    }
    if (this.#signOuts.count !== signOuts) {
      return err("device unlock is bound to the person it saw signed in, and that person signed out while engage was in flight, so nothing was engaged");
    }
    if (this.#generation !== generation) {
      return err("device unlock was superseded while engage was in flight, so nothing was engaged");
    }
    if (!this.#sessionIsAbsentOrFor(session.user.userId)) {
      return err("device unlock engages only for the user it saw signed in when it began: the session changed while engage was in flight, so nothing was engaged");
    }
    this.#generation += 1;
    this.#engaged = { v: 1, userId: session.user.userId, credentialId, engagedAtMs: this.#now() };
    this.#openAtMs = null;
    this.#failures = 0;
    this.#backoffUntilMs = 0;
    this.#mirror.write(this.#engaged);
    return ok(undefined);
  }

  /**
   * Run one unlock attempt. A failed attempt (wrong PIN, cancelled prompt,
   * platform refusal) feeds an in-memory exponential backoff before the next
   * attempt is even tried: one second after the first failure, doubling, and
   * capped. A success resets the backoff and opens the lock. The backoff
   * lives in memory, so a reload clears it and re-locks: it is a courtesy
   * against fat fingers, not a control. The platform authenticator's own
   * lockout is the real limiter.
   *
   * The attempt binds to the user who engaged: a live session belonging to a
   * different user is refused before the platform is asked. The generation
   * guard runs across the await: a lock, a disengage, or a newer attempt
   * that starts mid verification supersedes this one, and on a mismatch it
   * adopts nothing (no open unlock, and no backoff recorded from a failure
   * that no longer matters). The custody is read again after the prompt, on
   * the success path and on the failure path alike: another person's
   * sign-in while the platform prompt was open leaves the custody holding a
   * different user, and the attempt refuses instead of opening the lock
   * for, or recording a failure's backoff against, the person who now is
   * signed in. The sign-out counter is captured before the prompt and
   * re-read after it, on the success path and the failure path alike: the
   * person who engaged signing out mid prompt is an act the counter counts
   * (Colton's ruling C, relay message 048), the lock is already gone by
   * then (signOut() disengages it), and the attempt stores nothing and
   * opens nothing even in the window where the sign-out's own await is
   * still pending. The bound user's own token moving underneath is not a
   * refusal: a token refreshed or expired mid prompt moves no counter and
   * leaves the custody holding no session at all or the same user's fresh
   * session, and both are accepted.
   */
  async unlock(): Promise<Result<void>> {
    if (this.#engaged === null) {
      return err("no device lock is engaged on this device");
    }
    this.#relockAfterWindow();
    if (this.#openAtMs !== null) {
      return ok(undefined); // idempotent while open
    }
    const session = this.#sessions.current;
    if (session !== null && session.user.userId !== this.#engaged.userId) {
      return err("device unlock binds to the user who engaged it: this session belongs to a different user");
    }
    const nowMs = this.#now();
    if (nowMs < this.#backoffUntilMs) {
      return err(`device unlock is rate limited: try again in ${this.#backoffUntilMs - nowMs} ms`);
    }
    const generation = ++this.#generation;
    const signOuts = this.#signOuts.count;
    const credentialId = this.#engaged.credentialId;
    try {
      await this.#credentials.verify(credentialId);
    } catch (error) {
      if (this.#signOuts.count !== signOuts) {
        return err("device unlock is bound to the person it saw signed in, and that person signed out while the platform prompt was in flight, so nothing was recorded");
      }
      if (this.#generation !== generation || this.#engaged === null) {
        return err("device unlock was superseded while the platform prompt was in flight");
      }
      // The failure path carries the same session rule as the success path,
      // so the rule has no exception: a failed attempt records its backoff
      // only while the custody holds the engaged user's session, or no
      // session at all. A different user's session landed mid prompt records
      // nothing.
      if (!this.#sessionIsAbsentOrFor(this.#engaged.userId)) {
        return err("device unlock binds to the user it saw signed in before the prompt: the session changed while the platform prompt was in flight, so nothing was recorded");
      }
      this.#failures += 1;
      const delayMs = Math.min(this.#backoffBaseMs * 2 ** (this.#failures - 1), this.#backoffCapMs);
      this.#backoffUntilMs = this.#now() + delayMs;
      return err(`device unlock failed: ${asMessage(error)} (failed attempts: ${this.#failures})`);
    }
    if (this.#signOuts.count !== signOuts) {
      return err("device unlock is bound to the person it saw signed in, and that person signed out while the platform prompt was in flight, so nothing opened");
    }
    if (this.#generation !== generation || this.#engaged === null) {
      return err("device unlock was superseded while the platform prompt was in flight");
    }
    if (!this.#sessionIsAbsentOrFor(this.#engaged.userId)) {
      return err("device unlock binds to the user it saw signed in before the prompt: the session changed while the platform prompt was in flight, so nothing opened");
    }
    this.#failures = 0;
    this.#backoffUntilMs = 0;
    this.#openAtMs = this.#now();
    return ok(undefined);
  }

  /**
   * Re-engage an open lock. The engaged credential and its mirror entry
   * stay; the open unlock and the backoff are dropped. On an already locked
   * lock it does nothing, so it cannot clear a failure backoff, and the
   * generation bump sends any verification still in flight away empty
   * handed: a lock during an unlock in flight ends locked.
   */
  lock(): void {
    // A verification still in flight is superseded whether or not the lock
    // is already closed; the failure backoff of a closed lock is kept.
    this.#generation += 1;
    if (this.#openAtMs === null) return;
    this.#openAtMs = null;
    this.#failures = 0;
    this.#backoffUntilMs = 0;
  }

  /**
   * Remove the lock entirely: memory and mirror. The generation bump sends
   * any verification still in flight away empty handed: a disengage during
   * an unlock in flight ends absent, never open. The platform credential the
   * engage ceremony created stays on the device; the platform owns its
   * deletion.
   */
  disengage(): void {
    this.#generation += 1;
    this.#engaged = null;
    this.#openAtMs = null;
    this.#failures = 0;
    this.#backoffUntilMs = 0;
    this.#mirror.clear();
  }

  /**
   * The adoption reconciliation (Colton's ruling C, relay message 048: the
   * lock is yours while you are signed in; a lock belongs to the signed-in
   * person, or to nobody). The controller calls this with the user of every
   * session it adopts: a lock bound to a DIFFERENT user is disengaged here,
   * before the new session is shown, and the same user signing in again
   * (their own re-sign-in, or their refresh through resume) keeps their
   * lock. A lock for nobody is a no-op.
   */
  disengageUnlessBoundTo(userId: string): void {
    if (this.#engaged === null) return;
    if (this.#engaged.userId === userId) return;
    this.disengage();
  }

  /**
   * Serialises to nothing secret: the state and the degradation flag. No
   * identity claim, no credential id, no unlock instant.
   */
  toJSON(): { state: LockState; degraded: boolean } {
    return { state: this.state, degraded: this.#mirror.degraded };
  }
}
