// The test entry point for @hardscapehouse/auth, following the api-client precedent
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
  getSessionCount = 0;
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
    this.getSessionCount += 1;
    if (!this.#hasPlatformSession) throw new Error("fake: no platform session");
    return { ...this.user };
  }

  async createJWT(): Promise<{ jwt: string; expiresAtMs: number }> {
    this.jwtCount += 1;
    // Each mint is distinct, so a test can tell a fresh token from the one it
    // replaces; the value stays an obvious fake.
    return { jwt: `${FAKE_JWT}-MINT-${this.jwtCount}`, expiresAtMs: this.#now() + this.jwtLifetimeMs };
  }

  async deleteSession(): Promise<void> {
    this.deleteCount += 1;
    this.#hasPlatformSession = false;
  }
}

/**
 * The completing face of one platform call held open for the test. The
 * deferred surfaces below hand these out, so an in-flight sign-in, resume,
 * or verification can be parked across a sign-out or a lock change and
 * answered afterwards, exactly as a slow platform answers after the user has
 * moved on. All values are fake.
 */
export interface DeferredHandoff<T> {
  readonly promise: Promise<T>;
  /** Complete the call the way the platform would on success. */
  complete(value: T): void;
  /** Complete the call the way the platform would on failure. */
  fail(error: Error): void;
}

export class DeferredCall<T> implements DeferredHandoff<T> {
  readonly promise: Promise<T>;
  #resolve: (value: T) => void = () => {};
  #reject: (error: Error) => void = () => {};

  constructor() {
    this.promise = new Promise<T>((resolve, reject) => {
      this.#resolve = resolve;
      this.#reject = reject;
    });
  }

  /** Complete the call the way the platform would on success. */
  complete(value: T): void {
    this.#resolve(value);
  }

  /** Complete the call the way the platform would on failure. */
  fail(error: Error): void {
    this.#reject(error);
  }
}

/** The AccountSurface methods the deferred account surface holds open. */
export type DeferredMethodName = "createEmailPasswordSession" | "getSession" | "createJWT" | "deleteSession";

/**
 * A deferred fake of THIS package's AccountSurface: the three platform calls
 * the controller's sign-in and resume paths make are handed to the test and
 * do not resolve until the test completes them. This is the fake the
 * in-flight races run against: begin a resume, sign out, then let the
 * platform answer late, and assert the custody adopted nothing. All values
 * are fake. By default deleteSession resolves immediately (the sign-out is
 * the event under test, never the deferred leg); with deferDeleteSession the
 * platform session delete is handed to the test as well, so a sign-in or a
 * resume can be parked against a sign-out that is itself mid await. The
 * OAuth2 ceremony is not deferred.
 */
export class DeferredAccountSurface implements AccountSurface {
  user: AccountUser;
  deleteCount = 0;
  #createEmailPasswordSession: DeferredCall<AccountUser> | null = null;
  #getSession: DeferredCall<AccountUser> | null = null;
  #createJWT: DeferredCall<{ jwt: string; expiresAtMs: number }> | null = null;
  #deleteSession: DeferredCall<void> | null = null;
  #deferDeleteSession: boolean;

  constructor(options?: { user?: AccountUser; deferDeleteSession?: boolean }) {
    this.user = options?.user ?? { ...FAKE_ACCOUNT };
    this.#deferDeleteSession = options?.deferDeleteSession ?? false;
  }

  /** The handoff for an in-flight call, or null until that call has been made. */
  handoff(
    name: DeferredMethodName,
  ): DeferredHandoff<AccountUser | { jwt: string; expiresAtMs: number } | void> | null {
    if (name === "createEmailPasswordSession") return this.#createEmailPasswordSession;
    if (name === "getSession") return this.#getSession;
    if (name === "deleteSession") return this.#deleteSession;
    return this.#createJWT;
  }

  async createEmailPasswordSession(_email: string, _password: string): Promise<AccountUser> {
    const call = new DeferredCall<AccountUser>();
    this.#createEmailPasswordSession = call;
    return call.promise;
  }

  async createOAuth2Session(_provider: OAuth2Provider, _redirectTo: string): Promise<void> {
    throw new Error("fake: the deferred fake defers no OAuth2 ceremony");
  }

  async getSession(): Promise<AccountUser> {
    const call = new DeferredCall<AccountUser>();
    this.#getSession = call;
    return call.promise;
  }

  async createJWT(): Promise<{ jwt: string; expiresAtMs: number }> {
    const call = new DeferredCall<{ jwt: string; expiresAtMs: number }>();
    this.#createJWT = call;
    return call.promise;
  }

  async deleteSession(): Promise<void> {
    this.deleteCount += 1;
    if (!this.#deferDeleteSession) return;
    const call = new DeferredCall<void>();
    this.#deleteSession = call;
    return call.promise;
  }
}

/**
 * A deferred fake of THIS package's CredentialProvider: the platform
 * verification is handed to the test and does not resolve until the test
 * completes it, so an unlock attempt can be parked across a lock() or a
 * disengage() and answered afterwards, and two attempts can be held in
 * flight at once (every verify appends a handoff to `pending`). create()
 * and isAvailable() resolve immediately; every value is fake.
 */
export class DeferredCredentialProvider implements CredentialProvider {
  /** One handoff per verify call, in call order. */
  readonly pending: DeferredHandoff<void>[] = [];

  creationCount = 0;
  verificationCount = 0;
  #createdCredentialId: string | null = null;

  get credentialId(): string | null {
    return this.#createdCredentialId;
  }

  async isAvailable(): Promise<boolean> {
    return true;
  }

  async create(): Promise<string> {
    this.creationCount += 1;
    this.#createdCredentialId = `fake-credential-id-${this.creationCount}`;
    return this.#createdCredentialId;
  }

  async verify(_credentialId: string): Promise<void> {
    this.verificationCount += 1;
    const call = new DeferredCall<void>();
    this.pending.push(call);
    return call.promise;
  }
}
