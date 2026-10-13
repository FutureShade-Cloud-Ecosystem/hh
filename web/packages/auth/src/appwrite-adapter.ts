// The browser adapter: this package's AccountSurface (src/client.ts) over
// FB Console's REST API and the core's exchange route, with plain fetch and
// no SDK. It is a SEPARATE ENTRY (the "./appwrite-adapter" export): the
// package root imports nothing from here, so a host that does not sign in
// from a browser (a Tauri shell forwards to its Rust side instead) bundles
// none of it.
//
// What it holds: nothing, and it writes nothing anywhere. Each exchange
// attempt mints an FB Console JWT (POST /account/jwts, 900 seconds of life),
// posts it with the org to POST /api/v1/auth/exchange, and drops it; the
// next attempt, a retry included, mints a new one, because the core accepts a
// JWT once. The JWT is a local of one attempt, never a field, never returned,
// never in an error text. The only thing handed back is the core's own answer
// or a typed refusal code.
//
// The session cookie is FB Console's, httpOnly and first party to the page
// (every browser copy stays on a futurebuild.ai host), carried by
// credentials "include". The web SDK was dropped because it copies a session
// fallback to local storage whenever the console sends X-Fallback-Cookies;
// this adapter never reads that response header, never sets it on a request,
// and touches no storage of any kind (a test holds that line with storage
// doubles that throw on any write, and a scan of this source).
import { PlatformRefusedError, PlatformUnreachableError } from "./client.js";
import type {
  AccountSurface,
  AccountUser,
  ExchangeAnswer,
  ExchangeRefusalCode,
  ExchangeResult,
  OAuth2Provider,
} from "./client.js";

export interface AppwriteAccountSurfaceOptions {
  /** FB Console's API endpoint, for example https://api.futurebuild.ai/v1. */
  endpoint: string;
  /** The HH project id in FB Console. */
  projectId: string;
  /** The core's base URL, with no trailing path; the exchange route is appended. */
  coreBaseUrl: string;
  /** The fetch used for every call; the platform's when omitted. */
  fetch?: typeof fetch;
  /** Navigate the window to a URL (the Google flow); sets window.location.href when omitted. */
  navigate?: (url: string) => void;
  /** The page's own origin, the only place a redirect may land; window.location.origin when omitted. */
  pageOrigin?: string;
}

const EXCHANGE_PATH = "/api/v1/auth/exchange";

/** A URL the password or a JWT will be sent to must be https; a local development host may be http. */
function secureUrl(value: string, what: string): string {
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    throw new Error(`createAppwriteAccountSurface: the ${what} is not a URL`);
  }
  const local = url.protocol === "http:" && (url.hostname === "localhost" || url.hostname === "127.0.0.1");
  if (url.protocol !== "https:" && !local) {
    throw new Error(`createAppwriteAccountSurface: the ${what} must be https (http only for localhost and 127.0.0.1)`);
  }
  return value;
}

function nonEmpty(value: string, what: string): string {
  if (typeof value !== "string" || value.trim() === "") {
    throw new Error(`createAppwriteAccountSurface: the ${what} is required`);
  }
  return value;
}

/** The console's refusal of a JWT mint: the session is gone, blocked or throttled, or the console is down. */
function consoleRefusal(status: number): ExchangeRefusalCode {
  if (status === 401 || status === 403 || status === 404) return "no_session";
  if (status === 429) return "rate_limited";
  return "unavailable";
}

function coreRefusal(status: number): ExchangeRefusalCode {
  if (status === 400) return "bad_request";
  if (status === 401) return "unauthorized";
  if (status === 403) return "forbidden";
  if (status === 429) return "rate_limited";
  return "unavailable";
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

/** The core's 200 body, read field by field; anything short of a usable answer is null. */
export function readExchangeAnswer(body: unknown): ExchangeAnswer | null {
  if (!isRecord(body)) return null;
  const { token, expiresAtMs, user } = body;
  if (typeof token !== "string" || token === "") return null;
  if (typeof expiresAtMs !== "number" || !Number.isFinite(expiresAtMs)) return null;
  if (!isRecord(user)) return null;
  const { user_id, email, display_name, role_code, role_name, tier, scope } = user;
  if (typeof user_id !== "string" || user_id === "") return null;
  if (typeof email !== "string" || typeof display_name !== "string") return null;
  if (typeof role_code !== "string" || typeof role_name !== "string" || typeof scope !== "string") return null;
  if (typeof tier !== "number" || !Number.isInteger(tier)) return null;
  return { token, expiresAtMs, user: { user_id, email, display_name, role_code, role_name, tier, scope } };
}

/** The console's account, read field by field; anything short of an id, an email and a name is refused. */
function readAccount(body: unknown): AccountUser {
  if (isRecord(body) && typeof body["$id"] === "string" && body["$id"] !== "" && typeof body["email"] === "string" && typeof body["name"] === "string") {
    return { userId: body["$id"], email: body["email"], name: body["name"] };
  }
  throw new Error("the console's account answer is not usable");
}

/** Validate a redirect: an absolute URL on the page's own origin, and nothing else. */
function sameOriginRedirect(redirectTo: string, origin: string | null): URL {
  let target: URL;
  try {
    target = new URL(redirectTo);
  } catch {
    throw new Error("the redirect is not an absolute URL on this page's own host");
  }
  if (origin === null || target.origin !== origin) {
    throw new Error("the redirect is not on this page's own host");
  }
  return target;
}

/**
 * The AccountSurface over FB Console and the core. Construction validates the
 * options and touches nothing.
 */
export function createAppwriteAccountSurface(options: AppwriteAccountSurfaceOptions): AccountSurface {
  const endpoint = secureUrl(nonEmpty(options.endpoint, "endpoint"), "endpoint").replace(/\/+$/, "");
  const projectId = nonEmpty(options.projectId, "project id");
  let exchangeUrl: string;
  try {
    exchangeUrl = new URL(EXCHANGE_PATH, secureUrl(nonEmpty(options.coreBaseUrl, "core base URL"), "core base URL")).toString();
  } catch (error) {
    throw error instanceof Error && /must be https|is required/.test(error.message)
      ? error
      : new Error("createAppwriteAccountSurface: the core base URL is not a URL");
  }

  const send = (url: string, init: RequestInit): Promise<Response> => (options.fetch ?? globalThis.fetch)(url, init);
  const origin = (): string | null => {
    if (options.pageOrigin !== undefined) return options.pageOrigin;
    const o = (globalThis as { window?: { location?: { origin?: unknown } } }).window?.location?.origin;
    return typeof o === "string" && o !== "" && o !== "null" ? o : null;
  };
  const navigate =
    options.navigate ??
    ((url: string): void => {
      (globalThis as unknown as { window: { location: { href: string } } }).window.location.href = url;
    });

  /** One console call: the project header, JSON, the first party cookie, no redirects. The response's headers are never read. */
  function consoleCall(method: "GET" | "POST" | "DELETE", path: string, body?: unknown): Promise<Response> {
    return send(`${endpoint}${path}`, {
      method,
      headers: { "X-Appwrite-Project": projectId, "Content-Type": "application/json" },
      credentials: "include",
      redirect: "error",
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
  }

  async function readJson(response: Response): Promise<unknown> {
    try {
      return await response.json();
    } catch {
      return null;
    }
  }

  /** One attempt: a fresh JWT, one post to the core, and the JWT is dropped on the way out. */
  async function attempt(org: string): Promise<ExchangeResult> {
    let jwt: string;
    try {
      const minted = await consoleCall("POST", "/account/jwts", {});
      if (minted.status < 200 || minted.status > 299) return { ok: false, code: consoleRefusal(minted.status) };
      const body = await readJson(minted);
      const value = isRecord(body) ? body["jwt"] : undefined;
      if (typeof value !== "string" || value === "") return { ok: false, code: "unavailable" };
      jwt = value;
    } catch {
      return { ok: false, code: "offline" };
    }
    let response: Response;
    try {
      response = await send(exchangeUrl, {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        body: JSON.stringify({ jwt, org }),
        credentials: "omit",
        redirect: "error",
      });
    } catch {
      return { ok: false, code: "offline" };
    }
    jwt = ""; // the attempt is over; the JWT is not kept
    if (response.status !== 200) return { ok: false, code: coreRefusal(response.status) };
    const answer = readExchangeAnswer(await readJson(response));
    return answer === null ? { ok: false, code: "unavailable" } : { ok: true, answer };
  }

  /** The console's error type from a refused response, or null when the body is not JSON with one. */
  async function errorType(response: Response): Promise<string | null> {
    const body = await readJson(response);
    return isRecord(body) && typeof body["type"] === "string" ? body["type"] : null;
  }

  async function account(): Promise<AccountUser> {
    let response: Response;
    try {
      response = await consoleCall("GET", "/account");
    } catch {
      throw new PlatformUnreachableError("the console could not be reached (network)");
    }
    if (response.status !== 200) throw new Error(`the console refused the account read (status ${response.status})`);
    return readAccount(await readJson(response));
  }

  return {
    async createEmailPasswordSession(email: string, password: string): Promise<AccountUser> {
      const create = async (): Promise<Response> => {
        try {
          return await consoleCall("POST", "/account/sessions/email", { email, password });
        } catch {
          throw new PlatformUnreachableError("the console could not be reached (network)");
        }
      };
      let response = await create();
      // A leftover session (an earlier attempt, a sign out that never reached
      // the console) makes the console refuse a create: delete the current
      // one and create again, once.
      if (response.status === 401 && (await errorType(response)) === "user_session_already_exists") {
        await this.deleteSession();
        response = await create();
      }
      if (response.status < 200 || response.status > 299) {
        throw new PlatformRefusedError(response.status, `the console refused the sign-in (status ${response.status})`);
      }
      return account();
    },

    async createOAuth2Session(provider: OAuth2Provider, redirectTo: string): Promise<void> {
      if (provider !== "google") throw new Error("the provider is not supported: only google");
      const success = sameOriginRedirect(redirectTo, origin());
      const failure = new URL(success);
      failure.searchParams.set("signin", "failed");
      const target = new URL(`${endpoint}/account/sessions/oauth2/google`);
      target.searchParams.set("project", projectId);
      target.searchParams.set("success", success.toString());
      target.searchParams.set("failure", failure.toString());
      navigate(target.toString());
    },

    getSession: account,

    async exchange(org: string): Promise<ExchangeResult> {
      if (typeof org !== "string" || org.trim() === "") return { ok: false, code: "bad_request" };
      const first = await attempt(org);
      // A core 401 on a freshly minted JWT may be a lost race on the claim,
      // not a verdict on the person: one retry, with a new JWT, before the
      // caller ends the session.
      if (!first.ok && first.code === "unauthorized") return attempt(org);
      return first;
    },

    async deleteSession(): Promise<void> {
      const response = await consoleCall("DELETE", "/account/sessions/current");
      if (response.status < 200 || response.status > 299) {
        throw new Error(`the console refused the session delete (status ${response.status})`);
      }
    },
  };
}
