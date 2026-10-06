// Ported from hardscapeos_dibbits master 87d7941b app/src/hotkey-logic.ts; adapted for HH.

// hotkey-logic.ts: DIB-496 (ruling D-095): dealer-level keyboard shortcuts, every decision pure.
//
// NO DOM, NO LIT, NO api(): the same shape as quick-action-registry.ts and nav-access.ts, so the
// chord grammar, the action registry and the access rule are all exercised on the Node runner
// without booting a browser. app-shell.ts holds only the window listener and the two lines that
// carry out a resolved action.
//
// THE SHAPE OF THE FEATURE. The DEALER decides what a key means, once, for the whole company
// (dealer_hotkeys, 0164): "F1 always opens a cash sale" is only true when F1 is the same key at
// every desk. The map arrives on the dashboard payload (the one request the shell already makes,
// the same ride the pinned strip takes) as rows of { binding, action }. This module owns:
//
// * the ACTION REGISTRY: action name → what it does, declared the way quick actions declare
// themselves: a destination in the shell's own keys plus an optional intent, never a code path
// per action. The shell interprets the declaration; a new action is a registry line.
// * the CHORD GRAMMAR: the canonical spelling of a pressed key combination, mirroring
// internal/dashboard/hotkeys.go so the stored spelling and the pressed spelling meet.
// * the ACCESS RULE: a hotkey into an app this role cannot open resolves to NOTHING, silently.
// Same data (SCREEN_GATE), same honesty note as nav-access.ts: this is an entry-point
// convenience, not a security boundary: the real RBAC stays on the routes.
//
// WHY AN UNBOUND KEY IS LEFT ALONE. resolveHotkey returning null means the shell does NOT call
// preventDefault, so every keystroke the dealer did not claim keeps its native meaning: F5 still
// reloads, F3 still finds: and a hotkey a role may not use behaves exactly like a key that was
// never bound. No error, no toast: a keystroke is not a place to explain an authorisation model.

import { asRows } from '@hardscapehouse/pos-kit/src/ported/_response-shape.js';
import { SCREEN_GATE, canSeeGate, visibleModuleKeys, type AccessRole } from '@hardscapehouse/pos-kit/src/ported/ports.js';
import { isNavModuleKey, isNavUnitKey, type NavModuleKey, type NavUnitKey } from '@hardscapehouse/pos-kit/src/ported/ports.js';
import { INTENT_POS_NEW_TICKET } from './quick-action-registry.js';

/** One dealer-level row, as the API sends it: a canonical chord bound to a named action. */
export interface DealerHotkey {
  binding: string;
  action: string;
}

/**
 * Normalise the payload's hotkey list at the loader boundary (asRows, per DIB-471): a body that is
 * not the expected shape yields an EMPTY map: keys silently unbound, never a broken shell.
 */
export function asHotkeyRows(body: unknown): DealerHotkey[] {
  return asRows<Record<string, unknown>>(body, ['hotkeys'])
    .filter((r) => r && typeof r.binding === 'string' && typeof r.action === 'string')
    .map((r) => ({ binding: r.binding as string, action: r.action as string }));
}

/** What carrying out an action means. Interpreted by the shell; declared here as data. */
export type HotkeyActionKind =
  | 'navigate' // openUnit (+ setActiveModule): the DEFAULT ROUTING every entry point uses (457(d))
  | 'new-tab'; // spawnInstance: an EXPLICIT new instance of the unit, like the rail's open-in-new-tab

/** One registered hotkey action: the overlay's words for it, where it goes, what it asks for. */
export interface HotkeyAction {
  id: string;
 /** The keyboard-shortcuts overlay's label. Plain counter language: no internal vocabulary. */
  label: string;
  kind: HotkeyActionKind;
  unit: NavUnitKey;
  module?: NavModuleKey;
  /** Published on the quick-action intent channel BEFORE navigating, exactly as DIB-434 does. */
  intent?: string;
}

/** The instruction search.focus hands the catalogue: put the cursor in the search box. */
export const INTENT_CATALOGUE_FOCUS_SEARCH = 'catalogue.focus_search';

/**
 * The action registry: name → declaration. The names are what dealer_hotkeys rows point at, so
 * they follow the registry rule every stored id in this codebase carries: ADD, NEVER RENAME: a
 * renamed action orphans the dealer's bindings and silently kills their keys.
 *
 * ── SEED SET: PLACEHOLDERS AWAITING THE CLIENT'S OFFICIAL LIST (D-095) ────────────────────────
 * These five are the mechanism's demonstration set, matching 0164's seeded bindings. The client
 * has confirmed the SHAPE (dealer-level keys, "F1 opens a cash sale") but not the final roster;
 * expect this list and the seed rows to change together when the official list lands.
 */
export const HOTKEY_ACTIONS: Record<string, HotkeyAction> = {
  // The client's example, verbatim: jump to the till and start a fresh sale. It reuses DIB-434's
  // intent, so the POS guard for a live ticket (Continue / Park) protects a hotkey press exactly
 // as it protects the "+ Sale" click: same instruction, same destination, same guard.
  'sale.cash.new': {
    id: 'sale.cash.new',
    label: 'Start a new sale at the till',
    kind: 'navigate',
    unit: 'sales',
    module: 'pos',
    intent: INTENT_POS_NEW_TICKET,
  },
  // D-111 · 'sale.pay' USED TO BE HERE, AND IS DELIBERATELY GONE. It was UNION-TRAIN11's entry for
 // the concept's "F8 Pay". Pay belongs to the COUNTER roster and only to it: F12, by the client's
 // own BisTrack ruling (R1): and while this map ALSO knew a pay key, the till's
  // Complete-sale button could and did advertise the wrong one: R1 moved the counter to F12 and
  // left the shell's F8 in place, so the chip named a key that is deliberately unbound. Retiring
  // the action here means no second surface can ever advertise a stale pay key again. Migration
  // 0191 removes the seeded 0171 row in the same commit.
  //
 // The pos.pay INTENT CHANNEL SURVIVES UNTOUCHED: pos-terminal.ts still consumes
  // INTENT_POS_PAY on both of its intent paths. Only the shell's publisher of it is gone, because
  // a shell hotkey was the only thing that ever published it.
  //
  // F8 IS LEFT UNBOUND, NOT REBOUND, at every shipped default on both maps. An unbound key teaches
  // nothing; a rebound one teaches a trained hand the wrong action.
  //
 // A second copy of the Sales app: DIB-457's explicit duplication, from the keyboard.
  'sales.tab.new': {
    id: 'sales.tab.new',
    label: 'Open a new Sales tab',
    kind: 'new-tab',
    unit: 'sales',
  },
  // The fastest write-up surface shipped today. Flagged in NOTES: the destination is a placeholder
  // until the client's list says what "quick order" opens.
  'order.quick.open': {
    id: 'order.quick.open',
    label: 'Open the order write-up screen',
    kind: 'navigate',
    unit: 'sales',
    module: 'quotes',
  },
  // Product lookup with the cursor already in the search box (the catalogue consumes the intent).
  'search.focus': {
    id: 'search.focus',
    label: 'Search the product list',
    kind: 'navigate',
    unit: 'sales',
    module: 'catalogue',
    intent: INTENT_CATALOGUE_FOCUS_SEARCH,
  },
  // The assistant panel.
  'assist.open': {
    id: 'assist.open',
    label: 'Open the assistant',
    kind: 'navigate',
    unit: 'admin',
    module: 'assist',
  },
};

/**
 * The canonical spelling of a pressed chord, or null when the keystroke is not one this system
 * binds. MIRRORS internal/dashboard/hotkeys.go: modifiers Ctrl, Alt, Shift in that order, then
 * F1-F12, a letter, or a digit; letters and digits need Ctrl or Alt (Shift alone is just typing a
 * capital letter, and a bare letter is a keystroke, not a chord); F-keys stand alone.
 *
 * Meta/Cmd returns null outright: the OS and the browser own that key, and claiming a chord there
 * would fire for keystrokes aimed at the machine: the refusal isBackChord already records.
 */
export function bindingFromEvent(e: {
  key: string;
  ctrlKey: boolean;
  altKey: boolean;
  shiftKey: boolean;
  metaKey: boolean;
}): string | null {
  if (e.metaKey) return null;
  const key = canonicalKey(e.key);
  if (!key) return null;
  if (!key.startsWith('F') && !e.ctrlKey && !e.altKey) return null;
  const mods = [e.ctrlKey ? 'Ctrl' : '', e.altKey ? 'Alt' : '', e.shiftKey ? 'Shift' : ''].filter(Boolean);
  return [...mods, key].join('+');
}

/** F1-F12 pass through; a single letter or digit is uppercased; everything else is not a key. */
function canonicalKey(key: string): string | null {
  if (/^F([1-9]|1[0-2])$/.test(key)) return key;
  if (/^[a-zA-Z0-9]$/.test(key)) return key.toUpperCase();
  return null;
}

/**
 * DIB-512 · is this key one that TYPES nothing? An F-key inserts no character into an input, so
 * letting it reach the dealer hotkey map from inside a text field costs the field nothing: and
 * the POS scan box (which re-grabs focus after every add: its whole design) is exactly where the
 * bound pay key gets pressed. Before this, the shell's typing guard swallowed F8 from the
 * terminal's own resting state: the CTA advertised a key its focus discipline made unreachable.
 * Letters/digits (which DO type) and every unbound key keep their native meaning as before.
 */
export function isFunctionKey(key: string): boolean {
  return /^F([1-9]|1[0-2])$/.test(key);
}

/** Does this keystroke ask for the keyboard-shortcuts overlay? '?': Shift+/ on most layouts. */
export function isHelpChord(e: {
  key: string;
  ctrlKey: boolean;
  altKey: boolean;
  metaKey: boolean;
}): boolean {
  return e.key === '?' && !e.ctrlKey && !e.altKey && !e.metaKey;
}

/**
 * May this role carry out this action? The SAME data every other entry point gates on: a module
 * destination answers with its SCREEN_GATE, a unit destination (a new tab) with whether the unit
 * has any visible module left. An entry-point convenience, not a security boundary (nav-access.ts
 * says why): and the shell's openUnit/spawnInstance paths check again on their own account.
 */
export function canRunAction(action: HotkeyAction, role: AccessRole): boolean {
  if (!isNavUnitKey(action.unit)) return false;
  if (action.module) {
    if (!isNavModuleKey(action.module)) return false;
    return canSeeGate(SCREEN_GATE[action.module], role);
  }
  return visibleModuleKeys(action.unit, role).length > 0;
}

/**
 * The whole decision, one call: a pressed chord against the dealer's map, the registry, and the
 * caller's access. Null: silently: for an unbound chord, a binding to an action this build does
 * not know (a row ahead of its release, degrading exactly like a stale pin key), and an action
 * this role may not run. The caller must preventDefault ONLY on a non-null answer, so every
 * unclaimed keystroke keeps its native meaning.
 */
export function resolveHotkey(
  binding: string | null,
  map: readonly DealerHotkey[],
  role: AccessRole,
): HotkeyAction | null {
  if (!binding) return null;
  const row = map.find((r) => r.binding === binding);
  if (!row) return null;
  const action = HOTKEY_ACTIONS[row.action];
  if (!action) return null;
  return canRunAction(action, role) ? action : null;
}

/**
 * UNION-TRAIN11 · which key (if any) the dealer bound to one action, for THIS caller: the data
 * behind an on-surface keycap chip (the concept's "F8 Pay" on the complete-sale CTA). Null means
 * "wear no chip": unbound, bound to an action this build does not know, or an action this role
 * cannot run: the same silences resolveHotkey keeps, because a chip advertising a dead key
 * teaches people the keyboard is broken (hotkeyHelpRows' own rule).
 */
export function bindingForAction(
  map: readonly DealerHotkey[],
  actionId: string,
  role: AccessRole,
): string | null {
  const action = HOTKEY_ACTIONS[actionId];
  if (!action || !canRunAction(action, role)) return null;
  const row = map.find((r) => r.action === actionId);
  return row ? row.binding : null;
}

/** One overlay line: the key, spelled for a person, and what it does. */
export interface HotkeyHelpRow {
  binding: string;
  label: string;
}

/**
 * The shortcut overlay's rows: the dealer's bindings THIS caller can actually use, in map order.
 * A binding the role cannot follow is omitted rather than greyed out: advertising a key that
 * does nothing teaches people the keyboard is broken.
 */
export function hotkeyHelpRows(map: readonly DealerHotkey[], role: AccessRole): HotkeyHelpRow[] {
  const out: HotkeyHelpRow[] = [];
  for (const row of map) {
    const action = HOTKEY_ACTIONS[row.action];
    if (!action || !canRunAction(action, role)) continue;
    out.push({ binding: row.binding, label: action.label });
  }
  return out;
}
