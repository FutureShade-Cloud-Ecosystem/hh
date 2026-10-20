// The framed band's per-app words (GAPS-r2 row G1): the Counter Till's app
// name and the surface labels the demo provides, mapped from the till's own
// route so the white band above the app-local header names where the person
// stands ("Counter Till > New sale"). The band's element and the string
// composition are the shell chrome's; this file is the till's slice.

export const FRAMED_BAND_APP = "Counter Till";

/** The surface label for one till route: the sale is the board's New sale. */
export function framedBandSurface(route: string): string {
  if (route === "returns") return "Returns";
  if (route === "pay-balance") return "Pay balance";
  return "New sale";
}
