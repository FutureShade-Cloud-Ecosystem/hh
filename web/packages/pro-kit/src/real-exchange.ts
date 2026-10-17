// WIRE THE REAL EXCHANGE HERE.
//
// This is the seam the auth lead's go will fill; until then it refuses in plain
// words and nothing in a Pro app depends on it succeeding.
//
// What the real adapter needs (all of it is the auth contract's, AUTH-SHELL-CONTRACT
// r5, and none of it is built on the web side today):
// 1. A contractor identity on the platform: the core's tenant login, accept
//    invite, password reset and refresh routes are unregistered on the served
//    mux (core/internal/portal/handler.go, "CARRY NOTE"), so a contractor's
//    session is the platform bearer, minted by the identity provider and
//    exchanged for the core's tenant token. The contract's "Not in this
//    contract" names contractor (tenant class) sign in as open.
// 2. One AccountSurface style call that resolves {token, expiresAtMs, user} for
//    the tenant class. In a shell that is a Rust command forwarding to the
//    keychain held session; in a browser it is the platform web SDK's JWT then
//    the core's exchange. The two are chosen by the host, never by an app.
// 3. A refresh before expiresAtMs (the window is short by design) and the
//    drain rules for a 401 and a 403 (a 403 is not a sign out).
// 4. The signIn answer's words: plain, no mention of the machinery.
//
// The shape to return is TenantAnswer (session.ts). Replace the body of
// realExchangeAdapter and keep its name; the apps call nothing else.
import { err, type Result } from "@hardscapehouse/api-client";
import type { TenantAnswer, TenantSignInAdapter } from "./session.js";

const NOT_WIRED_WORDS = "Sign in is not open yet for contractor accounts. Ask the dealer when it opens.";

export function realExchangeAdapter(): TenantSignInAdapter {
  return {
    async signIn(): Promise<Result<TenantAnswer>> {
      return err(NOT_WIRED_WORDS);
    },
    async signOut(): Promise<void> {},
  };
}
