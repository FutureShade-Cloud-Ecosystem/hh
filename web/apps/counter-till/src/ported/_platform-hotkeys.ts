// Ported from hardscapeos_dibbits master 87d7941b app/src/_platform-hotkeys.ts; adapted for HH.

// _platform-hotkeys.ts: D-111: the PLATFORM key roster's pure half.
//
// No DOM, no Lit, no api(): the same shape as hotkey-logic.ts and _pos-hotkeys.ts, so the payload
// normaliser, the per-surface action lists, the collision rule and the copy are all exercised on
// the Node runner without booting a browser. platform-hotkeys.ts holds the element and the fetches.
//
// THE RULING, verbatim: "i think we need a platform admin configurator for hotkeys". What was
// missing was never an editor: train 14 shipped the dealer's: but the LEVEL ABOVE IT. Under a
// dealer's override there was only a constant in source, so the only way for Hardscape House to
// change what a key does across the deployment was to cut a release.
//
// ── THE ONE THING THIS FILE EXISTS TO GET RIGHT ────────────────────────────────────────────────
//
// There are TWO hotkey substrates in this product and this screen shows both. They must not merge,
// and this module is where that discipline is kept, because a screen that renders two lists with
// one set of helpers is one refactor away from being a screen that renders one list:
//
// app.shell the app-wide NAVIGATION map. Chord spelling `F1`, `Ctrl+Shift+K`: capitalised,
// '+'-joined (hotkey-logic.ts's bindingFromEvent). Actions are the nav registry's
// ('sale.cash.new'), and their labels are already sentences.
// pos.counter the till's in-sale ACTION roster. Chord spelling `f4`, `shift-f4`: lowercase,
// '-'-joined (_pos-hotkeys.ts's chordFromEvent). Actions are the counter's five
// ('step-next'), and their legend labels are single words.
//
// Two grammars, two namespaces, two label sets. EVERY function below takes a surface, and none of
// them has a default. The last conflation of these two cost a real defect: the till's
// Complete-sale button advertised F8 while the counter actually paid on F12: and the fix was to
// give each surface exactly one source of truth. This screen must not undo that by being
// convenient.
//
// ── WHY THE COUNTER GETS A SECOND SET OF LABELS HERE ───────────────────────────────────────────
//
// _pos-hotkeys.ts's HOTKEY_LABELS are legend CHROME: "Next", "Back", "Pay". One word is exactly
// right on a strip pinned across the foot of a till, where the keycap beside it carries the rest of
// the meaning and the person reading it is mid-sale. In a configurator they are wrong: an
// administrator picking a key for "Back" has no way to know whether that is a step, a screen, or a
// browser gesture. So this file carries a sentence for each, the way the shell's registry already
// does. Both label sets stay: they are for two different readers, at two different moments.
//
// The single-word labels remain the LEGEND's, and the counter's own collision refusal keeps using
// them, because that refusal is read by the same administrator looking at the same strip.

import { HOTKEY_ACTIONS, bindingFromEvent, type HotkeyHelpRow } from './hotkey-logic.js';
import {
  HOTKEY_ORDER,
  chordFromEvent,
  keycap,
  legendKeys,
  validChord,
  type HotkeyState,
  type PosHotkey,
} from './_pos-hotkeys.js';

/** The app-wide navigation map's surface id: matches settings.PlatformSurfaceShell. */
export const PLATFORM_SHELL_SURFACE = 'app.shell';
/** The counter roster's surface id: matches settings.PlatformSurfaceCounter and 0172's own id. */
export const PLATFORM_COUNTER_SURFACE = 'pos.counter';

/** The two surfaces, in the order the screen renders them. */
export const PLATFORM_SURFACES = [PLATFORM_SHELL_SURFACE, PLATFORM_COUNTER_SURFACE] as const;

/** One platform binding as the API sends it. */
export interface PlatformKey {
  action: string;
  binding: string;
  position: number;
}

/** One surface's whole platform roster, and the tier immediately above it. */
export interface PlatformRosterView {
  surface: string;
  keys: PlatformKey[];
  /**
   * The DEALER OVERRIDE in force above this roster, on this surface. Item 4c: a platform binding
   * that lands on a key the dealer already gave to a different action is a key with two meanings
   * across one deployment, and until this arrived nothing on either screen could say so. Empty for
 * app.shell, always: the server's platform_hotkeys_effective.go argues why.
   */
  dealer: PlatformKey[];
}

/** One keystroke the app shell owns outright: no tier binds it, no tier can free it. */
export interface ReservedKey {
  binding: string;
 /** Plain language, from the server: "opens the shortcut list". Never an action id. */
  holds: string;
}

/** The GET /api/v1/platform/hotkeys answer, normalised. */
export interface PlatformHotkeyView {
  rosters: PlatformRosterView[];
  /** The SERVER's answer to "may this caller change the roster". Never computed here. */
  canEdit: boolean;
  /**
   * The shell's own hardcoded keys, SERVED rather than mirrored here. The chord grammars are
 * mirrored on both sides on purpose: each side must be able to refuse before the round trip:
   * but this list is a fact about the shell with only one owner, and a hand-copied array in a
   * settings screen would be the copy that drifts the day a third hardcoded key is added.
   */
  reserved: ReservedKey[];
}

/**
 * The screen's name for each roster. Two or three words: this is a section heading, not a lesson.
 * Deliberately says what the keys DO rather than which table they live in: 'dealer_hotkeys' and
 * 'surface_role_items' are engineering vocabulary and belong nowhere near a screen.
 */
export const PLATFORM_SURFACE_LABEL: Record<string, string> = {
  [PLATFORM_SHELL_SURFACE]: 'Everywhere in the app',
  [PLATFORM_COUNTER_SURFACE]: 'At the counter',
};

/**
 * One line under each heading saying who obeys these keys. It is the ONE explanatory string each
 * panel is allowed, and it earns its place: the two rosters look identical and are not, and an
 * administrator who rebinds the wrong one finds out from a counter person, later than anyone wanted.
 */
export const PLATFORM_SURFACE_SCOPE: Record<string, string> = {
  [PLATFORM_SHELL_SURFACE]: 'Every screen obeys these.',
  [PLATFORM_COUNTER_SURFACE]: 'Only the till obeys these, and they win while a sale is open.',
};

/**
 * Sentence labels for the counter's five actions: see the file header for why these exist beside
 * _pos-hotkeys.ts's one-word legend labels.
 *
 * This is also the LABEL MAP the counter-vocabulary rule requires: the screen renders through this,
 * never `${key.action}`, so a stored id like 'step-next' cannot reach a person as text.
 */
export const COUNTER_ACTION_LABEL: Record<string, string> = {
  'step-next': 'Move the sale on to the next step',
  'step-back': 'Go back one step in the sale',
  pay: 'Take payment: complete the sale',
  park: 'Park the ticket and start a new one',
  search: 'Put the cursor back in the scan box',
};

/**
 * Every action one surface can bind, in the order the screen lists them.
 *
 * The order is each registry's own, not alphabetical: the shell's is the order the `?` overlay uses,
 * and the counter's is HOTKEY_ORDER, which is the order the legend strip draws. A configurator that
 * reordered them would make an administrator translate between this screen and the one they are
 * configuring.
 */
export function platformActions(surface: string): { id: string; label: string }[] {
  if (surface === PLATFORM_COUNTER_SURFACE) {
    return HOTKEY_ORDER.map((id) => ({ id, label: COUNTER_ACTION_LABEL[id] ?? id }));
  }
  return Object.values(HOTKEY_ACTIONS).map((a) => ({ id: a.id, label: a.label }));
}

/**
 * Normalise the payload at the loader boundary (the DIB-471 posture): a body this build cannot read
 * yields EMPTY rosters and canEdit false: a configurator that shows nothing, never a broken screen
 * and never an accidental edit affordance. canEdit defaults FALSE for the same reason every
 * capability flag does: the safe answer to "may I" when nobody said is no.
 */
export function asPlatformView(body: unknown): PlatformHotkeyView {
  const b = (body ?? {}) as Record<string, unknown>;
  const raw = Array.isArray(b.rosters) ? (b.rosters as unknown[]) : [];
  const rosters: PlatformRosterView[] = [];
  for (const surface of PLATFORM_SURFACES) {
    const hit = raw.find((r) => r && typeof r === 'object' && (r as Record<string, unknown>).surface === surface) as
      | Record<string, unknown>
      | undefined;
    rosters.push({
      surface,
      keys: asKeyRows(hit?.keys),
      // A payload from a build older than item 4c has no `dealer` at all, and the honest reading of
 // that is "this server cannot tell me about the tier above": which normalises to EMPTY and
      // costs the screen only the cross-tier refusal, never the screen.
      dealer: asKeyRows(hit?.dealer),
    });
  }
  return {
    rosters,
    canEdit: b.can_edit === true,
    reserved: (Array.isArray(b.reserved) ? (b.reserved as unknown[]) : [])
      .filter((r): r is Record<string, unknown> => !!r && typeof r === 'object')
      .filter((r) => typeof r.binding === 'string' && (r.binding as string) !== '')
      .map((r) => ({
        binding: r.binding as string,
        // A reserved key with no words is still reserved. The fallback is a phrase and not an id,
        // because it is going straight into a refusal a person reads.
        holds: typeof r.holds === 'string' && r.holds ? (r.holds as string) : 'belongs to the app itself',
      })),
  };
}

/** The shared row coercion for both tiers on one roster: the same shape, twice. */
function asKeyRows(raw: unknown): PlatformKey[] {
  return (Array.isArray(raw) ? (raw as unknown[]) : [])
    .filter((k): k is Record<string, unknown> => !!k && typeof k === 'object')
    .filter((k) => typeof k.action === 'string' && typeof k.binding === 'string')
    .map((k, i) => ({
      action: k.action as string,
      binding: k.binding as string,
      position: typeof k.position === 'number' ? k.position : i,
    }));
}

/** One surface's rows out of a normalised view, or an empty roster. */
export function rosterFor(view: PlatformHotkeyView | null, surface: string): PlatformKey[] {
  return view?.rosters.find((r) => r.surface === surface)?.keys ?? [];
}

/** The DEALER OVERRIDE in force above one surface's platform roster, or nothing. */
export function dealerFor(view: PlatformHotkeyView | null, surface: string): PlatformKey[] {
  return view?.rosters.find((r) => r.surface === surface)?.dealer ?? [];
}

/** The shell's own hardcoded keys, as the server sent them. */
export function reservedOf(view: PlatformHotkeyView | null): ReservedKey[] {
  return view?.reserved ?? [];
}

/** The OTHER substrate's id: the one this surface's keys are allowed to share with. */
export function otherSurface(surface: string): string {
  return surface === PLATFORM_COUNTER_SURFACE ? PLATFORM_SHELL_SURFACE : PLATFORM_COUNTER_SURFACE;
}

/** The roster as the editor holds it while it is being changed: action → chord. */
export function draftFrom(keys: readonly PlatformKey[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const k of keys) out[k.action] = k.binding;
  return out;
}

/** A draft back on the wire, in the screen's own listed order: position IS that order. */
export function draftToKeys(surface: string, draft: Record<string, string>): PlatformKey[] {
  return platformActions(surface)
    .filter((a) => draft[a.id])
    .map((a, i) => ({ action: a.id, binding: draft[a.id], position: i }));
}

/**
 * The chord a keypress spells, in THIS SURFACE's grammar, or '' when the keystroke is not one the
 * surface can bind.
 *
 * Both halves delegate to the grammar that already exists for their surface. Writing a third one
 * here would be the copy that drifts: dashboard/hotkeys.go says the quiet part out loud: "a row
 * the two disagree on is a key that can be stored but never pressed, so keep them identical."
 */
export function platformChordFrom(surface: string, e: KeyboardEvent): string {
  if (surface === PLATFORM_COUNTER_SURFACE) return chordFromEvent(e);
  return bindingFromEvent(e) ?? '';
}

/** Whether a chord is bindable at all on this surface: again, each surface's own rule. */
export function platformValidBinding(surface: string, binding: string): boolean {
  if (!binding) return false;
  if (surface === PLATFORM_COUNTER_SURFACE) return validChord(binding);
  return bindingFromEvent(eventFromShellBinding(binding)) === binding;
}

/**
 * The display form of a stored binding. The shell's spelling is already what a person reads
 * ("Ctrl+Shift+K"); the counter's is stored lowercase and dash-joined, so it goes through the
 * legend's own keycap() and comes back as "⇧F4".
 */
export function platformKeycap(surface: string, binding: string): string {
  if (!binding) return '';
  return surface === PLATFORM_COUNTER_SURFACE ? keycap(binding) : binding;
}

/**
 * WHICH OTHER ACTION holds this key on this roster, or null: the collision, named by the action it
 * would break.
 *
 * The same predicate the dealer's editor uses (_pos-hotkeys.ts collisionFor), restated over a draft
 * keyed by action so it can serve both surfaces. A roster where two actions claim one key resolves
 * by row order and drops the loser SILENTLY, so an administrator who found out afterwards would
 * find out from a counter person telling them a key stopped working.
 */
export function platformCollisionFor(
  draft: Record<string, string>,
  action: string,
  binding: string,
): string | null {
  if (!binding) return null;
  for (const [other, bound] of Object.entries(draft)) {
    if (other !== action && bound === binding) return other;
  }
  return null;
}

/** The human label for one action on one surface: never the stored id. */
export function platformActionLabel(surface: string, action: string): string {
  return platformActions(surface).find((a) => a.id === action)?.label ?? action;
}

/**
 * The refusal sentence for a clash, in the walkthrough-error voice: what happened, and the way
 * forward. Keycap plus a human label, never a stored id and never a ticket code.
 */
export function platformClashSentence(surface: string, binding: string, clashAction: string): string {
  return `${platformKeycap(surface, binding)} already runs “${platformActionLabel(surface, clashAction)}”. Free that one first, or pick another key.`;
}

// ── ITEM 4c · THE EFFECTIVE ROSTER, AND THE ONE DISTINCTION THE COLLISION RULE WAS MISSING ──────
//
// The collision check exists in five places and every one is scoped to ONE SURFACE AND ONE TIER:
// settings.ValidatePlatformRoster, 0191's unique index on (surface, binding), _pos-hotkeys.ts's
// collisionFor, platformCollisionFor above, and the read-side dedupes. So a key already spoken for
// one tier UP, or one the shell itself owns, was bound in silence.
//
// AND THE OBVIOUS FIX IS WRONG. "Refuse any key claimed anywhere" would refuse the roster this
// product ships with: 0191 binds F2, F4 and F9 on BOTH surfaces, deliberately: the counter listens
// in the capture phase and stopPropagations, the shell listens in bubble, so the till wins while a
// sale is open and the shell wins everywhere else. A refusal that rejected the shipped defaults
// would be a bug wearing a safety feature's clothes.
//
// So the answer has THREE values, not two, and which one you get depends on whose keyboard the two
// meanings actually meet on:
//
// 'taken' REFUSE. One surface, one physical key, two different actions: because a second tier
// on the same surface (the dealer's override) already holds it, or because the SHELL
// holds it and no tier can take it. The tiers resolve whole-or-absent, so the two never
// fire at once; that is exactly what makes it worth refusing rather than warning, since
// nobody at any one till ever sees the conflict and so nobody can report it.
// 'shared' WARN. The deliberate cross-substrate double claim. The words name WHICH other surface
// holds it and say the capture discipline resolves it, because "F4 is also bound" with
// no explanation reads as a fault and teaches an administrator to go and break it.
// 'free' No words at all. Most presses.
//
// EVERY REFUSAL NAMES WHAT HOLDS THE KEY, in that surface's own label set. The server's floor names
// the KEY instead (platform_hotkeys.go argues why: it has no human label for a stored action id and
// would otherwise paint a raw enum). Here we have the labels, so here we say which one.

/** Which of the three answers a candidate binding got, and the sentence that goes with it. */
export type KeyVerdictKind = 'free' | 'shared' | 'taken';

/** One answer. `sentence` is '' only when kind is 'free'. */
export interface KeyVerdict {
  kind: KeyVerdictKind;
  sentence: string;
}

/** Everything the verdict needs about the world outside the row being edited. */
export interface EffectiveContext {
  /** The roster being edited, action → chord. This tier, this surface. */
  draft: Record<string, string>;
  /** The dealer override in force above this surface, if any. */
  dealer?: readonly PlatformKey[];
  /** The keys the shell hardcodes, from the server. */
  reserved?: readonly ReservedKey[];
 /** The OTHER substrate's roster: for the warning, never for a refusal. */
  other?: readonly PlatformKey[];
}

const FREE: KeyVerdict = { kind: 'free', sentence: '' };

/**
 * effectiveVerdict: the whole item-4c decision for one candidate binding, in one call, so that the
 * platform configurator and the dealer's counter editor cannot drift into two different answers
 * about the same keyboard.
 *
 * THE ORDER OF THE ARMS IS THE RULE. Reserved first: a key no tier can hold is not a collision to be
 * negotiated, it is simply not available, and saying so before naming a dealer's action keeps the
 * refusal one sentence. Then this roster (the case that already existed). Then the tier above. The
 * cross-substrate warning is LAST and only reached when nothing refused, because a warning printed
 * beside a refusal is a screen asking a person to read two things to learn one.
 */
export function effectiveVerdict(
  surface: string,
  action: string,
  binding: string,
  ctx: EffectiveContext,
): KeyVerdict {
  if (!binding) return FREE;
  const held = reservedHolderFor(binding, ctx.reserved);
  if (held) {
    return {
      kind: 'taken',
      sentence: `${platformKeycap(surface, binding)} ${held} on every screen. No roster can take it, so pick another key.`,
    };
  }
  const clash = platformCollisionFor(ctx.draft, action, binding);
  if (clash) return { kind: 'taken', sentence: platformClashSentence(surface, binding, clash) };
  const overridden = (ctx.dealer ?? []).find(
    (d) => d && sameBinding(d.binding, binding) && d.action !== action,
  );
  if (overridden) {
    return {
      kind: 'taken',
      sentence: `${platformKeycap(surface, binding)} is already this dealer's key for “${platformActionLabel(
        surface,
        overridden.action,
      )}”. Free it in Counter settings first, or pick another key.`,
    };
  }
  const elsewhere = (ctx.other ?? []).find((o) => o && sameBinding(o.binding, binding));
  if (elsewhere) {
    const there = otherSurface(surface);
    return {
      kind: 'shared',
      sentence: `${platformKeycap(surface, binding)} also runs “${platformActionLabel(
        there,
        elsewhere.action,
      )}” ${PLATFORM_SURFACE_LABEL[there].toLowerCase()}. Both keep working: the till answers first while a sale is open.`,
    };
  }
  return FREE;
}

/**
 * Is this the same physical key? The two substrates spell it differently: the shell stores "F2",
 * the counter stores "f2": and a check that missed that would be a check that never fires on the
 * one overlap this product actually ships.
 */
function sameBinding(a: string, b: string): boolean {
  return (a || '').toLowerCase() === (b || '').toLowerCase();
}

/**
 * What the shell already does with this keystroke, in plain words, or ''.
 *
 * The comparison folds the spellings (either separator, either modifier order, the arrow glyph
 * written out) the way the server's canonicalReservedForm does, because a row can arrive from four
 * writers with four spellings and a rule that catches one of them reports itself clean.
 */
export function reservedHolderFor(binding: string, reserved: readonly ReservedKey[] | undefined): string {
  const want = reservedForm(binding);
  if (!want) return '';
  for (const r of reserved ?? []) {
    if (reservedForm(r.binding) === want) return r.holds;
  }
  return '';
}

function reservedForm(binding: string): string {
  const s = (binding || '').trim().toLowerCase().replace(/\+/g, '-').replace(/←/g, 'arrowleft');
  if (!s) return '';
  const parts = s.split('-');
  const base = parts[parts.length - 1];
  return [...parts.slice(0, -1).sort(), base].join('-');
}

// ── ITEM 4d · THE OVERLAY'S COUNTER ROWS ────────────────────────────────────────────────────────

/** The neutral availability the overlay reads the roster at: see counterHelpRows. */
const OVERLAY_STATE: HotkeyState = {
  step: '',
  lineCount: 0,
  rungDone: false,
  ringBusy: false,
  tenderable: false,
};

/**
 * counterHelpRows: the counter's roster as `?`-overlay rows.
 *
 * IT GOES THROUGH legendKeys ON PURPOSE, and that is the whole mechanism of "the overlay and the
 * legend never disagree". They do not compare answers and they do not exchange messages; the
 * overlay's rows ARE the legend's rows, drawn from the same roster through the same dedupe, the
 * same drop-what-this-build-cannot-render rule and the same keycap() spelling. Two components
 * reading one function cannot hold two opinions. (Before this they held three: the overlay read
 * dealer_hotkeys through hotkey-logic.ts's registry and the legend read the counter roster through
 * _pos-hotkeys.ts, so five keys produced two lists that disagreed on three of them.)
 *
 * THE STATE IS NEUTRAL BECAUSE THE OVERLAY IS NOT THE STRIP. legendKeys' `live` flag dims a key that
 * is unavailable at this step in this sale; the overlay is opened from anywhere in the app and is a
 * catalogue of what the keys ARE, not a readout of what is pressable this second. Dimming here
 * would be a claim the overlay has no way to keep current.
 *
 * THE WORDS ARE THE SENTENCE SET, NOT THE LEGEND'S. "Pay" is exactly right on a strip pinned across
 * the foot of a till where the keycap beside it carries the rest and the reader is mid-sale; in a
 * list read beside "Start a new sale at the till" it is a fragment. Both label sets stay: this
 * file's header already argues why: and the ROW SET is what must not differ, not the wording.
 */
export function counterHelpRows(roster: readonly PosHotkey[] | null | undefined): HotkeyHelpRow[] {
  return legendKeys(roster, OVERLAY_STATE).map((k) => ({
    binding: k.keycap,
    label: COUNTER_ACTION_LABEL[k.action] ?? k.label,
  }));
}

/** The refusal for a keystroke that is not bindable, in each surface's own terms. */
export function platformBadChordSentence(surface: string): string {
  return surface === PLATFORM_COUNTER_SURFACE
    ? 'That key types into the sale. Use a function key, or hold Ctrl or Alt with a letter.'
    : 'That combination cannot be bound. Use a function key, or hold Ctrl or Alt with a letter.';
}

/**
 * The server's own sentence, fit to be painted on a screen.
 *
 * WHY THE PREFIX IS STRIPPED HERE. Every service in this product prefixes its error strings with
 * its own module name: "settings: …", "pos: …": deliberately, because the prefix is greppable and
 * survives being wrapped. It is engineering vocabulary all the same, and the house rule is that no
 * `module:`-prefixed string reaches a screen. The dealer's counter editor paints the server's
 * sentence verbatim today and therefore does paint the prefix; rather than repeat that here, this
 * boundary takes it off. The strip is conservative: one leading lowercase word, a colon, a space,
 * which is the exact shape the Go side emits and not a shape any real sentence starts with: and it
 * is a no-op on a message that never had one, so it stays correct if the envelope is ever cleaned up
 * server-side.
 */
export function serverSentence(msg: string): string {
  return (msg || '').replace(/^[a-z][a-z0-9_-]*:\s+/, '');
}

/**
 * A synthetic KeyboardEvent-shaped object for a stored SHELL binding, so a stored spelling can be
 * checked against the one grammar rather than against a second regex written here. Round-tripping a
 * binding through bindingFromEvent is the cheapest honest way to ask "would pressing this produce
 * exactly this string": and if the answer is no, the row is a key that can be stored and never
 * pressed, which is the failure mode the whole grammar discipline exists to prevent.
 */
function eventFromShellBinding(binding: string): {
  key: string;
  ctrlKey: boolean;
  altKey: boolean;
  shiftKey: boolean;
  metaKey: boolean;
} {
  const parts = binding.split('+');
  const key = parts[parts.length - 1] ?? '';
  const mods = parts.slice(0, -1);
  return {
    key,
    ctrlKey: mods.includes('Ctrl'),
    altKey: mods.includes('Alt'),
    shiftKey: mods.includes('Shift'),
    metaKey: false,
  };
}
