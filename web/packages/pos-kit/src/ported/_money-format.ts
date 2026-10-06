// Ported from hardscapeos_dibbits master 87d7941b app/src/_money-format.ts; adapted for HH.

// _money-format.ts: the one place integer cents become a readable amount.
//
// WHAT THIS FILE IS FOR, and what it deliberately is NOT.
//
// A sweep on found TWENTY-EIGHT money formatters in this app, disagreeing on four
// separate axes:
//
// 1. thousands separators: 15 had them, 13 did not
// 2. negatives : `(1,234.00)` in gl/inventory, `-1,234.00` in bankrec/finstmt
// 3. the prefix : a currency mark on some, a bare `$` in tax-api
// 4. the arithmetic : most use toLocaleString; finstmt floors and pads by hand
//
// Axis 1 is not a matter of taste. `10870.00` on a receivables screen is wrong, and it was
// visible: AR printed `10170.00` for the same invoice the invoices screen printed as
// `10,170.00`. Same money, two screens, two answers, with an accountant reviewing the financial
// module.
//
// Axes 2 and 3 ARE a matter of taste, and specifically somebody else's: whether a credit reads as
// parentheses or a minus is an accounting convention, and picking one here would be a UI decision
// quietly overriding a finance one. So this file does the part that has a right answer and nothing
// else. Each caller keeps its own sign and prefix decoration until the finance walkthrough settles
// them (Q-091), at which point there is ONE place to change instead of twenty-eight.
//
// Cents in, no currency mark out, no sign out. `1087000` becomes `10,870.00`.

/**
 * The MAGNITUDE of an amount, formatted the way a person reads money: grouped thousands, always
 * exactly two decimal places.
 *
 * Takes the ABSOLUTE value on purpose. Every caller already decides for itself how a negative
 * looks, and returning a sign here would silently add a second one to the six call sites that
 * write their own.
 *
 * Non-finite input yields "0.00" rather than "NaN". A NaN on a money screen is worse than a zero:
 * zero is a number a person can disbelieve, NaN is a bug report from the till.
 */
export function centsAmount(cents: number): string {
  const n = Number.isFinite(cents) ? Math.abs(cents) : 0;
  return (n / 100).toLocaleString('en-CA', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  });
}

/**
 * The customer facing money word: `$1,234.00`, and `-$1,234.00` for a negative. No currency
 * letters: the currency is the dealer's own and stays in the data and the API, never in the
 * display. Every screen, receipt and message that shows an amount calls this one function.
 */
export function formatDollars(cents: number): string {
  const sign = Number.isFinite(cents) && cents < 0 ? '-' : '';
  return `${sign}$${centsAmount(cents)}`;
}

/**
 * The PLAIN shape some surfaces need: integer cents in, `12.34` out, ungrouped,
 * the value the counter types back into an amount box or reads beside a middot.
 * HH note: this is the ported surface's named display helper for that shape,
 * consolidated here so the arithmetic spelling lives in one reviewed file
 * (CLOSURE.md names it); the relief dialog and the split panel's fill render
 * through it instead of dividing inline.
 */
export function plainDollars(cents: number): string {
  return (cents / 100).toFixed(2);
}
