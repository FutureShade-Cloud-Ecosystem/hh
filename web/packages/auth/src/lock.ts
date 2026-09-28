// Device unlock for shared devices: a cached session on a shared device is
// locked behind the platform's user verification (the PIN or the biometric
// the platform authenticator collects), through WebAuthn where the platform
// exposes it. The lock records who unlocked and when, the header provider in
// client.ts stamps that into the audit header on every request made under
// the unlock, and failed attempts are rate-limited locally with exponential
// backoff, in memory (a reload clears the backoff and re-locks, which is the
// honest trade: the compensating control is that a reload also ends any
// open unlock).
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
// throw-safe mirror the session uses (who it protects, which credential
// unlocks it, when it was engaged). An OPEN unlock does not survive a
// reload: a reload re-locks. The mirror payload carries no token and no
// secret; there is no PIN in this package's state at all.
import { err, ok, type Result } from "../../api-client/src/result.js";
import { ThrowSafeMirror } from "./session.js";

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

export interface OpenUnlock {
  userId: string;
  unlockedAtMs: number;
}

/** The audit header carrying who the open unlock belongs to. */
export const AUDIT_USER_HEADER = "X-Unlocked-By";
/** The audit header carrying the unlock instant, as unix epoch milliseconds. */
export const AUDIT_AT_HEADER = "X-Unlocked-At";

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
 * The device lock. engage() registers the platform credential and writes the
 * engaged state to the mirror; unlock() runs one verification, rate-limited
 * on failure; lock() re-engages; disengage() removes the lock. The open
 * unlock lives in memory only, and auditHeaders() returns the audit pair
 * exactly while a lock is open (the client's header provider merges it).
 */
export class DeviceLock {
  private readonly credentials: CredentialProvider;
  private readonly mirror: ThrowSafeMirror;
  private readonly now: () => number;
  private readonly backoffBaseMs: number;
  private readonly backoffCapMs: number;
  private engaged: LockMirrorPayload | null = null;
  private open: OpenUnlock | null = null;
  private failures = 0;
  private backoffUntilMs = 0;

  constructor(options: {
    credentials: CredentialProvider;
    mirror?: ThrowSafeMirror;
    now?: () => number;
    backoffBaseMs?: number;
    backoffCapMs?: number;
  }) {
    this.credentials = options.credentials;
    this.mirror = options?.mirror ?? new ThrowSafeMirror(LOCK_MIRROR_KEY);
    this.now = options.now ?? Date.now;
    this.backoffBaseMs = options.backoffBaseMs ?? 1_000;
    this.backoffCapMs = options.backoffCapMs ?? 60_000;
    const restored = parseLockPayload(this.mirror.read());
    if (restored !== null) {
      // The engaged lock survives a reload; the open unlock does not.
      this.engaged = restored;
    } else {
      this.mirror.clear();
    }
  }

  get state(): LockState {
    if (this.engaged === null) return "absent";
    return this.open === null ? "locked" : "open";
  }

  get openUnlock(): OpenUnlock | null {
    return this.open === null ? null : { ...this.open };
  }

  /** True when the lock's mirror is degraded: a reload forgets the lock entirely. */
  get degraded(): boolean {
    return this.mirror.degraded;
  }

  /** Register the platform credential and engage the lock for the named user's cached session. */
  async engage(userId: string): Promise<Result<void>> {
    if (this.engaged !== null) {
      return err("device unlock is already engaged on this device: disengage it first");
    }
    if (typeof userId !== "string" || userId === "") {
      return err("device unlock needs the user id of the session it protects");
    }
    let available: boolean;
    try {
      available = await this.credentials.isAvailable();
    } catch (error) {
      return err(`device unlock could not ask the platform: ${asMessage(error)}`);
    }
    if (!available) {
      return err("device unlock is unavailable: no platform credential is available on this device");
    }
    let credentialId: string;
    try {
      credentialId = await this.credentials.create();
    } catch (error) {
      return err(`device unlock could not create the credential: ${asMessage(error)}`);
    }
    this.engaged = { v: 1, userId, credentialId, engagedAtMs: this.now() };
    this.mirror.write(this.engaged);
    return ok(undefined);
  }

  /**
   * Run one unlock attempt. A failed attempt (wrong PIN, cancelled prompt,
   * platform refusal) feeds an in-memory exponential backoff before the next
   * attempt is even tried: one second after the first failure, doubling, and
   * capped. A success resets the backoff and records who unlocked and when.
   */
  async unlock(): Promise<Result<OpenUnlock>> {
    if (this.engaged === null) {
      return err("no device lock is engaged on this device");
    }
    if (this.open !== null) {
      return ok({ ...this.open }); // idempotent while open
    }
    const nowMs = this.now();
    if (nowMs < this.backoffUntilMs) {
      return err(`device unlock is rate limited: try again in ${this.backoffUntilMs - nowMs} ms`);
    }
    try {
      await this.credentials.verify(this.engaged.credentialId);
    } catch (error) {
      this.failures += 1;
      const delayMs = Math.min(this.backoffBaseMs * 2 ** (this.failures - 1), this.backoffCapMs);
      this.backoffUntilMs = nowMs + delayMs;
      return err(`device unlock failed: ${asMessage(error)} (failed attempts: ${this.failures})`);
    }
    this.failures = 0;
    this.backoffUntilMs = 0;
    this.open = { userId: this.engaged.userId, unlockedAtMs: this.now() };
    return ok({ ...this.open });
  }

  /** Re-engage an open lock. The engaged credential and its mirror entry stay; the recorded unlock and the backoff are dropped. */
  lock(): void {
    this.open = null;
    this.failures = 0;
    this.backoffUntilMs = 0;
  }

  /** Remove the lock entirely: memory and mirror. */
  disengage(): void {
    this.engaged = null;
    this.open = null;
    this.failures = 0;
    this.backoffUntilMs = 0;
    this.mirror.clear();
  }

  /** The audit headers, exactly while a lock is open: empty otherwise. */
  auditHeaders(): Record<string, string> {
    if (this.open === null) return {};
    return {
      [AUDIT_USER_HEADER]: this.open.userId,
      [AUDIT_AT_HEADER]: String(this.open.unlockedAtMs),
    };
  }
}
