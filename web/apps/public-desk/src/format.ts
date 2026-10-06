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
