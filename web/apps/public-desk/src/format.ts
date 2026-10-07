// The page's display words and figures: the money formatter and the quote
// status words. Both are ports of the older product's helpers for this page,
// with the money split carried onto this repository's convention: integer
// arithmetic on the absolute value, so no float ever touches the figure, and
// the explicit placeholder for anything the server left out, never a
// plausible zero (an empty or unknown value renders as an explicit
// placeholder, because a confident wrong number is dishonest in a way that
// costs money).

import { formatDollars } from "@hardscapehouse/pos-kit/src/ported/_money-format.js";

export const MONEY_PLACEHOLDER = "(not priced)";

/**
 * Format integer cents as the customer's money string: 9665 becomes "$96.65".
 * The figure itself comes from the shared dollars formatter in pos-kit, the one
 * place a customer facing amount is spelled. Anything that is not an integer (a
 * field the server left out) renders the explicit placeholder.
 */
export function formatMoney(cents: number | null | undefined): string {
  if (typeof cents !== "number" || !Number.isInteger(cents)) return MONEY_PLACEHOLDER;
  return formatDollars(cents);
}

/**
 * QUOTE_STATUS_PHRASE is the six quote states said mid-sentence. The public
 * page puts the status inside a sentence a customer reads ("This quote is ...
 * and is not awaiting acceptance"), where a chip word would either shout or
 * read as a proper noun mid-clause, and DRAFT needs an article to be a
 * sentence at all. An unmapped state degrades honestly: it comes back as
 * itself, lowercased with underscores opened out, so a new state reads oddly
 * in the sentence instead of silently reading as "Unknown".
 */
export const QUOTE_STATUS_PHRASE: Record<string, string> = {
  DRAFT: "a draft",
  SENT: "sent",
  ACCEPTED: "accepted",
  CONVERTED: "converted",
  EXPIRED: "expired",
  LOST: "lost",
};

/** The mid-sentence form of a quote status. */
export function quoteStatusPhrase(status: string): string {
  return QUOTE_STATUS_PHRASE[status] ?? status.toLowerCase().replace(/_/g, " ");
}

/**
 * End a customer sentence exactly once. The page's sentences often end on
 * something that carries its own full stop (the en-CA locale time renders
 * "2:51:42 a.m.", a company name can render "Inc."), and appending the
 * sentence's period blindly is how the thank-you line grew "a.m.". The
 * timestamp's own style is kept untouched: this only decides whether one more
 * period is owed.
 */
export function withFullStop(text: string): string {
  return /[.!?…]\s*$/.test(text) ? text : `${text}.`;
}

/**
 * The quote number with its "#" shown exactly once. The core serves the number with the mark already
 * on it ("#Q-6"), so a page that prefixes its own mark reads "##Q-6"; this takes any run of marks off
 * the front and puts one back.
 */
export function quoteNumberLabel(quoteNo: string): string {
  return `#${quoteNo.replace(/^#+/, "")}`;
}

/**
 * A moment in the page's own words for a customer ("October 6, 2026 at 7:49 p.m. EDT"), never the raw
 * stamp with its seconds. The viewer's own zone is named, so a customer in another zone reads the
 * moment right. An unreadable stamp comes back as it arrived rather than as an invented time.
 */
export function plainWhen(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString("en-CA", {
        year: "numeric",
        month: "long",
        day: "numeric",
        hour: "numeric",
        minute: "2-digit",
        timeZoneName: "short",
      });
}

/**
 * A calendar day ("2026-10-27") in the page's own words ("October 27, 2026"). Read as the day it
 * names, never as a moment, so no time zone can move it; anything that is not a plain day comes back
 * as it arrived.
 */
export function plainDay(day: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(day);
  if (m === null) return day;
  const d = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]));
  if (d.getFullYear() !== Number(m[1]) || d.getMonth() !== Number(m[2]) - 1 || d.getDate() !== Number(m[3])) return day;
  return d.toLocaleDateString("en-CA", { dateStyle: "long" });
}
