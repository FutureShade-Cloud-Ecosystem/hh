// The framed band's per-app words (GAPS-r2 row G1): the Money Desk's app
// name and the surface labels the demo provides, mapped from the desk's own
// area so the white band above the app-local header names where the person
// stands ("Money Desk > Receivables"). The band's element and the string
// composition are the shell chrome's; this file is the desk's slice.

export const FRAMED_BAND_APP = "Money Desk";

/** The surface label for one desk area: receivables is the board's default. */
export function framedBandSurface(area: string): string {
  if (area === "ledger") return "Approvals and ledger";
  if (area === "payables") return "Payables";
  if (area === "statements") return "Statements";
  if (area === "bank") return "Bank reconciliation";
  if (area === "tax") return "Tax";
  if (area === "liens") return "Liens";
  if (area === "compliance") return "Compliance";
  return "Receivables";
}
