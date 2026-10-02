// Ported from hardscapeos_dibbits master 87d7941b app/src/_date.ts; adapted for HH.

// _date.ts: pure date-entry helpers for the POS terminal.
//
// DIB-200 compact date entry: a counter clerk types the requested date as one run of digits
// (YYYYMMDD) without tabbing year→month→day through the native picker's
// segments. parseCompactDate turns that into the ISO YYYY-MM-DD the sale body carries, and
// passes an already-ISO value (the native <input type="date"> emits ISO) straight through, so
// the compact box and the DIB-198 calendar can feed the same requestedDate. Pure: no DOM: so
// it is unit-testable without a browser. The server re-validates the requested date regardless.

// isRealYmd validates that y-m-d names a real calendar day: an impossible day in a real month
// fails the UTC round trip, which is the check. The round trip also rejects a 0-99 year (Date
// maps those to 1900-1999), which is what we want: a compact date is a full 4-digit year.
function isRealYmd(y: number, m: number, d: number): boolean {
  if (m < 1 || m > 12 || d < 1 || d > 31) return false;
  const dt = new Date(Date.UTC(y, m - 1, d));
  return dt.getUTCFullYear() === y && dt.getUTCMonth() === m - 1 && dt.getUTCDate() === d;
}

/**
 * Parse a requested-date entry into ISO YYYY-MM-DD, or null when it isn't a real date.
 * 'YYYYMMDD' → 'YYYY-MM-DD' (compact 8-digit entry: the DIB-200 ask)
 * an already-ISO value passes through (the native picker's own value)
 * any separators the clerk types are ignored
 * a short run, an empty entry, too many digits, or an impossible day → null
 * Surrounding whitespace is ignored.
 */
export function parseCompactDate(raw: string): string | null {
  const s = (raw ?? '').trim();
  if (!s) return null;
 // Already ISO (what the native date picker emits): validate the calendar date and pass through.
  const iso = /^(\d{4})-(\d{2})-(\d{2})$/.exec(s);
  if (iso) {
    return isRealYmd(+iso[1], +iso[2], +iso[3]) ? s : null;
  }
  // Compact numeric entry: strip any separators, then require exactly 8 digits (YYYYMMDD).
  const digits = s.replace(/\D/g, '');
  if (digits.length !== 8) return null;
  const y = digits.slice(0, 4);
  const m = digits.slice(4, 6);
  const d = digits.slice(6, 8);
  return isRealYmd(+y, +m, +d) ? `${y}-${m}-${d}` : null;
}
