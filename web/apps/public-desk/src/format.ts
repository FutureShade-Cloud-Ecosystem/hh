// The page's display words and figures: the money formatter and the quote
// status words. Both are ports of the older product's helpers for this page,
// with the money split carried onto this repository's convention: integer
// arithmetic on the absolute value, so no float ever touches the figure, and
// the explicit placeholder for anything the server left out, never a
// plausible zero (an empty or unknown value renders as an explicit
// placeholder, because a confident wrong number is dishonest in a way that
// costs money).

export const MONEY_PLACEHOLDER = "(not priced)";

/**
 * Format integer cents as the customer's money string: 9665 becomes "$96.65".
 * The split is integer arithmetic on the absolute value. Anything that is not
 * an integer (a field the server left out) renders the explicit placeholder.
 */
export function formatMoney(cents: number | null | undefined): string {
  if (typeof cents !== "number" || !Number.isInteger(cents)) return MONEY_PLACEHOLDER;
  const negative = cents < 0;
  const magnitude = negative ? -cents : cents;
  const whole = Math.floor(magnitude / 100);
  const remainder = magnitude - whole * 100;
  const wholePart = whole.toLocaleString("en-CA");
  const fraction = String(remainder).padStart(2, "0");
  return `${negative ? "-" : ""}$${wholePart}.${fraction}`;
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
