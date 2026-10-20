// Ported from hardscapeos_dibbits master 87d7941b app/src/_pos-stock.ts; adapted for HH.

// _pos-stock.ts: DIB-488: the stock-status line under a ticket line, derived from the glance.
//
// The counter shows one short honest sentence per item: "In stock · 250 SF available",
// "Low stock · 4 T available", or "Out of stock here". Everything is EXISTING
// availability data: the POS stock glance already returns AVAILABLE (on-hand − reserved) per
// branch in the product's BASE unit, and DIB-488 extended it with the unit meta (sell unit + the
// product_uoms factor) and the dealer's authored 0117 reorder points as low thresholds.
//
// The rules this module owes the reader:
//
// • "Low" exists ONLY where the dealer authored a reorder point for that branch. No threshold on
// file means the app does not get to invent one: the line says in/out and nothing more.
// • The quantity is spoken in the item's SELL unit when a conversion factor is on file
// (available_base ÷ factor), else in the base unit, honestly labelled. Never divided by a
// guess and never by zero.
// • No glance at all (the read hasn't landed, or the branch has no balance row) renders NOTHING.
// Absent data is not an error and not "out of stock": a claim either way would be a lie.
//
// DOM-free per the `_*.ts` convention; the full decision table is in _pos-stock.test.mjs.

/** The glance facts for ONE branch, as the terminal caches them per product. */
export interface StockFacts {
  /** AVAILABLE at the branch, base units. undefined = the glance has no answer here. */
  availableBase: number | undefined;
  /** The dealer's authored low threshold (reorder point) at the branch, base units. undefined = none authored. */
  lowBase: number | undefined;
  /** Stocking base unit ('' when the meta hasn't loaded). */
  baseUom: string;
  /** Declared sell unit ('' when the meta hasn't loaded). */
  sellUom: string;
  /** Base units per ONE sell unit. 0 = no conversion on file. */
  sellFactor: number;
}

/** The tone drives the token the line paints with: never a hex. */
export type StockTone = 'ok' | 'low' | 'out';

export interface StockStatus {
  tone: StockTone;
  /** The whole rendered sentence. */
  label: string;
}

/**
 * formatQty renders a decimal quantity the way a counter reads it: whole numbers bare, fractions to
 * at most one decimal place, trailing zero trimmed (250, 4, 2.5: never 250.0000).
 */
export function formatQty(qty: number): string {
  const rounded = Math.round(qty * 10) / 10;
  return Number.isInteger(rounded) ? String(rounded) : rounded.toFixed(1);
}

/**
 * availableInSellUnit converts the base-unit availability into the sell unit when a factor is on
 * file. factor <= 0 means "no conversion" and the caller shows base units instead: the guard is
 * the whole point (never divide by a guess).
 */
export function availableInSellUnit(availableBase: number, sellFactor: number): number | null {
  if (!(sellFactor > 0)) return null;
  return availableBase / sellFactor;
}

export const OUT_OF_STOCK_WORDS = 'Out of stock here';

/**
 * onHandStatus formats a branch's on-hand count for the counter search row:
 * zero or below reads "Out of stock here" in the out-of-stock tone (never a
 * negative count), and a positive count reads "<n> on hand".
 */
export function onHandStatus(onHand: number | undefined): { tone: 'ok' | 'out'; label: string } | null {
  if (typeof onHand !== 'number' || !Number.isFinite(onHand)) return null;
  if (onHand <= 0) return { tone: 'out', label: OUT_OF_STOCK_WORDS };
  return { tone: 'ok', label: `${onHand} on hand` };
}

/**
 * stockStatus: the one decision. Returns null when there is nothing honest to say (no glance yet):
 * the caller renders nothing, because absent data is neither an error nor a stock claim.
 */
export function stockStatus(f: StockFacts): StockStatus | null {
  if (f.availableBase === undefined) return null;
  if (f.availableBase <= 0) {
    return { tone: 'out', label: OUT_OF_STOCK_WORDS };
  }
  // Speak the sell unit when convertible; else the base unit, honestly labelled.
  const sell = f.sellUom && f.sellUom !== f.baseUom ? availableInSellUnit(f.availableBase, f.sellFactor) : f.availableBase;
  const qty = sell === null ? f.availableBase : sell;
  const uom = sell === null ? f.baseUom : f.sellUom || f.baseUom;
  const amount = `${formatQty(qty)}${uom ? ` ${uom}` : ''} available`;
  if (f.lowBase !== undefined && f.availableBase <= f.lowBase) {
    return { tone: 'low', label: `Low stock · ${amount}` };
  }
  return { tone: 'ok', label: `In stock · ${amount}` };
}
