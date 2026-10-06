// Ported from hardscapeos_dibbits master 87d7941b app/src/_pos-counter-view.ts; adapted for HH.

// _pos-counter-view.ts: WHICH PARTS OF THE COUNTER THIS PERSON WANTS ON SCREEN. Pure.
//
// THE ASK, AND WHY IT IS PER USER AND NOT PER DEALER. Two people at the same yard gave opposite
// answers on the same call. The counter lead wants BisTrack's keys in hand; the back-office lead,
// asked whether the yard is hotkey-heavy, said "No, I don't even know what that is. So, probably
// not": the same side that wants screen real estate back and said "I wouldn't need it [the hot list]
// when I'm in my back office". A dealer-wide switch would have to pick one of them and be wrong for
// the other every day, so this is a USER preference on the presentation substrate
// (surface_user_items, kind `panel`, surface `pos.counter`: the 0140/0149 whole-or-absent idiom),
// with the dealer's role items as the default a person starts from.
//
// TWO THINGS IT DOES NOT DO, both of them the point:
// * Dismissing the legend does NOT disable the keys. The roster is unchanged, F4 still advances,
// F12 still pays. What goes away is the strip of chrome that reminds you: which is exactly
// what was asked for, and nothing more.
// * Nothing here is the only way to do anything. Every action on the legend keeps a visible
// control on the screen; a key is a shortcut. Turning the hot list off leaves search and scan
// where they always were.
//
// WHY `panel` AND NOT A NEW COLUMN. These are "does this pane render", which is the substrate's own
// vocabulary. The kit's user override is whole-or-absent PER KIND, so writing a panel row cannot
// disturb the person's key roster (kind `action`) or their curated tiles (kind `chip`).

import { asRows } from '@hardscapehouse/pos-kit/src/ported/_response-shape.js';

/** The surface these rows are filed under: the same one the roster and the tiles use. */
export const POS_COUNTER_SURFACE_ID = 'pos.counter';

/** The kit item kind: a pane that either renders or does not. */
export const PANEL_KIND = 'panel';

/** The panel ids. STORED: add, never rename (a rename silently resets everybody's choice). */
export const PANEL_LEGEND = 'legend';
export const PANEL_HOTLIST = 'hotlist';

/** The panels this build can turn on and off, in the order the view menu lists them. */
export const COUNTER_PANELS = [PANEL_LEGEND, PANEL_HOTLIST] as const;
export type CounterPanelId = (typeof COUNTER_PANELS)[number];

/** The view menu's words. One or two: this is chrome. */
export const PANEL_LABELS: Record<string, string> = {
  [PANEL_LEGEND]: 'Key legend',
  [PANEL_HOTLIST]: 'Hot list',
};

/**
 * BOTH ON is the shipped default, and it is deliberate rather than lazy. A counter person who has
 * never opened a settings menu should see the keys they can press and the tiles they can tap; the
 * person who wants the screen back can say so once, and it sticks. Defaults do the work.
 */
export const COUNTER_PANEL_DEFAULTS: Readonly<Record<string, boolean>> = {
  [PANEL_LEGEND]: true,
  [PANEL_HOTLIST]: true,
};

/** One curation row as the settings kit sends it. */
export interface SurfaceItem {
  kind: string;
  id: string;
  position: number;
  visible: boolean;
}

/** Loader-boundary coercion (asRows, DIB-471) for one of the config's two item lists. */
export function asSurfaceItems(body: unknown, key: 'role_items' | 'user_items'): SurfaceItem[] {
  return asRows<Record<string, unknown>>(body, [key])
    .filter((r) => r && typeof r.kind === 'string' && typeof r.id === 'string')
    .map((r) => ({
      kind: r.kind as string,
      id: r.id as string,
      position: typeof r.position === 'number' ? r.position : 0,
      visible: r.visible !== false,
    }));
}

/**
 * panelView: is each panel on, for this person, right now?
 *
 * WHOLE OR ABSENT, one level at a time (the 0140/0149 ruling, verbatim): a person who has stored
 * any panel row IS the answer, and the dealer's set is not merged into it. Only when they have
 * stored nothing does the dealer's set answer, and only when the dealer has curated nothing do the
 * shipped defaults. A panel the level did not mention is ON: a level that curated the legend off
 * has said nothing about the hot list.
 */
export function panelView(
  roleItems: readonly SurfaceItem[] | null | undefined,
  userItems: readonly SurfaceItem[] | null | undefined,
): Record<string, boolean> {
  const level = (rows: readonly SurfaceItem[] | null | undefined): SurfaceItem[] =>
    (Array.isArray(rows) ? rows : []).filter((r) => r && r.kind === PANEL_KIND && isCounterPanel(r.id));
  const rows = level(userItems).length > 0 ? level(userItems) : level(roleItems);
  const out: Record<string, boolean> = { ...COUNTER_PANEL_DEFAULTS };
  for (const r of rows) out[r.id] = r.visible;
  return out;
}

/** Is this an id this build can render? An unknown id draws nothing, silently (the 0149 posture). */
export function isCounterPanel(id: string): boolean {
  return (COUNTER_PANELS as readonly string[]).includes(id);
}

/**
 * panelWrite: the WHOLE panel set to send, with one panel flipped.
 *
 * The write is whole because the read is: sending only the changed row would make the stored set
 * mean "this is everything I chose", which it would not be. Positions are the registry's order, so
 * two people who turned different things off still store the same shape.
 */
export function panelWrite(view: Record<string, boolean>, id: string, on: boolean): SurfaceItem[] {
  const next = { ...COUNTER_PANEL_DEFAULTS, ...view, [id]: on };
  return COUNTER_PANELS.map((p, i) => ({ kind: PANEL_KIND, id: p, position: i, visible: next[p] !== false }));
}
