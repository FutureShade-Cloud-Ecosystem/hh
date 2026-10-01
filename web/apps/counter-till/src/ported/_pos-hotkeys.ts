// Ported from hardscapeos_dibbits master 87d7941b app/src/_pos-hotkeys.ts; adapted for HH.

// _pos-hotkeys.ts: the counter's F-key roster and the legend's availability rules. Pure.
//
// THE ASK (Ryan Rood): BisTrack muscle memory: "a function key advances the step, an
// F12-style key takes payment": plus a PERSISTENT LEGEND pinned at the bottom so nobody has to
// memorise anything, showing the CURRENT step's keys and dimming what is not available yet.
//
// WHAT LIVES HERE AND WHAT DOES NOT. The BINDINGS are the dealer's (server-side curation, see
// backend/internal/pos/hotkeys.go): this module never invents which key does what. What it owns is
// the part that is genuinely the frontend's: what each action is CALLED on the legend, how a
// keyboard event is spelled so it can be matched against a stored binding, whether an action is
// available in the sale's current state, and the escape rule that keeps a dealer's choice of a
// browser-owned key from being a hijack.
//
// THE ESCAPE RULE, STATED ONCE. A dealer may bind a key the browser owns: the client asked for
// F12 by name, and refusing it would be us overruling his ergonomics. What we owe in exchange is
// that the browser's key is always reachable: a binding matches ONLY its exact chord, so any
// modifier the binding did not ask for falls straight through to the host. Bind F12 to payment and
// Shift+F12 still opens dev tools; bind F3 to search and Ctrl+F3 is still Find. Plus, every action
// on the legend has a visible control: a key is a shortcut, never the only way.

import { asRows } from './_response-shape.js';
import type { FlowStepId } from './_pos-flow.js';
import { FLOW_ORDER } from './_pos-flow.js';

/** One bound key, as the server sends it. */
export interface PosHotkey {
  action: string;
  key: string;
  browser_owned?: boolean;
}

/** The action ids. STORED: add, never rename (a renamed action kills every dealer's binding). */
export const HK_STEP_NEXT = 'step-next';
export const HK_STEP_BACK = 'step-back';
export const HK_PAY = 'pay';
export const HK_PARK = 'park';
export const HK_SEARCH = 'search';

/**
 * The legend's words. One or two, per the chrome budget: a legend is chrome, so it gets keycaps
 * and labels, never sentences. An action with no entry here is one this build cannot render and is
 * dropped rather than shown as a keycap that does nothing.
 */
export const HOTKEY_LABELS: Record<string, string> = {
  [HK_STEP_NEXT]: 'Next',
  [HK_STEP_BACK]: 'Back',
  [HK_PAY]: 'Pay',
  [HK_PARK]: 'Park',
  [HK_SEARCH]: 'Search',
};

/**
 * The roster this build falls back to when the read has not answered yet or could not. It mirrors
 * the server's default so the legend is never blank on a cold mount: and it is a FALLBACK, not the
 * source of truth: the moment the server answers, the dealer's roster replaces it wholesale.
 *
 * THESE ARE BISTRACK'S KEYS, and that is a ruling, not a preference. Ryan Rood, on the call: "for
 * hot keys bistrack does [F]4 to move to the next step and then [F]12 to take a payment. So, it'd
 * be nice if we either hardcoded something like that where, you can hit F4 to move to the next
 * step automatically." Every BisTrack refugee arrives with that muscle memory in their hands, so
 * F4 ADVANCES and F12 PAYS, and the other three are picked AROUND them:
 *
 * step-back shift-f4 the pair of the advance key: one chord, one direction, learned once.
 * park f9 a free bare F-key the browser does not claim.
 * search f2 likewise, and the closest bare key to the scan box's own reflex.
 *
 * F8 IS DELIBERATELY LEFT UNBOUND. It used to ring the sale, and rebinding it to something else
 * would turn a trained reflex into the wrong action; an unbound key teaches nothing, a rebound one
 * teaches a lie. (See BISTRACK_ROSTER: the same list, which the admin editor resets to.)
 */
export const FALLBACK_ROSTER: PosHotkey[] = [
  { action: HK_STEP_NEXT, key: 'f4' },
  { action: HK_STEP_BACK, key: 'shift-f4' },
  { action: HK_PAY, key: 'f12' },
  { action: HK_PARK, key: 'f9' },
  { action: HK_SEARCH, key: 'f2' },
];

/**
 * BISTRACK_ROSTER: what "Reset to defaults" restores in the admin editor. The SAME list the
 * fallback is, named for what an administrator is choosing rather than for when the client happens
 * to use it: an admin who has re-keyed the counter and wants the BisTrack pattern back gets these.
 */
export const BISTRACK_ROSTER: readonly PosHotkey[] = FALLBACK_ROSTER;

/** The actions in the order the editor lists them and the legend renders them. */
export const HOTKEY_ORDER: readonly string[] = [HK_STEP_NEXT, HK_STEP_BACK, HK_PAY, HK_PARK, HK_SEARCH];

/** Loader-boundary coercion (asRows, DIB-471): a body of the wrong shape yields the fallback. */
export function asHotkeyRoster(body: unknown): PosHotkey[] {
  const rows = asRows<Record<string, unknown>>(body, ['keys'])
    .filter((r) => r && typeof r.action === 'string' && typeof r.key === 'string')
    .filter((r) => HOTKEY_LABELS[r.action as string] !== undefined)
    .map((r) => ({
      action: r.action as string,
      key: (r.key as string).toLowerCase(),
      browser_owned: r.browser_owned === true,
    }));
  return rows.length > 0 ? rows : FALLBACK_ROSTER;
}

/**
 * chordFromEvent: the canonical spelling of a pressed key, matching the server's grammar
 * (`shift-ctrl-alt-f8`, modifiers in that fixed order).
 *
 * TWO KINDS OF KEY PRODUCE A CHORD, and the difference is whether the key TYPES anything:
 *
 * * A FUNCTION KEY stands alone (`f4`, `shift-f4`): it inserts no character, so claiming it
 * costs the scan field nothing.
 * * A LETTER OR DIGIT needs Ctrl or Alt (`ctrl-shift-b`): Colton asked for chords like
 * "control shift b" by name. Shift alone is NOT a modifier here: Shift+B is how a person types
 * a capital B, and a counter screen whose whole premise is that everything typed lands in the
 * scan box must never claim that. (The app shell's dealer map draws the same line for the same
 * reason; this is one grammar in two spellings, not two grammars.)
 *
 * Everything else returns '' and is therefore unmatched, which is how ordinary typing keeps
 * reaching the scan field.
 */
export function chordFromEvent(e: { key: string; shiftKey?: boolean; ctrlKey?: boolean; altKey?: boolean; metaKey?: boolean }): string {
 // Meta is never bindable (the server's grammar has no 'meta'), so a Meta press cannot match any
 // binding: which is the escape rule doing its job rather than a special case.
  if (e.metaKey) return '';
  const fn = isFnKey(e.key);
  const typed = isTypedKey(e.key);
  if (!fn && !typed) return '';
  if (typed && !e.ctrlKey && !e.altKey) return ''; // a bare (or shifted) letter is typing, not a chord
  const parts: string[] = [];
  if (e.shiftKey) parts.push('shift');
  if (e.ctrlKey) parts.push('ctrl');
  if (e.altKey) parts.push('alt');
  parts.push(e.key.toLowerCase());
  return parts.join('-');
}

function isFnKey(k: string): boolean {
  return /^F([1-9]|1[0-2])$/.test(k);
}

/** A single letter or digit: a key that TYPES, and therefore may only be bound with Ctrl or Alt. */
function isTypedKey(k: string): boolean {
  return /^[a-zA-Z0-9]$/.test(k);
}

// ── the editor's half of the grammar: is this a legal binding, and does it collide? ─────────────

/**
 * validChord: is this the canonical spelling of a bindable key? The SAME rule the server's
 * validHotkeyKey enforces, stated once on each side of the wire because the editor must be able to
 * refuse a chord before it is sent, and the server must refuse it again when it arrives.
 */
export function validChord(chord: string): boolean {
  const parts = (chord || '').toLowerCase().split('-');
  const base = parts[parts.length - 1];
  const mods = parts.slice(0, -1);
  const allowed = ['shift', 'ctrl', 'alt'];
  let last = -1;
  for (const m of mods) {
    const i = allowed.indexOf(m);
    if (i < 0 || i <= last) return false; // unknown, duplicated, or out of canonical order
    last = i;
  }
  if (isFnKey(base.toUpperCase()) || /^f([1-9]|1[0-2])$/.test(base)) return true;
  if (!isTypedKey(base)) return false;
  return mods.includes('ctrl') || mods.includes('alt');
}

/**
 * collisionFor: WHICH OTHER ACTION already holds this key, or null.
 *
 * The editor's whole reason for existing in this form: an administrator is shown the clash BEFORE
 * saving, named by the action it would break, rather than after: a save that silently drops one
 * of two rows claiming F4 is a counter where a key stopped working and nobody was told.
 */
export function collisionFor(
  roster: readonly PosHotkey[] | null | undefined,
  action: string,
  chord: string,
): string | null {
  const key = (chord || '').toLowerCase();
  if (!key) return null;
  const rows = Array.isArray(roster) ? roster : [];
  const hit = rows.find((r) => r && r.key === key && r.action !== action);
  return hit ? hit.action : null;
}

/**
 * resolveHotkey: which action this exact chord runs, or null.
 *
 * EXACT is the whole escape rule: `f12` matches F12 and nothing else, so Shift+F12 and Ctrl+F12 are
 * unmatched and fall through to the browser untouched. A caller that gets null must NOT call
 * preventDefault: an unbound keystroke keeps its native meaning, exactly as the shell's dealer
 * hotkeys already behave.
 */
export function resolveHotkey(chord: string, roster: readonly PosHotkey[] | null | undefined): string | null {
  if (!chord) return null;
  const rows = Array.isArray(roster) ? roster : [];
  const hit = rows.find((r) => r && r.key === chord);
  return hit && HOTKEY_LABELS[hit.action] !== undefined ? hit.action : null;
}

/** The sale facts the legend reads to decide what is live right now. */
export interface HotkeyState {
  step: FlowStepId | '';
  lineCount: number;
  rungDone: boolean;
  ringBusy: boolean;
  tenderable: boolean;
}

/**
 * available: is this action live in the sale's current state?
 *
 * The client asked for the legend to dim what is not available, and a dimmed key must mean the same
 * thing as a disabled control: pressing it does nothing, on purpose, and the reason is visible on
 * the screen. So availability is derived from the SAME facts the controls disable on, never from a
 * second opinion.
 */
export function available(actionId: string, s: HotkeyState): boolean {
  const idx = FLOW_ORDER.indexOf(s.step as FlowStepId);
  switch (actionId) {
    case HK_STEP_NEXT:
      return idx >= 0 && idx < FLOW_ORDER.length - 1;
    case HK_STEP_BACK:
      return idx > 0;
    case HK_PAY:
 // Exactly the Complete-sale CTA's own disabled predicate: lines on the ticket, a tender, not
 // already rung, not mid-ring.
      return s.tenderable && s.lineCount > 0 && !s.rungDone && !s.ringBusy;
    case HK_PARK:
      return s.lineCount > 0 && !s.rungDone && !s.ringBusy;
    case HK_SEARCH:
      return !s.rungDone;
    default:
      return false;
  }
}

/** One rendered keycap: what it says, what it does, whether it is live, whether the browser owns it. */
export interface LegendKey {
  action: string;
  key: string;
  label: string;
  keycap: string;
  live: boolean;
  browserOwned: boolean;
}

/**
 * legendKeys: the pinned legend, in roster order. Every bound action appears at every step; the
 * unavailable ones are DIMMED rather than removed, because a legend whose contents move around is
 * a legend nobody can build muscle memory against: which was the entire point of asking for one.
 */
export function legendKeys(roster: readonly PosHotkey[] | null | undefined, s: HotkeyState): LegendKey[] {
  const rows = Array.isArray(roster) ? roster : [];
  const out: LegendKey[] = [];
  const seen = new Set<string>();
  for (const r of rows) {
    if (!r || seen.has(r.action)) continue;
    const label = HOTKEY_LABELS[r.action];
    if (label === undefined) continue; // an action this build cannot render draws no keycap
    seen.add(r.action);
    out.push({
      action: r.action,
      key: r.key,
      label,
      keycap: keycap(r.key),
      live: available(r.action, s),
      browserOwned: r.browser_owned === true,
    });
  }
  return out;
}

/** A key's printed form: `shift-f2` reads as `⇧F2`. Symbols, because a keycap has no room for words. */
export function keycap(key: string): string {
  const parts = (key || '').toLowerCase().split('-');
  const fn = parts[parts.length - 1];
  let out = '';
  if (parts.includes('ctrl')) out += '⌃';
  if (parts.includes('alt')) out += '⌥';
  if (parts.includes('shift')) out += '⇧';
  return out + fn.toUpperCase();
}

/** The step an advance/back lands on, or null at either end. Pure so the walk is testable. */
export function stepAfter(step: FlowStepId | '', dir: 1 | -1): FlowStepId | null {
  const i = FLOW_ORDER.indexOf(step as FlowStepId);
  if (i < 0) return dir === 1 ? FLOW_ORDER[0] : null;
  const j = i + dir;
  return j >= 0 && j < FLOW_ORDER.length ? FLOW_ORDER[j] : null;
}
