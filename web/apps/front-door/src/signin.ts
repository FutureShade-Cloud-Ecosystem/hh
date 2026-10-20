// The console mode's pure parts: the words of every sign in state, the methods
// route, the persona route, the configuration error page, the identity tap and
// the landing lookup. Nothing here touches the DOM, so the node suite asserts
// every word as text.
//
// THE WORDS ARE THE SCREEN'S. The core never explains a refusal (ADR-0020:
// one shared body per status), so a 403 means "not set up for this site" here
// and nowhere else, a 429 means "wait", a 503 means "try again". The voice is
// the dealer kit's: sentence case, the decision first, no exclamation marks,
// no status codes, no dashes.
import type { SignInFailureReason } from "@hardscapehouse/auth";
import type { DealerBrand, RuntimeConfigFailure } from "@hardscapehouse/auth/runtime-config";
import { houseZone } from "@hardscapehouse/pos-kit/src/ported/host-facts.js";
import { LIVE_ROLES, type LiveRole } from "./roles.js";

/** The dealer's name and optional branch line from the tenant configuration. */
export function dealerLine(dealer: DealerBrand): string {
  const branch = typeof dealer.branch === "string" ? dealer.branch.trim() : "";
  return branch === "" ? dealer.name : `${dealer.name} · ${branch}`;
}

/** The sentence under the form for each reason a sign in did not land. */
export const SIGN_IN_WORDS: Readonly<Record<string, string>> = {
  bad_credentials: "That sign in did not work. Check the email and the password and try again.",
  forbidden: "This account is not set up for this site. Ask your administrator to add you.",
  rate_limited: "Too many attempts. Wait a moment, then try again.",
  unavailable: "Sign in is unavailable right now. Try again in a moment.",
  offline: "You are offline. Connect to the internet, then try again.",
  no_session: "Your session ended. Sign in again.",
  generic: "Sign in did not complete. Try again.",
  google_failed: "Google sign in did not finish. Try again, or use your email and password.",
  methods_unavailable: "The sign in options could not be loaded. Try again.",
  no_methods: "No sign in method is available for this site yet. Ask your administrator.",
};

/** What the status line says for a reason; empty for the two guards that are not the person's to read. */
export function signInNotice(reason: SignInFailureReason): string {
  if (reason === "cancelled" || reason === "superseded") return "";
  return Object.hasOwn(SIGN_IN_WORDS, reason) ? (SIGN_IN_WORDS[reason] as string) : (SIGN_IN_WORDS["generic"] as string);
}

/** Where focus goes after a failed sign in: back into the password after wrong credentials, else the button. */
export function focusAfter(reason: SignInFailureReason): "password" | "submit" {
  return reason === "bad_credentials" ? "password" : "submit";
}

/** The sentence in the persona card that earned a refusal. */
export const PERSONA_REFUSAL_WORDS: Readonly<Record<string, string>> = {
  unauthorized: "Sign in first, then pick a role.",
  forbidden: "This site does not let you enter as that role.",
  rate_limited: "Too many tries. Wait a moment, then tap again.",
  bad_request: "That role could not be opened. Try again.",
  unavailable: "That role is not available right now. Try again in a moment.",
  offline: "You are offline. Connect to the internet, then tap again.",
};

/** The page a copy shows when its configuration is not usable: honest, and never the demo cards. */
export const CONFIG_ERROR_WORDS: Readonly<Record<RuntimeConfigFailure, { title: string; body: string }>> = {
  unreachable: {
    title: "This site cannot load its settings",
    body: "Check your connection and reload the page. If it keeps happening, tell your administrator.",
  },
  missing: {
    title: "This site is not set up yet",
    body: "Its settings file is missing. Tell your administrator.",
  },
  unreadable: {
    title: "This site's settings are damaged",
    body: "The settings file could not be read. Tell your administrator.",
  },
  no_mode: {
    title: "This site is not set up yet",
    body: "Its settings do not say how people sign in. Tell your administrator.",
  },
  unknown_mode: {
    title: "This site's settings are not recognised",
    body: "They name a way of signing in this version does not know. Tell your administrator.",
  },
  incomplete: {
    title: "This site's settings are incomplete",
    body: "Something it needs to sign people in is missing. Tell your administrator.",
  },
};

const RENDERED_METHODS = ["email_password", "google"] as const;
export type SignInMethod = (typeof RENDERED_METHODS)[number];

/** The methods the screen can draw today, in the dealer's order, once each. Microsoft and magic link draw nothing yet. */
export function methodsFor(codes: readonly unknown[]): SignInMethod[] {
  const out: SignInMethod[] = [];
  for (const code of codes) {
    if ((RENDERED_METHODS as readonly unknown[]).includes(code) && !out.includes(code as SignInMethod)) {
      out.push(code as SignInMethod);
    }
  }
  return out;
}

function base(coreBaseUrl: string): string {
  return coreBaseUrl.replace(/\/+$/, "");
}

/** GET /api/v1/auth/methods?org=<slug>: the codes the dealer allows. A failure is reported, never defaulted. */
export async function fetchMethods(
  fetchFn: typeof fetch,
  coreBaseUrl: string,
  org: string,
): Promise<{ ok: true; methods: SignInMethod[] } | { ok: false; reason: "unavailable" | "offline" }> {
  let response: Response;
  try {
    response = await fetchFn(`${base(coreBaseUrl)}/api/v1/auth/methods?org=${encodeURIComponent(org)}`, {
      method: "GET",
      headers: { Accept: "application/json" },
      credentials: "omit",
      redirect: "error",
    });
  } catch {
    return { ok: false, reason: "offline" };
  }
  if (response.status !== 200) return { ok: false, reason: "unavailable" };
  let body: unknown;
  try {
    body = await response.json();
  } catch {
    return { ok: false, reason: "unavailable" };
  }
  const methods = body !== null && typeof body === "object" ? (body as Record<string, unknown>)["methods"] : undefined;
  if (!Array.isArray(methods)) return { ok: false, reason: "unavailable" };
  return { ok: true, methods: methodsFor(methods) };
}

/** The live role a role code lands on, or null when this site has no screens for it yet. */
export function landingFor(roleCode: string): LiveRole | null {
  const exact = LIVE_ROLES.find((role) => role.id === roleCode);
  if (exact !== undefined) return exact;
  return heldRolesFor(roleCode)[0] ?? null;
}

/** The live roles a signed-in person holds, in order, without duplicates. */
export function heldRolesFor(roleCode: string): LiveRole[] {
  const codes = roleCode
    .split(",")
    .map((part) => part.trim())
    .filter((part) => part !== "");
  const out: LiveRole[] = [];
  for (const code of codes) {
    const live = LIVE_ROLES.find((role) => role.id === code);
    if (live !== undefined && !out.includes(live)) out.push(live);
  }
  return out;
}

/** The person's first name for the role picker greeting, stripped of any parenthetical suffix. */
export function firstNameOf(name: string): string {
  const cleaned = name.replace(/\s*\([^)]*\)\s*/g, " ").trim();
  const token = cleaned.split(/\s+/)[0] ?? "";
  return token;
}

export type DayPeriod = "morning" | "afternoon" | "evening";

/** Period of the clock in the dealer's house zone (never the device's zone). */
export function dayPeriodInZone(now: Date = new Date(), zone: string = houseZone()): DayPeriod {
  let hour = 9;
  try {
    const formatted = new Intl.DateTimeFormat("en-US", {
      timeZone: zone,
      hour: "2-digit",
      hourCycle: "h23",
    }).format(now);
    const parsed = Number.parseInt(formatted, 10);
    if (Number.isFinite(parsed)) hour = parsed;
  } catch {
    // Keep morning fallback if Intl fails on an unrecognised zone.
  }
  if (hour >= 12 && hour < 17) return "afternoon";
  if (hour >= 17) return "evening";
  return "morning";
}

/** Greeting heading for the role and branch picker card. */
export function pickerGreeting(name: string, now: Date = new Date(), zone: string = houseZone()): string {
  const first = firstNameOf(name);
  const period = dayPeriodInZone(now, zone);
  return first === ""
    ? `Good ${period}. What are you working on today?`
    : `Good ${period}, ${first}. What are you working on today?`;
}
