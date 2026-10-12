// The browser adapter: this package's AccountSurface (src/client.ts) over the
// Appwrite web SDK, against FB Console's HH project, and over the core's
// exchange route. It is a SEPARATE ENTRY (the "./appwrite-adapter" export):
// the package root imports nothing from here, so a host that does not sign in
// from a browser (a Tauri shell forwards to its Rust side instead) takes no
// SDK dependency.
//
// What it holds: nothing. Each exchange attempt mints an FB Console JWT
// (createJWT, 900 seconds of life), posts it with the org to
// POST /api/v1/auth/exchange, and drops it; the next attempt, a retry
// included, mints a new one, because the core accepts a JWT once. The JWT is
// a local of one attempt, never a field, never returned, never in an error
// text. The only thing handed back is the core's own answer or a typed
// refusal code.
//
// The session cookie is FB Console's, httpOnly and first party to the page
// (every browser copy stays on a futurebuild.ai host, so the console's
// api.futurebuild.ai cookie is not third party). The SDK has a second
// mechanism this package refuses: whenever a console response carries
// X-Fallback-Cookies the SDK writes it to window.localStorage under
// "cookieFallback" and sends it back on every request. That value is the
// session secret, and this package's custody rule is that no credential rests
// in browser storage. The adapter therefore removes the key after every SDK
// call, so the value is never left at rest and is never sent back. This is a
// mitigation, not a prevention (the SDK writes before the adapter can see
// the response); the TO-PRINCIPAL finding for this item says so.
import { Account, Client, OAuthProvider } from "appwrite";
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
  /** The fetch used for the core's exchange route; the platform's when omitted. The SDK uses the platform's own. */
  fetch?: typeof fetch;
}

const FALLBACK_KEY = "cookieFallback";
const EXCHANGE_PATH = "/api/v1/auth/exchange";

function nonEmpty(value: string, what: string): string {
  if (typeof value !== "string" || value.trim() === "") {
    throw new Error(`createAppwriteAccountSurface: the ${what} is required`);
  }
  return value;
}

/** Remove the SDK's session fallback from local storage, whether or not it is there. Never throws. */
function scrubFallback(): void {
  try {
    const storage = (globalThis as { window?: { localStorage?: Storage } }).window?.localStorage;
    storage?.removeItem(FALLBACK_KEY);
  } catch {
    // denied site data: nothing could have been written either
  }
}

/** Run one SDK call, and leave no fallback behind whatever it did. */
async function sdk<T>(call: () => Promise<T>): Promise<T> {
  try {
    return await call();
  } finally {
    scrubFallback();
  }
}

/** The SDK's exception carries the console's HTTP status in `code`; anything without one is the network. */
function consoleRefusal(error: unknown): ExchangeRefusalCode {
  const code = (error as { code?: unknown } | null)?.code;
  if (typeof code !== "number") return "offline";
  if (code === 401 || code === 403 || code === 404) return "no_session";
  if (code === 429) return "rate_limited";
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
function readAnswer(body: unknown): ExchangeAnswer | null {
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

function accountUser(account: { $id: string; email: string; name: string }): AccountUser {
  return { userId: account.$id, email: account.email, name: account.name };
}

/** The page's own origin, the only place a redirect may land. */
function pageOrigin(): string | null {
  const origin = (globalThis as { window?: { location?: { origin?: unknown } } }).window?.location?.origin;
  return typeof origin === "string" && origin !== "" && origin !== "null" ? origin : null;
}

/** Validate a redirect: an absolute URL on the page's own origin, and nothing else. */
function sameOriginRedirect(redirectTo: string): URL {
  const origin = pageOrigin();
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
 * options and touches nothing; the SDK client is built here and lives for the
 * surface's life.
 */
export function createAppwriteAccountSurface(options: AppwriteAccountSurfaceOptions): AccountSurface {
  const endpoint = nonEmpty(options.endpoint, "endpoint");
  const projectId = nonEmpty(options.projectId, "project id");
  let exchangeUrl: string;
  try {
    exchangeUrl = new URL(EXCHANGE_PATH, nonEmpty(options.coreBaseUrl, "core base URL")).toString();
  } catch {
    throw new Error("createAppwriteAccountSurface: the core base URL is not a URL");
  }
  const client = new Client().setEndpoint(endpoint).setProject(projectId);
  const account = new Account(client);

  /** One attempt: a fresh JWT, one post to the core, and the JWT is dropped on the way out. */
  async function attempt(org: string): Promise<ExchangeResult> {
    let jwt: string;
    try {
      jwt = (await sdk(() => account.createJWT())).jwt;
    } catch (error) {
      return { ok: false, code: consoleRefusal(error) };
    }
    let response: Response;
    try {
      const send = options.fetch ?? globalThis.fetch;
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
    let body: unknown;
    try {
      body = await response.json();
    } catch {
      return { ok: false, code: "unavailable" };
    }
    const answer = readAnswer(body);
    return answer === null ? { ok: false, code: "unavailable" } : { ok: true, answer };
  }

  return {
    async createEmailPasswordSession(email: string, password: string): Promise<AccountUser> {
      await sdk(() => account.createEmailPasswordSession({ email, password }));
      return accountUser(await sdk(() => account.get()));
    },

    async createOAuth2Session(provider: OAuth2Provider, redirectTo: string): Promise<void> {
      if (provider !== "google") throw new Error("the provider is not supported: only google");
      const success = sameOriginRedirect(redirectTo);
      const failure = new URL(success);
      failure.searchParams.set("signin", "failed");
      // The SDK navigates the page to the console's OAuth route and returns.
      account.createOAuth2Session({
        provider: OAuthProvider.Google,
        success: success.toString(),
        failure: failure.toString(),
      });
    },

    async getSession(): Promise<AccountUser> {
      await sdk(() => account.getSession({ sessionId: "current" }));
      return accountUser(await sdk(() => account.get()));
    },

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
      await sdk(() => account.deleteSession({ sessionId: "current" }));
    },
  };
}
