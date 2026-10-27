// The harness's calendar day arithmetic, in the house zone's own calendar.
//
// The copies this module replaces added UTC days and then read the zone's
// calendar day, which drifts by one across the zone's DST change: on the
// evening the clocks changed, the stub's nine day due term rendered
// "in 8 days" and the receivables leg went red (CI run 40447496412). The
// offset is added to the zone's own calendar day here, once, so every harness
// copy answers with the dates the app's words assume.
const ZONE = "America/Toronto";
const ZONE_DAY = new Intl.DateTimeFormat("en-CA", { timeZone: ZONE, year: "numeric", month: "2-digit", day: "2-digit" });

/** The zone's calendar date this many days from now's own zone date. */
export function houseDayOffset(offset, now = new Date()) {
  const [y, m, d] = ZONE_DAY.format(now).split("-").map(Number);
  // Noon UTC reads as the same calendar day in the zone on both sides of a DST change.
  return ZONE_DAY.format(new Date(Date.UTC(y, m - 1, d + offset, 12)));
}

/** The zone's calendar date an instant falls on. */
export function houseDay(now = new Date()) {
  return ZONE_DAY.format(now);
}
