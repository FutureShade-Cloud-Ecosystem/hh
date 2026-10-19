// The framed band's per-app words (GAPS-r2 row G1): the Yard Pick and Receive's app
// name and the surface labels the demo provides, mapped from the yard's own
// view so the white band above the app-local header names where the person
// stands ("Yard Pick and Receive > Pick queue"). The band's element and the string
// composition are the shell chrome's; this file is the yard's slice.

export const FRAMED_BAND_APP = "Yard Pick and Receive";

/** The surface label for one yard view: the queue is the board's Pick queue. */
export function framedBandSurface(view: string): string {
  if (view === "pick") return "Open pick";
  if (view === "changes") return "Changes";
  if (view === "conflicts") return "Review";
  return "Pick queue";
}
