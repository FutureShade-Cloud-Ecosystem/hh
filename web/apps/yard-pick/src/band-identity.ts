// What the desk's band says about whose desk it is (item 370). A dealer's own
// box is served a console config: its band names the dealer from that config
// (the box's HH_WEB_DEALER_NAME) and wears no DEMO chip. Only a demo page, one
// served no console config, is DEMO. The public tenant config's name stands in
// where the box's own is blank, and on a demo page.

import type { ConsoleRuntimeConfig } from "@hardscapehouse/auth/runtime-config";

export interface BandIdentity {
  org: string;
  demo: boolean;
}

export function bandIdentity(served: ConsoleRuntimeConfig | null, tenantName: string): BandIdentity {
  const own = served === null ? "" : served.dealer.name.trim();
  return { org: own !== "" ? own : tenantName, demo: served === null };
}

/** The words a demo page adds to its page copy, with the space that joins them; a dealer's own box adds nothing. */
export function demoNote(served: unknown): string {
  return served === null ? " Demo data only." : "";
}
