// The host facts the ported surface's seam file reads, carried beside it in
// the kit so the kit keeps no import into any app (the no reverse dependency
// rule). The three names moved here from the Counter Till's till-client.ts
// when the shared pieces moved into this package: the app's till-client.ts
// re-exports the same three names, so its own callers are unchanged. The
// wording below is theirs.

/**
 * The zone a dealer that serves none is read in: the Ontario dealer's, and what an older core (one
 * that carries no business_zone on its surface config) means. It is a default, never the dealer's
 * zone: the dealer's own zone is SERVED on the surface config read (business_zone) and held below.
 */
export const DEFAULT_HOUSE_ZONE = "America/Toronto";

let servedZone: string = DEFAULT_HOUSE_ZONE;

function isKnownZone(zone: string): boolean {
  try {
    new Intl.DateTimeFormat("en-CA", { timeZone: zone });
    return true;
  } catch {
    return false;
  }
}

/**
 * Hold the zone a surface config read served. Every app calls this with the body of the config
 * read it already makes; an absent, empty, non string or unknown zone keeps the default, so an
 * older core and an Ontario dealer see no change, and a later read replaces the zone (a change of
 * zone is live at the next read).
 */
export function serveHouseZoneFrom(config: unknown): void {
  const raw = typeof config === "object" && config !== null ? (config as Record<string, unknown>)["business_zone"] : undefined;
  servedZone = typeof raw === "string" && raw !== "" && isKnownZone(raw) ? raw : DEFAULT_HOUSE_ZONE;
}

/** The dealer's business day zone as last served, or the Ontario default when none has been. */
export function houseZone(): string {
  return servedZone;
}

/** Forget the served zone (tests, and a session that ends). */
export function resetHouseZone(): void {
  servedZone = DEFAULT_HOUSE_ZONE;
}

/** The calendar day an instant falls on in the dealer's served zone (the default when none is). */
export function houseDay(now: Date = new Date()): string {
  return houseDayInZone(servedZone, now);
}

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

const DATE_ONLY = /^(\d{4})-(\d{2})-(\d{2})$/;
const TIMESTAMP_PREFIX = /^\d{4}-\d{2}-\d{2}[T ]/;
const ZONE_DESIGNATOR = /(Z|[+-]\d{2}(:?\d{2})?)$/i;

/**
 * The calendar day a wire value names, in the dealer's served zone. A date only value ("2026-01-31")
 * is already a calendar day and is never parsed as UTC midnight; a timestamp converts to the day it
 * falls on in the house zone, so a stamp made this evening reads today, never tomorrow. An empty or
 * missing value answers "".
 */
export function stampDay(stamp: string | null | undefined): string {
  if (stamp === null || stamp === undefined || stamp === "") return "";
  const trimmed = stamp.trim();
  if (DATE_ONLY.test(trimmed)) return trimmed;
  if (TIMESTAMP_PREFIX.test(trimmed)) {
    // A stamp with no zone designator is the core's UTC clock, never the machine's.
    const ms = Date.parse(ZONE_DESIGNATOR.test(trimmed) ? trimmed : `${trimmed.replace(" ", "T")}Z`);
    if (!Number.isNaN(ms)) return houseDay(new Date(ms));
  }
  return trimmed.slice(0, 10);
}

/**
 * The card tender's token: the fixed demo token the simulator requires to be
 * non-empty. No field in this app takes a card's digits, and no script that
 * reads a card is loaded: the token is a constant, and the second source scan
 * holds that.
 */
export const DEMO_CARD_TOKEN = "demo-card-token";
