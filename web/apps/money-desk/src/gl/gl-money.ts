// The ledger slice's one money helper: integer cents in, the words a person
// reads out. Money is integer cents everywhere in this app and the server
// computes every figure; this file only formats. An absent figure reads as a
// placeholder, never a plausible zero.
import { formatDollars } from "@hardscapehouse/pos-kit/src/ported/_money-format.js";

export const MONEY_PLACEHOLDER = "(no figure)";

/** 750000 reads "$7,500.00"; anything that is not an integer reads the placeholder. */
export function formatMoney(cents: number | null | undefined): string {
  if (typeof cents !== "number" || !Number.isInteger(cents)) return MONEY_PLACEHOLDER;
  return formatDollars(cents);
}
