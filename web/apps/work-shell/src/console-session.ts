// The shell's own sign in path: the auth package's AuthController over the
// Tauri adapter (the four Rust commands), used when the webview has an invoke.
// The FB Console session secret and every Console JWT stay in Rust; the
// webview gets the user and the core's short token.
//
// The auth package keeps no role state (roles are the server's), so the host
// reads the role from the exchange answer itself: a thin wrapper around the
// adapter's `exchange` remembers the last user block it saw, and the host
// holds that beside the session, in memory. A shell with no Console
// configuration answers `unavailable` to every command; resume then yields
// null and the host shows the front door's picker.
import {
  AuthController,
  SessionStore,
  type AccountSurface,
  type ExchangeAnswer,
  type ExchangeResult,
} from "@hardscapehouse/auth";
import { createTauriAccountSurface, type TauriInvoke } from "@hardscapehouse/auth/tauri-adapter";
import type { HostSession } from "./host-state.js";

export interface ConsoleSession {
  /** Resume the shell's Console session (the keychain's, on the Rust side). Null when there is none. */
  resume(): Promise<HostSession | null>;
  /** End it: the platform session is deleted and custody cleared. */
  signOut(): Promise<void>;
  /** The controller's current token (it refreshes before expiry), or null. */
  currentToken(): string | null;
}

export function createConsoleSession(deps: {
  invoke: TauriInvoke;
  org: string;
  sessions?: SessionStore;
}): ConsoleSession {
  const adapter = createTauriAccountSurface({ invoke: deps.invoke });
  let lastAnswer: ExchangeAnswer | null = null;
  const account: AccountSurface = {
    createEmailPasswordSession: (email, password) => adapter.createEmailPasswordSession(email, password),
    createOAuth2Session: (provider, redirectTo) => adapter.createOAuth2Session(provider, redirectTo),
    getSession: () => adapter.getSession(),
    deleteSession: () => adapter.deleteSession(),
    async exchange(org: string): Promise<ExchangeResult> {
      const result = await adapter.exchange(org);
      if (result.ok) lastAnswer = result.answer;
      return result;
    },
  };
  const sessions = deps.sessions ?? new SessionStore();
  const controller = new AuthController({ account, sessions, activeOrg: () => deps.org });

  return {
    async resume(): Promise<HostSession | null> {
      const resumed = await controller.resume();
      if (!resumed.ok) return null;
      const live = sessions.current;
      const answer = lastAnswer;
      if (live === null || answer === null || answer.user.user_id !== live.user.userId) return null;
      return {
        source: "console",
        userId: live.user.userId,
        email: live.user.email,
        name: live.user.name,
        roleCode: answer.user.role_code,
        roleLabel: answer.user.role_name,
        roleName: answer.user.role_name,
        token: live.token,
        tokenExpiresAtMs: live.tokenExpiresAtMs,
        flow: answer.user.role_code,
        tier: answer.user.tier,
        scope: answer.user.scope,
      };
    },
    async signOut(): Promise<void> {
      lastAnswer = null;
      await controller.signOut();
    },
    currentToken(): string | null {
      return sessions.current?.token ?? null;
    },
  };
}
