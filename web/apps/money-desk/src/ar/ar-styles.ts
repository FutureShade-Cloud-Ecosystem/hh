// The receivables slice's own styles. Every value is a token of the design
// system; nothing here is a literal colour. Status is a word before it is a
// colour, figures sit in tabular columns and line up on the right, and a wide
// table scrolls inside its own card so the page never scrolls sideways.
import { css } from "lit";

export const arStyles = css`
  .desk { max-width: var(--hh-content-max, 1180px); margin: 0 auto; padding: var(--hh-s-24) var(--hh-s-24) var(--hh-s-48); }
  h1 { margin: 0; font-size: 28px; line-height: 1.2; font-weight: 700; }
  h2 { margin: 0; font-size: 20px; line-height: 1.25; font-weight: 700; }
  h3 { margin: 0 0 var(--hh-s-8); font-size: 14px; letter-spacing: 0.08em; text-transform: uppercase; color: var(--hh-accent-text); font-weight: 700; }
  p { margin: 0 0 var(--hh-s-8); }
  .dim { color: var(--hh-text-dim); }
  .head { display: flex; flex-wrap: wrap; align-items: baseline; justify-content: space-between; gap: var(--hh-s-8) var(--hh-s-16); margin-bottom: var(--hh-s-16); }
  .head .asof { color: var(--hh-text-dim); font-family: var(--hh-font-mono); font-size: 14px; }

  .panel { background: var(--hh-card); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); padding: var(--hh-s-20); margin-bottom: var(--hh-s-16); min-width: 0; }
  .panel-head { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: var(--hh-s-8) var(--hh-s-16); margin-bottom: var(--hh-s-12); }
  .panel-head h3 { margin: 0; }

  .alert { border-radius: var(--hh-radius-control); padding: var(--hh-s-12) var(--hh-s-16); margin: 0 0 var(--hh-s-16); border: 1px solid var(--hh-line); background: var(--hh-card); }
  .alert-error { border-color: var(--hh-state-stop); color: var(--hh-text); border-inline-start-width: 4px; }
  .alert-ok { border-color: var(--hh-state-good); color: var(--hh-text); border-inline-start-width: 4px; }

  /* The four aging cards, worst first. */
  .agecards { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: var(--hh-s-12); }
  .agecard { text-align: start; display: block; width: 100%; min-height: 0; padding: var(--hh-s-16); background: var(--hh-widget, var(--hh-card)); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-widget, var(--hh-radius-card)); color: var(--hh-text); }
  .agecard:hover { border-color: var(--hh-control-line); }
  .agecard[aria-pressed="true"] { border-color: var(--hh-focus-ring); border-width: 2px; padding: calc(var(--hh-s-16) - 1px); }
  .agecard .kicker { display: block; font-size: 12px; letter-spacing: 0.1em; text-transform: uppercase; color: var(--hh-text-dim); font-weight: 700; }
  .agecard .figure { display: block; font-size: 24px; font-weight: 700; margin: var(--hh-s-8) 0 var(--hh-s-4); }
  .agecard .figure.stop { color: var(--hh-state-stop); }
  .agecard .small { display: block; font-size: 14px; color: var(--hh-text-dim); }

  /* Tabs. */
  .tabs { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); margin: var(--hh-s-16) 0; }
  .tab { min-height: 44px; border-radius: var(--hh-radius-pill); background: transparent; color: var(--hh-text); border: 1px solid var(--hh-control-line); padding: 0 var(--hh-s-20); }
  .tab[aria-current="true"] { background: var(--brand-fill, var(--hh-accent)); color: var(--brand-fill-on, var(--hh-accent-ink)); border-color: var(--brand-fill-on, var(--hh-accent-ink)); font-weight: 700; }
  .count { font-variant-numeric: tabular-nums; margin-inline-start: var(--hh-s-8); }

  /* Tables. */
  .scroll { overflow-x: auto; }
  table { width: 100%; border-collapse: collapse; font-size: 15px; }
  th { text-align: start; font-weight: 700; padding: var(--hh-s-8) var(--hh-s-12); border-bottom: 1px solid var(--hh-line); color: var(--hh-text-dim); white-space: nowrap; }
  td { padding: var(--hh-s-12); border-bottom: 1px solid var(--hh-line); vertical-align: middle; }
  tr:last-child td { border-bottom: 0; }
  th.num, td.num { text-align: end; font-variant-numeric: tabular-nums; white-space: nowrap; }
  td.stop { color: var(--hh-state-stop); font-weight: 700; }
  tr[aria-current="true"] td { background: var(--hh-row-alt); }
  .who { display: block; font-weight: 600; }
  .acct { display: inline-block; margin-top: var(--hh-s-4); padding: 0 var(--hh-s-8); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-pill); font-size: 12px; color: var(--hh-text-dim); font-family: var(--hh-font-mono); }
  .empty { padding: var(--hh-s-24) var(--hh-s-8); }
  .empty strong { display: block; font-size: 18px; margin-bottom: var(--hh-s-4); }

  /* Status words. */
  .status { display: inline-flex; align-items: center; gap: var(--hh-s-8); padding: var(--hh-s-2) var(--hh-s-12); border-radius: var(--hh-radius-pill); font-size: 13px; font-weight: 700; border: 1px solid var(--hh-line); white-space: nowrap; }
  .status-stop { color: var(--hh-state-stop); border-color: var(--hh-state-stop); }
  .status-watch { color: var(--hh-state-watch); border-color: var(--hh-state-watch); }
  .status-good { color: var(--hh-state-good); border-color: var(--hh-state-good); }
  .status-plain { color: var(--hh-text-dim); }
  .status::before { content: ""; width: 8px; height: 8px; border-radius: var(--hh-radius-pill); background: currentColor; }

  /* Account view. */
  .position { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: var(--hh-s-12); margin: var(--hh-s-12) 0 0; }
  .position div { padding: var(--hh-s-12); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-control); }
  .position dt { font-size: 12px; letter-spacing: 0.08em; text-transform: uppercase; color: var(--hh-text-dim); font-weight: 700; }
  .position dd { margin: var(--hh-s-4) 0 0; font-size: 20px; font-weight: 700; font-variant-numeric: tabular-nums; }
  dl { margin: 0; }
  .facts { display: grid; grid-template-columns: max-content minmax(0, 1fr); gap: var(--hh-s-4) var(--hh-s-24); margin-bottom: var(--hh-s-12); }
  .facts dt { color: var(--hh-text-dim); }
  .facts dd { margin: 0; overflow-wrap: anywhere; }
  .forms { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: var(--hh-s-16); }
  form { display: grid; gap: var(--hh-s-12); margin: 0; }
  label { display: grid; gap: var(--hh-s-4); font-size: 14px; color: var(--hh-text-dim); font-weight: 600; }
  input, select, textarea { font: inherit; font-size: 16px; min-height: 44px; padding: 0 var(--hh-s-12); background: var(--hh-field); color: var(--hh-text); border: 1px solid var(--hh-control-line); border-radius: var(--hh-radius-control); width: 100%; box-sizing: border-box; }
  input:focus-visible, select:focus-visible, textarea:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: 2px; }
  .actions { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); align-items: center; }
  .holdlist { list-style: none; margin: 0; padding: 0; }
  .hold { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: var(--hh-s-12) var(--hh-s-24); align-items: end; padding: var(--hh-s-12) 0; border-bottom: 1px solid var(--hh-line); }
  .hold:last-child { border-bottom: 0; }
  .hold-what { display: grid; gap: var(--hh-s-4); justify-items: start; }
  .inline-form { display: grid; grid-template-columns: minmax(0, 1fr) auto auto; gap: var(--hh-s-8); align-items: end; }
  .sub { border-top: 1px solid var(--hh-line); margin-top: var(--hh-s-16); padding-top: var(--hh-s-16); }
  .banner { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-8); padding: var(--hh-s-12) var(--hh-s-16); border: 1px solid var(--hh-state-stop); border-radius: var(--hh-radius-control); margin-top: var(--hh-s-12); }
  .btn { white-space: nowrap; }
  .small-btn { min-height: 36px; padding: 0 var(--hh-s-12); font-size: 14px; }

  @media (max-width: 900px) {
    .agecards, .position { grid-template-columns: repeat(2, minmax(0, 1fr)); }
    .hold { grid-template-columns: minmax(0, 1fr); }
    .forms { grid-template-columns: minmax(0, 1fr); }
  }
  @media (max-width: 520px) {
    .desk { padding: var(--hh-s-16) var(--hh-s-12) var(--hh-s-40); }
    .agecards { grid-template-columns: minmax(0, 1fr); }
    .inline-form { grid-template-columns: minmax(0, 1fr); }
    .hold { grid-template-columns: minmax(0, 1fr); }
  }
`;
