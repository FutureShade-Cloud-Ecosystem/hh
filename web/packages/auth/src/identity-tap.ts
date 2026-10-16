// The identity tap: this package keeps no role state on purpose, but a page
// that recovers its session must know the person's own role to say "Continue
// as" and to land them. This decorator remembers the user block of the last
// exchange that was accepted and forwards every call untouched. It holds the
// display block only, never the token (the answer's token is not copied).
import type { AccountSurface, ExchangeResult, ExchangeUser } from "./client.js";

export function tapIdentity(surface: AccountSurface): {
  surface: AccountSurface;
  identity: () => ExchangeUser | null;
  forget: () => void;
} {
  let last: ExchangeUser | null = null;
  const tapped: AccountSurface = {
    createEmailPasswordSession: (email, password) => surface.createEmailPasswordSession(email, password),
    createOAuth2Session: (provider, redirectTo) => surface.createOAuth2Session(provider, redirectTo),
    getSession: () => surface.getSession(),
    exchange: async (org): Promise<ExchangeResult> => {
      const result = await surface.exchange(org);
      if (result.ok) last = { ...result.answer.user };
      return result;
    },
    deleteSession: () => surface.deleteSession(),
  };
  return {
    surface: tapped,
    identity: () => last,
    forget: () => {
      last = null;
    },
  };
}
