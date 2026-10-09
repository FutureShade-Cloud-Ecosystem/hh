// The state icons, as GEOMETRY. Until the ERP icon registry lands behind the
// seam documented in src/icons/README.md, this package holds the five marks
// its state system needs plus the guided flow's done check, hand-drawn on the
// house 24 grid: stroked, not filled, 2 stroke, round caps and joins, in
// currentColor, no colour of their own. The same rules the ERP's registry
// carries, held here by test/kits.test.mjs: path data only (no scripts, no
// event attributes, no external references, no embedded images), and every
// mark pairs with a word at its use site, because colour never encodes state
// alone and an icon alone is still not a word.
//
// These are deliberately dull geometry, not identity marks: they exist so a
// state indicator keeps its shape when colour is removed.
//
// The geometry is bound through the rendering layer's escaped attribute path,
// never joined into a markup string: each path's d is one interpolated
// attribute value, so even a poisoned registry entry could not close the
// attribute and inject.

import { html, nothing, svg, type TemplateResult as Template } from 'lit';

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
  // a step that is done: the check that pairs with the word "done".
  check: { paths: ['M5 12l5 5 9-10'] },
} as const satisfies Record<string, StateIcon>;

export type StateIconName = keyof typeof STATE_ICONS;

/** Renders an icon as inline SVG through the escaped template path. With a
 * label the mark is a labelled image carrying its own text alternative, for
 * standalone use; beside a visible word (the state system's case) render it
 * bare so the word is not announced twice.
 *
 * The label is the aria-label attribute, not a <title> child: Lit allows no
 * expression inside a raw-text element's body (title, textarea, style, script
 * are read as text by the template parser), and a raw-markup path to put the
 * label there is exactly what this package refuses. role stays img, so the
 * accessible name is carried either way.
 *
 * The paths are built with the svg tag, not html: a template made with html
 * is parsed as HTML even when it is slotted into an svg, so its paths would
 * be HTMLUnknownElements and paint nothing. test/browser.test.mjs holds the
 * namespace in a real page. */
export function icon(name: StateIconName, label?: string): Template {
  const paths = STATE_ICONS[name].paths;
  return html`<svg
    class="icon"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="2"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="${label ? 'false' : 'true'}"
    role="${label ? 'img' : 'presentation'}"
    aria-label=${label ?? nothing}
  >${paths.map((d) => svg`<path d="${d}"></path>`)}</svg>`;
}
