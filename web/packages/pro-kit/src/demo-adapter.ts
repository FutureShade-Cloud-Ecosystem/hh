// The demo sign in adapter: a contractor persona chosen by its role label, for the demo build only (each app
// picks it where Vite's MODE is "demo"; a production build carries only realExchangeAdapter). It asks the
// core's demo routes: GET /api/v1/demo/flows lists the closed flow table with each row's class and role code,
// and POST /api/v1/demo/session takes one flow name and nothing else and answers a tenant class bearer. The
// bearer goes straight into the session's in memory custody (session.ts); nothing is written to web storage.
//
// The five role labels are the kit's own words (Owner, Buyer, Project manager, Accounts payable, Field crew);
// the flow is found by the role code the listing carries, so a label never names a staff flow.
import { err, ok, type Result } from "@hardscapehouse/api-client";
import { TENANT_ROLES, ROLE_LABELS } from "./capabilities.js";
import { DEMO_METHODS, type SignInMethod } from "./signin-methods.js";
import type { SignInRequest, TenantAnswer, TenantSignInAdapter } from "./session.js";

const UNREACHABLE_WORDS = "Pro is not reachable right now. Try again in a moment.";
const TOO_MANY_WORDS = "Too many sign in attempts. Wait a moment and try again.";
const PICK_A_ROLE_WORDS = "That is not open on the demo. Pick a demo role below.";
const NOT_A_DEMO_ACCOUNT_WORDS = "That email is not a demo account. Pick a demo role below.";
const NO_SUCH_ROLE_WORDS = "That demo role is not available. Pick another demo role.";
const DEMO_EMAIL_DOMAIN = "hh-demo.invalid";

type Fetcher = (url: string, init?: RequestInit) => Promise<{ status: number; ok: boolean; json(): Promise<unknown> }>;

export interface DemoTenantAdapterOptions {
  /** The base URL the demo routes answer on; empty for same origin (the demo box). */
  baseUrl?: string;
  fetch?: Fetcher;
}

/** The expiry in a token's own exp claim, in milliseconds; null when it is not readable. */
function expiryOf(token: string): number | null {
  const parts = token.split(".");
  if (parts.length !== 3) return null;
  try {
    const padded = (parts[1] as string).replace(/-/g, "+").replace(/_/g, "/");
    const binary = atob(padded + "=".repeat((4 - (padded.length % 4)) % 4));
    const payload = JSON.parse(new TextDecoder().decode(Uint8Array.from(binary, (c) => c.charCodeAt(0)))) as { exp?: unknown };
    return typeof payload.exp === "number" && Number.isFinite(payload.exp) ? payload.exp * 1000 : null;
  } catch {
    return null;
  }
}

function roleCodeOf(label: string): string | undefined {
  return TENANT_ROLES.find((role) => ROLE_LABELS[role] === label);
}

export class DemoTenantSignInAdapter implements TenantSignInAdapter {
  readonly #baseUrl: string;
  readonly #fetch: Fetcher;

  constructor(options: DemoTenantAdapterOptions = {}) {
    this.#baseUrl = options.baseUrl ?? "";
    this.#fetch = options.fetch ?? ((url, init) => fetch(url, init));
  }

  methods(): readonly SignInMethod[] {
    return DEMO_METHODS;
  }

  personas(): readonly string[] {
    return TENANT_ROLES.map((role) => ROLE_LABELS[role]);
  }

  /** The demo has no passwords: the card offers the email link, Google and the role picker. */
  async signIn(): Promise<Result<TenantAnswer>> {
    return err(PICK_A_ROLE_WORDS);
  }

  async signInWith(request: SignInRequest): Promise<Result<TenantAnswer>> {
    if (request.method === "persona") return this.#mintRole(roleCodeOf(request.roleLabel));
    if (request.method === "email_link") {
      // A demo account's address is its role code at the reserved domain (core/internal/demo/flows).
      const [local, domain, ...rest] = (request.email ?? "").trim().toLowerCase().split("@");
      const known = TENANT_ROLES.find((role) => role === local);
      if (rest.length > 0 || domain !== DEMO_EMAIL_DOMAIN || known === undefined) return err(NOT_A_DEMO_ACCOUNT_WORDS);
      return this.#mintRole(known);
    }
    return err(PICK_A_ROLE_WORDS);
  }

  async signOut(): Promise<void> {}

  async #mintRole(roleCode: string | undefined): Promise<Result<TenantAnswer>> {
    if (roleCode === undefined) return err(NO_SUCH_ROLE_WORDS);
    let listing: Response | Awaited<ReturnType<Fetcher>>;
    try {
      listing = await this.#fetch(`${this.#baseUrl}/api/v1/demo/flows`, { method: "GET", redirect: "error" });
    } catch {
      return err(UNREACHABLE_WORDS);
    }
    if (!listing.ok) return err(UNREACHABLE_WORDS, listing.status);
    const flow = await flowFor(listing, roleCode);
    if (flow === null) return err(NO_SUCH_ROLE_WORDS);
    let response: Awaited<ReturnType<Fetcher>>;
    try {
      response = await this.#fetch(`${this.#baseUrl}/api/v1/demo/session`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ flow }),
        redirect: "error",
      });
    } catch {
      return err(UNREACHABLE_WORDS);
    }
    if (response.status === 429) return err(TOO_MANY_WORDS, 429);
    if (!response.ok) return err(UNREACHABLE_WORDS, response.status);
    let body: unknown;
    try {
      body = await response.json();
    } catch {
      return err(UNREACHABLE_WORDS);
    }
    return answerOf(body);
  }
}

async function flowFor(listing: { json(): Promise<unknown> }, roleCode: string): Promise<string | null> {
  let body: unknown;
  try {
    body = await listing.json();
  } catch {
    return null;
  }
  const rows = (body as { flows?: unknown } | null)?.flows;
  if (!Array.isArray(rows)) return null;
  for (const row of rows as Array<Record<string, unknown>>) {
    if (row !== null && typeof row === "object" && row["class"] === "tenant" && row["role_code"] === roleCode && typeof row["flow"] === "string") return row["flow"];
  }
  return null;
}

function answerOf(body: unknown): Result<TenantAnswer> {
  const answer = body as { token?: unknown; class?: unknown; user?: { user_id?: unknown; email?: unknown; display_name?: unknown } } | null;
  if (answer === null || typeof answer !== "object" || answer.class !== "tenant") return err(UNREACHABLE_WORDS);
  const token = answer.token;
  const user = answer.user;
  if (typeof token !== "string" || token === "" || user === undefined || user === null) return err(UNREACHABLE_WORDS);
  const expiresAtMs = expiryOf(token);
  if (expiresAtMs === null) return err(UNREACHABLE_WORDS);
  if (typeof user.user_id !== "string" || user.user_id === "") return err(UNREACHABLE_WORDS);
  return ok({ token, expiresAtMs, user: { userId: user.user_id, email: String(user.email ?? ""), name: String(user.display_name ?? "") } });
}

export function demoTenantAdapter(options: DemoTenantAdapterOptions = {}): DemoTenantSignInAdapter {
  return new DemoTenantSignInAdapter(options);
}
