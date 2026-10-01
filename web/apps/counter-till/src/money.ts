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
}
