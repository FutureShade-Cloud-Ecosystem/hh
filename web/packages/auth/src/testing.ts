// The test entry point for @hh/auth, following the api-client precedent
// (web/packages/api-client/src/testing.ts): test doubles and gates data that
// deliberately never ride the public entry (src/index.ts) into a browser
// bundle.
//
// Every value these doubles mint is obviously fake and documented as fake;
// no real credential, token, or secret exists anywhere in this package or
// its fixtures.
import type { AccountSurface, AccountUser, OAuth2Provider } from "./client.js";
import type { CredentialProvider } from "./lock.js";
import type { StorageLike } from "./session.js";

/** The fake PIN the fake credential provider accepts. A fake value for tests only; it unlocks nothing real. */
export const FAKE_PIN = "246813";

/** A fake JWT minted by the fake account surface. Not a token; the shape is all a test needs. */
export const FAKE_JWT = "FAKE.JWT.NOT-A-REAL-TOKEN";

/** The fake account identity the fake account surface signs in. */
export const FAKE_ACCOUNT: AccountUser = {
  userId: "fake-user-1",
  email: "fake.user@example.invalid",
  name: "Fake User",
};

/** A fake storage accessor that works (a Map): the mirror attaches and is not degraded. Every write is recorded for the gates. */
export class RecordedStorage implements StorageLike {
  readonly writes: { key: string; value: string }[] = [];
  readonly #map = new Map<string, string>();

  getItem(key: string): string | null {
    return this.#map.has(key) ? (this.#map.get(key) as string) : null;
  }

  setItem(key: string, value: string): void {
    this.writes.push({ key, value });
    this.#map.set(key, value);
  }

  removeItem(key: string): void {
    this.#map.delete(key);
  }

  get size(): number {
    return this.#map.size;
  }
}

/** A fake storage accessor whose every operation throws: the denied-site-data posture the ERP shim recorded (finding DIB-488 rev 5). */
export class ThrowingStorage implements StorageLike {
  getItem(): string | null {
    throw new Error("simulated: the storage accessor itself threw (denied site data)");
  }

  setItem(): void {
    throw new Error("simulated: the storage accessor itself threw (denied site data)");
  }

  removeItem(): void {
    throw new Error("simulated: the storage accessor itself threw (denied site data)");
  }
}

export interface FakeCredentialProviderOptions {
  /** The PIN the simulated platform accepts; defaults to FAKE_PIN (fake). */
  pin?: string;
  /** Whether the simulated platform offers a credential; default true. */
  available?: boolean;
}

/**
 * The fake credential provider the unlock tests run against (the acceptance
 * line names it). It implements the same narrow contract as the platform
 * WebAuthn adapter: create registers one device credential, verify asserts
 * THAT credential id, and the simulated user's typing is set with present()
 * (a wrong PIN, or null for a cancelled prompt, rejects exactly as the
 * platform's user verification would). All values are fake.
 */
export class FakeCredentialProvider implements CredentialProvider {
  readonly #expectedPin: string;
  readonly #available: boolean;
  #presented: string | null = null;
  #createdCredentialId: string | null = null;

  creationCount = 0;
  verificationCount = 0;
  rejectionCount = 0;

  constructor(options?: FakeCredentialProviderOptions) {
    this.#expectedPin = options?.pin ?? FAKE_PIN;
    this.#available = options?.available ?? true;
  }

  /** What the simulated user types into the next verification; null simulates a cancelled prompt. */
  present(pin: string | null): void {
    this.#presented = pin;
  }

  get credentialId(): string | null {
    return this.#createdCredentialId;
  }

  async isAvailable(): Promise<boolean> {
    return this.#available;
  }

  async create(): Promise<string> {
    if (!this.#available) throw new Error("fake: no platform credential is available");
    this.creationCount += 1;
    this.#createdCredentialId = `fake-credential-id-${this.creationCount}`;
    return this.#createdCredentialId;
  }

  async verify(credentialId: string): Promise<void> {
    this.verificationCount += 1;
    if (this.#createdCredentialId === null) throw new Error("fake: no credential was created");
    if (credentialId !== this.#createdCredentialId) {
      throw new Error("fake: the verification targeted a different credential");
    }
    if (this.#presented !== this.#expectedPin) {
      this.rejectionCount += 1;
      throw new Error("fake: user verification failed (simulated wrong PIN or cancelled prompt)");
    }
  }
}

export interface FakeAccountSurfaceOptions {
  /** The email the fake accepts; defaults to FAKE_ACCOUNT.email (fake). */
  email?: string;
  /** The password the fake accepts; defaults to FAKE_PASSWORD (fake). */
  password?: string;
}

/** The fake password the fake account surface accepts. A fake value for tests only. */
export const FAKE_PASSWORD = "fake-password-not-real";

/**
 * The fake of THIS package's AccountSurface (never a fake of Appwrite
 * itself): three scripted answers with obviously fake values, for the
 * controller and custody tests. The fake keeps no platform session beyond a
 * counter: createEmailPasswordSession flips the counter (the platform's
 * cookie-backed session exists), getSession answers while it does, and
 * deleteSession clears it.
 */
export class FakeAccountSurface implements AccountSurface {
  readonly #acceptedEmail: string;
  readonly #acceptedPassword: string;
  #now: () => number;
  #hasPlatformSession = false;

  creationCount = 0;
  oauthCount = 0;
  deleteCount = 0;
  jwtCount = 0;
  jwtLifetimeMs = 900_000;
  user: AccountUser = { ...FAKE_ACCOUNT };

  constructor(options?: FakeAccountSurfaceOptions) {
    this.#acceptedEmail = options?.email ?? FAKE_ACCOUNT.email;
    this.#acceptedPassword = options?.password ?? FAKE_PASSWORD;
    this.#now = Date.now;
  }

  /** Pin the fake's clock (tests advance it to prove expiry handling). */
  setClock(now: () => number): void {
    this.#now = now;
  }

  async createEmailPasswordSession(email: string, password: string): Promise<AccountUser> {
    if (email !== this.#acceptedEmail || password !== this.#acceptedPassword) {
      throw new Error("fake: invalid credentials");
    }
    this.creationCount += 1;
    this.#hasPlatformSession = true;
    return { ...this.user };
  }

  async createOAuth2Session(provider: OAuth2Provider, redirectTo: string): Promise<void> {
    if (provider !== "google") throw new Error("fake: unsupported provider");
    if (typeof redirectTo !== "string" || redirectTo === "") throw new Error("fake: no redirect target");
    this.oauthCount += 1;
    this.#hasPlatformSession = true;
  }

  async getSession(): Promise<AccountUser> {
    if (!this.#hasPlatformSession) throw new Error("fake: no platform session");
    return { ...this.user };
  }

  async createJWT(): Promise<{ jwt: string; expiresAtMs: number }> {
    this.jwtCount += 1;
    return { jwt: FAKE_JWT, expiresAtMs: this.#now() + this.jwtLifetimeMs };
  }

  async deleteSession(): Promise<void> {
    this.deleteCount += 1;
    this.#hasPlatformSession = false;
  }
}
