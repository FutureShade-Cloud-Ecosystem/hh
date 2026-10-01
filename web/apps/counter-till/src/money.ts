// The one money helper: the app's single door for turning what the core
// computed into what the counter shows. Money is integer cents everywhere in
// this app, computed by the core and never here; this module formats, and
// parses one input the core must receive as cents (the cash tendered field),
// through the decimal string at the boundary, exactly as the house invariant
// asks. Every figure the till shows goes through formatMoney or formatMargin;
// an unknown or absent figure becomes the explicit placeholder, never a
// plausible zero, because a confident wrong number at a counter costs money.
//
// The placeholder words are deliberate: "(not priced)" says the number is not
// known yet. It never renders "$0.00" for what is merely unanswered.

export const MONEY_PLACEHOLDER = "(not priced)";
export const MARGIN_PLACEHOLDER = "(no margin)";

/**
 * Format integer cents as the counter's money string: 1968 becomes "$19.68",
 * -311 becomes "-$3.11". The split is integer arithmetic on the absolute
 * value, so no float ever touches the figure. Anything that is not an integer
 * (a field the core left out, a glance that answered nothing) renders the
 * explicit placeholder.
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
 * Format a margin the core answered in basis points as the column's percent
 * with one decimal: 1585 becomes "15.9%". Basis points are a ratio, not
 * money, so this is display rounding by integer steps: half-up at the tenth.
 * Absent (the glance's line answered nothing for this line) renders the
 * explicit placeholder, never a zero percent.
 */
export function formatMargin(bps: number | null | undefined): string {
  if (typeof bps !== "number" || !Number.isInteger(bps)) return MARGIN_PLACEHOLDER;
  const tenths = Math.round(bps / 10);
  const negative = tenths < 0;
  const magnitude = negative ? -tenths : tenths;
  const whole = Math.floor(magnitude / 10);
  const tenth = magnitude - whole * 10;
  return `${negative ? "-" : ""}${whole}.${tenth}%`;
}

/**
 * Parse what the cashier typed into the cash tendered field as integer cents,
 * through the decimal string, never through a float multiply: "19.68" becomes
 * 1968, "50" becomes 5000. More than two decimal places, a sign, a thousands
 * separator, or any non-digit is refused as null: the field accepts exactly
 * what a till can be handed. The core owns every consequence (whether the
 * tendered covers the total, what the change due is); this only converts the
 * typed string into the integer the wire type carries.
 */
export function parseAmountToCents(text: string): number | null {
  const trimmed = text.trim();
  if (trimmed === "") return null;
  const match = trimmed.match(/^([0-9]+)(?:\.([0-9]*))?$/);
  if (match === null) return null;
  const dollars = match[1];
  const fraction = (match[2] ?? "").padEnd(2, "0");
  return Number(`${dollars}${fraction}`);
}
