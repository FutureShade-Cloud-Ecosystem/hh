// The micro apps' session custody, chosen once at boot by the served runtime
// config (the front door's /config.json, read by loadAppRuntime before the
// app's element exists).
//
// DEMO MODE (no config, an unreadable one, or mode "demo") is what every app
// did before this module: the auth controller over the app's DemoAccount, the
// demo org, the app's own relative base URL. Nothing here runs.
//
// CONSOLE MODE is the design in the state note, section 7: every page recovers
// its own session on load, and no token ever crosses pages. A partner's page
// calls resume() (FB Console's first party cookie, then the exchange for a core
// token held in this page's memory). A persona has no FB Console session, so the
// front door leaves the one thing a persona needs, its id, in session storage
// (PersonaMemo: a role name, not bearer capable), and the page asks the core
// for a fresh persona session itself: the public copy with no bearer, the
// testers' copy after it has resumed the partner and with the partner's bearer.
// Each hop therefore mints a persona session and writes an audit row.
//
// The apps call begin(appId, onOutcome) once and act on what it reports; they
// never build a surface, read a token or touch storage. The only place a role
// code becomes something an app needs is entryFor below.
import {
  AuthController,
  PersonaMemo,
  tapIdentity,
  type AccountSurface,
  type ExchangeUser,
  type RefreshTimers,
  type SessionStore,
  type SignInFailureReason,
  type StorageLike,
} from "@hardscapehouse/auth";
import { createAppwriteAccountSurface, enterPersona } from "@hardscapehouse/auth/appwrite-adapter";
import type { EntryView } from "./entry.js";
import { loadRuntimeConfig, type ConsoleRuntimeConfig, type RuntimeConfig } from "@hardscapehouse/auth/runtime-config";

// ── the role matrix, once ─────────────────────────────────────────────────

export type RoleCode =
  | "inside_sales"
  | "sales_manager"
  | "yard_manager"
  | "yard_team"
  | "payables_receivables"
  | "procurement_manager"
  | "driver"
  | "financial_controller";

/** The demo flow id each live role maps to (the flows table's own mapping; the persona route takes the same ids). */
export const ROLE_FLOWS: Readonly<Record<RoleCode, string>> = {
  inside_sales: "sales",
  sales_manager: "sales-manager",
  yard_manager: "yard-manager",
  yard_team: "yard-team",
  payables_receivables: "payables-receivables",
  procurement_manager: "procurement-manager",
  driver: "driver",
  financial_controller: "financial-controller",
};

export const APP_IDS = [
  "counter-till",
  "quote-desk",
  "manager-home",
  "drive-run",
  "buying-and-stock",
  "money-desk",
  "yard-pick",
  "dealer-admin",
  "role-template",
] as const;
export type AppId = (typeof APP_IDS)[number];

/**
 * The role codes the decided matrix gives each app: the front door's role
 * catalog tiles (a test in the front door holds this table to them). A role not
 * listed has no screens in the app and goes back to the front door.
 */
export const APP_ROLES: Readonly<Record<AppId, readonly RoleCode[]>> = {
  "counter-till": ["inside_sales", "sales_manager"],
  "quote-desk": ["inside_sales", "sales_manager"],
  "manager-home": ["sales_manager", "yard_manager", "payables_receivables", "procurement_manager", "financial_controller"],
  "drive-run": ["driver"],
  "buying-and-stock": ["yard_manager", "procurement_manager"],
  "money-desk": ["payables_receivables", "financial_controller"],
  "yard-pick": ["yard_manager", "yard_team"],
  "dealer-admin": ["sales_manager"],
  // The role template admits every live role until the role that copies it narrows this list (ROLE-GOES-LIVE.md).
  "role-template": ["inside_sales", "sales_manager", "yard_manager", "yard_team", "payables_receivables", "procurement_manager", "driver", "financial_controller"],
};

/** What an app needs from the session the exchange named, in its own terms. */
export interface AppEntry {
  userId: string;
  email: string;
  name: string;
  roleCode: string;
  /** The role's label from the exchange (staff_role.label): the header's role label. */
  roleLabel: string;
  /** The demo flow id of the role, for the app code that keys on it. */
  flow: string;
  tier: number;
  scope: string;
  /** True when the session is a persona entry rather than the person's own. */
  persona: boolean;
}

/** The exchange's user block as an app's entry, or null when the matrix gives this app no screens for the role. */
export function entryFor(appId: AppId, user: ExchangeUser, persona: boolean): AppEntry | null {
  const allowed = APP_ROLES[appId] as readonly string[];
  if (!allowed.includes(user.role_code)) return null;
  return {
    userId: user.user_id,
    email: user.email,
    name: user.display_name,
    roleCode: user.role_code,
    roleLabel: user.role_name,
    flow: ROLE_FLOWS[user.role_code as RoleCode],
    tier: user.tier,
    scope: user.scope,
    persona,
  };
}

// ── the served config, read once before the app exists ────────────────────

let served: ConsoleRuntimeConfig | null = null;

/**
 * Read /config.json once. Only a usable console config switches an app to
 * console mode; a missing, unreadable or demo config leaves the app exactly as
 * it was (the demo cards and the front door's handoff).
 */
export async function loadAppRuntime(fetchFn?: typeof fetch): Promise<void> {
  const result = await loadRuntimeConfig(fetchFn);
  served = result.ok && result.config.mode === "console" ? result.config : null;
}

/** The console config this page was served with, or null in demo mode. */
export function servedConsoleConfig(): ConsoleRuntimeConfig | null {
  return served;
}

/** The entry view an app shows while it has no session and the demo cards do not apply: its existing unavailable state. */
export function entryUnavailable(): EntryView {
  return { flows: { kind: "unavailable", status: null }, busy: false, message: null };
}

// ── custody ───────────────────────────────────────────────────────────────

export type AppStart =
  | { kind: "ready"; entry: AppEntry }
  | { kind: "front-door"; why: "no_session" | "forbidden" | "wrong_role" | "config" | "persona_refused" | "ended" }
  | { kind: "unavailable"; reason: "offline" | "unavailable" | "rate_limited" };

export interface BootDeps {
  sessions: SessionStore;
  /** The app's demo account: the controller's surface in demo mode. */
  demoAccount: AccountSurface;
  demoOrg: string;
  /** Replaces the REST adapter in console mode (tests). */
  surface?: AccountSurface;
  fetch?: typeof fetch;
  /** Where the persona memo lives; the tab's session storage when omitted. */
  storage?: StorageLike;
  timers?: RefreshTimers;
  now?: () => number;
  /** Navigate the window to a URL; window.location.assign when omitted. */
  navigate?: (url: string) => void;
  pageOrigin?: string;
}

/** The build's BASE_URL (Vite sets it); "/" when there is none. Read loosely: these apps carry no Vite client types. */
function buildBase(): string {
  return (import.meta as unknown as { env?: { BASE_URL?: string } }).env?.BASE_URL ?? "/";
}

export interface Custody {
  readonly mode: "demo" | "console";
  readonly controller: AuthController;
  /** The surface the controller runs over. */
  readonly surface: AccountSurface;
  readonly sessions: SessionStore;
  /** The org slug for X-Org on every call. */
  readonly org: string;
  /** The base URL for the app's api clients: the configured core, or "" for the page's own origin. */
  readonly coreBaseUrl: string;
  /** The exchange's user block of the session in custody (a persona's own for a persona), or null. */
  identity(): ExchangeUser | null;
  /** Console mode: recover the session and report each outcome until it is final. Demo mode: nothing. */
  begin(appId: AppId, onOutcome: (outcome: AppStart) => void): void;
  /** Sign out for good: end the session, forget the persona id, stop the loops, then go to the front door. True when it navigated. */
  leave(baseUrl?: string): Promise<boolean>;
  /**
   * Go to the front door when the app is served under a prefix (the front door is at the origin
   * root); true when it navigated. The base is the build's BASE_URL unless a test names one.
   */
  toFrontDoor(baseUrl?: string): boolean;
}

const RETRY_FIRST_MS = 15_000;
const RETRY_CAP_MS = 120_000;
const WATCH_MS = 2_000;

function transientReason(reason: SignInFailureReason): "offline" | "unavailable" | "rate_limited" | null {
  return reason === "offline" || reason === "unavailable" || reason === "rate_limited" ? reason : null;
}

/** Why a refusal that ends the attempt sends the person back to the front door. */
function endWhy(reason: SignInFailureReason): "no_session" | "forbidden" | "config" {
  if (reason === "forbidden") return "forbidden";
  if (reason === "failed") return "config";
  return "no_session";
}

export function bootCustody(config: RuntimeConfig | null, deps: BootDeps): Custody {
  const navigate =
    deps.navigate ??
    ((url: string): void => {
      (globalThis as unknown as { window: { location: { assign: (u: string) => void } } }).window.location.assign(url);
    });
  const toFrontDoor = (baseUrl: string = buildBase()): boolean => {
    if (baseUrl === "/") return false;
    navigate("/");
    return true;
  };

  if (config === null || config.mode !== "console") {
    const controller = new AuthController({ account: deps.demoAccount, sessions: deps.sessions });
    return {
      mode: "demo",
      controller,
      surface: deps.demoAccount,
      sessions: deps.sessions,
      org: deps.demoOrg,
      coreBaseUrl: "",
      identity: () => null,
      begin: () => {},
      leave: async (baseUrl = buildBase()) => {
        await controller.signOut();
        return toFrontDoor(baseUrl);
      },
      toFrontDoor,
    };
  }

  const cfg = config;
  const origin = deps.pageOrigin ?? (globalThis as { window?: { location?: { origin?: string } } }).window?.location?.origin ?? "";
  const rest =
    deps.surface ??
    createAppwriteAccountSurface({
      endpoint: cfg.consoleEndpoint,
      projectId: cfg.consoleProjectId,
      coreBaseUrl: cfg.coreBaseUrl === "" ? origin : cfg.coreBaseUrl,
      ...(deps.fetch === undefined ? {} : { fetch: deps.fetch }),
    });
  const tap = tapIdentity(rest);
  const memo = new PersonaMemo(deps.storage);
  const fetchFn = deps.fetch ?? ((input: RequestInfo | URL, init?: RequestInit) => globalThis.fetch(input, init));
  const timers: RefreshTimers = deps.timers ?? {
    setTimeout: (callback, ms) => globalThis.setTimeout(callback, ms),
    clearTimeout: (handle) => globalThis.clearTimeout(handle as ReturnType<typeof globalThis.setTimeout>),
  };
  const controller = new AuthController({
    account: tap.surface,
    sessions: deps.sessions,
    activeOrg: () => cfg.org,
    ...(deps.now === undefined ? {} : { now: deps.now }),
    ...(deps.timers === undefined ? {} : { timers: deps.timers }),
  });
  const publicCopy = cfg.personas && cfg.personasOpen;
  const testersCopy = cfg.personas && !cfg.personasOpen;

  let persona: ExchangeUser | null = null;
  /** True once a persona session ended at its window and custody was cleared locally: leave() then has no console session to delete. */
  let endedLocally = false;
  let generation = 0;
  let pending: unknown = null;

  const stopTimer = (): void => {
    if (pending !== null) {
      timers.clearTimeout(pending);
      pending = null;
    }
  };

  /** One attempt at recovering this page's session. */
  async function attempt(appId: AppId): Promise<AppStart> {
    const remembered = memo.read();
    if (remembered !== null && !cfg.personas) {
      memo.clear(); // a copy without personas has none to enter
    }
    if (remembered !== null && cfg.personas) {
      let bearer: string | null = null;
      if (testersCopy) {
        // After a persona entry the front door mirrored the persona's profile; resuming over it would let the
        // controller's own refresh (armed by a transient) see the partner answer for someone else and sign out,
        // which deletes the partner's console session. The profile is display only: forget it, keep the console.
        if (deps.sessions.profile?.userId.startsWith("persona:")) deps.sessions.clear();
        const partner = await controller.resume();
        if (!partner.ok) {
          const transient = transientReason(partner.reason);
          return transient !== null ? { kind: "unavailable", reason: transient } : { kind: "front-door", why: endWhy(partner.reason) };
        }
        bearer = deps.sessions.current?.token ?? null;
      } else if (!publicCopy) {
        return { kind: "front-door", why: "config" };
      }
      const entered = await enterPersona(fetchFn, cfg.coreBaseUrl, cfg.org, remembered, bearer);
      if (!entered.ok) {
        if (entered.reason === "offline" || entered.reason === "unavailable" || entered.reason === "rate_limited") {
          return { kind: "unavailable", reason: entered.reason };
        }
        memo.clear();
        return { kind: "front-door", why: "persona_refused" };
      }
      const answer = entered.answer;
      const entry = entryFor(appId, answer.user, true);
      if (entry === null) return { kind: "front-door", why: "wrong_role" };
      // A persona has no console session behind it: no refresh (it would end the session with no_session).
      const adopted = controller.adopt({
        token: answer.token,
        tokenExpiresAtMs: answer.expiresAtMs,
        user: { userId: answer.user.user_id, email: answer.user.email, name: answer.user.display_name },
        refresh: false,
      });
      if (!adopted) return { kind: "unavailable", reason: "unavailable" };
      persona = { ...answer.user };
      return { kind: "ready", entry };
    }
    const resumed = await controller.resume();
    if (!resumed.ok) {
      const transient = transientReason(resumed.reason);
      return transient !== null ? { kind: "unavailable", reason: transient } : { kind: "front-door", why: endWhy(resumed.reason) };
    }
    const user = tap.identity();
    if (user === null) return { kind: "front-door", why: "config" };
    const entry = entryFor(appId, user, false);
    return entry === null ? { kind: "front-door", why: "wrong_role" } : { kind: "ready", entry };
  }

  return {
    mode: "console",
    controller,
    surface: tap.surface,
    sessions: deps.sessions,
    org: cfg.org,
    coreBaseUrl: cfg.coreBaseUrl,
    identity: () => persona ?? tap.identity(),
    begin(appId, onOutcome) {
      const mine = ++generation;
      let wait = RETRY_FIRST_MS;
      const live = (): boolean => mine === generation;
      const watch = (): void => {
        pending = timers.setTimeout(() => {
          pending = null;
          if (!live()) return;
          if (deps.sessions.current === null) {
            if (deps.sessions.profile === null) {
              // Custody was cleared (an ending refusal, a sign out elsewhere): the session is over.
              onOutcome({ kind: "front-door", why: "ended" });
              return;
            }
            if (persona !== null) {
              // A persona has no console session and no refresh: its window ran out. Forget it locally and go to
              // the front door; the partner's console session is not ours to delete.
              memo.clear();
              persona = null;
              tap.forget();
              deps.sessions.clear();
              endedLocally = true;
              onOutcome({ kind: "front-door", why: "ended" });
              return;
            }
            // The token ran out but custody still holds the person (an exchange outage that outlasted it): not
            // an end. Say unavailable and recover on the backoff; the console cookie decides.
            onOutcome({ kind: "unavailable", reason: "unavailable" });
            pending = timers.setTimeout(() => {
              pending = null;
              if (live()) void run();
            }, wait);
            wait = Math.min(wait * 2, RETRY_CAP_MS);
            return;
          }
          watch();
        }, WATCH_MS);
      };
      const run = async (): Promise<void> => {
        const outcome = await attempt(appId);
        if (!live()) return;
        onOutcome(outcome);
        if (outcome.kind === "ready") {
          watch();
        } else if (outcome.kind === "unavailable") {
          pending = timers.setTimeout(() => {
            pending = null;
            if (live()) void run();
          }, wait);
          wait = Math.min(wait * 2, RETRY_CAP_MS);
        }
      };
      void run();
    },
    async leave(baseUrl = buildBase()) {
      generation += 1; // every loop and any attempt in flight stops reporting
      stopTimer();
      memo.clear();
      persona = null;
      tap.forget();
      if (endedLocally) {
        endedLocally = false;
        deps.sessions.clear();
      } else {
        await controller.signOut();
      }
      return toFrontDoor(baseUrl);
    },
    toFrontDoor,
  };
}
