// The purchasing areas' own styles, on top of the procurement slice's (po-styles.ts). Every value
// is a token of the design system; nothing here is a literal colour. Cards border first with no
// shadow, status is a word before it is a colour, figures sit in tabular columns on the right, and
// a wide table becomes a card per row on a phone.
import { css } from "lit";

export const purchStyles = css`
  /* The area switcher: the open area is a white pill, the rest are plain words. */
  .areas { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-8); margin: 0 0 var(--hh-s-16); padding-bottom: var(--hh-s-12); border-bottom: 1px solid var(--hh-line); }
  .areas .tab { min-height: 44px; }

  .tiles-3 { grid-template-columns: repeat(3, minmax(0, 1fr)); }
  .filters { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); margin-bottom: var(--hh-s-12); }
  .filters .tab { min-height: 40px; padding: 0 var(--hh-s-16); }

  /* The step before a consequential press. */
  .confirm { margin-top: var(--hh-s-16); padding: var(--hh-s-16); border: 1px solid var(--hh-state-watch); border-inline-start-width: 4px; border-radius: var(--hh-radius-control); background: color-mix(in srgb, var(--hh-accent) 18%, transparent); }
  .confirm p { margin: 0 0 var(--hh-s-8); }
  /* On the wash the dim text colour falls under 4.5 to 1 in the brand theme: labels take the text colour. */
  .confirm label { color: var(--hh-text); margin-bottom: var(--hh-s-8); }
  .confirm .actions { justify-content: flex-end; }

  .check { display: flex; align-items: center; gap: var(--hh-s-12); min-height: 44px; }
  .check .box { flex: none; }

  .detail-grid { display: grid; grid-template-columns: minmax(0, 2.4fr) minmax(280px, 1fr); gap: var(--hh-s-24); align-items: start; }
  .detail-grid > * { min-width: 0; }
  .compare { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); margin-bottom: var(--hh-s-8); }
  .compare .seg { flex: 0 1 auto; padding: 0 var(--hh-s-16); border-color: var(--hh-control-line); }

  .split { display: grid; grid-template-columns: minmax(0, 1.6fr) minmax(0, 1fr); gap: var(--hh-s-16); align-items: start; }
  .split > * { min-width: 0; }
  .split-rev { grid-template-columns: minmax(440px, 1fr) minmax(0, 1.5fr); }
  .link-inline { white-space: nowrap; }
  .panel > p.dim + .forms, .panel > .forms { margin-top: var(--hh-s-8); }
  .panel.inner { background: var(--hh-row-alt); margin-top: var(--hh-s-16); }
  .inline-form { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: var(--hh-s-8); align-items: end; }
  .code-line { grid-template-columns: minmax(0, 2fr) minmax(0, 1.2fr) minmax(0, 1fr) minmax(0, 1fr) auto; }
  .badge-row { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); align-items: center; }
  .btn-danger { white-space: nowrap; }
  .mono { font-family: var(--hh-font-mono); }
  td.warn { color: var(--hh-state-watch); font-weight: 700; }
  td.warn::before { content: ""; }
  .variance-note { margin: var(--hh-s-4) 0 0; font-size: 13px; }
  .stack-fields { display: grid; gap: var(--hh-s-12); }
  textarea { font: inherit; font-size: 16px; min-height: 88px; padding: var(--hh-s-8) var(--hh-s-12); background: var(--hh-field); color: var(--hh-text); border: 1px solid var(--hh-control-line); border-radius: var(--hh-radius-control); width: 100%; box-sizing: border-box; }
  textarea:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: 2px; }

  @media (max-width: 1100px) {
    .detail-grid, .split, .split-rev { grid-template-columns: minmax(0, 1fr); }
    .code-line { grid-template-columns: minmax(0, 1fr); }
  }
  @media (max-width: 700px) {
    .tiles-3 { grid-template-columns: minmax(0, 1fr); }
    .areas { flex-wrap: nowrap; overflow-x: auto; }
    .areas .tab { white-space: nowrap; }
  }
`;
