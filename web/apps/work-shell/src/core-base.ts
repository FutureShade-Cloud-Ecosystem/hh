// Where the Work shell's server side calls go (PRR-531). In a browser copy
// the shell is served from the core's own origin, so a relative
// /api/v1/auth/sign-out is right. In a native Tauri profile the webview's
// origin is the asset server, and the same relative post would reach a
// static file: the core's base URL has to be resolved and forwarded. It is
// read once at boot, from the page global a native shell can set before the
// bundle runs, or from the served /config.json the front door already reads
// (loadRuntimeConfig); a demo copy, or one whose config cannot be read,
// resolves nothing and the calls stay relative, exactly as before.
import { loadRuntimeConfig } from "@hardscapehouse/auth/runtime-config";

/** The page global a native shell can set: { coreBaseUrl: "https://..." }, public facts only. */
export interface WebConfigGlobal {
  coreBaseUrl?: unknown;
}

/** The slice of window this module reads: the config global and the page origin. */
export interface CoreBaseWindow {
  __HH_WEB_CONFIG__?: WebConfigGlobal;
  location?: { origin?: unknown };
}

function trimmedTrailingSlashes(value: string): string {
  return value.replace(/\/+$/, "");
}

/** A base the page may send a bearer to: https, or http on a loopback host (a dev build), mirroring the Rust side's clean_base_url. */
function secureBase(value: string): boolean {
  if (value.startsWith("https://")) return true;
  return /^http:\/\/(localhost|127\.0\.0\.1)(:[0-9]+)?(\/|$)/.test(value);
}

/**
 * The native dist's own /config.json: the build writes the two public facts
 * it knows (coreBaseUrl, consoleEndpoint), a slimmer body than a console
 * copy's served config, so the runtime config reader refuses it. This reads
 * only that fallback shape, and only when the standard read resolved nothing,
 * and accepts the base only when it is a secure URL. A browser copy never
 * ships the file, so the extra read happens only in a native shell.
 */
async function nativeConfigBase(fetchFn?: typeof fetch): Promise<string | undefined> {
  if (typeof fetchFn !== "function") return undefined;
  let response: Response;
  try {
    response = await fetchFn("/config.json", { cache: "no-store", credentials: "same-origin", headers: { Accept: "application/json" } });
  } catch {
    return undefined;
  }
  if (!response.ok) return undefined;
  let body: unknown;
  try {
    body = await response.json();
  } catch {
    return undefined;
  }
  if (body === null || typeof body !== "object" || Array.isArray(body)) return undefined;
  const base = (body as Record<string, unknown>)["coreBaseUrl"];
  if (typeof base !== "string") return undefined;
  const trimmed = trimmedTrailingSlashes(base.trim());
  return trimmed !== "" && secureBase(trimmed) ? trimmed : undefined;
}

/**
 * The core's base URL for this copy, or undefined when calls should stay
 * relative. The page global wins (a native shell knows its core without a
 * round trip); otherwise the served config answers: a console copy's
 * coreBaseUrl, with an empty one meaning the page's own origin (the same
 * reading the front door gives it); a native dist's own slim config answers
 * last; a demo copy resolves nothing.
 */
export async function resolveCoreBaseUrl(options?: { fetch?: typeof fetch; win?: CoreBaseWindow }): Promise<string | undefined> {
  const global = options?.win?.__HH_WEB_CONFIG__?.coreBaseUrl;
  if (typeof global === "string" && global.trim() !== "") return trimmedTrailingSlashes(global.trim());
  const loaded = await loadRuntimeConfig(options?.fetch);
  if (loaded.ok && loaded.config.mode === "console") {
    if (loaded.config.coreBaseUrl !== "") return trimmedTrailingSlashes(loaded.config.coreBaseUrl);
    const origin = options?.win?.location?.origin;
    if (typeof origin === "string" && origin !== "") return origin;
  }
  return nativeConfigBase(options?.fetch ?? (typeof globalThis.fetch === "function" ? globalThis.fetch.bind(globalThis) : undefined));
}
