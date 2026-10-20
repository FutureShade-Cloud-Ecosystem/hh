// Tenant-driven browser tab title. Reads the public dealer config once,
// caches a success, and composes "<dealer> · <app>" for staff apps or
// "Your quote from <dealer>" for the public quote page. A failed read keeps
// the neutral static title and never throws, so the page is never left with
// a blank or wrong title because of a network fault. The neutral title is set
// synchronously before the async read starts, then composed when the read lands.

import { ApiClient, tenantAuth } from "@hardscapehouse/api-client";

interface TenantTitleConfig {
  ok: true;
  dealerName: string;
}
interface TenantTitleFailure {
  ok: false;
}

let readOnce: Promise<TenantTitleConfig | TenantTitleFailure> | null = null;

/** Test seam: clears the cached config read so each test starts cold. */
export function __resetTenantTitleCache(): void {
  readOnce = null;
}

const READ_TIMEOUT_MS = 5000;

function readTenantConfig(): Promise<TenantTitleConfig | TenantTitleFailure> {
  if (readOnce !== null) return readOnce;
  const origin = typeof location !== "undefined" && location?.origin ? location.origin : "";
  const client = new ApiClient({ baseUrl: origin });
  readOnce = tenantAuth
    .tenantGetConfig(client, { timeoutMs: READ_TIMEOUT_MS })
    .then((result) => {
      if (result.ok) return { ok: true as const, dealerName: result.value.dealer_name };
      readOnce = null;
      return { ok: false as const };
    })
    .catch(() => {
      readOnce = null;
      return { ok: false as const };
    });
  return readOnce;
}

/**
 * The dealer's name from the public tenant config, for a band or a header that names the dealer. It
 * shares the one cached read the tab title makes (one request per session, never one per screen) and
 * answers an empty string when the read fails, so a screen shows no name rather than a wrong one.
 */
export async function readDealerName(): Promise<string> {
  const config = await readTenantConfig();
  return config.ok ? config.dealerName : "";
}

export interface ApplyTenantTitleOptions {
  /** When true, the title becomes "Your quote from <dealer>" instead of
   *  "<dealer> · <app>". */
  quote?: boolean;
}

/** Set the tab title from the tenant config. The neutral fallback is written
 *  synchronously first, then replaced with the composed title when the config
 *  read lands. */
export async function applyTenantTitle(appName: string, options?: ApplyTenantTitleOptions): Promise<void> {
  setNeutralTitle(appName, options);
  const config = await readTenantConfig();
  if (!config.ok) return;
  const dealerName = config.dealerName;
  document.title = options?.quote === true ? `Your quote from ${dealerName}` : `${dealerName} · ${appName}`;
}

/** Synchronously set the neutral fallback title. Use when the page must show
 *  the static name immediately, before the config read lands. */
export function setNeutralTitle(appName: string, options?: ApplyTenantTitleOptions): void {
  document.title = options?.quote === true ? "Your quote" : appName;
}
