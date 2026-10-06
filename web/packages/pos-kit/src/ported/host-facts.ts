// The host facts the ported surface's seam file reads, carried beside it in
// the kit so the kit keeps no import into any app (the no reverse dependency
// rule). The three names moved here from the Counter Till's till-client.ts
// when the shared pieces moved into this package: the app's till-client.ts
// re-exports the same three names, so its own callers are unchanged. The
// wording below is theirs.

/**
 * The demo dealer's house zone: the same constant the core itself compiles
 * (core/internal/dashboard/window.go, core/internal/gl/businessday.go). No
 * route serves an organization's zone yet, and the ring's requested_date is
 * contract-required, so the till names a day in THIS zone and never in the
 * browser's UTC. The stub validates the day against the same zone, and the
 * review records for a later item the served zone that should replace this
 * constant when the core offers one.
 */
export const HOUSE_ZONE = "America/Toronto";

/**
 * The calendar day an instant falls on in the given zone, as the date literal
 * the wire type carries: "2026-01-31". Through Intl's formatter, never
 * through toISOString's UTC clock, because the dealer's day is the invariant:
 * a sale rung at nine in the evening in Trenton is that day's sale, though
 * the UTC clock has rolled over.
 */
export function houseDayInZone(zone: string, now: Date = new Date()): string {
  return new Intl.DateTimeFormat("en-CA", {
    timeZone: zone,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).format(now);
}

/**
 * The card tender's token: the fixed demo token the simulator requires to be
 * non-empty. No field in this app takes a card's digits, and no script that
 * reads a card is loaded: the token is a constant, and the second source scan
 * holds that.
 */
export const DEMO_CARD_TOKEN = "demo-card-token";
