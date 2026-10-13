// The console mode's state machine: one object the app root renders from.
// Signed out, signing in, signed in (at the role entry, then landed), and the
// starting moment before either is known. It owns the auth package's
// controller and custody (the core's token in memory, nothing bearer capable
// in any storage), the dealer's sign in methods, the persona entries and the
// words of every state, so the DOM layer stays a thin renderer and every
// transition is testable with fakes.
//
// WHAT IT NEVER HOLDS. The password is a local of one submit call and is
// dropped when the call returns; no field, notice or log carries it. The
// Console JWT never reaches this object (the adapter mints and drops it).
// Both belong to the rules in web/packages/auth/README.md.
import {
  AuthController,
  SessionStore,
  ThrowSafeMirror,
  SESSION_MIRROR_KEY,
  tapIdentity,
  type AccountSurface,
  type ExchangeUser,
  type RefreshTimers,
  type SignInFailureReason,
  type StorageLike,
} from "@hardscapehouse/auth";
import { enterPersona } from "@hardscapehouse/auth/appwrite-adapter";
import type { ConsoleRuntimeConfig } from "@hardscapehouse/auth/runtime-config";
import { LIVE_ROLES } from "./roles.js";
import {
  PERSONA_REFUSAL_WORDS,
  SIGN_IN_WORDS,
  fetchMethods,
  focusAfter,
  signInNotice,
  type SignInMethod,
} from "./signin.js";

export type ConsolePhase = "starting" | "signed-out" | "signing-in" | "signed-in";

export type MethodsState =
  | { kind: "loading" }
  | { kind: "ready"; methods: SignInMethod[] }
  | { kind: "failed"; reason: "unavailable" | "offline" };

/** The line under the form: words for a screen reader and for the eye, with the tone that colours it. */
export interface Notice {
  text: string;
  tone: "info" | "error";
}

/** Who is signed in: the exchange's own user block, in the front door's terms. */
export interface Who {
  userId: string;
  name: string;
  email: string;
  roleCode: string;
  roleLabel: string;
  /** True when this session is a persona entry rather than the person's own. */
  persona: boolean;
}

export interface ConsoleDeps {
  config: ConsoleRuntimeConfig;
  /** FB Console and the core's exchange, as the auth package's surface. */
  account: AccountSurface;
  fetch: typeof fetch;
  /** Where the display profile mirror lives; the tab's session storage when omitted. */
  storage?: StorageLike;
  now?: () => number;
  timers?: RefreshTimers;
  online?: () => boolean;
  /** The page's current address, read once at start to find a failed Google return. */
  pageUrl?: () => string;
  replaceUrl?: (url: string) => void;
  onChange?: () => void;
}

/** The core's base URL for every call this page makes: the configured one, or the page's own origin when it is empty. */
export function coreBaseFor(config: ConsoleRuntimeConfig, pageOrigin: string): string {
  return (config.coreBaseUrl === "" ? pageOrigin : config.coreBaseUrl).replace(/\/+$/, "");
}

const NO_NOTICE: Notice = { text: "", tone: "info" };

export class ConsoleSession {
  readonly config: ConsoleRuntimeConfig;
  readonly #deps: ConsoleDeps;
  readonly #sessions: SessionStore;
  readonly #controller: AuthController;
  readonly #tap: ReturnType<typeof tapIdentity>;
  readonly #account: AccountSurface;
  #phase: ConsolePhase = "starting";
  #landed = false;
  #methods: MethodsState = { kind: "loading" };
  #notice: Notice = NO_NOTICE;
  #focus: "email" | "password" | "submit" | null = null;
  #persona: ExchangeUser | null = null;
  #personaBusy: string | null = null;
  #refusals = new Map<string, string>();

  constructor(deps: ConsoleDeps) {
    this.#deps = deps;
    this.config = deps.config;
    this.#tap = tapIdentity(deps.account);
    this.#account = this.#tap.surface;
    this.#sessions = new SessionStore({
      mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, deps.storage),
      ...(deps.now === undefined ? {} : { now: deps.now }),
    });
    this.#controller = new AuthController({
      account: this.#account,
      sessions: this.#sessions,
      activeOrg: () => this.config.org,
      ...(deps.now === undefined ? {} : { now: deps.now }),
      ...(deps.timers === undefined ? {} : { timers: deps.timers }),
    });
  }

  // ── what the page reads ─────────────────────────────────────────────────

  get phase(): ConsolePhase {
    return this.#phase;
  }

  /** Whether the person has passed the role entry and is on their home. */
  get landed(): boolean {
    return this.#landed;
  }

  get methods(): MethodsState {
    return this.#methods;
  }

  get notice(): Notice {
    return this.#notice;
  }

  /** The core's bearer for a call the page makes itself; null when signed out. Read per request, never kept. */
  get bearer(): string | null {
    return this.#sessions.current?.token ?? null;
  }

  get who(): Who | null {
    if (this.#phase !== "signed-in") return null;
    const profile = this.#sessions.profile;
    const identity = this.#persona ?? this.#tap.identity();
    if (identity === null && profile === null) return null;
    return {
      userId: identity?.user_id ?? profile?.userId ?? "",
      name: identity?.display_name ?? profile?.name ?? "",
      email: identity?.email ?? profile?.email ?? "",
      roleCode: identity?.role_code ?? "",
      roleLabel: identity?.role_name ?? "",
      persona: this.#persona !== null,
    };
  }

  /** The control focus should return to after the last change, once. */
  takeFocus(): "email" | "password" | "submit" | null {
    const focus = this.#focus;
    this.#focus = null;
    return focus;
  }

  get personaBusy(): string | null {
    return this.#personaBusy;
  }

  /** The refusal standing in one persona card, or the empty string. */
  personaRefusal(id: string): string {
    return this.#refusals.get(id) ?? "";
  }

  // ── transitions ─────────────────────────────────────────────────────────

  async start(): Promise<void> {
    const url = this.#deps.pageUrl?.();
    if (url !== undefined && url !== "") this.#readGoogleReturn(url);
    this.#changed();
    const [, resumed] = await Promise.all([this.#loadMethods(), this.#controller.resume()]);
    if (resumed.ok) {
      this.#phase = "signed-in";
      this.#landed = !this.config.personas;
      this.#changed();
      return;
    }
    this.#phase = "signed-out";
    if (["forbidden", "offline", "unavailable", "rate_limited"].includes(resumed.reason)) this.#error(signInNotice(resumed.reason));
    this.#changed();
  }

  async retryMethods(): Promise<void> {
    this.#methods = { kind: "loading" };
    this.#changed();
    await this.#loadMethods();
    this.#changed();
  }

  /** The form's submit. The password lives in this call and nowhere else. */
  async submit(email: string, password: string): Promise<void> {
    if (this.#phase === "signing-in" || this.#phase === "starting") return;
    const address = email.trim();
    if (address === "" || password === "") {
      this.#error("Enter your email and password.");
      this.#focus = address === "" ? "email" : "password";
      this.#changed();
      return;
    }
    if (!this.#online()) {
      this.#error(signInNotice("offline"));
      this.#focus = "submit";
      this.#changed();
      return;
    }
    this.#phase = "signing-in";
    this.#notice = { text: "Signing in.", tone: "info" };
    this.#changed();
    const result = await this.#controller.signInWithPassword(address, password);
    if (result.ok) {
      this.#phase = "signed-in";
      this.#landed = false;
      this.#persona = null;
      this.#notice = NO_NOTICE;
      this.#changed();
      return;
    }
    this.#phase = "signed-out";
    this.#failed(result.reason);
    this.#changed();
  }

  /** Continue with Google: the console takes the page away and returns to this address. */
  async google(): Promise<void> {
    if (this.#phase === "signing-in" || this.#phase === "starting") return;
    if (!this.#online()) {
      this.#error(signInNotice("offline"));
      this.#focus = "submit";
      this.#changed();
      return;
    }
    const url = this.#deps.pageUrl?.() ?? "";
    let back: string;
    try {
      const here = new URL(url);
      here.search = "";
      here.hash = "";
      back = here.toString();
    } catch {
      this.#error(SIGN_IN_WORDS["generic"] as string);
      this.#changed();
      return;
    }
    this.#notice = { text: "Opening Google.", tone: "info" };
    this.#changed();
    try {
      await this.#account.createOAuth2Session("google", back);
    } catch {
      this.#error(SIGN_IN_WORDS["generic"] as string);
      this.#changed();
    }
  }

  /** "Continue as" the person's own role: the session they signed in with lands them on its home. */
  continueOwn(): void {
    if (this.#phase !== "signed-in") return;
    this.#landed = true;
    this.#changed();
  }

  /** One persona card. The id is the core's persona id, which is the role's flow id ("sales-manager"), not its role code. The refusal, if any, stands in that card; the others keep working. */
  async persona(id: string): Promise<void> {
    if (!this.config.personas || this.#personaBusy !== null) return;
    if (!LIVE_ROLES.some((role) => role.flow === id)) return;
    this.#personaBusy = id;
    this.#refusals.delete(id);
    this.#changed();
    if (!this.#online()) {
      this.#refusals.set(id, PERSONA_REFUSAL_WORDS["offline"] as string);
      this.#personaBusy = null;
      this.#changed();
      return;
    }
    const result = await enterPersona(this.#deps.fetch, this.config.coreBaseUrl, this.config.org, id, this.bearer);
    this.#personaBusy = null;
    if (!result.ok) {
      this.#refusals.set(id, PERSONA_REFUSAL_WORDS[result.reason] as string);
      this.#changed();
      return;
    }
    const answer = result.answer;
    const adopted = this.#controller.adopt({
      token: answer.token,
      tokenExpiresAtMs: answer.expiresAtMs,
      user: { userId: answer.user.user_id, email: answer.user.email, name: answer.user.display_name },
      refresh: false,
    });
    if (!adopted) {
      this.#refusals.set(id, PERSONA_REFUSAL_WORDS["unavailable"] as string);
      this.#changed();
      return;
    }
    this.#persona = { ...answer.user };
    this.#phase = "signed-in";
    this.#landed = true;
    this.#notice = NO_NOTICE;
    this.#changed();
  }

  /** Back to the role entry. From a persona, the person's own session is asked for again; with none, back to sign in. */
  async switchRole(): Promise<void> {
    if (this.#phase !== "signed-in") return;
    if (this.#persona === null) {
      this.#landed = false;
      this.#changed();
      return;
    }
    this.#persona = null;
    this.#tap.forget();
    const resumed = await this.#controller.resume();
    if (resumed.ok) {
      this.#landed = false;
      this.#changed();
      return;
    }
    await this.#controller.signOut();
    this.#reset();
    this.#phase = "signed-out";
    this.#changed();
  }

  async signOut(): Promise<void> {
    await this.#controller.signOut();
    this.#reset();
    this.#phase = "signed-out";
    this.#notice = { text: "You are signed out.", tone: "info" };
    this.#changed();
  }

  /**
   * Called on a timer by the page: a session the controller ended (a refresh
   * the core refused) or a token that ran out leaves custody empty while the
   * page still shows a home, and the page must not keep showing one.
   */
  tick(): void {
    if (this.#phase !== "signed-in" || this.#sessions.current !== null) return;
    this.#reset();
    this.#phase = "signed-out";
    this.#notice = { text: SIGN_IN_WORDS["no_session"] as string, tone: "info" };
    this.#changed();
  }

  /** The browser said it went offline or came back. */
  setOnline(): void {
    if (this.#phase === "signed-out" && !this.#online()) {
      this.#error(signInNotice("offline"));
    } else if (this.#phase === "signed-out" && this.#notice.text === signInNotice("offline")) {
      this.#notice = NO_NOTICE;
    }
    this.#changed();
  }

  // ── private ─────────────────────────────────────────────────────────────

  #online(): boolean {
    return this.#deps.online === undefined ? true : this.#deps.online();
  }

  #error(text: string): void {
    this.#notice = { text, tone: "error" };
  }

  #failed(reason: SignInFailureReason): void {
    const text = signInNotice(reason);
    if (text === "") this.#notice = NO_NOTICE;
    else this.#error(text);
    this.#focus = focusAfter(reason);
  }

  #reset(): void {
    this.#landed = false;
    this.#persona = null;
    this.#tap.forget();
    this.#refusals = new Map();
    this.#personaBusy = null;
  }

  async #loadMethods(): Promise<void> {
    const result = await fetchMethods(this.#deps.fetch, this.config.coreBaseUrl, this.config.org);
    this.#methods = result.ok ? { kind: "ready", methods: result.methods } : { kind: "failed", reason: result.reason };
  }

  /** A return from Google that failed arrives as ?signin=failed: say so once and clean the address. */
  #readGoogleReturn(url: string): void {
    let here: URL;
    try {
      here = new URL(url);
    } catch {
      return;
    }
    if (here.searchParams.get("signin") !== "failed") return;
    this.#error(SIGN_IN_WORDS["google_failed"] as string);
    here.searchParams.delete("signin");
    this.#deps.replaceUrl?.(here.toString());
  }

  #changed(): void {
    this.#deps.onChange?.();
  }
}
