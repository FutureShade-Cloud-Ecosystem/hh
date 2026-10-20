// The framed band's per-app words (GAPS-r2 row G1): Manager Home's app name
// and the surface labels the demo provides, mapped from the app's own view so
// the white band above the app-local header names where the person stands
// ("Manager Home > Home"). The band's element and the string composition are
// the shell chrome's; this file is Manager Home's slice.

export const FRAMED_BAND_APP = "Manager Home";

/** The surface label for one Manager Home view: the demo's own VIEW_TITLE. */
export function framedBandSurface(view: string): string {
  if (view === "shortages") return "Shortages · open";
  if (view === "receiving") return "Receiving · short";
  if (view === "approvals") return "Approvals";
  if (view === "drivers") return "Drivers";
  if (view === "builder") return "Arrange home";
  if (view === "reporting") return "Reporting";
  if (view === "warranty") return "Warranty";
  if (view === "quick-actions") return "Quick actions";
  return "Home";
}
