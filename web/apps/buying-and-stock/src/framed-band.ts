// The framed band's per-app words (GAPS-r2 row G1): the Buying and Stock app's
// name and the surface labels the demo provides, mapped from the app's own area
// id so the white band above the app-local header names where the person stands
// ("Buying and Stock > Purchase orders"). The band's element and the string
// composition are the shell chrome's; this file is the app's slice.

export const FRAMED_BAND_APP = "Buying and Stock";

/** The surface label for one area id: purchase orders are the board's front door. */
export function framedBandSurface(areaId: string): string {
  if (areaId === "incoming") return "Incoming";
  if (areaId === "receiving") return "Receiving";
  if (areaId === "vendors") return "Vendors";
  if (areaId === "bills") return "Vendor bills";
  if (areaId === "landed") return "Landed cost";
  if (areaId === "inventory") return "Inventory";
  if (areaId === "counts") return "Cycle counts";
  if (areaId === "lookup") return "Stock lookup";
  return "Purchase orders";
}
