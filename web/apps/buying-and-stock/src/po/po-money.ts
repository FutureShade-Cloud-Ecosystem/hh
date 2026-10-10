// The procurement slice's one formatting helper: integer cents in, the words a
// person reads out, and quantities as plain numbers with their unit. The app
// computes no money: the server decides every figure and this file only formats
// it. An absent figure reads as a placeholder, never a plausible zero.
import { formatDollars } from "@hardscapehouse/pos-kit/src/ported/_money-format.js";

export const MONEY_PLACEHOLDER = "(no figure)";
export const QTY_PLACEHOLDER = "(unknown)";

/** 201600 reads "$2,016.00"; anything that is not an integer reads the placeholder. */
export function formatMoney(cents: number | null | undefined): string {
  if (typeof cents !== "number" || !Number.isInteger(cents)) return MONEY_PLACEHOLDER;
  return formatDollars(cents);
}

// A piece is the default unit: the board draws it bare, every other unit beside the figure.
const BARE_UNITS = new Set(["pc", "ea", "each"]);

/** 1200 pieces read "1,200"; 40 tonnes read "40 T"; a negative keeps its minus sign. */
export function formatQty(qty: number | null | undefined, uom: string | null | undefined): string {
  if (typeof qty !== "number" || !Number.isFinite(qty)) return QTY_PLACEHOLDER;
  const figure = qty.toLocaleString("en-CA", { maximumFractionDigits: 3 });
  const unit = (uom ?? "").trim();
  return unit === "" || BARE_UNITS.has(unit.toLowerCase()) ? figure : `${figure} ${unit}`;
}

export type CostCheck = { ok: true; cents: number } | { ok: false; words: string };

/**
 * What the buyer typed into a unit cost field, as the cents the core expects: through the decimal
 * string, never a float multiply ("0.1" and "19.99" become exactly 10 and 1999 cents; Number(x) *
 * 100 already misses). A figure with more than two decimals is refused in words: the core holds
 * whole cents and cannot carry a third, and rounding what the buyer typed would order a price
 * nobody chose. A negative figure passes through as negative cents: the core refuses it in its own
 * words beside the button, rather than this app inventing a sentence the core lacks. Call only
 * with a non-empty text; an empty field is the caller's own "nothing typed".
 */
export function costCentsFromTyped(text: string): CostCheck {
  const value = text.trim();
  const match = value.match(/^-?(\d+)(?:\.(\d{1,2}))?$/);
  if (match === null) return { ok: false, words: "Write the unit cost as dollars and cents, two decimals at most." };
  const whole = Number(match[1]);
  const cents = match[2] === undefined ? 0 : Number(match[2].padEnd(2, "0"));
  return { ok: true, cents: value.startsWith("-") ? -(whole * 100 + cents) : whole * 100 + cents };
}
