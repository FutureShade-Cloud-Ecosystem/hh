// The framed band's per-app words (GAPS-r2 row G1): the Quote Desk's app
// name and the surface labels the demo provides, mapped from the desk's own
// route so the white band above the app-local header names where the person
// stands ("Quote Desk > New quote"). The band's element and the string
// composition are the shell chrome's; this file is the desk's slice.

export const FRAMED_BAND_APP = "Quote Desk";

/** The surface label for one desk route kind: the list is the board's Quotes. */
export function framedBandSurface(route: string): string {
  if (route === "compose") return "New quote";
  if (route === "quote") return "Quote";
  if (route === "convert") return "Convert to order";
  return "Quotes";
}
