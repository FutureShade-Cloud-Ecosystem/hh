// Ported from hardscapeos_dibbits master 87d7941b app/src/_pos-rev8.ts; adapted for HH.

// _pos-rev8.ts: DIB-488 rev 8: the pure logic behind the unified smart field, the header clock,
// the per-line weight column and the footer transaction bar. DOM-free per the `_*.ts` house
// convention (_pos.ts, _pos-flow.ts, _pos-stock.ts, _pos-workspace.ts); the full decision tables
// live in _pos-rev8.test.mjs.

// ── item 1 · the unified smart field's scanner-vs-typist question ───────────────────────────────
//
// A counter scanner is a keyboard that types the whole code in one machine-fast burst and presses
// Enter. A person types at human speed. The unified field needs the difference exactly ONCE: when
// Enter finds NO exact barcode/SKU match. A scanner shooting an unknown label must stay LOUD (the
// scan-box contract: clear the box, keep focus, say so): a silent no-op leaves the cashier
// believing a line was added. A person pressing Enter mid-search just wants the results that are
// already on screen: shouting "no product" over a live result list would be wrong.
//
// The classifier reads the INTER-KEY GAPS (ms). USB HID scanners emit characters a few ms apart:
// far under any human's sustained rate: so the rule is: enough characters to be a code, and a
// MEDIAN gap under the scanner threshold. Median, not mean: one slow first character (the gun
// waking) must not disqualify a burst, and one fast key-roll pair must not promote a typist.

/** Minimum characters before a burst can be called a scan (a 3-char burst is a fat-finger). */
export const SCAN_MIN_CHARS = 4;
/** Median inter-key gap (ms) at or under which the entry reads as a scanner burst. */
export const SCAN_BURST_GAP_MS = 35;

/**
 * isScannerBurst: was this entry typed by a gun? `gapsMs` is the list of gaps between consecutive
 * character inputs (length = chars − 1). Empty/one-char entries and garbage gaps are a typist by
 * construction (the safe answer: a wrong "typist" costs one quiet fall-through; a wrong "scanner"
 * shouts over live results).
 */
export function isScannerBurst(gapsMs: readonly number[], charCount: number): boolean {
  if (charCount < SCAN_MIN_CHARS) return false;
  if (gapsMs.length < SCAN_MIN_CHARS - 1) return false;
  const clean = gapsMs.filter((g) => Number.isFinite(g) && g >= 0);
  if (clean.length < SCAN_MIN_CHARS - 1) return false;
  const sorted = [...clean].sort((a, b) => a - b);
  const median = sorted[Math.floor(sorted.length / 2)];
  return median <= SCAN_BURST_GAP_MS;
}

/** What the unified field does on an Enter whose exact resolve MISSED. */
export type EnterMissOutcome = 'loud-unknown-code' | 'fall-through-to-search';

/**
 * looksLikeProductName: letters and spaces only, so it is a name a person typed and not a label.
 * A barcode is digits and a SKU carries a digit or a dash; both resolve exactly before this is asked.
 */
export function looksLikeProductName(code: string): boolean {
  return /^[A-Za-z][A-Za-z '.]*$/.test(code.trim());
}

/**
 * burstMayCommit: a letters only entry that arrived at scanner speed (a gift card or vendor label
 * reads like this) may commit the top search row only when that row's name begins with the typed
 * text, case blind; any other row is a guess, and the loud unknown code answer stands instead.
 */
export function burstMayCommit(term: string, topName: string | null | undefined): boolean {
  const typed = term.trim().toLowerCase();
  if (typed === '' || !topName) return false;
  return topName.trim().toLowerCase().startsWith(typed);
}

/**
 * enterMissOutcome: the one decision the unified field adds over the two boxes it replaces.
 * A scanner burst keeps the scan box's LOUD unknown-code contract; a typed entry falls through to
 * the live search that is already showing (its own no-match state answers there). A fast typist
 * (or a paste) is as quick as a gun, so when the entry is plainly a name (`code` given, letters
 * only) it is a typed entry whatever its timing.
 */
export function enterMissOutcome(gapsMs: readonly number[], charCount: number, code?: string): EnterMissOutcome {
  if (code !== undefined && looksLikeProductName(code)) return 'fall-through-to-search';
  return isScannerBurst(gapsMs, charCount) ? 'loud-unknown-code' : 'fall-through-to-search';
}

// ── item 2 · the header clock (INJECTED clock: the house rule) ─────────────────────────────────

/**
 * clockLabel: "10:36 AM" from an injected Date. Pure: the caller owns the timer and the clock;
 * this never reads Date.now() (the injected-clock house rule, so tests pin exact strings).
 */
export function clockLabel(now: Date): string {
  let h = now.getHours();
  const ampm = h >= 12 ? 'PM' : 'AM';
  h = h % 12;
  if (h === 0) h = 12;
  const mm = String(now.getMinutes()).padStart(2, '0');
  return `${h}:${mm} ${ampm}`;
}

// ── item 9 · per-line weight (lbs) from the DISCOVERED catalogue data ───────────────────────────
//
// The glance carries weight_kg_per_base (products.weight_per_unit_kg: kg per BASE unit) plus the
// unit meta the stock line already uses. A line's weight is qty × (base units per line unit) ×
// kg-per-base × KG_PER_LB⁻¹: and it exists ONLY when every factor is honestly known:
// • no weight on file → null (the column shows the blank word)
// • line unit is the base unit → factor 1
// • line unit is the sell unit → the glance's sell_factor, only when > 0
// • any other unit / factor 0 → null (never divided-or-multiplied by a guess)

export const LBS_PER_KG = 2.20462;

export interface LineWeightFacts {
  /** kg per BASE unit; 0/undefined = no weight on file. */
  weightKgPerBase: number | undefined;
  baseUom: string;
  sellUom: string;
  /** base units per ONE sell unit; 0 = no conversion on file. */
  sellFactor: number;
}

/** lineWeightLbs: the line's total weight in lbs, or null when it cannot be said honestly. */
export function lineWeightLbs(qty: number, uom: string, f: LineWeightFacts): number | null {
  const kg = f.weightKgPerBase;
  if (!kg || !(kg > 0) || !Number.isFinite(qty) || qty <= 0 || !uom) return null;
  let toBase: number;
  if (uom === f.baseUom) toBase = 1;
  else if (uom === f.sellUom && f.sellFactor > 0) toBase = f.sellFactor;
  else return null; // an unconvertible unit is not a licence to guess
  return qty * toBase * kg * LBS_PER_KG;
}

/** formatLbs: "2,450 lbs": rounded to whole pounds, thousands-grouped, counter-readable. */
export function formatLbs(lbs: number): string {
  const n = Math.round(lbs);
  return `${n.toLocaleString('en-CA')} lbs`;
}

/**
 * ticketWeightTotalLbs: the footer total over the lines that HAVE a weight ("excluded honestly":
 * a null line contributes nothing and `allKnown` says whether the total covers the whole ticket).
 */
export function ticketWeightTotalLbs(lineLbs: ReadonlyArray<number | null>): { totalLbs: number; allKnown: boolean } {
  let total = 0;
  let allKnown = true;
  for (const w of lineLbs) {
    if (w === null) allKnown = false;
    else total += w;
  }
  return { totalLbs: total, allKnown };
}

// ── item 10 · margin formatting (display only: the numbers are the SERVER's) ───────────────────

/** marginPctLabel: 3230 bps → "32.3%" (one decimal, trailing zero kept: a stable column).
 * HH note: kept for its own unit tests and the reference's shape; the mounted
 * surface's margin words route through the app's reviewed margin helper
 * (in the money module, half away from zero), so the same core figure reads the same
 * word everywhere in HH. */
export function marginPctLabel(bps: number): string {
  return `${(bps / 100).toFixed(1)}%`;
}

// ── item 12 · the footer transaction bar ────────────────────────────────────────────────────────

export interface TxbarFacts {
  itemCount: number;
 /** Total known weight in lbs; null when NO line has a weight (the segment then reads "(not weighed)"). */
  totalWeightLbs: number | null;
 /** Server margin bps: undefined when the role-gated data is absent. */
  marginBps: number | undefined;
 /** Server est. profit cents: undefined when absent. */
  estProfitCents: number | undefined;
  /** The per-user quick-hide (eye) toggle. */
  marginHidden: boolean;
}

export interface TxbarSegment {
  id: 'items' | 'weight' | 'margin' | 'profit';
  text: string;
}

/**
 * txbarSegments: WHAT the bar says. Items + weight always (an explicit placeholder when no line
 * has a weight: the reference drew a bare rule there, a character the house forbids, so the
 * placeholder names the fact instead); the margin/profit segments exist ONLY when the role-gated
 * data is present AND the quick-hide is off: an absent segment, never a masked value.
 */
export function txbarSegments(f: TxbarFacts, moneyFmt: (cents: number) => string): TxbarSegment[] {
  const out: TxbarSegment[] = [
    { id: 'items', text: `${f.itemCount} Item${f.itemCount === 1 ? '' : 's'}` },
    { id: 'weight', text: `Total Weight ${f.totalWeightLbs === null ? '(not weighed)' : formatLbs(f.totalWeightLbs)}` },
  ];
  if (!f.marginHidden && f.marginBps !== undefined) {
    out.push({ id: 'margin', text: `Margin ${marginPctLabel(f.marginBps)}` });
  }
  if (!f.marginHidden && f.estProfitCents !== undefined) {
    out.push({ id: 'profit', text: `Est. Profit ${moneyFmt(f.estProfitCents)}` });
  }
  return out;
}

// ── item 4 · the curated chip set, hardened at the edge ─────────────────────────────────────────

/**
 * resolveCuratedChips: the policy's quick_chip_set off the wire: only a non-empty array of
 * non-empty strings counts (trimmed, de-duplicated, capped at 8); anything else resolves to []
 * and the grid falls back to its full chip row (empty-never-error).
 */
export function resolveCuratedChips(wire: unknown): string[] {
  if (!Array.isArray(wire)) return [];
  const out: string[] = [];
  const seen = new Set<string>();
  for (const v of wire) {
    if (typeof v !== 'string') continue;
    const t = v.trim();
    if (t === '' || seen.has(t.toLowerCase())) continue;
    seen.add(t.toLowerCase());
    out.push(t);
    if (out.length >= 8) break;
  }
  return out;
}

// ── DIB-512 · the payment tiles as ONE keyboard stop (roving tabindex) ──────────────────────────

/**
 * payTileStep: where the arrow keys move focus inside the 2×2 tender-tile grid. One Tab stop for
 * the whole group (the critique measured the Cash tile SIXTY Tab stops from the scan field);
 * Left/Right walk the tiles in order, Up/Down jump the row (±2), Home/End go to the edges. Null =
 * not a roving key: the caller lets the keystroke keep its native meaning. Pure and clamped: a
 * degenerate count (< 1) always answers null.
 */
export function payTileStep(current: number, key: string, count: number): number | null {
  if (count < 1) return null;
  const clamp = (n: number): number => Math.min(Math.max(n, 0), count - 1);
  switch (key) {
    case 'ArrowRight':
      return clamp(current + 1);
    case 'ArrowLeft':
      return clamp(current - 1);
    case 'ArrowDown':
      return clamp(current + 2);
    case 'ArrowUp':
      return clamp(current - 2);
    case 'Home':
      return 0;
    case 'End':
      return count - 1;
    default:
      return null;
  }
}
