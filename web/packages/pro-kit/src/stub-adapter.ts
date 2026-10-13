// The stub sign in adapter, for the dev server and the tests. It talks to the
// shared tenant stub's own door (POST /_stub/session, test-stub/tenant-stub.mjs),
// which exists only there: the core serves no contractor sign in today. A
// production build never ships it: each app picks it only where Vite's
// import.meta.env.DEV is true and uses realExchangeAdapter() everywhere else.
import { err, ok, type Result } from "@hardscapehouse/api-client";
import type { TenantAnswer, TenantSignInAdapter } from "./session.js";

const UNREACHABLE_WORDS = "Pro is not reachable right now. Try again in a moment.";
const MISMATCH_WORDS = "That email and password do not match.";

export interface StubAdapterOptions {
  baseUrl?: string;
  fetch?: typeof fetch;
}

export function stubAdapter(options: StubAdapterOptions = {}): TenantSignInAdapter {
  const baseUrl = options.baseUrl ?? "";
  const doFetch = options.fetch ?? ((url: string, init?: RequestInit) => fetch(url, init));
  return {
    async signIn(email: string, password: string): Promise<Result<TenantAnswer>> {
      let response: Response;
      try {
        response = await doFetch(`${baseUrl}/_stub/session`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ email, password }),
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
    },
    async signOut(): Promise<void> {},
  };
}
