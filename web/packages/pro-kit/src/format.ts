// The words for money, tax, quantity and days on a Pro screen.
//
// Money is the dealer's US dollars and reads `$1,234.00`, never with currency
// letters (the one formatter is pos-kit's formatDollars, so every Pro screen
// agrees with the counter). Money is integer cents the core computed: nothing
// here adds, multiplies or rounds a price, and tax is shown exactly as the core
// returned it (the dealer's California rate is the core's business, never this
// file's). An absent or unusable figure reads in the glossary's plain words
// ("Not priced", "Not counted", "Not set"), never as "n/a" or a plausible zero.
import { css } from "lit";
import { formatDollars } from "@hardscapehouse/pos-kit/src/ported/_money-format.js";

/** What an unknown money figure reads as. Plain and sentence case; never a zero. */
export const NO_PRICE = "Not priced";

/** What an unknown quantity reads as. Plain and sentence case; never a zero. */
export const NO_COUNT = "Not counted";

/** What an unknown day or moment reads as. Plain and sentence case. */
export const NO_DAY = "Not set";

/** Kept as an alias of NO_PRICE for callers that name the money placeholder NO_FIGURE. */
export const NO_FIGURE = NO_PRICE;

function usable(cents: number | null | undefined): cents is number {
  return typeof cents === "number" && Number.isFinite(cents);
}

export function money(cents: number | null | undefined): string {
  return usable(cents) ? formatDollars(cents) : NO_PRICE;
}

export interface TotalsInput {
  subtotal_cents?: number | null;
  tax_cents?: number | null;
  total_cents?: number | null;
}

/** Subtotal, tax and total as the core returned them, each through money(). No arithmetic. */
export function totalsLine(input: TotalsInput): { subtotal: string; tax: string; total: string } {
  return { subtotal: money(input.subtotal_cents), tax: money(input.tax_cents), total: money(input.total_cents) };
}

/** A quantity as the core's decimal string with its unit after it; no parsing, no rounding. */
export function qtyWithUnit(qty: string | null | undefined, unit: string | null | undefined): string {
  if (typeof qty !== "string" || qty.trim() === "") return NO_COUNT;
  const u = typeof unit === "string" ? unit.trim() : "";
  return u === "" ? qty.trim() : `${qty.trim()} ${u}`;
}

// The dealer's own zone: a day means the dealer's day, never UTC's (a UTC day
// rolls over mid afternoon west of Greenwich). The demo dealer is in San Diego,
// and a dealer config that carries business_zone serves that zone for the session.
export const DEFAULT_PRO_ZONE = "America/Los_Angeles";

let servedZone: string = DEFAULT_PRO_ZONE;

function isValidZone(zone: string): boolean {
  try {
    new Intl.DateTimeFormat("en-US", { timeZone: zone });
    return true;
  } catch {
    return false;
  }
}

/** Record the dealer's IANA time zone when the tenant config carries business_zone. */
export function serveProZoneFrom(config: unknown): string {
  if (config !== null && typeof config === "object" && "business_zone" in config) {
    const raw = (config as { business_zone?: unknown }).business_zone;
    if (typeof raw === "string" && raw.trim() !== "" && isValidZone(raw.trim())) {
      servedZone = raw.trim();
    }
  }
  return servedZone;
}

/** The dealer's active IANA time zone for Pro screens. */
export function proZone(): string {
  return servedZone;
}

/** Reset the dealer's zone to the default between unit tests. */
export function resetProZoneForTest(): void {
  servedZone = DEFAULT_PRO_ZONE;
}

/** A timestamp as the dealer's calendar day in glossary words, for example `Mar 1, 2026`. */
export function houseDay(iso: string | null | undefined, zone: string = proZone()): string {
  if (typeof iso !== "string" || iso.trim() === "") return NO_DAY;
  const when = new Date(iso);
  if (Number.isNaN(when.getTime())) return NO_DAY;
  const tz = isValidZone(zone) ? zone : proZone();
  return new Intl.DateTimeFormat("en-US", { timeZone: tz, month: "short", day: "numeric", year: "numeric" }).format(when);
}

/** A timestamp as the dealer's calendar day and clock time in glossary words, for example `Mar 1, 2026, 8:15 AM`. */
export function houseMoment(iso: string | null | undefined, zone: string = proZone()): string {
  if (typeof iso !== "string" || iso.trim() === "") return NO_DAY;
  const when = new Date(iso);
  if (Number.isNaN(when.getTime())) return NO_DAY;
  const tz = isValidZone(zone) ? zone : proZone();
  return new Intl.DateTimeFormat("en-US", {
    timeZone: tz,
    month: "short",
    day: "numeric",
    year: "numeric",
    hour: "numeric",
    minute: "2-digit",
  }).format(when);
}

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

/**
 * A bare day from the core (an issue or due date, `YYYY-MM-DD`) in glossary words (`Mar 1, 2026`). It is the dealer's
 * day already, so it is read as written and never converted through a zone; anything after the day is ignored.
 */
export function calendarDay(value: string | null | undefined): string {
  if (typeof value !== "string") return NO_DAY;
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(value.trim());
  if (m === null) return NO_DAY;
  const year = Number(m[1]);
  const month = Number(m[2]);
  const day = Number(m[3]);
  if (month < 1 || month > 12 || day < 1 || day > 31) return NO_DAY;
  return `${MONTHS[month - 1]} ${day}, ${year}`;
}

/** The mono face for ids, quantities and totals, with tabular figures so a column lines up. */
export const monoStyles = css`
  .mono {
    font-family: var(--hh-font-mono);
    font-variant-numeric: tabular-nums;
  }
`;
