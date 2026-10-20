// The framed band's per-app words (GAPS-r2 row G1): the Dealer Admin's app
// name and the surface labels the demo provides, mapped from the app's own
// surface id so the white band above the app-local header names where the
// person stands ("Dealer Admin > Staff"). The band's element and the string
// composition are the shell chrome's; this file is the dealer-admin slice.

export const FRAMED_BAND_APP = "Dealer Admin";

/** The surface label for one dealer-admin surface: the registry names them. */
export function framedBandSurface(surface: string): string {
  if (surface === "role-config" || surface === "roles") return "Roles";
  if (surface === "sign-in-methods" || surface === "sign-in") return "Sign in methods";
  if (surface === "chat-rules") return "Chat rules";
  if (surface === "pricing") return "Pricing";
  if (surface === "catalog") return "Catalog";
  if (surface === "delivery") return "Delivery";
  if (surface === "warranty") return "Warranty";
  if (surface === "printing") return "Printing";
  if (surface === "exports") return "Exports";
  if (surface === "audit") return "Audit";
  if (surface === "assist") return "Assistant";
  if (surface === "keys") return "Keys";
  if (surface === "deployment") return "Deployment";
  if (surface === "hotkeys") return "Hotkeys";
  return "Staff";
}
