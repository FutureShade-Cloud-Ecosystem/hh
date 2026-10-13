// Plain logic the purchasing areas share.
import type { Result } from "@hardscapehouse/api-client";

/**
 * Whether the answer to a write is unknown: it never arrived, or a gateway or the core answered a
 * server fault (the write may have been made before the fault). A definite refusal (a 4xx) and a
 * definite success are not lost. The consequential writes (receive, apply, send, void, post) read
 * the document again instead of trusting a lost answer, and never say "nothing was changed".
 */
export function answerLost(result: Result<unknown>): boolean {
  if (result.ok) return false;
  return result.status === undefined || result.status >= 500;
}
