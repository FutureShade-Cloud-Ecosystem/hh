// Unknown figures say what is missing in words, never "n/a" (the glossary's ruling): Not priced for a
// price, Not counted for a quantity. The kit's money() and qtyWithUnit() carry the figures that are there.
import { money, qtyWithUnit } from "@hardscapehouse/pro-kit";

/** A price or total as dollars, or Not priced when the core sent none. */
export function priceWords(cents: number | null | undefined): string {
  return typeof cents === "number" && Number.isFinite(cents) ? money(cents) : "Not priced";
}

/** A quantity with its unit, or Not counted when the core sent none. */
export function qtyWords(qty: string | null | undefined, unit: string | null | undefined): string {
  return typeof qty !== "string" || qty.trim() === "" ? "Not counted" : qtyWithUnit(qty, unit);
}
