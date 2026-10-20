// The runtime configuration a browser copy is served: /config.json, read once
// at boot, shared by the front door and by every micro app that recovers its
// session the way the front door does. It is a separate entry (the
// "./runtime-config" export): the package root imports nothing from here.
//
// THE MODE HAS NO DEFAULT. `console` is the real sign in against FB Console
// and the core's exchange; `demo` is the tap a role page of the demo boxes. A
// missing file, an unreadable one, no mode and an unknown mode are each a
// refusal with its own reason, so a copy that lost its configuration shows an
// honest error and never falls back to the demo cards.
//
// The file holds public facts only: a console endpoint, a project id, an org
// slug, a dealer name, two flags. It never holds a key, a token or a secret,
// and fields this module does not know are dropped rather than carried.

export type RuntimeMode = "console" | "demo";

/** The dealer the console copy signs into: the name on the screen and the built in mark to draw. */
export interface DealerBrand {
  name: string;
  /** A built in tenant mark by its code ("gable"); absent means a plain initial. */
  mark?: string;
  /** Optional branch name from the tenant configuration. */
  branch?: string;
}

export type PersonaCaller = "public" | "partner";

/** The personas only the testers' copy shows: the admin roles (principal ruling A6). Ids are the core's persona ids. */
export const ADMIN_PERSONA_IDS: readonly string[] = ["general-manager", "financial-controller", "hr", "dealer-admin"];

/** Whether a persona may be shown, or entered, on this deployment. A config without the field is judged by personasOpen. */
export function personaVisible(config: { personasOpen: boolean; personaCaller?: PersonaCaller }, id: string): boolean {
  const caller = config.personaCaller ?? (config.personasOpen ? "public" : "partner");
  return caller !== "public" || !ADMIN_PERSONA_IDS.includes(id);
}

export interface DemoRuntimeConfig {
  mode: "demo";
}

export interface ConsoleRuntimeConfig {
  mode: "console";
  /** The org slug the exchange and the methods route are asked for. */
  org: string;
  /** FB Console's API endpoint. */
  consoleEndpoint: string;
  /** The HH project id in FB Console. */
  consoleProjectId: string;
  /** The core's base URL; empty means the page's own origin. */
  coreBaseUrl: string;
  /** The persona route exists on this deployment (the testers' copy and the public copy). */
  personas: boolean;
  /**
   * Who calls the persona route on this deployment, mirroring the core's CORE_PERSONA_CALLER: `public` (no
   * bearer, the public copy) or `partner` (a partner's bearer, the testers' copy). Absent, it follows
   * personasOpen. The public copy never shows the admin personas (principal ruling A6).
   */
  personaCaller: PersonaCaller;
  /** The persona route answers without a bearer (the public copy), so the cards show before any sign in. */
  personasOpen: boolean;
  dealer: DealerBrand;
}

export type RuntimeConfig = DemoRuntimeConfig | ConsoleRuntimeConfig;

export type RuntimeConfigFailure =
  | "unreachable"
  | "missing"
  | "unreadable"
  | "no_mode"
  | "unknown_mode"
  | "incomplete";

export type RuntimeConfigResult = { ok: true; config: RuntimeConfig } | { ok: false; reason: RuntimeConfigFailure };

const ORG_PATTERN = /^[a-z0-9][a-z0-9-]{0,62}$/;

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

/** https, or http on a local development host; anything else cannot carry a password. */
function secureUrl(value: unknown): value is string {
  if (typeof value !== "string" || value === "") return false;
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    return false;
  }
  const local = url.protocol === "http:" && (url.hostname === "localhost" || url.hostname === "127.0.0.1");
  return url.protocol === "https:" || local;
}

function flag(raw: Record<string, unknown>, name: string): boolean | null {
  const value = raw[name];
  if (value === undefined) return false;
  return typeof value === "boolean" ? value : null;
}

function dealerOf(value: unknown): DealerBrand | null {
  if (!isRecord(value)) return null;
  const name = value["name"];
  if (typeof name !== "string" || name.trim() === "") return null;
  const mark = value["mark"];
  if (mark !== undefined && (typeof mark !== "string" || mark === "")) return null;
  const branch = value["branch"];
  if (branch !== undefined && (typeof branch !== "string" || branch.trim() === "")) return null;
  const out: DealerBrand = { name };
  if (mark !== undefined) out.mark = mark;
  if (branch !== undefined) out.branch = branch.trim();
  return out;
}

/** Read the parsed JSON field by field; never cast. */
export function parseRuntimeConfig(raw: unknown): RuntimeConfigResult {
  if (!isRecord(raw)) return { ok: false, reason: "unreadable" };
  const mode = raw["mode"];
  if (mode === undefined) return { ok: false, reason: "no_mode" };
  if (mode === "demo") return { ok: true, config: { mode: "demo" } };
  if (mode !== "console") return { ok: false, reason: "unknown_mode" };

  const org = raw["org"];
  const endpoint = raw["consoleEndpoint"];
  const project = raw["consoleProjectId"];
  const core = raw["coreBaseUrl"] === undefined ? "" : raw["coreBaseUrl"];
  const personas = flag(raw, "personas");
  const personasOpen = flag(raw, "personasOpen");
  const dealer = dealerOf(raw["dealer"]);
  const rawCaller = raw["personaCaller"];
  const incomplete: RuntimeConfigResult = { ok: false, reason: "incomplete" };
  if (typeof org !== "string" || !ORG_PATTERN.test(org)) return incomplete;
  if (!secureUrl(endpoint)) return incomplete;
  if (typeof project !== "string" || project === "") return incomplete;
  if (core !== "" && !secureUrl(core)) return incomplete;
  if (personas === null || personasOpen === null || dealer === null) return incomplete;
  if (rawCaller !== undefined && rawCaller !== "public" && rawCaller !== "partner") return incomplete;
  // The caller must agree with whether the route answers without a bearer.
  if (rawCaller !== undefined && (rawCaller === "public") !== personasOpen) return incomplete;
  const personaCaller: PersonaCaller = personasOpen ? "public" : "partner";
  return {
    ok: true,
    config: {
      mode: "console",
      org,
      consoleEndpoint: endpoint,
      consoleProjectId: project,
      coreBaseUrl: core as string,
      personas,
      personasOpen,
      personaCaller,
      dealer,
    },
  };
}

/**
 * Fetch /config.json and parse it. Same origin credentials only (a gated copy
 * needs the browser's own authentication entry, and omit would drop it) and no
 * cache: a stale copy is a wrong mode.
 */
export async function loadRuntimeConfig(
  fetchFn: typeof fetch = globalThis.fetch.bind(globalThis),
  url = "/config.json",
): Promise<RuntimeConfigResult> {
  let response: Response;
  try {
    response = await fetchFn(url, { cache: "no-store", credentials: "same-origin", headers: { Accept: "application/json" } });
  } catch {
    return { ok: false, reason: "unreachable" };
  }
  if (response.status !== 200) return { ok: false, reason: "missing" };
  let body: unknown;
  try {
    body = await response.json();
  } catch {
    return { ok: false, reason: "unreadable" };
  }
  return parseRuntimeConfig(body);
}
