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
// trusts it. The README carries the open question for Colton: what a real
// per-person unlock needs.
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
// secret; there is no PIN in this package's state at all.
import { err, ok, type Result } from "../../api-client/src/result.js";
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

interface LockMirrorPayload {
  v: 1;
  userId: string;
  credentialId: string;
  engagedAtMs: number;
}

export const LOCK_MIRROR_KEY = "hh-auth.lock.v1";

function parseLockPayload(raw: unknown): LockMirrorPayload | null {
  if (raw === null || typeof raw !== "object" || Array.isArray(raw)) return null;
  const candidate = raw as Record<string, unknown>;
  if (candidate["v"] !== 1) return null;
  if (typeof candidate["userId"] !== "string" || candidate["userId"] === "") return null;
  if (typeof candidate["credentialId"] !== "string" || candidate["credentialId"] === "") return null;
  if (typeof candidate["engagedAtMs"] !== "number" || !Number.isFinite(candidate["engagedAtMs"])) return null;
  return {
    v: 1,
    userId: candidate["userId"],
    credentialId: candidate["credentialId"],
    engagedAtMs: candidate["engagedAtMs"],
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
 */
export class DeviceLock {
  readonly #credentials: CredentialProvider;
  /** The custody the lock binds to. engage() protects the signed-in user's session, never a caller-named identity. */
  readonly #sessions: Pick<SessionStore, "current">;
  readonly #mirror: ThrowSafeMirror;
  readonly #now: () => number;
  readonly #backoffBaseMs: number;
  readonly #backoffCapMs: number;
  readonly #openWindowMs: number;
  #engaged: LockMirrorPayload | null = null;
  #openAtMs: number | null = null;
  #failures = 0;
  #backoffUntilMs = 0;

  constructor(options: {
    credentials: CredentialProvider;
    sessions: Pick<SessionStore, "current">;
    mirror?: ThrowSafeMirror;
    now?: () => number;
    backoffBaseMs?: number;
    backoffCapMs?: number;
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
    const restored = parseLockPayload(this.#mirror.read());
    if (restored !== null) {
      // The engaged lock survives a reload; the open unlock does not.
      this.#engaged = restored;
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
   * Register the platform credential and engage the lock for the signed-in
   * user's session. Takes no identity argument on purpose: the user id comes
   * from custody, so the lock cannot be pointed at a name the caller made
   * up. Refused when no live session is held, when the platform offers no
   * credential (WebAuthn absent), or when a lock is already engaged.
   */
  async engage(): Promise<Result<void>> {
    if (this.#engaged !== null) {
      return err("device unlock is already engaged on this device: disengage it first");
    }
    const session = this.#sessions.current;
    if (session === null) {
      return err("device unlock engages only for a signed-in user: no live session is held");
    }
    let available: boolean;
    try {
      available = await this.#credentials.isAvailable();
    } catch (error) {
      return err(`device unlock could not ask the platform: ${asMessage(error)}`);
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
    this.#engaged = { v: 1, userId: session.user.userId, credentialId, engagedAtMs: this.#now() };
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
   */
  async unlock(): Promise<Result<void>> {
    if (this.#engaged === null) {
      return err("no device lock is engaged on this device");
    }
    this.#relockAfterWindow();
    if (this.#openAtMs !== null) {
      return ok(undefined); // idempotent while open
    }
    const nowMs = this.#now();
    if (nowMs < this.#backoffUntilMs) {
      return err(`device unlock is rate limited: try again in ${this.#backoffUntilMs - nowMs} ms`);
    }
    try {
      await this.#credentials.verify(this.#engaged.credentialId);
    } catch (error) {
      this.#failures += 1;
      const delayMs = Math.min(this.#backoffBaseMs * 2 ** (this.#failures - 1), this.#backoffCapMs);
      this.#backoffUntilMs = nowMs + delayMs;
      return err(`device unlock failed: ${asMessage(error)} (failed attempts: ${this.#failures})`);
    }
    this.#failures = 0;
    this.#backoffUntilMs = 0;
    this.#openAtMs = this.#now();
    return ok(undefined);
  }

  /** Re-engage an open lock. The engaged credential and its mirror entry stay; the open unlock and the backoff are dropped. */
  lock(): void {
    this.#openAtMs = null;
    this.#failures = 0;
    this.#backoffUntilMs = 0;
  }

  /** Remove the lock entirely: memory and mirror. The platform credential the engage ceremony created stays on the device; the platform owns its deletion. */
  disengage(): void {
    this.#engaged = null;
    this.#openAtMs = null;
    this.#failures = 0;
    this.#backoffUntilMs = 0;
    this.#mirror.clear();
  }

  /**
   * Serialises to nothing secret: the state and the degradation flag. No
   * identity claim, no credential id, no unlock instant.
   */
  toJSON(): { state: LockState; degraded: boolean } {
    return { state: this.state, degraded: this.#mirror.degraded };
  }
}
