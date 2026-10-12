// The test entry point for @hardscapehouse/auth, following the api-client precedent
// (web/packages/api-client/src/testing.ts): test doubles and gates data that
// deliberately never ride the public entry (src/index.ts) into a browser
// bundle.
//
// Every value these doubles mint is obviously fake and documented as fake;
// no real credential, token, or secret exists anywhere in this package or
// its fixtures.
import type {
  AccountSurface,
  AccountUser,
  ExchangeRefusalCode,
  ExchangeResult,
  ExchangeUser,
  OAuth2Provider,
  RefreshTimers,
} from "./client.js";
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

/** The fake exchange user block for an account, in the core's snake case. Fake role values. */
export function fakeExchangeUser(account: AccountUser = FAKE_ACCOUNT, overrides: Partial<ExchangeUser> = {}): ExchangeUser {
  return {
    user_id: account.userId,
    email: account.email,
    display_name: account.name,
    role_code: "fake-role",
    role_name: "Fake Role",
    tier: 1,
    scope: "fake-scope",
    ...overrides,
  };
}

/** A successful fake exchange: the token, its expiry and the user block (fields of the block can be overridden). */
export function fakeExchangeAnswer(
  token: string,
  expiresAtMs: number,
  userOverrides: Partial<ExchangeUser> = {},
  account: AccountUser = FAKE_ACCOUNT,
): ExchangeResult {
  return { ok: true, answer: { token, expiresAtMs, user: fakeExchangeUser(account, userOverrides) } };
}

/** A refused fake exchange, by the shells contract's code. */
export function fakeExchangeRefusal(code: ExchangeRefusalCode): ExchangeResult {
  return { ok: false, code };
}

/**
 * A fake clock and timer set in one: `now` is the clock, `setTimeout` and
 * `clearTimeout` arm timers on it, and `advance(ms)` moves time forward,
 * firing every timer that falls due in order (a timer armed by a firing
 * timer fires too when it falls inside the window) and letting the work each
 * firing starts settle before the next. Nothing waits in real time.
 */
export class FakeScheduler implements RefreshTimers {
  #time: number;
  #nextId = 1;
  readonly #timers = new Map<number, { due: number; callback: () => void }>();

  constructor(startMs: number) {
    this.#time = startMs;
  }

  readonly now = (): number => this.#time;

  setTimeout(callback: () => void, ms: number): unknown {
    const id = this.#nextId;
    this.#nextId += 1;
    this.#timers.set(id, { due: this.#time + ms, callback });
    return id;
  }

  clearTimeout(handle: unknown): void {
    this.#timers.delete(handle as number);
  }

  /** The timers armed and not yet fired or cleared. */
  get pending(): number {
    return this.#timers.size;
  }

  async advance(ms: number): Promise<void> {
    const target = this.#time + ms;
    for (;;) {
      let nextId: number | null = null;
      let nextDue = Infinity;
      for (const [id, timer] of this.#timers) {
        if (timer.due <= target && timer.due < nextDue) {
          nextId = id;
          nextDue = timer.due;
        }
      }
      if (nextId === null) break;
      const timer = this.#timers.get(nextId) as { due: number; callback: () => void };
      this.#timers.delete(nextId);
      this.#time = Math.max(this.#time, timer.due);
      timer.callback();
      await new Promise<void>((resolve) => setImmediate(resolve)); // let the firing's async work settle
    }
    this.#time = target;
  }
}

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
 * itself): scripted answers with obviously fake values, for the controller
 * and custody tests. The fake keeps no platform session beyond a flag:
 * createEmailPasswordSession sets it (the platform's cookie-backed session
 * exists), getSession and exchange answer while it holds, and deleteSession
 * clears it. script() queues refusals or errors for the next exchanges.
 */
export class FakeAccountSurface implements AccountSurface {
  readonly #acceptedEmail: string;
  readonly #acceptedPassword: string;
  #now: () => number;
  #hasPlatformSession = false;

  creationCount = 0;
  oauthCount = 0;
  deleteCount = 0;
  exchangeCount = 0;
  getSessionCount = 0;
  /** The life of the token each fake exchange answers with. */
  tokenLifetimeMs = 900_000;
  /** The organisation of every exchange asked, in order. */
  readonly exchangeOrgs: string[] = [];
  user: AccountUser = { ...FAKE_ACCOUNT };
  readonly #scripted: (ExchangeResult | Error)[] = [];

  constructor(options?: FakeAccountSurfaceOptions) {
    this.#acceptedEmail = options?.email ?? FAKE_ACCOUNT.email;
    this.#acceptedPassword = options?.password ?? FAKE_PASSWORD;
    this.#now = Date.now;
  }

  /** Queue results the next exchanges answer with, in order (an Error is thrown); once empty the fake answers a fresh token. */
  script(...results: (ExchangeResult | Error)[]): void {
    this.#scripted.push(...results);
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

  async exchange(org: string): Promise<ExchangeResult> {
    this.exchangeCount += 1;
    this.exchangeOrgs.push(org);
    const scripted = this.#scripted.shift();
    if (scripted instanceof Error) throw scripted;
    if (scripted !== undefined) return scripted;
    if (!this.#hasPlatformSession) return { ok: false, code: "no_session" };
    // Each exchange is distinct, so a test can tell a fresh token from the one
    // it replaces; the value stays an obvious fake.
    return fakeExchangeAnswer(`${FAKE_JWT}-EX-${this.exchangeCount}`, this.#now() + this.tokenLifetimeMs, {}, this.user);
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
export type DeferredMethodName = "createEmailPasswordSession" | "getSession" | "exchange" | "deleteSession";

/**
 * A deferred fake of THIS package's AccountSurface: the platform calls
 * the controller's sign-in and resume paths make are handed to the test and
 * do not resolve until the test completes them. This is the fake the
 * in-flight races run against: begin a resume, sign out, then let the
 * platform answer late, and assert the custody adopted nothing. Every call
 * of a method is kept, in call order (two and three sign-ins in flight are
 * parked at once), so `handoffs(name)` lists them all and `handoff(name)`
 * returns the latest, or null until that call has been made. All values are
 * fake. By default deleteSession resolves immediately (the sign-out is
 * the event under test, never the deferred leg); with deferDeleteSession the
 * platform session delete is handed to the test as well, so a sign-in or a
 * resume can be parked against a sign-out that is itself mid await. The
 * OAuth2 ceremony is not deferred.
 */
export class DeferredAccountSurface implements AccountSurface {
  user: AccountUser;
  deleteCount = 0;
  readonly #createEmailPasswordSessions: DeferredCall<AccountUser>[] = [];
  readonly #getSessions: DeferredCall<AccountUser>[] = [];
  readonly #exchanges: DeferredCall<ExchangeResult>[] = [];
  readonly #deleteSessions: DeferredCall<void>[] = [];
  #deferDeleteSession: boolean;

  constructor(options?: { user?: AccountUser; deferDeleteSession?: boolean }) {
    this.user = options?.user ?? { ...FAKE_ACCOUNT };
    this.#deferDeleteSession = options?.deferDeleteSession ?? false;
  }

  /** Every handoff made for a method, in call order (an empty array before the first). */
  handoffs(
    name: DeferredMethodName,
  ): DeferredHandoff<AccountUser | ExchangeResult | void>[] {
    if (name === "createEmailPasswordSession") return [...this.#createEmailPasswordSessions];
    if (name === "getSession") return [...this.#getSessions];
    if (name === "deleteSession") return [...this.#deleteSessions];
    return [...this.#exchanges];
  }

  /** The handoff for the latest in-flight call of a method, or null until that call has been made. */
  handoff(
    name: DeferredMethodName,
  ): DeferredHandoff<AccountUser | ExchangeResult | void> | null {
    const all = this.handoffs(name);
    return all.length === 0 ? null : all[all.length - 1];
  }

  async createEmailPasswordSession(_email: string, _password: string): Promise<AccountUser> {
    const call = new DeferredCall<AccountUser>();
    this.#createEmailPasswordSessions.push(call);
    return call.promise;
  }

  async createOAuth2Session(_provider: OAuth2Provider, _redirectTo: string): Promise<void> {
    throw new Error("fake: the deferred fake defers no OAuth2 ceremony");
  }

  async getSession(): Promise<AccountUser> {
    const call = new DeferredCall<AccountUser>();
    this.#getSessions.push(call);
    return call.promise;
  }

  async exchange(_org: string): Promise<ExchangeResult> {
    const call = new DeferredCall<ExchangeResult>();
    this.#exchanges.push(call);
    return call.promise;
  }

  async deleteSession(): Promise<void> {
    this.deleteCount += 1;
    if (!this.#deferDeleteSession) return;
    const call = new DeferredCall<void>();
    this.#deleteSessions.push(call);
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
