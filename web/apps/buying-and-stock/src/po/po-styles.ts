// The procurement slice's own styles. Every value is a token of the design
// system; nothing here is a literal colour. Status is a word before it is a
// colour, figures sit in tabular columns and line up on the right, and a wide
// table scrolls inside its own card so the page never scrolls sideways.
import { css } from "lit";

export const poStyles = css`
  .desk { max-width: 1440px; margin: 0 auto; padding: var(--hh-s-24) var(--hh-s-24) var(--hh-s-48); }
  h1 { margin: 0; font-size: 28px; line-height: 1.2; font-weight: 700; }
  h2 { margin: 0; font-size: 20px; line-height: 1.25; font-weight: 700; }
  h3 { margin: 0 0 var(--hh-s-8); font-size: 14px; letter-spacing: 0.08em; text-transform: uppercase; color: var(--hh-accent-text); font-weight: 700; }
  p { margin: 0 0 var(--hh-s-8); }
  .dim { color: var(--hh-text-dim); }
  .head { display: flex; flex-wrap: wrap; align-items: baseline; justify-content: space-between; gap: var(--hh-s-8) var(--hh-s-16); margin-bottom: var(--hh-s-16); }

  .panel { background: var(--hh-card); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); padding: var(--hh-s-20); margin-bottom: var(--hh-s-16); min-width: 0; }
  .panel-head { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: var(--hh-s-8) var(--hh-s-16); margin-bottom: var(--hh-s-12); }
  .panel-head h3 { margin: 0; }

  .alert { border-radius: var(--hh-radius-control); padding: var(--hh-s-12) var(--hh-s-16); margin: 0 0 var(--hh-s-16); border: 1px solid var(--hh-line); background: var(--hh-card); }
  .alert-error { border-color: var(--hh-state-stop); color: var(--hh-text); border-inline-start-width: 4px; }
  .alert-ok { border-color: var(--hh-state-good); color: var(--hh-text); border-inline-start-width: 4px; }
  .callout { margin: var(--hh-s-16) 0 0; padding: var(--hh-s-12) var(--hh-s-16); border: 1px solid var(--hh-state-watch); border-radius: var(--hh-radius-control); background: var(--hh-row-alt); }

  /* The four tiles. */
  .tiles { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: var(--hh-s-12); margin-bottom: var(--hh-s-16); }
  .tile { display: flex; flex-direction: column; align-items: flex-start; gap: var(--hh-s-4); padding: var(--hh-s-16); background: var(--hh-widget, var(--hh-card)); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-widget, var(--hh-radius-card)); min-width: 0; }
  .tile .kicker { font-size: 12px; letter-spacing: 0.1em; text-transform: uppercase; color: var(--hh-text-dim); font-weight: 700; }
  .tile .figure { font-size: 32px; font-weight: 700; line-height: 1.1; font-variant-numeric: tabular-nums; }
  .tile .figure.stop { color: var(--hh-state-stop); }
  .tile .small { font-size: 14px; color: var(--hh-text-dim); }
  .link { background: transparent; border: 0; padding: var(--hh-s-8) 0 0; min-height: 44px; color: var(--hh-accent-text); font-weight: 700; text-align: start; cursor: pointer; }
  .link:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: 2px; }

  /* Tabs, and the tabs that are not yet. */
  .tabs { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-8); margin: var(--hh-s-16) 0; }
  .tab { min-height: 44px; border-radius: var(--hh-radius-pill); background: transparent; color: var(--hh-text); border: 1px solid var(--hh-control-line); padding: 0 var(--hh-s-20); }
  .tab[aria-current="true"] { background: var(--brand-fill, var(--hh-accent)); color: var(--brand-fill-on, var(--hh-accent-ink)); border-color: var(--brand-fill-on, var(--hh-accent-ink)); font-weight: 700; }
  .tag-later { display: inline-flex; align-items: center; gap: var(--hh-s-8); min-height: 44px; padding: 0 var(--hh-s-12); color: var(--hh-text-dim); }
  .tag-later em { font-style: normal; font-size: 12px; letter-spacing: 0.08em; text-transform: uppercase; padding: 0 var(--hh-s-8); border: 1px dashed var(--hh-text-dim); border-radius: var(--hh-radius-pill); }

  /* The home: the worklist or the draft on the left, the register and incoming beside it. */
  .home { display: grid; grid-template-columns: minmax(0, 1.7fr) minmax(380px, 1fr); gap: var(--hh-s-16); align-items: start; }
  .home-main, .home-side { min-width: 0; }

  /* Tables. */
  .scroll { overflow-x: auto; position: relative; }
  table { width: 100%; border-collapse: collapse; font-size: 15px; }
  th { text-align: start; font-weight: 700; padding: var(--hh-s-8) var(--hh-s-8); border-bottom: 1px solid var(--hh-line); color: var(--hh-text-dim); white-space: nowrap; font-size: 12px; letter-spacing: 0.08em; text-transform: uppercase; }
  td { padding: var(--hh-s-8) var(--hh-s-8); border-bottom: 1px solid var(--hh-line); vertical-align: middle; }
  tbody tr:last-child td { border-bottom: 0; }
  tfoot td { border-top: 1px solid var(--hh-text); border-bottom: 0; font-weight: 700; }
  th.num, td.num { text-align: end; font-variant-numeric: tabular-nums; white-space: nowrap; }
  td.stop { color: var(--hh-state-stop); font-weight: 700; }
  td.good { color: var(--hh-state-good); font-weight: 700; }
  td.sku { font-family: var(--hh-font-mono); font-weight: 700; white-space: nowrap; }
  tr.hot td { background: var(--hh-row-alt); }
  tr[aria-current="true"] td { background: var(--hh-row-alt); }
  .who { display: block; font-weight: 700; white-space: nowrap; }
  [data-expected] { white-space: nowrap; }
  .small { display: block; font-size: 13px; }
  .empty { padding: var(--hh-s-24) var(--hh-s-8); }
  .empty strong { display: block; font-size: 18px; margin-bottom: var(--hh-s-4); }
  .foot { margin: var(--hh-s-12) 0 0; font-size: 14px; }
  .nodate { display: inline-block; padding: 0 var(--hh-s-8); border: 1px dashed var(--hh-state-watch); border-radius: var(--hh-radius-pill); color: var(--hh-state-watch); font-size: 13px; font-weight: 700; }

  /* The pick box: a button that is a checkbox, so the position gate has nothing to bind. */
  .box { width: 28px; height: 28px; min-height: 28px; padding: 0; border-radius: var(--hh-radius-control); border: 2px solid var(--hh-control-line); background: var(--hh-field); position: relative; }
  .box[aria-checked="true"] { background: var(--brand-fill, var(--hh-accent)); border-color: var(--brand-fill-on, var(--hh-accent-ink)); }
  .box[aria-checked="true"]::after { content: ""; position: absolute; inset-inline-start: 8px; top: 3px; width: 6px; height: 12px; border: solid var(--brand-fill-on, var(--hh-accent-ink)); border-width: 0 3px 3px 0; transform: rotate(45deg); }
  .box:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: 2px; }

  /* Status words: outline pills, never buttons. */
  .status { display: inline-flex; align-items: center; gap: var(--hh-s-8); padding: var(--hh-s-2) var(--hh-s-12); border-radius: var(--hh-radius-pill); font-size: 12px; font-weight: 700; letter-spacing: 0.06em; text-transform: uppercase; border: 1px solid var(--hh-line); white-space: nowrap; }
  .status-stop { color: var(--hh-state-stop); border-color: var(--hh-state-stop); }
  .status-watch { color: var(--hh-state-watch); border-color: var(--hh-state-watch); }
  .status-good { color: var(--hh-state-good); border-color: var(--hh-state-good); }
  .status-plain { color: var(--hh-text-dim); }

  /* The draft. */
  dl { margin: 0; }
  .facts { display: grid; grid-template-columns: max-content minmax(0, 1fr); gap: var(--hh-s-4) var(--hh-s-24); margin-bottom: var(--hh-s-12); }
  .facts dt { color: var(--hh-text-dim); }
  .facts dd { margin: 0; overflow-wrap: anywhere; }
  .forms { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: var(--hh-s-16); }
  label { display: grid; gap: var(--hh-s-4); font-size: 14px; color: var(--hh-text-dim); font-weight: 600; }
  input, select { font: inherit; font-size: 16px; min-height: 44px; padding: 0 var(--hh-s-12); background: var(--hh-field); color: var(--hh-text); border: 1px solid var(--hh-control-line); border-radius: var(--hh-radius-control); width: 100%; box-sizing: border-box; }
  input:focus-visible, select:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: 2px; }
  .sub { border-top: 1px solid var(--hh-line); margin-top: var(--hh-s-16); padding-top: var(--hh-s-16); }
  .actions { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); align-items: center; margin-top: var(--hh-s-16); }
  .actions.end { justify-content: flex-end; }
  .btn { white-space: nowrap; }
  .small-btn { min-height: 36px; padding: 0 var(--hh-s-12); font-size: 14px; }

  .mob { display: none; }
  @media (max-width: 1500px) {
    .opt1 { display: none; }
  }
  @media (max-width: 1180px) {
    .home { grid-template-columns: minmax(0, 1fr); }
  }
  @media (max-width: 900px) {
    .tiles { grid-template-columns: repeat(2, minmax(0, 1fr)); }
    .forms { grid-template-columns: minmax(0, 1fr); }
  }
  @media (max-width: 700px) {
    .opt2 { display: none; }
    .mob { display: inline; }
  }
  @media (max-width: 520px) {
    .desk { padding: var(--hh-s-16) var(--hh-s-12) var(--hh-s-40); }
    .tiles { grid-template-columns: minmax(0, 1fr); }
  }
`;
