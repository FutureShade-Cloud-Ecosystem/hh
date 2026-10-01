<<<<<<< HEAD
// The one money formatter for the Counter Till: every amount the till renders
// passes through formatMoney, and no other module in this app formats money or
// does money arithmetic. The amounts arrive as integer cents from the core
// (the house invariant: money is computed by the core, never client-side), so
// this helper only spells an integer for a person: it converts through the
// decimal string, never by multiplying or dividing by 100, because a float
// detour rounds a half-cent the wrong way. A value the core has not stated
// never reaches this helper: the views render an explicit placeholder for
// what they do not know, and the helper refuses a non-integer loudly rather
// than quietly rounding it into a plausible-looking figure.
export function formatMoney(amountCents: number): string {
  if (!Number.isSafeInteger(amountCents)) {
    throw new Error("formatMoney needs an integer amount in cents, and nothing else");
  }
  const sign = amountCents < 0 ? "-" : "";
  const digits = String(Math.abs(amountCents)).padStart(3, "0");
  const whole = digits.slice(0, -2).replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  const fraction = digits.slice(-2);
  return `${sign}$${whole}.${fraction}`;
=======
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
 * money, so this is display rounding by integer steps, half away from zero on
 * BOTH signs, so -155 and 155 disagree by sign alone. Absent (the glance's
 * line answered nothing for this line) renders the explicit placeholder,
 * never a zero percent.
 */
export function formatMargin(bps: number | null | undefined): string {
  if (typeof bps !== "number" || !Number.isInteger(bps)) return MARGIN_PLACEHOLDER;
  const magnitude = Math.abs(bps);
  const tenths = Math.round(magnitude / 10);
  const whole = Math.floor(tenths / 10);
  const tenth = tenths - whole * 10;
  return `${bps < 0 ? "-" : ""}${whole}.${tenth}%`;
}

/**
 * Parse what the cashier typed into the cash tendered field as integer cents,
 * through the decimal string, never through a float multiply: "19.68" becomes
 * 1968, "50" becomes 5000. Refused as null, and nothing is sent: more than
 * two decimal places (the one slip this parser exists to refuse: "20.005" is
 * a mistyped "20.05", never two hundred dollars and five cents), a trailing
 * point with no digits, a sign, a thousands separator, any non-digit, and
 * more than nine digits before the point. The core owns every consequence
 * (whether the tendered covers the total, what the change due is); this only
 * converts the typed string into the integer the wire type carries.
 */
export function parseAmountToCents(text: string): number | null {
  const trimmed = text.trim();
  if (trimmed === "") return null;
  const match = trimmed.match(/^([0-9]{1,9})(?:\.([0-9]{1,2}))?$/);
  if (match === null) return null;
  const dollars = match[1];
  const fraction = (match[2] ?? "").padEnd(2, "0");
  return Number(`${dollars}${fraction}`);
>>>>>>> origin/till/l5-w7
}
