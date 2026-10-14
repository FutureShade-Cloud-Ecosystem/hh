// The Tauri shell adapter: this package's AccountSurface (src/client.ts) over
// the four Rust commands of the shells contract (auth_sign_in, auth_resume,
// auth_exchange, auth_sign_out). It is a SEPARATE ENTRY (the "./tauri-adapter"
// export), like the browser adapter, and the host chooses between the two, so
// an app never branches on where it runs.
//
// It imports nothing from Tauri: the host injects `invoke` (the shell's
// `invoke` from @tauri-apps/api/core, or a fake in a test), so no Tauri
// module is bundled into a browser build.
//
// What it holds: nothing. The FB Console session secret and every Console JWT
// stay on the Rust side (the OS keychain, one JWT per exchange attempt); the
// webview receives only the user, and the core's short token with its expiry
// from `auth_exchange`. An error from a command is `{code}` and nothing else,
// and this adapter puts only that code in a message: no input, no token.
// Personas are not Console users and do not come through here (the webview
// calls the core's persona route with fetch, and holds no keychain entry).
import { PlatformUnreachableError } from "./client.js";
import type {
  AccountSurface,
  AccountUser,
  ExchangeAnswer,
  ExchangeRefusalCode,
  ExchangeResult,
  OAuth2Provider,
} from "./client.js";

/** The shell's invoke: a command name and its arguments in, the command's answer (or a `{code}` rejection) out. */
export type TauriInvoke = (command: string, args?: Record<string, unknown>) => Promise<unknown>;

/** The shell's event listen: an event name and a handler in, an unlisten function out. */
export type TauriListen = (event: string, handler: () => void) => Promise<() => void>;

export interface TauriAccountSurfaceOptions {
  /** The injected invoke. */
  invoke: TauriInvoke;
}

/** The event `auth_sign_out` emits so every window drops its token (the contract: the Rust side tells the webview). */
export const SIGNED_OUT_EVENT = "auth:signed-out";

/**
 * Subscribe to the shell's sign-out event. The handler should clear custody
 * (AuthController.signOut() does, and tells the platform again, harmlessly).
 * Resolves the unlisten function.
 */
export function onShellSignedOut(listen: TauriListen, handler: () => void): Promise<() => void> {
  return listen(SIGNED_OUT_EVENT, handler);
}

const COMMAND = {
  signIn: "auth_sign_in",
  resume: "auth_resume",
  exchange: "auth_exchange",
  signOut: "auth_sign_out",
} as const;

/** Every code the Rust side can send. An unknown word is treated as `unavailable`, never as a refusal. */
type RustCode =
  | "bad_credentials"
  | "no_session"
  | "offline"
  | "keychain_unavailable"
  | "bad_request"
  | "forbidden"
  | "unauthorized"
  | "unavailable"
  | "rate_limited";

const RUST_CODES: ReadonlySet<string> = new Set<RustCode>([
  "bad_credentials",
  "no_session",
  "offline",
  "keychain_unavailable",
  "bad_request",
  "forbidden",
  "unauthorized",
  "unavailable",
  "rate_limited",
]);

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

/** The code of a rejection, or `unavailable` when it carries none we know: a broken shell says nothing about the person. */
function codeOf(rejection: unknown): RustCode {
  const code = isRecord(rejection) ? rejection["code"] : undefined;
  return typeof code === "string" && RUST_CODES.has(code) ? (code as RustCode) : "unavailable";
}

/**
 * The codes that say nothing about the person: no network, an outage, a
 * throttle, a locked or missing keychain. A resume that meets one keeps the
 * profile and the lock and tries again later (PlatformUnreachableError); only
 * `no_session` and the like end it.
 */
const OUTAGE: ReadonlySet<RustCode> = new Set<RustCode>(["offline", "unavailable", "rate_limited", "keychain_unavailable"]);

/** The Rust code as the package's exchange refusal code. Only unauthorized, forbidden and no_session end a session. */
function exchangeCode(code: RustCode): ExchangeRefusalCode {
  switch (code) {
    case "bad_request":
    case "unauthorized":
    case "forbidden":
    case "rate_limited":
    case "unavailable":
    case "offline":
    case "no_session":
      return code;
    default:
      // bad_credentials cannot come from an exchange, and a locked keychain is
      // not a verdict on the person: neither may end a session.
      return "unavailable";
  }
}

/** The Rust user block of a sign in or resume answer, read field by field. */
function readUser(answer: unknown): AccountUser {
  const user = isRecord(answer) ? answer["user"] : undefined;
  if (
    isRecord(user) &&
    typeof user["user_id"] === "string" &&
    user["user_id"] !== "" &&
    typeof user["email"] === "string" &&
    typeof user["name"] === "string"
  ) {
    return { userId: user["user_id"], email: user["email"], name: user["name"] };
  }
  throw new Error("the shell's account answer is not usable");
}

/** The core's exchange answer, read field by field; anything short of a usable one is null. */
function readExchange(body: unknown): ExchangeAnswer | null {
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

/**
 * The AccountSurface over the shell's Rust commands. Construction validates
 * the injected function and touches nothing.
 */
export function createTauriAccountSurface(options: TauriAccountSurfaceOptions): AccountSurface {
  if (typeof options?.invoke !== "function") {
    throw new Error("createTauriAccountSurface: an invoke function is required");
  }
  const { invoke } = options;

  /** A command call whose rejection becomes a coded error; the message carries the code and nothing else. */
  async function call(command: string, args?: Record<string, unknown>): Promise<unknown> {
    try {
      return args === undefined ? await invoke(command) : await invoke(command, args);
    } catch (rejection) {
      const code = codeOf(rejection);
      if (OUTAGE.has(code)) throw new PlatformUnreachableError(`the shell could not reach the platform (${code})`);
      throw new Error(`the shell refused the request (${code})`);
    }
  }

  return {
    async createEmailPasswordSession(email: string, password: string): Promise<AccountUser> {
      return readUser(await call(COMMAND.signIn, { email, password }));
    },

    async createOAuth2Session(_provider: OAuth2Provider, _redirectTo: string): Promise<void> {
      // Google sign in is not in the shells contract's four commands.
      throw new Error("the provider is not supported in the shell");
    },

    async getSession(): Promise<AccountUser> {
      return readUser(await call(COMMAND.resume));
    },

    async exchange(org: string): Promise<ExchangeResult> {
      if (typeof org !== "string" || org.trim() === "") return { ok: false, code: "bad_request" };
      let answer: unknown;
      try {
        answer = await invoke(COMMAND.exchange, { org });
      } catch (rejection) {
        return { ok: false, code: exchangeCode(codeOf(rejection)) };
      }
      const read = readExchange(answer);
      return read === null ? { ok: false, code: "unavailable" } : { ok: true, answer: read };
    },

    async deleteSession(): Promise<void> {
      // The Rust side clears the keychain first and answers an error only when it could not.
      await call(COMMAND.signOut);
    },
  };
}
