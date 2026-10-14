// Ported from hardscapeos_dibbits master 87d7941b app/src/quick-action-intent.ts; adapted for HH.

// quick-action-intent.ts: DIB-434. The one-shot channel that carries "do THIS
// on arrival" from a quick action into whichever surface owns it.
//
// The same shape as the original's record-focus channel, for the same reason:
// the destination may not exist yet. Clicking "+ Sale" from Manager Home moves
// the caller to the till, and the till's terminal may be constructed after the
// navigation lands. This keeps the knowledge of "what a quick action asks for"
// in the destination, so a second quick action is one takeIntent() call in one
// component.
//
// Both arrival orders are covered:
//  * destination already mounted  → it hears INTENT_EVENT
//  * destination mounts afterwards → it calls takeIntent() when it connects
// Whichever fires first CONSUMES the intent, so one click never acts twice.
// That matters more here than anywhere else: acting twice on "start a fresh
// ticket" would mean a second reset of a till a cashier had already started
// typing into. The till's own side of this channel (its guard and its
// start-guard dialog) lives in the counter-till app, which owns that decision.

/** Fired on `window` when an intent is published. `detail` is the PendingIntent. */
export const INTENT_EVENT = "hos-quick-action-intent";

export interface PendingIntent {
  /** The surface tab the intent is addressed to, e.g. "pos". */
  module: string;
  /** What to do, e.g. "pos.new_ticket". */
  intent: string;
  /** The action that published it, for diagnosis and for a destination that handles several. */
  actionId: string;
}

// An intent nobody claims must not sit here waiting to ambush a later,
// unrelated visit to the same surface: a cashier who wanders into the till ten
// minutes later must not have their ticket reset by a click they had forgotten
// making. Ten seconds is far longer than a surface takes to mount and far
// shorter than a human navigation cycle.
const INTENT_TTL_MS = 10_000;

let pending: (PendingIntent & { at: number }) | null = null;

/** publishIntent records the intent and announces it. Called by the quick-action bar on click. */
export function publishIntent(intent: PendingIntent): void {
  pending = { ...intent, at: Date.now() };
  window.dispatchEvent(new CustomEvent<PendingIntent>(INTENT_EVENT, { detail: { ...intent } }));
}

/**
 * takeIntent returns and CONSUMES the pending intent if it is addressed to
 * `module` and has not expired; otherwise null. Consuming is what makes the
 * mounted-versus-mounting race harmless: whichever of the mount read and the
 * event listener runs first wins, and the other gets null.
 *
 * `intents` narrows to the instructions this caller can actually carry out. An
 * intent the surface does not handle is LEFT IN PLACE rather than swallowed,
 * so a sibling that does handle it still can.
 */
export function takeIntent(module: string, intents?: readonly string[]): PendingIntent | null {
  if (!pending) return null;
  if (pending.module !== module) return null;
  if (Date.now() - pending.at > INTENT_TTL_MS) {
    pending = null;
    return null;
  }
  if (intents && !intents.includes(pending.intent)) return null;
  const taken = pending;
  pending = null;
  return { module: taken.module, intent: taken.intent, actionId: taken.actionId };
}

/** Test seam: drop any pending intent. Not used by application code. */
export function clearPendingIntent(): void {
  pending = null;
}

/** Test seam: peek without consuming. Not used by application code. */
export function peekPendingIntent(): PendingIntent | null {
  return pending ? { module: pending.module, intent: pending.intent, actionId: pending.actionId } : null;
}
