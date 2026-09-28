// The wiring into the api-client: one header provider the shell passes to
// `new ApiClient({ headers: authHeaderProvider(...) })`, so every request
// carries Authorization from memory (the JWT never rests anywhere else),
// X-Org from the active organisation, and the unlocked-user audit pair
// exactly while a device lock is open, and nothing else.
//
// A request without an active organisation is refused by the client before
// it leaves: the provider throws naming the missing header, and the
// transport (web/packages/api-client/src/http.ts) turns a provider throw
// into an error Result without dialing. The provider is synchronous on
// purpose: no request ever leaves this process without the org resolved.
//
// NO ROLE PICKER. This package exposes no role state at all: not a role
// field, not a role header, not a role-based branch anywhere below. Roles
// are the server's, and the header provider is exactly where a client-side
// role assumption would creep in (pick a header by role, render by role).
// The rule is HH Pro's, carried with its provenance:
// /home/colton/Desktop/hh_pro_dibbits/src/core/supplier/port.ts lines 71 to
// 77: a role code is the account's own, never re-expressed as another
// product's role vocabulary, because "Shipping a role picker the server
// cannot enforce is the post-M8 bug class the spec's R-4 exists to
// prevent." What the server cannot enforce, this package does not ship.
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
// in scope (src/lock.ts).
import { err, ok, type Result } from "../../api-client/src/result.js";
import type { HeaderProvider } from "../../api-client/src/http.js";
import type { DeviceLock } from "./lock.js";
import type { SessionStore, SessionUser } from "./session.js";

function asMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

export interface AuthHeaderProviderOptions {
  /** The custody holding the in-memory token (and nothing persistent). */
  sessions: SessionStore;
  /** The device lock, when the shell engages one; only its audit headers are read. */
  lock?: Pick<DeviceLock, "auditHeaders">;
  /** The active organisation, resolved per request by the shell. */
  activeOrg: () => string | null | undefined;
}

/**
 * Build the header provider for the api-client. Throws naming X-Org when no
 * active organisation is set or the value is unusable: the transport
 * converts that into an error Result and the request never leaves. Returns
 * Authorization, X-Org and (while a lock is open) the audit pair, and
 * nothing else; there is deliberately no role input to configure.
 */
export function authHeaderProvider(options: AuthHeaderProviderOptions): HeaderProvider {
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
    if (options.lock !== undefined) {
      Object.assign(headers, options.lock.auditHeaders());
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

/** A platform session: the identity plus the short-lived handle a reload may trade. */
export interface AccountSession {
  user: AccountUser;
  handle: string;
  expiresAtMs: number;
}

/**
 * The Appwrite account surface this package needs, as a narrow injected
 * interface. The consuming shell's adapter maps these onto the Appwrite Web
 * SDK (the one integration point a shell wires, recorded in the README); the
 * SDK's dependency lives there, not here.
 */
export interface AccountSurface {
  /** The house sign-in (email plus password through the Console bridge). */
  createEmailPasswordSession(email: string, password: string): Promise<AccountSession>;
  /** The Google flow; the platform navigates to redirectTo and returns through it. */
  createOAuth2Session(provider: OAuth2Provider, redirectTo: string): Promise<void>;
  /** The current platform session's user, when one exists. */
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
  refreshHandle?: { handle: string; expiresAtMs: number } | null;
}

/**
 * The sign-in orchestration over the seams: an AccountSurface for the
 * platform, a SessionStore for custody. Every path resolves a Result; a
 * wrong credential is data, never a throw.
 */
export class AuthController {
  private readonly account: AccountSurface;
  private readonly sessions: SessionStore;

  constructor(deps: { account: AccountSurface; sessions: SessionStore }) {
    this.account = deps.account;
    this.sessions = deps.sessions;
  }

  /** The house sign-in: establish the platform session, mint the JWT, adopt it. The refresh handle rides along so a reload can restore. */
  async signInWithPassword(email: string, password: string): Promise<Result<AccountUser>> {
    let platform: AccountSession;
    try {
      platform = await this.account.createEmailPasswordSession(email, password);
    } catch (error) {
      return err(`sign-in failed: ${asMessage(error)}`);
    }
    let minted: { jwt: string; expiresAtMs: number };
    try {
      minted = await this.account.createJWT();
    } catch (error) {
      return err(`sign-in failed minting the token: ${asMessage(error)}`);
    }
    const adopted = this.sessions.adopt({
      token: minted.jwt,
      tokenExpiresAtMs: minted.expiresAtMs,
      user: platform.user,
      refreshHandle: { handle: platform.handle, expiresAtMs: platform.expiresAtMs },
    });
    if (!adopted) return err("sign-in failed: the platform returned an unusable session");
    return ok({ ...platform.user });
  }

  /**
   * Adopt a session established out of band (the Google callback through the
   * Console bridge, or a reload's restored platform session), the ERP's
   * adoptSession posture: custody takes the token into memory and the handle
   * into the mirror, nothing else.
   */
  adopt(input: AdoptSessionInput): boolean {
    const user: SessionUser = { userId: input.user.userId, email: input.user.email, name: input.user.name };
    return this.sessions.adopt({
      token: input.token,
      tokenExpiresAtMs: input.tokenExpiresAtMs,
      user,
      refreshHandle: input.refreshHandle ?? null,
    });
  }

  /** Mint a fresh in-memory token for the live platform session, carrying the stored refresh handle through unchanged. */
  async refreshJWT(): Promise<Result<void>> {
    const current = this.sessions.current;
    if (current === null) return err("no live session to refresh");
    const restorable = this.sessions.restorable;
    let minted: { jwt: string; expiresAtMs: number };
    try {
      minted = await this.account.createJWT();
    } catch (error) {
      return err(`the token refresh failed: ${asMessage(error)}`);
    }
    this.sessions.adopt({
      token: minted.jwt,
      tokenExpiresAtMs: minted.expiresAtMs,
      user: current.user,
      refreshHandle: restorable === null ? null : { handle: restorable.handle, expiresAtMs: restorable.expiresAtMs },
    });
    return ok(undefined);
  }

  /** Sign out: end the platform session (best effort; it may already be gone) and wipe custody either way. */
  async signOut(): Promise<void> {
    try {
      await this.account.deleteSession();
    } catch {
      // the platform session was already gone; custody is cleared regardless
    }
    this.sessions.clear();
  }
}
