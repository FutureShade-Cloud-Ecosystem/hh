// The state icons, as GEOMETRY. Until the ERP icon registry lands behind the
// seam documented in src/icons/README.md, this package holds exactly the five
// marks its state system needs, hand-drawn on the house 24 grid: stroked, not
// filled, 2 stroke, round caps and joins, in currentColor, no colour of their
// own. The same rules the ERP's registry carries, held here by test/kits.test.mjs:
// path data only (no scripts, no event attributes, no external references, no
// embedded images), and every mark pairs with a word at its use site, because
// colour never encodes state alone and an icon alone is still not a word.
//
// These are deliberately dull geometry, not identity marks: they exist so a
// state indicator keeps its shape when colour is removed.

import { unsafeSvg } from './lit.js';

export interface StateIcon {
  /** SVG path `d` strings, drawn in order, stroked in currentColor. */
  readonly paths: readonly string[];
}

export const STATE_ICONS = {
  // empty: an inbox tray with nothing in it.
  inbox: { paths: ['M4 4h16v16H4z', 'M4 13h5l2 3h2l2-3h5'] },
  // loading: an open ring, drawn to spin.
  loader: { paths: ['M12 3a9 9 0 1 0 9 9'] },
  // offline: wifi arcs with the slash through them.
  wifiOff: { paths: ['M3 3l18 18', 'M9.5 14.5a4 4 0 0 1 5 0', 'M6 11a9 9 0 0 1 4-2.3', 'M18 11a9 9 0 0 0-3.4-2.1', 'M12 18h.01'] },
  // conflict: two arrows diverging from one line.
  diverge: { paths: ['M9 8l-4 4 4 4', 'M15 8l4 4-4 4', 'M5 12h14'] },
  // error: an octagon with the cross in it.
  alertOctagon: { paths: ['M8 2h8l6 6v8l-6 6H8l-6-6V8z', 'M9 9l6 6', 'M15 9l-6 6'] },
} as const satisfies Record<string, StateIcon>;

export type StateIconName = keyof typeof STATE_ICONS;

/** Renders an icon as inline SVG. The only unsafeSvg call site in the package. */
export function icon(name: StateIconName, label?: string): ReturnType<typeof unsafeSvg> {
  const paths = STATE_ICONS[name].paths;
  const title = label
    ? `<title>${label.replace(/[&<>"]/g, '')}</title>`
    : '';
  const body = paths.map((d) => `<path d="${d}"/>`).join('');
  return unsafeSvg(
    `<svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" ` +
      `stroke-width="2" stroke-linecap="round" stroke-linejoin="round" ` +
      `aria-hidden="${label ? 'false' : 'true'}" role="${label ? 'img' : 'presentation'}">${title}${body}</svg>`,
  );
}
