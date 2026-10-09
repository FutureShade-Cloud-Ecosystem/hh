// Yard Pick's framework free logic: what the yard team's pick is allowed to
// do and how it reads. No UI, no network, no storage: plain values in, plain
// values out, so the screens only draw it and a test can hold it without a
// browser.
//
// PROVENANCE (each port records its source: repository, commit, path).
// The original HardscapeOS app (repository hardscapeos_dibbits), commit
// f8f4973db317b92296ae8ec7e62a3f54f5dc82ab, read only:
//   - qtyText: app/src/yard-logic.ts
// HH's own, written against the core and not ported:
//   - yardError mirrors the core's error envelope (httpx.CounterMessage
//     strips the module prefix) and the tasks taxonomy's statuses
// The words use no dashes: where the original wrote one, a comma, a colon or
// a middle dot stands.

/** The core's refusals arrive as "module: sentence" already stripped to the sentence by the envelope; the yard reads the sentence. */
export function plainSentence(msg: string | null | undefined): string {
  const s = (msg ?? "").trim();
  const m = /^([a-z][a-z0-9_]*): (.+)$/s.exec(s);
  return m ? (m[2] ?? "").trim() : s;
}

/**
 * What the yard reads when a call fails. A refusal in the core's own words is
 * shown as it came (a capital goes on, a full stop closes it); a server fault
 * or no answer is never shown as it came. `fallback` is that sentence for the
 * call that failed.
 */
export function yardError(
  error: string,
  status: number | undefined,
  fallback: string,
  words: { notFound?: string } = {},
): string {
  if (status === undefined || status >= 500) return fallback;
  if (status === 401) return "Your sign in has ended. Sign out and sign in again.";
  if (status === 403) return "You are not allowed to do that on this yard.";
  if (status === 404) return words.notFound ?? "That is no longer on the board.";
  const plain = plainSentence(error);
  if (plain === "") return fallback;
  const cased = plain.charAt(0).toUpperCase() + plain.slice(1);
  return /[.!?]$/.test(cased) ? cased : `${cased}.`;
}
