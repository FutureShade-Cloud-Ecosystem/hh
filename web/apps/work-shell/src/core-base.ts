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

/**
 * The core's base URL for this copy, or undefined when calls should stay
 * relative. The page global wins (a native shell knows its core without a
 * round trip); otherwise the served config answers: a console copy's
 * coreBaseUrl, with an empty one meaning the page's own origin (the same
 * reading the front door gives it), and a demo copy resolving nothing.
 */
export async function resolveCoreBaseUrl(options?: { fetch?: typeof fetch; win?: CoreBaseWindow }): Promise<string | undefined> {
  const global = options?.win?.__HH_WEB_CONFIG__?.coreBaseUrl;
  if (typeof global === "string" && global.trim() !== "") return trimmedTrailingSlashes(global.trim());
  const loaded = await loadRuntimeConfig(options?.fetch);
  if (!loaded.ok || loaded.config.mode !== "console") return undefined;
  if (loaded.config.coreBaseUrl !== "") return trimmedTrailingSlashes(loaded.config.coreBaseUrl);
  const origin = options?.win?.location?.origin;
  return typeof origin === "string" && origin !== "" ? origin : undefined;
}
