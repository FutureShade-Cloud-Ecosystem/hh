// Ported from hardscapeos_dibbits master 87d7941b app/src/field-refusal.ts; adapted for HH.

// field-refusal.ts: put the server's 422 sentence NEXT TO the control it is about.
//
// WHY THIS EXISTS. Every settings editor in this repo surfaces a refusal the same way: one
// panel-level banner at the top of the page. That is fine for "you are not an administrator" and
// wrong for "commit_prompt_hours must be between 0 and 336", where the reader has to work out which
// of eight boxes the sentence belongs to and then scroll back to it. A refusal that names a field
// should be rendered at that field.
//
// THE ENVELOPE IS ONE FLAT STRING and carries no field identity: `{"error": "commit_prompt_hours
// must be between 0 and 336"}`. It does not have to: every validator in this codebase writes the
// knob's own name as the FIRST WORD of the sentence, because that is how a sentence about a knob
// reads out loud. So the field identity is recoverable without changing a single server message, and
// this file recovers it.
//
// WHAT IT DELIBERATELY DOES NOT DO. It does not translate, shorten or rewrite the sentence. The
// server's words are the message: a client-side paraphrase that disagreed with the server would be
// worse than no message at all, which is the rule the whole settings kit is built on.

/** A refusal, split into the control it belongs to and the words to show. */
export interface FieldRefusal {
  /** The knob the sentence is about, or '' when it names none. */
  field: string;
  /** The sentence, verbatim, with the internal module prefix removed. */
  sentence: string;
}

/**
 * The module prefix, defensively.
 *
 * The marshal layer already strips this (`httpx.Error`), so a current server never sends one. It is
 * stripped here as well because this function's whole job is to find the FIRST WORD, and a stale
 * server: or a message that reached the client by some other path: would otherwise offer
 * "dispatch" as the field name.
 */
const MODULE_PREFIX = /^[a-z][a-z0-9]*: /;

/**
 * FIELD_ALIASES maps a validator's word onto the column the screen actually renders.
 *
 * There is exactly one entry, and it is a real mismatch rather than a hypothetical: the settings
 * kit's shared visibility validator says `cost_visibility` because it is shared, while the dispatch
 * policy's column: and the control on the screen: is `load_value_visibility`. Without the alias the
 * refusal would float to the top of the page as an unattached banner, which is the behaviour this
 * file exists to replace.
 *
 * It is a map rather than a rename because the validator is shared by modules whose column IS called
 * cost_visibility; renaming it would move the mismatch rather than remove it.
 */
const FIELD_ALIASES: Record<string, string> = {
  cost_visibility: 'load_value_visibility',
};

/**
 * fieldRefusal reads which control a server refusal is about.
 *
 * The knob is the first token, and it must LOOK like a column name: lower_snake_case with at least
 * one underscore. That shape test is what stops an ordinary sentence ("Nothing was saved…") from
 * being mistaken for a field name and anchoring a message under an unrelated input.
 */
export function fieldRefusal(message: string): FieldRefusal {
  const sentence = (message ?? '').replace(MODULE_PREFIX, '').trim();
  const first = sentence.split(/[\s:(]/, 1)[0] ?? '';
  const looksLikeAKnob = /^[a-z][a-z0-9]*(_[a-z0-9]+)+$/.test(first);
  if (!looksLikeAKnob) return { field: '', sentence };
  return { field: FIELD_ALIASES[first] ?? first, sentence };
}

/**
 * refusalFor picks the sentence belonging to one control, for rendering beside it.
 *
 * A screen holds ONE refusal at a time: the server answers one request with one message: so this
 * is a lookup rather than a collection. It returns '' for every other control, which is what lets a
 * template ask each field the question without knowing which one lost.
 */
export function refusalFor(refusal: FieldRefusal | null, field: string): string {
  if (!refusal || !refusal.field) return '';
  return refusal.field === field ? refusal.sentence : '';
}

/**
 * formRefusal is what stays at the top: a refusal that names no field, or names one this screen does
 * not render.
 *
 * THE SECOND CASE IS THE LOAD-BEARING ONE. If a validator gains a knob the screen has not caught up
 * with, anchoring by field alone would silently swallow the message: the save would fail and the
 * screen would say nothing. A sentence with nowhere to go goes to the top.
 */
export function formRefusal(refusal: FieldRefusal | null, renderedFields: readonly string[]): string {
  if (!refusal) return '';
  if (!refusal.field) return refusal.sentence;
  return renderedFields.includes(refusal.field) ? '' : refusal.sentence;
}

/**
 * whenWord renders a recorded instant the way somebody reads a history out loud.
 *
 * IT PARSES A VALUE AND READS NO CLOCK. The instant comes from the server on the row being rendered;
 * `new Date(value)` is a parse, not a clock read, which is the distinction the injected-clock rule
 * turns on. Nothing here asks what time it is now.
 *
 * An unparseable stamp comes back as the empty string rather than "Invalid Date": a history row that
 * says "Invalid Date" looks broken in a way that sends somebody to check a bug, and the row's real
 * content (what changed, from what, by whom) is still worth showing without it.
 */
export function whenWord(iso: string | undefined): string {
  const t = Date.parse(iso ?? '');
  if (Number.isNaN(t)) return '';
  return new Date(t).toLocaleString('en-CA', {
    year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit',
  });
}
