// Ported from hardscapeos_dibbits master 87d7941b app/src/quick-action-guard.ts; adapted for HH.

// quick-action-guard.ts: DIB-434. THE IN-PROGRESS GUARD, as a pure decision.
//
// THE HARD REQUIREMENT, IN ONE SENTENCE: a quick "+ Sale" from anywhere in the OS must NEVER
// silently discard a ticket that is already live on the till.
//
// That is easy to say and easy to lose. The obvious implementation of "start a fresh ticket" is to
// call the terminal's existing reset: which is exactly what the shipped "New sale" button does,
// behind its own confirm: and a quick action that reached for that reset directly would wipe a
// half-built order belonging to the person standing at the counter, triggered by a button somebody
// clicked from a screen where the till was not even visible. So the decision is separated from the
// terminal and written here, where it is a function of two numbers and can be mutated in a test.
//
// WHY THREE OUTCOMES AND NOT TWO
// ------------------------------
// "Lines on the ticket" and "work on the ticket" are not the same set:
//
// * lines > 0 → the ticket can be PARKED. The rail (Q-042 / POS Counter V2) is
// shipped; parking is a real server ticket that can be resumed and
// re-priced. Offer Continue or Park it: no third option, because
// there is no reason to destroy something that can be kept.
// * no lines, but a customer → there is nothing the parked rail can hold (a parked ticket with
// / notes / a requested date no lines is not a ticket), but there IS work to lose. So the
// answer is not "park" and it is certainly not "silently reset":
// it is an explicit confirm that names what goes.
// * nothing at all → start fresh, immediately. Prompting on an empty terminal is the
// nag that makes people stop reading prompts.
//
// The third case is the one that keeps the guard honest under its own edge: without it the "park"
// path would no-op on a line-less ticket (parkTicket needs lines) and the terminal would sit there
// having done nothing, which reads as a broken button.

/** What a quick "start a fresh ticket" must do about whatever is already on the till. */
export type QuickStartMode =
 /** Nothing to lose: reset now, no prompt. */
  | 'fresh'
 /** Lines are on the ticket: offer Continue or Park it. NEVER a discard. */
  | 'park-or-continue'
 /** No lines to park, but real work to lose: offer Continue or an explicit, named clear. */
  | 'confirm-or-continue';

/** The live terminal state the decision reads. Deliberately primitives: no Lit, no cart type. */
export interface LiveTicket {
 /** Number of priced lines on the ticket. */
  lineCount: number;
 /** True when a customer account is selected (not walk-in). */
  hasCustomer: boolean;
 /** True when notes, a requested date, or anything else typed is present. */
  hasOtherWork: boolean;
}

/**
 * quickStartMode is the whole guard.
 *
 * It is INTENTIONALLY total over its inputs and has no side effects: the terminal calls it, then
 * renders one of three things. Every "never clobber" claim this ticket makes is a claim about this
 * function's output for a non-empty ticket, which is why it is tested by mutation
 * (quick-action-guard.test.mjs) rather than by reading the terminal's template.
 */
export function quickStartMode(t: LiveTicket): QuickStartMode {
  if (t.lineCount > 0) return 'park-or-continue';
  if (t.hasCustomer || t.hasOtherWork) return 'confirm-or-continue';
  return 'fresh';
}

/** True when the quick action must stop and ask. The negation of "may reset immediately". */
export function quickStartNeedsGuard(t: LiveTicket): boolean {
  return quickStartMode(t) !== 'fresh';
}

/** True when the ticket can actually be handed to the parked rail (it needs lines). */
export function canPark(t: LiveTicket): boolean {
  return t.lineCount > 0;
}

/**
 * The sentence the guard shows. Written here, next to the decision, so the words and the behaviour
 * are changed in one place: a dialog that offers "Park it" for a ticket that cannot be parked is
 * the failure this pairing exists to prevent.
 */
export function guardMessage(t: LiveTicket): string {
  switch (quickStartMode(t)) {
    case 'park-or-continue': {
      const n = t.lineCount;
      return `This till already has a ticket open: ${n} line${n === 1 ? '' : 's'}. Park it to keep it for later, or carry on with it.`;
    }
    case 'confirm-or-continue':
      return 'This ticket has no lines to park yet, but it does have work on it: starting fresh clears the selected customer and anything typed.';
    default:
      return '';
  }
}
