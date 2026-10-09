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
