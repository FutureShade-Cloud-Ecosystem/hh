// The two new areas' shared surface classes: cards border first and no
// shadow, every value a design system token, status a word before it is a
// colour, figures in tabular columns lined up on the end, and a wide table
// scrolling inside its own card so the page never scrolls sideways. On a
// phone a table marked stack becomes a card per row: every cell keeps its
// label, none is hidden. The procurement slice keeps its own styles file;
// these are the classes the inventory and counts areas share.
import { css } from "lit";

export const areaStyles = css`

  .desk { max-width: 1440px; margin: 0 auto; padding: var(--hh-s-16) var(--hh-s-24) var(--hh-s-48); }
  h1 { margin: 0; font-size: 28px; line-height: 1.2; font-weight: 700; }
  h2 { margin: 0; font-size: 20px; line-height: 1.25; font-weight: 700; }
  h3 { margin: 0 0 var(--hh-s-8); font-size: 14px; letter-spacing: 0.08em; text-transform: uppercase; color: var(--hh-accent-text); font-weight: 700; }
  p { margin: 0 0 var(--hh-s-8); }
  .dim { color: var(--hh-text-dim); }
  .head { display: flex; flex-wrap: wrap; align-items: baseline; justify-content: space-between; gap: var(--hh-s-8) var(--hh-s-16); margin-bottom: var(--hh-s-12); }
  .tabs { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-8); margin: 0 0 var(--hh-s-16); }
  .tab { min-height: 44px; border-radius: var(--hh-radius-pill); background: transparent; color: var(--hh-text-dim); border: 1px solid transparent; padding: 0 var(--hh-s-16); }
  .tab[aria-current="true"] { background: var(--hh-card); color: var(--hh-text); border-color: var(--hh-control-line); font-weight: 700; }
  .tab:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: 2px; }

  .panel { background: var(--hh-card); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); padding: var(--hh-s-20); margin-bottom: var(--hh-s-16); min-width: 0; }
  .panel-head { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: var(--hh-s-8) var(--hh-s-16); margin-bottom: var(--hh-s-12); }
  .panel-head h3 { margin: 0; }
  .cols { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: var(--hh-s-16); align-items: start; }
  .cols > * { min-width: 0; }

  .alert { border-radius: var(--hh-radius-control); padding: var(--hh-s-12) var(--hh-s-16); margin: 0 0 var(--hh-s-16); border: 1px solid var(--hh-line); background: var(--hh-card); }
  .alert-error { border-color: var(--hh-state-stop); color: var(--hh-text); border-inline-start-width: 4px; }
  .alert-ok { border-color: var(--hh-state-good); color: var(--hh-text); border-inline-start-width: 4px; }
  .alert strong { display: block; }
  .alert p { margin: var(--hh-s-4) 0 0; }

  .scroll { overflow-x: auto; position: relative; }
  table { width: 100%; border-collapse: collapse; font-size: 15px; }
  th { text-align: start; font-weight: 700; padding: var(--hh-s-8); border-bottom: 1px solid var(--hh-line); color: var(--hh-text-dim); white-space: nowrap; font-size: 12px; letter-spacing: 0.08em; text-transform: uppercase; }
  td { padding: var(--hh-s-8); border-bottom: 1px solid var(--hh-line); vertical-align: middle; }
  tbody tr:last-child td { border-bottom: 0; }
  th.num, td.num { text-align: end; font-variant-numeric: tabular-nums; white-space: nowrap; }
  td.stop { color: var(--hh-state-stop); font-weight: 700; }
  td.good { color: var(--hh-state-good); font-weight: 700; }
  td.sku { font-family: var(--hh-font-mono); font-weight: 700; white-space: nowrap; }
  td.code { font-family: var(--hh-font-mono); white-space: nowrap; }
  .who { display: block; font-weight: 700; white-space: nowrap; }
  .small { display: block; font-size: 13px; }
  .mono { font-family: var(--hh-font-mono); }
  .empty { padding: var(--hh-s-24) var(--hh-s-8); }
  .empty strong { display: block; font-size: 18px; margin-bottom: var(--hh-s-4); }

  .status { display: inline-flex; align-items: center; padding: var(--hh-s-2) var(--hh-s-12); border-radius: var(--hh-radius-pill); font-size: 12px; font-weight: 700; letter-spacing: 0.06em; text-transform: uppercase; border: 1px solid var(--hh-line); white-space: nowrap; }
  .status-stop { color: var(--hh-state-stop); border-color: var(--hh-state-stop); }
  .status-watch { color: var(--hh-state-watch); border-color: var(--hh-state-watch); }
  .status-good { color: var(--hh-state-good); border-color: var(--hh-state-good); }
  .status-plain { color: var(--hh-text-dim); }

  label { display: grid; gap: var(--hh-s-4); font-size: 14px; color: var(--hh-text-dim); font-weight: 600; }
  input, select { font: inherit; font-size: 16px; min-height: 44px; padding: 0 var(--hh-s-12); background: var(--hh-field); color: var(--hh-text); border: 1px solid var(--hh-control-line); border-radius: var(--hh-radius-control); width: 100%; box-sizing: border-box; }
  input:focus-visible, select:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: 2px; }
  .check { display: flex; align-items: center; gap: var(--hh-s-8); font-weight: 400; }
  .check input { width: 20px; min-height: 20px; }
  .forms { display: flex; flex-wrap: wrap; gap: var(--hh-s-12); align-items: end; }
  .forms label { min-width: 180px; flex: 1 1 180px; }
  .forms label.narrow { min-width: 120px; flex: 0 1 140px; }
  .actions { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); align-items: center; margin-top: var(--hh-s-12); }
  .actions.end { justify-content: flex-end; }
  .btn { white-space: nowrap; }
  .small-btn { min-height: 36px; padding: 0 var(--hh-s-12); font-size: 14px; }
  .foot { margin: var(--hh-s-12) 0 0; font-size: 14px; }

  /* A consequential press asks first: the confirm is a bordered box naming the facts. */
  .confirm { border: 1px solid var(--hh-state-watch); border-radius: var(--hh-radius-control); padding: var(--hh-s-16); margin: var(--hh-s-12) 0; background: color-mix(in srgb, var(--hh-accent) 18%, transparent); }
  .confirm strong { display: block; margin-bottom: var(--hh-s-4); }
  .confirm p { margin: 0 0 var(--hh-s-4); font-size: 15px; }
  .scrim { position: fixed; inset: 0; background: color-mix(in srgb, var(--hh-page) 60%, transparent); display: grid; place-items: center; padding: var(--hh-s-16); z-index: 10; }
  .modal { background: var(--hh-card); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); padding: var(--hh-s-24); max-width: 460px; width: 100%; }
  .modal h3 { margin-top: 0; }
  dl { margin: 0; }
  .facts { display: grid; grid-template-columns: max-content minmax(0, 1fr); gap: var(--hh-s-4) var(--hh-s-24); margin-bottom: var(--hh-s-12); }
  .facts dt { color: var(--hh-text-dim); }
  .facts dd { margin: 0; overflow-wrap: anywhere; }
  .chips { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); margin: 0 0 var(--hh-s-12); }
  .chip { display: inline-flex; gap: var(--hh-s-8); align-items: baseline; padding: var(--hh-s-4) var(--hh-s-12); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-pill); font-size: 14px; }
  .chip .figure { font-variant-numeric: tabular-nums; font-weight: 700; }

  .mob { display: none; }
  @media (max-width: 1100px) {
    .cols { grid-template-columns: minmax(0, 1fr); }
  }
  @media (max-width: 700px) {
    /* A table marked stack becomes a card per row: every cell keeps its label, none is hidden. */
    table.stack, .stack tbody { display: block; }
    .stack thead { display: none; }
    .stack tbody tr { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: var(--hh-s-8) var(--hh-s-16); padding: var(--hh-s-12) var(--hh-s-8); border-bottom: 1px solid var(--hh-line); }
    .stack td { display: block; padding: 0; border: 0; text-align: start; min-width: 0; }
    .stack td.num { text-align: start; }
    .stack td::before { content: attr(data-label); display: block; font-size: 11px; letter-spacing: 0.08em; text-transform: uppercase; color: var(--hh-text-dim); font-weight: 700; }
    .stack td.pick, .stack td:not([data-label]), .stack td[colspan] { grid-column: 1 / -1; }
    .stack td.pick::before { display: none; }
  }
  @media (max-width: 520px) {
    .desk { padding: var(--hh-s-16) var(--hh-s-12) var(--hh-s-40); }
  }
`;
