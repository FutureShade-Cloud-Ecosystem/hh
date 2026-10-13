// The statements slice's own styles. Every value is a token of the design system; nothing here is a literal
// colour. Status is a word before it is a colour, figures sit in tabular columns and line up on the right, a
// wide table scrolls inside its own card so the page never scrolls sideways, and on a phone the tables that
// carry text give way to a stack of cards. A statement is a document: the print stylesheet at the foot drops
// the chrome, the controls and the tabs, and leaves the title, the period, the scope and the table on the
// sheet. Class names carry an fs- prefix so the slice shares an element with the others without either's
// rules reaching the other.
import { css } from "lit";

export const finstmtStyles = css`
  main.fs-desk { max-width: 1440px; margin: 0 auto; padding: var(--hh-s-24) var(--hh-s-24) var(--hh-s-48); }
  .fs-head { margin-bottom: var(--hh-s-16); }
  .fs-head h1 { margin: 0; font-size: 28px; line-height: 1.2; font-weight: 700; }
  .fs-head p { margin: var(--hh-s-4) 0 0; }
  .fs-dim { color: var(--hh-text-dim); }
  .fs-sub { margin: var(--hh-s-4) 0 0; }
  .fs-print-only { display: none; }

  .fs-tabs { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); margin: var(--hh-s-16) 0; }
  .fs-tab { min-height: 44px; border-radius: var(--hh-radius-pill); background: transparent; color: var(--hh-text); border: 1px solid var(--hh-control-line); padding: 0 var(--hh-s-20); }
  .fs-tab[aria-current="true"] { background: var(--brand-fill, var(--hh-accent)); color: var(--brand-fill-on, var(--hh-accent-ink)); border-color: var(--brand-fill-on, var(--hh-accent-ink)); font-weight: 700; }

  .fs-panel { background: var(--hh-card); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); padding: var(--hh-s-20); min-width: 0; margin-bottom: var(--hh-s-16); }
  .fs-panel h2 { margin: 0; font-size: 20px; line-height: 1.25; font-weight: 700; }
  .fs-panel h3 { margin: 0; font-size: 14px; letter-spacing: 0.08em; text-transform: uppercase; color: var(--hh-accent-text); font-weight: 700; }
  .fs-panel-head { display: flex; flex-wrap: wrap; align-items: flex-start; justify-content: space-between; gap: var(--hh-s-8) var(--hh-s-16); margin-bottom: var(--hh-s-12); }
  .fs-heading-marks { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); }

  .fs-alert { border-radius: var(--hh-radius-control); padding: var(--hh-s-12) var(--hh-s-16); margin: 0 0 var(--hh-s-16); border: 1px solid var(--hh-line); background: var(--hh-card); overflow-wrap: anywhere; }
  .fs-alert-error { border-color: var(--hh-state-stop); border-inline-start-width: 4px; background: color-mix(in srgb, var(--hh-state-stop) 12%, var(--hh-card)); }
  .fs-alert-ok { border-color: var(--hh-state-good); border-inline-start-width: 4px; }
  .fs-note { margin: var(--hh-s-12) 0 0; padding: var(--hh-s-12) var(--hh-s-16); border-radius: var(--hh-radius-control); background: color-mix(in srgb, var(--hh-accent) 18%, var(--hh-card)); color: var(--hh-text); font-size: 14px; }
  .fs-later { display: inline-block; padding: 0 var(--hh-s-8); border: 1px dashed var(--hh-text-dim); border-radius: var(--hh-radius-pill); font-size: 12px; letter-spacing: 0.08em; text-transform: uppercase; color: var(--hh-text-dim); font-weight: 700; vertical-align: middle; }

  .fs-status { display: inline-flex; align-items: center; gap: var(--hh-s-8); padding: var(--hh-s-2) var(--hh-s-12); border-radius: var(--hh-radius-pill); font-size: 13px; font-weight: 700; border: 1px solid var(--hh-line); }
  .fs-status::before { content: ""; flex: none; width: 8px; height: 8px; border-radius: var(--hh-radius-pill); background: currentColor; }
  .fs-status-stop { color: var(--hh-state-stop); border-color: var(--hh-state-stop); }
  .fs-status-watch { color: var(--hh-state-watch); border-color: var(--hh-state-watch); }
  .fs-status-good { color: var(--hh-state-good); border-color: var(--hh-state-good); }
  .fs-status-plain { color: var(--hh-text-dim); }

  /* The choice of books. */
  .fs-controls { display: flex; flex-wrap: wrap; align-items: flex-end; justify-content: space-between; gap: var(--hh-s-12) var(--hh-s-16); }
  .fs-form { display: grid; grid-template-columns: repeat(auto-fit, minmax(180px, 1fr)); gap: var(--hh-s-12); margin: 0 0 var(--hh-s-12); flex: 1 1 520px; }
  .fs-controls .fs-form { margin: 0; }
  .fs-field { display: grid; gap: var(--hh-s-4); font-size: 14px; color: var(--hh-text-dim); font-weight: 600; min-width: 0; }
  .fs-field-wide { grid-column: 1 / -1; }
  .fs-field input, .fs-field select { font: inherit; font-size: 16px; min-height: 44px; padding: var(--hh-s-8) var(--hh-s-12); background: var(--hh-field); color: var(--hh-text); border: 1px solid var(--hh-control-line); border-radius: var(--hh-radius-control); width: 100%; box-sizing: border-box; }
  .fs-field input:focus-visible, .fs-field select:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: 2px; }
  .fs-actions { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); align-items: center; }

  /* The figures at the head of a statement, the server's own totals. */
  .fs-tiles { display: grid; grid-template-columns: repeat(auto-fit, minmax(170px, 1fr)); gap: var(--hh-s-8); margin: var(--hh-s-8) 0 var(--hh-s-16); }
  .fs-tiles div { padding: var(--hh-s-12); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-control); min-width: 0; }
  .fs-tiles dt { font-size: 12px; letter-spacing: 0.08em; text-transform: uppercase; color: var(--hh-text-dim); font-weight: 700; }
  .fs-tiles dd { margin: var(--hh-s-4) 0 0; font-size: 20px; font-weight: 700; font-variant-numeric: tabular-nums; overflow-wrap: anywhere; }
  .fs-tiles-aging { grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); }
  .fs-tile-total { border-color: var(--hh-text) !important; }

  .fs-empty { padding: var(--hh-s-24) var(--hh-s-8); display: grid; gap: var(--hh-s-4); }
  .fs-empty strong { font-size: 18px; }

  /* The tables. */
  .fs-scroll { overflow-x: auto; }
  .fs-panel table { width: 100%; border-collapse: collapse; font-size: 15px; }
  .fs-panel th { text-align: start; font-weight: 700; padding: var(--hh-s-8) var(--hh-s-12); border-bottom: 1px solid var(--hh-line); color: var(--hh-text-dim); white-space: nowrap; }
  .fs-panel td { padding: var(--hh-s-12); border-bottom: 1px solid var(--hh-line); vertical-align: middle; }
  .fs-panel th.num, .fs-panel td.num { text-align: end; font-variant-numeric: tabular-nums; white-space: nowrap; }
  .fs-panel tr[aria-current="true"] td { background: var(--hh-row-alt); }
  .fs-panel tr.fs-section th { background: var(--hh-row-alt); color: var(--hh-text); text-transform: uppercase; letter-spacing: 0.08em; font-size: 13px; }
  .fs-panel tr.fs-total td { border-top: 2px solid var(--hh-line); border-bottom: 0; }
  .fs-panel tr.fs-grand td { border-top: 3px double var(--hh-text); font-size: 17px; }
  .fs-panel th.fs-col-code, .fs-panel td.fs-col-code { width: 140px; }
  .fs-nowrap { white-space: nowrap; }
  .fs-wrap { overflow-wrap: anywhere; }
  .fs-code-narrow { display: none; color: var(--hh-text-dim); font-size: 12px; }
  .fs-name { display: block; }
  .fs-link { white-space: nowrap; background: transparent; border: 0; padding: var(--hh-s-4) 0; min-height: 36px; color: var(--hh-accent-text); font-weight: 700; text-decoration: underline; margin-inline-end: var(--hh-s-12); }
  .fs-link:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: 2px; }
  .fs-confirm { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); align-items: center; }

  /* A phone shows the figures: the code gives way under the name, and the tables that carry text become cards. */
  @media (max-width: 520px) {
    main.fs-desk { padding: var(--hh-s-16) var(--hh-s-12) var(--hh-s-40); }
    .fs-tabs, .areas { gap: var(--hh-s-4); }
    .fs-tab { padding: 0 var(--hh-s-12); font-size: 14px; }
    /* Three areas share one row on a phone. */
    .areas .gl-tab { padding: 0 var(--hh-s-8); font-size: 13px; }
    .fs-panel { padding: var(--hh-s-16); }
    .fs-panel table { font-size: 14px; }
    .fs-panel th, .fs-panel td { padding: var(--hh-s-8) var(--hh-s-4); }
    .fs-panel th { white-space: normal; }
    .fs-col-code, .fs-col-type, .fs-col-balance { display: none; }
    .fs-code-narrow { display: block; }
    .fs-form { flex-basis: 100%; }
    table.fs-cards thead { display: none; }
    table.fs-cards, table.fs-cards tbody, table.fs-cards tr, table.fs-cards td { display: block; width: 100%; box-sizing: border-box; }
    table.fs-cards tr { border: 1px solid var(--hh-line); border-radius: var(--hh-radius-control); margin-bottom: var(--hh-s-8); padding: var(--hh-s-8) var(--hh-s-12); }
    table.fs-cards td { display: flex; justify-content: space-between; gap: var(--hh-s-12); border-bottom: 0; padding: var(--hh-s-4) 0; text-align: end; }
    table.fs-cards td::before { content: attr(data-label); flex: none; color: var(--hh-text-dim); font-weight: 600; text-align: start; }
    table.fs-cards td.fs-col-period, table.fs-cards td.fs-col-source { display: none; }
  }

  /* The printed sheet: the statement and nothing else. */
  @media print {
    :host { background: #fff; color: #000; }
    hos-app-header, nav.areas, .fs-noprint { display: none !important; }
    main.fs-desk { max-width: none; padding: 0; }
    .fs-print-only { display: block; margin: 0 0 var(--hh-s-8); }
    .fs-panel { border: 0; padding: 0; margin: 0; background: transparent; }
    .fs-scroll { overflow: visible; }
    .fs-panel table { font-size: 12px; }
    .fs-panel tr { break-inside: avoid; }
    .fs-panel thead { display: table-header-group; }
    .fs-link { color: inherit; text-decoration: none; }
    .fs-col-code { display: table-cell !important; }
    .fs-code-narrow { display: none !important; }
    .fs-status { border: 0; padding: 0; }
  }
`;
