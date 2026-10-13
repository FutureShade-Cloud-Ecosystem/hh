// The stub sign in adapter, for the dev server and the tests. It talks to the
// shared tenant stub's own door (POST /_stub/session, test-stub/tenant-stub.mjs),
// which exists only there: the core serves no contractor sign in today. A
// production build never ships it: each app picks it only where Vite's
// import.meta.env.DEV is true and uses realExchangeAdapter() everywhere else.
import { err, ok, type Result } from "@hardscapehouse/api-client";
import type { SignInRequest, TenantAnswer, TenantSignInAdapter } from "./session.js";
import { ROLE_LABELS, TENANT_ROLES } from "./capabilities.js";
import type { SignInMethod } from "./signin-methods.js";

const UNREACHABLE_WORDS = "Pro is not reachable right now. Try again in a moment.";
const PICK_A_ROLE_WORDS = "That is not open here. Pick a role below.";
const MISMATCH_WORDS = "That email and password do not match.";

// The card's methods in the dev server and the legs: FutureBuild's own email and password (what every
// leg types), unless the page address asks otherwise (?signin-methods=email_link,google and
// ?signin-refused=microsoft), so a leg can look at each board state. A development double only.
function fromAddress(name: string): string | null {
  const search = (globalThis as { location?: { search?: string } }).location?.search;
  return search === undefined ? null : new URLSearchParams(search).get(name);
}

export interface StubAdapterOptions {
  baseUrl?: string;
  fetch?: typeof fetch;
}

export function stubAdapter(options: StubAdapterOptions = {}): TenantSignInAdapter {
  const baseUrl = options.baseUrl ?? "";
  const doFetch = options.fetch ?? ((url: string, init?: RequestInit) => fetch(url, init));
  return {
    methods(): readonly SignInMethod[] {
      const asked = fromAddress("signin-methods");
      return asked === null ? ["futurebuild"] : (asked.split(",").filter((m) => m !== "") as SignInMethod[]);
    },
    refused(): SignInMethod | null {
      return fromAddress("signin-refused") as SignInMethod | null;
    },
    personas(): readonly string[] {
      return fromAddress("signin-personas") === null ? [] : TENANT_ROLES.map((role) => ROLE_LABELS[role]);
    },
    async signInWith(request: SignInRequest): Promise<Result<TenantAnswer>> {
      if (request.method !== "persona") return err(PICK_A_ROLE_WORDS);
      const role = TENANT_ROLES.find((r) => ROLE_LABELS[r] === request.roleLabel);
      return role === undefined ? err(PICK_A_ROLE_WORDS) : mint({ role });
    },
    signIn(email: string, password: string): Promise<Result<TenantAnswer>> {
      return mint({ email, password });
    },
    async signOut(): Promise<void> {},
  };

  async function mint(credentials: Record<string, string>): Promise<Result<TenantAnswer>> {
      let response: Response;
      try {
        response = await doFetch(`${baseUrl}/_stub/session`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(credentials),
          redirect: "error",
        });
      } catch {
        return err(UNREACHABLE_WORDS);
      }
      if (response.status === 401) return err(MISMATCH_WORDS, 401);
      if (!response.ok) return err(UNREACHABLE_WORDS, response.status);
      let body: unknown;
      try {
        body = await response.json();
      } catch {
        return err(UNREACHABLE_WORDS);
      }
      const answer = body as { token?: unknown; expires_at_ms?: unknown; user?: { id?: unknown; email?: unknown; name?: unknown } };
      if (typeof answer.token !== "string" || typeof answer.expires_at_ms !== "number" || answer.user === undefined) return err(UNREACHABLE_WORDS);
      return ok({
        token: answer.token,
        expiresAtMs: answer.expires_at_ms,
        user: { userId: String(answer.user.id), email: String(answer.user.email), name: String(answer.user.name) },
      });
  }
}
