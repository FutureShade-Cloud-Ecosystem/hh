// Ported from hardscapeos_dibbits master 87d7941b app/src/_icon-consumer.ts; adapted for HH.

// _icon-consumer.ts - the accessor layer over the generated icon registry (HH-175, DIB-ICONS-1.3).
//
// WHAT THIS FILE IS. The ONE seam between the generated registry (_icon-registry.ts, written by
// scripts/import-icons.mjs) and every surface that draws a module or unit icon: the launcher, the
// tab strip's switcher panel, the rail, the unit overview tiles and the pin lists all resolve
// through here, and this file owns the three rules the lane's plan recorded:
//
// 1. id lookup. A glyph string in an icon slot is a namespaced registry id (lucide:<name>,
// pencil:<name>), never a character. A character is only an icon if the machine has a font
// that covers it; geometry draws on any machine (the _tile-icons.ts ruling, made general).
// 2. the defaults record. Checkpoint A approved one default icon per module and per unit
// (src/icons/DEFAULTS-PROPOSAL.md). Its rows are carried here VERBATIM, typed so the
// compiler names any Tab or UnitKey that has no row. app-shell.ts's SCREEN_META and
// UNIT_META hold the same ids as their glyph fields; _icon-consumer.test.mjs pins the two
// to this record so a default cannot drift between the shell and this file in silence.
// 3. never an empty slot. An unknown id falls back to the module default when the caller
// names one, then to the neutral square - a rendering decision, the same posture
// _tile-icons.ts takes for an unmapped tile id. Nothing renders as an empty box, and
// nothing renders as text.
//
// THE HIDE SUBSTITUTION (plan ruling 1, recorded at intake and in the generated header).
// Pencil's own hide control exports as a bare curve, so the runtime hide control draws
// lucide:eye-off instead of pencil:hide. The substitution is a MAPPING, not a gap: both ids
// exist in the registry, and only this file maps between them.
//
// THE ONE SVG BUILDER. drawIcon applies the house drawing rules exactly as _tile-icons.ts
// documents them - 24x24 viewBox, stroked not filled, stroke 2, round caps and joins,
// currentColor so every icon inherits its surface's ink, aria-hidden because the text label
// beside an icon is its accessible name, never the drawing. One weight for every icon in the
// system is the rule; _home-icons.test.mjs's weight gate holds every 24x24 <svg> in src to it.

import { svg } from 'lit';
import type { SVGTemplateResult } from 'lit';
import type { NavModuleKey, NavUnitKey } from './ports.js';
import type { ModuleGlyph } from './_glyph.js';
import { ICON_REGISTRY } from './_icon-registry.js';
import type { TileIcon } from './_tile-icons.js';

/**
 * The approved defaults (checkpoint A, DEFAULTS-PROPOSAL.md), verbatim, keyed by the shell's own
 * Tab and UnitKey. `Record<NavModuleKey, string>` and `Record<NavUnitKey, string>` are complete
 * by construction: adding a screen to dashboard-nav.ts without giving it a default does not
 * compile. The two maps are separate because one key can be BOTH a module and a unit - `yard`
 * the screen (Pick Queue) and `yard` the app draw different marks by approval, and one flat map
 * would have collapsed them silently.
 */
export const DEFAULTS: Readonly<{
  modules: Record<NavModuleKey, string>;
  units: Record<NavUnitKey, string>;
}> = Object.freeze({
  modules: Object.freeze({
    catalogue: 'lucide:brick-wall',
    calculator: 'lucide:ruler',
    quote: 'lucide:file-search',
    quotes: 'lucide:file-pen-line',
    pos: 'lucide:receipt',
    invoices: 'lucide:receipt-text',
    returns: 'lucide:undo-2',
    pricing: 'lucide:tag',
    gl: 'lucide:book-open-text',
    bankrec: 'lucide:landmark',
    statements: 'lucide:chart-column',
    ar: 'lucide:hand-coins',
    inventory: 'lucide:package',
    lien: 'lucide:scale',
    tax: 'lucide:percent',
    warranty: 'lucide:shield-check',
    dispatch: 'lucide:truck',
    yard: 'lucide:forklift',
    purchasing: 'lucide:clipboard-list',
    reporting: 'lucide:chart-line',
    staff: 'lucide:users',
    approvals: 'lucide:badge-check',
    audit: 'lucide:scroll-text',
    assist: 'lucide:sparkles',
    'assist-admin': 'lucide:bot',
    hotkeys: 'lucide:keyboard',
    account: 'lucide:circle-user',
    messaging: 'lucide:message-square',
  }),
  units: Object.freeze({
    sales: 'lucide:store',
    finance: 'lucide:wallet',
    inventory: 'lucide:package',
    compliance: 'lucide:scale',
    messaging: 'lucide:message-square',
    warranty: 'lucide:shield-check',
    dispatch: 'lucide:truck',
    yard: 'lucide:warehouse',
    purchasing: 'lucide:clipboard-list',
    reporting: 'lucide:chart-line',
    admin: 'lucide:settings',
  }),
});

/** The one recorded substitution (plan ruling 1): the runtime hide control draws lucide:eye-off. */
const SUBSTITUTIONS: Readonly<Record<string, string>> = Object.freeze({
  'pencil:hide': 'lucide:eye-off',
});

/** The id the runtime hide control draws, named so no caller re-types the ruling. */
export const HIDE_ICON_ID = 'lucide:eye-off';

/**
 * A record's OWN value for a key, or undefined. Every lookup below goes through this, because a
 * plain index read consults the prototype chain: `table['constructor']`, `table['toString']` and
 * `table['__proto__']` all resolve to inherited Object members, so a name like `constructor` was
 * safe in the glyph path only by accident (the Function it returned then missed the registry and
 * fell through to the fallback). Object.hasOwn makes that safety by construction: a prototype
 * name is not an entry, in the substitution map, the registry or the defaults record.
 */
function ownValue<T>(record: Record<string, T>, key: string): T | undefined {
  return Object.hasOwn(record, key) ? record[key] : undefined;
}

/** An id with the recorded substitutions applied. Unknown ids pass through untouched. */
export function resolveIconId(id: string): string {
  return ownValue(SUBSTITUTIONS, id) ?? id;
}

/** The registry entry for an id, or undefined. The gate's predicate as well as the lookup. */
export function registryIcon(id: string): TileIcon | undefined {
  return ownValue(ICON_REGISTRY, resolveIconId(id));
}

/**
 * The neutral square for an id nobody knows. Deliberately dull, exactly like _tile-icons.ts's
 * fallback: visible to whoever shipped the unmapped id, never illegible to the person using it.
 */
const FALLBACK_ICON: TileIcon = { paths: ['M 5 5.5 H 19 V 18.5 H 5 Z'] };

/**
 * The icon for a registry id. An unknown id falls back to the module default when the caller
 * names the module it was reaching for, then to the neutral square - never undefined, never an
 * empty slot, never a character.
 */
export function iconForId(id: string, module?: string): TileIcon {
  const hit = registryIcon(id);
  if (hit) return hit;
  if (module !== undefined) {
    const def = ownValue(DEFAULTS.modules, module);
    const viaDefault = def ? registryIcon(def) : undefined;
    if (viaDefault) return viaDefault;
  }
  return FALLBACK_ICON;
}

/** The approved default icon for a module screen (a Tab key). */
export function moduleIcon(tab: string): TileIcon {
  const def = ownValue(DEFAULTS.modules, tab);
  return (def ? registryIcon(def) : undefined) ?? FALLBACK_ICON;
}

/** The approved default icon for an operational unit (a UnitKey). */
export function unitIcon(unit: string): TileIcon {
  const def = ownValue(DEFAULTS.units, unit);
  return (def ? registryIcon(def) : undefined) ?? FALLBACK_ICON;
}

/**
 * THE ONE SVG BUILDER, per _tile-icons.ts's documented rules: 24x24 viewBox, stroked not filled,
 * stroke 2, round caps and joins, currentColor, aria-hidden, one `d` per path in drawing order.
 * Every consumer renders through this function, which is what makes the whole app one visual
 * weight; nothing else in src may open an icon `<svg>` tag.
 */
export function drawIcon(icon: TileIcon, size = 20): SVGTemplateResult {
  // HH note: the reference bound the size attributes here. This app's template
  // fence allows bound values only on a small attribute allow list (width and
  // height are not on it), so the icon opens from a per-size static branch
  // instead: the same rendered element, one static branch per size the surface
  // asks for (12, 19 and the 20 default).
  const paths = icon.paths.map((d) => svg`<path d="${d}" />`);
  if (size === 12) {
    return svg`<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">${paths}</svg>`;
  }
  if (size === 19) {
    return svg`<svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">${paths}</svg>`;
  }
  return svg`<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">${paths}</svg>`;
}

/**
 * The renderer's entry for a ModuleGlyph. A DRAWING passes through untouched (TanyaAI's mark,
 * _tanya-mark.ts, already honours currentColor by construction); a string is a registry id and
 * resolves through it. A string that resolves to nothing still draws the neutral square, so a
 * caller can never turn an icon slot into an empty box by handing it a typo.
 */
export function drawGlyph(glyph: ModuleGlyph, size = 20): SVGTemplateResult {
  return typeof glyph === 'string' ? drawIcon(iconForId(glyph), size) : glyph;
}
