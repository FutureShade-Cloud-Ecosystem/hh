// Tenant-driven browser tab title. Reads the public dealer config once,
// caches it, and composes "<dealer> · <app>" for staff apps or
// "Your quote from <dealer>" for the public quote page. A failed read keeps
// the neutral static title and never throws, so the page is never left with
// a blank or wrong title because of a network fault.

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

function readTenantConfig(): Promise<TenantTitleConfig | TenantTitleFailure> {
  if (readOnce !== null) return readOnce;
  const origin = typeof location !== "undefined" && location?.origin ? location.origin : "";
  const client = new ApiClient({ baseUrl: origin });
  readOnce = tenantAuth
    .tenantGetConfig(client)
    .then((result) => {
      if (result.ok) return { ok: true as const, dealerName: result.value.dealer_name };
      return { ok: false as const };
    })
    .catch(() => ({ ok: false as const }));
  return readOnce;
}

export interface ApplyTenantTitleOptions {
  /** When true, the title becomes "Your quote from <dealer>" instead of
   *  "<dealer> · <app>". */
  quote?: boolean;
}

/** Set the tab title from the tenant config. The call is async only so the
 *  config read can settle; the title is updated synchronously with the neutral
 *  fallback first if the read is not yet cached, then again when the config
 *  arrives. */
export async function applyTenantTitle(appName: string, options?: ApplyTenantTitleOptions): Promise<void> {
  const config = await readTenantConfig();
  const dealerName = config.ok ? config.dealerName : "";
  if (dealerName === "") {
    document.title = options?.quote === true ? "Your quote" : appName;
    return;
  }
  document.title = options?.quote === true ? `Your quote from ${dealerName}` : `${dealerName} · ${appName}`;
}

/** Synchronously set the neutral fallback title. Use when the page must show
 *  the static name immediately, before the config read lands. */
export function setNeutralTitle(appName: string, options?: ApplyTenantTitleOptions): void {
  document.title = options?.quote === true ? "Your quote" : appName;
}
