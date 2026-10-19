// The framed band's per-app words (GAPS-r2 row G1): HH Drive's app name and
// the surface labels the demo already displays, mapped from the run sheet's
// own view so the white band above the app-local header names where the
// person stands ("HH Drive > My run"). The band's element and the string
// composition are the shell chrome's; this file is the drive-run slice.

export const FRAMED_BAND_APP = "HH Drive";

/** The surface label for one drive-run view: the run sheet is the board's My run. */
export function framedBandSurface(view: string): string {
  if (view === "stop") return "Stop";
  if (view === "pod") return "Proof of delivery";
  if (view === "fail") return "Could not deliver";
  if (view === "close") return "End of run";
  if (view === "conflicts") return "Review";
  return "My run";
}
